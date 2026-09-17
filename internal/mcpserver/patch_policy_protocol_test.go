package mcpserver

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcptest"
)

func TestMCPConceptPatchPolicyFailuresBlockButOrdinaryWarningsDoNot(t *testing.T) {
	srv, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer srv.Close()

	operation := map[string]any{
		"kind": "set_generated", "concept_id": "a",
		"generated": map[string]any{"by": "human:reviewer"},
	}

	t.Run("explicit selector mismatch is diagnostic-only", func(t *testing.T) {
		// Arrange.
		root := t.TempDir()
		writeTestFile(t, root, "index.md",
			"---\nokf_version: \"0.1\"\n---\n# Notes\n\n- [A](a.md)\n",
		)
		writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nBody.\n")
		before := protocolTreeSnapshot(t, root)
		arguments := map[string]any{
			"bundle_path": root,
			"actor":       "mcp:policy-test",
			"operations":  []any{operation},
		}

		// Act.
		directPreview := callHandler(t, handlePreviewConceptPatch, arguments)
		preview := callMCPTool(t, srv, "preview_concept_patch", arguments)

		// Assert.
		if directPreview.IsError {
			t.Fatalf("direct policy-rejected preview returned a tool error: %s", resultText(t, directPreview))
		}
		if preview.IsError {
			t.Fatalf("policy-rejected preview returned a tool error: %s", resultText(t, preview))
		}
		payload := preview.StructuredContent.(map[string]any)
		directPayload := outputStateMap(t, directPreview.StructuredContent)
		if !reflect.DeepEqual(directPayload, payload) {
			t.Fatalf("direct/wrapped policy-rejected preview drift:\ndirect=%#v\nwrapped=%#v", directPayload, payload)
		}
		if got := enforceContractResult("preview_concept_patch", directPreview); got != directPreview {
			t.Fatalf("direct policy-rejected preview violates advertised output: %#v", got.StructuredContent)
		}
		if payload["status"] != "rejected" ||
			payload["result_revision"] != nil ||
			payload["diff"] != "" ||
			payload["unknown_preserved"] != false ||
			len(payload["affected_paths"].([]any)) != 0 {
			t.Fatalf("policy-rejected preview exposed a commit-ready plan: %#v", payload)
		}
		assertPatchDiagnosticCode(t, payload["diagnostics"].([]any), "version_assertion_failed")
		assertPatchDiagnosticSeverity(
			t,
			payload["diagnostics"].([]any),
			"staged_validation_failed",
			"ERROR",
		)

		applyArguments := cloneArguments(arguments)
		applyArguments["expected_revision"] = payload["base_revision"]
		applyArguments["expected_plan_digest"] = payload["plan_digest"]

		// Act.
		directApply := callHandler(t, handleApplyConceptPatch, applyArguments)
		applied := callMCPTool(t, srv, "apply_concept_patch", applyArguments)

		// Assert.
		if directApply.IsError {
			t.Fatalf("direct policy-rejected apply returned a tool error: %s", resultText(t, directApply))
		}
		if applied.IsError {
			t.Fatalf("policy-rejected apply returned a tool error: %s", resultText(t, applied))
		}
		appliedPayload := applied.StructuredContent.(map[string]any)
		directAppliedPayload := outputStateMap(t, directApply.StructuredContent)
		if !reflect.DeepEqual(directAppliedPayload, appliedPayload) {
			t.Fatalf(
				"direct/wrapped policy-rejected apply drift:\ndirect=%#v\nwrapped=%#v",
				directAppliedPayload,
				appliedPayload,
			)
		}
		if got := enforceContractResult("apply_concept_patch", directApply); got != directApply {
			t.Fatalf("direct policy-rejected apply violates advertised output: %#v", got.StructuredContent)
		}
		if appliedPayload["status"] != "rejected" ||
			appliedPayload["base_revision"] != payload["base_revision"] ||
			appliedPayload["result_revision"] != payload["base_revision"] ||
			appliedPayload["plan_digest"] != payload["plan_digest"] ||
			len(appliedPayload["changed_paths"].([]any)) != 0 {
			t.Fatalf("policy-rejected apply exposed a committed result: %#v", appliedPayload)
		}
		assertPatchDiagnosticCode(
			t,
			appliedPayload["diagnostics"].([]any),
			"version_assertion_failed",
		)
		assertPatchDiagnosticSeverity(
			t,
			appliedPayload["diagnostics"].([]any),
			"staged_validation_failed",
			"ERROR",
		)
		if after := protocolTreeSnapshot(t, root); !reflect.DeepEqual(after, before) {
			t.Fatalf("policy rejection changed bundle tree:\nbefore=%#v\nafter=%#v", before, after)
		}
	})

	t.Run("ordinary strict warning remains applicable", func(t *testing.T) {
		// Arrange.
		root := t.TempDir()
		writeTestFile(t, root, "index.md",
			"---\nokf_version: \"0.2\"\n---\n# Notes\n\n- [A](a.md)\n",
		)
		writeTestFile(t, root, "a.md",
			"---\ntype: Note\nstatus: archived\n---\nBody.\n",
		)
		arguments := map[string]any{
			"bundle_path": root,
			"actor":       "mcp:policy-test",
			"operations":  []any{operation},
		}

		// Act.
		preview := callMCPTool(t, srv, "preview_concept_patch", arguments)

		// Assert.
		if preview.IsError {
			t.Fatalf("warning-only preview returned a tool error: %s", resultText(t, preview))
		}
		payload := preview.StructuredContent.(map[string]any)
		if payload["status"] != "applicable" || payload["result_revision"] == nil {
			t.Fatalf("warning-only preview = %#v, want applicable", payload)
		}
		assertPatchDiagnosticCode(t, payload["diagnostics"].([]any), "status_invalid")

		applyArguments := cloneArguments(arguments)
		applyArguments["expected_revision"] = payload["base_revision"]
		applyArguments["expected_plan_digest"] = payload["plan_digest"]

		// Act.
		applied := callMCPTool(t, srv, "apply_concept_patch", applyArguments)

		// Assert.
		if applied.IsError {
			t.Fatalf("warning-only apply returned a tool error: %s", resultText(t, applied))
		}
		appliedPayload := applied.StructuredContent.(map[string]any)
		if appliedPayload["status"] != "applied" ||
			appliedPayload["base_revision"] != payload["base_revision"] ||
			appliedPayload["result_revision"] != payload["result_revision"] {
			t.Fatalf("warning-only apply = %#v, want applied preview result", appliedPayload)
		}
		if document := readTestFile(t, root, "a.md"); !strings.Contains(document, "by: human:reviewer") {
			t.Fatalf("warning-only apply did not publish generated metadata: %q", document)
		}
	})
}

func assertPatchDiagnosticSeverity(
	t *testing.T,
	diagnostics []any,
	code string,
	severity string,
) {
	t.Helper()
	for _, value := range diagnostics {
		diagnostic, ok := value.(map[string]any)
		if ok && diagnostic["code"] == code {
			if diagnostic["severity"] != severity {
				t.Fatalf(
					"diagnostic %q severity = %#v, want %q",
					code,
					diagnostic["severity"],
					severity,
				)
			}
			return
		}
	}
	t.Fatalf("diagnostic %q not found: %#v", code, diagnostics)
}
