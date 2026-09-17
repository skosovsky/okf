package fs

import (
	"context"
	"errors"
	"os"
	"sort"

	"github.com/skosovsky/okf/store"
)

// openObserved is the canonical setup helper for tests whose subject starts
// after lazy Store initialization. Open itself remains side-effect-free; the
// first Snapshot owns private setup and recovery.
func openObserved(root string, config Config) (*Store, error) {
	return observeOpened(openContextWithRecoveryLimitsAndSync(
		context.Background(),
		root,
		config,
		absoluteStagedManifestLimits(),
		testDurabilitySyncImplementation(),
	))
}

func openObservedWithRecoveryLimits(ctx context.Context, root string, config Config, limits stagedManifestLimits) (*Store, error) {
	return observeOpened(openContextWithRecoveryLimitsAndSync(
		ctx,
		root,
		config,
		limits,
		testDurabilitySyncImplementation(),
	))
}

func openObservedWithRecoveryLimitsAndSync(ctx context.Context, root string, config Config, limits stagedManifestLimits, sync durabilitySyncImplementation) (*Store, error) {
	return observeOpened(openContextWithRecoveryLimitsAndSync(ctx, root, config, limits, sync))
}

// testDurabilitySyncImplementation keeps crash and recovery matrices focused
// on ordering, ownership, and state transitions. Tests of sync failures and
// durability call ordering pass an explicit implementation through
// openObservedWithRecoveryLimitsAndSync.
func testDurabilitySyncImplementation() durabilitySyncImplementation {
	return durabilitySyncImplementation{
		file: func(*os.File) error {
			return nil
		},
		directory: func(*Store, string) error {
			return nil
		},
	}
}

// OpenWithoutPhysicalSyncForTesting opens a store with the production
// durability protocol and fault hooks, but without issuing physical fsync
// syscalls. It is exported only from the test build so external-package
// contract tests can exercise the same logical crash boundaries.
func OpenWithoutPhysicalSyncForTesting(root string, config Config) (*Store, error) {
	return openContextWithRecoveryLimitsAndSync(
		context.Background(),
		root,
		config,
		absoluteStagedManifestLimits(),
		testDurabilitySyncImplementation(),
	)
}

func observeOpened(s *Store, err error) (*Store, error) {
	if err != nil {
		return nil, err
	}
	if _, err := s.Snapshot(context.Background()); err != nil {
		return nil, errors.Join(err, s.Close())
	}
	return s, nil
}

// durableFaultInventory is derived from the authoritative executable owner
// registry. It intentionally contains no second literal step inventory.
func durableFaultInventory() []Step {
	steps := make([]Step, 0, len(stepOwnerRegistry))
	for step := range stepOwnerRegistry {
		steps = append(steps, step)
	}
	sort.Slice(steps, func(i, j int) bool { return steps[i] < steps[j] })
	return steps
}

func newSnapshot(ctx context.Context, files map[string][]byte) (*snapshot, error) {
	return newSnapshotWithAlgorithm(ctx, files, nil)
}

// decodeJournal accepts only the current complete on-disk transaction format.
// It is a test convenience over the production limits-aware decoder.
func decodeJournal(raw []byte) (journal, error) {
	return decodeJournalWithAlgorithmName(raw, nil, "sha256")
}

func decodeJournalWithAlgorithm(raw []byte, algorithm store.HashAlgorithm) (journal, error) {
	return decodeJournalWithAlgorithmName(raw, algorithm, algorithmName(algorithm))
}

func decodeJournalWithAlgorithmName(raw []byte, algorithm store.HashAlgorithm, expectedAlgorithm string) (journal, error) {
	return decodeJournalWithAlgorithmNameAndLimits(raw, algorithm, expectedAlgorithm, absoluteStagedManifestLimits())
}

func (s *Store) stageJournal(next *snapshot, receipt store.CommitReceipt, replay replaceReplay) (journal, error) {
	return s.stageJournalWithManifestLimits(next, receipt, replay, productionJournalManifestByteLimits())
}

func (s *Store) stageJournalWithManifestLimits(
	next *snapshot,
	receipt store.CommitReceipt,
	replay replaceReplay,
	manifestLimits journalManifestByteLimits,
) (journal, error) {
	j, _, err := s.stageJournalWithManifestLimitsObserved(context.Background(), next, receipt, replay, manifestLimits)
	return j, err
}

func (s *Store) stageJournalWithManifestLimitsObserved(
	ctx context.Context,
	next *snapshot,
	receipt store.CommitReceipt,
	replay replaceReplay,
	manifestLimits journalManifestByteLimits,
) (journal, stagePublicationOwnership, error) {
	j, _, err := s.prepareJournalContext(ctx, next, receipt, replay, manifestLimits)
	if err != nil {
		return journal{}, stagePublicationOwnership{}, err
	}
	owned, err := s.stageJournalPayloadsObserved(ctx, next, j)
	if err != nil {
		return journal{}, owned, err
	}
	return j, owned, nil
}

// stageDurableJournalFixture creates recovery evidence through the same live
// publication primitives as production, stopping immediately after the
// physical journal-directory durability boundary. The returned journal keeps
// its claim, witness, binding and stage sentinel inventory for recovery.
func stageDurableJournalFixture(t interface {
	Helper()
	Fatal(...any)
}, s *Store, next *snapshot, receipt store.CommitReceipt) journal {
	t.Helper()
	j, raw, err := s.prepareJournalContext(context.Background(), next, receipt, replaceReplay{}, productionJournalManifestByteLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.stageJournalPayloadsObserved(context.Background(), next, j); err != nil {
		t.Fatal(err)
	}
	name, err := journalPath(receipt.RequestDigest)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := s.writeJournalObserved(context.Background(), name, raw)
	if err != nil {
		t.Fatal(err)
	}
	if !publication.durable || publication.identity == nil {
		t.Fatal("journal fixture did not cross the physical durability boundary")
	}
	return j
}

func (s *Store) writeReceipt(receipt store.CommitReceipt) error {
	return s.writeReceiptWithReplay(receipt, replaceReplay{})
}
