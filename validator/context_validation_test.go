package validator

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

func TestStrictV02HighCardinalityFamiliesCheckCancellation(t *testing.T) {
	t.Parallel()

	const count = 512
	list := func(prefix string, item func(int) string) string {
		var out strings.Builder
		out.WriteString(prefix)
		for index := 0; index < count; index++ {
			out.WriteString(item(index))
		}
		return out.String()
	}
	concept := func(t *testing.T, frontmatter, body string) bundle.Concept {
		t.Helper()
		document, err := bundle.ParseDocument("---\ntype: Note\n" + frontmatter + "---\n" + body)
		if err != nil {
			t.Fatal(err)
		}
		id, err := bundle.ParseConceptID("concept")
		if err != nil {
			t.Fatal(err)
		}
		return bundle.NewConcept(id, "concept.md", document)
	}

	tests := []struct {
		name string
		run  func(*validator) error
	}{
		{
			name: "tags",
			run: func(v *validator) error {
				value := concept(t, list("tags:\n", func(int) string { return "  - 17\n" }), "")
				return v.validateV02Resource(value)
			},
		},
		{
			name: "sources",
			run: func(v *validator) error {
				value := concept(t, list("sources:\n", func(index int) string {
					return fmt.Sprintf("  - {id: source-%04d, resource: scope}\n", index)
				}), "")
				return v.validateSources(value)
			},
		},
		{
			name: "verifications",
			run: func(v *validator) error {
				value := concept(t, list("verified:\n", func(int) string {
					return "  - {by: process:nightly}\n"
				}), "")
				return v.validateVerified(value)
			},
		},
		{
			name: "parameters",
			run: func(v *validator) error {
				value := concept(t, list("parameters:\n", func(index int) string {
					return fmt.Sprintf("  - {name: p-%04d, type: string, required: true}\n", index)
				}), "")
				parameters, _ := value.Document.Frontmatter.SemanticGet("parameters")
				return v.validateParameters(value.Path, parameters)
			},
		},
		{
			name: "receipt",
			run: func(v *validator) error {
				value := concept(t, "executor:\n"+list("  receipt:\n", func(index int) string {
					return fmt.Sprintf("    - field-%04d\n", index)
				}), "")
				executor, _ := value.Document.Frontmatter.SemanticGet("executor")
				return v.validateExecutor(value.Path, executor)
			},
		},
		{
			name: "attributions",
			run: func(v *validator) error {
				body := list("", func(index int) string {
					return fmt.Sprintf("[^source-%04d]: definition\n", index)
				})
				return v.validateSourceFootnotes(concept(t, "", body))
			},
		},
		{
			name: "computation markdown ownership",
			run: func(v *validator) error {
				var body strings.Builder
				body.WriteString("# Computation\n\n```\nSELECT 1;\n```\n")
				for index := 0; index < count; index++ {
					body.WriteString(fmt.Sprintf("\n## Step %04d\n\nExplanation.\n", index))
				}
				document, err := bundle.ParseDocument(
					"---\ntype: Attested Computation\nruntime: custom\n---\n" + body.String(),
				)
				if err != nil {
					t.Fatal(err)
				}
				id, err := bundle.ParseConceptID("computation")
				if err != nil {
					t.Fatal(err)
				}
				return v.validateAttestedComputation(bundle.NewConcept(id, "computation.md", document))
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			ctx := &cancelAfterValidationErrChecksContext{allowed: 32}
			v := &validator{ctx: ctx}

			// Act.
			err := tt.run(v)

			// Assert.
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("%s validation error = %v, want context.Canceled after %d checks", tt.name, err, ctx.checks.Load())
			}
		})
	}
}

func TestStrictV02CancellationReturnsNoPartialReport(t *testing.T) {
	t.Parallel()

	// Arrange.
	var frontmatter strings.Builder
	frontmatter.WriteString("---\ntype: Note\ntags:\n")
	for index := 0; index < 512; index++ {
		frontmatter.WriteString("  - 17\n")
	}
	frontmatter.WriteString("---\nBody.\n")
	b, err := bundle.Load(context.Background(), validationSource{"concept.md": []byte(frontmatter.String())})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var checkpoints atomic.Int32
	cfg := &ValidatorConfig{Strict: true, checkpoint: func() {
		if checkpoints.Add(1) == 64 {
			cancel()
		}
	}}

	// Act.
	report, err := ValidateBundleContext(ctx, b, cfg)

	// Assert.
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(report, Report{}) {
		t.Fatalf("ValidateBundleContext() = (%#v, %v), want zero report and context.Canceled", report, err)
	}
}

func TestRelationDiagnosticSortAndDedupeUseCollisionFreeTuples(t *testing.T) {
	t.Parallel()

	// Arrange: raw-target/message boundaries collided under NUL concatenation.
	source, err := bundle.ParseConceptID("source")
	if err != nil {
		t.Fatal(err)
	}
	first := bundle.RelationDiagnostic{
		Code: "relation", Source: source, RelationType: "depends_on",
		RawTarget: "a", File: "source.md", Message: "b\x00c",
	}
	second := bundle.RelationDiagnostic{
		Code: "relation", Source: source, RelationType: "depends_on",
		RawTarget: "a\x00b", File: "source.md", Message: "c",
	}
	project := func(input []bundle.RelationDiagnostic) []Diagnostic {
		v := &validator{ctx: context.Background()}
		if err := v.addRelationDiagnostics(input); err != nil {
			t.Fatal(err)
		}
		if err := v.sortDiagnostics(); err != nil {
			t.Fatal(err)
		}
		return v.report.Diagnostics
	}

	// Act.
	forward := project([]bundle.RelationDiagnostic{first, second, first})
	reverse := project([]bundle.RelationDiagnostic{second, first, first})

	// Assert.
	if len(forward) != 2 || !reflect.DeepEqual(forward, reverse) {
		t.Fatalf("collision-free deterministic diagnostics: forward=%#v reverse=%#v", forward, reverse)
	}
	if forward[0].RawTarget != "a" || forward[1].RawTarget != "a\x00b" {
		t.Fatalf("structural order = %#v", forward)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	v := &validator{ctx: canceled}
	if err := v.addRelationDiagnostics([]bundle.RelationDiagnostic{first, second}); !errors.Is(err, context.Canceled) || len(v.report.Diagnostics) != 0 {
		t.Fatalf("canceled projection = (%#v, %v), want zero context.Canceled", v.report, err)
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

type cancelAfterValidationErrChecksContext struct {
	checks  atomic.Int64
	allowed int64
}

func (c *cancelAfterValidationErrChecksContext) Deadline() (time.Time, bool) {
	return time.Time{}, false
}
func (c *cancelAfterValidationErrChecksContext) Done() <-chan struct{} { return nil }
func (c *cancelAfterValidationErrChecksContext) Value(any) any         { return nil }
func (c *cancelAfterValidationErrChecksContext) Err() error {
	if c.checks.Add(1) > c.allowed {
		return context.Canceled
	}
	return nil
}
