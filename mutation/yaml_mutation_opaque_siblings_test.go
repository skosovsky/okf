package mutation

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/skosovsky/okf/store"
	"gopkg.in/yaml.v3"
)

type deferredShadowRouteFixture struct {
	name             string
	collection       string
	body             string
	opaqueBytes      []byte
	selectedBytes    []byte
	updatedBytes     []byte
	selectedSelector yamlSequenceSelector
	selectedValue    yamlRenderValue
	updatedValue     yamlRenderValue
	addedSelector    yamlSequenceSelector
	addedValue       yamlRenderValue
}

func deferredShadowRouteFixtures() []deferredShadowRouteFixture {
	selectedVerification := yamlMapping(
		yamlEntry("by", yamlString("process:selected")),
		yamlEntry("at", yamlDateTimeMust("2026-01-01T00:00:00Z")),
	)
	updatedVerification := yamlMapping(
		yamlEntry("by", yamlString("process:selected")),
		yamlEntry("at", yamlDateTimeMust("2026-01-01T00:00:00Z")),
		yamlEntry("note", yamlString("updated")),
	)
	addedVerification := yamlMapping(
		yamlEntry("by", yamlString("process:added")),
		yamlEntry("at", yamlDateTimeMust("2026-01-03T00:00:00Z")),
	)
	return []deferredShadowRouteFixture{
		{
			name:       "sources id",
			collection: "sources",
			body: "sources:\n" +
				"  - id: selected\n" +
				"    resource: old.md\n" +
				"  - id: opaque\n" +
				"    resource: opaque.md\n" +
				"    note: first\n" +
				"    note: second\n",
			opaqueBytes:      []byte("  - id: opaque\n    resource: opaque.md\n    note: first\n    note: second\n"),
			selectedBytes:    []byte("id: selected"),
			updatedBytes:     []byte("resource: new.md"),
			selectedSelector: shadowSourceSelector("selected"),
			selectedValue:    shadowSourceValue("selected", "old.md"),
			updatedValue:     shadowSourceValue("selected", "new.md"),
			addedSelector:    shadowSourceSelector("added"),
			addedValue:       shadowSourceValue("added", "added.md"),
		},
		{
			name:       "sources anonymous exact",
			collection: "sources",
			body: "sources:\n" +
				"  - resource: anonymous.md\n" +
				"    title: Selected\n" +
				"  - id: opaque\n" +
				"    resource: opaque.md\n" +
				"    note: first\n" +
				"    note: second\n",
			opaqueBytes:   []byte("  - id: opaque\n    resource: opaque.md\n    note: first\n    note: second\n"),
			selectedBytes: []byte("resource: anonymous.md"),
			updatedBytes:  []byte("resource: anonymous.md"),
			selectedValue: yamlMapping(
				yamlEntry("resource", yamlString("anonymous.md")),
				yamlEntry("title", yamlString("Selected")),
			),
			addedValue: yamlMapping(
				yamlEntry("resource", yamlString("added-anonymous.md")),
			),
		},
		{
			name:       "verified",
			collection: "verified",
			body: "verified:\n" +
				"  - by: process:selected\n" +
				"    at: 2026-01-01T00:00:00Z\n" +
				"  - by: process:opaque\n" +
				"    at: 2026-01-02T00:00:00Z\n" +
				"    note: first\n" +
				"    note: second\n",
			opaqueBytes:   []byte("  - by: process:opaque\n    at: 2026-01-02T00:00:00Z\n    note: first\n    note: second\n"),
			selectedBytes: []byte("by: process:selected"),
			updatedBytes:  []byte("note: updated"),
			selectedSelector: yamlMappingSelector(
				yamlSelector("by", yamlString("process:selected")),
				yamlSelector("at", yamlDateTimeMust("2026-01-01T00:00:00Z")),
			),
			selectedValue: selectedVerification,
			updatedValue:  updatedVerification,
			addedSelector: yamlMappingSelector(
				yamlSelector("by", yamlString("process:added")),
				yamlSelector("at", yamlDateTimeMust("2026-01-03T00:00:00Z")),
			),
			addedValue: addedVerification,
		},
		{
			name:       "parameters",
			collection: "parameters",
			body: "parameters:\n" +
				"  - name: selected\n" +
				"    type: string\n" +
				"  - name: opaque\n" +
				"    type: string\n" +
				"    note: first\n" +
				"    note: second\n",
			opaqueBytes:      []byte("  - name: opaque\n    type: string\n    note: first\n    note: second\n"),
			selectedBytes:    []byte("name: selected"),
			updatedBytes:     []byte("type: integer"),
			selectedSelector: yamlMappingSelector(yamlSelector("name", yamlString("selected"))),
			selectedValue: yamlMapping(
				yamlEntry("name", yamlString("selected")),
				yamlEntry("type", yamlString("string")),
			),
			updatedValue: yamlMapping(
				yamlEntry("name", yamlString("selected")),
				yamlEntry("type", yamlString("integer")),
			),
			addedSelector: yamlMappingSelector(yamlSelector("name", yamlString("added"))),
			addedValue: yamlMapping(
				yamlEntry("name", yamlString("added")),
				yamlEntry("type", yamlString("boolean")),
			),
		},
	}
}

func yamlDateTimeMust(raw string) yamlRenderValue {
	value, err := yamlDateTime(raw)
	if err != nil {
		panic(err)
	}
	return value
}

func TestYAMLMutation_GranularSequenceEditsPreserveOpaqueUnselectedSibling(t *testing.T) {
	for _, fixture := range deferredShadowRouteFixtures() {
		fixture := fixture
		if fixture.selectedSelector.exact == nil && fixture.collection == "sources" && fixture.selectedValue.kind == yamlRenderMapping &&
			!yamlRenderMappingHasKey(fixture.selectedValue, "id") {
			fixture.selectedSelector = yamlExactSelector(fixture.selectedValue)
			fixture.addedSelector = yamlExactSelector(fixture.addedValue)
		}
		if fixture.name == "sources anonymous exact" {
			fixture.selectedSelector = yamlExactSelector(fixture.selectedValue)
			fixture.updatedValue = fixture.selectedValue
			fixture.addedSelector = yamlExactSelector(fixture.addedValue)
		}

		operations := []struct {
			name string
			act  func(*yamlMutation, *yaml.Node) error
			want []byte
			gone []byte
		}{
			{
				name: "ensure absent",
				act: func(m *yamlMutation, collection *yaml.Node) error {
					return m.ensureSequenceItem(collection, fixture.addedSelector, fixture.addedValue)
				},
				want: []byte("added"),
			},
			{
				name: "update selected",
				act: func(m *yamlMutation, collection *yaml.Node) error {
					return m.ensureSequenceItem(collection, fixture.selectedSelector, fixture.updatedValue)
				},
				want: fixture.updatedBytes,
			},
			{
				name: "remove selected",
				act: func(m *yamlMutation, collection *yaml.Node) error {
					return m.removeSequenceItem(collection, fixture.selectedSelector)
				},
				gone: fixture.selectedBytes,
			},
		}

		for _, operation := range operations {
			t.Run(fixture.name+"/"+operation.name, func(t *testing.T) {
				// Arrange.
				source := []byte("---\ntype: Knowledge\n" + fixture.body + "---\nBody\n")
				p, err := parsePresentationContext(context.Background(), source)
				if err != nil {
					t.Fatal(err)
				}
				collection := mappingValuesForTest(t, p.root, fixture.collection)[0]
				mutation := p.newYAMLMutationContext(p.ctx)

				// Act.
				mutationErr := operation.act(mutation, collection)
				var updated []byte
				if mutationErr == nil {
					updated, mutationErr = mutation.apply()
				}

				// Assert.
				if mutationErr != nil {
					t.Fatal(mutationErr)
				}
				if bytes.Count(updated, fixture.opaqueBytes) != 1 {
					t.Fatalf("opaque sibling was not byte-preserved exactly once:\n%s", updated)
				}
				if len(operation.want) != 0 && !bytes.Contains(updated, operation.want) {
					t.Fatalf("desired item is absent:\n%s", updated)
				}
				if len(operation.gone) != 0 && bytes.Contains(updated, operation.gone) {
					t.Fatalf("removed item remains:\n%s", updated)
				}
			})
		}
	}
}

func TestYAMLMutation_SelectedOpaqueItemFailsWithDeferredTypedError(t *testing.T) {
	tests := []struct {
		name string
		body string
		code string
	}{
		{
			name: "duplicate key",
			body: "sources:\n" +
				"  - id: selected\n" +
				"    resource: selected.md\n" +
				"    note: first\n" +
				"    note: second\n" +
				"  - id: keep\n" +
				"    resource: keep.md\n",
			code: yamlCodeDuplicateTouchedKey,
		},
		{
			name: "alias value",
			body: "note: &shared preserved\n" +
				"sources:\n" +
				"  - id: selected\n" +
				"    resource: selected.md\n" +
				"    note: *shared\n" +
				"  - id: keep\n" +
				"    resource: keep.md\n",
			code: yamlCodeAliasProvenance,
		},
		{
			name: "merge value",
			body: "defaults: &defaults {note: preserved}\n" +
				"sources:\n" +
				"  - id: selected\n" +
				"    resource: selected.md\n" +
				"    <<: *defaults\n" +
				"  - id: keep\n" +
				"    resource: keep.md\n",
			code: yamlCodeAliasProvenance,
		},
	}
	for _, test := range tests {
		for _, operation := range []string{"update", "remove"} {
			t.Run(test.name+"/"+operation, func(t *testing.T) {
				// Arrange.
				source := []byte("---\ntype: Knowledge\n" + test.body + "---\n")
				p, err := parsePresentationContext(context.Background(), source)
				if err != nil {
					t.Fatal(err)
				}
				sources := mappingValuesForTest(t, p.root, "sources")[0]
				mutation := p.newYAMLMutationContext(p.ctx)

				// Act.
				switch operation {
				case "update":
					err = mutation.ensureSequenceItem(
						sources,
						shadowSourceSelector("selected"),
						shadowSourceValue("selected", "updated.md"),
					)
				case "remove":
					err = mutation.removeSequenceItem(sources, shadowSourceSelector("selected"))
				}
				updated, applyErr := mutation.apply()

				// Assert.
				assertStructuralPresentationError(t, updated, err, ErrAmbiguousPresentation, test.code, len(source))
				assertStructuralPresentationError(t, updated, applyErr, ErrAmbiguousPresentation, test.code, len(source))
				if len(mutation.patches) != 0 {
					t.Fatalf("rejected selected item staged patches: %#v", mutation.patches)
				}
			})
		}
	}
}

func TestYAMLMutation_WholeSequenceMaterializationFailsClosedOnOpaqueSibling(t *testing.T) {
	tests := []struct {
		name string
		body string
		act  func(*yamlMutation, *yaml.Node) error
	}{
		{
			name: "flow removal",
			body: "sources: [{id: selected, resource: selected.md}, {id: opaque, resource: opaque.md, note: first, note: second}]\n",
			act: func(m *yamlMutation, collection *yaml.Node) error {
				return m.removeSequenceItem(collection, shadowSourceSelector("selected"))
			},
		},
		{
			name: "whole replacement",
			body: "sources:\n" +
				"  - id: selected\n" +
				"    resource: selected.md\n" +
				"  - id: opaque\n" +
				"    resource: opaque.md\n" +
				"    note: first\n" +
				"    note: second\n",
			act: func(m *yamlMutation, collection *yaml.Node) error {
				return m.replaceCollectionValue(collection, yamlSequence(shadowSourceValue("selected", "updated.md")))
			},
		},
		{
			name: "bare normalization",
			body: "verified:\n" +
				"  by: process:opaque\n" +
				"  at: 2026-01-02T00:00:00Z\n" +
				"  note: first\n" +
				"  note: second\n",
			act: func(m *yamlMutation, collection *yaml.Node) error {
				desired := yamlMapping(
					yamlEntry("by", yamlString("process:added")),
					yamlEntry("at", yamlDateTimeMust("2026-01-03T00:00:00Z")),
				)
				return m.ensureSequenceItem(collection, yamlMappingSelector(
					yamlSelector("by", yamlString("process:added")),
					yamlSelector("at", yamlDateTimeMust("2026-01-03T00:00:00Z")),
				), desired)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			source := []byte("---\ntype: Knowledge\n" + test.body + "---\n")
			p, err := parsePresentationContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			collectionName := "sources"
			if bytes.HasPrefix([]byte(test.body), []byte("verified:")) {
				collectionName = "verified"
			}
			collection := mappingValuesForTest(t, p.root, collectionName)[0]
			mutation := p.newYAMLMutationContext(p.ctx)

			// Act.
			stageErr := test.act(mutation, collection)
			var updated []byte
			applyErr := stageErr
			if stageErr == nil {
				updated, applyErr = mutation.apply()
			}

			// Assert.
			assertStructuralPresentationError(t, updated, applyErr, ErrAmbiguousPresentation, yamlCodeDuplicateTouchedKey, len(source))
			if len(mutation.patches) != 0 {
				t.Fatalf("failed whole materialization staged patches: %#v", mutation.patches)
			}
		})
	}
}

func TestPlanRemoveSource_PreservesUnselectedOpaqueSibling(t *testing.T) {
	tests := []struct {
		name       string
		provenance string
		opaqueItem string
	}{
		{
			name: "duplicate key",
			opaqueItem: "  - id: keep\n" +
				"    resource: keep.md\n" +
				"    note: first\n" +
				"    note: second\n",
		},
		{
			name:       "alias value",
			provenance: "shared: &shared preserved\n",
			opaqueItem: "  - id: keep\n" +
				"    resource: keep.md\n" +
				"    note: *shared\n",
		},
		{
			name:       "merge value",
			provenance: "defaults: &defaults {note: preserved}\n",
			opaqueItem: "  - id: keep\n" +
				"    resource: keep.md\n" +
				"    <<: *defaults\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			document := []byte("---\ntype: Note\n" + test.provenance +
				"sources:\n" +
				"  - id: remove\n" +
				"    resource: remove.md\n" +
				test.opaqueItem +
				"---\nBody\n")
			source := memorySource{"a.md": document}
			selector, err := store.SourceByID("remove")
			if err != nil {
				t.Fatal(err)
			}
			operation, err := store.NewRemoveSource(ref(t, "a").ID, selector)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			result, planErr := Plan(context.Background(), source, change(t, source, operation))
			var updated []byte
			if planErr == nil {
				updated, planErr = result.Staged.ReadFile(context.Background(), "a.md")
			}

			// Assert.
			if planErr != nil {
				t.Fatal(planErr)
			}
			if bytes.Contains(updated, []byte("id: remove")) || bytes.Contains(updated, []byte("resource: remove.md")) {
				t.Fatalf("selected source remains:\n%s", updated)
			}
			if !bytes.Contains(updated, []byte(test.provenance)) || bytes.Count(updated, []byte(test.opaqueItem)) != 1 {
				t.Fatalf("opaque sibling/provenance bytes changed:\n%s", updated)
			}
		})
	}
}

func TestPlanRemoveSource_SelectedOpaqueItemHasTypedZeroStageFailure(t *testing.T) {
	for _, test := range []struct {
		name       string
		provenance string
		extension  string
		code       string
	}{
		{name: "duplicate", extension: "    note: first\n    note: second\n", code: yamlCodeDuplicateTouchedKey},
		{name: "alias", provenance: "shared: &shared preserved\n", extension: "    note: *shared\n", code: yamlCodeAliasProvenance},
		{name: "merge", provenance: "defaults: &defaults {note: preserved}\n", extension: "    <<: *defaults\n", code: yamlCodeAliasProvenance},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			document := []byte("---\ntype: Note\n" + test.provenance +
				"sources:\n" +
				"  - id: remove\n" +
				"    resource: remove.md\n" +
				test.extension +
				"  - id: keep\n" +
				"    resource: keep.md\n" +
				"---\nBody\n")
			source := memorySource{"a.md": document}
			selector, err := store.SourceByID("remove")
			if err != nil {
				t.Fatal(err)
			}
			operation, err := store.NewRemoveSource(ref(t, "a").ID, selector)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			result, planErr := Plan(context.Background(), source, change(t, source, operation))

			// Assert.
			if !errors.Is(planErr, ErrAmbiguousPresentation) {
				t.Fatalf("Plan() error = %#v", planErr)
			}
			var presentationErr *PresentationError
			if !errors.As(planErr, &presentationErr) || presentationErr.Code != test.code ||
				presentationErr.Path != "a.md" || presentationErr.Operation != "remove_source" {
				t.Fatalf("typed Plan() error = %#v", planErr)
			}
			if result.Staged != nil || len(result.Preview.Writes) != 0 ||
				len(result.Preview.Renames) != 0 || len(result.Preview.Plan) != 0 {
				t.Fatalf("rejected Plan() exposed staged state: %#v", result)
			}
			if !bytes.Equal(source["a.md"], document) {
				t.Fatal("rejected Plan() mutated caller source")
			}
		})
	}
}

func granularOpaqueMappingBody() string {
	return "defaults: &defaults {inherited: preserved}\n" +
		"<<: *defaults\n" +
		"shared: &shared preserved\n" +
		"opaque_alias: *shared\n" +
		"opaque_tag: !future tagged\n" +
		"opaque: first\n" +
		"opaque: second\n" +
		"target: old\n" +
		"remove: old\n" +
		"nested:\n" +
		"  defaults: &nested_defaults {inherited: preserved}\n" +
		"  <<: *nested_defaults\n" +
		"  shared: &nested_shared preserved\n" +
		"  opaque_alias: *nested_shared\n" +
		"  opaque_tag: !future tagged\n" +
		"  opaque: first\n" +
		"  opaque: second\n" +
		"  target: old\n" +
		"  remove: old\n"
}

func TestYAMLMutation_GranularMappingPrimitivesPreserveOpaqueUnrelatedState(t *testing.T) {
	for _, owner := range []string{"root", "nested"} {
		for _, operation := range []string{"replace", "insert", "delete"} {
			t.Run(owner+"/"+operation, func(t *testing.T) {
				// Arrange.
				source := []byte("---\ntype: Note\n" + granularOpaqueMappingBody() + "---\nBody\n")
				p, err := parsePresentationContext(context.Background(), source)
				if err != nil {
					t.Fatal(err)
				}
				mapping := p.root
				indent := ""
				if owner == "nested" {
					mapping = mappingValuesForTest(t, p.root, "nested")[0]
					indent = "  "
				}
				opaqueBytes := []byte(indent +
					"defaults: &" + map[string]string{"root": "defaults", "nested": "nested_defaults"}[owner] + " {inherited: preserved}\n" +
					indent + "<<: *" + map[string]string{"root": "defaults", "nested": "nested_defaults"}[owner] + "\n" +
					indent + "shared: &" + map[string]string{"root": "shared", "nested": "nested_shared"}[owner] + " preserved\n" +
					indent + "opaque_alias: *" + map[string]string{"root": "shared", "nested": "nested_shared"}[owner] + "\n" +
					indent + "opaque_tag: !future tagged\n" +
					indent + "opaque: first\n" +
					indent + "opaque: second\n")
				mutation := p.newYAMLMutationContext(p.ctx)

				// Act.
				switch operation {
				case "replace":
					err = mutation.replaceMappingValue(mapping, "target", yamlString("new"))
				case "insert":
					err = mutation.insertMappingValue(mapping, "inserted", yamlString("new"))
				case "delete":
					err = mutation.deleteMappingValue(mapping, "remove")
				}
				var updated []byte
				if err == nil {
					updated, err = mutation.apply()
				}

				// Assert.
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Count(updated, opaqueBytes) != 1 {
					t.Fatalf("opaque unrelated mapping state changed:\n%s", updated)
				}
				switch operation {
				case "replace":
					if !bytes.Contains(updated, []byte("\n"+indent+"target: new\n")) {
						t.Fatalf("replacement missing:\n%s", updated)
					}
				case "insert":
					if !bytes.Contains(updated, []byte("\n"+indent+"inserted: new\n")) {
						t.Fatalf("insertion missing:\n%s", updated)
					}
				case "delete":
					if bytes.Contains(updated, []byte("\n"+indent+"remove: old\n")) {
						t.Fatalf("deleted entry remains:\n%s", updated)
					}
				}
			})
		}
	}
}

func TestYAMLMutation_GranularMappingTouchedDuplicateStillFailsTyped(t *testing.T) {
	for _, operation := range []string{"replace", "insert", "delete"} {
		t.Run(operation, func(t *testing.T) {
			// Arrange.
			source := []byte("---\ntype: Note\ntarget: first\ntarget: second\n---\n")
			p, err := parsePresentationContext(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			mutation := p.newYAMLMutationContext(p.ctx)

			// Act.
			switch operation {
			case "replace":
				err = mutation.replaceMappingValue(p.root, "target", yamlString("new"))
			case "insert":
				err = mutation.insertMappingValue(p.root, "target", yamlString("new"))
			case "delete":
				err = mutation.deleteMappingValue(p.root, "target")
			}
			updated, applyErr := mutation.apply()

			// Assert.
			assertStructuralPresentationError(t, updated, err, ErrAmbiguousPresentation, yamlCodeDuplicateTouchedKey, len(source))
			assertStructuralPresentationError(t, updated, applyErr, ErrAmbiguousPresentation, yamlCodeDuplicateTouchedKey, len(source))
			if len(mutation.patches) != 0 {
				t.Fatalf("touched duplicate staged patches: %#v", mutation.patches)
			}
		})
	}
}

func TestPlanSetLifecycle_PreservesOpaqueRootAndNestedExtensions(t *testing.T) {
	// Arrange.
	document := []byte("---\ntype: Note\nstatus: draft\n" + granularOpaqueMappingBody() + "---\nBody\n")
	source := memorySource{"a.md": document}
	status := "stable"
	operation, err := store.NewSetLifecycle(ref(t, "a").ID, store.Lifecycle{Status: &status})
	if err != nil {
		t.Fatal(err)
	}
	rootOpaque := []byte("defaults: &defaults {inherited: preserved}\n" +
		"<<: *defaults\n" +
		"shared: &shared preserved\n" +
		"opaque_alias: *shared\n" +
		"opaque_tag: !future tagged\n" +
		"opaque: first\n" +
		"opaque: second\n")
	nestedOpaque := []byte("  defaults: &nested_defaults {inherited: preserved}\n" +
		"  <<: *nested_defaults\n" +
		"  shared: &nested_shared preserved\n" +
		"  opaque_alias: *nested_shared\n" +
		"  opaque_tag: !future tagged\n" +
		"  opaque: first\n" +
		"  opaque: second\n")

	// Act.
	result, planErr := Plan(context.Background(), source, change(t, source, operation))
	var updated []byte
	if planErr == nil {
		updated, planErr = result.Staged.ReadFile(context.Background(), "a.md")
	}

	// Assert.
	if planErr != nil {
		t.Fatal(planErr)
	}
	if !bytes.Contains(updated, []byte("status: stable")) ||
		bytes.Count(updated, rootOpaque) != 1 || bytes.Count(updated, nestedOpaque) != 1 {
		t.Fatalf("SetLifecycle changed opaque extension bytes:\n%s", updated)
	}
}

func TestPlanSetLifecycle_TouchedDuplicateHasTypedZeroStageFailure(t *testing.T) {
	// Arrange.
	document := []byte("---\ntype: Note\nstatus: draft\nstatus: stale\nopaque: preserved\n---\nBody\n")
	source := memorySource{"a.md": document}
	status := "stable"
	operation, err := store.NewSetLifecycle(ref(t, "a").ID, store.Lifecycle{Status: &status})
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	result, planErr := Plan(context.Background(), source, change(t, source, operation))

	// Assert.
	if !errors.Is(planErr, ErrAmbiguousPresentation) {
		t.Fatalf("Plan() error = %#v", planErr)
	}
	var presentationErr *PresentationError
	if !errors.As(planErr, &presentationErr) || presentationErr.Code != yamlCodeDuplicateTouchedKey ||
		presentationErr.Path != "a.md" || presentationErr.Operation != "set_lifecycle" {
		t.Fatalf("typed Plan() error = %#v", planErr)
	}
	if result.Staged != nil || len(result.Preview.Writes) != 0 ||
		len(result.Preview.Renames) != 0 || len(result.Preview.Plan) != 0 {
		t.Fatalf("rejected SetLifecycle exposed staged state: %#v", result)
	}
	if !bytes.Equal(source["a.md"], document) {
		t.Fatal("rejected SetLifecycle mutated caller source")
	}
}

func TestYAMLMutation_FieldSelectorsSkipOpaqueItemsWithProvablyMissingIdentity(t *testing.T) {
	type routeFixture struct {
		name         string
		collection   string
		selected     string
		selector     yamlSequenceSelector
		updated      yamlRenderValue
		added        yamlRenderValue
		addedSelect  yamlSequenceSelector
		missingItem  func(string) string
		opaqueMarker []byte
	}
	at := yamlDateTimeMust("2026-01-01T00:00:00Z")
	addedAt := yamlDateTimeMust("2026-01-03T00:00:00Z")
	routes := []routeFixture{
		{
			name:       "sources id",
			collection: "sources",
			selected: "  - id: selected\n" +
				"    resource: old.md\n",
			selector:    shadowSourceSelector("selected"),
			updated:     shadowSourceValue("selected", "new.md"),
			added:       shadowSourceValue("added", "added.md"),
			addedSelect: shadowSourceSelector("added"),
			missingItem: func(extension string) string {
				return "  - resource: anonymous.md\n" + extension
			},
			opaqueMarker: []byte("resource: anonymous.md"),
		},
		{
			name:       "verified by-at",
			collection: "verified",
			selected: "  - by: process:selected\n" +
				"    at: 2026-01-01T00:00:00Z\n",
			selector: yamlMappingSelector(
				yamlSelector("by", yamlString("process:selected")),
				yamlSelector("at", at),
			),
			updated: yamlMapping(
				yamlEntry("by", yamlString("process:selected")),
				yamlEntry("at", at),
				yamlEntry("note", yamlString("updated")),
			),
			added: yamlMapping(
				yamlEntry("by", yamlString("process:added")),
				yamlEntry("at", addedAt),
			),
			addedSelect: yamlMappingSelector(
				yamlSelector("by", yamlString("process:added")),
				yamlSelector("at", addedAt),
			),
			missingItem: func(extension string) string {
				return "  - note: missing-verification-identity\n" + extension
			},
			opaqueMarker: []byte("missing-verification-identity"),
		},
		{
			name:       "parameters name",
			collection: "parameters",
			selected: "  - name: selected\n" +
				"    type: string\n",
			selector: yamlMappingSelector(yamlSelector("name", yamlString("selected"))),
			updated: yamlMapping(
				yamlEntry("name", yamlString("selected")),
				yamlEntry("type", yamlString("integer")),
			),
			added: yamlMapping(
				yamlEntry("name", yamlString("added")),
				yamlEntry("type", yamlString("boolean")),
			),
			addedSelect: yamlMappingSelector(yamlSelector("name", yamlString("added"))),
			missingItem: func(extension string) string {
				return "  - type: missing-parameter-identity\n" + extension
			},
			opaqueMarker: []byte("missing-parameter-identity"),
		},
	}
	opaqueVariants := []struct {
		name       string
		provenance string
		extension  string
	}{
		{name: "duplicate", extension: "    opaque: first\n    opaque: second\n"},
		{name: "alias", provenance: "shared: &shared preserved\n", extension: "    opaque: *shared\n"},
		{name: "merge", provenance: "defaults: &defaults {opaque: preserved}\n", extension: "    <<: *defaults\n"},
	}
	for _, route := range routes {
		for _, opaque := range opaqueVariants {
			for _, operation := range []string{"ensure", "update", "remove"} {
				t.Run(route.name+"/"+opaque.name+"/"+operation, func(t *testing.T) {
					// Arrange.
					opaqueItem := route.missingItem(opaque.extension)
					source := []byte("---\ntype: Knowledge\n" + opaque.provenance +
						route.collection + ":\n" + opaqueItem + route.selected + "---\nBody\n")
					p, err := parsePresentationContext(context.Background(), source)
					if err != nil {
						t.Fatal(err)
					}
					collection := mappingValuesForTest(t, p.root, route.collection)[0]
					mutation := p.newYAMLMutationContext(p.ctx)

					// Act.
					switch operation {
					case "ensure":
						err = mutation.ensureSequenceItem(collection, route.addedSelect, route.added)
					case "update":
						err = mutation.ensureSequenceItem(collection, route.selector, route.updated)
					case "remove":
						err = mutation.removeSequenceItem(collection, route.selector)
					}
					var updated []byte
					if err == nil {
						updated, err = mutation.apply()
					}

					// Assert.
					if err != nil {
						t.Fatal(err)
					}
					if bytes.Count(updated, []byte(opaqueItem)) != 1 ||
						bytes.Count(updated, route.opaqueMarker) != 1 ||
						!bytes.Contains(updated, []byte(opaque.provenance)) {
						t.Fatalf("opaque missing-identity item changed:\n%s", updated)
					}
					if operation == "remove" && bytes.Contains(updated, []byte("selected")) {
						t.Fatalf("selected item remains:\n%s", updated)
					}
				})
			}
		}
	}
}

func TestYAMLMutation_AnonymousExactSelectorFailsOnUnresolvedOpaqueIdentity(t *testing.T) {
	tests := []struct {
		name       string
		provenance string
		extension  string
		code       string
	}{
		{name: "duplicate", extension: "    opaque: first\n    opaque: second\n", code: yamlCodeDuplicateTouchedKey},
		{name: "alias", provenance: "shared: &shared preserved\n", extension: "    opaque: *shared\n", code: yamlCodeAliasProvenance},
		{name: "merge", provenance: "defaults: &defaults {opaque: preserved}\n", extension: "    <<: *defaults\n", code: yamlCodeAliasProvenance},
	}
	exact := yamlMapping(yamlEntry("resource", yamlString("selected.md")))
	selector := yamlExactSelector(exact)
	for _, test := range tests {
		for _, operation := range []string{"select", "ensure", "remove"} {
			t.Run(test.name+"/"+operation, func(t *testing.T) {
				// Arrange.
				source := []byte("---\ntype: Knowledge\n" + test.provenance +
					"sources:\n" +
					"  - resource: opaque.md\n" + test.extension +
					"  - resource: selected.md\n" +
					"---\nBody\n")
				p, err := parsePresentationContext(context.Background(), source)
				if err != nil {
					t.Fatal(err)
				}
				sources := mappingValuesForTest(t, p.root, "sources")[0]
				mutation := p.newYAMLMutationContext(p.ctx)

				// Act.
				switch operation {
				case "select":
					_, _, err = p.selectSequenceItem(sources, selector)
				case "ensure":
					err = mutation.ensureSequenceItem(sources, selector, exact)
				case "remove":
					err = mutation.removeSequenceItem(sources, selector)
				}
				var updated []byte
				var applyErr error
				if operation == "select" {
					updated, applyErr = mutation.apply()
				} else {
					updated, applyErr = mutation.apply()
				}

				// Assert.
				var presentationErr *PresentationError
				if !errors.Is(err, ErrAmbiguousPresentation) ||
					!errors.As(err, &presentationErr) || presentationErr.Code != test.code {
					t.Fatalf("typed exact-selector error = %#v", err)
				}
				if operation == "select" {
					if applyErr != nil || !bytes.Equal(updated, source) || len(mutation.patches) != 0 {
						t.Fatalf("select failure changed mutation state: bytes=%q err=%v patches=%#v", updated, applyErr, mutation.patches)
					}
				} else {
					assertStructuralPresentationError(t, updated, applyErr, ErrAmbiguousPresentation, test.code, len(source))
					if len(mutation.patches) != 0 {
						t.Fatalf("exact-selector failure staged patches: %#v", mutation.patches)
					}
				}
			})
		}
	}
}
