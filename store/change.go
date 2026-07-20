package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/skosovsky/okf/bundle"
)

// Operation is a closed set of idempotent semantic intents. Only the concrete
// operation types in this package implement it, so canonical serialization is
// complete and forward-compatible through ChangeSetFormatVersion.
type Operation interface {
	operation()
	validate() error
	appendCanonical(*canonicalEncoder)
}

// EnsureRelation makes the specified semantic relation present exactly once.
type EnsureRelation struct {
	Source bundle.RelationRef
	Type   string
	Target bundle.RelationRef
}

func (EnsureRelation) operation()        {}
func (o EnsureRelation) validate() error { return validateRelation(o.Source, o.Type, o.Target) }
func (o EnsureRelation) appendCanonical(e *canonicalEncoder) {
	e.string("ensure_relation")
	e.ref(o.Source)
	e.string(o.Type)
	e.ref(o.Target)
}

// MoveConcept moves a concept from From to To, rewriting statically resolvable
// canonical references as part of its eventual operation plan.
type MoveConcept struct{ From, To bundle.ConceptID }

func (MoveConcept) operation() {}
func (o MoveConcept) validate() error {
	if err := validateMovableConceptID(o.From); err != nil {
		return err
	}
	if err := validateMovableConceptID(o.To); err != nil {
		return err
	}
	if o.From.String() == o.To.String() {
		return fmt.Errorf("%w: invalid move concept", ErrInvalidChangeSet)
	}
	return nil
}
func (o MoveConcept) appendCanonical(e *canonicalEncoder) {
	e.string("move_concept")
	e.string(o.From.String())
	e.string(o.To.String())
}

// RenameFragment renames one explicit, unique fragment in Concept and rewrites
// all incoming canonical fragment references.
type RenameFragment struct {
	Concept  bundle.ConceptID
	From, To string
}

func (RenameFragment) operation() {}
func (o RenameFragment) validate() error {
	if err := bundle.ValidateConceptID(o.Concept); err != nil || bundle.ValidateRelationFragment(o.From) != nil || bundle.ValidateRelationFragment(o.To) != nil || o.From == o.To {
		return fmt.Errorf("%w: invalid rename fragment", ErrInvalidChangeSet)
	}
	return nil
}
func (o RenameFragment) appendCanonical(e *canonicalEncoder) {
	e.string("rename_fragment")
	e.string(o.Concept.String())
	e.string(o.From)
	e.string(o.To)
}

// Precondition is a closed set of assertions evaluated against the snapshot
// selected by Preview or Commit.
type Precondition interface {
	precondition()
	validate() error
	appendCanonical(*canonicalEncoder)
}

type RefExists struct{ Ref bundle.RelationRef }

func (RefExists) precondition()                         {}
func (p RefExists) validate() error                     { return validateRef(p.Ref) }
func (p RefExists) appendCanonical(e *canonicalEncoder) { e.string("ref_exists"); e.ref(p.Ref) }

type RefAbsent struct{ Ref bundle.RelationRef }

func (RefAbsent) precondition()                         {}
func (p RefAbsent) validate() error                     { return validateRef(p.Ref) }
func (p RefAbsent) appendCanonical(e *canonicalEncoder) { e.string("ref_absent"); e.ref(p.Ref) }

type FileDigestEquals struct{ Path, Digest string }

func (FileDigestEquals) precondition() {}
func (p FileDigestEquals) validate() error {
	if err := validateManifestPath(p.Path); err != nil {
		return fmt.Errorf("%w: invalid file digest precondition", ErrInvalidChangeSet)
	}
	if err := validateSHA256HexDigest(p.Digest); err != nil {
		return fmt.Errorf("%w: invalid file digest precondition", ErrInvalidChangeSet)
	}
	return nil
}
func (p FileDigestEquals) appendCanonical(e *canonicalEncoder) {
	e.string("file_digest_equals")
	e.string(p.Path)
	e.string(p.Digest)
}

type RelationExists struct {
	Source bundle.RelationRef
	Type   string
	Target bundle.RelationRef
}

func (RelationExists) precondition()     {}
func (p RelationExists) validate() error { return validateRelation(p.Source, p.Type, p.Target) }
func (p RelationExists) appendCanonical(e *canonicalEncoder) {
	e.string("relation_exists")
	e.ref(p.Source)
	e.string(p.Type)
	e.ref(p.Target)
}

type RelationAbsent struct {
	Source bundle.RelationRef
	Type   string
	Target bundle.RelationRef
}

func (RelationAbsent) precondition()     {}
func (p RelationAbsent) validate() error { return validateRelation(p.Source, p.Type, p.Target) }
func (p RelationAbsent) appendCanonical(e *canonicalEncoder) {
	e.string("relation_absent")
	e.ref(p.Source)
	e.string(p.Type)
	e.ref(p.Target)
}

type FragmentUnique struct{ Ref bundle.RelationRef }

func (FragmentUnique) precondition() {}
func (p FragmentUnique) validate() error {
	if p.Ref.Fragment == "" {
		return fmt.Errorf("%w: fragment unique needs fragment", ErrInvalidChangeSet)
	}
	return validateRef(p.Ref)
}
func (p FragmentUnique) appendCanonical(e *canonicalEncoder) {
	e.string("fragment_unique")
	e.ref(p.Ref)
}

type RevisionEquals struct{ Revision Revision }

func (RevisionEquals) precondition() {}
func (p RevisionEquals) validate() error {
	if !p.Revision.Valid() {
		return fmt.Errorf("%w: invalid revision precondition", ErrInvalidChangeSet)
	}
	return nil
}
func (p RevisionEquals) appendCanonical(e *canonicalEncoder) {
	e.string("revision_equals")
	e.string(p.Revision.String())
}

func validateRef(ref bundle.RelationRef) error {
	if err := bundle.ValidateRelationRef(ref); err != nil {
		return fmt.Errorf("%w: invalid ref", ErrInvalidChangeSet)
	}
	return nil
}
func validateRelation(source bundle.RelationRef, kind string, target bundle.RelationRef) error {
	if err := validateRef(source); err != nil {
		return err
	}
	if err := validateRef(target); err != nil {
		return err
	}
	if err := bundle.ValidateRelationType(kind); err != nil {
		return fmt.Errorf("%w: invalid relation type", ErrInvalidChangeSet)
	}
	return nil
}
func validateMovableConceptID(id bundle.ConceptID) error {
	if err := bundle.ValidateConceptID(id); err != nil || id.Name() == "index" || id.Name() == "log" {
		return fmt.Errorf("%w: invalid move concept", ErrInvalidChangeSet)
	}
	return nil
}

// ChangeSetFormatVersion identifies the canonical binary ChangeSet format.
const ChangeSetFormatVersion uint16 = 1

// CanonicalBytes returns the versioned deterministic ChangeSet representation.
// It is intended for hashing and journal/receipt persistence, not for JSON APIs.
func (c ChangeSet) CanonicalBytes() ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	e := canonicalEncoder{}
	e.string("okf:changeset")
	e.uint(uint64(c.Version))
	e.string(string(c.ID))
	e.string(string(c.Actor))
	e.string(c.BaseRevision.String())
	e.uint(uint64(len(c.Operations)))
	for _, op := range c.Operations {
		op.appendCanonical(&e)
	}
	e.uint(uint64(len(c.Preconditions)))
	for _, pre := range c.Preconditions {
		pre.appendCanonical(&e)
	}
	return append([]byte(nil), e.Bytes()...), nil
}

// RequestDigest returns sha256:<lowercase-hex> of CanonicalBytes.
func (c ChangeSet) RequestDigest() (string, error) {
	raw, err := c.CanonicalBytes()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

type canonicalEncoder struct{ bytes.Buffer }

func (e *canonicalEncoder) uint(v uint64) {
	var b [8]byte
	for i := 7; i >= 0; i-- {
		b[i] = byte(v)
		v >>= 8
	}
	e.Write(b[:])
}
func (e *canonicalEncoder) string(v string) { e.uint(uint64(len(v))); e.WriteString(v) }
func (e *canonicalEncoder) ref(ref bundle.RelationRef) {
	e.string(ref.ID.String())
	e.string(ref.Fragment)
}
