package bundle

import "testing"

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
	writeFile(t, root, "b.md", "---\ntype: Note\n---\nBody.\n")
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
		{source: "a#field1", typ: "writes_to", target: "b#col-2", exists: true},
		{source: "a#anchor_only", typ: "reads_from", target: "b#anchor-target", exists: true},
		{source: "a#fallback_anchor", typ: "reads_from", target: "b", exists: true},
		{source: "a#chosen_id", typ: "maps_to", target: "missing#col", exists: false},
		{source: "a", typ: "depends_on", target: "b#section-1", exists: true},
		{source: "a", typ: "depends_on", target: "b#section-1", exists: true},
		{source: "a", typ: "traces", target: "b", exists: true},
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
	if relations[4].Source.String() != "a" {
		t.Fatalf("top-level relation source = %s, want document source a", relations[4].Source)
	}

	relations[0].Type = "mutated"
	if got := bundle.SemanticLinksFrom(a)[0].Type; got != "writes_to" {
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
	writeFile(t, root, "b.md", "---\ntype: Note\n---\nBody.\n")
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
