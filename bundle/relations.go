package bundle

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"unicode"
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
	value, _ := r.StringContext(context.Background())
	return value
}

// StringContext returns the escaped semantic identity with cancellation.
func (r RelationRef) StringContext(ctx context.Context) (string, error) {
	return relationRefStringContext(ctx, r)
}

type relationRefKey struct {
	id       string
	fragment string
}

func relationRefIdentity(ref RelationRef) relationRefKey {
	return relationRefKey{id: ref.ID.String(), fragment: ref.Fragment}
}

func compareRelationRefs(left, right RelationRef) int {
	if result := strings.Compare(left.ID.String(), right.ID.String()); result != 0 {
		return result
	}
	return strings.Compare(left.Fragment, right.Fragment)
}

func compareStringsContext(ctx context.Context, left, right string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	const chunk = 64 << 10
	limit := min(len(left), len(right))
	for offset := 0; offset < limit; offset += chunk {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		end := min(offset+chunk, limit)
		if result := bytes.Compare([]byte(left[offset:end]), []byte(right[offset:end])); result != 0 {
			return result, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	switch {
	case len(left) < len(right):
		return -1, nil
	case len(left) > len(right):
		return 1, nil
	default:
		return 0, nil
	}
}

func conceptIDStringContext(ctx context.Context, id ConceptID) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var out strings.Builder
	for index, segment := range id.segments {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if index != 0 {
			out.WriteByte('/')
		}
		for offset := 0; offset < len(segment); offset += 64 << 10 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			out.WriteString(segment[offset:min(offset+(64<<10), len(segment))])
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return out.String(), nil
}

func relationRefIdentityContext(ctx context.Context, ref RelationRef) (relationRefKey, error) {
	id, err := conceptIDStringContext(ctx, ref.ID)
	if err != nil {
		return relationRefKey{}, err
	}
	fragment, err := stringFromStringContext(ctx, ref.Fragment)
	if err != nil {
		return relationRefKey{}, err
	}
	return relationRefKey{id: id, fragment: fragment}, ctx.Err()
}

func compareRelationRefsContext(ctx context.Context, left, right RelationRef) (int, error) {
	leftID, err := conceptIDStringContext(ctx, left.ID)
	if err != nil {
		return 0, err
	}
	rightID, err := conceptIDStringContext(ctx, right.ID)
	if err != nil {
		return 0, err
	}
	result, err := compareStringsContext(ctx, leftID, rightID)
	if err != nil || result != 0 {
		return result, err
	}
	return compareStringsContext(ctx, left.Fragment, right.Fragment)
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

// RelationObservation is one syntactically valid declared semantic relation.
// Unlike Relation, it retains unresolved targets with TargetExists false.
type RelationObservation struct {
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
	"attester":     {},
	"bundle":       {},
	"computation":  {},
	"description":  {},
	"executor":     {},
	"exists":       {},
	"generated":    {},
	"is_part_of":   {},
	"okf":          {},
	"okf_version":  {},
	"parameters":   {},
	"references":   {},
	"resource":     {},
	"runtime":      {},
	"sources":      {},
	"stale_after":  {},
	"status":       {},
	"tags":         {},
	"target":       {},
	"timestamp":    {},
	"title":        {},
	"type":         {},
	"usage_window": {},
	"verified":     {},
}

func extractSemanticRelations(concept Concept, bundle *Bundle) ([]Relation, []RelationObservation) {
	relations, observations, _ := extractSemanticRelationsContext(context.Background(), concept, bundle)
	return relations, observations
}

func extractSemanticRelationsContext(ctx context.Context, concept Concept, bundle *Bundle) ([]Relation, []RelationObservation, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	rootValue := concept.Document.Frontmatter.mappingNode()
	root := &rootValue
	source := RelationRef{ID: concept.ID}
	var relations []Relation
	var observations []RelationObservation
	standardFamily, err := standardSemanticFamilyNodesContext(ctx, root)
	if err != nil {
		return nil, nil, err
	}
	if err := traverseRelationNodeContext(ctx, root, source, false, true, standardFamily, concept.Path, bundle, &relations, &observations); err != nil {
		return nil, nil, err
	}
	return relations, observations, ctx.Err()
}

// traverseRelationNode finds relation blocks anywhere in frontmatter. An
// explicit id (or anchor) establishes the source for its descendants; this is
// deliberately carried into metadata and relation items so nested blocks keep
// their nearest canonical source. The document source is only valid for the
// top-level relations block.
func traverseRelationNodeContext(ctx context.Context, node *yaml.Node, source RelationRef, hasNestedSource, topLevel bool, standardFamily map[*yaml.Node]struct{}, file string, bundle *Bundle, out *[]Relation, observations *[]RelationObservation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if node == nil {
		return nil
	}
	if _, excluded := standardFamily[node]; excluded {
		return nil
	}

	switch node.Kind {
	case yaml.MappingNode:
		currentSource, currentHasNestedSource := source, hasNestedSource
		if !topLevel {
			identity, err := resolveMappingIdentityContext(ctx, node)
			if err != nil {
				return err
			}
			switch identity.State {
			case MappingIdentityValid:
				currentSource, currentHasNestedSource = RelationRef{ID: source.ID, Fragment: identity.Fragment}, true
			case MappingIdentityInvalid:
				bundle.addRelationDiagnostic("invalid_source", source, "", "", file, "nested relation source has invalid or ambiguous id or anchor")
				currentSource, currentHasNestedSource = RelationRef{}, false
			}
		}
		for i := 0; i+1 < len(node.Content); i += 2 {
			if err := ctx.Err(); err != nil {
				return err
			}
			key := node.Content[i]
			value := node.Content[i+1]
			if topLevel && key != nil && key.Kind == yaml.ScalarNode && key.Tag == "!!str" {
				standardKey, err := isStandardFrontmatterKeyContext(ctx, key.Value)
				if err != nil {
					return err
				}
				if standardKey {
					continue
				}
			}
			relationsKey, err := isStringKeyContext(ctx, key, "relations")
			if err != nil {
				return err
			}
			if relationsKey {
				relationSource, ok := currentSource, topLevel || currentHasNestedSource
				if !ok {
					// Identity-bearing mappings are diagnosed when their state
					// clears inherited source. Identity-free mappings need a
					// diagnostic at this relation block.
					hasIdentity, err := hasIdentityKeysContext(ctx, node)
					if err != nil {
						return err
					}
					if !hasIdentity {
						bundle.addRelationDiagnostic("invalid_source", source, "", "", file, "nested relation source requires a valid id or anchor")
					}
					// Keep shape diagnostics anchored to the nearest known
					// source while publishEdges remains false.
					relationSource = source
				}
				// Diagnostics describe the malformed block even when its mapping
				// cannot establish a source. Keep the nearest known concept/ref as
				// diagnostic context, but do not publish edges without a source.
				if err := appendRelationsFromBlockContext(ctx, value, relationSource, ok, file, bundle, out, observations); err != nil {
					return err
				}
				// A relation item may itself carry semantic metadata. Continue
				// through the block after recording its outer relations, rather
				// than treating that block as a terminal node.
				if err := traverseRelationNodeContext(ctx, value, currentSource, currentHasNestedSource, false, standardFamily, file, bundle, out, observations); err != nil {
					return err
				}
				continue
			}
			if err := traverseRelationNodeContext(ctx, value, currentSource, currentHasNestedSource, false, standardFamily, file, bundle, out, observations); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for _, item := range node.Content {
			if err := traverseRelationNodeContext(ctx, item, source, hasNestedSource, false, standardFamily, file, bundle, out, observations); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

func hasIdentityKeysContext(ctx context.Context, node *yaml.Node) (bool, error) {
	ids, err := mappingValuesContext(ctx, node, "id")
	if err != nil {
		return false, err
	}
	anchors, err := mappingValuesContext(ctx, node, "anchor")
	if err != nil {
		return false, err
	}
	return len(ids) != 0 || len(anchors) != 0, ctx.Err()
}

func appendRelationsFromBlockContext(ctx context.Context, node *yaml.Node, source RelationRef, publishEdges bool, file string, bundle *Bundle, out *[]Relation, observations *[]RelationObservation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if node == nil || node.Kind != yaml.MappingNode {
		bundle.addRelationDiagnostic("relations_not_mapping", source, "", "", file, "relations must be a mapping")
		return ctx.Err()
	}

	for i := 0; i+1 < len(node.Content); i += 2 {
		if err := ctx.Err(); err != nil {
			return err
		}
		key := node.Content[i]
		value := node.Content[i+1]
		// YAML merge provenance has no explicit relation-type owner in this
		// mapping. Mutation handles a merge only when the operation actually
		// touches a relation inherited through it; unrelated merges stay opaque.
		if key != nil && key.Tag == "!!merge" {
			continue
		}
		validType, err := validRelationTypeContext(ctx, key.Value)
		if err != nil {
			return err
		}
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || !validType {
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
		values, err := mappingValuesContext(ctx, node, key.Value)
		if err != nil {
			return err
		}
		if len(values) > 1 {
			bundle.addRelationDiagnostic("ambiguous_relation_type", source, key.Value, "", file, "duplicate relation type keys are ambiguous")
			continue
		}
		for _, item := range value.Content {
			if err := ctx.Err(); err != nil {
				return err
			}
			if item == nil || item.Kind != yaml.MappingNode {
				bundle.addRelationDiagnostic("relation_item_not_mapping", source, key.Value, "", file, "relation item must be a mapping")
				continue
			}
			targetNodes, err := mappingValuesContext(ctx, item, "target")
			if err != nil {
				return err
			}
			if len(targetNodes) == 0 {
				bundle.addRelationDiagnostic("missing_target", source, key.Value, "", file, "relation item requires target")
				continue
			}
			if len(targetNodes) > 1 {
				bundle.addRelationDiagnostic("ambiguous_target", source, key.Value, "", file, "duplicate target keys are ambiguous")
				continue
			}
			for _, targetNode := range targetNodes {
				if err := ctx.Err(); err != nil {
					return err
				}
				if targetNode.Kind != yaml.ScalarNode || targetNode.Tag != "!!str" {
					bundle.addRelationDiagnostic("target_not_string", source, key.Value, targetNode.Value, file, "relation target must be a string")
					continue
				}
				targetRaw := targetNode.Value
				if targetRaw == "" {
					bundle.addRelationDiagnostic("empty_target", source, key.Value, targetRaw, file, "relation target must not be empty")
					continue
				}
				target, err := parseRelationRefContext(ctx, targetRaw)
				if err != nil {
					bundle.addRelationDiagnostic("invalid_target_ref", source, key.Value, targetRaw, file, err.Error())
					continue
				}
				exists, err := bundle.targetExistsContext(ctx, target)
				if err != nil {
					return err
				}
				if publishEdges {
					*observations = append(*observations, RelationObservation{
						Source:       source,
						Type:         key.Value,
						Target:       target,
						TargetExists: exists,
						RawTarget:    targetRaw,
					})
					if err := ctx.Err(); err != nil {
						return err
					}
				}
				if !exists {
					code, message := "missing_target_concept", "target concept does not exist"
					conceptExists, err := bundle.targetExistsContext(ctx, RelationRef{ID: target.ID})
					if err != nil {
						return err
					}
					if conceptExists && target.Fragment != "" {
						code, message = "missing_or_ambiguous_target_fragment", "target fragment does not exist or is ambiguous"
					}
					bundle.addRelationDiagnostic(code, source, key.Value, targetRaw, file, message)
				}
				// SemanticLinksFrom and the reverse index are resolved graph views.
				// Keep unresolved observations as diagnostics only: publishing them as
				// edges makes graph consumers invent dangling resources.
				if publishEdges && exists {
					*out = append(*out, Relation{Source: source, Type: key.Value, Target: target, TargetExists: true, RawTarget: targetRaw})
					if err := ctx.Err(); err != nil {
						return err
					}
				}
			}
		}
	}
	return ctx.Err()
}

func resolveMappingIdentityContext(ctx context.Context, node *yaml.Node) (MappingIdentity, error) {
	ids, err := mappingValuesContext(ctx, node, "id")
	if err != nil {
		return MappingIdentity{}, err
	}
	anchors, err := mappingValuesContext(ctx, node, "anchor")
	if err != nil {
		return MappingIdentity{}, err
	}
	if len(ids) == 0 && len(anchors) == 0 {
		return MappingIdentity{State: MappingIdentityAbsent}, ctx.Err()
	}
	validIDs, err := validMappingFragmentsContext(ctx, ids)
	if err != nil {
		return MappingIdentity{}, err
	}
	if len(validIDs) > 1 {
		return MappingIdentity{State: MappingIdentityInvalid}, ctx.Err()
	}
	if len(validIDs) == 1 {
		return MappingIdentity{Fragment: validIDs[0].Value, Node: validIDs[0], State: MappingIdentityValid}, ctx.Err()
	}
	validAnchors, err := validMappingFragmentsContext(ctx, anchors)
	if err != nil {
		return MappingIdentity{}, err
	}
	if len(validAnchors) > 1 {
		return MappingIdentity{State: MappingIdentityInvalid}, ctx.Err()
	}
	if len(validAnchors) == 1 {
		return MappingIdentity{Fragment: validAnchors[0].Value, Node: validAnchors[0], State: MappingIdentityValid}, ctx.Err()
	}
	return MappingIdentity{State: MappingIdentityInvalid}, ctx.Err()
}

func validMappingFragmentsContext(ctx context.Context, values []*yaml.Node) ([]*yaml.Node, error) {
	var valid []*yaml.Node
	for _, value := range values {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		validFragment := false
		if value != nil && value.Kind == yaml.ScalarNode && value.Tag == "!!str" {
			var err error
			validFragment, err = validRelationFragmentContext(ctx, value.Value)
			if err != nil {
				return nil, err
			}
		}
		if validFragment {
			valid = append(valid, value)
		}
	}
	return valid, ctx.Err()
}

func mappingValuesContext(ctx context.Context, node *yaml.Node, key string) ([]*yaml.Node, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, nil
	}
	var values []*yaml.Node
	for i := 0; i+1 < len(node.Content); i += 2 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		matches, err := isStringKeyContext(ctx, node.Content[i], key)
		if err != nil {
			return nil, err
		}
		if matches {
			values = append(values, node.Content[i+1])
		}
	}
	return values, ctx.Err()
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

func isStringKeyContext(ctx context.Context, node *yaml.Node, value string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return false, ctx.Err()
	}
	return equalStringContext(ctx, node.Value, value)
}

// ParseRelationRef parses only canonical <escaped-concept-id>[#<fragment>]
// references. A '#' byte in the concept id is escaped as '\#'; backslashes
// retain their existing fragment semantics after the unescaped delimiter.
func ParseRelationRef(raw string) (RelationRef, error) {
	return parseRelationRefContext(context.Background(), raw)
}

func parseRelationRefContext(ctx context.Context, raw string) (RelationRef, error) {
	if err := ctx.Err(); err != nil {
		return RelationRef{}, err
	}
	copied, err := stringFromStringContext(ctx, raw)
	if err != nil {
		return RelationRef{}, err
	}
	raw = copied
	canonicalSpace, err := canonicalSpaceContext(ctx, raw)
	if err != nil {
		return RelationRef{}, err
	}
	if raw == "" || !canonicalSpace {
		return RelationRef{}, fmt.Errorf("%w: %q", ErrInvalidRelationRef, raw)
	}

	conceptPart, fragment, err := splitRelationRefContext(ctx, raw)
	if err != nil {
		return RelationRef{}, err
	}
	validConcept, err := validRelationConceptPartContext(ctx, conceptPart)
	if err != nil {
		return RelationRef{}, err
	}
	if !validConcept {
		return RelationRef{}, fmt.Errorf("%w: invalid concept", ErrInvalidRelationRef)
	}

	id, err := parseConceptIDContext(ctx, conceptPart)
	if err != nil {
		if ctx.Err() != nil {
			return RelationRef{}, ctx.Err()
		}
		return RelationRef{}, fmt.Errorf("%w: non-canonical concept", ErrInvalidRelationRef)
	}
	idString, err := conceptIDStringContext(ctx, id)
	if err != nil {
		return RelationRef{}, err
	}
	canonical, err := equalStringContext(ctx, idString, conceptPart)
	if err != nil {
		return RelationRef{}, err
	}
	if !canonical {
		return RelationRef{}, fmt.Errorf("%w: non-canonical concept", ErrInvalidRelationRef)
	}
	ref := RelationRef{ID: id, Fragment: fragment}
	if err := validateRelationRefContext(ctx, ref); err != nil {
		return RelationRef{}, err
	}
	encoded, err := relationRefStringContext(ctx, ref)
	if err != nil {
		return RelationRef{}, err
	}
	canonical, err = equalStringContext(ctx, encoded, raw)
	if err != nil {
		return RelationRef{}, err
	}
	if !canonical {
		return RelationRef{}, fmt.Errorf("%w: non-canonical encoding", ErrInvalidRelationRef)
	}
	return ref, ctx.Err()
}

func splitRelationRefContext(ctx context.Context, raw string) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	var concept strings.Builder
	for index := 0; index < len(raw); index++ {
		if index%(64<<10) == 0 {
			if err := ctx.Err(); err != nil {
				return "", "", err
			}
		}
		switch raw[index] {
		case '\\':
			if index+1 >= len(raw) || raw[index+1] != '#' {
				return "", "", fmt.Errorf("%w: invalid concept escape", ErrInvalidRelationRef)
			}
			concept.WriteByte('#')
			index++
		case '#':
			fragment := raw[index+1:]
			valid, err := validRelationFragmentContext(ctx, fragment)
			if err != nil {
				return "", "", err
			}
			if !valid {
				return "", "", fmt.Errorf("%w: invalid fragment", ErrInvalidRelationRef)
			}
			return concept.String(), fragment, ctx.Err()
		default:
			concept.WriteByte(raw[index])
		}
	}
	return concept.String(), "", ctx.Err()
}

func parseConceptIDContext(ctx context.Context, raw string) (ConceptID, error) {
	var segments []string
	start := 0
	for index := 0; index <= len(raw); index++ {
		if index%(64<<10) == 0 {
			if err := ctx.Err(); err != nil {
				return ConceptID{}, err
			}
		}
		if index != len(raw) && raw[index] != '/' {
			continue
		}
		if index > start {
			segment := raw[start:index]
			if err := validateConceptSegmentContext(ctx, segment); err != nil {
				return ConceptID{}, err
			}
			segments = append(segments, segment)
		}
		start = index + 1
	}
	if len(segments) == 0 {
		return ConceptID{}, fmt.Errorf("%w: empty concept id %q", ErrInvalidConceptID, raw)
	}
	return ConceptID{segments: segments}, ctx.Err()
}

func validateConceptSegmentContext(ctx context.Context, segment string) error {
	if segment == "" || segment == "." || segment == ".." {
		return fmt.Errorf("%w: invalid segment %q", ErrInvalidConceptID, segment)
	}
	for index := 0; index < len(segment); {
		if index%(64<<10) == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		r, size := utf8.DecodeRuneInString(segment[index:])
		if r == utf8.RuneError && size == 1 || r <= 0x1f || r == 0x7f || r == '\\' || r == '/' {
			return fmt.Errorf("%w: invalid segment %q", ErrInvalidConceptID, segment)
		}
		index += size
	}
	return ctx.Err()
}

func relationRefStringContext(ctx context.Context, ref RelationRef) (string, error) {
	id, err := conceptIDStringContext(ctx, ref.ID)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	for index := 0; index < len(id); index++ {
		if index%(64<<10) == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		if id[index] == '#' {
			out.WriteByte('\\')
		}
		out.WriteByte(id[index])
	}
	if ref.Fragment != "" {
		out.WriteByte('#')
		for offset := 0; offset < len(ref.Fragment); offset += 64 << 10 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			out.WriteString(ref.Fragment[offset:min(offset+(64<<10), len(ref.Fragment))])
		}
	}
	return out.String(), ctx.Err()
}

func splitRelationRef(raw string) (string, string, error) {
	var concept strings.Builder
	concept.Grow(len(raw))
	fragment := ""

	for index := 0; index < len(raw); index++ {
		switch raw[index] {
		case '\\':
			if index+1 >= len(raw) || raw[index+1] != '#' {
				return "", "", fmt.Errorf("%w: invalid concept escape", ErrInvalidRelationRef)
			}
			concept.WriteByte('#')
			index++
		case '#':
			fragment = raw[index+1:]
			if err := ValidateRelationFragment(fragment); err != nil {
				return "", "", fmt.Errorf("%w: invalid fragment", ErrInvalidRelationRef)
			}
			return concept.String(), fragment, nil
		default:
			concept.WriteByte(raw[index])
		}
	}

	return concept.String(), fragment, nil
}

// ValidateRelationRef validates a constructed canonical relation reference.
func ValidateRelationRef(ref RelationRef) error {
	if err := ValidateConceptID(ref.ID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRelationRef, err)
	}
	if !validRelationConceptPart(ref.ID.String()) {
		return fmt.Errorf("%w: invalid concept", ErrInvalidRelationRef)
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

func canonicalSpaceContext(ctx context.Context, value string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if value == "" {
		return true, nil
	}
	first, _ := utf8.DecodeRuneInString(value)
	last, _ := utf8.DecodeLastRuneInString(value)
	return !unicode.IsSpace(first) && !unicode.IsSpace(last), ctx.Err()
}

func validRelationConceptPartContext(ctx context.Context, value string) (bool, error) {
	canonicalSpace, err := canonicalSpaceContext(ctx, value)
	if err != nil {
		return false, err
	}
	if value == "" || !canonicalSpace || strings.HasSuffix(value, ".md") || strings.HasPrefix(value, "/") || strings.HasPrefix(value, "./") || strings.HasPrefix(value, "../") || strings.HasPrefix(value, "//") {
		return false, ctx.Err()
	}
	colon := -1
	for index := 0; index < len(value); index++ {
		if index%(64<<10) == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		if value[index] == ':' {
			colon = index
			break
		}
	}
	if colon <= 0 {
		return true, ctx.Err()
	}
	for index, r := range value[:colon] {
		if index%(64<<10) == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		if index == 0 && !asciiLetter(r) || index > 0 && !(asciiLetter(r) || asciiDigit(r) || r == '+' || r == '.' || r == '-') {
			return true, ctx.Err()
		}
	}
	return false, ctx.Err()
}

func validRelationFragment(value string) bool {
	return ValidateRelationFragment(value) == nil
}

func validRelationFragmentContext(ctx context.Context, value string) (bool, error) {
	if err := validateRelationFragmentContext(ctx, value); err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, nil
	}
	return true, ctx.Err()
}

func validateRelationFragmentContext(ctx context.Context, value string) error {
	if value == "" {
		return fmt.Errorf("%w: %q", ErrInvalidRelationFragment, value)
	}
	first, _ := utf8.DecodeRuneInString(value)
	last, _ := utf8.DecodeLastRuneInString(value)
	if unicode.IsSpace(first) || unicode.IsSpace(last) {
		return fmt.Errorf("%w: %q", ErrInvalidRelationFragment, value)
	}
	for index := 0; index < len(value); {
		if index%(64<<10) == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		r, size := utf8.DecodeRuneInString(value[index:])
		if r == utf8.RuneError && size == 1 || r < 0x20 || r == 0x7f || r == '#' {
			return fmt.Errorf("%w: %q", ErrInvalidRelationFragment, value)
		}
		index += size
	}
	return ctx.Err()
}

func validRelationType(value string) bool {
	return ValidateRelationType(value) == nil
}

func validRelationTypeContext(ctx context.Context, value string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if value == "" {
		return false, nil
	}
	if len(value) <= len("usage_window") {
		if _, reserved := reservedRelationTypes[value]; reserved {
			return false, ctx.Err()
		}
	}
	for index := 0; index < len(value); {
		if index%(64<<10) == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		r, size := utf8.DecodeRuneInString(value[index:])
		if r == utf8.RuneError && size == 1 || index == 0 && !asciiLetter(r) || index > 0 && !(asciiLetter(r) || asciiDigit(r) || r == '_') {
			return false, nil
		}
		index += size
	}
	return true, ctx.Err()
}

func validateRelationRefContext(ctx context.Context, ref RelationRef) error {
	if len(ref.ID.segments) == 0 {
		return fmt.Errorf("%w: %v", ErrInvalidRelationRef, fmt.Errorf("%w: empty concept id", ErrInvalidConceptID))
	}
	for _, segment := range ref.ID.segments {
		if err := validateConceptSegmentContext(ctx, segment); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("%w: %v", ErrInvalidRelationRef, err)
		}
	}
	id, err := conceptIDStringContext(ctx, ref.ID)
	if err != nil {
		return err
	}
	validConcept, err := validRelationConceptPartContext(ctx, id)
	if err != nil {
		return err
	}
	if !validConcept {
		return fmt.Errorf("%w: invalid concept", ErrInvalidRelationRef)
	}
	if ref.Fragment != "" {
		if err := validateRelationFragmentContext(ctx, ref.Fragment); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("%w: %v", ErrInvalidRelationRef, err)
		}
	}
	return ctx.Err()
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
