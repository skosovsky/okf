package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/mutation"
	"github.com/skosovsky/okf/store"
	"github.com/skosovsky/okf/validator"
)

func TestPreviewApplyAndWriteHandlerProjectionStateSchemaParity(t *testing.T) {
	// Arrange.
	fixtures := outputStateContractFixtures(t)
	writeRejected, err := writeConceptRejectedContext(
		t.Context(), "", "a.md", validator.Report{},
		[]store.Diagnostic{{
			Code: "invalid_change_set", Severity: store.DiagnosticError, Message: "rejected",
		}},
	)
	if err != nil {
		t.Fatalf("writeConceptRejectedContext() error = %v", err)
	}
	structured := func(value any) *mcp.CallToolResult {
		return mcp.NewToolResultStructured(value, "")
	}
	cases := []struct {
		name   string
		tool   string
		result *mcp.CallToolResult
	}{
		{name: "patch applicable", tool: "preview_concept_patch", result: structured(fixtures.patchApplicable)},
		{name: "patch noop", tool: "preview_concept_patch", result: structured(fixtures.patchNoop)},
		{name: "patch rejected", tool: "preview_concept_patch", result: structured(fixtures.patchRejected)},
		{name: "migration applicable", tool: "preview_v02_migration", result: structured(fixtures.migrationApplicable)},
		{name: "migration planned noop", tool: "preview_v02_migration", result: structured(fixtures.migrationPlannedNoop)},
		{name: "migration target noop", tool: "preview_v02_migration", result: structured(fixtures.migrationTargetNoop)},
		{name: "migration blocked", tool: "preview_v02_migration", result: structured(fixtures.migrationBlocked)},
		{name: "patch applied", tool: "apply_concept_patch", result: structured(fixtures.patchApplied)},
		{name: "patch noop", tool: "apply_concept_patch", result: structured(fixtures.patchApplyNoop)},
		{name: "patch rejected", tool: "apply_concept_patch", result: structured(fixtures.patchApplyRejected)},
		{name: "migration applied", tool: "apply_v02_migration", result: structured(fixtures.migrationApplied)},
		{name: "migration transition noop", tool: "apply_v02_migration", result: structured(fixtures.migrationTransitionNoop)},
		{name: "migration target noop", tool: "apply_v02_migration", result: structured(fixtures.migrationApplyTargetNoop)},
		{name: "migration rejected", tool: "apply_v02_migration", result: structured(fixtures.migrationApplyRejected)},
		{name: "write success", tool: "write_concept", result: structured(fixtures.writeSuccess)},
		{
			name: "write rejection error envelope", tool: "write_concept",
			result: jsonErrorResult(writeRejected),
		},
	}

	// Act and assert.
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := enforceContractResult(test.tool, test.result); got != test.result {
				t.Fatalf("handler projection violates %s output state schema: %#v", test.tool, got.StructuredContent)
			}
		})
	}
}

func TestApplyAndWriteRealHandlersMatchPublishedOutputStateMachines(t *testing.T) {
	// Arrange.
	call := func(tool string, handler func(
		context.Context,
		mcp.CallToolRequest,
	) (*mcp.CallToolResult, error), arguments map[string]any) *mcp.CallToolResult {
		t.Helper()
		return callHandler(t, contractToolHandler(tool, handler), arguments)
	}
	assertSuccess := func(tool, status string, result *mcp.CallToolResult) map[string]any {
		t.Helper()
		if result.IsError {
			t.Fatalf("%s %s returned error: %s", tool, status, resultText(t, result))
		}
		value := outputStateMap(t, result.StructuredContent)
		if value["status"] != status {
			t.Fatalf("%s status = %#v, want %q", tool, value["status"], status)
		}
		if _, exists := value["receipt"]; exists {
			t.Fatalf("%s exposed durable receipt: %#v", tool, value)
		}
		if _, exists := value["proof"]; exists {
			t.Fatalf("%s exposed authorization proof: %#v", tool, value)
		}
		var fallback map[string]any
		if err := json.Unmarshal([]byte(resultText(t, result)), &fallback); err != nil {
			t.Fatalf("Unmarshal(%s text fallback) error = %v", tool, err)
		}
		if !reflect.DeepEqual(fallback, value) {
			t.Fatalf("%s structured/text fallback mismatch:\nstructured=%#v\ntext=%#v", tool, value, fallback)
		}
		return value
	}
	assertError := func(tool string, result *mcp.CallToolResult) {
		t.Helper()
		if !result.IsError {
			t.Fatalf("%s returned success: %#v", tool, result.StructuredContent)
		}
		if err := validateContract("error", result.StructuredContent); err != nil {
			t.Fatalf("%s error envelope violates contract: %v", tool, err)
		}
		if err := validateContract(tool+".output", result.StructuredContent); err == nil {
			t.Fatalf("%s error envelope also satisfies its success schema", tool)
		}
	}

	patchOperation := map[string]any{
		"kind": "set_generated", "concept_id": "a",
		"generated": map[string]any{"by": "human:reviewer"},
	}
	newPatchRoot := func(version string) string {
		t.Helper()
		root := t.TempDir()
		writeTestFile(t, root, "index.md",
			"---\nokf_version: \""+version+"\"\n---\n# Notes\n\n- [A](a.md)\n",
		)
		writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nA.\n")
		return root
	}
	patchArguments := func(root string) map[string]any {
		return map[string]any{
			"bundle_path": root, "actor": "mcp:output-state",
			"operations": []any{patchOperation},
		}
	}
	patchRoot := newPatchRoot("0.2")
	patchPreview := call(
		"preview_concept_patch", handlePreviewConceptPatch, patchArguments(patchRoot),
	)
	patchPlan := patchPreview.StructuredContent.(patchPreviewResponse)
	patchApply := patchArguments(patchRoot)
	patchApply["expected_revision"] = patchPlan.BaseRevision
	patchApply["expected_plan_digest"] = patchPlan.PlanDigest

	patchNoopRoot := newPatchRoot("0.2")
	writeTestFile(t, patchNoopRoot, "a.md",
		"---\ntype: Note\ngenerated:\n  by: human:reviewer\n---\nA.\n",
	)
	patchNoopPreview := call(
		"preview_concept_patch", handlePreviewConceptPatch, patchArguments(patchNoopRoot),
	)
	patchNoopPlan := patchNoopPreview.StructuredContent.(patchPreviewResponse)
	patchNoopApply := patchArguments(patchNoopRoot)
	patchNoopApply["expected_revision"] = patchNoopPlan.BaseRevision
	patchNoopApply["expected_plan_digest"] = patchNoopPlan.PlanDigest

	rejectedPatchRoot := newPatchRoot("0.1")
	rejectedPatchPreview := call(
		"preview_concept_patch", handlePreviewConceptPatch, patchArguments(rejectedPatchRoot),
	)
	rejectedPatchPlan := rejectedPatchPreview.StructuredContent.(patchPreviewResponse)
	rejectedPatchApply := patchArguments(rejectedPatchRoot)
	rejectedPatchApply["expected_revision"] = rejectedPatchPlan.BaseRevision
	rejectedPatchApply["expected_plan_digest"] = rejectedPatchPlan.PlanDigest

	errorPatchRoot := newPatchRoot("0.2")
	errorPatchPreview := call(
		"preview_concept_patch", handlePreviewConceptPatch, patchArguments(errorPatchRoot),
	)
	errorPatchPlan := errorPatchPreview.StructuredContent.(patchPreviewResponse)
	errorPatchApply := patchArguments(errorPatchRoot)
	errorPatchApply["expected_revision"] = errorPatchPlan.BaseRevision
	errorPatchApply["expected_plan_digest"] = outputStateRevision("f")

	transitionArguments := func(root string) map[string]any {
		return map[string]any{
			"bundle_path": root, "from": "0.1",
			"timestamp_policy": "preserve", "citation_mappings": []any{},
		}
	}
	transitionNoopRoot := t.TempDir()
	writeTestFile(t, transitionNoopRoot, "a.md", "---\ntype: Note\n---\nA.\n")
	transitionPreview := call(
		"preview_v02_migration", handlePreviewV02Migration,
		transitionArguments(transitionNoopRoot),
	)
	transitionPlan := transitionPreview.StructuredContent.(migrationPreviewResponse)
	transitionApply := transitionArguments(transitionNoopRoot)
	transitionApply["expected_source"] = transitionPlan.Source
	transitionApply["proof"] = transitionPlan.Proof
	transitionApply["expected_plan_digest"] = transitionPlan.PlanDigest
	transitionDigestError := cloneArguments(transitionApply)
	transitionDigestError["expected_plan_digest"] = outputStateRevision("f")

	migrationRoot := t.TempDir()
	writeTestFile(t, migrationRoot, "index.md",
		"---\nokf_version: \"0.1\"\n---\n# Notes\n\n- [A](a.md)\n",
	)
	writeTestFile(t, migrationRoot, "a.md",
		"---\ntype: Note\ntimestamp: 2026-07-29T00:00:00Z\n---\nA.\n",
	)
	migrationArguments := map[string]any{
		"bundle_path": migrationRoot, "generated_by": "human:reviewer",
		"timestamp_policy": "remove_after_copy", "timestamp_conflict_policy": "reject",
		"citation_mappings": []any{},
	}
	migrationPreview := call(
		"preview_v02_migration", handlePreviewV02Migration, migrationArguments,
	)
	migrationPlan := migrationPreview.StructuredContent.(migrationPreviewResponse)
	migrationApply := cloneArguments(migrationArguments)
	migrationApply["expected_source"] = migrationPlan.Source
	migrationApply["proof"] = migrationPlan.Proof
	migrationApply["expected_plan_digest"] = migrationPlan.PlanDigest

	targetRoot := newPatchRoot("0.2")
	targetArguments := map[string]any{
		"bundle_path": targetRoot, "timestamp_policy": "preserve",
		"citation_mappings": []any{},
	}
	targetPreview := call(
		"preview_v02_migration", handlePreviewV02Migration, targetArguments,
	)
	targetPlan := targetPreview.StructuredContent.(migrationPreviewResponse)
	targetApply := cloneArguments(targetArguments)
	targetApply["expected_source"] = targetPlan.Source

	writeRoot := newPatchRoot("0.2")
	writeArguments := map[string]any{
		"bundle_path": writeRoot, "concept_id": "new",
		"frontmatter": "title: Missing Type\n", "body": "Body.\n",
	}

	// Act.
	patchApplied := call("apply_concept_patch", handleApplyConceptPatch, patchApply)
	patchNoop := call("apply_concept_patch", handleApplyConceptPatch, patchNoopApply)
	patchRejected := call("apply_concept_patch", handleApplyConceptPatch, rejectedPatchApply)
	patchError := call("apply_concept_patch", handleApplyConceptPatch, errorPatchApply)
	migrationError := call("apply_v02_migration", handleApplyV02Migration, transitionDigestError)
	migrationTransitionNoop := call(
		"apply_v02_migration", handleApplyV02Migration, transitionApply,
	)
	migrationApplied := call("apply_v02_migration", handleApplyV02Migration, migrationApply)
	migrationTargetNoop := call("apply_v02_migration", handleApplyV02Migration, targetApply)
	writeRejection := call("write_concept", handleWriteConcept, writeArguments)

	// Assert.
	assertSuccess("apply_concept_patch", "applied", patchApplied)
	assertSuccess("apply_concept_patch", "noop", patchNoop)
	assertSuccess("apply_concept_patch", "rejected", patchRejected)
	assertError("apply_concept_patch", patchError)
	assertError("apply_v02_migration", migrationError)
	assertSuccess("apply_v02_migration", "noop", migrationTransitionNoop)
	assertSuccess("apply_v02_migration", "applied", migrationApplied)
	assertSuccess("apply_v02_migration", "noop", migrationTargetNoop)
	assertError("write_concept", writeRejection)
}

func TestMutationOutputPathsOwnedBoundary(t *testing.T) {
	// Arrange.
	fixtures := outputStateContractFixtures(t)
	paths := make([]any, maxChangedPaths)
	for index := range paths {
		paths[index] = fmt.Sprintf("concepts/%05d.md", index)
	}
	tests := []struct {
		tool     string
		contract string
		field    string
		base     any
	}{
		{
			tool: "preview_concept_patch", contract: "preview_concept_patch.output",
			field: "affected_paths", base: fixtures.patchApplicable,
		},
		{
			tool: "preview_v02_migration", contract: "preview_v02_migration.output",
			field: "affected_paths", base: fixtures.migrationApplicable,
		},
		{
			tool: "apply_concept_patch", contract: "apply_concept_patch.output",
			field: "changed_paths", base: fixtures.patchApplied,
		},
		{
			tool: "apply_v02_migration", contract: "apply_v02_migration.output",
			field: "changed_paths", base: fixtures.migrationApplied,
		},
	}

	// Act and assert.
	for _, test := range tests {
		t.Run(test.tool, func(t *testing.T) {
			boundary := outputStateMap(t, test.base)
			boundary[test.field] = paths
			result := mcp.NewToolResultStructured(boundary, "")
			if err := validateContract(test.contract, boundary); err != nil {
				t.Fatalf("owned path boundary rejected: %v", err)
			}
			if got := enforceContractResult(test.tool, result); got != result {
				t.Fatalf("owned path boundary violates output invariant: %#v", got.StructuredContent)
			}

			over := outputStateMap(t, boundary)
			over[test.field] = append(over[test.field].([]any), "concepts/10000.md")
			if err := validateContract(test.contract, over); err == nil {
				t.Fatal("owned path boundary+1 accepted")
			}
		})
	}
}

func TestPreviewOutputRuntimeInvariantRejectsUnreachableStates(t *testing.T) {
	// Arrange.
	fixtures := outputStateContractFixtures(t)
	tests := []struct {
		name         string
		tool         string
		contract     string
		base         any
		schemaAdmits bool
		mutate       func(map[string]any)
	}{
		{
			name: "patch applicable same revision", tool: "preview_concept_patch",
			contract: "preview_concept_patch.output", base: fixtures.patchApplicable,
			schemaAdmits: true,
			mutate: func(value map[string]any) {
				value["result_revision"] = value["base_revision"]
			},
		},
		{
			name: "patch applicable null revision", tool: "preview_concept_patch",
			contract: "preview_concept_patch.output", base: fixtures.patchApplicable,
			mutate: func(value map[string]any) {
				value["result_revision"] = nil
			},
		},
		{
			name: "patch applicable empty paths", tool: "preview_concept_patch",
			contract: "preview_concept_patch.output", base: fixtures.patchApplicable,
			mutate: func(value map[string]any) {
				value["affected_paths"] = []any{}
			},
		},
		{
			name: "patch applicable duplicate paths", tool: "preview_concept_patch",
			contract: "preview_concept_patch.output", base: fixtures.patchApplicable,
			mutate: func(value map[string]any) {
				value["affected_paths"] = []any{"a.md", "a.md"}
			},
		},
		{
			name: "patch applicable unsorted paths", tool: "preview_concept_patch",
			contract: "preview_concept_patch.output", base: fixtures.patchApplicable,
			schemaAdmits: true,
			mutate: func(value map[string]any) {
				value["affected_paths"] = []any{"z.md", "a.md"}
			},
		},
		{
			name: "patch applicable noncanonical path", tool: "preview_concept_patch",
			contract: "preview_concept_patch.output", base: fixtures.patchApplicable,
			schemaAdmits: true,
			mutate: func(value map[string]any) {
				value["affected_paths"] = []any{"../a.md"}
			},
		},
		{
			name: "patch noop different revision", tool: "preview_concept_patch",
			contract: "preview_concept_patch.output", base: fixtures.patchNoop,
			schemaAdmits: true,
			mutate: func(value map[string]any) {
				value["result_revision"] = outputStateRevision("c")
			},
		},
		{
			name: "patch noop null revision", tool: "preview_concept_patch",
			contract: "preview_concept_patch.output", base: fixtures.patchNoop,
			mutate: func(value map[string]any) {
				value["result_revision"] = nil
			},
		},
		{
			name: "patch noop affected path", tool: "preview_concept_patch",
			contract: "preview_concept_patch.output", base: fixtures.patchNoop,
			mutate: func(value map[string]any) {
				value["affected_paths"] = []any{"a.md"}
			},
		},
		{
			name: "patch rejected result revision", tool: "preview_concept_patch",
			contract: "preview_concept_patch.output", base: fixtures.patchRejected,
			mutate: func(value map[string]any) {
				value["result_revision"] = value["base_revision"]
			},
		},
		{
			name: "migration applicable same revision", tool: "preview_v02_migration",
			contract: "preview_v02_migration.output", base: fixtures.migrationApplicable,
			schemaAdmits: true,
			mutate: func(value map[string]any) {
				value["result_revision"] = value["base_revision"]
			},
		},
		{
			name: "migration applicable empty paths", tool: "preview_v02_migration",
			contract: "preview_v02_migration.output", base: fixtures.migrationApplicable,
			mutate: func(value map[string]any) {
				value["affected_paths"] = []any{}
			},
		},
		{
			name: "migration applicable duplicate paths", tool: "preview_v02_migration",
			contract: "preview_v02_migration.output", base: fixtures.migrationApplicable,
			mutate: func(value map[string]any) {
				value["affected_paths"] = []any{"a.md", "a.md"}
			},
		},
		{
			name: "migration applicable unsorted paths", tool: "preview_v02_migration",
			contract: "preview_v02_migration.output", base: fixtures.migrationApplicable,
			schemaAdmits: true,
			mutate: func(value map[string]any) {
				value["affected_paths"] = []any{"z.md", "a.md"}
			},
		},
		{
			name: "migration applicable noncanonical path", tool: "preview_v02_migration",
			contract: "preview_v02_migration.output", base: fixtures.migrationApplicable,
			schemaAdmits: true,
			mutate: func(value map[string]any) {
				value["affected_paths"] = []any{"/a.md"}
			},
		},
		{
			name: "migration noop different revision", tool: "preview_v02_migration",
			contract: "preview_v02_migration.output", base: fixtures.migrationPlannedNoop,
			schemaAdmits: true,
			mutate: func(value map[string]any) {
				value["result_revision"] = outputStateRevision("c")
			},
		},
		{
			name: "migration noop null revision", tool: "preview_v02_migration",
			contract: "preview_v02_migration.output", base: fixtures.migrationPlannedNoop,
			mutate: func(value map[string]any) {
				value["result_revision"] = nil
			},
		},
		{
			name: "migration noop affected path", tool: "preview_v02_migration",
			contract: "preview_v02_migration.output", base: fixtures.migrationPlannedNoop,
			mutate: func(value map[string]any) {
				value["affected_paths"] = []any{"a.md"}
			},
		},
		{
			name: "migration blocked result revision", tool: "preview_v02_migration",
			contract: "preview_v02_migration.output", base: fixtures.migrationBlocked,
			mutate: func(value map[string]any) {
				value["result_revision"] = value["base_revision"]
			},
		},
		{
			name: "migration blocked affected path", tool: "preview_v02_migration",
			contract: "preview_v02_migration.output", base: fixtures.migrationBlocked,
			mutate: func(value map[string]any) {
				value["affected_paths"] = []any{"a.md"}
			},
		},
	}

	// Act and assert.
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := outputStateMap(t, test.base)
			test.mutate(value)
			if err := validateMutationOutputState(test.tool, value); err == nil {
				t.Fatalf("%s runtime invariant accepted unreachable state: %#v", test.tool, value)
			}
			if !test.schemaAdmits {
				return
			}
			if err := validateContract(test.contract, value); err != nil {
				t.Fatalf("schema/runtime boundary fixture was rejected by schema: %v", err)
			}
			result := mcp.NewToolResultStructured(value, "")
			if enforceContractResult(test.tool, result) == result {
				t.Fatalf("%s schema-valid unreachable state crossed runtime boundary", test.tool)
			}
		})
	}
}

func TestPreviewApplyAndWriteOutputContractsRejectUnreachableStates(t *testing.T) {
	// Arrange.
	fixtures := outputStateContractFixtures(t)
	blocker := outputStateMap(t, fixtures.migrationBlocked)["blockers"].([]any)[0]
	manualAction := map[string]any{
		"code": "provide_generated_by", "path": "a.md", "message": "provide an actor",
	}
	proof := outputStateMap(t, fixtures.migrationApplicable)["proof"]
	warning := map[string]any{
		"code": "warning", "severity": "WARN", "file": "", "field": "",
		"message": "warning", "spec_ref": "", "policy_failure": false,
	}
	cases := []struct {
		name     string
		contract string
		base     any
		mutate   func(map[string]any)
	}{
		{
			name: "patch rejected result revision", contract: "preview_concept_patch.output",
			base: fixtures.patchRejected,
			mutate: func(value map[string]any) {
				value["result_revision"] = outputStateRevision("c")
			},
		},
		{
			name: "patch rejected diff", contract: "preview_concept_patch.output",
			base: fixtures.patchRejected,
			mutate: func(value map[string]any) {
				value["diff"] = "unexpected"
			},
		},
		{
			name: "patch rejected truncated diff", contract: "preview_concept_patch.output",
			base: fixtures.patchRejected,
			mutate: func(value map[string]any) {
				value["diff_truncated"] = true
			},
		},
		{
			name: "patch rejected affected path", contract: "preview_concept_patch.output",
			base: fixtures.patchRejected,
			mutate: func(value map[string]any) {
				value["affected_paths"] = []any{"a.md"}
			},
		},
		{
			name: "patch rejected without diagnostic", contract: "preview_concept_patch.output",
			base: fixtures.patchRejected,
			mutate: func(value map[string]any) {
				value["diagnostics"] = []any{}
			},
		},
		{
			name: "patch rejected claims unknown preservation", contract: "preview_concept_patch.output",
			base: fixtures.patchRejected,
			mutate: func(value map[string]any) {
				value["unknown_preserved"] = true
			},
		},
		{
			name: "patch applicable null result", contract: "preview_concept_patch.output",
			base: fixtures.patchApplicable,
			mutate: func(value map[string]any) {
				value["result_revision"] = nil
			},
		},
		{
			name: "patch applicable empty diff", contract: "preview_concept_patch.output",
			base: fixtures.patchApplicable,
			mutate: func(value map[string]any) {
				value["diff"] = ""
			},
		},
		{
			name: "patch applicable without affected path", contract: "preview_concept_patch.output",
			base: fixtures.patchApplicable,
			mutate: func(value map[string]any) {
				value["affected_paths"] = []any{}
			},
		},
		{
			name: "patch applicable drops unknown fields", contract: "preview_concept_patch.output",
			base: fixtures.patchApplicable,
			mutate: func(value map[string]any) {
				value["unknown_preserved"] = false
			},
		},
		{
			name: "patch noop null result", contract: "preview_concept_patch.output",
			base: fixtures.patchNoop,
			mutate: func(value map[string]any) {
				value["result_revision"] = nil
			},
		},
		{
			name: "patch noop diff", contract: "preview_concept_patch.output",
			base: fixtures.patchNoop,
			mutate: func(value map[string]any) {
				value["diff"] = "unexpected"
			},
		},
		{
			name: "patch noop truncated diff", contract: "preview_concept_patch.output",
			base: fixtures.patchNoop,
			mutate: func(value map[string]any) {
				value["diff_truncated"] = true
			},
		},
		{
			name: "patch noop affected path", contract: "preview_concept_patch.output",
			base: fixtures.patchNoop,
			mutate: func(value map[string]any) {
				value["affected_paths"] = []any{"a.md"}
			},
		},
		{
			name: "patch noop drops unknown fields", contract: "preview_concept_patch.output",
			base: fixtures.patchNoop,
			mutate: func(value map[string]any) {
				value["unknown_preserved"] = false
			},
		},
		{
			name: "migration blocked result revision", contract: "preview_v02_migration.output",
			base: fixtures.migrationBlocked,
			mutate: func(value map[string]any) {
				value["result_revision"] = outputStateRevision("c")
			},
		},
		{
			name: "migration blocked plan digest", contract: "preview_v02_migration.output",
			base: fixtures.migrationBlocked,
			mutate: func(value map[string]any) {
				value["plan_digest"] = outputStateRevision("d")
			},
		},
		{
			name: "migration blocked proof", contract: "preview_v02_migration.output",
			base: fixtures.migrationBlocked,
			mutate: func(value map[string]any) {
				value["proof"] = proof
			},
		},
		{
			name: "migration blocked affected path", contract: "preview_v02_migration.output",
			base: fixtures.migrationBlocked,
			mutate: func(value map[string]any) {
				value["affected_paths"] = []any{"a.md"}
			},
		},
		{
			name: "migration blocked diff", contract: "preview_v02_migration.output",
			base: fixtures.migrationBlocked,
			mutate: func(value map[string]any) {
				value["diff"] = "unexpected"
			},
		},
		{
			name: "migration blocked truncated diff", contract: "preview_v02_migration.output",
			base: fixtures.migrationBlocked,
			mutate: func(value map[string]any) {
				value["diff_truncated"] = true
			},
		},
		{
			name: "migration blocked without blocker", contract: "preview_v02_migration.output",
			base: fixtures.migrationBlocked,
			mutate: func(value map[string]any) {
				value["blockers"] = []any{}
			},
		},
		{
			name: "migration applicable null result", contract: "preview_v02_migration.output",
			base: fixtures.migrationApplicable,
			mutate: func(value map[string]any) {
				value["result_revision"] = nil
			},
		},
		{
			name: "migration applicable empty plan digest", contract: "preview_v02_migration.output",
			base: fixtures.migrationApplicable,
			mutate: func(value map[string]any) {
				value["plan_digest"] = ""
			},
		},
		{
			name: "migration applicable without proof", contract: "preview_v02_migration.output",
			base: fixtures.migrationApplicable,
			mutate: func(value map[string]any) {
				delete(value, "proof")
			},
		},
		{
			name: "migration applicable without affected path", contract: "preview_v02_migration.output",
			base: fixtures.migrationApplicable,
			mutate: func(value map[string]any) {
				value["affected_paths"] = []any{}
			},
		},
		{
			name: "migration applicable empty diff", contract: "preview_v02_migration.output",
			base: fixtures.migrationApplicable,
			mutate: func(value map[string]any) {
				value["diff"] = ""
			},
		},
		{
			name: "migration applicable blocker", contract: "preview_v02_migration.output",
			base: fixtures.migrationApplicable,
			mutate: func(value map[string]any) {
				value["blockers"] = []any{blocker}
			},
		},
		{
			name: "migration applicable manual action", contract: "preview_v02_migration.output",
			base: fixtures.migrationApplicable,
			mutate: func(value map[string]any) {
				value["manual_actions"] = []any{manualAction}
			},
		},
		{
			name: "migration applicable target transition", contract: "preview_v02_migration.output",
			base: fixtures.migrationApplicable,
			mutate: func(value map[string]any) {
				outputStateSource(value)["transition"] = "target-noop"
			},
		},
		{
			name: "migration target noop plan digest", contract: "preview_v02_migration.output",
			base: fixtures.migrationTargetNoop,
			mutate: func(value map[string]any) {
				value["plan_digest"] = outputStateRevision("d")
			},
		},
		{
			name: "migration target noop proof", contract: "preview_v02_migration.output",
			base: fixtures.migrationTargetNoop,
			mutate: func(value map[string]any) {
				value["proof"] = proof
			},
		},
		{
			name: "migration target noop source transition", contract: "preview_v02_migration.output",
			base: fixtures.migrationTargetNoop,
			mutate: func(value map[string]any) {
				outputStateSource(value)["transition"] = "v0.1-to-v0.2"
			},
		},
		{
			name: "migration target noop affected path", contract: "preview_v02_migration.output",
			base: fixtures.migrationTargetNoop,
			mutate: func(value map[string]any) {
				value["affected_paths"] = []any{"a.md"}
			},
		},
		{
			name: "migration target noop diff", contract: "preview_v02_migration.output",
			base: fixtures.migrationTargetNoop,
			mutate: func(value map[string]any) {
				value["diff"] = "unexpected"
			},
		},
		{
			name: "migration target noop truncated diff", contract: "preview_v02_migration.output",
			base: fixtures.migrationTargetNoop,
			mutate: func(value map[string]any) {
				value["diff_truncated"] = true
			},
		},
		{
			name: "migration target noop blocker", contract: "preview_v02_migration.output",
			base: fixtures.migrationTargetNoop,
			mutate: func(value map[string]any) {
				value["blockers"] = []any{blocker}
			},
		},
		{
			name: "migration target noop manual action", contract: "preview_v02_migration.output",
			base: fixtures.migrationTargetNoop,
			mutate: func(value map[string]any) {
				value["manual_actions"] = []any{manualAction}
			},
		},
		{
			name: "migration planned noop without proof", contract: "preview_v02_migration.output",
			base: fixtures.migrationPlannedNoop,
			mutate: func(value map[string]any) {
				delete(value, "proof")
			},
		},
		{
			name: "migration planned noop empty plan digest", contract: "preview_v02_migration.output",
			base: fixtures.migrationPlannedNoop,
			mutate: func(value map[string]any) {
				value["plan_digest"] = ""
			},
		},
		{
			name: "migration planned noop target transition", contract: "preview_v02_migration.output",
			base: fixtures.migrationPlannedNoop,
			mutate: func(value map[string]any) {
				outputStateSource(value)["transition"] = "target-noop"
			},
		},
		{
			name: "migration planned noop affected path", contract: "preview_v02_migration.output",
			base: fixtures.migrationPlannedNoop,
			mutate: func(value map[string]any) {
				value["affected_paths"] = []any{"a.md"}
			},
		},
		{
			name: "migration planned noop diff", contract: "preview_v02_migration.output",
			base: fixtures.migrationPlannedNoop,
			mutate: func(value map[string]any) {
				value["diff"] = "unexpected"
			},
		},
		{
			name: "migration planned noop truncated diff", contract: "preview_v02_migration.output",
			base: fixtures.migrationPlannedNoop,
			mutate: func(value map[string]any) {
				value["diff_truncated"] = true
			},
		},
		{
			name: "migration planned noop blocker", contract: "preview_v02_migration.output",
			base: fixtures.migrationPlannedNoop,
			mutate: func(value map[string]any) {
				value["blockers"] = []any{blocker}
			},
		},
		{
			name: "migration planned noop manual action", contract: "preview_v02_migration.output",
			base: fixtures.migrationPlannedNoop,
			mutate: func(value map[string]any) {
				value["manual_actions"] = []any{manualAction}
			},
		},
		{
			name: "patch applied without changed receipt path", contract: "apply_concept_patch.output",
			base: fixtures.patchApplied,
			mutate: func(value map[string]any) {
				value["changed_paths"] = []any{}
			},
		},
		{
			name: "patch applied same revision", contract: "apply_concept_patch.output",
			base: fixtures.patchApplied,
			mutate: func(value map[string]any) {
				value["result_revision"] = value["base_revision"]
			},
		},
		{
			name: "patch applied diagnostic", contract: "apply_concept_patch.output",
			base: fixtures.patchApplied,
			mutate: func(value map[string]any) {
				value["diagnostics"] = []any{warning}
			},
		},
		{
			name: "patch applied empty digest", contract: "apply_concept_patch.output",
			base: fixtures.patchApplied,
			mutate: func(value map[string]any) {
				value["plan_digest"] = ""
			},
		},
		{
			name: "patch applied duplicate changed receipt path", contract: "apply_concept_patch.output",
			base: fixtures.patchApplied,
			mutate: func(value map[string]any) {
				value["changed_paths"] = []any{"a.md", "a.md"}
			},
		},
		{
			name: "patch applied unsorted changed receipt paths", contract: "apply_concept_patch.output",
			base: fixtures.patchApplied,
			mutate: func(value map[string]any) {
				value["changed_paths"] = []any{"z.md", "a.md"}
			},
		},
		{
			name: "patch applied empty changed receipt path", contract: "apply_concept_patch.output",
			base: fixtures.patchApplied,
			mutate: func(value map[string]any) {
				value["changed_paths"] = []any{""}
			},
		},
		{
			name: "patch applied noncanonical changed receipt path", contract: "apply_concept_patch.output",
			base: fixtures.patchApplied,
			mutate: func(value map[string]any) {
				value["changed_paths"] = []any{"../a.md"}
			},
		},
		{
			name: "patch noop changed receipt path", contract: "apply_concept_patch.output",
			base: fixtures.patchApplyNoop,
			mutate: func(value map[string]any) {
				value["changed_paths"] = []any{"a.md"}
			},
		},
		{
			name: "patch noop different revision", contract: "apply_concept_patch.output",
			base: fixtures.patchApplyNoop,
			mutate: func(value map[string]any) {
				value["result_revision"] = outputStateRevision("c")
			},
		},
		{
			name: "patch noop diagnostic", contract: "apply_concept_patch.output",
			base: fixtures.patchApplyNoop,
			mutate: func(value map[string]any) {
				value["diagnostics"] = []any{warning}
			},
		},
		{
			name: "patch rejected without diagnostic", contract: "apply_concept_patch.output",
			base: fixtures.patchApplyRejected,
			mutate: func(value map[string]any) {
				value["diagnostics"] = []any{}
			},
		},
		{
			name: "patch rejected different revision", contract: "apply_concept_patch.output",
			base: fixtures.patchApplyRejected,
			mutate: func(value map[string]any) {
				value["result_revision"] = outputStateRevision("c")
			},
		},
		{
			name: "patch rejected changed receipt path", contract: "apply_concept_patch.output",
			base: fixtures.patchApplyRejected,
			mutate: func(value map[string]any) {
				value["changed_paths"] = []any{"a.md"}
			},
		},
		{
			name: "patch output leaks durable receipt", contract: "apply_concept_patch.output",
			base: fixtures.patchApplied,
			mutate: func(value map[string]any) {
				value["receipt"] = map[string]any{"result_revision": value["result_revision"]}
			},
		},
		{
			name: "patch output leaks migration proof", contract: "apply_concept_patch.output",
			base: fixtures.patchApplied,
			mutate: func(value map[string]any) {
				value["proof"] = proof
			},
		},
		{
			name: "patch error envelope presented as success", contract: "apply_concept_patch.output",
			base: fixtures.patchApplied,
			mutate: func(value map[string]any) {
				replaceOutputStateMap(value, map[string]any{
					"code": "plan_mismatch", "message": "mismatch",
					"retryable": false, "diagnostics": []any{},
				})
			},
		},
		{
			name: "migration applied without changed receipt path", contract: "apply_v02_migration.output",
			base: fixtures.migrationApplied,
			mutate: func(value map[string]any) {
				value["changed_paths"] = []any{}
			},
		},
		{
			name: "migration applied same revision", contract: "apply_v02_migration.output",
			base: fixtures.migrationApplied,
			mutate: func(value map[string]any) {
				value["result_revision"] = value["base_revision"]
			},
		},
		{
			name: "migration applied target transition", contract: "apply_v02_migration.output",
			base: fixtures.migrationApplied,
			mutate: func(value map[string]any) {
				source := outputStateSource(value)
				source["resolved_source"] = "0.2"
				source["from_version"] = "0.2"
				source["to_version"] = "0.2"
				source["transition"] = "target-noop"
			},
		},
		{
			name: "migration applied empty digest", contract: "apply_v02_migration.output",
			base: fixtures.migrationApplied,
			mutate: func(value map[string]any) {
				value["plan_digest"] = ""
			},
		},
		{
			name: "migration applied diagnostic", contract: "apply_v02_migration.output",
			base: fixtures.migrationApplied,
			mutate: func(value map[string]any) {
				value["diagnostics"] = []any{warning}
			},
		},
		{
			name: "migration applied unsorted changed receipt paths", contract: "apply_v02_migration.output",
			base: fixtures.migrationApplied,
			mutate: func(value map[string]any) {
				value["changed_paths"] = []any{"z.md", "a.md"}
			},
		},
		{
			name: "migration applied duplicate changed receipt path", contract: "apply_v02_migration.output",
			base: fixtures.migrationApplied,
			mutate: func(value map[string]any) {
				value["changed_paths"] = []any{"a.md", "a.md"}
			},
		},
		{
			name: "migration applied noncanonical changed receipt path", contract: "apply_v02_migration.output",
			base: fixtures.migrationApplied,
			mutate: func(value map[string]any) {
				value["changed_paths"] = []any{"/a.md"}
			},
		},
		{
			name: "migration transition noop empty digest", contract: "apply_v02_migration.output",
			base: fixtures.migrationTransitionNoop,
			mutate: func(value map[string]any) {
				value["plan_digest"] = ""
			},
		},
		{
			name: "migration transition noop different revision", contract: "apply_v02_migration.output",
			base: fixtures.migrationTransitionNoop,
			mutate: func(value map[string]any) {
				value["result_revision"] = outputStateRevision("c")
			},
		},
		{
			name: "migration transition noop changed receipt path", contract: "apply_v02_migration.output",
			base: fixtures.migrationTransitionNoop,
			mutate: func(value map[string]any) {
				value["changed_paths"] = []any{"a.md"}
			},
		},
		{
			name: "migration transition noop error diagnostic", contract: "apply_v02_migration.output",
			base: fixtures.migrationTransitionNoop,
			mutate: func(value map[string]any) {
				diagnostic := outputStateMap(t, fixtures.migrationApplyRejected)["diagnostics"].([]any)[0]
				value["diagnostics"] = []any{diagnostic}
			},
		},
		{
			name: "migration target noop digest", contract: "apply_v02_migration.output",
			base: fixtures.migrationApplyTargetNoop,
			mutate: func(value map[string]any) {
				value["plan_digest"] = outputStateRevision("d")
			},
		},
		{
			name: "migration target noop changed receipt path", contract: "apply_v02_migration.output",
			base: fixtures.migrationApplyTargetNoop,
			mutate: func(value map[string]any) {
				value["changed_paths"] = []any{"a.md"}
			},
		},
		{
			name: "migration target noop different revision", contract: "apply_v02_migration.output",
			base: fixtures.migrationApplyTargetNoop,
			mutate: func(value map[string]any) {
				value["result_revision"] = outputStateRevision("c")
			},
		},
		{
			name: "migration target noop diagnostic", contract: "apply_v02_migration.output",
			base: fixtures.migrationApplyTargetNoop,
			mutate: func(value map[string]any) {
				value["diagnostics"] = []any{warning}
			},
		},
		{
			name: "migration target noop proof", contract: "apply_v02_migration.output",
			base: fixtures.migrationApplyTargetNoop,
			mutate: func(value map[string]any) {
				value["proof"] = proof
			},
		},
		{
			name: "migration rejected target transition", contract: "apply_v02_migration.output",
			base: fixtures.migrationApplyRejected,
			mutate: func(value map[string]any) {
				source := outputStateSource(value)
				source["resolved_source"] = "0.2"
				source["from_version"] = "0.2"
				source["to_version"] = "0.2"
				source["transition"] = "target-noop"
			},
		},
		{
			name: "migration rejected without error diagnostic", contract: "apply_v02_migration.output",
			base: fixtures.migrationApplyRejected,
			mutate: func(value map[string]any) {
				value["diagnostics"] = []any{warning}
			},
		},
		{
			name: "migration rejected different revision", contract: "apply_v02_migration.output",
			base: fixtures.migrationApplyRejected,
			mutate: func(value map[string]any) {
				value["result_revision"] = outputStateRevision("c")
			},
		},
		{
			name: "migration rejected changed receipt path", contract: "apply_v02_migration.output",
			base: fixtures.migrationApplyRejected,
			mutate: func(value map[string]any) {
				value["changed_paths"] = []any{"a.md"}
			},
		},
		{
			name: "migration output leaks durable receipt", contract: "apply_v02_migration.output",
			base: fixtures.migrationApplied,
			mutate: func(value map[string]any) {
				value["receipt"] = map[string]any{"result_revision": value["result_revision"]}
			},
		},
		{
			name: "migration output leaks authorization proof", contract: "apply_v02_migration.output",
			base: fixtures.migrationApplied,
			mutate: func(value map[string]any) {
				value["proof"] = proof
			},
		},
		{
			name: "migration error envelope presented as success", contract: "apply_v02_migration.output",
			base: fixtures.migrationApplied,
			mutate: func(value map[string]any) {
				replaceOutputStateMap(value, map[string]any{
					"code": "plan_mismatch", "message": "mismatch",
					"retryable": false, "diagnostics": []any{},
				})
			},
		},
		{
			name: "write rejected success output", contract: "write_concept.output",
			base: fixtures.writeSuccess,
			mutate: func(value map[string]any) {
				value["status"] = "rejected"
			},
		},
		{
			name: "write success without path", contract: "write_concept.output",
			base: fixtures.writeSuccess,
			mutate: func(value map[string]any) {
				value["path"] = ""
			},
		},
	}

	// Act and assert.
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			value := outputStateMap(t, test.base)
			test.mutate(value)
			if err := validateContract(test.contract, value); err == nil {
				result := mcp.NewToolResultStructured(value, "")
				tool := strings.TrimSuffix(test.contract, ".output")
				if enforceContractResult(tool, result) == result {
					t.Fatalf("%s accepted unreachable output state: %#v", test.contract, value)
				}
			}
		})
	}
}

type outputStateFixtures struct {
	patchApplicable          patchPreviewResponse
	patchNoop                patchPreviewResponse
	patchRejected            patchPreviewResponse
	migrationApplicable      migrationPreviewResponse
	migrationPlannedNoop     migrationPreviewResponse
	migrationTargetNoop      migrationPreviewResponse
	migrationBlocked         migrationPreviewResponse
	patchApplied             patchApplyResponse
	patchApplyNoop           patchApplyResponse
	patchApplyRejected       patchApplyResponse
	migrationApplied         migrationApplyResponse
	migrationTransitionNoop  migrationApplyResponse
	migrationApplyTargetNoop migrationApplyResponse
	migrationApplyRejected   migrationApplyResponse
	writeSuccess             writeConceptResponse
}

func outputStateContractFixtures(t *testing.T) outputStateFixtures {
	t.Helper()
	base := store.Revision(outputStateRevision("a"))
	result := store.Revision(outputStateRevision("b"))
	planDigest := outputStateRevision("d")
	writeDigest := strings.Repeat("c", 64)
	source := &capturedMigrationSource{
		paths: []string{"a.md"},
		files: map[string][]byte{"a.md": []byte("old\n")},
	}
	applicablePreview := store.Preview{
		BaseRevision: base, ResultRevision: result,
		Writes:      []store.Write{{Path: "a.md", Digest: writeDigest, Content: []byte("new\n")}},
		Diagnostics: []store.Diagnostic{},
	}
	noopPreview := store.Preview{
		BaseRevision: base, ResultRevision: base,
		Writes: []store.Write{}, Deletes: []string{}, Renames: []store.Rename{},
		Diagnostics: []store.Diagnostic{},
	}
	v01Resolution := mutation.MigrationSourceResolution{
		RequestedSelector:  mutation.MigrationSelectorAuto,
		DeclarationPresent: true, DeclarationValid: true,
		DeclarationRaw: "0.1", DeclaredVersion: mutation.MigrationVersionV01,
		ResolvedSource:   mutation.MigrationVersionV01,
		ResolutionSource: mutation.MigrationResolutionDeclared,
		FromVersion:      mutation.MigrationVersionV01, ToVersion: mutation.MigrationVersionV02,
		Transition: mutation.MigrationTransitionV01ToV02,
	}
	targetResolution := mutation.MigrationSourceResolution{
		RequestedSelector: mutation.MigrationSelectorAuto,
		DeclarationValid:  true,
		ResolvedSource:    mutation.MigrationVersionV02,
		ResolutionSource:  mutation.MigrationResolutionDefault,
		FromVersion:       mutation.MigrationVersionV02, ToVersion: mutation.MigrationVersionV02,
		Transition: mutation.MigrationTransitionTargetNoop,
	}
	appliedReceipt := store.CommitReceipt{
		BaseRevision: base, ResultRevision: result,
		ChangedFiles: []store.FileChange{{Kind: store.FileWrite, Path: "a.md"}},
	}
	appliedPaths := changedReceiptPaths(appliedReceipt)
	errorDiagnostics := []wireDiagnostic{{
		Code: "invalid_change_set", Severity: "ERROR", Message: "rejected",
	}}
	patchApplicable, err := successfulPatchPreview(t.Context(), source, planDigest, applicablePreview)
	if err != nil {
		t.Fatalf("successfulPatchPreview(applicable) error = %v", err)
	}
	patchNoop, err := successfulPatchPreview(t.Context(), source, planDigest, noopPreview)
	if err != nil {
		t.Fatalf("successfulPatchPreview(noop) error = %v", err)
	}
	patchRejected, err := rejectedPatchPreviewContext(t.Context(), base, planDigest, []store.Diagnostic{{
		Code: "invalid_change_set", Severity: store.DiagnosticError, Message: "rejected",
	}})
	if err != nil {
		t.Fatalf("rejectedPatchPreviewContext() error = %v", err)
	}
	migrationApplicable, err := migrationPreviewProjection(
		t.Context(), source, base, v01Resolution,
		mutation.MigrationPreview{
			Preview: applicablePreview, Proof: outputStateMigrationProof(base, result, planDigest, writeDigest, true),
			PlanDigest: planDigest, Blockers: []mutation.MigrationBlocker{}, ManualActions: []mutation.MigrationManualAction{},
		}, nil,
	)
	if err != nil {
		t.Fatalf("migrationPreviewProjection(applicable) error = %v", err)
	}
	migrationPlannedNoop, err := migrationPreviewProjection(
		t.Context(), source, base, v01Resolution,
		mutation.MigrationPreview{
			Preview: noopPreview, Proof: outputStateMigrationProof(base, base, planDigest, writeDigest, false),
			PlanDigest: planDigest, Blockers: []mutation.MigrationBlocker{}, ManualActions: []mutation.MigrationManualAction{},
		}, nil,
	)
	if err != nil {
		t.Fatalf("migrationPreviewProjection(noop) error = %v", err)
	}
	migrationTargetNoop, err := migrationResolutionPreviewContext(t.Context(), base, targetResolution, nil)
	if err != nil {
		t.Fatalf("migrationResolutionPreviewContext() error = %v", err)
	}
	migrationBlocked, err := migrationInputPreflightPreviewContext(
		t.Context(), base, v01Resolution,
		mutation.MigrationInputPreflight{
			Blockers:      []mutation.MigrationBlocker{{Code: "migration_blocked", Path: "a.md", Message: "blocked"}},
			ManualActions: []mutation.MigrationManualAction{},
		}, errors.New("blocked"),
	)
	if err != nil {
		t.Fatalf("migrationInputPreflightPreviewContext() error = %v", err)
	}
	writeSuccess, err := writeConceptSuccessContext(
		t.Context(), "", "a.md", validator.Report{Diagnostics: []validator.Diagnostic{}},
	)
	if err != nil {
		t.Fatalf("writeConceptSuccessContext() error = %v", err)
	}

	return outputStateFixtures{
		patchApplicable:      patchApplicable,
		patchNoop:            patchNoop,
		patchRejected:        patchRejected,
		migrationApplicable:  migrationApplicable,
		migrationPlannedNoop: migrationPlannedNoop,
		migrationTargetNoop:  migrationTargetNoop,
		migrationBlocked:     migrationBlocked,
		patchApplied: patchApplyResponse{
			Status: "applied", BaseRevision: string(base), ResultRevision: string(result),
			PlanDigest: planDigest, ChangedPaths: appliedPaths, Diagnostics: []wireDiagnostic{},
		},
		patchApplyNoop: patchApplyResponse{
			Status: "noop", BaseRevision: string(base), ResultRevision: string(base),
			PlanDigest: planDigest, ChangedPaths: []string{}, Diagnostics: []wireDiagnostic{},
		},
		patchApplyRejected: patchApplyResponse{
			Status: "rejected", BaseRevision: string(base), ResultRevision: string(base),
			PlanDigest: planDigest, ChangedPaths: []string{}, Diagnostics: errorDiagnostics,
		},
		migrationApplied: migrationApplyResponse{
			Status: "applied", Source: migrationSourceDTOFromDomain(v01Resolution),
			BaseRevision: string(base), ResultRevision: string(result), PlanDigest: planDigest,
			ChangedPaths: appliedPaths, Diagnostics: []wireDiagnostic{},
		},
		migrationTransitionNoop: migrationApplyResponse{
			Status: "noop", Source: migrationSourceDTOFromDomain(v01Resolution),
			BaseRevision: string(base), ResultRevision: string(base), PlanDigest: planDigest,
			ChangedPaths: []string{}, Diagnostics: []wireDiagnostic{},
		},
		migrationApplyTargetNoop: migrationApplyResponse{
			Status: "noop", Source: migrationSourceDTOFromDomain(targetResolution),
			BaseRevision: string(base), ResultRevision: string(base), PlanDigest: "",
			ChangedPaths: []string{}, Diagnostics: []wireDiagnostic{},
		},
		migrationApplyRejected: migrationApplyResponse{
			Status: "rejected", Source: migrationSourceDTOFromDomain(v01Resolution),
			BaseRevision: string(base), ResultRevision: string(base), PlanDigest: planDigest,
			ChangedPaths: []string{}, Diagnostics: errorDiagnostics,
		},
		writeSuccess: writeSuccess,
	}
}

func outputStateMigrationProof(
	base store.Revision,
	result store.Revision,
	planDigest string,
	writeDigest string,
	withWrite bool,
) mutation.MigrationPlanProof {
	proof := mutation.MigrationPlanProof{
		FormatVersion: mutation.MigrationPlanProofFormatVersion,
		RequestDigest: planDigest, ResolutionDigest: outputStateRevision("e"),
		BaseRevision: base, ResultRevision: result,
		Reads: []store.Read{}, Writes: []mutation.MigrationPlanWrite{},
		Deletes: []string{}, Renames: []store.Rename{},
		AffectedRefs: []bundle.RelationRef{}, ReverseImpact: []bundle.RelationRef{},
		ChangedFiles: []store.FileChange{}, ChangedRefs: []bundle.RelationRef{},
	}
	if withWrite {
		proof.Writes = append(proof.Writes, mutation.MigrationPlanWrite{
			Path: "a.md", Digest: writeDigest,
		})
		proof.ChangedFiles = append(proof.ChangedFiles, store.FileChange{
			Kind: store.FileWrite, Path: "a.md",
		})
	}
	return proof
}

func outputStateMap(t *testing.T, value any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal(output state fixture) error = %v", err)
	}
	var cloned map[string]any
	if err := json.Unmarshal(data, &cloned); err != nil {
		t.Fatalf("Unmarshal(output state fixture) error = %v", err)
	}
	return cloned
}

func outputStateSource(value map[string]any) map[string]any {
	return value["source"].(map[string]any)
}

func replaceOutputStateMap(target, replacement map[string]any) {
	for key := range target {
		delete(target, key)
	}
	for key, value := range replacement {
		target[key] = value
	}
}

func outputStateRevision(fill string) string {
	return "sha256:" + strings.Repeat(fill, 64)
}
