package mcpserver

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcptest"
)

func TestMCPMigrationExplicitGeneratedAtBindsTransitionInstant(t *testing.T) {
	srv, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer srv.Close()

	newBundle := func(t *testing.T) (string, string) {
		t.Helper()
		root := t.TempDir()
		writeTestFile(t, root, "index.md",
			"---\nokf_version: \"0.1\"\n---\n# Notes\n\n- [Alpha](alpha.md)\n",
		)
		document := "---\ntype: Knowledge\ngenerated:\n" +
			"  by: process:existing\n" +
			"  at: 2026-06-25T16:00:00+07:00\n---\n\nAlpha.\n"
		writeTestFile(t, root, "alpha.md", document)
		return root, document
	}
	arguments := func(root, generatedAt string) map[string]any {
		return map[string]any{
			"bundle_path":  root,
			"from":         "auto",
			"generated_by": "process:migration",
			"generated_at": []any{map[string]any{
				"concept_id": "alpha",
				"at":         generatedAt,
			}},
			"timestamp_policy":          "preserve",
			"timestamp_conflict_policy": "reject",
			"citation_mappings":         []any{},
		}
	}
	assertNoWrite := func(t *testing.T, root string, before map[string]string) {
		t.Helper()
		if after := protocolTreeSnapshot(t, root); !reflect.DeepEqual(after, before) {
			t.Fatalf("migration rejection changed bundle tree:\nbefore=%#v\nafter=%#v", before, after)
		}
		if _, statErr := os.Lstat(filepath.Join(root, ".okf")); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("migration rejection opened transactional store: %v", statErr)
		}
	}

	t.Run("different instant blocks preview and rejects apply", func(t *testing.T) {
		// Arrange.
		root, document := newBundle(t)
		before := protocolTreeSnapshot(t, root)
		equivalentArguments := arguments(root, "2026-06-25T09:00:00.000Z")
		baseline := callMCPTool(t, srv, "preview_v02_migration", equivalentArguments)
		if baseline.IsError {
			t.Fatalf("baseline preview returned error: %s", resultText(t, baseline))
		}
		baselinePayload := baseline.StructuredContent.(map[string]any)
		if baselinePayload["status"] != "applicable" ||
			baselinePayload["proof"] == nil ||
			baselinePayload["plan_digest"] == "" {
			t.Fatalf("baseline preview = %#v, want applicable frozen plan", baselinePayload)
		}
		mismatchArguments := arguments(root, "2026-06-26T09:00:00Z")

		// Act.
		preview := callMCPTool(t, srv, "preview_v02_migration", mismatchArguments)

		// Assert.
		if preview.IsError {
			t.Fatalf("mismatched preview returned a generic tool error: %s", resultText(t, preview))
		}
		payload := preview.StructuredContent.(map[string]any)
		blockers := payload["blockers"].([]any)
		if payload["status"] != "blocked" ||
			payload["result_revision"] != nil ||
			payload["proof"] != nil ||
			payload["plan_digest"] != "" ||
			payload["diff"] != "" ||
			len(payload["affected_paths"].([]any)) != 0 ||
			len(blockers) != 1 {
			t.Fatalf("mismatched preview exposed a stage: %#v", payload)
		}
		blocker := blockers[0].(map[string]any)
		if blocker["code"] != "explicit_generated_at_mismatch" ||
			blocker["path"] != "alpha.md" {
			t.Fatalf("mismatched preview blocker = %#v", blocker)
		}
		location := blocker["location"].(map[string]any)
		start, end := int(location["start"].(float64)), int(location["end"].(float64))
		if start < 0 || end <= start || end > len(document) ||
			!bytes.Contains([]byte(document)[start:end], []byte("2026-06-25T16:00:00+07:00")) {
			t.Fatalf("mismatched blocker location = [%d,%d) over %q", start, end, document)
		}
		assertNoWrite(t, root, before)

		applyArguments := cloneArguments(mismatchArguments)
		applyArguments["expected_source"] = baselinePayload["source"]
		applyArguments["proof"] = baselinePayload["proof"]
		applyArguments["expected_plan_digest"] = baselinePayload["plan_digest"]

		// Act.
		applied := callMCPTool(t, srv, "apply_v02_migration", applyArguments)

		// Assert.
		assertSchemaErrorEnvelope(t, applied, "plan_mismatch")
		assertNoWrite(t, root, before)
	})

	t.Run("equivalent RFC3339 instant applies and preserves existing actor", func(t *testing.T) {
		// Arrange.
		root, _ := newBundle(t)
		migrationArguments := arguments(root, "2026-06-25T09:00:00.000Z")

		// Act.
		preview := callMCPTool(t, srv, "preview_v02_migration", migrationArguments)

		// Assert.
		if preview.IsError {
			t.Fatalf("equivalent preview returned error: %s", resultText(t, preview))
		}
		payload := preview.StructuredContent.(map[string]any)
		if payload["status"] != "applicable" ||
			payload["result_revision"] == nil ||
			payload["proof"] == nil ||
			payload["plan_digest"] == "" ||
			len(payload["blockers"].([]any)) != 0 {
			t.Fatalf("equivalent preview = %#v, want applicable", payload)
		}

		applyArguments := cloneArguments(migrationArguments)
		applyArguments["expected_source"] = payload["source"]
		applyArguments["proof"] = payload["proof"]
		applyArguments["expected_plan_digest"] = payload["plan_digest"]

		// Act.
		applied := callMCPTool(t, srv, "apply_v02_migration", applyArguments)

		// Assert.
		if applied.IsError {
			t.Fatalf("equivalent apply returned error: %s", resultText(t, applied))
		}
		appliedPayload := applied.StructuredContent.(map[string]any)
		if appliedPayload["status"] != "applied" ||
			appliedPayload["base_revision"] != payload["base_revision"] ||
			appliedPayload["result_revision"] != payload["result_revision"] {
			t.Fatalf("equivalent apply = %#v, want applied preview result", appliedPayload)
		}
		document := readTestFile(t, root, "alpha.md")
		if !strings.Contains(document, "by: process:existing") ||
			!strings.Contains(document, "at: 2026-06-25T16:00:00+07:00") ||
			strings.Contains(document, "by: process:migration") {
			t.Fatalf("equivalent migration did not preserve creation-owned generated metadata:\n%s", document)
		}
	})
}
