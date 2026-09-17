package fs

// Close-error precedence at recovery boundaries.

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/skosovsky/okf/store"
)

func TestStructuralCloseFailurePreservesOrderedFacts(t *testing.T) {
	for _, name := range []string{"nonregular", "declared size", "bounded read limit", "identity"} {
		t.Run(name, func(t *testing.T) {
			// Arrange.
			file, err := os.CreateTemp(t.TempDir(), "close-precedence-")
			if err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			structural := errors.New(name + " mismatch")

			// Act.
			joined := closeStructuralMetadata(file, structural)

			// Assert.
			if !errors.Is(joined, os.ErrClosed) || !errors.Is(joined, structural) || !errors.Is(joined, store.ErrStorageCorrupt) || strings.Index(joined.Error(), "close ") > strings.Index(joined.Error(), structural.Error()) {
				t.Fatalf("joined=%v, want close I/O first plus structural corruption", joined)
			}
		})
	}
}

func TestCloseOnlyAndCancellationRemainOperational(t *testing.T) {
	// Arrange.
	file, err := os.CreateTemp(t.TempDir(), "close-control-")
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	// Act.
	closeErr := closeStructuralMetadata(file, nil)
	withCancel := errors.Join(closeErr, context.Canceled)

	// Assert.
	if !errors.Is(closeErr, os.ErrClosed) || errors.Is(closeErr, store.ErrStorageCorrupt) || !errors.Is(withCancel, os.ErrClosed) || !errors.Is(withCancel, context.Canceled) || errors.Is(withCancel, store.ErrStorageCorrupt) || strings.Index(withCancel.Error(), "close ") > strings.Index(withCancel.Error(), context.Canceled.Error()) {
		t.Fatalf("close=%v joined=%v, want operational close before cancellation", closeErr, withCancel)
	}
}
