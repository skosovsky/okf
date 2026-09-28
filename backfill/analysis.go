package backfill

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/skosovsky/okf/bundle"
)

// ValidateAnalysis checks provenance, exact event coverage, candidate identity,
// and reversal ordering. It does not claim that an analyst's prose is true.
func ValidateAnalysis(m Manifest, a Analysis) (Coverage, []Candidate, error) {
	report := Coverage{IncludedEvents: len(m.Events), MissingEvents: []string{}, UnresolvedConflicts: []string{}, TruncatedEvents: []string{}, SkippedFiles: map[string]int{}, Limitations: []string{"Coverage proves event disposition, not factual correctness.", "Git intent and partial diffs do not prove runtime behavior."}}
	report.Usage = a.Usage
	if a.Usage != nil && (a.Usage.InputTokens < 0 || a.Usage.OutputTokens < 0 || a.Usage.CostUSD < 0) {
		return report, nil, fmt.Errorf("negative usage")
	}
	if m.SchemaVersion != SchemaVersion || m.ExtractorVersion != ExtractorVersion || a.SchemaVersion != SchemaVersion || a.EventsSHA256 != m.EventsSHA256 || a.PromptVersion == "" {
		return report, nil, fmt.Errorf("analysis input/version binding mismatch")
	}
	configJSON, err := json.Marshal(m.Config)
	if err != nil {
		return report, nil, err
	}
	eventsJSON, err := json.Marshal(m.Events)
	if err != nil {
		return report, nil, err
	}
	if digest(configJSON) != m.ConfigSHA256 || digest(eventsJSON) != m.EventsSHA256 || m.Config.Repository != m.Repository || m.Config.Base != m.Base || m.Config.Head != m.Head {
		return report, nil, fmt.Errorf("manifest digest or frozen ref mismatch")
	}
	if t, err := time.Parse(time.RFC3339, a.ProducedAt); err != nil || t.Location() == nil {
		return report, nil, fmt.Errorf("produced_at must be RFC3339")
	}
	events := make(map[string]Event, len(m.Events))
	for _, e := range m.Events {
		shownBytes := int64(len(e.Diff))
		if e.DiffBytes < shownBytes || e.DiffTruncated != (e.DiffBytes > shownBytes) || (!e.DiffTruncated && e.DiffSHA256 != digest([]byte(e.Diff))) {
			return report, nil, fmt.Errorf("event %s has inconsistent diff completeness", e.ID)
		}
		if _, err := time.Parse(time.RFC3339, e.AuthorTime); err != nil {
			return report, nil, fmt.Errorf("event %s has invalid author_time", e.ID)
		}
		if _, err := time.Parse(time.RFC3339, e.CommitterTime); err != nil {
			return report, nil, fmt.Errorf("event %s has invalid committer_time", e.ID)
		}
		if _, ok := events[e.ID]; ok {
			return report, nil, fmt.Errorf("duplicate event %s", e.ID)
		}
		events[e.ID] = e
		if e.DiffTruncated {
			report.TruncatedEvents = append(report.TruncatedEvents, e.ID)
		}
		for _, f := range e.Files {
			if f.SkippedReason != "" {
				report.SkippedFiles[f.SkippedReason]++
			}
		}
	}
	candidates := make(map[string]Candidate, len(a.Candidates))
	byConcept := map[string][]string{}
	for _, c := range a.Candidates {
		if c.ID == "" || c.ConceptID == "" || c.Title == "" || c.Description == "" || strings.TrimSpace(c.Body) == "" {
			return report, nil, fmt.Errorf("incomplete candidate %q", c.ID)
		}
		if _, err := bundle.ParseConceptID(c.ConceptID); err != nil {
			return report, nil, fmt.Errorf("candidate %s: %w", c.ID, err)
		}
		if _, ok := candidates[c.ID]; ok {
			return report, nil, fmt.Errorf("duplicate candidate %q", c.ID)
		}
		if len(c.Evidence) == 0 {
			return report, nil, fmt.Errorf("candidate %s has no evidence", c.ID)
		}
		incomplete := false
		for _, ref := range c.Evidence {
			e, ok := events[ref.EventID]
			if !ok || ref.DiffSHA256 != e.DiffSHA256 {
				return report, nil, fmt.Errorf("candidate %s has unbound evidence", c.ID)
			}
			if ref.Path == "" {
				return report, nil, fmt.Errorf("candidate %s evidence requires file path", c.ID)
			}
			if ref.Path != "" {
				found := false
				for _, f := range e.Files {
					if f.Path == ref.Path && f.SkippedReason == "" {
						found = true
						break
					}
				}
				if !found {
					return report, nil, fmt.Errorf("candidate %s cites excluded or absent file", c.ID)
				}
			}
			incomplete = incomplete || e.DiffTruncated
		}
		if incomplete && !c.EvidenceIncomplete {
			return report, nil, fmt.Errorf("candidate %s must acknowledge truncated evidence", c.ID)
		}
		for _, h := range c.History {
			if h.Text == "" {
				return report, nil, fmt.Errorf("empty history note")
			}
			if _, ok := events[h.EventID]; !ok {
				return report, nil, fmt.Errorf("history note cites unknown event")
			}
		}
		if c.UnresolvedConflict != "" {
			report.UnresolvedConflicts = append(report.UnresolvedConflicts, c.ID)
		}
		candidates[c.ID] = c
		byConcept[c.ConceptID] = append(byConcept[c.ConceptID], c.ID)
	}
	seenResults := map[string]bool{}
	consideredLinks := map[string]map[string]bool{}
	for _, r := range a.Results {
		if _, ok := events[r.EventID]; !ok || seenResults[r.EventID] {
			return report, nil, fmt.Errorf("unknown or duplicate event result %q", r.EventID)
		}
		seenResults[r.EventID] = true
		switch r.Decision {
		case DecisionRejected:
			if r.Reason == "" || len(r.CandidateIDs) != 0 {
				return report, nil, fmt.Errorf("rejected event requires reason and no candidates")
			}
			report.RejectedEvents++
		case DecisionConsidered:
			if len(r.CandidateIDs) == 0 {
				return report, nil, fmt.Errorf("considered event requires candidates")
			}
			for _, id := range r.CandidateIDs {
				if consideredLinks[id] == nil {
					consideredLinks[id] = map[string]bool{}
				}
				if consideredLinks[id][r.EventID] {
					return report, nil, fmt.Errorf("duplicate candidate link %q for event %q", id, r.EventID)
				}
				consideredLinks[id][r.EventID] = true
				c, ok := candidates[id]
				if !ok {
					return report, nil, fmt.Errorf("event result references unknown candidate %q", id)
				}
				linked := false
				for _, e := range c.Evidence {
					if e.EventID == r.EventID {
						linked = true
						break
					}
				}
				if !linked {
					return report, nil, fmt.Errorf("event result has no candidate evidence")
				}
			}
			report.ConsideredEvents++
		default:
			return report, nil, fmt.Errorf("invalid event decision")
		}
	}
	for _, e := range m.Events {
		if !seenResults[e.ID] {
			report.MissingEvents = append(report.MissingEvents, e.ID)
		}
	}
	if len(report.MissingEvents) > 0 {
		return report, nil, fmt.Errorf("missing %d event dispositions", len(report.MissingEvents))
	}
	for _, c := range a.Candidates {
		links := consideredLinks[c.ID]
		if len(links) == 0 {
			return report, nil, fmt.Errorf("candidate %s has no considered event", c.ID)
		}
		for _, ref := range c.Evidence {
			if !links[ref.EventID] {
				return report, nil, fmt.Errorf("candidate %s evidence %s was not considered for it", c.ID, ref.EventID)
			}
		}
	}
	// A predecessor is historical, never another active concept. A successor
	// must refer to the same entity and preserve an explicit history note.
	superseded := map[string]bool{}
	for _, c := range a.Candidates {
		for _, oldID := range c.Supersedes {
			old, ok := candidates[oldID]
			if !ok || old.ConceptID != c.ConceptID || oldID == c.ID {
				return report, nil, fmt.Errorf("invalid supersession %q -> %q", oldID, c.ID)
			}
			if superseded[oldID] {
				return report, nil, fmt.Errorf("multiple successors for %q", oldID)
			}
			superseded[oldID] = true
			if len(c.History) == 0 {
				return report, nil, fmt.Errorf("successor %s lacks history", c.ID)
			}
			if !eventPrecedes(m, old.Evidence[0].EventID, c.Evidence[0].EventID) {
				return report, nil, fmt.Errorf("supersession contradicts causal order")
			}
		}
	}
	conflictedConcepts := map[string]bool{}
	for _, c := range a.Candidates {
		if c.UnresolvedConflict != "" {
			conflictedConcepts[c.ConceptID] = true
		}
	}
	active := []Candidate{}
	for _, c := range a.Candidates {
		if !superseded[c.ID] && !conflictedConcepts[c.ConceptID] {
			active = append(active, c)
		}
	}
	for concept, ids := range byConcept {
		count := 0
		for _, id := range ids {
			if !superseded[id] && !conflictedConcepts[concept] {
				count++
			}
		}
		if count > 1 {
			return report, nil, fmt.Errorf("multiple active candidates for %s", concept)
		}
	}
	sort.Slice(active, func(i, j int) bool { return active[i].ConceptID < active[j].ConceptID })
	sort.Strings(report.UnresolvedConflicts)
	sort.Strings(report.TruncatedEvents)
	return report, active, nil
}

func eventPrecedes(m Manifest, oldID, newID string) bool {
	if oldID == newID {
		return false
	}
	commits := map[string]Event{}
	for _, e := range m.Events {
		commits[e.Commit] = e
	}
	old := strings.TrimPrefix(oldID, "git:")
	newCommit := strings.TrimPrefix(newID, "git:")
	stack := []string{newCommit}
	seen := map[string]bool{}
	for len(stack) > 0 {
		last := len(stack) - 1
		id := stack[last]
		stack = stack[:last]
		if seen[id] {
			continue
		}
		seen[id] = true
		if id == old {
			return true
		}
		e, ok := commits[id]
		if ok {
			stack = append(stack, e.Parents...)
		}
	}
	return false
}

func AnalysisDigest(a Analysis) (string, error) {
	b, err := json.Marshal(a)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
