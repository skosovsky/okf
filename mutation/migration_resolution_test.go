package mutation

import (
	"context"
	"errors"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

type migrationResolutionCancelAtFunctionContext struct {
	context.Context
	function string
	required string
	checks   int
}

func (c *migrationResolutionCancelAtFunctionContext) Err() error {
	c.checks++
	callers := make([]uintptr, 24)
	count := runtime.Callers(2, callers)
	frames := runtime.CallersFrames(callers[:count])
	matched := false
	required := c.required == ""
	for {
		frame, more := frames.Next()
		if strings.HasSuffix(frame.Function, c.function) {
			matched = true
		}
		if c.required != "" && strings.Contains(frame.Function, c.required) {
			required = true
		}
		if !more {
			break
		}
	}
	if matched && required {
		return context.Canceled
	}
	return c.Context.Err()
}

type countedMigrationResolutionSource struct {
	memorySource
	pathCalls int
	readCalls int
}

func (s *countedMigrationResolutionSource) Paths(ctx context.Context) ([]string, error) {
	s.pathCalls++
	return s.memorySource.Paths(ctx)
}

func (s *countedMigrationResolutionSource) ReadFile(ctx context.Context, path string) ([]byte, error) {
	s.readCalls++
	return s.memorySource.ReadFile(ctx, path)
}

func TestResolveMigrationSource_TerminalValidationKeepsCallerContext(t *testing.T) {
	tests := []struct {
		name           string
		source         memorySource
		options        MigrationSourceOptions
		wantTransition MigrationTransition
		wantErr        error
	}{
		{
			name: "explicit",
			source: memorySource{
				"concept.md": []byte("---\ntype: Note\n---\n"),
			},
			options:        MigrationSourceOptions{RequestedSelector: MigrationVersionV01},
			wantTransition: MigrationTransitionV01ToV02,
		},
		{
			name: "declared v0.1",
			source: memorySource{
				"index.md": []byte("---\nokf_version: \"0.1\"\n---\n"),
			},
			options:        MigrationSourceOptions{RequestedSelector: MigrationSelectorAuto},
			wantTransition: MigrationTransitionV01ToV02,
		},
		{
			name: "declared v0.2",
			source: memorySource{
				"index.md": []byte("---\nokf_version: \"0.2\"\n---\n"),
			},
			options:        MigrationSourceOptions{RequestedSelector: MigrationSelectorAuto},
			wantTransition: MigrationTransitionTargetNoop,
		},
		{
			name: "undeclared",
			source: memorySource{
				"concept.md": []byte("---\ntype: Note\n---\n"),
			},
			options:        MigrationSourceOptions{RequestedSelector: MigrationSelectorAuto},
			wantTransition: MigrationTransitionTargetNoop,
		},
		{
			name: "future noop",
			source: memorySource{
				"index.md": []byte("---\nokf_version: \"9.0\"\n---\n"),
			},
			options: MigrationSourceOptions{
				RequestedSelector: MigrationSelectorAuto,
				AllowFutureNoop:   true,
			},
			wantTransition: MigrationTransitionTargetNoop,
		},
		{
			name: "future blocked",
			source: memorySource{
				"index.md": []byte("---\nokf_version: \"9.0\"\n---\n"),
			},
			options:        MigrationSourceOptions{RequestedSelector: MigrationSelectorAuto},
			wantTransition: MigrationTransitionBlocked,
			wantErr:        ErrUnsupportedMigrationVersion,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			baselineSource := &countedMigrationResolutionSource{memorySource: test.source}
			baseline, baselineErr := ResolveMigrationSource(context.Background(), baselineSource, test.options)
			if !errors.Is(baselineErr, test.wantErr) || baseline.Transition != test.wantTransition {
				t.Fatalf("baseline ResolveMigrationSource() = %#v, %v", baseline, baselineErr)
			}
			if validationErr := ValidateMigrationSourceResolution(baseline); validationErr != nil {
				t.Fatalf("ValidateMigrationSourceResolution(baseline) error = %v", validationErr)
			}
			if baselineSource.pathCalls != 1 || baselineSource.readCalls != len(test.source) {
				t.Fatalf("baseline source calls = paths:%d reads:%d", baselineSource.pathCalls, baselineSource.readCalls)
			}
			ctx := &migrationResolutionCancelAtFunctionContext{
				Context:  context.Background(),
				function: "validateMigrationSourceResolutionContext",
			}
			cancelledSource := &countedMigrationResolutionSource{memorySource: test.source}

			// Act.
			got, err := ResolveMigrationSource(ctx, cancelledSource, test.options)

			// Assert.
			if err != context.Canceled || !reflect.DeepEqual(got, MigrationSourceResolution{}) {
				t.Fatalf("ResolveMigrationSource() = %#v, %v; want exact zero and context cancellation", got, err)
			}
			if ctx.checks == 0 || cancelledSource.pathCalls != 1 || cancelledSource.readCalls != len(test.source) {
				t.Fatalf("cancelled source calls = paths:%d reads:%d, context checks:%d", cancelledSource.pathCalls, cancelledSource.readCalls, ctx.checks)
			}
			if test.wantErr != nil {
				wantBlocker := MigrationBlocker{
					Code:    "unsupported_migration_source",
					Path:    "index.md",
					Message: "future declaration \"9.0\" cannot be rewritten as v0.2",
				}
				if !reflect.DeepEqual(baseline.Blockers, []MigrationBlocker{wantBlocker}) ||
					baselineErr.Error() != "unsupported migration version: "+wantBlocker.Message {
					t.Fatalf("blocked baseline diagnostics = %#v, %v", baseline, baselineErr)
				}
			}
		})
	}
}

func TestResolveMigrationSource_ParseErrorSortKeepsCallerContext(t *testing.T) {
	// Arrange.
	ctx := &migrationResolutionCancelAtFunctionContext{
		Context:  context.Background(),
		function: "compareStringsContext",
		required: "sortSliceCompareContext",
	}
	source := &countedMigrationResolutionSource{memorySource: memorySource{
		"b.md": []byte("---\ntype: [\n---\n"),
		"a.md": []byte("---\ntype: [\n---\n"),
	}}

	// Act.
	got, err := ResolveMigrationSource(ctx, source, MigrationSourceOptions{RequestedSelector: MigrationSelectorAuto})

	// Assert.
	if err != context.Canceled || !reflect.DeepEqual(got, MigrationSourceResolution{}) {
		t.Fatalf("ResolveMigrationSource() = %#v, %v; want exact zero and context cancellation", got, err)
	}
	if ctx.checks == 0 || source.pathCalls != 1 || source.readCalls != len(source.memorySource) {
		t.Fatalf("source calls = paths:%d reads:%d, context checks:%d", source.pathCalls, source.readCalls, ctx.checks)
	}
}

func TestResolveMigrationSource_ContextErrorsBeforeTerminalReturnExactZero(t *testing.T) {
	tests := []struct {
		name     string
		source   memorySource
		context  func() context.Context
		function string
	}{
		{
			name:   "bundle load",
			source: memorySource{"a.md": []byte("# A\n")},
			context: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
		},
		{
			name:     "declaration",
			source:   memorySource{"index.md": []byte("---\nokf_version: \"0.1\"\n---\n")},
			function: "migrationDeclaredVersionState",
		},
		{
			name:     "legacy probe",
			source:   memorySource{"a.md": []byte("---\ntype: Note\n---\n")},
			function: "probeLegacyMigrationCandidates",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			ctx := context.Context(context.Background())
			if test.context != nil {
				ctx = test.context()
			} else {
				ctx = &migrationResolutionCancelAtFunctionContext{
					Context: context.Background(), function: test.function,
				}
			}

			// Act.
			got, err := ResolveMigrationSource(ctx, test.source, MigrationSourceOptions{RequestedSelector: MigrationSelectorAuto})

			// Assert.
			if err != context.Canceled || !reflect.DeepEqual(got, MigrationSourceResolution{}) {
				t.Fatalf("ResolveMigrationSource() = (%#v, %v), want exact zero/canceled", got, err)
			}
		})
	}
}

func TestResolveMigrationSource_NonContextPartialDiagnosticsRemainStable(t *testing.T) {
	// Arrange.
	source := memorySource{"index.md": []byte("---\nokf_version: \"v0.2\"\n---\n")}

	// Act.
	got, err := ResolveMigrationSource(context.Background(), source, MigrationSourceOptions{RequestedSelector: MigrationSelectorAuto})

	// Assert.
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		got.RequestedSelector != MigrationSelectorAuto || got.ToVersion != MigrationVersionV02 ||
		!got.DeclarationPresent || got.DeclarationRaw != "v0.2" {
		t.Fatalf("ResolveMigrationSource() = (%#v, %v), want stable partial diagnostics", got, err)
	}
}

func TestResolveMigrationSource_AllBranches(t *testing.T) {
	tests := []struct {
		name           string
		source         memorySource
		options        MigrationSourceOptions
		wantDeclared   string
		wantResolved   string
		wantSource     MigrationResolutionSource
		wantTransition MigrationTransition
		wantCandidates int
		wantErr        error
	}{
		{
			name: "declared legacy",
			source: memorySource{
				"index.md": []byte("---\nokf_version: \"0.1\"\n---\n"),
			},
			options:        MigrationSourceOptions{RequestedSelector: MigrationSelectorAuto},
			wantDeclared:   "0.1",
			wantResolved:   "0.1",
			wantSource:     MigrationResolutionDeclared,
			wantTransition: MigrationTransitionV01ToV02,
		},
		{
			name: "declared target",
			source: memorySource{
				"index.md": []byte("---\nokf_version: \"0.2\"\n---\n"),
			},
			options:        MigrationSourceOptions{RequestedSelector: MigrationSelectorAuto},
			wantDeclared:   "0.2",
			wantResolved:   "0.2",
			wantSource:     MigrationResolutionDeclared,
			wantTransition: MigrationTransitionTargetNoop,
		},
		{
			name: "absent legacy timestamp",
			source: memorySource{
				"concept.md": []byte("---\ntype: Note\ntimestamp: 2026-01-01T00:00:00Z\n---\n"),
			},
			options:        MigrationSourceOptions{RequestedSelector: MigrationSelectorAuto},
			wantResolved:   "0.1",
			wantSource:     MigrationResolutionLegacyProbe,
			wantTransition: MigrationTransitionV01ToV02,
			wantCandidates: 1,
		},
		{
			name: "absent legacy citations",
			source: memorySource{
				"concept.md": []byte("---\ntype: Note\n---\n\nClaim [1].\n\n# Citations\n\n[1] https://example.test\n"),
			},
			options:        MigrationSourceOptions{RequestedSelector: MigrationSelectorAuto},
			wantResolved:   "0.1",
			wantSource:     MigrationResolutionLegacyProbe,
			wantTransition: MigrationTransitionV01ToV02,
			wantCandidates: 1,
		},
		{
			name: "rootless log-only legacy citations",
			source: memorySource{
				"log.md": []byte("Claim [1].\n\n# Citations\n\n[1] https://example.test\n"),
			},
			options:        MigrationSourceOptions{RequestedSelector: MigrationSelectorAuto},
			wantResolved:   "0.1",
			wantSource:     MigrationResolutionLegacyProbe,
			wantTransition: MigrationTransitionV01ToV02,
			wantCandidates: 1,
		},
		{
			name: "rootless orphan legacy timestamp",
			source: memorySource{
				"notes/orphan.md": []byte("---\ntimestamp: 2026-01-01T00:00:00Z\n---\n"),
			},
			options:        MigrationSourceOptions{RequestedSelector: MigrationSelectorAuto},
			wantResolved:   "0.1",
			wantSource:     MigrationResolutionLegacyProbe,
			wantTransition: MigrationTransitionV01ToV02,
			wantCandidates: 1,
		},
		{
			name: "nested reserved legacy citations",
			source: memorySource{
				"nested/index.md": []byte("Claim [1].\n\n# Citations\n\n[1] https://example.test\n"),
				"nested/log.md":   []byte("Claim [2].\n\n# Citations\n\n[2] https://example.test/two\n"),
			},
			options:        MigrationSourceOptions{RequestedSelector: MigrationSelectorAuto},
			wantResolved:   "0.1",
			wantSource:     MigrationResolutionLegacyProbe,
			wantTransition: MigrationTransitionV01ToV02,
			wantCandidates: 2,
		},
		{
			name: "absent native default",
			source: memorySource{
				"concept.md": []byte("---\ntype: Note\n---\n\nBody.\n"),
			},
			options:        MigrationSourceOptions{RequestedSelector: MigrationSelectorAuto},
			wantResolved:   "0.2",
			wantSource:     MigrationResolutionDefault,
			wantTransition: MigrationTransitionTargetNoop,
		},
		{
			name: "explicit legacy assertion",
			source: memorySource{
				"concept.md": []byte("---\ntype: Note\n---\n\nBody.\n"),
			},
			options:        MigrationSourceOptions{RequestedSelector: "0.1"},
			wantResolved:   "0.1",
			wantSource:     MigrationResolutionExplicit,
			wantTransition: MigrationTransitionV01ToV02,
		},
		{
			name: "future blocked",
			source: memorySource{
				"index.md": []byte("---\nokf_version: \"9.0\"\n---\n"),
			},
			options:        MigrationSourceOptions{RequestedSelector: MigrationSelectorAuto},
			wantDeclared:   "9.0",
			wantResolved:   "9.0",
			wantSource:     MigrationResolutionFuture,
			wantTransition: MigrationTransitionBlocked,
			wantErr:        ErrUnsupportedMigrationVersion,
		},
		{
			name: "future explicitly allowed noop",
			source: memorySource{
				"index.md": []byte("---\nokf_version: \"9.0\"\n---\n"),
			},
			options:        MigrationSourceOptions{RequestedSelector: MigrationSelectorAuto, AllowFutureNoop: true},
			wantDeclared:   "9.0",
			wantResolved:   "9.0",
			wantSource:     MigrationResolutionFuture,
			wantTransition: MigrationTransitionTargetNoop,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			planner := NewMigrationPlanner()

			// Act.
			got, err := planner.ResolveSource(context.Background(), test.source, test.options)

			// Assert.
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ResolveSource() error = %v, want %v", err, test.wantErr)
			}
			if got.DeclaredVersion != test.wantDeclared ||
				got.ResolvedSource != test.wantResolved ||
				got.ResolutionSource != test.wantSource ||
				got.Transition != test.wantTransition ||
				len(got.Candidates) != test.wantCandidates {
				t.Fatalf("ResolveSource() = %#v", got)
			}
			if validationErr := ValidateMigrationSourceResolution(got); validationErr != nil {
				t.Fatalf("ValidateMigrationSourceResolution(ResolveSource()) error = %v", validationErr)
			}
		})
	}
}

func TestResolveMigrationSource_ErrorDiagnosticsSatisfyCanonicalResolutionContract(t *testing.T) {
	tests := []struct {
		name                   string
		source                 memorySource
		options                MigrationSourceOptions
		wantCandidates         int
		wantDeclarationInvalid bool
	}{
		{
			name: "root parse failure before declaration",
			source: memorySource{
				"index.md": []byte("---\nokf_version: [\n---\n"),
			},
			options: MigrationSourceOptions{RequestedSelector: MigrationSelectorAuto},
		},
		{
			name: "malformed declaration",
			source: memorySource{
				"index.md": []byte("---\nokf_version: \"v0.2\"\n---\n"),
			},
			options: MigrationSourceOptions{RequestedSelector: MigrationSelectorAuto},
		},
		{
			name: "explicit declaration conflict",
			source: memorySource{
				"index.md": []byte("---\nokf_version: \"0.2\"\n---\n"),
			},
			options: MigrationSourceOptions{RequestedSelector: MigrationVersionV01},
		},
		{
			name: "probe failure with retained candidate",
			source: memorySource{
				"broken.md": []byte("---\ntype: [\n---\n"),
				"legacy.md": []byte("---\ntimestamp: 2026-01-01T00:00:00Z\n---\n"),
			},
			options:        MigrationSourceOptions{RequestedSelector: MigrationSelectorAuto},
			wantCandidates: 1,
		},
		{
			name: "invalid utf8 root with retained sibling candidate",
			source: memorySource{
				"index.md":  append([]byte("# Root\n\n- [Legacy](legacy.md)\n"), 0xff),
				"legacy.md": []byte("---\ntype: Note\ntimestamp: 2026-01-01T00:00:00Z\n---\n"),
			},
			options:                MigrationSourceOptions{RequestedSelector: MigrationSelectorAuto},
			wantCandidates:         1,
			wantDeclarationInvalid: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			before := cloneMemorySource(test.source)

			// Act.
			resolution, err := ResolveMigrationSource(context.Background(), test.source, test.options)
			validationErr := ValidateMigrationSourceResolution(resolution)

			// Assert.
			if err == nil {
				t.Fatal("ResolveMigrationSource() error = nil")
			}
			if validationErr != nil {
				t.Fatalf("ValidateMigrationSourceResolution() error = %v for %#v", validationErr, resolution)
			}
			if len(resolution.Candidates) != test.wantCandidates ||
				test.wantDeclarationInvalid && resolution.DeclarationValid {
				t.Fatalf("ResolveMigrationSource() diagnostics = %#v, error = %v", resolution, err)
			}
			if !equalMemorySource(test.source, before) {
				t.Fatal("resolution or validation mutated source")
			}
		})
	}
}

func TestResolveMigrationSource_ParserOwnedEvidenceRejectsFalsePositives(t *testing.T) {
	// Arrange.
	source := memorySource{
		"concept.md": []byte("---\ntype: Note\nx-note: \"timestamp: old\"\n---\n\n" +
			"The word timestamp and heading text # Citations are prose.\n\n" +
			"```markdown\n# Citations\n[1] hidden\n```\n"),
	}

	// Act.
	got, err := ResolveMigrationSource(context.Background(), source, MigrationSourceOptions{
		RequestedSelector: MigrationSelectorAuto,
	})

	// Assert.
	if err != nil {
		t.Fatalf("ResolveMigrationSource() error = %v", err)
	}
	if got.ResolvedSource != MigrationVersionV02 ||
		got.Transition != MigrationTransitionTargetNoop ||
		len(got.Candidates) != 0 {
		t.Fatalf("ResolveMigrationSource() = %#v, want native target noop", got)
	}
}

func TestResolveMigrationSource_NumericMarkersRequireActiveCitationsSection(t *testing.T) {
	tests := []struct {
		name           string
		source         memorySource
		selector       string
		wantResolved   string
		wantSource     MigrationResolutionSource
		wantTransition MigrationTransition
		wantCandidates int
	}{
		{
			name: "undeclared marker only defaults to target",
			source: memorySource{
				"concept.md": []byte("---\ntype: Note\n---\n\nA numbered claim [1].\n"),
			},
			selector:       MigrationSelectorAuto,
			wantResolved:   MigrationVersionV02,
			wantSource:     MigrationResolutionDefault,
			wantTransition: MigrationTransitionTargetNoop,
		},
		{
			name: "exact citations heading is active legacy evidence",
			source: memorySource{
				"concept.md": []byte("---\ntype: Note\n---\n\nA claim [1].\n\n# Citations\n\n[1] https://example.test\n"),
			},
			selector:       MigrationSelectorAuto,
			wantResolved:   MigrationVersionV01,
			wantSource:     MigrationResolutionLegacyProbe,
			wantTransition: MigrationTransitionV01ToV02,
			wantCandidates: 1,
		},
		{
			name: "present sources suppress citation fallback even when malformed",
			source: memorySource{
				"concept.md": []byte("---\ntype: Note\nsources: malformed\n---\n\nA claim [1].\n\n# Citations\n\n[1] https://example.test\n"),
			},
			selector:       MigrationSelectorAuto,
			wantResolved:   MigrationVersionV02,
			wantSource:     MigrationResolutionDefault,
			wantTransition: MigrationTransitionTargetNoop,
		},
		{
			name: "explicit legacy authority wins over marker only",
			source: memorySource{
				"concept.md": []byte("---\ntype: Note\n---\n\nA numbered claim [1].\n"),
			},
			selector:       MigrationVersionV01,
			wantResolved:   MigrationVersionV01,
			wantSource:     MigrationResolutionExplicit,
			wantTransition: MigrationTransitionV01ToV02,
		},
		{
			name: "declared target authority wins over exact heading",
			source: memorySource{
				"index.md":   []byte("---\nokf_version: \"0.2\"\n---\n"),
				"concept.md": []byte("---\ntype: Note\n---\n\n# Citations\n\n[1] https://example.test\n"),
			},
			selector:       MigrationSelectorAuto,
			wantResolved:   MigrationVersionV02,
			wantSource:     MigrationResolutionDeclared,
			wantTransition: MigrationTransitionTargetNoop,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Act.
			got, err := ResolveMigrationSource(context.Background(), test.source, MigrationSourceOptions{
				RequestedSelector: test.selector,
			})

			// Assert.
			if err != nil {
				t.Fatalf("ResolveMigrationSource() error = %v", err)
			}
			if got.ResolvedSource != test.wantResolved ||
				got.ResolutionSource != test.wantSource ||
				got.Transition != test.wantTransition ||
				len(got.Candidates) != test.wantCandidates {
				t.Fatalf("ResolveMigrationSource() = %#v", got)
			}
		})
	}
}

func TestMigrationLegacyEvidenceOccurrenceLock(t *testing.T) {
	for _, test := range []struct {
		name string
		want int
	}{
		{name: "migration.go", want: 1},
		{name: "migration_resolution.go", want: 0},
	} {
		// Arrange.
		source, err := os.ReadFile(test.name)
		if err != nil {
			t.Fatal(err)
		}

		// Assert.
		if got := strings.Count(string(source), ".NumericMarkers"); got != test.want {
			t.Fatalf("%s NumericMarkers occurrences = %d, want %d replay/rewrite owners only", test.name, got, test.want)
		}
	}
}

func TestMigrationSourceResolutionDigest_BindsEverySemanticFieldAndFraming(t *testing.T) {
	base := MigrationSourceResolution{
		RequestedSelector: MigrationSelectorAuto,
		DeclarationValid:  true,
		ResolvedSource:    MigrationVersionV01,
		ResolutionSource:  MigrationResolutionLegacyProbe,
		FromVersion:       MigrationVersionV01,
		ToVersion:         MigrationVersionV02,
		Transition:        MigrationTransitionV01ToV02,
		Candidates: []MigrationLegacyCandidate{
			{Kind: "citations", Path: "a.md", Location: SourceSpan{Start: 1, End: 4}},
			{Kind: "citations", Path: "b.md", Location: SourceSpan{Start: 3, End: 5}},
		},
	}
	baseDigest, err := migrationSourceResolutionDigestContext(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*MigrationSourceResolution){
		"requested selector":  func(value *MigrationSourceResolution) { value.RequestedSelector = MigrationVersionV01 },
		"declaration present": func(value *MigrationSourceResolution) { value.DeclarationPresent = true },
		"declaration valid":   func(value *MigrationSourceResolution) { value.DeclarationValid = false },
		"declaration raw":     func(value *MigrationSourceResolution) { value.DeclarationRaw = "0.1" },
		"declared version":    func(value *MigrationSourceResolution) { value.DeclaredVersion = MigrationVersionV01 },
		"resolved source":     func(value *MigrationSourceResolution) { value.ResolvedSource = MigrationVersionV02 },
		"resolution source":   func(value *MigrationSourceResolution) { value.ResolutionSource = MigrationResolutionDefault },
		"from version":        func(value *MigrationSourceResolution) { value.FromVersion = MigrationVersionV02 },
		"to version":          func(value *MigrationSourceResolution) { value.ToVersion = MigrationVersionV01 },
		"transition":          func(value *MigrationSourceResolution) { value.Transition = MigrationTransitionTargetNoop },
		"candidate omission":  func(value *MigrationSourceResolution) { value.Candidates = value.Candidates[:1] },
		"candidate kind": func(value *MigrationSourceResolution) {
			value.Candidates[0].Kind = "timestamp"
			value.Candidates[0].Location = SourceSpan{}
		},
		"candidate path":       func(value *MigrationSourceResolution) { value.Candidates[0].Path = "c.md" },
		"candidate span start": func(value *MigrationSourceResolution) { value.Candidates[0].Location.Start++ },
		"candidate span end":   func(value *MigrationSourceResolution) { value.Candidates[0].Location.End++ },
		"candidate order": func(value *MigrationSourceResolution) {
			value.Candidates[0], value.Candidates[1] = value.Candidates[1], value.Candidates[0]
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := base.Clone()
			mutate(&changed)
			got, digestErr := migrationSourceResolutionDigestContext(context.Background(), changed)
			if digestErr == nil && constantTimeDigestEqual(got, baseDigest) {
				t.Fatalf("mutation %q is not resolution-bound", name)
			}
		})
	}

	blocked := validFutureMigrationResolution(MigrationTransitionBlocked)
	blockedDigest, err := migrationSourceResolutionDigestContext(context.Background(), blocked)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*MigrationSourceResolution){
		"blocker omission":   func(value *MigrationSourceResolution) { value.Blockers = value.Blockers[:0] },
		"blocker code":       func(value *MigrationSourceResolution) { value.Blockers[0].Code = "changed" },
		"blocker path":       func(value *MigrationSourceResolution) { value.Blockers[0].Path = "c.md" },
		"blocker span start": func(value *MigrationSourceResolution) { value.Blockers[0].Location.Start++ },
		"blocker span end":   func(value *MigrationSourceResolution) { value.Blockers[0].Location.End++ },
		"blocker message":    func(value *MigrationSourceResolution) { value.Blockers[0].Message = "changed" },
		"blocker addition": func(value *MigrationSourceResolution) {
			value.Blockers = append(value.Blockers, value.Blockers[0])
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := blocked.Clone()
			mutate(&changed)
			got, digestErr := migrationSourceResolutionDigestContext(context.Background(), changed)
			if digestErr == nil && constantTimeDigestEqual(got, blockedDigest) {
				t.Fatalf("mutation %q is not resolution-bound", name)
			}
		})
	}

	defaultTarget := MigrationSourceResolution{
		RequestedSelector: MigrationSelectorAuto,
		DeclarationValid:  true,
		ResolvedSource:    MigrationVersionV02,
		ResolutionSource:  MigrationResolutionDefault,
		FromVersion:       MigrationVersionV02,
		ToVersion:         MigrationVersionV02,
		Transition:        MigrationTransitionTargetNoop,
	}
	nilDigest, err := migrationSourceResolutionDigestContext(context.Background(), defaultTarget)
	if err != nil {
		t.Fatal(err)
	}
	defaultTarget.Candidates = []MigrationLegacyCandidate{}
	defaultTarget.Blockers = []MigrationBlocker{}
	emptyDigest, err := migrationSourceResolutionDigestContext(context.Background(), defaultTarget)
	if err != nil || nilDigest != emptyDigest {
		t.Fatalf("nil/empty semantic normalization = (%q, %q, %v)", nilDigest, emptyDigest, err)
	}

	left := validFutureMigrationResolution(MigrationTransitionTargetNoop)
	left.DeclarationRaw, left.DeclaredVersion = "1.23", "1.23"
	left.ResolvedSource, left.FromVersion, left.ToVersion = "1.23", "1.23", "1.23"
	right := validFutureMigrationResolution(MigrationTransitionTargetNoop)
	right.DeclarationRaw, right.DeclaredVersion = "12.3", "12.3"
	right.ResolvedSource, right.FromVersion, right.ToVersion = "12.3", "12.3", "12.3"
	leftDigest, leftErr := migrationSourceResolutionDigestContext(context.Background(), left)
	rightDigest, rightErr := migrationSourceResolutionDigestContext(context.Background(), right)
	if leftErr != nil || rightErr != nil || leftDigest == rightDigest {
		t.Fatalf("length framing collision = (%q, %v), (%q, %v)", leftDigest, leftErr, rightDigest, rightErr)
	}
}

func TestResolveMigrationSource_MalformedCitationsStillResolveLegacyForManualAction(t *testing.T) {
	// Arrange.
	source := memorySource{
		"concept.md": []byte("---\ntype: Note\n---\n\n# Citations\n\nnot a supported entry\n"),
	}

	// Act.
	got, err := ResolveMigrationSource(context.Background(), source, MigrationSourceOptions{
		RequestedSelector: MigrationSelectorAuto,
	})

	// Assert.
	if err != nil {
		t.Fatalf("ResolveMigrationSource() error = %v", err)
	}
	if got.ResolvedSource != MigrationVersionV01 ||
		got.ResolutionSource != MigrationResolutionLegacyProbe ||
		len(got.Candidates) != 1 ||
		got.Candidates[0].Kind != "citations" {
		t.Fatalf("ResolveMigrationSource() = %#v, want legacy citation candidate", got)
	}
}

func TestResolveMigrationSource_OwnershipFailuresRemainLegacyCandidates(t *testing.T) {
	tests := []struct {
		name           string
		source         []byte
		wantCandidates int
		wantErr        bool
	}{
		{
			name:           "ambiguous destination",
			wantCandidates: 1,
			source: []byte("---\ntype: Note\n---\n\n# Citations\n\n" +
				"[1] [One](https://one.test) [Two](https://two.test)\n"),
		},
		{
			name:    "invalid UTF-8",
			source:  append([]byte("---\ntype: Note\n---\n\n"), 0xff),
			wantErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			source := memorySource{"concept.md": test.source}

			// Act.
			got, err := ResolveMigrationSource(context.Background(), source, MigrationSourceOptions{
				RequestedSelector: MigrationSelectorAuto,
			})

			// Assert.
			if (err != nil) != test.wantErr {
				t.Fatalf("ResolveMigrationSource() error = %v, wantErr %t", err, test.wantErr)
			}
			if len(got.Candidates) != test.wantCandidates {
				t.Fatalf("ResolveMigrationSource() = %#v", got)
			}
			if test.wantCandidates != 0 &&
				(got.ResolvedSource != MigrationVersionV01 ||
					got.ResolutionSource != MigrationResolutionLegacyProbe ||
					got.Candidates[0].Kind != "citations" ||
					got.Candidates[0].Path != "concept.md" ||
					got.Candidates[0].Location.End <= got.Candidates[0].Location.Start) {
				t.Fatalf("ResolveMigrationSource() = %#v, want exact legacy failure candidate", got)
			}
		})
	}
}

func TestResolveMigrationSource_AuthoritativeSourceDoesNotAbortOnNonRootParseErrors(t *testing.T) {
	tests := []struct {
		name     string
		root     string
		selector string
		want     string
		source   MigrationResolutionSource
	}{
		{
			name: "declared legacy auto", root: "0.1", selector: MigrationSelectorAuto,
			want: MigrationVersionV01, source: MigrationResolutionDeclared,
		},
		{
			name: "declared target auto", root: "0.2", selector: MigrationSelectorAuto,
			want: MigrationVersionV02, source: MigrationResolutionDeclared,
		},
		{
			name: "explicit legacy", root: "", selector: MigrationVersionV01,
			want: MigrationVersionV01, source: MigrationResolutionExplicit,
		},
		{
			name: "explicit target", root: "", selector: MigrationVersionV02,
			want: MigrationVersionV02, source: MigrationResolutionExplicit,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			source := memorySource{
				"broken.md": []byte("---\ntype: [\n---\n"),
			}
			if test.root != "" {
				source["index.md"] = []byte("---\nokf_version: \"" + test.root + "\"\n---\n")
			}

			// Act.
			got, err := ResolveMigrationSource(context.Background(), source, MigrationSourceOptions{
				RequestedSelector: test.selector,
			})

			// Assert.
			if err != nil {
				t.Fatalf("ResolveMigrationSource() error = %v", err)
			}
			if got.ResolvedSource != test.want || got.ResolutionSource != test.source {
				t.Fatalf("ResolveMigrationSource() = %#v", got)
			}
		})
	}
}

func TestResolveMigrationSource_AutoProbeDoesNotInventCitationEvidenceForUnreadableDocuments(t *testing.T) {
	tests := []struct {
		name           string
		withCandidate  bool
		wantCandidates int
	}{
		{name: "unreadable only"},
		{name: "unreadable plus real timestamp", withCandidate: true, wantCandidates: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var baseline MigrationSourceResolution
			var baselineErr string
			for _, reverse := range []bool{false, true} {
				// Arrange.
				files := []struct {
					path string
					data []byte
				}{
					{path: "unreadable.md", data: append([]byte("---\ntype: Note\n---\n\n"), 0xff)},
				}
				if test.withCandidate {
					files = append(files, struct {
						path string
						data []byte
					}{
						path: "timestamp.md",
						data: []byte("---\ntype: Note\ntimestamp: 2026-01-01T00:00:00Z\n---\n"),
					})
				}
				if reverse {
					for left, right := 0, len(files)-1; left < right; left, right = left+1, right-1 {
						files[left], files[right] = files[right], files[left]
					}
				}
				source := memorySource{}
				for _, file := range files {
					source[file.path] = file.data
				}

				// Act.
				got, err := ResolveMigrationSource(context.Background(), source, MigrationSourceOptions{
					RequestedSelector: MigrationSelectorAuto,
				})

				// Assert.
				if err == nil {
					t.Fatal("ResolveMigrationSource() error = nil, want unreadable finding")
				}
				if len(got.Candidates) != test.wantCandidates {
					t.Fatalf("Candidates = %#v", got.Candidates)
				}
				for _, candidate := range got.Candidates {
					if candidate.Path == "unreadable.md" {
						t.Fatalf("unreadable document invented candidate: %#v", got.Candidates)
					}
				}
				if test.withCandidate &&
					(got.Candidates[0].Path != "timestamp.md" || got.Candidates[0].Kind != "timestamp") {
					t.Fatalf("Candidates = %#v", got.Candidates)
				}
				if baselineErr == "" {
					baseline = got
					baselineErr = err.Error()
				} else if !reflect.DeepEqual(got.Candidates, baseline.Candidates) || err.Error() != baselineErr {
					t.Fatalf("insertion order changed result:\n%#v\n%#v\n%s\n%s", baseline, got, baselineErr, err)
				}
			}
		})
	}
}

func TestResolveMigrationSource_AutoProbePreservesAllCandidatesWhenDocumentsFail(t *testing.T) {
	// Arrange.
	files := []struct {
		path   string
		source []byte
	}{
		{path: "a.md", source: []byte("---\ntype: [\n---\n")},
		{
			path: "b.md",
			source: append(
				[]byte("---\ntype: Note\n---\n\nClaim [1].\n\n# Citations\n\n[1] https://example.test\n"),
				0xff,
			),
		},
		{path: "c.md", source: []byte("---\ntype: Note\ntimestamp: 2026-01-01T00:00:00Z\n---\n")},
		{path: "z.md", source: []byte("---\ntype: {\n---\n")},
	}
	sources := []memorySource{{}, {}}
	for index, file := range files {
		sources[0][file.path] = file.source
		sources[1][files[len(files)-1-index].path] = files[len(files)-1-index].source
	}

	var first MigrationSourceResolution
	var firstErr string
	for index, source := range sources {
		// Act.
		got, err := ResolveMigrationSource(context.Background(), source, MigrationSourceOptions{
			RequestedSelector: MigrationSelectorAuto,
		})

		// Assert.
		if err == nil {
			t.Fatalf("ResolveMigrationSource(%d) error = nil, want joined probe errors", index)
		}
		if len(got.Candidates) != 1 ||
			got.Candidates[0].Path != "c.md" ||
			got.Candidates[0].Kind != "timestamp" {
			t.Fatalf("ResolveMigrationSource(%d) candidates = %#v", index, got.Candidates)
		}
		if index == 0 {
			first = got
			firstErr = err.Error()
			continue
		}
		if !reflect.DeepEqual(got.Candidates, first.Candidates) {
			t.Fatalf("candidate order changed: first=%#v second=%#v", first.Candidates, got.Candidates)
		}
		if err.Error() != firstErr {
			t.Fatalf("joined error order changed:\nfirst:  %s\nsecond: %s", firstErr, err)
		}
	}
}
