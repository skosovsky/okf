package upkeeplive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skosovsky/okf/benchmark/agent"
)

type fixtureAdapter struct {
	seen  []Request
	count int
}

func (a *fixtureAdapter) Call(_ context.Context, req Request) (Response, error) {
	a.count++
	a.seen = append(a.seen, req)
	r := Response{SessionID: "fixture-" + string(rune('a'+a.count)), InputTokens: 10, OutputTokens: 5}
	switch req.Role {
	case "writer":
		r.Draft = &Draft{FinalCode: append([]agent.Artifact(nil), req.Writer.TargetCode...), FinalBundle: append([]agent.Artifact(nil), req.Writer.InitialBundle...)}
	case "writer_revision":
		if req.CheckerInitial == nil || req.CheckerInitial.Status != "needs_review" {
			r.Failure = "checker absent"
			return r, nil
		}
		r.Draft = &Draft{FinalCode: append([]agent.Artifact(nil), req.Draft.FinalCode...), FinalBundle: append([]agent.Artifact(nil), req.Draft.FinalBundle...)}
		for i := range r.Draft.FinalBundle {
			artifact := &r.Draft.FinalBundle[i]
			switch artifact.ID {
			case "decision":
				artifact.Content = strings.Replace(artifact.Content, "mode A", "mode B", 1)
			case "handler":
				artifact.Content = strings.Replace(artifact.Content, "The complete captured evidence indicates that the handler rollout finished.", "Evidence is incomplete; rollout completion is unverified.", 1)
			}
		}
	case "consumer":
		answer, evidence := "A", "decision"
		if strings.Contains(req.Consumer.Question, "handler") {
			answer, evidence = "Yes", "handler"
		}
		for _, artifact := range req.Consumer.Artifacts {
			if artifact.ID == "decision" && strings.Contains(artifact.Content, "mode B") {
				answer, evidence = "B", "decision"
			}
			if artifact.ID == "handler" && strings.Contains(artifact.Content, "Evidence is incomplete") && strings.Contains(req.Consumer.Question, "handler") {
				answer, evidence = "Insufficient evidence", "handler"
			}
		}
		r.Observation = &agent.Observation{Answer: answer, Evidence: []string{evidence}}
	default:
		r.Failure = "unknown role"
	}
	return r, nil
}

func TestRunFrozenUpkeepProtocol(t *testing.T) {
	// Arrange: derive the frozen cases; no model endpoint is contacted.
	b, err := os.ReadFile("../corpus/backfill_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	cases, sourceHash, err := agent.LoadCorpus(strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte("fixed prompts and checker"))
	pin := hex.EncodeToString(h[:])
	plan, err := agent.BuildUpkeepStudyFromBackfill(cases, sourceHash, "fixture", 1, "fixture-writer", "fixture-consumer", pin, pin, pin)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &fixtureAdapter{}
	// Act.
	got, err := Run(context.Background(), plan, adapter, Limits{MaxCalls: 12, MaxSeconds: 30, MaxTokens: 1000, CallSeconds: 5})
	if err != nil {
		t.Fatal(err)
	}
	// Assert: two arms × two cases, real checker/CLI, independent consumer.
	if got.Calls != 10 || len(got.Rows.Writers) != 4 || len(got.Rows.Consumers) != 4 {
		t.Fatalf("calls=%d writers=%d consumers=%d events=%+v", got.Calls, len(got.Rows.Writers), len(got.Rows.Consumers), got.Events)
	}
	report, err := agent.AnalyzeUpkeepStudy(plan, got.Rows)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Valid {
		t.Fatalf("invalid report: %+v", report.Reasons)
	}
	if report.Arms[agent.UpkeepCheckerOn].Correct != 2 || report.Arms[agent.UpkeepCheckerOff].Correct != 0 {
		t.Fatalf("quality: %+v", report.Arms)
	}
	for _, req := range adapter.seen {
		wire, _ := json.Marshal(req)
		if strings.Contains(string(wire), "expected") || strings.Contains(string(wire), "stale_answers") || strings.Contains(string(wire), "checker_off") || strings.Contains(string(wire), "checker_on") {
			t.Fatalf("role leakage: %s", wire)
		}
		if req.Role == "consumer" && (req.Writer != nil || req.CheckerInitial != nil || req.Reminder != "" || req.Draft != nil) {
			t.Fatalf("consumer leakage: %+v", req)
		}
	}
}

func TestRunStopsAtCallCapWithoutInventingRows(t *testing.T) {
	b, err := os.ReadFile("../corpus/backfill_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	cases, sourceHash, err := agent.LoadCorpus(strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte("pin"))
	pin := hex.EncodeToString(h[:])
	plan, err := agent.BuildUpkeepStudyFromBackfill(cases, sourceHash, "cap", 1, "w", "c", pin, pin, pin)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Run(context.Background(), plan, &fixtureAdapter{}, Limits{MaxCalls: 1, MaxSeconds: 30, MaxTokens: 1000, CallSeconds: 5})
	if err != nil {
		t.Fatal(err)
	}
	if got.Calls != 1 || got.Stopped == "" {
		t.Fatalf("cap not enforced: %+v", got)
	}
	report, err := agent.AnalyzeUpkeepStudy(plan, got.Rows)
	if err != nil {
		t.Fatal(err)
	}
	if report.Valid {
		t.Fatal("incomplete run marked valid")
	}
}

func TestCheckerPreflightRejectsInertTreatmentBeforeModel(t *testing.T) {
	// Arrange: a syntactically valid plan whose target does not change code.
	b, err := os.ReadFile("../corpus/backfill_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	cases, sourceHash, err := agent.LoadCorpus(strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte("pin"))
	pin := hex.EncodeToString(h[:])
	plan, err := agent.BuildUpkeepStudyFromBackfill(cases, sourceHash, "inert", 1, "w", "c", pin, pin, pin)
	if err != nil {
		t.Fatal(err)
	}
	plan.Cases[0].TargetCode = append([]agent.Artifact(nil), plan.Cases[0].InitialCode...)
	adapter := &fixtureAdapter{}
	// Act.
	_, err = Run(context.Background(), plan, adapter, Limits{MaxCalls: 12, MaxSeconds: 30, MaxTokens: 1000, CallSeconds: 5})
	// Assert: no paid call can be made for an inert checker treatment.
	if err == nil || !strings.Contains(err.Error(), "checker preflight") || adapter.count != 0 {
		t.Fatalf("err=%v adapter calls=%d", err, adapter.count)
	}
}

func TestWriteDraftRemovesOmittedRootFiles(t *testing.T) {
	// Arrange: the first writer draft adds a second root file.
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	c := agent.UpkeepCase{InitialCode: []agent.Artifact{{ID: "code", Path: "decision.go", Content: "A"}}}
	first := Draft{FinalCode: []agent.Artifact{{ID: "code", Path: "decision.go", Content: "B"}, {ID: "extra", Path: "nested/extra.go", Content: "stale"}}, FinalBundle: []agent.Artifact{{ID: "index", Path: "index.md", Content: "index"}}}
	second := Draft{FinalCode: []agent.Artifact{{ID: "code", Path: "decision.go", Content: "B"}}, FinalBundle: first.FinalBundle}
	// Act.
	if err := writeDraft(root, c, first); err != nil {
		t.Fatal(err)
	}
	if err := writeDraft(root, c, second); err != nil {
		t.Fatal(err)
	}
	// Assert: checkout bytes match only the second declared inventory.
	if _, err := os.Stat(filepath.Join(root, "nested", "extra.go")); !os.IsNotExist(err) {
		t.Fatalf("stale file remains: %v", err)
	}
	if err := verifyCodeCheckout(root, second.FinalCode); err != nil {
		t.Fatal(err)
	}
}

type failingAdapter struct{ calls int }

func (a *failingAdapter) Call(context.Context, Request) (Response, error) {
	a.calls++
	return Response{}, errors.New("adapter timeout")
}

type consumerTransportFailure struct{ fixtureAdapter }

func (a *consumerTransportFailure) Call(ctx context.Context, req Request) (Response, error) {
	if req.Role == "consumer" {
		return Response{}, errors.New("consumer transport timeout")
	}
	return a.fixtureAdapter.Call(ctx, req)
}

func TestConsumerTransportFailureProducesAnalyzableRowAndStops(t *testing.T) {
	b, err := os.ReadFile("../corpus/backfill_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	cases, sourceHash, err := agent.LoadCorpus(strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte("pin"))
	pin := hex.EncodeToString(h[:])
	plan, err := agent.BuildUpkeepStudyFromBackfill(cases, sourceHash, "consumer-failure", 1, "w", "c", pin, pin, pin)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Run(context.Background(), plan, &consumerTransportFailure{}, Limits{MaxCalls: 12, MaxSeconds: 30, MaxTokens: 1000, CallSeconds: 5})
	if err != nil {
		t.Fatal(err)
	}
	if got.UnknownUsageCalls != 1 || got.Stopped == "" || len(got.Rows.Writers) != 1 || len(got.Rows.Consumers) != 1 {
		t.Fatalf("result=%+v", got)
	}
	consumer := got.Rows.Consumers[0]
	if consumer.ConsumerSessionID != "" || consumer.Failure == "" || consumer.VisibleSHA256 == "" {
		t.Fatalf("consumer=%+v", consumer)
	}
	report, err := agent.AnalyzeUpkeepStudy(plan, got.Rows)
	if err != nil || report.Valid || report.Arms[consumer.Arm].Failures != 1 || report.Arms[consumer.Arm].ConsumerObserved != 1 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestCommandAdapterBoundsOutput(t *testing.T) {
	// Arrange: a trusted test executable emits more than the wire cap.
	path := filepath.Join(t.TempDir(), "flood.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nyes x | head -c 5000000\n"), 0700); err != nil {
		t.Fatal(err)
	}
	// Act.
	_, err := (CommandAdapter{Path: path}).Call(context.Background(), Request{Role: "writer"})
	// Assert.
	if err == nil || !strings.Contains(err.Error(), "exceeds 4 MiB") {
		t.Fatalf("error=%v", err)
	}
}

func TestAdapterFailureProducesAnalyzableWriterRow(t *testing.T) {
	b, err := os.ReadFile("../corpus/backfill_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	cases, sourceHash, err := agent.LoadCorpus(strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte("pin"))
	pin := hex.EncodeToString(h[:])
	plan, err := agent.BuildUpkeepStudyFromBackfill(cases, sourceHash, "failure", 1, "w", "c", pin, pin, pin)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &failingAdapter{}
	got, err := Run(context.Background(), plan, adapter, Limits{MaxCalls: 4, MaxSeconds: 30, MaxTokens: 1000, CallSeconds: 5})
	if err != nil {
		t.Fatal(err)
	}
	if got.UnknownUsageCalls != 1 || len(got.Rows.Writers) != 1 || got.Calls != 1 || got.Stopped == "" {
		t.Fatalf("result=%+v", got)
	}
	report, err := agent.AnalyzeUpkeepStudy(plan, got.Rows)
	if err != nil || report.Valid || report.Arms[agent.UpkeepCheckerOff].Expected != 2 || report.Arms[agent.UpkeepCheckerOn].Expected != 2 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}
