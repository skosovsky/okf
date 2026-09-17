package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/internal/migrationpath"
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

// Generation is the desired generated family of one concept. At is optional;
// when present it must be an RFC3339 timestamp.
type Generation struct {
	By string
	At string
}

// Verification identifies one verification event. The (By, At) pair is the
// canonical event selector used by EnsureVerification and RemoveVerification.
type Verification struct {
	By string
	At string
}

// UsageWindow is an inclusive source-usage observation interval.
type UsageWindow struct {
	From string
	To   string
}

// ProvenanceSource is the standard, known semantic portion of one v0.2 source
// entry. Unknown nested fields are preserved by mutation handlers and are not
// exposed as an arbitrary YAML mutation surface.
type ProvenanceSource struct {
	ID           string
	Resource     string
	Title        string
	Author       string
	UsageCount   *uint64
	LastModified string
	UsageWindow  *UsageWindow
}

// SourceSelector selects either the unique source carrying ID or an anonymous
// source by full equality of its known v0.2 fields. Constructors are required
// so the selector cannot be both forms and cannot retain caller-owned pointers.
type SourceSelector struct {
	id    string
	exact *ProvenanceSource
}

// SourceByID constructs a unique-ID source selector.
func SourceByID(id string) (SourceSelector, error) {
	s := SourceSelector{id: id}
	if err := s.validate(); err != nil {
		return SourceSelector{}, err
	}
	return s, nil
}

// SourceByExact constructs an anonymous-source selector. RemoveSource fails
// closed when unknown fields prevent full exact equality. SetUsageWindow may
// use the same selector when it can prove the selector's pre/post projection,
// preserving unknown source extensions around that narrow update.
func SourceByExact(source ProvenanceSource) (SourceSelector, error) {
	cloned := cloneProvenanceSource(source)
	s := SourceSelector{exact: &cloned}
	if err := s.validate(); err != nil {
		return SourceSelector{}, err
	}
	return s, nil
}

// ID returns the ID selector and whether this selector uses the ID form.
func (s SourceSelector) ID() (string, bool) { return s.id, s.id != "" }

// Exact returns a defensive copy of the exact selector and whether this
// selector uses the exact-value form.
func (s SourceSelector) Exact() (ProvenanceSource, bool) {
	if s.exact == nil {
		return ProvenanceSource{}, false
	}
	return cloneProvenanceSource(*s.exact), true
}

func (s SourceSelector) validate() error {
	if s.id != "" && s.exact != nil {
		return invalidOperation("source selector must have exactly one form")
	}
	if s.id != "" {
		if err := validateSourceID(s.id); err != nil {
			return err
		}
		return nil
	}
	if s.exact == nil || s.exact.ID != "" {
		return invalidOperation("exact source selector requires an anonymous source")
	}
	return validateProvenanceSource(*s.exact)
}

func (s SourceSelector) appendCanonical(e *canonicalEncoder) {
	if s.id != "" {
		e.string("id")
		e.string(s.id)
		return
	}
	e.string("exact")
	appendCanonicalProvenanceSource(e, *s.exact)
}

// Lifecycle is the complete desired lifecycle family. Nil fields remove the
// corresponding standard key; setting both nil removes the whole family.
type Lifecycle struct {
	Status     *string
	StaleAfter *string
}

// ComputationParameter declares one typed runtime binding.
type ComputationParameter struct {
	Name     string
	Type     string
	Required bool
}

// ExecutorContract declares inert executor metadata and receipt field names.
type ExecutorContract struct {
	Resource string
	Receipt  []string
}

// AttesterContract declares the inert deterministic attester resource.
type AttesterContract struct {
	Resource string
}

// AttestedComputationMode is the explicit payload representation selected by
// an AttestedComputationContract. The zero value and unknown values are invalid.
type AttestedComputationMode string

const (
	AttestedComputationModeInline AttestedComputationMode = "inline"
	AttestedComputationModeFile   AttestedComputationMode = "file"
)

// AttestedComputationContract is the complete desired atomic v0.2 computation
// contract. Mode is required because an empty inline payload is valid and must
// remain distinguishable from an absent payload. InlineLanguage is presentation
// metadata for the single fenced computation.
type AttestedComputationContract struct {
	Mode              AttestedComputationMode
	Runtime           string
	Parameters        []ComputationParameter
	ComputationPath   string
	InlineComputation string
	InlineLanguage    string
	Executor          ExecutorContract
	Attester          AttesterContract
}

// SetGenerated sets the complete generated family for Concept.
type SetGenerated struct {
	Concept   bundle.ConceptID
	Generated Generation
}

// NewSetGenerated constructs a validated generated-family operation.
func NewSetGenerated(concept bundle.ConceptID, generated Generation) (SetGenerated, error) {
	o := SetGenerated{Concept: concept, Generated: generated}
	if err := o.validate(); err != nil {
		return SetGenerated{}, err
	}
	return o, nil
}

func (SetGenerated) operation() {}
func (o SetGenerated) validate() error {
	if err := validateSemanticConceptID(o.Concept); err != nil {
		return err
	}
	return validateGeneration(o.Generated)
}
func (o SetGenerated) appendCanonical(e *canonicalEncoder) {
	e.string("set_generated")
	e.string(o.Concept.String())
	e.string(o.Generated.By)
	e.string(o.Generated.At)
}

// EnsureVerification ensures one verification event is present exactly once.
type EnsureVerification struct {
	Concept      bundle.ConceptID
	Verification Verification
}

// NewEnsureVerification constructs a validated verification-ensure operation.
func NewEnsureVerification(concept bundle.ConceptID, verification Verification) (EnsureVerification, error) {
	o := EnsureVerification{Concept: concept, Verification: verification}
	if err := o.validate(); err != nil {
		return EnsureVerification{}, err
	}
	return o, nil
}

func (EnsureVerification) operation() {}
func (o EnsureVerification) validate() error {
	if err := validateSemanticConceptID(o.Concept); err != nil {
		return err
	}
	return validateVerification(o.Verification)
}
func (o EnsureVerification) appendCanonical(e *canonicalEncoder) {
	e.string("ensure_verification")
	e.string(o.Concept.String())
	e.string(o.Verification.By)
	e.string(o.Verification.At)
}

// RemoveVerification removes the uniquely selected verification event.
type RemoveVerification struct {
	Concept      bundle.ConceptID
	Verification Verification
}

// NewRemoveVerification constructs a validated verification-removal operation.
func NewRemoveVerification(concept bundle.ConceptID, verification Verification) (RemoveVerification, error) {
	o := RemoveVerification{Concept: concept, Verification: verification}
	if err := o.validate(); err != nil {
		return RemoveVerification{}, err
	}
	return o, nil
}

func (RemoveVerification) operation() {}
func (o RemoveVerification) validate() error {
	if err := validateSemanticConceptID(o.Concept); err != nil {
		return err
	}
	return validateVerification(o.Verification)
}
func (o RemoveVerification) appendCanonical(e *canonicalEncoder) {
	e.string("remove_verification")
	e.string(o.Concept.String())
	e.string(o.Verification.By)
	e.string(o.Verification.At)
}

// PutSource ensures the desired known source fields are present. An ID source
// updates the unique matching entry while preserving unknown nested fields; an
// anonymous source is matched only by exact known semantic equality.
type PutSource struct {
	Concept bundle.ConceptID
	source  ProvenanceSource
}

// NewPutSource constructs an immutable source operation.
func NewPutSource(concept bundle.ConceptID, source ProvenanceSource) (PutSource, error) {
	o := PutSource{Concept: concept, source: cloneProvenanceSource(source)}
	if err := o.validate(); err != nil {
		return PutSource{}, err
	}
	return o, nil
}

// Source returns a defensive copy of the desired source.
func (o PutSource) Source() ProvenanceSource { return cloneProvenanceSource(o.source) }
func (PutSource) operation()                 {}
func (o PutSource) validate() error {
	if err := validateSemanticConceptID(o.Concept); err != nil {
		return err
	}
	return validateProvenanceSource(o.source)
}
func (o PutSource) appendCanonical(e *canonicalEncoder) {
	e.string("put_source")
	e.string(o.Concept.String())
	appendCanonicalProvenanceSource(e, o.source)
}

// RemoveSource removes one uniquely selected source.
type RemoveSource struct {
	Concept  bundle.ConceptID
	selector SourceSelector
}

// NewRemoveSource constructs an immutable source-removal operation.
func NewRemoveSource(concept bundle.ConceptID, selector SourceSelector) (RemoveSource, error) {
	o := RemoveSource{Concept: concept, selector: cloneSourceSelector(selector)}
	if err := o.validate(); err != nil {
		return RemoveSource{}, err
	}
	return o, nil
}

// Selector returns a defensive copy of the source selector.
func (o RemoveSource) Selector() SourceSelector { return cloneSourceSelector(o.selector) }
func (RemoveSource) operation()                 {}
func (o RemoveSource) validate() error {
	if err := validateSemanticConceptID(o.Concept); err != nil {
		return err
	}
	return o.selector.validate()
}
func (o RemoveSource) appendCanonical(e *canonicalEncoder) {
	e.string("remove_source")
	e.string(o.Concept.String())
	o.selector.appendCanonical(e)
}

// SetUsageWindow sets or removes a shared or per-source usage window. A nil
// selector addresses the shared root window; a nil window removes it.
type SetUsageWindow struct {
	Concept  bundle.ConceptID
	selector *SourceSelector
	window   *UsageWindow
}

// NewSetUsageWindow constructs an immutable usage-window operation.
func NewSetUsageWindow(concept bundle.ConceptID, selector *SourceSelector, window *UsageWindow) (SetUsageWindow, error) {
	o := SetUsageWindow{Concept: concept}
	if selector != nil {
		cloned := cloneSourceSelector(*selector)
		o.selector = &cloned
	}
	if window != nil {
		cloned := *window
		o.window = &cloned
	}
	if err := o.validate(); err != nil {
		return SetUsageWindow{}, err
	}
	return o, nil
}

// Selector returns the per-source selector. False means the shared window.
func (o SetUsageWindow) Selector() (SourceSelector, bool) {
	if o.selector == nil {
		return SourceSelector{}, false
	}
	return cloneSourceSelector(*o.selector), true
}

// Window returns the desired window. False means removal.
func (o SetUsageWindow) Window() (UsageWindow, bool) {
	if o.window == nil {
		return UsageWindow{}, false
	}
	return *o.window, true
}

func (SetUsageWindow) operation() {}
func (o SetUsageWindow) validate() error {
	if err := validateSemanticConceptID(o.Concept); err != nil {
		return err
	}
	if o.selector != nil {
		if err := o.selector.validate(); err != nil {
			return err
		}
	}
	if o.window != nil {
		return validateUsageWindow(*o.window)
	}
	return nil
}
func (o SetUsageWindow) appendCanonical(e *canonicalEncoder) {
	e.string("set_usage_window")
	e.string(o.Concept.String())
	e.optional(o.selector != nil, func() { o.selector.appendCanonical(e) })
	e.optional(o.window != nil, func() {
		e.string(o.window.From)
		e.string(o.window.To)
	})
}

// SetLifecycle sets or removes the complete standard lifecycle family.
type SetLifecycle struct {
	Concept   bundle.ConceptID
	lifecycle Lifecycle
}

// NewSetLifecycle constructs an immutable lifecycle operation.
func NewSetLifecycle(concept bundle.ConceptID, lifecycle Lifecycle) (SetLifecycle, error) {
	o := SetLifecycle{Concept: concept, lifecycle: cloneLifecycle(lifecycle)}
	if err := o.validate(); err != nil {
		return SetLifecycle{}, err
	}
	return o, nil
}

// Lifecycle returns a defensive copy of the desired lifecycle state.
func (o SetLifecycle) Lifecycle() Lifecycle { return cloneLifecycle(o.lifecycle) }
func (SetLifecycle) operation()             {}
func (o SetLifecycle) validate() error {
	if err := validateSemanticConceptID(o.Concept); err != nil {
		return err
	}
	return validateLifecycle(o.lifecycle)
}
func (o SetLifecycle) appendCanonical(e *canonicalEncoder) {
	e.string("set_lifecycle")
	e.string(o.Concept.String())
	e.optional(o.lifecycle.Status != nil, func() { e.string(*o.lifecycle.Status) })
	e.optional(o.lifecycle.StaleAfter != nil, func() { e.string(*o.lifecycle.StaleAfter) })
}

// PutAttestedComputation atomically sets a complete Attested Computation
// contract, including exactly one inline or file-backed computation mode.
type PutAttestedComputation struct {
	Concept  bundle.ConceptID
	contract AttestedComputationContract
}

// NewPutAttestedComputation constructs an immutable atomic contract operation.
func NewPutAttestedComputation(concept bundle.ConceptID, contract AttestedComputationContract) (PutAttestedComputation, error) {
	o := PutAttestedComputation{Concept: concept, contract: cloneAttestedComputationContract(contract)}
	if err := o.validate(); err != nil {
		return PutAttestedComputation{}, err
	}
	return o, nil
}

// Contract returns a defensive copy of the complete desired contract.
func (o PutAttestedComputation) Contract() AttestedComputationContract {
	return cloneAttestedComputationContract(o.contract)
}

func (PutAttestedComputation) operation() {}
func (o PutAttestedComputation) validate() error {
	if err := validateSemanticConceptID(o.Concept); err != nil {
		return err
	}
	return validateAttestedComputationContract(o.contract)
}
func (o PutAttestedComputation) appendCanonical(e *canonicalEncoder) {
	e.string("put_attested_computation")
	e.string(o.Concept.String())
	appendCanonicalAttestedComputationContract(e, o.contract)
}

// SetBundleVersion sets the declaration in the bundle-root index.md.
type SetBundleVersion struct{ Version string }

// NewSetBundleVersion constructs a validated bundle-version operation.
func NewSetBundleVersion(version string) (SetBundleVersion, error) {
	o := SetBundleVersion{Version: version}
	if err := o.validate(); err != nil {
		return SetBundleVersion{}, err
	}
	return o, nil
}

func (SetBundleVersion) operation() {}
func (o SetBundleVersion) validate() error {
	if _, err := bundle.ParseOKFVersion(o.Version); err != nil {
		return invalidOperation("invalid OKF bundle version")
	}
	return nil
}
func (o SetBundleVersion) appendCanonical(e *canonicalEncoder) {
	e.string("set_bundle_version")
	e.string(o.Version)
}

// LegacyTimestampPolicy controls whether a successfully copied legacy
// timestamp remains in the document.
type LegacyTimestampPolicy string

const (
	LegacyTimestampPreserve        LegacyTimestampPolicy = "preserve"
	LegacyTimestampRemoveAfterCopy LegacyTimestampPolicy = "remove_after_copy"
)

// TimestampConflictPolicy resolves an explicit legacy timestamp/generated.at
// conflict. The zero value is invalid so migration never guesses.
type TimestampConflictPolicy string

const (
	TimestampConflictReject       TimestampConflictPolicy = "reject"
	TimestampConflictUseGenerated TimestampConflictPolicy = "use_generated"
	TimestampConflictUseTimestamp TimestampConflictPolicy = "use_timestamp"
)

// MigrationGeneratedAt supplies generated.at for a document that has no
// usable legacy timestamp or whose explicit conflict policy selects it.
type MigrationGeneratedAt struct {
	Path string
	At   string
}

// LegacyCitationMapping maps one exact legacy citation to one explicit v0.2
// source. LegacyNumber and LegacyEntry are independent optional selectors; at
// least one is required, and both must match when both are supplied.
type LegacyCitationMapping struct {
	LegacyNumber uint64
	LegacyEntry  string
	SourceID     string
	Title        string
	Resource     string
}

// DocumentCitationMigration contains explicit mappings for one document.
type DocumentCitationMigration struct {
	Path    string
	Entries []LegacyCitationMapping
}

// MigrationAsset is a caller-supplied, inert file-backed computation asset.
// It is written only as part of its closed ComputationMigration.
type MigrationAsset struct {
	Path    string
	Content []byte
}

// ComputationMigration explicitly migrates one target concept to the supplied
// sanctioned computation contract. It never derives code from prose.
type ComputationMigration struct {
	Path     string
	Contract AttestedComputationContract
	Asset    *MigrationAsset
}

// V01ToV02Migration is the caller-owned migration request copied by
// NewMigrateV01ToV02.
type V01ToV02Migration struct {
	GeneratedBy       string
	GeneratedAt       []MigrationGeneratedAt
	TimestampPolicy   LegacyTimestampPolicy
	TimestampConflict TimestampConflictPolicy
	Citations         []DocumentCitationMigration
	Computations      []ComputationMigration
}

// MigrateV01ToV02 is a narrow closed semantic migration operation. It does not
// expose raw frontmatter, Markdown, YAML nodes, or file writes.
type MigrateV01ToV02 struct{ migration V01ToV02Migration }

// NewMigrateV01ToV02 constructs an immutable, canonicalized migration
// operation. Map-like request collections are sorted by their semantic keys.
func NewMigrateV01ToV02(request V01ToV02Migration) (MigrateV01ToV02, error) {
	o := MigrateV01ToV02{migration: cloneV01ToV02Migration(request)}
	sort.Slice(o.migration.GeneratedAt, func(i, j int) bool {
		return o.migration.GeneratedAt[i].Path < o.migration.GeneratedAt[j].Path
	})
	sort.Slice(o.migration.Citations, func(i, j int) bool {
		return o.migration.Citations[i].Path < o.migration.Citations[j].Path
	})
	for i := range o.migration.Citations {
		sort.Slice(o.migration.Citations[i].Entries, func(left, right int) bool {
			a, b := o.migration.Citations[i].Entries[left], o.migration.Citations[i].Entries[right]
			if a.LegacyNumber != b.LegacyNumber {
				return a.LegacyNumber < b.LegacyNumber
			}
			if a.LegacyEntry != b.LegacyEntry {
				return a.LegacyEntry < b.LegacyEntry
			}
			return a.SourceID < b.SourceID
		})
	}
	sort.Slice(o.migration.Computations, func(i, j int) bool {
		return o.migration.Computations[i].Path < o.migration.Computations[j].Path
	})
	if err := o.validate(); err != nil {
		return MigrateV01ToV02{}, err
	}
	return o, nil
}

// Migration returns a defensive copy of the normalized migration request.
func (o MigrateV01ToV02) Migration() V01ToV02Migration {
	return cloneV01ToV02Migration(o.migration)
}

func (MigrateV01ToV02) operation() {}
func (o MigrateV01ToV02) validate() error {
	return validateV01ToV02Migration(o.migration)
}
func (o MigrateV01ToV02) appendCanonical(e *canonicalEncoder) {
	e.string("migrate_v01_to_v02")
	e.string(o.migration.GeneratedBy)
	e.string(string(o.migration.TimestampPolicy))
	e.string(string(o.migration.TimestampConflict))
	e.uint(uint64(len(o.migration.GeneratedAt)))
	for _, generated := range o.migration.GeneratedAt {
		e.string(generated.Path)
		e.string(generated.At)
	}
	e.uint(uint64(len(o.migration.Citations)))
	for _, document := range o.migration.Citations {
		e.string(document.Path)
		e.uint(uint64(len(document.Entries)))
		for _, citation := range document.Entries {
			e.uint(citation.LegacyNumber)
			e.string(citation.LegacyEntry)
			e.string(citation.SourceID)
			e.string(citation.Title)
			e.string(citation.Resource)
		}
	}
	e.uint(uint64(len(o.migration.Computations)))
	for _, computation := range o.migration.Computations {
		e.string(computation.Path)
		appendCanonicalAttestedComputationContract(e, computation.Contract)
		e.optional(computation.Asset != nil, func() {
			e.string(computation.Asset.Path)
			e.bytes(computation.Asset.Content)
		})
	}
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

func validateSemanticConceptID(id bundle.ConceptID) error {
	if err := bundle.ValidateConceptID(id); err != nil || id.Name() == "index" || id.Name() == "log" {
		return invalidOperation("invalid semantic concept")
	}
	return nil
}

func validateGeneration(generation Generation) error {
	if err := validateDocumentActor(generation.By); err != nil {
		return err
	}
	if generation.At != "" {
		if err := validateRFC3339("generated.at", generation.At); err != nil {
			return err
		}
	}
	return nil
}

func validateVerification(verification Verification) error {
	if err := validateDocumentActor(verification.By); err != nil {
		return err
	}
	return validateRFC3339("verified.at", verification.At)
}

func validateDocumentActor(actor string) error {
	if !bundle.ValidActor(actor) {
		return invalidOperation("invalid document actor")
	}
	return nil
}

func validateProvenanceSource(source ProvenanceSource) error {
	if source.ID != "" {
		if err := validateSourceID(source.ID); err != nil {
			return err
		}
	}
	if err := validateOpenV02String("source resource", source.Resource); err != nil {
		return err
	}
	for _, field := range [...]struct {
		kind  string
		value string
	}{
		{kind: "source title", value: source.Title},
		{kind: "source author", value: source.Author},
	} {
		if field.value != "" {
			if err := validateOpenV02String(field.kind, field.value); err != nil {
				return err
			}
		}
	}
	if source.LastModified != "" {
		if err := validateDate("source last_modified", source.LastModified); err != nil {
			return err
		}
	}
	if source.UsageWindow != nil {
		if err := validateUsageWindow(*source.UsageWindow); err != nil {
			return err
		}
	}
	return nil
}

func validateUsageWindow(window UsageWindow) error {
	if err := validateDate("usage_window.from", window.From); err != nil {
		return err
	}
	if err := validateDate("usage_window.to", window.To); err != nil {
		return err
	}
	if window.From > window.To {
		return invalidOperation("usage window starts after it ends")
	}
	return nil
}

func validateLifecycle(lifecycle Lifecycle) error {
	if lifecycle.Status != nil {
		switch *lifecycle.Status {
		case "draft", "stable", "deprecated":
		default:
			return invalidOperation("invalid lifecycle status")
		}
	}
	if lifecycle.StaleAfter != nil {
		if err := validateDate("stale_after", *lifecycle.StaleAfter); err != nil {
			return err
		}
	}
	return nil
}

func validateAttestedComputationContract(contract AttestedComputationContract) error {
	if err := validateOpenV02String("computation runtime", contract.Runtime); err != nil {
		return err
	}
	seenParameters := make(map[string]struct{}, len(contract.Parameters))
	for _, parameter := range contract.Parameters {
		if err := validateOpenV02String("parameter name", parameter.Name); err != nil {
			return err
		}
		if err := validateOpenV02String("parameter type", parameter.Type); err != nil {
			return err
		}
		if _, duplicate := seenParameters[parameter.Name]; duplicate {
			return invalidOperation("duplicate computation parameter")
		}
		seenParameters[parameter.Name] = struct{}{}
	}
	switch contract.Mode {
	case AttestedComputationModeInline:
		if contract.ComputationPath != "" {
			return invalidOperation("inline computation mode cannot declare a computation path")
		}
		if !utf8.ValidString(contract.InlineComputation) ||
			strings.IndexByte(contract.InlineComputation, 0) >= 0 {
			return invalidOperation("invalid inline computation")
		}
		if contract.InlineLanguage != "" {
			if err := validatePlainValue("inline computation language", contract.InlineLanguage); err != nil {
				return err
			}
		}
	case AttestedComputationModeFile:
		if err := validateOpenV02String("computation path", contract.ComputationPath); err != nil {
			return err
		}
		if contract.InlineComputation != "" {
			return invalidOperation("file computation mode cannot declare inline computation")
		}
		if contract.InlineLanguage != "" {
			return invalidOperation("file computation mode cannot declare inline language")
		}
	default:
		return invalidOperation("invalid attested computation mode")
	}
	if err := validateOpenV02String("executor resource", contract.Executor.Resource); err != nil {
		return err
	}
	if len(contract.Executor.Receipt) == 0 {
		return invalidOperation("executor receipt must declare at least one field")
	}
	seenReceipt := make(map[string]struct{}, len(contract.Executor.Receipt))
	for _, field := range contract.Executor.Receipt {
		if err := validateOpenV02String("executor receipt field", field); err != nil {
			return err
		}
		if _, duplicate := seenReceipt[field]; duplicate {
			return invalidOperation("duplicate executor receipt field")
		}
		seenReceipt[field] = struct{}{}
	}
	return validateOpenV02String("attester resource", contract.Attester.Resource)
}

func validateV01ToV02Migration(migration V01ToV02Migration) error {
	if migration.GeneratedBy != "" {
		if err := validateDocumentActor(migration.GeneratedBy); err != nil {
			return err
		}
	}
	switch migration.TimestampPolicy {
	case LegacyTimestampPreserve, LegacyTimestampRemoveAfterCopy:
	default:
		return invalidOperation("invalid legacy timestamp policy")
	}
	switch migration.TimestampConflict {
	case TimestampConflictReject, TimestampConflictUseGenerated, TimestampConflictUseTimestamp:
	default:
		return invalidOperation("invalid timestamp conflict policy")
	}
	previousPath := ""
	for _, generated := range migration.GeneratedAt {
		if err := validateMigrationDocumentPath(generated.Path); err != nil {
			return err
		}
		if generated.Path <= previousPath {
			return invalidOperation("generated-at paths must be unique")
		}
		previousPath = generated.Path
		if err := validateRFC3339("migration generated.at", generated.At); err != nil {
			return err
		}
	}
	previousPath = ""
	for _, document := range migration.Citations {
		if err := validateMigrationDocumentPath(document.Path); err != nil {
			return err
		}
		if document.Path <= previousPath {
			return invalidOperation("citation migration paths must be unique")
		}
		previousPath = document.Path
		if len(document.Entries) == 0 {
			return invalidOperation("citation migration must contain mappings")
		}
		var previousSelector citationSelector
		havePreviousSelector := false
		seenNumbers := make(map[uint64]struct{}, len(document.Entries))
		selectorNumberByEntry := make(map[string]uint64, len(document.Entries))
		type sourceMetadata struct {
			title    string
			resource string
		}
		sourceMetadataByID := make(map[string]sourceMetadata, len(document.Entries))
		for _, citation := range document.Entries {
			if citation.LegacyEntry != "" {
				if err := validateLegacyCitationEntry(citation.LegacyEntry); err != nil {
					return err
				}
			}
			if citation.LegacyNumber == 0 && citation.LegacyEntry == "" {
				return invalidOperation("citation mapping requires a number or exact legacy entry")
			}
			if citation.LegacyNumber != 0 {
				if _, duplicate := seenNumbers[citation.LegacyNumber]; duplicate {
					return invalidOperation("legacy citation numbers must be unique")
				}
				seenNumbers[citation.LegacyNumber] = struct{}{}
			}
			if citation.LegacyEntry != "" {
				if previousNumber, duplicate := selectorNumberByEntry[citation.LegacyEntry]; duplicate &&
					(previousNumber == 0 || citation.LegacyNumber == 0) {
					return invalidOperation("legacy citation entry selectors overlap")
				}
				selectorNumberByEntry[citation.LegacyEntry] = citation.LegacyNumber
			}
			if err := validateSourceID(citation.SourceID); err != nil {
				return err
			}
			if citation.Title != "" {
				if err := validateOpenV02String("migration source title", citation.Title); err != nil {
					return err
				}
			}
			if citation.Resource != "" {
				if err := validateOpenV02String("migration source resource", citation.Resource); err != nil {
					return err
				}
			}
			selector := citationSelector{number: citation.LegacyNumber, entry: citation.LegacyEntry}
			if havePreviousSelector && !previousSelector.less(selector) {
				return invalidOperation("citation mapping selectors must be unique")
			}
			previousSelector, havePreviousSelector = selector, true
			metadata, reused := sourceMetadataByID[citation.SourceID]
			if reused &&
				((metadata.title != "" && citation.Title != "" && metadata.title != citation.Title) ||
					(metadata.resource != "" && citation.Resource != "" && metadata.resource != citation.Resource)) {
				return invalidOperation("reused migration source id has conflicting metadata")
			}
			if metadata.title == "" {
				metadata.title = citation.Title
			}
			if metadata.resource == "" {
				metadata.resource = citation.Resource
			}
			sourceMetadataByID[citation.SourceID] = metadata
		}
	}
	previousPath = ""
	for _, computation := range migration.Computations {
		if err := validateMigrationComputationPath(computation.Path); err != nil {
			return err
		}
		if computation.Path <= previousPath {
			return invalidOperation("computation migration paths must be unique")
		}
		previousPath = computation.Path
		if err := validateAttestedComputationContract(computation.Contract); err != nil {
			return err
		}
		if computation.Contract.Mode == AttestedComputationModeInline {
			if computation.Asset != nil {
				return invalidOperation("inline computation migration cannot carry an asset")
			}
			continue
		}
		if computation.Asset == nil {
			return invalidOperation("file computation migration requires an asset")
		}
		if err := validateMigrationAssetPath(computation.Asset.Path); err != nil {
			return err
		}
	}
	return nil
}

type citationSelector struct {
	number uint64
	entry  string
}

func (left citationSelector) less(right citationSelector) bool {
	if left.number != right.number {
		return left.number < right.number
	}
	return left.entry < right.entry
}

func validateMigrationDocumentPath(value string) error {
	if !migrationpath.IsMarkdownDocument(value) {
		return invalidOperation("invalid migration document path")
	}
	return nil
}

func validateMigrationComputationPath(value string) error {
	if !migrationpath.IsMarkdownDocument(value) ||
		value == "index.md" || value == "log.md" ||
		strings.HasSuffix(value, "/index.md") || strings.HasSuffix(value, "/log.md") {
		return invalidOperation("invalid migration computation path")
	}
	return nil
}

func validateMigrationAssetPath(value string) error {
	if err := validateManifestPath(value); err != nil ||
		strings.HasSuffix(value, ".md") ||
		strings.ContainsAny(value, "?#") {
		return invalidOperation("invalid migration computation asset path")
	}
	return nil
}

func validatePlainValue(kind, value string) error {
	if err := validateIdentity(kind, value, 4096); err != nil {
		return err
	}
	return nil
}

// validateOpenV02String follows the domain contract for v0.2 fields whose
// values are open strings rather than transport identities. Exact caller bytes
// are preserved; only malformed UTF-8 and values containing no non-whitespace
// rune are rejected.
func validateOpenV02String(kind, value string) error {
	if !utf8.ValidString(value) || strings.TrimSpace(value) == "" {
		return invalidOperation("invalid " + kind)
	}
	return nil
}

// validateSourceID names the attribution-identity boundary separately from
// other open strings. Source IDs are exact, non-blank UTF-8 join keys; selector
// uniqueness and CommonMark-normalized attribution collisions are checked by
// their respective mutation and validation layers.
func validateSourceID(value string) error {
	return validateOpenV02String("source id", value)
}

func validateLegacyCitationEntry(value string) error {
	if len(value) > 4096 || !utf8.ValidString(value) || strings.TrimSpace(value) == "" ||
		strings.ContainsRune(value, '\x00') {
		return invalidOperation("invalid legacy citation entry")
	}
	for index := 0; index < len(value); index++ {
		current := value[index]
		if (current < 0x20 && current != '\t' && current != '\n' && current != '\r') || current == 0x7f {
			return invalidOperation("invalid legacy citation entry")
		}
	}
	return nil
}

func validateRFC3339(kind, value string) error {
	if value == "" {
		return invalidOperation("missing " + kind)
	}
	if _, err := time.Parse(time.RFC3339, value); err != nil {
		return invalidOperation("invalid " + kind)
	}
	return nil
}

func validateDate(kind, value string) error {
	if value == "" {
		return invalidOperation("missing " + kind)
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil || parsed.Format("2006-01-02") != value {
		return invalidOperation("invalid " + kind)
	}
	return nil
}

func invalidOperation(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidChangeSet, message)
}

func cloneProvenanceSource(source ProvenanceSource) ProvenanceSource {
	if source.UsageCount != nil {
		count := *source.UsageCount
		source.UsageCount = &count
	}
	if source.UsageWindow != nil {
		window := *source.UsageWindow
		source.UsageWindow = &window
	}
	return source
}

func cloneSourceSelector(selector SourceSelector) SourceSelector {
	cloned := SourceSelector{id: selector.id}
	if selector.exact != nil {
		exact := cloneProvenanceSource(*selector.exact)
		cloned.exact = &exact
	}
	return cloned
}

func cloneLifecycle(lifecycle Lifecycle) Lifecycle {
	if lifecycle.Status != nil {
		status := *lifecycle.Status
		lifecycle.Status = &status
	}
	if lifecycle.StaleAfter != nil {
		staleAfter := *lifecycle.StaleAfter
		lifecycle.StaleAfter = &staleAfter
	}
	return lifecycle
}

func cloneAttestedComputationContract(contract AttestedComputationContract) AttestedComputationContract {
	contract.Parameters = append([]ComputationParameter(nil), contract.Parameters...)
	contract.Executor.Receipt = append([]string(nil), contract.Executor.Receipt...)
	return contract
}

func cloneV01ToV02Migration(migration V01ToV02Migration) V01ToV02Migration {
	migration.GeneratedAt = append([]MigrationGeneratedAt(nil), migration.GeneratedAt...)
	migration.Citations = append([]DocumentCitationMigration(nil), migration.Citations...)
	for i := range migration.Citations {
		migration.Citations[i].Entries = append([]LegacyCitationMapping(nil), migration.Citations[i].Entries...)
	}
	migration.Computations = append([]ComputationMigration(nil), migration.Computations...)
	for i := range migration.Computations {
		migration.Computations[i].Contract = cloneAttestedComputationContract(migration.Computations[i].Contract)
		if migration.Computations[i].Asset != nil {
			asset := *migration.Computations[i].Asset
			asset.Content = append([]byte(nil), asset.Content...)
			migration.Computations[i].Asset = &asset
		}
	}
	return migration
}

func appendCanonicalProvenanceSource(e *canonicalEncoder, source ProvenanceSource) {
	e.string(source.ID)
	e.string(source.Resource)
	e.string(source.Title)
	e.string(source.Author)
	e.optional(source.UsageCount != nil, func() { e.uint(*source.UsageCount) })
	e.string(source.LastModified)
	e.optional(source.UsageWindow != nil, func() {
		e.string(source.UsageWindow.From)
		e.string(source.UsageWindow.To)
	})
}

func appendCanonicalAttestedComputationContract(e *canonicalEncoder, contract AttestedComputationContract) {
	e.string(string(contract.Mode))
	e.string(contract.Runtime)
	e.uint(uint64(len(contract.Parameters)))
	for _, parameter := range contract.Parameters {
		e.string(parameter.Name)
		e.string(parameter.Type)
		e.boolean(parameter.Required)
	}
	e.string(contract.ComputationPath)
	e.string(contract.InlineComputation)
	e.string(contract.InlineLanguage)
	e.string(contract.Executor.Resource)
	e.uint(uint64(len(contract.Executor.Receipt)))
	for _, field := range contract.Executor.Receipt {
		e.string(field)
	}
	e.string(contract.Attester.Resource)
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
func (e *canonicalEncoder) bytes(v []byte)  { e.uint(uint64(len(v))); e.Write(v) }
func (e *canonicalEncoder) boolean(v bool) {
	if v {
		e.uint(1)
		return
	}
	e.uint(0)
}
func (e *canonicalEncoder) optional(present bool, appendValue func()) {
	e.boolean(present)
	if present {
		appendValue()
	}
}
func (e *canonicalEncoder) ref(ref bundle.RelationRef) {
	e.string(ref.ID.String())
	e.string(ref.Fragment)
}
