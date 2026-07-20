package fs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
)

func TestFilesystemCaseCapabilityGuardsPublicMutations(t *testing.T) {
	newStore := func(t *testing.T) (string, *Store) {
		t.Helper()
		root := t.TempDir()
		writeTestFile(t, root, "a.md", adversarialDocument("original"))
		s, err := Open(root, Config{})
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

	// Forge a complete v5 transaction after capturing the current bytes. The
	// recovery reader must reject aliases before opening either payload, leaving
	// both the journal and the visible source untouched.
	raw, err := encodeJournalFixture(t, root, next, receipt)
	if err != nil {
		t.Fatal(err)
	}
	jp, err := journalPath(receipt.RequestDigest)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.writePrivateDurableAt(jp, raw); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(root, "a.md"))
	if err != nil {
		t.Fatal(err)
	}
	err = s.recoverContext(context.Background())
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

func TestJournalPayloadLimitsRejectOverflowBeforePayloadRead(t *testing.T) {
	base := store.Revision("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	result := store.Revision("sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	receipt := testJournalReceipt(base, result, "payload-overflow", "")
	stage, err := journalStage(receipt.RequestDigest)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := journalBaseBinding(receipt, nil)
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb"
	for _, files := range [][]journalFile{
		{{Path: "a.md", Payload: "payload-00000", Size: math.MaxInt64, Digest: digest}},
		{{Path: "a.md", Payload: "payload-00000", Size: DefaultMaxStagedPayloadBytes, Digest: digest}, {Path: "b.md", Payload: "payload-00001", Size: 1, Digest: digest}},
	} {
		raw, marshalErr := json.Marshal(journal{Version: 5, HashAlgorithm: "sha256", Stage: stage, Base: []journalBaseFile{}, BaseBinding: binding, Files: files, Receipt: receipt})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if _, decodeErr := decodeJournal(raw); decodeErr == nil {
			t.Fatal("accepted oversized or aggregate-overflow manifest")
		}
	}
}

func TestConfiguredStagePayloadLimitsRejectBeforePayloadWrite(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "a.md", adversarialDocument("base"))
	s, err := Open(root, Config{MaxStagedPayloadBytes: 8, MaxStagedTransactionBytes: 12})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	base := adversarialSnapshot(t, s)
	for _, files := range []map[string][]byte{
		{"a.md": bytes.Repeat([]byte("a"), 9)},
		{"a.md": bytes.Repeat([]byte("a"), 8), "b.md": bytes.Repeat([]byte("b"), 5)},
	} {
		next, snapshotErr := newSnapshot(context.Background(), files)
		if snapshotErr != nil {
			t.Fatal(snapshotErr)
		}
		receipt := testJournalReceipt(base.Revision(), next.Revision(), "configured-stage-limit", "")
		_, stageErr := s.stageJournal(next, receipt, replaceReplay{})
		if !errors.Is(stageErr, store.ErrInvalidChangeSet) {
			t.Fatalf("stage error = %v, want InvalidChangeSet", stageErr)
		}
		stage, pathErr := journalStage(receipt.RequestDigest)
		if pathErr != nil {
			t.Fatal(pathErr)
		}
		if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(stage))); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("limit rejection wrote staged payload: %v", statErr)
		}
	}
}

func TestCaseProbeIsPrivateAndCleanAcrossReopen(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "a.md", adversarialDocument("visible"))
	s, err := Open(root, Config{})
	if err != nil {
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
	reopened, err := Open(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
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
	_, err := Open(root, Config{})
	if err == nil {
		t.Fatal("Open accepted symlinked capability directory")
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

				failed, err := Open(root, cfg)
				if failed != nil {
					_ = failed.Close()
				}
				if err == nil {
					t.Fatal("Open did not surface injected capability fault")
				}
				reopened, reopenErr := Open(root, Config{})
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
	s.caseAliases = true // private seam: the actual volume capability is irrelevant.
	base := adversarialSnapshot(t, s)
	next, err := newSnapshot(context.Background(), map[string][]byte{"A.md": []byte(adversarialDocument("moved")), "b.md": []byte(adversarialDocument("B"))})
	if err != nil {
		t.Fatal(err)
	}
	receipt := testJournalReceipt(base.Revision(), next.Revision(), "recovery-case-transition", "")
	raw, err := encodeJournalFixture(t, root, next, receipt)
	if err != nil {
		t.Fatal(err)
	}
	jp, err := journalPath(receipt.RequestDigest)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.writePrivateDurableAt(jp, raw); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(root, "a.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.recoverContext(context.Background()); !errors.Is(err, store.ErrStorageCorrupt) {
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

func TestPayloadOrdinalCeiling(t *testing.T) {
	if got := payloadName(DefaultMaxStagedFiles - 1); got != "payload-99999" || !safePayloadName(got) {
		t.Fatalf("last ordinal=%q", got)
	}
	if got := payloadName(DefaultMaxStagedFiles); got != "" || safePayloadName("payload-100000") {
		t.Fatalf("ordinal ceiling not enforced: %q", got)
	}
	if err := (Config{MaxStagedFiles: DefaultMaxStagedFiles + 1}).Validate(); err == nil {
		t.Fatal("accepted oversized staged file limit")
	}
}

func TestPrivateMetadataDirectoriesAreRepairedToOwnerOnly(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{internalDirectory, path.Join(internalDirectory, "transactions"), path.Join(internalDirectory, "staging"), path.Join(internalDirectory, "receipts"), path.Join(internalDirectory, "capabilities")} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(root, dir), 0o777); err != nil {
			t.Fatal(err)
		}
	}
	s, err := Open(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	for _, dir := range []string{internalDirectory, path.Join(internalDirectory, "transactions"), path.Join(internalDirectory, "staging"), path.Join(internalDirectory, "receipts"), path.Join(internalDirectory, "capabilities")} {
		info, err := os.Stat(filepath.Join(root, dir))
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("%s mode=%#o err=%v", dir, info.Mode().Perm(), err)
		}
	}
}

func TestRecoveryRejectsEditorDriftBeforeMissingPayload(t *testing.T) {
	root, s := adversarialStore(t, Config{})
	t.Cleanup(func() { _ = s.Close() })
	base := adversarialSnapshot(t, s)
	next, err := newSnapshot(context.Background(), map[string][]byte{"a.md": []byte(adversarialDocument("result")), "b.md": []byte(adversarialDocument("B"))})
	if err != nil {
		t.Fatal(err)
	}
	receipt := testJournalReceipt(base.Revision(), next.Revision(), "drift-before-payload", "")
	raw, err := encodeJournalFixture(t, root, next, receipt)
	if err != nil {
		t.Fatal(err)
	}
	jp, err := journalPath(receipt.RequestDigest)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.writePrivateDurableAt(jp, raw); err != nil {
		t.Fatal(err)
	}
	stage, err := journalStage(receipt.RequestDigest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(path.Join(stage, "payload-00000")))); err != nil {
		t.Fatal(err)
	}
	before := []byte(adversarialDocument("editor-third-state"))
	writeTestFile(t, root, "a.md", string(before))
	err = s.recoverContext(context.Background())
	if !errors.Is(err, store.ErrStorageCorrupt) || !strings.Contains(err.Error(), "base/result state mismatch") {
		t.Fatalf("recover=%v, want provenance corruption before payload", err)
	}
	after, readErr := os.ReadFile(filepath.Join(root, "a.md"))
	if readErr != nil || !bytes.Equal(after, before) {
		t.Fatalf("current changed: %v %q", readErr, after)
	}
	if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(jp))); statErr != nil {
		t.Fatalf("journal removed: %v", statErr)
	}
}
