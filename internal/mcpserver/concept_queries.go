package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/skosovsky/okf/bundle"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const (
	defaultConceptQueryLimit = 20
	maxConceptQueryLimit     = 100
)

var errConceptQueryNotFound = errors.New("concept not found")

// A query is evaluated against one immutable Bundle snapshot. V1 deliberately
// has no pagination: total and truncated make the bounded result explicit.
type conceptQueryCard struct {
	ID          string         `json:"id"`
	Path        string         `json:"path"`
	Type        string         `json:"type"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Tags        []string       `json:"tags"`
	Status      *string        `json:"status"`
	StatusState statusStateDTO `json:"status_state"`
	Trust       string         `json:"trust"`
	Stale       *bool          `json:"stale"`
	StaleAfter  *string        `json:"stale_after"`
}

type conceptSearchHit struct {
	Concept conceptQueryCard `json:"concept"`
	Match   string           `json:"match"`
}

type conceptSearchResult struct {
	Query           string             `json:"query"`
	TemporalProfile string             `json:"temporal_profile"`
	AsOf            *string            `json:"as_of"`
	Total           int                `json:"total"`
	Truncated       bool               `json:"truncated"`
	Hits            []conceptSearchHit `json:"hits"`
}

// Explicit edge kinds keep Markdown navigation separate from the relations
// extension. Provenance resources appear in Sources, never as concept edges.
type conceptNeighborEdge struct {
	From         string           `json:"from"`
	To           string           `json:"to"`
	Kind         string           `json:"kind"`
	RelationType *string          `json:"relation_type"`
	Source       conceptQueryCard `json:"source"`
	Target       conceptQueryCard `json:"target"`
}

type conceptSourceLink struct {
	ID       string `json:"id"`
	Resource string `json:"resource"`
	Title    string `json:"title"`
}

type conceptNeighborsResult struct {
	Concept         conceptQueryCard      `json:"concept"`
	TemporalProfile string                `json:"temporal_profile"`
	AsOf            *string               `json:"as_of"`
	Direction       string                `json:"direction"`
	Kinds           []string              `json:"kinds"`
	Total           int                   `json:"total"`
	Truncated       bool                  `json:"truncated"`
	Edges           []conceptNeighborEdge `json:"edges"`
	Sources         []conceptSourceLink   `json:"sources"`
}

func normalizedConceptQuery(value string) string {
	return norm.NFC.String(cases.Fold().String(value))
}

func conceptCardContext(ctx context.Context, concept bundle.Concept, asOf *time.Time, profile bundle.TemporalProfile) (conceptQueryCard, error) {
	if err := ctx.Err(); err != nil {
		return conceptQueryCard{}, err
	}
	frontmatter := concept.Document.Frontmatter
	typ, _ := frontmatter.Type()
	title, _ := frontmatter.Title()
	description, _ := frontmatter.Description()
	trust, err := concept.Document.TrustTierContext(ctx)
	if err != nil {
		return conceptQueryCard{}, err
	}
	status := statusStateFromBundle(concept.Document.StatusState())
	card := conceptQueryCard{
		ID: concept.ID.String(), Path: concept.Path, Type: typ, Title: title,
		Description: description, Tags: frontmatter.Tags(), Status: status.Effective, StatusState: status,
		Trust: string(trust),
	}
	if card.Tags == nil {
		card.Tags = []string{}
	}
	staleAfter, err := concept.Document.Frontmatter.StaleAfterForProfileContext(ctx, profile)
	if err != nil {
		return conceptQueryCard{}, err
	}
	if staleAfter.Value.State == bundle.TemporalValid {
		value := staleAfter.Value.Raw
		card.StaleAfter = &value
	}
	if asOf != nil {
		stale, known, err := concept.Document.Frontmatter.IsStaleForProfileContext(ctx, *asOf, profile)
		if err != nil {
			return conceptQueryCard{}, err
		}
		if known {
			card.Stale = &stale
		}
	}
	return card, ctx.Err()
}

func searchConceptSnapshot(ctx context.Context, loaded *bundle.Bundle, query string, limit int, asOf *time.Time, profile bundle.TemporalProfile) (conceptSearchResult, error) {
	result := conceptSearchResult{Query: query, TemporalProfile: string(profile), Hits: []conceptSearchHit{}}
	if asOf != nil {
		value := formatConceptQueryAsOf(*asOf, profile)
		result.AsOf = &value
	}
	if strings.TrimSpace(query) == "" || limit < 1 || limit > maxConceptQueryLimit {
		return result, fmt.Errorf("invalid concept search query or limit")
	}
	needle := normalizedConceptQuery(query)
	ids, err := loaded.ConceptIDsContext(ctx)
	if err != nil {
		return result, err
	}
	if len(ids) > maxConceptItems {
		return result, errMCPConceptCountLimit
	}
	type ranked struct {
		hit   conceptSearchHit
		score int
	}
	matches := make([]ranked, 0)
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		concept, ok, err := loaded.GetContext(ctx, id)
		if err != nil {
			return result, err
		}
		if !ok {
			return result, fmt.Errorf("captured concept index is inconsistent")
		}
		card, err := conceptCardContext(ctx, concept, asOf, profile)
		if err != nil {
			return result, err
		}
		match, score := "", 0
		for _, field := range []struct {
			name, text string
			rank       int
		}{
			{"id", card.ID, 6}, {"title", card.Title, 5}, {"description", card.Description, 4},
			{"body", concept.Document.Body, 1},
		} {
			if strings.Contains(normalizedConceptQuery(field.text), needle) && field.rank > score {
				match, score = field.name, field.rank
			}
		}
		for _, tag := range card.Tags {
			if strings.Contains(normalizedConceptQuery(tag), needle) && score < 3 {
				match, score = "tags", 3
			}
		}
		if score != 0 {
			matches = append(matches, ranked{conceptSearchHit{card, match}, score})
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].hit.Concept.ID < matches[j].hit.Concept.ID
	})
	if err := ctx.Err(); err != nil {
		return result, err
	}
	result.Total = len(matches)
	result.Truncated = len(matches) > limit
	for _, match := range matches[:min(limit, len(matches))] {
		result.Hits = append(result.Hits, match.hit)
	}
	return result, nil
}

func neighborConceptSnapshot(ctx context.Context, loaded *bundle.Bundle, id bundle.ConceptID, direction string, kinds []string, limit int, asOf *time.Time, profile bundle.TemporalProfile) (conceptNeighborsResult, error) {
	result := conceptNeighborsResult{TemporalProfile: string(profile), Direction: direction, Kinds: kinds, Edges: []conceptNeighborEdge{}, Sources: []conceptSourceLink{}}
	if asOf != nil {
		value := formatConceptQueryAsOf(*asOf, profile)
		result.AsOf = &value
	}
	if limit < 1 || limit > maxConceptQueryLimit {
		return result, fmt.Errorf("invalid neighbor limit")
	}
	selected, ok, err := loaded.GetContext(ctx, id)
	if err != nil {
		return result, err
	}
	if !ok {
		return result, errConceptQueryNotFound
	}
	result.Concept, err = conceptCardContext(ctx, selected, asOf, profile)
	if err != nil {
		return result, err
	}
	includeNavigation, includeRelations, includeSources := false, false, false
	for _, kind := range kinds {
		switch kind {
		case "navigation":
			includeNavigation = true
		case "relation":
			includeRelations = true
		case "source":
			includeSources = true
		default:
			return result, fmt.Errorf("unknown neighbor kind")
		}
	}
	if includeSources && direction != "in" {
		sources, err := selected.Document.SourcesContext(ctx)
		if err != nil {
			return result, err
		}
		for _, source := range sources {
			result.Sources = append(result.Sources, conceptSourceLink{source.ID, source.Resource, source.Title})
		}
		sort.Slice(result.Sources, func(i, j int) bool {
			if result.Sources[i].ID != result.Sources[j].ID {
				return result.Sources[i].ID < result.Sources[j].ID
			}
			if result.Sources[i].Resource != result.Sources[j].Resource {
				return result.Sources[i].Resource < result.Sources[j].Resource
			}
			return result.Sources[i].Title < result.Sources[j].Title
		})
		if len(result.Sources) > maxConceptQueryLimit {
			return result, errMCPResourceLimit
		}
	}
	ids, err := loaded.ConceptIDsContext(ctx)
	if err != nil {
		return result, err
	}
	if len(ids) > maxConceptItems {
		return result, errMCPConceptCountLimit
	}
	for _, sourceID := range ids {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if direction == "out" && sourceID.String() != id.String() {
			continue
		}
		if includeNavigation {
			links, err := loaded.LinksFromContext(ctx, sourceID)
			if err != nil {
				return result, err
			}
			for _, link := range links {
				if !link.Exists || (sourceID.String() != id.String() && link.Target.String() != id.String()) || (direction == "in" && link.Target.String() != id.String()) {
					continue
				}
				edge, err := neighborEdgeContext(ctx, loaded, sourceID, link.Target, "navigation", nil, asOf, profile)
				if err != nil {
					return result, err
				}
				result.Edges = append(result.Edges, edge)
				if len(result.Edges) > maxMCPArgumentItems {
					return result, errMCPResourceLimit
				}
			}
		}
		if includeRelations {
			relations, err := loaded.SemanticLinksFromContext(ctx, sourceID)
			if err != nil {
				return result, err
			}
			for _, relation := range relations {
				if !relation.TargetExists || (sourceID.String() != id.String() && relation.Target.ID.String() != id.String()) || (direction == "in" && relation.Target.ID.String() != id.String()) {
					continue
				}
				kind := relation.Type
				edge, err := neighborEdgeContext(ctx, loaded, relation.Source.ID, relation.Target.ID, "relation", &kind, asOf, profile)
				if err != nil {
					return result, err
				}
				edge.From, edge.To = relation.Source.String(), relation.Target.String()
				result.Edges = append(result.Edges, edge)
				if len(result.Edges) > maxMCPArgumentItems {
					return result, errMCPResourceLimit
				}
			}
		}
	}
	sort.Slice(result.Edges, func(i, j int) bool {
		a, b := result.Edges[i], result.Edges[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		return stringValue(a.RelationType) < stringValue(b.RelationType)
	})
	if err := ctx.Err(); err != nil {
		return result, err
	}
	result.Total = len(result.Edges)
	result.Truncated = len(result.Edges) > limit
	result.Edges = result.Edges[:min(limit, len(result.Edges))]
	return result, nil
}

func neighborEdgeContext(ctx context.Context, loaded *bundle.Bundle, sourceID, targetID bundle.ConceptID, kind string, relationType *string, asOf *time.Time, profile bundle.TemporalProfile) (conceptNeighborEdge, error) {
	source, ok, err := loaded.GetContext(ctx, sourceID)
	if err != nil {
		return conceptNeighborEdge{}, err
	}
	if !ok {
		return conceptNeighborEdge{}, fmt.Errorf("missing source concept")
	}
	target, ok, err := loaded.GetContext(ctx, targetID)
	if err != nil {
		return conceptNeighborEdge{}, err
	}
	if !ok {
		return conceptNeighborEdge{}, fmt.Errorf("missing target concept")
	}
	sourceCard, err := conceptCardContext(ctx, source, asOf, profile)
	if err != nil {
		return conceptNeighborEdge{}, err
	}
	targetCard, err := conceptCardContext(ctx, target, asOf, profile)
	if err != nil {
		return conceptNeighborEdge{}, err
	}
	return conceptNeighborEdge{From: sourceID.String(), To: targetID.String(), Kind: kind, RelationType: relationType, Source: sourceCard, Target: targetCard}, nil
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func formatConceptQueryAsOf(value time.Time, profile bundle.TemporalProfile) string {
	return formatMCPTemporalReference(value, profile)
}
