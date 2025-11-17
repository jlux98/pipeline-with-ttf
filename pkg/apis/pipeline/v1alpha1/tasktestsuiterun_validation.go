package v1alpha1

import (
	"context"
	"slices"

	"github.com/tektoncd/pipeline/pkg/apis/validate"
	"knative.dev/pkg/apis"
)

// Validate implements apis.Validatable.
func (t *TaskTestSuiteRun) Validate(ctx context.Context) *apis.FieldError {
	// FIXME(jlux98) implement this
	errs := validate.ObjectMetadata(t.GetObjectMeta()).ViaField("metadata")
	errs = errs.Also(t.Spec.Validate(ctx).ViaField("spec"))
	return errs.Also(t.Status.Validate(ctx).ViaField("status"))
}

var _ apis.Validatable = (*TaskTestSuiteRun)(nil)

// Validate implements apis.Validatable.
func (t *TaskTestSuiteRunSpec) Validate(ctx context.Context) *apis.FieldError {
	var errs *apis.FieldError
	if t.TaskTestSuiteRef == nil && t.TaskTestSuiteSpec == nil {
		errs = errs.Also(apis.ErrMissingOneOf("taskTestSuiteRef", "taskTestSuiteSpec"))
	}

	if t.TaskTestSuiteRef != nil && t.TaskTestSuiteSpec != nil {
		errs = errs.Also(apis.ErrMultipleOneOf("taskTestSuiteRef", "taskTestSuiteSpec"))
	}

	if !slices.Contains(AllowedTestSuiteExecutionModes, t.ExecutionMode) {
		errs = errs.Also(apis.ErrInvalidValue("'"+t.ExecutionMode+"'", "executionMode", "executionMode must be set to either 'Parallel' or 'Sequential'"))
	}

	if t.TaskTestSuiteSpec != nil {
		errs = errs.Also(t.TaskTestSuiteSpec.Validate(ctx).ViaField("taskTestSuiteSpec"))
	}

	errs = errs.Also(validateTimeoutDuration(t.Timeout))

	return errs
}

var _ apis.Validatable = (*TaskTestSuiteRunSpec)(nil)

var AllowedTestSuiteExecutionModes []TestSuiteExecutionMode = []TestSuiteExecutionMode{
	TaskTestSuiteRunExecutionModeParallel,
	TaskTestSuiteRunExecutionModeSequential,
}

func (t *TaskTestSuiteRunStatus) Validate(ctx context.Context) *apis.FieldError {
	var errs *apis.FieldError
	if !apis.IsInUpdate(ctx) {
		return errs
	}
	oldObj, ok := apis.GetBaseline(ctx).(*TaskTestSuiteRun)
	if !ok || oldObj == nil {
		return errs
	}
	old := &oldObj.Status
	if old.CurrentSuiteTest != nil && t.CurrentSuiteTest != nil {
		if *old.CurrentSuiteTest != *t.CurrentSuiteTest {
			errs = apis.ErrInvalidValue(*t.CurrentSuiteTest, "currentSuiteTest", "field currentSuiteTest must first be unset before a new test can be started.")
		}
	}
	return errs
}
