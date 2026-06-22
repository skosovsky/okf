package graph

import (
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/skosovsky/okf/bundle"
)

const (
	ntriplesOntologyPrefix = "https://okf.io/ontology/v0.1#"
	ntriplesBundlePrefix   = "local:bundle:"
	ntriplesRDFType        = "http://www.w3.org/1999/02/22-rdf-syntax-ns#type"
	ntriplesConceptClass   = ntriplesOntologyPrefix + "Concept"
	ntriplesSubResource    = ntriplesOntologyPrefix + "SubResource"
)

// RenderNTriples writes an RDF N-Triples graph export.
func RenderNTriples(w io.Writer, b *bundle.Bundle) error {
	emittedSubresources := make(map[string]struct{})
	for _, concept := range b.Concepts() {
		subject := ntriplesConceptIRI(concept.ID)
		if err := writeNTriple(w, subject, ntriplesRDFType, ntriplesIRI(ntriplesConceptClass)); err != nil {
			return err
		}

		frontmatter := concept.Document.Frontmatter
		if value, ok := frontmatter.Type(); ok {
			if err := writeNTriple(w, subject, ntriplesPredicate("type"), ntriplesLiteral(value)); err != nil {
				return err
			}
		}
		if value, ok := frontmatter.Title(); ok {
			if err := writeNTriple(w, subject, ntriplesPredicate("title"), ntriplesLiteral(value)); err != nil {
				return err
			}
		}
		if value, ok := frontmatter.Description(); ok {
			if err := writeNTriple(w, subject, ntriplesPredicate("description"), ntriplesLiteral(value)); err != nil {
				return err
			}
		}
		if value, ok := frontmatter.Resource(); ok {
			if err := writeNTriple(w, subject, ntriplesPredicate("resource"), ntriplesLiteral(value)); err != nil {
				return err
			}
		}
		if value, ok := frontmatter.Timestamp(); ok {
			if err := writeNTriple(w, subject, ntriplesPredicate("timestamp"), ntriplesLiteral(value)); err != nil {
				return err
			}
		}
		for _, tag := range frontmatter.Tags() {
			if err := writeNTriple(w, subject, ntriplesPredicate("tags"), ntriplesLiteral(tag)); err != nil {
				return err
			}
		}
		for _, link := range b.LinksFrom(concept.ID) {
			if err := writeNTriple(w, subject, ntriplesPredicate("references"), ntriplesConceptIRI(link.Target)); err != nil {
				return err
			}
		}
		for _, relation := range b.SemanticLinksFrom(concept.ID) {
			if relation.Source.Fragment != "" {
				sourceKey := relation.Source.String()
				if _, ok := emittedSubresources[sourceKey]; !ok {
					emittedSubresources[sourceKey] = struct{}{}
					source := ntriplesRelationRefIRI(relation.Source)
					if err := writeNTriple(w, source, ntriplesRDFType, ntriplesIRI(ntriplesSubResource)); err != nil {
						return err
					}
					if err := writeNTriple(w, source, ntriplesPredicate("is_part_of"), ntriplesConceptIRI(relation.Source.ID)); err != nil {
						return err
					}
				}
			}
			if err := writeNTriple(w, ntriplesRelationRefIRI(relation.Source), ntriplesPredicate(relation.Type), ntriplesRelationRefIRI(relation.Target)); err != nil {
				return err
			}
		}
	}
	return nil
}

func ntriplesConceptIRI(id bundle.ConceptID) string {
	return ntriplesIRI(ntriplesBundlePrefix + url.PathEscape(id.String()))
}

func ntriplesRelationRefIRI(ref bundle.RelationRef) string {
	value := ntriplesBundlePrefix + url.PathEscape(ref.ID.String())
	if ref.Fragment != "" {
		value += "#" + url.PathEscape(ref.Fragment)
	}
	return ntriplesIRI(value)
}

func ntriplesPredicate(name string) string {
	return ntriplesOntologyPrefix + name
}

func ntriplesIRI(value string) string {
	return "<" + value + ">"
}

func ntriplesLiteral(value string) string {
	var builder strings.Builder
	builder.Grow(len(value) + 2)
	builder.WriteByte('"')
	for _, r := range value {
		switch r {
		case '\\':
			builder.WriteString(`\\`)
		case '"':
			builder.WriteString(`\"`)
		case '\t':
			builder.WriteString(`\t`)
		case '\n':
			builder.WriteString(`\n`)
		case '\r':
			builder.WriteString(`\r`)
		case '\f':
			builder.WriteString(`\f`)
		case '\b':
			builder.WriteString(`\b`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&builder, `\u%04X`, r)
				continue
			}
			builder.WriteRune(r)
		}
	}
	builder.WriteByte('"')
	return builder.String()
}

func writeNTriple(w io.Writer, subject, predicate, object string) error {
	return writef(w, "%s <%s> %s .\n", subject, predicate, object)
}
