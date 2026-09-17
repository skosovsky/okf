package markdownowner

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/skosovsky/okf/internal/markdownlimit"
	"github.com/yuin/goldmark/util"
)

// FootnoteProjection is the parser-owned view of keyed footnote references and
// full multiline definitions in one Markdown body. Each definition preserves
// its complete Span, ContentSpan, and Content, including LF or CRLF line
// endings and tab-, space-, or blank-line-indented continuations.
type FootnoteProjection struct {
	References  []FootnoteRef
	Definitions []FootnoteDef
}

// NormalizeFootnoteLabel applies CommonMark reference-label normalization
// while callers retain the original label for display and diagnostics.
func NormalizeFootnoteLabel(label string) string { return normalizeLabel(label) }

// NormalizeFootnoteLabelContext is the cancellation-aware form of
// NormalizeFootnoteLabel. Work passed to goldmark's folding table is bounded
// to chunks; whitespace collapse remains continuous across chunk boundaries.
func NormalizeFootnoteLabelContext(ctx context.Context, label string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	const chunkSize = 64 << 10
	var out strings.Builder
	pendingSpace, wrote := false, false
	for offset := 0; offset < len(label); {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		end := min(offset+chunkSize, len(label))
		for end < len(label) && !utf8.RuneStart(label[end]) {
			end--
		}
		if end == offset {
			_, size := utf8.DecodeRuneInString(label[offset:])
			end = offset + size
		}
		folded := util.DoFullUnicodeCaseFolding([]byte(label[offset:end]))
		for _, value := range folded {
			if util.IsSpace(value) {
				if wrote {
					pendingSpace = true
				}
				continue
			}
			if pendingSpace {
				out.WriteByte(' ')
				pendingSpace = false
			}
			out.WriteByte(value)
			wrote = true
		}
		offset = end
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return out.String(), nil
}

// CollectFootnotes returns exact byte spans in a Markdown body. Code, raw HTML,
// Link, Image, AutoLink, and reference-definition ownership is excluded.
// Exact whole shortcut-link bytes shaped as [^label] remain OKF footnotes.
func CollectFootnotes(ctx context.Context, markdown []byte) (FootnoteProjection, error) {
	document, err := parseMarkdownContext(ctx, markdown, markdownlimit.ResourceBodyBytes)
	if err != nil {
		return FootnoteProjection{}, err
	}
	opaque, err := collectBlockOpaque(ctx, markdown, document, 0)
	if err != nil {
		return FootnoteProjection{}, err
	}
	definitions, definitionSpans, err := collectFootnoteDefinitions(ctx, markdown, 0, opaque)
	if err != nil {
		return FootnoteProjection{}, err
	}
	_, references, err := collectInlineTokens(ctx, markdown, document, 0, Span{}, definitionSpans)
	if err != nil {
		return FootnoteProjection{}, err
	}
	if err := stableSortContext(ctx, definitions, func(left, right FootnoteDef) bool {
		return left.Span.Start < right.Span.Start
	}); err != nil {
		return FootnoteProjection{}, err
	}
	if err := stableSortContext(ctx, references, func(left, right FootnoteRef) bool {
		return left.Span.Start < right.Span.Start
	}); err != nil {
		return FootnoteProjection{}, err
	}
	if err := ctx.Err(); err != nil {
		return FootnoteProjection{}, err
	}
	return FootnoteProjection{References: references, Definitions: definitions}, nil
}
