package bundle

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestMarkdownLimitPublicMarkdownResourceContract(t *testing.T) {
	t.Parallel()

	// Arrange.
	err := &MarkdownResourceLimitError{
		Kind:     MarkdownResourceDocumentBytes,
		Limit:    MaxMarkdownDocumentBytes,
		Observed: MaxMarkdownDocumentBytes + 1,
	}

	// Act/Assert.
	if !errors.Is(err, ErrMarkdownResourceLimit) {
		t.Fatalf("errors.Is(error, ErrMarkdownResourceLimit) = false: %v", err)
	}
	if MaxMarkdownDocumentBytes != 16<<20 || MaxMarkdownBodyBytes != MaxMarkdownDocumentBytes {
		t.Fatalf("public limits = document %d, body %d", MaxMarkdownDocumentBytes, MaxMarkdownBodyBytes)
	}
	if err.Kind != MarkdownResourceDocumentBytes || err.Limit != 16<<20 || err.Observed != (16<<20)+1 {
		t.Fatalf("typed error = %#v", err)
	}
}

func TestMarkdownLimitPublicMarkdownCallersRejectOversizeWithoutPublication(t *testing.T) {
	t.Parallel()

	// Arrange.
	over := strings.Repeat("a", MaxMarkdownBodyBytes+1)
	document := Document{Body: over}

	// Act/Assert.
	if links, err := ExtractLinksContext(context.Background(), over); links != nil || !errors.Is(err, ErrMarkdownResourceLimit) {
		t.Fatalf("ExtractLinksContext() = (%#v, %v)", links, err)
	}
	if values, err := document.AttributionsContext(context.Background()); values != nil || !errors.Is(err, ErrMarkdownResourceLimit) {
		t.Fatalf("AttributionsContext() = (%#v, %v)", values, err)
	}
	if observation, err := document.LegacyFallbackObservationContext(context.Background()); observation != (LegacyFallbackObservation{}) || !errors.Is(err, ErrMarkdownResourceLimit) {
		t.Fatalf("LegacyFallbackObservationContext() = (%#v, %v)", observation, err)
	}
}
