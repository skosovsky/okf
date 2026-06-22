package bundle

import (
	"errors"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestFrontmatterZeroValueIsUsable(t *testing.T) {
	t.Parallel()

	// Arrange.
	var frontmatter Frontmatter

	// Act.
	err := frontmatter.SetString("type", "Note")
	value, ok := frontmatter.Type()

	// Assert.
	if err != nil {
		t.Fatalf("SetString() error = %v", err)
	}
	if !ok || value != "Note" {
		t.Fatalf("Type() = %q, %v; want Note, true", value, ok)
	}
}

func TestFrontmatterPreservesKeyOrderAndExtensions(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter := NewFrontmatter()

	// Act.
	mustSetString(t, &frontmatter, "type", "BigQuery Table")
	mustSetString(t, &frontmatter, "title", "Orders")
	mustSetString(t, &frontmatter, "custom_key", "custom value")
	mustSetString(t, &frontmatter, "description", "One row per order.")

	// Assert.
	if got, want := frontmatter.Keys(), []string{"type", "title", "custom_key", "description"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Keys() = %#v, want %#v", got, want)
	}
	if got, want := frontmatter.ExtensionKeys(), []string{"custom_key"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ExtensionKeys() = %#v, want %#v", got, want)
	}
}

func TestFrontmatterSetReplacesWithoutMovingKey(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter := NewFrontmatter()
	mustSetString(t, &frontmatter, "type", "Old")
	mustSetString(t, &frontmatter, "title", "Title")

	// Act.
	mustSetString(t, &frontmatter, "type", "New")

	// Assert.
	if got, want := frontmatter.Keys(), []string{"type", "title"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Keys() = %#v, want %#v", got, want)
	}
	if got, ok := frontmatter.Type(); !ok || got != "New" {
		t.Fatalf("Type() = %q, %v; want New, true", got, ok)
	}
}

func TestFrontmatterTypedAccessors(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter := NewFrontmatter()

	// Act.
	mustSetString(t, &frontmatter, "type", "Playbook")
	mustSetString(t, &frontmatter, "title", "Incident Response")
	mustSetString(t, &frontmatter, "description", "Triage steps.")
	mustSetString(t, &frontmatter, "resource", "https://example.com/runbook")
	mustSetString(t, &frontmatter, "timestamp", "2026-05-28T00:00:00Z")
	if err := frontmatter.Set("tags", sequenceNode(
		scalarNode("!!str", "oncall"),
		scalarNode("!!int", "7"),
	)); err != nil {
		t.Fatalf("Set(tags) error = %v", err)
	}

	// Assert.
	assertAccessor(t, "Type", frontmatter.Type, "Playbook")
	assertAccessor(t, "Title", frontmatter.Title, "Incident Response")
	assertAccessor(t, "Description", frontmatter.Description, "Triage steps.")
	assertAccessor(t, "Resource", frontmatter.Resource, "https://example.com/runbook")
	assertAccessor(t, "Timestamp", frontmatter.Timestamp, "2026-05-28T00:00:00Z")
	if got, want := frontmatter.Tags(), []string{"oncall", "7"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Tags() = %#v, want %#v", got, want)
	}
}

func TestNewFrontmatterFromNodeClonesInput(t *testing.T) {
	t.Parallel()

	// Arrange.
	node := mappingNode(
		"type", scalarNode("!!str", "Note"),
		"title", scalarNode("!!str", "Original"),
	)

	// Act.
	frontmatter, err := NewFrontmatterFromNode(node)
	if err != nil {
		t.Fatalf("NewFrontmatterFromNode() error = %v", err)
	}
	node.Content[3].Value = "Mutated"
	gotNode, ok := frontmatter.Get("title")
	if !ok {
		t.Fatal("Get(title) ok = false, want true")
	}
	gotNode.Value = "Caller mutation"
	title, ok := frontmatter.Title()

	// Assert.
	if !ok || title != "Original" {
		t.Fatalf("Title() = %q, %v; want Original, true", title, ok)
	}
}

func TestNewFrontmatterFromNodeRejectsNonMapping(t *testing.T) {
	t.Parallel()

	// Arrange.
	node := sequenceNode(scalarNode("!!str", "not a mapping"))

	// Act.
	_, err := NewFrontmatterFromNode(node)

	// Assert.
	if !errors.Is(err, ErrInvalidFrontmatter) {
		t.Fatalf("NewFrontmatterFromNode() error = %v, want ErrInvalidFrontmatter", err)
	}
}

func TestIsEmptyYAMLValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		node *yaml.Node
		want bool
	}{
		{name: "nil", node: nil, want: true},
		{name: "null", node: scalarNode("!!null", ""), want: true},
		{name: "empty string", node: scalarNode("!!str", ""), want: true},
		{name: "false", node: scalarNode("!!bool", "false"), want: true},
		{name: "zero int", node: scalarNode("!!int", "0"), want: true},
		{name: "empty sequence", node: sequenceNode(), want: true},
		{name: "non-empty string", node: scalarNode("!!str", "x"), want: false},
		{name: "true", node: scalarNode("!!bool", "true"), want: false},
		{name: "non-zero int", node: scalarNode("!!int", "42"), want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			node := tt.node

			// Act.
			got := isEmptyYAMLValue(node)

			// Assert.
			if got != tt.want {
				t.Fatalf("isEmptyYAMLValue() = %v, want %v", got, tt.want)
			}
		})
	}
}

func mustSetString(t *testing.T, frontmatter *Frontmatter, key, value string) {
	t.Helper()
	if err := frontmatter.SetString(key, value); err != nil {
		t.Fatalf("SetString(%q) error = %v", key, err)
	}
}

func assertAccessor(t *testing.T, name string, accessor func() (string, bool), want string) {
	t.Helper()
	got, ok := accessor()
	if !ok || got != want {
		t.Fatalf("%s() = %q, %v; want %q, true", name, got, ok, want)
	}
}

func scalarNode(tag, value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value}
}

func sequenceNode(items ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: items}
}

func mappingNode(keyValues ...any) *yaml.Node {
	content := make([]*yaml.Node, 0, len(keyValues))
	for i := 0; i+1 < len(keyValues); i += 2 {
		content = append(content, scalarNode("!!str", keyValues[i].(string)), keyValues[i+1].(*yaml.Node))
	}
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: content}
}
