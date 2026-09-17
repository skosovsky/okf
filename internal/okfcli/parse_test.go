package okfcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
)

func TestParseLegacyFallbackAggregateCoversEveryActiveFamily(t *testing.T) {
	tests := []struct {
		name                string
		frontmatter         string
		body                string
		wantFallback        bool
		wantTime            string
		wantCitations       int
		wantTimestampMarker bool
	}{
		{
			name:          "citations only",
			body:          "Claim [1].\n\n# Citations\n\n[1] https://example.test\n",
			wantFallback:  true,
			wantCitations: 1,
		},
		{
			name:                "timestamp only",
			frontmatter:         "timestamp: 2026-05-28T22:53:05Z\n",
			body:                "Body.\n",
			wantFallback:        true,
			wantTime:            "2026-05-28T22:53:05Z",
			wantTimestampMarker: true,
		},
		{
			name: "none",
			body: "Body.\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			path := filepath.Join(t.TempDir(), "concept.md")
			writeFixtureFile(t, path, "---\ntype: Note\n"+test.frontmatter+"---\n\n"+test.body)
			var jsonStdout, jsonStderr bytes.Buffer
			var textStdout, textStderr bytes.Buffer

			// Act.
			jsonCode := Run([]string{"parse", path, "--spec", "0.2", "--json"}, &jsonStdout, &jsonStderr)
			textCode := Run([]string{"parse", path, "--spec", "0.2"}, &textStdout, &textStderr)
			var response parseResponse
			decodeErr := json.Unmarshal(jsonStdout.Bytes(), &response)

			// Assert.
			if jsonCode != 0 || textCode != 0 ||
				jsonStderr.Len() != 0 || textStderr.Len() != 0 || decodeErr != nil {
				t.Fatalf("parse code/stderr/decode = %d/%d/%q/%q/%v; JSON=%q",
					jsonCode, textCode, jsonStderr.String(), textStderr.String(), decodeErr, jsonStdout.String())
			}
			if response.LegacyFallback != test.wantFallback ||
				response.EffectiveContentChangeTime != test.wantTime ||
				len(response.LegacyCitations) != test.wantCitations {
				t.Fatalf("legacy fallback/time/citations = %t/%q/%d, want %t/%q/%d; response=%#v",
					response.LegacyFallback, response.EffectiveContentChangeTime, len(response.LegacyCitations),
					test.wantFallback, test.wantTime, test.wantCitations, response)
			}
			if marker := "legacy fallback: " + fmt.Sprint(test.wantFallback) + "\n"; !strings.Contains(textStdout.String(), marker) {
				t.Fatalf("parse text = %q, missing aggregate marker %q", textStdout.String(), marker)
			}
			hasTimestampMarker := strings.Contains(textStdout.String(), "generated: legacy timestamp fallback")
			if hasTimestampMarker != test.wantTimestampMarker {
				t.Fatalf("parse text timestamp marker = %t, want %t; text=%q",
					hasTimestampMarker, test.wantTimestampMarker, textStdout.String())
			}
		})
	}
}

func TestParseAuditPreservesAbsentEmptyAndInvalidOptionalFamilies(t *testing.T) {
	tests := []struct {
		name        string
		frontmatter string
		assert      func(*testing.T, parseResponse)
	}{
		{
			name:        "absent",
			frontmatter: "type: Note\n",
			assert: func(t *testing.T, response parseResponse) {
				t.Helper()
				if response.GeneratedAudit.Family.Present ||
					response.GeneratedAudit.Family.Shape != "absent" ||
					!response.GeneratedAudit.Family.ShapeValid ||
					response.VerifiedAudit.Family.Present ||
					response.SourcesAudit.Family.Present ||
					response.UsageWindowAudit.Present ||
					response.LifecycleAudit.Status.Present ||
					response.LifecycleAudit.StaleAfter.State != string(bundle.TemporalAbsent) ||
					response.ComputationAudit.Contract.Present {
					t.Fatalf("absent audit collapsed or fabricated: %#v", response)
				}
			},
		},
		{
			name: "present empty",
			frontmatter: "type: Attested Computation\n" +
				"generated: {}\n" +
				"verified: []\n" +
				"sources: []\n" +
				"usage_window: {}\n" +
				"parameters: []\n",
			assert: func(t *testing.T, response parseResponse) {
				t.Helper()
				if !response.GeneratedAudit.Family.Present ||
					response.GeneratedAudit.Family.Shape != "mapping" ||
					!response.GeneratedAudit.Family.ShapeValid ||
					!response.GeneratedAudit.Mapping ||
					response.GeneratedAudit.HasValue ||
					!response.VerifiedAudit.Family.Present ||
					response.VerifiedAudit.Family.Shape != "sequence" ||
					!response.VerifiedAudit.Family.ShapeValid ||
					len(response.VerifiedAudit.Entries) != 0 ||
					!response.SourcesAudit.Family.Present ||
					response.SourcesAudit.Family.Shape != "sequence" ||
					!response.SourcesAudit.Family.ShapeValid ||
					len(response.SourcesAudit.Entries) != 0 ||
					!response.UsageWindowAudit.Present ||
					!response.UsageWindowAudit.Mapping ||
					response.UsageWindowAudit.HasValue ||
					!response.ComputationAudit.ContractPresent ||
					!response.ComputationAudit.Contract.ParametersPresent ||
					!response.ComputationAudit.Contract.ParametersValid ||
					len(response.ComputationAudit.Contract.Parameters) != 0 {
					t.Fatalf("present-empty audit collapsed or fabricated: %#v", response)
				}
			},
		},
		{
			name: "present invalid with raw evidence",
			frontmatter: "type: Attested Computation\n" +
				"generated: wrong\n" +
				"verified:\n" +
				"  - by: not-an-actor\n" +
				"    at: not-an-instant\n" +
				"sources:\n" +
				"  - resource: 42\n" +
				"    usage_count: -1\n" +
				"    last_modified: not-a-date\n" +
				"    usage_window: wrong\n" +
				"usage_window: wrong\n" +
				"status: []\n" +
				"stale_after: not-a-date\n" +
				"runtime: 42\n" +
				"parameters: [wrong]\n" +
				"computation: 42\n" +
				"executor: wrong\n" +
				"attester: []\n",
			assert: func(t *testing.T, response parseResponse) {
				t.Helper()
				source := response.SourcesAudit.Entries
				contract := response.ComputationAudit.Contract
				if response.GeneratedAudit.Family.Shape != "scalar" ||
					response.GeneratedAudit.Family.ShapeValid ||
					response.GeneratedAudit.Family.Raw != "wrong" ||
					response.VerifiedAudit.Family.Shape != "sequence" ||
					len(response.VerifiedAudit.Entries) != 1 ||
					response.VerifiedAudit.Entries[0].Valid ||
					response.VerifiedAudit.Entries[0].RawBy != "not-an-actor" ||
					response.VerifiedAudit.Entries[0].RawAt != "not-an-instant" ||
					response.SourcesAudit.Family.Shape != "sequence" ||
					len(source) != 1 ||
					source[0].Valid ||
					source[0].Resource.Raw != "42" ||
					source[0].UsageCount.Raw != "-1" ||
					source[0].LastModified.Raw != "not-a-date" ||
					source[0].UsageWindow.Mapping ||
					response.UsageWindowAudit.Mapping ||
					response.UsageWindowAudit.Family == nil ||
					response.UsageWindowAudit.Family.Raw != "wrong" ||
					response.LifecycleAudit.Status.Valid ||
					response.LifecycleAudit.StatusFamily.Shape != "sequence" ||
					response.LifecycleAudit.StatusFamily.ShapeValid ||
					response.LifecycleAudit.StaleAfter.Raw != "not-a-date" ||
					!contract.Present ||
					contract.Valid ||
					contract.Runtime.Raw != "42" ||
					!contract.ParametersPresent ||
					contract.ParametersValid ||
					contract.ParametersFamily.Raw != "[wrong]" ||
					len(contract.Parameters) != 1 ||
					contract.Parameters[0].Mapping ||
					contract.Computation.Raw != "42" ||
					contract.Executor.Mapping ||
					contract.ExecutorFamily.Raw != "wrong" ||
					contract.Executor.Value != nil ||
					contract.Attester.Mapping ||
					contract.AttesterFamily.Raw != "[]" ||
					contract.Attester.Value != nil {
					t.Fatalf("present-invalid audit lost evidence: %#v", response)
				}
				if response.Generated != nil || len(response.Verified) != 0 ||
					len(response.Sources) != 0 || response.UsageWindow != nil ||
					response.Computation != nil {
					t.Fatalf("invalid observations fabricated typed values: %#v", response)
				}
			},
		},
		{
			name: "present invalid collection shapes",
			frontmatter: "type: Note\n" +
				"verified: wrong\n" +
				"sources: wrong\n",
			assert: func(t *testing.T, response parseResponse) {
				t.Helper()
				if !response.VerifiedAudit.Family.Present ||
					response.VerifiedAudit.Family.Shape != "scalar" ||
					response.VerifiedAudit.Family.ShapeValid ||
					response.VerifiedAudit.Family.Raw != "wrong" ||
					len(response.VerifiedAudit.Entries) != 1 ||
					response.VerifiedAudit.Entries[0].Valid ||
					!response.SourcesAudit.Family.Present ||
					response.SourcesAudit.Family.Shape != "scalar" ||
					response.SourcesAudit.Family.ShapeValid ||
					response.SourcesAudit.Family.Raw != "wrong" ||
					len(response.SourcesAudit.Entries) != 0 ||
					len(response.Verified) != 0 ||
					len(response.Sources) != 0 {
					t.Fatalf("invalid collection family shape collapsed: %#v", response)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			path := filepath.Join(t.TempDir(), "concept.md")
			writeFixtureFile(t, path, "---\n"+test.frontmatter+"---\n\nBody.\n")
			var stdout, stderr bytes.Buffer

			// Act.
			code := Run([]string{"parse", path, "--spec", "0.2", "--json"}, &stdout, &stderr)
			var response parseResponse
			decodeErr := json.Unmarshal(stdout.Bytes(), &response)

			// Assert.
			if stderr.Len() != 0 || decodeErr != nil {
				t.Fatalf("Run(parse) code/stderr/decode = %d/%q/%v; stdout=%q",
					code, stderr.String(), decodeErr, stdout.String())
			}
			test.assert(t, response)
		})
	}
}

func TestProjectDocumentContextCancellationReturnsNoPartialProjection(t *testing.T) {
	// Arrange.
	document, err := bundle.ParseDocument(
		"---\ntype: Note\nsources: [{id: source, resource: source.md}]\n---\nBody.\n",
	)
	if err != nil {
		t.Fatalf("bundle.ParseDocument() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Act.
	projection, projectionErr := projectDocument(
		ctx,
		"concept.md",
		document,
		bundle.VersionResolution{},
		nil,
	)

	// Assert.
	if !errors.Is(projectionErr, context.Canceled) ||
		!reflect.DeepEqual(projection, parseResponse{}) {
		t.Fatalf("projectDocument(canceled) = (%#v, %v)", projection, projectionErr)
	}
}

func TestParseGeneratedFieldPresenceControlsTimestampFallback(t *testing.T) {
	const (
		legacyTimestamp = "2026-05-28T22:53:05Z"
		generatedAt     = "2026-06-30T10:20:30Z"
	)
	tests := []struct {
		name          string
		generated     string
		wantGenerated *generationDTO
		wantEffective string
	}{
		{
			name:          "missing generated by preserves valid generated at",
			generated:     "generated:\n  at: " + generatedAt + "\n",
			wantGenerated: &generationDTO{At: generatedAt},
			wantEffective: generatedAt,
		},
		{
			name:          "malformed generated by preserves valid generated at",
			generated:     "generated:\n  by: \"not an actor\"\n  at: " + generatedAt + "\n",
			wantGenerated: &generationDTO{At: generatedAt},
			wantEffective: generatedAt,
		},
		{
			name:          "malformed generated at suppresses legacy timestamp",
			generated:     "generated:\n  by: process:test\n  at: not-an-instant\n",
			wantGenerated: &generationDTO{By: "process:test"},
		},
		{
			name:          "missing generated at suppresses legacy timestamp",
			generated:     "generated:\n  by: process:test\n",
			wantGenerated: &generationDTO{By: "process:test"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			path := filepath.Join(t.TempDir(), "concept.md")
			writeFixtureFile(t, path,
				"---\n"+
					"type: Note\n"+
					"timestamp: "+legacyTimestamp+"\n"+
					test.generated+
					"---\n\nBody.\n")
			var jsonStdout, jsonStderr bytes.Buffer
			var textStdout, textStderr bytes.Buffer

			// Act.
			jsonCode := Run([]string{"parse", path, "--json"}, &jsonStdout, &jsonStderr)
			textCode := Run([]string{"parse", path}, &textStdout, &textStderr)
			var response parseResponse
			decodeErr := json.Unmarshal(jsonStdout.Bytes(), &response)
			wantGeneratedJSON, marshalErr := json.Marshal(test.wantGenerated)

			// Assert.
			if jsonCode != 0 || textCode != 0 ||
				jsonStderr.Len() != 0 || textStderr.Len() != 0 ||
				decodeErr != nil || marshalErr != nil {
				t.Fatalf("parse json/text code/stderr/decode/marshal = %d/%d/%q/%q/%v/%v",
					jsonCode, textCode, jsonStderr.String(), textStderr.String(),
					decodeErr, marshalErr)
			}
			if !reflect.DeepEqual(response.Generated, test.wantGenerated) ||
				response.LegacyFallback ||
				response.EffectiveContentChangeTime != test.wantEffective {
				t.Fatalf("generated/timestamp projection = generated:%#v fallback:%t effective:%q, want %#v/false/%q",
					response.Generated, response.LegacyFallback,
					response.EffectiveContentChangeTime, test.wantGenerated, test.wantEffective)
			}
			if response.EffectiveContentChangeTime == legacyTimestamp {
				t.Fatalf("present generated replacement leaked legacy timestamp %q", legacyTimestamp)
			}
			wireFragment := `"generated":` + string(wantGeneratedJSON)
			if !strings.Contains(jsonStdout.String(), wireFragment) {
				t.Fatalf("parse JSON = %s, want exact compact generation fragment %s",
					jsonStdout.String(), wireFragment)
			}
			textFragment := fmt.Sprintf("generated: by=%s at=%s\n",
				renderTextString(test.wantGenerated.By),
				renderTextString(test.wantGenerated.At))
			if !strings.Contains(textStdout.String(), textFragment) {
				t.Fatalf("parse text = %q, want exact generation line %q",
					textStdout.String(), textFragment)
			}
		})
	}
}

func TestValidateStrictV02HasNoLegacyTimestampFalsePositive(t *testing.T) {
	// Arrange.
	root := fixturePath(t, "positive", "minimal")
	var stdout, stderr bytes.Buffer

	// Act.
	code := Run([]string{"validate", "--path", root, "--spec", "0.2", "--strict", "--json"}, &stdout, &stderr)

	// Assert.
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("Run(validate) code/stderr = %d/%q; stdout=%q", code, stderr.String(), stdout.String())
	}
	var response struct {
		EffectiveVersion string                     `json:"effective_version"`
		Diagnostics      []ValidationJSONDiagnostic `json:"diagnostics"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; output = %q", err, stdout.String())
	}
	if response.EffectiveVersion != "0.2" {
		t.Fatalf("effective_version = %q, want 0.2", response.EffectiveVersion)
	}
	for _, diagnostic := range response.Diagnostics {
		if strings.Contains(strings.ToLower(diagnostic.Message), "timestamp") {
			t.Fatalf("unexpected timestamp diagnostic: %#v", diagnostic)
		}
	}
}

func TestValidateStrictV02HasNoLegacyCitationsFalsePositive(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "index.md"), "---\nokf_version: \"0.2\"\n---\n# Knowledge\n\n- [Native](native.md)\n- [Marker only](marker-only.md)\n")
	writeFixtureFile(t, filepath.Join(root, "native.md"), "---\n"+
		"type: Note\n"+
		"sources:\n  - {id: spec, resource: https://example.test/spec}\n"+
		"---\n\nClaim [^spec].\n\n[^spec]: https://example.test/spec\n")
	writeFixtureFile(t, filepath.Join(root, "marker-only.md"), "---\ntype: Note\n---\n\nProse marker [1] without an owned Citations section.\n")
	var stdout, stderr bytes.Buffer

	// Act.
	code := Run([]string{"validate", "--path", root, "--spec", "0.2", "--strict", "--json"}, &stdout, &stderr)
	var response struct {
		Diagnostics []ValidationJSONDiagnostic `json:"diagnostics"`
	}
	decodeErr := json.Unmarshal(stdout.Bytes(), &response)

	// Assert.
	if code != 0 || stderr.Len() != 0 || decodeErr != nil {
		t.Fatalf("Run(validate strict v0.2) code/stderr/decode = %d/%q/%v; stdout=%q",
			code, stderr.String(), decodeErr, stdout.String())
	}
	for _, diagnostic := range response.Diagnostics {
		if diagnostic.Code == "citation_integrity_invalid" {
			t.Fatalf("unexpected legacy Citations diagnostic: %#v", diagnostic)
		}
	}
}

func runParseAuditJSON(
	t *testing.T,
	frontmatter string,
	body string,
) (int, parseResponse) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "concept.md")
	writeFixtureFile(t, path, "---\n"+frontmatter+"---\n"+body)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"parse", path, "--spec", "0.2", "--json"}, &stdout, &stderr)
	var response parseResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil || stderr.Len() != 0 {
		t.Fatalf("Run(parse audit) code/stderr/decode = %d/%q/%v; stdout=%q",
			code, stderr.String(), err, stdout.String())
	}
	return code, response
}

func runParseJSON(t *testing.T, path string) parseResponse {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run([]string{"parse", path, "--json"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("Run(parse %s) code/stderr = %d/%q", path, code, stderr.String())
	}
	var response parseResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; output = %q", err, stdout.String())
	}
	return response
}
