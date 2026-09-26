package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/skosovsky/okf/upkeep"
)

// UpkeepStudy is the frozen, provider-independent contract for the separate
// task-013 writer-to-consumer experiment. Each arm starts from the same bytes.
// The checker is the only intended writer-arm difference.
type UpkeepStudy struct {
	ID                 string       `json:"id"`
	SourceCorpusSHA256 string       `json:"source_corpus_sha256"`
	Repeats            int          `json:"repeats"`
	WriterModel        string       `json:"writer_model"`
	ConsumerModel      string       `json:"consumer_model"`
	WriterPrompt       string       `json:"writer_prompt_sha256"`
	ConsumerPrompt     string       `json:"consumer_prompt_sha256"`
	CheckerSHA256      string       `json:"checker_sha256"`
	Cases              []UpkeepCase `json:"cases"`
}

type UpkeepCase struct {
	ID               string     `json:"id"`
	WriterTask       string     `json:"writer_task"`
	InitialCode      []Artifact `json:"initial_code"`
	TargetCode       []Artifact `json:"target_code"`
	InitialBundle    []Artifact `json:"initial_bundle"`
	ConsumerQuestion string     `json:"consumer_question"`
	Expected         Expected   `json:"expected"`
}

// WriterRequest is the only case projection that may be sent to a writer.
// The exact requested edit is visible; the gold answer and consumer question
// are deliberately absent. The checker reminder is added only in the on arm.
type UpkeepWriterRequest struct {
	CaseID        string     `json:"case_id"`
	Task          string     `json:"task"`
	InitialCode   []Artifact `json:"initial_code"`
	TargetCode    []Artifact `json:"target_code"`
	InitialBundle []Artifact `json:"initial_bundle"`
}

// ConsumerRequest is the only case projection that may be sent to a consumer.
// It omits the writer task, code edit, checker arm and gold answer.
type UpkeepConsumerRequest struct {
	CaseID    string     `json:"case_id"`
	Question  string     `json:"question"`
	Artifacts []Artifact `json:"artifacts"`
}

func (c UpkeepCase) WriterRequest() UpkeepWriterRequest {
	return UpkeepWriterRequest{CaseID: c.ID, Task: c.WriterTask, InitialCode: c.InitialCode, TargetCode: c.TargetCode, InitialBundle: c.InitialBundle}
}

func (c UpkeepCase) ConsumerRequest(finalBundle []Artifact) UpkeepConsumerRequest {
	return UpkeepConsumerRequest{CaseID: c.ID, Question: c.ConsumerQuestion, Artifacts: finalBundle}
}

// UpkeepWriterRow records actual output bytes, rather than a writer's claim
// that documentation was updated. CheckerResult is diagnostic process evidence.
type UpkeepWriterRow struct {
	CaseID         string     `json:"case_id"`
	Arm            string     `json:"arm"` // checker_off or checker_on
	Repeat         int        `json:"repeat"`
	SessionID      string     `json:"session_id"`
	StartingSHA256 string     `json:"starting_sha256"`
	FinalCode      []Artifact `json:"final_code"`
	FinalBundle    []Artifact `json:"final_bundle"`
	CheckerInitial string     `json:"checker_initial,omitempty"`
	CheckerResult  string     `json:"checker_result,omitempty"`
	Failure        string     `json:"failure,omitempty"`
	InputTokens    int        `json:"input_tokens"`
	OutputTokens   int        `json:"output_tokens"`
}

// ConsumerSessionID must differ from the writer session. The runner must send
// only ConsumerQuestion and FinalBundle to this independent consumer.
type UpkeepConsumerRow struct {
	CaseID            string      `json:"case_id"`
	Arm               string      `json:"arm"`
	Repeat            int         `json:"repeat"`
	ConsumerSessionID string      `json:"consumer_session_id"`
	VisibleSHA256     string      `json:"visible_bundle_sha256"`
	Observation       Observation `json:"observation"`
	Failure           string      `json:"failure,omitempty"`
}

type UpkeepStudyRows struct {
	Writers   []UpkeepWriterRow   `json:"writers"`
	Consumers []UpkeepConsumerRow `json:"consumers"`
}

type UpkeepArmReport struct {
	Expected          int     `json:"expected"`
	WriterObserved    int     `json:"writer_observed"`
	ConsumerObserved  int     `json:"consumer_observed"`
	DocUpdated        int     `json:"doc_updated"`
	ConceptUpdated    int     `json:"concept_updated"`
	DocDeleted        int     `json:"doc_deleted"`
	ConceptDeleted    int     `json:"concept_deleted"`
	Correct           int     `json:"correct"`
	Stale             int     `json:"stale"`
	Wrong             int     `json:"wrong"`
	Refusal           int     `json:"refusal"`
	Ungradable        int     `json:"ungradable"`
	Failures          int     `json:"failures"`
	DocUpdateRate     float64 `json:"doc_update_rate"`
	ConceptUpdateRate float64 `json:"concept_update_rate"`
	AnswerQualityRate float64 `json:"answer_quality_rate"`
}

type UpkeepStudyReport struct {
	ID      string                     `json:"id"`
	Valid   bool                       `json:"valid"`
	Reasons []string                   `json:"reasons,omitempty"`
	Arms    map[string]UpkeepArmReport `json:"arms"`
}

const (
	UpkeepCheckerOff = "checker_off"
	UpkeepCheckerOn  = "checker_on"
)

// UpkeepArtifactsSHA256 hashes paths, IDs and bytes in stable path order.
// The same function binds the writer's final bundle to the consumer input.
func UpkeepArtifactsSHA256(artifacts []Artifact) (string, error) {
	if len(artifacts) == 0 {
		return "", errors.New("empty artifact set")
	}
	ordered := append([]Artifact(nil), artifacts...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Path < ordered[j].Path })
	seen := map[string]bool{}
	seenIDs := map[string]bool{}
	for _, a := range ordered {
		if a.ID == "" || a.Path == "" || a.Content == "" || path.IsAbs(a.Path) || path.Clean(a.Path) != a.Path || a.Path == ".." || strings.HasPrefix(a.Path, "../") || strings.Contains(a.Path, "\\") || seen[a.Path] || seenIDs[a.ID] {
			return "", fmt.Errorf("invalid or duplicate artifact path %q", a.Path)
		}
		seen[a.Path] = true
		seenIDs[a.ID] = true
	}
	b, err := json.Marshal(ordered)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

func upkeepStartingHash(c UpkeepCase) (string, error) {
	code, err := UpkeepArtifactsSHA256(c.InitialCode)
	if err != nil {
		return "", err
	}
	bundle, err := UpkeepArtifactsSHA256(c.InitialBundle)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256([]byte(code + "\x00" + bundle))
	return hex.EncodeToString(h[:]), nil
}

type upkeepDocDelta struct{ updated, conceptUpdated, deleted, conceptDeleted bool }

func upkeepDocChanges(before, after []Artifact) upkeepDocDelta {
	old := map[string]string{}
	newDocs := map[string]string{}
	for _, a := range before {
		if strings.HasSuffix(a.Path, ".md") {
			old[a.Path] = a.Content
		}
	}
	for _, a := range after {
		if strings.HasSuffix(a.Path, ".md") {
			newDocs[a.Path] = a.Content
		}
	}
	var delta upkeepDocDelta
	for p, content := range old {
		newContent, exists := newDocs[p]
		if !exists {
			delta.deleted = true
			if path.Base(p) != "index.md" && path.Base(p) != "log.md" {
				delta.conceptDeleted = true
			}
		} else if newContent != content {
			delta.updated = true
			if path.Base(p) != "index.md" && path.Base(p) != "log.md" {
				delta.conceptUpdated = true
			}
		}
	}
	for p := range newDocs {
		if _, exists := old[p]; !exists {
			delta.updated = true
			if path.Base(p) != "index.md" && path.Base(p) != "log.md" {
				delta.conceptUpdated = true
			}
		}
	}
	return delta
}

// AnalyzeUpkeepStudy enforces paired inputs and independent consumers. Rates
// use all preregistered cases × repeats; missing rows cannot improve a rate.
// It does not infer document truth from a checker result or an edited file.
func AnalyzeUpkeepStudy(plan UpkeepStudy, rows UpkeepStudyRows) (UpkeepStudyReport, error) {
	if plan.ID == "" || !sha256Pattern.MatchString(plan.SourceCorpusSHA256) || plan.Repeats < 1 || plan.WriterModel == "" || plan.ConsumerModel == "" || !sha256Pattern.MatchString(plan.WriterPrompt) || !sha256Pattern.MatchString(plan.ConsumerPrompt) || !sha256Pattern.MatchString(plan.CheckerSHA256) || len(plan.Cases) == 0 {
		return UpkeepStudyReport{}, errors.New("incomplete upkeep study plan")
	}
	cases := map[string]UpkeepCase{}
	starts := map[string]string{}
	targets := map[string]string{}
	for _, c := range plan.Cases {
		if c.ID == "" || cases[c.ID].ID != "" || c.WriterTask == "" || c.ConsumerQuestion == "" || c.Expected.Answer == "" || len(c.Expected.Evidence) == 0 {
			return UpkeepStudyReport{}, fmt.Errorf("incomplete or duplicate case %q", c.ID)
		}
		start, err := upkeepStartingHash(c)
		if err != nil {
			return UpkeepStudyReport{}, err
		}
		target, err := UpkeepArtifactsSHA256(c.TargetCode)
		if err != nil {
			return UpkeepStudyReport{}, err
		}
		cases[c.ID], starts[c.ID], targets[c.ID] = c, start, target
	}
	key := func(id, arm string, repeat int) string { return fmt.Sprintf("%s/%s/%d", id, arm, repeat) }
	writers := map[string]UpkeepWriterRow{}
	consumers := map[string]UpkeepConsumerRow{}
	sessions := map[string]bool{}
	for _, w := range rows.Writers {
		k := key(w.CaseID, w.Arm, w.Repeat)
		if _, ok := cases[w.CaseID]; !ok || (w.Arm != UpkeepCheckerOff && w.Arm != UpkeepCheckerOn) || w.Repeat < 1 || w.Repeat > plan.Repeats || writers[k].CaseID != "" || (w.SessionID == "" && w.Failure == "") || (w.SessionID != "" && sessions[w.SessionID]) || w.InputTokens < 0 || w.OutputTokens < 0 {
			return UpkeepStudyReport{}, fmt.Errorf("invalid or duplicate writer row %s", k)
		}
		if w.StartingSHA256 != starts[w.CaseID] {
			return UpkeepStudyReport{}, fmt.Errorf("%s: starting snapshot differs", k)
		}
		if w.Arm == UpkeepCheckerOff && (w.CheckerInitial != "" || w.CheckerResult != "") {
			return UpkeepStudyReport{}, fmt.Errorf("%s: checker result in off arm", k)
		}
		if w.Arm == UpkeepCheckerOn && (w.CheckerInitial == "" || w.CheckerResult == "") && w.Failure == "" {
			return UpkeepStudyReport{}, fmt.Errorf("%s: missing checker result", k)
		}
		for _, raw := range []string{w.CheckerInitial, w.CheckerResult} {
			if raw == "" {
				continue
			}
			var result upkeep.Result
			if err := json.Unmarshal([]byte(raw), &result); err != nil {
				return UpkeepStudyReport{}, fmt.Errorf("%s: invalid checker JSON: %w", k, err)
			}
			if !sha256Pattern.MatchString(result.Fingerprint) || !result.Advisory || len(result.Changes) == 0 {
				return UpkeepStudyReport{}, fmt.Errorf("%s: checker result lacks relevant advisory evidence", k)
			}
			relevantCode := false
			for _, change := range result.Changes {
				for _, target := range cases[w.CaseID].TargetCode {
					if change.Path == target.Path || change.OldPath == target.Path {
						relevantCode = true
					}
				}
			}
			if !relevantCode {
				return UpkeepStudyReport{}, fmt.Errorf("%s: checker result is unrelated to target code", k)
			}
			switch result.Status {
			case "needs_review", "reviewed_updated", "explicit_unaffected", "no_relevant_change", "overridden", "loop_guard_allow", "unavailable":
			default:
				return UpkeepStudyReport{}, fmt.Errorf("%s: invalid checker status %q", k, result.Status)
			}
		}
		if w.Failure == "" {
			code, err := UpkeepArtifactsSHA256(w.FinalCode)
			if err != nil {
				return UpkeepStudyReport{}, fmt.Errorf("%s: %w", k, err)
			}
			if code != targets[w.CaseID] {
				return UpkeepStudyReport{}, fmt.Errorf("%s: code task not completed", k)
			}
			if _, err := UpkeepArtifactsSHA256(w.FinalBundle); err != nil {
				return UpkeepStudyReport{}, fmt.Errorf("%s: %w", k, err)
			}
		}
		writers[k] = w
		if w.SessionID != "" {
			sessions[w.SessionID] = true
		}
	}
	for _, c := range rows.Consumers {
		k := key(c.CaseID, c.Arm, c.Repeat)
		w, exists := writers[k]
		if !exists || w.Failure != "" || consumers[k].CaseID != "" || (c.ConsumerSessionID == "" && c.Failure == "") || (c.ConsumerSessionID != "" && sessions[c.ConsumerSessionID]) || c.Observation.InputTokens < 0 || c.Observation.OutputTokens < 0 {
			return UpkeepStudyReport{}, fmt.Errorf("invalid, duplicate or non-independent consumer row %s", k)
		}
		bundle, err := UpkeepArtifactsSHA256(w.FinalBundle)
		if err != nil {
			return UpkeepStudyReport{}, err
		}
		if c.VisibleSHA256 != bundle {
			return UpkeepStudyReport{}, fmt.Errorf("%s: consumer did not receive writer bundle", k)
		}
		ids := map[string]bool{}
		for _, a := range w.FinalBundle {
			ids[a.ID] = true
		}
		for _, evidence := range c.Observation.Evidence {
			if !ids[evidence] {
				return UpkeepStudyReport{}, fmt.Errorf("%s: citation absent from writer bundle", k)
			}
		}
		consumers[k] = c
		if c.ConsumerSessionID != "" {
			sessions[c.ConsumerSessionID] = true
		}
	}
	report := UpkeepStudyReport{ID: plan.ID, Valid: true, Arms: map[string]UpkeepArmReport{}}
	for _, arm := range []string{UpkeepCheckerOff, UpkeepCheckerOn} {
		a := UpkeepArmReport{Expected: len(plan.Cases) * plan.Repeats}
		for _, spec := range plan.Cases {
			for repeat := 1; repeat <= plan.Repeats; repeat++ {
				k := key(spec.ID, arm, repeat)
				w, ok := writers[k]
				if !ok {
					report.Reasons = append(report.Reasons, "missing writer "+k)
					continue
				}
				a.WriterObserved++
				if w.Failure != "" {
					a.Failures++
					report.Reasons = append(report.Reasons, "writer failed "+k)
					continue
				}
				if arm == UpkeepCheckerOn {
					var initial, final struct {
						Status string `json:"status"`
					}
					_ = json.Unmarshal([]byte(w.CheckerInitial), &initial)
					_ = json.Unmarshal([]byte(w.CheckerResult), &final)
					if initial.Status != "needs_review" || final.Status == "unavailable" || final.Status == "no_relevant_change" || final.Status == "overridden" || final.Status == "loop_guard_allow" {
						report.Reasons = append(report.Reasons, "checker protocol failed "+k)
					}
				}
				delta := upkeepDocChanges(spec.InitialBundle, w.FinalBundle)
				if delta.updated {
					a.DocUpdated++
				}
				if delta.conceptUpdated {
					a.ConceptUpdated++
				}
				if delta.deleted {
					a.DocDeleted++
				}
				if delta.conceptDeleted {
					a.ConceptDeleted++
				}
				c, ok := consumers[k]
				if !ok {
					report.Reasons = append(report.Reasons, "missing consumer "+k)
					continue
				}
				a.ConsumerObserved++
				failure := c.Failure
				if c.Observation.AdapterError != "" {
					failure = c.Observation.AdapterError
				}
				verdict := Grade(Case{Expected: spec.Expected}, c.Observation, failure)
				switch verdict {
				case Correct:
					a.Correct++
				case Stale:
					a.Stale++
				case Wrong:
					a.Wrong++
				case Refusal:
					a.Refusal++
				case Ungradable:
					a.Ungradable++
				case OperationalFailure:
					a.Failures++
					report.Reasons = append(report.Reasons, "consumer failed "+k)
				}
			}
		}
		a.DocUpdateRate = float64(a.DocUpdated) / float64(a.Expected)
		a.ConceptUpdateRate = float64(a.ConceptUpdated) / float64(a.Expected)
		a.AnswerQualityRate = float64(a.Correct) / float64(a.Expected)
		report.Arms[arm] = a
	}
	if len(report.Reasons) > 0 {
		report.Valid = false
	}
	sort.Strings(report.Reasons)
	return report, nil
}
