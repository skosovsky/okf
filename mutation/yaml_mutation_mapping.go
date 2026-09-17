package mutation

import (
	"bytes"

	"gopkg.in/yaml.v3"
)

func (m *yamlMutation) replaceMappingValue(mapping *yaml.Node, key string, value yamlRenderValue) (err error) {
	if err := m.requireActive(); err != nil {
		return err
	}
	defer m.rememberFailure(&err)
	if err := m.p.requireMutationPathProvenance(mapping); err != nil {
		return err
	}
	if err := validateYAMLRenderValueContext(m.ctx, value); err != nil {
		return m.p.preserveTraversalError(err, "invalid_render_value", ErrUnsupportedPresentation, mapping)
	}
	if handled, err := m.mutateReplacementDescendantMapping(mapping, key, value, "replace"); handled {
		return err
	}
	if err := m.rejectReplacedAncestor(mapping); err != nil {
		return err
	}
	if err := m.validateOrdinaryIdentityMutation(mapping, key, value, "replace"); err != nil {
		return err
	}
	if handled, err := m.mutateReplacementMapping(mapping, key, value, "replace"); handled {
		return err
	}
	if mapping.Style&yaml.FlowStyle != 0 {
		if err := m.p.requireWholeCollectionOwnership(mapping); err != nil {
			return err
		}
		current, renderErr := yamlRenderFromNodeContext(m.ctx, mapping)
		if renderErr != nil {
			return m.p.renderNodeError(renderErr, mapping)
		}
		if current.kind != yamlRenderMapping {
			return m.p.yamlNodeError(yamlCodeUnsupportedCollection, ErrUnsupportedPresentation, mapping)
		}
		if _, present := yamlRenderMappingValue(current, key); !present {
			return m.p.yamlNodeError(yamlCodeMissingMappingEntry, ErrUnsupportedPresentation, mapping)
		}
		if _, err := setRenderMappingKeyContext(m.ctx, &current, key, value, true); err != nil {
			return err
		}
		return m.setCollectionValue(mapping, current)
	}
	expectedMapping := m.expectedNodes[mapping]
	index, count, err := semanticMappingEntryIndexContext(m.ctx, expectedMapping, key)
	if err != nil {
		return err
	}
	if count > 1 {
		return m.p.yamlNodeError(yamlCodeDuplicateTouchedKey, ErrAmbiguousPresentation, mappingKeyNodes(mapping, key)...)
	}
	if count == 0 {
		return m.p.yamlNodeError(yamlCodeMissingMappingEntry, ErrUnsupportedPresentation, mapping)
	}
	if appendEntry, _ := m.mappingAppend(mapping, key); appendEntry != nil {
		if err := m.validateMappingEntryUniqueness(mapping, key, value, "replace"); err != nil {
			return err
		}
		desired, semanticErr := value.semanticContext(m.ctx)
		if semanticErr != nil {
			return semanticErr
		}
		equal, equalErr := equalSemanticYAMLContext(m.ctx, appendEntry.valueNode, desired)
		if equalErr != nil {
			return equalErr
		}
		if equal {
			return nil
		}
		appendEntry.value = value
		*appendEntry.valueNode = *desired
		return nil
	}
	match, ok, err := m.p.structuralMappingEntry(mapping, key)
	if err != nil {
		return err
	}
	if !ok {
		return m.p.yamlNodeError(yamlCodeMissingMappingEntry, ErrUnsupportedPresentation, mapping)
	}
	if err := m.p.requireStructuralEntryOwnership(mapping, match); err != nil {
		return err
	}
	expectedValue := m.expectedNodes[match.value]
	if expectedMapping == nil || expectedValue == nil {
		return m.p.yamlNodeError("semantic_edit_path", ErrUnsupportedPresentation, mapping)
	}
	if semanticChildIndex(expectedMapping, expectedValue) != index+1 {
		return m.p.yamlNodeError(yamlCodeOverlappingPatch, ErrUnsupportedPresentation, match.key, match.value)
	}
	if err := m.validateMappingEntryUniqueness(mapping, key, value, "replace"); err != nil {
		return err
	}
	desired, semanticErr := value.semanticContext(m.ctx)
	if semanticErr != nil {
		return semanticErr
	}
	equal, equalErr := equalSemanticYAMLContext(m.ctx, expectedValue, desired)
	if equalErr != nil {
		return equalErr
	}
	if equal {
		return nil
	}
	if err := m.stageValuePatch(match.value, value); err != nil {
		return err
	}
	*expectedValue = *desired
	return nil
}

func (m *yamlMutation) insertMappingValue(mapping *yaml.Node, key string, value yamlRenderValue) (err error) {
	if err := m.requireActive(); err != nil {
		return err
	}
	defer m.rememberFailure(&err)
	if err := m.p.requireMutationPathProvenance(mapping); err != nil {
		return err
	}
	if err := validateYAMLRenderValueContext(m.ctx, value); err != nil {
		return m.p.preserveTraversalError(err, "invalid_render_value", ErrUnsupportedPresentation, mapping)
	}
	if key == "" {
		return m.p.yamlNodeError("invalid_render_value", ErrUnsupportedPresentation, mapping)
	}
	if handled, err := m.mutateReplacementDescendantMapping(mapping, key, value, "insert"); handled {
		return err
	}
	if err := m.rejectReplacedAncestor(mapping); err != nil {
		return err
	}
	if err := m.validateOrdinaryIdentityMutation(mapping, key, value, "insert"); err != nil {
		return err
	}
	if handled, err := m.mutateReplacementMapping(mapping, key, value, "insert"); handled {
		return err
	}
	if _, _, err := m.p.structuralMappingEntry(mapping, key); err != nil {
		return err
	}
	if mapping.Style&yaml.FlowStyle != 0 {
		if err := m.p.requireWholeCollectionOwnership(mapping); err != nil {
			return err
		}
		current, renderErr := yamlRenderFromNodeContext(m.ctx, mapping)
		if renderErr != nil {
			return m.p.renderNodeError(renderErr, mapping)
		}
		if _, present := yamlRenderMappingValue(current, key); present {
			return m.p.yamlNodeError(yamlCodeDuplicateTouchedKey, ErrAmbiguousPresentation, mappingKeyNodes(mapping, key)...)
		}
		if _, err := setRenderMappingKeyContext(m.ctx, &current, key, value, true); err != nil {
			return err
		}
		return m.setCollectionValue(mapping, current)
	}
	if err := m.p.requireInsertionProvenanceContext(m.ctx, mapping); err != nil {
		return err
	}
	expectedMapping := m.expectedNodes[mapping]
	if expectedMapping == nil || expectedMapping.Kind != yaml.MappingNode {
		return m.p.yamlNodeError("semantic_edit_path", ErrUnsupportedPresentation, mapping)
	}
	if _, count, err := semanticMappingEntryIndexContext(m.ctx, expectedMapping, key); err != nil {
		return err
	} else if count != 0 {
		return m.p.yamlNodeError(yamlCodeDuplicateTouchedKey, ErrAmbiguousPresentation, mapping)
	}
	if err := m.validateMappingEntryUniqueness(mapping, key, value, "insert"); err != nil {
		return err
	}
	keyNode := &yamlSemanticNode{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	valueNode, semanticErr := value.semanticContext(m.ctx)
	if semanticErr != nil {
		return semanticErr
	}
	expectedMapping.Content = append(expectedMapping.Content, keyNode, valueNode)
	m.mappingAppends[mapping] = append(m.mappingAppends[mapping], &yamlMappingAppend{
		key: key, value: value, keyNode: keyNode, valueNode: valueNode, order: m.issueOrder(),
	})
	return nil
}

func (m *yamlMutation) deleteMappingValue(mapping *yaml.Node, key string) (err error) {
	if err := m.requireActive(); err != nil {
		return err
	}
	defer m.rememberFailure(&err)
	if err := m.p.requireMutationPathProvenance(mapping); err != nil {
		return err
	}
	if handled, err := m.mutateReplacementDescendantMapping(mapping, key, yamlRenderValue{}, "delete"); handled {
		return err
	}
	if err := m.rejectReplacedAncestor(mapping); err != nil {
		return err
	}
	if err := m.validateOrdinaryIdentityMutation(mapping, key, yamlRenderValue{}, "delete"); err != nil {
		return err
	}
	if handled, err := m.mutateReplacementMapping(mapping, key, yamlRenderValue{}, "delete"); handled {
		return err
	}
	if mapping.Style&yaml.FlowStyle != 0 {
		if err := m.p.requireWholeCollectionOwnership(mapping); err != nil {
			return err
		}
		current, renderErr := yamlRenderFromNodeContext(m.ctx, mapping)
		if renderErr != nil {
			return m.p.renderNodeError(renderErr, mapping)
		}
		if _, present := yamlRenderMappingValue(current, key); !present {
			return m.p.yamlNodeError(yamlCodeMissingMappingEntry, ErrUnsupportedPresentation, mapping)
		}
		if _, err := setRenderMappingKeyContext(m.ctx, &current, key, yamlRenderValue{}, false); err != nil {
			return err
		}
		return m.setCollectionValue(mapping, current)
	}
	expectedMapping := m.expectedNodes[mapping]
	expectedIndex, count, err := semanticMappingEntryIndexContext(m.ctx, expectedMapping, key)
	if err != nil {
		return err
	}
	if count > 1 {
		return m.p.yamlNodeError(yamlCodeDuplicateTouchedKey, ErrAmbiguousPresentation, mappingKeyNodes(mapping, key)...)
	}
	if count == 0 {
		return m.p.yamlNodeError(yamlCodeMissingMappingEntry, ErrUnsupportedPresentation, mapping)
	}
	if _, appendIndex := m.mappingAppend(mapping, key); appendIndex >= 0 {
		if err := m.validateMappingEntryUniqueness(mapping, key, yamlRenderValue{}, "delete"); err != nil {
			return err
		}
		expectedMapping.Content = append(expectedMapping.Content[:expectedIndex], expectedMapping.Content[expectedIndex+2:]...)
		m.mappingAppends[mapping] = append(m.mappingAppends[mapping][:appendIndex], m.mappingAppends[mapping][appendIndex+1:]...)
		return nil
	}
	match, ok, err := m.p.structuralMappingEntry(mapping, key)
	if err != nil {
		return err
	}
	if !ok {
		return m.p.yamlNodeError(yamlCodeMissingMappingEntry, ErrUnsupportedPresentation, mapping)
	}
	if err := m.p.requireStructuralEntryOwnership(mapping, match); err != nil {
		return err
	}
	if patchIndex, replaced := m.valuePatch[match.value]; replaced {
		m.removePatchAt(patchIndex)
	}
	expectedKey := m.expectedNodes[match.key]
	if expectedMapping == nil || expectedKey == nil {
		return m.p.yamlNodeError("semantic_edit_path", ErrUnsupportedPresentation, mapping)
	}
	if semanticChildIndex(expectedMapping, expectedKey) != expectedIndex || expectedIndex+1 >= len(expectedMapping.Content) {
		return m.p.yamlNodeError(yamlCodeOverlappingPatch, ErrUnsupportedPresentation, match.key, match.value)
	}
	if err := m.validateMappingEntryUniqueness(mapping, key, yamlRenderValue{}, "delete"); err != nil {
		return err
	}
	var patch bytePatch
	if len(mapping.Content) == 2 {
		patch, err = m.p.structuralValuePatch(mapping, yamlMapping())
	} else {
		patch, err = m.p.mappingEntryDeletionPatch(mapping, match)
	}
	if err != nil {
		return err
	}
	patch.Owner = mapping
	if err := m.addPatch(patch); err != nil {
		return err
	}
	expectedMapping.Content = append(expectedMapping.Content[:expectedIndex], expectedMapping.Content[expectedIndex+2:]...)
	return nil
}

func semanticChildIndex(parent, child *yamlSemanticNode) int {
	if parent == nil || child == nil {
		return -1
	}
	for i, candidate := range parent.Content {
		if candidate == child {
			return i
		}
	}
	return -1
}

func (m *yamlMutation) replaceCollectionValue(collection *yaml.Node, value yamlRenderValue) (err error) {
	if err := m.requireActive(); err != nil {
		return err
	}
	defer m.rememberFailure(&err)
	if err := m.p.requireMutationPathProvenance(collection); err != nil {
		return err
	}
	if value.kind != yamlRenderMapping && value.kind != yamlRenderSequence {
		return m.p.yamlNodeError(yamlCodeUnsupportedCollection, ErrUnsupportedPresentation, collection)
	}
	if err := validateYAMLRenderValueContext(m.ctx, value); err != nil {
		return m.p.preserveTraversalError(err, "invalid_render_value", ErrUnsupportedPresentation, collection)
	}
	if err := m.p.requireWholeCollectionOwnership(collection); err != nil {
		return err
	}
	base, renderErr := yamlRenderFromNodeIgnoringCommentsContext(m.ctx, collection)
	if renderErr != nil {
		return m.p.renderNodeError(renderErr, collection)
	}
	route := m.collectionRoute(collection)
	if err := m.requireUniqueDesired(collection, base, route); err != nil {
		return err
	}
	if err := m.requireUniqueDesired(collection, value, route); err != nil {
		return err
	}
	if handled, err := m.replaceReplacementDescendantCollection(collection, value); handled {
		return err
	}
	if err := m.rejectReplacedAncestor(collection); err != nil {
		return err
	}
	expected := m.expectedNodes[collection]
	if expected == nil {
		return m.p.yamlNodeError("semantic_edit_path", ErrUnsupportedPresentation, collection)
	}
	desired, semanticErr := value.semanticContext(m.ctx)
	if semanticErr != nil {
		return semanticErr
	}
	equal, equalErr := equalSemanticYAMLContext(m.ctx, expected, desired)
	if equalErr != nil {
		return equalErr
	}
	if equal {
		return nil
	}
	delete(m.mappingAppends, collection)
	return m.setCollectionValue(collection, value)
}

func (p *presentation) requireStructuralEntryOwnership(mapping *yaml.Node, match yamlMappingEntryMatch) error {
	if mapping.Style&yaml.FlowStyle != 0 {
		return p.yamlNodeError("touched_flow", ErrUnsupportedPresentation, mapping)
	}
	if err := p.requireInsertionProvenanceContext(p.ctx, mapping); err != nil {
		return err
	}
	if err := p.requireNoComments(match.key); err != nil {
		return err
	}
	if match.value == nil {
		return p.yamlNodeError("unowned_touched_node", ErrUnsupportedPresentation, match.key)
	}
	if match.value.Kind == yaml.AliasNode || match.value.Alias != nil {
		return p.yamlNodeError(yamlCodeAliasProvenance, ErrAmbiguousPresentation, match.value)
	}
	switch match.value.Kind {
	case yaml.MappingNode, yaml.SequenceNode:
		return p.requireWholeCollectionOwnership(match.value)
	case yaml.ScalarNode:
		if err := p.requireNoComments(match.value); err != nil {
			return err
		}
		_, err := p.resolver.typedScalar(match.value)
		return err
	default:
		return p.yamlNodeError(yamlCodeUnsupportedCollection, ErrUnsupportedPresentation, match.value)
	}
}

func (p *presentation) requireWholeCollectionOwnership(collection *yaml.Node) error {
	if collection != nil && (collection.Kind == yaml.AliasNode || collection.Alias != nil) {
		return p.yamlNodeError(yamlCodeAliasProvenance, ErrAmbiguousPresentation, collection)
	}
	if collection == nil || (collection.Kind != yaml.MappingNode && collection.Kind != yaml.SequenceNode) {
		return p.yamlNodeError(yamlCodeUnsupportedCollection, ErrUnsupportedPresentation, collection)
	}
	if err := p.requireNoAmbiguousCollectionProvenance(collection); err != nil {
		return err
	}
	var visit func(*yaml.Node) error
	visit = func(n *yaml.Node) error {
		if n == nil {
			return p.yamlNodeError(yamlCodeUnsupportedCollection, ErrUnsupportedPresentation, collection)
		}
		if n.Kind == yaml.AliasNode || n.Alias != nil {
			return p.yamlNodeError(yamlCodeAliasProvenance, ErrAmbiguousPresentation, n)
		}
		if n.Anchor != "" {
			return p.yamlNodeError(yamlCodeTouchedAnchor, ErrUnsupportedPresentation, n)
		}
		if err := p.requireNoSyntaxProvenance(n); err != nil {
			return err
		}
		if err := p.requireNoComments(n); err != nil {
			return err
		}
		if n.Kind == yaml.MappingNode {
			if len(n.Content)%2 != 0 {
				return p.yamlNodeError(yamlCodeUnsupportedCollection, ErrUnsupportedPresentation, n)
			}
			seen := make(map[string]*yaml.Node, len(n.Content)/2)
			for i := 0; i+1 < len(n.Content); i += 2 {
				key := n.Content[i]
				if key.Tag == "!!merge" {
					return p.yamlNodeError(yamlCodeAliasProvenance, ErrAmbiguousPresentation, key, n.Content[i+1])
				}
				if err := p.requireScalarKey(key); err != nil {
					return err
				}
				if previous := seen[key.Value]; previous != nil {
					return p.yamlNodeError(yamlCodeDuplicateTouchedKey, ErrAmbiguousPresentation, previous, key)
				}
				seen[key.Value] = key
			}
		}
		if n.Kind == yaml.ScalarNode {
			if !yamlSupportedScalarTag(n.Tag) || n.Style&(yaml.LiteralStyle|yaml.FoldedStyle|yaml.TaggedStyle) != 0 {
				return p.yamlNodeError("unsupported_scalar", ErrUnsupportedPresentation, n)
			}
			if !p.nodeHasFlowAncestorWithin(n, collection) {
				if _, err := p.resolver.typedScalar(n); err != nil {
					return err
				}
			}
		}
		for _, child := range n.Content {
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(collection); err != nil {
		return err
	}
	// Whole-value ownership stops at the collection boundary. A flow parent is
	// allowed because no token inside that parent is edited separately.
	return nil
}

func (p *presentation) requireNoAmbiguousCollectionProvenance(collection *yaml.Node) error {
	var visit func(*yaml.Node) error
	visit = func(n *yaml.Node) error {
		if n == nil {
			return nil
		}
		if n.Kind == yaml.AliasNode || n.Alias != nil {
			return p.yamlNodeError(yamlCodeAliasProvenance, ErrAmbiguousPresentation, n)
		}
		if n.Kind == yaml.MappingNode {
			seen := make(map[string]*yaml.Node, len(n.Content)/2)
			for i := 0; i+1 < len(n.Content); i += 2 {
				key := n.Content[i]
				if key != nil && key.Tag == "!!merge" {
					return p.yamlNodeError(yamlCodeAliasProvenance, ErrAmbiguousPresentation, key, n.Content[i+1])
				}
				if key != nil && key.Kind == yaml.ScalarNode && key.Tag == "!!str" {
					if previous := seen[key.Value]; previous != nil {
						return p.yamlNodeError(yamlCodeDuplicateTouchedKey, ErrAmbiguousPresentation, previous, key)
					}
					seen[key.Value] = key
				}
			}
		}
		for _, child := range n.Content {
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	return visit(collection)
}

func (p *presentation) nodeHasFlowAncestorWithin(n, boundary *yaml.Node) bool {
	for current := n; current != nil; current = p.parents[current] {
		if current.Style&yaml.FlowStyle != 0 {
			return true
		}
		if current == boundary {
			return false
		}
	}
	return false
}

func (p *presentation) requireNoComments(n *yaml.Node) error {
	if n == nil {
		return nil
	}
	if n.HeadComment != "" || n.LineComment != "" || n.FootComment != "" {
		return p.yamlNodeError(yamlCodeUnownedComment, ErrUnsupportedPresentation, n)
	}
	return nil
}

func (p *presentation) structuralValuePatch(current *yaml.Node, desired yamlRenderValue) (bytePatch, error) {
	if current == nil {
		return bytePatch{}, p.yamlNodeError("unowned_touched_node", ErrUnsupportedPresentation, current)
	}
	if current.Kind == yaml.AliasNode || current.Alias != nil {
		return bytePatch{}, p.yamlNodeError(yamlCodeAliasProvenance, ErrAmbiguousPresentation, current)
	}
	var span SourceSpan
	var err error
	switch current.Kind {
	case yaml.ScalarNode:
		if err := p.requireNoComments(current); err != nil {
			return bytePatch{}, err
		}
		resolved, resolveErr := p.resolver.typedScalar(current)
		if resolveErr != nil {
			return bytePatch{}, resolveErr
		}
		span = resolved.Span
	case yaml.MappingNode, yaml.SequenceNode:
		span, err = p.collectionValueSpan(current)
		if err != nil {
			return bytePatch{}, err
		}
	default:
		return bytePatch{}, p.yamlNodeError(yamlCodeUnsupportedCollection, ErrUnsupportedPresentation, current)
	}
	text, err := renderYAMLReplacementContext(p.ctx, current, desired)
	if err != nil {
		return bytePatch{}, p.preserveTraversalError(err, "invalid_render_value", ErrUnsupportedPresentation, current)
	}
	text = yamlGeneratedNewlines(text, p.newline)
	if current.Kind != yaml.ScalarNode && current.Style&yaml.FlowStyle == 0 &&
		span.End > span.Start && span.End <= len(p.yaml) && p.yaml[span.End-1] == '\n' {
		text += string(p.newline)
	}
	return bytePatch{
		Start: p.yamlStart + span.Start,
		End:   p.yamlStart + span.End,
		Text:  []byte(text),
	}, nil
}

func (p *presentation) collectionValueSpan(collection *yaml.Node) (SourceSpan, error) {
	if collection.Style&yaml.FlowStyle != 0 {
		return p.resolver.flowCollection(collection)
	}
	at, err := p.resolver.coordinate(collection.Line, collection.Column)
	if err != nil {
		return SourceSpan{}, err
	}
	end, err := p.structuralBoundary(collection)
	if err != nil {
		return SourceSpan{}, err
	}
	span := SourceSpan{Start: at, End: end}
	if !span.valid(len(p.yaml)) || span.Start >= span.End {
		return SourceSpan{}, p.yamlLocalError("invalid_collection_span", ErrUnsupportedPresentation, span)
	}
	if err := p.requireOwnedStructuralTrivia(span); err != nil {
		return SourceSpan{}, err
	}
	return span, nil
}

func (p *presentation) mappingEntrySpan(mapping *yaml.Node, match yamlMappingEntryMatch) (SourceSpan, error) {
	if mapping.Style&yaml.FlowStyle != 0 {
		return SourceSpan{}, p.yamlNodeError("touched_flow", ErrUnsupportedPresentation, mapping)
	}
	if match.key.Line < 1 || match.key.Line > len(p.resolver.lines) {
		return SourceSpan{}, p.yamlNodeError("invalid_coordinate", ErrUnsupportedPresentation, match.key)
	}
	start := p.resolver.lines[match.key.Line-1]
	end, err := p.structuralBoundary(match.value)
	if err != nil {
		return SourceSpan{}, err
	}
	span := SourceSpan{Start: start, End: end}
	if !span.valid(len(p.yaml)) || span.Start >= span.End {
		return SourceSpan{}, p.yamlLocalError("invalid_mapping_entry_span", ErrUnsupportedPresentation, span)
	}
	if err := p.requireOwnedStructuralTrivia(span); err != nil {
		return SourceSpan{}, err
	}
	return span, nil
}

func (p *presentation) mappingEntryDeletionPatch(mapping *yaml.Node, match yamlMappingEntryMatch) (bytePatch, error) {
	if mapping == nil || len(mapping.Content) <= 2 {
		return bytePatch{}, p.yamlNodeError("invalid_mapping_entry_span", ErrUnsupportedPresentation, mapping)
	}
	keyAt, err := p.resolver.coordinate(match.key.Line, match.key.Column)
	if err != nil {
		return bytePatch{}, err
	}
	lineStart := p.resolver.lines[match.key.Line-1]
	compactSequenceKey := bytes.Equal(bytes.TrimSpace(p.yaml[lineStart:keyAt]), []byte("-"))
	if compactSequenceKey {
		if match.index != 0 || match.index+2 >= len(mapping.Content) {
			return bytePatch{}, p.yamlNodeError("invalid_mapping_entry_span", ErrUnsupportedPresentation, match.key)
		}
		nextKey := mapping.Content[match.index+2]
		nextAt, err := p.resolver.coordinate(nextKey.Line, nextKey.Column)
		if err != nil {
			return bytePatch{}, err
		}
		span := SourceSpan{Start: keyAt, End: nextAt}
		if !span.valid(len(p.yaml)) || span.Start >= span.End {
			return bytePatch{}, p.yamlLocalError("invalid_mapping_entry_span", ErrUnsupportedPresentation, span)
		}
		if err := p.requireOwnedStructuralTrivia(span); err != nil {
			return bytePatch{}, err
		}
		return bytePatch{Start: p.yamlStart + span.Start, End: p.yamlStart + span.End}, nil
	}
	span, err := p.mappingEntrySpan(mapping, match)
	if err != nil {
		return bytePatch{}, err
	}
	return bytePatch{Start: p.yamlStart + span.Start, End: p.yamlStart + span.End}, nil
}

func (p *presentation) structuralBoundary(n *yaml.Node) (int, error) {
	current := n
	for parent := p.parents[current]; parent != nil; parent = p.parents[current] {
		index := yamlChildIndex(parent, current)
		if index < 0 {
			return 0, p.yamlNodeError("unowned_touched_node", ErrUnsupportedPresentation, current)
		}
		next := index + 1
		if parent.Kind == yaml.MappingNode {
			if index%2 == 0 {
				return 0, p.yamlNodeError("unowned_touched_node", ErrUnsupportedPresentation, current)
			}
			next = index + 1
		}
		if next < len(parent.Content) {
			line := parent.Content[next].Line
			if line < 1 || line > len(p.resolver.lines) {
				return 0, p.yamlNodeError("invalid_coordinate", ErrUnsupportedPresentation, parent.Content[next])
			}
			return p.resolver.lines[line-1], nil
		}
		current = parent
	}
	return len(p.yaml), nil
}

func (p *presentation) requireOwnedStructuralTrivia(span SourceSpan) error {
	if !span.valid(len(p.yaml)) {
		return p.yamlLocalError("invalid_collection_span", ErrUnsupportedPresentation, span)
	}
	raw := p.yaml[span.Start:span.End]
	lines := bytes.Split(raw, []byte{'\n'})
	for i, line := range lines {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			if i == len(lines)-1 {
				continue
			}
			return p.yamlLocalError("unowned_trivia", ErrUnsupportedPresentation, span)
		}
		if trimmed[0] == '#' {
			return p.yamlLocalError(yamlCodeUnownedComment, ErrUnsupportedPresentation, span)
		}
	}
	return nil
}
