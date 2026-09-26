package store

import (
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/skosovsky/okf/bundle"
)

// TemporalFieldRewrite identifies one exact legacy scalar and the caller's
// explicit instant policy for it. Path is relative to a concept frontmatter.
type TemporalFieldRewrite struct {
	Path string
	From string
	To   string
}

var temporalFieldPath = regexp.MustCompile(`^(stale_after|usage_window\.(from|to)|sources\[[0-9]+\]\.(last_modified|usage_window\.(from|to)))$`)

// UpgradeTemporalConcept is an additive, revision-bound operation. Its field
// paths are positional within the exact base revision and each rewrite checks
// the original scalar again before mutation.
type UpgradeTemporalConcept struct {
	Concept  bundle.ConceptID
	rewrites []TemporalFieldRewrite
}

func NewUpgradeTemporalConcept(concept bundle.ConceptID, rewrites []TemporalFieldRewrite) (UpgradeTemporalConcept, error) {
	op := UpgradeTemporalConcept{Concept: concept, rewrites: append([]TemporalFieldRewrite(nil), rewrites...)}
	sort.Slice(op.rewrites, func(i, j int) bool { return op.rewrites[i].Path < op.rewrites[j].Path })
	if err := op.validate(); err != nil {
		return UpgradeTemporalConcept{}, err
	}
	return op, nil
}

func (o UpgradeTemporalConcept) Rewrites() []TemporalFieldRewrite {
	return append([]TemporalFieldRewrite(nil), o.rewrites...)
}

func (UpgradeTemporalConcept) operation() {}

func (o UpgradeTemporalConcept) validate() error {
	if err := bundle.ValidateConceptID(o.Concept); err != nil {
		return invalidOperation("invalid temporal upgrade concept")
	}
	if len(o.rewrites) == 0 {
		return invalidOperation("temporal upgrade requires rewrites")
	}
	last := ""
	for _, field := range o.rewrites {
		if !temporalFieldPath.MatchString(field.Path) || field.Path <= last {
			return invalidOperation("invalid or duplicate temporal field path")
		}
		last = field.Path
		date, err := time.Parse("2006-01-02", field.From)
		if err != nil || date.Format("2006-01-02") != field.From {
			return invalidOperation("temporal upgrade source must be an exact calendar date")
		}
		if _, err := bundle.ParseOffsetDateTime(field.To); err != nil {
			return invalidOperation(fmt.Sprintf("temporal upgrade target for %s must be offset datetime", field.Path))
		}
	}
	return nil
}

func (o UpgradeTemporalConcept) appendCanonical(e *canonicalEncoder) {
	e.string("upgrade_temporal_concept")
	e.string(o.Concept.String())
	e.uint(uint64(len(o.rewrites)))
	for _, field := range o.rewrites {
		e.string(field.Path)
		e.string(field.From)
		e.string(field.To)
	}
}
