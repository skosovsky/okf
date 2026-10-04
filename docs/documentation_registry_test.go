package docs_test

import (
	"crypto/sha256"
	"encoding/hex"
	"go/ast"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/skosovsky/okf/internal/mcpserver"
)

type documentationEdition struct {
	Path string `json:"path"`
	URL  string `json:"url"`
}
type documentationEntry struct {
	ID              string               `json:"id"`
	Purpose         string               `json:"purpose"`
	Status          string               `json:"status"`
	Source          string               `json:"source"`
	SourceRevision  string               `json:"source_revision"`
	BaselineSHA256  string               `json:"baseline_sha256"`
	PreserveSource  bool                 `json:"preserve_source"`
	NormativeSource *string              `json:"normative_source"`
	EN              documentationEdition `json:"en"`
	RU              documentationEdition `json:"ru"`
}
type documentationRegistry struct {
	SchemaVersion    int                  `json:"schema_version"`
	BaselineRevision string               `json:"baseline_revision"`
	Locales          []string             `json:"locales"`
	Documents        []documentationEntry `json:"documents"`
	Exclusions       []struct {
		Path   string `json:"path"`
		Reason string `json:"reason"`
	} `json:"exclusions"`
	ExclusionRules []string `json:"exclusion_rules"`
}

func loadDocumentationRegistry(t *testing.T) documentationRegistry {
	t.Helper()
	var registry documentationRegistry
	decodeJSONFile(t, filepath.Join(repositoryRoot(t), "docs", "_data", "documentation.yml"), &registry)
	return registry
}

// Original 95 records frozen before editorial changes, expanded by the two
// human-readable fixture README scope omissions identified independently.
// The original baseline is retained unchanged in task evidence; this additive
// 97-record lock protects the complete agreed scope. New documents have no baseline hash;
// deleting a baseline document and its two editions must not silently reduce
// coverage. The digest covers sorted source path + NUL + original SHA + LF.
func TestDocumentationBaselineInventoryRemainsComplete(t *testing.T) {
	t.Parallel()
	registry := loadDocumentationRegistry(t)
	records := []string{}
	for _, document := range registry.Documents {
		if document.BaselineSHA256 == "" {
			if document.SourceRevision != "" {
				t.Errorf("new document %s claims baseline revision without baseline bytes", document.ID)
			}
			continue
		}
		if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(document.BaselineSHA256) {
			t.Errorf("%s invalid baseline digest", document.ID)
		}
		if document.SourceRevision != registry.BaselineRevision {
			t.Errorf("%s baseline revision changed", document.ID)
		}
		records = append(records, document.Source+"\x00"+document.BaselineSHA256+"\n")
	}
	sort.Strings(records)
	digest := sha256.Sum256([]byte(strings.Join(records, "")))
	const wantDigest = "a96b4d3aced216425631a22ee6c5c70a26b9f83554cd05948aa27d49d19ce24b"
	if len(records) != 97 || hex.EncodeToString(digest[:]) != wantDigest {
		t.Errorf("frozen baseline inventory changed: count=%d sha256=%s", len(records), hex.EncodeToString(digest[:]))
	}
}

// These checks are structural. Matching anchors or inventory counts cannot
// establish a good translation; every pair also needs independent human review.
func TestDocumentationRegistryInventoryAndEditions(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	registry := loadDocumentationRegistry(t)
	if registry.SchemaVersion != 1 || strings.Join(registry.Locales, ",") != "en,ru" || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(registry.BaselineRevision) {
		t.Fatalf("invalid registry header: schema=%d locales=%v revision=%q", registry.SchemaVersion, registry.Locales, registry.BaselineRevision)
	}
	ids, paths, urls := map[string]bool{}, map[string]bool{}, map[string]bool{}
	covered := map[string]bool{}
	for _, document := range registry.Documents {
		if document.ID == "" || ids[document.ID] {
			t.Errorf("empty or duplicate document ID %q", document.ID)
		}
		ids[document.ID] = true
		if document.Purpose != "guide" && document.Purpose != "contract" && document.Purpose != "historical" {
			t.Errorf("%s invalid purpose %q", document.ID, document.Purpose)
		}
		if document.Status != "current" && document.Status != "historical" {
			t.Errorf("%s invalid status %q", document.ID, document.Status)
		}
		if document.Purpose == "historical" && document.Status != "historical" {
			t.Errorf("%s historical document advertised current", document.ID)
		}
		if !validRegistryPath(document.Source) {
			t.Errorf("%s unsafe source %q", document.ID, document.Source)
			continue
		}
		assertExists(t, root, document.ID, document.Source)
		covered[document.Source] = true
		for _, edition := range []documentationEdition{document.EN, document.RU} {
			if !validRegistryPath(edition.Path) {
				t.Errorf("%s unsafe edition path %q", document.ID, edition.Path)
				continue
			}
			if paths[edition.Path] {
				t.Errorf("duplicate edition path %q", edition.Path)
			}
			paths[edition.Path] = true
			covered[edition.Path] = true
			assertExists(t, root, document.ID, edition.Path)
			if edition.URL == "" || urls[edition.URL] {
				t.Errorf("%s empty or duplicate route %q", document.ID, edition.URL)
			}
			urls[edition.URL] = true
			if strings.HasPrefix(edition.URL, "/") {
				content := readText(t, filepath.Join(root, edition.Path))
				match := regexp.MustCompile(`(?m)^permalink:\s*['"]?([^\s'"]+)['"]?\s*$`).FindStringSubmatch(content)
				if len(match) != 2 || match[1] != edition.URL {
					t.Errorf("%s route %s disagrees with permalink", edition.Path, edition.URL)
				}
			}
		}
		if document.EN.Path == document.RU.Path {
			t.Errorf("%s shares one path for both languages", document.ID)
		}
		if document.PreserveSource {
			digest := sha256.Sum256([]byte(readText(t, filepath.Join(root, document.Source))))
			if hex.EncodeToString(digest[:]) != document.BaselineSHA256 {
				t.Errorf("preserved original %s changed", document.Source)
			}
		}
	}
	for _, exclusion := range registry.Exclusions {
		if !validRegistryPath(exclusion.Path) || strings.TrimSpace(exclusion.Reason) == "" {
			t.Errorf("invalid exclusion %#v", exclusion)
			continue
		}
		if covered[exclusion.Path] {
			t.Errorf("document %s is both included and excluded", exclusion.Path)
		}
		if _, err := os.Lstat(filepath.Join(root, exclusion.Path)); err != nil {
			t.Errorf("excluded file missing %s: %v", exclusion.Path, err)
		}
		covered[exclusion.Path] = true
	}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative := mustRelative(t, root, path)
		if entry.IsDir() {
			if entry.Name() != "." && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" || entry.Name() == "_site" || entry.Name() == "vendor") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".md" && entry.Name() != "README.txt" {
			return nil
		}
		if !covered[relative] {
			t.Errorf("documentation omitted from registry or explicit exclusions: %s", relative)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func validRegistryPath(path string) bool {
	return path != "" && !filepath.IsAbs(path) && !escaped(path) && filepath.ToSlash(filepath.Clean(path)) == path && !strings.Contains(path, "\\")
}

func TestNormativeEditionsIdentifyCanonicalSourceAndRevision(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	registry := loadDocumentationRegistry(t)
	canonicalRoutes := map[string]string{}
	for _, document := range registry.Documents {
		canonicalRoutes[document.Source] = document.EN.URL
	}
	for _, document := range registry.Documents {
		if document.NormativeSource == nil {
			continue
		}
		if !validRegistryPath(*document.NormativeSource) {
			t.Errorf("%s invalid canonical owner", document.ID)
			continue
		}
		assertExists(t, root, document.ID, *document.NormativeSource)
		if !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(document.SourceRevision) {
			t.Errorf("%s invalid normative revision", document.ID)
		}
		for _, edition := range []documentationEdition{document.EN, document.RU} {
			if edition.Path == *document.NormativeSource {
				continue
			}
			text := readText(t, filepath.Join(root, edition.Path))
			route := canonicalRoutes[*document.NormativeSource]
			ownerLinked := strings.Contains(text, *document.NormativeSource) || (route != "" && (strings.Contains(text, "'"+route+"'") || strings.Contains(text, "\""+route+"\"")))
			if !ownerLinked || !strings.Contains(text, document.SourceRevision) {
				t.Errorf("%s does not identify canonical owner %s at %s", edition.Path, *document.NormativeSource, document.SourceRevision)
			}
		}
	}
}

func explicitSectionIDs(content string) []string {
	content = markdownWithoutCode(content)
	pattern := regexp.MustCompile(`(?:<a\s+id=["']([^"']+)["'][^>]*>|\{#([A-Za-z0-9_-]+)\})`)
	result := []string{}
	for _, match := range pattern.FindAllStringSubmatch(content, -1) {
		result = append(result, match[1]+match[2])
	}
	sort.Strings(result)
	return result
}

func TestPrimaryGuideLanguageSwitchPreservesSectionIDs(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	primary := map[string]bool{"docs/index.md": true, "docs/quickstart.md": true, "docs/toolkit.md": true, "docs/skill.md": true, "docs/migration.md": true, "docs/getting-started-mcp.md": true, "docs/knowledge.md": true, "docs/knowledge-upkeep.md": true, "docs/knowledge-manual-review.md": true}
	for _, document := range loadDocumentationRegistry(t).Documents {
		if !primary[document.EN.Path] {
			continue
		}
		en := explicitSectionIDs(readText(t, filepath.Join(root, document.EN.Path)))
		ru := explicitSectionIDs(readText(t, filepath.Join(root, document.RU.Path)))
		if len(en) == 0 || strings.Join(en, "\n") != strings.Join(ru, "\n") {
			t.Errorf("%s section identifiers disagree: EN=%v RU=%v", document.ID, en, ru)
		}
		seen := map[string]bool{}
		for _, id := range en {
			if seen[id] {
				t.Errorf("%s duplicate section ID %q", document.ID, id)
			}
			seen[id] = true
		}
	}
}

func TestReferenceCataloguesMatchRegisteredToolsAndCLICommands(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	tools, err := mcpserver.ServerTools()
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 14 {
		t.Fatalf("registered MCP inventory changed: %d; update both catalogues", len(tools))
	}
	references := []string{"docs/reference.md", "docs/ru/reference.md"}
	for _, path := range references {
		content := readText(t, filepath.Join(root, path))
		for _, tool := range tools {
			if !strings.Contains(content, "`"+tool.Tool.Name+"`") {
				t.Errorf("%s omits registered MCP tool %s", path, tool.Tool.Name)
			}
			for _, direction := range []string{"input", "output"} {
				assertExists(t, root, path, "internal/mcpserver/contracts/"+tool.Tool.Name+"."+direction+".schema.json")
			}
		}
	}
	pkg := parseGoPackage(t, filepath.Join(root, "internal", "okfcli"))
	function := requireFunction(t, pkg, "runWithDependencies")
	commands := map[string]bool{}
	ast.Inspect(function.Body, func(node ast.Node) bool {
		clause, ok := node.(*ast.CaseClause)
		if !ok {
			return true
		}
		for _, expr := range clause.List {
			literal, ok := expr.(*ast.BasicLit)
			if !ok {
				continue
			}
			name, err := strconv.Unquote(literal.Value)
			if err == nil && !strings.HasPrefix(name, "-") {
				commands[name] = true
			}
		}
		return true
	})
	if !commands["search"] || !commands["setup"] || !commands["view"] {
		t.Fatalf("CLI dispatch inventory incomplete: %v", commands)
	}
	for _, path := range references {
		content := readText(t, filepath.Join(root, path))
		for command := range commands {
			if !strings.Contains(content, "`"+command+"`") && !strings.Contains(content, "okf "+command) {
				t.Errorf("%s omits CLI command %s", path, command)
			}
		}
	}
}
