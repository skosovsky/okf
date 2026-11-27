package store

import (
	"context"

	"github.com/skosovsky/okf/bundle"
)

// ProvenanceV03 applies an explicit generation or verification edit under the
// OKF 0.3 actor and timestamp contract.
type ProvenanceV03 struct {
	concept bundle.ConceptID
	kind    string
	by, at  string
}

func NewSetGeneratedV03(concept bundle.ConceptID, generated Generation) (ProvenanceV03, error) {
	return newProvenanceV03(concept, "set_generated", generated.By, generated.At)
}

func NewEnsureVerificationV03(concept bundle.ConceptID, verification Verification) (ProvenanceV03, error) {
	return newProvenanceV03(concept, "ensure_verification", verification.By, verification.At)
}

func NewRemoveVerificationV03(concept bundle.ConceptID, verification Verification) (ProvenanceV03, error) {
	return newProvenanceV03(concept, "remove_verification", verification.By, verification.At)
}

func newProvenanceV03(id bundle.ConceptID, kind, by, at string) (ProvenanceV03, error) {
	op := ProvenanceV03{concept: id, kind: kind, by: by, at: at}
	if err := op.validate(); err != nil {
		return ProvenanceV03{}, err
	}
	return op, nil
}

// Fields exposes the canonical intent without changing the input date form.
func (o ProvenanceV03) Fields() (concept bundle.ConceptID, kind, by, at string) {
	return o.concept, o.kind, o.by, o.at
}

func (ProvenanceV03) operation() {}

func (o ProvenanceV03) validate() error {
	if err := validateSemanticConceptID(o.concept); err != nil {
		return err
	}
	valid, _ := bundle.ValidActorV03Context(context.Background(), o.by)
	if !valid {
		return invalidOperation("invalid document actor")
	}
	switch o.kind {
	case "set_generated":
		if o.at == "" {
			return nil
		}
		return validateTemporalForProfile("generated.at", o.at, bundle.TemporalProfileV03)
	case "ensure_verification", "remove_verification":
		return validateTemporalForProfile("verified.at", o.at, bundle.TemporalProfileV03)
	default:
		return invalidOperation("invalid OKF 0.3 provenance operation")
	}
}

func (o ProvenanceV03) appendCanonical(e *canonicalEncoder) {
	e.string("provenance_v03")
	e.string(o.kind)
	e.string(o.concept.String())
	e.string(o.by)
	e.string(o.at)
}
