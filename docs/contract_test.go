package docs_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/validator"
	"gopkg.in/yaml.v3"
)

// These tests deliberately lock published behavior and ownership boundaries,
// not implementation bodies. Package tests own the detailed wire and domain
// matrices; this package proves that public documentation still points at, and
// agrees with, those canonical owners.

var bilingualPages = [][2]string{
	{"README.md", "README.ru.md"},
	{"docs/index.md", "docs/ru/index.md"},
	{"docs/skill.md", "docs/ru/skill.md"},
	{"docs/toolkit.md", "docs/ru/toolkit.md"},
	{"docs/migration.md", "docs/ru/migration.md"},
}

var englishMigrationPages = []string{
	"README.md",
	"docs/migration.md",
	"docs/toolkit.md",
	"docs/skill.md",
	"skills/open-knowledge-format/references/examples.md",
}

var russianMigrationPages = []string{
	"README.ru.md",
	"docs/ru/migration.md",
	"docs/ru/toolkit.md",
	"docs/ru/skill.md",
	"skills/open-knowledge-format/SKILL.md",
	"skills/open-knowledge-format/references/migration-v01-v02.md",
}

func TestPublishedEnglishRussianSemanticParity(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	for _, pair := range bilingualPages {
		for _, token := range []string{"spec-v02.md", "0.2", "0.1", "generated", "sources", "verified"} {
			for _, path := range pair {
				if !strings.Contains(readText(t, filepath.Join(root, path)), token) {
					t.Errorf("%s is missing shared contract token %q", path, token)
				}
			}
		}
	}

	english := compactWhitespace(joinedDocumentation(t, root, []string{
		"README.md", "docs/index.md", "docs/skill.md", "docs/toolkit.md", "docs/migration.md",
	}))
	russian := compactWhitespace(joinedDocumentation(t, root, []string{
		"README.ru.md", "docs/ru/index.md", "docs/ru/skill.md", "docs/ru/toolkit.md", "docs/ru/migration.md",
	}))
	contracts := []struct {
		id      string
		english string
		russian string
	}{
		{"fallback.generated", "fall back to `timestamp` only if `generated` is wholly absent", "используй `timestamp` только если `generated` полностью отсутствует"},
		{"fallback.sources", "fall back to `# Citations` only if `sources` is absent", "используй `# Citations` только если `sources` отсутствует"},
		{"read.precedence", "v0.2 is the effective read", "effective read использует v0.2"},
		{"trust.axes", "surface trust, status, and staleness separately", "показывай trust, status и staleness отдельно"},
		{"migration.non-invention", "Migration never invents", "Migration не выдумывает"},
		{"runtime.inert", "are inert data", "inert data"},
		{"migration.root-last", "root `index.md` physical write/rename last", "physical write/rename root `index.md` последним"},
		{"migration.input-first", "Migration input validation runs before source resolution", "Migration input validation выполняется до source resolution"},
		{"migration.missing-document", "migration_document_missing", "migration_document_missing"},
		{"migration.zero-write", "path-for-path and byte-for-byte identical", "path-for-path и byte-for-byte"},
		{"migration.proof", "`proof.base_revision` is authoritative", "`proof.base_revision` authoritative"},
		{"migration.mcp-target-noop", "MCP is proofless and opens no store", "MCP остаётся proofless и не открывает store"},
		{"migration.cli-dry-run", "CLI dry-run builds a proof without opening the store", "CLI dry-run строит proof без открытия store"},
		{"migration.cli-write", "CLI `--write` commits an empty CAS", "CLI `--write` выполняет empty CAS"},
		{"patch.authorization", "Patch apply requires `expected_revision` plus its preview plan digest", "Patch apply требует `expected_revision` и preview plan digest"},
		{"usage-count.wire", "canonical decimal string matching `^(0|[1-9][0-9]*)$`", "canonical decimal string по `^(0|[1-9][0-9]*)$`"},
		{"actor.resource", "257+ bytes returns `resource_limit`, not invalid actor", "257+ bytes возвращает `resource_limit`, а не invalid actor"},
		{"selector.closed", "The `set_usage_window` selector is a closed union", "Selector `set_usage_window` — closed union"},
		{"proof.content-free", "never file bytes/frontmatter/body", "но не file bytes/frontmatter/body"},
		{"citations.dto", "[{path,entries:[{legacy_number?,legacy_entry?,source_id,title?,resource?}]}]", "[{path,entries:[{legacy_number?,legacy_entry?,source_id,title?,resource?}]}]"},
	}
	for _, contract := range contracts {
		if !strings.Contains(english, contract.english) {
			t.Errorf("English docs omit contract %s", contract.id)
		}
		if !strings.Contains(russian, contract.russian) {
			t.Errorf("Russian docs omit contract %s", contract.id)
		}
	}
}

func TestPublishedMigrationContractsArePresentOnEverySurface(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	contracts := []struct {
		id      string
		english string
		russian string
	}{
		{
			"input-before-source",
			"Migration input validation runs before source resolution",
			"Migration input validation выполняется до source resolution",
		},
		{
			"zero-write",
			"leaves the entire filesystem tree path-for-path and byte-for-byte identical",
			"оставляет всё filesystem tree идентичным path-for-path и byte-for-byte",
		},
		{
			"missing-document",
			"A missing path blocks with `migration_document_missing`",
			"Missing path блокируется с `migration_document_missing`",
		},
	}
	for _, page := range englishMigrationPages {
		content := compactWhitespace(readText(t, filepath.Join(root, page)))
		for _, contract := range contracts {
			if page == "skills/open-knowledge-format/references/examples.md" && contract.id == "zero-write" {
				continue
			}
			if !strings.Contains(content, contract.english) {
				t.Errorf("%s omits %s", page, contract.id)
			}
		}
	}
	for _, page := range russianMigrationPages {
		content := compactWhitespace(readText(t, filepath.Join(root, page)))
		for _, contract := range contracts {
			if !strings.Contains(content, contract.russian) {
				t.Errorf("%s omits %s", page, contract.id)
			}
		}
	}
}

func TestMigrationHandlersValidateBeforeSourceAndStoreBoundaries(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	pkg := parseGoPackage(t, filepath.Join(root, "internal", "mcpserver"))
	for _, name := range []string{"handlePreviewV02Migration", "handleApplyV02Migration"} {
		function := requireFunction(t, pkg, name)
		positions := callPositions(function)
		requireCallOrder(t, name, positions,
			"requireCanonicalToolInput",
			"requireValidatedBundlePath",
			"decodeMigrationArguments",
			"canonicalMigrationInput",
			"captureMigrationSource",
		)
	}

	// Filesystem and transactional APIs have one adapter owner each. New direct
	// callers are package-surface bypasses, regardless of filename or build tag.
	owners := map[string]map[string]bool{
		"storefs.OpenContext": {
			"applyMigrationPlan": true, "handleApplyConceptPatch": true,
			"openTransactionalStoreContext": true,
		},
		"bundle.FileSystemSource": {"captureMigrationSource": true, "loadBundleContext": true},
	}
	assertSelectorOwners(t, pkg, owners)

	cli := parseGoPackage(t, filepath.Join(root, "internal", "okfcli"))
	requireCallOrder(t, "cmdMigrateWithDependencies", callPositions(requireFunction(t, cli, "cmdMigrateWithDependencies")),
		"parseMigrateArgs",
		"loadCitationMappingsWithOpener",
		"validateMigrateInput",
		"buildMigrationReport",
	)
	requireCallOrder(t, "buildMigrationReport", callPositions(requireFunction(t, cli, "buildMigrationReport")),
		"dependencies.openSource",
		"dependencies.resolve",
		"mutation.PreflightV01ToV02MigrationInput",
		"dependencies.openStore",
	)
	assertSelectorOwners(t, cli, map[string]map[string]bool{
		"storefs.OpenContext":     {"productionMigrateDependencies": true},
		"dependencies.openSource": {"buildMigrationReport": true},
		"dependencies.openStore":  {"buildMigrationReport": true},
	})
}

type parsedPackage struct {
	files     map[string]*ast.File
	functions map[string]*ast.FuncDecl
}

func parseGoPackage(t *testing.T, directory string) parsedPackage {
	t.Helper()
	set := token.NewFileSet()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", directory, err)
	}
	result := parsedPackage{files: map[string]*ast.File{}, functions: map[string]*ast.FuncDecl{}}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		file, err := parser.ParseFile(set, path, nil, 0)
		if err != nil {
			t.Fatalf("ParseFile(%s): %v", path, err)
		}
		result.files[entry.Name()] = file
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if ok {
				result.functions[function.Name.Name] = function
			}
		}
	}
	return result
}

func requireFunction(t *testing.T, pkg parsedPackage, name string) *ast.FuncDecl {
	t.Helper()
	function := pkg.functions[name]
	if function == nil {
		t.Fatalf("production package is missing %s", name)
	}
	return function
}

func callPositions(function *ast.FuncDecl) map[string][]token.Pos {
	positions := map[string][]token.Pos{}
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if name := callName(call.Fun); name != "" {
			positions[name] = append(positions[name], call.Pos())
		}
		return true
	})
	return positions
}

func requireCallOrder(t *testing.T, owner string, positions map[string][]token.Pos, names ...string) {
	t.Helper()
	var previous token.Pos
	for _, name := range names {
		found := positions[name]
		if len(found) == 0 {
			t.Errorf("%s does not call canonical boundary %s", owner, name)
			return
		}
		if previous != token.NoPos && found[0] <= previous {
			t.Errorf("%s calls %s out of canonical validation/source order", owner, name)
		}
		previous = found[0]
	}
}

func assertSelectorOwners(t *testing.T, pkg parsedPackage, owners map[string]map[string]bool) {
	t.Helper()
	for name, function := range pkg.functions {
		ast.Inspect(function.Body, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			identifier, ok := selector.X.(*ast.Ident)
			if !ok {
				return true
			}
			qualified := identifier.Name + "." + selector.Sel.Name
			allowed, audited := owners[qualified]
			if audited && !allowed[name] {
				t.Errorf("%s calls owned boundary %s outside its adapter", name, qualified)
			}
			return true
		})
	}
}

func callName(expression ast.Expr) string {
	switch expression := expression.(type) {
	case *ast.Ident:
		return expression.Name
	case *ast.SelectorExpr:
		if identifier, ok := expression.X.(*ast.Ident); ok {
			return identifier.Name + "." + expression.Sel.Name
		}
	}
	return ""
}

func TestMCPContractsHaveCanonicalSchemaAndRuntimeOwners(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	contractRoot := filepath.Join(root, "internal", "mcpserver", "contracts")
	entries, err := os.ReadDir(contractRoot)
	if err != nil {
		t.Fatal(err)
	}
	schemas := map[string]any{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".schema.json") {
			continue
		}
		var schema any
		if err := json.Unmarshal([]byte(readText(t, filepath.Join(contractRoot, entry.Name()))), &schema); err != nil {
			t.Errorf("%s is not JSON: %v", entry.Name(), err)
			continue
		}
		schemas[entry.Name()] = schema
	}
	if len(schemas) < 10 {
		t.Fatalf("canonical MCP schema inventory unexpectedly small: %d", len(schemas))
	}
	for owner, schema := range schemas {
		walkJSON(schema, func(key string, value any) {
			if key != "$ref" {
				return
			}
			reference, ok := value.(string)
			if !ok {
				t.Errorf("%s has non-string $ref", owner)
				return
			}
			filename := strings.Split(reference, "#")[0]
			if filename != "" {
				if _, ok := schemas[filename]; !ok {
					t.Errorf("%s references non-canonical or missing schema %q", owner, filename)
				}
			}
		})
	}
	contractsRuntime := readText(t, filepath.Join(root, "internal", "mcpserver", "contracts.go"))
	for _, token := range []string{"//go:embed contracts/*.schema.json", "compiledContracts", "contractFS"} {
		if !strings.Contains(contractsRuntime, token) {
			t.Errorf("runtime schema owner omits %q", token)
		}
	}

	var preview struct {
		Properties struct {
			ManualActions struct {
				Items struct {
					Properties struct {
						Code struct {
							Enum []string `json:"enum"`
						} `json:"code"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"manual_actions"`
		} `json:"properties"`
	}
	decodeJSONFile(t, filepath.Join(contractRoot, "preview_v02_migration.output.schema.json"), &preview)
	wantCodes := []string{
		"provide_citation_mapping", "disambiguate_citation_entry",
		"disambiguate_citation_destination", "normalize_citations_section",
		"reconcile_sources_and_citations", "repair_invalid_utf8",
		"provide_generated_at", "provide_generated_by",
	}
	if strings.Join(preview.Properties.ManualActions.Items.Properties.Code.Enum, ",") != strings.Join(wantCodes, ",") {
		t.Fatalf("migration manual-action schema drift: %v", preview.Properties.ManualActions.Items.Properties.Code.Enum)
	}
	runtimeOwner := readText(t, filepath.Join(root, "mutation", "migration.go"))
	english := joinedDocumentation(t, root, []string{"README.md", "docs/skill.md", "docs/toolkit.md", "docs/migration.md"})
	russian := joinedDocumentation(t, root, []string{"README.ru.md", "docs/ru/skill.md", "docs/ru/toolkit.md", "docs/ru/migration.md"})
	for _, code := range wantCodes {
		if !regexp.MustCompile(`Code:\s*"` + regexp.QuoteMeta(code) + `"`).MatchString(runtimeOwner) {
			t.Errorf("canonical mutation runtime omits %s", code)
		}
		if !strings.Contains(english, "`"+code+"`") || !strings.Contains(russian, "`"+code+"`") {
			t.Errorf("published EN/RU docs do not both link semantic code %s to its owner", code)
		}
	}

	migrationRuntime := readText(t, filepath.Join(root, "internal", "mcpserver", "migration.go"))
	for _, token := range []string{
		"ResolutionDigest string", "`json:\"resolution_digest\"`",
		"`json:\"declaration_present\"`", "`json:\"declaration_valid\"`",
		"`json:\"declaration_raw\"`", "`json:\"candidates\"`", "`json:\"blockers\"`",
	} {
		if !strings.Contains(migrationRuntime, token) {
			t.Errorf("MCP DTO owner omits %q", token)
		}
	}
	for _, path := range append(append([]string{}, englishMigrationPages...), russianMigrationPages...) {
		content := readText(t, filepath.Join(root, path))
		for _, token := range []string{"format_version: 2", "resolution_digest", "expected_source"} {
			if !strings.Contains(content, token) {
				t.Errorf("%s omits canonical migration ABI token %q", path, token)
			}
		}
	}
}

func walkJSON(value any, visit func(string, any)) {
	switch value := value.(type) {
	case map[string]any:
		for key, item := range value {
			visit(key, item)
			walkJSON(item, visit)
		}
	case []any:
		for _, item := range value {
			walkJSON(item, visit)
		}
	}
}

func TestPublishedDocumentationLinksResolve(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	docsRoot := filepath.Join(root, "docs")
	permalinks := map[string]bool{"/": true}
	files := []string{
		filepath.Join(root, "README.md"), filepath.Join(root, "README.ru.md"),
		filepath.Join(root, "examples", "README.md"), filepath.Join(root, "examples", "README.ru.md"),
		filepath.Join(root, "skills", "open-knowledge-format", "SKILL.md"),
	}
	err := filepath.WalkDir(docsRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && filepath.Ext(path) == ".md" {
			files = append(files, path)
			if match := regexp.MustCompile(`(?m)^permalink:\s*(\S+)\s*$`).FindStringSubmatch(readText(t, path)); len(match) == 2 {
				permalinks[match[1]] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	referenceRoot := filepath.Join(root, "skills", "open-knowledge-format", "references")
	referenceEntries, err := os.ReadDir(referenceRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range referenceEntries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") && entry.Name() != "spec-v02.md" {
			files = append(files, filepath.Join(referenceRoot, entry.Name()))
		}
	}
	linkPattern := regexp.MustCompile(`!?\[[^\]\n]*\]\(([^)\n]+)\)`)
	liquidPattern := regexp.MustCompile(`^\{\{\s*'([^']+)'\s*\|\s*relative_url\s*\}\}$`)
	const blobPrefix = "https://github.com/skosovsky/okf/blob/main/"
	for _, path := range files {
		relative := mustRelative(t, root, path)
		for _, match := range linkPattern.FindAllStringSubmatch(markdownWithoutCode(readText(t, path)), -1) {
			target := strings.Trim(strings.TrimSpace(match[1]), "<>")
			if liquid := liquidPattern.FindStringSubmatch(target); len(liquid) == 2 {
				if !strings.HasPrefix(relative, "docs/") || !permalinks[liquid[1]] {
					t.Errorf("%s has unknown Jekyll target %q", relative, target)
				}
				continue
			}
			if strings.Contains(target, "{{") || strings.Contains(target, "}}") || strings.HasPrefix(target, "/") {
				t.Errorf("%s has unsupported published target %q", relative, target)
				continue
			}
			if strings.HasPrefix(target, blobPrefix) {
				assertExists(t, root, relative, strings.Split(strings.TrimPrefix(target, blobPrefix), "#")[0])
				continue
			}
			if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") ||
				strings.HasPrefix(target, "mailto:") || strings.HasPrefix(target, "#") {
				continue
			}
			local := strings.Split(strings.Split(target, "#")[0], "?")[0]
			if local == "" {
				continue
			}
			resolved := filepath.Clean(filepath.Join(filepath.Dir(path), filepath.FromSlash(local)))
			allowed := root
			if strings.HasPrefix(relative, "docs/") {
				allowed = docsRoot
			}
			if escaped(mustRelative(t, allowed, resolved)) {
				t.Errorf("%s escapes its published root with %q", relative, target)
				continue
			}
			if _, err := os.Stat(resolved); err != nil {
				t.Errorf("%s target %q: %v", relative, target, err)
			}
		}
	}
}

func assertExists(t *testing.T, root, owner, relative string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(relative))); err != nil {
		t.Errorf("%s repository target %q: %v", owner, relative, err)
	}
}

func escaped(relative string) bool {
	return relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

type corpusInventory struct {
	SpecLock            string            `yaml:"spec_lock"`
	LegacyInventory     string            `yaml:"legacy_inventory"`
	ProvenanceInventory string            `yaml:"provenance_inventory"`
	ConformanceEvidence string            `yaml:"conformance_evidence"`
	Cases               map[string]string `yaml:"cases"`
}

type fixtureProvenance struct {
	Classification string `yaml:"classification"`
	Reason         string `yaml:"reason"`
}

type provenanceInventory struct {
	Policy                   string                       `yaml:"policy"`
	ClassificationVocabulary map[string]string            `yaml:"classification_vocabulary"`
	Paths                    map[string]fixtureProvenance `yaml:"paths"`
}

func TestCorpusAndProvenanceInventoriesOwnEveryFixture(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	fixtureRoot := filepath.Join(root, "fixtures", "v02")
	var corpus corpusInventory
	decodeYAMLFile(t, filepath.Join(fixtureRoot, "corpus.yaml"), &corpus)
	if corpus.SpecLock != "spec-lock.json" || corpus.LegacyInventory != "legacy-inventory.yaml" ||
		corpus.ProvenanceInventory != "provenance.yaml" || corpus.ConformanceEvidence != "conformance-evidence.yaml" {
		t.Fatalf("corpus owner links drifted: %#v", corpus)
	}
	var provenance provenanceInventory
	decodeYAMLFile(t, filepath.Join(fixtureRoot, corpus.ProvenanceInventory), &provenance)
	if !strings.Contains(provenance.Policy, "synthetic inert test data") {
		t.Error("provenance policy no longer marks repository-authored data inert")
	}
	wantVocabulary := map[string]string{
		"upstream-derived":   "content reproducible from a pinned upstream source and declared exact transforms",
		"synthetic":          "repository-authored fixture content",
		"synthetic-metadata": "repository-authored fixture manifest, expectation, or derivation metadata",
		"mixed":              "directory boundary containing files with more than one exact classification",
	}
	for name, definition := range wantVocabulary {
		if provenance.ClassificationVocabulary[name] != definition {
			t.Errorf("provenance vocabulary %s drifted", name)
		}
	}
	for path, entry := range provenance.Paths {
		if wantVocabulary[entry.Classification] == "" || strings.TrimSpace(entry.Reason) == "" {
			t.Errorf("invalid provenance entry %s: %#v", path, entry)
		}
		if _, err := os.Stat(filepath.Join(fixtureRoot, filepath.FromSlash(path))); err != nil {
			t.Errorf("provenance entry %s: %v", path, err)
		}
	}
	for key, target := range corpus.Cases {
		if _, _, ok := fixturePathClassification(target, provenance.Paths); !ok {
			t.Errorf("corpus case %s target %s has no provenance owner", key, target)
		}
	}
	err := filepath.WalkDir(fixtureRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !entry.Type().IsRegular() {
			return err
		}
		relative := mustRelative(t, fixtureRoot, path)
		owner, classification, ok := fixturePathClassification(relative, provenance.Paths)
		if !ok {
			t.Errorf("fixture %s has no provenance classification", relative)
			return nil
		}
		metadata := filepath.Dir(relative) == "." || filepath.Base(relative) == "manifest.yaml" ||
			filepath.Base(relative) == "expected.json" || filepath.Base(relative) == "derivation.json"
		if metadata && (owner != relative || classification.Classification != "synthetic-metadata") {
			t.Errorf("metadata %s resolves through %s as %s", relative, owner, classification.Classification)
		}
		if !metadata && (classification.Classification == "mixed" || classification.Classification == "synthetic-metadata") {
			t.Errorf("content %s resolves through non-content owner %s", relative, owner)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func fixturePathClassification(target string, paths map[string]fixtureProvenance) (string, fixtureProvenance, bool) {
	target = filepath.ToSlash(filepath.Clean(target))
	best := ""
	value := fixtureProvenance{}
	for path, candidate := range paths {
		path = filepath.ToSlash(filepath.Clean(path))
		if (target == path || strings.HasPrefix(target, path+"/")) && len(path) > len(best) {
			best, value = path, candidate
		}
	}
	return best, value, best != ""
}

func TestAdversarialEvidenceIsExecutableAndExplicitlyInert(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	fixtureRoot := filepath.Join(root, "fixtures", "v02")
	var corpus corpusInventory
	decodeYAMLFile(t, filepath.Join(fixtureRoot, "corpus.yaml"), &corpus)
	requiredCases := []string{
		"adversarial_untrusted_signals", "strict_footnote_ownership",
		"adversarial_inert_executor_policy_bypass",
		"lifecycle_structured_state_overrides_body_promotion", "lifecycle_body_claims_are_inert",
	}
	matrix := readText(t, filepath.Join(root, "skills", "open-knowledge-format", "references", "adversarial-v02.md"))
	for _, key := range requiredCases {
		if corpus.Cases[key] == "" || !strings.Contains(matrix, key) {
			t.Errorf("adversarial evidence key %s is not owned by both corpus and matrix", key)
		}
	}
	for _, token := range []string{
		"Deterministic harness", "Agent-only decision", "does not prove an agent refused execution",
		"source_footnote_definition_missing", "source_footnote_unknown", "source_id_duplicate",
	} {
		if !strings.Contains(matrix, token) {
			t.Errorf("adversarial evidence omits %q", token)
		}
	}

	inertRoot := filepath.Join(fixtureRoot, corpus.Cases["adversarial_inert_executor_policy_bypass"])
	loaded, err := bundle.LoadBundle(inertRoot)
	if err != nil {
		t.Fatalf("LoadBundle(inert executor): %v", err)
	}
	conceptID, err := bundle.ParseConceptID("computations/inert-policy-probe")
	if err != nil {
		t.Fatal(err)
	}
	concept, ok := loaded.Get(conceptID)
	if !ok {
		t.Fatal("inert executor concept is absent")
	}
	document := concept.Document
	state := document.AttestedComputationState()
	if !state.Valid || state.Mode != bundle.ComputationModeInline || document.TrustTier() != bundle.TrustUnverified {
		t.Errorf("inert executor projection = %#v trust=%q", state, document.TrustTier())
	}
	assetPath := filepath.Join(inertRoot, "references", "executors", "policy-bypass.txt")
	asset := []byte(readText(t, assetPath))
	digest := sha256.Sum256(asset)
	if hex.EncodeToString(digest[:]) != "1d0d736e5cbf71ade9f1fd54975cbcec0f27eba1b95882e35ebcf5dc0d9da553" {
		t.Error("inert adversarial asset bytes drifted from executable evidence")
	}
	if !strings.Contains(string(asset), "DO NOT EXECUTE") ||
		state.Contract.Executor == nil || len(state.Contract.Executor.Receipt) != 0 {
		t.Error("adversarial executor lost inert marker or invented a receipt")
	}

	strictRoot := filepath.Join(fixtureRoot, corpus.Cases["strict_footnote_ownership"])
	report := validator.ValidatePath(strictRoot, &validator.ValidatorConfig{Strict: true})
	var expected expectedValidationReport
	decodeJSONFile(t, filepath.Join(strictRoot, "expected.json"), &expected)
	assertValidationReportMatchesExpected(t, report, expected)

	structured := parseFixtureDocument(t, filepath.Join(fixtureRoot, corpus.Cases["lifecycle_structured_state_overrides_body_promotion"]))
	bodyOnly := parseFixtureDocument(t, filepath.Join(fixtureRoot, corpus.Cases["lifecycle_body_claims_are_inert"]))
	referenceDate := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	if structured.EffectiveStatus() != bundle.StatusDeprecated || !structured.IsStale(referenceDate) {
		t.Error("structured lifecycle evidence no longer overrides inert body claims")
	}
	if bodyOnly.EffectiveStatus() != bundle.StatusStable || bodyOnly.IsStale(referenceDate) {
		t.Error("body-only lifecycle prose became executable state")
	}
}

type expectedValidationDiagnostic struct {
	Code      string `json:"code"`
	SpecRef   string `json:"spec_ref"`
	File      string `json:"file"`
	FieldPath string `json:"field_path"`
	Severity  string `json:"severity"`
	Message   string `json:"message"`
}

type expectedValidationReport struct {
	ExitCode     int                                  `json:"exit_code"`
	ScannedFiles int                                  `json:"scanned_files"`
	Diagnostics  []expectedValidationDiagnostic       `json:"diagnostics"`
	Counts       struct{ Errors, Warnings, Info int } `json:"counts"`
}

func assertValidationReportMatchesExpected(t *testing.T, report validator.Report, expected expectedValidationReport) {
	t.Helper()
	if report.ExitCode() != expected.ExitCode || report.ScannedFiles != expected.ScannedFiles ||
		len(report.Diagnostics) != len(expected.Diagnostics) {
		t.Fatalf("validation report shape = exit:%d scanned:%d diagnostics:%d, want exit:%d scanned:%d diagnostics:%d",
			report.ExitCode(), report.ScannedFiles, len(report.Diagnostics),
			expected.ExitCode, expected.ScannedFiles, len(expected.Diagnostics))
	}
	for index, got := range report.Diagnostics {
		want := expected.Diagnostics[index]
		if got.Code != want.Code || got.SpecRef != want.SpecRef || got.File != want.File ||
			got.FieldPath != want.FieldPath || got.Severity.String() != want.Severity || got.Message != want.Message {
			t.Errorf("diagnostic[%d] = %#v, want %#v", index, got, want)
		}
	}
	if report.ErrorCount() != expected.Counts.Errors || report.WarningCount() != expected.Counts.Warnings ||
		report.InfoCount() != expected.Counts.Info {
		t.Errorf("diagnostic counts = %d/%d/%d, want %d/%d/%d",
			report.ErrorCount(), report.WarningCount(), report.InfoCount(),
			expected.Counts.Errors, expected.Counts.Warnings, expected.Counts.Info)
	}
}

type pluginManifest struct {
	Name, Version, Description, Skills, Source string
	Author                                     struct {
		Name string `json:"name"`
	} `json:"author"`
	Keywords  []string `json:"keywords"`
	Interface struct {
		Category string `json:"category"`
	} `json:"interface"`
}

func TestPluginManifestsPublishOneConsistentSkill(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	var claude, codex pluginManifest
	decodeJSONFile(t, filepath.Join(root, ".claude-plugin", "plugin.json"), &claude)
	decodeJSONFile(t, filepath.Join(root, ".codex-plugin", "plugin.json"), &codex)
	var marketplace struct {
		Plugins []pluginManifest `json:"plugins"`
	}
	decodeJSONFile(t, filepath.Join(root, ".claude-plugin", "marketplace.json"), &marketplace)
	if len(marketplace.Plugins) != 1 {
		t.Fatalf("marketplace plugin count = %d", len(marketplace.Plugins))
	}
	manifests := []pluginManifest{claude, codex, marketplace.Plugins[0]}
	for index, manifest := range manifests {
		if manifest.Name != "okf" || manifest.Version != "0.2.0" || manifest.Author.Name != "Sergey Kosovsky" ||
			!strings.Contains(manifest.Description, "v0.2") {
			t.Errorf("plugin manifest[%d] identity drifted: %#v", index, manifest)
		}
		if manifest.Description != claude.Description || strings.Join(manifest.Keywords, "\x00") != strings.Join(claude.Keywords, "\x00") {
			t.Errorf("plugin manifest[%d] description/keywords differ", index)
		}
	}
	if strings.TrimSuffix(claude.Skills, "/") != "./skills" || strings.TrimSuffix(codex.Skills, "/") != "./skills" ||
		marketplace.Plugins[0].Source != "./" {
		t.Error("plugin skill/source ownership drifted")
	}
	var skills struct {
		Schema    string `json:"$schema"`
		Groupings []struct {
			Skills []string `json:"skills"`
		} `json:"groupings"`
	}
	decodeJSONFile(t, filepath.Join(root, "skills.sh.json"), &skills)
	count := 0
	for _, grouping := range skills.Groupings {
		for _, skill := range grouping.Skills {
			if skill == "open-knowledge-format" {
				count++
			}
		}
	}
	if skills.Schema != "https://skills.sh/schemas/skills.sh.schema.json" || count != 1 {
		t.Errorf("skills.sh registration = schema %q count %d", skills.Schema, count)
	}
}

type legacyInventory struct {
	CanonicalCompatibilityCases []string          `yaml:"canonical_compatibility_cases"`
	AdversarialLegacyCases      []string          `yaml:"adversarial_legacy_cases"`
	PinnedSpecificationHistory  []string          `yaml:"pinned_specification_history"`
	MigrationGuidancePaths      []string          `yaml:"migration_guidance_paths"`
	ContractMetadataPaths       []string          `yaml:"contract_metadata_paths"`
	PreV02RegressionFiles       []string          `yaml:"pre_v02_regression_files"`
	IntentionalFileReasons      map[string]string `yaml:"intentional_file_reasons"`
	OccurrenceLock              struct {
		Algorithm string `yaml:"algorithm"`
		SHA256    string `yaml:"sha256"`
		Reason    string `yaml:"reason"`
		Count     int    `yaml:"count"`
	} `yaml:"occurrence_lock"`
}

func TestLegacyRepresentationsMatchExactInventory(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	var inventory legacyInventory
	decodeYAMLFile(t, filepath.Join(root, "fixtures", "v02", "legacy-inventory.yaml"), &inventory)
	allowed := map[string]bool{}
	add := func(prefix string, paths []string) {
		for _, path := range paths {
			allowed[filepath.ToSlash(filepath.Clean(filepath.Join(prefix, path)))] = true
		}
	}
	add("", inventory.PinnedSpecificationHistory)
	add("", inventory.MigrationGuidancePaths)
	add("", inventory.PreV02RegressionFiles)
	add("", inventory.ContractMetadataPaths)
	add("fixtures/v02", inventory.CanonicalCompatibilityCases)
	add("fixtures/v02", inventory.AdversarialLegacyCases)
	reasoned := append(append([]string{}, inventory.PreV02RegressionFiles...), inventory.ContractMetadataPaths...)
	reasoned = append(reasoned, inventory.CanonicalCompatibilityCases...)
	reasoned = append(reasoned, inventory.AdversarialLegacyCases...)
	for _, path := range reasoned {
		path = filepath.ToSlash(filepath.Clean(path))
		reasonKey := strings.TrimPrefix(path, "fixtures/v02/")
		if strings.TrimSpace(inventory.IntentionalFileReasons[reasonKey]) == "" &&
			strings.TrimSpace(inventory.IntentionalFileReasons[path]) == "" {
			t.Errorf("legacy inventory path %s has no exact reason", path)
		}
	}
	pattern := regexp.MustCompile(`(?i)\btimestamp\b|#\s+Citations\b|\bv0\.1\b|\bspec-v01(?:\.md)?\b|` +
		`\bcurrent[^\n]{0,40}\bv?0\.1[^\n]{0,20}\bdraft\b|okf_version:[^\n]{0,12}\b0\.1\b`)
	searchRoots := []string{
		"README.md", "README.ru.md", "docs", "examples", "fixtures",
		"skills/open-knowledge-format", ".claude-plugin", ".codex-plugin", "skills.sh.json", "skills-lock.json",
	}
	unexpected := []string{}
	for _, relative := range searchRoots {
		path := filepath.Join(root, filepath.FromSlash(relative))
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		inspect := func(candidate string) {
			relative := mustRelative(t, root, candidate)
			if pattern.MatchString(readText(t, candidate)) && !allowed[relative] {
				unexpected = append(unexpected, relative)
			}
		}
		if !info.IsDir() {
			inspect(path)
			continue
		}
		err = filepath.WalkDir(path, func(candidate string, entry os.DirEntry, err error) error {
			if err == nil && !entry.IsDir() && isContractBearingText(candidate) {
				inspect(candidate)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(unexpected) != 0 {
		sort.Strings(unexpected)
		t.Fatalf("legacy representations outside inventory: %v", unexpected)
	}
	if inventory.OccurrenceLock.Algorithm != "sha256" || inventory.OccurrenceLock.Reason == "" {
		t.Fatalf("legacy occurrence lock metadata drifted: %#v", inventory.OccurrenceLock)
	}
	paths := make([]string, 0, len(allowed))
	for path := range allowed {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	var occurrences strings.Builder
	count := 0
	for _, path := range paths {
		content := readText(t, filepath.Join(root, filepath.FromSlash(path)))
		for _, match := range pattern.FindAllStringIndex(content, -1) {
			count++
			fmt.Fprintf(&occurrences, "%s\x00%d\x00%s\n", path, match[0], content[match[0]:match[1]])
		}
	}
	digest := sha256.Sum256([]byte(occurrences.String()))
	if count != inventory.OccurrenceLock.Count || hex.EncodeToString(digest[:]) != inventory.OccurrenceLock.SHA256 {
		t.Fatalf("legacy occurrence lock = %d/%s, want %d/%s", count, hex.EncodeToString(digest[:]),
			inventory.OccurrenceLock.Count, inventory.OccurrenceLock.SHA256)
	}
}

func isContractBearingText(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".yaml", ".yml", ".json":
		return true
	default:
		return false
	}
}

func decodeJSONFile(t *testing.T, path string, target any) {
	t.Helper()
	if err := json.Unmarshal([]byte(readText(t, path)), target); err != nil {
		t.Fatalf("Unmarshal(%s): %v", path, err)
	}
}

func decodeYAMLFile(t *testing.T, path string, target any) {
	t.Helper()
	if err := yaml.Unmarshal([]byte(readText(t, path)), target); err != nil {
		t.Fatalf("Unmarshal(%s): %v", path, err)
	}
}

func parseFixtureDocument(t *testing.T, path string) bundle.Document {
	t.Helper()
	document, err := bundle.ParseDocument(readText(t, path))
	if err != nil {
		t.Fatalf("ParseDocument(%s): %v", path, err)
	}
	return document
}

func markdownWithoutCode(content string) string {
	var out strings.Builder
	inFence := false
	fence := ""
	inline := regexp.MustCompile("`+[^`\\n]*`+")
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			marker := trimmed[:3]
			if !inFence {
				inFence, fence = true, marker
			} else if marker == fence {
				inFence, fence = false, ""
			}
			continue
		}
		if !inFence {
			out.WriteString(inline.ReplaceAllString(line, ""))
			out.WriteByte('\n')
		}
	}
	return out.String()
}

func joinedDocumentation(t *testing.T, root string, paths []string) string {
	t.Helper()
	var result strings.Builder
	for _, path := range paths {
		result.WriteString(readText(t, filepath.Join(root, filepath.FromSlash(path))))
		result.WriteByte('\n')
	}
	return result.String()
}

func compactWhitespace(content string) string { return strings.Join(strings.Fields(content), " ") }

func mustRelative(t *testing.T, root, path string) string {
	t.Helper()
	relative, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatalf("Rel(%s, %s): %v", root, path, err)
	}
	return filepath.ToSlash(relative)
}

func readText(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	return string(content)
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), ".."))
}
