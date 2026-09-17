package bundle

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestDocumentSessionScopeResolutionMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		arrange      func(t *testing.T, sandbox string) string
		wantRoot     func(sandbox string) string
		wantRelative string
		wantVersion  VersionResolution
	}{
		{
			name: "outermost declaring root wins over nearer declaring index",
			arrange: func(t *testing.T, sandbox string) string {
				t.Helper()
				outer := filepath.Join(sandbox, "outer")
				documentSessionWriteFile(t, filepath.Join(outer, "index.md"), documentSessionIndex("0.2", "outer"))
				documentSessionWriteFile(t, filepath.Join(outer, "nested", "index.md"), documentSessionIndex("0.2", "nested"))
				target := filepath.Join(outer, "nested", "deep", "concept.md")
				documentSessionWriteFile(t, target, documentSessionConcept("concept", "captured"))
				return target
			},
			wantRoot: func(sandbox string) string {
				return canonicalVolumePath(filepath.Clean(filepath.Join(sandbox, "outer")))
			},
			wantRelative: "nested/deep/concept.md",
			wantVersion: VersionResolution{
				Declared:      "0.2",
				Effective:     "0.2",
				Source:        VersionSourceDeclared,
				Compatibility: VersionCompatibilityNative,
			},
		},
		{
			name: "nearest index is fallback when no ancestor declares a version",
			arrange: func(t *testing.T, sandbox string) string {
				t.Helper()
				near := filepath.Join(sandbox, "near")
				documentSessionWriteFile(t, filepath.Join(sandbox, "index.md"), documentSessionIndex("", "far"))
				documentSessionWriteFile(t, filepath.Join(near, "index.md"), documentSessionIndex("", "near"))
				target := filepath.Join(near, "deep", "concept.md")
				documentSessionWriteFile(t, target, documentSessionConcept("concept", "captured"))
				return target
			},
			wantRoot: func(sandbox string) string {
				return canonicalVolumePath(filepath.Clean(filepath.Join(sandbox, "near")))
			},
			wantRelative: "deep/concept.md",
			wantVersion: VersionResolution{
				Effective:     "0.2",
				Source:        VersionSourceDefault,
				Compatibility: VersionCompatibilityNative,
			},
		},
		{
			name: "standalone document is scoped to its parent",
			arrange: func(t *testing.T, sandbox string) string {
				t.Helper()
				target := filepath.Join(sandbox, "standalone", "note.md")
				documentSessionWriteFile(t, target, documentSessionConcept("note", "captured"))
				return target
			},
			wantRoot: func(sandbox string) string {
				return canonicalVolumePath(filepath.Clean(filepath.Join(sandbox, "standalone")))
			},
			wantRelative: "note.md",
			wantVersion: VersionResolution{
				Effective:     "0.2",
				Source:        VersionSourceDefault,
				Compatibility: VersionCompatibilityNative,
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			sandbox := t.TempDir()
			target := test.arrange(t, sandbox)

			// Act.
			session, err := OpenDocumentSessionContext(context.Background(), target, "")
			if err == nil {
				t.Cleanup(func() { _ = session.Close() })
			}

			// Assert.
			if err != nil {
				t.Fatalf("OpenDocumentSessionContext() error = %v", err)
			}
			gotRoot, wantRoot := session.Root(), test.wantRoot(sandbox)
			if !filepath.IsAbs(gotRoot) {
				t.Fatalf("Root() = %q, want absolute path", gotRoot)
			}
			documentSessionAssertSamePathIdentity(t, gotRoot, wantRoot)
			gotPath := session.Path()
			if !filepath.IsAbs(gotPath) {
				t.Fatalf("Path() = %q, want absolute path", gotPath)
			}
			documentSessionAssertSamePathIdentity(t, gotPath, target)
			if got := session.RelativePath(); got != test.wantRelative {
				t.Fatalf("RelativePath() = %q, want %q", got, test.wantRelative)
			}
			if got := session.VersionResolution(); got != test.wantVersion {
				t.Fatalf("VersionResolution() = %#v, want %#v", got, test.wantVersion)
			}
		})
	}
}

func TestDocumentSessionCapturesEveryDocumentSurface(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		relative string
		content  string
	}{
		{
			name:     "root index",
			relative: "index.md",
			content:  documentSessionIndex("0.2", "root"),
		},
		{
			name:     "nested index",
			relative: "nested/index.md",
			content:  documentSessionIndex("", "nested"),
		},
		{
			name:     "concept",
			relative: "nested/concept.md",
			content:  documentSessionConcept("concept", "concept body"),
		},
		{
			name:     "root log",
			relative: "log.md",
			content:  documentSessionConcept("log", "# Log\n\n## 2026-07-30\n* updated"),
		},
		{
			name:     "nested log",
			relative: "nested/log.md",
			content:  documentSessionConcept("log", "# Nested log"),
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			if test.relative != "index.md" {
				documentSessionWriteFile(t, filepath.Join(root, "index.md"), documentSessionIndex("0.2", "root"))
			}
			target := filepath.Join(root, filepath.FromSlash(test.relative))
			documentSessionWriteFile(t, target, test.content)

			// Act.
			session, err := OpenDocumentSessionContext(context.Background(), target, "0.2")
			if err == nil {
				t.Cleanup(func() { _ = session.Close() })
			}

			// Assert.
			if err != nil {
				t.Fatalf("OpenDocumentSessionContext() error = %v", err)
			}
			if got := session.RelativePath(); got != test.relative {
				t.Fatalf("RelativePath() = %q, want %q", got, test.relative)
			}
			if got := session.Bytes(); !bytes.Equal(got, []byte(test.content)) {
				t.Fatalf("Bytes() = %q, want exact %q", got, test.content)
			}
			wantDocument, parseErr := ParseDocument(test.content)
			if parseErr != nil {
				t.Fatalf("ParseDocument(test content) error = %v", parseErr)
			}
			if got := session.Document(); got.Body != wantDocument.Body ||
				got.HasFrontmatter != wantDocument.HasFrontmatter ||
				!bytes.Equal(got.FrontmatterYAML(), wantDocument.FrontmatterYAML()) {
				t.Fatalf("Document() = %#v, want capture equivalent to %#v", got, wantDocument)
			}
		})
	}
}

func TestDocumentSessionCaptureIsImmutableAndDefensive(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	original := documentSessionConcept("original", "captured body")
	documentSessionWriteFile(t, filepath.Join(root, "index.md"), documentSessionIndex("0.2", "root"))
	documentSessionWriteFile(t, target, original)
	session, err := OpenDocumentSessionContext(context.Background(), target, "")
	if err != nil {
		t.Fatalf("OpenDocumentSessionContext() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	// Act.
	firstBytes := session.Bytes()
	firstBytes[0] ^= 0xff
	firstDocument := session.Document()
	firstDocument.Body = "caller mutation"
	if err := firstDocument.Frontmatter.SetString("title", "caller mutation"); err != nil {
		t.Fatalf("mutate caller-owned frontmatter: %v", err)
	}
	firstFrontmatter := firstDocument.FrontmatterYAML()
	if len(firstFrontmatter) > 0 {
		firstFrontmatter[0] ^= 0xff
	}
	secondBytes := session.Bytes()
	secondDocument := session.Document()

	// Assert.
	if !bytes.Equal(secondBytes, []byte(original)) {
		t.Fatalf("second Bytes() = %q, want immutable capture %q", secondBytes, original)
	}
	if secondDocument.Body != "captured body" {
		t.Fatalf("second Document().Body = %q, want captured body", secondDocument.Body)
	}
	if got, ok := secondDocument.Frontmatter.Title(); !ok || got != "original" {
		t.Fatalf("second Document().Frontmatter.Title() = %q, %v; want original, true", got, ok)
	}
	if bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("Bytes() calls alias the same backing array")
	}
}

func TestOpenDocumentSessionRejectsSelectorAndDeclarationFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		declaration string
		selector    string
		want        error
	}{
		{
			name:        "selector conflicts with declaration",
			declaration: "0.2",
			selector:    "0.1",
			want:        ErrVersionConflict,
		},
		{
			name:        "unsupported selector",
			declaration: "",
			selector:    "9.9",
			want:        ErrUnsupportedVersionSelector,
		},
		{
			name:        "malformed selector",
			declaration: "",
			selector:    "02.0",
			want:        ErrInvalidVersionDeclaration,
		},
		{
			name:        "malformed root declaration",
			declaration: "02.0",
			selector:    "",
			want:        ErrInvalidVersionDeclaration,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			documentSessionWriteFile(t, filepath.Join(root, "index.md"), documentSessionIndex(test.declaration, "root"))
			target := filepath.Join(root, "concept.md")
			documentSessionWriteFile(t, target, documentSessionConcept("concept", "body"))

			// Act.
			session, err := OpenDocumentSessionContext(context.Background(), target, test.selector)
			if session != nil {
				_ = session.Close()
			}

			// Assert.
			if !errors.Is(err, test.want) {
				t.Fatalf("OpenDocumentSessionContext() error = %v, want errors.Is(%v)", err, test.want)
			}
			if session != nil {
				t.Fatal("OpenDocumentSessionContext() returned a session on failed resolution")
			}
			reopened, reopenErr := OpenDocumentSessionContext(context.Background(), target, "")
			if test.declaration == "02.0" {
				if reopened != nil {
					_ = reopened.Close()
				}
				if !errors.Is(reopenErr, ErrInvalidVersionDeclaration) {
					t.Fatalf("reopen error = %v, want invalid declaration", reopenErr)
				}
				return
			}
			if reopenErr != nil {
				t.Fatalf("failed open leaked publication ownership: %v", reopenErr)
			}
			_ = reopened.Close()
		})
	}
}

func TestOpenDocumentSessionRejectsMissingSymlinkAndDirectoryTargets(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	regular := filepath.Join(root, "regular.md")
	documentSessionWriteFile(t, regular, documentSessionConcept("regular", "body"))
	symlink := filepath.Join(root, "symlink.md")
	if err := os.Symlink(regular, symlink); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	directory := filepath.Join(root, "directory.md")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	tests := []struct {
		name   string
		target string
		want   error
	}{
		{name: "missing", target: filepath.Join(root, "missing.md"), want: fs.ErrNotExist},
		{name: "symlink", target: symlink, want: ErrNotRegularFile},
		{name: "directory", target: directory, want: ErrNotRegularFile},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			// Act.
			session, err := OpenDocumentSessionContext(context.Background(), test.target, "")
			if session != nil {
				_ = session.Close()
			}

			// Assert.
			if !errors.Is(err, test.want) {
				t.Fatalf("OpenDocumentSessionContext() error = %v, want errors.Is(%v)", err, test.want)
			}
			if session != nil {
				t.Fatal("OpenDocumentSessionContext() returned a session for unsafe target")
			}
		})
	}
}

func TestDocumentSessionCopiedHandleSharesCloseLifecycle(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	documentSessionWriteFile(t, target, documentSessionConcept("concept", "before"))
	var enteredCAS atomic.Int32
	session, err := openDocumentSessionContext(
		context.Background(),
		target,
		"",
		documentSessionHooks{
			publish: documentPublishHooks{
				beforeCAS: func(*DocumentSession) error {
					enteredCAS.Add(1)
					return nil
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("openDocumentSessionContext() error = %v", err)
	}
	copied := *session

	// Act.
	closeErr := copied.Close()
	rewriteErr := session.RewriteContext(
		context.Background(),
		documentSessionParsed(t, documentSessionConcept("concept", "after")),
	)
	originalCloseErr := session.Close()

	// Assert.
	if closeErr != nil {
		t.Fatalf("copied.Close() error = %v", closeErr)
	}
	if !errors.Is(rewriteErr, ErrDocumentConflict) {
		t.Fatalf("original RewriteContext() error = %v, want ErrDocumentConflict", rewriteErr)
	}
	if got := enteredCAS.Load(); got != 0 {
		t.Fatalf("publication hook entered %d times after copied handle closed shared lifecycle, want 0", got)
	}
	if originalCloseErr != nil {
		t.Fatalf("original Close() error = %v", originalCloseErr)
	}
	reopened, err := OpenDocumentSessionContext(context.Background(), target, "")
	if err != nil {
		t.Fatalf("copied Close did not release ownership: %v", err)
	}
	_ = reopened.Close()
}

func TestDocumentSessionCopiedHandleSharesOneShotRewriteLifecycle(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	documentSessionWriteFile(t, target, documentSessionConcept("concept", "before"))
	var enteredCAS atomic.Int32
	session, err := openDocumentSessionContext(
		context.Background(),
		target,
		"",
		documentSessionHooks{
			publish: documentPublishHooks{
				beforeCAS: func(*DocumentSession) error {
					enteredCAS.Add(1)
					return nil
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("openDocumentSessionContext() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	copied := *session
	replacement := documentSessionParsed(t, documentSessionConcept("concept", "after"))

	// Act.
	firstErr := copied.RewriteContext(context.Background(), replacement)
	secondErr := session.RewriteContext(context.Background(), replacement)

	// Assert.
	if firstErr != nil {
		t.Fatalf("copied RewriteContext() error = %v", firstErr)
	}
	if !errors.Is(secondErr, ErrDocumentConflict) {
		t.Fatalf("original RewriteContext() second attempt error = %v, want ErrDocumentConflict", secondErr)
	}
	if got := enteredCAS.Load(); got != 1 {
		t.Fatalf("publication hook entered %d times, want exactly one shared attempt", got)
	}
}

func TestDocumentSessionOwnsWholePhysicalScopeUntilClose(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	firstTarget := filepath.Join(root, "first.md")
	secondTarget := filepath.Join(root, "nested", "second.md")
	documentSessionWriteFile(t, filepath.Join(root, "index.md"), documentSessionIndex("0.2", "root"))
	documentSessionWriteFile(t, firstTarget, documentSessionConcept("first", "body"))
	documentSessionWriteFile(t, secondTarget, documentSessionConcept("second", "body"))
	first, err := OpenDocumentSessionContext(context.Background(), firstTarget, "")
	if err != nil {
		t.Fatalf("first OpenDocumentSessionContext() error = %v", err)
	}

	// Act.
	conflicting, conflictErr := OpenDocumentSessionContext(context.Background(), secondTarget, "")
	if conflicting != nil {
		_ = conflicting.Close()
	}
	closeErr := first.Close()
	reopened, reopenErr := OpenDocumentSessionContext(context.Background(), secondTarget, "")
	if reopened != nil {
		defer reopened.Close()
	}

	// Assert.
	if !errors.Is(conflictErr, ErrDocumentConflict) {
		t.Fatalf("second OpenDocumentSessionContext() error = %v, want ErrDocumentConflict", conflictErr)
	}
	if !errors.Is(conflictErr, ErrPublicationOwnershipConflict) {
		t.Fatalf("second OpenDocumentSessionContext() error = %v, want ErrPublicationOwnershipConflict", conflictErr)
	}
	if conflicting != nil {
		t.Fatal("conflicting OpenDocumentSessionContext() returned a session")
	}
	if closeErr != nil {
		t.Fatalf("first Close() error = %v", closeErr)
	}
	if reopenErr != nil {
		t.Fatalf("OpenDocumentSessionContext() after Close error = %v", reopenErr)
	}
}

func TestDocumentSessionCopiedHandlesAuthorizeExactlyOneConcurrentRewrite(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	documentSessionWriteFile(t, target, documentSessionConcept("concept", "before"))
	var enteredCAS atomic.Int32
	session, err := openDocumentSessionContext(
		context.Background(),
		target,
		"",
		documentSessionHooks{
			publish: documentPublishHooks{
				beforeCAS: func(*DocumentSession) error {
					enteredCAS.Add(1)
					return nil
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("openDocumentSessionContext() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	copied := *session
	replacement := documentSessionParsed(t, documentSessionConcept("concept", "after"))
	start := make(chan struct{})
	results := make(chan error, 2)

	// Act.
	for _, handle := range []*DocumentSession{session, &copied} {
		handle := handle
		go func() {
			<-start
			results <- handle.RewriteContext(context.Background(), replacement)
		}()
	}
	close(start)
	firstErr, secondErr := <-results, <-results

	// Assert.
	successes := 0
	conflicts := 0
	for _, err := range []error{firstErr, secondErr} {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrDocumentConflict):
			conflicts++
		default:
			t.Fatalf("concurrent RewriteContext() unexpected error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent results = %v, %v; want one success and one conflict", firstErr, secondErr)
	}
	if got := enteredCAS.Load(); got != 1 {
		t.Fatalf("publication CAS entered %d times, want exactly one", got)
	}
}

func TestDocumentSessionCloseWaitsForActiveRewrite(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	documentSessionWriteFile(t, target, documentSessionConcept("concept", "before"))
	entered := make(chan struct{})
	release := make(chan struct{})
	session, err := openDocumentSessionContext(
		context.Background(),
		target,
		"",
		documentSessionHooks{
			publish: documentPublishHooks{
				beforeCAS: func(*DocumentSession) error {
					close(entered)
					<-release
					return nil
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("openDocumentSessionContext() error = %v", err)
	}
	rewriteDone := make(chan error, 1)
	closeDone := make(chan error, 1)
	replacement := documentSessionParsed(t, documentSessionConcept("concept", "after"))

	// Act.
	go func() {
		rewriteDone <- session.RewriteContext(context.Background(), replacement)
	}()
	<-entered
	go func() {
		closeDone <- session.Close()
	}()
	select {
	case err := <-closeDone:
		t.Fatalf("Close() returned before active rewrite completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)

	// Assert.
	if err := <-rewriteDone; err != nil {
		t.Fatalf("RewriteContext() error = %v", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := session.RewriteContext(
		context.Background(),
		documentSessionParsed(t, documentSessionConcept("concept", "third")),
	); !errors.Is(err, ErrDocumentConflict) {
		t.Fatalf("RewriteContext() after Close error = %v, want ErrDocumentConflict", err)
	}
}

func TestDocumentSessionCancellationIsIdentityPreservingAndConsumesAttempt(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	original := documentSessionConcept("concept", "before")
	documentSessionWriteFile(t, target, original)
	session, err := OpenDocumentSessionContext(context.Background(), target, "")
	if err != nil {
		t.Fatalf("OpenDocumentSessionContext() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Act.
	firstErr := session.RewriteContext(
		ctx,
		documentSessionParsed(t, documentSessionConcept("concept", "after")),
	)
	secondErr := session.RewriteContext(
		context.Background(),
		documentSessionParsed(t, documentSessionConcept("concept", "after")),
	)

	// Assert.
	if !errors.Is(firstErr, context.Canceled) {
		t.Fatalf("canceled RewriteContext() error = %v, want context.Canceled", firstErr)
	}
	if !errors.Is(secondErr, ErrDocumentConflict) {
		t.Fatalf("second RewriteContext() error = %v, want consumed ErrDocumentConflict", secondErr)
	}
	if got := documentSessionReadFile(t, target); got != original {
		t.Fatalf("target after canceled rewrite = %q, want unchanged %q", got, original)
	}
	documentSessionAssertNoArtifacts(t, root)
}

func TestOpenDocumentSessionCancellationIsIdentityPreservingAndReleasesOwnership(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		open  func(ctx context.Context, target string) (*DocumentSession, error)
		setup func() (context.Context, context.CancelFunc)
	}{
		{
			name: "already canceled",
			setup: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, cancel
			},
			open: func(ctx context.Context, target string) (*DocumentSession, error) {
				return OpenDocumentSessionContext(ctx, target, "")
			},
		},
		{
			name: "canceled after lock",
			setup: func() (context.Context, context.CancelFunc) {
				return context.WithCancel(context.Background())
			},
			open: func(ctx context.Context, target string) (*DocumentSession, error) {
				cancel := ctx.Value(documentSessionCancelContextKey{}).(context.CancelFunc)
				return openDocumentSessionContext(
					ctx,
					target,
					"",
					documentSessionHooks{
						afterLock: func() error {
							cancel()
							return nil
						},
					},
				)
			},
		},
		{
			name: "canceled after bundle load",
			setup: func() (context.Context, context.CancelFunc) {
				return context.WithCancel(context.Background())
			},
			open: func(ctx context.Context, target string) (*DocumentSession, error) {
				cancel := ctx.Value(documentSessionCancelContextKey{}).(context.CancelFunc)
				return openDocumentSessionContext(
					ctx,
					target,
					"",
					documentSessionHooks{
						afterLoad: func() error {
							cancel()
							return nil
						},
					},
				)
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			target := filepath.Join(root, "concept.md")
			documentSessionWriteFile(t, target, documentSessionConcept("concept", "body"))
			ctx, cancel := test.setup()
			defer cancel()
			ctx = context.WithValue(ctx, documentSessionCancelContextKey{}, context.CancelFunc(cancel))

			// Act.
			session, err := test.open(ctx, target)
			if session != nil {
				_ = session.Close()
			}

			// Assert.
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("open error = %v, want context.Canceled", err)
			}
			if session != nil {
				t.Fatal("canceled open returned a session")
			}
			reopened, reopenErr := OpenDocumentSessionContext(context.Background(), target, "")
			if reopenErr != nil {
				t.Fatalf("canceled open leaked publication ownership: %v", reopenErr)
			}
			_ = reopened.Close()
		})
	}
}

func TestDocumentSessionNilAndClosedContract(t *testing.T) {
	t.Parallel()

	// Arrange.
	var nilSession *DocumentSession
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	documentSessionWriteFile(t, target, documentSessionConcept("concept", "before"))
	session, err := OpenDocumentSessionContext(context.Background(), target, "")
	if err != nil {
		t.Fatalf("OpenDocumentSessionContext() error = %v", err)
	}

	// Act.
	nilRewriteErr := nilSession.RewriteContext(context.Background(), Document{})
	nilCloseErr := nilSession.Close()
	firstCloseErr := session.Close()
	secondCloseErr := session.Close()
	closedRewriteErr := session.RewriteContext(context.Background(), Document{})

	// Assert.
	if !errors.Is(nilRewriteErr, ErrDocumentConflict) {
		t.Fatalf("nil RewriteContext() error = %v, want ErrDocumentConflict", nilRewriteErr)
	}
	if nilCloseErr != nil {
		t.Fatalf("nil Close() error = %v", nilCloseErr)
	}
	if firstCloseErr != nil || secondCloseErr != nil {
		t.Fatalf("Close() errors = %v, %v; want idempotent nil", firstCloseErr, secondCloseErr)
	}
	if !errors.Is(closedRewriteErr, ErrDocumentConflict) {
		t.Fatalf("closed RewriteContext() error = %v, want ErrDocumentConflict", closedRewriteErr)
	}
	nilDocument := nilSession.Document()
	if nilSession.Root() != "" || nilSession.Path() != "" || nilSession.RelativePath() != "" ||
		nilSession.Bytes() != nil || nilDocument.Body != "" || nilDocument.HasFrontmatter ||
		!nilDocument.Frontmatter.IsEmpty() || nilSession.VersionResolution() != (VersionResolution{}) {
		t.Fatal("nil session accessors do not return zero values")
	}
}

type documentSessionCancelContextKey struct{}

func TestDocumentSessionDetectsTargetChangeAfterBundleSnapshot(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	original := documentSessionConcept("concept", "before")
	replacement := documentSessionConcept("concept", "after load")
	documentSessionWriteFile(t, target, original)

	// Act.
	session, err := openDocumentSessionContext(
		context.Background(),
		target,
		"",
		documentSessionHooks{
			afterLoad: func() error {
				documentSessionWriteFile(t, target, replacement)
				return nil
			},
		},
	)
	if session != nil {
		_ = session.Close()
	}

	// Assert.
	if !errors.Is(err, ErrDocumentConflict) {
		t.Fatalf("openDocumentSessionContext() error = %v, want ErrDocumentConflict", err)
	}
	if session != nil {
		t.Fatal("openDocumentSessionContext() returned mixed-snapshot session")
	}
	reopened, reopenErr := OpenDocumentSessionContext(context.Background(), target, "")
	if reopenErr != nil {
		t.Fatalf("failed capture leaked publication ownership: %v", reopenErr)
	}
	defer reopened.Close()
	if got := reopened.Bytes(); !bytes.Equal(got, []byte(replacement)) {
		t.Fatalf("fresh session bytes = %q, want %q", got, replacement)
	}
}

func TestDocumentSessionRejectsSameByteDifferentInodeSwapAfterBundleSnapshot(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	original := documentSessionConcept("concept", "before")
	documentSessionWriteFile(t, target, original)
	if err := os.Chmod(target, 0o640); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	var replacement os.FileInfo

	// Act.
	session, err := openDocumentSessionContext(
		context.Background(),
		target,
		"",
		documentSessionHooks{
			afterLoad: func() error {
				documentSessionAtomicReplace(t, target, original, 0o640)
				var statErr error
				replacement, statErr = os.Lstat(target)
				return statErr
			},
		},
	)
	if session != nil {
		_ = session.Close()
	}

	// Assert.
	if replacement == nil || os.SameFile(before, replacement) {
		t.Fatal("afterLoad replacement did not change target identity")
	}
	if !errors.Is(err, ErrDocumentConflict) {
		t.Fatalf("openDocumentSessionContext() error = %v, want ErrDocumentConflict", err)
	}
	if session != nil {
		t.Fatal("openDocumentSessionContext() authorized a mixed-identity session")
	}
	if got := documentSessionReadFile(t, target); got != original {
		t.Fatalf("replacement bytes = %q, want %q", got, original)
	}
}

func TestDocumentSessionRejectsDetachedScopeAfterBundleSnapshot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		arrange func(t *testing.T, sandbox string) (target string, mutate func())
	}{
		{
			name: "scope root detached",
			arrange: func(t *testing.T, sandbox string) (string, func()) {
				t.Helper()
				root := filepath.Join(sandbox, "bundle")
				target := filepath.Join(root, "concept.md")
				original := documentSessionConcept("concept", "before")
				foreign := documentSessionConcept("foreign", "replacement root")
				documentSessionWriteFile(t, target, original)
				return target, func() {
					detached := filepath.Join(sandbox, "detached-root")
					if err := os.Rename(root, detached); err != nil {
						t.Fatal(err)
					}
					documentSessionWriteFile(t, target, foreign)
				}
			},
		},
		{
			name: "target parent detached",
			arrange: func(t *testing.T, sandbox string) (string, func()) {
				t.Helper()
				root := filepath.Join(sandbox, "bundle")
				documentSessionWriteFile(t, filepath.Join(root, "index.md"), documentSessionIndex("0.2", "root"))
				parent := filepath.Join(root, "nested")
				target := filepath.Join(parent, "concept.md")
				original := documentSessionConcept("concept", "before")
				foreign := documentSessionConcept("foreign", "replacement parent")
				documentSessionWriteFile(t, target, original)
				return target, func() {
					detached := filepath.Join(root, "detached-parent")
					if err := os.Rename(parent, detached); err != nil {
						t.Fatal(err)
					}
					documentSessionWriteFile(t, target, foreign)
				}
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			sandbox := t.TempDir()
			target, mutate := test.arrange(t, sandbox)

			// Act.
			session, err := openDocumentSessionContext(
				context.Background(),
				target,
				"",
				documentSessionHooks{
					afterLoad: func() error {
						mutate()
						return nil
					},
				},
			)
			if session != nil {
				_ = session.Close()
			}

			// Assert.
			if !errors.Is(err, ErrDocumentConflict) {
				t.Fatalf("openDocumentSessionContext() error = %v, want ErrDocumentConflict", err)
			}
			if session != nil {
				t.Fatal("openDocumentSessionContext() authorized a detached scope")
			}
			if got := documentSessionReadFile(t, target); !bytes.Contains([]byte(got), []byte("foreign")) {
				t.Fatalf("replacement tree target = %q, want preserved foreign document", got)
			}
		})
	}
}

func documentSessionIndex(version, title string) string {
	versionLine := ""
	if version != "" {
		versionLine = "okf_version: \"" + version + "\"\n"
	}
	return "---\n" +
		"type: index\n" +
		"title: " + title + "\n" +
		versionLine +
		"---\n\n# " + title + "\n"
}

func documentSessionConcept(title, body string) string {
	return "---\n" +
		"type: concept\n" +
		"title: " + title + "\n" +
		"---\n\n" + body + "\n"
}

func documentSessionParsed(t *testing.T, content string) Document {
	t.Helper()
	document, err := ParseDocument(content)
	if err != nil {
		t.Fatalf("ParseDocument() error = %v", err)
	}
	return document
}

func documentSessionWriteFile(t *testing.T, filename, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(filename), err)
	}
	if err := os.WriteFile(filename, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", filename, err)
	}
}

func documentSessionReadFile(t *testing.T, filename string) string {
	t.Helper()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", filename, err)
	}
	return string(data)
}

func documentSessionAssertSamePathIdentity(t *testing.T, left, right string) {
	t.Helper()
	leftInfo, err := os.Lstat(left)
	if err != nil {
		t.Fatalf("Lstat(%q) error = %v", left, err)
	}
	rightInfo, err := os.Lstat(right)
	if err != nil {
		t.Fatalf("Lstat(%q) error = %v", right, err)
	}
	if !os.SameFile(leftInfo, rightInfo) {
		t.Fatalf("paths %q and %q do not identify the same filesystem object", left, right)
	}
}

func documentSessionAssertNoArtifacts(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if isDocumentTransactionReservedPath(entry.Name()) {
			t.Errorf("transaction artifact remains after completed operation: %q", filename)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir(%q) error = %v", root, err)
	}
}
