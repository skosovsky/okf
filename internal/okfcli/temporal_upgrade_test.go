package okfcli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTemporalUpgradeCLI_PreviewThenApplyExactProof(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("---\ntype: Note\nstale_after: 2026-01-02\n---\nBody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mappings := filepath.Join(t.TempDir(), "mappings.json")
	if err := os.WriteFile(mappings, []byte(`{"mappings":[{"concept":"a","path":"stale_after","from":"2026-01-02","to":"2026-01-02T12:00:00Z"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{root, "--id", "cli-upgrade", "--actor", "human:reviewer", "--mappings", mappings}
	var previewOutput bytes.Buffer

	// Act.
	code, err := cmdTemporalUpgrade(args, &previewOutput)
	if err != nil || code != 0 {
		t.Fatalf("preview = %d, %v", code, err)
	}
	var preview struct {
		Status     string `json:"status"`
		PlanDigest string `json:"plan_digest"`
		Preview    struct {
			BaseRevision string `json:"BaseRevision"`
		} `json:"preview"`
	}
	if err := json.Unmarshal(previewOutput.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(root, "a.md"))
	if err != nil {
		t.Fatal(err)
	}
	var applyOutput bytes.Buffer
	writeArgs := append(append([]string{}, args...), "--write", "--base-revision", preview.Preview.BaseRevision, "--plan-digest", preview.PlanDigest)
	applyCode, applyErr := cmdTemporalUpgrade(writeArgs, &applyOutput)
	after, readErr := os.ReadFile(filepath.Join(root, "a.md"))

	// Assert.
	if preview.Status != "ready" || preview.PlanDigest == "" || strings.Contains(string(before), "T12:00:00Z") {
		t.Fatalf("preview failed or wrote: %#v, %s", preview, before)
	}
	if applyErr != nil || applyCode != 0 || readErr != nil || !strings.Contains(string(after), "2026-01-02T12:00:00Z") {
		t.Fatalf("apply = %d, %v, %v: %s", applyCode, applyErr, readErr, after)
	}
	if !strings.Contains(applyOutput.String(), `"status":"applied"`) {
		t.Fatalf("apply report: %s", applyOutput.String())
	}
}
