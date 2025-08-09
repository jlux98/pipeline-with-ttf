/*
Copyright 2019 The Tekton Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/tektoncd/pipeline/pkg/apis/config"
	cfgtesting "github.com/tektoncd/pipeline/pkg/apis/config/testing"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1alpha1"
	"github.com/tektoncd/pipeline/test/diff"
	corev1 "k8s.io/api/core/v1"
	corev1resources "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"knative.dev/pkg/apis"
)

// func TestTaskTestRun_Invalidate(t *testing.T) {
// 	tests := []struct {
// 		name        string
// 		taskTestRun *v1alpha1.TaskTestRun
// 		want        *apis.FieldError
// 		wc          func(context.Context) context.Context
// 	}{{}}
// 	for _, ts := range tests {
// 		t.Run(ts.name, func(t *testing.T) {
// 			ctx := t.Context()
// 			if ts.wc != nil {
// 				ctx = ts.wc(ctx)
// 			}
// 			err := ts.taskTestRun.Validate(ctx)
// 			if d := cmp.Diff(ts.want.Error(), err.Error()); d != "" {
// 				t.Error(diff.PrintWantGot(d))
// 			}
// 		})
// 	}
// }

func TestTaskTestRun_Validate(t *testing.T) {
	tests := []struct {
		name        string
		taskTestRun *v1alpha1.TaskTestRun
		wc          func(context.Context) context.Context
	}{{
		name: "valid run with ref",
		taskTestRun: &v1alpha1.TaskTestRun{
			ObjectMeta: metav1.ObjectMeta{Name: "run"},
			Spec: v1alpha1.TaskTestRunSpec{
				TaskTestRef: &v1alpha1.TaskTestRef{Name: "test"},
				Workspaces: []v1.WorkspaceBinding{{
					Name: "workspace",
					VolumeClaimTemplate: &corev1.PersistentVolumeClaim{
						Spec: corev1.PersistentVolumeClaimSpec{
							AccessModes: []corev1.PersistentVolumeAccessMode{"ReadWriteOnce"},
						},
					},
				}},
				Timeout:             &metav1.Duration{Duration: 500 * time.Millisecond},
				Retries:             1,
				AllTriesMustSucceed: ptr.To(false),
				ServiceAccountName:  "account",
				ComputeResources: &corev1.ResourceRequirements{
					Limits:   corev1.ResourceList{"cpu": corev1resources.Quantity{Format: "500m"}},
					Requests: corev1.ResourceList{"cpu": corev1resources.Quantity{Format: "500m"}},
				},
			},
		},
	}, {
		name: "valid run with task test spec",
		taskTestRun: &v1alpha1.TaskTestRun{
			ObjectMeta: metav1.ObjectMeta{Name: "run"},
			Spec: v1alpha1.TaskTestRunSpec{
				TaskTestSpec: &v1alpha1.TaskTestSpec{TaskRef: &v1.TaskRef{Name: "task"}},
				Workspaces: []v1.WorkspaceBinding{{
					Name: "workspace",
					VolumeClaimTemplate: &corev1.PersistentVolumeClaim{
						Spec: corev1.PersistentVolumeClaimSpec{
							AccessModes: []corev1.PersistentVolumeAccessMode{"ReadWriteOnce"},
						},
					},
				}},
				Timeout:             &metav1.Duration{Duration: 500 * time.Millisecond},
				Retries:             1,
				AllTriesMustSucceed: ptr.To(false),
				ServiceAccountName:  "account",
				ComputeResources: &corev1.ResourceRequirements{
					Limits:   corev1.ResourceList{"cpu": corev1resources.Quantity{Format: "500m"}},
					Requests: corev1.ResourceList{"cpu": corev1resources.Quantity{Format: "500m"}},
				},
			},
		},
	}}
	for _, ts := range tests {
		t.Run(ts.name, func(t *testing.T) {
			ctx := t.Context()
			if ts.wc != nil {
				ctx = ts.wc(ctx)
			}
			if err := ts.taskTestRun.Validate(ctx); err != nil {
				t.Errorf("TaskTestRun.Validate() error = %v", err)
			}
		})
	}
}

func TestTaskTestRun_Workspaces_Invalid(t *testing.T) {
	tests := []struct {
		name    string
		tr      *v1alpha1.TaskTestRun
		wantErr *apis.FieldError
	}{{
		name: "make sure WorkspaceBinding validation invoked",
		tr: &v1alpha1.TaskTestRun{
			ObjectMeta: metav1.ObjectMeta{Name: "taskname"},
			Spec: v1alpha1.TaskTestRunSpec{
				TaskTestRef: &v1alpha1.TaskTestRef{Name: "task"},
				Workspaces: []v1.WorkspaceBinding{{
					Name:                  "workspace",
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{},
				}},
			},
		},
		wantErr: apis.ErrMissingField("spec.workspaces[0].persistentvolumeclaim.claimname"),
	}, {
		name: "bind same workspace twice",
		tr: &v1alpha1.TaskTestRun{
			ObjectMeta: metav1.ObjectMeta{Name: "taskname"},
			Spec: v1alpha1.TaskTestRunSpec{
				TaskTestRef: &v1alpha1.TaskTestRef{Name: "task"},
				Workspaces: []v1.WorkspaceBinding{{
					Name:     "workspace",
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				}, {
					Name:     "workspace",
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				}},
			},
		},
		wantErr: apis.ErrMultipleOneOf("spec.workspaces[1].name"),
	}}
	for _, ts := range tests {
		t.Run(ts.name, func(t *testing.T) {
			err := ts.tr.Validate(t.Context())
			if err == nil {
				t.Errorf("Expected error for invalid TaskTestRun but got none")
			}
			if d := cmp.Diff(ts.wantErr.Error(), err.Error()); d != "" {
				t.Error(diff.PrintWantGot(d))
			}
		})
	}
}

func EnableForbiddenEnv(ctx context.Context) context.Context {
	ctx = cfgtesting.EnableAlphaAPIFields(ctx)
	c := config.FromContext(ctx)
	c.Defaults.DefaultForbiddenEnv = []string{"TEST_ENV"}
	return config.ToContext(ctx, c)
}

func TestTaskTestRun_Invalidate(t *testing.T) {
	tests := []struct {
		name    string
		ttr     v1alpha1.TaskTestRun
		wantErr *apis.FieldError
		wc      func(context.Context) context.Context
	}{{
		name:    "invalid tasktestspec",
		ttr:     v1alpha1.TaskTestRun{},
		wantErr: apis.ErrGeneric(`invalid resource name "": must be a valid DNS label`, "metadata.name").Also(apis.ErrMissingOneOf("spec.taskTestRef", "spec.taskTestSpec")),
	}, {
		name: "taskTestRef and taskTestSpec both set",
		ttr: v1alpha1.TaskTestRun{
			ObjectMeta: metav1.ObjectMeta{Name: "run"},
			Spec: v1alpha1.TaskTestRunSpec{
				TaskTestRef: &v1alpha1.TaskTestRef{Name: "test"},
				TaskTestSpec: &v1alpha1.TaskTestSpec{
					TaskRef: &v1.TaskRef{Name: "task"},
				},
			},
		},
		wantErr: apis.ErrMultipleOneOf("spec.taskTestRef", "spec.taskTestSpec"),
	}, {
		name: "neither taskTestRef nor taskTestSpec set",
		ttr: v1alpha1.TaskTestRun{
			ObjectMeta: metav1.ObjectMeta{Name: "run"},
			Spec:       v1alpha1.TaskTestRunSpec{},
		},
		wantErr: apis.ErrMissingOneOf("spec.taskTestRef", "spec.taskTestSpec"),
	}, {
		name: "negative retries",
		ttr: v1alpha1.TaskTestRun{
			ObjectMeta: metav1.ObjectMeta{Name: "run"},
			Spec: v1alpha1.TaskTestRunSpec{
				TaskTestRef: &v1alpha1.TaskTestRef{Name: "test"},
				Retries:     -5,
			},
		},
		wantErr: apis.ErrInvalidValue(-5, "spec.retries", "retries must be set to a value >= 0"),
	}, {
		name: "negative timeout",
		ttr: v1alpha1.TaskTestRun{
			ObjectMeta: metav1.ObjectMeta{Name: "run"},
			Spec: v1alpha1.TaskTestRunSpec{
				TaskTestRef: &v1alpha1.TaskTestRef{Name: "test"},
				Timeout:     &metav1.Duration{Duration: -48 * time.Hour},
			},
		},
		wantErr: apis.ErrInvalidValue("-48h0m0s should be >= 0", "spec.timeout"),
	}}

	for _, ts := range tests {
		t.Run(ts.name, func(t *testing.T) {
			ctx := t.Context()
			if ts.wc != nil {
				ctx = ts.wc(ctx)
			}
			err := ts.ttr.Validate(ctx)
			if d := cmp.Diff(ts.wantErr.Error(), err.Error()); d != "" {
				t.Error(diff.PrintWantGot(d))
			}
		})
	}
}

// func TestTaskTestRunSpec_Validate(t *testing.T) {
// 	tests := []struct {
// 		name string
// 		spec v1alpha1.TaskTestRunSpec
// 		wc   func(context.Context) context.Context
// 	}{{}}

// 	for _, ts := range tests {
// 		t.Run(ts.name, func(t *testing.T) {
// 			ctx := t.Context()
// 			if ts.wc != nil {
// 				ctx = ts.wc(ctx)
// 			}
// 			if err := ts.spec.Validate(ctx); err != nil {
// 				t.Error(err)
// 			}
// 		})
// 	}
// }
