package bundle

import (
	"gopkg.in/yaml.v3"
	"sort"
)

// RelationDiagnostics returns stable, defensive copies of semantic diagnostics.
func (b *Bundle) RelationDiagnostics() []RelationDiagnostic {
	if b == nil {
		return nil
	}
	return append([]RelationDiagnostic(nil), b.diagnostics...)
}

// TargetExists reports whether a concept or an unambiguous explicit fragment exists.
func (b *Bundle) TargetExists(ref RelationRef) bool {
	if b == nil || !b.Contains(ref.ID) {
		return false
	}
	if ref.Fragment == "" {
		return true
	}
	state, ok := b.subresources[ref.ID.String()][ref.Fragment]
	return ok && state.count == 1
}

// FragmentExists reports whether fragment is an unambiguous explicit fragment
// of id. Aliases are deliberately not canonical relation targets.
func (b *Bundle) FragmentExists(id ConceptID, fragment string) bool {
	return b.TargetExists(RelationRef{ID: id, Fragment: fragment})
}

// Subresources returns canonical, unambiguous fragment names in lexical order.
func (b *Bundle) Subresources(id ConceptID) []string {
	if b == nil {
		return nil
	}
	states := b.subresources[id.String()]
	out := make([]string, 0, len(states))
	for name, state := range states {
		if state.count == 1 {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// SemanticLinksTo returns resolved incoming semantic relations for a target.
func (b *Bundle) SemanticLinksTo(target RelationRef) []Relation {
	if b == nil {
		return nil
	}
	return append([]Relation(nil), b.incoming[target.String()]...)
}

// ReverseImpactConcept returns semantic relations impacted by changing a concept.
func (b *Bundle) ReverseImpactConcept(id ConceptID) []Relation {
	if b == nil {
		return nil
	}

	// A concept move changes the meaning of both its root reference and every
	// canonical fragment reference. Collecting these keys here keeps callers
	// from accidentally overlooking fragment-targeted incoming relations.
	seen := make(map[string]Relation)
	add := func(relations []Relation) {
		for _, relation := range relations {
			key := relation.Source.String() + "\x00" + relation.Type + "\x00" + relation.Target.String()
			seen[key] = relation
		}
	}
	add(b.SemanticLinksTo(RelationRef{ID: id}))
	for _, fragment := range b.Subresources(id) {
		add(b.SemanticLinksTo(RelationRef{ID: id, Fragment: fragment}))
	}

	out := make([]Relation, 0, len(seen))
	for _, relation := range seen {
		out = append(out, relation)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Source.String() != out[j].Source.String() {
			return out[i].Source.String() < out[j].Source.String()
		}
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].Target.String() < out[j].Target.String()
	})
	return out
}

// ReverseImpactFragment returns semantic relations impacted by changing a fragment.
func (b *Bundle) ReverseImpactFragment(id ConceptID, fragment string) []Relation {
	return b.SemanticLinksTo(RelationRef{ID: id, Fragment: fragment})
}

func (b *Bundle) addRelationDiagnostic(code string, source RelationRef, typ, raw, file, message string) {
	b.addRelationDiagnosticWithSeverity(RelationDiagnosticError, code, source, typ, raw, file, message)
}

func (b *Bundle) addRelationDiagnosticWithSeverity(severity RelationDiagnosticSeverity, code string, source RelationRef, typ, raw, file, message string) {
	b.diagnostics = append(b.diagnostics, RelationDiagnostic{Code: code, Source: source.ID, SourceFragment: source.Fragment, RelationType: typ, RawTarget: raw, File: file, Message: message, Severity: severity})
}

func (b *Bundle) indexSubresources(concept Concept) {
	root := concept.Document.Frontmatter.YAMLNode()
	if root == nil {
		return
	}
	states := make(map[string]fragmentState)
	addCanonical := func(fragment string) {
		state := states[fragment]
		state.count++
		state.canonical = true
		states[fragment] = state
	}
	invalidField := func(key string, value *yaml.Node) {
		if value.Kind == yaml.ScalarNode && value.Tag == "!!str" && validRelationFragment(value.Value) {
			return
		}
		b.addRelationDiagnostic("invalid_fragment", RelationRef{ID: concept.ID}, key, value.Value, concept.Path, key+" must be a valid string fragment")
	}
	var visit func(*yaml.Node)
	visit = func(node *yaml.Node) {
		if node == nil {
			return
		}
		if node.Kind == yaml.MappingNode {
			idValues := mappingValues(node, "id")
			anchorValues := mappingValues(node, "anchor")
			for _, value := range idValues {
				if value.Kind == yaml.ScalarNode && value.Tag == "!!str" && validRelationFragment(value.Value) {
					continue
				}
				invalidField("id", value)
			}
			for _, value := range anchorValues {
				if value.Kind == yaml.ScalarNode && value.Tag == "!!str" && validRelationFragment(value.Value) {
					continue
				}
				invalidField("anchor", value)
			}
			identity := ResolveMappingIdentity(node)
			validIDs, validAnchors := validMappingFragments(idValues), validMappingFragments(anchorValues)
			if len(validIDs) > 1 {
				// A mapping cannot name two canonical ids. Record every occurrence,
				// but deliberately make each candidate ambiguous so relation targets
				// never inherit a first-wins identity.
				for _, id := range validIDs {
					addCanonical(id.Value)
					addCanonical(id.Value)
					b.addRelationDiagnostic("ambiguous_fragment", RelationRef{ID: concept.ID, Fragment: id.Value}, "id", id.Value, concept.Path, "multiple id fields make the fragment ambiguous")
				}
			} else if identity.State == MappingIdentityValid && len(validIDs) == 1 {
				addCanonical(identity.Fragment)
				for _, anchor := range validAnchors {
					if anchor.Value == identity.Fragment {
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
					addCanonical(anchor.Value)
					addCanonical(anchor.Value)
					b.addRelationDiagnostic("ambiguous_fragment", RelationRef{ID: concept.ID, Fragment: anchor.Value}, "anchor", anchor.Value, concept.Path, "multiple anchor fields make the fragment ambiguous")
				}
			} else if identity.State == MappingIdentityValid {
				addCanonical(identity.Fragment)
			}
			for _, child := range node.Content {
				visit(child)
			}
			return
		}
		if node.Kind == yaml.SequenceNode {
			for _, child := range node.Content {
				visit(child)
			}
		}
	}
	// A document's frontmatter mapping describes the concept itself. Its id and
	// anchor are ordinary top-level metadata, not subresource identities. Only
	// mappings nested below it participate in the fragment namespace; this is
	// also the scope used by relation source extraction and mutation.
	for i := 1; i < len(root.Content); i += 2 {
		visit(root.Content[i])
	}
	for fragment, state := range states {
		if state.count > 1 {
			b.addRelationDiagnostic("duplicate_fragment", RelationRef{ID: concept.ID, Fragment: fragment}, "", fragment, concept.Path, "duplicate fragment is ambiguous")
		}
	}
	b.subresources[concept.ID.String()] = states
}

func sortRelations(relations []Relation) {
	sort.SliceStable(relations, func(i, j int) bool {
		if relations[i].Source.String() != relations[j].Source.String() {
			return relations[i].Source.String() < relations[j].Source.String()
		}
		if relations[i].Type != relations[j].Type {
			return relations[i].Type < relations[j].Type
		}
		return relations[i].Target.String() < relations[j].Target.String()
	})
}

func (b *Bundle) finalizeRelationDiagnostics() {
	sort.SliceStable(b.diagnostics, func(i, j int) bool {
		a, c := b.diagnostics[i], b.diagnostics[j]
		if a.File != c.File {
			return a.File < c.File
		}
		if a.Source.String() != c.Source.String() {
			return a.Source.String() < c.Source.String()
		}
		if a.RelationType != c.RelationType {
			return a.RelationType < c.RelationType
		}
		if a.RawTarget != c.RawTarget {
			return a.RawTarget < c.RawTarget
		}
		return a.Code < c.Code
	})
}
