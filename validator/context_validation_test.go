package validator

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/skosovsky/okf/bundle"
)

func TestValidateSourceCancelledBeforeValidation(t *testing.T) {
	// Arrange.
	source := validationSource{"concept.md": []byte("---\ntype: Note\n---\n\nBody\n")}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Act.
	_, err := ValidateSource(ctx, source, nil)

	// Assert.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ValidateSource() error = %v, want context.Canceled", err)
	}
}

func TestValidateBundleContextCancelledDuringTraversal(t *testing.T) {
	// Arrange.
	b := testValidationBundle(t, 128)
	ctx, cancel := context.WithCancel(context.Background())
	var checkpoints atomic.Int32
	cfg := &ValidatorConfig{checkpoint: func() {
		if checkpoints.Add(1) == 24 {
			cancel()
		}
	}}

	// Act.
	_, err := ValidateBundleContext(ctx, b, cfg)

	// Assert.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ValidateBundleContext() error = %v, want context.Canceled", err)
	}
	if got := checkpoints.Load(); got < 24 {
		t.Fatalf("validation checkpoints = %d, want traversal checkpoint", got)
	}
}

func testValidationBundle(t *testing.T, count int) *bundle.Bundle {
	t.Helper()
	source := make(validationSource, count)
	for i := 0; i < count; i++ {
		source[fmt.Sprintf("concept-%03d.md", i)] = []byte("---\ntype: Note\n---\n\nBody\n")
	}
	b, err := bundle.Load(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type validationSource map[string][]byte

func (s validationSource) Paths(context.Context) ([]string, error) {
	paths := make([]string, 0, len(s))
	for path := range s {
		paths = append(paths, path)
	}
	return paths, nil
}

func (s validationSource) ReadFile(_ context.Context, path string) ([]byte, error) {
	return append([]byte(nil), s[path]...), nil
}
