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
// a Task that is run either on its own or as part of a TaskTestSuiteRun. TaskTests
// execute when TaskTestRuns are created that provide the input parameters and
// resources and output resources the TaskTest requires.
//
// +k8s:openapi-gen=true
type TaskTest struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata"`

	// Spec holds the desired state of the TaskTest from the client
	//
	// +optional
	Spec TaskTestSpec `json:"spec,omitempty"`
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
	// TaskRef is a reference to a task definition, which must be in the same
	// namespace as the this test.
	// N2H: in the future this might use v1.TaskRef and be able to resolve
	// remote tasks.
	TaskRef *SimpleTaskRef `json:"taskRef,omitempty"`

	// Inputs represents the test data for executing the test case.
	//
	// +optional
	Inputs *TaskTestInputs `json:"inputs,omitempty"`

	// Expected contains the data, which the TaskTestRun controller will
	// use to check, whether a TaskTestRun was successful or not.
	// If this field is left empty, then the TaskTestRun is deemed successful,
	// if the TaskRun completes without a failure occurring.
	// +optional
	Expected *ExpectedOutcomes `json:"expected,omitempty"`
}

type SimpleTaskRef struct {
	Name string `json:"name"`
}

// TaskTestInputs holds the test data, which the TaskTestRun controller uses to
// prepare the environments necessary for running the test
type TaskTestInputs struct {
	// Parameters declares parameters passed to the task under test.
	//
	// +optional
	Params v1.Params `json:"params,omitempty"`

	// List of environment variables to set in all of the Task's Steps.
	//
	// +optional
	// +patchMergeKey=name
	// +patchStrategy=merge
	// +listType=atomic
	Env []corev1.EnvVar `json:"env,omitempty" patchMergeKey:"name" patchStrategy:"merge" protobuf:"bytes,7,rep,name=env"`

	// List of Step environments, where environment variables can be individually
	// set for each one of the Task's Steps. Values set here will overwrite
	// values set in 'env'.
	//
	// +listType=map
	// +listMapKey=name
	// +optional
	StepEnvs []StepEnv `json:"stepEnvs,omitempty"`

	// List of Workspaces with preset values, which will be initialized for any
	// runs of the task under test.
	//
	// +listType=map
	// +listMapKey=name
	// +optional
	WorkspaceContents []InitialWorkspaceContents `json:"workspaceContents,omitempty"`
}

func extractNamesFromWorkspaceContents(list []InitialWorkspaceContents) []string {
	result := make([]string, len(list))
	for i := range list {
		result[i] = list[i].Name
	}
	return result
}

func extractNamesFromTaskResults(list []v1.TaskResult) []string {
	result := make([]string, len(list))
	for i := range list {
		result[i] = list[i].Name
	}
	return result
}

func extractPathsFromInputFileSystemObjects(list []InputFileSystemObject) []string {
	result := make([]string, len(list))
	for i := range list {
		result[i] = list[i].Path
	}
	return result
}

func extractPathsFromFileSystemObjects(list []FileSystemObject) []string {
	result := make([]string, len(list))
	for i := range list {
		result[i] = list[i].Path
	}
	return result
}

// StepEnv contains the name of a step as defined the manifest of the Task under
// test and a list of environment variable declarations to be set for this step.
// TODO(jlux98) find a better name for this type
type StepEnv struct {
	// StepName is the name of the step for whom these environment variables will be set.
	StepName string `json:"stepName"`

	// List of environment variables to set for this step.
	//
	// +optional
	// +patchMergeKey=name
	// +patchStrategy=merge
	// +listType=atomic
	Env []corev1.EnvVar `json:"env,omitempty" patchMergeKey:"name" patchStrategy:"merge" protobuf:"bytes,7,rep,name=env"`
}

// InitialWorkspaceContents describes the desired contents of a workspace
// declared in the Task under Test before starting the test.
// N2H: it might be useful to be able to populate a workspace with files from a
// git repo.
type InitialWorkspaceContents struct {
	// Name is the name of the workspace as declared by the Task under test.
	Name string `json:"name"`

	// Objects is a list of file system objects to be placed in the specified
	// workspace. Relative paths are interpreted from the root of the workspace,
	// and for absolute paths the leading '/' denotes the root of the workspace.
	// If the type chosen for the object is 'Directory', then an empty directory
	// will be created at the location denoted by Path and Content must not be
	// fille. I the type chosen for the object is 'TextFile', then a text file
	// with Content as its content will be created at that location.
	//
	// +listType=map
	// +listMapKey=path
	Objects []InputFileSystemObject `json:"objects"`
}

// InputFileSystemObject describes a file system object to be placed when
// setting up a workspace for testing a Task by giving a path to the object, the
// type of the object and in case it is a text file the contents of that text
// file.
type InputFileSystemObject struct {
	// Path is the path to this file system object
	Path string `json:"path"`

	// Type is the type of this file system object.
	Type InputFileSystemObjectType `json:"type"`

	// The content of the file system object. Setting this value is only
	// acceptable, if the field Type is set to 'TextFile'.
	// N2H: it might be useful to be able to populate the contents field using
	// values from a ConfigMap or Secret.
	//
	// +optional
	Content string `json:"content,omitempty"`
}

// InputFileSystemObjectType is an enum containing file system object types
// supported for populating a workspace with.
type InputFileSystemObjectType string

const (
	InputDirectoryType InputFileSystemObjectType = "Directory" // a directory
	InputTextFileType  InputFileSystemObjectType = "TextFile"  // a text file of any encoding
)

// ExpectedOutcomes defines the outcomes that should be observed after
// executing the Task under test.
type ExpectedOutcomes struct {
	// Results is a list of Results declared in the Task under test and the
	// values the Task is expected to fill these Results with given the input
	// data.
	//
	// +listType=atomic
	// +optional
	Results []v1.TaskResult `json:"results,omitempty"`

	// List of environment variables with expected values to be checked for in
	// all of the Task's Steps.
	//
	// +optional
	// +patchMergeKey=name
	// +patchStrategy=merge
	// +listType=atomic
	Env []corev1.EnvVar `json:"env,omitempty" patchMergeKey:"name" patchStrategy:"merge" protobuf:"bytes,7,rep,name=env"`

	// List of Step environments, where expected values for environment
	// variables can be individually defined for all of the Task's Steps.
	// Expected values defined here will take precedence over expectations
	// defined in 'env'.
	//
	// +listType=atomic
	// +optional
	StepEnvs []StepEnv `json:"stepEnvs,omitempty"`

	// SuccessStatus reports, whether the TaskRuns initiated by this test are
	// expected to succeed. This is useful for testing cases in which the Task
	// is supposed to fail because of a faulty input.
	//
	// +optional
	SuccessStatus *bool `json:"successStatus,omitempty"`

	// SuccessReason is the reason, with which the TaskRuns initiated by this
	// test are expected to be marked upon completion.
	//
	// +optional
	SuccessReason *v1.TaskRunReason `json:"successReason,omitempty"`

	// FileSystemContents is a list step names, each one paired with a list of expected
	// file system objects.
	//
	// +listType=map
	// +listMapKey=stepName
	// +optional
	FileSystemContents []ExpectedStepFileSystemContent `json:"fileSystemContents,omitempty"`
}

// ExpectedStepFileSystemContent contains the name of a step as declared in the
// Task under Test and a list of file system objects.
//
// The rules for these file system objects are as follows: If the Type is
// not set it will default to "AnyObjectType". If Type is set to a type other
// than "TextFile", then Content must be left empty. Path value must be an
// absolute path, variable substitution for workspace paths is possible.
type ExpectedStepFileSystemContent struct {
	// StepName is the name of the step, whose file system will be checked for
	// the objects in FileSystemObject.
	StepName string `json:"stepName"`

	// Objects is a list of File System Objects, which are expected to be
	// in the container's file system after the step has finished executing (or
	// in the case of Type being set to "None" expected to not be there). If
	// this field is left empty, then it will default to "AnyObjectType".
	//
	// +listType=map
	// +listMapKey=path
	// +optional
	Objects []FileSystemObject `json:"objects,omitempty"`
}

// FileSystemObject describes a file system object by giving a path to the
// object, the type of the object and in case it is a text file the contents of
// that text file. Path value must be an absolute path, variable substitution for workspace paths is possible.
type FileSystemObject struct {
	// Path is the path to this file system object
	Path string `json:"path"`

	// Type is the type of this file system object. The values, which are
	// acceptable for this field, are defined in the enum FileSystemObjectType
	//
	// +optional
	Type FileSystemObjectType `json:"type,omitempty"`

	// The content of the file system object. Setting this value is only
	// acceptable, if the field Type is set to 'TextFile'.
	// N2H: it might be useful to be able to populate the contents field using
	// values from a ConfigMap or Secret.
	//
	// +optional
	Content string `json:"content,omitempty"`
}

// FileSystemObjectType is an enum containing the possible values for describing
// a file system object's type.
type FileSystemObjectType string

const (
	DirectoryType  FileSystemObjectType = "Directory"     // a directory
	EmptyFileType  FileSystemObjectType = "EmptyFile"     // a text file of any encoding
	TextFileType   FileSystemObjectType = "TextFile"      // a text file of any encoding
	BinaryFileType FileSystemObjectType = "BinaryFile"    // any type of binary file
	AnyFileType    FileSystemObjectType = "AnyFileType"   // any type of file
	AnyObjectType  FileSystemObjectType = "AnyObjectType" // any file or directory
	None           FileSystemObjectType = "None"          // no file at the given location
)

func (f FileSystemObjectType) String() string {
	return string(f)
}

const AnnotationKeyExpectedValuesJSON string = "ExpectedValuesJSON"
const ResultNameEnvironmentDump string = "Testing|Environment"
const ResultNameFileSystemContents string = "Testing|FileSystemContent"
