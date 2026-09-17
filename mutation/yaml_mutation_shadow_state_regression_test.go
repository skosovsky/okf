package mutation

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestReplacementShadowCanonicalStateTransitions(t *testing.T) {
	type fixture struct {
		name, key, empty, initial string
		a, updated, b             yamlRenderValue
		selectA, selectB          yamlSequenceSelector
	}
	atA := "2026-01-02T03:04:05Z"
	fixtures := []fixture{
		{name: "sources", key: "sources", empty: "sources: []", initial: "sources:\n  - id: a\n    resource: old.md", a: shadowSourceValue("a", "old.md"), updated: shadowSourceValue("a", "new.md"), b: shadowSourceValue("b", "b.md"), selectA: shadowSourceSelector("a"), selectB: shadowSourceSelector("b")},
		{name: "verified", key: "verified", empty: "verified: []", initial: "verified:\n  - by: person:a\n    at: " + atA, a: yamlMapping(yamlEntry("by", yamlString("person:a")), yamlEntry("at", yamlString(atA))), updated: yamlMapping(yamlEntry("by", yamlString("person:a")), yamlEntry("at", yamlString(atA)), yamlEntry("note", yamlString("updated"))), b: yamlMapping(yamlEntry("by", yamlString("person:b")), yamlEntry("at", yamlString("2026-02-03T04:05:06Z"))), selectA: yamlMappingSelector(yamlSelector("by", yamlString("person:a")), yamlSelector("at", yamlString(atA))), selectB: yamlMappingSelector(yamlSelector("by", yamlString("person:b")), yamlSelector("at", yamlString("2026-02-03T04:05:06Z")))},
		{name: "parameters", key: "parameters", empty: "parameters: []", initial: "parameters:\n  - name: a\n    type: string", a: yamlMapping(yamlEntry("name", yamlString("a")), yamlEntry("type", yamlString("string"))), updated: yamlMapping(yamlEntry("name", yamlString("a")), yamlEntry("type", yamlString("integer"))), b: yamlMapping(yamlEntry("name", yamlString("b")), yamlEntry("type", yamlString("boolean"))), selectA: yamlMappingSelector(yamlSelector("name", yamlString("a"))), selectB: yamlMappingSelector(yamlSelector("name", yamlString("b")))},
	}
	for _, fixture := range fixtures {
		for _, operation := range []string{"remove", "update", "add"} {
			t.Run(fixture.name+"/"+operation, func(t *testing.T) {
				body := fixture.empty
				if operation == "update" {
					body = fixture.initial
				}
				source := []byte("---\ntype: Knowledge\nstatus: keep\n" + body + "\n---\nBody\n")
				p, err := parsePresentationContext(context.Background(), source)
				if err != nil {
					t.Fatal(err)
				}
				collection := mappingValuesForTest(t, p.root, fixture.key)[0]
				m := p.newYAMLMutationContext(p.ctx)
				if err := m.replaceCollectionValue(collection, yamlSequence(fixture.a)); err != nil {
					t.Fatal(err)
				}
				final := yamlSequence(fixture.a)
				switch operation {
				case "remove":
					if err := m.removeSequenceItem(collection, fixture.selectA); err != nil {
						t.Fatal(err)
					}
				case "update":
					if err := m.ensureSequenceItem(collection, fixture.selectA, fixture.updated); err != nil {
						t.Fatal(err)
					}
					final = yamlSequence(fixture.updated)
				case "add":
					if err := m.ensureSequenceItem(collection, fixture.selectB, fixture.b); err != nil {
						t.Fatal(err)
					}
					final = yamlSequence(fixture.a, fixture.b)
				}
				if err := m.replaceCollectionValue(collection, final); err != nil {
					t.Fatal(err)
				}
				got, err := m.apply()
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Contains(got, []byte("status: keep")) || !bytes.Contains(got, []byte("\nBody\n")) {
					t.Fatalf("unowned bytes changed:\n%s", got)
				}
				after, err := parsePresentationContext(context.Background(), got)
				if err != nil {
					t.Fatal(err)
				}
				rendered := yamlRenderFromNodeForTest(t, mappingValuesForTest(t, after.root, fixture.key)[0])
				equal, err := equalYAMLRenderStateContext(context.Background(), rendered, final)
				if err != nil || !equal {
					t.Fatalf("final render=%#v want=%#v err=%v", rendered, final, err)
				}
				replay := after.newYAMLMutationContext(context.Background())
				replayCollection := mappingValuesForTest(t, after.root, fixture.key)[0]
				if err := replay.replaceCollectionValue(replayCollection, final); err != nil {
					t.Fatal(err)
				}
				replayed, err := replay.apply()
				if err != nil || !bytes.Equal(replayed, got) {
					t.Fatalf("replay changed bytes error=%v\nfirst=%s\nreplay=%s", err, got, replayed)
				}
			})
		}
		t.Run(fixture.name+"/conflict", func(t *testing.T) {
			source := []byte("---\ntype: Knowledge\n" + fixture.empty + "\n---\n")
			p, err := parsePresentationContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			collection := mappingValuesForTest(t, p.root, fixture.key)[0]
			m := p.newYAMLMutationContext(p.ctx)
			if err := m.replaceCollectionValue(collection, yamlSequence(fixture.a)); err != nil {
				t.Fatal(err)
			}
			conflict := m.ensureSequenceItem(collection, fixture.selectA, fixture.updated)
			got, applyErr := m.apply()
			if conflict == nil || applyErr != conflict || got != nil || !errors.Is(conflict, ErrUnsupportedPresentation) {
				t.Fatalf("conflict/apply=%#v/%#v bytes=%q", conflict, applyErr, got)
			}
		})
		t.Run(fixture.name+"/cancel", func(t *testing.T) {
			source := []byte("---\ntype: Knowledge\n" + fixture.empty + "\n---\n")
			p, err := parsePresentationContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			collection := mappingValuesForTest(t, p.root, fixture.key)[0]
			ctx, cancel := context.WithCancel(context.Background())
			m := p.newYAMLMutationContext(ctx)
			if err := m.replaceCollectionValue(collection, yamlSequence(fixture.a)); err != nil {
				t.Fatal(err)
			}
			cancel()
			cancelErr := m.removeSequenceItem(collection, fixture.selectA)
			got, applyErr := m.apply()
			if !errors.Is(cancelErr, context.Canceled) || !errors.Is(applyErr, context.Canceled) || got != nil {
				t.Fatalf("cancel/apply=%#v/%#v bytes=%q", cancelErr, applyErr, got)
			}
		})
	}
}

func TestFuzzC0CReplacementShadowRegression(t *testing.T) {
	p, sources, source := shadowSourcePresentation(t, "sources: []\n")
	m := p.newYAMLMutationContext(p.ctx)
	a := shadowSourceValue("a", "a.md")
	if err := m.replaceCollectionValue(sources, yamlSequence(a)); err != nil {
		t.Fatal(err)
	}
	if err := m.removeSequenceItem(sources, shadowSourceSelector("a")); err != nil {
		t.Fatal(err)
	}
	if err := m.replaceCollectionValue(sources, yamlSequence(a)); err != nil {
		t.Fatal(err)
	}

	got, err := m.apply()

	if err != nil || !bytes.Contains(got, []byte("id: a")) || !bytes.Contains(got, []byte("Body\n")) || bytes.Equal(got, source) {
		t.Fatalf("C0C result=%q error=%v", got, err)
	}
}

func TestReplacementDescendantMixedSequenceFailsClosed(t *testing.T) {
	source := []byte("---\ntype: Knowledge\nconfig:\n  sources:\n    - id: a\n      resource: a.md\n    - invalid\n---\n")
	p, err := parsePresentationContext(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	config := mappingValuesForTest(t, p.root, "config")[0]
	sources := mappingValuesForTest(t, config, "sources")[0]
	item := sources.Content[0]
	desired := yamlRenderFromNodeForTest(t, item)
	expectedErr := func() error { _, err := p.sequenceSelectorItems(sources); return err }()
	var expected *PresentationError
	if !errors.As(expectedErr, &expected) {
		t.Fatalf("sequenceSelectorItems() error = %#v", expectedErr)
	}
	m := p.newYAMLMutationContext(context.Background())
	if err := m.replaceCollectionValue(config, yamlRenderFromNodeForTest(t, config)); err != nil {
		t.Fatal(err)
	}

	mutationErr := m.ensureSequenceItem(sources, shadowSourceSelector("a"), desired)
	updated, applyErr := m.apply()

	var located *PresentationError
	if !errors.As(mutationErr, &located) || located.Code != expected.Code || located.Location != expected.Location ||
		!errors.Is(mutationErr, ErrUnsupportedPresentation) || applyErr != mutationErr || updated != nil ||
		len(m.patches) != 0 || len(m.collectionLineage) != 0 {
		t.Fatalf("mixed descendant = error=%#v apply=%#v bytes=%q patches=%#v lineage=%#v, want %s at %#v and zero result", mutationErr, applyErr, updated, m.patches, m.collectionLineage, expected.Code, expected.Location)
	}
}
