package validator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skosovsky/okf/bundle"
	"gopkg.in/yaml.v3"
)

func TestPublicValidationRoutesContextThroughBaseLegacyLinksAndOrphans(t *testing.T) {
	t.Parallel()

	large := strings.Repeat("x", 2<<20)
	tests := []struct {
		name   string
		source validationSource
		cfg    ValidatorConfig
		target string
	}{
		{
			name: "base document parse",
			source: validationSource{
				"concept.md": []byte("---\ntype: Note\n---\n" + large),
			},
			target: "bundle.ParseDocumentContext",
		},
		{
			name: "legacy resource scalar",
			source: validationSource{
				"index.md":   []byte("---\nokf_version: \"0.1\"\n---\n# Notes\n\n- [Concept](concept.md)\n"),
				"concept.md": []byte("---\ntype: Note\nresource: \"https://example.test/" + large + "\"\n---\nBody\n"),
			},
			cfg:    ValidatorConfig{Strict: true},
			target: "bundle.Frontmatter.ResourceStateContext",
		},
		{
			name: "link extraction",
			source: validationSource{
				"concept.md": []byte("---\ntype: Note\n---\n" + large + " [target](missing.md)\n"),
			},
			cfg:    ValidatorConfig{CheckLinks: true},
			target: "bundle.ExtractLinksContext",
		},
		{
			name: "orphan index link extraction",
			source: validationSource{
				"index.md":   []byte("# Notes\n\n- [Concept](concept.md)\n" + large),
				"concept.md": []byte("---\ntype: Note\n---\nBody\n"),
			},
			cfg:    ValidatorConfig{CheckOrphans: true},
			target: "bundle.ExtractLinksContext",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			loaded, err := bundle.Load(context.Background(), tt.source)
			if err != nil {
				t.Fatal(err)
			}
			ctx := &stackFrameCancelContext{target: tt.target}

			// Act.
			report, err := ValidateBundleContext(ctx, loaded, &tt.cfg)

			// Assert.
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("ValidateBundleContext() error = %v, want context.Canceled in %s", err, tt.target)
			}
			if !reflect.DeepEqual(report, Report{}) {
				t.Fatalf("ValidateBundleContext() report = %#v, want exact zero", report)
			}
			if got := ctx.hits.Load(); got != 1 {
				t.Fatalf("target hits = %d, want exactly one", got)
			}
		})
	}
}

func TestStrictV02RoutesContextThroughSemanticAndScalarOwners(t *testing.T) {
	t.Parallel()

	large := strings.Repeat("x", 2<<20)
	tests := []struct {
		name   string
		typ    string
		front  string
		target string
	}{
		{
			name:   "semantic mapping",
			front:  "sources:\n  - id: source\n    resource: scope\n",
			target: "bundle.SemanticMappingValueContext",
		},
		{
			name:   "semantic node",
			front:  "tags:\n  - value\n",
			target: "bundle.SemanticNodeContext",
		},
		{
			name:   "nonblank scalar",
			front:  "resource: \"" + large + "\"\n",
			target: "validator.nonEmptyScalarContext",
		},
		{
			name:   "actor scalar",
			front:  "generated:\n  by: \"process:" + large + "\"\n",
			target: "validator.validActorScalarContext",
		},
		{
			name:   "verified event mapping",
			front:  "verified:\n  - by: process:review\n    at: 2025-01-01T00:00:00Z\n",
			target: "bundle.SemanticMappingValueContext",
		},
		{
			name:   "computation parameter node",
			typ:    "Attested Computation",
			front:  "runtime: go\nparameters:\n  - {name: p, type: string, required: true}\n",
			target: "bundle.SemanticNodeContext",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			typ := tt.typ
			if typ == "" {
				typ = "Note"
			}
			loaded, err := bundle.Load(context.Background(), validationSource{
				"concept.md": []byte("---\ntype: " + typ + "\n" + tt.front + "---\nBody\n"),
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx := &stackFrameCancelContext{target: tt.target}

			// Act.
			report, err := ValidateBundleContext(ctx, loaded, &ValidatorConfig{Strict: true})

			// Assert.
			if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(report, Report{}) {
				t.Fatalf("ValidateBundleContext() = (%#v, %v), want exact zero and context.Canceled", report, err)
			}
			if got := ctx.hits.Load(); got != 1 {
				t.Fatalf("target hits = %d, want exactly one in %s", got, tt.target)
			}
		})
	}
}

func TestDiagnosticSortCancellationHasZeroPublicReport(t *testing.T) {
	t.Parallel()

	// Arrange.
	loaded, err := bundle.Load(context.Background(), validationSource{
		"a.md": []byte("---\ntitle: A\n---\nBody\n"),
		"b.md": []byte("---\ntitle: B\n---\nBody\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := &stackFrameCancelContext{target: "validator.compareDiagnosticRecordContext"}

	// Act.
	report, err := ValidateBundleContext(ctx, loaded, nil)

	// Assert.
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(report, Report{}) {
		t.Fatalf("ValidateBundleContext() = (%#v, %v), want exact zero and context.Canceled", report, err)
	}
	if got := ctx.hits.Load(); got != 1 {
		t.Fatalf("diagnostic comparator hits = %d, want one", got)
	}
}

func TestDiagnosticComparatorCancelsInsideLargeFields(t *testing.T) {
	t.Parallel()

	// Arrange.
	prefix := strings.Repeat("x", 2<<20)
	left := diagnosticRecord{diagnostic: Diagnostic{Severity: SeverityWarning, Message: prefix + "a"}}
	right := diagnosticRecord{diagnostic: Diagnostic{Severity: SeverityWarning, Message: prefix + "b"}}
	ctx := &cancelAfterValidationErrChecksContext{allowed: 16}

	// Act.
	_, err := compareDiagnosticRecordContext(ctx, left, right)

	// Assert.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("compareDiagnosticRecordContext() error = %v, want context.Canceled", err)
	}
}

func TestPathAndKeyOwnersCancelThroughPublicValidation(t *testing.T) {
	t.Parallel()

	large := strings.Repeat("x", 2<<20)
	tests := []struct {
		name   string
		source validationSource
		cfg    ValidatorConfig
		target string
	}{
		{
			name:   "reserved body trim",
			source: validationSource{"index.md": []byte("# Notes\n\n" + large)},
			target: "validator.nonBlankValidatorStringContext",
		},
		{
			name:   "link target normalization",
			source: validationSource{"concept.md": []byte("---\ntype: Note\n---\n[missing](" + large + ".md)\n")},
			cfg:    ValidatorConfig{CheckLinks: true},
			target: "resolveLinkTargetContext",
		},
		{
			name: "orphan prepared key",
			source: validationSource{
				"index.md":  []byte("# Notes\n\n- [Listed](listed.md)\n"),
				"listed.md": []byte("---\ntype: Note\n---\nBody\n"),
				"other.md":  []byte("---\ntype: Note\n---\nBody\n"),
			},
			cfg:    ValidatorConfig{CheckOrphans: true},
			target: "validator.validatorStringKeyContext",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Arrange.
			loaded, err := bundle.Load(context.Background(), tt.source)
			if err != nil {
				t.Fatal(err)
			}
			ctx := &stackFrameCancelContext{target: tt.target}
			// Act.
			report, err := ValidateBundleContext(ctx, loaded, &tt.cfg)
			// Assert.
			if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(report, Report{}) {
				t.Fatalf("ValidateBundleContext() error=%v diagnostics=%d, want zero/canceled", err, len(report.Diagnostics))
			}
			if ctx.hits.Load() != 1 {
				t.Fatalf("target %s hits=%d, want 1", tt.target, ctx.hits.Load())
			}
		})
	}
}

func TestStrictDedupeCollisionAndMessageCancellation(t *testing.T) {
	t.Parallel()

	// Arrange: the injected digest deliberately collides for distinct values.
	buckets := newValidatorStringBuckets(func(context.Context, string) ([32]byte, error) { return [32]byte{}, nil })
	ctx := context.Background()

	// Act.
	firstDuplicate, err := buckets.InsertContext(ctx, "first")
	if err != nil {
		t.Fatal(err)
	}
	secondDuplicate, err := buckets.InsertContext(ctx, "second")
	if err != nil {
		t.Fatal(err)
	}
	repeatDuplicate, err := buckets.InsertContext(ctx, "first")
	if err != nil {
		t.Fatal(err)
	}

	// Assert.
	if firstDuplicate || secondDuplicate || !repeatDuplicate {
		t.Fatalf("collision bucket duplicate states = %v/%v/%v, want false/false/true", firstDuplicate, secondDuplicate, repeatDuplicate)
	}

	// Arrange.
	large := strings.Repeat("x", 2<<20)
	cancelCtx := &cancelAfterValidationErrChecksContext{allowed: 16}

	// Act.
	message, err := validatorDiagnosticMessageContext(cancelCtx, "value %q", large)

	// Assert.
	if message != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("message builder = (%d bytes, %v), want zero/canceled", len(message), err)
	}
}

func TestDiagnosticPublicationCancellationIsExactZero(t *testing.T) {
	t.Parallel()

	// Arrange.
	large := strings.Repeat("x", 2<<20)
	loaded, err := bundle.Load(context.Background(), validationSource{
		"concept.md": []byte("---\ntype: Note\nresource: 17\n---\n" + large + "\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := &stackFrameCancelContext{target: "validatorOwnedStringContext"}

	// Act.
	report, err := ValidateBundleContext(ctx, loaded, &ValidatorConfig{Strict: true})

	// Assert.
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(report, Report{}) {
		t.Fatalf("ValidateBundleContext() error=%v diagnostics=%d, want zero/canceled", err, len(report.Diagnostics))
	}
}

func TestRelationFieldAndRelativePathBuildersCancel(t *testing.T) {
	t.Parallel()
	large := strings.Repeat("x", 2<<20)
	tests := []struct {
		name   string
		target string
		run    func(context.Context) error
	}{
		{name: "relation field", target: "semanticRelationFieldPathContext", run: func(ctx context.Context) error {
			_, err := semanticRelationFieldPathContext(ctx, bundle.RelationDiagnostic{SourceFragment: large, RelationType: large})
			return err
		}},
		{name: "relative path", target: "relContext", run: func(ctx context.Context) error {
			v := &validator{ctx: ctx, root: "."}
			_, err := v.relContext(large)
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &stackFrameCancelContext{target: tt.target}
			if err := tt.run(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("%s error=%v, want canceled", tt.name, err)
			}
		})
	}
}

func TestStrictCallerRejectsOnlyExactDuplicatesUnderDigestCollision(t *testing.T) {
	t.Parallel()
	// Arrange.
	loaded, err := bundle.Load(context.Background(), validationSource{
		"concept.md": []byte("---\ntype: Note\nsources:\n  - {id: first, resource: scope}\n  - {id: second, resource: scope}\n  - {id: first, resource: scope}\n---\nBody\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ValidatorConfig{Strict: true, stringDigest: func(context.Context, string) ([32]byte, error) { return [32]byte{}, nil }}

	// Act.
	report, err := ValidateBundleContext(context.Background(), loaded, cfg)

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	var duplicates int
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Code == "source_id_duplicate" {
			duplicates++
		}
	}
	if duplicates != 1 {
		t.Fatalf("source_id_duplicate count=%d, want exactly one exact duplicate", duplicates)
	}
}

type stackFrameCancelContext struct {
	target string
	hits   atomic.Int32
}

type cancelingError struct{ cancel context.CancelFunc }

func (e cancelingError) Error() string { e.cancel(); return "source failed" }

type failingSource struct{ err error }

func (s failingSource) Paths(context.Context) ([]string, error) { return nil, s.err }
func (failingSource) ReadFile(context.Context, string) ([]byte, error) {
	panic("unexpected read")
}

func TestValidateSourceErrorMaterializationHonorsCancellation(t *testing.T) {
	t.Parallel()
	// Arrange.
	ctx, cancel := context.WithCancel(context.Background())
	source := failingSource{err: cancelingError{cancel: cancel}}

	// Act.
	report, err := ValidateSource(ctx, source, nil)

	// Assert.
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(report, Report{}) {
		t.Fatalf("ValidateSource() = (%#v, %v), want exact zero/context.Canceled", report, err)
	}

	// Arrange.
	wantErr := errors.New("source failed")

	// Act.
	report, err = ValidateSource(context.Background(), failingSource{err: wantErr}, nil)

	// Assert.
	if err != nil || len(report.Diagnostics) != 1 || report.Diagnostics[0].Message != wantErr.Error() {
		t.Fatalf("noncancel ValidateSource() = (%#v, %v), want exact diagnostic parity", report, err)
	}
}

func TestCapturedBundleValidationIgnoresPostLoadFilesystemMutation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink mutation oracle is Unix-specific")
	}
	// Arrange.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "source.md"), []byte("---\ntype: Note\n---\n[Target](target.md)\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := bundle.LoadBundle(root)
	if err != nil {
		t.Fatal(err)
	}
	want, err := ValidateBundleContext(context.Background(), loaded, &ValidatorConfig{CheckLinks: true, CheckOrphans: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "target.md"), []byte("---\ntype: Note\n---\nBody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.md"), []byte("# Notes\n\n- [Source](source.md)\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act.
	got, err := ValidateBundleContext(context.Background(), loaded, &ValidatorConfig{CheckLinks: true, CheckOrphans: true})

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("post-load filesystem mutation changed captured report:\nwant=%#v\ngot=%#v", want, got)
	}
}

func TestAuthoritativeBucketsPreserveExactKeysUnderCollision(t *testing.T) {
	t.Parallel()
	// Arrange.
	loaded, err := bundle.Load(context.Background(), validationSource{
		"index.md":  []byte("# Notes\n\n- [A](a.md)\n"),
		"a.md":      []byte("---\ntype: Note\n---\n# Alpha\n"),
		"b.md":      []byte("---\ntype: Note\n---\n# Beta\n"),
		"source.md": []byte("---\ntype: Note\n---\n[A](a.md#alpha) [B](b.md#missing)\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ValidatorConfig{CheckLinks: true, CheckOrphans: true, stringDigest: func(context.Context, string) ([32]byte, error) { return [32]byte{}, nil }}

	// Act.
	report, err := ValidateBundleContext(context.Background(), loaded, cfg)

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	var anchorMissing, orphanB int
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Code == "link_anchor_missing" {
			anchorMissing++
		}
		if diagnostic.Code == "orphan_unlisted" && diagnostic.File == "b.md" {
			orphanB++
		}
	}
	if anchorMissing != 1 || orphanB != 1 {
		t.Fatalf("collision report anchor=%d orphan-b=%d diagnostics=%#v", anchorMissing, orphanB, report.Diagnostics)
	}
}

func TestAuthoritativeBucketEqualityCancelsInsideLargeCollision(t *testing.T) {
	t.Parallel()
	// Arrange.
	bucket := newValidatorStringBuckets(func(context.Context, string) ([32]byte, error) { return [32]byte{}, nil })
	prefix := strings.Repeat("x", 2<<20)
	if _, err := bucket.InsertContext(context.Background(), prefix+"a"); err != nil {
		t.Fatal(err)
	}
	ctx := &cancelAfterValidationErrChecksContext{allowed: 16}

	// Act.
	_, err := bucket.ContainsContext(ctx, prefix+"b")

	// Assert.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ContainsContext() error=%v, want canceled inside equality", err)
	}
}

func TestParserAndFormatterPostPreparationCancellation(t *testing.T) {
	t.Parallel()
	large := strings.Repeat("x", 2<<20)
	tests := []struct {
		name    string
		run     func(context.Context) error
		allowed int64
	}{
		{name: "uri post scan", allowed: 34, run: func(ctx context.Context) error { _, err := isValidURIContext(ctx, "scheme:"+large); return err }},
		{name: "message post copy", allowed: 34, run: func(ctx context.Context) error {
			_, err := validatorDiagnosticMessageContext(ctx, "%s", large)
			return err
		}},
		{name: "rfc3339 post parse", allowed: 1, run: func(ctx context.Context) error {
			_, err := validRFC3339ScalarContext(ctx, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "2025-01-01T00:00:00Z"})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &cancelAfterValidationErrChecksContext{allowed: tt.allowed}
			if err := tt.run(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("error=%v, want post-preparation cancellation", err)
			}
		})
	}
}

func TestFinalCheckpointsCancelThroughPublicValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source validationSource
		cfg    ValidatorConfig
		target string
	}{
		{
			name:   "link target final",
			source: validationSource{"concept.md": []byte("---\ntype: Note\n---\n[missing](missing.md)\n")},
			cfg:    ValidatorConfig{CheckLinks: true},
			target: "resolveLinkTargetFinalContext",
		},
		{
			name:   "integer post parse",
			source: validationSource{"concept.md": []byte("---\ntype: Note\nsources:\n  - resource: scope\n    usage_count: 1\n    usage_window: {from: 2025-01-01, to: 2025-01-02}\n---\nBody\n")},
			cfg:    ValidatorConfig{Strict: true},
			target: "validNonNegativeIntegerPostParseContext",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			loaded, err := bundle.Load(context.Background(), tt.source)
			if err != nil {
				t.Fatal(err)
			}
			ctx := &stackFrameCancelContext{target: tt.target}

			// Act.
			report, err := ValidateBundleContext(ctx, loaded, &tt.cfg)

			// Assert.
			if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(report, Report{}) {
				t.Fatalf("ValidateBundleContext()=(%#v,%v), want zero/canceled", report, err)
			}
			if got := ctx.hits.Load(); got != 1 {
				t.Fatalf("%s hits=%d, want 1", tt.target, got)
			}
		})
	}
}

func TestRelationSegmentProjectionCancelsInsideAndAtFinal(t *testing.T) {
	t.Parallel()
	large := strings.Repeat("x", 2<<20)
	id, err := bundle.NewConceptID([]string{large, "leaf"})
	if err != nil {
		t.Fatal(err)
	}
	diagnostic := bundle.RelationDiagnostic{Code: "relation", Source: id}
	for _, target := range []string{"bundle.stringFromStringContext", "bundle.conceptIDSegmentsFinalContext"} {
		t.Run(target, func(t *testing.T) {
			// Arrange.
			ctx := &stackFrameCancelContext{target: target}
			v := &validator{ctx: ctx}

			// Act.
			err := v.addRelationDiagnostics([]bundle.RelationDiagnostic{diagnostic})

			// Assert.
			if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(v.report, Report{}) {
				t.Fatalf("addRelationDiagnostics()=(%#v,%v), want zero/canceled", v.report, err)
			}
			if got := ctx.hits.Load(); got != 1 {
				t.Fatalf("%s hits=%d, want 1", target, got)
			}
		})
	}
}

func TestLinkExistenceUsesAuthoritativeCapturedPathBucket(t *testing.T) {
	t.Parallel()
	// Arrange: the target is retained in the immutable Bundle snapshot. The
	// cancellation target exists only in the validator-owned authoritative
	// lookup, so reverting to Bundle.ReadFile/raw map hashing cannot satisfy it.
	large := strings.Repeat("x", 2<<20) + ".bin"
	loaded, err := bundle.Load(context.Background(), validationSource{
		"concept.md": []byte("---\ntype: Note\n---\n[target](" + large + ")\n"),
		large:        []byte("asset"),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := &stackFrameCancelContext{target: "ContainsContext"}

	// Act.
	report, err := ValidateBundleContext(ctx, loaded, &ValidatorConfig{CheckLinks: true})

	// Assert.
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(report, Report{}) {
		t.Fatalf("ValidateBundleContext()=(%#v,%v), want zero/canceled", report, err)
	}
	if got := ctx.hits.Load(); got != 1 {
		t.Fatalf("authoritative captured-path lookup hits=%d, want 1", got)
	}
}

func TestAbsentCapturedPathRemainsMissingUnderForcedDigestCollision(t *testing.T) {
	t.Parallel()
	// Arrange.
	loaded, err := bundle.Load(context.Background(), validationSource{
		"concept.md":  []byte("---\ntype: Note\n---\n[missing](missing.bin)\n"),
		"present.bin": []byte("captured"),
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ValidatorConfig{CheckLinks: true, stringDigest: func(context.Context, string) ([32]byte, error) {
		return [32]byte{}, nil
	}}

	// Act.
	report, err := ValidateBundleContext(context.Background(), loaded, cfg)

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	var missing int
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Code == "link_target_missing" && strings.Contains(diagnostic.Message, `"missing.bin"`) {
			missing++
		}
	}
	if missing != 1 {
		t.Fatalf("link_target_missing count=%d diagnostics=%#v, want exact missing target under collision", missing, report.Diagnostics)
	}
}

func TestRelationProjectionContextPhasesThroughPublicCaller(t *testing.T) {
	t.Parallel()
	loaded, err := bundle.Load(context.Background(), validationSource{
		"a.md": []byte("---\ntype: Note\nrelations:\n  uses:\n    - target: missing-a\n    - target: missing-b\n---\nBody\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		chain []string
	}{
		{name: "reconstruction final", chain: []string{"newConceptIDFinalContext", "cloneValidatorRelationRefContext"}},
		{name: "comparator segments", chain: []string{"conceptIDSegmentsFinalContext", "compareRelationRefContext"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			ctx := &callChainCancelContext{chain: tt.chain}

			// Act.
			report, err := ValidateBundleContext(ctx, loaded, &ValidatorConfig{CheckRelations: true})

			// Assert.
			if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(report, Report{}) {
				t.Fatalf("ValidateBundleContext()=(%#v,%v), want zero/canceled", report, err)
			}
			if got := ctx.hits.Load(); got != 1 {
				t.Fatalf("chain %v hits=%d, want 1", tt.chain, got)
			}
		})
	}
}

func TestResolveLinkPathCallersPropagateExactCancellation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source validationSource
		cfg    ValidatorConfig
		caller string
	}{
		{
			name: "legacy index descriptions",
			source: validationSource{
				"index.md":   []byte("# Notes\n\n- [Concept](concept.md): summary\n"),
				"concept.md": []byte("---\ntype: Note\ndescription: summary\n---\nBody\n"),
			},
			cfg:    ValidatorConfig{Strict: true, Spec: "0.1"},
			caller: "validateIndexDescriptions",
		},
		{
			name: "orphan index coverage",
			source: validationSource{
				"index.md":   []byte("# Notes\n\n- [Concept](concept.md)\n"),
				"concept.md": []byte("---\ntype: Note\n---\nBody\n"),
			},
			cfg:    ValidatorConfig{CheckOrphans: true},
			caller: "indexCoveredConcepts",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			loaded, err := bundle.Load(context.Background(), tt.source)
			if err != nil {
				t.Fatal(err)
			}
			ctx := &callChainCancelContext{chain: []string{"resolveLinkTargetFinalContext", tt.caller}}

			// Act.
			report, err := ValidateBundleContext(ctx, loaded, &tt.cfg)

			// Assert.
			if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(report, Report{}) {
				t.Fatalf("ValidateBundleContext()=(%#v,%v), want zero/canceled", report, err)
			}
			if got := ctx.hits.Load(); got != 1 {
				t.Fatalf("caller %s hits=%d, want 1", tt.caller, got)
			}
		})
	}
}

type callChainCancelContext struct {
	chain []string
	hits  atomic.Int32
}

func (*callChainCancelContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*callChainCancelContext) Done() <-chan struct{}       { return nil }
func (*callChainCancelContext) Value(any) any               { return nil }

func (c *callChainCancelContext) Err() error {
	if c.hits.Load() != 0 {
		return context.Canceled
	}
	pcs := make([]uintptr, 64)
	n := runtime.Callers(2, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	matched := make([]bool, len(c.chain))
	for {
		frame, more := frames.Next()
		for index, target := range c.chain {
			if strings.Contains(frame.Function, target) {
				matched[index] = true
			}
		}
		if !more {
			break
		}
	}
	for _, ok := range matched {
		if !ok {
			return nil
		}
	}
	c.hits.Add(1)
	return context.Canceled
}

func (*stackFrameCancelContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*stackFrameCancelContext) Done() <-chan struct{}       { return nil }
func (*stackFrameCancelContext) Value(any) any               { return nil }

func (c *stackFrameCancelContext) Err() error {
	if c.hits.Load() != 0 {
		return context.Canceled
	}
	pcs := make([]uintptr, 64)
	n := runtime.Callers(2, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		if strings.Contains(frame.Function, c.target) {
			c.hits.Add(1)
			return context.Canceled
		}
		if !more {
			return nil
		}
	}
}
