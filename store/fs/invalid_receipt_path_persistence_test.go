package fs

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
)

func TestWriteReceiptRejectsInvalidFileChangePathsBeforeDurability(t *testing.T) {
	for _, tc := range invalidReceiptFileChangePathCases() {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange.
			root, s, durabilityCallbacks, arm := invalidReceiptPathStore(t)
			snapshot := adversarialSnapshot(t, s)
			receipt := testJournalReceipt(
				snapshot.Revision(),
				snapshot.Revision(),
				"invalid-receipt-"+tc.name,
				store.IdempotencyKey("invalid-receipt-"+tc.name),
			)
			receipt.ChangedFiles = []store.FileChange{tc.change}
			receiptName := s.receiptPath(receipt.IdempotencyKey)

			// Act.
			arm(true)
			writeErr := s.writeReceiptWithReplay(receipt, replaceReplay{})
			arm(false)

			// Assert.
			if !errors.Is(writeErr, store.ErrStorageCorrupt) {
				t.Fatalf("writeReceiptWithReplay() error = %v, want storage corruption", writeErr)
			}
			if got := durabilityCallbacks(); got != 0 {
				t.Fatalf("invalid receipt reached %d durability callbacks", got)
			}
			assertInvalidReceiptPathLeftNoDurableArtifact(t, root, tc.value, receiptName)
		})
	}
}

func TestPublishRejectsInvalidReceiptFileChangePathsBeforeJournalSideEffects(t *testing.T) {
	for _, tc := range invalidReceiptFileChangePathCases() {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange.
			root, s, durabilityCallbacks, arm := invalidReceiptPathStore(t)
			next := adversarialSnapshot(t, s).(*snapshot)
			receipt := testJournalReceipt(
				next.Revision(),
				next.Revision(),
				"invalid-publish-"+tc.name,
				store.IdempotencyKey("invalid-publish-"+tc.name),
			)
			receipt.ChangedFiles = []store.FileChange{tc.change}
			stage, err := journalStage(receipt.RequestDigest)
			if err != nil {
				t.Fatal(err)
			}
			journalName, err := journalPath(receipt.RequestDigest)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			arm(true)
			publishErr := s.publish(context.Background(), next, receipt)
			arm(false)

			// Assert.
			if !errors.Is(publishErr, store.ErrStorageCorrupt) {
				t.Fatalf("publish() error = %v, want storage corruption", publishErr)
			}
			if got := durabilityCallbacks(); got != 0 {
				t.Fatalf("invalid publish reached %d durability callbacks", got)
			}
			assertInvalidReceiptPathLeftNoDurableArtifact(
				t,
				root,
				tc.value,
				path.Dir(stage),
				journalName,
				s.receiptPath(receipt.IdempotencyKey),
			)
		})
	}
}

func TestReceiptReplayPreservesExactValidUnicodeFileChangePaths(t *testing.T) {
	// Arrange. U+FFFD is legitimate UTF-8 here; malformed source bytes must not
	// be normalized into this value by either writer or replay decoder.
	root, s := adversarialStore(t, Config{})
	snapshot := adversarialSnapshot(t, s)
	receipt := testJournalReceipt(
		snapshot.Revision(),
		snapshot.Revision(),
		"exact-unicode-receipt",
		"exact-unicode-receipt",
	)
	receipt.ChangedRefs = make([]bundle.RelationRef, 0)
	receipt.ChangedFiles = []store.FileChange{{
		Kind: store.FileRename,
		Path: "assets/renamed-東京.bin",
		From: "assets/original-\uFFFD.bin",
	}}

	// Act.
	writeErr := s.writeReceiptWithReplay(receipt, replaceReplay{})
	replayed, found, replayErr := s.lookupReceiptContext(t.Context(), receipt.IdempotencyKey, receipt.RequestDigest)
	raw, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(s.receiptPath(receipt.IdempotencyKey))))
	decoded, decodeErr := decodeReceiptFile(raw)

	// Assert.
	if writeErr != nil || replayErr != nil || readErr != nil || decodeErr != nil || !found {
		t.Fatalf(
			"exact replay errors: write=%v lookup=%v read=%v decode=%v found=%t",
			writeErr,
			replayErr,
			readErr,
			decodeErr,
			found,
		)
	}
	if !reflect.DeepEqual(replayed, receipt) || !reflect.DeepEqual(decoded.Receipt, receipt) {
		t.Fatalf("exact receipt changed across replay:\nwant:   %#v\nlookup: %#v\ndecode: %#v", receipt, replayed, decoded.Receipt)
	}
	for _, value := range []string{receipt.ChangedFiles[0].Path, receipt.ChangedFiles[0].From} {
		if !bytes.Contains(raw, []byte(value)) {
			t.Fatalf("durable receipt does not contain exact UTF-8 path %q: %s", value, raw)
		}
	}
}

type invalidReceiptFileChangePathCase struct {
	name   string
	value  string
	change store.FileChange
}

func invalidReceiptFileChangePathCases() []invalidReceiptFileChangePathCase {
	values := []struct {
		name  string
		value string
	}{
		{name: "invalid-utf8", value: string([]byte("assets/bad-\xff.bin"))},
		{name: "control", value: "assets/bad-\x01.bin"},
		{name: "nul", value: "assets/bad-\x00.bin"},
		{name: "del", value: "assets/bad-\x7f.bin"},
	}
	cases := make([]invalidReceiptFileChangePathCase, 0, len(values)*2)
	for _, value := range values {
		cases = append(cases,
			invalidReceiptFileChangePathCase{
				name:  "path-" + value.name,
				value: value.value,
				change: store.FileChange{
					Kind: store.FileWrite,
					Path: value.value,
				},
			},
			invalidReceiptFileChangePathCase{
				name:  "rename-from-" + value.name,
				value: value.value,
				change: store.FileChange{
					Kind: store.FileRename,
					Path: "assets/renamed-" + value.name + ".bin",
					From: value.value,
				},
			},
		)
	}
	return cases
}

func invalidReceiptPathStore(t *testing.T) (string, *Store, func() int, func(bool)) {
	t.Helper()
	armed := false
	callbacks := 0
	count := func(Step) error {
		if armed {
			callbacks++
		}
		return nil
	}
	root, s := adversarialStore(t, Config{
		Fault:     count,
		PostFault: count,
		DirectorySync: func(string) error {
			if armed {
				callbacks++
			}
			return nil
		},
	})
	return root, s, func() int { return callbacks }, func(value bool) { armed = value }
}

func assertInvalidReceiptPathLeftNoDurableArtifact(t *testing.T, root, invalid string, relativeNames ...string) {
	t.Helper()
	for _, name := range relativeNames {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("invalid receipt created durable artifact %q: %v", name, err)
		}
	}
	replacement := []byte("\uFFFD")
	err := filepath.WalkDir(root, func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		if bytes.Contains(raw, replacement) {
			t.Fatalf("invalid receipt was normalized to U+FFFD in durable file %q", name)
		}
		if bytes.Contains(raw, []byte(invalid)) {
			t.Fatalf("invalid receipt path reached durable file %q", name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
