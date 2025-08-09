package v1alpha1

import (
	"context"
	"time"

	"github.com/tektoncd/pipeline/pkg/apis/config"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"
)

// SetDefaults implements apis.Defaultable.
func (tsr *TaskTestSuiteRun) SetDefaults(ctx context.Context) {
	tsr.Spec.SetDefaults(ctx)
}

// SetDefaults implements apis.Defaultable.
func (trs *TaskTestSuiteRunSpec) SetDefaults(ctx context.Context) {
	cfg := config.FromContextOrDefaults(ctx)

	if trs.Timeout == nil {
		trs.Timeout = &metav1.Duration{Duration: time.Duration(cfg.Defaults.DefaultTimeoutMinutes) * time.Minute}
	}
}

var _ apis.Defaultable = (*TaskTestSuiteRun)(nil)
var _ apis.Defaultable = (*TaskTestSuiteRunSpec)(nil)
