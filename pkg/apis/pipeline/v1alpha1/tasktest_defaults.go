package v1alpha1

import (
	"context"

	"knative.dev/pkg/apis"
)

var _ apis.Defaultable = (*TaskTest)(nil)

// SetDefaults implements apis.Defaultable.
func (tt *TaskTest) SetDefaults(ctx context.Context) {
	tt.Spec.SetDefaults(ctx)
}

// FIXME(jlux98) implement this
// SetDefaults set any defaults for the task test spec
func (tts *TaskTestSpec) SetDefaults(context.Context) {
	for _, fc := range tts.Expected.FileSystemContents {
		for i := range fc.Objects {
			if fc.Objects[i].Type == "" {
				fc.Objects[i].Type = AnyObjectType
			}
		}
	}
}
