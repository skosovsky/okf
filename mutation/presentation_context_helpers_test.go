package mutation

import (
	"context"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"gopkg.in/yaml.v3"
)

func bytesOutsidePatchesEqualForTest(t *testing.T, before, after []byte, patches []bytePatch) bool {
	t.Helper()
	equal, err := bytesOutsidePatchesEqualContext(context.Background(), before, after, patches)
	if err != nil {
		t.Fatal(err)
	}
	return equal
}

func semanticPresentationsEqualForTest(t *testing.T, left, right *yaml.Node) bool {
	t.Helper()
	leftSemantic, err := semanticYAMLContext(context.Background(), left)
	if err != nil {
		t.Fatal(err)
	}
	rightSemantic, err := semanticYAMLContext(context.Background(), right)
	if err != nil {
		t.Fatal(err)
	}
	equal, err := equalSemanticYAMLContext(context.Background(), leftSemantic, rightSemantic)
	if err != nil {
		t.Fatal(err)
	}
	return equal
}

func yamlRenderStatesEqualForTest(t *testing.T, left, right yamlRenderValue) bool {
	t.Helper()
	equal, err := equalYAMLRenderStateContext(context.Background(), left, right)
	if err != nil {
		t.Fatal(err)
	}
	return equal
}

func applyYAMLMutationForTest(p *presentation, action func(*yamlMutation) error) ([]byte, error) {
	mutation := p.newYAMLMutationContext(context.Background())
	if err := action(mutation); err != nil {
		return nil, err
	}
	return mutation.apply()
}

func mappingValuesForTest(t *testing.T, mapping *yaml.Node, key string) []*yaml.Node {
	t.Helper()
	values, err := mappingValuesForKeyContext(context.Background(), mapping, key)
	if err != nil {
		t.Fatal(err)
	}
	return values
}

func yamlDateForTest(t *testing.T, value string) yamlRenderValue {
	t.Helper()
	rendered, err := yamlDate(value)
	if err != nil {
		t.Fatal(err)
	}
	return rendered
}

func yamlDateTimeForTest(t *testing.T, value string) yamlRenderValue {
	t.Helper()
	rendered, err := yamlDateTime(value)
	if err != nil {
		t.Fatal(err)
	}
	return rendered
}

func yamlRenderFromNodeForTest(t *testing.T, node *yaml.Node) yamlRenderValue {
	t.Helper()
	rendered, err := yamlRenderFromNodeIgnoringCommentsContext(context.Background(), node)
	if err != nil {
		t.Fatal(err)
	}
	return rendered
}

func selectSequenceItemForTest(t *testing.T, p *presentation, collection *yaml.Node, selector yamlSequenceSelector) *yaml.Node {
	t.Helper()
	item, found, err := p.selectSequenceItem(collection, selector)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("sequence item not found")
	}
	return item
}

func readSourceFileForTest(t *testing.T, source bundle.Source, path string) []byte {
	t.Helper()
	data, err := source.ReadFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
