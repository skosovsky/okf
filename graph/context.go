package graph

import (
	"context"
	"io"
	"net/url"
	"strings"

	"github.com/skosovsky/okf/bundle"
)

const graphContextCheckInterval = 256
const graphWriteChunkSize = 4096

func checkGraphContext(ctx context.Context) error {
	if ctx == nil {
		return context.Canceled
	}
	return ctx.Err()
}

// stableSortContext is a cancellable stable merge sort. The comparator may
// itself observe cancellation while comparing long values.
func stableSortContext[T any](
	ctx context.Context,
	values []T,
	less func(context.Context, T, T) (bool, error),
) error {
	if err := checkGraphContext(ctx); err != nil {
		return err
	}
	if len(values) < 2 {
		return nil
	}
	buffer := make([]T, len(values))
	for width := 1; width < len(values); width *= 2 {
		if err := checkGraphContext(ctx); err != nil {
			return err
		}
		for left := 0; left < len(values); left += 2 * width {
			middle := min(left+width, len(values))
			right := min(left+2*width, len(values))
			i, j, out := left, middle, left
			for i < middle && j < right {
				if (out-left)%graphContextCheckInterval == 0 {
					if err := checkGraphContext(ctx); err != nil {
						return err
					}
				}
				// Choose the left value when equal to preserve stability.
				rightLess, err := less(ctx, values[j], values[i])
				if err != nil {
					return err
				}
				if rightLess {
					buffer[out] = values[j]
					j++
				} else {
					buffer[out] = values[i]
					i++
				}
				out++
			}
			out += copy(buffer[out:right], values[i:middle])
			copy(buffer[out:right], values[j:right])
		}
		copy(values, buffer)
		if width > len(values)/2 {
			break
		}
	}
	return checkGraphContext(ctx)
}

func compareGraphStringsContext(ctx context.Context, left, right string) (int, error) {
	limit := min(len(left), len(right))
	for offset := 0; offset < limit; offset += graphContextCheckInterval {
		if err := checkGraphContext(ctx); err != nil {
			return 0, err
		}
		end := min(offset+graphContextCheckInterval, limit)
		leftChunk, rightChunk := left[offset:end], right[offset:end]
		if leftChunk < rightChunk {
			return -1, nil
		}
		if leftChunk > rightChunk {
			return 1, nil
		}
	}
	if len(left) < len(right) {
		return -1, nil
	}
	if len(left) > len(right) {
		return 1, nil
	}
	return 0, checkGraphContext(ctx)
}

func equalGraphStringsContext(ctx context.Context, left, right string) (bool, error) {
	comparison, err := compareGraphStringsContext(ctx, left, right)
	return comparison == 0, err
}

func sortGraphStringsContext(ctx context.Context, values []string) error {
	return stableSortContext(ctx, values, func(ctx context.Context, left, right string) (bool, error) {
		comparison, err := compareGraphStringsContext(ctx, left, right)
		return comparison < 0, err
	})
}

func graphConceptIDContext(ctx context.Context, id bundle.ConceptID) (string, error) {
	return id.StringContext(ctx)
}

func graphRelationRefContext(ctx context.Context, ref bundle.RelationRef) (string, error) {
	return ref.StringContext(ctx)
}

func equalRelationRefsContext(ctx context.Context, left, right bundle.RelationRef) (bool, error) {
	comparison, err := compareRelationRefsContext(ctx, left, right)
	return comparison == 0, err
}

// graphPathEscapeContext is byte-for-byte compatible with url.PathEscape.
// Escaping one byte at a time preserves its byte-oriented encoding while
// providing cancellation checkpoints for attacker-sized identities.
func graphPathEscapeContext(ctx context.Context, value string) (string, error) {
	if err := checkGraphContext(ctx); err != nil {
		return "", err
	}
	var builder strings.Builder
	for index := 0; index < len(value); index++ {
		if index%graphContextCheckInterval == 0 {
			if err := checkGraphContext(ctx); err != nil {
				return "", err
			}
		}
		builder.WriteString(url.PathEscape(value[index : index+1]))
	}
	return builder.String(), checkGraphContext(ctx)
}

func graphConceptsContext(ctx context.Context, b *bundle.Bundle) ([]bundle.Concept, error) {
	ids, err := b.ConceptIDsContext(ctx)
	if err != nil {
		return nil, err
	}
	concepts := make([]bundle.Concept, 0, len(ids))
	for index, id := range ids {
		if index%graphContextCheckInterval == 0 {
			if err := checkGraphContext(ctx); err != nil {
				return nil, err
			}
		}
		concept, ok, err := b.GetContext(ctx, id)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		concepts = append(concepts, concept)
	}
	if err := checkGraphContext(ctx); err != nil {
		return nil, err
	}
	return concepts, nil
}

// writeStringContext gives writer errors precedence over cancellation observed
// after the write. This keeps the first externally observable failure stable.
func writeStringContext(ctx context.Context, w io.Writer, text string) error {
	if len(text) == 0 {
		return checkGraphContext(ctx)
	}
	for offset := 0; offset < len(text); {
		if err := checkGraphContext(ctx); err != nil {
			return err
		}
		end := min(offset+graphWriteChunkSize, len(text))
		written, err := io.WriteString(w, text[offset:end])
		if err != nil {
			return err
		}
		if written != end-offset {
			return io.ErrShortWrite
		}
		offset = end
		if err := checkGraphContext(ctx); err != nil {
			return err
		}
	}
	return nil
}
