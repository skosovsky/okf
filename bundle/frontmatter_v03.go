package bundle

import (
	"context"
	"encoding/binary"
	"fmt"
	"slices"

	"gopkg.in/yaml.v3"
)

// ValidateUniqueKeysContext rejects semantically repeated keys in each mapping.
// An explicit key can override a key inherited through a merge.
func (f Frontmatter) ValidateUniqueKeysContext(ctx context.Context) error {
	return f.validateCoreKeysContext(ctx, false)
}

// resolveCoreV03Context is called on newly parsed, privately owned nodes.
// The source lexemes and retained bytes are still used by presentation writers.
func (f *Frontmatter) resolveCoreV03Context(ctx context.Context) error {
	if err := f.validateCoreKeysContext(ctx, true); err != nil {
		return err
	}
	f.coreV03 = true
	return nil
}

func (f Frontmatter) validateCoreKeysContext(ctx context.Context, resolveTags bool) error {
	root := f.mappingNode()
	stack := []*yaml.Node{&root}
	source, err := newYAMLKeySource(ctx, f.raw)
	if err != nil {
		return err
	}
	identities := yamlKeyIdentities{byNode: make(map[*yaml.Node]int), byValue: make(map[yamlKeyValue]int), source: source}
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if resolveTags && node.Kind == yaml.ScalarNode {
			node.Tag = yamlCoreScalarTag(node)
			if node.Style == 0 && source.hasNonSpecificTag(node) {
				node.Tag = "!!str"
			}
		}
		if node.Kind == yaml.MappingNode {
			keys := make(map[int]struct{}, len(node.Content)/2)
			for i := 0; i < len(node.Content); i += 2 {
				if err := ctx.Err(); err != nil {
					return err
				}
				key := node.Content[i]
				identity, err := identities.id(ctx, key)
				if err != nil {
					return err
				}
				if _, exists := keys[identity]; exists {
					resolved := key
					if key.Kind == yaml.AliasNode {
						resolved = key.Alias
					}
					if resolved.Kind == yaml.ScalarNode {
						return fmt.Errorf("%w: duplicate YAML key %q on line %d", ErrInvalidFrontmatter, resolved.Value, key.Line)
					}
					return fmt.Errorf("%w: duplicate YAML collection key on line %d", ErrInvalidFrontmatter, key.Line)
				}
				keys[identity] = struct{}{}
			}
		}
		for i := len(node.Content) - 1; i >= 0; i-- {
			stack = append(stack, node.Content[i])
		}
	}
	return ctx.Err()
}

type yamlKeyValue struct {
	kind       yaml.Kind
	tag, value string
}

// yaml.v3's typed decoder interprets unprefixed leading zeros as octal.
// Normalize its private input copy while retaining original lexemes on reads.
func prepareCoreV03Decode(root *yaml.Node) {
	stack := []*yaml.Node{root}
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if node.Kind == yaml.ScalarNode && node.Tag == "!!int" && yamlCoreDecimal.MatchString(node.Value) {
			node.Value = yamlCoreDecimalInteger(node.Value)
		}
		stack = append(stack, node.Content...)
	}
}

// Interning child identities keeps nested and aliased collection keys bounded
// by the already-validated graph instead of serializing each expanded subtree.
type yamlKeyIdentities struct {
	byNode  map[*yaml.Node]int
	byValue map[yamlKeyValue]int
	source  yamlKeySource
}

func (keys yamlKeyIdentities) id(ctx context.Context, node *yaml.Node) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if node.Kind == yaml.AliasNode {
		node = node.Alias
	}
	if id := keys.byNode[node]; id != 0 {
		return id, nil
	}
	value := yamlKeyValue{node.Kind, node.Tag, node.Value}
	switch node.Kind {
	case yaml.ScalarNode:
		if node.Style == 0 && keys.source.hasNonSpecificTag(node) {
			value.tag = "!!str"
		} else {
			value.tag, value.value = yamlCoreKeyScalar(node)
		}
	case yaml.SequenceNode, yaml.MappingNode:
		children := make([]uint64, len(node.Content))
		for i, child := range node.Content {
			id, err := keys.id(ctx, child)
			if err != nil {
				return 0, err
			}
			children[i] = uint64(id)
		}
		if node.Kind == yaml.MappingNode {
			pairs := children[:len(children)/2]
			for i := range pairs {
				// IDs cannot exceed the YAML graph node limit.
				pairs[i] = children[2*i]<<32 | children[2*i+1]
			}
			slices.Sort(pairs)
			children = pairs
		}
		encoded := make([]byte, 0, 8*len(children))
		for _, child := range children {
			encoded = binary.LittleEndian.AppendUint64(encoded, child)
		}
		value.value = string(encoded)
	}
	id := keys.byValue[value]
	if id == 0 {
		id = len(keys.byValue) + 1
		keys.byValue[value] = id
	}
	keys.byNode[node] = id
	return id, ctx.Err()
}
