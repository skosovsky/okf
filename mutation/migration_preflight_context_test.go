package mutation

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
)

type migrationPhaseCheckpointContext struct {
	context.Context
	function  string
	remaining int
	matches   int
}

func (c *migrationPhaseCheckpointContext) Err() error {
	callers := make([]uintptr, 24)
	count := runtime.Callers(2, callers)
	frames := runtime.CallersFrames(callers[:count])
	for {
		frame, more := frames.Next()
		if strings.HasSuffix(frame.Function, c.function) {
			c.matches++
			if c.matches >= c.remaining {
				return context.Canceled
			}
			return nil
		}
		if !more {
			break
		}
	}
	return c.Context.Err()
}

type migrationDirectCheckpointContext struct {
	context.Context
	function  string
	remaining int
	matches   int
	canceled  bool
}

type migrationCheckpointSite struct {
	direct string
	line   int
	chain  []string
}

type migrationCallsiteCheckpointContext struct {
	context.Context
	target   migrationCheckpointSite
	matches  int
	canceled bool
	trace    []int
}

func migrationCheckpointStack(skip int) (runtime.Frame, []runtime.Frame) {
	callers := make([]uintptr, 32)
	count := runtime.Callers(skip, callers)
	frames := runtime.CallersFrames(callers[:count])
	direct, more := frames.Next()
	stack := []runtime.Frame{direct}
	for more {
		frame, next := frames.Next()
		stack = append(stack, frame)
		more = next
	}
	return direct, stack
}

func migrationCheckpointSiteMatches(site migrationCheckpointSite, direct runtime.Frame, stack []runtime.Frame) bool {
	if !strings.HasSuffix(direct.Function, site.direct) || (site.line != 0 && direct.Line != site.line) {
		return false
	}
	position := 1
	for _, required := range site.chain {
		found := false
		for position < len(stack) {
			if strings.Contains(stack[position].Function, required) {
				found = true
				position++
				break
			}
			position++
		}
		if !found {
			return false
		}
	}
	return true
}

func (c *migrationCallsiteCheckpointContext) Err() error {
	if c.canceled {
		return context.Canceled
	}
	direct, stack := migrationCheckpointStack(3)
	if strings.HasSuffix(direct.Function, c.target.direct) {
		c.trace = append(c.trace, direct.Line)
	}
	if migrationCheckpointSiteMatches(c.target, direct, stack) {
		c.matches++
		c.canceled = true
		return context.Canceled
	}
	return c.Context.Err()
}

type migrationCheckpointTraceContext struct {
	context.Context
	direct string
	chain  []string
	lines  []int
}

type migrationNthCallsiteCheckpointContext struct {
	context.Context
	site       migrationCheckpointSite
	occurrence int
	matches    int
	hits       int
	canceled   bool
	oneShot    bool
}

func (c *migrationNthCallsiteCheckpointContext) Err() error {
	if c.canceled {
		if c.oneShot {
			return c.Context.Err()
		}
		return context.Canceled
	}
	direct, stack := migrationCheckpointStack(3)
	if migrationCheckpointSiteMatches(c.site, direct, stack) {
		c.matches++
		if c.matches == c.occurrence {
			c.hits++
			c.canceled = true
			return context.Canceled
		}
	}
	return c.Context.Err()
}

func (c *migrationCheckpointTraceContext) Err() error {
	direct, stack := migrationCheckpointStack(3)
	if migrationCheckpointSiteMatches(migrationCheckpointSite{direct: c.direct, chain: c.chain}, direct, stack) {
		c.lines = append(c.lines, direct.Line)
	}
	return c.Context.Err()
}

func (c *migrationDirectCheckpointContext) Err() error {
	if c.canceled {
		return context.Canceled
	}
	callers := make([]uintptr, 24)
	count := runtime.Callers(2, callers)
	frames := runtime.CallersFrames(callers[:count])
	frame, _ := frames.Next()
	if strings.HasSuffix(frame.Function, c.function) {
		c.matches++
		if c.matches >= c.remaining {
			c.canceled = true
			return context.Canceled
		}
	}
	return c.Context.Err()
}

type migrationNestedCheckpointContext struct {
	context.Context
	direct    string
	outer     string
	remaining int
	matches   int
	canceled  bool
}

func (c *migrationNestedCheckpointContext) Err() error {
	if c.canceled {
		return context.Canceled
	}
	callers := make([]uintptr, 24)
	count := runtime.Callers(2, callers)
	frames := runtime.CallersFrames(callers[:count])
	frame, more := frames.Next()
	direct := strings.HasSuffix(frame.Function, c.direct)
	outer := false
	for more {
		frame, more = frames.Next()
		outer = outer || strings.HasSuffix(frame.Function, c.outer)
	}
	if direct && outer {
		c.matches++
		if c.matches >= c.remaining {
			c.canceled = true
			return context.Canceled
		}
	}
	return c.Context.Err()
}

type migrationAfterHelperCheckpointContext struct {
	context.Context
	helper          string
	caller          string
	helperRemaining int
	helperMatches   int
	callerMatches   int
	callerLine      int
	helperLines     []int
	armed           bool
	canceled        bool
}

func (c *migrationAfterHelperCheckpointContext) Err() error {
	if c.canceled {
		return context.Canceled
	}
	callers := make([]uintptr, 24)
	count := runtime.Callers(2, callers)
	frames := runtime.CallersFrames(callers[:count])
	frame, _ := frames.Next()
	switch {
	case strings.HasSuffix(frame.Function, c.helper):
		c.helperMatches++
		c.helperLines = append(c.helperLines, frame.Line)
		if c.helperMatches == c.helperRemaining {
			c.armed = true
		}
	case c.armed && strings.HasSuffix(frame.Function, c.caller) && frame.Line == c.callerLine:
		c.callerMatches++
		c.canceled = true
		return context.Canceled
	}
	return c.Context.Err()
}

func migrationPreflightSourceFixture() memorySource {
	return memorySource{
		"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [A](a.md)\n- [B](b.md)\n"),
		"a.md":     []byte("---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n---\n\nClaim [1].\n\n# Citations\n\n[1] https://example.test\n"),
		"b.md":     []byte("---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n---\n\nClaim [1].\n\n# Citations\n\n[1] https://example.test\n"),
	}
}

func migrationPreflightInputFixture(withMapEntries bool) store.V01ToV02Migration {
	input := store.V01ToV02Migration{
		TimestampPolicy:   store.LegacyTimestampPreserve,
		TimestampConflict: store.TimestampConflictReject,
	}
	if !withMapEntries {
		return input
	}
	input.Citations = []store.DocumentCitationMigration{
		{Path: "missing-a.md", Entries: []store.LegacyCitationMapping{{LegacyNumber: 1, SourceID: "source-a", Resource: "https://a.test"}}},
		{Path: "missing-b.md", Entries: []store.LegacyCitationMapping{{LegacyNumber: 1, SourceID: "source-b", Resource: "https://b.test"}}},
	}
	input.GeneratedAt = []store.MigrationGeneratedAt{
		{Path: "missing-a.md", At: "2026-06-25T09:00:00Z"},
		{Path: "missing-b.md", At: "2026-06-25T10:00:00Z"},
	}
	input.Computations = []store.ComputationMigration{
		migrationPreflightComputationFixture("missing-a.md", "references/a.sql"),
		migrationPreflightComputationFixture("missing-b.md", "references/b.sql"),
	}
	return input
}

func migrationPreflightComputationFixture(path, assetPath string) store.ComputationMigration {
	return store.ComputationMigration{
		Path: path,
		Contract: store.AttestedComputationContract{
			Mode:            store.AttestedComputationModeFile,
			Runtime:         "sql",
			ComputationPath: assetPath,
			Executor:        store.ExecutorContract{Resource: "executors/sql.md", Receipt: []string{"rows"}},
			Attester:        store.AttesterContract{Resource: "attesters/sql.md"},
		},
		Asset: &store.MigrationAsset{Path: assetPath, Content: []byte("SELECT 1;\n")},
	}
}

func migrationPreflightRequestFixture(input store.V01ToV02Migration) MigrationRequest {
	return MigrationRequest{
		ID: "migration-preflight", Actor: "operator",
		FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
		Migration: input,
	}
}

func TestMigrationInputFindingsPhases_CancellationPublishesExactZero(t *testing.T) {
	phases := []struct {
		name           string
		function       string
		remaining      int
		withMapEntries bool
	}{
		{name: "citation map fill", function: "migrationCitationMappingsContext", remaining: 2, withMapEntries: true},
		{name: "generated-at map fill", function: "migrationGeneratedAtContext", remaining: 2, withMapEntries: true},
		{name: "computation map fill", function: "migrationComputationsContext", remaining: 2, withMapEntries: true},
		{name: "findings sort comparator", function: "compareMigrationBlockerContext", remaining: 2},
		{name: "blocker dedupe middle", function: "dedupeMigrationBlockersContext", remaining: 3},
		{name: "blocker dedupe comparator", function: "equalMigrationBlockerContext", remaining: 2},
		{name: "action dedupe middle", function: "dedupeMigrationManualActionsContext", remaining: 3},
		{name: "action dedupe comparator", function: "equalMigrationManualActionContext", remaining: 2},
		{name: "cause dedupe middle", function: "dedupeMigrationCausesContext", remaining: 3},
		{name: "cause dedupe comparator", function: "equalMigrationCauseIdentityContext", remaining: 2},
	}

	for _, phase := range phases {
		for _, surface := range []string{"Preflight", "Preview"} {
			t.Run(surface+"/"+phase.name, func(t *testing.T) {
				// Arrange.
				source := migrationPreflightSourceFixture()
				resolution := testMigrationResolution(t, source)
				input := migrationPreflightInputFixture(phase.withMapEntries)
				baselineSource := &countedMigrationResolutionSource{memorySource: source}
				var baselineErr error
				if surface == "Preflight" {
					_, baselineErr = PreflightV01ToV02MigrationInput(
						context.Background(), baselineSource, resolution, input,
					)
				} else {
					_, baselineErr = NewMigrationPlanner().Preview(
						context.Background(), baselineSource, resolution, migrationPreflightRequestFixture(input),
					)
				}
				if baselineErr == nil {
					t.Fatal("baseline unexpectedly reached a successful plan")
				}
				ctx := &migrationPhaseCheckpointContext{
					Context: context.Background(), function: phase.function, remaining: phase.remaining,
				}
				counted := &countedMigrationResolutionSource{memorySource: source}

				// Act.
				if surface == "Preflight" {
					got, err := PreflightV01ToV02MigrationInput(ctx, counted, resolution, input)

					// Assert.
					if err != context.Canceled || !reflect.DeepEqual(got, MigrationInputPreflight{}) {
						t.Fatalf("Preflight() = (%#v, %v), want exact zero/canceled", got, err)
					}
				} else {
					got, err := NewMigrationPlanner().Preview(ctx, counted, resolution, migrationPreflightRequestFixture(input))

					// Assert.
					if err != context.Canceled || !reflect.DeepEqual(got, MigrationPreview{}) {
						t.Fatalf("Preview() = (%#v, %v), want exact zero/canceled", got, err)
					}
				}
				if ctx.matches != phase.remaining || counted.pathCalls == 0 || counted.readCalls == 0 {
					t.Fatalf("checkpoint matches=%d, source calls paths=%d reads=%d", ctx.matches, counted.pathCalls, counted.readCalls)
				}
				if counted.pathCalls > baselineSource.pathCalls || counted.readCalls > baselineSource.readCalls {
					t.Fatalf("canceled path crossed blocked planning boundary: canceled=%d/%d baseline=%d/%d",
						counted.pathCalls, counted.readCalls, baselineSource.pathCalls, baselineSource.readCalls)
				}
			})
		}
	}
}

func TestJoinMigrationCausesContext_CallsiteTraceAndCancellation(t *testing.T) {
	// Arrange. Four distinct causes exercise every literal item boundary.
	causes := []error{errors.New("one"), errors.New("two"), errors.New("three"), errors.New("four")}
	trace := &migrationCheckpointTraceContext{Context: context.Background(), direct: "joinMigrationCausesContext"}

	// Act.
	joined, err := joinMigrationCausesContext(trace, causes)

	// Assert. This exact trace makes insertion, deletion, or movement of a
	// checkpoint observable instead of merely recalibrating an ordinal.
	wantTrace := []int{1277, 1282, 1292, 1282, 1292, 1282, 1292, 1282, 1292, 1296}
	if err != nil || joined == nil || !reflect.DeepEqual(trace.lines, wantTrace) {
		t.Fatalf("join baseline = (%v, %v), trace=%v want=%v", joined, err, trace.lines, wantTrace)
	}

	for index, line := range wantTrace {
		name := "item"
		if index == 0 {
			name = "entry"
		} else if index == len(wantTrace)-1 {
			name = "true-final"
		}
		occurrence := 0
		for prior := 0; prior <= index; prior++ {
			if wantTrace[prior] == line {
				occurrence++
			}
		}
		t.Run(name+"/"+string(rune('a'+index)), func(t *testing.T) {
			// Arrange.
			// Repeated item lines are selected by adding the already observed
			// prefix to the required full trace and checking exact occurrence below.
			ctxMatch := &migrationNthCallsiteCheckpointContext{
				Context: context.Background(), occurrence: occurrence,
				site: migrationCheckpointSite{
					direct: "joinMigrationCausesContext", line: line,
					chain: []string{"TestJoinMigrationCausesContext_CallsiteTraceAndCancellation"},
				},
			}

			// Act.
			got, gotErr := joinMigrationCausesContext(ctxMatch, causes)

			// Assert.
			wantJoined := index == len(wantTrace)-1
			if gotErr != context.Canceled || (got != nil) != wantJoined || ctxMatch.matches != occurrence || ctxMatch.hits != 1 {
				t.Fatalf("join = (%v, %v), matches=%d hits=%d want occurrence=%d", got, gotErr, ctxMatch.matches, ctxMatch.hits, occurrence)
			}
		})

		for _, surface := range []string{"Preflight", "Preview"} {
			t.Run(surface+"/"+name+"/"+string(rune('a'+index)), func(t *testing.T) {
				// Arrange. A one-shot cancellation proves the real caller must
				// propagate this exact helper error; later ctx.Err checks cannot
				// accidentally rescue an ignored or replaced error.
				source := migrationPreflightSourceFixture()
				resolution := testMigrationResolution(t, source)
				input := migrationPreflightInputFixture(true)
				chain := []string{"migrationExplicitInputBlockers"}
				if surface == "Preflight" {
					chain = append(chain, "PreflightV01ToV02MigrationInput")
				} else {
					chain = append(chain, "MigrationPlanner.Preview")
				}
				ctx := &migrationNthCallsiteCheckpointContext{
					Context: context.Background(), occurrence: occurrence, oneShot: true,
					site: migrationCheckpointSite{direct: "joinMigrationCausesContext", line: line, chain: chain},
				}
				counted := &countedMigrationResolutionSource{memorySource: source}

				// Act.
				if surface == "Preflight" {
					got, gotErr := PreflightV01ToV02MigrationInput(ctx, counted, resolution, input)

					// Assert.
					if gotErr != context.Canceled || !reflect.DeepEqual(got, MigrationInputPreflight{}) {
						t.Fatalf("Preflight()=(%#v, %v), want exact zero/canceled", got, gotErr)
					}
				} else {
					got, gotErr := NewMigrationPlanner().Preview(ctx, counted, resolution, migrationPreflightRequestFixture(input))

					// Assert.
					if gotErr != context.Canceled || !reflect.DeepEqual(got, MigrationPreview{}) {
						t.Fatalf("Preview()=(%#v, %v), want exact zero/canceled", got, gotErr)
					}
				}
				if ctx.matches != occurrence || ctx.hits != 1 || counted.pathCalls == 0 || counted.readCalls == 0 {
					t.Fatalf("surface checkpoint matches=%d hits=%d reads=%d/%d", ctx.matches, ctx.hits, counted.pathCalls, counted.readCalls)
				}
			})
		}
	}
}

func TestMigrationInputFindings_NonCancelledProjectionIsStableAcrossSurfaces(t *testing.T) {
	// Arrange.
	source := migrationPreflightSourceFixture()
	resolution := testMigrationResolution(t, source)
	input := migrationPreflightInputFixture(false)
	request := migrationPreflightRequestFixture(input)

	// Act.
	firstPreflight, firstPreflightErr := PreflightV01ToV02MigrationInput(context.Background(), source, resolution, input)
	secondPreflight, secondPreflightErr := PreflightV01ToV02MigrationInput(context.Background(), source, resolution, input)
	firstPreview, firstPreviewErr := NewMigrationPlanner().Preview(context.Background(), source, resolution, request)
	secondPreview, secondPreviewErr := NewMigrationPlanner().Preview(context.Background(), source, resolution, request)

	// Assert.
	if firstPreflightErr == nil || firstPreviewErr == nil ||
		!errors.Is(firstPreflightErr, ErrUnsupportedPresentation) ||
		!errors.Is(firstPreviewErr, ErrUnsupportedPresentation) {
		t.Fatalf("errors = preflight:%v preview:%v", firstPreflightErr, firstPreviewErr)
	}
	if firstPreflightErr.Error() != secondPreflightErr.Error() ||
		firstPreviewErr.Error() != secondPreviewErr.Error() ||
		firstPreflightErr.Error() != firstPreviewErr.Error() {
		t.Fatalf("unstable errors = %q / %q / %q / %q",
			firstPreflightErr, secondPreflightErr, firstPreviewErr, secondPreviewErr)
	}
	if !reflect.DeepEqual(firstPreflight, secondPreflight) ||
		!reflect.DeepEqual(firstPreview.Blockers, secondPreview.Blockers) ||
		!reflect.DeepEqual(firstPreview.ManualActions, secondPreview.ManualActions) ||
		!reflect.DeepEqual(firstPreflight.Blockers, firstPreview.Blockers) ||
		!reflect.DeepEqual(firstPreflight.ManualActions, firstPreview.ManualActions) {
		t.Fatalf("unstable findings:\npreflight=%#v\npreview=%#v", firstPreflight, firstPreview)
	}
	wantCodes := []string{
		"missing_explicit_citation_mapping", "missing_explicit_generated_by",
		"missing_explicit_citation_mapping", "missing_explicit_generated_by",
	}
	if len(firstPreflight.Blockers) != len(wantCodes) {
		t.Fatalf("blockers = %#v", firstPreflight.Blockers)
	}
	for index, code := range wantCodes {
		wantPath := []string{"a.md", "a.md", "b.md", "b.md"}[index]
		if firstPreflight.Blockers[index].Path != wantPath ||
			firstPreflight.Blockers[index].Code != code ||
			firstPreflight.Blockers[index].Location.End < firstPreflight.Blockers[index].Location.Start {
			t.Fatalf("blockers = %#v", firstPreflight.Blockers)
		}
	}
	if firstPreview.Staged != nil || len(firstPreview.Preview.Writes) != 0 {
		t.Fatalf("blocked preview published plan/stage: %#v", firstPreview)
	}
}

func TestMigrationComputationAssetContext_CancellationPublishesExactZero(t *testing.T) {
	for _, phase := range []struct {
		name       string
		resolution func(*testing.T, memorySource) MigrationSourceResolution
		function   string
		remaining  int
	}{
		{
			name: "asset path validation",
			resolution: func(t *testing.T, source memorySource) MigrationSourceResolution {
				return testMigrationResolution(t, source)
			},
			function:  "migrationComputationAssetPathErrorContext",
			remaining: 2,
		},
		{
			name:       "target replay nil asset item",
			resolution: func(_ *testing.T, _ memorySource) MigrationSourceResolution { return testDeclaredTargetResolutionV02() },
			function:   "migrationTargetReplayComputationContext",
			remaining:  2,
		},
	} {
		for _, surface := range []string{"Preflight", "Preview"} {
			t.Run(surface+"/"+phase.name, func(t *testing.T) {
				// Arrange.
				source := migrationPreflightSourceFixture()
				if phase.name == "target replay nil asset item" {
					source["index.md"] = []byte("---\nokf_version: \"0.2\"\n---\n\n# Knowledge\n\n- [A](a.md)\n- [B](b.md)\n")
				}
				input := migrationPreflightInputFixture(false)
				computation := migrationPreflightComputationFixture("a.md", "references/a.sql")
				if phase.name == "target replay nil asset item" {
					computation.Contract.Mode = store.AttestedComputationModeInline
					computation.Contract.ComputationPath = ""
					computation.Contract.InlineComputation = "SELECT 1;\n"
					computation.Contract.InlineLanguage = "sql"
					computation.Asset = nil
				}
				input.Computations = []store.ComputationMigration{computation}
				resolution := phase.resolution(t, source)
				ctx := &migrationPhaseCheckpointContext{
					Context: context.Background(), function: phase.function, remaining: phase.remaining,
				}

				// Act.
				if surface == "Preflight" {
					got, err := PreflightV01ToV02MigrationInput(ctx, source, resolution, input)

					// Assert.
					if err != context.Canceled || !reflect.DeepEqual(got, MigrationInputPreflight{}) {
						t.Fatalf("Preflight() = (%#v, %v), want exact zero/canceled", got, err)
					}
				} else {
					got, err := NewMigrationPlanner().Preview(ctx, source, resolution, migrationPreflightRequestFixture(input))

					// Assert.
					if err != context.Canceled || !reflect.DeepEqual(got, MigrationPreview{}) {
						t.Fatalf("Preview() = (%#v, %v), want exact zero/canceled", got, err)
					}
				}
				if ctx.matches < phase.remaining {
					t.Fatalf("checkpoint matches = %d, want >= %d", ctx.matches, phase.remaining)
				}
			})
		}
	}
}

func TestMigrationInputFindings_ManualActionSortComparatorCancelsInsideStringCompare(t *testing.T) {
	for _, surface := range []string{"Preflight", "Preview"} {
		t.Run(surface, func(t *testing.T) {
			// Arrange.
			source := migrationPreflightSourceFixture()
			resolution := testMigrationResolution(t, source)
			input := migrationPreflightInputFixture(false)
			trace := &migrationCheckpointTraceContext{
				Context: context.Background(), direct: "compareStringsContext",
				chain: []string{"compareMigrationManualActionContext", "siftSliceCompareContext", "sortSliceCompareContext", "sortMigrationInputFindingsContext"},
			}
			if surface == "Preflight" {
				_, _ = PreflightV01ToV02MigrationInput(trace, source, resolution, input)
			} else {
				_, _ = NewMigrationPlanner().Preview(trace, source, resolution, migrationPreflightRequestFixture(input))
			}
			wantTrace := []int{1286, 1300, 1286, 1286, 1286, 1286, 1286, 1286, 1286, 1300, 1286}
			if surface == "Preview" {
				wantTrace = []int{1286, 1300, 1286, 1286, 1286, 1286, 1286, 1286, 1286, 1300, 1286, 1286}
			}
			if !reflect.DeepEqual(trace.lines, wantTrace) {
				t.Fatalf("manual-action sort trace=%v want=%v", trace.lines, wantTrace)
			}
			ctx := &migrationNthCallsiteCheckpointContext{
				Context: context.Background(), occurrence: 1,
				site: migrationCheckpointSite{
					direct: "compareStringsContext", line: 1300,
					chain: []string{"compareMigrationManualActionContext", "siftSliceCompareContext", "sortSliceCompareContext", "sortMigrationInputFindingsContext"},
				},
			}

			// Act.
			if surface == "Preflight" {
				got, err := PreflightV01ToV02MigrationInput(ctx, source, resolution, input)

				// Assert.
				if err != context.Canceled || !reflect.DeepEqual(got, MigrationInputPreflight{}) {
					t.Fatalf("Preflight() = (%#v, %v), want exact zero/canceled", got, err)
				}
			} else {
				got, err := NewMigrationPlanner().Preview(ctx, source, resolution, migrationPreflightRequestFixture(input))

				// Assert.
				if err != context.Canceled || !reflect.DeepEqual(got, MigrationPreview{}) {
					t.Fatalf("Preview() = (%#v, %v), want exact zero/canceled", got, err)
				}
			}
			if ctx.matches != 1 || ctx.hits != 1 {
				t.Fatalf("manual-action sort comparator matches=%d hits=%d, want 1/1", ctx.matches, ctx.hits)
			}
		})
	}
}

func TestMigrationComputationAssetHelper_IsolatedCheckpointsPublishExactZero(t *testing.T) {
	// Arrange a non-cancel trace first. Exact direct call sites prove that the
	// later final-site rows cannot silently bind an inserted earlier check.
	baselineSource := migrationPreflightSourceFixture()
	baselineLoaded, err := bundle.Load(context.Background(), baselineSource)
	if err != nil {
		t.Fatal(err)
	}
	baselineComputation := migrationPreflightComputationFixture("a.md", "references/a.sql")
	matchingTrace := &migrationCheckpointTraceContext{Context: context.Background(), direct: "migrationComputationAssetPathErrorContext"}
	if err := migrationComputationAssetPathErrorContext(matchingTrace, baselineLoaded, baselineComputation); err != nil ||
		!reflect.DeepEqual(matchingTrace.lines, []int{1014, 1025, 1035}) {
		t.Fatalf("matching helper trace=%v err=%v", matchingTrace.lines, err)
	}
	baselineComputation.Asset.Path = "references/b.sql"
	mismatchTrace := &migrationCheckpointTraceContext{Context: context.Background(), direct: "migrationComputationAssetPathErrorContext"}
	if err := migrationComputationAssetPathErrorContext(mismatchTrace, baselineLoaded, baselineComputation); err == nil ||
		!reflect.DeepEqual(mismatchTrace.lines, []int{1014, 1025, 1037}) {
		t.Fatalf("mismatch helper trace=%v err=%v", mismatchTrace.lines, err)
	}

	tests := []struct {
		name       string
		mismatch   bool
		newContext func() (context.Context, func(*testing.T))
	}{
		{
			name: "before resolve",
			newContext: func() (context.Context, func(*testing.T)) {
				ctx := &migrationCallsiteCheckpointContext{Context: context.Background(), target: migrationCheckpointSite{direct: "migrationComputationAssetPathErrorContext", line: 1014}}
				return ctx, func(t *testing.T) {
					if ctx.matches != 1 || !reflect.DeepEqual(ctx.trace, []int{1014}) {
						t.Fatalf("direct helper matches=%d trace=%v", ctx.matches, ctx.trace)
					}
				}
			},
		},
		{
			name: "after resolve",
			newContext: func() (context.Context, func(*testing.T)) {
				ctx := &migrationCallsiteCheckpointContext{Context: context.Background(), target: migrationCheckpointSite{direct: "migrationComputationAssetPathErrorContext", line: 1025}}
				return ctx, func(t *testing.T) {
					if ctx.matches != 1 || !reflect.DeepEqual(ctx.trace, []int{1014, 1025}) {
						t.Fatalf("direct helper matches=%d trace=%v", ctx.matches, ctx.trace)
					}
				}
			},
		},
		{
			name: "inside context string compare",
			newContext: func() (context.Context, func(*testing.T)) {
				ctx := &migrationCallsiteCheckpointContext{
					Context: context.Background(),
					target:  migrationCheckpointSite{direct: "compareStringsContext", line: 1300, chain: []string{"migrationComputationAssetPathErrorContext"}},
				}
				return ctx, func(t *testing.T) {
					if ctx.matches != 1 {
						t.Fatalf("asset nested comparator matches = %d, want 1", ctx.matches)
					}
				}
			},
		},
		{
			name: "matching helper final",
			newContext: func() (context.Context, func(*testing.T)) {
				ctx := &migrationCallsiteCheckpointContext{Context: context.Background(), target: migrationCheckpointSite{direct: "migrationComputationAssetPathErrorContext", line: 1035}}
				return ctx, func(t *testing.T) {
					if ctx.matches != 1 || !reflect.DeepEqual(ctx.trace, []int{1014, 1025, 1035}) {
						t.Fatalf("matching helper matches=%d trace=%v", ctx.matches, ctx.trace)
					}
				}
			},
		},
		{
			name:     "mismatch helper final",
			mismatch: true,
			newContext: func() (context.Context, func(*testing.T)) {
				ctx := &migrationCallsiteCheckpointContext{Context: context.Background(), target: migrationCheckpointSite{direct: "migrationComputationAssetPathErrorContext", line: 1037}}
				return ctx, func(t *testing.T) {
					if ctx.matches != 1 || !reflect.DeepEqual(ctx.trace, []int{1014, 1025, 1037}) {
						t.Fatalf("mismatch helper matches=%d trace=%v", ctx.matches, ctx.trace)
					}
				}
			},
		},
	}
	for _, test := range tests {
		for _, surface := range []string{"Preflight", "Preview"} {
			t.Run(surface+"/"+test.name, func(t *testing.T) {
				// Arrange.
				source := migrationPreflightSourceFixture()
				resolution := testMigrationResolution(t, source)
				input := migrationPreflightInputFixture(false)
				computation := migrationPreflightComputationFixture("a.md", "references/a.sql")
				if test.mismatch {
					computation.Asset.Path = "references/b.sql"
				}
				input.Computations = []store.ComputationMigration{computation}
				ctx, assertCheckpoint := test.newContext()

				// Act.
				if surface == "Preflight" {
					got, err := PreflightV01ToV02MigrationInput(ctx, source, resolution, input)

					// Assert.
					if err != context.Canceled || !reflect.DeepEqual(got, MigrationInputPreflight{}) {
						t.Fatalf("Preflight() = (%#v, %v), want exact zero/canceled", got, err)
					}
				} else {
					got, err := NewMigrationPlanner().Preview(ctx, source, resolution, migrationPreflightRequestFixture(input))

					// Assert.
					if err != context.Canceled || !reflect.DeepEqual(got, MigrationPreview{}) {
						t.Fatalf("Preview() = (%#v, %v), want exact zero/canceled", got, err)
					}
				}
				assertCheckpoint(t)
			})
		}
	}
}

func TestMigrationTargetReplay_CallerPostItemCheckpointPublishesExactZero(t *testing.T) {
	for _, surface := range []string{"Preflight", "Preview"} {
		t.Run(surface, func(t *testing.T) {
			// Arrange.
			source := migrationPreflightSourceFixture()
			source["index.md"] = []byte("---\nokf_version: \"0.2\"\n---\n\n# Knowledge\n\n- [A](a.md)\n- [B](b.md)\n")
			input := migrationPreflightInputFixture(false)
			computation := migrationPreflightComputationFixture("a.md", "references/a.sql")
			computation.Contract.Mode = store.AttestedComputationModeInline
			computation.Contract.ComputationPath = ""
			computation.Contract.InlineComputation = "SELECT 1;\n"
			computation.Contract.InlineLanguage = "sql"
			computation.Asset = nil
			input.Computations = []store.ComputationMigration{computation}
			resolution := testDeclaredTargetResolutionV02()
			ctx := &migrationAfterHelperCheckpointContext{
				Context: context.Background(), helper: "migrationTargetReplayComputationContext",
				caller: "migrationExplicitInputBlockers", callerLine: 757, helperRemaining: 2,
			}

			// Act.
			if surface == "Preflight" {
				got, err := PreflightV01ToV02MigrationInput(ctx, source, resolution, input)

				// Assert.
				if err != context.Canceled || !reflect.DeepEqual(got, MigrationInputPreflight{}) {
					t.Fatalf("Preflight() = (%#v, %v), want exact zero/canceled", got, err)
				}
			} else {
				got, err := NewMigrationPlanner().Preview(ctx, source, resolution, migrationPreflightRequestFixture(input))

				// Assert.
				if err != context.Canceled || !reflect.DeepEqual(got, MigrationPreview{}) {
					t.Fatalf("Preview() = (%#v, %v), want exact zero/canceled", got, err)
				}
			}
			if ctx.helperMatches != 2 || ctx.callerMatches != 1 || !ctx.armed || !reflect.DeepEqual(ctx.helperLines, []int{929, 933}) {
				t.Fatalf("checkpoint helper=%d lines=%v caller=%d armed=%t", ctx.helperMatches, ctx.helperLines, ctx.callerMatches, ctx.armed)
			}
		})
	}
}

func TestValidateMigrationComputationAssetPathsContext_IsolatedOuterCheckpoints(t *testing.T) {
	// Arrange.
	fixture := newMigrationCommitOutcomeFixture(t)
	request := fixture.apply.Request
	request.Migration.Computations = []store.ComputationMigration{
		migrationPreflightComputationFixture("alpha.md", "references/alpha.sql"),
	}
	resolution := testMigrationResolution(t, fixture.snapshot.memorySource)
	loaded, err := bundle.Load(context.Background(), fixture.snapshot.memorySource)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateMigrationComputationAssetPathsContext(context.Background(), loaded, request.Migration.Computations); err != nil {
		t.Fatalf("non-cancel validation baseline: %v", err)
	}
	trace := &migrationCheckpointTraceContext{Context: context.Background(), direct: "validateMigrationComputationAssetPathsContext"}
	if err := validateMigrationComputationAssetPathsContext(trace, loaded, request.Migration.Computations); err != nil ||
		!reflect.DeepEqual(trace.lines, []int{1058, 1062, 1068, 1072}) {
		t.Fatalf("validator trace=%v err=%v", trace.lines, err)
	}
	if _, err := NewMigrationPlanner().Preview(context.Background(), fixture.snapshot.memorySource, resolution, request); err != nil {
		t.Fatalf("non-cancel Preview baseline: %v", err)
	}

	for _, checkpoint := range []struct {
		name string
		line int
		want []int
	}{
		{name: "literal first entry", line: 1058, want: []int{1058}},
		{name: "literal middle per-item before", line: 1062, want: []int{1058, 1062}},
		{name: "per-item after", line: 1068, want: []int{1058, 1062, 1068}},
		{name: "true final", line: 1072, want: []int{1058, 1062, 1068, 1072}},
	} {
		t.Run("direct/"+checkpoint.name, func(t *testing.T) {
			// Arrange.
			ctx := &migrationCallsiteCheckpointContext{
				Context: context.Background(),
				target:  migrationCheckpointSite{direct: "validateMigrationComputationAssetPathsContext", line: checkpoint.line},
			}

			// Act.
			err := validateMigrationComputationAssetPathsContext(ctx, loaded, request.Migration.Computations)

			// Assert.
			if err != context.Canceled || ctx.matches != 1 || !reflect.DeepEqual(ctx.trace, checkpoint.want) {
				t.Fatalf("validate()=%v matches=%d trace=%v want=%v", err, ctx.matches, ctx.trace, checkpoint.want)
			}
		})

		t.Run("Preview/"+checkpoint.name, func(t *testing.T) {
			// Arrange.
			ctx := &migrationCallsiteCheckpointContext{
				Context: context.Background(),
				target:  migrationCheckpointSite{direct: "validateMigrationComputationAssetPathsContext", line: checkpoint.line},
			}

			// Act.
			got, err := NewMigrationPlanner().Preview(ctx, fixture.snapshot.memorySource, resolution, request)

			// Assert.
			if err != context.Canceled || !reflect.DeepEqual(got, MigrationPreview{}) || ctx.matches != 1 || !reflect.DeepEqual(ctx.trace, checkpoint.want) {
				t.Fatalf("Preview()=(%#v, %v); matches=%d trace=%v want=%v", got, err, ctx.matches, ctx.trace, checkpoint.want)
			}
		})
	}

	t.Run("Preflight does not claim Planner-only validator phases", func(t *testing.T) {
		// Arrange.
		trace := &migrationCheckpointTraceContext{
			Context: context.Background(), direct: "validateMigrationComputationAssetPathsContext",
		}

		// Act.
		_, _ = PreflightV01ToV02MigrationInput(trace, fixture.snapshot.memorySource, resolution, request.Migration)

		// Assert. Preflight owns its migrationComputationAssetPathErrorContext
		// path; the outer validator belongs exclusively to Planner.Preview.
		if len(trace.lines) != 0 {
			t.Fatalf("Preflight unexpectedly reached Planner-only validator: %v", trace.lines)
		}
	})
}

func TestMigrationPlannerPreview_AmbiguousInputTerminalCancellationReturnsZero(t *testing.T) {
	// Arrange.
	request := migrationPreflightRequestFixture(migrationPreflightInputFixture(false))
	request.Migration.Citations = []store.DocumentCitationMigration{{
		Path: "a.md",
		Entries: []store.LegacyCitationMapping{
			{LegacyNumber: 1, SourceID: "Spec", Resource: "https://example.test/spec"},
			{LegacyNumber: 2, SourceID: "spec", Resource: "https://example.test/spec"},
		},
	}}
	ctx := &migrationPhaseCheckpointContext{
		Context: context.Background(), function: "cloneMigrationPreviewContext", remaining: 1,
	}

	// Act.
	preview, err := NewMigrationPlanner().Preview(
		ctx,
		noReadSource{},
		MigrationSourceResolution{RequestedSelector: MigrationSelectorAuto, ToVersion: MigrationVersionV02},
		request,
	)

	// Assert.
	if err != context.Canceled || !reflect.DeepEqual(preview, MigrationPreview{}) || ctx.matches == 0 {
		t.Fatalf("Preview() = (%#v, %v), checkpoint matches=%d", preview, err, ctx.matches)
	}
}
