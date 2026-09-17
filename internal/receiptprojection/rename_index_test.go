package receiptprojection

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
)

func TestDeriveFilesDistinctRenameCandidatesStayLinear(t *testing.T) {
	// Arrange. Disjoint metadata means exact comparison is unnecessary. The
	// previous implementation nevertheless rebuilt and sorted every removed
	// path for every created path, allocating quadratically.
	const fileCount = 4096
	base := make(map[string]capturedMetadata, fileCount)
	target := make(map[string]capturedMetadata, fileCount)
	for index := range fileCount {
		base[fmt.Sprintf("old/%04d.asset", index)] = capturedMetadata{size: uint64(index*2 + 1)}
		target[fmt.Sprintf("new/%04d.asset", index)] = capturedMetadata{size: uint64(index*2 + 2)}
	}
	comparisons := 0
	equal := func(context.Context, *bundle.Bundle, string, *bundle.Bundle, string) (bool, error) {
		comparisons++
		return false, nil
	}
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	// Act.
	changes, err := deriveFiles(context.Background(), nil, nil, base, target, equal)
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(base)
	runtime.KeepAlive(target)

	// Assert.
	if err != nil {
		t.Fatalf("deriveFiles() error = %v", err)
	}
	if comparisons != 0 {
		t.Fatalf("exact comparisons = %d, want 0 for disjoint metadata buckets", comparisons)
	}
	if len(changes) != fileCount*2 {
		t.Fatalf("changes = %d, want %d writes and deletes", len(changes), fileCount*2)
	}
	const allocationLimit = 32 << 20
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated >= allocationLimit {
		t.Fatalf("deriveFiles() TotalAlloc = %d bytes, want < %d bytes", allocated, allocationLimit)
	}
}

func TestDeriveFilesDuplicateContentConsumesLexicalCandidatesOnce(t *testing.T) {
	// Arrange.
	metadata := capturedMetadata{size: 4}
	base := map[string]capturedMetadata{
		"old-a.asset": metadata,
		"old-b.asset": metadata,
		"old-c.asset": metadata,
	}
	target := map[string]capturedMetadata{
		"new-x.asset": metadata,
		"new-y.asset": metadata,
	}
	var comparisons [][2]string
	equal := func(
		_ context.Context,
		_ *bundle.Bundle,
		leftPath string,
		_ *bundle.Bundle,
		rightPath string,
	) (bool, error) {
		comparisons = append(comparisons, [2]string{leftPath, rightPath})
		return true, nil
	}
	wantChanges := []store.FileChange{
		{Kind: store.FileRename, Path: "new-x.asset", From: "old-a.asset"},
		{Kind: store.FileRename, Path: "new-y.asset", From: "old-b.asset"},
		{Kind: store.FileDelete, Path: "old-c.asset"},
	}
	wantComparisons := [][2]string{
		{"old-a.asset", "new-x.asset"},
		{"old-b.asset", "new-y.asset"},
	}

	// Act.
	changes, err := deriveFiles(context.Background(), nil, nil, base, target, equal)

	// Assert.
	if err != nil {
		t.Fatalf("deriveFiles() error = %v", err)
	}
	if !reflect.DeepEqual(changes, wantChanges) {
		t.Fatalf("deriveFiles() = %#v, want %#v", changes, wantChanges)
	}
	if !reflect.DeepEqual(comparisons, wantComparisons) {
		t.Fatalf("exact comparisons = %#v, want %#v", comparisons, wantComparisons)
	}
}

func TestDeriveFilesMetadataCollisionPreservesFirstRemainingExactMatch(t *testing.T) {
	// Arrange. All candidates deliberately share metadata, but exact content
	// equivalence differs per created path.
	metadata := capturedMetadata{size: 4}
	base := map[string]capturedMetadata{
		"old-a.asset": metadata,
		"old-b.asset": metadata,
		"old-c.asset": metadata,
	}
	target := map[string]capturedMetadata{
		"new-x.asset": metadata,
		"new-y.asset": metadata,
	}
	var comparisons [][2]string
	equal := func(
		_ context.Context,
		_ *bundle.Bundle,
		leftPath string,
		_ *bundle.Bundle,
		rightPath string,
	) (bool, error) {
		comparisons = append(comparisons, [2]string{leftPath, rightPath})
		switch rightPath {
		case "new-x.asset":
			return leftPath == "old-b.asset", nil
		case "new-y.asset":
			return leftPath == "old-a.asset" || leftPath == "old-c.asset", nil
		default:
			return false, nil
		}
	}
	wantChanges := []store.FileChange{
		{Kind: store.FileRename, Path: "new-x.asset", From: "old-b.asset"},
		{Kind: store.FileRename, Path: "new-y.asset", From: "old-a.asset"},
		{Kind: store.FileDelete, Path: "old-c.asset"},
	}
	wantComparisons := [][2]string{
		{"old-a.asset", "new-x.asset"},
		{"old-b.asset", "new-x.asset"},
		{"old-a.asset", "new-y.asset"},
	}

	// Act.
	changes, err := deriveFiles(context.Background(), nil, nil, base, target, equal)

	// Assert.
	if err != nil {
		t.Fatalf("deriveFiles() error = %v", err)
	}
	if !reflect.DeepEqual(changes, wantChanges) {
		t.Fatalf("deriveFiles() = %#v, want %#v", changes, wantChanges)
	}
	if !reflect.DeepEqual(comparisons, wantComparisons) {
		t.Fatalf("exact comparisons = %#v, want %#v", comparisons, wantComparisons)
	}
}

func TestRenameCandidateIndexCancellationInterruptsComparisonBucket(t *testing.T) {
	// Arrange.
	const candidateCount = 1024
	metadata := capturedMetadata{size: 4}
	removed := make(map[string]capturedMetadata, candidateCount)
	for index := range candidateCount {
		removed[fmt.Sprintf("old-%04d.asset", index)] = metadata
	}
	index, err := newRenameCandidateIndex(context.Background(), removed)
	if err != nil {
		t.Fatal(err)
	}
	ctx := newCancelAfterChecksContext(129)
	comparisons := 0

	// Act.
	_, matched, err := index.takeFirstExact(
		ctx,
		metadata,
		func(context.Context, string) (bool, error) {
			comparisons++
			return false, nil
		},
	)

	// Assert.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("takeFirstExact() error = %v, want context.Canceled", err)
	}
	if matched {
		t.Fatal("takeFirstExact() matched a forced non-match")
	}
	if comparisons == 0 || comparisons >= candidateCount {
		t.Fatalf("exact comparisons = %d, want a bounded partial scan", comparisons)
	}
	if got := ctx.checks.Load(); got != 129 {
		t.Fatalf("context checks = %d, want deterministic cancellation at 129", got)
	}
}

func TestRenameCandidateIndexCancellationInterruptsIndexing(t *testing.T) {
	// Arrange.
	const candidateCount = 1024
	removed := make(map[string]capturedMetadata, candidateCount)
	for index := range candidateCount {
		removed[fmt.Sprintf("old-%04d.asset", index)] = capturedMetadata{size: uint64(index + 1)}
	}
	ctx := newCancelAfterChecksContext(129)

	// Act.
	index, err := newRenameCandidateIndex(ctx, removed)

	// Assert.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("newRenameCandidateIndex() error = %v, want context.Canceled", err)
	}
	if index != nil {
		t.Fatalf("newRenameCandidateIndex() = %#v, want nil on cancellation", index)
	}
	if got := ctx.checks.Load(); got != 129 {
		t.Fatalf("context checks = %d, want deterministic cancellation at 129", got)
	}
}
