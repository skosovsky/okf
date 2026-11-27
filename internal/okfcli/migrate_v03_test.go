package okfcli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOKF03MigrationCLIRequiresChoicesAndReplaysReceipt(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	index := "---\nokf_version: '0.2'\nlicense: CC-BY-4.0 # keep\nx-project: {team: delivery}\n---\n\n# Notes\n\n- [Policy](notes/old.md)\n- [Reference](root%20source.md)\n"
	note := "---\ntype: Policy\nresource: policy.md\nsources:\n  - resource: root-source.md\n    x-origin: retained\ngenerated: {by: bot/v1, at: '2026-11-18T10:00:00Z'}\nverified: {by: 'human:ada', at: '2026-11-17T10:00:00Z'}\nstale_after: 2026-11-19\n---\n\nPolicy text.\n"
	for path, content := range map[string]string{
		"index.md": index, "notes/old.md": note,
		"policy.md":        "---\ntype: Policy\n---\n",
		"notes/policy.md":  "---\ntype: Policy\n---\n",
		"root-source.md":   "---\ntype: Reference\n---\n",
		"root%20source.md": "---\ntype: Reference\n---\n",
		"root source.md":   "---\ntype: Reference\n---\n",
	} {
		path = filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	choices := filepath.Join(t.TempDir(), "choices.json")
	if err := os.WriteFile(choices, []byte(`[
		{"concept":"notes/old","field":"resource","target":"policy.md"},
		{"concept":"index","field":"markdown[2]","target":"root%20source.md"}
	]`), 0o600); err != nil {
		t.Fatal(err)
	}
	call := func(args ...string) (int, string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := Run(append([]string{"migrate-v03", root}, args...), &stdout, &stderr)
		return code, stdout.String() + stderr.String()
	}
	read := func(path string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	// Act and assert.
	if code, output := call(); code != 1 || !strings.Contains(output, "migration_v03_path_choices_required") ||
		!strings.Contains(output, "notes/policy.md") || !strings.Contains(output, "index.md markdown[2]") {
		t.Fatalf("ambiguous preview: %d %s", code, output)
	}
	code, output := call("--path-choices", choices)
	if code != 0 {
		t.Fatalf("preview: %d %s", code, output)
	}
	if again, outputAgain := call("--path-choices", choices); again != 0 || outputAgain != output {
		t.Fatalf("preview changed: %d %s", again, outputAgain)
	}
	if read("index.md") != index || read("notes/old.md") != note || !strings.Contains(output, "human-reviewed → unverified") {
		t.Fatalf("unexpected preview: %s", output)
	}
	var proof struct {
		PlanDigest string `json:"plan_digest"`
		Preview    struct {
			BaseRevision string
		}
	}
	if err := json.Unmarshal([]byte(output), &proof); err != nil {
		t.Fatal(err)
	}
	args := []string{"--write", "--path-choices", choices, "--base-revision", proof.Preview.BaseRevision}
	if code, failure := call(append(append([]string(nil), args...), "--plan-digest", "sha256:"+strings.Repeat("0", 64))...); code != 1 || !strings.Contains(failure, "digest mismatch") {
		t.Fatalf("tampered apply: %d %s", code, failure)
	}
	args = append(args, "--plan-digest", proof.PlanDigest)
	concurrentEdit := filepath.Join(root, "notes", "old.md")
	if err := os.WriteFile(concurrentEdit, []byte(note+"Concurrent edit.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, failure := call(args...); code != 1 || !strings.Contains(failure, "conflict") || read("index.md") != index {
		t.Fatalf("stale apply: %d %s", code, failure)
	}
	if err := os.WriteFile(concurrentEdit, []byte(note), 0o600); err != nil {
		t.Fatal(err)
	}
	code, receipt := call(args...)
	if code != 0 {
		t.Fatalf("apply: %d %s", code, receipt)
	}
	if code, replay := call(args...); code != 0 || receipt != replay {
		t.Fatalf("replay: %d %s", code, replay)
	}
	updatedIndex, updatedNote := read("index.md"), read("notes/old.md")
	if !strings.Contains(updatedIndex, "0.3") || !strings.Contains(updatedIndex, "license: CC-BY-4.0 # keep") ||
		!strings.Contains(updatedIndex, "x-project: {team: delivery}") ||
		!strings.Contains(updatedIndex, "[Reference](/root%2520source.md)") ||
		!strings.Contains(updatedNote, "resource: /policy.md") || !strings.Contains(updatedNote, "resource: /root-source.md") ||
		!strings.Contains(updatedNote, "x-origin: retained") || !strings.Contains(updatedNote, "stale_after: 2026-11-19") ||
		!strings.Contains(updatedNote, "verified: {by: 'human:ada'") {
		t.Fatalf("unexpected result:\n%s\n%s", updatedIndex, updatedNote)
	}
}
