package bundle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDocumentSessionRewritePublishesExactSerializationPreservesModeAndCleansArtifacts(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	original := documentSessionConcept("concept", "before")
	documentSessionWriteFile(t, target, original)
	if err := os.Chmod(target, 0o751); err != nil {
		t.Fatalf("Chmod(target) error = %v", err)
	}
	session, err := OpenDocumentSessionContext(context.Background(), target, "")
	if err != nil {
		t.Fatalf("OpenDocumentSessionContext() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	replacement := documentSessionParsed(t, documentSessionConcept("concept", "after"))
	want, err := replacement.Serialize()
	if err != nil {
		t.Fatalf("replacement.Serialize() error = %v", err)
	}

	// Act.
	err = session.RewriteContext(context.Background(), replacement)

	// Assert.
	if err != nil {
		t.Fatalf("RewriteContext() error = %v", err)
	}
	if got := documentSessionReadFile(t, target); got != want {
		t.Fatalf("published bytes = %q, want exact serialization %q", got, want)
	}
	info, err := os.Lstat(target)
	if err != nil {
		t.Fatalf("Lstat(target) error = %v", err)
	}
	if got := publicationPreservedMode(info.Mode()); got != 0o751 {
		t.Fatalf("published mode = %v, want 0751", got)
	}
	documentSessionAssertNoArtifacts(t, root)
	if err := session.RewriteContext(context.Background(), replacement); !errors.Is(err, ErrDocumentConflict) {
		t.Fatalf("second RewriteContext() error = %v, want one-shot ErrDocumentConflict", err)
	}
}

func TestDocumentSessionRewriteNoopStillPerformsExactCAS(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	original := documentSessionConcept("concept", "same")
	documentSessionWriteFile(t, target, original)
	session, err := openDocumentSessionContext(
		context.Background(),
		target,
		"",
		documentSessionHooks{
			publish: documentPublishHooks{
				beforeCAS: func(*DocumentSession) error {
					documentSessionAtomicReplace(t, target, original, 0o644)
					return nil
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("openDocumentSessionContext() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	// Act.
	err = session.RewriteContext(context.Background(), documentSessionParsed(t, original))

	// Assert.
	if !errors.Is(err, ErrDocumentConflict) {
		t.Fatalf("same-byte, different-inode no-op error = %v, want ErrDocumentConflict", err)
	}
	if got := documentSessionReadFile(t, target); got != original {
		t.Fatalf("foreign same-byte replacement changed: %q", got)
	}
	documentSessionAssertNoArtifacts(t, root)
}

func TestDocumentSessionRewriteRejectsCapturedDependencyChangesBeforeMutation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		arrange func(t *testing.T, root string) (target string, mutate func())
	}{
		{
			name: "target inode swapped with same bytes",
			arrange: func(t *testing.T, root string) (string, func()) {
				t.Helper()
				target := filepath.Join(root, "nested", "concept.md")
				content := documentSessionConcept("concept", "before")
				documentSessionWriteFile(t, filepath.Join(root, "index.md"), documentSessionIndex("0.2", "root"))
				documentSessionWriteFile(t, target, content)
				return target, func() {
					documentSessionAtomicReplace(t, target, content, 0o644)
				}
			},
		},
		{
			name: "target bytes changed in place",
			arrange: func(t *testing.T, root string) (string, func()) {
				t.Helper()
				target := filepath.Join(root, "nested", "concept.md")
				documentSessionWriteFile(t, filepath.Join(root, "index.md"), documentSessionIndex("0.2", "root"))
				documentSessionWriteFile(t, target, documentSessionConcept("concept", "before"))
				return target, func() {
					documentSessionWriteFile(t, target, documentSessionConcept("concept", "foreign"))
				}
			},
		},
		{
			name: "target mode changed",
			arrange: func(t *testing.T, root string) (string, func()) {
				t.Helper()
				target := filepath.Join(root, "nested", "concept.md")
				documentSessionWriteFile(t, filepath.Join(root, "index.md"), documentSessionIndex("0.2", "root"))
				documentSessionWriteFile(t, target, documentSessionConcept("concept", "before"))
				return target, func() {
					if err := os.Chmod(target, 0o600); err != nil {
						t.Fatalf("Chmod(target) error = %v", err)
					}
				}
			},
		},
		{
			name: "target replaced by symlink",
			arrange: func(t *testing.T, root string) (string, func()) {
				t.Helper()
				target := filepath.Join(root, "nested", "concept.md")
				foreign := filepath.Join(root, "foreign.md")
				documentSessionWriteFile(t, filepath.Join(root, "index.md"), documentSessionIndex("0.2", "root"))
				documentSessionWriteFile(t, target, documentSessionConcept("concept", "before"))
				documentSessionWriteFile(t, foreign, documentSessionConcept("foreign", "preserve"))
				return target, func() {
					if err := os.Remove(target); err != nil {
						t.Fatalf("Remove(target) error = %v", err)
					}
					if err := os.Symlink(foreign, target); err != nil {
						t.Fatalf("Symlink(target) error = %v", err)
					}
				}
			},
		},
		{
			name: "root declaration bytes changed",
			arrange: func(t *testing.T, root string) (string, func()) {
				t.Helper()
				index := filepath.Join(root, "index.md")
				target := filepath.Join(root, "nested", "concept.md")
				documentSessionWriteFile(t, index, documentSessionIndex("0.2", "root"))
				documentSessionWriteFile(t, target, documentSessionConcept("concept", "before"))
				return target, func() {
					documentSessionWriteFile(t, index, documentSessionIndex("0.1", "root"))
				}
			},
		},
		{
			name: "root declaration inode swapped with same bytes",
			arrange: func(t *testing.T, root string) (string, func()) {
				t.Helper()
				index := filepath.Join(root, "index.md")
				target := filepath.Join(root, "nested", "concept.md")
				content := documentSessionIndex("0.2", "root")
				documentSessionWriteFile(t, index, content)
				documentSessionWriteFile(t, target, documentSessionConcept("concept", "before"))
				return target, func() {
					documentSessionAtomicReplace(t, index, content, 0o644)
				}
			},
		},
		{
			name: "previously absent root index appears",
			arrange: func(t *testing.T, root string) (string, func()) {
				t.Helper()
				target := filepath.Join(root, "concept.md")
				documentSessionWriteFile(t, target, documentSessionConcept("concept", "before"))
				return target, func() {
					documentSessionWriteFile(t, filepath.Join(root, "index.md"), documentSessionIndex("0.2", "new"))
				}
			},
		},
		{
			name: "target parent is detached and replaced",
			arrange: func(t *testing.T, root string) (string, func()) {
				t.Helper()
				parent := filepath.Join(root, "nested", "deep")
				target := filepath.Join(parent, "concept.md")
				content := documentSessionConcept("concept", "before")
				documentSessionWriteFile(t, filepath.Join(root, "index.md"), documentSessionIndex("0.2", "root"))
				documentSessionWriteFile(t, target, content)
				return target, func() {
					detached := filepath.Join(root, "detached-deep")
					if err := os.Rename(parent, detached); err != nil {
						t.Fatalf("Rename(parent) error = %v", err)
					}
					documentSessionWriteFile(t, target, content)
				}
			},
		},
		{
			name: "target ancestor is detached and replaced",
			arrange: func(t *testing.T, root string) (string, func()) {
				t.Helper()
				ancestor := filepath.Join(root, "nested")
				target := filepath.Join(ancestor, "deep", "concept.md")
				content := documentSessionConcept("concept", "before")
				documentSessionWriteFile(t, filepath.Join(root, "index.md"), documentSessionIndex("0.2", "root"))
				documentSessionWriteFile(t, target, content)
				return target, func() {
					detached := filepath.Join(root, "detached-nested")
					if err := os.Rename(ancestor, detached); err != nil {
						t.Fatalf("Rename(ancestor) error = %v", err)
					}
					documentSessionWriteFile(t, target, content)
				}
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			target, mutate := test.arrange(t, root)
			originalPathBytes := documentSessionReadFile(t, target)
			session, err := openDocumentSessionContext(
				context.Background(),
				target,
				"",
				documentSessionHooks{
					publish: documentPublishHooks{
						beforeCAS: func(*DocumentSession) error {
							mutate()
							return nil
						},
					},
				},
			)
			if err != nil {
				t.Fatalf("openDocumentSessionContext() error = %v", err)
			}
			t.Cleanup(func() { _ = session.Close() })

			// Act.
			err = session.RewriteContext(
				context.Background(),
				documentSessionParsed(t, documentSessionConcept("concept", "after")),
			)

			// Assert.
			if !errors.Is(err, ErrDocumentConflict) {
				t.Fatalf("RewriteContext() error = %v, want ErrDocumentConflict", err)
			}
			if got := documentSessionReadFile(t, target); got == documentSessionConcept("concept", "after") {
				t.Fatal("rewrite published after a captured dependency changed")
			}
			if test.name == "target inode swapped with same bytes" && documentSessionReadFile(t, target) != originalPathBytes {
				t.Fatal("same-byte target replacement was modified")
			}
			documentSessionAssertNoArtifacts(t, root)
		})
	}
}

func TestDocumentSessionRootIndexRewriteMustPreserveSelectorAssertion(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "index.md")
	original := documentSessionIndex("0.2", "root")
	documentSessionWriteFile(t, target, original)
	session, err := OpenDocumentSessionContext(context.Background(), target, "0.2")
	if err != nil {
		t.Fatalf("OpenDocumentSessionContext() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	replacement := documentSessionParsed(t, documentSessionIndex("0.1", "root"))

	// Act.
	err = session.RewriteContext(context.Background(), replacement)

	// Assert.
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("RewriteContext() error = %v, want ErrVersionConflict", err)
	}
	if got := documentSessionReadFile(t, target); got != original {
		t.Fatalf("root index changed on selector conflict: %q", got)
	}
	documentSessionAssertNoArtifacts(t, root)
}

func TestDocumentSessionPrecommitPostinstallDependencyChangeRollsBackTarget(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	rootIndex := filepath.Join(root, "index.md")
	target := filepath.Join(root, "nested", "concept.md")
	originalIndex := documentSessionIndex("0.2", "root")
	foreignIndex := documentSessionIndex("0.2", "foreign root")
	originalTarget := documentSessionConcept("concept", "before")
	documentSessionWriteFile(t, rootIndex, originalIndex)
	documentSessionWriteFile(t, target, originalTarget)
	var changed atomic.Bool
	session, err := openDocumentSessionContext(
		context.Background(),
		target,
		"",
		documentSessionHooks{
			publish: documentPublishHooks{
				afterInstall: func(*os.Root, string) error {
					if changed.CompareAndSwap(false, true) {
						documentSessionAtomicReplace(t, rootIndex, foreignIndex, 0o644)
					}
					return nil
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("openDocumentSessionContext() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	// Act.
	err = session.RewriteContext(
		context.Background(),
		documentSessionParsed(t, documentSessionConcept("concept", "after")),
	)

	// Assert.
	// afterInstall is still before the durable full-postimage CAS and therefore
	// remains a rollback boundary, not a committed-publication boundary.
	if !changed.Load() ||
		!errors.Is(err, ErrDocumentConflict) ||
		errors.Is(err, ErrPublicationCommitted) {
		t.Fatalf(
			"changed=%t, RewriteContext() error=%v, want precommit ErrDocumentConflict without ErrPublicationCommitted",
			changed.Load(),
			err,
		)
	}
	if got := documentSessionReadFile(t, target); got != originalTarget {
		t.Fatalf("target after dependency rollback = %q, want original %q", got, originalTarget)
	}
	if got := documentSessionReadFile(t, rootIndex); got != foreignIndex {
		t.Fatalf("foreign dependency = %q, want preserved %q", got, foreignIndex)
	}
	documentSessionAssertNoArtifacts(t, root)
}

func TestDocumentSessionRewriteFaultsRollBackWithoutCancellation(t *testing.T) {
	t.Parallel()

	fault := errors.New("injected publication fault")
	tests := []struct {
		name          string
		hooks         func() documentPublishHooks
		wantCommitted bool
		wantErrors    []string
		wantRole      string
		wantArtifacts []string
		wantResidue   bool
	}{
		{
			name:       "manifest file sync",
			wantErrors: []string{"fault"},
			wantRole:   "original-inode",
			hooks: func() documentPublishHooks {
				var calls atomic.Int32
				return documentPublishHooks{
					fileSync: func(*os.File) error {
						if calls.Add(1) == 1 {
							return fault
						}
						return nil
					},
				}
			},
		},
		{
			name:       "stage file sync",
			wantErrors: []string{"fault"},
			wantRole:   "original-inode",
			hooks: func() documentPublishHooks {
				var calls atomic.Int32
				return documentPublishHooks{
					fileSync: func(*os.File) error {
						if calls.Add(1) == 2 {
							return fault
						}
						return nil
					},
				}
			},
		},
		{
			name:       "first directory sync",
			wantErrors: []string{"fault"},
			wantRole:   "original-inode",
			hooks: func() documentPublishHooks {
				var calls atomic.Int32
				return documentPublishHooks{
					directorySync: func(*os.Root) error {
						if calls.Add(1) == 1 {
							return fault
						}
						return nil
					},
				}
			},
		},
		{
			name:       "install barrier",
			wantErrors: []string{"document-conflict", "fault"},
			wantRole:   "restored-independent",
			hooks: func() documentPublishHooks {
				var fired atomic.Bool
				return documentPublishHooks{
					beforeInstall: func(*os.Root, string, string) error {
						if fired.CompareAndSwap(false, true) {
							return fault
						}
						return nil
					},
				}
			},
		},
		{
			name:       "post install barrier",
			wantErrors: []string{"document-conflict", "fault"},
			wantRole:   "restored-independent",
			hooks: func() documentPublishHooks {
				var fired atomic.Bool
				return documentPublishHooks{
					afterInstall: func(*os.Root, string) error {
						if fired.CompareAndSwap(false, true) {
							return fault
						}
						return nil
					},
				}
			},
		},
		{
			name:          "pre cleanup barrier",
			wantCommitted: true,
			wantErrors:    []string{"publication-committed", "fault"},
			wantRole:      "stage",
			wantArtifacts: []string{"backup", "claim", "manifest", "stage", "witness"},
			wantResidue:   true,
			hooks: func() documentPublishHooks {
				var fired atomic.Bool
				return documentPublishHooks{
					beforeCleanup: func(*os.Root, string, string) error {
						if fired.CompareAndSwap(false, true) {
							return fault
						}
						return nil
					},
				}
			},
		},
		{
			name:          "post cleanup barrier",
			wantCommitted: true,
			wantErrors:    []string{"publication-committed", "fault"},
			wantRole:      "stage",
			wantArtifacts: []string{"backup", "claim", "stage", "witness"},
			wantResidue:   true,
			hooks: func() documentPublishHooks {
				var fired atomic.Bool
				return documentPublishHooks{
					afterCleanup: func(*os.Root, string, string) error {
						if fired.CompareAndSwap(false, true) {
							return fault
						}
						return nil
					},
				}
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			result := runDocumentFaultParity(
				t, test.hooks(), convergeDocumentRollback, fault, test.wantCommitted,
			)
			wantBytes := documentSessionConcept("concept", "before")
			if test.wantCommitted {
				wantBytes = documentSessionConcept("concept", "after")
			}
			if !reflect.DeepEqual(result.errorIdentities, test.wantErrors) ||
				result.committed != test.wantCommitted ||
				result.targetMissing || result.targetBytes != wantBytes ||
				result.targetMode != 0o644 || result.targetIdentityRole != test.wantRole ||
				!reflect.DeepEqual(result.artifactKinds, test.wantArtifacts) ||
				(result.residue != "") != test.wantResidue || !result.targetStable ||
				len(result.errorOrder) == 0 {
				t.Fatalf("canonical result violates independent contract: %+v", result)
			}
		})
	}
}

type documentFaultParity struct {
	errorOrder         []string
	errorIdentities    []string
	committed          bool
	targetMissing      bool
	targetBytes        string
	targetMode         fs.FileMode
	targetIdentityRole string
	artifactKinds      []string
	residue            string
	targetStable       bool
}

type documentFaultIdentity struct {
	name string
	err  error
}

func runDocumentFaultParity(
	t *testing.T,
	hooks documentPublishHooks,
	rollback func(context.Context, *DocumentSession, *documentTransaction, []byte, bool, bool, documentPublishHooks) error,
	fault error,
	wantCommitted bool,
) documentFaultParity {
	t.Helper()
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	original := documentSessionConcept("concept", "before")
	published := documentSessionConcept("concept", "after")
	documentSessionWriteFile(t, target, original)
	originalInfo, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	session, err := openDocumentSessionContext(
		context.Background(), target, "", documentSessionHooks{publish: hooks},
	)
	if err != nil {
		t.Fatal(err)
	}
	replacement := documentSessionParsed(t, published)
	serialized, err := replacement.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	err = mapDocumentPublicError(rewriteCapturedDocumentWithRollback(
		context.Background(), session, []byte(serialized), hooks, rollback,
	))
	if !errors.Is(err, fault) {
		t.Fatalf("rewrite error = %v, want injected fault", err)
	}
	want := original
	if wantCommitted {
		want = published
		if !errors.Is(err, ErrPublicationCommitted) {
			t.Fatalf("rewrite error = %v, want ErrPublicationCommitted", err)
		}
	} else if errors.Is(err, ErrPublicationCommitted) {
		t.Fatalf("rewrite error = %v, want precommit failure", err)
	}
	beforeRecovery, statErr := os.Lstat(target)
	if statErr != nil {
		t.Fatal(statErr)
	}
	result := documentFaultParity{
		errorOrder: documentFaultErrorOrder(err, fault),
		errorIdentities: documentFaultIdentityOrder(err,
			documentFaultIdentity{"fault", fault},
			documentFaultIdentity{"publication-committed", ErrPublicationCommitted},
			documentFaultIdentity{"document-conflict", ErrDocumentConflict},
		),
		committed: errors.Is(err, ErrPublicationCommitted),
	}
	observeDocumentFaultParity(t, &result, root, target, originalInfo, original, published)
	if result.targetBytes != want {
		t.Fatalf("target after failure = %q, want %q", result.targetBytes, want)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	recovered, recoveryErr := OpenDocumentSessionContext(context.Background(), target, "")
	if recoveryErr != nil {
		t.Fatal(recoveryErr)
	}
	if closeErr := recovered.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	afterRecovery, statErr := os.Lstat(target)
	if statErr != nil {
		t.Fatal(statErr)
	}
	result.targetStable = os.SameFile(beforeRecovery, afterRecovery)
	if !result.targetStable || documentSessionReadFile(t, target) != want {
		t.Fatal("recovery changed target identity or bytes")
	}
	documentSessionAssertNoArtifacts(t, root)
	return result
}

func documentFaultIdentityOrder(err error, identities ...documentFaultIdentity) []string {
	if err == nil {
		return nil
	}
	var observed []string
	for _, identity := range identities {
		if err == identity.err {
			observed = append(observed, identity.name)
		}
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			observed = append(observed, documentFaultIdentityOrder(child, identities...)...)
		}
		return observed
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok && wrapped.Unwrap() != nil {
		observed = append(observed, documentFaultIdentityOrder(wrapped.Unwrap(), identities...)...)
	}
	return observed
}

func observeDocumentFaultParity(
	t *testing.T,
	result *documentFaultParity,
	root string,
	target string,
	originalInfo os.FileInfo,
	original string,
	published string,
) os.FileInfo {
	t.Helper()
	targetInfo, statErr := os.Lstat(target)
	if errors.Is(statErr, fs.ErrNotExist) {
		result.targetMissing = true
		result.targetIdentityRole = "missing"
		statErr = nil
	}
	if statErr != nil {
		t.Fatal(statErr)
	}
	if targetInfo != nil {
		result.targetBytes = documentSessionReadFile(t, target)
		result.targetMode = targetInfo.Mode().Perm()
	}
	walkErr := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		kind, _, ok := parseDocumentArtifactName(entry.Name())
		if !ok {
			return nil
		}
		result.artifactKinds = append(result.artifactKinds, kind)
		info, infoErr := os.Lstat(name)
		if infoErr != nil {
			return infoErr
		}
		if targetInfo != nil && os.SameFile(targetInfo, info) {
			result.targetIdentityRole = kind
		}
		return nil
	})
	if walkErr != nil {
		t.Fatal(walkErr)
	}
	sort.Strings(result.artifactKinds)
	if targetInfo != nil && result.targetIdentityRole == "" {
		switch {
		case originalInfo != nil && os.SameFile(originalInfo, targetInfo):
			result.targetIdentityRole = "original-inode"
		case result.targetBytes == original:
			result.targetIdentityRole = "restored-independent"
		case result.targetBytes == published:
			result.targetIdentityRole = "published-independent"
		default:
			result.targetIdentityRole = "foreign-independent"
		}
	}
	result.residue = documentSessionPublicationArtifactDebug(t, root)
	return targetInfo
}

func documentFaultErrorOrder(err, fault error) []string {
	if err == nil {
		return nil
	}
	order := []string{documentFaultErrorRole(err, fault)}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			order = append(order, documentFaultErrorOrder(child, fault)...)
		}
		return order
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok && wrapped.Unwrap() != nil {
		return append(order, documentFaultErrorOrder(wrapped.Unwrap(), fault)...)
	}
	return order
}

func documentFaultErrorRole(err, fault error) string {
	if err == fault {
		return "fault"
	}
	if err == ErrPublicationCommitted {
		return "publication-committed"
	}
	if err == ErrDocumentConflict {
		return "document-conflict"
	}
	return fmt.Sprintf("%T:%s", err, err.Error())
}

func TestDocumentFaultErrorOrderTraversesSingleAndMultiUnwrapPreorder(t *testing.T) {
	// Arrange.
	fault := errors.New("fault")
	err := fmt.Errorf("outer: %w", errors.Join(fault, ErrDocumentConflict))

	// Act.
	got := documentFaultErrorOrder(err, fault)

	// Assert.
	want := []string{
		"*fmt.wrapError:outer: fault\ndocument rewrite conflict",
		"*errors.joinError:fault\ndocument rewrite conflict",
		"fault",
		"document-conflict",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("preorder = %q, want %q", got, want)
	}
}

func TestDocumentPublicationPrimitivesCancelImmediatelyBeforeNamespaceMutation(t *testing.T) {
	tests := []struct {
		name string
		act  func(context.Context, *os.Root, publicationBarrierHooks) error
	}{
		{
			name: "vacate",
			act: func(ctx context.Context, parent *os.Root, hooks publicationBarrierHooks) error {
				expected, err := capturePublicationFile(ctx, parent, "leaf.md")
				if err != nil {
					return err
				}
				_, err = guardedVacatePublicationLeaf(ctx, parent, "leaf.md", expected, "claim.md", hooks)
				return err
			},
		},
		{
			name: "install",
			act: func(ctx context.Context, parent *os.Root, hooks publicationBarrierHooks) error {
				stage, err := capturePublicationFile(ctx, parent, "stage.md")
				if err != nil {
					return err
				}
				_, err = guardedInstallPublicationLeaf(ctx, parent, "stage.md", stage, "leaf.md", hooks)
				return err
			},
		},
		{
			name: "consume",
			act: func(ctx context.Context, parent *os.Root, hooks publicationBarrierHooks) error {
				install, err := capturePublicationFile(ctx, parent, "install.md")
				if err != nil {
					return err
				}
				witness, err := capturePublicationFile(ctx, parent, "witness.md")
				if err != nil {
					return err
				}
				_, err = guardedConsumePublicationInstallLeaf(
					ctx, parent, "install.md", install, "witness.md", witness, "leaf.md", hooks,
				)
				return err
			},
		},
		{
			name: "remove",
			act: func(ctx context.Context, parent *os.Root, hooks publicationBarrierHooks) error {
				expected, err := capturePublicationFile(ctx, parent, "leaf.md")
				if err != nil {
					return err
				}
				_, err = guardedRemovePublicationFile(ctx, parent, "leaf.md", expected, hooks)
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			payload := documentSessionConcept("concept", "before")
			leafName := "leaf.md"
			if test.name == "install" {
				leafName = "stage.md"
			}
			if test.name == "consume" {
				leafName = "install.md"
			}
			documentSessionWriteFile(t, filepath.Join(root, leafName), payload)
			var (
				witnessBefore os.FileInfo
				err           error
			)
			if test.name == "consume" {
				if err := os.Link(filepath.Join(root, "install.md"), filepath.Join(root, "witness.md")); err != nil {
					t.Fatal(err)
				}
				witnessBefore, err = os.Lstat(filepath.Join(root, "witness.md"))
				if err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.Lstat(filepath.Join(root, leafName))
			if err != nil {
				t.Fatal(err)
			}
			parent, err := os.OpenRoot(root)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = parent.Close() })
			ctx, cancel := context.WithCancel(context.Background())
			mutationChecks := 0
			hooks := publicationBarrierHooks{beforeMutation: func() {
				mutationChecks++
				cancel()
			}}

			// Act.
			err = test.act(ctx, parent, hooks)

			// Assert.
			if !errors.Is(err, context.Canceled) || mutationChecks != 1 {
				t.Fatalf("error=%v mutation checks=%d", err, mutationChecks)
			}
			after, statErr := os.Lstat(filepath.Join(root, leafName))
			if statErr != nil || !os.SameFile(before, after) || after.Mode() != before.Mode() {
				t.Fatalf("source identity/mode changed: before=%v after=%v error=%v", before, after, statErr)
			}
			if got := documentSessionReadFile(t, filepath.Join(root, leafName)); got != payload {
				t.Fatalf("source bytes changed: got %q", got)
			}
			if witnessBefore != nil {
				witnessAfter, witnessErr := os.Lstat(filepath.Join(root, "witness.md"))
				if witnessErr != nil || !os.SameFile(witnessBefore, witnessAfter) ||
					witnessAfter.Mode() != witnessBefore.Mode() ||
					documentSessionReadFile(t, filepath.Join(root, "witness.md")) != payload {
					t.Fatalf("witness changed: before=%v after=%v error=%v", witnessBefore, witnessAfter, witnessErr)
				}
			}
			if test.name != "remove" {
				if _, statErr := os.Lstat(filepath.Join(root, "leaf.md")); leafName != "leaf.md" && !errors.Is(statErr, fs.ErrNotExist) {
					t.Fatalf("destination mutated: %v", statErr)
				}
			}
			if test.name == "vacate" {
				if _, statErr := os.Lstat(filepath.Join(root, "claim.md")); !errors.Is(statErr, fs.ErrNotExist) {
					t.Fatalf("claim mutated: %v", statErr)
				}
			}
			if residue := documentSessionPublicationArtifactDebug(t, root); residue != "" {
				t.Fatalf("unexpected residue: %s", residue)
			}
		})
	}
}

func TestDocumentSessionBackupIsFileSyncedBeforeDurableLinkEvidence(t *testing.T) {
	t.Parallel()

	t.Run("file sync fault cannot authorize publication", func(t *testing.T) {
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
		fault := errors.New("injected backup file sync failure")
		var backupPhase atomic.Bool
		var syncFaulted atomic.Bool
		session, err := openDocumentSessionContext(
			context.Background(),
			target,
			"",
			documentSessionHooks{
				publish: documentPublishHooks{
					afterWitnessSync: func(*os.Root, string) error {
						backupPhase.Store(true)
						return nil
					},
					fileSync: func(file *os.File) error {
						if backupPhase.Load() &&
							syncFaulted.CompareAndSwap(false, true) {
							backupPhase.Store(false)
							return fault
						}
						return file.Sync()
					},
					directorySync: func(parent *os.Root) error {
						return syncPublicationDirectoryPlatform(parent)
					},
				},
			},
		)
		if err != nil {
			t.Fatalf("openDocumentSessionContext() error = %v", err)
		}
		t.Cleanup(func() { _ = session.Close() })

		// Act.
		err = session.RewriteContext(
			context.Background(),
			documentSessionParsed(t, documentSessionConcept("concept", "after")),
		)

		// Assert.
		if !syncFaulted.Load() || !errors.Is(err, fault) {
			t.Fatalf("backup sync faulted=%t, error=%v; want injected fault", syncFaulted.Load(), err)
		}
		after, statErr := os.Lstat(target)
		if statErr != nil || !os.SameFile(before, after) ||
			publicationPreservedMode(after.Mode()) != 0o640 {
			t.Fatalf("target identity/mode changed: before=%v after=%v error=%v", before, after, statErr)
		}
		if got := documentSessionReadFile(t, target); got != original {
			t.Fatalf("target bytes = %q, want %q", got, original)
		}
		documentSessionAssertNoArtifacts(t, root)
	})

	t.Run("file sync precedes backup namespace sync", func(t *testing.T) {
		t.Parallel()

		// Arrange.
		root := t.TempDir()
		target := filepath.Join(root, "concept.md")
		documentSessionWriteFile(t, target, documentSessionConcept("concept", "before"))
		var backupPhase atomic.Bool
		var trace []string
		session, err := openDocumentSessionContext(
			context.Background(),
			target,
			"",
			documentSessionHooks{
				publish: documentPublishHooks{
					afterWitnessSync: func(*os.Root, string) error {
						backupPhase.Store(true)
						return nil
					},
					fileSync: func(file *os.File) error {
						if backupPhase.Load() {
							trace = append(trace, "file-sync:"+filepath.Base(file.Name()))
						}
						return file.Sync()
					},
					directorySync: func(parent *os.Root) error {
						if backupPhase.Load() {
							trace = append(trace, "directory-sync")
						}
						return syncPublicationDirectoryPlatform(parent)
					},
					afterBackupSync: func(*os.Root, string) error {
						trace = append(trace, "after-backup-sync")
						backupPhase.Store(false)
						return nil
					},
				},
			},
		)
		if err != nil {
			t.Fatalf("openDocumentSessionContext() error = %v", err)
		}
		t.Cleanup(func() { _ = session.Close() })

		// Act.
		err = session.RewriteContext(
			context.Background(),
			documentSessionParsed(t, documentSessionConcept("concept", "after")),
		)

		// Assert.
		if err != nil {
			t.Fatalf("RewriteContext() error = %v", err)
		}
		if len(trace) != 3 ||
			!strings.HasPrefix(trace[0], "file-sync:") ||
			trace[1] != "directory-sync" ||
			trace[2] != "after-backup-sync" {
			t.Fatalf("backup durability trace = %#v, want file sync -> directory sync -> after hook", trace)
		}
		documentSessionAssertNoArtifacts(t, root)
	})
}

func TestDocumentSessionVacateDirectorySyncFaultRestoresDurableOriginal(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	original := documentSessionConcept("concept", "before")
	documentSessionWriteFile(t, target, original)
	if err := os.Chmod(target, 0o640); err != nil {
		t.Fatal(err)
	}
	originalInfo, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	fault := errors.New("injected vacate directory sync failure")
	var vacatePhase atomic.Bool
	var faulted atomic.Bool
	session, err := openDocumentSessionContext(
		context.Background(),
		target,
		"",
		documentSessionHooks{
			publish: documentPublishHooks{
				afterStageSync: func(*os.Root, string) error {
					vacatePhase.Store(true)
					return nil
				},
				directorySync: func(parent *os.Root) error {
					if vacatePhase.Load() && faulted.CompareAndSwap(false, true) {
						vacatePhase.Store(false)
						return fault
					}
					return syncPublicationDirectoryPlatform(parent)
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("openDocumentSessionContext() error = %v", err)
	}

	// Act.
	rewriteErr := session.RewriteContext(
		context.Background(),
		documentSessionParsed(t, documentSessionConcept("concept", "after")),
	)

	// Assert.
	if !faulted.Load() || !errors.Is(rewriteErr, fault) {
		t.Fatalf("faulted=%t, RewriteContext() error=%v; want vacate sync fault", faulted.Load(), rewriteErr)
	}
	currentInfo, statErr := os.Lstat(target)
	if statErr != nil || !os.SameFile(originalInfo, currentInfo) ||
		publicationPreservedMode(currentInfo.Mode()) != 0o640 {
		t.Fatalf("original target was not restored exactly: before=%v after=%v error=%v", originalInfo, currentInfo, statErr)
	}
	if got := documentSessionReadFile(t, target); got != original {
		t.Fatalf("restored target bytes = %q, want %q", got, original)
	}
	if closeErr := session.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	reopened, reopenErr := OpenDocumentSessionContext(context.Background(), target, "")
	if reopenErr != nil {
		t.Fatalf(
			"next OpenDocumentSessionContext() error = %v; residuals: %s",
			reopenErr,
			documentSessionPublicationArtifactDebug(t, root),
		)
	}
	if closeErr := reopened.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	documentSessionAssertNoArtifacts(t, root)
}

func TestDocumentSessionVacatePostRenameFaultConvergesCFirstAndCleansEvidence(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	original := documentSessionConcept("concept", "before")
	documentSessionWriteFile(t, target, original)
	if err := os.Chmod(target, 0o640); err != nil {
		t.Fatal(err)
	}
	originalInfo, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	fault := errors.New("injected post-rename claim capture boundary failure")
	var faulted atomic.Bool
	session, err := openDocumentSessionContext(
		context.Background(),
		target,
		"",
		documentSessionHooks{
			publish: documentPublishHooks{
				afterVacateRename: func(*os.Root, string, string) error {
					if faulted.CompareAndSwap(false, true) {
						return fault
					}
					return nil
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("openDocumentSessionContext() error = %v", err)
	}

	// Act.
	rewriteErr := session.RewriteContext(
		context.Background(),
		documentSessionParsed(t, documentSessionConcept("concept", "after")),
	)

	// Assert.
	if !faulted.Load() ||
		!errors.Is(rewriteErr, fault) ||
		!errors.Is(rewriteErr, ErrDocumentConflict) {
		t.Fatalf(
			"faulted=%t, RewriteContext() error=%v; want post-rename typed conflict; residuals: %s",
			faulted.Load(),
			rewriteErr,
			documentSessionPublicationArtifactDebug(t, root),
		)
	}
	currentInfo, statErr := os.Lstat(target)
	if statErr != nil || os.SameFile(originalInfo, currentInfo) ||
		publicationPreservedMode(currentInfo.Mode()) != 0o640 {
		t.Fatalf(
			"C-first active convergence did not restore an independent exact target: before=%v after=%v error=%v residuals: %s",
			originalInfo,
			currentInfo,
			statErr,
			documentSessionPublicationArtifactDebug(t, root),
		)
	}
	if got := documentSessionReadFile(t, target); got != original {
		t.Fatalf(
			"restored target bytes = %q, want %q; residuals: %s",
			got,
			original,
			documentSessionPublicationArtifactDebug(t, root),
		)
	}
	documentSessionAssertNoArtifacts(t, root)
	if closeErr := session.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	reopened, reopenErr := OpenDocumentSessionContext(context.Background(), target, "")
	if reopenErr != nil {
		t.Fatalf(
			"next OpenDocumentSessionContext() error = %v; residuals: %s",
			reopenErr,
			documentSessionPublicationArtifactDebug(t, root),
		)
	}
	if closeErr := reopened.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	documentSessionAssertNoArtifacts(t, root)
}

func TestDocumentSessionBeforeInstallClaimLossConvergesFromBackupAndRetainsDrift(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	original := documentSessionConcept("concept", "before")
	documentSessionWriteFile(t, target, original)
	if err := os.Chmod(target, 0o640); err != nil {
		t.Fatal(err)
	}
	originalInfo, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	fault := errors.New("injected before-install claim loss")
	claimRemoved := false
	session, err := openDocumentSessionContext(
		context.Background(),
		target,
		"",
		documentSessionHooks{
			publish: documentPublishHooks{
				beforeInstall: func(parent *os.Root, stage, _ string) error {
					if claimRemoved {
						return nil
					}
					claim := filepath.Base(documentSessionSingleRecoveryArtifact(t, parent.Name(), "claim"))
					if claim == "" {
						t.Fatalf("cannot find canonical claim beside stage %q", stage)
					}
					if err := parent.Remove(claim); err != nil {
						t.Fatalf("Remove(claim) error = %v", err)
					}
					claimRemoved = true
					return fault
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("openDocumentSessionContext() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	// Act.
	rewriteErr := session.RewriteContext(
		context.Background(),
		documentSessionParsed(t, documentSessionConcept("concept", "after")),
	)

	// Assert.
	if !claimRemoved ||
		!errors.Is(rewriteErr, fault) ||
		!errors.Is(rewriteErr, ErrDocumentConflict) {
		t.Fatalf(
			"claimRemoved=%t, RewriteContext() error=%v, want fault plus typed conflict; residuals: %s",
			claimRemoved,
			rewriteErr,
			documentSessionPublicationArtifactDebug(t, root),
		)
	}
	anchorPath := documentSessionSingleRecoveryArtifact(
		t,
		root,
		documentRollbackAnchorProtocolKind(0),
	)
	anchorInfo, anchorErr := os.Lstat(anchorPath)
	currentInfo, statErr := os.Lstat(target)
	if statErr != nil || anchorErr != nil ||
		!os.SameFile(anchorInfo, currentInfo) ||
		os.SameFile(originalInfo, currentInfo) ||
		publicationPreservedMode(currentInfo.Mode()) != 0o640 {
		t.Fatalf(
			"B-selected A/R did not consume to the exact original target: original=%v target=%v A=%v targetErr=%v AErr=%v residuals: %s",
			originalInfo,
			currentInfo,
			anchorInfo,
			statErr,
			anchorErr,
			documentSessionPublicationArtifactDebug(t, root),
		)
	}
	if got := documentSessionReadFile(t, target); got != original {
		t.Fatalf(
			"restored target bytes = %q, want %q; residuals: %s",
			got,
			original,
			documentSessionPublicationArtifactDebug(t, root),
		)
	}
	if paths := documentSessionRecoveryArtifactPaths(t, root, "claim"); len(paths) != 0 {
		t.Fatalf(
			"removed claim was recreated: %v; residuals: %s",
			paths,
			documentSessionPublicationArtifactDebug(t, root),
		)
	}
	witnessPath := documentSessionSingleRecoveryArtifact(t, root, "witness")
	witnessInfo, witnessErr := os.Lstat(witnessPath)
	if witnessErr != nil ||
		!os.SameFile(originalInfo, witnessInfo) ||
		documentSessionReadFile(t, witnessPath) != original {
		t.Fatalf(
			"exact W was not retained after C drift: original=%v W=%v error=%v residuals: %s",
			originalInfo,
			witnessInfo,
			witnessErr,
			documentSessionPublicationArtifactDebug(t, root),
		)
	}
	backupPath := documentSessionSingleRecoveryArtifact(t, root, "backup")
	backupInfo, backupErr := os.Lstat(backupPath)
	if backupErr != nil ||
		os.SameFile(originalInfo, backupInfo) ||
		os.SameFile(anchorInfo, backupInfo) ||
		documentSessionReadFile(t, backupPath) != original {
		t.Fatalf(
			"independent exact B was not retained: original=%v A=%v B=%v error=%v residuals: %s",
			originalInfo,
			anchorInfo,
			backupInfo,
			backupErr,
			documentSessionPublicationArtifactDebug(t, root),
		)
	}
	if paths := documentSessionRecoveryArtifactPaths(t, root, "restore-install"); len(paths) != 0 {
		t.Fatalf(
			"consumed R remains: %v; residuals: %s",
			paths,
			documentSessionPublicationArtifactDebug(t, root),
		)
	}
	if !documentSessionHasArtifacts(t, root) {
		t.Fatal("claim drift incorrectly triggered terminal cleanup")
	}
}

func TestDocumentSessionRollbackRevalidatesSourcesBeforeVacatingPublishedLeaf(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	original := documentSessionConcept("concept", "before")
	published := documentSessionConcept("concept", "after")
	documentSessionWriteFile(t, target, original)
	replacement := documentSessionParsed(t, documentSessionConcept("concept", "after"))
	fault := errors.New("injected post-install rollback")
	var installedInfo os.FileInfo
	var invalidated atomic.Bool
	var anchorPath, restorePath string
	session, err := openDocumentSessionContext(
		context.Background(),
		target,
		"",
		documentSessionHooks{
			publish: documentPublishHooks{
				afterInstall: func(*os.Root, string) error {
					if installedInfo != nil {
						return nil
					}
					var statErr error
					installedInfo, statErr = os.Lstat(target)
					if statErr != nil {
						return statErr
					}
					return fault
				},
				afterRestoreSync: func(parent *os.Root, restore string) error {
					if !invalidated.CompareAndSwap(false, true) {
						return nil
					}
					restorePath = filepath.Join(parent.Name(), restore)
					anchorPath = documentSessionSingleRecoveryArtifact(
						t,
						parent.Name(),
						documentRollbackAnchorProtocolKind(0),
					)
					documentSessionAtomicReplace(t, anchorPath, "foreign selected rollback source", 0o600)
					return nil
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("openDocumentSessionContext() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	// Act.
	rewriteErr := session.RewriteContext(context.Background(), replacement)

	// Assert.
	if installedInfo == nil ||
		!invalidated.Load() ||
		!errors.Is(rewriteErr, fault) ||
		!errors.Is(rewriteErr, ErrDocumentConflict) {
		t.Fatalf(
			"installed=%v, invalidated=%t, error=%v, want pre-D typed conflict; residuals: %s",
			installedInfo,
			invalidated.Load(),
			rewriteErr,
			documentSessionPublicationArtifactDebug(t, root),
		)
	}
	current, statErr := os.Lstat(target)
	if statErr != nil || !os.SameFile(installedInfo, current) {
		t.Fatalf(
			"published New moved before selected-source validation: installed=%v target=%v error=%v residuals: %s",
			installedInfo,
			current,
			statErr,
			documentSessionPublicationArtifactDebug(t, root),
		)
	}
	if got := documentSessionReadFile(t, target); got != published {
		t.Fatalf(
			"published target bytes = %q, want %q; residuals: %s",
			got,
			published,
			documentSessionPublicationArtifactDebug(t, root),
		)
	}
	if got := documentSessionReadFile(t, anchorPath); got != "foreign selected rollback source" {
		t.Fatalf(
			"foreign selected source = %q; residuals: %s",
			got,
			documentSessionPublicationArtifactDebug(t, root),
		)
	}
	restoreInfo, restoreErr := os.Lstat(restorePath)
	if restoreErr != nil || documentSessionReadFile(t, restorePath) != original {
		t.Fatalf(
			"durable R was consumed or changed before D receipt: R=%v error=%v residuals: %s",
			restoreInfo,
			restoreErr,
			documentSessionPublicationArtifactDebug(t, root),
		)
	}
	if paths := documentSessionRecoveryArtifactPaths(t, root, "discard"); len(paths) != 0 {
		t.Fatalf(
			"D was created despite failed source revalidation: %v; residuals: %s",
			paths,
			documentSessionPublicationArtifactDebug(t, root),
		)
	}
}

func TestDocumentSessionLateCleanupFaultMatrixNeverLeavesTargetMissing(t *testing.T) {
	t.Parallel()

	fault := errors.New("injected late cleanup fault")
	for _, kind := range []string{"stage", "claim", "backup", "manifest"} {
		kind := kind
		for _, boundary := range []string{"directory-sync", "after-cleanup", "after-cleanup-cancellation"} {
			boundary := boundary
			t.Run(kind+"/"+boundary, func(t *testing.T) {
				t.Parallel()

				// Arrange.
				root := t.TempDir()
				target := filepath.Join(root, "concept.md")
				original := documentSessionConcept("concept", "before")
				documentSessionWriteFile(t, target, original)
				if err := os.Chmod(target, 0o640); err != nil {
					t.Fatal(err)
				}
				originalInfo, err := os.Lstat(target)
				if err != nil {
					t.Fatal(err)
				}
				replacement := documentSessionParsed(t, documentSessionConcept("concept", "after"))
				published, err := replacement.Serialize()
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				activeCleanup := ""
				injected := false
				injectedErr := fault
				if boundary == "after-cleanup-cancellation" {
					injectedErr = context.Canceled
				}
				hooks := documentPublishHooks{
					beforeCleanup: func(_ *os.Root, currentKind, _ string) error {
						if currentKind == kind && !injected {
							activeCleanup = currentKind
						}
						return nil
					},
					directorySync: func(parent *os.Root) error {
						if boundary == "directory-sync" && activeCleanup == kind && !injected {
							injected = true
							activeCleanup = ""
							return fault
						}
						return syncPublicationDirectoryPlatform(parent)
					},
					afterCleanup: func(_ *os.Root, currentKind, _ string) error {
						activeCleanup = ""
						if boundary == "directory-sync" || currentKind != kind || injected {
							return nil
						}
						injected = true
						if boundary == "after-cleanup-cancellation" {
							cancel()
							return ctx.Err()
						}
						return fault
					},
				}
				session, err := openDocumentSessionContext(
					context.Background(),
					target,
					"",
					documentSessionHooks{publish: hooks},
				)
				if err != nil {
					t.Fatalf("openDocumentSessionContext() error = %v", err)
				}

				// Act.
				rewriteErr := session.RewriteContext(ctx, replacement)

				// Assert.
				if !injected || !errors.Is(rewriteErr, injectedErr) {
					t.Fatalf("injected=%t, RewriteContext() error=%v; want %v", injected, rewriteErr, injectedErr)
				}
				if !errors.Is(rewriteErr, ErrPublicationCommitted) {
					t.Fatalf("RewriteContext() error=%v, want explicit ErrPublicationCommitted outcome", rewriteErr)
				}
				info, statErr := os.Lstat(target)
				if statErr != nil || !info.Mode().IsRegular() {
					t.Fatalf("late cleanup left target unavailable: info=%v error=%v", info, statErr)
				}
				current := documentSessionReadFile(t, target)
				switch current {
				case original:
					if !os.SameFile(originalInfo, info) ||
						publicationPreservedMode(info.Mode()) != 0o640 {
						t.Fatalf("restored original identity/mode changed: before=%v after=%v", originalInfo, info)
					}
				case string(published):
					if publicationPreservedMode(info.Mode()) != 0o640 {
						t.Fatalf("durable published mode = %v, want 0640", info.Mode())
					}
				default:
					t.Fatalf("target bytes = %q, want exact pre- or post-state", current)
				}
				if closeErr := session.Close(); closeErr != nil {
					t.Fatalf("Close() error = %v", closeErr)
				}

				reopened, reopenErr := OpenDocumentSessionContext(context.Background(), target, "")
				if reopenErr != nil {
					t.Fatalf("next OpenDocumentSessionContext() did not converge: %v", reopenErr)
				}
				if closeErr := reopened.Close(); closeErr != nil {
					t.Fatalf("reopened Close() error = %v", closeErr)
				}
				afterRecovery := documentSessionReadFile(t, target)
				if afterRecovery != original && afterRecovery != string(published) {
					t.Fatalf("target after recovery = %q, want exact pre- or post-state", afterRecovery)
				}
				documentSessionAssertNoArtifacts(t, root)
			})
		}
	}
}

func TestDocumentSessionRollbackIgnoresCanceledCallerContext(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	original := documentSessionConcept("concept", "before")
	documentSessionWriteFile(t, target, original)
	ctx, cancel := context.WithCancel(context.Background())
	var fired atomic.Bool
	session, err := openDocumentSessionContext(
		context.Background(),
		target,
		"",
		documentSessionHooks{
			publish: documentPublishHooks{
				afterInstall: func(*os.Root, string) error {
					if !fired.CompareAndSwap(false, true) {
						return nil
					}
					cancel()
					return ctx.Err()
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("openDocumentSessionContext() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	// Act.
	err = session.RewriteContext(
		ctx,
		documentSessionParsed(t, documentSessionConcept("concept", "after")),
	)

	// Assert.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RewriteContext() error = %v, want context.Canceled", err)
	}
	if got := documentSessionReadFile(t, target); got != original {
		t.Fatalf("target after noncancelable rollback = %q, want %q", got, original)
	}
	documentSessionAssertNoArtifacts(t, root)
}

func TestDocumentSessionFinalValidationRejectsPublishedTampering(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	original := documentSessionConcept("concept", "before")
	foreign := documentSessionConcept("foreign", "do not overwrite")
	documentSessionWriteFile(t, target, original)
	var tampered atomic.Bool
	session, err := openDocumentSessionContext(
		context.Background(),
		target,
		"",
		documentSessionHooks{
			publish: documentPublishHooks{
				afterCleanup: func(_ *os.Root, kind, _ string) error {
					if kind == "stage" && tampered.CompareAndSwap(false, true) {
						documentSessionAtomicReplace(t, target, foreign, 0o644)
					}
					return nil
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("openDocumentSessionContext() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	// Act.
	err = session.RewriteContext(
		context.Background(),
		documentSessionParsed(t, documentSessionConcept("concept", "after")),
	)

	// Assert.
	if !errors.Is(err, ErrDocumentConflict) {
		t.Fatalf("RewriteContext() error = %v, want ErrDocumentConflict", err)
	}
	if got := documentSessionReadFile(t, target); got != foreign {
		t.Fatalf("foreign final-validation replacement = %q, want preserved %q", got, foreign)
	}
	if documentSessionHasArtifacts(t, root) {
		t.Fatal("fully committed cleanup retained transaction artifacts after final target tampering")
	}
}

func TestDocumentSessionCleanupNeverUnlinksForeignArtifactReplacement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		kind string
		swap func(t *testing.T, filename string)
	}{
		{
			name: "stage regular replacement",
			kind: "stage",
			swap: func(t *testing.T, filename string) {
				t.Helper()
				documentSessionAtomicReplace(t, filename, "foreign-stage", 0o600)
			},
		},
		{
			name: "backup regular replacement",
			kind: "backup",
			swap: func(t *testing.T, filename string) {
				t.Helper()
				documentSessionAtomicReplace(t, filename, "foreign-backup", 0o600)
			},
		},
		{
			name: "manifest regular replacement",
			kind: "manifest",
			swap: func(t *testing.T, filename string) {
				t.Helper()
				documentSessionAtomicReplace(t, filename, "foreign-manifest", 0o600)
			},
		},
		{
			name: "stage symlink replacement",
			kind: "stage",
			swap: func(t *testing.T, filename string) {
				t.Helper()
				foreignTarget := filepath.Join(filepath.Dir(filename), "foreign-symlink-target")
				documentSessionWriteFile(t, foreignTarget, "foreign-symlink-target")
				if err := os.Remove(filename); err != nil {
					t.Fatalf("Remove(artifact) error = %v", err)
				}
				if err := os.Symlink(foreignTarget, filename); err != nil {
					t.Fatalf("Symlink(artifact) error = %v", err)
				}
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
			documentSessionWriteFile(t, target, documentSessionConcept("concept", "before"))
			var swapped atomic.Bool
			var foreignArtifact string
			session, err := openDocumentSessionContext(
				context.Background(),
				target,
				"",
				documentSessionHooks{
					publish: documentPublishHooks{
						beforeCleanup: func(_ *os.Root, kind, name string) error {
							if kind == test.kind && swapped.CompareAndSwap(false, true) {
								foreignArtifact = filepath.Join(root, name)
								test.swap(t, foreignArtifact)
							}
							return nil
						},
					},
				},
			)
			if err != nil {
				t.Fatalf("openDocumentSessionContext() error = %v", err)
			}
			t.Cleanup(func() { _ = session.Close() })

			// Act.
			err = session.RewriteContext(
				context.Background(),
				documentSessionParsed(t, documentSessionConcept("concept", "after")),
			)

			// Assert.
			if err == nil {
				t.Fatal("RewriteContext() succeeded after transaction artifact replacement")
			}
			if !swapped.Load() {
				t.Fatalf("%s cleanup hook was not reached", test.kind)
			}
			info, statErr := os.Lstat(foreignArtifact)
			if statErr != nil {
				t.Fatalf("foreign artifact was unlinked: %v", statErr)
			}
			if strings.Contains(test.name, "symlink") {
				if info.Mode()&os.ModeSymlink == 0 {
					t.Fatalf("foreign artifact mode = %v, want symlink", info.Mode())
				}
			} else {
				got := documentSessionReadFile(t, foreignArtifact)
				if !strings.HasPrefix(got, "foreign-") {
					t.Fatalf("foreign artifact bytes = %q, want preserved replacement", got)
				}
			}
		})
	}
}

func TestDocumentSessionInstallNeverOverwritesForeignLeaf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		swap func(t *testing.T, target string)
	}{
		{
			name: "regular file",
			swap: func(t *testing.T, target string) {
				t.Helper()
				documentSessionWriteFile(t, target, "foreign-regular")
			},
		},
		{
			name: "symlink",
			swap: func(t *testing.T, target string) {
				t.Helper()
				foreign := filepath.Join(filepath.Dir(target), "foreign-target")
				documentSessionWriteFile(t, foreign, "foreign-symlink-target")
				if err := os.Symlink(foreign, target); err != nil {
					t.Fatalf("Symlink(foreign leaf) error = %v", err)
				}
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
			documentSessionWriteFile(t, target, documentSessionConcept("concept", "before"))
			var swapped atomic.Bool
			session, err := openDocumentSessionContext(
				context.Background(),
				target,
				"",
				documentSessionHooks{
					publish: documentPublishHooks{
						beforeInstall: func(_ *os.Root, _, _ string) error {
							if swapped.CompareAndSwap(false, true) {
								test.swap(t, target)
							}
							return nil
						},
					},
				},
			)
			if err != nil {
				t.Fatalf("openDocumentSessionContext() error = %v", err)
			}
			t.Cleanup(func() { _ = session.Close() })

			// Act.
			err = session.RewriteContext(
				context.Background(),
				documentSessionParsed(t, documentSessionConcept("concept", "after")),
			)

			// Assert.
			if err == nil {
				t.Fatal("RewriteContext() overwrote a foreign install-boundary leaf")
			}
			info, statErr := os.Lstat(target)
			if statErr != nil {
				t.Fatalf("foreign install-boundary leaf disappeared: %v", statErr)
			}
			if test.name == "symlink" {
				if info.Mode()&os.ModeSymlink == 0 {
					t.Fatalf("foreign leaf mode = %v, want symlink", info.Mode())
				}
			} else if got := documentSessionReadFile(t, target); got != "foreign-regular" {
				t.Fatalf("foreign leaf bytes = %q, want preserved", got)
			}
			if !documentSessionHasArtifacts(t, root) {
				t.Fatal("original recovery evidence was lost after foreign install-boundary leaf appeared")
			}
		})
	}
}

func TestGuardedVacateNeverOverwritesForeignClaimDestination(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		inject func(t *testing.T, parent *os.Root, claim string)
		assert func(t *testing.T, filename string)
	}{
		{
			name: "regular",
			inject: func(t *testing.T, parent *os.Root, claim string) {
				t.Helper()
				if err := parent.WriteFile(claim, []byte("foreign claim sentinel"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			assert: func(t *testing.T, filename string) {
				t.Helper()
				if got := documentSessionReadFile(t, filename); got != "foreign claim sentinel" {
					t.Fatalf("foreign claim bytes = %q", got)
				}
			},
		},
		{
			name: "symlink",
			inject: func(t *testing.T, parent *os.Root, claim string) {
				t.Helper()
				external := filepath.Join(t.TempDir(), "external")
				documentSessionWriteFile(t, external, "external sentinel")
				if err := os.Symlink(external, filepath.Join(parent.Name(), claim)); err != nil {
					t.Skipf("symlinks are unavailable: %v", err)
				}
			},
			assert: func(t *testing.T, filename string) {
				t.Helper()
				info, err := os.Lstat(filename)
				if err != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Fatalf("foreign claim = %v, %v; want symlink", info, err)
				}
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			leaf := "concept.md"
			target := filepath.Join(root, leaf)
			original := []byte(documentSessionConcept("concept", "before"))
			if err := os.WriteFile(target, original, 0o640); err != nil {
				t.Fatal(err)
			}
			originalInfo, err := os.Lstat(target)
			if err != nil {
				t.Fatal(err)
			}
			pinned, err := openRootWithoutSymlinks(root)
			if err != nil {
				t.Fatal(err)
			}
			defer pinned.Close()
			claim := documentArtifactName("claim", strings.Repeat("a", 64))
			injected := false

			// Act.
			claimed, err := guardedVacatePublicationLeaf(
				context.Background(),
				pinned,
				leaf,
				publicationSpecFromBytes(originalInfo, original),
				claim,
				publicationBarrierHooks{
					beforeVacate: func() error {
						injected = true
						test.inject(t, pinned, claim)
						return nil
					},
				},
			)

			// Assert.
			if !injected || err == nil || claimed.info != nil {
				t.Fatalf("injected=%t, claimed=%v, error=%v; want no-replace conflict", injected, claimed.info, err)
			}
			current, statErr := os.Lstat(target)
			if statErr != nil || !os.SameFile(originalInfo, current) {
				t.Fatalf("source identity changed: before=%v after=%v error=%v", originalInfo, current, statErr)
			}
			if got := documentSessionReadFile(t, target); got != string(original) {
				t.Fatalf("source bytes = %q, want %q", got, original)
			}
			test.assert(t, filepath.Join(root, claim))
		})
	}
}

func TestIndexRollbackPreservesForeignLeafAtPostInstallBoundary(t *testing.T) {
	t.Parallel()

	injected := errors.New("injected post-install failure")
	tests := []struct {
		name    string
		replace func(t *testing.T, filename string) os.FileInfo
	}{
		{
			name: "same-byte different inode",
			replace: func(t *testing.T, filename string) os.FileInfo {
				t.Helper()
				data, err := os.ReadFile(filename)
				if err != nil {
					t.Fatalf("ReadFile(installed leaf) error = %v", err)
				}
				documentSessionAtomicReplace(t, filename, string(data), 0o640)
				info, err := os.Lstat(filename)
				if err != nil {
					t.Fatalf("Lstat(foreign replacement) error = %v", err)
				}
				return info
			},
		},
		{
			name: "symlink",
			replace: func(t *testing.T, filename string) os.FileInfo {
				t.Helper()
				if err := os.Remove(filename); err != nil {
					t.Fatalf("Remove(installed leaf) error = %v", err)
				}
				foreign := filepath.Join(filepath.Dir(filename), "foreign-index-target")
				documentSessionWriteFile(t, foreign, "foreign-index")
				if err := os.Symlink(foreign, filename); err != nil {
					t.Fatalf("Symlink(foreign index leaf) error = %v", err)
				}
				info, err := os.Lstat(filename)
				if err != nil {
					t.Fatalf("Lstat(foreign symlink) error = %v", err)
				}
				return info
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			documentSessionWriteFile(t, filepath.Join(root, "index.md"), documentSessionIndex("0.2", "root"))
			documentSessionWriteFile(t, filepath.Join(root, "nested", "note.md"), documentSessionConcept("note", "body"))
			destination := filepath.Join(root, "nested", "index.md")
			original := []byte("# Original nested index\n")
			documentSessionWriteFile(t, destination, string(original))
			if err := os.Chmod(destination, 0o640); err != nil {
				t.Fatalf("Chmod(original index) error = %v", err)
			}
			var foreign os.FileInfo
			hooks := indexPublishHooks{
				afterInstallLink: func(relative string, _ *os.Root, _, _ string) error {
					if relative != "nested/index.md" || foreign != nil {
						return nil
					}
					foreign = test.replace(t, destination)
					return injected
				},
			}

			// Act.
			written, err := regenerateIndexesWithTestHooks(root, nil, "", hooks)

			// Assert.
			if !errors.Is(err, injected) || written != nil {
				t.Fatalf("regenerateIndexesWithHooks() = %#v, %v; want injected failure", written, err)
			}
			current, statErr := os.Lstat(destination)
			if statErr != nil {
				t.Fatalf("foreign installed leaf disappeared: %v", statErr)
			}
			if foreign == nil || !os.SameFile(foreign, current) {
				t.Fatal("foreign installed leaf identity was overwritten or removed")
			}
			if backups := indexRecoveryBackups(t, root); len(backups) == 0 {
				t.Fatal("original recovery evidence was not retained")
			}
		})
	}
}

func TestOpenDocumentSessionRecoveryStateMatrix(t *testing.T) {
	t.Parallel()

	type recoveryCase struct {
		name          string
		state         documentV2RecoveryState
		mutate        func(*testing.T, string, documentSessionRecoveryFixture)
		wantOpen      bool
		wantLeaf      string
		wantArtifacts bool
	}
	const (
		originalState  = "original"
		publishedState = "published"
		foreignState   = "foreign"
		missingState   = "missing"
	)
	tests := []recoveryCase{
		{
			name:          "original leaf cleans completed preparation",
			state:         documentV2State(true, false, false, false, false, documentV2TargetWitness),
			wantOpen:      true,
			wantLeaf:      originalState,
			wantArtifacts: false,
		},
		{
			name:          "published leaf converges committed transaction",
			state:         documentV2State(false, false, false, false, true, documentV2TargetStage),
			wantOpen:      true,
			wantLeaf:      publishedState,
			wantArtifacts: false,
		},
		{
			name:          "missing pre-new leaf consumes exact restore token",
			state:         documentV2State(true, true, true, false, true, documentV2TargetMissing),
			wantOpen:      true,
			wantLeaf:      originalState,
			wantArtifacts: false,
		},
		{
			name:          "missing published leaf consumes exact restore token",
			state:         documentV2State(false, true, true, true, true, documentV2TargetMissing),
			wantOpen:      true,
			wantLeaf:      originalState,
			wantArtifacts: false,
		},
		{
			name:  "foreign leaf fails closed and retains evidence",
			state: documentV2State(true, false, false, false, false, documentV2TargetWitness),
			mutate: func(t *testing.T, target string, _ documentSessionRecoveryFixture) {
				documentSessionAtomicReplace(t, target, documentSessionConcept("foreign", "preserve"), 0o644)
			},
			wantOpen:      false,
			wantLeaf:      foreignState,
			wantArtifacts: true,
		},
		{
			name:  "missing leaf without original evidence fails closed",
			state: documentV2State(true, true, true, false, true, documentV2TargetMissing),
			mutate: func(t *testing.T, target string, fixture documentSessionRecoveryFixture) {
				if err := os.Remove(filepath.Join(filepath.Dir(target), fixture.names["witness"])); err != nil {
					t.Fatal(err)
				}
			},
			wantOpen:      false,
			wantLeaf:      missingState,
			wantArtifacts: true,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			target := filepath.Join(root, "concept.md")
			original := documentSessionConcept("concept", "before")
			published := documentSessionConcept("concept", "after")
			foreign := documentSessionConcept("foreign", "preserve")
			documentSessionWriteFile(t, filepath.Join(root, indexFilename), documentSessionIndex("0.2", "Root"))
			fixture := documentSessionCreateV2RecoveryState(
				t,
				root,
				"concept.md",
				0o644,
				original,
				published,
				test.state,
			)
			if test.mutate != nil {
				test.mutate(t, target, fixture)
			}

			// Act.
			session, err := OpenDocumentSessionContext(context.Background(), target, "")
			if session != nil {
				defer session.Close()
			}

			// Assert.
			if test.wantOpen && err != nil {
				t.Fatalf("OpenDocumentSessionContext() error = %v", err)
			}
			if !test.wantOpen && err == nil {
				t.Fatal("OpenDocumentSessionContext() succeeded for unsafe recovery state")
			}
			switch test.wantLeaf {
			case originalState:
				if got := documentSessionReadFile(t, target); got != original {
					t.Fatalf("recovered leaf = %q, want original %q", got, original)
				}
			case publishedState:
				if got := documentSessionReadFile(t, target); got != published {
					t.Fatalf("recovered leaf = %q, want published %q", got, published)
				}
			case foreignState:
				if got := documentSessionReadFile(t, target); got != foreign {
					t.Fatalf("foreign leaf = %q, want preserved %q", got, foreign)
				}
			case missingState:
				if _, statErr := os.Lstat(target); !errors.Is(statErr, fs.ErrNotExist) {
					t.Fatalf("missing leaf Lstat error = %v, want fs.ErrNotExist", statErr)
				}
			}
			if got := documentSessionHasArtifacts(t, root); got != test.wantArtifacts {
				t.Fatalf("recovery artifacts present = %v, want %v", got, test.wantArtifacts)
			}
		})
	}
}

func TestOpenDocumentSessionRecoveryAcceptsIndependentBackupButRejectsDetachedPublishedStage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		leafState string
		wantOpen  bool
	}{
		{name: "original leaf and backup are independent", leafState: "original", wantOpen: true},
		{name: "published leaf does not alias stage", leafState: "published", wantOpen: false},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			target := filepath.Join(root, "concept.md")
			original := documentSessionConcept("concept", "before")
			published := documentSessionConcept("concept", "after")
			leafBytes := original
			state := documentV2State(true, false, false, false, false, documentV2TargetWitness)
			if test.leafState == "published" {
				leafBytes = published
				state = documentV2State(false, false, false, false, true, documentV2TargetStage)
			}
			documentSessionWriteFile(t, filepath.Join(root, indexFilename), documentSessionIndex("0.2", "Root"))
			fixture := documentSessionCreateV2RecoveryState(
				t,
				root,
				"concept.md",
				0o640,
				original,
				published,
				state,
			)
			artifacts := documentSessionCaptureRecoveryArtifacts(t, root, fixture)
			aliasKind := "backup"
			if test.leafState == "published" {
				aliasKind = "stage"
				documentSessionAtomicReplace(t, target, published, 0o640)
			}
			targetInfo, err := os.Lstat(target)
			if err != nil {
				t.Fatal(err)
			}
			aliasInfo := artifacts[aliasKind].info
			if os.SameFile(targetInfo, aliasInfo) {
				t.Fatalf("fixture %s unexpectedly aliases target", aliasKind)
			}

			// Act.
			session, err := OpenDocumentSessionContext(context.Background(), target, "")

			// Assert.
			if test.wantOpen {
				if err != nil || session == nil {
					t.Fatalf("session=%#v, error=%v; want independent backup acceptance", session, err)
				}
				if closeErr := session.Close(); closeErr != nil {
					t.Fatal(closeErr)
				}
				documentSessionAssertNoArtifacts(t, root)
			} else {
				if session != nil {
					_ = session.Close()
				}
				if err == nil || session != nil {
					t.Fatalf("session=%#v, error=%v; want detached stage rejection", session, err)
				}
				documentSessionAssertRecoveryArtifactsUnchanged(t, root, artifacts)
			}
			after, statErr := os.Lstat(target)
			if statErr != nil || !os.SameFile(targetInfo, after) {
				t.Fatalf("target identity changed: before=%v after=%v error=%v", targetInfo, after, statErr)
			}
			if got := documentSessionReadFile(t, target); got != leafBytes {
				t.Fatalf("target bytes = %q, want %q", got, leafBytes)
			}
		})
	}
}

func TestOpenDocumentSessionRecoveryDoesNotReplaceOccupiedTargetBesideForeignClaim(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	original := documentSessionConcept("concept", "before")
	published := documentSessionConcept("concept", "after")
	foreignTarget := documentSessionConcept("foreign-target", "preserve")
	foreignClaim := documentSessionConcept("foreign-claim", "preserve")
	documentSessionWriteFile(t, target, foreignTarget)
	fixture := documentSessionCreateRecoveryGroup(
		t, root, "concept.md", 0o644, original, published,
		"manifest", "backup", "claim", "stage",
	)
	documentSessionAtomicReplace(t, filepath.Join(root, fixture.names["claim"]), foreignClaim, 0o644)
	before := documentSessionCaptureTree(t, root)

	// Act.
	session, err := OpenDocumentSessionContext(context.Background(), target, "")
	if session != nil {
		_ = session.Close()
	}
	after := documentSessionCaptureTree(t, root)

	// Assert.
	if session != nil || !errors.Is(err, ErrDocumentConflict) {
		t.Fatalf("session=%#v, error=%v; want occupied-target conflict", session, err)
	}
	documentSessionAssertTreeSnapshotEqual(t, before, after)
}

func TestOpenDocumentSessionRecoveryRejectsSameBytesDifferentInodeAsCompletedBackupRepair(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	original := documentSessionConcept("concept", "before")
	fixture := documentSessionCreateRecoveryGroup(
		t, root, "concept.md", 0o640, original, documentSessionConcept("concept", "after"),
		"manifest", "backup", "claim", "stage",
	)
	documentSessionAtomicReplace(
		t,
		filepath.Join(root, fixture.names["claim"]),
		documentSessionConcept("foreign-claim", "preserve"),
		0o640,
	)
	documentSessionWriteFile(t, target, original)
	if err := os.Chmod(target, 0o640); err != nil {
		t.Fatal(err)
	}
	targetInfo, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	backupInfo, err := os.Lstat(filepath.Join(root, fixture.names["backup"]))
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(targetInfo, backupInfo) {
		t.Fatal("same-byte target unexpectedly aliases the durable backup")
	}
	before := documentSessionCaptureTree(t, root)

	// Act.
	session, recoveryErr := OpenDocumentSessionContext(context.Background(), target, "")
	if session != nil {
		_ = session.Close()
	}
	after := documentSessionCaptureTree(t, root)

	// Assert.
	if session != nil || !errors.Is(recoveryErr, ErrDocumentConflict) {
		t.Fatalf("session=%#v error=%v, want identity-proof conflict", session, recoveryErr)
	}
	documentSessionAssertTreeSnapshotEqual(t, before, after)
}

func TestDocumentRecoveryPlannerRejectsNonManifestExactClaimBeforeAnyGroupMutation(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	safeTarget := filepath.Join(root, "a.md")
	cleanupTarget := filepath.Join(root, "z.md")
	safeOriginal := documentSessionConcept("a", "before")
	documentSessionWriteFile(t, filepath.Join(root, indexFilename), documentSessionIndex("0.2", "Root"))
	safeFixture := documentSessionCreateV2RecoveryState(
		t, root, "a.md", 0o640, safeOriginal, documentSessionConcept("a", "after"),
		documentV2State(false, true, true, true, true, documentV2TargetMissing),
	)
	safeClaimPath := filepath.Join(root, safeFixture.names["claim"])
	foreignClaim := documentSessionConcept("foreign-claim", "preserve")
	documentSessionAtomicReplace(t, safeClaimPath, foreignClaim, 0o640)
	safeArtifacts := documentSessionCaptureRecoveryArtifacts(t, root, safeFixture)

	cleanupOriginal := documentSessionConcept("z", "before")
	cleanupFixture := documentSessionCreateV2RecoveryState(
		t, root, "z.md", 0o600, cleanupOriginal, documentSessionConcept("z", "after"),
		documentV2State(true, false, false, false, false, documentV2TargetWitness),
	)
	cleanupArtifacts := documentSessionCaptureRecoveryArtifacts(t, root, cleanupFixture)

	// Act.
	session, err := OpenDocumentSessionContext(context.Background(), safeTarget, "")
	if session != nil {
		_ = session.Close()
	}

	// Assert.
	if session != nil || !errors.Is(err, ErrDocumentConflict) {
		t.Fatalf("session=%#v, error=%v; want manifest-exact claim conflict", session, err)
	}
	if _, statErr := os.Lstat(safeTarget); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("unsafe group target error=%v, want unchanged missing target", statErr)
	}
	if got := documentSessionReadFile(t, safeClaimPath); got != foreignClaim {
		t.Fatalf("foreign claim bytes = %q, want %q", got, foreignClaim)
	}
	documentSessionAssertRecoveryArtifactsUnchanged(t, root, safeArtifacts)
	if got := documentSessionReadFile(t, cleanupTarget); got != cleanupOriginal {
		t.Fatalf("cleanup target bytes = %q, want %q", got, cleanupOriginal)
	}
	documentSessionAssertRecoveryArtifactsUnchanged(t, root, cleanupArtifacts)
}

func TestDocumentSessionHeldDescriptorMutationSelectsIndependentBackupEvidence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		phase string
	}{
		{name: "original inode mutates immediately after vacate rename", phase: "after-vacate"},
		{name: "original inode mutates immediately before install", phase: "before-install"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			target := filepath.Join(root, "concept.md")
			original := documentSessionConcept("concept", "before")
			documentSessionWriteFile(t, target, original)
			if err := os.Chmod(target, 0o640); err != nil {
				t.Fatal(err)
			}
			held, err := os.OpenFile(target, os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = held.Close() })
			var claimName string
			mutated := false
			mutateHeld := func() error {
				if mutated {
					return nil
				}
				if _, err := held.WriteAt([]byte("X"), 0); err != nil {
					return err
				}
				if err := held.Sync(); err != nil {
					return err
				}
				mutated = true
				return nil
			}
			session, err := openDocumentSessionContext(
				context.Background(),
				target,
				"",
				documentSessionHooks{publish: documentPublishHooks{
					afterVacateRename: func(_ *os.Root, _, claim string) error {
						claimName = claim
						if test.phase == "after-vacate" {
							return mutateHeld()
						}
						return nil
					},
					beforeInstall: func(*os.Root, string, string) error {
						if test.phase == "before-install" {
							return mutateHeld()
						}
						return nil
					},
				}},
			)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = session.Close() })

			// Act.
			rewriteErr := session.RewriteContext(
				context.Background(),
				documentSessionParsed(t, documentSessionConcept("concept", "after")),
			)

			// Assert.
			if !mutated || !errors.Is(rewriteErr, ErrDocumentConflict) {
				t.Fatalf(
					"mutated=%t error=%v, want held-descriptor conflict; residuals: %s",
					mutated,
					rewriteErr,
					documentSessionPublicationArtifactDebug(t, root),
				)
			}
			anchorPath := documentSessionSingleRecoveryArtifact(
				t,
				root,
				documentRollbackAnchorProtocolKind(0),
			)
			anchorInfo, anchorErr := os.Lstat(anchorPath)
			targetInfo, statErr := os.Lstat(target)
			if statErr != nil ||
				anchorErr != nil ||
				!os.SameFile(targetInfo, anchorInfo) ||
				publicationPreservedMode(targetInfo.Mode()) != 0o640 {
				t.Fatalf(
					"B-selected A/R did not consume to target: target=%v A=%v targetErr=%v AErr=%v residuals: %s",
					targetInfo,
					anchorInfo,
					statErr,
					anchorErr,
					documentSessionPublicationArtifactDebug(t, root),
				)
			}
			if got := documentSessionReadFile(t, target); got != original {
				t.Fatalf(
					"target bytes = %q, want exact B-selected restoration %q; residuals: %s",
					got,
					original,
					documentSessionPublicationArtifactDebug(t, root),
				)
			}
			if claimName == "" {
				t.Fatal("vacated original claim was not observed")
			}
			claimPath := filepath.Join(root, claimName)
			claimInfo, claimErr := os.Lstat(claimPath)
			if claimErr != nil || documentSessionReadFile(t, claimPath) == original {
				t.Fatalf(
					"mutated C was not retained: C=%v error=%v residuals: %s",
					claimInfo,
					claimErr,
					documentSessionPublicationArtifactDebug(t, root),
				)
			}
			witnessPath := documentSessionSingleRecoveryArtifact(t, root, "witness")
			witnessInfo, witnessErr := os.Lstat(witnessPath)
			if witnessErr != nil ||
				!os.SameFile(claimInfo, witnessInfo) ||
				documentSessionReadFile(t, witnessPath) == original {
				t.Fatalf(
					"mutated hardlinked W/C evidence was not retained: C=%v W=%v error=%v residuals: %s",
					claimInfo,
					witnessInfo,
					witnessErr,
					documentSessionPublicationArtifactDebug(t, root),
				)
			}
			backupPath := documentSessionSingleRecoveryArtifact(t, root, "backup")
			backupInfo, backupErr := os.Lstat(backupPath)
			if backupErr != nil ||
				os.SameFile(backupInfo, claimInfo) ||
				os.SameFile(backupInfo, anchorInfo) ||
				documentSessionReadFile(t, backupPath) != original {
				t.Fatalf(
					"independent exact B was not retained: C=%v A=%v B=%v error=%v residuals: %s",
					claimInfo,
					anchorInfo,
					backupInfo,
					backupErr,
					documentSessionPublicationArtifactDebug(t, root),
				)
			}
		})
	}
}

func TestDocumentSessionHeldBackupMutationFailsClosedWithoutFallback(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	original := documentSessionConcept("concept", "before")
	documentSessionWriteFile(t, target, original)
	if err := os.Chmod(target, 0o640); err != nil {
		t.Fatal(err)
	}
	var heldBackup *os.File
	mutated := false
	var mutationBoundary map[string]documentSessionTreeEntry
	session, err := openDocumentSessionContext(
		context.Background(),
		target,
		"",
		documentSessionHooks{publish: documentPublishHooks{
			afterBackupSync: func(parent *os.Root, name string) error {
				var openErr error
				heldBackup, openErr = parent.OpenFile(name, os.O_RDWR, 0)
				return openErr
			},
			beforeInstall: func(*os.Root, string, string) error {
				if _, writeErr := heldBackup.WriteAt([]byte("X"), 0); writeErr != nil {
					return writeErr
				}
				if syncErr := heldBackup.Sync(); syncErr != nil {
					return syncErr
				}
				mutated = true
				mutationBoundary = documentSessionCaptureTree(t, root)
				return nil
			},
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if heldBackup != nil {
			_ = heldBackup.Close()
		}
		_ = session.Close()
	})

	// Act.
	rewriteErr := session.RewriteContext(
		context.Background(),
		documentSessionParsed(t, documentSessionConcept("concept", "after")),
	)

	// Assert.
	if !mutated || !errors.Is(rewriteErr, ErrDocumentConflict) {
		t.Fatalf(
			"mutated=%t error=%v, want hard B conflict; residuals: %s",
			mutated,
			rewriteErr,
			documentSessionPublicationArtifactDebug(t, root),
		)
	}
	if _, statErr := os.Lstat(target); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf(
			"target error=%v, want missing after hard B conflict; residuals: %s",
			statErr,
			documentSessionPublicationArtifactDebug(t, root),
		)
	}
	if mutationBoundary == nil {
		t.Fatal("hard B mutation boundary was not captured")
	}
	afterDetection := documentSessionCaptureTree(t, root)
	documentSessionAssertTreeSnapshotEqual(
		t,
		mutationBoundary,
		afterDetection,
	)
	if closeErr := session.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	beforeRestart := documentSessionCaptureTree(t, root)
	reopened, recoveryErr := OpenDocumentSessionContext(context.Background(), target, "")
	if reopened != nil {
		_ = reopened.Close()
	}
	if reopened != nil || !errors.Is(recoveryErr, ErrDocumentConflict) {
		t.Fatalf(
			"restart session=%#v error=%v, want zero-mutation hard B conflict; residuals: %s",
			reopened,
			recoveryErr,
			documentSessionPublicationArtifactDebug(t, root),
		)
	}
	afterRestart := documentSessionCaptureTree(t, root)
	documentSessionAssertTreeSnapshotEqual(t, beforeRestart, afterRestart)
}

func TestDocumentRollbackHeldEvidenceMutationBeforeDiscardReceiptIsPhaseAware(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{"claim", "backup", "witness", "stage"} {
		kind := kind
		t.Run(kind, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			target := filepath.Join(root, "concept.md")
			original := documentSessionConcept("concept", "before")
			published := documentSessionConcept("concept", "after")
			documentSessionWriteFile(t, target, original)
			if err := os.Chmod(target, 0o640); err != nil {
				t.Fatal(err)
			}
			fault := errors.New("injected post-install rollback")
			var held *os.File
			var anchorInfo os.FileInfo
			var anchorPath string
			var restorePath string
			var discardPath string
			var installedInfo os.FileInfo
			var hardStageBoundary map[string]documentSessionTreeEntry
			rollingBack := false
			mutated := false
			openHeld := func(parent *os.Root, name string) error {
				if held != nil {
					return nil
				}
				var err error
				held, err = parent.OpenFile(name, os.O_RDWR, 0)
				return err
			}
			hooks := documentPublishHooks{
				afterWitnessSync: func(parent *os.Root, name string) error {
					if kind != "witness" {
						return nil
					}
					return openHeld(parent, name)
				},
				afterBackupSync: func(parent *os.Root, name string) error {
					if kind != "backup" {
						return nil
					}
					return openHeld(parent, name)
				},
				afterStageSync: func(parent *os.Root, name string) error {
					if kind != "stage" {
						return nil
					}
					return openHeld(parent, name)
				},
				afterVacateRename: func(parent *os.Root, _, claim string) error {
					if !rollingBack {
						if kind == "claim" {
							return openHeld(parent, claim)
						}
						return nil
					}
					discardPath = filepath.Join(parent.Name(), claim)
					anchorPath = documentSessionSingleRecoveryArtifact(
						t,
						parent.Name(),
						documentRollbackAnchorProtocolKind(0),
					)
					var err error
					anchorInfo, err = os.Lstat(anchorPath)
					if err != nil {
						return err
					}
					if held == nil {
						return errors.New("held evidence descriptor is unavailable")
					}
					if _, err := held.WriteAt([]byte("X"), 0); err != nil {
						return err
					}
					if err := held.Sync(); err != nil {
						return err
					}
					mutated = true
					if kind == "stage" {
						hardStageBoundary = documentSessionCaptureTree(t, root)
					}
					return nil
				},
				afterRestoreSync: func(parent *os.Root, name string) error {
					restorePath = filepath.Join(parent.Name(), name)
					return nil
				},
				afterInstall: func(*os.Root, string) error {
					if rollingBack {
						return nil
					}
					var statErr error
					installedInfo, statErr = os.Lstat(target)
					if statErr != nil {
						return statErr
					}
					rollingBack = true
					return fault
				},
			}
			session, err := openDocumentSessionContext(
				context.Background(),
				target,
				"",
				documentSessionHooks{publish: hooks},
			)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if held != nil {
					_ = held.Close()
				}
				_ = session.Close()
			})

			// Act.
			rewriteErr := session.RewriteContext(
				context.Background(),
				documentSessionParsed(t, published),
			)

			// Assert.
			if !mutated ||
				anchorInfo == nil ||
				installedInfo == nil ||
				!errors.Is(rewriteErr, fault) ||
				!errors.Is(rewriteErr, ErrDocumentConflict) {
				t.Fatalf(
					"mutated=%t installed=%v A=%v error=%v, want phase-aware pre-D typed conflict; residuals: %s",
					mutated,
					installedInfo,
					anchorInfo,
					rewriteErr,
					documentSessionPublicationArtifactDebug(t, root),
				)
			}
			if discardPath == "" {
				t.Fatal("rollback D transition was not observed")
			}
			currentAnchor, anchorErr := os.Lstat(anchorPath)
			restoreInfo, restoreErr := os.Lstat(restorePath)
			if anchorErr != nil ||
				restoreErr != nil ||
				!os.SameFile(currentAnchor, anchorInfo) ||
				!os.SameFile(currentAnchor, restoreInfo) ||
				documentSessionReadFile(t, restorePath) != original {
				t.Fatalf(
					"exact A~R was not retained after %s mutation: A(before)=%v A(after)=%v R=%v AErr=%v RErr=%v residuals: %s",
					kind,
					anchorInfo,
					currentAnchor,
					restoreInfo,
					anchorErr,
					restoreErr,
					documentSessionPublicationArtifactDebug(t, root),
				)
			}
			if kind == "stage" {
				if hardStageBoundary == nil {
					t.Fatal("hard S~D mutation boundary was not captured")
				}
				if _, targetErr := os.Lstat(target); !errors.Is(targetErr, fs.ErrNotExist) {
					t.Fatalf(
						"target error=%v, want missing after hard S~D drift; residuals: %s",
						targetErr,
						documentSessionPublicationArtifactDebug(t, root),
					)
				}
				discardInfo, discardErr := os.Lstat(discardPath)
				if discardErr != nil || !os.SameFile(discardInfo, installedInfo) {
					t.Fatalf(
						"mutated D was not retained as exact installed New inode: installed=%v D=%v error=%v residuals: %s",
						installedInfo,
						discardInfo,
						discardErr,
						documentSessionPublicationArtifactDebug(t, root),
					)
				}
				if got, want := documentSessionReadFile(t, discardPath), "X"+published[1:]; got != want {
					t.Fatalf(
						"mutated D bytes = %q, want retained hard drift %q; residuals: %s",
						got,
						want,
						documentSessionPublicationArtifactDebug(t, root),
					)
				}
				documentSessionAssertTreeSnapshotEqual(
					t,
					hardStageBoundary,
					documentSessionCaptureTree(t, root),
				)
				if closeErr := session.Close(); closeErr != nil {
					t.Fatal(closeErr)
				}
				beforeRestart := documentSessionCaptureTree(t, root)
				reopened, recoveryErr := OpenDocumentSessionContext(context.Background(), target, "")
				if reopened != nil {
					_ = reopened.Close()
				}
				if reopened != nil || !errors.Is(recoveryErr, ErrDocumentConflict) {
					t.Fatalf(
						"restart session=%#v error=%v, want zero-mutation hard S~D conflict; residuals: %s",
						reopened,
						recoveryErr,
						documentSessionPublicationArtifactDebug(t, root),
					)
				}
				documentSessionAssertTreeSnapshotEqual(
					t,
					beforeRestart,
					documentSessionCaptureTree(t, root),
				)
				return
			}
			targetInfo, targetErr := os.Lstat(target)
			if targetErr != nil || !os.SameFile(targetInfo, installedInfo) {
				t.Fatalf(
					"pre-D compensation did not restore exact installed New after %s mutation: installed=%v target=%v error=%v residuals: %s",
					kind,
					installedInfo,
					targetInfo,
					targetErr,
					documentSessionPublicationArtifactDebug(t, root),
				)
			}
			if got := documentSessionReadFile(t, target); got != published {
				t.Fatalf(
					"compensated New bytes after %s mutation = %q, want %q; residuals: %s",
					kind,
					got,
					published,
					documentSessionPublicationArtifactDebug(t, root),
				)
			}
			if _, discardErr := os.Lstat(discardPath); !errors.Is(discardErr, fs.ErrNotExist) {
				t.Fatalf(
					"D error=%v, want absent after pre-receipt compensation; residuals: %s",
					discardErr,
					documentSessionPublicationArtifactDebug(t, root),
				)
			}
		})
	}
}

func TestDocumentRecoveryRevalidatesEarlierGroupAfterLaterCleanupHook(t *testing.T) {
	t.Parallel()

	tests := []string{"held descriptor mutation", "detached parent"}
	for _, mutation := range tests {
		mutation := mutation
		t.Run(mutation, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			earlierTarget := filepath.Join(root, "a", "concept.md")
			laterTarget := filepath.Join(root, "z", "concept.md")
			earlier := documentSessionConcept("a", "before")
			later := documentSessionConcept("z", "before")
			documentSessionWriteFile(t, filepath.Join(root, indexFilename), documentSessionIndex("0.2", "root"))
			earlierFixture := documentSessionCreateV2RecoveryState(
				t, root, "a/concept.md", 0o644, earlier, documentSessionConcept("a", "after"),
				documentV2State(true, false, false, false, false, documentV2TargetWitness),
			)
			laterFixture := documentSessionCreateV2RecoveryState(
				t, root, "z/concept.md", 0o644, later, documentSessionConcept("z", "after"),
				documentV2State(true, false, false, false, false, documentV2TargetWitness),
			)
			if !validDocumentRecoveryManifest(&documentRecoveryGroup{directory: "a", manifest: earlierFixture.manifest}) ||
				!validDocumentRecoveryManifest(&documentRecoveryGroup{directory: "z", manifest: laterFixture.manifest}) {
				t.Fatalf("invalid arranged manifests: earlier=%+v later=%+v", earlierFixture.manifest, laterFixture.manifest)
			}
			held, err := os.OpenFile(earlierTarget, os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = held.Close() })
			laterParent, err := os.Lstat(filepath.Join(root, "z"))
			if err != nil {
				t.Fatal(err)
			}
			mutated := false
			hooks := documentSessionHooks{publish: documentPublishHooks{
				beforeCleanup: func(parent *os.Root, kind, _ string) error {
					parentInfo, statErr := parent.Lstat(".")
					if statErr != nil {
						return statErr
					}
					if !mutated &&
						kind == "manifest" &&
						os.SameFile(parentInfo, laterParent) {
						mutated = true
						switch mutation {
						case "held descriptor mutation":
							if _, writeErr := held.WriteAt([]byte("X"), 0); writeErr != nil {
								return writeErr
							}
							if syncErr := held.Sync(); syncErr != nil {
								return syncErr
							}
						case "detached parent":
							if renameErr := os.Rename(filepath.Join(root, "a"), filepath.Join(root, "a.detached")); renameErr != nil {
								return renameErr
							}
							if mkdirErr := os.Mkdir(filepath.Join(root, "a"), 0o755); mkdirErr != nil {
								return mkdirErr
							}
						default:
							t.Fatalf("unknown mutation %q", mutation)
						}
					}
					return nil
				},
			}}

			// Act.
			session, recoveryErr := openDocumentSessionContext(
				context.Background(), laterTarget, "", hooks,
			)
			if session != nil {
				_ = session.Close()
			}

			// Assert.
			if !mutated || session != nil || !errors.Is(recoveryErr, ErrDocumentConflict) {
				t.Fatalf("mutated=%t session=%#v error=%v, want global revalidation conflict", mutated, session, recoveryErr)
			}
			if mutation == "held descriptor mutation" {
				if got := documentSessionReadFile(t, earlierTarget); got == earlier {
					t.Fatal("earlier target mutation was not retained for conflict inspection")
				}
			} else {
				if got := documentSessionReadFile(t, filepath.Join(root, "a.detached", "concept.md")); got != earlier {
					t.Fatalf("detached earlier target bytes = %q, want %q", got, earlier)
				}
			}
		})
	}
}

func TestOpenDocumentSessionRecoveryRejectsNoOpManifestWithoutMutation(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	content := documentSessionConcept("concept", "same")
	documentSessionWriteFile(t, target, content)
	documentSessionCreateRecoveryGroup(
		t,
		root,
		"concept.md",
		0o644,
		content,
		content,
		"manifest",
		"backup",
		"stage",
	)
	before := documentSessionCaptureTree(t, root)

	// Act.
	session, err := OpenDocumentSessionContext(context.Background(), target, "")
	if session != nil {
		_ = session.Close()
	}
	after := documentSessionCaptureTree(t, root)

	// Assert.
	if err == nil || session != nil {
		t.Fatalf("session=%#v, error=%v; want no-op manifest rejection", session, err)
	}
	documentSessionAssertTreeSnapshotEqual(t, before, after)
}

func TestOpenDocumentSessionRecoveryRevalidatesTargetAfterPreparedInventory(t *testing.T) {
	t.Parallel()

	const (
		sameBytes = "same-bytes-different-inode"
		foreign   = "foreign-bytes"
		symlink   = "symlink"
		missing   = "missing"
	)
	tests := []struct {
		name  string
		state string
	}{
		{name: "same bytes and mode under a different inode", state: sameBytes},
		{name: "foreign regular bytes", state: foreign},
		{name: "symlink", state: symlink},
		{name: "missing leaf", state: missing},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			target := filepath.Join(root, "concept.md")
			external := filepath.Join(root, "external.md")
			original := documentSessionConcept("concept", "before")
			published := documentSessionConcept("concept", "after")
			foreignBytes := documentSessionConcept("foreign", "preserve")
			documentSessionWriteFile(t, filepath.Join(root, indexFilename), documentSessionIndex("0.2", "Root"))
			fixture := documentSessionCreateV2RecoveryState(
				t,
				root,
				"concept.md",
				0o640,
				original,
				published,
				documentV2State(true, false, false, false, false, documentV2TargetWitness),
			)
			artifacts := documentSessionCaptureRecoveryArtifacts(t, root, fixture)
			mutated := false
			hooks := documentSessionHooks{
				publish: documentPublishHooks{
					afterRecoveryInventory: func() error {
						mutated = true
						switch test.state {
						case sameBytes:
							documentSessionAtomicReplace(t, target, original, 0o640)
						case foreign:
							documentSessionAtomicReplace(t, target, foreignBytes, 0o640)
						case symlink:
							documentSessionWriteFile(t, external, "external sentinel")
							if err := os.Remove(target); err != nil {
								t.Fatal(err)
							}
							if err := os.Symlink(external, target); err != nil {
								t.Skipf("symlinks are unavailable: %v", err)
							}
						case missing:
							if err := os.Remove(target); err != nil {
								t.Fatal(err)
							}
						default:
							t.Fatalf("unknown target state %q", test.state)
						}
						return nil
					},
				},
			}

			// Act.
			session, err := openDocumentSessionContext(context.Background(), target, "", hooks)
			if session != nil {
				_ = session.Close()
			}

			// Assert.
			if !mutated || err == nil || session != nil {
				t.Fatalf("mutated=%t, session=%#v, error=%v; want fail-closed recovery", mutated, session, err)
			}
			documentSessionAssertRecoveryArtifactsUnchanged(t, root, artifacts)
			switch test.state {
			case sameBytes:
				if got := documentSessionReadFile(t, target); got != original {
					t.Fatalf("same-byte replacement = %q, want %q", got, original)
				}
			case foreign:
				if got := documentSessionReadFile(t, target); got != foreignBytes {
					t.Fatalf("foreign replacement = %q, want %q", got, foreignBytes)
				}
			case symlink:
				info, statErr := os.Lstat(target)
				if statErr != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Fatalf("symlink replacement = %v, %v", info, statErr)
				}
				if got := documentSessionReadFile(t, external); got != "external sentinel" {
					t.Fatalf("external sentinel = %q", got)
				}
			case missing:
				if _, statErr := os.Lstat(target); !errors.Is(statErr, fs.ErrNotExist) {
					t.Fatalf("Lstat(missing target) error = %v", statErr)
				}
			}
		})
	}
}

func TestOpenDocumentSessionRecoveryRejectsInvalidGlobalInventoryWithoutMutation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		inject func(t *testing.T, root string)
	}{
		{
			name: "mismatched stage",
			inject: func(t *testing.T, root string) {
				t.Helper()
				group := documentSessionCreateRecoveryGroup(
					t,
					root,
					"a/concept.md",
					0o644,
					documentSessionConcept("a", "before"),
					documentSessionConcept("a", "after"),
					"manifest",
					"backup",
					"stage",
				)
				documentSessionWriteFile(t, filepath.Join(root, "a", group.names["stage"]), "tampered")
			},
		},
		{
			name: "partial artifact without manifest",
			inject: func(t *testing.T, root string) {
				t.Helper()
				name := documentArtifactName("backup", strings.Repeat("a", 64))
				documentSessionWriteFile(t, filepath.Join(root, "z", name), "orphan")
			},
		},
		{
			name: "noncanonical casefold artifact",
			inject: func(t *testing.T, root string) {
				t.Helper()
				name := strings.ToUpper(documentTransactionPrefix) + "manifest-" + strings.Repeat("a", 64)
				documentSessionWriteFile(t, filepath.Join(root, "z", name), "{}")
			},
		},
		{
			name: "duplicate physical target",
			inject: func(t *testing.T, root string) {
				t.Helper()
				original := documentSessionConcept("a", "before")
				documentSessionCreateRecoveryGroup(
					t, root, "a/concept.md", 0o644, original,
					documentSessionConcept("a", "after-one"), "manifest", "backup",
				)
				documentSessionCreateRecoveryGroup(
					t, root, "a/concept.md", 0o644, original,
					documentSessionConcept("a", "after-two"), "manifest", "stage",
				)
			},
		},
		{
			name: "cross namespace invalid index artifact",
			inject: func(t *testing.T, root string) {
				t.Helper()
				documentSessionWriteFile(
					t,
					filepath.Join(root, "z", indexTransactionPrefix+"unknown"),
					"foreign index protocol evidence",
				)
			},
		},
		{
			name: "cross namespace casefold index artifact",
			inject: func(t *testing.T, root string) {
				t.Helper()
				documentSessionWriteFile(
					t,
					filepath.Join(
						root,
						"z",
						strings.ToUpper(indexTransactionPrefix)+"backup-v1-0000-"+strings.Repeat("a", 64),
					),
					"casefold index protocol evidence",
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
			target := filepath.Join(root, "a", "concept.md")
			original := documentSessionConcept("a", "before")
			documentSessionWriteFile(t, target, original)
			fixture := documentSessionCreateRecoveryGroup(
				t,
				root,
				"a/concept.md",
				0o644,
				original,
				documentSessionConcept("a", "after"),
				"manifest",
				"backup",
			)
			documentSessionLinkOriginalRecoveryBackup(t, root, target, fixture)
			test.inject(t, root)
			before := documentSessionCaptureTree(t, root)

			// Act.
			session, err := OpenDocumentSessionContext(context.Background(), target, "")
			if session != nil {
				_ = session.Close()
			}
			after := documentSessionCaptureTree(t, root)

			// Assert.
			if err == nil {
				t.Fatal("OpenDocumentSessionContext() succeeded with invalid global recovery inventory")
			}
			documentSessionAssertTreeSnapshotEqual(t, before, after)
		})
	}
}

func TestCombinedPublicationRecoveryRejectsInvalidDocumentInventoryBeforeIndexMutation(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	documentSessionWriteFile(t, filepath.Join(root, "index.md"), documentSessionIndex("0.2", "root"))
	documentSessionWriteFile(t, filepath.Join(root, "nested", "note.md"), documentSessionConcept("note", "body"))
	originalIndex := []byte("# Original nested index\n")
	documentSessionWriteFile(t, filepath.Join(root, "nested", "index.md"), string(originalIndex))
	writeIndexTransactionArtifact(
		t,
		root,
		"nested/index.md",
		indexArtifactBackup,
		originalIndex,
		0o644,
		0,
	)
	documentSessionWriteFile(
		t,
		filepath.Join(root, "z", documentArtifactName("backup", strings.Repeat("a", 64))),
		"invalid document recovery evidence without manifest",
	)
	before := documentSessionCaptureTree(t, root)

	// Act.
	written, err := regenerateIndexesWithSelectorForTest(root, "")
	after := documentSessionCaptureTree(t, root)

	// Assert.
	if err == nil {
		t.Fatalf("RegenerateIndexesWithSelector() = %#v, nil; want combined inventory error", written)
	}
	if written != nil {
		t.Fatalf("RegenerateIndexesWithSelector() written = %#v, want nil", written)
	}
	documentSessionAssertTreeSnapshotEqual(t, before, after)
}

func TestCombinedPublicationRecoveryRejectsCrossProtocolPhysicalTargetCollision(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "nested", "index.md")
	original := documentSessionIndex("", "nested")
	published := documentSessionIndex("", "published")
	documentSessionWriteFile(t, filepath.Join(root, "index.md"), documentSessionIndex("0.2", "root"))
	documentSessionWriteFile(t, target, original)
	fixture := documentSessionCreateRecoveryGroup(
		t,
		root,
		"nested/index.md",
		0o644,
		original,
		published,
		"manifest",
		"backup",
	)
	documentSessionLinkOriginalRecoveryBackup(t, root, target, fixture)
	writeIndexTransactionArtifact(
		t,
		root,
		"nested/index.md",
		indexArtifactBackup,
		[]byte(original),
		0o644,
		0,
	)
	before := documentSessionCaptureTree(t, root)

	// Act.
	session, err := OpenDocumentSessionContext(context.Background(), target, "")
	if session != nil {
		_ = session.Close()
	}
	after := documentSessionCaptureTree(t, root)

	// Assert.
	if err == nil {
		t.Fatal("OpenDocumentSessionContext() accepted two recovery protocols claiming one physical target")
	}
	if session != nil {
		t.Fatal("OpenDocumentSessionContext() returned a session for cross-protocol collision")
	}
	documentSessionAssertTreeSnapshotEqual(t, before, after)
}

func TestPublicationRecoveryConvergesOwnedPrivateRemovalClaimAfterCrash(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		kind    string
		recover func(t *testing.T, root, target string) error
	}{
		{
			name: "session open after stage quarantine",
			kind: "stage",
			recover: func(t *testing.T, _ string, target string) error {
				t.Helper()
				session, err := OpenDocumentSessionContext(context.Background(), target, "")
				if session != nil {
					err = errors.Join(err, session.Close())
				}
				return err
			},
		},
		{
			name: "session open after backup quarantine",
			kind: "backup",
			recover: func(t *testing.T, _ string, target string) error {
				t.Helper()
				session, err := OpenDocumentSessionContext(context.Background(), target, "")
				if session != nil {
					err = errors.Join(err, session.Close())
				}
				return err
			},
		},
		{
			name: "session open after claim quarantine",
			kind: "claim",
			recover: func(t *testing.T, _ string, target string) error {
				t.Helper()
				session, err := OpenDocumentSessionContext(context.Background(), target, "")
				if session != nil {
					err = errors.Join(err, session.Close())
				}
				return err
			},
		},
		{
			name: "session open after manifest quarantine",
			kind: "manifest",
			recover: func(t *testing.T, _ string, target string) error {
				t.Helper()
				session, err := OpenDocumentSessionContext(context.Background(), target, "")
				if session != nil {
					err = errors.Join(err, session.Close())
				}
				return err
			},
		},
		{
			name: "index regeneration after stage quarantine",
			kind: "stage",
			recover: func(t *testing.T, root, _ string) error {
				t.Helper()
				_, err := regenerateIndexesWithSelectorForTest(root, "")
				return err
			},
		},
		{
			name: "index regeneration after backup quarantine",
			kind: "backup",
			recover: func(t *testing.T, root, _ string) error {
				t.Helper()
				_, err := regenerateIndexesWithSelectorForTest(root, "")
				return err
			},
		},
		{
			name: "index regeneration after claim quarantine",
			kind: "claim",
			recover: func(t *testing.T, root, _ string) error {
				t.Helper()
				_, err := regenerateIndexesWithSelectorForTest(root, "")
				return err
			},
		},
		{
			name: "index regeneration after manifest quarantine",
			kind: "manifest",
			recover: func(t *testing.T, root, _ string) error {
				t.Helper()
				_, err := regenerateIndexesWithSelectorForTest(root, "")
				return err
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
			original := documentSessionConcept("concept", "before")
			published := documentSessionConcept("concept", "after")
			documentSessionWriteFile(t, filepath.Join(root, "index.md"), documentSessionIndex("0.2", "root"))
			documentSessionWriteFile(t, target, published)
			fixture := documentSessionCreateRecoveryGroup(
				t,
				root,
				"concept.md",
				0o644,
				original,
				published,
				"manifest",
				"backup",
				"stage",
				"claim",
			)
			documentSessionLinkCommittedRecoveryAliases(t, root, target, fixture)
			pinned, err := openRootWithoutSymlinks(root)
			if err != nil {
				t.Fatalf("openRootWithoutSymlinks() error = %v", err)
			}
			publicName := fixture.names[test.kind]
			privateName, err := allocatePrivatePublicationClaim(pinned, publicName)
			if err != nil {
				_ = pinned.Close()
				t.Fatalf("allocatePrivatePublicationClaim() error = %v", err)
			}
			if err := pinned.Rename(publicName, privateName); err != nil {
				_ = pinned.Close()
				t.Fatalf("simulate artifact-to-private-claim crash rename: %v", err)
			}
			if err := pinned.Close(); err != nil {
				t.Fatalf("close arranged root: %v", err)
			}

			// Act.
			err = test.recover(t, root, target)

			// Assert.
			if err != nil {
				t.Fatalf("recovery after private-claim crash error = %v", err)
			}
			if got := documentSessionReadFile(t, target); got != published {
				t.Fatalf("published target after recovery = %q, want %q", got, published)
			}
			documentSessionAssertNoPublicationArtifacts(t, root)
		})
	}
}

func TestPublicationRecoveryConvergesOwnedPrivateIndexClaimAfterCrash(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		kind    indexArtifactKind
		recover func(t *testing.T, root, target string) error
	}{
		{
			name: "session open after index stage quarantine",
			kind: indexArtifactStage,
			recover: func(t *testing.T, _ string, target string) error {
				t.Helper()
				session, err := OpenDocumentSessionContext(context.Background(), target, "")
				if session != nil {
					err = errors.Join(err, session.Close())
				}
				return err
			},
		},
		{
			name: "session open after index backup quarantine",
			kind: indexArtifactBackup,
			recover: func(t *testing.T, _ string, target string) error {
				t.Helper()
				session, err := OpenDocumentSessionContext(context.Background(), target, "")
				if session != nil {
					err = errors.Join(err, session.Close())
				}
				return err
			},
		},
		{
			name: "session open after index restore quarantine",
			kind: indexArtifactRestore,
			recover: func(t *testing.T, _ string, target string) error {
				t.Helper()
				session, err := OpenDocumentSessionContext(context.Background(), target, "")
				if session != nil {
					err = errors.Join(err, session.Close())
				}
				return err
			},
		},
		{
			name: "index regeneration after index stage quarantine",
			kind: indexArtifactStage,
			recover: func(t *testing.T, root, _ string) error {
				t.Helper()
				_, err := regenerateIndexesWithSelectorForTest(root, "")
				return err
			},
		},
		{
			name: "index regeneration after index backup quarantine",
			kind: indexArtifactBackup,
			recover: func(t *testing.T, root, _ string) error {
				t.Helper()
				_, err := regenerateIndexesWithSelectorForTest(root, "")
				return err
			},
		},
		{
			name: "index regeneration after index restore quarantine",
			kind: indexArtifactRestore,
			recover: func(t *testing.T, root, _ string) error {
				t.Helper()
				_, err := regenerateIndexesWithSelectorForTest(root, "")
				return err
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
			documentSessionWriteFile(t, filepath.Join(root, "index.md"), documentSessionIndex("0.2", "root"))
			documentSessionWriteFile(t, target, documentSessionConcept("concept", "body"))
			documentSessionWriteFile(t, filepath.Join(root, "nested", "note.md"), documentSessionConcept("note", "body"))
			artifactData := []byte("# Interrupted nested index\n")
			if test.kind == indexArtifactBackup || test.kind == indexArtifactRestore {
				documentSessionWriteFile(t, filepath.Join(root, "nested", "index.md"), string(artifactData))
			}
			artifact := writeIndexTransactionArtifact(
				t,
				root,
				"nested/index.md",
				test.kind,
				artifactData,
				0o644,
				0,
			)
			if test.kind == indexArtifactRestore {
				destination := filepath.Join(root, "nested", "index.md")
				if err := os.Remove(destination); err != nil {
					t.Fatalf("Remove(independent restore destination) error = %v", err)
				}
				if err := os.Link(
					filepath.Join(root, filepath.FromSlash(artifact)),
					destination,
				); err != nil {
					t.Fatalf("Link(restore artifact, destination) error = %v", err)
				}
			}
			pinned, err := openRootWithoutSymlinks(root)
			if err != nil {
				t.Fatalf("openRootWithoutSymlinks() error = %v", err)
			}
			parent, err := openDirectory(pinned, "nested")
			if err != nil {
				_ = pinned.Close()
				t.Fatalf("openDirectory(nested) error = %v", err)
			}
			publicName := filepath.Base(filepath.FromSlash(artifact))
			privateName, err := allocatePrivatePublicationClaim(parent, publicName)
			if err != nil {
				_ = parent.Close()
				_ = pinned.Close()
				t.Fatalf("allocatePrivatePublicationClaim() error = %v", err)
			}
			if err := parent.Rename(publicName, privateName); err != nil {
				_ = parent.Close()
				_ = pinned.Close()
				t.Fatalf("simulate index artifact-to-private-claim crash rename: %v", err)
			}
			if err := errors.Join(parent.Close(), pinned.Close()); err != nil {
				t.Fatalf("close arranged roots: %v", err)
			}
			// Act.
			err = test.recover(t, root, target)

			// Assert.
			if err != nil {
				t.Fatalf("all-old recovery after private index-claim crash error = %v", err)
			}
			documentSessionAssertNoPublicationArtifacts(t, root)
		})
	}
}

func TestPublicationRecoveryRejectsUnprovenPrivateClaimWithoutMutation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		inject func(t *testing.T, root *os.Root, fixture documentSessionRecoveryFixture) string
	}{
		{
			name: "same artifact bytes under wrong canonical name hash",
			inject: func(
				t *testing.T,
				root *os.Root,
				fixture documentSessionRecoveryFixture,
			) string {
				t.Helper()
				stageName := fixture.names["stage"]
				wrongName, err := allocatePrivatePublicationClaim(root, fixture.names["backup"])
				if err != nil {
					t.Fatalf("allocate wrong-hash private claim: %v", err)
				}
				if err := root.Rename(stageName, wrongName); err != nil {
					t.Fatalf("rename stage under wrong-hash private claim: %v", err)
				}
				return wrongName
			},
		},
		{
			name: "nested hash binds private physical name",
			inject: func(
				t *testing.T,
				root *os.Root,
				fixture documentSessionRecoveryFixture,
			) string {
				t.Helper()
				stageName := fixture.names["stage"]
				firstPrivate, err := allocatePrivatePublicationClaim(root, stageName)
				if err != nil {
					t.Fatalf("allocate first private claim: %v", err)
				}
				if err := root.Rename(stageName, firstPrivate); err != nil {
					t.Fatalf("rename stage to first private claim: %v", err)
				}
				nestedPrivate := publicationPrivateClaimPrefix +
					documentDigest([]byte(firstPrivate)) + "-" +
					strings.Repeat("b", 32)
				if err := root.Rename(firstPrivate, nestedPrivate); err != nil {
					t.Fatalf("rename first private to nested-hash claim: %v", err)
				}
				return nestedPrivate
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
			original := documentSessionConcept("concept", "before")
			published := documentSessionConcept("concept", "after")
			documentSessionWriteFile(t, filepath.Join(root, "index.md"), documentSessionIndex("0.2", "root"))
			documentSessionWriteFile(t, target, published)
			fixture := documentSessionCreateRecoveryGroup(
				t,
				root,
				"concept.md",
				0o644,
				original,
				published,
				"manifest",
				"backup",
				"stage",
				"claim",
			)
			documentSessionLinkCommittedRecoveryAliases(t, root, target, fixture)
			pinned, err := openRootWithoutSymlinks(root)
			if err != nil {
				t.Fatalf("openRootWithoutSymlinks() error = %v", err)
			}
			privateName := test.inject(t, pinned, fixture)
			if err := pinned.Close(); err != nil {
				t.Fatalf("close arranged root: %v", err)
			}
			before := documentSessionCaptureTree(t, root)

			// Act.
			session, err := OpenDocumentSessionContext(context.Background(), target, "")
			if session != nil {
				_ = session.Close()
			}
			after := documentSessionCaptureTree(t, root)

			// Assert.
			if err == nil {
				t.Fatal("OpenDocumentSessionContext() accepted unproven private claim")
			}
			if session != nil {
				t.Fatal("OpenDocumentSessionContext() returned session with unproven private claim")
			}
			if _, statErr := os.Lstat(filepath.Join(root, privateName)); statErr != nil {
				t.Fatalf("unproven private claim was not retained: %v", statErr)
			}
			documentSessionAssertTreeSnapshotEqual(t, before, after)
		})
	}
}

func TestPublicationRecoveryRejectsExcessPartialPrivateCreatesBeforeMutation(t *testing.T) {
	t.Parallel()

	entrypoints := []struct {
		name string
		run  func(t *testing.T, root, target string) error
	}{
		{
			name: "document session",
			run: func(t *testing.T, _ string, target string) error {
				t.Helper()
				session, err := OpenDocumentSessionContext(context.Background(), target, "")
				if session != nil {
					err = errors.Join(err, session.Close())
				}
				return err
			},
		},
		{
			name: "index regeneration",
			run: func(t *testing.T, root, _ string) error {
				t.Helper()
				_, err := regenerateIndexesForTest(root)
				return err
			},
		},
	}
	for _, entrypoint := range entrypoints {
		entrypoint := entrypoint
		t.Run(entrypoint.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			target := filepath.Join(root, "concept.md")
			documentSessionWriteFile(t, filepath.Join(root, "index.md"), documentSessionIndex("0.2", "root"))
			documentSessionWriteFile(t, target, documentSessionConcept("concept", "body"))
			pinned, err := openRootWithoutSymlinks(root)
			if err != nil {
				t.Fatal(err)
			}
			for ordinal := 0; ordinal < maxLivePrivatePublicationClaims+1; ordinal++ {
				privateName, allocateErr := allocatePrivatePublicationClaim(
					pinned,
					documentArtifactName("stage", strings.Repeat(itoa(ordinal+1), 64)),
				)
				if allocateErr != nil {
					_ = pinned.Close()
					t.Fatal(allocateErr)
				}
				if writeErr := pinned.WriteFile(
					privateName,
					[]byte("partial private create "+itoa(ordinal)+"\n"),
					0o600,
				); writeErr != nil {
					_ = pinned.Close()
					t.Fatal(writeErr)
				}
			}
			if err := pinned.Close(); err != nil {
				t.Fatal(err)
			}
			before := documentSessionCaptureTree(t, root)

			// Act.
			err = entrypoint.run(t, root, target)
			after := documentSessionCaptureTree(t, root)

			// Assert.
			if err == nil || !strings.Contains(err.Error(), "inventory exceeds protocol limit") {
				t.Fatalf("recovery error = %v, want private inventory cap", err)
			}
			documentSessionAssertTreeSnapshotEqual(t, before, after)
		})
	}
}

func TestPublicationRecoveryRejectsForeignPrivateEntriesBothEntrypoints(t *testing.T) {
	t.Parallel()

	entrySetups := []struct {
		name string
		make func(t *testing.T, filename string)
	}{
		{
			name: "symlink",
			make: func(t *testing.T, filename string) {
				t.Helper()
				foreign := filepath.Join(filepath.Dir(filename), "foreign-private-target")
				documentSessionWriteFile(t, foreign, "foreign")
				if err := os.Symlink(foreign, filename); err != nil {
					t.Fatalf("Symlink(private entry) error = %v", err)
				}
			},
		},
		{
			name: "directory",
			make: func(t *testing.T, filename string) {
				t.Helper()
				if err := os.Mkdir(filename, 0o755); err != nil {
					t.Fatalf("Mkdir(private entry) error = %v", err)
				}
			},
		},
		{
			name: "malformed name",
			make: func(t *testing.T, filename string) {
				t.Helper()
				documentSessionWriteFile(t, filename, "malformed-private-entry")
			},
		},
	}
	entrypoints := []struct {
		name string
		run  func(t *testing.T, root, target string) error
	}{
		{
			name: "session open",
			run: func(t *testing.T, _ string, target string) error {
				t.Helper()
				session, err := OpenDocumentSessionContext(context.Background(), target, "")
				if session != nil {
					err = errors.Join(err, session.Close())
				}
				return err
			},
		},
		{
			name: "index regeneration",
			run: func(t *testing.T, root, _ string) error {
				t.Helper()
				_, err := regenerateIndexesWithSelectorForTest(root, "")
				return err
			},
		},
	}

	for _, entry := range entrySetups {
		entry := entry
		for _, entrypoint := range entrypoints {
			entrypoint := entrypoint
			t.Run(entrypoint.name+"/"+entry.name, func(t *testing.T) {
				t.Parallel()

				// Arrange.
				root := t.TempDir()
				target := filepath.Join(root, "concept.md")
				documentSessionWriteFile(t, filepath.Join(root, "index.md"), documentSessionIndex("0.2", "root"))
				documentSessionWriteFile(t, target, documentSessionConcept("concept", "body"))
				privateName := publicationPrivateClaimPrefix + "malformed"
				if entry.name != "malformed name" {
					pinned, err := openRootWithoutSymlinks(root)
					if err != nil {
						t.Fatalf("openRootWithoutSymlinks() error = %v", err)
					}
					privateName, err = allocatePrivatePublicationClaim(
						pinned,
						documentArtifactName("stage", strings.Repeat("a", 64)),
					)
					closeErr := pinned.Close()
					if err != nil || closeErr != nil {
						t.Fatalf("allocate private name = %q, %v; close = %v", privateName, err, closeErr)
					}
				}
				privatePath := filepath.Join(root, privateName)
				entry.make(t, privatePath)
				before := documentSessionCaptureTree(t, root)

				// Act.
				err := entrypoint.run(t, root, target)
				after := documentSessionCaptureTree(t, root)

				// Assert.
				if err == nil {
					t.Fatal("publication entrypoint accepted foreign private entry")
				}
				if _, statErr := os.Lstat(privatePath); statErr != nil {
					t.Fatalf("foreign private entry was not retained: %v", statErr)
				}
				documentSessionAssertTreeSnapshotEqual(t, before, after)
			})
		}
	}
}

func TestPublicationRecoveryConvergesNestedPrivateClaimAfterSecondCrash(t *testing.T) {
	t.Parallel()

	t.Run("document artifact", func(t *testing.T) {
		t.Parallel()

		// Arrange.
		root := t.TempDir()
		target := filepath.Join(root, "concept.md")
		original := documentSessionConcept("concept", "before")
		published := documentSessionConcept("concept", "after")
		documentSessionWriteFile(t, filepath.Join(root, "index.md"), documentSessionIndex("0.2", "root"))
		documentSessionWriteFile(t, target, published)
		fixture := documentSessionCreateRecoveryGroup(
			t, root, "concept.md", 0o644, original, published,
			"manifest", "backup", "stage", "claim",
		)
		documentSessionLinkCommittedRecoveryAliases(t, root, target, fixture)
		pinned, err := openRootWithoutSymlinks(root)
		if err != nil {
			t.Fatalf("openRootWithoutSymlinks() error = %v", err)
		}
		publicName := fixture.names["stage"]
		firstPrivate, err := allocatePrivatePublicationClaim(pinned, publicName)
		if err != nil {
			t.Fatalf("allocate first private claim: %v", err)
		}
		if err := pinned.Rename(publicName, firstPrivate); err != nil {
			t.Fatalf("first crash rename: %v", err)
		}
		secondPrivate, err := allocatePrivatePublicationClaim(pinned, publicName)
		if err != nil {
			t.Fatalf("allocate nested private claim: %v", err)
		}
		if err := pinned.Rename(firstPrivate, secondPrivate); err != nil {
			t.Fatalf("second crash rename: %v", err)
		}
		if err := pinned.Close(); err != nil {
			t.Fatalf("close arranged root: %v", err)
		}

		// Act.
		session, err := OpenDocumentSessionContext(context.Background(), target, "")
		if session != nil {
			err = errors.Join(err, session.Close())
		}

		// Assert.
		if err != nil {
			t.Fatalf("recovery after nested private claim error = %v", err)
		}
		documentSessionAssertNoPublicationArtifacts(t, root)
	})

	t.Run("index artifact", func(t *testing.T) {
		t.Parallel()

		// Arrange.
		root := t.TempDir()
		documentSessionWriteFile(t, filepath.Join(root, "index.md"), documentSessionIndex("0.2", "root"))
		documentSessionWriteFile(t, filepath.Join(root, "nested", "note.md"), documentSessionConcept("note", "body"))
		artifact := writeIndexTransactionArtifact(
			t, root, "nested/index.md", indexArtifactStage,
			[]byte("# Interrupted nested index\n"), 0o644, 0,
		)
		pinned, err := openRootWithoutSymlinks(root)
		if err != nil {
			t.Fatalf("openRootWithoutSymlinks() error = %v", err)
		}
		parent, err := openDirectory(pinned, "nested")
		if err != nil {
			t.Fatalf("openDirectory(nested) error = %v", err)
		}
		publicName := filepath.Base(filepath.FromSlash(artifact))
		firstPrivate, err := allocatePrivatePublicationClaim(parent, publicName)
		if err != nil {
			t.Fatalf("allocate first private claim: %v", err)
		}
		if err := parent.Rename(publicName, firstPrivate); err != nil {
			t.Fatalf("first crash rename: %v", err)
		}
		secondPrivate, err := allocatePrivatePublicationClaim(parent, publicName)
		if err != nil {
			t.Fatalf("allocate nested private claim: %v", err)
		}
		if err := parent.Rename(firstPrivate, secondPrivate); err != nil {
			t.Fatalf("second crash rename: %v", err)
		}
		if err := errors.Join(parent.Close(), pinned.Close()); err != nil {
			t.Fatalf("close arranged roots: %v", err)
		}

		// Act.
		_, err = regenerateIndexesWithSelectorForTest(root, "")

		// Assert.
		if err != nil {
			t.Fatalf("index recovery after nested private claim error = %v", err)
		}
		documentSessionAssertNoPublicationArtifacts(t, root)
	})
}

func TestOpenDocumentSessionSkipsRootStoreMetadataDirectory(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	documentSessionWriteFile(t, target, documentSessionConcept("concept", "body"))
	metadata := filepath.Join(root, ".okf")
	if err := os.Mkdir(metadata, 0o700); err != nil {
		t.Fatalf("Mkdir(.okf) error = %v", err)
	}
	documentSessionWriteFile(t, filepath.Join(metadata, "private"), "opaque")
	if err := os.Chmod(metadata, 0); err != nil {
		t.Fatalf("Chmod(.okf) error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(metadata, 0o700) })

	// Act.
	session, err := OpenDocumentSessionContext(context.Background(), target, "")
	if session != nil {
		defer session.Close()
	}

	// Assert.
	if err != nil {
		t.Fatalf("OpenDocumentSessionContext() accessed root .okf metadata: %v", err)
	}
}

type documentSessionRecoveryFixture struct {
	id       string
	manifest documentTransactionManifest
	names    map[string]string
}

type documentSessionRecoveryArtifact struct {
	name string
	info os.FileInfo
	data []byte
}

func documentSessionCreateV2RecoveryState(
	t *testing.T,
	root string,
	target string,
	mode fs.FileMode,
	original string,
	published string,
	state documentV2RecoveryState,
) documentSessionRecoveryFixture {
	t.Helper()
	target = filepath.ToSlash(target)
	directoryContract := path.Dir(target)
	if directoryContract == "." {
		directoryContract = ""
	}
	directory := filepath.Join(root, filepath.FromSlash(directoryContract))
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := documentTransactionManifest{
		Format:          documentTransactionFormat,
		Target:          target,
		Mode:            uint32(mode),
		OriginalSize:    uint64(len(original)),
		OriginalSHA256:  documentDigest([]byte(original)),
		PublishedSize:   uint64(len(published)),
		PublishedSHA256: documentDigest([]byte(published)),
	}
	manifest.Stage = pathJoin(
		directoryContract,
		documentBoundArtifactName("stage", target, mode, []byte(published)),
	)
	manifest.NewInstall = pathJoin(
		directoryContract,
		documentBoundArtifactName("new-install", target, mode, []byte(published)),
	)
	manifest.Anchor = pathJoin(
		directoryContract,
		documentRollbackAnchorName(target, mode, []byte(original), 0),
	)
	manifest.RestoreInstall = pathJoin(
		directoryContract,
		documentBoundArtifactName("restore-install", target, mode, []byte(original)),
	)
	manifest.Discard = pathJoin(
		directoryContract,
		documentBoundArtifactName("discard", target, mode, []byte(published)),
	)
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	id := documentManifestDigest(manifestData)
	names := map[string]string{
		"manifest": documentArtifactName("manifest", id),
		"witness":  documentOriginalWitnessName(target, mode, []byte(original)),
		"backup":   documentBoundArtifactName("backup", target, mode, []byte(original)),
		"stage":    path.Base(manifest.Stage),
	}
	paths := map[string]string{
		"manifest": filepath.Join(directory, names["manifest"]),
		"witness":  filepath.Join(directory, names["witness"]),
		"backup":   filepath.Join(directory, names["backup"]),
		"stage":    filepath.Join(root, filepath.FromSlash(manifest.Stage)),
	}
	documentSessionWriteFile(t, paths["manifest"], string(manifestData))
	if err := os.Chmod(paths["manifest"], 0o600); err != nil {
		t.Fatal(err)
	}
	documentSessionWriteFile(t, paths["witness"], original)
	documentSessionWriteFile(t, paths["backup"], original)
	documentSessionWriteFile(t, paths["stage"], published)
	for _, kind := range []string{"witness", "backup", "stage"} {
		if err := os.Chmod(paths[kind], mode); err != nil {
			t.Fatal(err)
		}
	}
	link := func(source, destination string) {
		t.Helper()
		if err := os.Link(source, destination); err != nil {
			t.Fatal(err)
		}
	}
	if state.newInstall {
		names["new-install"] = path.Base(manifest.NewInstall)
		paths["new-install"] = filepath.Join(root, filepath.FromSlash(manifest.NewInstall))
		link(paths["stage"], paths["new-install"])
	}
	if state.anchor {
		names[documentRollbackAnchorProtocolKind(0)] = path.Base(manifest.Anchor)
		paths[documentRollbackAnchorProtocolKind(0)] = filepath.Join(root, filepath.FromSlash(manifest.Anchor))
		documentSessionWriteFile(t, paths[documentRollbackAnchorProtocolKind(0)], original)
		if err := os.Chmod(paths[documentRollbackAnchorProtocolKind(0)], mode); err != nil {
			t.Fatal(err)
		}
	}
	if state.restoreInstall {
		names["restore-install"] = path.Base(manifest.RestoreInstall)
		paths["restore-install"] = filepath.Join(root, filepath.FromSlash(manifest.RestoreInstall))
		link(paths[documentRollbackAnchorProtocolKind(0)], paths["restore-install"])
	}
	if state.discard {
		names["discard"] = path.Base(manifest.Discard)
		paths["discard"] = filepath.Join(root, filepath.FromSlash(manifest.Discard))
		link(paths["stage"], paths["discard"])
	}
	if state.claim {
		names["claim"] = documentArtifactName("claim", id)
		paths["claim"] = filepath.Join(directory, names["claim"])
		link(paths["witness"], paths["claim"])
	}
	targetPath := filepath.Join(root, filepath.FromSlash(target))
	if err := os.Remove(targetPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	switch state.target {
	case documentV2TargetWitness:
		link(paths["witness"], targetPath)
	case documentV2TargetStage:
		link(paths["stage"], targetPath)
	case documentV2TargetAnchor:
		link(paths[documentRollbackAnchorProtocolKind(0)], targetPath)
	case documentV2TargetMissing:
	default:
		documentSessionWriteFile(t, targetPath, "foreign\n")
	}
	return documentSessionRecoveryFixture{id: id, manifest: manifest, names: names}
}

func documentSessionLinkOriginalRecoveryBackup(
	t *testing.T,
	root string,
	target string,
	fixture documentSessionRecoveryFixture,
) {
	t.Helper()
	backup, present := fixture.names["backup"]
	if !present {
		t.Fatal("recovery fixture has no backup")
	}
	backupPath := filepath.Join(root, filepath.Dir(filepath.FromSlash(fixture.manifest.Target)), backup)
	targetInfo, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	backupInfo, err := os.Lstat(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(targetInfo, backupInfo) {
		t.Fatal("original target and durable backup must be independent inodes")
	}
}

func documentSessionLinkCommittedRecoveryAliases(
	t *testing.T,
	root string,
	target string,
	fixture documentSessionRecoveryFixture,
) {
	t.Helper()
	directory := filepath.Join(root, filepath.Dir(filepath.FromSlash(fixture.manifest.Target)))
	stagePath := filepath.Join(directory, fixture.names["stage"])
	if err := os.Remove(stagePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(target, stagePath); err != nil {
		t.Fatalf("Link(published target, stage) error = %v", err)
	}
	backupPath := filepath.Join(directory, fixture.names["backup"])
	claimPath := filepath.Join(directory, fixture.names["claim"])
	witnessPath := filepath.Join(directory, fixture.names["witness"])
	if err := os.Remove(witnessPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(claimPath, witnessPath); err != nil {
		t.Fatalf("Link(original claim, witness) error = %v", err)
	}
	backupInfo, err := os.Lstat(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	claimInfo, err := os.Lstat(claimPath)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(backupInfo, claimInfo) {
		t.Fatal("durable backup and original claim must be independent inodes")
	}
	witnessInfo, err := os.Lstat(witnessPath)
	if err != nil || !os.SameFile(witnessInfo, claimInfo) {
		t.Fatalf("identity witness does not alias original claim: witness=%v claim=%v error=%v", witnessInfo, claimInfo, err)
	}
}

func documentSessionCaptureRecoveryArtifacts(
	t *testing.T,
	root string,
	fixture documentSessionRecoveryFixture,
) map[string]documentSessionRecoveryArtifact {
	t.Helper()
	captured := make(map[string]documentSessionRecoveryArtifact, len(fixture.names))
	directory := filepath.Dir(filepath.FromSlash(fixture.manifest.Target))
	for kind, name := range fixture.names {
		relative := filepath.Join(directory, name)
		filename := filepath.Join(root, relative)
		info, err := os.Lstat(filename)
		if err != nil {
			t.Fatalf("Lstat(%s recovery artifact) error = %v", kind, err)
		}
		data, err := os.ReadFile(filename)
		if err != nil {
			t.Fatalf("ReadFile(%s recovery artifact) error = %v", kind, err)
		}
		captured[kind] = documentSessionRecoveryArtifact{
			name: filepath.ToSlash(relative),
			info: info,
			data: append([]byte(nil), data...),
		}
	}
	return captured
}

func documentSessionAssertRecoveryArtifactsUnchanged(
	t *testing.T,
	root string,
	captured map[string]documentSessionRecoveryArtifact,
) {
	t.Helper()
	for kind, expected := range captured {
		filename := filepath.Join(root, expected.name)
		info, err := os.Lstat(filename)
		if err != nil {
			t.Fatalf("Lstat(%s recovery artifact) error = %v", kind, err)
		}
		if !os.SameFile(expected.info, info) {
			t.Fatalf("%s recovery artifact identity changed", kind)
		}
		data, err := os.ReadFile(filename)
		if err != nil || !bytes.Equal(data, expected.data) {
			t.Fatalf("%s recovery artifact bytes = %q, error = %v; want %q", kind, data, err, expected.data)
		}
	}
}

func documentSessionCreateRecoveryGroup(
	t *testing.T,
	root string,
	target string,
	mode fs.FileMode,
	original string,
	published string,
	kinds ...string,
) documentSessionRecoveryFixture {
	t.Helper()
	target = filepath.ToSlash(target)
	directoryContract := path.Dir(target)
	if directoryContract == "." {
		directoryContract = ""
	}
	manifest := documentTransactionManifest{
		Format:          documentTransactionFormat,
		Target:          target,
		Mode:            uint32(mode),
		OriginalSize:    uint64(len(original)),
		OriginalSHA256:  documentDigest([]byte(original)),
		PublishedSize:   uint64(len(published)),
		PublishedSHA256: documentDigest([]byte(published)),
	}
	manifest.Stage = pathJoin(
		directoryContract,
		documentBoundArtifactName("stage", target, mode, []byte(published)),
	)
	manifest.NewInstall = pathJoin(
		directoryContract,
		documentBoundArtifactName("new-install", target, mode, []byte(published)),
	)
	manifest.Anchor = pathJoin(
		directoryContract,
		documentRollbackAnchorName(target, mode, []byte(original), 0),
	)
	manifest.RestoreInstall = pathJoin(
		directoryContract,
		documentBoundArtifactName("restore-install", target, mode, []byte(original)),
	)
	manifest.Discard = pathJoin(
		directoryContract,
		documentBoundArtifactName("discard", target, mode, []byte(published)),
	)
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("json.Marshal(manifest) error = %v", err)
	}
	id := documentManifestDigest(manifestData)
	directory := filepath.Join(root, filepath.Dir(filepath.FromSlash(target)))
	requested := make(map[string]struct{}, len(kinds)+1)
	for _, kind := range kinds {
		switch kind {
		case "manifest", "backup", "claim", "stage", "witness",
			documentRollbackAnchorProtocolKind(0),
			documentRollbackAnchorProtocolKind(1):
		default:
			t.Fatalf("unsupported recovery artifact kind %q", kind)
		}
		requested[kind] = struct{}{}
	}
	if _, hasManifest := requested["manifest"]; hasManifest {
		requested["witness"] = struct{}{}
	}
	names := make(map[string]string)
	orderedKinds := []string{
		"manifest",
		"backup",
		"claim",
		"stage",
		"witness",
		documentRollbackAnchorProtocolKind(0),
		documentRollbackAnchorProtocolKind(1),
	}
	for _, kind := range orderedKinds {
		if _, present := requested[kind]; !present || kind == "witness" {
			continue
		}
		name := documentArtifactName(kind, id)
		switch kind {
		case "backup":
			name = documentBoundArtifactName(
				"backup",
				target,
				mode,
				[]byte(original),
			)
		case "stage":
			name = documentBoundArtifactName(
				"stage",
				target,
				mode,
				[]byte(published),
			)
		case documentRollbackAnchorProtocolKind(0):
			name = documentRollbackAnchorName(
				target,
				mode,
				[]byte(original),
				0,
			)
		case documentRollbackAnchorProtocolKind(1):
			name = documentRollbackAnchorName(
				target,
				mode,
				[]byte(original),
				1,
			)
		}
		names[kind] = name
		var data []byte
		artifactMode := mode
		switch kind {
		case "manifest":
			data = manifestData
			artifactMode = 0o600
		case "backup", "claim",
			documentRollbackAnchorProtocolKind(0),
			documentRollbackAnchorProtocolKind(1):
			data = []byte(original)
		case "stage":
			data = []byte(published)
		default:
			t.Fatalf("unsupported recovery artifact kind %q", kind)
		}
		documentSessionWriteFile(t, filepath.Join(directory, name), string(data))
		if err := os.Chmod(filepath.Join(directory, name), artifactMode); err != nil {
			t.Fatalf("Chmod(%s artifact) error = %v", kind, err)
		}
	}
	if _, hasWitness := requested["witness"]; hasWitness {
		name := documentOriginalWitnessName(target, mode, []byte(original))
		names["witness"] = name
		witnessPath := filepath.Join(directory, name)
		sourcePath := filepath.Join(root, filepath.FromSlash(target))
		if claimName := names["claim"]; claimName != "" {
			sourcePath = filepath.Join(directory, claimName)
		}
		sourceInfo, sourceErr := os.Lstat(sourcePath)
		if sourceErr == nil &&
			sourceInfo.Mode().IsRegular() &&
			publicationPreservedMode(sourceInfo.Mode()) == publicationPreservedMode(mode) {
			sourceData, readErr := os.ReadFile(sourcePath)
			if readErr == nil && bytes.Equal(sourceData, []byte(original)) {
				if witnessInfo, witnessErr := os.Lstat(witnessPath); witnessErr == nil {
					if !os.SameFile(sourceInfo, witnessInfo) {
						t.Fatalf("existing witness does not alias %s", filepath.Base(sourcePath))
					}
				} else if !errors.Is(witnessErr, fs.ErrNotExist) {
					t.Fatalf("Lstat(existing witness) error = %v", witnessErr)
				} else if err := os.Link(sourcePath, witnessPath); err != nil {
					t.Fatalf("Link(%s witness) error = %v", filepath.Base(sourcePath), err)
				}
			} else {
				documentSessionWriteFile(t, witnessPath, original)
			}
		} else {
			documentSessionWriteFile(t, witnessPath, original)
		}
		if err := os.Chmod(witnessPath, mode); err != nil {
			t.Fatalf("Chmod(witness artifact) error = %v", err)
		}
	}
	return documentSessionRecoveryFixture{id: id, manifest: manifest, names: names}
}

func documentSessionSingleRecoveryArtifact(t *testing.T, root, wantKind string) string {
	t.Helper()
	found := documentSessionRecoveryArtifactPaths(t, root, wantKind)
	if len(found) == 0 {
		t.Fatalf("%s artifact is missing under %s", wantKind, root)
	}
	if len(found) != 1 {
		t.Fatalf("multiple %s artifacts: %q", wantKind, found)
	}
	return found[0]
}

func documentSessionRecoveryArtifactPaths(t *testing.T, root, wantKind string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(root, func(filename string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		kind, _, ok := parseDocumentArtifactName(entry.Name())
		if !ok || kind != wantKind {
			return nil
		}
		found = append(found, filename)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir(%s artifact) error = %v", wantKind, err)
	}
	return found
}

func documentSessionAtomicReplace(
	t *testing.T,
	target string,
	content string,
	mode fs.FileMode,
) {
	t.Helper()
	replacement := filepath.Join(
		filepath.Dir(target),
		".document-session-foreign-"+filepath.Base(target),
	)
	if err := os.WriteFile(replacement, []byte(content), mode.Perm()); err != nil {
		t.Fatalf("WriteFile(replacement) error = %v", err)
	}
	if err := os.Chmod(replacement, mode); err != nil {
		t.Fatalf("Chmod(replacement) error = %v", err)
	}
	if err := os.Rename(replacement, target); err != nil {
		t.Fatalf("Rename(replacement, target) error = %v", err)
	}
}

func documentSessionHasArtifacts(t *testing.T, root string) bool {
	t.Helper()
	found := false
	err := filepath.WalkDir(root, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if isDocumentTransactionReservedPath(entry.Name()) {
			found = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir(%q) error = %v", root, err)
	}
	return found
}

func documentSessionAssertNoPublicationArtifacts(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if isReservedTransactionPath(entry.Name()) {
			t.Errorf("publication artifact remains after recovery: %q", filename)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir(%q) error = %v", root, err)
	}
}

func documentSessionPublicationArtifactDebug(t *testing.T, root string) string {
	t.Helper()
	var entries []string
	err := filepath.WalkDir(root, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !isReservedTransactionPath(entry.Name()) {
			return nil
		}
		relative, err := filepath.Rel(root, filename)
		if err != nil {
			return err
		}
		info, err := os.Lstat(filename)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			entries = append(entries, fmt.Sprintf("%s mode=%v", filepath.ToSlash(relative), info.Mode()))
			return nil
		}
		data, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		entries = append(entries, fmt.Sprintf(
			"%s mode=%v size=%d sha256=%s bytes=%q",
			filepath.ToSlash(relative),
			publicationPreservedMode(info.Mode()),
			len(data),
			documentDigest(data),
			data,
		))
		return nil
	})
	if err != nil {
		t.Fatalf("debug publication artifacts: %v", err)
	}
	return strings.Join(entries, "; ")
}

type documentSessionTreeEntry struct {
	info os.FileInfo
	data string
	link string
}

func documentSessionCaptureTree(t *testing.T, root string) map[string]documentSessionTreeEntry {
	t.Helper()
	captured := make(map[string]documentSessionTreeEntry)
	err := filepath.WalkDir(root, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, filename)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		capturedEntry := documentSessionTreeEntry{info: info}
		if entry.IsDir() {
			captured[relative] = capturedEntry
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(filename)
			if err != nil {
				return err
			}
			capturedEntry.link = target
			captured[relative] = capturedEntry
			return nil
		}
		if !info.Mode().IsRegular() {
			captured[relative] = capturedEntry
			return nil
		}
		data, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		capturedEntry.data = string(data)
		captured[relative] = capturedEntry
		return nil
	})
	if err != nil {
		t.Fatalf("capture tree %q: %v", root, err)
	}
	return captured
}

func documentSessionAssertTreeSnapshotEqual(
	t *testing.T,
	before map[string]documentSessionTreeEntry,
	after map[string]documentSessionTreeEntry,
) {
	t.Helper()
	if len(after) != len(before) {
		beforeNames := make([]string, 0, len(before))
		afterNames := make([]string, 0, len(after))
		for name := range before {
			beforeNames = append(beforeNames, name)
		}
		for name := range after {
			afterNames = append(afterNames, name)
		}
		slices.Sort(beforeNames)
		slices.Sort(afterNames)
		t.Fatalf(
			"tree entry count changed: before=%d %q after=%d %q",
			len(before),
			beforeNames,
			len(after),
			afterNames,
		)
	}
	for name, left := range before {
		right, present := after[name]
		if !present {
			t.Fatalf("tree entry %q disappeared", name)
		}
		if left.info == nil || right.info == nil ||
			!os.SameFile(left.info, right.info) ||
			left.info.Mode() != right.info.Mode() ||
			left.info.Size() != right.info.Size() ||
			!left.info.ModTime().Equal(right.info.ModTime()) ||
			left.data != right.data ||
			left.link != right.link {
			t.Fatalf(
				"tree entry %q changed:\nbefore mode=%v size=%d mod=%v data=%q link=%q\nafter  mode=%v size=%d mod=%v data=%q link=%q",
				name,
				left.info.Mode(),
				left.info.Size(),
				left.info.ModTime(),
				left.data,
				left.link,
				right.info.Mode(),
				right.info.Size(),
				right.info.ModTime(),
				right.data,
				right.link,
			)
		}
	}
}
