package mcpserver

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/skosovsky/okf/mutation"
	"github.com/skosovsky/okf/store"
	"github.com/skosovsky/okf/validator"
)

func TestAdapterOwnedOutputCapsFailWithResourceLimit(t *testing.T) {
	// Arrange.
	revision := "sha256:" + strings.Repeat("a", 64)
	resultRevision := "sha256:" + strings.Repeat("c", 64)
	digest := "sha256:" + strings.Repeat("b", 64)
	basePatch := patchPreviewResponse{
		Status: "applicable", BaseRevision: revision, ResultRevision: &resultRevision,
		PlanDigest: digest, Diff: "change", AffectedPaths: []string{"a.md"}, Diagnostics: []wireDiagnostic{},
		UnknownPreserved: true,
	}
	boundaryPatch := basePatch
	boundaryPatch.Diff = strings.Repeat("d", maxPatchDiffBytes)
	overDiff := basePatch
	overDiff.Diff = strings.Repeat("d", maxPatchDiffBytes+1)
	overPaths := basePatch
	overPaths.AffectedPaths = make([]string, maxChangedPaths+1)
	overDiagnostics := basePatch
	overDiagnostics.Diagnostics = make([]wireDiagnostic, maxChangedPaths+1)
	for index := range overDiagnostics.Diagnostics {
		overDiagnostics.Diagnostics[index] = wireDiagnostic{
			Code: "diagnostic", Severity: "INFO",
		}
	}

	proof := testMigrationPlanProofDTO(revision, resultRevision, digest)
	proof.Reads = make([]string, maxChangedPaths+1)
	for index := range proof.Reads {
		proof.Reads[index] = "a.md"
	}
	overProof := migrationPreviewResponse{
		Status: "applicable",
		Source: migrationSourceDTOFromDomain(mutation.MigrationSourceResolution{
			RequestedSelector: mutation.MigrationSelectorAuto,
			ResolvedSource:    mutation.MigrationVersionV01,
			ResolutionSource:  mutation.MigrationResolutionDeclared,
			FromVersion:       mutation.MigrationVersionV01,
			ToVersion:         mutation.MigrationVersionV02,
			Transition:        mutation.MigrationTransitionV01ToV02,
		}),
		Proof: proof, BaseRevision: revision, ResultRevision: &resultRevision,
		PlanDigest: digest, AffectedPaths: []string{"a.md"}, Diff: "change",
		Blockers: []migrationBlockerDTO{}, ManualActions: []migrationManualActionDTO{},
		Diagnostics: []wireDiagnostic{},
	}

	graphNodes := make([]any, 100_001)
	node := map[string]any{}
	for index := range graphNodes {
		graphNodes[index] = node
	}

	// Act and assert.
	boundary := mcp.NewToolResultStructured(boundaryPatch, "")
	if got := enforceContractResult("preview_concept_patch", boundary); got != boundary {
		t.Fatalf("diff boundary returned error: %#v", got.StructuredContent)
	}
	for _, test := range []struct {
		name       string
		tool       string
		structured any
	}{
		{name: "diff", tool: "preview_concept_patch", structured: overDiff},
		{name: "affected paths", tool: "preview_concept_patch", structured: overPaths},
		{name: "diagnostics", tool: "preview_concept_patch", structured: overDiagnostics},
		{name: "proof reads", tool: "preview_v02_migration", structured: overProof},
		{name: "graph nodes", tool: "get_semantic_graph", structured: map[string]any{
			"@context": map[string]any{}, "@graph": graphNodes,
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := enforceContractResult(test.tool, mcp.NewToolResultStructured(test.structured, ""))
			if !result.IsError {
				t.Fatal("over-limit output returned success")
			}
			envelope, ok := result.StructuredContent.(errorEnvelope)
			if !ok || envelope.Code != "resource_limit" {
				t.Fatalf("error envelope = %#v, want resource_limit", result.StructuredContent)
			}
		})
	}
}

func TestDiagnosticCodesAreNonEmptyAcrossSchemasAndProjections(t *testing.T) {
	// Arrange.
	entries, err := contractFS.ReadDir("contracts")
	if err != nil {
		t.Fatalf("ReadDir(contracts) error = %v", err)
	}
	fragments := 0
	var inspect func(*testing.T, string, any)
	inspect = func(t *testing.T, pointer string, value any) {
		t.Helper()
		switch typed := value.(type) {
		case map[string]any:
			required, _ := typed["required"].([]any)
			if containsSchemaRequired(required, "code") {
				properties, _ := typed["properties"].(map[string]any)
				code, _ := properties["code"].(map[string]any)
				minLength, hasMinLength := code["minLength"].(float64)
				enum, hasEnum := code["enum"].([]any)
				if !hasMinLength || minLength != 1 {
					if !hasEnum || enumContainsEmptyString(enum) {
						t.Errorf("%s code does not reject an empty string: %#v", pointer, code)
					}
				}
				fragments++
			}
			for key, child := range typed {
				inspect(t, pointer+"/"+key, child)
			}
		case []any:
			for index, child := range typed {
				inspect(t, pointer+"/"+strconv.Itoa(index), child)
			}
		}
	}
	for _, entry := range entries {
		data, err := contractFS.ReadFile(filepath.Join("contracts", entry.Name()))
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", entry.Name(), err)
		}
		var schema any
		if err := json.Unmarshal(data, &schema); err != nil {
			t.Fatalf("Unmarshal(%s) error = %v", entry.Name(), err)
		}
		inspect(t, entry.Name(), schema)
	}
	if fragments == 0 {
		t.Fatal("no diagnostic code fragments inspected")
	}

	// Act.
	storeDiagnostics, err := wireDiagnosticsFromStoreContext(t.Context(), []store.Diagnostic{{
		Severity: store.DiagnosticError, Code: "",
	}})
	if err != nil {
		t.Fatalf("wireDiagnosticsFromStoreContext() error = %v", err)
	}
	storeDiagnostic := storeDiagnostics[0]
	validatorDiagnostic := wireDiagnosticFromValidator("", validator.Diagnostic{
		Severity: validator.SeverityError, Code: "",
	})
	source := migrationSourceDTOFromDomain(mutation.MigrationSourceResolution{
		Blockers: []mutation.MigrationBlocker{{Code: ""}},
	})
	errorResult := stableToolError("", "failed", false)

	// Assert.
	for name, code := range map[string]string{
		"store diagnostic":     storeDiagnostic.Code,
		"validator diagnostic": validatorDiagnostic.Code,
		"migration blocker":    source.Blockers[0].Code,
		"error envelope":       errorResult.StructuredContent.(errorEnvelope).Code,
	} {
		if code != "operation_rejected" {
			t.Errorf("%s code = %q, want operation_rejected", name, code)
		}
	}
	if got := stableDiagnosticCodeOr("ambiguous_presentation", "invalid_change_set"); got != "ambiguous_presentation" {
		t.Errorf("valid InvalidChangeSet code = %q", got)
	}
	if got := stableDiagnosticCodeOr("INVALID CODE", "invalid_change_set"); got != "invalid_change_set" {
		t.Errorf("invalid InvalidChangeSet code = %q", got)
	}
}

func containsSchemaRequired(required []any, target string) bool {
	for _, item := range required {
		if item == target {
			return true
		}
	}
	return false
}

func enumContainsEmptyString(values []any) bool {
	for _, value := range values {
		if value == "" {
			return true
		}
	}
	return false
}
