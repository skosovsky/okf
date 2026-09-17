package mutation

import (
	"bytes"
	"context"
	"errors"
	"strings"

	"gopkg.in/yaml.v3"
)

func (m *yamlMutation) syncSequenceExpected(shadow *yamlSequenceShadow) error {
	if err := m.ctx.Err(); err != nil {
		return err
	}
	expected := m.expectedNodes[shadow.collection]
	if expected == nil {
		return m.p.yamlNodeError("semantic_edit_path", ErrUnsupportedPresentation, shadow.collection)
	}
	var nextExpected yamlSemanticNode
	if shadow.bare && !shadow.normalized && len(shadow.items) == 1 {
		nextExpected = *shadow.items[0].semantic
	} else {
		sequence := yamlSemanticNode{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, item := range shadow.items {
			if err := m.ctx.Err(); err != nil {
				return err
			}
			sequence.Content = append(sequence.Content, item.semantic)
		}
		nextExpected = sequence
	}
	if !shadow.replacement {
		if err := m.ctx.Err(); err != nil {
			return err
		}
		*expected = nextExpected
		return nil
	}

	desired := yamlSequence()
	if shadow.bare && !shadow.normalized && len(shadow.items) == 1 {
		desired = shadow.items[0].value
	} else {
		for _, item := range shadow.items {
			if err := m.ctx.Err(); err != nil {
				return err
			}
			desired.sequence = append(desired.sequence, item.value)
		}
	}
	desired, err := cloneYAMLRenderValueContext(m.ctx, desired)
	if err != nil {
		return err
	}
	lineages := make([]yamlSequenceLineage, len(shadow.items))
	for index, item := range shadow.items {
		if err := m.ctx.Err(); err != nil {
			return err
		}
		lineages[index] = item.lineage
	}
	if err := m.ctx.Err(); err != nil {
		return err
	}
	*expected = nextExpected
	m.collectionValues[shadow.collection] = desired
	if desired.kind == yamlRenderSequence {
		m.collectionLineage[shadow.collection] = lineages
	} else {
		delete(m.collectionLineage, shadow.collection)
	}
	return nil
}

func (m *yamlMutation) scheduleInsertion(owner *yaml.Node, at, order int, text []byte) error {
	if err := m.ctx.Err(); err != nil {
		return err
	}
	depth := 0
	for current := owner; current != nil; current = m.p.parents[current] {
		if err := m.ctx.Err(); err != nil {
			return err
		}
		depth++
	}
	path := make([]int, len(m.p.paths[owner]))
	for index, component := range m.p.paths[owner] {
		if err := m.ctx.Err(); err != nil {
			return err
		}
		path[index] = component
	}
	ownedText, err := appendBytesContext(m.ctx, nil, text)
	if err != nil {
		return err
	}
	m.insertions = append(m.insertions, yamlScheduledInsertion{
		owner: owner,
		at:    at,
		depth: depth,
		order: order,
		path:  path,
		text:  ownedText,
	})
	return m.ctx.Err()
}

func compareYAMLPaths(left, right []int) int {
	for i := 0; i < len(left) && i < len(right); i++ {
		if left[i] < right[i] {
			return -1
		}
		if left[i] > right[i] {
			return 1
		}
	}
	switch {
	case len(left) < len(right):
		return -1
	case len(left) > len(right):
		return 1
	default:
		return 0
	}
}

func (m *yamlMutation) flushScheduledInsertions() error {
	defer func() { m.insertions = nil }()
	if err := sortSliceContext(m.ctx, m.insertions, func(left, right yamlScheduledInsertion) bool {
		if left.at != right.at {
			return left.at < right.at
		}
		if left.depth != right.depth {
			return left.depth > right.depth
		}
		if pathOrder := compareYAMLPaths(left.path, right.path); pathOrder != 0 {
			return pathOrder < 0
		}
		return left.order < right.order
	}); err != nil {
		return err
	}
	for start := 0; start < len(m.insertions); {
		if err := m.ctx.Err(); err != nil {
			return err
		}
		end := start + 1
		var text bytes.Buffer
		text.Write(m.insertions[start].text)
		for end < len(m.insertions) && m.insertions[end].at == m.insertions[start].at {
			if err := m.ctx.Err(); err != nil {
				return err
			}
			text.Write(m.insertions[end].text)
			end++
		}
		if err := m.addPatch(bytePatch{
			Start: m.p.yamlStart + m.insertions[start].at,
			End:   m.p.yamlStart + m.insertions[start].at,
			Text:  text.Bytes(),
			Owner: m.insertions[start].owner,
		}); err != nil {
			return err
		}
		start = end
	}
	return nil
}

func (m *yamlMutation) materializeMappingPatches() error {
	if err := m.ctx.Err(); err != nil {
		return err
	}
	if m.materialized {
		return nil
	}
	type orderedMapping struct {
		mapping  *yaml.Node
		appends  []*yamlMappingAppend
		boundary int
		start    int
		depth    int
	}
	ordered := make([]orderedMapping, 0, len(m.mappingAppends))
	for mapping, appends := range m.mappingAppends {
		if err := m.ctx.Err(); err != nil {
			return err
		}
		if len(appends) == 0 {
			continue
		}
		start, err := m.p.resolver.coordinate(mapping.Line, mapping.Column)
		if err != nil {
			return err
		}
		boundary, err := m.p.insertionPoint(mapping)
		if err != nil {
			return err
		}
		depth := 0
		for current := mapping; current != nil; current = m.p.parents[current] {
			if err := m.ctx.Err(); err != nil {
				return err
			}
			depth++
		}
		ordered = append(ordered, orderedMapping{
			mapping: mapping, appends: appends, boundary: boundary, start: start, depth: depth,
		})
	}
	if err := sortSliceContext(m.ctx, ordered, func(left, right orderedMapping) bool {
		if left.boundary != right.boundary {
			return left.boundary < right.boundary
		}
		if left.start != right.start {
			return left.start < right.start
		}
		return left.depth < right.depth
	}); err != nil {
		return err
	}
	for _, pending := range ordered {
		if err := m.ctx.Err(); err != nil {
			return err
		}
		var text strings.Builder
		for _, appendEntry := range pending.appends {
			if err := m.ctx.Err(); err != nil {
				return err
			}
			rendered, err := renderYAMLMappingEntryContext(m.ctx, appendEntry.key, appendEntry.value, max(pending.mapping.Column-1, 0))
			if err != nil {
				return m.p.preserveTraversalError(err, "invalid_render_value", ErrUnsupportedPresentation, pending.mapping)
			}
			if text.Len() != 0 {
				text.Write(m.p.newline)
			}
			text.WriteString(yamlGeneratedNewlines(rendered, m.p.newline))
		}
		text.Write(m.p.newline)
		order := pending.appends[0].order
		for _, appendEntry := range pending.appends[1:] {
			order = min(order, appendEntry.order)
		}
		if err := m.scheduleInsertion(pending.mapping, pending.boundary, order, []byte(text.String())); err != nil {
			return err
		}
	}
	return nil
}

func (m *yamlMutation) materializeSequencePatches() error {
	if err := m.ctx.Err(); err != nil {
		return err
	}
	if m.materialized {
		return nil
	}
	m.materialized = true
	type orderedShadow struct {
		shadow   *yamlSequenceShadow
		start    int
		boundary int
		depth    int
	}
	ordered := make([]orderedShadow, 0, len(m.sequenceShadows))
	for _, shadow := range m.sequenceShadows {
		if err := m.ctx.Err(); err != nil {
			return err
		}
		start, err := m.p.resolver.coordinate(shadow.collection.Line, shadow.collection.Column)
		if err != nil {
			return err
		}
		boundary, err := m.p.structuralBoundary(shadow.collection)
		if err != nil {
			return err
		}
		depth := 0
		for current := shadow.collection; current != nil; current = m.p.parents[current] {
			if err := m.ctx.Err(); err != nil {
				return err
			}
			depth++
		}
		ordered = append(ordered, orderedShadow{shadow: shadow, start: start, boundary: boundary, depth: depth})
	}
	if err := sortSliceContext(m.ctx, ordered, func(left, right orderedShadow) bool {
		if left.boundary != right.boundary {
			return left.boundary < right.boundary
		}
		if left.start != right.start {
			return left.start < right.start
		}
		return left.depth < right.depth
	}); err != nil {
		return err
	}
	for _, pending := range ordered {
		if err := m.ctx.Err(); err != nil {
			return err
		}
		shadow := pending.shadow
		if !shadow.dirty {
			continue
		}
		matchesOriginal, err := shadow.matchesOriginalContext(m.ctx)
		if err != nil {
			return err
		}
		if matchesOriginal {
			continue
		}
		collection := shadow.collection
		if shadow.replacement || shadow.bare || collection.Style&yaml.FlowStyle != 0 {
			if err := m.p.requireWholeCollectionOwnership(collection); err != nil {
				return err
			}
			desired := yamlSequence()
			if shadow.bare && !shadow.normalized && len(shadow.items) == 1 {
				if err := m.requireShadowItemMaterializable(shadow.items[0]); err != nil {
					return err
				}
				desired = shadow.items[0].value
			} else {
				for _, item := range shadow.items {
					if err := m.ctx.Err(); err != nil {
						return err
					}
					if err := m.requireShadowItemMaterializable(item); err != nil {
						return err
					}
					desired.sequence = append(desired.sequence, item.value)
				}
			}
			if err := m.stageCollectionPatch(collection, desired); err != nil {
				return err
			}
			m.collectionValues[collection] = desired
			continue
		}
		if len(shadow.items) == 0 {
			if err := m.p.requireWholeCollectionOwnership(collection); err != nil {
				return err
			}
			if err := m.stageCollectionPatch(collection, yamlSequence()); err != nil {
				return err
			}
			continue
		}
		finalBySource := make(map[*yaml.Node]*yamlSequenceShadowItem, len(shadow.items))
		var appendedItems []*yamlSequenceShadowItem
		for _, item := range shadow.items {
			if err := m.ctx.Err(); err != nil {
				return err
			}
			if item.source == nil {
				if err := m.requireShadowItemMaterializable(item); err != nil {
					return err
				}
				appendedItems = append(appendedItems, item)
			} else {
				finalBySource[item.source] = item
			}
		}
		for _, original := range shadow.original {
			if err := m.ctx.Err(); err != nil {
				return err
			}
			current := finalBySource[original.source]
			if current == nil {
				span, err := m.p.sequenceItemSpan(collection, original.source)
				if err != nil {
					return err
				}
				if err := m.addPatch(bytePatch{
					Start: m.p.yamlStart + span.Start,
					End:   m.p.yamlStart + span.End,
					Owner: collection,
				}); err != nil {
					return err
				}
				continue
			}
			equal, err := equalSemanticYAMLContext(m.ctx, current.originalSemantic, current.semantic)
			if err != nil {
				return err
			}
			if current.dirty && !equal {
				if err := m.p.requireWholeCollectionOwnership(current.source); err != nil {
					return err
				}
				if err := m.stageValuePatch(current.source, current.value); err != nil {
					return err
				}
			}
		}
		if len(appendedItems) != 0 {
			at, err := m.p.insertionPoint(collection)
			if err != nil {
				return err
			}
			appended := make([]yamlRenderValue, 0, len(appendedItems))
			order := appendedItems[0].order
			for _, item := range appendedItems {
				appended = append(appended, item.value)
				order = min(order, item.order)
			}
			rendered, err := renderYAMLBlockContext(m.ctx, yamlSequence(appended...), max(collection.Column-1, 0))
			if err != nil {
				return m.p.preserveTraversalError(err, "invalid_render_value", ErrUnsupportedPresentation, collection)
			}
			rendered = yamlGeneratedNewlines(rendered, m.p.newline)
			if err := m.scheduleInsertion(collection, at, order, append([]byte(rendered), m.p.newline...)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (shadow *yamlSequenceShadow) matchesOriginalContext(ctx context.Context) (bool, error) {
	if shadow == nil || len(shadow.items) != len(shadow.original) || shadow.bare && shadow.normalized {
		return false, nil
	}
	for i := range shadow.items {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		equal, err := equalSemanticYAMLContext(ctx, shadow.items[i].semantic, shadow.original[i].originalSemantic)
		if err != nil {
			return false, err
		}
		if shadow.items[i].source != shadow.original[i].source || !equal {
			return false, nil
		}
	}
	return true, ctx.Err()
}

func (m *yamlMutation) stageValuePatch(node *yaml.Node, desired yamlRenderValue) error {
	patch, err := m.p.structuralValuePatch(node, desired)
	if err != nil {
		return err
	}
	patch.Owner = node
	if index, exists := m.valuePatch[node]; exists {
		return m.replacePatchAt(index, patch)
	}
	index, err := m.addPatchTracked(patch)
	if err != nil {
		return err
	}
	m.valuePatch[node] = index
	return nil
}

func (m *yamlMutation) stageCollectionPatch(collection *yaml.Node, desired yamlRenderValue) error {
	patch, err := m.p.structuralValuePatch(collection, desired)
	if err != nil {
		return err
	}
	patch.Owner = collection
	if index, exists := m.collectionPatch[collection]; exists {
		return m.replacePatchAt(index, patch)
	}
	index, err := m.addPatchTracked(patch)
	if err != nil {
		return err
	}
	m.collectionPatch[collection] = index
	return nil
}

func (m *yamlMutation) ensureSequenceItem(collection *yaml.Node, selector yamlSequenceSelector, desired yamlRenderValue) (err error) {
	if err := m.requireActive(); err != nil {
		return err
	}
	defer m.rememberFailure(&err)
	if err := m.p.requireMutationPathProvenance(collection); err != nil {
		return err
	}
	if err := validateYAMLRenderValueContext(m.ctx, desired); err != nil {
		return m.p.preserveTraversalError(err, "invalid_render_value", ErrUnsupportedPresentation, collection)
	}
	if err := m.p.validateYAMLSequenceSelector(collection, selector); err != nil {
		return m.p.preserveTraversalError(err, yamlCodeUnsupportedSelector, ErrUnsupportedPresentation, collection)
	}
	if err := validateSelectorDesiredContext(m.ctx, selector, desired); err != nil {
		return m.p.preserveTraversalError(err, yamlCodeSelectorMismatch, ErrUnsupportedPresentation, collection)
	}
	if handled, err := m.mutateReplacementDescendantSequence(collection, selector, &desired); handled {
		return err
	}
	if err := m.rejectReplacedAncestor(collection); err != nil {
		return err
	}
	shadow, err := m.sequenceShadow(collection)
	if err != nil {
		return err
	}
	index, found, err := m.selectShadowItem(shadow, selector)
	if err != nil {
		return err
	}
	if found {
		item := shadow.items[index]
		if err := m.requireShadowItemMaterializable(item); err != nil {
			return err
		}
		equal := false
		if item.renderable {
			var equalErr error
			equal, equalErr = equalYAMLRenderCanonicalAtKeyContext(m.ctx, item.value, desired, "")
			if equalErr != nil {
				return equalErr
			}
		}
		if equal {
			if item.source != nil {
				if err := m.p.requireWholeCollectionOwnership(item.source); err != nil {
					return err
				}
			}
			return nil
		}
		if item.source == nil || item.originalSemantic == nil {
			return m.p.yamlNodeError(yamlCodeOverlappingPatch, ErrUnsupportedPresentation, collection)
		}
		if err := m.p.requireWholeCollectionOwnership(item.source); err != nil {
			return err
		}
		if collection.Style&yaml.FlowStyle != 0 {
			if err := m.p.requireWholeCollectionOwnership(collection); err != nil {
				return err
			}
		}
		base, baseErr := yamlRenderFromSemanticContext(m.ctx, item.originalSemantic)
		accumulated, accumulatedErr := yamlRenderFromSemanticContext(m.ctx, item.semantic)
		if baseErr != nil {
			return m.p.preserveTraversalError(baseErr, "semantic_edit_path", ErrUnsupportedPresentation, item.source)
		}
		if accumulatedErr != nil {
			return m.p.preserveTraversalError(accumulatedErr, "semantic_edit_path", ErrUnsupportedPresentation, item.source)
		}
		desired, err = m.foldYAMLDesired(item.source, base, accumulated, desired, "")
		if err != nil {
			return err
		}
		stateEqual, equalErr := equalYAMLRenderStateContext(m.ctx, accumulated, desired)
		if equalErr != nil {
			return equalErr
		}
		if stateEqual {
			return nil
		}
		if item.source != collection {
			if err := m.supersedeDescendantState(item.source); err != nil {
				return err
			}
		}
		return m.syncReplacementShadowItem(shadow, item, desired)
	} else {
		if collection.Style&yaml.FlowStyle != 0 {
			if err := m.p.requireWholeCollectionOwnership(collection); err != nil {
				return err
			}
		}
		semantic, semanticErr := desired.semanticContext(m.ctx)
		if semanticErr != nil {
			return semanticErr
		}
		identity, identityErr := sequenceIdentityContext(m.ctx, desired, shadow.route)
		if identityErr != nil {
			return identityErr
		}
		item := &yamlSequenceShadowItem{
			value:    desired,
			identity: identity,
			lineage: yamlSequenceLineage{
				baseIndex: -1,
				added:     identity,
				appended:  true,
			},
			semantic:   semantic,
			renderable: true,
			dirty:      true,
			order:      m.issueOrder(),
		}
		shadow.items = append(shadow.items, item)
		if err := m.validateShadowIdentities(shadow); err != nil {
			shadow.items = shadow.items[:len(shadow.items)-1]
			return err
		}
		if shadow.bare {
			shadow.normalized = true
		}
	}
	shadow.dirty = true
	if err := m.syncSequenceExpected(shadow); err != nil {
		return err
	}
	return nil
}

func (p *presentation) renderNodeError(err error, node *yaml.Node) error {
	fallbackCode := yamlCodeUnsupportedCollection
	fallbackCause := ErrUnsupportedPresentation
	if errors.Is(err, ErrAmbiguousPresentation) {
		fallbackCause = ErrAmbiguousPresentation
		message := err.Error()
		switch {
		case strings.Contains(message, "duplicate mapping key"):
			fallbackCode = yamlCodeDuplicateTouchedKey
		case strings.Contains(message, "alias"), strings.Contains(message, "merge"):
			fallbackCode = yamlCodeAliasProvenance
		default:
			fallbackCode = yamlCodeAmbiguousSelector
		}
	}
	return p.preserveTraversalError(err, fallbackCode, fallbackCause, node)
}

func (m *yamlMutation) removeSequenceItem(collection *yaml.Node, selector yamlSequenceSelector) (err error) {
	if err := m.requireActive(); err != nil {
		return err
	}
	defer m.rememberFailure(&err)
	if err := m.p.requireMutationPathProvenance(collection); err != nil {
		return err
	}
	if err := m.p.validateYAMLSequenceSelector(collection, selector); err != nil {
		return m.p.preserveTraversalError(err, yamlCodeUnsupportedSelector, ErrUnsupportedPresentation, collection)
	}
	if handled, err := m.mutateReplacementDescendantSequence(collection, selector, nil); handled {
		return err
	}
	if err := m.rejectReplacedAncestor(collection); err != nil {
		return err
	}
	shadow, err := m.sequenceShadow(collection)
	if err != nil {
		return err
	}
	index, found, err := m.selectShadowItem(shadow, selector)
	if err != nil {
		return err
	}
	if !found {
		if shadow.replacement {
			baseItems, baseErr := m.p.sequenceSelectorItems(collection)
			if baseErr != nil {
				return baseErr
			}
			baseMatches := 0
			for _, baseItem := range baseItems {
				matched, matchErr := m.p.matchesSequenceSelector(baseItem, selector)
				if matchErr != nil {
					return matchErr
				}
				if matched {
					baseMatches++
				}
			}
			switch {
			case baseMatches == 1:
				// The replacement branch already removed the immutable item.
				return nil
			case baseMatches > 1:
				return m.p.yamlNodeError(yamlCodeAmbiguousSelector, ErrAmbiguousPresentation, collection)
			}
		}
		return m.p.yamlNodeError(yamlCodeMissingSequenceItem, ErrUnsupportedPresentation, collection)
	}
	item := shadow.items[index]
	if err := m.requireShadowItemMaterializable(item); err != nil {
		return err
	}
	if collection.Style&yaml.FlowStyle != 0 {
		if err := m.p.requireWholeCollectionOwnership(collection); err != nil {
			return err
		}
	}
	if item.source != nil {
		if err := m.p.requireWholeCollectionOwnership(item.source); err != nil {
			return err
		}
		equal, equalErr := equalSemanticYAMLContext(m.ctx, item.originalSemantic, item.semantic)
		if equalErr != nil {
			return equalErr
		}
		if item.originalSemantic != nil && !equal {
			return m.p.yamlNodeError(yamlCodeOverlappingPatch, ErrUnsupportedPresentation, item.source, collection)
		}
		if item.source != collection {
			if err := m.supersedeDescendantState(item.source); err != nil {
				return err
			}
		}
		m.replacedSubtrees[item.source] = true
	}
	item.active = false
	shadow.items = append(shadow.items[:index], shadow.items[index+1:]...)
	if err := m.validateShadowIdentities(shadow); err != nil {
		return err
	}
	shadow.dirty = true
	if shadow.bare {
		shadow.normalized = true
	}
	return m.syncSequenceExpected(shadow)
}

func (p *presentation) sequenceItemSpan(sequence, item *yaml.Node) (SourceSpan, error) {
	if sequence.Style&yaml.FlowStyle != 0 || item.Line < 1 || item.Line > len(p.resolver.lines) {
		return SourceSpan{}, p.yamlNodeError("touched_flow", ErrUnsupportedPresentation, sequence)
	}
	start := p.resolver.lines[item.Line-1]
	end, err := p.structuralBoundary(item)
	if err != nil {
		return SourceSpan{}, err
	}
	span := SourceSpan{Start: start, End: end}
	if err := p.requireOwnedStructuralTrivia(span); err != nil {
		return SourceSpan{}, err
	}
	return span, nil
}
