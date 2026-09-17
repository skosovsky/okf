package mutation

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/skosovsky/okf/bundle"
)

func TestCloneYAMLRenderValueContextResourceBoundaries(t *testing.T) {
	nested := func(depth int) yamlRenderValue {
		value := yamlString("leaf")
		for range depth {
			value = yamlSequence(value)
		}
		return value
	}
	wide := func(children int) yamlRenderValue {
		return yamlRenderValue{kind: yamlRenderSequence, sequence: make([]yamlRenderValue, children)}
	}
	sharedEdges := func(edges int) yamlRenderValue {
		return wide(edges)
	}
	tests := []struct {
		name     string
		exact    yamlRenderValue
		over     yamlRenderValue
		wantKind bundle.YAMLResourceLimitKind
	}{
		{name: "physical depth", exact: nested(bundle.MaxYAMLPhysicalDepth), over: nested(bundle.MaxYAMLPhysicalDepth + 1), wantKind: bundle.YAMLResourcePhysicalDepth},
		{name: "graph nodes", exact: wide(bundle.MaxYAMLGraphNodes - 1), over: wide(bundle.MaxYAMLGraphNodes), wantKind: bundle.YAMLResourceGraphNodes},
		{name: "graph edges", exact: sharedEdges(bundle.MaxYAMLGraphEdges - 1), over: sharedEdges(bundle.MaxYAMLGraphEdges + 1), wantKind: bundle.YAMLResourceGraphEdges},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			ctx := context.Background()

			// Act.
			exact, exactErr := cloneYAMLRenderValueContext(ctx, test.exact)
			over, overErr := cloneYAMLRenderValueContext(ctx, test.over)

			// Assert.
			if exactErr != nil || !reflect.DeepEqual(exact, test.exact) {
				t.Fatalf("exact clone = %#v, %v", exact, exactErr)
			}
			if !reflect.DeepEqual(over, yamlRenderValue{}) {
				t.Fatalf("over-limit clone exposed partial result: %#v", over)
			}
			var limit *bundle.YAMLResourceLimitError
			if !errors.As(overErr, &limit) || limit.Kind != test.wantKind || limit.Observed != limit.Limit+1 {
				t.Fatalf("over-limit error = %#v, want %s limit+1", overErr, test.wantKind)
			}
		})
	}
}

func TestCloneYAMLRenderValueContextOwnershipCancellationAndCycle(t *testing.T) {
	t.Run("nil order and defensive deep copy", func(t *testing.T) {
		// Arrange.
		original := yamlMapping(
			yamlEntry("z", yamlSequence(yamlString("first"))),
			yamlEntry("a", yamlRenderValue{kind: yamlRenderSequence, sequence: nil}),
		)

		// Act.
		cloned, err := cloneYAMLRenderValueContext(context.Background(), original)
		cloned.mapping[0].Value.sequence[0].text = "changed"

		// Assert.
		if err != nil || cloned.mapping[0].Key != "z" || cloned.mapping[1].Key != "a" {
			t.Fatalf("clone order/error = %#v, %v", cloned, err)
		}
		if cloned.mapping[1].Value.sequence != nil || original.mapping[0].Value.sequence[0].text != "first" {
			t.Fatalf("nil/deep-copy ownership changed: original=%#v cloned=%#v", original, cloned)
		}
	})

	t.Run("legal DAG sharing is preserved and detached", func(t *testing.T) {
		// Arrange.
		shared := yamlSequence(yamlString("shared"))
		original := yamlSequence(shared, shared)

		// Act.
		cloned, err := cloneYAMLRenderValueContext(context.Background(), original)
		cloned.sequence[0].sequence[0].text = "changed"

		// Assert.
		if err != nil || cloned.sequence[1].sequence[0].text != "changed" {
			t.Fatalf("DAG clone/sharing = %#v, %v", cloned, err)
		}
		if original.sequence[0].sequence[0].text != "shared" || &cloned.sequence[0].sequence[0] == &original.sequence[0].sequence[0] {
			t.Fatalf("DAG clone retained source ownership: original=%#v cloned=%#v", original, cloned)
		}
	})

	t.Run("distinct empty containers remain distinct occurrences", func(t *testing.T) {
		// Arrange.
		left := yamlRenderValue{kind: yamlRenderSequence, sequence: make([]yamlRenderValue, 0)}
		right := yamlRenderValue{kind: yamlRenderSequence, sequence: make([]yamlRenderValue, 0)}

		// Act.
		cloned, err := cloneYAMLRenderValueContext(context.Background(), yamlSequence(left, right))

		// Assert.
		if err != nil || cloned.sequence[0].sequence == nil || cloned.sequence[1].sequence == nil {
			t.Fatalf("empty container clone = %#v, %v", cloned, err)
		}
	})

	t.Run("deep cancellation returns zero", func(t *testing.T) {
		// Arrange.
		value := yamlRenderValue{kind: yamlRenderSequence, sequence: make([]yamlRenderValue, 4096)}
		ctx := &countdownContext{Context: context.Background(), remaining: 128}

		// Act.
		cloned, err := cloneYAMLRenderValueContext(ctx, value)

		// Assert.
		if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(cloned, yamlRenderValue{}) {
			t.Fatalf("canceled clone = %#v, %v", cloned, err)
		}
	})

	t.Run("content cycle is typed", func(t *testing.T) {
		// Arrange.
		content := make([]yamlRenderValue, 1)
		cycle := yamlRenderValue{kind: yamlRenderSequence, sequence: content}
		content[0] = cycle

		// Act.
		cloned, err := cloneYAMLRenderValueContext(context.Background(), cycle)

		// Assert.
		var integrity *bundle.YAMLIntegrityError
		if !reflect.DeepEqual(cloned, yamlRenderValue{}) || !errors.As(err, &integrity) || integrity.Code != bundle.YAMLIntegrityContentCycle {
			t.Fatalf("cycle clone = %#v, %#v", cloned, err)
		}
	})
}

func TestCloneYAMLRenderValueContextMemoizedDAGAndLogicalSubsliceIdentity(t *testing.T) {
	t.Run("depth-64 binary DAG is bounded and cancellable", func(t *testing.T) {
		// Arrange.
		value := yamlString("leaf")
		for range bundle.MaxYAMLPhysicalDepth {
			value = yamlSequence(value, value)
		}

		// Act.
		cloned, expansionErr := cloneYAMLRenderValueContext(context.Background(), value)
		cancelled, cancelErr := cloneYAMLRenderValueContext(
			&countdownContext{Context: context.Background(), remaining: 16},
			value,
		)

		// Assert.
		var limit *bundle.YAMLResourceLimitError
		if !reflect.DeepEqual(cloned, yamlRenderValue{}) || !errors.As(expansionErr, &limit) || limit.Kind != bundle.YAMLResourceSemanticExpansion {
			t.Fatalf("binary DAG expansion = %#v, %#v", cloned, expansionErr)
		}
		if !reflect.DeepEqual(cancelled, yamlRenderValue{}) || !errors.Is(cancelErr, context.Canceled) {
			t.Fatalf("binary DAG cancellation = %#v, %v", cancelled, cancelErr)
		}
	})

	t.Run("total expansion is bounded independently of multiplicity premium", func(t *testing.T) {
		// Arrange. The expanded graph has 120001 visits, while its reuse premium
		// is only 70000 and its unique node/edge counts remain below their caps.
		shared := yamlSequence(yamlString("shared"))
		children := make([]yamlRenderValue, 0, 85_000)
		children = append(children, make([]yamlRenderValue, 50_000)...)
		for range 35_000 {
			children = append(children, shared)
		}
		value := yamlSequence(children...)

		// Act.
		cloned, err := cloneYAMLRenderValueContext(context.Background(), value)

		// Assert.
		var limit *bundle.YAMLResourceLimitError
		if !reflect.DeepEqual(cloned, yamlRenderValue{}) || !errors.As(err, &limit) || limit.Kind != bundle.YAMLResourceSemanticExpansion {
			t.Fatalf("total expansion = %#v, %#v", cloned, err)
		}
	})

	t.Run("same-start subslices retain distinct logical lengths and limits", func(t *testing.T) {
		// Arrange.
		base := make([]yamlRenderValue, bundle.MaxYAMLGraphEdges)
		short := yamlRenderValue{kind: yamlRenderSequence, sequence: base[:1]}
		long := yamlRenderValue{kind: yamlRenderSequence, sequence: base}
		small := yamlSequence(
			yamlRenderValue{kind: yamlRenderSequence, sequence: base[:1]},
			yamlRenderValue{kind: yamlRenderSequence, sequence: base[:2]},
		)

		// Act.
		cloned, cloneErr := cloneYAMLRenderValueContext(context.Background(), small)
		over, overErr := cloneYAMLRenderValueContext(context.Background(), yamlSequence(short, long))

		// Assert.
		if cloneErr != nil || len(cloned.sequence[0].sequence) != 1 || len(cloned.sequence[1].sequence) != 2 {
			t.Fatalf("subslice clone lengths = %#v, %v", cloned, cloneErr)
		}
		var limit *bundle.YAMLResourceLimitError
		if !reflect.DeepEqual(over, yamlRenderValue{}) || !errors.As(overErr, &limit) || limit.Kind != bundle.YAMLResourceGraphEdges {
			t.Fatalf("subslice limit = %#v, %#v", over, overErr)
		}
	})
}
