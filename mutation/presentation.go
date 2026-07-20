package mutation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/internal/documentlayout"
	"gopkg.in/yaml.v3"
)

type bytePatch struct {
	Start, End int
	Text       []byte
	Edit       *SemanticEdit
}

// SemanticEdit is the semantic contract carried by a YAML byte patch.  Offsets
// alone are not a contract: a valid YAML token at the wrong node is still a
// corrupt mutation.
type SemanticEdit struct {
	Path          []int
	Before, After string
	Expected      *yamlSemanticNode
}

type yamlSemanticNode struct {
	Kind    yaml.Kind
	Tag     string
	Value   string
	Anchor  string
	Alias   bool
	Content []*yamlSemanticNode
}

func applyBytePatches(in []byte, patches []bytePatch) ([]byte, error) {
	return applyBytePatchesContext(context.Background(), in, patches)
}

func applyBytePatchesContext(ctx context.Context, in []byte, patches []bytePatch) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sort.Slice(patches, func(i, j int) bool { return patches[i].Start < patches[j].Start })
	last := 0
	finalLength := len(in)
	for _, p := range patches {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if p.Start < last || !(SourceSpan{Start: p.Start, End: p.End}).valid(len(in)) {
			return nil, yamlPresentationError("invalid_patch", ErrUnsupportedPresentation, SourceSpan{Start: p.Start, End: p.End})
		}
		delta := len(p.Text) - (p.End - p.Start)
		if delta > 0 && finalLength > int(^uint(0)>>1)-delta {
			return nil, yamlPresentationError("invalid_patch", ErrUnsupportedPresentation, SourceSpan{Start: p.Start, End: p.End})
		}
		finalLength += delta
		if finalLength < 0 {
			return nil, yamlPresentationError("invalid_patch", ErrUnsupportedPresentation, SourceSpan{Start: p.Start, End: p.End})
		}
		last = p.End
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]byte, finalLength)
	sourceAt, outAt := len(in), len(out)
	for index := len(patches) - 1; index >= 0; index-- {
		patch := patches[index]
		trailing := in[patch.End:sourceAt]
		outAt -= len(trailing)
		if err := copyBytesIntoContext(ctx, out[outAt:outAt+len(trailing)], trailing); err != nil {
			return nil, err
		}
		outAt -= len(patch.Text)
		if err := copyBytesIntoContext(ctx, out[outAt:outAt+len(patch.Text)], patch.Text); err != nil {
			return nil, err
		}
		sourceAt = patch.Start
	}
	outAt -= sourceAt
	if outAt != 0 {
		return nil, yamlPresentationError("invalid_patch", ErrUnsupportedPresentation, SourceSpan{Start: 0, End: len(in)})
	}
	if err := copyBytesIntoContext(ctx, out[:sourceAt], in[:sourceAt]); err != nil {
		return nil, err
	}
	return out, ctx.Err()
}

func copyBytesIntoContext(ctx context.Context, destination, source []byte) error {
	if len(destination) != len(source) {
		return yamlPresentationError("invalid_patch", ErrUnsupportedPresentation, SourceSpan{Start: 0, End: len(source)})
	}
	const chunkSize = 64 << 10
	for len(source) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := min(len(source), chunkSize)
		copy(destination[:n], source[:n])
		destination, source = destination[n:], source[n:]
	}
	return ctx.Err()
}

func appendBytesContext(ctx context.Context, dst, source []byte) ([]byte, error) {
	const chunkSize = 64 << 10
	for len(source) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n := min(len(source), chunkSize)
		dst = append(dst, source[:n]...)
		source = source[n:]
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return dst, nil
}

type presentation struct {
	data, yaml         []byte
	yamlStart, yamlEnd int
	root               *yaml.Node
	newline            []byte
	resolver           *yamlResolver
	paths              map[*yaml.Node][]int
	parents            map[*yaml.Node]*yaml.Node
}

func parsePresentation(data []byte) (*presentation, error) {
	return parsePresentationContext(context.Background(), data)
}

// parsePresentationContext only treats the bytes between the recognized
// opening and closing Markdown delimiters as YAML.  In particular, a body
// thematic break or setext underline is never a second YAML document.
func parsePresentationContext(ctx context.Context, data []byte) (*presentation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	invalid, valid, err := validateUTF8Context(ctx, data)
	if err != nil {
		return nil, err
	}
	if !valid {
		format := "markdown"
		if layout, ok, err := documentlayout.Split(data); err == nil && ok && invalid.Start < layout.BodyStart {
			format = "yaml"
		}
		return nil, invalidUTF8PresentationErrorAt(format, invalid)
	}
	layout, ok, splitErr := documentlayout.Split(data)
	if splitErr != nil || !ok {
		location := SourceSpan{}
		if documentlayout.HasOpeningDelimiter(data) {
			location = SourceSpan{Start: layout.YAMLStart, End: len(data)}
		}
		return nil, yamlPresentationError("unterminated_frontmatter", fmt.Errorf("%w: %w", ErrUnsupportedPresentation, bundle.ErrUnterminatedFrontmatter), location)
	}
	first, end := layout.YAMLStart, layout.YAMLEnd
	var doc yaml.Node
	if span, ok, err := leadingYAMLStreamTokenContext(ctx, data[first:end]); err != nil {
		return nil, err
	} else if ok {
		return nil, yamlLocalPresentationError("yaml_stream_syntax", ErrUnsupportedPresentation, span)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data[first:end]))
	if err := decoder.Decode(&doc); err != nil {
		return nil, yamlLocalPresentationError("yaml_syntax", fmt.Errorf("%w: %w", ErrUnsupportedPresentation, err), yamlDocumentDiagnosticSpan(data[first:end]))
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, yamlLocalPresentationError("invalid_frontmatter", fmt.Errorf("%w: %w", ErrUnsupportedPresentation, bundle.ErrInvalidFrontmatter), yamlDocumentDiagnosticSpan(data[first:end]))
	}
	r, err := newYAMLResolverContext(ctx, data[first:end])
	if err != nil {
		return nil, yamlLocalPresentationError("yaml_resolver", fmt.Errorf("%w: %w", ErrUnsupportedPresentation, err), yamlDocumentDiagnosticSpan(data[first:end]))
	}
	// Once the closing delimiter is recognized, every following byte is
	// Markdown body. Stream syntax is rejected only when yaml.v3 has established
	// the first document and the raw token is proven outside quoted scalars.
	if err := supportedYAMLDocumentContext(ctx, data[first:end], &doc, r); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err == nil {
		return nil, yamlLocalPresentationError("yaml_stream_syntax", ErrUnsupportedPresentation, yamlDocumentDiagnosticSpan(data[first:end]))
	} else if err != io.EOF {
		return nil, yamlLocalPresentationError("yaml_syntax", fmt.Errorf("%w: %w", ErrUnsupportedPresentation, err), yamlDocumentDiagnosticSpan(data[first:end]))
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	nl := []byte("\n")
	if containsCRLF, err := containsCRLFContext(ctx, data[first:end]); err != nil {
		return nil, err
	} else if containsCRLF {
		nl = []byte("\r\n")
	}
	p := &presentation{data: data, yaml: data[first:end], yamlStart: first, yamlEnd: end, root: doc.Content[0], newline: nl, resolver: r, paths: map[*yaml.Node][]int{}, parents: map[*yaml.Node]*yaml.Node{}}
	if err := p.indexPathsContext(ctx, p.root, nil, nil); err != nil {
		return nil, err
	}
	return p, nil
}

func yamlDocumentDiagnosticSpan(source []byte) SourceSpan {
	if len(source) == 0 {
		// The byte immediately following an empty YAML slice is the recognized
		// closing delimiter. A one-byte local marker keeps the internal typed
		// rejection concrete; planner remapping places it at that delimiter.
		return SourceSpan{Start: 0, End: 1}
	}
	return SourceSpan{Start: 0, End: len(source)}
}

func leadingYAMLStreamToken(source []byte) (SourceSpan, bool) {
	span, ok, _ := leadingYAMLStreamTokenContext(context.Background(), source)
	return span, ok
}

func leadingYAMLStreamTokenContext(ctx context.Context, source []byte) (SourceSpan, bool, error) {
	for at := 0; at < len(source); {
		if err := ctx.Err(); err != nil {
			return SourceSpan{}, false, err
		}
		next, err := indexByteContext(ctx, source[at:], '\n')
		if err != nil {
			return SourceSpan{}, false, err
		}
		end := len(source)
		if next >= 0 {
			end = at + next
		}
		line := bytes.TrimSuffix(source[at:end], []byte("\r"))
		blankOrComment, err := yamlBlankOrCommentLineContext(ctx, line)
		if err != nil {
			return SourceSpan{}, false, err
		}
		if blankOrComment {
			if next < 0 {
				return SourceSpan{}, false, ctx.Err()
			}
			at = end + 1
			continue
		}
		if yamlStreamTokenLine(line) {
			return SourceSpan{Start: at, End: end}, true, nil
		}
		return SourceSpan{}, false, nil
	}
	return SourceSpan{}, false, ctx.Err()
}

// supportedYAMLDocument rejects stream syntax before yaml.v3 can normalize it
// into an ordinary node tree. The outer Markdown delimiters are deliberately
// not part of source, so a conventional leading and closing "---" remains
// valid while inner directives and document markers fail closed.
func supportedYAMLDocument(source []byte, doc *yaml.Node, resolver *yamlResolver) error {
	return supportedYAMLDocumentContext(context.Background(), source, doc, resolver)
}

func supportedYAMLDocumentContext(ctx context.Context, source []byte, doc *yaml.Node, resolver *yamlResolver) error {
	quoted, err := quotedYAMLScalarSpansContext(ctx, doc, resolver)
	if err != nil {
		return err
	}
	for at := 0; at < len(source); {
		if err := ctx.Err(); err != nil {
			return err
		}
		next, err := indexByteContext(ctx, source[at:], '\n')
		if err != nil {
			return err
		}
		end := len(source)
		if next >= 0 {
			end = at + next
		}
		line := bytes.TrimSuffix(source[at:end], []byte("\r"))
		inside, err := offsetInsideAnySpanContext(ctx, at, quoted)
		if err != nil {
			return err
		}
		if !inside && yamlStreamTokenLine(line) {
			return yamlLocalPresentationError("yaml_stream_syntax", ErrUnsupportedPresentation, SourceSpan{Start: at, End: end})
		}
		if next < 0 {
			break
		}
		at = end + 1
	}
	return ctx.Err()
}

func indexByteContext(ctx context.Context, source []byte, wanted byte) (int, error) {
	const chunkSize = 64 << 10
	for offset := 0; offset < len(source); offset += chunkSize {
		if err := ctx.Err(); err != nil {
			return -1, err
		}
		end := min(offset+chunkSize, len(source))
		if found := bytes.IndexByte(source[offset:end], wanted); found >= 0 {
			return offset + found, nil
		}
	}
	return -1, ctx.Err()
}

func containsCRLFContext(ctx context.Context, source []byte) (bool, error) {
	for at := 0; at+1 < len(source); at++ {
		if at&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		if source[at] == '\r' && source[at+1] == '\n' {
			return true, nil
		}
	}
	return false, ctx.Err()
}

func yamlBlankOrCommentLineContext(ctx context.Context, line []byte) (bool, error) {
	for index, value := range line {
		if index&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		if value == ' ' || value == '\t' {
			continue
		}
		return value == '#', nil
	}
	return true, ctx.Err()
}

// yamlStreamTokenLine recognizes only column-zero YAML stream syntax. A
// document marker may be followed by separation whitespace and an optional
// comment; arbitrary suffix text is an ordinary scalar. Directives likewise
// require a token boundary after their name.
func yamlStreamTokenLine(line []byte) bool {
	line = bytes.TrimSuffix(line, []byte("\r"))
	for _, directive := range [][]byte{[]byte("%YAML"), []byte("%TAG")} {
		if bytes.HasPrefix(line, directive) && yamlSeparationTail(line[len(directive):], false) {
			return true
		}
	}
	for _, marker := range [][]byte{[]byte("---"), []byte("...")} {
		if bytes.HasPrefix(line, marker) && yamlSeparationTail(line[len(marker):], true) {
			return true
		}
	}
	return false
}

func yamlSeparationTail(tail []byte, commentOnly bool) bool {
	if len(tail) == 0 {
		return true
	}
	if tail[0] != ' ' && tail[0] != '\t' {
		return false
	}
	if !commentOnly {
		return true
	}
	tail = bytes.TrimLeft(tail, " \t")
	return len(tail) == 0 || tail[0] == '#'
}

func quotedYAMLScalarSpans(doc *yaml.Node, resolver *yamlResolver) ([]SourceSpan, error) {
	return quotedYAMLScalarSpansContext(context.Background(), doc, resolver)
}

func quotedYAMLScalarSpansContext(ctx context.Context, doc *yaml.Node, resolver *yamlResolver) ([]SourceSpan, error) {
	spans := make([]SourceSpan, 0)
	var visit func(*yaml.Node) error
	visit = func(n *yaml.Node) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if n == nil {
			return nil
		}
		if n.Kind == yaml.ScalarNode && n.Style&(yaml.SingleQuotedStyle|yaml.DoubleQuotedStyle) != 0 {
			start, err := resolver.coordinate(n.Line, n.Column)
			if err != nil {
				return err
			}
			end, _, err := resolver.scalarEnd(n, start)
			if err != nil {
				return err
			}
			spans = append(spans, SourceSpan{Start: start, End: end})
		}
		for _, child := range n.Content {
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(doc); err != nil {
		return nil, err
	}
	return spans, ctx.Err()
}

func offsetInsideAnySpan(offset int, spans []SourceSpan) bool {
	inside, _ := offsetInsideAnySpanContext(context.Background(), offset, spans)
	return inside
}

func offsetInsideAnySpanContext(ctx context.Context, offset int, spans []SourceSpan) (bool, error) {
	for _, span := range spans {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if offset >= span.Start && offset < span.End {
			return true, nil
		}
	}
	return false, ctx.Err()
}

func (p *presentation) indexPathsContext(ctx context.Context, n *yaml.Node, path []int, parent *yaml.Node) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if n == nil {
		return nil
	}
	p.paths[n] = append([]int(nil), path...)
	p.parents[n] = parent
	for i, child := range n.Content {
		if err := p.indexPathsContext(ctx, child, append(path, i), n); err != nil {
			return err
		}
	}
	return nil
}

func semanticYAML(n *yaml.Node) *yamlSemanticNode {
	out, _ := semanticYAMLContext(context.Background(), n)
	return out
}

func semanticYAMLContext(ctx context.Context, n *yaml.Node) (*yamlSemanticNode, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if n == nil {
		return nil, nil
	}
	out := &yamlSemanticNode{Kind: n.Kind, Tag: n.Tag, Value: n.Value, Anchor: n.Anchor, Alias: n.Alias != nil}
	for _, child := range n.Content {
		semantic, err := semanticYAMLContext(ctx, child)
		if err != nil {
			return nil, err
		}
		out.Content = append(out.Content, semantic)
	}
	return out, nil
}

func equalSemanticYAMLContext(ctx context.Context, left, right *yamlSemanticNode) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if left == nil || right == nil {
		return left == right, nil
	}
	if left.Kind != right.Kind || left.Tag != right.Tag || left.Anchor != right.Anchor || left.Alias != right.Alias || len(left.Content) != len(right.Content) {
		return false, nil
	}
	valueEqual, err := stringsEqualContext(ctx, left.Value, right.Value)
	if err != nil || !valueEqual {
		return valueEqual, err
	}
	for index := range left.Content {
		equal, err := equalSemanticYAMLContext(ctx, left.Content[index], right.Content[index])
		if err != nil || !equal {
			return equal, err
		}
	}
	return true, ctx.Err()
}

func stringsEqualContext(ctx context.Context, left, right string) (bool, error) {
	if len(left) != len(right) {
		return false, nil
	}
	const chunkSize = 64 << 10
	for at := 0; at < len(left); at += chunkSize {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		end := min(at+chunkSize, len(left))
		if left[at:end] != right[at:end] {
			return false, nil
		}
	}
	return true, ctx.Err()
}

func nodeAtPath(n *yaml.Node, path []int) *yaml.Node {
	for _, i := range path {
		if n == nil || i < 0 || i >= len(n.Content) {
			return nil
		}
		n = n.Content[i]
	}
	return n
}

// VerifyPatched proves both the declared scalar transitions and equality of
// every operation-relevant untouched YAML node.  Raw bytes outside spans are
// checked separately by patchYAML.
func (p *presentation) VerifyPatched(out []byte, patches []bytePatch) error {
	return p.VerifyPatchedContext(context.Background(), out, patches)
}

func (p *presentation) VerifyPatchedContext(ctx context.Context, out []byte, patches []bytePatch) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	after, err := parsePresentationContext(ctx, out)
	if err != nil {
		return err
	}
	hasContract := false
	for _, patch := range patches {
		if err := ctx.Err(); err != nil {
			return err
		}
		hasContract = hasContract || patch.Edit != nil
	}
	if len(patches) == 0 {
		return nil
	}
	if !hasContract {
		return yamlPresentationError("missing_semantic_edit", ErrUnsupportedPresentation, bytePatchesSpan(patches))
	}
	afterSemantic, err := semanticYAMLContext(ctx, after.root)
	if err != nil {
		return err
	}
	hasProjection := false
	for _, patch := range patches {
		if err := ctx.Err(); err != nil {
			return err
		}
		if patch.Edit == nil {
			return yamlPresentationError("missing_semantic_edit", ErrUnsupportedPresentation, SourceSpan{Start: patch.Start, End: patch.End})
		}
		if patch.Edit.Expected != nil {
			hasProjection = true
			equal, err := equalSemanticYAMLContext(ctx, patch.Edit.Expected, afterSemantic)
			if err != nil {
				return err
			}
			if !equal {
				return yamlPresentationError("semantic_edit_mismatch", ErrUnsupportedPresentation, SourceSpan{Start: patch.Start, End: patch.End})
			}
		}
	}
	if hasProjection {
		return nil
	}
	expected, err := semanticYAMLContext(ctx, p.root)
	if err != nil {
		return err
	}
	for _, patch := range patches {
		if err := ctx.Err(); err != nil {
			return err
		}
		if patch.Edit.Expected != nil {
			continue
		}
		before := nodeAtPath(p.root, patch.Edit.Path)
		afterNode := nodeAtPath(after.root, patch.Edit.Path)
		if before == nil || afterNode == nil || before.Value != patch.Edit.Before || afterNode.Value != patch.Edit.After {
			return yamlPresentationError("semantic_edit_mismatch", ErrUnsupportedPresentation, SourceSpan{Start: patch.Start, End: patch.End})
		}
		expectedNode := nodeAtPathNode(expected, patch.Edit.Path)
		if expectedNode == nil {
			return yamlPresentationError("semantic_edit_path", ErrUnsupportedPresentation, SourceSpan{Start: patch.Start, End: patch.End})
		}
		expectedNode.Value = patch.Edit.After
	}
	equal, err := equalSemanticYAMLContext(ctx, expected, afterSemantic)
	if err != nil {
		return err
	}
	if !equal {
		return yamlPresentationError("semantic_projection_mismatch", ErrUnsupportedPresentation, bytePatchesSpan(patches))
	}
	return nil
}

func bytePatchesSpan(patches []bytePatch) SourceSpan {
	if len(patches) == 0 {
		return SourceSpan{}
	}
	span := SourceSpan{Start: patches[0].Start, End: patches[0].End}
	for _, patch := range patches[1:] {
		if patch.Start < span.Start {
			span.Start = patch.Start
		}
		if patch.End > span.End {
			span.End = patch.End
		}
	}
	return span
}

func bytePatchesDiagnosticSpan(sourceLength int, patches []bytePatch) SourceSpan {
	span := bytePatchesSpan(patches)
	if sourceLength <= 0 || span.Start != span.End {
		return span
	}
	if span.End < sourceLength {
		span.End++
	} else if span.Start > 0 {
		span.Start--
	}
	return span
}

func nodeAtPathNode(n *yamlSemanticNode, path []int) *yamlSemanticNode {
	for _, i := range path {
		if n == nil || i < 0 || i >= len(n.Content) {
			return nil
		}
		n = n.Content[i]
	}
	return n
}

func mappingValues(n *yaml.Node, key string) []*yaml.Node {
	var out []*yaml.Node
	if n == nil || n.Kind != yaml.MappingNode {
		return out
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Kind == yaml.ScalarNode && n.Content[i].Value == key {
			out = append(out, n.Content[i+1])
		}
	}
	return out
}

// yamlNodeSpan returns a frontmatter-local source range for diagnostics. It
// prefers the resolver's exact scalar ownership and otherwise points at the
// parser coordinate token. Collection coordinates are still materially more
// useful than the frontmatter-opening fallback used at the planner boundary.
func (p *presentation) yamlNodeSpan(n *yaml.Node) SourceSpan {
	if p == nil || p.resolver == nil || n == nil {
		return SourceSpan{}
	}
	if n.Kind == yaml.ScalarNode {
		if scalar, err := p.resolver.scalar(n); err == nil {
			return scalar.Span
		}
	}
	at, err := p.resolver.coordinate(n.Line, n.Column)
	if err != nil || at < 0 || at > len(p.yaml) {
		return SourceSpan{}
	}
	end := at
	for end < len(p.yaml) && !yamlSeparationByte(p.yaml[end]) {
		end++
	}
	if end == at && end < len(p.yaml) {
		_, size := utf8.DecodeRune(p.yaml[end:])
		end += max(size, 1)
	}
	return SourceSpan{Start: at, End: end}
}

func (p *presentation) yamlNodesSpan(nodes ...*yaml.Node) SourceSpan {
	span := SourceSpan{}
	for _, node := range nodes {
		candidate := p.yamlNodeSpan(node)
		if candidate == (SourceSpan{}) {
			continue
		}
		if span == (SourceSpan{}) || candidate.Start < span.Start {
			span.Start = candidate.Start
		}
		if candidate.End > span.End {
			span.End = candidate.End
		}
	}
	return span
}

func (p *presentation) yamlNodeError(code string, cause error, nodes ...*yaml.Node) error {
	span := p.yamlNodesSpan(nodes...)
	if span == (SourceSpan{}) && p != nil && len(p.yaml) > 0 {
		// yaml.v3 can publish a semantic node whose coordinate falls on an
		// irregular CR boundary that the raw resolver correctly refuses to own.
		// Fail closed with the concrete frontmatter range, never an empty marker.
		span = SourceSpan{Start: 0, End: len(p.yaml)}
	}
	if p != nil && len(p.yaml) > 0 && span.Start == span.End {
		if span.End < len(p.yaml) {
			span.End++
		} else if span.Start > 0 {
			span.Start--
		}
	}
	return yamlLocalPresentationError(code, cause, span)
}

func mappingKeyNodes(n *yaml.Node, key string) []*yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	out := make([]*yaml.Node, 0, 1)
	for i := 0; i+1 < len(n.Content); i += 2 {
		candidate := n.Content[i]
		if candidate.Kind == yaml.ScalarNode && candidate.Value == key {
			out = append(out, candidate)
		}
	}
	return out
}

// rejectMergedKey rejects a touched key whose value can originate from YAML
// merge provenance. Merge resolution is useful for loading semantics but has
// no single raw owner that a lossless mutation may rewrite or insert beside.
func (p *presentation) rejectMergedKey(n *yaml.Node, touchedKey string) error {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, value := n.Content[i], n.Content[i+1]
		if key == nil || key.Tag != "!!merge" {
			continue
		}
		if mergedValueProvidesKey(value, touchedKey, make(map[*yaml.Node]bool)) {
			return p.yamlNodeError("alias_provenance", ErrAmbiguousPresentation, key, value)
		}
	}
	return nil
}

func mergedValueProvidesKey(n *yaml.Node, touchedKey string, visited map[*yaml.Node]bool) bool {
	if n == nil {
		return true
	}
	if visited[n] {
		return false
	}
	visited[n] = true
	if n.Kind == yaml.AliasNode || n.Alias != nil {
		if n.Alias == nil {
			return true
		}
		return mergedValueProvidesKey(n.Alias, touchedKey, visited)
	}
	switch n.Kind {
	case yaml.SequenceNode:
		for _, child := range n.Content {
			if mergedValueProvidesKey(child, touchedKey, visited) {
				return true
			}
		}
		return false
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, value := n.Content[i], n.Content[i+1]
			if key != nil && key.Kind == yaml.ScalarNode && key.Tag == "!!str" && key.Value == touchedKey {
				return true
			}
			if key != nil && key.Tag == "!!merge" && mergedValueProvidesKey(value, touchedKey, visited) {
				return true
			}
		}
		return false
	default:
		return true
	}
}

func (p *presentation) resolvedScalarPatch(n *yaml.Node, value string) (bytePatch, error) {
	if err := p.requireTouchedProvenance(n); err != nil {
		return bytePatch{}, err
	}
	span, err := p.resolver.scalar(n)
	if err != nil {
		return bytePatch{}, err
	}
	path, ok := p.paths[n]
	if !ok {
		return bytePatch{}, yamlLocalPresentationError("unindexed_scalar", ErrUnsupportedPresentation, span.Span)
	}
	return bytePatch{Start: p.yamlStart + span.Span.Start, End: p.yamlStart + span.Span.End, Text: []byte(yamlScalarToken(value, span.Style)), Edit: &SemanticEdit{Path: path, Before: n.Value, After: value}}, nil
}

// requireTouchedProvenance deliberately walks only the chain that produced the
// value being changed.  That keeps opaque extension YAML lossless while
// refusing an alias, merge, tag, anchor, or duplicate key that could make a
// touched value come from more than one source location.
func (p *presentation) requireTouchedProvenance(n *yaml.Node) error {
	for current := n; current != nil; current = p.parents[current] {
		if current.Kind == yaml.AliasNode || current.Alias != nil {
			return p.yamlNodeError("alias_provenance", ErrAmbiguousPresentation, current)
		}
		if current.Anchor != "" {
			return p.yamlNodeError("touched_anchor", ErrUnsupportedPresentation, current)
		}
		if current.Style&yaml.TaggedStyle != 0 {
			return p.yamlNodeError("explicit_tag", ErrUnsupportedPresentation, current)
		}
		if err := p.requireNoSyntaxProvenance(current); err != nil {
			return err
		}
		if current.Style&yaml.FlowStyle != 0 {
			return p.yamlNodeError("touched_flow", ErrUnsupportedPresentation, current)
		}
		parent := p.parents[current]
		if parent == nil || parent.Kind != yaml.MappingNode {
			continue
		}
		if parent.Style&yaml.FlowStyle != 0 {
			return p.yamlNodeError("touched_flow", ErrUnsupportedPresentation, parent)
		}
		index := yamlChildIndex(parent, current)
		if index <= 0 || index%2 == 0 {
			return p.yamlNodeError("unowned_touched_node", ErrUnsupportedPresentation, current)
		}
		key := parent.Content[index-1]
		if key.Value == "<<" && key.Tag == "!!merge" {
			return p.yamlNodeError("alias_provenance", ErrAmbiguousPresentation, key, current)
		}
		if err := p.requireScalarKey(key); err != nil {
			return err
		}
		matches := 0
		for i := 0; i+1 < len(parent.Content); i += 2 {
			candidate := parent.Content[i]
			if candidate.Kind == yaml.ScalarNode && candidate.Tag == "!!str" && candidate.Value == key.Value {
				matches++
			}
		}
		if matches > 1 {
			return p.touchedDuplicateKeyError(parent, key.Value)
		}
	}
	return nil
}

func yamlChildIndex(parent, child *yaml.Node) int {
	for i, candidate := range parent.Content {
		if candidate == child {
			return i
		}
	}
	return -1
}

func (p *presentation) touchedDuplicateKeyError(parent *yaml.Node, key string) error {
	nodes := mappingKeyNodes(parent, key)
	switch key {
	case "relations":
		return p.yamlNodeError("duplicate_relations", ErrAmbiguousPresentation, nodes...)
	case "target":
		return p.yamlNodeError("duplicate_target", ErrAmbiguousPresentation, nodes...)
	case "id", "anchor":
		return p.yamlNodeError("duplicate_canonical_fragment", ErrAmbiguousPresentation, nodes...)
	}
	if owner := p.mappingOwnerKey(parent); owner == "relations" {
		return p.yamlNodeError("duplicate_relation_type", ErrAmbiguousPresentation, nodes...)
	}
	return p.yamlNodeError("duplicate_touched_key", ErrUnsupportedPresentation, nodes...)
}

func (p *presentation) mappingOwnerKey(mapping *yaml.Node) string {
	// This helper is intentionally syntax-free: callers use it only for error
	// classification after provenance for the actual route has been proved.
	owner := p.parents[mapping]
	if owner == nil || owner.Kind != yaml.MappingNode {
		return ""
	}
	index := yamlChildIndex(owner, mapping)
	if index <= 0 || index%2 == 0 {
		return ""
	}
	key := owner.Content[index-1]
	if key.Kind == yaml.ScalarNode && key.Tag == "!!str" {
		return key.Value
	}
	return ""
}

// requireScalarKey proves a structural key against the same raw scalar
// resolver used for values. Plain, single-quoted, and double-quoted string
// keys are supported; complex, tagged, anchored, aliased, flow, and block
// scalar keys remain fail-closed.
func (p *presentation) requireScalarKey(key *yaml.Node) error {
	if key == nil || key.Kind == yaml.AliasNode || key.Alias != nil {
		return p.yamlNodeError("alias_provenance", ErrAmbiguousPresentation, key)
	}
	if key.Tag == "!!merge" {
		return p.yamlNodeError("alias_provenance", ErrAmbiguousPresentation, key)
	}
	if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || key.Anchor != "" ||
		key.Style&(yaml.LiteralStyle|yaml.FoldedStyle|yaml.FlowStyle|yaml.TaggedStyle) != 0 {
		return p.yamlNodeError("unsupported_key", ErrUnsupportedPresentation, key)
	}
	if err := p.requireNoSyntaxProvenance(key); err != nil {
		return err
	}
	if _, err := p.resolver.scalar(key); err != nil {
		return err
	}
	return nil
}

func (p *presentation) requireNoSyntaxProvenance(n *yaml.Node) error {
	at, err := p.resolver.coordinate(n.Line, n.Column)
	if err != nil {
		return err
	}
	if provenance, ok, err := p.resolver.nodeSyntaxProvenanceContext(n, at); err != nil {
		return err
	} else if ok {
		return yamlLocalPresentationError("explicit_tag", ErrUnsupportedPresentation, provenance)
	}
	return nil
}

// requireInsertionProvenance proves that the collection and every mapping that
// owns it belong to the lossless block-YAML subset. Keeping this at the
// presentation boundary makes a new insertion branch unable to bypass it.
func (p *presentation) requireInsertionProvenance(n *yaml.Node) error {
	return p.requireInsertionProvenanceContext(context.Background(), n)
}

func (p *presentation) requireInsertionProvenanceContext(ctx context.Context, n *yaml.Node) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if n == nil || (n.Kind != yaml.MappingNode && n.Kind != yaml.SequenceNode) {
		return p.yamlNodeError("unsupported_block", ErrUnsupportedPresentation, n)
	}
	if err := p.requireTouchedProvenance(n); err != nil {
		return err
	}
	return ctx.Err()
}

func (p *presentation) patchYAML(patches []bytePatch) ([]byte, error) {
	return p.patchYAMLContext(context.Background(), patches)
}

func (p *presentation) patchYAMLContext(ctx context.Context, patches []bytePatch) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out, err := applyBytePatchesContext(ctx, p.data, patches)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := p.VerifyPatchedContext(ctx, out, patches); err != nil {
		// Verification reparses the generated buffer, whose offsets can exceed
		// the original source after an insertion. Public mutation diagnostics
		// always address the original file, so attribute a generated-output
		// failure to the original patch union and mark it full-file-relative.
		var presentation *PresentationError
		if errors.As(err, &presentation) {
			copy := *presentation
			copy.Location = bytePatchesDiagnosticSpan(len(p.data), patches)
			copy.yamlRelative = false
			return nil, &copy
		}
		return nil, err
	}
	equal, err := bytesOutsidePatchesEqualContext(ctx, p.data, out, patches)
	if err != nil {
		return nil, err
	}
	if !equal {
		return nil, yamlPresentationError("outside_span_changed", ErrUnsupportedPresentation, bytePatchesDiagnosticSpan(len(p.data), patches))
	}
	return out, nil
}

func relationTargetPatches(p *presentation, from, to bundle.ConceptID, oldFragment, newFragment string) ([]bytePatch, error) {
	return relationTargetPatchesContext(context.Background(), p, from, to, oldFragment, newFragment)
}

func relationTargetPatchesContext(ctx context.Context, p *presentation, from, to bundle.ConceptID, oldFragment, newFragment string) ([]bytePatch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var patches []bytePatch
	var first error
	walkMappings(p.root, func(n *yaml.Node) {
		if first == nil {
			first = ctx.Err()
		}
		if first != nil {
			return
		}
		if err := p.rejectMergedRootRelationTarget(n, from, oldFragment); err != nil {
			first = err
			return
		}
		rels := mappingValues(n, "relations")
		for _, relations := range rels {
			relations = yamlAliasTarget(relations)
			if err := p.rejectMergedRelationsTarget(relations, from, oldFragment); err != nil {
				first = err
				return
			}
			if err := p.rejectMergedTargetsInRelationItems(relations, from, oldFragment); err != nil {
				first = err
				return
			}
		}
		// First find semantic candidates.  An unsupported extension relation is
		// opaque unless it actually contains a target this operation owns.
		if !relationsContainTarget(rels, from, oldFragment) {
			return
		}
		if len(rels) != 1 {
			first = p.touchedDuplicateKeyError(n, "relations")
			return
		}
		relations := rels[0]
		if relations.Kind == yaml.AliasNode || relations.Alias != nil {
			first = p.yamlNodeError("alias_provenance", ErrAmbiguousPresentation, relations)
			return
		}
		if relations.Kind != yaml.MappingNode || relations.Style&yaml.FlowStyle != 0 || relations.Anchor != "" || relations.Alias != nil {
			first = p.yamlNodeError("relations_not_block_mapping", ErrUnsupportedPresentation, relations)
			return
		}
		if err := p.rejectMergedRelationsTarget(relations, from, oldFragment); err != nil {
			first = err
			return
		}
		typeCounts := make(map[string]int, len(relations.Content)/2)
		for i := 0; i+1 < len(relations.Content); i += 2 {
			typeCounts[relations.Content[i].Value]++
		}
		for i := 0; i+1 < len(relations.Content); i += 2 {
			key, items := relations.Content[i], relations.Content[i+1]
			if typeCounts[key.Value] > 1 && sequenceContainsTarget(items, from, oldFragment) {
				first = p.touchedDuplicateKeyError(relations, key.Value)
				return
			}
		}
		for i := 0; i+1 < len(relations.Content); i += 2 {
			key, items := relations.Content[i], relations.Content[i+1]
			if !sequenceContainsTarget(items, from, oldFragment) {
				continue
			}
			if err := p.requireKey(key); err != nil {
				first = err
				return
			}
			if items.Kind == yaml.AliasNode || items.Alias != nil {
				first = p.yamlNodeError("alias_provenance", ErrAmbiguousPresentation, items)
				return
			}
			if items.Kind != yaml.SequenceNode || items.Style&yaml.FlowStyle != 0 || items.Anchor != "" || items.Alias != nil {
				first = p.yamlNodeError("relation_type_not_block_sequence", ErrUnsupportedPresentation, items)
				return
			}
			duplicateTargets := duplicateRelationTargetsInSequence(items, from, oldFragment)
			if len(duplicateTargets) > 1 {
				first = p.yamlNodeError("duplicate_target", ErrAmbiguousPresentation, duplicateTargets...)
				return
			}
			for _, item := range items.Content {
				semanticItem := yamlAliasTarget(item)
				if err := p.rejectMergedItemTarget(semanticItem, from, oldFragment); err != nil {
					first = err
					return
				}
				vals := mappingValues(semanticItem, "target")
				if !targetsContain(vals, from, oldFragment) {
					continue
				}
				if len(vals) != 1 {
					first = p.touchedDuplicateKeyError(semanticItem, "target")
					return
				}
				if item.Kind == yaml.AliasNode || item.Alias != nil {
					first = p.yamlNodeError("alias_provenance", ErrAmbiguousPresentation, item)
					return
				}
				if item.Kind != yaml.MappingNode || item.Style&yaml.FlowStyle != 0 || item.Anchor != "" {
					first = p.yamlNodeError("relation_item_presentation", ErrUnsupportedPresentation, item)
					return
				}
				target := vals[0]
				r, _ := bundle.ParseRelationRef(target.Value)
				r.ID = to
				if oldFragment != "" {
					r.Fragment = newFragment
				}
				patch, e := p.resolvedScalarPatch(target, r.String())
				if e != nil {
					first = e
					return
				}
				patches = append(patches, patch)
			}
		}
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return patches, first
}

func (p *presentation) rejectMergedRootRelationTarget(mapping *yaml.Node, id bundle.ConceptID, fragment string) error {
	return p.rejectMatchingMerge(mapping, func(value *yaml.Node) bool {
		return mergedRootContainsRelationTarget(value, id, fragment, make(map[*yaml.Node]bool))
	})
}

func (p *presentation) rejectMergedRelationsTarget(mapping *yaml.Node, id bundle.ConceptID, fragment string) error {
	return p.rejectMatchingMerge(mapping, func(value *yaml.Node) bool {
		return mergedRelationsContainTarget(value, id, fragment, make(map[*yaml.Node]bool))
	})
}

func (p *presentation) rejectMergedItemTarget(mapping *yaml.Node, id bundle.ConceptID, fragment string) error {
	return p.rejectMatchingMerge(mapping, func(value *yaml.Node) bool {
		return mergedItemContainsTarget(value, id, fragment, make(map[*yaml.Node]bool))
	})
}

func (p *presentation) rejectMergedTargetsInRelationItems(relations *yaml.Node, id bundle.ConceptID, fragment string) error {
	if relations == nil || relations.Kind != yaml.MappingNode {
		return nil
	}
	for i := 1; i < len(relations.Content); i += 2 {
		sequence := yamlAliasTarget(relations.Content[i])
		if sequence == nil || sequence.Kind != yaml.SequenceNode {
			continue
		}
		for _, item := range sequence.Content {
			if err := p.rejectMergedItemTarget(yamlAliasTarget(item), id, fragment); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *presentation) rejectMergedCanonicalFragment(mapping *yaml.Node, fragment string) error {
	return p.rejectMatchingMerge(mapping, func(value *yaml.Node) bool {
		return mergedCanonicalContainsFragment(value, fragment, make(map[*yaml.Node]bool))
	})
}

func (p *presentation) rejectMatchingMerge(mapping *yaml.Node, matches func(*yaml.Node) bool) error {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		key, value := mapping.Content[i], mapping.Content[i+1]
		if key != nil && key.Tag == "!!merge" && matches(value) {
			return p.yamlNodeError("alias_provenance", ErrAmbiguousPresentation, key, value)
		}
	}
	return nil
}

func mergedRootContainsRelationTarget(value *yaml.Node, id bundle.ConceptID, fragment string, visited map[*yaml.Node]bool) bool {
	return anyMergedMapping(value, visited, func(mapping *yaml.Node) bool {
		if relationsContainTarget(mappingValues(mapping, "relations"), id, fragment) {
			return true
		}
		for i := 0; i+1 < len(mapping.Content); i += 2 {
			if mapping.Content[i].Tag == "!!merge" && mergedRootContainsRelationTarget(mapping.Content[i+1], id, fragment, visited) {
				return true
			}
		}
		return false
	})
}

func mergedRelationsContainTarget(value *yaml.Node, id bundle.ConceptID, fragment string, visited map[*yaml.Node]bool) bool {
	return anyMergedMapping(value, visited, func(mapping *yaml.Node) bool {
		for i := 0; i+1 < len(mapping.Content); i += 2 {
			if mapping.Content[i].Tag == "!!merge" {
				if mergedRelationsContainTarget(mapping.Content[i+1], id, fragment, visited) {
					return true
				}
				continue
			}
			if sequenceContainsTarget(mapping.Content[i+1], id, fragment) {
				return true
			}
		}
		return false
	})
}

func mergedItemContainsTarget(value *yaml.Node, id bundle.ConceptID, fragment string, visited map[*yaml.Node]bool) bool {
	return anyMergedMapping(value, visited, func(mapping *yaml.Node) bool {
		if targetsContain(mappingValues(mapping, "target"), id, fragment) {
			return true
		}
		for i := 0; i+1 < len(mapping.Content); i += 2 {
			if mapping.Content[i].Tag == "!!merge" && mergedItemContainsTarget(mapping.Content[i+1], id, fragment, visited) {
				return true
			}
		}
		return false
	})
}

func mergedCanonicalContainsFragment(value *yaml.Node, fragment string, visited map[*yaml.Node]bool) bool {
	return anyMergedMapping(value, visited, func(mapping *yaml.Node) bool {
		identity := bundle.ResolveMappingIdentity(mapping)
		if identity.State == bundle.MappingIdentityValid && identity.Fragment == fragment ||
			identity.State == bundle.MappingIdentityInvalid && identityMentionsFragment(mapping, fragment) {
			return true
		}
		for i := 0; i+1 < len(mapping.Content); i += 2 {
			if mapping.Content[i].Tag == "!!merge" && mergedCanonicalContainsFragment(mapping.Content[i+1], fragment, visited) {
				return true
			}
		}
		return false
	})
}

func anyMergedMapping(node *yaml.Node, visited map[*yaml.Node]bool, matches func(*yaml.Node) bool) bool {
	if node == nil || visited[node] {
		return false
	}
	visited[node] = true
	if node.Kind == yaml.AliasNode || node.Alias != nil {
		return anyMergedMapping(node.Alias, visited, matches)
	}
	if node.Kind == yaml.SequenceNode {
		for _, child := range node.Content {
			if anyMergedMapping(child, visited, matches) {
				return true
			}
		}
		return false
	}
	return node.Kind == yaml.MappingNode && matches(node)
}

func matchesRelationTarget(n *yaml.Node, id bundle.ConceptID, fragment string) bool {
	n = yamlAliasTarget(n)
	if n == nil || n.Kind != yaml.ScalarNode {
		return false
	}
	r, err := bundle.ParseRelationRef(n.Value)
	return err == nil && r.ID.String() == id.String() && (fragment == "" || r.Fragment == fragment)
}
func targetsContain(nodes []*yaml.Node, id bundle.ConceptID, fragment string) bool {
	for _, n := range nodes {
		if matchesRelationTarget(n, id, fragment) {
			return true
		}
	}
	return false
}
func sequenceContainsTarget(items *yaml.Node, id bundle.ConceptID, fragment string) bool {
	return len(relationTargetsInSequence(items, id, fragment)) != 0
}

func relationTargetsInSequence(items *yaml.Node, id bundle.ConceptID, fragment string) []*yaml.Node {
	if items == nil {
		return nil
	}
	semantic := yamlAliasTarget(items)
	if semantic == nil || semantic.Kind != yaml.SequenceNode {
		return nil
	}
	var matches []*yaml.Node
	for _, item := range semantic.Content {
		for _, target := range mappingValues(yamlAliasTarget(item), "target") {
			if matchesRelationTarget(target, id, fragment) {
				matches = append(matches, target)
			}
		}
	}
	return matches
}

func duplicateRelationTargetsInSequence(items *yaml.Node, id bundle.ConceptID, fragment string) []*yaml.Node {
	groups := make(map[string][]*yaml.Node)
	order := make([]string, 0)
	for _, target := range relationTargetsInSequence(items, id, fragment) {
		semantic := yamlAliasTarget(target)
		ref, err := bundle.ParseRelationRef(semantic.Value)
		if err != nil {
			continue
		}
		key := ref.String()
		if _, exists := groups[key]; !exists {
			order = append(order, key)
		}
		groups[key] = append(groups[key], target)
	}
	for _, key := range order {
		candidates := groups[key]
		if len(candidates) > 1 {
			return candidates
		}
	}
	return nil
}
func relationsContainTarget(rels []*yaml.Node, id bundle.ConceptID, fragment string) bool {
	for _, rel := range rels {
		semantic := yamlAliasTarget(rel)
		if semantic != nil && semantic.Kind == yaml.MappingNode {
			for i := 1; i < len(semantic.Content); i += 2 {
				if sequenceContainsTarget(semantic.Content[i], id, fragment) {
					return true
				}
			}
		}
	}
	return false
}

func yamlAliasTarget(n *yaml.Node) *yaml.Node {
	if n != nil && (n.Kind == yaml.AliasNode || n.Alias != nil) && n.Alias != nil {
		return n.Alias
	}
	return n
}
func (p *presentation) requireKey(n *yaml.Node) error {
	return p.requireScalarKey(n)
}

func fragmentPatch(p *presentation, from, to string) (bytePatch, bool, error) {
	return fragmentPatchContext(context.Background(), p, from, to)
}

func fragmentPatchContext(ctx context.Context, p *presentation, from, to string) (bytePatch, bool, error) {
	if err := ctx.Err(); err != nil {
		return bytePatch{}, false, err
	}
	alias, err := canonicalAliasProvenanceContext(ctx, p.root, from)
	if err != nil {
		return bytePatch{}, false, err
	}
	if len(alias) != 0 {
		return bytePatch{}, false, p.yamlNodeError("alias_provenance", ErrAmbiguousPresentation, alias...)
	}
	var matches []*yaml.Node
	var first error
	walkNestedMappings(p.root, func(n *yaml.Node) {
		if first == nil {
			first = ctx.Err()
		}
		if first != nil {
			return
		}
		if err := p.rejectMergedCanonicalFragment(n, from); err != nil {
			first = err
			return
		}
		canonical, node, ambiguous := canonicalFragmentNode(n)
		if err := p.unsupportedIdentityPresentationForFragment(n, from); err != nil {
			first = err
			return
		}
		if ambiguous {
			if !identityMentionsFragment(n, from) {
				return
			}
			first = p.yamlNodeError("duplicate_canonical_fragment", ErrAmbiguousPresentation, identityKeyNodes(n)...)
			return
		}
		if canonical != from {
			return
		}
		if n.Style&yaml.FlowStyle != 0 {
			first = p.yamlNodeError("canonical_fragment_flow_mapping", ErrUnsupportedPresentation, n)
			return
		}
		matches = append(matches, node)
	})
	if err := ctx.Err(); err != nil {
		return bytePatch{}, false, err
	}
	if first != nil {
		return bytePatch{}, false, first
	}
	if len(matches) > 1 {
		return bytePatch{}, false, p.yamlNodeError("duplicate_canonical_fragment", ErrAmbiguousPresentation, matches...)
	}
	if len(matches) == 0 {
		return bytePatch{}, false, nil
	}
	result, err := p.resolvedScalarPatch(matches[0], to)
	return result, err == nil, err
}

func canonicalFragmentCandidatesContext(ctx context.Context, root *yaml.Node, fragment string) ([]*yaml.Node, error) {
	var candidates []*yaml.Node
	var first error
	walkNestedMappings(root, func(mapping *yaml.Node) {
		if first == nil {
			first = ctx.Err()
		}
		if first != nil {
			return
		}
		identity := bundle.ResolveMappingIdentity(mapping)
		switch {
		case identity.State == bundle.MappingIdentityValid && identity.Fragment == fragment:
			candidates = append(candidates, identity.Node)
		case identity.State == bundle.MappingIdentityInvalid && identityMentionsFragment(mapping, fragment):
			candidates = append(candidates, identityKeyNodes(mapping)...)
		}
	})
	return candidates, first
}

func canonicalAliasProvenanceContext(ctx context.Context, n *yaml.Node, fragment string) ([]*yaml.Node, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if n == nil {
		return nil, nil
	}
	if n.Kind == yaml.MappingNode {
		for _, key := range []string{"id", "anchor"} {
			for _, value := range mappingValues(n, key) {
				if value != nil && (value.Kind == yaml.AliasNode || value.Alias != nil) && value.Alias != nil && value.Alias.Value == fragment {
					return []*yaml.Node{value, value.Alias}, nil
				}
			}
		}
	}
	if n.Kind == yaml.AliasNode && n.Alias != nil && (n.Alias.Kind == yaml.MappingNode || n.Alias.Kind == yaml.SequenceNode) {
		matched, err := canonicalIdentityInAliasTargetContext(ctx, n.Alias, fragment, make(map[*yaml.Node]bool))
		if err != nil {
			return nil, err
		}
		if matched != nil {
			return []*yaml.Node{n, matched}, nil
		}
	}
	for _, child := range n.Content {
		found, err := canonicalAliasProvenanceContext(ctx, child, fragment)
		if err != nil || len(found) != 0 {
			return found, err
		}
	}
	return nil, nil
}

func canonicalIdentityInAliasTargetContext(ctx context.Context, n *yaml.Node, fragment string, visited map[*yaml.Node]bool) (*yaml.Node, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if n == nil || visited[n] {
		return nil, nil
	}
	visited[n] = true
	if n.Kind == yaml.AliasNode || n.Alias != nil {
		return canonicalIdentityInAliasTargetContext(ctx, n.Alias, fragment, visited)
	}
	if n.Kind == yaml.MappingNode {
		identity := bundle.ResolveMappingIdentity(n)
		if identity.State == bundle.MappingIdentityValid && identity.Fragment == fragment {
			return identity.Node, nil
		}
		if identity.State == bundle.MappingIdentityInvalid && identityMentionsFragment(n, fragment) {
			nodes := identityKeyNodes(n)
			if len(nodes) != 0 {
				return nodes[0], nil
			}
			return n, nil
		}
	}
	for _, child := range n.Content {
		matched, err := canonicalIdentityInAliasTargetContext(ctx, child, fragment, visited)
		if err != nil || matched != nil {
			return matched, err
		}
	}
	return nil, nil
}

// identityMentionsFragment reports whether a mapping carries an id/anchor that
// names from, including block-scalar Values that only differ by a trailing
// newline keep-chomp that validRelationFragment rejects.
func identityMentionsFragment(n *yaml.Node, from string) bool {
	for _, key := range []string{"id", "anchor"} {
		for _, value := range mappingValues(n, key) {
			if value == nil || value.Kind != yaml.ScalarNode {
				continue
			}
			if value.Value == from || strings.TrimSuffix(value.Value, "\n") == from {
				return true
			}
		}
	}
	return false
}

func ensureRelationPresentation(data []byte, source bundle.RelationRef, typ, target string) ([]byte, error) {
	return ensureRelationPresentationContext(context.Background(), data, source, typ, target)
}

func ensureRelationPresentationContext(ctx context.Context, data []byte, source bundle.RelationRef, typ, target string) ([]byte, error) {
	p, err := parsePresentationContext(ctx, data)
	if err != nil {
		return nil, err
	}
	n, err := mappingForRef(ctx, p, p.root, source)
	if err != nil {
		return nil, err
	}
	if n == nil {
		return nil, fmt.Errorf("source fragment missing")
	}
	if err := p.rejectMergedKey(n, "relations"); err != nil {
		return nil, err
	}
	rels := mappingValues(n, "relations")
	if len(rels) > 1 {
		return nil, p.touchedDuplicateKeyError(n, "relations")
	}
	if len(rels) == 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := p.requireInsertionProvenanceContext(ctx, n); err != nil {
			return nil, err
		}
		expected, err := p.expectedEnsureRelationContext(ctx, n, typ, target)
		if err != nil {
			return nil, err
		}
		return p.insertAfterContext(ctx, n, strings.Repeat(" ", n.Column-1)+"relations:"+string(p.newline)+strings.Repeat(" ", n.Column+1)+yamlScalarToken(typ, 0)+":"+string(p.newline)+strings.Repeat(" ", n.Column+3)+"- target: "+yamlScalarToken(target, 0)+string(p.newline), expected)
	}
	relations := rels[0]
	if relations.Kind == yaml.AliasNode || relations.Alias != nil {
		return nil, p.yamlNodeError("alias_provenance", ErrAmbiguousPresentation, relations)
	}
	if relations.Kind != yaml.MappingNode || relations.Style&yaml.FlowStyle != 0 {
		return nil, p.yamlNodeError("relations_not_block_mapping", ErrUnsupportedPresentation, relations)
	}
	if err := p.rejectMergedKey(relations, typ); err != nil {
		return nil, err
	}
	seqs := mappingValues(relations, typ)
	if len(seqs) > 1 {
		return nil, p.touchedDuplicateKeyError(relations, typ)
	}
	if len(seqs) == 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := p.requireInsertionProvenanceContext(ctx, relations); err != nil {
			return nil, err
		}
		expected, err := p.expectedEnsureRelationContext(ctx, n, typ, target)
		if err != nil {
			return nil, err
		}
		return p.insertAfterContext(ctx, relations, strings.Repeat(" ", relations.Column-1)+yamlScalarToken(typ, 0)+":"+string(p.newline)+strings.Repeat(" ", relations.Column+1)+"- target: "+yamlScalarToken(target, 0)+string(p.newline), expected)
	}
	seq := seqs[0]
	if seq.Kind == yaml.AliasNode || seq.Alias != nil {
		return nil, p.yamlNodeError("alias_provenance", ErrAmbiguousPresentation, seq)
	}
	if seq.Kind != yaml.SequenceNode || seq.Style&yaml.FlowStyle != 0 {
		return nil, p.yamlNodeError("relation_type_not_block_sequence", ErrUnsupportedPresentation, seq)
	}
	var matches []*yaml.Node
	for _, item := range seq.Content {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		semanticItem := yamlAliasTarget(item)
		if err := p.rejectMergedKey(semanticItem, "target"); err != nil {
			return nil, err
		}
		vals := mappingValues(semanticItem, "target")
		if len(vals) > 1 {
			return nil, p.touchedDuplicateKeyError(semanticItem, "target")
		}
		for _, v := range vals {
			semantic := yamlAliasTarget(v)
			if semantic != nil && semantic.Value == target {
				matches = append(matches, item)
			}
		}
	}
	if len(matches) > 1 {
		return nil, p.yamlNodeError("duplicate_target", ErrAmbiguousPresentation, matches...)
	}
	if err := p.requireSupportedRelationSequence(seq); err != nil {
		return nil, err
	}
	if len(matches) == 1 {
		return appendBytesContext(ctx, nil, data)
	}
	if err := p.requireInsertionProvenanceContext(ctx, seq); err != nil {
		return nil, err
	}
	expected, err := p.expectedEnsureRelationContext(ctx, n, typ, target)
	if err != nil {
		return nil, err
	}
	return p.insertAfterContext(ctx, seq, strings.Repeat(" ", seq.Column-1)+"- target: "+yamlScalarToken(target, 0)+string(p.newline), expected)
}

func (p *presentation) expectedEnsureRelation(source *yaml.Node, typ, target string) (*yamlSemanticNode, error) {
	return p.expectedEnsureRelationContext(context.Background(), source, typ, target)
}

func (p *presentation) expectedEnsureRelationContext(ctx context.Context, source *yaml.Node, typ, target string) (*yamlSemanticNode, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, ok := p.paths[source]
	if !ok {
		return nil, p.yamlNodeError("semantic_edit_path", ErrUnsupportedPresentation, source)
	}
	expected, err := semanticYAMLContext(ctx, p.root)
	if err != nil {
		return nil, err
	}
	mapping := nodeAtPathNode(expected, path)
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil, p.yamlNodeError("semantic_edit_path", ErrUnsupportedPresentation, source)
	}
	relations := semanticMappingValue(mapping, "relations")
	if relations == nil {
		relations = &yamlSemanticNode{Kind: yaml.MappingNode, Tag: "!!map"}
		semanticMappingAppend(mapping, "relations", relations)
	}
	if relations.Kind != yaml.MappingNode {
		return nil, p.yamlNodeError("relations_not_block_mapping", ErrUnsupportedPresentation, source)
	}
	sequence := semanticMappingValue(relations, typ)
	if sequence == nil {
		sequence = &yamlSemanticNode{Kind: yaml.SequenceNode, Tag: "!!seq"}
		semanticMappingAppend(relations, typ, sequence)
	}
	if sequence.Kind != yaml.SequenceNode {
		return nil, p.yamlNodeError("relation_type_not_block_sequence", ErrUnsupportedPresentation, source)
	}
	item := &yamlSemanticNode{Kind: yaml.MappingNode, Tag: "!!map"}
	semanticMappingAppend(item, "target", &yamlSemanticNode{Kind: yaml.ScalarNode, Tag: "!!str", Value: target})
	sequence.Content = append(sequence.Content, item)
	return expected, nil
}

func semanticMappingValue(mapping *yamlSemanticNode, key string) *yamlSemanticNode {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Kind == yaml.ScalarNode && mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func semanticMappingAppend(mapping *yamlSemanticNode, key string, value *yamlSemanticNode) {
	mapping.Content = append(mapping.Content,
		&yamlSemanticNode{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		value,
	)
}

// requireSupportedRelationSequence proves only the semantic target route that
// decides idempotence and ambiguity. Unknown extension keys and values are not
// patch owners and remain opaque; exact outside-span equality protects them.
func (p *presentation) requireSupportedRelationSequence(seq *yaml.Node) error {
	if seq == nil || seq.Kind == yaml.AliasNode || seq.Alias != nil {
		return p.yamlNodeError("alias_provenance", ErrAmbiguousPresentation, seq)
	}
	if err := p.requireInsertionProvenance(seq); err != nil {
		return err
	}
	// Alias/merge ambiguity has priority over a separate unsupported item in
	// the same sequence because it proves multiple raw provenance candidates.
	for _, item := range seq.Content {
		if item != nil && (item.Kind == yaml.AliasNode || item.Alias != nil) {
			return p.yamlNodeError("alias_provenance", ErrAmbiguousPresentation, item)
		}
		if item != nil && item.Kind == yaml.MappingNode {
			if err := p.rejectMergedKey(item, "target"); err != nil {
				return err
			}
		}
	}
	for _, item := range seq.Content {
		if item == nil || item.Kind != yaml.MappingNode || item.Style&yaml.FlowStyle != 0 || len(item.Content)%2 != 0 {
			return p.yamlNodeError("relation_item_presentation", ErrUnsupportedPresentation, item)
		}
		targets := mappingValues(item, "target")
		if len(targets) > 1 {
			return p.touchedDuplicateKeyError(item, "target")
		}
		if len(targets) != 1 {
			return p.yamlNodeError("relation_item_presentation", ErrUnsupportedPresentation, item)
		}
		target := targets[0]
		if target == nil || target.Kind == yaml.AliasNode || target.Alias != nil {
			return p.yamlNodeError("alias_provenance", ErrAmbiguousPresentation, target)
		}
		if target.Kind != yaml.ScalarNode || target.Tag != "!!str" || target.Style&(yaml.LiteralStyle|yaml.FoldedStyle|yaml.FlowStyle) != 0 {
			return p.yamlNodeError("relation_item_presentation", ErrUnsupportedPresentation, target)
		}
		if err := p.requireTouchedProvenance(target); err != nil {
			return err
		}
		if _, err := p.resolver.scalar(target); err != nil {
			return err
		}
	}
	return nil
}
func (p *presentation) insertAfterContext(ctx context.Context, n *yaml.Node, text string, expected *yamlSemanticNode) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := p.requireInsertionProvenanceContext(ctx, n); err != nil {
		return nil, err
	}
	at, err := p.insertionPoint(n)
	if err != nil {
		return nil, err
	}
	patch := bytePatch{Start: p.yamlStart + at, End: p.yamlStart + at, Text: []byte(text), Edit: &SemanticEdit{Expected: expected}}
	return p.patchYAMLContext(ctx, []bytePatch{patch})
}

// insertionPoint uses parser ownership rather than scanning every descendant.
// The first following AST sibling starts the next owned block; if none exists,
// the boundary is inherited from the parent up to the YAML document end.
func (p *presentation) insertionPoint(n *yaml.Node) (int, error) {
	if n == nil || (n.Kind != yaml.MappingNode && n.Kind != yaml.SequenceNode) || n.Style&yaml.FlowStyle != 0 || n.Anchor != "" || n.Alias != nil {
		return 0, p.yamlNodeError("unsupported_block", ErrUnsupportedPresentation, n)
	}
	current := n
	for parent := p.parents[current]; parent != nil; parent = p.parents[current] {
		index := yamlChildIndex(parent, current)
		if index < 0 {
			return 0, p.yamlNodeError("unowned_insertion_block", ErrUnsupportedPresentation, current)
		}
		next := index + 1
		if parent.Kind == yaml.MappingNode {
			if index%2 == 0 {
				return 0, p.yamlNodeError("unowned_insertion_block", ErrUnsupportedPresentation, current)
			}
			next = index + 1 // next mapping key after the current value
		}
		if next < len(parent.Content) {
			line := parent.Content[next].Line
			if line < 1 || line > len(p.resolver.lines) {
				return 0, p.yamlNodeError("invalid_coordinate", ErrUnsupportedPresentation, parent.Content[next])
			}
			return p.proveInsertionTrivia(p.resolver.lines[line-1])
		}
		current = parent
	}
	return p.proveInsertionTrivia(len(p.yaml))
}

// proveInsertionTrivia rejects a boundary preceded by standalone comments or
// blank lines. yaml.v3 does not preserve whether that trivia belongs to the
// current collection or the following sibling, so moving it across a newly
// inserted node would not be a lossless mutation.
func (p *presentation) proveInsertionTrivia(at int) (int, error) {
	if at < 0 || at > len(p.yaml) {
		point := min(max(at, 0), len(p.yaml))
		return 0, yamlLocalPresentationError("invalid_coordinate", ErrUnsupportedPresentation, SourceSpan{Start: point, End: point})
	}
	lineEnd := at
	if lineEnd > 0 && p.yaml[lineEnd-1] == '\n' {
		lineEnd--
		if lineEnd > 0 && p.yaml[lineEnd-1] == '\r' {
			lineEnd--
		}
	}
	lineStart := bytes.LastIndexByte(p.yaml[:lineEnd], '\n') + 1
	line := bytes.TrimSpace(p.yaml[lineStart:lineEnd])
	if len(line) == 0 || line[0] == '#' {
		return 0, yamlLocalPresentationError("ambiguous_insertion_trivia", ErrUnsupportedPresentation, SourceSpan{Start: lineStart, End: at})
	}
	return at, nil
}
