package fs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
)

func TestFilesystemCaseCapabilityGuardsPublicMutations(t *testing.T) {
	newStore := func(t *testing.T) (string, *Store) {
		t.Helper()
		root := t.TempDir()
		writeTestFile(t, root, "a.md", adversarialDocument("original"))
		s, err := openObserved(root, Config{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return root, s
	}
	parse := func(t *testing.T, raw string) bundle.Document {
		t.Helper()
		document, err := bundle.ParseDocument(raw)
		if err != nil {
			t.Fatal(err)
		}
		return document
	}

	t.Run("move", func(t *testing.T) {
		root, s := newStore(t)
		base := adversarialSnapshot(t, s)
		before, err := os.ReadFile(filepath.Join(root, "a.md"))
		if err != nil {
			t.Fatal(err)
		}
		change := store.ChangeSet{Version: store.ChangeSetFormatVersion, ID: "case-capability-move", Actor: "tester", BaseRevision: base.Revision(), Operations: []store.Operation{store.MoveConcept{From: adversarialRef(t, "a").ID, To: adversarialRef(t, "A").ID}}}

		_, err = s.Commit(context.Background(), change, store.CommitOptions{})
		if s.caseAliases {
			if !errors.Is(err, store.ErrInvalidChangeSet) {
				t.Fatalf("case-folding filesystem move error = %v, want InvalidChangeSet", err)
			}
			after := adversarialSnapshot(t, s)
			got, readErr := os.ReadFile(filepath.Join(root, "a.md"))
			if readErr != nil || string(got) != string(before) || after.Revision() != base.Revision() {
				t.Fatalf("rejected move changed visible state: read=%v bytes=%q revision=%s", readErr, got, after.Revision())
			}
			if _, statErr := os.Stat(filepath.Join(root, internalDirectory, "transactions")); statErr == nil {
				entries, readErr := os.ReadDir(filepath.Join(root, internalDirectory, "transactions"))
				if readErr != nil || len(entries) != 0 {
					t.Fatalf("rejected move reached journal: entries=%v err=%v", entries, readErr)
				}
			}
			return
		}
		if err != nil {
			t.Fatalf("case-sensitive filesystem move error = %v", err)
		}
		if _, statErr := os.Stat(filepath.Join(root, "a.md")); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("a.md remains after case-only move: %v", statErr)
		}
		got, readErr := os.ReadFile(filepath.Join(root, "A.md"))
		if readErr != nil || string(got) != string(before) {
			t.Fatalf("A.md after move: read=%v bytes=%q", readErr, got)
		}
	})

	t.Run("replace", func(t *testing.T) {
		root, s := newStore(t)
		base := adversarialSnapshot(t, s)
		before, err := os.ReadFile(filepath.Join(root, "a.md"))
		if err != nil {
			t.Fatal(err)
		}
		req := ReplaceConceptRequest{ChangeSetID: "case-capability-replace", Actor: "tester", BaseRevision: base.Revision(), ConceptID: adversarialRef(t, "A").ID, Document: parse(t, adversarialDocument("replacement"))}

		_, err = s.ReplaceConcept(context.Background(), req, store.CommitOptions{})
		if s.caseAliases {
			if !errors.Is(err, store.ErrInvalidChangeSet) {
				t.Fatalf("case-folding filesystem replace error = %v, want InvalidChangeSet", err)
			}
			after := adversarialSnapshot(t, s)
			got, readErr := os.ReadFile(filepath.Join(root, "a.md"))
			if readErr != nil || string(got) != string(before) || after.Revision() != base.Revision() {
				t.Fatalf("rejected replace changed visible state: read=%v bytes=%q revision=%s", readErr, got, after.Revision())
			}
			return
		}
		if err != nil {
			t.Fatalf("case-sensitive filesystem replace error = %v", err)
		}
		gotA, readErr := os.ReadFile(filepath.Join(root, "A.md"))
		if readErr != nil || string(gotA) != adversarialDocument("replacement") {
			t.Fatalf("A.md after replace: read=%v bytes=%q", readErr, gotA)
		}
		gotLower, readErr := os.ReadFile(filepath.Join(root, "a.md"))
		if readErr != nil || string(gotLower) != string(before) {
			t.Fatalf("a.md after replace: read=%v bytes=%q", readErr, gotLower)
		}
	})
}

func TestForcedFilesystemAliasesRejectStageAndRecovery(t *testing.T) {
	root, s := adversarialStore(t, Config{})
	t.Cleanup(func() { _ = s.Close() })
	s.caseAliases, s.normalizationAliases = true, true // private test seam: model a normalizing, case-folding volume.
	base := adversarialSnapshot(t, s)
	next, err := newSnapshot(context.Background(), map[string][]byte{"A.md": []byte(adversarialDocument("upper")), "a.md": []byte(adversarialDocument("lower")), "b.md": []byte(adversarialDocument("B"))})
	if err != nil {
		t.Fatal(err)
	}
	receipt := testJournalReceipt(base.Revision(), next.Revision(), "forced-alias", "")

	// Arrange / Act / Assert: no payload or journal may be written for a
	// rejected plan. NFC exercises composed/decomposed aliases in addition to
	// the ASCII case-fold collision used by recovery below.
	if err := s.validateFilesystemPathAliases(map[string][]byte{"\u00e9.md": nil, "e\u0301.md": nil}); !errors.Is(err, store.ErrInvalidChangeSet) {
		t.Fatalf("NFC aliases error = %v, want InvalidChangeSet", err)
	}
	if _, err := s.stageJournal(next, receipt, replaceReplay{}); !errors.Is(err, store.ErrInvalidChangeSet) {
		t.Fatalf("stage aliases error = %v, want InvalidChangeSet", err)
	}
	stage, stageErr := journalStage(receipt.RequestDigest)
	if stageErr != nil {
		t.Fatal(stageErr)
	}
	if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(stage))); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("rejected stage left payload directory: %v", statErr)
	}

	// Publish a non-aliasing transaction through the live proof protocol, then
	// mutate the installed journal inode into an aliasing manifest. Recovery
	// must reject aliases before opening either payload.
	liveNext, err := newSnapshot(context.Background(), map[string][]byte{"c.md": []byte(adversarialDocument("upper")), "b.md": []byte(adversarialDocument("B"))})
	if err != nil {
		t.Fatal(err)
	}
	liveReceipt := testJournalReceipt(base.Revision(), liveNext.Revision(), "forced-alias-recovery", "")
	jp, raw := adversarialLiveDurableJournal(t, root, s, liveNext, liveReceipt)
	var durable journal
	if err := json.Unmarshal(raw, &durable); err != nil {
		t.Fatal(err)
	}
	for i := range durable.Files {
		if durable.Files[i].Path == "c.md" {
			durable.Files[i].Path = "A.md"
		}
	}
	mutated, err := json.Marshal(durable)
	if err != nil {
		t.Fatal(err)
	}
	adversarialRewriteSameInode(t, filepath.Join(root, filepath.FromSlash(jp)), mutated)
	before, err := os.ReadFile(filepath.Join(root, "a.md"))
	if err != nil {
		t.Fatal(err)
	}
	err = s.recoverForTest(context.Background())
	if !errors.Is(err, store.ErrStorageCorrupt) {
		t.Fatalf("aliased recovery error = %v, want storage corruption", err)
	}
	after, readErr := os.ReadFile(filepath.Join(root, "a.md"))
	if readErr != nil || string(after) != string(before) {
		t.Fatalf("aliased recovery changed current: read=%v bytes=%q", readErr, after)
	}
	if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(jp))); statErr != nil {
		t.Fatalf("aliased recovery removed journal: %v", statErr)
	}
}

func TestCaseProbeIsPrivateAndCleanAcrossReopen(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "a.md", adversarialDocument("visible"))
	s, err := openObserved(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	visible, readErr := readVisibleRoot(context.Background(), s.rootFD)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(visible) != 1 || string(visible["a.md"]) != adversarialDocument("visible") {
		t.Fatalf("probe entered revision-visible source: %#v", visible)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openObserved(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if _, err := reopened.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"case-probe-a", "CASE-PROBE-A"} {
		if _, statErr := os.Stat(filepath.Join(root, internalDirectory, "capabilities", name)); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("case probe leaked after reopen: %s: %v", name, statErr)
		}
	}
}

func TestCaseProbeSymlinkCannotTraverseOutsideRoot(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	writeTestFile(t, root, "a.md", adversarialDocument("visible"))
	if err := os.MkdirAll(filepath.Join(root, internalDirectory), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, internalDirectory, "capabilities")); err != nil {
		t.Fatal(err)
	}
	s, err := openObserved(root, Config{})
	if s != nil {
		_ = s.Close()
	}
	if err == nil {
		t.Fatal("Snapshot accepted symlinked capability directory")
	}
	if _, statErr := os.Stat(filepath.Join(outside, "case-probe-a")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("case probe traversed symlink outside root: %v", statErr)
	}
}

func TestCapabilityProbeFaultHooksRecoverAndClean(t *testing.T) {
	steps := []Step{
		StepCapabilityMkdir, StepCapabilityChmod, StepCapabilityFileWrite,
		StepCapabilityFileSync, StepCapabilityFileClose, StepCapabilityRename,
		StepCapabilityRemove, StepCapabilityDirectorySync,
	}
	for _, post := range []bool{false, true} {
		for _, step := range steps {
			t.Run(string(step), func(t *testing.T) {
				root := t.TempDir()
				writeTestFile(t, root, "a.md", adversarialDocument("visible"))
				inject := func(candidate Step) error {
					if candidate == step {
						return errors.New("capability probe fault")
					}
					return nil
				}
				cfg := Config{}
				if post {
					cfg.PostFault = inject
				} else {
					cfg.Fault = inject
				}

				failed, err := openObserved(root, cfg)
				if failed != nil {
					_ = failed.Close()
				}
				if err == nil {
					t.Fatal("Snapshot did not surface injected capability fault")
				}
				reopened, reopenErr := openObserved(root, Config{})
				if reopenErr != nil {
					t.Fatalf("reopen after capability fault: %v", reopenErr)
				}
				defer reopened.Close()
				for _, name := range []string{"case-probe-a", "CASE-PROBE-A"} {
					if _, statErr := os.Stat(filepath.Join(root, internalDirectory, "capabilities", name)); !errors.Is(statErr, os.ErrNotExist) {
						t.Fatalf("capability fault leaked probe %q: %v", name, statErr)
					}
				}
			})
		}
	}
}

func TestRecoveryRejectsAliasedBaseToResultBeforePayloadRead(t *testing.T) {
	root, s := adversarialStore(t, Config{})
	t.Cleanup(func() { _ = s.Close() })
	base := adversarialSnapshot(t, s)
	next, err := newSnapshot(context.Background(), map[string][]byte{"c.md": []byte(adversarialDocument("moved")), "b.md": []byte(adversarialDocument("B"))})
	if err != nil {
		t.Fatal(err)
	}
	receipt := testJournalReceipt(base.Revision(), next.Revision(), "recovery-case-transition", "")
	jp, err := journalPath(receipt.RequestDigest)
	if err != nil {
		t.Fatal(err)
	}
	crash := errors.New("retain live alias journal")
	s.config.PostFault = func(step Step) error {
		if step == StepJournalDirectorySync {
			return crash
		}
		return nil
	}
	if publishErr := s.publish(context.Background(), next, receipt); !errors.Is(publishErr, crash) {
		t.Fatalf("live durable evidence=%v", publishErr)
	}
	s.config.PostFault = nil
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(jp)))
	if err != nil {
		t.Fatal(err)
	}
	j, err := decodeJournal(raw)
	if err != nil {
		t.Fatal(err)
	}
	for i := range j.Files {
		if j.Files[i].Path == "c.md" {
			j.Files[i].Path = "A.md"
		}
	}
	mutated, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(root, filepath.FromSlash(jp)), os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(mutated); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	s.caseAliases = true // private seam: model the recovery volume after durable capture.
	before, err := os.ReadFile(filepath.Join(root, "a.md"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Snapshot(context.Background()); !errors.Is(err, store.ErrStorageCorrupt) {
		t.Fatalf("recover error=%v want corruption", err)
	}
	after, err := os.ReadFile(filepath.Join(root, "a.md"))
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("current changed: %v %q", err, after)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(jp))); err != nil {
		t.Fatalf("journal removed: %v", err)
	}
}
