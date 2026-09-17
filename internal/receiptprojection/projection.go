// Package receiptprojection derives the canonical semantic/file projection
// persisted in commit receipts and bound by migration authorization proofs.
package receiptprojection

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
)

// Projection is the canonical semantic/file delta persisted in a receipt.
type Projection struct {
	ChangedFiles []store.FileChange
	ChangedRefs  []bundle.RelationRef
}

// Derive compares two immutable captured bundles and returns canonical,
// non-nil receipt arrays. Pure byte-identical moves are represented as
// renames; all semantic refs whose meaning changed are returned in lexical
// canonical order.
func Derive(
	ctx context.Context,
	base *bundle.Bundle,
	target *bundle.Bundle,
) (Projection, error) {
	if base == nil || target == nil {
		return Projection{}, fmt.Errorf("receipt projection: nil bundle")
	}
	baseFiles, err := readFileInventory(ctx, base)
	if err != nil {
		return Projection{}, err
	}
	targetFiles, err := readFileInventory(ctx, target)
	if err != nil {
		return Projection{}, err
	}
	files, err := deriveFiles(ctx, base, target, baseFiles, targetFiles, capturedFilesEqual)
	if err != nil {
		return Projection{}, err
	}
	refs, err := deriveRefs(ctx, base, target)
	if err != nil {
		return Projection{}, err
	}
	return Projection{ChangedFiles: files, ChangedRefs: refs}, nil
}

type capturedMetadata struct {
	size   uint64
	sha256 [sha256.Size]byte
}

type filesEqualFunc func(context.Context, *bundle.Bundle, string, *bundle.Bundle, string) (bool, error)

func readFileInventory(ctx context.Context, source *bundle.Bundle) (map[string]capturedMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	paths := source.Files()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make(map[string]capturedMetadata, len(paths))
	previous := ""
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if bundle.ValidateRevisionPath(path) != nil || path == previous {
			return nil, fmt.Errorf("receipt projection: invalid or duplicate path %q", path)
		}
		size, digest, ok := source.CapturedFileMetadata(path)
		if !ok {
			return nil, fmt.Errorf("receipt projection: captured metadata for %q is missing", path)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out[path] = capturedMetadata{size: size, sha256: digest}
		previous = path
	}
	return out, ctx.Err()
}

func deriveFiles(
	ctx context.Context,
	baseBundle, targetBundle *bundle.Bundle,
	base, target map[string]capturedMetadata,
	equal filesEqualFunc,
) ([]store.FileChange, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]store.FileChange, 0)
	created := make(map[string]capturedMetadata)
	removed := make(map[string]capturedMetadata)
	targetPaths, err := sortedMapPaths(ctx, target)
	if err != nil {
		return nil, err
	}
	for _, path := range targetPaths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		targetMetadata := target[path]
		baseMetadata, exists := base[path]
		switch {
		case !exists:
			created[path] = targetMetadata
		case baseMetadata != targetMetadata:
			out = append(out, store.FileChange{Kind: store.FileWrite, Path: path})
		default:
			exact, err := equal(ctx, baseBundle, path, targetBundle, path)
			if err != nil {
				return nil, err
			}
			if !exact {
				out = append(out, store.FileChange{Kind: store.FileWrite, Path: path})
			}
		}
	}
	basePaths, err := sortedMapPaths(ctx, base)
	if err != nil {
		return nil, err
	}
	for _, path := range basePaths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		baseMetadata := base[path]
		if _, exists := target[path]; !exists {
			removed[path] = baseMetadata
		}
	}
	createdPaths, err := sortedMapPaths(ctx, created)
	if err != nil {
		return nil, err
	}
	renameCandidates, err := newRenameCandidateIndex(ctx, removed)
	if err != nil {
		return nil, err
	}
	for _, to := range createdPaths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		from, renamed, err := renameCandidates.takeFirstExact(
			ctx,
			created[to],
			func(ctx context.Context, candidate string) (bool, error) {
				return equal(ctx, baseBundle, candidate, targetBundle, to)
			},
		)
		if err != nil {
			return nil, err
		}
		if !renamed {
			out = append(out, store.FileChange{Kind: store.FileWrite, Path: to})
			continue
		}
		out = append(out, store.FileChange{Kind: store.FileRename, Path: to, From: from})
		delete(removed, from)
	}
	removedPaths, err := sortedMapPaths(ctx, removed)
	if err != nil {
		return nil, err
	}
	for _, path := range removedPaths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out = append(out, store.FileChange{Kind: store.FileDelete, Path: path})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := sortContext(ctx, out, func(left, right store.FileChange) bool {
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		return left.From < right.From
	}); err != nil {
		return nil, err
	}
	return out, nil
}

type renameCandidateBucket struct {
	paths []string
	next  []int
	head  int
}

type renameCandidateIndex struct {
	byMetadata map[capturedMetadata]int
	buckets    []renameCandidateBucket
}

func newRenameCandidateIndex(
	ctx context.Context,
	removed map[string]capturedMetadata,
) (*renameCandidateIndex, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	index := &renameCandidateIndex{
		byMetadata: make(map[capturedMetadata]int),
		buckets:    make([]renameCandidateBucket, 0),
	}
	for path, metadata := range removed {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		bucketIndex, exists := index.byMetadata[metadata]
		if !exists {
			bucketIndex = len(index.buckets)
			index.byMetadata[metadata] = bucketIndex
			index.buckets = append(index.buckets, renameCandidateBucket{head: -1})
		}
		index.buckets[bucketIndex].paths = append(index.buckets[bucketIndex].paths, path)
	}
	for bucketIndex := range index.buckets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		bucket := &index.buckets[bucketIndex]
		if err := sortContext(ctx, bucket.paths, func(left, right string) bool { return left < right }); err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		bucket.next = make([]int, len(bucket.paths))
		for candidateIndex := range bucket.paths {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			bucket.next[candidateIndex] = candidateIndex + 1
		}
		if len(bucket.paths) > 0 {
			bucket.head = 0
			bucket.next[len(bucket.next)-1] = -1
		}
	}
	return index, ctx.Err()
}

func (index *renameCandidateIndex) takeFirstExact(
	ctx context.Context,
	metadata capturedMetadata,
	exact func(context.Context, string) (bool, error),
) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	bucketIndex, exists := index.byMetadata[metadata]
	if !exists {
		return "", false, nil
	}
	bucket := &index.buckets[bucketIndex]
	previous := -1
	for candidateIndex := bucket.head; candidateIndex >= 0; candidateIndex = bucket.next[candidateIndex] {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		matches, err := exact(ctx, bucket.paths[candidateIndex])
		if err != nil {
			return "", false, err
		}
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		if !matches {
			previous = candidateIndex
			continue
		}
		next := bucket.next[candidateIndex]
		if previous < 0 {
			bucket.head = next
		} else {
			bucket.next[previous] = next
		}
		bucket.next[candidateIndex] = -1
		return bucket.paths[candidateIndex], true, nil
	}
	return "", false, ctx.Err()
}

func sortedMapPaths[V any](ctx context.Context, values map[string]V) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(values))
	for path := range values {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out = append(out, path)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := sortContext(ctx, out, func(left, right string) bool { return left < right }); err != nil {
		return nil, err
	}
	return out, nil
}

type semanticConcept struct {
	id           bundle.ConceptID
	path         string
	subresources []string
	relations    []bundle.Relation
}

func readSemanticInventory(ctx context.Context, current *bundle.Bundle) (map[string]semanticConcept, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ids, err := current.ConceptIDsContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make(map[string]semanticConcept, len(ids))
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		subresources, err := current.SubresourcesContext(ctx, id)
		if err != nil {
			return nil, err
		}
		relations, err := current.SemanticLinksFromContext(ctx, id)
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out[id.String()] = semanticConcept{
			id:           id,
			path:         id.ToPath(current.Root()),
			subresources: subresources,
			relations:    relations,
		}
	}
	return out, ctx.Err()
}

func deriveRefs(ctx context.Context, base, target *bundle.Bundle) ([]bundle.RelationRef, error) {
	baseConcepts, err := readSemanticInventory(ctx, base)
	if err != nil {
		return nil, err
	}
	targetConcepts, err := readSemanticInventory(ctx, target)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	seen := make(map[relationRefKey]bundle.RelationRef)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(baseConcepts)+len(targetConcepts))
	for id := range baseConcepts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	for id := range targetConcepts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, exists := baseConcepts[id]; !exists {
			ids = append(ids, id)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := sortContext(ctx, ids, func(left, right string) bool { return left < right }); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		baseConcept, baseOK := baseConcepts[id]
		targetConcept, targetOK := targetConcepts[id]
		changed := !baseOK || !targetOK
		if !changed {
			exact, err := capturedFilesEqual(
				ctx,
				base,
				baseConcept.path,
				target,
				targetConcept.path,
			)
			if err != nil {
				return nil, err
			}
			changed = !exact
		}
		if changed {
			if err := addConceptRefs(ctx, seen, baseConcepts, id); err != nil {
				return nil, err
			}
			if err := addConceptRefs(ctx, seen, targetConcepts, id); err != nil {
				return nil, err
			}
		}
	}
	baseRelations, err := relationInventory(ctx, baseConcepts, ids)
	if err != nil {
		return nil, err
	}
	targetRelations, err := relationInventory(ctx, targetConcepts, ids)
	if err != nil {
		return nil, err
	}
	for key, relation := range baseRelations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, ok := targetRelations[key]; !ok {
			if err := addRef(ctx, seen, relation.Source); err != nil {
				return nil, err
			}
			if err := addRef(ctx, seen, relation.Target); err != nil {
				return nil, err
			}
		}
	}
	for key, relation := range targetRelations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, ok := baseRelations[key]; !ok {
			if err := addRef(ctx, seen, relation.Source); err != nil {
				return nil, err
			}
			if err := addRef(ctx, seen, relation.Target); err != nil {
				return nil, err
			}
		}
	}
	return canonicalRefs(ctx, seen)
}

func addConceptRefs(
	ctx context.Context,
	seen map[relationRefKey]bundle.RelationRef,
	concepts map[string]semanticConcept,
	id string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	concept, ok := concepts[id]
	if !ok {
		return nil
	}
	if err := addRef(ctx, seen, bundle.RelationRef{ID: concept.id}); err != nil {
		return err
	}
	for _, fragment := range concept.subresources {
		if err := addRef(ctx, seen, bundle.RelationRef{ID: concept.id, Fragment: fragment}); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func addRef(ctx context.Context, seen map[relationRefKey]bundle.RelationRef, ref bundle.RelationRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	key := relationRefKeyOf(ref)
	if key.id != "" {
		seen[key] = ref
	}
	return nil
}

type relationRefKey struct {
	id       string
	fragment string
}

func relationRefKeyOf(ref bundle.RelationRef) relationRefKey {
	return relationRefKey{id: ref.ID.String(), fragment: ref.Fragment}
}

func (left relationRefKey) less(right relationRefKey) bool {
	if left.id != right.id {
		return left.id < right.id
	}
	return left.fragment < right.fragment
}

type relationKey struct {
	source relationRefKey
	typ    string
	target relationRefKey
}

func relationInventory(
	ctx context.Context,
	concepts map[string]semanticConcept,
	ids []string,
) (map[relationKey]bundle.Relation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make(map[relationKey]bundle.Relation)
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		concept, ok := concepts[id]
		if !ok {
			continue
		}
		for _, relation := range concept.relations {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			out[relationKey{
				source: relationRefKeyOf(relation.Source),
				typ:    relation.Type,
				target: relationRefKeyOf(relation.Target),
			}] = relation
		}
	}
	return out, ctx.Err()
}

func canonicalRefs(
	ctx context.Context,
	seen map[relationRefKey]bundle.RelationRef,
) ([]bundle.RelationRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]bundle.RelationRef, 0, len(seen))
	for _, ref := range seen {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	if err := sortContext(ctx, out, func(left, right bundle.RelationRef) bool {
		return left.String() < right.String()
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// sortContext is a stable bottom-up merge sort with a cancellation boundary
// before every element transfer. Callers discard the partially ordered slice
// on error, so cancellation cannot leave externally visible partial evidence.
func sortContext[T any](ctx context.Context, values []T, less func(T, T) bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(values) < 2 {
		return nil
	}
	scratch := make([]T, len(values))
	source, destination := values, scratch
	sourceIsScratch := false
	for width := 1; width < len(values); {
		for left := 0; left < len(values); {
			middle := left + min(width, len(values)-left)
			right := middle + min(width, len(values)-middle)
			first, second, output := left, middle, left
			for first < middle && second < right {
				if err := ctx.Err(); err != nil {
					return err
				}
				if less(source[second], source[first]) {
					destination[output] = source[second]
					second++
				} else {
					destination[output] = source[first]
					first++
				}
				output++
			}
			for first < middle {
				if err := ctx.Err(); err != nil {
					return err
				}
				destination[output] = source[first]
				first++
				output++
			}
			for second < right {
				if err := ctx.Err(); err != nil {
					return err
				}
				destination[output] = source[second]
				second++
				output++
			}
			left = right
		}
		source, destination = destination, source
		sourceIsScratch = !sourceIsScratch
		if width >= len(values)-width {
			width = len(values)
		} else {
			width *= 2
		}
	}
	if sourceIsScratch {
		for index := range source {
			if err := ctx.Err(); err != nil {
				return err
			}
			values[index] = source[index]
		}
	}
	return ctx.Err()
}

func capturedFilesEqual(
	ctx context.Context,
	left *bundle.Bundle,
	leftPath string,
	right *bundle.Bundle,
	rightPath string,
) (bool, error) {
	if _, _, ok := left.CapturedFileMetadata(leftPath); !ok {
		return false, fmt.Errorf("receipt projection: captured metadata for %q is missing", leftPath)
	}
	if _, _, ok := right.CapturedFileMetadata(rightPath); !ok {
		return false, fmt.Errorf("receipt projection: captured metadata for %q is missing", rightPath)
	}
	return bundle.CapturedFilesEqualContext(ctx, left, leftPath, right, rightPath)
}
