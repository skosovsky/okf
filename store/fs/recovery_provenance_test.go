package fs

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skosovsky/okf/store"
)

// The gate must decide provenance before staged payload I/O. In particular a
// sparse hostile file is metadata, not an invitation to allocate its logical
// length in readVisibleRoot.
func TestRecoveryProvenanceGateRejectsBeforeStagedPayloadRead(t *testing.T) {
	setup := func(t *testing.T) (string, *Store, string) {
		t.Helper()
		root, s := adversarialStore(t, Config{})
		base := adversarialSnapshot(t, s)
		next, err := newSnapshot(context.Background(), map[string][]byte{
			"a.md": []byte(adversarialDocument("result")), "b.md": []byte(adversarialDocument("B")),
		})
		if err != nil {
			t.Fatal(err)
		}
		receipt := testJournalReceipt(base.Revision(), next.Revision(), "streaming-provenance", "")
		j, raw, err := s.prepareJournalContext(context.Background(), next, receipt, replaceReplay{}, productionJournalManifestByteLimits())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.stageJournalPayloadsObserved(context.Background(), next, j); err != nil {
			t.Fatal(err)
		}
		jp, err := journalPath(receipt.RequestDigest)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.writeJournalObserved(context.Background(), jp, raw); err != nil {
			t.Fatal(err)
		}
		// A missing payload would normally fail recovery; provenance must win.
		stage, err := journalStage(receipt.RequestDigest)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(stage), "payload-00000")); err != nil {
			t.Fatal(err)
		}
		return root, s, jp
	}

	for _, tt := range []struct {
		name   string
		mutate func(t *testing.T, root string)
		path   string
		reads  int
	}{
		{"extra sparse", func(t *testing.T, root string) {
			makeSparse(t, filepath.Join(root, "extra.md"))
		}, "extra.md", 0},
		{"known size mismatch", func(t *testing.T, root string) {
			if err := os.Truncate(filepath.Join(root, "a.md"), 4<<30); err != nil {
				t.Fatal(err)
			}
		}, "a.md", 0},
		{"same size wrong bytes", func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, "a.md"), bytes.Repeat([]byte("x"), len(adversarialDocument("A"))), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "a.md", 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root, s, jp := setup(t)
			defer s.Close()
			tt.mutate(t, root)
			reads := 0
			s.provenanceReadHook = func(name string) {
				if name == tt.path {
					reads++
				}
			}
			err := s.recoverForTest(context.Background())
			if !errors.Is(err, store.ErrStorageCorrupt) || !strings.Contains(err.Error(), "base/result state mismatch") {
				t.Fatalf("recover=%v, want provenance corruption", err)
			}
			if reads != tt.reads {
				t.Fatalf("reads(%s)=%d want=%d", tt.path, reads, tt.reads)
			}
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(jp))); err != nil {
				t.Fatalf("journal was not preserved: %v", err)
			}
		})
	}
}

func makeSparse(t *testing.T, name string) {
	t.Helper()
	f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(4 << 30); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
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
	j, raw, err := s.prepareJournalContext(context.Background(), next, receipt, replaceReplay{}, productionJournalManifestByteLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.stageJournalPayloadsObserved(context.Background(), next, j); err != nil {
		t.Fatal(err)
	}
	jp, err := journalPath(receipt.RequestDigest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.writeJournalObserved(context.Background(), jp, raw); err != nil {
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
	err = s.recoverForTest(context.Background())
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
