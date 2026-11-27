package bundle

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// CurrentVerification reports whether a verification can contribute to the
// OKF 0.3 trust tier. Verification retains invalid and historical entries.
type CurrentVerification struct {
	Verification VerificationState
	Current      bool
	Reason       string
}

// TrustStateV03 explains the tier and preserves observations in source order.
type TrustStateV03 struct {
	Tier              TrustTier
	GenerationPresent bool
	GeneratedAt       DateTimeValue
	Verifications     []CurrentVerification
}

// TrustStateV03Context derives trust for the OKF 0.3 document contract.
func (f Frontmatter) TrustStateV03Context(ctx context.Context) (TrustStateV03, error) {
	root := f.mappingNode()
	resolver := newYAMLSemanticResolver(ctx)
	selection, err := resolver.mappingValue(&root, "generated")
	if err != nil {
		return TrustStateV03{}, err
	}
	node, resolved := semanticSelectionNode(selection)
	state := TrustStateV03{Tier: TrustUnverified, GenerationPresent: selection.present}
	if resolved && node.Kind == yaml.MappingNode {
		by, present, err := semanticMappingNodeContext(resolver, node, "by")
		if err != nil {
			return TrustStateV03{}, err
		}
		state.GenerationPresent = present && (by == nil || !emptyV03Value(by))
		state.GeneratedAt.State = TemporalAbsent
		if state.GenerationPresent {
			at, present, err := semanticMappingNodeContext(resolver, node, "at")
			if err != nil {
				return TrustStateV03{}, err
			}
			state.GeneratedAt, err = timestampV03FromNode(ctx, at, present)
			if err != nil {
				return TrustStateV03{}, err
			}
		}
	} else if !state.GenerationPresent || resolved && emptyV03Value(node) {
		state.GenerationPresent = false
		state.GeneratedAt.State = TemporalAbsent
	} else {
		state.GeneratedAt.State = TemporalMalformed
	}

	events, err := f.verificationStatesContext(ctx, timestampV03FromNode, ValidActorV03Context)
	if err != nil {
		return TrustStateV03{}, err
	}
	for _, event := range events {
		if err := ctx.Err(); err != nil {
			return TrustStateV03{}, err
		}
		observation := CurrentVerification{Verification: event}
		switch {
		case !event.Valid:
			observation.Reason = "invalid_verification"
		case !state.GenerationPresent:
			observation.Current, observation.Reason = true, "no_generation"
		case state.GeneratedAt.State != TemporalValid:
			observation.Reason = "generation_time_unavailable"
		case event.AtValue.Time.Before(state.GeneratedAt.Time):
			observation.Reason = "before_generation"
		default:
			observation.Current, observation.Reason = true, "at_or_after_generation"
		}
		state.Verifications = append(state.Verifications, observation)
		if observation.Current {
			switch {
			case strings.HasPrefix(event.Value.By, "human:"):
				state.Tier = TrustHumanReviewed
			case state.Tier == TrustUnverified:
				state.Tier = TrustMachineConfirmed
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return TrustStateV03{}, err
	}
	return state, nil
}

func emptyV03Value(node *yaml.Node) bool {
	return node.Tag == "!!null" || node.Tag == "!!str" && node.Value == "" ||
		node.Kind == yaml.SequenceNode && len(node.Content) == 0
}

func timestampV03FromNode(ctx context.Context, node *yaml.Node, present bool) (DateTimeValue, error) {
	if err := ctx.Err(); err != nil {
		return DateTimeValue{}, err
	}
	if !present || node != nil && emptyV03Value(node) {
		return DateTimeValue{State: TemporalAbsent}, nil
	}
	if node == nil || node.Kind != yaml.ScalarNode {
		return DateTimeValue{State: TemporalMalformed}, nil
	}
	raw, err := stringFromStringContext(ctx, node.Value)
	if err != nil {
		return DateTimeValue{}, err
	}
	observation := DateTimeValue{Raw: raw, State: TemporalMalformed}
	if node.Tag != "!!str" && node.Tag != "!!timestamp" {
		return observation, nil
	}
	parsed, err := parseTimestampV03Context(ctx, raw)
	if err == nil {
		observation.State = TemporalValid
		observation.Time = parsed
	}
	return observation, ctx.Err()
}

// ParseTimestampV03 interprets a date as UTC midnight and an offset-bearing
// date-time as an instant.
func ParseTimestampV03(raw string) (time.Time, error) {
	return parseTimestampV03Context(context.Background(), raw)
}

func parseTimestampV03Context(ctx context.Context, raw string) (time.Time, error) {
	if len(raw) == len(time.DateOnly) {
		return time.Parse(time.DateOnly, raw)
	}
	invalid := fmt.Errorf("expected an RFC3339 date or offset-bearing date-time")
	if len(raw) < len("2006-01-02T15:04:05Z") || raw[13] != ':' {
		return time.Time{}, invalid
	}
	normalized := raw
	if normalized[10] == 't' {
		normalized = normalized[:10] + "T" + normalized[11:]
	}
	switch {
	case strings.HasSuffix(normalized, "z"):
		normalized = normalized[:len(normalized)-1] + "Z"
	case strings.HasSuffix(normalized, "-00:00"):
		normalized = normalized[:len(normalized)-6] + "Z"
	}
	normalized, valid, err := boundedRFC3339ForParseContext(ctx, normalized)
	if err != nil {
		return time.Time{}, err
	}
	if !valid {
		return time.Time{}, invalid
	}
	return ParseOffsetDateTime(normalized)
}

// ValidActorV03Context accepts named actors and opaque scheme identifiers.
func ValidActorV03Context(ctx context.Context, actor string) (bool, error) {
	scheme, id, hasScheme := strings.Cut(actor, ":")
	if hasScheme && !strings.Contains(scheme, "/") {
		if scheme == "" {
			return false, ctx.Err()
		}
		for i, c := range scheme {
			if i%contextStringChunkSize == 0 && ctx.Err() != nil {
				return false, ctx.Err()
			}
			letter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
			if !letter && (i == 0 || !(c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.')) {
				return false, nil
			}
		}
		return nonBlankStringValueContext(ctx, id)
	}
	return validActorContext(ctx, actor)
}

// TrustTierForVersionContext selects the trust semantics of the effective
// document contract. Standalone callers can retain the original TrustTier API.
func (d Document) TrustTierForVersionContext(ctx context.Context, version VersionResolution) (TrustTier, error) {
	if version.Effective == OKFVersion03 {
		state, err := d.Frontmatter.TrustStateV03Context(ctx)
		return state.Tier, err
	}
	return d.TrustTierContext(ctx)
}

// StatusStateV03Context applies the OKF 0.3 empty-value rules to status.
func (f Frontmatter) StatusStateV03Context(ctx context.Context) (StatusState, error) {
	node, present, err := f.SemanticGetContext(ctx, "status")
	if err != nil {
		return StatusState{}, err
	}
	if !present || node != nil && emptyV03Value(node) {
		return StatusState{Valid: true, Effective: StatusStable}, nil
	}
	state, err := strictStringNodeStateContext(ctx, node, true)
	if err != nil {
		return StatusState{}, err
	}
	return StatusState{Present: true, Valid: state.Valid, Effective: state.Value, Raw: state.Raw}, ctx.Err()
}

// StatusStateForVersionContext projects lifecycle with the selected contract.
func (d Document) StatusStateForVersionContext(ctx context.Context, version VersionResolution) (StatusState, error) {
	if version.Effective == OKFVersion03 {
		return d.Frontmatter.StatusStateV03Context(ctx)
	}
	return d.StatusStateContext(ctx)
}
