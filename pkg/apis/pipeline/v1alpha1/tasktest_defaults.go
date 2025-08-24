package v1alpha1

import (
	"context"

	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"knative.dev/pkg/apis"
	"knative.dev/pkg/logging"
)

var _ apis.Defaultable = (*TaskTest)(nil)

// SetDefaults implements apis.Defaultable.
func (tt *TaskTest) SetDefaults(ctx context.Context) {
	tt.Spec.SetDefaults(ctx)
}

// FIXME(jlux98) implement this
// SetDefaults set any defaults for the task test spec
func (tts *TaskTestSpec) SetDefaults(ctx context.Context) {
	if tts.Expected != nil {
		if tts.Expected.Results != nil {
			for i := range tts.Expected.Results {
				logging.FromContext(ctx).Infof(`Result "%s", expected value "%s", type "%s"`, tts.Expected.Results[i].Name, tts.Expected.Results[i].Value.StringVal, tts.Expected.Results[i].Type)
				if tts.Expected.Results[i].Value.Type == "" {
					tts.Expected.Results[i].Value.Type = v1.ParamTypeString
				}
				if tts.Expected.Results[i].Type == "" {
					tts.Expected.Results[i].Type = v1.ResultsTypeString
				}
			}
		}
		if tts.Expected.FileSystemContents != nil {
			for i := range tts.Expected.FileSystemContents {
				for j := range tts.Expected.FileSystemContents[i].Objects {
					if tts.Expected.FileSystemContents[i].Objects[j].Type == "" {
						tts.Expected.FileSystemContents[i].Objects[j].Type = AnyObjectType
					}
				}
			}
		}
	}
}
