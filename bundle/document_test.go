package bundle

import (
	"errors"
	"strings"
	"testing"
)

func TestNewDocument(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter := NewFrontmatter()
	if err := frontmatter.SetString("type", "Metric"); err != nil {
		t.Fatalf("SetString() error = %v", err)
	}

	// Act.
	document := NewDocument(frontmatter, "# DAU\n")

	// Assert.
	if got, want := document.Body, "# DAU\n"; got != want {
		t.Fatalf("Body = %q, want %q", got, want)
	}
	if got, ok := document.Frontmatter.Type(); !ok || got != "Metric" {
		t.Fatalf("Frontmatter.Type() = %q, %v; want Metric, true", got, ok)
	}
}

func TestParseDocumentRoundtripPreservesFrontmatterAndBody(t *testing.T) {
	t.Parallel()

	// Arrange.
	src := "---\n" +
		"type: BigQuery Table\n" +
		"title: Sample\n" +
		"description: A sample table.\n" +
		"tags: [a, b]\n" +
		"timestamp: 2026-05-27T00:00:00+00:00\n" +
		"---\n\n" +
		"# Sample\n\n" +
		"Body text.\n"

	// Act.
	document, err := ParseDocument(src)
	if err != nil {
		t.Fatalf("ParseDocument() error = %v", err)
	}
	serialized, err := document.Serialize()
	if err != nil {
		t.Fatalf("Serialize() error = %v", err)
	}
	reparsed, err := ParseDocument(serialized)
	if err != nil {
		t.Fatalf("ParseDocument(serialized) error = %v", err)
	}

	// Assert.
	if got, ok := document.Frontmatter.Type(); !ok || got != "BigQuery Table" {
		t.Fatalf("Frontmatter.Type() = %q, %v; want BigQuery Table, true", got, ok)
	}
	if got, want := document.Frontmatter.Tags(), []string{"a", "b"}; !equalStrings(got, want) {
		t.Fatalf("Tags() = %#v, want %#v", got, want)
	}
	if !strings.HasPrefix(document.Body, "# Sample") {
		t.Fatalf("Body = %q, want # Sample prefix", document.Body)
	}
	if got, want := reparsed.Frontmatter.Keys(), document.Frontmatter.Keys(); !equalStrings(got, want) {
		t.Fatalf("reparsed keys = %#v, want %#v", got, want)
	}
	if strings.TrimSpace(reparsed.Body) != strings.TrimSpace(document.Body) {
		t.Fatalf("reparsed body = %q, want %q", reparsed.Body, document.Body)
	}
}

func TestParseDocumentNoFrontmatterTreatsAllAsBody(t *testing.T) {
	t.Parallel()

	// Arrange.
	src := "# Hello\n\nNo frontmatter here.\n"

	// Act.
	document, err := ParseDocument(src)

	// Assert.
	if err != nil {
		t.Fatalf("ParseDocument() error = %v", err)
	}
	if !document.Frontmatter.IsEmpty() {
		t.Fatal("Frontmatter.IsEmpty() = false, want true")
	}
	if document.Body != src {
		t.Fatalf("Body = %q, want original source", document.Body)
	}
}

func TestParseDocumentUnterminatedFrontmatter(t *testing.T) {
	t.Parallel()

	// Arrange.
	src := "---\ntype: X\nstill in frontmatter\n"

	// Act.
	_, err := ParseDocument(src)

	// Assert.
	if !errors.Is(err, ErrUnterminatedFrontmatter) {
		t.Fatalf("ParseDocument() error = %v, want ErrUnterminatedFrontmatter", err)
	}
}

func TestDocumentValidateConformanceRequiresOnlyType(t *testing.T) {
	t.Parallel()

	// Arrange.
	document, err := ParseDocument("---\ntype: Metric\n---\nbody\n")
	if err != nil {
		t.Fatalf("ParseDocument() error = %v", err)
	}
	noType, err := ParseDocument("---\ntitle: X\n---\n")
	if err != nil {
		t.Fatalf("ParseDocument(noType) error = %v", err)
	}

	// Act.
	conformanceErr := document.ValidateConformance()
	noTypeErr := noType.ValidateConformance()

	// Assert.
	if conformanceErr != nil {
		t.Fatalf("ValidateConformance() error = %v, want nil", conformanceErr)
	}
	if !errors.Is(noTypeErr, ErrMissingFrontmatterKeys) {
		t.Fatalf("noType.ValidateConformance() error = %v, want ErrMissingFrontmatterKeys", noTypeErr)
	}
}

func TestDocumentValidateConformanceRejectsEmptyType(t *testing.T) {
	t.Parallel()

	// Arrange.
	document, err := ParseDocument("---\ntype: \"\"\n---\n")
	if err != nil {
		t.Fatalf("ParseDocument() error = %v", err)
	}

	// Act.
	err = document.ValidateConformance()

	// Assert.
	if !errors.Is(err, ErrMissingFrontmatterKeys) {
		t.Fatalf("ValidateConformance() error = %v, want ErrMissingFrontmatterKeys", err)
	}
}

func TestDocumentValidateConformanceRejectsNonStringType(t *testing.T) {
	t.Parallel()

	// Arrange.
	document, err := ParseDocument("---\ntype: 123\n---\n")
	if err != nil {
		t.Fatalf("ParseDocument() error = %v", err)
	}

	// Act.
	err = document.ValidateConformance()

	// Assert.
	if !errors.Is(err, ErrMissingFrontmatterKeys) {
		t.Fatalf("ValidateConformance() error = %v, want ErrMissingFrontmatterKeys", err)
	}
}

func TestParseDocumentRejectsNonMappingFrontmatter(t *testing.T) {
	t.Parallel()

	// Arrange.
	src := "---\n- not\n- a\n- mapping\n---\nbody\n"

	// Act.
	_, err := ParseDocument(src)

	// Assert.
	if !errors.Is(err, ErrInvalidFrontmatter) {
		t.Fatalf("ParseDocument() error = %v, want ErrInvalidFrontmatter", err)
	}
}

func TestParseDocumentPreservesUnknownKeysOnRoundtrip(t *testing.T) {
	t.Parallel()

	// Arrange.
	src := "---\n" +
		"type: X\n" +
		"custom_key: custom value\n" +
		"nested:\n" +
		"  a: 1\n" +
		"  b: 2\n" +
		"relations:\n" +
		"  depends_on:\n" +
		"    - target: other\n" +
		"---\n" +
		"body\n"

	// Act.
	document, err := ParseDocument(src)
	if err != nil {
		t.Fatalf("ParseDocument() error = %v", err)
	}
	serialized, err := document.Serialize()
	if err != nil {
		t.Fatalf("Serialize() error = %v", err)
	}
	reparsed, err := ParseDocument(serialized)
	if err != nil {
		t.Fatalf("ParseDocument(serialized) error = %v", err)
	}

	// Assert.
	if _, ok := document.Frontmatter.Get("custom_key"); !ok {
		t.Fatal("Get(custom_key) ok = false, want true")
	}
	if _, ok := document.Frontmatter.Get("nested"); !ok {
		t.Fatal("Get(nested) ok = false, want true")
	}
	if got, want := document.Frontmatter.ExtensionKeys(), []string{"custom_key", "nested", "relations"}; !equalStrings(got, want) {
		t.Fatalf("ExtensionKeys() = %#v, want %#v", got, want)
	}
	if got, want := reparsed.Frontmatter.Keys(), document.Frontmatter.Keys(); !equalStrings(got, want) {
		t.Fatalf("reparsed keys = %#v, want %#v", got, want)
	}
}

func TestParseDocumentEmptyFrontmatterBlock(t *testing.T) {
	t.Parallel()

	// Arrange.
	src := "---\n---\nbody\n"

	// Act.
	document, err := ParseDocument(src)
	if err != nil {
		t.Fatalf("ParseDocument() error = %v", err)
	}
	serialized, err := document.Serialize()
	if err != nil {
		t.Fatalf("Serialize() error = %v", err)
	}

	// Assert.
	if !document.Frontmatter.IsEmpty() {
		t.Fatal("Frontmatter.IsEmpty() = false, want true")
	}
	if got, want := document.Body, "body"; got != want {
		t.Fatalf("Body = %q, want %q", got, want)
	}
	if !strings.HasSuffix(serialized, "body\n") {
		t.Fatalf("Serialize() = %q, want body newline suffix", serialized)
	}
}

func TestDocumentLinksAndCitationsIntegration(t *testing.T) {
	t.Parallel()

	// Arrange.
	src := "---\ntype: BigQuery Table\n---\n\n" +
		"Joined with [customers](/tables/customers.md).\n\n" +
		"# Citations\n" +
		"[1] [BQ](https://bq)\n"

	// Act.
	document, err := ParseDocument(src)
	if err != nil {
		t.Fatalf("ParseDocument() error = %v", err)
	}
	links := document.Links()
	citations := document.Citations()

	// Assert.
	if got, want := len(links), 2; got != want {
		t.Fatalf("len(Links()) = %d, want %d", got, want)
	}
	internal := 0
	for _, link := range links {
		if link.Kind == LinkAbsolute {
			internal++
		}
	}
	if internal != 1 {
		t.Fatalf("absolute link count = %d, want 1", internal)
	}
	if got, want := len(citations), 1; got != want {
		t.Fatalf("len(Citations()) = %d, want %d", got, want)
	}
}

func equalStrings(got, want []string) bool {
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
