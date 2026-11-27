package markdownowner

import (
	"bytes"
	"context"
	"strings"

	"github.com/skosovsky/okf/internal/markdownlimit"
	"github.com/yuin/goldmark/ast"
)

// ComputationFencesV03Context returns the fences of the first ATX Computation
// section in source order. Heading levels are relative to that section.
func ComputationFencesV03Context(ctx context.Context, body string) ([]Fence, error) {
	if err := validateMarkdownStringContext(ctx, body, markdownlimit.ResourceBodyBytes); err != nil {
		return nil, err
	}
	source := []byte(body)
	document, err := parseValidatedMarkdownContext(ctx, source)
	if err != nil {
		return nil, err
	}
	var section *ast.Heading
	var fences []Fence
	err = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if err := ctx.Err(); err != nil {
			return ast.WalkStop, err
		}
		if !entering {
			return ast.WalkContinue, nil
		}
		if heading, ok := node.(*ast.Heading); ok {
			if section != nil && heading.Level <= section.Level {
				return ast.WalkStop, nil
			}
			if section == nil && isATXHeadingV03(source, heading) &&
				strings.TrimSpace(string(heading.Text(source))) == "Computation" {
				section = heading
			}
		}
		if fence, ok := node.(*ast.FencedCodeBlock); ok && section != nil {
			metadata, ok := fenceMetadataFor(fence)
			if !ok {
				return ast.WalkContinue, nil
			}
			candidate := Fence{Start: metadata.Start, End: metadata.End, Closed: metadata.Closed}
			if len(fences) == 0 {
				candidate.Content, err = fencedContentContext(ctx, source, fence)
				if err != nil {
					return ast.WalkStop, err
				}
				if fence.Info != nil {
					candidate.Info = strings.TrimSpace(string(fence.Info.Text(source)))
				}
			}
			fences = append(fences, candidate)
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return nil, err
	}
	return fences, ctx.Err()
}

func isATXHeadingV03(source []byte, heading *ast.Heading) bool {
	if heading.Lines().Len() == 0 {
		return false
	}
	start := heading.Lines().At(0).Start
	lineStart := fullLineStart(source, start)
	prefix := bytes.TrimRight(source[lineStart:start], " \t")
	count := 0
	for index := len(prefix) - 1; index >= 0 && prefix[index] == '#'; index-- {
		count++
	}
	return count == heading.Level
}
