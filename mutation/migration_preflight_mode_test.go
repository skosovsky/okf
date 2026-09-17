package mutation

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/skosovsky/okf/store"
)

func TestPreflightV01ToV02MigrationInput_RootlessTargetReplay(t *testing.T) {
	for _, test := range []struct {
		name        string
		newline     string
		legacyEntry string
		number      uint64
	}{
		{name: "number-only/LF", newline: "\n", number: 1},
		{name: "exact-entry/CRLF", newline: "\r\n", legacyEntry: "[1] https://example.test/spec"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			nl := test.newline
			source := memorySource{
				"a.md": []byte(
					"---" + nl +
						"type: Knowledge" + nl +
						"sources:" + nl +
						"  - id: spec" + nl +
						"    resource: https://example.test/spec" + nl +
						"---" + nl + nl +
						"Claim [^spec]." + nl + nl +
						"[^spec]: https://example.test/spec" + nl,
				),
			}
			before := cloneMemorySource(source)
			resolution, err := ResolveMigrationSource(context.Background(), source, MigrationSourceOptions{
				RequestedSelector: MigrationSelectorAuto,
			})
			if err != nil {
				t.Fatal(err)
			}
			input := store.V01ToV02Migration{
				TimestampPolicy:   store.LegacyTimestampPreserve,
				TimestampConflict: store.TimestampConflictReject,
				Citations: []store.DocumentCitationMigration{{
					Path: "a.md",
					Entries: []store.LegacyCitationMapping{{
						LegacyNumber: test.number,
						LegacyEntry:  test.legacyEntry,
						SourceID:     "spec",
						Resource:     "https://example.test/spec",
					}},
				}},
			}

			// Act.
			first, firstErr := PreflightV01ToV02MigrationInput(
				context.Background(),
				source,
				resolution,
				input,
			)
			second, secondErr := PreflightV01ToV02MigrationInput(
				context.Background(),
				source,
				resolution,
				input,
			)

			// Assert.
			if resolution.ResolutionSource != MigrationResolutionDefault ||
				resolution.Transition != MigrationTransitionTargetNoop {
				t.Fatalf("resolution = %#v, want rootless default target-noop", resolution)
			}
			if firstErr != nil || secondErr != nil ||
				len(first.Blockers) != 0 || len(first.ManualActions) != 0 ||
				!reflect.DeepEqual(first, second) {
				t.Fatalf("preflight = (%#v, %v), repeated = (%#v, %v)", first, firstErr, second, secondErr)
			}
			if !equalMemorySource(source, before) {
				t.Fatal("preflight mutated source")
			}
		})
	}
}

func TestPreflightV01ToV02MigrationInput_ExplicitResolutionMode(t *testing.T) {
	validInput := store.V01ToV02Migration{
		TimestampPolicy:   store.LegacyTimestampPreserve,
		TimestampConflict: store.TimestampConflictReject,
	}
	tests := []struct {
		name       string
		resolution MigrationSourceResolution
	}{
		{
			name: "transition labeled as target",
			resolution: MigrationSourceResolution{
				RequestedSelector:  MigrationSelectorAuto,
				DeclarationPresent: true,
				DeclarationValid:   true,
				DeclarationRaw:     MigrationVersionV01,
				DeclaredVersion:    MigrationVersionV01,
				ResolvedSource:     MigrationVersionV01,
				ResolutionSource:   MigrationResolutionDeclared,
				FromVersion:        MigrationVersionV01,
				ToVersion:          MigrationVersionV02,
				Transition:         MigrationTransitionTargetNoop,
			},
		},
		{
			name: "target labeled as transition",
			resolution: MigrationSourceResolution{
				RequestedSelector:  MigrationSelectorAuto,
				DeclarationPresent: true,
				DeclarationValid:   true,
				DeclarationRaw:     MigrationVersionV02,
				DeclaredVersion:    MigrationVersionV02,
				ResolvedSource:     MigrationVersionV02,
				ResolutionSource:   MigrationResolutionDeclared,
				FromVersion:        MigrationVersionV02,
				ToVersion:          MigrationVersionV02,
				Transition:         MigrationTransitionV01ToV02,
			},
		},
		{
			name: "noop rewrites target",
			resolution: MigrationSourceResolution{
				RequestedSelector:  MigrationSelectorAuto,
				DeclarationPresent: true,
				DeclarationValid:   true,
				DeclarationRaw:     MigrationVersionV02,
				DeclaredVersion:    MigrationVersionV02,
				ResolvedSource:     MigrationVersionV02,
				ResolutionSource:   MigrationResolutionDeclared,
				FromVersion:        MigrationVersionV02,
				ToVersion:          "9.0",
				Transition:         MigrationTransitionTargetNoop,
			},
		},
		{
			name: "blocked resolution",
			resolution: MigrationSourceResolution{
				RequestedSelector:  MigrationSelectorAuto,
				DeclarationPresent: true,
				DeclarationValid:   true,
				DeclarationRaw:     "9.0",
				DeclaredVersion:    "9.0",
				ResolvedSource:     "9.0",
				ResolutionSource:   MigrationResolutionFuture,
				FromVersion:        "9.0",
				Transition:         MigrationTransitionBlocked,
				Blockers:           []MigrationBlocker{{Code: "unsupported_migration_source"}},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			source := memorySource{}

			// Act.
			got, err := PreflightV01ToV02MigrationInput(
				context.Background(),
				source,
				test.resolution,
				validInput,
			)

			// Assert.
			if !errors.Is(err, ErrUnsupportedMigrationVersion) ||
				len(got.Blockers) != 0 ||
				len(got.ManualActions) != 0 {
				t.Fatalf("preflight = (%#v, %v), want incoherent resolution rejection", got, err)
			}
		})
	}
}

func TestPreflightV01ToV02MigrationInput_TargetGeneratedReplay(t *testing.T) {
	for _, test := range []struct {
		name         string
		document     string
		generatedBy  string
		generatedAt  string
		policy       store.LegacyTimestampPolicy
		conflict     store.TimestampConflictPolicy
		wantFragment string
		wantEOF      bool
	}{
		{
			name: "LF block preserves existing actor with equivalent explicit instant",
			document: "---\ntype: Knowledge\ngenerated:\n  by: process:migration\n" +
				"  at: 2026-06-25T16:00:00+07:00\n---\n",
			generatedBy: "process:requested",
			generatedAt: "2026-06-25T09:00:00.000Z",
			policy:      store.LegacyTimestampPreserve,
			conflict:    store.TimestampConflictReject,
		},
		{
			name: "CRLF flow exact actor and retained equivalent timestamp",
			document: "---\r\ntype: Knowledge\r\ntimestamp: 2026-06-25T09:00:00Z\r\n" +
				"generated: {by: process:migration, at: 2026-06-25T16:00:00+07:00}\r\n---\r\n",
			generatedBy: "process:migration",
			generatedAt: "2026-06-25T09:00:00Z",
			policy:      store.LegacyTimestampPreserve,
			conflict:    store.TimestampConflictReject,
		},
		{
			name: "retained timestamp does not turn existing actor into a creation assertion",
			document: "---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n" +
				"generated: {by: process:existing, at: 2026-06-25T09:00:00Z}\n---\n",
			generatedBy: "process:migration",
			policy:      store.LegacyTimestampPreserve,
			conflict:    store.TimestampConflictReject,
		},
		{
			name: "generated-at tamper has scalar span",
			document: "---\ntype: Knowledge\ngenerated:\n  by: process:migration\n" +
				"  at: 2026-06-26T09:00:00Z\n---\n",
			generatedAt:  "2026-06-25T09:00:00Z",
			policy:       store.LegacyTimestampPreserve,
			conflict:     store.TimestampConflictReject,
			wantFragment: "2026-06-26T09:00:00Z",
		},
		{
			name:         "generated absent with explicit at points to YAML boundary",
			document:     "---\ntype: Knowledge\n---\n\nBody.\n",
			generatedAt:  "2026-06-25T09:00:00Z",
			policy:       store.LegacyTimestampPreserve,
			conflict:     store.TimestampConflictReject,
			wantFragment: "---\n\nBody.",
			wantEOF:      true,
		},
		{
			name: "retained timestamp mismatch spans both instants",
			document: "---\ntype: Knowledge\ntimestamp: 2026-06-24T09:00:00Z\ngenerated:\n" +
				"  by: process:migration\n  at: 2026-06-25T09:00:00Z\n---\n",
			policy:       store.LegacyTimestampPreserve,
			conflict:     store.TimestampConflictReject,
			wantFragment: "2026-06-24T09:00:00Z",
		},
		{
			name: "remove policy rejects retained timestamp",
			document: "---\r\ntype: Knowledge\r\ntimestamp: 2026-06-25T09:00:00Z\r\n" +
				"generated: {by: process:migration, at: 2026-06-25T09:00:00Z}\r\n---\r\n",
			policy:       store.LegacyTimestampRemoveAfterCopy,
			conflict:     store.TimestampConflictReject,
			wantFragment: "2026-06-25T09:00:00Z",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			source := memorySource{"a.md": []byte(test.document)}
			before := cloneMemorySource(source)
			input := store.V01ToV02Migration{
				GeneratedBy:       test.generatedBy,
				TimestampPolicy:   test.policy,
				TimestampConflict: test.conflict,
			}
			if test.generatedAt != "" {
				input.GeneratedAt = []store.MigrationGeneratedAt{{Path: "a.md", At: test.generatedAt}}
			}

			// Act.
			got, err := PreflightV01ToV02MigrationInput(
				context.Background(),
				source,
				testMigrationResolution(t, source),
				input,
			)

			// Assert.
			if !equalMemorySource(source, before) {
				t.Fatal("preflight mutated source")
			}
			if test.wantFragment == "" {
				if err != nil || len(got.Blockers) != 0 {
					t.Fatalf("preflight = (%#v, %v), want success", got, err)
				}
				return
			}
			if err == nil || len(got.Blockers) != 1 ||
				got.Blockers[0].Code != "migration_replay_mismatch" {
				t.Fatalf("preflight = (%#v, %v), want exact replay mismatch", got, err)
			}
			location := got.Blockers[0].Location
			if test.wantEOF {
				want := bytes.Index(source["a.md"], []byte("\n---\n"))
				if location != (SourceSpan{Start: want + 1, End: want + 1}) {
					t.Fatalf("location = %#v, want YAML boundary %d", location, want+1)
				}
				return
			}
			start := bytes.Index(source["a.md"], []byte(test.wantFragment))
			if start < 0 || location.Start > start || location.End < start+len(test.wantFragment) {
				t.Fatalf("location = %#v, want to own %q at %d", location, test.wantFragment, start)
			}
		})
	}
}

func TestPreflightV01ToV02MigrationInput_TargetComputationReplay(t *testing.T) {
	contractSource := func(t *testing.T, body string, contract store.AttestedComputationContract) []byte {
		t.Helper()
		updated, err := reconcileAttestedComputationYAMLBytesContext(
			context.Background(),
			[]byte("---\ntype: Knowledge\n---\n"+body),
			contract,
		)
		if err != nil {
			t.Fatal(err)
		}
		return updated
	}
	baseInput := func(computation store.ComputationMigration) store.V01ToV02Migration {
		return store.V01ToV02Migration{
			TimestampPolicy:   store.LegacyTimestampPreserve,
			TimestampConflict: store.TimestampConflictReject,
			Computations:      []store.ComputationMigration{computation},
		}
	}

	t.Run("inline and file modes replay without writes", func(t *testing.T) {
		// Arrange.
		inlineContract := store.AttestedComputationContract{
			Mode:              store.AttestedComputationModeInline,
			Runtime:           "sql",
			InlineComputation: "SELECT 1;\n",
			InlineLanguage:    "sql",
			Executor:          store.ExecutorContract{Resource: "executors/sql.md", Receipt: []string{"rows"}},
			Attester:          store.AttesterContract{Resource: "attesters/sql.md"},
		}
		fileContract := store.AttestedComputationContract{
			Mode:            store.AttestedComputationModeFile,
			Runtime:         "sql",
			ComputationPath: "references/file.sql",
			Executor:        store.ExecutorContract{Resource: "executors/sql.md", Receipt: []string{"rows"}},
			Attester:        store.AttesterContract{Resource: "attesters/sql.md"},
		}
		source := memorySource{
			"inline.md":           contractSource(t, "\n# Computation\n\n```sql\nSELECT 1;\n```\n", inlineContract),
			"file.md":             contractSource(t, "\nBody.\n", fileContract),
			"references/file.sql": []byte("SELECT 2;\n"),
		}
		before := cloneMemorySource(source)
		input := store.V01ToV02Migration{
			TimestampPolicy:   store.LegacyTimestampPreserve,
			TimestampConflict: store.TimestampConflictReject,
			Computations: []store.ComputationMigration{
				{Path: "inline.md", Contract: inlineContract},
				{
					Path: "file.md", Contract: fileContract,
					Asset: &store.MigrationAsset{
						Path: "references/file.sql", Content: []byte("SELECT 2;\n"),
					},
				},
			},
		}

		// Act.
		got, err := PreflightV01ToV02MigrationInput(
			context.Background(),
			source,
			testMigrationResolution(t, source),
			input,
		)

		// Assert.
		if err != nil || len(got.Blockers) != 0 || !equalMemorySource(source, before) {
			t.Fatalf("preflight = (%#v, %v), mutated=%t", got, err, !equalMemorySource(source, before))
		}
	})

	for _, test := range []struct {
		name      string
		assetData []byte
		present   bool
	}{
		{name: "missing file asset", present: false},
		{name: "tampered file asset", present: true, assetData: []byte("SELECT tampered;\n")},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			contract := store.AttestedComputationContract{
				Mode:            store.AttestedComputationModeFile,
				Runtime:         "sql",
				ComputationPath: "references/file.sql",
				Executor:        store.ExecutorContract{Resource: "executors/sql.md", Receipt: []string{"rows"}},
				Attester:        store.AttesterContract{Resource: "attesters/sql.md"},
			}
			computation := store.ComputationMigration{
				Path: "file.md", Contract: contract,
				Asset: &store.MigrationAsset{
					Path: "references/file.sql", Content: []byte("SELECT 2;\n"),
				},
			}
			source := memorySource{"file.md": contractSource(t, "\nBody.\n", contract)}
			if test.present {
				source["references/file.sql"] = test.assetData
			}

			// Act.
			got, err := PreflightV01ToV02MigrationInput(
				context.Background(),
				source,
				testMigrationResolution(t, source),
				baseInput(computation),
			)

			// Assert.
			if err == nil || len(got.Blockers) != 1 ||
				got.Blockers[0].Code != "migration_replay_mismatch" ||
				got.Blockers[0].Path != "references/file.sql" {
				t.Fatalf("preflight = (%#v, %v), want asset replay mismatch", got, err)
			}
		})
	}
}

func TestMigrationPlannerPreview_UnresolvedSourceUsesDiagnosticsOnly(t *testing.T) {
	tests := []struct {
		name     string
		source   memorySource
		wantPath string
	}{
		{
			name: "malformed declared target",
			source: memorySource{
				"index.md": []byte("---\nokf_version: \"0.2\"\ninvalid: [\n---\n"),
				"a.md":     []byte("---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n---\n"),
			},
			wantPath: "index.md",
		},
		{
			name: "malformed rootless bundle",
			source: memorySource{
				"broken.md": []byte("---\ntype: [\n---\n"),
				"a.md":      []byte("---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n---\n"),
			},
			wantPath: "broken.md",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			before := cloneMemorySource(test.source)
			request := MigrationRequest{
				ID:          "diagnostics-only",
				Actor:       "operator",
				FromVersion: MigrationVersionV01,
				ToVersion:   MigrationVersionV02,
				Migration: store.V01ToV02Migration{
					GeneratedBy:       "process:migration",
					TimestampPolicy:   store.LegacyTimestampPreserve,
					TimestampConflict: store.TimestampConflictReject,
				},
			}

			// Act.
			got, err := NewMigrationPlanner().Preview(context.Background(), test.source, testUnresolvedMigrationResolution(t, test.source), request)

			// Assert.
			if err == nil ||
				len(got.Blockers) != 1 ||
				got.Blockers[0].Path != test.wantPath ||
				got.Blockers[0].Code != "yaml_syntax" {
				t.Fatalf("Preview() = (%#v, %v), want parser-owned diagnostic only", got, err)
			}
			if got.Staged != nil ||
				!reflect.DeepEqual(got.Proof, MigrationPlanProof{}) ||
				got.PlanDigest != "" ||
				len(got.Preview.Writes) != 0 ||
				!equalMemorySource(test.source, before) {
				t.Fatalf("diagnostic preview staged/proved/wrote: %#v", got)
			}
			for _, blocker := range got.Blockers {
				if blocker.Code == "missing_explicit_generated_by" {
					t.Fatalf("diagnostic mode ran transition staging: %#v", got.Blockers)
				}
			}
		})
	}
}

func TestPreflightV01ToV02MigrationInput_AllowedFutureNoopIsDocumentAware(t *testing.T) {
	// Arrange.
	source := memorySource{
		"index.md": []byte("---\nokf_version: \"9.0\"\n---\n"),
		"a.md": []byte("---\ntype: Knowledge\ngenerated:\n" +
			"  by: process:tampered\n  at: 2026-06-25T09:00:00Z\n---\n"),
	}
	resolution, err := ResolveMigrationSource(context.Background(), source, MigrationSourceOptions{
		RequestedSelector: MigrationSelectorAuto,
		AllowFutureNoop:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	input := store.V01ToV02Migration{
		GeneratedBy:       "process:migration",
		TimestampPolicy:   store.LegacyTimestampPreserve,
		TimestampConflict: store.TimestampConflictReject,
	}

	// Act.
	got, preflightErr := PreflightV01ToV02MigrationInput(
		context.Background(),
		source,
		resolution,
		input,
	)

	// Assert.
	if resolution.ResolutionSource != MigrationResolutionFuture ||
		resolution.Transition != MigrationTransitionTargetNoop ||
		preflightErr != nil ||
		len(got.Blockers) != 0 {
		t.Fatalf("future resolution/preflight = (%#v, %#v, %v)", resolution, got, preflightErr)
	}
}
