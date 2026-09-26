package main

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/skosovsky/okf/benchmark/agent"
)

func TestBackfillConsumerBundleUsesGoCLI(t *testing.T) {
	// Arrange
	m, err := os.ReadFile("../../../../backfill/testdata/reversal-events.json")
	if err != nil {
		t.Fatal(err)
	}
	a, err := os.ReadFile("../../../../backfill/testdata/reversal-analysis.json")
	if err != nil {
		t.Fatal(err)
	}
	cases, _, err := agent.BuildReversalFixtureCorpus(context.Background(), m, a)
	if err != nil {
		t.Fatal(err)
	}
	// Act and assert
	for _, c := range cases {
		if c.ID == "backfill-current-code" {
			// This consumer case deliberately includes the same raw Go diff in
			// both arms and is run in toolkit-mode none. Go CLI treatment
			// projections require a bundle containing only OKF files.
			continue
		}
		if _, calls, err := exposeGoCLI(c.Treatment); err != nil {
			t.Fatalf("%s: %v", c.ID, err)
		} else if calls != 3 {
			t.Fatalf("%s: calls=%d", c.ID, calls)
		}
	}
}

func TestPrecomputedCorpusOnlyClarifiesDelegationQuestion(t *testing.T) {
	// Arrange: historical plans pin the original primary corpus hash.
	load := func(path string) []agent.Case {
		t.Helper()
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		cases, _, err := agent.LoadCorpus(f)
		if err != nil {
			t.Fatal(err)
		}
		return cases
	}
	old := load("../../corpus/primary_realistic.json")
	newCases := load("../../corpus/primary_precomputed.json")

	// Act and assert: a new protocol may refine the question without silently
	// changing source evidence, expected answers, or historical inputs.
	if len(old) != len(newCases) {
		t.Fatalf("case count changed: %d vs %d", len(old), len(newCases))
	}
	changed := 0
	for i := range old {
		if old[i].Question != newCases[i].Question {
			if old[i].ID != "repo-cli-delegation" || !strings.Contains(newCases[i].Question, "local run wrapper delegate") {
				t.Fatalf("unexpected question change in %s", old[i].ID)
			}
			changed++
		}
		newCases[i].Question = old[i].Question
		if !reflect.DeepEqual(old[i], newCases[i]) {
			t.Fatalf("%s changed beyond its question", old[i].ID)
		}
	}
	if changed != 1 {
		t.Fatalf("changed %d questions, want one", changed)
	}
}

func TestPrimaryPrecomputedCitationContract(t *testing.T) {
	// Arrange: realistic cases expose the answer in both source and OKF arms.
	f, err := os.Open("../../corpus/primary_precomputed.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cases, _, err := agent.LoadCorpus(f)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(instructions, "cite its source current, not cli-current") {
		t.Fatal("shared prompt does not specify original-artifact citation")
	}

	for _, c := range cases {
		// Act: run the real in-process Go CLI used by the proposed pilot.
		artifacts, calls, err := exposeGoCLI(c.Treatment)
		if err != nil {
			t.Fatalf("%s: %v", c.ID, err)
		}
		byID := make(map[string]agent.Artifact, len(artifacts))
		for _, a := range artifacts {
			byID[a.ID] = a
		}

		// Assert: the projection points back to the original supported concept,
		// and the frozen grader accepts only the original citation.
		if calls < 2 || byID["cli-validation"].Content == "" {
			t.Fatalf("%s: missing CLI validation/parse calls", c.ID)
		}
		for _, support := range c.Expected.Support {
			original := byID[support.ArtifactID]
			projection := byID["cli-"+support.ArtifactID]
			if original.Path == "" || projection.Content == "" {
				t.Fatalf("%s: projection missing source %s", c.ID, support.ArtifactID)
			}
			var parsed struct {
				File string `json:"file"`
			}
			if err := json.Unmarshal([]byte(projection.Content), &parsed); err != nil || parsed.File != original.Path {
				t.Fatalf("%s: projection provenance %q, want %q: %v", c.ID, parsed.File, original.Path, err)
			}
			if got := agent.Grade(c, agent.Observation{Answer: c.Expected.Answer, Evidence: []string{projection.ID}}, ""); got != agent.Ungradable {
				t.Fatalf("%s: derived-only citation graded %s", c.ID, got)
			}
			if got := agent.Grade(c, agent.Observation{Answer: c.Expected.Answer, Evidence: []string{original.ID}}, ""); got != agent.Correct {
				t.Fatalf("%s: original citation graded %s", c.ID, got)
			}
		}
	}
}

func TestTreatmentUsesGoCLI(t *testing.T) {
	// Arrange
	f, err := os.Open("../../corpus/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cases, _, err := agent.LoadCorpus(f)
	if err != nil {
		t.Fatal(err)
	}
	// Act
	artifacts, calls, err := exposeGoCLI(cases[0].Treatment)
	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 || len(artifacts) != 6 {
		t.Fatalf("calls=%d artifacts=%d", calls, len(artifacts))
	}
	if artifacts[3].ID != "cli-validation" || artifacts[5].ID != "cli-current" {
		t.Fatalf("unexpected CLI evidence: %+v", artifacts)
	}
}
