package mutation

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"gopkg.in/yaml.v3"
)

func shadowSourcePresentation(t *testing.T, body string) (*presentation, *yaml.Node, []byte) {
	t.Helper()
	source := []byte("---\ntype: Knowledge\n" + body + "---\nBody\n")
	p, err := parsePresentationContext(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	return p, mappingValuesForTest(t, p.root, "sources")[0], source
}

func shadowSourceSelector(id string) yamlSequenceSelector {
	return yamlMappingSelector(yamlSelector("id", yamlString(id)))
}

func shadowSourceValue(id, resource string) yamlRenderValue {
	return yamlMapping(yamlEntry("id", yamlString(id)), yamlEntry("resource", yamlString(resource)))
}

func TestYAMLMutation_ShadowSequenceOperationOrders(t *testing.T) {
	t.Run("unequal same-selector updates conflict", func(t *testing.T) {
		p, sources, _ := shadowSourcePresentation(t, "sources:\n  - id: a\n    resource: old.md\n")
		m := p.newYAMLMutationContext(p.ctx)
		if err := m.ensureSequenceItem(sources, shadowSourceSelector("a"), shadowSourceValue("a", "middle.md")); err != nil {
			t.Fatal(err)
		}
		conflict := m.ensureSequenceItem(sources, shadowSourceSelector("a"), shadowSourceValue("a", "final.md"))
		got, applyErr := m.apply()
		if conflict == nil || conflict != applyErr || got != nil || !errors.Is(conflict, ErrUnsupportedPresentation) {
			t.Fatalf("conflict/apply = %#v / %#v, bytes=%q", conflict, applyErr, got)
		}
	})

	t.Run("unequal same-selector flow updates conflict", func(t *testing.T) {
		p, sources, _ := shadowSourcePresentation(t, "sources: [{id: a, resource: old.md}]\n")
		m := p.newYAMLMutationContext(p.ctx)
		if err := m.ensureSequenceItem(sources, shadowSourceSelector("a"), shadowSourceValue("a", "middle.md")); err != nil {
			t.Fatal(err)
		}
		conflict := m.ensureSequenceItem(sources, shadowSourceSelector("a"), shadowSourceValue("a", "final.md"))
		got, applyErr := m.apply()
		if conflict == nil || conflict != applyErr || got != nil || !errors.Is(conflict, ErrUnsupportedPresentation) {
			t.Fatalf("conflict/apply = %#v / %#v, bytes=%q", conflict, applyErr, got)
		}
	})

	for _, appendFirst := range []bool{false, true} {
		name := "update-then-append"
		if appendFirst {
			name = "append-then-update"
		}
		t.Run(name, func(t *testing.T) {
			p, sources, _ := shadowSourcePresentation(t, "sources:\n  - id: a\n    resource: old.md\n")
			m := p.newYAMLMutationContext(p.ctx)
			update := func() error {
				return m.ensureSequenceItem(sources, shadowSourceSelector("a"), shadowSourceValue("a", "new.md"))
			}
			appendItem := func() error {
				return m.ensureSequenceItem(sources, shadowSourceSelector("b"), shadowSourceValue("b", "b.md"))
			}
			if appendFirst {
				err := appendItem()
				if err == nil {
					err = update()
				}
				if err != nil {
					t.Fatal(err)
				}
			} else {
				err := update()
				if err == nil {
					err = appendItem()
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			got, err := m.apply()
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Count(got, []byte("id: a")) != 1 || bytes.Count(got, []byte("id: b")) != 1 ||
				!bytes.Contains(got, []byte("resource: new.md")) {
				t.Fatalf("append/update order mismatch:\n%s", got)
			}
		})
	}

	for _, order := range []string{"a-then-b", "b-then-a"} {
		t.Run("remove remove "+order, func(t *testing.T) {
			p, sources, _ := shadowSourcePresentation(t, "sources:\n  - id: a\n    resource: a.md\n  - id: b\n    resource: b.md\n")
			m := p.newYAMLMutationContext(p.ctx)
			ids := []string{"a", "b"}
			if order == "b-then-a" {
				ids[0], ids[1] = ids[1], ids[0]
			}
			for _, id := range ids {
				if err := m.removeSequenceItem(sources, shadowSourceSelector(id)); err != nil {
					t.Fatal(err)
				}
			}
			got, err := m.apply()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(got, []byte("sources:\n  []")) || bytes.Contains(got, []byte("resource:")) {
				t.Fatalf("empty shadow was not rendered deterministically:\n%s", got)
			}
		})
	}

	t.Run("remove then replace resurrects once", func(t *testing.T) {
		p, sources, _ := shadowSourcePresentation(t, "sources:\n  - id: a\n    resource: old.md\n")
		m := p.newYAMLMutationContext(p.ctx)
		if err := m.removeSequenceItem(sources, shadowSourceSelector("a")); err != nil {
			t.Fatal(err)
		}
		if err := m.ensureSequenceItem(sources, shadowSourceSelector("a"), shadowSourceValue("a", "new.md")); err != nil {
			t.Fatal(err)
		}
		got, err := m.apply()
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Count(got, []byte("id: a")) != 1 || !bytes.Contains(got, []byte("resource: new.md")) {
			t.Fatalf("resurrected item mismatch:\n%s", got)
		}
	})

	t.Run("append then remove cancels exactly", func(t *testing.T) {
		p, sources, source := shadowSourcePresentation(t, "sources:\n  - id: a\n    resource: a.md\n")
		m := p.newYAMLMutationContext(p.ctx)
		if err := m.ensureSequenceItem(sources, shadowSourceSelector("b"), shadowSourceValue("b", "b.md")); err != nil {
			t.Fatal(err)
		}
		if err := m.removeSequenceItem(sources, shadowSourceSelector("b")); err != nil {
			t.Fatal(err)
		}
		got, err := m.apply()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, source) {
			t.Fatalf("cancelled append changed bytes:\n%s", got)
		}
	})

	t.Run("bare normalize then remove renders empty sequence", func(t *testing.T) {
		source := []byte("---\ntype: Knowledge\nverified:\n  by: person:a\n  at: 2026-01-02T03:04:05Z\n---\n")
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		verified := mappingValuesForTest(t, p.root, "verified")[0]
		at := yamlDateTimeForTest(t, "2026-01-02T03:04:05Z")
		selector := yamlMappingSelector(yamlSelector("by", yamlString("person:a")), yamlSelector("at", at))
		m := p.newYAMLMutationContext(p.ctx)
		if err := m.removeSequenceItem(verified, selector); err != nil {
			t.Fatal(err)
		}
		got, err := m.apply()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(got, []byte("verified:\n  []")) {
			t.Fatalf("bare empty sequence mismatch:\n%s", got)
		}
	})
}

func TestYAMLMutation_TemporalSelectorCanonicalization(t *testing.T) {
	for _, token := range []string{"'2026-01-02T03:04:05Z'", "2026-01-02T03:04:05Z"} {
		t.Run(token, func(t *testing.T) {
			source := []byte("---\ntype: Knowledge\nverified:\n  - by: person:a\n    at: " + token + "\n---\n")
			p, err := parsePresentationContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			verified := mappingValuesForTest(t, p.root, "verified")[0]
			at := yamlDateTimeForTest(t, "2026-01-02T03:04:05+00:00")
			desired := yamlMapping(yamlEntry("by", yamlString("person:a")), yamlEntry("at", at))
			got, err := applyYAMLMutationForTest(p, func(m *yamlMutation) error {
				return m.ensureSequenceItem(verified,
					yamlMappingSelector(yamlSelector("by", yamlString("person:a")), yamlSelector("at", at)),
					desired,
				)
			})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, source) || bytes.Count(got, []byte("person:a")) != 1 {
				t.Fatalf("temporal equivalent duplicated or rewrote item:\n%s", got)
			}
		})
	}

	t.Run("anonymous exact source quoted date", func(t *testing.T) {
		p, sources, source := shadowSourcePresentation(t, "sources:\n  - resource: policy.md\n    last_modified: '2026-01-02'\n")
		date := yamlDateForTest(t, "2026-01-02")
		desired := yamlMapping(
			yamlEntry("resource", yamlString("policy.md")),
			yamlEntry("last_modified", date),
		)
		got, err := applyYAMLMutationForTest(p, func(m *yamlMutation) error { return m.ensureSequenceItem(sources, yamlExactSelector(desired), desired) })
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, source) || bytes.Count(got, []byte("resource: policy.md")) != 1 {
			t.Fatalf("quoted date produced duplicate anonymous source:\n%s", got)
		}
	})
}

func TestYAMLMutation_PoisonAndRepeatedScalarReplacement(t *testing.T) {
	t.Run("first error poisons every later mutation and apply", func(t *testing.T) {
		source := []byte("---\ntype: Knowledge\ncount: 1\n---\n")
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		m := p.newYAMLMutationContext(p.ctx)
		first := m.deleteMappingValue(p.root, "missing")
		if first == nil {
			t.Fatal("expected first error")
		}
		beforePatches := len(m.patches)
		later := m.replaceMappingValue(p.root, "count", yamlInt(2))
		got, applyErr := m.apply()
		if later != first || applyErr != first || got != nil || len(m.patches) != beforePatches {
			t.Fatalf("poison mismatch: first=%p later=%p apply=%p bytes=%q patches=%d/%d",
				first, later, applyErr, got, beforePatches, len(m.patches))
		}
	})

	t.Run("same scalar coalesces to last desired", func(t *testing.T) {
		source := []byte("---\ntype: Knowledge\ncount: 1\n---\n")
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		m := p.newYAMLMutationContext(p.ctx)
		if err := m.replaceMappingValue(p.root, "count", yamlInt(2)); err != nil {
			t.Fatal(err)
		}
		if err := m.replaceMappingValue(p.root, "count", yamlInt(3)); err != nil {
			t.Fatal(err)
		}
		got, err := m.apply()
		if err != nil {
			t.Fatal(err)
		}
		if len(m.patches) != 1 || !bytes.Contains(got, []byte("count: 3")) || bytes.Contains(got, []byte("count: 2")) {
			t.Fatalf("replacement was not coalesced: patches=%d\n%s", len(m.patches), got)
		}
	})
}

func TestYAMLMutation_FieldSelectorPreservesOpaqueSiblingComment(t *testing.T) {
	source := []byte("---\ntype: Knowledge\nsources:\n  - id: a\n    resource: a.md\n    x-window: keep # opaque sibling\n---\n")
	p, err := parsePresentationContext(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	sources := mappingValuesForTest(t, p.root, "sources")[0]
	item, found, err := p.selectSequenceItem(sources, shadowSourceSelector("a"))
	if err != nil || !found || item == nil {
		t.Fatalf("scoped selector failed: found=%v err=%v", found, err)
	}
	if _, _, err := p.selectSequenceItem(sources, yamlExactSelector(shadowSourceValue("a", "a.md"))); !errors.Is(err, ErrUnsupportedPresentation) {
		t.Fatalf("exact selector must retain whole-value ownership, got %v", err)
	}
}

func TestYAMLMutation_LastFlowItemWholeFamilyDelete(t *testing.T) {
	p, sources, _ := shadowSourcePresentation(t, "sources:\n  - {id: only, resource: only.md}\n")
	if _, err := p.resolver.flowCollection(sources.Content[0]); err != nil {
		t.Fatalf("flow source ownership: %#v", err)
	}
	m := p.newYAMLMutationContext(p.ctx)
	if err := m.deleteMappingValue(p.root, "sources"); err != nil {
		t.Fatalf("stage whole-family delete: %#v", err)
	}
	got, err := m.apply()
	if err != nil {
		t.Fatalf("apply whole-family delete: %#v", err)
	}
	if bytes.Contains(got, []byte("sources:")) {
		t.Fatalf("sources family remains:\n%s", got)
	}
}

func TestYAMLMutation_DeleteAndInsertAtSameBoundaryBothOrders(t *testing.T) {
	for _, insertFirst := range []bool{false, true} {
		name := "delete-first"
		if insertFirst {
			name = "insert-first"
		}
		t.Run(name, func(t *testing.T) {
			source := []byte("---\ntype: Knowledge\ntimestamp: 2026-01-01\n---\n")
			p, err := parsePresentationContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			m := p.newYAMLMutationContext(p.ctx)
			insert := func() error {
				return m.insertMappingValue(p.root, "generated", yamlMapping(yamlEntry("by", yamlString("person:a"))))
			}
			remove := func() error { return m.deleteMappingValue(p.root, "timestamp") }
			if insertFirst {
				err = insert()
				if err == nil {
					err = remove()
				}
			} else {
				err = remove()
				if err == nil {
					err = insert()
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := m.apply()
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(got, []byte("timestamp:")) || bytes.Count(got, []byte("generated:")) != 1 {
				t.Fatalf("boundary merge mismatch:\n%s", got)
			}
		})
	}
}

func TestYAMLMutation_UnifiedMappingAndCollectionState(t *testing.T) {
	t.Run("delete then insert same key", func(t *testing.T) {
		source := []byte("---\ntype: Knowledge\nstatus: old\nx: keep\n---\n")
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		m := p.newYAMLMutationContext(p.ctx)
		if err := m.deleteMappingValue(p.root, "status"); err != nil {
			t.Fatal(err)
		}
		if err := m.insertMappingValue(p.root, "status", yamlString("new")); err != nil {
			t.Fatal(err)
		}
		got, err := m.apply()
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Count(got, []byte("status:")) != 1 || !bytes.Contains(got, []byte("status: new")) {
			t.Fatalf("current mapping state mismatch:\n%s", got)
		}
	})

	t.Run("insert then delete cancels", func(t *testing.T) {
		source := []byte("---\ntype: Knowledge\n---\n")
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		m := p.newYAMLMutationContext(p.ctx)
		if err := m.insertMappingValue(p.root, "status", yamlString("new")); err != nil {
			t.Fatal(err)
		}
		if err := m.deleteMappingValue(p.root, "status"); err != nil {
			t.Fatal(err)
		}
		got, err := m.apply()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, source) {
			t.Fatalf("cancelled mapping mutation changed bytes:\n%s", got)
		}
	})

	for _, replaceFirst := range []bool{true, false} {
		name := "replace then selector"
		if !replaceFirst {
			name = "selector then replace"
		}
		t.Run(name, func(t *testing.T) {
			p, sources, _ := shadowSourcePresentation(t, "sources:\n  - id: a\n    resource: a.md\n")
			m := p.newYAMLMutationContext(p.ctx)
			replacement := yamlSequence(shadowSourceValue("b", "b.md"))
			appendC := func() error {
				return m.ensureSequenceItem(sources, shadowSourceSelector("c"), shadowSourceValue("c", "c.md"))
			}
			replace := func() error { return m.replaceCollectionValue(sources, replacement) }
			if replaceFirst {
				if err := replace(); err != nil {
					t.Fatal(err)
				}
				if err := appendC(); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := appendC(); err != nil {
					t.Fatal(err)
				}
				if err := replace(); err != nil {
					t.Fatal(err)
				}
			}
			got, err := m.apply()
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Count(got, []byte("id: b")) != 1 || bytes.Contains(got, []byte("id: a")) ||
				bytes.Count(got, []byte("id: c")) != 1 {
				t.Fatalf("collection/selector state mismatch:\n%s", got)
			}
		})
	}
}

func TestYAMLMutation_OpaqueUntouchedSequenceSemantics(t *testing.T) {
	source := []byte("---\ntype: Knowledge\nsources:\n" +
		"  - id: a\n    resource: a.md\n" +
		"  - id: opaque\n    resource: opaque.md\n    enabled: TRUE\n    mask: 0x10\n    ratio: 1.20\n    absent: ~\n    observed: 2026-01-02 03:04:05+00:00\n---\n")
	for _, operation := range []string{"append", "update"} {
		t.Run(operation, func(t *testing.T) {
			p, err := parsePresentationContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			sources := mappingValuesForTest(t, p.root, "sources")[0]
			m := p.newYAMLMutationContext(p.ctx)
			if operation == "append" {
				err = m.ensureSequenceItem(sources, shadowSourceSelector("b"), shadowSourceValue("b", "b.md"))
			} else {
				err = m.ensureSequenceItem(sources, shadowSourceSelector("a"), shadowSourceValue("a", "new.md"))
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := m.apply()
			if err != nil {
				t.Fatal(err)
			}
			for _, exact := range [][]byte{
				[]byte("enabled: TRUE"), []byte("mask: 0x10"), []byte("ratio: 1.20"),
				[]byte("absent: ~"), []byte("observed: 2026-01-02 03:04:05+00:00"),
			} {
				if !bytes.Contains(got, exact) {
					t.Fatalf("opaque lexical value %q changed:\n%s", exact, got)
				}
			}
		})
	}
}

func TestYAMLMutation_CRLFNestedAndFlowSafety(t *testing.T) {
	t.Run("nested renderer uses CRLF", func(t *testing.T) {
		source := []byte("---\r\ntype: Knowledge\r\nsettings:\r\n  enabled: true\r\n---\r\n")
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		settings := mappingValuesForTest(t, p.root, "settings")[0]
		m := p.newYAMLMutationContext(p.ctx)
		if err := m.insertMappingValue(settings, "nested", yamlMapping(yamlEntry("name", yamlString("value")))); err != nil {
			t.Fatal(err)
		}
		got, err := m.apply()
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(bytes.ReplaceAll(got, []byte("\r\n"), nil), []byte("\n")) ||
			!bytes.Contains(got, []byte("  nested:\r\n    name: value\r\n")) {
			t.Fatalf("mixed newline output:\n%q", got)
		}
	})

	t.Run("flow keys and values quote flow punctuation", func(t *testing.T) {
		source := []byte("---\ntype: Knowledge\nsettings: {old: value}\n---\n")
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		settings := mappingValuesForTest(t, p.root, "settings")[0]
		m := p.newYAMLMutationContext(p.ctx)
		if err := m.insertMappingValue(settings, "a,b[c]{d}", yamlString("x,y[z]{q}")); err != nil {
			t.Fatal(err)
		}
		got, err := m.apply()
		if err != nil {
			t.Fatal(err)
		}
		after, err := parsePresentationContext(context.Background(), got)
		if err != nil {
			t.Fatal(err)
		}
		rendered, err := yamlRenderFromNodeContext(context.Background(), mappingValuesForTest(t, after.root, "settings")[0])
		if err != nil {
			t.Fatal(err)
		}
		value, found := yamlRenderMappingValue(rendered, "a,b[c]{d}")
		if !found || value.kind != yamlRenderString || value.text != "x,y[z]{q}" {
			t.Fatalf("flow punctuation changed semantics: %#v\n%s", value, got)
		}
	})
}

func TestYAMLMutation_AnchoredOwningSequenceFailsBeforeMutation(t *testing.T) {
	source := []byte("---\ntype: Knowledge\nsources: &owned\n  - id: a\n    resource: a.md\n---\n")
	p, err := parsePresentationContext(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	sources := mappingValuesForTest(t, p.root, "sources")[0]
	got, err := applyYAMLMutationForTest(p, func(m *yamlMutation) error {
		return m.ensureSequenceItem(sources, shadowSourceSelector("b"), shadowSourceValue("b", "b.md"))
	})
	assertStructuralPresentationError(t, got, err, ErrUnsupportedPresentation, yamlCodeTouchedAnchor, len(source))
}

func TestYAMLMutation_DiagnosticAnchorsAreOwnedAndNonEmpty(t *testing.T) {
	fixtures := []struct {
		name   string
		source []byte
		root   bool
	}{
		{name: "scalar null at start", source: []byte("---\nnull\n---"), root: true},
		{name: "scalar string at start", source: []byte("---\nvalue\n---"), root: true},
		{name: "scalar int at start", source: []byte("---\n42\n---"), root: true},
		{name: "value at eof boundary", source: []byte("---\nsources: \n---")},
		{name: "crlf value boundary", source: []byte("---\r\nsources: \r\n---\r\n")},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			// Arrange.

			// Act.
			p, err := parsePresentationContext(context.Background(), fixture.source)
			if fixture.root {
				// Assert. A scalar root has no structural ownership domain.
				var presentation *PresentationError
				if p != nil || !errors.Is(err, ErrUnsupportedPresentation) ||
					!errors.As(err, &presentation) || presentation.Code != "invalid_frontmatter" ||
					presentation.Location.Start < 0 || presentation.Location.Start >= presentation.Location.End ||
					presentation.Location.End > len(fixture.source) {
					t.Fatalf("presentation/error = %#v / %#v", p, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			target := p.root
			values := mappingValuesForTest(t, p.root, "sources")
			if len(values) != 1 {
				t.Fatalf("sources = %#v", values)
			}
			target = values[0]
			got, err := applyYAMLMutationForTest(p, func(m *yamlMutation) error { return m.replaceCollectionValue(target, yamlSequence()) })

			// Assert.
			var presentation *PresentationError
			if got != nil || !errors.Is(err, ErrUnsupportedPresentation) || !errors.As(err, &presentation) ||
				presentation.Location.Start < 0 || presentation.Location.Start >= presentation.Location.End ||
				presentation.Location.End > len(p.yaml) {
				t.Fatalf("bytes/error = %q / %#v", got, err)
			}
		})
	}

	t.Run("terminal alias", func(t *testing.T) {
		source := []byte("---\nbase: &base value\ntarget: *base\n---\n")
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		target := mappingValuesForTest(t, p.root, "target")[0]
		got, err := applyYAMLMutationForTest(p, func(m *yamlMutation) error { return m.replaceCollectionValue(target, yamlSequence()) })
		var presentation *PresentationError
		if got != nil || !errors.Is(err, ErrAmbiguousPresentation) || !errors.As(err, &presentation) ||
			presentation.Location.Start < 0 || presentation.Location.Start >= presentation.Location.End ||
			presentation.Location.End > len(p.yaml) {
			t.Fatalf("bytes/error = %q / %#v", got, err)
		}
	})
}

func TestYAMLMutation_MultiShadowMaterializationIsDeterministic(t *testing.T) {
	source := []byte("---\ntype: Knowledge\nsources:\n" +
		"  - id: a\n    resource: a.md\n    parameters: []\n---\n")
	var baseline []byte
	for iteration := 0; iteration < 40; iteration++ {
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		sources := mappingValuesForTest(t, p.root, "sources")[0]
		parameters := mappingValuesForTest(t, sources.Content[0], "parameters")[0]
		m := p.newYAMLMutationContext(p.ctx)
		parameter := yamlMapping(
			yamlEntry("name", yamlString("input")),
			yamlEntry("type", yamlString("string")),
		)
		if err := m.ensureSequenceItem(parameters,
			yamlMappingSelector(yamlSelector("name", yamlString("input"))),
			parameter,
		); err != nil {
			t.Fatal(err)
		}
		if err := m.ensureSequenceItem(sources, shadowSourceSelector("b"), shadowSourceValue("b", "b.md")); err != nil {
			t.Fatal(err)
		}
		got, err := m.apply()
		if err != nil {
			t.Fatal(err)
		}
		if iteration == 0 {
			baseline = got
		} else if !bytes.Equal(got, baseline) {
			t.Fatalf("iteration %d changed materialization order:\n%s\nbaseline:\n%s", iteration, got, baseline)
		}
	}
}

func TestYAMLMutation_GlobalSameBoundaryScheduler(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		newline := newline
		label := "lf"
		if newline == "\r\n" {
			label = "crlf"
		}
		for _, outerFirst := range []bool{false, true} {
			order := "descendant-first-call"
			if outerFirst {
				order = "ancestor-first-call"
			}
			t.Run("mapping-mapping/"+label+"/"+order, func(t *testing.T) {
				source := []byte("---" + newline + "type: Knowledge" + newline + "settings:" + newline + "  existing: yes" + newline + "---" + newline)
				p, err := parsePresentationContext(context.Background(), source)
				if err != nil {
					t.Fatal(err)
				}
				settings := mappingValuesForTest(t, p.root, "settings")[0]
				m := p.newYAMLMutationContext(p.ctx)
				ancestor := func() error { return m.insertMappingValue(p.root, "status", yamlString("stable")) }
				descendant := func() error { return m.insertMappingValue(settings, "nested", yamlString("value")) }
				if outerFirst {
					err = ancestor()
					if err == nil {
						err = descendant()
					}
				} else {
					err = descendant()
					if err == nil {
						err = ancestor()
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				got, err := m.apply()
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Contains(got, []byte("  nested: value"+newline+"status: stable"+newline)) {
					t.Fatalf("descendant was not emitted before ancestor:\n%q", got)
				}
			})
		}
	}

	for _, outerFirst := range []bool{false, true} {
		order := "sequence-first"
		if outerFirst {
			order = "mapping-first"
		}
		t.Run("mapping-sequence/"+order, func(t *testing.T) {
			source := []byte("---\ntype: Knowledge\nsources:\n  - id: a\n    resource: a.md\n    metadata:\n      existing: yes\n---\n")
			p, err := parsePresentationContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			sources := mappingValuesForTest(t, p.root, "sources")[0]
			metadata := mappingValuesForTest(t, sources.Content[0], "metadata")[0]
			m := p.newYAMLMutationContext(p.ctx)
			mappingInsert := func() error { return m.insertMappingValue(metadata, "nested", yamlString("value")) }
			sequenceInsert := func() error {
				return m.ensureSequenceItem(sources, shadowSourceSelector("b"), shadowSourceValue("b", "b.md"))
			}
			if outerFirst {
				err = mappingInsert()
				if err == nil {
					err = sequenceInsert()
				}
			} else {
				err = sequenceInsert()
				if err == nil {
					err = mappingInsert()
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := m.apply()
			if err != nil {
				t.Fatal(err)
			}
			after, parseErr := parsePresentationContext(context.Background(), got)
			if parseErr != nil {
				t.Fatalf("mapping/sequence boundary corruption: %v\n%s", parseErr, got)
			}
			afterSources := mappingValuesForTest(t, after.root, "sources")[0]
			itemB, found, selectErr := after.selectSequenceItem(afterSources, shadowSourceSelector("b"))
			metadataAfter := mappingValuesForTest(t, afterSources.Content[0], "metadata")[0]
			nested := mappingValuesForTest(t, metadataAfter, "nested")
			if selectErr != nil || !found || itemB == nil || len(nested) != 1 || nested[0].Value != "value" {
				t.Fatalf("mapping/sequence semantic mismatch: found=%v err=%v\n%s", found, selectErr, got)
			}
		})
	}

	for _, outerFirst := range []bool{false, true} {
		order := "inner-first"
		if outerFirst {
			order = "outer-first"
		}
		t.Run("sequence-sequence/"+order, func(t *testing.T) {
			source := []byte("---\ntype: Knowledge\nsources:\n  - id: a\n    resource: a.md\n    parameters:\n      - name: old\n        type: string\n---\n")
			p, err := parsePresentationContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			sources := mappingValuesForTest(t, p.root, "sources")[0]
			parameters := mappingValuesForTest(t, sources.Content[0], "parameters")[0]
			m := p.newYAMLMutationContext(p.ctx)
			inner := func() error {
				return m.ensureSequenceItem(parameters,
					yamlMappingSelector(yamlSelector("name", yamlString("new"))),
					yamlMapping(yamlEntry("name", yamlString("new")), yamlEntry("type", yamlString("string"))),
				)
			}
			outer := func() error {
				return m.ensureSequenceItem(sources, shadowSourceSelector("b"), shadowSourceValue("b", "b.md"))
			}
			if outerFirst {
				err = outer()
				if err == nil {
					err = inner()
				}
			} else {
				err = inner()
				if err == nil {
					err = outer()
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := m.apply()
			if err != nil {
				t.Fatal(err)
			}
			after, parseErr := parsePresentationContext(context.Background(), got)
			if parseErr != nil {
				t.Fatalf("sequence/sequence boundary corruption: %v\n%s", parseErr, got)
			}
			afterSources := mappingValuesForTest(t, after.root, "sources")[0]
			_, foundSource, sourceErr := after.selectSequenceItem(afterSources, shadowSourceSelector("b"))
			afterParameters := mappingValuesForTest(t, afterSources.Content[0], "parameters")[0]
			_, foundParameter, parameterErr := after.selectSequenceItem(afterParameters,
				yamlMappingSelector(yamlSelector("name", yamlString("new"))),
			)
			if sourceErr != nil || parameterErr != nil || !foundSource || !foundParameter {
				t.Fatalf("sequence/sequence semantic mismatch: source=%v/%v parameter=%v/%v\n%s",
					foundSource, sourceErr, foundParameter, parameterErr, got)
			}
		})
	}

	t.Run("flow collection plus eof insertion", func(t *testing.T) {
		source := []byte("---\ntype: Knowledge\nsources: [{id: a, resource: a.md}]\n---")
		for _, sequenceFirst := range []bool{false, true} {
			p, err := parsePresentationContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			sources := mappingValuesForTest(t, p.root, "sources")[0]
			m := p.newYAMLMutationContext(p.ctx)
			sequence := func() error {
				return m.ensureSequenceItem(sources, shadowSourceSelector("b"), shadowSourceValue("b", "b.md"))
			}
			mapping := func() error { return m.insertMappingValue(p.root, "status", yamlString("stable")) }
			if sequenceFirst {
				err = sequence()
				if err == nil {
					err = mapping()
				}
			} else {
				err = mapping()
				if err == nil {
					err = sequence()
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := m.apply()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := parsePresentationContext(context.Background(), got); err != nil {
				t.Fatalf("flow/eof output invalid: %v\n%s", err, got)
			}
		}
	})
}

func TestYAMLMutation_WholeReplacementSupersedesDescendants(t *testing.T) {
	t.Run("child edit conflicts with parent deletion", func(t *testing.T) {
		source := []byte("---\ntype: Knowledge\nsettings:\n  count: 1\n  nested:\n    enabled: false\n---\n")
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		settings := mappingValuesForTest(t, p.root, "settings")[0]
		nested := mappingValuesForTest(t, settings, "nested")[0]
		m := p.newYAMLMutationContext(p.ctx)
		if err := m.replaceMappingValue(nested, "enabled", yamlBool(true)); err != nil {
			t.Fatal(err)
		}
		desired := yamlMapping(yamlEntry("final", yamlString("owned")))
		conflict := m.replaceCollectionValue(settings, desired)
		got, applyErr := m.apply()
		if conflict == nil || conflict != applyErr || got != nil || !errors.Is(conflict, ErrUnsupportedPresentation) {
			t.Fatalf("conflict/apply = %#v / %#v, bytes=%q", conflict, applyErr, got)
		}
	})

	t.Run("whole then child rebases into desired subtree", func(t *testing.T) {
		source := []byte("---\ntype: Knowledge\nsettings:\n  nested:\n    enabled: false\n---\n")
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		settings := mappingValuesForTest(t, p.root, "settings")[0]
		nested := mappingValuesForTest(t, settings, "nested")[0]
		m := p.newYAMLMutationContext(p.ctx)
		if err := m.replaceCollectionValue(settings, yamlMapping(
			yamlEntry("nested", yamlMapping(yamlEntry("enabled", yamlBool(false)))),
			yamlEntry("marker", yamlString("parent")),
		)); err != nil {
			t.Fatal(err)
		}
		if err := m.replaceMappingValue(nested, "enabled", yamlBool(true)); err != nil {
			t.Fatal(err)
		}
		got, err := m.apply()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(got, []byte("enabled: true")) || !bytes.Contains(got, []byte("marker: parent")) {
			t.Fatalf("child was not rebased into parent desired subtree:\n%s", got)
		}
	})

	t.Run("nested sequence append conflicts with parent deletion", func(t *testing.T) {
		source := []byte("---\ntype: Knowledge\nsettings:\n  parameters:\n    - name: old\n      type: string\n---\n")
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		settings := mappingValuesForTest(t, p.root, "settings")[0]
		parameters := mappingValuesForTest(t, settings, "parameters")[0]
		m := p.newYAMLMutationContext(p.ctx)
		if err := m.ensureSequenceItem(parameters,
			yamlMappingSelector(yamlSelector("name", yamlString("new"))),
			yamlMapping(yamlEntry("name", yamlString("new")), yamlEntry("type", yamlString("string"))),
		); err != nil {
			t.Fatal(err)
		}
		conflict := m.replaceCollectionValue(settings, yamlMapping(yamlEntry("final", yamlString("owned"))))
		got, applyErr := m.apply()
		if conflict == nil || conflict != applyErr || got != nil || !errors.Is(conflict, ErrUnsupportedPresentation) {
			t.Fatalf("conflict/apply = %#v / %#v, bytes=%q", conflict, applyErr, got)
		}
	})

	t.Run("whole sequence item update conflicts with same-leaf granular edit", func(t *testing.T) {
		p, sources, _ := shadowSourcePresentation(t, "sources:\n  - id: a\n    resource: old.md\n")
		item := sources.Content[0]
		m := p.newYAMLMutationContext(p.ctx)
		if err := m.ensureSequenceItem(sources, shadowSourceSelector("a"), shadowSourceValue("a", "new.md")); err != nil {
			t.Fatal(err)
		}
		conflict := m.replaceMappingValue(item, "resource", yamlString("late.md"))
		got, applyErr := m.apply()
		if conflict == nil || conflict != applyErr || got != nil || !errors.Is(conflict, ErrUnsupportedPresentation) {
			t.Fatalf("conflict/apply = %#v / %#v, bytes=%q", conflict, applyErr, got)
		}
	})

	t.Run("whole sequence item update rebases disjoint granular edit", func(t *testing.T) {
		p, sources, _ := shadowSourcePresentation(t, "sources:\n  - id: a\n    resource: old.md\n")
		item := sources.Content[0]
		m := p.newYAMLMutationContext(p.ctx)
		if err := m.ensureSequenceItem(sources, shadowSourceSelector("a"), shadowSourceValue("a", "new.md")); err != nil {
			t.Fatal(err)
		}
		if err := m.insertMappingValue(item, "title", yamlString("kept")); err != nil {
			t.Fatal(err)
		}
		got, err := m.apply()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(got, []byte("resource: new.md")) || !bytes.Contains(got, []byte("title: kept")) {
			t.Fatalf("disjoint edit was not rebased into sequence item desired value:\n%s", got)
		}
	})

	t.Run("removed sequence item rejects later source-node edit", func(t *testing.T) {
		p, sources, _ := shadowSourcePresentation(t, "sources:\n  - id: a\n    resource: old.md\n")
		item := sources.Content[0]
		m := p.newYAMLMutationContext(p.ctx)
		if err := m.removeSequenceItem(sources, shadowSourceSelector("a")); err != nil {
			t.Fatal(err)
		}
		childErr := m.replaceMappingValue(item, "resource", yamlString("late.md"))
		got, applyErr := m.apply()
		if childErr == nil || childErr != applyErr || got != nil || !errors.Is(childErr, ErrUnsupportedPresentation) {
			t.Fatalf("child/apply = %#v / %#v, bytes=%q", childErr, applyErr, got)
		}
		var presentation *PresentationError
		if !errors.As(childErr, &presentation) || presentation.Code != yamlCodeOverlappingPatch ||
			presentation.Location.Start >= presentation.Location.End {
			t.Fatalf("typed error = %#v", childErr)
		}
	})

	t.Run("removed sequence item then parent whole supersedes barrier", func(t *testing.T) {
		p, sources, _ := shadowSourcePresentation(t, "sources:\n  - id: a\n    resource: old.md\n")
		m := p.newYAMLMutationContext(p.ctx)
		if err := m.removeSequenceItem(sources, shadowSourceSelector("a")); err != nil {
			t.Fatal(err)
		}
		if err := m.replaceCollectionValue(sources, yamlSequence(shadowSourceValue("b", "b.md"))); err != nil {
			t.Fatal(err)
		}
		got, err := m.apply()
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(got, []byte("id: a")) || !bytes.Contains(got, []byte("id: b")) {
			t.Fatalf("parent whole did not supersede removed child:\n%s", got)
		}
	})
}

func TestYAMLMutation_WholeReplacementOrderEquivalenceMatrix(t *testing.T) {
	fixtures := []struct {
		name   string
		source []byte
	}{
		{
			name: "three-level block lf",
			source: []byte("---\ntype: Knowledge\nsettings:\n  mode: old\n  nested:\n    enabled: false\n    parameters: []\n---\n" +
				"BODY MUST STAY BYTE-EXACT\n"),
		},
		{
			name: "three-level flow crlf",
			source: []byte("---\r\ntype: Knowledge\r\nsettings: {mode: old, nested: {enabled: false, parameters: []}}\r\n---\r\n" +
				"BODY MUST STAY BYTE-EXACT\r\n"),
		},
	}

	run := func(t *testing.T, source []byte, parentFirst bool) []byte {
		t.Helper()
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		settings := mappingValuesForTest(t, p.root, "settings")[0]
		nested := mappingValuesForTest(t, settings, "nested")[0]
		parameters := mappingValuesForTest(t, nested, "parameters")[0]
		whole, err := yamlRenderFromNodeIgnoringCommentsContext(context.Background(), settings)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := setRenderMappingKeyContext(context.Background(), &whole, "mode", yamlString("stable"), true); err != nil {
			t.Fatal(err)
		}
		parameter := yamlMapping(
			yamlEntry("name", yamlString("input")),
			yamlEntry("type", yamlString("string")),
		)
		parent := func(m *yamlMutation) error {
			return m.replaceCollectionValue(settings, whole)
		}
		descendants := func(m *yamlMutation) error {
			if err := m.replaceMappingValue(nested, "enabled", yamlBool(true)); err != nil {
				return err
			}
			return m.ensureSequenceItem(parameters,
				yamlMappingSelector(yamlSelector("name", yamlString("input"))),
				parameter,
			)
		}
		m := p.newYAMLMutationContext(p.ctx)
		if parentFirst {
			err = parent(m)
			if err == nil {
				err = descendants(m)
			}
		} else {
			err = descendants(m)
			if err == nil {
				err = parent(m)
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		got, err := m.apply()
		if err != nil {
			t.Fatal(err)
		}
		bodyMarker := []byte("BODY MUST STAY BYTE-EXACT")
		sourceBody := bytes.Index(source, bodyMarker)
		gotBody := bytes.Index(got, bodyMarker)
		if sourceBody < 0 || gotBody < 0 {
			t.Fatalf("unowned body marker missing:\n%s", got)
		}
		if !bytes.Equal(got[gotBody:], source[sourceBody:]) {
			t.Fatalf("unowned body changed:\nsource suffix=%q\ngot suffix=%q", source[sourceBody:], got[gotBody:])
		}
		after, err := parsePresentationContext(context.Background(), got)
		if err != nil {
			t.Fatal(err)
		}
		afterSettings := mappingValuesForTest(t, after.root, "settings")[0]
		afterNested := mappingValuesForTest(t, afterSettings, "nested")[0]
		afterParameters := mappingValuesForTest(t, afterNested, "parameters")[0]
		_, found, selectErr := after.selectSequenceItem(afterParameters,
			yamlMappingSelector(yamlSelector("name", yamlString("input"))),
		)
		enabled := mappingValuesForTest(t, afterNested, "enabled")
		mode := mappingValuesForTest(t, afterSettings, "mode")
		if selectErr != nil || !found || len(enabled) != 1 || enabled[0].Value != "true" ||
			len(mode) != 1 || mode[0].Value != "stable" {
			t.Fatalf("folded semantics mismatch: found=%v err=%v\n%s", found, selectErr, got)
		}
		return got
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			nestedFirst := run(t, fixture.source, false)
			parentFirst := run(t, fixture.source, true)
			left, leftErr := parsePresentationContext(context.Background(), nestedFirst)
			right, rightErr := parsePresentationContext(context.Background(), parentFirst)
			if leftErr != nil || rightErr != nil || !semanticPresentationsEqualForTest(t, left.root, right.root) {
				t.Fatalf("operation orders diverged: left=%v right=%v\nnested-first:\n%s\nparent-first:\n%s",
					leftErr, rightErr, nestedFirst, parentFirst)
			}
			if !bytes.Equal(nestedFirst, parentFirst) {
				t.Fatalf("operation orders changed bytes:\nnested-first:\n%q\nparent-first:\n%q", nestedFirst, parentFirst)
			}
			replayedNested := run(t, nestedFirst, false)
			replayedParent := run(t, parentFirst, true)
			if !bytes.Equal(replayedNested, nestedFirst) || !bytes.Equal(replayedParent, parentFirst) {
				t.Fatalf("replay was not idempotent:\nfirst:\n%q\nnested replay:\n%q\nparent replay:\n%q",
					nestedFirst, replayedNested, replayedParent)
			}
		})
	}
}

func TestYAMLMutation_StableSequenceIdentityMatrix(t *testing.T) {
	atA := yamlDateTimeForTest(t, "2026-01-02T03:04:05Z")
	atB := yamlDateTimeForTest(t, "2026-02-03T04:05:06Z")
	fixtures := []struct {
		name      string
		source    []byte
		key       string
		selectorA yamlSequenceSelector
		selectorB yamlSequenceSelector
	}{
		{
			name:      "sources id",
			source:    []byte("---\ntype: Knowledge\nsources:\n  - id: a\n    resource: a.md\n  - id: b\n    resource: b.md\n---\nBODY\n"),
			key:       "sources",
			selectorA: shadowSourceSelector("a"),
			selectorB: shadowSourceSelector("b"),
		},
		{
			name: "verified by-at",
			source: []byte("---\ntype: Knowledge\nverified:\n" +
				"  - by: person:a\n    at: 2026-01-02T03:04:05Z\n" +
				"  - by: person:b\n    at: 2026-02-03T04:05:06Z\n---\nBODY\n"),
			key: "verified",
			selectorA: yamlMappingSelector(
				yamlSelector("by", yamlString("person:a")),
				yamlSelector("at", atA),
			),
			selectorB: yamlMappingSelector(
				yamlSelector("by", yamlString("person:b")),
				yamlSelector("at", atB),
			),
		},
		{
			name: "parameters name",
			source: []byte("---\ntype: Knowledge\nparameters:\n" +
				"  - name: a\n    type: string\n" +
				"  - name: b\n    type: integer\n---\nBODY\n"),
			key:       "parameters",
			selectorA: yamlMappingSelector(yamlSelector("name", yamlString("a"))),
			selectorB: yamlMappingSelector(yamlSelector("name", yamlString("b"))),
		},
	}

	run := func(t *testing.T, fixture struct {
		name      string
		source    []byte
		key       string
		selectorA yamlSequenceSelector
		selectorB yamlSequenceSelector
	}, source []byte, parentFirst bool) []byte {
		t.Helper()
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		collection := mappingValuesForTest(t, p.root, fixture.key)[0]
		itemA, foundA, errA := p.selectSequenceItem(collection, fixture.selectorA)
		itemB, foundB, errB := p.selectSequenceItem(collection, fixture.selectorB)
		if errA != nil || errB != nil || !foundA || !foundB {
			t.Fatalf("identity selection failed: a=%v/%v b=%v/%v", foundA, errA, foundB, errB)
		}
		valueA, err := yamlRenderFromNodeIgnoringCommentsContext(context.Background(), itemA)
		if err != nil {
			t.Fatal(err)
		}
		valueB, err := yamlRenderFromNodeIgnoringCommentsContext(context.Background(), itemB)
		if err != nil {
			t.Fatal(err)
		}
		parent := func(m *yamlMutation) error {
			return m.replaceCollectionValue(collection, yamlSequence(valueB, valueA))
		}
		edit := func(m *yamlMutation, item *yaml.Node, value string) error {
			if len(mappingValuesForTest(t, item, "x-lineage")) == 0 {
				return m.insertMappingValue(item, "x-lineage", yamlString(value))
			}
			return m.replaceMappingValue(item, "x-lineage", yamlString(value))
		}
		descendants := func(m *yamlMutation) error {
			if err := edit(m, itemA, "a"); err != nil {
				return err
			}
			return edit(m, itemB, "b")
		}
		m := p.newYAMLMutationContext(p.ctx)
		if parentFirst {
			err = parent(m)
			if err == nil {
				err = descendants(m)
			}
		} else {
			err = descendants(m)
			if err == nil {
				err = parent(m)
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		got, err := m.apply()
		if err != nil {
			t.Fatal(err)
		}
		after, err := parsePresentationContext(context.Background(), got)
		if err != nil {
			t.Fatal(err)
		}
		afterCollection := mappingValuesForTest(t, after.root, fixture.key)[0]
		afterA, foundA, errA := after.selectSequenceItem(afterCollection, fixture.selectorA)
		afterB, foundB, errB := after.selectSequenceItem(afterCollection, fixture.selectorB)
		if errA != nil || errB != nil || !foundA || !foundB ||
			mappingValuesForTest(t, afterA, "x-lineage")[0].Value != "a" ||
			mappingValuesForTest(t, afterB, "x-lineage")[0].Value != "b" {
			t.Fatalf("lineage edits missing after reorder:\n%s", got)
		}
		return got
	}

	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			nestedFirst := run(t, fixture, fixture.source, false)
			parentFirst := run(t, fixture, fixture.source, true)
			if !bytes.Equal(nestedFirst, parentFirst) {
				t.Fatalf("identity route order changed bytes:\nnested:\n%s\nparent:\n%s", nestedFirst, parentFirst)
			}
			if replay := run(t, fixture, nestedFirst, true); !bytes.Equal(replay, nestedFirst) {
				t.Fatalf("identity route replay changed bytes:\nfirst:\n%s\nreplay:\n%s", nestedFirst, replay)
			}
		})
	}

	t.Run("removed identity conflicts with granular edit in both orders", func(t *testing.T) {
		source := []byte("---\ntype: Knowledge\nsources:\n  - id: a\n    resource: a.md\n  - id: b\n    resource: b.md\n---\n")
		for _, parentFirst := range []bool{false, true} {
			p, err := parsePresentationContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			sources := mappingValuesForTest(t, p.root, "sources")[0]
			itemA := selectSequenceItemForTest(t, p, sources, shadowSourceSelector("a"))
			itemB := selectSequenceItemForTest(t, p, sources, shadowSourceSelector("b"))
			valueB := yamlRenderFromNodeForTest(t, itemB)
			m := p.newYAMLMutationContext(p.ctx)
			parent := func() error { return m.replaceCollectionValue(sources, yamlSequence(valueB)) }
			child := func() error { return m.insertMappingValue(itemA, "title", yamlString("conflict")) }
			if parentFirst {
				err = parent()
				if err == nil {
					err = child()
				}
			} else {
				err = child()
				if err == nil {
					err = parent()
				}
			}
			got, applyErr := m.apply()
			if err == nil || err != applyErr || got != nil || !errors.Is(err, ErrUnsupportedPresentation) {
				t.Fatalf("order=%v conflict/apply=%#v/%#v bytes=%q", parentFirst, err, applyErr, got)
			}
		}
	})

	t.Run("same-selector additions coalesce or conflict", func(t *testing.T) {
		source := []byte("---\ntype: Knowledge\nsources: []\n---\n")
		for _, parentFirst := range []bool{false, true} {
			p, err := parsePresentationContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			sources := mappingValuesForTest(t, p.root, "sources")[0]
			desired := shadowSourceValue("c", "c.md")
			m := p.newYAMLMutationContext(p.ctx)
			parent := func() error { return m.replaceCollectionValue(sources, yamlSequence(desired)) }
			child := func() error { return m.ensureSequenceItem(sources, shadowSourceSelector("c"), desired) }
			if parentFirst {
				err = parent()
				if err == nil {
					err = child()
				}
			} else {
				err = child()
				if err == nil {
					err = parent()
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := m.apply()
			if err != nil || bytes.Count(got, []byte("id: c")) != 1 {
				t.Fatalf("identical addition did not coalesce: err=%v\n%s", err, got)
			}
		}

		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		sources := mappingValuesForTest(t, p.root, "sources")[0]
		m := p.newYAMLMutationContext(p.ctx)
		if err := m.ensureSequenceItem(sources, shadowSourceSelector("c"), shadowSourceValue("c", "left.md")); err != nil {
			t.Fatal(err)
		}
		conflict := m.replaceCollectionValue(sources, yamlSequence(shadowSourceValue("c", "right.md")))
		got, applyErr := m.apply()
		if conflict == nil || conflict != applyErr || got != nil || !errors.Is(conflict, ErrUnsupportedPresentation) {
			t.Fatalf("unequal additions conflict/apply=%#v/%#v bytes=%q", conflict, applyErr, got)
		}
	})

	t.Run("outer ensure atomically consumes descendant state", func(t *testing.T) {
		fixtures := [][]byte{
			[]byte("---\ntype: Knowledge\nsources:\n  - id: a\n    resource: old.md\n    metadata:\n      keep: yes\n---\nBODY\n"),
			[]byte("---\r\ntype: Knowledge\r\nsources: [{id: a, resource: old.md, metadata: {keep: yes}}]\r\n---\r\nBODY\r\n"),
		}
		run := func(t *testing.T, source []byte, outerFirst bool) []byte {
			t.Helper()
			p, err := parsePresentationContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			sources := mappingValuesForTest(t, p.root, "sources")[0]
			item := selectSequenceItemForTest(t, p, sources, shadowSourceSelector("a"))
			metadata := mappingValuesForTest(t, item, "metadata")[0]
			desired, err := yamlRenderFromNodeIgnoringCommentsContext(context.Background(), item)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := setRenderMappingKeyContext(context.Background(), &desired, "resource", yamlString("new.md"), true); err != nil {
				t.Fatal(err)
			}
			outer := func(m *yamlMutation) error {
				return m.ensureSequenceItem(sources, shadowSourceSelector("a"), desired)
			}
			child := func(m *yamlMutation) error {
				if len(mappingValuesForTest(t, metadata, "x-child")) == 0 {
					return m.insertMappingValue(metadata, "x-child", yamlString("kept"))
				}
				return m.replaceMappingValue(metadata, "x-child", yamlString("kept"))
			}
			m := p.newYAMLMutationContext(p.ctx)
			if outerFirst {
				err = outer(m)
				if err == nil {
					err = child(m)
				}
			} else {
				err = child(m)
				if err == nil {
					err = outer(m)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := m.apply()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(got, []byte("resource: new.md")) || !bytes.Contains(got, []byte("x-child")) ||
				bytes.Count(got, []byte("x-child")) != 1 {
				t.Fatalf("outer ensure did not atomically consume descendant state:\n%s", got)
			}
			return got
		}
		for _, source := range fixtures {
			nestedFirst := run(t, source, false)
			outerFirst := run(t, source, true)
			if !bytes.Equal(nestedFirst, outerFirst) {
				t.Fatalf("outer ensure order changed bytes:\nnested:\n%q\nouter:\n%q", nestedFirst, outerFirst)
			}
			if replay := run(t, nestedFirst, true); !bytes.Equal(replay, nestedFirst) {
				t.Fatalf("outer ensure replay changed bytes:\nfirst:\n%q\nreplay:\n%q", nestedFirst, replay)
			}
		}
	})

	t.Run("outer remove conflicts with descendant state in both orders", func(t *testing.T) {
		source := []byte("---\ntype: Knowledge\nsources:\n  - id: a\n    resource: a.md\n    metadata: {}\n---\n")
		for _, removeFirst := range []bool{false, true} {
			p, err := parsePresentationContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			sources := mappingValuesForTest(t, p.root, "sources")[0]
			item := selectSequenceItemForTest(t, p, sources, shadowSourceSelector("a"))
			metadata := mappingValuesForTest(t, item, "metadata")[0]
			m := p.newYAMLMutationContext(p.ctx)
			remove := func() error { return m.removeSequenceItem(sources, shadowSourceSelector("a")) }
			child := func() error { return m.insertMappingValue(metadata, "x", yamlString("conflict")) }
			if removeFirst {
				err = remove()
				if err == nil {
					err = child()
				}
			} else {
				err = child()
				if err == nil {
					err = remove()
				}
			}
			got, applyErr := m.apply()
			if err == nil || err != applyErr || got != nil || !errors.Is(err, ErrUnsupportedPresentation) {
				t.Fatalf("order=%v conflict/apply=%#v/%#v bytes=%q", removeFirst, err, applyErr, got)
			}
		}
	})

	t.Run("anonymous exact identity survives reorder without duplication", func(t *testing.T) {
		source := []byte("---\ntype: Knowledge\nsources:\n  - resource: anonymous.md\n  - id: b\n    resource: b.md\n---\n")
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		sources := mappingValuesForTest(t, p.root, "sources")[0]
		anonymous, err := yamlRenderFromNodeIgnoringCommentsContext(context.Background(), sources.Content[0])
		if err != nil {
			t.Fatal(err)
		}
		valueB, err := yamlRenderFromNodeIgnoringCommentsContext(context.Background(), sources.Content[1])
		if err != nil {
			t.Fatal(err)
		}
		m := p.newYAMLMutationContext(p.ctx)
		if err := m.replaceCollectionValue(sources, yamlSequence(valueB, anonymous)); err != nil {
			t.Fatal(err)
		}
		if err := m.ensureSequenceItem(sources, yamlExactSelector(anonymous), anonymous); err != nil {
			t.Fatal(err)
		}
		got, err := m.apply()
		if err != nil || bytes.Count(got, []byte("anonymous.md")) != 1 {
			t.Fatalf("anonymous exact identity duplicated: err=%v\n%s", err, got)
		}
	})
}

func TestYAMLMutation_OrdinaryIdentityMutationMatrix(t *testing.T) {
	assertRejected := func(t *testing.T, m *yamlMutation, err error) {
		t.Helper()
		got, applyErr := m.apply()
		var presentation *PresentationError
		if err == nil || err != applyErr || got != nil || !errors.Is(err, ErrAmbiguousPresentation) ||
			!errors.As(err, &presentation) || presentation.Code != yamlCodeAmbiguousSelector ||
			presentation.Location.Start >= presentation.Location.End || len(m.patches) != 0 {
			t.Fatalf("error/apply=%#v/%#v bytes=%q patches=%d", err, applyErr, got, len(m.patches))
		}
	}

	t.Run("sources replace id duplicate block", func(t *testing.T) {
		p, sources, _ := shadowSourcePresentation(t, "sources:\n  - id: a\n    resource: a.md\n  - id: b\n    resource: b.md\n")
		m := p.newYAMLMutationContext(p.ctx)
		assertRejected(t, m, m.replaceMappingValue(sources.Content[0], "id", yamlString("b")))
	})

	t.Run("sources delete id collides with anonymous exact flow crlf", func(t *testing.T) {
		source := []byte("---\r\ntype: Knowledge\r\nsources: [{id: a, resource: same.md}, {resource: same.md}]\r\n---\r\n")
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		sources := mappingValuesForTest(t, p.root, "sources")[0]
		m := p.newYAMLMutationContext(p.ctx)
		assertRejected(t, m, m.deleteMappingValue(sources.Content[0], "id"))
	})

	t.Run("sources insert id collides from anonymous", func(t *testing.T) {
		p, sources, _ := shadowSourcePresentation(t, "sources:\n  - resource: a.md\n  - id: b\n    resource: b.md\n")
		m := p.newYAMLMutationContext(p.ctx)
		assertRejected(t, m, m.insertMappingValue(sources.Content[0], "id", yamlString("b")))
	})

	t.Run("verified replace and delete identity fields", func(t *testing.T) {
		at := "2026-01-02T03:04:05Z"
		for _, operation := range []string{"replace", "delete"} {
			var source []byte
			if operation == "replace" {
				source = []byte("---\ntype: Knowledge\nverified:\n  - by: person:a\n    at: " + at +
					"\n  - by: person:b\n    at: " + at + "\n---\n")
			} else {
				source = []byte("---\ntype: Knowledge\nverified:\n  - by: person:a\n    at: " + at +
					"\n  - at: " + at + "\n---\n")
			}
			p, err := parsePresentationContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			verified := mappingValuesForTest(t, p.root, "verified")[0]
			m := p.newYAMLMutationContext(p.ctx)
			if operation == "replace" {
				err = m.replaceMappingValue(verified.Content[0], "by", yamlString("person:b"))
			} else {
				err = m.deleteMappingValue(verified.Content[0], "by")
			}
			assertRejected(t, m, err)
		}
	})

	t.Run("parameters replace name duplicate", func(t *testing.T) {
		source := []byte("---\ntype: Knowledge\nparameters: [{name: a, type: string}, {name: b, type: integer}]\n---\n")
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		parameters := mappingValuesForTest(t, p.root, "parameters")[0]
		m := p.newYAMLMutationContext(p.ctx)
		assertRejected(t, m, m.replaceMappingValue(parameters.Content[0], "name", yamlString("b")))
	})

	type identityFixture struct {
		name        string
		source      []byte
		route       string
		locatorKey  string
		locator     string
		identityKey string
		identity    string
		mode        string
	}
	fixtures := []identityFixture{
		{
			name: "sources id rename flow crlf", route: "sources", locatorKey: "resource", locator: "a.md",
			identityKey: "id", identity: "x", mode: "replace",
			source: []byte("---\r\ntype: Knowledge\r\nsources: [{id: a, resource: a.md}, {id: b, resource: b.md}]\r\n---\r\nBODY\r\n"),
		},
		{
			name: "sources anonymous to id", route: "sources", locatorKey: "resource", locator: "a.md",
			identityKey: "id", identity: "x", mode: "insert",
			source: []byte("---\ntype: Knowledge\nsources:\n  - resource: a.md\n  - id: b\n    resource: b.md\n---\nBODY\n"),
		},
		{
			name: "sources id to anonymous", route: "sources", locatorKey: "resource", locator: "a.md",
			identityKey: "id", mode: "delete",
			source: []byte("---\ntype: Knowledge\nsources:\n  - id: a\n    resource: a.md\n  - id: b\n    resource: b.md\n---\nBODY\n"),
		},
		{
			name: "verified by rename", route: "verified", locatorKey: "x-owner", locator: "a",
			identityKey: "by", identity: "person:x", mode: "replace",
			source: []byte("---\ntype: Knowledge\nverified:\n  - by: person:a\n    at: 2026-01-02T03:04:05Z\n    x-owner: a\n" +
				"  - by: person:b\n    at: 2026-02-03T04:05:06Z\n    x-owner: b\n---\nBODY\n"),
		},
		{
			name: "parameters name rename", route: "parameters", locatorKey: "type", locator: "string",
			identityKey: "name", identity: "x", mode: "replace",
			source: []byte("---\ntype: Knowledge\nparameters:\n  - name: a\n    type: string\n  - name: b\n    type: integer\n---\nBODY\n"),
		},
	}

	findItem := func(collection *yaml.Node, key, value string) *yaml.Node {
		for _, item := range collection.Content {
			values := mappingValuesForTest(t, item, key)
			if len(values) == 1 && values[0].Value == value {
				return item
			}
		}
		return nil
	}
	run := func(t *testing.T, fixture identityFixture, source []byte, parentFirst bool) []byte {
		t.Helper()
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		collection := mappingValuesForTest(t, p.root, fixture.route)[0]
		itemA := findItem(collection, fixture.locatorKey, fixture.locator)
		if itemA == nil || len(collection.Content) != 2 {
			t.Fatal("lineage item missing")
		}
		itemB := collection.Content[0]
		if itemB == itemA {
			itemB = collection.Content[1]
		}
		valueA := yamlRenderFromNodeForTest(t, itemA)
		valueB := yamlRenderFromNodeForTest(t, itemB)
		parent := func(m *yamlMutation) error {
			return m.replaceCollectionValue(collection, yamlSequence(valueB, valueA))
		}
		child := func(m *yamlMutation) error {
			switch fixture.mode {
			case "insert":
				if len(mappingValuesForTest(t, itemA, fixture.identityKey)) != 0 {
					return m.replaceMappingValue(itemA, fixture.identityKey, yamlString(fixture.identity))
				}
				return m.insertMappingValue(itemA, fixture.identityKey, yamlString(fixture.identity))
			case "delete":
				if len(mappingValuesForTest(t, itemA, fixture.identityKey)) == 0 {
					return nil
				}
				return m.deleteMappingValue(itemA, fixture.identityKey)
			default:
				return m.replaceMappingValue(itemA, fixture.identityKey, yamlString(fixture.identity))
			}
		}
		m := p.newYAMLMutationContext(p.ctx)
		if parentFirst {
			err = parent(m)
			if err == nil {
				err = child(m)
			}
		} else {
			err = child(m)
			if err == nil {
				err = parent(m)
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		got, err := m.apply()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := parsePresentationContext(context.Background(), got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			childFirst := run(t, fixture, fixture.source, false)
			parentFirst := run(t, fixture, fixture.source, true)
			if !bytes.Equal(childFirst, parentFirst) {
				t.Fatalf("identity/reorder order mismatch:\nchild:\n%q\nparent:\n%q", childFirst, parentFirst)
			}
			if replay := run(t, fixture, childFirst, true); !bytes.Equal(replay, childFirst) {
				t.Fatalf("identity/reorder replay mismatch:\nfirst:\n%q\nreplay:\n%q", childFirst, replay)
			}
		})
	}

	t.Run("whole rename versus granular identity change conflicts both orders", func(t *testing.T) {
		source := []byte("---\ntype: Knowledge\nsources:\n  - id: a\n    resource: a.md\n  - id: b\n    resource: b.md\n---\n")
		for _, parentFirst := range []bool{false, true} {
			p, err := parsePresentationContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			sources := mappingValuesForTest(t, p.root, "sources")[0]
			itemA, itemB := sources.Content[0], sources.Content[1]
			valueA := yamlRenderFromNodeForTest(t, itemA)
			valueB := yamlRenderFromNodeForTest(t, itemB)
			if _, err := setRenderMappingKeyContext(context.Background(), &valueA, "id", yamlString("parent"), true); err != nil {
				t.Fatal(err)
			}
			m := p.newYAMLMutationContext(p.ctx)
			parent := func() error { return m.replaceCollectionValue(sources, yamlSequence(valueB, valueA)) }
			child := func() error { return m.replaceMappingValue(itemA, "id", yamlString("child")) }
			if parentFirst {
				err = parent()
				if err == nil {
					err = child()
				}
			} else {
				err = child()
				if err == nil {
					err = parent()
				}
			}
			got, applyErr := m.apply()
			if err == nil || err != applyErr || got != nil || !errors.Is(err, ErrUnsupportedPresentation) {
				t.Fatalf("order=%v error/apply=%#v/%#v bytes=%q", parentFirst, err, applyErr, got)
			}
		}
	})

	t.Run("ordinary identity override routes later selector", func(t *testing.T) {
		p, sources, _ := shadowSourcePresentation(t, "sources:\n  - id: a\n    resource: a.md\n")
		item := sources.Content[0]
		m := p.newYAMLMutationContext(p.ctx)
		if err := m.replaceMappingValue(item, "id", yamlString("x")); err != nil {
			t.Fatal(err)
		}
		desired := shadowSourceValue("x", "a.md")
		if err := m.ensureSequenceItem(sources, shadowSourceSelector("x"), desired); err != nil {
			t.Fatal(err)
		}
		got, err := m.apply()
		if err != nil || bytes.Count(got, []byte("id: x")) != 1 || bytes.Contains(got, []byte("id: a")) {
			t.Fatalf("identity override did not route selector: err=%v\n%s", err, got)
		}
	})

	t.Run("incompatible concurrent reorders conflict independent of call order", func(t *testing.T) {
		source := []byte("---\ntype: Knowledge\nsources:\n" +
			"  - id: a\n    resource: a.md\n" +
			"  - id: b\n    resource: b.md\n" +
			"  - id: c\n    resource: c.md\n---\n")
		for _, reverse := range []bool{false, true} {
			p, err := parsePresentationContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			sources := mappingValuesForTest(t, p.root, "sources")[0]
			values := make([]yamlRenderValue, len(sources.Content))
			for i, item := range sources.Content {
				values[i] = yamlRenderFromNodeForTest(t, item)
			}
			left := yamlSequence(values[1], values[0], values[2])
			right := yamlSequence(values[0], values[2], values[1])
			if reverse {
				left, right = right, left
			}
			m := p.newYAMLMutationContext(p.ctx)
			if err := m.replaceCollectionValue(sources, left); err != nil {
				t.Fatal(err)
			}
			conflict := m.replaceCollectionValue(sources, right)
			got, applyErr := m.apply()
			if conflict == nil || conflict != applyErr || got != nil || !errors.Is(conflict, ErrUnsupportedPresentation) {
				t.Fatalf("reverse=%v conflict/apply=%#v/%#v bytes=%q", reverse, conflict, applyErr, got)
			}
		}
	})
}

func TestYAMLMutation_GenericSequenceDuplicatesRemainSupported(t *testing.T) {
	fixtures := []yamlRenderValue{
		yamlSequence(yamlString("same"), yamlString("same")),
		yamlSequence(
			yamlMapping(yamlEntry("value", yamlString("same"))),
			yamlMapping(yamlEntry("value", yamlString("same"))),
		),
	}
	for index, desired := range fixtures {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			source := []byte("---\ntype: Knowledge\nvalues: [left, right]\n---\nBODY\n")
			p, err := parsePresentationContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			values := mappingValuesForTest(t, p.root, "values")[0]
			got, err := applyYAMLMutationForTest(p, func(m *yamlMutation) error { return m.replaceCollectionValue(values, desired) })
			if err != nil {
				t.Fatal(err)
			}
			after, err := parsePresentationContext(context.Background(), got)
			if err != nil {
				t.Fatal(err)
			}
			if len(mappingValuesForTest(t, after.root, "values")[0].Content) != 2 || !bytes.HasSuffix(got, []byte("---\nBODY\n")) {
				t.Fatalf("generic duplicate sequence changed structure/body:\n%s", got)
			}
		})
	}
}

func TestYAMLMutation_DuplicateIdentityIngressMatrix(t *testing.T) {
	type fixture struct {
		name      string
		source    []byte
		route     string
		duplicate yamlRenderValue
		unique    yamlRenderValue
	}
	at := "2026-01-02T03:04:05Z"
	fixtures := []fixture{
		{
			name:   "sources id flow crlf",
			source: []byte("---\r\ntype: Knowledge\r\nsources: [{id: a, resource: a.md}, {id: b, resource: b.md}]\r\n---\r\n"),
			route:  "sources",
			duplicate: yamlSequence(
				shadowSourceValue("a", "left.md"),
				shadowSourceValue("a", "right.md"),
			),
			unique: yamlSequence(shadowSourceValue("c", "c.md"), shadowSourceValue("d", "d.md")),
		},
		{
			name:   "sources anonymous exact",
			source: []byte("---\ntype: Knowledge\nsources:\n  - resource: a.md\n  - resource: b.md\n---\n"),
			route:  "sources",
			duplicate: yamlSequence(
				yamlMapping(yamlEntry("resource", yamlString("same.md"))),
				yamlMapping(yamlEntry("resource", yamlString("same.md"))),
			),
			unique: yamlSequence(
				yamlMapping(yamlEntry("resource", yamlString("c.md"))),
				yamlMapping(yamlEntry("resource", yamlString("d.md"))),
			),
		},
		{
			name: "verified block",
			source: []byte("---\ntype: Knowledge\nverified:\n" +
				"  - by: person:a\n    at: " + at + "\n" +
				"  - by: person:b\n    at: " + at + "\n---\n"),
			route: "verified",
			duplicate: yamlSequence(
				yamlMapping(yamlEntry("by", yamlString("person:x")), yamlEntry("at", yamlString(at))),
				yamlMapping(yamlEntry("by", yamlString("person:x")), yamlEntry("at", yamlString(at))),
			),
			unique: yamlSequence(
				yamlMapping(yamlEntry("by", yamlString("person:c")), yamlEntry("at", yamlString(at))),
				yamlMapping(yamlEntry("by", yamlString("person:d")), yamlEntry("at", yamlString(at))),
			),
		},
		{
			name:   "parameters block",
			source: []byte("---\ntype: Knowledge\nparameters:\n  - name: a\n    type: string\n  - name: b\n    type: integer\n---\n"),
			route:  "parameters",
			duplicate: yamlSequence(
				yamlMapping(yamlEntry("name", yamlString("x")), yamlEntry("type", yamlString("string"))),
				yamlMapping(yamlEntry("name", yamlString("x")), yamlEntry("type", yamlString("integer"))),
			),
			unique: yamlSequence(
				yamlMapping(yamlEntry("name", yamlString("c")), yamlEntry("type", yamlString("string"))),
				yamlMapping(yamlEntry("name", yamlString("d")), yamlEntry("type", yamlString("integer"))),
			),
		},
	}
	assertAmbiguous := func(t *testing.T, m *yamlMutation, before int, err error) {
		t.Helper()
		got, applyErr := m.apply()
		var presentation *PresentationError
		if err == nil || err != applyErr || got != nil || len(m.patches) != before ||
			!errors.Is(err, ErrAmbiguousPresentation) || !errors.As(err, &presentation) ||
			presentation.Code != yamlCodeAmbiguousSelector {
			t.Fatalf("error/apply=%#v/%#v bytes=%q patches=%d want=%d", err, applyErr, got, len(m.patches), before)
		}
	}

	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			t.Run("direct", func(t *testing.T) {
				p, err := parsePresentationContext(context.Background(), fixture.source)
				if err != nil {
					t.Fatal(err)
				}
				collection := mappingValuesForTest(t, p.root, fixture.route)[0]
				m := p.newYAMLMutationContext(p.ctx)
				assertAmbiguous(t, m, 0, m.replaceCollectionValue(collection, fixture.duplicate))
			})
			t.Run("second whole", func(t *testing.T) {
				p, err := parsePresentationContext(context.Background(), fixture.source)
				if err != nil {
					t.Fatal(err)
				}
				collection := mappingValuesForTest(t, p.root, fixture.route)[0]
				m := p.newYAMLMutationContext(p.ctx)
				if err := m.replaceCollectionValue(collection, fixture.unique); err != nil {
					t.Fatal(err)
				}
				before := len(m.patches)
				assertAmbiguous(t, m, before, m.replaceCollectionValue(collection, fixture.duplicate))
			})
			t.Run("fold after granular edit", func(t *testing.T) {
				p, err := parsePresentationContext(context.Background(), fixture.source)
				if err != nil {
					t.Fatal(err)
				}
				collection := mappingValuesForTest(t, p.root, fixture.route)[0]
				first := collection.Content[0]
				m := p.newYAMLMutationContext(p.ctx)
				if err := m.insertMappingValue(first, "x-note", yamlString("edited")); err != nil {
					t.Fatal(err)
				}
				before := len(m.patches)
				assertAmbiguous(t, m, before, m.replaceCollectionValue(collection, fixture.duplicate))
			})
		})
	}

	t.Run("nested desired", func(t *testing.T) {
		source := []byte("---\ntype: Knowledge\nconfig:\n  sources:\n    - id: a\n      resource: a.md\n    - id: b\n      resource: b.md\n---\n")
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		config := mappingValuesForTest(t, p.root, "config")[0]
		desired := yamlMapping(yamlEntry("sources", yamlSequence(
			shadowSourceValue("x", "left.md"),
			shadowSourceValue("x", "right.md"),
		)))
		m := p.newYAMLMutationContext(p.ctx)
		assertAmbiguous(t, m, 0, m.replaceCollectionValue(config, desired))
	})

	t.Run("duplicate immutable base rejects noop and unique replacement", func(t *testing.T) {
		for _, desiredUnique := range []bool{false, true} {
			source := []byte("---\ntype: Knowledge\nsources:\n  - id: a\n    resource: left.md\n  - id: a\n    resource: right.md\n---\n")
			p, err := parsePresentationContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			sources := mappingValuesForTest(t, p.root, "sources")[0]
			desired, err := yamlRenderFromNodeIgnoringCommentsContext(context.Background(), sources)
			if err != nil {
				t.Fatal(err)
			}
			if desiredUnique {
				desired = yamlSequence(shadowSourceValue("c", "c.md"))
			}
			m := p.newYAMLMutationContext(p.ctx)
			assertAmbiguous(t, m, 0, m.replaceCollectionValue(sources, desired))
		}
	})
}

func TestYAMLMutation_ImmutableSequenceLineageMatrix(t *testing.T) {
	type fixture struct {
		name       string
		source     []byte
		route      string
		selectorA  yamlSequenceSelector
		selectorC  yamlSequenceSelector
		valueB     yamlRenderValue
		valueC     yamlRenderValue
		wholeEditA yamlRenderValue
	}
	atA, atB, atC := "2026-01-02T03:04:05Z", "2026-02-03T04:05:06Z", "2026-03-04T05:06:07Z"
	fixtures := []fixture{
		{
			name:      "sources id",
			source:    []byte("---\ntype: Knowledge\nsources:\n  - id: a\n    resource: a.md\n  - id: b\n    resource: b.md\n---\n"),
			route:     "sources",
			selectorA: shadowSourceSelector("a"),
			selectorC: shadowSourceSelector("c"),
			valueB:    shadowSourceValue("b", "b.md"),
			valueC:    shadowSourceValue("c", "c.md"),
			wholeEditA: yamlMapping(
				yamlEntry("id", yamlString("a")),
				yamlEntry("resource", yamlString("edited.md")),
			),
		},
		{
			name:   "verified",
			source: []byte("---\r\ntype: Knowledge\r\nverified: [{by: person:a, at: " + atA + ", x-note: old}, {by: person:b, at: " + atB + "}]\r\n---\r\n"),
			route:  "verified",
			selectorA: yamlMappingSelector(
				yamlSelector("by", yamlString("person:a")),
				yamlSelector("at", yamlString(atA)),
			),
			selectorC: yamlMappingSelector(
				yamlSelector("by", yamlString("person:c")),
				yamlSelector("at", yamlString(atC)),
			),
			valueB: yamlMapping(yamlEntry("by", yamlString("person:b")), yamlEntry("at", yamlString(atB))),
			valueC: yamlMapping(yamlEntry("by", yamlString("person:c")), yamlEntry("at", yamlString(atC))),
			wholeEditA: yamlMapping(
				yamlEntry("by", yamlString("person:a")),
				yamlEntry("at", yamlString(atA)),
				yamlEntry("x-note", yamlString("edited")),
			),
		},
		{
			name:      "parameters",
			source:    []byte("---\ntype: Knowledge\nparameters:\n  - name: a\n    type: string\n  - name: b\n    type: integer\n---\n"),
			route:     "parameters",
			selectorA: yamlMappingSelector(yamlSelector("name", yamlString("a"))),
			selectorC: yamlMappingSelector(yamlSelector("name", yamlString("c"))),
			valueB:    yamlMapping(yamlEntry("name", yamlString("b")), yamlEntry("type", yamlString("integer"))),
			valueC:    yamlMapping(yamlEntry("name", yamlString("c")), yamlEntry("type", yamlString("boolean"))),
			wholeEditA: yamlMapping(
				yamlEntry("name", yamlString("a")),
				yamlEntry("type", yamlString("number")),
			),
		},
	}

	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			run := func(t *testing.T, parentFirst, edit bool) ([]byte, error) {
				t.Helper()
				p, err := parsePresentationContext(context.Background(), fixture.source)
				if err != nil {
					t.Fatal(err)
				}
				collection := mappingValuesForTest(t, p.root, fixture.route)[0]
				m := p.newYAMLMutationContext(p.ctx)
				granular := func() error {
					if err := m.removeSequenceItem(collection, fixture.selectorA); err != nil {
						return err
					}
					return m.ensureSequenceItem(collection, fixture.selectorC, fixture.valueC)
				}
				whole := func() error {
					if edit {
						return m.replaceCollectionValue(collection, yamlSequence(fixture.wholeEditA, fixture.valueB))
					}
					return m.replaceCollectionValue(collection, yamlSequence(fixture.valueB))
				}
				if parentFirst {
					err = whole()
					if err == nil {
						err = granular()
					}
				} else {
					err = granular()
					if err == nil {
						err = whole()
					}
				}
				got, applyErr := m.apply()
				if err != nil {
					if err != applyErr || got != nil || !errors.Is(err, ErrUnsupportedPresentation) {
						t.Fatalf("order=%v error/apply=%#v/%#v bytes=%q", parentFirst, err, applyErr, got)
					}
					return nil, err
				}
				if applyErr != nil {
					t.Fatal(applyErr)
				}
				return got, nil
			}

			for _, parentFirst := range []bool{false, true} {
				if got, err := run(t, parentFirst, true); err == nil || got != nil {
					t.Fatalf("edit/remove+add must conflict, order=%v bytes=%q err=%v", parentFirst, got, err)
				}
			}
			childFirst, childErr := run(t, false, false)
			parentFirst, parentErr := run(t, true, false)
			if childErr != nil || parentErr != nil || !bytes.Equal(childFirst, parentFirst) {
				t.Fatalf("compatible removal mismatch: child=%q/%v parent=%q/%v", childFirst, childErr, parentFirst, parentErr)
			}
		})
	}

	t.Run("sources anonymous remove add versus remove", func(t *testing.T) {
		source := []byte("---\ntype: Knowledge\nsources:\n  - resource: a.md\n  - resource: b.md\n---\n")
		valueA := yamlMapping(yamlEntry("resource", yamlString("a.md")))
		valueB := yamlMapping(yamlEntry("resource", yamlString("b.md")))
		valueC := yamlMapping(yamlEntry("resource", yamlString("c.md")))
		var outputs [][]byte
		for _, parentFirst := range []bool{false, true} {
			p, err := parsePresentationContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			sources := mappingValuesForTest(t, p.root, "sources")[0]
			m := p.newYAMLMutationContext(p.ctx)
			granular := func() error {
				if err := m.removeSequenceItem(sources, yamlExactSelector(valueA)); err != nil {
					return err
				}
				return m.ensureSequenceItem(sources, yamlExactSelector(valueC), valueC)
			}
			whole := func() error { return m.replaceCollectionValue(sources, yamlSequence(valueB)) }
			if parentFirst {
				err = whole()
				if err == nil {
					err = granular()
				}
			} else {
				err = granular()
				if err == nil {
					err = whole()
				}
			}
			got, applyErr := m.apply()
			if err != nil || applyErr != nil {
				t.Fatalf("order=%v error/apply=%v/%v", parentFirst, err, applyErr)
			}
			outputs = append(outputs, got)
		}
		if !bytes.Equal(outputs[0], outputs[1]) {
			t.Fatalf("anonymous lineage order mismatch:\n%q\n%q", outputs[0], outputs[1])
		}
	})
}

func TestYAMLMutation_WholeGranularIdenticalMappingOrders(t *testing.T) {
	type operation struct {
		name  string
		mode  string
		key   string
		value yamlRenderValue
		whole yamlRenderValue
	}
	operations := []operation{
		{
			name: "replace",
			mode: "replace",
			key:  "a", value: yamlString("new"),
			whole: yamlMapping(yamlEntry("a", yamlString("new"))),
		},
		{
			name:  "insert",
			mode:  "insert",
			key:   "b",
			value: yamlString("new"),
			whole: yamlMapping(yamlEntry("a", yamlString("old")), yamlEntry("b", yamlString("new"))),
		},
		{
			name:  "delete",
			mode:  "delete",
			key:   "a",
			whole: yamlMapping(),
		},
	}
	mutateMapping := func(m *yamlMutation, mapping *yaml.Node, operation operation) error {
		switch operation.mode {
		case "insert":
			return m.insertMappingValue(mapping, operation.key, operation.value)
		case "delete":
			return m.deleteMappingValue(mapping, operation.key)
		default:
			return m.replaceMappingValue(mapping, operation.key, operation.value)
		}
	}
	run := func(
		t *testing.T,
		source []byte,
		mappingPath []string,
		parentPath []string,
		operation operation,
		parentFirst bool,
	) []byte {
		t.Helper()
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		resolve := func(path []string) *yaml.Node {
			current := p.root
			for _, key := range path {
				values := mappingValuesForTest(t, current, key)
				if len(values) != 1 {
					t.Fatalf("path %v missing at %q", path, key)
				}
				current = values[0]
			}
			return current
		}
		mapping := resolve(mappingPath)
		parent := resolve(parentPath)
		parentDesired := operation.whole
		if len(parentPath) != len(mappingPath) {
			parentDesired = yamlMapping(yamlEntry("inner", operation.whole))
		}
		m := p.newYAMLMutationContext(p.ctx)
		whole := func() error { return m.replaceCollectionValue(parent, parentDesired) }
		child := func() error { return mutateMapping(m, mapping, operation) }
		if parentFirst {
			err = whole()
			if err == nil {
				err = child()
			}
		} else {
			err = child()
			if err == nil {
				err = whole()
			}
		}
		if err != nil {
			t.Fatalf("order=%v operation=%s: %v", parentFirst, operation.name, err)
		}
		got, err := m.apply()
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	for _, operation := range operations {
		operation := operation
		t.Run("root "+operation.name, func(t *testing.T) {
			source := []byte("---\ntype: Knowledge\nconfig:\n  a: old\n---\n")
			childFirst := run(t, source, []string{"config"}, []string{"config"}, operation, false)
			parentFirst := run(t, source, []string{"config"}, []string{"config"}, operation, true)
			if !bytes.Equal(childFirst, parentFirst) {
				t.Fatalf("root order mismatch:\n%q\n%q", childFirst, parentFirst)
			}
		})
		t.Run("nested replacement render "+operation.name, func(t *testing.T) {
			source := []byte("---\ntype: Knowledge\nouter:\n  inner:\n    a: old\n---\n")
			childFirst := run(t, source, []string{"outer", "inner"}, []string{"outer"}, operation, false)
			parentFirst := run(t, source, []string{"outer", "inner"}, []string{"outer"}, operation, true)
			if !bytes.Equal(childFirst, parentFirst) {
				childPresentation, childErr := parsePresentationContext(context.Background(), childFirst)
				parentPresentation, parentErr := parsePresentationContext(context.Background(), parentFirst)
				if childErr != nil || parentErr != nil ||
					!semanticPresentationsEqualForTest(t, childPresentation.root, parentPresentation.root) {
					t.Fatalf("nested order mismatch:\n%q\n%q", childFirst, parentFirst)
				}
			}
		})
	}

	t.Run("replacement shadow nested mapping", func(t *testing.T) {
		source := []byte("---\ntype: Knowledge\nsources:\n  - id: a\n    resource: a.md\n    config:\n      a: old\n---\n")
		desiredItem := yamlMapping(
			yamlEntry("id", yamlString("a")),
			yamlEntry("resource", yamlString("a.md")),
			yamlEntry("config", yamlMapping(yamlEntry("a", yamlString("new")))),
		)
		var outputs [][]byte
		for _, parentFirst := range []bool{false, true} {
			p, err := parsePresentationContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			sources := mappingValuesForTest(t, p.root, "sources")[0]
			config := mappingValuesForTest(t, sources.Content[0], "config")[0]
			m := p.newYAMLMutationContext(p.ctx)
			whole := func() error {
				return m.ensureSequenceItem(sources, shadowSourceSelector("a"), desiredItem)
			}
			child := func() error { return m.replaceMappingValue(config, "a", yamlString("new")) }
			if parentFirst {
				err = whole()
				if err == nil {
					err = child()
				}
			} else {
				err = child()
				if err == nil {
					err = whole()
				}
			}
			if err != nil {
				t.Fatalf("order=%v: %v", parentFirst, err)
			}
			got, err := m.apply()
			if err != nil {
				t.Fatal(err)
			}
			outputs = append(outputs, got)
		}
		if !bytes.Equal(outputs[0], outputs[1]) {
			t.Fatalf("shadow order mismatch:\n%q\n%q", outputs[0], outputs[1])
		}
	})

}

func TestYAMLMutation_MappingEntryUniquenessIngressMatrix(t *testing.T) {
	type fixture struct {
		name            string
		route           string
		uniqueSource    []byte
		duplicateSource []byte
		unique          yamlRenderValue
		duplicate       yamlRenderValue
	}
	at := "2026-01-02T03:04:05Z"
	fixtures := []fixture{
		{
			name:            "sources id",
			route:           "sources",
			uniqueSource:    []byte("---\ntype: Knowledge\nsources:\n  - id: a\n    resource: a.md\n  - id: b\n    resource: b.md\n---\n"),
			duplicateSource: []byte("---\ntype: Knowledge\nsources:\n  - id: a\n    resource: left.md\n  - id: a\n    resource: right.md\n---\n"),
			unique:          yamlSequence(shadowSourceValue("a", "a.md"), shadowSourceValue("b", "b.md")),
			duplicate:       yamlSequence(shadowSourceValue("x", "left.md"), shadowSourceValue("x", "right.md")),
		},
		{
			name:            "sources anonymous exact",
			route:           "sources",
			uniqueSource:    []byte("---\ntype: Knowledge\nsources:\n  - resource: a.md\n  - resource: b.md\n---\n"),
			duplicateSource: []byte("---\ntype: Knowledge\nsources:\n  - resource: same.md\n  - resource: same.md\n---\n"),
			unique: yamlSequence(
				yamlMapping(yamlEntry("resource", yamlString("a.md"))),
				yamlMapping(yamlEntry("resource", yamlString("b.md"))),
			),
			duplicate: yamlSequence(
				yamlMapping(yamlEntry("resource", yamlString("same.md"))),
				yamlMapping(yamlEntry("resource", yamlString("same.md"))),
			),
		},
		{
			name:  "verified",
			route: "verified",
			uniqueSource: []byte("---\ntype: Knowledge\nverified:\n" +
				"  - by: person:a\n    at: " + at + "\n" +
				"  - by: person:b\n    at: " + at + "\n---\n"),
			duplicateSource: []byte("---\ntype: Knowledge\nverified:\n" +
				"  - by: person:a\n    at: " + at + "\n" +
				"  - by: person:a\n    at: " + at + "\n---\n"),
			unique: yamlSequence(
				yamlMapping(yamlEntry("by", yamlString("person:a")), yamlEntry("at", yamlString(at))),
				yamlMapping(yamlEntry("by", yamlString("person:b")), yamlEntry("at", yamlString(at))),
			),
			duplicate: yamlSequence(
				yamlMapping(yamlEntry("by", yamlString("person:x")), yamlEntry("at", yamlString(at))),
				yamlMapping(yamlEntry("by", yamlString("person:x")), yamlEntry("at", yamlString(at))),
			),
		},
		{
			name:            "parameters",
			route:           "parameters",
			uniqueSource:    []byte("---\ntype: Knowledge\nparameters: [{name: a, type: string}, {name: b, type: integer}]\n---\n"),
			duplicateSource: []byte("---\ntype: Knowledge\nparameters: [{name: a, type: string}, {name: a, type: integer}]\n---\n"),
			unique: yamlSequence(
				yamlMapping(yamlEntry("name", yamlString("a")), yamlEntry("type", yamlString("string"))),
				yamlMapping(yamlEntry("name", yamlString("b")), yamlEntry("type", yamlString("integer"))),
			),
			duplicate: yamlSequence(
				yamlMapping(yamlEntry("name", yamlString("x")), yamlEntry("type", yamlString("string"))),
				yamlMapping(yamlEntry("name", yamlString("x")), yamlEntry("type", yamlString("integer"))),
			),
		},
	}
	assertAmbiguous := func(t *testing.T, source []byte, m *yamlMutation, before int, err error) {
		t.Helper()
		got, applyErr := m.apply()
		var presentation *PresentationError
		if err == nil || err != applyErr || got != nil || len(m.patches) != before ||
			!errors.Is(err, ErrAmbiguousPresentation) || !errors.As(err, &presentation) ||
			presentation.Code != yamlCodeAmbiguousSelector ||
			presentation.Location.Start >= presentation.Location.End ||
			presentation.Location.End > len(source) {
			t.Fatalf("error/apply=%#v/%#v bytes=%q patches=%d/%d location=%#v",
				err, applyErr, got, len(m.patches), before, presentation)
		}
	}

	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			t.Run("replace direct block", func(t *testing.T) {
				p, err := parsePresentationContext(context.Background(), fixture.uniqueSource)
				if err != nil {
					t.Fatal(err)
				}
				m := p.newYAMLMutationContext(p.ctx)
				assertAmbiguous(t, fixture.uniqueSource, m, 0,
					m.replaceMappingValue(p.root, fixture.route, fixture.duplicate))
			})
			t.Run("insert direct block", func(t *testing.T) {
				source := []byte("---\ntype: Knowledge\n---\n")
				p, err := parsePresentationContext(context.Background(), source)
				if err != nil {
					t.Fatal(err)
				}
				m := p.newYAMLMutationContext(p.ctx)
				assertAmbiguous(t, source, m, 0,
					m.insertMappingValue(p.root, fixture.route, fixture.duplicate))
			})
			t.Run("delete duplicate immutable base", func(t *testing.T) {
				p, err := parsePresentationContext(context.Background(), fixture.duplicateSource)
				if err != nil {
					t.Fatal(err)
				}
				m := p.newYAMLMutationContext(p.ctx)
				assertAmbiguous(t, fixture.duplicateSource, m, 0,
					m.deleteMappingValue(p.root, fixture.route))
			})
			t.Run("noop duplicate immutable base", func(t *testing.T) {
				p, err := parsePresentationContext(context.Background(), fixture.duplicateSource)
				if err != nil {
					t.Fatal(err)
				}
				duplicate := mappingValuesForTest(t, p.root, fixture.route)[0]
				rendered, err := yamlRenderFromNodeIgnoringCommentsContext(context.Background(), duplicate)
				if err != nil {
					t.Fatal(err)
				}
				m := p.newYAMLMutationContext(p.ctx)
				assertAmbiguous(t, fixture.duplicateSource, m, 0,
					m.replaceMappingValue(p.root, fixture.route, rendered))
			})
			t.Run("unique replacement still rejects duplicate base", func(t *testing.T) {
				p, err := parsePresentationContext(context.Background(), fixture.duplicateSource)
				if err != nil {
					t.Fatal(err)
				}
				m := p.newYAMLMutationContext(p.ctx)
				assertAmbiguous(t, fixture.duplicateSource, m, 0,
					m.replaceMappingValue(p.root, fixture.route, fixture.unique))
			})
			t.Run("append update validates accumulated and desired", func(t *testing.T) {
				source := []byte("---\ntype: Knowledge\n---\n")
				p, err := parsePresentationContext(context.Background(), source)
				if err != nil {
					t.Fatal(err)
				}
				m := p.newYAMLMutationContext(p.ctx)
				if err := m.insertMappingValue(p.root, fixture.route, fixture.unique); err != nil {
					t.Fatal(err)
				}
				before := len(m.patches)
				assertAmbiguous(t, source, m, before,
					m.replaceMappingValue(p.root, fixture.route, fixture.duplicate))
			})
		})
	}

	t.Run("nested mapping desired", func(t *testing.T) {
		source := []byte("---\ntype: Knowledge\nconfig:\n  sources:\n    - id: a\n      resource: a.md\n---\n")
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		desired := yamlMapping(yamlEntry("sources", yamlSequence(
			shadowSourceValue("x", "left.md"),
			shadowSourceValue("x", "right.md"),
		)))
		m := p.newYAMLMutationContext(p.ctx)
		assertAmbiguous(t, source, m, 0, m.replaceMappingValue(p.root, "config", desired))
	})

	t.Run("nested duplicate base delete", func(t *testing.T) {
		source := []byte("---\ntype: Knowledge\nconfig:\n  parameters:\n    - name: a\n    - name: a\n---\n")
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		m := p.newYAMLMutationContext(p.ctx)
		assertAmbiguous(t, source, m, 0, m.deleteMappingValue(p.root, "config"))
	})

	t.Run("flow crlf replace parity", func(t *testing.T) {
		source := []byte("---\r\n{type: Knowledge, sources: [{id: a, resource: a.md}, {id: b, resource: b.md}]}\r\n---\r\n")
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		duplicate := yamlSequence(shadowSourceValue("x", "left.md"), shadowSourceValue("x", "right.md"))
		m := p.newYAMLMutationContext(p.ctx)
		assertAmbiguous(t, source, m, 0, m.replaceMappingValue(p.root, "sources", duplicate))
	})

	t.Run("direct item patch validates duplicate base", func(t *testing.T) {
		source := []byte("---\ntype: Knowledge\nsources:\n  - id: a\n    resource: left.md\n  - id: a\n    resource: right.md\n---\n")
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		sources := mappingValuesForTest(t, p.root, "sources")[0]
		m := p.newYAMLMutationContext(p.ctx)
		assertAmbiguous(t, source, m, 0,
			m.replaceMappingValue(sources.Content[0], "resource", yamlString("edited.md")))
	})
}

func TestYAMLMutation_BaseRelativeBranchAdditionOrder(t *testing.T) {
	type fixture struct {
		name   string
		source []byte
		route  string
		base   []yamlRenderValue
		added  []yamlRenderValue
	}
	atA, atB, atC, atX, atY, atZ :=
		"2026-01-02T03:04:05Z",
		"2026-02-03T04:05:06Z",
		"2026-03-04T05:06:07Z",
		"2026-04-05T06:07:08Z",
		"2026-05-06T07:08:09Z",
		"2026-06-07T08:09:10Z"
	verified := func(by, at string) yamlRenderValue {
		return yamlMapping(yamlEntry("by", yamlString(by)), yamlEntry("at", yamlString(at)))
	}
	parameter := func(name string) yamlRenderValue {
		return yamlMapping(yamlEntry("name", yamlString(name)), yamlEntry("type", yamlString("string")))
	}
	anonymous := func(resource string) yamlRenderValue {
		return yamlMapping(yamlEntry("resource", yamlString(resource)))
	}
	fixtures := []fixture{
		{
			name: "sources id flow crlf",
			source: []byte("---\r\ntype: Knowledge\r\nsources: [" +
				"{id: a, resource: a.md}, {id: b, resource: b.md}, {id: c, resource: c.md}]\r\n---\r\n"),
			route: "sources",
			base: []yamlRenderValue{
				shadowSourceValue("a", "a.md"),
				shadowSourceValue("b", "b.md"),
				shadowSourceValue("c", "c.md"),
			},
			added: []yamlRenderValue{
				shadowSourceValue("x", "x.md"),
				shadowSourceValue("y", "y.md"),
				shadowSourceValue("z", "z.md"),
			},
		},
		{
			name:   "sources anonymous exact",
			source: []byte("---\ntype: Knowledge\nsources:\n  - resource: a.md\n  - resource: b.md\n  - resource: c.md\n---\n"),
			route:  "sources",
			base:   []yamlRenderValue{anonymous("a.md"), anonymous("b.md"), anonymous("c.md")},
			added:  []yamlRenderValue{anonymous("x.md"), anonymous("y.md"), anonymous("z.md")},
		},
		{
			name: "verified",
			source: []byte("---\ntype: Knowledge\nverified:\n" +
				"  - by: person:a\n    at: " + atA + "\n" +
				"  - by: person:b\n    at: " + atB + "\n" +
				"  - by: person:c\n    at: " + atC + "\n---\n"),
			route: "verified",
			base: []yamlRenderValue{
				verified("person:a", atA),
				verified("person:b", atB),
				verified("person:c", atC),
			},
			added: []yamlRenderValue{
				verified("person:x", atX),
				verified("person:y", atY),
				verified("person:z", atZ),
			},
		},
		{
			name:   "parameters",
			source: []byte("---\ntype: Knowledge\nparameters: [{name: a}, {name: b}, {name: c}]\n---\n"),
			route:  "parameters",
			base:   []yamlRenderValue{parameter("a"), parameter("b"), parameter("c")},
			added:  []yamlRenderValue{parameter("x"), parameter("y"), parameter("z")},
		},
	}
	run := func(t *testing.T, fixture fixture, left, right yamlRenderValue, reverse bool) []byte {
		t.Helper()
		p, err := parsePresentationContext(context.Background(), fixture.source)
		if err != nil {
			t.Fatal(err)
		}
		collection := mappingValuesForTest(t, p.root, fixture.route)[0]
		if reverse {
			left, right = right, left
		}
		m := p.newYAMLMutationContext(p.ctx)
		if err := m.replaceCollectionValue(collection, left); err != nil {
			t.Fatal(err)
		}
		if err := m.replaceCollectionValue(collection, right); err != nil {
			t.Fatal(err)
		}
		got, err := m.apply()
		if err != nil {
			t.Fatal(err)
		}
		after, err := parsePresentationContext(context.Background(), got)
		if err != nil {
			t.Fatal(err)
		}
		afterCollection := mappingValuesForTest(t, after.root, fixture.route)[0]
		replay, err := applyYAMLMutationForTest(after, func(m *yamlMutation) error {
			return m.replaceCollectionValue(afterCollection, func() yamlRenderValue {
				rendered, renderErr := yamlRenderFromNodeIgnoringCommentsContext(context.Background(), afterCollection)
				if renderErr != nil {
					t.Fatal(renderErr)
				}
				return rendered
			}())
		})
		if err != nil || !bytes.Equal(replay, got) {
			t.Fatalf("replay changed result: err=%v\nfirst=%q\nreplay=%q", err, got, replay)
		}
		return got
	}

	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			t.Run("front middle end", func(t *testing.T) {
				left := yamlSequence(
					fixture.added[0],
					fixture.base[0],
					fixture.base[1],
					fixture.base[2],
					fixture.added[2],
				)
				right := yamlSequence(
					fixture.base[0],
					fixture.added[1],
					fixture.base[1],
					fixture.base[2],
				)
				forward := run(t, fixture, left, right, false)
				reverse := run(t, fixture, left, right, true)
				if !bytes.Equal(forward, reverse) {
					t.Fatalf("branch addition order mismatch:\n%q\n%q", forward, reverse)
				}
			})
			t.Run("shared reorder", func(t *testing.T) {
				left := yamlSequence(
					fixture.base[1],
					fixture.base[0],
					fixture.base[2],
					fixture.added[0],
				)
				right := yamlSequence(
					fixture.base[1],
					fixture.base[0],
					fixture.added[1],
					fixture.base[2],
				)
				forward := run(t, fixture, left, right, false)
				reverse := run(t, fixture, left, right, true)
				if !bytes.Equal(forward, reverse) {
					t.Fatalf("shared reorder mismatch:\n%q\n%q", forward, reverse)
				}
			})
			t.Run("same gap additions conflict", func(t *testing.T) {
				left := yamlSequence(fixture.added[0], fixture.base[0], fixture.base[1], fixture.base[2])
				right := yamlSequence(fixture.added[1], fixture.base[0], fixture.base[1], fixture.base[2])
				for _, reverse := range []bool{false, true} {
					p, err := parsePresentationContext(context.Background(), fixture.source)
					if err != nil {
						t.Fatal(err)
					}
					collection := mappingValuesForTest(t, p.root, fixture.route)[0]
					if reverse {
						left, right = right, left
					}
					m := p.newYAMLMutationContext(p.ctx)
					if err := m.replaceCollectionValue(collection, left); err != nil {
						t.Fatal(err)
					}
					conflict := m.replaceCollectionValue(collection, right)
					got, applyErr := m.apply()
					if conflict == nil || conflict != applyErr || got != nil ||
						!errors.Is(conflict, ErrUnsupportedPresentation) {
						t.Fatalf("reverse=%v conflict/apply=%#v/%#v bytes=%q", reverse, conflict, applyErr, got)
					}
				}
			})
		})
	}

	t.Run("nested route", func(t *testing.T) {
		source := []byte("---\ntype: Knowledge\nconfig:\n  sources:\n    - id: a\n      resource: a.md\n    - id: b\n      resource: b.md\n---\n")
		baseA, baseB := shadowSourceValue("a", "a.md"), shadowSourceValue("b", "b.md")
		addedX, addedY := shadowSourceValue("x", "x.md"), shadowSourceValue("y", "y.md")
		left := yamlMapping(yamlEntry("sources", yamlSequence(addedX, baseA, baseB)))
		right := yamlMapping(yamlEntry("sources", yamlSequence(baseA, addedY, baseB)))
		var outputs [][]byte
		for _, reverse := range []bool{false, true} {
			p, err := parsePresentationContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			config := mappingValuesForTest(t, p.root, "config")[0]
			first, second := left, right
			if reverse {
				first, second = second, first
			}
			m := p.newYAMLMutationContext(p.ctx)
			if err := m.replaceCollectionValue(config, first); err != nil {
				t.Fatal(err)
			}
			if err := m.replaceCollectionValue(config, second); err != nil {
				t.Fatal(err)
			}
			got, err := m.apply()
			if err != nil {
				t.Fatal(err)
			}
			outputs = append(outputs, got)
		}
		if !bytes.Equal(outputs[0], outputs[1]) {
			t.Fatalf("nested order mismatch:\n%q\n%q", outputs[0], outputs[1])
		}
	})

	t.Run("mapping distinct keys", func(t *testing.T) {
		source := []byte("---\ntype: Knowledge\nconfig:\n  base: zero\n  tail: keep\n---\n")
		left := yamlMapping(
			yamlEntry("front", yamlString("x")),
			yamlEntry("base", yamlString("zero")),
			yamlEntry("tail", yamlString("keep")),
			yamlEntry("zeta", yamlString("z")),
		)
		right := yamlMapping(
			yamlEntry("base", yamlString("zero")),
			yamlEntry("middle", yamlString("y")),
			yamlEntry("tail", yamlString("keep")),
			yamlEntry("alpha", yamlString("a")),
		)
		var outputs [][]byte
		for _, reverse := range []bool{false, true} {
			p, err := parsePresentationContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			config := mappingValuesForTest(t, p.root, "config")[0]
			first, second := left, right
			if reverse {
				first, second = second, first
			}
			m := p.newYAMLMutationContext(p.ctx)
			if err := m.replaceCollectionValue(config, first); err != nil {
				t.Fatal(err)
			}
			if err := m.replaceCollectionValue(config, second); err != nil {
				t.Fatal(err)
			}
			got, err := m.apply()
			if err != nil {
				t.Fatal(err)
			}
			outputs = append(outputs, got)
		}
		if !bytes.Equal(outputs[0], outputs[1]) {
			t.Fatalf("mapping key order mismatch:\n%q\n%q", outputs[0], outputs[1])
		}
	})
}

func TestYAMLMutation_LoneNonSpecificTagRawProvenance(t *testing.T) {
	assertTagRejected := func(t *testing.T, p *presentation, m *yamlMutation, before int, err error) {
		t.Helper()
		got, applyErr := m.apply()
		var presentation *PresentationError
		tagAt := -1
		for i, value := range p.yaml {
			if value == '!' && yamlTokenBoundary(p.yaml, i) &&
				(i+1 == len(p.yaml) || yamlTokenDelimiter(p.yaml[i+1])) {
				tagAt = i
				break
			}
		}
		wantLocation := SourceSpan{Start: tagAt, End: tagAt + 1}
		if err == nil || err != applyErr || got != nil || len(m.patches) != before ||
			!errors.Is(err, ErrUnsupportedPresentation) || !errors.As(err, &presentation) ||
			presentation.Code != yamlCodeExplicitTag || presentation.Location != wantLocation {
			t.Fatalf("error/apply=%#v/%#v bytes=%q patches=%d/%d location=%#v want=%#v",
				err, applyErr, got, len(m.patches), before, presentation, wantLocation)
		}
	}

	collectionFixtures := []struct {
		name   string
		source []byte
	}{
		{
			name:   "block nested scalar lf",
			source: []byte("---\ntype: Knowledge\nconfig:\n  nested:\n    value: ! tagged\n---\n"),
		},
		{
			name:   "flow nested scalar crlf",
			source: []byte("---\r\ntype: Knowledge\r\nconfig: {nested: {safe: wow!, value: ! tagged}}\r\n---\r\n"),
		},
		{
			name:   "flow nested sequence scalar",
			source: []byte("---\ntype: Knowledge\nconfig: {values: [safe, ! tagged]}\n---\n"),
		},
		{
			name:   "block mapping tag",
			source: []byte("---\ntype: Knowledge\nconfig: !\n  nested: value\n---\n"),
		},
		{
			name:   "flow mapping tag",
			source: []byte("---\ntype: Knowledge\nconfig: ! {nested: value}\n---\n"),
		},
		{
			name:   "flow sequence tag",
			source: []byte("---\ntype: Knowledge\nconfig: ! [one, two]\n---\n"),
		},
	}
	for _, fixture := range collectionFixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			t.Run("mapping replace noop", func(t *testing.T) {
				p, err := parsePresentationContext(context.Background(), fixture.source)
				if err != nil {
					t.Fatal(err)
				}
				config := mappingValuesForTest(t, p.root, "config")[0]
				desired, err := yamlRenderFromNodeIgnoringCommentsContext(context.Background(), config)
				if err != nil {
					t.Fatal(err)
				}
				m := p.newYAMLMutationContext(p.ctx)
				assertTagRejected(t, p, m, 0, m.replaceMappingValue(p.root, "config", desired))
			})
			t.Run("mapping delete", func(t *testing.T) {
				p, err := parsePresentationContext(context.Background(), fixture.source)
				if err != nil {
					t.Fatal(err)
				}
				m := p.newYAMLMutationContext(p.ctx)
				assertTagRejected(t, p, m, 0, m.deleteMappingValue(p.root, "config"))
			})
			t.Run("whole collection noop", func(t *testing.T) {
				p, err := parsePresentationContext(context.Background(), fixture.source)
				if err != nil {
					t.Fatal(err)
				}
				config := mappingValuesForTest(t, p.root, "config")[0]
				if config.Kind != yaml.MappingNode && config.Kind != yaml.SequenceNode {
					t.Skip("scalar fixture")
				}
				desired, err := yamlRenderFromNodeIgnoringCommentsContext(context.Background(), config)
				if err != nil {
					t.Fatal(err)
				}
				m := p.newYAMLMutationContext(p.ctx)
				assertTagRejected(t, p, m, 0, m.replaceCollectionValue(config, desired))
			})
		})
	}

	t.Run("direct nested scalar replace", func(t *testing.T) {
		source := []byte("---\ntype: Knowledge\nconfig:\n  nested:\n    value: ! tagged\n---\n")
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		config := mappingValuesForTest(t, p.root, "config")[0]
		nested := mappingValuesForTest(t, config, "nested")[0]
		m := p.newYAMLMutationContext(p.ctx)
		assertTagRejected(t, p, m, 0, m.replaceMappingValue(nested, "value", yamlString("tagged")))
	})

	t.Run("insert into tagged collection", func(t *testing.T) {
		source := []byte("---\ntype: Knowledge\nconfig: !\n  nested: value\n---\n")
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		config := mappingValuesForTest(t, p.root, "config")[0]
		m := p.newYAMLMutationContext(p.ctx)
		assertTagRejected(t, p, m, 0, m.insertMappingValue(config, "added", yamlString("value")))
	})

	for _, flow := range []bool{false, true} {
		name := "selector block"
		source := []byte("---\ntype: Knowledge\nsources:\n  - id: a\n    resource: a.md\n    note: ! tagged\n---\n")
		if flow {
			name = "selector flow crlf"
			source = []byte("---\r\ntype: Knowledge\r\nsources: [{id: a, resource: a.md, note: ! tagged}]\r\n---\r\n")
		}
		t.Run(name, func(t *testing.T) {
			for _, remove := range []bool{false, true} {
				p, err := parsePresentationContext(context.Background(), source)
				if err != nil {
					t.Fatal(err)
				}
				sources := mappingValuesForTest(t, p.root, "sources")[0]
				m := p.newYAMLMutationContext(p.ctx)
				if remove {
					err = m.removeSequenceItem(sources, shadowSourceSelector("a"))
				} else {
					desired, renderErr := yamlRenderFromNodeIgnoringCommentsContext(context.Background(), sources.Content[0])
					if renderErr != nil {
						t.Fatal(renderErr)
					}
					err = m.ensureSequenceItem(sources, shadowSourceSelector("a"), desired)
				}
				assertTagRejected(t, p, m, 0, err)
			}
		})
	}

	safeFixtures := [][]byte{
		[]byte("---\ntype: Knowledge\nconfig:\n  value: hello ! world\n---\n"),
		[]byte("---\ntype: Knowledge\nconfig: {plain: wow!, spaced: hello ! world, single: '!', double: \"!\"}\n---\n"),
		[]byte("---\r\ntype: Knowledge\r\nconfig: {values: [wow!, \"!\", '!', hello ! world]}\r\n---\r\n"),
	}
	for index, source := range safeFixtures {
		t.Run("safe bang content "+strconv.Itoa(index), func(t *testing.T) {
			p, err := parsePresentationContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			config := mappingValuesForTest(t, p.root, "config")[0]
			desired, err := yamlRenderFromNodeIgnoringCommentsContext(context.Background(), config)
			if err != nil {
				t.Fatal(err)
			}
			got, err := applyYAMLMutationForTest(p, func(m *yamlMutation) error { return m.replaceCollectionValue(config, desired) })
			if err != nil || !bytes.Equal(got, source) {
				t.Fatalf("safe bang rejected/changed: err=%v\n%s", err, got)
			}
		})
	}
}

func TestYAMLMutation_TerminalAliasTaxonomy(t *testing.T) {
	assertAliasRejected := func(t *testing.T, source []byte, m *yamlMutation, before int, err error) {
		t.Helper()
		got, applyErr := m.apply()
		var presentation *PresentationError
		if err == nil || err != applyErr || got != nil || len(m.patches) != before ||
			!errors.Is(err, ErrAmbiguousPresentation) || !errors.As(err, &presentation) ||
			presentation.Code != yamlCodeAliasProvenance ||
			presentation.Location.Start >= presentation.Location.End ||
			presentation.Location.End > len(source) {
			t.Fatalf("error/apply=%#v/%#v bytes=%q patches=%d/%d presentation=%#v",
				err, applyErr, got, len(m.patches), before, presentation)
		}
	}

	families := []string{
		"generated",
		"verified",
		"sources",
		"computation",
		"executor",
		"attester",
		"okf_version",
	}
	for _, family := range families {
		family := family
		for _, flow := range []bool{false, true} {
			name := family + " block"
			source := []byte("---\ntype: Knowledge\nbase: &terminal\n  value: old\n" + family + ": *terminal\n---\n")
			if flow {
				name = family + " flow crlf"
				source = []byte("---\r\n{type: Knowledge, base: &terminal {value: old}, " + family + ": *terminal}\r\n---\r\n")
			}
			t.Run(name, func(t *testing.T) {
				for _, remove := range []bool{false, true} {
					p, err := parsePresentationContext(context.Background(), source)
					if err != nil {
						t.Fatal(err)
					}
					m := p.newYAMLMutationContext(p.ctx)
					if remove {
						err = m.deleteMappingValue(p.root, family)
					} else {
						err = m.replaceMappingValue(
							p.root,
							family,
							yamlMapping(yamlEntry("value", yamlString("new"))),
						)
					}
					assertAliasRejected(t, source, m, 0, err)
				}
			})
		}
	}

	for _, flow := range []bool{false, true} {
		name := "terminal sequence alias block"
		source := []byte("---\ntype: Knowledge\nbase: &items\n  - id: a\n    resource: a.md\nsources: *items\n---\n")
		if flow {
			name = "terminal sequence alias flow"
			source = []byte("---\ntype: Knowledge\nbase: &items [{id: a, resource: a.md}]\nsources: *items\n---\n")
		}
		t.Run(name, func(t *testing.T) {
			p, err := parsePresentationContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			sources := mappingValuesForTest(t, p.root, "sources")[0]
			m := p.newYAMLMutationContext(p.ctx)
			assertAliasRejected(t, source, m, 0,
				m.ensureSequenceItem(sources, shadowSourceSelector("a"), shadowSourceValue("a", "a.md")))
		})
	}

	for _, flow := range []bool{false, true} {
		name := "alias item block"
		source := []byte("---\ntype: Knowledge\nsources:\n  - &item\n    id: a\n    resource: a.md\n  - *item\n---\n")
		if flow {
			name = "alias item flow crlf"
			source = []byte("---\r\ntype: Knowledge\r\nsources: [&item {id: a, resource: a.md}, *item]\r\n---\r\n")
		}
		t.Run(name, func(t *testing.T) {
			p, err := parsePresentationContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			sources := mappingValuesForTest(t, p.root, "sources")[0]
			m := p.newYAMLMutationContext(p.ctx)
			assertAliasRejected(t, source, m, 0,
				m.removeSequenceItem(sources, shadowSourceSelector("a")))
		})
	}

	t.Run("mapping merge terminal alias", func(t *testing.T) {
		source := []byte("---\ntype: Knowledge\ndefaults: &defaults\n  generated:\n    by: old\n<<: *defaults\n---\n")
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = p.structuralMappingEntry(p.root, "generated")
		var presentation *PresentationError
		if err == nil || !errors.Is(err, ErrAmbiguousPresentation) ||
			!errors.As(err, &presentation) || presentation.Code != yamlCodeAliasProvenance ||
			presentation.Location.Start >= presentation.Location.End {
			t.Fatalf("merge alias error=%#v", err)
		}
	})

	t.Run("structural terminal alias node", func(t *testing.T) {
		source := []byte("---\ntype: Knowledge\nbase: &terminal\n  value: old\ngenerated: *terminal\n---\n")
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		alias := mappingValuesForTest(t, p.root, "generated")[0]
		_, _, err = p.structuralMappingEntry(alias, "value")
		var presentation *PresentationError
		if err == nil || !errors.Is(err, ErrAmbiguousPresentation) ||
			!errors.As(err, &presentation) || presentation.Code != yamlCodeAliasProvenance {
			t.Fatalf("terminal alias error=%#v", err)
		}
	})
}

func FuzzYAMLMutationShadowBatch(f *testing.F) {
	f.Add([]byte{0, 1, 2, 0, 3})
	f.Add([]byte{2, 0, 0, 2})
	f.Add([]byte{1, 3, 0, 2})
	f.Fuzz(func(t *testing.T, operations []byte) {
		if len(operations) > 128 {
			operations = operations[:128]
		}
		p, sources, _ := shadowSourcePresentation(t, "sources:\n  - id: a\n    resource: a-0.md\n")
		m := p.newYAMLMutationContext(p.ctx)
		model := map[string]string{"a": "a-0.md"}
		version := 0
		accepted := func(mutationErr error) bool {
			if mutationErr == nil {
				return true
			}
			got, applyErr := m.apply()
			if mutationErr != applyErr || got != nil || !errors.Is(mutationErr, ErrUnsupportedPresentation) {
				t.Fatalf("conflict/apply = %#v / %#v, bytes=%q", mutationErr, applyErr, got)
			}
			return false
		}
		for _, operation := range operations {
			id := "a"
			switch operation % 4 {
			case 0, 1:
				if operation%4 == 1 {
					id = "b"
				}
				version++
				resource := id + "-" + strconv.Itoa(version) + ".md"
				if !accepted(m.ensureSequenceItem(sources, shadowSourceSelector(id), shadowSourceValue(id, resource))) {
					return
				}
				model[id] = resource
			case 2, 3:
				if operation%4 == 3 {
					id = "b"
				}
				if _, present := model[id]; !present {
					continue
				}
				if !accepted(m.removeSequenceItem(sources, shadowSourceSelector(id))) {
					return
				}
				delete(model, id)
			}
		}
		got, err := m.apply()
		if err != nil {
			t.Fatal(err)
		}
		after, err := parsePresentationContext(context.Background(), got)
		if err != nil {
			t.Fatal(err)
		}
		afterSources := mappingValuesForTest(t, after.root, "sources")
		if len(afterSources) != 1 || afterSources[0].Kind != yaml.SequenceNode || len(afterSources[0].Content) != len(model) {
			t.Fatalf("shadow/model cardinality mismatch: model=%v\n%s", model, got)
		}
		for id, resource := range model {
			item, found, err := after.selectSequenceItem(afterSources[0], shadowSourceSelector(id))
			if err != nil || !found {
				t.Fatalf("id %q missing: found=%v err=%v\n%s", id, found, err, got)
			}
			rendered, err := yamlRenderFromNodeContext(context.Background(), item)
			if err != nil {
				t.Fatal(err)
			}
			actual, _ := yamlRenderMappingValue(rendered, "resource")
			if actual.kind != yamlRenderString || actual.text != resource {
				t.Fatalf("id %q resource=%#v want %q", id, actual, resource)
			}
		}
	})
}

func FuzzYAMLMutationUnifiedState(f *testing.F) {
	f.Add([]byte{0, 1, 0, 2, 3})
	f.Add([]byte{4, 5, 6, 5})
	f.Add([]byte{2, 0, 4, 1, 5})
	f.Fuzz(func(t *testing.T, operations []byte) {
		if len(operations) > 96 {
			operations = operations[:96]
		}
		source := []byte("---\ntype: Knowledge\nstatus: initial\nsources: []\n---\n")
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		sources := mappingValuesForTest(t, p.root, "sources")[0]
		m := p.newYAMLMutationContext(p.ctx)
		scalars := map[string]string{"status": "initial"}
		sourceIDs := map[string]bool{}
		version := 0
		for _, operation := range operations {
			switch operation % 7 {
			case 0, 2:
				key := "status"
				if operation%7 == 2 {
					key = "version"
				}
				version++
				value := key + "-" + strconv.Itoa(version)
				if _, present := scalars[key]; present {
					err = m.replaceMappingValue(p.root, key, yamlString(value))
				} else {
					err = m.insertMappingValue(p.root, key, yamlString(value))
				}
				if err != nil {
					t.Fatal(err)
				}
				scalars[key] = value
			case 1, 3:
				key := "status"
				if operation%7 == 3 {
					key = "version"
				}
				if _, present := scalars[key]; !present {
					continue
				}
				if err := m.deleteMappingValue(p.root, key); err != nil {
					t.Fatal(err)
				}
				delete(scalars, key)
			case 4:
				if err := m.replaceCollectionValue(sources, yamlSequence(shadowSourceValue("a", "a.md"))); err != nil {
					t.Fatal(err)
				}
				sourceIDs["a"] = true
			case 5:
				if err := m.ensureSequenceItem(sources, shadowSourceSelector("b"), shadowSourceValue("b", "b.md")); err != nil {
					t.Fatal(err)
				}
				sourceIDs["b"] = true
			case 6:
				if !sourceIDs["a"] {
					continue
				}
				if err := m.removeSequenceItem(sources, shadowSourceSelector("a")); err != nil {
					t.Fatal(err)
				}
				delete(sourceIDs, "a")
			}
		}
		got, err := m.apply()
		if err != nil {
			t.Fatal(err)
		}
		after, err := parsePresentationContext(context.Background(), got)
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"status", "version"} {
			values := mappingValuesForTest(t, after.root, key)
			want, present := scalars[key]
			if !present {
				if len(values) != 0 {
					t.Fatalf("deleted key %q remains:\n%s", key, got)
				}
				continue
			}
			if len(values) != 1 || values[0].Value != want {
				t.Fatalf("key %q = %#v, want %q\n%s", key, values, want, got)
			}
		}
		afterSources := mappingValuesForTest(t, after.root, "sources")
		if len(afterSources) != 1 || afterSources[0].Kind != yaml.SequenceNode || len(afterSources[0].Content) != len(sourceIDs) {
			t.Fatalf("source model mismatch: %v\n%s", sourceIDs, got)
		}
	})
}

func FuzzYAMLMutationIdentityOrderFold(f *testing.F) {
	for _, seed := range [][2]byte{
		{0, 0}, // rename plus reorder
		{2, 1}, // id to anonymous plus reorder
		{0, 2}, // identity change versus whole removal
		{0, 3}, // granular identity change versus whole rename
		{1, 4}, // duplicate identity after an unrelated addition
		{0, 4}, // identity change plus addition
		{0, 5}, // identity change plus reverse order
	} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, childMode, parentMode byte) {
		source := []byte("---\ntype: Knowledge\nsources:\n" +
			"  - id: a\n    resource: a.md\n" +
			"  - id: b\n    resource: b.md\n" +
			"  - id: c\n    resource: c.md\n---\nBODY\n")

		run := func(parentFirst bool) ([]byte, error) {
			p, err := parsePresentationContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			sources := mappingValuesForTest(t, p.root, "sources")[0]
			itemA := sources.Content[0]
			values := make([]yamlRenderValue, len(sources.Content))
			for i, item := range sources.Content {
				values[i], err = yamlRenderFromNodeIgnoringCommentsContext(context.Background(), item)
				if err != nil {
					t.Fatal(err)
				}
			}

			child := func(m *yamlMutation) error {
				switch childMode % 3 {
				case 0:
					return m.replaceMappingValue(itemA, "id", yamlString("x"))
				case 1:
					return m.replaceMappingValue(itemA, "id", yamlString("b"))
				default:
					return m.deleteMappingValue(itemA, "id")
				}
			}
			parent := func(m *yamlMutation) error {
				switch parentMode % 6 {
				case 0:
					return m.replaceCollectionValue(sources, yamlSequence(values[1], values[0], values[2]))
				case 1:
					return m.replaceCollectionValue(sources, yamlSequence(values[0], values[2], values[1]))
				case 2:
					return m.replaceCollectionValue(sources, yamlSequence(values[1], values[2]))
				case 3:
					renamed := values[0]
					if _, err := setRenderMappingKeyContext(context.Background(), &renamed, "id", yamlString("parent"), true); err != nil {
						return err
					}
					return m.replaceCollectionValue(sources, yamlSequence(renamed, values[1], values[2]))
				case 4:
					return m.replaceCollectionValue(sources, yamlSequence(
						values[0],
						values[1],
						values[2],
						shadowSourceValue("d", "d.md"),
					))
				default:
					return m.replaceCollectionValue(sources, yamlSequence(values[2], values[1], values[0]))
				}
			}

			m := p.newYAMLMutationContext(p.ctx)
			if parentFirst {
				err = parent(m)
				if err == nil {
					err = child(m)
				}
			} else {
				err = child(m)
				if err == nil {
					err = parent(m)
				}
			}
			got, applyErr := m.apply()
			if err != nil {
				if got != nil || err != applyErr ||
					(!errors.Is(err, ErrAmbiguousPresentation) && !errors.Is(err, ErrUnsupportedPresentation)) {
					t.Fatalf("order=%v conflict/apply=%#v/%#v bytes=%q", parentFirst, err, applyErr, got)
				}
				return nil, err
			}
			if applyErr != nil {
				t.Fatal(applyErr)
			}
			after, err := parsePresentationContext(context.Background(), got)
			if err != nil {
				t.Fatal(err)
			}
			rendered, err := yamlRenderFromNodeIgnoringCommentsContext(context.Background(), mappingValuesForTest(t, after.root, "sources")[0])
			if err != nil {
				t.Fatal(err)
			}
			if err := validateYAMLDesiredUniquenessContext(context.Background(), rendered, "sources"); err != nil {
				t.Fatalf("order=%v produced duplicate identity: %v\n%s", parentFirst, err, got)
			}
			return got, nil
		}

		childFirst, childErr := run(false)
		parentFirst, parentErr := run(true)
		if (childErr == nil) != (parentErr == nil) {
			t.Fatalf("order-dependent conflict: child-first=%v parent-first=%v", childErr, parentErr)
		}
		if childErr == nil && !bytes.Equal(childFirst, parentFirst) {
			t.Fatalf("order-dependent bytes:\nchild-first:\n%q\nparent-first:\n%q", childFirst, parentFirst)
		}
	})
}

func FuzzYAMLMutationLineageAndDuplicateFold(f *testing.F) {
	f.Add(byte(0), byte(0))
	f.Add(byte(1), byte(1))
	f.Add(byte(2), byte(0))
	f.Fuzz(func(t *testing.T, mode, addedVariant byte) {
		source := []byte("---\ntype: Knowledge\nsources:\n" +
			"  - id: a\n    resource: a.md\n" +
			"  - id: b\n    resource: b.md\n---\n")
		addedID := "c"
		if addedVariant%2 != 0 {
			addedID = "d"
		}
		if mode%3 == 2 {
			p, err := parsePresentationContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			sources := mappingValuesForTest(t, p.root, "sources")[0]
			m := p.newYAMLMutationContext(p.ctx)
			duplicate := yamlSequence(
				shadowSourceValue(addedID, "left.md"),
				shadowSourceValue(addedID, "right.md"),
			)
			mutationErr := m.replaceCollectionValue(sources, duplicate)
			got, applyErr := m.apply()
			var presentation *PresentationError
			if mutationErr == nil || mutationErr != applyErr || got != nil || len(m.patches) != 0 ||
				!errors.Is(mutationErr, ErrAmbiguousPresentation) ||
				!errors.As(mutationErr, &presentation) || presentation.Code != yamlCodeAmbiguousSelector {
				t.Fatalf("duplicate error/apply=%#v/%#v bytes=%q patches=%d", mutationErr, applyErr, got, len(m.patches))
			}
			return
		}

		run := func(parentFirst bool) ([]byte, error) {
			p, err := parsePresentationContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			sources := mappingValuesForTest(t, p.root, "sources")[0]
			m := p.newYAMLMutationContext(p.ctx)
			granular := func() error {
				if err := m.removeSequenceItem(sources, shadowSourceSelector("a")); err != nil {
					return err
				}
				return m.ensureSequenceItem(
					sources,
					shadowSourceSelector(addedID),
					shadowSourceValue(addedID, addedID+".md"),
				)
			}
			whole := func() error {
				if mode%3 == 0 {
					return m.replaceCollectionValue(sources, yamlSequence(
						shadowSourceValue("a", "edited.md"),
						shadowSourceValue("b", "b.md"),
					))
				}
				return m.replaceCollectionValue(sources, yamlSequence(shadowSourceValue("b", "b.md")))
			}
			if parentFirst {
				err = whole()
				if err == nil {
					err = granular()
				}
			} else {
				err = granular()
				if err == nil {
					err = whole()
				}
			}
			got, applyErr := m.apply()
			if err != nil {
				if err != applyErr || got != nil || !errors.Is(err, ErrUnsupportedPresentation) {
					t.Fatalf("order=%v error/apply=%#v/%#v bytes=%q", parentFirst, err, applyErr, got)
				}
				return nil, err
			}
			if applyErr != nil {
				t.Fatal(applyErr)
			}
			return got, nil
		}

		childFirst, childErr := run(false)
		parentFirst, parentErr := run(true)
		if mode%3 == 0 {
			if childErr == nil || parentErr == nil {
				t.Fatalf("edit/remove+add must conflict in both orders: %v/%v", childErr, parentErr)
			}
			return
		}
		if childErr != nil || parentErr != nil || !bytes.Equal(childFirst, parentFirst) {
			t.Fatalf("compatible removal mismatch: child=%q/%v parent=%q/%v", childFirst, childErr, parentFirst, parentErr)
		}
	})
}

func FuzzYAMLMutationBaseRelativeAdditionOrder(f *testing.F) {
	f.Add(byte(0), byte(1))
	f.Add(byte(1), byte(3))
	f.Add(byte(2), byte(2))
	f.Fuzz(func(t *testing.T, leftGap, rightGap byte) {
		leftGap %= 4
		rightGap %= 4
		source := []byte("---\ntype: Knowledge\nsources:\n" +
			"  - id: a\n    resource: a.md\n" +
			"  - id: b\n    resource: b.md\n" +
			"  - id: c\n    resource: c.md\n---\n")
		base := []yamlRenderValue{
			shadowSourceValue("a", "a.md"),
			shadowSourceValue("b", "b.md"),
			shadowSourceValue("c", "c.md"),
		}
		withAdded := func(added yamlRenderValue, gap byte) yamlRenderValue {
			items := append([]yamlRenderValue(nil), base...)
			index := int(gap)
			items = append(items, yamlRenderValue{})
			copy(items[index+1:], items[index:])
			items[index] = added
			return yamlSequence(items...)
		}
		left := withAdded(shadowSourceValue("x", "x.md"), leftGap)
		right := withAdded(shadowSourceValue("y", "y.md"), rightGap)
		run := func(reverse bool) ([]byte, error) {
			p, err := parsePresentationContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			sources := mappingValuesForTest(t, p.root, "sources")[0]
			first, second := left, right
			if reverse {
				first, second = second, first
			}
			m := p.newYAMLMutationContext(p.ctx)
			if err := m.replaceCollectionValue(sources, first); err != nil {
				t.Fatal(err)
			}
			err = m.replaceCollectionValue(sources, second)
			got, applyErr := m.apply()
			if err != nil {
				if err != applyErr || got != nil || !errors.Is(err, ErrUnsupportedPresentation) {
					t.Fatalf("reverse=%v error/apply=%#v/%#v bytes=%q", reverse, err, applyErr, got)
				}
				return nil, err
			}
			if applyErr != nil {
				t.Fatal(applyErr)
			}
			return got, nil
		}
		forward, forwardErr := run(false)
		reverse, reverseErr := run(true)
		if leftGap == rightGap {
			if forwardErr == nil || reverseErr == nil {
				t.Fatalf("same-gap additions must conflict: %v/%v", forwardErr, reverseErr)
			}
			return
		}
		if forwardErr != nil || reverseErr != nil || !bytes.Equal(forward, reverse) {
			t.Fatalf("gap=%d/%d order mismatch: forward=%q/%v reverse=%q/%v",
				leftGap, rightGap, forward, forwardErr, reverse, reverseErr)
		}
	})
}

func FuzzYAMLMutationMappingEntryUniqueness(f *testing.F) {
	f.Add(byte(0), false)
	f.Add(byte(1), true)
	f.Add(byte(2), false)
	f.Fuzz(func(t *testing.T, mode byte, flow bool) {
		mode %= 3
		duplicate := yamlSequence(shadowSourceValue("x", "left.md"), shadowSourceValue("x", "right.md"))
		var source []byte
		switch mode {
		case 0:
			source = []byte("---\ntype: Knowledge\n---\n")
		case 1:
			if flow {
				source = []byte("---\r\n{type: Knowledge, sources: [{id: a, resource: a.md}, {id: b, resource: b.md}]}\r\n---\r\n")
			} else {
				source = []byte("---\ntype: Knowledge\nsources:\n  - id: a\n    resource: a.md\n  - id: b\n    resource: b.md\n---\n")
			}
		default:
			source = []byte("---\ntype: Knowledge\nsources:\n  - id: x\n    resource: left.md\n  - id: x\n    resource: right.md\n---\n")
		}
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		m := p.newYAMLMutationContext(p.ctx)
		switch mode {
		case 0:
			err = m.insertMappingValue(p.root, "sources", duplicate)
		case 1:
			err = m.replaceMappingValue(p.root, "sources", duplicate)
		default:
			err = m.deleteMappingValue(p.root, "sources")
		}
		got, applyErr := m.apply()
		var presentation *PresentationError
		if err == nil || err != applyErr || got != nil || len(m.patches) != 0 ||
			!errors.Is(err, ErrAmbiguousPresentation) || !errors.As(err, &presentation) ||
			presentation.Code != yamlCodeAmbiguousSelector {
			t.Fatalf("mode=%d flow=%v error/apply=%#v/%#v bytes=%q patches=%d",
				mode, flow, err, applyErr, got, len(m.patches))
		}
	})
}

func FuzzYAMLMutationLoneTagAndTerminalAlias(f *testing.F) {
	f.Add(byte(0), false)
	f.Add(byte(1), true)
	f.Add(byte(2), false)
	f.Add(byte(3), true)
	f.Fuzz(func(t *testing.T, mode byte, flow bool) {
		mode %= 4
		if mode == 3 {
			family := "generated"
			if flow {
				family = "sources"
			}
			source := []byte("---\ntype: Knowledge\nbase: &terminal\n  value: old\n" + family + ": *terminal\n---\n")
			p, err := parsePresentationContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			m := p.newYAMLMutationContext(p.ctx)
			err = m.replaceMappingValue(
				p.root,
				family,
				yamlMapping(yamlEntry("value", yamlString("new"))),
			)
			got, applyErr := m.apply()
			var presentation *PresentationError
			if err == nil || err != applyErr || got != nil || len(m.patches) != 0 ||
				!errors.Is(err, ErrAmbiguousPresentation) || !errors.As(err, &presentation) ||
				presentation.Code != yamlCodeAliasProvenance {
				t.Fatalf("alias error/apply=%#v/%#v bytes=%q patches=%d", err, applyErr, got, len(m.patches))
			}
			return
		}

		var source []byte
		switch mode {
		case 0:
			source = []byte("---\ntype: Knowledge\nconfig:\n  nested:\n    value: ! tagged\n---\n")
		case 1:
			source = []byte("---\r\ntype: Knowledge\r\nconfig: {values: [safe, ! tagged]}\r\n---\r\n")
		default:
			if flow {
				source = []byte("---\r\ntype: Knowledge\r\nsources: [{id: a, resource: a.md, note: ! tagged}]\r\n---\r\n")
			} else {
				source = []byte("---\ntype: Knowledge\nsources:\n  - id: a\n    resource: a.md\n    note: ! tagged\n---\n")
			}
		}
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		m := p.newYAMLMutationContext(p.ctx)
		switch mode {
		case 0, 1:
			config := mappingValuesForTest(t, p.root, "config")[0]
			desired, renderErr := yamlRenderFromNodeIgnoringCommentsContext(context.Background(), config)
			if renderErr != nil {
				t.Fatal(renderErr)
			}
			err = m.replaceCollectionValue(config, desired)
		default:
			sources := mappingValuesForTest(t, p.root, "sources")[0]
			err = m.removeSequenceItem(sources, shadowSourceSelector("a"))
		}
		got, applyErr := m.apply()
		var presentation *PresentationError
		if err == nil || err != applyErr || got != nil || len(m.patches) != 0 ||
			!errors.Is(err, ErrUnsupportedPresentation) || !errors.As(err, &presentation) ||
			presentation.Code != yamlCodeExplicitTag || presentation.Location.End-presentation.Location.Start != 1 ||
			presentation.Location.Start < 0 || presentation.Location.End > len(p.yaml) ||
			p.yaml[presentation.Location.Start] != '!' {
			t.Fatalf("tag error/apply=%#v/%#v bytes=%q patches=%d presentation=%#v",
				err, applyErr, got, len(m.patches), presentation)
		}
	})
}

func TestYAMLMutation_ProvenSelectorIdentityWithOpaqueSiblings(t *testing.T) {
	t.Run("hidden duplicate survives unsupported full render", func(t *testing.T) {
		source := []byte("---\ntype: Knowledge\nsources:\n" +
			"  - id: duplicate\n    ratio: 1.25\n" +
			"  - id: duplicate\n    absent: null\n---\n")
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		sources := mappingValuesForTest(t, p.root, "sources")[0]
		m := p.newYAMLMutationContext(p.ctx)
		mutationErr := m.removeSequenceItem(sources, shadowSourceSelector("duplicate"))
		got, applyErr := m.apply()
		var presentation *PresentationError
		if mutationErr == nil || mutationErr != applyErr || got != nil || len(m.patches) != 0 ||
			!errors.Is(mutationErr, ErrAmbiguousPresentation) ||
			!errors.As(mutationErr, &presentation) || presentation.Code != yamlCodeAmbiguousSelector {
			t.Fatalf("duplicate error/apply=%#v/%#v bytes=%q patches=%d presentation=%#v",
				mutationErr, applyErr, got, len(m.patches), presentation)
		}
	})

	t.Run("distinct block selector preserves opaque bytes", func(t *testing.T) {
		source := []byte("---\r\ntype: Knowledge\r\nsources:\r\n" +
			"  - id: remove\r\n    resource: remove.md\r\n" +
			"  - id: opaque\r\n    ratio: 1.20\r\n    absent: ~\r\n" +
			"    ? [complex]\r\n    : keep\r\n---\r\n")
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		sources := mappingValuesForTest(t, p.root, "sources")[0]
		got, err := applyYAMLMutationForTest(p, func(m *yamlMutation) error { return m.removeSequenceItem(sources, shadowSourceSelector("remove")) })
		if err != nil {
			t.Fatal(err)
		}
		for _, token := range [][]byte{
			[]byte("id: opaque"),
			[]byte("ratio: 1.20"),
			[]byte("absent: ~"),
			[]byte("? [complex]\r\n    : keep"),
		} {
			if !bytes.Contains(got, token) {
				t.Fatalf("opaque token %q changed:\n%q", token, got)
			}
		}
		if bytes.Contains(bytes.ReplaceAll(got, []byte("\r\n"), nil), []byte("\n")) {
			t.Fatalf("CRLF output acquired bare LF:\n%q", got)
		}
	})

	t.Run("distinct flow selector fails closed without bytes", func(t *testing.T) {
		source := []byte("---\r\ntype: Knowledge\r\n" +
			"sources: [{id: remove, resource: remove.md}, {id: opaque, ratio: 1.20}]\r\n---\r\n")
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		sources := mappingValuesForTest(t, p.root, "sources")[0]
		m := p.newYAMLMutationContext(p.ctx)
		mutationErr := m.removeSequenceItem(sources, shadowSourceSelector("remove"))
		got, applyErr := m.apply()
		if mutationErr == nil || mutationErr != applyErr || got != nil ||
			!errors.Is(mutationErr, ErrUnsupportedPresentation) {
			t.Fatalf("flow error/apply=%#v/%#v bytes=%q", mutationErr, applyErr, got)
		}
	})

	for _, fixture := range []struct {
		name       string
		body       string
		route      string
		selectItem yamlSequenceSelector
	}{
		{
			name:       "sources explicit tag",
			body:       "sources:\n  - id: opaque\n    note: ! tagged\n",
			route:      "sources",
			selectItem: shadowSourceSelector("opaque"),
		},
		{
			name:  "verified float",
			body:  "verified:\n  - by: person:a\n    at: 2026-01-02T03:04:05Z\n    ratio: 1.25\n",
			route: "verified",
			selectItem: yamlMappingSelector(
				yamlSelector("by", yamlString("person:a")),
				yamlSelector("at", func() yamlRenderValue {
					value := yamlDateTimeForTest(t, "2026-01-02T03:04:05Z")
					return value
				}()),
			),
		},
		{
			name:       "parameters null",
			body:       "parameters:\n  - name: opaque\n    value: null\n",
			route:      "parameters",
			selectItem: yamlMappingSelector(yamlSelector("name", yamlString("opaque"))),
		},
	} {
		fixture := fixture
		t.Run("selected unrenderable fails "+fixture.name, func(t *testing.T) {
			source := []byte("---\ntype: Knowledge\n" + fixture.body + "---\n")
			p, err := parsePresentationContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			collection := mappingValuesForTest(t, p.root, fixture.route)[0]
			m := p.newYAMLMutationContext(p.ctx)
			mutationErr := m.removeSequenceItem(collection, fixture.selectItem)
			got, applyErr := m.apply()
			var presentation *PresentationError
			if mutationErr == nil || mutationErr != applyErr || got != nil || len(m.patches) != 0 ||
				!errors.Is(mutationErr, ErrUnsupportedPresentation) ||
				!errors.As(mutationErr, &presentation) || presentation.Code == "" {
				t.Fatalf("selected error/apply=%#v/%#v bytes=%q patches=%d presentation=%#v",
					mutationErr, applyErr, got, len(m.patches), presentation)
			}
		})
	}

	t.Run("anonymous exact remains fail closed", func(t *testing.T) {
		source := []byte("---\ntype: Knowledge\nsources:\n  - resource: policy.md\n    ratio: 1.25\n---\n")
		p, err := parsePresentationContext(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		sources := mappingValuesForTest(t, p.root, "sources")[0]
		desired := yamlMapping(
			yamlEntry("resource", yamlString("other.md")),
			yamlEntry("ratio", yamlString("opaque")),
		)
		m := p.newYAMLMutationContext(p.ctx)
		mutationErr := m.ensureSequenceItem(sources, yamlExactSelector(desired), desired)
		got, applyErr := m.apply()
		if mutationErr == nil || mutationErr != applyErr || got != nil || len(m.patches) != 0 ||
			!errors.Is(mutationErr, ErrUnsupportedPresentation) {
			t.Fatalf("anonymous error/apply=%#v/%#v bytes=%q patches=%d",
				mutationErr, applyErr, got, len(m.patches))
		}
	})
}

func TestYAMLMutation_MappingOrderThreeWayRenderState(t *testing.T) {
	entry := func(key, value string) yamlRenderEntry {
		return yamlEntry(key, yamlString(value))
	}
	base := yamlMapping(entry("a", "a"), entry("b", "b"), entry("c", "c"))

	t.Run("incompatible reorders conflict both invocation orders", func(t *testing.T) {
		left := yamlMapping(entry("b", "b"), entry("a", "a"), entry("c", "c"))
		right := yamlMapping(entry("a", "a"), entry("c", "c"), entry("b", "b"))
		for _, branches := range [][2]yamlRenderValue{{left, right}, {right, left}} {
			if _, err := mergeYAMLDesiredContext(context.Background(), base, branches[0], branches[1], ""); !errors.Is(err, errYAMLDesiredConflict) {
				t.Fatalf("merge error = %v", err)
			}
		}
	})

	t.Run("reorder and disjoint leaf edit commute", func(t *testing.T) {
		reordered := yamlMapping(entry("b", "b"), entry("a", "a"), entry("c", "c"))
		edited := yamlMapping(entry("a", "a"), entry("b", "b"), entry("c", "edited"))
		forward, err := mergeYAMLDesiredContext(context.Background(), base, reordered, edited, "")
		if err != nil {
			t.Fatal(err)
		}
		reverse, err := mergeYAMLDesiredContext(context.Background(), base, edited, reordered, "")
		if err != nil {
			t.Fatal(err)
		}
		expected := yamlMapping(entry("b", "b"), entry("a", "a"), entry("c", "edited"))
		if !yamlRenderStatesEqualForTest(t, forward, reverse) || !yamlRenderStatesEqualForTest(t, forward, expected) {
			t.Fatalf("forward/reverse = %#v / %#v", forward, reverse)
		}
	})

	t.Run("same reorder with add and delete is deterministic", func(t *testing.T) {
		added := yamlMapping(entry("b", "b"), entry("a", "a"), entry("c", "c"), entry("d", "d"))
		deleted := yamlMapping(entry("b", "b"), entry("a", "a"))
		forward, err := mergeYAMLDesiredContext(context.Background(), base, added, deleted, "")
		if err != nil {
			t.Fatal(err)
		}
		reverse, err := mergeYAMLDesiredContext(context.Background(), base, deleted, added, "")
		if err != nil {
			t.Fatal(err)
		}
		expected := yamlMapping(entry("b", "b"), entry("a", "a"), entry("d", "d"))
		if !yamlRenderStatesEqualForTest(t, forward, reverse) || !yamlRenderStatesEqualForTest(t, forward, expected) {
			t.Fatalf("forward/reverse = %#v / %#v", forward, reverse)
		}
	})

	t.Run("lexically equivalent timestamps cannot diverge", func(t *testing.T) {
		zulu, err := yamlDateTime("2026-01-02T03:04:05Z")
		if err != nil {
			t.Fatal(err)
		}
		offset := zulu
		offset.lexical = "2026-01-02T03:04:05+00:00"
		timeBase := yamlMapping(yamlEntry("at", zulu), entry("status", "old"))
		lexicalBranch := yamlMapping(yamlEntry("at", offset), entry("status", "old"))
		editBranch := yamlMapping(yamlEntry("at", zulu), entry("status", "new"))
		forward, err := mergeYAMLDesiredContext(context.Background(), timeBase, lexicalBranch, editBranch, "")
		if err != nil {
			t.Fatal(err)
		}
		reverse, err := mergeYAMLDesiredContext(context.Background(), timeBase, editBranch, lexicalBranch, "")
		if err != nil {
			t.Fatal(err)
		}
		if !yamlRenderStatesEqualForTest(t, forward, reverse) || forward.mapping[0].Value.lexical != offset.lexical {
			t.Fatalf("forward/reverse = %#v / %#v", forward, reverse)
		}
	})

	t.Run("positive int and uint are the same emitted state", func(t *testing.T) {
		numberBase := yamlMapping(yamlEntry("count", yamlInt(1)), entry("status", "old"))
		signed := yamlMapping(yamlEntry("count", yamlInt(42)), entry("status", "old"))
		unsigned := yamlMapping(yamlEntry("count", yamlUint(42)), entry("status", "new"))
		forward, err := mergeYAMLDesiredContext(context.Background(), numberBase, signed, unsigned, "")
		if err != nil {
			t.Fatal(err)
		}
		reverse, err := mergeYAMLDesiredContext(context.Background(), numberBase, unsigned, signed, "")
		if err != nil {
			t.Fatal(err)
		}
		if !yamlRenderStatesEqualForTest(t, forward, reverse) {
			t.Fatalf("forward/reverse = %#v / %#v", forward, reverse)
		}
	})
}

func FuzzYAMLMutationMappingOrderThreeWay(f *testing.F) {
	f.Add(byte(0), byte(1))
	f.Add(byte(1), byte(2))
	f.Add(byte(4), byte(5))
	permutations := [][3]string{
		{"a", "b", "c"},
		{"a", "c", "b"},
		{"b", "a", "c"},
		{"b", "c", "a"},
		{"c", "a", "b"},
		{"c", "b", "a"},
	}
	f.Fuzz(func(t *testing.T, leftIndex, rightIndex byte) {
		makeMapping := func(order [3]string) yamlRenderValue {
			entries := make([]yamlRenderEntry, 0, len(order))
			for _, key := range order {
				entries = append(entries, yamlEntry(key, yamlString(key)))
			}
			return yamlMapping(entries...)
		}
		base := makeMapping(permutations[0])
		left := makeMapping(permutations[int(leftIndex)%len(permutations)])
		right := makeMapping(permutations[int(rightIndex)%len(permutations)])
		forward, forwardErr := mergeYAMLDesiredContext(context.Background(), base, left, right, "")
		reverse, reverseErr := mergeYAMLDesiredContext(context.Background(), base, right, left, "")
		if (forwardErr == nil) != (reverseErr == nil) {
			t.Fatalf("asymmetric errors: %v / %v", forwardErr, reverseErr)
		}
		if forwardErr != nil {
			if !errors.Is(forwardErr, errYAMLDesiredConflict) || !errors.Is(reverseErr, errYAMLDesiredConflict) {
				t.Fatalf("unexpected errors: %v / %v", forwardErr, reverseErr)
			}
			return
		}
		if !yamlRenderStatesEqualForTest(t, forward, reverse) {
			t.Fatalf("asymmetric merge: %#v / %#v", forward, reverse)
		}
	})
}

func TestYAMLMutation_RelationTargetStructuralTupleIdentity(t *testing.T) {
	hashID, err := bundle.NewConceptID([]string{"source#part"})
	if err != nil {
		t.Fatal(err)
	}
	sourceID, err := bundle.NewConceptID([]string{"source"})
	if err != nil {
		t.Fatal(err)
	}
	hashRoot := bundle.RelationRef{ID: hashID}
	sourceFragment := bundle.RelationRef{ID: sourceID, Fragment: "part"}

	t.Run("tuple key separates hash concept from fragment", func(t *testing.T) {
		// Arrange.
		hashKey := relationRefIdentity(hashRoot)
		fragmentKey := relationRefIdentity(sourceFragment)

		// Act.
		equal := hashKey == fragmentKey
		less := func(left, right relationRefKey) bool {
			return left.id < right.id || left.id == right.id && left.fragment < right.fragment
		}
		ordered := less(hashKey, fragmentKey) != less(fragmentKey, hashKey)

		// Assert.
		if equal || !ordered {
			t.Fatalf("structural keys collapsed or unordered: %#v / %#v", hashKey, fragmentKey)
		}
	})

	parseTargets := func(t *testing.T, targets []string) *yaml.Node {
		t.Helper()
		var source bytes.Buffer
		source.WriteString("---\ntype: Knowledge\nrelations:\n  uses:\n")
		for _, target := range targets {
			source.WriteString("    - target: ")
			source.WriteString(target)
			source.WriteByte('\n')
		}
		source.WriteString("---\n")
		p, parseErr := parsePresentationContext(context.Background(), source.Bytes())
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		relations := mappingValuesForTest(t, p.root, "relations")
		if len(relations) != 1 {
			t.Fatalf("relations = %d", len(relations))
		}
		uses := mappingValuesForTest(t, relations[0], "uses")
		if len(uses) != 1 {
			t.Fatalf("uses = %d", len(uses))
		}
		return uses[0]
	}

	for index, targets := range [][]string{
		{`source\#part`, "source#part"},
		{"source#part", `source\#part`},
	} {
		targets := append([]string(nil), targets...)
		t.Run("distinct permutation "+strconv.Itoa(index), func(t *testing.T) {
			// Arrange.
			items := parseTargets(t, targets)

			// Act.
			hashDuplicates, hashErr := duplicateRelationTargetsInSequenceContext(context.Background(), items, hashID, "")
			fragmentDuplicates, fragmentErr := duplicateRelationTargetsInSequenceContext(context.Background(), items, sourceID, "")

			// Assert.
			if hashErr != nil || fragmentErr != nil || len(hashDuplicates) != 0 || len(fragmentDuplicates) != 0 {
				t.Fatalf("distinct tuples reported duplicate: hash=%d/%v fragment=%d/%v", len(hashDuplicates), hashErr, len(fragmentDuplicates), fragmentErr)
			}
		})
	}

	for index, targets := range [][]string{
		{`source\#part`, "source#part", "source#part"},
		{"source#part", `source\#part`, "source#part"},
		{"source#part", "source#part", `source\#part`},
	} {
		targets := append([]string(nil), targets...)
		t.Run("exact duplicate permutation "+strconv.Itoa(index), func(t *testing.T) {
			// Arrange.
			items := parseTargets(t, targets)

			// Act.
			hashDuplicates, hashErr := duplicateRelationTargetsInSequenceContext(context.Background(), items, hashID, "")
			fragmentDuplicates, fragmentErr := duplicateRelationTargetsInSequenceContext(context.Background(), items, sourceID, "")

			// Assert.
			if hashErr != nil || fragmentErr != nil || len(hashDuplicates) != 0 || len(fragmentDuplicates) != 2 {
				t.Fatalf("exact tuple duplicate mismatch: hash=%d/%v fragment=%d/%v", len(hashDuplicates), hashErr, len(fragmentDuplicates), fragmentErr)
			}
			for _, target := range fragmentDuplicates {
				if target.Value != "source#part" {
					t.Fatalf("unrelated tuple included in duplicate: %q", target.Value)
				}
			}
		})
	}
}
