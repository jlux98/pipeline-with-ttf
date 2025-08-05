package v1alpha1

import (
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:noStatus
// +genreconciler:krshapedlogic=false
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// TaskTestSuite represents a collection of test cases for verifying the
// functional requirements of one or more Tasks. TaskTestSuites execute when
// TaskTestSuiteRuns are created, which provide the input and output resources
// the TaskTest requires.
//
// +k8s:openapi-gen=true
type TaskTestSuite struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata"`

	// Spec holds the desired state of the TaskTestSuite from the client
	// +optional
	Spec TaskTestSuiteSpec `json:"spec"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// TaskTestList contains a list of TaskTests
type TaskTestSuiteList struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TaskTestSuite `json:"items"`
}

type TaskTestSuiteSpec struct {
	ExecutionMode TestSuiteExecutionMode `json:"executionMode"`

	// TaskTests is a list of references to the TaskTests, which make up the
	// suite.
	// +listType=map
	// +listMapKey=name
	TaskTests []SuiteTest `json:"taskTests"`
}

type TestSuiteExecutionMode string

// N2H Maybe a compromise in the form of a "Staggered" execution mode would
// be interesting, where a delay can be defined so that the runner doesn't
// always wait until the previous test has finished executing but still
// doesn't overwhelm the cluster with too many test being triggered at the
// same time.
const (
	Parallel   TestSuiteExecutionMode = "Parallel"
	Sequential TestSuiteExecutionMode = "Sequential"
)

type SuiteTest struct {
	Name        string      `json:"name"`
	TaskTestRef TaskTestRef `json:"taskTestRef"`
	// OnError defaults to StopAndFail if unset
	OnError v1.OnErrorType `json:"onError,omitempty"`
}
