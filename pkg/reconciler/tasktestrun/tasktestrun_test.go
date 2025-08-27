package tasktestrun

import (
	"context"
	"errors"
	"fmt"
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
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
  params:
  - name: args
    default: ""
  steps:
  - name: simple-step
    command:
    - /mycmd
    env:
    - name: foo
      value: bar
    image: foo
  - name: another-simple-step
    command:
    - /mycmd
    env:
    - name: foo
      value: bar
    image: foo
`

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
      date +%Y-%m-%d | tee $(results.current-date.path)`
)

// TaskTest manifests
const ttManifest = `
metadata:
  name: task-test
  namespace: foo
spec:
  taskRef:
    name: hello-task
  expects:
    results:
    - name: current-date
      type: string
      value: "2025-08-15"
    - name: current-time
      type: string
      value: "15:17:59"
    successStatus: true
    successReason: Succeeded`

// TaskRun manifests
const (
	trManifestJustStarted = `
metadata:
  name: ttr-newly-created-run
  namespace: foo
  annotations:
    ExpectedValuesJSON: '{"successStatus":true}'
  labels:
    tekton.dev/taskTestRun: ttr-newly-created
  ownerReferences:
  - apiVersion: tekton.dev/v1alpha1
    kind: TaskTestRun
    name: ttr-newly-created
    controller: true
    blockOwnerDeletion: true
spec:
  taskSpec:
    params:
    - name: args
      default: ""
    steps:
    - name: simple-step
      command:
      - /mycmd
      env:
      - name: foo
        value: bar
      image: foo
    - name: another-simple-step
      command:
      - /mycmd
      env:
      - name: foo
        value: bar
      - name: ANOTHER_FOO
        value: ANOTHER_BAR
      image: foo
  params:
  - name: "args"
    value: "arg"
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
  - name: Testing|Environment
    type: string
    value: |
      {"step": "hello-step", "environment": {
      "HOME=/root",
      }}
  - name: Testing|FileSystemContent
    type: string
    value: '[{"stepName":"/tekton/run/0/status","objects":[{"path":"/tekton/results/current-date","type":"TextFile","content":"bar"},{"path":"/tekton/results/current-time","type":"TextFile","content":"bar"}]}]'
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
	ttrManifestNewTaskRun = `
metadata:
  name: ttr-newly-created
  namespace: foo
spec:
  taskTestSpec:
    taskRef:
      name: task
    inputs:
      params:
      - name: "args"
        value: "arg"
      env:
      - name: FOO
        value: bar
      stepEnvs:
      - stepName: another-simple-step
        env:
        - name: ANOTHER_FOO
          value: ANOTHER_BAR
    expects:
      successStatus: true`

	ttrManifestExistingTaskRun = `
metadata:
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

	ttrManifestCompletedTaskRunWithTestSpec = `
metadata:
  name: ttr-completed-task-run
  namespace: foo
spec:
  taskTestSpec:
    taskRef:
      name: hello-task
    expects:
      results:
      - name: current-date
        type: string
        value: "2025-08-15"
      - name: current-time
        type: string
        value: "15:17:59"
      env:
      - name: HOME
        value: "/root"
      fileSystemContents:
      - stepName: hello-step
        objects:
        - path: /tekton/results/current-date
          type: TextFile
          content: bar
      successStatus: true
      successReason: Succeeded
status:
  startTime:  "2025-08-15T15:17:55Z"
  conditions:
  - type: Succeeded
    reason: Started
    status: Unknown`

	ttrManifestCompletedTaskRunWithTestSpecUnexpectedResults = `
metadata:
  name: ttr-completed-task-run-unexpected-results
  namespace: foo
spec:
  taskTestSpec:
    taskRef:
      name: hello-task
    expects:
      results:
      - name: current-date
        type: string
        value: "2015-08-15"
      - name: current-time
        type: string
        value: "05:17:59"
      env:
      - name: HOME
        value: "/groot"
      fileSystemContents:
      - stepName: hello-step
        objects:
        - path: /tekton/results/current-date
          type: Directory
        - path: /tekton/results/current-time
          type: TextFile
          content: foo
      successStatus: False
      successReason: "TaskRunCancelled"
status:
  startTime:  "2025-08-15T15:17:55Z"
  conditions:
  - type: Succeeded
    reason: Started
    status: Unknown`

	ttrManifestCompletedTaskRunWithTestRef = `
metadata:
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
	ttrManifestAbsentTaskTest = `
metadata:
  name: invalid-ttr-absent-task-test
  namespace: foo
spec:
  taskTestRef:
    name: absent-task-test`

	ttrManifestExpectsUndeclaredResult = `
metadata:
  name: invalid-ttr-expects-undeclared-result
  namespace: foo
spec:
  timeout: 1h
  taskTestSpec:
    taskRef:
      name: task
    expects:
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
  startTime: %s`

	ttrManifestInputsUndeclaredParam = `
metadata:
  name: invalid-ttr-inputs-undeclared-param
  namespace: foo
spec:
  timeout: 1h
  taskTestSpec:
    taskRef:
      name: task
    inputs:
      params:
      - name: foo
        value: bar
status:
  startTime: %s`

	ttrManifestInputsUndeclaredStepEnvStep = `
metadata:
  name: invalid-ttr-inputs-undeclared-step-env-step
  namespace: foo
spec:
  timeout: 1h
  taskTestSpec:
    taskRef:
      name: task
    inputs:
      stepEnvs:
      - stepName: goodbye-step
        env:
        - name: FOO
          value: BAR
status:
  startTime: %s`

	ttrManifestExpectsUndeclaredEnvStep = `
metadata:
  name: invalid-ttr-expects-undeclared-env-step
  namespace: foo
spec:
  timeout: 1h
  taskTestSpec:
    taskRef:
      name: task
    expects:
      stepEnvs:
      - stepName: goodbye-step
        env:
        - name: HOME
          value: /root
      successStatus: true
      successReason: Succeeded
status:
  startTime: %s`

	ttrManifestExpectsUndeclaredFileSystemStep = `
metadata:
  name: invalid-ttr-expects-undeclared-fs-step
  namespace: foo
spec:
  timeout: 1h
  taskTestSpec:
    taskRef:
      name: task
    expects:
      fileSystemContents:
      - stepName: goodbye-step
        objects:
        - path: /tekton/results/current-date
          type: Directory
        - path: /tekton/results/current-time
          type: TextFile
          content: foo
      successStatus: true
      successReason: Succeeded
status:
  startTime: %s`

	ttrManifestAbsentTask = `
metadata:
  name: invalid-ttr-absent-task
  namespace: foo
spec:
  taskTestSpec:
    taskRef:
      name: absent-task`
)

func TestReconciler_ValidateReconcileKind(t *testing.T) {
	// instantiate custom resources
	task := parse.MustParseV1Task(t, tManifest)
	taskHelloTask := parse.MustParseV1Task(t, tManifestHelloTask)
	taskTestRun := parse.MustParseTaskTestRun(t, ttrManifestNewTaskRun)
	taskTestRunExistingTaskRun := parse.MustParseTaskTestRun(t, ttrManifestExistingTaskRun)
	taskTestRunCompletedTaskRunWithTestSpec := parse.MustParseTaskTestRun(t, ttrManifestCompletedTaskRunWithTestSpec)
	taskTestRunCompletedTaskRunWithTestRef := parse.MustParseTaskTestRun(t, ttrManifestCompletedTaskRunWithTestRef)
	ttrCompletedTaskRunWithTestSpecUnexpectedResults := parse.MustParseTaskTestRun(t, ttrManifestCompletedTaskRunWithTestSpecUnexpectedResults)
	taskRunAlreadyStarted := parse.MustParseV1TaskRun(t, trManifestAlreadyStarted)
	taskRunCompletedWithTestSpec := parse.MustParseV1TaskRun(t, trManifestCompleted)
	taskRunCompletedWithTestRef := parse.MustParseV1TaskRun(t, strings.ReplaceAll(trManifestCompleted, `ttr-completed-task-run`, `ttr-completed-task-run-referenced-test`))
	taskRunCompletedUnexpectedResults := parse.MustParseV1TaskRun(t, strings.ReplaceAll(strings.ReplaceAll(trManifestCompleted, `ttr-completed-task-run`, `ttr-completed-task-run-unexpected-results`), `  - name: current-time
    type: string
    value: 15:17:59
`, ""))
	taskTest := parse.MustParseTaskTest(t, ttManifest)

	// load custom resources into data for the fake cluster
	data := test.Data{
		Tasks:        []*v1.Task{task, taskHelloTask},
		TaskRuns:     []*v1.TaskRun{taskRunAlreadyStarted, taskRunCompletedWithTestSpec, taskRunCompletedWithTestRef, taskRunCompletedUnexpectedResults},
		TaskTests:    []*v1alpha1.TaskTest{taskTest},
		TaskTestRuns: []*v1alpha1.TaskTestRun{taskTestRun, taskTestRunExistingTaskRun, taskTestRunCompletedTaskRunWithTestSpec, taskTestRunCompletedTaskRunWithTestRef, ttrCompletedTaskRunWithTestSpecUnexpectedResults},
	}

	// declare some diffs that will be used in multiple places
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
	diffEnv := cmp.Diff("/groot", "/root")
	diffType := cmp.Diff(v1alpha1.DirectoryType, v1alpha1.TextFileType)
	diffContent := cmp.Diff("foo", "bar")

	type tc struct {
		ttr                *v1alpha1.TaskTestRun
		wantTtrStatus      *v1alpha1.TaskTestRunStatus
		wantTr             *v1.TaskRun
		wantStartTime      bool
		wantCompletionTime bool
	}
	tests := map[string]tc{
		"starting_new_run": {
			ttr: taskTestRun,
			wantTtrStatus: patchTaskTestRun(taskTestRun, func(ttr *v1alpha1.TaskTestRun) {
				ttr.Status.Conditions = duckv1.Conditions{{
					Type:   "Succeeded",
					Status: "Unknown",
					Reason: "Started",
				}}
				ttr.Status.TaskRunName = ptr.To("ttr-newly-created-run")
				ttr.Status.TaskTestSpec = ttr.Spec.TaskTestSpec
			}),
			wantTr:        parse.MustParseV1TaskRun(t, trManifestJustStarted),
			wantStartTime: true,
		},
		"run_has_already_started": {
			ttr: taskTestRunExistingTaskRun,
			wantTtrStatus: patchTaskTestRun(taskTestRunExistingTaskRun, func(ttr *v1alpha1.TaskTestRun) {
				ttr.Status.TaskRunName = ptr.To("task-test-run123-run")
				ttr.Status.TaskTestSpec = &v1alpha1.TaskTestSpec{TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"}}
			}),
			wantTr:        parse.MustParseV1TaskRun(t, trManifestAlreadyStarted),
			wantStartTime: true,
		},
		"completed_run_with_inline_test_and_expected_results": {
			ttr: taskTestRunCompletedTaskRunWithTestSpec,
			wantTtrStatus: patchTaskTestRun(taskTestRunCompletedTaskRunWithTestSpec, func(ttr *v1alpha1.TaskTestRun) {
				ttr.Status.MarkSuccessful()
				ttr.Status.TaskTestSpec = ttr.Spec.TaskTestSpec
				ttr.Status.TaskRunName = ptr.To("ttr-completed-task-run-run")
				ttr.Status.Outcomes = &v1alpha1.ObservedOutcomes{
					Results: &[]v1alpha1.ObservedResults{{
						Name: "current-date",
						Want: &v1.ResultValue{Type: "string", StringVal: "2025-08-15"},
						Got:  &v1.ResultValue{Type: "string", StringVal: "2025-08-15"},
					}, {
						Name: "current-time",
						Want: &v1.ResultValue{Type: "string", StringVal: "15:17:59"},
						Got:  &v1.ResultValue{Type: "string", StringVal: "15:17:59"},
					}},
					StepEnvs: &[]v1alpha1.ObservedStepEnv{{
						StepName: "hello-step",
						Env: []v1alpha1.ObservedEnvVar{{
							Name: "HOME",
							Want: "/root",
							Got:  "/root",
						}},
					}},
					FileSystemObjects: ptr.To([]v1alpha1.ObservedStepFileSystemContent{{
						StepName: "hello-step",
						Objects: []v1alpha1.ObservedFileSystemObject{{
							Path:        "/tekton/results/current-date",
							WantType:    "TextFile",
							GotType:     "TextFile",
							WantContent: "bar",
							GotContent:  "bar",
						}},
					}}),
					SuccessStatus: &v1alpha1.ObservedSuccessStatus{Want: true, Got: true},
					SuccessReason: &v1alpha1.ObservedSuccessReason{
						Want: v1.TaskRunReasonSuccessful,
						Got:  v1.TaskRunReasonSuccessful,
					},
				}
			}),
			wantTr:             taskRunCompletedWithTestSpec,
			wantStartTime:      true,
			wantCompletionTime: true,
		},
		"completed_run_with_referenced_test_and_expected_results": {
			ttr: taskTestRunCompletedTaskRunWithTestRef,
			wantTtrStatus: patchTaskTestRun(taskTestRunCompletedTaskRunWithTestRef, func(ttr *v1alpha1.TaskTestRun) {
				ttr.Status.Conditions = duckv1.Conditions{{
					Type:    "Succeeded",
					Status:  "True",
					Reason:  "Succeeded",
					Message: "TaskRun completed executing and outcomes were as expected",
				}}
				ttr.Status.TaskTestRunStatusFields = v1alpha1.TaskTestRunStatusFields{
					TaskRunName:  ptr.To("ttr-completed-task-run-referenced-test-run"),
					TaskTestName: ptr.To("task-test"),
					TaskTestSpec: &v1alpha1.TaskTestSpec{
						TaskRef: &v1alpha1.SimpleTaskRef{Name: "hello-task"},
						Expects: &v1alpha1.ExpectedOutcomes{
							Results: []v1.TaskResult{{
								Name:  "current-date",
								Type:  "string",
								Value: &v1.ResultValue{StringVal: "2025-08-15", Type: "string"},
							}, {
								Name:  "current-time",
								Type:  "string",
								Value: &v1.ResultValue{StringVal: "15:17:59", Type: "string"},
							}},
							SuccessStatus: ptr.To(true),
							SuccessReason: ptr.To(v1.TaskRunReasonSuccessful),
						},
					},
					Outcomes: &v1alpha1.ObservedOutcomes{
						Results: &[]v1alpha1.ObservedResults{{Name: "current-date",
							Want: &v1.ResultValue{Type: "string", StringVal: "2025-08-15"},
							Got:  &v1.ResultValue{Type: "string", StringVal: "2025-08-15"}}, {Name: "current-time",
							Want: &v1.ResultValue{Type: "string", StringVal: "15:17:59"},
							Got:  &v1.ResultValue{Type: "string", StringVal: "15:17:59"},
						}},
						SuccessStatus: &v1alpha1.ObservedSuccessStatus{Want: true, Got: true},
						SuccessReason: &v1alpha1.ObservedSuccessReason{
							Want: v1.TaskRunReasonSuccessful,
							Got:  v1.TaskRunReasonSuccessful,
						},
					},
				}
			}),
			wantTr:             taskRunCompletedWithTestRef,
			wantStartTime:      true,
			wantCompletionTime: true,
		},
		"completed_run_with_inline_test_and_unexpected_results": {
			ttr: ttrCompletedTaskRunWithTestSpecUnexpectedResults,
			wantTtrStatus: patchTaskTestRun(ttrCompletedTaskRunWithTestSpecUnexpectedResults, func(ttr *v1alpha1.TaskTestRun) {
				ttr.Status.Conditions = duckv1.Conditions{{
					Type:   "Succeeded",
					Status: "False",
					Reason: "TaskTestRunUnexpectedOutcomes",
					Message: "not all expectations were met:\n" +
						"observed success status did not match expectation\n" +
						"observed success reason did not match expectation\n" +
						"Result current-date: " + diffCurrentDate +
						"Result current-time: " + diffCurrentTime +
						"envVar HOME in step hello-step: " + diffEnv +
						"file system object \"/tekton/results/current-date\" type in step hello-step: " + diffType +
						"file system object \"/tekton/results/current-time\" content in step hello-step: " + diffContent,
				}}
				ttr.Status.TaskTestRunStatusFields = v1alpha1.TaskTestRunStatusFields{
					TaskRunName: ptr.To("ttr-completed-task-run-unexpected-results-run"),
					Outcomes: &v1alpha1.ObservedOutcomes{
						Results: &[]v1alpha1.ObservedResults{{Name: "current-date",
							Want: &v1.ResultValue{Type: "string", StringVal: "2015-08-15"},
							Got:  &v1.ResultValue{Type: "string", StringVal: "2025-08-15"},
							Diff: diffCurrentDate,
						}, {Name: "current-time",
							Want: &v1.ResultValue{Type: "string", StringVal: "05:17:59"},
							Got:  &v1.ResultValue{},
							Diff: diffCurrentTime,
						}},
						StepEnvs: &[]v1alpha1.ObservedStepEnv{{
							StepName: "hello-step",
							Env: []v1alpha1.ObservedEnvVar{{
								Name: "HOME",
								Want: "/groot",
								Got:  "/root",
								Diff: diffEnv,
							}},
						}},
						FileSystemObjects: &[]v1alpha1.ObservedStepFileSystemContent{{
							StepName: "hello-step",
							Objects: []v1alpha1.ObservedFileSystemObject{{
								Path:       "/tekton/results/current-date",
								WantType:   v1alpha1.DirectoryType,
								GotType:    v1alpha1.TextFileType,
								DiffType:   diffType,
								GotContent: "bar",
							}, {
								Path:        "/tekton/results/current-time",
								WantType:    v1alpha1.TextFileType,
								GotType:     v1alpha1.TextFileType,
								WantContent: "foo",
								GotContent:  "bar",
								DiffContent: diffContent,
							}},
						}},
						SuccessStatus: &v1alpha1.ObservedSuccessStatus{Want: false, Got: true, WantDiffersFromGot: true},
						SuccessReason: &v1alpha1.ObservedSuccessReason{
							Want:               "TaskRunCancelled",
							Got:                "Succeeded",
							WantDiffersFromGot: true,
						},
					},
				}
				ttr.Status.TaskTestSpec = ttr.Spec.TaskTestSpec
			}),
			wantTr:             taskRunCompletedUnexpectedResults,
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
			//     t.Errorf("Go unexpected error: %v", gotErr)
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

			if ttr.Status.TaskRunName == nil {
				ttr.Status.TaskRunName = ptr.To("")
			}
			tr, err := clients.Pipeline.TektonV1().TaskRuns(tt.ttr.Namespace).Get(testAssets.Ctx, *ttr.Status.TaskRunName, metav1.GetOptions{})
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
			if d := cmp.Diff(&tr.Status, ttr.Status.TaskRunStatus); d != "" {
				t.Errorf("TaskRun Status not mirrored properly to TaskTestRun: %v", diff.PrintWantGot(d))
			}
		})
	}
}

func TestReconciler_InvalidateReconcileKind(t *testing.T) {
	trCompletedNoEnvDump := parse.MustParseV1TaskRun(t, strings.ReplaceAll(trManifestCompleted, `  - name: Testing|Environment
    type: string
    value: |
      {"step": "hello-step", "environment": {
      "HOME=/root",
      }}
  - name: Testing|FileSystemContent
    type: string
    value: '[{"stepName":"/tekton/run/0/status","objects":[{"path":"/tekton/results/current-date","type":"TextFile","content":"bar"},{"path":"/tekton/results/current-time","type":"TextFile","content":"bar"}]}]'
`, ""))
	ttrAbsentTaskTest := parse.MustParseTaskTestRun(t, ttrManifestAbsentTaskTest)
	ttrAbsentTask := parse.MustParseTaskTestRun(t, ttrManifestAbsentTask)
	ttrInputsUndeclaredParam := parse.MustParseTaskTestRun(t, fmt.Sprintf(ttrManifestInputsUndeclaredParam, testClock.Now().Format(time.RFC3339)))
	ttrInputsUndeclaredEnvStep := parse.MustParseTaskTestRun(t, fmt.Sprintf(ttrManifestInputsUndeclaredStepEnvStep, testClock.Now().Format(time.RFC3339)))
	ttrExpectsUndeclaredResult := parse.MustParseTaskTestRun(t, fmt.Sprintf(ttrManifestExpectsUndeclaredResult, testClock.Now().Format(time.RFC3339)))
	ttrExpectsUndeclaredEnvStep := parse.MustParseTaskTestRun(t, fmt.Sprintf(ttrManifestExpectsUndeclaredEnvStep, testClock.Now().Format(time.RFC3339)))
	ttrExpectsUndeclaredFileSystemStep := parse.MustParseTaskTestRun(t, fmt.Sprintf(ttrManifestExpectsUndeclaredFileSystemStep, testClock.Now().Format(time.RFC3339)))
	ttrCompletedButNoEnvDumpInTR := parse.MustParseTaskTestRun(t, ttrManifestCompletedTaskRunWithTestSpec)
	ttrCompletedButNoFileSystemObservationsInTR := ttrCompletedButNoEnvDumpInTR.DeepCopy()
	ttrCompletedButNoFileSystemObservationsInTR.Name += "-no-expected-env"
	ttrCompletedButNoFileSystemObservationsInTR.Spec.TaskTestSpec.Expects.Env = nil
	ttrCompletedButNoFileSystemObservationsInTR.Status.TaskRunName = ptr.To("ttr-completed-task-run-run")

	task := parse.MustParseV1Task(t, tManifest)

	data := test.Data{
		Tasks:     []*v1.Task{task},
		TaskRuns:  []*v1.TaskRun{trCompletedNoEnvDump},
		TaskTests: []*v1alpha1.TaskTest{},
		TaskTestRuns: []*v1alpha1.TaskTestRun{ttrAbsentTaskTest, ttrAbsentTask,
			ttrInputsUndeclaredParam, ttrInputsUndeclaredEnvStep,
			ttrExpectsUndeclaredResult, ttrExpectsUndeclaredFileSystemStep, ttrExpectsUndeclaredEnvStep,
			ttrCompletedButNoEnvDumpInTR, ttrCompletedButNoFileSystemObservationsInTR},
	}

	type tc struct {
		ttr                *v1alpha1.TaskTestRun
		wantErr            error
		wantTtrStatus      *v1alpha1.TaskTestRunStatus
		wantCompletionTime bool
	}
	tests := map[string]tc{
		"ttr_references_absent_task_test": {
			ttr:     ttrAbsentTaskTest,
			wantErr: fmt.Errorf("could not prepare reconciliation of task test run invalid-ttr-absent-task-test: %w", apierrors.NewNotFound(schema.GroupResource{Group: "tekton.dev", Resource: "tasktests"}, "absent-task-test")),
		},
		"ttr_inputs_result_not_declared_in_task": {
			ttr: ttrInputsUndeclaredParam,
			wantTtrStatus: &v1alpha1.TaskTestRunStatus{
				Status: duckv1.Status{
					Conditions: duckv1.Conditions{
						{
							Type:   "Succeeded",
							Status: "False",
							Reason: "TaskTestRunValidationFailed",
							Message: `validation failed for referenced object: invalid value: foo: status.taskTestSpec.inputs.params[0].name
task "task" has no Param named "foo"`,
						},
					},
				},
				TaskTestRunStatusFields: v1alpha1.TaskTestRunStatusFields{
					TaskTestSpec: &v1alpha1.TaskTestSpec{
						TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
						Inputs: &v1alpha1.TaskTestInputs{Params: []v1.Param{{
							Name:  "foo",
							Value: v1.ParamValue{Type: "string", StringVal: "bar"},
						}}},
					},
				},
			},
			wantErr: errors.New(`validation failed for referenced object: invalid value: foo: status.taskTestSpec.inputs.params[0].name
task "task" has no Param named "foo"`),
			wantCompletionTime: true,
		},
		"ttr_inputs_step_for_stepEnv_not_declared_in_task": {
			ttr: ttrInputsUndeclaredEnvStep,
			wantTtrStatus: &v1alpha1.TaskTestRunStatus{
				Status: duckv1.Status{
					Conditions: duckv1.Conditions{
						{
							Type:   "Succeeded",
							Status: "False",
							Reason: "TaskTestRunValidationFailed",
							Message: `validation failed for referenced object: invalid value: goodbye-step: status.taskTestSpec.inputs.stepEnvs[0].stepName
task "task" has no Step named "goodbye-step"`,
						},
					},
				},
				TaskTestRunStatusFields: v1alpha1.TaskTestRunStatusFields{
					TaskTestSpec: &v1alpha1.TaskTestSpec{
						TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
						Inputs: &v1alpha1.TaskTestInputs{StepEnvs: []v1alpha1.StepEnv{{
							StepName: "goodbye-step",
							Env: []corev1.EnvVar{{
								Name:  "FOO",
								Value: "BAR",
							}},
						}}},
					},
				},
			},
			wantErr: errors.New(`validation failed for referenced object: invalid value: goodbye-step: status.taskTestSpec.inputs.stepEnvs[0].stepName
task "task" has no Step named "goodbye-step"`),
			wantCompletionTime: true,
		},
		"ttr_expects_result_not_declared_in_task": {
			ttr: ttrExpectsUndeclaredResult,
			wantTtrStatus: &v1alpha1.TaskTestRunStatus{
				Status: duckv1.Status{
					Conditions: duckv1.Conditions{
						{
							Type:   "Succeeded",
							Status: "False",
							Reason: "TaskTestRunValidationFailed",
							Message: `validation failed for referenced object: invalid value: current-date: status.taskTestSpec.expected.results[0].name
task "task" has no Result named "current-date"`,
						},
					},
				},
				TaskTestRunStatusFields: v1alpha1.TaskTestRunStatusFields{
					TaskTestSpec: &v1alpha1.TaskTestSpec{
						TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
						Expects: &v1alpha1.ExpectedOutcomes{
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
							SuccessStatus: ptr.To(true),
							SuccessReason: ptr.To(v1.TaskRunReasonSuccessful),
						},
					},
				},
			},
			wantErr: errors.New(`validation failed for referenced object: invalid value: current-date: status.taskTestSpec.expected.results[0].name
task "task" has no Result named "current-date"`),
			wantCompletionTime: true,
		},
		"ttr_expects_step_for_stepEnv_not_declared_in_task": {
			ttr: ttrExpectsUndeclaredEnvStep,
			wantTtrStatus: &v1alpha1.TaskTestRunStatus{
				Status: duckv1.Status{
					Conditions: duckv1.Conditions{
						{
							Type:   "Succeeded",
							Status: "False",
							Reason: "TaskTestRunValidationFailed",
							Message: `validation failed for referenced object: invalid value: goodbye-step: status.taskTestSpec.expected.stepEnvs[0].stepName
task "task" has no Step named "goodbye-step"`,
						},
					},
				},
				TaskTestRunStatusFields: v1alpha1.TaskTestRunStatusFields{
					TaskTestSpec: &v1alpha1.TaskTestSpec{
						TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
						Expects: &v1alpha1.ExpectedOutcomes{
							StepEnvs: []v1alpha1.StepEnv{{
								StepName: "goodbye-step",
								Env: []corev1.EnvVar{{
									Name:  "HOME",
									Value: "/root",
								}},
							}},
							SuccessStatus: ptr.To(true),
							SuccessReason: ptr.To(v1.TaskRunReason("Succeeded")),
						},
					},
				},
			},
			wantErr: errors.New(`validation failed for referenced object: invalid value: goodbye-step: status.taskTestSpec.expected.stepEnvs[0].stepName
task "task" has no Step named "goodbye-step"`),
			wantCompletionTime: true,
		},
		"ttr_expects_file_system_step_not_declared_in_task": {
			ttr: ttrExpectsUndeclaredFileSystemStep,
			wantTtrStatus: &v1alpha1.TaskTestRunStatus{
				Status: duckv1.Status{
					Conditions: duckv1.Conditions{
						{
							Type:   "Succeeded",
							Status: "False",
							Reason: "TaskTestRunValidationFailed",
							Message: `validation failed for referenced object: invalid value: goodbye-step: status.taskTestSpec.expected.fileSystemContents[0].stepName
task "task" has no Step named "goodbye-step"`,
						},
					},
				},
				TaskTestRunStatusFields: v1alpha1.TaskTestRunStatusFields{
					TaskTestSpec: &v1alpha1.TaskTestSpec{
						TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
						Expects: &v1alpha1.ExpectedOutcomes{
							FileSystemContents: []v1alpha1.ExpectedStepFileSystemContent{{
								StepName: "goodbye-step",
								Objects: []v1alpha1.FileSystemObject{{
									Path: "/tekton/results/current-date",
									Type: "Directory",
								}, {
									Path:    "/tekton/results/current-time",
									Type:    "TextFile",
									Content: "foo",
								}},
							}},
							SuccessStatus: ptr.To(true),
							SuccessReason: ptr.To(v1.TaskRunReason("Succeeded")),
						},
					},
				},
			},
			wantErr: errors.New(`validation failed for referenced object: invalid value: goodbye-step: status.taskTestSpec.expected.fileSystemContents[0].stepName
task "task" has no Step named "goodbye-step"`),
			wantCompletionTime: true,
		},
		"tt_references_absent_task": {
			ttr:     ttrAbsentTask,
			wantErr: fmt.Errorf("could not dereference task under test: %w", apierrors.NewNotFound(schema.GroupResource{Group: "tekton.dev", Resource: "tasks"}, "absent-task")),
			wantTtrStatus: &v1alpha1.TaskTestRunStatus{
				Status: duckv1.Status{Conditions: duckv1.Conditions{{
					Type:   "Succeeded",
					Status: "Unknown",
					Reason: "Started",
				}}},
				TaskTestRunStatusFields: v1alpha1.TaskTestRunStatusFields{
					TaskTestSpec: &v1alpha1.TaskTestSpec{TaskRef: &v1alpha1.SimpleTaskRef{Name: "absent-task"}},
				},
			},
		},
		"ttr_expects_env_value_but_no_dump_in_tr": {
			ttr: ttrCompletedButNoEnvDumpInTR,
			wantErr: errors.New(`error occurred while checking expectations: error while checking the expectations for env: could not find environment dump for stepEnv
error while checking the expectations for file system objects: could not find result with file system observations`),
			wantTtrStatus: &v1alpha1.TaskTestRunStatus{
				Status: duckv1.Status{Conditions: duckv1.Conditions{{
					Type:   "Succeeded",
					Status: "False",
					Reason: "TaskTestRunValidationFailed",
					Message: `error occurred while checking expectations: error while checking the expectations for env: could not find environment dump for stepEnv
error while checking the expectations for file system objects: could not find result with file system observations`,
				}}},
				TaskTestRunStatusFields: v1alpha1.TaskTestRunStatusFields{
					TaskRunName: ptr.To("ttr-completed-task-run-run"),
					TaskTestSpec: &v1alpha1.TaskTestSpec{
						TaskRef: &v1alpha1.SimpleTaskRef{Name: "hello-task"},
						Expects: &v1alpha1.ExpectedOutcomes{
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
							Env: []corev1.EnvVar{{
								Name:  "HOME",
								Value: "/root",
							}},
							FileSystemContents: []v1alpha1.ExpectedStepFileSystemContent{{
								StepName: "hello-step",
								Objects: []v1alpha1.FileSystemObject{{
									Path:    "/tekton/results/current-date",
									Type:    "TextFile",
									Content: "bar",
								}},
							}},
							SuccessStatus: ptr.To(true),
							SuccessReason: ptr.To(v1.TaskRunReasonSuccessful),
						},
					},
					Outcomes: &v1alpha1.ObservedOutcomes{
						Results: &[]v1alpha1.ObservedResults{
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
						SuccessStatus: &v1alpha1.ObservedSuccessStatus{
							Want: true,
							Got:  true,
						},
						SuccessReason: &v1alpha1.ObservedSuccessReason{
							Want: v1.TaskRunReasonSuccessful,
							Got:  v1.TaskRunReasonSuccessful,
						},
					},
				},
			},
			wantCompletionTime: true,
		},
		"ttr_expects_fs_observations_but_no_observations_in_tr": {
			ttr:     ttrCompletedButNoFileSystemObservationsInTR,
			wantErr: errors.New("error occurred while checking expectations: error while checking the expectations for file system objects: could not find result with file system observations"),
			wantTtrStatus: &v1alpha1.TaskTestRunStatus{
				Status: duckv1.Status{Conditions: duckv1.Conditions{{
					Type:    "Succeeded",
					Status:  "False",
					Reason:  "TaskTestRunValidationFailed",
					Message: "error occurred while checking expectations: error while checking the expectations for file system objects: could not find result with file system observations",
				}}},
				TaskTestRunStatusFields: v1alpha1.TaskTestRunStatusFields{
					TaskRunName: ptr.To("ttr-completed-task-run-run"),
					TaskTestSpec: &v1alpha1.TaskTestSpec{
						TaskRef: &v1alpha1.SimpleTaskRef{Name: "hello-task"},
						Expects: &v1alpha1.ExpectedOutcomes{
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
							FileSystemContents: []v1alpha1.ExpectedStepFileSystemContent{{
								StepName: "hello-step",
								Objects: []v1alpha1.FileSystemObject{{
									Path:    "/tekton/results/current-date",
									Type:    "TextFile",
									Content: "bar",
								}},
							}},
							SuccessStatus: ptr.To(true),
							SuccessReason: ptr.To(v1.TaskRunReasonSuccessful),
						},
					},
					Outcomes: &v1alpha1.ObservedOutcomes{
						Results: &[]v1alpha1.ObservedResults{
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
						SuccessStatus: &v1alpha1.ObservedSuccessStatus{
							Want: true,
							Got:  true,
						},
						SuccessReason: &v1alpha1.ObservedSuccessReason{
							Want: v1.TaskRunReasonSuccessful,
							Got:  v1.TaskRunReasonSuccessful,
						},
					},
				},
			},
			wantCompletionTime: true,
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
				ignoreTaskRunStatus,
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

func getRunName(tr *v1alpha1.TaskTestRun) string {
	return strings.Join([]string{tr.Namespace, tr.Name}, "/")
}

type statusPatchFunc = func(*v1alpha1.TaskTestRun)

func patchTaskTestRun(ttrs *v1alpha1.TaskTestRun, pf statusPatchFunc) *v1alpha1.TaskTestRunStatus {
	ttrsCopy := ttrs.DeepCopy()
	pf(ttrsCopy)
	return &ttrsCopy.Status
}
