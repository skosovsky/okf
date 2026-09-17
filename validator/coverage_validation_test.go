package validator

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
)

func TestValidationEdgePaths(t *testing.T) {
	t.Parallel()

	// Arrange.
	report := Report{Diagnostics: []Diagnostic{
		{Code: "version_assertion_failed", SpecRef: "toolkit#version-selector-assertion", FieldPath: "okf_version", Severity: SeverityWarning, File: "a.md", Message: "path warning", PolicyFailure: true},
		{Severity: SeverityInfo, File: "a", Message: "concept info"},
		{Severity: SeverityError, Message: "plain error"},
	}}

	// Act / Assert.
	if SeverityError.String() != "ERROR" || SeverityWarning.String() != "WARN" || SeverityInfo.String() != "INFO" || Severity(99).String() != "UNKNOWN" {
		t.Fatal("Severity.String() returned unexpected labels")
	}
	if !strings.Contains(report.Diagnostics[0].String(), "a.md: path warning") {
		t.Fatalf("Diagnostic path String() = %q", report.Diagnostics[0].String())
	}
	for _, fragment := range []string{"code=version_assertion_failed", "spec_ref=toolkit#version-selector-assertion", "field_path=okf_version", "policy_failure=true"} {
		if !strings.Contains(report.Diagnostics[0].String(), fragment) {
			t.Fatalf("Diagnostic.String() = %q, want %q", report.Diagnostics[0].String(), fragment)
		}
	}
	if !strings.Contains(report.Diagnostics[1].String(), "a: concept info") {
		t.Fatalf("Diagnostic concept String() = %q", report.Diagnostics[1].String())
	}
	if !strings.Contains(report.Diagnostics[2].String(), "plain error") {
		t.Fatalf("Diagnostic plain String() = %q", report.Diagnostics[2].String())
	}
	if got, want := report.WarningCount(), 1; got != want {
		t.Fatalf("WarningCount() = %d, want %d", got, want)
	}
	if got, want := report.PolicyFailureCount(), 1; got != want || !report.HasPolicyFailures() || report.ExitCode() != 1 {
		t.Fatalf("policy outcome = count:%d has:%v exit:%d, want %d/true/1", got, report.HasPolicyFailures(), report.ExitCode(), want)
	}
	nilReport := ValidateBundle(nil, nil)
	if nilReport.IsConformant() {
		t.Fatal("ValidateBundle(nil).IsConformant() = true, want false")
	}
	assertDiagnosticMachineContract(t, nilReport)
	assertExactValidationDiagnostic(t, nilReport, Diagnostic{
		Code: "bundle_required", SpecRef: "toolkit#bundle-input", File: ".",
		FieldPath: "bundle", Severity: SeverityError,
	})

	sourceReport, err := ValidateSource(context.Background(), failingValidationSource{}, nil)
	if err != nil {
		t.Fatalf("ValidateSource(failing) error = %v, want structured load diagnostic", err)
	}
	assertDiagnosticMachineContract(t, sourceReport)
	assertExactValidationDiagnostic(t, sourceReport, Diagnostic{
		Code: "bundle_load_failed", SpecRef: "toolkit#bundle-loading", File: ".",
		FieldPath: "bundle", Severity: SeverityError,
	})
}

func TestValidateBundleDoesNotLoadBundle(t *testing.T) {
	t.Parallel()

	// Arrange.
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() ok = false")
	}
	sourcePath := filepath.Join(filepath.Dir(filename), "validate.go")
	file, err := parser.ParseFile(token.NewFileSet(), sourcePath, nil, 0)
	if err != nil {
		t.Fatalf("ParseFile(validate.go) error = %v", err)
	}
	var validateBundle *ast.FuncDecl
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == "ValidateBundle" {
			validateBundle = fn
			break
		}
	}
	if validateBundle == nil {
		t.Fatal("ValidateBundle declaration not found")
	}

	// Act.
	loadsBundle := false
	ast.Inspect(validateBundle.Body, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "LoadBundle" {
			return true
		}
		ident, ok := selector.X.(*ast.Ident)
		if ok && ident.Name == "bundle" {
			loadsBundle = true
			return false
		}
		return true
	})

	// Assert.
	if loadsBundle {
		t.Fatal("ValidateBundle calls bundle.LoadBundle; path loading belongs to ValidatePath")
	}
}

func TestValidatePathMatchesLoadedBundleValidation(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeValidationFile(t, root, "a.md", "---\ntype: Note\n---\nSee [missing](/missing.md).\n")
	b, err := bundle.LoadBundle(root)
	if err != nil {
		t.Fatalf("LoadBundle() error = %v", err)
	}
	cfg := &ValidatorConfig{CheckLinks: true}

	// Act.
	pathReport := ValidatePath(root, cfg)
	bundleReport := ValidateBundle(b, cfg)

	// Assert.
	if !reflect.DeepEqual(pathReport, bundleReport) {
		t.Fatalf("ValidatePath() = %#v, ValidateBundle(loaded) = %#v", pathReport, bundleReport)
	}
}

func TestValidateTimestampAndReservedEdges(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeValidationFile(t, root, "a.md", "---\ntype: Note\ntitle: A\ndescription: A\ntimestamp: not-a-date\n---\nbody\n")
	writeValidationFile(t, root, "index.md", "---\nokf_version: \"0.1\"\ntitle: Root\n---\n\n# Root\n")
	writeValidationFile(t, root, "empty/index.md", "# Empty\n")
	writeValidationFile(t, root, "bad/index.md", "---\ntitle: Broken\n")

	// Act.
	report := ValidatePath(root, &ValidatorConfig{Strict: true})

	// Assert.
	warnings := report.Of(SeverityWarning)
	if !validationDiagnosticsContain(warnings, "timestamp") {
		t.Fatalf("warnings = %#v, want timestamp warning", warnings)
	}
	errors := report.Of(SeverityError)
	if !validationDiagnosticsContain(errors, "root index.md frontmatter") {
		t.Fatalf("errors = %#v, want root index extra key error", errors)
	}
}

func TestValidateMissingBundlePath(t *testing.T) {
	t.Parallel()

	// Arrange.
	path := filepath.Join(t.TempDir(), "missing")

	// Act.
	report := ValidatePath(path, nil)

	// Assert.
	if report.IsConformant() {
		t.Fatalf("ValidatePath() = %#v, want missing path error", report.Diagnostics)
	}
	assertDiagnosticMachineContract(t, report)
	assertExactValidationDiagnostic(t, report, Diagnostic{
		Code: "bundle_load_failed", SpecRef: "toolkit#bundle-loading", File: path,
		FieldPath: "bundle", Severity: SeverityError,
	})
}

func TestParseDiagnosticsAreEmittedExactlyOnceAcrossOptionalPasses(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeValidationFile(t, root, "bad.md", "---\ntype: [\n---\nBody.\n")
	writeValidationFile(t, root, "index.md", "---\nokf_version: [\n---\n# Notes\n")

	// Act.
	report := ValidatePath(root, &ValidatorConfig{CheckLinks: true, CheckOrphans: true})

	// Assert.
	for _, want := range []Diagnostic{
		{Code: "concept_unparseable", SpecRef: "okf-v0.2#11.1", File: "bad.md", FieldPath: "frontmatter", Severity: SeverityError},
		{Code: "index_structure_invalid", SpecRef: "okf-v0.2#11.3", File: "index.md", FieldPath: "frontmatter", Severity: SeverityError},
	} {
		count := 0
		for _, diagnostic := range report.Diagnostics {
			if diagnostic.Code == want.Code && diagnostic.File == want.File && diagnostic.FieldPath == want.FieldPath {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("%s/%s count = %d, want exactly one: %#v", want.Code, want.File, count, report.Diagnostics)
		}
		assertExactValidationDiagnostic(t, report, want)
	}
}

func TestISODateInvalidDigitsAndDay(t *testing.T) {
	t.Parallel()

	// Act / Assert.
	if IsISODate("2026-aa-22") {
		t.Fatal("IsISODate(non-digits) = true, want false")
	}
	if IsISODate("2026-05-00") {
		t.Fatal("IsISODate(day zero) = true, want false")
	}
}

type failingValidationSource struct{}

func (failingValidationSource) Paths(context.Context) ([]string, error) {
	return nil, errors.New("source unavailable")
}

func (failingValidationSource) ReadFile(context.Context, string) ([]byte, error) {
	return nil, errors.New("source unavailable")
}
