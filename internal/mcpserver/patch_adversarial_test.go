package mcpserver

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mark3labs/mcp-go/mcptest"
)

func TestMCPConceptPatchAdversarialPresentationsFailClosed(t *testing.T) {
	// Arrange.
	srv, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer srv.Close()

	tests := []struct {
		name               string
		document           string
		operation          map[string]any
		wantDiagnosticCode string
	}{
		{
			name: "partial edit of flow sequence",
			document: "---\n" +
				"type: Note\n" +
				"sources: [{id: remove, resource: remove.md}, {id: opaque, ratio: 1.20}]\n" +
				"x-opaque: keep\n" +
				"---\nBody.\n",
			operation: map[string]any{
				"kind": "remove_source", "concept_id": "a", "source_id": "remove",
			},
			wantDiagnosticCode: "invalid_presentation",
		},
		{
			name: "alias-owned generated family",
			document: "---\n" +
				"type: Note\n" +
				"generated-template: &generated-template\n" +
				"  by: process:old\n" +
				"  at: 2026-01-01T00:00:00Z\n" +
				"generated: *generated-template\n" +
				"x-opaque: keep\n" +
				"---\nBody.\n",
			operation: map[string]any{
				"kind": "set_generated", "concept_id": "a",
				"generated": map[string]any{"by": "process:new", "at": "2026-02-01T00:00:00Z"},
			},
			wantDiagnosticCode: "ambiguous_presentation",
		},
		{
			name: "merge-owned generated key",
			document: "---\n" +
				"type: Note\n" +
				"generated-template: &generated-template\n" +
				"  by: process:old\n" +
				"generated:\n" +
				"  <<: *generated-template\n" +
				"  at: 2026-01-01T00:00:00Z\n" +
				"x-opaque: keep\n" +
				"---\nBody.\n",
			operation: map[string]any{
				"kind": "set_generated", "concept_id": "a",
				"generated": map[string]any{"by": "process:new", "at": "2026-02-01T00:00:00Z"},
			},
			wantDiagnosticCode: "ambiguous_presentation",
		},
		{
			name: "duplicate known key",
			document: "---\n" +
				"type: Note\n" +
				"generated:\n" +
				"  by: process:first\n" +
				"  by: process:second\n" +
				"x-opaque: keep\n" +
				"---\nBody.\n",
			operation: map[string]any{
				"kind": "set_generated", "concept_id": "a",
				"generated": map[string]any{"by": "process:new"},
			},
			wantDiagnosticCode: "ambiguous_presentation",
		},
		{
			name: "ambiguous source selector",
			document: "---\n" +
				"type: Note\n" +
				"sources:\n" +
				"  - {id: duplicate, resource: first.md}\n" +
				"  - {id: duplicate, resource: second.md}\n" +
				"x-opaque: keep\n" +
				"---\nBody.\n",
			operation: map[string]any{
				"kind": "remove_source", "concept_id": "a", "source_id": "duplicate",
			},
			wantDiagnosticCode: "ambiguous_presentation",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "index.md",
				"---\nokf_version: \"0.2\"\n---\n# Notes\n\n- [A](a.md)\n",
			)
			writeTestFile(t, root, "a.md", test.document)
			before := protocolTreeSnapshot(t, root)
			arguments := map[string]any{
				"bundle_path": root,
				"actor":       "mcp:adversarial-test",
				"operations":  []any{test.operation},
			}

			// Act.
			preview := callMCPTool(t, srv, "preview_concept_patch", arguments)

			// Assert.
			if preview.IsError {
				t.Fatalf("rejected preview returned a tool error: %s", resultText(t, preview))
			}
			payload := preview.StructuredContent.(map[string]any)
			if payload["status"] != "rejected" ||
				payload["result_revision"] != nil ||
				payload["diff"] != "" ||
				payload["unknown_preserved"] != false ||
				len(payload["affected_paths"].([]any)) != 0 {
				t.Fatalf("rejected preview exposed a commit-ready plan: %#v", payload)
			}
			assertPatchDiagnosticCode(t, payload["diagnostics"].([]any), test.wantDiagnosticCode)
			if after := protocolTreeSnapshot(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("preview changed bundle tree:\nbefore=%#v\nafter=%#v", before, after)
			}

			applyArguments := cloneArguments(arguments)
			applyArguments["expected_revision"] = payload["base_revision"]
			applyArguments["expected_plan_digest"] = payload["plan_digest"]

			// Act.
			applied := callMCPTool(t, srv, "apply_concept_patch", applyArguments)

			// Assert.
			if applied.IsError {
				t.Fatalf("rejected apply returned a tool error: %s", resultText(t, applied))
			}
			appliedPayload := applied.StructuredContent.(map[string]any)
			if appliedPayload["status"] != "rejected" ||
				appliedPayload["base_revision"] != payload["base_revision"] ||
				appliedPayload["result_revision"] != payload["base_revision"] ||
				appliedPayload["plan_digest"] != payload["plan_digest"] ||
				len(appliedPayload["changed_paths"].([]any)) != 0 {
				t.Fatalf("rejected apply exposed a committed result: %#v", appliedPayload)
			}
			assertPatchDiagnosticCode(
				t,
				appliedPayload["diagnostics"].([]any),
				test.wantDiagnosticCode,
			)
			if after := protocolTreeSnapshot(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("rejected apply changed bundle tree:\nbefore=%#v\nafter=%#v", before, after)
			}
		})
	}
}

func TestMCPConceptPatchExternalWriterWinsAfterPreview(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "index.md",
		"---\nokf_version: \"0.2\"\n---\n# Notes\n\n- [A](a.md)\n",
	)
	writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nOriginal.\n")
	srv, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer srv.Close()
	arguments := map[string]any{
		"bundle_path": root,
		"actor":       "mcp:adversarial-test",
		"operations": []any{map[string]any{
			"kind": "set_lifecycle", "concept_id": "a",
			"lifecycle": map[string]any{"status": "stable", "stale_after": nil},
		}},
	}
	preview := callMCPTool(t, srv, "preview_concept_patch", arguments)
	if preview.IsError {
		t.Fatalf("preview returned error: %s", resultText(t, preview))
	}
	payload := preview.StructuredContent.(map[string]any)
	if payload["status"] != "applicable" {
		t.Fatalf("preview status = %#v, want applicable", payload)
	}

	externalDocument := "---\ntype: Note\nexternal: preserved\n---\nExternal writer won.\n"
	writerDone := make(chan error, 1)
	go func() {
		writerDone <- os.WriteFile(filepath.Join(root, "a.md"), []byte(externalDocument), 0o644)
	}()
	if err := <-writerDone; err != nil {
		t.Fatalf("external writer: %v", err)
	}
	afterExternalWrite := protocolTreeSnapshot(t, root)
	applyArguments := cloneArguments(arguments)
	applyArguments["expected_revision"] = payload["base_revision"]
	applyArguments["expected_plan_digest"] = payload["plan_digest"]

	// Act.
	applied := callMCPTool(t, srv, "apply_concept_patch", applyArguments)

	// Assert.
	if !applied.IsError {
		t.Fatalf("stale apply overwrote an external writer: %#v", applied.StructuredContent)
	}
	envelope := applied.StructuredContent.(map[string]any)
	if envelope["code"] != "revision_conflict" || envelope["retryable"] != true {
		t.Fatalf("stale apply error = %#v, want retryable revision_conflict", envelope)
	}
	if got := readTestFile(t, root, "a.md"); got != externalDocument {
		t.Fatalf("external writer content was overwritten:\n%s", got)
	}
	if afterApply := protocolTreeSnapshot(t, root); !reflect.DeepEqual(afterApply, afterExternalWrite) {
		t.Fatalf("stale apply changed bundle tree:\nexternal=%#v\nafter=%#v", afterExternalWrite, afterApply)
	}
}

func assertPatchDiagnosticCode(t *testing.T, diagnostics []any, want string) {
	t.Helper()
	for _, raw := range diagnostics {
		diagnostic, ok := raw.(map[string]any)
		if ok && diagnostic["code"] == want {
			return
		}
	}
	t.Fatalf("diagnostics = %#v, want code %q", diagnostics, want)
}
