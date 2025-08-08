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
			name: "input workspace object type directory but contents not empty",
			tc: testCase{
				taskTest: &v1alpha1.TaskTest{
					ObjectMeta: metav1.ObjectMeta{Name: "taskname"},
					Spec: v1alpha1.TaskTestSpec{
						Inputs: v1alpha1.TaskTestInputs{
							WorkspaceContents: []v1alpha1.InitialWorkspaceContents{{
								Name: "workspace",
								Objects: []v1alpha1.InputFileSystemObject{{
									Path:    "/path/to/object",
									Type:    "Directory",
									Content: "content",
								}},
							}},
						},
					},
				},
				want: apis.ErrDisallowedFields("spec.inputs.workspaceContents[0].objects[0].content"),
			},
		}, {
			name: "input workspace object type field has invalid value",
			tc: testCase{
				taskTest: &v1alpha1.TaskTest{
					ObjectMeta: metav1.ObjectMeta{Name: "taskname"},
					Spec: v1alpha1.TaskTestSpec{
						Inputs: v1alpha1.TaskTestInputs{
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
						Inputs: v1alpha1.TaskTestInputs{
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
						Inputs: v1alpha1.TaskTestInputs{
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
						Inputs: v1alpha1.TaskTestInputs{
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
						Inputs: v1alpha1.TaskTestInputs{
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
						Expected: v1alpha1.ExpectedOutcomes{
							FileSystemContents: []v1alpha1.ExpectedStepFileSystemContent{
								{
									StepName: "step0",
									Objects: []v1alpha1.FileSystemObject{{
										Path: "/path/to/object",
										Type: "TextFile",
									}},
								}, {

									StepName: "step1",
									Objects: []v1alpha1.FileSystemObject{
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
				want: apis.ErrInvalidValue("InvalidFileType", "spec.expected.fileSystemContents[1].objects[2].type").Also(apis.ErrDisallowedFields("spec.expected.fileSystemContents[1].objects[1].content")),
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
					TaskRef: &v1.TaskRef{Name: "task"},
				},
			},
		}, {
			name: "full inputs, no expectations",
			run: &v1alpha1.TaskTest{
				ObjectMeta: metav1.ObjectMeta{
					Name: "tt",
				},
				Spec: v1alpha1.TaskTestSpec{
					TaskRef: &v1.TaskRef{Name: "task"},
					Inputs: v1alpha1.TaskTestInputs{
						Params: v1.Params{
							{
								Name: "myparam",
								Value: v1.ParamValue{
									Type:      "mytype",
									StringVal: "myvalue",
									ArrayVal:  []string{"myvalue"},
									ObjectVal: map[string]string{"mykey": "myvalue"},
								},
							},
						},
						Env: []corev1.EnvVar{{
							Name:  "myname",
							Value: "myvalue",
						}},
						StepEnvs: []v1alpha1.StepEnv{{Env: []corev1.EnvVar{{Name: "myname",
							ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "myname"},
								Key:                  "mykey"},
							}}}}},
						WorkspaceContents: []v1alpha1.InitialWorkspaceContents{{
							Name: "myname",
							Objects: []v1alpha1.InputFileSystemObject{{
								Path: "/my/path",
								Type: "TextFile",
								Content: `my
							content
							`,
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
					TaskRef: &v1.TaskRef{Name: "task"},
					Expected: v1alpha1.ExpectedOutcomes{
						Env: []corev1.EnvVar{{
							Name:  "myname",
							Value: "myvalue",
						}},
						StepEnvs: []v1alpha1.StepEnv{{Env: []corev1.EnvVar{{Name: "myname",
							ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "myname"},
								Key:                  "mykey"},
							}}}}},
						FileSystemContents: []v1alpha1.ExpectedStepFileSystemContent{{
							StepName: "mystep",
							Objects: []v1alpha1.FileSystemObject{{
								Path: "/my/path",
								Type: "TextFile",
								Content: `my
							content
							`,
							}}}},
						Results: []v1.TaskResult{
							{
								Name:        "myname",
								Type:        "mytype",
								Properties:  map[string]v1.PropertySpec{"mykey": {Type: "mytype"}},
								Description: "description",
								Value: &v1.ResultValue{
									Type:      "mytype",
									StringVal: "myvalue",
									ArrayVal:  []string{"myvalue"},
									ObjectVal: map[string]string{"mykey": "myvalue"},
								},
							},
						},
						SuccessStatus: true,
						SuccessReason: "blah",
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
					TaskRef: &v1.TaskRef{Name: "task"},
					Inputs: v1alpha1.TaskTestInputs{
						Params: v1.Params{
							{
								Name: "myparam",
								Value: v1.ParamValue{
									Type:      "mytype",
									StringVal: "myvalue",
									ArrayVal:  []string{"myvalue"},
									ObjectVal: map[string]string{"mykey": "myvalue"},
								},
							},
						},
						Env: []corev1.EnvVar{{
							Name:  "myname",
							Value: "myvalue",
						}},
						StepEnvs: []v1alpha1.StepEnv{{Env: []corev1.EnvVar{{Name: "myname",
							ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "myname"},
								Key:                  "mykey"},
							}}}}},
						WorkspaceContents: []v1alpha1.InitialWorkspaceContents{{
							Name: "myname",
							Objects: []v1alpha1.InputFileSystemObject{{
								Path: "my/path",
								Type: "Directory",
							}}},
						},
					},
					Expected: v1alpha1.ExpectedOutcomes{
						Env: []corev1.EnvVar{{
							Name:  "myname",
							Value: "myvalue",
						}},
						StepEnvs: []v1alpha1.StepEnv{{Env: []corev1.EnvVar{{Name: "myname",
							ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "myname"},
								Key:                  "mykey"},
							}}}}},
						FileSystemContents: []v1alpha1.ExpectedStepFileSystemContent{{
							StepName: "mystep",
							Objects: []v1alpha1.FileSystemObject{{
								Path: "/my/path",
								Type: "Directory",
							}}}},
						Results: []v1.TaskResult{
							{
								Name:        "myname",
								Type:        "mytype",
								Properties:  map[string]v1.PropertySpec{"mykey": {Type: "mytype"}},
								Description: "description",
								Value: &v1.ResultValue{
									Type:      "mytype",
									StringVal: "myvalue",
									ArrayVal:  []string{"myvalue"},
									ObjectVal: map[string]string{"mykey": "myvalue"},
								},
							},
						},
						SuccessStatus: true,
						SuccessReason: "blah",
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
