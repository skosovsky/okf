package pathportability

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/text/cases"
)

// CaseCollision identifies a new path component and its existing spelling.
type CaseCollision struct {
	Path, Existing string
}

func (e *CaseCollision) Error() string {
	return fmt.Sprintf("path %q differs only by case from %q", e.Path, e.Existing)
}

// CheckNew rejects new case aliases in slash-separated bundle paths. An
// existing spelling may still be edited, even in a bundle with older aliases.
func CheckNew(ctx context.Context, base, proposed []string) error {
	if len(proposed) == 0 {
		return ctx.Err()
	}
	exact := make(map[string]struct{}, len(base))
	for _, name := range base {
		if err := ctx.Err(); err != nil {
			return err
		}
		exact[name] = struct{}{}
	}
	var created []string
	for _, name := range proposed {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, exists := exact[name]; !exists {
			created = append(created, name)
		}
	}
	if len(created) == 0 {
		return ctx.Err()
	}

	fold := cases.Fold()
	spellings := make(map[string]string, len(base))
	record := func(name string, existing bool) error {
		folded := fold.String(name)
		start, foldedStart := 0, 0
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			end, foldedEnd := len(name), len(folded)
			if slash := strings.IndexByte(name[start:], '/'); slash >= 0 {
				end = start + slash
				foldedEnd = foldedStart + strings.IndexByte(folded[foldedStart:], '/')
			}
			prefix, key := name[:end], folded[:foldedEnd]
			if previous, exists := spellings[key]; exists {
				if _, known := exact[prefix]; previous != prefix && !known && !existing {
					return &CaseCollision{Path: prefix, Existing: previous}
				}
			} else {
				spellings[key] = prefix
			}
			exact[prefix] = struct{}{}
			if end == len(name) {
				return nil
			}
			start, foldedStart = end+1, foldedEnd+1
		}
	}
	for _, name := range base {
		if err := record(name, true); err != nil {
			return err
		}
	}
	for _, name := range created {
		if err := record(name, false); err != nil {
			return err
		}
	}
	return ctx.Err()
}
