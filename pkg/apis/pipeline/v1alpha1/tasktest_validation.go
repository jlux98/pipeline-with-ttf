package v1alpha1

import (
	"context"

	"github.com/tektoncd/pipeline/pkg/apis/validate"
	"knative.dev/pkg/apis"
)

var _ apis.Validatable = (*TaskTest)(nil)

// Validate implements apis.Validatable.
func (tt *TaskTest) Validate(ctx context.Context) *apis.FieldError {
	errs := validate.ObjectMetadata(tt.GetObjectMeta()).ViaField("metadata")

	// FIXME(jlux98) finish implementing this
	return errs
}
