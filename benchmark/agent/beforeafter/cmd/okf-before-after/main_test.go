package main

import (
	"bytes"
	"testing"
)

func TestBoundedAdapterBufferCapsOutput(t *testing.T) {
	// Arrange.
	var b boundedBuffer
	chunk := bytes.Repeat([]byte{'x'}, 2<<20)
	// Act.
	n1, e1 := b.Write(chunk)
	n2, e2 := b.Write([]byte{'y'})
	// Assert.
	if e1 != nil || e2 != nil || n1 != len(chunk) || n2 != 1 || !b.exceeded || len(b.b) != 2<<20 {
		t.Fatalf("output cap failed: len=%d exceeded=%t", len(b.b), b.exceeded)
	}
}
