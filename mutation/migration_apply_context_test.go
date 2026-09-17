package mutation

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/skosovsky/okf/store"
)

func migrationApplyStoreFixture(t *testing.T, fixture migrationCommitOutcomeFixture, stale bool) *scriptedMigrationStore {
	t.Helper()
	snapshot := fixture.snapshot
	if stale {
		snapshot.revision = store.Revision("sha256:" + strings.Repeat("f", 64))
	}
	destination := &scriptedMigrationStore{snapshot: snapshot}
	destination.commit = func(context.Context, store.ChangeSet, store.CommitOptions) (store.CommitReceipt, error) {
		t.Fatal("Commit crossed a canceled planner precommit checkpoint")
		return store.CommitReceipt{}, nil
	}
	return destination
}

func TestMigrationPlannerApply_PrecommitCheckpointsReturnExactZero(t *testing.T) {
	tests := []struct {
		name     string
		function string
		fresh    bool
		stale    bool
	}{
		{name: "resolution validation/fresh", function: "validateMigrationSourceResolutionContext", fresh: true},
		{name: "resolution validation/stale", function: "validateMigrationSourceResolutionContext", stale: true},
		{name: "proof validation/fresh", function: "validateMigrationPlanProofContext", fresh: true},
		{name: "proof validation/stale", function: "validateMigrationPlanProofContext", stale: true},
		{name: "proof digest/fresh", function: "migrationPlanProofDigestContext", fresh: true},
		{name: "proof digest/stale", function: "migrationPlanProofDigestContext", stale: true},
		{name: "resolution clone/fresh", function: "MigrationSourceResolution.cloneContext", fresh: true},
		{name: "resolution clone/stale", function: "MigrationSourceResolution.cloneContext", stale: true},
		{name: "commit request assembly/fresh", function: "migrationApplyCommitOptionsContext", fresh: true},
		{name: "commit request assembly/stale", function: "migrationApplyCommitOptionsContext", stale: true},
		{name: "fresh Preview", function: "MigrationPlanner.Preview", fresh: true},
		{name: "fresh proof canonicalization", function: "canonicalizeMigrationPlanProofContext", fresh: true},
		{name: "fresh proof equality", function: "equalMigrationPlanProofContext", fresh: true},
		{name: "terminal pre-Commit/fresh", function: "migrationApplyPreCommitContext", fresh: true},
		{name: "terminal pre-Commit/stale", function: "migrationApplyPreCommitContext", stale: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			fixture := newMigrationCommitOutcomeFixture(t)
			destination := migrationApplyStoreFixture(t, fixture, test.stale)
			ctx := &migrationPhaseCheckpointContext{
				Context: context.Background(), function: test.function, remaining: 1,
			}

			// Act.
			receipt, err := fixture.planner.Apply(ctx, destination, fixture.apply)

			// Assert.
			if err != context.Canceled || !reflect.DeepEqual(receipt, store.CommitReceipt{}) ||
				destination.commitCalls != 0 || ctx.matches == 0 {
				t.Fatalf("Apply() = (%#v, %v); calls snapshot=%d commit=%d checkpoint=%d",
					receipt, err, destination.snapshotCalls, destination.commitCalls, ctx.matches)
			}
		})
	}
}

type snapshotCancelMigrationStore struct {
	store.Store
	snapshot    store.Snapshot
	cancel      context.CancelFunc
	snapshots   int
	commits     int
	commitError error
}

func (s *snapshotCancelMigrationStore) Snapshot(context.Context) (store.Snapshot, error) {
	s.snapshots++
	s.cancel()
	return s.snapshot, nil
}

func (s *snapshotCancelMigrationStore) Commit(context.Context, store.ChangeSet, store.CommitOptions) (store.CommitReceipt, error) {
	s.commits++
	return store.CommitReceipt{}, s.commitError
}

func TestMigrationPlannerApply_SnapshotCancellationStopsBeforeCommit(t *testing.T) {
	for _, stale := range []bool{false, true} {
		branch := "fresh"
		if stale {
			branch = "stale"
		}
		t.Run(branch, func(t *testing.T) {
			// Arrange.
			fixture := newMigrationCommitOutcomeFixture(t)
			snapshot := fixture.snapshot
			if stale {
				snapshot.revision = store.Revision("sha256:" + strings.Repeat("f", 64))
			}
			ctx, cancel := context.WithCancel(context.Background())
			destination := &snapshotCancelMigrationStore{snapshot: snapshot, cancel: cancel}

			// Act.
			receipt, err := fixture.planner.Apply(ctx, destination, fixture.apply)

			// Assert.
			if err != context.Canceled || !reflect.DeepEqual(receipt, store.CommitReceipt{}) ||
				destination.snapshots != 1 || destination.commits != 0 {
				t.Fatalf("Apply() = (%#v, %v); snapshots=%d commits=%d",
					receipt, err, destination.snapshots, destination.commits)
			}
		})
	}
}

func TestMigrationPlannerApply_StoreOwnsCommitBoundaryCancellation(t *testing.T) {
	for _, stale := range []bool{false, true} {
		branch := "fresh"
		if stale {
			branch = "stale-replay"
		}
		t.Run(branch, func(t *testing.T) {
			// Arrange.
			fixture := newMigrationCommitOutcomeFixture(t)
			destination := migrationApplyStoreFixture(t, fixture, stale)
			destination.commit = func(context.Context, store.ChangeSet, store.CommitOptions) (store.CommitReceipt, error) {
				return store.CommitReceipt{}, context.Canceled
			}

			// Act.
			receipt, err := fixture.planner.Apply(context.Background(), destination, fixture.apply)

			// Assert.
			if err != context.Canceled || !reflect.DeepEqual(receipt, store.CommitReceipt{}) ||
				destination.commitCalls != 1 {
				t.Fatalf("Apply() = (%#v, %v); commits=%d", receipt, err, destination.commitCalls)
			}
		})
	}
}

func TestMigrationPlannerApply_PrecommitDomainErrorAndLocationRemainStable(t *testing.T) {
	// Arrange.
	fixture := newMigrationCommitOutcomeFixture(t)
	fixture.apply.Resolution = MigrationSourceResolution{
		RequestedSelector:  MigrationSelectorAuto,
		DeclarationPresent: true,
		DeclarationValid:   true,
		DeclarationRaw:     "9.0",
		DeclaredVersion:    "9.0",
		ResolvedSource:     "9.0",
		ResolutionSource:   MigrationResolutionFuture,
		FromVersion:        "9.0",
		Transition:         MigrationTransitionBlocked,
		Blockers: []MigrationBlocker{{
			Code:     "unsupported_migration_source",
			Path:     "index.md",
			Location: SourceSpan{Start: 1, End: 2},
			Message:  "future declaration \"9.0\" cannot be rewritten as v0.2",
		}},
	}
	before := fixture.apply.Resolution.Clone()
	destination := migrationApplyStoreFixture(t, fixture, false)

	// Act.
	receipt, err := fixture.planner.Apply(context.Background(), destination, fixture.apply)

	// Assert.
	if err == nil || err.Error() != "unsupported migration version: invalid blocked source finding" ||
		!reflect.DeepEqual(receipt, store.CommitReceipt{}) ||
		!reflect.DeepEqual(fixture.apply.Resolution, before) ||
		destination.snapshotCalls != 0 || destination.commitCalls != 0 {
		t.Fatalf("Apply() = (%#v, %v); resolution=%#v calls=%d/%d",
			receipt, err, fixture.apply.Resolution, destination.snapshotCalls, destination.commitCalls)
	}
}
