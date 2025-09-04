package tasktestsuiterun

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1alpha1"
	_ "github.com/tektoncd/pipeline/pkg/client/injection/informers/pipeline/v1alpha1/tasktestrun/fake"
	ttesting "github.com/tektoncd/pipeline/pkg/reconciler/testing"
	"github.com/tektoncd/pipeline/test"
	"github.com/tektoncd/pipeline/test/diff"
	"github.com/tektoncd/pipeline/test/names"
	"github.com/tektoncd/pipeline/test/parse"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	clock "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	cminformer "knative.dev/pkg/configmap/informer"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/logging"
	logtesting "knative.dev/pkg/logging/testing"
	pkgreconciler "knative.dev/pkg/reconciler"
	"knative.dev/pkg/system"
	_ "knative.dev/pkg/system/testing" // Setup system.Namespace()
)

var now = time.Date(2022, time.January, 1, 0, 0, 0, 0, time.UTC)
var testClock = clock.NewFakePassiveClock(now)

const dateWorkspace = `
  - name: date-workspace
    emptyDir: {}
`

// IgnoreFields options
var (
	ignoreResourceVersion = cmpopts.IgnoreFields(
		metav1.ObjectMeta{},
		"ResourceVersion",
	)
	ignoreLastTransitionTime = cmpopts.IgnoreFields(
		apis.Condition{},
		"LastTransitionTime.Inner.Time",
	)
	ignoreTaskTestRunStatus = cmpopts.IgnoreFields(
		v1alpha1.TaskTestSuiteRunStatusFields{},
		"TaskTestRunStatuses",
	)
	ignoreStartTimeTaskTestRun = cmpopts.IgnoreFields(
		v1alpha1.TaskTestRunStatusFields{},
		"StartTime",
	)
	ignoreStartTimeTaskTestSuiteRun = cmpopts.IgnoreFields(
		v1alpha1.TaskTestSuiteRunStatusFields{},
		"StartTime",
	)
	ignoreCompletionTimeTaskTestSuiteRun = cmpopts.IgnoreFields(
		v1alpha1.TaskTestSuiteRunStatusFields{},
		"CompletionTime",
	)
)

func TestReconciler_ValidateReconcileKind(t *testing.T) {
	const (
		tcStartNewSequentialTtrInlineTts    = "start-new-sequential-ttr-inline-tts"
		tcStartSecondSequentialTtrInlineTts = "start-second-sequential-ttr-inline-tts"
		tcStartNewParallelTtrsInlineTts     = "start-new-parallel-inline-ttrs-inline-tts"
		tcStartNewTtrsRefTts                = "start-new-ttrs-referenced-tts"
		tcCancelDecTts                      = "cancel-dec-tts"
		tcCheckSuccessDecTts                = "check-success-dec-tts"
		tcCheckSuccessTtrsRefTts            = "check-success-ttrs-ref-tts"
		tcCheckFailTtrsDecTts               = "check-fail-ttrs-dec-tts"
		tcCheckFailTtrsRefTts               = "check-fail-ttrs-ref-tts"
		tcCheckFailTtrsOnErrContRefTts      = "check-fail-ttrs-on-err-cont-ref-tts"
	)

	messageExpectationsNotMet := `{"type":"Succeeded","status":"False","lastTransitionTime":null,"reason":"TaskTestRunUnexpectedOutcomes","message":"not all expectations were met:\n` +
		`observed success status did not match expectation\n` +
		`observed success reason did not match expectation\n"}` + "\n"

	// instantiate custom resources
	taskMap := map[string]*v1.Task{"simple_task": parse.MustParseV1Task(t, tManifest)}
	taskTestMap := map[string]*v1alpha1.TaskTest{"simple_task_test": parse.MustParseTaskTest(t, ttManifest)}
	taskTestSuiteMap := map[string]*v1alpha1.TaskTestSuite{
		"simple_task_test_suite":                  parse.MustParseTaskTestSuite(t, ttsManifestSimpleSuite),
		"simple_task_test_suite_onerror_continue": parse.MustParseTaskTestSuite(t, ttsManifestSimpleSuiteOnErrorContinue),
	}
	ttsrMap := map[string]*v1alpha1.TaskTestSuiteRun{
		tcStartNewParallelTtrsInlineTts:     generateTaskTestSuiteRun(t, ttsrManifestTemplateInlineTts, tcStartNewParallelTtrsInlineTts),
		tcStartNewSequentialTtrInlineTts:    generateTaskTestSuiteRun(t, ttsrManifestTemplateInlineTts, tcStartNewSequentialTtrInlineTts, "Sequential"),
		tcStartSecondSequentialTtrInlineTts: generateTaskTestSuiteRun(t, ttsrManifestTemplateInlineTts, tcStartSecondSequentialTtrInlineTts, "Sequential"),
		tcCancelDecTts:                      generateTaskTestSuiteRun(t, ttsrManifestTemplateInlineTts+"\n  status: TaskTestSuiteRunCancelled", tcCancelDecTts),
		tcCheckSuccessDecTts:                generateTaskTestSuiteRun(t, ttsrManifestTemplateInlineTts, tcCheckSuccessDecTts),
		tcCheckFailTtrsDecTts:               generateTaskTestSuiteRun(t, ttsrManifestTemplateInlineTts, tcCheckFailTtrsDecTts),
		tcStartNewTtrsRefTts:                parse.MustParseTaskTestSuiteRun(t, fmt.Sprintf(ttsrManifestTemplateReferencedTts, tcStartNewTtrsRefTts)),
		tcCheckSuccessTtrsRefTts:            parse.MustParseTaskTestSuiteRun(t, fmt.Sprintf(ttsrManifestTemplateReferencedTts, tcCheckSuccessTtrsRefTts)),
		tcCheckFailTtrsRefTts:               parse.MustParseTaskTestSuiteRun(t, fmt.Sprintf(ttsrManifestTemplateReferencedTts, tcCheckFailTtrsRefTts)),
		tcCheckFailTtrsOnErrContRefTts: parse.MustParseTaskTestSuiteRun(
			t, fmt.Sprintf(
				strings.ReplaceAll(ttsrManifestTemplateReferencedTts, "    name: suite", "    name: suite-onerror-continue"),
				tcCheckFailTtrsOnErrContRefTts,
			)),
	}
	taskTestRunMap := map[string]*v1alpha1.TaskTestRun{
		tcCancelDecTts + "-0":                      generateTaskTestRun(t, ttrManifestTemplateSpec, tcCancelDecTts, "task-0"),
		tcCancelDecTts + "-1":                      generateTaskTestRun(t, addWorkspace(ttrManifestTemplateSpec, dateWorkspace), tcCancelDecTts, "task-1"),
		tcStartSecondSequentialTtrInlineTts + "-0": generateTaskTestRun(t, ttrTemplateCompletedSuccess, tcStartSecondSequentialTtrInlineTts, "task-0"),
		tcCheckSuccessDecTts + "-0":                generateTaskTestRun(t, ttrTemplateCompletedSuccess, tcCheckSuccessDecTts, "task-0"),
		tcCheckSuccessDecTts + "-1":                generateTaskTestRun(t, addWorkspace(ttrTemplateCompletedSuccess, dateWorkspace), tcCheckSuccessDecTts, "task-1"),
		tcCheckSuccessTtrsRefTts + "-0":            generateTaskTestRun(t, ttrTemplateCompletedSuccess, tcCheckSuccessTtrsRefTts, "task-0"),
		tcCheckSuccessTtrsRefTts + "-1":            generateTaskTestRun(t, addWorkspace(ttrTemplateCompletedSuccess, dateWorkspace), tcCheckSuccessTtrsRefTts, "task-1"),
		tcCheckFailTtrsDecTts + "-0":               generateTaskTestRun(t, ttrTemplateCompletedSuccess, tcCheckFailTtrsDecTts, "task-0"),
		tcCheckFailTtrsDecTts + "-1":               generateTaskTestRun(t, addWorkspace(ttrTemplateCompletedFail, dateWorkspace), tcCheckFailTtrsDecTts, "task-1"),
		tcCheckFailTtrsRefTts + "-0":               generateTaskTestRun(t, ttrTemplateCompletedSuccess, tcCheckFailTtrsRefTts, "task-0"),
		tcCheckFailTtrsRefTts + "-1":               generateTaskTestRun(t, addWorkspace(ttrTemplateCompletedFail, dateWorkspace), tcCheckFailTtrsRefTts, "task-1"),
		tcCheckFailTtrsOnErrContRefTts + "-0":      generateTaskTestRun(t, ttrTemplateCompletedSuccess, tcCheckFailTtrsOnErrContRefTts, "task-0"),
		tcCheckFailTtrsOnErrContRefTts + "-1":      generateTaskTestRun(t, addWorkspace(ttrTemplateCompletedSuccess, dateWorkspace), tcCheckFailTtrsOnErrContRefTts, "task-1"),
	}

	// load custom resources into data for the fake cluster
	data := test.Data{
		Tasks:             slices.Collect(maps.Values(taskMap)),
		TaskRuns:          []*v1.TaskRun{},
		TaskTests:         slices.Collect(maps.Values(taskTestMap)),
		TaskTestRuns:      slices.Collect(maps.Values(taskTestRunMap)),
		TaskTestSuites:    slices.Collect(maps.Values(taskTestSuiteMap)),
		TaskTestSuiteRuns: slices.Collect(maps.Values(ttsrMap)),
	}

	type tc struct {
		ttsr                        *v1alpha1.TaskTestSuiteRun
		wantTtsrStatus              *v1alpha1.TaskTestSuiteRunStatus
		wantTtrs                    []v1alpha1.TaskTestRun
		wantStartTimeSuiteRun       bool
		wantCompletionTimeSuiteRun  bool
		wantStartTimesTestRuns      []bool
		wantCompletionTimesTestRuns []bool
	}
	tests := map[string]tc{
		tcStartNewParallelTtrsInlineTts: {
			wantTtsrStatus: patchTaskTestSuiteRun(
				ttsrMap[tcStartNewParallelTtrsInlineTts],
				func(ttsr *v1alpha1.TaskTestSuiteRun) {
					ttsr.Status.Conditions = duckv1.Conditions{{
						Type:   "Succeeded",
						Status: "Unknown",
						Reason: "Started",
					}}
					ttsr.Status.TaskTestSuiteSpec = ttsr.Spec.TaskTestSuiteSpec.DeepCopy()
					ttsr.Status.TaskTestSuiteSpec.TaskTests[0].TaskTestSpec = &v1alpha1.TaskTestSpec{
						TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
						Expects: &v1alpha1.ExpectedOutcomes{
							SuccessStatus: ptr.To(true),
							SuccessReason: ptr.To(v1.TaskRunReason("Succeeded")),
						},
					}
				},
			),
			wantTtrs: []v1alpha1.TaskTestRun{
				*generateTaskTestRun(t, ttrManifestTemplateSpec, tcStartNewParallelTtrsInlineTts, "task-0"),
				*generateTaskTestRun(t, addWorkspace(ttrManifestTemplateSpec, dateWorkspace), tcStartNewParallelTtrsInlineTts, "task-1"),
			},
			wantStartTimeSuiteRun: true,
		},
		tcStartNewSequentialTtrInlineTts: {
			wantTtsrStatus: patchTaskTestSuiteRun(
				ttsrMap[tcStartNewSequentialTtrInlineTts],
				func(ttsr *v1alpha1.TaskTestSuiteRun) {
					ttsr.Status.Conditions = duckv1.Conditions{{
						Type:   "Succeeded",
						Status: "Unknown",
						Reason: "Started",
					}}
					ttsr.Status.CurrentSuiteTest = ptr.To("task-0")
					ttsr.Status.TaskTestSuiteSpec = ttsr.Spec.TaskTestSuiteSpec.DeepCopy()
					ttsr.Status.TaskTestSuiteSpec.TaskTests[0].TaskTestSpec = &v1alpha1.TaskTestSpec{
						TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
						Expects: &v1alpha1.ExpectedOutcomes{
							SuccessStatus: ptr.To(true),
							SuccessReason: ptr.To(v1.TaskRunReason("Succeeded")),
						},
					}
				},
			),
			wantTtrs: []v1alpha1.TaskTestRun{
				*generateTaskTestRun(t, ttrManifestTemplateSpec, tcStartNewSequentialTtrInlineTts, "task-0"),
			},
			wantStartTimeSuiteRun: true,
		},
		tcStartSecondSequentialTtrInlineTts: {
			wantTtsrStatus: patchTaskTestSuiteRun(
				ttsrMap[tcStartSecondSequentialTtrInlineTts],
				func(ttsr *v1alpha1.TaskTestSuiteRun) {
					ttsr.Status.Conditions = duckv1.Conditions{{
						Type:   "Succeeded",
						Status: "Unknown",
						Reason: "Started",
					}}
					ttsr.Status.CurrentSuiteTest = ptr.To("task-1")
					ttsr.Status.TaskTestSuiteSpec = ttsr.Spec.TaskTestSuiteSpec.DeepCopy()
					ttsr.Status.TaskTestSuiteSpec.TaskTests[0].TaskTestSpec = &v1alpha1.TaskTestSpec{
						TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
						Expects: &v1alpha1.ExpectedOutcomes{
							SuccessStatus: ptr.To(true),
							SuccessReason: ptr.To(v1.TaskRunReason("Succeeded")),
						},
					}
				},
			),
			wantTtrs: []v1alpha1.TaskTestRun{
				*taskTestRunMap[tcStartSecondSequentialTtrInlineTts+"-0"],
				*generateTaskTestRun(t, addWorkspace(ttrManifestTemplateSpec, dateWorkspace), tcStartSecondSequentialTtrInlineTts, "task-1"),
			},
			wantStartTimeSuiteRun:       true,
			wantCompletionTimesTestRuns: []bool{true, false},
		},
		tcStartNewTtrsRefTts: {
			wantTtsrStatus: patchTaskTestSuiteRun(
				ttsrMap[tcStartNewTtrsRefTts],
				func(ttsr *v1alpha1.TaskTestSuiteRun) {
					ttsr.Status.Conditions = duckv1.Conditions{{
						Type:   "Succeeded",
						Status: "Unknown",
						Reason: "Started",
					}}
					ttsr.Status.TaskTestSuiteName = ptr.To("suite")
					ttsr.Status.TaskTestSuiteSpec = taskTestSuiteMap["simple_task_test_suite"].Spec.DeepCopy()
					ttsr.Status.TaskTestSuiteSpec.TaskTests[0].TaskTestSpec = &v1alpha1.TaskTestSpec{
						TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
						Expects: &v1alpha1.ExpectedOutcomes{
							SuccessStatus: ptr.To(true),
							SuccessReason: ptr.To(v1.TaskRunReason("Succeeded")),
						},
					}
				},
			),
			wantTtrs: []v1alpha1.TaskTestRun{
				*generateTaskTestRun(t, ttrManifestTemplateSpec, tcStartNewTtrsRefTts, "task-0"),
				*generateTaskTestRun(t, addWorkspace(ttrManifestTemplateSpec, dateWorkspace), tcStartNewTtrsRefTts, "task-1"),
			},
			wantStartTimeSuiteRun: true,
		},
		tcCancelDecTts: {
			wantTtsrStatus: patchTaskTestSuiteRun(ttsrMap[tcCancelDecTts], func(ttsr *v1alpha1.TaskTestSuiteRun) {
				ttsr.Status.Conditions = duckv1.Conditions{{
					Type:    "Succeeded",
					Status:  "False",
					Reason:  "TaskTestSuiteRunCancelled",
					Message: `TaskTestSuiteRun "` + tcCancelDecTts + `" was cancelled. `,
				}}
				ttsr.Status.CompletionTime = &metav1.Time{Time: testClock.Now()}
				ttsr.Spec.TaskTestSuiteSpec.TaskTests[0].TaskTestSpec = &taskTestMap["simple_task_test"].Spec
				ttsr.Status.TaskTestSuiteSpec = ttsr.Spec.TaskTestSuiteSpec
			}),
			wantTtrs: []v1alpha1.TaskTestRun{
				*generateTaskTestRun(t, ttrManifestTemplateSpec, tcCancelDecTts, "task-0",
					"0", "false", fmt.Sprintf(ttrSpecCancelled, "TaskTestRun cancelled as the TaskTestSuiteRun it belongs to has been cancelled.")),
				*generateTaskTestRun(t, addWorkspace(ttrManifestTemplateSpec, dateWorkspace), tcCancelDecTts, "task-1",
					"0", "false", fmt.Sprintf(ttrSpecCancelled, "TaskTestRun cancelled as the TaskTestSuiteRun it belongs to has been cancelled.")),
			},
			wantStartTimeSuiteRun:       true,
			wantCompletionTimeSuiteRun:  true,
			wantCompletionTimesTestRuns: []bool{false, false},
		},
		tcCheckSuccessDecTts: {
			wantTtsrStatus: patchTaskTestSuiteRun(
				ttsrMap[tcCheckSuccessDecTts],
				func(ttsr *v1alpha1.TaskTestSuiteRun) {
					ttsr.Status.Conditions = duckv1.Conditions{{
						Type:    "Succeeded",
						Status:  "True",
						Reason:  "Succeeded",
						Message: "all TaskTestRuns completed executing and were successful",
					}}
					ttsr.Status.TaskTestSuiteSpec = ttsr.Spec.TaskTestSuiteSpec.DeepCopy()
					ttsr.Status.TaskTestSuiteSpec.TaskTests[0].TaskTestSpec = &v1alpha1.TaskTestSpec{
						TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
						Expects: &v1alpha1.ExpectedOutcomes{
							SuccessStatus: ptr.To(true),
							SuccessReason: ptr.To(v1.TaskRunReason("Succeeded")),
						},
					}
				},
			),
			wantTtrs: []v1alpha1.TaskTestRun{
				*taskTestRunMap[tcCheckSuccessDecTts+"-0"],
				*taskTestRunMap[tcCheckSuccessDecTts+"-1"],
			},
			wantStartTimeSuiteRun:      true,
			wantCompletionTimeSuiteRun: true,
		},
		tcCheckSuccessTtrsRefTts: {
			wantTtsrStatus: patchTaskTestSuiteRun(
				ttsrMap[tcCheckSuccessTtrsRefTts],
				func(ttsr *v1alpha1.TaskTestSuiteRun) {
					ttsr.Status.Conditions = duckv1.Conditions{{
						Type:    "Succeeded",
						Status:  "True",
						Reason:  "Succeeded",
						Message: "all TaskTestRuns completed executing and were successful",
					}}
					ttsr.Status.TaskTestSuiteName = ptr.To("suite")
					ttsr.Status.TaskTestSuiteSpec = taskTestSuiteMap["simple_task_test_suite"].Spec.DeepCopy()
					ttsr.Status.TaskTestSuiteSpec.TaskTests[0].TaskTestSpec = &v1alpha1.TaskTestSpec{
						TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
						Expects: &v1alpha1.ExpectedOutcomes{
							SuccessStatus: ptr.To(true),
							SuccessReason: ptr.To(v1.TaskRunReason("Succeeded")),
						},
					}
				},
			),
			wantTtrs: []v1alpha1.TaskTestRun{
				*taskTestRunMap[tcCheckSuccessTtrsRefTts+"-0"],
				*taskTestRunMap[tcCheckSuccessTtrsRefTts+"-1"],
			},
			wantStartTimeSuiteRun:      true,
			wantCompletionTimeSuiteRun: true,
		},
		tcCheckFailTtrsDecTts: {
			wantTtsrStatus: patchTaskTestSuiteRun(
				ttsrMap[tcCheckFailTtrsDecTts],
				func(ttsr *v1alpha1.TaskTestSuiteRun) {
					ttsr.Status.TaskTestSuiteSpec = ttsr.Spec.TaskTestSuiteSpec.DeepCopy()
					ttsr.Status.TaskTestSuiteSpec.TaskTests[0].TaskTestSpec = &v1alpha1.TaskTestSpec{
						TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
						Expects: &v1alpha1.ExpectedOutcomes{
							SuccessStatus: ptr.To(true),
							SuccessReason: ptr.To(v1.TaskRunReason("Succeeded")),
						},
					}
					ttsr.Status.Conditions = duckv1.Conditions{{
						Type:   "Succeeded",
						Status: "False",
						Reason: "TaskTestSuiteRunUnexpectedOutcomes",
						Message: "all TaskTestRuns completed executing and not all were successful: " +
							ttsr.Status.TaskTestSuiteSpec.TaskTests[1].GetTaskTestRunName(
								ttsr.Name,
							) + ": " +
							messageExpectationsNotMet,
					}}
				},
			),
			wantTtrs: []v1alpha1.TaskTestRun{
				*taskTestRunMap[tcCheckFailTtrsDecTts+"-0"],
				*taskTestRunMap[tcCheckFailTtrsDecTts+"-1"],
			},
			wantStartTimeSuiteRun:      true,
			wantCompletionTimeSuiteRun: true,
		},
		tcCheckFailTtrsRefTts: {
			wantTtsrStatus: patchTaskTestSuiteRun(
				ttsrMap[tcCheckFailTtrsRefTts],
				func(ttsr *v1alpha1.TaskTestSuiteRun) {
					ttsr.Status.TaskTestSuiteName = ptr.To("suite")
					ttsr.Status.TaskTestSuiteSpec = taskTestSuiteMap["simple_task_test_suite"].Spec.DeepCopy()
					ttsr.Status.TaskTestSuiteSpec.TaskTests[0].TaskTestSpec = &v1alpha1.TaskTestSpec{
						TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
						Expects: &v1alpha1.ExpectedOutcomes{
							SuccessStatus: ptr.To(true),
							SuccessReason: ptr.To(v1.TaskRunReason("Succeeded")),
						},
					}
					ttsr.Status.Conditions = duckv1.Conditions{{
						Type:   "Succeeded",
						Status: "False",
						Reason: "TaskTestSuiteRunUnexpectedOutcomes",
						Message: "all TaskTestRuns completed executing and not all were successful: " +
							ttsr.Status.TaskTestSuiteSpec.TaskTests[1].GetTaskTestRunName(
								ttsr.Name,
							) + ": " +
							messageExpectationsNotMet,
					}}
				},
			),
			wantTtrs: []v1alpha1.TaskTestRun{
				*taskTestRunMap[tcCheckFailTtrsRefTts+"-0"],
				*taskTestRunMap[tcCheckFailTtrsRefTts+"-1"],
			},
			wantStartTimeSuiteRun:      true,
			wantCompletionTimeSuiteRun: true,
		},
		tcCheckFailTtrsOnErrContRefTts: {
			wantTtsrStatus: patchTaskTestSuiteRun(
				ttsrMap[tcCheckFailTtrsOnErrContRefTts],
				func(ttsr *v1alpha1.TaskTestSuiteRun) {
					ttsr.Status.TaskTestSuiteName = ptr.To("suite-onerror-continue")
					ttsr.Status.TaskTestSuiteSpec = taskTestSuiteMap["simple_task_test_suite"].Spec.DeepCopy()
					ttsr.Status.TaskTestSuiteSpec.TaskTests[0].TaskTestSpec = &v1alpha1.TaskTestSpec{
						TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
						Expects: &v1alpha1.ExpectedOutcomes{
							SuccessStatus: ptr.To(true),
							SuccessReason: ptr.To(v1.TaskRunReason("Succeeded")),
						},
					}
					ttsr.Status.TaskTestSuiteSpec.TaskTests[1].OnError = "Continue"
					ttsr.Status.Conditions = duckv1.Conditions{{
						Type:    "Succeeded",
						Status:  "True",
						Reason:  "Succeeded",
						Message: "all TaskTestRuns completed executing and were successful",
					}}
				},
			),
			wantTtrs: []v1alpha1.TaskTestRun{
				*taskTestRunMap[tcCheckFailTtrsOnErrContRefTts+"-0"],
				*taskTestRunMap[tcCheckFailTtrsOnErrContRefTts+"-1"],
			},
			wantStartTimeSuiteRun:      true,
			wantCompletionTimeSuiteRun: true,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			testAssets, cancel := getTaskTestSuiteRunController(t, data)
			clients := testAssets.Clients

			if tt.ttsr == nil {
				tt.ttsr = ttsrMap[name]
			}

			if tt.wantStartTimesTestRuns == nil {
				tt.wantStartTimesTestRuns = []bool{}
				for range tt.wantTtrs {
					tt.wantStartTimesTestRuns = append(tt.wantStartTimesTestRuns, tt.wantStartTimeSuiteRun)
				}
			}

			if tt.wantCompletionTimesTestRuns == nil {
				tt.wantCompletionTimesTestRuns = []bool{}
				for range tt.wantTtrs {
					tt.wantCompletionTimesTestRuns = append(tt.wantCompletionTimesTestRuns, tt.wantCompletionTimeSuiteRun)
				}
			}

			defer cancel()

			gotErr := testAssets.Controller.Reconciler.Reconcile(
				testAssets.Ctx,
				tt.ttsr.GetNamespacedName().String(),
			)

			if gotErrIsRequeue, _ := controller.IsRequeueKey(gotErr); gotErr != nil &&
				!gotErrIsRequeue {
				t.Errorf("unexpected error: %v", gotErr)
			}

			ttsr, err := clients.Pipeline.TektonV1alpha1().
				TaskTestSuiteRuns(tt.ttsr.Namespace).
				Get(testAssets.Ctx, tt.ttsr.Name, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("getting updated tasktestrun: %v", err)
			}
			if tt.wantStartTimeSuiteRun && ttsr.Status.StartTime == nil {
				t.Error("TaskTestSuiteRun: Didn't expect start time to be nil")
			}
			if !tt.wantStartTimeSuiteRun && ttsr.Status.StartTime != nil {
				t.Error("TaskTestSuiteRun: Expected start time to be nil")
			}
			if tt.wantCompletionTimeSuiteRun && ttsr.Status.CompletionTime == nil {
				t.Error("TaskTestSuiteRun: Didn't expect completion time to be nil")
			}
			if !tt.wantCompletionTimeSuiteRun && ttsr.Status.CompletionTime != nil {
				t.Error("TaskTestSuiteRun: Expected completion time to be nil")
			}
			if d := cmp.Diff(*tt.wantTtsrStatus, ttsr.Status,
				ignoreResourceVersion,
				ignoreLastTransitionTime,
				ignoreTaskTestRunStatus,
				ignoreStartTimeTaskTestSuiteRun,
				ignoreCompletionTimeTaskTestSuiteRun); d != "" {
				t.Errorf("Didn't get expected TaskTestSuiteRun: %v", diff.PrintWantGot(d))
			}

			if ttsr.Status.TaskTestRunStatuses == nil {
				ttsr.Status.TaskTestRunStatuses = map[string]*v1alpha1.TaskTestRunStatus{}
			}

			trl, err := clients.Pipeline.TektonV1alpha1().TaskTestRuns(tt.ttsr.Namespace).
				List(testAssets.Ctx, metav1.ListOptions{LabelSelector: "tekton.dev/taskTestSuiteRun=" + ttsr.Name})
			if err != nil {
				t.Fatalf("error getting updated tasktestruns: %v", err)
			}

			if len(trl.Items) != len(tt.wantTtrs) {
				allTtrs, err := clients.Pipeline.TektonV1alpha1().TaskTestRuns(tt.ttsr.Namespace).
					List(testAssets.Ctx, metav1.ListOptions{})
				if err != nil {
					t.Fatalf("error getting all tasktestruns: %v", err)
				}
				errorMessage := "TaskTestRuns: Didn't get expected number of TaskTestRuns - expected %d but got %d"
				if len(allTtrs.Items) != len(trl.Items) {
					errorMessage += fmt.Sprintf(
						": there probably is an issue with the labels and the filtering, as there are %d TaskTestRuns in the namespace\n%v",
						len(allTtrs.Items),
						allTtrs.Items,
					)
				} else {
					taskTestRunList, _ := json.MarshalIndent(allTtrs.Items, "", "  ")
					errorMessage += fmt.Sprintf(" which is the exact number of TaskTestRuns in the namespace\n%s", taskTestRunList)
				}
				t.Errorf(errorMessage, len(tt.wantTtrs), len(trl.Items))
			}

			slices.SortFunc(trl.Items, taskTestRunSortFunc)
			slices.SortFunc(tt.wantTtrs, taskTestRunSortFunc)
			startTimes := make([]*metav1.Time, len(trl.Items))
			for i, ttr := range trl.Items {
				if tt.wantStartTimesTestRuns[i] && ttr.Status.StartTime == nil {
					t.Errorf("TaskTestRun %d: Didn't expect start time to be nil", i)
				}
				if ttr.Status.StartTime != nil {
					if !tt.wantStartTimesTestRuns[i] {
						t.Errorf("TaskTestRun %d: Expected start time to be nil", i)
					} else {
						startTimes[i] = ttr.Status.StartTime
					}
				}
				if tt.wantCompletionTimesTestRuns[i] && ttr.Status.CompletionTime == nil {
					t.Errorf("TaskTestRun %d: Didn't expect completion time to be nil", i)
				}
				if !tt.wantCompletionTimesTestRuns[i] && ttr.Status.CompletionTime != nil {
					t.Errorf("TaskTestRun %d: Expected completion time to be nil", i)
				}
				if i < len(tt.wantTtrs) {
					if d := cmp.Diff(tt.wantTtrs[i], ttr,
						ignoreResourceVersion,
						ignoreStartTimeTaskTestRun,
						ignoreLastTransitionTime); d != "" {
						t.Errorf("Didn't get expected TaskTestRun %d: %v", i, diff.PrintWantGot(d))
					}
					if ttsr.Status.TaskTestRunStatuses[ttr.Name] == nil {
						t.Errorf("TaskTestRun %d Status not mirrored to TaskTestSuiteRun at all", i)
					} else {
						if d := cmp.Diff(ttr.Status, *ttsr.Status.TaskTestRunStatuses[ttr.Name]); d != "" {
							t.Errorf("TaskTestRun %d Status not mirrored properly to TaskTestSuiteRun: %v", i, diff.PrintWantGot(d))
						}
					}
				}
			}
		})
	}
}

func TestReconciler_InvalidateReconcileKind(t *testing.T) {
	// setup custom resources

	// load custom resources into data for the fake cluster
	data := test.Data{
		Tasks:             []*v1.Task{},
		TaskRuns:          []*v1.TaskRun{},
		TaskTests:         []*v1alpha1.TaskTest{},
		TaskTestRuns:      []*v1alpha1.TaskTestRun{},
		TaskTestSuiteRuns: []*v1alpha1.TaskTestSuiteRun{},
	}

	type tc struct {
		ttr                *v1alpha1.TaskTestSuiteRun
		wantErr            error
		wantTtrStatus      *v1alpha1.TaskTestSuiteRunStatus
		wantCompletionTime bool
	}
	tests := map[string]tc{}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			testAssets, cancel := getTaskTestSuiteRunController(t, data)
			clients := testAssets.Clients
			defer cancel()

			if tt.wantTtrStatus == nil {
				tt.wantTtrStatus = &tt.ttr.Status
			}

			gotErr := testAssets.Controller.Reconciler.Reconcile(
				testAssets.Ctx,
				tt.ttr.GetNamespacedName().String(),
			)

			if gotErr == nil {
				if tt.wantErr != nil {
					t.Errorf("expected error but got none")
				}
			} else {
				if tt.wantErr == nil {
					t.Errorf("unexpected error: %v", gotErr)
				} else {
					if d := cmp.Diff(tt.wantErr.Error(), gotErr.Error()); d != "" {
						t.Errorf("Didn't get expected error: %v", diff.PrintWantGot(d))
					}
				}
			}

			ttr, err := clients.Pipeline.TektonV1alpha1().
				TaskTestSuiteRuns(tt.ttr.Namespace).
				Get(testAssets.Ctx, tt.ttr.Name, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("getting updated tasktestrun: %v", err)
			}

			if tt.wantCompletionTime && ttr.Status.CompletionTime == nil {
				t.Error("TaskTestSuiteRun: Didn't expect completion time to be nil")
			}
			if !tt.wantCompletionTime && ttr.Status.CompletionTime != nil {
				t.Error("TaskTestSuiteRun: Expected completion time to be nil")
			}

			if d := cmp.Diff(*tt.wantTtrStatus, ttr.Status,
				ignoreTaskTestRunStatus,
				ignoreResourceVersion,
				ignoreLastTransitionTime,
				ignoreStartTimeTaskTestSuiteRun,
				ignoreCompletionTimeTaskTestSuiteRun); d != "" {
				t.Errorf("Did not get expected TaskTestSuiteRun status: %v", diff.PrintWantGot(d))
			}
		})
	}
}

// getTaskTestSuiteRunController returns an instance of the TaskTestSuiteRun controller/reconciler that has been seeded with
// d, where d represents the state of the system (existing resources) needed for the test.
func getTaskTestSuiteRunController(t *testing.T, d test.Data) (test.Assets, func()) {
	t.Helper()
	names.TestingSeed()
	return initializeTaskTestSuiteRunControllerAssets(
		t, d, pipeline.Options{Images: pipeline.Images{}},
	)
}

func initializeTaskTestSuiteRunControllerAssets(
	t *testing.T, d test.Data,
	opts pipeline.Options,
) (test.Assets, func()) {
	t.Helper()
	ctx, _ := ttesting.SetupFakeContext(t)
	loggingConfig := logtesting.TestLogger(t).
		Desugar().
		WithOptions(zap.IncreaseLevel(zap.InfoLevel))
	ctx = logging.WithLogger(ctx, loggingConfig.Sugar())
	ctx = ttesting.SetupFakeCloudClientContext(ctx, d.ExpectedCloudEventCount)
	ctx, cancel := context.WithCancel(ctx)
	test.EnsureConfigurationConfigMapsExist(&d)
	c, informers := test.SeedTestData(t, ctx, d)
	configMapWatcher := cminformer.NewInformedWatcher(c.Kube, system.Namespace())
	ctl := NewController(&opts, testClock)(ctx, configMapWatcher)
	if err := configMapWatcher.Start(ctx.Done()); err != nil {
		t.Fatalf("error starting configmap watcher: %v", err)
	}

	if la, ok := ctl.Reconciler.(pkgreconciler.LeaderAware); ok {
		la.Promote(
			pkgreconciler.UniversalBucket(),
			func(pkgreconciler.Bucket, types.NamespacedName) {},
		)
	}

	return test.Assets{
		Logger:     logging.FromContext(ctx),
		Controller: ctl,
		Clients:    c,
		Informers:  informers,
		Recorder:   controller.GetEventRecorder(ctx).(*record.FakeRecorder),
		Ctx:        ctx,
	}, cancel
}

type statusPatchFunc = func(*v1alpha1.TaskTestSuiteRun)

func patchTaskTestSuiteRun(ttrs *v1alpha1.TaskTestSuiteRun, pf statusPatchFunc) *v1alpha1.TaskTestSuiteRunStatus {
	ttrsCopy := ttrs.DeepCopy()
	pf(ttrsCopy)
	return &ttrsCopy.Status
}

func taskTestRunSortFunc(a, b v1alpha1.TaskTestRun) int {
	if a.Status.StartTime != nil && b.Status.StartTime != nil {
		return a.Status.StartTime.Time.Compare(b.Status.StartTime.Time)
	}
	return strings.Compare(a.GetNamespacedName().String(), b.GetNamespacedName().String())
}

func generateTaskTestRun(t *testing.T, yaml, suiteRunName, suiteTaskName string, optionalArgs ...string) *v1alpha1.TaskTestRun {
	t.Helper()

	if len(optionalArgs) > 3 {
		panic("only three optional args allowed for this function")
	}
	if len(optionalArgs) < 3 {
		optionalArgs = append(optionalArgs, "", "", "")
	}

	if optionalArgs[0] == "" {
		optionalArgs[0] = "0"
	}

	if optionalArgs[1] == "" {
		optionalArgs[1] = "false"
	}

	if optionalArgs[2] != "" {
		yaml = strings.Replace(yaml, "spec:", "spec:\n"+optionalArgs[2], 1)
	}

	result := parse.MustParseTaskTestRun(
		t, fmt.Sprintf(yaml, suiteRunName, suiteTaskName, suiteRunName, suiteTaskName,
			suiteRunName, optionalArgs[0], optionalArgs[1]),
	)
	result.SetDefaults(t.Context())
	return result
}

func generateTaskTestSuiteRun(
	t *testing.T, yaml, suiteRunName string, optionalArgs ...string,
) *v1alpha1.TaskTestSuiteRun {
	t.Helper()

	if len(optionalArgs) > 5 {
		panic("only five optional args allowed for this function")
	}
	if len(optionalArgs) < 5 {
		optionalArgs = append(optionalArgs, "", "", "", "", "")
	}

	if optionalArgs[0] == "" {
		optionalArgs[0] = "Parallel"
	}
	if optionalArgs[1] == "" {
		optionalArgs[1] = "0"
	}

	if optionalArgs[2] == "" {
		optionalArgs[2] = "false"
	}
	if optionalArgs[3] == "" {
		optionalArgs[3] = "0"
	}

	if optionalArgs[4] == "" {
		optionalArgs[4] = "false"
	}

	result := parse.MustParseTaskTestSuiteRun(
		t, fmt.Sprintf(yaml, suiteRunName, optionalArgs[0], optionalArgs[1], optionalArgs[2], optionalArgs[3], optionalArgs[4]),
	)
	result.SetDefaults(t.Context())
	return result
}

func addWorkspace(manifest, workspace string) string {
	result := strings.Replace(manifest, `  workspaces:
  - name: time-workspace
    emptyDir: {}
`, `
  workspaces:
  - name: time-workspace
    emptyDir: {}
`+workspace, 1)
	return result
}
