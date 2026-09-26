package graph

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInstantProjectionUsesDateTimeDatatypeAndExactBoundary(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeGraphFile(t, root, "a.md", "---\ntype: Note\n"+
		"stale_after: 2026-09-23T18:00:00+07:00\n"+
		"sources:\n  - id: s1\n    resource: https://example.test/source\n"+
		"    last_modified: 2026-09-23T19:00:00+07:00\n"+
		"usage_window: {from: 2026-09-23T18:00:00+07:00, to: 2026-09-23T12:00:00Z}\n"+
		"---\nBody.\n")
	b := loadGraphBundle(t, root)
	before := time.Date(2026, 9, 23, 10, 59, 59, 999999999, time.UTC)
	at := before.Add(time.Nanosecond)

	// Act.
	var beforeOut, atOut strings.Builder
	beforeErr := RenderNTriplesWithOptions(&beforeOut, b, Options{Profile: ProjectionProfileToolkitV02Instant, AsOf: &before})
	atErr := RenderNTriplesWithOptions(&atOut, b, Options{Profile: ProjectionProfileToolkitV02Instant, AsOf: &at})

	// Assert.
	if beforeErr != nil || atErr != nil {
		t.Fatalf("render errors = %v, %v", beforeErr, atErr)
	}
	for _, field := range []string{"staleAfter", "stalenessAsOf", "lastModified", "usageFrom", "usageTo"} {
		if !strings.Contains(atOut.String(), "#"+field+">") {
			t.Fatalf("missing %s: %s", field, atOut.String())
		}
	}
	if strings.Count(atOut.String(), "XMLSchema#dateTime") < 5 {
		t.Fatalf("dateTime datatype missing: %s", atOut.String())
	}
	if strings.Contains(atOut.String(), "XMLSchema#date>\n") {
		t.Fatalf("date datatype leaked: %s", atOut.String())
	}
	if !strings.Contains(beforeOut.String(), `#stale> "false"^^`) || !strings.Contains(atOut.String(), `#stale> "true"^^`) {
		t.Fatalf("staleness boundary: before=%s at=%s", beforeOut.String(), atOut.String())
	}
	if !strings.Contains(atOut.String(), `"skosovsky/okf-v0.2-instant"`) {
		t.Fatalf("profile missing: %s", atOut.String())
	}
}

func TestInstantProjectionContractPinsTemporalDatatypes(t *testing.T) {
	// Arrange.
	data, err := os.ReadFile(filepath.Join("contracts", "v0.2-instant.json"))
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Profile string `json:"profile"`
		Classes map[string]struct {
			Predicates map[string]struct {
				Datatype string `json:"datatype"`
			} `json:"predicates"`
		} `json:"classes"`
	}
	// Act.
	err = json.Unmarshal(data, &contract)
	// Assert.
	if err != nil || contract.Profile != string(ProjectionProfileToolkitV02Instant) {
		t.Fatalf("contract profile: %#v %v", contract.Profile, err)
	}
	for class, predicates := range map[string][]string{"Concept": {"staleAfter", "stalenessAsOf"}, "ProvenanceSource": {"lastModified", "usageFrom", "usageTo"}} {
		for _, predicate := range predicates {
			if got := contract.Classes[class].Predicates[predicate].Datatype; got != "http://www.w3.org/2001/XMLSchema#dateTime" {
				t.Fatalf("%s.%s datatype = %q", class, predicate, got)
			}
		}
	}
}

func TestInstantSourceOrderingDoesNotDependOnYAMLSequence(t *testing.T) {
	// Arrange: temporal fields are the only distinction between sources.
	entries := []string{
		"  - {resource: policy.md, last_modified: 2026-09-23T12:00:00Z}\n",
		"  - {resource: policy.md, last_modified: 2026-09-23T18:00:00+07:00}\n",
	}
	var rendered []string
	for _, order := range [][]int{{0, 1}, {1, 0}} {
		root := t.TempDir()
		body := "---\ntype: Note\nsources:\n" + entries[order[0]] + entries[order[1]] + "---\nBody.\n"
		writeGraphFile(t, root, "a.md", body)
		b := loadGraphBundle(t, root)
		// Act.
		var out strings.Builder
		if err := RenderNTriplesWithOptions(&out, b, Options{Profile: ProjectionProfileToolkitV02Instant}); err != nil {
			t.Fatal(err)
		}
		rendered = append(rendered, out.String())
	}
	// Assert.
	if rendered[0] != rendered[1] {
		t.Fatalf("source permutation changed graph:\nfirst=%s\nsecond=%s", rendered[0], rendered[1])
	}
}
