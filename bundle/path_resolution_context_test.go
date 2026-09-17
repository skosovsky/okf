package bundle

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestPathAndResolveOwnersCancelInsideAttackerInput(t *testing.T) {
	large := strings.Repeat("a", 2<<20)
	tests := []struct {
		name string
		act  func(context.Context) (any, error)
		zero any
	}{
		{name: "revision validation", act: func(ctx context.Context) (any, error) { return true, ValidateRevisionPathContext(ctx, large+".md") }, zero: true},
		{name: "source normalization", act: func(ctx context.Context) (any, error) { return normalizeSourcePathContext(ctx, large+".md") }, zero: ""},
		{name: "absolute resolve", act: func(ctx context.Context) (any, error) {
			id, _, err := (Link{Kind: LinkAbsolute, Target: "/" + large + ".md"}).ResolveContext(ctx, ConceptID{segments: []string{"source"}})
			return id, err
		}, zero: ConceptID{}},
		{name: "relative many segments", act: func(ctx context.Context) (any, error) {
			id, _, err := (Link{Kind: LinkRelative, Target: strings.Repeat("segment/", 1<<17) + "target.md"}).ResolveContext(ctx, ConceptID{segments: []string{"root", "source"}})
			return id, err
		}, zero: ConceptID{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
			_, err := test.act(probe)
			checks := probe.checks.Load()
			if err != nil || checks < 16 {
				t.Fatalf("probe=%v/%d", err, checks)
			}
			got, gotErr := test.act(&cancelAfterErrChecksContext{allowed: checks / 2})
			if !errors.Is(gotErr, context.Canceled) || !reflect.DeepEqual(got, test.zero) {
				t.Fatalf("cancel=(%#v,%v)", got, gotErr)
			}
		})
	}
}

func TestPathResolutionBackgroundParity(t *testing.T) {
	paths := []string{"a.md", "nested/a b.bin", "../bad", ".okf/x", "bad\\name"}
	for _, path := range paths {
		legacy := ValidateRevisionPath(path)
		got := ValidateRevisionPathContext(context.Background(), path)
		if !sameErrorText(got, legacy) {
			t.Fatalf("path %q got=%v legacy=%v", path, got, legacy)
		}
	}
	links := []Link{{Kind: LinkAbsolute, Target: "/a/b.md#part"}, {Kind: LinkRelative, Target: "../target.md?x"}}
	for _, link := range links {
		legacyID, legacyOK := link.Resolve(ConceptID{segments: []string{"root", "source"}})
		gotID, gotOK, err := link.ResolveContext(context.Background(), ConceptID{segments: []string{"root", "source"}})
		if err != nil || gotOK != legacyOK || !reflect.DeepEqual(gotID, legacyID) {
			t.Fatalf("resolve=(%#v,%v,%v) legacy=(%#v,%v)", gotID, gotOK, err, legacyID, legacyOK)
		}
	}
}
