package agent

import (
	"encoding/json"
	"testing"
	"time"
)

func TestPrecomputedToolkitAccountingSeparatesRunnerAndModelCalls(t *testing.T) {
	// Arrange: one treatment observation has a runner-produced projection but
	// the model itself made no tool call.
	cases, hash := fixture(t)
	meta := Metadata{RunID: "precomputed", CorpusSHA256: hash, PromptSHA256: hash, Commit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SpecRevision: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Model: "model", ModelVersion: "model", Settings: `{"reasoning_effort":"low"}`, Adapter: "adapter", Clock: time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC), GraderRevision: "exact-v1"}
	plan := Plan{Metadata: meta, SpecSHA256: hash, AdapterSHA256: hash, ToolCallAccounting: "separate-v1", ToolkitMode: "treatment", ModelToolAccess: "fixed", Repeats: 1, CaseCount: len(cases), MaxTrials: len(cases) * 2, MaxSeconds: 60, MaxTokens: 1000, Unpriced: true}
	row := Row{Metadata: meta, CaseID: cases[0].ID, Arm: Treatment, Repeat: 1, Observation: Observation{Answer: cases[0].Expected.Answer, Evidence: cases[0].Expected.Evidence, InputTokens: 10, OutputTokens: 2}, ToolkitEvidence: []Artifact{{ID: "cli-current", Path: "tool/parse-current.json", Content: `{"file":"current.md"}`}}, RunnerToolkitCalls: 1, Verdict: Correct}

	// Act.
	report, err := AnalyzePlanned(cases, hash, []Row{row}, plan)
	if err != nil {
		t.Fatal(err)
	}

	// Assert: the model and runner counters cannot be confused in new reports.
	arm := report.Arms[Treatment]
	if report.ToolCallAccounting != "separate-v1" || arm.ToolCalls != 0 || arm.ModelToolkitCalls != 0 || arm.RunnerToolkitCalls != 1 {
		t.Fatalf("mixed tool call provenance: %+v %+v", report, arm)
	}
	for name, value := range map[string]any{"plan": plan, "row": row, "report": report} {
		wire, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateWire(name, wire); err != nil {
			t.Fatalf("%s does not match schema: %v", name, err)
		}
	}
	row.RunnerToolkitCalls = 0
	if _, err := AnalyzePlanned(cases, hash, []Row{row}, plan); err == nil {
		t.Fatal("accepted projection without a runner call")
	}
	row.RunnerToolkitCalls = 1
	row.Observation.ToolkitCalls = 1
	if _, err := AnalyzePlanned(cases, hash, []Row{row}, plan); err == nil {
		t.Fatal("accepted runner call misreported as a model CLI call")
	}
}

func TestLegacyToolkitRowsRemainAnalyzable(t *testing.T) {
	// Arrange: old plans counted precomputed calls in Observation.ToolCalls.
	cases, hash := fixture(t)
	meta := Metadata{RunID: "legacy", CorpusSHA256: hash, PromptSHA256: hash, Commit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SpecRevision: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Model: "model", ModelVersion: "model", Settings: "legacy", Adapter: "adapter", Clock: time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC), GraderRevision: "exact-v1"}
	plan := Plan{Metadata: meta, SpecSHA256: hash, AdapterSHA256: hash, ToolkitMode: "treatment", ModelToolAccess: "fixed", Repeats: 1, CaseCount: len(cases), MaxTrials: len(cases) * 2, MaxSeconds: 60, MaxTokens: 1000, Unpriced: true}
	row := Row{Metadata: meta, CaseID: cases[0].ID, Arm: Treatment, Repeat: 1, Observation: Observation{Answer: cases[0].Expected.Answer, Evidence: cases[0].Expected.Evidence, ToolCalls: 1}, ToolkitEvidence: []Artifact{{ID: "cli-current", Path: "tool/parse-current.json", Content: `{}`}}, Verdict: Correct}

	// Act and assert: analysis retains historical accounting without relabeling it.
	report, err := AnalyzePlanned(cases, hash, []Row{row}, plan)
	if err != nil {
		t.Fatal(err)
	}
	if report.ToolCallAccounting != "" || report.Arms[Treatment].ToolCalls != 1 || report.Arms[Treatment].RunnerToolkitCalls != 0 {
		t.Fatalf("legacy report changed provenance: %+v", report.Arms[Treatment])
	}
}
