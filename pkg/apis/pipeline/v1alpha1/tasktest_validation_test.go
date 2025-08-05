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

func TestTaskTest_Invalid(t *testing.T) {
	tests := []struct {
		name     string
		taskTest *v1alpha1.TaskTest
		want     *apis.FieldError
		wc       func(context.Context) context.Context
	}{{
		name:     "invalid taskspec",
		taskTest: &v1alpha1.TaskTest{},
		want:     apis.ErrGeneric(`invalid resource name "": must be a valid DNS label`, "metadata.name"),
		// }, {
		// 	name: "missing spec",
		// 	taskTest: &v1alpha1.TaskTest{
		// 		ObjectMeta: metav1.ObjectMeta{Name: "tt"},
		// 	},
		// 	want: apis.ErrMissingField("spec"),
	}}
	for _, ts := range tests {
		t.Run(ts.name, func(t *testing.T) {
			ctx := t.Context()
			if ts.wc != nil {
				ctx = ts.wc(ctx)
			}
			err := ts.taskTest.Validate(ctx)
			if d := cmp.Diff(ts.want.Error(), err.Error()); d != "" {
				t.Error(diff.PrintWantGot(d))
			}
		})
	}
}

func TestTaskTest_Valid(t *testing.T) {
	for _, c := range []struct {
		name string
		run  *v1alpha1.TaskTest
	}{{
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
					WorkspaceContents: []v1alpha1.WorkspaceContentDeclaration{{
						Name: "myname",
						Objects: []v1alpha1.FileSystemObject{{
							Path: "/my/path",
							Type: "mytype",
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
					FileSystemContents: []v1alpha1.StepFileSystemContent{{
						StepName: "mystep",
						Objects: []v1alpha1.FileSystemObject{{
							Path: "/my/path",
							Type: "mytype",
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
					WorkspaceContents: []v1alpha1.WorkspaceContentDeclaration{{
						Name: "myname",
						Objects: []v1alpha1.FileSystemObject{{
							Path: "/my/path",
							Type: "mytype",
							Content: `my
							content
							`,
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
					FileSystemContents: []v1alpha1.StepFileSystemContent{{
						StepName: "mystep",
						Objects: []v1alpha1.FileSystemObject{{
							Path: "/my/path",
							Type: "mytype",
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
	}} {
		t.Run(c.name, func(t *testing.T) {
			if err := c.run.Validate(t.Context()); err != nil {
				t.Fatalf("validating valid TaskTest: %v", err)
			}
		})
	}
}
