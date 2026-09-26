package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/skosovsky/okf/internal/okfcli"
)

func TestBackfillWriterPublishesConsumerCorpus(t *testing.T) {
	// Arrange
	m, err := os.ReadFile("../../backfill/testdata/reversal-events.json")
	if err != nil {
		t.Fatal(err)
	}
	a, err := os.ReadFile("../../backfill/testdata/reversal-analysis.json")
	if err != nil {
		t.Fatal(err)
	}
	// Act
	cases, coverage, err := BuildReversalFixtureCorpus(context.Background(), m, a)
	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 3 || coverage.IncludedEvents != 3 || len(coverage.TruncatedEvents) != 1 {
		t.Fatalf("cases=%d coverage=%+v", len(cases), coverage)
	}
	for _, c := range cases {
		for _, artifact := range c.Treatment {
			if artifact.ID == "decision" && bytes.Contains([]byte(artifact.Content), []byte("The implementation uses mode A.")) {
				t.Fatalf("%s: stale claim remained active", c.ID)
			}
		}
	}
	committed, err := os.ReadFile("corpus/backfill_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var frozen []Case
	if err := json.Unmarshal(committed, &frozen); err != nil {
		t.Fatal(err)
	}
	left, _ := json.Marshal(cases)
	right, _ := json.Marshal(frozen)
	if !bytes.Equal(left, right) {
		t.Fatal("committed backfill consumer corpus drifted")
	}
}

func TestTreatmentBundlesPassGoCLIValidation(t *testing.T) {
	// Arrange
	mainCases, _ := fixture(t)
	f, err := os.Open("corpus/metadata_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	metadataCases, _, err := LoadCorpus(f)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range append(mainCases, metadataCases...) {
		t.Run(c.ID, func(t *testing.T) {
			root := t.TempDir()
			for _, a := range c.Treatment {
				path := filepath.Join(root, a.Path)
				if err := os.WriteFile(path, []byte(a.Content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			// Act
			var stdout, stderr bytes.Buffer
			code := okfcli.Run([]string{"validate", "--path", root, "--json"}, &stdout, &stderr)
			// Assert
			if code != 0 {
				t.Fatalf("Go CLI rejected bundle: exit %d, stdout %s, stderr %s", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestSchemaAcceptsCommittedCorpus(t *testing.T) {
	// Arrange
	data, err := os.ReadFile("schema.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("schema.json", doc); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("schema.json")
	if err != nil {
		t.Fatal(err)
	}
	corpus, err := os.ReadFile("corpus/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal(corpus, &value); err != nil {
		t.Fatal(err)
	}
	// Act
	err = schema.Validate(value)
	// Assert
	if err != nil {
		t.Fatal(err)
	}
}

func TestSchemaCoversPlanRequestRowAndReport(t *testing.T) {
	// Arrange
	cases, hash := fixture(t)
	m := Metadata{RunID: "pilot", CorpusSHA256: hash, PromptSHA256: hash, Commit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SpecRevision: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Model: "model", ModelVersion: "model", Settings: `{"reasoning_effort":"low"}`, Adapter: "adapter", Clock: time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC), GraderRevision: "exact-v1"}
	plan := Plan{Metadata: m, SpecSHA256: hash, AdapterSHA256: hash, ToolkitMode: "treatment", ModelToolAccess: "read-only temp", Repeats: 1, CaseCount: len(cases), MaxTrials: len(cases) * 2, MaxSeconds: 60, MaxTokens: 1000, Unpriced: true}
	obs := Observation{Answer: cases[0].Expected.Answer, Evidence: []string{"current"}}
	row := Row{Metadata: m, CaseID: cases[0].ID, Arm: Control, Repeat: 1, Observation: obs, Verdict: Correct}
	report, err := AnalyzePlanned(cases, hash, []Row{row}, plan)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]any{"plan": plan, "request": Request{CaseID: cases[0].ID, Arm: Control, Question: cases[0].Question, Artifacts: cases[0].Control, Instructions: "answer"}, "observation": obs, "row": row, "report": report}
	raw, err := os.ReadFile("schema.json")
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range values {
		t.Run(name, func(t *testing.T) {
			var schemaDoc map[string]any
			if err := json.Unmarshal(raw, &schemaDoc); err != nil {
				t.Fatal(err)
			}
			delete(schemaDoc, "type")
			delete(schemaDoc, "minItems")
			delete(schemaDoc, "items")
			schemaDoc["$ref"] = "#/$defs/" + name
			data, err := json.Marshal(schemaDoc)
			if err != nil {
				t.Fatal(err)
			}
			doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			compiler := jsonschema.NewCompiler()
			if err := compiler.AddResource("test-schema.json", doc); err != nil {
				t.Fatal(err)
			}
			schema, err := compiler.Compile("test-schema.json")
			if err != nil {
				t.Fatal(err)
			}
			wire, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			var generic any
			if err := json.Unmarshal(wire, &generic); err != nil {
				t.Fatal(err)
			}
			// Act
			err = schema.Validate(generic)
			// Assert
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func fixture(t *testing.T) ([]Case, string) {
	t.Helper()
	f, err := os.Open("corpus/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cases, hash, err := LoadCorpus(f)
	if err != nil {
		t.Fatal(err)
	}
	return cases, hash
}

func TestMetadataContrastKeepsIdenticalBodiesAndCorrectEvidence(t *testing.T) {
	// Arrange
	f, err := os.Open("corpus/metadata_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cases, _, err := LoadCorpus(f)
	if err != nil {
		t.Fatal(err)
	}
	// Act and assert
	if len(cases) != 4 {
		t.Fatalf("metadata cases=%d", len(cases))
	}
	for _, c := range cases {
		for i, a := range c.Control {
			b := c.Treatment[i]
			if a.ID != b.ID || a.Path != b.Path {
				t.Fatalf("%s: differing artifact identity", c.ID)
			}
			if !bytes.HasSuffix([]byte(b.Content), []byte(a.Content+"\n")) && !bytes.HasSuffix([]byte(b.Content), []byte(a.Content)) {
				t.Fatalf("%s/%s: differing factual body", c.ID, a.ID)
			}
		}
	}
}

func TestCorpusHasRequiredCategoriesAndAvailableAnswers(t *testing.T) {
	// Arrange
	cases, _ := fixture(t)
	want := map[string]bool{"changed_default": false, "changed_limit": false, "reversal": false, "stale_vs_code": false, "injection": false, "verification": false, "intent_vs_execution": false, "truncated_diff": false, "insufficient_evidence": false}
	// Act
	for _, c := range cases {
		if _, ok := want[c.Category]; ok {
			want[c.Category] = true
		}
		for _, arm := range [][]Artifact{c.Control, c.Treatment} {
			found := false
			for _, a := range arm {
				if bytes.Contains([]byte(a.Content), []byte(c.Expected.Answer)) {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: correct answer unavailable in arm", c.ID)
			}
		}
	}
	// Assert
	if len(cases) < 12 || len(cases) > 20 {
		t.Errorf("case count %d", len(cases))
	}
	for k, v := range want {
		if !v {
			t.Errorf("missing category %s", k)
		}
	}
}

func TestRealisticCasePinsActualGoSource(t *testing.T) {
	// Arrange
	cases, _ := fixture(t)
	realistic := 0
	for _, c := range cases {
		if c.Tier != "realistic" {
			continue
		}
		realistic++
		for _, support := range c.Expected.Support {
			if support.RepoPath == "" {
				t.Fatalf("%s: unpinned realistic source", c.ID)
			}
			data, err := os.ReadFile(filepath.Join("../..", support.RepoPath))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(data, []byte(support.Quote)) {
				t.Fatalf("%s: quoted source drifted", c.ID)
			}
			matched := false
			for _, artifact := range c.Control {
				if artifact.ID == support.ArtifactID && bytes.Contains(data, []byte(artifact.Content)) {
					matched = true
				}
			}
			if !matched {
				t.Fatalf("%s: control excerpt no longer matches Go source", c.ID)
			}
		}
	}
	// Act and assert
	if realistic < 4 || realistic > 6 {
		t.Fatalf("realistic cases=%d", realistic)
	}
}

func TestPrimaryRealisticSubsetMatchesMainCorpus(t *testing.T) {
	// Arrange
	all, _ := fixture(t)
	f, err := os.Open("corpus/primary_realistic.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	subset, _, err := LoadCorpus(f)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Case{}
	for _, c := range all {
		byID[c.ID] = c
	}
	// Act and assert
	if len(subset) != 4 {
		t.Fatalf("primary subset has %d cases", len(subset))
	}
	for _, c := range subset {
		if c.Tier != "realistic" {
			t.Fatalf("%s is synthetic", c.ID)
		}
		left, _ := json.Marshal(c)
		right, _ := json.Marshal(byID[c.ID])
		if !bytes.Equal(left, right) {
			t.Fatalf("%s drifted from main corpus", c.ID)
		}
	}
}

func TestGradeAdversarialAndPositive(t *testing.T) {
	// Arrange
	cases, _ := fixture(t)
	c := cases[0]
	tests := []struct {
		name          string
		obs           Observation
		failure, want string
	}{
		{"current with evidence", Observation{Answer: c.Expected.Answer, Evidence: []string{"current"}}, "", Correct},
		{"terminal punctuation", Observation{Answer: c.Expected.Answer + ".", Evidence: []string{"current"}}, "", Correct},
		{"historical answer", Observation{Answer: c.Expected.StaleAnswers[0], Evidence: []string{"historical"}}, "", Stale},
		{"answer without evidence", Observation{Answer: c.Expected.Answer}, "", Ungradable},
		{"refusal", Observation{Refused: true}, "", Refusal},
		{"wrong", Observation{Answer: "unknown", Evidence: []string{"current"}}, "", Wrong},
		{"adapter failed", Observation{}, "timeout", OperationalFailure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act
			got := Grade(c, tt.obs, tt.failure)
			// Assert
			if got != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestAnalyzeIncludesMissingAndFailuresInDenominator(t *testing.T) {
	// Arrange
	cases, hash := fixture(t)
	m := Metadata{RunID: "pilot", CorpusSHA256: hash, PromptSHA256: hash, Commit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SpecRevision: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Model: "m", ModelVersion: "v", Settings: "x", Adapter: "a", Clock: time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC), GraderRevision: "exact-v1"}
	rows := []Row{{Metadata: m, CaseID: cases[0].ID, Arm: Control, Repeat: 1, Observation: Observation{Answer: cases[0].Expected.Answer, Evidence: []string{"current"}}, Verdict: Correct}, {Metadata: m, CaseID: cases[0].ID, Arm: Treatment, Repeat: 1, Failure: "timeout", Verdict: OperationalFailure}}
	// Act
	r, err := Analyze(cases, hash, rows, 1)
	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if r.Valid || r.Arms[Control].Missing != len(cases)-1 || r.Arms[Treatment].OperationalFailure != 1 {
		t.Fatalf("bad accounting: %+v", r)
	}
}

func TestRejectDuplicateAndTamperedRows(t *testing.T) {
	// Arrange
	cases, hash := fixture(t)
	m := Metadata{RunID: "pilot", CorpusSHA256: hash, PromptSHA256: hash, Commit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SpecRevision: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Model: "m", ModelVersion: "v", Settings: "x", Adapter: "a", Clock: time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC), GraderRevision: "exact-v1"}
	row := Row{Metadata: m, CaseID: cases[0].ID, Arm: Control, Repeat: 1, Observation: Observation{Answer: cases[0].Expected.Answer, Evidence: []string{"current"}}, Verdict: Wrong}
	// Act and assert
	if _, err := Analyze(cases, hash, []Row{row}, 1); err == nil {
		t.Fatal("accepted tampered verdict")
	}
	row.Verdict = Correct
	if _, err := Analyze(cases, hash, []Row{row, row}, 1); err == nil {
		t.Fatal("accepted duplicate trial")
	}
}

func TestAnalyzeChecksPrerecordedPlan(t *testing.T) {
	// Arrange
	cases, hash := fixture(t)
	m := Metadata{RunID: "pilot", CorpusSHA256: hash, PromptSHA256: hash, Commit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SpecRevision: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Model: "model", ModelVersion: "model", Settings: `{"reasoning_effort":"low"}`, Adapter: "adapter", Clock: time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC), GraderRevision: "exact-v1"}
	plan := Plan{Metadata: m, SpecSHA256: hash, AdapterSHA256: hash, ToolkitMode: "treatment", ModelToolAccess: "read-only temp", Repeats: 1, CaseCount: len(cases), MaxTrials: len(cases) * 2, MaxSeconds: 60, MaxTokens: 1000, Unpriced: true}
	row := Row{Metadata: m, CaseID: cases[0].ID, Arm: Control, Repeat: 1, Observation: Observation{Answer: cases[0].Expected.Answer, Evidence: []string{"current"}}, Verdict: Correct}
	// Act
	_, err := AnalyzePlanned(cases, hash, []Row{row}, plan)
	// Assert
	if err != nil {
		t.Fatal(err)
	}
	row.Metadata.Model = "other"
	if _, err := AnalyzePlanned(cases, hash, []Row{row}, plan); err == nil {
		t.Fatal("accepted model drift")
	}
}

func TestDirectModeRequiresObservedGoCLICall(t *testing.T) {
	// Arrange
	cases, hash := fixture(t)
	m := Metadata{RunID: "direct", CorpusSHA256: hash, PromptSHA256: hash, Commit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SpecRevision: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Model: "model", ModelVersion: "model", Settings: `{"reasoning_effort":"low"}`, Adapter: "adapter", Clock: time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC), GraderRevision: "exact-v1"}
	plan := Plan{Metadata: m, SpecSHA256: hash, AdapterSHA256: hash, GoCLISHA256: hash, ModelRuntimePath: "/absolute/codex", ModelRuntimeVersion: "codex-cli 0.155.0-alpha.16.3", ModelRuntimeSHA256: hash, ToolkitMode: "direct", ModelToolAccess: "read-only shell", Repeats: 1, CaseCount: len(cases), MaxTrials: len(cases) * 2, MaxSeconds: 60, MaxTokens: 1000, Unpriced: true}
	row := Row{Metadata: m, CaseID: cases[0].ID, Arm: Treatment, Repeat: 1, Observation: Observation{Answer: cases[0].Expected.Answer, Evidence: []string{"current"}, InputTokens: 10, OutputTokens: 5}, Verdict: Correct}
	// Act and assert
	if _, err := AnalyzePlanned(cases, hash, []Row{row}, plan); err == nil {
		t.Fatal("accepted no Go CLI call")
	}
	row.Observation.ToolkitCalls = 1
	row.Observation.ToolCalls = 1
	report, err := AnalyzePlanned(cases, hash, []Row{row}, plan)
	if err != nil {
		t.Fatal(err)
	}
	if report.ModelRuntimePath != plan.ModelRuntimePath || report.ModelRuntimeVersion != plan.ModelRuntimeVersion || report.ModelRuntimeSHA256 != hash {
		t.Fatal("model runtime provenance missing from report")
	}
	plan.ModelRuntimeSHA256 = "invalid"
	if _, err := AnalyzePlanned(cases, hash, []Row{row}, plan); err == nil {
		t.Fatal("accepted malformed runtime hash")
	}
}

func TestMalformedRawRowRemainsVisible(t *testing.T) {
	// Arrange
	cases, hash := fixture(t)
	m := Metadata{RunID: "pilot", CorpusSHA256: hash, PromptSHA256: hash, Commit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SpecRevision: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Model: "model", ModelVersion: "model", Settings: `{"reasoning_effort":"low"}`, Adapter: "adapter", Clock: time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC), GraderRevision: "exact-v1"}
	plan := Plan{Metadata: m, SpecSHA256: hash, AdapterSHA256: hash, ToolkitMode: "treatment", ModelToolAccess: "read-only temp", Repeats: 1, CaseCount: len(cases), MaxTrials: len(cases) * 2, MaxSeconds: 60, MaxTokens: 1000, Unpriced: true}
	raw := bytes.NewBufferString("{broken\n{}\n")
	// Act
	rows, malformed, err := ReadRowsWithInvalid(raw)
	if err != nil {
		t.Fatal(err)
	}
	report, err := AnalyzePlannedWithInvalid(cases, hash, rows, malformed, plan)
	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if report.Valid || report.InvalidRawRows != 2 || report.Arms[Control].Missing != len(cases) {
		t.Fatalf("malformed accounting: %+v", report)
	}
}
