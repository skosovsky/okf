package fs

// Pinned-read close and path-rebind precedence.

import (
	"context"
	"errors"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/skosovsky/okf/store"
)

func TestActualPinnedReadCloseAndRebindPrecedence(t *testing.T) {
	for _, reader := range []string{"journal", "payload", "metadata", "provenance"} {
		for _, mode := range []struct {
			name     string
			closeErr bool
			mismatch bool
		}{
			{name: "close-only", closeErr: true},
			{name: "path-only", mismatch: true},
			{name: "close-and-path", closeErr: true, mismatch: true},
		} {
			t.Run(reader+"/"+mode.name, func(t *testing.T) {
				t.Parallel()

				// Arrange.
				root, s, _, next := stageRecoveryJournal(t, Config{})
				j := readOnlyPendingJournal(t, root)
				closeEIO := errors.New(reader + " close EIO")
				armed := false
				fired := false
				s.descriptorBarrier = func(phase string) error {
					var target string
					switch reader {
					case "journal":
						if phase != "journal_restat" {
							return nil
						}
						target, _ = journalPath(j.Receipt.RequestDigest)
					case "payload":
						if phase != "payload_restat" {
							return nil
						}
						target = path.Join(j.Stage, j.Files[0].Payload)
					case "metadata":
						if !strings.HasPrefix(phase, "metadata_restat:"+claimDirectory+"/") {
							return nil
						}
						target = strings.TrimPrefix(phase, "metadata_restat:")
					case "provenance":
						if phase != "provenance_restat:a.md" {
							return nil
						}
						target = "a.md"
					}
					if fired {
						return nil
					}
					fired = true
					if mode.mismatch {
						replaceRegularWithSameBytes(t, root, target)
					}
					armed = mode.closeErr
					return nil
				}
				if mode.closeErr {
					s.closeRead = func(file *os.File) error {
						actual := file.Close()
						if !armed {
							return actual
						}
						armed = false
						return errors.Join(closeEIO, actual)
					}
				}

				// Act.
				recoveryErr := s.recoverForTest(context.Background())
				var committed *store.CommittedError

				// Assert.
				if !fired || errors.Is(recoveryErr, context.Canceled) || errors.Is(recoveryErr, closeEIO) != mode.closeErr || errors.Is(recoveryErr, store.ErrStorageCorrupt) != mode.mismatch {
					t.Fatalf("fired=%t recovery=%v close=%t corrupt=%t", fired, recoveryErr, errors.Is(recoveryErr, closeEIO), errors.Is(recoveryErr, store.ErrStorageCorrupt))
				}
				wantCommitted := reader == "payload"
				if errors.As(recoveryErr, &committed) != wantCommitted || wantCommitted && committed.Receipt().ResultRevision != next.Revision() {
					t.Fatalf("recovery=%v committed=%v want_committed=%t", recoveryErr, committed, wantCommitted)
				}
				if mode.closeErr && mode.mismatch && strings.Index(recoveryErr.Error(), closeEIO.Error()) > strings.Index(recoveryErr.Error(), store.ErrStorageCorrupt.Error()) {
					t.Fatalf("recovery=%v, want close I/O ordered before structural corruption", recoveryErr)
				}
			})
		}
	}
}
