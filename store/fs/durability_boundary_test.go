package fs

// Durability boundary outcomes and context behavior.

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/skosovsky/okf/store"
)

func TestDurableBoundaryOutcomeMatrix(t *testing.T) {
	tests := []struct {
		name           string
		configure      func(*Store, context.CancelFunc, error)
		idempotent     bool
		wantCommitted  bool
		wantResult     bool
		wantEvidence   bool
		wantCanceled   bool
		wantInjectedIO bool
	}{
		{
			name: "fault before transaction directory sync aborts exactly",
			configure: func(s *Store, _ context.CancelFunc, injected error) {
				fired := false
				s.config.Fault = func(step Step) error {
					if !fired && step == StepJournalDirectorySync {
						fired = true
						return injected
					}
					return nil
				}
			},
			wantInjectedIO: true,
		},
		{
			name: "physical transaction directory sync failure aborts exactly",
			configure: func(s *Store, _ context.CancelFunc, injected error) {
				physical := s.directorySync
				fired := false
				s.directorySync = func(store *Store, dir string) error {
					if !fired && dir == filepath.ToSlash(filepath.Join(internalDirectory, "transactions")) {
						fired = true
						return injected
					}
					return physical(store, dir)
				}
			},
			wantInjectedIO: true,
		},
		{
			name: "cancellation during partial staging aborts exactly",
			configure: func(s *Store, cancel context.CancelFunc, _ error) {
				fired := false
				s.config.PostFault = func(step Step) error {
					if !fired && step == StepPrivateMetadataClose {
						fired = true
						cancel()
					}
					return nil
				}
			},
			wantCanceled: true,
		},
		{
			name: "postfault after physical sync retains recovery evidence",
			configure: func(s *Store, _ context.CancelFunc, injected error) {
				fired := false
				s.config.PostFault = func(step Step) error {
					if !fired && step == StepJournalDirectorySync {
						fired = true
						return injected
					}
					return nil
				}
			},
			wantCommitted: true, wantEvidence: true, wantInjectedIO: true,
		},
		{
			name: "cancellation at clean physical sync converges",
			configure: func(s *Store, cancel context.CancelFunc, _ error) {
				fired := false
				s.config.DirectorySync = func(dir string) error {
					if !fired && dir == filepath.ToSlash(filepath.Join(internalDirectory, "transactions")) {
						fired = true
						cancel()
					}
					return nil
				}
			},
			wantCommitted: true, wantResult: true, wantCanceled: true,
		},
		{
			name: "apply I/O failure outranks cancellation",
			configure: func(s *Store, cancel context.CancelFunc, injected error) {
				canceled := false
				s.config.DirectorySync = func(dir string) error {
					if !canceled && dir == filepath.ToSlash(filepath.Join(internalDirectory, "transactions")) {
						canceled = true
						cancel()
					}
					return nil
				}
				fired := false
				s.config.Fault = func(step Step) error {
					if !fired && step == StepFileWrite {
						fired = true
						return injected
					}
					return nil
				}
			},
			wantCommitted: true, wantEvidence: true, wantInjectedIO: true,
		},
		{
			name: "receipt failure preserves durable outcome",
			configure: func(s *Store, _ context.CancelFunc, injected error) {
				fired := false
				s.config.Fault = func(step Step) error {
					if !fired && step == StepReceiptWrite {
						fired = true
						return injected
					}
					return nil
				}
			},
			idempotent: true, wantCommitted: true, wantResult: true, wantEvidence: true, wantInjectedIO: true,
		},
		{
			name: "journal cleanup failure preserves durable outcome",
			configure: func(s *Store, _ context.CancelFunc, injected error) {
				fired := false
				s.config.Fault = func(step Step) error {
					if !fired && step == StepRemove {
						fired = true
						return injected
					}
					return nil
				}
			},
			wantCommitted: true, wantResult: true, wantEvidence: true, wantInjectedIO: true,
		},
		{
			name: "stage cleanup failure preserves durable outcome",
			configure: func(s *Store, _ context.CancelFunc, injected error) {
				fired := false
				s.config.Fault = func(step Step) error {
					if !fired && step == StepStageCleanupPayloadRemove {
						fired = true
						return injected
					}
					return nil
				}
			},
			wantCommitted: true, wantResult: true, wantEvidence: true, wantInjectedIO: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root, s := adversarialStore(t, Config{})
			base := adversarialSnapshot(t, s)
			next, err := newSnapshot(context.Background(), map[string][]byte{
				"a.md": []byte(adversarialDocument("changed")),
				"b.md": []byte(adversarialDocument("B")),
			})
			if err != nil {
				t.Fatal(err)
			}
			key := store.IdempotencyKey("")
			if test.idempotent {
				key = "task17-matrix"
			}
			receipt := testJournalReceipt(base.Revision(), next.Revision(), "task17-matrix", key)
			unrelated := map[string][]byte{
				filepath.Join(root, filepath.FromSlash(path.Join(internalDirectory, "staging", "unrelated.bin"))):      []byte("unrelated-stage"),
				filepath.Join(root, filepath.FromSlash(path.Join(internalDirectory, "transactions", "unrelated.bin"))): []byte("unrelated-journal"),
			}
			for name, data := range unrelated {
				if err := os.WriteFile(name, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			injected := errors.New("injected durable I/O")
			test.configure(s, cancel, injected)

			// Act.
			publishErr := s.publish(ctx, next, receipt)
			raw, rawErr := readVisibleRoot(context.Background(), s.rootFD)
			journals := pendingJournalNames(t, root)
			stageName, stagePathErr := journalStage(receipt.RequestDigest)
			if stagePathErr != nil {
				t.Fatal(stagePathErr)
			}
			_, stageStatErr := os.Stat(filepath.Join(root, filepath.FromSlash(stageName)))
			stageExists := stageStatErr == nil
			if stageStatErr != nil && !errors.Is(stageStatErr, os.ErrNotExist) {
				t.Fatal(stageStatErr)
			}

			// Assert.
			var committed *store.CommittedError
			if errors.As(publishErr, &committed) != test.wantCommitted {
				t.Fatalf("committed error=%v want=%t: %v", committed, test.wantCommitted, publishErr)
			}
			if test.wantCommitted {
				if !reflect.DeepEqual(committed.Receipt(), receipt) || store.ValidateCommitReceipt(committed.Receipt()) != nil {
					t.Fatalf("committed receipt=%#v want valid %#v", committed.Receipt(), receipt)
				}
			}
			if errors.Is(publishErr, injected) != test.wantInjectedIO || errors.Is(publishErr, context.Canceled) != test.wantCanceled {
				t.Fatalf("error identity injected=%t canceled=%t: %v", errors.Is(publishErr, injected), errors.Is(publishErr, context.Canceled), publishErr)
			}
			if rawErr != nil {
				t.Fatal(rawErr)
			}
			want := base.(*snapshot).files
			if test.wantResult {
				want = next.files
			}
			if !sameVisibleFiles(raw, want) {
				t.Fatalf("visible state=%v want=%v", sortedFilePaths(raw), sortedFilePaths(want))
			}
			if (len(journals) != 0 || stageExists) != test.wantEvidence {
				t.Fatalf("journals=%v stage=%t want evidence=%t", journals, stageExists, test.wantEvidence)
			}
			for name, wantBytes := range unrelated {
				gotBytes, readErr := os.ReadFile(name)
				if readErr != nil || !reflect.DeepEqual(gotBytes, wantBytes) {
					t.Fatalf("unrelated artifact %s changed: bytes=%q error=%v", name, gotBytes, readErr)
				}
			}
			if test.wantEvidence {
				recovered, recoverErr := s.Snapshot(context.Background())
				if recoverErr != nil || recovered.Revision() != next.Revision() {
					t.Fatalf("recovery revision=%v error=%v want=%s", recovered, recoverErr, next.Revision())
				}
				if remaining := pendingJournalNames(t, root); len(remaining) != 0 {
					t.Fatalf("recovery left journals=%v", remaining)
				}
			}
		})
	}
}

func TestPostBoundaryContextRetainsValuesWithoutCancellation(t *testing.T) {
	// Arrange.
	var canceled, seen atomic.Bool
	_, s := adversarialStore(t, Config{HashAlgorithm: contextValueHash{canceled: &canceled, seen: &seen}})
	base := adversarialSnapshot(t, s)
	next, err := newSnapshotWithAlgorithm(context.Background(), map[string][]byte{
		"a.md": []byte(adversarialDocument("changed")),
		"b.md": []byte(adversarialDocument("B")),
	}, s.config.HashAlgorithm)
	if err != nil {
		t.Fatal(err)
	}
	receipt := testJournalReceipt(base.Revision(), next.Revision(), "task17-context", "")
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), durabilityContextKey("proof"), "retained"))
	fired := false
	s.config.DirectorySync = func(dir string) error {
		if !fired && dir == filepath.ToSlash(filepath.Join(internalDirectory, "transactions")) {
			fired = true
			canceled.Store(true)
			cancel()
		}
		return nil
	}

	// Act.
	publishErr := s.publish(ctx, next, receipt)

	// Assert.
	var committed *store.CommittedError
	if !errors.Is(publishErr, context.Canceled) || !errors.As(publishErr, &committed) || !seen.Load() {
		t.Fatalf("publish error=%v committed=%v value observed=%t", publishErr, committed, seen.Load())
	}
	if got := adversarialSnapshot(t, s).Revision(); got != next.Revision() {
		t.Fatalf("revision=%s want=%s", got, next.Revision())
	}
}

func TestPreBoundaryCleanupPreservesSameBytesForeignJournalInode(t *testing.T) {
	// Arrange.
	root, s := adversarialStore(t, Config{})
	base := adversarialSnapshot(t, s)
	next, err := newSnapshot(context.Background(), map[string][]byte{
		"a.md": []byte(adversarialDocument("changed")),
		"b.md": []byte(adversarialDocument("B")),
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt := testJournalReceipt(base.Revision(), next.Revision(), "task17-foreign", "")
	journalName, err := journalPath(receipt.RequestDigest)
	if err != nil {
		t.Fatal(err)
	}
	fired := false
	var foreignRaw []byte
	var foreignInfo os.FileInfo
	s.config.DirectorySync = func(dir string) error {
		if fired || dir != filepath.ToSlash(filepath.Join(internalDirectory, "transactions")) {
			return nil
		}
		fired = true
		absolute := filepath.Join(root, filepath.FromSlash(journalName))
		raw, readErr := os.ReadFile(absolute)
		if readErr != nil {
			return readErr
		}
		if removeErr := os.Remove(absolute); removeErr != nil {
			return removeErr
		}
		if writeErr := os.WriteFile(absolute, raw, 0o600); writeErr != nil {
			return writeErr
		}
		foreignRaw = append([]byte(nil), raw...)
		foreignInfo, readErr = os.Stat(absolute)
		return readErr
	}

	// Act.
	publishErr := s.publish(context.Background(), next, receipt)
	_, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(journalName)))
	raw, rawErr := readVisibleRoot(context.Background(), s.rootFD)

	// Assert.
	var committed *store.CommittedError
	if !errors.Is(publishErr, store.ErrStorageCorrupt) || !errors.As(publishErr, &committed) || committed.Receipt().RequestDigest != receipt.RequestDigest {
		t.Fatalf("publish error=%v committed=%v, want receipt-bearing post-boundary identity rejection", publishErr, committed)
	}
	if statErr != nil {
		t.Fatalf("foreign journal replacement removed: %v", statErr)
	}
	if rawErr != nil || !sameVisibleFiles(raw, base.(*snapshot).files) {
		t.Fatalf("visible state changed: read=%v files=%v", rawErr, sortedFilePaths(raw))
	}
	s.config.DirectorySync = nil
	firstRecovery := s.recoverForTest(context.Background())
	secondRecovery := s.recoverForTest(context.Background())
	var recovered *store.CommittedError
	if !errors.Is(firstRecovery, errArtifactClaimConflict) || errors.As(firstRecovery, &recovered) || !errors.Is(secondRecovery, errArtifactClaimConflict) {
		t.Fatalf("recovery first=%v committed=%v second=%v, want stable plain preownership conflict", firstRecovery, recovered, secondRecovery)
	}
	foreignAfter, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(journalName)))
	foreignAfterRaw, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(journalName)))
	stageName, _ := journalStage(receipt.RequestDigest)
	_, stageErr := os.Stat(filepath.Join(root, filepath.FromSlash(path.Dir(stageName))))
	if statErr != nil || readErr != nil || foreignInfo == nil || !os.SameFile(foreignInfo, foreignAfter) || foreignInfo.Mode() != foreignAfter.Mode() || !bytes.Equal(foreignRaw, foreignAfterRaw) || stageErr != nil {
		t.Fatalf("foreign/proof state changed: stat=%v read=%v same=%t mode=%v/%v bytes=%t stage=%v", statErr, readErr, foreignInfo != nil && foreignAfter != nil && os.SameFile(foreignInfo, foreignAfter), modeOf(foreignInfo), modeOf(foreignAfter), bytes.Equal(foreignRaw, foreignAfterRaw), stageErr)
	}
	visibleAfter, visibleErr := readVisibleRoot(context.Background(), s.rootFD)
	if visibleErr != nil || !sameVisibleFiles(visibleAfter, base.(*snapshot).files) {
		t.Fatalf("conflicted recovery changed visible base: read=%v files=%v", visibleErr, sortedFilePaths(visibleAfter))
	}
}

func TestPreBoundaryCleanupPreservesForeignPrivateArtifactInodes(t *testing.T) {
	tests := []struct {
		name string
		step Step
		swap func(*testing.T, string, string) (string, []byte, os.FileInfo)
	}{
		{
			name: "staged payload",
			step: StepStageRename,
			swap: func(t *testing.T, root, stage string) (string, []byte, os.FileInfo) {
				return replaceRegularWithSameBytes(t, root, filepath.ToSlash(filepath.Join(stage, payloadName(0))))
			},
		},
		{
			name: "atomic scratch",
			step: StepStageFileWrite,
			swap: func(t *testing.T, root, _ string) (string, []byte, os.FileInfo) {
				entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(temporaryDirectory)))
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range entries {
					if !entry.IsDir() && len(entry.Name()) > len(".okf-tmp-") && entry.Name()[:len(".okf-tmp-")] == ".okf-tmp-" {
						return replaceRegularWithSameBytes(t, root, filepath.ToSlash(filepath.Join(temporaryDirectory, entry.Name())))
					}
				}
				t.Fatal("stage scratch not found")
				return "", nil, nil
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root, s := adversarialStore(t, Config{})
			base := adversarialSnapshot(t, s)
			next, err := newSnapshot(context.Background(), map[string][]byte{
				"a.md": []byte(adversarialDocument("changed")),
				"b.md": []byte(adversarialDocument("B")),
			})
			if err != nil {
				t.Fatal(err)
			}
			receipt := testJournalReceipt(base.Revision(), next.Revision(), "task17-owned-"+test.name, "")
			stage, err := journalStage(receipt.RequestDigest)
			if err != nil {
				t.Fatal(err)
			}
			injected := errors.New("stop after foreign replacement")
			var foreignName string
			var foreignBytes []byte
			var foreignInfo os.FileInfo
			fired := false
			s.config.PostFault = func(step Step) error {
				if fired || step != test.step {
					return nil
				}
				fired = true
				foreignName, foreignBytes, foreignInfo = test.swap(t, root, stage)
				return injected
			}

			// Act.
			publishErr := s.publish(context.Background(), next, receipt)
			afterInfo, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(foreignName)))
			var afterBytes []byte
			if len(foreignBytes) != 0 {
				afterBytes, err = os.ReadFile(filepath.Join(root, filepath.FromSlash(foreignName)))
			}
			raw, rawErr := readVisibleRoot(context.Background(), s.rootFD)

			// Assert.
			var committed *store.CommittedError
			if !fired || !errors.Is(publishErr, injected) || errors.As(publishErr, &committed) {
				t.Fatalf("publish error=%v fired=%t committed=%v", publishErr, fired, committed)
			}
			if statErr != nil || foreignInfo == nil || !os.SameFile(foreignInfo, afterInfo) || afterInfo.Mode() != foreignInfo.Mode() || !reflect.DeepEqual(afterBytes, foreignBytes) {
				t.Fatalf("foreign artifact changed: stat=%v same=%t mode=%v/%v bytes=%t", statErr, foreignInfo != nil && afterInfo != nil && os.SameFile(foreignInfo, afterInfo), modeOf(afterInfo), modeOf(foreignInfo), reflect.DeepEqual(afterBytes, foreignBytes))
			}
			if rawErr != nil || !sameVisibleFiles(raw, base.(*snapshot).files) {
				t.Fatalf("visible state changed: read=%v files=%v", rawErr, sortedFilePaths(raw))
			}
		})
	}
}

func TestRecoveryCancellationOwnershipBoundary(t *testing.T) {
	t.Run("canceled before journal ownership preserves evidence", func(t *testing.T) {
		// Arrange.
		root, s, _, next := stageRecoveryJournal(t, Config{})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		// Act.
		recoverErr := s.recoverForTest(ctx)

		// Assert.
		if !errors.Is(recoverErr, context.Canceled) {
			t.Fatalf("recover error=%v want cancellation", recoverErr)
		}
		if journals := pendingJournalNames(t, root); len(journals) != 1 {
			t.Fatalf("pre-ownership cancellation journals=%v want one", journals)
		}
		if got := adversarialSnapshot(t, s).Revision(); got != next.Revision() {
			t.Fatalf("retry revision=%s want=%s", got, next.Revision())
		}
	})

	t.Run("canceled during provenance stays pre-ownership and retry retains values", func(t *testing.T) {
		// Arrange.
		var canceled, seen atomic.Bool
		root, s, _, next := stageRecoveryJournal(t, Config{HashAlgorithm: contextValueHash{canceled: &canceled, seen: &seen}})
		ctx, cancel := context.WithCancel(context.WithValue(context.Background(), durabilityContextKey("proof"), "retained"))
		fired := false
		s.provenanceReadHook = func(string) {
			if !fired {
				fired = true
				canceled.Store(true)
				cancel()
			}
		}

		// Act.
		recoverErr := s.recoverForTest(ctx)
		preRetryJournals := pendingJournalNames(t, root)
		retryCtx := context.WithValue(context.Background(), durabilityContextKey("proof"), "retained")
		retryErr := s.recoverForTest(retryCtx)
		raw, rawErr := readVisibleRoot(context.Background(), s.rootFD)

		// Assert.
		if !errors.Is(recoverErr, context.Canceled) || !fired || len(preRetryJournals) != 1 || retryErr != nil || !seen.Load() {
			t.Fatalf("recover=%v fired=%t journals=%v retry=%v value observed=%t", recoverErr, fired, preRetryJournals, retryErr, seen.Load())
		}
		if rawErr != nil || !sameVisibleFiles(raw, next.files) {
			t.Fatalf("recovered state read=%v files=%v", rawErr, sortedFilePaths(raw))
		}
		if journals := pendingJournalNames(t, root); len(journals) != 0 {
			t.Fatalf("post-ownership cancellation journals=%v want none", journals)
		}
	})

	t.Run("canceled after validated ownership converges with receipt and values", func(t *testing.T) {
		// Arrange.
		var canceled, seen atomic.Bool
		root, s, _, next := stageRecoveryJournal(t, Config{HashAlgorithm: contextValueHash{canceled: &canceled, seen: &seen}})
		ctx, cancel := context.WithCancel(context.WithValue(context.Background(), durabilityContextKey("proof"), "retained"))
		fired := false
		s.descriptorBarrier = func(operation string) error {
			if operation == "recovery_ownership_validated" && !fired {
				fired = true
				canceled.Store(true)
				cancel()
			}
			return nil
		}

		// Act.
		recoverErr := s.recoverForTest(ctx)
		raw, rawErr := readVisibleRoot(context.Background(), s.rootFD)
		var committed *store.CommittedError

		// Assert.
		if !fired || !seen.Load() || !errors.Is(recoverErr, context.Canceled) || !errors.As(recoverErr, &committed) {
			t.Fatalf("recover=%v fired=%t value=%t committed=%v", recoverErr, fired, seen.Load(), committed)
		}
		if rawErr != nil || !sameVisibleFiles(raw, next.files) || len(pendingJournalNames(t, root)) != 0 {
			t.Fatalf("state read=%v files=%v journals=%v", rawErr, sortedFilePaths(raw), pendingJournalNames(t, root))
		}
	})
}
