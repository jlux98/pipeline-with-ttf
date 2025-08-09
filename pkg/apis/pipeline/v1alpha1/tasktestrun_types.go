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
	// +optional
	Spec TaskTestRunSpec `json:"spec"`

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
	TaskTestRef *TaskTestRef `json:"taskTestRef"`

	// TaskTestSpec is a task test definition.
	// Either this or TaskTestRef must be set, if neither or both are set then
	// validation of this TaskTestRun fails.
	//
	// +optional
	TaskTestSpec *TaskTestSpec `json:"taskTestSpec"`

	// Workspaces is a list of WorkspaceBindings from volumes to workspaces.
	//
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
	Name string `json:"name,omitempty"`
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

type TaskTestRunStatusFields struct {
	// TaskTestSpec is a copy of the Spec of the referenced TaskTest.
	// TODO(jlu98) decide, whether to also populate this field when TaskTests are defined inline
	//
	// +optional
	TaskTestSpec NamedTaskTestSpec `json:"taskTestSpec,omitempty"`

	// TaskRunName is the name of the TaskRun responsible for executing this
	// test's Tasks.
	TaskRunName string `json:"taskRunName"`

	// TaskRunStatus is the status of the TaskRun responsible for executing this
	// test's Tasks.
	TaskRunStatus v1.TaskRunStatus `json:"taskRunStatus"`

	// CompletionTime is the time the test completed.
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`

	Outcomes ObservedOutcomes `json:"outcomes"`
}

type ObservedOutcomes struct {
	// +optional
	FileSystemObjects ObservedFileSystemObject `json:"fileSystemObjects,omitempty"`

	// +listType=map
	// listMapKey=name
	// +optional
	Results []ObservedResults `json:"results,omitempty"`

	// +listType=map
	// listMapKey=name
	// +optional
	Env []ObservedEnvVar `json:"env,omitempty"`

	// +listType=map
	// listMapKey=stepName
	// +optional
	StepEnvs []ObservedStepEnv `json:"stepEnvs,omitempty"`

	SuccessStatus ObservedSuccessStatus `json:"successStatus,omitempty"`

	SuccessReason ObservedSuccessReason `json:"successReason,omitempty"`
}

type ObservedStepFileSystemContent struct {
	// StepName is the name of the step, whose file system was checked.
	StepName string `json:"stepName,omitempty"`

	// Objects is a list of Observed File System Objects (currently possible:
	// text files, binary files and empty directories), which were expected to be
	// in the container's file system after the step had finished executing
	// +listType=map
	// +listMapKey=name
	// +optional
	Objects []ObservedFileSystemObject `json:"objects,omitempty"`
}

type ObservedFileSystemObject struct {
	// Want describes the file system object the test expected to find at Path
	Want FileSystemObject `json:"want,omitempty"`

	// Got describes the file system object the test found at Path
	Got FileSystemObject `json:"got,omitempty"`

	// Diff describes, how Want and Got differ, using the typical
	// notation for go tests (prefacing lines from want with a - and lines from
	// got with a +)
	//
	// +optional
	Diff string `json:"diff,omitempty"`
}

type ObservedResults struct {
	Name string `json:"name,omitempty"`
	// +optional
	Diff string `json:"diff,omitempty"`

	Got  v1.TaskResult `json:"got,omitempty"`
	Want v1.TaskResult `json:"want,omitempty"`
}

type ObservedEnvVar struct {
	Name string `json:"name,omitempty"`
	Want string `json:"want,omitempty"`
	Got  string `json:"got,omitempty"`
	// +optional
	Diff string `json:"diff,omitempty"`
}

type ObservedStepEnv struct {
	StepName string `json:"stepName,omitempty"`
	// +listType=map
	// +listMapKey=name
	Env []ObservedEnvVar `json:"env,omitempty"`
}

type ObservedSuccessStatus struct {
	Want bool `json:"want,omitempty"`
	Got  bool `json:"got,omitempty"`
}

type ObservedSuccessReason struct {
	Want v1.TaskRunReason `json:"want,omitempty"`
	Got  v1.TaskRunReason `json:"got,omitempty"`
	// +optional
	Diff string `json:"diff,omitempty"`
}
