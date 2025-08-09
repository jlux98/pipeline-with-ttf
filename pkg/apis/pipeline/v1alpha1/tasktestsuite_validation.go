package v1alpha1

import (
	"context"

	"github.com/tektoncd/pipeline/pkg/apis/validate"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"knative.dev/pkg/apis"
)

// Validate implements apis.Validatable.
func (t *TaskTestSuite) Validate(ctx context.Context) *apis.FieldError {
	// FIXME(jlux98) implement this
	errs := validate.ObjectMetadata(t.GetObjectMeta()).ViaField("metadata")
	errs = errs.Also(t.Spec.Validate(ctx).ViaField("spec"))
	return errs
}

type named interface {
	GetName() string
}

func extractNamesFromSuiteTest(list []SuiteTest) []string {
	result := make([]string, len(list))
	for i := range list {
		result[i] = list[i].Name
	}
	return result
}

var _ named = (*SuiteTest)(nil)

// Validate implements apis.Validatable.
func (t *TaskTestSuiteSpec) Validate(ctx context.Context) *apis.FieldError {
	// FIXME(jlux98) implement this
	var errs *apis.FieldError
	if len(t.TaskTests) == 0 {
		errs = errs.Also(apis.ErrGeneric("expected at least one, got none", "taskTests"))
	}
	errs = errs.Also(ValidateIdentifierUniqueness(
		extractNamesFromSuiteTest(t.TaskTests), "name").
		ViaField("taskTests"))

	for i := range t.TaskTests {
		if t.TaskTests[i].TaskTestRef == nil && t.TaskTests[i].TaskTestSpec == nil {
			errs = errs.Also(apis.ErrMissingOneOf("taskTestRef", "taskTestSpec").ViaFieldIndex("taskTests", i))
		}

		if t.TaskTests[i].TaskTestRef != nil && t.TaskTests[i].TaskTestSpec != nil {
			errs = errs.Also(apis.ErrMultipleOneOf("taskTestRef", "taskTestSpec").ViaFieldIndex("taskTests", i))
		}

		if t.TaskTests[i].Retries < 0 {
			errs = errs.Also(apis.ErrInvalidValue(t.TaskTests[i].Retries, "retries", "retries must be set to a value >= 0").ViaFieldIndex("taskTests", i))
		}

		errs = errs.Also(validateTimeoutDuration(t.TaskTests[i].Timeout).ViaFieldIndex("taskTests", i))
	}
	return errs
}

func ValidateIdentifierUniqueness(list []string, fieldName string) *apis.FieldError {
	var errs *apis.FieldError
	identifiers := sets.Set[string]{}
	for i := range list {
		if _, ok := identifiers[list[i]]; ok {
			errs = errs.Also(apis.ErrMultipleOneOf(fieldName).ViaIndex(i))
		} else {
			identifiers.Insert(list[i])
		}
	}
	return errs
}

func validateTimeoutDuration(d *metav1.Duration) (errs *apis.FieldError) {
	if d != nil && d.Duration < 0 {
		return errs.Also(apis.ErrInvalidValue(d.Duration.String()+" should be >= 0", "timeout"))
	}
	return nil
}

var _ apis.Validatable = (*TaskTestSuite)(nil)
var _ apis.Validatable = (*TaskTestSuiteSpec)(nil)
