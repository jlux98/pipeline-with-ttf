package v1alpha1

import (
	"context"

	"knative.dev/pkg/apis"
)

// Validate implements apis.Validatable.
func (t *TaskTestSuiteRun) Validate(context.Context) *apis.FieldError {
	// FIXME(jlux98) implement this
	panic("unimplemented")
}

var _ apis.Validatable = (*TaskTestSuiteRun)(nil)
