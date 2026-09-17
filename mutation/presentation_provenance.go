package mutation

import (
	"context"

	"gopkg.in/yaml.v3"
)

func (p *presentation) rejectMergedKeyContext(ctx context.Context, n *yaml.Node, touchedKey string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if err := ctx.Err(); err != nil {
			return err
		}
		key, value := n.Content[i], n.Content[i+1]
		if key == nil || key.Tag != "!!merge" {
			continue
		}
		matched, err := mergedValueProvidesKeyContext(ctx, value, touchedKey, make(map[*yaml.Node]bool))
		if err != nil {
			return err
		}
		if matched {
			return p.yamlNodeError("alias_provenance", ErrAmbiguousPresentation, key, value)
		}
	}
	return ctx.Err()
}

func mergedValueProvidesKeyContext(ctx context.Context, n *yaml.Node, touchedKey string, visited map[*yaml.Node]bool) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if n == nil {
		return true, nil
	}
	if visited[n] {
		return false, nil
	}
	visited[n] = true
	if n.Kind == yaml.AliasNode || n.Alias != nil {
		if n.Alias == nil {
			return true, nil
		}
		return mergedValueProvidesKeyContext(ctx, n.Alias, touchedKey, visited)
	}
	switch n.Kind {
	case yaml.SequenceNode:
		for _, child := range n.Content {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			matched, err := mergedValueProvidesKeyContext(ctx, child, touchedKey, visited)
			if err != nil || matched {
				return matched, err
			}
		}
		return false, nil
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			key, value := n.Content[i], n.Content[i+1]
			if key != nil && key.Kind == yaml.ScalarNode && key.Tag == "!!str" {
				equal, err := stringsEqualContext(ctx, key.Value, touchedKey)
				if err != nil || equal {
					return equal, err
				}
			}
			if key != nil && key.Tag == "!!merge" {
				matched, err := mergedValueProvidesKeyContext(ctx, value, touchedKey, visited)
				if err != nil || matched {
					return matched, err
				}
			}
		}
		return false, nil
	default:
		return true, nil
	}
}

func (p *presentation) resolvedScalarPatch(n *yaml.Node, value string) (bytePatch, error) {
	if err := p.requireTouchedProvenance(n); err != nil {
		return bytePatch{}, err
	}
	span, err := p.resolver.scalar(n)
	if err != nil {
		return bytePatch{}, err
	}
	path, ok := p.paths[n]
	if !ok {
		return bytePatch{}, p.yamlLocalError("unindexed_scalar", ErrUnsupportedPresentation, span.Span)
	}
	token, err := yamlScalarTokenContext(p.ctx, value, span.Style)
	if err != nil {
		return bytePatch{}, err
	}
	return bytePatch{Start: p.yamlStart + span.Span.Start, End: p.yamlStart + span.Span.End, Text: []byte(token), edit: &semanticEdit{path: path, before: n.Value, after: value}}, nil
}

// requireTouchedProvenance deliberately walks only the chain that produced the
// value being changed.  That keeps opaque extension YAML lossless while
// refusing an alias, merge, tag, anchor, or duplicate key that could make a
// touched value come from more than one source location.
func (p *presentation) requireTouchedProvenance(n *yaml.Node) error {
	if p == nil || p.ctx == nil {
		return yamlPresentationError("invalid_patch", ErrUnsupportedPresentation, SourceSpan{})
	}
	if _, owned := p.paths[n]; n == nil || !owned {
		return p.yamlNodeError("unowned_touched_node", ErrUnsupportedPresentation, n)
	}
	for current := n; current != nil; current = p.parents[current] {
		if err := p.ctx.Err(); err != nil {
			return err
		}
		if current.Kind == yaml.AliasNode || current.Alias != nil {
			return p.yamlNodeError("alias_provenance", ErrAmbiguousPresentation, current)
		}
		if current.Anchor != "" {
			return p.yamlNodeError("touched_anchor", ErrUnsupportedPresentation, current)
		}
		if err := p.requireNoSyntaxProvenance(current); err != nil {
			return err
		}
		if current.Style&yaml.FlowStyle != 0 {
			return p.yamlNodeError("touched_flow", ErrUnsupportedPresentation, current)
		}
		parent := p.parents[current]
		if parent == nil || parent.Kind != yaml.MappingNode {
			continue
		}
		if parent.Style&yaml.FlowStyle != 0 {
			return p.yamlNodeError("touched_flow", ErrUnsupportedPresentation, parent)
		}
		index := yamlChildIndex(parent, current)
		if index <= 0 || index%2 == 0 {
			return p.yamlNodeError("unowned_touched_node", ErrUnsupportedPresentation, current)
		}
		key := parent.Content[index-1]
		if key.Value == "<<" && key.Tag == "!!merge" {
			return p.yamlNodeError("alias_provenance", ErrAmbiguousPresentation, key, current)
		}
		if err := p.requireScalarKey(key); err != nil {
			return err
		}
		matches := 0
		for i := 0; i+1 < len(parent.Content); i += 2 {
			candidate := parent.Content[i]
			if candidate.Kind == yaml.ScalarNode && candidate.Tag == "!!str" && candidate.Value == key.Value {
				matches++
			}
		}
		if matches > 1 {
			return p.touchedDuplicateKeyError(parent, key.Value)
		}
	}
	return nil
}

// requireMutationPathProvenance proves every owner between the touched node
// and the document root while allowing a flow collection to be replaced as
// one parser-backed value. Alias/merge/duplicate ambiguity is classified
// before shape errors so callers receive a stable fail-closed taxonomy.
func (p *presentation) requireMutationPathProvenance(n *yaml.Node) error {
	if p == nil || p.ctx == nil {
		return yamlPresentationError("invalid_patch", ErrUnsupportedPresentation, SourceSpan{})
	}
	if _, owned := p.paths[n]; n == nil || !owned {
		return p.yamlNodeError("unowned_touched_node", ErrUnsupportedPresentation, n)
	}
	for current := n; current != nil; current = p.parents[current] {
		if err := p.ctx.Err(); err != nil {
			return err
		}
		parent := p.parents[current]
		childIndex := -1
		if parent != nil && parent.Kind == yaml.MappingNode {
			childIndex = yamlChildIndex(parent, current)
			if childIndex < 0 {
				return p.yamlNodeError("unowned_touched_node", ErrUnsupportedPresentation, current, parent)
			}
			if childIndex%2 == 0 {
				return p.yamlNodeError(yamlCodeUnsupportedKey, ErrUnsupportedPresentation, current)
			}
		}
		if current.Kind == yaml.AliasNode || current.Alias != nil {
			return p.yamlNodeError(yamlCodeAliasProvenance, ErrAmbiguousPresentation, current)
		}
		if current.Anchor != "" {
			return p.yamlNodeError(yamlCodeTouchedAnchor, ErrUnsupportedPresentation, current)
		}
		if err := p.requireNoSyntaxProvenance(current); err != nil {
			return err
		}
		if parent == nil || parent.Kind != yaml.MappingNode {
			continue
		}
		key := parent.Content[childIndex-1]
		if key == nil || key.Kind == yaml.AliasNode || key.Alias != nil || key.Tag == "!!merge" || key.Value == "<<" {
			return p.yamlNodeError(yamlCodeAliasProvenance, ErrAmbiguousPresentation, key, current)
		}
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return p.yamlNodeError(yamlCodeUnsupportedKey, ErrUnsupportedPresentation, key)
		}
		if err := p.rejectMergedKeyContext(p.ctx, parent, key.Value); err != nil {
			return err
		}
		if nodes := mappingKeyNodes(parent, key.Value); len(nodes) > 1 {
			return p.yamlNodeError(yamlCodeDuplicateTouchedKey, ErrAmbiguousPresentation, nodes...)
		}
	}
	return nil
}

func yamlChildIndex(parent, child *yaml.Node) int {
	for i, candidate := range parent.Content {
		if candidate == child {
			return i
		}
	}
	return -1
}

func (p *presentation) yamlChildIndexContext(ctx context.Context, parent, child *yaml.Node) (int, error) {
	if err := ctx.Err(); err != nil {
		return -1, err
	}
	path, owned := p.paths[child]
	if !owned || len(path) == 0 {
		return -1, nil
	}
	index := path[len(path)-1]
	if index < 0 || index >= len(parent.Content) || parent.Content[index] != child || p.parents[child] != parent {
		return -1, nil
	}
	return index, ctx.Err()
}

func (p *presentation) touchedDuplicateKeyError(parent *yaml.Node, key string) error {
	nodes := mappingKeyNodes(parent, key)
	switch key {
	case "relations":
		return p.yamlNodeError("duplicate_relations", ErrAmbiguousPresentation, nodes...)
	case "target":
		return p.yamlNodeError("duplicate_target", ErrAmbiguousPresentation, nodes...)
	case "id", "anchor":
		return p.yamlNodeError("duplicate_canonical_fragment", ErrAmbiguousPresentation, nodes...)
	}
	if owner := p.mappingOwnerKey(parent); owner == "relations" {
		return p.yamlNodeError("duplicate_relation_type", ErrAmbiguousPresentation, nodes...)
	}
	return p.yamlNodeError("duplicate_touched_key", ErrAmbiguousPresentation, nodes...)
}

func (p *presentation) mappingOwnerKey(mapping *yaml.Node) string {
	// This helper is intentionally syntax-free: callers use it only for error
	// classification after provenance for the actual route has been proved.
	owner := p.parents[mapping]
	if owner == nil || owner.Kind != yaml.MappingNode {
		return ""
	}
	index := yamlChildIndex(owner, mapping)
	if index <= 0 || index%2 == 0 {
		return ""
	}
	key := owner.Content[index-1]
	if key.Kind == yaml.ScalarNode && key.Tag == "!!str" {
		return key.Value
	}
	return ""
}

// requireScalarKey proves a structural key against the same raw scalar
// resolver used for values. Plain, single-quoted, and double-quoted string
// keys are supported; complex, tagged, anchored, aliased, flow, and block
// scalar keys remain fail-closed.
func (p *presentation) requireScalarKey(key *yaml.Node) error {
	if key == nil || key.Kind == yaml.AliasNode || key.Alias != nil {
		return p.yamlNodeError("alias_provenance", ErrAmbiguousPresentation, key)
	}
	if key.Tag == "!!merge" {
		return p.yamlNodeError("alias_provenance", ErrAmbiguousPresentation, key)
	}
	if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || key.Anchor != "" ||
		key.Style&(yaml.LiteralStyle|yaml.FoldedStyle|yaml.FlowStyle|yaml.TaggedStyle) != 0 {
		return p.yamlNodeError("unsupported_key", ErrUnsupportedPresentation, key)
	}
	if err := p.requireNoSyntaxProvenance(key); err != nil {
		return err
	}
	if _, err := p.resolver.scalar(key); err != nil {
		return err
	}
	return nil
}

func (p *presentation) requireNoSyntaxProvenance(n *yaml.Node) error {
	at, err := p.resolver.coordinate(n.Line, n.Column)
	if err != nil {
		return err
	}
	if provenance, ok, err := p.resolver.nodeSyntaxProvenanceContext(n, at); err != nil {
		return err
	} else if ok {
		if provenance.Start == provenance.End {
			return p.yamlNodeError(yamlCodeExplicitTag, ErrUnsupportedPresentation, n)
		}
		return p.yamlLocalError("explicit_tag", ErrUnsupportedPresentation, provenance)
	}
	return nil
}

// requireInsertionProvenance proves that the collection and every mapping that
// owns it belong to the lossless block-YAML subset. Keeping this at the
// presentation boundary makes a new insertion branch unable to bypass it.

func (p *presentation) requireInsertionProvenanceContext(ctx context.Context, n *yaml.Node) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if n != nil && (n.Kind == yaml.AliasNode || n.Alias != nil) {
		return p.yamlNodeError(yamlCodeAliasProvenance, ErrAmbiguousPresentation, n)
	}
	if n == nil || (n.Kind != yaml.MappingNode && n.Kind != yaml.SequenceNode) {
		return p.yamlNodeError("unsupported_block", ErrUnsupportedPresentation, n)
	}
	if err := p.requireTouchedProvenance(n); err != nil {
		return err
	}
	return ctx.Err()
}
