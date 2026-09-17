package mutation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

func validateYAMLRenderValueContext(ctx context.Context, value yamlRenderValue) error {
	if err := walkYAMLRenderValueContext(ctx, value, nil); err != nil {
		return err
	}
	return validateYAMLRenderValueUncheckedContext(ctx, value)
}

func validateYAMLRenderValueUncheckedContext(ctx context.Context, value yamlRenderValue) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch value.kind {
	case yamlRenderString, yamlRenderBool, yamlRenderInt, yamlRenderUint:
		return nil
	case yamlRenderDate:
		_, err := yamlDate(value.text)
		return err
	case yamlRenderDateTime:
		_, err := yamlDateTime(value.text)
		return err
	case yamlRenderMapping:
		seen := make(map[string]bool, len(value.mapping))
		for _, entry := range value.mapping {
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.Key == "" || seen[entry.Key] {
				return fmt.Errorf("invalid or duplicate YAML mapping key %q", entry.Key)
			}
			seen[entry.Key] = true
			if err := validateYAMLRenderValueUncheckedContext(ctx, entry.Value); err != nil {
				return err
			}
		}
		return nil
	case yamlRenderSequence:
		for _, item := range value.sequence {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := validateYAMLRenderValueUncheckedContext(ctx, item); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("invalid YAML render value")
	}
}

func (value yamlRenderValue) semanticContext(ctx context.Context) (*yamlSemanticNode, error) {
	if err := walkYAMLRenderValueContext(ctx, value, nil); err != nil {
		return nil, err
	}
	return value.semanticUncheckedContext(ctx)
}

func (value yamlRenderValue) semanticUncheckedContext(ctx context.Context) (*yamlSemanticNode, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch value.kind {
	case yamlRenderString:
		return &yamlSemanticNode{Kind: yaml.ScalarNode, Tag: "!!str", Value: value.text}, nil
	case yamlRenderBool:
		return &yamlSemanticNode{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(value.boolean)}, nil
	case yamlRenderInt:
		return &yamlSemanticNode{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.FormatInt(value.integer, 10)}, nil
	case yamlRenderUint:
		return &yamlSemanticNode{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.FormatUint(value.unsigned, 10)}, nil
	case yamlRenderDate, yamlRenderDateTime:
		return &yamlSemanticNode{Kind: yaml.ScalarNode, Tag: "!!timestamp", Value: value.text}, nil
	case yamlRenderMapping:
		out := &yamlSemanticNode{Kind: yaml.MappingNode, Tag: "!!map"}
		for _, entry := range value.mapping {
			child, err := entry.Value.semanticUncheckedContext(ctx)
			if err != nil {
				return nil, err
			}
			semanticMappingAppend(out, entry.Key, child)
		}
		return out, ctx.Err()
	case yamlRenderSequence:
		out := &yamlSemanticNode{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, item := range value.sequence {
			child, err := item.semanticUncheckedContext(ctx)
			if err != nil {
				return nil, err
			}
			out.Content = append(out.Content, child)
		}
		return out, ctx.Err()
	default:
		return nil, nil
	}
}

// equalYAMLRenderState compares the exact state that the renderer would
// preserve or emit. It intentionally keeps mapping order and source timestamp
// spelling out of selector/canonical equality and inside three-way merge
// conflict detection.

func equalYAMLRenderStateContext(ctx context.Context, left, right yamlRenderValue) (bool, error) {
	if err := walkYAMLRenderValueContext(ctx, left, nil); err != nil {
		return false, err
	}
	if err := walkYAMLRenderValueContext(ctx, right, nil); err != nil {
		return false, err
	}
	return equalYAMLRenderStateUncheckedContext(ctx, left, right)
}

func equalYAMLRenderStateUncheckedContext(ctx context.Context, left, right yamlRenderValue) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	// Positive signed and unsigned integers have identical renderer state.
	// Keeping that equivalence here avoids manufacturing a branch conflict
	// merely because two typed callers chose different Go integer domains.
	if left.kind == yamlRenderInt && right.kind == yamlRenderUint {
		return left.integer >= 0 && uint64(left.integer) == right.unsigned, nil
	}
	if left.kind == yamlRenderUint && right.kind == yamlRenderInt {
		return right.integer >= 0 && left.unsigned == uint64(right.integer), nil
	}
	if left.kind != right.kind {
		return false, nil
	}
	switch left.kind {
	case yamlRenderString:
		return stringsEqualContext(ctx, left.text, right.text)
	case yamlRenderBool:
		return left.boolean == right.boolean, nil
	case yamlRenderInt:
		return left.integer == right.integer, nil
	case yamlRenderUint:
		return left.unsigned == right.unsigned, nil
	case yamlRenderDate, yamlRenderDateTime:
		leftLexical, rightLexical := left.lexical, right.lexical
		if leftLexical == "" {
			leftLexical = left.text
		}
		if rightLexical == "" {
			rightLexical = right.text
		}
		textEqual, err := stringsEqualContext(ctx, left.text, right.text)
		if err != nil || !textEqual {
			return textEqual, err
		}
		return stringsEqualContext(ctx, leftLexical, rightLexical)
	case yamlRenderMapping:
		if len(left.mapping) != len(right.mapping) {
			return false, nil
		}
		for i := range left.mapping {
			keyEqual, err := stringsEqualContext(ctx, left.mapping[i].Key, right.mapping[i].Key)
			if err != nil || !keyEqual {
				return keyEqual, err
			}
			equal, err := equalYAMLRenderStateUncheckedContext(ctx, left.mapping[i].Value, right.mapping[i].Value)
			if err != nil || !equal {
				return equal, err
			}
		}
		return true, ctx.Err()
	case yamlRenderSequence:
		if len(left.sequence) != len(right.sequence) {
			return false, nil
		}
		for i := range left.sequence {
			equal, err := equalYAMLRenderStateUncheckedContext(ctx, left.sequence[i], right.sequence[i])
			if err != nil || !equal {
				return equal, err
			}
		}
		return true, ctx.Err()
	default:
		return false, nil
	}
}

func equalYAMLRenderCanonicalAtKeyContext(ctx context.Context, left, right yamlRenderValue, key string) (bool, error) {
	leftCanonical, err := yamlRenderCanonicalKeyContext(ctx, left, key)
	if err != nil {
		return false, err
	}
	rightCanonical, err := yamlRenderCanonicalKeyContext(ctx, right, key)
	if err != nil {
		return false, err
	}
	return stringsEqualContext(ctx, leftCanonical, rightCanonical)
}

func isTemporalSelectorKey(key string) bool {
	switch key {
	case "at", "last_modified", "from", "to":
		return true
	default:
		return false
	}
}

func renderYAMLReplacementContext(ctx context.Context, current *yaml.Node, desired yamlRenderValue) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if desired.kind != yamlRenderMapping && desired.kind != yamlRenderSequence {
		return renderYAMLInlineContext(ctx, desired)
	}
	if current.Kind == yaml.ScalarNode || current.Style&yaml.FlowStyle != 0 {
		return renderYAMLFlowContext(ctx, desired)
	}
	indent := max(current.Column-1, 0)
	rendered, err := renderYAMLBlockContext(ctx, desired, indent)
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(rendered, strings.Repeat(" ", indent)), nil
}

func renderYAMLMappingEntryContext(ctx context.Context, key string, value yamlRenderValue, indent int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	safe, err := yamlPlainSafeContext(ctx, key)
	if err != nil {
		return "", err
	}
	if !safe {
		key, err = yamlScalarTokenContext(ctx, key, 0)
		if err != nil {
			return "", err
		}
	}
	prefix := strings.Repeat(" ", indent) + key + ":"
	if value.kind != yamlRenderMapping && value.kind != yamlRenderSequence || value.emptyCollection() {
		inline, err := renderYAMLInlineContext(ctx, value)
		if err != nil {
			return "", err
		}
		return prefix + " " + inline, nil
	}
	nested, err := renderYAMLBlockContext(ctx, value, indent+2)
	if err != nil {
		return "", err
	}
	return prefix + "\n" + nested, nil
}

func renderYAMLBlockContext(ctx context.Context, value yamlRenderValue, indent int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	switch value.kind {
	case yamlRenderMapping:
		if len(value.mapping) == 0 {
			return strings.Repeat(" ", indent) + "{}", nil
		}
		lines := make([]string, 0, len(value.mapping))
		for _, entry := range value.mapping {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			line, err := renderYAMLMappingEntryContext(ctx, entry.Key, entry.Value, indent)
			if err != nil {
				return "", err
			}
			lines = append(lines, line)
		}
		return strings.Join(lines, "\n"), nil
	case yamlRenderSequence:
		if len(value.sequence) == 0 {
			return strings.Repeat(" ", indent) + "[]", nil
		}
		lines := make([]string, 0, len(value.sequence))
		for _, item := range value.sequence {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			prefix := strings.Repeat(" ", indent) + "-"
			if item.kind != yamlRenderMapping && item.kind != yamlRenderSequence || item.emptyCollection() {
				inline, err := renderYAMLInlineContext(ctx, item)
				if err != nil {
					return "", err
				}
				lines = append(lines, prefix+" "+inline)
				continue
			}
			nested, err := renderYAMLBlockContext(ctx, item, indent+2)
			if err != nil {
				return "", err
			}
			lines = append(lines, prefix+"\n"+nested)
		}
		return strings.Join(lines, "\n"), nil
	default:
		inline, err := renderYAMLInlineContext(ctx, value)
		if err != nil {
			return "", err
		}
		return strings.Repeat(" ", indent) + inline, nil
	}
}

func renderYAMLInlineContext(ctx context.Context, value yamlRenderValue) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	switch value.kind {
	case yamlRenderString:
		return yamlScalarTokenContext(ctx, value.text, 0)
	case yamlRenderBool:
		return strconv.FormatBool(value.boolean), nil
	case yamlRenderInt:
		return strconv.FormatInt(value.integer, 10), nil
	case yamlRenderUint:
		return strconv.FormatUint(value.unsigned, 10), nil
	case yamlRenderDate, yamlRenderDateTime:
		return value.text, nil
	case yamlRenderMapping, yamlRenderSequence:
		return renderYAMLFlowContext(ctx, value)
	default:
		return "", fmt.Errorf("invalid YAML render value")
	}
}

func renderYAMLFlowContext(ctx context.Context, value yamlRenderValue) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	switch value.kind {
	case yamlRenderMapping:
		parts := make([]string, 0, len(value.mapping))
		for _, entry := range value.mapping {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			key := entry.Key
			safe, err := yamlFlowPlainSafeContext(ctx, key)
			if err != nil {
				return "", err
			}
			if !safe {
				key = strconv.Quote(key)
			}
			rendered, err := renderYAMLFlowContext(ctx, entry.Value)
			if err != nil {
				return "", err
			}
			parts = append(parts, key+": "+rendered)
		}
		return "{" + strings.Join(parts, ", ") + "}", nil
	case yamlRenderSequence:
		parts := make([]string, 0, len(value.sequence))
		for _, item := range value.sequence {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			rendered, err := renderYAMLFlowContext(ctx, item)
			if err != nil {
				return "", err
			}
			parts = append(parts, rendered)
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	case yamlRenderString:
		return yamlFlowScalarTokenContext(ctx, value.text)
	default:
		return renderYAMLInlineContext(ctx, value)
	}
}

func yamlFlowScalarTokenContext(ctx context.Context, value string) (string, error) {
	safe, err := yamlFlowPlainSafeContext(ctx, value)
	if err != nil {
		return "", err
	}
	if safe {
		return value, nil
	}
	return strconv.Quote(value), nil
}

func yamlFlowPlainSafeContext(ctx context.Context, value string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if value == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\t") {
		return false, nil
	}
	probe, ok := boundedSyntheticYAMLScalarProbe("{v: ", value, "}\n")
	if !ok {
		return false, nil
	}
	doc, err := decodeGuardedYAMLNodeContext(ctx, probe)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false, err
		}
		// Flow probing is only a style decision. Any YAML-looking input which
		// cannot be proved to be one plain string is emitted quoted.
		return false, nil
	}
	if len(doc.Content) != 1 {
		return false, nil
	}
	root := doc.Content[0]
	return root.Kind == yaml.MappingNode && len(root.Content) == 2 &&
		root.Content[1].Kind == yaml.ScalarNode && root.Content[1].Tag == "!!str" &&
		root.Content[1].Value == value, ctx.Err()
}

func yamlGeneratedNewlines(text string, newline []byte) string {
	if bytes.Equal(newline, []byte("\r\n")) {
		return strings.ReplaceAll(text, "\n", "\r\n")
	}
	return text
}
