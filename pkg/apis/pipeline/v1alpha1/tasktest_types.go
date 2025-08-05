package v1alpha1

import (
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:noStatus
// +genreconciler:krshapedlogic=false
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// TaskTest represents a test case for verifying the functional requirements of
// a Task that is run either on its own or as part of a TaskTestSuite. TaskTests
// execute when TaskTestRuns are created that provide the input parameters and
// resources and output resources the TaskTest requires.
//
// +k8s:openapi-gen=true
type TaskTest struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata"`

	// Spec holds the desired state of the TaskTest from the client
	// +optional
	Spec TaskTestSpec `json:"spec"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// TaskTestList contains a list of TaskTests
type TaskTestList struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TaskTest `json:"items"`
}

// TaskTestSpec defines the desired state of TaskTest.
type TaskTestSpec struct {
	// TaskRef is a reference to a task definition.
	TaskRef *v1.TaskRef `json:"taskRef"`

	// Inputs represents the test data for executing the test case.
	Inputs TaskTestInputs `json:"inputs"`

	// Expected contains the data, which the TaskTestRun controller will
	// use to check, whether a TaskTestRun was successful or not.
	// If this field is left empty, then the TaskTestRun is deemed successful,
	// if the TaskRun completes without a failure occurring.
	// +optional
	Expected ExpectedOutcomes `json:"expected,omitempty"`
}

type TaskTestInputs struct {
	// Parameters declares parameters passed to the task under test.
	// +optional
	Params v1.Params `json:"params,omitempty"`

	// List of environment variables to set in all of the Task's Steps.
	// +optional
	// +patchMergeKey=name
	// +patchStrategy=merge
	// +listType=atomic
	Env []corev1.EnvVar `json:"env,omitempty" patchMergeKey:"name" patchStrategy:"merge" protobuf:"bytes,7,rep,name=env"`

	// List of Step environment, where environment variables can be individually
	// set for all of the Task's Steps.
	// +listType=map
	// +listMapKey=name
	// +optional
	StepEnvs []StepEnv `json:"stepEnvs,omitempty"`

	// List of Workspaces with preset values, which will be initialized for any
	// runs of the TaskTest.
	// +listType=map
	// +listMapKey=name
	// +optional
	WorkspaceContents []WorkspaceContentDeclaration `json:"workspaceContents,omitempty"`
}

type StepEnv struct {
	// List of environment variables to set in all of the Task's Steps.
	// +optional
	// +patchMergeKey=name
	// +patchStrategy=merge
	// +listType=atomic
	Env []corev1.EnvVar `json:"env,omitempty" patchMergeKey:"name" patchStrategy:"merge" protobuf:"bytes,7,rep,name=env"`
}

// WorkspaceContentDeclaration describes how a workspace passed into the pipeline should be
// mapped to a task's declared workspace.
type WorkspaceContentDeclaration struct {
	// Name is the name of the workspace as declared by the task
	Name string `json:"name"`

	// +listType=map
	// +listMapKey=path
	Objects []FileSystemObject `json:"objects"`
}

type FileSystemObject struct {
	Path string               `json:"path"`
	Type FileSystemObjectType `json:"type"`
	// +optional
	Content string `json:"content,omitempty"`
}

type FileSystemObjectType string

const (
	TextFile   FileSystemObjectType = "TextFile"
	EmptyDir   FileSystemObjectType = "EmptyDir"
	BinaryFile FileSystemObjectType = "BinaryFile" // currently only supported for expected objects, not inputs
	None       FileSystemObjectType = "None"       // only meant for TaskTestRunStatus if an expected object can't be found
)

type ExpectedOutcomes struct {

	// +listType=atomic
	// +optional
	Results []v1.TaskResult `json:"results,omitempty"`

	// List of environment variables with expected values to be checked for in
	// all of the Task's Steps.
	// +optional
	// +patchMergeKey=name
	// +patchStrategy=merge
	// +listType=atomic
	Env []corev1.EnvVar `json:"env,omitempty" patchMergeKey:"name" patchStrategy:"merge" protobuf:"bytes,7,rep,name=env"`

	// List of Step environments, where expected values for environment
	// variables can be individually set for all of the Task's Steps.
	//  +optional
	//  +listType=atomic
	StepEnvs []StepEnv `json:"stepEnvs,omitempty"`

	// SuccessStatus reports, whether the TaskRuns initiated by this test are
	// expected to succeed. This is useful for testing cases in which the Task
	// is supposed to fail because of a faulty input.
	SuccessStatus bool `json:"successStatus,omitempty"`

	// SuccessReason is the reason, with which the TaskRuns initiated by this
	// test are expected to be marked upon completion.
	SuccessReason v1.TaskRunReason `json:"successReason,omitempty"`

	// FileSystemContents is a list step names, each one paired with a list of expected
	// file system objects.
	// +listType=map
	// +listMapKey=stepName
	// +optional
	FileSystemContents []StepFileSystemContent `json:"fileSystemContents"`
}

type StepFileSystemContent struct {
	// StepName is the name of the step, whose file system will be checked for
	// the objects in FileSystemObject.
	StepName string `json:"stepName,omitempty"`

	// Objects is a list of File System Objects (currently possible:
	// text files, binary files and empty directories), which are expected to be
	// in the container's file system after the step has finished executing
	// +listType=map
	// +listMapKey=name
	// +optional
	Objects []FileSystemObject `json:"objects,omitempty"`
}
