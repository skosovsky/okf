//go:build darwin || linux

package bundle

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestPublicationVacateOutcomeReceiptBoundaries(t *testing.T) {
	t.Parallel()

	t.Run("before rename leaves target untouched", func(t *testing.T) {
		// Arrange.
		parent, _, expected := newIndexV3DiscardFixture(t)
		cut := errors.New("cut before rename")
		hooks := publicationBarrierHooks{
			beforeVacate: func() error {
				return cut
			},
		}

		// Act.
		outcome, err := guardedVacatePublicationLeafWithOutcome(
			context.Background(),
			parent,
			"index.md",
			expected,
			"discard",
			hooks,
		)

		// Assert.
		if !errors.Is(err, cut) {
			t.Fatalf("guardedVacatePublicationLeafWithOutcome() error = %v, want cut", err)
		}
		if outcome.renamed || outcome.durableReceipt || outcome.claimed.created {
			t.Fatalf("outcome = %+v, want unrenamed without receipt or claim", outcome)
		}
		assertPublicationVacateTargetIdentity(t, parent, "index.md", expected.info)
		assertPublicationVacateAbsent(t, parent, "discard")
	})

	t.Run("after rename error compensates before receipt", func(t *testing.T) {
		// Arrange.
		parent, _, expected := newIndexV3DiscardFixture(t)
		cut := errors.New("cut after rename")
		hooks := publicationBarrierHooks{
			afterVacateRename: func() error {
				return cut
			},
		}

		// Act.
		outcome, err := guardedVacatePublicationLeafWithOutcome(
			context.Background(),
			parent,
			"index.md",
			expected,
			"discard",
			hooks,
		)

		// Assert.
		assertPublicationVacateCompensated(t, parent, expected, outcome, err, cut)
	})

	t.Run("compensation validator permits safe convergence despite wider drift", func(t *testing.T) {
		// Arrange.
		parent, _, expected := newIndexV3DiscardFixture(t)
		cut := errors.New("cut after rename")
		widerDrift := errors.New("wider evidence drift")
		drifted := false
		hooks := publicationBarrierHooks{
			validateScope: func(publicationScopePhase) error {
				if drifted {
					return widerDrift
				}
				return nil
			},
			validateCompensation: func() error {
				return nil
			},
			afterVacateRename: func() error {
				drifted = true
				return cut
			},
		}

		// Act.
		outcome, err := guardedVacatePublicationLeafWithOutcome(
			context.Background(),
			parent,
			"index.md",
			expected,
			"discard",
			hooks,
		)

		// Assert.
		if !errors.Is(err, widerDrift) {
			t.Fatalf("guardedVacatePublicationLeafWithOutcome() error = %v, want retained drift", err)
		}
		assertPublicationVacateCompensated(t, parent, expected, outcome, err, cut)
	})

	t.Run("directory sync error compensates before receipt", func(t *testing.T) {
		// Arrange.
		parent, _, expected := newIndexV3DiscardFixture(t)
		cut := errors.New("cut at directory sync")
		hooks := publicationBarrierHooks{
			directorySync: func(*os.Root) error {
				return cut
			},
		}

		// Act.
		outcome, err := guardedVacatePublicationLeafWithOutcome(
			context.Background(),
			parent,
			"index.md",
			expected,
			"discard",
			hooks,
		)

		// Assert.
		assertPublicationVacateCompensated(t, parent, expected, outcome, err, cut)
	})

	t.Run("post directory sync error retains durable receipt", func(t *testing.T) {
		// Arrange.
		parent, _, expected := newIndexV3DiscardFixture(t)
		cut := errors.New("cut after durable receipt")
		hooks := publicationBarrierHooks{
			afterVacate: func() error {
				return cut
			},
		}

		// Act.
		outcome, err := guardedVacatePublicationLeafWithOutcome(
			context.Background(),
			parent,
			"index.md",
			expected,
			"discard",
			hooks,
		)

		// Assert.
		if !errors.Is(err, cut) {
			t.Fatalf("guardedVacatePublicationLeafWithOutcome() error = %v, want cut", err)
		}
		if !outcome.renamed || !outcome.durableReceipt || !outcome.claimed.created {
			t.Fatalf("outcome = %+v, want renamed durable retained claim", outcome)
		}
		assertPublicationVacateAbsent(t, parent, "index.md")
		assertPublicationVacateTargetIdentity(t, parent, "discard", expected.info)
	})

	t.Run("blocked compensation preserves claim and occupant", func(t *testing.T) {
		// Arrange.
		parent, _, expected := newIndexV3DiscardFixture(t)
		cut := errors.New("cut with occupied target")
		var occupant os.FileInfo
		hooks := publicationBarrierHooks{
			afterVacateRename: func() error {
				var err error
				occupant, err = createIndexRollbackForeign(
					parent,
					"index.md",
					[]byte("foreign occupant\n"),
				)
				return errors.Join(cut, err)
			},
		}

		// Act.
		outcome, err := guardedVacatePublicationLeafWithOutcome(
			context.Background(),
			parent,
			"index.md",
			expected,
			"discard",
			hooks,
		)

		// Assert.
		if !errors.Is(err, cut) || !errors.Is(err, errPublicationConflict) {
			t.Fatalf("guardedVacatePublicationLeafWithOutcome() error = %v, want cut and conflict", err)
		}
		if !outcome.renamed || outcome.durableReceipt || !outcome.claimed.created {
			t.Fatalf("outcome = %+v, want pre-receipt retained claim", outcome)
		}
		assertPublicationVacateTargetIdentity(t, parent, "index.md", occupant)
		assertPublicationVacateTargetIdentity(t, parent, "discard", expected.info)
	})

	t.Run("drifted claim is never authorized as compensated target", func(t *testing.T) {
		// Arrange.
		parent, _, expected := newIndexV3DiscardFixture(t)
		var foreign os.FileInfo
		hooks := publicationBarrierHooks{
			afterVacateRename: func() error {
				if err := parent.Remove("discard"); err != nil {
					return err
				}
				var err error
				foreign, err = createIndexRollbackForeign(
					parent,
					"discard",
					[]byte("foreign discard\n"),
				)
				return err
			},
		}

		// Act.
		outcome, err := guardedVacatePublicationLeafWithOutcome(
			context.Background(),
			parent,
			"index.md",
			expected,
			"discard",
			hooks,
		)

		// Assert.
		if !errors.Is(err, errPublicationConflict) {
			t.Fatalf("guardedVacatePublicationLeafWithOutcome() error = %v, want conflict", err)
		}
		if !outcome.renamed || outcome.durableReceipt || !outcome.claimed.created {
			t.Fatalf("outcome = %+v, want retained drifted claim without receipt", outcome)
		}
		assertPublicationVacateAbsent(t, parent, "index.md")
		assertPublicationVacateTargetIdentity(t, parent, "discard", foreign)
	})

	t.Run("same identity payload drift is never compensated", func(t *testing.T) {
		// Arrange.
		parent, _, expected := newIndexV3DiscardFixture(t)
		hooks := publicationBarrierHooks{
			afterVacateRename: func() error {
				file, err := parent.OpenFile("discard", os.O_WRONLY|os.O_TRUNC, 0)
				if err != nil {
					return err
				}
				if _, err := file.Write([]byte("mutated discard\n")); err != nil {
					_ = file.Close()
					return err
				}
				return file.Close()
			},
		}

		// Act.
		outcome, err := guardedVacatePublicationLeafWithOutcome(
			context.Background(),
			parent,
			"index.md",
			expected,
			"discard",
			hooks,
		)

		// Assert.
		if !errors.Is(err, errPublicationConflict) {
			t.Fatalf("guardedVacatePublicationLeafWithOutcome() error = %v, want conflict", err)
		}
		if !outcome.renamed || outcome.durableReceipt || !outcome.claimed.created {
			t.Fatalf("outcome = %+v, want retained payload-drifted claim without receipt", outcome)
		}
		assertPublicationVacateAbsent(t, parent, "index.md")
		assertPublicationVacateTargetIdentity(t, parent, "discard", expected.info)
	})

	t.Run("detached parent rejects compensation and retains parked claim", func(t *testing.T) {
		// Arrange.
		root := t.TempDir()
		live := filepath.Join(root, "live")
		parked := filepath.Join(root, "parked")
		if err := os.Mkdir(live, 0o755); err != nil {
			t.Fatal(err)
		}
		parent, err := os.OpenRoot(live)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = parent.Close() })
		stage, err := createPublicationArtifact(
			context.Background(),
			parent,
			"stage",
			0o640,
			[]byte("# generated\n"),
			publicationBarrierHooks{},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := parent.Link("stage", "index.md"); err != nil {
			t.Fatal(err)
		}
		expected := publicationSpecFromBytes(stage.info, stage.data)
		originalParent, err := os.Lstat(live)
		if err != nil {
			t.Fatal(err)
		}
		detached := errors.New("publication parent detached")
		directorySyncs := 0
		validateParent := func() error {
			current, err := os.Lstat(live)
			if err != nil ||
				current == nil ||
				!os.SameFile(originalParent, current) {
				return errors.Join(detached, err)
			}
			return nil
		}
		hooks := publicationBarrierHooks{
			validateScope: func(publicationScopePhase) error {
				return validateParent()
			},
			validateCompensation: validateParent,
			directorySync: func(*os.Root) error {
				directorySyncs++
				if err := os.Rename(live, parked); err != nil {
					return err
				}
				if err := os.Mkdir(live, 0o755); err != nil {
					return err
				}
				return os.WriteFile(
					filepath.Join(live, "foreign"),
					[]byte("replacement\n"),
					0o600,
				)
			},
		}

		// Act.
		outcome, err := guardedVacatePublicationLeafWithOutcome(
			context.Background(),
			parent,
			"index.md",
			expected,
			"discard",
			hooks,
		)

		// Assert.
		if !errors.Is(err, detached) ||
			!errors.Is(err, errPublicationScopeInvalid) ||
			!errors.Is(err, errPublicationConflict) {
			t.Fatalf(
				"guardedVacatePublicationLeafWithOutcome() error = %v, want detached scope conflict",
				err,
			)
		}
		if directorySyncs != 1 {
			t.Fatalf("directory sync calls = %d, want one pre-compensation attack", directorySyncs)
		}
		if !outcome.renamed || outcome.durableReceipt || !outcome.claimed.created {
			t.Fatalf("outcome = %+v, want pre-receipt retained parked claim", outcome)
		}
		assertPublicationVacateAbsent(t, parent, "index.md")
		assertPublicationVacateTargetIdentity(t, parent, "discard", expected.info)
		assertPublicationVacateTargetIdentity(t, parent, "stage", expected.info)
		if _, err := os.Lstat(filepath.Join(live, "index.md")); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("replacement target error = %v, want absent", err)
		}
		if _, err := os.Lstat(filepath.Join(live, "discard")); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("replacement claim error = %v, want absent", err)
		}
		replacement, err := os.ReadFile(filepath.Join(live, "foreign"))
		if err != nil || string(replacement) != "replacement\n" {
			t.Fatalf("replacement tree changed: bytes=%q error=%v", replacement, err)
		}
		parkedClaim, err := os.Lstat(filepath.Join(parked, "discard"))
		if err != nil || !os.SameFile(expected.info, parkedClaim) {
			t.Fatalf("parked claim changed: info=%v error=%v", parkedClaim, err)
		}
	})
}

func assertPublicationVacateCompensated(
	t *testing.T,
	parent *os.Root,
	expected publicationFileSpec,
	outcome publicationVacateOutcome,
	err error,
	cut error,
) {
	t.Helper()
	if !errors.Is(err, cut) || !errors.Is(err, errPublicationConflict) {
		t.Fatalf("guardedVacatePublicationLeafWithOutcome() error = %v, want cut and conflict", err)
	}
	if !outcome.renamed || outcome.durableReceipt || outcome.claimed.created {
		t.Fatalf("outcome = %+v, want compensated pre-receipt rename", outcome)
	}
	assertPublicationVacateTargetIdentity(t, parent, "index.md", expected.info)
	assertPublicationVacateAbsent(t, parent, "discard")
}

func assertPublicationVacateTargetIdentity(
	t *testing.T,
	parent *os.Root,
	name string,
	expected os.FileInfo,
) {
	t.Helper()
	actual, err := parent.Lstat(name)
	if err != nil || expected == nil || !os.SameFile(expected, actual) {
		t.Fatalf("Lstat(%q) = %v, %v; want exact identity", name, actual, err)
	}
}

func assertPublicationVacateAbsent(t *testing.T, parent *os.Root, name string) {
	t.Helper()
	if _, err := parent.Lstat(name); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Lstat(%q) error = %v, want absent", name, err)
	}
}
