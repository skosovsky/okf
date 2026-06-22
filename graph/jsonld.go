package graph

import (
	"encoding/json"
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

// RenderJSONLD writes a JSON-LD graph export.
func RenderJSONLD(w io.Writer, b *bundle.Bundle) error {
	concepts := b.Concepts()
	document := jsonldDocument{
		Context: jsonldContext(),
		Graph:   make([]map[string]any, 0, len(concepts)),
	}

	for _, concept := range concepts {
		node := map[string]any{
			"@id":   "bundle:" + concept.ID.String(),
			"@type": "okf:Concept",
		}

		frontmatter := concept.Document.Frontmatter
		if value, ok := frontmatter.Type(); ok {
			node["type"] = value
		}
		if value, ok := frontmatter.Title(); ok {
			node["title"] = value
		}
		if value, ok := frontmatter.Description(); ok {
			node["description"] = value
		}
		if value, ok := frontmatter.Resource(); ok {
			node["resource"] = value
		}
		if value, ok := frontmatter.Timestamp(); ok {
			node["timestamp"] = value
		}
		if tags := frontmatter.Tags(); len(tags) > 0 {
			node["tags"] = tags
		}

		for _, link := range b.LinksFrom(concept.ID) {
			references, _ := node["references"].([]jsonldReference)
			node["references"] = append(references, jsonldReference{
				Kind:   "okf:Reference",
				Target: "bundle:" + link.Target.String(),
				Exists: link.Exists,
			})
		}

		var subresourceOrder []string
		subresources := make(map[string]map[string]any)
		for _, relation := range b.SemanticLinksFrom(concept.ID) {
			document.Context[relation.Type] = map[string]string{"@id": "okf:" + relation.Type, "@type": "@id"}
			if relation.Source.Fragment == "" {
				appendJSONLDRelation(node, relation)
				continue
			}
			source := relation.Source.String()
			subresource, ok := subresources[source]
			if !ok {
				document.Context["is_part_of"] = map[string]string{"@id": "okf:is_part_of", "@type": "@id"}
				subresource = map[string]any{
					"@id":        "bundle:" + source,
					"@type":      "okf:SubResource",
					"is_part_of": map[string]string{"@id": "bundle:" + relation.Source.ID.String()},
				}
				subresources[source] = subresource
				subresourceOrder = append(subresourceOrder, source)
			}
			appendJSONLDRelation(subresource, relation)
		}

		document.Graph = append(document.Graph, node)
		for _, source := range subresourceOrder {
			document.Graph = append(document.Graph, subresources[source])
		}
	}

	output, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	if written, err := w.Write(output); err != nil {
		return err
	} else if written != len(output) {
		return io.ErrShortWrite
	}
	return writeString(w, "\n")
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

func appendJSONLDRelation(node map[string]any, relation bundle.Relation) {
	relations, _ := node[relation.Type].([]map[string]any)
	node[relation.Type] = append(relations, map[string]any{
		"@id":    "bundle:" + relation.Target.String(),
		"exists": relation.TargetExists,
	})
}
