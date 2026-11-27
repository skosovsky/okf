package markdownowner

import (
	"context"

	"github.com/skosovsky/okf/internal/markdownlimit"
	"github.com/yuin/goldmark/ast"
)

type Link struct {
	Text   string
	Target string
}

// LinksContext lists inline and reference-style links parsed from prose.
func LinksContext(ctx context.Context, body []byte) ([]Link, error) {
	document, err := parseMarkdownContext(ctx, body, markdownlimit.ResourceBodyBytes)
	if err != nil {
		return nil, err
	}
	var links []Link
	err = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if err := ctx.Err(); err != nil {
			return ast.WalkStop, err
		}
		if link, ok := node.(*ast.Link); entering && ok {
			links = append(links, Link{Text: string(link.Text(body)), Target: string(link.Destination)})
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return nil, err
	}
	return links, ctx.Err()
}
