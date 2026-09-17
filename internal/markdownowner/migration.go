package markdownowner

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/skosovsky/okf/internal/documentlayout"
	"github.com/skosovsky/okf/internal/markdownlimit"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/util"
)

// Span is a half-open byte range in the original full Markdown document.
type Span struct {
	Start int
	End   int
}

// OwnershipError is a fail-closed collector error with a stable machine code
// and exact source span. Ambiguous distinguishes conflicting ownership from an
// unsupported or malformed presentation.
type OwnershipError struct {
	Code      string
	Span      Span
	Ambiguous bool
	Err       error
}

func (e *OwnershipError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err != nil {
		return fmt.Sprintf("markdownowner: %s: %v", e.Code, e.Err)
	}
	return "markdownowner: " + e.Code
}

func (e *OwnershipError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// MarkdownSection is one parser-owned top-level heading section.
type MarkdownSection struct {
	Heading     string
	Level       int
	HeadingSpan Span
	Span        Span
}

// CitationEntry is one numbered or bullet entry in the top-level Citations
// section. Number is zero for bullet entries.
type CitationEntry struct {
	Ordinal     int
	Number      uint64
	Span        Span
	ContentSpan Span
	Title       string
	Resource    string
	LinkTitle   string
	Raw         string
}

type CitationSectionProjection struct {
	Found    bool
	Section  MarkdownSection
	Entries  []CitationEntry
	Residual []Span
	Opaque   []Span
}

// FootnoteRef is one prose-owned keyed reference.
type FootnoteRef struct {
	Label           string
	NormalizedLabel string
	Span            Span
}

// FootnoteDef is one footnote definition, including all owned continuation
// lines. Content preserves the exact bytes from ContentSpan.
type FootnoteDef struct {
	Label           string
	NormalizedLabel string
	Span            Span
	ContentSpan     Span
	Content         string
}

// NumericMarker is one legacy numeric claim marker in ordinary prose.
type NumericMarker struct {
	Number uint64
	Span   Span
}

// LabelCollision exposes colliding Goldmark-normalized labels.
type LabelCollision struct {
	NormalizedLabel string
	Labels          []string
	Spans           []Span
}

// ComputationBoundary contains top-level Computation sections and their
// directly-owned, closed fences.
type ComputationBoundary struct {
	Sections []MarkdownSection
	Fences   []Span
}

// MigrationOwnership is the neutral parser-backed migration projection.
type MigrationOwnership struct {
	Sections              []MarkdownSection
	CitationEntries       []CitationEntry
	NumericMarkers        []NumericMarker
	FootnoteReferences    []FootnoteRef
	FootnoteDefinitions   []FootnoteDef
	LabelCollisions       []LabelCollision
	Computation           ComputationBoundary
	CitationsSectionIndex int
}

var (
	numericMarkerPattern = regexp.MustCompile(`\[(\d+)\]`)
	footnoteRefPattern   = regexp.MustCompile(`\[\^([^\]\r\n]+)\]`)
	footnoteDefPattern   = regexp.MustCompile(`^ {0,3}\[\^([^\]\r\n]+)\]:[ \t]*(.*)$`)
)

// CollectCitationSectionProjection returns parser-owned full-file spans for a
// single top-level Citations section. Non-whitespace bytes not owned by an
// entry remain explicit Residual observations.
func CollectCitationSectionProjection(ctx context.Context, source []byte) (CitationSectionProjection, error) {
	if err := validateMarkdownBytesContext(ctx, source, markdownlimit.ResourceDocumentBytes); err != nil {
		return CitationSectionProjection{}, err
	}
	bodyStart := 0
	if layout, ok, err := documentlayout.SplitContext(ctx, source); err != nil {
		return CitationSectionProjection{}, ownershipError("frontmatter_boundary", Span{Start: 0, End: len(source)}, false, err)
	} else if ok {
		bodyStart = layout.BodyStart
	}
	body := source[bodyStart:]
	document, err := parseValidatedMarkdownContext(ctx, body)
	if err != nil {
		return CitationSectionProjection{}, err
	}
	sections, err := collectSections(ctx, body, document, bodyStart)
	if err != nil {
		return CitationSectionProjection{}, err
	}
	var citationSections []MarkdownSection
	for _, section := range sections {
		if err := ctx.Err(); err != nil {
			return CitationSectionProjection{}, err
		}
		if section.Level == 1 && exactRawATXHeading(source, section.HeadingSpan, "Citations") {
			citationSections = append(citationSections, section)
		}
	}
	if len(citationSections) == 0 {
		return CitationSectionProjection{}, nil
	}
	if len(citationSections) > 1 {
		return CitationSectionProjection{}, ownershipError(
			"multiple_citations_sections",
			unionSpan(citationSections[0].HeadingSpan, citationSections[len(citationSections)-1].HeadingSpan),
			true,
			nil,
		)
	}
	projection := CitationSectionProjection{Found: true, Section: citationSections[0]}
	projection.Entries, err = collectParserCitationEntries(ctx, source, body, document, bodyStart, projection.Section)
	if err != nil {
		return CitationSectionProjection{}, err
	}

	localOpaque, err := collectASTOpaqueContext(ctx, body, document)
	if err != nil {
		return CitationSectionProjection{}, err
	}
	for _, opaque := range localOpaque {
		if err := ctx.Err(); err != nil {
			return CitationSectionProjection{}, err
		}
		full := shift(opaque, bodyStart)
		if intersection, ok := intersectSpan(full, projection.Section.Span); ok &&
			!spansOverlap(intersection, projection.Section.HeadingSpan) {
			projection.Opaque = append(projection.Opaque, intersection)
		}
	}
	if err := sortSpansContext(ctx, projection.Opaque); err != nil {
		return CitationSectionProjection{}, err
	}
	projection.Opaque = mergeSpans(projection.Opaque)

	excluded := []Span{projection.Section.HeadingSpan}
	for _, span := range projection.Opaque {
		if err := ctx.Err(); err != nil {
			return CitationSectionProjection{}, err
		}
		excluded = append(excluded, span)
	}
	for _, entry := range projection.Entries {
		if err := ctx.Err(); err != nil {
			return CitationSectionProjection{}, err
		}
		excluded = append(excluded, entry.Span)
	}
	if err := sortSpansContext(ctx, excluded); err != nil {
		return CitationSectionProjection{}, err
	}
	projection.Residual = residualSpans(source, projection.Section.Span, mergeSpans(excluded))
	if err := ctx.Err(); err != nil {
		return CitationSectionProjection{}, err
	}
	return projection, nil
}

// CollectMarkdownMigrationOwnership returns exact full-file byte ownership for
// legacy citations, claim markers, footnotes, and computation boundaries.
func CollectMarkdownMigrationOwnership(ctx context.Context, source []byte) (MigrationOwnership, error) {
	if err := validateMarkdownBytesContext(ctx, source, markdownlimit.ResourceDocumentBytes); err != nil {
		return MigrationOwnership{}, err
	}
	bodyStart := 0
	if layout, ok, err := documentlayout.SplitContext(ctx, source); err != nil {
		return MigrationOwnership{}, ownershipError("frontmatter_boundary", Span{Start: 0, End: len(source)}, false, err)
	} else if ok {
		bodyStart = layout.BodyStart
	}
	body := source[bodyStart:]
	document, err := parseValidatedMarkdownContext(ctx, body)
	if err != nil {
		return MigrationOwnership{}, err
	}

	sections, err := collectSections(ctx, body, document, bodyStart)
	if err != nil {
		return MigrationOwnership{}, err
	}
	out := MigrationOwnership{Sections: sections, CitationsSectionIndex: -1}
	for index, section := range sections {
		switch {
		case section.Level == 1 && exactRawATXHeading(source, section.HeadingSpan, "Citations"):
			if out.CitationsSectionIndex >= 0 {
				return MigrationOwnership{}, ownershipError(
					"multiple_citations_sections",
					unionSpan(sections[out.CitationsSectionIndex].HeadingSpan, section.HeadingSpan),
					true,
					nil,
				)
			}
			out.CitationsSectionIndex = index
		case section.Level == 1 && exactRawATXHeading(source, section.HeadingSpan, "Computation"):
			out.Computation.Sections = append(out.Computation.Sections, section)
		}
	}
	fences, err := computationFencesFromASTContext(ctx, body, document)
	if err != nil {
		return MigrationOwnership{}, err
	}
	for _, fence := range fences {
		out.Computation.Fences = append(out.Computation.Fences, Span{Start: bodyStart + fence.Start, End: bodyStart + fence.End})
	}

	opaque, err := collectBlockOpaque(ctx, body, document, bodyStart)
	if err != nil {
		return MigrationOwnership{}, err
	}
	if out.CitationsSectionIndex >= 0 {
		entries, err := collectParserCitationEntries(
			ctx,
			source,
			body,
			document,
			bodyStart,
			sections[out.CitationsSectionIndex],
		)
		if err != nil {
			return MigrationOwnership{}, err
		}
		out.CitationEntries = entries
		excluded := []Span{sections[out.CitationsSectionIndex].HeadingSpan}
		for _, entry := range entries {
			excluded = append(excluded, entry.Span)
		}
		if residual := residualSpans(source, sections[out.CitationsSectionIndex].Span, excluded); len(residual) > 0 {
			return MigrationOwnership{}, ownershipError("unsupported_citation_entry", residual[0], false, nil)
		}
	}
	definitions, definitionSpans, err := collectFootnoteDefinitions(ctx, source, bodyStart, opaque)
	if err != nil {
		return MigrationOwnership{}, err
	}
	out.FootnoteDefinitions = definitions
	citationSpan := Span{}
	if out.CitationsSectionIndex >= 0 {
		citationSpan = sections[out.CitationsSectionIndex].Span
	}
	out.NumericMarkers, out.FootnoteReferences, err = collectInlineTokens(ctx, body, document, bodyStart, citationSpan, definitionSpans)
	if err != nil {
		return MigrationOwnership{}, err
	}
	out.LabelCollisions, err = collectLabelCollisionsContext(ctx, out.FootnoteReferences, out.FootnoteDefinitions)
	if err != nil {
		return MigrationOwnership{}, err
	}
	if err := ctx.Err(); err != nil {
		return MigrationOwnership{}, err
	}
	return out, nil
}

func collectSections(ctx context.Context, body []byte, document ast.Node, bodyStart int) ([]MarkdownSection, error) {
	type headingOwner struct {
		node *ast.Heading
		span Span
	}
	var headings []headingOwner
	for child := document.FirstChild(); child != nil; child = child.NextSibling() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		heading, ok := child.(*ast.Heading)
		if !ok {
			continue
		}
		span, ok := blockLineSpan(body, heading)
		if !ok {
			return nil, ownershipError("heading_source_anchor", Span{Start: bodyStart, End: bodyStart + len(body)}, false, nil)
		}
		headings = append(headings, headingOwner{node: heading, span: span})
	}
	var sections []MarkdownSection
	for index, owner := range headings {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := len(body)
		for next := index + 1; next < len(headings); next++ {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if headings[next].node.Level <= owner.node.Level {
				end = headings[next].span.Start
				break
			}
		}
		sections = append(sections, MarkdownSection{
			Heading:     strings.TrimSpace(string(owner.node.Text(body))),
			Level:       owner.node.Level,
			HeadingSpan: shift(owner.span, bodyStart),
			Span:        shift(Span{Start: owner.span.Start, End: end}, bodyStart),
		})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return sections, nil
}

func collectBlockOpaque(ctx context.Context, body []byte, document ast.Node, bodyStart int) ([]Span, error) {
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
				out = append(out, shift(Span{Start: metadata.Start, End: metadata.End}, bodyStart))
			}
		case *ast.CodeBlock, *ast.HTMLBlock:
			if span, ok := blockLineSpan(body, value); ok {
				out = append(out, shift(span, bodyStart))
			}
		case *ast.RawHTML:
			for index := 0; index < value.Segments.Len(); index++ {
				segment := value.Segments.At(index)
				out = append(out, shift(Span{Start: segment.Start, End: segment.Stop}, bodyStart))
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func collectParserCitationEntries(
	ctx context.Context,
	source, body []byte,
	document ast.Node,
	bodyStart int,
	section MarkdownSection,
) ([]CitationEntry, error) {
	var out []CitationEntry
	seen := make(map[uint64]Span)
	for child := document.FirstChild(); child != nil; child = child.NextSibling() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		localSpan, ok := descendantBlockSpan(body, child)
		if !ok {
			continue
		}
		fullSpan := shift(localSpan, bodyStart)
		if fullSpan.Start < section.HeadingSpan.End || fullSpan.Start >= section.Span.End {
			continue
		}
		switch value := child.(type) {
		case *ast.List:
			if value.IsOrdered() {
				continue
			}
			for item := value.FirstChild(); item != nil; item = item.NextSibling() {
				entry, ok, err := parserCitationEntry(source, body, item, bodyStart, len(out)+1, 0)
				if err != nil {
					return nil, err
				}
				if ok {
					out = append(out, entry)
				}
			}
		case *ast.Paragraph:
			entries, err := numericParagraphCitationEntries(source, body, value, bodyStart, len(out)+1)
			if err != nil {
				return nil, err
			}
			for _, entry := range entries {
				if previous, duplicate := seen[entry.Number]; duplicate {
					return nil, ownershipError("duplicate_citation_number", unionSpan(previous, entry.Span), true, nil)
				}
				seen[entry.Number] = entry.Span
				entry.Ordinal = len(out) + 1
				out = append(out, entry)
			}
		}
	}
	return out, nil
}

func parserCitationEntry(
	source, body []byte,
	node ast.Node,
	bodyStart, ordinal int,
	number uint64,
) (CitationEntry, bool, error) {
	local, ok := descendantBlockSpan(body, node)
	if !ok {
		return CitationEntry{}, false, nil
	}
	contentStart, ok := firstContentOffset(node)
	if !ok {
		return CitationEntry{}, false, nil
	}
	contentEnd := trimTrailingLineEndings(source, bodyStart+local.End)
	entry := CitationEntry{
		Ordinal:     ordinal,
		Number:      number,
		Span:        shift(local, bodyStart),
		ContentSpan: Span{Start: bodyStart + contentStart, End: contentEnd},
	}
	if entry.ContentSpan.End < entry.ContentSpan.Start {
		entry.ContentSpan.End = entry.ContentSpan.Start
	}
	entry.Raw = string(source[entry.ContentSpan.Start:entry.ContentSpan.End])
	if err := populateCitationDestination(&entry, body, node, bodyStart); err != nil {
		return CitationEntry{}, false, err
	}
	return entry, true, nil
}

func populateCitationDestination(entry *CitationEntry, body []byte, node ast.Node, bodyStart int) error {
	type destination struct {
		title, resource, linkTitle string
		span                       Span
	}
	var destinations []destination
	entryLocal := Span{
		Start: entry.ContentSpan.Start - bodyStart,
		End:   entry.ContentSpan.End - bodyStart,
	}
	usedAutoLinks := make(map[Span]struct{})
	_ = ast.Walk(node, func(current ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch value := current.(type) {
		case *ast.Image:
			return ast.WalkSkipChildren, nil
		case *ast.Link:
			span, ok := inlineNodeSpan(body, value)
			if !ok || !spanContains(entryLocal, span) {
				return ast.WalkSkipChildren, nil
			}
			fullSpan := shift(span, bodyStart)
			destinations = append(destinations, destination{
				title:     strings.TrimSpace(string(value.Text(body))),
				resource:  string(value.Destination),
				linkTitle: string(util.UnescapePunctuations(value.Title)),
				span:      fullSpan,
			})
			return ast.WalkSkipChildren, nil
		case *ast.AutoLink:
			span, ok := autoLinkSpanWithin(body, entryLocal, value, usedAutoLinks)
			if !ok {
				return ast.WalkSkipChildren, nil
			}
			usedAutoLinks[span] = struct{}{}
			destinations = append(destinations, destination{
				title:    string(value.Label(body)),
				resource: string(value.URL(body)),
				span:     shift(span, bodyStart),
			})
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	if len(destinations) > 1 {
		span := entry.ContentSpan
		for _, candidate := range destinations {
			span = unionSpan(span, candidate.span)
		}
		return ownershipError("ambiguous_citation_destination", span, true, nil)
	}
	if len(destinations) == 1 {
		entry.Title = destinations[0].title
		entry.Resource = destinations[0].resource
		entry.LinkTitle = destinations[0].linkTitle
	} else {
		trimmed := strings.TrimSpace(entry.Raw)
		if strings.IndexAny(trimmed, " \t\r\n[]()<>") < 0 {
			entry.Resource = trimmed
		}
	}
	return nil
}

func numericCitationPrefix(source []byte, paragraph *ast.Paragraph) (int, uint64, bool) {
	lines := paragraph.Lines()
	if lines.Len() == 0 {
		return 0, 0, false
	}
	segment := lines.At(0)
	lineEnd := trimLineEnding(source, segment.Start, segment.Stop)
	index := segment.Start
	if index >= lineEnd || source[index] != '[' {
		return 0, 0, false
	}
	index++
	digitsStart := index
	for index < lineEnd && source[index] >= '0' && source[index] <= '9' {
		index++
	}
	if index == digitsStart || index >= lineEnd || source[index] != ']' {
		return 0, 0, false
	}
	number, err := strconv.ParseUint(string(source[digitsStart:index]), 10, 64)
	if err != nil || number == 0 {
		return 0, 0, false
	}
	index++
	if index >= lineEnd || source[index] != ' ' && source[index] != '\t' {
		return 0, 0, false
	}
	for index < lineEnd && (source[index] == ' ' || source[index] == '\t') {
		index++
	}
	return index, number, true
}

func numericParagraphCitationEntries(
	source, body []byte,
	paragraph *ast.Paragraph,
	bodyStart, firstOrdinal int,
) ([]CitationEntry, error) {
	type startOwner struct {
		lineIndex    int
		contentStart int
		number       uint64
	}
	lines := paragraph.Lines()
	var starts []startOwner
	for index := 0; index < lines.Len(); index++ {
		segment := lines.At(index)
		synthetic := ast.NewParagraph()
		synthetic.Lines().Append(segment)
		contentStart, number, ok := numericCitationPrefix(body, synthetic)
		if ok {
			starts = append(starts, startOwner{lineIndex: index, contentStart: contentStart, number: number})
		}
	}
	var out []CitationEntry
	for index, owner := range starts {
		first := lines.At(owner.lineIndex)
		end := fullLineEnd(body, fullLineStart(body, first.Start))
		if index+1 < len(starts) {
			next := lines.At(starts[index+1].lineIndex)
			end = fullLineStart(body, next.Start)
		} else if lines.Len() > 0 {
			last := lines.At(lines.Len() - 1)
			end = fullLineEnd(body, fullLineStart(body, last.Start))
		}
		entry := CitationEntry{
			Ordinal:     firstOrdinal + index,
			Number:      owner.number,
			Span:        shift(Span{Start: fullLineStart(body, first.Start), End: end}, bodyStart),
			ContentSpan: Span{Start: bodyStart + owner.contentStart, End: trimTrailingLineEndings(source, bodyStart+end)},
		}
		entry.Raw = string(source[entry.ContentSpan.Start:entry.ContentSpan.End])
		if err := populateCitationDestination(&entry, body, paragraph, bodyStart); err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, nil
}

func firstContentOffset(node ast.Node) (int, bool) {
	offset := -1
	_ = ast.Walk(node, func(current ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if current.Type() != ast.TypeBlock {
			return ast.WalkContinue, nil
		}
		lines := current.Lines()
		if lines != nil && lines.Len() > 0 {
			start := lines.At(0).Start
			if offset < 0 || start < offset {
				offset = start
			}
		}
		return ast.WalkContinue, nil
	})
	return offset, offset >= 0
}

func descendantBlockSpan(source []byte, node ast.Node) (Span, bool) {
	start, end := len(source), -1
	_ = ast.Walk(node, func(current ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if fence, ok := current.(*ast.FencedCodeBlock); ok {
			if metadata, ok := fenceMetadataFor(fence); ok {
				start = min(start, metadata.Start)
				end = max(end, metadata.End)
			}
		}
		if current.Type() != ast.TypeBlock {
			return ast.WalkContinue, nil
		}
		lines := current.Lines()
		if lines != nil && lines.Len() > 0 {
			first, last := lines.At(0), lines.At(lines.Len()-1)
			start = min(start, fullLineStart(source, first.Start))
			end = max(end, fullLineEnd(source, fullLineStart(source, last.Start)))
		}
		return ast.WalkContinue, nil
	})
	return Span{Start: start, End: end}, end >= start
}

func inlineNodeSpan(source []byte, node ast.Node) (Span, bool) {
	start := node.Pos()
	if start < 0 || start >= len(source) {
		return Span{}, false
	}
	labelStart := start
	if source[labelStart] == '!' {
		labelStart++
	}
	labelEnd, ok := bracketEnd(source, labelStart)
	if !ok {
		return Span{}, false
	}

	var reference *ast.ReferenceLink
	switch value := node.(type) {
	case *ast.Link:
		reference = value.Reference
	case *ast.Image:
		reference = value.Reference
	default:
		return Span{}, false
	}
	if reference == nil {
		end, ok := inlineLinkDestinationEnd(source, labelEnd)
		return Span{Start: start, End: end}, ok
	}
	switch reference.Type {
	case ast.ReferenceLinkShortcut:
		return Span{Start: start, End: labelEnd}, true
	case ast.ReferenceLinkFull, ast.ReferenceLinkCollapsed:
		end, ok := bracketEnd(source, labelEnd)
		return Span{Start: start, End: end}, ok
	default:
		return Span{}, false
	}
}

func bracketEnd(source []byte, open int) (int, bool) {
	if open < 0 || open >= len(source) || source[open] != '[' {
		return 0, false
	}
	depth := 1
	for index := open + 1; index < len(source); index++ {
		switch source[index] {
		case '\\':
			index++
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return index + 1, true
			}
		}
	}
	return 0, false
}

func inlineLinkDestinationEnd(source []byte, open int) (int, bool) {
	if open < 0 || open >= len(source) || source[open] != '(' {
		return 0, false
	}
	index := skipMarkdownSpaces(source, open+1)
	if index >= len(source) {
		return 0, false
	}
	if source[index] == ')' {
		return index + 1, true
	}

	if source[index] == '<' {
		index++
		for index < len(source) && source[index] != '>' {
			if source[index] == '\\' {
				index++
			}
			index++
		}
		if index >= len(source) {
			return 0, false
		}
		index++
	} else {
		destinationStart := index
		nesting := 0
		for index < len(source) {
			switch source[index] {
			case '\\':
				index += 2
				continue
			case '(':
				nesting++
			case ')':
				if nesting == 0 {
					if index == destinationStart {
						return 0, false
					}
					return index + 1, true
				}
				nesting--
			default:
				if isMarkdownWhitespace(source[index]) && nesting == 0 {
					goto destinationDone
				}
			}
			index++
		}
		return 0, false
	}

destinationDone:
	index = skipMarkdownSpaces(source, index)
	if index >= len(source) {
		return 0, false
	}
	if source[index] == ')' {
		return index + 1, true
	}

	openTitle := source[index]
	closeTitle := openTitle
	if openTitle == '(' {
		closeTitle = ')'
	} else if openTitle != '\'' && openTitle != '"' {
		return 0, false
	}
	index++
	for index < len(source) && source[index] != closeTitle {
		if source[index] == '\\' {
			index++
		}
		index++
	}
	if index >= len(source) {
		return 0, false
	}
	index = skipMarkdownSpaces(source, index+1)
	if index >= len(source) || source[index] != ')' {
		return 0, false
	}
	return index + 1, true
}

func skipMarkdownSpaces(source []byte, index int) int {
	for index < len(source) && isMarkdownWhitespace(source[index]) {
		index++
	}
	return index
}

func autoLinkSpanWithin(source []byte, within Span, link *ast.AutoLink, used map[Span]struct{}) (Span, bool) {
	within.Start = max(0, within.Start)
	within.End = min(len(source), within.End)
	if within.End <= within.Start {
		return Span{}, false
	}
	label := link.Label(source)
	if len(label) == 0 {
		return Span{}, false
	}
	for cursor := within.Start; cursor < within.End; {
		relative := bytes.Index(source[cursor:within.End], label)
		if relative < 0 {
			return Span{}, false
		}
		labelStart := cursor + relative
		candidate := Span{Start: labelStart - 1, End: labelStart + len(label) + 1}
		if candidate.Start >= within.Start && candidate.End <= within.End &&
			source[candidate.Start] == '<' && source[candidate.End-1] == '>' {
			if _, claimed := used[candidate]; !claimed {
				return candidate, true
			}
		}
		cursor = labelStart + max(1, len(label))
	}
	return Span{}, false
}

func spanContains(outer, inner Span) bool {
	return outer.Start <= inner.Start && inner.End <= outer.End
}

func inlineDescendantTextSpan(node ast.Node) (Span, bool) {
	start, end := int(^uint(0)>>1), -1
	_ = ast.Walk(node, func(current ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if value, ok := current.(*ast.Text); ok {
			start = min(start, value.Segment.Start)
			end = max(end, value.Segment.Stop)
		}
		return ast.WalkContinue, nil
	})
	return Span{Start: start, End: end}, end >= start
}

func trimTrailingLineEndings(source []byte, end int) int {
	for end > 0 && (source[end-1] == '\n' || source[end-1] == '\r') {
		end--
	}
	return end
}

func residualSpans(source []byte, section Span, excluded []Span) []Span {
	var out []Span
	cursor := section.Start
	for _, owned := range excluded {
		if owned.End <= cursor || owned.Start >= section.End {
			continue
		}
		if owned.Start > cursor {
			if span, ok := trimWhitespaceSpan(source, Span{Start: cursor, End: min(owned.Start, section.End)}); ok {
				out = append(out, span)
			}
		}
		cursor = max(cursor, owned.End)
	}
	if cursor < section.End {
		if span, ok := trimWhitespaceSpan(source, Span{Start: cursor, End: section.End}); ok {
			out = append(out, span)
		}
	}
	return out
}

func trimWhitespaceSpan(source []byte, span Span) (Span, bool) {
	for span.Start < span.End && isMarkdownWhitespace(source[span.Start]) {
		span.Start++
	}
	for span.End > span.Start && isMarkdownWhitespace(source[span.End-1]) {
		span.End--
	}
	return span, span.End > span.Start
}

func isMarkdownWhitespace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\r' || value == '\n'
}

func intersectSpan(left, right Span) (Span, bool) {
	span := Span{Start: max(left.Start, right.Start), End: min(left.End, right.End)}
	return span, span.End > span.Start
}

func spansOverlap(left, right Span) bool {
	return left.Start < right.End && right.Start < left.End
}

func collectFootnoteDefinitions(ctx context.Context, source []byte, bodyStart int, opaque []Span) ([]FootnoteDef, []Span, error) {
	var out []FootnoteDef
	var spans []Span
	for start := bodyStart; start < len(source); {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		end := fullLineEnd(source, start)
		contentEnd := trimLineEnding(source, start, end)
		lineSpan := Span{Start: start, End: contentEnd}
		if !overlapsAny(lineSpan, opaque) {
			line := source[start:contentEnd]
			if match := footnoteDefPattern.FindSubmatchIndex(line); match != nil {
				label := string(line[match[2]:match[3]])
				definitionEnd := end
				scan := end
				for scan < len(source) {
					if err := ctx.Err(); err != nil {
						return nil, nil, err
					}
					candidateEnd := fullLineEnd(source, scan)
					candidateContentEnd := trimLineEnding(source, scan, candidateEnd)
					candidate := source[scan:candidateContentEnd]
					if len(bytes.TrimSpace(candidate)) == 0 {
						blankScan := candidateEnd
						foundContinuation := false
						for blankScan < len(source) {
							if err := ctx.Err(); err != nil {
								return nil, nil, err
							}
							nextEnd := fullLineEnd(source, blankScan)
							nextContentEnd := trimLineEnding(source, blankScan, nextEnd)
							next := source[blankScan:nextContentEnd]
							if len(bytes.TrimSpace(next)) == 0 {
								blankScan = nextEnd
								continue
							}
							if footnoteContinuationLine(next) {
								definitionEnd = nextEnd
								scan = nextEnd
								foundContinuation = true
							}
							break
						}
						if foundContinuation {
							continue
						}
						break
					}
					if !footnoteContinuationLine(candidate) {
						break
					}
					definitionEnd = candidateEnd
					scan = candidateEnd
				}
				contentSpan := Span{
					Start: start + match[4],
					End:   trimTrailingLineEndings(source, definitionEnd),
				}
				out = append(out, FootnoteDef{
					Label:           label,
					NormalizedLabel: normalizeLabel(label),
					Span:            Span{Start: start, End: definitionEnd},
					ContentSpan:     contentSpan,
					Content:         string(source[contentSpan.Start:contentSpan.End]),
				})
				spans = append(spans, Span{Start: start, End: definitionEnd})
				end = definitionEnd
			}
		}
		if end <= start {
			break
		}
		start = end
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	return out, spans, nil
}

func footnoteContinuationLine(line []byte) bool {
	column := 0
	for _, value := range line {
		switch value {
		case ' ':
			column++
		case '\t':
			column += 4 - column%4
		default:
			return column >= 4
		}
		if column >= 4 {
			return true
		}
	}
	return false
}

func collectInlineTokens(ctx context.Context, body []byte, document ast.Node, bodyStart int, citations Span, definitions []Span) ([]NumericMarker, []FootnoteRef, error) {
	var markers []NumericMarker
	var references []FootnoteRef
	err := ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if err := ctx.Err(); err != nil {
			return ast.WalkStop, err
		}
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node.(type) {
		case *ast.Paragraph, *ast.Heading:
		default:
			return ast.WalkContinue, nil
		}
		excluded, err := inlineExcludedSpansContext(ctx, body, node)
		if err != nil {
			return ast.WalkStop, err
		}
		footnoteExcluded, err := footnoteExcludedSpansContext(ctx, body, excluded)
		if err != nil {
			return ast.WalkStop, err
		}
		lines := node.Lines()
		for index := 0; index < lines.Len(); index++ {
			if err := ctx.Err(); err != nil {
				return ast.WalkStop, err
			}
			segment := lines.At(index)
			value := body[segment.Start:segment.Stop]
			for _, match := range numericMarkerPattern.FindAllSubmatchIndex(value, -1) {
				if err := ctx.Err(); err != nil {
					return ast.WalkStop, err
				}
				local := Span{Start: segment.Start + match[0], End: segment.Start + match[1]}
				full := shift(local, bodyStart)
				if escapedAt(value, match[0]) || containsOffset(citations, full.Start) ||
					overlapsAny(full, definitions) || overlapsAny(local, excluded) {
					continue
				}
				number, err := strconv.ParseUint(string(value[match[2]:match[3]]), 10, 64)
				if err != nil || number == 0 {
					return ast.WalkStop, ownershipError("invalid_claim_marker", full, false, nil)
				}
				markers = append(markers, NumericMarker{Number: number, Span: full})
			}
			for _, match := range footnoteRefPattern.FindAllSubmatchIndex(value, -1) {
				if err := ctx.Err(); err != nil {
					return ast.WalkStop, err
				}
				local := Span{Start: segment.Start + match[0], End: segment.Start + match[1]}
				full := shift(local, bodyStart)
				if escapedAt(value, match[0]) ||
					match[0] > 0 && value[match[0]-1] == '!' ||
					match[1] < len(value) && value[match[1]] == '[' ||
					overlapsAny(full, definitions) ||
					overlapsAny(local, footnoteExcluded) {
					continue
				}
				label := string(value[match[2]:match[3]])
				references = append(references, FootnoteRef{Label: label, NormalizedLabel: normalizeLabel(label), Span: full})
			}
		}
		return ast.WalkSkipChildren, nil
	})
	if err != nil {
		return nil, nil, err
	}
	if err := stableSortContext(ctx, markers, func(left, right NumericMarker) bool {
		return left.Span.Start < right.Span.Start
	}); err != nil {
		return nil, nil, err
	}
	if err := stableSortContext(ctx, references, func(left, right FootnoteRef) bool {
		return left.Span.Start < right.Span.Start
	}); err != nil {
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	return markers, references, nil
}

func inlineExcludedSpans(source []byte, block ast.Node) []Span {
	out, _ := inlineExcludedSpansContext(context.Background(), source, block)
	return out
}

func inlineExcludedSpansContext(ctx context.Context, source []byte, block ast.Node) ([]Span, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var out []Span
	usedAutoLinks := make(map[Span]struct{})
	err := ast.Walk(block, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if err := ctx.Err(); err != nil {
			return ast.WalkStop, err
		}
		if !entering || node == block {
			return ast.WalkContinue, nil
		}
		switch value := node.(type) {
		case *ast.RawHTML:
			for index := 0; index < value.Segments.Len(); index++ {
				if err := ctx.Err(); err != nil {
					return ast.WalkStop, err
				}
				segment := value.Segments.At(index)
				out = append(out, Span{Start: segment.Start, End: segment.Stop})
			}
		case *ast.Link, *ast.Image:
			if span, ok := inlineNodeSpan(source, value); ok {
				out = append(out, span)
			} else if span, ok := inlineDescendantTextSpan(value); ok {
				out = append(out, span)
			}
			return ast.WalkSkipChildren, nil
		case *ast.AutoLink:
			within, ok := descendantBlockSpan(source, block)
			if !ok {
				return ast.WalkSkipChildren, nil
			}
			if span, ok := autoLinkSpanWithin(source, within, value, usedAutoLinks); ok {
				out = append(out, span)
				usedAutoLinks[span] = struct{}{}
			}
			return ast.WalkSkipChildren, nil
		case *ast.Text:
			for parent := value.Parent(); parent != nil && parent != block; parent = parent.Parent() {
				if _, opaque := parent.(*ast.CodeSpan); opaque {
					out = append(out, Span{Start: value.Segment.Start, End: value.Segment.Stop})
					return ast.WalkContinue, nil
				}
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return mergeSpans(out), nil
}

func footnoteExcludedSpansContext(ctx context.Context, source []byte, excluded []Span) ([]Span, error) {
	var out []Span
	for _, span := range excluded {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		match := footnoteRefPattern.FindSubmatchIndex(source[span.Start:span.End])
		if match != nil && match[0] == 0 && match[1] == span.End-span.Start &&
			(span.End == len(source) || source[span.End] != '[') {
			// OKF footnote ownership deliberately wins over CommonMark shortcut
			// reference-link interpretation of the exact same [^label] bytes.
			continue
		}
		out = append(out, span)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func collectLabelCollisionsContext(ctx context.Context, references []FootnoteRef, definitions []FootnoteDef) ([]LabelCollision, error) {
	type group struct {
		labels          map[string]struct{}
		spans           []Span
		definitionCount int
	}
	groups := make(map[string]*group)
	add := func(normalized, label string, span Span, definition bool) {
		current := groups[normalized]
		if current == nil {
			current = &group{labels: make(map[string]struct{})}
			groups[normalized] = current
		}
		current.labels[label] = struct{}{}
		current.spans = append(current.spans, span)
		if definition {
			current.definitionCount++
		}
	}
	for _, reference := range references {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		add(reference.NormalizedLabel, reference.Label, reference.Span, false)
	}
	for _, definition := range definitions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		add(definition.NormalizedLabel, definition.Label, definition.Span, true)
	}
	var keys []string
	for normalized, current := range groups {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(current.labels) > 1 || current.definitionCount > 1 {
			keys = append(keys, normalized)
		}
	}
	if err := stableSortContext(ctx, keys, func(left, right string) bool { return left < right }); err != nil {
		return nil, err
	}
	var out []LabelCollision
	for _, normalized := range keys {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		current := groups[normalized]
		var labels []string
		for label := range current.labels {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			labels = append(labels, label)
		}
		if err := stableSortContext(ctx, labels, func(left, right string) bool { return left < right }); err != nil {
			return nil, err
		}
		if err := sortSpansContext(ctx, current.spans); err != nil {
			return nil, err
		}
		var spans []Span
		for _, span := range current.spans {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			spans = append(spans, span)
		}
		out = append(out, LabelCollision{NormalizedLabel: normalized, Labels: labels, Spans: spans})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func blockLineSpan(source []byte, node ast.Node) (Span, bool) {
	lines := node.Lines()
	if lines == nil || lines.Len() == 0 {
		return Span{}, false
	}
	first, last := lines.At(0), lines.At(lines.Len()-1)
	if first.Start < 0 || last.Stop < first.Start || last.Stop > len(source) {
		return Span{}, false
	}
	start := fullLineStart(source, first.Start)
	end := fullLineEnd(source, fullLineStart(source, last.Start))
	return Span{Start: start, End: end}, true
}

func normalizeLabel(label string) string { return string(util.ToLinkReference([]byte(label))) }
func shift(span Span, delta int) Span    { return Span{Start: span.Start + delta, End: span.End + delta} }
func containsOffset(span Span, offset int) bool {
	return span.End > span.Start && offset >= span.Start && offset < span.End
}
func overlapsAny(span Span, spans []Span) bool {
	for _, candidate := range spans {
		if span.Start < candidate.End && candidate.Start < span.End {
			return true
		}
	}
	return false
}
func sortSpansContext(ctx context.Context, spans []Span) error {
	return stableSortContext(ctx, spans, func(left, right Span) bool {
		if left.Start != right.Start {
			return left.Start < right.Start
		}
		return left.End < right.End
	})
}
func fullLineStart(source []byte, at int) int {
	for at > 0 && source[at-1] != '\n' {
		at--
	}
	return at
}
func fullLineEnd(source []byte, at int) int {
	for at < len(source) && source[at] != '\n' {
		at++
	}
	if at < len(source) {
		at++
	}
	return at
}
func escapedAt(source []byte, at int) bool {
	count := 0
	for at > 0 && source[at-1] == '\\' {
		count++
		at--
	}
	return count%2 == 1
}

func ownershipError(code string, span Span, ambiguous bool, err error) error {
	return &OwnershipError{Code: code, Span: span, Ambiguous: ambiguous, Err: err}
}

func unionSpan(left, right Span) Span {
	if left.End <= left.Start {
		return right
	}
	if right.End <= right.Start {
		return left
	}
	return Span{Start: min(left.Start, right.Start), End: max(left.End, right.End)}
}

func firstInvalidUTF8(source []byte) int {
	for offset := 0; offset < len(source); {
		_, size := utf8.DecodeRune(source[offset:])
		if size == 1 && source[offset] >= utf8.RuneSelf {
			return offset
		}
		offset += size
	}
	return len(source)
}
