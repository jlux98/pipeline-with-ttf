package tasktestrun

import (
	"context"
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

var (
	ignoreObjectMeta                = cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ResourceVersion")
	ignoreLastTransitionTime        = cmpopts.IgnoreFields(apis.Condition{}, "LastTransitionTime.Inner.Time")
	ignoreTaskRunStatus             = cmpopts.IgnoreFields(v1alpha1.TaskTestRunStatusFields{}, "TaskRunStatus")
	ignoreStartTimeTaskRun          = cmpopts.IgnoreFields(v1.TaskRunStatusFields{}, "StartTime")
	ignoreStartTimeTaskTestRun      = cmpopts.IgnoreFields(v1alpha1.TaskTestRunStatusFields{}, "StartTime")
	ignoreCompletionTimeTaskTestRun = cmpopts.IgnoreFields(v1alpha1.TaskTestRunStatusFields{}, "CompletionTime")
)

var now = time.Date(2022, time.January, 1, 0, 0, 0, 0, time.UTC)
var testClock = clock.NewFakePassiveClock(now)

var images = pipeline.Images{
	EntrypointImage: "override-with-entrypoint:latest",
	NopImage:        "override-with-nop:latest",
	ShellImage:      "busybox",
}

const taskManifest = `
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
    name: simple-step
`

const taskManifestHelloTask = `
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
      date +%y-%m-%d | tee $(results.current-date.path)
`

const taskTestRunManifestNewTaskRun = `metadata:
  name: task-test-run
  namespace: foo
spec:
  taskTestSpec:
    name: task-test
    spec:
    taskRef:
      name: task
`
const taskTestRunManifestExistingTaskRun = `metadata:
  name: task-test-run1
  namespace: foo
spec:
  taskTestSpec:
    name: task-test
    spec:
    taskRef:
      name: task
status:
  startTime:  "2025-08-15T15:17:55Z"
  conditions:
  - type: Succeeded
    reason: Started
    status: Unknown`

const taskTestRunManifestCompletedTaskRun = `metadata:
  name: task-test-run2
  namespace: foo
spec:
  taskTestSpec:
    name: task-test
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
      successReason: Succeeded
status:
  startTime:  "2025-08-15T15:17:55Z"
  conditions:
  - type: Succeeded
    reason: Started
    status: Unknown`

const taskRunManifestJustStarted = `
metadata:
  name: task-test-run-run
  namespace: foo
  labels:
    tekton.dev/taskTestRun: task-test-run
  ownerReferences:
  - apiVersion: tekton.dev/v1alpha1
    kind: TaskTestRun
    name: task-test-run
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

const taskRunManifestAlreadyStarted = `
metadata:
  name: task-test-run123-run
  namespace: foo
  labels:
    tekton.dev/taskTestRun: task-test-run1
  ownerReferences:
  - apiVersion: tekton.dev/v1alpha1
    kind: TaskTestRun
    name: task-test-run1
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

const taskRunManifestCompleted = `
metadata:
  name: task-test-run2-run
  namespace: foo
  labels:
    tekton.dev/taskTestRun: task-test-run2
  ownerReferences:
  - apiVersion: tekton.dev/v1alpha1
    kind: TaskTestRun
    name: task-test-run2
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

func TestReconciler_ReconcileKind(t *testing.T) {
	task := parse.MustParseV1Task(t, taskManifest)
	taskHelloTask := parse.MustParseV1Task(t, taskManifestHelloTask)
	taskTestRun := parse.MustParseTaskTestRun(t, taskTestRunManifestNewTaskRun)
	taskTestRunExistingTaskRun := parse.MustParseTaskTestRun(t, taskTestRunManifestExistingTaskRun)
	taskTestRunCompletedTaskRun := parse.MustParseTaskTestRun(t, taskTestRunManifestCompletedTaskRun)
	taskRunAlreadyStarted := parse.MustParseV1TaskRun(t, taskRunManifestAlreadyStarted)
	taskRunCompleted := parse.MustParseV1TaskRun(t, taskRunManifestCompleted)

	d := test.Data{
		Tasks:        []*v1.Task{task, taskHelloTask},
		TaskRuns:     []*v1.TaskRun{taskRunAlreadyStarted, taskRunCompleted},
		TaskTestRuns: []*v1alpha1.TaskTestRun{taskTestRun, taskTestRunExistingTaskRun, taskTestRunCompletedTaskRun},
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
					TaskRunName:  "task-test-run-run",
					TaskTestSpec: &v1alpha1.NamedTaskTestSpec{Spec: &v1alpha1.TaskTestSpec{TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"}}},
				},
			},
			wantTr:        parse.MustParseV1TaskRun(t, taskRunManifestJustStarted),
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
			wantTr:        parse.MustParseV1TaskRun(t, taskRunManifestAlreadyStarted),
			wantStartTime: true,
		},
		"completed run with expected results": {
			ttr: taskTestRunCompletedTaskRun,
			wantTtrStatus: &v1alpha1.TaskTestRunStatus{
				Status: duckv1.Status{Conditions: duckv1.Conditions{{
					Type:   "Succeeded",
					Status: "True",
					Reason: "All Expectations were met.",
				}}},
				TaskTestRunStatusFields: v1alpha1.TaskTestRunStatusFields{
					TaskRunName: "task-test-run2-run",
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
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			testAssets, cancel := getTaskTestRunController(t, d)
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
				ignoreObjectMeta,
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
				ignoreObjectMeta,
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

// getTaskTestRunController returns an instance of the TaskTestRun controller/reconciler that has been seeded with
// d, where d represents the state of the system (existing resources) needed for the test.
func getTaskTestRunController(t *testing.T, d test.Data) (test.Assets, func()) {
	t.Helper()
	names.TestingSeed()
	return initializeTaskTestRunControllerAssets(t, d, pipeline.Options{Images: images})
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
