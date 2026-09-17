package bundle

import (
	"context"
	"crypto/sha256"

	"github.com/skosovsky/okf/internal/markdownowner"
)

// FootnoteReference is one claim marker keyed to a provenance source.
type FootnoteReference struct {
	ID  string
	Raw string
}

// FootnoteDefinition is one prose footnote definition. Text is informative;
// structured provenance is resolved exclusively through Sources.
type FootnoteDefinition struct {
	ID   string
	Text string
	Raw  string
}

// Attribution joins footnote observations to every structured source with the
// same id. Duplicate and unknown ids remain visible.
type Attribution struct {
	ID           string
	NormalizedID string
	Sources      []ProvenanceSource
	References   []FootnoteReference
	Definitions  []FootnoteDefinition
}

// Attributions extracts keyed footnote markers and definitions outside fenced
// and inline code, then joins them to sources[].id. Results are sorted by id so
// source ordering cannot change attribution semantics.
func (d Document) Attributions() []Attribution {
	out, _ := d.AttributionsContext(context.Background())
	return out
}

// AttributionsContext returns the cancellation-aware, deterministic attribution
// projection. Cancellation returns no partial result.
func (d Document) AttributionsContext(ctx context.Context) ([]Attribution, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	type group struct {
		value  Attribution
		rawIDs []string
	}
	byID := make(map[[sha256.Size]byte][]*group)
	ensure := func(id string) (*group, error) {
		if id == "" {
			return nil, ctx.Err()
		}
		normalized, err := normalizeFootnoteLabelContext(ctx, id)
		if err != nil {
			return nil, err
		}
		if normalized == "" {
			return nil, ctx.Err()
		}
		key, err := attributionStringDigestContext(ctx, normalized)
		if err != nil {
			return nil, err
		}
		for _, existing := range byID[key] {
			equal, err := equalStringContext(ctx, existing.value.NormalizedID, normalized)
			if err != nil {
				return nil, err
			}
			if equal {
				existing.rawIDs = append(existing.rawIDs, id)
				return existing, ctx.Err()
			}
		}
		created := &group{
			value:  Attribution{ID: id, NormalizedID: normalized},
			rawIDs: []string{id},
		}
		byID[key] = append(byID[key], created)
		return created, ctx.Err()
	}

	sources, err := d.Frontmatter.sourceStatesContext(ctx)
	if err != nil {
		return nil, err
	}
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !source.ID.Valid {
			continue
		}
		attribution, err := ensure(source.ID.Value)
		if err != nil {
			return nil, err
		}
		if attribution != nil {
			owned, err := cloneProvenanceSourceContext(ctx, source.Value)
			if err != nil {
				return nil, err
			}
			attribution.value.Sources = append(attribution.value.Sources, owned)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	body, err := bytesFromStringContext(ctx, d.Body)
	if err != nil {
		return nil, err
	}
	projection, err := markdownowner.CollectFootnotes(ctx, body)
	if err != nil {
		return nil, err
	}
	for _, definition := range projection.Definitions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		attribution, err := ensure(definition.Label)
		if err != nil {
			return nil, err
		}
		if attribution == nil {
			continue
		}
		rawEnd := definition.ContentSpan.Start
		if rawEnd < definition.Span.Start || rawEnd > len(body) {
			rawEnd = definition.Span.Start
		}
		id, err := stringFromStringContext(ctx, definition.Label)
		if err != nil {
			return nil, err
		}
		text, err := stringFromStringContext(ctx, definition.Content)
		if err != nil {
			return nil, err
		}
		raw, err := attributionTrimmedBytesContext(ctx, body[definition.Span.Start:rawEnd])
		if err != nil {
			return nil, err
		}
		attribution.value.Definitions = append(attribution.value.Definitions, FootnoteDefinition{
			ID: id, Text: text, Raw: raw,
		})
	}
	for _, reference := range projection.References {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		attribution, err := ensure(reference.Label)
		if err != nil {
			return nil, err
		}
		if attribution == nil {
			continue
		}
		id, err := stringFromStringContext(ctx, reference.Label)
		if err != nil {
			return nil, err
		}
		raw, err := attributionBytesContext(ctx, body[reference.Span.Start:reference.Span.End])
		if err != nil {
			return nil, err
		}
		attribution.value.References = append(attribution.value.References, FootnoteReference{
			ID: id, Raw: raw,
		})
	}

	var normalizedIDs []string
	for _, groups := range byID {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, group := range groups {
			normalizedIDs = append(normalizedIDs, group.value.NormalizedID)
		}
	}
	if err := sortCompareContext(ctx, normalizedIDs, compareStringsContext); err != nil {
		return nil, err
	}
	var out []Attribution
	for _, normalized := range normalizedIDs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		key, err := attributionStringDigestContext(ctx, normalized)
		if err != nil {
			return nil, err
		}
		var current *group
		for _, candidate := range byID[key] {
			equal, err := equalStringContext(ctx, candidate.value.NormalizedID, normalized)
			if err != nil {
				return nil, err
			}
			if equal {
				current = candidate
				break
			}
		}
		if err := sortCompareContext(ctx, current.rawIDs, compareStringsContext); err != nil {
			return nil, err
		}
		value := current.value
		value.ID = current.rawIDs[0]
		if err := sortCompareContext(ctx, value.Sources, compareProvenanceSourcesContext); err != nil {
			return nil, err
		}
		if err := sortCompareContext(ctx, value.References, func(ctx context.Context, left, right FootnoteReference) (int, error) {
			result, err := compareStringsContext(ctx, left.ID, right.ID)
			if err != nil || result != 0 {
				return result, err
			}
			return compareStringsContext(ctx, left.Raw, right.Raw)
		}); err != nil {
			return nil, err
		}
		if err := sortCompareContext(ctx, value.Definitions, func(ctx context.Context, left, right FootnoteDefinition) (int, error) {
			result, err := compareStringsContext(ctx, left.ID, right.ID)
			if err != nil || result != 0 {
				return result, err
			}
			result, err = compareStringsContext(ctx, left.Text, right.Text)
			if err != nil || result != 0 {
				return result, err
			}
			return compareStringsContext(ctx, left.Raw, right.Raw)
		}); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func normalizeFootnoteLabelContext(ctx context.Context, label string) (string, error) {
	return markdownowner.NormalizeFootnoteLabelContext(ctx, label)
}

func attributionStringDigestContext(ctx context.Context, value string) ([sha256.Size]byte, error) {
	hash := sha256.New()
	for offset := 0; offset < len(value); offset += 64 << 10 {
		if err := ctx.Err(); err != nil {
			return [sha256.Size]byte{}, err
		}
		_, _ = hash.Write([]byte(value[offset:min(offset+(64<<10), len(value))]))
	}
	if err := ctx.Err(); err != nil {
		return [sha256.Size]byte{}, err
	}
	var out [sha256.Size]byte
	copy(out[:], hash.Sum(nil))
	return out, nil
}

func attributionBytesContext(ctx context.Context, value []byte) (string, error) {
	return stringFromBytesContext(ctx, value)
}

func attributionTrimmedBytesContext(ctx context.Context, value []byte) (string, error) {
	owned, err := stringFromBytesContext(ctx, value)
	if err != nil {
		return "", err
	}
	return trimSpaceStringContext(ctx, owned)
}

func compareProvenanceSourcesContext(ctx context.Context, left, right ProvenanceSource) (int, error) {
	for _, pair := range [][2]string{{left.ID, right.ID}, {left.Resource, right.Resource}, {left.Title, right.Title}, {left.Author, right.Author}, {left.LastModified, right.LastModified}} {
		result, err := compareStringsContext(ctx, pair[0], pair[1])
		if err != nil || result != 0 {
			return result, err
		}
	}
	if (left.UsageCount == nil) != (right.UsageCount == nil) {
		if left.UsageCount == nil {
			return -1, nil
		}
		return 1, nil
	}
	if left.UsageCount != nil && *left.UsageCount != *right.UsageCount {
		if *left.UsageCount < *right.UsageCount {
			return -1, nil
		}
		return 1, nil
	}
	if (left.UsageWindow == nil) != (right.UsageWindow == nil) {
		if left.UsageWindow == nil {
			return -1, nil
		}
		return 1, nil
	}
	if left.UsageWindow != nil {
		result, err := compareStringsContext(ctx, left.UsageWindow.From, right.UsageWindow.From)
		if err != nil || result != 0 {
			return result, err
		}
		return compareStringsContext(ctx, left.UsageWindow.To, right.UsageWindow.To)
	}
	return 0, ctx.Err()
}

func cloneProvenanceSourceContext(ctx context.Context, source ProvenanceSource) (ProvenanceSource, error) {
	var out ProvenanceSource
	var err error
	if out.ID, err = stringFromStringContext(ctx, source.ID); err != nil {
		return ProvenanceSource{}, err
	}
	if out.Resource, err = stringFromStringContext(ctx, source.Resource); err != nil {
		return ProvenanceSource{}, err
	}
	if out.Title, err = stringFromStringContext(ctx, source.Title); err != nil {
		return ProvenanceSource{}, err
	}
	if out.Author, err = stringFromStringContext(ctx, source.Author); err != nil {
		return ProvenanceSource{}, err
	}
	if out.LastModified, err = stringFromStringContext(ctx, source.LastModified); err != nil {
		return ProvenanceSource{}, err
	}
	if source.UsageCount != nil {
		value := *source.UsageCount
		out.UsageCount = &value
	}
	if source.UsageWindow != nil {
		from, err := stringFromStringContext(ctx, source.UsageWindow.From)
		if err != nil {
			return ProvenanceSource{}, err
		}
		to, err := stringFromStringContext(ctx, source.UsageWindow.To)
		if err != nil {
			return ProvenanceSource{}, err
		}
		out.UsageWindow = &UsageWindow{From: from, To: to}
	}
	return out, ctx.Err()
}

func cloneProvenanceSource(source ProvenanceSource) ProvenanceSource {
	if source.UsageCount != nil {
		value := *source.UsageCount
		source.UsageCount = &value
	}
	if source.UsageWindow != nil {
		value := *source.UsageWindow
		source.UsageWindow = &value
	}
	return source
}
