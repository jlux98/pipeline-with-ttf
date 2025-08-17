package tasktestrun

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1alpha1"
	_ "github.com/tektoncd/pipeline/pkg/client/injection/informers/pipeline/v1alpha1/tasktestrun/fake"
	"github.com/tektoncd/pipeline/pkg/reconciler/apiserver"
	ttesting "github.com/tektoncd/pipeline/pkg/reconciler/testing"
	"github.com/tektoncd/pipeline/test"
	"github.com/tektoncd/pipeline/test/diff"
	"github.com/tektoncd/pipeline/test/names"
	"github.com/tektoncd/pipeline/test/parse"
	"go.uber.org/zap"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/clock"
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

// IgnoreFields options
var (
	ignoreResourceVersion           = cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ResourceVersion")
	ignoreLastTransitionTime        = cmpopts.IgnoreFields(apis.Condition{}, "LastTransitionTime.Inner.Time")
	ignoreTaskRunStatus             = cmpopts.IgnoreFields(v1alpha1.TaskTestRunStatusFields{}, "TaskRunStatus")
	ignoreStartTimeTaskRun          = cmpopts.IgnoreFields(v1.TaskRunStatusFields{}, "StartTime")
	ignoreStartTimeTaskTestRun      = cmpopts.IgnoreFields(v1alpha1.TaskTestRunStatusFields{}, "StartTime")
	ignoreCompletionTimeTaskTestRun = cmpopts.IgnoreFields(v1alpha1.TaskTestRunStatusFields{}, "CompletionTime")
)

// Task manifests
const (
	tManifest = `
metadata:
  name: task
  namespace: foo
spec:
  steps:
  - command:
    - /mycmd
    env:
    - name: foo
      value: bar
    image: foo
    name: simple-step`

	tManifestHelloTask = `
metadata:
  name: hello-task
  namespace: foo
spec:
  results:
  - description: |
      The current time in the format hh:mm:ss
    name: current-time
    type: string
  - description: |
      The current date in the format dd:mm:yy
    name: current-date
    type: string
  steps:
  - computeResources: {}
    image: alpine
    name: hello-step
    script: |
      echo "Hello world!"
      date +%H:%M:%S | tee $(results.current-time.path)
      date +%y-%m-%d | tee $(results.current-date.path)`
)

// TaskTest manifests
const (
	ttManifest = `
metadata:
  name: task-test
  namespace: foo
spec:
  taskRef:
    name: hello-task
  expected:
    results:
    - name: current-date
      type: string
      value: "2025-08-15"
    - name: current-time
      type: string
      value: "15:17:59"
    successStatus: true
    successReason: Succeeded`
)

// TaskRun manifests
const (
	trManifestJustStarted = `
metadata:
  name: ttr-newly-created-run
  namespace: foo
  labels:
    tekton.dev/taskTestRun: ttr-newly-created
  ownerReferences:
  - apiVersion: tekton.dev/v1alpha1
    kind: TaskTestRun
    name: ttr-newly-created
    controller: true
    blockOwnerDeletion: true
spec:
  taskRef:
    name: task
status:
  conditions:
    - reason: Started
      status: Unknown
      type: Succeeded`

	trManifestAlreadyStarted = `
metadata:
  name: task-test-run123-run
  namespace: foo
  labels:
    tekton.dev/taskTestRun: ttr-existing-taskrun

  ownerReferences:
  - apiVersion: tekton.dev/v1alpha1
    kind: TaskTestRun
    name: ttr-existing-taskrun

    controller: true
    blockOwnerDeletion: true
spec:
  taskRef:
    name: task
status:
  conditions:
    - reason: Started
      status: Unknown
      type: Succeeded
  startTime:  "2025-08-15T15:17:55Z"`

	trManifestCompleted = `
metadata:
  name: ttr-completed-task-run-run
  namespace: foo
  labels:
    tekton.dev/taskTestRun: ttr-completed-task-run
  ownerReferences:
  - apiVersion: tekton.dev/v1alpha1
    kind: TaskTestRun
    name: ttr-completed-task-run
    controller: true
    blockOwnerDeletion: true
spec:
  taskRef:
    name: hello-task
status:
  completionTime: "2025-08-15T15:17:59Z"
  conditions:
  - message: All Steps have completed executing
    reason: Succeeded
    status: "True"
    type: Succeeded
  podName: hello-world-run-b6b5k-pod
  results:
  - name: current-date
    type: string
    value: 2025-08-15
  - name: current-time
    type: string
    value: 15:17:59
  startTime: "2025-08-15T15:17:55Z"
  steps:
  - container: step-hello-step
    imageID: docker.io/library/alpine@sha256:4bcff63911fcb4448bd4fdacec207030997caf25e9bea4045fa6c8c44de311d1
    name: hello-step
    terminated:
      containerID: containerd://13d2449b5cf780bbcdd7e7fd489f72beb69d072eba12bbbf5de77b06098b5b48
      exitCode: 0
      finishedAt: "2025-08-15T15:17:59Z"
      message: '[{"key":"current-date","value":"2025-08-15\n","type":1},{"key":"current-time","value":"15:17:59\n","type":1}]'
      reason: Completed
      startedAt: "2025-08-15T15:17:59Z"
    terminationReason: Completed
  taskSpec:
    results:
    - description: |
        The current time in the format hh:mm:ss
      name: current-time
      type: string
    - description: |
        The current date in the format dd:mm:yy
      name: current-date
      type: string
    steps:
    - computeResources: {}
      image: alpine
      name: hello-step
      script: |
        echo "Hello world!"
        date +%H:%M:%S | tee /tekton/results/current-time
        date +%Y-%m-%d | tee /tekton/results/current-date`
)

// Valid TaskTestRun manifests
const (
	ttrManifestNewTaskRun = `metadata:
  name: ttr-newly-created
  namespace: foo
spec:
  taskTestSpec:
    taskRef:
      name: task`

	ttrManifestExistingTaskRun = `metadata:
  name: ttr-existing-taskrun

  namespace: foo
spec:
  taskTestSpec:
    taskRef:
      name: task
status:
  startTime:  "2025-08-15T15:17:55Z"
  conditions:
  - type: Succeeded
    reason: Started
    status: Unknown`

	ttrManifestCompletedTaskRunWithTestSpec = `metadata:
  name: ttr-completed-task-run
  namespace: foo
spec:
  taskTestSpec:
    taskRef:
      name: hello-task
    expected:
      results:
      - name: current-date
        type: string
        value: "2025-08-15"
      - name: current-time
        type: string
        value: "15:17:59"
      successStatus: true
      successReason: Succeeded
status:
  startTime:  "2025-08-15T15:17:55Z"
  conditions:
  - type: Succeeded
    reason: Started
    status: Unknown`

	ttrManifestCompletedTaskRunWithTestSpecUnexpectedResults = `metadata:
  name: ttr-completed-task-run-unexpected-results
  namespace: foo
spec:
  taskTestSpec:
    taskRef:
      name: hello-task
    expected:
      results:
      - name: current-date
        type: string
        value: "2015-08-15"
      - name: current-time
        type: string
        value: "05:17:59"
      successStatus: False
      successReason: "TaskRunCancelled"
status:
  startTime:  "2025-08-15T15:17:55Z"
  conditions:
  - type: Succeeded
    reason: Started
    status: Unknown`

	ttrManifestCompletedTaskRunWithTestRef = `metadata:
  name: ttr-completed-task-run-referenced-test
  namespace: foo
spec:
  taskTestRef:
    name: task-test
status:
  startTime:  "2025-08-15T15:17:55Z"
  conditions:
  - type: Succeeded
    reason: Started
    status: Unknown`
)

// Invalid TaskTestRun manifests
const (
	ttrManifestAbsentTaskTest = `metadata:
  name: invalid-ttr-absent-task-test
  namespace: foo
spec:
  taskTestRef:
    name: absent-task-test`

	ttrManifestExpectsUndeclaredResult = `metadata:
  name: invalid-ttr-expects-undeclared-result
  namespace: foo
spec:
  taskTestSpec:
    taskRef:
      name: task
    expected:
      results:
      - name: current-date
        type: string
        value: "2025-08-15"
      - name: current-time
        type: string
        value: "15:17:59"
      successStatus: true
      successReason: Succeeded`

	ttrManifestAbsentTask = `metadata:
  name: invalid-ttr-absent-task
  namespace: foo
spec:
  taskTestSpec:
    taskRef:
      name: absent-task`
)

func TestReconciler_ValidateReconcileKind(t *testing.T) {
	task := parse.MustParseV1Task(t, tManifest)
	taskHelloTask := parse.MustParseV1Task(t, tManifestHelloTask)
	taskTestRun := parse.MustParseTaskTestRun(t, ttrManifestNewTaskRun)
	taskTestRunExistingTaskRun := parse.MustParseTaskTestRun(t, ttrManifestExistingTaskRun)
	taskTestRunCompletedTaskRunWithTestSpec := parse.MustParseTaskTestRun(t, ttrManifestCompletedTaskRunWithTestSpec)
	taskTestRunCompletedTaskRunWithTestRef := parse.MustParseTaskTestRun(t, ttrManifestCompletedTaskRunWithTestRef)
	ttrCompletedTaskRunWithTestSpecUnexpectedResults := parse.MustParseTaskTestRun(t, ttrManifestCompletedTaskRunWithTestSpecUnexpectedResults)
	taskRunAlreadyStarted := parse.MustParseV1TaskRun(t, trManifestAlreadyStarted)
	taskRunCompleted := parse.MustParseV1TaskRun(t, trManifestCompleted)
	taskRunCompleted2 := parse.MustParseV1TaskRun(t, strings.ReplaceAll(trManifestCompleted, `ttr-completed-task-run`, `ttr-completed-task-run-referenced-test`))
	taskRunCompleted3 := parse.MustParseV1TaskRun(t, strings.ReplaceAll(strings.ReplaceAll(trManifestCompleted, `ttr-completed-task-run`, `ttr-completed-task-run-unexpected-results`), `  - name: current-time
    type: string
    value: 15:17:59
`, ""))

	taskTest := parse.MustParseTaskTest(t, ttManifest)

	diffCurrentDate := cmp.Diff(&v1.ResultValue{
		Type:      "string",
		StringVal: "2015-08-15",
	}, &v1.ResultValue{
		Type:      "string",
		StringVal: "2025-08-15",
	})

	diffCurrentTime := cmp.Diff(&v1.ResultValue{
		Type:      "string",
		StringVal: "05:17:59",
	}, &v1.ResultValue{})

	data := test.Data{
		Tasks:        []*v1.Task{task, taskHelloTask},
		TaskRuns:     []*v1.TaskRun{taskRunAlreadyStarted, taskRunCompleted, taskRunCompleted2, taskRunCompleted3},
		TaskTests:    []*v1alpha1.TaskTest{taskTest},
		TaskTestRuns: []*v1alpha1.TaskTestRun{taskTestRun, taskTestRunExistingTaskRun, taskTestRunCompletedTaskRunWithTestSpec, taskTestRunCompletedTaskRunWithTestRef, ttrCompletedTaskRunWithTestSpecUnexpectedResults},
	}

	type tc struct {
		ttr                *v1alpha1.TaskTestRun
		wantTtrStatus      *v1alpha1.TaskTestRunStatus
		wantTr             *v1.TaskRun
		wantStartTime      bool
		wantCompletionTime bool
	}
	tests := map[string]tc{
		"starting new run": {
			ttr: taskTestRun,
			wantTtrStatus: &v1alpha1.TaskTestRunStatus{
				Status: duckv1.Status{Conditions: duckv1.Conditions{{
					Type:   "Succeeded",
					Status: "Unknown",
					Reason: "Started",
				}}},
				TaskTestRunStatusFields: v1alpha1.TaskTestRunStatusFields{
					TaskRunName:  "ttr-newly-created-run",
					TaskTestSpec: &v1alpha1.NamedTaskTestSpec{Spec: &v1alpha1.TaskTestSpec{TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"}}},
				},
			},
			wantTr:        parse.MustParseV1TaskRun(t, trManifestJustStarted),
			wantStartTime: true,
		},
		"run has already started": {
			ttr: taskTestRunExistingTaskRun,
			wantTtrStatus: &v1alpha1.TaskTestRunStatus{
				Status: duckv1.Status{Conditions: duckv1.Conditions{{
					Type:   "Succeeded",
					Status: "Unknown",
					Reason: "Started",
				}}},
				TaskTestRunStatusFields: v1alpha1.TaskTestRunStatusFields{
					TaskRunName:  "task-test-run123-run",
					TaskTestSpec: &v1alpha1.NamedTaskTestSpec{Spec: &v1alpha1.TaskTestSpec{TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"}}},
				},
			},
			wantTr:        parse.MustParseV1TaskRun(t, trManifestAlreadyStarted),
			wantStartTime: true,
		},
		"completed run with inline test and expected results": {
			ttr: taskTestRunCompletedTaskRunWithTestSpec,
			wantTtrStatus: &v1alpha1.TaskTestRunStatus{
				Status: duckv1.Status{Conditions: duckv1.Conditions{{
					Type:   "Succeeded",
					Status: "True",
					Reason: "All Expectations were met.",
				}}},
				TaskTestRunStatusFields: v1alpha1.TaskTestRunStatusFields{
					TaskRunName: "ttr-completed-task-run-run",
					TaskTestSpec: &v1alpha1.NamedTaskTestSpec{Spec: &v1alpha1.TaskTestSpec{
						TaskRef: &v1alpha1.SimpleTaskRef{Name: "hello-task"},
						Expected: v1alpha1.ExpectedOutcomes{
							Results: []v1.TaskResult{
								{
									Name: "current-date",
									Type: "string",
									Value: &v1.ResultValue{
										StringVal: "2025-08-15",
										Type:      "string",
									},
								},
								{
									Name: "current-time",
									Type: "string",
									Value: &v1.ResultValue{
										StringVal: "15:17:59",
										Type:      "string",
									},
								},
							},
							SuccessStatus: true,
							SuccessReason: v1.TaskRunReasonSuccessful,
						},
					}},
					Outcomes: v1alpha1.ObservedOutcomes{
						Results: []v1alpha1.ObservedResults{
							{
								Name: "current-date",
								Want: &v1.ResultValue{
									Type:      "string",
									StringVal: "2025-08-15",
								},
								Got: &v1.ResultValue{
									Type:      "string",
									StringVal: "2025-08-15",
								},
							},
							{
								Name: "current-time",
								Want: &v1.ResultValue{
									Type:      "string",
									StringVal: "15:17:59",
								},
								Got: &v1.ResultValue{
									Type:      "string",
									StringVal: "15:17:59",
								},
							},
						},
						SuccessStatus: v1alpha1.ObservedSuccessStatus{
							Want:           true,
							Got:            true,
							WantMatchesGot: true,
						},
						SuccessReason: v1alpha1.ObservedSuccessReason{
							Want: v1.TaskRunReasonSuccessful,
							Got:  v1.TaskRunReasonSuccessful,
							Diff: "",
						},
					},
				},
			},
			wantTr:             taskRunCompleted,
			wantStartTime:      true,
			wantCompletionTime: true,
		},
		"completed run with referenced test and expected results": {
			ttr: taskTestRunCompletedTaskRunWithTestRef,
			wantTtrStatus: &v1alpha1.TaskTestRunStatus{
				Status: duckv1.Status{Conditions: duckv1.Conditions{{
					Type:   "Succeeded",
					Status: "True",
					Reason: "All Expectations were met.",
				}}},
				TaskTestRunStatusFields: v1alpha1.TaskTestRunStatusFields{
					TaskRunName: "ttr-completed-task-run-referenced-test-run",
					TaskTestSpec: &v1alpha1.NamedTaskTestSpec{
						Name: ptr.To("task-test"),
						Spec: &v1alpha1.TaskTestSpec{
							TaskRef: &v1alpha1.SimpleTaskRef{Name: "hello-task"},
							Expected: v1alpha1.ExpectedOutcomes{
								Results: []v1.TaskResult{
									{
										Name: "current-date",
										Type: "string",
										Value: &v1.ResultValue{
											StringVal: "2025-08-15",
											Type:      "string",
										},
									},
									{
										Name: "current-time",
										Type: "string",
										Value: &v1.ResultValue{
											StringVal: "15:17:59",
											Type:      "string",
										},
									},
								},
								SuccessStatus: true,
								SuccessReason: v1.TaskRunReasonSuccessful,
							},
						},
					},
					Outcomes: v1alpha1.ObservedOutcomes{
						Results: []v1alpha1.ObservedResults{
							{
								Name: "current-date",
								Want: &v1.ResultValue{
									Type:      "string",
									StringVal: "2025-08-15",
								},
								Got: &v1.ResultValue{
									Type:      "string",
									StringVal: "2025-08-15",
								},
							},
							{
								Name: "current-time",
								Want: &v1.ResultValue{
									Type:      "string",
									StringVal: "15:17:59",
								},
								Got: &v1.ResultValue{
									Type:      "string",
									StringVal: "15:17:59",
								},
							},
						},
						SuccessStatus: v1alpha1.ObservedSuccessStatus{
							Want:           true,
							Got:            true,
							WantMatchesGot: true,
						},
						SuccessReason: v1alpha1.ObservedSuccessReason{
							Want: v1.TaskRunReasonSuccessful,
							Got:  v1.TaskRunReasonSuccessful,
							Diff: "",
						},
					},
				},
			},
			wantTr:             taskRunCompleted2,
			wantStartTime:      true,
			wantCompletionTime: true,
		},
		"completed run with inline test and unexpected results": {
			ttr: ttrCompletedTaskRunWithTestSpecUnexpectedResults,
			wantTtrStatus: &v1alpha1.TaskTestRunStatus{
				Status: duckv1.Status{Conditions: duckv1.Conditions{{
					Type:   "Succeeded",
					Status: "False",
					Reason: "TaskTestRunUnmetExpectations",
					Message: "not all expectations were met:\n" +
						diffCurrentDate +
						diffCurrentTime +
						"observed success status did not match expectation\n" +
						cmp.Diff(v1.TaskRunReason("TaskRunCancelled"), v1.TaskRunReason("Succeeded")),
				}}},
				TaskTestRunStatusFields: v1alpha1.TaskTestRunStatusFields{
					TaskRunName: "ttr-completed-task-run-unexpected-results-run",
					TaskTestSpec: &v1alpha1.NamedTaskTestSpec{Spec: &v1alpha1.TaskTestSpec{
						TaskRef: &v1alpha1.SimpleTaskRef{Name: "hello-task"},
						Expected: v1alpha1.ExpectedOutcomes{
							Results: []v1.TaskResult{
								{
									Name: "current-date",
									Type: "string",
									Value: &v1.ResultValue{
										StringVal: "2015-08-15",
										Type:      "string",
									},
								},
								{
									Name: "current-time",
									Type: "string",
									Value: &v1.ResultValue{
										StringVal: "05:17:59",
										Type:      "string",
									},
								},
							},
							SuccessStatus: false,
							SuccessReason: "TaskRunCancelled",
						},
					}},
					Outcomes: v1alpha1.ObservedOutcomes{
						Results: []v1alpha1.ObservedResults{
							{
								Name: "current-date",
								Want: &v1.ResultValue{
									Type:      "string",
									StringVal: "2015-08-15",
								},
								Got: &v1.ResultValue{
									Type:      "string",
									StringVal: "2025-08-15",
								},
								Diff: diffCurrentDate,
							},
							{
								Name: "current-time",
								Want: &v1.ResultValue{
									Type:      "string",
									StringVal: "05:17:59",
								},
								Got:  &v1.ResultValue{},
								Diff: diffCurrentTime,
							},
						},
						SuccessStatus: v1alpha1.ObservedSuccessStatus{
							Want:           false,
							Got:            true,
							WantMatchesGot: false,
						},
						SuccessReason: v1alpha1.ObservedSuccessReason{
							Want: "TaskRunCancelled",
							Got:  "Succeeded",
							Diff: cmp.Diff(v1.TaskRunReason("TaskRunCancelled"), v1.TaskRunReason("Succeeded")),
						},
					},
				},
			},
			wantTr:             taskRunCompleted3,
			wantStartTime:      true,
			wantCompletionTime: true,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			testAssets, cancel := getTaskTestRunController(t, data)
			clients := testAssets.Clients
			defer cancel()

			_ = testAssets.Controller.Reconciler.Reconcile(testAssets.Ctx, getRunName(tt.ttr))
			// gotErr := testAssets.Controller.Reconciler.Reconcile(testAssets.Ctx, getRunName(tt.ttr))

			// if gotErr != nil {
			// 	t.Errorf("Go unexpected error: %v", gotErr)
			// }

			ttr, err := clients.Pipeline.TektonV1alpha1().TaskTestRuns(tt.ttr.Namespace).Get(testAssets.Ctx, tt.ttr.Name, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("getting updated tasktestrun: %v", err)
			}
			if tt.wantStartTime && ttr.Status.StartTime == nil {
				t.Error("TaskTestRun: Didn't expect start time to be nil")
			}
			if !tt.wantStartTime && ttr.Status.StartTime != nil {
				t.Error("TaskTestRun: Expected start time to be nil")
			}
			if tt.wantCompletionTime && ttr.Status.CompletionTime == nil {
				t.Error("TaskTestRun: Didn't expect completion time to be nil")
			}
			if !tt.wantCompletionTime && ttr.Status.CompletionTime != nil {
				t.Error("TaskTestRun: Expected completion time to be nil")
			}
			if d := cmp.Diff(*tt.wantTtrStatus, ttr.Status,
				ignoreResourceVersion,
				ignoreLastTransitionTime,
				ignoreTaskRunStatus,
				ignoreStartTimeTaskTestRun,
				ignoreCompletionTimeTaskTestRun); d != "" {
				t.Errorf("Didn't get expected TaskTestRun: %v", diff.PrintWantGot(d))
			}

			tr, err := clients.Pipeline.TektonV1().TaskRuns(tt.ttr.Namespace).Get(testAssets.Ctx, ttr.Status.TaskRunName, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("getting updated taskrun: %v", err)
			}
			if tt.wantStartTime && tr.Status.StartTime == nil {
				t.Error("TaskRun: Didn't expect start time to be nil")
			}
			if !tt.wantStartTime && tr.Status.StartTime != nil {
				t.Error("TaskRun: Expected start time to be nil")
			}
			if tt.wantCompletionTime && tr.Status.CompletionTime == nil {
				t.Error("TaskRun: Didn't expect completion time to be nil")
			}
			if !tt.wantCompletionTime && tr.Status.CompletionTime != nil {
				t.Error("TaskRun: Expected completion time to be nil")
			}
			if d := cmp.Diff(tt.wantTr, tr,
				ignoreResourceVersion,
				ignoreStartTimeTaskRun,
				ignoreLastTransitionTime); d != "" {
				t.Errorf("Didn't get expected TaskRun: %v", diff.PrintWantGot(d))
			}
			if d := cmp.Diff(tr.Status, ttr.Status.TaskRunStatus); d != "" {
				t.Errorf("TaskRun Status not mirrored properly to TaskTestRun: %v", diff.PrintWantGot(d))
			}
		})
	}
}

func TestReconciler_InvalidateReconcileKind(t *testing.T) {
	ttrAbsentTaskTest := parse.MustParseTaskTestRun(t, ttrManifestAbsentTaskTest)
	ttrAbsentTask := parse.MustParseTaskTestRun(t, ttrManifestAbsentTask)
	ttrExpectsUndeclaredResult := parse.MustParseTaskTestRun(t, ttrManifestExpectsUndeclaredResult)
	task := parse.MustParseV1Task(t, tManifest)

	data := test.Data{
		Tasks:        []*v1.Task{task},
		TaskRuns:     []*v1.TaskRun{},
		TaskTests:    []*v1alpha1.TaskTest{},
		TaskTestRuns: []*v1alpha1.TaskTestRun{ttrAbsentTaskTest, ttrExpectsUndeclaredResult, ttrAbsentTask},
	}

	type tc struct {
		ttr                *v1alpha1.TaskTestRun
		wantErr            error
		wantTtrStatus      *v1alpha1.TaskTestRunStatus
		wantCompletionTime bool
	}
	tests := map[string]tc{
		"ttr references absent task test": {
			ttr: ttrAbsentTaskTest,
			wantTtrStatus: &v1alpha1.TaskTestRunStatus{
				Status: duckv1.Status{Conditions: duckv1.Conditions{{
					Type:   "Succeeded",
					Status: "Unknown",
					Reason: "Started",
				}}},
			},
			wantErr: fmt.Errorf("could not prepare reconciliation of task test run invalid-ttr-absent-task-test: %w", apierrors.NewNotFound(schema.GroupResource{Group: "tekton.dev", Resource: "tasktests"}, "absent-task-test")),
		},
		"ttr expects result not declared in task": {
			ttr: ttrExpectsUndeclaredResult,
			wantTtrStatus: &v1alpha1.TaskTestRunStatus{
				Status: duckv1.Status{
					Conditions: duckv1.Conditions{
						{
							Type:    "Succeeded",
							Status:  "False",
							Reason:  "TaskTestRunValidationFailed",
							Message: `validation failed for referenced object: Result current-date expected but not declared by task "task"`,
						},
					},
				},
				TaskTestRunStatusFields: v1alpha1.TaskTestRunStatusFields{
					TaskTestSpec: &v1alpha1.NamedTaskTestSpec{Spec: &v1alpha1.TaskTestSpec{
						TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
						Expected: v1alpha1.ExpectedOutcomes{
							Results: []v1.TaskResult{
								{
									Name:  "current-date",
									Type:  "string",
									Value: &v1.ResultValue{StringVal: "2025-08-15", Type: "string"},
								},
								{
									Name:  "current-time",
									Type:  "string",
									Value: &v1.ResultValue{StringVal: "15:17:59", Type: "string"},
								},
							},
							SuccessStatus: true,
							SuccessReason: v1.TaskRunReasonSuccessful,
						},
					}},
				},
			},
			wantErr:            fmt.Errorf(`%w: Result current-date expected but not declared by task "task"`, apiserver.ErrReferencedObjectValidationFailed),
			wantCompletionTime: true,
		},
		"tt references absent task": {
			ttr:     ttrAbsentTask,
			wantErr: fmt.Errorf("could not dereference task under test: %w", apierrors.NewNotFound(schema.GroupResource{Group: "tekton.dev", Resource: "tasks"}, "absent-task")),
			wantTtrStatus: &v1alpha1.TaskTestRunStatus{
				Status: duckv1.Status{Conditions: duckv1.Conditions{{
					Type:   "Succeeded",
					Status: "Unknown",
					Reason: "Started",
				}}},
				TaskTestRunStatusFields: v1alpha1.TaskTestRunStatusFields{
					TaskTestSpec: &v1alpha1.NamedTaskTestSpec{Spec: &v1alpha1.TaskTestSpec{TaskRef: &v1alpha1.SimpleTaskRef{Name: "absent-task"}}},
				},
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			testAssets, cancel := getTaskTestRunController(t, data)
			clients := testAssets.Clients
			defer cancel()

			if tt.wantTtrStatus == nil {
				tt.wantTtrStatus = &tt.ttr.Status
			}

			gotErr := testAssets.Controller.Reconciler.Reconcile(testAssets.Ctx, getRunName(tt.ttr))

			if gotErr == nil {
				t.Errorf("expected error but got none")
			}

			if d := cmp.Diff(tt.wantErr.Error(), gotErr.Error()); d != "" {
				t.Errorf("Didn't get expected error: %v", diff.PrintWantGot(d))
			}

			ttr, err := clients.Pipeline.TektonV1alpha1().TaskTestRuns(tt.ttr.Namespace).Get(testAssets.Ctx, tt.ttr.Name, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("getting updated tasktestrun: %v", err)
			}

			if tt.wantCompletionTime && ttr.Status.CompletionTime == nil {
				t.Error("TaskTestRun: Didn't expect completion time to be nil")
			}
			if !tt.wantCompletionTime && ttr.Status.CompletionTime != nil {
				t.Error("TaskTestRun: Expected completion time to be nil")
			}

			if d := cmp.Diff(*tt.wantTtrStatus, ttr.Status,
				ignoreResourceVersion,
				ignoreLastTransitionTime,
				ignoreStartTimeTaskTestRun,
				ignoreCompletionTimeTaskTestRun); d != "" {
				t.Errorf("Did not get expected TaskTestRun status: %v", diff.PrintWantGot(d))
			}
		})
	}
}

// getTaskTestRunController returns an instance of the TaskTestRun controller/reconciler that has been seeded with
// d, where d represents the state of the system (existing resources) needed for the test.
func getTaskTestRunController(t *testing.T, d test.Data) (test.Assets, func()) {
	t.Helper()
	names.TestingSeed()
	return initializeTaskTestRunControllerAssets(t, d, pipeline.Options{Images: pipeline.Images{}})
}

func initializeTaskTestRunControllerAssets(t *testing.T, d test.Data, opts pipeline.Options) (test.Assets, func()) {
	t.Helper()
	ctx, _ := ttesting.SetupFakeContext(t)
	loggingConfig := logtesting.TestLogger(t).Desugar().WithOptions(zap.IncreaseLevel(zap.InfoLevel))
	ctx = logging.WithLogger(ctx, loggingConfig.Sugar())
	ctx = ttesting.SetupFakeCloudClientContext(ctx, d.ExpectedCloudEventCount)
	ctx, cancel := context.WithCancel(ctx)
	test.EnsureConfigurationConfigMapsExist(&d)
	c, informers := test.SeedTestData(t, ctx, d)
	configMapWatcher := cminformer.NewInformedWatcher(c.Kube, system.Namespace())
	ctl := NewController(&opts, clock.RealClock{})(ctx, configMapWatcher)
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

func getRunName(tr *v1alpha1.TaskTestRun) string {
	return strings.Join([]string{tr.Namespace, tr.Name}, "/")
}
