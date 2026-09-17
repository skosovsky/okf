package fs

import "testing"

func registerStoreCleanup(t *testing.T, s *Store) {
	t.Helper()
	t.Cleanup(func() {
		_ = s.Close()
	})
}
