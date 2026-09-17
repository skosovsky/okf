package fs

// Canonical artifact naming, role proof, and read-growth I/O.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/skosovsky/okf/store"
)

func TestCanonicalTempArtifactNameIsExact(t *testing.T) {
	for _, test := range []struct {
		name        string
		nanos       int64
		attempt     uint64
		want        string
		wantAllowed bool
	}{
		{name: "minimum", nanos: 1, attempt: 0, want: ".okf-tmp-1-0", wantAllowed: true},
		{name: "attempt 99", nanos: 9, attempt: 99, want: ".okf-tmp-9-99", wantAllowed: true},
		{name: "zero nanos", nanos: 0, attempt: 0},
		{name: "negative nanos", nanos: -1, attempt: 0},
		{name: "attempt 100", nanos: 1, attempt: 100},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange/Act.
			got, err := formatCanonicalTempArtifactName(test.nanos, test.attempt)
			parsedNanos, parsedAttempt, parsed := parseCanonicalTempArtifactName(got)

			// Assert.
			if test.wantAllowed {
				if err != nil || got != test.want || !parsed || parsedNanos != test.nanos || parsedAttempt != test.attempt {
					t.Fatalf("format=%q err=%v parse=%d/%d/%t", got, err, parsedNanos, parsedAttempt, parsed)
				}
			} else if err == nil || got != "" || parsed {
				t.Fatalf("invalid format=%q err=%v parsed=%t", got, err, parsed)
			}
		})
	}
	for _, impossible := range []string{
		".okf-tmp-0-0",
		".okf-tmp-01-0",
		".okf-tmp-+1-0",
		".okf-tmp--1-0",
		".okf-tmp-1-00",
		".okf-tmp-1-100",
		".okf-tmp-9223372036854775808-0",
		".okf-tmp-1-18446744073709551616",
	} {
		t.Run("reject/"+impossible, func(t *testing.T) {
			// Arrange/Act.
			_, _, ok := parseCanonicalTempArtifactName(impossible)

			// Assert.
			if ok {
				t.Fatalf("producer-impossible name accepted: %q", impossible)
			}
		})
	}
}

func TestInvalidClockCannotCreateTempArtifact(t *testing.T) {
	for _, nanos := range []int64{0, -1} {
		t.Run(time.Unix(0, nanos).String(), func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			s.clockNow = func() time.Time { return time.Unix(0, nanos) }

			// Act.
			err := s.writeDurableAtForTest(context.Background(), "clock-target.bin", []byte("payload"), 0o600)
			entries, readErr := os.ReadDir(filepath.Join(root, filepath.FromSlash(temporaryDirectory)))
			_, targetErr := os.Lstat(filepath.Join(root, "clock-target.bin"))

			// Assert.
			if err == nil || errors.Is(err, store.ErrStorageCorrupt) || readErr != nil && !errors.Is(readErr, os.ErrNotExist) || len(entries) != 0 || !errors.Is(targetErr, os.ErrNotExist) {
				t.Fatalf("write=%v read=%v entries=%v target=%v", err, readErr, entries, targetErr)
			}
		})
	}
}

func TestCompleteForgedRoleProofCannotReachAlias(t *testing.T) {
	operation := "sha256:" + strings.Repeat("9", 64)
	for _, test := range []struct {
		name   string
		role   artifactRole
		target string
		source string
	}{
		{name: "journal visible target", role: claimJournal, target: "visible-victim.md", source: path.Join(temporaryDirectory, ".okf-tmp-990000001-1")},
		{name: "payload visible target", role: claimPayload, target: "visible-victim.md", source: path.Join(temporaryDirectory, ".okf-tmp-990000002-2")},
		{name: "scratch zero nanos", role: claimScratch, target: path.Join(temporaryDirectory, ".okf-tmp-0-0"), source: path.Join(temporaryDirectory, ".okf-tmp-0-0")},
		{name: "scratch producer-impossible", role: claimScratch, target: path.Join(temporaryDirectory, ".okf-tmp-01-0"), source: path.Join(temporaryDirectory, ".okf-tmp-01-0")},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange: install the hostile binding and witness directly under the
			// forged key. No legitimate key or prepare path ever exists.
			root, s := adversarialStore(t, Config{})
			victim := "visible-victim.md"
			absoluteVictim := filepath.Join(root, victim)
			if err := os.WriteFile(absoluteVictim, []byte("visible victim"), 0o640); err != nil {
				t.Fatal(err)
			}
			seenAliases := map[string]bool{}
			for _, alias := range []string{test.source, test.target} {
				if seenAliases[alias] {
					continue
				}
				seenAliases[alias] = true
				absolute := filepath.Join(root, filepath.FromSlash(alias))
				if absolute == absoluteVictim {
					continue
				}
				if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(absoluteVictim, absolute); err != nil {
					t.Fatal(err)
				}
			}
			victimInfo, _ := os.Lstat(absoluteVictim)
			identity, _ := fileIdentityKey(victimInfo)
			key, err := newArtifactClaimKey(operation, test.target, test.role)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(claimDirectory)), 0o700); err != nil {
				t.Fatal(err)
			}
			record := directoryWitnessRecord{Version: 1, Operation: key.Operation, Path: key.Path, Role: key.Role, Identity: identity, Source: test.source}
			raw, _ := json.Marshal(record)
			if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(key.bindingPath())), raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Link(absoluteVictim, filepath.Join(root, filepath.FromSlash(key.witnessPath(false)))); err != nil {
				t.Fatal(err)
			}
			if path.Base(key.bindingPath()) != key.stem()+".binding.json" {
				t.Fatalf("binding basename=%q stem=%q", path.Base(key.bindingPath()), key.stem())
			}
			paths := []string{victim, test.source, test.target, key.bindingPath(), key.witnessPath(false)}
			before := exactFileStates(t, root, paths)
			var barriers []string
			s.descriptorBarrier = func(phase string) error { barriers = append(barriers, phase); return nil }

			// Act.
			var cleanupErr error
			if test.role == claimScratch {
				cleanupErr = s.cleanupScratchInventory(context.Background())
			} else {
				cleanupErr = s.compensateUndurableRegularBindings(context.Background())
			}
			after := exactFileStates(t, root, paths)

			// Assert.
			for _, phase := range barriers {
				if !strings.HasPrefix(phase, "metadata_") {
					t.Fatalf("role validation reached post-binding phase %q", phase)
				}
			}
			if !errors.Is(cleanupErr, errArtifactClaimConflict) || len(barriers) == 0 || !reflect.DeepEqual(before, after) {
				t.Fatalf("cleanup=%v barriers=%v state=%v/%v", cleanupErr, barriers, before, after)
			}
		})
	}
}

func TestJournalBeforeReadGrowthPreservesErrorFacts(t *testing.T) {
	for _, test := range []struct {
		name       string
		grow       bool
		closeError bool
		seamError  bool
	}{
		{name: "growth and close EIO", grow: true, closeError: true},
		{name: "operational-only", seamError: true},
		{name: "overflow-only", grow: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			name := path.Join(internalDirectory, "transactions", "r9-before-read.json")
			initial := []byte("{}")
			writeRecoveryFile(t, root, name, initial)
			absolute := filepath.Join(root, filepath.FromSlash(name))
			before, _ := os.Lstat(absolute)
			seamEIO := errors.New("journal before-read EIO")
			closeEIO := errors.New("journal growth close EIO")
			phases := map[string]int{}
			faultCalls := 0
			s.config.Fault = func(Step) error { faultCalls++; return nil }
			s.config.PostFault = func(Step) error { faultCalls++; return nil }
			s.descriptorBarrier = func(phase string) error {
				phases[phase]++
				if phase != "journal_before_read" {
					return nil
				}
				if test.seamError {
					return seamEIO
				}
				if test.grow {
					f, err := os.OpenFile(absolute, os.O_WRONLY|os.O_APPEND, 0)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := f.Write([]byte("x")); err != nil {
						_ = f.Close()
						t.Fatal(err)
					}
					if err := f.Sync(); err != nil {
						_ = f.Close()
						t.Fatal(err)
					}
					if err := f.Close(); err != nil {
						t.Fatal(err)
					}
				}
				return nil
			}
			if test.closeError {
				injectReadCloseError(t, s, closeEIO, "")
			}

			// Act.
			raw, _, err := s.readRecoveryJournal(context.Background(), name, int64(len(initial)))
			after, statErr := os.Lstat(absolute)

			// Assert.
			if raw != nil || statErr != nil || !os.SameFile(before, after) || phases["journal_before_read"] != 1 || phases["journal_ownership"] != 0 || phases["journal_restat"] != 0 || phases["payload_read"] != 0 || faultCalls != 0 {
				t.Fatalf("raw=%q stat=%v same=%t phases=%v faults=%d", raw, statErr, statErr == nil && os.SameFile(before, after), phases, faultCalls)
			}
			switch {
			case test.seamError:
				if !errors.Is(err, seamEIO) || errors.Is(err, store.ErrStorageCorrupt) || errors.Is(err, closeEIO) {
					t.Fatalf("operational error=%v", err)
				}
			case test.closeError:
				if !errors.Is(err, closeEIO) || !errors.Is(err, store.ErrStorageCorrupt) || strings.Index(err.Error(), closeEIO.Error()) > strings.Index(err.Error(), store.ErrStorageCorrupt.Error()) {
					t.Fatalf("combined error=%v", err)
				}
			default:
				if !errors.Is(err, store.ErrStorageCorrupt) || errors.Is(err, closeEIO) || errors.Is(err, seamEIO) {
					t.Fatalf("overflow error=%v", err)
				}
			}
		})
	}
}
