package tasktestrun

import (
	"context"

	"github.com/tektoncd/pipeline/pkg/apis/pipeline"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1alpha1"
	clientset "github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
	tasktestrunreconciler "github.com/tektoncd/pipeline/pkg/client/injection/reconciler/pipeline/v1alpha1/tasktestrun"
	v1listers "github.com/tektoncd/pipeline/pkg/client/listers/pipeline/v1"
	alphalisters "github.com/tektoncd/pipeline/pkg/client/listers/pipeline/v1alpha1"
	podconvert "github.com/tektoncd/pipeline/pkg/pod"
	"github.com/tektoncd/pipeline/pkg/reconciler/events"
	"github.com/tektoncd/pipeline/pkg/reconciler/events/cloudevent"
	"github.com/tektoncd/pipeline/pkg/reconciler/taskrun/resources"
	"github.com/tektoncd/pipeline/pkg/reconciler/volumeclaim"
	resolution "github.com/tektoncd/pipeline/pkg/remoteresolution/resource"
	"github.com/tektoncd/pipeline/pkg/spire"
	"github.com/tektoncd/pipeline/pkg/taskrunmetrics"
	"go.opentelemetry.io/otel/trace"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	corev1Listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/utils/clock"
	"knative.dev/pkg/apis"
	"knative.dev/pkg/logging"
	"knative.dev/pkg/reconciler"
)

// Reconciler implements controller.Reconciler for Configuration resources.
type Reconciler struct {
	KubeClientSet     kubernetes.Interface
	PipelineClientSet clientset.Interface
	Images            pipeline.Images
	Clock             clock.PassiveClock

	// listers index properties about resources
	spireClient              spire.ControllerAPIClient
	taskTestRunLister        alphalisters.TaskTestRunLister
	taskRunLister            v1listers.TaskRunLister
	limitrangeLister         corev1Listers.LimitRangeLister
	podLister                corev1Listers.PodLister
	verificationPolicyLister alphalisters.VerificationPolicyLister
	cloudEventClient         cloudevent.CEClient
	entrypointCache          podconvert.EntrypointCache
	metrics                  *taskrunmetrics.Recorder
	pvcHandler               volumeclaim.PvcHandler
	resolutionRequester      resolution.Requester
	tracerProvider           trace.TracerProvider
}

// ReconcileKind implements tasktestrun.Interface.
func (c *Reconciler) ReconcileKind(ctx context.Context, tr *v1alpha1.TaskTestRun) reconciler.Event {
	logger := logging.FromContext(ctx)

	logger.Info("unga bunga")

	ctx = cloudevent.ToContext(ctx, c.cloudEventClient)

	if !tr.HasStarted() {
		tr.Status.InitializeConditions()
		// In case node time was not synchronized, when controller has been scheduled to other nodes.
		if tr.Status.StartTime.Sub(tr.CreationTimestamp.Time) < 0 {
			logger.Warnf("TaskRun %s createTimestamp %s is after the taskRun started %s", tr.GetNamespacedName().String(), tr.CreationTimestamp, tr.Status.StartTime)
			tr.Status.StartTime = &tr.CreationTimestamp
		}
		// Emit events. During the first reconcile the status of the TaskRun may change twice
		// from not Started to Started and then to Running, so we need to sent the event here
		// and at the end of 'Reconcile' again.
		// We also want to send the "Started" event as soon as possible for anyone who may be waiting
		// on the event to perform user facing initialisations, such has reset a CI check status
		afterCondition := tr.Status.GetCondition(apis.ConditionSucceeded)
		events.Emit(ctx, nil, afterCondition, tr)

		err := c.reconcile(ctx, tr, nil)
		return err
	}

	// if tr.Status.StartTime != nil {
	// 	// Compute the time since the task started.
	// 	elapsed := c.Clock.Since(tr.Status.StartTime.Time)
	// 	// Snooze this resource until the timeout has elapsed.
	// 	timeout := tr.GetTimeout(ctx)
	// 	waitTime := timeout - elapsed
	// 	if timeout == config.NoTimeoutDuration {
	// 		waitTime = time.Duration(config.FromContextOrDefaults(ctx).Defaults.DefaultTimeoutMinutes) * time.Minute
	// 	}
	// 	return controller.NewRequeueAfter(waitTime)
	// }

	return nil
}

// `reconcile` creates the TaskRun associated to the TaskTestRun, and it pulls
// back status updates from the TaskRun to the TaskTestRun.
// It reports errors back to Reconcile, it updates the tasktest run status in
// case of error but it does not sync updates back to etcd. It does not emit
// events. `reconcile` consumes spec and resources returned by `prepare`
func (c *Reconciler) reconcile(ctx context.Context, ttr *v1alpha1.TaskTestRun, rtr *resources.ResolvedTask) error {

	logger := logging.FromContext(ctx)
	// recorder := controller.GetEventRecorder(ctx)
	var err error

	// Get the TaskTestRun's TaskRun if it should have one. Otherwise, create the TaskRun.
	var taskRun *v1.TaskRun

	if ttr.Status.TaskRunName != "" {
		taskRun, err = c.taskRunLister.TaskRuns(ttr.Namespace).Get(ttr.Status.TaskRunName)
		if k8serrors.IsNotFound(err) {
			// Keep going, this will result in the TaskRun being created below.
		} else if err != nil {
			// This is considered a transient error, so we return error, do not update
			// the task test run condition, and return an error which will cause this key to
			// be requeued for reconcile.
			logger.Errorf("Error getting TaskRun %q: %v", ttr.Status.TaskRunName, err)
			return err
		}
	} else {
		// List TaskRuns that have a label with this TaskTestRun name.  Do not include other labels from the
		// TaskTestRun in this selector.  The user could change them during the lifetime of the TasTestkRun so the
		// current labels may not be set on a previously created TaskRun.
		labelSelector := labels.Set{pipeline.TaskTestRunLabelKey: ttr.Name}
		trs, err := c.taskRunLister.TaskRuns(ttr.Namespace).List(labelSelector.AsSelector())
		if err != nil {
			logger.Errorf("Error listing task runs: %v", err)
			return err
		}
		for index := range trs {
			tr := trs[index]
			if metav1.IsControlledBy(tr, ttr) {
				taskRun = tr
			}
		}
	}

	// // Please note that this block is required to run before `applyParamsContextsResultsAndWorkspaces` is called the first time,
	// // and that `applyParamsContextsResultsAndWorkspaces` _must_ be called on every reconcile.
	// if taskRun == nil && ttr.HasVolumeClaimTemplate() {
	// 	for _, ws := range ttr.Spec.Workspaces {
	// 		if err := c.pvcHandler.CreatePVCFromVolumeClaimTemplate(ctx, ws, *kmeta.NewControllerRef(ttr), ttr.Namespace); err != nil {
	// 			logger.Errorf("Failed to create PVC for TaskRun %s: %v", ttr.Name, err)
	// 			ttr.Status.MarkResourceFailed(volumeclaim.ReasonCouldntCreateWorkspacePVC,
	// 				fmt.Errorf("failed to create PVC for TaskRun %s workspaces correctly: %w",
	// 					fmt.Sprintf("%s/%s", ttr.Namespace, ttr.Name), err))
	// 			return controller.NewPermanentError(err)
	// 		}
	// 	}

	// 	taskRunWorkspaces := applyVolumeClaimTemplates(ttr.Spec.Workspaces, *kmeta.NewControllerRef(ttr))
	// 	// This is used by createPod below. Changes to the Spec are not updated.
	// 	ttr.Spec.Workspaces = taskRunWorkspaces
	// }

	// resources.ApplyParametersToWorkspaceBindings(rtr.TaskSpec, ttr)
	// // Get the randomized volume names assigned to workspace bindings
	// workspaceVolumes := workspace.CreateVolumes(ttr.Spec.Workspaces)

	// ts, err := applyParamsContextsResultsAndWorkspaces(ctx, ttr, rtr, workspaceVolumes)
	// if err != nil {
	// 	logger.Errorf("Error updating task spec parameters, contexts, results and workspaces: %s", err)
	// 	return err
	// }
	// ttr.Status.TaskSpec = ts

	// if len(ttr.Status.TaskSpec.Steps) > 0 {
	// 	logger.Debugf("set taskspec for %s/%s - script: %s", ttr.Namespace, ttr.Name, ttr.Status.TaskSpec.Steps[0].Script)
	// }

	if taskRun == nil {
		taskRun, err = c.createTaskRun(ctx, ttr, rtr)
		if err != nil {
			logger.Errorf("Failed to create task run taskRun for taskrun %q: %v", ttr.Name, err)
			return err
		}
	}

	// if podconvert.IsPodExceedingNodeResources(taskRun) {
	// 	recorder.Eventf(ttr, corev1.EventTypeWarning, podconvert.ReasonExceededNodeResources, "Insufficient resources to schedule taskRun %q", taskRun.Name)
	// }

	// if podconvert.SidecarsReady(taskRun.Status) {
	// 	if err := podconvert.UpdateReady(ctx, c.KubeClientSet, *taskRun); err != nil {
	// 		return err
	// 	}
	// 	if err := c.metrics.RecordPodLatency(ctx, taskRun, ttr); err != nil {
	// 		logger.Warnf("Failed to log the metrics : %v", err)
	// 	}
	// }

	// // Convert the taskRun's status to the equivalent TaskRun Status.
	// ttr.Status, err = podconvert.MakeTaskRunStatus(ctx, logger, *ttr, taskRun, c.KubeClientSet, rtr.TaskSpec)
	// if err != nil {
	// 	return err
	// }

	// if err := validateTaskRunResults(ttr, rtr.TaskSpec); err != nil {
	// 	ttr.Status.MarkResourceFailed(v1.TaskRunReasonFailedValidation, err)
	// 	return err
	// }

	logger.Infof("Successfully reconciled taskrun %s/%s with status: %#v", ttr.Name, ttr.Namespace, ttr.Status.GetCondition(apis.ConditionSucceeded))
	return nil
}

func (c *Reconciler) createTaskRun(ctx context.Context, ttr *v1alpha1.TaskTestRun, rtr *resources.ResolvedTask) (*v1.TaskRun, error) {
	if ttr.Spec.TaskTestSpec != nil {
		task, err := c.PipelineClientSet.TektonV1().Tasks(ttr.Namespace).Get(ctx, ttr.Spec.TaskTestSpec.TaskRef.Name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		ttr.Status.TaskRunName = ttr.GetObjectMeta().GetName() + "-run"
		taskRun := &v1.TaskRun{
			TypeMeta:   metav1.TypeMeta{Kind: "TaskRun", APIVersion: "tekton.dev/v1"},
			ObjectMeta: metav1.ObjectMeta{Name: ttr.Status.TaskRunName, Namespace: ttr.Namespace},
			Spec:       v1.TaskRunSpec{TaskRef: &v1.TaskRef{Name: task.Name}},
		}
		taskRun.Status.InitializeConditions()
		taskRunAfterCreation, err := c.PipelineClientSet.TektonV1().TaskRuns(ttr.Namespace).Create(ctx, taskRun, metav1.CreateOptions{})
		return taskRunAfterCreation, err
	}
	return nil, nil
}

// Check that our Reconciler implements taskrunreconciler.Interface
var _ tasktestrunreconciler.Interface = (*Reconciler)(nil)
