package store

import (
	"testing"

	"github.com/skosovsky/okf/bundle"
)

func TestInstantTemporalOperationsRequireExplicitProfileAndOrderByInstant(t *testing.T) {
	// Arrange.
	concept := testRef(t, "temporal/example", "").ID
	window := UsageWindow{From: "2026-09-23T18:00:00+07:00", To: "2026-09-23T12:00:00Z"}
	stale := "2026-09-23T18:00:00+07:00"
	source := ProvenanceSource{ID: "s", Resource: "policy.md", LastModified: stale, UsageWindow: &window}

	// Act.
	_, oldWindowErr := NewSetUsageWindow(concept, nil, &window)
	instantWindow, instantWindowErr := NewSetUsageWindowForProfile(concept, nil, &window, bundle.TemporalProfileInstant)
	_, oldSourceErr := NewPutSource(concept, source)
	instantSource, instantSourceErr := NewPutSourceForProfile(concept, source, bundle.TemporalProfileInstant)
	_, oldLifecycleErr := NewSetLifecycle(concept, Lifecycle{StaleAfter: &stale})
	instantLifecycle, instantLifecycleErr := NewSetLifecycleForProfile(concept, Lifecycle{StaleAfter: &stale}, bundle.TemporalProfileInstant)

	// Assert.
	if oldWindowErr == nil || oldSourceErr == nil || oldLifecycleErr == nil {
		t.Fatalf("old constructors accepted instant: window=%v source=%v lifecycle=%v", oldWindowErr, oldSourceErr, oldLifecycleErr)
	}
	if instantWindowErr != nil || instantSourceErr != nil || instantLifecycleErr != nil {
		t.Fatalf("instant constructors: window=%v source=%v lifecycle=%v", instantWindowErr, instantSourceErr, instantLifecycleErr)
	}
	change := ChangeSet{Version: ChangeSetFormatVersion, ID: "instant-operation", Actor: "principal", BaseRevision: testRevision(t), Operations: []Operation{instantWindow, instantSource, instantLifecycle}}
	if _, err := change.RequestDigest(); err != nil {
		t.Fatalf("canonical instant request: %v", err)
	}
}

func TestInstantTemporalWindowRejectsActualReversalAndUnknownOffset(t *testing.T) {
	// Arrange.
	concept := testRef(t, "temporal/example", "").ID
	badWindows := []UsageWindow{
		{From: "2026-09-23T12:00:00Z", To: "2026-09-23T18:00:00+07:00"},
		{From: "2026-09-23T12:00:00-00:00", To: "2026-09-23T13:00:00Z"},
	}
	// Act and Assert.
	for _, window := range badWindows {
		if _, err := NewSetUsageWindowForProfile(concept, nil, &window, bundle.TemporalProfileInstant); err == nil {
			t.Fatalf("accepted invalid window: %#v", window)
		}
	}
}
