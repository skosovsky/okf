package bundle

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestReachableObservationCancellationInsideScalarOwners(t *testing.T) {
	large := strings.Repeat("1", 2<<20)
	tests := []struct {
		name   string
		phase  string
		yaml   string
		act    func(context.Context, Frontmatter) (any, error)
		zero   any
		atMost int64
	}{
		{
			name:  "stale after date owner",
			phase: "dateValueFromNodeContext",
			yaml:  "stale_after: \"2026-01-01" + large + "\"\n",
			act: func(ctx context.Context, frontmatter Frontmatter) (any, error) {
				return frontmatter.StaleAfterObservationContext(ctx)
			},
			zero:   StaleAfterObservation{},
			atMost: 3,
		},
		{
			name:  "executor receipt nonblank owner",
			phase: "nonBlankStringValueContext",
			yaml:  "executor:\n  resource: runner.md\n  receipt: [\"" + large + "\"]\n",
			act: func(ctx context.Context, frontmatter Frontmatter) (any, error) {
				return frontmatter.ExecutorReceiptObservationContext(ctx)
			},
			zero:   ExecutorReceiptObservation{},
			atMost: 2,
		},
		{
			name:  "source usage count numeric owner",
			phase: "parseNonNegativeYAMLUint64Context",
			yaml:  "sources:\n  - resource: source.md\n    usage_count: 0x" + strings.Repeat("0", 2<<20) + "1\n",
			act: func(ctx context.Context, frontmatter Frontmatter) (any, error) {
				return frontmatter.SourceStatesContext(ctx)
			},
			zero:   []ProvenanceSourceState(nil),
			atMost: 3,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			frontmatter, err := ParseFrontmatter(test.yaml)
			if err != nil {
				t.Fatal(err)
			}
			ctx := &stackFrameCancelContext{target: test.phase, cancelAt: test.atMost}

			// Act.
			got, gotErr := test.act(ctx, frontmatter)

			// Assert.
			if !errors.Is(gotErr, context.Canceled) || !reflect.DeepEqual(got, test.zero) || ctx.matches.Load() != test.atMost {
				t.Fatalf("phase %q zero=%v err=%v matches=%d want canceled/%d", test.phase, reflect.DeepEqual(got, test.zero), gotErr, ctx.matches.Load(), test.atMost)
			}
		})
	}
}

func TestObservationBackgroundParityNilShapeAndOwnership(t *testing.T) {
	frontmatter, err := ParseFrontmatter("stale_after: 2026-01-02\n" +
		"executor: {resource: runner.md, receipt: [stdout, digest]}\n" +
		"sources:\n  - {resource: source.md, usage_count: 0x10}\n")
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	stale, staleErr := frontmatter.StaleAfterObservationContext(context.Background())
	receipt, receiptErr := frontmatter.ExecutorReceiptObservationContext(context.Background())
	sources, sourcesErr := frontmatter.SourceStatesContext(context.Background())

	// Assert.
	if staleErr != nil || receiptErr != nil || sourcesErr != nil ||
		!reflect.DeepEqual(stale, frontmatter.StaleAfterObservation()) ||
		!reflect.DeepEqual(receipt, frontmatter.ExecutorReceiptObservation()) ||
		!reflect.DeepEqual(sources, frontmatter.SourceStates()) {
		t.Fatalf("Background parity stale=(%#v,%v) receipt=(%#v,%v) sources=(%#v,%v)", stale, staleErr, receipt, receiptErr, sources, sourcesErr)
	}
	receipt.Values[0] = "mutated"
	receipt.Items[0].Value = "mutated"
	receipt.Value.Receipt[0] = "mutated"
	*sources[0].Value.UsageCount = 999
	againReceipt, err := frontmatter.ExecutorReceiptObservationContext(context.Background())
	if err != nil || againReceipt.Values[0] != "stdout" || againReceipt.Value.Receipt[0] != "stdout" {
		t.Fatalf("receipt mutation leaked: (%#v,%v)", againReceipt, err)
	}
	againSources, err := frontmatter.SourceStatesContext(context.Background())
	if err != nil || *againSources[0].Value.UsageCount != 16 {
		t.Fatalf("source mutation leaked: (%#v,%v)", againSources, err)
	}

	absent, err := ParseFrontmatter("type: Note\n")
	if err != nil {
		t.Fatal(err)
	}
	absentSources, err := absent.SourceStatesContext(context.Background())
	if err != nil || absentSources != nil {
		t.Fatalf("nil shape changed: (%#v,%v)", absentSources, err)
	}
}
