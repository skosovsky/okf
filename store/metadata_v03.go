package store

import (
	"github.com/skosovsky/okf/bundle"
)

// SetBundleMetadata updates named metadata fields on an OKF 0.3 root index.
// Nil leaves a field unchanged. An empty string removes the field.
type SetBundleMetadata struct {
	title, description, license *string
}

func NewSetBundleMetadata(title, description, license *string) (SetBundleMetadata, error) {
	op := SetBundleMetadata{copyString(title), copyString(description), copyString(license)}
	return op, op.validate()
}

func (o SetBundleMetadata) Metadata() (title, description, license *string) {
	return copyString(o.title), copyString(o.description), copyString(o.license)
}

func (SetBundleMetadata) operation() {}

func (o SetBundleMetadata) validate() error {
	if o.title == nil && o.description == nil && o.license == nil {
		return invalidOperation("bundle metadata update needs at least one field")
	}
	for _, value := range []*string{o.title, o.description, o.license} {
		if value != nil && *value != "" {
			if err := validateOpenV02String("bundle metadata", *value); err != nil {
				return err
			}
		}
	}
	return nil
}

func (o SetBundleMetadata) appendCanonical(e *canonicalEncoder) {
	e.string("set_bundle_metadata")
	for _, value := range []*string{o.title, o.description, o.license} {
		e.optional(value != nil, func() { e.string(*value) })
	}
}

// SetSupersededBy sets or removes the replacement path of an OKF 0.3 concept.
type SetSupersededBy struct {
	Concept bundle.ConceptID
	path    *string
}

func NewSetSupersededBy(id bundle.ConceptID, path *string) (SetSupersededBy, error) {
	op := SetSupersededBy{Concept: id, path: copyString(path)}
	return op, op.validate()
}

func (o SetSupersededBy) Path() *string { return copyString(o.path) }

func (SetSupersededBy) operation() {}

func (o SetSupersededBy) validate() error {
	if err := validateSemanticConceptID(o.Concept); err != nil {
		return err
	}
	if o.path != nil {
		return validateOpenV02String("superseded_by path", *o.path)
	}
	return nil
}

func (o SetSupersededBy) appendCanonical(e *canonicalEncoder) {
	e.string("set_superseded_by")
	e.string(o.Concept.String())
	e.optional(o.path != nil, func() { e.string(*o.path) })
}

func copyString(value *string) *string {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}
