package v1alpha1

import (
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	TaskTestSuiteRef *TaskTestSuiteRef `json:"taskTestSuiteRef"`

	// TaskTestSuiteSpec is a definition of a task test suite.
	TaskTestSuiteSpec *TaskTestSuiteSpec `json:"taskTestSuiteSpec"`

	// ExecutionMode specifies, whether the tests in this run will be executed
	// in parallel or sequentially. Valid values for this field are "Parallel"
	// and "Sequential".
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
	// +optional
	// +listType=atomic
	Workspaces []v1.WorkspaceBinding `json:"workspaces,omitempty"`

	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// Compute resources to use for this TaskRun
	//
	// +optional
	ComputeResources *corev1.ResourceRequirements `json:"computeResources,omitempty"`
}

type SuiteTaskTestRun struct {
	// TaskTestRef is a reference to a task test definition. If a task test
	// defined inline inside the test suite shares a name with a test defined
	// outside the suite, then the task defined inside the suite will be chosen.
	TaskTestRef *TaskTestRef `json:"taskTestRef"`

	TaskTestRunTemplate `json:",inline"`
}

// Status and its resources start here

type TaskTestSuiteRunStatus struct {
	duckv1.Status `json:",inline"`

	// TaskTestRunStatusFields inlines the status fields.
	TaskTestSuiteRunStatusFields `json:",inline"`
}

type TaskTestSuiteRunStatusFields struct {
	// TaskTestSpecs is the list containing the spec fields of the
	// TaskTests being executed in this suite.
	//
	// +listType=map
	// +listMapKey=name
	TaskTestSpecs []NamedTaskTestSpec `json:"taskTestSpecs"`

	// TaskTestRunStatuses is the list containing the status fields of the
	// TaskTestRuns responsible for executing this suite's TasksTests.
	//
	// +listType=map
	// +listMapKey=name
	TaskTestRunStatuses []SuiteTaskTestRunStatus `json:"taskTestRunStatuses"`

	// CompletionTime is the time the test completed.
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`
}

type NamedTaskTestSpec struct {
	// Name is the name the TaskTest to which the spec belongs
	Name string `json:"name"`

	// Spec is the spec field of the TaskTest being executed in this suite.
	Spec TaskTestSpec `json:"spec"`
}

type SuiteTaskTestRunStatus struct {
	// TaskTestRunName is the identifier given to the TaskTestRun responsible
	// for executing this specific TaskTest
	TaskTestRunName string `json:"taskTestRunName"`
	// Status is the status field of the TaskTestRun responsible for executing
	// this specific TaskTest
	TaskTestRunStatus TaskTestRunStatus `json:"taskTestRunStatus"`
}
