package mutation

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParsePresentationPreservesSplitCancellation(t *testing.T) {
	tests := []struct {
		name      string
		data      []byte
		remaining int
	}{
		{name: "invalid utf8 classification", data: append([]byte("---\n\xff"), []byte(strings.Repeat("x", 256<<10))...), remaining: 12},
		{name: "unterminated frontmatter classification", data: []byte("---\n" + strings.Repeat("x", 256<<10)), remaining: 74},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := &countdownContext{Context: context.Background(), remaining: test.remaining}

			got, err := parsePresentationContext(ctx, test.data)

			if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(got, (*presentation)(nil)) {
				t.Fatalf("parsePresentationContext() = (%#v, %v), want nil and context.Canceled", got, err)
			}
		})
	}
}
