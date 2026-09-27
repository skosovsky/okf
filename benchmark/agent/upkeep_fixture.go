package agent

import (
	"errors"
	"fmt"
	"strings"
)

// BuildUpkeepStudyFromBackfill deterministically derives two task-013 starting
// scenarios from the committed task-010 reversal fixture. It produces a plan,
// not observations. Model and prompt provenance must be pinned by the caller.
func BuildUpkeepStudyFromBackfill(source []Case, sourceSHA256, id string, repeats int, writerModel, consumerModel, writerPromptSHA256, consumerPromptSHA256, checkerSHA256 string) (UpkeepStudy, error) {
	byID := map[string]Case{}
	for _, c := range source {
		byID[c.ID] = c
	}
	reversal, ok := byID["backfill-reversal"]
	if !ok || reversal.Expected.Answer != "B" {
		return UpkeepStudy{}, errors.New("missing or changed backfill-reversal fixture")
	}
	truncated, ok := byID["backfill-truncated"]
	if !ok || truncated.Expected.Answer != "Insufficient evidence" {
		return UpkeepStudy{}, errors.New("missing or changed backfill-truncated fixture")
	}
	index, ok := upkeepFindArtifact(reversal.Treatment, "bundle-index")
	if !ok {
		return UpkeepStudy{}, errors.New("reversal fixture lacks bundle index")
	}
	if !strings.Contains(index.Content, "Partial evidence for the handler rollout.") {
		return UpkeepStudy{}, errors.New("reversal fixture index description changed")
	}
	index.Content = strings.Replace(index.Content, "Partial evidence for the handler rollout.", "Current handler rollout evidence.", 1)
	currentDiff, ok := upkeepFindArtifact(reversal.Control, "decision")
	if !ok || !strings.Contains(currentDiff.Content, `+const Mode = "B"`) {
		return UpkeepStudy{}, errors.New("reversal fixture lacks current decision diff")
	}
	partial, ok := upkeepFindArtifact(truncated.Control, "handler")
	if !ok || !strings.Contains(partial.Content, "Captured diff is partial") {
		return UpkeepStudy{}, errors.New("truncated fixture lacks partial diff")
	}
	decisionDoc := Artifact{ID: "decision", Path: "decision.md", Content: upkeepInitialDecisionDoc}
	handlerDoc := Artifact{ID: "handler", Path: "handler-status.md", Content: upkeepInitialHandlerDoc}
	initialBundle := []Artifact{index, decisionDoc, handlerDoc}
	initialDecision := []Artifact{{ID: "decision-code", Path: "decision.go", Content: `const Mode = "A"` + "\n"}}
	targetDecision := []Artifact{{ID: "decision-code", Path: "decision.go", Content: `const Mode = "B"` + "\n"}}
	initialHandler := []Artifact{{ID: "handler-capture", Path: "handler-capture.txt", Content: "Captured diff is complete; rollout was completed.\n"}}
	targetHandler := []Artifact{{ID: "handler-capture", Path: "handler-capture.txt", Content: partial.Content}}
	plan := UpkeepStudy{ID: id, SourceCorpusSHA256: sourceSHA256, Repeats: repeats, WriterModel: writerModel, ConsumerModel: consumerModel, WriterPrompt: writerPromptSHA256, ConsumerPrompt: consumerPromptSHA256, CheckerSHA256: checkerSHA256, Cases: []UpkeepCase{
		{ID: "upkeep-reversal", WriterTask: "Change decision.go from Mode A to Mode B. Apply the target code exactly.", InitialCode: initialDecision, TargetCode: targetDecision, InitialBundle: initialBundle, ConsumerQuestion: reversal.Question, Expected: reversal.Expected},
		{ID: "upkeep-truncated", WriterTask: "Replace handler-capture.txt's complete trace with the supplied partial, truncated capture. Apply the target file exactly.", InitialCode: initialHandler, TargetCode: targetHandler, InitialBundle: initialBundle, ConsumerQuestion: truncated.Question, Expected: truncated.Expected},
	}}
	for _, c := range plan.Cases {
		if _, err := upkeepStartingHash(c); err != nil {
			return UpkeepStudy{}, fmt.Errorf("%s: %w", c.ID, err)
		}
		if _, err := UpkeepArtifactsSHA256(c.TargetCode); err != nil {
			return UpkeepStudy{}, fmt.Errorf("%s: %w", c.ID, err)
		}
	}
	if _, err := AnalyzeUpkeepStudy(plan, UpkeepStudyRows{}); err != nil {
		return UpkeepStudy{}, err
	}
	return plan, nil
}

func upkeepFindArtifact(artifacts []Artifact, id string) (Artifact, bool) {
	for _, a := range artifacts {
		if a.ID == id {
			return a, true
		}
	}
	return Artifact{}, false
}

const upkeepInitialDecisionDoc = `---
type: Note
title: Mode decision
description: Current mode decision.
status: draft
generated:
  at: "2026-01-01T00:00:00Z"
  by: process:okf-upkeep-study
---

The current decision and implementation use mode A.
`

const upkeepInitialHandlerDoc = `---
type: Note
title: Handler rollout evidence
description: Current handler rollout evidence.
status: draft
generated:
  at: "2026-01-01T00:00:00Z"
  by: process:okf-upkeep-study
---

The complete captured evidence indicates that the handler rollout finished.
`
