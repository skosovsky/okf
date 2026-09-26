package upkeep

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func git(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
}

func write(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) (string, Config) {
	t.Helper()
	root := t.TempDir()
	git(t, root, "init", "-q")
	git(t, root, "config", "user.name", "Test")
	git(t, root, "config", "user.email", "test@example.com")
	git(t, root, "config", "commit.gpgsign", "false")
	write(t, root, "src/a.go", "original")
	write(t, root, "knowledge/architecture.md", "old concept")
	write(t, root, "knowledge/log.md", "old log")
	write(t, root, "notes.txt", "old note")
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "initial")
	return root, Config{RepoRoot: root, BundleRoot: "knowledge", RelevantPaths: []string{"src"}, ExcludePaths: []string{"src/generated"}, Mode: "advisory", FailurePolicy: "open", TimeoutMS: 5000, MaxSessionMinutes: 1440}
}

func checkFixture(t *testing.T, cfg Config, baseline Baseline, decision *Decision) Result {
	t.Helper()
	current, err := Snapshot(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Check(cfg, baseline, current, decision)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSessionChanges(t *testing.T) {
	cases := []struct {
		name   string
		before func(*testing.T, string)
		after  func(*testing.T, string)
		want   string
	}{
		{"clean", nil, nil, "no_relevant_change"},
		{"unrelated", nil, func(t *testing.T, r string) { write(t, r, "notes.txt", "new note") }, "no_relevant_change"},
		{"changed asset", nil, func(t *testing.T, r string) { write(t, r, "src/a.go", "new") }, "needs_review"},
		{"untracked asset", nil, func(t *testing.T, r string) { write(t, r, "src/new.go", "new") }, "needs_review"},
		{"deleted asset", nil, func(t *testing.T, r string) {
			if err := os.Remove(filepath.Join(r, "src/a.go")); err != nil {
				t.Fatal(err)
			}
		}, "needs_review"},
		{"renamed asset", nil, func(t *testing.T, r string) { git(t, r, "mv", "src/a.go", "src/b.go") }, "needs_review"},
		{"committed edit", nil, func(t *testing.T, r string) {
			write(t, r, "src/a.go", "committed")
			git(t, r, "add", "src/a.go")
			git(t, r, "commit", "-qm", "edit")
		}, "needs_review"},
		{"committed delete", nil, func(t *testing.T, r string) { git(t, r, "rm", "-q", "src/a.go"); git(t, r, "commit", "-qm", "delete") }, "needs_review"},
		{"committed rename", nil, func(t *testing.T, r string) {
			git(t, r, "mv", "src/a.go", "src/b.go")
			git(t, r, "commit", "-qm", "rename")
		}, "needs_review"},
		{"preexisting dirty", func(t *testing.T, r string) { write(t, r, "src/a.go", "earlier") }, nil, "no_relevant_change"},
		{"preexisting dirty committed unchanged", func(t *testing.T, r string) { write(t, r, "src/a.go", "earlier") }, func(t *testing.T, r string) {
			git(t, r, "add", "src/a.go")
			git(t, r, "commit", "-qm", "record existing edit")
		}, "no_relevant_change"},
		{"preexisting rename committed unchanged", func(t *testing.T, r string) { git(t, r, "mv", "src/a.go", "src/b.go") }, func(t *testing.T, r string) { git(t, r, "commit", "-qm", "record existing rename") }, "no_relevant_change"},
		{"preexisting rename changed then committed", func(t *testing.T, r string) { git(t, r, "mv", "src/a.go", "src/b.go") }, func(t *testing.T, r string) {
			write(t, r, "src/b.go", "new content")
			git(t, r, "add", "src/b.go")
			git(t, r, "commit", "-qm", "change rename")
		}, "needs_review"},
		{"preexisting untracked", func(t *testing.T, r string) { write(t, r, "src/earlier.go", "earlier") }, nil, "no_relevant_change"},
		{"preexisting untracked committed unchanged", func(t *testing.T, r string) { write(t, r, "src/earlier.go", "earlier") }, func(t *testing.T, r string) {
			git(t, r, "add", "src/earlier.go")
			git(t, r, "commit", "-qm", "record existing file")
		}, "no_relevant_change"},
		{"changed after dirty", func(t *testing.T, r string) { write(t, r, "src/a.go", "earlier") }, func(t *testing.T, r string) { write(t, r, "src/a.go", "now") }, "needs_review"},
		{"old log edit", func(t *testing.T, r string) { write(t, r, "knowledge/log.md", "earlier log") }, func(t *testing.T, r string) { write(t, r, "src/a.go", "new") }, "needs_review"},
		{"excluded noise", nil, func(t *testing.T, r string) { write(t, r, "src/generated/out.go", "noise") }, "no_relevant_change"},
		{"newline path", nil, func(t *testing.T, r string) { write(t, r, "src/a\nb.go", "new") }, "needs_review"},
		{"committed newline path", nil, func(t *testing.T, r string) {
			write(t, r, "src/a\nb.go", "new")
			git(t, r, "add", "src/a\nb.go")
			git(t, r, "commit", "-qm", "newline path")
		}, "needs_review"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: commit an initial repository and capture the work boundary.
			root, cfg := fixture(t)
			if tc.before != nil {
				tc.before(t, root)
			}
			base, err := Snapshot(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			// Act: change the repository and inspect only the current session delta.
			if tc.after != nil {
				tc.after(t, root)
			}
			result := checkFixture(t, cfg, base, nil)
			// Assert: old dirt and excluded paths do not create a review claim.
			if result.Status != tc.want {
				t.Fatalf("status=%s, want %s, changes=%+v", result.Status, tc.want, result.Changes)
			}
		})
	}
}

func TestDecisionMustMatchCurrentFingerprint(t *testing.T) {
	// Arrange: a changed source, an old log edit, and a matching decision.
	root, cfg := fixture(t)
	write(t, root, "knowledge/log.md", "preexisting")
	base, err := Snapshot(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	write(t, root, "src/a.go", "new source")
	first := checkFixture(t, cfg, base, nil)
	// Act: assert explicit unaffected, then change the source again.
	d := &Decision{Fingerprint: first.Fingerprint, Kind: "unaffected", Reason: "Generated code has no architecture change."}
	accepted := checkFixture(t, cfg, base, d)
	write(t, root, "src/a.go", "another source")
	stale := checkFixture(t, cfg, base, d)
	// Assert: the reason binds to precisely the inspected delta.
	if accepted.Status != "explicit_unaffected" || stale.Status != "needs_review" {
		t.Fatalf("accepted=%s stale=%s", accepted.Status, stale.Status)
	}
}

func TestUpdatedRequiresFreshConceptNotOldLog(t *testing.T) {
	// Arrange: a relevant code edit and an already dirty log.
	root, cfg := fixture(t)
	write(t, root, "knowledge/log.md", "preexisting")
	base, err := Snapshot(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	write(t, root, "src/a.go", "changed")
	first := checkFixture(t, cfg, base, nil)
	// Act: try old log as evidence, then update a concept and bind a new decision.
	oldLog := checkFixture(t, cfg, base, &Decision{Fingerprint: first.Fingerprint, Kind: "updated", UpdatedConcepts: []string{"knowledge/log.md"}})
	write(t, root, "knowledge/architecture.md", "checked new source")
	newState := checkFixture(t, cfg, base, nil)
	updated := checkFixture(t, cfg, base, &Decision{Fingerprint: newState.Fingerprint, Kind: "updated", UpdatedConcepts: []string{"knowledge/architecture.md"}})
	if err := os.Remove(filepath.Join(root, "knowledge/architecture.md")); err != nil {
		t.Fatal(err)
	}
	deleted := checkFixture(t, cfg, base, &Decision{Fingerprint: newState.Fingerprint, Kind: "updated", UpdatedConcepts: []string{"knowledge/architecture.md"}})
	// Assert: an old log edit cannot satisfy the gate; a fresh concept can.
	if oldLog.Status != "needs_review" || updated.Status != "reviewed_updated" || deleted.Status != "needs_review" {
		t.Fatalf("old=%s updated=%s deleted=%s", oldLog.Status, updated.Status, deleted.Status)
	}
}

func TestConfigValidation(t *testing.T) {
	// Arrange: a config with a path that escapes the repository.
	root := t.TempDir()
	write(t, root, "config.json", `{"repo_root":".","bundle_root":"knowledge","relevant_paths":["../outside"]}`)
	// Act: read the config.
	_, err := LoadConfig(filepath.Join(root, "config.json"))
	// Assert: no path traversal is accepted.
	if err == nil || !strings.Contains(err.Error(), "relative path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSnapshotTimeout(t *testing.T) {
	// Arrange: a Git executable that stalls longer than the configured bound.
	_, cfg := fixture(t)
	bin := t.TempDir()
	gitPath := filepath.Join(bin, "git")
	if err := os.WriteFile(gitPath, []byte("#!/bin/sh\nsleep 2\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	cfg.TimeoutMS = 10
	// Act: take a snapshot with the bounded runner.
	start := time.Now()
	_, err := Snapshot(context.Background(), cfg)
	// Assert: it aborts rather than hanging on the host command.
	if err == nil {
		t.Fatal("expected timeout")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("timeout exceeded bound: %s", time.Since(start))
	}
}

func TestSymlinkParentCannotEscapeRepository(t *testing.T) {
	// Arrange: replace a tracked parent directory with a link to an external file.
	root, _ := fixture(t)
	outside := t.TempDir()
	write(t, outside, "secret.go", "external secret")
	if err := os.Rename(filepath.Join(root, "src"), filepath.Join(root, "src-old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "src")); err != nil {
		t.Fatal(err)
	}
	// Act: try to hash a Git-like path under the replaced parent.
	_, err := contentsHash(context.Background(), root, "src/secret.go")
	// Assert: no outside content is read or hashed.
	if err == nil {
		t.Fatal("symlink parent unexpectedly accepted")
	}
}

func TestBaselineSessionAndStateBinding(t *testing.T) {
	// Arrange: capture a session and a later relevant change.
	root, cfg := fixture(t)
	base, err := Snapshot(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	write(t, root, "src/a.go", "changed")
	current, err := Snapshot(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Act: alter the baseline state, then expire the original session.
	corrupt := base
	corrupt.Entries = []Entry{{Path: "src/a.go", Status: " M", Contents: "fabricated"}}
	_, _, corruptErr := Changes(cfg, corrupt, current)
	expired := base
	expired.CapturedAt = expired.CapturedAt.Add(-25 * time.Hour)
	_, _, expiredErr := Changes(cfg, expired, current)
	// Assert: neither an altered nor stale baseline can support a review claim.
	if corruptErr == nil || expiredErr == nil {
		t.Fatalf("corrupt=%v expired=%v", corruptErr, expiredErr)
	}
}

func TestCommittedConceptCountsAsFreshReview(t *testing.T) {
	// Arrange: capture baseline before both code and concept are committed.
	root, cfg := fixture(t)
	base, err := Snapshot(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	write(t, root, "src/a.go", "new behavior")
	write(t, root, "knowledge/architecture.md", "new concept")
	git(t, root, "add", "src/a.go", "knowledge/architecture.md")
	git(t, root, "commit", "-qm", "document change")
	// Act: inspect the clean tree after the commit and submit the concept decision.
	first := checkFixture(t, cfg, base, nil)
	updated := checkFixture(t, cfg, base, &Decision{Fingerprint: first.Fingerprint, Kind: "updated", UpdatedConcepts: []string{"knowledge/architecture.md"}})
	// Assert: committed work remains visible and the updated concept is fresh.
	if first.Status != "needs_review" || updated.Status != "reviewed_updated" {
		t.Fatalf("first=%s updated=%s", first.Status, updated.Status)
	}
}

func TestRepositoryPathWithTrailingWhitespaceAndNewline(t *testing.T) {
	// Arrange: Git worktree path ending with significant whitespace and newline.
	root := filepath.Join(t.TempDir(), "repo \n")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	git(t, root, "init", "-q")
	cfg := Config{RepoRoot: root, BundleRoot: "knowledge", RelevantPaths: []string{"src"}, TimeoutMS: 5000, MaxSessionMinutes: 1440}
	// Act: capture a baseline and resolve the worktree root through Git.
	snap, err := Snapshot(context.Background(), cfg)
	// Assert: the trailing newline belongs to the path, not to Git's terminator.
	if err != nil || snap.RepositoryID == "" {
		t.Fatalf("snapshot path %q: %v", root, err)
	}
}

func TestOldConceptCommitDoesNotCountAsReview(t *testing.T) {
	// Arrange: concept content was already dirty before the work session.
	root, cfg := fixture(t)
	write(t, root, "knowledge/architecture.md", "old pending edit")
	base, err := Snapshot(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	write(t, root, "src/a.go", "new code")
	git(t, root, "add", "knowledge/architecture.md")
	git(t, root, "commit", "-qm", "commit old concept")
	// Act: claim the old concept commit as an update for the new code.
	first := checkFixture(t, cfg, base, nil)
	claimed := checkFixture(t, cfg, base, &Decision{Fingerprint: first.Fingerprint, Kind: "updated", UpdatedConcepts: []string{"knowledge/architecture.md"}})
	// Assert: the concept did not change after baseline, so review remains needed.
	if first.Status != "needs_review" || claimed.Status != "needs_review" {
		t.Fatalf("first=%s claimed=%s", first.Status, claimed.Status)
	}
}

func TestCommittedChangeSurvivesWorktreeRevertToBaseline(t *testing.T) {
	// Arrange: a preexisting dirty worktree file A at baseline.
	root, cfg := fixture(t)
	write(t, root, "src/a.go", "A")
	base, err := Snapshot(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Act: commit new content B, then make the worktree look like baseline A.
	write(t, root, "src/a.go", "B")
	git(t, root, "add", "src/a.go")
	git(t, root, "commit", "-qm", "commit B")
	write(t, root, "src/a.go", "A")
	r := checkFixture(t, cfg, base, nil)
	// Assert: the committed layer changed even though worktree bytes match A.
	if r.Status != "needs_review" {
		t.Fatalf("status=%s changes=%+v", r.Status, r.Changes)
	}
}

func TestCommittedConceptReviewSurvivesWorktreeRevert(t *testing.T) {
	// Arrange: old concept A and source code are dirty at baseline.
	root, cfg := fixture(t)
	write(t, root, "knowledge/architecture.md", "A")
	base, err := Snapshot(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Act: commit a revised concept B and code change, then restore A in worktree.
	write(t, root, "knowledge/architecture.md", "B")
	write(t, root, "src/a.go", "new code")
	git(t, root, "add", "knowledge/architecture.md", "src/a.go")
	git(t, root, "commit", "-qm", "code and concept B")
	write(t, root, "knowledge/architecture.md", "A")
	first := checkFixture(t, cfg, base, nil)
	decision := &Decision{Fingerprint: first.Fingerprint, Kind: "updated", UpdatedConcepts: []string{"knowledge/architecture.md"}}
	result := checkFixture(t, cfg, base, decision)
	// Assert: committed B is fresh evidence independent of worktree A.
	if result.Status != "reviewed_updated" {
		t.Fatalf("status=%s evidence=%v", result.Status, result.Evidence)
	}
}
