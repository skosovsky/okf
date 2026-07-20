package mutation

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/skosovsky/okf/bundle"
	"gopkg.in/yaml.v3"
)

// yamlResolver maps yaml.v3 positions back to original frontmatter bytes. It
// deliberately owns no normalized representation: all ranges address raw YAML.
type yamlResolver struct {
	source []byte
	lines  []int // start offset of 1-based YAML lines
	ctx    context.Context
}

type yamlScalarSpan struct {
	Span  SourceSpan
	Style yaml.Style
}

func newYAMLResolver(source []byte) (*yamlResolver, error) {
	return newYAMLResolverContext(context.Background(), source)
}

func newYAMLResolverContext(ctx context.Context, source []byte) (*yamlResolver, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	_, valid, err := validateUTF8Context(ctx, source)
	if err != nil {
		return nil, err
	}
	if !valid {
		return nil, fmt.Errorf("%w: invalid UTF-8", bundle.ErrInvalidEncoding)
	}
	r := &yamlResolver{source: source, lines: []int{0}, ctx: ctx}
	for i := 0; i < len(source); i++ {
		if i&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if source[i] == '\n' && i+1 < len(source) {
			r.lines = append(r.lines, i+1)
		}
	}
	return r, ctx.Err()
}

func yamlPresentationError(code string, err error, span SourceSpan) error {
	return &PresentationError{Code: code, Format: "yaml", Location: span, Err: err}
}

// coordinate maps a yaml.v3 (Line, Column) pair to a byte offset in the raw
// frontmatter. yaml.v3 advances Column by Unicode characters (runes), not
// bytes; columns are 1-based within the physical line. CRLF keeps CR in the
// line and treats LF as the line terminator (next entry in r.lines).
func (r *yamlResolver) coordinate(line, column int) (int, error) {
	if line < 1 || line > len(r.lines) || column < 1 {
		span := SourceSpan{Start: 0, End: len(r.source)}
		if line >= 1 && line <= len(r.lines) {
			start := r.lines[line-1]
			end := len(r.source)
			if line < len(r.lines) {
				end = r.lines[line] - 1
			}
			if len(r.source) > 0 {
				end = min(max(start+1, end), len(r.source))
			}
			span = SourceSpan{Start: start, End: end}
		}
		return 0, r.presentationError("invalid_coordinate", ErrUnsupportedPresentation, span)
	}
	lineStart := r.lines[line-1]
	lineEnd := len(r.source)
	if line < len(r.lines) {
		lineEnd = r.lines[line] - 1 // excludes LF, retains CR for columns
	}
	at := lineStart
	for col := 1; col < column; col++ {
		if col&4095 == 0 {
			if err := r.ctx.Err(); err != nil {
				return 0, err
			}
		}
		if at >= lineEnd {
			end := lineEnd
			if len(r.source) > 0 {
				end = min(max(lineStart+1, lineEnd), len(r.source))
			}
			return 0, r.presentationError("coordinate_outside_source", ErrUnsupportedPresentation, SourceSpan{Start: lineStart, End: end})
		}
		ru, size := utf8.DecodeRune(r.source[at:lineEnd])
		if ru == utf8.RuneError && size == 1 {
			return 0, fmt.Errorf("%w: invalid UTF-8", bundle.ErrInvalidEncoding)
		}
		at += size
	}
	if at > lineEnd {
		end := lineEnd
		if len(r.source) > 0 {
			end = min(max(lineStart+1, lineEnd), len(r.source))
		}
		return 0, r.presentationError("coordinate_outside_source", ErrUnsupportedPresentation, SourceSpan{Start: lineStart, End: end})
	}
	return at, nil
}

func (r *yamlResolver) scalar(n *yaml.Node) (yamlScalarSpan, error) {
	if err := r.ctx.Err(); err != nil {
		return yamlScalarSpan{}, err
	}
	if n == nil || n.Kind != yaml.ScalarNode || n.Tag != "!!str" || n.Anchor != "" || n.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		span := SourceSpan{}
		if n != nil {
			if at, err := r.coordinate(n.Line, n.Column); err == nil {
				span = SourceSpan{Start: at, End: at}
			}
		}
		return yamlScalarSpan{}, r.presentationError("unsupported_scalar", ErrUnsupportedPresentation, span)
	}
	at, err := r.coordinate(n.Line, n.Column)
	if err != nil {
		return yamlScalarSpan{}, err
	}
	// yaml.v3 normalizes non-specific/default tags, including a lone `!`, so
	// raw syntax at the node coordinate and immediately before a block
	// collection must be proved independently of Node.Tag/Style.
	if provenance, ok, err := r.nodeSyntaxProvenanceContext(n, at); err != nil {
		return yamlScalarSpan{}, err
	} else if ok {
		return yamlScalarSpan{}, r.presentationError("explicit_tag", ErrUnsupportedPresentation, provenance)
	}
	end, style, err := r.scalarEnd(n, at)
	if err != nil {
		return yamlScalarSpan{}, err
	}
	span := SourceSpan{Start: at, End: end}
	matches := span.valid(len(r.source)) && r.matchesNode(span, n, style)
	if err := r.ctx.Err(); err != nil {
		return yamlScalarSpan{}, err
	}
	if !matches {
		return yamlScalarSpan{}, r.presentationError("raw_semantic_mismatch", ErrUnsupportedPresentation, span)
	}
	return yamlScalarSpan{Span: span, Style: style}, nil
}

// nodeSyntaxProvenance identifies YAML presentation syntax that makes a
// source-preserving edit non-local. It returns frontmatter-local byte offsets.
// Node fields are authoritative except for syntax yaml.v3 normalizes away.
func (r *yamlResolver) nodeSyntaxProvenance(n *yaml.Node, at int) (SourceSpan, bool) {
	span, ok, _ := r.nodeSyntaxProvenanceContext(n, at)
	return span, ok
}

func (r *yamlResolver) nodeSyntaxProvenanceContext(n *yaml.Node, at int) (SourceSpan, bool, error) {
	if err := r.ctx.Err(); err != nil {
		return SourceSpan{}, false, err
	}
	if n == nil || n.Anchor != "" || n.Alias != nil || n.Style&yaml.TaggedStyle != 0 {
		return SourceSpan{Start: at, End: at}, true, nil
	}
	switch n.Kind {
	case yaml.MappingNode:
		if n.Tag != "!!map" {
			return SourceSpan{Start: at, End: at}, true, nil
		}
	case yaml.SequenceNode:
		if n.Tag != "!!seq" {
			return SourceSpan{Start: at, End: at}, true, nil
		}
	case yaml.ScalarNode:
		if n.Tag != "!!str" {
			return SourceSpan{Start: at, End: at}, true, nil
		}
	}
	if n.Kind == yaml.ScalarNode {
		lineStart := bytes.LastIndexByte(r.source[:at], '\n') + 1
		probeEnd := at
		if probeEnd < len(r.source) {
			probeEnd++
		}
		found, err := yamlSyntaxInPrefixContext(r.ctx, r.source[lineStart:probeEnd])
		if err != nil {
			return SourceSpan{}, false, err
		}
		if found {
			return SourceSpan{Start: lineStart, End: probeEnd}, true, nil
		}
		return SourceSpan{}, false, nil
	}
	// yaml.v3 may locate a block collection at a lone non-specific tag rather
	// than at its first child while still normalizing Tag/Style. Unlike a merge
	// key (`<<`) this byte is the collection's own presentation token.
	if at < len(r.source) && (r.source[at] == '!' || r.source[at] == '&' || r.source[at] == '*') {
		end := at + 1
		for end < len(r.source) && !yamlTokenDelimiter(r.source[end]) {
			if end&4095 == 0 {
				if err := r.ctx.Err(); err != nil {
					return SourceSpan{}, false, err
				}
			}
			end++
		}
		return SourceSpan{Start: at, End: end}, true, nil
	}
	// A tag on a block collection belongs to the value, but yaml.v3 locates the
	// collection at its first child on the following line. Inspect only the
	// immediately preceding non-whitespace token, never unrelated earlier YAML.
	end := at
	for end > 0 && yamlSeparationByte(r.source[end-1]) {
		if end&4095 == 0 {
			if err := r.ctx.Err(); err != nil {
				return SourceSpan{}, false, err
			}
		}
		end--
	}
	start := end
	for start > 0 && !yamlTokenDelimiter(r.source[start-1]) {
		if start&4095 == 0 {
			if err := r.ctx.Err(); err != nil {
				return SourceSpan{}, false, err
			}
		}
		start--
	}
	if start < end && (r.source[start] == '!' || r.source[start] == '&' || r.source[start] == '*') {
		return SourceSpan{Start: start, End: end}, true, nil
	}
	return SourceSpan{}, false, r.ctx.Err()
}

// yamlSyntaxInPrefix recognizes only YAML tokens at lexical token boundaries.
// It is not a YAML parser; yaml.v3 has already parsed the document. Its role is
// to preserve provenance that yaml.v3 normalizes (notably default core tags).
func yamlSyntaxInPrefix(prefix []byte) bool {
	found, _ := yamlSyntaxInPrefixContext(context.Background(), prefix)
	return found
}

func yamlSyntaxInPrefixContext(ctx context.Context, prefix []byte) (bool, error) {
	inSingle, inDouble := false, false
	for i := 0; i < len(prefix); i++ {
		if i&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		c := prefix[i]
		if inSingle {
			if c == '\'' {
				if i+1 < len(prefix) && prefix[i+1] == '\'' {
					i++
				} else {
					inSingle = false
				}
			}
			continue
		}
		if inDouble {
			if c == '\\' {
				i++
			} else if c == '"' {
				inDouble = false
			}
			continue
		}
		switch c {
		case '#':
			return false, nil
		case '\'':
			inSingle = true
			continue
		case '"':
			inDouble = true
			continue
		}
		if !yamlTokenBoundary(prefix, i) {
			continue
		}
		if c == '!' {
			return true, nil
		}
		if c == '&' || c == '*' {
			if i+1 < len(prefix) && !yamlTokenDelimiter(prefix[i+1]) {
				return true, nil
			}
		}
		if c == '<' && i+1 < len(prefix) && prefix[i+1] == '<' && (i+2 == len(prefix) || yamlTokenDelimiter(prefix[i+2])) {
			return true, nil
		}
	}
	return false, ctx.Err()
}

func yamlTokenBoundary(raw []byte, at int) bool {
	return at == 0 || yamlTokenDelimiter(raw[at-1])
}

func yamlTokenDelimiter(c byte) bool {
	switch c {
	case ' ', '\t', '\r', '\n', ':', ',', '[', ']', '{', '}', '-', '?':
		return true
	default:
		return false
	}
}

func (r *yamlResolver) scalarEnd(n *yaml.Node, at int) (int, yaml.Style, error) {
	if at >= len(r.source) {
		return 0, 0, r.presentationError("scalar_outside_source", ErrUnsupportedPresentation, SourceSpan{Start: at, End: at})
	}
	switch r.source[at] {
	case '\'':
		for i := at + 1; i < len(r.source); i++ {
			if i&4095 == 0 {
				if err := r.ctx.Err(); err != nil {
					return 0, 0, err
				}
			}
			if r.source[i] == '\'' {
				if i+1 < len(r.source) && r.source[i+1] == '\'' {
					i++
					continue
				}
				return i + 1, yaml.SingleQuotedStyle, nil
			}
		}
	case '"':
		for i := at + 1; i < len(r.source); i++ {
			if i&4095 == 0 {
				if err := r.ctx.Err(); err != nil {
					return 0, 0, err
				}
			}
			if r.source[i] == '\\' {
				i++
				continue
			}
			if r.source[i] == '"' {
				return i + 1, yaml.DoubleQuotedStyle, nil
			}
		}
	default:
		return r.blockPlainScalarEnd(n, at)
	}
	return 0, 0, r.presentationError("unterminated_scalar", ErrUnsupportedPresentation, SourceSpan{Start: at, End: at})
}

// blockPlainScalarEnd finds parser-confirmed physical boundaries for a plain
// scalar. Flow-only punctuation (,[]{}) is ordinary data here. For multiline
// plain values, successive physical line ends are tried until yaml.v3 confirms
// the complete semantic value; comments and mapping separators remain hard
// lexical boundaries.
func (r *yamlResolver) blockPlainScalarEnd(n *yaml.Node, at int) (int, yaml.Style, error) {
	semanticEnd := func(rawEnd int) (int, bool) {
		end := len(bytes.TrimRight(r.source[at:rawEnd], " \t")) + at
		span := SourceSpan{Start: at, End: end}
		return end, end > at && r.matchesNode(span, n, 0)
	}
	for i := at; i < len(r.source); i++ {
		if i&4095 == 0 {
			if err := r.ctx.Err(); err != nil {
				return 0, 0, err
			}
		}
		c := r.source[i]
		if c == '#' && (i == at || r.source[i-1] == ' ' || r.source[i-1] == '\t') {
			if end, ok := semanticEnd(i); ok {
				return end, 0, nil
			}
			return 0, 0, r.presentationError("raw_semantic_mismatch", ErrUnsupportedPresentation, SourceSpan{Start: at, End: i})
		}
		if c == ':' && (i+1 == len(r.source) || yamlSeparationByte(r.source[i+1])) {
			if end, ok := semanticEnd(i); ok {
				return end, 0, nil
			}
			return 0, 0, r.presentationError("raw_semantic_mismatch", ErrUnsupportedPresentation, SourceSpan{Start: at, End: i})
		}
		if c == '\r' || c == '\n' {
			lineEnd := i
			if end, ok := semanticEnd(lineEnd); ok {
				return end, 0, nil
			}
			if c == '\r' && i+1 < len(r.source) && r.source[i+1] == '\n' {
				i++
			}
		}
	}
	if end, ok := semanticEnd(len(r.source)); ok {
		return end, 0, nil
	}
	return 0, 0, r.presentationError("raw_semantic_mismatch", ErrUnsupportedPresentation, SourceSpan{Start: at, End: len(r.source)})
}

func (r *yamlResolver) presentationError(code string, cause error, span SourceSpan) error {
	if len(r.source) > 0 && span.Start == span.End {
		if span.End < len(r.source) {
			span.End++
		} else if span.Start > 0 {
			span.Start--
		}
	}
	return yamlLocalPresentationError(code, cause, span)
}

func yamlSeparationByte(value byte) bool {
	return value == ' ' || value == '\t' || value == '\r' || value == '\n'
}

func (r *yamlResolver) matchesNode(span SourceSpan, n *yaml.Node, style yaml.Style) bool {
	if r.ctx.Err() != nil {
		return false
	}
	var doc yaml.Node
	// Never append to a subslice of source: a scalar followed by ':' (mapping
	// key) would otherwise corrupt the original presentation while proving it.
	token := append([]byte(nil), r.source[span.Start:span.End]...)
	probe := append([]byte("v: "), token...)
	probe = append(probe, '\n')
	if yaml.Unmarshal(probe, &doc) != nil || len(doc.Content) != 1 {
		return false
	}
	root := doc.Content[0]
	return r.ctx.Err() == nil && root.Kind == yaml.MappingNode && len(root.Content) == 2 && root.Content[1].Kind == yaml.ScalarNode && root.Content[1].Tag == n.Tag && root.Content[1].Value == n.Value && root.Content[1].Style == style
}

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
	if value == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\t") {
		return false
	}
	var doc yaml.Node
	if yaml.Unmarshal([]byte("v: "+value+"\n"), &doc) != nil || len(doc.Content) != 1 {
		return false
	}
	r := doc.Content[0]
	return r.Kind == yaml.MappingNode && len(r.Content) == 2 && r.Content[1].Kind == yaml.ScalarNode && r.Content[1].Tag == "!!str" && r.Content[1].Value == value
}
