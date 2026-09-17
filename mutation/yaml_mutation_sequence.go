package mutation

import (
	"context"
	"crypto/sha256"

	"gopkg.in/yaml.v3"
)

func (m *yamlMutation) accumulatedSequenceLineage(
	collection *yaml.Node,
	base, accumulated yamlRenderValue,
) ([]yamlSequenceLineage, error) {
	if shadow := m.sequenceShadows[collection]; shadow != nil {
		if len(shadow.items) != len(accumulated.sequence) {
			return nil, errYAMLDesiredConflict
		}
		lineages := make([]yamlSequenceLineage, len(shadow.items))
		for i, item := range shadow.items {
			if err := m.ctx.Err(); err != nil {
				return nil, err
			}
			lineages[i] = item.lineage
		}
		return lineages, m.ctx.Err()
	}
	if stored, present := m.collectionLineage[collection]; present {
		if len(stored) != len(accumulated.sequence) {
			return nil, errYAMLDesiredConflict
		}
		return cloneSequenceLineages(stored), m.ctx.Err()
	}
	equal, equalErr := equalYAMLRenderStateContext(m.ctx, base, accumulated)
	if equalErr != nil {
		return nil, equalErr
	}
	if equal {
		lineages := make([]yamlSequenceLineage, len(base.sequence))
		for i := range lineages {
			lineages[i] = yamlSequenceLineage{baseIndex: i}
		}
		return lineages, nil
	}
	expected := m.expectedNodes[collection]
	sourceItems, err := m.p.sequenceSelectorItems(collection)
	if err != nil {
		return nil, err
	}
	if expected == nil || expected.Kind != yaml.SequenceNode ||
		len(expected.Content) != len(accumulated.sequence) {
		return nil, errYAMLDesiredConflict
	}
	lineages := make([]yamlSequenceLineage, len(expected.Content))
	baseByExpected := make(map[*yamlSemanticNode]int, len(sourceItems))
	for sourceIndex, source := range sourceItems {
		candidate := m.expectedNodes[source]
		if candidate != nil {
			baseByExpected[candidate] = sourceIndex
		}
	}
	for accumulatedIndex, candidate := range expected.Content {
		baseIndex, present := baseByExpected[candidate]
		if !present {
			return nil, errYAMLDesiredConflict
		}
		lineages[accumulatedIndex] = yamlSequenceLineage{baseIndex: baseIndex}
	}
	return lineages, nil
}

func (m *yamlMutation) foldCollectionYAMLDesired(
	collection *yaml.Node,
	base, accumulated, whole yamlRenderValue,
	route string,
) (yamlRenderValue, []yamlSequenceLineage, error) {
	if !isSequenceIdentityRoute(route) || base.kind != yamlRenderSequence ||
		accumulated.kind != yamlRenderSequence || whole.kind != yamlRenderSequence {
		merged, err := m.foldYAMLDesired(collection, base, accumulated, whole, route)
		return merged, nil, err
	}
	lineages, err := m.accumulatedSequenceLineage(collection, base, accumulated)
	if err != nil {
		return yamlRenderValue{}, nil, m.yamlDesiredError(collection, err)
	}
	merged, mergedLineage, err := mergeYAMLDesiredSequenceWithLineageContext(
		m.ctx,
		base,
		accumulated,
		whole,
		route,
		lineages,
	)
	if err != nil {
		return yamlRenderValue{}, nil, m.yamlDesiredError(collection, err)
	}
	return merged, mergedLineage, nil
}

func (m *yamlMutation) requireUniqueSequence(owner *yaml.Node, values []yamlRenderValue, route string) error {
	if !isSequenceIdentityRoute(route) {
		return nil
	}
	if _, err := sequenceIdentitiesContext(m.ctx, yamlSequence(values...), route); err != nil {
		return m.p.preserveTraversalError(err, yamlCodeAmbiguousSelector, ErrAmbiguousPresentation, owner)
	}
	return nil
}

func (m *yamlMutation) mutateReplacementDescendantMapping(
	mapping *yaml.Node,
	key string,
	value yamlRenderValue,
	mode string,
) (bool, error) {
	ancestor, steps, found, pathErr := m.replacementRenderPath(mapping)
	if pathErr != nil {
		return true, pathErr
	}
	if !found {
		shadow, item, shadowSteps, shadowFound, shadowErr := m.replacementShadowPath(mapping)
		if shadowErr != nil {
			return true, shadowErr
		}
		if !shadowFound {
			return false, nil
		}
		root, cloneErr := cloneYAMLRenderValueContext(m.ctx, item.value)
		if cloneErr != nil {
			return true, cloneErr
		}
		target, resolved, resolveErr := yamlRenderValueAtPathContext(m.ctx, &root, shadowSteps)
		if resolveErr != nil {
			return true, resolveErr
		}
		if !resolved || target.kind != yamlRenderMapping {
			return true, m.p.yamlNodeError(yamlCodeOverlappingPatch, ErrUnsupportedPresentation, item.source, mapping)
		}
		changed, mutationErr := m.foldReplacementMappingEntry(mapping, target, key, value, mode)
		if mutationErr != nil || !changed {
			return true, mutationErr
		}
		return true, m.commitReplacementShadowRoot(shadow, item, root)
	}
	root, cloneErr := cloneYAMLRenderValueContext(m.ctx, m.collectionValues[ancestor])
	if cloneErr != nil {
		return true, cloneErr
	}
	target, resolved, resolveErr := yamlRenderValueAtPathContext(m.ctx, &root, steps)
	if resolveErr != nil {
		return true, resolveErr
	}
	if !resolved || target.kind != yamlRenderMapping {
		return true, m.p.yamlNodeError(yamlCodeOverlappingPatch, ErrUnsupportedPresentation, ancestor, mapping)
	}
	accumulatedTarget, cloneErr := cloneYAMLRenderValueContext(m.ctx, *target)
	if cloneErr != nil {
		return true, cloneErr
	}
	changed, mutationErr := m.foldReplacementMappingEntry(mapping, target, key, value, mode)
	if mutationErr != nil || !changed {
		return true, mutationErr
	}
	base, baseErr := yamlRenderFromNodeIgnoringCommentsContext(m.ctx, mapping)
	if baseErr != nil {
		return true, m.p.renderNodeError(baseErr, mapping)
	}
	mergedTarget, mergeErr := m.foldYAMLDesired(mapping, base, accumulatedTarget, *target, m.collectionRoute(mapping))
	if mergeErr != nil {
		return true, mergeErr
	}
	*target = mergedTarget
	if uniquenessErr := validateYAMLDesiredUniquenessContext(m.ctx, root, m.collectionRoute(ancestor)); uniquenessErr != nil {
		return true, m.p.preserveTraversalError(uniquenessErr, yamlCodeAmbiguousSelector, ErrAmbiguousPresentation, mapping, ancestor)
	}
	return true, m.installCollectionValue(ancestor, root)
}

func sequenceIdentityFromSemanticContext(ctx context.Context, node *yamlSemanticNode, route string) (yamlSequenceIdentity, error) {
	if node == nil || node.Kind != yaml.MappingNode {
		rendered, err := yamlRenderFromSemanticContext(ctx, node)
		if err != nil {
			return yamlSequenceIdentity{}, err
		}
		return sequenceIdentityContext(ctx, rendered, route)
	}
	var keys []string
	switch route {
	case "sources":
		_, count, err := semanticMappingEntryIndexContext(ctx, node, "id")
		if err != nil {
			return yamlSequenceIdentity{}, err
		}
		if count == 1 {
			keys = []string{"id"}
		}
	case "verified":
		keys = []string{"by", "at"}
	case "parameters":
		keys = []string{"name"}
	}
	if len(keys) == 0 {
		rendered, err := yamlRenderFromSemanticContext(ctx, node)
		if err != nil {
			return yamlSequenceIdentity{}, err
		}
		return sequenceIdentityContext(ctx, rendered, route)
	}
	identity := yamlSequenceIdentity{kind: route}
	for _, key := range keys {
		index, count, err := semanticMappingEntryIndexContext(ctx, node, key)
		if err != nil {
			return yamlSequenceIdentity{}, err
		}
		if count != 1 || index+1 >= len(node.Content) {
			rendered, err := yamlRenderFromSemanticContext(ctx, node)
			if err != nil {
				return yamlSequenceIdentity{}, err
			}
			return sequenceIdentityContext(ctx, rendered, route)
		}
		field, err := yamlRenderFromSemanticContext(ctx, node.Content[index+1])
		if err != nil {
			return yamlSequenceIdentity{}, err
		}
		identity.fields = append(identity.fields, yamlSelector(key, field))
	}
	canonical, err := sequenceIdentityCanonicalContext(ctx, identity)
	if err != nil {
		return yamlSequenceIdentity{}, err
	}
	identity.canonical = canonical
	identity.canonicalDigest, err = stringFingerprintContext(ctx, canonical)
	return identity, err
}

func (m *yamlMutation) validateOrdinaryIdentityMutation(
	mapping *yaml.Node,
	key string,
	value yamlRenderValue,
	mode string,
) error {
	sequence := m.p.parents[mapping]
	if sequence == nil || sequence.Kind != yaml.SequenceNode {
		return nil
	}
	route := m.collectionRoute(sequence)
	if !isSequenceIdentityRoute(route) {
		return nil
	}
	expectedItem := m.expectedNodes[mapping]
	if expectedItem == nil {
		return m.p.yamlNodeError("semantic_edit_path", ErrUnsupportedPresentation, mapping, sequence)
	}

	var identities []yamlSequenceIdentity
	var unresolvedIdentities []bool
	targetIndex := -1
	if shadow := m.sequenceShadows[sequence]; shadow != nil {
		identities = make([]yamlSequenceIdentity, len(shadow.items))
		unresolvedIdentities = make([]bool, len(shadow.items))
		for index, item := range shadow.items {
			identities[index] = item.identity
			unresolvedIdentities[index] = item.identity.kind == "unresolved"
			if item.source == mapping {
				targetIndex = index
				expectedItem = item.semantic
			}
		}
	} else {
		items, err := m.p.sequenceSelectorItems(sequence)
		if err != nil {
			return err
		}
		identities = make([]yamlSequenceIdentity, len(items))
		unresolvedIdentities = make([]bool, len(items))
		for index, item := range items {
			identity, unresolvedIdentityErr, identityErr := m.p.provenSequenceIdentityDeferred(item, route)
			if identityErr != nil {
				return identityErr
			}
			unresolvedIdentities[index] = unresolvedIdentityErr != nil
			if override, present := m.identityOverride[item]; present {
				identity = override
				unresolvedIdentities[index] = false
			}
			identities[index] = identity
			if item == mapping {
				targetIndex = index
			}
		}
	}
	if targetIndex < 0 {
		return m.p.yamlNodeError(yamlCodeOverlappingPatch, ErrUnsupportedPresentation, mapping, sequence)
	}
	identityBuckets := make(map[[sha256.Size]byte][]int, len(identities))
	for index, identity := range identities {
		if unresolvedIdentities[index] {
			continue
		}
		canonical := identity.canonical
		if canonical == "" {
			var canonicalErr error
			canonical, canonicalErr = sequenceIdentityCanonicalContext(m.ctx, identity)
			if canonicalErr != nil {
				return canonicalErr
			}
			identities[index].canonical = canonical
		}
		digest, digestErr := stringFingerprintContext(m.ctx, canonical)
		if digestErr != nil {
			return digestErr
		}
		identities[index].canonicalDigest = digest
		for _, previous := range identityBuckets[digest] {
			equal, equalErr := equalSequenceIdentityContext(m.ctx, identities[index], identities[previous])
			if equalErr != nil {
				return equalErr
			}
			if equal {
				return m.p.yamlNodeError(yamlCodeAmbiguousSelector, ErrAmbiguousPresentation, sequence)
			}
		}
		identityBuckets[digest] = append(identityBuckets[digest], index)
	}

	desiredItem, err := cloneSemanticNodeContext(m.ctx, expectedItem)
	if err != nil {
		return err
	}
	valueSemantic, err := value.semanticContext(m.ctx)
	if err != nil {
		return err
	}
	entryIndex, count, err := semanticMappingEntryIndexContext(m.ctx, desiredItem, key)
	if err != nil {
		return err
	}
	switch mode {
	case "replace":
		if count != 1 {
			return nil
		}
		desiredItem.Content[entryIndex+1] = valueSemantic
	case "insert":
		if count != 0 {
			return nil
		}
		semanticMappingAppend(desiredItem, key, valueSemantic)
	case "delete":
		if count != 1 {
			return nil
		}
		desiredItem.Content = append(desiredItem.Content[:entryIndex], desiredItem.Content[entryIndex+2:]...)
	default:
		return nil
	}
	desiredIdentity, err := sequenceIdentityFromSemanticContext(m.ctx, desiredItem, route)
	if err != nil {
		return m.p.preserveTraversalError(err, yamlCodeUnsupportedSelector, ErrUnsupportedPresentation, mapping)
	}
	desiredCanonical, err := sequenceIdentityCanonicalContext(m.ctx, desiredIdentity)
	if err != nil {
		return err
	}
	desiredIdentity.canonical = desiredCanonical
	desiredDigest, err := stringFingerprintContext(m.ctx, desiredCanonical)
	if err != nil {
		return err
	}
	desiredIdentity.canonicalDigest = desiredDigest
	for _, index := range identityBuckets[desiredDigest] {
		equal, equalErr := equalSequenceIdentityContext(m.ctx, desiredIdentity, identities[index])
		if equalErr != nil {
			return equalErr
		}
		if index != targetIndex && equal {
			return m.p.yamlNodeError(yamlCodeAmbiguousSelector, ErrAmbiguousPresentation, mapping, sequence)
		}
	}
	m.identityOverride[mapping] = desiredIdentity
	if shadow := m.sequenceShadows[sequence]; shadow != nil {
		for _, item := range shadow.items {
			if item.source == mapping {
				item.identity = desiredIdentity
				break
			}
		}
	}
	return nil
}

func (m *yamlMutation) validateMappingEntryUniqueness(
	mapping *yaml.Node,
	key string,
	value yamlRenderValue,
	mode string,
) error {
	expected := m.expectedNodes[mapping]
	if expected == nil || expected.Kind != yaml.MappingNode {
		return m.p.yamlNodeError("semantic_edit_path", ErrUnsupportedPresentation, mapping)
	}
	_, count, err := semanticMappingEntryIndexContext(m.ctx, expected, key)
	if err != nil {
		return err
	}
	if count > 1 {
		return m.p.yamlNodeError(yamlCodeDuplicateTouchedKey, ErrAmbiguousPresentation, mappingKeyNodes(mapping, key)...)
	}
	match, sourcePresent, err := m.p.structuralMappingEntry(mapping, key)
	if err != nil {
		return err
	}
	switch mode {
	case "replace":
		if count == 0 {
			return m.p.yamlNodeError(yamlCodeMissingMappingEntry, ErrUnsupportedPresentation, mapping)
		}
	case "insert":
		if count != 0 {
			return m.p.yamlNodeError(yamlCodeDuplicateTouchedKey, ErrAmbiguousPresentation, mappingKeyNodes(mapping, key)...)
		}
	case "delete":
		if count == 0 {
			return m.p.yamlNodeError(yamlCodeMissingMappingEntry, ErrUnsupportedPresentation, mapping)
		}
	default:
		return m.p.yamlNodeError(yamlCodeUnsupportedCollection, ErrUnsupportedPresentation, mapping)
	}
	// Granular mapping edits serialize only the touched value. Validate that
	// exact subtree (including sequence-route identity) without traversing
	// unrelated sibling YAML. Whole-value mutation paths retain recursive
	// uniqueness and provenance checks.
	var candidates []yamlRenderValue
	if sourcePresent {
		base, renderErr := yamlRenderFromNodeIgnoringCommentsContext(m.ctx, match.value)
		if renderErr != nil {
			return m.p.renderNodeError(renderErr, match.value)
		}
		candidates = append(candidates, base)
	}
	if count == 1 {
		index, _, indexErr := semanticMappingEntryIndexContext(m.ctx, expected, key)
		if indexErr != nil {
			return indexErr
		}
		accumulated, renderErr := yamlRenderFromSemanticContext(m.ctx, expected.Content[index+1])
		if renderErr != nil {
			return m.p.preserveTraversalError(renderErr, "semantic_edit_path", ErrUnsupportedPresentation, match.value, mapping)
		}
		candidates = append(candidates, accumulated)
	}
	if mode != "delete" {
		candidates = append(candidates, value)
	}
	for _, candidate := range candidates {
		if err := m.requireUniqueDesired(mapping, candidate, key); err != nil {
			return err
		}
	}
	return nil
}

func (m *yamlMutation) mutateReplacementDescendantSequence(
	collection *yaml.Node,
	selector yamlSequenceSelector,
	desired *yamlRenderValue,
) (bool, error) {
	ancestor, steps, found, pathErr := m.replacementRenderPath(collection)
	if pathErr != nil {
		return true, pathErr
	}
	var root yamlRenderValue
	var target *yamlRenderValue
	var commit func() error
	if !found {
		shadow, item, shadowSteps, shadowFound, shadowErr := m.replacementShadowPath(collection)
		if shadowErr != nil {
			return true, shadowErr
		}
		if !shadowFound {
			return false, nil
		}
		cloned, cloneErr := cloneYAMLRenderValueContext(m.ctx, item.value)
		if cloneErr != nil {
			return true, cloneErr
		}
		root = cloned
		var resolved bool
		var resolveErr error
		target, resolved, resolveErr = yamlRenderValueAtPathContext(m.ctx, &root, shadowSteps)
		if resolveErr != nil {
			return true, resolveErr
		}
		if !resolved {
			return true, m.p.yamlNodeError(yamlCodeOverlappingPatch, ErrUnsupportedPresentation, item.source, collection)
		}
		commit = func() error { return m.commitReplacementShadowRoot(shadow, item, root) }
	} else {
		cloned, cloneErr := cloneYAMLRenderValueContext(m.ctx, m.collectionValues[ancestor])
		if cloneErr != nil {
			return true, cloneErr
		}
		root = cloned
		var resolved bool
		var resolveErr error
		target, resolved, resolveErr = yamlRenderValueAtPathContext(m.ctx, &root, steps)
		if resolveErr != nil {
			return true, resolveErr
		}
		if !resolved {
			return true, m.p.yamlNodeError(yamlCodeOverlappingPatch, ErrUnsupportedPresentation, ancestor, collection)
		}
		commit = func() error { return m.setCollectionValue(ancestor, root) }
	}
	bare := target.kind == yamlRenderMapping
	var items []yamlRenderValue
	switch target.kind {
	case yamlRenderMapping:
		cloned, cloneErr := cloneYAMLRenderValueContext(m.ctx, *target)
		if cloneErr != nil {
			return true, cloneErr
		}
		items = []yamlRenderValue{cloned}
	case yamlRenderSequence:
		items = append([]yamlRenderValue(nil), target.sequence...)
	default:
		return true, m.p.yamlNodeError(yamlCodeOverlappingPatch, ErrUnsupportedPresentation, ancestor, collection)
	}
	matches := make([]int, 0, 1)
	for index, item := range items {
		matched, matchErr := matchesRenderSelectorContext(m.ctx, item, selector)
		if matchErr != nil {
			return true, matchErr
		}
		if matched {
			matches = append(matches, index)
		}
	}
	if len(matches) > 1 {
		return true, m.p.yamlNodeError(yamlCodeAmbiguousSelector, ErrAmbiguousPresentation, collection)
	}
	if desired == nil {
		if len(matches) == 0 {
			baseItems, baseErr := m.p.sequenceSelectorItems(collection)
			if baseErr != nil {
				return true, baseErr
			}
			baseMatches := 0
			for _, baseItem := range baseItems {
				matched, matchErr := m.p.matchesSequenceSelector(baseItem, selector)
				if matchErr != nil {
					return true, matchErr
				}
				if matched {
					baseMatches++
				}
			}
			switch baseMatches {
			case 0:
				return true, m.p.yamlNodeError(yamlCodeMissingSequenceItem, ErrUnsupportedPresentation, collection)
			case 1:
				// The immutable base item was already removed by the whole
				// branch. The identical granular delete coalesces.
				return true, nil
			default:
				return true, m.p.yamlNodeError(yamlCodeAmbiguousSelector, ErrAmbiguousPresentation, collection)
			}
		}
		index := matches[0]
		items = append(items[:index], items[index+1:]...)
		if err := m.requireUniqueSequence(collection, items, m.collectionRoute(collection)); err != nil {
			return true, err
		}
		*target = yamlSequence(items...)
		return true, commit()
	}
	if len(matches) == 0 {
		cloned, cloneErr := cloneYAMLRenderValueContext(m.ctx, *desired)
		if cloneErr != nil {
			return true, cloneErr
		}
		items = append(items, cloned)
		if err := m.requireUniqueSequence(collection, items, m.collectionRoute(collection)); err != nil {
			return true, err
		}
		*target = yamlSequence(items...)
		return true, commit()
	}
	index := matches[0]
	sourceItems, sourceErr := m.p.sequenceSelectorItems(collection)
	if sourceErr != nil {
		return true, m.p.preserveTraversalError(sourceErr, yamlCodeUnsupportedCollection, ErrUnsupportedPresentation, collection)
	}
	route := m.collectionRoute(collection)
	identity, identityErr := sequenceIdentityContext(m.ctx, items[index], route)
	if identityErr != nil {
		return true, identityErr
	}
	baseMatch := -1
	for sourceIndex, sourceItem := range sourceItems {
		base, baseErr := yamlRenderFromNodeIgnoringCommentsContext(m.ctx, sourceItem)
		if baseErr != nil {
			continue
		}
		baseIdentity, identityErr := sequenceIdentityContext(m.ctx, base, route)
		if identityErr != nil {
			return true, identityErr
		}
		equal, identityErr := equalSequenceIdentityContext(m.ctx, baseIdentity, identity)
		if identityErr != nil {
			return true, identityErr
		}
		if equal {
			if baseMatch >= 0 {
				return true, m.p.yamlNodeError(yamlCodeAmbiguousSelector, ErrAmbiguousPresentation, collection)
			}
			baseMatch = sourceIndex
		}
	}
	if baseMatch < 0 {
		equal, equalErr := equalYAMLRenderStateContext(m.ctx, items[index], *desired)
		if equalErr != nil {
			return true, equalErr
		}
		if !equal {
			return true, m.p.yamlNodeError(yamlCodeOverlappingPatch, ErrUnsupportedPresentation, collection)
		}
	} else {
		base, baseErr := yamlRenderFromNodeIgnoringCommentsContext(m.ctx, sourceItems[baseMatch])
		if baseErr != nil {
			return true, m.p.renderNodeError(baseErr, sourceItems[baseMatch])
		}
		merged, mergeErr := m.foldYAMLDesired(collection, base, items[index], *desired, "")
		if mergeErr != nil {
			return true, mergeErr
		}
		items[index] = merged
	}
	if err := m.requireUniqueSequence(collection, items, m.collectionRoute(collection)); err != nil {
		return true, err
	}
	if bare && len(items) == 1 {
		*target = items[0]
	} else {
		*target = yamlSequence(items...)
	}
	return true, commit()
}

func (m *yamlMutation) replaceReplacementDescendantCollection(
	collection *yaml.Node,
	desired yamlRenderValue,
) (bool, error) {
	ancestor, steps, found, pathErr := m.replacementRenderPath(collection)
	if pathErr != nil {
		return true, pathErr
	}
	if !found {
		shadow, item, shadowSteps, shadowFound, shadowErr := m.replacementShadowPath(collection)
		if shadowErr != nil {
			return true, shadowErr
		}
		if !shadowFound {
			return false, nil
		}
		root, cloneErr := cloneYAMLRenderValueContext(m.ctx, item.value)
		if cloneErr != nil {
			return true, cloneErr
		}
		target, resolved, resolveErr := yamlRenderValueAtPathContext(m.ctx, &root, shadowSteps)
		if resolveErr != nil {
			return true, resolveErr
		}
		if !resolved {
			return true, m.p.yamlNodeError(yamlCodeOverlappingPatch, ErrUnsupportedPresentation, item.source, collection)
		}
		base, err := yamlRenderFromNodeIgnoringCommentsContext(m.ctx, collection)
		if err != nil {
			return true, m.p.renderNodeError(err, collection)
		}
		merged, mergeErr := m.foldYAMLDesired(collection, base, *target, desired, m.collectionRoute(collection))
		if mergeErr != nil {
			return true, mergeErr
		}
		*target = merged
		return true, m.commitReplacementShadowRoot(shadow, item, root)
	}
	root, cloneErr := cloneYAMLRenderValueContext(m.ctx, m.collectionValues[ancestor])
	if cloneErr != nil {
		return true, cloneErr
	}
	target, resolved, resolveErr := yamlRenderValueAtPathContext(m.ctx, &root, steps)
	if resolveErr != nil {
		return true, resolveErr
	}
	if !resolved {
		return true, m.p.yamlNodeError(yamlCodeOverlappingPatch, ErrUnsupportedPresentation, ancestor, collection)
	}
	base, err := yamlRenderFromNodeIgnoringCommentsContext(m.ctx, collection)
	if err != nil {
		return true, m.p.renderNodeError(err, collection)
	}
	merged, mergeErr := m.foldYAMLDesired(collection, base, *target, desired, m.collectionRoute(collection))
	if mergeErr != nil {
		return true, mergeErr
	}
	*target = merged
	return true, m.setCollectionValue(ancestor, root)
}

func (m *yamlMutation) rejectReplacedAncestor(node *yaml.Node) error {
	if m.replacedSubtrees[node] {
		return m.p.yamlNodeError(yamlCodeOverlappingPatch, ErrUnsupportedPresentation, node)
	}
	for ancestor := m.p.parents[node]; ancestor != nil; ancestor = m.p.parents[ancestor] {
		if _, replaced := m.collectionValues[ancestor]; replaced {
			return m.p.yamlNodeError(yamlCodeOverlappingPatch, ErrUnsupportedPresentation, ancestor, node)
		}
		if m.replacedSubtrees[ancestor] {
			return m.p.yamlNodeError(yamlCodeOverlappingPatch, ErrUnsupportedPresentation, ancestor, node)
		}
	}
	return nil
}

func (m *yamlMutation) supersedeDescendantState(collection *yaml.Node) error {
	if err := m.ctx.Err(); err != nil {
		return err
	}
	keepPatch := -1
	if index, exists := m.collectionPatch[collection]; exists {
		keepPatch = index
	}
	var remove []int
	for index, patch := range m.patches {
		if err := m.ctx.Err(); err != nil {
			return err
		}
		within, err := m.p.nodeWithinContext(m.ctx, patch.Owner, collection)
		if err != nil {
			return err
		}
		if index != keepPatch && patch.Owner != nil && within {
			remove = append(remove, index)
		}
	}
	if err := sortSliceContext(m.ctx, remove, func(left, right int) bool { return left > right }); err != nil {
		return err
	}

	patches := append([]bytePatch(nil), m.patches...)
	valuePatch := make(map[*yaml.Node]int, len(m.valuePatch))
	for node, index := range m.valuePatch {
		if err := m.ctx.Err(); err != nil {
			return err
		}
		valuePatch[node] = index
	}
	collectionPatch := make(map[*yaml.Node]int, len(m.collectionPatch))
	for node, index := range m.collectionPatch {
		if err := m.ctx.Err(); err != nil {
			return err
		}
		collectionPatch[node] = index
	}
	for _, index := range remove {
		if err := m.ctx.Err(); err != nil {
			return err
		}
		removePatchFromProjectedState(&patches, valuePatch, collectionPatch, index)
	}

	mappingAppends := make(map[*yaml.Node][]*yamlMappingAppend, len(m.mappingAppends))
	for owner := range m.mappingAppends {
		within, err := m.p.nodeWithinContext(m.ctx, owner, collection)
		if err != nil {
			return err
		}
		if !within {
			mappingAppends[owner] = m.mappingAppends[owner]
		}
	}
	sequenceShadows := make(map[*yaml.Node]*yamlSequenceShadow, len(m.sequenceShadows))
	for owner := range m.sequenceShadows {
		within, err := m.p.nodeWithinContext(m.ctx, owner, collection)
		if err != nil {
			return err
		}
		if !within {
			sequenceShadows[owner] = m.sequenceShadows[owner]
		}
	}
	collectionValues := make(map[*yaml.Node]yamlRenderValue, len(m.collectionValues))
	collectionLineage := make(map[*yaml.Node][]yamlSequenceLineage, len(m.collectionLineage))
	for owner, lineage := range m.collectionLineage {
		if err := m.ctx.Err(); err != nil {
			return err
		}
		collectionLineage[owner] = lineage
	}
	for owner := range m.collectionValues {
		within, err := m.p.nodeWithinContext(m.ctx, owner, collection)
		if err != nil {
			return err
		}
		if owner != collection && within {
			delete(collectionLineage, owner)
			continue
		}
		collectionValues[owner] = m.collectionValues[owner]
	}
	replacedSubtrees := make(map[*yaml.Node]bool, len(m.replacedSubtrees))
	for owner := range m.replacedSubtrees {
		within, err := m.p.nodeWithinContext(m.ctx, owner, collection)
		if err != nil {
			return err
		}
		if !within {
			replacedSubtrees[owner] = m.replacedSubtrees[owner]
		}
	}
	insertions := make([]yamlScheduledInsertion, 0, len(m.insertions))
	for _, insertion := range m.insertions {
		within, err := m.p.nodeWithinContext(m.ctx, insertion.owner, collection)
		if err != nil {
			return err
		}
		if insertion.owner == nil || !within {
			insertions = append(insertions, insertion)
		}
	}
	if err := m.ctx.Err(); err != nil {
		return err
	}

	// Publish the fully prepared state without another cancellation point.
	m.patches = patches
	m.valuePatch = valuePatch
	m.collectionPatch = collectionPatch
	m.mappingAppends = mappingAppends
	m.sequenceShadows = sequenceShadows
	m.collectionValues = collectionValues
	m.collectionLineage = collectionLineage
	m.replacedSubtrees = replacedSubtrees
	m.insertions = insertions
	return nil
}

func removePatchFromProjectedState(
	patches *[]bytePatch,
	valuePatch, collectionPatch map[*yaml.Node]int,
	index int,
) {
	if index < 0 || index >= len(*patches) {
		return
	}
	*patches = append((*patches)[:index], (*patches)[index+1:]...)
	for _, indexes := range []map[*yaml.Node]int{valuePatch, collectionPatch} {
		for node, patchIndex := range indexes {
			switch {
			case patchIndex == index:
				delete(indexes, node)
			case patchIndex > index:
				indexes[node] = patchIndex - 1
			}
		}
	}
}

func (m *yamlMutation) setCollectionValue(collection *yaml.Node, value yamlRenderValue) error {
	base, err := yamlRenderFromNodeIgnoringCommentsContext(m.ctx, collection)
	if err != nil {
		return m.p.renderNodeError(err, collection)
	}
	expected := m.expectedNodes[collection]
	if expected == nil {
		return m.p.yamlNodeError("semantic_edit_path", ErrUnsupportedPresentation, collection)
	}
	accumulated, err := yamlRenderFromSemanticContext(m.ctx, expected)
	if err != nil {
		return m.p.preserveTraversalError(err, "semantic_edit_path", ErrUnsupportedPresentation, collection)
	}
	route := m.collectionRoute(collection)
	for _, candidate := range []yamlRenderValue{base, accumulated, value} {
		if err := m.requireUniqueDesired(collection, candidate, route); err != nil {
			return err
		}
	}
	var lineages []yamlSequenceLineage
	value, lineages, err = m.foldCollectionYAMLDesired(collection, base, accumulated, value, route)
	if err != nil {
		return err
	}
	return m.installCollectionValueWithLineage(collection, value, lineages)
}

func (m *yamlMutation) installCollectionValue(collection *yaml.Node, value yamlRenderValue) error {
	var lineages []yamlSequenceLineage
	if isSequenceIdentityRoute(m.collectionRoute(collection)) && value.kind == yamlRenderSequence {
		if stored, present := m.collectionLineage[collection]; present && len(stored) == len(value.sequence) {
			lineages = cloneSequenceLineages(stored)
		}
	}
	return m.installCollectionValueWithLineage(collection, value, lineages)
}

func (m *yamlMutation) installCollectionValueWithLineage(
	collection *yaml.Node,
	value yamlRenderValue,
	lineages []yamlSequenceLineage,
) error {
	expected := m.expectedNodes[collection]
	if expected == nil {
		return m.p.yamlNodeError("semantic_edit_path", ErrUnsupportedPresentation, collection)
	}
	route := m.collectionRoute(collection)
	if err := validateYAMLDesiredUniquenessContext(m.ctx, value, route); err != nil {
		return m.yamlDesiredError(collection, err)
	}
	sequenceIdentityRoute := isSequenceIdentityRoute(route) && value.kind == yamlRenderSequence
	if sequenceIdentityRoute {
		if len(lineages) == 0 {
			base, renderErr := yamlRenderFromNodeIgnoringCommentsContext(m.ctx, collection)
			if renderErr != nil {
				return m.p.renderNodeError(renderErr, collection)
			}
			if base.kind == yamlRenderMapping {
				baseIDs, identityErr := sequenceIdentitiesContext(m.ctx, yamlSequence(base), route)
				if identityErr != nil {
					return m.yamlDesiredError(collection, identityErr)
				}
				valueIDs, identityErr := sequenceIdentitiesContext(m.ctx, value, route)
				if identityErr != nil {
					return m.yamlDesiredError(collection, identityErr)
				}
				lineages, identityErr = alignSequenceLineageContext(m.ctx, baseIDs, valueIDs, false)
				if identityErr != nil {
					return m.yamlDesiredError(collection, identityErr)
				}
			}
			accumulated, renderErr := yamlRenderFromSemanticContext(m.ctx, expected)
			if renderErr != nil {
				return m.p.preserveTraversalError(renderErr, "semantic_edit_path", ErrUnsupportedPresentation, collection)
			}
			if len(lineages) == 0 {
				lineages, renderErr = m.accumulatedSequenceLineage(collection, base, accumulated)
				if renderErr != nil {
					return m.yamlDesiredError(collection, renderErr)
				}
				if len(lineages) != len(value.sequence) {
					return m.yamlDesiredError(collection, errYAMLDesiredConflict)
				}
			}
		}
	}
	if err := m.supersedeDescendantState(collection); err != nil {
		return err
	}
	if sequenceIdentityRoute {
		m.collectionLineage[collection] = cloneSequenceLineages(lineages)
	} else {
		delete(m.collectionLineage, collection)
	}
	if err := m.stageCollectionPatch(collection, value); err != nil {
		return err
	}
	m.collectionValues[collection] = value
	delete(m.sequenceShadows, collection)
	semantic, err := value.semanticContext(m.ctx)
	if err != nil {
		return err
	}
	*expected = *semantic
	return nil
}
