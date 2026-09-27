package bundle

import (
	"context"
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// TemporalProfile selects the temporal rules of a pinned OKF 0.2 revision.
// The zero value retains the originally published calendar-date contract.
type TemporalProfile string

const (
	TemporalProfileDate    TemporalProfile = "date-3fcbb9f"
	TemporalProfileInstant TemporalProfile = "instant-0b87c52"
)

func NormalizeTemporalProfile(profile TemporalProfile) (TemporalProfile, error) {
	if profile == "" {
		return TemporalProfileDate, nil
	}
	switch profile {
	case TemporalProfileDate, TemporalProfileInstant:
		return profile, nil
	default:
		return "", fmt.Errorf("unknown OKF temporal profile %q", profile)
	}
}

// ParseOffsetDateTime accepts the toolkit's documented RFC3339 subset. It
// requires a known UTC offset and rejects Go's permissive comma fraction and
// out-of-range numeric offsets before parsing the calendar/time fields.
func ParseOffsetDateTime(raw string) (time.Time, error) {
	invalid := fmt.Errorf("invalid offset-bearing RFC3339 datetime")
	if len(raw) < len("2006-01-02T15:04:05Z") || len(raw) > 1<<20 || raw[10] != 'T' {
		return time.Time{}, invalid
	}
	zoneIndex := 19
	if raw[zoneIndex] == '.' {
		zoneIndex++
		start := zoneIndex
		for zoneIndex < len(raw) && raw[zoneIndex] >= '0' && raw[zoneIndex] <= '9' {
			zoneIndex++
		}
		if zoneIndex == start {
			return time.Time{}, invalid
		}
	}
	if zoneIndex >= len(raw) {
		return time.Time{}, invalid
	}
	zone := raw[zoneIndex:]
	if zone != "Z" {
		if len(zone) != 6 || (zone[0] != '+' && zone[0] != '-') || zone[3] != ':' || zone == "-00:00" {
			return time.Time{}, invalid
		}
		for _, i := range []int{1, 2, 4, 5} {
			if zone[i] < '0' || zone[i] > '9' {
				return time.Time{}, invalid
			}
		}
		hour := int(zone[1]-'0')*10 + int(zone[2]-'0')
		minute := int(zone[4]-'0')*10 + int(zone[5]-'0')
		if hour > 23 || minute > 59 {
			return time.Time{}, invalid
		}
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, invalid
	}
	return parsed, nil
}

// TemporalObservation retains raw scalar text and its exact comparison value.
// Time is only meaningful when State is valid. A date profile's Time denotes
// a civil calendar date; callers must not treat it as a recovered instant.
type TemporalObservation struct {
	Raw   string
	Time  time.Time
	State TemporalValueState
}

type TemporalWindowState struct {
	Present bool
	Mapping bool
	Valid   bool
	From    TemporalObservation
	To      TemporalObservation
	Value   UsageWindow
}

type TemporalSourceState struct {
	Source       ProvenanceSourceState
	LastModified TemporalObservation
	UsageWindow  TemporalWindowState
	Valid        bool
	Value        ProvenanceSource
}

type ProfileStaleAfterObservation struct {
	Family SemanticFamilyObservation
	Value  TemporalObservation
}

func temporalValueFromNodeContext(ctx context.Context, node *yaml.Node, present bool, profile TemporalProfile) (TemporalObservation, error) {
	if err := ctx.Err(); err != nil {
		return TemporalObservation{}, err
	}
	if !present {
		return TemporalObservation{State: TemporalAbsent}, nil
	}
	if node == nil || node.Kind != yaml.ScalarNode {
		return TemporalObservation{State: TemporalMalformed}, nil
	}
	raw, err := stringFromStringContext(ctx, node.Value)
	if err != nil {
		return TemporalObservation{}, err
	}
	bad := TemporalObservation{Raw: raw, State: TemporalMalformed}
	if node.Tag != "!!str" && node.Tag != "!!timestamp" {
		return bad, nil
	}
	if profile == TemporalProfileDate {
		value, err := parseDateValueContext(ctx, raw, true)
		if err != nil {
			return TemporalObservation{}, err
		}
		return TemporalObservation{Raw: value.Raw, Time: value.Time, State: value.State}, nil
	}
	// RFC 3339 uses -00:00 to mean that the local offset is unknown. Such a
	// value cannot establish the explicit UTC offset required by this profile.
	value, parseErr := ParseOffsetDateTime(raw)
	if err := ctx.Err(); err != nil {
		return TemporalObservation{}, err
	}
	if parseErr != nil {
		return bad, nil
	}
	return TemporalObservation{Raw: raw, Time: value, State: TemporalValid}, nil
}

func (f Frontmatter) StaleAfterForProfileContext(ctx context.Context, profile TemporalProfile) (ProfileStaleAfterObservation, error) {
	profile, err := NormalizeTemporalProfile(profile)
	if err != nil {
		return ProfileStaleAfterObservation{}, err
	}
	if err := ctx.Err(); err != nil {
		return ProfileStaleAfterObservation{}, err
	}
	root := f.mappingNode()
	family, err := observeSemanticFamilyContext(ctx, &root, "stale_after", YAMLShapeScalar)
	if err != nil {
		return ProfileStaleAfterObservation{}, err
	}
	out := ProfileStaleAfterObservation{Family: family.observation}
	if family.observation.Ambiguous || family.resolved == nil && family.observation.Present {
		out.Value = TemporalObservation{Raw: family.observation.ResolvedRaw, State: TemporalMalformed}
	} else {
		out.Value, err = temporalValueFromNodeContext(ctx, family.resolved, family.observation.Present, profile)
		if err != nil {
			return ProfileStaleAfterObservation{}, err
		}
	}
	return out, ctx.Err()
}

func (f Frontmatter) IsStaleForProfileContext(ctx context.Context, at time.Time, profile TemporalProfile) (bool, bool, error) {
	profile, err := NormalizeTemporalProfile(profile)
	if err != nil {
		return false, false, err
	}
	observed, err := f.StaleAfterForProfileContext(ctx, profile)
	if err != nil {
		return false, false, err
	}
	if observed.Value.State != TemporalValid {
		return false, false, nil
	}
	if profile == TemporalProfileDate {
		y, m, d := at.Date()
		at = time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	}
	return !at.Before(observed.Value.Time), true, ctx.Err()
}

func temporalWindowFromMappingContext(ctx context.Context, resolver *yamlSemanticResolver, node *yaml.Node, key string, profile TemporalProfile) (TemporalWindowState, error) {
	value, present, err := semanticMappingNodeContext(resolver, node, key)
	if err != nil {
		return TemporalWindowState{}, err
	}
	if !present {
		return TemporalWindowState{}, nil
	}
	out := TemporalWindowState{Present: true}
	if value == nil || value.Kind != yaml.MappingNode {
		return out, nil
	}
	out.Mapping = true
	from, hasFrom, err := semanticMappingNodeContext(resolver, value, "from")
	if err != nil {
		return TemporalWindowState{}, err
	}
	to, hasTo, err := semanticMappingNodeContext(resolver, value, "to")
	if err != nil {
		return TemporalWindowState{}, err
	}
	out.From, err = temporalValueFromNodeContext(ctx, from, hasFrom, profile)
	if err != nil {
		return TemporalWindowState{}, err
	}
	out.To, err = temporalValueFromNodeContext(ctx, to, hasTo, profile)
	if err != nil {
		return TemporalWindowState{}, err
	}
	out.Valid = out.From.State == TemporalValid && out.To.State == TemporalValid && !out.From.Time.After(out.To.Time)
	if out.From.State == TemporalValid {
		out.Value.From = out.From.Raw
	}
	if out.To.State == TemporalValid {
		out.Value.To = out.To.Raw
	}
	return out, ctx.Err()
}

func (f Frontmatter) UsageWindowForProfileContext(ctx context.Context, profile TemporalProfile) (TemporalWindowState, error) {
	profile, err := NormalizeTemporalProfile(profile)
	if err != nil {
		return TemporalWindowState{}, err
	}
	root := f.mappingNode()
	return temporalWindowFromMappingContext(ctx, newYAMLSemanticResolver(ctx), &root, "usage_window", profile)
}

func (f Frontmatter) SourceStatesForProfileContext(ctx context.Context, profile TemporalProfile) ([]TemporalSourceState, error) {
	profile, err := NormalizeTemporalProfile(profile)
	if err != nil {
		return nil, err
	}
	root := f.mappingNode()
	resolver := newYAMLSemanticResolver(ctx)
	selection, err := resolver.mappingValue(&root, "sources")
	if err != nil {
		return nil, err
	}
	node, present := semanticSelectionNode(selection)
	if !present || node == nil || node.Kind != yaml.SequenceNode {
		return nil, ctx.Err()
	}
	result := make([]TemporalSourceState, 0, len(node.Content))
	for _, item := range node.Content {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resolved, err := resolver.value(item)
		if err != nil {
			return nil, err
		}
		if resolved.resolved {
			item = resolved.node
		}
		if item == nil || item.Kind != yaml.MappingNode {
			result = append(result, TemporalSourceState{Source: ProvenanceSourceState{Present: true}})
			continue
		}
		base, err := provenanceSourceStateContext(ctx, resolver, item)
		if err != nil {
			return nil, err
		}
		modified, hasModified, err := semanticMappingNodeContext(resolver, item, "last_modified")
		if err != nil {
			return nil, err
		}
		lastModified, err := temporalValueFromNodeContext(ctx, modified, hasModified, profile)
		if err != nil {
			return nil, err
		}
		window, err := temporalWindowFromMappingContext(ctx, resolver, item, "usage_window", profile)
		if err != nil {
			return nil, err
		}
		valid := base.Resource.Valid && (!base.ID.Present || base.ID.Valid) &&
			(!base.Title.Present || base.Title.Valid) && (!base.Author.Present || base.Author.Valid) &&
			(!base.UsageCount.Present || base.UsageCount.Valid) &&
			lastModified.State != TemporalMalformed && (!window.Present || window.Valid)
		value := cloneProvenanceSource(base.Value)
		if lastModified.State == TemporalValid {
			value.LastModified = lastModified.Raw
		}
		if window.Valid {
			copied := window.Value
			value.UsageWindow = &copied
		}
		result = append(result, TemporalSourceState{Source: base, LastModified: lastModified, UsageWindow: window, Valid: valid, Value: value})
	}
	return result, ctx.Err()
}
