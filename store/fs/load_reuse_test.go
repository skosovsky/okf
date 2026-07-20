package fs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/skosovsky/okf/bundle"
)

type snapshotCountdownContext struct {
	context.Context
	mu        sync.Mutex
	remaining int
}

func (c *snapshotCountdownContext) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.remaining > 0 {
		c.remaining--
	}
	if c.remaining == 0 {
		return context.Canceled
	}
	return nil
}

type countedValidationSource struct {
	mu        sync.Mutex
	paths     []string
	files     map[string][]byte
	pathCalls int
	readCalls int
}

func TestSnapshotSourceCancelsDuringTraversalAndCopy(t *testing.T) {
	// Arrange.
	files := make(map[string][]byte, 257)
	for i := 0; i < 256; i++ {
		files[fmt.Sprintf("asset%03d.bin", i)] = []byte("content")
	}
	files["large.bin"] = make([]byte, 1<<20)
	snapshot, err := newSnapshot(context.Background(), files)
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	paths, pathsErr := snapshot.Paths(&snapshotCountdownContext{Context: context.Background(), remaining: 8})
	data, readErr := snapshot.ReadFile(&snapshotCountdownContext{Context: context.Background(), remaining: 8}, "large.bin")

	// Assert.
	if !errors.Is(pathsErr, context.Canceled) || paths != nil {
		t.Fatalf("Paths() = %#v, %v", paths, pathsErr)
	}
	if !errors.Is(readErr, context.Canceled) || data != nil {
		t.Fatalf("ReadFile() = %d bytes, %v", len(data), readErr)
	}
}

func (s *countedValidationSource) Paths(context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pathCalls++
	return append([]string(nil), s.paths...), nil
}

func (s *countedValidationSource) ReadFile(_ context.Context, name string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readCalls++
	return append([]byte(nil), s.files[name]...), nil
}

func TestValidateStagedSourceLoadsOnceAndKeepsRelationDiagnostics(t *testing.T) {
	// Arrange.
	source := &countedValidationSource{
		paths: []string{"a.md"},
		files: map[string][]byte{"a.md": []byte("---\ntype: Note\nrelations:\n  uses:\n    - target: missing\n---\nA\n")},
	}
	s := &Store{}

	// Act.
	report, blocked, loaded, err := s.validateStagedSource(context.Background(), source)

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil {
		t.Fatal("validateStagedSource() returned nil bundle")
	}
	if source.pathCalls != 1 || source.readCalls != 1 {
		t.Fatalf("source calls = Paths:%d ReadFile:%d, want one bundle load", source.pathCalls, source.readCalls)
	}
	if !blocked {
		t.Fatalf("blocked = false, report = %#v", report)
	}
	if got := loaded.RelationDiagnostics(); len(got) != 1 || got[0].Code != "missing_target_concept" {
		t.Fatalf("loaded relation diagnostics = %#v", got)
	}
}

var _ bundle.Source = (*countedValidationSource)(nil)
