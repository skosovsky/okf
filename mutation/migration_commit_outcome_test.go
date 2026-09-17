package mutation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
)

type scriptedMigrationSnapshot struct {
	memorySource
	revision store.Revision
}

func (s scriptedMigrationSnapshot) Revision() store.Revision { return s.revision }

func (s scriptedMigrationSnapshot) OpenConcept(id bundle.ConceptID) (bundle.Concept, error) {
	loaded, err := bundle.Load(context.Background(), s.memorySource)
	if err != nil {
		return bundle.Concept{}, err
	}
	concept, ok := loaded.Get(id)
	if !ok {
		return bundle.Concept{}, os.ErrNotExist
	}
	return concept, nil
}

func (s scriptedMigrationSnapshot) ListConcepts() ([]bundle.ConceptID, error) {
	loaded, err := bundle.Load(context.Background(), s.memorySource)
	if err != nil {
		return nil, err
	}
	return loaded.ConceptIDsContext(context.Background())
}

type scriptedMigrationStore struct {
	snapshot      store.Snapshot
	snapshotCalls int
	previewCalls  int
	commitCalls   int
	commit        func(context.Context, store.ChangeSet, store.CommitOptions) (store.CommitReceipt, error)
}

func (s *scriptedMigrationStore) Snapshot(context.Context) (store.Snapshot, error) {
	s.snapshotCalls++
	return s.snapshot, nil
}

func (s *scriptedMigrationStore) Preview(context.Context, store.ChangeSet) (store.Preview, error) {
	s.previewCalls++
	return store.Preview{}, errors.New("scripted migration store: unexpected Preview")
}

func (s *scriptedMigrationStore) Commit(ctx context.Context, change store.ChangeSet, options store.CommitOptions) (store.CommitReceipt, error) {
	s.commitCalls++
	return s.commit(ctx, change, options)
}

type migrationCommitOutcomeFixture struct {
	planner  MigrationPlanner
	apply    MigrationApplyRequest
	snapshot scriptedMigrationSnapshot
}

func newMigrationCommitOutcomeFixture(t *testing.T) migrationCommitOutcomeFixture {
	t.Helper()
	source := memorySource{
		"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [Alpha](alpha.md)\n"),
		"alpha.md": []byte("---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n---\n\nAlpha.\n"),
	}
	request := MigrationRequest{
		ID:          "migration-commit-outcome",
		Actor:       "operator",
		FromVersion: MigrationVersionV01,
		ToVersion:   MigrationVersionV02,
		Migration: store.V01ToV02Migration{
			GeneratedBy:       "process:migration",
			TimestampPolicy:   store.LegacyTimestampRemoveAfterCopy,
			TimestampConflict: store.TimestampConflictReject,
		},
	}
	planner := NewMigrationPlanner()
	resolution := testMigrationResolution(t, source)
	preview, err := planner.Preview(context.Background(), source, resolution, request)
	if err != nil {
		t.Fatal(err)
	}
	return migrationCommitOutcomeFixture{
		planner: planner,
		apply: MigrationApplyRequest{
			Request: request, Resolution: preview.Resolution, Proof: preview.Proof,
			PlanDigest: preview.PlanDigest,
			Options:    store.CommitOptions{IdempotencyKey: "migration-commit-outcome"},
		},
		snapshot: scriptedMigrationSnapshot{memorySource: source, revision: preview.Proof.BaseRevision},
	}
}

func migrationCommitOutcomeReceipt(t *testing.T, change store.ChangeSet, options store.CommitOptions, proof MigrationPlanProof) store.CommitReceipt {
	t.Helper()
	digest, err := change.RequestDigest()
	if err != nil {
		t.Fatal(err)
	}
	receipt := store.CommitReceipt{
		FormatVersion:  store.CommitReceiptFormatVersion,
		ChangeSetID:    change.ID,
		IdempotencyKey: options.IdempotencyKey,
		RequestDigest:  digest,
		BaseRevision:   change.BaseRevision,
		ResultRevision: proof.ResultRevision,
		CommitTime:     time.Unix(1, 0).UTC(),
		ChangedRefs:    append([]bundle.RelationRef(nil), proof.ChangedRefs...),
		ChangedFiles:   append([]store.FileChange(nil), proof.ChangedFiles...),
	}
	if err := store.ValidateCommitReceipt(receipt); err != nil {
		t.Fatalf("fixture receipt: %v", err)
	}
	return receipt
}

func TestMigrationPlannerApplyPreservesAuthenticatedCommittedOutcome(t *testing.T) {
	for _, stale := range []bool{false, true} {
		branch := "fresh"
		if stale {
			branch = "stale-replay"
		}
		for _, test := range []struct {
			name  string
			cause error
		}{
			{name: "post-boundary IO", cause: errors.New("post-boundary IO identity")},
			{name: "post-boundary cancellation", cause: context.Canceled},
		} {
			t.Run(branch+"/"+test.name, func(t *testing.T) {
				// Arrange.
				fixture := newMigrationCommitOutcomeFixture(t)
				if stale {
					fixture.snapshot.revision = store.Revision("sha256:" + strings.Repeat("f", 64))
				}
				var wantReceipt store.CommitReceipt
				var wantErr error
				destination := &scriptedMigrationStore{snapshot: fixture.snapshot}
				destination.commit = func(_ context.Context, change store.ChangeSet, options store.CommitOptions) (store.CommitReceipt, error) {
					wantReceipt = migrationCommitOutcomeReceipt(t, change, options, fixture.apply.Proof)
					wantErr = store.NewCommittedError(wantReceipt, test.cause)
					return wantReceipt.Clone(), wantErr
				}

				// Act.
				gotReceipt, gotErr := fixture.planner.Apply(context.Background(), destination, fixture.apply)

				// Assert.
				if gotErr != wantErr || !reflect.DeepEqual(gotReceipt, wantReceipt) ||
					!errors.Is(gotErr, test.cause) || destination.snapshotCalls != 1 ||
					destination.previewCalls != 0 || destination.commitCalls != 1 {
					t.Fatalf("Apply() = (%#v, %v), want (%#v, exact %v); calls=%d/%d/%d",
						gotReceipt, gotErr, wantReceipt, wantErr,
						destination.snapshotCalls, destination.previewCalls, destination.commitCalls)
				}
			})
		}
	}
}

func TestMigrationPlannerApplyDurableSuccessWinsPostReturnCancellation(t *testing.T) {
	// Arrange.
	fixture := newMigrationCommitOutcomeFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	var wantReceipt store.CommitReceipt
	destination := &scriptedMigrationStore{snapshot: fixture.snapshot}
	destination.commit = func(_ context.Context, change store.ChangeSet, options store.CommitOptions) (store.CommitReceipt, error) {
		wantReceipt = migrationCommitOutcomeReceipt(t, change, options, fixture.apply.Proof)
		cancel()
		return wantReceipt.Clone(), nil
	}

	// Act.
	gotReceipt, gotErr := fixture.planner.Apply(ctx, destination, fixture.apply)

	// Assert.
	if gotErr != nil || !reflect.DeepEqual(gotReceipt, wantReceipt) || destination.commitCalls != 1 {
		t.Fatalf("Apply() = (%#v, %v), want durable %#v; commits=%d", gotReceipt, gotErr, wantReceipt, destination.commitCalls)
	}
}

func TestMigrationPlannerApplyRejectsUntrustedCommittedOutcome(t *testing.T) {
	tests := []struct {
		name    string
		outcome func(store.CommitReceipt, store.CommitReceipt, error, error) (store.CommitReceipt, error)
	}{
		{name: "returned receipt differs from error receipt", outcome: func(matching, _ store.CommitReceipt, _ error, foreignErr error) (store.CommitReceipt, error) {
			return matching, foreignErr
		}},
		{name: "error receipt differs from returned receipt", outcome: func(_ store.CommitReceipt, foreign store.CommitReceipt, matchingErr, _ error) (store.CommitReceipt, error) {
			return foreign, matchingErr
		}},
		{name: "foreign recovered receipt", outcome: func(_ store.CommitReceipt, foreign store.CommitReceipt, _ error, foreignErr error) (store.CommitReceipt, error) {
			return foreign, foreignErr
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			fixture := newMigrationCommitOutcomeFixture(t)
			cause := errors.New("committed cause identity")
			var wantCause error
			destination := &scriptedMigrationStore{snapshot: fixture.snapshot}
			destination.commit = func(_ context.Context, change store.ChangeSet, options store.CommitOptions) (store.CommitReceipt, error) {
				matching := migrationCommitOutcomeReceipt(t, change, options, fixture.apply.Proof)
				foreign := matching.Clone()
				foreign.ChangeSetID = "foreign-migration"
				matchingErr := store.NewCommittedError(matching, cause)
				foreignErr := store.NewCommittedError(foreign, cause)
				returned, outcomeErr := test.outcome(matching, foreign, matchingErr, foreignErr)
				wantCause = outcomeErr
				return returned, outcomeErr
			}

			// Act.
			gotReceipt, gotErr := fixture.planner.Apply(context.Background(), destination, fixture.apply)

			// Assert.
			if !reflect.DeepEqual(gotReceipt, store.CommitReceipt{}) ||
				!errors.Is(gotErr, ErrMigrationPlanMismatch) || !errors.Is(gotErr, wantCause) {
				t.Fatalf("Apply() = (%#v, %v), want zero receipt with integrity error preserving %v", gotReceipt, gotErr, wantCause)
			}
		})
	}
}

func TestMigrationPlannerApplyRejectsDistinctIndividuallyValidCommittedReceipts(t *testing.T) {
	for _, stale := range []bool{false, true} {
		branch := "fresh"
		if stale {
			branch = "stale-replay"
		}
		t.Run(branch, func(t *testing.T) {
			// Arrange.
			fixture := newMigrationCommitOutcomeFixture(t)
			if stale {
				fixture.snapshot.revision = store.Revision("sha256:" + strings.Repeat("f", 64))
			}
			cause := errors.New("committed receipt mismatch cause identity")
			var wantErr error
			destination := &scriptedMigrationStore{snapshot: fixture.snapshot}
			destination.commit = func(_ context.Context, change store.ChangeSet, options store.CommitOptions) (store.CommitReceipt, error) {
				returned := migrationCommitOutcomeReceipt(t, change, options, fixture.apply.Proof)
				committed := returned.Clone()
				committed.CommitTime = returned.CommitTime.Add(time.Second).UTC()
				if err := validateMigrationReceipt(change, options, fixture.apply.Proof, returned); err != nil {
					t.Fatalf("returned receipt must be independently plan-valid: %v", err)
				}
				if err := validateMigrationReceipt(change, options, fixture.apply.Proof, committed); err != nil {
					t.Fatalf("committed-error receipt must be independently plan-valid: %v", err)
				}
				wantErr = store.NewCommittedError(committed, cause)
				return returned, wantErr
			}

			// Act.
			gotReceipt, gotErr := fixture.planner.Apply(context.Background(), destination, fixture.apply)

			// Assert.
			if !reflect.DeepEqual(gotReceipt, store.CommitReceipt{}) ||
				!errors.Is(gotErr, ErrMigrationPlanMismatch) || !errors.Is(gotErr, wantErr) ||
				destination.snapshotCalls != 1 || destination.previewCalls != 0 || destination.commitCalls != 1 {
				t.Fatalf("Apply() = (%#v, %v), want zero receipt with mismatch preserving exact %v; calls=%d/%d/%d",
					gotReceipt, gotErr, wantErr,
					destination.snapshotCalls, destination.previewCalls, destination.commitCalls)
			}
		})
	}
}

func TestMigrationPlannerApplyReturnsIsolatedReceiptCopies(t *testing.T) {
	for _, committedFailure := range []bool{false, true} {
		name := "nil-error"
		if committedFailure {
			name = "committed-error"
		}
		t.Run(name, func(t *testing.T) {
			// Arrange.
			fixture := newMigrationCommitOutcomeFixture(t)
			cause := errors.New("post-boundary clone isolation cause")
			var sourceReceipt, sourceSnapshot, committedSnapshot store.CommitReceipt
			var committedErr *store.CommittedError
			destination := &scriptedMigrationStore{snapshot: fixture.snapshot}
			destination.commit = func(_ context.Context, change store.ChangeSet, options store.CommitOptions) (store.CommitReceipt, error) {
				sourceReceipt = migrationCommitOutcomeReceipt(t, change, options, fixture.apply.Proof)
				if len(sourceReceipt.ChangedFiles) == 0 || len(sourceReceipt.ChangedRefs) == 0 {
					t.Fatalf("clone fixture needs changed files and refs: %#v", sourceReceipt)
				}
				sourceSnapshot = sourceReceipt.Clone()
				if !committedFailure {
					return sourceReceipt, nil
				}
				constructed := store.NewCommittedError(sourceReceipt, cause)
				if !errors.As(constructed, &committedErr) {
					t.Fatalf("NewCommittedError() = %T, want *store.CommittedError", constructed)
				}
				committedSnapshot = committedErr.Receipt()
				return sourceReceipt, committedErr
			}

			// Act.
			gotReceipt, gotErr := fixture.planner.Apply(context.Background(), destination, fixture.apply)
			gotReceipt.ChangedFiles[0].Path = "caller-mutated.md"
			gotReceipt.ChangedRefs[0].Fragment = "caller-mutated"

			// Assert.
			if !reflect.DeepEqual(sourceReceipt, sourceSnapshot) {
				t.Fatalf("caller mutation changed scripted source receipt: got %#v, want %#v", sourceReceipt, sourceSnapshot)
			}
			if !committedFailure {
				if gotErr != nil {
					t.Fatalf("Apply() error = %v, want nil", gotErr)
				}
				return
			}
			if gotErr != committedErr || !reflect.DeepEqual(committedErr.Receipt(), committedSnapshot) {
				t.Fatalf("Apply() error = %v, want exact %v with receipt %#v", gotErr, committedErr, committedSnapshot)
			}
			firstGetter := committedErr.Receipt()
			firstGetter.ChangedFiles[0].Path = "getter-mutated.md"
			firstGetter.ChangedRefs[0].Fragment = "getter-mutated"
			if secondGetter := committedErr.Receipt(); !reflect.DeepEqual(secondGetter, committedSnapshot) {
				t.Fatalf("repeat Receipt() = %#v after caller mutation, want %#v", secondGetter, committedSnapshot)
			}
		})
	}
}

func TestMigrationPlannerApplyOrdinaryAndPrecommitFailuresReturnZero(t *testing.T) {
	t.Run("ordinary commit error", func(t *testing.T) {
		// Arrange.
		fixture := newMigrationCommitOutcomeFixture(t)
		ordinary := errors.New("ordinary commit error identity")
		destination := &scriptedMigrationStore{snapshot: fixture.snapshot}
		destination.commit = func(_ context.Context, change store.ChangeSet, options store.CommitOptions) (store.CommitReceipt, error) {
			return migrationCommitOutcomeReceipt(t, change, options, fixture.apply.Proof), ordinary
		}

		// Act.
		gotReceipt, gotErr := fixture.planner.Apply(context.Background(), destination, fixture.apply)

		// Assert.
		if !reflect.DeepEqual(gotReceipt, store.CommitReceipt{}) || gotErr != ordinary || destination.commitCalls != 1 {
			t.Fatalf("Apply() = (%#v, %v), want zero and exact %v", gotReceipt, gotErr, ordinary)
		}
	})

	t.Run("invalid plan digest", func(t *testing.T) {
		// Arrange.
		fixture := newMigrationCommitOutcomeFixture(t)
		fixture.apply.PlanDigest = "not-a-plan-digest"
		destination := &scriptedMigrationStore{}

		// Act.
		gotReceipt, gotErr := fixture.planner.Apply(context.Background(), destination, fixture.apply)

		// Assert.
		if !reflect.DeepEqual(gotReceipt, store.CommitReceipt{}) || gotErr == nil ||
			destination.snapshotCalls != 0 || destination.previewCalls != 0 || destination.commitCalls != 0 {
			t.Fatalf("Apply() = (%#v, %v), calls=%d/%d/%d; want precommit zero",
				gotReceipt, gotErr, destination.snapshotCalls, destination.previewCalls, destination.commitCalls)
		}
	})
}

type postCommitValueKey struct{}

type postCommitContext struct {
	context.Context
	err error
}

func (c *postCommitContext) Err() error { return c.err }

func TestMigrationPostCommitContext_PreservesValuesWithoutCancellation(t *testing.T) {
	// Arrange.
	resolution := migrationDigestResolutionFixture()
	parent := context.WithValue(context.Background(), postCommitValueKey{}, "retained")
	parent = context.WithValue(parent, migrationResolutionContextKey{}, resolution)
	canceled := &postCommitContext{Context: parent, err: context.DeadlineExceeded}

	// Act.
	got := migrationPostCommitContext(canceled)
	gotResolution, resolutionErr := frozenMigrationResolution(got)

	// Assert.
	if got.Err() != nil || got.Done() != nil {
		t.Fatalf("post-commit context cancellation leaked: err=%v done=%v", got.Err(), got.Done())
	}
	if got.Value(postCommitValueKey{}) != "retained" || resolutionErr != nil ||
		!reflect.DeepEqual(gotResolution, resolution) {
		t.Fatalf("post-commit values = %#v / %#v, %v", got.Value(postCommitValueKey{}), gotResolution, resolutionErr)
	}
}

func TestMigrationPlannerApplyPostCommitCancellationKeepsAuthenticatedOutcome(t *testing.T) {
	for _, stale := range []bool{false, true} {
		for _, callerErr := range []error{context.Canceled, context.DeadlineExceeded} {
			for _, committedFailure := range []bool{false, true} {
				name := fmt.Sprintf("stale=%t/%v/committed=%t", stale, callerErr, committedFailure)
				t.Run(name, func(t *testing.T) {
					// Arrange.
					fixture := newMigrationCommitOutcomeFixture(t)
					if stale {
						fixture.snapshot.revision = store.Revision("sha256:" + strings.Repeat("f", 64))
					}
					ctx := &postCommitContext{Context: context.WithValue(
						context.Background(), postCommitValueKey{}, "durable-value",
					)}
					cause := errors.New("post-commit exact cause")
					var wantReceipt store.CommitReceipt
					var wantErr error
					destination := &scriptedMigrationStore{snapshot: fixture.snapshot}
					destination.commit = func(commitCtx context.Context, change store.ChangeSet, options store.CommitOptions) (store.CommitReceipt, error) {
						if commitCtx.Value(postCommitValueKey{}) != "durable-value" {
							t.Fatal("Commit lost caller context value")
						}
						wantReceipt = migrationCommitOutcomeReceipt(t, change, options, fixture.apply.Proof)
						ctx.err = callerErr
						if committedFailure {
							wantErr = store.NewCommittedError(wantReceipt, cause)
						}
						return wantReceipt.Clone(), wantErr
					}

					// Act.
					gotReceipt, gotErr := fixture.planner.Apply(ctx, destination, fixture.apply)

					// Assert.
					if !reflect.DeepEqual(gotReceipt, wantReceipt) || gotErr != wantErr || destination.commitCalls != 1 {
						t.Fatalf("Apply() = (%#v, %v), want (%#v, exact %v); commits=%d",
							gotReceipt, gotErr, wantReceipt, wantErr, destination.commitCalls)
					}
					if committedFailure && (!errors.Is(gotErr, cause) || !errors.As(gotErr, new(*store.CommittedError))) {
						t.Fatalf("committed error identity/cause lost: %v", gotErr)
					}
				})
			}
		}
	}
}

func largeMigrationReceiptFixture(t *testing.T, size int) (store.ChangeSet, store.CommitOptions, MigrationPlanProof, store.CommitReceipt) {
	t.Helper()
	fixture := newMigrationCommitOutcomeFixture(t)
	change, err := migrationChangeSet(fixture.apply.Request, fixture.apply.Proof.BaseRevision)
	if err != nil {
		t.Fatal(err)
	}
	options, err := migrationApplyCommitOptionsContext(context.Background(), fixture.apply)
	if err != nil {
		t.Fatal(err)
	}
	proof := fixture.apply.Proof.Clone()
	proof.ChangedFiles = make([]store.FileChange, size)
	proof.ChangedRefs = make([]bundle.RelationRef, size)
	for index := range size {
		path := fmt.Sprintf("generated/%06d.md", index)
		id, parseErr := bundle.ParseConceptID(fmt.Sprintf("generated/%06d", index))
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		proof.ChangedFiles[index] = store.FileChange{Kind: store.FileWrite, Path: path}
		proof.ChangedRefs[index] = bundle.RelationRef{ID: id, Fragment: "result"}
	}
	receipt := migrationCommitOutcomeReceipt(t, change, options, proof)
	return change, options, proof, receipt
}

func TestMigrationCommitOutcome_LargeProofRelativeBoundsAndMismatchPositions(t *testing.T) {
	const size = 2048
	change, options, proof, matching := largeMigrationReceiptFixture(t, size)
	callerCtx := &postCommitContext{
		Context: context.WithValue(context.Background(), postCommitValueKey{}, "large-receipt"),
		err:     context.Canceled,
	}

	t.Run("matching committed receipt and clone isolation", func(t *testing.T) {
		// Arrange.
		cause := errors.New("large committed cause")
		committedErr := store.NewCommittedError(matching, cause)
		snapshot := matching.Clone()

		// Act.
		got, gotErr := migrationCommitOutcome(callerCtx, change, options, proof, matching, committedErr)
		got.ChangedFiles[size/2].Path = "caller-mutated.md"
		got.ChangedRefs[size/2].Fragment = "caller-mutated"

		// Assert.
		if gotErr != committedErr || !errors.Is(gotErr, cause) || !reflect.DeepEqual(matching, snapshot) {
			t.Fatalf("migrationCommitOutcome() error=%v, matching mutated=%t", gotErr, !reflect.DeepEqual(matching, snapshot))
		}
	})

	positions := []int{0, size / 2, size - 1}
	for _, index := range positions {
		for _, field := range []string{"file", "ref"} {
			t.Run(fmt.Sprintf("%s/%d", field, index), func(t *testing.T) {
				// Arrange.
				returned := matching.Clone()
				if field == "file" {
					returned.ChangedFiles[index].Path += "x"
				} else {
					returned.ChangedRefs[index].Fragment += "x"
				}
				cause := errors.New("mismatch exact cause")
				committedErr := store.NewCommittedError(matching, cause)

				// Act.
				got, gotErr := migrationCommitOutcome(callerCtx, change, options, proof, returned, committedErr)

				// Assert.
				if !reflect.DeepEqual(got, store.CommitReceipt{}) || !errors.Is(gotErr, ErrMigrationPlanMismatch) ||
					!errors.Is(gotErr, committedErr) || !errors.Is(gotErr, cause) {
					t.Fatalf("migrationCommitOutcome() = (%#v, %v)", got, gotErr)
				}
			})
		}
	}

	for _, test := range []struct {
		name   string
		mutate func(*store.CommitReceipt)
	}{
		{name: "cardinality", mutate: func(value *store.CommitReceipt) {
			value.ChangedFiles = append(value.ChangedFiles, store.FileChange{Kind: store.FileWrite, Path: "z.md"})
		}},
		{name: "scalar", mutate: func(value *store.CommitReceipt) { value.ChangeSetID = "foreign" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			returned := matching.Clone()
			test.mutate(&returned)
			cause := errors.New("bound mismatch cause")
			committedErr := store.NewCommittedError(matching, cause)

			// Act.
			got, gotErr := migrationCommitOutcome(callerCtx, change, options, proof, returned, committedErr)

			// Assert.
			if !reflect.DeepEqual(got, store.CommitReceipt{}) || !errors.Is(gotErr, ErrMigrationPlanMismatch) ||
				!errors.Is(gotErr, committedErr) || !errors.Is(gotErr, cause) {
				t.Fatalf("migrationCommitOutcome() = (%#v, %v)", got, gotErr)
			}
		})
	}
}
