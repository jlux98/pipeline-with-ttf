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
func (t *TaskTestSuite) SetDefaults(ctx context.Context) {
	t.Spec.SetDefaults(ctx)
}

func (ts *TaskTestSuiteSpec) SetDefaults(ctx context.Context) {
	for i := range ts.TaskTests {
		ts.TaskTests[i].SetDefaults(ctx)
	}
}
func (tst *SuiteTest) SetDefaults(ctx context.Context) {
	cfg := config.FromContextOrDefaults(ctx)

	if tst.AllTriesMustSucceed == nil {
		tst.AllTriesMustSucceed = ptr.To(false)
	}
	if tst.Timeout == nil {
		tst.Timeout = &metav1.Duration{Duration: time.Duration(cfg.Defaults.DefaultTimeoutMinutes) * time.Minute}
	}

	if tst.TaskTestSpec != nil {
		tst.TaskTestSpec.SetDefaults(ctx)
	}
}

var _ apis.Defaultable = (*TaskTestSuite)(nil)
