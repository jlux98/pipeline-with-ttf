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
	if tts.Inputs != nil {
		if tts.Inputs.Params != nil {
			for i := range tts.Inputs.Params {
				if tts.Inputs.Params[i].Value.Type == "" {
					tts.Inputs.Params[i].Value.Type = v1.ParamTypeString
				}
			}
		}
	}
	if tts.Expects != nil {
		if tts.Expects.Results != nil {
			for i := range tts.Expects.Results {
				logging.FromContext(ctx).Infof(`Result "%s", expected value "%s", type "%s"`, tts.Expects.Results[i].Name, tts.Expects.Results[i].Value.StringVal, tts.Expects.Results[i].Type)
				if tts.Expects.Results[i].Value.Type == "" {
					tts.Expects.Results[i].Value.Type = v1.ParamTypeString
				}
				if tts.Expects.Results[i].Type == "" {
					tts.Expects.Results[i].Type = v1.ResultsTypeString
				}
			}
		}
		if tts.Expects.FileSystemContents != nil {
			for i := range tts.Expects.FileSystemContents {
				for j := range tts.Expects.FileSystemContents[i].Objects {
					if tts.Expects.FileSystemContents[i].Objects[j].Type == "" {
						tts.Expects.FileSystemContents[i].Objects[j].Type = AnyObjectType
					}
				}
			}
		}
	}
}
