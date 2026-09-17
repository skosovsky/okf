package fs

// Closed proof requirements for durable artifacts.

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

	"github.com/skosovsky/okf/store"
)

func TestRecoveryRequiresClosedDirectoryProof(t *testing.T) {
	for _, test := range []struct {
		name     string
		build    bool
		binding  bool
		mismatch bool
		source   bool
	}{
		{name: "canonical sentinel without binding"},
		{name: "build sentinel without binding", build: true},
		{name: "canonical binding mismatch", binding: true, mismatch: true},
		{name: "canonical directory source", binding: true, source: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			operation := "sha256:" + strings.Repeat("6", 64)
			payloadDir, err := journalStage(operation)
			if err != nil {
				t.Fatal(err)
			}
			directory := path.Dir(payloadDir)
			key, err := newArtifactClaimKey(operation, directory, claimStageDir)
			if err != nil {
				t.Fatal(err)
			}
			name := directory
			if test.build {
				name = key.buildPath()
			}
			absolute := filepath.Join(root, filepath.FromSlash(name))
			if err := os.MkdirAll(absolute, 0o750); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(absolute)
			if err != nil {
				t.Fatal(err)
			}
			identity, ok := fileIdentityKey(before)
			if !ok {
				t.Fatal("directory identity unavailable")
			}
			record := directoryWitnessRecord{Version: 1, Operation: operation, Path: directory, Role: claimStageDir, Identity: identity}
			if test.source {
				record.Source = path.Join(temporaryDirectory, "forged-source")
			}
			sentinelRaw, _ := json.Marshal(record)
			if err := os.WriteFile(filepath.Join(absolute, directorySentinelName), sentinelRaw, 0o600); err != nil {
				t.Fatal(err)
			}
			if test.binding {
				binding := record
				if test.mismatch {
					binding.Identity = "different-inode"
				}
				bindingRaw, _ := json.Marshal(binding)
				if err := os.MkdirAll(filepath.Dir(filepath.Join(root, filepath.FromSlash(key.bindingPath()))), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(key.bindingPath())), bindingRaw, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			claimsBefore := claimInventory(t, root)
			mutationHooks := 0
			s.config.Fault = func(step Step) error {
				switch step {
				case StepRemove, StepStageCleanupPayloadRemove, StepStageCleanupPayloadDirRemove, StepStageCleanupStageDirRemove:
					mutationHooks++
				}
				return nil
			}

			// Act.
			_, recoveryErr := s.Snapshot(context.Background())
			after, statErr := os.Stat(absolute)
			afterSentinel, readErr := os.ReadFile(filepath.Join(absolute, directorySentinelName))
			claimsAfter := claimInventory(t, root)

			// Assert.
			claimsEqual := strings.Join(claimsBefore, "\x00") == strings.Join(claimsAfter, "\x00")
			if !errors.Is(recoveryErr, errArtifactClaimConflict) || statErr != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() || readErr != nil || !reflect.DeepEqual(afterSentinel, sentinelRaw) || !claimsEqual || mutationHooks != 0 {
				t.Fatalf("recovery=%v is_conflict=%t stat=%v same=%t mode=%v/%v read=%v sentinel=%t claims=%v/%v claims_equal=%t hooks=%d", recoveryErr, errors.Is(recoveryErr, errArtifactClaimConflict), statErr, statErr == nil && os.SameFile(before, after), before.Mode(), modeOf(after), readErr, reflect.DeepEqual(afterSentinel, sentinelRaw), claimsBefore, claimsAfter, claimsEqual, mutationHooks)
			}
		})
	}
}

func TestRecoveryConsumesExactDirectoryProof(t *testing.T) {
	// Arrange.
	root, s := adversarialStore(t, Config{})
	operation := "sha256:" + strings.Repeat("7", 64)
	payloadDir, err := journalStage(operation)
	if err != nil {
		t.Fatal(err)
	}
	directory := path.Dir(payloadDir)
	key, err := newArtifactClaimKey(operation, directory, claimStageDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.createOwnedStageDirectory(context.Background(), key); err != nil {
		t.Fatal(err)
	}

	// Act.
	_, recoveryErr := s.Snapshot(context.Background())
	_, directoryErr := os.Stat(filepath.Join(root, filepath.FromSlash(directory)))
	claims := claimInventory(t, root)

	// Assert.
	if recoveryErr != nil || !errors.Is(directoryErr, os.ErrNotExist) || len(claims) != 0 {
		t.Fatalf("recovery=%v directory=%v claims=%v", recoveryErr, directoryErr, claims)
	}
}

func TestVisibleWitnessWithoutBindingIsPreserved(t *testing.T) {
	for _, test := range []struct {
		name   string
		target string
	}{
		{name: "target and witness same inode", target: "a.md"},
		{name: "preinstall witness", target: "b.md"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			root, s, base, _ := stageRecoveryJournal(t, Config{})
			operation := "sha256:" + strings.Repeat("0", 64)
			key, err := newArtifactClaimKey(operation, test.target, claimVisibleInstall)
			if err != nil {
				t.Fatal(err)
			}
			witnessName := filepath.Join(root, filepath.FromSlash(key.witnessPath(false)))
			if test.target == "a.md" {
				if err := os.Link(filepath.Join(root, test.target), witnessName); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(witnessName, []byte("unbound preinstall"), 0o640); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(witnessName)
			if err != nil {
				t.Fatal(err)
			}
			beforeRaw, _ := os.ReadFile(witnessName)
			claimsBefore := claimInventory(t, root)

			// Act.
			_, recoveryErr := s.Snapshot(context.Background())
			after, statErr := os.Stat(witnessName)
			afterRaw, readErr := os.ReadFile(witnessName)
			claimsAfter := claimInventory(t, root)
			visible, visibleErr := readVisibleRoot(context.Background(), s.rootFD)

			// Assert.
			var committed *store.CommittedError
			if !errors.Is(recoveryErr, errArtifactClaimConflict) || !errors.As(recoveryErr, &committed) || statErr != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() || readErr != nil || !reflect.DeepEqual(beforeRaw, afterRaw) || strings.Join(claimsBefore, "\x00") != strings.Join(claimsAfter, "\x00") || visibleErr != nil || !sameVisibleFiles(visible, base.(*snapshot).files) {
				t.Fatalf("recovery=%v committed=%v stat=%v same=%t mode=%v/%v read=%v bytes=%t claims=%v/%v visible=%v", recoveryErr, committed, statErr, statErr == nil && os.SameFile(before, after), before.Mode(), modeOf(after), readErr, reflect.DeepEqual(beforeRaw, afterRaw), claimsBefore, claimsAfter, visibleErr)
			}
		})
	}
}

func TestDurableJournalFreshVisibleInstallIsBindingFirst(t *testing.T) {
	// Arrange.
	root, s, _, next := stageRecoveryJournal(t, Config{})
	var phases []string
	s.descriptorBarrier = func(phase string) error {
		if strings.HasPrefix(phase, "visible_installed_binding_ready") || strings.HasPrefix(phase, "visible_source_installed") {
			phases = append(phases, phase)
		}
		return nil
	}

	// Act.
	snapshot, recoveryErr := s.Snapshot(context.Background())
	s.descriptorBarrier = nil
	claims := claimInventory(t, root)

	// Assert.
	firstBinding, firstInstall := -1, -1
	for i, phase := range phases {
		if firstBinding < 0 && strings.HasPrefix(phase, "visible_installed_binding_ready") {
			firstBinding = i
		}
		if firstInstall < 0 && strings.HasPrefix(phase, "visible_source_installed") {
			firstInstall = i
		}
	}
	if recoveryErr != nil || snapshot.Revision() != next.Revision() || firstBinding < 0 || firstInstall < 0 || firstBinding >= firstInstall || len(claims) != 0 {
		t.Fatalf("recovery=%v revision=%s/%s phases=%v claims=%v", recoveryErr, snapshot.Revision(), next.Revision(), phases, claims)
	}
}

func TestVisibleExactBindingConverges(t *testing.T) {
	// Arrange.
	root, s, _, next := stageRecoveryJournal(t, Config{})
	operation := "sha256:" + strings.Repeat("0", 64)
	key, err := newArtifactClaimKey(operation, "a.md", claimVisibleInstall)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "a.md")
	if err := os.WriteFile(target, next.files["a.md"], 0o644); err != nil {
		t.Fatal(err)
	}
	owned, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	source := path.Join(temporaryDirectory, ".okf-tmp-1-0")
	if err := os.Link(target, filepath.Join(root, filepath.FromSlash(source))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.prepareRegularWitnessFrom(context.Background(), key, source, owned); err != nil {
		t.Fatal(err)
	}

	// Act.
	snapshot, recoveryErr := s.Snapshot(context.Background())
	claims := claimInventory(t, root)

	// Assert.
	if recoveryErr != nil || snapshot.Revision() != next.Revision() || len(claims) != 0 {
		t.Fatalf("recovery=%v revision=%s/%s claims=%v", recoveryErr, snapshot.Revision(), next.Revision(), claims)
	}
}
