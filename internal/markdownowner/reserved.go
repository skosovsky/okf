package markdownowner

import (
	"context"
	"strings"

	"github.com/skosovsky/okf/internal/markdownlimit"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// TopLevelBlockKind classifies parser-owned direct document children used by
// reserved index.md and log.md structure validation.
type TopLevelBlockKind string

const (
	TopLevelOther   TopLevelBlockKind = "other"
	TopLevelHeading TopLevelBlockKind = "heading"
	TopLevelList    TopLevelBlockKind = "list"
	TopLevelCode    TopLevelBlockKind = "code"
)

// TopLevelLink is one semantic Markdown link. Images are not links.
type TopLevelLink struct {
	Text   string
	Target string
}

// TopLevelListItem is one direct item of a top-level Markdown list.
type TopLevelListItem struct {
	Text              string
	HasLink           bool
	Links             []TopLevelLink
	Description       string
	Span              Span
	Raw               string
	Indent            int
	Continuation      bool
	ContinuationTexts []string
	HasNestedList     bool
}

// TopLevelBlock is one direct child of the Markdown document. Headings inside
// blockquotes/lists and lists inside containers are deliberately not surfaced.
// Raw and Indent retain source presentation needed by reserved-file messages
// without re-lexing Markdown in consumers.
type TopLevelBlock struct {
	Kind         TopLevelBlockKind
	Span         Span
	Raw          string
	Indent       int
	HeadingLevel int
	Text         string
	HasLink      bool
	Items        []TopLevelListItem
}

// TopLevelStructure returns parser-owned direct document blocks. ATX and
// Setext headings share the same heading representation; escaped headings,
// HTML, code, and container-owned blocks remain TopLevelOther.
func TopLevelStructure(markdown string) []TopLevelBlock {
	out, _ := TopLevelStructureContext(context.Background(), markdown)
	return out
}

// TopLevelStructureContext is the cancellation-aware form of
// TopLevelStructure.
func TopLevelStructureContext(ctx context.Context, markdown string) ([]TopLevelBlock, error) {
	if err := validateMarkdownStringContext(ctx, markdown, markdownlimit.ResourceBodyBytes); err != nil {
		return nil, err
	}
	source := []byte(markdown)
	document, err := parseValidatedMarkdownContext(ctx, source)
	if err != nil {
		return nil, err
	}
	var out []TopLevelBlock
	for child := document.FirstChild(); child != nil; child = child.NextSibling() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		block := TopLevelBlock{
			Kind: TopLevelOther,
			Text: strings.TrimSpace(string(child.Text(source))),
		}
		if span, ok := descendantBlockSpan(source, child); ok {
			block.Span = span
			block.Raw = strings.TrimSpace(string(source[span.Start:span.End]))
			block.Indent = sourceIndent(source, span.Start)
		}
		switch node := child.(type) {
		case *ast.Heading:
			block.Kind = TopLevelHeading
			block.HeadingLevel = node.Level
			block.Text = strings.TrimSpace(string(node.Text(source)))
		case *ast.List:
			block.Kind = TopLevelList
			for item := node.FirstChild(); item != nil; item = item.NextSibling() {
				listItem, ok := item.(*ast.ListItem)
				if !ok {
					continue
				}
				projection := directListItemProjection(source, listItem)
				block.Items = append(block.Items, projection)
				block.HasLink = block.HasLink || projection.HasLink
			}
		case *ast.FencedCodeBlock, *ast.CodeBlock:
			block.Kind = TopLevelCode
		default:
			block.HasLink = blockHasLink(child)
		}
		out = append(out, block)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func directListItemProjection(source []byte, item *ast.ListItem) TopLevelListItem {
	projection := TopLevelListItem{Text: strings.TrimSpace(string(item.Text(source)))}
	if span, ok := descendantBlockSpan(source, item); ok {
		projection.Span = span
		projection.Raw = string(source[span.Start:span.End])
		projection.Indent = sourceIndent(source, span.Start)
	}
	var firstLink *ast.Link
	_ = ast.Walk(item, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if node != item {
			if _, nested := node.(*ast.List); nested {
				projection.HasNestedList = true
				return ast.WalkSkipChildren, nil
			}
		}
		link, ok := node.(*ast.Link)
		if !ok {
			return ast.WalkContinue, nil
		}
		if firstLink == nil {
			firstLink = link
		}
		projection.Links = append(projection.Links, TopLevelLink{
			Text:   strings.TrimSpace(string(link.Text(source))),
			Target: string(link.Destination),
		})
		return ast.WalkContinue, nil
	})
	for child := item.FirstChild(); child != nil; child = child.NextSibling() {
		var lines *text.Segments
		switch node := child.(type) {
		case *ast.Paragraph:
			lines = node.Lines()
		case *ast.TextBlock:
			lines = node.Lines()
		}
		if lines != nil && lines.Len() > 1 {
			projection.Continuation = true
			for index := 1; index < lines.Len(); index++ {
				segment := lines.At(index)
				if text := strings.TrimSpace(string(segment.Value(source))); text != "" {
					projection.ContinuationTexts = append(projection.ContinuationTexts, text)
				}
			}
		}
		if child.PreviousSibling() != nil {
			projection.Continuation = true
			if _, nested := child.(*ast.List); !nested {
				if text := strings.TrimSpace(string(child.Text(source))); text != "" {
					projection.ContinuationTexts = append(projection.ContinuationTexts, text)
				}
			}
		}
	}
	projection.HasLink = len(projection.Links) > 0
	if firstLink != nil {
		var trailing strings.Builder
		for sibling := firstLink.NextSibling(); sibling != nil; sibling = sibling.NextSibling() {
			trailing.Write(sibling.Text(source))
		}
		value := strings.TrimSpace(trailing.String())
		if strings.HasPrefix(value, "-") {
			projection.Description = strings.TrimSpace(strings.TrimPrefix(value, "-"))
		}
	}
	return projection
}

func blockHasLink(node ast.Node) bool {
	hasLink := false
	_ = ast.Walk(node, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			if _, ok := node.(*ast.Link); ok {
				hasLink = true
				return ast.WalkStop, nil
			}
		}
		return ast.WalkContinue, nil
	})
	return hasLink
}

func sourceIndent(source []byte, offset int) int {
	lineStart := offset
	for lineStart > 0 && source[lineStart-1] != '\n' && source[lineStart-1] != '\r' {
		lineStart--
	}
	indent := 0
	for lineStart+indent < len(source) {
		switch source[lineStart+indent] {
		case ' ':
			indent++
		case '\t':
			return indent + 4
		default:
			return indent
		}
	}
	return indent
}
