package bundle

import (
	"errors"
	"reflect"
	"testing"
)

func TestRelationFragmentPublicContract_UTF8(t *testing.T) {
	invalid := []struct {
		name, fragment string
	}{
		{name: "leading continuation", fragment: "\x80fragment"},
		{name: "trailing incomplete sequence", fragment: "fragment\xc2"},
		{name: "invalid middle sequence", fragment: "fragment\xe2\x28\xa1"},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			ref := RelationRef{ID: mustParseConceptID(t, "concept"), Fragment: tt.fragment}

			// Act.
			fragmentErr := ValidateRelationFragment(tt.fragment)
			parseErr := error(nil)
			_, parseErr = ParseRelationRef("concept#" + tt.fragment)
			refErr := ValidateRelationRef(ref)

			// Assert.
			if !errors.Is(fragmentErr, ErrInvalidRelationFragment) {
				t.Fatalf("ValidateRelationFragment() error = %v, want ErrInvalidRelationFragment", fragmentErr)
			}
			if !errors.Is(parseErr, ErrInvalidRelationRef) {
				t.Fatalf("ParseRelationRef() error = %v, want ErrInvalidRelationRef", parseErr)
			}
			if !errors.Is(refErr, ErrInvalidRelationRef) {
				t.Fatalf("ValidateRelationRef() error = %v, want ErrInvalidRelationRef", refErr)
			}
		})
	}

	for _, fragment := range []string{"東京", "café", "emoji-😀"} {
		t.Run("valid "+fragment, func(t *testing.T) {
			// Arrange.
			ref := RelationRef{ID: mustParseConceptID(t, "concept"), Fragment: fragment}

			// Act.
			fragmentErr := ValidateRelationFragment(fragment)
			parsed, parseErr := ParseRelationRef("concept#" + fragment)
			refErr := ValidateRelationRef(ref)

			// Assert.
			if fragmentErr != nil || parseErr != nil || refErr != nil {
				t.Fatalf("valid Unicode errors: fragment=%v parse=%v ref=%v", fragmentErr, parseErr, refErr)
			}
			if parsed.String() != ref.String() {
				t.Fatalf("ParseRelationRef() = %#v, want %#v", parsed, ref)
			}
		})
	}
}

func TestReverseImpactConcept_IncludesRootAndCanonicalFragmentsDeterministically(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeFile(t, root, "a.md", "---\n"+
		"type: Note\n"+
		"parts:\n"+
		"  - id: first\n"+
		"  - id: second\n"+
		"---\nA\n")
	writeFile(t, root, "b.md", "---\n"+
		"type: Note\n"+
		"relations:\n"+
		"  depends_on:\n"+
		"    - target: a\n"+
		"    - target: a#first\n"+
		"    - target: a#first\n"+
		"---\nB\n")
	writeFile(t, root, "c.md", "---\n"+
		"type: Note\n"+
		"parts:\n"+
		"  - id: source\n"+
		"    relations:\n"+
		"      uses:\n"+
		"        - target: a#second\n"+
		"---\nC\n")
	loaded, err := LoadBundle(root)
	if err != nil {
		t.Fatal(err)
	}
	a := mustParseConceptID(t, "a")

	// Act.
	impact := loaded.ReverseImpactConcept(a)

	// Assert.
	want := []struct{ source, target string }{
		{source: "b", target: "a"},
		{source: "b", target: "a#first"},
		{source: "c#source", target: "a#second"},
	}
	if len(impact) != len(want) {
		t.Fatalf("impact = %#v, want %d relations", impact, len(want))
	}
	for i, relation := range impact {
		if relation.Source.String() != want[i].source || relation.Target.String() != want[i].target {
			t.Fatalf("impact[%d] = %s -> %s, want %s -> %s", i, relation.Source, relation.Target, want[i].source, want[i].target)
		}
	}
}

func TestBundleSemanticLinksFrom(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeFile(t, root, "a.md", "---\n"+
		"type: Note\n"+
		"id: top-id\n"+
		"anchor: top-anchor\n"+
		"schema:\n"+
		"  fields:\n"+
		"    - id: field1\n"+
		"      name: display_field1\n"+
		"      relations:\n"+
		"        writes_to:\n"+
		"          - target: b#col-2\n"+
		"    - anchor: anchor_only\n"+
		"      relations:\n"+
		"        reads_from:\n"+
		"          - target: b#anchor-target\n"+
		"    - name: display_only\n"+
		"      relations:\n"+
		"        writes_to:\n"+
		"          - target: b#ignored\n"+
		"    - id: \"bad\\x7fsource\"\n"+
		"      relations:\n"+
		"        writes_to:\n"+
		"          - target: b#ignored-del-source\n"+
		"    - anchor: \"bad\\x7fanchor\"\n"+
		"      relations:\n"+
		"        writes_to:\n"+
		"          - target: b#ignored-del-anchor\n"+
		"    - id: bad#id\n"+
		"      anchor: fallback_anchor\n"+
		"      relations:\n"+
		"        reads_from:\n"+
		"          - target: b\n"+
		"    - id: chosen_id\n"+
		"      anchor: ignored_anchor\n"+
		"      relations:\n"+
		"        maps_to:\n"+
		"          - target: missing#col\n"+
		"    - id: parent\n"+
		"      constraints:\n"+
		"        relations:\n"+
		"          depends_on:\n"+
		"            - target: b#not-inherited\n"+
		"relations:\n"+
		"  depends_on:\n"+
		"    - target: b#section-1\n"+
		"    - target: b#section-1\n"+
		"  traces:\n"+
		"    - target: b\n"+
		"      relations:\n"+
		"        should_ignore:\n"+
		"          - target: b#nested-in-relation-item\n"+
		"  bad-type:\n"+
		"    - target: b\n"+
		"  okf:\n"+
		"    - target: b\n"+
		"  bundle:\n"+
		"    - target: b\n"+
		"---\nSee [B](b.md) and [Missing](/missing.md).\n")
	writeFile(t, root, "b.md", "---\ntype: Note\nfields:\n  - id: col-2\n  - anchor: anchor-target\n  - id: section-1\n---\nBody.\n")
	bundle, err := LoadBundle(root)
	if err != nil {
		t.Fatalf("LoadBundle() error = %v", err)
	}
	a := mustParseConceptID(t, "a")

	// Act.
	relations := bundle.SemanticLinksFrom(a)

	// Assert.
	want := []struct {
		source string
		typ    string
		target string
		exists bool
	}{
		{source: "a", typ: "depends_on", target: "b#section-1", exists: true},
		{source: "a", typ: "depends_on", target: "b#section-1", exists: true},
		{source: "a", typ: "traces", target: "b", exists: true},
		{source: "a#anchor_only", typ: "reads_from", target: "b#anchor-target", exists: true},
		{source: "a#fallback_anchor", typ: "reads_from", target: "b", exists: true},
		{source: "a#field1", typ: "writes_to", target: "b#col-2", exists: true},
	}
	if len(relations) != len(want) {
		t.Fatalf("SemanticLinksFrom(a) length = %d, want %d: %#v", len(relations), len(want), relations)
	}
	for i, relation := range relations {
		if relation.Source.String() != want[i].source ||
			relation.Type != want[i].typ ||
			relation.Target.String() != want[i].target ||
			relation.TargetExists != want[i].exists {
			t.Fatalf("relation[%d] = %#v, want source=%s type=%s target=%s exists=%t",
				i, relation, want[i].source, want[i].typ, want[i].target, want[i].exists)
		}
	}
	for _, relation := range relations {
		if relation.Type == "should_ignore" {
			t.Fatalf("SemanticLinksFrom recursed into relation item mapping: %#v", relations)
		}
	}
	if relations[0].Source.String() != "a" {
		t.Fatalf("top-level relation source = %s, want document source a", relations[0].Source)
	}

	relations[0].Type = "mutated"
	if got := bundle.SemanticLinksFrom(a)[0].Type; got != "depends_on" {
		t.Fatalf("SemanticLinksFrom returned mutable backing slice; first type = %q", got)
	}
	links := bundle.LinksFrom(a)
	if len(links) != 2 || links[0].Target.String() != "b" || links[1].Target.String() != "missing" {
		t.Fatalf("LinksFrom(a) = %#v, want Markdown-only links to b and missing", links)
	}
	if got := len(bundle.BrokenLinks()); got != 1 {
		t.Fatalf("len(BrokenLinks()) = %d, want only Markdown broken link", got)
	}
	b := mustParseConceptID(t, "b")
	if got := len(bundle.Backlinks(b)); got != 1 {
		t.Fatalf("len(Backlinks(b)) = %d, want only Markdown backlink", got)
	}
}

func TestNestedRelationsInsideRelationItemsUseNearestCanonicalSource(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeFile(t, root, "a.md", "---\ntype: Note\nrelations:\n  traces:\n    - target: outer\n      id: relation-item\n      anchor: ignored-alias\n      metadata:\n        relations:\n          derives:\n            - target: target\n            - target: target#field\n---\nA\n")
	writeFile(t, root, "outer.md", "---\ntype: Note\n---\nOuter\n")
	writeFile(t, root, "target.md", "---\ntype: Note\nfields:\n  - id: field\n  - id: field\n---\nTarget\n")
	loaded, err := LoadBundle(root)
	if err != nil {
		t.Fatal(err)
	}
	a := mustParseConceptID(t, "a")
	target, err := ParseRelationRef("target")
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	outgoing := loaded.SemanticLinksFrom(a)
	incoming := loaded.SemanticLinksTo(target)
	reverse := loaded.ReverseImpactConcept(target.ID)
	diagnostics := loaded.RelationDiagnostics()

	// Assert.
	if got, want := relationKeys(outgoing), []string{"a|traces|outer", "a#relation-item|derives|target"}; !sameRelationKeys(got, want) {
		t.Fatalf("outgoing = %v, want %v", got, want)
	}
	if got, want := relationKeys(incoming), []string{"a#relation-item|derives|target"}; !sameRelationKeys(got, want) {
		t.Fatalf("incoming = %v, want %v", got, want)
	}
	if got, want := relationKeys(reverse), []string{"a#relation-item|derives|target"}; !sameRelationKeys(got, want) {
		t.Fatalf("reverse impact = %v, want %v", got, want)
	}
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == "missing_or_ambiguous_target_fragment" && diagnostic.RawTarget == "target#field" {
			if got, want := (RelationRef{ID: diagnostic.Source, Fragment: diagnostic.SourceFragment}).String(), "a#relation-item"; got != want {
				t.Fatalf("diagnostic source = %q, want %q", got, want)
			}
			return
		}
	}
	t.Fatalf("diagnostics = %#v, want duplicate fragment diagnostic", diagnostics)
}

func TestSemanticRelationQueries_AreCanonicalAndIndependentOfYAMLOrdering(t *testing.T) {
	// Arrange. The two bundles contain the same edges but use opposite map and
	// sequence orders.
	first := t.TempDir()
	second := t.TempDir()
	writeSemanticPermutationBundle(t, first, false)
	writeSemanticPermutationBundle(t, second, true)
	left, err := LoadBundle(first)
	if err != nil {
		t.Fatal(err)
	}
	right, err := LoadBundle(second)
	if err != nil {
		t.Fatal(err)
	}
	a := mustParseConceptID(t, "a")
	target, err := ParseRelationRef("target")
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	leftOutgoing := left.SemanticLinksFrom(a)
	rightOutgoing := right.SemanticLinksFrom(a)
	leftIncoming := left.SemanticLinksTo(target)
	rightIncoming := right.SemanticLinksTo(target)
	leftReverse := left.ReverseImpactConcept(target.ID)
	rightReverse := right.ReverseImpactConcept(target.ID)

	// Assert.
	if got, want := relationKeys(leftOutgoing), relationKeys(rightOutgoing); !sameRelationKeys(got, want) {
		t.Fatalf("outgoing differs by YAML order: %v != %v", got, want)
	}
	if got, want := relationKeys(leftIncoming), relationKeys(rightIncoming); !sameRelationKeys(got, want) {
		t.Fatalf("incoming differs by YAML order: %v != %v", got, want)
	}
	if got, want := relationKeys(leftReverse), relationKeys(rightReverse); !sameRelationKeys(got, want) {
		t.Fatalf("reverse impact differs by YAML order: %v != %v", got, want)
	}
	want := []string{"a|alpha|target", "a|zeta|target", "a#part|uses|target", "b|depends_on|target"}
	if got := relationKeys(leftReverse); !sameRelationKeys(got, want) {
		t.Fatalf("canonical relation order = %v, want %v", got, want)
	}
	leftIncoming[0].Type = "mutated"
	if got := left.SemanticLinksTo(target)[0].Type; got == "mutated" {
		t.Fatal("SemanticLinksTo returned mutable backing storage")
	}
}

func TestRelationDiagnostics_PreserveInvalidAndReservedScalarRelationTypes(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeFile(t, root, "a.md", "---\ntype: Note\nrelations:\n  bad-type: []\n  okf: []\n  17: []\n---\nA\n")

	// Act.
	loaded, err := LoadBundle(root)
	if err != nil {
		t.Fatal(err)
	}

	// Assert.
	got := map[string]string{}
	for _, diagnostic := range loaded.RelationDiagnostics() {
		if diagnostic.Code == "invalid_relation_type" {
			got[diagnostic.RelationType] = diagnostic.Code
		}
	}
	for _, relationType := range []string{"bad-type", "okf", "17"} {
		if got[relationType] != "invalid_relation_type" {
			t.Fatalf("diagnostics = %#v, missing raw relation type %q", loaded.RelationDiagnostics(), relationType)
		}
	}
}

func TestRelationMappingKeysAndTargetsRequireStringTags(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeFile(t, root, "a.md", "---\ntype: Note\nrelations:\n  true: [{target: target}]\n  false: [{target: target}]\n  null: [{target: target}]\n  17: [{target: target}]\n  2026-07-19: [{target: target}]\n  'true':\n    - target: target\n    - target: true\n    - target: 17\n    - target: null\n---\nA\n")
	writeFile(t, root, "target.md", "---\ntype: Note\n---\nTarget\n")

	// Act.
	loaded, err := LoadBundle(root)
	if err != nil {
		t.Fatal(err)
	}
	a := mustParseConceptID(t, "a")

	// Assert.
	if got, want := relationKeys(loaded.SemanticLinksFrom(a)), []string{"a|true|target"}; !sameRelationKeys(got, want) {
		t.Fatalf("outgoing = %v, want %v", got, want)
	}
	invalidTypes := map[string]bool{}
	targetNotStrings := 0
	for _, diagnostic := range loaded.RelationDiagnostics() {
		if diagnostic.Code == "invalid_relation_type" {
			invalidTypes[diagnostic.RelationType] = true
		}
		if diagnostic.Code == "target_not_string" {
			targetNotStrings++
		}
	}
	for _, raw := range []string{"true", "false", "null", "17", "2026-07-19"} {
		if !invalidTypes[raw] {
			t.Fatalf("diagnostics = %#v, missing invalid relation key %q", loaded.RelationDiagnostics(), raw)
		}
	}
	if targetNotStrings != 3 {
		t.Fatalf("target_not_string diagnostics = %d, want 3: %#v", targetNotStrings, loaded.RelationDiagnostics())
	}
}

func TestSubresourceIndexScopesDuplicateExplicitMappingKeys(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeFile(t, root, "target.md", "---\ntype: Note\nparts:\n  - id: one\n    id: two\n    relations:\n      uses:\n        - target: source\n  - anchor: three\n    anchor: four\n  - id: canonical\n    anchor: alias\n  - id: 17\n    id: later\n---\nTarget\n")
	writeFile(t, root, "source.md", "---\ntype: Note\nrelations:\n  depends_on:\n    - target: target#one\n    - target: target#two\n    - target: target#three\n    - target: target#four\n    - target: target#canonical\n    - target: target#later\n---\nSource\n")

	// Act.
	loaded, err := LoadBundle(root)
	if err != nil {
		t.Fatal(err)
	}
	target := mustParseConceptID(t, "target")
	source := mustParseConceptID(t, "source")

	// Assert. Duplicate identity keys invalidate only their own mappings; valid
	// sibling mappings remain indexed and resolvable.
	if errors := loaded.ParseErrors(); len(errors) != 0 {
		t.Fatalf("ParseErrors() = %#v, want none", errors)
	}
	if got, want := loaded.Subresources(target), []string{"canonical", "later"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Subresources(target) = %v, want %v", got, want)
	}
	if got, want := relationKeys(loaded.SemanticLinksFrom(source)), []string{"source|depends_on|target#canonical", "source|depends_on|target#later"}; !sameRelationKeys(got, want) {
		t.Fatalf("resolved relations = %v, want %v", got, want)
	}
	if got := loaded.SemanticLinksFrom(target); len(got) != 0 {
		t.Fatalf("SemanticLinksFrom(target) = %#v, want no edge from ambiguous nested source", got)
	}

	ambiguous := map[string]bool{}
	missing := map[string]bool{}
	for _, diagnostic := range loaded.RelationDiagnostics() {
		if diagnostic.Code == "ambiguous_fragment" {
			ambiguous[diagnostic.RawTarget] = true
		}
		if diagnostic.Code == "missing_or_ambiguous_target_fragment" {
			missing[diagnostic.RawTarget] = true
		}
	}
	for _, fragment := range []string{"one", "two", "three", "four"} {
		if !ambiguous[fragment] {
			t.Fatalf("diagnostics = %#v, missing ambiguous fragment %q", loaded.RelationDiagnostics(), fragment)
		}
		if !missing["target#"+fragment] {
			t.Fatalf("diagnostics = %#v, missing unresolved target %q", loaded.RelationDiagnostics(), "target#"+fragment)
		}
	}
}

func TestSubresourceIndex_ExcludesFrontmatterRootIdentity(t *testing.T) {
	// Arrange. The root mapping identifies the concept document, not a fragment;
	// nested mappings are the complete fragment namespace.
	root := t.TempDir()
	writeFile(t, root, "a.md", "---\ntype: Note\nid: root-id\nanchor: root-anchor\nparts:\n  - id: nested-id\n  - anchor: nested-anchor\n  - id: duplicate\n  - id: duplicate\n---\nA\n")
	writeFile(t, root, "source.md", "---\ntype: Note\nrelations:\n  uses:\n    - target: a#root-id\n    - target: a#root-anchor\n    - target: a#nested-id\n    - target: a#nested-anchor\n    - target: a#duplicate\n---\nSource\n")

	// Act.
	loaded, err := LoadBundle(root)
	if err != nil {
		t.Fatal(err)
	}
	a := mustParseConceptID(t, "a")
	source := mustParseConceptID(t, "source")

	// Assert.
	if got, want := loaded.Subresources(a), []string{"nested-anchor", "nested-id"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Subresources(a) = %v, want %v", got, want)
	}
	for _, fragment := range []string{"root-id", "root-anchor", "duplicate"} {
		if loaded.FragmentExists(a, fragment) {
			t.Fatalf("FragmentExists(a, %q) = true, want false", fragment)
		}
	}
	if got, want := relationKeys(loaded.SemanticLinksFrom(source)), []string{"source|uses|a#nested-anchor", "source|uses|a#nested-id"}; !sameRelationKeys(got, want) {
		t.Fatalf("resolved relations = %v, want %v", got, want)
	}

	missing := map[string]bool{}
	for _, diagnostic := range loaded.RelationDiagnostics() {
		if diagnostic.Code == "missing_or_ambiguous_target_fragment" {
			missing[diagnostic.RawTarget] = true
		}
		if diagnostic.Code == "anchor_alias" && diagnostic.RawTarget == "root-anchor" {
			t.Fatalf("root anchor was treated as a fragment alias: %#v", diagnostic)
		}
	}
	for _, target := range []string{"a#root-id", "a#root-anchor", "a#duplicate"} {
		if !missing[target] {
			t.Fatalf("diagnostics = %#v, missing unresolved target %q", loaded.RelationDiagnostics(), target)
		}
	}
}

func TestNestedIdentityPolicyScopesDuplicateExplicitMappingKeys(t *testing.T) {
	// Arrange. Every identity occurrence is intentionally explicit: invalid
	// values must not hide a unique fallback, while duplicate valid candidates
	// must never select the first one.
	root := t.TempDir()
	writeFile(t, root, "a.md", "---\ntype: Note\nparts:\n  - id: 17\n    anchor: fallback-anchor\n    relations:\n      uses:\n        - target: target\n  - id: 17\n    id: fallback-id\n    relations:\n      uses:\n        - target: target\n  - id: first\n    id: second\n    anchor: alias\n    relations:\n      uses:\n        - target: target\n  - anchor: first-anchor\n    anchor: second-anchor\n    relations:\n      uses:\n        - target: target\n---\nA\n")
	writeFile(t, root, "target.md", "---\ntype: Note\n---\nTarget\n")

	// Act.
	loaded, err := LoadBundle(root)
	if err != nil {
		t.Fatal(err)
	}
	a := mustParseConceptID(t, "a")

	// Assert. A unique valid fallback remains usable beside an invalid scalar,
	// while multiple valid candidates make only that nested source ambiguous.
	if errors := loaded.ParseErrors(); len(errors) != 0 {
		t.Fatalf("ParseErrors() = %#v, want none", errors)
	}
	if got, want := loaded.Subresources(a), []string{"fallback-anchor", "fallback-id"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Subresources(a) = %v, want %v", got, want)
	}
	if got, want := relationKeys(loaded.SemanticLinksFrom(a)), []string{"a#fallback-anchor|uses|target", "a#fallback-id|uses|target"}; !sameRelationKeys(got, want) {
		t.Fatalf("SemanticLinksFrom(a) = %v, want %v", got, want)
	}

	ambiguous := map[string]bool{}
	invalidSources := 0
	for _, diagnostic := range loaded.RelationDiagnostics() {
		if diagnostic.Code == "ambiguous_fragment" {
			ambiguous[diagnostic.RawTarget] = true
		}
		if diagnostic.Code == "invalid_source" {
			invalidSources++
		}
	}
	for _, fragment := range []string{"first", "second", "first-anchor", "second-anchor"} {
		if !ambiguous[fragment] {
			t.Fatalf("diagnostics = %#v, missing ambiguous fragment %q", loaded.RelationDiagnostics(), fragment)
		}
	}
	if invalidSources != 2 {
		t.Fatalf("invalid_source diagnostics = %d, want 2: %#v", invalidSources, loaded.RelationDiagnostics())
	}
}

func writeSemanticPermutationBundle(t *testing.T, root string, reversed bool) {
	t.Helper()
	if reversed {
		writeFile(t, root, "a.md", "---\ntype: Note\nparts:\n  - id: part\n    relations:\n      uses:\n        - target: target\nrelations:\n  zeta:\n    - target: target\n  alpha:\n    - target: target\n---\nA\n")
		writeFile(t, root, "b.md", "---\ntype: Note\nrelations:\n  depends_on:\n    - target: target\n---\nB\n")
	} else {
		writeFile(t, root, "a.md", "---\ntype: Note\nrelations:\n  alpha:\n    - target: target\n  zeta:\n    - target: target\nparts:\n  - id: part\n    relations:\n      uses:\n        - target: target\n---\nA\n")
		writeFile(t, root, "b.md", "---\ntype: Note\nrelations:\n  depends_on:\n    - target: target\n---\nB\n")
	}
	writeFile(t, root, "target.md", "---\ntype: Note\n---\nTarget\n")
}

func relationKeys(relations []Relation) []string {
	out := make([]string, len(relations))
	for i, relation := range relations {
		out[i] = relation.Source.String() + "|" + relation.Type + "|" + relation.Target.String()
	}
	return out
}

func sameRelationKeys(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestBundleSemanticLinksFromInvalidTargets(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeFile(t, root, "a.md", "---\n"+
		"type: Note\n"+
		"relations:\n"+
		"  depends_on:\n"+
		"    - target: /b.md\n"+
		"    - target: b.md\n"+
		"    - target: tables/orders.md\n"+
		"    - target: ../b\n"+
		"    - target: '#section'\n"+
		"    - target: https://example.com/orders\n"+
		"    - target: urn:orders\n"+
		"    - target: file:/orders\n"+
		"    - target: ssh:orders\n"+
		"    - target: b#\n"+
		"    - target: b#a#c\n"+
		"    - target: \"b#bad\\x7f\"\n"+
		"    - target: 'b# section'\n"+
		"    - target: 'b#section '\n"+
		"    - target: a/\n"+
		"    - target: a//b\n"+
		"    - target: a/./b\n"+
		"    - target: b#ok\n"+
		"---\nBody.\n")
	writeFile(t, root, "b.md", "---\ntype: Note\nfields:\n  - id: ok\n---\nBody.\n")
	bundle, err := LoadBundle(root)
	if err != nil {
		t.Fatalf("LoadBundle() error = %v", err)
	}
	a := mustParseConceptID(t, "a")

	// Act.
	relations := bundle.SemanticLinksFrom(a)

	// Assert.
	if len(relations) != 1 {
		t.Fatalf("SemanticLinksFrom(a) = %#v, want only one valid target", relations)
	}
	if got, want := relations[0].Target.String(), "b#ok"; got != want {
		t.Fatalf("valid relation target = %q, want %q", got, want)
	}
}

func TestBundleSemanticLinksFromMalformedBlocks(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeFile(t, root, "a.md", "---\n"+
		"type: Note\n"+
		"relations:\n"+
		"  depends_on: b\n"+
		"  writes_to:\n"+
		"    - b\n"+
		"    - target: 123\n"+
		"    - note: missing target\n"+
		"schema:\n"+
		"  fields:\n"+
		"    - id: field1\n"+
		"      relations: []\n"+
		"---\nBody.\n")
	bundle, err := LoadBundle(root)
	if err != nil {
		t.Fatalf("LoadBundle() error = %v", err)
	}
	a := mustParseConceptID(t, "a")

	// Act.
	relations := bundle.SemanticLinksFrom(a)

	// Assert.
	if len(relations) != 0 {
		t.Fatalf("SemanticLinksFrom(a) = %#v, want malformed relations ignored", relations)
	}
}

func TestBundleNestedMalformedRelationsReportShapesWithoutValidSource(t *testing.T) {
	// Arrange. Every nested block lacks a valid local id/anchor, so none may
	// become an edge; its own shape errors must still be observable.
	root := t.TempDir()
	writeFile(t, root, "a.md", "---\n"+
		"type: Note\n"+
		"parts:\n"+
		"  - label: no-id\n"+
		"    relations:\n"+
		"      uses: target\n"+
		"  - id: 17\n"+
		"    relations: []\n"+
		"  - anchor: false\n"+
		"    relations:\n"+
		"      true:\n"+
		"        - target: target\n"+
		"  - id: []\n"+
		"    relations:\n"+
		"      uses:\n"+
		"        - nope\n"+
		"        - target: 17\n"+
		"        - target: ''\n"+
		"        - target: 'bad#'\n"+
		"        - target: missing\n"+
		"---\nA\n")
	writeFile(t, root, "target.md", "---\ntype: Note\n---\nTarget\n")

	// Act.
	loaded, err := LoadBundle(root)
	if err != nil {
		t.Fatal(err)
	}
	a := mustParseConceptID(t, "a")

	// Assert.
	if got := loaded.SemanticLinksFrom(a); len(got) != 0 {
		t.Fatalf("SemanticLinksFrom(a) = %#v, want no edges from invalid nested sources", got)
	}
	var got []string
	for _, diagnostic := range loaded.RelationDiagnostics() {
		got = append(got, diagnostic.Code+":"+diagnostic.RelationType+":"+diagnostic.RawTarget)
	}
	want := []string{
		"invalid_source::",
		"invalid_source::",
		"invalid_source::",
		"invalid_source::",
		"relations_not_mapping::",
		"invalid_fragment:anchor:false",
		"invalid_fragment:id:",
		"invalid_fragment:id:17",
		"invalid_relation_type:true:",
		"empty_target:uses:",
		"relation_item_not_mapping:uses:",
		"relation_not_sequence:uses:",
		"target_not_string:uses:17",
		"invalid_target_ref:uses:bad#",
		"missing_target_concept:uses:missing",
	}
	if !sameDiagnosticStrings(got, want) {
		t.Fatalf("diagnostics = %v, want %v", got, want)
	}
}

func TestBundleInvalidNestedIdentityDoesNotInheritParentSource(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeFile(t, root, "a.md", "---\ntype: Note\nparts:\n  - id: parent\n    children:\n      - id: 17\n        relations:\n          uses:\n            - target: target\n      - id: duplicate\n        id: duplicate-again\n        relations:\n          uses:\n            - target: target\n---\nA\n")
	writeFile(t, root, "target.md", "---\ntype: Note\n---\nTarget\n")

	// Act.
	loaded, err := LoadBundle(root)
	if err != nil {
		t.Fatal(err)
	}
	a := mustParseConceptID(t, "a")

	// Assert. Invalid child identities do not inherit the valid parent's source
	// and do not prevent the parent itself from remaining indexed.
	if errors := loaded.ParseErrors(); len(errors) != 0 {
		t.Fatalf("ParseErrors() = %#v, want none", errors)
	}
	if got, want := loaded.Subresources(a), []string{"parent"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Subresources(a) = %v, want %v", got, want)
	}
	if got := loaded.SemanticLinksFrom(a); len(got) != 0 {
		t.Fatalf("SemanticLinksFrom(a) = %#v, want no inherited child relations", got)
	}

	ambiguous := map[string]bool{}
	invalidSources := 0
	for _, diagnostic := range loaded.RelationDiagnostics() {
		if diagnostic.Code == "ambiguous_fragment" {
			ambiguous[diagnostic.RawTarget] = true
		}
		if diagnostic.Code == "invalid_source" {
			invalidSources++
		}
	}
	for _, fragment := range []string{"duplicate", "duplicate-again"} {
		if !ambiguous[fragment] {
			t.Fatalf("diagnostics = %#v, missing ambiguous fragment %q", loaded.RelationDiagnostics(), fragment)
		}
	}
	if invalidSources != 2 {
		t.Fatalf("invalid_source diagnostics = %d, want 2: %#v", invalidSources, loaded.RelationDiagnostics())
	}
}

func sameDiagnosticStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestSemanticLinksFromNilAndUnknownBundle(t *testing.T) {
	t.Parallel()

	// Arrange.
	var bundle *Bundle
	id := mustParseConceptID(t, "unknown")

	// Act + Assert.
	if got := bundle.SemanticLinksFrom(id); got != nil {
		t.Fatalf("nil Bundle SemanticLinksFrom() = %#v, want nil", got)
	}

	root := t.TempDir()
	writeFile(t, root, "a.md", "---\ntype: Note\n---\nBody.\n")
	loaded, err := LoadBundle(root)
	if err != nil {
		t.Fatalf("LoadBundle() error = %v", err)
	}
	if got := loaded.SemanticLinksFrom(id); got != nil {
		t.Fatalf("unknown SemanticLinksFrom() = %#v, want nil", got)
	}
}
