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
	if raw == "" {
		return nil, nil
	}
	date, err := time.Parse(time.DateOnly, raw)
	if err != nil {
		return nil, fmt.Errorf("invalid --as-of date %q (want YYYY-MM-DD)", raw)
	}
	return &date, nil
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
