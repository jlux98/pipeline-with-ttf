package v1alpha1

import (
	"context"
	"time"

	"github.com/tektoncd/pipeline/pkg/apis/config"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
)

// +genclient
// +genreconciler:krshapedlogic=false
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// TaskTestRun represents the execution of a test case for verifying the functional
// requirements of a Task that is run either on its own or as part of a
// TaskTestSuiteRun. TaskTests execute when TaskTestRuns are created that provide
// the input parameters and resources and output resources the TaskTest
// requires.
//
// +k8s:openapi-gen=true
type TaskTestRun struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata"`

	// Spec holds the desired state of the TaskTest from the client
	//
	// +optional
	Spec TaskTestRunSpec `json:"spec"`

	// Status holds the status of the TaskTestRun
	//
	// +optional
	Status TaskTestRunStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// TaskTestRunList contains a list of TaskTestRuns
type TaskTestRunList struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TaskTestRun `json:"items"`
}

// Spec and its resources start here

// TaskTestRunSpec defines the desired state of TaskTest.
type TaskTestRunSpec struct {
	// TaskTestRef is a reference to a task test definition.
	// Either this or TaskTestSpec must be set, if neither or both are set then
	// validation of this TaskTestRun fails.
	//
	// +optional
	TaskTestRef *TaskTestRef `json:"taskTestRef,omitempty"`

	// TaskTestSpec is a task test definition.
	// Either this or TaskTestRef must be set, if neither or both are set then
	// validation of this TaskTestRun fails.
	//
	// +optional
	TaskTestSpec *TaskTestSpec `json:"taskTestSpec,omitempty"`

	// Workspaces is a list of WorkspaceBindings from volumes to workspaces.
	//
	// +listType=atomic
	// +optional
	Workspaces []v1.WorkspaceBinding `json:"workspaces,omitempty"`

	// Time after which one retry attempt times out. Defaults to 1 hour.
	// Refer Go's ParseDuration documentation for expected format: https://golang.org/pkg/time/#ParseDuration
	// +optional
	Timeout *metav1.Duration `json:"timeout,omitempty"`

	// Retries represents how many times this TaskTestRun should be retried in
	// the event of test failure.
	// +optional
	Retries int `json:"retries,omitempty"`

	// The default behavior is that if out of all the tries at least one
	// succeeds then the TaskTestRun is marked as successful. But if the field
	// allTriesMustSucceed is set to true then the TaskTestRun is marked as
	// successful if and only if all of its tries come up successful.
	// +optional
	AllTriesMustSucceed *bool `json:"allTriesMustSucceed,omitempty"`

	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// Used for cancelling a TaskTestRun (and maybe more later on)
	// +optional
	Status TaskTestRunSpecStatus `json:"status,omitempty"`

	// Status message for cancellation.
	// +optional
	StatusMessage TaskTestRunSpecStatusMessage `json:"statusMessage,omitempty"`

	// Compute resources to use for this TaskRun
	// +optional
	ComputeResources *corev1.ResourceRequirements `json:"computeResources,omitempty"`
}

type TaskTestRef struct {
	// Name of the referent; More info: http://kubernetes.io/docs/user-guide/identifiers#names
	Name string `json:"name"`
}

// TaskTestRunSpecStatus defines the TaskRun spec status the user can provide
type TaskTestRunSpecStatus string

// TaskTestRunSpecStatusMessage defines human readable status messages for the TaskRun.
type TaskTestRunSpecStatusMessage string

const (
	// TaskTestRunSpecStatusCancelled indicates that the user wants to cancel the task,
	// if not already cancelled or terminated
	TaskTestRunSpecStatusCancelled = "TaskRunCancelled"
)

// Status and its resources start here

type TaskTestRunStatus struct {
	duckv1.Status `json:",inline"`

	// TaskTestRunStatusFields inlines the status fields.
	TaskTestRunStatusFields `json:",inline"`
}

func (trs TaskTestRunStatus) SetDefaults(ctx context.Context) {

}

type TaskTestRunStatusFields struct {
	// TaskTestSpec is a copy of the Spec of the referenced TaskTest.
	// TODO(jlu98) decide, whether to also populate this field when TaskTests are defined inline
	//
	// +optional
	TaskTestSpec *NamedTaskTestSpec `json:"taskTestSpec,omitempty"`

	// TaskRunName is the name of the TaskRun responsible for executing this
	// test's Tasks.
	TaskRunName string `json:"taskRunName"`

	// TaskRunStatus is the status of the TaskRun responsible for executing this
	// test's Tasks.
	TaskRunStatus v1.TaskRunStatus `json:"taskRunStatus"`

	// StartTime is the time the build is actually started.
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// CompletionTime is the time the test completed.
	//
	// +optional
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`

	Outcomes ObservedOutcomes `json:"outcomes"`
}

type ObservedOutcomes struct {
	// +optional
	FileSystemObjects ObservedFileSystemObject `json:"fileSystemObjects,omitempty"`

	// Results contains a list of Results with both their expected and actual values
	//
	// +listType=map
	// +listMapKey=name
	// +optional
	Results []ObservedResults `json:"results,omitempty"`

	// StepEnv contains a list of environment variables with both their expected
	// and actual values.
	//
	// +listType=map
	// +listMapKey=stepName
	// +optional
	StepEnvs []ObservedStepEnv `json:"stepEnvs,omitempty"`

	SuccessStatus ObservedSuccessStatus `json:"successStatus,omitempty"`

	SuccessReason ObservedSuccessReason `json:"successReason,omitempty"`
}

type ObservedStepFileSystemContent struct {
	// StepName is the name of the step, whose file system was checked.
	StepName string `json:"stepName"`

	// Objects is a list of Observed File System Objects (currently possible:
	// text files, binary files and empty directories), which were expected to be
	// in the container's file system after the step had finished executing
	// +listType=atomic
	// +optional
	Objects []ObservedFileSystemObject `json:"objects,omitempty"`
}

type ObservedFileSystemObject struct {
	// Want describes the file system object the test expected to find at Path
	Want FileSystemObject `json:"want"`

	// Got describes the file system object the test found at Path
	Got FileSystemObject `json:"got"`

	// Diff describes, how Want and Got differ, using the typical
	// notation for go tests (prefacing lines from want with a - and lines from
	// got with a +)
	//
	// +optional
	Diff string `json:"diff,omitempty"`
}

type ObservedResults struct {
	// Name is the name of a Result object declared in the task test executed by
	// this task test run
	Name string `json:"name"`

	// Want describes the value the test expected this Result to have
	//
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Schemaless
	Want *v1.ResultValue `json:"want"`

	// Got describes the value this Result was found to have
	//
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Schemaless
	Got *v1.ResultValue `json:"got"`

	// Diff describes, how Want and Got differ, using the typical
	// notation for go tests (prefacing lines from want with a - and lines from
	// got with a +)
	//
	// +optional
	Diff string `json:"diff,omitempty"`
}

type ObservedEnvVar struct {
	// Name is the identifier of an environment variable
	Name string `json:"name"`

	// Want is the value the test expects that environment variable to have
	Want string `json:"want"`

	// Got is the value that environment variable was found to have
	Got string `json:"got"`

	// Diff describes, how Want and Got differ, using the typical
	// notation for go tests (prefacing lines from want with a - and lines from
	// got with a +)
	//
	// +optional
	Diff string `json:"diff,omitempty"`
}

type ObservedStepEnv struct {
	// StepName is the name of a step declared by the task under test
	StepName string `json:"stepName"`

	// Env is a list of observed environment variables, showing their expected
	// and actual values
	//
	// +listType=map
	// +listMapKey=name
	Env []ObservedEnvVar `json:"env"`
}

type ObservedSuccessStatus struct {
	// Want describes, whether the test exects the task under test to succeed
	Want bool `json:"want"`

	// Got reports, whether the task under test actually succeeded
	Got bool `json:"got"`

	// WantMatchesGot describes, whether Want and Got have the same value.
	//
	// +optional
	WantMatchesGot bool `json:"diff,omitempty"`
}

type ObservedSuccessReason struct {
	// Want describes, what Reason the test expected to find for the success
	// status of the task under test
	Want v1.TaskRunReason `json:"want"`

	// Got reports, what Reason was given for the success status of the task
	// under test
	Got v1.TaskRunReason `json:"got"`

	//  Diff describes, how Want and Got differ, using the typical
	// notation for go tests (prefacing lines from want with a - and lines from
	// got with a +)
	//
	// +optional
	Diff string `json:"diff,omitempty"`
}

// HasStarted function check whether TaskRun has valid start time set in its status
func (ttr *TaskTestRun) HasStarted() bool {
	return ttr.Status.StartTime != nil && !ttr.Status.StartTime.IsZero()
}

// InitializeConditions will set all conditions in taskRunCondSet to unknown for the TaskRun
// and set the started time to the current time
func (trs *TaskTestRunStatus) InitializeConditions() {
	started := false
	if trs.StartTime.IsZero() {
		trs.StartTime = &metav1.Time{Time: time.Now()}
		started = true
	}
	conditionManager := taskTestRunCondSet.Manage(trs)
	conditionManager.InitializeConditions()
	// Ensure the started reason is set for the "Succeeded" condition
	if started {
		initialCondition := conditionManager.GetCondition(apis.ConditionSucceeded)
		initialCondition.Reason = v1.TaskRunReasonStarted.String()
		conditionManager.SetCondition(*initialCondition)
	}
}

var taskTestRunCondSet = apis.NewBatchConditionSet()

// MarkSuccessful sets the ConditionSucceeded condition to ConditionUnknown
// with the reason and message.
func (trs *TaskTestRunStatus) MarkSuccessful() {
	taskTestRunCondSet.Manage(trs).SetCondition(apis.Condition{
		Type:   apis.ConditionSucceeded,
		Status: corev1.ConditionTrue,
		Reason: TaskTestRunReasonSuccessful.String(),
	})
}

// GetNamespacedName returns a k8s namespaced name that identifies this TaskRun
func (ttr *TaskTestRun) GetNamespacedName() types.NamespacedName {
	return types.NamespacedName{Namespace: ttr.Namespace, Name: ttr.Name}
}

// GetTimeout returns the timeout for the TaskTestRun, or the default if not specified
func (tr *TaskTestRun) GetTimeout(ctx context.Context) time.Duration {
	// Use the platform default is no timeout is set
	if tr.Spec.Timeout == nil {
		defaultTimeout := time.Duration(config.FromContextOrDefaults(ctx).Defaults.DefaultTimeoutMinutes)
		return defaultTimeout * time.Minute //nolint:durationcheck
	}
	return tr.Spec.Timeout.Duration
}

type TaskTestRunReason string

const (
	// TaskTestRunReasonSuccessful is the reason set when the TaskRun completed successfully
	TaskTestRunReasonSuccessful TaskTestRunReason = "All Expectations were met."
)

func (t TaskTestRunReason) String() string {
	return string(t)
}
