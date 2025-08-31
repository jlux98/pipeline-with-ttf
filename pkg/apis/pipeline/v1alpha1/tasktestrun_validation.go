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

	if trs.TaskTestSpec == nil && trs.TaskTestRef == nil {
		errs = errs.Also(apis.ErrMissingOneOf("taskTestRef", "taskTestSpec"))
	}

	if trs.TaskTestSpec != nil {
		if trs.TaskTestRef != nil {
			errs = errs.Also(apis.ErrMultipleOneOf("taskTestRef", "taskTestSpec"))
		}
		errs = errs.Also(trs.TaskTestSpec.Validate(ctx).ViaField("taskTestSpec"))
	}

	if trs.Retries < 0 {
		errs = errs.Also(apis.ErrInvalidValue(trs.Retries, "retries", "retries must be set to a value >= 0"))
	}

	errs = errs.Also(validateTimeoutDuration(trs.Timeout))

	return errs
}

var _ apis.Validatable = (*TaskTestRun)(nil)
var _ apis.Validatable = (*TaskTestRunSpec)(nil)
