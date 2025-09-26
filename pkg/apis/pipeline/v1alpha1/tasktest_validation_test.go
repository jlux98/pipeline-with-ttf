package v1alpha1_test

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1alpha1"
	"github.com/tektoncd/pipeline/test/diff"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"knative.dev/pkg/apis"
)

func TestFileSystemObject_Invalid(t *testing.T) {
	type testCase struct {
		fileSystemObject v1alpha1.FileSystemObject
		want             *apis.FieldError
	}
	tests := []struct {
		name string
		tc   testCase
	}{
		{
			name: "type=directory but contents not empty",
			tc: testCase{
				fileSystemObject: v1alpha1.FileSystemObject{
					Path:    "/path/to/object",
					Type:    "Directory",
					Content: "content",
				},
				want: apis.ErrDisallowedFields("content"),
			},
		}, {
			name: "type has invalid value",
			tc: testCase{
				fileSystemObject: v1alpha1.FileSystemObject{
					Path: "/path/to/object",
					Type: "InvalidFileType",
				},
				want: apis.ErrInvalidValue("InvalidFileType", "type"),
			},
		}, {
			name: "path contains 0 byte",
			tc: testCase{
				fileSystemObject: v1alpha1.FileSystemObject{
					Path: "/path/to\000/object",
					Type: "TextFile",
				},
				want: apis.ErrInvalidValue("/path/to\000/object", "path", "illegal character \000 detected"),
			},
		}, {
			name: "path not absolute",
			tc: testCase{
				fileSystemObject: v1alpha1.FileSystemObject{
					Path: "path/to/object",
					Type: "TextFile",
				},
				want: apis.ErrInvalidValue("path/to/object", "path", "path must start with a '/'"),
			},
		},
	}
	for _, ts := range tests {
		t.Run(ts.name, func(t *testing.T) {
			ctx := t.Context()
			err := ts.tc.fileSystemObject.Validate(ctx)
			if d := cmp.Diff(ts.tc.want.Error(), err.Error()); d != "" {
				t.Error(diff.PrintWantGot(d))
			}
		})
	}
}

func TestTaskTest_Invalid(t *testing.T) {
	type testCase struct {
		taskTest *v1alpha1.TaskTest
		want     *apis.FieldError
		wc       func(context.Context) context.Context
	}
	tests := []struct {
		name string
		tc   testCase
	}{
		{
			name: "invalid tasktestspec",
			tc: testCase{
				taskTest: &v1alpha1.TaskTest{},
				want:     apis.ErrGeneric(`invalid resource name "": must be a valid DNS label`, "metadata.name"),
			},
		}, {
			name: "parameter names not unique",
			tc: testCase{
				taskTest: &v1alpha1.TaskTest{ObjectMeta: metav1.ObjectMeta{
					Name: "tt",
				},
					Spec: v1alpha1.TaskTestSpec{
						TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
						Inputs: &v1alpha1.TaskTestInputs{
							Params: v1.Params{
								{
									Name:  "name",
									Value: v1.ParamValue{StringVal: "value"},
								}, {
									Name:  "name",
									Value: v1.ParamValue{StringVal: "value"},
								},
							},
						},
					},
				},
				want: apis.ErrMultipleOneOf("spec.inputs.params[name].name"),
			},
		}, {
			name: "input workspace names not unique",
			tc: testCase{
				taskTest: &v1alpha1.TaskTest{ObjectMeta: metav1.ObjectMeta{
					Name: "tt",
				},
					Spec: v1alpha1.TaskTestSpec{
						TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
						Inputs: &v1alpha1.TaskTestInputs{
							WorkspaceContents: []v1alpha1.InitialWorkspaceContents{{
								Name: "name",
								Objects: []v1alpha1.InputFileSystemObject{{Path: "/path/to/object0",
									Type: "Directory",
								}},
							}, {
								Name: "name",
								Objects: []v1alpha1.InputFileSystemObject{{Path: "/path/to/object1",
									Type: "Directory",
								}},
							},
							},
						},
					},
				},
				want: apis.ErrMultipleOneOf("spec.inputs.workspaceContents[1].name"),
			},
		}, {
			name: "input workspace object path not unique",
			tc: testCase{
				taskTest: &v1alpha1.TaskTest{ObjectMeta: metav1.ObjectMeta{
					Name: "tt",
				},
					Spec: v1alpha1.TaskTestSpec{
						TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
						Inputs: &v1alpha1.TaskTestInputs{
							WorkspaceContents: []v1alpha1.InitialWorkspaceContents{{
								Name: "name0",
								Objects: []v1alpha1.InputFileSystemObject{
									{
										Path: "/object/path",
										Type: "TextFile",
										Content: &v1alpha1.InputFileSystemObjectContent{
											StringContent: `
											content
											`,
										},
									}, {
										Path: "/object/path",
										Type: "TextFile",
										Content: &v1alpha1.InputFileSystemObjectContent{
											StringContent: `not  content`,
										},
									}}},
							},
						},
					},
				},
				want: apis.ErrMultipleOneOf("spec.inputs.workspaceContents[0].objects[1].path"),
			},
		}, {
			name: "input workspace object type directory but contents not empty",
			tc: testCase{
				taskTest: &v1alpha1.TaskTest{
					ObjectMeta: metav1.ObjectMeta{Name: "taskname"},
					Spec: v1alpha1.TaskTestSpec{
						Inputs: &v1alpha1.TaskTestInputs{
							WorkspaceContents: []v1alpha1.InitialWorkspaceContents{{
								Name: "workspace",
								Objects: []v1alpha1.InputFileSystemObject{{
									Path: "/path/to/object",
									Type: "Directory",
									Content: &v1alpha1.InputFileSystemObjectContent{
										StringContent: "content",
									},
								}},
							}},
						},
					},
				},
				want: &apis.FieldError{
					Message: "must not set the field(s)",
					Paths:   []string{"spec.inputs.workspaceContents[0].objects[0].content"},
					Details: `the field "content" may only be set if the field "type" is set to "TextFile"`,
				},
			},
		}, {
			name: "input workspace type missing",
			tc: testCase{
				taskTest: &v1alpha1.TaskTest{
					ObjectMeta: metav1.ObjectMeta{Name: "taskname"},
					Spec: v1alpha1.TaskTestSpec{
						Inputs: &v1alpha1.TaskTestInputs{
							WorkspaceContents: []v1alpha1.InitialWorkspaceContents{{
								Name: "workspace",
								Objects: []v1alpha1.InputFileSystemObject{{
									Path: "/path/to/object",
								}},
							}},
						},
					},
				},
				want: apis.ErrMissingField("spec.inputs.workspaceContents[0].objects[0].type"),
			},
		}, {
			name: "input workspace object copyFrom but type not directory",
			tc: testCase{
				taskTest: &v1alpha1.TaskTest{
					ObjectMeta: metav1.ObjectMeta{Name: "taskname"},
					Spec: v1alpha1.TaskTestSpec{
						Inputs: &v1alpha1.TaskTestInputs{
							WorkspaceContents: []v1alpha1.InitialWorkspaceContents{{
								Name: "workspace",
								Objects: []v1alpha1.InputFileSystemObject{{
									Path: "/path/to/object",
									Type: "TextFile",
									Content: &v1alpha1.InputFileSystemObjectContent{
										CopyFrom: &v1alpha1.CopyFromRef{
											VolumeName: "test-volume",
											Path:       "/path/to/source",
										},
									},
								}},
							}},
						},
					},
				},
				want: &apis.FieldError{
					Message: "must not set the field(s)",
					Paths:   []string{"spec.inputs.workspaceContents[0].objects[0].content.copyFrom"},
					Details: `the field "copyFrom" may only be set if the field "type" is set to "Directory"`,
				},
			},
		}, {
			name: "input workspace object type field has invalid value",
			tc: testCase{
				taskTest: &v1alpha1.TaskTest{
					ObjectMeta: metav1.ObjectMeta{Name: "taskname"},
					Spec: v1alpha1.TaskTestSpec{
						Inputs: &v1alpha1.TaskTestInputs{
							WorkspaceContents: []v1alpha1.InitialWorkspaceContents{{
								Name: "workspace",
								Objects: []v1alpha1.InputFileSystemObject{{
									Path: "/path/to/object",
									Type: "InvalidFileType",
								}},
							}},
						},
					},
				},
				want: apis.ErrInvalidValue("InvalidFileType", "spec.inputs.workspaceContents[0].objects[0].type"),
			},
		}, {
			name: "input workspace object path contains invalid character",
			tc: testCase{
				taskTest: &v1alpha1.TaskTest{
					ObjectMeta: metav1.ObjectMeta{Name: "taskname"},
					Spec: v1alpha1.TaskTestSpec{
						Inputs: &v1alpha1.TaskTestInputs{
							WorkspaceContents: []v1alpha1.InitialWorkspaceContents{{
								Name: "workspace",
								Objects: []v1alpha1.InputFileSystemObject{{
									Path: "/path/to\000/object",
									Type: "TextFile",
								}},
							}},
						},
					},
				},
				want: apis.ErrInvalidValue("/path/to\000/object", "spec.inputs.workspaceContents[0].objects[0].path", "illegal character \000 detected"),
			},
		}, {
			name: "input workspace object path ends on slash",
			tc: testCase{
				taskTest: &v1alpha1.TaskTest{
					ObjectMeta: metav1.ObjectMeta{Name: "taskname"},
					Spec: v1alpha1.TaskTestSpec{
						Inputs: &v1alpha1.TaskTestInputs{
							WorkspaceContents: []v1alpha1.InitialWorkspaceContents{{
								Name: "workspace",
								Objects: []v1alpha1.InputFileSystemObject{{
									Path: "/path/to/object/",
									Type: "TextFile",
								}},
							}},
						},
					},
				},
				want: apis.ErrInvalidValue("/path/to/object/", "spec.inputs.workspaceContents[0].objects[0].path", "input path may not end on '/'"),
			},
		}, {
			name: "input workspace object path ends on dot",
			tc: testCase{
				taskTest: &v1alpha1.TaskTest{
					ObjectMeta: metav1.ObjectMeta{Name: "taskname"},
					Spec: v1alpha1.TaskTestSpec{
						Inputs: &v1alpha1.TaskTestInputs{
							WorkspaceContents: []v1alpha1.InitialWorkspaceContents{{
								Name: "workspace",
								Objects: []v1alpha1.InputFileSystemObject{{
									Path: "/path/to/object/.",
									Type: "TextFile",
								}},
							}},
						},
					},
				},
				want: apis.ErrInvalidValue("/path/to/object/.", "spec.inputs.workspaceContents[0].objects[0].path", "input path may not end on '.'"),
			},
		}, {
			name: "input workspace object path ends on whitespace",
			tc: testCase{
				taskTest: &v1alpha1.TaskTest{
					ObjectMeta: metav1.ObjectMeta{Name: "taskname"},
					Spec: v1alpha1.TaskTestSpec{
						Inputs: &v1alpha1.TaskTestInputs{
							WorkspaceContents: []v1alpha1.InitialWorkspaceContents{{
								Name: "workspace",
								Objects: []v1alpha1.InputFileSystemObject{{
									Path: "/path/to/object/ ",
									Type: "TextFile",
								}},
							}},
						},
					},
				},
				want: apis.ErrInvalidValue("/path/to/object/ ", "spec.inputs.workspaceContents[0].objects[0].path", "input path may not end on ' '"),
			},
		}, {
			name: "expected file system object type field has invalid value and content not empty in directory type object",
			tc: testCase{
				taskTest: &v1alpha1.TaskTest{
					ObjectMeta: metav1.ObjectMeta{Name: "taskname"},
					Spec: v1alpha1.TaskTestSpec{
						Expects: &v1alpha1.ExpectedOutcomes{
							StepExpectations: []v1alpha1.StepExpectation{{
								Name: "step0",
								FileSystemObjects: []v1alpha1.FileSystemObject{{
									Path: "/path/to/object",
									Type: "TextFile",
								}},
							}, {
								Name: "step1",
								FileSystemObjects: []v1alpha1.FileSystemObject{
									{
										Path: "/path/to/object0",
										Type: "Directory",
									}, {
										Path:    "/path/to/object1",
										Type:    "Directory",
										Content: "content",
									}, {
										Path: "/path/to/object2",
										Type: "InvalidFileType",
									},
								},
							}},
						},
					},
				},
				want: apis.ErrInvalidValue("InvalidFileType", "spec.expected.stepExpectations[1].fileSystemObjects[2].type").Also(apis.ErrDisallowedFields("spec.expected.stepExpectations[1].fileSystemObjects[1].content")),
			},
		}, {
			name: "result name not unique",
			tc: testCase{
				taskTest: &v1alpha1.TaskTest{
					ObjectMeta: metav1.ObjectMeta{
						Name: "tt",
					},
					Spec: v1alpha1.TaskTestSpec{
						TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
						Expects: &v1alpha1.ExpectedOutcomes{
							Results: []v1.TaskResult{
								{
									Name: "result",
								}, {
									Name: "result",
								},
							},
						},
					},
				},
				want: apis.ErrMultipleOneOf("spec.expected.results[1].name"),
			},
		}, {
			name: "expected step file path not unique",
			tc: testCase{
				taskTest: &v1alpha1.TaskTest{
					ObjectMeta: metav1.ObjectMeta{
						Name: "tt",
					},
					Spec: v1alpha1.TaskTestSpec{
						TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
						Expects: &v1alpha1.ExpectedOutcomes{
							StepExpectations: []v1alpha1.StepExpectation{{
								Name: "step",
								FileSystemObjects: []v1alpha1.FileSystemObject{{
									Path: "/path/to/object",
									Type: "AnyObjectType",
								}, {
									Path: "/path/to/object",
									Type: "AnyObjectType",
								}},
							}},
						},
					},
				},
				want: apis.ErrMultipleOneOf("spec.expected.stepExpectations[0].fileSystemObjects[1].path"),
			},
		},
	}
	for _, ts := range tests {
		t.Run(ts.name, func(t *testing.T) {
			ctx := t.Context()
			if ts.tc.wc != nil {
				ctx = ts.tc.wc(ctx)
			}
			err := ts.tc.taskTest.Validate(ctx)
			if d := cmp.Diff(ts.tc.want.Error(), err.Error()); d != "" {
				t.Error(diff.PrintWantGot(d))
			}
		})
	}
}

func TestTaskTest_Valid(t *testing.T) {
	for _, c := range []struct {
		name string
		run  *v1alpha1.TaskTest
	}{
		{
			name: "no inputs, no expectations",
			run: &v1alpha1.TaskTest{
				ObjectMeta: metav1.ObjectMeta{
					Name: "tt",
				},
				Spec: v1alpha1.TaskTestSpec{
					TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
				},
			},
		}, {
			name: "full inputs, no expectations",
			run: &v1alpha1.TaskTest{
				ObjectMeta: metav1.ObjectMeta{
					Name: "tt",
				},
				Spec: v1alpha1.TaskTestSpec{
					TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
					Inputs: &v1alpha1.TaskTestInputs{
						Params: v1.Params{
							{
								Name: "param0",
								Value: v1.ParamValue{
									Type:      "type",
									StringVal: "value",
									ArrayVal:  []string{"value"},
									ObjectVal: map[string]string{"key": "value"},
								},
							}, {
								Name: "param1",
								Value: v1.ParamValue{
									Type:      "type",
									StringVal: "value",
									ArrayVal:  []string{"value"},
									ObjectVal: map[string]string{"key": "value"},
								},
							},
						},
						Env: []corev1.EnvVar{{
							Name:  "name0",
							Value: "value",
						}, {
							Name:  "name1",
							Value: "value",
						}},
						StepEnvs: []v1alpha1.StepEnv{{Env: []corev1.EnvVar{
							{
								Name: "name0",
								ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
									LocalObjectReference: corev1.LocalObjectReference{Name: "name"},
									Key:                  "key"},
								},
							}, {
								Name:  "name1",
								Value: "value",
							}}}},
						WorkspaceContents: []v1alpha1.InitialWorkspaceContents{{
							Name: "name0",
							Objects: []v1alpha1.InputFileSystemObject{
								{
									Path: "/object/path0",
									Type: "TextFile",
									Content: &v1alpha1.InputFileSystemObjectContent{
										StringContent: `
							content
							`,
									},
								}, {
									Path: "/object/path1",
									Type: "TextFile",
									Content: &v1alpha1.InputFileSystemObjectContent{
										StringContent: `not  content`,
									},
								}}},
							{
								Name: "name1",
								Objects: []v1alpha1.InputFileSystemObject{{
									Path: "/object/path0",
									Type: "TextFile",
									Content: &v1alpha1.InputFileSystemObjectContent{
										StringContent: `
							content
							`,
									},
								}}},
						},
					},
				},
			},
		}, {
			name: "no inputs, full expectations",
			run: &v1alpha1.TaskTest{
				ObjectMeta: metav1.ObjectMeta{
					Name: "tt",
				},
				Spec: v1alpha1.TaskTestSpec{
					TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
					Expects: &v1alpha1.ExpectedOutcomes{
						Env: []corev1.EnvVar{{
							Name:  "name0",
							Value: "value",
						}, {
							Name:  "name1",
							Value: "value",
						}},
						StepExpectations: []v1alpha1.StepExpectation{{
							Name: "step",
							Env: []corev1.EnvVar{{Name: "name0",
								ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
									LocalObjectReference: corev1.LocalObjectReference{Name: "name"},
									Key:                  "key"},
								}}, {
								Name:  "name1",
								Value: "value",
							}},
							FileSystemObjects: []v1alpha1.FileSystemObject{{
								Path: "/object/path0",
								Type: "TextFile",
								Content: `
	content
	`,
							}, {
								Path:    "/object/path1",
								Type:    "TextFile",
								Content: `not content`,
							}}}},
						Results: []v1.TaskResult{
							{
								Name:        "name0",
								Type:        "type",
								Properties:  map[string]v1.PropertySpec{"key": {Type: "type"}},
								Description: "description",
								Value: &v1.ResultValue{
									Type:      "type",
									StringVal: "value",
									ArrayVal:  []string{"value"},
									ObjectVal: map[string]string{"key": "value"},
								},
							},
							{
								Name:        "name1",
								Type:        "type",
								Properties:  map[string]v1.PropertySpec{"key": {Type: "type"}},
								Description: "description",
								Value: &v1.ResultValue{
									Type:      "type",
									StringVal: "value",
									ArrayVal:  []string{"value"},
									ObjectVal: map[string]string{"key": "value"},
								},
							},
						},
						SuccessStatus: ptr.To(true),
						SuccessReason: ptr.To(v1.TaskRunReason("blah")),
					},
				},
			},
		}, {
			name: "full inputs, full expectations",
			run: &v1alpha1.TaskTest{
				ObjectMeta: metav1.ObjectMeta{
					Name: "tt",
				},
				Spec: v1alpha1.TaskTestSpec{
					TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
					Inputs: &v1alpha1.TaskTestInputs{
						Params: v1.Params{
							{
								Name: "param",
								Value: v1.ParamValue{
									Type:      "type",
									StringVal: "value",
									ArrayVal:  []string{"value"},
									ObjectVal: map[string]string{"key": "value"},
								},
							},
						},
						Env: []corev1.EnvVar{{
							Name:  "name",
							Value: "value",
						}},
						StepEnvs: []v1alpha1.StepEnv{{Env: []corev1.EnvVar{{Name: "name",
							ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "name"},
								Key:                  "key"},
							}}}}},
						WorkspaceContents: []v1alpha1.InitialWorkspaceContents{{
							Name: "name",
							Objects: []v1alpha1.InputFileSystemObject{{
								Path: "/path",
								Type: "Directory",
							}}},
						},
					},
					Expects: &v1alpha1.ExpectedOutcomes{
						Env: []corev1.EnvVar{{
							Name:  "name",
							Value: "value",
						}},
						StepExpectations: []v1alpha1.StepExpectation{{
							Name: "step",
							Env: []corev1.EnvVar{{Name: "name",
								ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
									LocalObjectReference: corev1.LocalObjectReference{Name: "name"},
									Key:                  "key"},
								}}},
							FileSystemObjects: []v1alpha1.FileSystemObject{{
								Path: "/object/path",
								Type: "Directory",
							}},
						}},
						Results: []v1.TaskResult{
							{
								Name:        "name",
								Type:        "type",
								Properties:  map[string]v1.PropertySpec{"key": {Type: "type"}},
								Description: "description",
								Value: &v1.ResultValue{
									Type:      "type",
									StringVal: "value",
									ArrayVal:  []string{"value"},
									ObjectVal: map[string]string{"key": "value"},
								},
							},
						},
						SuccessStatus: ptr.To(true),
						SuccessReason: ptr.To(v1.TaskRunReason("blah")),
					},
				},
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := c.run.Validate(t.Context()); err != nil {
				t.Fatalf("validating valid TaskTest: %v", err)
			}
		})
	}
}
