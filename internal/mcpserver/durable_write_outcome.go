package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/skosovsky/okf/mutation"
	"github.com/skosovsky/okf/store"
	storefs "github.com/skosovsky/okf/store/fs"
)

var errMCPWriteReceiptInvalid = errors.New("MCP write receipt integrity failure")

// mcpPostCommitContext retains request-scoped values while preventing caller
// cancellation observed after the durable boundary from erasing a success.
func mcpPostCommitContext(ctx context.Context) context.Context {
	return context.WithoutCancel(ctx)
}

func classifyMCPPatchCommitOutcome(
	change store.ChangeSet,
	options store.CommitOptions,
	receipt store.CommitReceipt,
	commitErr error,
) (store.CommitReceipt, error) {
	var committed *store.CommittedError
	if commitErr != nil && !errors.As(commitErr, &committed) {
		return store.CommitReceipt{}, commitErr
	}
	if err := store.ValidateCommitReceipt(receipt); err != nil {
		return store.CommitReceipt{}, joinDurableOutcomeError(commitErr,
			fmt.Errorf("patch commit returned invalid receipt: %w", err))
	}
	if committed != nil {
		inner := committed.Receipt()
		if err := store.ValidateCommitReceipt(inner); err != nil {
			return store.CommitReceipt{}, errors.Join(commitErr,
				fmt.Errorf("patch committed error contains invalid receipt: %w", err))
		}
		if !reflect.DeepEqual(receipt, inner) {
			return store.CommitReceipt{}, errors.Join(commitErr,
				fmt.Errorf("%w: patch returned receipt does not match committed error receipt", mutation.ErrMigrationPlanMismatch))
		}
	}
	digest, err := change.RequestDigest()
	if err != nil {
		return store.CommitReceipt{}, joinDurableOutcomeError(commitErr,
			fmt.Errorf("%w: derive patch receipt identity: %v", mutation.ErrMigrationPlanMismatch, err))
	}
	if receipt.ChangeSetID != change.ID ||
		receipt.IdempotencyKey != options.IdempotencyKey ||
		receipt.RequestDigest != digest ||
		receipt.BaseRevision != change.BaseRevision {
		return store.CommitReceipt{}, joinDurableOutcomeError(commitErr,
			fmt.Errorf("%w: patch commit receipt does not match authorized request", mutation.ErrMigrationPlanMismatch))
	}
	return receipt.Clone(), nil
}

func classifyMCPWriteCommitOutcome(
	request storefs.ReplaceConceptRequest,
	options store.CommitOptions,
	result storefs.ReplaceConceptResult,
	commitErr error,
) (storefs.ReplaceConceptResult, error) {
	var committed *store.CommittedError
	if commitErr != nil && !errors.As(commitErr, &committed) {
		return result, commitErr
	}
	if err := store.ValidateCommitReceipt(result.Receipt); err != nil {
		return storefs.ReplaceConceptResult{}, joinDurableOutcomeError(commitErr,
			fmt.Errorf("%w: write concept returned invalid receipt: %w", errMCPWriteReceiptInvalid, err))
	}
	if committed != nil {
		inner := committed.Receipt()
		if err := store.ValidateCommitReceipt(inner); err != nil {
			return storefs.ReplaceConceptResult{}, errors.Join(commitErr,
				fmt.Errorf("%w: write concept committed error contains invalid receipt: %w", errMCPWriteReceiptInvalid, err))
		}
		if !reflect.DeepEqual(result.Receipt, inner) {
			return storefs.ReplaceConceptResult{}, errors.Join(commitErr,
				fmt.Errorf("%w: write result receipt does not match committed error receipt", mutation.ErrMigrationPlanMismatch))
		}
	}
	if result.Receipt.ChangeSetID != request.ChangeSetID ||
		result.Receipt.IdempotencyKey != options.IdempotencyKey {
		return storefs.ReplaceConceptResult{}, joinDurableOutcomeError(commitErr,
			fmt.Errorf("%w: write receipt does not match authorized request", mutation.ErrMigrationPlanMismatch))
	}
	result.Receipt = result.Receipt.Clone()
	return result, nil
}

func joinDurableOutcomeError(original, classified error) error {
	if original == nil {
		return classified
	}
	return errors.Join(original, classified)
}
