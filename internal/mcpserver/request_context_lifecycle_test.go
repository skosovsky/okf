package mcpserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/mutation"
	"github.com/skosovsky/okf/store"
	"github.com/skosovsky/okf/validator"
)

func TestAllNineHandlersRejectPreCancelledRequestsBeforeIO(t *testing.T) {
	// Arrange, act, and assert.
	for _, surface := range inputContractSurfaces() {
		surface := surface
		for _, boundary := range []string{"direct", "wrapped"} {
			boundary := boundary
			t.Run(surface.name+"/"+boundary, func(t *testing.T) {
				root := t.TempDir()
				request := mcp.CallToolRequest{Params: mcp.CallToolParams{
					Name: surface.name, Arguments: surface.base(root),
				}}
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				sourceOpens, sourceReads, storeOpens := 0, 0, 0
				ctx = withLegacyToolIOObserver(ctx, legacyToolIOObserver{
					sourceOpen: func() { sourceOpens++ },
					sourceRead: func() { sourceReads++ },
					storeOpen:  func() { storeOpens++ },
				})
				ctx = withMigrationIOObserver(ctx, migrationIOObserver{
					sourceOpen: func() { sourceOpens++ },
					sourceRead: func() { sourceReads++ },
					storeOpen:  func() { storeOpens++ },
				})
				handler := surface.handler
				handlerCalls := 0
				if boundary == "wrapped" {
					handler = contractToolHandler(surface.name, func(
						ctx context.Context,
						request mcp.CallToolRequest,
					) (*mcp.CallToolResult, error) {
						handlerCalls++
						return surface.handler(ctx, request)
					})
				}

				result, err := handler(ctx, request)

				if err != nil {
					t.Fatalf("handler error = %v", err)
				}
				assertCancellationOnlyResult(t, result)
				if boundary == "wrapped" && handlerCalls != 0 {
					t.Fatalf("wrapped handler calls = %d, want 0", handlerCalls)
				}
				if sourceOpens != 0 || sourceReads != 0 || storeOpens != 0 {
					t.Fatalf("I/O = opens:%d reads:%d stores:%d, want zero", sourceOpens, sourceReads, storeOpens)
				}
				if _, statErr := os.Lstat(filepath.Join(root, ".okf")); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("pre-cancelled request changed store state: %v", statErr)
				}
			})
		}
	}
}

func TestAllNineHandlersMapCancellationAtCapturedSourceOpen(t *testing.T) {
	// Arrange, act, and assert.
	for _, surface := range inputContractSurfaces() {
		surface := surface
		t.Run(surface.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "alpha.md", "---\ntype: Knowledge\n---\nAlpha.\n")
			ctx, cancel := context.WithCancel(t.Context())
			var sourceOpens atomic.Int64
			cancelAtOpen := func() {
				sourceOpens.Add(1)
				cancel()
			}
			ctx = withLegacyToolIOObserver(ctx, legacyToolIOObserver{sourceOpen: cancelAtOpen})
			ctx = withMigrationIOObserver(ctx, migrationIOObserver{sourceOpen: cancelAtOpen})
			request := mcp.CallToolRequest{Params: mcp.CallToolParams{
				Name: surface.name, Arguments: surface.base(root),
			}}

			result, err := surface.handler(ctx, request)

			if err != nil {
				t.Fatalf("handler error = %v", err)
			}
			assertCancellationOnlyResult(t, result)
			if sourceOpens.Load() != 1 {
				t.Fatalf("source opens = %d, want exactly one cancellation point", sourceOpens.Load())
			}
			if _, statErr := os.Lstat(filepath.Join(root, ".okf")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("cancelled source capture changed store state: %v", statErr)
			}
		})
	}
}

func TestRecursiveRawArgumentWalkCancelsWithoutPartialClassification(t *testing.T) {
	// Arrange.
	values := make([]any, 8_000)
	for index := range values {
		values[index] = map[string]any{"index": index, "value": strings.Repeat("x", 32)}
	}
	probe := &readConceptCancelAfterContext{allowed: 1 << 60}
	if err := validateMCPInputContext(probe, values, 0, new(int)); err != nil {
		t.Fatalf("probe validation error = %v", err)
	}
	mid := &readConceptCancelAfterContext{allowed: probe.checks.Load() / 2}

	// Act.
	err := validateMCPInputContext(mid, values, 0, new(int))
	result := validateRawMCPArgumentsContext(
		&readConceptCancelAfterContext{allowed: probe.checks.Load() / 2},
		values,
	)

	// Assert.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("recursive validation error = %v, want context.Canceled", err)
	}
	assertCancellationOnlyResult(t, result)
}

func TestProjectionLoopsCancelWithZeroPartialDTOs(t *testing.T) {
	// Arrange.
	diagnostics := make([]validator.Diagnostic, 8_000)
	storeDiagnostics := make([]store.Diagnostic, 8_000)
	preview := store.Preview{Writes: make([]store.Write, 8_000)}
	proof := mutation.MigrationPlanProof{
		FormatVersion: mutation.MigrationPlanProofFormatVersion,
		Reads:         make([]store.Read, 8_000),
	}
	for index := range diagnostics {
		diagnostics[index] = validator.Diagnostic{
			Code: "test", Severity: validator.SeverityInfo, Message: "diagnostic",
		}
		storeDiagnostics[index] = store.Diagnostic{
			Code: "test", Severity: store.DiagnosticInfo, Message: "diagnostic",
		}
		preview.Writes[index] = store.Write{Path: "path/" + strings.Repeat("a", 8) + string(rune(index%26+'a'))}
		proof.Reads[index] = store.Read{Path: "path/read"}
	}
	var documentText strings.Builder
	documentText.WriteString("---\ntype: Knowledge\nsources:\n")
	for index := 0; index < 2_000; index++ {
		documentText.WriteString("  - resource: urn:test:")
		documentText.WriteString(strings.Repeat("x", index%8+1))
		documentText.WriteByte('\n')
	}
	documentText.WriteString("---\nBody.\n")
	document, err := bundle.ParseDocument(documentText.String())
	if err != nil {
		t.Fatalf("ParseDocument() error = %v", err)
	}

	tests := []struct {
		name string
		run  func(context.Context) (bool, error)
	}{
		{
			name: "validation report",
			run: func(ctx context.Context) (bool, error) {
				response, err := reportResponseContext(ctx, "", validator.Report{Diagnostics: diagnostics})
				return len(response.Diagnostics) != 0 || response.ScannedFiles != 0, err
			},
		},
		{
			name: "store diagnostics",
			run: func(ctx context.Context) (bool, error) {
				response, err := wireDiagnosticsFromStoreContext(ctx, storeDiagnostics)
				return response != nil, err
			},
		},
		{
			name: "preview paths and sort",
			run: func(ctx context.Context) (bool, error) {
				response, err := previewPathsContext(ctx, preview)
				return response != nil, err
			},
		},
		{
			name: "migration proof",
			run: func(ctx context.Context) (bool, error) {
				response, err := migrationPlanProofDTOFromDomainContext(ctx, proof)
				return response.FormatVersion != 0 || response.Reads != nil, err
			},
		},
		{
			name: "read concept typed collections",
			run: func(ctx context.Context) (bool, error) {
				response, err := collectReadConceptProjectionContext(ctx, document)
				return response.Sources != nil || response.Verified != nil || response.Attributions != nil, err
			},
		},
	}

	// Act and assert.
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			probe := &readConceptCancelAfterContext{allowed: 1 << 60}
			if partial, probeErr := test.run(probe); probeErr != nil || !partial {
				t.Fatalf("probe = (partial:%v, error:%v)", partial, probeErr)
			}
			mid := &readConceptCancelAfterContext{allowed: max(1, probe.checks.Load()/2)}

			partial, midErr := test.run(mid)

			if partial || !errors.Is(midErr, context.Canceled) {
				t.Fatalf("mid-cancel = (partial:%v, error:%v), want zero and context.Canceled", partial, midErr)
			}
		})
	}
}

func TestGraphProjectionBudgetCancelsDuringNodeAndStringWalks(t *testing.T) {
	// Arrange.
	nodes := make([]any, 8_000)
	for index := range nodes {
		nodes[index] = map[string]any{"@id": "urn:test:" + strings.Repeat("x", 1_024)}
	}
	document := map[string]any{"@graph": nodes}
	probe := &readConceptCancelAfterContext{allowed: 1 << 60}
	if result := graphProjectionBudgetContext(probe, document); result != nil {
		t.Fatalf("probe graph budget returned %#v", result)
	}
	mid := &readConceptCancelAfterContext{allowed: max(1, probe.checks.Load()/2)}

	// Act.
	result := graphProjectionBudgetContext(mid, document)

	// Assert.
	assertCancellationOnlyResult(t, result)
}

func TestBoundedPreviewDiffHasExactLimitAndZeroPartialFailure(t *testing.T) {
	// Arrange.
	const framingBytes = len("rename from ") + len("\nrename to ") + len("\n")
	exactPreview := store.Preview{Renames: []store.Rename{{
		From: strings.Repeat("a", maxPatchDiffBytes-framingBytes),
	}}}
	overPreview := store.Preview{Renames: []store.Rename{{
		From: strings.Repeat("a", maxPatchDiffBytes-framingBytes+1),
	}}}
	source := staticBundleSource{}

	// Act.
	exact, exactErr := boundedPreviewDiff(t.Context(), source, exactPreview)
	over, overErr := boundedPreviewDiff(t.Context(), source, overPreview)
	cancelledContext, cancel := context.WithCancel(t.Context())
	cancel()
	cancelled, cancelledErr := boundedPreviewDiff(cancelledContext, source, exactPreview)

	// Assert.
	if exactErr != nil || len(exact) != maxPatchDiffBytes {
		t.Fatalf("exact diff = %d bytes, error %v", len(exact), exactErr)
	}
	if over != "" || !errors.Is(overErr, errMCPPreviewDiffLimit) {
		t.Fatalf("over diff = %d bytes, error %v; want empty explicit limit error", len(over), overErr)
	}
	if cancelled != "" || !errors.Is(cancelledErr, context.Canceled) {
		t.Fatalf("cancelled diff = %q, error %v", cancelled, cancelledErr)
	}
}

func TestBoundedOutputBufferHasExactLimitAndNoPartialOverflowWrite(t *testing.T) {
	// Arrange.
	exact := newBoundedOutputBuffer(4)
	over := newBoundedOutputBuffer(4)

	// Act.
	exactCount, exactErr := exact.Write([]byte("1234"))
	firstCount, firstErr := over.Write([]byte("12"))
	overCount, overErr := over.Write([]byte("345"))

	// Assert.
	if exactCount != 4 || exactErr != nil || exact.String() != "1234" {
		t.Fatalf("exact write = (%d, %v, %q)", exactCount, exactErr, exact.String())
	}
	if firstCount != 2 || firstErr != nil || overCount != 0 || !errors.Is(overErr, errMCPOutputLimit) {
		t.Fatalf("overflow writes = first(%d,%v) over(%d,%v)", firstCount, firstErr, overCount, overErr)
	}
	if over.String() != "12" {
		t.Fatalf("overflow appended partial bytes: %q", over.String())
	}
}

func TestOwnedMigrationCaptureClosesExactlyOnceAndSuppressesOutputOnCloseFailure(t *testing.T) {
	// Arrange.
	closeFailure := errors.New("close migration source failed")
	loadFailure := errors.New("list migration source failed")
	successThenCloseFailure := &ownedSnapshotTestSource{
		paths:    []string{"alpha.md"},
		files:    map[string][]byte{"alpha.md": []byte("---\ntype: Knowledge\n---\nAlpha.\n")},
		closeErr: closeFailure,
	}
	loadAndCloseFailure := &ownedSnapshotTestSource{pathsErr: loadFailure, closeErr: closeFailure}

	// Act.
	captured, revision, err := captureOwnedMigrationSourceContext(t.Context(), successThenCloseFailure)
	failedCapture, failedRevision, joinedErr := captureOwnedMigrationSourceContext(t.Context(), loadAndCloseFailure)
	nilCapture, nilRevision, nilErr := captureOwnedMigrationSourceContext(t.Context(), nil)

	// Assert.
	if captured != nil || !revision.IsZero() || !errors.Is(err, closeFailure) {
		t.Fatalf("close failure output = (%#v, %q, %v)", captured, revision, err)
	}
	if successThenCloseFailure.closeCalls.Load() != 1 {
		t.Fatalf("successful source close calls = %d, want 1", successThenCloseFailure.closeCalls.Load())
	}
	if failedCapture != nil || !failedRevision.IsZero() ||
		!errors.Is(joinedErr, loadFailure) || !errors.Is(joinedErr, closeFailure) {
		t.Fatalf("joined failure output = (%#v, %q, %v)", failedCapture, failedRevision, joinedErr)
	}
	if loadAndCloseFailure.closeCalls.Load() != 1 {
		t.Fatalf("failed source close calls = %d, want 1", loadAndCloseFailure.closeCalls.Load())
	}
	if nilCapture != nil || !nilRevision.IsZero() || !errors.Is(nilErr, errNilOwnedBundleSource) {
		t.Fatalf("nil owner output = (%#v, %q, %v)", nilCapture, nilRevision, nilErr)
	}
}

func TestBundleRootCloserFailuresAreJoinedAfterExactOnceReverseClose(t *testing.T) {
	// Arrange.
	info, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rootClose := errors.New("close volume root")
	childClose := errors.New("close child root")
	root := &faultBundleRoot{info: info, closeErr: rootClose}
	child := &faultBundleRoot{info: info, closeErr: childClose}
	openCalls := 0
	path := filepath.Join(string(filepath.Separator), "bundle")

	// Act.
	validationErr := validateBundleRootPathWithOpeners(
		t.Context(),
		path,
		func(string) (bundleRootHandle, error) { return root, nil },
		func(bundleRootHandle, string) (bundleRootHandle, error) {
			openCalls++
			return child, nil
		},
	)
	nilRootErr := validateBundleRootPathWithOpeners(
		t.Context(),
		path,
		func(string) (bundleRootHandle, error) { return nil, nil },
		func(bundleRootHandle, string) (bundleRootHandle, error) { return child, nil },
	)

	// Assert.
	if !errors.Is(validationErr, rootClose) || !errors.Is(validationErr, childClose) {
		t.Fatalf("validation error = %v, want both close identities", validationErr)
	}
	if openCalls != 1 || root.closeCalls.Load() != 1 || child.closeCalls.Load() != 1 {
		t.Fatalf("lifecycle = opens:%d root-closes:%d child-closes:%d", openCalls, root.closeCalls.Load(), child.closeCalls.Load())
	}
	if !errors.Is(nilRootErr, errNilBundleRoot) {
		t.Fatalf("nil root error = %v, want errNilBundleRoot", nilRootErr)
	}
}

func assertCancellationOnlyResult(t *testing.T, result *mcp.CallToolResult) {
	t.Helper()
	if result == nil || !result.IsError {
		t.Fatalf("result = %#v, want cancellation error", result)
	}
	envelope, ok := result.StructuredContent.(errorEnvelope)
	if !ok {
		t.Fatalf("structured content = %T, want errorEnvelope", result.StructuredContent)
	}
	if envelope.Code != "operation_cancelled" || !envelope.Retryable || len(envelope.Diagnostics) != 0 {
		t.Fatalf("cancellation envelope = %#v", envelope)
	}
	if len(result.Content) != 1 {
		t.Fatalf("cancellation content items = %d, want one error text", len(result.Content))
	}
}

type faultBundleRoot struct {
	info       os.FileInfo
	closeErr   error
	closeCalls atomic.Int64
}

func (root *faultBundleRoot) Lstat(string) (os.FileInfo, error) { return root.info, nil }

func (root *faultBundleRoot) Close() error {
	root.closeCalls.Add(1)
	return root.closeErr
}
