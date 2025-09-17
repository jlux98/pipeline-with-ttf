package tasktestsuiterun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/tektoncd/pipeline/pkg/apis/config"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline"
	pipelineErrors "github.com/tektoncd/pipeline/pkg/apis/pipeline/errors"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	v1alpha1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1alpha1"
	clientset "github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
	tasktestsuiterunreconciler "github.com/tektoncd/pipeline/pkg/client/injection/reconciler/pipeline/v1alpha1/tasktestsuiterun"
	alphalisters "github.com/tektoncd/pipeline/pkg/client/listers/pipeline/v1alpha1"
	"github.com/tektoncd/pipeline/pkg/reconciler/events"
	"github.com/tektoncd/pipeline/pkg/reconciler/events/cloudevent"
	"github.com/tektoncd/pipeline/pkg/reconciler/taskrun/resources"
	"github.com/tektoncd/pipeline/pkg/reconciler/volumeclaim"
	"gomodules.xyz/jsonpatch/v2"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/clock"
	"knative.dev/pkg/apis"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/kmeta"
	"knative.dev/pkg/logging"
	"knative.dev/pkg/reconciler"
)

var cancelTaskTestRunPatchBytes, timeoutTaskTestRunPatchBytes []byte

// Reconciler implements controller.Reconciler for Configuration resources.
type Reconciler struct {
	KubeClientSet     kubernetes.Interface
	PipelineClientSet clientset.Interface
	Images            pipeline.Images
	Clock             clock.PassiveClock

	// listers index properties about resources
	TaskTestRunLister      alphalisters.TaskTestRunLister
	TaskTestSuiteRunLister alphalisters.TaskTestSuiteRunLister
	cloudEventClient       cloudevent.CEClient
	pvcHandler             volumeclaim.PvcHandler
	// spireClient              spire.ControllerAPIClient
	// limitrangeLister         corev1Listers.LimitRangeLister
	// podLister                corev1Listers.PodLister
	// verificationPolicyLister alphalisters.VerificationPolicyLister
	// entrypointCache          podconvert.EntrypointCache
	// metrics                  *taskrunmetrics.Recorder
	// resolutionRequester      resolution.Requester
	// tracerProvider           trace.TracerProvider
}

// ReconcileKind implements tasktestsuiterun.Interface.
func (c *Reconciler) ReconcileKind(
	ctx context.Context,
	ttsr *v1alpha1.TaskTestSuiteRun,
) reconciler.Event {
	logger := logging.FromContext(ctx)
	ctx = cloudevent.ToContext(ctx, c.cloudEventClient)

	before := ttsr.Status.GetCondition(apis.ConditionSucceeded)

	err := c.prepare(ctx, ttsr)
	if err != nil {
		return fmt.Errorf(
			"could not prepare reconciliation of task test suite run %s: %w",
			ttsr.Name,
			err,
		)
	}

	if !ttsr.HasStarted() {
		ttsr.Status.InitializeConditions()
		// In case node time was not synchronized, when controller has been scheduled to other nodes.
		if ttsr.Status.StartTime.Sub(ttsr.CreationTimestamp.Time) < 0 {
			logger.Warnf(
				"TaskTestRun %s createTimestamp %s is after the taskRun started %s",
				ttsr.GetName(),
				ttsr.CreationTimestamp,
				ttsr.Status.StartTime,
			)
			ttsr.Status.StartTime = &ttsr.CreationTimestamp
		}
		// Emit events. During the first reconcile the status of the TaskTestRun may change twice
		// from not Started to Started and then to Running, so we need to sent the event here
		// and at the end of 'Reconcile' again.
		// We also want to send the "Started" event as soon as possible for anyone who may be waiting
		// on the event to perform user facing initialisations, such has reset a CI check status
		afterCondition := ttsr.Status.GetCondition(apis.ConditionSucceeded)
		events.Emit(ctx, nil, afterCondition, ttsr)
	}

	// If the TaskTestSuiteRun is complete, run some post run fixtures when applicable
	if ttsr.IsDone() {
		logger.Infof("tasktestsuiterun done : %s \n", ttsr.Name)

		// We may be reading a version of the object that was stored at an older version
		// and may not have had all of the assumed default specified.
		ttsr.SetDefaults(ctx)

		return c.finishReconcileUpdateEmitEvents(ctx, ttsr, before, nil)
	}

	// If the TaskTestRun is cancelled, kill resources and update status
	if ttsr.IsCancelled() {
		message := fmt.Sprintf(
			"TaskTestSuiteRun %q was cancelled. %s",
			ttsr.Name,
			ttsr.Spec.StatusMessage,
		)
		err := c.failTaskRun(ctx, ttsr, v1alpha1.TaskTestSuiteRunReasonCancelled, message)
		return c.finishReconcileUpdateEmitEvents(ctx, ttsr, before, err)
	}

	// Check if the TaskRun has timed out; if it is, this will set its status
	// accordingly.
	if ttsr.HasTimedOut(ctx, c.Clock) {
		message := fmt.Sprintf(
			"TaskTestSuiteRun %q failed to finish within %q",
			ttsr.Name,
			ttsr.GetTimeout(ctx),
		)
		err := c.failTaskRun(ctx, ttsr, v1alpha1.TaskTestSuiteRunReasonTimedOut, message)
		return c.finishReconcileUpdateEmitEvents(ctx, ttsr, before, err)
	}

	if err := c.reconcile(ctx, ttsr, nil); err != nil {
		logger.Errorf("Reconcile: %v", err.Error())
		events.Emit(ctx, nil, ttsr.Status.GetCondition(apis.ConditionSucceeded), ttsr)
		return err
	}

	if ttsr.Status.StartTime != nil {
		// Compute the time since the task started.
		elapsed := c.Clock.Since(ttsr.Status.StartTime.Time)
		// Snooze this resource until the timeout has elapsed.
		timeout := ttsr.GetTimeout(ctx)
		waitTime := timeout - elapsed
		if timeout == config.NoTimeoutDuration {
			waitTime = time.Duration(
				config.FromContextOrDefaults(ctx).Defaults.DefaultTimeoutMinutes,
			) * time.Minute
		}
		return controller.NewRequeueAfter(waitTime)
	}

	return nil
}

func (c *Reconciler) failTaskRun(ctx context.Context, ttr *v1alpha1.TaskTestSuiteRun, reason v1alpha1.TaskTestSuiteRunReason, message string) error {
	logger := logging.FromContext(ctx)
	logger.Warnf("stopping task test run %q because of %q", ttr.Name, reason)
	ttr.Status.MarkResourceFailed(reason, errors.New(message))

	completionTime := metav1.Time{Time: c.Clock.Now()}
	// update tr completed time
	ttr.Status.CompletionTime = &completionTime

	for _, taskTest := range ttr.Status.TaskTestSuiteSpec.TaskTests {
		err := c.cancelTaskTestRun(ctx, ttr, reason, taskTest)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *Reconciler) cancelTaskTestRun(ctx context.Context, ttsr *v1alpha1.TaskTestSuiteRun, reason v1alpha1.TaskTestSuiteRunReason, taskTest v1alpha1.SuiteTest) error {
	logger := logging.FromContext(ctx)
	taskTestRun, err := c.getTaskTestRun(ctx, ttsr, taskTest)
	if err != nil {
		return err
	}

	if taskTestRun == nil {
		logger.Warnf("task test run %q has no task run running yet", ttsr.Name)
		return nil
	}

	var patch []byte
	switch reason {
	case v1alpha1.TaskTestSuiteRunReasonCancelled:
		patch = cancelTaskTestRunPatchBytes
	case v1alpha1.TaskTestSuiteRunReasonTimedOut:
		patch = timeoutTaskTestRunPatchBytes
	case v1alpha1.TaskTestSuiteRunReasonSuccessful, v1alpha1.TaskTestSuiteRunReasonUnexpectatedOutcomes, v1alpha1.TaskTestSuiteRunReasonValidationFailed:
		panic(fmt.Sprintf("unsupported v1alpha1.TaskTestRunReason: %#v", reason))
	default:
		panic(fmt.Sprintf("unexpected v1alpha1.TaskTestRunReason: %#v", reason))
	}

	_, err = c.PipelineClientSet.TektonV1alpha1().TaskTestRuns(ttsr.Namespace).Patch(ctx, taskTestRun.Name, types.JSONPatchType, patch, metav1.PatchOptions{}, "")
	if k8serrors.IsNotFound(err) {
		// The resource may have been deleted in the meanwhile, but we should
		// still be able to cancel the PipelineRun
		return nil
	}
	if pipelineErrors.IsImmutableTaskRunSpecError(err) {
		// The TaskRun may have completed and the spec field is immutable, we should ignore this error.
		return nil
	}
	return err
}

func (c *Reconciler) getTaskTestRun(ctx context.Context, ttsr *v1alpha1.TaskTestSuiteRun, taskTest v1alpha1.SuiteTest) (*v1alpha1.TaskTestRun, error) {
	logger := logging.FromContext(ctx)
	var taskTestRun *v1alpha1.TaskTestRun
	var err error

	if ttsr.Status.TaskTestRunStatuses[taskTest.GetTaskTestRunName(ttsr.Name)] != nil {
		logger.Infof(
			"boom: Now retrieving TaskTestRun %s for TTSR %s",
			taskTest.GetTaskTestRunName(ttsr.Name),
			ttsr.Name,
		)
		taskTestRun, err = c.TaskTestRunLister.TaskTestRuns(ttsr.Namespace).
			Get(taskTest.GetTaskTestRunName(ttsr.Name))
		if k8serrors.IsNotFound(err) {
			// Keep going, this will result in the TaskRun being created below.
		} else if err != nil {
			// This is considered a transient error, so we return error, do not update
			// the task test run condition, and return an error which will cause this key to
			// be requeued for reconcile.
			logger.Errorf("Error getting TaskTestRun %q: %v", taskTest.GetTaskTestRunName(ttsr.Name), err)
			events.Emit(ctx, nil, ttsr.Status.GetCondition(apis.ConditionSucceeded), ttsr)
			return nil, err
		}
	} else {
		// List TaskRuns that have a label with this TaskTestSuiteRun name.  Do not include other labels from the
		// TaskTestSuiteRun in this selector.  The user could change them during the lifetime of the TaskTestSuiteRun so the
		// current labels may not be set on a previously created TaskRun.
		logger.Infof("boom: Now retrieving TaskRun from list for TTR %s", ttsr.Name)
		labelSelector := labels.Set{pipeline.TaskTestSuiteRunLabelKey: ttsr.Name}
		ttrs, err := c.TaskTestRunLister.TaskTestRuns(ttsr.Namespace).List(labelSelector.AsSelector())
		if err != nil {
			logger.Errorf("Error listing task test runs: %v", err)
			events.Emit(ctx, nil, ttsr.Status.GetCondition(apis.ConditionSucceeded), ttsr)
			return nil, err
		}
		for index := range ttrs {
			tr := ttrs[index]
			if metav1.IsControlledBy(tr, ttsr) && tr.GetSuiteTestName() == taskTest.Name {
				logger.Infof("boom: Now found TaskRun %s in list controlled by TTR %s", tr.Name, ttsr.Name)
				taskTestRun = tr
				if ttsr.Status.TaskTestRunStatuses == nil {
					ttsr.Status.TaskTestRunStatuses = map[string]*v1alpha1.TaskTestRunStatus{}
				}
				ttsr.Status.TaskTestRunStatuses[taskTest.GetTaskTestRunName(ttsr.Name)] = &taskTestRun.Status
			}
		}
	}
	return taskTestRun, nil
}

func (c *Reconciler) finishReconcileUpdateEmitEvents(
	ctx context.Context,
	tr *v1alpha1.TaskTestSuiteRun,
	beforeCondition *apis.Condition,
	previousError error,
) reconciler.Event {
	afterCondition := tr.Status.GetCondition(apis.ConditionSucceeded)
	// if afterCondition.IsFalse() && !tr.IsCancelled() && tr.IsRetriable() {
	// 	retryTaskRun(tr, afterCondition.Message)
	// 	afterCondition = tr.Status.GetCondition(apis.ConditionSucceeded)
	// }

	// Send k8s events and cloud events (when configured)
	events.Emit(ctx, beforeCondition, afterCondition, tr)

	errs := []error{previousError}

	joinedErr := errors.Join(errs...)
	if controller.IsPermanentError(previousError) {
		return controller.NewPermanentError(joinedErr)
	}
	return joinedErr
}

// TODO(jlux98) implement this
// func retryTaskRun(tr *v1alpha1.TaskTestSuiteRun, s string) {
// 	panic("unimplemented")
// }

func (c *Reconciler) prepare(ctx context.Context, ttsr *v1alpha1.TaskTestSuiteRun) error {
	var observedTaskTestSuiteSpec *v1alpha1.TaskTestSuiteSpec
	var observedTaskTestSuiteName *string
	if ttsr.Spec.TaskTestSuiteSpec != nil {
		observedTaskTestSuiteSpec = ttsr.Spec.TaskTestSuiteSpec
	} else if ttsr.Spec.TaskTestSuiteRef != nil {
		taskTestSuite, err := c.dereferenceTaskTestSuiteRef(ctx, ttsr)
		if err != nil {
			events.Emit(ctx, nil, ttsr.Status.GetCondition(apis.ConditionSucceeded), ttsr)
			return fmt.Errorf(`error while dereferencing task test suite %q: %w`, ttsr.Spec.TaskTestSuiteRef.Name, err)
		}
		observedTaskTestSuiteName = &ttsr.Spec.TaskTestSuiteRef.Name
		observedTaskTestSuiteSpec = &taskTestSuite.Spec
		if ttsr.Status.TaskTestSuiteName == nil || *ttsr.Status.TaskTestSuiteName != *observedTaskTestSuiteName {
			ttsr.Status.TaskTestSuiteName = observedTaskTestSuiteName
		}
	}
	if ttsr.Status.TaskTestSuiteSpec == nil ||
		!cmp.Equal(*ttsr.Spec.TaskTestSuiteSpec, *observedTaskTestSuiteSpec) {
		ttsr.Status.TaskTestSuiteSpec = observedTaskTestSuiteSpec
	}

	for i, suiteTest := range ttsr.Status.TaskTestSuiteSpec.TaskTests {
		if suiteTest.TaskTestSpec == nil {
			taskTest, err := c.dereferenceTaskTestRef(ctx, &suiteTest, ttsr.Namespace)
			if err != nil {
				events.Emit(ctx, nil, ttsr.Status.GetCondition(apis.ConditionSucceeded), ttsr)
				return fmt.Errorf(
					`error while dereferencing task test %q for suite test %s.%s: %w`,
					suiteTest.TaskTestRef.Name,
					ttsr.Name,
					suiteTest.Name,
					err,
				)
			}

			ttsr.Status.TaskTestSuiteSpec.TaskTests[i].TaskTestSpec = &taskTest.Spec
		}
	}
	return nil
}

// `reconcile` creates the TaskRun associated to the TaskTestSuiteRun, and it pulls
// back status updates from the TaskRun to the TaskTestSuiteRun.
// It reports errors back to Reconcile, it updates the tasktest run status in
// case of error but it does not sync updates back to etcd. It does not emit
// events. `reconcile` consumes spec and resources returned by `prepare`
func (c *Reconciler) reconcile(
	ctx context.Context,
	ttsr *v1alpha1.TaskTestSuiteRun,
	rtr *resources.ResolvedTask,
) error {
	logger := logging.FromContext(ctx)

	if len(ttsr.Spec.SharedVolumes) > 0 {
		for _, volume := range ttsr.Spec.SharedVolumes {
			binding := v1.WorkspaceBinding{
				Name: volume.Name,
				VolumeClaimTemplate: &corev1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name: volume.Name,
					},
					Spec: volume.Spec,
				},
			}
			if err := c.pvcHandler.CreatePVCFromVolumeClaimTemplate(ctx, binding, *kmeta.NewControllerRef(ttsr), ttsr.Namespace); err != nil {
				logger.Errorf("Failed to create PVC for TaskRun %s: %v", ttsr.Name, err)
				ttsr.Status.MarkResourceFailed(volumeclaim.ReasonCouldntCreateWorkspacePVC,
					fmt.Errorf("failed to create PVC for TaskRun %s workspaces correctly: %w",
						fmt.Sprintf("%s/%s", ttsr.Namespace, ttsr.Name), err))
				return controller.NewPermanentError(err)
			}
		}

		ttsr.Status.SharedVolumes = applyVolumeClaimTemplates(ttsr.Spec.SharedVolumes, *kmeta.NewControllerRef(ttsr))
	}

	var err error
	allTaskTestRunsFinished := true

	for _, taskTest := range ttsr.Status.TaskTestSuiteSpec.TaskTests {
		logger.Infof("doing reconciliation loop for suite test %q", taskTest.Name)
		err = c.reconcileSuiteTest(ctx, ttsr, taskTest, &allTaskTestRunsFinished)
		if err != nil {
			return err
		}
	}

	if allTaskTestRunsFinished {
		logger.Infof(
			"boom: all TaskTestRuns for TTSR %s have been detected as completed",
			ttsr.Name,
		)
		if ttsr.Status.StartTime == nil {
			logger.Infof("boom: Now setting start time for TTR %s", ttsr.Name)
			ttsr.Status.StartTime = &metav1.Time{Time: c.Clock.Now()}
		}
		if ttsr.Status.CompletionTime == nil {
			logger.Infof("boom: Now setting Completion time for TTR %s", ttsr.Name)
			ttsr.Status.CompletionTime = &metav1.Time{Time: c.Clock.Now()}
		}

		tasksWithValidationErrors, tasksWithUnexpectedOutcomes, resultErr := aggregateSuccessStatusOfTaskTestRuns(ctx, ttsr)
		if resultErr != nil {
			return resultErr
		}
		// set status and emit event
		beforeCondition := ttsr.Status.GetCondition(apis.ConditionSucceeded)
		if len(tasksWithValidationErrors) > 0 {
			err := fmt.Errorf("all TaskTestRuns completed executing and not all were successful:\n- %s", strings.Join(tasksWithValidationErrors, "\n- "))
			ttsr.Status.MarkResourceFailed(v1alpha1.TaskTestSuiteRunReasonValidationFailed, err)
		} else {
			if len(tasksWithUnexpectedOutcomes) > 0 {
				err := fmt.Errorf("all TaskTestRuns completed executing and not all were successful:\n- %s", strings.Join(tasksWithUnexpectedOutcomes, "\n- "))
				ttsr.Status.MarkResourceFailed(v1alpha1.TaskTestSuiteRunReasonUnexpectatedOutcomes, err)
			} else {
				ttsr.Status.MarkSuccessful()
			}
		}
		events.Emit(ctx, beforeCondition, ttsr.Status.GetCondition(apis.ConditionSucceeded), ttsr)
		return nil
	}
	return nil
}

func (c *Reconciler) reconcileSuiteTest(ctx context.Context, ttsr *v1alpha1.TaskTestSuiteRun, taskTest v1alpha1.SuiteTest, allTaskTestRunsFinished *bool) error {
	logger := logging.FromContext(ctx)
	if ttsr.Spec.ExecutionMode == v1alpha1.TaskTestSuiteRunExecutionModeSequential && ttsr.Status.CurrentSuiteTest == nil {
		ttsr.Status.CurrentSuiteTest = &taskTest.Name
	}
	if ttsr.Spec.ExecutionMode == v1alpha1.TaskTestSuiteRunExecutionModeParallel ||
		*ttsr.Status.CurrentSuiteTest == taskTest.Name {
		// Get the TaskTest's TaskTestRun if it should have one. Otherwise, create the TaskRun.
		taskTestRun, err := c.getTaskTestRun(ctx, ttsr, taskTest)
		if err != nil {
			return err
		}

		if taskTestRun == nil {
			logger.Infof(
				"boom: Now creating TaskTestRun %q for TTSR %q",
				ttsr.Name+"-"+taskTest.Name,
				ttsr.Name,
			)
			taskTestRun, err = c.createTaskTestRun(ctx, ttsr, &taskTest, ttsr.Namespace)
			if err != nil {
				logger.Errorf(
					"Failed to create task test run %q for taskTestSuiteRun %q: %v",
					taskTest.GetTaskTestRunName(ttsr.Name),
					ttsr.Name,
					err,
				)
				return err
			}

			if ttsr.Status.TaskTestRunStatuses == nil {
				ttsr.Status.TaskTestRunStatuses = map[string]*v1alpha1.TaskTestRunStatus{}
			}
			ttsr.Status.TaskTestRunStatuses[taskTestRun.Name] = &taskTestRun.Status
			ttsr.Status.StartTime = &metav1.Time{
				Time: c.Clock.Now(),
			}
		}

		if ttsr.Status.TaskTestRunStatuses[taskTest.GetTaskTestRunName(ttsr.Name)] == nil ||
			!cmp.Equal(
				ttsr.Status.TaskTestRunStatuses[taskTest.GetTaskTestRunName(ttsr.Name)],
				taskTestRun.Status,
			) {
			logger.Infof("boom: Now setting task run name for TTR %s", ttsr.Name)
			ttsr.Status.TaskTestRunStatuses[taskTest.GetTaskTestRunName(ttsr.Name)] = &taskTestRun.Status
		}

		if taskTestRun.Status.CompletionTime == nil {
			*allTaskTestRunsFinished = false
		} else {
			ttsr.Status.CurrentSuiteTest = nil
		}
	}
	return nil
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

func aggregateSuccessStatusOfTaskTestRuns(ctx context.Context, ttsr *v1alpha1.TaskTestSuiteRun) ([]string, []string, error) {
	tasksWithUnexpectedOutcomes := []string{}
	tasksWithValidationErrors := []string{}
	logger := logging.FromContext(ctx)

	for i, suiteTest := range ttsr.Status.TaskTestSuiteSpec.TaskTests {
		trName := suiteTest.GetTaskTestRunName(ttsr.Name)
		condition := ttsr.Status.TaskTestRunStatuses[trName].GetCondition(
			apis.ConditionSucceeded,
		)
		if condition.IsFalse() {
			message := fmt.Sprintf("taskTestRun %q failed", trName)
			conditionJSON, err := json.Marshal(condition)
			if err != nil {
				return nil, nil, err
			}
			if condition.Reason == v1alpha1.TaskTestRunReasonFailedValidation.String() {
				message += " due to a validation error, so the whole TaskTestSuiteRun fails"
				tasksWithValidationErrors = append(tasksWithValidationErrors, fmt.Sprintf("%s: %s\n", trName, conditionJSON))
			} else {
				if ttsr.Status.TaskTestSuiteSpec.TaskTests[i].OnError != "Continue" {
					message += " due to unexpected outcomes, so the whole TaskTestSuiteRun fails"
					tasksWithUnexpectedOutcomes = append(tasksWithUnexpectedOutcomes, fmt.Sprintf("%s: %s\n", trName, conditionJSON))
				} else {
					message += " but onError was set to continue, so the show will go on."
				}
			}
			logger.Error(message)
		}
	}

	return tasksWithValidationErrors, tasksWithUnexpectedOutcomes, nil
}

func (c *Reconciler) dereferenceTaskTestSuiteRef(
	ctx context.Context,
	ttsr *v1alpha1.TaskTestSuiteRun,
) (*v1alpha1.TaskTestSuite, error) {
	taskTest, err := c.PipelineClientSet.TektonV1alpha1().
		TaskTestSuites(ttsr.Namespace).
		Get(ctx, ttsr.Spec.TaskTestSuiteRef.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return taskTest, nil
}

func (c *Reconciler) dereferenceTaskTestRef(
	ctx context.Context,
	st *v1alpha1.SuiteTest,
	namespace string,
) (*v1alpha1.TaskTest, error) {
	taskTest, err := c.PipelineClientSet.TektonV1alpha1().
		TaskTests(namespace).
		Get(ctx, st.TaskTestRef.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return taskTest, nil
}

func (c *Reconciler) createTaskTestRun(ctx context.Context, ttsr *v1alpha1.TaskTestSuiteRun,
	suiteTest *v1alpha1.SuiteTest, namespace string) (*v1alpha1.TaskTestRun, error) {
	logger := logging.FromContext(ctx)
	var taskTestRun *v1alpha1.TaskTestRun

	taskTestRun = &v1alpha1.TaskTestRun{
		TypeMeta: metav1.TypeMeta{Kind: "TaskTestRun", APIVersion: "tekton.dev/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      ttsr.Name + "-" + suiteTest.Name,
			Namespace: namespace,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(ttsr, schema.GroupVersionKind{Group: "tekton.dev", Version: "v1alpha1", Kind: "TaskTestSuiteRun"}),
			},
			Labels: map[string]string{
				pipeline.TaskTestSuiteRunLabelKey: ttsr.Name,
				pipeline.SuiteTestLabelKey:        suiteTest.Name,
			},
		},
	}

	taskTestRun.Spec.TaskTestSpec = suiteTest.TaskTestSpec

	if ttsr.Spec.DefaultRunSpecTemplate != nil {
		convertedWorkspaces := []v1.WorkspaceBinding{}
		for i, ws := range ttsr.Spec.DefaultRunSpecTemplate.Workspaces {
			if ws.SharedVolume != nil {
				idx := slices.IndexFunc(ttsr.Status.SharedVolumes, func(wb v1.WorkspaceBinding) bool {
					return wb.Name == ws.SharedVolume.VolumeName
				})
				if idx < 0 {
					return nil, apis.ErrInvalidValue(ws.SharedVolume.VolumeName, fmt.Sprintf("spec.defaultRunSpecTemplate[%d].name", i), fmt.Sprintf("TaskTestSuiteRun %q does not have a Shared Volume named %q", ttsr.Name, ws.SharedVolume.VolumeName))
				}
				binding := ttsr.Status.SharedVolumes[idx].DeepCopy()
				binding.Name = ws.Name
				convertedWorkspaces = append(convertedWorkspaces, *binding)
			} else {
				convertedWorkspaces = append(convertedWorkspaces, ws.ToWorkspaceBinding())
			}
		}
		taskTestRun.Spec.Workspaces = append(taskTestRun.Spec.Workspaces, convertedWorkspaces...)
		taskTestRun.Spec.Volumes = append(taskTestRun.Spec.Volumes, ttsr.Spec.DefaultRunSpecTemplate.Volumes...)
	}

	if ttsr.Spec.RunSpecMap == nil && ttsr.Spec.RunSpecs != nil {
		ttsr.Spec.RunSpecMap = v1alpha1.TaskTestRunTemplateMap{}
		ttsr.Spec.RunSpecMap.GenerateMap(ttsr.Spec.RunSpecs)
	}
	if ttsr.Spec.RunSpecs != nil && ttsr.Spec.RunSpecMap[suiteTest.Name] != nil {
		convertedWorkspaces := []v1.WorkspaceBinding{}
		for i, ws := range ttsr.Spec.RunSpecMap[suiteTest.Name].Workspaces {
			if ws.SharedVolume != nil {
				idx := slices.IndexFunc(ttsr.Status.SharedVolumes, func(wb v1.WorkspaceBinding) bool {
					return wb.Name == ws.SharedVolume.VolumeName
				})
				if idx < 0 {
					return nil, apis.ErrInvalidValue(ws.SharedVolume.VolumeName, fmt.Sprintf("spec.runSpecs[%s].workspaces[%d].name", suiteTest.Name, i), fmt.Sprintf("TaskTestSuiteRun %q does not have a Shared Volume named %q", ttsr.Name, ws.SharedVolume.VolumeName))
				}
				binding := ttsr.Status.SharedVolumes[idx].DeepCopy()
				binding.Name = ws.Name
				convertedWorkspaces = append(convertedWorkspaces, *binding)
			} else {
				convertedWorkspaces = append(convertedWorkspaces, ws.ToWorkspaceBinding())
			}
		}
		taskTestRun.Spec.Workspaces = append(taskTestRun.Spec.Workspaces, convertedWorkspaces...)
		taskTestRun.Spec.Volumes = append(taskTestRun.Spec.Volumes, ttsr.Spec.RunSpecMap[suiteTest.Name].Volumes...)
	}

	taskTestRun.Spec.Retries = suiteTest.Retries
	taskTestRun.Spec.Timeout = suiteTest.Timeout
	taskTestRun.Spec.AllTriesMustSucceed = suiteTest.AllTriesMustSucceed

	taskTestRun.Status.InitializeConditions()
	taskTestRun.SetDefaults(ctx)
	taskTestRun, err := c.PipelineClientSet.TektonV1alpha1().
		TaskTestRuns(namespace).
		Create(ctx, taskTestRun, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}
	ttsr.Status.InitializeConditions()

	logger.Infof(`TaskRun successfully created: %v`, *taskTestRun)

	return taskTestRun, nil
}

// Check that our Reconciler implements taskrunreconciler.Interface
var _ tasktestsuiterunreconciler.Interface = (*Reconciler)(nil)

func init() {
	var err error
	cancelTaskTestRunPatchBytes, err = json.Marshal([]jsonpatch.JsonPatchOperation{
		{
			Operation: "add",
			Path:      "/spec/status",
			Value:     v1alpha1.TaskTestRunSpecStatusCancelled,
		},
		{
			Operation: "add",
			Path:      "/spec/statusMessage",
			Value:     v1alpha1.TaskTestRunCancelledByTaskTestSuiteMsg,
		}})
	if err != nil {
		log.Fatalf("failed to marshal TaskRun cancel patch bytes: %v", err)
	}
	timeoutTaskTestRunPatchBytes, err = json.Marshal([]jsonpatch.JsonPatchOperation{
		{
			Operation: "add",
			Path:      "/spec/status",
			Value:     v1alpha1.TaskTestRunSpecStatusCancelled,
		},
		{
			Operation: "add",
			Path:      "/spec/statusMessage",
			Value:     v1alpha1.TaskTestRunCancelledByTaskTestSuiteTimeoutMsg,
		}})
	if err != nil {
		log.Fatalf("failed to marshal TaskRun cancel patch bytes: %v", err)
	}
}

// applyVolumeClaimTemplates and return WorkspaceBindings were templates is translated to PersistentVolumeClaims
func applyVolumeClaimTemplates(sharedVolumes []v1alpha1.NamedVolumeClaimTemplate, owner metav1.OwnerReference) []v1.WorkspaceBinding {
	taskRunWorkspaceBindings := make([]v1.WorkspaceBinding, 0, len(sharedVolumes))
	for _, volume := range sharedVolumes {
		templateBinding := v1.WorkspaceBinding{
			Name: volume.Name,
			VolumeClaimTemplate: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name: volume.Name,
				},
				Spec: volume.Spec,
			},
		}

		// apply template
		b := v1.WorkspaceBinding{
			Name: volume.Name,
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: volumeclaim.GeneratePVCNameFromWorkspaceBinding(volume.Name, templateBinding, owner),
			},
		}
		taskRunWorkspaceBindings = append(taskRunWorkspaceBindings, b)
	}
	return taskRunWorkspaceBindings
}
