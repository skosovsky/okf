// Package markdownowner provides neutral Markdown ownership primitives shared
// by read, validation, and mutation surfaces.
package markdownowner

import (
	"bytes"
	"context"
	"strings"

	"github.com/skosovsky/okf/internal/markdownlimit"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

const fenceMetadataAttribute = "okf.markdownowner.fence"

var markdownParser = newMarkdownParser()

// Fence is one fenced code block owned by a top-level "# Computation"
// section. Start and End are byte offsets into the original Markdown. End
// includes the closing fence line terminator when Closed is true.
type Fence struct {
	Start   int
	End     int
	Info    string
	Content string
	Closed  bool
}

// ComputationSection is one parser-owned top-level "# Computation" section.
// Heading and Span are exact byte ranges in the original Markdown.
type ComputationSection struct {
	Heading Span
	Span    Span
	Fences  []Fence
}

type ComputationState string

const (
	ComputationNoSection    ComputationState = "no-section"
	ComputationEmptySection ComputationState = "empty-section"
	ComputationInline       ComputationState = "inline"
	ComputationMalformed    ComputationState = "malformed"
)

type ComputationInspection struct {
	State             ComputationState
	Sections          []ComputationSection
	DirectFences      []Fence
	ConflictingFences []Fence
}

type fenceMetadata struct {
	Start        int
	End          int
	ContentStart int
	ContentEnd   int
	Character    byte
	Length       int
	Closed       bool
}

// ProseLines returns Markdown lines with parser-owned opaque regions replaced
// by spaces. Newline structure and non-opaque bytes are retained for
// downstream link, attribution, and citation parsing.
func ProseLines(markdown string) []string {
	lines, _ := ProseLinesContext(context.Background(), markdown)
	return lines
}

// ProseLinesContext is the cancellation-aware form of ProseLines.
func ProseLinesContext(ctx context.Context, markdown string) ([]string, error) {
	if err := validateMarkdownStringContext(ctx, markdown, markdownlimit.ResourceBodyBytes); err != nil {
		return nil, err
	}
	source := make([]byte, 0, len(markdown))
	for offset := 0; offset < len(markdown); offset += 64 << 10 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		source = append(source, markdown[offset:min(offset+(64<<10), len(markdown))]...)
	}
	document, err := parseValidatedMarkdownContext(ctx, source)
	if err != nil {
		return nil, err
	}
	masked := make([]byte, 0, len(source))
	for offset := 0; offset < len(source); offset += 64 << 10 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		masked = append(masked, source[offset:min(offset+(64<<10), len(source))]...)
	}
	spans, err := collectASTOpaqueContext(ctx, source, document)
	if err != nil {
		return nil, err
	}
	for _, span := range spans {
		for offset := span.Start; offset < span.End; offset += 64 << 10 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			maskBytesExceptNewlines(masked, offset, min(offset+(64<<10), span.End))
		}
	}
	var lines []string
	start := 0
	for index, value := range masked {
		if index%(64<<10) == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if value == '\n' {
			lines = append(lines, string(masked[start:index]))
			start = index + 1
		}
	}
	lines = append(lines, string(masked[start:]))
	return lines, ctx.Err()
}

func InspectComputation(markdown string) ComputationInspection {
	out, _ := InspectComputationContext(context.Background(), markdown)
	return out
}

func InspectComputationContext(ctx context.Context, markdown string) (ComputationInspection, error) {
	if err := validateMarkdownStringContext(ctx, markdown, markdownlimit.ResourceBodyBytes); err != nil {
		return ComputationInspection{}, err
	}
	source := []byte(markdown)
	document, err := parseValidatedMarkdownContext(ctx, source)
	if err != nil {
		return ComputationInspection{}, err
	}
	return inspectComputationFromASTContext(ctx, source, document)
}

func parseMarkdown(source []byte) ast.Node {
	return markdownParser.Parse(text.NewReader(source))
}

func inspectComputationFromASTContext(ctx context.Context, source []byte, document ast.Node) (ComputationInspection, error) {
	sections, err := computationSectionRangesContext(ctx, source, document)
	if err != nil {
		return ComputationInspection{}, err
	}
	if len(sections) == 0 {
		if err := ctx.Err(); err != nil {
			return ComputationInspection{}, err
		}
		return ComputationInspection{State: ComputationNoSection}, nil
	}
	inspection := ComputationInspection{Sections: sections, State: ComputationMalformed}
	err = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if err := ctx.Err(); err != nil {
			return ast.WalkStop, err
		}
		if !entering {
			return ast.WalkContinue, nil
		}
		fence, ok := node.(*ast.FencedCodeBlock)
		if !ok {
			return ast.WalkContinue, nil
		}
		metadata, ok := fenceMetadataFor(fence)
		if !ok {
			return ast.WalkContinue, nil
		}
		sectionIndex := containingComputationSection(metadata.Start, inspection.Sections)
		if sectionIndex < 0 {
			return ast.WalkContinue, nil
		}
		info := ""
		if fence.Info != nil {
			info = strings.TrimSpace(string(fence.Info.Text(source)))
		}
		content, err := fencedContentContext(ctx, source, fence)
		if err != nil {
			return ast.WalkStop, err
		}
		candidate := Fence{
			Start:   metadata.Start,
			End:     metadata.End,
			Info:    info,
			Content: content,
			Closed:  metadata.Closed,
		}
		inspection.Sections[sectionIndex].Fences = append(inspection.Sections[sectionIndex].Fences, candidate)
		if node.Parent() == document {
			inspection.DirectFences = append(inspection.DirectFences, candidate)
		} else {
			inspection.ConflictingFences = append(inspection.ConflictingFences, candidate)
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return ComputationInspection{}, err
	}
	for index := range inspection.Sections {
		if err := stableSortContext(ctx, inspection.Sections[index].Fences, func(left, right Fence) bool {
			return left.Start < right.Start
		}); err != nil {
			return ComputationInspection{}, err
		}
	}
	if err := stableSortContext(ctx, inspection.DirectFences, func(left, right Fence) bool {
		return left.Start < right.Start
	}); err != nil {
		return ComputationInspection{}, err
	}
	if err := stableSortContext(ctx, inspection.ConflictingFences, func(left, right Fence) bool {
		return left.Start < right.Start
	}); err != nil {
		return ComputationInspection{}, err
	}
	switch {
	case len(inspection.Sections) != 1:
		inspection.State = ComputationMalformed
	case len(inspection.DirectFences) == 0 && len(inspection.ConflictingFences) == 0:
		inspection.State = ComputationEmptySection
	case len(inspection.DirectFences) == 1 && inspection.DirectFences[0].Closed && len(inspection.ConflictingFences) == 0:
		inspection.State = ComputationInline
	default:
		inspection.State = ComputationMalformed
	}
	if err := ctx.Err(); err != nil {
		return ComputationInspection{}, err
	}
	return inspection, nil
}

func computationFencesFromASTContext(ctx context.Context, source []byte, document ast.Node) ([]Fence, error) {
	inspection, err := inspectComputationFromASTContext(ctx, source, document)
	if err != nil {
		return nil, err
	}
	var out []Fence
	for _, fence := range inspection.DirectFences {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out = append(out, fence)
	}
	for _, fence := range inspection.ConflictingFences {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out = append(out, fence)
	}
	if err := stableSortContext(ctx, out, func(left, right Fence) bool { return left.Start < right.Start }); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func stableSortContext[T any](ctx context.Context, values []T, less func(left, right T) bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(values) < 2 {
		return ctx.Err()
	}
	var scratch []T
	var zero T
	for range values {
		if err := ctx.Err(); err != nil {
			return err
		}
		scratch = append(scratch, zero)
	}
	source, destination := values, scratch
	for width := 1; width < len(values); width *= 2 {
		for start := 0; start < len(values); start += 2 * width {
			middle := min(start+width, len(values))
			end := min(start+2*width, len(values))
			left, right := start, middle
			for output := start; output < end; output++ {
				if err := ctx.Err(); err != nil {
					return err
				}
				if left >= middle {
					destination[output] = source[right]
					right++
				} else if right >= end {
					destination[output] = source[left]
					left++
				} else if less(source[right], source[left]) {
					destination[output] = source[right]
					right++
				} else {
					destination[output] = source[left]
					left++
				}
			}
		}
		source, destination = destination, source
		if width > len(values)/2 {
			break
		}
	}
	if len(source) > 0 && &source[0] != &values[0] {
		for index := range source {
			if err := ctx.Err(); err != nil {
				return err
			}
			values[index] = source[index]
		}
	}
	return ctx.Err()
}

func computationSectionRangesContext(ctx context.Context, source []byte, document ast.Node) ([]ComputationSection, error) {
	type headingOwner struct {
		heading *ast.Heading
		span    Span
	}
	var headings []headingOwner
	for child := document.FirstChild(); child != nil; child = child.NextSibling() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		heading, ok := child.(*ast.Heading)
		if !ok || heading.Level != 1 {
			continue
		}
		span, ok := blockLineSpan(source, heading)
		if !ok {
			continue
		}
		headings = append(headings, headingOwner{heading: heading, span: span})
	}
	var out []ComputationSection
	for index, owner := range headings {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !exactRawATXHeading(source, owner.span, "Computation") {
			continue
		}
		end := len(source)
		if index+1 < len(headings) {
			end = headings[index+1].span.Start
		}
		out = append(out, ComputationSection{
			Heading: owner.span,
			Span:    Span{Start: owner.span.Start, End: end},
		})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func exactRawATXHeading(source []byte, span Span, literal string) bool {
	if span.Start < 0 || span.End < span.Start || span.End > len(source) {
		return false
	}
	end := trimLineEnding(source, span.Start, span.End)
	return string(source[span.Start:end]) == "# "+literal
}

func containingComputationSection(offset int, sections []ComputationSection) int {
	for index, section := range sections {
		if offset >= section.Heading.End && offset < section.Span.End {
			return index
		}
	}
	return -1
}

func fencedContentContext(ctx context.Context, source []byte, fence *ast.FencedCodeBlock) (string, error) {
	var content bytes.Buffer
	lines := fence.Lines()
	for index := 0; index < lines.Len(); index++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		segment := lines.At(index)
		content.Write(segment.Value(source))
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return content.String(), nil
}

func collectASTOpaque(source []byte, document ast.Node) []Span {
	out, _ := collectASTOpaqueContext(context.Background(), source, document)
	return out
}

func collectASTOpaqueContext(ctx context.Context, source []byte, document ast.Node) ([]Span, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var out []Span
	err := ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if err := ctx.Err(); err != nil {
			return ast.WalkStop, err
		}
		if !entering {
			return ast.WalkContinue, nil
		}
		switch value := node.(type) {
		case *ast.FencedCodeBlock:
			if metadata, ok := fenceMetadataFor(value); ok {
				out = append(out, Span{Start: metadata.Start, End: metadata.End})
			}
			return ast.WalkSkipChildren, nil
		case *ast.CodeBlock, *ast.HTMLBlock:
			if span, ok := blockLineSpan(source, value); ok {
				out = append(out, span)
			}
			return ast.WalkSkipChildren, nil
		case *ast.RawHTML:
			for index := 0; index < value.Segments.Len(); index++ {
				if err := ctx.Err(); err != nil {
					return ast.WalkStop, err
				}
				segment := value.Segments.At(index)
				out = append(out, Span{Start: segment.Start, End: segment.Stop})
			}
		case *ast.Text:
			if textHasOpaqueParent(value) {
				out = append(out, Span{Start: value.Segment.Start, End: value.Segment.Stop})
			}
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return nil, err
	}
	if err := sortSpansContext(ctx, out); err != nil {
		return nil, err
	}
	var merged []Span
	for _, span := range out {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(merged) == 0 || span.Start > merged[len(merged)-1].End {
			merged = append(merged, span)
			continue
		}
		if span.End > merged[len(merged)-1].End {
			merged[len(merged)-1].End = span.End
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return merged, nil
}

func textHasOpaqueParent(node ast.Node) bool {
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		switch parent.(type) {
		case *ast.CodeSpan:
			return true
		case *ast.Paragraph:
			return false
		}
	}
	return false
}

func mergeSpans(spans []Span) []Span {
	if len(spans) < 2 {
		return spans
	}
	out := spans[:1]
	for _, current := range spans[1:] {
		last := &out[len(out)-1]
		if current.Start <= last.End {
			last.End = max(last.End, current.End)
			continue
		}
		out = append(out, current)
	}
	return out
}

func fenceMetadataFor(node ast.Node) (fenceMetadata, bool) {
	value, ok := node.AttributeString(fenceMetadataAttribute)
	if !ok {
		return fenceMetadata{}, false
	}
	metadata, ok := value.(*fenceMetadata)
	if !ok || metadata == nil {
		return fenceMetadata{}, false
	}
	return *metadata, true
}

func maskBytesExceptNewlines(source []byte, start, end int) {
	start = max(start, 0)
	end = min(end, len(source))
	for index := start; index < end; index++ {
		if source[index] != '\n' && source[index] != '\r' {
			source[index] = ' '
		}
	}
}

func trimLineEnding(source []byte, start, end int) int {
	if end > start && source[end-1] == '\n' {
		end--
	}
	if end > start && source[end-1] == '\r' {
		end--
	}
	return end
}

func newMarkdownParser() parser.Parser {
	blocks := parser.DefaultBlockParsers()
	for index := range blocks {
		if blocks[index].Priority == 700 {
			blocks[index].Value = &ownedFencedCodeBlockParser{}
		}
	}
	return parser.NewParser(
		parser.WithBlockParsers(blocks...),
		parser.WithInlineParsers(parser.DefaultInlineParsers()...),
		parser.WithParagraphTransformers(parser.DefaultParagraphTransformers()...),
	)
}

type ownedFenceData struct {
	character byte
	indent    int
	length    int
	node      ast.Node
	metadata  *fenceMetadata
}

var ownedFenceContextKey = parser.NewContextKey()

type ownedFencedCodeBlockParser struct{}

func (p *ownedFencedCodeBlockParser) Trigger() []byte { return []byte{'~', '`'} }

func (p *ownedFencedCodeBlockParser) Open(_ ast.Node, reader text.Reader, context parser.Context) (ast.Node, parser.State) {
	line, segment := reader.PeekLine()
	position := context.BlockOffset()
	if position < 0 || (line[position] != '`' && line[position] != '~') {
		return nil, parser.NoChildren
	}
	character := line[position]
	end := position
	for end < len(line) && line[end] == character {
		end++
	}
	length := end - position
	if length < 3 {
		return nil, parser.NoChildren
	}
	var info *ast.Text
	if end < len(line)-1 {
		rest := line[end:]
		left := util.TrimLeftSpaceLength(rest)
		right := util.TrimRightSpaceLength(rest)
		if left < len(rest)-right {
			value := rest[left : len(rest)-right]
			if character == '`' && bytes.IndexByte(value, '`') >= 0 {
				return nil, parser.NoChildren
			}
			infoStart := segment.Start - segment.Padding + end + left
			infoStop := segment.Stop - right
			if infoStart != infoStop {
				info = ast.NewTextSegment(text.NewSegment(infoStart, infoStop))
			}
		}
	}
	node := ast.NewFencedCodeBlock(info)
	start := fullLineStart(reader.Source(), segment.Start-segment.Padding)
	openEnd := fullLineEnd(reader.Source(), start)
	metadata := &fenceMetadata{
		Start:        start,
		End:          len(reader.Source()),
		ContentStart: openEnd,
		ContentEnd:   len(reader.Source()),
		Character:    character,
		Length:       length,
	}
	node.SetAttributeString(fenceMetadataAttribute, metadata)
	context.Set(ownedFenceContextKey, &ownedFenceData{
		character: character,
		indent:    position,
		length:    length,
		node:      node,
		metadata:  metadata,
	})
	return node, parser.NoChildren
}

func (p *ownedFencedCodeBlockParser) Continue(node ast.Node, reader text.Reader, context parser.Context) parser.State {
	line, segment := reader.PeekLine()
	data, ok := context.Get(ownedFenceContextKey).(*ownedFenceData)
	if !ok || data == nil {
		return parser.Close
	}
	width, position := util.IndentWidth(line, reader.LineOffset())
	if width < 4 {
		end := position
		for end < len(line) && line[end] == data.character {
			end++
		}
		if end-position >= data.length && util.IsBlank(line[end:]) {
			closeStart := fullLineStart(reader.Source(), segment.Start-segment.Padding)
			data.metadata.ContentEnd = closeStart
			data.metadata.End = fullLineEnd(reader.Source(), closeStart)
			data.metadata.Closed = true
			newline := 1
			if line[len(line)-1] != '\n' {
				newline = 0
			}
			reader.Advance(segment.Stop - segment.Start - newline + segment.Padding)
			return parser.Close
		}
	}
	position, padding := util.IndentPositionPadding(line, reader.LineOffset(), segment.Padding, data.indent)
	if position < 0 {
		position = max(0, util.FirstNonSpacePosition(line)) - segment.Padding
		padding = 0
	}
	code := text.NewSegmentPadding(segment.Start+position, segment.Stop, padding)
	code.ForceNewline = true
	node.Lines().Append(code)
	reader.AdvanceAndSetPadding(segment.Stop-segment.Start-position-1, padding)
	return parser.Continue | parser.NoChildren
}

func (p *ownedFencedCodeBlockParser) Close(node ast.Node, reader text.Reader, context parser.Context) {
	data, ok := context.Get(ownedFenceContextKey).(*ownedFenceData)
	if !ok || data == nil || data.node != node {
		return
	}
	if !data.metadata.Closed {
		data.metadata.ContentEnd = len(reader.Source())
		data.metadata.End = len(reader.Source())
	}
	context.Set(ownedFenceContextKey, nil)
}

func (p *ownedFencedCodeBlockParser) CanInterruptParagraph() bool { return true }
func (p *ownedFencedCodeBlockParser) CanAcceptIndentedLine() bool { return false }
