package bundle

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestParseErrorString(t *testing.T) {
	t.Parallel()

	// Arrange.
	withoutErr := ParseError{Path: "bad.md"}
	withErr := ParseError{Path: "bad.md", Err: errors.New("boom")}

	// Act.
	gotWithoutErr := withoutErr.Error()
	gotWithErr := withErr.Error()

	// Assert.
	if gotWithoutErr != "bad.md" {
		t.Fatalf("ParseError without err = %q, want bad.md", gotWithoutErr)
	}
	if gotWithErr != "bad.md: boom" {
		t.Fatalf("ParseError with err = %q, want bad.md: boom", gotWithErr)
	}
}

func TestLoadBundleErrorPaths(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	filePath := filepath.Join(root, "not-dir")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	missing := filepath.Join(root, "missing")

	// Act.
	_, missingErr := LoadBundle(missing)
	_, fileErr := LoadBundle(filePath)
	_, collectErr := collectMarkdownFiles(missing)

	// Assert.
	if missingErr == nil {
		t.Fatal("LoadBundle(missing) error = nil, want error")
	}
	if !errors.Is(fileErr, ErrNotDirectory) {
		t.Fatalf("LoadBundle(file) error = %v, want ErrNotDirectory", fileErr)
	}
	if collectErr == nil {
		t.Fatal("collectMarkdownFiles(missing) error = nil, want error")
	}
}

func TestTraversalErrors(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	blocked := filepath.Join(root, "blocked")
	if err := os.Mkdir(blocked, 0o755); err != nil {
		t.Fatalf("Mkdir(blocked) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "a.md"), []byte("---\ntype: Note\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(blocked/a.md) error = %v", err)
	}
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatalf("Chmod(blocked, 0) error = %v", err)
	}
	defer func() {
		_ = os.Chmod(blocked, 0o755)
	}()

	// Act.
	_, loadErr := LoadBundle(root)
	_, indexErr := RegenerateIndexes(root)

	// Assert.
	if loadErr == nil {
		t.Fatal("LoadBundle(unreadable child) error = nil, want error")
	}
	if indexErr == nil {
		t.Fatal("RegenerateIndexes(unreadable child) error = nil, want error")
	}
}

func TestNilBundleMethods(t *testing.T) {
	t.Parallel()

	// Arrange.
	var bundle *Bundle
	id := mustParseConceptID(t, "a")

	// Act / Assert.
	if bundle.Root() != "" {
		t.Fatalf("Root() = %q, want empty", bundle.Root())
	}
	if bundle.Concepts() != nil {
		t.Fatalf("Concepts() = %#v, want nil", bundle.Concepts())
	}
	if bundle.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", bundle.Len())
	}
	if !bundle.IsEmpty() {
		t.Fatal("IsEmpty() = false, want true")
	}
	if _, ok := bundle.Get(id); ok {
		t.Fatal("Get() ok = true, want false")
	}
	if bundle.Contains(id) {
		t.Fatal("Contains() = true, want false")
	}
	if bundle.IndexFiles() != nil {
		t.Fatalf("IndexFiles() = %#v, want nil", bundle.IndexFiles())
	}
	if bundle.LogFiles() != nil {
		t.Fatalf("LogFiles() = %#v, want nil", bundle.LogFiles())
	}
	if bundle.MarkdownFiles() != nil {
		t.Fatalf("MarkdownFiles() = %#v, want nil", bundle.MarkdownFiles())
	}
	if bundle.ParseErrors() != nil {
		t.Fatalf("ParseErrors() = %#v, want nil", bundle.ParseErrors())
	}
	if bundle.LinksFrom(id) != nil {
		t.Fatalf("LinksFrom() = %#v, want nil", bundle.LinksFrom(id))
	}
	if bundle.Backlinks(id) != nil {
		t.Fatalf("Backlinks() = %#v, want nil", bundle.Backlinks(id))
	}
	if bundle.BrokenLinks() != nil {
		t.Fatalf("BrokenLinks() = %#v, want nil", bundle.BrokenLinks())
	}
	if version, ok := bundle.OKFVersion(); ok || version != "" {
		t.Fatalf("OKFVersion() = %q, %v; want empty, false", version, ok)
	}
}

func TestBundleMarkdownFilesCopySemantics(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeFile(t, root, "b.md", "---\ntype: Note\n---\nbody\n")
	writeFile(t, root, "index.md", "# Index\n")
	writeFile(t, root, "log.md", "# Log\n")
	writeFile(t, root, "nested/a.md", "---\ntype: Note\n---\nbody\n")
	writeFile(t, root, "ignored.txt", "not markdown\n")
	bundle, err := LoadBundle(root)
	if err != nil {
		t.Fatalf("LoadBundle() error = %v", err)
	}

	// Act.
	files := bundle.MarkdownFiles()
	files[0] = "mutated"

	// Assert.
	want := []string{
		filepath.Join(root, "b.md"),
		filepath.Join(root, "index.md"),
		filepath.Join(root, "log.md"),
		filepath.Join(root, "nested", "a.md"),
	}
	if !reflect.DeepEqual(bundle.MarkdownFiles(), want) {
		t.Fatalf("MarkdownFiles() = %#v, want %#v", bundle.MarkdownFiles(), want)
	}
}

func TestBundleLookupAndCopySemantics(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeFile(t, root, "a.md", "---\ntype: Note\n---\nbody\n")
	bundle, err := LoadBundle(root)
	if err != nil {
		t.Fatalf("LoadBundle() error = %v", err)
	}
	id := mustParseConceptID(t, "a")
	missing := mustParseConceptID(t, "missing")

	// Act.
	concept, ok := bundle.Get(id)
	_, missingOK := bundle.Get(missing)
	concepts := bundle.Concepts()
	concepts[0].Path = "mutated"

	// Assert.
	if !ok {
		t.Fatal("Get(a) ok = false, want true")
	}
	if concept.ID.String() != "a" {
		t.Fatalf("Get(a).ID = %q, want a", concept.ID)
	}
	if missingOK {
		t.Fatal("Get(missing) ok = true, want false")
	}
	if bundle.concepts[0].Path == "mutated" {
		t.Fatal("Concepts() returned mutable internal storage")
	}
}

func TestBundleOKFVersionMissingMalformedAndNoKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		index string
	}{
		{name: "missing"},
		{name: "malformed", index: "---\nokf_version: 0.1\n"},
		{name: "no key", index: "---\ntitle: Root\n---\n# Root\n"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			writeFile(t, root, "a.md", "---\ntype: Note\n---\nbody\n")
			if tt.index != "" {
				writeFile(t, root, "index.md", tt.index)
			}
			bundle, err := LoadBundle(root)
			if err != nil {
				t.Fatalf("LoadBundle() error = %v", err)
			}

			// Act.
			version, ok := bundle.OKFVersion()

			// Assert.
			if ok || version != "" {
				t.Fatalf("OKFVersion() = %q, %v; want empty, false", version, ok)
			}
		})
	}
}

func TestBundleLoadConceptFileErrorPaths(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	bundle := &Bundle{root: root}
	missing := filepath.Join(root, "missing.md")
	outsideRoot := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outsideRoot, []byte("---\ntype: Note\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(outsideRoot) error = %v", err)
	}

	// Act.
	bundle.loadConceptFile(missing)
	bundle.loadConceptFile(outsideRoot)

	// Assert.
	if got, want := len(bundle.parseErrors), 2; got != want {
		t.Fatalf("parseErrors = %d, want %d", got, want)
	}
}

func TestBundleGraphSkipsUnresolvableAndDeduplicatesBacklinks(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeFile(t, root, "a.md", "---\ntype: Note\n---\n[one](/b.md) [two](/b.md) [web](https://example.com)\n")
	writeFile(t, root, "b.md", "---\ntype: Note\n---\nbody\n")
	bundle, err := LoadBundle(root)
	if err != nil {
		t.Fatalf("LoadBundle() error = %v", err)
	}
	a := mustParseConceptID(t, "a")
	b := mustParseConceptID(t, "b")

	// Act.
	links := bundle.LinksFrom(a)
	backlinks := bundle.Backlinks(b)

	// Assert.
	if got, want := len(links), 2; got != want {
		t.Fatalf("len(LinksFrom(a)) = %d, want only two internal links", got)
	}
	if got, want := len(backlinks), 1; got != want {
		t.Fatalf("len(Backlinks(b)) = %d, want deduplicated backlink", got)
	}
}

func TestConceptIDEdgePaths(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	empty := ConceptID{}

	// Act.
	_, emptyErr := NewConceptID(nil)
	name := empty.Name()
	path := empty.ToPath(root)
	_, outsideErr := ConceptIDFromPath(root, filepath.Join(filepath.Dir(root), "outside.md"))
	_, rootErr := ConceptIDFromPath(root, root)
	segmentErr := ValidateConceptSegment("")

	// Assert.
	if !errors.Is(emptyErr, ErrInvalidConceptID) {
		t.Fatalf("NewConceptID(nil) error = %v, want ErrInvalidConceptID", emptyErr)
	}
	if name != "" {
		t.Fatalf("empty.Name() = %q, want empty", name)
	}
	if path != root {
		t.Fatalf("empty.ToPath() = %q, want %q", path, root)
	}
	if !errors.Is(outsideErr, ErrInvalidConceptID) {
		t.Fatalf("ConceptIDFromPath(outside) error = %v, want ErrInvalidConceptID", outsideErr)
	}
	if !errors.Is(rootErr, ErrInvalidConceptID) {
		t.Fatalf("ConceptIDFromPath(root) error = %v, want ErrInvalidConceptID", rootErr)
	}
	if !errors.Is(segmentErr, ErrInvalidConceptID) {
		t.Fatalf("ValidateConceptSegment(empty) error = %v, want ErrInvalidConceptID", segmentErr)
	}
}

func TestDocumentSerializeAndValidateConformanceSuccess(t *testing.T) {
	t.Parallel()

	// Arrange.
	document, err := ParseDocument("---\ntype: X\ntitle: Y\ndescription: Z\ntimestamp: 2026-05-27\n---\nbody\n")
	if err != nil {
		t.Fatalf("ParseDocument() error = %v", err)
	}
	document.Body = "body\n"

	// Act.
	serialized, err := document.Serialize()
	validateErr := document.ValidateConformance()

	// Assert.
	if err != nil {
		t.Fatalf("Serialize() error = %v", err)
	}
	if validateErr != nil {
		t.Fatalf("ValidateConformance() error = %v, want nil", validateErr)
	}
	if !strings.HasSuffix(serialized, "body\n") {
		t.Fatalf("Serialize() = %q, want newline body suffix", serialized)
	}
}

func TestFrontmatterEdgePaths(t *testing.T) {
	t.Parallel()

	// Arrange.
	emptyDocument := &yaml.Node{Kind: yaml.DocumentNode}
	invalidEncode := Frontmatter{node: yaml.Node{
		Kind: yaml.MappingNode,
		Tag:  "!!map",
		Content: []*yaml.Node{
			scalarNode("!!str", "bad"),
			{Kind: 99},
		},
	}}
	var zero Frontmatter

	// Act.
	nilFM, nilErr := NewFrontmatterFromNode(nil)
	emptyDocFM, emptyDocErr := NewFrontmatterFromNode(emptyDocument)
	_, invalidParseErr := ParseFrontmatter("a: [")
	emptyYAML, emptyYAMLErr := NewFrontmatter().YAMLString()
	_, invalidYAMLErr := invalidEncode.YAMLString()
	_, serializeErr := (Document{Frontmatter: invalidEncode}).Serialize()
	zeroIsEmpty := zero.IsEmpty()
	setErr := zero.Set("", scalarNode("!!str", "x"))
	clone := cloneYAMLNode(nil)

	// Assert.
	if nilErr != nil || !nilFM.IsEmpty() {
		t.Fatalf("NewFrontmatterFromNode(nil) = %#v, %v; want empty nil error", nilFM, nilErr)
	}
	if emptyDocErr != nil || !emptyDocFM.IsEmpty() {
		t.Fatalf("NewFrontmatterFromNode(empty doc) = %#v, %v; want empty nil error", emptyDocFM, emptyDocErr)
	}
	if !errors.Is(invalidParseErr, ErrInvalidFrontmatter) {
		t.Fatalf("ParseFrontmatter(invalid) error = %v, want ErrInvalidFrontmatter", invalidParseErr)
	}
	if emptyYAMLErr != nil || emptyYAML != "" {
		t.Fatalf("empty YAMLString() = %q, %v; want empty nil", emptyYAML, emptyYAMLErr)
	}
	if !errors.Is(invalidYAMLErr, ErrInvalidFrontmatter) {
		t.Fatalf("invalid YAMLString() error = %v, want ErrInvalidFrontmatter", invalidYAMLErr)
	}
	if !errors.Is(serializeErr, ErrInvalidFrontmatter) {
		t.Fatalf("Serialize(invalid frontmatter) error = %v, want ErrInvalidFrontmatter", serializeErr)
	}
	if !zeroIsEmpty {
		t.Fatal("zero Frontmatter IsEmpty() = false, want true")
	}
	if !errors.Is(setErr, ErrInvalidFrontmatter) {
		t.Fatalf("Set(empty key) error = %v, want ErrInvalidFrontmatter", setErr)
	}
	if clone.Tag != "!!null" {
		t.Fatalf("cloneYAMLNode(nil).Tag = %q, want !!null", clone.Tag)
	}
}

func TestFrontmatterTagsAndScalarHelpers(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter := NewFrontmatter()
	if err := frontmatter.Set("tags", scalarNode("!!str", "not-a-sequence")); err != nil {
		t.Fatalf("Set(tags) error = %v", err)
	}
	sequenceFM := NewFrontmatter()
	if err := sequenceFM.Set("tags", sequenceNode(mappingNode("x", scalarNode("!!str", "y")))); err != nil {
		t.Fatalf("Set(tags sequence) error = %v", err)
	}

	// Act / Assert.
	if frontmatter.Tags() != nil {
		t.Fatalf("Tags(non-sequence) = %#v, want nil", frontmatter.Tags())
	}
	if got := sequenceFM.Tags(); len(got) != 0 {
		t.Fatalf("Tags(mapping item) = %#v, want empty", got)
	}
	if value, ok := displayString(nil); ok || value != "" {
		t.Fatalf("displayString(nil) = %q, %v; want empty false", value, ok)
	}
	if value, ok := displayString(mappingNode("x", scalarNode("!!str", "y"))); ok || value != "" {
		t.Fatalf("displayString(mapping) = %q, %v; want empty false", value, ok)
	}
	if value, ok := displayString(scalarNode("!!binary", "abc")); ok || value != "" {
		t.Fatalf("displayString(unsupported scalar) = %q, %v; want empty false", value, ok)
	}
	if !isEmptyYAMLValue(&yaml.Node{Kind: yaml.SequenceNode}) {
		t.Fatal("empty sequence should be empty")
	}
	if isEmptyYAMLValue(&yaml.Node{Kind: yaml.AliasNode}) {
		t.Fatal("alias node should not be empty")
	}
}

func TestIndexEdgePaths(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeIndexDoc(t, root, "docs/no-title.md", "Guide", "", "No title.")
	writeFile(t, root, "docs/bad.md", "---\ntype: Bad\n")
	writeFile(t, root, "docs/skip.txt", "ignore")
	writeFile(t, root, "plain.md", "# No frontmatter\n")

	// Act.
	emptyText := BuildIndexText(nil)
	defaultEmpty := DefaultSynthesizeDescription("", nil)
	defaultText := DefaultSynthesizeDescription("", []IndexChild{{Title: "A"}, {Title: "B"}})
	written, err := RegenerateIndexesWith(root, nil)
	missingEntries, missingEntriesErr := indexEntriesForDirectory(root, filepath.Join(root, "missing"), nil)
	missingDoc, missingDocOK := loadIndexDocument(filepath.Join(root, "missing.md"))
	badDoc, badDocOK := loadIndexDocument(filepath.Join(root, "docs", "bad.md"))
	_, missingDirsErr := directoriesToIndex(filepath.Join(root, "missing"))

	// Assert.
	if emptyText != "" {
		t.Fatalf("BuildIndexText(nil) = %q, want empty", emptyText)
	}
	if defaultEmpty != "" {
		t.Fatalf("DefaultSynthesizeDescription(nil) = %q, want empty", defaultEmpty)
	}
	if defaultText != "Contains 2: A, B." {
		t.Fatalf("DefaultSynthesizeDescription() = %q, want Contains 2: A, B.", defaultText)
	}
	if err != nil {
		t.Fatalf("RegenerateIndexesWith(nil synth) error = %v", err)
	}
	if len(written) == 0 {
		t.Fatal("RegenerateIndexesWith(nil synth) wrote no files")
	}
	docsIndex := readFile(t, root, "docs/index.md")
	if !strings.Contains(docsIndex, "[no-title](no-title.md)") {
		t.Fatalf("docs/index.md = %q, want file-stem title fallback", docsIndex)
	}
	if missingEntriesErr == nil || missingEntries != nil {
		t.Fatalf("indexEntriesForDirectory(missing) = %#v, %v; want nil error", missingEntries, missingEntriesErr)
	}
	if missingDocOK || missingDoc.Body != "" {
		t.Fatalf("loadIndexDocument(missing) = %#v, %v; want false", missingDoc, missingDocOK)
	}
	if badDocOK || badDoc.Body != "" {
		t.Fatalf("loadIndexDocument(bad) = %#v, %v; want false", badDoc, badDocOK)
	}
	if missingDirsErr == nil {
		t.Fatal("directoriesToIndex(missing) error = nil, want error")
	}
}

func TestRegenerateIndexesStatErrorAndEmptyDirectoryEntries(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeFile(t, root, "bad.md", "---\ntype: Broken\n")

	// Act.
	badPathWritten, badPathErr := RegenerateIndexesWith(string([]byte{0}), nil)
	written, err := RegenerateIndexes(root)

	// Assert.
	if badPathErr == nil || badPathWritten != nil {
		t.Fatalf("RegenerateIndexesWith(NUL) = %#v, %v; want nil error", badPathWritten, badPathErr)
	}
	if err != nil {
		t.Fatalf("RegenerateIndexes(bad doc root) error = %v", err)
	}
	if len(written) != 0 {
		t.Fatalf("RegenerateIndexes(bad doc root) wrote %d files, want 0", len(written))
	}
}

func TestRegenerateIndexesDirectoryRemovedDuringProcessing(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeIndexDoc(t, root, "a/one.md", "Note", "One", "")
	writeIndexDoc(t, root, "b/two.md", "Note", "Two", "Two.")
	removed := false
	synth := func(_ string, _ []IndexChild) string {
		if !removed {
			removed = true
			if err := os.RemoveAll(filepath.Join(root, "b")); err != nil {
				t.Fatalf("RemoveAll(b) error = %v", err)
			}
		}
		return "removed b"
	}

	// Act.
	_, err := RegenerateIndexesWith(root, synth)

	// Assert.
	if err == nil {
		t.Fatal("RegenerateIndexesWith(removed directory) error = nil, want error")
	}
}

func TestRegenerateIndexesWriteError(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeIndexDoc(t, root, "a.md", "Note", "A", "A.")
	if err := os.Mkdir(filepath.Join(root, "index.md"), 0o755); err != nil {
		t.Fatalf("Mkdir(index.md) error = %v", err)
	}

	// Act.
	_, err := RegenerateIndexes(root)

	// Assert.
	if err == nil {
		t.Fatal("RegenerateIndexes() error = nil, want write error")
	}
}

func TestLinkEdgePaths(t *testing.T) {
	t.Parallel()

	// Arrange.
	source := mustParseConceptID(t, "a/source")
	kinds := []LinkKind{LinkAbsolute, LinkRelative, LinkExternal, LinkAnchor, LinkOther, LinkKind(99)}

	// Act / Assert.
	for _, kind := range kinds {
		if kind.String() == "" {
			t.Fatalf("LinkKind(%d).String() returned empty", kind)
		}
	}
	if _, ok := (Link{Target: "/dir/", Kind: LinkAbsolute}).Resolve(source); ok {
		t.Fatal("absolute directory link resolved, want false")
	}
	if _, ok := (Link{Target: "", Kind: LinkRelative}).Resolve(source); ok {
		t.Fatal("empty relative link resolved, want false")
	}
	if _, ok := (Link{Target: "../..", Kind: LinkRelative}).Resolve(source); ok {
		t.Fatal("traversal-only relative link resolved, want false")
	}
	if got := stripAnchor("/a.md"); got != "/a.md" {
		t.Fatalf("stripAnchor(no anchor) = %q, want /a.md", got)
	}
	if got := stripAnchor("/a.md#section"); got != "/a.md" {
		t.Fatalf("stripAnchor(anchor) = %q, want /a.md", got)
	}
	if _, ok := conceptIDFromNormalizedSegments(nil); ok {
		t.Fatal("conceptIDFromNormalizedSegments(nil) ok = true, want false")
	}
	if _, ok := conceptIDFromNormalizedSegments([]string{"."}); ok {
		t.Fatal("conceptIDFromNormalizedSegments(invalid) ok = true, want false")
	}
}

func TestMarkdownScannerFailurePaths(t *testing.T) {
	t.Parallel()

	// Arrange.
	body := strings.Join([]string{
		"Unclosed [link",
		"No destination [x]",
		"Unclosed destination [x](/a.md",
		"Escaped [x\\]](/escaped.md)",
		"Escaped destination [x](/a\\).md)",
		"[no-title](target.md plain suffix)",
	}, "\n")

	// Act.
	links := ExtractLinks(body)

	// Assert.
	targets := make([]string, 0, len(links))
	for _, link := range links {
		targets = append(targets, link.Target)
	}
	want := []string{"/escaped.md", "/a\\).md", "target.md plain suffix"}
	if !reflect.DeepEqual(targets, want) {
		t.Fatalf("ExtractLinks targets = %#v, want %#v", targets, want)
	}
}

func TestCitationFailurePaths(t *testing.T) {
	t.Parallel()

	// Arrange.
	body := "# Citations\n" +
		"not a citation\n" +
		"[x] invalid number\n" +
		"[1 no close\n" +
		"[2] [broken](https://example.com\n"

	// Act.
	citations := ExtractCitations(body)

	// Assert.
	if got, want := len(citations), 1; got != want {
		t.Fatalf("len(ExtractCitations()) = %d, want %d", got, want)
	}
	if citations[0].Number != 2 || citations[0].Target != "" {
		t.Fatalf("citation = %#v, want number 2 without parsed target", citations[0])
	}
}

func TestLogMultipleDays(t *testing.T) {
	t.Parallel()

	// Arrange.
	text := "## 2026-05-23\n* one\n\n## 2026-05-22\n* two\n"

	// Act.
	log := ParseLog(text)

	// Assert.
	if got, want := len(log.Days), 2; got != want {
		t.Fatalf("len(Days) = %d, want %d", got, want)
	}
	if got, want := log.Days[0].Date, "2026-05-23"; got != want {
		t.Fatalf("Days[0].Date = %q, want %q", got, want)
	}
}
