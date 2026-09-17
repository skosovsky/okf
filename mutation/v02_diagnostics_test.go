package mutation

import (
	"context"
	"testing"

	"github.com/skosovsky/okf/store"
	"github.com/skosovsky/okf/validator"
)

func TestPreviewDiagnostics_ProjectsUnknownValidatorDiagnosticExactly(t *testing.T) {
	// Arrange. FieldPath remains available through validator.Report and
	// surface-specific DTOs; Preview v1 intentionally projects only its stable
	// Code/File/Severity/Message contract.
	report := validator.Report{Diagnostics: []validator.Diagnostic{{
		Code:      "future_v02_policy",
		File:      "concepts/future.md",
		FieldPath: "future_family.items[2]",
		Severity:  validator.SeverityWarning,
		Message:   "producer-defined future policy warning",
	}}}

	// Act.
	got, err := previewDiagnosticsContext(context.Background(), report, nil)

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("preview diagnostics = %#v, want one finding", got)
	}
	diagnostic := got[0]
	if diagnostic.Kind != store.DiagnosticValidation ||
		diagnostic.Code != "future_v02_policy" ||
		diagnostic.File != "concepts/future.md" ||
		diagnostic.Severity != store.DiagnosticWarning ||
		diagnostic.Message != "producer-defined future policy warning" {
		t.Fatalf("preview diagnostic = %#v, want exact validation projection", diagnostic)
	}
	if diagnostic.RelationType != "" || diagnostic.RawTarget != "" || len(diagnostic.Refs) != 0 {
		t.Fatalf("preview diagnostic leaked relation projection fields: %#v", diagnostic)
	}
}
