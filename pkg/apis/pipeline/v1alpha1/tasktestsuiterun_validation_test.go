package v1alpha1_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1alpha1"
	"github.com/tektoncd/pipeline/test/diff"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"
)

func TestTaskTestSuiteRun_Invalidate(t *testing.T) {
	tests := []struct {
		name string
		tsr  v1alpha1.TaskTestSuiteRun
		want *apis.FieldError
	}{{
		name: "invalid manifest",
		tsr:  v1alpha1.TaskTestSuiteRun{},
		want: apis.ErrMissingOneOf("spec.taskTestSuiteRef", "spec.taskTestSuiteSpec").Also(
			apis.ErrGeneric(`invalid resource name "": must be a valid DNS label`, "metadata.name")).Also(apis.ErrInvalidValue("''", "spec.executionMode", "executionMode must be set to either 'Parallel' or 'Sequential'")),
	}, {
		name: "negative timeout in inline TaskTestSuite",
		tsr: v1alpha1.TaskTestSuiteRun{
			ObjectMeta: v1.ObjectMeta{Name: "suite"},
			Spec: v1alpha1.TaskTestSuiteRunSpec{
				ExecutionMode: "Sequential",
				TaskTestSuiteSpec: &v1alpha1.TaskTestSuiteSpec{
					TaskTests: []v1alpha1.SuiteTest{{
						Name:        "test",
						TaskTestRef: &v1alpha1.TaskTestRef{Name: "test"},
						Timeout:     &metav1.Duration{Duration: -48 * time.Hour},
					}},
				},
			},
		},
		want: apis.ErrInvalidValue("-48h0m0s should be >= 0", "spec.taskTestSuiteSpec.taskTests[0].timeout"),
	}, {
		name: "negative timeout in run",
		tsr: v1alpha1.TaskTestSuiteRun{
			ObjectMeta: v1.ObjectMeta{Name: "suite"},
			Spec: v1alpha1.TaskTestSuiteRunSpec{
				ExecutionMode:    "Sequential",
				TaskTestSuiteRef: &v1alpha1.TaskTestSuiteRef{Name: "suite"},
				Timeout:          &metav1.Duration{Duration: -48 * time.Hour},
			},
		},
		want: apis.ErrInvalidValue("-48h0m0s should be >= 0", "spec.timeout"),
	}, {
		name: "task test suite ref and spec both set",
		tsr: v1alpha1.TaskTestSuiteRun{
			ObjectMeta: v1.ObjectMeta{Name: "run"},
			Spec: v1alpha1.TaskTestSuiteRunSpec{
				TaskTestSuiteRef: &v1alpha1.TaskTestSuiteRef{Name: "suite"},
				TaskTestSuiteSpec: &v1alpha1.TaskTestSuiteSpec{
					TaskTests: []v1alpha1.SuiteTest{{
						Name:        "test0",
						TaskTestRef: &v1alpha1.TaskTestRef{"task0"},
					}, {
						Name:        "test1",
						TaskTestRef: &v1alpha1.TaskTestRef{"task0"},
					}},
				},
				ExecutionMode: "Sequential",
			},
		},
		want: apis.ErrMultipleOneOf("spec.taskTestSuiteRef", "spec.taskTestSuiteSpec"),
	}, {
		name: "neither task test suite ref nor spec set",
		tsr: v1alpha1.TaskTestSuiteRun{
			ObjectMeta: v1.ObjectMeta{Name: "run"},
			Spec:       v1alpha1.TaskTestSuiteRunSpec{ExecutionMode: "Sequential"},
		},
		want: apis.ErrMissingOneOf("spec.taskTestSuiteRef", "spec.taskTestSuiteSpec"),
	}, {
		name: "execution mode set to invalid value",
		tsr: v1alpha1.TaskTestSuiteRun{
			ObjectMeta: v1.ObjectMeta{Name: "run"},
			Spec: v1alpha1.TaskTestSuiteRunSpec{
				TaskTestSuiteRef: &v1alpha1.TaskTestSuiteRef{Name: "suite"},
				ExecutionMode:    "InvalidMode",
			},
		},
		want: apis.ErrInvalidValue("'InvalidMode'", "spec.executionMode", "executionMode must be set to either 'Parallel' or 'Sequential'"),
	}, {
		name: "inline task test suite spec invalid",
		tsr: v1alpha1.TaskTestSuiteRun{
			ObjectMeta: v1.ObjectMeta{Name: "run"},
			Spec: v1alpha1.TaskTestSuiteRunSpec{
				TaskTestSuiteSpec: &v1alpha1.TaskTestSuiteSpec{
					TaskTests: []v1alpha1.SuiteTest{{
						Name:        "test0",
						TaskTestRef: &v1alpha1.TaskTestRef{"task0"},
					}, {
						Name:        "test0",
						TaskTestRef: &v1alpha1.TaskTestRef{"task0"},
					}},
				},
				ExecutionMode: "Sequential",
			},
		},
		want: apis.ErrMultipleOneOf("spec.taskTestSuiteSpec.taskTests[1].name"),
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.tsr.Validate(t.Context())
			if err == nil {
				t.Error("TaskTestSuiteRun.Validate() did not return error for invalid pipeline")
			}
			if d := cmp.Diff(tt.want.Error(), err.Error(), cmpopts.IgnoreUnexported(apis.FieldError{})); d != "" {
				t.Errorf("TaskTestSuiteRun.Validate() errors diff %s", diff.PrintWantGot(d))
			}
		})
	}
}

func TestTaskTestSuiteRun_Validate(t *testing.T) {
	tests := []struct {
		name string
		tsr  v1alpha1.TaskTestSuiteRun
		want *apis.FieldError
	}{{
		name: "run with spec and no ref",
		tsr: v1alpha1.TaskTestSuiteRun{
			ObjectMeta: v1.ObjectMeta{Name: "run"},
			Spec: v1alpha1.TaskTestSuiteRunSpec{
				TaskTestSuiteSpec: &v1alpha1.TaskTestSuiteSpec{
					TaskTests: []v1alpha1.SuiteTest{{
						Name:        "test0",
						TaskTestRef: &v1alpha1.TaskTestRef{"task0"},
					}, {
						Name:        "test1",
						TaskTestRef: &v1alpha1.TaskTestRef{"task0"},
					}},
				},
				ExecutionMode: "Sequential",
			},
		},
	}, {
		name: "run with ref and no spec",
		tsr: v1alpha1.TaskTestSuiteRun{
			ObjectMeta: v1.ObjectMeta{Name: "run"},
			Spec: v1alpha1.TaskTestSuiteRunSpec{
				TaskTestSuiteRef: &v1alpha1.TaskTestSuiteRef{Name: "suite"},
				ExecutionMode:    "Parallel",
			},
		},
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.tsr.Validate(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
}
