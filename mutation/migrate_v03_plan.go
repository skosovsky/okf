package mutation

import (
	"context"
	"errors"
	"fmt"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
)

// MigrationV03Request describes caller policy for 0.2-to-0.3 migration.
type MigrationV03Request struct {
	ID        store.ChangeSetID
	Actor     store.Actor
	AssumeV02 bool
	Choices   []store.MigrationV03PathChoice
}

type MigrationV03Preview struct {
	Preview    store.Preview `json:"preview"`
	PlanDigest string        `json:"plan_digest"`
}

type MigrationV03ApplyRequest struct {
	Request      MigrationV03Request
	BaseRevision store.Revision
	PlanDigest   string
	Options      store.CommitOptions
}

// PreviewV03Migration stages the requested migration without publishing it.
func PreviewV03Migration(ctx context.Context, source bundle.Source, request MigrationV03Request) (MigrationV03Preview, error) {
	if source == nil {
		return MigrationV03Preview{}, errors.New("nil migration source")
	}
	base, err := revision(ctx, source)
	if err != nil {
		return MigrationV03Preview{}, err
	}
	change, err := migrationV03Change(request, base)
	if err != nil {
		return MigrationV03Preview{}, err
	}
	result, err := Plan(ctx, source, change)
	if err != nil {
		return MigrationV03Preview{Preview: result.Preview}, err
	}
	digest, err := planDigestContext(ctx, change, result.Preview)
	if err != nil {
		return MigrationV03Preview{}, err
	}
	return MigrationV03Preview{Preview: result.Preview, PlanDigest: digest}, nil
}

// ApplyV03Migration recomputes the proof against a pinned snapshot and
// publishes through Store's revision CAS and durable idempotency mechanism.
func ApplyV03Migration(ctx context.Context, destination store.Store, request MigrationV03ApplyRequest) (store.CommitReceipt, error) {
	if destination == nil {
		return store.CommitReceipt{}, errors.New("nil migration store")
	}
	if err := ValidatePlanDigest(request.PlanDigest); err != nil {
		return store.CommitReceipt{}, err
	}
	change, err := migrationV03Change(request.Request, request.BaseRevision)
	if err != nil {
		return store.CommitReceipt{}, err
	}
	key, err := PlanDigestIdempotencyKey("migration-v03:v1", request.PlanDigest, request.Options.IdempotencyKey)
	if err != nil {
		return store.CommitReceipt{}, err
	}
	options := request.Options
	options.IdempotencyKey = key
	snapshot, err := destination.Snapshot(ctx)
	if err != nil {
		return store.CommitReceipt{}, err
	}
	if snapshot.Revision() == request.BaseRevision {
		result, err := Plan(ctx, snapshot, change)
		if err != nil {
			return store.CommitReceipt{}, err
		}
		digest, err := planDigestContext(ctx, change, result.Preview)
		if err != nil {
			return store.CommitReceipt{}, err
		}
		if digest != request.PlanDigest {
			return store.CommitReceipt{}, ErrPlanDigestMismatch
		}
	}
	// With a stale base the store may only return an existing matching
	// receipt. A request without a receipt fails the revision check.
	receipt, commitErr := destination.Commit(ctx, change, options)
	var committed *store.CommittedError
	if commitErr != nil && !errors.As(commitErr, &committed) {
		return store.CommitReceipt{}, commitErr
	}
	if err := store.ValidateCommitReceipt(receipt); err != nil {
		return store.CommitReceipt{}, errors.Join(commitErr, fmt.Errorf("invalid migration receipt: %w", err))
	}
	requestDigest, err := change.RequestDigest()
	if err != nil {
		return store.CommitReceipt{}, errors.Join(commitErr, err)
	}
	if receipt.BaseRevision != request.BaseRevision || receipt.ChangeSetID != change.ID ||
		receipt.IdempotencyKey != key || receipt.RequestDigest != requestDigest {
		return store.CommitReceipt{}, errors.Join(commitErr, ErrPlanDigestMismatch)
	}
	return receipt, commitErr
}

func migrationV03Change(request MigrationV03Request, base store.Revision) (store.ChangeSet, error) {
	op, err := store.NewMigrateV02ToV03(request.AssumeV02, request.Choices)
	if err != nil {
		return store.ChangeSet{}, err
	}
	change := store.ChangeSet{Version: store.ChangeSetFormatVersion, ID: request.ID, Actor: request.Actor, BaseRevision: base, Operations: []store.Operation{op}}
	return change, change.Validate()
}
