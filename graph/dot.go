package graph

import (
	"context"
	"io"

	"github.com/skosovsky/okf/bundle"
)

// RenderDOT writes a Graphviz DOT graph export.
func RenderDOT(w io.Writer, b *bundle.Bundle) error {
	return RenderDOTContext(context.Background(), w, b)
}

// RenderDOTContext writes a Graphviz DOT graph export with cancellation.
func RenderDOTContext(ctx context.Context, w io.Writer, b *bundle.Bundle) error {
	return RenderDOTWithOptionsContext(ctx, w, b, Options{Profile: ProjectionProfileLegacyV01})
}

// RenderDOTWithOptions writes a Graphviz DOT graph export using explicit
// rendering options.
func RenderDOTWithOptions(w io.Writer, b *bundle.Bundle, options Options) error {
	return RenderDOTWithOptionsContext(context.Background(), w, b, options)
}

// RenderDOTWithOptionsContext writes a Graphviz DOT graph export using
// explicit rendering options and a cancellable request context.
func RenderDOTWithOptionsContext(ctx context.Context, w io.Writer, b *bundle.Bundle, options Options) error {
	return renderTransactionalContext(ctx, w, func(spool io.Writer) error {
		return renderDOTWithOptionsContext(ctx, spool, b, options)
	})
}

func renderDOTWithOptionsContext(ctx context.Context, w io.Writer, b *bundle.Bundle, options Options) error {
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
	if err := writelnContext(ctx, w, "digraph okf {"); err != nil {
		return err
	}
	if err := writelnContext(ctx, w, "  rankdir=LR; node [shape=box, fontsize=10];"); err != nil {
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
		if options.AnnotateTopology && (len(links) > 0 || len(relations) > 0) {
			conceptID, err := graphConceptIDContext(ctx, concept.ID)
			if err != nil {
				return err
			}
			annotation, err := topologyAnnotationContext(ctx, concept.Document, options)
			if err != nil {
				return err
			}
			if err := writefContext(ctx, w, "  // %s %s\n", conceptID, annotation); err != nil {
				return err
			}
		}
		for linkIndex, link := range links {
			if linkIndex%graphContextCheckInterval == 0 {
				if err := checkGraphContext(ctx); err != nil {
					return err
				}
			}
			style := ""
			if !link.Exists {
				style = " [style=dashed, color=red]"
			}
			source, err := graphConceptIDContext(ctx, concept.ID)
			if err != nil {
				return err
			}
			target, err := graphConceptIDContext(ctx, link.Target)
			if err != nil {
				return err
			}
			if err := writefContext(ctx, w, "  %q -> %q%s;\n", source, target, style); err != nil {
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
			target, err := graphRelationRefContext(ctx, relation.Target)
			if err != nil {
				return err
			}
			if err := writefContext(ctx, w, "  %q -> %q [label=%q];\n", source, target, relation.Type); err != nil {
				return err
			}
		}
	}
	return writelnContext(ctx, w, "}")
}
