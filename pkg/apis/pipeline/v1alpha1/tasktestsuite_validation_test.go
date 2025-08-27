package v1alpha1_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1alpha1"
	"github.com/tektoncd/pipeline/test/diff"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"
)

func TestTaskTestSuite_Invalidate(t *testing.T) {
	tests := []struct {
		name string
		tts  v1alpha1.TaskTestSuite
		want *apis.FieldError
	}{
		{
			name: "invalid manifest",
			tts:  v1alpha1.TaskTestSuite{},
			want: apis.ErrGeneric(`invalid resource name "": must be a valid DNS label`, "metadata.name").Also(apis.ErrGeneric("expected at least one, got none", "spec.taskTests")),
		}, {
			name: "combine inline and ref tests with the same name",
			tts: v1alpha1.TaskTestSuite{
				ObjectMeta: metav1.ObjectMeta{Name: "suite"},
				Spec: v1alpha1.TaskTestSuiteSpec{
					TaskTests: []v1alpha1.SuiteTest{
						{
							Name: "test",
							TaskTestRef: &v1alpha1.TaskTestRef{
								Name: "test0",
							},
						}, {
							Name: "test",
							TaskTestSpec: &v1alpha1.TaskTestSpec{
								TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
								Inputs:  &v1alpha1.TaskTestInputs{},
								Expects: &v1alpha1.ExpectedOutcomes{},
							},
						},
					},
				},
			},
			want: apis.ErrMultipleOneOf("spec.taskTests[1].name"),
		}, {
			name: "list of tests may not be empty",
			tts: v1alpha1.TaskTestSuite{
				ObjectMeta: metav1.ObjectMeta{Name: "suite"},
				Spec: v1alpha1.TaskTestSuiteSpec{
					TaskTests: []v1alpha1.SuiteTest{},
				},
			},
			want: apis.ErrGeneric("expected at least one, got none", "spec.taskTests"),
		}, {
			name: "taskTestRef and taskTestSpec are both set",
			tts: v1alpha1.TaskTestSuite{
				ObjectMeta: metav1.ObjectMeta{Name: "suite"},
				Spec: v1alpha1.TaskTestSuiteSpec{
					TaskTests: []v1alpha1.SuiteTest{{
						Name:        "test",
						TaskTestRef: &v1alpha1.TaskTestRef{Name: "test0"},
						TaskTestSpec: &v1alpha1.TaskTestSpec{
							TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
						},
					}},
				},
			},
			want: apis.ErrMultipleOneOf("spec.taskTests[0].taskTestRef", "spec.taskTests[0].taskTestSpec"),
		}, {
			name: "neither taskTestRef nor taskTestSpec is set",
			tts: v1alpha1.TaskTestSuite{
				ObjectMeta: metav1.ObjectMeta{Name: "suite"},
				Spec: v1alpha1.TaskTestSuiteSpec{
					TaskTests: []v1alpha1.SuiteTest{{
						Name: "test",
					}},
				},
			},
			want: apis.ErrMissingOneOf("spec.taskTests[0].taskTestRef", "spec.taskTests[0].taskTestSpec"),
		}, {
			name: "negative retries",
			tts: v1alpha1.TaskTestSuite{
				ObjectMeta: metav1.ObjectMeta{Name: "suite"},
				Spec: v1alpha1.TaskTestSuiteSpec{
					TaskTests: []v1alpha1.SuiteTest{{
						Name:        "test",
						TaskTestRef: &v1alpha1.TaskTestRef{Name: "test"},
						Retries:     -5,
					}},
				},
			},
			want: apis.ErrInvalidValue(-5, "spec.taskTests[0].retries", "retries must be set to a value >= 0"),
		}, {
			name: "negative timeout",
			tts: v1alpha1.TaskTestSuite{
				ObjectMeta: metav1.ObjectMeta{Name: "suite"},
				Spec: v1alpha1.TaskTestSuiteSpec{
					TaskTests: []v1alpha1.SuiteTest{{
						Name:        "test",
						TaskTestRef: &v1alpha1.TaskTestRef{Name: "test"},
						Timeout:     &metav1.Duration{Duration: -48 * time.Hour},
					}},
				},
			},
			want: apis.ErrInvalidValue("-48h0m0s should be >= 0", "spec.taskTests[0].timeout"),
		}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.tts.Validate(t.Context())
			if err == nil {
				t.Error("TaskTestSuite.Validate() did not return error for invalid pipeline")
			}
			if d := cmp.Diff(tt.want.Error(), err.Error(), cmpopts.IgnoreUnexported(apis.FieldError{})); d != "" {
				t.Errorf("TaskTestSuite.Validate() errors diff %s", diff.PrintWantGot(d))
			}
		})
	}
}
func TestTaskTestSuite_Validate(t *testing.T) {
	tests := []struct {
		name string
		tts  v1alpha1.TaskTestSuite
	}{{
		name: "combine inline and ref tests with different names",
		tts: v1alpha1.TaskTestSuite{
			ObjectMeta: metav1.ObjectMeta{Name: "suite"},
			Spec: v1alpha1.TaskTestSuiteSpec{
				TaskTests: []v1alpha1.SuiteTest{
					{
						Name: "test0",
						TaskTestRef: &v1alpha1.TaskTestRef{
							Name: "test0",
						},
					}, {
						Name: "test1",
						TaskTestSpec: &v1alpha1.TaskTestSpec{
							TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
							Inputs:  &v1alpha1.TaskTestInputs{},
							Expects: &v1alpha1.ExpectedOutcomes{},
						},
					},
				},
			},
		},
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.tts.Validate(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
}
