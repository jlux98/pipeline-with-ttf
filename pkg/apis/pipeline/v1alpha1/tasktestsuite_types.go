package v1alpha1

import (
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
	// TaskTests is a list of the TaskTests, which make up the
	// suite. TaskTests can be added to this list by reference or be defined
	// directly inside the list.
	//
	// +listType=atomic
	TaskTests []SuiteTest `json:"taskTests"`
}

type SuiteTest struct {
	// Name is the identifier for a test in the context of this suite.
	Name string `json:"name"`

	// TaskTestRef is a reference to an existing Task.
	// Either this or TaskTestSpec must be set, if neither or both are
	// set then validation of this SuiteTest fails.
	//
	// +optional
	TaskTestRef *TaskTestRef `json:"taskTestRef,omitempty"`

	// TaskTestSpec is a specification of a task test
	// Either this or TaskTestRef must be set, if neither or both are
	// set then validation of this SuiteTest fails.
	//
	// +optional
	TaskTestSpec *TaskTestSpec `json:"taskTestSpec,omitempty"`

	// OnError specifies, how the suite will behave, if this test fails.
	// "StopSchedulingAndFail" means, that no new test will be scheduled but
	// tests already running will be able to finish, after which the suite
	// execution is marked as a failure. "CancelRunningAndFail"
	// means, that all other unfinished tests will be cancelled immediately and
	// the suite execution is marked as a failure. "Continue" means, that if the
	// test fails the
	// suite is still executed as if the test succeeded. This field defaults to
	// "CancelRunningAndFail" if unset
	//
	// +optional
	OnError OnTestErrorType `json:"onError,omitempty"`

	// Retries represents how many times this TaskTestRun should be retried in
	// the event of test failure.
	//
	// +optional
	Retries int `json:"retries,omitempty"`

	// The default behavior is that if out of all the tries at least one
	// succeeds then the TaskTestRun is marked as successful. But if the field
	// allTriesMustSucceed is set to true then the TaskTestRun is marked as
	// successful if and only if all of its tries come up successful.
	//
	// +optional
	AllTriesMustSucceed *bool `json:"allTriesMustSucceed,omitempty"`

	// Time after which one retry attempt times out. Defaults to 1 hour.
	// Refer Go's ParseDuration documentation for expected format: https://golang.org/pkg/time/#ParseDuration
	//
	// +optional
	Timeout *metav1.Duration `json:"timeout,omitempty"`
}

func (st SuiteTest) GetName() string {
	return st.Name
}

// OnErrorType defines a list of supported exiting behavior of a container on error
type OnTestErrorType string

const (
	// StopAndFail indicates exit the taskRun if the container exits with non-zero exit code
	StopSchedulingAndFail OnTestErrorType = "StopSchedulingAndFail"
	// StopAndFail indicates exit the taskRun if the container exits with non-zero exit code
	CancelRunningAndFail OnTestErrorType = "CancelRunningAndFail"
	// Continue indicates continue executing the rest of the steps irrespective of the container exit code
	Continue OnTestErrorType = "Continue"
)

func (st SuiteTest) GetTaskTestRunName(suiteRunName string) string {
	return suiteRunName + "-" + st.Name
}
