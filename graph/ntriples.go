package graph

import (
	"context"
	"fmt"
	"io"
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
	return RenderNTriplesContext(context.Background(), w, b)
}

// RenderNTriplesContext writes a canonical RDF N-Triples graph export with
// cancellation across projection, ordering, escaping, and output.
func RenderNTriplesContext(ctx context.Context, w io.Writer, b *bundle.Bundle) error {
	return RenderNTriplesWithOptionsContext(ctx, w, b, Options{Profile: ProjectionProfileLegacyV01})
}

// RenderNTriplesWithOptions writes an RDF N-Triples graph export using an
// explicit projection profile.
func RenderNTriplesWithOptions(w io.Writer, b *bundle.Bundle, options Options) error {
	return RenderNTriplesWithOptionsContext(context.Background(), w, b, options)
}

// RenderNTriplesWithOptionsContext writes an RDF N-Triples graph export using
// an explicit projection profile and cancellable request context.
func RenderNTriplesWithOptionsContext(ctx context.Context, w io.Writer, b *bundle.Bundle, options Options) error {
	return renderTransactionalContext(ctx, w, func(spool io.Writer) error {
		return renderNTriplesWithOptionsContext(ctx, spool, b, options)
	})
}

func renderNTriplesWithOptionsContext(ctx context.Context, w io.Writer, b *bundle.Bundle, options Options) error {
	if err := checkGraphContext(ctx); err != nil {
		return err
	}
	options, err := normalizeOptions(options)
	if err != nil {
		return err
	}
	if options.Profile == ProjectionProfileToolkitV02 {
		return renderToolkitNTriplesContext(ctx, w, b, options)
	}
	if err := assertVersionSelectorContext(ctx, b, options); err != nil {
		return err
	}
	return renderLegacyNTriplesContext(ctx, w, b, options)
}

func renderLegacyNTriples(w io.Writer, b *bundle.Bundle, options Options) error {
	return renderLegacyNTriplesContext(context.Background(), w, b, options)
}

func renderLegacyNTriplesContext(ctx context.Context, w io.Writer, b *bundle.Bundle, options Options) error {
	if err := checkGraphContext(ctx); err != nil {
		return err
	}
	var triples []string
	appendTriple := func(subject, predicate, object string) error {
		triple, err := ntriplesTripleContext(ctx, subject, predicate, object, false)
		if err != nil {
			return err
		}
		triples = append(triples, triple)
		return nil
	}
	appendLiteral := func(subject, predicate, value string) error {
		literal, err := ntriplesLiteralContext(ctx, value)
		if err != nil {
			return err
		}
		return appendTriple(subject, predicate, literal)
	}
	var emittedSubresources []bundle.RelationRef
	appendSubresource := func(ref bundle.RelationRef) error {
		if ref.Fragment == "" {
			return nil
		}
		for _, emitted := range emittedSubresources {
			equal, err := equalRelationRefsContext(ctx, emitted, ref)
			if err != nil {
				return err
			}
			if equal {
				return nil
			}
		}
		emittedSubresources = append(emittedSubresources, ref)
		subject, err := ntriplesRelationRefIRIContext(ctx, ref)
		if err != nil {
			return err
		}
		if err := appendTriple(subject, ntriplesRDFType, ntriplesIRI(ntriplesSubResource)); err != nil {
			return err
		}
		conceptIRI, err := ntriplesConceptIRIContext(ctx, ref.ID)
		if err != nil {
			return err
		}
		return appendTriple(subject, ntriplesPredicate("is_part_of"), conceptIRI)
	}
	relationOrdinal := 0
	concepts, err := graphConceptsContext(ctx, b)
	if err != nil {
		return err
	}
	for conceptIndex, concept := range concepts {
		if conceptIndex%graphContextCheckInterval == 0 {
			if err := checkGraphContext(ctx); err != nil {
				return err
			}
		}
		subject, err := ntriplesConceptIRIContext(ctx, concept.ID)
		if err != nil {
			return err
		}
		if err := appendTriple(subject, ntriplesRDFType, ntriplesIRI(ntriplesConceptClass)); err != nil {
			return err
		}

		frontmatter := concept.Document.Frontmatter
		if value, ok, err := frontmatter.TypeContext(ctx); err != nil {
			return err
		} else if ok {
			if err := appendLiteral(subject, ntriplesPredicate("type"), value); err != nil {
				return err
			}
		}
		if value, ok, err := frontmatter.TitleContext(ctx); err != nil {
			return err
		} else if ok {
			if err := appendLiteral(subject, ntriplesPredicate("title"), value); err != nil {
				return err
			}
		}
		if value, ok, err := frontmatter.DescriptionContext(ctx); err != nil {
			return err
		} else if ok {
			if err := appendLiteral(subject, ntriplesPredicate("description"), value); err != nil {
				return err
			}
		}
		if value, ok, err := frontmatter.ResourceContext(ctx); err != nil {
			return err
		} else if ok {
			if err := appendLiteral(subject, ntriplesPredicate("resource"), value); err != nil {
				return err
			}
		}
		if value, ok, err := frontmatter.TimestampContext(ctx); err != nil {
			return err
		} else if ok {
			if err := appendLiteral(subject, ntriplesPredicate("timestamp"), value); err != nil {
				return err
			}
		}
		tags, err := frontmatter.TagsContext(ctx)
		if err != nil {
			return err
		}
		for tagIndex, tag := range tags {
			if tagIndex%graphContextCheckInterval == 0 {
				if err := checkGraphContext(ctx); err != nil {
					return err
				}
			}
			literal, err := ntriplesLiteralContext(ctx, tag)
			if err != nil {
				return err
			}
			if err := appendTriple(subject, ntriplesPredicate("tags"), literal); err != nil {
				return err
			}
		}
		links, err := b.LinksFromContext(ctx, concept.ID)
		if err != nil {
			return err
		}
		for linkIndex, link := range links {
			if linkIndex%graphContextCheckInterval == 0 {
				if err := checkGraphContext(ctx); err != nil {
					return err
				}
			}
			targetIRI, err := ntriplesConceptIRIContext(ctx, link.Target)
			if err != nil {
				return err
			}
			if err := appendTriple(subject, ntriplesPredicate("references"), targetIRI); err != nil {
				return err
			}
		}
		if options.ExtensionRelations == ExtensionRelationsExclude {
			continue
		}
		relations, err := b.SemanticLinksFromContext(ctx, concept.ID)
		if err != nil {
			return err
		}
		for relationIndex, relation := range relations {
			if relationIndex%graphContextCheckInterval == 0 {
				if err := checkGraphContext(ctx); err != nil {
					return err
				}
			}
			if err := appendSubresource(relation.Source); err != nil {
				return err
			}
			if relation.TargetExists {
				if err := appendSubresource(relation.Target); err != nil {
					return err
				}
			}

			relationNode := ntriplesRelationIRI(relationOrdinal)
			relationOrdinal++
			source, err := ntriplesRelationRefIRIContext(ctx, relation.Source)
			if err != nil {
				return err
			}
			target, err := ntriplesRelationRefIRIContext(ctx, relation.Target)
			if err != nil {
				return err
			}
			predicate, err := ntriplesPredicateContext(ctx, relation.Type)
			if err != nil {
				return err
			}
			predicateIRI, err := ntriplesIRIContext(ctx, predicate)
			if err != nil {
				return err
			}

			for _, triple := range [][3]string{{source, predicate, target}, {relationNode, ntriplesRDFType, ntriplesIRI(ntriplesRelationClass)}, {relationNode, ntriplesRDFSubject, source}, {relationNode, ntriplesRDFPredicate, predicateIRI}, {relationNode, ntriplesRDFObject, target}, {relationNode, ntriplesPredicate("exists"), ntriplesBoolean(relation.TargetExists)}} {
				if err := appendTriple(triple[0], triple[1], triple[2]); err != nil {
					return err
				}
			}
		}
	}
	if err := sortGraphStringsContext(ctx, triples); err != nil {
		return err
	}
	for index, triple := range triples {
		if index%graphContextCheckInterval == 0 {
			if err := checkGraphContext(ctx); err != nil {
				return err
			}
		}
		if err := writeStringContext(ctx, w, triple); err != nil {
			return err
		}
	}
	return checkGraphContext(ctx)
}

func renderToolkitNTriples(w io.Writer, b *bundle.Bundle, options Options) error {
	return renderToolkitNTriplesContext(context.Background(), w, b, options)
}

func renderToolkitNTriplesContext(ctx context.Context, w io.Writer, b *bundle.Bundle, options Options) error {
	projection, err := buildToolkitProjectionContext(ctx, b, options)
	if err != nil {
		return err
	}
	var triples []string
	appendTriple := func(subject, predicate, object string) error {
		predicateIRI, err := concatGraphStringsContext(ctx, toolkitProjectionNamespace, predicate)
		if err != nil {
			return err
		}
		triple, err := ntriplesTripleContext(ctx, subject, predicateIRI, object, true)
		if err != nil {
			return err
		}
		triples = append(triples, triple)
		return nil
	}
	for nodeIndex, node := range projection.Nodes {
		if nodeIndex%graphContextCheckInterval == 0 {
			if err := checkGraphContext(ctx); err != nil {
				return err
			}
		}
		for typeIndex, typ := range node.Types {
			if typeIndex%graphContextCheckInterval == 0 {
				if err := checkGraphContext(ctx); err != nil {
					return err
				}
			}
			typeValue, err := concatGraphStringsContext(ctx, toolkitProjectionNamespace, typ)
			if err != nil {
				return err
			}
			typeIRI, err := ntriplesIRIContext(ctx, typeValue)
			if err != nil {
				return err
			}
			triple, err := ntriplesTripleContext(ctx, node.ID, ntriplesRDFType, typeIRI, true)
			if err != nil {
				return err
			}
			triples = append(triples, triple)
		}
		properties := make([]string, 0, len(node.Properties))
		propertyIndex := 0
		for property := range node.Properties {
			if propertyIndex%graphContextCheckInterval == 0 {
				if err := checkGraphContext(ctx); err != nil {
					return err
				}
			}
			propertyIndex++
			properties = append(properties, property)
		}
		if err := sortGraphStringsContext(ctx, properties); err != nil {
			return err
		}
		for _, property := range properties {
			for valueIndex, value := range node.Properties[property] {
				if valueIndex%graphContextCheckInterval == 0 {
					if err := checkGraphContext(ctx); err != nil {
						return err
					}
				}
				encoded, err := toolkitNTriplesValueContext(ctx, value)
				if err != nil {
					return err
				}
				if err := appendTriple(node.ID, property, encoded); err != nil {
					return err
				}
			}
		}
	}
	if err := sortGraphStringsContext(ctx, triples); err != nil {
		return err
	}
	for index, triple := range triples {
		if index%graphContextCheckInterval == 0 {
			if err := checkGraphContext(ctx); err != nil {
				return err
			}
		}
		if err := writeStringContext(ctx, w, triple); err != nil {
			return err
		}
	}
	return checkGraphContext(ctx)
}

func toolkitNTriplesValue(value projectionValue) string {
	encoded, _ := toolkitNTriplesValueContext(context.Background(), value)
	return encoded
}

func toolkitNTriplesValueContext(ctx context.Context, value projectionValue) (string, error) {
	if err := checkGraphContext(ctx); err != nil {
		return "", err
	}
	switch value.Kind {
	case projectionIRI:
		return ntriplesIRIContext(ctx, value.Value)
	case projectionBoolean:
		literal, err := ntriplesLiteralContext(ctx, value.Value)
		if err != nil {
			return "", err
		}
		return concatGraphStringsContext(ctx, literal, "^^<", ntriplesXSDBoolean, ">")
	case projectionInteger:
		literal, err := ntriplesLiteralContext(ctx, value.Value)
		if err != nil {
			return "", err
		}
		return concatGraphStringsContext(ctx, literal, "^^<http://www.w3.org/2001/XMLSchema#integer>")
	case projectionDate:
		literal, err := ntriplesLiteralContext(ctx, value.Value)
		if err != nil {
			return "", err
		}
		return concatGraphStringsContext(ctx, literal, "^^<http://www.w3.org/2001/XMLSchema#date>")
	case projectionDateTime:
		literal, err := ntriplesLiteralContext(ctx, value.Value)
		if err != nil {
			return "", err
		}
		return concatGraphStringsContext(ctx, literal, "^^<http://www.w3.org/2001/XMLSchema#dateTime>")
	default:
		return ntriplesLiteralContext(ctx, value.Value)
	}
}

func ntriplesConceptIRI(id bundle.ConceptID) string {
	value, _ := ntriplesConceptIRIContext(context.Background(), id)
	return value
}

func ntriplesConceptIRIContext(ctx context.Context, id bundle.ConceptID) (string, error) {
	identity, err := graphConceptIDContext(ctx, id)
	if err != nil {
		return "", err
	}
	escaped, err := graphPathEscapeContext(ctx, identity)
	if err != nil {
		return "", err
	}
	value, err := concatGraphStringsContext(ctx, ntriplesBundlePrefix, escaped)
	if err != nil {
		return "", err
	}
	return ntriplesIRIContext(ctx, value)
}

func ntriplesRelationRefIRI(ref bundle.RelationRef) string {
	value, _ := ntriplesRelationRefIRIContext(context.Background(), ref)
	return value
}

func ntriplesRelationRefIRIContext(ctx context.Context, ref bundle.RelationRef) (string, error) {
	identity, err := graphConceptIDContext(ctx, ref.ID)
	if err != nil {
		return "", err
	}
	escapedID, err := graphPathEscapeContext(ctx, identity)
	if err != nil {
		return "", err
	}
	value, err := concatGraphStringsContext(ctx, ntriplesBundlePrefix, escapedID)
	if err != nil {
		return "", err
	}
	if ref.Fragment != "" {
		escapedFragment, err := graphPathEscapeContext(ctx, ref.Fragment)
		if err != nil {
			return "", err
		}
		value, err = concatGraphStringsContext(ctx, value, "#", escapedFragment)
		if err != nil {
			return "", err
		}
	}
	return ntriplesIRIContext(ctx, value)
}

func ntriplesRelationIRI(ordinal int) string {
	return ntriplesIRI(fmt.Sprintf("%srelation:%06d", ntriplesBundlePrefix, ordinal))
}

func ntriplesTripleContext(ctx context.Context, subject, predicate, object string, wrapSubject bool) (string, error) {
	var out strings.Builder
	parts := []string{subject, " <", predicate, "> ", object, " .\n"}
	if wrapSubject {
		parts = []string{"<", subject, "> <", predicate, "> ", object, " .\n"}
	}
	for _, part := range parts {
		for offset := 0; offset < len(part); offset += graphContextCheckInterval {
			if err := checkGraphContext(ctx); err != nil {
				return "", err
			}
			end := min(offset+graphContextCheckInterval, len(part))
			out.WriteString(part[offset:end])
		}
	}
	if err := checkGraphContext(ctx); err != nil {
		return "", err
	}
	return out.String(), nil
}

func ntriplesPredicate(name string) string {
	return ntriplesOntologyPrefix + name
}

func ntriplesPredicateContext(ctx context.Context, name string) (string, error) {
	return concatGraphStringsContext(ctx, ntriplesOntologyPrefix, name)
}

func ntriplesIRI(value string) string {
	return "<" + value + ">"
}

func ntriplesIRIContext(ctx context.Context, value string) (string, error) {
	return concatGraphStringsContext(ctx, "<", value, ">")
}

func concatGraphStringsContext(ctx context.Context, values ...string) (string, error) {
	if err := checkGraphContext(ctx); err != nil {
		return "", err
	}
	var out strings.Builder
	for _, value := range values {
		if err := appendGraphStringContext(ctx, &out, value); err != nil {
			return "", err
		}
	}
	if err := checkGraphContext(ctx); err != nil {
		return "", err
	}
	return out.String(), nil
}

func appendGraphStringContext(ctx context.Context, out *strings.Builder, value string) error {
	for offset := 0; offset < len(value); offset += graphContextCheckInterval {
		if err := checkGraphContext(ctx); err != nil {
			return err
		}
		end := min(offset+graphContextCheckInterval, len(value))
		out.WriteString(value[offset:end])
	}
	return checkGraphContext(ctx)
}

func ntriplesLiteral(value string) string {
	literal, _ := ntriplesLiteralContext(context.Background(), value)
	return literal
}

func ntriplesLiteralContext(ctx context.Context, value string) (string, error) {
	var builder strings.Builder
	builder.Grow(len(value) + 2)
	builder.WriteByte('"')
	bytesSinceCheck := 0
	for _, r := range value {
		if bytesSinceCheck >= graphContextCheckInterval {
			if err := checkGraphContext(ctx); err != nil {
				return "", err
			}
			bytesSinceCheck = 0
		}
		bytesSinceCheck += len(string(r))
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
	if err := checkGraphContext(ctx); err != nil {
		return "", err
	}
	return builder.String(), nil
}

func ntriplesBoolean(value bool) string {
	return fmt.Sprintf("\"%t\"^^<%s>", value, ntriplesXSDBoolean)
}
