package bundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildIndexTextGroupsByTypeAndSortsTitles(t *testing.T) {
	t.Parallel()

	// Arrange.
	entries := []IndexEntry{
		{Type: "Table", Title: "users", Link: "users.md", Description: "Users."},
		{Type: "", Title: "Loose", Link: "loose.md"},
		{Type: "Table", Title: "Events", Link: "events.md", Description: "Events."},
	}

	// Act.
	text := BuildIndexText(entries)

	// Assert.
	if !strings.HasPrefix(text, "# Other\n\n* [Loose](loose.md)\n\n# Table") {
		t.Fatalf("BuildIndexText() = %q, want grouped sorted output", text)
	}
	if !strings.Contains(text, "* [Events](events.md) - Events.\n* [users](users.md) - Users.") {
		t.Fatalf("BuildIndexText() = %q, want case-insensitive title sorting", text)
	}
}

func TestRegenerateIndexesGroupsByTypeAndLinksRelative(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeIndexDoc(t, root, "datasets/ga4.md", "BigQuery Dataset", "GA4 Dataset", "GA4 obfuscated ecommerce sample.")
	writeIndexDoc(t, root, "tables/events_.md", "BigQuery Table", "events_*", "Daily-sharded GA4 event tables.")
	writeIndexDoc(t, root, "tables/users.md", "BigQuery Table", "users", "Per-user dimension.")
	synth := func(_ string, children []IndexChild) string {
		return "stub: " + itoa(len(children)) + " items"
	}

	// Act.
	written, err := RegenerateIndexesWith(root, synth)
	if err != nil {
		t.Fatalf("RegenerateIndexesWith() error = %v", err)
	}

	// Assert.
	if len(written) == 0 {
		t.Fatal("RegenerateIndexesWith() wrote no files")
	}
	tablesIndex := readFile(t, root, "tables/index.md")
	if !strings.HasPrefix(tablesIndex, "# BigQuery Table") {
		t.Fatalf("tables/index.md = %q, want BigQuery Table heading", tablesIndex)
	}
	if !strings.Contains(tablesIndex, "[events_*](events_.md)") {
		t.Fatalf("tables/index.md = %q, want events link", tablesIndex)
	}
	if !strings.Contains(tablesIndex, "[users](users.md)") {
		t.Fatalf("tables/index.md = %q, want users link", tablesIndex)
	}
	if !strings.Contains(tablesIndex, "Daily-sharded GA4 event tables.") {
		t.Fatalf("tables/index.md = %q, want description", tablesIndex)
	}

	rootIndex := readFile(t, root, "index.md")
	if !strings.Contains(rootIndex, "# Subdirectories") {
		t.Fatalf("index.md = %q, want Subdirectories heading", rootIndex)
	}
	if !strings.Contains(rootIndex, "(datasets/index.md) - GA4 obfuscated ecommerce sample.") {
		t.Fatalf("index.md = %q, want datasets description reuse", rootIndex)
	}
	if !strings.Contains(rootIndex, "(tables/index.md) - stub: 2 items") {
		t.Fatalf("index.md = %q, want synthesized tables description", rootIndex)
	}
}

func TestRegenerateIndexesSkipsEmptyDirectories(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "empty_dir"), 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	// Act.
	written, err := RegenerateIndexes(root)

	// Assert.
	if err != nil {
		t.Fatalf("RegenerateIndexes() error = %v", err)
	}
	if len(written) != 0 {
		t.Fatalf("len(written) = %d, want 0", len(written))
	}
	if _, err := os.Stat(filepath.Join(root, "empty_dir", "index.md")); !os.IsNotExist(err) {
		t.Fatalf("empty_dir/index.md stat error = %v, want not exist", err)
	}
}

func TestRegenerateIndexesSingleChildReusesDescription(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeIndexDoc(t, root, "datasets/only.md", "BigQuery Dataset", "Only Dataset", "The only dataset in this bundle.")
	calls := 0
	synth := func(_ string, children []IndexChild) string {
		calls++
		return "stub: " + itoa(len(children)) + " items"
	}

	// Act.
	_, err := RegenerateIndexesWith(root, synth)
	if err != nil {
		t.Fatalf("RegenerateIndexesWith() error = %v", err)
	}

	// Assert.
	rootIndex := readFile(t, root, "index.md")
	if !strings.Contains(rootIndex, "(datasets/index.md) - The only dataset in this bundle.") {
		t.Fatalf("index.md = %q, want single-child description reuse", rootIndex)
	}
	if calls != 0 {
		t.Fatalf("synth calls = %d, want 0", calls)
	}
}

func TestRegenerateIndexesPreservesRootOKFVersion(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeFile(t, root, "index.md", "---\nokf_version: \"0.1\"\n---\n\n# Old\n\n* [Old](old.md)\n")
	writeIndexDoc(t, root, "notes/a.md", "Note", "A", "Alpha.")

	// Act.
	_, err := RegenerateIndexes(root)
	if err != nil {
		t.Fatalf("RegenerateIndexes() error = %v", err)
	}
	document, err := ParseDocument(readFile(t, root, "index.md"))
	if err != nil {
		t.Fatalf("ParseDocument(index.md) error = %v", err)
	}

	// Assert.
	if got, ok := document.Frontmatter.OKFVersion(); !ok || got != "0.1" {
		t.Fatalf("OKFVersion() = %q, %v; want 0.1, true", got, ok)
	}
	if !strings.Contains(document.Body, "[notes](notes/index.md) - Alpha.") {
		t.Fatalf("index.md body = %q, want regenerated notes entry", document.Body)
	}
}

func TestRegenerateIndexesSkipsReservedFiles(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeFile(t, root, "log.md", "# Log\n\n## 2026-06-21\n* **Update**: Created bundle.\n")
	writeIndexDoc(t, root, "a.md", "Note", "A", "Alpha.")

	// Act.
	_, err := RegenerateIndexes(root)
	if err != nil {
		t.Fatalf("RegenerateIndexes() error = %v", err)
	}

	// Assert.
	index := readFile(t, root, "index.md")
	if strings.Contains(index, "log.md") {
		t.Fatalf("index.md = %q, want reserved log.md omitted", index)
	}
	if !strings.Contains(index, "[A](a.md)") {
		t.Fatalf("index.md = %q, want concept entry", index)
	}
}

func TestRegenerateIndexesMissingRootReturnsNoFiles(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := filepath.Join(t.TempDir(), "missing")

	// Act.
	written, err := RegenerateIndexes(root)

	// Assert.
	if err != nil {
		t.Fatalf("RegenerateIndexes() error = %v", err)
	}
	if len(written) != 0 {
		t.Fatalf("len(written) = %d, want 0", len(written))
	}
}

func writeIndexDoc(t *testing.T, root, rel, typ, title, description string) {
	t.Helper()
	contents := "---\n" +
		"type: " + typ + "\n" +
		"title: " + title + "\n" +
		"description: " + description + "\n" +
		"timestamp: 2026-05-27T00:00:00+00:00\n" +
		"---\n\n" +
		"# " + title + "\n\n" +
		description + "\n"
	writeFile(t, root, rel, contents)
}

func readFile(t *testing.T, root, rel string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", rel, err)
	}
	return string(contents)
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for value > 0 {
		i--
		digits[i] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[i:])
}
