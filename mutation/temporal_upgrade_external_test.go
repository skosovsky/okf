package mutation_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/mutation"
	"github.com/skosovsky/okf/store"
	storefs "github.com/skosovsky/okf/store/fs"
)

func TestTemporalUpgradeApplyTransactionalReplayAndStalePreview(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	for path, content := range map[string][]byte{"a.md": []byte("---\ntype: Note\nstale_after: 2026-01-02\n---\nBody\n")} {
		if err := os.WriteFile(filepath.Join(root, path), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	backend, err := storefs.OpenContext(context.Background(), root, storefs.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	planner := mutation.NewTemporalUpgradePlanner()
	request := mutation.TemporalUpgradeRequest{ID: "upgrade-2", Actor: "human:reviewer", Mappings: []mutation.TemporalUpgradeMapping{{Concept: mustTemporalID(t), Path: "stale_after", From: "2026-01-02", To: "2026-01-02T12:00:00+07:00"}}}
	snapshot, err := backend.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	preview, err := planner.Preview(context.Background(), snapshot, request)
	if err != nil {
		t.Fatal(err)
	}
	apply := mutation.TemporalUpgradeApplyRequest{Request: request, BaseRevision: preview.Preview.BaseRevision, PlanDigest: preview.PlanDigest, Options: store.CommitOptions{IdempotencyKey: "retry-1"}}

	// Act.
	receipt, err := planner.Apply(context.Background(), backend, apply)
	if err != nil {
		t.Fatal(err)
	}
	replay, replayErr := planner.Apply(context.Background(), backend, apply)
	content, readErr := os.ReadFile(filepath.Join(root, "a.md"))

	// Assert.
	if replayErr != nil || replay.RequestDigest != receipt.RequestDigest || readErr != nil {
		t.Fatalf("apply/replay: %#v %#v %v %v", receipt, replay, replayErr, readErr)
	}
	if !strings.Contains(string(content), "2026-01-02T12:00:00+07:00") {
		t.Fatalf("apply did not persist upgrade: %s", content)
	}
	invalid := apply
	invalid.PlanDigest = strings.Replace(apply.PlanDigest, "a", "b", 1)
	if invalid.PlanDigest == apply.PlanDigest {
		invalid.PlanDigest = apply.PlanDigest[:len(apply.PlanDigest)-1] + "0"
	}
	if _, err := planner.Apply(context.Background(), backend, invalid); err == nil {
		t.Fatal("tampered proof accepted")
	}
}

func mustTemporalID(t *testing.T) bundle.ConceptID {
	t.Helper()
	id, err := bundle.ParseConceptID("a")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestTemporalUpgradePreviewIsReadOnlyAndStaleApplyCannotPublish(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	file := filepath.Join(root, "a.md")
	if err := os.WriteFile(file, []byte("---\ntype: Note\nstale_after: 2026-01-02\n---\nBody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := mutation.TemporalUpgradeRequest{ID: "upgrade-stale", Actor: "human:reviewer", Mappings: []mutation.TemporalUpgradeMapping{{Concept: mustTemporalID(t), Path: "stale_after", From: "2026-01-02", To: "2026-01-02T12:00:00Z"}}}
	planner := mutation.NewTemporalUpgradePlanner()

	// Act.
	preview, err := planner.Preview(context.Background(), &bundle.FileSystemSource{Root: root}, request)
	if err != nil {
		t.Fatal(err)
	}
	_, statErr := os.Stat(filepath.Join(root, ".okf"))
	before := []byte("---\ntype: Note\nstale_after: 2026-01-02\n---\nConcurrent edit\n")
	if err := os.WriteFile(file, before, 0o644); err != nil {
		t.Fatal(err)
	}
	backend, err := storefs.OpenContext(context.Background(), root, storefs.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	_, applyErr := planner.Apply(context.Background(), backend, mutation.TemporalUpgradeApplyRequest{Request: request, BaseRevision: preview.Preview.BaseRevision, PlanDigest: preview.PlanDigest})
	after, readErr := os.ReadFile(file)

	// Assert.
	if !os.IsNotExist(statErr) {
		t.Fatalf("preview created store metadata: %v", statErr)
	}
	if applyErr == nil || readErr != nil || string(after) != string(before) {
		t.Fatalf("stale apply changed file: %v %v %s", applyErr, readErr, after)
	}
}
