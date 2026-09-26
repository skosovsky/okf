package main

import (
	"reflect"
	"testing"
)

func TestAnalyzerIncludesCorrectedOnlyAndKeepsUnavailableValues(t *testing.T) {
	// Arrange.
	base := map[string][]sample{"shared": {{ns: 10, bytes: 20, allocs: 2}}}
	corrected := map[string][]sample{"shared": {{ns: 12, bytes: 24, allocs: 3}}, "new": {{ns: 15, bytes: 30, allocs: 4}}}
	// Act.
	names := workloadNames(base, corrected)
	missing := summarize(base["new"])
	change := delta(10, 12, true)
	missingChange := delta(0, 15, false)
	// Assert.
	if !reflect.DeepEqual(names, []string{"new", "shared"}) {
		t.Fatalf("names = %v", names)
	}
	if missing.n != "N/A" || missing.bytesText != "N/A" || missing.allocsText != "N/A" || missingChange != "N/A" {
		t.Fatalf("missing row = %+v, delta = %s", missing, missingChange)
	}
	if change != "+20.0%" {
		t.Fatalf("delta = %s", change)
	}
}

func TestAnalyzerComputesMedianAndMADForAllMeasures(t *testing.T) {
	// Arrange.
	rows := []sample{{10, 100, 1}, {11, 101, 2}, {12, 102, 3}, {14, 104, 4}, {99, 999, 9}}
	// Act.
	got := summarize(rows)
	// Assert.
	if got.nsText != "12 ± 2" || got.bytesText != "102 ± 2" || got.allocsText != "3 ± 1" {
		t.Fatalf("summary = %+v", got)
	}
}
