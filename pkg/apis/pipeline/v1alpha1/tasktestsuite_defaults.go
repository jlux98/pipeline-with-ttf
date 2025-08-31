package v1alpha1

import (
	"context"

	"knative.dev/pkg/apis"
)

// SetDefaults implements apis.Defaultable.
func (t *TaskTestSuite) SetDefaults(context.Context) {
}

var _ apis.Defaultable = (*TaskTestSuite)(nil)
