/*
Copyright 2023 The Tekton Authors

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
	"testing"

	"github.com/google/go-cmp/cmp"
	cfgtesting "github.com/tektoncd/pipeline/pkg/apis/config/testing"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1alpha1"
	"github.com/tektoncd/pipeline/test/diff"
)

func TestTask_SetDefaults(t *testing.T) {
	tests := []struct {
		name string
		in   *v1alpha1.TaskTest
		want *v1alpha1.TaskTest
	}{{
		name: "expected file system object type set to AnyObjectType",
		in: &v1alpha1.TaskTest{
			Spec: v1alpha1.TaskTestSpec{
				Expected: &v1alpha1.ExpectedOutcomes{
					FileSystemContents: []v1alpha1.ExpectedStepFileSystemContent{{
						StepName: "MyStep",
						Objects: []v1alpha1.FileSystemObject{{
							Path: "/my/path",
							Type: "",
						}},
					}},
				},
			},
		},
		want: &v1alpha1.TaskTest{
			Spec: v1alpha1.TaskTestSpec{
				Expected: &v1alpha1.ExpectedOutcomes{
					FileSystemContents: []v1alpha1.ExpectedStepFileSystemContent{{
						StepName: "MyStep",
						Objects: []v1alpha1.FileSystemObject{{
							Path: "/my/path",
							Type: "AnyObjectType",
						}},
					}},
				},
			},
		},
	}, {
		name: "expected result type set to string",
		in: &v1alpha1.TaskTest{
			Spec: v1alpha1.TaskTestSpec{
				Expected: &v1alpha1.ExpectedOutcomes{
					Results: []v1.TaskResult{{
						Name:  "name",
						Value: &v1.ResultValue{StringVal: "value"},
					}},
				},
			},
		},
		want: &v1alpha1.TaskTest{
			Spec: v1alpha1.TaskTestSpec{
				Expected: &v1alpha1.ExpectedOutcomes{
					Results: []v1.TaskResult{{
						Name: "name",
						Type: "string",
						Value: &v1.ResultValue{
							StringVal: "value",
							Type:      "string"},
					}},
				},
			},
		},
	}, {
		name: "non-default expected file system object type not overwritten",
		in: &v1alpha1.TaskTest{
			Spec: v1alpha1.TaskTestSpec{
				Expected: &v1alpha1.ExpectedOutcomes{
					FileSystemContents: []v1alpha1.ExpectedStepFileSystemContent{{
						StepName: "MyStep",
						Objects: []v1alpha1.FileSystemObject{{
							Path: "/my/path",
							Type: "Directory",
						}},
					}},
				},
			},
		},
		want: &v1alpha1.TaskTest{
			Spec: v1alpha1.TaskTestSpec{
				Expected: &v1alpha1.ExpectedOutcomes{
					FileSystemContents: []v1alpha1.ExpectedStepFileSystemContent{{
						StepName: "MyStep",
						Objects: []v1alpha1.FileSystemObject{{
							Path: "/my/path",
							Type: "Directory",
						}},
					}},
				},
			},
		},
	}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := cfgtesting.SetDefaults(t.Context(), t, map[string]string{
				"default-resolver-type": "git",
			})
			got := tc.in
			got.SetDefaults(ctx)
			if d := cmp.Diff(tc.want, got); d != "" {
				t.Errorf("SetDefaults %s", diff.PrintWantGot(d))
			}
		})
	}
}
