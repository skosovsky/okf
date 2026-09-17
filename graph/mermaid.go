package graph

import (
	"context"
	"io"
	"strconv"
	"strings"

	"github.com/skosovsky/okf/bundle"
)

// RenderMermaid writes a Mermaid graph export.
func RenderMermaid(w io.Writer, b *bundle.Bundle) error {
	return RenderMermaidContext(context.Background(), w, b)
}

// RenderMermaidContext writes a Mermaid graph export with cancellation.
func RenderMermaidContext(ctx context.Context, w io.Writer, b *bundle.Bundle) error {
	return RenderMermaidWithOptionsContext(ctx, w, b, Options{Profile: ProjectionProfileLegacyV01})
}

// RenderMermaidWithOptions writes a Mermaid graph export using explicit
// rendering options.
func RenderMermaidWithOptions(w io.Writer, b *bundle.Bundle, options Options) error {
	return RenderMermaidWithOptionsContext(context.Background(), w, b, options)
}

// RenderMermaidWithOptionsContext writes a Mermaid graph export using
// explicit rendering options and a cancellable request context.
func RenderMermaidWithOptionsContext(ctx context.Context, w io.Writer, b *bundle.Bundle, options Options) error {
	return renderTransactionalContext(ctx, w, func(spool io.Writer) error {
		return renderMermaidWithOptionsContext(ctx, spool, b, options)
	})
}

func renderMermaidWithOptionsContext(ctx context.Context, w io.Writer, b *bundle.Bundle, options Options) error {
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
	nodes := mermaidNodeAllocator{}

	if err := writelnContext(ctx, w, "graph LR"); err != nil {
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
		if options.AnnotateTopology {
			annotation, err := topologyAnnotationContext(ctx, concept.Document, options)
			if err != nil {
				return err
			}
			conceptID, err := graphConceptIDContext(ctx, concept.ID)
			if err != nil {
				return err
			}
			if err := writefContext(ctx, w, "  %%%% %s %s\n", conceptID, annotation); err != nil {
				return err
			}
		}
		for linkIndex, link := range links {
			if linkIndex%graphContextCheckInterval == 0 {
				if err := checkGraphContext(ctx); err != nil {
					return err
				}
			}
			sourceLabel, err := graphConceptIDContext(ctx, concept.ID)
			if err != nil {
				return err
			}
			targetLabel, err := graphConceptIDContext(ctx, link.Target)
			if err != nil {
				return err
			}
			source, err := nodes.nodeContext(ctx, sourceLabel)
			if err != nil {
				return err
			}
			target, err := nodes.nodeContext(ctx, targetLabel)
			if err != nil {
				return err
			}
			if link.Exists {
				if err := writefContext(ctx, w, "  %s --> %s\n", source, target); err != nil {
					return err
				}
				continue
			}
			if err := writefContext(ctx, w, "  %s -.->|\"404\"| %s\n", source, target); err != nil {
				return err
			}
		}
		for relationIndex, relation := range relations {
			if relationIndex%graphContextCheckInterval == 0 {
				if err := checkGraphContext(ctx); err != nil {
					return err
				}
			}
			sourceLabel, err := graphRelationRefContext(ctx, relation.Source)
			if err != nil {
				return err
			}
			targetLabel, err := graphRelationRefContext(ctx, relation.Target)
			if err != nil {
				return err
			}
			source, err := nodes.nodeContext(ctx, sourceLabel)
			if err != nil {
				return err
			}
			target, err := nodes.nodeContext(ctx, targetLabel)
			if err != nil {
				return err
			}
			label, err := mermaidLabelContext(ctx, relation.Type)
			if err != nil {
				return err
			}
			if err := writefContext(ctx, w, "  %s -->|\"%s\"| %s\n", source, label, target); err != nil {
				return err
			}
		}
	}
	return checkGraphContext(ctx)
}

type mermaidNodeAllocator struct {
	labels []string
}

func (a *mermaidNodeAllocator) nodeContext(ctx context.Context, label string) (string, error) {
	index := -1
	for candidate, existing := range a.labels {
		result, err := compareGraphStringsContext(ctx, existing, label)
		if err != nil {
			return "", err
		}
		if result == 0 {
			index = candidate
			break
		}
	}
	if index < 0 {
		index = len(a.labels)
		a.labels = append(a.labels, label)
	}
	escaped, err := mermaidLabelContext(ctx, label)
	if err != nil {
		return "", err
	}
	var node strings.Builder
	if err := writefContext(ctx, &node, "n%s[%q]", strconv.Itoa(index), escaped); err != nil {
		return "", err
	}
	if err := checkGraphContext(ctx); err != nil {
		return "", err
	}
	return node.String(), nil
}

func mermaidLabel(label string) string {
	value, _ := mermaidLabelContext(context.Background(), label)
	return value
}

func mermaidLabelContext(ctx context.Context, label string) (string, error) {
	if err := checkGraphContext(ctx); err != nil {
		return "", err
	}
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"\"", "&quot;",
		"\n", " ",
		"\r", " ",
		"\t", " ",
		"]", "&#93;",
	)
	var out strings.Builder
	for offset := 0; offset < len(label); offset += graphContextCheckInterval {
		if err := checkGraphContext(ctx); err != nil {
			return "", err
		}
		end := min(offset+graphContextCheckInterval, len(label))
		out.WriteString(replacer.Replace(label[offset:end]))
	}
	if err := checkGraphContext(ctx); err != nil {
		return "", err
	}
	return out.String(), nil
}
