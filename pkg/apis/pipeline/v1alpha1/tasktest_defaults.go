package v1alpha1

import (
	"context"

	"knative.dev/pkg/apis"
)

var _ apis.Defaultable = (*TaskTest)(nil)

// SetDefaults implements apis.Defaultable.
func (in *TaskTest) SetDefaults(context.Context) {
	// FIXME(jlux98) implement this
	panic("unimplemented")
}
