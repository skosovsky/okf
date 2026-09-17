package mutation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/internal/documentlayout"
	"gopkg.in/yaml.v3"
)

type presentation struct {
	ctx                context.Context
	data, yaml         []byte
	yamlStart, yamlEnd int
	root               *yaml.Node
	newline            []byte
	resolver           *yamlResolver
	paths              map[*yaml.Node][]int
	parents            map[*yaml.Node]*yaml.Node
}

// parsePresentationContext only treats the bytes between the recognized
// opening and closing Markdown delimiters as YAML.  In particular, a body
// thematic break or setext underline is never a second YAML document.
func parsePresentationContext(ctx context.Context, data []byte) (*presentation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// The presentation and resolver retain byte slices for the lifetime of the
	// mutation. Snapshot caller-owned bytes before parsing so mutations after
	// this function returns cannot race with indexing or patch verification.
	data, err := appendBytesContext(ctx, nil, data)
	if err != nil {
		return nil, err
	}
	invalid, valid, err := validateUTF8Context(ctx, data)
	if err != nil {
		return nil, err
	}
	if !valid {
		format := "markdown"
		layout, ok, splitErr := documentlayout.SplitContext(ctx, data)
		if errors.Is(splitErr, context.Canceled) || errors.Is(splitErr, context.DeadlineExceeded) {
			return nil, splitErr
		}
		if splitErr == nil && ok && invalid.Start < layout.BodyStart {
			format = "yaml"
		}
		return nil, invalidUTF8PresentationErrorAt(format, invalid)
	}
	layout, ok, splitErr := documentlayout.SplitContext(ctx, data)
	if errors.Is(splitErr, context.Canceled) || errors.Is(splitErr, context.DeadlineExceeded) {
		return nil, splitErr
	}
	if splitErr != nil || !ok {
		location := SourceSpan{}
		if documentlayout.HasOpeningDelimiter(data) {
			location = SourceSpan{Start: layout.YAMLStart, End: len(data)}
		}
		return nil, yamlPresentationError("unterminated_frontmatter", fmt.Errorf("%w: %w", ErrUnsupportedPresentation, bundle.ErrUnterminatedFrontmatter), location)
	}
	first, end := layout.YAMLStart, layout.YAMLEnd
	if span, ok, err := leadingYAMLStreamTokenContext(ctx, data[first:end]); err != nil {
		return nil, err
	} else if ok {
		return nil, yamlDocumentPresentationError("yaml_stream_syntax", ErrUnsupportedPresentation, data, first, end, span)
	}
	decoder, err := newGuardedYAMLStreamDecoder(ctx, data[first:end])
	if err != nil {
		return nil, err
	}
	decoded, err := decoder.decode()
	if err != nil {
		if mapped := mutationYAMLDocumentGraphError(err, data, first, end); mapped != nil {
			return nil, mapped
		}
		return nil, yamlDocumentPresentationError("yaml_syntax", fmt.Errorf("%w: %w", ErrUnsupportedPresentation, err), data, first, end, yamlDocumentDiagnosticSpan(data[first:end]))
	}
	if len(decoded.Content) != 1 || decoded.Content[0].Kind != yaml.MappingNode {
		return nil, yamlDocumentPresentationError("invalid_frontmatter", fmt.Errorf("%w: %w", ErrUnsupportedPresentation, bundle.ErrInvalidFrontmatter), data, first, end, yamlDocumentDiagnosticSpan(data[first:end]))
	}
	r, err := newYAMLResolverContext(ctx, data[first:end])
	if err != nil {
		return nil, yamlDocumentPresentationError("yaml_resolver", fmt.Errorf("%w: %w", ErrUnsupportedPresentation, err), data, first, end, yamlDocumentDiagnosticSpan(data[first:end]))
	}
	// Once the closing delimiter is recognized, every following byte is
	// Markdown body. Stream syntax is rejected only when yaml.v3 has established
	// the first document and the raw token is proven outside quoted scalars.
	if err := supportedYAMLDocumentContext(ctx, data[first:end], decoded, r); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := decoder.decode(); err == nil {
		return nil, yamlDocumentPresentationError("yaml_stream_syntax", ErrUnsupportedPresentation, data, first, end, yamlDocumentDiagnosticSpan(data[first:end]))
	} else if err != io.EOF {
		if mapped := mutationYAMLDocumentGraphError(err, data, first, end); mapped != nil {
			return nil, mapped
		}
		return nil, yamlDocumentPresentationError("yaml_syntax", fmt.Errorf("%w: %w", ErrUnsupportedPresentation, err), data, first, end, yamlDocumentDiagnosticSpan(data[first:end]))
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
	p := &presentation{ctx: ctx, data: data, yaml: data[first:end], yamlStart: first, yamlEnd: end, root: decoded.Content[0], newline: nl, resolver: r, paths: map[*yaml.Node][]int{}, parents: map[*yaml.Node]*yaml.Node{}}
	if err := p.indexPathsContext(ctx, p.root, nil, nil); err != nil {
		return nil, err
	}
	return p, nil
}

func yamlDocumentDiagnosticSpan(source []byte) SourceSpan {
	if len(source) == 0 {
		// There is no byte to own in an empty local slice. The public planner
		// maps this absent local span to a concrete complete-source diagnostic.
		return SourceSpan{}
	}
	return SourceSpan{Start: 0, End: len(source)}
}

// yamlDocumentPresentationError keeps ordinary parser spans frontmatter-local,
// but an empty YAML slice has no local byte to own. In that one case the
// recognized closing delimiter is the exact complete-source provenance.
func yamlDocumentPresentationError(code string, cause error, data []byte, yamlStart, yamlEnd int, span SourceSpan) error {
	if yamlStart < yamlEnd {
		return yamlLocalPresentationErrorForSource(code, cause, data[yamlStart:yamlEnd], span)
	}
	return yamlPresentationError(code, cause, concreteYAMLDiagnosticSpan(data, SourceSpan{Start: yamlEnd, End: yamlEnd}))
}

func mutationYAMLDocumentGraphError(err error, data []byte, yamlStart, yamlEnd int) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if !errors.Is(err, bundle.ErrYAMLResourceLimit) && !errors.Is(err, bundle.ErrInvalidYAMLGraph) {
		return nil
	}
	code, cause, mapped := mutationYAMLGraphValidationCause(err)
	if !mapped {
		return cause
	}
	return yamlDocumentPresentationError(
		code,
		cause,
		data,
		yamlStart,
		yamlEnd,
		yamlDocumentDiagnosticSpan(data[yamlStart:yamlEnd]),
	)
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
			return yamlLocalPresentationErrorForSource("yaml_stream_syntax", ErrUnsupportedPresentation, source, SourceSpan{Start: at, End: end})
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

func quotedYAMLScalarSpansContext(ctx context.Context, doc *yaml.Node, resolver *yamlResolver) ([]SourceSpan, error) {
	if resolver == nil {
		return nil, yamlLocalPresentationError("yaml_resolver", fmt.Errorf("%w: nil YAML resolver", ErrUnsupportedPresentation), SourceSpan{})
	}
	if err := validateMutationYAMLGraphForSourceContext(
		ctx,
		doc,
		resolver.source,
		yamlDocumentDiagnosticSpan(resolver.source),
	); err != nil {
		return nil, err
	}
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
