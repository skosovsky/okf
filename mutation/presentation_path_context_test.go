package mutation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestYAMLRenderValueAtPathContext_LargeIdentityRoutesAndCancellation(t *testing.T) {
	const itemCount = 2048
	tests := []struct {
		route string
		item  func(int) yamlRenderValue
	}{
		{route: "sources", item: func(index int) yamlRenderValue {
			return yamlMapping(yamlEntry("id", yamlString(fmt.Sprintf("source-%04d", index))))
		}},
		{route: "verified", item: func(index int) yamlRenderValue {
			return yamlMapping(
				yamlEntry("by", yamlString(fmt.Sprintf("verifier-%04d", index))),
				yamlEntry("at", yamlString("2026-08-07T00:00:00Z")),
			)
		}},
		{route: "parameters", item: func(index int) yamlRenderValue {
			return yamlMapping(yamlEntry("name", yamlString(fmt.Sprintf("parameter-%04d", index))))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.route, func(t *testing.T) {
			// Arrange.
			items := make([]yamlRenderValue, itemCount)
			for index := range items {
				items[index] = tt.item(index)
			}
			root := yamlSequence(items...)
			identity, err := sequenceIdentityContext(context.Background(), items[itemCount-1], tt.route)
			if err != nil {
				t.Fatalf("sequenceIdentityContext() error = %v", err)
			}
			steps := []yamlRenderPathStep{{route: tt.route, identity: identity, sequence: true}}

			// Act.
			got, found, lookupErr := yamlRenderValueAtPathContext(context.Background(), &root, steps)

			// Assert.
			if lookupErr != nil || !found || got != &root.sequence[itemCount-1] {
				t.Fatalf("yamlRenderValueAtPathContext() = %p, %t, %v", got, found, lookupErr)
			}
		})
	}

	t.Run("pre-cancelled", func(t *testing.T) {
		// Arrange.
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		root := yamlSequence(yamlMapping(yamlEntry("id", yamlString("a"))))
		identity, err := sequenceIdentityContext(context.Background(), root.sequence[0], "sources")
		if err != nil {
			t.Fatal(err)
		}

		// Act.
		got, found, lookupErr := yamlRenderValueAtPathContext(ctx, &root, []yamlRenderPathStep{{route: "sources", identity: identity, sequence: true}})

		// Assert.
		if got != nil || found || !errors.Is(lookupErr, context.Canceled) {
			t.Fatalf("yamlRenderValueAtPathContext() = %p, %t, %v", got, found, lookupErr)
		}
	})

	t.Run("mid-scan", func(t *testing.T) {
		// Arrange.
		items := make([]yamlRenderValue, itemCount)
		for index := range items {
			items[index] = yamlMapping(yamlEntry("id", yamlString(fmt.Sprintf("source-%04d", index))))
		}
		root := yamlSequence(items...)
		identity, err := sequenceIdentityContext(context.Background(), items[itemCount-1], "sources")
		if err != nil {
			t.Fatal(err)
		}
		ctx := &countdownContext{Context: context.Background(), remaining: 64}

		// Act.
		got, found, lookupErr := yamlRenderValueAtPathContext(ctx, &root, []yamlRenderPathStep{{route: "sources", identity: identity, sequence: true}})

		// Assert.
		if got != nil || found || !errors.Is(lookupErr, context.Canceled) {
			t.Fatalf("yamlRenderValueAtPathContext() = %p, %t, %v", got, found, lookupErr)
		}
	})
}

func TestFoldReplacementMappingEntryContext_SemanticParityAndCancellation(t *testing.T) {
	tests := []struct {
		name        string
		mode        string
		key         string
		value       yamlRenderValue
		wantPresent bool
		wantValue   yamlRenderValue
	}{
		{name: "replace", mode: "replace", key: "resource", value: yamlString("new.md"), wantPresent: true, wantValue: yamlString("new.md")},
		{name: "insert", mode: "insert", key: "extra", value: yamlString("added"), wantPresent: true, wantValue: yamlString("added")},
		{name: "delete", mode: "delete", key: "resource", wantPresent: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			presentation, sources, _ := shadowSourcePresentation(t, "sources:\n  - id: a\n    resource: old.md\n")
			mapping := sources.Content[0]
			mutation := presentation.newYAMLMutationContext(presentation.ctx)
			current, err := yamlRenderFromNodeIgnoringCommentsContext(mutation.ctx, mapping)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			changed, foldErr := mutation.foldReplacementMappingEntry(mapping, &current, tt.key, tt.value, tt.mode)
			got, present, lookupErr := yamlRenderMappingEntryContext(context.Background(), current, tt.key)

			// Assert.
			if foldErr != nil || lookupErr != nil || !changed || present != tt.wantPresent {
				t.Fatalf("fold/lookup = changed %t, present %t, errors %v/%v", changed, present, foldErr, lookupErr)
			}
			if tt.wantPresent {
				equal, equalErr := equalYAMLRenderStateContext(context.Background(), got, tt.wantValue)
				if equalErr != nil || !equal {
					t.Fatalf("value = %#v, equal=%t, error=%v", got, equal, equalErr)
				}
			}
		})
	}

	t.Run("pre-cancelled", func(t *testing.T) {
		// Arrange.
		presentation, sources, _ := shadowSourcePresentation(t, "sources:\n  - id: a\n    resource: old.md\n")
		mapping := sources.Content[0]
		ctx, cancel := context.WithCancel(context.Background())
		mutation := presentation.newYAMLMutationContext(ctx)
		current, err := yamlRenderFromNodeIgnoringCommentsContext(context.Background(), mapping)
		if err != nil {
			t.Fatal(err)
		}
		cancel()

		// Act.
		changed, foldErr := mutation.foldReplacementMappingEntry(mapping, &current, "resource", yamlString("new.md"), "replace")

		// Assert.
		if changed || !errors.Is(foldErr, context.Canceled) {
			t.Fatalf("foldReplacementMappingEntry() = %t, %v", changed, foldErr)
		}
	})

	t.Run("mid-scan", func(t *testing.T) {
		// Arrange.
		var body strings.Builder
		body.WriteString("sources:\n  - id: a\n    resource: old.md\n")
		for index := 0; index < 4096; index++ {
			fmt.Fprintf(&body, "    key-%04d: value-%04d\n", index, index)
		}
		presentation, sources, _ := shadowSourcePresentation(t, body.String())
		mapping := sources.Content[0]
		mutation := presentation.newYAMLMutationContext(presentation.ctx)
		current, err := yamlRenderFromNodeIgnoringCommentsContext(context.Background(), mapping)
		if err != nil {
			t.Fatal(err)
		}
		mutation.ctx = &countdownContext{Context: context.Background(), remaining: 64}

		// Act.
		changed, foldErr := mutation.foldReplacementMappingEntry(mapping, &current, "extra", yamlString("added"), "insert")

		// Assert.
		if changed || !errors.Is(foldErr, context.Canceled) {
			t.Fatalf("foldReplacementMappingEntry() = %t, %v", changed, foldErr)
		}
	})
}

func TestSequenceIdentityLookup_ConfirmsInjectedDigestBucketCollisions(t *testing.T) {
	// Arrange. The digest bucket is deliberately forged: full canonical bytes,
	// not the digest, must decide which candidate matches.
	target := yamlSequenceIdentity{kind: "sources", canonical: "target"}
	digest, err := stringFingerprintContext(context.Background(), target.canonical)
	if err != nil {
		t.Fatal(err)
	}
	target.canonicalDigest = digest
	lookup := sequenceIdentityLookup{
		identities: []yamlSequenceIdentity{
			{kind: "sources", canonical: "different", canonicalDigest: digest},
			target,
		},
		buckets: map[[32]byte][]int{digest: {0, 1}},
	}

	// Act.
	index, found, lookupErr := lookup.uniqueIndexContext(context.Background(), target)

	// Assert.
	if lookupErr != nil || !found || index != 1 {
		t.Fatalf("uniqueIndexContext() = %d, %t, %v", index, found, lookupErr)
	}
	lookup.identities = append(lookup.identities, target)
	lookup.buckets[digest] = append(lookup.buckets[digest], 2)
	if duplicateIndex, duplicateFound, duplicateErr := lookup.uniqueIndexContext(context.Background(), target); duplicateErr != nil || duplicateFound || duplicateIndex != 0 {
		t.Fatalf("duplicate uniqueIndexContext() = %d, %t, %v", duplicateIndex, duplicateFound, duplicateErr)
	}
}

func TestYAMLMutation_ShadowSourceIndexRejectsStaleOwners(t *testing.T) {
	// Arrange.
	presentation, sources, _ := shadowSourcePresentation(t, "sources:\n  - id: a\n    resource: old.md\n")
	mutation := presentation.newYAMLMutationContext(presentation.ctx)
	shadow, err := mutation.sequenceShadow(sources)
	if err != nil {
		t.Fatalf("sequenceShadow() error = %v", err)
	}
	item := shadow.items[0]
	item.dirty = true

	// Act and assert: the live sparse owner resolves.
	gotShadow, gotItem, _, found, pathErr := mutation.replacementShadowPath(item.source)
	if pathErr != nil || !found || gotShadow != shadow || gotItem != item {
		t.Fatalf("live replacementShadowPath() = %p, %p, %t, %v", gotShadow, gotItem, found, pathErr)
	}

	// Act and assert: deleting the owning shadow leaves a stale map entry which
	// must never resolve.
	delete(mutation.sequenceShadows, sources)
	gotShadow, gotItem, _, found, pathErr = mutation.replacementShadowPath(item.source)
	if pathErr != nil || found || gotShadow != nil || gotItem != nil {
		t.Fatalf("stale replacementShadowPath() = %p, %p, %t, %v", gotShadow, gotItem, found, pathErr)
	}

	// Act and assert: an inactive removed item also cannot resolve after the
	// shadow is restored.
	mutation.sequenceShadows[sources] = shadow
	item.active = false
	gotShadow, gotItem, _, found, pathErr = mutation.replacementShadowPath(item.source)
	if pathErr != nil || found || gotShadow != nil || gotItem != nil {
		t.Fatalf("inactive replacementShadowPath() = %p, %p, %t, %v", gotShadow, gotItem, found, pathErr)
	}
}
