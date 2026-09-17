package mutation

import (
	"context"
	"crypto/sha256"
	"fmt"

	"gopkg.in/yaml.v3"
)

func (value yamlRenderValue) emptyCollection() bool {
	return value.kind == yamlRenderMapping && len(value.mapping) == 0 ||
		value.kind == yamlRenderSequence && len(value.sequence) == 0
}

func (p *presentation) selectSequenceItem(collection *yaml.Node, selector yamlSequenceSelector) (*yaml.Node, bool, error) {
	if err := p.requireMutationPathProvenance(collection); err != nil {
		return nil, false, err
	}
	if err := p.validateYAMLSequenceSelector(collection, selector); err != nil {
		return nil, false, p.preserveTraversalError(err, yamlCodeUnsupportedSelector, ErrUnsupportedPresentation, collection)
	}
	items, err := p.sequenceSelectorItems(collection)
	if err != nil {
		return nil, false, err
	}
	var matches []*yaml.Node
	for _, item := range items {
		matched, err := p.matchesSequenceSelector(item, selector)
		if err != nil {
			return nil, false, err
		}
		if matched {
			matches = append(matches, item)
		}
	}
	if len(matches) > 1 {
		return nil, false, p.yamlNodeError(yamlCodeAmbiguousSelector, ErrAmbiguousPresentation, matches...)
	}
	if len(matches) == 0 {
		return nil, false, nil
	}
	return matches[0], true, nil
}

func (p *presentation) sequenceSelectorItems(collection *yaml.Node) ([]*yaml.Node, error) {
	if collection == nil {
		return nil, p.yamlNodeError(yamlCodeUnsupportedCollection, ErrUnsupportedPresentation, collection)
	}
	if collection.Kind == yaml.AliasNode || collection.Alias != nil {
		return nil, p.yamlNodeError(yamlCodeAliasProvenance, ErrAmbiguousPresentation, collection)
	}
	if collection.Kind == yaml.MappingNode {
		return []*yaml.Node{collection}, nil
	}
	if collection.Kind != yaml.SequenceNode {
		return nil, p.yamlNodeError(yamlCodeUnsupportedCollection, ErrUnsupportedPresentation, collection)
	}
	for _, item := range collection.Content {
		if item == nil || item.Kind == yaml.AliasNode || item.Alias != nil {
			return nil, p.yamlNodeError(yamlCodeAliasProvenance, ErrAmbiguousPresentation, item)
		}
		if item.Kind != yaml.MappingNode {
			return nil, p.yamlNodeError(yamlCodeUnsupportedCollection, ErrUnsupportedPresentation, item)
		}
	}
	return collection.Content, nil
}

func (p *presentation) validateYAMLSequenceSelector(collection *yaml.Node, selector yamlSequenceSelector) error {
	if selector.exact != nil {
		if len(selector.fields) != 0 {
			return fmt.Errorf("mixed selector")
		}
		if err := validateYAMLRenderValueContext(p.ctx, *selector.exact); err != nil {
			return err
		}
		if p.mappingOwnerKey(collection) != "sources" || selector.exact.kind != yamlRenderMapping ||
			yamlRenderMappingHasKey(*selector.exact, "id") {
			return fmt.Errorf("exact selector is only valid for anonymous sources")
		}
		return nil
	}
	if len(selector.fields) == 0 {
		return fmt.Errorf("empty selector")
	}
	seen := make(map[string]bool, len(selector.fields))
	for _, field := range selector.fields {
		semantic, err := field.Value.semanticContext(p.ctx)
		if err != nil {
			return err
		}
		if field.Key == "" || seen[field.Key] || semantic == nil || semantic.Kind != yaml.ScalarNode {
			return fmt.Errorf("invalid selector field")
		}
		seen[field.Key] = true
	}
	switch p.mappingOwnerKey(collection) {
	case "sources":
		if len(selector.fields) == 1 && selector.fields[0].Key == "id" {
			return nil
		}
	case "verified":
		if len(selector.fields) == 2 && selectorFieldSet(selector, "by", "at") {
			return nil
		}
	case "parameters":
		if len(selector.fields) == 1 && selector.fields[0].Key == "name" {
			return nil
		}
	}
	return fmt.Errorf("selector route is not supported")
}

func selectorFieldSet(selector yamlSequenceSelector, keys ...string) bool {
	if len(selector.fields) != len(keys) {
		return false
	}
	seen := make(map[string]bool, len(selector.fields))
	for _, field := range selector.fields {
		seen[field.Key] = true
	}
	for _, key := range keys {
		if !seen[key] {
			return false
		}
	}
	return true
}

func yamlRenderMappingHasKey(value yamlRenderValue, key string) bool {
	if value.kind != yamlRenderMapping {
		return false
	}
	for _, entry := range value.mapping {
		if entry.Key == key {
			return true
		}
	}
	return false
}

func yamlRenderMappingValue(value yamlRenderValue, key string) (yamlRenderValue, bool) {
	if value.kind != yamlRenderMapping {
		return yamlRenderValue{}, false
	}
	for _, entry := range value.mapping {
		if entry.Key == key {
			return entry.Value, true
		}
	}
	return yamlRenderValue{}, false
}

func validateSelectorDesiredContext(ctx context.Context, selector yamlSequenceSelector, desired yamlRenderValue) error {
	if selector.exact != nil {
		equal, err := equalYAMLRenderCanonicalAtKeyContext(ctx, *selector.exact, desired, "")
		if err != nil {
			return err
		}
		if !equal {
			return fmt.Errorf("exact selector differs from desired item")
		}
		return nil
	}
	if desired.kind != yamlRenderMapping {
		return fmt.Errorf("field selector requires mapping desired item")
	}
	for _, field := range selector.fields {
		actual, ok := yamlRenderMappingValue(desired, field.Key)
		if !ok {
			return fmt.Errorf("selector field %q differs from desired item", field.Key)
		}
		equal, err := equalYAMLRenderCanonicalAtKeyContext(ctx, actual, field.Value, field.Key)
		if err != nil {
			return err
		}
		if !equal {
			return fmt.Errorf("selector field %q differs from desired item", field.Key)
		}
	}
	return nil
}

func (p *presentation) matchesSequenceSelector(item *yaml.Node, selector yamlSequenceSelector) (bool, error) {
	if item == nil || item.Kind == yaml.AliasNode || item.Alias != nil {
		return false, p.yamlNodeError(yamlCodeAliasProvenance, ErrAmbiguousPresentation, item)
	}
	if selector.exact != nil {
		// Exact selectors are reserved for anonymous sources. An identified
		// sibling can be excluded from the match using only its owned id field;
		// opaque extension YAML in that sibling is outside the selector
		// boundary and must not prevent a granular edit of another item.
		if parent := p.parents[item]; parent != nil && p.mappingOwnerKey(parent) == "sources" {
			_, identified, err := p.structuralMappingEntry(item, "id")
			if err != nil {
				return false, err
			}
			if identified {
				return false, nil
			}
		}
		if err := p.requireWholeCollectionOwnership(item); err != nil {
			return false, err
		}
		rendered, err := yamlRenderFromNodeContext(p.ctx, item)
		if err != nil {
			return false, p.renderNodeError(err, item)
		}
		return equalYAMLRenderCanonicalAtKeyContext(p.ctx, rendered, *selector.exact, "")
	}
	if item.Kind != yaml.MappingNode {
		return false, nil
	}
	for _, field := range selector.fields {
		match, ok, err := p.structuralMappingEntry(item, field.Key)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
		if err := p.requireSelectorFieldOwnership(item, match); err != nil {
			return false, err
		}
		actual, err := yamlRenderFromNodeContext(p.ctx, match.value)
		if err != nil {
			return false, p.renderNodeError(err, match.value)
		}
		equal, err := equalYAMLRenderCanonicalAtKeyContext(p.ctx, actual, field.Value, field.Key)
		if err != nil {
			return false, err
		}
		if !equal {
			return false, nil
		}
	}
	return true, nil
}

// requireSelectorFieldOwnership proves only the identity-bearing field. An
// opaque sibling is outside the lookup boundary and must not make a targeted
// nested edit lossy merely because it carries a comment or extension syntax.
func (p *presentation) requireSelectorFieldOwnership(item *yaml.Node, match yamlMappingEntryMatch) error {
	if item != nil && (item.Kind == yaml.AliasNode || item.Alias != nil) {
		return p.yamlNodeError(yamlCodeAliasProvenance, ErrAmbiguousPresentation, item)
	}
	if item == nil || item.Kind != yaml.MappingNode || match.value == nil {
		return p.yamlNodeError(yamlCodeUnsupportedCollection, ErrUnsupportedPresentation, item)
	}
	if item.Anchor != "" {
		return p.yamlNodeError(yamlCodeTouchedAnchor, ErrUnsupportedPresentation, item)
	}
	if err := p.requireNoSyntaxProvenance(item); err != nil {
		return err
	}
	if err := p.requireNoComments(match.key); err != nil {
		return err
	}
	if match.value.Kind == yaml.AliasNode || match.value.Alias != nil {
		return p.yamlNodeError(yamlCodeAliasProvenance, ErrAmbiguousPresentation, match.value)
	}
	if match.value.Anchor != "" {
		return p.yamlNodeError(yamlCodeTouchedAnchor, ErrUnsupportedPresentation, match.value)
	}
	if match.value.Kind != yaml.ScalarNode {
		return p.yamlNodeError(yamlCodeUnsupportedSelector, ErrUnsupportedPresentation, match.value)
	}
	if err := p.requireNoSyntaxProvenance(match.value); err != nil {
		return err
	}
	if err := p.requireNoComments(match.value); err != nil {
		return err
	}
	if p.nodeHasFlowAncestorWithin(match.value, item) {
		// The whole flow collection is parser-backed when it is rewritten.
		// blockPlainScalarEnd intentionally does not claim flow punctuation.
		return nil
	}
	_, err := p.resolver.typedScalar(match.value)
	return err
}

func yamlRenderFromNodeIgnoringCommentsContext(ctx context.Context, n *yaml.Node) (yamlRenderValue, error) {
	var clone func(*yaml.Node) *yaml.Node
	clone = func(current *yaml.Node) *yaml.Node {
		if current == nil || ctx.Err() != nil {
			return nil
		}
		detached := *current
		// Provenance remains attached to the source item and is proved when a
		// selector or mutator touches it; it is not shadow semantic state.
		detached.Anchor = ""
		detached.HeadComment = ""
		detached.LineComment = ""
		detached.FootComment = ""
		detached.Content = make([]*yaml.Node, len(current.Content))
		for i, child := range current.Content {
			if ctx.Err() != nil {
				return nil
			}
			detached.Content[i] = clone(child)
		}
		return &detached
	}
	detached := clone(n)
	if err := ctx.Err(); err != nil {
		return yamlRenderValue{}, err
	}
	return yamlRenderFromNodeContext(ctx, detached)
}

// provenSequenceIdentityDeferred derives immutable route identity only from
// selector-bearing fields. If a required field is provably absent, a field
// selector cannot match the item. Full rendering is attempted only to support
// exact identity; an opaque payload is returned as unresolved so field
// selectors may safely skip it while exact selectors remain fail-closed.
func (p *presentation) provenSequenceIdentityDeferred(
	item *yaml.Node,
	route string,
) (yamlSequenceIdentity, error, error) {
	if item == nil || item.Kind == yaml.AliasNode || item.Alias != nil {
		return yamlSequenceIdentity{}, nil, p.yamlNodeError(yamlCodeAliasProvenance, ErrAmbiguousPresentation, item)
	}
	if item.Kind != yaml.MappingNode {
		return yamlSequenceIdentity{}, nil, p.yamlNodeError(yamlCodeUnsupportedCollection, ErrUnsupportedPresentation, item)
	}

	var keys []string
	switch route {
	case "sources":
		keys = []string{"id"}
	case "verified":
		keys = []string{"by", "at"}
	case "parameters":
		keys = []string{"name"}
	default:
		rendered, renderErr := yamlRenderFromNodeIgnoringCommentsContext(p.ctx, item)
		if renderErr != nil {
			return yamlSequenceIdentity{}, nil, p.renderNodeError(renderErr, item)
		}
		identity, identityErr := sequenceIdentityContext(p.ctx, rendered, route)
		return identity, nil, identityErr
	}

	identity := yamlSequenceIdentity{kind: route}
	for _, key := range keys {
		match, present, err := p.structuralMappingEntry(item, key)
		if err != nil {
			return yamlSequenceIdentity{}, nil, err
		}
		if !present {
			rendered, renderErr := yamlRenderFromNodeIgnoringCommentsContext(p.ctx, item)
			if renderErr != nil {
				return yamlSequenceIdentity{kind: "unresolved"}, p.renderNodeError(renderErr, item), nil
			}
			identity, identityErr := sequenceIdentityContext(p.ctx, rendered, route)
			return identity, nil, identityErr
		}
		if err := p.requireSelectorFieldOwnership(item, match); err != nil {
			return yamlSequenceIdentity{}, nil, err
		}
		value, renderErr := yamlRenderFromNodeContext(p.ctx, match.value)
		if renderErr != nil {
			return yamlSequenceIdentity{}, nil, p.renderNodeError(renderErr, match.value)
		}
		identity.fields = append(identity.fields, yamlSelector(key, value))
	}
	canonical, err := sequenceIdentityCanonicalContext(p.ctx, identity)
	if err != nil {
		return yamlSequenceIdentity{}, nil, err
	}
	identity.canonical = canonical
	return identity, nil, nil
}

func (p *presentation) provenSequenceIdentity(item *yaml.Node, route string) (yamlSequenceIdentity, error) {
	identity, unresolvedErr, err := p.provenSequenceIdentityDeferred(item, route)
	if err != nil {
		return yamlSequenceIdentity{}, err
	}
	if unresolvedErr != nil {
		return yamlSequenceIdentity{}, unresolvedErr
	}
	return identity, nil
}

func (m *yamlMutation) sequenceShadow(collection *yaml.Node) (*yamlSequenceShadow, error) {
	if shadow := m.sequenceShadows[collection]; shadow != nil {
		return shadow, nil
	}
	if current, replaced := m.collectionValues[collection]; replaced {
		shadow := &yamlSequenceShadow{
			collection:  collection,
			route:       m.collectionRoute(collection),
			bare:        collection.Kind == yaml.MappingNode,
			replacement: true,
		}
		values := current.sequence
		if shadow.bare {
			if current.kind != yamlRenderMapping {
				return nil, m.p.yamlNodeError(yamlCodeUnsupportedCollection, ErrUnsupportedPresentation, collection)
			}
			values = []yamlRenderValue{current}
		} else if current.kind != yamlRenderSequence {
			return nil, m.p.yamlNodeError(yamlCodeUnsupportedCollection, ErrUnsupportedPresentation, collection)
		}
		currentSequence := yamlSequence(values...)
		currentIDs, identityErr := sequenceIdentitiesContext(m.ctx, currentSequence, shadow.route)
		if identityErr != nil {
			return nil, m.p.preserveTraversalError(identityErr, yamlCodeAmbiguousSelector, ErrAmbiguousPresentation, collection)
		}
		sourceItems, sourceErr := m.p.sequenceSelectorItems(collection)
		if sourceErr != nil {
			return nil, sourceErr
		}
		baseIDs := make([]yamlSequenceIdentity, len(sourceItems))
		baseBuckets := make(map[[sha256.Size]byte][]int, len(sourceItems))
		for index, source := range sourceItems {
			baseID, identityErr := m.p.provenSequenceIdentity(source, shadow.route)
			if identityErr != nil {
				return nil, identityErr
			}
			baseIDs[index] = baseID
			if !isSequenceIdentityRoute(shadow.route) {
				continue
			}
			canonical := baseID.canonical
			if canonical == "" {
				canonical, identityErr = sequenceIdentityCanonicalContext(m.ctx, baseID)
				if identityErr != nil {
					return nil, identityErr
				}
				baseIDs[index].canonical = canonical
			}
			digest, digestErr := stringFingerprintContext(m.ctx, canonical)
			if digestErr != nil {
				return nil, digestErr
			}
			baseIDs[index].canonicalDigest = digest
			for _, previous := range baseBuckets[digest] {
				equal, equalErr := equalSequenceIdentityContext(m.ctx, baseIDs[index], baseIDs[previous])
				if equalErr != nil {
					return nil, equalErr
				}
				if equal {
					return nil, m.p.yamlNodeError(
						yamlCodeAmbiguousSelector,
						ErrAmbiguousPresentation,
						sourceItems[previous],
						source,
						collection,
					)
				}
			}
			baseBuckets[digest] = append(baseBuckets[digest], index)
		}
		baseLookup, identityErr := newSequenceIdentityLookupContext(m.ctx, baseIDs)
		if identityErr != nil {
			return nil, identityErr
		}
		storedLineage := m.collectionLineage[collection]
		for index, value := range values {
			var source *yaml.Node
			var original *yamlSemanticNode
			var semanticErr error
			lineage := yamlSequenceLineage{baseIndex: -1, added: currentIDs[index]}
			if index < len(storedLineage) {
				lineage = storedLineage[index]
			} else if baseIndex, present, lookupErr := baseLookup.indexContext(m.ctx, currentIDs[index]); lookupErr != nil {
				return nil, lookupErr
			} else if present {
				source = sourceItems[baseIndex]
				original, semanticErr = semanticValidatedYAMLContext(m.ctx, source)
				if semanticErr != nil {
					return nil, semanticErr
				}
				lineage = yamlSequenceLineage{baseIndex: baseIndex}
			}
			if lineage.baseIndex >= 0 && lineage.baseIndex < len(sourceItems) {
				source = sourceItems[lineage.baseIndex]
				original, semanticErr = semanticValidatedYAMLContext(m.ctx, source)
				if semanticErr != nil {
					return nil, semanticErr
				}
			}
			semantic, semanticErr := value.semanticContext(m.ctx)
			if semanticErr != nil {
				return nil, semanticErr
			}
			item := &yamlSequenceShadowItem{
				value:            value,
				identity:         currentIDs[index],
				lineage:          lineage,
				semantic:         semantic,
				originalSemantic: original,
				source:           source,
				renderable:       true,
			}
			shadow.items = append(shadow.items, item)
			shadow.original = append(shadow.original, item)
		}
		if err := m.validateShadowIdentities(shadow); err != nil {
			return nil, err
		}
		m.sequenceShadows[collection] = shadow
		if err := m.indexSequenceShadow(shadow); err != nil {
			delete(m.sequenceShadows, collection)
			return nil, err
		}
		return shadow, nil
	}
	items, err := m.p.sequenceSelectorItems(collection)
	if err != nil {
		return nil, err
	}
	shadow := &yamlSequenceShadow{
		collection: collection,
		route:      m.collectionRoute(collection),
		bare:       collection.Kind == yaml.MappingNode,
	}
	for baseIndex, node := range items {
		baseIdentity, unresolvedIdentityErr, identityErr := m.p.provenSequenceIdentityDeferred(node, shadow.route)
		if identityErr != nil {
			return nil, identityErr
		}
		rendered, err := yamlRenderFromNodeIgnoringCommentsContext(m.ctx, node)
		renderable := err == nil
		var materializeErr error
		if err != nil {
			materializeErr = m.p.renderNodeError(err, node)
		}
		semantic := m.expectedNodes[node]
		if semantic == nil {
			return nil, m.p.yamlNodeError("semantic_edit_path", ErrUnsupportedPresentation, node)
		}
		itemSemantic := semantic
		if node == collection {
			// A bare mapping becomes the first item of its replacement
			// sequence. Keeping the collection's semantic pointer here would
			// make syncSequenceExpected wrap the node around itself.
			itemSemantic, err = cloneSemanticNodeContext(m.ctx, semantic)
			if err != nil {
				return nil, err
			}
		}
		currentIdentity := baseIdentity
		if override, present := m.identityOverride[node]; present {
			currentIdentity = override
		}
		originalSemantic, err := semanticValidatedYAMLContext(m.ctx, node)
		if err != nil {
			return nil, err
		}
		item := &yamlSequenceShadowItem{
			value:            rendered,
			identity:         currentIdentity,
			identityErr:      unresolvedIdentityErr,
			lineage:          yamlSequenceLineage{baseIndex: baseIndex},
			semantic:         itemSemantic,
			originalSemantic: originalSemantic,
			source:           node,
			materializeErr:   materializeErr,
			renderable:       renderable,
		}
		shadow.items = append(shadow.items, item)
		shadow.original = append(shadow.original, item)
	}
	if err := m.validateShadowIdentities(shadow); err != nil {
		return nil, err
	}
	m.sequenceShadows[collection] = shadow
	if err := m.indexSequenceShadow(shadow); err != nil {
		delete(m.sequenceShadows, collection)
		return nil, err
	}
	return shadow, nil
}

func (m *yamlMutation) requireShadowItemMaterializable(item *yamlSequenceShadowItem) error {
	if item == nil || item.renderable {
		return nil
	}
	if item.materializeErr != nil {
		return item.materializeErr
	}
	return m.p.yamlNodeError(yamlCodeUnsupportedCollection, ErrUnsupportedPresentation, item.source)
}

func (m *yamlMutation) validateShadowIdentities(shadow *yamlSequenceShadow) error {
	buckets := make(map[[sha256.Size]byte][]int, len(shadow.items))
	identities := make([]yamlSequenceIdentity, len(shadow.items))
	for i, item := range shadow.items {
		item.shadowIndex = i
		if item.source == nil || item.dirty {
			identity, err := sequenceIdentityContext(m.ctx, item.value, shadow.route)
			if err != nil {
				return err
			}
			item.identity = identity
			item.identityErr = nil
		}
		if item.identity.kind == "unresolved" {
			identities[i] = item.identity
			continue
		}
		canonical := item.identity.canonical
		if canonical == "" {
			var err error
			canonical, err = sequenceIdentityCanonicalContext(m.ctx, item.identity)
			if err != nil {
				return err
			}
			item.identity.canonical = canonical
		}
		digest, err := stringFingerprintContext(m.ctx, canonical)
		if err != nil {
			return err
		}
		item.identity.canonicalDigest = digest
		for _, j := range buckets[digest] {
			equal, equalErr := equalSequenceIdentityContext(m.ctx, item.identity, shadow.items[j].identity)
			if equalErr != nil {
				return equalErr
			}
			if equal && isSequenceIdentityRoute(shadow.route) {
				return m.p.yamlNodeError(
					yamlCodeAmbiguousSelector,
					ErrAmbiguousPresentation,
					shadow.items[j].source,
					item.source,
					shadow.collection,
				)
			}
		}
		buckets[digest] = append(buckets[digest], i)
		identities[i] = item.identity
	}
	lookup, err := newSequenceIdentityLookupContext(m.ctx, identities)
	if err != nil {
		return err
	}
	shadow.identityLookup = lookup
	return m.ctx.Err()
}

func (m *yamlMutation) selectShadowItem(shadow *yamlSequenceShadow, selector yamlSequenceSelector) (int, bool, error) {
	matches := make([]int, 0, 1)
	for index, item := range shadow.items {
		var matched bool
		var err error
		if isSequenceIdentityRoute(shadow.route) {
			if item.identity.kind == "unresolved" {
				if selector.exact != nil {
					err = item.identityErr
					if err == nil {
						err = m.p.yamlNodeError(yamlCodeAmbiguousSelector, ErrAmbiguousPresentation, item.source)
					}
				}
			} else {
				matched, err = matchesSequenceIdentitySelectorContext(m.ctx, item.identity, selector)
			}
		} else if item.source != nil && !item.dirty {
			matched, err = m.p.matchesSequenceSelector(item.source, selector)
		} else {
			matched, err = matchesRenderSelectorContext(m.ctx, item.value, selector)
		}
		if err != nil {
			return 0, false, err
		}
		if matched {
			matches = append(matches, index)
		}
	}
	if len(matches) > 1 {
		nodes := make([]*yaml.Node, 0, len(matches))
		for _, index := range matches {
			if shadow.items[index].source != nil {
				nodes = append(nodes, shadow.items[index].source)
			}
		}
		return 0, false, m.p.yamlNodeError(yamlCodeAmbiguousSelector, ErrAmbiguousPresentation, nodes...)
	}
	if len(matches) == 0 {
		return 0, false, nil
	}
	return matches[0], true, nil
}

func matchesSequenceIdentitySelectorContext(ctx context.Context, identity yamlSequenceIdentity, selector yamlSequenceSelector) (bool, error) {
	if selector.exact != nil {
		if identity.kind != "exact" {
			return false, nil
		}
		return equalYAMLRenderCanonicalAtKeyContext(ctx, identity.exact, *selector.exact, "")
	}
	if identity.kind == "exact" {
		return false, nil
	}
	for _, selectorField := range selector.fields {
		found := false
		for _, identityField := range identity.fields {
			if selectorField.Key != identityField.Key {
				continue
			}
			equal, err := equalYAMLRenderCanonicalAtKeyContext(ctx, selectorField.Value, identityField.Value, selectorField.Key)
			if err != nil {
				return false, err
			}
			if equal {
				found = true
				break
			}
		}
		if !found {
			return false, nil
		}
	}
	return true, ctx.Err()
}

func matchesRenderSelectorContext(ctx context.Context, item yamlRenderValue, selector yamlSequenceSelector) (bool, error) {
	if selector.exact != nil {
		return equalYAMLRenderCanonicalAtKeyContext(ctx, item, *selector.exact, "")
	}
	if item.kind != yamlRenderMapping {
		return false, nil
	}
	for _, field := range selector.fields {
		actual, ok := yamlRenderMappingValue(item, field.Key)
		if !ok {
			return false, nil
		}
		equal, err := equalYAMLRenderCanonicalAtKeyContext(ctx, actual, field.Value, field.Key)
		if err != nil || !equal {
			return equal, err
		}
	}
	return true, ctx.Err()
}
