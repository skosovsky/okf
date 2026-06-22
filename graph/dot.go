package graph

import (
	"io"

	"github.com/skosovsky/okf/bundle"
)

// RenderDOT writes a Graphviz DOT graph export.
func RenderDOT(w io.Writer, b *bundle.Bundle) error {
	if err := writeln(w, "digraph okf {"); err != nil {
		return err
	}
	if err := writeln(w, "  rankdir=LR; node [shape=box, fontsize=10];"); err != nil {
		return err
	}
	for _, concept := range b.Concepts() {
		for _, link := range b.LinksFrom(concept.ID) {
			style := ""
			if !link.Exists {
				style = " [style=dashed, color=red]"
			}
			if err := writef(w, "  %q -> %q%s;\n", concept.ID.String(), link.Target.String(), style); err != nil {
				return err
			}
		}
		for _, relation := range b.SemanticLinksFrom(concept.ID) {
			if relation.TargetExists {
				if err := writef(w, "  %q -> %q [label=%q];\n", relation.Source.String(), relation.Target.String(), relation.Type); err != nil {
					return err
				}
				continue
			}
			if err := writef(w, "  %q -> %q [label=%q, style=dashed, color=red];\n", relation.Source.String(), relation.Target.String(), relation.Type); err != nil {
				return err
			}
		}
	}
	return writeln(w, "}")
}
