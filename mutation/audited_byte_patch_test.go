package mutation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"gopkg.in/yaml.v3"
)

func TestAuditedBytePatchesFreezeCallerOwnedState(t *testing.T) {
	// Arrange.
	source := []byte("old")
	text := []byte("new")
	path := []int{1, 2}
	expected := &yamlSemanticNode{Kind: yaml.SequenceNode, Content: []*yamlSemanticNode{{Kind: yaml.ScalarNode, Value: "stable"}}}
	edit := &semanticEdit{path: path, before: "old", after: "new", expected: expected}
	patches := []bytePatch{{Start: 0, End: 3, Text: text, edit: edit}}
	plan, err := auditBytePatchesContext(context.Background(), source, patches)
	if err != nil {
		t.Fatalf("auditBytePatchesContext() error = %v", err)
	}

	// Act. Mutate every caller-owned slice and pointer reachable from input.
	text[0] = 'x'
	path[0] = 99
	expected.Content[0].Value = "changed"
	edit.before, edit.after = "changed", "changed"
	patches[0].Text = []byte("also changed")
	patches[0].edit = nil
	updated, applyErr := applyAuditedBytePatchesContext(context.Background(), source, plan)

	// Assert.
	if applyErr != nil || !bytes.Equal(updated, []byte("new")) {
		t.Fatalf("applyAuditedBytePatchesContext() = %q, %v", updated, applyErr)
	}
	frozen := plan.patches[0]
	if frozen.edit == nil || frozen.edit.before != "old" || frozen.edit.after != "new" ||
		len(frozen.edit.path) != 2 || frozen.edit.path[0] != 1 || frozen.edit.expected.Content[0].Value != "stable" {
		t.Fatalf("audited semantic edit changed through caller aliases: %#v", frozen.edit)
	}
}

func TestAuditedBytePatchesRejectInvalidSemanticEditGraphs(t *testing.T) {
	newScalar := func(value string) *yamlSemanticNode {
		return &yamlSemanticNode{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
	}
	selfCycle := &yamlSemanticNode{Kind: yaml.SequenceNode, Tag: "!!seq"}
	selfCycle.Content = []*yamlSemanticNode{selfCycle}
	reused := newScalar("reused")

	tests := []struct {
		name     string
		expected *yamlSemanticNode
	}{
		{name: "content cycle", expected: selfCycle},
		{name: "content reuse", expected: &yamlSemanticNode{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yamlSemanticNode{reused, reused}}},
		{name: "nil content node", expected: &yamlSemanticNode{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yamlSemanticNode{nil}}},
		{name: "odd mapping content", expected: &yamlSemanticNode{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yamlSemanticNode{newScalar("key")}}},
		{name: "scalar with content", expected: &yamlSemanticNode{Kind: yaml.ScalarNode, Tag: "!!str", Content: []*yamlSemanticNode{newScalar("child")}}},
		{name: "invalid kind", expected: &yamlSemanticNode{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			source := []byte("old")
			patches := []bytePatch{{
				Start: 0, End: len(source), Text: []byte("new"),
				edit: &semanticEdit{expected: test.expected},
			}}

			// Act.
			plan, err := auditBytePatchesContext(context.Background(), source, patches)

			// Assert.
			assertZeroInvalidPatchPlan(t, plan, err, len(source))
		})
	}
}

func TestAuditedBytePatchesEnforceSemanticEditResourceBounds(t *testing.T) {
	t.Run("physical depth exact and plus one", func(t *testing.T) {
		// Arrange.
		exact := nestedSemanticSequence(bundle.MaxYAMLPhysicalDepth)
		over := nestedSemanticSequence(bundle.MaxYAMLPhysicalDepth + 1)

		// Act.
		exactPlan, exactErr := auditSemanticExpected(exact)
		overPlan, overErr := auditSemanticExpected(over)

		// Assert.
		if exactErr != nil || len(exactPlan.patches) != 1 {
			t.Fatalf("exact depth audit = %#v, %v", exactPlan, exactErr)
		}
		assertZeroInvalidPatchPlan(t, overPlan, overErr, len("old"))
	})

	t.Run("graph nodes exact and plus one", func(t *testing.T) {
		// Arrange.
		exact := flatSemanticSequence(bundle.MaxYAMLGraphNodes)
		over := flatSemanticSequence(bundle.MaxYAMLGraphNodes + 1)

		// Act.
		exactPlan, exactErr := auditSemanticExpected(exact)
		overPlan, overErr := auditSemanticExpected(over)

		// Assert.
		if exactErr != nil || len(exactPlan.patches) != 1 {
			t.Fatalf("exact node limit audit = %d patches, %v", len(exactPlan.patches), exactErr)
		}
		assertZeroInvalidPatchPlan(t, overPlan, overErr, len("old"))
	})

	t.Run("path exact and plus one", func(t *testing.T) {
		// Arrange.
		exactPath := make([]int, bundle.MaxYAMLPhysicalDepth)
		overPath := make([]int, bundle.MaxYAMLPhysicalDepth+1)
		source := []byte("old")

		// Act.
		exactPlan, exactErr := auditBytePatchesContext(context.Background(), source, []bytePatch{{
			Start: 0, End: len(source), Text: []byte("new"), edit: &semanticEdit{path: exactPath},
		}})
		overPlan, overErr := auditBytePatchesContext(context.Background(), source, []bytePatch{{
			Start: 0, End: len(source), Text: []byte("new"), edit: &semanticEdit{path: overPath},
		}})

		// Assert.
		if exactErr != nil || len(exactPlan.patches) != 1 {
			t.Fatalf("exact path audit = %#v, %v", exactPlan, exactErr)
		}
		assertZeroInvalidPatchPlan(t, overPlan, overErr, len(source))
	})
}

func TestAuditedBytePatchesSemanticCloneCancellationReturnsZeroPlan(t *testing.T) {
	t.Run("pre-cancel", func(t *testing.T) {
		// Arrange.
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		// Act.
		plan, err := auditSemanticExpected(flatSemanticSequence(4))
		canceledPlan, canceledErr := auditBytePatchesContext(ctx, []byte("old"), []bytePatch{{
			Start: 0, End: len("old"), Text: []byte("new"), edit: &semanticEdit{expected: flatSemanticSequence(4)},
		}})

		// Assert. The background control proves the fixture itself is valid.
		if err != nil || len(plan.patches) != 1 {
			t.Fatalf("control audit = %#v, %v", plan, err)
		}
		assertZeroCanceledPlan(t, canceledPlan, canceledErr)
	})

	t.Run("mid-clone", func(t *testing.T) {
		// Arrange.
		ctx := &countdownContext{Context: context.Background(), remaining: 64}
		source := []byte("old")
		patches := []bytePatch{{
			Start: 0, End: len(source), Text: []byte("new"),
			edit: &semanticEdit{expected: flatSemanticSequence(4_096)},
		}}

		// Act.
		plan, err := auditBytePatchesContext(ctx, source, patches)

		// Assert.
		assertZeroCanceledPlan(t, plan, err)
	})

	t.Run("mid-path-clone", func(t *testing.T) {
		// Arrange.
		ctx := &countdownContext{Context: context.Background(), remaining: 12}
		source := []byte("old")
		patches := []bytePatch{{
			Start: 0, End: len(source), Text: []byte("new"),
			edit: &semanticEdit{path: make([]int, bundle.MaxYAMLPhysicalDepth)},
		}}

		// Act.
		plan, err := auditBytePatchesContext(ctx, source, patches)

		// Assert.
		assertZeroCanceledPlan(t, plan, err)
	})
}

func TestAuditedBytePatchesBindExactSource(t *testing.T) {
	t.Run("mutated source", func(t *testing.T) {
		// Arrange.
		source := []byte("old")
		plan, err := auditBytePatchesContext(context.Background(), source, []bytePatch{{Start: 0, End: 3, Text: []byte("new")}})
		if err != nil {
			t.Fatalf("auditBytePatchesContext() error = %v", err)
		}
		source[0] = 'x'

		// Act.
		updated, applyErr := applyAuditedBytePatchesContext(context.Background(), source, plan)

		// Assert.
		if updated != nil || !errors.Is(applyErr, ErrUnsupportedPresentation) {
			t.Fatalf("applyAuditedBytePatchesContext() = %q, %v", updated, applyErr)
		}
	})

	t.Run("different equal-length source", func(t *testing.T) {
		// Arrange.
		plan, err := auditBytePatchesContext(context.Background(), []byte("old"), []bytePatch{{Start: 0, End: 3, Text: []byte("new")}})
		if err != nil {
			t.Fatalf("auditBytePatchesContext() error = %v", err)
		}

		// Act.
		updated, applyErr := applyAuditedBytePatchesContext(context.Background(), []byte("bad"), plan)

		// Assert.
		if updated != nil || !errors.Is(applyErr, ErrUnsupportedPresentation) {
			t.Fatalf("applyAuditedBytePatchesContext() = %q, %v", updated, applyErr)
		}
	})
}

func TestPresentationAuditedPatchFreezesAndRevalidatesOwner(t *testing.T) {
	const document = "---\nname: old\n---\nbody\n"

	t.Run("owner field reassignment cannot affect proof", func(t *testing.T) {
		// Arrange.
		presentation, err := parsePresentationContext(context.Background(), []byte(document))
		if err != nil {
			t.Fatalf("parsePresentationContext(context.Background(), ) error = %v", err)
		}
		owner := presentation.root.Content[1]
		start := bytes.Index(presentation.data, []byte("old"))
		patches := []bytePatch{{
			Start: start, End: start + len("old"), Text: []byte("new"), Owner: owner,
			edit: &semanticEdit{path: []int{1}, before: "old", after: "new"},
		}}
		plan, auditErr := presentation.auditBytePatchesContext(context.Background(), patches)
		if auditErr != nil {
			t.Fatalf("auditBytePatchesContext() error = %v", auditErr)
		}
		ownerPath := presentation.paths[owner]

		// Act.
		patches[0].Owner = &yaml.Node{Kind: yaml.ScalarNode, Value: "detached"}
		ownerPath[0] = 0
		validateErr := presentation.validateAuditedOwnersContext(context.Background(), plan)

		// Assert.
		if validateErr != nil || plan.patches[0].owner == nil || plan.patches[0].owner.value != "old" {
			t.Fatalf("validateAuditedOwnersContext() error = %v, proof = %#v", validateErr, plan.patches[0].owner)
		}
	})

	t.Run("pointed owner mutation fails closed", func(t *testing.T) {
		// Arrange.
		presentation, err := parsePresentationContext(context.Background(), []byte(document))
		if err != nil {
			t.Fatalf("parsePresentationContext(context.Background(), ) error = %v", err)
		}
		owner := presentation.root.Content[1]
		start := bytes.Index(presentation.data, []byte("old"))
		plan, auditErr := presentation.auditBytePatchesContext(context.Background(), []bytePatch{{
			Start: start, End: start + len("old"), Text: []byte("new"), Owner: owner,
			edit: &semanticEdit{path: []int{1}, before: "old", after: "new"},
		}})
		if auditErr != nil {
			t.Fatalf("auditBytePatchesContext() error = %v", auditErr)
		}
		owner.Value = "tampered"

		// Act.
		validateErr := presentation.validateAuditedOwnersContext(context.Background(), plan)

		// Assert.
		if !errors.Is(validateErr, ErrUnsupportedPresentation) {
			t.Fatalf("validateAuditedOwnersContext() error = %v", validateErr)
		}
	})
}

func TestPresentationAuditedPatchRejectsOwnerGraphMutation(t *testing.T) {
	const document = "---\nname: old\n---\nbody\n"

	mutations := []struct {
		name   string
		mutate func(*yaml.Node)
	}{
		{
			name: "content cycle",
			mutate: func(owner *yaml.Node) {
				owner.Kind, owner.Tag, owner.Value = yaml.SequenceNode, "!!seq", ""
				owner.Content = []*yaml.Node{owner}
			},
		},
		{
			name: "content reuse",
			mutate: func(owner *yaml.Node) {
				child := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "child"}
				owner.Kind, owner.Tag, owner.Value = yaml.SequenceNode, "!!seq", ""
				owner.Content = []*yaml.Node{child, child}
			},
		},
		{
			name: "dangling alias",
			mutate: func(owner *yaml.Node) {
				target := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "target", Anchor: "detached"}
				owner.Kind, owner.Tag, owner.Value = yaml.AliasNode, "", "detached"
				owner.Alias, owner.Content = target, nil
			},
		},
		{
			name: "invalid scalar shape",
			mutate: func(owner *yaml.Node) {
				owner.Content = []*yaml.Node{{Kind: yaml.ScalarNode, Tag: "!!str", Value: "child"}}
			},
		},
	}

	for _, mutation := range mutations {
		t.Run("after audit "+mutation.name, func(t *testing.T) {
			// Arrange.
			presentation, owner, plan := auditedOwnerFixture(t, document)
			mutation.mutate(owner)

			// Act.
			err := presentation.validateAuditedOwnersContext(context.Background(), plan)

			// Assert.
			assertInvalidPatchError(t, err, len(presentation.data))
		})

		t.Run("before audit "+mutation.name, func(t *testing.T) {
			// Arrange.
			presentation, err := parsePresentationContext(context.Background(), []byte(document))
			if err != nil {
				t.Fatalf("parsePresentationContext(context.Background(), ) error = %v", err)
			}
			owner := presentation.root.Content[1]
			mutation.mutate(owner)
			start := bytes.Index(presentation.data, []byte("old"))

			// Act.
			plan, auditErr := presentation.auditBytePatchesContext(context.Background(), []bytePatch{{
				Start: start, End: start + len("old"), Text: []byte("new"), Owner: owner,
			}})

			// Assert.
			assertZeroInvalidPatchPlan(t, plan, auditErr, len(presentation.data))
		})
	}
}

func TestPresentationOwnerFingerprintCancellation(t *testing.T) {
	const document = "---\nname: old\n---\nbody\n"

	t.Run("chunked value fingerprint", func(t *testing.T) {
		// Arrange.
		presentation, err := parsePresentationContext(context.Background(), []byte(document))
		if err != nil {
			t.Fatalf("parsePresentationContext(context.Background(), ) error = %v", err)
		}
		owner := presentation.root.Content[1]
		owner.Value = strings.Repeat("x", 4<<20)
		ctx := &countdownContext{Context: context.Background(), remaining: 16}

		// Act.
		fingerprint, fingerprintErr := presentation.yamlNodeFingerprintContext(ctx, owner)

		// Assert.
		if !errors.Is(fingerprintErr, context.Canceled) || fingerprint != ([sha256.Size]byte{}) {
			t.Fatalf("yamlNodeFingerprintContext() = %x, %v", fingerprint, fingerprintErr)
		}
	})

	t.Run("owner audit returns zero plan", func(t *testing.T) {
		// Arrange.
		presentation, err := parsePresentationContext(context.Background(), []byte(document))
		if err != nil {
			t.Fatalf("parsePresentationContext(context.Background(), ) error = %v", err)
		}
		owner := presentation.root.Content[1]
		owner.Value = strings.Repeat("x", 4<<20)
		start := bytes.Index(presentation.data, []byte("old"))
		ctx := &countdownContext{Context: context.Background(), remaining: 48}

		// Act.
		plan, auditErr := presentation.auditBytePatchesContext(ctx, []bytePatch{{
			Start: start, End: start + len("old"), Text: []byte("new"), Owner: owner,
		}})

		// Assert.
		assertZeroCanceledPlan(t, plan, auditErr)
	})
}

func TestAuditedPatchOwnerScansPropagateCancellation(t *testing.T) {
	t.Run("presentation audit owner-presence scan returns zero plan", func(t *testing.T) {
		// Arrange.
		source := []byte("old")
		patches := make([]bytePatch, 8_192)
		presentation := &presentation{data: source}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		// Act.
		plan, err := presentation.auditBytePatchesContext(ctx, patches)

		// Assert.
		assertZeroCanceledPlan(t, plan, err)
	})

	t.Run("validated-plan owner-presence scan propagates error", func(t *testing.T) {
		// Arrange.
		source := []byte("old")
		patches := make([]bytePatch, 8_192)
		plan, err := auditBytePatchesContext(context.Background(), source, patches)
		if err != nil {
			t.Fatalf("auditBytePatchesContext() error = %v", err)
		}
		if err := plan.validateSourceContext(context.Background(), source); err != nil {
			t.Fatalf("validateSourceContext() error = %v", err)
		}
		presentation := &presentation{data: source}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		// Act.
		validateErr := presentation.validateAuditedOwnersContext(ctx, plan)

		// Assert.
		if !errors.Is(validateErr, context.Canceled) {
			t.Fatalf("validateAuditedOwnersContext() error = %v", validateErr)
		}
	})

	t.Run("content-kind clone and validation cancel mid-loop", func(t *testing.T) {
		// Arrange.
		content := make([]*yaml.Node, 8_192)
		kinds := make([]yaml.Kind, len(content))
		for index := range content {
			content[index] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str"}
			kinds[index] = yaml.ScalarNode
		}

		// Act.
		cloned, cloneErr := cloneYAMLContentKindsContext(
			&countdownContext{Context: context.Background(), remaining: 2},
			content,
		)
		equal, equalErr := equalYAMLContentKindsContext(
			&countdownContext{Context: context.Background(), remaining: 2},
			content,
			kinds,
		)

		// Assert.
		if cloned != nil || !errors.Is(cloneErr, context.Canceled) {
			t.Fatalf("cloneYAMLContentKindsContext() = %d kinds, %v", len(cloned), cloneErr)
		}
		if equal || !errors.Is(equalErr, context.Canceled) {
			t.Fatalf("equalYAMLContentKindsContext() = %v, %v", equal, equalErr)
		}
	})

	t.Run("owner helper scans cancel after first chunk", func(t *testing.T) {
		// Arrange.
		input := make([]bytePatch, 8_192)
		audited := make([]auditedBytePatch, 8_192)

		// Act.
		inputIndex, inputErr := firstInputPatchOwnerContext(
			&countdownContext{Context: context.Background(), remaining: 2}, input,
		)
		auditedIndex, auditedErr := firstAuditedPatchOwnerContext(
			&countdownContext{Context: context.Background(), remaining: 2}, audited,
		)

		// Assert.
		if inputIndex != -1 || !errors.Is(inputErr, context.Canceled) {
			t.Fatalf("firstInputPatchOwnerContext() = %d, %v", inputIndex, inputErr)
		}
		if auditedIndex != -1 || !errors.Is(auditedErr, context.Canceled) {
			t.Fatalf("firstAuditedPatchOwnerContext() = %d, %v", auditedIndex, auditedErr)
		}
	})
}

func auditSemanticExpected(expected *yamlSemanticNode) (auditedBytePatches, error) {
	source := []byte("old")
	return auditBytePatchesContext(context.Background(), source, []bytePatch{{
		Start: 0, End: len(source), Text: []byte("new"), edit: &semanticEdit{expected: expected},
	}})
}

func nestedSemanticSequence(depth int) *yamlSemanticNode {
	node := &yamlSemanticNode{Kind: yaml.ScalarNode, Tag: "!!str", Value: "leaf"}
	for range depth {
		node = &yamlSemanticNode{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yamlSemanticNode{node}}
	}
	return node
}

func flatSemanticSequence(nodes int) *yamlSemanticNode {
	root := &yamlSemanticNode{Kind: yaml.SequenceNode, Tag: "!!seq"}
	if nodes <= 1 {
		return root
	}
	root.Content = make([]*yamlSemanticNode, nodes-1)
	for index := range root.Content {
		root.Content[index] = &yamlSemanticNode{Kind: yaml.ScalarNode, Tag: "!!str", Value: "value"}
	}
	return root
}

func auditedOwnerFixture(t *testing.T, document string) (*presentation, *yaml.Node, auditedBytePatches) {
	t.Helper()
	presentation, err := parsePresentationContext(context.Background(), []byte(document))
	if err != nil {
		t.Fatalf("parsePresentationContext(context.Background(), ) error = %v", err)
	}
	owner := presentation.root.Content[1]
	start := bytes.Index(presentation.data, []byte("old"))
	plan, auditErr := presentation.auditBytePatchesContext(context.Background(), []bytePatch{{
		Start: start, End: start + len("old"), Text: []byte("new"), Owner: owner,
	}})
	if auditErr != nil {
		t.Fatalf("auditBytePatchesContext() error = %v", auditErr)
	}
	return presentation, owner, plan
}

func assertZeroInvalidPatchPlan(t *testing.T, plan auditedBytePatches, err error, sourceLen int) {
	t.Helper()
	assertZeroAuditedPlan(t, plan)
	assertInvalidPatchError(t, err, sourceLen)
}

func assertInvalidPatchError(t *testing.T, err error, sourceLen int) {
	t.Helper()
	var presentationErr *PresentationError
	if !errors.As(err, &presentationErr) || presentationErr.Code != "invalid_patch" ||
		!errors.Is(err, ErrUnsupportedPresentation) {
		t.Fatalf("error = %T %v, want invalid_patch", err, err)
	}
	if presentationErr.Location.Start < 0 || presentationErr.Location.End <= presentationErr.Location.Start ||
		presentationErr.Location.End > sourceLen {
		t.Fatalf("invalid_patch location = %#v for %d-byte source", presentationErr.Location, sourceLen)
	}
}

func assertZeroCanceledPlan(t *testing.T, plan auditedBytePatches, err error) {
	t.Helper()
	assertZeroAuditedPlan(t, plan)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func assertZeroAuditedPlan(t *testing.T, plan auditedBytePatches) {
	t.Helper()
	if len(plan.patches) != 0 || plan.outputLen != 0 || plan.sourceLen != 0 ||
		plan.sourceFingerprint != ([sha256.Size]byte{}) {
		t.Fatalf("audit returned a partial plan: %#v", plan)
	}
}
