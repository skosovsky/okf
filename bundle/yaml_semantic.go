package bundle

import (
	"context"

	"gopkg.in/yaml.v3"
)

// yamlSemanticResolver evaluates YAML merge-key lookup and terminal aliases
// over a graph that has already passed ValidateYAMLNodeContext. Results are
// memoized by (node, key), so shared donors and alias targets are evaluated
// once per lookup key instead of once per incoming reference.
type yamlSemanticResolver struct {
	ctx context.Context

	lookupMemo   map[yamlSemanticLookupKey]yamlSemanticSelection
	lookupActive map[yamlSemanticLookupKey]bool
	valueMemo    map[*yaml.Node]yamlSemanticValue
	valueActive  map[*yaml.Node]bool
}

type yamlSemanticLookupKey struct {
	node *yaml.Node
	key  string
}

type yamlSemanticSelection struct {
	present   bool
	ambiguous bool
	raw       *yaml.Node
	resolved  *yaml.Node
	viaAlias  bool
	viaMerge  bool
}

type yamlSemanticValue struct {
	node     *yaml.Node
	resolved bool
	viaAlias bool
}

func newYAMLSemanticResolver(ctx context.Context) *yamlSemanticResolver {
	if ctx == nil {
		ctx = context.Background()
	}
	return &yamlSemanticResolver{
		ctx:          ctx,
		lookupMemo:   make(map[yamlSemanticLookupKey]yamlSemanticSelection),
		lookupActive: make(map[yamlSemanticLookupKey]bool),
		valueMemo:    make(map[*yaml.Node]yamlSemanticValue),
		valueActive:  make(map[*yaml.Node]bool),
	}
}

func (r *yamlSemanticResolver) mappingValue(
	node *yaml.Node,
	key string,
) (yamlSemanticSelection, error) {
	if err := r.ctx.Err(); err != nil {
		return yamlSemanticSelection{}, err
	}
	if node == nil {
		return yamlSemanticSelection{}, nil
	}
	// Hashing an arbitrary caller key is not cancellable. Retained semantic
	// graphs are already validated, so very large keys safely skip the memo and
	// its defensive recursion marker while keeping chunked exact comparison.
	const maxMemoizedKeyBytes = 64 << 10
	memoized := len(key) <= maxMemoizedKeyBytes
	memoKey := yamlSemanticLookupKey{node: node, key: key}
	if memoized {
		if cached, ok := r.lookupMemo[memoKey]; ok {
			return cached, nil
		}
		// Defensive only: retained graphs are validated and therefore acyclic.
		if r.lookupActive[memoKey] {
			return yamlSemanticSelection{}, nil
		}
		r.lookupActive[memoKey] = true
		defer delete(r.lookupActive, memoKey)
	}
	storeMemo := func(selection yamlSemanticSelection) {
		if memoized {
			r.lookupMemo[memoKey] = selection
		}
	}

	var result yamlSemanticSelection
	if node.Kind == yaml.AliasNode {
		var err error
		result, err = r.mappingValue(node.Alias, key)
		if err != nil {
			return yamlSemanticSelection{}, err
		}
		if result.present {
			result.viaAlias = true
		}
		storeMemo(result)
		return result, nil
	}
	if node.Kind != yaml.MappingNode {
		storeMemo(result)
		return result, nil
	}

	var explicit []*yaml.Node
	var merges []*yaml.Node
	for index := 0; index+1 < len(node.Content); index += 2 {
		if err := r.ctx.Err(); err != nil {
			return yamlSemanticSelection{}, err
		}
		keyNode, valueNode := node.Content[index], node.Content[index+1]
		if isMergeKey(keyNode) {
			merges = append(merges, valueNode)
			continue
		}
		if keyNode == nil || keyNode.Kind != yaml.ScalarNode || keyNode.Tag != "!!str" {
			continue
		}
		equal, err := equalStringContext(r.ctx, keyNode.Value, key)
		if err != nil {
			return yamlSemanticSelection{}, err
		}
		if equal {
			explicit = append(explicit, valueNode)
		}
	}

	switch len(explicit) {
	case 1:
		value, err := r.value(explicit[0])
		if err != nil {
			return yamlSemanticSelection{}, err
		}
		result = yamlSemanticSelection{
			present:   true,
			ambiguous: !value.resolved,
			raw:       explicit[0],
			resolved:  value.node,
			viaAlias:  value.viaAlias,
		}
		storeMemo(result)
		return result, nil
	default:
		if len(explicit) > 1 {
			result = yamlSemanticSelection{present: true, ambiguous: true}
			storeMemo(result)
			return result, nil
		}
	}

	if len(merges) > 1 {
		for _, merge := range merges {
			selection, err := r.mergedValue(merge, key)
			if err != nil {
				return yamlSemanticSelection{}, err
			}
			if selection.present {
				result.present = true
				result.ambiguous = true
				result.viaMerge = true
				result.viaAlias = result.viaAlias || selection.viaAlias
			}
		}
		storeMemo(result)
		return result, nil
	}
	if len(merges) == 1 {
		var err error
		result, err = r.mergedValue(merges[0], key)
		if err != nil {
			return yamlSemanticSelection{}, err
		}
		if result.present {
			result.viaMerge = true
		}
	}
	storeMemo(result)
	return result, nil
}

func (r *yamlSemanticResolver) mergedValue(
	node *yaml.Node,
	key string,
) (yamlSemanticSelection, error) {
	if err := r.ctx.Err(); err != nil {
		return yamlSemanticSelection{}, err
	}
	if node == nil {
		return yamlSemanticSelection{}, nil
	}
	if node.Kind != yaml.SequenceNode {
		selection, err := r.mappingValue(node, key)
		if selection.present {
			selection.viaMerge = true
		}
		return selection, err
	}
	for _, item := range node.Content {
		selection, err := r.mappingValue(item, key)
		if err != nil {
			return yamlSemanticSelection{}, err
		}
		if selection.present {
			selection.viaMerge = true
			return selection, nil
		}
	}
	return yamlSemanticSelection{}, nil
}

func (r *yamlSemanticResolver) value(node *yaml.Node) (yamlSemanticValue, error) {
	if err := r.ctx.Err(); err != nil {
		return yamlSemanticValue{}, err
	}
	if node == nil {
		return yamlSemanticValue{}, nil
	}
	if cached, ok := r.valueMemo[node]; ok {
		return cached, nil
	}
	if r.valueActive[node] {
		return yamlSemanticValue{}, nil
	}
	r.valueActive[node] = true
	defer delete(r.valueActive, node)

	result := yamlSemanticValue{node: node, resolved: true}
	if node.Kind == yaml.AliasNode {
		var err error
		result, err = r.value(node.Alias)
		if err != nil {
			return yamlSemanticValue{}, err
		}
		result.viaAlias = true
	}
	if err := r.ctx.Err(); err != nil {
		return yamlSemanticValue{}, err
	}
	r.valueMemo[node] = result
	return result, nil
}
