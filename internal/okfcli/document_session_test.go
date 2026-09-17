package okfcli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/skosovsky/okf/bundle"
)

type recordingDocumentSession struct {
	document     bundle.Document
	resolution   bundle.VersionResolution
	rewriteErr   error
	closeErr     error
	rewriteCalls int
	closeCalls   int
	rewritten    bundle.Document
	events       *[]string
}

func (session *recordingDocumentSession) Document() bundle.Document {
	if session.events != nil {
		*session.events = append(*session.events, "document")
	}
	return session.document
}

func (session *recordingDocumentSession) VersionResolution() bundle.VersionResolution {
	if session.events != nil {
		*session.events = append(*session.events, "version")
	}
	return session.resolution
}

func (session *recordingDocumentSession) RewriteContext(_ context.Context, document bundle.Document) error {
	session.rewriteCalls++
	session.rewritten = document
	if session.events != nil {
		*session.events = append(*session.events, "rewrite")
	}
	return session.rewriteErr
}

func (session *recordingDocumentSession) Close() error {
	session.closeCalls++
	if session.events != nil {
		*session.events = append(*session.events, "close")
	}
	return session.closeErr
}

func TestDocumentSessionParseUsesOneCapturedRevisionAndClosesBeforeOutput(t *testing.T) {
	tests := []struct {
		name         string
		spec         string
		wantSelector string
	}{
		{name: "automatic", spec: "auto", wantSelector: ""},
		{name: "explicit", spec: "0.2", wantSelector: "0.2"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			document := validDocumentFixture(t)
			events := make([]string, 0, 5)
			session := &recordingDocumentSession{
				document: document,
				resolution: bundle.VersionResolution{
					Effective:     bundle.OKFVersion,
					Source:        bundle.VersionSourceExplicit,
					Compatibility: bundle.VersionCompatibilityNative,
				},
				events: &events,
			}
			openCalls := 0
			const path = "captured/concept.md"
			dependencies := productionRunDependencies()
			dependencies.openDocument = func(_ context.Context, gotPath, gotSelector string) (documentSession, error) {
				openCalls++
				events = append(events, "open")
				if gotPath != path || gotSelector != test.wantSelector {
					t.Fatalf("open path/selector = %q/%q, want %q/%q", gotPath, gotSelector, path, test.wantSelector)
				}
				return session, nil
			}
			stdout := &scriptedWriter{session: session, events: &events}
			var stderr bytes.Buffer

			// Act.
			code := runWithDependencies(
				[]string{"parse", path, "--spec", test.spec, "--json"},
				stdout,
				&stderr,
				dependencies,
			)

			// Assert.
			if code != 0 || stderr.Len() != 0 {
				t.Fatalf("parse code/stderr = %d/%q, want 0/empty", code, stderr.String())
			}
			if openCalls != 1 || session.closeCalls != 1 || session.rewriteCalls != 0 {
				t.Fatalf("open/close/rewrite calls = %d/%d/%d, want 1/1/0", openCalls, session.closeCalls, session.rewriteCalls)
			}
			if stdout.writeCalls == 0 || stdout.writesBeforeEnd != 0 {
				t.Fatalf("stdout writes/before close = %d/%d, want positive/0", stdout.writeCalls, stdout.writesBeforeEnd)
			}
			if want := []string{"open", "document", "version", "close", "stdout"}; !reflect.DeepEqual(events, want) {
				t.Fatalf("events = %v, want %v", events, want)
			}
			if !strings.Contains(stdout.String(), `"effective_version":"0.2"`) {
				t.Fatalf("parse output = %q, missing captured version", stdout.String())
			}
		})
	}
}

func TestDocumentSessionFmtPreviewAndWriteHaveOneLifecycle(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantRewrite  int
		wantSelector string
		wantEvents   []string
	}{
		{
			name:         "preview",
			args:         []string{"fmt", "concept.md", "--spec", "auto"},
			wantSelector: "",
			wantEvents:   []string{"open", "document", "close", "stdout"},
		},
		{
			name:         "write",
			args:         []string{"fmt", "concept.md", "--spec", "0.2", "--write"},
			wantRewrite:  1,
			wantSelector: "0.2",
			wantEvents:   []string{"open", "document", "rewrite", "close", "stdout"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			document := validDocumentFixture(t)
			events := make([]string, 0, 5)
			session := &recordingDocumentSession{document: document, events: &events}
			openCalls := 0
			dependencies := productionRunDependencies()
			dependencies.openDocument = func(_ context.Context, gotPath, gotSelector string) (documentSession, error) {
				openCalls++
				events = append(events, "open")
				if gotPath != "concept.md" || gotSelector != test.wantSelector {
					t.Fatalf("open path/selector = %q/%q, want concept.md/%q", gotPath, gotSelector, test.wantSelector)
				}
				return session, nil
			}
			stdout := &scriptedWriter{session: session, events: &events}
			var stderr bytes.Buffer

			// Act.
			code := runWithDependencies(test.args, stdout, &stderr, dependencies)

			// Assert.
			if code != 0 || stderr.Len() != 0 {
				t.Fatalf("fmt code/stderr = %d/%q, want 0/empty", code, stderr.String())
			}
			if openCalls != 1 || session.closeCalls != 1 || session.rewriteCalls != test.wantRewrite {
				t.Fatalf("open/close/rewrite calls = %d/%d/%d, want 1/1/%d", openCalls, session.closeCalls, session.rewriteCalls, test.wantRewrite)
			}
			if stdout.writeCalls == 0 || stdout.writesBeforeEnd != 0 {
				t.Fatalf("stdout writes/before close = %d/%d, want positive/0", stdout.writeCalls, stdout.writesBeforeEnd)
			}
			if !reflect.DeepEqual(events, test.wantEvents) {
				t.Fatalf("events = %v, want %v", events, test.wantEvents)
			}
			if test.wantRewrite == 1 {
				captured, capturedErr := document.Serialize()
				rewritten, rewrittenErr := session.rewritten.Serialize()
				if capturedErr != nil || rewrittenErr != nil || rewritten != captured {
					t.Fatalf("rewritten document = %q/%v, want captured %q/%v", rewritten, rewrittenErr, captured, capturedErr)
				}
			}
		})
	}
}

func TestDocumentSessionParseJoinsProjectionAndCloseErrorsWithoutOutput(t *testing.T) {
	// Arrange.
	projectErr := errors.New("project captured document")
	closeErr := errors.New("close captured document")
	session := &recordingDocumentSession{
		document: validDocumentFixture(t),
		closeErr: closeErr,
	}
	openDocument := func(context.Context, string, string) (documentSession, error) {
		return session, nil
	}
	project := func(
		context.Context,
		string,
		bundle.Document,
		bundle.VersionResolution,
		*time.Time,
	) (parseResponse, error) {
		return parseResponse{}, projectErr
	}
	var stdout bytes.Buffer

	// Act.
	_, err := cmdParseWithDocumentSessionAndProjector(
		[]string{"concept.md", "--json"},
		&stdout,
		openDocument,
		project,
	)

	// Assert.
	if !errors.Is(err, projectErr) || !errors.Is(err, closeErr) {
		t.Fatalf("parse error = %v, want joined projection and close identities", err)
	}
	if stdout.Len() != 0 || session.closeCalls != 1 || session.rewriteCalls != 0 {
		t.Fatalf("stdout/close/rewrite = %q/%d/%d, want empty/1/0", stdout.String(), session.closeCalls, session.rewriteCalls)
	}
}

func TestDocumentSessionParseOpenAndCloseFailuresHaveNoOutput(t *testing.T) {
	openErr := errors.New("open captured document")
	closeErr := errors.New("close captured document")
	tests := []struct {
		name      string
		session   *recordingDocumentSession
		openErr   error
		wantErr   error
		wantClose int
	}{
		{
			name:    "open",
			openErr: openErr,
			wantErr: openErr,
		},
		{
			name: "close",
			session: &recordingDocumentSession{
				document: validDocumentFixture(t),
				resolution: bundle.VersionResolution{
					Effective:     bundle.OKFVersion,
					Source:        bundle.VersionSourceExplicit,
					Compatibility: bundle.VersionCompatibilityNative,
				},
				closeErr: closeErr,
			},
			wantErr:   closeErr,
			wantClose: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			openCalls := 0
			openDocument := func(context.Context, string, string) (documentSession, error) {
				openCalls++
				return test.session, test.openErr
			}
			var stdout bytes.Buffer

			// Act.
			_, err := cmdParseWithDocumentSession(
				[]string{"concept.md", "--json"},
				&stdout,
				openDocument,
			)

			// Assert.
			if openCalls != 1 || !errors.Is(err, test.wantErr) {
				t.Fatalf("open calls/error = %d/%v, want 1/errors.Is(%v)", openCalls, err, test.wantErr)
			}
			if stdout.Len() != 0 {
				t.Fatalf("parse failure stdout = %q, want empty", stdout.String())
			}
			if test.session != nil && test.session.closeCalls != test.wantClose {
				t.Fatalf("close calls = %d, want %d", test.session.closeCalls, test.wantClose)
			}
		})
	}
}

func TestDocumentSessionFmtFailureMatrixPreservesIdentitiesAndStdoutPurity(t *testing.T) {
	openErr := errors.New("open captured document")
	rewriteErr := errors.New("rewrite captured document")
	closeErr := errors.New("close captured document")
	invalidDocument := bundle.NewDocument(bundle.NewFrontmatter(), string([]byte{0xff}))

	tests := []struct {
		name        string
		args        []string
		session     *recordingDocumentSession
		openErr     error
		wantErrors  []error
		wantClose   int
		wantRewrite int
	}{
		{
			name:       "open",
			args:       []string{"concept.md"},
			openErr:    openErr,
			wantErrors: []error{openErr},
		},
		{
			name: "serialize and close",
			args: []string{"concept.md"},
			session: &recordingDocumentSession{
				document: invalidDocument,
				closeErr: closeErr,
			},
			wantErrors: []error{bundle.ErrInvalidEncoding, closeErr},
			wantClose:  1,
		},
		{
			name: "rewrite and close",
			args: []string{"concept.md", "--write"},
			session: &recordingDocumentSession{
				document:   validDocumentFixture(t),
				rewriteErr: rewriteErr,
				closeErr:   closeErr,
			},
			wantErrors:  []error{rewriteErr, closeErr},
			wantClose:   1,
			wantRewrite: 1,
		},
		{
			name: "close after preview",
			args: []string{"concept.md"},
			session: &recordingDocumentSession{
				document: validDocumentFixture(t),
				closeErr: closeErr,
			},
			wantErrors: []error{closeErr},
			wantClose:  1,
		},
		{
			name: "close after rewrite",
			args: []string{"concept.md", "--write"},
			session: &recordingDocumentSession{
				document: validDocumentFixture(t),
				closeErr: closeErr,
			},
			wantErrors:  []error{closeErr},
			wantClose:   1,
			wantRewrite: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			openCalls := 0
			openDocument := func(context.Context, string, string) (documentSession, error) {
				openCalls++
				return test.session, test.openErr
			}
			var stdout bytes.Buffer

			// Act.
			_, err := cmdFmtWithDocumentSession(test.args, &stdout, openDocument)

			// Assert.
			if openCalls != 1 {
				t.Fatalf("open calls = %d, want 1", openCalls)
			}
			for _, wantErr := range test.wantErrors {
				if !errors.Is(err, wantErr) {
					t.Errorf("fmt error = %v, want errors.Is(%v)", err, wantErr)
				}
			}
			if stdout.Len() != 0 {
				t.Fatalf("fmt failure stdout = %q, want empty", stdout.String())
			}
			if test.session != nil && (test.session.closeCalls != test.wantClose || test.session.rewriteCalls != test.wantRewrite) {
				t.Fatalf("close/rewrite calls = %d/%d, want %d/%d", test.session.closeCalls, test.session.rewriteCalls, test.wantClose, test.wantRewrite)
			}
		})
	}
}

func TestDocumentSessionProductionPathsHaveNoLegacyResolverOrDirectDocumentIO(t *testing.T) {
	// Arrange.
	productionFiles := []string{"run.go", "projection.go"}
	forbidden := []string{
		"resolveDocumentVersion",
		"requireRegularDocumentPath",
		"bundleContainsFile",
		"os.ReadFile(path)",
		"os.Stat(path)",
		"os.WriteFile(path",
		"bundle.ParseDocument(string(text))",
	}

	// Act.
	var production strings.Builder
	for _, name := range productionFiles {
		content, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("read production file %q: %v", name, err)
		}
		production.Write(content)
	}

	// Assert.
	for _, token := range forbidden {
		if strings.Contains(production.String(), token) {
			t.Errorf("production parse/fmt retains forbidden token %q", token)
		}
	}
}

func validDocumentFixture(t *testing.T) bundle.Document {
	t.Helper()
	document, err := bundle.ParseDocument("---\ntype: Note\ntitle: Captured\n---\n\nBody.\n")
	if err != nil {
		t.Fatalf("bundle.ParseDocument() error = %v", err)
	}
	return document
}
