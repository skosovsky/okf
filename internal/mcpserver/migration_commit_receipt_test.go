package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/mutation"
	"github.com/skosovsky/okf/store"
)

type migrationReceiptApplyPlanner struct {
	apply func(context.Context, store.Store, mutation.MigrationApplyRequest) (store.CommitReceipt, error)
}

func (planner migrationReceiptApplyPlanner) Apply(
	ctx context.Context,
	destination store.Store,
	request mutation.MigrationApplyRequest,
) (store.CommitReceipt, error) {
	return planner.apply(ctx, destination, request)
}

type migrationReceiptStore struct{}

func (migrationReceiptStore) Snapshot(context.Context) (store.Snapshot, error) {
	return nil, errors.New("unexpected Snapshot")
}

func (migrationReceiptStore) Preview(context.Context, store.ChangeSet) (store.Preview, error) {
	return store.Preview{}, errors.New("unexpected Preview")
}

func (migrationReceiptStore) Commit(
	context.Context,
	store.ChangeSet,
	store.CommitOptions,
) (store.CommitReceipt, error) {
	return store.CommitReceipt{}, errors.New("unexpected Commit")
}

type migrationReceiptFixture struct {
	domain     mutation.MigrationRequest
	source     mutation.MigrationSourceResolution
	proof      mutation.MigrationPlanProof
	planDigest string
	receipt    store.CommitReceipt
}

func newMigrationReceiptFixture(t *testing.T, noop bool) migrationReceiptFixture {
	t.Helper()
	base := store.Revision("sha256:" + strings.Repeat("1", 64))
	result := store.Revision("sha256:" + strings.Repeat("2", 64))
	changedFiles := []store.FileChange{{Kind: store.FileWrite, Path: "alpha.md"}}
	ref, err := bundle.ParseRelationRef("alpha")
	if err != nil {
		t.Fatalf("parse fixture relation ref: %v", err)
	}
	changedRefs := []bundle.RelationRef{ref}
	if noop {
		result = base
		changedFiles = []store.FileChange{}
	}
	requestDigest := "sha256:" + strings.Repeat("3", 64)
	planDigest := "sha256:" + strings.Repeat("4", 64)
	idempotencyKey, err := mutation.PlanDigestIdempotencyKey(
		"migration:v0.1-to-v0.2",
		planDigest,
		"",
	)
	if err != nil {
		t.Fatalf("derive fixture idempotency key: %v", err)
	}
	proof := mutation.MigrationPlanProof{
		FormatVersion: mutation.MigrationPlanProofFormatVersion,
		RequestDigest: requestDigest, ResolutionDigest: "sha256:" + strings.Repeat("5", 64),
		BaseRevision: base, ResultRevision: result,
		Reads: []store.Read{}, Writes: []mutation.MigrationPlanWrite{}, Deletes: []string{},
		Renames: []store.Rename{}, AffectedRefs: []bundle.RelationRef{},
		ReverseImpact: []bundle.RelationRef{}, ChangedFiles: changedFiles, ChangedRefs: changedRefs,
	}
	domain := mutation.MigrationRequest{
		ID: "mcp-migration", Actor: migrationTransactionActor,
		FromVersion: mutation.MigrationVersionV01, ToVersion: mutation.MigrationVersionV02,
	}
	source := mutation.MigrationSourceResolution{
		RequestedSelector:  mutation.MigrationSelectorAuto,
		DeclarationPresent: true, DeclarationValid: true,
		DeclarationRaw: mutation.MigrationVersionV01, DeclaredVersion: mutation.MigrationVersionV01,
		ResolvedSource:   mutation.MigrationVersionV01,
		ResolutionSource: mutation.MigrationResolutionDeclared,
		FromVersion:      mutation.MigrationVersionV01, ToVersion: mutation.MigrationVersionV02,
		Transition: mutation.MigrationTransitionV01ToV02,
		Candidates: []mutation.MigrationLegacyCandidate{}, Blockers: []mutation.MigrationBlocker{},
	}
	receipt := store.CommitReceipt{
		FormatVersion: store.CommitReceiptFormatVersion,
		ChangeSetID:   domain.ID, IdempotencyKey: idempotencyKey, RequestDigest: requestDigest,
		BaseRevision: base, ResultRevision: result,
		CommitTime:   time.Date(2026, time.August, 12, 12, 0, 0, 0, time.UTC),
		ChangedRefs:  append([]bundle.RelationRef{}, changedRefs...),
		ChangedFiles: append([]store.FileChange{}, changedFiles...),
	}
	if err := store.ValidateCommitReceipt(receipt); err != nil {
		t.Fatalf("fixture receipt invalid: %v", err)
	}
	return migrationReceiptFixture{
		domain: domain, source: source, proof: proof, planDigest: planDigest, receipt: receipt,
	}
}

func TestApplyMigrationPlanWithStoreClassifiesReceiptOutcomes(t *testing.T) {
	postCommitIO := errors.New("post-commit IO identity")
	ordinary := errors.New("ordinary apply identity")
	tests := []struct {
		name       string
		noop       bool
		outcome    func(store.CommitReceipt) (store.CommitReceipt, error)
		wantStatus string
		wantCode   string
		wantRetry  bool
		cancel     bool
	}{
		{name: "changed receipt and nil", wantStatus: "applied", outcome: func(receipt store.CommitReceipt) (store.CommitReceipt, error) { return receipt, nil }},
		{name: "noop receipt and nil", noop: true, wantStatus: "noop", outcome: func(receipt store.CommitReceipt) (store.CommitReceipt, error) { return receipt, nil }},
		{name: "matching committed IO", wantStatus: "applied", outcome: func(receipt store.CommitReceipt) (store.CommitReceipt, error) {
			return receipt, store.NewCommittedError(receipt, postCommitIO)
		}},
		{name: "matching committed cancellation", wantStatus: "applied", outcome: func(receipt store.CommitReceipt) (store.CommitReceipt, error) {
			return receipt, store.NewCommittedError(receipt, context.Canceled)
		}},
		{name: "post durable context cancellation", wantStatus: "applied", cancel: true, outcome: func(receipt store.CommitReceipt) (store.CommitReceipt, error) { return receipt, nil }},
		{name: "ordinary cancellation", wantCode: "operation_cancelled", wantRetry: true, outcome: func(store.CommitReceipt) (store.CommitReceipt, error) {
			return store.CommitReceipt{}, context.Canceled
		}},
		{name: "ordinary conflict", wantCode: "revision_conflict", wantRetry: true, outcome: func(store.CommitReceipt) (store.CommitReceipt, error) {
			return store.CommitReceipt{}, &store.Conflict{Retryable: true}
		}},
		{name: "ordinary error", wantCode: "invalid_request", outcome: func(receipt store.CommitReceipt) (store.CommitReceipt, error) {
			return receipt, ordinary
		}},
		{name: "invalid returned receipt", wantCode: "operation_rejected", outcome: func(store.CommitReceipt) (store.CommitReceipt, error) {
			return store.CommitReceipt{}, nil
		}},
		{name: "invalid committed receipt", wantCode: "operation_rejected", outcome: func(receipt store.CommitReceipt) (store.CommitReceipt, error) {
			return receipt, &store.CommittedError{}
		}},
		{name: "valid receipt mismatch", wantCode: "plan_mismatch", outcome: func(receipt store.CommitReceipt) (store.CommitReceipt, error) {
			other := receipt.Clone()
			other.CommitTime = other.CommitTime.Add(time.Second).UTC()
			return receipt, store.NewCommittedError(other, postCommitIO)
		}},
		{name: "valid receipt mismatch with cancellation", wantCode: "plan_mismatch", outcome: func(receipt store.CommitReceipt) (store.CommitReceipt, error) {
			other := receipt.Clone()
			other.CommitTime = other.CommitTime.Add(time.Second).UTC()
			return receipt, store.NewCommittedError(other, context.Canceled)
		}},
		{name: "valid receipt mismatch with conflict", wantCode: "plan_mismatch", outcome: func(receipt store.CommitReceipt) (store.CommitReceipt, error) {
			other := receipt.Clone()
			other.CommitTime = other.CommitTime.Add(time.Second).UTC()
			return receipt, store.NewCommittedError(other, &store.Conflict{Retryable: true})
		}},
		{name: "invalid committed receipt with cancellation", wantCode: "operation_rejected", outcome: func(receipt store.CommitReceipt) (store.CommitReceipt, error) {
			return receipt, errors.Join(&store.CommittedError{}, context.Canceled)
		}},
		{name: "invalid committed receipt with conflict", wantCode: "operation_rejected", outcome: func(receipt store.CommitReceipt) (store.CommitReceipt, error) {
			return receipt, errors.Join(&store.CommittedError{}, &store.Conflict{Retryable: true})
		}},
		{name: "foreign receipt", wantCode: "plan_mismatch", outcome: func(receipt store.CommitReceipt) (store.CommitReceipt, error) {
			receipt.ChangeSetID = "foreign-migration"
			return receipt, nil
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			fixture := newMigrationReceiptFixture(t, test.noop)
			ctx := t.Context()
			var cancel context.CancelFunc
			if test.cancel {
				ctx, cancel = context.WithCancel(ctx)
			}
			planner := migrationReceiptApplyPlanner{apply: func(
				_ context.Context,
				_ store.Store,
				request mutation.MigrationApplyRequest,
			) (store.CommitReceipt, error) {
				if request.Request.ID != fixture.domain.ID || request.PlanDigest != fixture.planDigest {
					t.Fatalf("apply request = %#v", request)
				}
				if cancel != nil {
					cancel()
				}
				return test.outcome(fixture.receipt.Clone())
			}}

			// Act.
			result, err := applyMigrationPlanWithStore(
				ctx, planner, migrationReceiptStore{}, fixture.domain,
				fixture.source, fixture.proof, fixture.planDigest,
			)

			// Assert.
			if err != nil || result == nil {
				t.Fatalf("result/error = %#v/%v", result, err)
			}
			if test.wantCode != "" {
				assertSchemaErrorEnvelope(t, result, test.wantCode)
				envelope := result.StructuredContent.(errorEnvelope)
				if envelope.Retryable != test.wantRetry {
					t.Fatalf("retryable = %t, want %t; envelope=%#v", envelope.Retryable, test.wantRetry, envelope)
				}
				return
			}
			if result.IsError {
				t.Fatalf("success returned error: %s", resultText(t, result))
			}
			response, ok := result.StructuredContent.(migrationApplyResponse)
			if !ok {
				t.Fatalf("structured content type = %T", result.StructuredContent)
			}
			wantPaths := changedReceiptPaths(fixture.receipt)
			if response.Status != test.wantStatus || response.BaseRevision != fixture.receipt.BaseRevision.String() ||
				response.ResultRevision != fixture.receipt.ResultRevision.String() || response.PlanDigest != fixture.planDigest ||
				!reflect.DeepEqual(response.Source, migrationSourceDTOFromDomain(fixture.source)) ||
				!reflect.DeepEqual(response.ChangedPaths, wantPaths) || len(response.Diagnostics) != 0 {
				t.Fatalf("response = %#v, want status=%q paths=%#v", response, test.wantStatus, wantPaths)
			}
			wire, marshalErr := json.Marshal(result.StructuredContent)
			if marshalErr != nil || strings.Contains(string(wire), "receipt") {
				t.Fatalf("wire = %q/%v, receipt must remain internal", wire, marshalErr)
			}
			if got := enforceContractResult("apply_v02_migration", result); got != result {
				t.Fatalf("wrapped schema enforcement rejected direct result: %#v", got.StructuredContent)
			}
		})
	}
}

func TestClassifyMCPMigrationCommitOutcomeClonesAndAuthenticates(t *testing.T) {
	tests := []struct {
		name     string
		applyErr func(store.CommitReceipt) error
	}{
		{name: "nil error", applyErr: func(store.CommitReceipt) error { return nil }},
		{name: "committed error", applyErr: func(receipt store.CommitReceipt) error {
			return store.NewCommittedError(receipt, errors.New("post-commit identity"))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			fixture := newMigrationReceiptFixture(t, false)
			request := mutation.MigrationApplyRequest{
				Request: fixture.domain, Resolution: fixture.source,
				Proof: fixture.proof, PlanDigest: fixture.planDigest,
			}
			source := fixture.receipt.Clone()
			sourceSnapshot := source.Clone()
			applyErr := test.applyErr(source)
			var committed *store.CommittedError
			var committedSnapshot store.CommitReceipt
			if errors.As(applyErr, &committed) {
				committedSnapshot = committed.Receipt()
			}

			// Act.
			got, gotErr := classifyMCPMigrationCommitOutcome(request, source, applyErr)
			got.ChangedFiles[0].Path = "mutated.md"
			got.ChangedRefs[0].Fragment = "mutated"
			if committed != nil {
				getter := committed.Receipt()
				getter.ChangedFiles[0].Path = "getter-mutated.md"
				getter.ChangedRefs[0].Fragment = "getter-mutated"
			}

			// Assert.
			if gotErr != nil || !reflect.DeepEqual(source, sourceSnapshot) {
				t.Fatalf("error/source = %v/%#v, want nil/unchanged %#v", gotErr, source, sourceSnapshot)
			}
			if committed != nil && !reflect.DeepEqual(committed.Receipt(), committedSnapshot) {
				t.Fatalf("committed receipt changed = %#v, want %#v", committed.Receipt(), committedSnapshot)
			}
		})
	}
}

func TestClassifyMCPMigrationCommitOutcomePreservesRejectedErrorIdentity(t *testing.T) {
	ordinary := errors.New("ordinary exact identity")
	postCommit := errors.New("post-commit exact identity")
	tests := []struct {
		name        string
		outcome     func(store.CommitReceipt) (store.CommitReceipt, error)
		wantExact   error
		wantCause   error
		wantPlan    bool
		wantCorrupt bool
	}{
		{
			name: "ordinary exact identity",
			outcome: func(receipt store.CommitReceipt) (store.CommitReceipt, error) {
				return receipt, ordinary
			},
			wantExact: ordinary,
		},
		{
			name: "mismatch preserves committed wrapper and cause",
			outcome: func(receipt store.CommitReceipt) (store.CommitReceipt, error) {
				other := receipt.Clone()
				other.CommitTime = other.CommitTime.Add(time.Second).UTC()
				applyErr := store.NewCommittedError(other, postCommit)
				return receipt, applyErr
			},
			wantCause: postCommit, wantPlan: true,
		},
		{
			name: "invalid inner preserves committed wrapper",
			outcome: func(receipt store.CommitReceipt) (store.CommitReceipt, error) {
				return receipt, &store.CommittedError{}
			},
			wantCorrupt: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			fixture := newMigrationReceiptFixture(t, false)
			request := mutation.MigrationApplyRequest{
				Request: fixture.domain, Resolution: fixture.source,
				Proof: fixture.proof, PlanDigest: fixture.planDigest,
			}
			receipt, applyErr := test.outcome(fixture.receipt.Clone())

			// Act.
			got, gotErr := classifyMCPMigrationCommitOutcome(request, receipt, applyErr)

			// Assert.
			if !reflect.DeepEqual(got, store.CommitReceipt{}) {
				t.Fatalf("receipt = %#v, want zero", got)
			}
			if test.wantExact != nil && gotErr != test.wantExact {
				t.Fatalf("error identity = %p/%v, want %p/%v", gotErr, gotErr, test.wantExact, test.wantExact)
			}
			if test.wantExact == nil && !errors.Is(gotErr, applyErr) {
				t.Fatalf("error = %v, want original apply error %v", gotErr, applyErr)
			}
			if test.wantCause != nil && !errors.Is(gotErr, test.wantCause) {
				t.Fatalf("error = %v, want cause %v", gotErr, test.wantCause)
			}
			if errors.Is(gotErr, mutation.ErrMigrationPlanMismatch) != test.wantPlan ||
				errors.Is(gotErr, store.ErrStorageCorrupt) != test.wantCorrupt {
				t.Fatalf("error = %v, want plan/corrupt=%t/%t", gotErr, test.wantPlan, test.wantCorrupt)
			}
		})
	}
}

func TestValidateMCPMigrationReceiptUsesPlannerDerivedIdempotencyKey(t *testing.T) {
	for _, callerKey := range []store.IdempotencyKey{"", "caller-supplied-key"} {
		callerKey := callerKey
		t.Run(string(callerKey), func(t *testing.T) {
			// Arrange.
			fixture := newMigrationReceiptFixture(t, false)
			request := mutation.MigrationApplyRequest{
				Request: fixture.domain, Resolution: fixture.source,
				Proof: fixture.proof, PlanDigest: fixture.planDigest,
				Options: store.CommitOptions{IdempotencyKey: callerKey},
			}
			derived, err := mutation.PlanDigestIdempotencyKey(
				"migration:v0.1-to-v0.2",
				fixture.planDigest,
				callerKey,
			)
			if err != nil {
				t.Fatalf("derive expected key: %v", err)
			}
			valid := fixture.receipt.Clone()
			valid.IdempotencyKey = derived
			wrongRaw := valid.Clone()
			wrongRaw.IdempotencyKey = callerKey
			wrongDerived := valid.Clone()
			wrongDerived.IdempotencyKey, err = mutation.PlanDigestIdempotencyKey(
				"migration:v0.1-to-v0.2",
				"sha256:"+strings.Repeat("9", 64),
				callerKey,
			)
			if err != nil {
				t.Fatalf("derive wrong key: %v", err)
			}

			// Act.
			validErr := validateMCPMigrationReceipt(request, valid)
			rawErr := validateMCPMigrationReceipt(request, wrongRaw)
			derivedErr := validateMCPMigrationReceipt(request, wrongDerived)

			// Assert.
			if validErr != nil || !errors.Is(rawErr, mutation.ErrMigrationPlanMismatch) ||
				!errors.Is(derivedErr, mutation.ErrMigrationPlanMismatch) {
				t.Fatalf("valid/raw/wrong-derived errors = %v/%v/%v", validErr, rawErr, derivedErr)
			}
		})
	}
}

func TestValidateMCPMigrationReceiptAcceptsStructuralProofAndCanonicalReceiptRefOrders(t *testing.T) {
	// Arrange.
	fixture := newMigrationReceiptFixture(t, false)
	fragment, err := bundle.ParseRelationRef("a#x")
	if err != nil {
		t.Fatal(err)
	}
	escaped, err := bundle.ParseRelationRef(`a\#x`)
	if err != nil {
		t.Fatal(err)
	}
	upper, err := bundle.ParseRelationRef("aZ")
	if err != nil {
		t.Fatal(err)
	}
	request := mutation.MigrationApplyRequest{
		Request: fixture.domain, Resolution: fixture.source,
		Proof: fixture.proof, PlanDigest: fixture.planDigest,
	}
	request.Proof.ChangedRefs = []bundle.RelationRef{fragment, escaped, upper}
	receipt := fixture.receipt.Clone()
	receipt.ChangedRefs = []bundle.RelationRef{fragment, upper, escaped}
	if err := store.ValidateCommitReceipt(receipt); err != nil {
		t.Fatalf("canonical receipt order invalid: %v", err)
	}
	extraRef, err := bundle.ParseRelationRef("z")
	if err != nil {
		t.Fatal(err)
	}
	mutatedID, err := bundle.ParseRelationRef("b#x")
	if err != nil {
		t.Fatal(err)
	}
	mutatedFragment, err := bundle.ParseRelationRef("a#y")
	if err != nil {
		t.Fatal(err)
	}
	invalid := map[string][]bundle.RelationRef{
		"missing":          {fragment, upper},
		"extra":            {fragment, upper, escaped, extraRef},
		"duplicate":        {fragment, fragment, escaped},
		"concept id field": {mutatedID, upper, escaped},
		"fragment field":   {mutatedFragment, upper, escaped},
	}

	// Act.
	validErr := validateMCPMigrationReceipt(request, receipt)

	// Assert.
	if validErr != nil {
		t.Fatalf("equivalent ref set rejected: %v", validErr)
	}
	for name, refs := range invalid {
		candidate := receipt.Clone()
		candidate.ChangedRefs = append([]bundle.RelationRef{}, refs...)
		if err := validateMCPMigrationReceipt(request, candidate); !errors.Is(err, mutation.ErrMigrationPlanMismatch) {
			t.Fatalf("%s error = %v, want plan mismatch", name, err)
		}
	}
}
