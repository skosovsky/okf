package mutation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/skosovsky/okf/internal/documentlayout"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// MarkdownDestinationKind identifies the Goldmark semantic node owning a
// destination. Reference use sites intentionally have no span: their single
// LinkReferenceDefinition owns the destination.
type MarkdownDestinationKind uint8

const (
	MarkdownLinkDestination MarkdownDestinationKind = iota
	MarkdownImageDestination
	MarkdownReferenceDefinitionDestination
)

// MarkdownDestination is an exact source token plus its Goldmark semantic
// destination. Span includes angle brackets when they occurred in source.
type MarkdownDestination struct {
	Kind  MarkdownDestinationKind
	Span  SourceSpan
	Value string

	// angle records the source token wrapper so a replacement can preserve it
	// when possible. It is intentionally not part of the public contract.
	angle bool
}

type markdownInlineDestinationNode struct {
	node  ast.Node
	kind  MarkdownDestinationKind
	value []byte
}

// collectMarkdownDestinations maps every eligible Goldmark node directly to
// one source token. An inline node starts at its Pos() and examines only its
// bounded node-local window; a reference definition starts in its own
// LinkReferenceDefinition block segment. The small lexical reader proves one
// and only one raw token against the node's parsed Destination value.
func collectMarkdownDestinations(source []byte) ([]MarkdownDestination, error) {
	return collectMarkdownDestinationsContext(context.Background(), source)
}

func collectMarkdownDestinationsContext(ctx context.Context, source []byte) ([]MarkdownDestination, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	invalid, valid, err := validateUTF8Context(ctx, source)
	if err != nil {
		return nil, err
	}
	if !valid {
		return nil, invalidUTF8PresentationErrorAt("markdown", invalid)
	}
	bodyStart, err := markdownBodyStart(source)
	if err != nil {
		return nil, markdownPresentationErrorAt("frontmatter_boundary", fmt.Errorf("%w: %w", ErrUnsupportedPresentation, err), SourceSpan{Start: 0, End: len(source)})
	}
	body := source[bodyStart:]
	doc, parseContext, err := protectedGoldmarkParse(ctx, body)
	if err != nil {
		return nil, shiftMarkdownPresentationLocation(err, bodyStart)
	}

	inlineNodes := make([]markdownInlineDestinationNode, 0)
	err = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if err := ctx.Err(); err != nil {
			return ast.WalkStop, err
		}
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := node.(type) {
		case *ast.Link:
			if n.Reference != nil {
				return ast.WalkContinue, nil
			}
			inlineNodes = append(inlineNodes, markdownInlineDestinationNode{node: n, kind: MarkdownLinkDestination, value: n.Destination})
		case *ast.Image:
			if n.Reference != nil {
				return ast.WalkContinue, nil
			}
			inlineNodes = append(inlineNodes, markdownInlineDestinationNode{node: n, kind: MarkdownImageDestination, value: n.Destination})
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]MarkdownDestination, 0, len(inlineNodes))
	resolvedInline := make(map[ast.Node]SourceSpan, len(inlineNodes))
	sort.SliceStable(inlineNodes, func(i, j int) bool {
		return markdownNodeDepth(inlineNodes[i].node) > markdownNodeDepth(inlineNodes[j].node)
	})
	for _, item := range inlineNodes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		excluded := make([]SourceSpan, 0)
		for node, span := range resolvedInline {
			if markdownNodeDescendsFrom(node, item.node) {
				excluded = append(excluded, span)
			}
		}
		opaque, err := markdownOpaqueDescendantSpansContext(ctx, item.node)
		if err != nil {
			return nil, err
		}
		excluded = append(excluded, opaque...)
		followingStart, err := markdownFollowingInlineStartContext(ctx, item.node)
		if err != nil {
			return nil, err
		}
		span, raw, ok, err := markdownInlineDestinationContext(ctx, body, item.node, item.value, followingStart, excluded)
		if err != nil {
			// Inline readers operate on the Markdown body slice; Location must be
			// remapped to full-file offsets before leaving the collector.
			return nil, shiftMarkdownPresentationLocation(err, bodyStart)
		}
		if !ok {
			window, exists, windowErr := markdownNodeWindowContext(ctx, body, item.node)
			if windowErr != nil {
				return nil, windowErr
			}
			if !exists {
				window = SourceSpan{Start: 0, End: len(body)}
			}
			window.Start += bodyStart
			window.End += bodyStart
			return nil, markdownPresentationErrorAt("node_source_anchor", ErrUnsupportedPresentation, window)
		}
		span.Start += bodyStart
		span.End += bodyStart
		semantic, err := markdownSemanticDestinationContext(ctx, raw)
		if err != nil {
			return nil, err
		}
		out = append(out, MarkdownDestination{Kind: item.kind, Span: span, Value: semantic, angle: len(raw) > 0 && raw[0] == '<'})
		span.Start -= bodyStart
		span.End -= bodyStart
		resolvedInline[item.node] = span
	}
	definitions, err := markdownReferenceDestinationsContext(ctx, body, doc, parseContext)
	if err != nil {
		return nil, shiftMarkdownPresentationLocation(err, bodyStart)
	}
	for _, definition := range definitions {
		definition.Span.Start += bodyStart
		definition.Span.End += bodyStart
		out = append(out, definition)
	}
	if err := validateMarkdownDestinationSpansContext(ctx, source, out); err != nil {
		return nil, err
	}
	return out, nil
}

// protectedGoldmarkParse contains the only recovery boundary around Goldmark.
// Goldmark does not accept a context, so cancellation is checked immediately
// before and after its non-interruptible parser/transformer invocation.
func protectedGoldmarkParse(ctx context.Context, source []byte) (doc ast.Node, parseContext parser.Context, err error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				doc, parseContext, err = nil, nil, contextErr
				return
			}
			doc, parseContext, err = nil, nil, markdownPresentationErrorAt(
				"markdown_parser_failure",
				fmt.Errorf("%w: Goldmark parser panic: %v", ErrUnsupportedPresentation, recovered),
				SourceSpan{Start: 0, End: len(source)},
			)
		}
	}()
	parseContext = parser.NewContext()
	markdown := parser.NewParser(
		parser.WithBlockParsers(parser.DefaultBlockParsers()...),
		parser.WithInlineParsers(parser.DefaultInlineParsers()...),
		parser.WithParagraphTransformers(parser.DefaultParagraphTransformers()...),
	)
	doc = markdown.Parse(text.NewReader(source), parser.WithContext(parseContext))
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	return doc, parseContext, nil
}

// markdownInlineDestination reads precisely the construct beginning at node's
// Goldmark Pos(). The owning block's real source segments bound the reader, so
// a decoy in code, raw HTML, or another node cannot satisfy this node.
func markdownInlineDestination(source []byte, node ast.Node, want []byte, followingStart int, excluded []SourceSpan) (SourceSpan, []byte, bool, error) {
	return markdownInlineDestinationContext(context.Background(), source, node, want, followingStart, excluded)
}

func markdownInlineDestinationContext(ctx context.Context, source []byte, node ast.Node, want []byte, followingStart int, excluded []SourceSpan) (SourceSpan, []byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return SourceSpan{}, nil, false, err
	}
	start := node.Pos()
	window, ok, err := markdownNodeWindowContext(ctx, source, node)
	if err != nil {
		return SourceSpan{}, nil, false, err
	}
	if !ok || start < window.Start || start >= window.End {
		return SourceSpan{}, nil, false, nil
	}
	if followingStart > start && followingStart < window.End {
		window.End = followingStart
	}
	return markdownInlineTokenContext(ctx, source, start, window.End, want, excluded)
}

func markdownNodeDescendsFrom(node, ancestor ast.Node) bool {
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		if parent == ancestor {
			return true
		}
	}
	return false
}

func markdownNodeDepth(node ast.Node) int {
	depth := 0
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		depth++
	}
	return depth
}

func markdownOpaqueDescendantSpans(owner ast.Node) []SourceSpan {
	spans, _ := markdownOpaqueDescendantSpansContext(context.Background(), owner)
	return spans
}

func markdownOpaqueDescendantSpansContext(ctx context.Context, owner ast.Node) ([]SourceSpan, error) {
	spans := make([]SourceSpan, 0)
	err := ast.Walk(owner, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if err := ctx.Err(); err != nil {
			return ast.WalkStop, err
		}
		if !entering || node == owner {
			return ast.WalkContinue, nil
		}
		switch n := node.(type) {
		case *ast.RawHTML:
			for i := 0; i < n.Segments.Len(); i++ {
				segment := n.Segments.At(i)
				spans = append(spans, SourceSpan{Start: segment.Start, End: segment.Stop})
			}
		case *ast.Text:
			for parent := n.Parent(); parent != nil && parent != owner; parent = parent.Parent() {
				switch parent.(type) {
				case *ast.CodeSpan, *ast.AutoLink:
					spans = append(spans, SourceSpan{Start: n.Segment.Start, End: n.Segment.Stop})
					return ast.WalkContinue, nil
				}
			}
		}
		return ast.WalkContinue, nil
	})
	return spans, err
}

// markdownFollowingInlineStart isolates equal destination values owned by
// separate AST nodes in one block. Node.Pos is only a start marker, so the
// final node in a block still has to prove its destination from every viable
// source candidate in the remaining node-local window.
func markdownFollowingInlineStart(node ast.Node) int {
	start, _ := markdownFollowingInlineStartContext(context.Background(), node)
	return start
}

func markdownFollowingInlineStartContext(ctx context.Context, node ast.Node) (int, error) {
	for sibling := node.NextSibling(); sibling != nil; sibling = sibling.NextSibling() {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if sibling.Pos() > node.Pos() {
			return sibling.Pos(), nil
		}
	}
	return 0, ctx.Err()
}

func markdownReferenceDestinationsContext(ctx context.Context, source []byte, doc ast.Node, parserContext parser.Context) ([]MarkdownDestination, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	refs := make(map[string]parser.Reference, len(parserContext.References()))
	for _, reference := range parserContext.References() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		refs[util.ToLinkReference(reference.Label())] = reference
	}
	type definitionCandidate struct {
		definition *ast.LinkReferenceDefinition
		span       SourceSpan
		raw        []byte
	}
	groups := make(map[string][]definitionCandidate, len(refs))
	groupOrder := make([]string, 0, len(refs))
	err := ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if err := ctx.Err(); err != nil {
			return ast.WalkStop, err
		}
		if !entering {
			return ast.WalkContinue, nil
		}
		definition, ok := node.(*ast.LinkReferenceDefinition)
		if !ok {
			return ast.WalkContinue, nil
		}
		span, label, value, ok, tokenErr := markdownDefinitionNodeTokenContext(ctx, source, definition)
		if tokenErr != nil {
			return ast.WalkStop, tokenErr
		}
		labelEqual, err := bytesEqualContext(ctx, label, definition.Label)
		if err != nil {
			return ast.WalkStop, err
		}
		valueEqual, err := bytesEqualContext(ctx, value, definition.Destination)
		if err != nil {
			return ast.WalkStop, err
		}
		if !ok || !labelEqual || !valueEqual {
			window, exists, windowErr := markdownNodeWindowContext(ctx, source, definition)
			if windowErr != nil {
				return ast.WalkStop, windowErr
			}
			if !exists {
				window = SourceSpan{Start: 0, End: len(source)}
			}
			return ast.WalkStop, markdownPresentationErrorAt("reference_source_anchor", ErrUnsupportedPresentation, window)
		}
		key := util.ToLinkReference(definition.Label)
		if _, exists := groups[key]; !exists {
			groupOrder = append(groupOrder, key)
		}
		groups[key] = append(groups[key], definitionCandidate{definition: definition, span: span, raw: source[span.Start:span.End]})
		return ast.WalkContinue, nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]MarkdownDestination, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for _, key := range groupOrder {
		candidates := groups[key]
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(candidates) != 1 {
			span := candidates[0].span
			for _, candidate := range candidates[1:] {
				if candidate.span.Start < span.Start {
					span.Start = candidate.span.Start
				}
				if candidate.span.End > span.End {
					span.End = candidate.span.End
				}
			}
			return nil, markdownPresentationErrorAt("duplicate_reference_definition", ErrAmbiguousPresentation, span)
		}
		candidate := candidates[0]
		reference, active := refs[key]
		labelEqual, err := bytesEqualContext(ctx, candidate.definition.Label, reference.Label())
		if err != nil {
			return nil, err
		}
		valueEqual, err := bytesEqualContext(ctx, candidate.definition.Destination, reference.Destination())
		if err != nil {
			return nil, err
		}
		if !active || !labelEqual || !valueEqual {
			return nil, markdownPresentationErrorAt("reference_source_anchor", ErrUnsupportedPresentation, candidate.span)
		}
		seen[key] = struct{}{}
		semantic, err := markdownSemanticDestinationContext(ctx, candidate.raw)
		if err != nil {
			return nil, err
		}
		out = append(out, MarkdownDestination{
			Kind:  MarkdownReferenceDefinitionDestination,
			Span:  candidate.span,
			Value: semantic,
			angle: len(candidate.raw) > 0 && candidate.raw[0] == '<',
		})
	}
	if len(seen) != len(refs) {
		return nil, markdownPresentationErrorAt("reference_source_anchor", ErrUnsupportedPresentation, SourceSpan{Start: 0, End: len(source)})
	}
	return out, nil
}

// markdownDefinitionNodeToken proves the raw label and destination against a
// single real Goldmark definition node. Its Lines segments are the complete
// source boundary, including destination-on-continuation forms; paragraph text
// which merely resembles a definition never reaches this function.
func markdownDefinitionNodeToken(source []byte, definition *ast.LinkReferenceDefinition) (SourceSpan, []byte, []byte, bool) {
	span, label, value, ok, _ := markdownDefinitionNodeTokenContext(context.Background(), source, definition)
	return span, label, value, ok
}

func markdownDefinitionNodeTokenContext(ctx context.Context, source []byte, definition *ast.LinkReferenceDefinition) (SourceSpan, []byte, []byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return SourceSpan{}, nil, nil, false, err
	}
	lines := definition.Lines()
	if lines.Len() == 0 {
		return SourceSpan{}, nil, nil, false, nil
	}
	first := lines.At(0)
	last := lines.At(lines.Len() - 1)
	if first.Start < 0 || last.Stop < first.Start || last.Stop > len(source) {
		return SourceSpan{}, nil, nil, false, nil
	}
	windowStart, windowEnd := first.Start, last.Stop
	span, label, value, ok, err := markdownDefinitionTokenContext(ctx, source, windowStart, windowEnd)
	if err != nil {
		return SourceSpan{}, nil, nil, false, err
	}
	if !ok || span.Start < windowStart || span.End > windowEnd {
		return SourceSpan{}, nil, nil, false, nil
	}
	return span, label, value, true, ctx.Err()
}

func markdownNodeWindow(source []byte, node ast.Node) (SourceSpan, bool) {
	span, ok, _ := markdownNodeWindowContext(context.Background(), source, node)
	return span, ok
}

func markdownNodeWindowContext(ctx context.Context, source []byte, node ast.Node) (SourceSpan, bool, error) {
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		if err := ctx.Err(); err != nil {
			return SourceSpan{}, false, err
		}
		// Goldmark's BaseInline.Lines deliberately panics. Inline nodes inherit
		// their source window from the nearest block ancestor.
		if parent.Type() != ast.TypeBlock {
			continue
		}
		lines := parent.Lines()
		if lines.Len() == 0 {
			continue
		}
		start, end := len(source), 0
		for i := 0; i < lines.Len(); i++ {
			if err := ctx.Err(); err != nil {
				return SourceSpan{}, false, err
			}
			segment := lines.At(i)
			if segment.Start < 0 || segment.Stop < segment.Start || segment.Stop > len(source) {
				return SourceSpan{}, false, nil
			}
			if segment.Start < start {
				start = segment.Start
			}
			if segment.Stop > end {
				end = segment.Stop
			}
		}
		return SourceSpan{Start: start, End: end}, start < end, nil
	}
	return SourceSpan{}, false, ctx.Err()
}

func markdownInlineToken(source []byte, start, limit int, want []byte, excluded []SourceSpan) (SourceSpan, []byte, bool, error) {
	return markdownInlineTokenContext(context.Background(), source, start, limit, want, excluded)
}

func markdownInlineTokenContext(ctx context.Context, source []byte, start, limit int, want []byte, excluded []SourceSpan) (SourceSpan, []byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return SourceSpan{}, nil, false, err
	}
	if start < 0 || start >= limit || (source[start] != '[' && !(source[start] == '!' && start+1 < limit && source[start+1] == '[')) {
		return SourceSpan{}, nil, false, nil
	}
	var matches []SourceSpan
	wantSemantic, err := markdownSemanticDestinationContext(ctx, want)
	if err != nil {
		return SourceSpan{}, nil, false, err
	}
	for close := start + 1; close+1 < limit; close++ {
		if close&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return SourceSpan{}, nil, false, err
			}
		}
		escaped, err := markdownEscapedContext(ctx, source, close)
		if err != nil {
			return SourceSpan{}, nil, false, err
		}
		if source[close] != ']' || source[close+1] != '(' || escaped {
			continue
		}
		destinationStart, err := markdownSkipSpaceContext(ctx, source, close+2, limit)
		if err != nil {
			return SourceSpan{}, nil, false, err
		}
		if destinationStart < limit && source[destinationStart] == ')' && wantSemantic == "" {
			span := SourceSpan{Start: destinationStart, End: destinationStart}
			if !markdownSpanExcluded(span, excluded) {
				matches = append(matches, span)
			}
			continue
		}
		span, value, ok, err := markdownDestinationTokenContext(ctx, source, destinationStart, limit)
		if err != nil {
			return SourceSpan{}, nil, false, err
		}
		semantic, err := markdownSemanticDestinationContext(ctx, value)
		if err != nil {
			return SourceSpan{}, nil, false, err
		}
		if !ok || semantic != wantSemantic {
			continue
		}
		if markdownSpanExcluded(span, excluded) {
			continue
		}
		matches = append(matches, span)
	}
	switch len(matches) {
	case 0:
		return SourceSpan{}, nil, false, ctx.Err()
	case 1:
		span := matches[0]
		return span, source[span.Start:span.End], true, ctx.Err()
	default:
		// Multiple equal semantic values in one block window are safe only when
		// the delimiter structure rooted at the Goldmark node's Pos proves one
		// concrete owner. Descendant destination spans are skipped while matching
		// brackets so their payload cannot impersonate label structure.
		owned, raw, ok, err := markdownStructurallyOwnedInlineTokenContext(ctx, source, start, limit, want, excluded)
		if err != nil {
			return SourceSpan{}, nil, false, err
		}
		if ok {
			owners := 0
			for _, match := range matches {
				if match == owned {
					owners++
				}
			}
			if owners == 1 {
				return owned, raw, true, nil
			}
		}
		// Goldmark does not expose the closing delimiter span for Link/Image.
		// Equal semantic values therefore cannot safely distinguish multiple raw
		// candidates in this AST node's bounded source window.
		union := matches[0]
		for _, match := range matches[1:] {
			if match.Start < union.Start {
				union.Start = match.Start
			}
			if match.End > union.End {
				union.End = match.End
			}
		}
		return SourceSpan{}, nil, false, markdownPresentationErrorAt("node_source_anchor_ambiguous", ErrAmbiguousPresentation, union)
	}
}

func markdownStructurallyOwnedInlineTokenContext(ctx context.Context, source []byte, start, limit int, want []byte, excluded []SourceSpan) (SourceSpan, []byte, bool, error) {
	open := start
	if source[start] == '!' {
		open++
	}
	close, ok, err := markdownBracketClosureExcludingContext(ctx, source, open, limit, excluded)
	if err != nil || !ok || close+1 >= limit || source[close+1] != '(' {
		return SourceSpan{}, nil, false, err
	}
	destinationStart, err := markdownSkipSpaceContext(ctx, source, close+2, limit)
	if err != nil {
		return SourceSpan{}, nil, false, err
	}
	wantSemantic, err := markdownSemanticDestinationContext(ctx, want)
	if err != nil {
		return SourceSpan{}, nil, false, err
	}
	if destinationStart < limit && source[destinationStart] == ')' && wantSemantic == "" {
		return SourceSpan{Start: destinationStart, End: destinationStart}, source[destinationStart:destinationStart], true, nil
	}
	span, raw, ok, err := markdownDestinationTokenContext(ctx, source, destinationStart, limit)
	if err != nil || !ok || markdownSpanExcluded(span, excluded) {
		return SourceSpan{}, nil, false, err
	}
	semantic, err := markdownSemanticDestinationContext(ctx, raw)
	if err != nil || semantic != wantSemantic {
		return SourceSpan{}, nil, false, err
	}
	return span, source[span.Start:span.End], true, nil
}

func markdownBracketClosureExcludingContext(ctx context.Context, source []byte, open, limit int, excluded []SourceSpan) (int, bool, error) {
	depth := 0
	for at := open; at < limit; at++ {
		if at&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, false, err
			}
		}
		skipped := false
		for _, span := range excluded {
			if span.Start <= at && at < span.End {
				at = span.End - 1
				skipped = true
				break
			}
		}
		if skipped {
			continue
		}
		if source[at] == '\\' && at+1 < limit {
			at++
			continue
		}
		switch source[at] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return at, true, nil
			}
		}
	}
	return 0, false, ctx.Err()
}

func markdownSpanExcluded(span SourceSpan, excluded []SourceSpan) bool {
	for _, candidate := range excluded {
		if candidate == span || span.Start >= candidate.Start && span.Start < candidate.End {
			return true
		}
	}
	return false
}

func markdownSemanticDestination(raw []byte) string {
	value, _ := markdownSemanticDestinationContext(context.Background(), raw)
	return value
}

func markdownSemanticDestinationContext(ctx context.Context, raw []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(raw) >= 2 && raw[0] == '<' && raw[len(raw)-1] == '>' {
		raw = raw[1 : len(raw)-1]
	}
	value := util.UnescapePunctuations(raw)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	value = util.ResolveNumericReferences(value)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	value = util.ResolveEntityNames(value)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return string(value), ctx.Err()
}

func encodeMarkdownDestination(value string, preferAngle bool) ([]byte, error) {
	return encodeMarkdownDestinationContext(context.Background(), value, preferAngle)
}

func encodeMarkdownDestinationContext(ctx context.Context, value string, preferAngle bool) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !utf8.ValidString(value) || strings.ContainsAny(value, "\r\n\x00") {
		return nil, ErrUnsupportedPresentation
	}
	angle := preferAngle || strings.ContainsAny(value, " \t")
	encoded := make([]byte, 0, len(value)+8)
	if angle {
		encoded = append(encoded, '<')
	}
	for i := 0; i < len(value); i++ {
		if i&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		switch value[i] {
		case '&':
			encoded = append(encoded, "&amp;"...)
		case '\\', '<', '>':
			encoded = append(encoded, '\\', value[i])
		case '(', ')':
			if !angle {
				encoded = append(encoded, '\\')
			}
			encoded = append(encoded, value[i])
		default:
			encoded = append(encoded, value[i])
		}
	}
	if angle {
		encoded = append(encoded, '>')
	}
	return encoded, ctx.Err()
}

func markdownEscaped(source []byte, at int) bool {
	escaped, _ := markdownEscapedContext(context.Background(), source, at)
	return escaped
}

func markdownEscapedContext(ctx context.Context, source []byte, at int) (bool, error) {
	backslashes := 0
	for at > 0 && source[at-1] == '\\' {
		if at&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		backslashes++
		at--
	}
	return backslashes%2 == 1, ctx.Err()
}

func markdownDefinitionToken(source []byte, start, limit int) (SourceSpan, []byte, []byte, bool) {
	span, label, value, ok, _ := markdownDefinitionTokenContext(context.Background(), source, start, limit)
	return span, label, value, ok
}

func markdownDefinitionTokenContext(ctx context.Context, source []byte, start, limit int) (SourceSpan, []byte, []byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return SourceSpan{}, nil, nil, false, err
	}
	at := start
	for at < limit && at-start < 3 && source[at] == ' ' {
		at++
	}
	if at >= limit || source[at] != '[' {
		return SourceSpan{}, nil, nil, false, nil
	}
	labelEnd, ok, err := markdownBracketClosureContext(ctx, source, at, limit)
	if err != nil {
		return SourceSpan{}, nil, nil, false, err
	}
	if !ok || labelEnd+1 >= limit || source[labelEnd+1] != ':' {
		return SourceSpan{}, nil, nil, false, nil
	}
	destinationStart, err := markdownSkipSpaceContext(ctx, source, labelEnd+2, limit)
	if err != nil {
		return SourceSpan{}, nil, nil, false, err
	}
	span, value, ok, err := markdownDestinationTokenContext(ctx, source, destinationStart, limit)
	if err != nil {
		return SourceSpan{}, nil, nil, false, err
	}
	if !ok {
		return SourceSpan{}, nil, nil, false, nil
	}
	return span, source[at+1 : labelEnd], value, true, ctx.Err()
}

func markdownBracketClosure(source []byte, open, limit int) (int, bool) {
	at, ok, _ := markdownBracketClosureContext(context.Background(), source, open, limit)
	return at, ok
}

func markdownBracketClosureContext(ctx context.Context, source []byte, open, limit int) (int, bool, error) {
	depth := 0
	for at := open; at < limit; at++ {
		if at&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, false, err
			}
		}
		if source[at] == '\\' && at+1 < limit {
			at++
			continue
		}
		switch source[at] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return at, true, nil
			}
		}
	}
	return 0, false, ctx.Err()
}

func markdownSkipSpace(source []byte, at, limit int) int {
	at, _ = markdownSkipSpaceContext(context.Background(), source, at, limit)
	return at
}

func markdownSkipSpaceContext(ctx context.Context, source []byte, at, limit int) (int, error) {
	for at < limit {
		if at&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return at, err
			}
		}
		switch source[at] {
		case ' ', '\t', '\r', '\n':
			at++
		default:
			return at, nil
		}
	}
	return at, ctx.Err()
}

// markdownDestinationToken is intentionally node-local. Its grammar mirrors
// Goldmark's parseLinkDestination enough to locate the source token; equality
// with the node's Destination is the proof that it is that token.
func markdownDestinationToken(source []byte, at, limit int) (SourceSpan, []byte, bool) {
	span, value, ok, _ := markdownDestinationTokenContext(context.Background(), source, at, limit)
	return span, value, ok
}

func markdownDestinationTokenContext(ctx context.Context, source []byte, at, limit int) (SourceSpan, []byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return SourceSpan{}, nil, false, err
	}
	if at >= limit {
		return SourceSpan{}, nil, false, nil
	}
	if source[at] == '<' {
		for i := at + 1; i < limit; i++ {
			if i&4095 == 0 {
				if err := ctx.Err(); err != nil {
					return SourceSpan{}, nil, false, err
				}
			}
			if source[i] == '\\' && i+1 < limit {
				i++
				continue
			}
			if source[i] == '>' {
				return SourceSpan{Start: at, End: i + 1}, source[at+1 : i], true, nil
			}
			if source[i] == '\n' || source[i] == '\r' {
				return SourceSpan{}, nil, false, nil
			}
		}
		return SourceSpan{}, nil, false, ctx.Err()
	}
	depth := 0
	for i := at; i < limit; i++ {
		if i&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return SourceSpan{}, nil, false, err
			}
		}
		c := source[i]
		if c == '\\' && i+1 < limit {
			i++
			continue
		}
		switch c {
		case '(':
			depth++
		case ')':
			if depth == 0 {
				if i == at {
					return SourceSpan{}, nil, false, nil
				}
				return SourceSpan{Start: at, End: i}, source[at:i], true, nil
			}
			depth--
		case ' ', '\t', '\r', '\n':
			if i == at {
				return SourceSpan{}, nil, false, nil
			}
			return SourceSpan{Start: at, End: i}, source[at:i], true, nil
		}
	}
	if at < limit && depth == 0 {
		return SourceSpan{Start: at, End: limit}, source[at:limit], true, ctx.Err()
	}
	return SourceSpan{}, nil, false, ctx.Err()
}

func validateMarkdownDestinationSpansContext(ctx context.Context, source []byte, destinations []MarkdownDestination) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	sort.Slice(destinations, func(i, j int) bool {
		if destinations[i].Span.Start != destinations[j].Span.Start {
			return destinations[i].Span.Start < destinations[j].Span.Start
		}
		if destinations[i].Span.End != destinations[j].Span.End {
			return destinations[i].Span.End < destinations[j].Span.End
		}
		return destinations[i].Kind < destinations[j].Kind
	})
	if err := ctx.Err(); err != nil {
		return err
	}
	end := 0
	previous := SourceSpan{}
	for _, destination := range destinations {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !destination.Span.valid(len(source)) || destination.Span.Start < end {
			span := diagnosticMarkdownSpan(source, destination.Span)
			if destination.Span.Start < end && previous != (SourceSpan{}) {
				span = unionSourceSpans(previous, span)
			}
			return markdownPresentationErrorAt("invalid_source_span", ErrUnsupportedPresentation, span)
		}
		end = destination.Span.End
		previous = destination.Span
	}
	return nil
}

// rewriteMarkdownDestinations performs all validation before allocating the
// patched result. Reparse verification is order-sensitive: it compares each
// AST-owned destination, not a multiset of (kind, value) pairs.
func rewriteMarkdownDestinations(source []byte, rewrite func(string) (string, bool)) ([]byte, error) {
	return rewriteMarkdownDestinationsContext(context.Background(), source, rewrite)
}

func rewriteMarkdownDestinationsContext(ctx context.Context, source []byte, rewrite func(string) (string, bool)) ([]byte, error) {
	before, err := collectMarkdownDestinationsContext(ctx, source)
	if err != nil {
		return nil, err
	}
	expected := append([]MarkdownDestination(nil), before...)
	patches := make([]bytePatch, 0, len(before))
	for i, destination := range before {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		value, ok := rewrite(destination.Value)
		if !ok {
			continue
		}
		expected[i].Value = value
		replacement, err := encodeMarkdownDestinationContext(ctx, value, destination.angle)
		if err != nil {
			return nil, markdownPresentationErrorAt("unsupported_destination_value", fmt.Errorf("%w: %v", ErrUnsupportedPresentation, err), destination.Span)
		}
		patches = append(patches, bytePatch{Start: destination.Span.Start, End: destination.Span.End, Text: replacement})
	}
	if len(patches) == 0 {
		return appendBytesContext(ctx, nil, source)
	}
	out, err := applyBytePatchesContext(ctx, source, patches)
	if err != nil {
		var presentation *PresentationError
		if errors.As(err, &presentation) && presentation.Location != (SourceSpan{}) {
			return nil, markdownPresentationErrorAt("invalid_patch", ErrUnsupportedPresentation, presentation.Location)
		}
		return nil, markdownPresentationErrorAt("invalid_patch", ErrUnsupportedPresentation, bytePatchesSpan(patches))
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	after, err := collectMarkdownDestinationsContext(ctx, out)
	if err != nil {
		return nil, err
	}
	if err := equalMarkdownDestinationSemantics(expected, after, bytePatchesSpan(patches)); err != nil {
		return nil, err
	}
	equal, err := bytesOutsidePatchesEqualContext(ctx, source, out, patches)
	if err != nil {
		return nil, err
	}
	if !equal {
		return nil, markdownPresentationErrorAt("outside_span_changed", ErrUnsupportedPresentation, bytePatchesSpan(patches))
	}
	return out, nil
}

func equalMarkdownDestinationSemantics(want, got []MarkdownDestination, patchSpan SourceSpan) error {
	if len(want) != len(got) {
		return markdownPresentationErrorAt("semantic_span_mismatch", fmt.Errorf("%w: semantic count %d, source count %d", ErrUnsupportedPresentation, len(want), len(got)), patchSpan)
	}
	for i := range want {
		if want[i].Kind != got[i].Kind || want[i].Value != got[i].Value {
			return markdownPresentationErrorAt("semantic_span_mismatch", ErrUnsupportedPresentation, patchSpan)
		}
	}
	return nil
}

func diagnosticMarkdownSpan(source []byte, span SourceSpan) SourceSpan {
	if len(source) == 0 {
		return SourceSpan{}
	}
	start := min(max(span.Start, 0), len(source)-1)
	end := min(max(span.End, start+1), len(source))
	return SourceSpan{Start: start, End: end}
}

func unionSourceSpans(left, right SourceSpan) SourceSpan {
	if left == (SourceSpan{}) {
		return right
	}
	if right == (SourceSpan{}) {
		return left
	}
	return SourceSpan{Start: min(left.Start, right.Start), End: max(left.End, right.End)}
}

// bytesOutsidePatchesEqual compares the unchanged source segments while
// allowing replacements to have different lengths.
func bytesOutsidePatchesEqual(before, after []byte, patches []bytePatch) bool {
	equal, _ := bytesOutsidePatchesEqualContext(context.Background(), before, after, patches)
	return equal
}

func bytesOutsidePatchesEqualContext(ctx context.Context, before, after []byte, patches []bytePatch) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	sort.Slice(patches, func(i, j int) bool { return patches[i].Start < patches[j].Start })
	oldAt, newAt := 0, 0
	for _, patch := range patches {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		width := patch.Start - oldAt
		if width < 0 || patch.Start > len(before) || patch.End < patch.Start || patch.End > len(before) || newAt+width > len(after) {
			return false, nil
		}
		equal, err := bytesEqualContext(ctx, before[oldAt:patch.Start], after[newAt:newAt+width])
		if err != nil {
			return false, err
		}
		if !equal {
			return false, nil
		}
		oldAt = patch.End
		newAt += width + len(patch.Text)
	}
	if newAt > len(after) {
		return false, nil
	}
	return bytesEqualContext(ctx, before[oldAt:], after[newAt:])
}

func bytesEqualContext(ctx context.Context, left, right []byte) (bool, error) {
	if len(left) != len(right) {
		return false, nil
	}
	const chunkSize = 64 << 10
	for len(left) > 0 {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		n := min(len(left), chunkSize)
		if !bytes.Equal(left[:n], right[:n]) {
			return false, nil
		}
		left, right = left[n:], right[n:]
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return true, nil
}

func markdownBodyStart(source []byte) (int, error) {
	layout, ok, err := documentlayout.Split(source)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, nil
	}
	return layout.BodyStart, nil
}
