package fs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
)

func TestDecodeJournalUnsupportedVersionPrecedesV5NestedFields(t *testing.T) {
	nestedCases := []struct {
		name    string
		receipt string
		replay  string
		files   string
	}{
		{name: "malformed receipt", receipt: `[]`, replay: `{}`, files: `[]`},
		{name: "null receipt", receipt: `null`, replay: `{}`, files: `[]`},
		{name: "duplicate receipt", receipt: `{"FormatVersion":1,"FormatVersion":1}`, replay: `{}`, files: `[]`},
		{name: "malformed replay", receipt: `{}`, replay: `[]`, files: `[]`},
		{name: "null replay", receipt: `{}`, replay: `null`, files: `[]`},
		{name: "duplicate replay", receipt: `{}`, replay: `{"version":1,"version":1}`, files: `[]`},
		{name: "malformed files", receipt: `{}`, replay: `{}`, files: `{}`},
		{name: "null files", receipt: `{}`, replay: `{}`, files: `null`},
		{name: "duplicate files", receipt: `{}`, replay: `{}`, files: `[{"path":"a.md","path":"a.md"}]`},
	}
	for _, version := range []uint64{4, 6} {
		for _, tc := range nestedCases {
			t.Run(fmt.Sprintf("version_%d/%s", version, tc.name), func(t *testing.T) {
				// Arrange.
				raw := []byte(fmt.Sprintf(
					`{"version":%d,"hash_algorithm":"sha256","stage":"invalid-for-v5","base":[],"base_binding":"","files":%s,"receipt":%s,"replay":%s}`,
					version,
					tc.files,
					tc.receipt,
					tc.replay,
				))

				// Act.
				_, err := decodeJournal(raw)

				// Assert.
				want := fmt.Sprintf("unsupported journal version %d", version)
				if err == nil || err.Error() != want {
					t.Fatalf("decodeJournal() error = %v, want exact %q", err, want)
				}
			})
		}
	}
}

func TestDecodeJournalGenericSecurityGatesPrecedeUnsupportedVersion(t *testing.T) {
	t.Run("immutable byte ceiling", func(t *testing.T) {
		// Arrange.
		raw := bytes.Repeat([]byte{'x'}, maxJournalManifestRead+1)

		// Act.
		_, err := decodeJournal(raw)

		// Assert.
		want := fmt.Sprintf("journal manifest exceeds %d byte limit", maxJournalManifestRead)
		if err == nil || err.Error() != want {
			t.Fatalf("decodeJournal() error = %v, want exact %q", err, want)
		}
	})

	t.Run("invalid UTF-8", func(t *testing.T) {
		// Arrange.
		raw := append([]byte(`{"version":4,"receipt":"`), 0xff)
		raw = append(raw, []byte(`"}`)...)

		// Act.
		_, err := decodeJournal(raw)

		// Assert.
		if err == nil || err.Error() != "journal contains invalid UTF-8" {
			t.Fatalf("decodeJournal() error = %v, want invalid UTF-8 gate", err)
		}
	})

	t.Run("top-level duplicate", func(t *testing.T) {
		// Arrange.
		raw := []byte(`{"version":4,"version":4,"receipt":null}`)

		// Act.
		_, err := decodeJournal(raw)

		// Assert.
		if err == nil || !strings.Contains(err.Error(), `duplicate JSON key "version"`) {
			t.Fatalf("decodeJournal() error = %v, want top-level duplicate gate", err)
		}
	})
}

func TestCommitFinalLeaseRecoversCrossStoreDurableJournalBeforeCAS(t *testing.T) {
	// Arrange.
	root, winner := adversarialStore(t, Config{})
	contender, err := openObserved(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	registerStoreCleanup(t, contender)
	base := adversarialSnapshot(t, winner)
	winnerChange := adversarialChange(t, "commit-journal-winner", base.Revision(), "depends_on")
	contenderChange := adversarialChange(t, "commit-journal-contender", base.Revision(), "blocks")
	winnerOptions := store.CommitOptions{IdempotencyKey: "commit-journal-winner"}
	planned, allowFinalLease := make(chan struct{}), make(chan struct{})
	var plannedOnce atomic.Bool
	contender.beforeMutationLease = func() {
		if plannedOnce.CompareAndSwap(false, true) {
			close(planned)
			<-allowFinalLease
		}
	}
	contenderResult := make(chan store.CommitReceipt, 1)
	contenderErr := make(chan error, 1)
	go func() {
		receipt, callErr := contender.Commit(context.Background(), contenderChange, store.CommitOptions{})
		contenderResult <- receipt
		contenderErr <- callErr
	}()
	<-planned

	journalDurable, releaseWinner := make(chan struct{}), make(chan struct{})
	crashErr := errors.New("crash after durable commit journal")
	var crashed atomic.Bool
	var journalRenamed atomic.Bool
	winner.config.PostFault = func(step Step) error {
		if step == StepJournalRename {
			journalRenamed.Store(true)
		}
		if step == StepJournalDirectorySync && journalRenamed.Load() && crashed.CompareAndSwap(false, true) {
			close(journalDurable)
			<-releaseWinner
			return crashErr
		}
		return nil
	}
	winnerResult := make(chan store.CommitReceipt, 1)
	winnerErr := make(chan error, 1)
	go func() {
		receipt, callErr := winner.Commit(context.Background(), winnerChange, winnerOptions)
		winnerResult <- receipt
		winnerErr <- callErr
	}()
	<-journalDurable
	durable := readOnlyPendingJournal(t, root)
	close(allowFinalLease)
	close(releaseWinner)

	// Act.
	failedReceipt, firstErr := <-winnerResult, <-winnerErr
	rejectedReceipt, secondErr := <-contenderResult, <-contenderErr
	replayed, replayErr := contender.Commit(context.Background(), winnerChange, winnerOptions)
	post := adversarialSnapshot(t, contender)
	remaining := pendingJournalNames(t, root)

	// Assert.
	var committed *store.CommittedError
	if !errors.Is(firstErr, crashErr) || !errors.As(firstErr, &committed) || !reflect.DeepEqual(failedReceipt, durable.Receipt) || !reflect.DeepEqual(committed.Receipt(), durable.Receipt) {
		t.Fatalf("winner result = %#v, error = %v; want committed durable receipt", failedReceipt, firstErr)
	}
	var conflict *store.Conflict
	if !errors.As(secondErr, &conflict) || conflict.Expected != base.Revision() || conflict.Actual != durable.Receipt.ResultRevision || !rejectedReceipt.BaseRevision.IsZero() {
		t.Fatalf("contender result = %#v, error = %#v; want exact recovered conflict %s -> %s", rejectedReceipt, secondErr, base.Revision(), durable.Receipt.ResultRevision)
	}
	if post.Revision() != durable.Receipt.ResultRevision || replayErr != nil || !reflect.DeepEqual(replayed, durable.Receipt) {
		t.Fatalf("post/replay = %s / %#v err=%v, want durable %#v", post.Revision(), replayed, replayErr, durable.Receipt)
	}
	if len(remaining) != 0 {
		t.Fatalf("pending journals after recovery = %v, want none", remaining)
	}
}

func TestReplaceConceptFinalLeaseRecoversCrossStoreDurableJournalBeforeCAS(t *testing.T) {
	// Arrange.
	root, winner := adversarialStore(t, Config{})
	contender, err := openObserved(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	registerStoreCleanup(t, contender)
	base := adversarialSnapshot(t, winner)
	winnerDocument, err := bundle.ParseDocument(adversarialDocument("winner replacement"))
	if err != nil {
		t.Fatal(err)
	}
	contenderDocument, err := bundle.ParseDocument(adversarialDocument("contender replacement"))
	if err != nil {
		t.Fatal(err)
	}
	winnerRequest := ReplaceConceptRequest{
		ChangeSetID:  "replace-journal-winner",
		Actor:        "adversary-test",
		BaseRevision: base.Revision(),
		ConceptID:    adversarialRef(t, "a").ID,
		Document:     winnerDocument,
	}
	contenderRequest := winnerRequest
	contenderRequest.ChangeSetID = "replace-journal-contender"
	contenderRequest.Document = contenderDocument
	winnerOptions := store.CommitOptions{IdempotencyKey: "replace-journal-winner"}
	planned, allowFinalLease := make(chan struct{}), make(chan struct{})
	var plannedOnce atomic.Bool
	contender.beforeMutationLease = func() {
		if plannedOnce.CompareAndSwap(false, true) {
			close(planned)
			<-allowFinalLease
		}
	}
	contenderResult := make(chan ReplaceConceptResult, 1)
	contenderErr := make(chan error, 1)
	go func() {
		result, callErr := contender.ReplaceConcept(context.Background(), contenderRequest, store.CommitOptions{})
		contenderResult <- result
		contenderErr <- callErr
	}()
	<-planned

	journalDurable, releaseWinner := make(chan struct{}), make(chan struct{})
	crashErr := errors.New("crash after durable replace journal")
	var crashed atomic.Bool
	var journalRenamed atomic.Bool
	winner.config.PostFault = func(step Step) error {
		if step == StepJournalRename {
			journalRenamed.Store(true)
		}
		if step == StepJournalDirectorySync && journalRenamed.Load() && crashed.CompareAndSwap(false, true) {
			close(journalDurable)
			<-releaseWinner
			return crashErr
		}
		return nil
	}
	winnerResult := make(chan ReplaceConceptResult, 1)
	winnerErr := make(chan error, 1)
	go func() {
		result, callErr := winner.ReplaceConcept(context.Background(), winnerRequest, winnerOptions)
		winnerResult <- result
		winnerErr <- callErr
	}()
	<-journalDurable
	durable := readOnlyPendingJournal(t, root)
	if durable.Replay == nil {
		t.Fatal("durable replacement journal omitted replay projection")
	}
	wantReport, err := durable.Replay.validation()
	if err != nil {
		t.Fatal(err)
	}
	close(allowFinalLease)
	close(releaseWinner)

	// Act.
	failedResult, firstErr := <-winnerResult, <-winnerErr
	rejectedResult, secondErr := <-contenderResult, <-contenderErr
	replayed, replayErr := contender.ReplaceConcept(context.Background(), winnerRequest, winnerOptions)
	post := adversarialSnapshot(t, contender)
	remaining := pendingJournalNames(t, root)

	// Assert.
	var committed *store.CommittedError
	if !errors.Is(firstErr, crashErr) || !errors.As(firstErr, &committed) || !reflect.DeepEqual(failedResult.Receipt, durable.Receipt) || !reflect.DeepEqual(committed.Receipt(), durable.Receipt) || !reflect.DeepEqual(failedResult.Validation, wantReport) {
		t.Fatalf("winner result = %#v, error = %v; want committed receipt/report", failedResult, firstErr)
	}
	var conflict *store.Conflict
	if !errors.As(secondErr, &conflict) || conflict.Expected != base.Revision() || conflict.Actual != durable.Receipt.ResultRevision || !rejectedResult.Receipt.BaseRevision.IsZero() {
		t.Fatalf("contender result = %#v, error = %#v; want exact recovered conflict %s -> %s", rejectedResult, secondErr, base.Revision(), durable.Receipt.ResultRevision)
	}
	if post.Revision() != durable.Receipt.ResultRevision || replayErr != nil || !reflect.DeepEqual(replayed.Receipt, durable.Receipt) || !reflect.DeepEqual(replayed.Validation, wantReport) {
		t.Fatalf("post/replay = %s / %#v err=%v, want receipt %#v report %#v", post.Revision(), replayed, replayErr, durable.Receipt, wantReport)
	}
	if len(remaining) != 0 {
		t.Fatalf("pending journals after recovery = %v, want none", remaining)
	}
}

func TestRecoveryFailsClosedBeforeApplyingMultiplePendingJournals(t *testing.T) {
	// Arrange.
	root, s := adversarialStore(t, Config{})
	base := adversarialSnapshot(t, s)
	firstNext, err := newSnapshot(context.Background(), map[string][]byte{
		"a.md": []byte(adversarialDocument("first pending result")),
		"b.md": []byte(adversarialDocument("B")),
	})
	if err != nil {
		t.Fatal(err)
	}
	secondNext, err := newSnapshot(context.Background(), map[string][]byte{
		"a.md": []byte(adversarialDocument("A")),
		"b.md": []byte(adversarialDocument("second pending result")),
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, next := range []*snapshot{firstNext, secondNext} {
		receipt := testJournalReceipt(base.Revision(), next.Revision(), fmt.Sprintf("multiple-pending-%d", i), "")
		receipt.RequestDigest = fmt.Sprintf("sha256:%064x", i+1)
		raw, encodeErr := encodeJournalFixture(t, root, next, receipt)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		name, pathErr := journalPath(receipt.RequestDigest)
		if pathErr != nil {
			t.Fatal(pathErr)
		}
		if writeErr := writeDurableFixture(filepath.Join(root, name), raw); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	beforeFiles, err := readVisibleRoot(context.Background(), s.rootFD)
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	openStore, openErr := openObserved(root, Config{})
	afterFiles, readErr := readVisibleRoot(context.Background(), s.rootFD)
	remaining := pendingJournalNames(t, root)

	// Assert.
	if openStore != nil {
		_ = openStore.Close()
		t.Fatal("Open() returned a Store for multiple pending journals")
	}
	if !errors.Is(openErr, store.ErrStorageCorrupt) || !strings.Contains(openErr.Error(), "multiple pending journals: 2") {
		t.Fatalf("Open() error = %v, want fail-closed multiple-journal corruption", openErr)
	}
	if readErr != nil || !reflect.DeepEqual(afterFiles, beforeFiles) {
		t.Fatalf("multiple-journal recovery changed visible state: before=%v after=%v err=%v", sortedFilePaths(beforeFiles), sortedFilePaths(afterFiles), readErr)
	}
	if len(remaining) != 2 {
		t.Fatalf("pending journals after failed recovery = %v, want both preserved", remaining)
	}
}

func readOnlyPendingJournal(t *testing.T, root string) journal {
	t.Helper()
	names := pendingJournalNames(t, root)
	if len(names) != 1 {
		t.Fatalf("pending journals = %v, want exactly one", names)
	}
	raw, err := os.ReadFile(filepath.Join(root, internalDirectory, "transactions", names[0]))
	if err != nil {
		t.Fatal(err)
	}
	j, err := decodeJournal(raw)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func pendingJournalNames(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, internalDirectory, "transactions"))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			names = append(names, entry.Name())
		}
	}
	return names
}
