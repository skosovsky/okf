package mutation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestYAMLMutation_CollectionEntryOwnershipIsRecursive(t *testing.T) {
	fixtures := []struct {
		name string
		body string
		kind error
		code string
	}{
		{
			name: "nested alias",
			body: "base: &base\n  value: x\nsettings:\n  nested: *base\n",
			kind: ErrAmbiguousPresentation,
			code: yamlCodeAliasProvenance,
		},
		{
			name: "nested merge",
			body: "base: &base\n  value: x\nsettings:\n  nested:\n    <<: *base\n    own: y\n",
			kind: ErrAmbiguousPresentation,
			code: yamlCodeAliasProvenance,
		},
		{
			name: "nested anchor",
			body: "settings:\n  nested: &owned\n    value: x\n",
			kind: ErrUnsupportedPresentation,
			code: yamlCodeTouchedAnchor,
		},
		{
			name: "nested tag",
			body: "settings:\n  nested: !custom\n    value: x\n",
			kind: ErrUnsupportedPresentation,
			code: yamlCodeExplicitTag,
		},
		{
			name: "nested lone non-specific collection tag",
			body: "settings:\n  nested: !\n    value: x\n",
			kind: ErrUnsupportedPresentation,
			code: yamlCodeExplicitTag,
		},
		{
			name: "nested lone non-specific scalar tag",
			body: "settings:\n  nested:\n    value: ! x\n",
			kind: ErrUnsupportedPresentation,
			code: yamlCodeExplicitTag,
		},
		{
			name: "complex key",
			body: "settings:\n  nested:\n    ? [a, b]\n    : value\n",
			kind: ErrUnsupportedPresentation,
			code: yamlCodeUnsupportedKey,
		},
		{
			name: "nested comment",
			body: "settings:\n  nested:\n    value: x # owner unknown\n",
			kind: ErrUnsupportedPresentation,
			code: yamlCodeUnownedComment,
		},
	}
	for _, fixture := range fixtures {
		for _, operation := range []string{"replace", "delete"} {
			t.Run(fixture.name+"/"+operation, func(t *testing.T) {
				// Arrange.
				source := []byte("---\ntype: Knowledge\n" + fixture.body + "---\nBody\n")
				p, err := parsePresentationContext(context.Background(), source)
				if err != nil {
					t.Fatal(err)
				}

				// Act.
				var updated []byte
				if operation == "replace" {
					updated, err = p.replaceMappingValue(p.root, "settings", yamlString("new"))
				} else {
					updated, err = applyYAMLMutationForTest(p, func(m *yamlMutation) error { return m.deleteMappingValue(p.root, "settings") })
				}

				// Assert.
				assertStructuralPresentationError(t, updated, err, fixture.kind, fixture.code, len(source))
			})
		}
	}
}

func TestYAMLMutation_SequenceSelectorsAreRouteAware(t *testing.T) {
	// Arrange.
	source := []byte("---\ntype: Knowledge\nsources:\n  - id: source-a\n    resource: a.md\nverified:\n  - by: person:a\n    at: 2026-01-02T03:04:05Z\nparameters:\n  - name: input\n    type: string\ncustom:\n  - id: extension\n---\n")
	p, err := parsePresentationContext(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name       string
		collection string
		selector   yamlSequenceSelector
	}{
		{name: "source partial non-id", collection: "sources", selector: yamlMappingSelector(yamlSelector("resource", yamlString("a.md")))},
		{name: "source exact with id", collection: "sources", selector: yamlExactSelector(yamlMapping(yamlEntry("id", yamlString("source-a")), yamlEntry("resource", yamlString("a.md"))))},
		{name: "verified partial", collection: "verified", selector: yamlMappingSelector(yamlSelector("by", yamlString("person:a")))},
		{name: "parameter wrong key", collection: "parameters", selector: yamlMappingSelector(yamlSelector("type", yamlString("string")))},
		{name: "arbitrary extension route", collection: "custom", selector: yamlMappingSelector(yamlSelector("id", yamlString("extension")))},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Act.
			item, found, selectErr := p.selectSequenceItem(mappingValuesForTest(t, p.root, test.collection)[0], test.selector)

			// Assert.
			if item != nil || found || !errors.Is(selectErr, ErrUnsupportedPresentation) {
				t.Fatalf("item=%v found=%v err=%v", item, found, selectErr)
			}
			var presentation *PresentationError
			if !errors.As(selectErr, &presentation) || presentation.Code != yamlCodeUnsupportedSelector {
				t.Fatalf("error = %#v", selectErr)
			}
		})
	}
}

func TestYAMLMutation_EnsureRejectsSelectorDesiredDrift(t *testing.T) {
	// Arrange.
	source := []byte("---\ntype: Knowledge\nsources:\n  - id: old\n    resource: old.md\n---\n")
	p, err := parsePresentationContext(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	sources := mappingValuesForTest(t, p.root, "sources")[0]

	// Act.
	updated, err := applyYAMLMutationForTest(p, func(m *yamlMutation) error {
		return m.ensureSequenceItem(
			sources,
			yamlMappingSelector(yamlSelector("id", yamlString("old"))),
			yamlMapping(yamlEntry("id", yamlString("new")), yamlEntry("resource", yamlString("new.md"))),
		)
	})

	// Assert.
	assertStructuralPresentationError(t, updated, err, ErrUnsupportedPresentation, yamlCodeSelectorMismatch, len(source))
}

func TestYAMLMutation_BatchStateRejectsDuplicatesAndCoalescesDistinctInserts(t *testing.T) {
	t.Run("duplicate mapping key", func(t *testing.T) {
		// Arrange.
		source := []byte("---\ntype: Knowledge\n---\n")
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		mutation := p.newYAMLMutationContext(p.ctx)

		// Act.
		firstErr := mutation.insertMappingValue(p.root, "generated", yamlString("a"))
		secondErr := mutation.insertMappingValue(p.root, "generated", yamlString("b"))
		updated, applyErr := mutation.apply()

		// Assert.
		if firstErr != nil || !errors.Is(secondErr, ErrAmbiguousPresentation) {
			t.Fatalf("first=%v second=%v", firstErr, secondErr)
		}
		if updated != nil || !errors.Is(applyErr, ErrAmbiguousPresentation) {
			t.Fatalf("apply=%v updated=%q", applyErr, updated)
		}
	})

	t.Run("distinct same-boundary inserts", func(t *testing.T) {
		// Arrange.
		source := []byte("---\ntype: Knowledge\n---\n")
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		mutation := p.newYAMLMutationContext(p.ctx)

		// Act.
		firstErr := mutation.insertMappingValue(p.root, "generated", yamlBool(true))
		secondErr := mutation.insertMappingValue(p.root, "status", yamlString("stable"))
		updated, applyErr := mutation.apply()

		// Assert.
		if firstErr != nil || secondErr != nil || applyErr != nil {
			t.Fatalf("first=%v second=%v apply=%v", firstErr, secondErr, applyErr)
		}
		if !bytes.Contains(updated, []byte("generated: true\nstatus: stable\n")) {
			t.Fatalf("same-boundary inserts not deterministic:\n%s", updated)
		}
	})

	t.Run("duplicate ensure is noop", func(t *testing.T) {
		// Arrange.
		source := []byte("---\ntype: Knowledge\nsources: []\n---\n")
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		sources := mappingValuesForTest(t, p.root, "sources")[0]
		selector := yamlMappingSelector(yamlSelector("id", yamlString("a")))
		desired := yamlMapping(yamlEntry("id", yamlString("a")), yamlEntry("resource", yamlString("a.md")))
		mutation := p.newYAMLMutationContext(p.ctx)

		// Act.
		firstErr := mutation.ensureSequenceItem(sources, selector, desired)
		secondErr := mutation.ensureSequenceItem(sources, selector, desired)
		updated, applyErr := mutation.apply()

		// Assert.
		if firstErr != nil || secondErr != nil || applyErr != nil {
			t.Fatalf("first=%v second=%v apply=%v", firstErr, secondErr, applyErr)
		}
		if bytes.Count(updated, []byte("id: a")) != 1 {
			t.Fatalf("duplicate item generated:\n%s", updated)
		}
	})

	t.Run("distinct flow ensures share one replacement", func(t *testing.T) {
		// Arrange.
		source := []byte("---\ntype: Knowledge\nsources: []\n---\n")
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		sources := mappingValuesForTest(t, p.root, "sources")[0]
		mutation := p.newYAMLMutationContext(p.ctx)

		// Act.
		firstErr := mutation.ensureSequenceItem(
			sources,
			yamlMappingSelector(yamlSelector("id", yamlString("a"))),
			yamlMapping(yamlEntry("id", yamlString("a"))),
		)
		secondErr := mutation.ensureSequenceItem(
			sources,
			yamlMappingSelector(yamlSelector("id", yamlString("b"))),
			yamlMapping(yamlEntry("id", yamlString("b"))),
		)
		updated, applyErr := mutation.apply()

		// Assert.
		if firstErr != nil || secondErr != nil || applyErr != nil {
			t.Fatalf("first=%v second=%v apply=%v", firstErr, secondErr, applyErr)
		}
		if bytes.Count(updated, []byte("id: a")) != 1 || bytes.Count(updated, []byte("id: b")) != 1 {
			t.Fatalf("distinct flow items not preserved:\n%s", updated)
		}
	})
}

func TestYAMLMutation_SelectorNoopStillRequiresFullProvenance(t *testing.T) {
	fixtures := []struct {
		name string
		item string
		kind error
		code string
	}{
		{
			name: "alias",
			item: "base: &base\n  resource: anonymous.md\nsources:\n  - resource: *base\n",
			kind: ErrAmbiguousPresentation,
			code: yamlCodeAliasProvenance,
		},
		{
			name: "anchor",
			item: "sources:\n  - resource: &owned anonymous.md\n",
			kind: ErrUnsupportedPresentation,
			code: yamlCodeTouchedAnchor,
		},
		{
			name: "comment",
			item: "sources:\n  - resource: anonymous.md # unknown\n",
			kind: ErrUnsupportedPresentation,
			code: yamlCodeUnownedComment,
		},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			// Arrange.
			source := []byte("---\ntype: Knowledge\n" + fixture.item + "---\n")
			p, err := parsePresentationContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			sources := mappingValuesForTest(t, p.root, "sources")[0]
			desired := yamlMapping(yamlEntry("resource", yamlString("anonymous.md")))

			// Act.
			updated, err := applyYAMLMutationForTest(p, func(m *yamlMutation) error { return m.ensureSequenceItem(sources, yamlExactSelector(desired), desired) })

			// Assert.
			assertStructuralPresentationError(t, updated, err, fixture.kind, fixture.code, len(source))
		})
	}
}

func TestYAMLMutation_DeleteCompactSequenceMappingEntryPreservesDash(t *testing.T) {
	t.Run("first of several entries", func(t *testing.T) {
		// Arrange.
		source := []byte("---\ntype: Knowledge\nsources:\n  - id: remove\n    resource: 'keep.md'\n---\n")
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		item := mappingValuesForTest(t, p.root, "sources")[0].Content[0]

		// Act.
		updated, err := applyYAMLMutationForTest(p, func(m *yamlMutation) error { return m.deleteMappingValue(item, "id") })

		// Assert.
		if err != nil {
			t.Fatal(err)
		}
		want := []byte("---\ntype: Knowledge\nsources:\n  - resource: 'keep.md'\n---\n")
		if !bytes.Equal(updated, want) {
			t.Fatalf("updated:\n%s\nwant:\n%s", updated, want)
		}
	})

	t.Run("only entry becomes empty mapping", func(t *testing.T) {
		// Arrange.
		source := []byte("---\ntype: Knowledge\nsources:\n  - id: remove\n---\n")
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		item := mappingValuesForTest(t, p.root, "sources")[0].Content[0]

		// Act.
		updated, err := applyYAMLMutationForTest(p, func(m *yamlMutation) error { return m.deleteMappingValue(item, "id") })

		// Assert.
		if err != nil {
			t.Fatal(err)
		}
		want := []byte("---\ntype: Knowledge\nsources:\n  - {}\n---\n")
		if !bytes.Equal(updated, want) {
			t.Fatalf("updated:\n%s\nwant:\n%s", updated, want)
		}
	})
}

func TestYAMLMutation_FlowPlainHashIsDataNotComment(t *testing.T) {
	// Arrange.
	source := []byte("---\ntype: Knowledge\nsources: [{resource: https://example.test/doc#fragment}]\nx: keep\n---\n")
	p, err := parsePresentationContext(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	sources := mappingValuesForTest(t, p.root, "sources")[0]

	// Act.
	updated, err := applyYAMLMutationForTest(p, func(m *yamlMutation) error {
		return m.replaceCollectionValue(sources, yamlSequence(
			yamlMapping(yamlEntry("resource", yamlString("https://example.test/next#part"))),
		))
	})

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(updated, []byte("https://example.test/next#part")) || !bytes.Contains(updated, []byte("\nx: keep\n")) {
		t.Fatalf("unexpected flow replacement:\n%s", updated)
	}
}

func TestYAMLMutation_ComplexMappingKeysNeverOwnMutationPaths(t *testing.T) {
	operations := []struct {
		name      string
		selection bool
		run       func(*yamlMutation, *yaml.Node) error
	}{
		{
			name: "replace collection",
			run: func(mutation *yamlMutation, key *yaml.Node) error {
				return mutation.replaceCollectionValue(key, yamlMapping(yamlEntry("name", yamlString("replacement"))))
			},
		},
		{
			name: "remove sequence item",
			run: func(mutation *yamlMutation, key *yaml.Node) error {
				return mutation.removeSequenceItem(key, yamlExactSelector(yamlMapping()))
			},
		},
		{
			name: "replace mapping entry",
			run: func(mutation *yamlMutation, key *yaml.Node) error {
				return mutation.replaceMappingValue(key, "name", yamlString("replacement"))
			},
		},
		{
			name: "insert mapping entry",
			run: func(mutation *yamlMutation, key *yaml.Node) error {
				return mutation.insertMappingValue(key, "added", yamlString("replacement"))
			},
		},
		{
			name: "delete mapping entry",
			run: func(mutation *yamlMutation, key *yaml.Node) error {
				return mutation.deleteMappingValue(key, "name")
			},
		},
		{
			name:      "select sequence item",
			selection: true,
			run: func(mutation *yamlMutation, key *yaml.Node) error {
				_, _, err := mutation.p.selectSequenceItem(
					key,
					yamlMappingSelector(yamlSelector("id", yamlString("selected"))),
				)
				return err
			},
		},
		{
			name: "ensure sequence item",
			run: func(mutation *yamlMutation, key *yaml.Node) error {
				return mutation.ensureSequenceItem(
					key,
					yamlMappingSelector(yamlSelector("id", yamlString("selected"))),
					yamlMapping(yamlEntry("id", yamlString("selected"))),
				)
			},
		},
		{
			name: "remove sequence item",
			run: func(mutation *yamlMutation, key *yaml.Node) error {
				return mutation.removeSequenceItem(
					key,
					yamlMappingSelector(yamlSelector("id", yamlString("selected"))),
				)
			},
		},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			// Arrange.
			source := []byte("---\n? {name: selector}\n: selected\nordinary: keep\n---\nBody\n")
			p, err := parsePresentationContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			complexKey := p.root.Content[0]
			if complexKey.Kind != yaml.MappingNode {
				t.Fatalf("complex key kind = %d, want mapping", complexKey.Kind)
			}
			mutation := p.newYAMLMutationContext(p.ctx)

			// Act.
			mutationErr := operation.run(mutation, complexKey)
			updated, applyErr := mutation.apply()

			// Assert.
			var presentation *PresentationError
			if operation.selection {
				if mutationErr == nil || applyErr != nil || !bytes.Equal(updated, source) || len(mutation.patches) != 0 ||
					!errors.Is(mutationErr, ErrUnsupportedPresentation) ||
					!errors.As(mutationErr, &presentation) || presentation.Code != yamlCodeUnsupportedKey ||
					presentation.Location.Start < 0 || presentation.Location.Start >= presentation.Location.End ||
					presentation.Location.End > len(p.yaml) {
					t.Fatalf("selection error/apply=%#v/%#v bytes=%q patches=%d presentation=%#v",
						mutationErr, applyErr, updated, len(mutation.patches), presentation)
				}
				return
			}
			if mutationErr == nil || mutationErr != applyErr || updated != nil || len(mutation.patches) != 0 ||
				!errors.Is(mutationErr, ErrUnsupportedPresentation) ||
				!errors.As(mutationErr, &presentation) || presentation.Code != yamlCodeUnsupportedKey ||
				presentation.Location.Start < 0 || presentation.Location.Start >= presentation.Location.End ||
				presentation.Location.End > len(p.yaml) {
				t.Fatalf("error/apply=%#v/%#v bytes=%q patches=%d presentation=%#v",
					mutationErr, applyErr, updated, len(mutation.patches), presentation)
			}
		})
	}
}

func TestYAMLMutation_UnindexedMappingChildNeverOwnsMutationPath(t *testing.T) {
	// Arrange.
	source := []byte("---\ntype: Knowledge\n---\nBody\n")
	p, err := parsePresentationContext(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	detached := &yaml.Node{
		Kind:   yaml.MappingNode,
		Tag:    "!!map",
		Line:   p.root.Line,
		Column: p.root.Column,
	}
	p.parents[detached] = p.root
	mutation := p.newYAMLMutationContext(p.ctx)

	// Act.
	mutationErr := mutation.replaceCollectionValue(detached, yamlMapping())
	updated, applyErr := mutation.apply()

	// Assert.
	var presentation *PresentationError
	if mutationErr == nil || mutationErr != applyErr || updated != nil || len(mutation.patches) != 0 ||
		!errors.Is(mutationErr, ErrUnsupportedPresentation) ||
		!errors.As(mutationErr, &presentation) || presentation.Code != "unowned_touched_node" ||
		presentation.Location.Start < 0 || presentation.Location.Start >= presentation.Location.End ||
		presentation.Location.End > len(p.yaml) {
		t.Fatalf("error/apply=%#v/%#v bytes=%q patches=%d presentation=%#v",
			mutationErr, applyErr, updated, len(mutation.patches), presentation)
	}
}

func TestYAMLMutation_ConcurrentIndependentPlanningSurvivesCallerMutation(t *testing.T) {
	// Arrange.
	source := []byte("---\ntype: Knowledge\ncount: 1\n---\nBody\n")
	snapshot := append([]byte(nil), source...)
	p, err := parsePresentationContext(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if len(source) == 0 || len(p.data) == 0 || &source[0] == &p.data[0] {
		t.Fatal("presentation retained caller-owned backing storage")
	}
	const planners = 8
	const iterations = 100
	start := make(chan struct{})
	errs := make(chan error, planners)
	var workers sync.WaitGroup
	workers.Add(planners + 1)

	// Act.
	go func() {
		defer workers.Done()
		<-start
		for i := 0; i < 10_000; i++ {
			source[len(source)-1] ^= 1
		}
	}()
	for worker := 0; worker < planners; worker++ {
		desired := int64(worker + 2)
		go func() {
			defer workers.Done()
			<-start
			for iteration := 0; iteration < iterations; iteration++ {
				updated, planningErr := p.replaceMappingValue(p.root, "count", yamlInt(desired))
				if planningErr != nil {
					errs <- planningErr
					return
				}
				want := []byte("count: " + strconv.FormatInt(desired, 10) + "\n")
				if !bytes.Contains(updated, want) {
					errs <- fmt.Errorf("independent plan for %d produced %q", desired, updated)
					return
				}
			}
		}()
	}
	close(start)
	workers.Wait()
	close(errs)

	// Assert.
	for workerErr := range errs {
		t.Error(workerErr)
	}
	if !bytes.Equal(p.data, snapshot) {
		t.Fatalf("retained presentation changed: %q", p.data)
	}
}

func TestYAMLMutation_MixedSequenceAndInvalidUTF8FailClosed(t *testing.T) {
	t.Run("mixed sequence", func(t *testing.T) {
		// Arrange.
		source := []byte("---\ntype: Knowledge\nsources:\n  - id: a\n  - scalar\n---\n")
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		sources := mappingValuesForTest(t, p.root, "sources")[0]

		// Act.
		updated, err := applyYAMLMutationForTest(p, func(m *yamlMutation) error {
			return m.ensureSequenceItem(
				sources,
				yamlMappingSelector(yamlSelector("id", yamlString("b"))),
				yamlMapping(yamlEntry("id", yamlString("b"))),
			)
		})

		// Assert.
		assertStructuralPresentationError(t, updated, err, ErrUnsupportedPresentation, yamlCodeUnsupportedCollection, len(source))
	})

	t.Run("invalid UTF-8", func(t *testing.T) {
		// Arrange.
		source := []byte("---\ntype: Know")
		source = append(source, 0xff)
		source = append(source, []byte("ledge\n---\n")...)

		// Act.
		p, err := parsePresentationContext(context.Background(), source)

		// Assert.
		if p != nil || err == nil {
			t.Fatalf("presentation=%v err=%v", p, err)
		}
		var presentation *PresentationError
		if !errors.As(err, &presentation) || presentation.Code != "invalid_encoding" ||
			presentation.Location.Start < 0 || presentation.Location.End > len(source) ||
			presentation.Location.Start >= presentation.Location.End {
			t.Fatalf("typed error = %#v", err)
		}
	})
}

func assertStructuralPresentationError(t *testing.T, updated []byte, err, kind error, code string, sourceLength int) {
	t.Helper()
	if updated != nil || !errors.Is(err, kind) {
		t.Fatalf("updated=%q err=%v, want %v", updated, err, kind)
	}
	var presentation *PresentationError
	if !errors.As(err, &presentation) || presentation.Code != code {
		t.Fatalf("typed error = %#v, want code %q", err, code)
	}
	if presentation.Location.Start < 0 || presentation.Location.End > sourceLength ||
		presentation.Location.Start >= presentation.Location.End {
		t.Fatalf("out-of-bounds location %#v for %d-byte source", presentation.Location, sourceLength)
	}
}

func FuzzYAMLStructuralMutation(f *testing.F) {
	f.Add([]byte("---\ntype: Knowledge\nsources: [{resource: https://example.test/a#b}]\n---\nBody\n"))
	f.Add([]byte("---\r\ntype: Knowledge\r\nsources:\r\n  - id: a\r\n    resource: 'a.md'\r\n---\r\n"))
	f.Add([]byte("---\ntype: Knowledge\nsources:\n  - scalar\n---\n"))

	f.Fuzz(func(t *testing.T, source []byte) {
		// Arrange.
		caller := append([]byte(nil), source...)
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			return
		}
		sources := mappingValuesForTest(t, p.root, "sources")
		if len(sources) != 1 {
			return
		}
		desired := yamlSequence(yamlMapping(yamlEntry("resource", yamlString("fuzz.md"))))
		mutation := p.newYAMLMutationContext(p.ctx)

		// Act.
		mutationErr := mutation.replaceCollectionValue(sources[0], desired)
		var updated []byte
		if mutationErr == nil {
			updated, mutationErr = mutation.apply()
		}

		// Assert.
		if !bytes.Equal(source, caller) {
			t.Fatal("caller bytes mutated")
		}
		if mutationErr != nil {
			var presentation *PresentationError
			if errors.As(mutationErr, &presentation) && len(source) > 0 &&
				(presentation.Location.Start < 0 || presentation.Location.End > len(source) ||
					presentation.Location.Start >= presentation.Location.End) {
				t.Fatalf("out-of-bounds rejection: %#v", presentation.Location)
			}
			return
		}
		if updated == nil {
			t.Fatal("successful structural mutation returned nil")
		}
		for _, patch := range mutation.patches {
			if !(SourceSpan{Start: patch.Start, End: patch.End}).valid(len(p.data)) {
				t.Fatalf("out-of-bounds patch: %#v", patch)
			}
		}
		if !bytesOutsidePatchesEqualForTest(t, p.data, updated, mutation.patches) {
			t.Fatal("bytes outside structural spans changed")
		}
		if _, err := parsePresentationContext(context.Background(), updated); err != nil {
			t.Fatalf("generated presentation does not parse: %v", err)
		}
	})
}

func FuzzYAMLMutationComplexKeyOwnership(f *testing.F) {
	seeds := [][]byte{
		[]byte("---\n? {name: selector}\n: selected\nordinary: keep\n---\nBody\n"),
		[]byte("---\n? &owned {name: selector}\n: selected\n---\n"),
		[]byte("---\n? !custom {name: selector}\n: selected\n---\n"),
	}
	for _, seed := range seeds {
		for mode := byte(0); mode < 8; mode++ {
			f.Add(seed, mode)
		}
	}

	f.Fuzz(func(t *testing.T, source []byte, mode byte) {
		// Arrange.
		caller := append([]byte(nil), source...)
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil || p.root.Kind != yaml.MappingNode {
			return
		}
		var complexKey *yaml.Node
		for index := 0; index+1 < len(p.root.Content); index += 2 {
			if p.root.Content[index].Kind == yaml.MappingNode {
				complexKey = p.root.Content[index]
				break
			}
		}
		if complexKey == nil {
			return
		}
		mutation := p.newYAMLMutationContext(p.ctx)

		// Act.
		var mutationErr error
		switch mode % 8 {
		case 0:
			mutationErr = mutation.replaceCollectionValue(
				complexKey,
				yamlMapping(yamlEntry("name", yamlString("replacement"))),
			)
		case 1:
			mutationErr = mutation.removeSequenceItem(complexKey, yamlExactSelector(yamlMapping()))
		case 2:
			mutationErr = mutation.replaceMappingValue(complexKey, "name", yamlString("replacement"))
		case 3:
			mutationErr = mutation.insertMappingValue(complexKey, "added", yamlString("replacement"))
		case 4:
			mutationErr = mutation.deleteMappingValue(complexKey, "name")
		case 5:
			_, _, mutationErr = p.selectSequenceItem(
				complexKey,
				yamlMappingSelector(yamlSelector("id", yamlString("selected"))),
			)
		case 6:
			mutationErr = mutation.ensureSequenceItem(
				complexKey,
				yamlMappingSelector(yamlSelector("id", yamlString("selected"))),
				yamlMapping(yamlEntry("id", yamlString("selected"))),
			)
		case 7:
			mutationErr = mutation.removeSequenceItem(
				complexKey,
				yamlMappingSelector(yamlSelector("id", yamlString("selected"))),
			)
		}
		updated, applyErr := mutation.apply()

		// Assert.
		if !bytes.Equal(source, caller) {
			t.Fatal("caller bytes mutated")
		}
		var presentation *PresentationError
		if mode%8 == 5 {
			if mutationErr == nil || applyErr != nil || !bytes.Equal(updated, source) || len(mutation.patches) != 0 ||
				!errors.Is(mutationErr, ErrUnsupportedPresentation) ||
				!errors.As(mutationErr, &presentation) || presentation.Code != yamlCodeUnsupportedKey ||
				presentation.Location.Start < 0 || presentation.Location.Start >= presentation.Location.End ||
				presentation.Location.End > len(p.yaml) {
				t.Fatalf("mode=%d selection error/apply=%#v/%#v bytes=%q patches=%d presentation=%#v",
					mode%8, mutationErr, applyErr, updated, len(mutation.patches), presentation)
			}
			return
		}
		if mutationErr == nil || mutationErr != applyErr || updated != nil || len(mutation.patches) != 0 ||
			!errors.Is(mutationErr, ErrUnsupportedPresentation) ||
			!errors.As(mutationErr, &presentation) || presentation.Code != yamlCodeUnsupportedKey ||
			presentation.Location.Start < 0 || presentation.Location.Start >= presentation.Location.End ||
			presentation.Location.End > len(p.yaml) {
			t.Fatalf("mode=%d error/apply=%#v/%#v bytes=%q patches=%d presentation=%#v",
				mode%8, mutationErr, applyErr, updated, len(mutation.patches), presentation)
		}
	})
}
