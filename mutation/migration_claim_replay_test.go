package mutation

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
)

type countingMigrationStore struct {
	snapshots int
	previews  int
	commits   int
}

func (s *countingMigrationStore) Snapshot(context.Context) (store.Snapshot, error) {
	s.snapshots++
	return nil, errors.New("unexpected Snapshot")
}

func (s *countingMigrationStore) Preview(context.Context, store.ChangeSet) (store.Preview, error) {
	s.previews++
	return store.Preview{}, errors.New("unexpected Preview")
}

func (s *countingMigrationStore) Commit(
	context.Context,
	store.ChangeSet,
	store.CommitOptions,
) (store.CommitReceipt, error) {
	s.commits++
	return store.CommitReceipt{}, errors.New("unexpected Commit")
}

func TestVerifyMigratedCitationClaimReplay_NumberedMappingsAreAuthenticatedWithoutInference(t *testing.T) {
	const resource = "https://example.test/spec"
	mapping := func(number uint64, id string) store.LegacyCitationMapping {
		return store.LegacyCitationMapping{
			LegacyNumber: number,
			SourceID:     id,
			Resource:     resource,
		}
	}
	document := func(entries ...store.LegacyCitationMapping) store.DocumentCitationMigration {
		return store.DocumentCitationMigration{Path: "log.md", Entries: entries}
	}
	for _, newline := range []struct {
		name  string
		value string
	}{
		{name: "LF", value: "\n"},
		{name: "CRLF", value: "\r\n"},
	} {
		t.Run(newline.name, func(t *testing.T) {
			tests := []struct {
				name      string
				body      string
				migration store.DocumentCitationMigration
				want      string
				wantOK    bool
			}{
				{
					name:      "exact keyed reference",
					body:      "Claim [^spec].\n\n[^spec]: " + resource + "\n",
					migration: document(mapping(1, "spec")),
					wantOK:    true,
				},
				{
					name:      "leftover selected marker",
					body:      "Claim [1] and [^spec].\n\n[^spec]: " + resource + "\n",
					migration: document(mapping(1, "spec")),
					want:      "[1]",
				},
				{
					name:      "wrong keyed reference",
					body:      "Claim [^other].\n\n[^other]: other\n[^spec]: " + resource + "\n",
					migration: document(mapping(1, "spec")),
					want:      resource,
				},
				{
					name:      "unrelated marker remains prose",
					body:      "Unselected [2]; claim [^spec].\n\n[^spec]: " + resource + "\n",
					migration: document(mapping(1, "spec")),
					wantOK:    true,
				},
				{
					name: "code and raw HTML block are opaque",
					body: "`[1]`\n\n```text\n[1]\n```\n\n<div>\n[1]\n</div>\n\n" +
						"Claim [^spec].\n\n[^spec]: " + resource + "\n",
					migration: document(mapping(1, "spec")),
					wantOK:    true,
				},
				{
					name:      "two numbers share one keyed reference",
					body:      "Shared claim [^spec].\n\n[^spec]: " + resource + "\n",
					migration: document(mapping(1, "spec"), mapping(2, "spec")),
					wantOK:    true,
				},
				{
					name:      "entry-only mapping does not invent attribution",
					body:      "Narrative without a claim reference.\n\n[^spec]: " + resource + "\n",
					migration: document(mapping(0, "spec")),
					wantOK:    true,
				},
			}
			for _, test := range tests {
				t.Run(test.name, func(t *testing.T) {
					// Arrange.
					source := []byte(strings.ReplaceAll(test.body, "\n", newline.value))
					ownership, err := CollectMarkdownMigrationOwnership(context.Background(), source)
					if err != nil {
						t.Fatal(err)
					}

					// Act.
					err = verifyMigratedCitations(
						context.Background(),
						source,
						nil,
						ownership,
						test.migration,
						false,
					)

					// Assert.
					if test.wantOK {
						if err != nil {
							t.Fatalf("verifyMigratedCitations() error = %v", err)
						}
						return
					}
					var presentation *PresentationError
					if !errors.As(err, &presentation) ||
						presentation.Code != "migration_replay_mismatch" {
						t.Fatalf("verifyMigratedCitations() error = %#v", err)
					}
					start := bytes.Index(source, []byte(test.want))
					want := SourceSpan{Start: start, End: start + len(test.want)}
					if presentation.Location != want {
						t.Fatalf("Location = %#v, want %#v", presentation.Location, want)
					}
				})
			}
		})
	}
}

func TestPreflightV01ToV02MigrationInput_CitationClaimReplayAllDocumentRoles(t *testing.T) {
	const resource = "https://example.test/spec"
	for _, test := range []struct {
		name string
		path string
		head string
	}{
		{name: "root", path: "index.md", head: "---\nokf_version: \"0.2\"\nsources:\n  - id: spec\n    resource: " + resource + "\n---\n"},
		{name: "concept", path: "concepts/a.md", head: "---\ntype: Knowledge\nsources:\n  - id: spec\n    resource: " + resource + "\n---\n"},
		{name: "log", path: "log.md"},
		{name: "nested reserved", path: "notes/log.md"},
	} {
		for _, newline := range []struct{ name, value string }{
			{name: "LF", value: "\n"},
			{name: "CRLF", value: "\r\n"},
		} {
			t.Run(test.name+"/"+newline.name, func(t *testing.T) {
				// Arrange.
				content := test.head + "\nClaim [1] and [^spec].\n\n[^spec]: " + resource + "\n"
				content = strings.ReplaceAll(content, "\n", newline.value)
				source := memorySource{test.path: []byte(content)}
				before := cloneMemorySource(source)
				input := store.V01ToV02Migration{
					TimestampPolicy:   store.LegacyTimestampPreserve,
					TimestampConflict: store.TimestampConflictReject,
					Citations: []store.DocumentCitationMigration{{
						Path: test.path,
						Entries: []store.LegacyCitationMapping{{
							LegacyNumber: 1,
							SourceID:     "spec",
							Resource:     resource,
						}},
					}},
				}
				resolution, err := ResolveMigrationSource(context.Background(), source, MigrationSourceOptions{
					RequestedSelector: MigrationVersionV02,
				})
				if err != nil {
					t.Fatal(err)
				}
				wantStart := bytes.Index(source[test.path], []byte("[1]"))

				// Act.
				got, err := PreflightV01ToV02MigrationInput(
					context.Background(),
					source,
					resolution,
					input,
				)

				// Assert.
				if err == nil || got.Blockers == nil ||
					got.Blockers[0].Code != "migration_replay_mismatch" ||
					got.Blockers[0].Location != (SourceSpan{Start: wantStart, End: wantStart + 3}) ||
					!equalMemorySource(source, before) {
					t.Fatalf("preflight = (%#v, %v), mutated=%t", got, err, !equalMemorySource(source, before))
				}
			})
		}
	}
}

func TestMigrationPlannerPreview_CitationClaimMismatchHasNoAuthorizationArtifacts(t *testing.T) {
	const resource = "https://example.test/spec"
	// Arrange.
	source := memorySource{
		"index.md": []byte("---\nokf_version: \"0.2\"\n---\n"),
		"log.md":   []byte("Claim [1].\n\n[^spec]: " + resource + "\n"),
	}
	before := cloneMemorySource(source)
	request := MigrationRequest{
		ID: "claim-replay-mismatch", Actor: "operator",
		FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
		Migration: store.V01ToV02Migration{
			TimestampPolicy:   store.LegacyTimestampPreserve,
			TimestampConflict: store.TimestampConflictReject,
			Citations: []store.DocumentCitationMigration{{
				Path: "log.md",
				Entries: []store.LegacyCitationMapping{{
					LegacyNumber: 1, SourceID: "spec", Resource: resource,
				}},
			}},
		},
	}
	resolution := testMigrationResolution(t, source)

	// Act.
	got, err := NewMigrationPlanner().Preview(context.Background(), source, resolution, request)

	// Assert.
	if err == nil ||
		got.Staged != nil ||
		got.PlanDigest != "" ||
		got.Proof.FormatVersion != 0 ||
		got.Proof.ResolutionDigest != "" ||
		len(got.Preview.Writes) != 0 ||
		got.Preview.BaseRevision != got.Preview.ResultRevision ||
		len(got.Blockers) != 1 ||
		got.Blockers[0].Code != "migration_replay_mismatch" ||
		!equalMemorySource(source, before) {
		t.Fatalf("Preview() = (%#v, %v), mutated=%t", got, err, !equalMemorySource(source, before))
	}
}

func TestCitationClaimReplay_UsesCallerFrozenTargetModes(t *testing.T) {
	const resource = "https://example.test/spec"
	input := store.V01ToV02Migration{
		TimestampPolicy:   store.LegacyTimestampPreserve,
		TimestampConflict: store.TimestampConflictReject,
		Citations: []store.DocumentCitationMigration{{
			Path: "a.md",
			Entries: []store.LegacyCitationMapping{{
				LegacyNumber: 1, SourceID: "spec", Resource: resource,
			}},
		}},
	}
	concept := []byte("---\ntype: Knowledge\nsources:\n  - id: spec\n    resource: " + resource +
		"\n---\n\nClaim [^spec].\n\n[^spec]: " + resource + "\n")
	for _, test := range []struct {
		name    string
		source  memorySource
		options MigrationSourceOptions
		want    MigrationTransition
	}{
		{
			name: "declared target",
			source: memorySource{
				"index.md": []byte("---\nokf_version: \"0.2\"\n---\n\n# Knowledge\n\n- [A](a.md)\n"),
				"a.md":     concept,
			},
			options: MigrationSourceOptions{
				RequestedSelector: MigrationSelectorAuto,
			},
			want: MigrationTransitionTargetNoop,
		},
		{
			name:   "rootless default target",
			source: memorySource{"a.md": concept},
			options: MigrationSourceOptions{
				RequestedSelector: MigrationSelectorAuto,
			},
			want: MigrationTransitionTargetNoop,
		},
		{
			name:   "explicit target",
			source: memorySource{"a.md": concept},
			options: MigrationSourceOptions{
				RequestedSelector: MigrationVersionV02,
			},
			want: MigrationTransitionTargetNoop,
		},
		{
			name: "allowed future target",
			source: memorySource{
				"index.md": []byte("---\nokf_version: \"9.0\"\n---\n\n# Knowledge\n\n- [A](a.md)\n"),
				"a.md":     concept,
			},
			options: MigrationSourceOptions{
				RequestedSelector: MigrationSelectorAuto,
				AllowFutureNoop:   true,
			},
			want: MigrationTransitionTargetNoop,
		},
		{
			name:   "explicit source remains transition on target-shaped bytes",
			source: memorySource{"a.md": concept},
			options: MigrationSourceOptions{
				RequestedSelector: MigrationVersionV01,
			},
			want: MigrationTransitionV01ToV02,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			resolution, err := ResolveMigrationSource(context.Background(), test.source, test.options)
			if err != nil {
				t.Fatal(err)
			}
			before := cloneMemorySource(test.source)

			// Act.
			preflight, preflightErr := PreflightV01ToV02MigrationInput(
				context.Background(),
				test.source,
				resolution,
				input,
			)
			preview, previewErr := NewMigrationPlanner().Preview(
				context.Background(),
				test.source,
				resolution,
				MigrationRequest{
					ID:    store.ChangeSetID("frozen-" + strings.ReplaceAll(test.name, " ", "-")),
					Actor: "operator", FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
					Migration: input,
				},
			)

			// Assert.
			if (test.want == MigrationTransitionTargetNoop &&
				(preflightErr != nil || len(preflight.Blockers) != 0)) ||
				previewErr != nil ||
				preview.Resolution.Transition != test.want ||
				preview.Resolution.RequestedSelector != resolution.RequestedSelector ||
				preview.Proof.ResolutionDigest == "" ||
				len(preview.Preview.Writes) != 0 ||
				preview.Preview.BaseRevision != preview.Preview.ResultRevision ||
				!equalMemorySource(test.source, before) {
				t.Fatalf(
					"resolution=%#v preflight=(%#v,%v) preview=(%#v,%v)",
					resolution,
					preflight,
					preflightErr,
					preview,
					previewErr,
				)
			}
		})
	}
}

func TestPreflightV01ToV02MigrationInput_RejectsForgedDefaultTargetMode(t *testing.T) {
	// Arrange.
	source := memorySource{
		"log.md": []byte("Claim [1].\n\n# Citations\n\n[1] https://example.test/spec\n"),
	}
	forged := MigrationSourceResolution{
		RequestedSelector: MigrationSelectorAuto,
		DeclarationValid:  true,
		ResolvedSource:    MigrationVersionV02,
		ResolutionSource:  MigrationResolutionDefault,
		FromVersion:       MigrationVersionV02,
		ToVersion:         MigrationVersionV02,
		Transition:        MigrationTransitionTargetNoop,
	}
	input := store.V01ToV02Migration{
		TimestampPolicy:   store.LegacyTimestampPreserve,
		TimestampConflict: store.TimestampConflictReject,
	}

	// Act.
	got, err := PreflightV01ToV02MigrationInput(context.Background(), source, forged, input)

	// Assert.
	if !errors.Is(err, ErrUnsupportedMigrationVersion) ||
		len(got.Blockers) != 0 ||
		len(got.ManualActions) != 0 {
		t.Fatalf("PreflightV01ToV02MigrationInput() = (%#v, %v)", got, err)
	}
}

func TestMigrationPlannerApply_ResolutionTamperStopsBeforeStore(t *testing.T) {
	const resource = "https://example.test/spec"
	// Arrange.
	source := memorySource{
		"a.md": []byte("---\ntype: Knowledge\nsources:\n  - id: spec\n    resource: " + resource +
			"\n---\n\nClaim [^spec].\n\n[^spec]: " + resource + "\n"),
	}
	resolution, err := ResolveMigrationSource(context.Background(), source, MigrationSourceOptions{
		RequestedSelector: MigrationSelectorAuto,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := MigrationRequest{
		ID: "resolution-tamper", Actor: "operator",
		FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
		Migration: store.V01ToV02Migration{
			TimestampPolicy:   store.LegacyTimestampPreserve,
			TimestampConflict: store.TimestampConflictReject,
			Citations: []store.DocumentCitationMigration{{
				Path: "a.md",
				Entries: []store.LegacyCitationMapping{{
					LegacyNumber: 1, SourceID: "spec", Resource: resource,
				}},
			}},
		},
	}
	planner := NewMigrationPlanner()
	preview, err := planner.Preview(context.Background(), source, resolution, request)
	if err != nil {
		t.Fatal(err)
	}
	tampered := resolution.Clone()
	tampered.RequestedSelector = MigrationVersionV02
	tampered.ResolutionSource = MigrationResolutionExplicit
	destination := &countingMigrationStore{}

	// Act.
	_, err = planner.Apply(context.Background(), destination, MigrationApplyRequest{
		Request: request, Resolution: tampered, Proof: preview.Proof, PlanDigest: preview.PlanDigest,
	})

	// Assert.
	if !errors.Is(err, ErrMigrationPlanMismatch) ||
		destination.snapshots != 0 ||
		destination.previews != 0 ||
		destination.commits != 0 {
		t.Fatalf("Apply() error=%v store=%#v", err, destination)
	}
}

func TestMigrationPlannerPreview_UsesExactlyOneCallerResolution(t *testing.T) {
	// Arrange.
	source := memorySource{
		"a.md": []byte("---\ntype: Knowledge\n---\n\nA.\n"),
	}
	planner := NewMigrationPlanner()
	resolveCalls := 0
	resolve := func(ctx context.Context, source bundle.Source) (MigrationSourceResolution, error) {
		resolveCalls++
		return planner.ResolveSource(ctx, source, MigrationSourceOptions{
			RequestedSelector: MigrationVersionV01,
		})
	}
	resolution, err := resolve(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	request := MigrationRequest{
		ID: "single-resolution", Actor: "operator",
		FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
		Migration: store.V01ToV02Migration{
			TimestampPolicy:   store.LegacyTimestampPreserve,
			TimestampConflict: store.TimestampConflictReject,
		},
	}

	// Act.
	preview, previewErr := planner.Preview(context.Background(), source, resolution, request)
	production, readErr := os.ReadFile("migration.go")

	// Assert.
	if previewErr != nil ||
		preview.Resolution.Transition != MigrationTransitionV01ToV02 ||
		resolveCalls != 1 ||
		readErr != nil ||
		strings.Contains(string(production), "ResolveMigrationSource(") ||
		strings.Contains(string(production), ".ResolveSource(") {
		t.Fatalf(
			"Preview()=(%#v,%v) resolver calls=%d readErr=%v",
			preview,
			previewErr,
			resolveCalls,
			readErr,
		)
	}
}

func TestMigrationPlannerPreview_CitationReplayEvidenceAcrossDocumentRoles(t *testing.T) {
	roles := []struct {
		name   string
		path   string
		source func(string) memorySource
	}{
		{
			name: "root index", path: "index.md",
			source: func(nl string) memorySource {
				return memorySource{
					"index.md": []byte("---" + nl + "okf_version: \"0.1\"" + nl + "---" + nl + nl +
						"# Knowledge" + nl + nl + "- [A](a.md)" + nl + nl +
						"Claim [1]." + nl + nl + "# Citations" + nl + nl +
						"[1] [Spec](https://example.test/spec)" + nl),
					"a.md": []byte("---" + nl + "type: Knowledge" + nl + "---" + nl),
				}
			},
		},
		{
			name: "root log", path: "log.md",
			source: func(nl string) memorySource {
				return memorySource{
					"index.md": []byte("---" + nl + "okf_version: \"0.1\"" + nl + "---" + nl + nl +
						"# Knowledge" + nl + nl + "- [A](a.md)" + nl),
					"a.md": []byte("---" + nl + "type: Knowledge" + nl + "---" + nl),
					"log.md": []byte("Claim [1]." + nl + nl + "# Citations" + nl + nl +
						"[1] [Spec](https://example.test/spec)" + nl + nl +
						"# Log" + nl + nl + "## 2026-01-01" + nl + nl + "- event" + nl),
				}
			},
		},
		{
			name: "nested index", path: "nested/index.md",
			source: func(nl string) memorySource {
				return memorySource{
					"index.md": []byte("---" + nl + "okf_version: \"0.1\"" + nl + "---" + nl + nl +
						"# Knowledge" + nl + nl + "- [Nested](nested/index.md)" + nl),
					"nested/index.md": []byte("# Nested" + nl + nl + "- [A](a.md)" + nl + nl +
						"Claim [1]." + nl + nl + "# Citations" + nl + nl +
						"[1] [Spec](https://example.test/spec)" + nl),
					"nested/a.md": []byte("---" + nl + "type: Knowledge" + nl + "---" + nl),
				}
			},
		},
		{
			name: "concept", path: "a.md",
			source: func(nl string) memorySource {
				return memorySource{
					"index.md": []byte("---" + nl + "okf_version: \"0.1\"" + nl + "---" + nl + nl +
						"# Knowledge" + nl + nl + "- [A](a.md)" + nl),
					"a.md": []byte("---" + nl + "type: Knowledge" + nl + "---" + nl + nl +
						"Claim [1]." + nl + nl + "# Citations" + nl + nl +
						"[1] [Spec](https://example.test/spec)" + nl),
				}
			},
		},
	}
	for _, role := range roles {
		for _, newline := range []struct{ name, value string }{
			{name: "LF", value: "\n"},
			{name: "CRLF", value: "\r\n"},
		} {
			t.Run(role.name+"/"+newline.name, func(t *testing.T) {
				// Arrange.
				source := role.source(newline.value)
				request := citationMigrationRequest(
					"citation-role-"+strings.ReplaceAll(role.name+"-"+newline.name, " ", "-"),
					role.path,
					store.LegacyCitationMapping{
						LegacyNumber: 1,
						LegacyEntry:  "[Spec](https://example.test/spec)",
						SourceID:     "spec",
					},
				)
				planner := NewMigrationPlanner()

				// Act.
				preview, previewErr := planner.Preview(
					context.Background(),
					source,
					testMigrationResolution(t, source),
					request,
				)
				var replay MigrationPreview
				var replayErr error
				var tamperedPreview MigrationPreview
				var tamperErr error
				if previewErr == nil {
					replay, replayErr = planner.Preview(
						context.Background(),
						preview.Staged,
						testMigrationResolution(t, preview.Staged),
						request,
					)
				}
				if previewErr == nil {
					tampered := cloneMigrationMemorySource(t, preview.Staged)
					tampered[role.path] = bytes.Replace(
						tampered[role.path],
						[]byte("[^spec]: [Spec](https://example.test/spec)"),
						[]byte("[^spec]: [Spec](https://invalid.test/spec)"),
						1,
					)
					tamperedPreview, tamperErr = planner.Preview(
						context.Background(),
						tampered,
						testMigrationResolution(t, tampered),
						request,
					)
				}

				// Assert.
				if previewErr != nil || replayErr != nil {
					t.Fatalf(
						"transition/replay errors = %v/%v; blockers = %#v/%#v",
						previewErr,
						replayErr,
						preview.Blockers,
						replay.Blockers,
					)
				}
				if len(replay.Preview.Writes) != 0 ||
					replay.Preview.BaseRevision != replay.Preview.ResultRevision {
					t.Fatalf("identical replay is not a noop: %#v", replay.Preview)
				}
				if tamperErr == nil ||
					len(tamperedPreview.Blockers) != 1 ||
					tamperedPreview.Blockers[0].Code != "migration_replay_mismatch" ||
					tamperedPreview.Staged != nil {
					t.Fatalf("tampered replay = (%#v, %v)", tamperedPreview, tamperErr)
				}
			})
		}
	}
}

func TestMigrationPlannerPreview_NumberOnlyCitationNeedsDurableReplayEvidence(t *testing.T) {
	tests := []struct {
		name        string
		legacyEntry string
		mapping     store.LegacyCitationMapping
		wantBlocked bool
	}{
		{
			name:        "number-only titled link",
			legacyEntry: "[Spec](https://example.test/spec)",
			mapping:     store.LegacyCitationMapping{LegacyNumber: 1, SourceID: "spec"},
			wantBlocked: true,
		},
		{
			name:        "title-only",
			legacyEntry: "https://example.test/spec",
			mapping: store.LegacyCitationMapping{
				LegacyNumber: 1, SourceID: "spec", Title: "Spec",
			},
			wantBlocked: true,
		},
		{
			name:        "resource-only bare URL",
			legacyEntry: "https://example.test/spec",
			mapping: store.LegacyCitationMapping{
				LegacyNumber: 1, SourceID: "spec", Resource: "https://example.test/spec",
			},
		},
		{
			name:        "explicit title and resource",
			legacyEntry: "[Spec](https://example.test/spec)",
			mapping: store.LegacyCitationMapping{
				LegacyNumber: 1, SourceID: "spec",
				Title: "Spec", Resource: "https://example.test/spec",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			source := memorySource{
				"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [A](a.md)\n"),
				"a.md":     []byte("---\ntype: Knowledge\n---\n"),
				"log.md": []byte(
					"Claim [1].\n\n# Citations\n\n[1] " + test.legacyEntry +
						"\n\n# Log\n\n## 2026-01-01\n\n- event\n",
				),
			}
			request := citationMigrationRequest(
				"number-only-"+strings.ReplaceAll(test.name, " ", "-"),
				"log.md",
				test.mapping,
			)
			planner := NewMigrationPlanner()

			// Act.
			preview, err := planner.Preview(
				context.Background(),
				source,
				testMigrationResolution(t, source),
				request,
			)
			var replay MigrationPreview
			var replayErr error
			if err == nil {
				replay, replayErr = planner.Preview(
					context.Background(),
					preview.Staged,
					testMigrationResolution(t, preview.Staged),
					request,
				)
			}

			// Assert.
			if test.wantBlocked {
				if !errors.Is(err, ErrUnsupportedPresentation) ||
					len(preview.Blockers) != 1 ||
					preview.Blockers[0].Code != "migration_citation_replay_evidence_missing" ||
					preview.Staged != nil ||
					len(preview.Preview.Writes) != 0 {
					t.Fatalf("blocked preview = (%#v, %v)", preview, err)
				}
				return
			}
			if err != nil || replayErr != nil {
				t.Fatalf("transition/replay errors = %v/%v", err, replayErr)
			}
			if len(replay.Preview.Writes) != 0 ||
				replay.Preview.BaseRevision != replay.Preview.ResultRevision {
				t.Fatalf("replay is not a noop: %#v", replay.Preview)
			}
		})
	}
}

func TestMigrationPlannerPreview_TargetReservedNumberOnlyWithoutProjectionFailsClosed(t *testing.T) {
	// Arrange.
	source := memorySource{
		"index.md": []byte("---\nokf_version: \"0.2\"\n---\n\n# Knowledge\n\n- [A](a.md)\n"),
		"a.md":     []byte("---\ntype: Knowledge\n---\n"),
		"log.md": []byte(
			"Claim [^spec].\n\n[^spec]: [Spec](https://example.test/spec)\n\n" +
				"# Log\n\n## 2026-01-01\n\n- event\n",
		),
	}
	request := citationMigrationRequest(
		"target-number-only-without-projection",
		"log.md",
		store.LegacyCitationMapping{LegacyNumber: 1, SourceID: "spec"},
	)

	// Act.
	preview, err := NewMigrationPlanner().Preview(
		context.Background(),
		source,
		testMigrationResolution(t, source),
		request,
	)

	// Assert.
	if !errors.Is(err, ErrUnsupportedPresentation) ||
		len(preview.Blockers) != 1 ||
		preview.Blockers[0].Code != "migration_citation_replay_evidence_missing" ||
		preview.Staged != nil ||
		len(preview.Preview.Writes) != 0 {
		t.Fatalf("target replay = (%#v, %v), want evidence blocker and zero stage", preview, err)
	}
}

func TestMigrationPlannerPreview_NumberOnlyConceptUsesStructuredSourceProjection(t *testing.T) {
	// Arrange.
	source := memorySource{
		"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [A](a.md)\n"),
		"a.md": []byte(
			"---\ntype: Knowledge\n---\n\nClaim [1].\n\n# Citations\n\n" +
				"[1] [Spec](https://example.test/spec)\n",
		),
	}
	request := citationMigrationRequest(
		"number-only-concept-projection",
		"a.md",
		store.LegacyCitationMapping{LegacyNumber: 1, SourceID: "spec"},
	)
	planner := NewMigrationPlanner()

	// Act.
	preview, previewErr := planner.Preview(
		context.Background(),
		source,
		testMigrationResolution(t, source),
		request,
	)
	var replay MigrationPreview
	var replayErr error
	var tamperedPreview MigrationPreview
	var tamperErr error
	if previewErr == nil {
		replay, replayErr = planner.Preview(
			context.Background(),
			preview.Staged,
			testMigrationResolution(t, preview.Staged),
			request,
		)
	}
	if previewErr == nil {
		tampered := cloneMigrationMemorySource(t, preview.Staged)
		tampered["a.md"] = bytes.Replace(
			tampered["a.md"],
			[]byte("[^spec]: [Spec](https://example.test/spec)"),
			[]byte("[^spec]: [Spec](https://invalid.test/spec)"),
			1,
		)
		tamperedPreview, tamperErr = planner.Preview(
			context.Background(),
			tampered,
			testMigrationResolution(t, tampered),
			request,
		)
	}

	// Assert.
	if previewErr != nil || replayErr != nil {
		t.Fatalf(
			"transition/replay errors = %v/%v; blockers = %#v/%#v",
			previewErr,
			replayErr,
			preview.Blockers,
			replay.Blockers,
		)
	}
	if len(replay.Preview.Writes) != 0 ||
		replay.Preview.BaseRevision != replay.Preview.ResultRevision {
		t.Fatalf("structured-source replay is not a noop: %#v", replay.Preview)
	}
	if tamperErr == nil ||
		len(tamperedPreview.Blockers) != 1 ||
		tamperedPreview.Blockers[0].Code != "migration_replay_mismatch" ||
		tamperedPreview.Staged != nil {
		t.Fatalf("tampered structured-source replay = (%#v, %v)", tamperedPreview, tamperErr)
	}
}

func citationMigrationRequest(
	id string,
	path string,
	entry store.LegacyCitationMapping,
) MigrationRequest {
	return MigrationRequest{
		ID: store.ChangeSetID(id), Actor: "operator",
		FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
		Migration: store.V01ToV02Migration{
			TimestampPolicy:   store.LegacyTimestampPreserve,
			TimestampConflict: store.TimestampConflictReject,
			Citations: []store.DocumentCitationMigration{{
				Path: path, Entries: []store.LegacyCitationMapping{entry},
			}},
		},
	}
}

func cloneMigrationMemorySource(t *testing.T, source bundle.Source) memorySource {
	t.Helper()
	paths, err := source.Paths(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := make(memorySource, len(paths))
	for _, path := range paths {
		content, err := source.ReadFile(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		out[path] = content
	}
	return out
}
