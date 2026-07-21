package fs

import (
	"errors"
	"testing"
)

func TestUnsupportedPlatformErrorContract(t *testing.T) {
	// Arrange.
	err := &UnsupportedPlatformError{GOOS: "plan9", GOARCH: "amd64"}

	// Act.
	isUnsupported := errors.Is(err, ErrUnsupportedPlatform)
	var detail *UnsupportedPlatformError
	asDetail := errors.As(err, &detail)

	// Assert.
	if !isUnsupported {
		t.Fatal("errors.Is() = false, want ErrUnsupportedPlatform")
	}
	if !asDetail || detail.GOOS != "plan9" || detail.GOARCH != "amd64" {
		t.Fatalf("errors.As() = (%t, %#v), want plan9/amd64 detail", asDetail, detail)
	}
	const want = "fs store: unsupported platform: plan9/amd64 (supported: darwin, linux)"
	if got := err.Error(); got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}
