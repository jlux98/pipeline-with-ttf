package tasktestrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/tektoncd/pipeline/pkg/apis/config"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline"
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
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
		message := fmt.Sprintf("TaskRun %q was cancelled. %s", ttr.Name, ttr.Spec.StatusMessage)
		err := c.failTaskRun(ctx, ttr, v1alpha1.TaskTestRunReasonCancelled, message)
		return c.finishReconcileUpdateEmitEvents(ctx, ttr, before, err)
	}

	// Check if the TaskRun has timed out; if it is, this will set its status
	// accordingly.
	if ttr.HasTimedOut(ctx, c.Clock) {
		message := fmt.Sprintf("TaskRun %q failed to finish within %q", ttr.Name, ttr.GetTimeout(ctx))
		err := c.failTaskRun(ctx, ttr, v1alpha1.TaskTestRunReasonTimedOut, message)
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

func (c *Reconciler) failTaskRun(ctx context.Context, tr *v1alpha1.TaskTestRun, cancelled v1alpha1.TaskTestRunReason, message string) error {
	// TODO(jlux98) implement this
	logging.FromContext(ctx).Error("boom: Totally failing the TaskTestRun right now")
	return nil
}

func (c *Reconciler) finishReconcileUpdateEmitEvents(ctx context.Context, tr *v1alpha1.TaskTestRun, beforeCondition *apis.Condition, previousError error) reconciler.Event {
	afterCondition := tr.Status.GetCondition(apis.ConditionSucceeded)
	if afterCondition.IsFalse() && !tr.IsCancelled() && tr.IsRetriable() {
		retryTaskRun(tr, afterCondition.Message)
		afterCondition = tr.Status.GetCondition(apis.ConditionSucceeded)
	}
	// Send k8s events and cloud events (when configured)
	events.Emit(ctx, beforeCondition, afterCondition, tr)

	errs := []error{previousError}

	joinedErr := errors.Join(errs...)
	if controller.IsPermanentError(previousError) {
		return controller.NewPermanentError(joinedErr)
	}
	return joinedErr
}

func retryTaskRun(tr *v1alpha1.TaskTestRun, s string) {
	// TODO(jlux98) implement this
	panic("unimplemented")
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
	var err error

	// Get the TaskTestRun's TaskRun if it should have one. Otherwise, create the TaskRun.
	var taskRun *v1.TaskRun

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
			return err
		}
	} else {
		// List TaskRuns that have a label with this TaskTestRun name.  Do not include other labels from the
		// TaskTestRun in this selector.  The user could change them during the lifetime of the TasTestkRun so the
		// current labels may not be set on a previously created TaskRun.
		logger.Infof("boom: Now retrieving TaskRun from list for TTR %s", ttr.Name)
		labelSelector := labels.Set{pipeline.TaskTestRunLabelKey: ttr.Name}
		trs, err := c.taskRunLister.TaskRuns(ttr.Namespace).List(labelSelector.AsSelector())
		if err != nil {
			logger.Errorf("Error listing task runs: %v", err)
			events.Emit(ctx, nil, ttr.Status.GetCondition(apis.ConditionSucceeded), ttr)
			return err
		}
		for index := range trs {
			tr := trs[index]
			if metav1.IsControlledBy(tr, ttr) {
				logger.Infof("boom: Now found TaskRun %s in list controlled by TTR %s", tr.Name, ttr.Name)
				taskRun = tr
				ttr.Status.TaskRunName = &taskRun.Name
			}
		}
	}

	if taskRun == nil {
		logger.Infof("boom: Now creating TaskRun for TTR %s", ttr.Name)
		taskRun, err = c.createTaskRun(ctx, ttr, rtr)
		if err != nil {
			if errors.Is(err, apiserver.ErrReferencedObjectValidationFailed) {
				ttr.Status.MarkResourceFailed(v1alpha1.TaskTestRunReasonFailedValidation, err)
				events.Emit(ctx, nil, ttr.Status.GetCondition(apis.ConditionSucceeded), ttr)
				return nil
			}
			logger.Errorf("Failed to create task run for taskTestRun %q: %v", ttr.Name, err)
			return err
		}
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
		if err := checkActualOutcomesAgainstExpectations(ctx, ttr, &taskRun.Status); err != nil {
			events.Emit(ctx, nil, ttr.Status.GetCondition(apis.ConditionSucceeded), ttr)
			return err
		}
	}

	logger.Infof("Successfully reconciled tasktestrun %s/%s with status: %#v", ttr.Name, ttr.Namespace, ttr.Status.GetCondition(apis.ConditionSucceeded))
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

func checkActualOutcomesAgainstExpectations(ctx context.Context, ttr *v1alpha1.TaskTestRun, taskRun *v1.TaskRunStatus) error {
	logger := logging.FromContext(ctx)
	var err error
	logger.Infof("boom: Status.TaskTestSpec.Expected == %v and Status.GetCondition(apis.ConditionSucceeded).IsUnknown() == %v for TTR %s", ttr.Status.TaskTestSpec.Expected, ttr.Status.GetCondition(apis.ConditionSucceeded).IsUnknown(), ttr.Name)
	if ttr.Status.TaskTestSpec.Expected != nil && ttr.Status.GetCondition(apis.ConditionSucceeded).IsUnknown() {
		logger.Infof("boom: Now checking outcomes for TTR %s", ttr.Name)
		ttr.Status.Outcomes = &v1alpha1.ObservedOutcomes{}
		expectationsMet := true
		diffs := ""

		// check results
		if ttr.Status.TaskTestSpec.Expected.Results != nil && ttr.Status.Outcomes.Results == nil {
			logger.Infof("boom: Now checking results for TTR %s", ttr.Name)
			ttr.Status.CompletionTime = taskRun.CompletionTime
			taskResults := taskRun.Results
			ttr.Status.Outcomes.Results = &[]v1alpha1.ObservedResults{}

			for _, expectedResult := range ttr.Status.TaskTestSpec.Expected.Results {
				var gotValue *v1.ResultValue
				i := slices.IndexFunc(taskResults, func(actualResult v1.TaskRunResult) bool {
					return actualResult.Name == expectedResult.Name
				})
				if i == -1 {
					gotValue = &v1.ResultValue{}
				} else {
					gotValue = &taskResults[i].Value
				}
				diff := cmp.Diff(expectedResult.Value, gotValue)
				ttr.Status.Outcomes.Results = ptr.To(append(*ttr.Status.Outcomes.Results, v1alpha1.ObservedResults{
					Name: expectedResult.Name,
					Want: expectedResult.Value,
					Got:  gotValue,
					Diff: diff,
				}))
				if diff != "" {
					expectationsMet = false
					diffs += fmt.Sprintf(`Result %s: `, expectedResult.Name) + diff
				}
			}
		}

		// check success status
		if ttr.Status.TaskTestSpec.Expected.SuccessStatus != nil {
			logger.Infof("boom: Now checking success status for TTR %s", ttr.Name)
			ttr.Status.Outcomes.SuccessStatus = &v1alpha1.ObservedSuccessStatus{
				Want: *ttr.Status.TaskTestSpec.Expected.SuccessStatus,
				Got:  taskRun.GetCondition(apis.ConditionSucceeded).IsTrue(),
			}
			ttr.Status.Outcomes.SuccessStatus.WantMatchesGot = ttr.Status.Outcomes.SuccessStatus.Want == ttr.Status.Outcomes.SuccessStatus.Got

			if !ttr.Status.Outcomes.SuccessStatus.WantMatchesGot {
				expectationsMet = false
				diffs += "observed success status did not match expectation\n"
			}
		}

		// check success reason
		if ttr.Status.TaskTestSpec.Expected.SuccessReason != nil {
			logger.Infof("boom: Now checking success reason for TTR %s", ttr.Name)
			ttr.Status.Outcomes.SuccessReason = &v1alpha1.ObservedSuccessReason{
				Want: *ttr.Status.TaskTestSpec.Expected.SuccessReason,
				Got:  v1.TaskRunReason(taskRun.Conditions[0].Reason),
			}
			diff := cmp.Diff(ttr.Status.Outcomes.SuccessReason.Want, ttr.Status.Outcomes.SuccessReason.Got)
			ttr.Status.Outcomes.SuccessReason.Diff = diff

			if ttr.Status.Outcomes.SuccessReason.Diff != "" {
				expectationsMet = false
				diffs += "SuccessReason: " + diff
			}
		}

		// check environment variables
		if ttr.Status.TaskTestSpec.Expected.Env != nil {
			logger.Infof("boom: Now checking environment variables for TTR %s", ttr.Name)
			idx := slices.IndexFunc(taskRun.Results, func(result v1.TaskRunResult) bool {
				return result.Name == v1alpha1.ResultNameEnvironmentDump
			})
			if idx < 0 {
				err := errors.New("could not find environment dump for stepEnv")
				ttr.Status.MarkResourceFailed(v1alpha1.TaskTestRunReasonFailedValidation, err)
				return err
			}

			environments := taskRun.Results[idx].Value.StringVal
			pattern := regexp.MustCompile(`(".+?)=(.+",
)`)
			// environments = strings.ReplaceAll("["+environments+"]", "=", `":"`)
			environments = pattern.ReplaceAllString("["+environments+"]", `$1":"$2`)
			environments = strings.ReplaceAll(environments, ",\n}", "}")
			environments = strings.ReplaceAll(environments, ",\n]", "]")
			logging.FromContext(ctx).Info(environments)

			var stepEnvironments v1alpha1.StepEnvironmentList
			err := json.Unmarshal([]byte(environments), &stepEnvironments)
			if err != nil {
				logger.Errorf("problem while unmarshalling stepEnvironments: %v", err)
				events.Emit(ctx, nil, ttr.Status.GetCondition(apis.ConditionSucceeded), ttr)
				return err
			}
			stepEnvironmentsMap := stepEnvironments.ToMap()
			if ttr.Status.Outcomes.StepEnvs == nil {
				ttr.Status.Outcomes.StepEnvs = &[]v1alpha1.ObservedStepEnv{}
			}
			for step := range stepEnvironmentsMap {
				vars := []v1alpha1.ObservedEnvVar{}
				for _, expectedEnvVar := range ttr.Status.TaskTestSpec.Expected.Env {
					observation := v1alpha1.ObservedEnvVar{
						Name: expectedEnvVar.Name,
						Want: expectedEnvVar.Value,
						Got:  stepEnvironmentsMap[step][expectedEnvVar.Name],
					}
					observation.Diff = cmp.Diff(observation.Want, observation.Got)
					if observation.Diff != "" {
						expectationsMet = false
						diffs += fmt.Sprintf(`envVar %s in step %s: `, expectedEnvVar.Name, step) + observation.Diff
					}
					vars = append(vars, observation)
				}
				ttr.Status.Outcomes.StepEnvs = ptr.To(append(*ttr.Status.Outcomes.StepEnvs, v1alpha1.ObservedStepEnv{
					StepName: step,
					Env:      vars,
				}))
			}
		}

		if ttr.Status.TaskTestSpec.Expected.FileSystemContents != nil {
			logger.Infof("boom: Now checking file system observations for TTR %s", ttr.Name)
			idx := slices.IndexFunc(taskRun.Results, func(result v1.TaskRunResult) bool {
				return result.Name == v1alpha1.ResultNameFileSystemContents
			})
			if idx < 0 {
				err := errors.New("could not find result with file system observations")
				ttr.Status.MarkResourceFailed(v1alpha1.TaskTestRunReasonFailedValidation, err)
				return err
			}
			fileSystemObservationsJSON := taskRun.Results[idx].Value.StringVal
			fileSystemObservations := &v1alpha1.StepFileSystemList{}
			err := json.Unmarshal([]byte(fileSystemObservationsJSON), fileSystemObservations)
			if err != nil {
				logger.Errorf("problem while unmarshalling file system observations: %v", err)
				events.Emit(ctx, nil, ttr.Status.GetCondition(apis.ConditionSucceeded), ttr)
				return err
			}
			logging.FromContext(ctx).Info(fileSystemObservations)
			fileSystemObservationsMap := fileSystemObservations.ToMap()
			if ttr.Status.Outcomes.FileSystemObjects == nil {
				ttr.Status.Outcomes.FileSystemObjects = &[]v1alpha1.ObservedStepFileSystemContent{}
			}
			for step := range fileSystemObservationsMap {
				pattern := regexp.MustCompile(`/tekton/run/(\d)/status`)
				stepIndex, _ := strconv.Atoi(pattern.ReplaceAllString(step, `$1`))
				var stepName string
				if taskRun != nil {
					stepName = taskRun.TaskSpec.Steps[stepIndex].Name
				}

				stepFileSystemContent := v1alpha1.ObservedStepFileSystemContent{
					StepName: stepName,
					Objects:  []v1alpha1.ObservedFileSystemObject{},
				}
				for i, expectedFileSystemContent := range ttr.Status.TaskTestSpec.Expected.FileSystemContents {
					if i == stepIndex {
						for _, object := range expectedFileSystemContent.Objects {
							observation := v1alpha1.ObservedFileSystemObject{
								Path:        object.Path,
								WantType:    object.Type,
								GotType:     fileSystemObservationsMap[step][object.Path].Type,
								WantContent: object.Content,
								GotContent:  fileSystemObservationsMap[step][object.Path].Content,
							}

							observation.DiffType = cmp.Diff(observation.WantType, observation.GotType)
							if observation.DiffType != "" {
								expectationsMet = false
								diffs += fmt.Sprintf(`file system object %q type in step %s: `, observation.Path, stepName) + observation.DiffType
							}

							// we can assume, that the empty string here means,
							// that no content was specified, since if the user
							// wanted to check, if a file was empty, they would
							// use the EmptyFile type instead.
							if observation.WantContent != "" {
								observation.DiffContent = cmp.Diff(observation.WantContent, observation.GotContent)
								if observation.DiffContent != "" {
									expectationsMet = false
									diffs += fmt.Sprintf(`file system object %q content in step %s: `, observation.Path, stepName) + observation.DiffContent
								}
							}

							stepFileSystemContent.Objects = append(stepFileSystemContent.Objects, observation)
						}
					}
				}
				ttr.Status.Outcomes.FileSystemObjects = ptr.To(append(*ttr.Status.Outcomes.FileSystemObjects, stepFileSystemContent))
			}
		}

		// set status and emit event
		beforeCondition := ttr.Status.GetCondition(apis.ConditionSucceeded)
		logger.Infof("boom: Now setting Status for TTR %s", ttr.Name)
		if expectationsMet {
			ttr.Status.MarkSuccessful()
			events.Emit(ctx, beforeCondition, ttr.Status.GetCondition(apis.ConditionSucceeded), ttr)
		} else {
			err = errors.New("not all expectations were met:\n" + diffs)
			ttr.Status.MarkResourceFailed(v1alpha1.TaskTestRunUnexpectatedOutcomes, err)
		}
	}
	return err
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

	if ttr.Status.TaskTestSpec.Expected != nil {
		expected := ttr.Status.TaskTestSpec.Expected
		if expected.Results != nil {
			declaredResults := task.Spec.Results
			for i, expectedResult := range ttr.Status.TaskTestSpec.Expected.Results {
				if !slices.ContainsFunc(declaredResults, func(result v1.TaskResult) bool {
					return result.Name == expectedResult.Name
				}) {
					return nil, fmt.Errorf(`%w: %w`, apiserver.ErrReferencedObjectValidationFailed, apis.ErrInvalidValue(expectedResult.Name, fmt.Sprintf("status.taskTestSpec.expected.results[%d].name", i), fmt.Sprintf(`task %q has no Result named %q`, task.Name, expectedResult.Name)))
				}
			}
		}
		if ttr.Status.TaskTestSpec.Expected.StepEnvs != nil {
			declaredSteps := task.Spec.Steps
			for i, stepEnv := range ttr.Status.TaskTestSpec.Expected.StepEnvs {
				if !slices.ContainsFunc(declaredSteps, func(step v1.Step) bool {
					return step.Name == stepEnv.StepName
				}) {
					return nil, fmt.Errorf(`%w: %w`, apiserver.ErrReferencedObjectValidationFailed, apis.ErrInvalidValue(stepEnv.StepName, fmt.Sprintf("status.taskTestSpec.expected.stepEnvs[%d].stepName", i), fmt.Sprintf(`task %q has no Step named %q`, task.Name, stepEnv.StepName)))
				}
			}
		}
		if ttr.Status.TaskTestSpec.Expected.FileSystemContents != nil {
			declaredSteps := task.Spec.Steps
			for i, stepFileSystem := range ttr.Status.TaskTestSpec.Expected.FileSystemContents {
				if !slices.ContainsFunc(declaredSteps, func(step v1.Step) bool {
					return step.Name == stepFileSystem.StepName
				}) {
					return nil, fmt.Errorf(`%w: %w`, apiserver.ErrReferencedObjectValidationFailed, apis.ErrInvalidValue(stepFileSystem.StepName, fmt.Sprintf("status.taskTestSpec.expected.fileSystemContents[%d].stepName", i), fmt.Sprintf(`task %q has no Step named %q`, task.Name, stepFileSystem.StepName)))
				}
			}
		}
	}

	taskRun = &v1.TaskRun{
		TypeMeta: metav1.TypeMeta{Kind: "TaskRun", APIVersion: "tekton.dev/v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:            ttr.Name + "-run",
			Namespace:       taskRunNamespace,
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(ttr, schema.GroupVersionKind{Group: "tekton.dev", Version: "v1alpha1", Kind: "TaskTestRun"})},
			Labels:          map[string]string{"tekton.dev/taskTestRun": ttr.Name},
		},
		Spec: v1.TaskRunSpec{TaskRef: &v1.TaskRef{Name: task.Name}},
	}
	taskRun.Status.InitializeConditions()

	if ttr.Status.TaskTestSpec.Expected != nil {
		logger.Infof(`filling annotations for task test run %s`, ttr.Name)
		taskRun.Annotations = map[string]string{}
		expectedValuesJSON, err := json.Marshal(ttr.Status.TaskTestSpec.Expected)
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

// Check that our Reconciler implements taskrunreconciler.Interface
var _ tasktestrunreconciler.Interface = (*Reconciler)(nil)
