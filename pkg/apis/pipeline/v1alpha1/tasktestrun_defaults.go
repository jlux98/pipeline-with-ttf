package v1alpha1

import (
	"context"

	"knative.dev/pkg/apis"
)

// SetDefaults implements apis.Defaultable.
func (t *TaskTestRun) SetDefaults(context.Context) {
	// FIXME(jlux98) implement this
	panic("unimplemented")
}

var _ apis.Defaultable = (*TaskTestRun)(nil)
