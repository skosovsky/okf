package bundle

import (
	"context"
	"net/url"
	"strings"

	"github.com/skosovsky/okf/internal/markdownowner"
)

// EscapePathV03 encodes the bytes of a local bundle path as a URI reference.
// Slashes remain path separators. The input is a path, not an existing URI.
func EscapePathV03(path string) string {
	const hex = "0123456789ABCDEF"
	var result strings.Builder
	result.Grow(len(path))
	for i := 0; i < len(path); i++ {
		c := path[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '-' || c == '.' || c == '_' || c == '~' || c == '/' {
			result.WriteByte(c)
			continue
		}
		result.WriteByte('%')
		result.WriteByte(hex[c>>4])
		result.WriteByte(hex[c&15])
	}
	return result.String()
}

func (b *Bundle) resolveURIReferenceContext(
	ctx context.Context, documentPath, raw string, sourceResource, rootFallback bool,
) (ResolvedPathValue, bool, error) {
	value, err := trimSpaceStringContext(ctx, raw)
	if err != nil || value == "" {
		return ResolvedPathValue{}, false, err
	}
	path, suffix, err := splitPathValueSuffixContext(ctx, value)
	if err != nil {
		return ResolvedPathValue{}, false, err
	}
	external, err := looksURLLikeContext(ctx, path)
	if err != nil {
		return ResolvedPathValue{}, false, err
	}
	if external {
		return ResolvedPathValue{Raw: raw, Suffix: suffix, Kind: PathValueURL}, true, nil
	}
	decoded, decodeErr := url.PathUnescape(path)
	if decodeErr != nil || path == "" || strings.ContainsAny(decoded, "\\\x00") ||
		strings.Count(path, "/") != strings.Count(decoded, "/") {
		return ResolvedPathValue{Raw: raw, Suffix: suffix, Kind: PathValueAmbiguous}, true, ctx.Err()
	}

	local, err := b.resolveLocalPathValueContext(ctx, documentPath, raw, decoded, suffix)
	if err != nil || local.Exists {
		return local, err == nil, err
	}
	if local.Path == "" {
		local.Kind = PathValueURL
		return local, true, nil
	}
	if rootFallback && !strings.HasPrefix(decoded, "/") {
		fallback, err := b.resolveLocalPathValueContext(ctx, documentPath, raw, "/"+decoded, suffix)
		if err != nil {
			return ResolvedPathValue{}, false, err
		}
		if fallback.Exists {
			fallback.Kind = PathValueRelative
			fallback.RootFallback = true
			return fallback, true, nil
		}
	}
	if sourceResource {
		local, err = classifyUncapturedLocalPathContext(ctx, PathFieldSourceResource, decoded, local, raw, suffix)
	}
	return local, err == nil, err
}

func (b *Bundle) resolveConceptLinkContext(ctx context.Context, source Concept, link Link) (ConceptID, bool, error) {
	if b.discoveryVersion.Effective != OKFVersion03 {
		return link.ResolveContext(ctx, source.ID)
	}
	path, _, err := splitPathValueSuffixContext(ctx, link.Target)
	if err != nil || strings.HasSuffix(path, "/") {
		return ConceptID{}, false, err
	}
	local, ok, err := b.ResolveMarkdownTargetV03Context(ctx, source.Path, link.Target)
	if err != nil || !ok || !strings.HasSuffix(local.Path, ".md") {
		return ConceptID{}, false, err
	}
	target, err := ConceptIDFromPath("", local.Path)
	if err != nil {
		return ConceptID{}, false, nil
	}
	if target.String() == source.ID.String() || local.Exists && !b.Contains(target) {
		return ConceptID{}, false, ctx.Err()
	}
	return target, true, ctx.Err()
}

// PathConceptEdge links a concept to a captured concept identified by a
// frontmatter path field.
type PathConceptEdge struct {
	FieldPath string
	Target    ConceptID
	Raw       string
}

func (b *Bundle) PathConceptEdgesContext(ctx context.Context, id ConceptID) ([]PathConceptEdge, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b == nil || b.discoveryVersion.Effective != OKFVersion03 {
		return nil, nil
	}
	observed, found, err := b.ConceptPathValueObservationsContext(ctx, id)
	if !found || err != nil {
		return nil, err
	}
	concept := b.concepts[b.byID[id.String()]]
	var edges []PathConceptEdge
	for _, observation := range observed.Values {
		if !observation.Scalar.Valid {
			continue
		}
		if observation.Field == PathFieldSupersededBy {
			status, err := concept.Document.StatusStateContext(ctx)
			if err != nil {
				return nil, err
			}
			if status.Effective != StatusDeprecated {
				continue
			}
		}
		resolved, _, err := b.ResolvePathValueForContext(ctx, concept.Path, observation.Scalar.Value, observation.Field)
		if err != nil {
			return nil, err
		}
		if !resolved.Exists || !strings.HasSuffix(resolved.Path, ".md") {
			continue
		}
		target, err := ConceptIDFromPath("", resolved.Path)
		if err == nil && b.Contains(target) {
			edges = append(edges, PathConceptEdge{FieldPath: observation.FieldPath, Target: target, Raw: resolved.Raw})
		}
	}
	return edges, ctx.Err()
}

// ResolveMarkdownTargetV03Context resolves a Markdown target within the same
// immutable snapshot as frontmatter references. Markdown has no root fallback.
func (b *Bundle) ResolveMarkdownTargetV03Context(ctx context.Context, documentPath, target string) (ResolvedPathValue, bool, error) {
	return b.resolveURIReferenceContext(ctx, documentPath, target, false, false)
}

// ExtractLinksV03Context reads CommonMark inline and reference-style links.
func ExtractLinksV03Context(ctx context.Context, body string) ([]Link, error) {
	owned, err := bytesFromStringContext(ctx, body)
	if err != nil {
		return nil, err
	}
	links, err := markdownowner.LinksContext(ctx, owned)
	if err != nil {
		return nil, err
	}
	out := make([]Link, 0, len(links))
	for _, link := range links {
		kind, err := ClassifyLinkContext(ctx, link.Target)
		if err != nil {
			return nil, err
		}
		out = append(out, Link{Text: link.Text, Target: link.Target, Kind: kind})
	}
	return out, ctx.Err()
}
