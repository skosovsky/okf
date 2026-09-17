package fs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
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
	_, found, receiptErr := s.lookupReceiptContext(t.Context(), "cancelled-commit", "")

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
			root, s := adversarialStore(t, Config{})
			base := adversarialSnapshot(t, s)
			next, err := newSnapshot(context.Background(), map[string][]byte{"b.md": []byte(adversarialDocument("B"))})
			if err != nil {
				t.Fatal(err)
			}
			receipt := testJournalReceipt(base.Revision(), next.Revision(), "tampered-v5", "")
			journalName, raw := adversarialLiveDurableJournal(t, root, s, next, receipt)
			var malicious journal
			if err := json.Unmarshal(raw, &malicious); err != nil {
				t.Fatal(err)
			}
			if tc.name != "result revision mismatch" {
				malicious.Files[0].Path = tc.path
			} else {
				malicious.Receipt.ResultRevision = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
				malicious.BaseBinding, err = journalBaseBinding(malicious.Receipt, malicious.Base)
				if err != nil {
					t.Fatal(err)
				}
			}
			raw, err = json.Marshal(malicious)
			if err != nil {
				t.Fatal(err)
			}
			adversarialRewriteSameInode(t, filepath.Join(root, filepath.FromSlash(journalName)), raw)
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}

			// Act.
			_, openErr := openObserved(root, Config{})

			// Assert.
			if !errors.Is(openErr, store.ErrStorageCorrupt) {
				t.Fatalf("Open() error=%v, want storage corruption", openErr)
			}
			wantGate := "invalid journal file manifest"
			if tc.name == "result revision mismatch" {
				wantGate = "journal staged content does not match receipt revision"
			}
			if !strings.Contains(openErr.Error(), wantGate) {
				t.Fatalf("Open() error=%v, want intended %q gate", openErr, wantGate)
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
		name          string
		wantGate      string
		invalidBundle bool
		mutate        func(t *testing.T, raw []byte) []byte
	}{
		{name: "zero receipt", wantGate: "receipt fields are incomplete or unknown", mutate: func(t *testing.T, raw []byte) []byte {
			t.Helper()
			var j journal
			if err := json.Unmarshal(raw, &j); err != nil {
				t.Fatal(err)
			}
			receiptRaw, err := json.Marshal(j.Receipt)
			if err != nil {
				t.Fatal(err)
			}
			zeroed := bytes.Replace(raw, receiptRaw, []byte("{}"), 1)
			if bytes.Equal(zeroed, raw) {
				t.Fatal("zero-receipt fixture did not replace receipt")
			}
			return zeroed
		}},
		{name: "unknown field", wantGate: `unknown field "surprise"`, mutate: func(t *testing.T, raw []byte) []byte {
			t.Helper()
			trimmed := bytes.TrimSuffix(raw, []byte("}"))
			out := append([]byte(nil), trimmed...)
			return append(out, []byte(`,"surprise":true}`)...)
		}},
		{name: "invalid staged bundle", wantGate: "journal staged post-state fails validation", invalidBundle: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			base := adversarialSnapshot(t, s)
			validContent := []byte(adversarialDocument("published"))
			next, err := newSnapshot(context.Background(), map[string][]byte{"published.md": validContent})
			if err != nil {
				t.Fatal(err)
			}
			receipt := testJournalReceipt(base.Revision(), next.Revision(), "strict-"+tc.name, "")
			journalName, raw := adversarialLiveDurableJournal(t, root, s, next, receipt)
			if tc.invalidBundle {
				invalidContent := []byte("not an OKF document\n")
				var j journal
				if err := json.Unmarshal(raw, &j); err != nil {
					t.Fatal(err)
				}
				payloadPath := filepath.Join(root, filepath.FromSlash(path.Join(j.Stage, j.Files[0].Payload)))
				adversarialRewriteSameInode(t, payloadPath, invalidContent)
				sum := sha256.Sum256(invalidContent)
				j.Files[0].Size = int64(len(invalidContent))
				j.Files[0].Digest = "sha256:" + hex.EncodeToString(sum[:])
				invalidNext, snapshotErr := newSnapshot(context.Background(), map[string][]byte{"published.md": invalidContent})
				if snapshotErr != nil {
					t.Fatal(snapshotErr)
				}
				j.Receipt.ResultRevision = invalidNext.Revision()
				j.BaseBinding, err = journalBaseBinding(j.Receipt, j.Base)
				if err != nil {
					t.Fatal(err)
				}
				raw, err = json.Marshal(j)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				raw = tc.mutate(t, raw)
			}
			adversarialRewriteSameInode(t, filepath.Join(root, filepath.FromSlash(journalName)), raw)
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}

			// Act.
			_, openErr := openObserved(root, Config{})
			_, statErr := os.Stat(filepath.Join(root, "published.md"))

			// Assert.
			if !errors.Is(openErr, store.ErrStorageCorrupt) || !strings.Contains(openErr.Error(), tc.wantGate) || !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("Open() err=%v published=%v", openErr, statErr)
			}
		})
	}
}

func TestAdversarialRecoveryRejectsIncompleteJournalsDeterministically(t *testing.T) {
	// Arrange.
	root, _ := adversarialStore(t, Config{})
	for name := range map[string]string{"a.json": "first", "b.json": "second"} {
		raw := []byte(`{"version":5,"hash_algorithm":"sha256","stage":".okf/staging/txn-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/payload","files":[],"receipt":{}}`)
		if err := writeDurableFixture(filepath.Join(root, internalDirectory, "transactions", name), raw); err != nil {
			t.Fatal(err)
		}
	}

	// Act.
	_, err := openObserved(root, Config{})
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

func TestAdversarialRootLockCancellationAndCrossStoreCoherence(t *testing.T) {
	// Arrange.
	root, first := adversarialStore(t, Config{LeaseTimeout: time.Second})
	second, err := openObserved(root, Config{LeaseTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	registerStoreCleanup(t, second)
	lock := adversarialExclusiveRootLock(t, root)
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
	writer, err := openObserved(root, Config{LeaseTimeout: time.Second, Fault: func(step Step) error {
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
	registerStoreCleanup(t, writer)
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
	done := make(chan struct {
		receipt store.CommitReceipt
		err     error
	}, 1)
	go func() {
		receipt, err := s.Commit(ctx, change, store.CommitOptions{IdempotencyKey: "cancel-after-journal"})
		done <- struct {
			receipt store.CommitReceipt
			err     error
		}{receipt: receipt, err: err}
	}()
	<-entered
	cancel()
	close(release)

	// Act.
	result := <-done
	receipt, found, replayErr := s.lookupReceiptContext(t.Context(), "cancel-after-journal", mustDigest(t, change))

	// Assert.
	var committed *store.CommittedError
	if !errors.Is(result.err, context.Canceled) || !errors.As(result.err, &committed) || !reflect.DeepEqual(result.receipt, committed.Receipt()) {
		t.Fatalf("Commit() receipt=%#v error=%v, want receipt-bearing cancellation", result.receipt, result.err)
	}
	if replayErr != nil || !found || receipt.ResultRevision == base.Revision() || !reflect.DeepEqual(receipt, result.receipt) {
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
	_, leaseErr := os.Stat(filepath.Join(root, internalDirectory, "lease"))
	receiptEntries, receiptErr := os.ReadDir(filepath.Join(root, internalDirectory, "receipts"))
	invalidConfigErr := (Config{MinimumReceipts: 999}).Validate()
	defaultConfig := DefaultConfig()

	// Assert.
	if commitErr != nil || !errors.Is(leaseErr, os.ErrNotExist) || receiptErr != nil || len(receiptEntries) != 1 {
		t.Fatalf("metadata creation: commit=%v lease=%v receipts=%v entries=%d", commitErr, leaseErr, receiptErr, len(receiptEntries))
	}
	if invalidConfigErr == nil || defaultConfig.MinimumReceipts < 1000 {
		t.Fatalf("retention floor: validation=%v default=%d", invalidConfigErr, defaultConfig.MinimumReceipts)
	}
}

func TestAdversarialReceiptRetentionKeepsDeterministicMinimum(t *testing.T) {
	// Arrange.
	root, s := adversarialStore(t, Config{ReceiptRetention: time.Nanosecond, MinimumReceipts: 1000})
	oldest := store.IdempotencyKey("receipt-0000")
	base := time.Unix(0, 0).UTC()
	revision := store.Revision("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	digest := "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	if err := os.MkdirAll(filepath.Join(root, internalDirectory, "receipts"), 0o700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1001; i++ {
		key := store.IdempotencyKey("receipt-" + formatReceiptNumber(i))
		receipt := store.CommitReceipt{FormatVersion: store.CommitReceiptFormatVersion, ChangeSetID: store.ChangeSetID("change-" + formatReceiptNumber(i)), IdempotencyKey: key, RequestDigest: digest, BaseRevision: revision, ResultRevision: revision, CommitTime: base.Add(time.Duration(i) * time.Second)}
		raw, err := json.Marshal(receiptFile{
			Version: 2,
			Key:     string(key),
			Digest:  digest,
			Receipt: receipt,
		})
		if err != nil {
			t.Fatal(err)
		}
		// The subject is one prune operation over a complete inventory. Build
		// that inventory directly instead of running the durable publication
		// protocol (and its own prune scan) 1001 times during setup.
		if err := os.WriteFile(
			filepath.Join(root, filepath.FromSlash(s.receiptPath(key))),
			raw,
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	var pruneTrace []Step
	s.config.PostFault = func(step Step) error {
		pruneTrace = append(pruneTrace, step)
		return nil
	}

	// Act.
	err := s.pruneReceipts()
	_, oldestFound, oldestErr := s.lookupReceiptContext(t.Context(), oldest, digest)
	_, newestFound, newestErr := s.lookupReceiptContext(t.Context(), "receipt-1000", digest)
	entries, entriesErr := os.ReadDir(filepath.Join(root, internalDirectory, "receipts"))

	// Assert.
	if err != nil || oldestErr != nil || newestErr != nil {
		t.Fatalf("prune errors: prune=%v oldest=%v newest=%v", err, oldestErr, newestErr)
	}
	if oldestFound || !newestFound || entriesErr != nil || len(entries) != 1000 {
		t.Fatalf("retention result: oldest=%v newest=%v entries=%d err=%v", oldestFound, newestFound, len(entries), entriesErr)
	}
	var claimProofSyncs, pruneBoundaries int
	for _, step := range pruneTrace {
		switch step {
		case StepClaimDirectorySync:
			claimProofSyncs++
		case StepReceiptPrune:
			pruneBoundaries++
		}
	}
	if claimProofSyncs == 0 || pruneBoundaries != 1 || len(pruneTrace) < 2 || pruneTrace[len(pruneTrace)-2] != StepReceiptDirectorySync || pruneTrace[len(pruneTrace)-1] != StepReceiptPrune {
		t.Fatalf("prune post-fault trace=%v, want named claim proof phases and one final receipts sync/prune boundary", pruneTrace)
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
	registerStoreCleanup(t, s)
	return root, s
}

func adversarialLiveDurableJournal(t *testing.T, root string, s *Store, next *snapshot, receipt store.CommitReceipt) (string, []byte) {
	t.Helper()
	crash := errors.New("retain live adversarial journal")
	s.config.PostFault = func(step Step) error {
		if step == StepJournalDirectorySync {
			return crash
		}
		return nil
	}
	if err := s.publish(context.Background(), next, receipt); !errors.Is(err, crash) {
		t.Fatalf("publish live durable journal: %v", err)
	}
	s.config.PostFault = nil
	journalName, err := journalPath(receipt.RequestDigest)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(journalName)))
	if err != nil {
		t.Fatal(err)
	}
	return journalName, raw
}

func adversarialRewriteSameInode(t *testing.T, filename string, raw []byte) {
	t.Helper()
	before, err := os.Stat(filename)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filename, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(raw); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(filename)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("same-inode fixture rewrite replaced the installed artifact")
	}
}

func adversarialRewriteLivePostState(t *testing.T, root, journalName string, raw []byte, files map[string][]byte) {
	t.Helper()
	var j journal
	if err := json.Unmarshal(raw, &j); err != nil {
		t.Fatal(err)
	}
	byPath := make(map[string]*journalFile, len(j.Files))
	for i := range j.Files {
		byPath[j.Files[i].Path] = &j.Files[i]
	}
	for name, content := range files {
		entry := byPath[name]
		if entry == nil {
			t.Fatalf("live journal has no payload for %q", name)
		}
		adversarialRewriteSameInode(t, filepath.Join(root, filepath.FromSlash(path.Join(j.Stage, entry.Payload))), content)
		sum := sha256.Sum256(content)
		entry.Size = int64(len(content))
		entry.Digest = "sha256:" + hex.EncodeToString(sum[:])
	}
	next, err := newSnapshot(context.Background(), files)
	if err != nil {
		t.Fatal(err)
	}
	j.Receipt.ResultRevision = next.Revision()
	j.BaseBinding, err = journalBaseBinding(j.Receipt, j.Base)
	if err != nil {
		t.Fatal(err)
	}
	mutated, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	adversarialRewriteSameInode(t, filepath.Join(root, filepath.FromSlash(journalName)), mutated)
}

func adversarialV5JournalFixture(t *testing.T, root string, resultFiles map[string][]byte, id string) journal {
	t.Helper()
	baseFiles, err := readVisibleRoot(context.Background(), mustOpenRoot(t, root))
	if err != nil {
		t.Fatal(err)
	}
	base, err := newSnapshot(context.Background(), baseFiles)
	if err != nil {
		t.Fatal(err)
	}
	next, err := newSnapshot(context.Background(), resultFiles)
	if err != nil {
		t.Fatal(err)
	}
	receipt := testJournalReceipt(base.Revision(), next.Revision(), id, "")
	raw, err := encodeJournalFixture(t, root, next, receipt)
	if err != nil {
		t.Fatal(err)
	}
	var j journal
	if err := json.Unmarshal(raw, &j); err != nil {
		t.Fatal(err)
	}
	return j
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

func adversarialExclusiveRootLock(t *testing.T, root string) *os.File {
	t.Helper()
	f, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := testLockExclusive(f); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	return f
}
