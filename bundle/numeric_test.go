package bundle

import (
	"errors"
	"math"
	"testing"
)

func TestParseNonNegativeYAMLUint64(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw  string
		want uint64
	}{
		{raw: "0", want: 0},
		{raw: "-0", want: 0},
		{raw: "+1", want: 1},
		{raw: "1_000", want: 1000},
		{raw: "0x10", want: 16},
		{raw: "0o10", want: 8},
		{raw: "0b10", want: 2},
		{raw: "18446744073709551615", want: math.MaxUint64},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			t.Parallel()

			// Act.
			got, err := ParseNonNegativeYAMLUint64(tt.raw)

			// Assert.
			if err != nil || got != tt.want {
				t.Fatalf("ParseNonNegativeYAMLUint64(%q) = %d, %v; want %d", tt.raw, got, err, tt.want)
			}
		})
	}
}

func TestParseNonNegativeYAMLUint64RejectsInvalidOrOverflow(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"-1", "18446744073709551616", "1__0", "0x", "not-int"} {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()

			// Act.
			_, err := ParseNonNegativeYAMLUint64(raw)

			// Assert.
			if !errors.Is(err, ErrInvalidYAMLUint64) {
				t.Fatalf("ParseNonNegativeYAMLUint64(%q) error = %v", raw, err)
			}
		})
	}
}
