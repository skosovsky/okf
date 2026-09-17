package mutation

import (
	"context"
	"errors"

	"gopkg.in/yaml.v3"
)

type yamlSelectorField struct {
	Key   string
	Value yamlRenderValue
}

type yamlSequenceSelector struct {
	fields []yamlSelectorField
	exact  *yamlRenderValue
}

func yamlSelector(key string, value yamlRenderValue) yamlSelectorField {
	return yamlSelectorField{Key: key, Value: value}
}

func yamlMappingSelector(fields ...yamlSelectorField) yamlSequenceSelector {
	return yamlSequenceSelector{fields: append([]yamlSelectorField(nil), fields...)}
}

func yamlExactSelector(value yamlRenderValue) yamlSequenceSelector {
	copy := value
	return yamlSequenceSelector{exact: &copy}
}

// yamlMutation batches structural edits against one immutable presentation.
// It accumulates no output bytes until apply, then verifies one final semantic
// projection and exact byte equality outside the union of owned spans.
type yamlMutation struct {
	ctx                context.Context
	p                  *presentation
	patches            []bytePatch
	expected           *yamlSemanticNode
	expectedNodes      map[*yaml.Node]*yamlSemanticNode
	sequenceShadows    map[*yaml.Node]*yamlSequenceShadow
	shadowItemBySource map[*yaml.Node]yamlShadowItemOwner
	mappingAppends     map[*yaml.Node][]*yamlMappingAppend
	collectionValues   map[*yaml.Node]yamlRenderValue
	collectionLineage  map[*yaml.Node][]yamlSequenceLineage
	replacedSubtrees   map[*yaml.Node]bool
	collectionPatch    map[*yaml.Node]int
	valuePatch         map[*yaml.Node]int
	identityOverride   map[*yaml.Node]yamlSequenceIdentity
	insertions         []yamlScheduledInsertion
	nextOrder          int
	failed             error
	materialized       bool
}

type yamlMappingAppend struct {
	key       string
	value     yamlRenderValue
	keyNode   *yamlSemanticNode
	valueNode *yamlSemanticNode
	order     int
}

type yamlScheduledInsertion struct {
	owner *yaml.Node
	at    int
	depth int
	order int
	path  []int
	text  []byte
}

type yamlSequenceShadow struct {
	collection     *yaml.Node
	items          []*yamlSequenceShadowItem
	original       []*yamlSequenceShadowItem
	route          string
	bare           bool
	replacement    bool
	normalized     bool
	dirty          bool
	identityLookup sequenceIdentityLookup
}

type yamlSequenceShadowItem struct {
	value            yamlRenderValue
	identity         yamlSequenceIdentity
	identityErr      error
	lineage          yamlSequenceLineage
	semantic         *yamlSemanticNode
	originalSemantic *yamlSemanticNode
	source           *yaml.Node
	materializeErr   error
	renderable       bool
	dirty            bool
	order            int
	active           bool
	shadowIndex      int
}

type yamlShadowItemOwner struct {
	shadow *yamlSequenceShadow
	item   *yamlSequenceShadowItem
}

func (p *presentation) newYAMLMutationContext(ctx context.Context) *yamlMutation {
	mutation := &yamlMutation{ctx: ctx, p: p}
	if ctx == nil {
		mutation.failed = yamlPresentationError("invalid_patch", ErrUnsupportedPresentation, SourceSpan{})
		return mutation
	}
	if err := ctx.Err(); err != nil {
		mutation.failed = err
		return mutation
	}
	expected, err := semanticYAMLContext(ctx, p.root)
	if err != nil {
		mutation.failed = err
		return mutation
	}
	index := make(map[*yaml.Node]*yamlSemanticNode, len(p.paths))
	if err := indexSemanticNodesContext(ctx, p.root, expected, index); err != nil {
		mutation.failed = err
		return mutation
	}
	return &yamlMutation{
		ctx:                ctx,
		p:                  p,
		expected:           expected,
		expectedNodes:      index,
		sequenceShadows:    make(map[*yaml.Node]*yamlSequenceShadow),
		shadowItemBySource: make(map[*yaml.Node]yamlShadowItemOwner),
		mappingAppends:     make(map[*yaml.Node][]*yamlMappingAppend),
		collectionValues:   make(map[*yaml.Node]yamlRenderValue),
		collectionLineage:  make(map[*yaml.Node][]yamlSequenceLineage),
		replacedSubtrees:   make(map[*yaml.Node]bool),
		collectionPatch:    make(map[*yaml.Node]int),
		valuePatch:         make(map[*yaml.Node]int),
		identityOverride:   make(map[*yaml.Node]yamlSequenceIdentity),
	}
}

func indexSemanticNodesContext(ctx context.Context, source *yaml.Node, semantic *yamlSemanticNode, out map[*yaml.Node]*yamlSemanticNode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if source == nil || semantic == nil {
		return nil
	}
	out[source] = semantic
	for i := 0; i < len(source.Content) && i < len(semantic.Content); i++ {
		if err := indexSemanticNodesContext(ctx, source.Content[i], semantic.Content[i], out); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (m *yamlMutation) apply() ([]byte, error) {
	if m == nil || m.p == nil {
		return nil, yamlPresentationError("invalid_patch", ErrUnsupportedPresentation, SourceSpan{})
	}
	if m.ctx == nil {
		return nil, yamlPresentationError("invalid_patch", ErrUnsupportedPresentation, SourceSpan{})
	}
	if err := m.ctx.Err(); err != nil {
		return nil, err
	}
	if m.failed != nil {
		return nil, m.failed
	}
	if err := m.materializeMappingPatches(); err != nil {
		m.failed = err
		return nil, err
	}
	if err := m.materializeSequencePatches(); err != nil {
		m.failed = err
		return nil, err
	}
	if err := m.flushScheduledInsertions(); err != nil {
		m.failed = err
		return nil, err
	}
	if len(m.patches) == 0 {
		return appendBytesContext(m.ctx, nil, m.p.data)
	}
	patches := append([]bytePatch(nil), m.patches...)
	for i := range patches {
		if err := m.ctx.Err(); err != nil {
			return nil, err
		}
		patches[i].edit = &semanticEdit{expected: m.expected}
	}
	return m.p.patchYAMLContext(m.ctx, patches)
}

func (m *yamlMutation) rememberFailure(err *error) {
	if m != nil && err != nil && *err != nil && m.failed == nil {
		m.failed = *err
	}
}

func (m *yamlMutation) requireActive() error {
	if m == nil || m.p == nil {
		return yamlPresentationError("invalid_patch", ErrUnsupportedPresentation, SourceSpan{})
	}
	if m.ctx == nil {
		return yamlPresentationError("invalid_patch", ErrUnsupportedPresentation, SourceSpan{})
	}
	if err := m.ctx.Err(); err != nil {
		return err
	}
	if m.failed == nil && m.materialized {
		m.failed = yamlPresentationError("mutation_already_applied", ErrUnsupportedPresentation, SourceSpan{Start: 0, End: len(m.p.data)})
	}
	return m.failed
}

func (m *yamlMutation) issueOrder() int {
	order := m.nextOrder
	m.nextOrder++
	return order
}

func (m *yamlMutation) addPatch(patch bytePatch) error {
	_, err := m.addPatchTracked(patch)
	return err
}

func (m *yamlMutation) addPatchTracked(patch bytePatch) (int, error) {
	if m == nil || m.p == nil {
		return -1, yamlPresentationError("invalid_patch", ErrUnsupportedPresentation, SourceSpan{})
	}
	if _, err := m.p.auditBytePatchesContext(m.ctx, []bytePatch{patch}); err != nil {
		return -1, err
	}
	for i := range m.patches {
		existing := m.patches[i]
		if existing.Start == existing.End && patch.Start == patch.End && existing.Start == patch.Start && existing.Owner == patch.Owner {
			// Different append-only desired-state edits at one proven boundary
			// are one atomic insertion in call order. Duplicate semantic keys or
			// selectors are rejected before reaching this point.
			merged := existing
			merged.Text = append(append([]byte(nil), existing.Text...), patch.Text...)
			return i, m.replacePatchAt(i, merged)
		}
		if existing.Start == existing.End && patch.Start < patch.End &&
			(existing.Start == patch.Start || existing.Start == patch.End) && len(patch.Text) == 0 && existing.Owner == patch.Owner {
			patch.Text = append(append([]byte(nil), patch.Text...), existing.Text...)
			return i, m.replacePatchAt(i, patch)
		}
		if patch.Start == patch.End && existing.Start < existing.End &&
			(patch.Start == existing.Start || patch.Start == existing.End) && len(existing.Text) == 0 && existing.Owner == patch.Owner {
			merged := existing
			merged.Text = append(append([]byte(nil), existing.Text...), patch.Text...)
			return i, m.replacePatchAt(i, merged)
		}
		if structuralPatchesConflict(existing, patch) {
			return -1, yamlPresentationError(yamlCodeOverlappingPatch, ErrUnsupportedPresentation, unionSourceSpans(
				SourceSpan{Start: existing.Start, End: existing.End},
				SourceSpan{Start: patch.Start, End: patch.End},
			))
		}
	}
	candidate := append(append([]bytePatch(nil), m.patches...), patch)
	if _, err := m.p.auditBytePatchesContext(m.ctx, candidate); err != nil {
		return -1, err
	}
	m.patches = append(m.patches, patch)
	return len(m.patches) - 1, m.ctx.Err()
}

func (m *yamlMutation) replacePatchAt(index int, patch bytePatch) error {
	if m == nil || m.p == nil || index < 0 || index >= len(m.patches) {
		return yamlPresentationError("invalid_patch", ErrUnsupportedPresentation, SourceSpan{})
	}
	candidate := append([]bytePatch(nil), m.patches...)
	candidate[index] = patch
	if _, err := m.p.auditBytePatchesContext(m.ctx, candidate); err != nil {
		return err
	}
	m.patches[index] = patch
	return m.ctx.Err()
}

func structuralPatchesConflict(left, right bytePatch) bool {
	if left.Start == left.End {
		return left.Start > right.Start && left.Start < right.End
	}
	if right.Start == right.End {
		return right.Start > left.Start && right.Start < left.End
	}
	return left.Start < right.End && right.Start < left.End
}

func (m *yamlMutation) removePatchAt(index int) {
	if index < 0 || index >= len(m.patches) {
		return
	}
	m.patches = append(m.patches[:index], m.patches[index+1:]...)
	for node, patchIndex := range m.valuePatch {
		switch {
		case patchIndex == index:
			delete(m.valuePatch, node)
		case patchIndex > index:
			m.valuePatch[node] = patchIndex - 1
		}
	}
	for node, patchIndex := range m.collectionPatch {
		switch {
		case patchIndex == index:
			delete(m.collectionPatch, node)
		case patchIndex > index:
			m.collectionPatch[node] = patchIndex - 1
		}
	}
}

func (p *presentation) replaceMappingValue(mapping *yaml.Node, key string, value yamlRenderValue) ([]byte, error) {
	mutation := p.newYAMLMutationContext(p.ctx)
	if err := mutation.replaceMappingValue(mapping, key, value); err != nil {
		return nil, err
	}
	return mutation.apply()
}

func (p *presentation) insertMappingValue(mapping *yaml.Node, key string, value yamlRenderValue) ([]byte, error) {
	mutation := p.newYAMLMutationContext(p.ctx)
	if err := mutation.insertMappingValue(mapping, key, value); err != nil {
		return nil, err
	}
	return mutation.apply()
}

type yamlMappingEntryMatch struct {
	index int
	key   *yaml.Node
	value *yaml.Node
}

func (p *presentation) structuralMappingEntry(mapping *yaml.Node, touchedKey string) (yamlMappingEntryMatch, bool, error) {
	if p == nil || p.ctx == nil {
		return yamlMappingEntryMatch{}, false, yamlPresentationError("invalid_patch", ErrUnsupportedPresentation, SourceSpan{})
	}
	if mapping != nil && (mapping.Kind == yaml.AliasNode || mapping.Alias != nil) {
		return yamlMappingEntryMatch{}, false, p.yamlNodeError(yamlCodeAliasProvenance, ErrAmbiguousPresentation, mapping)
	}
	if _, owned := p.paths[mapping]; mapping == nil || !owned {
		return yamlMappingEntryMatch{}, false, p.yamlNodeError("unowned_touched_node", ErrUnsupportedPresentation, mapping)
	}
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return yamlMappingEntryMatch{}, false, p.yamlNodeError(yamlCodeUnsupportedCollection, ErrUnsupportedPresentation, mapping)
	}
	if len(mapping.Content)%2 != 0 {
		return yamlMappingEntryMatch{}, false, p.yamlNodeError(yamlCodeUnsupportedCollection, ErrUnsupportedPresentation, mapping)
	}
	if err := p.rejectMergedKeyContext(p.ctx, mapping, touchedKey); err != nil {
		return yamlMappingEntryMatch{}, false, err
	}
	var matches []yamlMappingEntryMatch
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if err := p.ctx.Err(); err != nil {
			return yamlMappingEntryMatch{}, false, err
		}
		candidate := mapping.Content[i]
		if candidate == nil || mapping.Content[i+1] == nil {
			return yamlMappingEntryMatch{}, false, yamlPresentationError("invalid_patch", ErrUnsupportedPresentation, SourceSpan{})
		}
		if candidate.Kind == yaml.ScalarNode && candidate.Value == touchedKey {
			if err := p.requireScalarKey(candidate); err != nil {
				return yamlMappingEntryMatch{}, false, err
			}
			matches = append(matches, yamlMappingEntryMatch{index: i, key: candidate, value: mapping.Content[i+1]})
		}
	}
	if len(matches) > 1 {
		nodes := make([]*yaml.Node, 0, len(matches))
		for _, match := range matches {
			nodes = append(nodes, match.key)
		}
		return yamlMappingEntryMatch{}, false, p.yamlNodeError(yamlCodeDuplicateTouchedKey, ErrAmbiguousPresentation, nodes...)
	}
	if len(matches) == 0 {
		return yamlMappingEntryMatch{}, false, nil
	}
	if matches[0].value != nil &&
		(matches[0].value.Kind == yaml.AliasNode || matches[0].value.Alias != nil) {
		return yamlMappingEntryMatch{}, false, p.yamlNodeError(
			yamlCodeAliasProvenance,
			ErrAmbiguousPresentation,
			matches[0].value,
		)
	}
	return matches[0], true, nil
}

func semanticMappingEntryIndexContext(ctx context.Context, mapping *yamlSemanticNode, key string) (int, int, error) {
	first := -1
	count := 0
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return first, count, ctx.Err()
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if err := ctx.Err(); err != nil {
			return -1, 0, err
		}
		candidate := mapping.Content[i]
		if candidate == nil || candidate.Kind != yaml.ScalarNode || candidate.Tag != "!!str" {
			continue
		}
		equal, err := stringsEqualContext(ctx, candidate.Value, key)
		if err != nil {
			return -1, 0, err
		}
		if equal {
			if first < 0 {
				first = i
			}
			count++
		}
	}
	return first, count, ctx.Err()
}

func (m *yamlMutation) mappingAppend(mapping *yaml.Node, key string) (*yamlMappingAppend, int) {
	for index, appendEntry := range m.mappingAppends[mapping] {
		if appendEntry != nil && appendEntry.key == key {
			return appendEntry, index
		}
	}
	return nil, -1
}

func setRenderMappingKeyContext(ctx context.Context, mapping *yamlRenderValue, key string, value yamlRenderValue, present bool) (bool, error) {
	if mapping == nil || mapping.kind != yamlRenderMapping {
		return false, ctx.Err()
	}
	for i := range mapping.mapping {
		equal, err := stringsEqualContext(ctx, mapping.mapping[i].Key, key)
		if err != nil {
			return false, err
		}
		if !equal {
			continue
		}
		if present {
			mapping.mapping[i].Value = value
		} else {
			mapping.mapping = append(mapping.mapping[:i], mapping.mapping[i+1:]...)
		}
		return true, ctx.Err()
	}
	if present {
		mapping.mapping = append(mapping.mapping, yamlEntry(key, value))
	}
	return false, ctx.Err()
}

func (m *yamlMutation) mutateReplacementMapping(mapping *yaml.Node, key string, value yamlRenderValue, mode string) (bool, error) {
	current, exists := m.collectionValues[mapping]
	if !exists {
		return false, nil
	}
	if current.kind != yamlRenderMapping {
		return true, m.p.yamlNodeError(yamlCodeUnsupportedCollection, ErrUnsupportedPresentation, mapping)
	}
	changed, err := m.foldReplacementMappingEntry(mapping, &current, key, value, mode)
	if err != nil || !changed {
		return true, err
	}
	return true, m.setCollectionValue(mapping, current)
}

func (m *yamlMutation) foldReplacementMappingEntry(
	mapping *yaml.Node,
	current *yamlRenderValue,
	key string,
	value yamlRenderValue,
	mode string,
) (bool, error) {
	base, err := yamlRenderFromNodeIgnoringCommentsContext(m.ctx, mapping)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false, err
		}
		return false, m.p.renderNodeError(err, mapping)
	}
	if base.kind != yamlRenderMapping || current == nil || current.kind != yamlRenderMapping {
		return false, m.p.yamlNodeError(yamlCodeUnsupportedCollection, ErrUnsupportedPresentation, mapping)
	}
	baseValue, basePresent, err := yamlRenderMappingEntryContext(m.ctx, base, key)
	if err != nil {
		return false, err
	}
	accumulatedValue, accumulatedPresent, err := yamlRenderMappingEntryContext(m.ctx, *current, key)
	if err != nil {
		return false, err
	}
	desiredPresent := mode != "delete"
	switch mode {
	case "replace", "delete":
		if !basePresent {
			return false, m.p.yamlNodeError(yamlCodeMissingMappingEntry, ErrUnsupportedPresentation, mapping)
		}
	case "insert":
		if basePresent {
			return false, m.p.yamlNodeError(yamlCodeDuplicateTouchedKey, ErrAmbiguousPresentation, mapping)
		}
	default:
		return false, m.p.yamlNodeError(yamlCodeUnsupportedCollection, ErrUnsupportedPresentation, mapping)
	}
	merged, present, mergeErr := mergeYAMLDesiredOptionalContext(
		m.ctx,
		baseValue,
		basePresent,
		accumulatedValue,
		accumulatedPresent,
		value,
		desiredPresent,
		key,
	)
	if mergeErr != nil {
		return false, m.yamlDesiredError(mapping, mergeErr)
	}
	equal, equalErr := equalYAMLRenderOptionalContext(m.ctx, accumulatedValue, accumulatedPresent, merged, present)
	if equalErr != nil {
		return false, equalErr
	}
	if equal {
		return false, nil
	}
	if _, err := setRenderMappingKeyContext(m.ctx, current, key, merged, present); err != nil {
		return false, err
	}
	return true, nil
}

func (p *presentation) nodeWithinContext(ctx context.Context, node, ancestor *yaml.Node) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	nodePath, nodeOwned := p.paths[node]
	ancestorPath, ancestorOwned := p.paths[ancestor]
	if !nodeOwned || !ancestorOwned || len(ancestorPath) > len(nodePath) {
		return false, nil
	}
	for index := range ancestorPath {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if ancestorPath[index] != nodePath[index] {
			return false, nil
		}
	}
	return true, ctx.Err()
}

type yamlRenderPathStep struct {
	key      string
	index    int
	route    string
	identity yamlSequenceIdentity
	sequence bool
}

func (m *yamlMutation) renderStepsBetween(ancestor, node *yaml.Node) ([]yamlRenderPathStep, bool, error) {
	if err := m.ctx.Err(); err != nil {
		return nil, false, err
	}
	within, err := m.p.nodeWithinContext(m.ctx, node, ancestor)
	if err != nil {
		return nil, false, err
	}
	if ancestor == nil || node == nil || !within {
		return nil, false, nil
	}
	var reversed []yamlRenderPathStep
	for current := node; current != ancestor; {
		parent := m.p.parents[current]
		if parent == nil {
			return nil, false, nil
		}
		index, err := m.p.yamlChildIndexContext(m.ctx, parent, current)
		if err != nil {
			return nil, false, err
		}
		if index < 0 {
			return nil, false, nil
		}
		switch parent.Kind {
		case yaml.MappingNode:
			if index%2 == 0 {
				return nil, false, nil
			}
			reversed = append(reversed, yamlRenderPathStep{key: parent.Content[index-1].Value})
		case yaml.SequenceNode:
			rendered, err := yamlRenderFromNodeIgnoringCommentsContext(m.ctx, current)
			if err != nil {
				return nil, false, err
			}
			route := m.collectionRoute(parent)
			identity, err := sequenceIdentityContext(m.ctx, rendered, route)
			if err != nil {
				return nil, false, err
			}
			reversed = append(reversed, yamlRenderPathStep{
				index: index, route: route, identity: identity, sequence: true,
			})
		default:
			return nil, false, nil
		}
		current = parent
	}
	steps := make([]yamlRenderPathStep, len(reversed))
	for i := range reversed {
		steps[len(reversed)-1-i] = reversed[i]
	}
	return steps, true, m.ctx.Err()
}

func (m *yamlMutation) replacementRenderPath(node *yaml.Node) (*yaml.Node, []yamlRenderPathStep, bool, error) {
	for current := node; current != nil; current = m.p.parents[current] {
		if err := m.ctx.Err(); err != nil {
			return nil, nil, false, err
		}
		parent := m.p.parents[current]
		if parent == nil {
			break
		}
		if _, replaced := m.collectionValues[parent]; replaced {
			steps, ok, err := m.renderStepsBetween(parent, node)
			if err != nil {
				return nil, nil, false, err
			}
			if !ok {
				return nil, nil, false, nil
			}
			return parent, steps, true, nil
		}
	}
	return nil, nil, false, m.ctx.Err()
}

func (m *yamlMutation) replacementShadowPath(
	node *yaml.Node,
) (*yamlSequenceShadow, *yamlSequenceShadowItem, []yamlRenderPathStep, bool, error) {
	for ancestor := node; ancestor != nil; ancestor = m.p.parents[ancestor] {
		if err := m.ctx.Err(); err != nil {
			return nil, nil, nil, false, err
		}
		owner, present := m.shadowItemBySource[ancestor]
		if !present || owner.shadow == nil || owner.item == nil || !owner.item.active || !owner.item.dirty ||
			m.sequenceShadows[owner.shadow.collection] != owner.shadow {
			continue
		}
		steps, ok, err := m.renderStepsBetween(ancestor, node)
		if err != nil {
			return nil, nil, nil, false, err
		}
		if !ok {
			return nil, nil, nil, false, nil
		}
		return owner.shadow, owner.item, steps, true, nil
	}
	return nil, nil, nil, false, m.ctx.Err()
}

func (m *yamlMutation) indexSequenceShadow(shadow *yamlSequenceShadow) error {
	if shadow == nil {
		return m.ctx.Err()
	}
	for _, item := range shadow.items {
		if err := m.ctx.Err(); err != nil {
			return err
		}
		if item.source == nil {
			continue
		}
		if existing, exists := m.shadowItemBySource[item.source]; exists && existing.item != item &&
			existing.item != nil && existing.item.active && m.sequenceShadows[existing.shadow.collection] == existing.shadow {
			return m.p.yamlNodeError(yamlCodeOverlappingPatch, ErrUnsupportedPresentation, item.source, shadow.collection)
		}
		item.active = true
		m.shadowItemBySource[item.source] = yamlShadowItemOwner{shadow: shadow, item: item}
	}
	return m.ctx.Err()
}

func (m *yamlMutation) syncReplacementShadowItem(
	shadow *yamlSequenceShadow,
	item *yamlSequenceShadowItem,
	value yamlRenderValue,
) error {
	identity, err := sequenceIdentityContext(m.ctx, value, shadow.route)
	if err != nil {
		return err
	}
	equal, err := equalSequenceIdentityContext(m.ctx, identity, item.identity)
	if err != nil {
		return err
	}
	if !equal {
		return m.p.yamlNodeError(yamlCodeOverlappingPatch, ErrUnsupportedPresentation, item.source, shadow.collection)
	}
	match, found, err := shadow.identityLookup.uniqueIndexContext(m.ctx, identity)
	if err != nil {
		return err
	}
	if !found || match != item.shadowIndex {
		var other *yaml.Node
		if found && match >= 0 && match < len(shadow.items) {
			other = shadow.items[match].source
		}
		return m.p.yamlNodeError(yamlCodeAmbiguousSelector, ErrAmbiguousPresentation, item.source, other, shadow.collection)
	}
	item.value = value
	item.identity = identity
	item.identityErr = nil
	semantic, err := value.semanticContext(m.ctx)
	if err != nil {
		return err
	}
	item.semantic = semantic
	item.renderable = true
	semanticEqual, err := equalSemanticYAMLContext(m.ctx, item.originalSemantic, item.semantic)
	if err != nil {
		return err
	}
	item.dirty = !semanticEqual
	if item.source != nil {
		if item.dirty {
			m.replacedSubtrees[item.source] = true
		} else {
			delete(m.replacedSubtrees, item.source)
		}
	}
	shadow.dirty = true
	return m.syncSequenceExpected(shadow)
}

func (m *yamlMutation) commitReplacementShadowRoot(
	shadow *yamlSequenceShadow,
	item *yamlSequenceShadowItem,
	desired yamlRenderValue,
) error {
	if item.originalSemantic == nil {
		equal, err := equalYAMLRenderStateContext(m.ctx, item.value, desired)
		if err != nil {
			return err
		}
		if equal {
			return nil
		}
		return m.p.yamlNodeError(yamlCodeOverlappingPatch, ErrUnsupportedPresentation, item.source, shadow.collection)
	}
	base, baseErr := yamlRenderFromSemanticContext(m.ctx, item.originalSemantic)
	if baseErr != nil {
		return m.p.preserveTraversalError(baseErr, "semantic_edit_path", ErrUnsupportedPresentation, item.source, shadow.collection)
	}
	merged, err := m.foldYAMLDesired(item.source, base, item.value, desired, "")
	if err != nil {
		return err
	}
	return m.syncReplacementShadowItem(shadow, item, merged)
}

func yamlRenderValueAtPathContext(ctx context.Context, root *yamlRenderValue, steps []yamlRenderPathStep) (*yamlRenderValue, bool, error) {
	current := root
	for _, step := range steps {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		if step.sequence {
			if current.kind != yamlRenderSequence {
				return nil, false, nil
			}
			identities, err := sequenceIdentitiesContext(ctx, *current, step.route)
			if err != nil {
				return nil, false, err
			}
			lookup, err := newSequenceIdentityLookupContext(ctx, identities)
			if err != nil {
				return nil, false, err
			}
			match, found, err := lookup.uniqueIndexContext(ctx, step.identity)
			if err != nil || !found {
				return nil, false, err
			}
			current = &current.sequence[match]
			continue
		}
		if current.kind != yamlRenderMapping {
			return nil, false, nil
		}
		found := false
		for i := range current.mapping {
			equal, err := stringsEqualContext(ctx, current.mapping[i].Key, step.key)
			if err != nil {
				return nil, false, err
			}
			if equal {
				current = &current.mapping[i].Value
				found = true
				break
			}
		}
		if !found {
			return nil, false, nil
		}
	}
	return current, true, ctx.Err()
}

func (m *yamlMutation) collectionRoute(collection *yaml.Node) string {
	parent := m.p.parents[collection]
	if parent == nil || parent.Kind != yaml.MappingNode {
		return ""
	}
	path, owned := m.p.paths[collection]
	if !owned || len(path) == 0 {
		return ""
	}
	index := path[len(path)-1]
	if index <= 0 || index%2 == 0 {
		return ""
	}
	if index >= len(parent.Content) || parent.Content[index] != collection {
		return ""
	}
	return parent.Content[index-1].Value
}

func (m *yamlMutation) foldYAMLDesired(
	owner *yaml.Node,
	base, accumulated, whole yamlRenderValue,
	route string,
) (yamlRenderValue, error) {
	merged, err := mergeYAMLDesiredContext(m.ctx, base, accumulated, whole, route)
	if err != nil {
		return yamlRenderValue{}, m.yamlDesiredError(owner, err)
	}
	return merged, nil
}

func (m *yamlMutation) yamlDesiredError(owner *yaml.Node, err error) error {
	if errors.Is(err, errYAMLDuplicateSequenceIdentity) {
		return m.p.preserveTraversalError(err, yamlCodeAmbiguousSelector, ErrAmbiguousPresentation, owner)
	}
	return m.p.preserveTraversalError(err, yamlCodeOverlappingPatch, ErrUnsupportedPresentation, owner)
}

func (m *yamlMutation) requireUniqueDesired(owner *yaml.Node, value yamlRenderValue, route string) error {
	if err := validateYAMLDesiredUniquenessContext(m.ctx, value, route); err != nil {
		return m.yamlDesiredError(owner, err)
	}
	return nil
}

func cloneSequenceLineages(lineages []yamlSequenceLineage) []yamlSequenceLineage {
	return append([]yamlSequenceLineage(nil), lineages...)
}
