package mutation

import (
	"reflect"
	"testing"

	"github.com/skosovsky/okf/bundle"
)

func TestOKF03DetectsChangedOpaqueAliasesAndTracksEntryIdentity(t *testing.T) {
	// Arrange.
	previous, err := bundle.ParseFrontmatter("title: &heading Old\nx-mirror: *heading\n" +
		"sources: [{id: a, resource: 'urn:a', x-owner: ops}, {id: b, resource: 'urn:b'}]\n")
	if err != nil {
		t.Fatal(err)
	}
	next, err := bundle.ParseFrontmatter("title: &heading New\nx-mirror: *heading\n" +
		"sources: [{id: b, resource: 'urn:b'}, {id: a, resource: 'urn:a2', x-owner: ops}]\n")
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	lost, err := LostExtensionsV03(t.Context(), previous, next)

	// Assert.
	if err != nil || !reflect.DeepEqual(lost, []string{"x-mirror"}) {
		t.Fatalf("lost=%v err=%v", lost, err)
	}
}

func TestOKF03ReplacementPreservesUnknownFragmentMetadata(t *testing.T) {
	before, err := bundle.ParseFrontmatter("parts: [{id: a, x-owner: ops}, {id: b}]\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		raw  string
		want []string
	}{
		{"parts: [{id: moved}, {id: a, x-owner: ops}]\n", nil},
		{"parts: [{id: a, x-owner: changed}, {id: b}]\n", []string{"parts[0].x-owner"}},
	} {
		after, err := bundle.ParseFrontmatter(item.raw)
		if err != nil {
			t.Fatal(err)
		}
		lost, err := LostExtensionsV03(t.Context(), before, after)
		if err != nil || !reflect.DeepEqual(lost, item.want) {
			t.Fatalf("lost=%v, want %v, err=%v", lost, item.want, err)
		}
	}
}
