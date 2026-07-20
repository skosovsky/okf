package graph

import (
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"

	"github.com/skosovsky/okf/bundle"
)

const (
	ntriplesOntologyPrefix = "https://okf.io/ontology/v0.1#"
	ntriplesBundlePrefix   = "local:bundle:"
	ntriplesRDFType        = "http://www.w3.org/1999/02/22-rdf-syntax-ns#type"
	ntriplesRDFSubject     = "http://www.w3.org/1999/02/22-rdf-syntax-ns#subject"
	ntriplesRDFPredicate   = "http://www.w3.org/1999/02/22-rdf-syntax-ns#predicate"
	ntriplesRDFObject      = "http://www.w3.org/1999/02/22-rdf-syntax-ns#object"
	ntriplesXSDBoolean     = "http://www.w3.org/2001/XMLSchema#boolean"
	ntriplesConceptClass   = ntriplesOntologyPrefix + "Concept"
	ntriplesSubResource    = ntriplesOntologyPrefix + "SubResource"
	ntriplesRelationClass  = ntriplesOntologyPrefix + "Relation"
)

// RenderNTriples writes a canonical RDF N-Triples graph export.
//
// Each resolved semantic relation is emitted both as its asserted predicate
// triple and as an RDF reification resource. Relation resources use their
// canonical output ordinal, which also preserves duplicate YAML relation
// entries.
func RenderNTriples(w io.Writer, b *bundle.Bundle) error {
	var triples []string
	appendTriple := func(subject, predicate, object string) {
		triples = append(triples, fmt.Sprintf("%s <%s> %s .\n", subject, predicate, object))
	}
	emittedSubresources := make(map[string]struct{})
	appendSubresource := func(ref bundle.RelationRef) {
		if ref.Fragment == "" {
			return
		}
		key := ref.String()
		if _, ok := emittedSubresources[key]; ok {
			return
		}
		emittedSubresources[key] = struct{}{}
		subject := ntriplesRelationRefIRI(ref)
		appendTriple(subject, ntriplesRDFType, ntriplesIRI(ntriplesSubResource))
		appendTriple(subject, ntriplesPredicate("is_part_of"), ntriplesConceptIRI(ref.ID))
	}
	relationOrdinal := 0
	for _, concept := range b.Concepts() {
		subject := ntriplesConceptIRI(concept.ID)
		appendTriple(subject, ntriplesRDFType, ntriplesIRI(ntriplesConceptClass))

		frontmatter := concept.Document.Frontmatter
		if value, ok := frontmatter.Type(); ok {
			appendTriple(subject, ntriplesPredicate("type"), ntriplesLiteral(value))
		}
		if value, ok := frontmatter.Title(); ok {
			appendTriple(subject, ntriplesPredicate("title"), ntriplesLiteral(value))
		}
		if value, ok := frontmatter.Description(); ok {
			appendTriple(subject, ntriplesPredicate("description"), ntriplesLiteral(value))
		}
		if value, ok := frontmatter.Resource(); ok {
			appendTriple(subject, ntriplesPredicate("resource"), ntriplesLiteral(value))
		}
		if value, ok := frontmatter.Timestamp(); ok {
			appendTriple(subject, ntriplesPredicate("timestamp"), ntriplesLiteral(value))
		}
		for _, tag := range frontmatter.Tags() {
			appendTriple(subject, ntriplesPredicate("tags"), ntriplesLiteral(tag))
		}
		for _, link := range b.LinksFrom(concept.ID) {
			appendTriple(subject, ntriplesPredicate("references"), ntriplesConceptIRI(link.Target))
		}
		for _, relation := range b.SemanticLinksFrom(concept.ID) {
			appendSubresource(relation.Source)
			if relation.TargetExists {
				appendSubresource(relation.Target)
			}

			relationNode := ntriplesRelationIRI(relationOrdinal)
			relationOrdinal++
			source := ntriplesRelationRefIRI(relation.Source)
			target := ntriplesRelationRefIRI(relation.Target)
			predicate := ntriplesPredicate(relation.Type)

			appendTriple(source, predicate, target)
			appendTriple(relationNode, ntriplesRDFType, ntriplesIRI(ntriplesRelationClass))
			appendTriple(relationNode, ntriplesRDFSubject, source)
			appendTriple(relationNode, ntriplesRDFPredicate, ntriplesIRI(predicate))
			appendTriple(relationNode, ntriplesRDFObject, target)
			appendTriple(relationNode, ntriplesPredicate("exists"), ntriplesBoolean(relation.TargetExists))
		}
	}
	sort.Strings(triples)
	for _, triple := range triples {
		if err := writeString(w, triple); err != nil {
			return err
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

func ntriplesRelationIRI(ordinal int) string {
	return ntriplesIRI(fmt.Sprintf("%srelation:%06d", ntriplesBundlePrefix, ordinal))
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

func ntriplesBoolean(value bool) string {
	return fmt.Sprintf("\"%t\"^^<%s>", value, ntriplesXSDBoolean)
}
