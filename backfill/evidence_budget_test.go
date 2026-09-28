package backfill

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBoundedWriterSignalsEveryOmittedByte(t *testing.T) {
	cases := []struct {
		name      string
		raw       []byte
		limit     int
		shown     string
		truncated bool
	}{
		{name: "exact valid UTF-8", raw: []byte("AéB"), limit: 4, shown: "AéB"},
		{name: "budget ends inside code point", raw: []byte("AéB"), limit: 2, shown: "A", truncated: true},
		{name: "invalid UTF-8 below budget", raw: []byte{'A', 0xff}, limit: 10, shown: "A", truncated: true},
		{name: "invalid UTF-8 inside prefix", raw: []byte{'A', 0xff, 'B'}, limit: 10, shown: "A", truncated: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange.
			writer := &boundedWriter{limit: tc.limit, hash: sha256.New()}
			// Act.
			if _, err := writer.Write(tc.raw); err != nil {
				t.Fatal(err)
			}
			shown, truncated := writer.displayedDiff()
			// Assert.
			if shown != tc.shown || truncated != tc.truncated || writer.count != int64(len(tc.raw)) {
				t.Fatalf("shown=%q truncated=%v count=%d", shown, truncated, writer.count)
			}
			want := sha256.Sum256(tc.raw)
			if got := writer.hash.Sum(nil); !bytes.Equal(got, want[:]) {
				t.Fatal("full-diff digest changed")
			}
		})
	}
}

func TestBoundedWriterLargeInvalidMidPrefix(t *testing.T) {
	// Arrange.
	before := bytes.Repeat([]byte{'x'}, 1<<20)
	after := bytes.Repeat([]byte{'y'}, 1<<20)
	raw := append(append(append([]byte(nil), before...), 0xff), after...)
	writer := &boundedWriter{limit: len(raw), hash: sha256.New()}

	// Act.
	if _, err := writer.Write(raw[:len(before)]); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(raw[len(before):]); err != nil {
		t.Fatal(err)
	}
	shown, truncated := writer.displayedDiff()

	// Assert.
	if !truncated || shown != string(before) || writer.count != int64(len(raw)) {
		t.Fatalf("shown_bytes=%d truncated=%v count=%d", len(shown), truncated, writer.count)
	}
	want := sha256.Sum256(raw)
	if !bytes.Equal(writer.hash.Sum(nil), want[:]) {
		t.Fatal("full-diff digest changed")
	}
}

// This is the failure case to measure before changing the v1 evidence
// projection. All fixture Git commands use isolated config and empty hooks.
func TestExtractLargeFirstFileHidesSecondFile(t *testing.T) {
	// Arrange.
	dir := t.TempDir()
	hooksDir := t.TempDir()
	templateDir := t.TempDir()
	safeGit := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", dir, "-c", "commit.gpgsign=false", "-c", "core.hooksPath=" + hooksDir}, args...)...)
		// Remove inherited GIT_* overrides, including config injections and
		// alternate worktrees, before applying the fixture's own settings.
		for _, variable := range os.Environ() {
			if !strings.HasPrefix(variable, "GIT_") {
				command.Env = append(command.Env, variable)
			}
		}
		command.Env = append(command.Env,
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
			"GIT_TEMPLATE_DIR="+templateDir, "GIT_TERMINAL_PROMPT=0",
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.invalid",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.invalid",
			"GIT_AUTHOR_DATE=2026-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z",
		)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("isolated git %v: %v: %s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	safeGit("init", "-q")
	// A local hook deliberately fails if run. The safeGit CLI hooksPath must
	// prevent it from being invoked by either fixture commit.
	if err := os.MkdirAll(filepath.Join(dir, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "hooks", "pre-commit"), []byte("#!/bin/sh\nexit 91\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a-large.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "z-decision.txt"), []byte("decision=A\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	safeGit("add", ".")
	safeGit("commit", "-qm", "base")
	base := safeGit("rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(dir, "a-large.txt"), []byte("new\n"+strings.Repeat("large line of filler\n", 4096)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "z-decision.txt"), []byte("decision=B\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	safeGit("add", ".")
	safeGit("commit", "-qm", "change decision")
	head := safeGit("rev-parse", "HEAD")
	config := Config{Repository: dir, Base: base, Head: head, FirstParent: true, MaxDiffBytes: 1024}

	// Act.
	manifest, err := Extract(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Events) != 1 {
		t.Fatalf("event count = %d", len(manifest.Events))
	}
	event := manifest.Events[0]
	encodedBaseline, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("budget=%d diff_bytes=%d shown_diff_bytes=%d manifest_json_bytes=%d second_file_visible=%v truncated=%v", config.MaxDiffBytes, event.DiffBytes, len(event.Diff), len(encodedBaseline), strings.Contains(event.Diff, "decision=B"), event.DiffTruncated)

	// Assert.
	if len(event.Files) != 2 {
		t.Fatalf("event/file count = %d/%d", len(manifest.Events), len(event.Files))
	}
	if event.Files[1].Path != "z-decision.txt" || event.Files[1].Added == nil || *event.Files[1].Added != 1 {
		t.Fatalf("second-file stat lost: %#v", event.Files)
	}
	if !event.DiffTruncated || len(event.Diff) != config.MaxDiffBytes || event.DiffBytes <= int64(config.MaxDiffBytes) {
		t.Fatalf("incorrect prefix bound: bytes=%d displayed=%d truncated=%v", event.DiffBytes, len(event.Diff), event.DiffTruncated)
	}
	if !strings.Contains(event.Diff, "a-large.txt") || strings.Contains(event.Diff, "z-decision.txt") || strings.Contains(event.Diff, "decision=B") {
		t.Fatalf("fixture does not reproduce second-file starvation: %q", event.Diff)
	}
	if event.DiffSHA256 == digest([]byte(event.Diff)) {
		t.Fatal("full-diff digest unexpectedly equals truncated prefix digest")
	}
	proposal := Analysis{
		SchemaVersion: SchemaVersion, EventsSHA256: manifest.EventsSHA256,
		PromptVersion: "truncation-signal/v1", ProducedAt: "2026-01-02T00:00:00Z",
		Results: []EventResult{{EventID: event.ID, Decision: DecisionConsidered, CandidateIDs: []string{"changed-decision"}}},
		Candidates: []Candidate{{
			ID: "changed-decision", ConceptID: "changed-decision", Title: "Decision change",
			Description: "Decision file change requires review.", Body: "The change in the decision file requires review.",
			Evidence: []EvidenceRef{{EventID: event.ID, Path: "z-decision.txt", DiffSHA256: event.DiffSHA256}},
		}},
	}
	if _, _, err := ValidateAnalysis(manifest, proposal); err == nil {
		t.Fatal("candidate citing truncated event accepted without evidence_incomplete")
	}
	proposal.Candidates[0].EvidenceIncomplete = true
	coverage, _, err := ValidateAnalysis(manifest, proposal)
	if err != nil || len(coverage.TruncatedEvents) != 1 || coverage.TruncatedEvents[0] != event.ID {
		t.Fatalf("truncated event not reported: coverage=%#v err=%v", coverage, err)
	}
	inconsistent := manifest
	inconsistent.Events = append([]Event(nil), manifest.Events...)
	inconsistent.Events[0].DiffTruncated = false
	inconsistentEventsJSON, err := json.Marshal(inconsistent.Events)
	if err != nil {
		t.Fatal(err)
	}
	inconsistent.EventsSHA256 = digest(inconsistentEventsJSON)
	inconsistentProposal := proposal
	inconsistentProposal.EventsSHA256 = inconsistent.EventsSHA256
	if _, _, err := ValidateAnalysis(inconsistent, inconsistentProposal); err == nil {
		t.Fatal("self-consistent manifest digest hid contradictory truncation flag")
	}
	for _, budget := range []int{64 << 10, 1 << 20} {
		config.MaxDiffBytes = budget
		start := time.Now()
		other, err := Extract(context.Background(), config)
		if err != nil {
			t.Fatal(err)
		}
		otherEvent := other.Events[0]
		if otherEvent.DiffBytes != event.DiffBytes || otherEvent.DiffSHA256 != event.DiffSHA256 || len(otherEvent.Files) != len(event.Files) {
			t.Fatal("full diff identity or file stats changed with display budget")
		}
		encoded, err := json.Marshal(other)
		if err != nil {
			t.Fatal(err)
		}
		visible := strings.Contains(otherEvent.Diff, "decision=B")
		t.Logf("budget=%d diff_bytes=%d shown_diff_bytes=%d manifest_json_bytes=%d second_file_visible=%v truncated=%v elapsed_ms=%d", budget, otherEvent.DiffBytes, len(otherEvent.Diff), len(encoded), visible, otherEvent.DiffTruncated, time.Since(start).Milliseconds())
		if budget == 64<<10 && visible {
			t.Fatal("default budget unexpectedly exposes the second file")
		}
		if budget == 1<<20 && (!visible || otherEvent.DiffTruncated || otherEvent.DiffSHA256 != digest([]byte(otherEvent.Diff))) {
			t.Fatal("full-budget control failed")
		}
		if other.ConfigSHA256 == manifest.ConfigSHA256 || other.EventsSHA256 == manifest.EventsSHA256 {
			t.Fatal("changed extraction budget did not invalidate frozen evidence identity")
		}
	}
}
