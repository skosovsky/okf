package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/skosovsky/okf/backfill"
	"github.com/skosovsky/okf/bundle"
	storefs "github.com/skosovsky/okf/store/fs"
	"github.com/skosovsky/okf/validator"
)

// BuildReversalFixtureCorpus commits reviewed draft knowledge through the real Go
// mutation/store path, then freezes consumer questions from the resulting
// bundle. The returned coverage is separate from semantic answer grading.
func BuildReversalFixtureCorpus(ctx context.Context, manifestJSON, analysisJSON []byte) ([]Case, backfill.Coverage, error) {
	var m backfill.Manifest
	var a backfill.Analysis
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		return nil, backfill.Coverage{}, err
	}
	if err := json.Unmarshal(analysisJSON, &a); err != nil {
		return nil, backfill.Coverage{}, err
	}
	root, err := os.MkdirTemp("", "okf-eval-backfill-*")
	if err != nil {
		return nil, backfill.Coverage{}, err
	}
	defer os.RemoveAll(root)
	if err := os.WriteFile(filepath.Join(root, "index.md"), []byte("---\nokf_version: \"0.2\"\n---\n\n# Concepts\n"), 0600); err != nil {
		return nil, backfill.Coverage{}, err
	}
	s, err := storefs.Open(root, storefs.Config{ValidatorConfig: &validator.ValidatorConfig{TemporalProfile: bundle.TemporalProfileInstant, CheckLinks: true, CheckOrphans: true}})
	if err != nil {
		return nil, backfill.Coverage{}, err
	}
	defer s.Close()
	plan, err := backfill.Prepare(ctx, s, m, a)
	if err != nil {
		return nil, backfill.Coverage{}, err
	}
	checkpointDir, err := os.MkdirTemp("", "okf-eval-checkpoint-*")
	if err != nil {
		return nil, plan.Coverage, err
	}
	defer os.RemoveAll(checkpointDir)
	if _, err := backfill.Apply(ctx, s, plan, m, a, filepath.Join(checkpointDir, "checkpoint.json")); err != nil {
		return nil, plan.Coverage, err
	}
	files := map[string]string{}
	for _, name := range []string{"index.md", "decision.md", "handler-status.md"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			return nil, plan.Coverage, err
		}
		files[name] = string(data)
	}
	var oldDiff, currentDiff, handlerDiff string
	for _, event := range m.Events {
		switch event.Subject {
		case "Introduce mode A":
			oldDiff = event.Diff
		case "Replace A with B":
			currentDiff = event.Diff
		case "Refactor handler":
			handlerDiff = event.Diff
		}
	}
	if oldDiff == "" || currentDiff == "" || handlerDiff == "" {
		return nil, plan.Coverage, fmt.Errorf("backfill fixture lacks A→B and truncated handler events")
	}
	treatment := []Artifact{{ID: "bundle-index", Path: "index.md", Content: files["index.md"]}, {ID: "decision", Path: "decision.md", Content: files["decision.md"]}, {ID: "handler", Path: "handler-status.md", Content: files["handler-status.md"]}}
	decisionControl := []Artifact{{ID: "historical", Path: "git/old.diff", Content: oldDiff}, {ID: "decision", Path: "git/current.diff", Content: currentDiff}}
	handlerControl := []Artifact{{ID: "handler", Path: "git/handler-diff.txt", Content: "Captured diff is partial; rollout status cannot be established.\n" + handlerDiff}}
	// The current-code question must expose the same captured Go diff to both
	// consumers. Otherwise it measures missing evidence rather than whether the
	// reconstructed bundle helps a consumer use current source evidence.
	currentCode := Artifact{ID: "current-code", Path: "git/current.diff", Content: currentDiff}
	currentCodeTreatment := append(append([]Artifact(nil), treatment...), currentCode)
	currentCodeControl := []Artifact{{ID: "historical", Path: "git/old.diff", Content: oldDiff}, currentCode}
	cases := []Case{
		{ID: "backfill-reversal", Category: "reversal", Tier: "mechanism_only", Question: "Which mode is the current decision?", Control: decisionControl, Treatment: treatment, Expected: Expected{Answer: "B", Evidence: []string{"decision"}, Support: []SupportQuote{{ArtifactID: "decision", ControlQuote: "+const Mode = \"B\"", TreatmentQuote: "mode B"}}, StaleAnswers: []string{"A"}}},
		{ID: "backfill-truncated", Category: "truncated_diff", Tier: "mechanism_only", Question: "Does the captured evidence prove that the handler rollout completed?", Control: handlerControl, Treatment: treatment, Expected: Expected{Answer: "Insufficient evidence", Evidence: []string{"handler"}, Support: []SupportQuote{{ArtifactID: "handler", ControlQuote: "Captured diff is partial", TreatmentQuote: "Evidence is incomplete"}}, StaleAnswers: []string{"Yes"}}},
		{ID: "backfill-current-code", Category: "stale_vs_code", Tier: "mechanism_only", Question: "What value does Mode have in the captured current Go diff?", Control: currentCodeControl, Treatment: currentCodeTreatment, Expected: Expected{Answer: "B", Evidence: []string{"current-code"}, Support: []SupportQuote{{ArtifactID: "current-code", Quote: "+const Mode = \"B\""}}, StaleAnswers: []string{"A"}}},
	}
	if err := ValidateCases(cases); err != nil {
		return nil, plan.Coverage, err
	}
	return cases, plan.Coverage, nil
}
