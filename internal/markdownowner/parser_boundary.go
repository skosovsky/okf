package markdownowner

import (
	"context"
	"unicode/utf8"

	"github.com/skosovsky/okf/internal/markdownlimit"
	"github.com/yuin/goldmark/ast"
)

const markdownContextChunk = 64 << 10

func validateMarkdownBytesContext(ctx context.Context, source []byte, kind markdownlimit.Kind) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for offset, nextCheck := 0, 0; offset < len(source); {
		if offset >= nextCheck {
			if err := ctx.Err(); err != nil {
				return err
			}
			nextCheck = offset + markdownContextChunk
		}
		value, size := utf8.DecodeRune(source[offset:])
		if value == utf8.RuneError && size == 1 {
			return ownershipError("invalid_utf8", Span{Start: offset, End: offset + 1}, false, nil)
		}
		offset += size
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	limit := markdownlimit.MaxDocumentBytes
	if kind == markdownlimit.ResourceBodyBytes {
		limit = markdownlimit.MaxBodyBytes
	}
	if len(source) > limit {
		return markdownlimit.NewError(kind, len(source))
	}
	return ctx.Err()
}

func validateMarkdownStringContext(ctx context.Context, source string, kind markdownlimit.Kind) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for offset, nextCheck := 0, 0; offset < len(source); {
		if offset >= nextCheck {
			if err := ctx.Err(); err != nil {
				return err
			}
			nextCheck = offset + markdownContextChunk
		}
		value, size := utf8.DecodeRuneInString(source[offset:])
		if value == utf8.RuneError && size == 1 {
			return ownershipError("invalid_utf8", Span{Start: offset, End: offset + 1}, false, nil)
		}
		offset += size
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	limit := markdownlimit.MaxDocumentBytes
	if kind == markdownlimit.ResourceBodyBytes {
		limit = markdownlimit.MaxBodyBytes
	}
	if len(source) > limit {
		return markdownlimit.NewError(kind, len(source))
	}
	return ctx.Err()
}

func parseMarkdownContext(ctx context.Context, source []byte, kind markdownlimit.Kind) (ast.Node, error) {
	if err := validateMarkdownBytesContext(ctx, source, kind); err != nil {
		return nil, err
	}
	return parseValidatedMarkdownContext(ctx, source)
}

func parseValidatedMarkdownContext(ctx context.Context, source []byte) (ast.Node, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	document := parseMarkdown(source)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return document, nil
}
