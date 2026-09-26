package agent

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
)

type ArmReport struct {
	Expected           int     `json:"expected"`
	Observed           int     `json:"observed"`
	Missing            int     `json:"missing"`
	Correct            int     `json:"correct"`
	Stale              int     `json:"stale"`
	Wrong              int     `json:"wrong"`
	Refusal            int     `json:"refusal"`
	Ungradable         int     `json:"ungradable"`
	OperationalFailure int     `json:"operational_failure"`
	ToolCalls          int     `json:"tool_calls"`
	InputTokens        int     `json:"input_tokens"`
	OutputTokens       int     `json:"output_tokens"`
	CacheTokens        int     `json:"cache_tokens"`
	ElapsedMS          int64   `json:"elapsed_ms"`
	FactualAttempts    int     `json:"factual_attempts"`
	FactualErrorRate   float64 `json:"factual_error_rate"`
	StaleRate          float64 `json:"stale_rate"`
	CompletionRate     float64 `json:"completion_rate"`
	CostKnownTrials    int     `json:"cost_known_trials"`
	CostUSD            float64 `json:"cost_usd"`
}

type Report struct {
	CorpusSHA256        string               `json:"corpus_sha256"`
	RunID               string               `json:"run_id"`
	ModelRuntimePath    string               `json:"model_runtime_path,omitempty"`
	ModelRuntimeVersion string               `json:"model_runtime_version,omitempty"`
	ModelRuntimeSHA256  string               `json:"model_runtime_sha256,omitempty"`
	PromptHashScope     string               `json:"prompt_hash_scope,omitempty"`
	RepeatCount         int                  `json:"repeat_count"`
	InvalidRawRows      int                  `json:"invalid_raw_rows"`
	Valid               bool                 `json:"valid"`
	Reasons             []string             `json:"reasons,omitempty"`
	Arms                map[string]ArmReport `json:"arms"`
}

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var revisionPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

func validateMetadata(m Metadata) error {
	if m.RunID == "" || !revisionPattern.MatchString(m.Commit) || !revisionPattern.MatchString(m.SpecRevision) || m.Model == "" || m.ModelVersion == "" || m.Settings == "" || m.Adapter == "" || m.Clock.IsZero() || !sha256Pattern.MatchString(m.CorpusSHA256) || !sha256Pattern.MatchString(m.PromptSHA256) || m.GraderRevision != "exact-v1" {
		return errors.New("incomplete or invalid pinned metadata")
	}
	return nil
}

func ReadRows(r io.Reader) ([]Row, error) {
	rows, invalid, err := ReadRowsWithInvalid(r)
	if err != nil {
		return nil, err
	}
	if invalid > 0 {
		return nil, fmt.Errorf("%d malformed raw rows", invalid)
	}
	return rows, nil
}

// ReadRowsWithInvalid retains well-formed rows and accounts for malformed
// lines. Their case/arm identity is unknowable; Analyze still counts the
// corresponding expected trial as missing.
func ReadRowsWithInvalid(r io.Reader) ([]Row, int, error) {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 64<<10), 2<<20)
	var rows []Row
	invalid := 0
	for s.Scan() {
		var row Row
		if err := ValidateWire("row", s.Bytes()); err != nil {
			invalid++
			continue
		}
		if err := json.Unmarshal(s.Bytes(), &row); err != nil {
			invalid++
			continue
		}
		rows = append(rows, row)
	}
	return rows, invalid, s.Err()
}

func Analyze(cases []Case, corpusHash string, rows []Row, repeats int) (Report, error) {
	if repeats < 1 {
		return Report{}, errors.New("repeats must be positive")
	}
	if err := ValidateCases(cases); err != nil {
		return Report{}, err
	}
	report := Report{CorpusSHA256: corpusHash, RepeatCount: repeats, Valid: true, Arms: map[string]ArmReport{}}
	byID := map[string]Case{}
	for _, c := range cases {
		byID[c.ID] = c
	}
	seen := map[string]bool{}
	var base *Metadata
	for _, row := range rows {
		if row.Arm != Control && row.Arm != Treatment {
			return Report{}, fmt.Errorf("invalid arm %q", row.Arm)
		}
		c, ok := byID[row.CaseID]
		if !ok || row.Repeat < 1 || row.Repeat > repeats {
			return Report{}, fmt.Errorf("unexpected trial %s/%s/%d", row.CaseID, row.Arm, row.Repeat)
		}
		key := fmt.Sprintf("%s/%s/%d", row.CaseID, row.Arm, row.Repeat)
		if seen[key] {
			return Report{}, fmt.Errorf("duplicate trial %s", key)
		}
		seen[key] = true
		if row.Observation.ToolCalls < 0 || row.Observation.InputTokens < 0 || row.Observation.OutputTokens < 0 || row.Observation.CacheTokens < 0 || row.Observation.ElapsedMS < 0 || row.TrialElapsedMS < 0 || row.Retries < 0 || (row.Observation.CostUSD != nil && *row.Observation.CostUSD < 0) {
			return Report{}, fmt.Errorf("%s: negative usage", key)
		}
		if row.Metadata.CorpusSHA256 != corpusHash || row.Metadata.GraderRevision != "exact-v1" {
			return Report{}, fmt.Errorf("%s: corpus/grader mismatch", key)
		}
		if err := validateMetadata(row.Metadata); err != nil {
			return Report{}, fmt.Errorf("%s: %w", key, err)
		}
		if base == nil {
			m := row.Metadata
			base = &m
			report.RunID = m.RunID
		} else if row.Metadata != *base {
			return Report{}, fmt.Errorf("%s: inconsistent metadata", key)
		}
		verdict := Grade(c, row.Observation, row.Failure)
		if row.Verdict != verdict {
			return Report{}, fmt.Errorf("%s: stored verdict %q != recomputed %q", key, row.Verdict, verdict)
		}
		a := report.Arms[row.Arm]
		a.Observed++
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
			a.OperationalFailure++
		}
		a.ToolCalls += row.Observation.ToolCalls
		a.InputTokens += row.Observation.InputTokens
		a.OutputTokens += row.Observation.OutputTokens
		a.CacheTokens += row.Observation.CacheTokens
		a.ElapsedMS += row.Observation.ElapsedMS
		if row.Observation.CostUSD != nil {
			a.CostKnownTrials++
			a.CostUSD += *row.Observation.CostUSD
		}
		report.Arms[row.Arm] = a
	}
	for _, arm := range []string{Control, Treatment} {
		a := report.Arms[arm]
		a.Expected = len(cases) * repeats
		a.Missing = a.Expected - a.Observed
		a.FactualAttempts = a.Correct + a.Stale + a.Wrong
		if a.FactualAttempts > 0 {
			a.FactualErrorRate = float64(a.Stale+a.Wrong) / float64(a.FactualAttempts)
			a.StaleRate = float64(a.Stale) / float64(a.FactualAttempts)
		}
		a.CompletionRate = float64(a.Correct) / float64(a.Expected)
		if a.Missing > 0 {
			report.Valid = false
			report.Reasons = append(report.Reasons, fmt.Sprintf("%s: %d missing trials", arm, a.Missing))
		}
		if a.OperationalFailure > 0 {
			report.Valid = false
			report.Reasons = append(report.Reasons, fmt.Sprintf("%s: %d operational failures", arm, a.OperationalFailure))
		}
		report.Arms[arm] = a
	}
	if len(rows) == 0 {
		report.Valid = false
		report.Reasons = append(report.Reasons, "no rows")
	}
	sort.Strings(report.Reasons)
	return report, nil
}

func AnalyzePlanned(cases []Case, corpusHash string, rows []Row, plan Plan) (Report, error) {
	if err := validateMetadata(plan.Metadata); err != nil {
		return Report{}, err
	}
	if plan.Metadata.CorpusSHA256 != corpusHash || plan.CaseCount != len(cases) || plan.Repeats < 1 || plan.MaxTrials < len(cases)*2*plan.Repeats || plan.MaxSeconds < 1 || plan.MaxTokens < 1 || plan.MaxCostUSD < 0 || (plan.MaxCostUSD == 0 && !plan.Unpriced) || (plan.MaxCostUSD > 0 && plan.Unpriced) || !sha256Pattern.MatchString(plan.SpecSHA256) || !sha256Pattern.MatchString(plan.AdapterSHA256) || (plan.ToolkitMode != "none" && plan.ToolkitMode != "treatment" && plan.ToolkitMode != "both" && plan.ToolkitMode != "direct") || plan.ModelToolAccess == "" || (plan.ToolkitMode == "direct" && !sha256Pattern.MatchString(plan.GoCLISHA256)) {
		return Report{}, errors.New("plan does not match corpus or valid budget")
	}
	if plan.ModelRuntimePath != "" || plan.ModelRuntimeVersion != "" || plan.ModelRuntimeSHA256 != "" {
		if !filepath.IsAbs(plan.ModelRuntimePath) || plan.ModelRuntimeVersion == "" || !sha256Pattern.MatchString(plan.ModelRuntimeSHA256) {
			return Report{}, errors.New("incomplete model runtime provenance")
		}
	}
	if plan.PromptHashScope != "" && plan.PromptHashScope != "shared_request_instructions_only" {
		return Report{}, errors.New("unknown prompt hash scope")
	}
	diagnosticBudget := (plan.Metadata.RunID == DiagnosticV1RunID && plan.MaxTokens == DiagnosticV1MaxTokens) || (plan.Metadata.RunID == DiagnosticV2RunID && plan.MaxTokens == DiagnosticV2MaxTokens)
	if plan.DiagnosticRead && (!diagnosticBudget || corpusHash != DiagnosticCorpusSHA256 || len(cases) != 1 || cases[0].ID != "repo-default-okf-version" || cases[0].Tier != "realistic" || plan.ToolkitMode != "direct" || plan.Repeats != 1 || plan.MaxTrials != 2 || plan.MaxSeconds != 120 || !plan.Unpriced || plan.MaxCostUSD != 0 || plan.Exploratory || plan.ModelToolAccess != "Codex read-only ephemeral temp with shell; Go CLI binary supplied") {
		return Report{}, errors.New("invalid diagnostic read plan")
	}
	for _, row := range rows {
		if row.Metadata != plan.Metadata {
			return Report{}, fmt.Errorf("%s/%s/%d: row differs from preregistered plan", row.CaseID, row.Arm, row.Repeat)
		}
		wantToolkit := plan.ToolkitMode == "both" || (plan.ToolkitMode == "treatment" && row.Arm == Treatment)
		if !wantToolkit && len(row.ToolkitEvidence) > 0 {
			return Report{}, fmt.Errorf("%s/%s: unexpected toolkit evidence", row.CaseID, row.Arm)
		}
		if wantToolkit && row.Failure == "" && len(row.ToolkitEvidence) == 0 {
			return Report{}, fmt.Errorf("%s/%s: missing toolkit evidence", row.CaseID, row.Arm)
		}
		if len(row.ToolkitEvidence) > row.Observation.ToolCalls {
			return Report{}, fmt.Errorf("%s/%s: toolkit calls undercounted", row.CaseID, row.Arm)
		}
		if plan.ToolkitMode == "direct" && row.Arm == Treatment && row.Failure == "" && row.Observation.ToolkitCalls < 1 {
			return Report{}, fmt.Errorf("%s: model made no Go CLI call", row.CaseID)
		}
		if plan.DiagnosticRead && row.Failure == "" {
			artifacts := cases[0].Control
			if row.Arm == Treatment {
				artifacts = cases[0].Treatment
			}
			if !allReadIDs(artifacts, row.Observation.ReadArtifacts) {
				return Report{}, fmt.Errorf("%s/%s: missing confirmed artifact reads", row.CaseID, row.Arm)
			}
		}
	}
	report, err := Analyze(cases, corpusHash, rows, plan.Repeats)
	if err != nil {
		return Report{}, err
	}
	report.ModelRuntimePath = plan.ModelRuntimePath
	report.ModelRuntimeVersion = plan.ModelRuntimeVersion
	report.ModelRuntimeSHA256 = plan.ModelRuntimeSHA256
	report.PromptHashScope = plan.PromptHashScope
	if plan.ToolkitMode == "direct" && plan.ModelRuntimePath == "" {
		report.Valid = false
		report.Reasons = append(report.Reasons, "direct run lacks pinned model runtime")
	}
	usedTokens, usedCost, knownCost, elapsed := 0, 0.0, 0, int64(0)
	for _, row := range rows {
		usedTokens += row.Observation.InputTokens + row.Observation.OutputTokens
		elapsed += row.TrialElapsedMS
		if row.Observation.CostUSD != nil {
			usedCost += *row.Observation.CostUSD
			knownCost++
		}
	}
	if usedTokens > plan.MaxTokens {
		report.Valid = false
		report.Reasons = append(report.Reasons, "token budget exceeded")
	}
	if elapsed > int64(plan.MaxSeconds)*1000 {
		report.Valid = false
		report.Reasons = append(report.Reasons, "wall-clock budget exceeded")
	}
	if plan.MaxCostUSD > 0 && (knownCost != len(rows) || usedCost > plan.MaxCostUSD) {
		report.Valid = false
		report.Reasons = append(report.Reasons, "cost data missing or budget exceeded")
	}
	if plan.Exploratory {
		report.Valid = false
		report.Reasons = append(report.Reasons, "exploratory dirty-tree run cannot satisfy acceptance")
	}
	sort.Strings(report.Reasons)
	return report, nil
}

func allReadIDs(artifacts []Artifact, readIDs []string) bool {
	read := make(map[string]bool, len(readIDs))
	for _, id := range readIDs {
		read[id] = true
	}
	for _, a := range artifacts {
		if !read[a.ID] {
			return false
		}
	}
	return true
}

func AnalyzePlannedWithInvalid(cases []Case, corpusHash string, rows []Row, malformed int, plan Plan) (Report, error) {
	if malformed < 0 {
		return Report{}, errors.New("negative malformed row count")
	}
	r, err := AnalyzePlanned(cases, corpusHash, rows, plan)
	if err != nil {
		return Report{}, err
	}
	r.InvalidRawRows = malformed
	if malformed > 0 {
		r.Valid = false
		r.Reasons = append(r.Reasons, fmt.Sprintf("%d malformed raw rows", malformed))
		sort.Strings(r.Reasons)
	}
	return r, nil
}
