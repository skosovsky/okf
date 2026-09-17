package fs

import (
	"context"
	"os"
)

// These adapters keep white-box fixtures on the same Context-aware and
// ownership-aware paths as production without restoring convenience methods
// to Store's production method set.
func (s *Store) recoverForTest(ctx context.Context) error {
	return s.recoverContextWithLimits(ctx, absoluteStagedManifestLimits(), configuredStagedManifestLimits(s.config))
}

func (s *Store) applyForTest(ctx context.Context, j journal) (bool, error) {
	owned, err := s.captureStageDirectoryOwnership(ctx, j)
	if err != nil {
		return false, err
	}
	return s.applyWithOwnership(ctx, j, &owned)
}

func (s *Store) writeFileForTest(ctx context.Context, target string, data []byte) error {
	return s.writeDurableAtObserved(ctx, target, data, 0o644, nil, nil)
}

func (s *Store) writeDurableAtForTest(ctx context.Context, target string, data []byte, fallbackMode os.FileMode) error {
	return s.writeDurableAtObserved(ctx, target, data, fallbackMode, nil, nil)
}
