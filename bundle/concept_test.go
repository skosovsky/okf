package bundle

import "testing"

func TestNewConcept(t *testing.T) {
	t.Parallel()

	// Arrange.
	id, err := ParseConceptID("tables/orders")
	if err != nil {
		t.Fatalf("ParseConceptID() error = %v", err)
	}
	document := NewDocument(NewFrontmatter(), "# Orders\n")

	// Act.
	concept := NewConcept(id, "tables/orders.md", document)

	// Assert.
	if got, want := concept.ID.String(), "tables/orders"; got != want {
		t.Fatalf("ID = %q, want %q", got, want)
	}
	if got, want := concept.Path, "tables/orders.md"; got != want {
		t.Fatalf("Path = %q, want %q", got, want)
	}
	if got, want := concept.Document.Body, "# Orders\n"; got != want {
		t.Fatalf("Document.Body = %q, want %q", got, want)
	}
}
