package okfcli

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
)

func TestParseTextAndJSONTypedProjectionGoldenParity(t *testing.T) {
	// Arrange.
	usageCount := uint64(7)
	stale := false
	projection := parseResponse{
		File: "rich.md",
		VersionDTO: VersionDTO{
			Declared:      "0.2",
			Effective:     "0.2",
			Source:        "declared",
			Compatibility: "native",
		},
		Type:        "Attested Computation",
		Title:       "Rich projection",
		Description: "Every typed value",
		Generated:   &generationDTO{By: "process:build", At: "2026-07-01T09:00:00Z"},
		GeneratedAudit: generationAuditDTO{
			Family: semanticFamilyAuditDTO{
				Present: true, Shape: "mapping", ShapeValid: true,
			},
			Mapping: true, Valid: true, HasValue: true,
			By: scalarAuditDTO{
				Present: true, Valid: true, Raw: "process:build", Value: "process:build",
			},
			At: temporalAuditDTO{State: "valid", Raw: "2026-07-01T09:00:00Z"},
		},
		EffectiveContentChangeTime: "2026-07-01T09:00:00Z",
		Verified: []verificationDTO{
			{By: "process:ci", At: "2026-07-02T09:00:00Z"},
			{By: "human:reviewer", At: "2026-07-03T09:00:00Z"},
		},
		VerifiedAudit: verificationsAuditDTO{
			Family: semanticFamilyAuditDTO{
				Present: true, Shape: "sequence", ShapeValid: true,
			},
			Entries: []verificationAuditDTO{
				{
					RawBy: "process:ci", RawAt: "2026-07-02T09:00:00Z", Valid: true,
					At:    temporalAuditDTO{State: "valid", Raw: "2026-07-02T09:00:00Z"},
					Value: &verificationDTO{By: "process:ci", At: "2026-07-02T09:00:00Z"},
				},
				{
					RawBy: "human:reviewer", RawAt: "2026-07-03T09:00:00Z", Valid: true,
					At:    temporalAuditDTO{State: "valid", Raw: "2026-07-03T09:00:00Z"},
					Value: &verificationDTO{By: "human:reviewer", At: "2026-07-03T09:00:00Z"},
				},
			},
		},
		TrustTier:     "human-reviewed",
		Status:        "stable",
		StatusRaw:     "stable",
		StatusPresent: true,
		StatusValid:   true,
		StaleAfter:    "2027-01-01",
		AsOf:          "2026-07-29",
		Stale:         &stale,
		LifecycleAudit: lifecycleAuditDTO{
			StatusFamily: semanticFamilyAuditDTO{
				Present: true, Shape: "scalar", ShapeValid: true, Raw: "stable",
			},
			Status: statusAuditDTO{
				Present: true, Valid: true, Raw: "stable", Effective: "stable",
			},
			StaleAfter: temporalAuditDTO{State: "valid", Raw: "2027-01-01"},
		},
		Sources: []sourceDTO{{
			ID:           "policy",
			Resource:     "https://example.test/policy",
			Title:        "Policy",
			Author:       "human:owner",
			UsageCount:   &usageCount,
			LastModified: "2026-06-30",
			UsageWindow: &usageWindowDTO{
				From: "2026-06-01",
				To:   "2026-06-30",
			},
			EffectiveUsageWindow: &usageWindowDTO{
				From: "2026-05-01",
				To:   "2026-07-31",
			},
		}},
		UsageWindow: &usageWindowDTO{
			From: "2026-01-01",
			To:   "2026-12-31",
		},
		UsageWindowAudit: usageWindowAuditDTO{
			Family: func() *semanticFamilyAuditDTO {
				value := semanticFamilyAuditDTO{Present: true, Shape: "mapping", ShapeValid: true}
				return &value
			}(),
			Present: true, Mapping: true, Valid: true, HasValue: true,
			From:  temporalAuditDTO{State: "valid", Raw: "2026-01-01"},
			To:    temporalAuditDTO{State: "valid", Raw: "2026-12-31"},
			Value: &usageWindowDTO{From: "2026-01-01", To: "2026-12-31"},
		},
		SourcesAudit: sourcesAuditDTO{
			Family: semanticFamilyAuditDTO{
				Present: true, Shape: "sequence", ShapeValid: true,
			},
			Entries: []sourceAuditDTO{{
				Present: true, Mapping: true, Valid: true, HasValue: true,
				ID: scalarAuditDTO{
					Present: true, Valid: true, Raw: "policy", Value: "policy",
				},
				Resource: scalarAuditDTO{
					Present: true, Valid: true,
					Raw: "https://example.test/policy", Value: "https://example.test/policy",
				},
				Title: scalarAuditDTO{
					Present: true, Valid: true, Raw: "Policy", Value: "Policy",
				},
				Author: scalarAuditDTO{
					Present: true, Valid: true, Raw: "human:owner", Value: "human:owner",
				},
				UsageCount: uint64AuditDTO{Present: true, Valid: true, Raw: "7", Value: 7},
				LastModified: temporalAuditDTO{
					State: "valid", Raw: "2026-06-30",
				},
				UsageWindow: usageWindowAuditDTO{
					Present: true, Mapping: true, Valid: true, HasValue: true,
					From:  temporalAuditDTO{State: "valid", Raw: "2026-06-01"},
					To:    temporalAuditDTO{State: "valid", Raw: "2026-06-30"},
					Value: &usageWindowDTO{From: "2026-06-01", To: "2026-06-30"},
				},
				Value: &sourceDTO{
					ID: "policy", Resource: "https://example.test/policy",
					Title: "Policy", Author: "human:owner",
					UsageCount: &usageCount, LastModified: "2026-06-30",
					UsageWindow: &usageWindowDTO{From: "2026-06-01", To: "2026-06-30"},
				},
			}},
		},
		Attributions: []attributionDTO{{
			ID: "policy",
			Sources: []sourceDTO{{
				ID:       "policy",
				Resource: "https://example.test/policy",
				Title:    "Policy",
				Author:   "human:owner",
			}},
			References: []footnoteReferenceDTO{{
				ID:  "policy",
				Raw: "[^policy]",
			}},
			Definitions: []footnoteDefinitionDTO{{
				ID:   "policy",
				Text: "Policy definition.",
				Raw:  "[^policy]: Policy definition.",
			}},
		}},
		AttributionsAudit: attributionAuditDTO{
			Entries: 1, Sources: 1, References: 1, Definitions: 1,
		},
		Computation: &computationDTO{
			Runtime: "sql",
			Parameters: []parameterDTO{
				{Name: "year", Type: "integer", Required: true},
				{Name: "region", Type: "string", Required: false},
			},
			Computation: "references/query.sql",
			Executor: &executorDTO{
				Resource: "executors/sql.md",
				Receipt:  []string{"query_id", "rows"},
			},
			Attester: &attesterDTO{Resource: "attesters/sql.md"},
		},
		ComputationAudit: computationAuditDTO{
			TypeMatches: true, ContractPresent: true, Valid: true, Mode: "file",
			Contract: computationContractAuditDTO{
				Present: true, Valid: true,
				Runtime: scalarAuditDTO{
					Present: true, Valid: true, Raw: "sql", Value: "sql",
				},
				ParametersPresent: true,
				ParametersValid:   true,
				ParametersFamily: semanticFamilyAuditDTO{
					Present: true, Shape: "sequence", ShapeValid: true,
				},
				Parameters: []parameterAuditDTO{
					{
						Present: true, Mapping: true, Valid: true, HasValue: true,
						Name: scalarAuditDTO{
							Present: true, Valid: true, Raw: "year", Value: "year",
						},
						Type: scalarAuditDTO{
							Present: true, Valid: true, Raw: "integer", Value: "integer",
						},
						Required: booleanAuditDTO{
							Present: true, Valid: true, Raw: "true", Value: true,
						},
						Value: &parameterDTO{Name: "year", Type: "integer", Required: true},
					},
					{
						Present: true, Mapping: true, Valid: true, HasValue: true,
						Name: scalarAuditDTO{
							Present: true, Valid: true, Raw: "region", Value: "region",
						},
						Type: scalarAuditDTO{
							Present: true, Valid: true, Raw: "string", Value: "string",
						},
						Required: booleanAuditDTO{
							Present: true, Valid: true, Raw: "false", Value: false,
						},
						Value: &parameterDTO{Name: "region", Type: "string", Required: false},
					},
				},
				Computation: scalarAuditDTO{
					Present: true, Valid: true,
					Raw: "references/query.sql", Value: "references/query.sql",
				},
				Executor: executorAuditDTO{
					Present: true, Mapping: true, Valid: true, HasValue: true,
					Resource: scalarAuditDTO{
						Present: true, Valid: true,
						Raw: "executors/sql.md", Value: "executors/sql.md",
					},
					Receipt: stringSequenceAuditDTO{
						Present: true, Valid: true,
						Items: []scalarAuditDTO{
							{Present: true, Valid: true, Raw: "query_id", Value: "query_id"},
							{Present: true, Valid: true, Raw: "rows", Value: "rows"},
						},
						Values: []string{"query_id", "rows"},
					},
					Value: &executorDTO{
						Resource: "executors/sql.md", Receipt: []string{"query_id", "rows"},
					},
				},
				ExecutorFamily: semanticFamilyAuditDTO{
					Present: true, Shape: "mapping", ShapeValid: true,
				},
				Attester: attesterAuditDTO{
					Present: true, Mapping: true, Valid: true, HasValue: true,
					Resource: scalarAuditDTO{
						Present: true, Valid: true,
						Raw: "attesters/sql.md", Value: "attesters/sql.md",
					},
					Value: &attesterDTO{Resource: "attesters/sql.md"},
				},
				AttesterFamily: semanticFamilyAuditDTO{
					Present: true, Shape: "mapping", ShapeValid: true,
				},
				Value: &computationDTO{
					Runtime: "sql",
					Parameters: []parameterDTO{
						{Name: "year", Type: "integer", Required: true},
						{Name: "region", Type: "string", Required: false},
					},
					Computation: "references/query.sql",
					Executor: &executorDTO{
						Resource: "executors/sql.md", Receipt: []string{"query_id", "rows"},
					},
					Attester: &attesterDTO{Resource: "attesters/sql.md"},
				},
			},
			FenceSpans: []sourceSpanDTO{},
		},
		Conformant: true,
		BodyBytes:  17,
		Links: []linkDTO{{
			Kind:   "inline",
			Text:   "Related",
			Target: "related.md",
		}},
		LegacyCitations: []citationDTO{{
			Number: 1,
			Text:   "Legacy source",
			Target: "https://legacy.example",
			Raw:    "[Legacy source](https://legacy.example)",
		}},
	}
	const wantText = `file: "rich.md"
declared version: "0.2"
effective version: "0.2" ("declared", "native")
type: "Attested Computation"
title: "Rich projection"
description: "Every typed value"
has non-empty string ` + "`type`" + `: true
body: 17 byte(s)
generated: by="process:build" at="2026-07-01T09:00:00Z"
effective content change time: "2026-07-01T09:00:00Z"
generated audit: present=true ambiguous=false shape="mapping" shape_valid=true raw="" mapping=true valid=true has_value=true
  by: present=true valid=true raw="process:build" value="process:build"
  at: state="valid" raw="2026-07-01T09:00:00Z"
legacy fallback: false
trust: "human-reviewed" (2 verification(s))
verified (2):
  [0] by="process:ci" at="2026-07-02T09:00:00Z"
  [1] by="human:reviewer" at="2026-07-03T09:00:00Z"
verified audit: present=true ambiguous=false shape="sequence" shape_valid=true raw="" entries=2
  [0] valid=true raw_by="process:ci" raw_at="2026-07-02T09:00:00Z" at_state="valid" typed_by="process:ci" typed_at="2026-07-02T09:00:00Z"
  [1] valid=true raw_by="human:reviewer" raw_at="2026-07-03T09:00:00Z" at_state="valid" typed_by="human:reviewer" typed_at="2026-07-03T09:00:00Z"
status: "stable"
status metadata: present=true valid=true raw="stable"
stale_after: "2027-01-01"
stale at "2026-07-29": false
lifecycle audit: status_present=true status_valid=true status_raw="stable" status_effective="stable" status_shape="scalar" status_shape_valid=true status_ambiguous=false status_family_raw="stable"
  stale_after: state="valid" raw="2027-01-01"
usage window: from="2026-01-01" to="2026-12-31"
usage window audit family: present=true ambiguous=false shape="mapping" shape_valid=true raw=""
usage window audit: present=true mapping=true valid=true has_value=true
  from: state="valid" raw="2026-01-01"
  to: state="valid" raw="2026-12-31"
sources: 1
  [0] id="policy" resource="https://example.test/policy" title="Policy" author="human:owner"
    usage_count: 7
    last_modified: "2026-06-30"
    usage_window: from="2026-06-01" to="2026-06-30"
    effective_usage_window: from="2026-05-01" to="2026-07-31"
sources audit: present=true ambiguous=false shape="sequence" shape_valid=true raw="" entries=1
  [0] present=true mapping=true valid=true has_value=true
    id: present=true valid=true raw="policy" value="policy"
    resource: present=true valid=true raw="https://example.test/policy" value="https://example.test/policy"
    title: present=true valid=true raw="Policy" value="Policy"
    author: present=true valid=true raw="human:owner" value="human:owner"
    usage_count: present=true valid=true raw="7" value=7
    last_modified: state="valid" raw="2026-06-30"
    usage_window: present=true mapping=true valid=true has_value=true
      from: state="valid" raw="2026-06-01"
      to: state="valid" raw="2026-06-30"
attributions: 1
  [0] id="policy"
    sources: 1
      [0] id="policy" resource="https://example.test/policy" title="Policy" author="human:owner"
    references: 1
      [0] id="policy" raw="[^policy]"
    definitions: 1
      [0] id="policy" text="Policy definition." raw="[^policy]: Policy definition."
attributions audit: entries=1 sources=1 references=1 definitions=1
attested computation: runtime="sql" parameters=2 computation="references/query.sql"
  parameters (2):
    [0] name="year" type="integer" required=true
    [1] name="region" type="string" required=false
  executor: resource="executors/sql.md"
    receipt (2):
      [0] "query_id"
      [1] "rows"
  attester: resource="attesters/sql.md"
attested computation audit: type_matches=true contract_present=true valid=true mode="file" unclosed=false multiple=false conflict=false inline="" fence_spans=0
  contract: present=true valid=true parameters_present=true parameters_valid=true parameters=2
    runtime: present=true valid=true raw="sql" value="sql"
    computation: present=true valid=true raw="references/query.sql" value="references/query.sql"
    parameters family: present=true ambiguous=false shape="sequence" shape_valid=true raw=""
    parameter[0]: present=true mapping=true valid=true has_value=true
      name: present=true valid=true raw="year" value="year"
      type: present=true valid=true raw="integer" value="integer"
      required: present=true valid=true raw="true" value=true
    parameter[1]: present=true mapping=true valid=true has_value=true
      name: present=true valid=true raw="region" value="region"
      type: present=true valid=true raw="string" value="string"
      required: present=true valid=true raw="false" value=false
    executor: present=true mapping=true valid=true has_value=true receipt_present=true receipt_valid=true receipt_items=2
      resource: present=true valid=true raw="executors/sql.md" value="executors/sql.md"
    executor family: present=true ambiguous=false shape="mapping" shape_valid=true raw=""
      receipt[0]: present=true valid=true raw="query_id" value="query_id"
      receipt[1]: present=true valid=true raw="rows" value="rows"
    attester: present=true mapping=true valid=true has_value=true
      resource: present=true valid=true raw="attesters/sql.md" value="attesters/sql.md"
    attester family: present=true ambiguous=false shape="mapping" shape_valid=true raw=""

links (1):
  kind="Inline" text="Related" target="related.md"

citations (1):
  [1] text="Legacy source" target="https://legacy.example" raw="[Legacy source](https://legacy.example)"
`
	var firstText, secondText bytes.Buffer
	var jsonOutput bytes.Buffer

	// Act.
	writeParseText(&firstText, projection)
	writeParseText(&secondText, projection)
	jsonErr := json.NewEncoder(&jsonOutput).Encode(projection)
	var decoded parseResponse
	decodeErr := json.Unmarshal(jsonOutput.Bytes(), &decoded)

	// Assert.
	if jsonErr != nil || decodeErr != nil {
		t.Fatalf("JSON encode/decode errors = %v/%v; payload=%q", jsonErr, decodeErr, jsonOutput.String())
	}
	if !reflect.DeepEqual(decoded, projection) {
		t.Fatalf("JSON projection = %#v, want %#v", decoded, projection)
	}
	if firstText.String() != wantText || secondText.String() != wantText {
		t.Fatalf("parse text golden mismatch\nfirst:\n%s\nsecond:\n%s\nwant:\n%s",
			firstText.String(), secondText.String(), wantText)
	}
	if strings.Contains(firstText.String(), "\r") || !strings.HasSuffix(firstText.String(), "\n") {
		t.Fatalf("parse text line endings are not canonical LF: %q", firstText.String())
	}
}

func TestInfoTextEscapesDocumentAndFilesystemStrings(t *testing.T) {
	// Arrange.
	const hostile = "Ω\n\r\x00\t\u2028end"
	response := infoResponse{
		VersionDTO: VersionDTO{
			Declared: hostile, Effective: hostile, Source: hostile, Compatibility: hostile,
		},
		Bundle: hostile,
		Types:  map[string]int{hostile: 1},
		Lifecycle: lifecycleCounts{
			Other: map[string]int{hostile: 1},
		},
		AsOf: hostile,
	}
	parseErrors := []bundle.ParseError{{Path: hostile, Err: errors.New(hostile)}}
	var stdout bytes.Buffer

	// Act.
	writeInfoText(&stdout, response, parseErrors)
	rendered := stdout.String()

	// Assert.
	if strings.ContainsAny(rendered, "\r\x00\t") ||
		strings.Contains(rendered, "\u2028") ||
		strings.Contains(rendered, "Ω\n") ||
		!strings.Contains(rendered, renderTextString(hostile)) ||
		!strings.HasSuffix(rendered, "\n") {
		t.Fatalf("info text contains an unescaped value or noncanonical ending: %q", rendered)
	}
}

func TestMigrationTextRendererGoldenAuditEvidenceByOutcome(t *testing.T) {
	base := func() migrationReport {
		return migrationReport{
			From:               "0.1",
			RequestedSelector:  "auto",
			DeclarationPresent: true,
			DeclarationValid:   true,
			DeclarationRaw:     "0.1",
			Declared:           "0.1",
			Effective:          "0.1",
			ResolvedSource:     "0.1",
			ResolutionSource:   "declared",
			Compatibility:      "legacy",
			Transition:         "v0.1_to_v0.2",
			To:                 "0.2",
			Actor:              "human:operator",
			Mode:               "dry-run",
			SourceFindings: []migrationSourceFindingDTO{{
				Kind: "legacy_timestamp", Path: "a.md", Start: 10, End: 20,
			}},
		}
	}
	preview := base()
	preview.Base = "sha256:base"
	preview.Result = "sha256:result"
	preview.PlanDigest = "sha256:plan"
	preview.ProofFormatVersion = 2
	preview.ResolutionDigest = "sha256:resolution"
	preview.Reads = []string{"index.md", "a.md"}
	preview.Changes = []migrationChangeDTO{
		{Kind: "write", Path: "a.md", Digest: "sha256:file"},
		{Kind: "rename", Path: "new.md", From: "old.md"},
	}
	applied := preview
	applied.Mode = "apply"
	applied.Applied = true
	applied.Receipt = &migrationReceiptDTO{
		FormatVersion:  1,
		ChangeSetID:    "okf-migrate-v01-v02",
		IdempotencyKey: "okf-migrate-sha256:plan",
		RequestDigest:  "sha256:request",
		BaseRevision:   "sha256:base",
		ResultRevision: "sha256:result",
		CommitTime:     "2026-07-29T10:00:00Z",
		ChangedRefs:    []string{"a", "a#x"},
		ChangedFiles: []migrationChangedFileDTO{
			{Kind: "write", Path: "a.md"},
			{Kind: "rename", Path: "new.md", From: "old.md"},
		},
	}
	noop := base()
	noop.SourceFindings = []migrationSourceFindingDTO{}
	noop.Noop = true
	blocked := base()
	blocked.DeclarationRaw = "0.1\r\n"
	blocked.SourceFindings = []migrationSourceFindingDTO{}
	blocked.Blockers = []migrationBlockerDTO{{
		Code: "migration_blocked", Path: "bad.md", Start: 3, End: 9,
		Message: "first\r\nsecond",
	}}
	blocked.ManualActions = []migrationManualActionDTO{{
		Code: "fix_source", Path: "bad.md", Message: "repair declaration",
	}}

	tests := []struct {
		name     string
		report   migrationReport
		code     int
		wantCode int
		want     string
	}{
		{
			name:   "preview",
			report: preview,
			want: `Migration: "0.1" -> "0.2"
Source:
  requested selector: "auto"
  declaration: present=true valid=true raw="0.1" declared="0.1"
  resolution: effective="0.1" resolved="0.1" source="declared" compatibility="legacy" transition="v0.1_to_v0.2" future=false
Source findings (1):
  kind="legacy_timestamp" path="a.md" start=10 end=20
Actor: "human:operator"
Mode: "dry-run"
Base revision: "sha256:base"
Result revision: "sha256:result"
Plan digest: "sha256:plan"
Proof: format_version=2 resolution_digest="sha256:resolution"
Reads (2):
  [0] "index.md"
  [1] "a.md"
Changes (2):
  kind="write" path="a.md" from="" digest="sha256:file"
  kind="rename" path="new.md" from="old.md" digest=""
Blockers (0):
Manual actions (0):
Cleanup diagnostics (0):
State: outcome="preview" noop=false applied=false
Receipt: (absent)
Result: "PREVIEW"
`,
		},
		{
			name:   "applied",
			report: applied,
			want: `Migration: "0.1" -> "0.2"
Source:
  requested selector: "auto"
  declaration: present=true valid=true raw="0.1" declared="0.1"
  resolution: effective="0.1" resolved="0.1" source="declared" compatibility="legacy" transition="v0.1_to_v0.2" future=false
Source findings (1):
  kind="legacy_timestamp" path="a.md" start=10 end=20
Actor: "human:operator"
Mode: "apply"
Base revision: "sha256:base"
Result revision: "sha256:result"
Plan digest: "sha256:plan"
Proof: format_version=2 resolution_digest="sha256:resolution"
Reads (2):
  [0] "index.md"
  [1] "a.md"
Changes (2):
  kind="write" path="a.md" from="" digest="sha256:file"
  kind="rename" path="new.md" from="old.md" digest=""
Blockers (0):
Manual actions (0):
Cleanup diagnostics (0):
State: outcome="applied" noop=false applied=true
Receipt:
  format_version: 1
  change_set_id: "okf-migrate-v01-v02"
  idempotency_key: "okf-migrate-sha256:plan"
  request_digest: "sha256:request"
  base_revision: "sha256:base"
  result_revision: "sha256:result"
  commit_time: "2026-07-29T10:00:00Z"
  changed_refs (2):
    [0] "a"
    [1] "a#x"
  changed_files (2):
    kind="write" path="a.md" from=""
    kind="rename" path="new.md" from="old.md"
Result: "APPLIED"
`,
		},
		{
			name:   "noop",
			report: noop,
			want: `Migration: "0.1" -> "0.2"
Source:
  requested selector: "auto"
  declaration: present=true valid=true raw="0.1" declared="0.1"
  resolution: effective="0.1" resolved="0.1" source="declared" compatibility="legacy" transition="v0.1_to_v0.2" future=false
Source findings (0):
Actor: "human:operator"
Mode: "dry-run"
Base revision: ""
Result revision: ""
Plan digest: ""
Proof: format_version=0 resolution_digest=""
Reads (0):
Changes (0):
Blockers (0):
Manual actions (0):
Cleanup diagnostics (0):
State: outcome="noop" noop=true applied=false
Receipt: (absent)
Result: "NOOP"
`,
		},
		{
			name:     "blocked escapes CRLF",
			report:   blocked,
			code:     1,
			wantCode: 1,
			want: `Migration: "0.1" -> "0.2"
Source:
  requested selector: "auto"
  declaration: present=true valid=true raw="0.1\r\n" declared="0.1"
  resolution: effective="0.1" resolved="0.1" source="declared" compatibility="legacy" transition="v0.1_to_v0.2" future=false
Source findings (0):
Actor: "human:operator"
Mode: "dry-run"
Base revision: ""
Result revision: ""
Plan digest: ""
Proof: format_version=0 resolution_digest=""
Reads (0):
Changes (0):
Blockers (1):
  code="migration_blocked" path="bad.md" start=3 end=9 message="first\r\nsecond"
Manual actions (1):
  code="fix_source" path="bad.md" message="repair declaration"
Cleanup diagnostics (0):
State: outcome="blocked" noop=false applied=false
Receipt: (absent)
Result: "BLOCKED"
`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			var first, second bytes.Buffer

			// Act.
			firstCode, firstErr := renderMigrationReport(&first, "text", test.report, test.code)
			secondCode, secondErr := renderMigrationReport(&second, "text", test.report, test.code)

			// Assert.
			if firstErr != nil || secondErr != nil ||
				firstCode != test.wantCode || secondCode != test.wantCode {
				t.Fatalf("render codes/errors = %d/%d/%v/%v, want %d/nil",
					firstCode, secondCode, firstErr, secondErr, test.wantCode)
			}
			if first.String() != test.want || second.String() != test.want {
				t.Fatalf("migration text golden mismatch\nfirst:\n%s\nsecond:\n%s\nwant:\n%s",
					first.String(), second.String(), test.want)
			}
			if strings.Contains(first.String(), "\r") || !strings.HasSuffix(first.String(), "\n") {
				t.Fatalf("migration text line endings are not canonical LF: %q", first.String())
			}
		})
	}
}

func TestMigrationTextAndJSONRenderEveryExternalStringWithExactQuotingParity(t *testing.T) {
	// Arrange.
	const hostile = "Ω\n\r\x00\x01\t\x1f\x7f\u2028\u2029end"
	report := migrationReport{
		From:               hostile,
		RequestedSelector:  hostile,
		DeclarationPresent: true,
		DeclarationValid:   true,
		DeclarationRaw:     hostile,
		Declared:           hostile,
		Effective:          hostile,
		ResolvedSource:     hostile,
		ResolutionSource:   hostile,
		Compatibility:      hostile,
		Transition:         hostile,
		SourceFindings: []migrationSourceFindingDTO{{
			Kind: hostile, Path: hostile, Start: 1, End: 2,
		}},
		To:               hostile,
		Actor:            hostile,
		Mode:             hostile,
		Base:             hostile,
		Result:           hostile,
		PlanDigest:       hostile,
		ResolutionDigest: hostile,
		Reads:            []string{hostile},
		Changes: []migrationChangeDTO{{
			Kind: hostile, Path: hostile, From: hostile, Digest: hostile,
		}},
		Blockers: []migrationBlockerDTO{{
			Code: hostile, Path: hostile, Start: 3, End: 4, Message: hostile,
		}},
		ManualActions: []migrationManualActionDTO{{
			Code: hostile, Path: hostile, Message: hostile,
		}},
		CleanupDiagnostics: []migrationCleanupDiagnosticDTO{},
		Receipt: &migrationReceiptDTO{
			ChangeSetID:    hostile,
			IdempotencyKey: hostile,
			RequestDigest:  hostile,
			BaseRevision:   hostile,
			ResultRevision: hostile,
			CommitTime:     hostile,
			ChangedRefs:    []string{hostile},
			ChangedFiles: []migrationChangedFileDTO{{
				Kind: hostile, Path: hostile, From: hostile,
			}},
		},
	}
	var firstText, secondText, firstJSON, secondJSON bytes.Buffer

	// Act.
	firstTextCode, firstTextErr := renderMigrationReport(&firstText, "text", report, 1)
	secondTextCode, secondTextErr := renderMigrationReport(&secondText, "text", report, 1)
	firstJSONCode, firstJSONErr := renderMigrationReport(&firstJSON, "json", report, 1)
	secondJSONCode, secondJSONErr := renderMigrationReport(&secondJSON, "json", report, 1)

	// Assert.
	if firstTextErr != nil || secondTextErr != nil || firstJSONErr != nil || secondJSONErr != nil ||
		firstTextCode != 1 || secondTextCode != 1 || firstJSONCode != 1 || secondJSONCode != 1 {
		t.Fatalf("render codes/errors = text %d/%d/%v/%v json %d/%d/%v/%v, want all 1/nil",
			firstTextCode, secondTextCode, firstTextErr, secondTextErr,
			firstJSONCode, secondJSONCode, firstJSONErr, secondJSONErr)
	}
	if firstText.String() != secondText.String() || firstJSON.String() != secondJSON.String() {
		t.Fatal("migration renderers are not deterministic")
	}
	const renderedExternalStringCount = 39
	if got := strings.Count(firstText.String(), renderTextString(hostile)); got != renderedExternalStringCount {
		t.Fatalf("text rendered hostile strings = %d, want %d; output=%q",
			got, renderedExternalStringCount, firstText.String())
	}
	if strings.ContainsAny(firstText.String(), "\r\x00\x01\t\x1f\x7f\u2028\u2029") ||
		strings.Contains(firstText.String(), "Ω\n") ||
		strings.Count(firstText.String(), "\n") != 36 ||
		!strings.HasSuffix(firstText.String(), "\n") {
		t.Fatalf("migration text has an injected control or noncanonical LF framing: %q", firstText.String())
	}

	encodedHostile, err := json.Marshal(hostile)
	if err != nil {
		t.Fatalf("json.Marshal(hostile) error = %v", err)
	}
	if got := bytes.Count(firstJSON.Bytes(), encodedHostile); got != renderedExternalStringCount {
		t.Fatalf("JSON encoded hostile strings = %d, want %d; output=%q",
			got, renderedExternalStringCount, firstJSON.String())
	}
	if bytes.Count(firstJSON.Bytes(), []byte{'\n'}) != 1 ||
		bytes.ContainsAny(firstJSON.Bytes(), "\r\x00\x01\t\x1f") ||
		bytes.Contains(firstJSON.Bytes(), []byte("\u2028")) ||
		bytes.Contains(firstJSON.Bytes(), []byte("\u2029")) ||
		firstJSON.Bytes()[firstJSON.Len()-1] != '\n' {
		t.Fatalf("migration JSON has unsafe string framing: %q", firstJSON.String())
	}
	var decoded migrationReport
	if err := json.Unmarshal(firstJSON.Bytes(), &decoded); err != nil {
		t.Fatalf("json.Unmarshal(migration report) error = %v; output=%q", err, firstJSON.String())
	}
	wantDecoded := report
	wantDecoded.Outcome = "blocked"
	if !reflect.DeepEqual(decoded, wantDecoded) {
		t.Fatalf("migration JSON semantic parity mismatch:\ngot:  %#v\nwant: %#v", decoded, wantDecoded)
	}
}
