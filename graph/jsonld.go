package graph

import (
	"context"
	"io"

	"github.com/skosovsky/okf/bundle"
)

type jsonldDocument struct {
	Context map[string]any   `json:"@context"`
	Graph   []map[string]any `json:"@graph"`
}

type jsonldReference struct {
	Kind   string `json:"@type"`
	Target string `json:"target"`
	Exists bool   `json:"exists"`
}

type toolkitJSONLDDocument struct {
	Context map[string]any   `json:"@context"`
	Graph   []map[string]any `json:"@graph"`
}

// RenderJSONLD writes a JSON-LD graph export.
func RenderJSONLD(w io.Writer, b *bundle.Bundle) error {
	return RenderJSONLDContext(context.Background(), w, b)
}

// RenderJSONLDContext writes a JSON-LD graph export with cancellation across
// projection, deterministic ordering, string escaping, and streamed output.
func RenderJSONLDContext(ctx context.Context, w io.Writer, b *bundle.Bundle) error {
	return RenderJSONLDWithOptionsContext(ctx, w, b, Options{Profile: ProjectionProfileLegacyV01})
}

// RenderJSONLDWithOptions writes a JSON-LD graph export using an explicit
// projection profile.
func RenderJSONLDWithOptions(w io.Writer, b *bundle.Bundle, options Options) error {
	return RenderJSONLDWithOptionsContext(context.Background(), w, b, options)
}

// RenderJSONLDWithOptionsContext writes a JSON-LD graph export using an
// explicit projection profile and cancellable request context.
func RenderJSONLDWithOptionsContext(ctx context.Context, w io.Writer, b *bundle.Bundle, options Options) error {
	return renderTransactionalContext(ctx, w, func(spool io.Writer) error {
		return renderJSONLDWithOptionsContext(ctx, spool, b, options)
	})
}

func renderJSONLDWithOptionsContext(ctx context.Context, w io.Writer, b *bundle.Bundle, options Options) error {
	if err := checkGraphContext(ctx); err != nil {
		return err
	}
	options, err := normalizeOptions(options)
	if err != nil {
		return err
	}
	if options.Profile == ProjectionProfileToolkitV02 {
		return renderToolkitJSONLDContext(ctx, w, b, options)
	}
	if err := assertVersionSelectorContext(ctx, b, options); err != nil {
		return err
	}
	return renderLegacyJSONLDContext(ctx, w, b, options)
}

func renderLegacyJSONLD(w io.Writer, b *bundle.Bundle, options Options) error {
	return renderLegacyJSONLDContext(context.Background(), w, b, options)
}

func renderLegacyJSONLDContext(ctx context.Context, w io.Writer, b *bundle.Bundle, options Options) error {
	if err := checkGraphContext(ctx); err != nil {
		return err
	}
	concepts, err := graphConceptsContext(ctx, b)
	if err != nil {
		return err
	}
	contextObject := orderedJSONObject{}
	for key, value := range jsonldContext() {
		if err := contextObject.setContext(ctx, key, value); err != nil {
			return err
		}
	}
	graphObjects := make([]orderedJSONObject, 0, len(concepts))

	for conceptIndex, concept := range concepts {
		if conceptIndex%graphContextCheckInterval == 0 {
			if err := checkGraphContext(ctx); err != nil {
				return err
			}
		}
		conceptID, err := graphConceptIDContext(ctx, concept.ID)
		if err != nil {
			return err
		}
		conceptIRI, err := concatGraphStringsContext(ctx, "bundle:", conceptID)
		if err != nil {
			return err
		}
		node := orderedJSONObject{{key: "@id", value: conceptIRI}, {key: "@type", value: "okf:Concept"}}

		frontmatter := concept.Document.Frontmatter
		if value, ok, err := frontmatter.TypeContext(ctx); err != nil {
			return err
		} else if ok {
			if err := node.setContext(ctx, "type", value); err != nil {
				return err
			}
		}
		if value, ok, err := frontmatter.TitleContext(ctx); err != nil {
			return err
		} else if ok {
			if err := node.setContext(ctx, "title", value); err != nil {
				return err
			}
		}
		if value, ok, err := frontmatter.DescriptionContext(ctx); err != nil {
			return err
		} else if ok {
			if err := node.setContext(ctx, "description", value); err != nil {
				return err
			}
		}
		if value, ok, err := frontmatter.ResourceContext(ctx); err != nil {
			return err
		} else if ok {
			if err := node.setContext(ctx, "resource", value); err != nil {
				return err
			}
		}
		if value, ok, err := frontmatter.TimestampContext(ctx); err != nil {
			return err
		} else if ok {
			if err := node.setContext(ctx, "timestamp", value); err != nil {
				return err
			}
		}
		if tags, err := frontmatter.TagsContext(ctx); err != nil {
			return err
		} else if len(tags) > 0 {
			if err := node.setContext(ctx, "tags", tags); err != nil {
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
			target, err := graphConceptIDContext(ctx, link.Target)
			if err != nil {
				return err
			}
			targetIRI, err := concatGraphStringsContext(ctx, "bundle:", target)
			if err != nil {
				return err
			}
			current, _, getErr := node.getContext(ctx, "references")
			if getErr != nil {
				return getErr
			}
			references, _ := current.([]jsonldReference)
			if err := node.setContext(ctx, "references", append(references, jsonldReference{
				Kind:   "okf:Reference",
				Target: targetIRI,
				Exists: link.Exists,
			})); err != nil {
				return err
			}
		}

		type legacySubresource struct {
			identity string
			node     orderedJSONObject
		}
		var subresources []legacySubresource
		if options.ExtensionRelations == ExtensionRelationsInclude {
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
				if !legacyJSONLDReservedKey(relation.Type) {
					vocabulary, err := concatGraphStringsContext(ctx, "okf:", relation.Type)
					if err != nil {
						return err
					}
					if err := contextObject.setContext(ctx, relation.Type, map[string]string{"@id": vocabulary, "@type": "@id"}); err != nil {
						return err
					}
				}
				if relation.Source.Fragment == "" {
					if err := appendJSONLDRelationContext(ctx, &node, relation); err != nil {
						return err
					}
					continue
				}
				source, err := graphRelationRefContext(ctx, relation.Source)
				if err != nil {
					return err
				}
				sourceID, err := graphConceptIDContext(ctx, relation.Source.ID)
				if err != nil {
					return err
				}
				var subresource orderedJSONObject
				for index := range subresources {
					equal, err := equalGraphStringsContext(ctx, subresources[index].identity, source)
					if err != nil {
						return err
					}
					if equal {
						subresource = subresources[index].node
						break
					}
				}
				if subresource == nil {
					if err := contextObject.setContext(ctx, "is_part_of", map[string]string{"@id": "okf:is_part_of", "@type": "@id"}); err != nil {
						return err
					}
					sourceIRI, err := concatGraphStringsContext(ctx, "bundle:", source)
					if err != nil {
						return err
					}
					parentIRI, err := concatGraphStringsContext(ctx, "bundle:", sourceID)
					if err != nil {
						return err
					}
					subresource = orderedJSONObject{{key: "@id", value: sourceIRI}, {key: "@type", value: "okf:SubResource"}, {key: "is_part_of", value: map[string]string{"@id": parentIRI}}}
					subresources = append(subresources, legacySubresource{identity: source, node: subresource})
				}
				if err := appendJSONLDRelationContext(ctx, &subresource, relation); err != nil {
					return err
				}
				for index := range subresources {
					equal, equalErr := equalGraphStringsContext(ctx, subresources[index].identity, source)
					if equalErr != nil {
						return equalErr
					}
					if equal {
						subresources[index].node = subresource
						break
					}
				}
			}
		}

		graphObjects = append(graphObjects, node)
		for sourceIndex, subresource := range subresources {
			if sourceIndex%graphContextCheckInterval == 0 {
				if err := checkGraphContext(ctx); err != nil {
					return err
				}
			}
			graphObjects = append(graphObjects, subresource.node)
		}
	}
	return writeOrderedJSONLDDocumentContext(ctx, w, contextObject, graphObjects)
}

func renderToolkitJSONLD(w io.Writer, b *bundle.Bundle, options Options) error {
	return renderToolkitJSONLDContext(context.Background(), w, b, options)
}

func renderToolkitJSONLDContext(ctx context.Context, w io.Writer, b *bundle.Bundle, options Options) error {
	projection, err := buildToolkitProjectionContext(ctx, b, options)
	if err != nil {
		return err
	}
	document := toolkitJSONLDDocument{
		Context: toolkitJSONLDContext(),
		Graph:   make([]map[string]any, 0, len(projection.Nodes)),
	}
	for nodeIndex, projected := range projection.Nodes {
		if nodeIndex%graphContextCheckInterval == 0 {
			if err := checkGraphContext(ctx); err != nil {
				return err
			}
		}
		node := map[string]any{"@id": projected.ID}
		if len(projected.Types) == 1 {
			node["@type"] = "proj:" + projected.Types[0]
		} else if len(projected.Types) > 1 {
			types := make([]string, len(projected.Types))
			for i, typ := range projected.Types {
				if i%graphContextCheckInterval == 0 {
					if err := checkGraphContext(ctx); err != nil {
						return err
					}
				}
				types[i] = "proj:" + typ
			}
			node["@type"] = types
		}
		properties := make([]string, 0, len(projected.Properties))
		propertyIndex := 0
		for property := range projected.Properties {
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
			values := projected.Properties[property]
			encoded := make([]any, 0, len(values))
			for valueIndex, value := range values {
				if valueIndex%graphContextCheckInterval == 0 {
					if err := checkGraphContext(ctx); err != nil {
						return err
					}
				}
				encoded = append(encoded, toolkitJSONLDValue(value))
			}
			if len(encoded) == 1 {
				node[property] = encoded[0]
			} else {
				node[property] = encoded
			}
		}
		document.Graph = append(document.Graph, node)
	}
	return writeJSONLDDocumentContext(ctx, w, document.Context, document.Graph)
}

func toolkitJSONLDContext() map[string]any {
	return map[string]any{
		"@vocab":              toolkitProjectionNamespace,
		"proj":                toolkitProjectionNamespace,
		"profile":             "proj:profile",
		"projectionVersion":   "proj:projectionVersion",
		"declaredOKFVersion":  "proj:declaredOKFVersion",
		"effectiveOKFVersion": "proj:effectiveOKFVersion",
		"versionSource":       "proj:versionSource",
		"compatibilityMode":   "proj:compatibilityMode",
		"xsd":                 "http://www.w3.org/2001/XMLSchema#",
	}
}

func toolkitJSONLDValue(value projectionValue) any {
	switch value.Kind {
	case projectionIRI:
		return map[string]string{"@id": value.Value}
	case projectionBoolean:
		return map[string]any{"@value": value.Value == "true", "@type": "xsd:boolean"}
	case projectionInteger:
		return map[string]string{"@value": value.Value, "@type": "xsd:integer"}
	case projectionDate:
		return map[string]string{"@value": value.Value, "@type": "xsd:date"}
	case projectionDateTime:
		return map[string]string{"@value": value.Value, "@type": "xsd:dateTime"}
	default:
		return value.Value
	}
}

func jsonldContext() map[string]any {
	return map[string]any{
		"okf":         "https://okf.io/ontology/v0.1#",
		"bundle":      "local:bundle:",
		"type":        "okf:type",
		"title":       "okf:title",
		"description": "okf:description",
		"resource":    "okf:resource",
		"tags":        "okf:tags",
		"timestamp":   "okf:timestamp",
		"references":  "okf:references",
		"target":      map[string]string{"@id": "okf:target", "@type": "@id"},
		"exists":      "okf:exists",
	}
}

func appendJSONLDRelationContext(ctx context.Context, node *orderedJSONObject, relation bundle.Relation) error {
	target, err := graphRelationRefContext(ctx, relation.Target)
	if err != nil {
		return err
	}
	property := relation.Type
	escaped := legacyJSONLDReservedKey(relation.Type)
	if escaped {
		property = "okf:_relations"
	}
	current, _, err := node.getContext(ctx, property)
	if err != nil {
		return err
	}
	relations, _ := current.([]map[string]any)
	targetIRI, err := concatGraphStringsContext(ctx, "bundle:", target)
	if err != nil {
		return err
	}
	entry := map[string]any{"@id": targetIRI, "exists": relation.TargetExists}
	if escaped {
		entry["type"] = relation.Type
	}
	return node.setContext(ctx, property, append(relations, entry))
}

func legacyJSONLDReservedKey(value string) bool {
	switch value {
	case "@context", "@graph", "@id", "@type", "okf", "bundle", "type", "title", "description", "resource", "tags", "timestamp", "references", "target", "exists", "is_part_of", "okf:_relations":
		return true
	default:
		return false
	}
}
