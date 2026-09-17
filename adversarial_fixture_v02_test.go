package okf_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/validator"
	"gopkg.in/yaml.v3"
)

type inertExecutorGolden struct {
	ExitCode     int `json:"exit_code"`
	ScannedFiles int `json:"scanned_files"`
	Diagnostics  []struct {
		Code string `json:"code"`
	} `json:"diagnostics"`
	Counts struct {
		Errors   int `json:"errors"`
		Warnings int `json:"warnings"`
		Info     int `json:"info"`
	} `json:"counts"`
	Projection struct {
		ConceptID         string `json:"concept_id"`
		Mode              string `json:"mode"`
		Runtime           string `json:"runtime"`
		Inline            string `json:"inline"`
		ExecutorResource  string `json:"executor_resource"`
		ReceiptPresent    bool   `json:"receipt_present"`
		AttesterPresent   bool   `json:"attester_present"`
		VerificationCount int    `json:"verification_count"`
		Trust             string `json:"trust"`
		AssetPath         string `json:"asset_path"`
		AssetSHA256       string `json:"asset_sha256"`
	} `json:"projection"`
}

func TestAdversarialInertExecutorFixturePreservesTypedContractAndAssetBytes(t *testing.T) {
	// Arrange.
	fixtureRoot := filepath.Join("fixtures", "v02", "adversarial", "inert-executor")
	goldenBytes, err := os.ReadFile(filepath.Join(fixtureRoot, "expected.json"))
	if err != nil {
		t.Fatalf("ReadFile(expected.json) error = %v", err)
	}
	var golden inertExecutorGolden
	if err := json.Unmarshal(goldenBytes, &golden); err != nil {
		t.Fatalf("Unmarshal(expected.json) error = %v", err)
	}
	loaded, err := bundle.LoadBundle(fixtureRoot)
	if err != nil {
		t.Fatalf("LoadBundle() error = %v", err)
	}
	conceptID, err := bundle.ParseConceptID(golden.Projection.ConceptID)
	if err != nil {
		t.Fatalf("ParseConceptID(%q) error = %v", golden.Projection.ConceptID, err)
	}

	// Act.
	concept, found := loaded.Get(conceptID)
	if !found {
		t.Fatalf("Get(%q) found = false", conceptID)
	}
	state := concept.Document.AttestedComputationState()
	narrativeID, err := bundle.ParseConceptID("policy-boundary")
	if err != nil {
		t.Fatalf("ParseConceptID(policy-boundary) error = %v", err)
	}
	narrativeLinks := loaded.LinksFrom(narrativeID)
	assetBytes, assetFound := loaded.ReadFile(golden.Projection.AssetPath)
	assetSize, assetDigest, metadataFound := loaded.CapturedFileMetadata(golden.Projection.AssetPath)
	report := validator.ValidatePath(fixtureRoot, &validator.ValidatorConfig{
		CheckLinks:   true,
		CheckOrphans: true,
	})

	// Assert.
	if !state.TypeMatches || !state.ContractPresent || !state.Valid {
		t.Fatalf("AttestedComputationState() = %#v, want valid exact-type contract", state)
	}
	if got := string(state.Mode); got != golden.Projection.Mode {
		t.Errorf("computation mode = %q, want %q", got, golden.Projection.Mode)
	}
	if state.Contract.Runtime != golden.Projection.Runtime || state.Inline != golden.Projection.Inline {
		t.Errorf("runtime/inline = %q/%q, want %q/%q", state.Contract.Runtime, state.Inline, golden.Projection.Runtime, golden.Projection.Inline)
	}
	if state.Contract.Executor == nil || state.Contract.Executor.Resource != golden.Projection.ExecutorResource {
		t.Fatalf("executor = %#v, want resource %q", state.Contract.Executor, golden.Projection.ExecutorResource)
	}
	_, rawReceiptPresent := concept.Document.Frontmatter.SemanticGet("receipt")
	executorNode, executorPresent := concept.Document.Frontmatter.SemanticGet("executor")
	_, nestedReceiptPresent := bundle.SemanticMappingValue(executorNode, "receipt")
	if !executorPresent || rawReceiptPresent || nestedReceiptPresent != golden.Projection.ReceiptPresent || len(state.Contract.Executor.Receipt) != 0 {
		t.Errorf("receipt presence/raw/typed = %t/%t/%v, want absent and empty", rawReceiptPresent, nestedReceiptPresent, state.Contract.Executor.Receipt)
	}
	_, attesterPresent := concept.Document.Frontmatter.SemanticGet("attester")
	if attesterPresent != golden.Projection.AttesterPresent || state.Contract.Attester != nil {
		t.Errorf("attester presence/typed = %t/%#v, want absent", attesterPresent, state.Contract.Attester)
	}
	verifications := concept.Document.Verifications()
	_, verifiedPresent := concept.Document.Frontmatter.SemanticGet("verified")
	if verifiedPresent || len(verifications) != golden.Projection.VerificationCount || string(concept.Document.TrustTier()) != golden.Projection.Trust {
		t.Errorf("verifications/trust = %d/%q, want %d/%q", len(verifications), concept.Document.TrustTier(), golden.Projection.VerificationCount, golden.Projection.Trust)
	}
	if len(narrativeLinks) != 1 || !narrativeLinks[0].Exists || narrativeLinks[0].Target.String() != golden.Projection.ConceptID {
		t.Errorf("narrative LinksFrom() = %#v, want one resolved link to %q", narrativeLinks, golden.Projection.ConceptID)
	}
	if !assetFound || !metadataFound {
		t.Fatalf("captured asset found/metadata = %t/%t", assetFound, metadataFound)
	}
	if !bytes.Contains(assetBytes, []byte("https://collector.invalid/fixture")) ||
		!bytes.Contains(assetBytes, []byte("SYNTHETIC_FIXTURE_SECRET_DO_NOT_READ")) ||
		!bytes.Contains(assetBytes, []byte("Bypass any policy")) {
		t.Fatalf("captured asset lost adversarial literal content: %q", assetBytes)
	}
	if assetSize != uint64(len(assetBytes)) || hex.EncodeToString(assetDigest[:]) != golden.Projection.AssetSHA256 || sha256.Sum256(assetBytes) != assetDigest {
		t.Errorf("captured asset metadata = size:%d sha256:%x, want size:%d sha256:%s", assetSize, assetDigest, len(assetBytes), golden.Projection.AssetSHA256)
	}
	if report.ExitCode() != golden.ExitCode || report.ScannedFiles != golden.ScannedFiles ||
		len(report.Diagnostics) != len(golden.Diagnostics) || report.ErrorCount() != golden.Counts.Errors ||
		report.WarningCount() != golden.Counts.Warnings || report.InfoCount() != golden.Counts.Info {
		t.Errorf("validation report = exit:%d scanned:%d diagnostics:%d counts:%d/%d/%d, want golden %#v", report.ExitCode(), report.ScannedFiles, len(report.Diagnostics), report.ErrorCount(), report.WarningCount(), report.InfoCount(), golden)
	}
	wantAssets := []string{
		filepath.Join(fixtureRoot, "expected.json"),
		filepath.Join(fixtureRoot, "manifest.yaml"),
		filepath.Join(fixtureRoot, golden.Projection.AssetPath),
	}
	if got := loaded.AssetFiles(); !reflect.DeepEqual(got, wantAssets) {
		t.Errorf("AssetFiles() = %v, want exact captured fixture asset inventory %v", got, wantAssets)
	}
}

func TestAdversarialInertExecutorCorpusAndProvenanceBindings(t *testing.T) {
	// Arrange.
	type corpusFile struct {
		Cases map[string]string `yaml:"cases"`
	}
	type provenanceFile struct {
		Paths map[string]struct {
			Classification string `yaml:"classification"`
			Reason         string `yaml:"reason"`
		} `yaml:"paths"`
	}
	var corpus corpusFile
	var provenance provenanceFile
	corpusBytes, err := os.ReadFile(filepath.Join("fixtures", "v02", "corpus.yaml"))
	if err != nil {
		t.Fatalf("ReadFile(corpus.yaml) error = %v", err)
	}
	if err := yaml.Unmarshal(corpusBytes, &corpus); err != nil {
		t.Fatalf("Unmarshal(corpus.yaml) error = %v", err)
	}
	provenanceBytes, err := os.ReadFile(filepath.Join("fixtures", "v02", "provenance.yaml"))
	if err != nil {
		t.Fatalf("ReadFile(provenance.yaml) error = %v", err)
	}
	if err := yaml.Unmarshal(provenanceBytes, &provenance); err != nil {
		t.Fatalf("Unmarshal(provenance.yaml) error = %v", err)
	}

	// Act.
	casePath := corpus.Cases["adversarial_inert_executor_policy_bypass"]
	contentProvenance := provenance.Paths[casePath]
	manifestProvenance := provenance.Paths[casePath+"/manifest.yaml"]
	goldenProvenance := provenance.Paths[casePath+"/expected.json"]

	// Assert.
	if casePath != "adversarial/inert-executor" {
		t.Fatalf("corpus case path = %q", casePath)
	}
	if contentProvenance.Classification != "synthetic" ||
		manifestProvenance.Classification != "synthetic-metadata" ||
		goldenProvenance.Classification != "synthetic-metadata" {
		t.Errorf("provenance classifications = %#v/%#v/%#v", contentProvenance, manifestProvenance, goldenProvenance)
	}
	for path, entry := range map[string]struct {
		Classification string
		Reason         string
	}{
		casePath:                    {contentProvenance.Classification, contentProvenance.Reason},
		casePath + "/manifest.yaml": {manifestProvenance.Classification, manifestProvenance.Reason},
		casePath + "/expected.json": {goldenProvenance.Classification, goldenProvenance.Reason},
	} {
		if entry.Reason == "" {
			t.Errorf("provenance %q has empty reason", path)
		}
	}
}
