package mcpserver

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcptest"
	"github.com/skosovsky/okf/internal/okfcli"
)

func TestOKF03CaseAliasIsRejectedByCLIAndMCP(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	const original = "---\ntype: Note\n---\nDraft.\n"
	writeTestFile(t, root, "INDEX.md", original)
	server, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	// Act.
	var stdout, stderr bytes.Buffer
	exit := okfcli.Run([]string{"index", root}, &stdout, &stderr)
	preview := callMCPTool(t, server, "preview_v03_migration", map[string]any{
		"bundle_path": root, "id": "upgrade", "actor": "human:reviewer", "assume_v02": true,
	})
	write := callMCPTool(t, server, "write_concept", map[string]any{
		"bundle_path": root, "concept_id": "Index", "frontmatter": "type: Note", "body": "Replacement.",
	})

	// Assert.
	if message := stderr.String(); exit == 0 || !strings.Contains(message, "differs only by case") ||
		!strings.Contains(message, "index.md") || !strings.Contains(message, "INDEX.md") {
		t.Fatalf("CLI index exit=%d: %s %s", exit, stdout.String(), stderr.String())
	}
	if preview.IsError {
		t.Fatalf("migration preview: %s", resultText(t, preview))
	}
	validateContractValue(t, "preview_v03_migration.output", preview.StructuredContent)
	plan := preview.StructuredContent.(map[string]any)
	findings := plan["diagnostics"].([]any)
	if plan["status"] != "blocked" || len(findings) != 1 || findings[0].(map[string]any)["code"] != "path_case_collision" {
		t.Fatalf("migration preview: %#v", plan)
	}
	if message := resultText(t, write); !write.IsError || !strings.Contains(message, `"status":"rejected"`) ||
		!strings.Contains(message, "differs only by case") {
		t.Fatalf("MCP write: %s", resultText(t, write))
	}
	if got := readTestFile(t, root, "INDEX.md"); got != original {
		t.Fatalf("original changed: %q", got)
	}
}

func TestOKF03MigrationMCPPublishesThePreviewedRevision(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "index.md", "---\nokf_version: '0.2'\nlicense: MIT\n---\n\n# Notes\n\n- [Policy](policy.md)\n")
	writeTestFile(t, root, "policy.md", "---\ntype: Policy\nstale_after: '2026-11-18T09:30:00-05:00'\n---\n\n[Guide](guide%zz#claim)\n")
	writeTestFile(t, root, "guide%zz.md", "---\ntype: Guide\n---\n\n## Claim\n")
	server, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	arguments := map[string]any{"bundle_path": root, "id": "upgrade", "actor": "human:reviewer"}

	// Act.
	preview := callMCPTool(t, server, "preview_v03_migration", arguments)
	if preview.IsError {
		t.Fatalf("preview failed: %s", resultText(t, preview))
	}
	plan := preview.StructuredContent.(map[string]any)
	arguments["base_revision"] = plan["base_revision"]
	arguments["plan_digest"] = plan["plan_digest"]
	applied := callMCPTool(t, server, "apply_v03_migration", arguments)
	replayed := callMCPTool(t, server, "apply_v03_migration", arguments)

	// Assert.
	if plan["status"] != "ready" || applied.IsError || replayed.IsError {
		t.Fatalf("preview=%v apply=%s replay=%s", plan, resultText(t, applied), resultText(t, replayed))
	}
	if response := applied.StructuredContent.(map[string]any); response["result_revision"] != plan["result_revision"] ||
		response["status"] != "applied" || resultText(t, applied) != resultText(t, replayed) {
		t.Fatalf("unexpected transaction result: %v", response)
	}
	index, err := os.ReadFile(filepath.Join(root, "index.md"))
	if err != nil || !strings.Contains(string(index), "0.3") || !strings.Contains(string(index), "license: MIT") {
		t.Fatalf("unexpected published index: %q (%v)", index, err)
	}
	policy, err := os.ReadFile(filepath.Join(root, "policy.md"))
	if err != nil || !strings.Contains(string(policy), "[Guide](/guide%25zz.md#claim)") {
		t.Fatalf("published Markdown link: %q (%v)", policy, err)
	}
}
