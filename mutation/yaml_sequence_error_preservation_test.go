package mutation

import (
	"context"
	"errors"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestAccumulatedSequenceLineagePreservesSelectorPresentationError(t *testing.T) {
	collection := &yaml.Node{Kind: yaml.AliasNode}
	p := &presentation{ctx: context.Background(), yaml: []byte("items: *shared\n")}
	m := &yamlMutation{
		ctx:               context.Background(),
		p:                 p,
		expectedNodes:     map[*yaml.Node]*yamlSemanticNode{collection: {Kind: yaml.SequenceNode}},
		collectionLineage: make(map[*yaml.Node][]yamlSequenceLineage),
		sequenceShadows:   make(map[*yaml.Node]*yamlSequenceShadow),
	}
	base := yamlString("base")
	accumulated := yamlString("changed")

	lineages, err := m.accumulatedSequenceLineage(collection, base, accumulated)

	var located *PresentationError
	if lineages != nil || !errors.As(err, &located) || located.Code != yamlCodeAliasProvenance ||
		located.Location != (SourceSpan{Start: 0, End: len(p.yaml)}) || !errors.Is(err, ErrAmbiguousPresentation) {
		t.Fatalf("accumulatedSequenceLineage() = (%#v, %#v), want alias provenance at full YAML span", lineages, err)
	}
}
