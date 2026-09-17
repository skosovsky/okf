package bundle

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestAttributionOwnersCancelInsideAttackerStrings(t *testing.T) {
	large := strings.Repeat("label", 512<<10)
	tests := []struct {
		name string
		act  func(context.Context) (any, error)
		zero any
	}{
		{name: "normalize label", act: func(ctx context.Context) (any, error) { return normalizeFootnoteLabelContext(ctx, large) }, zero: ""},
		{name: "body copy", act: func(ctx context.Context) (any, error) { return bytesFromStringContext(ctx, large) }, zero: []byte(nil)},
		{name: "trimmed definition copy", act: func(ctx context.Context) (any, error) {
			return attributionTrimmedBytesContext(ctx, []byte(" "+large+" "))
		}, zero: ""},
		{name: "raw reference copy", act: func(ctx context.Context) (any, error) { return attributionBytesContext(ctx, []byte(large)) }, zero: ""},
		{name: "provenance comparator", act: func(ctx context.Context) (any, error) {
			return compareProvenanceSourcesContext(ctx, ProvenanceSource{ID: large + "a"}, ProvenanceSource{ID: large + "b"})
		}, zero: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
			_, probeErr := test.act(probe)
			checks := probe.checks.Load()
			if probeErr != nil || checks < 16 {
				t.Fatalf("probe err=%v checks=%d", probeErr, checks)
			}
			got, gotErr := test.act(&cancelAfterErrChecksContext{allowed: checks / 2})
			if !errors.Is(gotErr, context.Canceled) || !reflect.DeepEqual(got, test.zero) {
				t.Fatalf("cancel=(%#v,%v), want (%#v,context.Canceled)", got, gotErr, test.zero)
			}
		})
	}
}

func TestAttributionsContextCancelsInsideRealNormalizationAndSort(t *testing.T) {
	large := strings.Repeat("a", 2<<20)
	frontmatter, err := ParseFrontmatter("sources:\n  - id: " + large + "b\n    resource: source-b.md\n  - id: " + large + "a\n    resource: source-a.md\n")
	if err != nil {
		t.Fatal(err)
	}
	document := NewDocument(frontmatter, "body\n")

	probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	baseline, probeErr := document.AttributionsContext(probe)
	checks := probe.checks.Load()
	if probeErr != nil || len(baseline) != 2 || checks < 64 {
		t.Fatalf("probe=(%d,%v), checks=%d", len(baseline), probeErr, checks)
	}
	got, gotErr := document.AttributionsContext(&cancelAfterErrChecksContext{allowed: checks - checks/3})
	if got != nil || !errors.Is(gotErr, context.Canceled) {
		t.Fatalf("AttributionsContext=(%#v,%v), want nil/context.Canceled", got, gotErr)
	}
}

func TestAttributionsContextDefinitionReferenceAndBackgroundParity(t *testing.T) {
	frontmatter, err := ParseFrontmatter("sources:\n  - id: alpha\n    resource: source.md\n")
	if err != nil {
		t.Fatal(err)
	}
	document := NewDocument(frontmatter, "claim[^alpha]\n\n[^alpha]: evidence\n")
	legacy := document.Attributions()
	got, gotErr := document.AttributionsContext(context.Background())
	if gotErr != nil || !reflect.DeepEqual(got, legacy) {
		t.Fatalf("attributions=(%#v,%v), legacy=%#v", got, gotErr, legacy)
	}
	if len(got) != 1 || len(got[0].References) != 1 || len(got[0].Definitions) != 1 {
		t.Fatalf("shape=%#v", got)
	}
	got[0].Sources[0].ID = "mutated"
	again, err := document.AttributionsContext(context.Background())
	if err != nil || again[0].Sources[0].ID != "alpha" {
		t.Fatalf("ownership=(%#v,%v)", again, err)
	}
}
