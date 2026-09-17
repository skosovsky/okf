package graph

import (
	"context"
	"io"

	"github.com/skosovsky/okf/bundle"
)

// RenderText writes a plain text graph export.
func RenderText(w io.Writer, b *bundle.Bundle) error {
	return RenderTextContext(context.Background(), w, b)
}

// RenderTextContext writes a plain text graph export and observes ctx during
// projection, traversal, and output.
func RenderTextContext(ctx context.Context, w io.Writer, b *bundle.Bundle) error {
	return RenderTextWithOptionsContext(ctx, w, b, Options{Profile: ProjectionProfileLegacyV01})
}

// RenderTextWithOptions writes a plain text graph export using explicit
// rendering options.
func RenderTextWithOptions(w io.Writer, b *bundle.Bundle, options Options) error {
	return RenderTextWithOptionsContext(context.Background(), w, b, options)
}

// RenderTextWithOptionsContext writes a plain text graph export using
// explicit rendering options and a cancellable request context.
func RenderTextWithOptionsContext(ctx context.Context, w io.Writer, b *bundle.Bundle, options Options) error {
	return renderTransactionalContext(ctx, w, func(spool io.Writer) error {
		return renderTextWithOptionsContext(ctx, spool, b, options)
	})
}

func renderTextWithOptionsContext(ctx context.Context, w io.Writer, b *bundle.Bundle, options Options) error {
	if err := checkGraphContext(ctx); err != nil {
		return err
	}
	options, err := normalizeOptions(options)
	if err != nil {
		return err
	}
	if err := assertVersionSelectorContext(ctx, b, options); err != nil {
		return err
	}
	concepts, err := graphConceptsContext(ctx, b)
	if err != nil {
		return err
	}
	for index, concept := range concepts {
		if index%graphContextCheckInterval == 0 {
			if err := checkGraphContext(ctx); err != nil {
				return err
			}
		}
		links, err := b.LinksFromContext(ctx, concept.ID)
		if err != nil {
			return err
		}
		var relations []bundle.Relation
		if options.ExtensionRelations == ExtensionRelationsInclude {
			relations, err = b.SemanticLinksFromContext(ctx, concept.ID)
			if err != nil {
				return err
			}
		}
		if len(links) == 0 && len(relations) == 0 {
			continue
		}
		conceptID, err := graphConceptIDContext(ctx, concept.ID)
		if err != nil {
			return err
		}
		if options.AnnotateTopology {
			annotation, err := topologyAnnotationContext(ctx, concept.Document, options)
			if err != nil {
				return err
			}
			if err := writefContext(ctx, w, "@ %s %s\n", conceptID, annotation); err != nil {
				return err
			}
		}
		lastHeader := ""
		if len(links) > 0 {
			lastHeader = conceptID
			if err := writelnContext(ctx, w, lastHeader); err != nil {
				return err
			}
		}
		for linkIndex, link := range links {
			if linkIndex%graphContextCheckInterval == 0 {
				if err := checkGraphContext(ctx); err != nil {
					return err
				}
			}
			mark := "->"
			if !link.Exists {
				mark = "-x"
			}
			target, err := graphConceptIDContext(ctx, link.Target)
			if err != nil {
				return err
			}
			if err := writefContext(ctx, w, "  %s %s\n", mark, target); err != nil {
				return err
			}
		}
		for relationIndex, relation := range relations {
			if relationIndex%graphContextCheckInterval == 0 {
				if err := checkGraphContext(ctx); err != nil {
					return err
				}
			}
			source, err := graphRelationRefContext(ctx, relation.Source)
			if err != nil {
				return err
			}
			if source != lastHeader {
				if err := writelnContext(ctx, w, source); err != nil {
					return err
				}
				lastHeader = source
			}
			target, err := graphRelationRefContext(ctx, relation.Target)
			if err != nil {
				return err
			}
			if err := writefContext(ctx, w, "  => %s %s\n", relation.Type, target); err != nil {
				return err
			}
		}
	}
	return checkGraphContext(ctx)
}
