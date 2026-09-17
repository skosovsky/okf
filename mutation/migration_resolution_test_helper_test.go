package mutation

import (
	"context"
	"testing"

	"github.com/skosovsky/okf/bundle"
)

func testMigrationResolution(t *testing.T, source bundle.Source) MigrationSourceResolution {
	t.Helper()
	resolution, err := ResolveMigrationSource(context.Background(), source, MigrationSourceOptions{
		RequestedSelector: MigrationSelectorAuto,
	})
	if err != nil {
		t.Fatal(err)
	}
	return resolution
}

func testUnresolvedMigrationResolution(t *testing.T, source bundle.Source) MigrationSourceResolution {
	t.Helper()
	resolution, err := ResolveMigrationSource(context.Background(), source, MigrationSourceOptions{
		RequestedSelector: MigrationSelectorAuto,
	})
	if err == nil {
		t.Fatal("ResolveMigrationSource() error = nil, want malformed source rejection")
	}
	return resolution
}

func testDeclaredTargetResolutionV02() MigrationSourceResolution {
	return MigrationSourceResolution{
		RequestedSelector:  MigrationSelectorAuto,
		DeclarationPresent: true,
		DeclarationValid:   true,
		DeclarationRaw:     MigrationVersionV02,
		DeclaredVersion:    MigrationVersionV02,
		ResolvedSource:     MigrationVersionV02,
		ResolutionSource:   MigrationResolutionDeclared,
		FromVersion:        MigrationVersionV02,
		ToVersion:          MigrationVersionV02,
		Transition:         MigrationTransitionTargetNoop,
	}
}
