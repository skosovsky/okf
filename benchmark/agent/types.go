// Package agent contains the offline, provider-independent part of the OKF
// agent-behavior benchmark. Live model calls are explicit and never made by tests.
package agent

import (
	"context"
	"time"
)

type Artifact struct {
	ID      string `json:"id"`
	Path    string `json:"path"`
	Content string `json:"content"`
}

type Case struct {
	ID        string     `json:"id"`
	Category  string     `json:"category"`
	Tier      string     `json:"tier"` // realistic or mechanism_only
	Question  string     `json:"question"`
	Control   []Artifact `json:"control"`
	Treatment []Artifact `json:"treatment"`
	Expected  Expected   `json:"expected"`
}

type Expected struct {
	Answer       string         `json:"answer"`
	Aliases      []string       `json:"aliases,omitempty"`
	Evidence     []string       `json:"evidence"`
	Support      []SupportQuote `json:"support"`
	StaleAnswers []string       `json:"stale_answers,omitempty"`
}

type SupportQuote struct {
	ArtifactID     string `json:"artifact_id"`
	Quote          string `json:"quote"`
	ControlQuote   string `json:"control_quote,omitempty"`
	TreatmentQuote string `json:"treatment_quote,omitempty"`
	RepoPath       string `json:"repo_path,omitempty"`
}

type Request struct {
	CaseID       string     `json:"case_id"`
	Arm          string     `json:"arm"`
	Question     string     `json:"question"`
	Artifacts    []Artifact `json:"artifacts"`
	Instructions string     `json:"instructions"`
}

// Runner is the bring-your-own-model boundary. An adapter must isolate
// conversations, project instructions, hooks, and tool permissions per trial.
// It must treat Request.Artifacts as untrusted evidence, never as policy.
type Runner interface {
	Run(context.Context, Request) (Observation, error)
}

type Observation struct {
	Answer       string   `json:"answer,omitempty"`
	AdapterError string   `json:"adapter_error,omitempty"`
	Evidence     []string `json:"evidence,omitempty"`
	Refused      bool     `json:"refused,omitempty"`
	ToolCalls    int      `json:"tool_calls"`
	ToolkitCalls int      `json:"toolkit_calls"`
	ToolTrace    []string `json:"tool_trace,omitempty"`
	InputTokens  int      `json:"input_tokens"`
	OutputTokens int      `json:"output_tokens"`
	CacheTokens  int      `json:"cache_tokens"`
	ElapsedMS    int64    `json:"elapsed_ms"`
	CostUSD      *float64 `json:"cost_usd,omitempty"`
}

type Metadata struct {
	RunID          string    `json:"run_id"`
	CorpusSHA256   string    `json:"corpus_sha256"`
	PromptSHA256   string    `json:"prompt_sha256"`
	Commit         string    `json:"commit"`
	SpecRevision   string    `json:"spec_revision"`
	Model          string    `json:"model"`
	ModelVersion   string    `json:"model_version"`
	Settings       string    `json:"settings"`
	Adapter        string    `json:"adapter"`
	Clock          time.Time `json:"clock"`
	GraderRevision string    `json:"grader_revision"`
}

// Plan is written before the first model call and is the independent pinned
// contract used to verify raw rows during analysis.
type Plan struct {
	Metadata        Metadata `json:"metadata"`
	SpecSHA256      string   `json:"spec_sha256"`
	AdapterSHA256   string   `json:"adapter_sha256"`
	GoCLISHA256     string   `json:"go_cli_sha256,omitempty"`
	ToolkitMode     string   `json:"toolkit_mode"`
	ModelToolAccess string   `json:"model_tool_access"`
	Exploratory     bool     `json:"exploratory"`
	Repeats         int      `json:"repeats"`
	CaseCount       int      `json:"case_count"`
	MaxTrials       int      `json:"max_trials"`
	MaxSeconds      int      `json:"max_seconds"`
	MaxTokens       int      `json:"max_tokens"`
	MaxCostUSD      float64  `json:"max_cost_usd"`
	Unpriced        bool     `json:"unpriced"`
}

type Row struct {
	Metadata        Metadata    `json:"metadata"`
	CaseID          string      `json:"case_id"`
	Arm             string      `json:"arm"`
	Repeat          int         `json:"repeat"`
	Observation     Observation `json:"observation"`
	ToolkitEvidence []Artifact  `json:"toolkit_evidence,omitempty"`
	TrialElapsedMS  int64       `json:"trial_elapsed_ms"`
	Failure         string      `json:"failure,omitempty"`
	Retries         int         `json:"retries"`
	Verdict         string      `json:"verdict"`
}

const (
	Correct            = "correct"
	Stale              = "stale"
	Wrong              = "wrong"
	Refusal            = "refusal"
	Ungradable         = "ungradable"
	OperationalFailure = "operational_failure"
	Control            = "control"
	Treatment          = "treatment"
)
