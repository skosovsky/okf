package store

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/skosovsky/okf/bundle"
)

var (
	ErrInvalidRevision = errors.New("invalid revision")
	ErrInvalidManifest = errors.New("invalid manifest")
	// ErrInvalidHashAlgorithm means a configured hash implementation cannot
	// safely construct a usable hash.Hash instance.
	ErrInvalidHashAlgorithm = errors.New("invalid hash algorithm")
	ErrInvalidChangeSet     = errors.New("invalid change set")
	ErrConflict             = errors.New("store conflict")
	ErrPreconditionFailed   = errors.New("precondition failed")
	ErrIdempotencyConflict  = errors.New("idempotency conflict")
	// ErrStorageCorrupt means persisted backend metadata failed its integrity
	// contract. Callers must not treat it as an absent idempotency receipt.
	ErrStorageCorrupt = errors.New("storage corruption")
)

// CommittedError reports a failure observed after a commit became durable.
// Callers must treat Receipt as the successful commit result and must not
// retry the request as a new transaction. The receipt passed to
// NewCommittedError must satisfy ValidateCommitReceipt.
type CommittedError struct {
	receipt CommitReceipt
	cause   error
}

// NewCommittedError preserves a validated durable receipt and the exact
// post-commit failure. Invalid receipts and nil causes are programmer errors,
// not committed outcomes, and therefore return a plain validation error.
func NewCommittedError(receipt CommitReceipt, cause error) error {
	if cause == nil {
		return errors.New("store committed error: nil cause")
	}
	if err := ValidateCommitReceipt(receipt); err != nil {
		return fmt.Errorf("store committed error: invalid receipt: %w", err)
	}
	return &CommittedError{receipt: receipt.Clone(), cause: cause}
}

func (e *CommittedError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.cause == nil {
		return "store commit completed durably"
	}
	return fmt.Sprintf("store commit completed durably: %v", e.cause)
}

// Unwrap exposes the exact failure observed after the durable boundary.
func (e *CommittedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// Receipt returns a copy safe for caller mutation.
func (e *CommittedError) Receipt() CommitReceipt {
	if e == nil {
		return CommitReceipt{}
	}
	return e.receipt.Clone()
}

// Diagnostic is a stable machine-readable validation finding. Code must remain
// stable across compatible releases; Message is for people and may improve.
type Diagnostic struct {
	Kind                DiagnosticKind
	Severity            DiagnosticSeverity
	Code, File, Message string
	// RelationType and RawTarget retain the source-level relation context for
	// relation diagnostics. They are empty for diagnostics from other producers.
	RelationType, RawTarget string
	Refs                    []bundle.RelationRef
}

// MarshalJSON keeps relation identities usable on Preview's JSON surface.
// ConceptID deliberately encapsulates its segments, so default struct JSON
// would otherwise erase every diagnostic reference.
func (d Diagnostic) MarshalJSON() ([]byte, error) {
	type wire struct {
		Kind         DiagnosticKind     `json:"Kind"`
		Severity     DiagnosticSeverity `json:"Severity"`
		Code         string             `json:"Code"`
		File         string             `json:"File"`
		Message      string             `json:"Message"`
		RelationType string             `json:"RelationType,omitempty"`
		RawTarget    string             `json:"RawTarget,omitempty"`
		Refs         []string           `json:"Refs"`
	}
	refs := make([]string, len(d.Refs))
	for i, ref := range d.Refs {
		refs[i] = ref.String()
	}
	return json.Marshal(wire{Kind: d.Kind, Severity: d.Severity, Code: d.Code, File: d.File, Message: d.Message, RelationType: d.RelationType, RawTarget: d.RawTarget, Refs: refs})
}

// InvalidChangeSet describes malformed input or an unplannable ChangeSet.
type InvalidChangeSet struct {
	Code        string
	Diagnostics []Diagnostic
	Cause       error
}

func (e *InvalidChangeSet) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s: %v", e.Code, e.Cause)
}
func (e *InvalidChangeSet) Unwrap() error {
	if e == nil || e.Cause == nil {
		return ErrInvalidChangeSet
	}
	return errors.Join(ErrInvalidChangeSet, e.Cause)
}

// Conflict indicates that the base revision changed before Commit could apply.
type Conflict struct {
	Expected, Actual Revision
	// ChangedRefs is the sorted semantic ref set that may have changed between
	// the expected and actual snapshots. It is nil only when that comparison is
	// unavailable to the implementation.
	ChangedRefs []bundle.RelationRef
	Retryable   bool
}

func (e *Conflict) Error() string {
	return fmt.Sprintf("%s: expected %s, got %s", ErrConflict, e.Expected, e.Actual)
}
func (e *Conflict) Is(target error) bool { return target == ErrConflict }

// PreconditionFailure identifies the failed condition and affected refs.
type PreconditionFailure struct {
	Precondition Precondition
	AffectedRefs []bundle.RelationRef
}

func (e *PreconditionFailure) Error() string        { return ErrPreconditionFailed.Error() }
func (e *PreconditionFailure) Is(target error) bool { return target == ErrPreconditionFailed }

// IdempotencyConflict indicates reuse of a key for a different canonical request.
type IdempotencyConflict struct {
	Key                           IdempotencyKey
	PreviousDigest, RequestDigest string
}

func (e *IdempotencyConflict) Error() string {
	return fmt.Sprintf("%s: key %q", ErrIdempotencyConflict, e.Key)
}
func (e *IdempotencyConflict) Is(target error) bool { return target == ErrIdempotencyConflict }
