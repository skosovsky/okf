package bundle

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestIndexPublicationLockUsesPhysicalIdentityAcrossRootAliases(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
	ready := make(chan struct{})
	release := make(chan struct{})
	winnerDone := make(chan error, 1)
	go func() {
		_, err := regenerateIndexesWithTestHooks(root, nil, "", indexPublishHooks{
			afterIndexLock: func() error {
				close(ready)
				<-release
				return nil
			},
		})

		winnerDone <- err
	}()
	<-ready
	alias := root + string(os.PathSeparator) + "."

	// Act.
	written, err := RegenerateIndexes(alias)
	close(release)
	winnerErr := <-winnerDone

	// Assert.
	if written != nil || !errors.Is(err, ErrPublicationOwnershipConflict) {
		t.Fatalf("alias contender = %#v, %v; want physical-root conflict", written, err)
	}
	if winnerErr != nil {
		t.Fatalf("winner error = %v", winnerErr)
	}
	assertNoIndexTransactionArtifacts(t, root)
}

func TestIndexPublicationLockReleasesAfterHookErrorAndPanic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T, root string)
	}{
		{
			name: "error",
			run: func(t *testing.T, root string) {
				t.Helper()
				injected := errors.New("injected after lock")
				written, err := regenerateIndexesWithTestHooks(root, nil, "", indexPublishHooks{
					afterIndexLock: func() error { return injected },
				})

				if written != nil || !errors.Is(err, injected) {
					t.Fatalf("hook error result = %#v, %v; want injected", written, err)
				}
			},
		},
		{
			name: "panic",
			run: func(t *testing.T, root string) {
				t.Helper()
				func() {
					defer func() {
						if recovered := recover(); recovered != "injected after lock panic" {
							t.Fatalf("recovered = %#v, want injected panic", recovered)
						}
					}()
					_, _ = regenerateIndexesWithTestHooks(root, nil, "", indexPublishHooks{
						afterIndexLock: func() error {
							panic("injected after lock panic")
						},
					})

				}()
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")

			// Act.
			test.run(t, root)
			written, err := RegenerateIndexes(root)

			// Assert.
			if err != nil || len(written) == 0 {
				t.Fatalf("fresh regeneration = %#v, %v; want immediate success", written, err)
			}
			assertNoIndexTransactionArtifacts(t, root)
		})
	}
}

func TestIndexPublicationLockCoversSourceFinalization(t *testing.T) {
	t.Parallel()

	panicValue := &struct{ message string }{message: "injected panic"}
	injected := errors.New("injected early error")
	tests := []struct {
		name            string
		afterIndexLock  func() error
		wantErr         error
		wantPanic       any
		wantSuccess     bool
		panicOnFinalize bool
	}{
		{
			name:        "success",
			wantSuccess: true,
		},
		{
			name: "early error",
			afterIndexLock: func() error {
				return injected
			},
			wantErr: injected,
		},
		{
			name: "panic",
			afterIndexLock: func() error {
				panic(panicValue)
			},
			wantPanic: panicValue,
		},
		{
			name:            "finalizer panic",
			wantPanic:       panicValue,
			panicOnFinalize: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
			finalizing := make(chan struct{})
			allowSourceClose := make(chan struct{})
			sourceCloseAllowed := false
			defer func() {
				if !sourceCloseAllowed {
					close(allowSourceClose)
				}
			}()
			type result struct {
				written    []string
				err        error
				panicValue any
			}
			done := make(chan result, 1)
			go func() {
				outcome := result{}
				defer func() {
					outcome.panicValue = recover()
					done <- outcome
				}()
				outcome.written, outcome.err = regenerateIndexesWithTestHooks(
					root,
					nil,
					"",
					indexPublishHooks{
						afterIndexLock: test.afterIndexLock,
						beforeSourceClose: func() {
							close(finalizing)
							<-allowSourceClose
							if test.panicOnFinalize {
								panic(panicValue)
							}
						},
					})

			}()
			<-finalizing

			// Act. An invalid selector proves the contender cannot reach source
			// resolution while the winner is closing its pinned root.
			contenderWritten, contenderErr := RegenerateIndexesWithSelector(root, "9.9")

			// Assert while finalization is blocked.
			if contenderWritten != nil || !errors.Is(contenderErr, ErrPublicationOwnershipConflict) ||
				errors.Is(contenderErr, ErrUnsupportedVersionSelector) {
				t.Fatalf(
					"contender during source finalization = %#v, %v; want ownership conflict",
					contenderWritten,
					contenderErr,
				)
			}
			assertNoIndexTransactionArtifacts(t, root)

			sourceCloseAllowed = true
			close(allowSourceClose)
			outcome := <-done
			switch {
			case test.wantSuccess:
				if outcome.err != nil || outcome.panicValue != nil || len(outcome.written) == 0 {
					t.Fatalf(
						"winner = %#v, %v, panic %#v; want success",
						outcome.written,
						outcome.err,
						outcome.panicValue,
					)
				}
			case test.wantErr != nil:
				if outcome.written != nil || !errors.Is(outcome.err, test.wantErr) ||
					outcome.panicValue != nil {
					t.Fatalf(
						"winner = %#v, %v, panic %#v; want error %v",
						outcome.written,
						outcome.err,
						outcome.panicValue,
						test.wantErr,
					)
				}
			default:
				if outcome.written != nil || outcome.err != nil || outcome.panicValue != test.wantPanic {
					t.Fatalf(
						"winner = %#v, %v, panic %#v; want panic %#v",
						outcome.written,
						outcome.err,
						outcome.panicValue,
						test.wantPanic,
					)
				}
			}

			freshWritten, freshErr := RegenerateIndexes(root)
			if freshErr != nil || len(freshWritten) == 0 {
				t.Fatalf(
					"regeneration after source close and lock release = %#v, %v; want success",
					freshWritten,
					freshErr,
				)
			}
			assertNoIndexTransactionArtifacts(t, root)
		})
	}
}

func TestIndexLocalPublicationLockReleaseIsABASafe(t *testing.T) {
	t.Parallel()

	// Arrange.
	identity := "test:" + filepath.ToSlash(t.TempDir())
	releaseFirst, err := tryAcquireLocalPublicationLock(identity)
	if err != nil {
		t.Fatal(err)
	}
	if releaseUnexpected, conflictErr := tryAcquireLocalPublicationLock(identity); releaseUnexpected != nil ||
		!errors.Is(conflictErr, ErrPublicationOwnershipConflict) {
		t.Fatalf("second acquisition release = %t, error = %v; want conflict", releaseUnexpected != nil, conflictErr)
	}

	// Act.
	releaseFirst()
	releaseCurrent, err := tryAcquireLocalPublicationLock(identity)
	if err != nil {
		t.Fatal(err)
	}
	releaseFirst()
	releaseUnexpected, conflictErr := tryAcquireLocalPublicationLock(identity)

	// Assert.
	if releaseUnexpected != nil || !errors.Is(conflictErr, ErrPublicationOwnershipConflict) {
		t.Fatalf(
			"stale release removed current owner: release = %t, error = %v",
			releaseUnexpected != nil,
			conflictErr,
		)
	}
	releaseCurrent()
	releaseFinal, err := tryAcquireLocalPublicationLock(identity)
	if err != nil {
		t.Fatalf("final acquisition error = %v", err)
	}
	releaseFinal()
}

func TestIndexRegenerationConflictPrecedesBundleReadsAndWrites(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
	ready := make(chan struct{})
	release := make(chan struct{})
	winnerDone := make(chan error, 1)
	go func() {
		_, err := regenerateIndexesWithTestHooks(root, nil, "", indexPublishHooks{
			afterIndexLock: func() error {
				close(ready)
				<-release
				return nil
			},
		})

		winnerDone <- err
	}()
	<-ready

	// Act.
	written, err := RegenerateIndexesWithSelector(root, "9.9")

	// Assert before the winner is released.
	if written != nil || !errors.Is(err, ErrPublicationOwnershipConflict) ||
		errors.Is(err, ErrUnsupportedVersionSelector) {
		t.Fatalf("contender = %#v, %v; want lock conflict before selector/source work", written, err)
	}
	assertPathDoesNotExist(t, filepath.Join(root, "nested", indexFilename))
	assertPathDoesNotExist(t, filepath.Join(root, indexFilename))
	assertNoIndexTransactionArtifacts(t, root)

	close(release)
	if winnerErr := <-winnerDone; winnerErr != nil {
		t.Fatalf("winner error = %v", winnerErr)
	}
}

func TestIndexPublicationLockRejectsIdenticalContenderWithExistingIndexes(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeIndexDoc(t, root, "x/a/one.md", "Note", "One", "")
	writeIndexDoc(t, root, "x/b/two.md", "Note", "Two", "")
	if written, err := RegenerateIndexes(root); err != nil || len(written) == 0 {
		t.Fatalf("initial regeneration = %#v, %v", written, err)
	}
	ready := make(chan struct{})
	release := make(chan struct{})
	winnerDone := make(chan error, 1)
	go func() {
		_, err := regenerateIndexesWithTestHooks(root, nil, "", indexPublishHooks{
			afterIndexLock: func() error {
				close(ready)
				<-release
				return nil
			},
		})

		winnerDone <- err
	}()
	<-ready

	// Act.
	written, err := RegenerateIndexes(root)
	close(release)
	winnerErr := <-winnerDone

	// Assert.
	if written != nil || !errors.Is(err, ErrPublicationOwnershipConflict) {
		t.Fatalf("identical contender = %#v, %v; want conflict", written, err)
	}
	if winnerErr != nil {
		t.Fatalf("winner error = %v", winnerErr)
	}
	assertNoIndexTransactionArtifacts(t, root)
	if freshWritten, freshErr := RegenerateIndexes(root); freshErr != nil || len(freshWritten) == 0 {
		t.Fatalf("fresh regeneration = %#v, %v; want success", freshWritten, freshErr)
	}
}
