package tasktestsuiterun

import (
	"context"

	"github.com/tektoncd/pipeline/pkg/apis/config"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1alpha1"
	pipelineclient "github.com/tektoncd/pipeline/pkg/client/injection/client"
	tasktestruninformer "github.com/tektoncd/pipeline/pkg/client/injection/informers/pipeline/v1alpha1/tasktestrun"
	tasktestsuiteruninformer "github.com/tektoncd/pipeline/pkg/client/injection/informers/pipeline/v1alpha1/tasktestsuiterun"
	"github.com/tektoncd/pipeline/pkg/client/injection/reconciler/pipeline/v1alpha1/tasktestsuiterun"
	"github.com/tektoncd/pipeline/pkg/reconciler/volumeclaim"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/clock"
	kubeclient "knative.dev/pkg/client/injection/kube/client"
	"knative.dev/pkg/configmap"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/logging"
)

func NewController(opts *pipeline.Options, clock clock.PassiveClock) func(context.Context, configmap.Watcher) *controller.Impl {
	return func(ctx context.Context, cmw configmap.Watcher) *controller.Impl {
		logger := logging.FromContext(ctx)

		configStore := config.NewStore(logger.Named("config-store"))
		configStore.WatchConfigs(cmw)

		// Access informers
		taskTestSuiteRunInformer := tasktestsuiteruninformer.Get(ctx)
		taskTestRunInformer := tasktestruninformer.Get(ctx)

		// TODO(jlu98): Access informers

		c := &Reconciler{
			// TODO(jlu98): Pass listers, clients, and other stuff.
			KubeClientSet:          kubeclient.Get(ctx),
			TaskTestRunLister:      taskTestRunInformer.Lister(),
			TaskTestSuiteRunLister: taskTestSuiteRunInformer.Lister(),
			PipelineClientSet:      pipelineclient.Get(ctx),
			Images:                 opts.Images,
			Clock:                  clock,
			pvcHandler:             volumeclaim.NewPVCHandler(kubeclient.Get(ctx), logger),
		}
		logger = logger.Named("tasktestsuiterun-reconciler")
		impl := tasktestsuiterun.NewImpl(ctx, c, func(impl *controller.Impl) controller.Options {
			return controller.Options{AgentName: pipeline.TaskTestRunControllerName}
		})

		// TODO(jlu98): Set up event handlers.
		if _, err := taskTestSuiteRunInformer.Informer().AddEventHandler(controller.HandleAll(impl.Enqueue)); err != nil {
			logging.FromContext(ctx).Panicf("Couldn't register TaskTestRun informer event handler: %w", err)
		}

		if _, err := taskTestRunInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
			FilterFunc: controller.FilterController(&v1alpha1.TaskTestSuiteRun{}),
			Handler:    controller.HandleAll(impl.EnqueueControllerOf),
		}); err != nil {
			logging.FromContext(ctx).Panicf("Couldn't register TaskRun informer event handler: %w", err)
		}

		return impl
	}
}
