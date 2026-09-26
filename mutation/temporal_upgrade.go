package mutation

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/skosovsky/okf/store"
	"gopkg.in/yaml.v3"
)

func (s *planState) upgradeTemporalConcept(op store.UpgradeTemporalConcept, operationName string) error {
	return s.applyV02ConceptOperation(op.Concept, operationName, op, func(p *presentation) ([]byte, error) {
		var patches []bytePatch
		for _, rewrite := range op.Rewrites() {
			if err := p.ctx.Err(); err != nil {
				return nil, err
			}
			parent, key, err := temporalRewriteParent(p, rewrite.Path)
			if err != nil {
				return nil, err
			}
			match, found, err := p.structuralMappingEntry(parent, key)
			if err != nil {
				return nil, err
			}
			if !found || match.value == nil || match.value.Kind != yaml.ScalarNode || match.value.Value != rewrite.From {
				return nil, fmt.Errorf("%w: temporal field %s no longer equals previewed date", ErrUnsupportedPresentation, rewrite.Path)
			}
			if match.value.Tag != "!!timestamp" && match.value.Tag != "!!str" {
				return nil, fmt.Errorf("%w: temporal field %s is not a date scalar", ErrUnsupportedPresentation, rewrite.Path)
			}
			patch, err := temporalScalarPatch(p, parent, match.value, rewrite.To)
			if err != nil {
				return nil, err
			}
			patches = append(patches, patch)
		}
		return p.patchYAMLContext(p.ctx, patches)
	})
}

func temporalScalarPatch(p *presentation, parent, node *yaml.Node, desired string) (bytePatch, error) {
	if node.Style&yaml.TaggedStyle != 0 {
		return temporalExplicitTagPatch(p, parent, node, desired)
	}
	if err := p.requireTouchedProvenance(node); err != nil {
		return bytePatch{}, err
	}
	span, err := p.resolver.typedScalar(node)
	if err != nil {
		return bytePatch{}, err
	}
	path, ok := p.paths[node]
	if !ok {
		return bytePatch{}, p.yamlLocalError("unindexed_scalar", ErrUnsupportedPresentation, span.Span)
	}
	token := desired
	if span.Style&yaml.SingleQuotedStyle != 0 {
		token = "'" + strings.ReplaceAll(desired, "'", "''") + "'"
	}
	if span.Style&yaml.DoubleQuotedStyle != 0 {
		token = strconv.Quote(desired)
	}
	return bytePatch{Start: p.yamlStart + span.Span.Start, End: p.yamlStart + span.Span.End, Text: []byte(token), edit: &semanticEdit{path: path, before: node.Value, after: desired}}, nil
}

// An explicit core tag begins at the YAML node coordinate, while the scalar
// itself begins after the tag. Patch only the owned scalar token, preserving
// the explicit tag and trailing comment byte-for-byte.
func temporalExplicitTagPatch(p *presentation, parent, node *yaml.Node, desired string) (bytePatch, error) {
	if err := p.requireTouchedProvenance(parent); err != nil {
		return bytePatch{}, err
	}
	if node.Anchor != "" || node.Alias != nil || node.Kind != yaml.ScalarNode || (node.Tag != "!!str" && node.Tag != "!!timestamp") || node.Style&^(yaml.TaggedStyle|yaml.SingleQuotedStyle|yaml.DoubleQuotedStyle) != 0 {
		return bytePatch{}, p.yamlNodeError("unsupported_scalar", ErrUnsupportedPresentation, node)
	}
	path, ok := p.paths[node]
	if !ok {
		return bytePatch{}, p.yamlNodeError("unindexed_scalar", ErrUnsupportedPresentation, node)
	}
	start, err := p.resolver.coordinate(node.Line, node.Column)
	if err != nil {
		return bytePatch{}, err
	}
	if start < 0 || start >= len(p.yaml) {
		return bytePatch{}, p.yamlNodeError("invalid_tag_coordinate", ErrUnsupportedPresentation, node)
	}
	tag := node.Tag
	if !strings.HasPrefix(string(p.yaml[start:]), tag) {
		return bytePatch{}, p.yamlNodeError("unsupported_tag_spelling", ErrUnsupportedPresentation, node)
	}
	valueStart := start + len(tag)
	if valueStart >= len(p.yaml) || (p.yaml[valueStart] != ' ' && p.yaml[valueStart] != '\t') {
		return bytePatch{}, p.yamlNodeError("unsupported_tag_spacing", ErrUnsupportedPresentation, node)
	}
	for valueStart < len(p.yaml) && (p.yaml[valueStart] == ' ' || p.yaml[valueStart] == '\t') {
		valueStart++
	}
	oldToken := node.Value
	newToken := desired
	if node.Style&yaml.SingleQuotedStyle != 0 {
		oldToken = "'" + node.Value + "'"
		newToken = "'" + desired + "'"
	} else if node.Style&yaml.DoubleQuotedStyle != 0 {
		oldToken = `"` + node.Value + `"`
		newToken = strconv.Quote(desired)
	}
	if !strings.HasPrefix(string(p.yaml[valueStart:]), oldToken) {
		return bytePatch{}, p.yamlNodeError("unsupported_tagged_scalar", ErrUnsupportedPresentation, node)
	}
	end := valueStart + len(oldToken)
	if end < len(p.yaml) && p.yaml[end] != ' ' && p.yaml[end] != '\t' && p.yaml[end] != '\r' && p.yaml[end] != '\n' && p.yaml[end] != ',' && p.yaml[end] != '}' && p.yaml[end] != ']' {
		return bytePatch{}, p.yamlNodeError("unsupported_tagged_scalar", ErrUnsupportedPresentation, node)
	}
	return bytePatch{Start: p.yamlStart + valueStart, End: p.yamlStart + end, Text: []byte(newToken), edit: &semanticEdit{path: path, before: node.Value, after: desired}}, nil
}

func temporalRewriteParent(p *presentation, field string) (*yaml.Node, string, error) {
	parts := strings.Split(field, ".")
	if len(parts) == 1 && parts[0] == "stale_after" {
		return p.root, parts[0], nil
	}
	if len(parts) == 2 && parts[0] == "usage_window" && (parts[1] == "from" || parts[1] == "to") {
		window, found, err := p.structuralMappingEntry(p.root, "usage_window")
		if err != nil {
			return nil, "", err
		}
		if !found || window.value.Kind != yaml.MappingNode {
			return nil, "", errors.New("usage_window is missing or malformed")
		}
		return window.value, parts[1], nil
	}
	if len(parts) < 2 || !strings.HasPrefix(parts[0], "sources[") || !strings.HasSuffix(parts[0], "]") {
		return nil, "", fmt.Errorf("unsupported temporal field %q", field)
	}
	index, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(parts[0], "sources["), "]"))
	if err != nil || index < 0 {
		return nil, "", fmt.Errorf("invalid temporal source index in %q", field)
	}
	sources, found, err := p.structuralMappingEntry(p.root, "sources")
	if err != nil {
		return nil, "", err
	}
	if !found || sources.value.Kind != yaml.SequenceNode || index >= len(sources.value.Content) {
		return nil, "", errors.New("sources are missing or malformed")
	}
	item := sources.value.Content[index]
	if item.Kind != yaml.MappingNode {
		return nil, "", errors.New("source item is not a mapping")
	}
	if len(parts) == 2 && parts[1] == "last_modified" {
		return item, parts[1], nil
	}
	if len(parts) == 3 && parts[1] == "usage_window" && (parts[2] == "from" || parts[2] == "to") {
		window, found, err := p.structuralMappingEntry(item, "usage_window")
		if err != nil {
			return nil, "", err
		}
		if !found || window.value.Kind != yaml.MappingNode {
			return nil, "", errors.New("source usage_window is missing or malformed")
		}
		return window.value, parts[2], nil
	}
	return nil, "", fmt.Errorf("unsupported temporal field %q", field)
}
