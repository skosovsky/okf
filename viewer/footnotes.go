package viewer

import (
	"bytes"
	"context"
	"regexp"
	"strconv"

	"github.com/skosovsky/okf/internal/markdownowner"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

var sourceFootnoteMarker = regexp.MustCompile(`\[\^([^\[\]\r\n]+)\]`)
var sourceFootnotesKey = parser.NewContextKey()

type sourceFootnote struct {
	width  int
	number int
}

type sourceFootnoteParser struct{}

func (sourceFootnoteParser) Trigger() []byte { return []byte{'['} }

func (sourceFootnoteParser) Parse(_ ast.Node, reader text.Reader, pc parser.Context) ast.Node {
	references, _ := pc.Get(sourceFootnotesKey).(map[int]sourceFootnote)
	_, position := reader.Position()
	reference, ok := references[position.Start]
	if !ok {
		return nil
	}
	reader.Advance(reference.width)
	number := strconv.Itoa(reference.number)
	link := ast.NewLink()
	link.Destination = []byte("#fn:source-" + number)
	link.SetAttributeString("class", []byte("source-footnote"))
	link.AppendChild(link, ast.NewString([]byte("S"+number)))
	return link
}

func renderSourceFootnotes(ctx context.Context, markdown goldmark.Markdown, body []byte, sources []Source, out *bytes.Buffer) error {
	sourceNumbers := make(map[string]int, len(sources))
	for i, source := range sources {
		if source.ID == "" {
			continue
		}
		label, err := markdownowner.NormalizeFootnoteLabelContext(ctx, source.ID)
		if err != nil {
			return err
		}
		if _, seen := sourceNumbers[label]; seen {
			sourceNumbers[label] = 0
		} else {
			sourceNumbers[label] = i + 1
		}
	}
	candidates := make(map[int]sourceFootnote)
	for _, match := range sourceFootnoteMarker.FindAllSubmatchIndex(body, -1) {
		at := match[0]
		backslashes := 0
		for i := at - 1; i >= 0 && body[i] == '\\'; i-- {
			backslashes++
		}
		if backslashes%2 != 0 || at > 0 && body[at-1] == '!' {
			continue
		}
		label, err := markdownowner.NormalizeFootnoteLabelContext(ctx, string(body[match[2]:match[3]]))
		if err != nil {
			return err
		}
		if number := sourceNumbers[label]; number != 0 {
			candidates[at] = sourceFootnote{width: match[1] - at, number: number}
		}
	}
	if len(candidates) == 0 {
		return markdown.Convert(body, out)
	}

	// The initial parse distinguishes literal prose from authored footnotes,
	// code and Markdown links. Only unresolved prose markers reach the fallback.
	doc := markdown.Parser().Parse(text.NewReader(body))
	references := make(map[int]sourceFootnote, len(candidates))
	err := ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if err := ctx.Err(); err != nil {
			return ast.WalkStop, err
		}
		if !entering {
			return ast.WalkContinue, nil
		}
		switch value := node.(type) {
		case *ast.CodeSpan, *ast.Link, *ast.Image:
			return ast.WalkSkipChildren, nil
		case *ast.Text:
			if !value.IsRaw() {
				for i := value.Segment.Start; i < value.Segment.Stop; i++ {
					if candidate, ok := candidates[i]; ok {
						references[i] = candidate
					}
				}
			}
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return err
	}
	if len(references) == 0 {
		return markdown.Renderer().Render(out, body, doc)
	}
	pc := parser.NewContext()
	pc.Set(sourceFootnotesKey, references)
	return markdown.Convert(body, out, parser.WithContext(pc))
}
