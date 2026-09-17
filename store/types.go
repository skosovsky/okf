package store

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/skosovsky/okf/bundle"
)

// ChangeSetID identifies a caller-created change set. Its zero value is invalid.
type ChangeSetID string

// Actor identifies the principal requesting a change. Its zero value is invalid.
type Actor string

// IdempotencyKey identifies one retryable commit request. Its zero value is invalid.
type IdempotencyKey string

// Validate reports whether an identifier is usable in a ChangeSet.
func (id ChangeSetID) Validate() error { return validateIdentity("change set id", string(id), 128) }

// Validate reports whether an actor is usable in a ChangeSet.
func (a Actor) Validate() error { return validateIdentity("actor", string(a), 256) }

// Validate reports whether a key is usable for idempotent Commit calls.
func (k IdempotencyKey) Validate() error { return validateIdentity("idempotency key", string(k), 256) }

func validateIdentity(kind, value string, maximum int) error {
	if value == "" || len(value) > maximum || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return fmt.Errorf("%w: invalid %s", ErrInvalidChangeSet, kind)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: invalid %s", ErrInvalidChangeSet, kind)
		}
	}
	return nil
}

// Snapshot is an immutable view of an OKF bundle at a revision. Returned
// concepts and concept-ID slices must be independent copies owned by the caller.
type Snapshot interface {
	bundle.Source
	Revision() Revision
	OpenConcept(bundle.ConceptID) (bundle.Concept, error)
	ListConcepts() ([]bundle.ConceptID, error)
}

// ManifestSource optionally exposes a defensive, immutable digest manifest.
// Consumers may use it to avoid rehashing unchanged captured content.
type ManifestSource interface {
	Manifest() Manifest
}

// ContextManifestSource is the request-path form of ManifestSource. Sources
// whose manifest retrieval or defensive materialization is O(N) implement it
// so cancellation can interrupt that work.
type ContextManifestSource interface {
	ManifestContext(context.Context) (Manifest, error)
}

// Store atomically previews and commits semantic changes. All methods honor
// context cancellation until their implementation's documented durable commit
// boundary. After that boundary, every failure is returned with the canonical
// nonzero receipt and a *CommittedError; cancellation is used as the cause only
// when no stronger post-boundary I/O failure exists. A matching idempotent retry
// returns the same receipt.
type Store interface {
	Snapshot(context.Context) (Snapshot, error)
	Preview(context.Context, ChangeSet) (Preview, error)
	Commit(context.Context, ChangeSet, CommitOptions) (CommitReceipt, error)
}

// ChangeSet is a versioned, declarative semantic change. BaseRevision, ID,
// Actor, operations, and preconditions are validated before planning. Slices
// are caller-owned and are never retained by Store implementations.
type ChangeSet struct {
	// Version is the ChangeSet serialization version. Only
	// ChangeSetFormatVersion is accepted; its zero value is invalid.
	Version       uint16
	ID            ChangeSetID
	Actor         Actor
	BaseRevision  Revision
	Operations    []Operation
	Preconditions []Precondition
}

// Validate checks the complete public ChangeSet contract.
func (c ChangeSet) Validate() error {
	if c.Version != ChangeSetFormatVersion {
		return invalidChangeSet(fmt.Errorf("%w: unsupported change set version %d", ErrInvalidChangeSet, c.Version))
	}
	if err := c.ID.Validate(); err != nil {
		return invalidChangeSet(err)
	}
	if err := c.Actor.Validate(); err != nil {
		return invalidChangeSet(err)
	}
	if !c.BaseRevision.Valid() {
		return invalidChangeSet(fmt.Errorf("%w: base revision", ErrInvalidChangeSet))
	}
	if len(c.Operations) == 0 {
		return invalidChangeSet(fmt.Errorf("%w: no operations", ErrInvalidChangeSet))
	}
	for _, op := range c.Operations {
		if err := validateOperationValue(op); err != nil {
			return invalidChangeSet(err)
		}
	}
	for _, pre := range c.Preconditions {
		if err := validatePreconditionValue(pre); err != nil {
			return invalidChangeSet(err)
		}
	}
	return nil
}

func validateOperationValue(operation Operation) error {
	switch operation.(type) {
	case EnsureRelation,
		MoveConcept,
		RenameFragment,
		SetGenerated,
		EnsureVerification,
		RemoveVerification,
		PutSource,
		RemoveSource,
		SetUsageWindow,
		SetLifecycle,
		PutAttestedComputation,
		SetBundleVersion,
		MigrateV01ToV02:
		return operation.validate()
	default:
		return fmt.Errorf("%w: operation must be a supported non-pointer value, got %T", ErrInvalidChangeSet, operation)
	}
}

func validatePreconditionValue(precondition Precondition) error {
	switch precondition.(type) {
	case RefExists,
		RefAbsent,
		FileDigestEquals,
		RelationExists,
		RelationAbsent,
		FragmentUnique,
		RevisionEquals:
		return precondition.validate()
	default:
		return fmt.Errorf("%w: precondition must be a supported non-pointer value, got %T", ErrInvalidChangeSet, precondition)
	}
}

func invalidChangeSet(cause error) error {
	return &InvalidChangeSet{Code: "invalid_change_set", Cause: cause}
}

// CommitOptions controls a Commit invocation. IdempotencyKey is optional: an
// empty key disables receipt replay. A non-empty key must validate.
type CommitOptions struct{ IdempotencyKey IdempotencyKey }

// Validate checks options independently of a ChangeSet.
func (o CommitOptions) Validate() error {
	if o.IdempotencyKey == "" {
		return nil
	}
	return o.IdempotencyKey.Validate()
}

// Ref is a canonical OKF target. RelationRef is retained in public contracts
// to avoid inventing a second reference model.
type Ref = bundle.RelationRef
