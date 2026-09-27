package okfcli

import (
	"fmt"
	"time"

	"github.com/skosovsky/okf/bundle"
)

type VersionDTO struct {
	Declared      string `json:"declared_version,omitempty"`
	Effective     string `json:"effective_version"`
	Source        string `json:"resolution"`
	Compatibility string `json:"compatibility"`
}

func projectVersion(resolution bundle.VersionResolution) VersionDTO {
	return VersionDTO{
		Declared:      resolution.Declared,
		Effective:     resolution.Effective,
		Source:        string(resolution.Source),
		Compatibility: string(resolution.Compatibility),
	}
}

func selectorAssertion(selector string) string {
	if selector == "" || selector == "auto" {
		return ""
	}
	return selector
}

func resolveBundleVersion(loaded *bundle.Bundle, selector string) (bundle.VersionResolution, error) {
	resolution, err := loaded.VersionResolution(selectorAssertion(selector))
	if err != nil {
		return bundle.VersionResolution{}, fmt.Errorf("resolve OKF version: %w", err)
	}
	return resolution, nil
}

func parseReferenceDate(raw string) (*time.Time, error) {
	return parseTemporalReference(raw, bundle.TemporalProfileDate)
}

func parseTemporalReference(raw string, profile bundle.TemporalProfile) (*time.Time, error) {
	profile, err := bundle.NormalizeTemporalProfile(profile)
	if err != nil {
		return nil, err
	}
	if raw == "" {
		return nil, nil
	}
	format := time.DateOnly
	if profile == bundle.TemporalProfileInstant {
		format = time.RFC3339
	}
	date, err := time.Parse(format, raw)
	if profile == bundle.TemporalProfileInstant {
		date, err = bundle.ParseOffsetDateTime(raw)
	}
	if err != nil {
		if profile == bundle.TemporalProfileInstant {
			return nil, fmt.Errorf("invalid --as-of datetime %q (want RFC3339 with UTC offset)", raw)
		}
		return nil, fmt.Errorf("invalid --as-of date %q (want YYYY-MM-DD)", raw)
	}
	return &date, nil
}

func parseTemporalProfile(raw string) (bundle.TemporalProfile, error) {
	return bundle.NormalizeTemporalProfile(bundle.TemporalProfile(raw))
}

func formatTemporalReference(at time.Time, profile bundle.TemporalProfile) string {
	if profile == bundle.TemporalProfileInstant {
		return at.Format(time.RFC3339Nano)
	}
	return at.Format(time.DateOnly)
}

type trustCounts struct {
	Unverified       int `json:"unverified"`
	MachineConfirmed int `json:"machine_confirmed"`
	HumanReviewed    int `json:"human_reviewed"`
}

func (counts *trustCounts) add(tier bundle.TrustTier) {
	switch tier {
	case bundle.TrustHumanReviewed:
		counts.HumanReviewed++
	case bundle.TrustMachineConfirmed:
		counts.MachineConfirmed++
	default:
		counts.Unverified++
	}
}

type lifecycleCounts struct {
	Draft      int            `json:"draft"`
	Stable     int            `json:"stable"`
	Deprecated int            `json:"deprecated"`
	Invalid    int            `json:"invalid"`
	Other      map[string]int `json:"other,omitempty"`
}

func (counts *lifecycleCounts) add(state bundle.StatusState) {
	if !state.Valid {
		counts.Invalid++
		return
	}
	switch state.Effective {
	case bundle.StatusDraft:
		counts.Draft++
	case bundle.StatusDeprecated:
		counts.Deprecated++
	case bundle.StatusStable:
		counts.Stable++
	default:
		if counts.Other == nil {
			counts.Other = make(map[string]int)
		}
		counts.Other[state.Effective]++
	}
}
