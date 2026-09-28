// Package temporalrequest owns canonical temporal-upgrade request construction
// shared by the planner's replay path and its MCP receipt validation.
package temporalrequest

import (
	"sort"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
)

type Mapping struct {
	Concept bundle.ConceptID
	Path    string
	From    string
	To      string
}

func ChangeSet(id store.ChangeSetID, actor store.Actor, base store.Revision, mappings []Mapping) (store.ChangeSet, error) {
	byConcept := make(map[string][]store.TemporalFieldRewrite)
	for _, mapping := range mappings {
		key := mapping.Concept.String()
		byConcept[key] = append(byConcept[key], store.TemporalFieldRewrite{
			Path: mapping.Path, From: mapping.From, To: mapping.To,
		})
	}
	keys := make([]string, 0, len(byConcept))
	for key := range byConcept {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var operations []store.Operation
	for _, key := range keys {
		concept, err := bundle.ParseConceptID(key)
		if err != nil {
			return store.ChangeSet{}, err
		}
		operation, err := store.NewUpgradeTemporalConcept(concept, byConcept[key])
		if err != nil {
			return store.ChangeSet{}, err
		}
		operations = append(operations, operation)
	}
	change := store.ChangeSet{
		Version: store.ChangeSetFormatVersion, ID: id, Actor: actor,
		BaseRevision: base, Operations: operations,
	}
	return change, change.Validate()
}
