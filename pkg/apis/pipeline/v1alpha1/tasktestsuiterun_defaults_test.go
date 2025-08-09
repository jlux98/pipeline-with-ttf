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
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1alpha1"
	"github.com/tektoncd/pipeline/test/diff"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestTaskTestSuiteRunSpec_SetDefaults(t *testing.T) {
	cases := []struct {
		desc string
		tsr  *v1alpha1.TaskTestSuiteRunSpec
		want *v1alpha1.TaskTestSuiteRunSpec
	}{{
		desc: "timeout is empty",
		tsr: &v1alpha1.TaskTestSuiteRunSpec{
			TaskTestSuiteRef: &v1alpha1.TaskTestSuiteRef{Name: "suite"},
			Timeout:          nil,
		},
		want: &v1alpha1.TaskTestSuiteRunSpec{
			TaskTestSuiteRef: &v1alpha1.TaskTestSuiteRef{Name: "suite"},
			Timeout:          &metav1.Duration{Duration: 60 * time.Minute},
		},
	}}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := t.Context()
			tc.tsr.SetDefaults(ctx)

			if d := cmp.Diff(tc.want, tc.tsr); d != "" {
				t.Errorf("Mismatch of TaskRunSpec: %s", diff.PrintWantGot(d))
			}
		})
	}
}
