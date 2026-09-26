package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skosovsky/okf/upkeep"
)

func TestBlockingAndLoopGuard(t *testing.T) {
	// Arrange: record a clean work boundary in a fresh Git repository.
	root := t.TempDir()
	cmd := exec.Command("git", "-C", root, "init", "-q")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	config := filepath.Join(root, "upkeep.json")
	baseline := filepath.Join(t.TempDir(), "baseline.json")
	if err := os.WriteFile(config, []byte(`{"repo_root":".","bundle_root":"knowledge","relevant_paths":["src"],"mode":"blocking","failure_policy":"closed","timeout_ms":5000}`), 0600); err != nil {
		t.Fatal(err)
	}
	var out, diagnostic bytes.Buffer
	if code := run([]string{"baseline", "--config", config, "--out", baseline}, bytes.NewReader(nil), &out, &diagnostic); code != 0 {
		t.Fatalf("baseline: %d %s", code, diagnostic.String())
	}
	session := strings.TrimSpace(out.String())
	if err := os.Mkdir(filepath.Join(root, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "new.go"), []byte("package src"), 0600); err != nil {
		t.Fatal(err)
	}
	// Act: check in blocking mode, then simulate a repeated Claude Stop invocation.
	out.Reset()
	diagnostic.Reset()
	code := run([]string{"check", "--config", config, "--baseline", baseline, "--session", session}, bytes.NewReader(nil), &out, &diagnostic)
	var result upkeep.Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	diagnostic.Reset()
	adapterCode := run([]string{"check", "--config", config, "--baseline", baseline, "--session", session, "--adapter", "claude-stop"}, bytes.NewBufferString(`{"stop_hook_active":false}`), &out, &diagnostic)
	var adapterResult struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal(out.Bytes(), &adapterResult); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	diagnostic.Reset()
	retryCode := run([]string{"check", "--config", config, "--baseline", baseline, "--session", session, "--adapter", "claude-stop"}, bytes.NewBufferString(`{"stop_hook_active":true}`), &out, &diagnostic)
	// Assert: first check blocks, repeated Stop invocation permits completion.
	if code != 2 || result.Status != "needs_review" || adapterCode != 0 || adapterResult.Decision != "block" || !strings.Contains(adapterResult.Reason, result.Fingerprint) || retryCode != 0 || out.String() != "{}\n" {
		t.Fatalf("block=%d status=%s adapter=%+v retry=%d output=%q errors=%q", code, result.Status, adapterResult, retryCode, out.String(), diagnostic.String())
	}
}

func TestFailurePolicyAndOverride(t *testing.T) {
	// Arrange: create a blocking config and an absent baseline.
	root := t.TempDir()
	config := filepath.Join(root, "upkeep.json")
	if err := os.WriteFile(config, []byte(`{"repo_root":".","bundle_root":"knowledge","relevant_paths":["src"],"mode":"blocking","failure_policy":"closed"}`), 0600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(root, "absent.json")
	var out, diagnostic bytes.Buffer
	// Act: run once under fail-closed, then with an explicit override.
	blocked := run([]string{"check", "--config", config, "--baseline", missing, "--session", "test"}, bytes.NewReader(nil), &out, &diagnostic)
	out.Reset()
	diagnostic.Reset()
	allowed := run([]string{"check", "--config", config, "--baseline", missing, "--session", "test", "--override", "Maintenance window"}, bytes.NewReader(nil), &out, &diagnostic)
	var result upkeep.Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	// Assert: unavailable is blocked unless explicitly overridden.
	if blocked != 2 || allowed != 0 || result.Status != "overridden" {
		t.Fatalf("blocked=%d allowed=%d result=%+v", blocked, allowed, result)
	}
}
