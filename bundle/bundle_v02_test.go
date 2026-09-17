package bundle

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBundleCapturesAssetsAndResolvesPathValues(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeFile(t, root, "computations/revenue.md", "---\ntype: Attested Computation\ncomputation: ../references/revenue.sql\n---\nbody\n")
	writeFile(t, root, "references/revenue.sql", "SELECT 1;\n")
	writeFile(t, root, "references/check.py", "print('ok')\n")
	writeFile(t, root, "references/config.json", "{}\n")
	loaded, err := LoadBundle(root)
	if err != nil {
		t.Fatal(err)
	}
	concept, ok := loaded.Get(mustParseConceptID(t, "computations/revenue"))
	if !ok {
		t.Fatal("concept missing")
	}

	// Act.
	files := loaded.Files()
	assets := loaded.AssetFiles()
	sql, sqlOK := loaded.ReadFile(filepath.Join(root, "references/revenue.sql"))
	resolved, resolvedOK := loaded.ResolvePathValueFor(concept.Path, "../references/revenue.sql", PathFieldConceptResource)
	resolvedSQL, resolvedSQLOK := loaded.ReadFile(resolved.Path)
	broken, brokenOK := loaded.ResolvePathValueFor(concept.Path, "../references/missing.sql", PathFieldConceptResource)
	scope, scopeOK := loaded.ResolvePathValueFor(concept.Path, "all queries in BigQuery project X", PathFieldConceptResource)
	spacedComputation, spacedOK := loaded.ResolvePathValueFor(concept.Path, "../references/missing computation.sql", PathFieldComputation)
	sourceAmbiguous, ambiguousOK := loaded.ResolvePathValueFor(concept.Path, "project/dataset.v1:events", PathFieldSourceResource)
	unknown, unknownOK := loaded.ResolvePathValueFor(concept.Path, "../references/revenue.sql", PathValueField("future-field"))

	// Assert.
	if len(files) != 4 || len(assets) != 3 || len(loaded.MarkdownFiles()) != 1 {
		t.Fatalf("Files/AssetFiles/MarkdownFiles = %v/%v/%v", files, assets, loaded.MarkdownFiles())
	}
	if !sqlOK || string(sql) != "SELECT 1;\n" {
		t.Fatalf("ReadFile(sql) = %q, %v", sql, sqlOK)
	}
	sql[0] = 'X'
	again, _ := loaded.ReadFile(filepath.Join(root, "references/revenue.sql"))
	if string(again) != "SELECT 1;\n" {
		t.Fatal("ReadFile() did not return a defensive copy")
	}
	if !resolvedOK || resolved.Path != "references/revenue.sql" || !resolved.Exists || resolved.Kind != PathValueRelative {
		t.Fatalf("resolved = %#v, %v", resolved, resolvedOK)
	}
	if !resolvedSQLOK || string(resolvedSQL) != "SELECT 1;\n" {
		t.Fatalf("ReadFile(resolved.Path) = %q, %v", resolvedSQL, resolvedSQLOK)
	}
	if !brokenOK || broken.Exists || broken.Path != "references/missing.sql" {
		t.Fatalf("broken = %#v, %v", broken, brokenOK)
	}
	if !scopeOK || scope.Kind != PathValueRelative || scope.Path == "" {
		t.Fatalf("generic space path = %#v, %v", scope, scopeOK)
	}
	if !spacedOK || spacedComputation.Kind != PathValueRelative || spacedComputation.Path != "references/missing computation.sql" {
		t.Fatalf("spaced computation = %#v, %v", spacedComputation, spacedOK)
	}
	if !ambiguousOK || sourceAmbiguous.Kind != PathValueAmbiguous || sourceAmbiguous.Path != "" {
		t.Fatalf("ambiguous source = %#v, %v", sourceAmbiguous, ambiguousOK)
	}
	if unknownOK || unknown != (ResolvedPathValue{}) {
		t.Fatalf("unknown field = %#v, %v", unknown, unknownOK)
	}
}

func TestResolvePathValueSplitsSuffixBeforePathNormalization(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeFile(t, root, "docs/concept.md", "---\ntype: Note\n---\n")
	writeFile(t, root, "docs/target.sql", "relative")
	writeFile(t, root, "target.sql", "bundle relative")
	writeFile(t, root, "decoy.sql", "must not capture")
	loaded, err := LoadBundle(root)
	if err != nil {
		t.Fatal(err)
	}
	fields := []PathValueField{
		PathFieldSourceResource,
		PathFieldConceptResource,
		PathFieldComputation,
		PathFieldExecutorResource,
		PathFieldAttesterResource,
	}
	tests := []struct {
		name   string
		value  string
		path   string
		kind   PathValueKind
		suffix string
	}{
		{
			name:   "relative query traversal payload",
			value:  "./target.sql?../../decoy.sql#receipt",
			path:   "docs/target.sql",
			kind:   PathValueRelative,
			suffix: "?../../decoy.sql#receipt",
		},
		{
			name:   "bundle relative fragment traversal payload",
			value:  "/target.sql#../../docs/target.sql?receipt",
			path:   "target.sql",
			kind:   PathValueBundleRelative,
			suffix: "#../../docs/target.sql?receipt",
		},
	}

	for _, field := range fields {
		field := field
		for _, test := range tests {
			test := test
			t.Run(string(field)+"/"+test.name, func(t *testing.T) {
				t.Parallel()

				// Act.
				resolved, ok := loaded.ResolvePathValueFor("docs/concept.md", test.value, field)

				// Assert.
				if !ok || resolved.Path != test.path || resolved.Suffix != test.suffix ||
					resolved.Kind != test.kind || !resolved.Exists || resolved.Raw != test.value {
					t.Fatalf("ResolvePathValueFor() = %#v, %v", resolved, ok)
				}
			})
		}
	}
}

func TestSourceResourceResolutionPreservesScopeAndAmbiguity(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeFile(t, root, "docs/a.md", "---\ntype: Note\n---\nbody\n")
	writeFile(t, root, "docs/existing source.v1", "captured")
	loaded, err := LoadBundle(root)
	if err != nil {
		t.Fatal(err)
	}
	documentPath := filepath.Join(root, "docs/a.md")

	tests := []struct {
		name   string
		value  string
		kind   PathValueKind
		path   string
		exists bool
	}{
		{name: "captured exact wins", value: "existing source.v1", kind: PathValueRelative, path: "docs/existing source.v1", exists: true},
		{name: "natural language scope", value: "all queries in project X", kind: PathValueScope},
		{name: "slash descriptor ambiguous", value: "project/dataset", kind: PathValueAmbiguous},
		{name: "colon descriptor ambiguous", value: "project:dataset", kind: PathValueAmbiguous},
		{name: "dot descriptor ambiguous", value: "project.dataset", kind: PathValueAmbiguous},
		{name: "explicit broken local", value: "./missing source.sql", kind: PathValueRelative, path: "docs/missing source.sql"},
		{name: "absolute URL", value: "https://example.test/source", kind: PathValueURL},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act.
			got, ok := loaded.ResolvePathValueFor(documentPath, tt.value, PathFieldSourceResource)

			// Assert.
			if !ok || got.Kind != tt.kind || got.Path != tt.path || got.Exists != tt.exists {
				t.Fatalf("ResolvePathValueFor() = %#v, %v; want kind=%q path=%q exists=%v", got, ok, tt.kind, tt.path, tt.exists)
			}
		})
	}
}

func TestPathValueResolutionClassifiesLocalBeforeURLSyntaxAcrossAllFields(t *testing.T) {
	t.Parallel()

	fields := []PathValueField{
		PathFieldConceptResource,
		PathFieldSourceResource,
		PathFieldComputation,
		PathFieldExecutorResource,
		PathFieldAttesterResource,
	}
	loaded := &Bundle{contents: map[string][]byte{
		"docs/a.md":                   nil,
		"docs/captured%zz.sql":        nil,
		"docs/encoded%2Fname.sql":     nil,
		"shared%zz.sql":               nil,
		"bundle%literal.sql":          nil,
		"docs/https:/captured%zz.sql": nil,
	}}
	for _, field := range fields {
		field := field
		for _, tt := range []struct {
			name, value, path, suffix string
			kind                      PathValueKind
			exists                    bool
		}{
			{name: "posix captured malformed escape", value: "captured%zz.sql", path: "docs/captured%zz.sql", kind: PathValueRelative, exists: true},
			{name: "posix parent captured malformed escape", value: "../shared%zz.sql", path: "shared%zz.sql", kind: PathValueRelative, exists: true},
			{name: "encoded relative identity is not decoded", value: "encoded%2Fname.sql", path: "docs/encoded%2Fname.sql", kind: PathValueRelative, exists: true},
			{name: "bundle captured literal percent", value: "/bundle%literal.sql", path: "bundle%literal.sql", kind: PathValueBundleRelative, exists: true},
			{name: "explicit relative malformed escape", value: "./missing%zz.sql", path: "docs/missing%zz.sql", kind: PathValueRelative},
			{name: "captured local wins before URL-looking parse", value: "https://captured%zz.sql", path: "docs/https:/captured%zz.sql", kind: PathValueRelative, exists: true},
			{name: "captured local query and fragment", value: "captured%zz.sql?query#fragment", path: "docs/captured%zz.sql", suffix: "?query#fragment", kind: PathValueRelative, exists: true},
			{name: "percent encoded valid URL", value: "https://example.test/a%20b", kind: PathValueURL},
			{name: "percent encoded URL suffix", value: "https://example.test/a%20b?query=yes#fragment", suffix: "?query=yes#fragment", kind: PathValueURL},
			{name: "malformed URL-looking escape", value: "https://example.test/%zz", kind: PathValueAmbiguous},
			{name: "malformed URL-looking suffix escape", value: "https://example.test/a?query=%zz#fragment", suffix: "?query=%zz#fragment", kind: PathValueAmbiguous},
			{name: "malformed mail URL-looking escape", value: "mailto:user%zz@example.test", kind: PathValueAmbiguous},
			{name: "windows drive slash", value: "C:/work/file.sql", kind: PathValueAmbiguous},
			{name: "windows drive backslash", value: `C:\work\file.sql`, kind: PathValueAmbiguous},
			{name: "windows UNC backslash", value: `\\server\share\file.sql`, kind: PathValueAmbiguous},
			{name: "fragment only", value: "#fragment", suffix: "#fragment", kind: PathValueAmbiguous},
			{name: "query only", value: "?query", suffix: "?query", kind: PathValueAmbiguous},
		} {
			tt := tt
			t.Run(string(field)+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				// Act.
				got, ok := loaded.ResolvePathValueFor("docs/a.md", tt.value, field)

				// Assert.
				if !ok || got.Raw != tt.value || got.Suffix != tt.suffix ||
					got.Kind != tt.kind || got.Path != tt.path || got.Exists != tt.exists {
					t.Fatalf("ResolvePathValueFor() = (%#v, %v), want kind=%q path=%q suffix=%q exists=%v", got, ok, tt.kind, tt.path, tt.suffix, tt.exists)
				}
			})
		}
	}
}

func TestNilBundlePathValueResolutionIsSyntacticAndNeverExists(t *testing.T) {
	t.Parallel()

	// Arrange.
	var loaded *Bundle

	// Act.
	relative, relativeOK := loaded.ResolvePathValueFor("docs/a.md", "asset.sql#part", PathFieldComputation)
	urlValue, urlOK := loaded.ResolvePathValueFor("docs/a.md", "https://example.test/asset.sql#part", PathFieldExecutorResource)
	scope, scopeOK := loaded.ResolvePathValueFor("docs/a.md", "all queries in project X", PathFieldSourceResource)
	suffixOnly, suffixOK := loaded.ResolvePathValueFor("docs/a.md", "#part", PathFieldAttesterResource)

	// Assert.
	if !relativeOK || relative.Kind != PathValueRelative || relative.Path != "docs/asset.sql" ||
		relative.Suffix != "#part" || relative.Exists {
		t.Fatalf("nil relative resolution = %#v, %v", relative, relativeOK)
	}
	if !urlOK || urlValue.Kind != PathValueURL || urlValue.Path != "" ||
		urlValue.Suffix != "#part" || urlValue.Exists {
		t.Fatalf("nil URL resolution = %#v, %v", urlValue, urlOK)
	}
	if !scopeOK || scope.Kind != PathValueScope || scope.Path != "" || scope.Exists {
		t.Fatalf("nil scope resolution = %#v, %v", scope, scopeOK)
	}
	if !suffixOK || suffixOnly.Kind != PathValueAmbiguous || suffixOnly.Path != "" ||
		suffixOnly.Suffix != "#part" || suffixOnly.Exists {
		t.Fatalf("nil suffix-only resolution = %#v, %v", suffixOnly, suffixOK)
	}
}

func TestNilBundlePathValueClassificationMatrixAcrossAllFields(t *testing.T) {
	t.Parallel()

	var loaded *Bundle
	fields := []PathValueField{
		PathFieldConceptResource,
		PathFieldSourceResource,
		PathFieldComputation,
		PathFieldExecutorResource,
		PathFieldAttesterResource,
	}
	tests := []struct {
		name, value, path, suffix string
		kind                      PathValueKind
	}{
		{name: "explicit POSIX literal percent", value: "./asset%zz.sql#part", path: "docs/asset%zz.sql", suffix: "#part", kind: PathValueRelative},
		{name: "bundle relative literal percent", value: "/asset%zz.sql?query", path: "asset%zz.sql", suffix: "?query", kind: PathValueBundleRelative},
		{name: "valid encoded URL", value: "https://example.test/a%20b#part", suffix: "#part", kind: PathValueURL},
		{name: "valid scheme relative URL", value: "//example.test/a%20b", kind: PathValueURL},
		{name: "malformed URL-looking escape", value: "https://example.test/%zz", kind: PathValueAmbiguous},
		{name: "windows drive", value: `C:\work\file.sql`, kind: PathValueAmbiguous},
		{name: "windows UNC", value: `\\server\share\file.sql`, kind: PathValueAmbiguous},
	}
	for _, field := range fields {
		field := field
		for _, test := range tests {
			test := test
			t.Run(string(field)+"/"+test.name, func(t *testing.T) {
				t.Parallel()

				got, ok := loaded.ResolvePathValueFor("docs/a.md", test.value, field)

				if !ok || got.Kind != test.kind || got.Path != test.path ||
					got.Suffix != test.suffix || got.Exists {
					t.Fatalf("ResolvePathValueFor() = (%#v, %v), want kind=%q path=%q suffix=%q exists=false", got, ok, test.kind, test.path, test.suffix)
				}
			})
		}
	}
}

func TestSourcesDoNotCreateSemanticFragmentsOrRelations(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeFile(t, root, "a.md", `---
type: Note
sources:
  - id: duplicate
    resource: one
    relations:
      uses:
        - target: target
  - id: duplicate
    resource: two
parts:
  - id: real-extension
relations:
  uses:
    - target: target
---
body
`)
	writeFile(t, root, "target.md", "---\ntype: Note\n---\nbody\n")
	loaded, err := LoadBundle(root)
	if err != nil {
		t.Fatal(err)
	}
	a := mustParseConceptID(t, "a")

	// Act.
	subresources := loaded.Subresources(a)
	relations := loaded.SemanticLinksFrom(a)
	diagnostics := loaded.RelationDiagnostics()

	// Assert.
	if !reflect.DeepEqual(subresources, []string{"real-extension"}) {
		t.Fatalf("Subresources(a) = %#v", subresources)
	}
	if len(relations) != 1 || relations[0].Source.Fragment != "" {
		t.Fatalf("SemanticLinksFrom(a) = %#v", relations)
	}
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == "duplicate_fragment" && diagnostic.RawTarget == "duplicate" {
			t.Fatalf("source ids produced duplicate_fragment: %#v", diagnostics)
		}
	}
}

func TestMalformedOptionalV02FamiliesRemainLoadableAndRaw(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeFile(t, root, "a.md", `---
type: Note
sources: wrong
usage_window: [wrong]
generated: wrong
verified: [wrong, {by: process:ok, at: 2026-07-29T00:00:00Z}]
status: 17
stale_after: wrong
parameters: wrong
executor: wrong
x-extra: {nested: keep}
---
body
`)

	// Act.
	loaded, err := LoadBundle(root)
	if err != nil {
		t.Fatal(err)
	}
	concept, ok := loaded.Get(mustParseConceptID(t, "a"))

	// Assert.
	if !ok || len(loaded.ParseErrors()) != 0 {
		t.Fatalf("load result = %#v, errors %#v", concept, loaded.ParseErrors())
	}
	for _, key := range []string{"sources", "usage_window", "generated", "verified", "parameters", "executor", "x-extra"} {
		if _, present := concept.Document.Frontmatter.Get(key); !present {
			t.Fatalf("Get(%q) absent after permissive loading", key)
		}
	}
	if got := concept.Document.TrustTier(); got != TrustMachineConfirmed {
		t.Fatalf("TrustTier() = %q, want machine-confirmed from valid list event", got)
	}
	serialized, err := concept.Document.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	reparsed, err := ParseDocument(serialized)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := reparsed.Frontmatter.Get("x-extra"); !present {
		t.Fatal("unknown nested extension disappeared on serialize")
	}
}

func TestDocumentFrontmatterYAMLIsExactAndDefensive(t *testing.T) {
	t.Parallel()

	// Arrange.
	source := "---\r\n# comment\r\ntype: 'Note'\r\ncustom: {x: 1}\r\n---\r\nbody\r\n"
	document, err := ParseDocument(source)
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	raw := document.FrontmatterYAML()
	_ = document.Sources()
	serialized, serializeErr := document.Serialize()
	raw[0] = 'X'
	again := document.FrontmatterYAML()

	// Assert.
	want := "# comment\r\ntype: 'Note'\r\ncustom: {x: 1}\r\n"
	if string(again) != want {
		t.Fatalf("FrontmatterYAML() = %q, want %q", again, want)
	}
	if serializeErr != nil {
		t.Fatal(serializeErr)
	}
	if !strings.Contains(serialized, want) {
		t.Fatalf("Serialize() lost raw frontmatter bytes:\n%q", serialized)
	}
	if NewDocument(NewFrontmatter(), "").FrontmatterYAML() != nil {
		t.Fatal("constructed FrontmatterYAML() != nil")
	}
}

func TestReturnedConceptFrontmatterMutationDoesNotChangeBundle(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	sourceText := "---\ntype: Note\nparts:\n  - id: original\n---\nbody\n"
	writeFile(t, root, "a.md", sourceText)
	loaded, err := LoadBundle(root)
	if err != nil {
		t.Fatal(err)
	}
	id := mustParseConceptID(t, "a")
	concept, ok := loaded.Get(id)
	if !ok {
		t.Fatal("Get(a) = false")
	}

	// Act.
	if err := concept.Document.Frontmatter.SetString("type", "Mutated"); err != nil {
		t.Fatal(err)
	}
	parts, _ := concept.Document.Frontmatter.Get("parts")
	parts.Content[0].Content[1].Value = "caller-node"
	again, _ := loaded.Get(id)
	captured, _ := loaded.ReadFile(filepath.Join(root, "a.md"))

	// Assert.
	if typ, _ := again.Document.Frontmatter.Type(); typ != "Note" {
		t.Fatalf("bundle type = %q, want Note", typ)
	}
	if got := loaded.Subresources(id); !reflect.DeepEqual(got, []string{"original"}) {
		t.Fatalf("Subresources(a) = %#v, want original", got)
	}
	if string(captured) != sourceText {
		t.Fatalf("captured bytes changed = %q", captured)
	}
	if raw := string(again.Document.FrontmatterYAML()); raw != "type: Note\nparts:\n  - id: original\n" {
		t.Fatalf("FrontmatterYAML() = %q", raw)
	}
}
