package bundle

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestVerificationStatesCancelInsideLargeScalarActorAndDate(t *testing.T) {
	large := strings.Repeat("a", 2<<20)
	tests := []struct {
		name string
		yaml string
	}{
		{
			name: "raw non-string scalar extraction",
			yaml: "verified:\n  - by: !!int " + strings.Repeat("7", 2<<20) +
				"\n    at: 2026-08-07T01:02:03Z\n",
		},
		{
			name: "valid actor scan",
			yaml: "verified:\n  - by: human:" + large +
				"\n    at: 2026-08-07T01:02:03Z\n",
		},
		{
			name: "date fractional scan and UTC parse",
			yaml: "verified:\n  - by: process:runner/v1\n    at: 2026-08-07T01:02:03." +
				strings.Repeat("1", 2<<20) + "+00:00\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			frontmatter, err := ParseFrontmatter(test.yaml)
			if err != nil {
				t.Fatal(err)
			}
			legacy := frontmatter.VerificationStates()
			probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
			background, backgroundErr := frontmatter.VerificationStatesContext(probe)
			checks := probe.checks.Load()
			if backgroundErr != nil || !reflect.DeepEqual(background, legacy) || checks < 16 {
				t.Fatalf("Background parity = (%#v, %v), legacy=%#v checks=%d", background, backgroundErr, legacy, checks)
			}
			cancelInside := &cancelAfterErrChecksContext{allowed: checks / 2}

			// Act.
			got, gotErr := frontmatter.VerificationStatesContext(cancelInside)

			// Assert.
			if !errors.Is(gotErr, context.Canceled) || got != nil {
				t.Fatalf("canceled states = (%#v, %v), want nil/context.Canceled", got, gotErr)
			}
		})
	}
}

func TestVerificationBackgroundParityNilShapeAndOwnership(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{name: "absent", yaml: "type: Note\n"},
		{name: "empty list", yaml: "verified: []\n"},
		{name: "non-mapping retained", yaml: "verified: [wrong]\n"},
		{
			name: "valid and malformed",
			yaml: "verified:\n" +
				"  - {by: process:runner/v1, at: 2026-08-07T01:02:03+00:00}\n" +
				"  - {by: human:sergey, at: malformed}\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			frontmatter, err := ParseFrontmatter(test.yaml)
			if err != nil {
				t.Fatal(err)
			}
			document := NewDocument(frontmatter, "")
			legacy := frontmatter.VerificationStates()

			// Act.
			got, gotErr := frontmatter.VerificationStatesContext(context.Background())
			documentGot, documentErr := document.VerificationStatesContext(context.Background())

			// Assert.
			if gotErr != nil || documentErr != nil || !reflect.DeepEqual(got, legacy) ||
				!reflect.DeepEqual(documentGot, legacy) {
				t.Fatalf("Background parity: frontmatter=(%#v,%v), document=(%#v,%v), legacy=%#v",
					got, gotErr, documentGot, documentErr, legacy)
			}
			if legacy == nil && (got != nil || documentGot != nil) {
				t.Fatalf("nil shape changed: frontmatter=%#v document=%#v", got, documentGot)
			}
			if len(got) > 0 {
				got[0].RawBy = "mutated"
				got[0].RawAt = "mutated"
				got[0].Value.By = "mutated"
				again, againErr := frontmatter.VerificationStatesContext(context.Background())
				if againErr != nil || !reflect.DeepEqual(again, legacy) {
					t.Fatalf("caller mutation leaked: again=(%#v,%v), legacy=%#v", again, againErr, legacy)
				}
			}
		})
	}
}

func TestVerificationDateCanonicalUTCAndActorWrapperParity(t *testing.T) {
	// Arrange.
	actor := "human:" + strings.Repeat("sergey", 1<<10)
	rawDate := "2026-08-07T01:02:03." + strings.Repeat("1", 1<<10) + "+00:00"

	// Act.
	actorContext, actorErr := validActorContext(context.Background(), actor)
	dateContext, dateErr := parseDateTimeValueContext(context.Background(), rawDate, true)
	actorLegacy := ValidActor(actor)
	dateLegacy := parseDateTimeValue(rawDate, true)

	// Assert.
	if actorErr != nil || actorContext != actorLegacy {
		t.Fatalf("actor parity = (%v,%v), legacy=%v", actorContext, actorErr, actorLegacy)
	}
	if dateErr != nil || dateContext.State != dateLegacy.State || dateContext.Raw != dateLegacy.Raw ||
		dateContext.State == TemporalValid && !dateContext.Time.Equal(dateLegacy.Time) {
		t.Fatalf("date parity = (%#v,%v), legacy=%#v", dateContext, dateErr, dateLegacy)
	}
}
