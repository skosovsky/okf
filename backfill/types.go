// Package backfill extracts frozen Git evidence and publishes reviewed draft
// concepts through the OKF store. Interpretation is supplied by a caller.
package backfill

import "github.com/skosovsky/okf/store"

const SchemaVersion = "okf-backfill/v1"
const ExtractorVersion = "git-extractor/v1"

type Config struct {
	Repository   string   `json:"repository"`
	Base         string   `json:"base"`
	Head         string   `json:"head"`
	FirstParent  bool     `json:"first_parent"`
	MaxDiffBytes int      `json:"max_diff_bytes"`
	SkipPaths    []string `json:"skip_paths"`
}

type File struct {
	OldPath       string `json:"old_path,omitempty"`
	Path          string `json:"path"`
	Status        string `json:"status"`
	Added         *int   `json:"added,omitempty"`
	Deleted       *int   `json:"deleted,omitempty"`
	Binary        bool   `json:"binary"`
	SkippedReason string `json:"skipped_reason,omitempty"`
}

type Event struct {
	ID            string   `json:"id"`
	Commit        string   `json:"commit"`
	Parents       []string `json:"parents"`
	Subject       string   `json:"subject"`
	Body          string   `json:"body"`
	Author        string   `json:"author"`
	AuthorTime    string   `json:"author_time"`
	CommitterTime string   `json:"committer_time"`
	Files         []File   `json:"files"`
	Diff          string   `json:"diff"`
	DiffSHA256    string   `json:"diff_sha256"`
	DiffBytes     int64    `json:"diff_bytes"`
	DiffTruncated bool     `json:"diff_truncated"`
}

type Manifest struct {
	SchemaVersion    string  `json:"schema_version"`
	ExtractorVersion string  `json:"extractor_version"`
	Config           Config  `json:"config"`
	Repository       string  `json:"repository"`
	Base             string  `json:"base"`
	Head             string  `json:"head"`
	ConfigSHA256     string  `json:"config_sha256"`
	EventsSHA256     string  `json:"events_sha256"`
	Events           []Event `json:"events"`
}

// EvidenceRef binds a proposed claim to an extracted event and file. A line
// range is optional because a truncated diff may not contain the complete file.
type EvidenceRef struct {
	EventID    string `json:"event_id"`
	Path       string `json:"path,omitempty"`
	DiffSHA256 string `json:"diff_sha256"`
}

type Decision string

const (
	DecisionConsidered Decision = "considered"
	DecisionRejected   Decision = "rejected"
)

type Analysis struct {
	SchemaVersion string        `json:"schema_version"`
	EventsSHA256  string        `json:"events_sha256"`
	PromptVersion string        `json:"prompt_version"`
	ProducedAt    string        `json:"produced_at"`
	Usage         *Usage        `json:"usage,omitempty"`
	Results       []EventResult `json:"results"`
	Candidates    []Candidate   `json:"candidates"`
}

type EventResult struct {
	EventID      string   `json:"event_id"`
	Decision     Decision `json:"decision"`
	Reason       string   `json:"reason,omitempty"`
	CandidateIDs []string `json:"candidate_ids,omitempty"`
}

// Candidate is an entity-level desired state. It is not a commit summary.
// Body must describe current claims; History records superseded claims.
type Candidate struct {
	ID                 string        `json:"id"`
	ConceptID          string        `json:"concept_id"`
	Title              string        `json:"title"`
	Description        string        `json:"description"`
	Body               string        `json:"body"`
	History            []HistoryNote `json:"history,omitempty"`
	Evidence           []EvidenceRef `json:"evidence"`
	Supersedes         []string      `json:"supersedes,omitempty"`
	UnresolvedConflict string        `json:"unresolved_conflict,omitempty"`
	EvidenceIncomplete bool          `json:"evidence_incomplete,omitempty"`
}

type HistoryNote struct {
	EventID string `json:"event_id"`
	Text    string `json:"text"`
}

type Checkpoint struct {
	SchemaVersion    string                `json:"schema_version"`
	ExtractorVersion string                `json:"extractor_version"`
	ConfigSHA256     string                `json:"config_sha256"`
	EventsSHA256     string                `json:"events_sha256"`
	AnalysisSHA256   string                `json:"analysis_sha256"`
	PlanSHA256       string                `json:"plan_sha256"`
	PromptVersion    string                `json:"prompt_version"`
	NextCandidate    int                   `json:"next_candidate"`
	BaseRevision     store.Revision        `json:"base_revision"`
	Receipts         []store.CommitReceipt `json:"receipts"`
}

type Coverage struct {
	IncludedEvents      int            `json:"included_events"`
	ConsideredEvents    int            `json:"considered_events"`
	RejectedEvents      int            `json:"rejected_events"`
	MissingEvents       []string       `json:"missing_events"`
	UnresolvedConflicts []string       `json:"unresolved_conflicts"`
	TruncatedEvents     []string       `json:"truncated_events"`
	SkippedFiles        map[string]int `json:"skipped_files"`
	Usage               *Usage         `json:"usage,omitempty"`
	Limitations         []string       `json:"limitations"`
}

type Usage struct {
	InputTokens  int64   `json:"input_tokens,omitempty"`
	OutputTokens int64   `json:"output_tokens,omitempty"`
	CostUSD      float64 `json:"cost_usd,omitempty"`
}
