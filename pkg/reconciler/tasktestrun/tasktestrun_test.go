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
	cminformer "knative.dev/pkg/configmap/informer"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/logging"
	logtesting "knative.dev/pkg/logging/testing"
	pkgreconciler "knative.dev/pkg/reconciler"
	"knative.dev/pkg/system"
	_ "knative.dev/pkg/system/testing" // Setup system.Namespace()
)

var ignoreObjectMeta = cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ResourceVersion")
var now = time.Date(2022, time.January, 1, 0, 0, 0, 0, time.UTC)
var testClock = clock.NewFakePassiveClock(now)

var images = pipeline.Images{
	EntrypointImage: "override-with-entrypoint:latest",
	NopImage:        "override-with-nop:latest",
	ShellImage:      "busybox",
}

func TestReconciler_ReconcileKind(t *testing.T) {
	task := parse.MustParseV1Task(t, `
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
`)
	taskTestRun := parse.MustParseTaskTestRun(t, `
metadata:
  name: task-test-run
  namespace: foo
spec:
  taskTestSpec:
    name: task-test
    spec:
    taskRef:
      name: task
`)

	d := test.Data{
		Tasks:        []*v1.Task{task},
		TaskTestRuns: []*v1alpha1.TaskTestRun{taskTestRun},
	}

	type tc struct {
		ttr       *v1alpha1.TaskTestRun
		wantTtr   *v1alpha1.TaskTestRun
		wantTr    *v1.TaskRun
		wantError error
	}
	tests := map[string]tc{
		"no start time": tc{
			ttr: taskTestRun,
			wantTtr: parse.MustParseTaskTestRun(t, `
metadata:
  name: task-test-run
  namespace: foo
spec:
  taskTestRef:
    name: task-test
status:
  conditions:
    - lastTransitionTime: '`+time.Now().Truncate(time.Second).Format(time.RFC3339)+`'
      reason: Started
      status: Unknown
      type: Succeeded
  startTime: '`+time.Now().Truncate(time.Second).Format(time.RFC3339)+`'
  taskRunName: 'task-test-run-run'`),
			wantTr: parse.MustParseV1TaskRun(t, `
metadata:
  name: task-test-run-run
  namespace: foo
spec:
  taskRef:
    name: task
status:
  conditions:
    - lastTransitionTime: '`+time.Now().Truncate(time.Second).Format(time.RFC3339)+`'
      reason: Started
      status: Unknown
      type: Succeeded
  startTime: '`+time.Now().Truncate(time.Second).Format(time.RFC3339)+`'`),
			wantError: nil,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			testAssets, cancel := getTaskTestRunController(t, d)
			clients := testAssets.Clients
			defer cancel()

			got := testAssets.Controller.Reconciler.Reconcile(testAssets.Ctx, getRunName(tt.ttr))

			if d := cmp.Diff(tt.wantError, got, ignoreObjectMeta); d != "" {
				t.Errorf("Didn't get expected TaskTestRun: %v", diff.PrintWantGot(d))
			}

			ttr, err := clients.Pipeline.TektonV1alpha1().TaskTestRuns(tt.ttr.Namespace).Get(testAssets.Ctx, tt.ttr.Name, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("getting updated tasktestrun: %v", err)
			}
			ttr.Status.StartTime = &metav1.Time{Time: ttr.Status.StartTime.Truncate(time.Second)}
			ttr.Status.Conditions[0].LastTransitionTime.Inner = metav1.Time{Time: ttr.Status.Conditions[0].LastTransitionTime.Inner.Truncate(time.Second)}
			// tr.Status.Conditions.
			if d := cmp.Diff(tt.wantTtr.Status, ttr.Status, ignoreObjectMeta); d != "" {
				t.Errorf("Didn't get expected TaskTestRun: %v", diff.PrintWantGot(d))
			}

			tr, err := clients.Pipeline.TektonV1().TaskRuns(tt.ttr.Namespace).Get(testAssets.Ctx, ttr.Status.TaskRunName, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("getting updated taskrun: %v", err)
			}
			tr.Status.StartTime = &metav1.Time{Time: tr.Status.StartTime.Truncate(time.Second)}
			tr.Status.Conditions[0].LastTransitionTime.Inner = metav1.Time{Time: tr.Status.Conditions[0].LastTransitionTime.Inner.Truncate(time.Second)}
			if d := cmp.Diff(tt.wantTr, tr, ignoreObjectMeta); d != "" {
				t.Errorf("Didn't get expected TaskRun: %v", diff.PrintWantGot(d))
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
