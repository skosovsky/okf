package fs

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"errors"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/skosovsky/okf/store"
)

type testHashAlgorithm struct{}

func (testHashAlgorithm) Name() string   { return "test-sha512-v1" }
func (testHashAlgorithm) New() hash.Hash { return sha512.New() }

func TestConfiguredHashAlgorithmPersistsAcrossCommitReplayAndRecovery(t *testing.T) {
	// Arrange.
	var faultOnce sync.Once
	armed := false
	root, s := adversarialStore(t, Config{
		HashAlgorithm: testHashAlgorithm{},
		PostFault: func(step Step) error {
			if armed && step == StepJournalDirectorySync {
				var err error
				faultOnce.Do(func() { err = errors.New("interrupted after journal") })
				return err
			}
			return nil
		},
	})
	base := adversarialSnapshot(t, s)
	change := adversarialChange(t, "custom-hash", base.Revision(), "depends_on")
	options := store.CommitOptions{IdempotencyKey: "custom-hash-replay"}
	armed = true

	// Act.
	_, commitErr := s.Commit(context.Background(), change, options)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	_, mismatchErr := Open(root, Config{})
	reopened, openErr := Open(root, Config{HashAlgorithm: testHashAlgorithm{}})
	if openErr != nil {
		t.Fatal(openErr)
	}
	replay, replayErr := reopened.Commit(context.Background(), change, options)

	// Assert.
	if commitErr == nil {
		t.Fatal("Commit() unexpectedly completed past injected post-journal fault")
	}
	if replayErr != nil {
		t.Fatalf("idempotent replay after recovery: %v", replayErr)
	}
	if !errors.Is(mismatchErr, store.ErrStorageCorrupt) {
		t.Fatalf("Open with mismatched algorithm error = %v, want storage corruption", mismatchErr)
	}
	if !strings.HasPrefix(string(base.Revision()), "test-sha512-v1:") || !strings.HasPrefix(string(replay.ResultRevision), "test-sha512-v1:") {
		t.Fatalf("revisions were not qualified with configured algorithm: %s -> %s", base.Revision(), replay.ResultRevision)
	}
}

type failingWriteHash struct{ hash.Hash }

func (f failingWriteHash) Write([]byte) (int, error) { return 0, nil }

type failingWriteHashAlgorithm struct{}

func (failingWriteHashAlgorithm) Name() string   { return "failing-write-v1" }
func (failingWriteHashAlgorithm) New() hash.Hash { return failingWriteHash{Hash: sha256.New()} }

type panickingHash struct {
	hash.Hash
	write, sum bool
}

func (h panickingHash) Write(p []byte) (int, error) {
	if h.write {
		panic("Write")
	}
	return h.Hash.Write(p)
}
func (h panickingHash) Sum(b []byte) []byte {
	if h.sum {
		panic("Sum")
	}
	return h.Hash.Sum(b)
}

type panickingHashAlgorithm struct{ write, sum bool }

func (panickingHashAlgorithm) Name() string { return "panic-hash-v1" }
func (a panickingHashAlgorithm) New() hash.Hash {
	return panickingHash{Hash: sha256.New(), write: a.write, sum: a.sum}
}

type nilHashAlgorithm struct{}

func (nilHashAlgorithm) Name() string   { return "nil-hash-v1" }
func (nilHashAlgorithm) New() hash.Hash { return nil }

func TestUnusableHashAlgorithmIsRejectedAtOpen(t *testing.T) {
	// Arrange.
	root := t.TempDir()

	// Act.
	_, err := Open(root, Config{HashAlgorithm: nilHashAlgorithm{}})

	// Assert.
	if !errors.Is(err, store.ErrInvalidHashAlgorithm) {
		t.Fatalf("Open error = %v, want invalid hash algorithm", err)
	}
}

type oneShotNameAlgorithm struct {
	mu    sync.Mutex
	calls int
}

func (a *oneShotNameAlgorithm) Name() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	if a.calls > 1 {
		panic("Name called after Open")
	}
	return "one-shot-name-v1"
}
func (*oneShotNameAlgorithm) New() hash.Hash { return sha256.New() }

func TestOpenCachesHashAlgorithmNameForJournalAndRecovery(t *testing.T) {
	// Arrange.
	algorithm := &oneShotNameAlgorithm{}
	root, s := adversarialStore(t, Config{HashAlgorithm: algorithm})
	base := adversarialSnapshot(t, s)
	change := adversarialChange(t, "cached-name", base.Revision(), "depends_on")

	// Act.
	receipt, commitErr := s.Commit(context.Background(), change, store.CommitOptions{IdempotencyKey: "cached-name"})
	closeErr := s.Close()
	reopened, openErr := Open(root, Config{HashAlgorithm: algorithm})

	// Assert.
	if commitErr != nil || closeErr != nil {
		t.Fatalf("Commit/Close errors: %v / %v", commitErr, closeErr)
	}
	if !strings.HasPrefix(string(receipt.ResultRevision), "one-shot-name-v1:") {
		t.Fatalf("result revision = %s", receipt.ResultRevision)
	}
	// The same deliberately stateful implementation cannot be reopened: Open
	// preflights Name once. The committed journal itself was already removed,
	// proving no later Name call was needed at the durable boundary.
	if openErr == nil || reopened != nil || !errors.Is(openErr, store.ErrInvalidHashAlgorithm) {
		t.Fatalf("second Open() = %v, %v; want typed name validation failure", reopened, openErr)
	}
}

func TestFailingHashWritePreventsSnapshotAndCommitPublication(t *testing.T) {
	// Arrange.
	root, s := adversarialStore(t, Config{HashAlgorithm: failingWriteHashAlgorithm{}})
	t.Cleanup(func() { _ = s.Close() })
	base := store.Revision("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	change := adversarialChange(t, "hash-write-failure", base, "depends_on")

	// Act.
	_, snapshotErr := s.Snapshot(context.Background())
	_, commitErr := s.Commit(context.Background(), change, store.CommitOptions{IdempotencyKey: "hash-write-failure"})
	_, journalErr := os.Stat(filepath.Join(root, internalDirectory, "journal.json"))

	// Assert.
	if !errors.Is(snapshotErr, io.ErrShortWrite) || !errors.Is(commitErr, io.ErrShortWrite) {
		t.Fatalf("hash write errors: Snapshot=%v Commit=%v", snapshotErr, commitErr)
	}
	if !errors.Is(journalErr, os.ErrNotExist) {
		t.Fatalf("journal published after hash failure: %v", journalErr)
	}
}

func TestPanickingHashPreventsSnapshotAndCommitPublication(t *testing.T) {
	// Arrange.
	for _, algorithm := range []store.HashAlgorithm{panickingHashAlgorithm{write: true}, panickingHashAlgorithm{sum: true}} {
		root, s := adversarialStore(t, Config{HashAlgorithm: algorithm})
		t.Cleanup(func() { _ = s.Close() })
		change := adversarialChange(t, "panic-hash", store.Revision("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), "depends_on")

		// Act.
		_, snapshotErr := s.Snapshot(context.Background())
		_, commitErr := s.Commit(context.Background(), change, store.CommitOptions{IdempotencyKey: "panic-hash"})
		_, journalErr := os.Stat(filepath.Join(root, internalDirectory, "journal.json"))

		// Assert.
		if !errors.Is(snapshotErr, store.ErrInvalidHashAlgorithm) || !errors.Is(commitErr, store.ErrInvalidHashAlgorithm) {
			t.Fatalf("hash panic errors: Snapshot=%v Commit=%v", snapshotErr, commitErr)
		}
		if !errors.Is(journalErr, os.ErrNotExist) {
			t.Fatalf("journal published after hash panic: %v", journalErr)
		}
	}
}
