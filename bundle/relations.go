package bundle

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// RelationRef identifies a concept or one of its semantic subresources.
type RelationRef struct {
	ID       ConceptID
	Fragment string
}

// String returns the concept id, optionally suffixed with its fragment.
func (r RelationRef) String() string {
	if r.Fragment == "" {
		return r.ID.String()
	}
	return r.ID.String() + "#" + r.Fragment
}

// Relation is a resolved typed semantic edge from YAML frontmatter relations.
// Both its source and target identify extant, unambiguous bundle resources.
type Relation struct {
	Source       RelationRef
	Type         string
	Target       RelationRef
	TargetExists bool
	RawTarget    string
}

// RelationDiagnosticSeverity determines whether a semantic diagnostic blocks
// a mutation plan.
type RelationDiagnosticSeverity string

const (
	// RelationDiagnosticError marks malformed or unresolved semantic data.
	// Such diagnostics block mutation planning.
	RelationDiagnosticError RelationDiagnosticSeverity = "error"
	// RelationDiagnosticInfo marks non-canonical but understood semantic data.
	// Informational diagnostics never block mutation planning.
	RelationDiagnosticInfo RelationDiagnosticSeverity = "info"
)

// RelationDiagnostic describes semantic data that is either invalid or
// informational. The zero severity is an error for safe compatibility with
// diagnostics constructed by callers.
type RelationDiagnostic struct {
	Code                                                   string
	Source                                                 ConceptID
	SourceFragment, RelationType, RawTarget, File, Message string
	Severity                                               RelationDiagnosticSeverity
}

// BlocksMutation reports whether this diagnostic makes a staged bundle unsafe
// to mutate.
func (d RelationDiagnostic) BlocksMutation() bool {
	return d.Severity != RelationDiagnosticInfo
}

type fragmentState struct {
	canonical bool
	aliases   []string
	count     int
}

// MappingIdentityState describes whether a YAML mapping establishes a usable
// local semantic identity. Invalid occurrences do not invalidate a unique
// valid fallback, but two valid candidates of the canonical kind do.
type MappingIdentityState uint8

const (
	MappingIdentityAbsent MappingIdentityState = iota
	MappingIdentityValid
	MappingIdentityInvalid
)

// MappingIdentity is the canonical identity selected from one YAML mapping.
// Node is the scalar that holds Fragment, which lets presentation consumers
// change the same canonical occurrence used by semantic resolution.
type MappingIdentity struct {
	Fragment string
	Node     *yaml.Node
	State    MappingIdentityState
}

// ResolveMappingIdentity applies the bundle-wide id/anchor policy. A unique
// valid id is canonical; otherwise a unique valid anchor is canonical. The
// presence of identity keys with no such candidate is invalid rather than
// inheritable, while a mapping with no identity keys is absent.
func ResolveMappingIdentity(node *yaml.Node) MappingIdentity {
	ids, anchors := mappingValues(node, "id"), mappingValues(node, "anchor")
	if len(ids) == 0 && len(anchors) == 0 {
		return MappingIdentity{State: MappingIdentityAbsent}
	}
	validIDs := validMappingFragments(ids)
	if len(validIDs) > 1 {
		return MappingIdentity{State: MappingIdentityInvalid}
	}
	if len(validIDs) == 1 {
		return MappingIdentity{Fragment: validIDs[0].Value, Node: validIDs[0], State: MappingIdentityValid}
	}
	validAnchors := validMappingFragments(anchors)
	if len(validAnchors) > 1 {
		return MappingIdentity{State: MappingIdentityInvalid}
	}
	if len(validAnchors) == 1 {
		return MappingIdentity{Fragment: validAnchors[0].Value, Node: validAnchors[0], State: MappingIdentityValid}
	}
	return MappingIdentity{State: MappingIdentityInvalid}
}

func validMappingFragments(values []*yaml.Node) []*yaml.Node {
	valid := make([]*yaml.Node, 0, len(values))
	for _, value := range values {
		if value != nil && value.Kind == yaml.ScalarNode && value.Tag == "!!str" && validRelationFragment(value.Value) {
			valid = append(valid, value)
		}
	}
	return valid
}

var reservedRelationTypes = map[string]struct{}{
	"bundle":      {},
	"description": {},
	"exists":      {},
	"is_part_of":  {},
	"okf":         {},
	"references":  {},
	"resource":    {},
	"tags":        {},
	"target":      {},
	"timestamp":   {},
	"title":       {},
	"type":        {},
}

func extractSemanticRelations(concept Concept, bundle *Bundle) []Relation {
	root := concept.Document.Frontmatter.YAMLNode()
	if root == nil || root.Kind != yaml.MappingNode {
		return nil
	}
	source := RelationRef{ID: concept.ID}
	var relations []Relation
	traverseRelationNode(root, source, false, true, concept.Path, bundle, &relations)
	return relations
}

// traverseRelationNode finds relation blocks anywhere in frontmatter. An
// explicit id (or anchor) establishes the source for its descendants; this is
// deliberately carried into metadata and relation items so nested blocks keep
// their nearest canonical source. The document source is only valid for the
// top-level relations block.
func traverseRelationNode(node *yaml.Node, source RelationRef, hasNestedSource, topLevel bool, file string, bundle *Bundle, out *[]Relation) {
	if node == nil {
		return
	}

	switch node.Kind {
	case yaml.MappingNode:
		currentSource, currentHasNestedSource := source, hasNestedSource
		if !topLevel {
			switch identity := ResolveMappingIdentity(node); identity.State {
			case MappingIdentityValid:
				currentSource, currentHasNestedSource = RelationRef{ID: source.ID, Fragment: identity.Fragment}, true
			case MappingIdentityInvalid:
				bundle.addRelationDiagnostic("invalid_source", source, "", "", file, "nested relation source has invalid or ambiguous id or anchor")
				currentSource, currentHasNestedSource = RelationRef{}, false
			}
		}
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i]
			value := node.Content[i+1]
			if isStringKey(key, "relations") {
				relationSource, ok := currentSource, topLevel || currentHasNestedSource
				if !ok {
					// Identity-bearing mappings are diagnosed when their state
					// clears inherited source. Identity-free mappings need a
					// diagnostic at this relation block.
					if !hasIdentityKeys(node) {
						bundle.addRelationDiagnostic("invalid_source", source, "", "", file, "nested relation source requires a valid id or anchor")
					}
					// Keep shape diagnostics anchored to the nearest known
					// source while publishEdges remains false.
					relationSource = source
				}
				// Diagnostics describe the malformed block even when its mapping
				// cannot establish a source. Keep the nearest known concept/ref as
				// diagnostic context, but do not publish edges without a source.
				appendRelationsFromBlock(value, relationSource, ok, file, bundle, out)
				// A relation item may itself carry semantic metadata. Continue
				// through the block after recording its outer relations, rather
				// than treating that block as a terminal node.
				traverseRelationNode(value, currentSource, currentHasNestedSource, false, file, bundle, out)
				continue
			}
			traverseRelationNode(value, currentSource, currentHasNestedSource, false, file, bundle, out)
		}
	case yaml.SequenceNode:
		for _, item := range node.Content {
			traverseRelationNode(item, source, hasNestedSource, false, file, bundle, out)
		}
	}
}

func hasIdentityKeys(node *yaml.Node) bool {
	return len(mappingValues(node, "id")) != 0 || len(mappingValues(node, "anchor")) != 0
}

func appendRelationsFromBlock(node *yaml.Node, source RelationRef, publishEdges bool, file string, bundle *Bundle, out *[]Relation) {
	if node == nil || node.Kind != yaml.MappingNode {
		bundle.addRelationDiagnostic("relations_not_mapping", source, "", "", file, "relations must be a mapping")
		return
	}

	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i]
		value := node.Content[i+1]
		// YAML merge provenance has no explicit relation-type owner in this
		// mapping. Mutation handles a merge only when the operation actually
		// touches a relation inherited through it; unrelated merges stay opaque.
		if key != nil && key.Tag == "!!merge" {
			continue
		}
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || !validRelationType(key.Value) {
			typ := ""
			if key.Kind == yaml.ScalarNode {
				typ = key.Value
			}
			bundle.addRelationDiagnostic("invalid_relation_type", source, typ, "", file, "relation type is invalid or reserved")
			continue
		}
		if value.Kind != yaml.SequenceNode {
			bundle.addRelationDiagnostic("relation_not_sequence", source, key.Value, "", file, "relation value must be a sequence")
			continue
		}
		if len(mappingValues(node, key.Value)) > 1 {
			bundle.addRelationDiagnostic("ambiguous_relation_type", source, key.Value, "", file, "duplicate relation type keys are ambiguous")
			continue
		}
		for _, item := range value.Content {
			if item == nil || item.Kind != yaml.MappingNode {
				bundle.addRelationDiagnostic("relation_item_not_mapping", source, key.Value, "", file, "relation item must be a mapping")
				continue
			}
			targetNodes := mappingValues(item, "target")
			if len(targetNodes) == 0 {
				bundle.addRelationDiagnostic("missing_target", source, key.Value, "", file, "relation item requires target")
				continue
			}
			if len(targetNodes) > 1 {
				bundle.addRelationDiagnostic("ambiguous_target", source, key.Value, "", file, "duplicate target keys are ambiguous")
				continue
			}
			for _, targetNode := range targetNodes {
				if targetNode.Kind != yaml.ScalarNode || targetNode.Tag != "!!str" {
					bundle.addRelationDiagnostic("target_not_string", source, key.Value, targetNode.Value, file, "relation target must be a string")
					continue
				}
				targetRaw := targetNode.Value
				if targetRaw == "" {
					bundle.addRelationDiagnostic("empty_target", source, key.Value, targetRaw, file, "relation target must not be empty")
					continue
				}
				target, err := ParseRelationRef(targetRaw)
				if err != nil {
					bundle.addRelationDiagnostic("invalid_target_ref", source, key.Value, targetRaw, file, err.Error())
					continue
				}
				exists := bundle.TargetExists(target)
				if !exists {
					code, message := "missing_target_concept", "target concept does not exist"
					if bundle.Contains(target.ID) && target.Fragment != "" {
						code, message = "missing_or_ambiguous_target_fragment", "target fragment does not exist or is ambiguous"
					}
					bundle.addRelationDiagnostic(code, source, key.Value, targetRaw, file, message)
				}
				// SemanticLinksFrom and the reverse index are resolved graph views.
				// Keep unresolved observations as diagnostics only: publishing them as
				// edges makes graph consumers invent dangling resources.
				if publishEdges && exists {
					*out = append(*out, Relation{Source: source, Type: key.Value, Target: target, TargetExists: true, RawTarget: targetRaw})
				}
			}
		}
	}
}

func mappingValues(node *yaml.Node, key string) []*yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	var values []*yaml.Node
	for i := 0; i+1 < len(node.Content); i += 2 {
		if isStringKey(node.Content[i], key) {
			values = append(values, node.Content[i+1])
		}
	}
	return values
}

func isStringKey(node *yaml.Node, value string) bool {
	return node != nil && node.Kind == yaml.ScalarNode && node.Tag == "!!str" && node.Value == value
}

// ParseRelationRef parses only canonical <concept-id>[#<fragment>] references.
func ParseRelationRef(raw string) (RelationRef, error) {
	if raw == "" || strings.TrimSpace(raw) != raw || strings.Count(raw, "#") > 1 {
		return RelationRef{}, fmt.Errorf("%w: %q", ErrInvalidRelationRef, raw)
	}

	conceptPart := raw
	fragment := ""
	if before, after, found := strings.Cut(raw, "#"); found {
		conceptPart = before
		fragment = after
		if err := ValidateRelationFragment(fragment); err != nil {
			return RelationRef{}, fmt.Errorf("%w: invalid fragment", ErrInvalidRelationRef)
		}
	}
	if !validRelationConceptPart(conceptPart) {
		return RelationRef{}, fmt.Errorf("%w: invalid concept", ErrInvalidRelationRef)
	}

	id, err := ParseConceptID(conceptPart)
	if err != nil || id.String() != conceptPart {
		return RelationRef{}, fmt.Errorf("%w: non-canonical concept", ErrInvalidRelationRef)
	}
	ref := RelationRef{ID: id, Fragment: fragment}
	if err := ValidateRelationRef(ref); err != nil {
		return RelationRef{}, err
	}
	return ref, nil
}

// ValidateRelationRef validates a constructed canonical relation reference.
func ValidateRelationRef(ref RelationRef) error {
	if err := ValidateConceptID(ref.ID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRelationRef, err)
	}
	if ref.Fragment != "" {
		if err := ValidateRelationFragment(ref.Fragment); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidRelationRef, err)
		}
	}
	return nil
}

func validRelationConceptPart(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || strings.HasSuffix(value, ".md") {
		return false
	}
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, "./") || strings.HasPrefix(value, "../") {
		return false
	}
	return !strings.HasPrefix(value, "//") && !hasURIScheme(value)
}

func validRelationFragment(value string) bool {
	return ValidateRelationFragment(value) == nil
}

func validRelationType(value string) bool {
	return ValidateRelationType(value) == nil
}

// ValidateRelationFragment validates one canonical relation fragment.
func ValidateRelationFragment(value string) error {
	if !utf8.ValidString(value) || value == "" || strings.TrimSpace(value) != value || strings.Contains(value, "#") {
		return fmt.Errorf("%w: %q", ErrInvalidRelationFragment, value)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: %q", ErrInvalidRelationFragment, value)
		}
	}
	return nil
}

// ValidateRelationType validates one canonical, non-reserved relation type.
func ValidateRelationType(value string) error {
	if value == "" {
		return fmt.Errorf("%w: empty", ErrInvalidRelationType)
	}
	if _, reserved := reservedRelationTypes[value]; reserved {
		return fmt.Errorf("%w: reserved %q", ErrInvalidRelationType, value)
	}
	for i, r := range value {
		switch {
		case i == 0 && !asciiLetter(r):
			return fmt.Errorf("%w: %q", ErrInvalidRelationType, value)
		case i > 0 && !(asciiLetter(r) || asciiDigit(r) || r == '_'):
			return fmt.Errorf("%w: %q", ErrInvalidRelationType, value)
		}
	}
	return nil
}

func asciiLetter(r rune) bool {
	return r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z'
}

func asciiDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

func hasURIScheme(value string) bool {
	colon := strings.Index(value, ":")
	if colon <= 0 {
		return false
	}
	for i, r := range value[:colon] {
		switch {
		case i == 0 && !asciiLetter(r):
			return false
		case i > 0 && !(asciiLetter(r) || asciiDigit(r) || r == '+' || r == '.' || r == '-'):
			return false
		}
	}
	return true
}
