package markdownowner

import (
	"context"

	"github.com/skosovsky/okf/internal/markdownlimit"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// HeadingIDsContext returns Goldmark's canonical automatic heading IDs in
// source order under the shared bounded Markdown body contract.
func HeadingIDsContext(ctx context.Context, body string) ([]string, error) {
	if err := validateMarkdownStringContext(ctx, body, markdownlimit.ResourceBodyBytes); err != nil {
		return nil, err
	}
	source := []byte(body)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	markdown := goldmark.New(goldmark.WithParserOptions(parser.WithAutoHeadingID()))
	document := markdown.Parser().Parse(text.NewReader(source))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var ids []string
	err := ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if err := ctx.Err(); err != nil {
			return ast.WalkStop, err
		}
		if !entering {
			return ast.WalkContinue, nil
		}
		if _, ok := node.(*ast.Heading); !ok {
			return ast.WalkContinue, nil
		}
		value, ok := node.AttributeString("id")
		if !ok {
			return ast.WalkContinue, nil
		}
		switch id := value.(type) {
		case []byte:
			owned, err := stringFromBytesContext(ctx, id)
			if err != nil {
				return ast.WalkStop, err
			}
			ids = append(ids, owned)
		case string:
			owned, err := stringFromStringContext(ctx, id)
			if err != nil {
				return ast.WalkStop, err
			}
			ids = append(ids, owned)
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

func stringFromBytesContext(ctx context.Context, value []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	out := make([]byte, 0, len(value))
	for offset := 0; offset < len(value); offset += markdownContextChunk {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		out = append(out, value[offset:min(offset+markdownContextChunk, len(value))]...)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return string(out), nil
}

func stringFromStringContext(ctx context.Context, value string) (string, error) {
	return stringFromBytesContext(ctx, []byte(value))
}
