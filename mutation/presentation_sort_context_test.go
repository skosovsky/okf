package mutation

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"gopkg.in/yaml.v3"
)

func TestDuplicateRelationTargetOrderingContextCancellation(t *testing.T) {
	t.Run("pre-cancel", func(t *testing.T) {
		// Arrange.
		items, id := largeUniqueRelationTargetSequence(t, 128)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		// Act.
		duplicates, err := duplicateRelationTargetsInSequenceContext(ctx, items, id, "")

		// Assert.
		if duplicates != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("duplicateRelationTargetsInSequenceContext() = %d nodes, %v", len(duplicates), err)
		}
	})

	t.Run("mid large projection", func(t *testing.T) {
		// Arrange.
		items, id := largeUniqueRelationTargetSequence(t, 8_192)
		ctx := &countdownContext{Context: context.Background(), remaining: 256}

		// Act.
		duplicates, err := duplicateRelationTargetsInSequenceContext(ctx, items, id, "")

		// Assert.
		if duplicates != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("duplicateRelationTargetsInSequenceContext() = %d nodes, %v", len(duplicates), err)
		}
	})

	t.Run("chunked comparator", func(t *testing.T) {
		// Arrange.
		large := string(make([]byte, 4<<20))
		ordered := []relationTargetProjection{
			{key: relationRefKey{id: "source", fragment: large + "b"}, ordinal: 1},
			{key: relationRefKey{id: "source", fragment: large + "a"}, ordinal: 0},
		}
		ctx := &countdownContext{Context: context.Background(), remaining: 4}

		// Act.
		err := sortSliceCompareContext(ctx, ordered, compareRelationTargetProjectionContext)

		// Assert.
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("sortSliceCompareContext() error = %v", err)
		}
	})
}

func TestDuplicateRelationTargetOrderingContextParity(t *testing.T) {
	// Arrange. The lexicographically first duplicate group is "a", while the
	// input puts both "z" candidates first; candidates within a group retain
	// their original order.
	id := ref(t, "source").ID
	items := relationTargetSequence(
		"source#z", "source#z", "source#a", "source#other", "source#a",
	)

	// Act.
	duplicates, err := duplicateRelationTargetsInSequenceContext(context.Background(), items, id, "")

	// Assert.
	if err != nil || len(duplicates) != 2 || duplicates[0].Value != "source#a" || duplicates[1].Value != "source#a" {
		t.Fatalf("duplicate ordering = %#v, %v", duplicates, err)
	}
}

func TestSupersedeDescendantStateContextCancellationIsAtomic(t *testing.T) {
	t.Run("pre-cancel", func(t *testing.T) {
		// Arrange.
		mutation, collection := largeDescendantCleanupMutation(64)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		mutation.ctx = ctx
		before := snapshotYAMLMutationCleanupState(mutation)

		// Act.
		err := mutation.supersedeDescendantState(collection)

		// Assert.
		if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(before, snapshotYAMLMutationCleanupState(mutation)) {
			t.Fatalf("pre-canceled cleanup error/state = %v, %#v", err, snapshotYAMLMutationCleanupState(mutation))
		}
	})

	t.Run("sort helper observes comparator boundary cancellation", func(t *testing.T) {
		// Arrange.
		values := []int{3, 2, 1}
		ctx, cancel := context.WithCancel(context.Background())
		canceledAtComparator := false

		// Act.
		err := sortSliceContext(ctx, values, func(left, right int) bool {
			canceledAtComparator = true
			cancel()
			return left > right
		})

		// Assert.
		if !errors.Is(err, context.Canceled) || !canceledAtComparator {
			t.Fatalf("sortSliceContext() = canceledAtComparator=%t error=%v", canceledAtComparator, err)
		}
	})
}

func TestSupersedeDescendantStateContextParity(t *testing.T) {
	// Arrange.
	collection := &yaml.Node{Kind: yaml.MappingNode}
	descendant := &yaml.Node{Kind: yaml.ScalarNode}
	outside := &yaml.Node{Kind: yaml.ScalarNode}
	presentation := &presentation{paths: map[*yaml.Node][]int{
		collection: {1}, descendant: {1, 1}, outside: {3},
	}}
	mutation := &yamlMutation{
		ctx: context.Background(), p: presentation,
		patches:          []bytePatch{{Owner: descendant}, {Owner: collection}, {Owner: outside}},
		collectionPatch:  map[*yaml.Node]int{collection: 1},
		valuePatch:       map[*yaml.Node]int{descendant: 0, outside: 2},
		mappingAppends:   map[*yaml.Node][]*yamlMappingAppend{descendant: nil, outside: nil},
		sequenceShadows:  map[*yaml.Node]*yamlSequenceShadow{descendant: {}, outside: {}},
		collectionValues: map[*yaml.Node]yamlRenderValue{collection: {}, descendant: {}, outside: {}},
		collectionLineage: map[*yaml.Node][]yamlSequenceLineage{
			descendant: nil, outside: nil,
		},
		replacedSubtrees: map[*yaml.Node]bool{descendant: true, outside: true},
		insertions: []yamlScheduledInsertion{
			{owner: descendant, order: 1}, {owner: outside, order: 2},
		},
	}

	// Act.
	err := mutation.supersedeDescendantState(collection)

	// Assert.
	if err != nil || len(mutation.patches) != 2 || mutation.patches[0].Owner != collection || mutation.patches[1].Owner != outside {
		t.Fatalf("patch cleanup = %#v, %v", mutation.patches, err)
	}
	if mutation.collectionPatch[collection] != 0 || mutation.valuePatch[outside] != 1 {
		t.Fatalf("patch indexes = collection %#v, value %#v", mutation.collectionPatch, mutation.valuePatch)
	}
	if _, present := mutation.mappingAppends[descendant]; present || mutation.mappingAppends[outside] == nil && len(mutation.mappingAppends) != 1 {
		t.Fatalf("mapping cleanup = %#v", mutation.mappingAppends)
	}
	if _, present := mutation.sequenceShadows[descendant]; present || mutation.sequenceShadows[outside] == nil && len(mutation.sequenceShadows) != 1 {
		t.Fatalf("shadow cleanup = %#v", mutation.sequenceShadows)
	}
	if _, present := mutation.collectionValues[descendant]; present || len(mutation.collectionValues) != 2 {
		t.Fatalf("collection cleanup = %#v", mutation.collectionValues)
	}
	if _, present := mutation.replacedSubtrees[descendant]; present || !mutation.replacedSubtrees[outside] {
		t.Fatalf("replacement cleanup = %#v", mutation.replacedSubtrees)
	}
	if len(mutation.insertions) != 1 || mutation.insertions[0].owner != outside {
		t.Fatalf("insertion cleanup = %#v", mutation.insertions)
	}
}

type yamlMutationCleanupSnapshot struct {
	patches           []bytePatch
	valuePatch        map[*yaml.Node]int
	collectionPatch   map[*yaml.Node]int
	mappingAppends    map[*yaml.Node][]*yamlMappingAppend
	sequenceShadows   map[*yaml.Node]*yamlSequenceShadow
	collectionValues  map[*yaml.Node]yamlRenderValue
	collectionLineage map[*yaml.Node][]yamlSequenceLineage
	replacedSubtrees  map[*yaml.Node]bool
	insertions        []yamlScheduledInsertion
}

func snapshotYAMLMutationCleanupState(mutation *yamlMutation) yamlMutationCleanupSnapshot {
	return yamlMutationCleanupSnapshot{
		patches:           append([]bytePatch(nil), mutation.patches...),
		valuePatch:        cloneNodeIndexTestMap(mutation.valuePatch),
		collectionPatch:   cloneNodeIndexTestMap(mutation.collectionPatch),
		mappingAppends:    cloneTestMap(mutation.mappingAppends),
		sequenceShadows:   cloneTestMap(mutation.sequenceShadows),
		collectionValues:  cloneTestMap(mutation.collectionValues),
		collectionLineage: cloneTestMap(mutation.collectionLineage),
		replacedSubtrees:  cloneTestMap(mutation.replacedSubtrees),
		insertions:        append([]yamlScheduledInsertion(nil), mutation.insertions...),
	}
}

func largeDescendantCleanupMutation(count int) (*yamlMutation, *yaml.Node) {
	collection := &yaml.Node{Kind: yaml.MappingNode}
	paths := map[*yaml.Node][]int{collection: {1}}
	patches := make([]bytePatch, count)
	valuePatch := make(map[*yaml.Node]int, count)
	for index := range patches {
		owner := &yaml.Node{Kind: yaml.ScalarNode, Value: strconv.Itoa(index)}
		paths[owner] = []int{1, index}
		patches[index] = bytePatch{Owner: owner}
		valuePatch[owner] = index
	}
	presentation := &presentation{paths: paths}
	return &yamlMutation{
		ctx: context.Background(), p: presentation, patches: patches,
		valuePatch: valuePatch, collectionPatch: make(map[*yaml.Node]int),
		mappingAppends:    make(map[*yaml.Node][]*yamlMappingAppend),
		sequenceShadows:   make(map[*yaml.Node]*yamlSequenceShadow),
		collectionValues:  make(map[*yaml.Node]yamlRenderValue),
		collectionLineage: make(map[*yaml.Node][]yamlSequenceLineage),
		replacedSubtrees:  make(map[*yaml.Node]bool),
	}, collection
}

func largeUniqueRelationTargetSequence(t *testing.T, count int) (*yaml.Node, bundle.ConceptID) {
	t.Helper()
	id := ref(t, "source").ID
	targets := make([]string, count)
	for index := range targets {
		targets[index] = "source#fragment-" + strconv.Itoa(count-index)
	}
	return relationTargetSequence(targets...), id
}

func relationTargetSequence(targets ...string) *yaml.Node {
	items := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, target := range targets {
		items.Content = append(items.Content, yamlTestMapping("target", yamlTestScalar(target)))
	}
	return items
}

func cloneNodeIndexTestMap(source map[*yaml.Node]int) map[*yaml.Node]int {
	return cloneTestMap(source)
}

func cloneTestMap[K comparable, V any](source map[K]V) map[K]V {
	cloned := make(map[K]V, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
