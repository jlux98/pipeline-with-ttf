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

// IgnoreFields options
var (
	ignoreResourceVersion                = cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ResourceVersion")
	ignoreLastTransitionTime             = cmpopts.IgnoreFields(apis.Condition{}, "LastTransitionTime.Inner.Time")
	ignoreTaskTestRunStatus              = cmpopts.IgnoreFields(v1alpha1.TaskTestSuiteRunStatusFields{}, "TaskTestRunStatuses")
	ignoreStartTimeTaskTestRun           = cmpopts.IgnoreFields(v1alpha1.TaskTestRunStatusFields{}, "StartTime")
	ignoreStartTimeTaskTestSuiteRun      = cmpopts.IgnoreFields(v1alpha1.TaskTestSuiteRunStatusFields{}, "StartTime")
	ignoreCompletionTimeTaskTestSuiteRun = cmpopts.IgnoreFields(v1alpha1.TaskTestSuiteRunStatusFields{}, "CompletionTime")
)

func TestReconciler_ValidateReconcileKind(t *testing.T) {
	const (
		tcStartNewInlineTtrsInlineTts      = "start_new_ttrs_inline_tts"
		tcStartNewTtrsReferencedTts        = "start_new_ttrs_referenced_tts"
		tcCheckSuccessfulTtrsInlineTts     = "check_successful_ttrs_inline_tts"
		tcCheckSuccessfulTtrsReferencedTts = "check_successful_ttrs_referenced_tts"
		tcCheckFailedTtrsInlineTts         = "check_failed_ttrs_inline_tts"
		tcCheckFailedTtrsReferencedTts     = "check_failed_ttrs_referenced_tts"
	)

	messageExpectationsNotMet := `{"type":"Succeeded","status":"False","lastTransitionTime":null,"reason":"TaskTestRunUnexpectedOutcomes","message":"not all expectations were met:\n` +
		`observed success status did not match expectation\n` +
		`observed success reason did not match expectation\n"}` + "\n"

	// instantiate custom resources
	taskMap := map[string]*v1.Task{
		"simple_task": parse.MustParseV1Task(t, tManifest),
	}
	taskTestMap := map[string]*v1alpha1.TaskTest{
		"simple_task_test": parse.MustParseTaskTest(t, ttManifest),
	}

	taskTestSuiteMap := map[string]*v1alpha1.TaskTestSuite{
		"simple_task_test_suite": parse.MustParseTaskTestSuite(t, ttsManifestSimpleSuite),
	}
	taskTestSuiteRunMap := map[string]*v1alpha1.TaskTestSuiteRun{
		tcStartNewInlineTtrsInlineTts:      parse.MustParseTaskTestSuiteRun(t, fmt.Sprintf(ttsrManifestInlineTts, "ttsr-start-new-runs-inline-tts")),
		tcStartNewTtrsReferencedTts:        parse.MustParseTaskTestSuiteRun(t, fmt.Sprintf(ttsrManifestReferencedTts, "ttsr-start-new-runs-referenced-tts")),
		tcCheckSuccessfulTtrsInlineTts:     parse.MustParseTaskTestSuiteRun(t, fmt.Sprintf(ttsrManifestInlineTts, "ttsr-check-successful-runs-inline-tts")),
		tcCheckSuccessfulTtrsReferencedTts: parse.MustParseTaskTestSuiteRun(t, fmt.Sprintf(ttsrManifestReferencedTts, "ttsr-check-successful-runs-referenced-tts")),
		tcCheckFailedTtrsInlineTts:         parse.MustParseTaskTestSuiteRun(t, fmt.Sprintf(ttsrManifestInlineTts, "ttsr-check-failed-runs-inline-tts")),
		tcCheckFailedTtrsReferencedTts:     parse.MustParseTaskTestSuiteRun(t, fmt.Sprintf(ttsrManifestReferencedTts, "ttsr-check-failed-runs-referenced-tts")),
	}

	taskTestRunMap := map[string]*v1alpha1.TaskTestRun{
		tcCheckSuccessfulTtrsInlineTts + "-0":     generateTaskTestRun(t, ttrManifestTemplateCompletedSuccessful, taskTestSuiteRunMap[tcCheckSuccessfulTtrsInlineTts].Name, "task-0"),
		tcCheckSuccessfulTtrsInlineTts + "-1":     generateTaskTestRun(t, ttrManifestTemplateCompletedSuccessful, taskTestSuiteRunMap[tcCheckSuccessfulTtrsInlineTts].Name, "task-1"),
		tcCheckSuccessfulTtrsReferencedTts + "-0": generateTaskTestRun(t, ttrManifestTemplateCompletedSuccessful, taskTestSuiteRunMap[tcCheckSuccessfulTtrsReferencedTts].Name, "task-0"),
		tcCheckSuccessfulTtrsReferencedTts + "-1": generateTaskTestRun(t, ttrManifestTemplateCompletedSuccessful, taskTestSuiteRunMap[tcCheckSuccessfulTtrsReferencedTts].Name, "task-1"),
		tcCheckFailedTtrsInlineTts + "-0":         generateTaskTestRun(t, ttrManifestTemplateCompletedSuccessful, taskTestSuiteRunMap[tcCheckFailedTtrsInlineTts].Name, "task-0"),
		tcCheckFailedTtrsInlineTts + "-1":         generateTaskTestRun(t, ttrManifestTemplateCompletedFailed, taskTestSuiteRunMap[tcCheckFailedTtrsInlineTts].Name, "task-1"),
		tcCheckFailedTtrsReferencedTts + "-0":     generateTaskTestRun(t, ttrManifestTemplateCompletedSuccessful, taskTestSuiteRunMap[tcCheckFailedTtrsReferencedTts].Name, "task-0"),
		tcCheckFailedTtrsReferencedTts + "-1":     generateTaskTestRun(t, ttrManifestTemplateCompletedFailed, taskTestSuiteRunMap[tcCheckFailedTtrsReferencedTts].Name, "task-1"),
	}

	// load custom resources into data for the fake cluster
	data := test.Data{
		Tasks:             slices.Collect(maps.Values(taskMap)),
		TaskRuns:          []*v1.TaskRun{},
		TaskTests:         slices.Collect(maps.Values(taskTestMap)),
		TaskTestRuns:      slices.Collect(maps.Values(taskTestRunMap)),
		TaskTestSuites:    slices.Collect(maps.Values(taskTestSuiteMap)),
		TaskTestSuiteRuns: slices.Collect(maps.Values(taskTestSuiteRunMap)),
	}

	type tc struct {
		ttsr               *v1alpha1.TaskTestSuiteRun
		wantTtsrStatus     *v1alpha1.TaskTestSuiteRunStatus
		wantTtrs           []v1alpha1.TaskTestRun
		wantStartTime      bool
		wantCompletionTime bool
	}
	tests := map[string]tc{
		tcStartNewInlineTtrsInlineTts: {
			wantTtsrStatus: patchTaskTestSuiteRun(taskTestSuiteRunMap[tcStartNewInlineTtrsInlineTts], func(ttsr *v1alpha1.TaskTestSuiteRun) {
				ttsr.Status.Conditions = duckv1.Conditions{{
					Type:   "Succeeded",
					Status: "Unknown",
					Reason: "Started",
				}}
				ttsr.Status.TaskTestSuiteSpec = ttsr.Spec.TaskTestSuiteSpec
				ttsr.Status.TaskTestSuiteSpec.TaskTests[0].TaskTestSpec = &v1alpha1.TaskTestSpec{
					TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
					Expects: &v1alpha1.ExpectedOutcomes{
						SuccessStatus: ptr.To(true),
						SuccessReason: ptr.To(v1.TaskRunReason("Succeeded")),
					},
				}
			}),
			wantTtrs: []v1alpha1.TaskTestRun{
				*generateTaskTestRun(t, ttrManifestTemplateNewRun, taskTestSuiteRunMap[tcStartNewInlineTtrsInlineTts].Name, "task-0"),
				*generateTaskTestRun(t, ttrManifestTemplateNewRun, taskTestSuiteRunMap[tcStartNewInlineTtrsInlineTts].Name, "task-1"),
			},
			wantStartTime: true,
		},
		tcStartNewTtrsReferencedTts: {
			wantTtsrStatus: patchTaskTestSuiteRun(taskTestSuiteRunMap[tcStartNewTtrsReferencedTts], func(ttsr *v1alpha1.TaskTestSuiteRun) {
				ttsr.Status.Conditions = duckv1.Conditions{{
					Type:   "Succeeded",
					Status: "Unknown",
					Reason: "Started",
				}}
				ttsr.Status.TaskTestSuiteName = ptr.To("suite")
				ttsr.Status.TaskTestSuiteSpec = &(taskTestSuiteMap["simple_task_test_suite"].Spec)
				ttsr.Status.TaskTestSuiteSpec.TaskTests[0].TaskTestSpec = &v1alpha1.TaskTestSpec{
					TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
					Expects: &v1alpha1.ExpectedOutcomes{
						SuccessStatus: ptr.To(true),
						SuccessReason: ptr.To(v1.TaskRunReason("Succeeded")),
					},
				}
			}),
			wantTtrs: []v1alpha1.TaskTestRun{
				*generateTaskTestRun(t, ttrManifestTemplateNewRun, taskTestSuiteRunMap[tcStartNewTtrsReferencedTts].Name, "task-0"),
				*generateTaskTestRun(t, ttrManifestTemplateNewRun, taskTestSuiteRunMap[tcStartNewTtrsReferencedTts].Name, "task-1"),
			},
			wantStartTime: true,
		},
		tcCheckSuccessfulTtrsInlineTts: {
			wantTtsrStatus: patchTaskTestSuiteRun(taskTestSuiteRunMap[tcCheckSuccessfulTtrsInlineTts], func(ttsr *v1alpha1.TaskTestSuiteRun) {
				ttsr.Status.Conditions = duckv1.Conditions{{
					Type:    "Succeeded",
					Status:  "True",
					Reason:  "Succeeded",
					Message: "all TaskTestRuns completed executing and were successful",
				}}
				ttsr.Status.TaskTestSuiteSpec = ttsr.Spec.TaskTestSuiteSpec
				ttsr.Status.TaskTestSuiteSpec.TaskTests[0].TaskTestSpec = &v1alpha1.TaskTestSpec{
					TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
					Expects: &v1alpha1.ExpectedOutcomes{
						SuccessStatus: ptr.To(true),
						SuccessReason: ptr.To(v1.TaskRunReason("Succeeded")),
					},
				}
			}),
			wantTtrs: []v1alpha1.TaskTestRun{
				*taskTestRunMap[tcCheckSuccessfulTtrsInlineTts+"-0"],
				*taskTestRunMap[tcCheckSuccessfulTtrsInlineTts+"-1"],
			},
			wantStartTime:      true,
			wantCompletionTime: true,
		},
		tcCheckSuccessfulTtrsReferencedTts: {
			wantTtsrStatus: patchTaskTestSuiteRun(taskTestSuiteRunMap[tcCheckSuccessfulTtrsReferencedTts], func(ttsr *v1alpha1.TaskTestSuiteRun) {
				ttsr.Status.Conditions = duckv1.Conditions{{
					Type:    "Succeeded",
					Status:  "True",
					Reason:  "Succeeded",
					Message: "all TaskTestRuns completed executing and were successful",
				}}
				ttsr.Status.TaskTestSuiteName = ptr.To("suite")
				ttsr.Status.TaskTestSuiteSpec = &(taskTestSuiteMap["simple_task_test_suite"].Spec)
				ttsr.Status.TaskTestSuiteSpec.TaskTests[0].TaskTestSpec = &v1alpha1.TaskTestSpec{
					TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
					Expects: &v1alpha1.ExpectedOutcomes{
						SuccessStatus: ptr.To(true),
						SuccessReason: ptr.To(v1.TaskRunReason("Succeeded")),
					},
				}
			}),
			wantTtrs: []v1alpha1.TaskTestRun{
				*taskTestRunMap[tcCheckSuccessfulTtrsReferencedTts+"-0"],
				*taskTestRunMap[tcCheckSuccessfulTtrsReferencedTts+"-1"],
			},
			wantStartTime:      true,
			wantCompletionTime: true,
		},
		tcCheckFailedTtrsInlineTts: {
			wantTtsrStatus: patchTaskTestSuiteRun(taskTestSuiteRunMap[tcCheckFailedTtrsInlineTts], func(ttsr *v1alpha1.TaskTestSuiteRun) {
				ttsr.Status.TaskTestSuiteSpec = ttsr.Spec.TaskTestSuiteSpec
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
					Reason: "TaskTestRunUnexpectedOutcomes",
					Message: "all TaskTestRuns completed executing and not all were successful: " +
						ttsr.Status.TaskTestSuiteSpec.TaskTests[1].GetTaskTestRunName(ttsr.Name) + ": " +
						messageExpectationsNotMet,
				}}
			}),
			wantTtrs: []v1alpha1.TaskTestRun{
				*taskTestRunMap[tcCheckFailedTtrsInlineTts+"-0"],
				*taskTestRunMap[tcCheckFailedTtrsInlineTts+"-1"],
			},
			wantStartTime:      true,
			wantCompletionTime: true,
		},
		tcCheckFailedTtrsReferencedTts: {
			wantTtsrStatus: patchTaskTestSuiteRun(taskTestSuiteRunMap[tcCheckFailedTtrsReferencedTts], func(ttsr *v1alpha1.TaskTestSuiteRun) {
				ttsr.Status.TaskTestSuiteName = ptr.To("suite")
				ttsr.Status.TaskTestSuiteSpec = &(taskTestSuiteMap["simple_task_test_suite"].Spec)
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
					Reason: "TaskTestRunUnexpectedOutcomes",
					Message: "all TaskTestRuns completed executing and not all were successful: " +
						ttsr.Status.TaskTestSuiteSpec.TaskTests[1].GetTaskTestRunName(ttsr.Name) + ": " +
						messageExpectationsNotMet,
				}}
			}),
			wantTtrs: []v1alpha1.TaskTestRun{
				*taskTestRunMap[tcCheckFailedTtrsReferencedTts+"-0"],
				*taskTestRunMap[tcCheckFailedTtrsReferencedTts+"-1"],
			},
			wantStartTime:      true,
			wantCompletionTime: true,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			testAssets, cancel := getTaskTestSuiteRunController(t, data)
			clients := testAssets.Clients

			if tt.ttsr == nil {
				tt.ttsr = taskTestSuiteRunMap[name]
			}

			defer cancel()

			gotErr := testAssets.Controller.Reconciler.Reconcile(testAssets.Ctx, tt.ttsr.GetNamespacedName().String())

			if gotErrIsRequeue, _ := controller.IsRequeueKey(gotErr); gotErr != nil && !gotErrIsRequeue {
				t.Errorf("unexpected error: %v", gotErr)
			}

			ttsr, err := clients.Pipeline.TektonV1alpha1().TaskTestSuiteRuns(tt.ttsr.Namespace).Get(testAssets.Ctx, tt.ttsr.Name, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("getting updated tasktestrun: %v", err)
			}
			if tt.wantStartTime && ttsr.Status.StartTime == nil {
				t.Error("TaskTestSuiteRun: Didn't expect start time to be nil")
			}
			if !tt.wantStartTime && ttsr.Status.StartTime != nil {
				t.Error("TaskTestSuiteRun: Expected start time to be nil")
			}
			if tt.wantCompletionTime && ttsr.Status.CompletionTime == nil {
				t.Error("TaskTestSuiteRun: Didn't expect completion time to be nil")
			}
			if !tt.wantCompletionTime && ttsr.Status.CompletionTime != nil {
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
					errorMessage += fmt.Sprintf(": there probably is an issue with the labels and the filtering, as there are %d TaskTestRuns in the namespace\n%v", len(allTtrs.Items), allTtrs.Items)
				} else {
					taskTestRunList, _ := json.MarshalIndent(allTtrs.Items, "", "  ")
					errorMessage += fmt.Sprintf(" which is the exact number of TaskTestRuns in the namespace\n%s", taskTestRunList)
				}
				t.Errorf(errorMessage, len(tt.wantTtrs), len(trl.Items))
			}

			slices.SortFunc(trl.Items, taskTestRunSortFunc)
			slices.SortFunc(tt.wantTtrs, taskTestRunSortFunc)
			for i, ttr := range trl.Items {
				if tt.wantStartTime && ttr.Status.StartTime == nil {
					t.Errorf("TaskTestRun %d: Didn't expect start time to be nil", i)
				}
				if !tt.wantStartTime && ttr.Status.StartTime != nil {
					t.Errorf("TaskTestRun %d: Expected start time to be nil", i)
				}
				if tt.wantCompletionTime && ttr.Status.CompletionTime == nil {
					t.Errorf("TaskTestRun %d: Didn't expect completion time to be nil", i)
				}
				if !tt.wantCompletionTime && ttr.Status.CompletionTime != nil {
					t.Errorf("TaskTestRun %d: Expected completion time to be nil", i)
				}
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

			gotErr := testAssets.Controller.Reconciler.Reconcile(testAssets.Ctx, tt.ttr.GetNamespacedName().String())

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

			ttr, err := clients.Pipeline.TektonV1alpha1().TaskTestSuiteRuns(tt.ttr.Namespace).Get(testAssets.Ctx, tt.ttr.Name, metav1.GetOptions{})
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
	return initializeTaskTestSuiteRunControllerAssets(t, d, pipeline.Options{Images: pipeline.Images{}})
}

func initializeTaskTestSuiteRunControllerAssets(t *testing.T, d test.Data, opts pipeline.Options) (test.Assets, func()) {
	t.Helper()
	ctx, _ := ttesting.SetupFakeContext(t)
	loggingConfig := logtesting.TestLogger(t).Desugar().WithOptions(zap.IncreaseLevel(zap.InfoLevel))
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
		la.Promote(pkgreconciler.UniversalBucket(), func(pkgreconciler.Bucket, types.NamespacedName) {})
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
	return strings.Compare(a.GetNamespacedName().String(), b.GetNamespacedName().String())
}

func generateTaskTestRun(t *testing.T, yaml, suiteRunName, suiteTaskName string) *v1alpha1.TaskTestRun {
	t.Helper()
	return parse.MustParseTaskTestRun(t, fmt.Sprintf(yaml, suiteRunName, suiteTaskName, suiteRunName, suiteTaskName, suiteRunName))
}
