package bundle

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestParseConceptID(t *testing.T) {
	t.Parallel()

	// Arrange.
	raw := "/tables//orders/"

	// Act.
	id, err := ParseConceptID(raw)

	// Assert.
	if err != nil {
		t.Fatalf("ParseConceptID() error = %v", err)
	}
	if got, want := id.String(), "tables/orders"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
	if got, want := id.Name(), "orders"; got != want {
		t.Fatalf("Name() = %q, want %q", got, want)
	}
}

func TestConceptIDParent(t *testing.T) {
	t.Parallel()

	// Arrange.
	id, err := ParseConceptID("datasets/sales/orders")
	if err != nil {
		t.Fatalf("ParseConceptID() error = %v", err)
	}

	// Act.
	parent, ok := id.Parent()

	// Assert.
	if !ok {
		t.Fatal("Parent() ok = false, want true")
	}
	if got, want := parent.String(), "datasets/sales"; got != want {
		t.Fatalf("Parent().String() = %q, want %q", got, want)
	}
}

func TestConceptIDRootHasNoParent(t *testing.T) {
	t.Parallel()

	// Arrange.
	id, err := ParseConceptID("root")
	if err != nil {
		t.Fatalf("ParseConceptID() error = %v", err)
	}

	// Act.
	_, ok := id.Parent()

	// Assert.
	if ok {
		t.Fatal("Parent() ok = true, want false")
	}
}

func TestConceptIDRejectsInvalidSegments(t *testing.T) {
	t.Parallel()

	tests := []string{
		"",
		".",
		"..",
	}

	for _, raw := range tests {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			// The raw value is the invalid concept id under test.

			// Act.
			_, err := ParseConceptID(raw)

			// Assert.
			if !errors.Is(err, ErrInvalidConceptID) {
				t.Fatalf("ParseConceptID(%q) error = %v, want ErrInvalidConceptID", raw, err)
			}
		})
	}
}

func TestConceptIDAllowsSpecPortableSegments(t *testing.T) {
	t.Parallel()

	// Arrange.
	raw := "таблицы/customer orders/@raw"

	// Act.
	id, err := ParseConceptID(raw)

	// Assert.
	if err != nil {
		t.Fatalf("ParseConceptID(%q) error = %v, want nil", raw, err)
	}
	if got, want := id.String(), raw; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestConceptIDSegmentsAreCopied(t *testing.T) {
	t.Parallel()

	// Arrange.
	segments := []string{"tables", "orders"}

	// Act.
	id, err := NewConceptID(segments)
	if err != nil {
		t.Fatalf("NewConceptID() error = %v", err)
	}
	segments[1] = "customers"
	gotSegments := id.Segments()
	gotSegments[0] = "mutated"

	// Assert.
	if got, want := id.String(), "tables/orders"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestConceptIDPathMapping(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	id, err := ParseConceptID("tables/orders")
	if err != nil {
		t.Fatalf("ParseConceptID() error = %v", err)
	}
	path := filepath.Join(root, "tables", "orders.md")

	// Act.
	gotPath := id.ToPath(root)
	gotID, err := ConceptIDFromPath(root, path)

	// Assert.
	if err != nil {
		t.Fatalf("ConceptIDFromPath() error = %v", err)
	}
	if gotPath != path {
		t.Fatalf("ToPath() = %q, want %q", gotPath, path)
	}
	if gotID.String() != id.String() {
		t.Fatalf("ConceptIDFromPath() = %q, want %q", gotID, id)
	}
}
