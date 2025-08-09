package v1alpha1

import (
	"context"

	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipeline/pkg/apis/validate"
	"knative.dev/pkg/apis"
)

// Validate implements apis.Validatable.
func (ttr *TaskTestRun) Validate(ctx context.Context) *apis.FieldError {
	errs := validate.ObjectMetadata(ttr.GetObjectMeta()).ViaField("metadata")
	return errs.Also(ttr.Spec.Validate(ctx).ViaField("spec"))
}

// Validate implements apis.Validatable.
func (trs *TaskTestRunSpec) Validate(ctx context.Context) *apis.FieldError {
	errs := v1.ValidateWorkspaceBindings(ctx, trs.Workspaces).ViaField("workspaces")

	if trs.TaskTestRef == nil && trs.TaskTestSpec == nil {
		errs = errs.Also(apis.ErrMissingOneOf("taskTestRef", "taskTestSpec"))
	}

	if trs.TaskTestRef != nil && trs.TaskTestSpec != nil {
		errs = errs.Also(apis.ErrMultipleOneOf("taskTestRef", "taskTestSpec"))
	}

	if trs.Retries < 0 {
		errs = errs.Also(apis.ErrInvalidValue(trs.Retries, "retries", "retries must be set to a value >= 0"))
	}

	errs = errs.Also(validateTimeoutDuration(trs.Timeout))

	return errs
}

var _ apis.Validatable = (*TaskTestRun)(nil)
var _ apis.Validatable = (*TaskTestRunSpec)(nil)
