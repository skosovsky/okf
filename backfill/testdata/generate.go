//go:build ignore

// Run with: go run ./backfill/testdata/generate.go
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/skosovsky/okf/backfill"
)

func hash(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func write(name string, v any) {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		panic(e)
	}
	if e := os.WriteFile(filepath.Join("backfill", "testdata", name), append(b, '\n'), 0o644); e != nil {
		panic(e)
	}
}
func main() {
	a, b, c := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	diffA := "diff --git a/decision.go b/decision.go\n+const Mode = \"A\"\n"
	diffB := "diff --git a/decision.go b/decision.go\n-const Mode = \"A\"\n+const Mode = \"B\"\n"
	diffC := "diff --git a/handler.go b/handler.go\n@@ -1,100 +1,100 @@\n"
	diffC += strings.Repeat("+", 128-len(diffC))
	events := []backfill.Event{
		{ID: "git:" + a, Commit: a, Parents: []string{}, Subject: "Introduce mode A", Body: "Initial design.", Author: "Example <example@example.invalid>", AuthorTime: "2026-01-01T00:00:00Z", CommitterTime: "2026-01-01T00:00:00Z", Files: []backfill.File{{Path: "decision.go", Status: "A", Binary: false}}, Diff: diffA, DiffSHA256: hash([]byte(diffA)), DiffBytes: int64(len(diffA))},
		{ID: "git:" + b, Commit: b, Parents: []string{a}, Subject: "Replace A with B", Body: "Decision reversed after test.", Author: "Example <example@example.invalid>", AuthorTime: "2026-01-02T00:00:00Z", CommitterTime: "2026-01-02T00:00:00Z", Files: []backfill.File{{Path: "decision.go", Status: "M", Binary: false}}, Diff: diffB, DiffSHA256: hash([]byte(diffB)), DiffBytes: int64(len(diffB))},
		{ID: "git:" + c, Commit: c, Parents: []string{b}, Subject: "Refactor handler", Body: "Intent says rollout complete, but captured diff is partial.", Author: "Example <example@example.invalid>", AuthorTime: "2026-01-03T00:00:00Z", CommitterTime: "2026-01-03T00:00:00Z", Files: []backfill.File{{Path: "handler.go", Status: "M", Binary: false}}, Diff: diffC, DiffSHA256: hash([]byte(diffC + strings.Repeat("unseen", 100))), DiffBytes: int64(len(diffC) + 600), DiffTruncated: true},
	}
	cfg := backfill.Config{Repository: "/synthetic/okf-backfill", Base: strings.Repeat("0", 40), Head: c, FirstParent: true, MaxDiffBytes: 128, SkipPaths: []string{}}
	cb, _ := json.Marshal(cfg)
	eb, _ := json.Marshal(events)
	m := backfill.Manifest{SchemaVersion: backfill.SchemaVersion, ExtractorVersion: backfill.ExtractorVersion, Config: cfg, Repository: cfg.Repository, Base: cfg.Base, Head: cfg.Head, ConfigSHA256: hash(cb), EventsSHA256: hash(eb), Events: events}
	aOut := backfill.Analysis{SchemaVersion: backfill.SchemaVersion, EventsSHA256: m.EventsSHA256, PromptVersion: "fixture/v1", ProducedAt: "2026-01-04T00:00:00Z", Results: []backfill.EventResult{{EventID: events[0].ID, Decision: backfill.DecisionConsidered, CandidateIDs: []string{"mode-A"}}, {EventID: events[1].ID, Decision: backfill.DecisionConsidered, CandidateIDs: []string{"mode-B"}}, {EventID: events[2].ID, Decision: backfill.DecisionConsidered, CandidateIDs: []string{"handler-uncertain"}}}, Candidates: []backfill.Candidate{
		{ID: "mode-A", ConceptID: "decision", Title: "Mode decision", Description: "Current mode decision.", Body: "The implementation uses mode A.", Evidence: []backfill.EvidenceRef{{EventID: events[0].ID, Path: "decision.go", DiffSHA256: events[0].DiffSHA256}}},
		{ID: "mode-B", ConceptID: "decision", Title: "Mode decision", Description: "Current mode decision.", Body: "The current decision and captured implementation use mode B.", History: []backfill.HistoryNote{{EventID: events[1].ID, Text: "Mode A was reversed and replaced by B"}}, Evidence: []backfill.EvidenceRef{{EventID: events[1].ID, Path: "decision.go", DiffSHA256: events[1].DiffSHA256}}, Supersedes: []string{"mode-A"}},
		{ID: "handler-uncertain", ConceptID: "handler-status", Title: "Handler rollout evidence", Description: "Partial evidence for the handler rollout.", Body: "The captured handler diff is incomplete; rollout status is unverified.", Evidence: []backfill.EvidenceRef{{EventID: events[2].ID, Path: "handler.go", DiffSHA256: events[2].DiffSHA256}}, EvidenceIncomplete: true},
	}}
	if _, _, e := backfill.ValidateAnalysis(m, aOut); e != nil {
		panic(e)
	}
	write("reversal-events.json", m)
	write("reversal-analysis.json", aOut)
}
