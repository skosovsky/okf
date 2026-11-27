package mutation

import (
	"time"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
	"gopkg.in/yaml.v3"
)

func (s *planState) applyProvenanceV03(op store.ProvenanceV03) error {
	if err := s.requireVersion03(); err != nil {
		return err
	}
	id, kind, by, at := op.Fields()
	switch kind {
	case "set_generated":
		return s.writeGenerated(id, kind, op, store.Generation{By: by, At: at}, yamlProvenanceTimeV03)
	case "ensure_verification":
		return s.writeVerification(id, kind, op, store.Verification{By: by, At: at}, yamlProvenanceTimeV03)
	default:
		return s.deleteVerification(id, kind, op, store.Verification{By: by, At: at}, yamlProvenanceTimeV03)
	}
}

func emptyOptionalFamilyV03(node *yaml.Node) bool {
	return node.Tag == "!!null" || node.Tag == "!!str" && node.Value == "" ||
		node.Kind == yaml.SequenceNode && len(node.Content) == 0
}

func yamlProvenanceTimeV03(raw string) (yamlRenderValue, error) {
	if len(raw) == len(time.DateOnly) {
		return yamlDate(raw)
	}
	parsed, err := bundle.ParseTimestampV03(raw)
	if err != nil {
		return yamlRenderValue{}, err
	}
	return yamlDateTime(parsed.Format(time.RFC3339Nano))
}
