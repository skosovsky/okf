package bundle

import "testing"

func TestDeclaredSemanticLinksRetainValidUnresolvedTargets(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeFile(t, root, "source.md", "---\ntype: Note\nrelations:\n  uses:\n    - target: existing\n    - target: missing\n    - target: existing#missing-fragment\n    - target: /invalid.md\n---\nbody\n")
	writeFile(t, root, "existing.md", "---\ntype: Note\n---\nbody\n")
	loaded, err := LoadBundle(root)
	if err != nil {
		t.Fatal(err)
	}
	source := mustParseConceptID(t, "source")

	// Act.
	observations := loaded.DeclaredSemanticLinksFrom(source)
	resolved := loaded.SemanticLinksFrom(source)

	// Assert.
	if len(observations) != 3 {
		t.Fatalf("DeclaredSemanticLinksFrom() = %#v, want 3 valid declarations", observations)
	}
	exists := map[string]bool{}
	for _, observation := range observations {
		exists[observation.Target.String()] = observation.TargetExists
	}
	if !exists["existing"] || exists["missing"] || exists["existing#missing-fragment"] {
		t.Fatalf("observation existence = %#v", exists)
	}
	if len(resolved) != 1 || resolved[0].Target.String() != "existing" {
		t.Fatalf("SemanticLinksFrom() = %#v", resolved)
	}
	observations[0].Type = "mutated"
	if loaded.DeclaredSemanticLinksFrom(source)[0].Type == "mutated" {
		t.Fatal("DeclaredSemanticLinksFrom() did not return defensive copy")
	}
}
