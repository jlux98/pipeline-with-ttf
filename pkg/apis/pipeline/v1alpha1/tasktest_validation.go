package v1alpha1

import (
	"context"
	"slices"
	"strings"

	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipeline/pkg/apis/validate"
	"knative.dev/pkg/apis"
)

var _ apis.Validatable = (*TaskTest)(nil)
var _ apis.Validatable = (*TaskTestSpec)(nil)

// Validate implements apis.Validatable.
func (tt *TaskTest) Validate(ctx context.Context) *apis.FieldError {
	errs := validate.ObjectMetadata(tt.GetObjectMeta()).ViaField("metadata")

	// FIXME(jlux98) finish implementing this
	return errs.Also(tt.Spec.Validate(ctx).ViaField("spec"))
}

// Validate implements apis.Validatable.
func (ts *TaskTestSpec) Validate(ctx context.Context) *apis.FieldError {
	errs := ts.Inputs.Validate(ctx).ViaField("inputs")
	errs = errs.Also(ts.Expected.Validate(ctx).ViaField("expected"))
	return errs
}

func (tti *TaskTestInputs) Validate(ctx context.Context) *apis.FieldError {
	var errs *apis.FieldError
	errs = errs.Also(v1.ValidateParameters(ctx, tti.Params).ViaField("params"))
	errs = errs.Also(ValidateIdentifierUniqueness(extractNamesFromWorkspaceContents(tti.WorkspaceContents), "name").ViaField("workspaceContents"))
	for i, wc := range tti.WorkspaceContents {
		errs = errs.Also(wc.Validate().ViaFieldIndex("workspaceContents", i))
	}
	return errs.Also()
}

func (wc *InitialWorkspaceContents) Validate() *apis.FieldError {
	var errs *apis.FieldError
	errs = errs.Also(ValidateIdentifierUniqueness(extractPathsFromInputFileSystemObjects(wc.Objects), "path").ViaField("objects"))
	for i, wo := range wc.Objects {
		errs = errs.Also(wo.Validate().ViaFieldIndex("objects", i))
	}
	return errs.Also()
}

func (ifo *InputFileSystemObject) Validate() *apis.FieldError {
	var errs *apis.FieldError
	if i := slices.Index(DisallowedInputFileSystemPathEndings, rune(ifo.Path[len(ifo.Path)-1])); i >= 0 {
		errs = errs.Also(apis.ErrInvalidValue(ifo.Path, "path", "input path may not end on '"+string(DisallowedInputFileSystemPathEndings[i])+"'"))
	}
	if strings.Contains(ifo.Path, "\000") {
		errs = errs.Also(apis.ErrInvalidValue(ifo.Path, "path", "illegal character \000 detected"))
	}
	if !slices.Contains(AllowedInputFileSystemObjectTypes, ifo.Type) {
		errs = errs.Also(apis.ErrInvalidValue(ifo.Type, "type"))
	}
	if ifo.Type != InputTextFileType && ifo.Content != "" {
		errs = errs.Also(apis.ErrDisallowedFields("content"))
	}
	return errs
}

var AllowedInputFileSystemObjectTypes []InputFileSystemObjectType = []InputFileSystemObjectType{
	InputDirectoryType,
	InputTextFileType,
}

var DisallowedInputFileSystemPathEndings []rune = []rune{
	'/', '.', ' ',
}

func (e *ExpectedOutcomes) Validate(ctx context.Context) *apis.FieldError {
	var errs *apis.FieldError
	errs = errs.Also(ValidateIdentifierUniqueness(extractNamesFromTaskResults(e.Results), "name").ViaField("results"))
	for i := range e.FileSystemContents {
		errs = errs.Also(e.FileSystemContents[i].Validate(ctx).ViaFieldIndex("fileSystemContents", i))
	}

	return errs
}

func (fc *ExpectedStepFileSystemContent) Validate(ctx context.Context) *apis.FieldError {
	var errs *apis.FieldError
	errs = errs.Also(ValidateIdentifierUniqueness(extractPathsFromFileSystemObjects(fc.Objects), "path").ViaField("objects"))
	for i := range fc.Objects {
		errs = errs.Also(fc.Objects[i].Validate(ctx).ViaFieldIndex("objects", i))
	}
	return errs
}

func (fo *FileSystemObject) Validate(ctx context.Context) *apis.FieldError {
	var errs *apis.FieldError
	if !strings.HasPrefix(fo.Path, "/") {
		errs = errs.Also(apis.ErrInvalidValue(fo.Path, "path", "path must start with a '/'"))
	}
	if strings.Contains(fo.Path, "\000") {
		errs = errs.Also(apis.ErrInvalidValue(fo.Path, "path", "illegal character \000 detected"))
	}
	if !slices.Contains(AllowedFileSystemObjectTypes, fo.Type) {
		errs = errs.Also(apis.ErrInvalidValue(fo.Type, "type"))
	}
	if fo.Type != TextFileType && fo.Content != "" {
		errs = errs.Also(apis.ErrDisallowedFields("content"))
	}
	return errs
}

var AllowedFileSystemObjectTypes []FileSystemObjectType = []FileSystemObjectType{
	DirectoryType,
	TextFileType,
	BinaryFileType,
	AnyFileType,
	AnyObjectType,
	None,
}
