package mutation

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/skosovsky/okf/bundle"
	"gopkg.in/yaml.v3"
)

// errLossyRelationDeduplication marks a desired-state duplicate which cannot
// be removed without discarding presentation or extension data. It is kept
// internal so the planner can expose one stable public InvalidChangeSet code.
var errLossyRelationDeduplication = errors.New("lossy relation deduplication")

// bytePatch replaces [Start, End) in one source buffer.  Patches are applied
// from right to left; overlapping patches are rejected rather than silently
// corrupting a presentation document.
type bytePatch struct {
	Start, End int
	Text       []byte
}

func applyBytePatches(in []byte, patches []bytePatch) ([]byte, error) {
	sort.Slice(patches, func(i, j int) bool { return patches[i].Start < patches[j].Start })
	last := 0
	for _, p := range patches {
		if p.Start < last || p.Start < 0 || p.End < p.Start || p.End > len(in) {
			return nil, fmt.Errorf("invalid or overlapping source patches")
		}
		last = p.End
	}
	out := append([]byte(nil), in...)
	for i := len(patches) - 1; i >= 0; i-- {
		p := patches[i]
		out = append(append(out[:p.Start:p.Start], p.Text...), out[p.End:]...)
	}
	return out, nil
}

type presentation struct {
	data, yaml         []byte
	yamlStart, yamlEnd int
	root               *yaml.Node
	newline            []byte
}

func parsePresentation(data []byte) (*presentation, error) {
	// yaml.v3 normalizes malformed UTF-8. A mutation must never calculate
	// source offsets against normalized bytes and then patch the original file.
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("%w: invalid UTF-8", bundle.ErrInvalidEncoding)
	}

	if !bytes.HasPrefix(data, []byte("---")) {
		return nil, bundle.ErrUnterminatedFrontmatter
	}
	lineEnd := func(at int) int {
		if n := bytes.IndexByte(data[at:], '\n'); n >= 0 {
			return at + n + 1
		}
		return len(data)
	}
	firstEnd := lineEnd(0)
	if strings.TrimSpace(string(bytes.TrimSuffix(data[:firstEnd], []byte("\n")))) != "---" {
		return nil, bundle.ErrUnterminatedFrontmatter
	}
	start := firstEnd
	end := -1
	for at := start; at < len(data); {
		next := lineEnd(at)
		if strings.TrimSpace(string(bytes.TrimSuffix(data[at:next], []byte("\n")))) == "---" {
			end = at
			break
		}
		at = next
	}
	if end < 0 {
		return nil, bundle.ErrUnterminatedFrontmatter
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data[start:end], &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, bundle.ErrInvalidFrontmatter
	}
	if err := validateSemanticPresentation(doc.Content[0]); err != nil {
		return nil, err
	}
	nl := []byte("\n")
	if bytes.Contains(data[start:end], []byte("\r\n")) {
		nl = []byte("\r\n")
	}
	return &presentation{data: data, yaml: data[start:end], yamlStart: start, yamlEnd: end, root: doc.Content[0], newline: nl}, nil
}

// validateSemanticPresentation rejects duplicate keys whose meaning is used by
// a presentation patch. YAML permits duplicate mapping keys, but a mutation
// cannot give them a deterministic desired-state interpretation: selecting one
// would silently make the others stale, while deleting one can delete an
// unrelated relation item. This is intentionally stricter than YAML parsing.
func validateSemanticPresentation(root *yaml.Node) error {
	var visit func(*yaml.Node) error
	visit = func(n *yaml.Node) error {
		if n == nil {
			return nil
		}
		switch n.Kind {
		case yaml.MappingNode:
			for _, key := range []string{"relations"} {
				if len(mappingValues(n, key)) > 1 {
					return fmt.Errorf("ambiguous duplicate %s keys", key)
				}
			}
			for _, relations := range mappingValues(n, "relations") {
				if relations.Kind != yaml.MappingNode {
					continue
				}
				for i := 0; i+1 < len(relations.Content); i += 2 {
					key := relations.Content[i]
					if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
						continue
					}
					if len(mappingValues(relations, key.Value)) > 1 {
						return fmt.Errorf("ambiguous duplicate relation type keys")
					}
					items := relations.Content[i+1]
					if items.Kind != yaml.SequenceNode {
						continue
					}
					for _, item := range items.Content {
						if item != nil && item.Kind == yaml.MappingNode && len(mappingValues(item, "target")) > 1 {
							return fmt.Errorf("ambiguous duplicate target keys")
						}
					}
				}
			}
			for i := 1; i < len(n.Content); i += 2 {
				if err := visit(n.Content[i]); err != nil {
					return err
				}
			}
		case yaml.SequenceNode:
			for _, child := range n.Content {
				if err := visit(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return visit(root)
}

func (p *presentation) offset(n *yaml.Node) (int, error) {
	if n == nil || n.Line < 1 || n.Column < 1 {
		return 0, fmt.Errorf("YAML node has no source position")
	}
	line, at := 1, 0
	for line < n.Line {
		i := bytes.IndexByte(p.yaml[at:], '\n')
		if i < 0 {
			return 0, fmt.Errorf("YAML node line outside source")
		}
		at += i + 1
		line++
	}
	return at + n.Column - 1, nil
}

// scalarPatch changes a scalar token while retaining its original YAML style
// when that style can safely represent the replacement.
func (p *presentation) scalarPatch(n *yaml.Node, value string) (bytePatch, error) {
	start, err := p.offset(n)
	if err != nil {
		return bytePatch{}, err
	}
	end := start
	if start >= len(p.yaml) {
		return bytePatch{}, fmt.Errorf("scalar outside source")
	}
	if p.yaml[start] == '\'' || p.yaml[start] == '"' {
		quote := p.yaml[start]
		end++
		for end < len(p.yaml) {
			if p.yaml[end] == quote {
				if quote == '\'' && end+1 < len(p.yaml) && p.yaml[end+1] == '\'' {
					end += 2
					continue
				}
				end++
				break
			}
			if quote == '"' && p.yaml[end] == '\\' {
				end += 2
				continue
			}
			end++
		}
		if end > len(p.yaml) || p.yaml[end-1] != quote {
			return bytePatch{}, fmt.Errorf("unterminated YAML scalar")
		}
		return bytePatch{start, end, []byte(yamlScalarToken(value, n.Style))}, nil
	}
	for end < len(p.yaml) && !bytes.ContainsRune([]byte(" \t\r\n,[]{}:"), rune(p.yaml[end])) {
		end++
	}
	return bytePatch{start, end, []byte(yamlScalarToken(value, n.Style))}, nil
}

// yamlScalarToken encodes exactly one YAML scalar without relying on callers
// to pre-escape interpolated values. Existing quote styles are kept where
// possible; unsafe plain scalars are emitted as YAML-compatible JSON strings.
func yamlScalarToken(value string, preferred yaml.Style) string {
	if preferred&yaml.SingleQuotedStyle != 0 {
		return "'" + strings.ReplaceAll(value, "'", "''") + "'"
	}
	if preferred&yaml.DoubleQuotedStyle != 0 {
		return strconv.Quote(value)
	}
	if yamlPlainSafe(value) {
		return value
	}
	return strconv.Quote(value)
}

func yamlPlainSafe(value string) bool {
	if value == "" || strings.TrimSpace(value) != value {
		return false
	}
	if strings.ContainsAny(value, "\r\n\t") {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	var document yaml.Node
	if err := yaml.Unmarshal([]byte("value: "+value+"\n"), &document); err != nil || len(document.Content) == 0 {
		return false
	}
	root := document.Content[0]
	return root.Kind == yaml.MappingNode && len(root.Content) == 2 && root.Content[1].Kind == yaml.ScalarNode && root.Content[1].Tag == "!!str" && root.Content[1].Value == value
}

func mappingValues(n *yaml.Node, key string) []*yaml.Node {
	var out []*yaml.Node
	if n == nil || n.Kind != yaml.MappingNode {
		return out
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			out = append(out, n.Content[i+1])
		}
	}
	return out
}

func relationTargetPatches(p *presentation, from, to bundle.ConceptID, oldFragment, newFragment string) ([]bytePatch, error) {
	var patches []bytePatch
	var firstErr error
	walkMappings(p.root, func(n *yaml.Node) {
		if firstErr != nil {
			return
		}
		for _, relations := range mappingValues(n, "relations") {
			if relations.Kind != yaml.MappingNode {
				continue
			}
			for i := 0; i+1 < len(relations.Content); i += 2 {
				items := relations.Content[i+1]
				if items.Kind != yaml.SequenceNode {
					continue
				}
				for _, item := range items.Content {
					for _, target := range mappingValues(item, "target") {
						r, e := bundle.ParseRelationRef(target.Value)
						if e != nil || r.ID.String() != from.String() || (oldFragment != "" && r.Fragment != oldFragment) {
							continue
						}
						r.ID = to
						if oldFragment != "" {
							r.Fragment = newFragment
						}
						patch, e := p.scalarPatch(target, r.String())
						if e != nil {
							firstErr = e
							return
						}
						patches = append(patches, patch)
					}
				}
			}
		}
	})
	if firstErr != nil {
		return nil, firstErr
	}
	return patches, nil
}

func fragmentPatch(p *presentation, from, to string) (bytePatch, bool, error) {
	var result bytePatch
	found := false
	var err error
	walkNestedMappings(p.root, func(n *yaml.Node) {
		if found || err != nil {
			return
		}
		canonical, node, ambiguous := canonicalFragmentNode(n)
		if ambiguous {
			err = fmt.Errorf("ambiguous duplicate canonical fragment keys")
			return
		}
		if canonical != from {
			return
		}
		result, err = p.scalarPatch(node, to)
		found = err == nil
	})
	return result, found, err
}

func (p *presentation) patchYAML(patches []bytePatch) ([]byte, error) {
	for i := range patches {
		patches[i].Start += p.yamlStart
		patches[i].End += p.yamlStart
	}
	return applyBytePatches(p.data, patches)
}

// ensureRelationPresentation changes only the selected relation sequence (or
// inserts one at the end of the selected source mapping). Duplicate matching
// mapping keys are deliberately rejected: YAML permits them but they have no
// stable desired-state interpretation.
func ensureRelationPresentation(data []byte, source bundle.RelationRef, typ, target string) ([]byte, error) {
	p, err := parsePresentation(data)
	if err != nil {
		return nil, err
	}
	n := mappingForRef(p.root, source)
	if n == nil {
		return nil, fmt.Errorf("source fragment missing")
	}
	relationBlocks := mappingValues(n, "relations")
	if len(relationBlocks) > 1 {
		return nil, fmt.Errorf("ambiguous duplicate relations keys")
	}
	var relations *yaml.Node
	if len(relationBlocks) == 1 {
		relations = relationBlocks[0]
		if relations.Kind != yaml.MappingNode || relations.Style&yaml.FlowStyle != 0 {
			return nil, fmt.Errorf("relations must be a block mapping")
		}
	}
	if relations != nil {
		seqs := mappingValues(relations, typ)
		if len(seqs) > 1 {
			return nil, fmt.Errorf("ambiguous duplicate relation type keys")
		}
		if len(seqs) == 1 {
			seq := seqs[0]
			if seq.Kind != yaml.SequenceNode || seq.Style&yaml.FlowStyle != 0 {
				return nil, fmt.Errorf("relation type must be a block sequence")
			}
			var matches []*yaml.Node
			var duplicates []bytePatch
			for _, item := range seq.Content {
				for _, v := range mappingValues(item, "target") {
					if v.Value == target {
						matches = append(matches, item)
						if len(matches) > 1 {
							start, offsetErr := p.offset(item)
							if offsetErr != nil {
								return nil, offsetErr
							}
							for start > 0 && p.yaml[start-1] != '\n' {
								start--
							}
							end, offsetErr := p.blockEnd(item)
							if offsetErr != nil {
								return nil, offsetErr
							}
							duplicates = append(duplicates, bytePatch{Start: p.yamlStart + start, End: p.yamlStart + end})
						}
					}
				}
			}
			if len(matches) > 1 {
				// Deleting a whole YAML item is safe only when every duplicate is
				// the same bare, plain one-line item. Any extension, comment,
				// anchor, tag, style, or intervening presentation would otherwise
				// be silently discarded by the byte-range patch below.
				for _, item := range matches {
					if !p.losslesslyRemovablePlainRelationItem(item, target) {
						return nil, fmt.Errorf("%w: duplicate relation target %q", errLossyRelationDeduplication, target)
					}
				}
			}
			if len(matches) > 0 {
				return applyBytePatches(data, duplicates)
			}
			at, err := p.blockEnd(seq)
			if err != nil {
				return nil, err
			}
			indent := p.indentOf(seq) + 2
			text := strings.Repeat(" ", indent) + "- target: " + yamlScalarToken(target, 0)
			return applyBytePatches(data, []bytePatch{{Start: p.yamlStart + at, End: p.yamlStart + at, Text: append([]byte(text), p.newline...)}})
		}
		at, err := p.blockEnd(relations)
		if err != nil {
			return nil, err
		}
		indent := p.indentOf(relations)
		text := strings.Repeat(" ", indent) + yamlScalarToken(typ, 0) + ":" + string(p.newline) + strings.Repeat(" ", indent+2) + "- target: " + yamlScalarToken(target, 0) + string(p.newline)
		return applyBytePatches(data, []bytePatch{{Start: p.yamlStart + at, End: p.yamlStart + at, Text: []byte(text)}})
	}
	at, err := p.blockEnd(n)
	if err != nil {
		return nil, err
	}
	indent := p.indentOf(n)
	text := strings.Repeat(" ", indent) + "relations:" + string(p.newline) + strings.Repeat(" ", indent+2) + yamlScalarToken(typ, 0) + ":" + string(p.newline) + strings.Repeat(" ", indent+4) + "- target: " + yamlScalarToken(target, 0) + string(p.newline)
	return applyBytePatches(data, []bytePatch{{Start: p.yamlStart + at, End: p.yamlStart + at, Text: []byte(text)}})
}

// losslesslyRemovablePlainRelationItem proves that the source range selected
// for deletion contains precisely a plain `- target: value` line. Comparing
// the original bytes also catches comments between sequence items, which yaml
// node comments alone do not reliably attach to the preceding item.
func (p *presentation) losslesslyRemovablePlainRelationItem(item *yaml.Node, target string) bool {
	if item == nil || item.Kind != yaml.MappingNode || item.Style != 0 || item.Tag != "!!map" || item.Anchor != "" || hasYAMLComments(item) || len(item.Content) != 2 {
		return false
	}
	key, value := item.Content[0], item.Content[1]
	if key == nil || value == nil || key.Kind != yaml.ScalarNode || key.Tag != "!!str" || key.Value != "target" || key.Style != 0 || key.Anchor != "" || hasYAMLComments(key) {
		return false
	}
	if value.Kind != yaml.ScalarNode || value.Tag != "!!str" || value.Value != target || value.Style != 0 || value.Anchor != "" || hasYAMLComments(value) || !yamlPlainSafe(target) {
		return false
	}
	start, err := p.offset(item)
	if err != nil {
		return false
	}
	for start > 0 && p.yaml[start-1] != '\n' {
		start--
	}
	end, err := p.blockEnd(item)
	if err != nil {
		return false
	}
	line := p.yaml[start:end]
	trimmed := bytes.TrimLeft(line, " ")
	return bytes.Equal(trimmed, append([]byte("- target: "+target), p.newline...))
}

func hasYAMLComments(n *yaml.Node) bool {
	return n.HeadComment != "" || n.LineComment != "" || n.FootComment != ""
}

func (p *presentation) indentOf(n *yaml.Node) int {
	if n.Column > 0 {
		return n.Column - 1
	}
	return 0
}
func (p *presentation) blockEnd(n *yaml.Node) (int, error) {
	if n == p.root {
		return len(p.yaml), nil
	}
	if n.Style&yaml.FlowStyle != 0 {
		return 0, fmt.Errorf("flow YAML collection cannot be extended")
	}
	start, err := p.offset(n)
	if err != nil {
		return 0, err
	}
	lineStart := start
	for lineStart > 0 && p.yaml[lineStart-1] != '\n' {
		lineStart--
	}
	base := p.indentOf(n)
	for at := lineStart; at < len(p.yaml); {
		next := bytes.IndexByte(p.yaml[at:], '\n')
		lineEnd := len(p.yaml)
		if next >= 0 {
			lineEnd = at + next + 1
		}
		line := bytes.TrimRight(p.yaml[at:lineEnd], "\r\n")
		trimmed := bytes.TrimSpace(line)
		if at > lineStart && len(trimmed) > 0 && trimmed[0] != '#' {
			indent := len(line) - len(bytes.TrimLeft(line, " "))
			if indent <= base {
				return at, nil
			}
		}
		at = lineEnd
	}
	return len(p.yaml), nil
}
