package mutation

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
)

func temporalUpgradeFixture() memorySource {
	return memorySource{"a.md": []byte("---\r\ntype: Note\r\nstale_after: 2026-01-02 # keep\r\nusage_window:\r\n  from: 2026-01-01\r\n  to: 2026-01-02\r\nsources:\r\n  - id: source\r\n    resource: source.md\r\n    last_modified: 2026-01-01\r\n    x-source: kept\r\nx-root: kept\r\n---\r\nBody\r\n")}
}

func temporalUpgradeMappings(t *testing.T) []TemporalUpgradeMapping {
	t.Helper()
	id, err := bundle.ParseConceptID("a")
	if err != nil {
		t.Fatal(err)
	}
	return []TemporalUpgradeMapping{
		{Concept: id, Path: "stale_after", From: "2026-01-02", To: "2026-01-02T12:00:00+07:00"},
		{Concept: id, Path: "usage_window.from", From: "2026-01-01", To: "2026-01-01T00:00:00+07:00"},
		{Concept: id, Path: "usage_window.to", From: "2026-01-02", To: "2026-01-02T23:59:59+07:00"},
		{Concept: id, Path: "sources[0].last_modified", From: "2026-01-01", To: "2026-01-01T10:00:00+07:00"},
	}
}

func TestTemporalUpgradeRequestDigestMatchesPreviewCanonicalChange(t *testing.T) {
	// Arrange.
	request := TemporalUpgradeRequest{ID: "digest-005", Actor: "human:reviewer", Mappings: temporalUpgradeMappings(t)}
	reversed := request
	reversed.Mappings = append([]TemporalUpgradeMapping(nil), request.Mappings...)
	for i, j := 0, len(reversed.Mappings)-1; i < j; i, j = i+1, j-1 {
		reversed.Mappings[i], reversed.Mappings[j] = reversed.Mappings[j], reversed.Mappings[i]
	}
	preview, err := NewTemporalUpgradePlanner().Preview(context.Background(), temporalUpgradeFixture(), request)
	if err != nil {
		t.Fatal(err)
	}
	previewDigest, err := preview.Change.RequestDigest()
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	canonicalChange, canonicalErr := temporalUpgradeChangeFromRequest(TemporalUpgradeApplyRequest{Request: request, BaseRevision: preview.Preview.BaseRevision})
	reorderedChange, reorderedErr := temporalUpgradeChangeFromRequest(TemporalUpgradeApplyRequest{Request: reversed, BaseRevision: preview.Preview.BaseRevision})
	var canonical, reordered string
	if canonicalErr == nil {
		canonical, canonicalErr = canonicalChange.RequestDigest()
	}
	if reorderedErr == nil {
		reordered, reorderedErr = reorderedChange.RequestDigest()
	}

	// Assert.
	if canonicalErr != nil || reorderedErr != nil || canonical != previewDigest || reordered != previewDigest {
		t.Fatalf("digests: preview=%q request=%q/%v reordered=%q/%v", previewDigest, canonical, canonicalErr, reordered, reorderedErr)
	}
}

func TestTemporalUpgradePreviewRequiresEveryExplicitInstantAndPreservesPresentation(t *testing.T) {
	// Arrange.
	source := temporalUpgradeFixture()
	request := TemporalUpgradeRequest{ID: "upgrade-1", Actor: "human:reviewer", Mappings: temporalUpgradeMappings(t)}
	planner := NewTemporalUpgradePlanner()

	// Act.
	incomplete := request
	incomplete.Mappings = incomplete.Mappings[:1]
	blocked, blockedErr := planner.Preview(context.Background(), source, incomplete)
	preview, err := planner.Preview(context.Background(), source, request)
	if err != nil {
		t.Fatal(err)
	}
	updated := preview.Preview.Writes[0].Content

	// Assert.
	if !errors.Is(blockedErr, ErrTemporalUpgradeIncomplete) || len(blocked.Unresolved) != 3 || blocked.PlanDigest != "" {
		t.Fatalf("blocked preview: %#v, %v", blocked, blockedErr)
	}
	if preview.PlanDigest == "" || len(preview.Preview.Writes) != 1 {
		t.Fatalf("incomplete preview: %#v", preview)
	}
	if !bytes.Contains(updated, []byte("stale_after: 2026-01-02T12:00:00+07:00 # keep")) || !bytes.Contains(updated, []byte("x-source: kept")) || !bytes.Contains(updated, []byte("x-root: kept")) {
		t.Fatalf("presentation changed: %s", updated)
	}
	if bytes.Contains(updated, []byte("\n")) && bytes.Count(updated, []byte("\r\n")) != bytes.Count(updated, []byte("\n")) {
		t.Fatalf("CRLF lost: %q", updated)
	}
}

func TestTemporalUpgradePreviewRejectsLegacyVersionAndInvalidOffset(t *testing.T) {
	// Arrange.
	planner := NewTemporalUpgradePlanner()
	request := TemporalUpgradeRequest{ID: "upgrade-invalid", Actor: "human:reviewer", Mappings: temporalUpgradeMappings(t)}
	legacy := temporalUpgradeFixture()
	legacy["index.md"] = []byte("---\nokf_version: \"0.1\"\n---\n# Index\n\n- [A](a.md)\n")

	// Act.
	_, legacyErr := planner.Preview(context.Background(), legacy, request)
	invalid := request
	invalid.Mappings = append([]TemporalUpgradeMapping(nil), request.Mappings...)
	invalid.Mappings[0].To = "2026-01-02T12:00:00-00:00"
	_, offsetErr := planner.Preview(context.Background(), temporalUpgradeFixture(), invalid)

	// Assert.
	if legacyErr == nil || offsetErr == nil {
		t.Fatalf("legacy/invalid offset accepted: %v %v", legacyErr, offsetErr)
	}
}

func TestTemporalUpgradeExplicitTimestampTagKeepsTagAndComment(t *testing.T) {
	id, err := bundle.ParseConceptID("a")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"!!timestamp 2026-01-02", "!!str 2026-01-02"} {
		t.Run(value, func(t *testing.T) {
			// Arrange.
			source := memorySource{"a.md": []byte("---\r\ntype: Note\r\nstale_after: " + value + " # keep\r\n---\r\nBody\r\n")}
			request := TemporalUpgradeRequest{ID: "tagged", Actor: "human:reviewer", Mappings: []TemporalUpgradeMapping{{Concept: id, Path: "stale_after", From: "2026-01-02", To: "2026-01-02T12:00:00Z"}}}
			// Act.
			preview, err := NewTemporalUpgradePlanner().Preview(context.Background(), source, request)
			if err != nil {
				t.Fatal(err)
			}
			updated := preview.Preview.Writes[0].Content
			// Assert.
			want := strings.Replace(value, "2026-01-02", "2026-01-02T12:00:00Z", 1)
			if !bytes.Contains(updated, []byte("stale_after: "+want+" # keep\r\n")) {
				t.Fatalf("tag/comment/newline lost: %s", updated)
			}
		})
	}
}

func TestTemporalUpgradeUnsupportedTaggedQuotedScalarFailsClosed(t *testing.T) {
	// Arrange.
	source := memorySource{"a.md": []byte("---\ntype: Note\nstale_after: !!str '2026-01-02' # keep\n---\nBody\n")}
	id, err := bundle.ParseConceptID("a")
	if err != nil {
		t.Fatal(err)
	}
	request := TemporalUpgradeRequest{ID: "tagged-quoted", Actor: "human:reviewer", Mappings: []TemporalUpgradeMapping{{Concept: id, Path: "stale_after", From: "2026-01-02", To: "2026-01-02T12:00:00Z"}}}

	// Act.
	preview, previewErr := NewTemporalUpgradePlanner().Preview(context.Background(), source, request)
	current, readErr := source.ReadFile(context.Background(), "a.md")

	// Assert.
	if previewErr == nil || preview.PlanDigest != "" || readErr != nil || !bytes.Equal(current, source["a.md"]) {
		t.Fatalf("unsupported tagged scalar changed source: %#v %v %v", preview, previewErr, readErr)
	}
}
