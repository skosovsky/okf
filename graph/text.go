package graph

import (
	"io"

	"github.com/skosovsky/okf/bundle"
)

// RenderText writes a plain text graph export.
func RenderText(w io.Writer, b *bundle.Bundle) error {
	for _, concept := range b.Concepts() {
		links := b.LinksFrom(concept.ID)
		relations := b.SemanticLinksFrom(concept.ID)
		if len(links) == 0 && len(relations) == 0 {
			continue
		}
		lastHeader := ""
		if len(links) > 0 {
			lastHeader = concept.ID.String()
			if err := writeln(w, lastHeader); err != nil {
				return err
			}
		}
		for _, link := range links {
			mark := "->"
			if !link.Exists {
				mark = "-x"
			}
			if err := writef(w, "  %s %s\n", mark, link.Target); err != nil {
				return err
			}
		}
		for _, relation := range relations {
			source := relation.Source.String()
			if source != lastHeader {
				if err := writeln(w, source); err != nil {
					return err
				}
				lastHeader = source
			}
			mark := "=>"
			if !relation.TargetExists {
				mark = "=x"
			}
			if err := writef(w, "  %s %s %s\n", mark, relation.Type, relation.Target); err != nil {
				return err
			}
		}
	}
	return nil
}
