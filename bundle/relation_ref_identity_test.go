package bundle

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func TestRelationRefCanonicalEscapingRoundTripsWithoutChangingOrdinaryWire(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		raw          string
		wantID       string
		wantFragment string
	}{
		{name: "ordinary root", raw: "source", wantID: "source"},
		{name: "legacy delimiter", raw: "source#part", wantID: "source", wantFragment: "part"},
		{name: "fragment backslash unchanged", raw: `source#part\leaf`, wantID: "source", wantFragment: `part\leaf`},
		{name: "escaped concept hash", raw: `source\#part`, wantID: "source#part"},
		{name: "escaped concept hash and fragment", raw: `source\#part#frag`, wantID: "source#part", wantFragment: "frag"},
		{name: "multiple escaped concept hashes", raw: `source\#part\#leaf#frag`, wantID: "source#part#leaf", wantFragment: "frag"},
		{name: "unicode concept and fragment", raw: `東京\#売上#明細`, wantID: "東京#売上", wantFragment: "明細"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			raw := test.raw

			// Act.
			ref, err := ParseRelationRef(raw)
			validateErr := ValidateRelationRef(ref)

			// Assert.
			if err != nil || validateErr != nil {
				t.Fatalf("ParseRelationRef(%q)/ValidateRelationRef() errors = %v/%v", raw, err, validateErr)
			}
			if ref.ID.String() != test.wantID || ref.Fragment != test.wantFragment {
				t.Fatalf("ParseRelationRef(%q) = ID %q fragment %q, want %q/%q",
					raw, ref.ID.String(), ref.Fragment, test.wantID, test.wantFragment)
			}
			if got := ref.String(); got != raw {
				t.Fatalf("RelationRef.String() = %q, want byte-identical %q", got, raw)
			}
		})
	}
}

func TestRelationRefRejectsStrayAndNonCanonicalConceptEscapes(t *testing.T) {
	t.Parallel()

	invalid := []string{
		`source\part`,
		`source\`,
		`source\\#part`,
		`source\#part\leaf`,
		`source#part#extra`,
		`source\##frag#extra`,
		`source#`,
		`source\#part#`,
		`source//part`,
	}
	for _, raw := range invalid {
		raw := raw
		t.Run(fmt.Sprintf("%q", raw), func(t *testing.T) {
			t.Parallel()

			// Arrange.
			input := raw

			// Act.
			_, err := ParseRelationRef(input)

			// Assert.
			if err == nil {
				t.Fatalf("ParseRelationRef(%q) unexpectedly succeeded", input)
			}
		})
	}
}

func TestRelationRefValidateGuaranteesCanonicalParseRoundTrip(t *testing.T) {
	t.Parallel()

	// Arrange.
	valid := []RelationRef{
		{ID: mustParseConceptID(t, "source")},
		{ID: mustParseConceptID(t, "source#part")},
		{ID: mustParseConceptID(t, "source#part"), Fragment: `leaf\value`},
		{ID: mustParseConceptID(t, "東京#売上"), Fragment: "明細"},
	}

	// Act and assert.
	for _, ref := range valid {
		if err := ValidateRelationRef(ref); err != nil {
			t.Fatalf("ValidateRelationRef(%#v) error = %v", ref, err)
		}
		parsed, err := ParseRelationRef(ref.String())
		if err != nil {
			t.Fatalf("ParseRelationRef(%q) error = %v", ref.String(), err)
		}
		if compareRelationRefs(parsed, ref) != 0 {
			t.Fatalf("round trip = %#v, want %#v", parsed, ref)
		}
	}

	invalid := []RelationRef{
		{ID: ConceptID{segments: []string{"source\\part"}}},
		{ID: ConceptID{segments: []string{"source\x00part"}}},
		{ID: mustParseConceptID(t, "source.md")},
		{ID: mustParseConceptID(t, "urn:source")},
	}
	for _, ref := range invalid {
		if err := ValidateRelationRef(ref); err == nil {
			t.Fatalf("ValidateRelationRef(%#v) unexpectedly succeeded", ref)
		}
	}
}

func TestBundleRelationRefStructuralIdentityPermutationAndDuplicates(t *testing.T) {
	t.Parallel()

	// Arrange.
	left := loadRelationRefCollisionBundle(t, []string{`source\#part`, "source#part", `source\#part`})
	right := loadRelationRefCollisionBundle(t, []string{"source#part", `source\#part`, `source\#part`})
	rootID := mustParseConceptID(t, "source#part")
	fragmentID := mustParseConceptID(t, "source")
	rootRef := RelationRef{ID: rootID}
	fragmentRef := RelationRef{ID: fragmentID, Fragment: "part"}

	// Act.
	leftRootIncoming := left.SemanticLinksTo(rootRef)
	leftFragmentIncoming := left.SemanticLinksTo(fragmentRef)
	leftRootImpact := left.ReverseImpactConcept(rootID)
	leftFragmentImpact := left.ReverseImpactConcept(fragmentID)
	rightRootIncoming := right.SemanticLinksTo(rootRef)
	rightFragmentIncoming := right.SemanticLinksTo(fragmentRef)
	rightRootImpact := right.ReverseImpactConcept(rootID)
	rightFragmentImpact := right.ReverseImpactConcept(fragmentID)

	// Assert.
	if rootRef.String() != `source\#part` || fragmentRef.String() != "source#part" {
		t.Fatalf("adversarial wire spellings = %q/%q", rootRef.String(), fragmentRef.String())
	}
	if !left.TargetExists(rootRef) || !left.TargetExists(fragmentRef) {
		t.Fatal("both structurally distinct adversarial targets must exist")
	}
	if len(leftRootIncoming) != 2 || len(leftFragmentIncoming) != 1 {
		t.Fatalf("incoming duplicate exactness = root %d fragment %d, want 2/1",
			len(leftRootIncoming), len(leftFragmentIncoming))
	}
	if len(leftRootImpact) != 1 || len(leftFragmentImpact) != 1 {
		t.Fatalf("reverse-impact exact dedupe = root %d fragment %d, want 1/1",
			len(leftRootImpact), len(leftFragmentImpact))
	}
	if !reflect.DeepEqual(leftRootIncoming, rightRootIncoming) ||
		!reflect.DeepEqual(leftFragmentIncoming, rightFragmentIncoming) ||
		!reflect.DeepEqual(leftRootImpact, rightRootImpact) ||
		!reflect.DeepEqual(leftFragmentImpact, rightFragmentImpact) {
		t.Fatal("semantic index changed under target declaration permutation")
	}
	for _, relation := range leftRootIncoming {
		if compareRelationRefs(relation.Target, rootRef) != 0 {
			t.Fatalf("root incoming leaked colliding fragment target: %#v", relation.Target)
		}
	}
	if compareRelationRefs(leftFragmentIncoming[0].Target, fragmentRef) != 0 {
		t.Fatalf("fragment incoming leaked colliding root target: %#v", leftFragmentIncoming[0].Target)
	}
}

func TestBundleRelationRefStructuralIdentityConcurrentReads(t *testing.T) {
	t.Parallel()

	// Arrange.
	loaded := loadRelationRefCollisionBundle(t, []string{`source\#part`, "source#part", `source\#part`})
	rootRef := RelationRef{ID: mustParseConceptID(t, "source#part")}
	fragmentRef := RelationRef{ID: mustParseConceptID(t, "source"), Fragment: "part"}
	const workers = 32
	const iterations = 64
	errs := make(chan error, workers)
	var wait sync.WaitGroup

	// Act.
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for iteration := 0; iteration < iterations; iteration++ {
				rootIncoming := loaded.SemanticLinksTo(rootRef)
				fragmentIncoming := loaded.SemanticLinksTo(fragmentRef)
				rootImpact := loaded.ReverseImpactConcept(rootRef.ID)
				fragmentImpact := loaded.ReverseImpactFragment(fragmentRef.ID, fragmentRef.Fragment)
				if len(rootIncoming) != 2 || len(fragmentIncoming) != 1 ||
					len(rootImpact) != 1 || len(fragmentImpact) != 1 ||
					compareRelationRefs(rootIncoming[0].Target, rootRef) != 0 ||
					compareRelationRefs(fragmentIncoming[0].Target, fragmentRef) != 0 {
					errs <- fmt.Errorf("iteration %d returned mixed structural identities", iteration)
					return
				}
			}
		}()
	}
	wait.Wait()
	close(errs)

	// Assert.
	for err := range errs {
		t.Fatal(err)
	}
}

func loadRelationRefCollisionBundle(t *testing.T, targets []string) *Bundle {
	t.Helper()

	root := t.TempDir()
	writeFile(t, root, "source#part.md", "---\ntype: Note\n---\nHashed concept.\n")
	writeFile(t, root, "source.md", "---\ntype: Note\nparts:\n  - id: part\n---\nFragment owner.\n")
	var relations string
	for _, target := range targets {
		relations += "    - target: '" + target + "'\n"
	}
	writeFile(t, root, "consumer.md", "---\ntype: Note\nrelations:\n  depends_on:\n"+relations+"---\nConsumer.\n")

	loaded, err := LoadBundle(root)
	if err != nil {
		t.Fatal(err)
	}
	if parseErrors := loaded.ParseErrors(); len(parseErrors) != 0 {
		t.Fatalf("ParseErrors() = %#v", parseErrors)
	}
	return loaded
}
