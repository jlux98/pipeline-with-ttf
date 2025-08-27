package v1alpha1

import (
	"context"
	"time"

	"github.com/tektoncd/pipeline/pkg/apis/config"
	apisconfig "github.com/tektoncd/pipeline/pkg/apis/config"
	pipelineErrors "github.com/tektoncd/pipeline/pkg/apis/pipeline/errors"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/clock"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
)

// +genclient
// +genreconciler:krshapedlogic=false
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// TaskTestSuiteRun represents the execution of a list of test cases.
//
// +k8s:openapi-gen=true
type TaskTestSuiteRun struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata"`

	// Spec holds the desired state of the TaskTest from the client
	// +optional
	Spec TaskTestSuiteRunSpec `json:"spec"`

	// +optional
	Status TaskTestSuiteRunStatus `json:"status,omitempty"`
}

func (tsr *TaskTestSuiteRun) GetNamespacedName() types.NamespacedName {
	return types.NamespacedName{Namespace: tsr.Namespace, Name: tsr.Name}
}

// HasStarted function check whether TaskTestRun has valid start time set in its status
func (ttr *TaskTestSuiteRun) HasStarted() bool {
	return ttr.Status.StartTime != nil && !ttr.Status.StartTime.IsZero()
}

// GetGroupVersionKind implements kmeta.OwnerRefable.
func (tsr *TaskTestSuiteRun) GetGroupVersionKind() schema.GroupVersionKind {
	return schema.GroupVersionKind{
		Group:   "tekton.dev",
		Version: "v1alpha1",
		Kind:    "TaskTestSuiteRun",
	}
}

// GetObjectMeta implements kmeta.OwnerRefable.
// Subtle: this method shadows the method (ObjectMeta).GetObjectMeta of TaskTestSuiteRun.ObjectMeta.
func (tsr *TaskTestSuiteRun) GetObjectMeta() metav1.Object {
	return &tsr.ObjectMeta
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// TaskTestRunList contains a list of TaskTestRuns
type TaskTestSuiteRunList struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TaskTestSuiteRun `json:"items"`
}

// Spec and its resources start here

// TaskTestSuiteRunSpec
type TaskTestSuiteRunSpec struct {
	// TaskTestSuiteRef is a reference to a task test suite definition.
	// Either this or TaskTestSuiteSpec must be set, if neither or both are set then
	// validation of this TaskTestSuiteRun fails.
	//
	// +optional
	TaskTestSuiteRef *TaskTestSuiteRef `json:"taskTestSuiteRef,omitempty"`

	// TaskTestSuiteSpec is a definition of a task test suite.
	// Either this or TaskTestSuiteSpec must be set, if neither or both are set then
	// validation of this TaskTestSuiteRun fails.
	//
	// +optional
	TaskTestSuiteSpec *TaskTestSuiteSpec `json:"taskTestSuiteSpec,omitempty"`

	// ExecutionMode specifies, whether the tests in this run will be executed
	// in parallel or sequentially. Valid values for this field are "Parallel"
	// and "Sequential".
	//
	// +kubebuilder:validation:Enum=Parallel;Sequential
	ExecutionMode TestSuiteExecutionMode `json:"executionMode"`

	// DefaultRunSpecTemplate defines the template after which the
	// TaskTestRuns for the tests in this suite are generated. It supports the
	// same fields as the Spec of a TaskTestRun with the exception of
	// TaskTestRef and the SpecStatus fields.
	//
	// +optional
	DefaultRunSpecTemplate TaskTestRunTemplate `json:"defaultRunSpecTemplate"`

	// RunSpecs is a list of RunSpecs, except that the
	// SpecStatus fields are not allowed. It contains all the tests that will be
	// executed by this run, in addition to providing the option of configuring
	// them on a case-by-case basis. Configurations made in this field overwrite
	// the default template.
	//
	// +optional
	// +listType=map
	// +listMapKey=name
	RunSpecs []SuiteTaskTestRun `json:"runSpecs"`

	// Used for cancelling a TaskTestSuiteRun
	//
	// +optional
	Status TaskTestRunSpecStatus `json:"status,omitempty"`

	// Status message for cancellation.
	//
	// +optional
	StatusMessage TaskTestRunSpecStatusMessage `json:"statusMessage,omitempty"`

	// Time after which the suite run times out. Defaults to 1 hour.
	// Refer Go's ParseDuration documentation for expected format: https://golang.org/pkg/time/#ParseDuration
	//
	// +optional
	Timeout *metav1.Duration `json:"timeout,omitempty"`
}

type TaskTestSuiteRef struct {
	Name string `json:"name"`
}

type TestSuiteExecutionMode string

// N2H: Maybe a compromise in the form of a "Staggered" execution mode would
// be interesting, where a delay can be defined so that the runner doesn't
// always wait until the previous test has finished executing but still
// doesn't overwhelm the cluster with too many test being triggered at the
// same time.
const (
	Parallel   TestSuiteExecutionMode = "Parallel"
	Sequential TestSuiteExecutionMode = "Sequential"
)

type TaskTestRunTemplate struct {
	// Workspaces is a list of WorkspaceBindings from volumes to workspaces.
	//
	// +listType=atomic
	// +optional
	Workspaces []v1.WorkspaceBinding `json:"workspaces,omitempty"`

	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// Compute resources to use for this TaskRun
	//
	// +optional
	ComputeResources *corev1.ResourceRequirements `json:"computeResources,omitempty"`
}

type SuiteTaskTestRun struct {
	// Name is an identifier for a task test. If a task test
	// defined inline inside the test suite shares a name with a test defined
	// outside the suite, then the task defined inside the suite will be chosen.
	Name string `json:"name"`

	TaskTestRunTemplate `json:",inline"`
}

// Status and its resources start here

type TaskTestSuiteRunStatus struct {
	duckv1.Status `json:",inline"`

	// TaskTestRunStatusFields inlines the status fields.
	TaskTestSuiteRunStatusFields `json:",inline"`
}

type TaskTestSuiteRunStatusFields struct {
	// StartTime is the time the build is actually started.
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// TaskTestSuiteName is the name of the referenced TaskTestSuite, if one is
	// referenced. If the TaskTestSuite is declared inline, then this field will
	// be nil.
	TaskTestSuiteName *string `json:"taskTestSuiteName"`

	// TaskTestSuiteSpec is the spec of the TaskTestSuite being run. This spec
	// can either be declared inline in the TaskTestSuiteRun manifest or it can
	// come from referencing a pre-existing TaskTestSuite
	TaskTestSuiteSpec *TaskTestSuiteSpec `json:"taskTestSuiteSpec"`

	// TaskTestRunStatuses is the list containing the status fields of the
	// TaskTestRuns responsible for executing this suite's TasksTests.
	TaskTestRunStatuses map[string]*TaskTestRunStatus `json:"taskTestRunStatuses"`

	// CompletionTime is the time the test completed.
	//
	// +optional
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`
}

type NamedTaskTestSpec struct {
	// Name is the name the TaskTest to which the spec belongs
	Name *string `json:"name"`

	// Spec is the spec field of the TaskTest being executed in this suite.
	Spec *TaskTestSpec `json:"spec"`
}

type SuiteTaskTestRunStatus struct {
	// TaskTestRunName is the identifier given to the TaskTestRun responsible
	// for executing this specific TaskTest
	TaskTestRunName string `json:"taskTestRunName"`
	// Status is the status field of the TaskTestRun responsible for executing
	// this specific TaskTest
	TaskTestRunStatus TaskTestRunStatus `json:"taskTestRunStatus"`
}

// InitializeConditions will set all conditions in taskRunCondSet to unknown for the TaskRun
// and set the started time to the current time
func (trs *TaskTestSuiteRunStatus) InitializeConditions() {
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

func (ttr *TaskTestSuiteRun) IsDone() bool {
	return !ttr.Status.GetCondition(apis.ConditionSucceeded).IsUnknown()
}

func (ttr *TaskTestSuiteRun) IsCancelled() bool {
	return ttr.Spec.Status == TaskTestRunSpecStatusCancelled
}

// GetTimeout returns the timeout for the TaskTestRun, or the default if not specified
func (ttr *TaskTestSuiteRun) GetTimeout(ctx context.Context) time.Duration {
	// Use the platform default is no timeout is set
	if ttr.Spec.Timeout == nil {
		defaultTimeout := time.Duration(config.FromContextOrDefaults(ctx).Defaults.DefaultTimeoutMinutes)
		return defaultTimeout * time.Minute //nolint:durationcheck
	}
	return ttr.Spec.Timeout.Duration
}

func (ttr *TaskTestSuiteRun) HasTimedOut(ctx context.Context, c clock.PassiveClock) bool {
	if ttr.Status.StartTime.IsZero() {
		return false
	}
	timeout := ttr.GetTimeout(ctx)
	// If timeout is set to 0 or defaulted to 0, there is no timeout.
	if timeout == apisconfig.NoTimeoutDuration {
		return false
	}
	runtime := c.Since(ttr.Status.StartTime.Time)
	return runtime > timeout
}

// MarkResourceFailed sets the ConditionSucceeded condition to ConditionFalse
// based on an error that occurred and a reason
func (trs *TaskTestSuiteRunStatus) MarkResourceFailed(reason TaskTestRunReason, err error) {
	taskTestRunCondSet.Manage(trs).SetCondition(apis.Condition{
		Type:    apis.ConditionSucceeded,
		Status:  corev1.ConditionFalse,
		Reason:  reason.String(),
		Message: pipelineErrors.GetErrorMessage(err),
	})
	succeeded := trs.GetCondition(apis.ConditionSucceeded)
	trs.CompletionTime = &succeeded.LastTransitionTime.Inner
	if trs.CompletionTime == nil {
		trs.CompletionTime = &metav1.Time{Time: time.Now()}
	}
}

// MarkSuccessful sets the ConditionSucceeded condition to ConditionUnknown
// with the reason and message.
func (trs *TaskTestSuiteRunStatus) MarkSuccessful() {
	taskTestRunCondSet.Manage(trs).SetCondition(apis.Condition{
		Type:    apis.ConditionSucceeded,
		Status:  corev1.ConditionTrue,
		Reason:  TaskTestRunReasonSuccessful.String(),
		Message: "All TaskTestRuns completed executing and were successful",
	})
}
