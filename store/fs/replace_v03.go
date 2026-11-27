package fs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/internal/pathportability"
	"github.com/skosovsky/okf/mutation"
	"github.com/skosovsky/okf/store"
)

func validateV03NewPaths(ctx context.Context, base store.Snapshot, paths []string) error {
	existing, err := base.Paths(ctx)
	if err != nil {
		return err
	}
	err = pathportability.CheckNew(ctx, existing, paths)
	var collision *pathportability.CaseCollision
	if errors.As(err, &collision) {
		return invalidV03Replacement(collision.Path, "path_case_collision", collision.Error())
	}
	return err
}

// A semantic transaction can update links in existing companion Markdown.
// A new non-reserved Markdown path must be a typed concept. Recovery uses the
// original path inventory so the rule is independent of partial publication.
func introducedCompanionMarkdown(ctx context.Context, staged *bundle.Bundle, base []journalBaseFile) (string, error) {
	for _, path := range staged.CompanionMarkdown() {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		index := sort.Search(len(base), func(i int) bool { return base[i].Path >= path })
		if index == len(base) || base[index].Path != path {
			return path, nil
		}
	}
	return "", ctx.Err()
}

func validateV03Replacement(ctx context.Context, base store.Snapshot, target, serialized string) error {
	version := bundle.VersionResolution{Effective: bundle.OKFVersion03}
	document, err := bundle.ParseDocumentForVersionContext(ctx, serialized, version)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return invalidV03Replacement(target, "concept_unparseable", err.Error())
	}
	_, hasType, err := document.Frontmatter.TypeContext(ctx)
	if err != nil {
		return err
	}
	if !hasType {
		return invalidV03Replacement(target, "concept_type_required", "A concept write requires a non-empty type.")
	}
	content, err := base.ReadFile(ctx, target)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	previous, err := bundle.ParseDocumentForVersionContext(ctx, string(content), version)
	if err != nil {
		return err
	}
	lost, err := mutation.LostExtensionsV03(ctx, previous.Frontmatter, document.Frontmatter)
	if err != nil {
		return err
	}
	if len(lost) > 0 {
		return invalidV03Replacement(target, "extension_not_preserved",
			fmt.Sprintf("Replacement changes or omits extension %q. Keep its value or use a lossless patch.", lost[0]))
	}
	return nil
}

func invalidV03Replacement(target, code, message string) error {
	return &store.InvalidChangeSet{Code: code, Diagnostics: []store.Diagnostic{{
		Code: code, File: target, Severity: store.DiagnosticError, Message: message,
	}}, Cause: errors.New(message)}
}
