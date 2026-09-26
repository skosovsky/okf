package main

import (
	"context"
	"os"
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
		if _, calls, err := exposeGoCLI(c.Treatment); err != nil {
			t.Fatalf("%s: %v", c.ID, err)
		} else if calls != 3 {
			t.Fatalf("%s: calls=%d", c.ID, calls)
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
