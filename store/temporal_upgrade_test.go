package store

import (
	"testing"

	"github.com/skosovsky/okf/bundle"
)

func TestUpgradeTemporalConceptCanonicalIdentityAndOffsetValidation(t *testing.T) {
	// Arrange.
	id, err := bundle.ParseConceptID("a")
	if err != nil {
		t.Fatal(err)
	}
	base := []TemporalFieldRewrite{{Path: "usage_window.to", From: "2026-09-26", To: "2026-09-26T18:00:00+07:00"}, {Path: "stale_after", From: "2026-09-26", To: "2026-09-26T12:00:00.1200Z"}}
	first, err := NewUpgradeTemporalConcept(id, base)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewUpgradeTemporalConcept(id, []TemporalFieldRewrite{base[1], base[0]})
	if err != nil {
		t.Fatal(err)
	}
	forward := ChangeSet{Version: ChangeSetFormatVersion, ID: "upgrade", Actor: "human:reviewer", BaseRevision: Revision("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), Operations: []Operation{first}}
	reordered := forward
	reordered.Operations = []Operation{second}

	// Act.
	digest1, err1 := forward.RequestDigest()
	digest2, err2 := reordered.RequestDigest()
	_, unknownOffsetErr := NewUpgradeTemporalConcept(id, []TemporalFieldRewrite{{Path: "stale_after", From: "2026-09-26", To: "2026-09-26T12:00:00-00:00"}})
	_, duplicateErr := NewUpgradeTemporalConcept(id, []TemporalFieldRewrite{base[1], base[1]})

	// Assert.
	if err1 != nil || err2 != nil || digest1 != digest2 {
		t.Fatalf("canonical digest mismatch: %s %s %v %v", digest1, digest2, err1, err2)
	}
	if unknownOffsetErr == nil || duplicateErr == nil {
		t.Fatalf("invalid mappings accepted: %v %v", unknownOffsetErr, duplicateErr)
	}
	if len(first.Rewrites()) != 2 || first.Rewrites()[0].Path != "stale_after" {
		t.Fatalf("rewrites not sorted: %#v", first.Rewrites())
	}
}
