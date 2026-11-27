package mutation

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/skosovsky/okf/bundle"
	"gopkg.in/yaml.v3"
)

// LostExtensionsV03 returns the locations of extension values that a
// whole-document replacement would remove or change. Named entries can move
// within a list. Anonymous entries with the same identity match by occurrence.
func LostExtensionsV03(ctx context.Context, before, after bundle.Frontmatter) ([]string, error) {
	left, err := before.YAMLNodeContext(ctx)
	if err != nil {
		return nil, err
	}
	right, err := after.YAMLNodeContext(ctx)
	if err != nil {
		return nil, err
	}
	var lost []string
	if err := compareExtensions(ctx, left, right, "", conceptExtensionLayout, &lost); err != nil {
		return nil, err
	}
	return lost, ctx.Err()
}

type extensionLayout struct {
	known    string
	identity []string
	nested   map[string]*extensionLayout
}

var usageWindowExtensionLayout = &extensionLayout{known: " from to "}

var conceptExtensionLayout = &extensionLayout{
	known: " type title description resource tags sources usage_window generated verified status stale_after superseded_by runtime parameters computation executor attester parts ",
	nested: map[string]*extensionLayout{
		"generated": {known: " by at "},
		"verified":  {known: " by at ", identity: []string{"by", "at"}},
		"sources": {
			known: " id resource title author usage_count last_modified usage_window ", identity: []string{"id", "resource"},
			nested: map[string]*extensionLayout{"usage_window": usageWindowExtensionLayout},
		},
		"usage_window": usageWindowExtensionLayout,
		"parameters":   {known: " name type required ", identity: []string{"name"}},
		"executor":     {known: " resource receipt "},
		"attester":     {known: " resource "},
		"parts":        {known: " id anchor ", identity: []string{"id", "anchor"}},
	},
}

func compareExtensions(ctx context.Context, before, after *yaml.Node, path string, layout *extensionLayout, lost *[]string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(layout.identity) > 0 {
		left := extensionEntries(before)
		right := extensionEntries(after)
		candidates := make(map[string][]*yaml.Node)
		for _, item := range right {
			identity, err := extensionIdentity(ctx, item, layout.identity)
			if err != nil {
				return err
			}
			candidates[identity] = append(candidates[identity], item)
		}
		for index, item := range left {
			identity, err := extensionIdentity(ctx, item, layout.identity)
			if err != nil {
				return err
			}
			var replacement *yaml.Node
			if matches := candidates[identity]; len(matches) != 0 {
				replacement = matches[0]
				candidates[identity] = matches[1:]
			}
			if err := compareExtensionMapping(ctx, item, replacement, fmt.Sprintf("%s[%d]", path, index), layout, lost); err != nil {
				return err
			}
		}
		return ctx.Err()
	}
	return compareExtensionMapping(ctx, before, after, path, layout, lost)
}

func compareExtensionMapping(ctx context.Context, before, after *yaml.Node, path string, layout *extensionLayout, lost *[]string) error {
	before = extensionMapping(before)
	after = extensionMapping(after)
	if before == nil {
		return ctx.Err()
	}
	// Index simple keys without interpreting merge keys as known fields.
	values := make(map[string]*yaml.Node)
	if after != nil {
		for index := 0; index+1 < len(after.Content); index += 2 {
			if err := ctx.Err(); err != nil {
				return err
			}
			key := after.Content[index]
			if key.Kind == yaml.ScalarNode {
				values[key.Tag+"\x00"+key.Value] = after.Content[index+1]
			}
		}
	}
	for index := 0; index+1 < len(before.Content); index += 2 {
		if err := ctx.Err(); err != nil {
			return err
		}
		key, value := before.Content[index], before.Content[index+1]
		next := values[key.Tag+"\x00"+key.Value]
		field := key.Value
		if path != "" {
			field = path + "." + field
		}
		if key.Tag == "!!str" && key.Kind == yaml.ScalarNode && !strings.ContainsAny(key.Value, " \t\r\n") &&
			strings.Contains(layout.known, " "+key.Value+" ") {
			if nested := layout.nested[key.Value]; nested != nil {
				if err := compareExtensions(ctx, value, next, field, nested, lost); err != nil {
					return err
				}
			}
			continue
		}
		if key.Kind != yaml.ScalarNode {
			next = nil
			field = path + "[key " + strconv.Itoa(index/2) + "]"
			if after != nil {
				for i := 0; i+1 < len(after.Content); i += 2 {
					equal, err := equalExtensionValue(ctx, key, after.Content[i])
					if err != nil {
						return err
					}
					if equal {
						next = after.Content[i+1]
						break
					}
				}
			}
		}
		equal, err := equalExtensionValue(ctx, value, next)
		if err != nil {
			return err
		}
		if !equal {
			*lost = append(*lost, field)
		}
	}
	return ctx.Err()
}

func extensionMapping(node *yaml.Node) *yaml.Node {
	if node != nil && node.Kind == yaml.AliasNode {
		node = node.Alias
	}
	if node != nil && node.Kind == yaml.MappingNode {
		return node
	}
	return nil
}

func extensionEntries(node *yaml.Node) []*yaml.Node {
	if node != nil && node.Kind == yaml.AliasNode {
		node = node.Alias
	}
	switch {
	case node == nil:
		return nil
	case node.Kind == yaml.SequenceNode:
		return node.Content
	case node.Kind == yaml.MappingNode:
		return []*yaml.Node{node}
	default:
		return nil
	}
}

func extensionIdentity(ctx context.Context, node *yaml.Node, keys []string) (string, error) {
	node = extensionMapping(node)
	if node == nil {
		return "", ctx.Err()
	}
	var identity strings.Builder
	for _, key := range keys {
		resolved, _, err := bundle.SemanticMappingValueContext(ctx, node, key)
		if err != nil {
			return "", err
		}
		value := ""
		if resolved != nil && resolved.Kind == yaml.ScalarNode {
			value = resolved.Value
		}
		identity.WriteString(strconv.Itoa(len(value)))
		identity.WriteByte(':')
		identity.WriteString(value)
		// A source's ID is sufficient even when its resource was edited.
		if key == "id" && value != "" {
			break
		}
	}
	return identity.String(), ctx.Err()
}

// Compare alias targets as well as their spelling. A replacement may retain an
// alias token while changing the anchor it points to.
func equalExtensionValue(ctx context.Context, before, after *yaml.Node) (bool, error) {
	type pair struct {
		before, after *yaml.Node
	}
	stack := []pair{{before, after}}
	seen := make(map[pair]bool)
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		item := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[item] {
			continue
		}
		seen[item] = true
		if item.before == nil || item.after == nil {
			if item.before != item.after {
				return false, nil
			}
			continue
		}
		left, right := item.before, item.after
		if left.Kind != right.Kind || left.Tag != right.Tag || left.Anchor != right.Anchor ||
			len(left.Content) != len(right.Content) {
			return false, nil
		}
		equal, err := stringsEqualContext(ctx, left.Value, right.Value)
		if err != nil || !equal {
			return false, err
		}
		stack = append(stack, pair{left.Alias, right.Alias})
		for i := range left.Content {
			stack = append(stack, pair{left.Content[i], right.Content[i]})
		}
	}
	return true, ctx.Err()
}
