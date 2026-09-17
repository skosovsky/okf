package okf_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/graph"
	"github.com/skosovsky/okf/internal/okfcli"
	"github.com/skosovsky/okf/validator"
	"gopkg.in/yaml.v3"
)

const (
	pinnedSpecRevision = "3fcbb9f828c2f23d109c855ee403c3a4c81f3a96"
	pinnedSpecSHA256   = "5a3311d270bebb16d558010e75064f5b75323f284992641732b1c8097511f948"
)

type specLock struct {
	Repository        string `json:"repository"`
	Path              string `json:"path"`
	Revision          string `json:"revision"`
	RawURL            string `json:"raw_url"`
	SHA256            string `json:"sha256"`
	NormativeSections string `json:"normative_sections"`
	CanonicalExample  string `json:"canonical_example"`
}

func TestProductionSourceForbidsRemovedVersionAccessorsAndValidators(t *testing.T) {
	t.Parallel()

	forbidden := []string{"." + "OKFVersion(", "valid" + "OKFVersion"}
	err := filepath.WalkDir(".", func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, occurrence := range forbidden {
			if bytes.Contains(source, []byte(occurrence)) {
				t.Errorf("%s contains removed production occurrence %q", path, occurrence)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

type corpusManifest struct {
	SpecLock            string            `yaml:"spec_lock"`
	LegacyInventory     string            `yaml:"legacy_inventory"`
	ProvenanceInventory string            `yaml:"provenance_inventory"`
	ConformanceEvidence string            `yaml:"conformance_evidence"`
	Cases               map[string]string `yaml:"cases"`
}

type conformanceEvidence struct {
	Clauses map[string]struct {
		Tests []struct {
			Package string `yaml:"package"`
			Name    string `yaml:"name"`
		} `yaml:"tests"`
		CorpusKeys []string `yaml:"corpus_keys"`
	} `yaml:"clauses"`
}

type fixtureProvenanceInventory struct {
	Paths map[string]struct {
		Classification string `yaml:"classification"`
		Reason         string `yaml:"reason"`
	} `yaml:"paths"`
}

func fixtureProvenanceFor(
	target string,
	paths map[string]struct {
		Classification string `yaml:"classification"`
		Reason         string `yaml:"reason"`
	},
) (string, struct {
	Classification string `yaml:"classification"`
	Reason         string `yaml:"reason"`
}, bool) {
	target = filepath.ToSlash(filepath.Clean(target))
	bestPath := ""
	var best struct {
		Classification string `yaml:"classification"`
		Reason         string `yaml:"reason"`
	}
	for path, classification := range paths {
		classifiedPath := filepath.ToSlash(filepath.Clean(path))
		if target != classifiedPath && !strings.HasPrefix(target, classifiedPath+"/") {
			continue
		}
		if len(classifiedPath) > len(bestPath) {
			bestPath = classifiedPath
			best = classification
		}
	}
	return bestPath, best, bestPath != ""
}

type legacyInventory struct {
	CanonicalCompatibilityCases []string          `yaml:"canonical_compatibility_cases"`
	AdversarialLegacyCases      []string          `yaml:"adversarial_legacy_cases"`
	PinnedSpecificationHistory  []string          `yaml:"pinned_specification_history"`
	MigrationGuidancePaths      []string          `yaml:"migration_guidance_paths"`
	ContractMetadataPaths       []string          `yaml:"contract_metadata_paths"`
	PreV02RegressionFiles       []string          `yaml:"pre_v02_regression_files"`
	IntentionalFileReasons      map[string]string `yaml:"intentional_file_reasons"`
}

type appendixDerivation struct {
	Source struct {
		Path          string `json:"path"`
		Revision      string `json:"revision"`
		SHA256        string `json:"sha256"`
		AppendixLines string `json:"appendix_lines"`
	} `json:"source"`
	Transforms []struct {
		SourceLines       string `json:"source_lines"`
		SourceSliceSHA256 string `json:"source_slice_sha256"`
		Target            string `json:"target"`
		Reason            string `json:"reason"`
		Operations        []struct {
			Kind string `json:"kind"`
			Old  string `json:"old"`
			New  string `json:"new"`
		} `json:"operations"`
	} `json:"transforms"`
	SyntheticMaterializations []struct {
		Target string `json:"target"`
		Reason string `json:"reason"`
	} `json:"synthetic_materializations"`
	DerivedSHA256 map[string]string `json:"derived_sha256"`
}

type failingReadSource struct {
	paths   []string
	failing string
	err     error
}

func (s failingReadSource) Paths(context.Context) ([]string, error) {
	return append([]string(nil), s.paths...), nil
}

func (s failingReadSource) ReadFile(_ context.Context, path string) ([]byte, error) {
	if path == s.failing {
		return nil, s.err
	}
	if filepath.Ext(path) == ".md" {
		return []byte("---\ntype: Note\n---\n"), nil
	}
	return []byte("asset"), nil
}

func TestV02PinnedSpecificationLock(t *testing.T) {
	t.Parallel()

	// Arrange.
	lockBytes, err := os.ReadFile(filepath.Join("fixtures", "v02", "spec-lock.json"))
	if err != nil {
		t.Fatalf("ReadFile(spec-lock.json) error = %v", err)
	}
	var lock specLock
	if err := json.Unmarshal(lockBytes, &lock); err != nil {
		t.Fatalf("Unmarshal(spec-lock.json) error = %v", err)
	}
	specBytes, err := os.ReadFile(filepath.Join("skills", "open-knowledge-format", "references", "spec-v02.md"))
	if err != nil {
		t.Fatalf("ReadFile(spec-v02.md) error = %v", err)
	}

	// Act.
	sum := sha256.Sum256(specBytes)
	actualSHA := hex.EncodeToString(sum[:])

	// Assert.
	if lock.Revision != pinnedSpecRevision {
		t.Fatalf("locked revision = %q, want %q", lock.Revision, pinnedSpecRevision)
	}
	if lock.Repository != "https://github.com/GoogleCloudPlatform/knowledge-catalog" {
		t.Fatalf("locked repository = %q", lock.Repository)
	}
	if lock.Path != "okf/SPEC.md" {
		t.Fatalf("locked path = %q", lock.Path)
	}
	wantRawURL := "https://raw.githubusercontent.com/GoogleCloudPlatform/knowledge-catalog/" +
		pinnedSpecRevision + "/okf/SPEC.md"
	if lock.RawURL != wantRawURL {
		t.Fatalf("locked raw URL = %q, want %q", lock.RawURL, wantRawURL)
	}
	if lock.SHA256 != pinnedSpecSHA256 {
		t.Fatalf("locked SHA-256 = %q, want %q", lock.SHA256, pinnedSpecSHA256)
	}
	if lock.NormativeSections != "1-13" {
		t.Fatalf("locked normative sections = %q, want 1-13", lock.NormativeSections)
	}
	if lock.CanonicalExample != "Appendix A" {
		t.Fatalf("locked canonical example = %q, want Appendix A", lock.CanonicalExample)
	}
	if actualSHA != pinnedSpecSHA256 {
		t.Fatalf("spec-v02.md SHA-256 = %q, want %q", actualSHA, pinnedSpecSHA256)
	}
}

func TestV02CanonicalCorpusPathsAndBundles(t *testing.T) {
	t.Parallel()

	// Arrange.
	manifestBytes, err := os.ReadFile(filepath.Join("fixtures", "v02", "corpus.yaml"))
	if err != nil {
		t.Fatalf("ReadFile(corpus.yaml) error = %v", err)
	}
	var manifest corpusManifest
	if err := yaml.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("Unmarshal(corpus.yaml) error = %v", err)
	}
	if len(manifest.Cases) == 0 {
		t.Fatal("corpus has no cases")
	}
	for _, relative := range []string{
		manifest.SpecLock,
		manifest.LegacyInventory,
		manifest.ProvenanceInventory,
		manifest.ConformanceEvidence,
	} {
		if relative == "" {
			t.Fatal("corpus metadata path is empty")
		}
		if _, err := os.Stat(filepath.Join("fixtures", "v02", filepath.FromSlash(relative))); err != nil {
			t.Fatalf("Stat(%s) error = %v", relative, err)
		}
	}

	// Act / Assert.
	for name, relative := range manifest.Cases {
		name, relative := name, relative
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join("fixtures", "v02", filepath.FromSlash(relative))
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("Stat(%s) error = %v", relative, err)
			}
			if !info.IsDir() {
				content, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("ReadFile(%s) error = %v", relative, err)
				}
				if _, err := bundle.ParseDocument(string(content)); err != nil {
					t.Fatalf("ParseDocument(%s) error = %v", relative, err)
				}
				return
			}
			loaded, err := bundle.LoadBundle(path)
			if err != nil {
				t.Fatalf("LoadBundle(%s) error = %v", relative, err)
			}
			if loaded.Len() == 0 {
				t.Fatalf("LoadBundle(%s) has no concepts", relative)
			}
			if parseErrors := loaded.ParseErrors(); len(parseErrors) != 0 {
				t.Fatalf("LoadBundle(%s) parse errors = %#v", relative, parseErrors)
			}
		})
	}
}

func TestV02ConformanceEvidenceNamesExistingTestsAndCorpusKeys(t *testing.T) {
	t.Parallel()

	// Arrange.
	manifestBytes, err := os.ReadFile(filepath.Join("fixtures", "v02", "corpus.yaml"))
	if err != nil {
		t.Fatalf("ReadFile(corpus.yaml) error = %v", err)
	}
	var manifest corpusManifest
	if err := yaml.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("Unmarshal(corpus.yaml) error = %v", err)
	}
	evidenceBytes, err := os.ReadFile(filepath.Join("fixtures", "v02", filepath.FromSlash(manifest.ConformanceEvidence)))
	if err != nil {
		t.Fatalf("ReadFile(conformance evidence) error = %v", err)
	}
	var evidence conformanceEvidence
	if err := yaml.Unmarshal(evidenceBytes, &evidence); err != nil {
		t.Fatalf("Unmarshal(conformance evidence) error = %v", err)
	}
	requiredClauses := []string{
		"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12", "13",
		"Appendix A", "toolkit extension", "toolkit graph",
	}

	// Act / Assert.
	if len(evidence.Clauses) != len(requiredClauses) {
		t.Fatalf("conformance evidence clauses = %d, want exactly %d", len(evidence.Clauses), len(requiredClauses))
	}
	for _, clause := range requiredClauses {
		item, ok := evidence.Clauses[clause]
		if !ok {
			t.Fatalf("conformance evidence missing clause %q", clause)
		}
		if len(item.Tests) == 0 || len(item.CorpusKeys) == 0 {
			t.Fatalf("conformance evidence clause %q has no tests or corpus keys", clause)
		}
		for _, test := range item.Tests {
			assertGoTestFunctionExists(t, test.Package, test.Name)
		}
		for _, key := range item.CorpusKeys {
			if _, ok := manifest.Cases[key]; !ok {
				t.Fatalf("conformance evidence clause %q references unknown corpus key %q", clause, key)
			}
		}
	}
}

func TestV02DuplicateYAMLKeyPolicyReferencesSharedExecutableEvidence(t *testing.T) {
	t.Parallel()

	// Arrange.
	baseline, err := os.ReadFile(filepath.Join("docs", "contracts", "okf-v0.2.md"))
	if err != nil {
		t.Fatalf("ReadFile(okf-v0.2.md) error = %v", err)
	}
	matrix, err := os.ReadFile(filepath.Join("docs", "contracts", "okf-v0.2-conformance.md"))
	if err != nil {
		t.Fatalf("ReadFile(okf-v0.2-conformance.md) error = %v", err)
	}
	evidenceBytes, err := os.ReadFile(filepath.Join("fixtures", "v02", "conformance-evidence.yaml"))
	if err != nil {
		t.Fatalf("ReadFile(conformance-evidence.yaml) error = %v", err)
	}
	var evidence conformanceEvidence
	if err := yaml.Unmarshal(evidenceBytes, &evidence); err != nil {
		t.Fatalf("Unmarshal(conformance-evidence.yaml) error = %v", err)
	}
	requiredEvidence := map[string][]struct {
		pkg  string
		name string
	}{
		"4": {
			{pkg: "bundle", name: "TestParseFrontmatterPreservesUnrelatedDuplicateExtensionKeysLosslessly"},
			{pkg: "bundle", name: "TestSemanticValueStateFailsClosedOnDuplicateStandardKeys"},
			{pkg: "mutation", name: "TestRenameFragment_PreservesDuplicateUnrelatedKeys"},
			{pkg: "mutation", name: "TestV02Operations_DuplicateTouchedKeysFailClosedWithoutStage"},
		},
		"5": {
			{pkg: "validator", name: "TestStrictV02DuplicateNestedStandardKeysHaveExactPaths"},
			{pkg: "validator", name: "TestStrictV02DuplicateStandardFamiliesFailClosedWithoutLegacyFallback"},
		},
		"11": {
			{pkg: "validator", name: "TestDuplicateTypeAndReservedVersionRemainBaseDiagnostics"},
		},
		"12": {
			{pkg: "validator", name: "TestDuplicateTypeAndReservedVersionRemainBaseDiagnostics"},
		},
		"13": {
			{pkg: "bundle", name: "TestDocumentDuplicateSourcesSuppressLegacyCitationsFallback"},
		},
	}

	// Act / Assert.
	for path, content := range map[string][]byte{
		"docs/contracts/okf-v0.2.md":             baseline,
		"docs/contracts/okf-v0.2-conformance.md": matrix,
	} {
		for _, token := range []string{
			"duplicate",
			"present",
			"unresolved",
			"strict",
			"mutation",
		} {
			if !bytes.Contains(bytes.ToLower(content), []byte(token)) {
				t.Fatalf("%s is missing duplicate-key policy token %q", path, token)
			}
		}
	}
	for clause, required := range requiredEvidence {
		item, ok := evidence.Clauses[clause]
		if !ok {
			t.Fatalf("conformance evidence missing clause %q", clause)
		}
		for _, want := range required {
			found := false
			for _, test := range item.Tests {
				if test.Package == want.pkg && test.Name == want.name {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("clause %q is missing shared evidence %s.%s", clause, want.pkg, want.name)
			}
			assertGoTestFunctionExists(t, want.pkg, want.name)
		}
	}
}

func assertGoTestFunctionExists(t *testing.T, packagePath, testName string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.FromSlash(packagePath))
	if err != nil {
		t.Fatalf("ReadDir(%s) error = %v", packagePath, err)
	}
	needle := []byte("func " + testName + "(")
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(filepath.FromSlash(packagePath), entry.Name()))
		if err != nil {
			t.Fatalf("ReadFile(%s/%s) error = %v", packagePath, entry.Name(), err)
		}
		if bytes.Contains(content, needle) {
			return
		}
	}
	t.Fatalf("conformance evidence references missing %s.%s", packagePath, testName)
}

func TestV02LoadAbortsOnEveryPerFileIOFailure(t *testing.T) {
	t.Parallel()

	readFailure := errors.New("injected read failure")
	tests := []struct {
		name    string
		failing string
	}{
		{name: "concept", failing: "concept.md"},
		{name: "reserved index", failing: "index.md"},
		{name: "reserved log", failing: "log.md"},
		{name: "asset", failing: "references/data.bin"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			source := failingReadSource{
				paths:   []string{"concept.md", "index.md", "log.md", "references/data.bin"},
				failing: tt.failing,
				err:     readFailure,
			}

			// Act.
			loaded, err := bundle.Load(context.Background(), source)

			// Assert.
			if loaded != nil {
				t.Fatalf("Load() bundle = %#v, want nil after incomplete immutable capture", loaded)
			}
			if !errors.Is(err, readFailure) || !strings.Contains(err.Error(), tt.failing) {
				t.Fatalf("Load() error = %v, want wrapped read failure naming %q", err, tt.failing)
			}
		})
	}
}

func TestV02CorpusClassifiesSyntheticProvenance(t *testing.T) {
	t.Parallel()

	// Arrange.
	manifestBytes, err := os.ReadFile(filepath.Join("fixtures", "v02", "corpus.yaml"))
	if err != nil {
		t.Fatalf("ReadFile(corpus.yaml) error = %v", err)
	}
	var manifest corpusManifest
	if err := yaml.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("Unmarshal(corpus.yaml) error = %v", err)
	}
	inventoryBytes, err := os.ReadFile(filepath.Join("fixtures", "v02", filepath.FromSlash(manifest.ProvenanceInventory)))
	if err != nil {
		t.Fatalf("ReadFile(provenance inventory) error = %v", err)
	}
	var inventory fixtureProvenanceInventory
	if err := yaml.Unmarshal(inventoryBytes, &inventory); err != nil {
		t.Fatalf("Unmarshal(provenance inventory) error = %v", err)
	}

	// Act / Assert.
	for name, relative := range manifest.Cases {
		classification := ""
		reason := ""
		bestMatchLength := -1
		for classifiedPath, metadata := range inventory.Paths {
			if (relative == classifiedPath || strings.HasPrefix(relative, classifiedPath+"/")) &&
				len(classifiedPath) > bestMatchLength {
				classification = metadata.Classification
				reason = metadata.Reason
				bestMatchLength = len(classifiedPath)
			}
		}
		if classification == "" || strings.TrimSpace(reason) == "" {
			t.Fatalf("corpus case %s (%s) has no provenance classification/reason", name, relative)
		}
		if relative == "positive/appendix-a" {
			if classification != "mixed" {
				t.Fatalf("Appendix A directory classification = %q, want mixed", classification)
			}
			continue
		}
		if strings.HasPrefix(relative, "positive/appendix-a/") {
			if classification != "upstream-derived" {
				t.Fatalf("Appendix A source-derived case %s classification = %q, want upstream-derived", name, classification)
			}
			continue
		}
		if classification != "synthetic" {
			t.Fatalf("repo-authored case %s classification = %q, want synthetic", name, classification)
		}
	}
	durability := inventory.Paths["positive/durability"]
	for _, token := range []string{"finance", "actors", "sources", "executor", "inert attester"} {
		if !strings.Contains(durability.Reason, token) {
			t.Fatalf("durability synthetic reason %q is missing %q", durability.Reason, token)
		}
	}
	for relative, want := range map[string]string{
		"positive/appendix-a/index.md":                    "synthetic",
		"positive/appendix-a/references":                  "synthetic",
		"positive/appendix-a/derivation.json":             "synthetic-metadata",
		"positive/appendix-a/metrics/income-statement.md": "upstream-derived",
	} {
		if got := inventory.Paths[relative].Classification; got != want {
			t.Fatalf("provenance[%s] = %q, want %q", relative, got, want)
		}
	}
}

func TestV02ProvenanceClassifiesEveryRegularFixture(t *testing.T) {
	t.Parallel()

	// Arrange.
	fixtureRoot := filepath.Join("fixtures", "v02")
	data, err := os.ReadFile(filepath.Join(fixtureRoot, "provenance.yaml"))
	if err != nil {
		t.Fatalf("ReadFile(provenance.yaml) error = %v", err)
	}
	var inventory fixtureProvenanceInventory
	if err := yaml.Unmarshal(data, &inventory); err != nil {
		t.Fatalf("Unmarshal(provenance.yaml) error = %v", err)
	}
	rootMetadata := map[string]struct{}{
		"conformance-evidence.yaml": {},
		"corpus.yaml":               {},
		"legacy-inventory.yaml":     {},
		"provenance.yaml":           {},
		"spec-lock.json":            {},
	}

	// Act / Assert.
	err = filepath.WalkDir(fixtureRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		relative, err := filepath.Rel(fixtureRoot, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		classifiedPath, classification, ok := fixtureProvenanceFor(relative, inventory.Paths)
		if !ok {
			t.Errorf("regular fixture %q has no provenance classification", relative)
			return nil
		}
		if classification.Classification == "mixed" {
			t.Errorf("regular fixture %q resolves to aggregate mixed classification via %q", relative, classifiedPath)
		}
		_, rootIsMetadata := rootMetadata[relative]
		isMetadata := rootIsMetadata ||
			filepath.Base(relative) == "manifest.yaml" ||
			filepath.Base(relative) == "expected.json" ||
			filepath.Base(relative) == "derivation.json"
		if isMetadata {
			if classifiedPath != relative || classification.Classification != "synthetic-metadata" {
				t.Errorf(
					"metadata fixture %q resolves via %q as %q, want exact synthetic-metadata",
					relative,
					classifiedPath,
					classification.Classification,
				)
			}
		} else if classification.Classification == "synthetic-metadata" {
			t.Errorf("content fixture %q resolves to metadata classification via %q", relative, classifiedPath)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir(fixtures/v02) error = %v", err)
	}
}

func TestV02MinimalAndUnknownExtensionEvidenceAreDisjoint(t *testing.T) {
	t.Parallel()

	// Arrange.
	readDocument := func(relative string) bundle.Document {
		t.Helper()
		data, err := os.ReadFile(filepath.Join("fixtures", "v02", filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", relative, err)
		}
		document, err := bundle.ParseDocument(string(data))
		if err != nil {
			t.Fatalf("ParseDocument(%s) error = %v", relative, err)
		}
		return document
	}
	minimal := readDocument("positive/minimal/minimal.md")
	extended := readDocument("adversarial/unknown-extension/unknown-extension.md")

	// Act.
	minimalKeys := minimal.Frontmatter.Keys()
	extension, extensionPresent := extended.Frontmatter.Get("x-producer")
	serialized, serializeErr := extended.Serialize()

	// Assert.
	if !slices.Equal(minimalKeys, []string{"type"}) {
		t.Fatalf("minimal frontmatter keys = %v, want exactly [type]", minimalKeys)
	}
	if !extensionPresent || extension.Kind != yaml.MappingNode {
		t.Fatalf("unknown extension = %#v, %v; want retained mapping", extension, extensionPresent)
	}
	if serializeErr != nil || !strings.Contains(serialized, "x-producer:\n  keep: [all, unknown, data]") {
		t.Fatalf("unknown extension round-trip = %q, %v", serialized, serializeErr)
	}
}

func TestV02LifecycleIgnoresBodySelfPromotionAndSelfDemotion(t *testing.T) {
	t.Parallel()

	// Arrange.
	readDocument := func(relative string) bundle.Document {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(
			"fixtures",
			"v02",
			"adversarial",
			"lifecycle-self-promotion",
			relative,
		))
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", relative, err)
		}
		document, err := bundle.ParseDocument(string(data))
		if err != nil {
			t.Fatalf("ParseDocument(%s) error = %v", relative, err)
		}
		return document
	}
	structured := readDocument("structured-deprecated-stale.md")
	bodyOnly := readDocument("body-only-claims.md")
	referenceDate := time.Date(2026, 6, 15, 23, 59, 59, 0, time.FixedZone("fixture", 7*60*60))

	// Act.
	structuredStatus := structured.StatusState()
	bodyOnlyStatus := bodyOnly.StatusState()
	structuredStale := structured.IsStale(referenceDate)
	bodyOnlyStale := bodyOnly.IsStale(referenceDate)

	// Assert.
	if !structuredStatus.Valid || structuredStatus.Effective != bundle.StatusDeprecated || !structuredStale {
		t.Fatalf(
			"structured lifecycle = status %#v stale=%v, want deprecated and stale",
			structuredStatus,
			structuredStale,
		)
	}
	if !bodyOnlyStatus.Valid || bodyOnlyStatus.Present ||
		bodyOnlyStatus.Effective != bundle.StatusStable || bodyOnlyStale {
		t.Fatalf(
			"body-only lifecycle = status %#v stale=%v, want absent/default stable and not stale",
			bodyOnlyStatus,
			bodyOnlyStale,
		)
	}
	if bodyOnly.StaleAfterValue().State != bundle.TemporalAbsent {
		t.Fatalf("body-only stale_after = %#v, want absent", bodyOnly.StaleAfterValue())
	}
}

func TestV02StalenessRequiresExplicitReferenceDateAcrossContractSurfaces(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	concept := []byte("---\ntype: Note\nstale_after: 2000-01-01\n---\nBody.\n")
	if err := os.WriteFile(filepath.Join(root, "concept.md"), concept, 0o600); err != nil {
		t.Fatalf("WriteFile(concept.md) error = %v", err)
	}
	loaded, err := bundle.LoadBundle(root)
	if err != nil {
		t.Fatalf("LoadBundle() error = %v", err)
	}
	referenceDate := time.Date(2000, 1, 1, 23, 59, 59, 0, time.FixedZone("fixture", 7*60*60))
	contractText, err := os.ReadFile(filepath.Join("docs", "contracts", "okf-v0.2.md"))
	if err != nil {
		t.Fatalf("ReadFile(okf-v0.2.md) error = %v", err)
	}
	conformanceText, err := os.ReadFile(filepath.Join("docs", "contracts", "okf-v0.2-conformance.md"))
	if err != nil {
		t.Fatalf("ReadFile(okf-v0.2-conformance.md) error = %v", err)
	}
	graphContractText, err := os.ReadFile(filepath.Join("graph", "contracts", "v0.2.md"))
	if err != nil {
		t.Fatalf("ReadFile(graph contract) error = %v", err)
	}

	// Act.
	implicitReport := validator.ValidateBundle(loaded, &validator.ValidatorConfig{Strict: true})
	explicitReport := validator.ValidateBundle(loaded, &validator.ValidatorConfig{
		Strict:        true,
		ReferenceDate: referenceDate,
	})
	var implicitGraph bytes.Buffer
	implicitGraphErr := graph.RenderJSONLDWithOptions(
		&implicitGraph,
		loaded,
		graph.Options{Profile: graph.ProjectionProfileToolkitV02},
	)
	var explicitGraph bytes.Buffer
	explicitGraphErr := graph.RenderJSONLDWithOptions(
		&explicitGraph,
		loaded,
		graph.Options{Profile: graph.ProjectionProfileToolkitV02, AsOf: &referenceDate},
	)
	var cliStdout, cliStderr bytes.Buffer
	cliCode := okfcli.Run([]string{"info", root, "--json"}, &cliStdout, &cliStderr)
	var cliInfo struct {
		AsOf  *string `json:"as_of"`
		Stale int     `json:"stale"`
	}
	cliDecodeErr := json.Unmarshal(cliStdout.Bytes(), &cliInfo)

	// Assert.
	for _, diagnostic := range implicitReport.Diagnostics {
		if diagnostic.Code == "stale" {
			t.Fatalf("implicit validation consulted a reference date: %#v", diagnostic)
		}
	}
	foundExplicitStale := false
	for _, diagnostic := range explicitReport.Diagnostics {
		if diagnostic.Code == "stale" {
			foundExplicitStale = true
		}
	}
	if !foundExplicitStale {
		t.Fatalf("explicit validation diagnostics = %#v, want stale warning", explicitReport.Diagnostics)
	}
	if implicitGraphErr != nil || explicitGraphErr != nil {
		t.Fatalf("graph errors = implicit %v, explicit %v", implicitGraphErr, explicitGraphErr)
	}
	for _, predicate := range []string{`"stalenessAsOf":`, `"stale":`} {
		if bytes.Contains(implicitGraph.Bytes(), []byte(predicate)) {
			t.Fatalf("implicit graph contains evaluated predicate %s: %s", predicate, implicitGraph.Bytes())
		}
		if !bytes.Contains(explicitGraph.Bytes(), []byte(predicate)) {
			t.Fatalf("explicit graph is missing evaluated predicate %s: %s", predicate, explicitGraph.Bytes())
		}
	}
	if cliCode != 0 || cliStderr.Len() != 0 || cliDecodeErr != nil ||
		cliInfo.AsOf != nil || cliInfo.Stale != 0 {
		t.Fatalf(
			"CLI implicit date contract = code %d stderr %q decode %v as_of %#v stale %d",
			cliCode,
			cliStderr.String(),
			cliDecodeErr,
			cliInfo.AsOf,
			cliInfo.Stale,
		)
	}
	for path, content := range map[string][]byte{
		"docs/contracts/okf-v0.2.md":             contractText,
		"docs/contracts/okf-v0.2-conformance.md": conformanceText,
		"graph/contracts/v0.2.md":                graphContractText,
	} {
		if !bytes.Contains(content, []byte("wall clock")) {
			t.Fatalf("%s does not freeze the no-wall-clock policy", path)
		}
	}
}

func TestV02LegacyInventoryPathsExist(t *testing.T) {
	t.Parallel()

	// Arrange.
	data, err := os.ReadFile(filepath.Join("fixtures", "v02", "legacy-inventory.yaml"))
	if err != nil {
		t.Fatalf("ReadFile(legacy-inventory.yaml) error = %v", err)
	}
	var inventory legacyInventory
	if err := yaml.Unmarshal(data, &inventory); err != nil {
		t.Fatalf("Unmarshal(legacy-inventory.yaml) error = %v", err)
	}
	paths := append([]string(nil), inventory.PinnedSpecificationHistory...)
	paths = append(paths, inventory.MigrationGuidancePaths...)
	paths = append(paths, inventory.ContractMetadataPaths...)
	paths = append(paths, inventory.PreV02RegressionFiles...)
	for _, relative := range inventory.CanonicalCompatibilityCases {
		paths = append(paths, filepath.Join("fixtures", "v02", filepath.FromSlash(relative)))
	}
	for _, relative := range inventory.AdversarialLegacyCases {
		paths = append(paths, filepath.Join("fixtures", "v02", filepath.FromSlash(relative)))
	}
	intentional := append([]string(nil), inventory.CanonicalCompatibilityCases...)
	intentional = append(intentional, inventory.AdversarialLegacyCases...)
	intentional = append(intentional, inventory.ContractMetadataPaths...)
	intentional = append(intentional, inventory.PreV02RegressionFiles...)

	// Act / Assert.
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("Stat(%s) error = %v", path, err)
		}
	}
	for _, path := range intentional {
		reasonPath := filepath.ToSlash(path)
		if !strings.HasPrefix(reasonPath, "fixtures/") {
			reasonPath = filepath.ToSlash(filepath.Join("fixtures", "v02", path))
		}
		reason := inventory.IntentionalFileReasons[path]
		if reason == "" {
			reason = inventory.IntentionalFileReasons[reasonPath]
		}
		if strings.TrimSpace(reason) == "" {
			t.Fatalf("intentional legacy file %s has no reason", path)
		}
	}
}

func TestV02AppendixIsSharedAcrossReadValidateAndGraph(t *testing.T) {
	t.Parallel()

	// Arrange.
	path := filepath.Join("fixtures", "v02", "positive", "appendix-a")
	loaded, err := bundle.LoadBundle(path)
	if err != nil {
		t.Fatalf("LoadBundle(appendix-a) error = %v", err)
	}
	revenueID, err := bundle.ParseConceptID("computations/revenue")
	if err != nil {
		t.Fatalf("ParseConceptID(revenue) error = %v", err)
	}
	revenue, ok := loaded.Get(revenueID)
	if !ok {
		t.Fatal("Appendix A revenue concept not loaded")
	}
	profitID, err := bundle.ParseConceptID("computations/profit")
	if err != nil {
		t.Fatalf("ParseConceptID(profit) error = %v", err)
	}
	profit, ok := loaded.Get(profitID)
	if !ok {
		t.Fatal("Appendix A profit concept not loaded")
	}

	// Act.
	contract, contractOK := revenue.Document.AttestedComputation()
	sources := revenue.Document.Sources()
	attributions := revenue.Document.Attributions()
	status := revenue.Document.StatusState()
	report := validator.ValidateBundle(loaded, &validator.ValidatorConfig{Strict: true})
	var projection bytes.Buffer
	renderErr := graph.RenderJSONLDWithOptions(
		&projection,
		loaded,
		graph.Options{Profile: graph.ProjectionProfileToolkitV02},
	)

	// Assert.
	if !contractOK || contract.Runtime != "bigquery" {
		t.Fatalf("AttestedComputation() = %#v, %v; want bigquery contract", contract, contractOK)
	}
	if len(contract.Parameters) != 1 || contract.Parameters[0].Name != "year" || !contract.Parameters[0].Required {
		t.Fatalf("AttestedComputation().Parameters = %#v, want required year", contract.Parameters)
	}
	if contract.Executor == nil || !slices.Equal(contract.Executor.Receipt, []string{"job_id", "executed_sql", "result"}) {
		t.Fatalf("AttestedComputation().Executor = %#v, want canonical receipt", contract.Executor)
	}
	if revenue.Document.TrustTier() != bundle.TrustHumanReviewed {
		t.Fatalf("TrustTier() = %q, want %q", revenue.Document.TrustTier(), bundle.TrustHumanReviewed)
	}
	if !status.Valid || status.Effective != bundle.StatusStable {
		t.Fatalf("StatusState() = %#v, want valid stable", status)
	}
	beforeRevenueBoundary := time.Date(2026, 12, 30, 0, 0, 0, 0, time.UTC)
	profitBoundary := time.Date(2026, 6, 15, 23, 59, 59, 0, time.FixedZone("fixture", 7*60*60))
	if revenue.Document.IsStale(beforeRevenueBoundary) {
		t.Fatal("revenue is stale before its explicit boundary")
	}
	if !profit.Document.IsStale(profitBoundary) {
		t.Fatal("profit is not stale on its exact explicit boundary")
	}
	if len(sources) != 2 || len(attributions) != 2 {
		t.Fatalf("Sources/Attributions = %d/%d, want 2/2", len(sources), len(attributions))
	}
	for _, attribution := range attributions {
		if len(attribution.Sources) != 1 || len(attribution.References) == 0 {
			t.Fatalf("Attribution(%q) = %#v, want one joined source and prose reference", attribution.ID, attribution)
		}
	}
	dashboard, ok := loaded.ResolvePathValueFor(revenue.Path, sources[1].Resource, bundle.PathFieldSourceResource)
	if !ok || dashboard.Kind != bundle.PathValueAmbiguous {
		t.Fatalf("dashboard source resolution = %#v, %v; want ambiguous scope/path", dashboard, ok)
	}
	if !report.IsConformant() || report.WarningCount() != 0 {
		t.Fatalf("strict Appendix A diagnostics = %#v, want clean", report.Diagnostics)
	}
	if renderErr != nil {
		t.Fatalf("RenderJSONLDWithOptions() error = %v", renderErr)
	}
	var document map[string]any
	if err := json.Unmarshal(projection.Bytes(), &document); err != nil {
		t.Fatalf("JSON-LD is invalid JSON: %v", err)
	}
	if !strings.Contains(projection.String(), "proj:AttestedComputationContract") {
		t.Fatal("JSON-LD does not contain the canonical Attested Computation contract")
	}
}

func TestV02CanonicalSingleFileSemantics(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := filepath.Join("fixtures", "v02", "positive", "canonical")
	readDocument := func(relative string) bundle.Document {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", relative, err)
		}
		document, err := bundle.ParseDocument(string(data))
		if err != nil {
			t.Fatalf("ParseDocument(%s) error = %v", relative, err)
		}
		return document
	}
	bare := readDocument("verified-bare.md")
	list := readDocument("verified-list.md")
	fileBacked := readDocument("computations/file.md")
	inline := readDocument("computations/inline.md")
	humanAuthored := readDocument("human-authored.md")
	draft := readDocument("draft.md")
	stable := readDocument("stable.md")
	deprecated := readDocument("deprecated.md")
	multipleSources := readDocument("multiple-sources.md")
	loaded, err := bundle.LoadBundle(root)
	if err != nil {
		t.Fatalf("LoadBundle(canonical) error = %v", err)
	}

	// Act.
	bareEvents := bare.Verifications()
	listEvents := list.Verifications()
	fileContract, fileOK := fileBacked.AttestedComputation()
	inlineContract, inlineOK := inline.AttestedComputation()

	// Assert.
	if !slices.Equal(bareEvents, listEvents) || bare.TrustTier() != list.TrustTier() || bare.TrustTier() != bundle.TrustHumanReviewed {
		t.Fatalf("bare/list verification semantics differ: %#v/%#v, %q/%q", bareEvents, listEvents, bare.TrustTier(), list.TrustTier())
	}
	if !fileOK || fileContract.Computation != "../references/account-balance.sql" {
		t.Fatalf("file-backed contract = %#v, %v", fileContract, fileOK)
	}
	if fileContract.Executor == nil || fileContract.Attester == nil {
		t.Fatalf("file-backed executor/attester = %#v/%#v", fileContract.Executor, fileContract.Attester)
	}
	if !inlineOK || inlineContract.Computation != "" || !strings.Contains(inline.Body, "```sql") {
		t.Fatalf("inline contract/body = %#v/%q", inlineContract, inline.Body)
	}
	if humanAuthored.TrustTier() != bundle.TrustUnverified {
		t.Fatalf("human generated concept TrustTier() = %q, want unverified", humanAuthored.TrustTier())
	}
	for name, state := range map[string]bundle.StatusState{
		"draft":      draft.StatusState(),
		"stable":     stable.StatusState(),
		"deprecated": deprecated.StatusState(),
	} {
		want := name
		if name == "stable" {
			want = bundle.StatusStable
		}
		if !state.Valid || state.Effective != want {
			t.Fatalf("%s StatusState() = %#v, want valid %q", name, state, want)
		}
	}
	if len(multipleSources.Sources()) != 2 || len(multipleSources.Attributions()) != 2 {
		t.Fatalf("multiple-source semantics = %d sources/%d attributions", len(multipleSources.Sources()), len(multipleSources.Attributions()))
	}
	metricID, err := bundle.ParseConceptID("metric")
	if err != nil {
		t.Fatalf("ParseConceptID(metric) error = %v", err)
	}
	if links := loaded.LinksFrom(metricID); len(links) != 2 || !links[0].Exists || !links[1].Exists {
		t.Fatalf("narrative computation links = %#v, want two resolved links", links)
	}
}

func TestV02AppendixDerivationIsPinned(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := filepath.Join("fixtures", "v02", "positive", "appendix-a")
	data, err := os.ReadFile(filepath.Join(root, "derivation.json"))
	if err != nil {
		t.Fatalf("ReadFile(derivation.json) error = %v", err)
	}
	var derivation appendixDerivation
	if err := json.Unmarshal(data, &derivation); err != nil {
		t.Fatalf("Unmarshal(derivation.json) error = %v", err)
	}

	specBytes, err := os.ReadFile(derivation.Source.Path)
	if err != nil {
		t.Fatalf("ReadFile(derivation source) error = %v", err)
	}

	// Act / Assert.
	if derivation.Source.Revision != pinnedSpecRevision || derivation.Source.SHA256 != pinnedSpecSHA256 {
		t.Fatalf("derivation source = %#v, want pinned revision and digest", derivation.Source)
	}
	if derivation.Source.Path != "skills/open-knowledge-format/references/spec-v02.md" ||
		derivation.Source.AppendixLines != "831-1003" {
		t.Fatalf("derivation source identity = %#v", derivation.Source)
	}
	specSum := sha256.Sum256(specBytes)
	if got := hex.EncodeToString(specSum[:]); got != pinnedSpecSHA256 {
		t.Fatalf("derivation source SHA-256 = %q, want %q", got, pinnedSpecSHA256)
	}
	if len(derivation.DerivedSHA256) == 0 {
		t.Fatal("derivation has no derived file digests")
	}
	for relative, want := range derivation.DerivedSHA256 {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", relative, err)
		}
		sum := sha256.Sum256(content)
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Fatalf("%s SHA-256 = %q, want %q", relative, got, want)
		}
	}
	transformedTargets := make(map[string]struct{}, len(derivation.Transforms))
	for _, transform := range derivation.Transforms {
		if strings.TrimSpace(transform.Reason) == "" || len(transform.Operations) == 0 {
			t.Fatalf("transform for %q has no reason or operations", transform.Target)
		}
		sourceSlice := sourceLineRange(t, specBytes, transform.SourceLines)
		sliceSum := sha256.Sum256(sourceSlice)
		if got := hex.EncodeToString(sliceSum[:]); got != transform.SourceSliceSHA256 {
			t.Fatalf("%s source slice SHA-256 = %q, want %q", transform.Target, got, transform.SourceSliceSHA256)
		}
		reproduced := string(sourceSlice)
		for _, operation := range transform.Operations {
			if operation.Kind != "replace_exact" {
				t.Fatalf("%s operation kind = %q", transform.Target, operation.Kind)
			}
			if strings.Count(reproduced, operation.Old) != 1 {
				t.Fatalf("%s replacement old text occurs %d times, want exactly once", transform.Target, strings.Count(reproduced, operation.Old))
			}
			reproduced = strings.Replace(reproduced, operation.Old, operation.New, 1)
		}
		target, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(transform.Target)))
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", transform.Target, err)
		}
		if !bytes.Equal([]byte(reproduced), target) {
			t.Fatalf("%s cannot be reproduced from the pinned source slice and declared transforms", transform.Target)
		}
		transformedTargets[transform.Target] = struct{}{}
	}
	syntheticTargets := make(map[string]struct{}, len(derivation.SyntheticMaterializations))
	for _, materialization := range derivation.SyntheticMaterializations {
		if strings.TrimSpace(materialization.Reason) == "" {
			t.Fatalf("synthetic materialization %q has no reason", materialization.Target)
		}
		syntheticTargets[materialization.Target] = struct{}{}
	}
	for target := range derivation.DerivedSHA256 {
		_, transformed := transformedTargets[target]
		_, synthetic := syntheticTargets[target]
		if transformed == synthetic {
			t.Fatalf("derived target %q must be classified exactly once as transformed or synthetic", target)
		}
	}
}

func sourceLineRange(t *testing.T, source []byte, rawRange string) []byte {
	t.Helper()
	var start, end int
	if _, err := fmt.Sscanf(rawRange, "%d-%d", &start, &end); err != nil || start < 1 || end < start {
		t.Fatalf("invalid one-based source line range %q", rawRange)
	}
	lines := bytes.SplitAfter(source, []byte("\n"))
	if end > len(lines) {
		t.Fatalf("source line range %q exceeds %d lines", rawRange, len(lines))
	}
	return bytes.Join(lines[start-1:end], nil)
}

func TestV02DurabilityAttesterUsesOnlyDeclaredReceiptEvidence(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := filepath.Join("fixtures", "v02", "positive", "durability")
	loaded, err := bundle.LoadBundle(root)
	if err != nil {
		t.Fatalf("LoadBundle(durability) error = %v", err)
	}
	computationID, err := bundle.ParseConceptID("computations/revenue")
	if err != nil {
		t.Fatalf("ParseConceptID(revenue) error = %v", err)
	}
	concept, ok := loaded.Get(computationID)
	if !ok {
		t.Fatal("durability computation not loaded")
	}
	contract, ok := concept.Document.AttestedComputation()
	if !ok {
		t.Fatal("durability computation has no typed Attested Computation contract")
	}
	sqlBytes, err := os.ReadFile(filepath.Join(root, "references", "computations", "revenue.sql"))
	if err != nil {
		t.Fatalf("ReadFile(revenue.sql) error = %v", err)
	}
	attesterBytes, err := os.ReadFile(filepath.Join(root, "references", "attesters", "sql-equality.py"))
	if err != nil {
		t.Fatalf("ReadFile(sql-equality.py) error = %v", err)
	}

	// Act.
	sqlDigest := sha256.Sum256(sqlBytes)
	digestText := hex.EncodeToString(sqlDigest[:])
	wantAttester := fmt.Sprintf(`# Inert reference example: the sanctioned SQL digest is pinned to the captured
# computation bytes. This file documents the attestation rule; the OKF toolkit
# does not execute attester resources.
import hashlib

SANCTIONED_SQL_SHA256 = %q


def attest(receipt: dict) -> bool:
    executed_sql = receipt.get("executed_sql")
    if not isinstance(executed_sql, str):
        return False
    return hashlib.sha256(executed_sql.encode()).hexdigest() == SANCTIONED_SQL_SHA256
`, digestText)

	// Assert.
	if !slices.Contains(contract.Executor.Receipt, "executed_sql") {
		t.Fatalf("executor receipt = %#v, want executed_sql", contract.Executor.Receipt)
	}
	if slices.Contains(contract.Executor.Receipt, "expected_sql") {
		t.Fatal("attester must not trust undeclared expected_sql receipt bytes")
	}
	if !bytes.Equal(attesterBytes, []byte(wantAttester)) {
		t.Fatalf("attester source differs from the canonical inert template parameterized by SQL digest %s", digestText)
	}
}

func TestV02CompatibilityCorpusPreservesFallbackPrecedenceAndBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		relative         string
		conceptID        string
		wantDeclared     string
		wantSource       bundle.VersionSource
		wantEffectiveAt  string
		wantSources      int
		wantCitations    int
		wantGraphText    string
		rejectGraphText  string
		wantUnknownField string
	}{
		{
			name:            "declared v0.1 fallbacks",
			relative:        filepath.Join("compat", "declared-v01"),
			conceptID:       "legacy",
			wantDeclared:    bundle.LegacyOKFVersion,
			wantSource:      bundle.VersionSourceDeclared,
			wantEffectiveAt: "2026-06-01T10:00:00Z",
			wantCitations:   1,
			wantGraphText:   "https://example.invalid/legacy-source",
		},
		{
			name:            "undeclared v0.1 fallbacks",
			relative:        filepath.Join("compat", "undeclared-v01"),
			conceptID:       "legacy",
			wantSource:      bundle.VersionSourceDefault,
			wantEffectiveAt: "2026-05-28T22:53:05Z",
			wantCitations:   1,
			wantGraphText:   "https://example.test/legacy",
		},
		{
			name:            "v0.2 presence suppresses legacy",
			relative:        filepath.Join("compat", "mixed-provenance"),
			conceptID:       "mixed",
			wantSource:      bundle.VersionSourceDeclared,
			wantDeclared:    bundle.OKFVersion,
			wantEffectiveAt: "2026-07-01T10:00:00Z",
			wantSources:     1,
			rejectGraphText: "https://example.invalid/legacy-source",
		},
		{
			name:             "future declaration remains visible",
			relative:         filepath.Join("compat", "future-version"),
			conceptID:        "future",
			wantDeclared:     "9.9",
			wantSource:       bundle.VersionSourceFutureBestEffort,
			wantUnknownField: "future_family",
			wantGraphText:    "9.9",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := filepath.Join("fixtures", "v02", tt.relative)
			sourcePath := filepath.Join(root, tt.conceptID+".md")
			before, err := os.ReadFile(sourcePath)
			if err != nil {
				t.Fatalf("ReadFile(before) error = %v", err)
			}
			loaded, err := bundle.LoadBundle(root)
			if err != nil {
				t.Fatalf("LoadBundle() error = %v", err)
			}
			id, err := bundle.ParseConceptID(tt.conceptID)
			if err != nil {
				t.Fatalf("ParseConceptID() error = %v", err)
			}
			concept, ok := loaded.Get(id)
			if !ok {
				t.Fatalf("Get(%s) = false", tt.conceptID)
			}

			// Act.
			resolution, err := loaded.VersionResolution("")
			if err != nil {
				t.Fatalf("VersionResolution(auto) error = %v", err)
			}
			effectiveAt, hasEffectiveAt := concept.Document.EffectiveContentChangeTime()
			sources := concept.Document.Sources()
			citations := concept.Document.Citations()
			report := validator.ValidateBundle(loaded, &validator.ValidatorConfig{Strict: true})
			var projection bytes.Buffer
			if err := graph.RenderJSONLDWithOptions(
				&projection,
				loaded,
				graph.Options{Profile: graph.ProjectionProfileToolkitV02},
			); err != nil {
				t.Fatalf("RenderJSONLDWithOptions() error = %v", err)
			}
			after, err := os.ReadFile(sourcePath)
			if err != nil {
				t.Fatalf("ReadFile(after) error = %v", err)
			}

			// Assert.
			if resolution.Declared != tt.wantDeclared || resolution.Source != tt.wantSource {
				t.Fatalf("VersionResolution(auto) = %#v, want declared=%q source=%q", resolution, tt.wantDeclared, tt.wantSource)
			}
			if tt.wantEffectiveAt != "" && (!hasEffectiveAt || effectiveAt != tt.wantEffectiveAt) {
				t.Fatalf("EffectiveContentChangeTime() = %q, %v; want %q, true", effectiveAt, hasEffectiveAt, tt.wantEffectiveAt)
			}
			if len(sources) != tt.wantSources || len(citations) != tt.wantCitations {
				t.Fatalf("Sources/Citations = %d/%d, want %d/%d", len(sources), len(citations), tt.wantSources, tt.wantCitations)
			}
			if tt.wantUnknownField != "" {
				if _, ok := concept.Document.Frontmatter.Get(tt.wantUnknownField); !ok {
					t.Fatalf("future field %q was not preserved", tt.wantUnknownField)
				}
			}
			if !report.IsConformant() {
				t.Fatalf("strict report errors = %#v", report.Diagnostics)
			}
			if tt.wantGraphText != "" && !strings.Contains(projection.String(), tt.wantGraphText) {
				t.Fatalf("projection does not contain %q", tt.wantGraphText)
			}
			if tt.rejectGraphText != "" && strings.Contains(projection.String(), tt.rejectGraphText) {
				t.Fatalf("projection unexpectedly contains suppressed legacy value %q", tt.rejectGraphText)
			}
			if !bytes.Equal(after, before) {
				t.Fatal("read/validate/graph changed source bytes")
			}
		})
	}
}

func TestV02AdversarialTrustSignalsAndMarkdownOwnership(t *testing.T) {
	t.Parallel()

	// Arrange.
	trustRoot := filepath.Join("fixtures", "v02", "adversarial", "trust-signals")
	trustBundle, err := bundle.LoadBundle(trustRoot)
	if err != nil {
		t.Fatalf("LoadBundle(trust-signals) error = %v", err)
	}
	trustID, err := bundle.ParseConceptID("untrusted-signals")
	if err != nil {
		t.Fatalf("ParseConceptID(untrusted-signals) error = %v", err)
	}
	trustConcept, ok := trustBundle.Get(trustID)
	if !ok {
		t.Fatal("untrusted-signals concept not loaded")
	}
	footnoteRoot := filepath.Join("fixtures", "v02", "strict-footnote-integrity")
	footnoteBundle, err := bundle.LoadBundle(footnoteRoot)
	if err != nil {
		t.Fatalf("LoadBundle(strict-footnote-integrity) error = %v", err)
	}
	footnoteID, err := bundle.ParseConceptID("footnotes")
	if err != nil {
		t.Fatalf("ParseConceptID(footnotes) error = %v", err)
	}
	footnoteConcept, ok := footnoteBundle.Get(footnoteID)
	if !ok {
		t.Fatal("footnotes concept not loaded")
	}

	// Act.
	sources := trustConcept.Document.Sources()
	trustReport := validator.ValidateBundle(trustBundle, &validator.ValidatorConfig{Strict: true})
	attributions := footnoteConcept.Document.Attributions()
	footnoteReport := validator.ValidateBundle(footnoteBundle, &validator.ValidatorConfig{Strict: true})

	// Assert.
	if trustConcept.Document.TrustTier() != bundle.TrustUnverified {
		t.Fatalf("TrustTier() = %q, want unverified despite human source author and usage_count", trustConcept.Document.TrustTier())
	}
	if len(sources) != 1 || sources[0].Author != "human:source-writer" ||
		sources[0].UsageCount == nil || *sources[0].UsageCount != ^uint64(0) {
		t.Fatalf("typed source signals = %#v, want human author and MaxUint64", sources)
	}
	if !trustReport.IsConformant() || trustReport.WarningCount() != 0 {
		t.Fatalf("trust-signals strict diagnostics = %#v, want clean", trustReport.Diagnostics)
	}
	var attributionIDs []string
	for _, attribution := range attributions {
		attributionIDs = append(attributionIDs, attribution.ID)
	}
	if !slices.Equal(attributionIDs, []string{"duplicate", "orphan", "unknown"}) {
		t.Fatalf("prose-owned attribution IDs = %#v; code markers must be absent", attributionIDs)
	}
	if !footnoteReport.IsConformant() || footnoteReport.WarningCount() != 4 {
		t.Fatalf("footnote strict diagnostics = %#v, want four policy warnings and no errors", footnoteReport.Diagnostics)
	}
}
