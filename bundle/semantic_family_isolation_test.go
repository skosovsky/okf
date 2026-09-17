package bundle

import (
	"context"
	"fmt"
	"testing"
)

func TestStandardSemanticFamilyDonorsCannotLeakThroughExtensions(t *testing.T) {
	t.Parallel()

	keys := []string{
		"type", "title", "description", "resource", "tags", "okf_version",
		"timestamp", "sources", "usage_window", "generated", "verified",
		"status", "stale_after", "runtime", "parameters", "computation",
		"executor", "attester",
	}
	for _, key := range keys {
		key := key
		for _, shape := range []string{"terminal alias", "merge donor"} {
			shape := shape
			t.Run(key+"/"+shape, func(t *testing.T) {
				t.Parallel()

				// Arrange.
				fragment := "leaked-" + key
				var yamlText string
				if shape == "terminal alias" {
					yamlText = fmt.Sprintf(
						"carrier: &family\n  id: %s\n  relations:\n    depends_on: target\n%s: *family\n",
						fragment,
						key,
					)
				} else {
					yamlText = fmt.Sprintf(
						"carrier: &family\n  %s:\n    id: %s\n    relations:\n      depends_on: target\n<<: *family\n",
						key,
						fragment,
					)
				}
				frontmatter, err := ParseFrontmatter(yamlText)
				if err != nil {
					t.Fatal(err)
				}
				source := mustParseConceptID(t, "source")
				target := mustParseConceptID(t, "target")
				concept := Concept{ID: source, Path: "source.md", Document: NewDocument(frontmatter, "")}
				loaded := &Bundle{
					concepts:     []Concept{{ID: source}, {ID: target}},
					byID:         map[string]int{source.String(): 0, target.String(): 1},
					subresources: make(map[string]map[string]fragmentState),
				}

				// Act.
				if err := loaded.indexSubresourcesContext(context.Background(), concept); err != nil {
					t.Fatal(err)
				}
				relations, observations := extractSemanticRelations(concept, loaded)

				// Assert.
				if loaded.FragmentExists(source, fragment) {
					t.Fatalf("%s family leaked fragment %q through %s", key, fragment, shape)
				}
				if len(relations) != 0 || len(observations) != 0 {
					t.Fatalf("%s family leaked relations through %s: resolved=%#v observed=%#v", key, shape, relations, observations)
				}
			})
		}
	}
}

func TestDuplicateStandardFamilyMergeDonorsCannotLeakThroughExtensions(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter, err := ParseFrontmatter(`
carrier: &carrier
  sources:
    - id: leaked-first
      resource: one
      relations:
        uses:
          - target: target
  sources:
    - id: leaked-second
      resource: two
      relations:
        uses:
          - target: target
<<: *carrier
parts:
  - id: retained-extension
`)
	if err != nil {
		t.Fatal(err)
	}
	source := mustParseConceptID(t, "source")
	target := mustParseConceptID(t, "target")
	concept := Concept{ID: source, Path: "source.md", Document: NewDocument(frontmatter, "")}
	loaded := &Bundle{
		concepts:     []Concept{{ID: source}, {ID: target}},
		byID:         map[string]int{source.String(): 0, target.String(): 1},
		subresources: make(map[string]map[string]fragmentState),
	}

	// Act.
	if err := loaded.indexSubresourcesContext(context.Background(), concept); err != nil {
		t.Fatal(err)
	}
	relations, observations := extractSemanticRelations(concept, loaded)
	state := frontmatter.SemanticValueState("sources")

	// Assert.
	if !state.Present || !state.Ambiguous || state.Value != nil {
		t.Fatalf("sources state = %#v, want present ambiguous unresolved", state)
	}
	if got := loaded.Subresources(source); len(got) != 1 || got[0] != "retained-extension" {
		t.Fatalf("Subresources(source) = %#v, want only retained extension", got)
	}
	if len(relations) != 0 || len(observations) != 0 {
		t.Fatalf("duplicate standard donors leaked relations: resolved=%#v observed=%#v", relations, observations)
	}
}
