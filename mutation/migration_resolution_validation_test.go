package mutation

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/skosovsky/okf/store"
)

func TestValidateMigrationSourceResolution_AcceptsCanonicalStateMatrix(t *testing.T) {
	tests := []struct {
		name       string
		resolution MigrationSourceResolution
	}{
		{name: "explicit v0.1 absent declaration", resolution: validExplicitMigrationResolution(MigrationVersionV01, false)},
		{name: "explicit v0.1 matching declaration", resolution: validExplicitMigrationResolution(MigrationVersionV01, true)},
		{name: "explicit v0.2 absent declaration", resolution: validExplicitMigrationResolution(MigrationVersionV02, false)},
		{name: "explicit v0.2 matching declaration", resolution: validExplicitMigrationResolution(MigrationVersionV02, true)},
		{name: "declared v0.1", resolution: validDeclaredMigrationResolution(MigrationVersionV01)},
		{name: "declared v0.2", resolution: validDeclaredMigrationResolution(MigrationVersionV02)},
		{name: "legacy probe", resolution: validLegacyProbeMigrationResolution()},
		{name: "default target", resolution: validDefaultMigrationResolution()},
		{name: "allowed future noop", resolution: validFutureMigrationResolution(MigrationTransitionTargetNoop)},
		{name: "blocked future", resolution: validFutureMigrationResolution(MigrationTransitionBlocked)},
		{
			name: "diagnostic before declaration auto",
			resolution: MigrationSourceResolution{
				RequestedSelector: MigrationSelectorAuto,
				ToVersion:         MigrationVersionV02,
			},
		},
		{
			name: "diagnostic before declaration explicit",
			resolution: MigrationSourceResolution{
				RequestedSelector: MigrationVersionV01,
				ToVersion:         MigrationVersionV02,
			},
		},
		{
			name: "diagnostic malformed declaration",
			resolution: MigrationSourceResolution{
				RequestedSelector:  MigrationSelectorAuto,
				DeclarationPresent: true,
				DeclarationRaw:     "v0.2",
				ToVersion:          MigrationVersionV02,
			},
		},
		{
			name: "diagnostic explicit conflict",
			resolution: MigrationSourceResolution{
				RequestedSelector:  MigrationVersionV01,
				DeclarationPresent: true,
				DeclarationValid:   true,
				DeclarationRaw:     MigrationVersionV02,
				DeclaredVersion:    MigrationVersionV02,
				ToVersion:          MigrationVersionV02,
			},
		},
		{
			name: "diagnostic probe failure without candidates",
			resolution: MigrationSourceResolution{
				RequestedSelector: MigrationSelectorAuto,
				DeclarationValid:  true,
				ToVersion:         MigrationVersionV02,
			},
		},
		{
			name: "diagnostic probe failure retains canonical candidates",
			resolution: MigrationSourceResolution{
				RequestedSelector: MigrationSelectorAuto,
				DeclarationValid:  true,
				ToVersion:         MigrationVersionV02,
				Candidates: []MigrationLegacyCandidate{
					{Kind: "citations", Path: "a.md", Location: SourceSpan{Start: 2, End: 7}},
					{Kind: "timestamp", Path: "a.md"},
					{Kind: "timestamp", Path: "b.md"},
				},
			},
		},
		{
			name: "diagnostic invalid root retains canonical sibling candidates",
			resolution: MigrationSourceResolution{
				RequestedSelector: MigrationSelectorAuto,
				ToVersion:         MigrationVersionV02,
				Candidates: []MigrationLegacyCandidate{
					{Kind: "timestamp", Path: "sibling.md"},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			before := test.resolution.Clone()

			// Act.
			err := ValidateMigrationSourceResolution(test.resolution)

			// Assert.
			if err != nil {
				t.Fatalf("ValidateMigrationSourceResolution() error = %v", err)
			}
			if !reflect.DeepEqual(test.resolution, before) {
				t.Fatalf("ValidateMigrationSourceResolution() mutated input: %#v -> %#v", before, test.resolution)
			}
		})
	}
}

func TestValidateMigrationSourceResolution_RejectsImpossibleStateMatrix(t *testing.T) {
	validFutureBlocker := validFutureMigrationResolution(MigrationTransitionBlocked).Blockers[0]
	tests := []struct {
		name       string
		resolution MigrationSourceResolution
	}{
		{name: "empty selector", resolution: mutateMigrationResolution(validDefaultMigrationResolution(), func(r *MigrationSourceResolution) { r.RequestedSelector = "" })},
		{name: "unknown selector", resolution: mutateMigrationResolution(validDefaultMigrationResolution(), func(r *MigrationSourceResolution) { r.RequestedSelector = "9.0" })},
		{name: "diagnostic missing target", resolution: MigrationSourceResolution{RequestedSelector: MigrationSelectorAuto}},
		{
			name: "diagnostic partial provenance",
			resolution: MigrationSourceResolution{
				RequestedSelector: MigrationSelectorAuto,
				ToVersion:         MigrationVersionV02,
				ResolutionSource:  MigrationResolutionDefault,
			},
		},
		{
			name: "diagnostic blocker",
			resolution: MigrationSourceResolution{
				RequestedSelector: MigrationSelectorAuto,
				ToVersion:         MigrationVersionV02,
				Blockers:          []MigrationBlocker{validFutureBlocker},
			},
		},
		{
			name: "diagnostic explicit candidate",
			resolution: MigrationSourceResolution{
				RequestedSelector: MigrationVersionV01,
				ToVersion:         MigrationVersionV02,
				Candidates:        []MigrationLegacyCandidate{{Kind: "timestamp", Path: "a.md"}},
			},
		},
		{
			name: "diagnostic auto with valid declaration",
			resolution: MigrationSourceResolution{
				RequestedSelector:  MigrationSelectorAuto,
				DeclarationPresent: true,
				DeclarationValid:   true,
				DeclarationRaw:     MigrationVersionV01,
				DeclaredVersion:    MigrationVersionV01,
				ToVersion:          MigrationVersionV02,
			},
		},
		{
			name: "diagnostic explicit with matching declaration",
			resolution: MigrationSourceResolution{
				RequestedSelector:  MigrationVersionV01,
				DeclarationPresent: true,
				DeclarationValid:   true,
				DeclarationRaw:     MigrationVersionV01,
				DeclaredVersion:    MigrationVersionV01,
				ToVersion:          MigrationVersionV02,
			},
		},
		{
			name: "diagnostic explicit after valid absent declaration",
			resolution: MigrationSourceResolution{
				RequestedSelector: MigrationVersionV01,
				DeclarationValid:  true,
				ToVersion:         MigrationVersionV02,
			},
		},
		{name: "absent declaration raw value", resolution: mutateMigrationResolution(validDefaultMigrationResolution(), func(r *MigrationSourceResolution) { r.DeclarationRaw = "0.2" })},
		{name: "present declaration missing raw", resolution: mutateMigrationResolution(validDeclaredMigrationResolution(MigrationVersionV01), func(r *MigrationSourceResolution) { r.DeclarationRaw = "" })},
		{name: "valid declaration raw mismatch", resolution: mutateMigrationResolution(validDeclaredMigrationResolution(MigrationVersionV01), func(r *MigrationSourceResolution) { r.DeclarationRaw = "0.2" })},
		{name: "valid declaration malformed version", resolution: mutateMigrationResolution(validFutureMigrationResolution(MigrationTransitionTargetNoop), func(r *MigrationSourceResolution) {
			r.DeclarationRaw, r.DeclaredVersion, r.ResolvedSource, r.FromVersion, r.ToVersion = "v9", "v9", "v9", "v9", "v9"
		})},
		{name: "resolved invalid declaration", resolution: mutateMigrationResolution(validDefaultMigrationResolution(), func(r *MigrationSourceResolution) { r.DeclarationValid = false })},
		{name: "explicit auto selector", resolution: mutateMigrationResolution(validExplicitMigrationResolution(MigrationVersionV01, false), func(r *MigrationSourceResolution) { r.RequestedSelector = MigrationSelectorAuto })},
		{name: "explicit resolved mismatch", resolution: mutateMigrationResolution(validExplicitMigrationResolution(MigrationVersionV01, false), func(r *MigrationSourceResolution) { r.ResolvedSource = MigrationVersionV02 })},
		{name: "explicit declaration mismatch", resolution: mutateMigrationResolution(validExplicitMigrationResolution(MigrationVersionV01, true), func(r *MigrationSourceResolution) {
			r.DeclarationRaw, r.DeclaredVersion = MigrationVersionV02, MigrationVersionV02
		})},
		{name: "explicit candidates", resolution: mutateMigrationResolution(validExplicitMigrationResolution(MigrationVersionV01, false), func(r *MigrationSourceResolution) {
			r.Candidates = []MigrationLegacyCandidate{{Kind: "timestamp", Path: "a.md"}}
		})},
		{name: "declared explicit selector", resolution: mutateMigrationResolution(validDeclaredMigrationResolution(MigrationVersionV01), func(r *MigrationSourceResolution) { r.RequestedSelector = MigrationVersionV01 })},
		{name: "declared absent declaration", resolution: mutateMigrationResolution(validDeclaredMigrationResolution(MigrationVersionV01), func(r *MigrationSourceResolution) {
			r.DeclarationPresent, r.DeclarationRaw, r.DeclaredVersion = false, "", ""
		})},
		{name: "declared future version", resolution: mutateMigrationResolution(validDeclaredMigrationResolution(MigrationVersionV01), func(r *MigrationSourceResolution) {
			r.DeclarationRaw, r.DeclaredVersion, r.ResolvedSource, r.FromVersion = "9.0", "9.0", "9.0", "9.0"
		})},
		{name: "declared resolved mismatch", resolution: mutateMigrationResolution(validDeclaredMigrationResolution(MigrationVersionV01), func(r *MigrationSourceResolution) { r.ResolvedSource = MigrationVersionV02 })},
		{name: "declared candidates", resolution: mutateMigrationResolution(validDeclaredMigrationResolution(MigrationVersionV01), func(r *MigrationSourceResolution) {
			r.Candidates = []MigrationLegacyCandidate{{Kind: "timestamp", Path: "a.md"}}
		})},
		{name: "legacy probe explicit selector", resolution: mutateMigrationResolution(validLegacyProbeMigrationResolution(), func(r *MigrationSourceResolution) { r.RequestedSelector = MigrationVersionV01 })},
		{name: "legacy probe declaration", resolution: mutateMigrationResolution(validLegacyProbeMigrationResolution(), func(r *MigrationSourceResolution) {
			r.DeclarationPresent, r.DeclarationRaw, r.DeclaredVersion = true, MigrationVersionV01, MigrationVersionV01
		})},
		{name: "legacy probe target resolved", resolution: mutateMigrationResolution(validLegacyProbeMigrationResolution(), func(r *MigrationSourceResolution) { r.ResolvedSource = MigrationVersionV02 })},
		{name: "legacy probe without candidates", resolution: mutateMigrationResolution(validLegacyProbeMigrationResolution(), func(r *MigrationSourceResolution) { r.Candidates = nil })},
		{name: "default explicit selector", resolution: mutateMigrationResolution(validDefaultMigrationResolution(), func(r *MigrationSourceResolution) { r.RequestedSelector = MigrationVersionV02 })},
		{name: "default declaration", resolution: mutateMigrationResolution(validDefaultMigrationResolution(), func(r *MigrationSourceResolution) {
			r.DeclarationPresent, r.DeclarationRaw, r.DeclaredVersion = true, MigrationVersionV02, MigrationVersionV02
		})},
		{name: "default legacy resolved", resolution: mutateMigrationResolution(validDefaultMigrationResolution(), func(r *MigrationSourceResolution) { r.ResolvedSource = MigrationVersionV01 })},
		{name: "default candidates", resolution: mutateMigrationResolution(validDefaultMigrationResolution(), func(r *MigrationSourceResolution) {
			r.Candidates = []MigrationLegacyCandidate{{Kind: "timestamp", Path: "a.md"}}
		})},
		{name: "future explicit selector", resolution: mutateMigrationResolution(validFutureMigrationResolution(MigrationTransitionTargetNoop), func(r *MigrationSourceResolution) { r.RequestedSelector = MigrationVersionV02 })},
		{name: "future absent declaration", resolution: mutateMigrationResolution(validFutureMigrationResolution(MigrationTransitionTargetNoop), func(r *MigrationSourceResolution) {
			r.DeclarationPresent, r.DeclarationRaw, r.DeclaredVersion = false, "", ""
		})},
		{name: "future resolved mismatch", resolution: mutateMigrationResolution(validFutureMigrationResolution(MigrationTransitionTargetNoop), func(r *MigrationSourceResolution) { r.ResolvedSource = "8.0" })},
		{name: "future supported version", resolution: mutateMigrationResolution(validFutureMigrationResolution(MigrationTransitionTargetNoop), func(r *MigrationSourceResolution) {
			r.DeclarationRaw, r.DeclaredVersion, r.ResolvedSource, r.FromVersion, r.ToVersion = MigrationVersionV02, MigrationVersionV02, MigrationVersionV02, MigrationVersionV02, MigrationVersionV02
		})},
		{name: "future candidates", resolution: mutateMigrationResolution(validFutureMigrationResolution(MigrationTransitionTargetNoop), func(r *MigrationSourceResolution) {
			r.Candidates = []MigrationLegacyCandidate{{Kind: "timestamp", Path: "a.md"}}
		})},
		{name: "transition target resolved", resolution: mutateMigrationResolution(validDeclaredMigrationResolution(MigrationVersionV01), func(r *MigrationSourceResolution) { r.ResolvedSource = MigrationVersionV02 })},
		{name: "transition wrong from", resolution: mutateMigrationResolution(validDeclaredMigrationResolution(MigrationVersionV01), func(r *MigrationSourceResolution) { r.FromVersion = MigrationVersionV02 })},
		{name: "transition wrong to", resolution: mutateMigrationResolution(validDeclaredMigrationResolution(MigrationVersionV01), func(r *MigrationSourceResolution) { r.ToVersion = MigrationVersionV01 })},
		{name: "transition blockers", resolution: mutateMigrationResolution(validDeclaredMigrationResolution(MigrationVersionV01), func(r *MigrationSourceResolution) { r.Blockers = []MigrationBlocker{validFutureBlocker} })},
		{name: "target noop legacy", resolution: mutateMigrationResolution(validDefaultMigrationResolution(), func(r *MigrationSourceResolution) {
			r.ResolvedSource, r.FromVersion, r.ToVersion = MigrationVersionV01, MigrationVersionV01, MigrationVersionV01
		})},
		{name: "target noop wrong from", resolution: mutateMigrationResolution(validDefaultMigrationResolution(), func(r *MigrationSourceResolution) { r.FromVersion = MigrationVersionV01 })},
		{name: "target noop wrong to", resolution: mutateMigrationResolution(validDefaultMigrationResolution(), func(r *MigrationSourceResolution) { r.ToVersion = MigrationVersionV01 })},
		{name: "target noop blockers", resolution: mutateMigrationResolution(validDefaultMigrationResolution(), func(r *MigrationSourceResolution) { r.Blockers = []MigrationBlocker{validFutureBlocker} })},
		{name: "blocked nonfuture provenance", resolution: mutateMigrationResolution(validFutureMigrationResolution(MigrationTransitionBlocked), func(r *MigrationSourceResolution) { r.ResolutionSource = MigrationResolutionDeclared })},
		{name: "blocked wrong from", resolution: mutateMigrationResolution(validFutureMigrationResolution(MigrationTransitionBlocked), func(r *MigrationSourceResolution) { r.FromVersion = "8.0" })},
		{name: "blocked target present", resolution: mutateMigrationResolution(validFutureMigrationResolution(MigrationTransitionBlocked), func(r *MigrationSourceResolution) { r.ToVersion = MigrationVersionV02 })},
		{name: "blocked missing blocker", resolution: mutateMigrationResolution(validFutureMigrationResolution(MigrationTransitionBlocked), func(r *MigrationSourceResolution) { r.Blockers = nil })},
		{name: "blocked duplicate blockers", resolution: mutateMigrationResolution(validFutureMigrationResolution(MigrationTransitionBlocked), func(r *MigrationSourceResolution) { r.Blockers = append(r.Blockers, r.Blockers[0]) })},
		{name: "blocked empty blocker code", resolution: mutateMigrationResolution(validFutureMigrationResolution(MigrationTransitionBlocked), func(r *MigrationSourceResolution) { r.Blockers[0].Code = "" })},
		{name: "blocked wrong blocker path", resolution: mutateMigrationResolution(validFutureMigrationResolution(MigrationTransitionBlocked), func(r *MigrationSourceResolution) { r.Blockers[0].Path = "other.md" })},
		{name: "blocked nonzero blocker span", resolution: mutateMigrationResolution(validFutureMigrationResolution(MigrationTransitionBlocked), func(r *MigrationSourceResolution) { r.Blockers[0].Location.End = 1 })},
		{name: "blocked wrong blocker message", resolution: mutateMigrationResolution(validFutureMigrationResolution(MigrationTransitionBlocked), func(r *MigrationSourceResolution) { r.Blockers[0].Message = "different" })},
		{name: "unknown provenance", resolution: mutateMigrationResolution(validDefaultMigrationResolution(), func(r *MigrationSourceResolution) { r.ResolutionSource = "other" })},
		{name: "unknown transition", resolution: mutateMigrationResolution(validDefaultMigrationResolution(), func(r *MigrationSourceResolution) { r.Transition = "other" })},
		{name: "incomplete resolved tuple", resolution: mutateMigrationResolution(validDefaultMigrationResolution(), func(r *MigrationSourceResolution) { r.FromVersion = "" })},
		{name: "candidate unknown kind", resolution: mutateMigrationResolution(validLegacyProbeMigrationResolution(), func(r *MigrationSourceResolution) { r.Candidates[0].Kind = "other" })},
		{name: "candidate non-markdown path", resolution: mutateMigrationResolution(validLegacyProbeMigrationResolution(), func(r *MigrationSourceResolution) { r.Candidates[0].Path = "asset.bin" })},
		{name: "candidate invalid path", resolution: mutateMigrationResolution(validLegacyProbeMigrationResolution(), func(r *MigrationSourceResolution) { r.Candidates[0].Path = "../a.md" })},
		{name: "candidate negative span", resolution: mutateMigrationResolution(validLegacyProbeMigrationResolution(), func(r *MigrationSourceResolution) { r.Candidates[0].Location.Start = -1 })},
		{name: "candidate reversed span", resolution: mutateMigrationResolution(validLegacyProbeMigrationResolution(), func(r *MigrationSourceResolution) { r.Candidates[0].Location = SourceSpan{Start: 2, End: 1} })},
		{name: "timestamp candidate nonzero span", resolution: mutateMigrationResolution(validLegacyProbeMigrationResolution(), func(r *MigrationSourceResolution) { r.Candidates[0].Location.End = 1 })},
		{name: "citations candidate empty span", resolution: mutateMigrationResolution(validLegacyProbeMigrationResolution(), func(r *MigrationSourceResolution) { r.Candidates[0].Kind = "citations" })},
		{
			name: "candidate noncanonical order",
			resolution: mutateMigrationResolution(validLegacyProbeMigrationResolution(), func(r *MigrationSourceResolution) {
				r.Candidates = []MigrationLegacyCandidate{
					{Kind: "timestamp", Path: "b.md"},
					{Kind: "timestamp", Path: "a.md"},
				}
			}),
		},
		{
			name: "duplicate candidate",
			resolution: mutateMigrationResolution(validLegacyProbeMigrationResolution(), func(r *MigrationSourceResolution) {
				r.Candidates = append(r.Candidates, r.Candidates[0])
			}),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			before := test.resolution.Clone()

			// Act.
			err := ValidateMigrationSourceResolution(test.resolution)

			// Assert.
			if !errors.Is(err, ErrUnsupportedMigrationVersion) {
				t.Fatalf("ValidateMigrationSourceResolution() error = %v", err)
			}
			if !reflect.DeepEqual(test.resolution, before) {
				t.Fatalf("ValidateMigrationSourceResolution() mutated input: %#v -> %#v", before, test.resolution)
			}
		})
	}
}

func TestValidateMigrationSourceResolution_IsDeterministicAndOwnsNoCallerMemory(t *testing.T) {
	// Arrange.
	resolution := validLegacyProbeMigrationResolution()
	resolution.Candidates = []MigrationLegacyCandidate{
		{Kind: "timestamp", Path: "b.md"},
		{Kind: "citations", Path: "a.md", Location: SourceSpan{Start: 1, End: 3}},
	}
	before := resolution.Clone()
	const want = "unsupported migration version: noncanonical legacy candidates"

	// Act.
	for iteration := 0; iteration < 256; iteration++ {
		err := ValidateMigrationSourceResolution(resolution)
		if err == nil || err.Error() != want {
			t.Fatalf("iteration %d error = %v, want %q", iteration, err, want)
		}
	}

	// Assert.
	if !reflect.DeepEqual(resolution, before) {
		t.Fatalf("ValidateMigrationSourceResolution() retained or mutated caller memory: %#v -> %#v", before, resolution)
	}
}

func TestMigrationResolutionValidation_PrecedesSourceAndStoreIO(t *testing.T) {
	// Arrange.
	invalid := validDefaultMigrationResolution()
	invalid.FromVersion = MigrationVersionV01
	validInput := store.V01ToV02Migration{
		TimestampPolicy:   store.LegacyTimestampPreserve,
		TimestampConflict: store.TimestampConflictReject,
	}
	destination := &countingMigrationStore{}

	// Act.
	_, previewErr := NewMigrationPlanner().Preview(
		context.Background(),
		noReadSource{},
		invalid,
		MigrationRequest{Migration: validInput},
	)
	_, preflightErr := PreflightV01ToV02MigrationInput(
		context.Background(),
		noReadSource{},
		invalid,
		validInput,
	)
	_, applyErr := NewMigrationPlanner().Apply(
		context.Background(),
		destination,
		MigrationApplyRequest{Resolution: invalid},
	)

	// Assert.
	for name, err := range map[string]error{
		"preview":   previewErr,
		"preflight": preflightErr,
		"apply":     applyErr,
	} {
		if !errors.Is(err, ErrUnsupportedMigrationVersion) {
			t.Fatalf("%s error = %v", name, err)
		}
	}
	if destination.snapshots != 0 || destination.previews != 0 || destination.commits != 0 {
		t.Fatalf("invalid resolution reached store: %#v", destination)
	}
}

func validExplicitMigrationResolution(version string, declared bool) MigrationSourceResolution {
	resolution := MigrationSourceResolution{
		RequestedSelector: version,
		DeclarationValid:  true,
		ResolvedSource:    version,
		ResolutionSource:  MigrationResolutionExplicit,
		FromVersion:       version,
		ToVersion:         version,
		Transition:        MigrationTransitionTargetNoop,
	}
	if version == MigrationVersionV01 {
		resolution.ToVersion = MigrationVersionV02
		resolution.Transition = MigrationTransitionV01ToV02
	}
	if declared {
		resolution.DeclarationPresent = true
		resolution.DeclarationRaw = version
		resolution.DeclaredVersion = version
	}
	return resolution
}

func validDeclaredMigrationResolution(version string) MigrationSourceResolution {
	resolution := validExplicitMigrationResolution(version, true)
	resolution.RequestedSelector = MigrationSelectorAuto
	resolution.ResolutionSource = MigrationResolutionDeclared
	return resolution
}

func validLegacyProbeMigrationResolution() MigrationSourceResolution {
	return MigrationSourceResolution{
		RequestedSelector: MigrationSelectorAuto,
		DeclarationValid:  true,
		ResolvedSource:    MigrationVersionV01,
		ResolutionSource:  MigrationResolutionLegacyProbe,
		FromVersion:       MigrationVersionV01,
		ToVersion:         MigrationVersionV02,
		Transition:        MigrationTransitionV01ToV02,
		Candidates: []MigrationLegacyCandidate{{
			Kind: "timestamp",
			Path: "a.md",
		}},
	}
}

func validDefaultMigrationResolution() MigrationSourceResolution {
	return MigrationSourceResolution{
		RequestedSelector: MigrationSelectorAuto,
		DeclarationValid:  true,
		ResolvedSource:    MigrationVersionV02,
		ResolutionSource:  MigrationResolutionDefault,
		FromVersion:       MigrationVersionV02,
		ToVersion:         MigrationVersionV02,
		Transition:        MigrationTransitionTargetNoop,
	}
}

func validFutureMigrationResolution(transition MigrationTransition) MigrationSourceResolution {
	const version = "9.0"
	resolution := MigrationSourceResolution{
		RequestedSelector:  MigrationSelectorAuto,
		DeclarationPresent: true,
		DeclarationValid:   true,
		DeclarationRaw:     version,
		DeclaredVersion:    version,
		ResolvedSource:     version,
		ResolutionSource:   MigrationResolutionFuture,
		FromVersion:        version,
		ToVersion:          version,
		Transition:         transition,
	}
	if transition == MigrationTransitionBlocked {
		resolution.ToVersion = ""
		resolution.Blockers = []MigrationBlocker{{
			Code:    "unsupported_migration_source",
			Path:    "index.md",
			Message: `future declaration "9.0" cannot be rewritten as v0.2`,
		}}
	}
	return resolution
}

func mutateMigrationResolution(
	resolution MigrationSourceResolution,
	mutate func(*MigrationSourceResolution),
) MigrationSourceResolution {
	resolution = resolution.Clone()
	mutate(&resolution)
	return resolution
}
