package fs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestPrivateDirectoryParentSyncFaultMatrix makes the metadata-only repair
// boundary explicit.  Open must not proceed to its capability probes or
// recovery after a parent sync cannot be established, and a subsequent neutral
// Open must converge every private directory and the lease back to owner-only.
func TestPrivateDirectoryParentSyncFaultMatrix(t *testing.T) {
	const privateMode = os.FileMode(0o700)
	const privateFileMode = os.FileMode(0o600)

	type faultKind string
	const (
		pre      faultKind = "pre"
		post     faultKind = "post"
		callback faultKind = "directory-sync-callback"
	)

	privateDirs := []string{
		internalDirectory,
		filepath.Join(internalDirectory, "transactions"),
		filepath.Join(internalDirectory, "staging"),
		filepath.Join(internalDirectory, "receipts"),
		filepath.Join(internalDirectory, "capabilities"),
	}
	cases := make([]struct {
		name   string
		target string
		parent string
		kind   faultKind
	}, 0, len(privateDirs)*3)
	for _, target := range privateDirs {
		parent := filepath.Dir(target)
		for _, kind := range []faultKind{pre, post, callback} {
			cases = append(cases, struct {
				name   string
				target string
				parent string
				kind   faultKind
			}{name: string(kind) + "/" + target, target: target, parent: parent, kind: kind})
		}
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange: first create a clean, complete private namespace.  Corrupt
			// exactly one component so this case has one deterministic repair and
			// exactly one parent-sync hook invocation.
			root := t.TempDir()
			writeTestFile(t, root, "visible.md", adversarialDocument("before"))
			initial, err := Open(root, Config{})
			if err != nil {
				t.Fatal(err)
			}
			if err := initial.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(filepath.Join(root, tt.target), 0o777); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(filepath.Join(root, "visible.md"))
			if err != nil {
				t.Fatal(err)
			}
			journalBefore := privateEntries(t, root, filepath.Join(internalDirectory, "transactions"))
			wantErr := errors.New("private parent sync fault")
			var faults, posts, callbacks int
			config := Config{
				Fault: func(step Step) error {
					if step != StepPrivateDirectoryParentSync {
						return nil
					}
					faults++
					if tt.kind == pre {
						return wantErr
					}
					return nil
				},
				PostFault: func(step Step) error {
					if step != StepPrivateDirectoryParentSync {
						return nil
					}
					posts++
					if tt.kind == post {
						return wantErr
					}
					return nil
				},
				DirectorySync: func(dir string) error {
					if dir != tt.parent {
						return nil
					}
					callbacks++
					if tt.kind == callback {
						return wantErr
					}
					return nil
				},
			}

			// Act.
			s, openErr := Open(root, config)
			if s != nil {
				_ = s.Close()
				t.Fatal("Open returned a store after private parent-sync failure")
			}

			// Assert: the failure happens before probes/recovery can affect private
			// evidence, and never reaches a revision-visible file.
			if !errors.Is(openErr, wantErr) {
				t.Fatalf("Open error=%v, want %v", openErr, wantErr)
			}
			if got, readErr := os.ReadFile(filepath.Join(root, "visible.md")); readErr != nil || string(got) != string(before) {
				t.Fatalf("Open changed visible project: read=%v bytes=%q", readErr, got)
			}
			if got := privateEntries(t, root, filepath.Join(internalDirectory, "transactions")); !sameStrings(got, journalBefore) {
				t.Fatalf("Open changed journal evidence: got=%v want=%v", got, journalBefore)
			}
			for _, dir := range []string{"staging", "capabilities"} {
				if got := privateEntries(t, root, filepath.Join(internalDirectory, dir)); len(got) != 0 {
					t.Fatalf("Open leaked probe or temporary metadata in %s: %v", dir, got)
				}
			}
			wantFaults, wantPosts, wantCallbacks := 1, 0, 0
			if tt.kind != pre {
				wantCallbacks = 1
			}
			if tt.kind == post {
				wantPosts = 1
			}
			if faults != wantFaults || posts != wantPosts || callbacks != wantCallbacks {
				t.Fatalf("hook calls fault/post/callback=%d/%d/%d, want %d/%d/%d", faults, posts, callbacks, wantFaults, wantPosts, wantCallbacks)
			}

			// A neutral reopen repairs every namespace component, not only the
			// component used to exercise this fault.  lease is private metadata,
			// so verify the matching owner-only file invariant as well.
			for _, dir := range privateDirs {
				if err := os.Chmod(filepath.Join(root, dir), 0o777); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Chmod(filepath.Join(root, internalDirectory, "lease"), 0o666); err != nil {
				t.Fatal(err)
			}
			repaired, err := Open(root, Config{})
			if err != nil {
				t.Fatalf("neutral Open error=%v", err)
			}
			defer repaired.Close()
			for _, dir := range privateDirs {
				info, statErr := os.Stat(filepath.Join(root, dir))
				if statErr != nil {
					t.Fatalf("stat private directory %s: %v", dir, statErr)
				}
				if info.Mode().Perm() != privateMode {
					t.Fatalf("private directory %s mode=%v, want 0700", dir, info.Mode())
				}
			}
			lease, statErr := os.Stat(filepath.Join(root, internalDirectory, "lease"))
			if statErr != nil {
				t.Fatalf("stat lease: %v", statErr)
			}
			if lease.Mode().Perm() != privateFileMode {
				t.Fatalf("lease mode=%v, want 0600", lease.Mode())
			}
		})
	}
}

// Fresh .okf creation does not pass through chmod repair, so it needs its own
// parent durability boundary. These cases begin with no private namespace at
// all and exercise that first (root) parent sync.
func TestPrivateDirectoryParentSyncFreshFaultMatrix(t *testing.T) {
	for _, kind := range []string{"pre", "post", "callback"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "visible.md", adversarialDocument("fresh"))
			want := errors.New("fresh parent sync")
			var calls int
			cfg := Config{
				Fault: func(step Step) error {
					if step == StepPrivateDirectoryParentSync && kind == "pre" {
						calls++
						return want
					}
					return nil
				},
				PostFault: func(step Step) error {
					if step == StepPrivateDirectoryParentSync && kind == "post" {
						calls++
						return want
					}
					return nil
				},
				DirectorySync: func(dir string) error {
					if dir == "." && kind == "callback" {
						calls++
						return want
					}
					return nil
				},
			}
			s, err := Open(root, cfg)
			if s != nil {
				_ = s.Close()
				t.Fatal("Open succeeded after fresh private sync failure")
			}
			if !errors.Is(err, want) || calls != 1 {
				t.Fatalf("Open error=%v calls=%d", err, calls)
			}
			if _, err := os.Stat(filepath.Join(root, internalDirectory, "capabilities", "case-probe-a")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed Open leaked probe: %v", err)
			}
			repaired, err := Open(root, Config{})
			if err != nil {
				t.Fatal(err)
			}
			defer repaired.Close()
			for _, dir := range []string{internalDirectory, filepath.Join(internalDirectory, "transactions"), filepath.Join(internalDirectory, "staging"), filepath.Join(internalDirectory, "receipts"), filepath.Join(internalDirectory, "capabilities")} {
				info, err := os.Stat(filepath.Join(root, dir))
				if err != nil {
					t.Fatalf("stat private directory %s: %v", dir, err)
				}
				if info.Mode().Perm() != 0o700 {
					t.Fatalf("private directory %s: mode=%v", dir, info.Mode())
				}
			}
		})
	}
}

func privateEntries(t *testing.T, root, relative string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, relative))
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(entries))
	for i, entry := range entries {
		got[i] = entry.Name()
	}
	return got
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
