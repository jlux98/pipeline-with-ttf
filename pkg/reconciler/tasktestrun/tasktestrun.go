package tasktestrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"regexp"
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
	tasktestrunreconciler "github.com/tektoncd/pipeline/pkg/client/injection/reconciler/pipeline/v1alpha1/tasktestrun"
	v1listers "github.com/tektoncd/pipeline/pkg/client/listers/pipeline/v1"
	alphalisters "github.com/tektoncd/pipeline/pkg/client/listers/pipeline/v1alpha1"
	"github.com/tektoncd/pipeline/pkg/reconciler/apiserver"
	"github.com/tektoncd/pipeline/pkg/reconciler/events"
	"github.com/tektoncd/pipeline/pkg/reconciler/events/cloudevent"
	"github.com/tektoncd/pipeline/pkg/reconciler/taskrun/resources"
	"gomodules.xyz/jsonpatch/v2"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/clock"
	"k8s.io/utils/ptr"
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

var cancelTaskRunPatchBytes, timeoutTaskRunPatchBytes []byte

// ReconcileKind implements tasktestrun.Interface.
func (c *Reconciler) ReconcileKind(ctx context.Context, ttr *v1alpha1.TaskTestRun) reconciler.Event {
	logger := logging.FromContext(ctx)
	ctx = cloudevent.ToContext(ctx, c.cloudEventClient)

	before := ttr.Status.GetCondition(apis.ConditionSucceeded)

	err := c.prepare(ctx, ttr)
	if err != nil {
		return fmt.Errorf("could not prepare reconciliation of task test run %s: %w", ttr.Name, err)
	}

	if !ttr.HasStarted() {
		ttr.Status.InitializeConditions()
		// In case node time was not synchronized, when controller has been scheduled to other nodes.
		if ttr.Status.StartTime.Sub(ttr.CreationTimestamp.Time) < 0 {
			logger.Warnf("TaskRun %s createTimestamp %s is after the taskRun started %s", ttr.GetNamespacedName().String(), ttr.CreationTimestamp, ttr.Status.StartTime)
			ttr.Status.StartTime = &ttr.CreationTimestamp
		}
		// Emit events. During the first reconcile the status of the TaskRun may change twice
		// from not Started to Started and then to Running, so we need to sent the event here
		// and at the end of 'Reconcile' again.
		// We also want to send the "Started" event as soon as possible for anyone who may be waiting
		// on the event to perform user facing initialisations, such has reset a CI check status
		afterCondition := ttr.Status.GetCondition(apis.ConditionSucceeded)
		events.Emit(ctx, nil, afterCondition, ttr)
	}

	// If the TaskTestRun is complete, run some post run fixtures when applicable
	if ttr.IsDone() {
		logger.Infof("tasktestrun done : %s \n", ttr.Name)

		// We may be reading a version of the object that was stored at an older version
		// and may not have had all of the assumed default specified.
		ttr.SetDefaults(ctx)

		return c.finishReconcileUpdateEmitEvents(ctx, ttr, before, nil)
	}

	// If the TaskRun is cancelled, kill resources and update status
	if ttr.IsCancelled() {
		message := fmt.Sprintf("TaskTestRun %q was cancelled. %s", ttr.Name, ttr.Spec.StatusMessage)
		err := c.failTaskTestRun(ctx, ttr, v1alpha1.TaskTestRunReasonCancelled, message)
		return c.finishReconcileUpdateEmitEvents(ctx, ttr, before, err)
	}

	// Check if the TaskRun has timed out; if it is, this will set its status
	// accordingly.
	if ttr.HasTimedOut(ctx, c.Clock) {
		message := fmt.Sprintf("TaskTestRun %q failed to finish within %q", ttr.Name, ttr.GetTimeout(ctx))
		err := c.failTaskTestRun(ctx, ttr, v1alpha1.TaskTestRunReasonTimedOut, message)
		return c.finishReconcileUpdateEmitEvents(ctx, ttr, before, err)
	}

	if err := c.reconcile(ctx, ttr, nil); err != nil {
		logger.Errorf("Reconcile: %v", err.Error())
		events.Emit(ctx, nil, ttr.Status.GetCondition(apis.ConditionSucceeded), ttr)
		return err
	}

	if ttr.Status.StartTime != nil {
		// Compute the time since the task started.
		elapsed := c.Clock.Since(ttr.Status.StartTime.Time)
		// Snooze this resource until the timeout has elapsed.
		timeout := ttr.GetTimeout(ctx)
		waitTime := timeout - elapsed
		if timeout == config.NoTimeoutDuration {
			waitTime = time.Duration(config.FromContextOrDefaults(ctx).Defaults.DefaultTimeoutMinutes) * time.Minute
		}
		return controller.NewRequeueAfter(waitTime)
	}

	return nil
}

func (c *Reconciler) failTaskTestRun(ctx context.Context, ttr *v1alpha1.TaskTestRun, reason v1alpha1.TaskTestRunReason, message string) error {
	logger := logging.FromContext(ctx)
	logger.Warnf("stopping task test run %q because of %q", ttr.Name, reason)
	ttr.Status.MarkResourceFailed(reason, errors.New(message))

	completionTime := metav1.Time{Time: c.Clock.Now()}
	// update tr completed time
	ttr.Status.CompletionTime = &completionTime

	taskRun, err := c.getTaskRun(ctx, ttr)
	if err != nil {
		return err
	}

	if taskRun == nil {
		logger.Warnf("task test run %q has no task run running yet", ttr.Name)
		return nil
	}

	var patch []byte
	switch reason {
	case v1alpha1.TaskTestRunReasonCancelled:
		patch = cancelTaskRunPatchBytes
	case v1alpha1.TaskTestRunReasonTimedOut:
		patch = timeoutTaskRunPatchBytes
	case v1alpha1.TaskTestRunReasonFailedValidation, v1alpha1.TaskTestRunReasonSuccessful, v1alpha1.TaskTestRunUnexpectatedOutcomes:
		panic(fmt.Sprintf("unsupported v1alpha1.TaskTestRunReason: %#v", reason))
	default:
		panic(fmt.Sprintf("unexpected v1alpha1.TaskTestRunReason: %#v", reason))
	}
	_, err = c.PipelineClientSet.TektonV1().TaskRuns(ttr.Namespace).Patch(ctx, taskRun.Name, types.JSONPatchType, patch, metav1.PatchOptions{}, "")
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

func (c *Reconciler) finishReconcileUpdateEmitEvents(ctx context.Context, ttr *v1alpha1.TaskTestRun, beforeCondition *apis.Condition, previousError error) reconciler.Event {
	afterCondition := ttr.Status.GetCondition(apis.ConditionSucceeded)
	logging.FromContext(ctx).Infof(`commencing retry check:
	condition: %s,
	isRetriable: %s,
	allTriesMustSucced: %s
	hasNotFailedYet: %s`, afterCondition, ttr.IsRetriable(), *ttr.Spec.AllTriesMustSucceed, ttr.HasNotFailedYet())
	if !ttr.IsCancelled() && ttr.IsRetriable() && ((afterCondition.IsFalse() && !*ttr.Spec.AllTriesMustSucceed) ||
		(afterCondition.IsTrue() && *ttr.Spec.AllTriesMustSucceed && ttr.HasNotFailedYet())) {
		retryTaskTestRun(ttr, afterCondition.Message)
		afterCondition = ttr.Status.GetCondition(apis.ConditionSucceeded)
	}
	// Send k8s events and cloud events (when configured)
	events.Emit(ctx, beforeCondition, afterCondition, ttr)

	errs := []error{previousError}

	joinedErr := errors.Join(errs...)
	if controller.IsPermanentError(previousError) {
		return controller.NewPermanentError(joinedErr)
	}
	return joinedErr
}

func retryTaskTestRun(ttr *v1alpha1.TaskTestRun, message string) {
	newStatus := ttr.Status.DeepCopy()
	newStatus.RetriesStatus = nil
	ttr.Status.RetriesStatus = append(ttr.Status.RetriesStatus, *newStatus)
	ttr.Status.StartTime = nil
	ttr.Status.CompletionTime = nil
	ttr.Status.TaskRunName = nil
	ttr.Status.Outcomes = nil
	taskTestRunCondSet := apis.NewBatchConditionSet()
	taskTestRunCondSet.Manage(&ttr.Status).MarkUnknown(apis.ConditionSucceeded, v1.TaskRunReasonToBeRetried.String(), message)
}

func (c *Reconciler) prepare(ctx context.Context, ttr *v1alpha1.TaskTestRun) error {
	var observedTaskTestSpec *v1alpha1.TaskTestSpec
	var observedTaskTestName *string
	if ttr.Spec.TaskTestSpec != nil {
		observedTaskTestSpec = ttr.Spec.TaskTestSpec
	}
	if ttr.Spec.TaskTestRef != nil {
		taskTest, err := c.dereferenceTaskTestRef(ctx, ttr)
		if err != nil {
			events.Emit(ctx, nil, ttr.Status.GetCondition(apis.ConditionSucceeded), ttr)
			return err
		}
		observedTaskTestName = &ttr.Spec.TaskTestRef.Name
		observedTaskTestSpec = &taskTest.Spec
		if ttr.Status.TaskTestName == nil || *ttr.Status.TaskTestName != *observedTaskTestName {
			ttr.Status.TaskTestName = observedTaskTestName
		}
	}
	if ttr.Status.TaskTestSpec == nil || !cmp.Equal(*ttr.Spec.TaskTestSpec, *observedTaskTestSpec) {
		ttr.Status.TaskTestSpec = observedTaskTestSpec
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

	// Get the TaskTestRun's TaskRun if it should have one. Otherwise, create the TaskRun.
	taskRun, err := c.getTaskRun(ctx, ttr)
	if err != nil {
		return err
	}

	if taskRun == nil {
		logger.Infof("boom: Now creating TaskRun for TTR %s", ttr.Name)
		taskRun, err = c.createTaskRun(ctx, ttr, rtr)
		if err != nil {
			if errors.Is(err, apiserver.ErrReferencedObjectValidationFailed) {
				ttr.Status.MarkResourceFailed(v1alpha1.TaskTestRunReasonFailedValidation, err)
				events.Emit(ctx, nil, ttr.Status.GetCondition(apis.ConditionSucceeded), ttr)
				return controller.NewPermanentError(err)
			}
			logger.Errorf("Failed to create task run for taskTestRun %q: %v", ttr.Name, err)
			return err
		}
	}

	if cond := taskRun.Status.GetCondition(apis.ConditionSucceeded); cond.IsFalse() {
		// if cond.Reason == volumeclaim.ReasonCouldntCreateWorkspacePVC {
		// 	err := errors.New(cond.Message)
		// 	logger.Errorf("Failed to create PVC for TaskTestRun %s: %v", ttr.Name, err)
		// 	ttr.Status.MarkResourceFailed(volumeclaim.ReasonCouldntCreateWorkspacePVC,
		// 		fmt.Errorf("failed to create PVC for TaskTestRun %s workspaces correctly: %w",
		// 			fmt.Sprintf("%s/%s", ttr.Namespace, ttr.Name), err))
		// 	return err
		// }
		events.Emit(ctx, ttr.Status.GetCondition(apis.ConditionSucceeded), cond, ttr)
	}

	if ttr.Status.TaskRunName == nil || *ttr.Status.TaskRunName != taskRun.Name {
		logger.Infof("boom: Now setting task run name for TTR %s", ttr.Name)
		ttr.Status.TaskRunName = &taskRun.Name
	}

	if ttr.Status.TaskRunStatus == nil || !cmp.Equal(*ttr.Status.TaskRunStatus, taskRun.Status) {
		logger.Infof("boom: Now setting task run status for TTR %s", ttr.Name)
		ttr.Status.TaskRunStatus = &taskRun.Status
	}

	if taskRun.Status.CompletionTime != nil {
		logger.Infof("boom: TaskRun for TTR %s has been detected as completed", ttr.Name)
		if ttr.Status.StartTime == nil {
			logger.Infof("boom: Now setting start time for TTR %s", ttr.Name)
			ttr.Status.StartTime = &metav1.Time{Time: c.Clock.Now()}
		}
		if ttr.Status.CompletionTime == nil {
			logger.Infof("boom: Now setting Completion time for TTR %s", ttr.Name)
			ttr.Status.CompletionTime = &metav1.Time{Time: c.Clock.Now()}
		}

		resultErr, expectationsMet, diffs := checkActualOutcomesAgainstExpectations(ctx, &ttr.Status, &taskRun.Status)

		// set status and emit event
		beforeCondition := ttr.Status.GetCondition(apis.ConditionSucceeded)
		if resultErr != nil {
			resultErr = fmt.Errorf("error occurred while checking expectations: %w", resultErr)
			ttr.Status.MarkResourceFailed(v1alpha1.TaskTestRunReasonFailedValidation, errors.New("error occurred while checking expectations"))
		} else {
			if expectationsMet {
				ttr.Status.MarkSuccessful()
			} else {
				err := errors.New("not all expectations were met")
				ttr.Status.Outcomes.Diffs = diffs
				ttr.Status.MarkResourceFailed(v1alpha1.TaskTestRunUnexpectatedOutcomes, err)
			}
		}
		events.Emit(ctx, beforeCondition, ttr.Status.GetCondition(apis.ConditionSucceeded), ttr)
		return resultErr
	}

	logger.Infof("Successfully reconciled tasktestrun %s/%s with status: %#v", ttr.Name, ttr.Namespace, ttr.Status.GetCondition(apis.ConditionSucceeded))
	return nil
}

func (c *Reconciler) getTaskRun(ctx context.Context, ttr *v1alpha1.TaskTestRun) (*v1.TaskRun, error) {
	var taskRun *v1.TaskRun = nil
	var err error
	logger := logging.FromContext(ctx)
	if ttr.Status.TaskRunName != nil {
		logger.Infof("boom: Now retrieving TaskRun %s for TTR %s", *ttr.Status.TaskRunName, ttr.Name)
		taskRun, err = c.taskRunLister.TaskRuns(ttr.Namespace).Get(*ttr.Status.TaskRunName)
		if k8serrors.IsNotFound(err) {
			// Keep going, this will result in the TaskRun being created below.
		} else if err != nil {
			// This is considered a transient error, so we return error, do not update
			// the task test run condition, and return an error which will cause this key to
			// be requeued for reconcile.
			logger.Errorf("Error getting TaskRun %q: %v", ttr.Status.TaskRunName, err)
			events.Emit(ctx, nil, ttr.Status.GetCondition(apis.ConditionSucceeded), ttr)
			return nil, err
		}
		return taskRun, nil
	}

	// List TaskRuns that have a label with this TaskTestRun name.  Do not include other labels from the
	// TaskTestRun in this selector.  The user could change them during the lifetime of the TasTestkRun so the
	// current labels may not be set on a previously created TaskRun.
	logger.Infof("boom: Now retrieving TaskRun from list for TTR %s", ttr.Name)
	labelSelector := labels.Set{pipeline.TaskTestRunLabelKey: ttr.Name}
	trs, err := c.taskRunLister.TaskRuns(ttr.Namespace).List(labelSelector.AsSelector())
	if err != nil {
		logger.Errorf("Error listing task runs: %v", err)
		events.Emit(ctx, nil, ttr.Status.GetCondition(apis.ConditionSucceeded), ttr)
		return nil, err
	}
	for index := range trs {
		tr := trs[index]
		if metav1.IsControlledBy(tr, ttr) && (ttr.Status.RetriesStatus == nil || !slices.ContainsFunc(ttr.Status.RetriesStatus, func(trs v1alpha1.TaskTestRunStatus) bool {
			return trs.TaskRunName != nil && *trs.TaskRunName == tr.Name
		})) {
			logger.Infof("boom: Now found TaskRun %s in list controlled by TTR %s", tr.Name, ttr.Name)
			taskRun = tr
			ttr.Status.TaskRunName = &taskRun.Name
		}
	}
	return taskRun, nil
}

func checkActualOutcomesAgainstExpectations(ctx context.Context, ttrs *v1alpha1.TaskTestRunStatus, trs *v1.TaskRunStatus) (error, bool, string) {
	expectationsMet := true
	diffs := ""
	var resultErr error = nil
	if ttrs.TaskTestSpec.Expects != nil && ttrs.GetCondition(apis.ConditionSucceeded).IsUnknown() {
		ttrs.Outcomes = &v1alpha1.ObservedOutcomes{}

		// check success status
		if ttrs.TaskTestSpec.Expects.SuccessStatus != nil {
			ttrs.Outcomes.SuccessStatus = &v1alpha1.ObservedSuccessStatus{
				Want: *ttrs.TaskTestSpec.Expects.SuccessStatus,
				Got:  trs.GetCondition(apis.ConditionSucceeded).IsTrue(),
			}
			ttrs.Outcomes.SuccessStatus.WantDiffersFromGot = ttrs.Outcomes.SuccessStatus.Want != ttrs.Outcomes.SuccessStatus.Got

			if ttrs.Outcomes.SuccessStatus.WantDiffersFromGot {
				expectationsMet = false
				diffs += "observed success status did not match expectation\n"
			}
		}

		// check success reason
		if ttrs.TaskTestSpec.Expects.SuccessReason != nil {
			ttrs.Outcomes.SuccessReason = &v1alpha1.ObservedSuccessReason{
				Want: *ttrs.TaskTestSpec.Expects.SuccessReason,
				Got:  v1.TaskRunReason(trs.Conditions[0].Reason),
			}
			ttrs.Outcomes.SuccessReason.WantDiffersFromGot = ttrs.Outcomes.SuccessReason.Want != ttrs.Outcomes.SuccessReason.Got

			if ttrs.Outcomes.SuccessReason.WantDiffersFromGot {
				expectationsMet = false
				diffs += "observed success reason did not match expectation\n"
			}
		}

		// check results
		if ttrs.TaskTestSpec.Expects.Results != nil && ttrs.Outcomes.Results == nil {
			if err := checkExpectationsForResults(ttrs, trs, &expectationsMet, &diffs); err != nil {
				resultErr = fmt.Errorf(`error while checking the expectations for results: %w`, err)
			}
		}

		// check environment variables
		if ttrs.TaskTestSpec.Expects.Env != nil {
			if err := checkExpectationsForEnv(ttrs, trs, &expectationsMet, &diffs); err != nil {
				err = fmt.Errorf(`error while checking the expectations for env: %w`, err)
				if resultErr == nil {
					resultErr = err
				} else {
					resultErr = fmt.Errorf("%w\n%w", resultErr, err)
				}
			}
		}

		// check file system contents
		if ttrs.TaskTestSpec.Expects.FileSystemContents != nil {
			if err := checkExpectationsForFileSystemObjects(ctx, ttrs, trs, &expectationsMet, &diffs); err != nil {
				err = fmt.Errorf(`error while checking the expectations for file system objects: %w`, err)
				if resultErr == nil {
					resultErr = err
				} else {
					resultErr = fmt.Errorf("%w\n%w", resultErr, err)
				}
			}
		}
	}
	return resultErr, expectationsMet, diffs
}

func (c *Reconciler) dereferenceTaskTestRef(ctx context.Context, ttr *v1alpha1.TaskTestRun) (*v1alpha1.TaskTest, error) {
	taskTest, err := c.PipelineClientSet.TektonV1alpha1().TaskTests(ttr.Namespace).Get(ctx, ttr.Spec.TaskTestRef.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return taskTest, nil
}

func (c *Reconciler) createTaskRun(ctx context.Context, ttr *v1alpha1.TaskTestRun, _ *resources.ResolvedTask) (*v1.TaskRun, error) {
	ttr.SetDefaults(ctx)
	logger := logging.FromContext(ctx)
	var taskRun *v1.TaskRun
	var taskName string
	taskRunNamespace := ttr.Namespace

	if ttr.Status.TaskTestSpec != nil {
		taskName = ttr.Status.TaskTestSpec.TaskRef.Name
	}

	task, err := c.PipelineClientSet.TektonV1().Tasks(taskRunNamespace).Get(ctx, taskName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("could not dereference task under test: %w", err)
	}

	logger.Infof(`Task successfully dereferenced: %v`, *task)

	if ttr.Status.TaskTestSpec.Expects != nil {
		expected := ttr.Status.TaskTestSpec.Expects
		if expected.Results != nil {
			declaredResults := task.Spec.Results
			for i, expectedResult := range ttr.Status.TaskTestSpec.Expects.Results {
				if !slices.ContainsFunc(declaredResults, func(result v1.TaskResult) bool {
					return result.Name == expectedResult.Name
				}) {
					return nil, fmt.Errorf(`%w: %w`, apiserver.ErrReferencedObjectValidationFailed, apis.ErrInvalidValue(expectedResult.Name, fmt.Sprintf("status.taskTestSpec.expected.results[%d].name", i), fmt.Sprintf(`task %q has no Result named %q`, task.Name, expectedResult.Name)))
				}
			}
		}
		if ttr.Status.TaskTestSpec.Expects.StepEnvs != nil {
			declaredSteps := task.Spec.Steps
			for i, stepEnv := range ttr.Status.TaskTestSpec.Expects.StepEnvs {
				if !slices.ContainsFunc(declaredSteps, func(step v1.Step) bool {
					return step.Name == stepEnv.StepName
				}) {
					return nil, fmt.Errorf(`%w: %w`, apiserver.ErrReferencedObjectValidationFailed, apis.ErrInvalidValue(stepEnv.StepName, fmt.Sprintf("status.taskTestSpec.expected.stepEnvs[%d].stepName", i), fmt.Sprintf(`task %q has no Step named %q`, task.Name, stepEnv.StepName)))
				}
			}
		}
		if ttr.Status.TaskTestSpec.Expects.FileSystemContents != nil {
			declaredSteps := task.Spec.Steps
			for i, stepFileSystem := range ttr.Status.TaskTestSpec.Expects.FileSystemContents {
				if !slices.ContainsFunc(declaredSteps, func(step v1.Step) bool {
					return step.Name == stepFileSystem.StepName
				}) {
					return nil, fmt.Errorf(`%w: %w`, apiserver.ErrReferencedObjectValidationFailed, apis.ErrInvalidValue(stepFileSystem.StepName, fmt.Sprintf("status.taskTestSpec.expected.fileSystemContents[%d].stepName", i), fmt.Sprintf(`task %q has no Step named %q`, task.Name, stepFileSystem.StepName)))
				}
			}
		}
	}

	taskRunSpec := v1.TaskRunSpec{
		TaskSpec:   &task.Spec,
		Workspaces: ttr.Spec.Workspaces,
	}
	if ttr.Status.TaskTestSpec.Inputs != nil {
		if ttr.Status.TaskTestSpec.Inputs.Params != nil {
			for i, param := range ttr.Status.TaskTestSpec.Inputs.Params {
				if !slices.ContainsFunc(task.Spec.Params, func(ps v1.ParamSpec) bool {
					return ps.Name == param.Name
				}) {
					return nil, fmt.Errorf(`%w: %w`, apiserver.ErrReferencedObjectValidationFailed,
						apis.ErrInvalidValue(param.Name, fmt.Sprintf(
							"status.taskTestSpec.inputs.params[%d].name", i),
							fmt.Sprintf(`task %q has no Param named %q`, task.Name, param.Name)))
				}
			}
		}
		taskRunSpec.Params = ttr.Status.TaskTestSpec.Inputs.Params

		if ttr.Status.TaskTestSpec.Inputs.Env != nil {
			if task.Spec.StepTemplate == nil {
				task.Spec.StepTemplate = &v1.StepTemplate{}
			}
			task.Spec.StepTemplate.Env = ttr.Status.TaskTestSpec.Inputs.Env
		}

		if ttr.Status.TaskTestSpec.Inputs.StepEnvs != nil {
			for i, stepEnv := range ttr.Status.TaskTestSpec.Inputs.StepEnvs {
				logger.Infof("checking stepEnv for step %q", stepEnv.StepName)
				if idx := slices.IndexFunc(task.Spec.Steps, func(s v1.Step) bool {
					return s.Name == stepEnv.StepName
				}); idx >= 0 {
					task.Spec.Steps[idx].Env = append(task.Spec.Steps[idx].Env, stepEnv.Env...)
				} else {
					return nil, fmt.Errorf(`%w: %w`, apiserver.ErrReferencedObjectValidationFailed, apis.ErrInvalidValue(stepEnv.StepName, fmt.Sprintf("status.taskTestSpec.inputs.stepEnvs[%d].stepName", i), fmt.Sprintf(`task %q has no Step named %q`, task.Name, stepEnv.StepName)))
				}
			}
		}

		if ttr.Status.TaskTestSpec.Inputs.WorkspaceContents != nil {
			for i, workspace := range ttr.Status.TaskTestSpec.Inputs.WorkspaceContents {
				if !slices.ContainsFunc(task.Spec.Workspaces, func(ws v1.WorkspaceDeclaration) bool {
					return ws.Name == workspace.Name
				}) {
					return nil, fmt.Errorf(`%w: %w`, apiserver.ErrReferencedObjectValidationFailed,
						apis.ErrInvalidValue(workspace.Name, fmt.Sprintf(
							"status.taskTestSpec.inputs.workspaceContents[%d].name", i),
							fmt.Sprintf(`task %q has no Workspace named %q`, task.Name, workspace.Name)))
				}
			}

			workspacePreparationStep, err := c.generateWorkspacePreparationStep(ttr.Status.TaskTestSpec.Inputs.WorkspaceContents)
			if err != nil {
				return nil, fmt.Errorf("error while generating workspace preparation step: %w", err)
			}
			task.Spec.Steps = append([]v1.Step{*workspacePreparationStep}, task.Spec.Steps...)
		}
	}

	taskRun = &v1.TaskRun{
		TypeMeta: metav1.TypeMeta{Kind: "TaskRun", APIVersion: "tekton.dev/v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:            ttr.GetTaskRunName(),
			Namespace:       taskRunNamespace,
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(ttr, schema.GroupVersionKind{Group: "tekton.dev", Version: "v1alpha1", Kind: "TaskTestRun"})},
			Labels:          map[string]string{pipeline.TaskTestRunLabelKey: ttr.Name},
		},
		Spec: taskRunSpec,
	}
	taskRun.Status.InitializeConditions()

	if ttr.Status.TaskTestSpec.Expects != nil {
		logger.Infof(`filling annotations for task test run %s`, ttr.Name)
		taskRun.Annotations = map[string]string{}
		expectedValuesJSON, err := json.Marshal(ttr.Status.TaskTestSpec.Expects)
		if err != nil {
			return nil, err
		}
		taskRun.Annotations[v1alpha1.AnnotationKeyExpectedValuesJSON] = string(expectedValuesJSON)
	}

	taskRun, err = c.PipelineClientSet.TektonV1().TaskRuns(taskRunNamespace).Create(ctx, taskRun, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	logger.Infof(`TaskRun successfully created: %v`, *taskRun)

	ttr.Status.TaskRunName = &taskRun.Name

	ttr.Status.StartTime = &metav1.Time{
		Time: c.Clock.Now(),
	}

	return taskRun, nil
}

func (r *Reconciler) generateWorkspacePreparationStep(initialWorkspaceContents []v1alpha1.InitialWorkspaceContents) (*v1.Step, error) {
	preparationStep := &v1.Step{
		Image:   r.Images.ShellImage,
		Name:    "prepare-workspace",
		Command: []string{"sh", "-c"},
		Args:    []string{""},
	}
	for i, ws := range initialWorkspaceContents {
		for j, object := range ws.Objects {
			objectPath := filepath.Clean(object.Path)

			if !filepath.IsAbs(objectPath) {
				objectPath = string(filepath.Separator) + objectPath
			}
			switch object.Type {
			case v1alpha1.InputDirectoryType:
				preparationStep.Args[0] += fmt.Sprintf(`mkdir -p $(workspaces.%s.path)%s`+"\n", ws.Name, objectPath)
			case v1alpha1.InputTextFileType:
				preparationStep.Args[0] += fmt.Sprintf(`mkdir -p $(workspaces.%s.path)%s`+"\n", ws.Name, filepath.Dir(objectPath))
				preparationStep.Args[0] += fmt.Sprintf(`printf "%%s" "%s" > $(workspaces.%s.path)%s`+"\n", object.Content, ws.Name, objectPath)
			default:
				return nil, fmt.Errorf(`%w: %w`, apiserver.ErrReferencedObjectValidationFailed, apis.ErrInvalidValue(
					object.Type, fmt.Sprintf("status.taskTestSpec.input.workspaceContents[%d].objects[%d]", i, j), fmt.Sprintf(`unknown type %q, allowed types are "TextFile" and "Directory"`, object.Type)))
			}
		}
	}
	return preparationStep, nil
}

func checkExpectationsForResults(ttrs *v1alpha1.TaskTestRunStatus, trs *v1.TaskRunStatus, expectationsMet *bool, diffs *string) error {
	ttrs.CompletionTime = trs.CompletionTime
	taskResults := trs.Results
	ttrs.Outcomes.Results = &[]v1alpha1.ObservedResults{}

	for i, expectedResult := range ttrs.TaskTestSpec.Expects.Results {
		var gotValue *v1.ResultValue
		j := slices.IndexFunc(taskResults, func(actualResult v1.TaskRunResult) bool {
			return actualResult.Name == expectedResult.Name
		})
		if j == -1 {
			if slices.ContainsFunc(ttrs.TaskRunStatus.TaskSpec.Results, func(res v1.TaskResult) bool {
				return res.Name == expectedResult.Name
			}) {
				gotValue = &v1.ResultValue{
					Type:      v1.ParamTypeString,
					StringVal: "",
				}
			} else {
				return apis.ErrInvalidValue(expectedResult.Name, fmt.Sprintf("status.taskTestSpec.expected.results[%d].name", i), fmt.Sprintf(`task %q has no Result named %q`, ttrs.TaskRunStatus.TaskSpec.DisplayName, expectedResult.Name))
			}
		} else {
			gotValue = &taskResults[j].Value
		}
		diff := cmp.Diff(expectedResult.Value, gotValue)
		ttrs.Outcomes.Results = ptr.To(append(*ttrs.Outcomes.Results, v1alpha1.ObservedResults{
			Name: expectedResult.Name,
			Want: expectedResult.Value,
			Got:  gotValue,
			Diff: diff,
		}))
		if diff != "" {
			*expectationsMet = false
			*diffs += fmt.Sprintf(`Result %s: `, expectedResult.Name) + diff
		}
	}

	return nil
}

func checkExpectationsForEnv(ttrs *v1alpha1.TaskTestRunStatus, trs *v1.TaskRunStatus, expectationsMet *bool, diffs *string) error {
	idx := slices.IndexFunc(trs.Results, func(result v1.TaskRunResult) bool {
		return result.Name == v1alpha1.ResultNameEnvironmentDump
	})
	if idx < 0 {
		err := errors.New("could not find environment dump for stepEnv")
		ttrs.MarkResourceFailed(v1alpha1.TaskTestRunReasonFailedValidation, err)
		return err
	}

	environments := trs.Results[idx].Value.StringVal
	pattern := regexp.MustCompile(`(".+?)=(.+",
)`)
	// environments = strings.ReplaceAll("["+environments+"]", "=", `":"`)
	environments = pattern.ReplaceAllString("["+environments+"]", `$1":"$2`)
	environments = strings.ReplaceAll(environments, ",\n}", "}")
	environments = strings.ReplaceAll(environments, ",\n]", "]")

	var stepEnvironments v1alpha1.StepEnvironmentList
	err := json.Unmarshal([]byte(environments), &stepEnvironments)
	if err != nil {
		return fmt.Errorf("problem while unmarshalling stepEnvironments: %w", err)
	}
	if ttrs.Outcomes.StepEnvs == nil {
		ttrs.Outcomes.StepEnvs = &[]v1alpha1.ObservedStepEnv{}
	}
	for _, step := range stepEnvironments {
		vars := []v1alpha1.ObservedEnvVar{}
		varsToCheck := ttrs.TaskTestSpec.Expects.Env
		if ttrs.TaskTestSpec.Expects.StepEnvs != nil {
		}
		for _, stepEnv := range ttrs.TaskTestSpec.Expects.StepEnvs {
			if stepEnv.StepName == step.Step {
				varsToCheck = append(varsToCheck, stepEnv.Env...)
			}
		}

		for _, expectedEnvVar := range varsToCheck {
			observation := v1alpha1.ObservedEnvVar{
				Name: expectedEnvVar.Name,
				Want: expectedEnvVar.Value,
				Got:  step.Environment[expectedEnvVar.Name],
			}
			observation.Diff = cmp.Diff(observation.Want, observation.Got)
			if observation.Diff != "" {
				*expectationsMet = false
				if diffs == nil {
					diffs = ptr.To("")
				}
				*diffs += fmt.Sprintf(`envVar %s in step %s: `, expectedEnvVar.Name, step.Step) + observation.Diff
			}
			vars = append(vars, observation)
		}
		ttrs.Outcomes.StepEnvs = ptr.To(append(*ttrs.Outcomes.StepEnvs, v1alpha1.ObservedStepEnv{
			StepName: step.Step,
			Env:      vars,
		}))
		// sorting to make this predicatable for testing
		slices.SortFunc(vars, func(a, b v1alpha1.ObservedEnvVar) int {
			return strings.Compare(a.Name, b.Name)
		})
		// sorting to make this predicatable for testing
		slices.SortFunc(*ttrs.Outcomes.StepEnvs, func(a, b v1alpha1.ObservedStepEnv) int {
			return strings.Compare(a.StepName, b.StepName)
		})
	}
	return nil
}

func checkExpectationsForFileSystemObjects(ctx context.Context, ttrs *v1alpha1.TaskTestRunStatus, trs *v1.TaskRunStatus, expectationsMet *bool, diffs *string) error {
	logger := logging.FromContext(ctx)
	idx := slices.IndexFunc(trs.Results, func(result v1.TaskRunResult) bool {
		return result.Name == v1alpha1.ResultNameFileSystemContents
	})
	if idx < 0 {
		err := errors.New("could not find result with file system observations")
		ttrs.MarkResourceFailed(v1alpha1.TaskTestRunReasonFailedValidation, err)
		logger.Error(err.Error())
		return err
	}
	fileSystemObservationsJSON := trs.Results[idx].Value.StringVal
	fileSystemObservations := &v1alpha1.StepFileSystemList{}
	err := json.Unmarshal([]byte(fileSystemObservationsJSON), fileSystemObservations)
	if err != nil {
		logger.Errorf("problem while unmarshalling file system observations: %w", err)
		return fmt.Errorf("problem while unmarshalling file system observations: %w", err)
	}
	fileSystemObservationsMap := fileSystemObservations.ToMap()
	fileSystemObservationsMap.SetStepNames(trs)
	if ttrs.Outcomes.FileSystemObjects == nil {
		ttrs.Outcomes.FileSystemObjects = &[]v1alpha1.ObservedStepFileSystemContent{}
	}
	for _, step := range ttrs.TaskTestSpec.Expects.FileSystemContents {
		stepFileSystemContent := v1alpha1.ObservedStepFileSystemContent{
			StepName: step.StepName,
			Objects:  []v1alpha1.ObservedFileSystemObject{},
		}
		for _, object := range step.Objects {
			observation := v1alpha1.ObservedFileSystemObject{
				Path:        object.Path,
				WantType:    object.Type,
				GotType:     fileSystemObservationsMap[step.StepName][object.Path].Type,
				WantContent: object.Content,
				GotContent:  fileSystemObservationsMap[step.StepName][object.Path].Content,
			}

			observation.DiffType = cmp.Diff(observation.WantType, observation.GotType)
			if observation.DiffType != "" {
				*expectationsMet = false
				*diffs += fmt.Sprintf(`file system object %q type in step %s: `, observation.Path, step.StepName) + observation.DiffType
			}

			// we can assume, that the empty string here means,
			// that no content was specified, since if the user
			// wanted to check, if a file was empty, they would
			// use the EmptyFile type instead.
			if observation.WantContent != "" {
				observation.DiffContent = cmp.Diff(observation.WantContent, observation.GotContent)
				if observation.DiffContent != "" {
					*expectationsMet = false

					*diffs += fmt.Sprintf(`file system object %q content in step %s: `, observation.Path, step.StepName) + observation.DiffContent
				}
			}

			stepFileSystemContent.Objects = append(stepFileSystemContent.Objects, observation)
		}
		*ttrs.Outcomes.FileSystemObjects = append(*ttrs.Outcomes.FileSystemObjects, stepFileSystemContent)
	}
	return nil
}

// Check that our Reconciler implements taskrunreconciler.Interface
var _ tasktestrunreconciler.Interface = (*Reconciler)(nil)

func init() {
	var err error
	cancelTaskRunPatchBytes, err = json.Marshal([]jsonpatch.JsonPatchOperation{
		{
			Operation: "add",
			Path:      "/spec/status",
			Value:     v1.TaskRunSpecStatusCancelled,
		},
		{
			Operation: "add",
			Path:      "/spec/statusMessage",
			Value:     v1.TaskRunCancelledByTaskTestMsg,
		}})
	if err != nil {
		log.Fatalf("failed to marshal TaskRun cancel patch bytes: %v", err)
	}
	timeoutTaskRunPatchBytes, err = json.Marshal([]jsonpatch.JsonPatchOperation{
		{
			Operation: "add",
			Path:      "/spec/status",
			Value:     v1.TaskRunSpecStatusCancelled,
		},
		{
			Operation: "add",
			Path:      "/spec/statusMessage",
			Value:     v1.TaskRunCancelledByTaskTestTimeoutMsg,
		}})
	if err != nil {
		log.Fatalf("failed to marshal TaskRun cancel patch bytes: %v", err)
	}
}
