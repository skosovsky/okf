//go:build ignore

// Validates Agent Skills frontmatter using the repository's YAML parser.
package main

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

var validName = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: go run scripts/validate-skill-frontmatter.go NAME SKILL.md")
		os.Exit(2)
	}
	content, err := os.ReadFile(os.Args[2])
	if err == nil {
		err = validate(os.Args[1], content)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func validate(expected string, content []byte) error {
	if len(content) > 100_000 || !bytes.HasPrefix(content, []byte("---\n")) {
		return fmt.Errorf("%s: invalid or oversized SKILL.md", expected)
	}
	end := bytes.Index(content[4:], []byte("\n---\n"))
	if end < 0 || end > 10_000 {
		return fmt.Errorf("%s: YAML frontmatter missing or oversized", expected)
	}
	frontmatter := content[4 : 4+end]
	var document yaml.Node
	if err := yaml.Unmarshal(frontmatter, &document); err != nil {
		return fmt.Errorf("%s: malformed YAML: %w", expected, err)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("%s: frontmatter must be a mapping", expected)
	}
	fields := map[string]*yaml.Node{}
	entries := document.Content[0].Content
	for i := 0; i < len(entries); i += 2 {
		key, value := entries[i], entries[i+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return fmt.Errorf("%s: frontmatter key must be a string", expected)
		}
		if _, exists := fields[key.Value]; exists {
			return fmt.Errorf("%s: duplicate frontmatter key %q", expected, key.Value)
		}
		fields[key.Value] = value
	}
	name, err := stringField(fields, "name", true)
	if err != nil {
		return fmt.Errorf("%s: %w", expected, err)
	}
	if name != expected || utf8.RuneCountInString(name) > 64 || !validName.MatchString(name) {
		return fmt.Errorf("%s: invalid name %q", expected, name)
	}
	description, err := stringField(fields, "description", true)
	if err != nil {
		return fmt.Errorf("%s: %w", expected, err)
	}
	if strings.TrimSpace(description) == "" || utf8.RuneCountInString(description) > 1024 {
		return fmt.Errorf("%s: description must contain 1–1024 characters", expected)
	}
	if compatibility, exists := fields["compatibility"]; exists {
		if compatibility.Kind != yaml.ScalarNode || compatibility.Tag != "!!str" || strings.TrimSpace(compatibility.Value) == "" || utf8.RuneCountInString(compatibility.Value) > 500 {
			return fmt.Errorf("%s: compatibility must contain 1–500 characters", expected)
		}
	}
	return nil
}

func stringField(fields map[string]*yaml.Node, key string, required bool) (string, error) {
	value, exists := fields[key]
	if !exists {
		if required {
			return "", fmt.Errorf("%s missing", key)
		}
		return "", nil
	}
	if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
		return "", fmt.Errorf("%s must be a YAML string", key)
	}
	return value.Value, nil
}
