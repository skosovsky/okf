package bundle

import (
	"bytes"
	"errors"
	"io/fs"
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

func TestRegenerateIndexesExistingEmptyBundleIsSuccessfulNoOp(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()

	// Act.
	written, err := RegenerateIndexes(root)

	// Assert.
	if err != nil {
		t.Fatalf("RegenerateIndexes() error = %v", err)
	}
	if written != nil {
		t.Fatalf("written = %#v, want nil", written)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("bundle entries = %#v, want none", entries)
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
	if got := document.Frontmatter.VersionDeclarationState(); !got.Present || !got.Valid || got.Value != "0.1" {
		t.Fatalf("VersionDeclarationState() = %#v, want valid 0.1", got)
	}
	if !strings.Contains(document.Body, "[notes](notes/index.md) - Alpha.") {
		t.Fatalf("index.md body = %q, want regenerated notes entry", document.Body)
	}
}

func TestRegenerateIndexesRejectsMalformedPresentVersionBeforeAnyWrite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		frontmatter string
	}{
		{name: "scalar type", frontmatter: "okf_version: 2\n"},
		{name: "sequence type", frontmatter: "okf_version: [0.2]\n"},
		{name: "mapping type", frontmatter: "okf_version: {major: 0, minor: 2}\n"},
		{name: "blank string", frontmatter: "okf_version: \"\"\n"},
		{name: "noncanonical syntax", frontmatter: "okf_version: \"v0.2\"\n"},
		{
			name: "malformed terminal alias",
			frontmatter: "declared: &declared [0.2]\n" +
				"okf_version: *declared\n",
		},
		{
			name: "malformed merged declaration",
			frontmatter: "defaults: &defaults\n" +
				"  okf_version: {major: 0, minor: 2}\n" +
				"<<: *defaults\n",
		},
		{
			name: "malformed explicit overrides valid merge",
			frontmatter: "defaults: &defaults\n" +
				"  okf_version: \"0.1\"\n" +
				"<<: *defaults\n" +
				"okf_version: \"02.0\"\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			rootBefore := "---\n" + test.frontmatter + "---\n\n# Root before\n"
			nestedBefore := "# Nested before\n"
			writeFile(t, root, "index.md", rootBefore)
			writeFile(t, root, "notes/index.md", nestedBefore)
			writeIndexDoc(t, root, "notes/a.md", "Note", "A", "Alpha.")

			// Act.
			written, err := RegenerateIndexes(root)

			// Assert.
			if !errors.Is(err, ErrInvalidVersionDeclaration) {
				t.Fatalf("RegenerateIndexes() error = %v, want ErrInvalidVersionDeclaration", err)
			}
			if len(written) != 0 {
				t.Fatalf("written = %#v, want none", written)
			}
			if after := readFile(t, root, "index.md"); after != rootBefore {
				t.Fatalf("root index changed:\n%s", after)
			}
			if after := readFile(t, root, "notes/index.md"); after != nestedBefore {
				t.Fatalf("nested index changed:\n%s", after)
			}
		})
	}
}

func TestRegenerateIndexesRejectsInvalidRootDocumentBeforeAnyWrite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		root []byte
		want error
	}{
		{
			name: "malformed yaml",
			root: []byte("---\nokf_version: [\n---\n\n# Root before\n"),
			want: ErrInvalidFrontmatter,
		},
		{
			name: "unterminated frontmatter",
			root: []byte("---\nokf_version: \"0.2\"\n# Root before\n"),
			want: ErrUnterminatedFrontmatter,
		},
		{
			name: "invalid utf8",
			root: append([]byte("---\nokf_version: \"0.2\"\n---\n\n# Root "), 0xff, '\n'),
			want: ErrInvalidEncoding,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			rootPath := filepath.Join(root, "index.md")
			if err := os.WriteFile(rootPath, test.root, 0o644); err != nil {
				t.Fatal(err)
			}
			nestedBefore := []byte("# Nested before\n")
			conceptBefore := []byte("---\ntype: Note\ntitle: A\n---\n\n# A\n")
			writeFile(t, root, "notes/index.md", string(nestedBefore))
			writeFile(t, root, "notes/a.md", string(conceptBefore))
			before := map[string][]byte{
				"index.md":       append([]byte(nil), test.root...),
				"notes/index.md": append([]byte(nil), nestedBefore...),
				"notes/a.md":     append([]byte(nil), conceptBefore...),
			}
			synthesizeCalls := 0

			// Act.
			written, err := RegenerateIndexesWith(root, func(string, []IndexChild) string {
				synthesizeCalls++
				return "must not run"
			})

			// Assert.
			if !errors.Is(err, test.want) {
				t.Fatalf("RegenerateIndexesWith() error = %v, want %v", err, test.want)
			}
			if len(written) != 0 || synthesizeCalls != 0 {
				t.Fatalf("written = %#v, synthesize calls = %d; want no side effects", written, synthesizeCalls)
			}
			for relative, want := range before {
				after, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
				if readErr != nil {
					t.Fatalf("read %s: %v", relative, readErr)
				}
				if !bytes.Equal(after, want) {
					t.Fatalf("%s changed:\ngot  %q\nwant %q", relative, after, want)
				}
			}
		})
	}
}

func TestRegenerateIndexesAcceptsRootWithoutFrontmatter(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeFile(t, root, "index.md", "# Root before\n")
	writeIndexDoc(t, root, "a.md", "Note", "A", "Alpha.")

	// Act.
	written, err := RegenerateIndexes(root)

	// Assert.
	if err != nil {
		t.Fatalf("RegenerateIndexes() error = %v", err)
	}
	if len(written) == 0 {
		t.Fatal("RegenerateIndexes() wrote no indexes")
	}
	document, err := ParseDocument(readFile(t, root, "index.md"))
	if err != nil {
		t.Fatal(err)
	}
	if document.HasFrontmatter || !strings.Contains(document.Body, "[A](a.md) - Alpha.") {
		t.Fatalf("root document = %#v", document)
	}
}

func TestRegenerateIndexesPreservesSemanticVersionDeclaration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		frontmatter string
		want        string
	}{
		{
			name: "terminal alias",
			frontmatter: "declared: &declared \"0.1\"\n" +
				"okf_version: *declared\n",
			want: "0.1",
		},
		{
			name: "merged declaration",
			frontmatter: "defaults: &defaults\n" +
				"  okf_version: \"0.1\"\n" +
				"<<: *defaults\n",
			want: "0.1",
		},
		{
			name: "valid explicit overrides malformed merge",
			frontmatter: "defaults: &defaults\n" +
				"  okf_version: nope\n" +
				"<<: *defaults\n" +
				"okf_version: \"0.2\"\n",
			want: "0.2",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			writeFile(t, root, "index.md", "---\n"+test.frontmatter+"---\n\n# Root before\n")
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
			state := document.Frontmatter.VersionDeclarationState()
			if !state.Present || !state.Valid || state.Value != test.want {
				t.Fatalf("VersionDeclarationState() = %#v, want %q", state, test.want)
			}
			if !strings.Contains(document.Body, "[notes](notes/index.md) - Alpha.") {
				t.Fatalf("index body = %q, want regenerated notes entry", document.Body)
			}
		})
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

func TestRegenerateIndexesMissingRootReturnsNotExistWithoutSideEffects(t *testing.T) {
	t.Parallel()

	// Arrange.
	parent := t.TempDir()
	root := filepath.Join(parent, "missing")

	// Act.
	written, err := RegenerateIndexes(root)

	// Assert.
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("RegenerateIndexes() error = %v, want fs.ErrNotExist", err)
	}
	if written != nil {
		t.Fatalf("written = %#v, want nil", written)
	}
	if _, statErr := os.Lstat(root); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("Lstat(%q) error = %v, want fs.ErrNotExist", root, statErr)
	}
	entries, readErr := os.ReadDir(parent)
	if readErr != nil {
		t.Fatalf("ReadDir(%q) error = %v", parent, readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("parent entries = %#v, want no paths or transaction artifacts", entries)
	}
}

func TestRegenerateIndexesMissingOnlyRootIndexGeneratesIt(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeIndexDoc(t, root, "a.md", "Note", "A", "Alpha.")

	// Act.
	written, err := RegenerateIndexes(root)

	// Assert.
	if err != nil {
		t.Fatalf("RegenerateIndexes() error = %v", err)
	}
	wantWritten := []string{filepath.Join(root, indexFilename)}
	if len(written) != len(wantWritten) || written[0] != wantWritten[0] {
		t.Fatalf("written = %#v, want %#v", written, wantWritten)
	}
	wantIndex := "# Note\n\n* [A](a.md) - Alpha.\n"
	if got := readFile(t, root, indexFilename); got != wantIndex {
		t.Fatalf("%s = %q, want %q", indexFilename, got, wantIndex)
	}
	entries, readErr := os.ReadDir(root)
	if readErr != nil {
		t.Fatalf("ReadDir(%q) error = %v", root, readErr)
	}
	if len(entries) != 2 || entries[0].Name() != "a.md" || entries[1].Name() != indexFilename {
		t.Fatalf("bundle entries = %#v, want only a.md and %s", entries, indexFilename)
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
