package graph

import (
	"fmt"
	"io"
	"strings"

	"github.com/skosovsky/okf/bundle"
)

// RenderMermaid writes a Mermaid graph export.
func RenderMermaid(w io.Writer, b *bundle.Bundle) error {
	nodes := mermaidNodeAllocator{
		labels: make(map[string]string),
	}

	if err := writeln(w, "graph LR"); err != nil {
		return err
	}
	for _, concept := range b.Concepts() {
		links := b.LinksFrom(concept.ID)
		relations := b.SemanticLinksFrom(concept.ID)
		if len(links) == 0 && len(relations) == 0 {
			continue
		}
		for _, link := range links {
			source := nodes.node(concept.ID.String())
			target := nodes.node(link.Target.String())
			if link.Exists {
				if err := writef(w, "  %s --> %s\n", source, target); err != nil {
					return err
				}
				continue
			}
			if err := writef(w, "  %s -.->|\"404\"| %s\n", source, target); err != nil {
				return err
			}
		}
		for _, relation := range relations {
			source := nodes.node(relation.Source.String())
			target := nodes.node(relation.Target.String())
			if relation.TargetExists {
				if err := writef(w, "  %s -->|\"%s\"| %s\n", source, mermaidLabel(relation.Type), target); err != nil {
					return err
				}
				continue
			}
			if err := writef(w, "  %s -.->|\"%s\"| %s\n", source, mermaidLabel(relation.Type+" 404"), target); err != nil {
				return err
			}
		}
	}
	return nil
}

type mermaidNodeAllocator struct {
	labels map[string]string
	order  []string
}

func (a *mermaidNodeAllocator) node(label string) string {
	id, ok := a.labels[label]
	if !ok {
		id = fmt.Sprintf("n%d", len(a.order))
		a.labels[label] = id
		a.order = append(a.order, label)
	}
	return fmt.Sprintf("%s[%q]", id, mermaidLabel(label))
}

func mermaidLabel(label string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"\"", "&quot;",
		"\n", " ",
		"\r", " ",
		"\t", " ",
		"]", "&#93;",
	)
	return replacer.Replace(label)
}
