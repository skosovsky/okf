package mutation

import (
	"bytes"
	"context"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
)

func TestInstantTemporalMutationPreservesUnknownFieldsAndRawOffsets(t *testing.T) {
	// Arrange.
	source := memorySource{"a.md": []byte("---\r\ntype: Note\r\nx-unknown: keep\r\n---\r\nBody\r\n")}
	id := ref(t, "a").ID
	window := store.UsageWindow{From: "2026-09-23T18:00:00+07:00", To: "2026-09-23T12:00:00Z"}
	stale := "2026-09-23T18:00:00.123456789+07:00"
	put, err := store.NewPutSourceForProfile(id, store.ProvenanceSource{ID: "source", Resource: "policy.md", LastModified: stale, UsageWindow: &window}, bundle.TemporalProfileInstant)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := store.NewSetUsageWindowForProfile(id, nil, &window, bundle.TemporalProfileInstant)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, err := store.NewSetLifecycleForProfile(id, store.Lifecycle{StaleAfter: &stale}, bundle.TemporalProfileInstant)
	if err != nil {
		t.Fatal(err)
	}
	change := store.ChangeSet{Version: store.ChangeSetFormatVersion, ID: "instant-temporal", Actor: "principal", BaseRevision: revisionFor(t, source), Operations: []store.Operation{put, shared, lifecycle}}

	// Act.
	plan, err := Plan(context.Background(), source, change)
	if err != nil {
		t.Fatal(err)
	}
	result, err := plan.Staged.ReadFile(context.Background(), "a.md")
	if err != nil {
		t.Fatal(err)
	}

	// Assert.
	for _, want := range []string{"x-unknown: keep", stale, window.From, window.To} {
		if !bytes.Contains(result, []byte(want)) {
			t.Fatalf("missing %q in %s", want, result)
		}
	}
	if bytes.Contains(result, []byte("\n")) && !bytes.Contains(result, []byte("\r\n")) {
		t.Fatalf("CRLF lost: %q", result)
	}
}
