package fs

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
)

func TestAdversarialCommitStaleBaseAndIdempotency(t *testing.T) {
	// Arrange.
	_, s := adversarialStore(t, Config{})
	base := adversarialSnapshot(t, s)
	first := adversarialChange(t, "first", base.Revision(), "depends_on")
	stale := adversarialChange(t, "stale", base.Revision(), "blocks")

	// Act.
	receipt, err := s.Commit(context.Background(), first, store.CommitOptions{IdempotencyKey: "retry-1"})
	replay, replayErr := s.Commit(context.Background(), first, store.CommitOptions{IdempotencyKey: "retry-1"})
	_, staleErr := s.Commit(context.Background(), stale, store.CommitOptions{})
	different := first
	different.ID = "different-payload"
	different.Operations = []store.Operation{store.EnsureRelation{Source: adversarialRef(t, "a"), Type: "blocks", Target: adversarialRef(t, "b")}}
	_, keyErr := s.Commit(context.Background(), different, store.CommitOptions{IdempotencyKey: "retry-1"})

	// Assert.
	if err != nil {
		t.Fatalf("first Commit() error = %v", err)
	}
	if receipt.BaseRevision != base.Revision() || receipt.ResultRevision == base.Revision() {
		t.Fatalf("receipt revisions = %s -> %s, base = %s", receipt.BaseRevision, receipt.ResultRevision, base.Revision())
	}
	if replayErr != nil || replay.ResultRevision != receipt.ResultRevision || !replay.CommitTime.Equal(receipt.CommitTime) {
		t.Fatalf("idempotent replay = %#v, err=%v; want %#v", replay, replayErr, receipt)
	}
	var conflict *store.Conflict
	if !errors.As(staleErr, &conflict) || !errors.Is(staleErr, store.ErrConflict) {
		t.Fatalf("stale base error = %v, want store conflict", staleErr)
	}
	var keyConflict *store.IdempotencyConflict
	if !errors.As(keyErr, &keyConflict) || !errors.Is(keyErr, store.ErrIdempotencyConflict) {
		t.Fatalf("reused key error = %v, want idempotency conflict", keyErr)
	}
}

func TestCancelledValidationDoesNotPublishPreviewCommitOrReplace(t *testing.T) {
	// Arrange.
	_, s := adversarialStore(t, Config{})
	base := adversarialSnapshot(t, s)
	before, err := base.ReadFile(context.Background(), "a.md")
	if err != nil {
		t.Fatal(err)
	}
	change := adversarialChange(t, "cancelled-validation", base.Revision(), "depends_on")
	document, err := bundle.ParseDocument(adversarialDocument("replacement"))
	if err != nil {
		t.Fatal(err)
	}
	replace := ReplaceConceptRequest{ChangeSetID: "cancelled-replace", Actor: "adversary-test", BaseRevision: base.Revision(), ConceptID: adversarialRef(t, "a").ID, Document: document}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Act.
	_, previewErr := s.Preview(ctx, change)
	_, commitErr := s.Commit(ctx, change, store.CommitOptions{IdempotencyKey: "cancelled-commit"})
	_, replaceErr := s.ReplaceConcept(ctx, replace, store.CommitOptions{IdempotencyKey: "cancelled-replace"})
	after := adversarialSnapshot(t, s)
	afterBytes, readErr := after.ReadFile(context.Background(), "a.md")
	_, found, receiptErr := s.lookupReceipt("cancelled-commit", "")

	// Assert.
	for name, callErr := range map[string]error{"Preview": previewErr, "Commit": commitErr, "ReplaceConcept": replaceErr} {
		if !errors.Is(callErr, context.Canceled) {
			t.Fatalf("%s() error = %v, want context.Canceled", name, callErr)
		}
	}
	if readErr != nil || !bytes.Equal(afterBytes, before) || after.Revision() != base.Revision() {
		t.Fatalf("cancelled calls changed snapshot: read=%v bytes=%q revision=%s", readErr, afterBytes, after.Revision())
	}
	if receiptErr != nil || found {
		t.Fatalf("cancelled Commit receipt lookup = (%t, %v), want no receipt", found, receiptErr)
	}
}

func TestAdversarialRecoveryRejectsUnsafeAndTamperedJournals(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
	}{
		{name: "parent traversal", path: "../outside.md"},
		{name: "absolute", path: filepath.Join(t.TempDir(), "outside.md")},
		{name: "internal directory", path: ".okf/receipt.json"},
		{name: "result revision mismatch", path: "b.md"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange.
			root, _ := adversarialStore(t, Config{})
			next, err := newSnapshot(context.Background(), map[string][]byte{"b.md": []byte(adversarialDocument("B"))})
			if err != nil {
				t.Fatal(err)
			}
			files := []journalFile{{Path: tc.path, Payload: "payload-00000", Size: 1, Digest: "sha256:ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb"}}
			if tc.name == "result revision mismatch" {
				files = []journalFile{{Path: "b.md", Payload: "payload-00000", Size: 1, Digest: "sha256:ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb"}}
			}
			raw, err := json.Marshal(journal{Version: 1, Files: files, Receipt: testJournalReceipt(next.Revision(), next.Revision(), "tampered", "")})
			if err != nil {
				t.Fatal(err)
			}
			journalPath := filepath.Join(root, internalDirectory, "transactions", "tampered.json")
			if err := writeDurableFixture(journalPath, raw); err != nil {
				t.Fatal(err)
			}

			// Act.
			_, openErr := Open(root, Config{})

			// Assert.
			if openErr == nil {
				t.Fatal("Open() accepted a malicious or tampered journal")
			}
			if tc.name == "parent traversal" {
				if _, err := os.Stat(filepath.Join(filepath.Dir(root), "outside.md")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("journal wrote outside root: %v", err)
				}
			}
		})
	}
}

func TestRecoveryRejectsStrictJournalClassesBeforePublication(t *testing.T) {
	tests := []struct {
		name string
		raw  func(t *testing.T, root string) []byte
	}{
		{name: "zero receipt", raw: func(t *testing.T, _ string) []byte {
			t.Helper()
			return []byte(`{"version":1,"files":{},"receipt":{}}`)
		}},
		{name: "unknown field", raw: func(t *testing.T, _ string) []byte {
			t.Helper()
			return []byte(`{"version":1,"files":{},"receipt":{},"surprise":true}`)
		}},
		{name: "invalid staged bundle", raw: func(t *testing.T, _ string) []byte {
			t.Helper()
			content := []byte("not an OKF document\n")
			revision, err := store.RevisionFromManifest([]store.ManifestEntry{{Path: "published.md", Content: content}})
			if err != nil {
				t.Fatal(err)
			}
			j := journal{Version: 1, Files: []journalFile{{Path: "published.md", Payload: "payload-00000", Size: int64(len(content)), Digest: "sha256:ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb"}}, Receipt: testJournalReceipt(revision, revision, "invalid-stage", "")}
			raw, err := json.Marshal(j)
			if err != nil {
				t.Fatal(err)
			}
			return raw
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange.
			root, _ := adversarialStore(t, Config{})
			if err := writeDurableFixture(filepath.Join(root, internalDirectory, "transactions", "malicious.json"), tc.raw(t, root)); err != nil {
				t.Fatal(err)
			}

			// Act.
			_, err := Open(root, Config{})
			_, statErr := os.Stat(filepath.Join(root, "published.md"))

			// Assert.
			if err == nil || !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("Open() err=%v published=%v", err, statErr)
			}
		})
	}
}

func TestAdversarialRecoveryRejectsIncompleteJournalsDeterministically(t *testing.T) {
	// Arrange.
	root, _ := adversarialStore(t, Config{})
	for name, body := range map[string]string{"a.json": "first", "b.json": "second"} {
		files, err := json.Marshal(map[string]string{"result.md": base64.StdEncoding.EncodeToString([]byte(adversarialDocument(body)))})
		if err != nil {
			t.Fatal(err)
		}
		raw := []byte(`{"version":1,"files":` + string(files) + `,"receipt":{}}`)
		if err := writeDurableFixture(filepath.Join(root, internalDirectory, "transactions", name), raw); err != nil {
			t.Fatal(err)
		}
	}

	// Act.
	_, err := Open(root, Config{})
	got, readErr := os.ReadFile(filepath.Join(root, "result.md"))
	entries, dirErr := os.ReadDir(filepath.Join(root, internalDirectory, "transactions"))

	// Assert. An incomplete journal is never a recoverable transaction and must
	// not publish either staged result.
	if err == nil || !errors.Is(readErr, os.ErrNotExist) {
		t.Fatalf("recovery err=%v read=%v result=%q", err, readErr, got)
	}
	if dirErr != nil || len(entries) != 2 {
		t.Fatalf("journals = %#v, err=%v", entries, dirErr)
	}
}

func TestAdversarialLeaseCancellationAndCrossStoreCoherence(t *testing.T) {
	// Arrange.
	root, first := adversarialStore(t, Config{LeaseTimeout: time.Second})
	second, err := Open(root, Config{LeaseTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	lock := adversarialExclusiveLock(t, root)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Act.
	_, snapshotErr := second.Snapshot(ctx)
	_, openErr := OpenContext(ctx, root, Config{LeaseTimeout: time.Second})

	// Assert.
	if !errors.Is(snapshotErr, context.Canceled) {
		t.Fatalf("Snapshot() under held lease = %v, want cancellation", snapshotErr)
	}
	if !errors.Is(openErr, context.Canceled) {
		t.Fatalf("OpenContext() under held lease = %v, want cancellation", openErr)
	}
	if err := testUnlockFile(lock); err != nil {
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}

	// Arrange a barriered writer; its journal write is inside the exclusive lease.
	entered := make(chan struct{})
	release := make(chan struct{})
	writer, err := Open(root, Config{LeaseTimeout: time.Second, Fault: func(step Step) error {
		if step == StepFileWrite {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-release
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	base := adversarialSnapshot(t, writer)
	commitDone := make(chan struct{})
	var receipt store.CommitReceipt
	var commitErr error
	go func() {
		receipt, commitErr = writer.Commit(context.Background(), adversarialChange(t, "barrier", base.Revision(), "depends_on"), store.CommitOptions{})
		close(commitDone)
	}()
	<-entered
	blockedCtx, blockedCancel := context.WithCancel(context.Background())
	snapshotDone := make(chan error, 1)
	go func() { _, err := first.Snapshot(blockedCtx); snapshotDone <- err }()
	blockedCancel()
	blockedErr := <-snapshotDone
	close(release)
	<-commitDone
	stable, stableErr := second.Snapshot(context.Background())

	// Assert.
	if !errors.Is(blockedErr, context.Canceled) {
		t.Fatalf("Snapshot() did not honor cancellation while writer held lease: %v", blockedErr)
	}
	if commitErr != nil || stableErr != nil || stable.Revision() != receipt.ResultRevision {
		t.Fatalf("cross-store coherence: commit=%v snapshot=%v got=%v want=%v", commitErr, stableErr, stable.Revision(), receipt.ResultRevision)
	}
}

func TestCommitCancellationAfterJournalPersistsReceipt(t *testing.T) {
	// Arrange. Journal writes have a dedicated boundary, so file write is the
	// post-journal apply boundary.
	entered := make(chan struct{})
	release := make(chan struct{})
	calls := 0
	_, s := adversarialStore(t, Config{Fault: func(step Step) error {
		if step == StepFileWrite {
			calls++
			if calls == 1 {
				close(entered)
				<-release
			}
		}
		return nil
	}})
	base := adversarialSnapshot(t, s)
	change := adversarialChange(t, "cancel-after-journal", base.Revision(), "depends_on")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := s.Commit(ctx, change, store.CommitOptions{IdempotencyKey: "cancel-after-journal"})
		done <- err
	}()
	<-entered
	cancel()
	close(release)

	// Act.
	err := <-done
	receipt, found, replayErr := s.lookupReceipt("cancel-after-journal", mustDigest(t, change))

	// Assert.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Commit() error = %v, want context cancellation", err)
	}
	if replayErr != nil || !found || receipt.ResultRevision == base.Revision() {
		t.Fatalf("persisted receipt = %#v, found=%v, err=%v", receipt, found, replayErr)
	}
}

func TestSameStoreRetryRecoversDurableJournalBeforeCAS(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(fmt.Sprintf("replace=%t", replace), func(t *testing.T) {
			fired := false
			_, s := adversarialStore(t, Config{PostFault: func(step Step) error {
				if step == StepFileWrite && !fired {
					fired = true
					return errors.New("crash after apply")
				}
				return nil
			}})
			base := adversarialSnapshot(t, s)
			if !replace {
				change := adversarialChange(t, "same-store-retry", base.Revision(), "depends_on")
				opts := store.CommitOptions{IdempotencyKey: "same-store-retry"}
				_, firstErr := s.Commit(context.Background(), change, opts)
				replay, replayErr := s.Commit(context.Background(), change, opts)
				second, secondErr := s.Commit(context.Background(), change, opts)
				if firstErr == nil || replayErr != nil || secondErr != nil || replay.ResultRevision == base.Revision() || !replay.CommitTime.Equal(second.CommitTime) {
					t.Fatalf("commit first=%v replay=%#v/%v second=%#v/%v", firstErr, replay, replayErr, second, secondErr)
				}
				return
			}
			doc, err := bundle.ParseDocument(adversarialDocument("replacement"))
			if err != nil {
				t.Fatal(err)
			}
			req := ReplaceConceptRequest{ChangeSetID: "same-store-replace", Actor: "adversary-test", BaseRevision: base.Revision(), ConceptID: adversarialRef(t, "a").ID, Document: doc}
			opts := store.CommitOptions{IdempotencyKey: "same-store-replace"}
			_, firstErr := s.ReplaceConcept(context.Background(), req, opts)
			replay, replayErr := s.ReplaceConcept(context.Background(), req, opts)
			second, secondErr := s.ReplaceConcept(context.Background(), req, opts)
			if firstErr == nil || replayErr != nil || secondErr != nil || replay.Receipt.ResultRevision == base.Revision() || !replay.Receipt.CommitTime.Equal(second.Receipt.CommitTime) {
				t.Fatalf("replace first=%v replay=%#v/%v second=%#v/%v", firstErr, replay, replayErr, second, secondErr)
			}
		})
	}
}

func mustDigest(t *testing.T, change store.ChangeSet) string {
	t.Helper()
	digest, err := change.RequestDigest()
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func TestAdversarialMetadataModesAndRetentionFloor(t *testing.T) {
	// Arrange.
	root, s := adversarialStore(t, Config{})
	base := adversarialSnapshot(t, s)

	// Act.
	_, commitErr := s.Commit(context.Background(), adversarialChange(t, "metadata", base.Revision(), "depends_on"), store.CommitOptions{IdempotencyKey: "metadata-key"})
	leaseInfo, leaseErr := os.Stat(filepath.Join(root, internalDirectory, "lease"))
	receiptEntries, receiptErr := os.ReadDir(filepath.Join(root, internalDirectory, "receipts"))
	invalidConfigErr := (Config{MinimumReceipts: 999}).Validate()
	defaultConfig := DefaultConfig()

	// Assert.
	if commitErr != nil || leaseErr != nil || receiptErr != nil || len(receiptEntries) != 1 {
		t.Fatalf("metadata creation: commit=%v lease=%v receipts=%v entries=%d", commitErr, leaseErr, receiptErr, len(receiptEntries))
	}
	if leaseInfo.Mode().Perm() != 0o600 {
		t.Fatalf("lease mode = %o, want 600", leaseInfo.Mode().Perm())
	}
	if invalidConfigErr == nil || defaultConfig.MinimumReceipts < 1000 {
		t.Fatalf("retention floor: validation=%v default=%d", invalidConfigErr, defaultConfig.MinimumReceipts)
	}
}

func TestAdversarialReceiptRetentionKeepsDeterministicMinimum(t *testing.T) {
	// Arrange.
	_, s := adversarialStore(t, Config{ReceiptRetention: time.Nanosecond, MinimumReceipts: 1000})
	oldest := store.IdempotencyKey("receipt-0000")
	base := time.Unix(0, 0).UTC()
	revision := store.Revision("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	digest := "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	for i := 0; i < 1001; i++ {
		key := store.IdempotencyKey("receipt-" + formatReceiptNumber(i))
		receipt := store.CommitReceipt{FormatVersion: store.CommitReceiptFormatVersion, ChangeSetID: store.ChangeSetID("change-" + formatReceiptNumber(i)), IdempotencyKey: key, RequestDigest: digest, BaseRevision: revision, ResultRevision: revision, CommitTime: base.Add(time.Duration(i) * time.Second)}
		if err := s.writeReceipt(receipt); err != nil {
			t.Fatal(err)
		}
	}

	// Act.
	err := s.pruneReceipts()
	_, oldestFound, oldestErr := s.lookupReceipt(oldest, digest)
	_, newestFound, newestErr := s.lookupReceipt("receipt-1000", digest)
	entries, entriesErr := os.ReadDir(filepath.Join(s.root, internalDirectory, "receipts"))

	// Assert.
	if err != nil || oldestErr != nil || newestErr != nil {
		t.Fatalf("prune errors: prune=%v oldest=%v newest=%v", err, oldestErr, newestErr)
	}
	if oldestFound || !newestFound || entriesErr != nil || len(entries) != 1000 {
		t.Fatalf("retention result: oldest=%v newest=%v entries=%d err=%v", oldestFound, newestFound, len(entries), entriesErr)
	}
}

func adversarialStore(t *testing.T, cfg Config) (string, *Store) {
	t.Helper()
	root := t.TempDir()
	writeTestFile(t, root, "a.md", adversarialDocument("A"))
	writeTestFile(t, root, "b.md", adversarialDocument("B"))
	s, err := Open(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return root, s
}

func adversarialSnapshot(t *testing.T, s *Store) store.Snapshot {
	t.Helper()
	snap, err := s.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func adversarialChange(t *testing.T, id string, base store.Revision, relation string) store.ChangeSet {
	t.Helper()
	return store.ChangeSet{Version: store.ChangeSetFormatVersion, ID: store.ChangeSetID(id), Actor: "adversary-test", BaseRevision: base, Operations: []store.Operation{store.EnsureRelation{Source: adversarialRef(t, "a"), Type: relation, Target: adversarialRef(t, "b")}}}
}

func adversarialRef(t *testing.T, raw string) bundle.RelationRef {
	t.Helper()
	ref, err := bundle.ParseRelationRef(raw)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func adversarialDocument(body string) string { return "---\ntype: Note\n---\n\n" + body + "\n" }

func formatReceiptNumber(i int) string {
	if i < 10 {
		return "000" + string(rune('0'+i))
	}
	if i < 100 {
		return "00" + itoa(i)
	}
	if i < 1000 {
		return "0" + itoa(i)
	}
	return itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var digits [20]byte
	n := len(digits)
	for i > 0 {
		n--
		digits[n] = byte('0' + i%10)
		i /= 10
	}
	return string(digits[n:])
}

func adversarialExclusiveLock(t *testing.T, root string) *os.File {
	t.Helper()
	path := filepath.Join(root, internalDirectory, "lease")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := testLockExclusive(f); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	return f
}
