package mutation

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestYAMLMutationBatch_RootAndNestedMappingEntriesTyped(t *testing.T) {
	// Arrange.
	source := []byte("---\ntype: Knowledge\ntimestamp: 2025-01-01\nsettings:\n  enabled: false\n  count: 1\nx-opaque: keep\n---\nBody\n")
	original := append([]byte(nil), source...)
	p, err := parsePresentationContext(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	settings := mappingValuesForTest(t, p.root, "settings")[0]
	generatedAt, err := yamlDateTime("2026-07-29T10:11:12+07:00")
	if err != nil {
		t.Fatal(err)
	}
	mutation := p.newYAMLMutationContext(p.ctx)

	// Act.
	if err := mutation.replaceMappingValue(settings, "enabled", yamlBool(true)); err != nil {
		t.Fatal(err)
	}
	if err := mutation.replaceMappingValue(settings, "count", yamlInt(2)); err != nil {
		t.Fatal(err)
	}
	if err := mutation.insertMappingValue(p.root, "generated", yamlMapping(
		yamlEntry("by", yamlString("person:sergey")),
		yamlEntry("at", generatedAt),
	)); err != nil {
		t.Fatal(err)
	}
	if err := mutation.deleteMappingValue(p.root, "timestamp"); err != nil {
		t.Fatal(err)
	}
	updated, err := mutation.apply()
	if err != nil {
		t.Fatal(err)
	}

	// Assert.
	if !bytes.Equal(source, original) {
		t.Fatal("caller/source bytes mutated")
	}
	if !bytes.Contains(updated, []byte("enabled: true\n")) || !bytes.Contains(updated, []byte("count: 2\n")) {
		t.Fatalf("typed replacements missing:\n%s", updated)
	}
	if bytes.Contains(updated, []byte("timestamp:")) || !bytes.Contains(updated, []byte("generated:\n  by: person:sergey\n  at: 2026-07-29T10:11:12+07:00\n")) {
		t.Fatalf("root batch mismatch:\n%s", updated)
	}
	if !bytes.Contains(updated, []byte("x-opaque: keep\n")) || bytes.Contains(updated, []byte("x-opaque: 'keep'")) ||
		!bytes.HasSuffix(updated, []byte("---\nBody\n")) {
		t.Fatalf("unrelated presentation changed:\n%s", updated)
	}
}

func TestYAMLMutation_WholeFlowCollectionReplacementIsAtomic(t *testing.T) {
	// Arrange.
	source := []byte("---\ntype: Knowledge\nsources: [{id: old, resource: \"old.md\"}]\nx: keep\n---\nBody\n")
	p, err := parsePresentationContext(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	sources := mappingValuesForTest(t, p.root, "sources")[0]
	desired := yamlSequence(yamlMapping(
		yamlEntry("id", yamlString("new")),
		yamlEntry("resource", yamlString("new.md")),
	))

	// Act.
	updated, err := applyYAMLMutationForTest(p, func(m *yamlMutation) error { return m.replaceCollectionValue(sources, desired) })
	if err != nil {
		t.Fatal(err)
	}

	// Assert.
	want := []byte("---\ntype: Knowledge\nsources: [{id: new, resource: new.md}]\nx: keep\n---\nBody\n")
	if !bytes.Equal(updated, want) {
		t.Fatalf("updated:\n%s\nwant:\n%s", updated, want)
	}
}

func TestYAMLMutation_BareMappingBecomesUniqueSelectorSequence(t *testing.T) {
	// Arrange.
	source := []byte("---\ntype: Knowledge\nverified:\n  by: person:a\n  at: 2026-01-02T03:04:05Z\n  x-note: keep\n---\nBody\n")
	p, err := parsePresentationContext(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	verified := mappingValuesForTest(t, p.root, "verified")[0]
	atA := yamlDateTimeForTest(t, "2026-01-02T03:04:05Z")
	atB := yamlDateTimeForTest(t, "2026-02-03T04:05:06Z")
	selector := yamlMappingSelector(
		yamlSelector("by", yamlString("person:b")),
		yamlSelector("at", atB),
	)
	desired := yamlMapping(
		yamlEntry("by", yamlString("person:b")),
		yamlEntry("at", atB),
	)

	// Act.
	updated, err := applyYAMLMutationForTest(p, func(m *yamlMutation) error { return m.ensureSequenceItem(verified, selector, desired) })
	if err != nil {
		t.Fatal(err)
	}
	after, err := parsePresentationContext(context.Background(), updated)
	if err != nil {
		t.Fatal(err)
	}
	afterVerified := mappingValuesForTest(t, after.root, "verified")[0]
	first, found, err := after.selectSequenceItem(afterVerified, yamlMappingSelector(
		yamlSelector("by", yamlString("person:a")),
		yamlSelector("at", atA),
	))
	secondApplication, secondErr := applyYAMLMutationForTest(after, func(m *yamlMutation) error { return m.ensureSequenceItem(afterVerified, selector, desired) })

	// Assert.
	if err != nil || !found || first == nil {
		t.Fatalf("existing bare item missing after normalization: found=%v err=%v", found, err)
	}
	if afterVerified.Kind != yaml.SequenceNode || len(afterVerified.Content) != 2 {
		t.Fatalf("verified shape = kind %d, items %d:\n%s", afterVerified.Kind, len(afterVerified.Content), updated)
	}
	if !bytes.Contains(updated, []byte("x-note: keep")) {
		t.Fatalf("unknown nested field lost:\n%s", updated)
	}
	if secondErr != nil || !bytes.Equal(secondApplication, updated) {
		t.Fatalf("second application changed bytes: err=%v\nfirst:\n%s\nsecond:\n%s", secondErr, updated, secondApplication)
	}
}

func TestYAMLMutation_RemoveSequenceItemOwnsOnlySelectedItem(t *testing.T) {
	// Arrange.
	source := []byte("---\ntype: Knowledge\nsources:\n  - id: remove\n    resource: remove.md\n  - id: keep\n    resource: 'keep.md'\nx-opaque: keep\n---\nBody\n")
	p, err := parsePresentationContext(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	sources := mappingValuesForTest(t, p.root, "sources")[0]

	// Act.
	updated, err := applyYAMLMutationForTest(p, func(m *yamlMutation) error {
		return m.removeSequenceItem(
			sources,
			yamlMappingSelector(yamlSelector("id", yamlString("remove"))),
		)
	})
	if err != nil {
		t.Fatal(err)
	}

	// Assert.
	want := []byte("---\ntype: Knowledge\nsources:\n  - id: keep\n    resource: 'keep.md'\nx-opaque: keep\n---\nBody\n")
	if !bytes.Equal(updated, want) {
		t.Fatalf("updated:\n%s\nwant:\n%s", updated, want)
	}
}

func TestYAMLMutation_SequenceSelectorRejectsDuplicates(t *testing.T) {
	// Arrange.
	source := []byte("---\ntype: Knowledge\nsources:\n  - id: duplicate\n    resource: a\n  - id: duplicate\n    resource: b\n---\n")
	p, err := parsePresentationContext(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	sources := mappingValuesForTest(t, p.root, "sources")[0]

	// Act.
	updated, err := applyYAMLMutationForTest(p, func(m *yamlMutation) error {
		return m.ensureSequenceItem(
			sources,
			yamlMappingSelector(yamlSelector("id", yamlString("duplicate"))),
			yamlMapping(yamlEntry("id", yamlString("duplicate")), yamlEntry("resource", yamlString("c"))),
		)
	})

	// Assert.
	if updated != nil || !errors.Is(err, ErrAmbiguousPresentation) {
		t.Fatalf("updated=%q err=%v", updated, err)
	}
	var presentation *PresentationError
	if !errors.As(err, &presentation) || presentation.Code != yamlCodeAmbiguousSelector ||
		presentation.Location.Start < 0 || presentation.Location.End > len(source) || presentation.Location.Start >= presentation.Location.End {
		t.Fatalf("typed error = %#v", err)
	}
}

func TestYAMLMutation_StructuralOwnershipRejectsCommentsAliasAndDuplicateKey(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		key  string
		kind error
		code string
	}{
		{name: "comment", yaml: "type: Knowledge\ngenerated: # ownership unknown\n  by: old\n", key: "generated", kind: ErrUnsupportedPresentation, code: yamlCodeUnownedComment},
		{name: "alias", yaml: "type: Knowledge\nbase: &base\n  by: old\ngenerated: *base\n", key: "generated", kind: ErrAmbiguousPresentation, code: yamlCodeAliasProvenance},
		{name: "duplicate", yaml: "type: Knowledge\ngenerated: {by: a}\ngenerated: {by: b}\n", key: "generated", kind: ErrAmbiguousPresentation, code: yamlCodeDuplicateTouchedKey},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			source := []byte("---\n" + tt.yaml + "---\n")
			p, err := parsePresentationContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			updated, err := p.replaceMappingValue(p.root, tt.key, yamlMapping(yamlEntry("by", yamlString("new"))))

			// Assert.
			if updated != nil || !errors.Is(err, tt.kind) {
				t.Fatalf("updated=%q err=%v", updated, err)
			}
			var presentation *PresentationError
			if !errors.As(err, &presentation) || presentation.Code != tt.code {
				t.Fatalf("typed error = %#v, want %s", err, tt.code)
			}
		})
	}
}

func TestYAMLMutation_CRLFAndOutsideSpanPreservation(t *testing.T) {
	// Arrange.
	source := []byte("---\r\ntype: Knowledge\r\ncount: 1\r\nquoted: 'untouched'\r\n---\r\nBody\r\n")
	p, err := parsePresentationContext(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	updated, err := p.replaceMappingValue(p.root, "count", yamlInt(9))
	if err != nil {
		t.Fatal(err)
	}

	// Assert.
	want := []byte("---\r\ntype: Knowledge\r\ncount: 9\r\nquoted: 'untouched'\r\n---\r\nBody\r\n")
	if !bytes.Equal(updated, want) {
		t.Fatalf("updated=%q want=%q", updated, want)
	}
}
