package mutation

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
	"github.com/skosovsky/okf/validator"
)

func TestPlanPreviewContract_DiagnosticsAndReads(t *testing.T) {
	// Arrange.
	source := memorySource{
		"a.md":      []byte("---\ntype: thing\nparts:\n  - id: canonical\n    anchor: legacy\n---\nA\n"),
		"b.md":      []byte("---\ntype: thing\n---\n[old](a.md)\n"),
		"c.md":      []byte("---\ntype: thing\n---\n[old](a.md)\n"),
		"asset.bin": []byte{0, 1, 2},
		"digest":    []byte("checked"),
	}
	move := store.ChangeSet{
		Version: store.ChangeSetFormatVersion, ID: "preview-move", Actor: "tester", BaseRevision: revisionFor(t, source),
		Operations:    []store.Operation{store.MoveConcept{From: ref(t, "a").ID, To: ref(t, "nested/a").ID}},
		Preconditions: []store.Precondition{store.FileDigestEquals{Path: "digest", Digest: "e61a3d78c65133c7260c43e719d97c46e16bd50c64ee7800536cf14c6407a617"}},
	}

	// Act.
	result, err := Plan(context.Background(), source, move)

	// Assert.
	if err != nil {
		t.Fatalf("%v; validation=%#v relations=%#v", err, result.Validation, result.RelationDiagnostics)
	}
	paths := make([]string, len(result.Preview.Reads))
	for i, read := range result.Preview.Reads {
		paths[i] = read.Path
	}
	if want := []string{"a.md", "asset.bin", "b.md", "c.md", "digest"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("preview reads = %#v, want %#v", paths, want)
	}
	if !sort.StringsAreSorted(paths) {
		t.Fatalf("preview reads are not sorted: %#v", paths)
	}

	alias := store.ChangeSet{Version: store.ChangeSetFormatVersion, ID: "preview-alias", Actor: "tester", BaseRevision: revisionFor(t, source), Operations: []store.Operation{store.EnsureRelation{Source: ref(t, "a#canonical"), Type: "uses", Target: ref(t, "b")}}}
	aliasResult, err := Plan(context.Background(), source, alias)
	if err != nil {
		t.Fatal(err)
	}
	if !containsPreviewDiagnostic(aliasResult.Preview.Diagnostics, store.DiagnosticRelation, store.DiagnosticInfo, "anchor_alias") {
		t.Fatalf("preview diagnostics = %#v, want informational anchor_alias", aliasResult.Preview.Diagnostics)
	}

	renameSource := memorySource{
		"a.md": []byte("---\ntype: thing\nparts:\n  - id: old\n---\nA\n"),
		"b.md": []byte("---\ntype: thing\nrelations:\n  uses:\n    - target: a#old\n---\nB\n"),
		"c.md": []byte("---\ntype: thing\n---\nC\n"),
	}
	rename := store.ChangeSet{Version: store.ChangeSetFormatVersion, ID: "preview-rename", Actor: "tester", BaseRevision: revisionFor(t, renameSource), Operations: []store.Operation{store.RenameFragment{Concept: ref(t, "a").ID, From: "old", To: "new"}}}
	renameResult, err := Plan(context.Background(), renameSource, rename)
	if err != nil {
		t.Fatal(err)
	}
	renamePaths := make([]string, len(renameResult.Preview.Reads))
	for i, read := range renameResult.Preview.Reads {
		renamePaths[i] = read.Path
	}
	if want := []string{"a.md", "b.md", "c.md"}; !reflect.DeepEqual(renamePaths, want) {
		t.Fatalf("rename preview reads = %#v, want %#v", renamePaths, want)
	}
}

func TestPlanPreviewContract_BlockingRelationDiagnostic(t *testing.T) {
	// Arrange.
	source := memorySource{
		"a.md": []byte("---\ntype: thing\n---\nA\n"),
		"b.md": []byte("---\ntype: thing\nrelations:\n  uses:\n    - target: a#missing\n---\nB\n"),
	}
	change := change(t, source, store.EnsureRelation{Source: ref(t, "a"), Type: "uses", Target: ref(t, "b")})

	// Act.
	result, err := Plan(context.Background(), source, change)

	// Assert.
	var invalid *store.InvalidChangeSet
	if !errors.As(err, &invalid) || invalid.Code != "staged_validation_failed" {
		t.Fatalf("Plan() error = %#v", err)
	}
	if !containsPreviewDiagnostic(result.Preview.Diagnostics, store.DiagnosticRelation, store.DiagnosticError, "missing_or_ambiguous_target_fragment") {
		t.Fatalf("invalid preview diagnostics = %#v, want blocking relation finding", result.Preview.Diagnostics)
	}
	if len(result.Preview.Writes) != 0 || result.Staged != nil {
		t.Fatalf("invalid plan exposed commit-ready state: %#v", result)
	}
	if len(result.Preview.Plan) != 0 || len(result.Preview.Deletes) != 0 || len(result.Preview.Renames) != 0 {
		t.Fatalf("invalid plan exposed commit-ready operations: %#v", result.Preview)
	}
	if !containsStructuredRelationDiagnostic(result.Preview.Diagnostics, "missing_or_ambiguous_target_fragment", "b.md", "uses", "a#missing", "b") {
		t.Fatalf("preview diagnostics lost relation context: %#v", result.Preview.Diagnostics)
	}
	if !reflect.DeepEqual(invalid.Diagnostics, result.Preview.Diagnostics) {
		t.Fatalf("error diagnostics = %#v, want preview diagnostics %#v", invalid.Diagnostics, result.Preview.Diagnostics)
	}
	for _, diagnostic := range result.RelationDiagnostics {
		if diagnostic.BlocksMutation() && diagnostic.Code != "" {
			return
		}
	}
	t.Fatalf("relation diagnostics = %#v, want blocking finding", result.RelationDiagnostics)
}

func TestPlanPreviewContract_ExplicitVersionPolicyFailureIsDiagnosticOnly(t *testing.T) {
	// Arrange.
	source := memorySource{
		"index.md": []byte("---\nokf_version: \"0.2\"\n---\n# Knowledge\n\n* [A](a.md)\n"),
		"a.md":     []byte("---\ntype: Note\n---\nBody\n"),
	}
	operation, err := store.NewSetGenerated(
		ref(t, "a").ID,
		store.Generation{By: "human:reviewer"},
	)
	if err != nil {
		t.Fatal(err)
	}
	request := change(t, source, operation)
	planner := NewPlanner(&validator.ValidatorConfig{Spec: bundle.LegacyOKFVersion})

	// Act.
	result, err := planner.Plan(context.Background(), source, request)

	// Assert.
	var invalid *store.InvalidChangeSet
	if !errors.As(err, &invalid) || invalid.Code != "staged_validation_failed" {
		t.Fatalf("Plan() error = %#v, want staged_validation_failed", err)
	}
	if !result.Validation.IsConformant() ||
		!result.Validation.HasPolicyFailures() ||
		result.Validation.ExitCode() == 0 {
		t.Fatalf("validation = %#v, want conformant report with failed explicit policy", result.Validation)
	}
	if !containsPreviewDiagnostic(
		result.Preview.Diagnostics,
		store.DiagnosticValidation,
		store.DiagnosticWarning,
		"version_assertion_failed",
	) {
		t.Fatalf("preview diagnostics = %#v, want version assertion policy failure", result.Preview.Diagnostics)
	}
	if result.Staged != nil ||
		len(result.Preview.Writes) != 0 ||
		len(result.Preview.Deletes) != 0 ||
		len(result.Preview.Renames) != 0 ||
		len(result.Preview.Plan) != 0 ||
		len(result.Preview.AffectedRefs) != 0 ||
		len(result.Preview.ReverseImpact) != 0 {
		t.Fatalf("policy-rejected plan exposed commit-ready artifacts: %#v", result)
	}
	if !reflect.DeepEqual(invalid.Diagnostics, result.Preview.Diagnostics) {
		t.Fatalf(
			"error diagnostics = %#v, want preview diagnostics %#v",
			invalid.Diagnostics,
			result.Preview.Diagnostics,
		)
	}
}

func TestPlanPreviewContract_OrdinaryStrictWarningsRemainNonBlocking(t *testing.T) {
	// Arrange.
	source := memorySource{
		"index.md": []byte("---\nokf_version: \"0.2\"\n---\n# Knowledge\n\n* [A](a.md)\n"),
		"a.md":     []byte("---\ntype: Note\nstatus: archived\n---\nBody\n"),
	}
	operation, err := store.NewSetGenerated(
		ref(t, "a").ID,
		store.Generation{By: "human:reviewer"},
	)
	if err != nil {
		t.Fatal(err)
	}
	request := change(t, source, operation)
	planner := NewPlanner(&validator.ValidatorConfig{Strict: true, Spec: bundle.OKFVersion})

	// Act.
	result, err := planner.Plan(context.Background(), source, request)

	// Assert.
	if err != nil {
		t.Fatalf("Plan() error = %v; validation=%#v", err, result.Validation)
	}
	if !result.Validation.IsConformant() ||
		result.Validation.HasPolicyFailures() ||
		result.Validation.ExitCode() != 0 ||
		result.Validation.WarningCount() == 0 {
		t.Fatalf("validation = %#v, want nonblocking strict warning", result.Validation)
	}
	if !containsPreviewDiagnostic(
		result.Preview.Diagnostics,
		store.DiagnosticValidation,
		store.DiagnosticWarning,
		"status_invalid",
	) {
		t.Fatalf("preview diagnostics = %#v, want ordinary strict warning", result.Preview.Diagnostics)
	}
	if result.Staged == nil || len(result.Preview.Writes) == 0 || len(result.Preview.Plan) == 0 {
		t.Fatalf("warning-only plan lost commit-ready artifacts: %#v", result)
	}
}

func containsStructuredRelationDiagnostic(diagnostics []store.Diagnostic, code, file, relationType, rawTarget, ref string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Kind == store.DiagnosticRelation && diagnostic.Severity == store.DiagnosticError && diagnostic.Code == code && diagnostic.File == file && diagnostic.RelationType == relationType && diagnostic.RawTarget == rawTarget && len(diagnostic.Refs) == 1 && diagnostic.Refs[0].String() == ref {
			return true
		}
	}
	return false
}

func containsPreviewDiagnostic(diagnostics []store.Diagnostic, kind store.DiagnosticKind, severity store.DiagnosticSeverity, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Kind == kind && diagnostic.Severity == severity && diagnostic.Code == code {
			return true
		}
	}
	return false
}
