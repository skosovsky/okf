package mutation

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/internal/documentlayout"
	"github.com/skosovsky/okf/store"
	"gopkg.in/yaml.v3"
)

func (s *planState) migrateV03YAML(affected []bundle.RelationRef, details []string) ([]bundle.RelationRef, []string, error) {
	files, err := s.bundle.MarkdownFilesContext(s.ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, file := range files {
		data, err := s.overlay.ReadFile(s.ctx, file)
		if err != nil {
			return nil, nil, err
		}
		next, count, err := migrateV03YAMLScalarsContext(s.ctx, data)
		if err != nil {
			if s.ctx.Err() != nil {
				return nil, nil, s.ctx.Err()
			}
			return nil, nil, invalid("migration_v03_yaml_scalar", []store.Diagnostic{{
				Code: "migration_v03_yaml_scalar", File: file, Severity: store.DiagnosticError, Message: err.Error(),
			}}, err)
		}
		if count == 0 {
			continue
		}
		if err := s.overlay.PutContext(s.ctx, file, next); err != nil {
			return nil, nil, err
		}
		id, err := bundle.ConceptIDFromPath("", file)
		if err != nil {
			return nil, nil, err
		}
		if s.bundle.Contains(id) {
			affected = append(affected, bundle.RelationRef{ID: id})
		}
		details = append(details, fmt.Sprintf("%s: preserved %d scalar value(s) across YAML Core resolution", file, count))
	}
	return affected, details, s.ctx.Err()
}

func migrateV03YAMLScalarsContext(ctx context.Context, data []byte) ([]byte, int, error) {
	layout, hasFrontmatter, err := documentlayout.SplitContext(ctx, data)
	if err != nil || !hasFrontmatter {
		return data, 0, err
	}
	legacy, err := bundle.ParseFrontmatterContext(ctx, string(data[layout.YAMLStart:layout.YAMLEnd]))
	if err != nil {
		return nil, 0, err
	}
	expected, err := legacy.YAMLNodeContext(ctx)
	if err != nil {
		return nil, 0, err
	}
	resolver, err := newYAMLResolverContext(ctx, data[layout.YAMLStart:layout.YAMLEnd])
	if err != nil {
		return nil, 0, err
	}
	type fieldNode struct {
		node *yaml.Node
		path string
	}
	stack := []fieldNode{{node: expected}}
	var patches []bytePatch
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		field := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		node := field.node
		switch node.Kind {
		case yaml.MappingNode:
			for i := len(node.Content) - 2; i >= 0; i -= 2 {
				key, value := node.Content[i], node.Content[i+1]
				path := "." + key.Value
				if strings.Contains(key.Value, ".") || key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
					path = ".?"
				}
				stack = append(stack, fieldNode{value, field.path + path}, fieldNode{node: key})
			}
		case yaml.SequenceNode:
			path := field.path
			if path != ".sources" && path != ".verified" {
				path += "[]"
			}
			for i := len(node.Content) - 1; i >= 0; i-- {
				stack = append(stack, fieldNode{node.Content[i], path})
			}
		case yaml.ScalarNode:
			temporal := isMigrationTemporalField(field.path)
			token, value, err := bundle.YAMLScalarForV03Migration(node, temporal)
			if err != nil {
				return nil, 0, fmt.Errorf("YAML line %d, column %d: %w", node.Line, node.Column, err)
			}
			if token == "" {
				if node.Tag == "!!timestamp" && node.Style == 0 && temporal && node.Anchor == "" {
					node.Tag = "!!str"
				}
				continue
			}
			span, err := migrationV03ScalarSpan(resolver, node)
			if err != nil {
				return nil, 0, fmt.Errorf("YAML line %d, column %d: %w", node.Line, node.Column, err)
			}
			if span.Start == span.End {
				continue
			}
			if node.Style&(yaml.SingleQuotedStyle|yaml.DoubleQuotedStyle) != 0 {
				quote := string(resolver.source[span.Start])
				token = quote + token + quote
			}
			patches = append(patches, bytePatch{Start: layout.YAMLStart + span.Start, End: layout.YAMLStart + span.End, Text: []byte(token)})
			node.Value = value
		}
	}
	next := data
	if len(patches) > 0 {
		next, err = applyBytePatchesContext(ctx, data, patches)
		if err != nil {
			return nil, 0, err
		}
	}
	current, err := bundle.ParseDocumentForVersionContext(ctx, string(next), bundle.VersionResolution{Effective: bundle.OKFVersion03})
	if err != nil {
		return nil, 0, fmt.Errorf("cannot read migrated YAML Core mapping: %w", err)
	}
	actual, err := current.Frontmatter.YAMLNodeContext(ctx)
	if err != nil {
		return nil, 0, err
	}
	left, err := semanticYAMLContext(ctx, expected)
	if err != nil {
		return nil, 0, err
	}
	right, err := semanticYAMLContext(ctx, actual)
	if err != nil {
		return nil, 0, err
	}
	equal, err := equalSemanticYAMLContext(ctx, left, right)
	if err != nil {
		return nil, 0, err
	}
	if !equal {
		return nil, 0, fmt.Errorf("YAML Core changes an ambiguous scalar or mapping; make its type explicit before migrating")
	}
	return next, len(patches), nil
}

func isMigrationTemporalField(path string) bool {
	switch path {
	case ".generated.at", ".verified.at", ".stale_after", ".usage_window.from", ".usage_window.to",
		".sources.last_modified", ".sources.usage_window.from", ".sources.usage_window.to":
		return true
	default:
		return false
	}
}

// The scalar's YAML coordinates and exact original bytes identify the token.
// The final Core parse independently checks the whole mapping and its aliases.
func migrationV03ScalarSpan(resolver *yamlResolver, node *yaml.Node) (SourceSpan, error) {
	at, err := resolver.coordinate(node.Line, node.Column)
	if err != nil {
		return SourceSpan{}, err
	}
	source := resolver.source
	for _, property := range []string{"&" + node.Anchor, "!!int", "&" + node.Anchor} {
		if property == "&" || (property == "!!int" && node.Tag != "!!int") {
			continue
		}
		end := at + len(property)
		if end < len(source) && bytes.HasPrefix(source[at:], []byte(property)) && yamlSeparationByte(source[end]) {
			at = end
			for at < len(source) && yamlSeparationByte(source[at]) {
				if err := resolver.ctx.Err(); err != nil {
					return SourceSpan{}, err
				}
				at++
			}
		}
	}
	if node.Tag == "!!str" && at+1 < len(source) && source[at] == '!' && yamlSeparationByte(source[at+1]) {
		return SourceSpan{}, nil
	}
	if node.Tag == "!!int" && at < len(source) && (source[at] == '\'' || source[at] == '"') {
		end, _, err := resolver.scalarEnd(node, at)
		return SourceSpan{Start: at, End: end}, err
	}
	end := at + len(node.Value)
	if node.Value == "" || !bytes.HasPrefix(source[at:], []byte(node.Value)) ||
		end < len(source) && !yamlSeparationByte(source[end]) && !strings.ContainsRune(",]}:", rune(source[end])) {
		return SourceSpan{}, fmt.Errorf("cannot prove the original token of %q; rewrite the scalar before migrating", node.Value)
	}
	return SourceSpan{Start: at, End: end}, nil
}
