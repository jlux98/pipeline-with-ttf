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

// TaskTestSuiteRun represents the execution of all the test cases in a
// TaskTestSuite. TaskTestSuites execute when TaskTestRuns are created that
// provide the input and output resources the TaskTestSuite requires.
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

type TaskTestSuiteRunSpec struct {
	TaskTestSuiteRef TaskTestSuiteRef `json:"taskTestSuiteRef"`

	// DefaultTaskTestRunTemplate defines the template after which the
	// TaskTestRuns for the tests in this suite are generated. It supports the
	// same fields as the Spec of a TaskTestRun with the exception of
	// TaskTestRef and the SpecStatus fields. This field must be filled unless
	// there is a TaskTestRunTemplate specified for every TaskTest in the suite.
	//
	// +optional
	DefaultTaskTestRunTemplate TaskTestRunTemplate `json:"defaultTaskTestRunTemplate"`

	// TaskTestRunSpecs is a list of TaskTestRunSpecs (except for the
	// TaskTestRef and the SpecStatus fields), with each one being assigned to a
	// specific SuiteTaskTest. If every test in the referenced suite has a spec
	// defined in this list, then defaultTaskTestRunTemplate can be omitted.
	//
	// +optional
	// +listType=map
	// +listMapKey=name
	TaskTestRunSpecs []SuiteTaskTestRunTemplate `json:"taskTestRunSpecs"`
	// Used for cancelling a TaskTestRun (and maybe more later on)
	// +optional
	Status TaskTestRunSpecStatus `json:"status,omitempty"`
	// Status message for cancellation.
	// +optional
	StatusMessage TaskTestRunSpecStatusMessage `json:"statusMessage,omitempty"`
}

type TaskTestSuiteRef struct {
	Name string `json:"name"`
}

type TaskTestRunTemplate struct {
	// Workspaces is a list of WorkspaceBindings from volumes to workspaces.
	// +optional
	// +listType=atomic
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
	AllTriesMustSucceed bool `json:"allTriesMustSucceed,omitempty"`

	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// Compute resources to use for this TaskRun
	ComputeResources *corev1.ResourceRequirements `json:"computeResources,omitempty"`
}

type SuiteTaskTestRunTemplate struct {
	Name string              `json:"name"`
	Spec TaskTestRunTemplate `json:"spec"`
}

// Status and its resources start here

type TaskTestSuiteRunStatus struct {
	duckv1.Status `json:",inline"`

	// TaskTestRunStatusFields inlines the status fields.
	TaskTestSuiteRunStatusFields `json:",inline"`
}

type TaskTestSuiteRunStatusFields struct {
	// TaskTestRunSpecs is the list containing the spec fields of the
	// TaskTests being executed in this suite.
	//
	// +listType=map
	// +listMapKey=name
	TaskTestRunSpecs []SuiteTaskTestSpec `json:"taskTestRunSpecs"`

	// TaskTestRunStatuses is the list containing the status fields of the
	// TaskTestRuns responsible for executing this suite's TasksTests.
	//
	// +listType=map
	// +listMapKey=name
	TaskTestRunStatuses []SuiteTaskTestRunStatus `json:"taskTestRunStatuses"`

	// CompletionTime is the time the test completed.
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`

	// +listType=map
	// +listMapKey=name
	// +optional
	Outcomes []SuiteTaskObservedOutcomes `json:"outcomes"`
}

type SuiteTaskTestSpec struct {
	// Name is the identifier given to the TaskTest in the context of
	// the suite
	Name string `json:"name"`

	// Spec is the spec field of the TaskTest being executed in this suite.
	Spec TaskTestSpec `json:"spec"`
}

type SuiteTaskTestRunStatus struct {
	// Name is the identifier given to a TaskTest in the context of
	// the suite
	Name string `json:"name"`
	// TaskTestRunName is the identifier given to the TaskTestRun responsible
	// for executing this specific TaskTest
	TaskTestRunName string `json:"taskTestRunName"`
	// Status is the status field of the TaskTestRun responsible for executing
	// this specific TaskTest
	TaskTestRunStatus TaskTestRunStatus `json:"taskTestRunStatus"`
}

type SuiteTaskObservedOutcomes struct {
	Name             string `json:"name"`
	ObservedOutcomes `json:",inline"`
}
