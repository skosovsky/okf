package mutation

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/skosovsky/okf/bundle"
	"gopkg.in/yaml.v3"
)

func relationTargetPatchesContext(ctx context.Context, p *presentation, from, to bundle.ConceptID, oldFragment, newFragment string) ([]bytePatch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var patches []bytePatch
	err := walkMappingsContext(ctx, p.root, includeRootMapping, func(n *yaml.Node) error {
		if err := p.rejectMergedRootRelationTargetContext(ctx, n, from, oldFragment); err != nil {
			return err
		}
		rels, err := mappingValuesForKeyContext(ctx, n, "relations")
		if err != nil {
			return err
		}
		for _, relations := range rels {
			relations = yamlAliasTarget(relations)
			if err := p.rejectMergedRelationsTargetContext(ctx, relations, from, oldFragment); err != nil {
				return err
			}
			if err := p.rejectMergedTargetsInRelationItemsContext(ctx, relations, from, oldFragment); err != nil {
				return err
			}
		}
		// First find semantic candidates.  An unsupported extension relation is
		// opaque unless it actually contains a target this operation owns.
		contains, err := relationsContainTargetContext(ctx, rels, from, oldFragment)
		if err != nil {
			return err
		}
		if !contains {
			return nil
		}
		if len(rels) != 1 {
			return p.touchedDuplicateKeyError(n, "relations")
		}
		relations := rels[0]
		if relations.Kind == yaml.AliasNode || relations.Alias != nil {
			return p.yamlNodeError("alias_provenance", ErrAmbiguousPresentation, relations)
		}
		if relations.Kind != yaml.MappingNode || relations.Style&yaml.FlowStyle != 0 || relations.Anchor != "" || relations.Alias != nil {
			return p.yamlNodeError("relations_not_block_mapping", ErrUnsupportedPresentation, relations)
		}
		if err := p.rejectMergedRelationsTargetContext(ctx, relations, from, oldFragment); err != nil {
			return err
		}
		typeCounts := make(map[string]int, len(relations.Content)/2)
		for i := 0; i+1 < len(relations.Content); i += 2 {
			typeCounts[relations.Content[i].Value]++
		}
		for i := 0; i+1 < len(relations.Content); i += 2 {
			key, items := relations.Content[i], relations.Content[i+1]
			contains, err := sequenceContainsTargetContext(ctx, items, from, oldFragment)
			if err != nil {
				return err
			}
			if typeCounts[key.Value] > 1 && contains {
				return p.touchedDuplicateKeyError(relations, key.Value)
			}
		}
		for i := 0; i+1 < len(relations.Content); i += 2 {
			key, items := relations.Content[i], relations.Content[i+1]
			contains, err := sequenceContainsTargetContext(ctx, items, from, oldFragment)
			if err != nil {
				return err
			}
			if !contains {
				continue
			}
			if err := p.requireScalarKey(key); err != nil {
				return err
			}
			if items.Kind == yaml.AliasNode || items.Alias != nil {
				return p.yamlNodeError("alias_provenance", ErrAmbiguousPresentation, items)
			}
			if items.Kind != yaml.SequenceNode || items.Style&yaml.FlowStyle != 0 || items.Anchor != "" || items.Alias != nil {
				return p.yamlNodeError("relation_type_not_block_sequence", ErrUnsupportedPresentation, items)
			}
			duplicateTargets, duplicateErr := duplicateRelationTargetsInSequenceContext(ctx, items, from, oldFragment)
			if duplicateErr != nil {
				return duplicateErr
			}
			if len(duplicateTargets) > 1 {
				return p.yamlNodeError("duplicate_target", ErrAmbiguousPresentation, duplicateTargets...)
			}
			for _, item := range items.Content {
				semanticItem := yamlAliasTarget(item)
				if err := p.rejectMergedItemTargetContext(ctx, semanticItem, from, oldFragment); err != nil {
					return err
				}
				vals, err := mappingValuesForKeyContext(ctx, semanticItem, "target")
				if err != nil {
					return err
				}
				contains, err := targetsContainContext(ctx, vals, from, oldFragment)
				if err != nil {
					return err
				}
				if !contains {
					continue
				}
				if len(vals) != 1 {
					return p.touchedDuplicateKeyError(semanticItem, "target")
				}
				if item.Kind == yaml.AliasNode || item.Alias != nil {
					return p.yamlNodeError("alias_provenance", ErrAmbiguousPresentation, item)
				}
				if item.Kind != yaml.MappingNode || item.Style&yaml.FlowStyle != 0 || item.Anchor != "" {
					return p.yamlNodeError("relation_item_presentation", ErrUnsupportedPresentation, item)
				}
				target := vals[0]
				r, _ := bundle.ParseRelationRef(target.Value)
				r.ID = to
				if oldFragment != "" {
					r.Fragment = newFragment
				}
				patch, e := p.resolvedScalarPatch(target, r.String())
				if e != nil {
					return e
				}
				patches = append(patches, patch)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return patches, ctx.Err()
}

func (p *presentation) rejectMergedRootRelationTargetContext(ctx context.Context, mapping *yaml.Node, id bundle.ConceptID, fragment string) error {
	return p.rejectMatchingMergeContext(ctx, mapping, func(ctx context.Context, value *yaml.Node) (bool, error) {
		return mergedRootContainsRelationTargetContext(ctx, value, id, fragment, make(map[*yaml.Node]bool))
	})
}

func (p *presentation) rejectMergedRelationsTargetContext(ctx context.Context, mapping *yaml.Node, id bundle.ConceptID, fragment string) error {
	return p.rejectMatchingMergeContext(ctx, mapping, func(ctx context.Context, value *yaml.Node) (bool, error) {
		return mergedRelationsContainTargetContext(ctx, value, id, fragment, make(map[*yaml.Node]bool))
	})
}

func (p *presentation) rejectMergedItemTargetContext(ctx context.Context, mapping *yaml.Node, id bundle.ConceptID, fragment string) error {
	return p.rejectMatchingMergeContext(ctx, mapping, func(ctx context.Context, value *yaml.Node) (bool, error) {
		return mergedItemContainsTargetContext(ctx, value, id, fragment, make(map[*yaml.Node]bool))
	})
}

func (p *presentation) rejectMergedTargetsInRelationItemsContext(ctx context.Context, relations *yaml.Node, id bundle.ConceptID, fragment string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if relations == nil || relations.Kind != yaml.MappingNode {
		return nil
	}
	for i := 1; i < len(relations.Content); i += 2 {
		if err := ctx.Err(); err != nil {
			return err
		}
		sequence := yamlAliasTarget(relations.Content[i])
		if sequence == nil || sequence.Kind != yaml.SequenceNode {
			continue
		}
		for _, item := range sequence.Content {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := p.rejectMergedItemTargetContext(ctx, yamlAliasTarget(item), id, fragment); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

func (p *presentation) rejectMergedCanonicalFragmentContext(ctx context.Context, mapping *yaml.Node, fragment string) error {
	return p.rejectMatchingMergeContext(ctx, mapping, func(ctx context.Context, value *yaml.Node) (bool, error) {
		return mergedCanonicalContainsFragmentContext(ctx, value, fragment, make(map[*yaml.Node]bool))
	})
}

func (p *presentation) rejectMatchingMergeContext(ctx context.Context, mapping *yaml.Node, matches func(context.Context, *yaml.Node) (bool, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if err := ctx.Err(); err != nil {
			return err
		}
		key, value := mapping.Content[i], mapping.Content[i+1]
		if key != nil && key.Tag == "!!merge" {
			matched, err := matches(ctx, value)
			if err != nil {
				return err
			}
			if matched {
				return p.yamlNodeError("alias_provenance", ErrAmbiguousPresentation, key, value)
			}
		}
	}
	return ctx.Err()
}

func mergedRootContainsRelationTargetContext(ctx context.Context, value *yaml.Node, id bundle.ConceptID, fragment string, visited map[*yaml.Node]bool) (bool, error) {
	return anyMergedMappingContext(ctx, value, visited, func(ctx context.Context, mapping *yaml.Node) (bool, error) {
		relations, err := mappingValuesForKeyContext(ctx, mapping, "relations")
		if err != nil {
			return false, err
		}
		matched, err := relationsContainTargetContext(ctx, relations, id, fragment)
		if err != nil || matched {
			return matched, err
		}
		for i := 0; i+1 < len(mapping.Content); i += 2 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			if mapping.Content[i].Tag == "!!merge" {
				matched, err := mergedRootContainsRelationTargetContext(ctx, mapping.Content[i+1], id, fragment, visited)
				if err != nil || matched {
					return matched, err
				}
			}
		}
		return false, nil
	})
}

func mergedRelationsContainTargetContext(ctx context.Context, value *yaml.Node, id bundle.ConceptID, fragment string, visited map[*yaml.Node]bool) (bool, error) {
	return anyMergedMappingContext(ctx, value, visited, func(ctx context.Context, mapping *yaml.Node) (bool, error) {
		for i := 0; i+1 < len(mapping.Content); i += 2 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			if mapping.Content[i].Tag == "!!merge" {
				matched, err := mergedRelationsContainTargetContext(ctx, mapping.Content[i+1], id, fragment, visited)
				if err != nil || matched {
					return matched, err
				}
				continue
			}
			matched, err := sequenceContainsTargetContext(ctx, mapping.Content[i+1], id, fragment)
			if err != nil || matched {
				return matched, err
			}
		}
		return false, nil
	})
}

func mergedItemContainsTargetContext(ctx context.Context, value *yaml.Node, id bundle.ConceptID, fragment string, visited map[*yaml.Node]bool) (bool, error) {
	return anyMergedMappingContext(ctx, value, visited, func(ctx context.Context, mapping *yaml.Node) (bool, error) {
		targets, err := mappingValuesForKeyContext(ctx, mapping, "target")
		if err != nil {
			return false, err
		}
		matched, err := targetsContainContext(ctx, targets, id, fragment)
		if err != nil || matched {
			return matched, err
		}
		for i := 0; i+1 < len(mapping.Content); i += 2 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			if mapping.Content[i].Tag == "!!merge" {
				matched, err := mergedItemContainsTargetContext(ctx, mapping.Content[i+1], id, fragment, visited)
				if err != nil || matched {
					return matched, err
				}
			}
		}
		return false, nil
	})
}

func mergedCanonicalContainsFragmentContext(ctx context.Context, value *yaml.Node, fragment string, visited map[*yaml.Node]bool) (bool, error) {
	return anyMergedMappingContext(ctx, value, visited, func(ctx context.Context, mapping *yaml.Node) (bool, error) {
		identity, err := resolveMappingIdentityContext(ctx, mapping)
		if err != nil {
			return false, err
		}
		if identity.State == bundle.MappingIdentityValid {
			equal, err := stringsEqualContext(ctx, identity.Fragment, fragment)
			if err != nil || equal {
				return equal, err
			}
		}
		if identity.State == bundle.MappingIdentityInvalid {
			matched, err := identityMentionsFragmentContext(ctx, mapping, fragment)
			if err != nil || matched {
				return matched, err
			}
		}
		for i := 0; i+1 < len(mapping.Content); i += 2 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			if mapping.Content[i].Tag == "!!merge" {
				matched, err := mergedCanonicalContainsFragmentContext(ctx, mapping.Content[i+1], fragment, visited)
				if err != nil || matched {
					return matched, err
				}
			}
		}
		return false, nil
	})
}

func anyMergedMappingContext(ctx context.Context, node *yaml.Node, visited map[*yaml.Node]bool, matches func(context.Context, *yaml.Node) (bool, error)) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if node == nil || visited[node] {
		return false, nil
	}
	visited[node] = true
	if node.Kind == yaml.AliasNode || node.Alias != nil {
		return anyMergedMappingContext(ctx, node.Alias, visited, matches)
	}
	if node.Kind == yaml.SequenceNode {
		for _, child := range node.Content {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			matched, err := anyMergedMappingContext(ctx, child, visited, matches)
			if err != nil || matched {
				return matched, err
			}
		}
		return false, nil
	}
	if node.Kind != yaml.MappingNode {
		return false, nil
	}
	return matches(ctx, node)
}

func mappingValuesForKeyContext(ctx context.Context, mapping *yaml.Node, key string) ([]*yaml.Node, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil, nil
	}
	values := make([]*yaml.Node, 0, 1)
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		candidate := mapping.Content[index]
		if candidate == nil || candidate.Kind != yaml.ScalarNode || candidate.Tag != "!!str" {
			continue
		}
		equal, err := stringsEqualContext(ctx, candidate.Value, key)
		if err != nil {
			return nil, err
		}
		if equal {
			values = append(values, mapping.Content[index+1])
		}
	}
	return values, ctx.Err()
}

func matchesRelationTargetContext(ctx context.Context, node *yaml.Node, id bundle.ConceptID, fragment string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	node = yamlAliasTarget(node)
	if node == nil || node.Kind != yaml.ScalarNode {
		return false, nil
	}
	concept := (bundle.RelationRef{ID: id}).String()
	if fragment != "" {
		return stringsEqualContext(ctx, node.Value, concept+"#"+fragment)
	}
	if len(node.Value) < len(concept) {
		return false, nil
	}
	prefixEqual, err := stringsEqualContext(ctx, node.Value[:len(concept)], concept)
	if err != nil || !prefixEqual {
		return false, err
	}
	if len(node.Value) == len(concept) {
		return true, ctx.Err()
	}
	if node.Value[len(concept)] != '#' {
		return false, nil
	}
	return validRelationFragmentContext(ctx, node.Value[len(concept)+1:])
}

func targetsContainContext(ctx context.Context, nodes []*yaml.Node, id bundle.ConceptID, fragment string) (bool, error) {
	for _, node := range nodes {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		matched, err := matchesRelationTargetContext(ctx, node, id, fragment)
		if err != nil || matched {
			return matched, err
		}
	}
	return false, ctx.Err()
}

func sequenceContainsTargetContext(ctx context.Context, items *yaml.Node, id bundle.ConceptID, fragment string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	items = yamlAliasTarget(items)
	if items == nil || items.Kind != yaml.SequenceNode {
		return false, nil
	}
	for _, item := range items.Content {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		targets, err := mappingValuesForKeyContext(ctx, yamlAliasTarget(item), "target")
		if err != nil {
			return false, err
		}
		matched, err := targetsContainContext(ctx, targets, id, fragment)
		if err != nil || matched {
			return matched, err
		}
	}
	return false, ctx.Err()
}

func relationsContainTargetContext(ctx context.Context, relations []*yaml.Node, id bundle.ConceptID, fragment string) (bool, error) {
	for _, relation := range relations {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		semantic := yamlAliasTarget(relation)
		if semantic == nil || semantic.Kind != yaml.MappingNode {
			continue
		}
		for index := 1; index < len(semantic.Content); index += 2 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			matched, err := sequenceContainsTargetContext(ctx, semantic.Content[index], id, fragment)
			if err != nil || matched {
				return matched, err
			}
		}
	}
	return false, ctx.Err()
}

func resolveMappingIdentityContext(ctx context.Context, mapping *yaml.Node) (bundle.MappingIdentity, error) {
	ids, err := mappingValuesForKeyContext(ctx, mapping, "id")
	if err != nil {
		return bundle.MappingIdentity{}, err
	}
	anchors, err := mappingValuesForKeyContext(ctx, mapping, "anchor")
	if err != nil {
		return bundle.MappingIdentity{}, err
	}
	if len(ids) == 0 && len(anchors) == 0 {
		return bundle.MappingIdentity{State: bundle.MappingIdentityAbsent}, nil
	}
	validIDs, err := validMappingFragmentsContext(ctx, ids)
	if err != nil {
		return bundle.MappingIdentity{}, err
	}
	if len(validIDs) > 1 {
		return bundle.MappingIdentity{State: bundle.MappingIdentityInvalid}, nil
	}
	if len(validIDs) == 1 {
		return bundle.MappingIdentity{Fragment: validIDs[0].Value, Node: validIDs[0], State: bundle.MappingIdentityValid}, nil
	}
	validAnchors, err := validMappingFragmentsContext(ctx, anchors)
	if err != nil {
		return bundle.MappingIdentity{}, err
	}
	if len(validAnchors) > 1 {
		return bundle.MappingIdentity{State: bundle.MappingIdentityInvalid}, nil
	}
	if len(validAnchors) == 1 {
		return bundle.MappingIdentity{Fragment: validAnchors[0].Value, Node: validAnchors[0], State: bundle.MappingIdentityValid}, nil
	}
	return bundle.MappingIdentity{State: bundle.MappingIdentityInvalid}, ctx.Err()
}

func validMappingFragmentsContext(ctx context.Context, values []*yaml.Node) ([]*yaml.Node, error) {
	valid := make([]*yaml.Node, 0, len(values))
	for _, value := range values {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if value == nil || value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
			continue
		}
		accepted, err := validRelationFragmentContext(ctx, value.Value)
		if err != nil {
			return nil, err
		}
		if accepted {
			valid = append(valid, value)
		}
	}
	return valid, ctx.Err()
}

func validRelationFragmentContext(ctx context.Context, value string) (bool, error) {
	if value == "" {
		return false, ctx.Err()
	}
	first, last := true, rune(0)
	for offset := 0; offset < len(value); {
		if offset&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		decoded, size := utf8.DecodeRuneInString(value[offset:])
		if decoded == utf8.RuneError && size == 1 || decoded == '#' || decoded < 0x20 || decoded == 0x7f {
			return false, nil
		}
		if first && unicode.IsSpace(decoded) {
			return false, nil
		}
		first, last = false, decoded
		offset += size
	}
	return !unicode.IsSpace(last), ctx.Err()
}

type relationTargetProjection struct {
	key     relationRefKey
	target  *yaml.Node
	ordinal int
}

func duplicateRelationTargetsInSequenceContext(ctx context.Context, items *yaml.Node, id bundle.ConceptID, fragment string) ([]*yaml.Node, error) {
	targets, err := relationTargetsInSequenceContext(ctx, items, id, fragment)
	if err != nil {
		return nil, err
	}
	ordered := make([]relationTargetProjection, 0, len(targets))
	for ordinal, target := range targets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		semantic := yamlAliasTarget(target)
		ref, err := bundle.ParseRelationRef(semantic.Value)
		if err != nil {
			continue
		}
		ordered = append(ordered, relationTargetProjection{
			key: relationRefIdentity(ref), target: target, ordinal: ordinal,
		})
	}
	if err := sortSliceCompareContext(ctx, ordered, compareRelationTargetProjectionContext); err != nil {
		return nil, err
	}
	for start := 0; start < len(ordered); {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := start + 1
		for end < len(ordered) {
			equal, err := equalRelationRefKeyContext(ctx, ordered[start].key, ordered[end].key)
			if err != nil {
				return nil, err
			}
			if !equal {
				break
			}
			end++
		}
		if end-start > 1 {
			duplicates := make([]*yaml.Node, end-start)
			for index := range duplicates {
				duplicates[index] = ordered[start+index].target
			}
			return duplicates, ctx.Err()
		}
		start = end
	}
	return nil, ctx.Err()
}

func relationTargetsInSequenceContext(ctx context.Context, items *yaml.Node, id bundle.ConceptID, fragment string) ([]*yaml.Node, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	items = yamlAliasTarget(items)
	if items == nil || items.Kind != yaml.SequenceNode {
		return nil, nil
	}
	var matches []*yaml.Node
	for _, item := range items.Content {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		targets, err := mappingValuesForKeyContext(ctx, yamlAliasTarget(item), "target")
		if err != nil {
			return nil, err
		}
		for _, target := range targets {
			matched, err := matchesRelationTargetContext(ctx, target, id, fragment)
			if err != nil {
				return nil, err
			}
			if matched {
				matches = append(matches, target)
			}
		}
	}
	return matches, ctx.Err()
}

func compareRelationTargetProjectionContext(ctx context.Context, left, right relationTargetProjection) (int, error) {
	if order, err := compareStringsContext(ctx, left.key.id, right.key.id); err != nil || order != 0 {
		return order, err
	}
	if order, err := compareStringsContext(ctx, left.key.fragment, right.key.fragment); err != nil || order != 0 {
		return order, err
	}
	switch {
	case left.ordinal < right.ordinal:
		return -1, ctx.Err()
	case left.ordinal > right.ordinal:
		return 1, ctx.Err()
	default:
		return 0, ctx.Err()
	}
}

func equalRelationRefKeyContext(ctx context.Context, left, right relationRefKey) (bool, error) {
	idEqual, err := stringsEqualContext(ctx, left.id, right.id)
	if err != nil || !idEqual {
		return false, err
	}
	return stringsEqualContext(ctx, left.fragment, right.fragment)
}
func yamlAliasTarget(n *yaml.Node) *yaml.Node {
	if n != nil && (n.Kind == yaml.AliasNode || n.Alias != nil) && n.Alias != nil {
		return n.Alias
	}
	return n
}
func fragmentPatchContext(ctx context.Context, p *presentation, from, to string) (bytePatch, bool, error) {
	if err := ctx.Err(); err != nil {
		return bytePatch{}, false, err
	}
	alias, err := canonicalAliasProvenanceContext(ctx, p.root, from)
	if err != nil {
		return bytePatch{}, false, err
	}
	if len(alias) != 0 {
		return bytePatch{}, false, p.yamlNodeError("alias_provenance", ErrAmbiguousPresentation, alias...)
	}
	var matches []*yaml.Node
	err = walkMappingsContext(ctx, p.root, excludeRootMapping, func(n *yaml.Node) error {
		if err := p.rejectMergedCanonicalFragmentContext(ctx, n, from); err != nil {
			return err
		}
		canonical, node, ambiguous := canonicalFragmentNode(n)
		if err := p.unsupportedIdentityPresentationForFragmentContext(ctx, n, from); err != nil {
			return err
		}
		if ambiguous {
			mentions, err := identityMentionsFragmentContext(ctx, n, from)
			if err != nil {
				return err
			}
			if !mentions {
				return nil
			}
			return p.yamlNodeError("duplicate_canonical_fragment", ErrAmbiguousPresentation, identityKeyNodes(n)...)
		}
		if canonical != from {
			return nil
		}
		if n.Style&yaml.FlowStyle != 0 {
			return p.yamlNodeError("canonical_fragment_flow_mapping", ErrUnsupportedPresentation, n)
		}
		matches = append(matches, node)
		return nil
	})
	if err != nil {
		return bytePatch{}, false, err
	}
	if len(matches) > 1 {
		return bytePatch{}, false, p.yamlNodeError("duplicate_canonical_fragment", ErrAmbiguousPresentation, matches...)
	}
	if len(matches) == 0 {
		return bytePatch{}, false, nil
	}
	result, err := p.resolvedScalarPatch(matches[0], to)
	return result, err == nil, err
}

func canonicalFragmentCandidatesContext(ctx context.Context, root *yaml.Node, fragment string) ([]*yaml.Node, error) {
	var candidates []*yaml.Node
	err := walkMappingsContext(ctx, root, excludeRootMapping, func(mapping *yaml.Node) error {
		identity := bundle.ResolveMappingIdentity(mapping)
		switch {
		case identity.State == bundle.MappingIdentityValid && identity.Fragment == fragment:
			candidates = append(candidates, identity.Node)
		case identity.State == bundle.MappingIdentityInvalid:
			mentions, err := identityMentionsFragmentContext(ctx, mapping, fragment)
			if err != nil {
				return err
			}
			if mentions {
				candidates = append(candidates, identityKeyNodes(mapping)...)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return candidates, ctx.Err()
}

func canonicalAliasProvenanceContext(ctx context.Context, n *yaml.Node, fragment string) ([]*yaml.Node, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if n == nil {
		return nil, nil
	}
	if n.Kind == yaml.MappingNode {
		for _, key := range []string{"id", "anchor"} {
			values, err := mappingValuesForKeyContext(ctx, n, key)
			if err != nil {
				return nil, err
			}
			for _, value := range values {
				if value != nil && (value.Kind == yaml.AliasNode || value.Alias != nil) && value.Alias != nil && value.Alias.Value == fragment {
					return []*yaml.Node{value, value.Alias}, nil
				}
			}
		}
	}
	if n.Kind == yaml.AliasNode && n.Alias != nil && (n.Alias.Kind == yaml.MappingNode || n.Alias.Kind == yaml.SequenceNode) {
		matched, err := canonicalIdentityInAliasTargetContext(ctx, n.Alias, fragment, make(map[*yaml.Node]bool))
		if err != nil {
			return nil, err
		}
		if matched != nil {
			return []*yaml.Node{n, matched}, nil
		}
	}
	for _, child := range n.Content {
		found, err := canonicalAliasProvenanceContext(ctx, child, fragment)
		if err != nil || len(found) != 0 {
			return found, err
		}
	}
	return nil, nil
}

func canonicalIdentityInAliasTargetContext(ctx context.Context, n *yaml.Node, fragment string, visited map[*yaml.Node]bool) (*yaml.Node, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if n == nil || visited[n] {
		return nil, nil
	}
	visited[n] = true
	if n.Kind == yaml.AliasNode || n.Alias != nil {
		return canonicalIdentityInAliasTargetContext(ctx, n.Alias, fragment, visited)
	}
	if n.Kind == yaml.MappingNode {
		identity := bundle.ResolveMappingIdentity(n)
		if identity.State == bundle.MappingIdentityValid && identity.Fragment == fragment {
			return identity.Node, nil
		}
		if identity.State == bundle.MappingIdentityInvalid {
			mentions, err := identityMentionsFragmentContext(ctx, n, fragment)
			if err != nil {
				return nil, err
			}
			if mentions {
				nodes := identityKeyNodes(n)
				if len(nodes) != 0 {
					return nodes[0], nil
				}
				return n, nil
			}
		}
	}
	for _, child := range n.Content {
		matched, err := canonicalIdentityInAliasTargetContext(ctx, child, fragment, visited)
		if err != nil || matched != nil {
			return matched, err
		}
	}
	return nil, nil
}

// identityMentionsFragmentContext reports whether a mapping carries an
// id/anchor that names fragment, including a block-scalar trailing newline.
func identityMentionsFragmentContext(ctx context.Context, mapping *yaml.Node, fragment string) (bool, error) {
	for _, key := range []string{"id", "anchor"} {
		values, err := mappingValuesForKeyContext(ctx, mapping, key)
		if err != nil {
			return false, err
		}
		for _, value := range values {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			if value == nil || value.Kind != yaml.ScalarNode {
				continue
			}
			candidate := value.Value
			if strings.HasSuffix(candidate, "\n") {
				candidate = candidate[:len(candidate)-1]
			}
			equal, err := stringsEqualContext(ctx, candidate, fragment)
			if err != nil || equal {
				return equal, err
			}
		}
	}
	return false, ctx.Err()
}

func ensureRelationPresentationContext(ctx context.Context, data []byte, source bundle.RelationRef, typ, target string) ([]byte, error) {
	p, err := parsePresentationContext(ctx, data)
	if err != nil {
		return nil, err
	}
	n, err := mappingForRef(ctx, p, p.root, source)
	if err != nil {
		return nil, err
	}
	if n == nil {
		return nil, fmt.Errorf("source fragment missing")
	}
	if err := p.rejectMergedKeyContext(ctx, n, "relations"); err != nil {
		return nil, err
	}
	rels, err := mappingValuesForKeyContext(ctx, n, "relations")
	if err != nil {
		return nil, err
	}
	if len(rels) > 1 {
		return nil, p.touchedDuplicateKeyError(n, "relations")
	}
	if len(rels) == 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := p.requireInsertionProvenanceContext(ctx, n); err != nil {
			return nil, err
		}
		expected, err := p.expectedEnsureRelationContext(ctx, n, typ, target)
		if err != nil {
			return nil, err
		}
		typeToken, err := yamlScalarTokenContext(ctx, typ, 0)
		if err != nil {
			return nil, err
		}
		targetToken, err := yamlScalarTokenContext(ctx, target, 0)
		if err != nil {
			return nil, err
		}
		return p.insertAfterContext(ctx, n, strings.Repeat(" ", n.Column-1)+"relations:"+string(p.newline)+strings.Repeat(" ", n.Column+1)+typeToken+":"+string(p.newline)+strings.Repeat(" ", n.Column+3)+"- target: "+targetToken+string(p.newline), expected)
	}
	relations := rels[0]
	if relations.Kind == yaml.AliasNode || relations.Alias != nil {
		return nil, p.yamlNodeError("alias_provenance", ErrAmbiguousPresentation, relations)
	}
	if relations.Kind != yaml.MappingNode || relations.Style&yaml.FlowStyle != 0 {
		return nil, p.yamlNodeError("relations_not_block_mapping", ErrUnsupportedPresentation, relations)
	}
	if err := p.rejectMergedKeyContext(ctx, relations, typ); err != nil {
		return nil, err
	}
	seqs, err := mappingValuesForKeyContext(ctx, relations, typ)
	if err != nil {
		return nil, err
	}
	if len(seqs) > 1 {
		return nil, p.touchedDuplicateKeyError(relations, typ)
	}
	if len(seqs) == 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := p.requireInsertionProvenanceContext(ctx, relations); err != nil {
			return nil, err
		}
		expected, err := p.expectedEnsureRelationContext(ctx, n, typ, target)
		if err != nil {
			return nil, err
		}
		typeToken, err := yamlScalarTokenContext(ctx, typ, 0)
		if err != nil {
			return nil, err
		}
		targetToken, err := yamlScalarTokenContext(ctx, target, 0)
		if err != nil {
			return nil, err
		}
		return p.insertAfterContext(ctx, relations, strings.Repeat(" ", relations.Column-1)+typeToken+":"+string(p.newline)+strings.Repeat(" ", relations.Column+1)+"- target: "+targetToken+string(p.newline), expected)
	}
	seq := seqs[0]
	if seq.Kind == yaml.AliasNode || seq.Alias != nil {
		return nil, p.yamlNodeError("alias_provenance", ErrAmbiguousPresentation, seq)
	}
	if seq.Kind != yaml.SequenceNode || seq.Style&yaml.FlowStyle != 0 {
		return nil, p.yamlNodeError("relation_type_not_block_sequence", ErrUnsupportedPresentation, seq)
	}
	var matches []*yaml.Node
	for _, item := range seq.Content {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		semanticItem := yamlAliasTarget(item)
		if err := p.rejectMergedKeyContext(ctx, semanticItem, "target"); err != nil {
			return nil, err
		}
		vals, err := mappingValuesForKeyContext(ctx, semanticItem, "target")
		if err != nil {
			return nil, err
		}
		if len(vals) > 1 {
			return nil, p.touchedDuplicateKeyError(semanticItem, "target")
		}
		for _, v := range vals {
			semantic := yamlAliasTarget(v)
			if semantic != nil && semantic.Value == target {
				matches = append(matches, item)
			}
		}
	}
	if len(matches) > 1 {
		return nil, p.yamlNodeError("duplicate_target", ErrAmbiguousPresentation, matches...)
	}
	if err := p.requireSupportedRelationSequence(seq); err != nil {
		return nil, err
	}
	if len(matches) == 1 {
		return appendBytesContext(ctx, nil, data)
	}
	if err := p.requireInsertionProvenanceContext(ctx, seq); err != nil {
		return nil, err
	}
	expected, err := p.expectedEnsureRelationContext(ctx, n, typ, target)
	if err != nil {
		return nil, err
	}
	targetToken, err := yamlScalarTokenContext(ctx, target, 0)
	if err != nil {
		return nil, err
	}
	return p.insertAfterContext(ctx, seq, strings.Repeat(" ", seq.Column-1)+"- target: "+targetToken+string(p.newline), expected)
}

func (p *presentation) expectedEnsureRelationContext(ctx context.Context, source *yaml.Node, typ, target string) (*yamlSemanticNode, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, ok := p.paths[source]
	if !ok {
		return nil, p.yamlNodeError("semantic_edit_path", ErrUnsupportedPresentation, source)
	}
	expected, err := semanticYAMLContext(ctx, p.root)
	if err != nil {
		return nil, err
	}
	mapping := nodeAtPathNode(expected, path)
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil, p.yamlNodeError("semantic_edit_path", ErrUnsupportedPresentation, source)
	}
	relations := semanticMappingValue(mapping, "relations")
	if relations == nil {
		relations = &yamlSemanticNode{Kind: yaml.MappingNode, Tag: "!!map"}
		semanticMappingAppend(mapping, "relations", relations)
	}
	if relations.Kind != yaml.MappingNode {
		return nil, p.yamlNodeError("relations_not_block_mapping", ErrUnsupportedPresentation, source)
	}
	sequence := semanticMappingValue(relations, typ)
	if sequence == nil {
		sequence = &yamlSemanticNode{Kind: yaml.SequenceNode, Tag: "!!seq"}
		semanticMappingAppend(relations, typ, sequence)
	}
	if sequence.Kind != yaml.SequenceNode {
		return nil, p.yamlNodeError("relation_type_not_block_sequence", ErrUnsupportedPresentation, source)
	}
	item := &yamlSemanticNode{Kind: yaml.MappingNode, Tag: "!!map"}
	semanticMappingAppend(item, "target", &yamlSemanticNode{Kind: yaml.ScalarNode, Tag: "!!str", Value: target})
	sequence.Content = append(sequence.Content, item)
	return expected, nil
}

func (p *presentation) requireSupportedRelationSequence(seq *yaml.Node) error {
	if seq == nil || seq.Kind == yaml.AliasNode || seq.Alias != nil {
		return p.yamlNodeError("alias_provenance", ErrAmbiguousPresentation, seq)
	}
	if err := p.requireInsertionProvenanceContext(p.ctx, seq); err != nil {
		return err
	}
	// Alias/merge ambiguity has priority over a separate unsupported item in
	// the same sequence because it proves multiple raw provenance candidates.
	for _, item := range seq.Content {
		if item != nil && (item.Kind == yaml.AliasNode || item.Alias != nil) {
			return p.yamlNodeError("alias_provenance", ErrAmbiguousPresentation, item)
		}
		if item != nil && item.Kind == yaml.MappingNode {
			if err := p.rejectMergedKeyContext(p.ctx, item, "target"); err != nil {
				return err
			}
		}
	}
	for _, item := range seq.Content {
		if item == nil || item.Kind != yaml.MappingNode || item.Style&yaml.FlowStyle != 0 || len(item.Content)%2 != 0 {
			return p.yamlNodeError("relation_item_presentation", ErrUnsupportedPresentation, item)
		}
		targets, err := mappingValuesForKeyContext(p.ctx, item, "target")
		if err != nil {
			return err
		}
		if len(targets) > 1 {
			return p.touchedDuplicateKeyError(item, "target")
		}
		if len(targets) != 1 {
			return p.yamlNodeError("relation_item_presentation", ErrUnsupportedPresentation, item)
		}
		target := targets[0]
		if target == nil || target.Kind == yaml.AliasNode || target.Alias != nil {
			return p.yamlNodeError("alias_provenance", ErrAmbiguousPresentation, target)
		}
		if target.Kind != yaml.ScalarNode || target.Tag != "!!str" || target.Style&(yaml.LiteralStyle|yaml.FoldedStyle|yaml.FlowStyle) != 0 {
			return p.yamlNodeError("relation_item_presentation", ErrUnsupportedPresentation, target)
		}
		if err := p.requireTouchedProvenance(target); err != nil {
			return err
		}
		if _, err := p.resolver.scalar(target); err != nil {
			return err
		}
	}
	return nil
}
func (p *presentation) insertAfterContext(ctx context.Context, n *yaml.Node, text string, expected *yamlSemanticNode) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := p.requireInsertionProvenanceContext(ctx, n); err != nil {
		return nil, err
	}
	at, err := p.insertionPoint(n)
	if err != nil {
		return nil, err
	}
	patch := bytePatch{Start: p.yamlStart + at, End: p.yamlStart + at, Text: []byte(text), edit: &semanticEdit{expected: expected}}
	return p.patchYAMLContext(ctx, []bytePatch{patch})
}

// insertionPoint uses parser ownership rather than scanning every descendant.
// The first following AST sibling starts the next owned block; if none exists,
// the boundary is inherited from the parent up to the YAML document end.
func (p *presentation) insertionPoint(n *yaml.Node) (int, error) {
	if n != nil && (n.Kind == yaml.AliasNode || n.Alias != nil) {
		return 0, p.yamlNodeError(yamlCodeAliasProvenance, ErrAmbiguousPresentation, n)
	}
	if n == nil || (n.Kind != yaml.MappingNode && n.Kind != yaml.SequenceNode) || n.Style&yaml.FlowStyle != 0 || n.Anchor != "" {
		return 0, p.yamlNodeError("unsupported_block", ErrUnsupportedPresentation, n)
	}
	current := n
	for parent := p.parents[current]; parent != nil; parent = p.parents[current] {
		index := yamlChildIndex(parent, current)
		if index < 0 {
			return 0, p.yamlNodeError("unowned_insertion_block", ErrUnsupportedPresentation, current)
		}
		next := index + 1
		if parent.Kind == yaml.MappingNode {
			if index%2 == 0 {
				return 0, p.yamlNodeError("unowned_insertion_block", ErrUnsupportedPresentation, current)
			}
			next = index + 1 // next mapping key after the current value
		}
		if next < len(parent.Content) {
			line := parent.Content[next].Line
			if line < 1 || line > len(p.resolver.lines) {
				return 0, p.yamlNodeError("invalid_coordinate", ErrUnsupportedPresentation, parent.Content[next])
			}
			return p.proveInsertionTrivia(p.resolver.lines[line-1])
		}
		current = parent
	}
	return p.proveInsertionTrivia(len(p.yaml))
}

// proveInsertionTrivia rejects a boundary preceded by standalone comments or
// blank lines. yaml.v3 does not preserve whether that trivia belongs to the
// current collection or the following sibling, so moving it across a newly
// inserted node would not be a lossless mutation.
func (p *presentation) proveInsertionTrivia(at int) (int, error) {
	if at < 0 || at > len(p.yaml) {
		point := min(max(at, 0), len(p.yaml))
		return 0, p.yamlLocalError("invalid_coordinate", ErrUnsupportedPresentation, SourceSpan{Start: point, End: point})
	}
	lineEnd := at
	if lineEnd > 0 && p.yaml[lineEnd-1] == '\n' {
		lineEnd--
		if lineEnd > 0 && p.yaml[lineEnd-1] == '\r' {
			lineEnd--
		}
	}
	lineStart := bytes.LastIndexByte(p.yaml[:lineEnd], '\n') + 1
	line := bytes.TrimSpace(p.yaml[lineStart:lineEnd])
	if len(line) == 0 || line[0] == '#' {
		return 0, p.yamlLocalError("ambiguous_insertion_trivia", ErrUnsupportedPresentation, SourceSpan{Start: lineStart, End: at})
	}
	return at, nil
}

// yamlRenderValue is the private, typed input language for structural YAML
// mutations. It is deliberately not a raw YAML escape hatch.
