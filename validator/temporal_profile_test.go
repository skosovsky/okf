package validator

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/skosovsky/okf/bundle"
)

func TestStrictInstantTemporalProfileUsesExactBoundaryAndInstantOrdering(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.md"), []byte("---\nokf_version: \"0.2\"\n---\n# Notes\n- [A](a.md)\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("---\ntype: Note\n"+
		"stale_after: 2026-09-23T18:00:00.123456789+07:00\n"+
		"sources:\n  - {resource: policy.md, last_modified: 2026-09-23T12:00:00Z}\n"+
		"usage_window: {from: 2026-09-23T18:00:00+07:00, to: 2026-09-23T12:00:00Z}\n"+
		"---\nA.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	boundary := time.Date(2026, 9, 23, 11, 0, 0, 123456789, time.UTC)

	// Act.
	before := ValidatePath(root, &ValidatorConfig{Strict: true, TemporalProfile: bundle.TemporalProfileInstant, ReferenceDate: boundary.Add(-time.Nanosecond)})
	at := ValidatePath(root, &ValidatorConfig{Strict: true, TemporalProfile: bundle.TemporalProfileInstant, ReferenceDate: boundary})
	legacy := ValidatePath(root, &ValidatorConfig{Strict: true, TemporalProfile: bundle.TemporalProfileDate, ReferenceDate: boundary})

	// Assert.
	has := func(report Report, code string) bool {
		for _, diagnostic := range report.Diagnostics {
			if diagnostic.Code == code {
				return true
			}
		}
		return false
	}
	if before.ErrorCount() != 0 || at.ErrorCount() != 0 || has(before, "stale") || !has(at, "stale") {
		t.Fatalf("boundary reports: before=%#v at=%#v", before, at)
	}
	for _, code := range []string{"source_field_invalid", "usage_window_invalid", "usage_window_order", "stale_after_invalid"} {
		if has(at, code) {
			t.Fatalf("instant profile got %s: %#v", code, at)
		}
	}
	if !has(legacy, "source_field_invalid") || !has(legacy, "stale_after_invalid") {
		t.Fatalf("legacy profile accepted instants: %#v", legacy)
	}
}
