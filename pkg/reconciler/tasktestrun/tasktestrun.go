package tasktestrun

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/tektoncd/pipeline/pkg/apis/config"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1alpha1"
	clientset "github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
	tasktestrunreconciler "github.com/tektoncd/pipeline/pkg/client/injection/reconciler/pipeline/v1alpha1/tasktestrun"
	v1listers "github.com/tektoncd/pipeline/pkg/client/listers/pipeline/v1"
	alphalisters "github.com/tektoncd/pipeline/pkg/client/listers/pipeline/v1alpha1"
	"github.com/tektoncd/pipeline/pkg/reconciler/events"
	"github.com/tektoncd/pipeline/pkg/reconciler/events/cloudevent"
	"github.com/tektoncd/pipeline/pkg/reconciler/taskrun/resources"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/clock"
	"knative.dev/pkg/apis"
	"knative.dev/pkg/controller"
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
	taskTestRunLister alphalisters.TaskTestRunLister
	taskRunLister     v1listers.TaskRunLister
	cloudEventClient  cloudevent.CEClient
	// spireClient              spire.ControllerAPIClient
	// limitrangeLister         corev1Listers.LimitRangeLister
	// podLister                corev1Listers.PodLister
	// verificationPolicyLister alphalisters.VerificationPolicyLister
	// entrypointCache          podconvert.EntrypointCache
	// metrics                  *taskrunmetrics.Recorder
	// pvcHandler               volumeclaim.PvcHandler
	// resolutionRequester      resolution.Requester
	// tracerProvider           trace.TracerProvider
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
	}

	if err := c.reconcile(ctx, tr, nil); err != nil {
		logger.Errorf("Reconcile: %v", err.Error())
		return err
	}

	// // Emit events (only when ConditionSucceeded was changed)
	// if err := c.finishReconcileUpdateEmitEvents(ctx, tr, before, err); err != nil {
	// 	return err
	// }

	if tr.Status.StartTime != nil {
		// Compute the time since the task started.
		elapsed := c.Clock.Since(tr.Status.StartTime.Time)
		// Snooze this resource until the timeout has elapsed.
		timeout := tr.GetTimeout(ctx)
		waitTime := timeout - elapsed
		if timeout == config.NoTimeoutDuration {
			waitTime = time.Duration(config.FromContextOrDefaults(ctx).Defaults.DefaultTimeoutMinutes) * time.Minute
		}
		return controller.NewRequeueAfter(waitTime)
	}

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

	if ttr.Spec.TaskTestSpec != nil {
		ttr.Status.TaskTestSpec = &v1alpha1.NamedTaskTestSpec{
			Spec: ttr.Spec.TaskTestSpec,
		}
	}
	if ttr.Spec.TaskTestRef != nil {
		logger.Info(`// TODO deref TaskTestRef and copy spec `)
	}

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
	} else if taskRun.Status.CompletionTime != nil {
		expectationsMet := true
		ttr.Status.CompletionTime = taskRun.Status.CompletionTime
		taskResults := taskRun.Status.Results
		for _, expectedResult := range ttr.Status.TaskTestSpec.Spec.Expected.Results {
			var gotValue *v1.ResultValue
			i := slices.IndexFunc(taskResults, func(actualResult v1.TaskRunResult) bool {
				return actualResult.Name == expectedResult.Name
			})
			if i == -1 {
				gotValue = nil
				expectationsMet = false
			} else {
				gotValue = &taskResults[i].Value
			}
			diff := cmp.Diff(expectedResult.Value, gotValue)
			ttr.Status.Outcomes.Results = append(ttr.Status.Outcomes.Results, v1alpha1.ObservedResults{
				Name: expectedResult.Name,
				Want: expectedResult.Value,
				Got:  gotValue,
				Diff: diff,
			})
			if diff != "" {
				expectationsMet = false
			}
		}
		ttr.Status.Outcomes.SuccessStatus = v1alpha1.ObservedSuccessStatus{
			Want: ttr.Status.TaskTestSpec.Spec.Expected.SuccessStatus,
			Got:  taskRun.IsSuccessful(),
		}
		ttr.Status.Outcomes.SuccessStatus.WantMatchesGot = ttr.Status.Outcomes.SuccessStatus.Want == ttr.Status.Outcomes.SuccessStatus.Got

		if !ttr.Status.Outcomes.SuccessStatus.WantMatchesGot {
			expectationsMet = false
		}

		ttr.Status.Outcomes.SuccessReason = v1alpha1.ObservedSuccessReason{
			Want: ttr.Status.TaskTestSpec.Spec.Expected.SuccessReason,
			Got:  v1.TaskRunReason(taskRun.Status.Conditions[0].Reason),
		}
		ttr.Status.Outcomes.SuccessReason.Diff = cmp.Diff(ttr.Status.Outcomes.SuccessReason.Want, ttr.Status.Outcomes.SuccessReason.Got)

		if ttr.Status.Outcomes.SuccessReason.Diff != "" {
			expectationsMet = false
		}

		if expectationsMet {
			ttr.Status.MarkSuccessful()
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

	ttr.Status.TaskRunName = taskRun.Name
	ttr.Status.TaskRunStatus = taskRun.Status

	logger.Infof("Successfully reconciled taskrun %s/%s with status: %#v", ttr.Name, ttr.Namespace, ttr.Status.GetCondition(apis.ConditionSucceeded))
	return nil
}

func (c *Reconciler) createTaskRun(ctx context.Context, ttr *v1alpha1.TaskTestRun, rtr *resources.ResolvedTask) (*v1.TaskRun, error) {
	var taskRun *v1.TaskRun
	var taskName string

	if ttr.Spec.TaskTestSpec != nil {
		taskName = ttr.Spec.TaskTestSpec.TaskRef.Name
	}

	task, err := c.PipelineClientSet.TektonV1().Tasks(ttr.Namespace).Get(ctx, taskName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	if ttr.Status.TaskTestSpec.Spec.Expected.Results != nil {
		declaredResults := task.Spec.Results
		for _, expectedResult := range ttr.Status.TaskTestSpec.Spec.Expected.Results {
			if !slices.ContainsFunc(declaredResults, func(result v1.TaskResult) bool {
				return result.Name == expectedResult.Name
			}) {
				return nil, fmt.Errorf("error: Result %s expected but not declared by task %s", expectedResult.Name, taskName)
			}
		}
	}

	taskRun = &v1.TaskRun{
		TypeMeta: metav1.TypeMeta{Kind: "TaskRun", APIVersion: "tekton.dev/v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:            ttr.GetObjectMeta().GetName() + "-run",
			Namespace:       ttr.Namespace,
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(ttr, schema.GroupVersionKind{Group: "tekton.dev", Version: "v1alpha1", Kind: "TaskTestRun"})},
			Labels:          map[string]string{"tekton.dev/taskTestRun": ttr.Name},
		},
		Spec: v1.TaskRunSpec{TaskRef: &v1.TaskRef{Name: task.Name}},
	}
	taskRun.Status.InitializeConditions()

	taskRun, err = c.PipelineClientSet.TektonV1().TaskRuns(ttr.Namespace).Create(ctx, taskRun, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	return taskRun, nil
}

// Check that our Reconciler implements taskrunreconciler.Interface
var _ tasktestrunreconciler.Interface = (*Reconciler)(nil)
