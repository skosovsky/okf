package bundle

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestInstantTemporalProfilePreservesPrecisionAndBoundary(t *testing.T) {
	// Arrange.
	f, err := ParseFrontmatter("stale_after: 2026-09-23T18:00:00.123456789+07:00\n")
	if err != nil {
		t.Fatal(err)
	}
	boundary := time.Date(2026, 9, 23, 11, 0, 0, 123456789, time.UTC)

	// Act.
	observed, err := f.StaleAfterForProfileContext(context.Background(), TemporalProfileInstant)
	before, knownBefore, errBefore := f.IsStaleForProfileContext(context.Background(), boundary.Add(-time.Nanosecond), TemporalProfileInstant)
	at, knownAt, errAt := f.IsStaleForProfileContext(context.Background(), boundary, TemporalProfileInstant)
	after, knownAfter, errAfter := f.IsStaleForProfileContext(context.Background(), boundary.Add(time.Nanosecond), TemporalProfileInstant)
	legacy, legacyErr := f.StaleAfterForProfileContext(context.Background(), TemporalProfileDate)

	// Assert.
	if err != nil || errBefore != nil || errAt != nil || errAfter != nil || legacyErr != nil {
		t.Fatalf("errors: %v %v %v %v %v", err, errBefore, errAt, errAfter, legacyErr)
	}
	if observed.Value.State != TemporalValid || observed.Value.Raw != "2026-09-23T18:00:00.123456789+07:00" || !observed.Value.Time.Equal(boundary) {
		t.Fatalf("observation = %#v", observed)
	}
	if !knownBefore || !knownAt || !knownAfter || before || !at || !after {
		t.Fatalf("boundary = (%t,%t) (%t,%t) (%t,%t)", before, knownBefore, at, knownAt, after, knownAfter)
	}
	if legacy.Value.State != TemporalMalformed {
		t.Fatalf("legacy observation = %#v", legacy.Value)
	}
}

func TestInstantTemporalYAMLFormsAndCancellation(t *testing.T) {
	for _, test := range []struct {
		name, yaml, wantRaw string
		want                TemporalValueState
	}{
		{"quoted", "stale_after: '2026-09-23T18:00:00+07:00'\n", "2026-09-23T18:00:00+07:00", TemporalValid},
		{"explicit-timestamp", "stale_after: !!timestamp 2026-09-23T18:00:00+07:00\n", "2026-09-23T18:00:00+07:00", TemporalValid},
		{"explicit-string", "stale_after: !!str 2026-09-23T18:00:00+07:00\n", "2026-09-23T18:00:00+07:00", TemporalValid},
		{"alias", "deadline: &deadline 2026-09-23T18:00:00+07:00\nstale_after: *deadline\n", "2026-09-23T18:00:00+07:00", TemporalValid},
		{"merge", "defaults: &defaults {stale_after: 2026-09-23T18:00:00+07:00}\n<<: *defaults\n", "2026-09-23T18:00:00+07:00", TemporalValid},
		{"null", "stale_after: null\n", "null", TemporalMalformed},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			f, err := ParseFrontmatter(test.yaml)
			if err != nil {
				t.Fatal(err)
			}
			// Act.
			observed, err := f.StaleAfterForProfileContext(context.Background(), TemporalProfileInstant)
			// Assert.
			if err != nil || observed.Value.State != test.want || observed.Value.Raw != test.wantRaw {
				t.Fatalf("observation=%#v err=%v", observed, err)
			}
		})
	}
	// Arrange.
	f, err := ParseFrontmatter("stale_after: 2026-09-23T18:00:00+07:00\n")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Act.
	observed, err := f.StaleAfterForProfileContext(ctx, TemporalProfileInstant)
	// Assert.
	if !errors.Is(err, context.Canceled) || observed != (ProfileStaleAfterObservation{}) {
		t.Fatalf("cancelled result=%#v err=%v", observed, err)
	}
}

func TestInstantTemporalEquivalentOffsetsAndSourceShapes(t *testing.T) {
	// Arrange.
	f, err := ParseFrontmatter("window: &window {from: 2026-09-23T18:00:00+07:00, to: 2026-09-23T11:00:00Z}\n" +
		"usage_window: *window\n" +
		"source_defaults: &source_defaults {last_modified: 2026-09-23T18:00:00+07:00}\n" +
		"sources:\n  - <<: *source_defaults\n    resource: policy.md\n    usage_window: *window\n")
	if err != nil {
		t.Fatal(err)
	}
	// Act.
	window, windowErr := f.UsageWindowForProfileContext(context.Background(), TemporalProfileInstant)
	sources, sourcesErr := f.SourceStatesForProfileContext(context.Background(), TemporalProfileInstant)
	// Assert.
	if windowErr != nil || sourcesErr != nil {
		t.Fatalf("errors=%v %v", windowErr, sourcesErr)
	}
	if !window.Valid || !window.From.Time.Equal(window.To.Time) {
		t.Fatalf("equivalent offset window=%#v", window)
	}
	if len(sources) != 1 || !sources[0].Valid || !sources[0].UsageWindow.Valid || sources[0].LastModified.State != TemporalValid {
		t.Fatalf("source states=%#v", sources)
	}
}

func TestInstantTemporalWindowComparesInstants(t *testing.T) {
	// Arrange: lexical order is reversed but the instants are increasing.
	f, err := ParseFrontmatter("usage_window: {from: 2026-09-23T18:00:00+07:00, to: 2026-09-23T12:00:00Z}\n")
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	instant, err := f.UsageWindowForProfileContext(context.Background(), TemporalProfileInstant)
	legacy, legacyErr := f.UsageWindowForProfileContext(context.Background(), TemporalProfileDate)

	// Assert.
	if err != nil || legacyErr != nil {
		t.Fatalf("errors: %v %v", err, legacyErr)
	}
	if !instant.Valid || instant.Value.From != "2026-09-23T18:00:00+07:00" || instant.Value.To != "2026-09-23T12:00:00Z" {
		t.Fatalf("instant = %#v", instant)
	}
	if legacy.Valid {
		t.Fatalf("legacy accepted datetimes: %#v", legacy)
	}
}

func TestInstantTemporalRejectsUnknownOffsetAndAmbiguousYAML(t *testing.T) {
	for _, raw := range []string{
		"stale_after: 2026-09-23T18:00:00-00:00\n",
		"stale_after: 2026-09-23T18:00:00+24:00\n",
		"stale_after: 2026-09-23T18:00:00,123Z\n",
		"stale_after: 2026-09-23T18:00:00\n",
		"stale_after: 2026-09-23T18:00:00Z\nstale_after: 2026-09-24T18:00:00Z\n",
	} {
		// Arrange.
		f, err := ParseFrontmatter(raw)
		if err != nil {
			t.Fatal(err)
		}
		// Act.
		observed, err := f.StaleAfterForProfileContext(context.Background(), TemporalProfileInstant)
		// Assert.
		if err != nil || observed.Value.State != TemporalMalformed {
			t.Fatalf("%q => %#v, %v", raw, observed, err)
		}
	}
}
