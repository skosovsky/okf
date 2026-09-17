package fs

// Claim-zone path and handle boundaries.

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

var _ func(*Store, claimsZonePath, fs.FileInfo, bool) (bool, error) = (*Store).fdRemoveClaimOwned

func TestClaimsZonePathRejectsNonClaimsBeforeMutation(t *testing.T) {
	for _, name := range []string{"a.md", claimDirectory, claimDirectory + "/../temporary/x", temporaryDirectory + "/x", internalDirectory + "/receipts/x", internalDirectory + "/capabilities/x"} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("claimZonePath(%q) accepted non-claims path", name)
				}
			}()
			_ = claimZonePath(name)
		})
	}
}

func TestClaimsZoneHandleRevalidatesModeAndRootBinding(t *testing.T) {
	for _, test := range []struct {
		name string
		swap bool
	}{
		{name: "mode"},
		{name: "root inode swap", swap: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, s := adversarialStore(t, Config{})
			if err := s.ensureClaimNamespace(context.Background()); err != nil {
				t.Fatal(err)
			}
			name := filepath.Join(root, filepath.FromSlash(claimDirectory), "owned")
			if err := os.WriteFile(name, []byte("owned"), 0o600); err != nil {
				t.Fatal(err)
			}
			owned, err := os.Stat(name)
			if err != nil {
				t.Fatal(err)
			}
			if test.swap {
				s.descriptorBarrier = func(phase string) error {
					if phase != "remove" {
						return nil
					}
					held := filepath.Join(root, filepath.FromSlash(claimDirectory)+".held")
					if err := os.Rename(filepath.Join(root, filepath.FromSlash(claimDirectory)), held); err != nil {
						return err
					}
					return os.Mkdir(filepath.Join(root, filepath.FromSlash(claimDirectory)), 0o700)
				}
			} else if err := os.Chmod(filepath.Join(root, filepath.FromSlash(claimDirectory)), 0o755); err != nil {
				t.Fatal(err)
			}

			removed, removeErr := s.fdRemoveClaimOwned(claimZonePath(claimDirectory+"/owned"), owned, false)

			if removed || !errors.Is(removeErr, errArtifactClaimConflict) {
				t.Fatalf("removed=%t error=%v", removed, removeErr)
			}
			if test.swap {
				if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(claimDirectory)+".held", "owned")); err != nil {
					t.Fatalf("owned inode changed: %v", err)
				}
			} else if after, err := os.Stat(name); err != nil || !os.SameFile(owned, after) {
				t.Fatalf("owned inode changed: after=%v err=%v", after, err)
			}
		})
	}
}
