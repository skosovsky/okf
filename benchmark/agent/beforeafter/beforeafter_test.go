package beforeafter

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/skosovsky/okf/benchmark/agent"
)

func fixtureRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestCorpusUsesFrozenEvidenceAndSeparateSpecs(t *testing.T) {
	// Arrange.
	root := fixtureRoot(t)
	// Act.
	cases, corpusSHA, fixtureSHA, err := LoadCorpus(filepath.Join(root, "benchmark/agent/beforeafter/corpus.json"), root)
	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 5 || len(corpusSHA) != 64 || len(fixtureSHA) != 64 {
		t.Fatalf("unexpected corpus: %d %q %q", len(cases), corpusSHA, fixtureSHA)
	}
	count := map[string]int{}
	for _, c := range cases {
		count[c.Stratum]++
	}
	if count["005"] != 4 || count["004"] != 1 {
		t.Fatalf("wrong strata: %#v", count)
	}
}

func TestCorpusRejectsUnsupportedRevisionMixedWithDateCases(t *testing.T) {
	// Arrange.
	root := fixtureRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "benchmark/agent/beforeafter/corpus.json"))
	if err != nil {
		t.Fatal(err)
	}
	bad := strings.Replace(string(b), `"spec_revision": "`+InstantSpec+`"`, `"spec_revision": "`+DateSpec+`"`, 1)
	path := filepath.Join(t.TempDir(), "cases.json")
	if err := os.WriteFile(path, []byte(bad), 0600); err != nil {
		t.Fatal(err)
	}
	// Act.
	_, _, _, err = LoadCorpus(path, root)
	// Assert.
	if err == nil || !strings.Contains(err.Error(), "004 requires instant spec") {
		t.Fatalf("wrong error: %v", err)
	}
}

func TestCLIExitOneWithWellFormedDiagnosticsIsModelEvidence(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("helper executable env is covered by Unix CI")
	}
	// Arrange.
	t.Setenv("OKF_BAF_TEST_HELPER", "exit1")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	// Act.
	r, err := runCLI(context.Background(), self, "binary-sha", root, []string{"validate", "--path", root, "--json"}, true)
	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if r.ExitCode != 1 || r.Errors != 2 || r.Diagnostics != 2 || !strings.Contains(r.Projection, `"diagnostics"`) {
		t.Fatalf("lost diagnostics: %+v", r)
	}
}

func TestParseExitOneWithArbitraryJSONIsOperationalFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("helper executable env is covered by Unix CI")
	}
	// Arrange.
	t.Setenv("OKF_BAF_TEST_HELPER", "parsebad")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// Act.
	_, err = runCLI(context.Background(), self, "binary-sha", t.TempDir(), []string{"parse", "example.md", "--json"}, false)
	// Assert.
	if err == nil || !strings.Contains(err.Error(), "parse projection nonconformant") {
		t.Fatalf("wrong parse outcome: %v", err)
	}
}

func TestAnalyzeKeepsUnsupportedOutsideFactualDenominator(t *testing.T) {
	// Arrange.
	cases := []Case{{ID: "capability", Stratum: "004", SpecRevision: InstantSpec, ParseFile: "example.md", Answer: "policy", EvidenceFile: "example.md"}}
	plan := Plan{RunID: "run", CorpusSHA256: "x", PromptSHA256: hash([]byte(prompt)), ExpectedRows: 2, Repeats: 1, Old: ArmPin{Commit: OldCommit, BinarySHA256: "old-sha"}, New: ArmPin{BinarySHA256: "new-sha"}, Strata: map[string]StratumPin{"005": {OldSpecRevision: DateSpec, NewSpecRevision: DateSpec}, "004": {OldSpecRevision: DateSpec, NewSpecRevision: InstantSpec, OldCapability: RevisionUnsupported}}}
	validateArgs, parseArgs := cliArgs(cases[0], "<bundle>", false)
	rows := []Row{{RunID: "run", CaseID: "capability", Stratum: "004", SpecRevision: InstantSpec, Arm: "old", Repeat: 1, Capability: RevisionUnsupported, Verdict: RevisionUnsupported},
		{RunID: "run", CaseID: "capability", Stratum: "004", SpecRevision: InstantSpec, Arm: "new", Repeat: 1, CLIValidate: CLIResult{CommandSHA256: commandDigest("new-sha", validateArgs), Projection: `{"errors":0,"diagnostics":[]}`}, CLIParse: CLIResult{CommandSHA256: commandDigest("new-sha", parseArgs), Projection: `{"conformant":true,"effective_version":"0.2","file":"example.md"}`}, ModelAttempted: true, Observation: agent.Observation{Answer: "policy", Evidence: []string{"example.md"}, InputTokens: 10, OutputTokens: 1}, Verdict: agent.Correct}}
	// Act.
	report, err := Analyze(plan, cases, rows)
	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if !report.Valid || report.CapabilityUnsupported != 1 || report.Verdicts["new"][agent.Correct] != 1 || report.Paired005 != 0 {
		t.Fatalf("wrong accounting: %+v", report)
	}
	for _, tc := range []struct {
		name   string
		mutate func([]Row)
	}{
		{"command digest", func(rs []Row) { rs[1].CLIParse.CommandSHA256 = "tampered" }},
		{"zero model usage", func(rs []Row) { rs[1].Observation.InputTokens = 0; rs[1].Observation.OutputTokens = 0 }},
		{"projection disagrees", func(rs []Row) { rs[1].CLIValidate.Errors = 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange.
			mutated := append([]Row(nil), rows...)
			tc.mutate(mutated)
			// Act.
			_, err := Analyze(plan, cases, mutated)
			// Assert.
			if err == nil {
				t.Fatal("tampered row accepted")
			}
		})
	}
	badPlan := plan
	badPlan.Strata = map[string]StratumPin{"005": {OldSpecRevision: DateSpec, NewSpecRevision: InstantSpec}}
	if _, err := Analyze(badPlan, cases, rows); err == nil {
		t.Fatal("mixed contract accepted")
	}
}

func TestPinnedCLIIntegration(t *testing.T) {
	oldBinary, newBinary := os.Getenv("OKF_BAF_OLD_BIN"), os.Getenv("OKF_BAF_NEW_BIN")
	if oldBinary == "" || newBinary == "" {
		t.Skip("set pinned old/new CLI binaries for the manual offline preflight")
	}
	// Arrange.
	root := fixtureRoot(t)
	cases, _, _, err := LoadCorpus(filepath.Join(root, "benchmark/agent/beforeafter/corpus.json"), root)
	if err != nil {
		t.Fatal(err)
	}
	// Act and Assert.
	for _, c := range cases {
		files, err := bundleFiles(filepath.Join(root, c.Bundle))
		if err != nil {
			t.Fatal(err)
		}
		bundle := t.TempDir()
		if err := materialize(bundle, files); err != nil {
			t.Fatal(err)
		}
		newValidation, newParse, err := CLIProjection(context.Background(), newBinary, "new-sha", bundle, c, false)
		if err != nil {
			t.Fatalf("%s new: %v", c.ID, err)
		}
		if newValidation.ExitCode != 0 || newValidation.Errors != 0 || newParse.Projection == "" {
			t.Fatalf("%s new result: %+v %+v", c.ID, newValidation, newParse)
		}
		if c.Stratum == "005" {
			oldValidation, oldParse, err := CLIProjection(context.Background(), oldBinary, "old-sha", bundle, c, true)
			if err != nil {
				t.Fatalf("%s old: %v", c.ID, err)
			}
			if oldValidation.ExitCode != 1 || oldValidation.Errors == 0 || oldValidation.Diagnostics == 0 || oldParse.Projection == "" {
				t.Fatalf("%s old result: %+v %+v", c.ID, oldValidation, oldParse)
			}
			if oldValidation.Errors != 12 || len(oldValidation.Assessments) != 12 {
				t.Fatalf("%s old diagnostic contract changed: %+v", c.ID, oldValidation)
			}
			for _, assessment := range oldValidation.Assessments {
				if assessment.Classification == "unclassified" {
					t.Fatalf("%s unclassified diagnostic: %+v", c.ID, assessment)
				}
			}
		}
	}
}

func TestAnalyzeReportsPaired005OutcomeAndSeparateUsage(t *testing.T) {
	// Arrange.
	c := Case{ID: "date-case", Stratum: "005", SpecRevision: DateSpec, ParseFile: "concept.md", Answer: "new", EvidenceFile: "concept.md"}
	plan := Plan{RunID: "paired", CorpusSHA256: "x", PromptSHA256: hash([]byte(prompt)), ExpectedRows: 2, Repeats: 1, Old: ArmPin{Commit: OldCommit, BinarySHA256: "old-bin"}, New: ArmPin{BinarySHA256: "new-bin"}, Strata: map[string]StratumPin{"005": {OldSpecRevision: DateSpec, NewSpecRevision: DateSpec}, "004": {OldSpecRevision: DateSpec, NewSpecRevision: InstantSpec, OldCapability: RevisionUnsupported}}}
	var assessments []DiagnosticAssessment
	for i := 0; i < 12; i++ {
		a := DiagnosticAssessment{Code: "log_structure_invalid", File: "log.md", FieldPath: "body", SpecRef: "okf-v0.2#11.3"}
		a.Classification = classifyDiagnostic(a)
		assessments = append(assessments, a)
	}
	projected, _ := json.Marshal(map[string]any{"errors": 12, "diagnostics": assessments})
	oldValidateArgs, oldParseArgs := cliArgs(c, "<bundle>", true)
	newValidateArgs, newParseArgs := cliArgs(c, "<bundle>", false)
	rows := []Row{
		{RunID: "paired", CaseID: c.ID, Stratum: "005", SpecRevision: DateSpec, Arm: "old", Repeat: 1, ModelAttempted: true, CLIValidate: CLIResult{CommandSHA256: commandDigest("old-bin", oldValidateArgs), ExitCode: 1, Errors: 12, Diagnostics: 12, Assessments: assessments, Projection: string(projected)}, CLIParse: CLIResult{CommandSHA256: commandDigest("old-bin", oldParseArgs), Projection: `{"conformant":true,"effective_version":"0.2","file":"concept.md"}`}, Observation: agent.Observation{Answer: "old", Evidence: []string{"concept.md"}, InputTokens: 20, OutputTokens: 2}, Verdict: agent.Wrong},
		{RunID: "paired", CaseID: c.ID, Stratum: "005", SpecRevision: DateSpec, Arm: "new", Repeat: 1, ModelAttempted: true, CLIValidate: CLIResult{CommandSHA256: commandDigest("new-bin", newValidateArgs), Projection: `{"errors":0,"diagnostics":[]}`}, CLIParse: CLIResult{CommandSHA256: commandDigest("new-bin", newParseArgs), Projection: `{"conformant":true,"effective_version":"0.2","file":"concept.md"}`}, Observation: agent.Observation{Answer: "new", Evidence: []string{"concept.md"}, InputTokens: 21, OutputTokens: 2}, Verdict: agent.Correct},
	}
	// Act.
	report, err := Analyze(plan, []Case{c}, rows)
	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if !report.Valid || report.Paired005 != 1 || report.Paired005Outcomes["wrong->correct"] != 1 || report.VerdictsByStratum["005/old"][agent.Wrong] != 1 || report.UsageByStratumArm["005/new"].InputTokens != 21 {
		t.Fatalf("wrong paired summary: %+v", report)
	}
}

func TestInvalidAdapterUsageCannotReduceBudget(t *testing.T) {
	// Arrange.
	negative := -0.01
	nan := math.NaN()
	observations := []agent.Observation{{InputTokens: -1}, {OutputTokens: -1}, {CacheTokens: -1}, {CostUSD: &negative}, {CostUSD: &nan}}
	// Act and Assert.
	for _, obs := range observations {
		if !invalidUsage(obs) {
			t.Fatalf("invalid usage accepted: %+v", obs)
		}
	}
	if invalidUsage(agent.Observation{InputTokens: 1, OutputTokens: 2}) {
		t.Fatal("valid usage rejected")
	}
}

func TestMain(m *testing.M) {
	if os.Getenv("OKF_BAF_TEST_HELPER") == "exit1" {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"conformant": false, "errors": 2, "warnings": 0, "diagnostics": []map[string]string{{"code": "log_item"}, {"code": "root_intro"}}})
		os.Exit(1)
	}
	if os.Getenv("OKF_BAF_TEST_HELPER") == "parsebad" {
		_, _ = os.Stdout.WriteString(`{"conformant":true,"effective_version":"0.2","file":"example.md"}`)
		os.Exit(1)
	}
	os.Exit(m.Run())
}
