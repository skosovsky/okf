package store

import (
	"sort"
	"strings"

	"github.com/skosovsky/okf/bundle"
)

// MigrationV03PathChoice selects a captured target for a frontmatter field or
// Markdown destination. Markdown fields use markdown[N] in source order.
type MigrationV03PathChoice struct {
	Concept string `json:"concept"`
	Field   string `json:"field"`
	Target  string `json:"target"`
}

// MigrateV02ToV03 requests a lossless, explicit upgrade to OKF 0.3.
type MigrateV02ToV03 struct {
	AssumeV02 bool
	choices   []MigrationV03PathChoice
}

func NewMigrateV02ToV03(assumeV02 bool, choices []MigrationV03PathChoice) (MigrateV02ToV03, error) {
	op := MigrateV02ToV03{AssumeV02: assumeV02, choices: append([]MigrationV03PathChoice(nil), choices...)}
	sort.Slice(op.choices, func(i, j int) bool {
		left, right := op.choices[i], op.choices[j]
		if left.Concept != right.Concept {
			return left.Concept < right.Concept
		}
		return left.Field < right.Field
	})
	return op, op.validate()
}

func (o MigrateV02ToV03) PathChoices() []MigrationV03PathChoice {
	return append([]MigrationV03PathChoice(nil), o.choices...)
}

func (MigrateV02ToV03) operation() {}

func (o MigrateV02ToV03) validate() error {
	for i, choice := range o.choices {
		id, err := bundle.ParseConceptID(choice.Concept)
		if err != nil || id.String() != choice.Concept || strings.TrimSpace(choice.Field) != choice.Field ||
			choice.Field == "" || bundle.ValidateRevisionPath(choice.Target) != nil {
			return invalidOperation("invalid OKF 0.3 migration path choice")
		}
		if i > 0 && o.choices[i-1].Concept == choice.Concept && o.choices[i-1].Field == choice.Field {
			return invalidOperation("duplicate OKF 0.3 migration path choice")
		}
	}
	return nil
}

func (o MigrateV02ToV03) appendCanonical(e *canonicalEncoder) {
	e.string("migrate_v02_to_v03")
	// Keep a fixed-width discriminator in the canonical representation.
	if o.AssumeV02 {
		e.string("assume-v02")
	} else {
		e.string("declared-v02")
	}
	// Canonical field lengths and the operation's item count delimit the list.
	e.uint(uint64(len(o.choices)))
	for _, choice := range o.choices {
		e.string(choice.Concept)
		e.string(choice.Field)
		e.string(choice.Target)
	}
}
