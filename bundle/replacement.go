package bundle

import (
	"context"
	"strings"
)

// ReplacementState explains the result of resolving an OKF 0.3 replacement.
type ReplacementState string

const (
	ReplacementCurrent     ReplacementState = "current"
	ReplacementUnavailable ReplacementState = "unavailable"
	ReplacementMissing     ReplacementState = "missing"
	ReplacementCycle       ReplacementState = "cycle"
	ReplacementNonCurrent  ReplacementState = "non-current"
)

// ResolveCurrentConceptContext follows superseded_by from a deprecated
// concept to its current replacement. A stable or draft initial concept is
// current. Other statuses and unresolved chains return no target.
func (b *Bundle) ResolveCurrentConceptContext(ctx context.Context, id ConceptID) (ConceptID, ReplacementState, error) {
	if err := ctx.Err(); err != nil {
		return ConceptID{}, "", err
	}
	if b == nil || b.discoveryVersion.Effective != OKFVersion03 {
		return ConceptID{}, ReplacementUnavailable, nil
	}
	visited := make(map[string]struct{})
	for {
		key, err := conceptIDStringContext(ctx, id)
		if err != nil {
			return ConceptID{}, "", err
		}
		if _, seen := visited[key]; seen {
			return ConceptID{}, ReplacementCycle, nil
		}
		visited[key] = struct{}{}
		index, found, err := lookupStringMapContext(ctx, b.byID, key)
		if err != nil {
			return ConceptID{}, "", err
		}
		if !found {
			return ConceptID{}, ReplacementMissing, nil
		}
		concept := b.concepts[index]
		status, err := concept.Document.Frontmatter.StatusStateV03Context(ctx)
		if err != nil {
			return ConceptID{}, "", err
		}
		switch status.Effective {
		case StatusStable, StatusDraft:
			return cloneCurrentConceptID(ctx, concept.ID)
		case StatusDeprecated:
			// Only deprecated concepts can forward knowledge requests.
		default:
			return ConceptID{}, ReplacementNonCurrent, nil
		}
		replacement, err := concept.Document.Frontmatter.semanticStringStateContext(ctx, "superseded_by")
		if err != nil {
			return ConceptID{}, "", err
		}
		if !replacement.Present {
			return ConceptID{}, ReplacementUnavailable, nil
		}
		if !replacement.Valid {
			return ConceptID{}, ReplacementMissing, nil
		}
		target, _, err := b.ResolvePathValueForContext(ctx, concept.Path, replacement.Value, PathFieldSupersededBy)
		if err != nil {
			return ConceptID{}, "", err
		}
		if !target.Exists || !strings.HasSuffix(target.Path, ".md") {
			return ConceptID{}, ReplacementMissing, nil
		}
		id, err = ConceptIDFromPath("", target.Path)
		if err != nil {
			return ConceptID{}, ReplacementMissing, nil
		}
	}
}

func cloneCurrentConceptID(ctx context.Context, id ConceptID) (ConceptID, ReplacementState, error) {
	owned, err := cloneConceptIDContext(ctx, id)
	if err != nil {
		return ConceptID{}, "", err
	}
	return owned, ReplacementCurrent, nil
}
