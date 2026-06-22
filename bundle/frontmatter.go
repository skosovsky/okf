package bundle

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Frontmatter stores OKF YAML frontmatter as an order-preserving YAML mapping.
type Frontmatter struct {
	node yaml.Node
}

// NewFrontmatter creates an empty frontmatter mapping.
func NewFrontmatter() Frontmatter {
	return Frontmatter{node: newMappingNode()}
}

// NewFrontmatterFromNode creates frontmatter from a YAML node.
//
// A document node is unwrapped. A null or zero node becomes an empty mapping.
// A non-mapping value is rejected because OKF frontmatter is always a mapping.
func NewFrontmatterFromNode(node *yaml.Node) (Frontmatter, error) {
	if node == nil || node.Kind == 0 || isNullNode(node) {
		return NewFrontmatter(), nil
	}
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			return NewFrontmatter(), nil
		}
		return NewFrontmatterFromNode(node.Content[0])
	}
	if node.Kind != yaml.MappingNode {
		return Frontmatter{}, fmt.Errorf("%w: expected YAML mapping", ErrInvalidFrontmatter)
	}

	return Frontmatter{node: cloneYAMLNode(node)}, nil
}

// ParseFrontmatter parses a YAML frontmatter mapping.
func ParseFrontmatter(text string) (Frontmatter, error) {
	if strings.TrimSpace(text) == "" {
		return NewFrontmatter(), nil
	}

	var node yaml.Node
	if err := yaml.Unmarshal([]byte(text), &node); err != nil {
		return Frontmatter{}, fmt.Errorf("%w: %v", ErrInvalidFrontmatter, err)
	}

	frontmatter, err := NewFrontmatterFromNode(&node)
	if err != nil {
		return Frontmatter{}, err
	}
	return frontmatter, nil
}

// IsEmpty reports whether the frontmatter has no keys.
func (f Frontmatter) IsEmpty() bool {
	node := f.mappingNode()
	return len(node.Content) == 0
}

// YAMLString serializes frontmatter as a YAML mapping.
func (f Frontmatter) YAMLString() (string, error) {
	node := f.YAMLNode()
	if len(node.Content) == 0 {
		return "", nil
	}

	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(node); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidFrontmatter, err)
	}
	_ = encoder.Close()

	return strings.TrimSuffix(buf.String(), "...\n"), nil
}

// YAMLNode returns a copy of the underlying YAML mapping node.
func (f Frontmatter) YAMLNode() *yaml.Node {
	node := f.mappingNode()
	cloned := cloneYAMLNode(&node)
	return &cloned
}

// Get returns the YAML value node for a string key.
func (f Frontmatter) Get(key string) (*yaml.Node, bool) {
	node := f.mappingNode()
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Kind == yaml.ScalarNode && node.Content[i].Value == key {
			value := cloneYAMLNode(node.Content[i+1])
			return &value, true
		}
	}
	return nil, false
}

// Set stores a raw YAML value for a string key.
//
// If the key already exists, its value is replaced without moving the key.
func (f *Frontmatter) Set(key string, value *yaml.Node) error {
	if key == "" {
		return fmt.Errorf("%w: empty key", ErrInvalidFrontmatter)
	}
	f.ensureMapping()

	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	valueNode := yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: ""}
	if value != nil {
		valueNode = cloneYAMLNode(value)
	}

	for i := 0; i+1 < len(f.node.Content); i += 2 {
		if f.node.Content[i].Kind == yaml.ScalarNode && f.node.Content[i].Value == key {
			f.node.Content[i+1] = &valueNode
			return nil
		}
	}

	f.node.Content = append(f.node.Content, keyNode, &valueNode)
	return nil
}

// SetString stores a string scalar for a frontmatter key.
func (f *Frontmatter) SetString(key, value string) error {
	return f.Set(key, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}

// Keys returns string keys in source/insertion order.
func (f Frontmatter) Keys() []string {
	node := f.mappingNode()
	keys := make([]string, 0, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i]
		if key.Kind == yaml.ScalarNode {
			keys = append(keys, key.Value)
		}
	}
	return keys
}

// Type returns the required OKF type field.
func (f Frontmatter) Type() (string, bool) {
	return f.stringScalar("type")
}

// Title returns the optional display title field.
func (f Frontmatter) Title() (string, bool) {
	return f.scalarDisplayString("title")
}

// Description returns the optional one-line description field.
func (f Frontmatter) Description() (string, bool) {
	return f.scalarDisplayString("description")
}

// Resource returns the optional resource URI field.
func (f Frontmatter) Resource() (string, bool) {
	return f.scalarDisplayString("resource")
}

// Timestamp returns the optional last-modified timestamp field.
func (f Frontmatter) Timestamp() (string, bool) {
	return f.scalarDisplayString("timestamp")
}

// OKFVersion returns the root index OKF version declaration.
func (f Frontmatter) OKFVersion() (string, bool) {
	return f.stringScalar("okf_version")
}

// Tags returns the optional tags list. Scalar tag values are returned in their
// display form. A non-sequence tags value returns nil.
func (f Frontmatter) Tags() []string {
	node, ok := f.Get("tags")
	if !ok || node.Kind != yaml.SequenceNode {
		return nil
	}

	tags := make([]string, 0, len(node.Content))
	for _, item := range node.Content {
		if value, ok := displayString(item); ok {
			tags = append(tags, value)
		}
	}
	return tags
}

// ExtensionKeys returns keys that are not well-known OKF frontmatter fields.
func (f Frontmatter) ExtensionKeys() []string {
	known := map[string]struct{}{
		"type":        {},
		"title":       {},
		"description": {},
		"resource":    {},
		"tags":        {},
		"timestamp":   {},
	}

	var extensions []string
	for _, key := range f.Keys() {
		if _, ok := known[key]; !ok {
			extensions = append(extensions, key)
		}
	}
	return extensions
}

func (f Frontmatter) scalarDisplayString(key string) (string, bool) {
	node, ok := f.Get(key)
	if !ok {
		return "", false
	}
	return displayString(node)
}

func (f Frontmatter) stringScalar(key string) (string, bool) {
	node, ok := f.Get(key)
	if !ok || node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return "", false
	}
	if node.Value == "" {
		return "", false
	}
	return node.Value, true
}

func (f *Frontmatter) ensureMapping() {
	if f.node.Kind == yaml.MappingNode {
		return
	}
	f.node = newMappingNode()
}

func (f Frontmatter) mappingNode() yaml.Node {
	if f.node.Kind == yaml.MappingNode {
		return f.node
	}
	return newMappingNode()
}

func newMappingNode() yaml.Node {
	return yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
}

func isNullNode(node *yaml.Node) bool {
	return node.Kind == yaml.ScalarNode && (node.Tag == "!!null" || node.Value == "" && node.Tag == "")
}

func displayString(node *yaml.Node) (string, bool) {
	if node == nil || node.Kind != yaml.ScalarNode || isNullNode(node) {
		return "", false
	}

	switch node.Tag {
	case "!!str", "!!bool", "!!int", "!!float", "!!timestamp", "":
		return node.Value, true
	default:
		return "", false
	}
}

func isEmptyYAMLValue(node *yaml.Node) bool {
	if node == nil || isNullNode(node) {
		return true
	}

	switch node.Kind {
	case yaml.ScalarNode:
		switch node.Tag {
		case "!!str":
			return node.Value == ""
		case "!!bool":
			value, err := strconv.ParseBool(node.Value)
			return err == nil && !value
		case "!!int":
			value, err := strconv.ParseInt(node.Value, 10, 64)
			return err == nil && value == 0
		default:
			return false
		}
	case yaml.SequenceNode, yaml.MappingNode:
		return len(node.Content) == 0
	default:
		return false
	}
}

func cloneYAMLNode(node *yaml.Node) yaml.Node {
	if node == nil {
		return yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: ""}
	}

	cloned := *node
	if len(node.Content) > 0 {
		cloned.Content = make([]*yaml.Node, len(node.Content))
		for i, child := range node.Content {
			childClone := cloneYAMLNode(child)
			cloned.Content[i] = &childClone
		}
	}
	return cloned
}
