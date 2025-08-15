package v1alpha1

import (
	"context"
	"time"

	"github.com/tektoncd/pipeline/pkg/apis/config"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"knative.dev/pkg/apis"
)

// SetDefaults implements apis.Defaultable.
func (ttr *TaskTestRun) SetDefaults(ctx context.Context) {
	ttr.Spec.SetDefaults(ctx)
	// ttr.Status.SetDefaults(ctx)
}

func (trs *TaskTestRunSpec) SetDefaults(ctx context.Context) {
	cfg := config.FromContextOrDefaults(ctx)

	if trs.Timeout == nil {
		trs.Timeout = &metav1.Duration{Duration: time.Duration(cfg.Defaults.DefaultTimeoutMinutes) * time.Minute}
	}

	if trs.AllTriesMustSucceed == nil {
		trs.AllTriesMustSucceed = ptr.To(false)
	}
}

var _ apis.Defaultable = (*TaskTestRun)(nil)
var _ apis.Defaultable = (*TaskTestRunSpec)(nil)
