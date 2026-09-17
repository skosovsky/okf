package fs

// Artifact-claim lifecycle, namespace recovery, and visible rollback.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/skosovsky/okf/store"
)

func TestClaimBindingIsClosed(t *testing.T) {
	for _, test := range []struct {
		name, operation, target string
		role                    artifactRole
	}{
		{name: "empty operation", target: "a.md", role: claimVisible},
		{name: "absolute", operation: "op", target: "/a.md", role: claimVisible},
		{name: "traversal", operation: "op", target: "../a.md", role: claimVisible},
		{name: "unknown role", operation: "op", target: "a.md", role: "future-role"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange/Act.
			_, err := newArtifactClaimKey(test.operation, test.target, test.role)

			// Assert.
			if err == nil || !errors.Is(err, store.ErrStorageCorrupt) {
				t.Fatalf("invalid claim binding error=%v", err)
			}
		})
	}
}

func TestPreJournalRegularProofCompensation(t *testing.T) {
	operation := "sha256:" + strings.Repeat("a", 64)
	tests := []struct {
		name  string
		role  artifactRole
		phase string
	}{
		{name: "payload binding only", role: claimPayload, phase: "artifact_binding_ready:payload"},
		{name: "payload binding and witness", role: claimPayload, phase: "artifact_witness_ready:payload"},
		{name: "journal binding only", role: claimJournal, phase: "artifact_binding_ready:journal"},
		{name: "journal binding and witness", role: claimJournal, phase: "artifact_witness_ready:journal"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			source := path.Join(temporaryDirectory, ".okf-tmp-410000001-0")
			payloadDir, _ := journalStage(operation)
			target := path.Join(payloadDir, "payload-00000")
			if test.role == claimJournal {
				target, _ = journalPath(operation)
			}
			absoluteSource := filepath.Join(root, filepath.FromSlash(source))
			if err := os.MkdirAll(filepath.Dir(absoluteSource), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(absoluteSource, []byte("owned proof source"), 0o600); err != nil {
				t.Fatal(err)
			}
			owned, err := os.Stat(absoluteSource)
			if err != nil {
				t.Fatal(err)
			}
			key, err := newArtifactClaimKey(operation, target, test.role)
			if err != nil {
				t.Fatal(err)
			}
			injected := errors.New("stop at named regular proof phase")
			s.descriptorBarrier = func(phase string) error {
				if phase == test.phase {
					return injected
				}
				return nil
			}

			// Act.
			_, prepareErr := s.prepareRegularWitnessFrom(context.Background(), key, source, owned)
			s.descriptorBarrier = nil
			cleanupErr := s.compensateUndurableRegularBindings(context.Background())
			secondErr := s.compensateUndurableRegularBindings(context.Background())

			// Assert.
			if !errors.Is(prepareErr, injected) || cleanupErr != nil || secondErr != nil {
				t.Fatalf("prepare=%v cleanup=%v second=%v", prepareErr, cleanupErr, secondErr)
			}
			for _, name := range []string{source, target, key.claimPath(), key.witnessPath(false), key.bindingPath()} {
				if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(name))); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("owned residue %s: %v", name, err)
				}
			}
		})
	}
}

func TestPreJournalForeignSourceIsPreserved(t *testing.T) {
	for _, role := range []artifactRole{claimPayload, claimJournal} {
		t.Run(string(role), func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			operation := "sha256:" + strings.Repeat("c", 64)
			source := path.Join(temporaryDirectory, ".okf-binding-foreign-"+string(role))
			target := path.Join(internalDirectory, "staging", "txn-"+strings.Repeat("d", 64), "payload", "payload-00000")
			if role == claimJournal {
				target, _ = journalPath(operation)
			}
			absoluteSource := filepath.Join(root, filepath.FromSlash(source))
			if err := os.MkdirAll(filepath.Dir(absoluteSource), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(absoluteSource, []byte("same bytes"), 0o600); err != nil {
				t.Fatal(err)
			}
			owned, _ := os.Stat(absoluteSource)
			key, _ := newArtifactClaimKey(operation, target, role)
			injected := errors.New("foreign replacement installed")
			var foreign os.FileInfo
			s.descriptorBarrier = func(phase string) error {
				if phase == "artifact_binding_ready:"+string(role) {
					replaceRegularWithSameBytes(t, root, source)
					foreign, _ = os.Stat(absoluteSource)
					return injected
				}
				return nil
			}

			// Act.
			_, prepareErr := s.prepareRegularWitnessFrom(context.Background(), key, source, owned)
			s.descriptorBarrier = nil
			cleanupErr := s.compensateUndurableRegularBindings(context.Background())
			after, statErr := os.Stat(absoluteSource)

			// Assert.
			if !errors.Is(prepareErr, injected) || !errors.Is(cleanupErr, errArtifactClaimConflict) || statErr != nil || foreign == nil || !os.SameFile(foreign, after) || os.SameFile(owned, after) {
				t.Fatalf("prepare=%v cleanup=%v stat=%v foreign preserved=%t old preserved=%t", prepareErr, cleanupErr, statErr, foreign != nil && os.SameFile(foreign, after), os.SameFile(owned, after))
			}
			raw, err := os.ReadFile(absoluteSource)
			if err != nil || !bytes.Equal(raw, []byte("same bytes")) || after.Mode().Perm() != 0o600 {
				t.Fatalf("foreign bytes/mode=(%q,%v,%#o)", raw, err, after.Mode().Perm())
			}
		})
	}
}

func TestPreJournalForeignWitnessIsPreserved(t *testing.T) {
	for _, role := range []artifactRole{claimPayload, claimJournal} {
		t.Run(string(role), func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			operation := "sha256:" + strings.Repeat("9", 64)
			source := path.Join(temporaryDirectory, ".okf-witness-source-"+string(role))
			target := path.Join(internalDirectory, "staging", "txn-"+strings.Repeat("8", 64), "payload", "payload-00000")
			if role == claimJournal {
				target, _ = journalPath(operation)
			}
			absoluteSource := filepath.Join(root, filepath.FromSlash(source))
			if err := os.MkdirAll(filepath.Dir(absoluteSource), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(absoluteSource, []byte("same witness bytes"), 0o600); err != nil {
				t.Fatal(err)
			}
			owned, _ := os.Stat(absoluteSource)
			key, _ := newArtifactClaimKey(operation, target, role)
			injected := errors.New("stop before witness")
			s.descriptorBarrier = func(phase string) error {
				if phase != "artifact_binding_ready:"+string(role) {
					return nil
				}
				absoluteWitness := filepath.Join(root, filepath.FromSlash(key.witnessPath(false)))
				if err := os.WriteFile(absoluteWitness, []byte("same witness bytes"), 0o640); err != nil {
					t.Fatal(err)
				}
				return injected
			}

			// Act.
			_, prepareErr := s.prepareRegularWitnessFrom(context.Background(), key, source, owned)
			s.descriptorBarrier = nil
			cleanupErr := s.compensateUndurableRegularBindings(context.Background())
			witnessRaw, witnessErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(key.witnessPath(false))))
			sourceRaw, sourceErr := os.ReadFile(absoluteSource)

			// Assert.
			if !errors.Is(prepareErr, injected) || !errors.Is(cleanupErr, errArtifactClaimConflict) || witnessErr != nil || sourceErr != nil || !bytes.Equal(witnessRaw, []byte("same witness bytes")) || !bytes.Equal(sourceRaw, []byte("same witness bytes")) {
				t.Fatalf("prepare=%v cleanup=%v witness=(%q,%v) source=(%q,%v)", prepareErr, cleanupErr, witnessRaw, witnessErr, sourceRaw, sourceErr)
			}
			witnessInfo, _ := os.Stat(filepath.Join(root, filepath.FromSlash(key.witnessPath(false))))
			if witnessInfo.Mode().Perm() != 0o640 || os.SameFile(owned, witnessInfo) {
				t.Fatalf("foreign witness mode=%#o aliases source=%t", witnessInfo.Mode().Perm(), os.SameFile(owned, witnessInfo))
			}
		})
	}
}

func TestPreJournalWrongBindingIsPreserved(t *testing.T) {
	for _, role := range []artifactRole{claimPayload, claimJournal} {
		t.Run(string(role), func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			operation := "sha256:" + strings.Repeat("7", 64)
			source := path.Join(temporaryDirectory, ".okf-wrong-binding-"+string(role))
			target := path.Join(internalDirectory, "staging", "txn-"+strings.Repeat("6", 64), "payload", "payload-00000")
			if role == claimJournal {
				target, _ = journalPath(operation)
			}
			absoluteSource := filepath.Join(root, filepath.FromSlash(source))
			if err := os.MkdirAll(filepath.Dir(absoluteSource), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(absoluteSource, []byte("owned"), 0o600); err != nil {
				t.Fatal(err)
			}
			owned, _ := os.Stat(absoluteSource)
			key, _ := newArtifactClaimKey(operation, target, role)
			absoluteBinding := filepath.Join(root, filepath.FromSlash(key.bindingPath()))
			if err := os.MkdirAll(filepath.Dir(absoluteBinding), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(absoluteBinding, []byte("foreign binding"), 0o640); err != nil {
				t.Fatal(err)
			}
			foreign, _ := os.Stat(absoluteBinding)

			// Act.
			_, prepareErr := s.prepareRegularWitnessFrom(context.Background(), key, source, owned)
			recoverErr := s.restoreClaimInventory(context.Background())
			after, statErr := os.Stat(absoluteBinding)

			// Assert.
			if !errors.Is(prepareErr, errArtifactClaimConflict) || !errors.Is(recoverErr, errArtifactClaimConflict) || statErr != nil || !os.SameFile(foreign, after) || after.Mode().Perm() != 0o640 {
				t.Fatalf("prepare=%v recover=%v stat=%v same=%t mode=%#o", prepareErr, recoverErr, statErr, os.SameFile(foreign, after), after.Mode().Perm())
			}
			raw, err := os.ReadFile(absoluteBinding)
			if err != nil || !bytes.Equal(raw, []byte("foreign binding")) {
				t.Fatalf("foreign binding=(%q,%v)", raw, err)
			}
		})
	}
}

func TestPreJournalContradictoryClaimIsPreserved(t *testing.T) {
	for _, role := range []artifactRole{claimPayload, claimJournal} {
		t.Run(string(role), func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			operation := "sha256:" + strings.Repeat("5", 64)
			source := path.Join(temporaryDirectory, ".okf-contradictory-"+string(role))
			target := path.Join(internalDirectory, "staging", "txn-"+strings.Repeat("4", 64), "payload", "payload-00000")
			if role == claimJournal {
				target, _ = journalPath(operation)
			}
			absoluteSource := filepath.Join(root, filepath.FromSlash(source))
			if err := os.MkdirAll(filepath.Dir(absoluteSource), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(absoluteSource, []byte("owned"), 0o600); err != nil {
				t.Fatal(err)
			}
			owned, _ := os.Stat(absoluteSource)
			key, _ := newArtifactClaimKey(operation, target, role)
			injected := errors.New("stop with binding and witness")
			s.descriptorBarrier = func(phase string) error {
				if phase != "artifact_witness_ready:"+string(role) {
					return nil
				}
				absoluteClaim := filepath.Join(root, filepath.FromSlash(key.claimPath()))
				if err := os.WriteFile(absoluteClaim, []byte("foreign claim"), 0o640); err != nil {
					t.Fatal(err)
				}
				return injected
			}

			// Act.
			_, prepareErr := s.prepareRegularWitnessFrom(context.Background(), key, source, owned)
			s.descriptorBarrier = nil
			cleanupErr := s.compensateUndurableRegularBindings(context.Background())
			claimRaw, claimErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(key.claimPath())))
			sourceRaw, sourceErr := os.ReadFile(absoluteSource)
			witnessName := filepath.Join(root, filepath.FromSlash(key.witnessPath(false)))
			witnessRaw, witnessErr := os.ReadFile(witnessName)
			witnessInfo, witnessStatErr := os.Stat(witnessName)

			// Assert.
			if !errors.Is(prepareErr, injected) || !errors.Is(cleanupErr, errArtifactClaimConflict) || claimErr != nil || sourceErr != nil || witnessErr != nil || witnessStatErr != nil || !bytes.Equal(claimRaw, []byte("foreign claim")) || !bytes.Equal(sourceRaw, []byte("owned")) || !bytes.Equal(witnessRaw, []byte("owned")) || !os.SameFile(owned, witnessInfo) {
				t.Fatalf("prepare=%v cleanup=%v claim=(%q,%v) source=(%q,%v) witness=(%q,%v,%v)", prepareErr, cleanupErr, claimRaw, claimErr, sourceRaw, sourceErr, witnessRaw, witnessErr, witnessStatErr)
			}
		})
	}
}

func TestValidatedJournalCleansInstalledRegularProof(t *testing.T) {
	for _, role := range []artifactRole{claimPayload, claimJournal} {
		for _, phase := range []string{"installed_target_parent_synced:" + string(role), "artifact_proof_parent_synced", "artifact_binding_removed"} {
			t.Run(string(role)+"/"+phase, func(t *testing.T) {
				root, s := adversarialStore(t, Config{})
				base := adversarialSnapshot(t, s)
				next := nextSnapshotForDurabilityTest(t, s)
				receipt := testJournalReceipt(base.Revision(), next.Revision(), "validated-proof-"+string(role)+phase, "")
				crash := errors.New("retain validated journal")
				s.config.PostFault = func(step Step) error {
					if step == StepJournalDirectorySync {
						return crash
					}
					return nil
				}
				publishErr := s.publish(context.Background(), next, receipt)
				s.config.PostFault = nil
				var committed *store.CommittedError
				if !errors.Is(publishErr, crash) || !errors.As(publishErr, &committed) {
					t.Fatalf("live crash evidence=%v committed=%v", publishErr, committed)
				}
				injected := errors.New("stop in installed proof tail")
				fired := false
				s.descriptorBarrier = func(got string) error {
					if !fired && got == phase {
						fired = true
						return injected
					}
					return nil
				}

				_, firstErr := s.Snapshot(context.Background())
				s.descriptorBarrier = nil
				recovered, recoverErr := s.Snapshot(context.Background())
				second, secondErr := s.Snapshot(context.Background())
				if !fired || !errors.Is(firstErr, injected) || recoverErr != nil || secondErr != nil || recovered.Revision() != next.Revision() || second.Revision() != next.Revision() {
					t.Fatalf("fired=%t first=%v recover=%v second=%v recovered=%v secondSnapshot=%v", fired, firstErr, recoverErr, secondErr, recovered, second)
				}
				if claims := claimInventory(t, root); len(claims) != 0 {
					t.Fatalf("proof residue=%v", claims)
				}
			})
		}
	}
}

func TestPreJournalCompensationProofTail(t *testing.T) {
	phases := []string{
		"undurable_source_parent_synced:",
		"artifact_proof_parent_synced",
		"artifact_binding_removed",
	}
	for _, role := range []artifactRole{claimPayload, claimJournal} {
		for _, phasePrefix := range phases {
			phase := phasePrefix
			if strings.HasSuffix(phase, ":") {
				phase += string(role)
			}
			t.Run(string(role)+"/"+phase, func(t *testing.T) {
				// Arrange.
				root, s := adversarialStore(t, Config{})
				operation := "sha256:" + strings.Repeat("1", 64)
				source := path.Join(temporaryDirectory, ".okf-tmp-410000002-0")
				payloadDir, _ := journalStage(operation)
				target := path.Join(payloadDir, "payload-00000")
				if role == claimJournal {
					target, _ = journalPath(operation)
				}
				absoluteSource := filepath.Join(root, filepath.FromSlash(source))
				if err := os.MkdirAll(filepath.Dir(absoluteSource), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(absoluteSource, []byte("proof tail"), 0o600); err != nil {
					t.Fatal(err)
				}
				owned, _ := os.Stat(absoluteSource)
				key, _ := newArtifactClaimKey(operation, target, role)
				if _, err := s.prepareRegularWitnessFrom(context.Background(), key, source, owned); err != nil {
					t.Fatal(err)
				}
				injected := errors.New("stop in compensation proof tail")
				fired := false
				s.descriptorBarrier = func(got string) error {
					if !fired && got == phase {
						fired = true
						return injected
					}
					return nil
				}

				// Act.
				firstErr := s.compensateUndurableRegularBindings(context.Background())
				s.descriptorBarrier = nil
				reopenErr := s.compensateUndurableRegularBindings(context.Background())
				secondErr := s.compensateUndurableRegularBindings(context.Background())

				// Assert.
				if !fired || !errors.Is(firstErr, injected) || reopenErr != nil || secondErr != nil {
					t.Fatalf("fired=%t first=%v reopen=%v second=%v", fired, firstErr, reopenErr, secondErr)
				}
				for _, name := range []string{source, target, key.claimPath(), key.witnessPath(false), key.bindingPath()} {
					if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(name))); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("proof residue %s: %v", name, err)
					}
				}
			})
		}
	}
}

func TestClaimProtocolAdversarialMatrix(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*testing.T, string, *Store, artifactClaimKey, os.FileInfo, error)
		wantRetry bool
	}{
		{
			name: "swap before witness link",
			configure: func(t *testing.T, root string, s *Store, key artifactClaimKey, _ os.FileInfo, _ error) {
				s.descriptorBarrier = func(operation string) error {
					if operation == "witness_before_link" {
						replaceRegularWithSameBytes(t, root, key.Path)
						s.descriptorBarrier = nil
					}
					return nil
				}
			},
		},
		{
			name: "swap after witness link",
			configure: func(t *testing.T, root string, s *Store, key artifactClaimKey, _ os.FileInfo, _ error) {
				s.descriptorBarrier = func(operation string) error {
					if operation == "witness_after_link" {
						replaceRegularWithSameBytes(t, root, key.Path)
						s.descriptorBarrier = nil
					}
					return nil
				}
			},
		},
		{
			name: "swap before no-replace claim",
			configure: func(t *testing.T, root string, s *Store, key artifactClaimKey, _ os.FileInfo, _ error) {
				s.descriptorBarrier = func(operation string) error {
					if operation == "claim_before_rename" {
						replaceRegularWithSameBytes(t, root, key.Path)
						s.descriptorBarrier = nil
					}
					return nil
				}
			},
		},
		{
			name: "crash after claim and retry",
			configure: func(_ *testing.T, _ string, s *Store, _ artifactClaimKey, _ os.FileInfo, injected error) {
				s.descriptorBarrier = func(operation string) error {
					if operation == "claim_after_rename" {
						return injected
					}
					return nil
				}
			},
			wantRetry: true,
		},
		{
			name: "hostile precreated claim",
			configure: func(t *testing.T, root string, _ *Store, key artifactClaimKey, _ os.FileInfo, _ error) {
				absolute := filepath.Join(root, filepath.FromSlash(key.claimPath()))
				if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(absolute, []byte("foreign"), 0o640); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "hostile precreated witness",
			configure: func(t *testing.T, root string, _ *Store, key artifactClaimKey, _ os.FileInfo, _ error) {
				absolute := filepath.Join(root, filepath.FromSlash(key.witnessPath(false)))
				if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(absolute, []byte("foreign"), 0o640); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			target := path.Join(temporaryDirectory, ".okf-r4-owned")
			absolute := filepath.Join(root, filepath.FromSlash(target))
			if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(absolute, []byte("owned"), 0o600); err != nil {
				t.Fatal(err)
			}
			owned, err := os.Stat(absolute)
			if err != nil {
				t.Fatal(err)
			}
			key, err := newArtifactClaimKey("receipt:r4", target, claimScratch)
			if err != nil {
				t.Fatal(err)
			}
			injected := errors.New("crash after claim")
			test.configure(t, root, s, key, owned, injected)

			// Act.
			_, prepareErr := s.prepareOwnedClaim(context.Background(), key, owned, false)
			removed, removeErr := false, prepareErr
			if prepareErr == nil {
				removed, removeErr = s.consumeOwnedClaim(context.Background(), key, false)
			}

			// Assert.
			if test.wantRetry {
				if removed || !errors.Is(removeErr, injected) {
					t.Fatalf("first removal=(%t,%v)", removed, removeErr)
				}
				s.descriptorBarrier = nil
				if _, prepareErr = s.prepareOwnedClaim(context.Background(), key, owned, false); prepareErr != nil {
					t.Fatalf("retry prepare=%v", prepareErr)
				}
				removed, removeErr = s.consumeOwnedClaim(context.Background(), key, false)
				if !removed || removeErr != nil {
					t.Fatalf("retry removal=(%t,%v)", removed, removeErr)
				}
				return
			}
			if removed || removeErr == nil {
				t.Fatalf("removal=(%t,%v), want preserved conflict", removed, removeErr)
			}
			if raw, err := os.ReadFile(absolute); err != nil || !bytes.Equal(raw, []byte("owned")) {
				t.Fatalf("target=(%q,%v), want preserved named inode", raw, err)
			}
		})
	}
}

func TestTerminalClaimSwapIsPreserved(t *testing.T) {
	// Arrange.
	root, s := adversarialStore(t, Config{})
	target := path.Join(temporaryDirectory, ".okf-r4-terminal")
	absolute := filepath.Join(root, filepath.FromSlash(target))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolute, []byte("owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	owned, _ := os.Stat(absolute)
	key, _ := newArtifactClaimKey("receipt:terminal", target, claimScratch)
	injected := errors.New("stop with claim")
	s.descriptorBarrier = func(operation string) error {
		if operation == "claim_after_rename" {
			return injected
		}
		return nil
	}
	if _, err := s.prepareOwnedClaim(context.Background(), key, owned, false); !errors.Is(err, injected) {
		t.Fatalf("claim setup error=%v", err)
	}
	var foreign os.FileInfo
	s.descriptorBarrier = func(operation string) error {
		if operation == "remove" && foreign == nil {
			_, _, foreign = replaceRegularWithSameBytes(t, root, key.claimPath())
		}
		return nil
	}

	// Act.
	removed, removeErr := s.consumeOwnedClaim(context.Background(), key, false)
	after, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(key.claimPath())))

	// Assert.
	if removed || removeErr == nil || foreign == nil || statErr != nil || !os.SameFile(foreign, after) {
		t.Fatalf("removed=%t err=%v foreign=%v stat=%v", removed, removeErr, foreign, statErr)
	}
}

func TestDirectoryClaimUsesDurableSentinel(t *testing.T) {
	// Arrange.
	root, s := adversarialStore(t, Config{})
	target := path.Join(internalDirectory, "staging", "txn-"+strings.Repeat("e", 64), "payload")
	absolute := filepath.Join(root, filepath.FromSlash(target))
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		t.Fatal(err)
	}
	owned, _ := os.Stat(absolute)
	key, _ := newArtifactClaimKey("receipt:directory", target, claimStageDir)
	if _, err := s.prepareArtifactWitness(context.Background(), key, owned, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.prepareArtifactWitness(context.Background(), key, owned, true); err != nil {
		t.Fatalf("idempotent witness: %v", err)
	}

	// Act.
	if _, err := s.prepareOwnedClaim(context.Background(), key, owned, true); err != nil {
		t.Fatal(err)
	}
	removed, removeErr := s.consumeOwnedClaim(context.Background(), key, true)
	_, targetErr := os.Stat(absolute)
	_, claimErr := os.Stat(filepath.Join(root, filepath.FromSlash(key.claimPath())))
	_, witnessErr := os.Stat(filepath.Join(root, filepath.FromSlash(key.witnessPath(true))))

	// Assert.
	if !removed || removeErr != nil || !errors.Is(targetErr, os.ErrNotExist) || !errors.Is(claimErr, os.ErrNotExist) || !errors.Is(witnessErr, os.ErrNotExist) {
		t.Fatalf("removed=%t err=%v target=%v claim=%v witness=%v", removed, removeErr, targetErr, claimErr, witnessErr)
	}
}

func TestInstallProtocolAdversarialMatrix(t *testing.T) {
	for _, kind := range []string{"success and retry", "crash after no-replace", "foreign destination", "foreign source restored", "restore collision"} {
		t.Run(kind, func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			dir := filepath.Join(root, filepath.FromSlash(temporaryDirectory))
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			source := path.Join(temporaryDirectory, ".okf-r4-source")
			target := path.Join(internalDirectory, "transactions", "r4-install.json")
			sourceAbs := filepath.Join(root, filepath.FromSlash(source))
			targetAbs := filepath.Join(root, filepath.FromSlash(target))
			if err := os.MkdirAll(filepath.Dir(targetAbs), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(sourceAbs, []byte("owned"), 0o600); err != nil {
				t.Fatal(err)
			}
			owned, _ := os.Stat(sourceAbs)
			key, _ := newArtifactClaimKey("receipt:install", target, claimJournal)
			injected := errors.New("stop after install")
			var foreignTarget os.FileInfo
			switch kind {
			case "crash after no-replace":
				s.descriptorBarrier = func(operation string) error {
					if operation == "install_after_noreplace" {
						return injected
					}
					return nil
				}
			case "foreign destination":
				if err := os.WriteFile(targetAbs, []byte("foreign"), 0o640); err != nil {
					t.Fatal(err)
				}
				foreignTarget, _ = os.Stat(targetAbs)
			case "foreign source restored", "restore collision":
				s.descriptorBarrier = func(operation string) error {
					if operation != "claim_after_rename" || foreignTarget != nil {
						return nil
					}
					_, _, foreignTarget = replaceRegularWithSameBytes(t, root, target)
					if kind == "restore collision" {
						if err := os.WriteFile(sourceAbs, []byte("occupied"), 0o600); err != nil {
							t.Fatal(err)
						}
					}
					return nil
				}
			}

			// Act.
			installed, installErr := s.installOwnedClaimed(context.Background(), key, source, owned, false)

			// Assert.
			switch kind {
			case "success and retry":
				if installErr != nil || installed == nil {
					t.Fatalf("install=(%v,%v)", installed, installErr)
				}
				replayed, replayErr := s.installOwnedClaimed(context.Background(), key, source, owned, false)
				if replayErr != nil || replayed == nil || !os.SameFile(installed, replayed) {
					t.Fatalf("replay=(%v,%v)", replayed, replayErr)
				}
			case "crash after no-replace":
				if !errors.Is(installErr, injected) {
					t.Fatalf("install error=%v", installErr)
				}
				s.descriptorBarrier = nil
				replayed, replayErr := s.installOwnedClaimed(context.Background(), key, source, owned, false)
				if replayErr != nil || replayed == nil {
					t.Fatalf("replay=(%v,%v)", replayed, replayErr)
				}
			case "foreign destination":
				after, statErr := os.Stat(targetAbs)
				if installErr == nil || statErr != nil || !os.SameFile(foreignTarget, after) {
					t.Fatalf("install=%v foreign=%v stat=%v", installErr, foreignTarget, statErr)
				}
			case "foreign source restored":
				restored, sourceErr := os.Stat(sourceAbs)
				_, targetErr := os.Stat(targetAbs)
				if installErr == nil || sourceErr != nil || !os.SameFile(foreignTarget, restored) || !errors.Is(targetErr, os.ErrNotExist) {
					t.Fatalf("install=%v restored=%v source=%v target=%v", installErr, restored, sourceErr, targetErr)
				}
			case "restore collision":
				after, targetErr := os.Stat(targetAbs)
				if installErr == nil || targetErr != nil || !os.SameFile(foreignTarget, after) {
					t.Fatalf("install=%v target=%v foreign=%v", installErr, targetErr, foreignTarget)
				}
			}
		})
	}
}

func TestJournalHasSinglePublicationBoundary(t *testing.T) {
	// Arrange.
	root, s := adversarialStore(t, Config{})
	base := adversarialSnapshot(t, s)
	next := nextSnapshotForDurabilityTest(t, s)
	receipt := testJournalReceipt(base.Revision(), next.Revision(), "r4-sync-trace", "")
	transactionSyncs := 0
	var syncDirs []string
	s.config.DirectorySync = func(dir string) error {
		syncDirs = append(syncDirs, dir)
		if dir == path.Join(internalDirectory, "transactions") {
			transactionSyncs++
		}
		return nil
	}
	injected := errors.New("post-apply directory sync")
	var steps []Step
	s.config.Fault = func(step Step) error {
		steps = append(steps, step)
		if step == StepDirectorySync {
			return injected
		}
		return nil
	}

	// Act.
	publishErr := s.publish(context.Background(), next, receipt)
	journalCount := len(pendingJournalNames(t, root))
	s.config.Fault = nil
	s.config.DirectorySync = nil
	recoverErr := s.recoverForTest(context.Background())
	visible, snapshotErr := s.Snapshot(context.Background())

	// Assert.
	if !errors.Is(publishErr, injected) || journalCount != 1 || transactionSyncs != 1 || recoverErr != nil || snapshotErr != nil || visible == nil || visible.Revision() != next.Revision() {
		t.Fatalf("publish=%v journals=%d txSyncs=%d recover=%v snapshot=%v visible=%v want=%s steps=%v dirs=%v", publishErr, journalCount, transactionSyncs, recoverErr, snapshotErr, visible, next.Revision(), steps, syncDirs)
	}
}

func TestRoleClaimTerminalSwapOutcome(t *testing.T) {
	for _, role := range []artifactRole{claimJournal, claimPayload} {
		t.Run(string(role), func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			base := adversarialSnapshot(t, s)
			next := nextSnapshotForDurabilityTest(t, s)
			receipt := testJournalReceipt(base.Revision(), next.Revision(), "r4-role-"+string(role), "")
			journalName, _ := journalPath(receipt.RequestDigest)
			stageName, _ := journalStage(receipt.RequestDigest)
			artifactName := journalName
			if role == claimPayload {
				artifactName = path.Join(stageName, payloadName(0))
			}
			key, _ := newArtifactClaimKey(receipt.RequestDigest, artifactName, role)
			var foreign os.FileInfo
			var wantBytes []byte
			var wantMode os.FileMode
			s.descriptorBarrier = func(operation string) error {
				if operation == "claim_before_unlink:"+string(role) && foreign == nil {
					_, wantBytes, foreign = replaceRegularWithSameBytes(t, root, key.claimPath())
					wantMode = foreign.Mode()
				}
				return nil
			}

			// Act.
			publishErr := s.publish(context.Background(), next, receipt)
			claimAbs := filepath.Join(root, filepath.FromSlash(key.claimPath()))
			after, statErr := os.Stat(claimAbs)
			gotBytes, readErr := os.ReadFile(claimAbs)
			_, stageErr := os.Stat(filepath.Join(root, filepath.FromSlash(stageName)))
			visible, visibleErr := readVisibleRoot(context.Background(), s.rootFD)
			var committed *store.CommittedError

			// Assert.
			if !errors.Is(publishErr, errArtifactClaimConflict) || !errors.As(publishErr, &committed) || committed.Receipt().RequestDigest != receipt.RequestDigest {
				t.Fatalf("publish=%v committed=%v", publishErr, committed)
			}
			if foreign == nil || statErr != nil || readErr != nil || !os.SameFile(foreign, after) || after.Mode() != wantMode || !bytes.Equal(gotBytes, wantBytes) {
				t.Fatalf("foreign=%v after=%v stat=%v read=%v mode=%v/%v bytes=%t", foreign, after, statErr, readErr, after.Mode(), wantMode, bytes.Equal(gotBytes, wantBytes))
			}
			if stageErr != nil || visibleErr != nil || !sameVisibleFiles(visible, next.files) {
				t.Fatalf("stage=%v visible=%v files=%v", stageErr, visibleErr, sortedFilePaths(visible))
			}
			s.descriptorBarrier = nil
			retryErr := s.recoverForTest(context.Background())
			if retryErr == nil {
				t.Fatal("foreign quarantine retry unexpectedly succeeded")
			}
		})
	}
}

func TestStageNamespaceLifecycleMatrix(t *testing.T) {
	const stageID = "txn-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, test := range []struct {
		name string
		act  func(*testing.T, string, *Store, artifactClaimKey) error
		want string
	}{
		{
			name: "crash before canonical noreplace",
			act: func(_ *testing.T, _ string, s *Store, key artifactClaimKey) error {
				injected := errors.New("before canonical publication")
				s.descriptorBarrier = func(operation string) error {
					if operation == "claim_before_rename" {
						return injected
					}
					return nil
				}
				_, err := s.createOwnedStageDirectory(context.Background(), key)
				s.descriptorBarrier = nil
				if !errors.Is(err, injected) {
					return errors.Join(errors.New("wrong injected outcome"), err)
				}
				return s.cleanupStageNamespaceClaims(context.Background())
			},
			want: "clean",
		},
		{
			name: "crash after canonical noreplace",
			act: func(_ *testing.T, _ string, s *Store, key artifactClaimKey) error {
				injected := errors.New("after canonical publication")
				s.descriptorBarrier = func(operation string) error {
					if operation == "claim_after_rename" {
						return injected
					}
					return nil
				}
				_, err := s.createOwnedStageDirectory(context.Background(), key)
				s.descriptorBarrier = nil
				if !errors.Is(err, injected) {
					return errors.Join(errors.New("wrong injected outcome"), err)
				}
				return s.cleanupOrphanStages(context.Background())
			},
			want: "clean",
		},
		{
			name: "torn private sentinel",
			act: func(t *testing.T, root string, s *Store, key artifactClaimKey) error {
				build := filepath.Join(root, filepath.FromSlash(key.buildPath()))
				if err := os.MkdirAll(build, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(build, directorySentinelName), []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
				return s.cleanupStageNamespaceClaims(context.Background())
			},
			want: "preserved-conflict",
		},
		{
			name: "duplicate build and canonical states",
			act: func(t *testing.T, root string, s *Store, key artifactClaimKey) error {
				if _, err := s.createOwnedStageDirectory(context.Background(), key); err != nil {
					t.Fatal(err)
				}
				build := filepath.Join(root, filepath.FromSlash(key.buildPath()))
				if err := os.Mkdir(build, 0o700); err != nil {
					t.Fatal(err)
				}
				copyStageSentinel(t, root, key.Path, key.buildPath())
				return s.cleanupStageNamespaceClaims(context.Background())
			},
			want: "preserved-conflict",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			stagePath := path.Join(internalDirectory, "staging", stageID)
			key, err := newArtifactClaimKey("r4-stage-namespace", stagePath, claimStageDir)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			err = test.act(t, root, s, key)
			_, canonicalErr := os.Stat(filepath.Join(root, filepath.FromSlash(key.Path)))
			_, buildErr := os.Stat(filepath.Join(root, filepath.FromSlash(key.buildPath())))
			_, claimErr := os.Stat(filepath.Join(root, filepath.FromSlash(key.claimPath())))

			// Assert.
			switch test.want {
			case "clean":
				if err != nil || !errors.Is(canonicalErr, os.ErrNotExist) || !errors.Is(buildErr, os.ErrNotExist) || !errors.Is(claimErr, os.ErrNotExist) {
					t.Fatalf("cleanup=%v canonical=%v build=%v claim=%v", err, canonicalErr, buildErr, claimErr)
				}
			case "preserved-conflict":
				if !errors.Is(err, errArtifactClaimConflict) || errors.Is(buildErr, os.ErrNotExist) {
					t.Fatalf("cleanup=%v canonical=%v build=%v claim=%v", err, canonicalErr, buildErr, claimErr)
				}
			}
		})
	}
}

func TestStageNamespaceRejectsContradictoryStates(t *testing.T) {
	const stageID = "txn-1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, test := range []struct {
		name  string
		setup func(*testing.T, string, *Store, artifactClaimKey)
	}{
		{
			name: "missing build sentinel",
			setup: func(t *testing.T, root string, _ *Store, key artifactClaimKey) {
				if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(key.buildPath())), 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "wrong build sentinel",
			setup: func(t *testing.T, root string, s *Store, key artifactClaimKey) {
				foreign, _ := newArtifactClaimKey(key.Operation, key.Path+"-foreign", claimStageDir)
				makeStageNamespaceDirectory(t, root, s, foreign, key.buildPath())
			},
		},
		{
			name: "hostile precreated claim",
			setup: func(t *testing.T, root string, _ *Store, key artifactClaimKey) {
				if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(key.claimPath())), 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "build plus claim",
			setup: func(t *testing.T, root string, s *Store, key artifactClaimKey) {
				makeStageNamespaceDirectory(t, root, s, key, key.buildPath())
				if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(key.claimPath())), 0o700); err != nil {
					t.Fatal(err)
				}
				copyStageSentinel(t, root, key.buildPath(), key.claimPath())
			},
		},
		{
			name: "canonical plus claim",
			setup: func(t *testing.T, root string, s *Store, key artifactClaimKey) {
				if _, err := s.createOwnedStageDirectory(context.Background(), key); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(key.claimPath())), 0o700); err != nil {
					t.Fatal(err)
				}
				copyStageSentinel(t, root, key.Path, key.claimPath())
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			key, _ := newArtifactClaimKey("r4-stage-conflict", path.Join(internalDirectory, "staging", stageID), claimStageDir)
			test.setup(t, root, s, key)

			// Act.
			err := s.cleanupStageNamespaceClaims(context.Background())

			// Assert.
			if !errors.Is(err, errArtifactClaimConflict) {
				t.Fatalf("cleanup error=%v", err)
			}
			if _, buildErr := os.Stat(filepath.Join(root, filepath.FromSlash(key.buildPath()))); buildErr == nil && test.name != "canonical plus claim" {
				return // preserved evidence is the expected outcome
			}
			if _, claimErr := os.Stat(filepath.Join(root, filepath.FromSlash(key.claimPath()))); claimErr == nil {
				return
			}
			t.Fatal("conflicting namespace evidence was not preserved")
		})
	}
}

func copyStageSentinel(t *testing.T, root, sourceDir, targetDir string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(sourceDir), directorySentinelName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetDir), directorySentinelName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestStageNamespaceClaimsRecoverDeepestFirst(t *testing.T) {
	// Arrange.
	root, s := adversarialStore(t, Config{})
	outerPath := path.Join(internalDirectory, "staging", "txn-2123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	innerPath := path.Join(outerPath, "payload")
	outerKey, _ := newArtifactClaimKey("r4-stage-depth", outerPath, claimStageDir)
	innerKey, _ := newArtifactClaimKey("r4-stage-depth", innerPath, claimStageDir)
	outer, err := s.createOwnedStageDirectory(context.Background(), outerKey)
	if err != nil {
		t.Fatal(err)
	}
	inner, err := s.createOwnedStageDirectory(context.Background(), innerKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.fdRenameOwnedNoReplace(innerPath, innerKey.claimPath(), inner, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.fdRenameOwnedNoReplace(outerPath, outerKey.claimPath(), outer, true); err != nil {
		t.Fatal(err)
	}

	// Act.
	firstErr := s.cleanupStageNamespaceClaims(context.Background())
	secondErr := s.cleanupStageNamespaceClaims(context.Background())

	// Assert.
	if firstErr != nil || secondErr != nil {
		t.Fatalf("first=%v second=%v", firstErr, secondErr)
	}
	for _, name := range []string{outerPath, innerPath, outerKey.buildPath(), innerKey.buildPath(), outerKey.claimPath(), innerKey.claimPath()} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("residue %s: %v", name, err)
		}
	}
}

func TestStageNamespaceParentSyncCutsRetry(t *testing.T) {
	const stageID = "txn-3123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, test := range []struct {
		seam      string
		canonical bool
	}{
		{seam: "stage_build_directory_synced"},
		{seam: "stage_build_parent_synced"},
		{seam: "stage_build_source_parent_synced", canonical: true},
		{seam: "stage_canonical_parent_synced", canonical: true},
	} {
		t.Run(test.seam, func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			key, _ := newArtifactClaimKey("r4-stage-sync-cut", path.Join(internalDirectory, "staging", stageID), claimStageDir)
			injected := errors.New("parent sync cut")
			s.descriptorBarrier = func(operation string) error {
				if operation == test.seam {
					return injected
				}
				return nil
			}

			// Act.
			_, createErr := s.createOwnedStageDirectory(context.Background(), key)
			s.descriptorBarrier = nil
			canonicalInfo, canonicalErr := os.Stat(filepath.Join(root, filepath.FromSlash(key.Path)))
			buildInfo, buildErr := os.Stat(filepath.Join(root, filepath.FromSlash(key.buildPath())))
			var recoverErr error
			if test.canonical {
				recoverErr = s.cleanupOrphanStages(context.Background())
			} else {
				recoverErr = s.cleanupStageNamespaceClaims(context.Background())
			}
			retryErr := s.cleanupStageNamespaceClaims(context.Background())

			// Assert.
			if !errors.Is(createErr, injected) || recoverErr != nil || retryErr != nil {
				t.Fatalf("create=%v recover=%v retry=%v", createErr, recoverErr, retryErr)
			}
			if test.canonical {
				if canonicalErr != nil || canonicalInfo == nil || !errors.Is(buildErr, os.ErrNotExist) {
					t.Fatalf("canonical=%v/%v build=%v/%v", canonicalInfo, canonicalErr, buildInfo, buildErr)
				}
			} else if buildErr != nil || buildInfo == nil || !errors.Is(canonicalErr, os.ErrNotExist) {
				t.Fatalf("build=%v/%v canonical=%v/%v", buildInfo, buildErr, canonicalInfo, canonicalErr)
			}
			for _, name := range []string{key.Path, key.buildPath(), key.claimPath()} {
				if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("residue %s: %v", name, err)
				}
			}
		})
	}
}

func TestStageClaimParentSyncCutIsIdempotent(t *testing.T) {
	// Arrange.
	root, s := adversarialStore(t, Config{})
	stagePath := path.Join(internalDirectory, "staging", "txn-4123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	key, _ := newArtifactClaimKey("r4-stage-claim-sync", stagePath, claimStageDir)
	_, err := s.createOwnedStageDirectory(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("claim parent synced")
	s.descriptorBarrier = func(operation string) error {
		if operation == "stage_claim_parent_synced" {
			return injected
		}
		return nil
	}

	// Act.
	removed, removeErr := s.consumeOwnedClaim(context.Background(), key, true)
	s.descriptorBarrier = nil
	retryErr := s.cleanupStageNamespaceClaims(context.Background())

	// Assert.
	if removed || !errors.Is(removeErr, injected) || retryErr != nil {
		t.Fatalf("removed=%t remove=%v retry=%v", removed, removeErr, retryErr)
	}
	for _, name := range []string{key.Path, key.buildPath(), key.claimPath()} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("residue %s: %v", name, err)
		}
	}
}

func TestClaimCleanupBatchesOwnedStageBoundaries(t *testing.T) {
	for _, count := range []int{0, 1, 3} {
		t.Run(formatReceiptNumber(count), func(t *testing.T) {
			// Arrange.
			root, s, j, owned, unrelated, unrelatedInfo := stageNamespaceCleanupFixture(t, count)
			var syncs []string
			var posts []Step
			s.config.DirectorySync = func(dir string) error {
				syncs = append(syncs, dir)
				return nil
			}
			s.config.PostFault = func(step Step) error {
				posts = append(posts, step)
				return nil
			}

			// Act.
			err := s.cleanupOwnedStage(j, owned)

			// Assert.
			if err != nil {
				t.Fatal(err)
			}
			for _, boundary := range []struct {
				step Step
				dir  string
			}{
				{StepStageCleanupPayloadDirectory, j.Stage},
				{StepStageCleanupStageDirectory, path.Dir(j.Stage)},
				{StepStageCleanupRootDirectory, path.Dir(path.Dir(j.Stage))},
			} {
				if got := countSteps(posts, boundary.step); got != 1 {
					t.Fatalf("boundary %s posts=%d trace=%#v, want one", boundary.step, got, posts)
				}
				if got := countStrings(syncs, boundary.dir); got == 0 {
					t.Fatalf("boundary %s missing physical sync trace=%#v", boundary.step, syncs)
				}
			}
			if got := countSteps(posts, StepStageCleanupPayloadRemove); got != count {
				t.Fatalf("payload removals=%d trace=%#v, want %d", got, posts, count)
			}
			assertStageCleanupState(t, root, j, unrelated, unrelatedInfo)
		})
	}
}

func TestClaimCleanupPostFaultConvergesByNamedDirectoryPhase(t *testing.T) {
	for _, phase := range []Step{
		StepStageCleanupPayloadDirectory,
		StepStageCleanupStageDirectory,
		StepStageCleanupRootDirectory,
	} {
		t.Run(string(phase), func(t *testing.T) {
			// Arrange.
			root, s, j, owned, unrelated, unrelatedInfo := stageNamespaceCleanupFixture(t, 3)
			injected := errors.New("named stage cleanup boundary")
			fired := false
			var firstPosts []Step
			s.config.PostFault = func(step Step) error {
				firstPosts = append(firstPosts, step)
				if !fired && step == phase {
					fired = true
					return injected
				}
				return nil
			}

			// Act: the first call stops after the named physical directory sync;
			// recovery consumes the factual namespace state, then a second reopen
			// proves that no claim or stage evidence remains.
			firstErr := s.cleanupOwnedStage(j, owned)
			s.config.PostFault = nil
			firstRecoveryErr := s.cleanupOrphanStages(context.Background())
			secondRecoveryErr := s.cleanupOrphanStages(context.Background())

			// Assert.
			if !errors.Is(firstErr, injected) || firstRecoveryErr != nil || secondRecoveryErr != nil {
				t.Fatalf("first=%v recovery=%v second=%v posts=%#v", firstErr, firstRecoveryErr, secondRecoveryErr, firstPosts)
			}
			if got := countSteps(firstPosts, phase); got != 1 {
				t.Fatalf("phase %s posts=%d trace=%#v, want one before recovery", phase, got, firstPosts)
			}
			assertStageCleanupState(t, root, j, unrelated, unrelatedInfo)
		})
	}
}

func stageNamespaceCleanupFixture(t *testing.T, count int) (string, *Store, journal, stagePublicationOwnership, string, os.FileInfo) {
	t.Helper()
	root, s := adversarialStore(t, Config{})
	base := adversarialSnapshot(t, s)
	files := make(map[string][]byte, count)
	for i := 0; i < count; i++ {
		files[fmt.Sprintf("result-%02d.md", i)] = []byte(adversarialDocument(fmt.Sprintf("result-%02d", i)))
	}
	next, err := newSnapshot(context.Background(), files)
	if err != nil {
		t.Fatal(err)
	}
	receipt := testJournalReceipt(base.Revision(), next.Revision(), "r4-stage-cleanup", "")
	j, owned, err := s.stageJournalWithManifestLimitsObserved(context.Background(), next, receipt, replaceReplay{}, productionJournalManifestByteLimits())
	if err != nil {
		t.Fatal(err)
	}
	unrelated := path.Join(internalDirectory, "staging", "unproven-neighbor.bin")
	unrelatedAbs := filepath.Join(root, filepath.FromSlash(unrelated))
	if err := os.WriteFile(unrelatedAbs, []byte("unproven"), 0o640); err != nil {
		t.Fatal(err)
	}
	unrelatedInfo, err := os.Stat(unrelatedAbs)
	if err != nil {
		t.Fatal(err)
	}
	return root, s, j, owned, unrelated, unrelatedInfo
}

func assertStageCleanupState(t *testing.T, root string, j journal, unrelated string, unrelatedInfo os.FileInfo) {
	t.Helper()
	stageRoot := path.Dir(j.Stage)
	for _, name := range []string{j.Stage, stageRoot} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stage residue %s: %v", name, err)
		}
	}
	unrelatedAfter, err := os.Stat(filepath.Join(root, filepath.FromSlash(unrelated)))
	if err != nil || !os.SameFile(unrelatedInfo, unrelatedAfter) || unrelatedInfo.Mode() != unrelatedAfter.Mode() {
		t.Fatalf("unproven neighbor changed: before=%v after=%v err=%v", unrelatedInfo, unrelatedAfter, err)
	}
	claimEntries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(claimDirectory)))
	if err != nil || len(claimEntries) != 0 {
		t.Fatalf("claim residue entries=%v err=%v", claimEntries, err)
	}
}

func TestScratchSourceForeignSwapMatrix(t *testing.T) {
	for _, seam := range []string{"witness_before_link", "witness_after_link", "scratch_ready_restat"} {
		t.Run(seam, func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			var foreign os.FileInfo
			var foreignName string
			var wantBytes []byte
			var wantMode os.FileMode
			s.descriptorBarrier = func(operation string) error {
				if operation != seam || foreign != nil {
					return nil
				}
				entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(temporaryDirectory)))
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range entries {
					if entry.IsDir() || !strings.HasPrefix(entry.Name(), ".okf-tmp-") {
						continue
					}
					foreignName, wantBytes, foreign = replaceRegularWithSameBytes(t, root, path.Join(temporaryDirectory, entry.Name()))
					wantMode = foreign.Mode()
					return nil
				}
				t.Fatal("scratch source not found")
				return nil
			}

			// Act.
			writeErr := s.writeDurableAtForTest(context.Background(), "scratch-swap-target.bin", []byte("scratch payload"), 0o640)
			s.descriptorBarrier = nil
			recoveryErr := s.cleanupScratchInventory(context.Background())
			after, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(foreignName)))
			got, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(foreignName)))
			_, targetErr := os.Stat(filepath.Join(root, "scratch-swap-target.bin"))

			// Assert.
			if writeErr == nil || foreign == nil || statErr != nil || readErr != nil || !os.SameFile(foreign, after) || after.Mode() != wantMode || !bytes.Equal(got, wantBytes) || !errors.Is(targetErr, os.ErrNotExist) {
				t.Fatalf("write=%v foreign=%v after=%v stat=%v read=%v mode=%v/%v bytes=%t target=%v recovery=%v", writeErr, foreign, after, statErr, readErr, after.Mode(), wantMode, bytes.Equal(got, wantBytes), targetErr, recoveryErr)
			}
			if seam == "scratch_ready_restat" && !errors.Is(recoveryErr, errArtifactClaimConflict) {
				t.Fatalf("bound foreign recovery=%v, want conflict", recoveryErr)
			}
		})
	}
}

func TestProofLastTailCutsConvergeTwice(t *testing.T) {
	regularSeams := []string{"claim_object_removed", "claim_object_parent_synced", "artifact_proof_parent_synced", "artifact_binding_removed"}
	for seamIndex, seam := range regularSeams {
		t.Run("scratch/"+seam, func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			name := path.Join(temporaryDirectory, fmt.Sprintf(".okf-tmp-%d-%d", 840000000+seamIndex, seamIndex))
			if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(temporaryDirectory)), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte("proof-last"), 0o640); err != nil {
				t.Fatal(err)
			}
			owned, err := s.rootFD.Lstat(name)
			if err != nil {
				t.Fatal(err)
			}
			operation := internalArtifactOperation("r4-proof-last/" + seam)
			key, _ := newArtifactClaimKey(operation, name, claimScratch)
			if _, err := s.prepareRegularWitnessFrom(context.Background(), key, name, owned); err != nil {
				t.Fatal(err)
			}
			injected := errors.New("proof tail cut")
			s.descriptorBarrier = func(operation string) error {
				if operation == seam {
					return injected
				}
				return nil
			}

			// Act.
			firstErr := s.cleanupTempOwned(operation, name, owned)
			s.descriptorBarrier = nil
			recoverErr := s.cleanupScratchInventory(context.Background())
			secondErr := s.cleanupScratchInventory(context.Background())

			// Assert.
			if !errors.Is(firstErr, injected) || recoverErr != nil || secondErr != nil {
				t.Fatalf("first=%v recover=%v second=%v", firstErr, recoverErr, secondErr)
			}
			for _, artifact := range []string{name, key.claimPath(), key.witnessPath(false), key.bindingPath()} {
				if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(artifact))); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("residue %s: %v", artifact, err)
				}
			}
		})
	}

	t.Run("scratch/noncanonical-proof-is-preserved", func(t *testing.T) {
		// Arrange: the legacy tuple is syntactically safe but not a canonical
		// attempt-local scratch name.
		root, s := adversarialStore(t, Config{})
		name := path.Join(temporaryDirectory, ".okf-tmp-proof-last-legacy")
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(temporaryDirectory)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte("legacy proof"), 0o640); err != nil {
			t.Fatal(err)
		}
		owned, _ := s.rootFD.Lstat(name)
		key, _ := newArtifactClaimKey(internalArtifactOperation("r4-proof-last/legacy"), name, claimScratch)
		if _, err := s.prepareRegularWitnessFrom(context.Background(), key, name, owned); err != nil {
			t.Fatal(err)
		}
		paths := []string{name, key.witnessPath(false), key.bindingPath()}
		before := exactFileStates(t, root, paths)

		// Act.
		err := s.cleanupScratchInventory(context.Background())
		after := exactFileStates(t, root, paths)

		// Assert.
		if !errors.Is(err, errArtifactClaimConflict) || !reflect.DeepEqual(before, after) {
			t.Fatalf("cleanup=%v state=%v/%v", err, before, after)
		}
	})

	t.Run("scratch/identity-mismatch-is-preserved", func(t *testing.T) {
		// Arrange.
		root, s := adversarialStore(t, Config{})
		name := path.Join(temporaryDirectory, ".okf-tmp-840000010-10")
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(temporaryDirectory)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte("identity proof"), 0o640); err != nil {
			t.Fatal(err)
		}
		owned, _ := s.rootFD.Lstat(name)
		key, _ := newArtifactClaimKey(internalArtifactOperation("r4-proof-last/identity"), name, claimScratch)
		if _, err := s.prepareRegularWitnessFrom(context.Background(), key, name, owned); err != nil {
			t.Fatal(err)
		}
		replaceRegularWithSameBytes(t, root, name)
		paths := []string{name, key.witnessPath(false), key.bindingPath()}
		before := exactFileStates(t, root, paths)

		// Act.
		err := s.cleanupScratchInventory(context.Background())
		after := exactFileStates(t, root, paths)

		// Assert.
		if !errors.Is(err, errArtifactClaimConflict) || !reflect.DeepEqual(before, after) {
			t.Fatalf("cleanup=%v state=%v/%v", err, before, after)
		}
	})

	t.Run("scratch/visible-hardlink-survives-consume", func(t *testing.T) {
		// Arrange.
		root, s := adversarialStore(t, Config{})
		name := path.Join(temporaryDirectory, ".okf-tmp-840000011-11")
		absolute := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte("contained proof"), 0o640); err != nil {
			t.Fatal(err)
		}
		owned, _ := s.rootFD.Lstat(name)
		key, _ := newArtifactClaimKey(internalArtifactOperation("r4-proof-last/visible-hardlink"), name, claimScratch)
		if _, err := s.prepareRegularWitnessFrom(context.Background(), key, name, owned); err != nil {
			t.Fatal(err)
		}
		visible := filepath.Join(root, "visible-proof-last.md")
		if err := os.Link(absolute, visible); err != nil {
			t.Fatal(err)
		}
		visibleBefore, _ := os.Lstat(visible)

		// Act.
		err := s.cleanupScratchInventory(context.Background())
		visibleAfter, statErr := os.Lstat(visible)
		raw, readErr := os.ReadFile(visible)

		// Assert.
		if err != nil || statErr != nil || readErr != nil || !os.SameFile(visibleBefore, visibleAfter) || visibleAfter.Mode() != visibleBefore.Mode() || string(raw) != "contained proof" {
			t.Fatalf("cleanup=%v stat=%v read=%v same=%t mode=%v/%v bytes=%q", err, statErr, readErr, statErr == nil && os.SameFile(visibleBefore, visibleAfter), visibleBefore.Mode(), visibleAfter.Mode(), raw)
		}
		for _, artifact := range []string{name, key.claimPath(), key.witnessPath(false), key.bindingPath()} {
			if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(artifact))); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("scratch residue %s: %v", artifact, err)
			}
		}
	})

	directorySeams := []string{"directory_sentinel_removed", "directory_sentinel_parent_synced", "claim_object_removed", "claim_object_parent_synced", "artifact_binding_removed"}
	for _, seam := range directorySeams {
		t.Run("directory/"+seam, func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			stagePath := path.Join(internalDirectory, "staging", "txn-5123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
			key, _ := newArtifactClaimKey("r4-directory-proof-"+seam, stagePath, claimStageDir)
			_, err := s.createOwnedStageDirectory(context.Background(), key)
			if err != nil {
				t.Fatal(err)
			}
			injected := errors.New("directory proof tail cut")
			s.descriptorBarrier = func(operation string) error {
				if operation == seam {
					return injected
				}
				return nil
			}

			// Act.
			_, firstErr := s.consumeOwnedClaim(context.Background(), key, true)
			s.descriptorBarrier = nil
			recoverErr := s.cleanupStageNamespaceClaims(context.Background())
			secondErr := s.cleanupStageNamespaceClaims(context.Background())

			// Assert.
			if !errors.Is(firstErr, injected) || recoverErr != nil || secondErr != nil {
				t.Fatalf("first=%v recover=%v second=%v", firstErr, recoverErr, secondErr)
			}
			for _, artifact := range []string{stagePath, key.claimPath(), key.bindingPath()} {
				if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(artifact))); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("residue %s: %v", artifact, err)
				}
			}
		})
	}
}

func TestForeignBindingIsPreservedExactly(t *testing.T) {
	for _, role := range []artifactRole{claimScratch, claimStageDir} {
		t.Run(string(role), func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			key, _ := newArtifactClaimKey("r4-foreign-binding", path.Join(temporaryDirectory, ".okf-tmp-foreign-binding"), role)
			absolute := filepath.Join(root, filepath.FromSlash(key.bindingPath()))
			if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
				t.Fatal(err)
			}
			want := []byte(`{"foreign":true}`)
			if err := os.WriteFile(absolute, want, 0o640); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(absolute)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			var cleanupErr error
			if role == claimScratch {
				cleanupErr = s.cleanupScratchInventory(context.Background())
			} else {
				cleanupErr = s.cleanupStageNamespaceClaims(context.Background())
			}
			after, statErr := os.Stat(absolute)
			got, readErr := os.ReadFile(absolute)

			// Assert.
			if cleanupErr != nil || statErr != nil || readErr != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() || !bytes.Equal(got, want) {
				t.Fatalf("cleanup=%v before=%v after=%v stat=%v read=%v mode=%v/%v bytes=%t", cleanupErr, before, after, statErr, readErr, before.Mode(), after.Mode(), bytes.Equal(got, want))
			}
		})
	}
}

func TestPrivateSyncStepClassificationIsClosed(t *testing.T) {
	for _, test := range []struct {
		dir  string
		want Step
	}{
		{dir: claimDirectory, want: StepClaimDirectorySync},
		{dir: path.Join(claimDirectory, "nested"), want: StepClaimDirectorySync},
		{dir: temporaryDirectory, want: StepTempCleanupDirectorySync},
		{dir: path.Join(temporaryDirectory, "nested"), want: StepTempCleanupDirectorySync},
		{dir: path.Join(internalDirectory, "staging"), want: StepStageDirectorySync},
		{dir: path.Join(internalDirectory, "staging", "nested"), want: StepStageDirectorySync},
		{dir: "visible", want: StepDirectorySync},
	} {
		t.Run(string(test.want)+"/"+test.dir, func(t *testing.T) {
			// Arrange.
			_, s := adversarialStore(t, Config{})
			injected := errors.New("classification stop")
			var got Step
			s.config.Fault = func(step Step) error {
				got = step
				return injected
			}

			// Act.
			err := s.syncDirAtObserved(test.dir, nil)

			// Assert.
			if !errors.Is(err, injected) || got != test.want {
				t.Fatalf("dir=%s step=%s want=%s error=%v", test.dir, got, test.want, err)
			}
		})
	}
}

func TestVisibleOverwriteCrashRetryMatrix(t *testing.T) {
	for _, seam := range []string{
		"visible_before_target_witness",
		"visible_after_target_witness",
		"visible_target_quarantined",
		"visible_before_install",
		"visible_before_installed_binding",
		"visible_installed_binding_ready",
		"visible_source_installed",
		"visible_parent_synced",
		"visible_claim_removed",
		"visible_claim_parent_synced",
		"visible_witness_parent_synced",
		"visible_binding_removed",
		"visible_binding_parent_synced",
		"visible_installed_witness_removed",
		"visible_installed_witness_parent_synced",
		"visible_installed_binding_removed",
		"visible_installed_binding_parent_synced",
	} {
		t.Run(seam, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root, s := adversarialStore(t, Config{})
			base := adversarialSnapshot(t, s)
			next := nextSnapshotForDurabilityTest(t, s)
			receipt := testJournalReceipt(base.Revision(), next.Revision(), "visible-"+seam, "")
			oldPath := filepath.Join(root, "a.md")
			oldInfo, err := os.Stat(oldPath)
			if err != nil {
				t.Fatal(err)
			}
			oldBytes, err := os.ReadFile(oldPath)
			if err != nil {
				t.Fatal(err)
			}
			injected := errors.New("visible crash cut")
			fired := false
			s.descriptorBarrier = func(operation string) error {
				if operation == seam && !fired {
					fired = true
					return injected
				}
				return nil
			}

			// Act.
			publishErr := s.publish(context.Background(), next, receipt)
			s.descriptorBarrier = nil
			immediateInfo, immediateStatErr := os.Stat(oldPath)
			immediateBytes, immediateReadErr := os.ReadFile(oldPath)
			reopened, reopenErr := openObserved(root, Config{})
			if reopenErr == nil {
				registerStoreCleanup(t, reopened)
			}
			var recovered store.Snapshot
			if reopenErr == nil {
				recovered, reopenErr = reopened.Snapshot(context.Background())
			}
			second, secondErr := openObserved(root, Config{})
			if secondErr == nil {
				registerStoreCleanup(t, second)
			}
			var committed *store.CommittedError

			// Assert.
			if !fired || !errors.Is(publishErr, injected) || !errors.As(publishErr, &committed) || committed.Receipt().RequestDigest != receipt.RequestDigest {
				t.Fatalf("fired=%t publish=%v committed=%v", fired, publishErr, committed)
			}
			if seam == "visible_target_quarantined" || seam == "visible_before_install" || seam == "visible_before_installed_binding" || seam == "visible_installed_binding_ready" {
				if immediateStatErr != nil || immediateReadErr != nil || !os.SameFile(oldInfo, immediateInfo) || oldInfo.Mode() != immediateInfo.Mode() || !bytes.Equal(oldBytes, immediateBytes) {
					t.Fatalf("rollback before=%v after=%v stat=%v read=%v mode=%v/%v bytes=%t", oldInfo, immediateInfo, immediateStatErr, immediateReadErr, oldInfo.Mode(), immediateInfo.Mode(), bytes.Equal(oldBytes, immediateBytes))
				}
			}
			if reopenErr != nil || secondErr != nil || recovered == nil || recovered.Revision() != next.Revision() {
				t.Fatalf("reopen=%v second=%v recovered=%v want=%s", reopenErr, secondErr, recovered, next.Revision())
			}
			visibleKey, _ := newArtifactClaimKey(receipt.RequestDigest, "a.md", claimVisible)
			for _, artifact := range []string{visibleKey.claimPath(), visibleKey.witnessPath(false), visibleKey.bindingPath()} {
				if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(artifact))); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("visible residue %s: %v", artifact, err)
				}
			}
		})
	}
}

func TestVisibleCreateCrashRetryMatrix(t *testing.T) {
	for _, seam := range []string{"visible_before_install", "visible_source_installed", "visible_parent_synced"} {
		t.Run(seam, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root, s := adversarialStore(t, Config{})
			base := adversarialSnapshot(t, s)
			next := createNextSnapshot(t, s, base)
			receipt := testJournalReceipt(base.Revision(), next.Revision(), "visible-create-"+seam, "")
			unrelatedPath := filepath.Join(root, "b.md")
			unrelatedInfo, _ := os.Stat(unrelatedPath)
			unrelatedBytes, _ := os.ReadFile(unrelatedPath)
			injected := errors.New("visible create crash cut")
			fired := false
			s.descriptorBarrier = func(operation string) error {
				if operation == seam && !fired {
					fired = true
					return injected
				}
				return nil
			}

			// Act.
			publishErr := s.publish(context.Background(), next, receipt)
			s.descriptorBarrier = nil
			reopened, reopenErr := openObserved(root, Config{})
			if reopenErr == nil {
				registerStoreCleanup(t, reopened)
			}
			var recovered store.Snapshot
			if reopenErr == nil {
				recovered, reopenErr = reopened.Snapshot(context.Background())
			}
			second, secondErr := openObserved(root, Config{})
			if secondErr == nil {
				registerStoreCleanup(t, second)
			}
			unrelatedAfter, unrelatedStatErr := os.Stat(unrelatedPath)
			unrelatedAfterBytes, unrelatedReadErr := os.ReadFile(unrelatedPath)
			var committed *store.CommittedError

			// Assert.
			if !fired || !errors.Is(publishErr, injected) || !errors.As(publishErr, &committed) || committed.Receipt().RequestDigest != receipt.RequestDigest {
				t.Fatalf("fired=%t publish=%v committed=%v", fired, publishErr, committed)
			}
			if reopenErr != nil || secondErr != nil || recovered == nil || recovered.Revision() != next.Revision() {
				t.Fatalf("reopen=%v second=%v recovered=%v want=%s", reopenErr, secondErr, recovered, next.Revision())
			}
			if unrelatedStatErr != nil || unrelatedReadErr != nil || !os.SameFile(unrelatedInfo, unrelatedAfter) || unrelatedInfo.Mode() != unrelatedAfter.Mode() || !bytes.Equal(unrelatedBytes, unrelatedAfterBytes) {
				t.Fatalf("unrelated changed: stat=%v read=%v same=%t mode=%v/%v bytes=%t", unrelatedStatErr, unrelatedReadErr, os.SameFile(unrelatedInfo, unrelatedAfter), unrelatedInfo.Mode(), unrelatedAfter.Mode(), bytes.Equal(unrelatedBytes, unrelatedAfterBytes))
			}
		})
	}
}

func TestVisibleForeignSwapMatrix(t *testing.T) {
	for _, kind := range []string{"old target after witness", "foreign destination and restore collision", "foreign source", "installed target swapped"} {
		t.Run(kind, func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			base := adversarialSnapshot(t, s)
			next := nextSnapshotForDurabilityTest(t, s)
			receipt := testJournalReceipt(base.Revision(), next.Revision(), "visible-foreign-"+kind, "")
			oldPath := filepath.Join(root, "a.md")
			oldInfo, _ := os.Stat(oldPath)
			oldBytes, _ := os.ReadFile(oldPath)
			var foreign os.FileInfo
			var foreignPath string
			var foreignBytes []byte
			var foreignMode os.FileMode
			fired := false
			s.descriptorBarrier = func(operation string) error {
				if fired {
					return nil
				}
				switch kind {
				case "old target after witness":
					if operation != "visible_after_target_witness" {
						return nil
					}
					foreignPath, foreignBytes, foreign = replaceRegularWithSameBytes(t, root, "a.md")
				case "foreign destination and restore collision":
					if operation != "visible_target_quarantined" {
						return nil
					}
					foreignPath = "a.md"
					if err := os.WriteFile(oldPath, oldBytes, 0o640); err != nil {
						t.Fatal(err)
					}
					foreignBytes = append([]byte(nil), oldBytes...)
					foreign, _ = os.Stat(oldPath)
				case "foreign source":
					if operation != "visible_before_install" {
						return nil
					}
					entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(temporaryDirectory)))
					if err != nil {
						t.Fatal(err)
					}
					for _, entry := range entries {
						if strings.HasPrefix(entry.Name(), ".okf-tmp-") {
							foreignPath, foreignBytes, foreign = replaceRegularWithSameBytes(t, root, path.Join(temporaryDirectory, entry.Name()))
							break
						}
					}
				case "installed target swapped":
					if operation != "visible_source_installed" {
						return nil
					}
					foreignPath, foreignBytes, foreign = replaceRegularWithSameBytes(t, root, "a.md")
				}
				if foreign != nil {
					foreignMode = foreign.Mode()
					fired = true
				}
				return nil
			}

			// Act.
			publishErr := s.publish(context.Background(), next, receipt)
			s.descriptorBarrier = nil
			foreignAfter, foreignStatErr := os.Stat(filepath.Join(root, filepath.FromSlash(foreignPath)))
			foreignAfterBytes, foreignReadErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(foreignPath)))
			oldAfter, oldStatErr := os.Stat(oldPath)
			oldAfterBytes, oldReadErr := os.ReadFile(oldPath)
			_, reopenErr := openObserved(root, Config{})
			var committed *store.CommittedError
			var recoveredCommitted *store.CommittedError

			// Assert.
			if !fired || foreign == nil || publishErr == nil || !errors.As(publishErr, &committed) || committed.Receipt().RequestDigest != receipt.RequestDigest {
				t.Fatalf("kind=%s fired=%t foreign=%v publish=%v committed=%v", kind, fired, foreign, publishErr, committed)
			}
			if foreignStatErr != nil || foreignReadErr != nil || !os.SameFile(foreign, foreignAfter) || foreignAfter.Mode() != foreignMode || !bytes.Equal(foreignBytes, foreignAfterBytes) {
				t.Fatalf("foreign changed: stat=%v read=%v same=%t mode=%v/%v bytes=%t", foreignStatErr, foreignReadErr, os.SameFile(foreign, foreignAfter), foreignAfter.Mode(), foreignMode, bytes.Equal(foreignBytes, foreignAfterBytes))
			}
			if kind == "foreign source" && (oldStatErr != nil || oldReadErr != nil || !os.SameFile(oldInfo, oldAfter) || oldInfo.Mode() != oldAfter.Mode() || !bytes.Equal(oldBytes, oldAfterBytes)) {
				t.Fatalf("rollback changed old target: stat=%v read=%v same=%t mode=%v/%v bytes=%t", oldStatErr, oldReadErr, os.SameFile(oldInfo, oldAfter), oldInfo.Mode(), oldAfter.Mode(), bytes.Equal(oldBytes, oldAfterBytes))
			}
			if kind == "foreign destination and restore collision" {
				key, _ := newArtifactClaimKey(receipt.RequestDigest, "a.md", claimVisible)
				claim, claimErr := os.Stat(filepath.Join(root, filepath.FromSlash(key.claimPath())))
				if claimErr != nil || !os.SameFile(oldInfo, claim) {
					t.Fatalf("old quarantine lost: claim=%v error=%v", claim, claimErr)
				}
			}
			if reopenErr == nil || !errors.As(reopenErr, &recoveredCommitted) || recoveredCommitted.Receipt().RequestDigest != receipt.RequestDigest {
				t.Fatalf("reopen=%v committed=%v", reopenErr, recoveredCommitted)
			}
		})
	}
}

func TestVisibleCreateForeignDestinationIsPreserved(t *testing.T) {
	// Arrange.
	root, s := adversarialStore(t, Config{})
	base := adversarialSnapshot(t, s)
	next := createNextSnapshot(t, s, base)
	receipt := testJournalReceipt(base.Revision(), next.Revision(), "visible-create-foreign", "")
	var foreign os.FileInfo
	var want []byte
	s.descriptorBarrier = func(operation string) error {
		if operation != "visible_before_install" || foreign != nil {
			return nil
		}
		want = []byte(adversarialDocument("created"))
		if err := os.WriteFile(filepath.Join(root, "c.md"), want, 0o640); err != nil {
			t.Fatal(err)
		}
		foreign, _ = os.Stat(filepath.Join(root, "c.md"))
		return nil
	}

	// Act.
	publishErr := s.publish(context.Background(), next, receipt)
	s.descriptorBarrier = nil
	after, statErr := os.Stat(filepath.Join(root, "c.md"))
	got, readErr := os.ReadFile(filepath.Join(root, "c.md"))
	_, reopenErr := openObserved(root, Config{})
	var committed, recoveredCommitted *store.CommittedError

	// Assert.
	if foreign == nil || publishErr == nil || !errors.As(publishErr, &committed) || committed.Receipt().RequestDigest != receipt.RequestDigest {
		t.Fatalf("foreign=%v publish=%v committed=%v", foreign, publishErr, committed)
	}
	if statErr != nil || readErr != nil || !os.SameFile(foreign, after) || after.Mode() != foreign.Mode() || !bytes.Equal(want, got) {
		t.Fatalf("foreign changed: stat=%v read=%v same=%t mode=%v/%v bytes=%t", statErr, readErr, os.SameFile(foreign, after), after.Mode(), foreign.Mode(), bytes.Equal(want, got))
	}
	if reopenErr == nil || !errors.As(reopenErr, &recoveredCommitted) || recoveredCommitted.Receipt().RequestDigest != receipt.RequestDigest {
		t.Fatalf("reopen=%v committed=%v", reopenErr, recoveredCommitted)
	}
}

func TestVisibleRollbackTailRetryMatrix(t *testing.T) {
	for _, seam := range []string{
		"visible_claim_removed",
		"visible_claim_parent_synced",
		"visible_witness_removed",
		"visible_witness_parent_synced",
		"visible_binding_removed",
		"visible_binding_parent_synced",
	} {
		t.Run(seam, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root, s := adversarialStore(t, Config{})
			base := adversarialSnapshot(t, s)
			next := nextSnapshotForDurabilityTest(t, s)
			receipt := testJournalReceipt(base.Revision(), next.Revision(), "visible-rollback-tail-"+seam, "")
			oldInfo, _ := os.Stat(filepath.Join(root, "a.md"))
			oldBytes, _ := os.ReadFile(filepath.Join(root, "a.md"))
			trigger := errors.New("trigger rollback")
			tail := errors.New("rollback tail cut")
			directorySyncs := 0
			s.config.PostFault = func(step Step) error {
				if step == StepDirectorySync {
					directorySyncs++
					if directorySyncs == 2 {
						return tail
					}
				}
				return nil
			}
			s.descriptorBarrier = func(operation string) error {
				if operation == "visible_target_quarantined" {
					return trigger
				}
				return nil
			}

			// Act.
			publishErr := s.publish(context.Background(), next, receipt)
			s.config.PostFault = nil
			fired := false
			s.descriptorBarrier = func(operation string) error {
				if operation == seam && !fired {
					fired = true
					return tail
				}
				return nil
			}
			firstErr := s.recoverForTest(context.Background())
			s.descriptorBarrier = nil
			secondErr := s.recoverForTest(context.Background())
			thirdErr := s.recoverForTest(context.Background())
			visible, snapshotErr := s.Snapshot(context.Background())
			var firstCommitted *store.CommittedError
			immediate, statErr := os.Stat(filepath.Join(root, "a.md"))
			immediateBytes, readErr := os.ReadFile(filepath.Join(root, "a.md"))

			// Assert.
			if !errors.Is(publishErr, trigger) || !errors.Is(publishErr, tail) || directorySyncs != 2 {
				t.Fatalf("publish=%v syncs=%d", publishErr, directorySyncs)
			}
			if !fired || !errors.Is(firstErr, tail) || !errors.As(firstErr, &firstCommitted) || firstCommitted.Receipt().RequestDigest != receipt.RequestDigest {
				t.Fatalf("fired=%t first=%v committed=%v", fired, firstErr, firstCommitted)
			}
			if secondErr != nil || thirdErr != nil || snapshotErr != nil || visible == nil || visible.Revision() != next.Revision() {
				t.Fatalf("second=%v third=%v snapshot=%v visible=%v", secondErr, thirdErr, snapshotErr, visible)
			}
			if statErr != nil || readErr != nil || os.SameFile(oldInfo, immediate) || bytes.Equal(oldBytes, immediateBytes) {
				t.Fatalf("result not installed: stat=%v read=%v old-inode=%t old-bytes=%t", statErr, readErr, os.SameFile(oldInfo, immediate), bytes.Equal(oldBytes, immediateBytes))
			}
		})
	}
}

func createNextSnapshot(t *testing.T, s *Store, base store.Snapshot) *snapshot {
	t.Helper()
	a, err := base.ReadFile(context.Background(), "a.md")
	if err != nil {
		t.Fatal(err)
	}
	b, err := base.ReadFile(context.Background(), "b.md")
	if err != nil {
		t.Fatal(err)
	}
	next, err := newSnapshotWithAlgorithm(context.Background(), map[string][]byte{
		"a.md": a,
		"b.md": b,
		"c.md": []byte(adversarialDocument("created")),
	}, s.config.HashAlgorithm)
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func TestVisibleDeleteCrashRetryMatrix(t *testing.T) {
	for _, seam := range []string{
		"visible_delete_before_target_witness",
		"visible_delete_after_target_witness",
		"visible_delete_claimed",
		"visible_delete_parent_synced",
		"visible_claim_removed",
		"visible_claim_parent_synced",
		"visible_witness_removed",
		"visible_witness_parent_synced",
		"visible_binding_removed",
		"visible_binding_parent_synced",
	} {
		t.Run(seam, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root, s := adversarialStore(t, Config{})
			base := adversarialSnapshot(t, s)
			next := deleteNextSnapshot(t, s, base)
			receipt := testJournalReceipt(base.Revision(), next.Revision(), "visible-delete-"+seam, "")
			oldPath := filepath.Join(root, "b.md")
			oldInfo, _ := os.Stat(oldPath)
			oldBytes, _ := os.ReadFile(oldPath)
			unrelatedInfo, _ := os.Stat(filepath.Join(root, "a.md"))
			unrelatedBytes, _ := os.ReadFile(filepath.Join(root, "a.md"))
			injected := errors.New("visible delete cut")
			fired := false
			s.descriptorBarrier = func(operation string) error {
				if operation == seam && !fired {
					fired = true
					return injected
				}
				return nil
			}

			// Act.
			publishErr := s.publish(context.Background(), next, receipt)
			s.descriptorBarrier = nil
			immediate, immediateStatErr := os.Stat(oldPath)
			immediateBytes, immediateReadErr := os.ReadFile(oldPath)
			reopened, reopenErr := openObserved(root, Config{})
			if reopenErr == nil {
				registerStoreCleanup(t, reopened)
			}
			var recovered store.Snapshot
			if reopenErr == nil {
				recovered, reopenErr = reopened.Snapshot(context.Background())
			}
			second, secondErr := openObserved(root, Config{})
			if secondErr == nil {
				registerStoreCleanup(t, second)
			}
			unrelatedAfter, unrelatedStatErr := os.Stat(filepath.Join(root, "a.md"))
			unrelatedAfterBytes, unrelatedReadErr := os.ReadFile(filepath.Join(root, "a.md"))
			var committed *store.CommittedError

			// Assert.
			if !fired || !errors.Is(publishErr, injected) || !errors.As(publishErr, &committed) || committed.Receipt().RequestDigest != receipt.RequestDigest {
				t.Fatalf("fired=%t publish=%v committed=%v", fired, publishErr, committed)
			}
			if seam == "visible_delete_before_target_witness" || seam == "visible_delete_after_target_witness" {
				if immediateStatErr != nil || immediateReadErr != nil || !os.SameFile(oldInfo, immediate) || oldInfo.Mode() != immediate.Mode() || !bytes.Equal(oldBytes, immediateBytes) {
					t.Fatalf("preclaim target changed: stat=%v read=%v same=%t mode=%v/%v bytes=%t", immediateStatErr, immediateReadErr, os.SameFile(oldInfo, immediate), oldInfo.Mode(), immediate.Mode(), bytes.Equal(oldBytes, immediateBytes))
				}
			}
			if reopenErr != nil || secondErr != nil || recovered == nil || recovered.Revision() != next.Revision() {
				t.Fatalf("reopen=%v second=%v recovered=%v want=%s", reopenErr, secondErr, recovered, next.Revision())
			}
			if _, err := os.Stat(oldPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("deleted path remains: %v", err)
			}
			if unrelatedStatErr != nil || unrelatedReadErr != nil || !os.SameFile(unrelatedInfo, unrelatedAfter) || unrelatedInfo.Mode() != unrelatedAfter.Mode() || !bytes.Equal(unrelatedBytes, unrelatedAfterBytes) {
				t.Fatalf("unrelated changed: stat=%v read=%v same=%t mode=%v/%v bytes=%t", unrelatedStatErr, unrelatedReadErr, os.SameFile(unrelatedInfo, unrelatedAfter), unrelatedInfo.Mode(), unrelatedAfter.Mode(), bytes.Equal(unrelatedBytes, unrelatedAfterBytes))
			}
		})
	}
}

func TestVisibleDeleteForeignSwapMatrix(t *testing.T) {
	for _, seam := range []string{"visible_delete_after_target_witness", "visible_delete_claimed"} {
		t.Run(seam, func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			base := adversarialSnapshot(t, s)
			next := deleteNextSnapshot(t, s, base)
			receipt := testJournalReceipt(base.Revision(), next.Revision(), "visible-delete-foreign-"+seam, "")
			oldInfo, _ := os.Stat(filepath.Join(root, "b.md"))
			oldBytes, _ := os.ReadFile(filepath.Join(root, "b.md"))
			var foreign os.FileInfo
			var want []byte
			var wantMode os.FileMode
			s.descriptorBarrier = func(operation string) error {
				if operation != seam || foreign != nil {
					return nil
				}
				if seam == "visible_delete_claimed" {
					want = append([]byte(nil), oldBytes...)
					if err := os.WriteFile(filepath.Join(root, "b.md"), want, 0o640); err != nil {
						t.Fatal(err)
					}
					foreign, _ = os.Stat(filepath.Join(root, "b.md"))
				} else {
					_, want, foreign = replaceRegularWithSameBytes(t, root, "b.md")
				}
				wantMode = foreign.Mode()
				return nil
			}

			// Act.
			publishErr := s.publish(context.Background(), next, receipt)
			s.descriptorBarrier = nil
			after, statErr := os.Stat(filepath.Join(root, "b.md"))
			got, readErr := os.ReadFile(filepath.Join(root, "b.md"))
			_, reopenErr := openObserved(root, Config{})
			var committed, recoveredCommitted *store.CommittedError

			// Assert.
			if foreign == nil || publishErr == nil || !errors.As(publishErr, &committed) || committed.Receipt().RequestDigest != receipt.RequestDigest {
				t.Fatalf("foreign=%v publish=%v committed=%v", foreign, publishErr, committed)
			}
			if statErr != nil || readErr != nil || !os.SameFile(foreign, after) || after.Mode() != wantMode || !bytes.Equal(want, got) {
				t.Fatalf("foreign changed: stat=%v read=%v same=%t mode=%v/%v bytes=%t", statErr, readErr, os.SameFile(foreign, after), after.Mode(), wantMode, bytes.Equal(want, got))
			}
			if seam == "visible_delete_claimed" {
				key, _ := newArtifactClaimKey(receipt.RequestDigest, "b.md", claimVisible)
				claim, claimErr := os.Stat(filepath.Join(root, filepath.FromSlash(key.claimPath())))
				if claimErr != nil || !os.SameFile(oldInfo, claim) {
					t.Fatalf("old quarantine lost: claim=%v error=%v", claim, claimErr)
				}
			}
			if reopenErr == nil || !errors.As(reopenErr, &recoveredCommitted) || recoveredCommitted.Receipt().RequestDigest != receipt.RequestDigest {
				t.Fatalf("reopen=%v committed=%v", reopenErr, recoveredCommitted)
			}
		})
	}
}

func TestVisibleDeleteAbsentExpectation(t *testing.T) {
	// Arrange.
	root, s := adversarialStore(t, Config{})
	expected := mutationTargetIdentity{absent: true, operation: "visible-delete-absent"}

	// Act/Assert: a proven absence is idempotent.
	if err := s.removeVisibleGuarded(context.Background(), "missing.md", expected); err != nil {
		t.Fatalf("absent delete: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "missing.md"), []byte("foreign"), 0o640); err != nil {
		t.Fatal(err)
	}
	foreign, _ := os.Stat(filepath.Join(root, "missing.md"))
	deleteErr := s.removeVisibleGuarded(context.Background(), "missing.md", expected)
	after, statErr := os.Stat(filepath.Join(root, "missing.md"))
	if deleteErr == nil || statErr != nil || !os.SameFile(foreign, after) {
		t.Fatalf("changed absence delete=%v foreign=%v stat=%v", deleteErr, foreign, statErr)
	}
}

func deleteNextSnapshot(t *testing.T, s *Store, base store.Snapshot) *snapshot {
	t.Helper()
	a, err := base.ReadFile(context.Background(), "a.md")
	if err != nil {
		t.Fatal(err)
	}
	next, err := newSnapshotWithAlgorithm(context.Background(), map[string][]byte{"a.md": a}, s.config.HashAlgorithm)
	if err != nil {
		t.Fatal(err)
	}
	return next
}
