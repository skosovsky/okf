package mutation

import (
	"bytes"
	"context"

	"gopkg.in/yaml.v3"
)

func semanticYAMLContext(ctx context.Context, n *yaml.Node) (*yamlSemanticNode, error) {
	if err := validateMutationYAMLGraphContext(ctx, n); err != nil {
		return nil, err
	}
	return semanticValidatedYAMLContext(ctx, n)
}

// semanticValidatedYAMLContext recurses only after the public graph guard.
func semanticValidatedYAMLContext(ctx context.Context, n *yaml.Node) (*yamlSemanticNode, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if n == nil {
		return nil, nil
	}
	out := &yamlSemanticNode{Kind: n.Kind, Tag: n.Tag, Value: n.Value, Anchor: n.Anchor, Alias: n.Alias != nil}
	for _, child := range n.Content {
		semantic, err := semanticValidatedYAMLContext(ctx, child)
		if err != nil {
			return nil, err
		}
		out.Content = append(out.Content, semantic)
	}
	return out, nil
}

func equalSemanticYAMLContext(ctx context.Context, left, right *yamlSemanticNode) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if left == nil || right == nil {
		return left == right, nil
	}
	if left.Kind != right.Kind || left.Tag != right.Tag || left.Anchor != right.Anchor || left.Alias != right.Alias || len(left.Content) != len(right.Content) {
		return false, nil
	}
	valueEqual, err := stringsEqualContext(ctx, left.Value, right.Value)
	if err != nil || !valueEqual {
		return valueEqual, err
	}
	for index := range left.Content {
		equal, err := equalSemanticYAMLContext(ctx, left.Content[index], right.Content[index])
		if err != nil || !equal {
			return equal, err
		}
	}
	return true, ctx.Err()
}

func stringsEqualContext(ctx context.Context, left, right string) (bool, error) {
	if len(left) != len(right) {
		return false, nil
	}
	const chunkSize = 64 << 10
	for at := 0; at < len(left); at += chunkSize {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		end := min(at+chunkSize, len(left))
		if left[at:end] != right[at:end] {
			return false, nil
		}
	}
	return true, ctx.Err()
}

func nodeAtPathContext(ctx context.Context, n *yaml.Node, path []int) (*yaml.Node, error) {
	for _, i := range path {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if n == nil || i < 0 || i >= len(n.Content) {
			return nil, nil
		}
		n = n.Content[i]
	}
	return n, ctx.Err()
}

func nodeAtPathNode(n *yamlSemanticNode, path []int) *yamlSemanticNode {
	for _, i := range path {
		if n == nil || i < 0 || i >= len(n.Content) {
			return nil
		}
		n = n.Content[i]
	}
	return n
}

// yamlNodeSpan returns a frontmatter-local source range for diagnostics. It
// prefers the resolver's exact scalar ownership and otherwise points at the
// parser coordinate token. Collection coordinates are still materially more
// useful than the frontmatter-opening fallback used at the planner boundary.
func (p *presentation) yamlNodeSpan(n *yaml.Node) SourceSpan {
	if p == nil || p.resolver == nil || n == nil {
		return SourceSpan{}
	}
	if n.Kind == yaml.ScalarNode {
		if scalar, err := p.resolver.scalar(n); err == nil {
			return scalar.Span
		}
	}
	at, err := p.resolver.coordinate(n.Line, n.Column)
	if err != nil || at < 0 || at > len(p.yaml) {
		return SourceSpan{}
	}
	end := at
	for end < len(p.yaml) && !yamlSeparationByte(p.yaml[end]) {
		end++
	}
	if end == at {
		if parent := p.parents[n]; parent != nil {
			index := yamlChildIndex(parent, n)
			if parent.Kind == yaml.MappingNode && index > 0 && index%2 == 1 {
				if owner := p.yamlNodeSpan(parent.Content[index-1]); owner.Start < owner.End {
					return owner
				}
			}
			if parent.Kind == yaml.SequenceNode && n.Line >= 1 && n.Line <= len(p.resolver.lines) {
				lineStart := p.resolver.lines[n.Line-1]
				for cursor := lineStart; cursor < at && cursor < len(p.yaml); cursor++ {
					if p.yaml[cursor] == '-' {
						return SourceSpan{Start: cursor, End: cursor + 1}
					}
					if !yamlSeparationByte(p.yaml[cursor]) {
						break
					}
				}
			}
		}
		lineStart := bytes.LastIndexByte(p.yaml[:min(at, len(p.yaml))], '\n') + 1
		cursor := min(at, len(p.yaml))
		for cursor > lineStart && yamlSeparationByte(p.yaml[cursor-1]) {
			cursor--
		}
		tokenEnd := cursor
		for cursor > lineStart && !yamlSeparationByte(p.yaml[cursor-1]) {
			cursor--
		}
		if cursor < tokenEnd {
			return SourceSpan{Start: cursor, End: tokenEnd}
		}
		// yaml.v3 can advance an irregular leading-CR scalar coordinate to the
		// following physical line. If the coordinate line owns no token, the
		// immediately preceding non-separation token is the scalar provenance.
		cursor = min(at, len(p.yaml))
		for cursor > 0 && yamlSeparationByte(p.yaml[cursor-1]) {
			cursor--
		}
		tokenEnd = cursor
		for cursor > 0 && !yamlSeparationByte(p.yaml[cursor-1]) {
			cursor--
		}
		if cursor < tokenEnd {
			return SourceSpan{Start: cursor, End: tokenEnd}
		}
	}
	return SourceSpan{Start: at, End: end}
}

func (p *presentation) yamlNodesSpan(nodes ...*yaml.Node) SourceSpan {
	span := SourceSpan{}
	for _, node := range nodes {
		candidate := p.yamlNodeSpan(node)
		if candidate == (SourceSpan{}) {
			continue
		}
		if span == (SourceSpan{}) || candidate.Start < span.Start {
			span.Start = candidate.Start
		}
		if candidate.End > span.End {
			span.End = candidate.End
		}
	}
	return span
}

func (p *presentation) yamlNodeError(code string, cause error, nodes ...*yaml.Node) error {
	span := p.yamlNodesSpan(nodes...)
	if span == (SourceSpan{}) && p != nil && len(p.yaml) > 0 {
		// yaml.v3 can publish a semantic node whose coordinate falls on an
		// irregular CR boundary that the raw resolver correctly refuses to own.
		// Fail closed with the concrete frontmatter range, never an empty marker.
		span = SourceSpan{Start: 0, End: len(p.yaml)}
	}
	if p != nil {
		return yamlLocalPresentationErrorForSource(code, cause, p.yaml, span)
	}
	return yamlLocalPresentationError(code, cause, span)
}

func (p *presentation) yamlLocalError(code string, cause error, span SourceSpan) error {
	if p == nil {
		return yamlLocalPresentationError(code, cause, span)
	}
	return yamlLocalPresentationErrorForSource(code, cause, p.yaml, span)
}

func mappingKeyNodes(n *yaml.Node, key string) []*yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	out := make([]*yaml.Node, 0, 1)
	for i := 0; i+1 < len(n.Content); i += 2 {
		candidate := n.Content[i]
		if candidate.Kind == yaml.ScalarNode && candidate.Value == key {
			out = append(out, candidate)
		}
	}
	return out
}

func semanticMappingValue(mapping *yamlSemanticNode, key string) *yamlSemanticNode {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Kind == yaml.ScalarNode && mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func semanticMappingAppend(mapping *yamlSemanticNode, key string, value *yamlSemanticNode) {
	mapping.Content = append(mapping.Content,
		&yamlSemanticNode{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		value,
	)
}
