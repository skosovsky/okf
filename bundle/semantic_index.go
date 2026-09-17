package bundle

import (
	"context"
	"crypto/sha256"

	"gopkg.in/yaml.v3"
)

// RelationDiagnostics returns stable, defensive copies of semantic diagnostics.
func (b *Bundle) RelationDiagnostics() []RelationDiagnostic {
	out, _ := b.RelationDiagnosticsContext(context.Background())
	return out
}

// RelationDiagnosticsContext returns a cancellation-aware defensive copy.
func (b *Bundle) RelationDiagnosticsContext(ctx context.Context) ([]RelationDiagnostic, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b == nil {
		return nil, nil
	}
	var out []RelationDiagnostic
	for _, diagnostic := range b.diagnostics {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		owned, err := cloneRelationDiagnosticContext(ctx, diagnostic)
		if err != nil {
			return nil, err
		}
		out = append(out, owned)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// TargetExists reports whether a concept or an unambiguous explicit fragment exists.
func (b *Bundle) TargetExists(ref RelationRef) bool {
	exists, _ := b.targetExistsContext(context.Background(), ref)
	return exists
}

func (b *Bundle) targetExistsContext(ctx context.Context, ref RelationRef) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if b == nil {
		return false, ctx.Err()
	}
	id, err := conceptIDStringContext(ctx, ref.ID)
	if err != nil {
		return false, err
	}
	conceptPresent := false
	for candidate := range b.byID {
		equal, compareErr := equalStringContext(ctx, candidate, id)
		if compareErr != nil {
			return false, compareErr
		}
		if equal {
			conceptPresent = true
			break
		}
	}
	if !conceptPresent {
		return false, ctx.Err()
	}
	if ref.Fragment == "" {
		return true, ctx.Err()
	}
	fragment, err := stringFromStringContext(ctx, ref.Fragment)
	if err != nil {
		return false, err
	}
	var states map[string]fragmentState
	for candidate, value := range b.subresources {
		equal, compareErr := equalStringContext(ctx, candidate, id)
		if compareErr != nil {
			return false, compareErr
		}
		if equal {
			states = value
			break
		}
	}
	for candidate, state := range states {
		equal, compareErr := equalStringContext(ctx, candidate, fragment)
		if compareErr != nil {
			return false, compareErr
		}
		if equal {
			return state.count == 1, ctx.Err()
		}
	}
	return false, ctx.Err()
}

// FragmentExists reports whether fragment is an unambiguous explicit fragment
// of id. Aliases are deliberately not canonical relation targets.
func (b *Bundle) FragmentExists(id ConceptID, fragment string) bool {
	return b.TargetExists(RelationRef{ID: id, Fragment: fragment})
}

// Subresources returns canonical, unambiguous fragment names in lexical order.
func (b *Bundle) Subresources(id ConceptID) []string {
	out, _ := b.SubresourcesContext(context.Background(), id)
	return out
}

// SubresourcesContext returns canonical, unambiguous fragment names in lexical
// order with cancellation checks during projection.
func (b *Bundle) SubresourcesContext(ctx context.Context, id ConceptID) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b == nil {
		return nil, nil
	}
	idString, err := conceptIDStringContext(ctx, id)
	if err != nil {
		return nil, err
	}
	states, _, err := lookupStringMapContext(ctx, b.subresources, idString)
	if err != nil {
		return nil, err
	}
	if len(states) == 0 {
		return nil, nil
	}
	var out []string
	for name, state := range states {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if state.count == 1 {
			out = append(out, name)
		}
	}
	if err := sortCompareContext(ctx, out, compareStringsContext); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// SemanticLinksTo returns resolved incoming semantic relations for a target.
func (b *Bundle) SemanticLinksTo(target RelationRef) []Relation {
	if b == nil {
		return nil
	}
	relations := b.incoming[relationRefIdentity(target)]
	out := make([]Relation, len(relations))
	for index, relation := range relations {
		out[index] = cloneRelation(relation)
	}
	return out
}

// ReverseImpactConcept returns semantic relations impacted by changing a concept.
func (b *Bundle) ReverseImpactConcept(id ConceptID) []Relation {
	out, _ := b.ReverseImpactConceptContext(context.Background(), id)
	return out
}

// ReverseImpactConceptContext returns a cancellation-aware deterministic,
// defensive projection of relations impacted by changing a concept.
func (b *Bundle) ReverseImpactConceptContext(ctx context.Context, id ConceptID) ([]Relation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b == nil {
		return nil, nil
	}
	idString, err := conceptIDStringContext(ctx, id)
	if err != nil {
		return nil, err
	}
	if _, ok, err := lookupStringMapContext(ctx, b.byID, idString); err != nil {
		return nil, err
	} else if !ok {
		return nil, nil
	}
	// A concept move changes the meaning of both its root reference and every
	// canonical fragment reference. Collecting these keys here keeps callers
	// from accidentally overlooking fragment-targeted incoming relations.
	type seenEntry struct {
		key      relationProjectionKey
		relation Relation
	}
	seen := make(map[[sha256.Size]byte][]seenEntry)
	add := func(relations []Relation) error {
		for _, relation := range relations {
			if err := ctx.Err(); err != nil {
				return err
			}
			key, err := migrationSafeRelationProjectionKeyContext(ctx, relation)
			if err != nil {
				return err
			}
			owned, err := cloneRelationContext(ctx, relation)
			if err != nil {
				return err
			}
			bucket := seen[key.digest]
			replaced := false
			for index := range bucket {
				equal, err := equalRelationProjectionKeysContext(ctx, bucket[index].key, key)
				if err != nil {
					return err
				}
				if equal {
					bucket[index].relation = owned
					replaced = true
					break
				}
			}
			if !replaced {
				bucket = append(bucket, seenEntry{key: key, relation: owned})
			}
			seen[key.digest] = bucket
		}
		return nil
	}
	rootKey, err := relationRefIdentityContext(ctx, RelationRef{ID: id})
	if err != nil {
		return nil, err
	}
	rootRelations, _, err := lookupRelationRefMapContext(ctx, b.incoming, rootKey)
	if err != nil {
		return nil, err
	}
	if err := add(rootRelations); err != nil {
		return nil, err
	}
	fragments, err := b.SubresourcesContext(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, fragment := range fragments {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		key, err := relationRefIdentityContext(ctx, RelationRef{ID: id, Fragment: fragment})
		if err != nil {
			return nil, err
		}
		relations, _, err := lookupRelationRefMapContext(ctx, b.incoming, key)
		if err != nil {
			return nil, err
		}
		if err := add(relations); err != nil {
			return nil, err
		}
	}

	var out []Relation
	for _, bucket := range seen {
		for _, entry := range bucket {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			out = append(out, entry.relation)
		}
	}
	if err := sortCompareContext(ctx, out, compareRelationsContext); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ReverseImpactFragment returns semantic relations impacted by changing a fragment.
func (b *Bundle) ReverseImpactFragment(id ConceptID, fragment string) []Relation {
	out, _ := b.ReverseImpactFragmentContext(context.Background(), id, fragment)
	return out
}

// ReverseImpactFragmentContext returns a cancellation-aware defensive copy of
// relations impacted by changing one canonical fragment.
func (b *Bundle) ReverseImpactFragmentContext(ctx context.Context, id ConceptID, fragment string) ([]Relation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b == nil {
		return nil, nil
	}
	exists, err := b.targetExistsContext(ctx, RelationRef{ID: id, Fragment: fragment})
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	key, err := relationRefIdentityContext(ctx, RelationRef{ID: id, Fragment: fragment})
	if err != nil {
		return nil, err
	}
	relations, _, err := lookupRelationRefMapContext(ctx, b.incoming, key)
	if err != nil {
		return nil, err
	}
	if len(relations) == 0 {
		return nil, nil
	}
	var out []Relation
	for _, relation := range relations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		owned, err := cloneRelationContext(ctx, relation)
		if err != nil {
			return nil, err
		}
		out = append(out, owned)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

type relationProjectionKey struct {
	source, target relationRefKey
	typ            string
	digest         [sha256.Size]byte
}

func migrationSafeRelationProjectionKeyContext(ctx context.Context, relation Relation) (relationProjectionKey, error) {
	source, err := relationRefIdentityContext(ctx, relation.Source)
	if err != nil {
		return relationProjectionKey{}, err
	}
	target, err := relationRefIdentityContext(ctx, relation.Target)
	if err != nil {
		return relationProjectionKey{}, err
	}
	typ, err := stringFromStringContext(ctx, relation.Type)
	if err != nil {
		return relationProjectionKey{}, err
	}
	hash := sha256.New()
	for _, value := range []string{source.id, source.fragment, typ, target.id, target.fragment} {
		for offset := 0; offset < len(value); offset += 64 << 10 {
			if err := ctx.Err(); err != nil {
				return relationProjectionKey{}, err
			}
			_, _ = hash.Write([]byte(value[offset:min(offset+(64<<10), len(value))]))
		}
		_, _ = hash.Write([]byte{0})
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return relationProjectionKey{source: source, target: target, typ: typ, digest: digest}, ctx.Err()
}

func equalRelationProjectionKeysContext(ctx context.Context, left, right relationProjectionKey) (bool, error) {
	for _, pair := range [][2]string{{left.source.id, right.source.id}, {left.source.fragment, right.source.fragment}, {left.typ, right.typ}, {left.target.id, right.target.id}, {left.target.fragment, right.target.fragment}} {
		equal, err := equalStringContext(ctx, pair[0], pair[1])
		if err != nil || !equal {
			return equal, err
		}
	}
	return true, ctx.Err()
}

func lookupStringMapContext[V any](ctx context.Context, values map[string]V, key string) (V, bool, error) {
	var zero V
	for candidate, value := range values {
		equal, err := equalStringContext(ctx, candidate, key)
		if err != nil {
			return zero, false, err
		}
		if equal {
			return value, true, ctx.Err()
		}
	}
	return zero, false, ctx.Err()
}

func lookupRelationRefMapContext[V any](ctx context.Context, values map[relationRefKey]V, key relationRefKey) (V, bool, error) {
	var zero V
	for candidate, value := range values {
		equal, err := equalStringContext(ctx, candidate.id, key.id)
		if err != nil {
			return zero, false, err
		}
		if !equal {
			continue
		}
		equal, err = equalStringContext(ctx, candidate.fragment, key.fragment)
		if err != nil {
			return zero, false, err
		}
		if equal {
			return value, true, ctx.Err()
		}
	}
	return zero, false, ctx.Err()
}

func cloneRelation(relation Relation) Relation {
	owned, _ := cloneRelationContext(context.Background(), relation)
	return owned
}

func cloneRelationContext(ctx context.Context, relation Relation) (Relation, error) {
	var out Relation
	var err error
	if out.Source.ID, err = cloneConceptIDContext(ctx, relation.Source.ID); err != nil {
		return Relation{}, err
	}
	if out.Source.Fragment, err = stringFromStringContext(ctx, relation.Source.Fragment); err != nil {
		return Relation{}, err
	}
	if out.Type, err = stringFromStringContext(ctx, relation.Type); err != nil {
		return Relation{}, err
	}
	if out.Target.ID, err = cloneConceptIDContext(ctx, relation.Target.ID); err != nil {
		return Relation{}, err
	}
	if out.Target.Fragment, err = stringFromStringContext(ctx, relation.Target.Fragment); err != nil {
		return Relation{}, err
	}
	if out.RawTarget, err = stringFromStringContext(ctx, relation.RawTarget); err != nil {
		return Relation{}, err
	}
	out.TargetExists = relation.TargetExists
	return out, ctx.Err()
}

func cloneRelationDiagnosticContext(ctx context.Context, diagnostic RelationDiagnostic) (RelationDiagnostic, error) {
	var out RelationDiagnostic
	var err error
	if out.Code, err = stringFromStringContext(ctx, diagnostic.Code); err != nil {
		return RelationDiagnostic{}, err
	}
	if out.Source, err = cloneConceptIDContext(ctx, diagnostic.Source); err != nil {
		return RelationDiagnostic{}, err
	}
	if out.SourceFragment, err = stringFromStringContext(ctx, diagnostic.SourceFragment); err != nil {
		return RelationDiagnostic{}, err
	}
	if out.RelationType, err = stringFromStringContext(ctx, diagnostic.RelationType); err != nil {
		return RelationDiagnostic{}, err
	}
	if out.RawTarget, err = stringFromStringContext(ctx, diagnostic.RawTarget); err != nil {
		return RelationDiagnostic{}, err
	}
	if out.File, err = stringFromStringContext(ctx, diagnostic.File); err != nil {
		return RelationDiagnostic{}, err
	}
	if out.Message, err = stringFromStringContext(ctx, diagnostic.Message); err != nil {
		return RelationDiagnostic{}, err
	}
	out.Severity = diagnostic.Severity
	return out, ctx.Err()
}

func sortContext[T any](ctx context.Context, values []T, less func(left, right T) bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(values) < 2 {
		return ctx.Err()
	}

	// Grow the merge buffer incrementally so high-cardinality materialization
	// remains cancellable instead of hiding one large allocation behind a
	// single checkpoint.
	var scratch []T
	var zero T
	for range values {
		if err := ctx.Err(); err != nil {
			return err
		}
		scratch = append(scratch, zero)
	}

	source, destination := values, scratch
	for width := 1; width < len(values); width *= 2 {
		for start := 0; start < len(values); start += 2 * width {
			middle := min(start+width, len(values))
			end := min(start+2*width, len(values))
			left, right := start, middle
			for output := start; output < end; output++ {
				if err := ctx.Err(); err != nil {
					return err
				}
				switch {
				case left >= middle:
					destination[output] = source[right]
					right++
				case right >= end:
					destination[output] = source[left]
					left++
				case less(source[right], source[left]):
					destination[output] = source[right]
					right++
				default:
					// Choosing the left item on equality makes the sort stable.
					destination[output] = source[left]
					left++
				}
			}
		}
		source, destination = destination, source
		if width > len(values)/2 {
			break
		}
	}

	if len(source) > 0 && &source[0] != &values[0] {
		for index := range source {
			if err := ctx.Err(); err != nil {
				return err
			}
			values[index] = source[index]
		}
	}
	return ctx.Err()
}

func sortCompareContext[T any](ctx context.Context, values []T, compare func(context.Context, T, T) (int, error)) error {
	var compareErr error
	err := sortContext(ctx, values, func(left, right T) bool {
		if compareErr != nil {
			return false
		}
		var result int
		result, compareErr = compare(ctx, left, right)
		return result < 0
	})
	if compareErr != nil {
		return compareErr
	}
	return err
}

func compareRelationsContext(ctx context.Context, left, right Relation) (int, error) {
	result, err := compareRelationRefsContext(ctx, left.Source, right.Source)
	if err != nil || result != 0 {
		return result, err
	}
	result, err = compareStringsContext(ctx, left.Type, right.Type)
	if err != nil || result != 0 {
		return result, err
	}
	return compareRelationRefsContext(ctx, left.Target, right.Target)
}

func (b *Bundle) addRelationDiagnostic(code string, source RelationRef, typ, raw, file, message string) {
	b.addRelationDiagnosticWithSeverity(RelationDiagnosticError, code, source, typ, raw, file, message)
}

func (b *Bundle) addRelationDiagnosticWithSeverity(severity RelationDiagnosticSeverity, code string, source RelationRef, typ, raw, file, message string) {
	b.diagnostics = append(b.diagnostics, RelationDiagnostic{Code: code, Source: source.ID, SourceFragment: source.Fragment, RelationType: typ, RawTarget: raw, File: file, Message: message, Severity: severity})
}

func (b *Bundle) indexSubresourcesContext(ctx context.Context, concept Concept) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	rootValue := concept.Document.Frontmatter.mappingNode()
	root := &rootValue
	standardFamily, err := standardSemanticFamilyNodesContext(ctx, root)
	if err != nil {
		return err
	}
	states := make(map[string]fragmentState)
	addCanonical := func(fragment string) {
		state := states[fragment]
		state.count++
		state.canonical = true
		states[fragment] = state
	}
	invalidField := func(key string, value *yaml.Node) error {
		valid := false
		if value.Kind == yaml.ScalarNode && value.Tag == "!!str" {
			var err error
			valid, err = validRelationFragmentContext(ctx, value.Value)
			if err != nil {
				return err
			}
		}
		if valid {
			return ctx.Err()
		}
		b.addRelationDiagnostic("invalid_fragment", RelationRef{ID: concept.ID}, key, value.Value, concept.Path, key+" must be a valid string fragment")
		return ctx.Err()
	}
	var visit func(*yaml.Node) error
	visit = func(node *yaml.Node) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if node == nil {
			return nil
		}
		if _, excluded := standardFamily[node]; excluded {
			return nil
		}
		if node.Kind == yaml.MappingNode {
			idValues, err := mappingValuesContext(ctx, node, "id")
			if err != nil {
				return err
			}
			anchorValues, err := mappingValuesContext(ctx, node, "anchor")
			if err != nil {
				return err
			}
			for _, value := range idValues {
				if err := ctx.Err(); err != nil {
					return err
				}
				valid, err := validRelationFragmentContext(ctx, value.Value)
				if err != nil {
					return err
				}
				if value.Kind == yaml.ScalarNode && value.Tag == "!!str" && valid {
					continue
				}
				if err := invalidField("id", value); err != nil {
					return err
				}
			}
			for _, value := range anchorValues {
				if err := ctx.Err(); err != nil {
					return err
				}
				valid, err := validRelationFragmentContext(ctx, value.Value)
				if err != nil {
					return err
				}
				if value.Kind == yaml.ScalarNode && value.Tag == "!!str" && valid {
					continue
				}
				if err := invalidField("anchor", value); err != nil {
					return err
				}
			}
			identity, err := resolveMappingIdentityContext(ctx, node)
			if err != nil {
				return err
			}
			validIDs, err := validMappingFragmentsContext(ctx, idValues)
			if err != nil {
				return err
			}
			validAnchors, err := validMappingFragmentsContext(ctx, anchorValues)
			if err != nil {
				return err
			}
			if len(validIDs) > 1 {
				// A mapping cannot name two canonical ids. Record every occurrence,
				// but deliberately make each candidate ambiguous so relation targets
				// never inherit a first-wins identity.
				for _, id := range validIDs {
					if err := ctx.Err(); err != nil {
						return err
					}
					addCanonical(id.Value)
					addCanonical(id.Value)
					b.addRelationDiagnostic("ambiguous_fragment", RelationRef{ID: concept.ID, Fragment: id.Value}, "id", id.Value, concept.Path, "multiple id fields make the fragment ambiguous")
				}
			} else if identity.State == MappingIdentityValid && len(validIDs) == 1 {
				addCanonical(identity.Fragment)
				for _, anchor := range validAnchors {
					if err := ctx.Err(); err != nil {
						return err
					}
					equal, err := equalStringContext(ctx, anchor.Value, identity.Fragment)
					if err != nil {
						return err
					}
					if equal {
						continue
					}
					state := states[identity.Fragment]
					state.aliases = append(state.aliases, anchor.Value)
					states[identity.Fragment] = state
					b.addRelationDiagnosticWithSeverity(RelationDiagnosticInfo, "anchor_alias", RelationRef{ID: concept.ID, Fragment: identity.Fragment}, "", anchor.Value, concept.Path, "anchor is a non-canonical alias; id is the canonical fragment")
				}
			} else if len(validAnchors) > 1 {
				// Anchor is canonical only in the absence of id, so multiple anchors
				// have the same ambiguity rule as multiple ids.
				for _, anchor := range validAnchors {
					if err := ctx.Err(); err != nil {
						return err
					}
					addCanonical(anchor.Value)
					addCanonical(anchor.Value)
					b.addRelationDiagnostic("ambiguous_fragment", RelationRef{ID: concept.ID, Fragment: anchor.Value}, "anchor", anchor.Value, concept.Path, "multiple anchor fields make the fragment ambiguous")
				}
			} else if identity.State == MappingIdentityValid {
				addCanonical(identity.Fragment)
			}
			for _, child := range node.Content {
				if err := visit(child); err != nil {
					return err
				}
			}
			return nil
		}
		if node.Kind == yaml.SequenceNode {
			for _, child := range node.Content {
				if err := visit(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	// A document's frontmatter mapping describes the concept itself. Its id and
	// anchor are ordinary top-level metadata, not subresource identities. Only
	// mappings nested below it participate in the fragment namespace; this is
	// also the scope used by relation source extraction and mutation.
	for i := 0; i+1 < len(root.Content); i += 2 {
		if err := ctx.Err(); err != nil {
			return err
		}
		key, value := root.Content[i], root.Content[i+1]
		if key != nil && key.Kind == yaml.ScalarNode && key.Tag == "!!str" {
			standardKey, err := isStandardFrontmatterKeyContext(ctx, key.Value)
			if err != nil {
				return err
			}
			if standardKey {
				continue
			}
		}
		if err := visit(value); err != nil {
			return err
		}
	}
	for fragment, state := range states {
		if err := ctx.Err(); err != nil {
			return err
		}
		if state.count > 1 {
			b.addRelationDiagnostic("duplicate_fragment", RelationRef{ID: concept.ID, Fragment: fragment}, "", fragment, concept.Path, "duplicate fragment is ambiguous")
		}
	}
	id, err := conceptIDStringContext(ctx, concept.ID)
	if err != nil {
		return err
	}
	b.subresources[id] = states
	return ctx.Err()
}

func sortRelationsContext(ctx context.Context, relations []Relation) error {
	return sortCompareContext(ctx, relations, compareRelationsContext)
}

func sortRelationObservationsContext(ctx context.Context, observations []RelationObservation) error {
	return sortCompareContext(ctx, observations, func(ctx context.Context, left, right RelationObservation) (int, error) {
		return compareRelationsContext(ctx, Relation{Source: left.Source, Type: left.Type, Target: left.Target}, Relation{Source: right.Source, Type: right.Type, Target: right.Target})
	})
}

func (b *Bundle) finalizeRelationDiagnosticsContext(ctx context.Context) error {
	return sortCompareContext(ctx, b.diagnostics, func(ctx context.Context, a, c RelationDiagnostic) (int, error) {
		if result, err := compareStringsContext(ctx, a.File, c.File); err != nil || result != 0 {
			return result, err
		}
		if result, err := compareRelationRefsContext(ctx,
			RelationRef{ID: a.Source, Fragment: a.SourceFragment},
			RelationRef{ID: c.Source, Fragment: c.SourceFragment},
		); err != nil || result != 0 {
			return result, err
		}
		if result, err := compareStringsContext(ctx, a.RelationType, c.RelationType); err != nil || result != 0 {
			return result, err
		}
		if result, err := compareStringsContext(ctx, a.RawTarget, c.RawTarget); err != nil || result != 0 {
			return result, err
		}
		return compareStringsContext(ctx, a.Code, c.Code)
	})
}
