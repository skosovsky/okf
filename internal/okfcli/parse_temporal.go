package okfcli

import (
	"cmp"
	"context"
	"sort"
	"time"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/internal/markdownowner"
)

// projectDocumentForTemporalProfile preserves the established date projection
// and replaces only the fields whose meaning changed in the instant revision.
func projectDocumentForTemporalProfile(ctx context.Context, path string, document bundle.Document, resolution bundle.VersionResolution, asOf *time.Time, profile bundle.TemporalProfile) (parseResponse, error) {
	response, err := projectDocument(ctx, path, document, resolution, nil)
	if err != nil {
		return parseResponse{}, err
	}
	response.TemporalProfile = profile
	stale, err := document.Frontmatter.StaleAfterForProfileContext(ctx, profile)
	if err != nil {
		return parseResponse{}, err
	}
	response.StaleAfter = ""
	if stale.Value.State == bundle.TemporalValid {
		response.StaleAfter = stale.Value.Raw
	}
	response.LifecycleAudit.StaleAfter = temporalObservationAudit(stale.Value)
	if asOf != nil {
		value, known, err := document.Frontmatter.IsStaleForProfileContext(ctx, *asOf, profile)
		if err != nil {
			return parseResponse{}, err
		}
		response.AsOf = formatTemporalReference(*asOf, profile)
		if known {
			response.Stale = &value
		}
	}
	window, err := document.Frontmatter.UsageWindowForProfileContext(ctx, profile)
	if err != nil {
		return parseResponse{}, err
	}
	response.UsageWindow = nil
	if window.Valid {
		response.UsageWindow = &usageWindowDTO{From: window.Value.From, To: window.Value.To}
	}
	response.UsageWindowAudit = temporalWindowAudit(response.UsageWindowAudit, window)
	states, err := document.Frontmatter.SourceStatesForProfileContext(ctx, profile)
	if err != nil {
		return parseResponse{}, err
	}
	response.Sources = make([]sourceDTO, 0, len(states))
	response.SourcesAudit.Entries = make([]sourceAuditDTO, 0, len(states))
	for _, state := range states {
		entry := projectSourceAudit(state.Source)
		entry.Valid = state.Valid
		entry.HasValue = state.Valid
		entry.LastModified = temporalObservationAudit(state.LastModified)
		entry.UsageWindow = temporalWindowAudit(entry.UsageWindow, state.UsageWindow)
		if state.Valid {
			value := projectSources([]bundle.ProvenanceSource{state.Value})[0]
			if value.UsageWindow != nil {
				value.EffectiveUsageWindow = value.UsageWindow
			} else if response.UsageWindow != nil {
				shared := *response.UsageWindow
				value.EffectiveUsageWindow = &shared
			}
			entry.Value = &value
			response.Sources = append(response.Sources, value)
		} else {
			entry.Value = nil
		}
		response.SourcesAudit.Entries = append(response.SourcesAudit.Entries, entry)
	}
	for index := range response.Attributions {
		attribution := &response.Attributions[index]
		attribution.Sources = make([]sourceDTO, 0)
		for _, source := range response.Sources {
			if source.ID == "" {
				continue
			}
			normalized, err := markdownowner.NormalizeFootnoteLabelContext(ctx, source.ID)
			if err != nil {
				return parseResponse{}, err
			}
			if normalized == attribution.NormalizedID {
				attribution.Sources = append(attribution.Sources, source)
			}
		}
		sort.Slice(attribution.Sources, func(left, right int) bool {
			return compareAttributionSourceDTO(attribution.Sources[left], attribution.Sources[right]) < 0
		})
	}
	response.AttributionsAudit.Sources = 0
	for _, attribution := range response.Attributions {
		response.AttributionsAudit.Sources += len(attribution.Sources)
	}
	for index := range response.AttributionsAudit.EntryEvidence {
		entry := &response.AttributionsAudit.EntryEvidence[index]
		entry.Valid = false
		entry.NormalizedID = ""
		entry.Value = nil
		if entry.Index >= 0 && entry.Index < len(response.SourcesAudit.Entries) {
			entry.Source = response.SourcesAudit.Entries[entry.Index]
			state := states[entry.Index]
			if entry.Family.ShapeValid && !entry.Family.Ambiguous && !entry.Ambiguous &&
				state.Valid && state.Source.ID.Valid {
				normalized, err := markdownowner.NormalizeFootnoteLabelContext(ctx, state.Source.ID.Value)
				if err != nil {
					return parseResponse{}, err
				}
				for _, attribution := range response.Attributions {
					if normalized != "" && attribution.NormalizedID == normalized {
						copy := attribution
						entry.Valid = true
						entry.NormalizedID = normalized
						entry.Value = &copy
						break
					}
				}
			}
		}
	}
	return response, ctx.Err()
}

func compareAttributionSourceDTO(left, right sourceDTO) int {
	for _, pair := range [][2]string{{left.ID, right.ID}, {left.Resource, right.Resource}, {left.Title, right.Title}, {left.Author, right.Author}, {left.LastModified, right.LastModified}} {
		if order := cmp.Compare(pair[0], pair[1]); order != 0 {
			return order
		}
	}
	if left.UsageCount == nil || right.UsageCount == nil {
		if left.UsageCount == nil && right.UsageCount != nil {
			return -1
		}
		if left.UsageCount != nil && right.UsageCount == nil {
			return 1
		}
	} else if order := cmp.Compare(*left.UsageCount, *right.UsageCount); order != 0 {
		return order
	}
	if left.UsageWindow == nil || right.UsageWindow == nil {
		if left.UsageWindow == nil && right.UsageWindow != nil {
			return -1
		}
		if left.UsageWindow != nil && right.UsageWindow == nil {
			return 1
		}
		return 0
	}
	if order := cmp.Compare(left.UsageWindow.From, right.UsageWindow.From); order != 0 {
		return order
	}
	return cmp.Compare(left.UsageWindow.To, right.UsageWindow.To)
}

func temporalObservationAudit(value bundle.TemporalObservation) temporalAuditDTO {
	return temporalAuditDTO{State: string(value.State), Raw: value.Raw}
}

func temporalWindowAudit(base usageWindowAuditDTO, value bundle.TemporalWindowState) usageWindowAuditDTO {
	base.Present = value.Present
	base.Mapping = value.Mapping
	base.Valid = value.Valid
	base.HasValue = value.Valid
	base.From = temporalObservationAudit(value.From)
	base.To = temporalObservationAudit(value.To)
	base.Value = nil
	if value.Valid {
		base.Value = &usageWindowDTO{From: value.Value.From, To: value.Value.To}
	}
	return base
}
