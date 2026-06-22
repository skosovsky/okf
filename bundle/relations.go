package bundle

import (
	"strings"

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

// Relation is a typed semantic edge from YAML frontmatter relations.
type Relation struct {
	Source       RelationRef
	Type         string
	Target       RelationRef
	TargetExists bool
	RawTarget    string
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
	traverseRelationNode(root, source, true, bundle, &relations)
	return relations
}

func traverseRelationNode(node *yaml.Node, documentSource RelationRef, topLevel bool, bundle *Bundle, out *[]Relation) {
	if node == nil {
		return
	}

	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i]
			value := node.Content[i+1]
			if key.Kind == yaml.ScalarNode && key.Value == "relations" {
				source, ok := documentSource, true
				if !topLevel {
					source, ok = relationSourceFromMapping(node, documentSource.ID)
				}
				if ok {
					appendRelationsFromBlock(value, source, bundle, out)
				}
				continue
			}
			traverseRelationNode(value, documentSource, false, bundle, out)
		}
	case yaml.SequenceNode:
		for _, item := range node.Content {
			traverseRelationNode(item, documentSource, false, bundle, out)
		}
	}
}

func relationSourceFromMapping(node *yaml.Node, conceptID ConceptID) (RelationRef, bool) {
	if fragment, ok := validAnchorField(node, "id"); ok {
		return RelationRef{ID: conceptID, Fragment: fragment}, true
	}
	if fragment, ok := validAnchorField(node, "anchor"); ok {
		return RelationRef{ID: conceptID, Fragment: fragment}, true
	}
	return RelationRef{}, false
}

func validAnchorField(node *yaml.Node, key string) (string, bool) {
	value, ok := mappingValue(node, key)
	if !ok || value.Kind != yaml.ScalarNode || value.Tag != "!!str" || !validRelationFragment(value.Value) {
		return "", false
	}
	return value.Value, true
}

func appendRelationsFromBlock(node *yaml.Node, source RelationRef, bundle *Bundle, out *[]Relation) {
	if node == nil || node.Kind != yaml.MappingNode {
		return
	}

	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i]
		value := node.Content[i+1]
		if key.Kind != yaml.ScalarNode || !validRelationType(key.Value) || value.Kind != yaml.SequenceNode {
			continue
		}
		for _, item := range value.Content {
			targetRaw, ok := relationItemTarget(item)
			if !ok {
				continue
			}
			target, ok := parseRelationRef(targetRaw)
			if !ok {
				continue
			}
			*out = append(*out, Relation{
				Source:       source,
				Type:         key.Value,
				Target:       target,
				TargetExists: bundle.Contains(target.ID),
				RawTarget:    targetRaw,
			})
		}
	}
}

func relationItemTarget(node *yaml.Node) (string, bool) {
	if node == nil || node.Kind != yaml.MappingNode {
		return "", false
	}
	target, ok := mappingValue(node, "target")
	if !ok || target.Kind != yaml.ScalarNode || target.Tag != "!!str" || target.Value == "" {
		return "", false
	}
	return target.Value, true
}

func mappingValue(node *yaml.Node, key string) (*yaml.Node, bool) {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, false
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Kind == yaml.ScalarNode && node.Content[i].Value == key {
			return node.Content[i+1], true
		}
	}
	return nil, false
}

func parseRelationRef(raw string) (RelationRef, bool) {
	if raw == "" || strings.TrimSpace(raw) != raw || strings.Count(raw, "#") > 1 {
		return RelationRef{}, false
	}

	conceptPart := raw
	fragment := ""
	if before, after, found := strings.Cut(raw, "#"); found {
		conceptPart = before
		fragment = after
		if !validRelationFragment(fragment) {
			return RelationRef{}, false
		}
	}
	if !validRelationConceptPart(conceptPart) {
		return RelationRef{}, false
	}

	id, err := ParseConceptID(conceptPart)
	if err != nil || id.String() != conceptPart {
		return RelationRef{}, false
	}
	return RelationRef{ID: id, Fragment: fragment}, true
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
	if value == "" || strings.TrimSpace(value) != value || strings.Contains(value, "#") {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func validRelationType(value string) bool {
	if value == "" {
		return false
	}
	if _, reserved := reservedRelationTypes[value]; reserved {
		return false
	}
	for i, r := range value {
		switch {
		case i == 0 && !asciiLetter(r):
			return false
		case i > 0 && !(asciiLetter(r) || asciiDigit(r) || r == '_'):
			return false
		}
	}
	return true
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
