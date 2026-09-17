package mutation

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/skosovsky/okf/internal/documentlayout"
	"github.com/skosovsky/okf/internal/markdownowner"
	"github.com/skosovsky/okf/store"
)

func TestApplyComputationBoundary_CreatesOneExplicitInlineBoundaryAndReplays(t *testing.T) {
	tests := []struct {
		name        string
		newline     string
		computation string
	}{
		{name: "LF/non-empty", newline: "\n", computation: "SELECT 1;\n"},
		{name: "LF/present-empty", newline: "\n"},
		{name: "CRLF/non-empty", newline: "\r\n", computation: "SELECT 1;\r\n"},
		{name: "CRLF/present-empty", newline: "\r\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			source := []byte(strings.ReplaceAll(
				"---\ntype: Knowledge\n---\n\nNarrative without a computation section.",
				"\n",
				test.newline,
			))
			migration := store.ComputationMigration{
				Path: "query.md",
				Contract: store.AttestedComputationContract{
					Mode:              store.AttestedComputationModeInline,
					InlineComputation: test.computation,
					InlineLanguage:    "sql",
				},
			}

			// Act.
			updated, err := applyComputationBoundary(context.Background(), source, migration)
			replayed, replayErr := applyComputationBoundary(context.Background(), updated, migration)
			layout, hasFrontmatter, splitErr := documentlayout.Split(updated)
			inspection := markdownowner.ComputationInspection{}
			if splitErr == nil && hasFrontmatter {
				inspection = markdownowner.InspectComputation(string(updated[layout.BodyStart:]))
			}

			// Assert.
			if err != nil || replayErr != nil || splitErr != nil {
				t.Fatalf("create/replay/split errors = %v/%v/%v", err, replayErr, splitErr)
			}
			if !hasFrontmatter || !bytes.HasPrefix(updated, source) {
				t.Fatalf("creation changed caller-owned prefix:\n%s", updated)
			}
			if inspection.State != markdownowner.ComputationInline ||
				len(inspection.Sections) != 1 ||
				len(inspection.DirectFences) != 1 {
				t.Fatalf("inspection = %#v, want one parser-owned inline boundary", inspection)
			}
			fence := inspection.DirectFences[0]
			if fence.Info != "sql" ||
				fence.Content != computationFenceContent(updated, test.computation) {
				t.Fatalf("fence = %#v, want exact language/content", fence)
			}
			if !bytes.Equal(replayed, updated) {
				t.Fatalf("identical replay changed bytes:\n%s", replayed)
			}
		})
	}
}

func TestApplyComputationBoundary_CreationDoesNotAdoptExistingInvalidBoundary(t *testing.T) {
	tests := []struct {
		name     string
		source   string
		wantCode string
	}{
		{
			name:     "empty section",
			source:   "# Computation\n\nNarrative.\n",
			wantCode: "missing_computation_boundary",
		},
		{
			name:     "mismatched direct fence",
			source:   "# Computation\n\n```sql\nSELECT 2;\n```\n",
			wantCode: "sanctioned_computation_mismatch",
		},
		{
			name: "multiple direct fences",
			source: "# Computation\n\n```sql\nSELECT 1;\n```\n\n" +
				"```sql\nSELECT 1;\n```\n",
			wantCode: "ambiguous_computation_boundary",
		},
		{
			name:     "nested fence",
			source:   "# Computation\n\n> ```sql\n> SELECT 1;\n> ```\n",
			wantCode: "ambiguous_computation_boundary",
		},
		{
			name:     "unclosed fence",
			source:   "# Computation\n\n```sql\nSELECT 1;\n",
			wantCode: "ambiguous_computation_boundary",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			source := []byte(test.source)
			migration := store.ComputationMigration{
				Path: "query.md",
				Contract: store.AttestedComputationContract{
					Mode:              store.AttestedComputationModeInline,
					InlineComputation: "SELECT 1;\n",
					InlineLanguage:    "sql",
				},
			}

			// Act.
			updated, err := applyComputationBoundary(context.Background(), source, migration)
			var presentation *PresentationError

			// Assert.
			if updated != nil ||
				!errors.As(err, &presentation) ||
				presentation.Code != test.wantCode {
				t.Fatalf(
					"applyComputationBoundary() = (%q, %#v), want nil/%s",
					updated,
					err,
					test.wantCode,
				)
			}
		})
	}
}

func TestMigrationPlannerPreview_CreatesMissingInlineBoundaryAndTargetReplayIsNoop(t *testing.T) {
	tests := []struct {
		name        string
		newline     string
		computation string
	}{
		{name: "LF/non-empty", newline: "\n", computation: "SELECT 1;\n"},
		{name: "CRLF/present-empty", newline: "\r\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			nl := test.newline
			source := memorySource{
				"index.md": []byte("---" + nl + "okf_version: \"0.1\"" + nl + "---" + nl + nl +
					"# Computations" + nl + nl + "- [Query](query.md)" + nl),
				"query.md": []byte("---" + nl + "type: Knowledge" + nl + "---" + nl + nl +
					"Narrative stays." + nl),
			}
			request := MigrationRequest{
				ID:    "create-inline-" + store.ChangeSetID(strings.ReplaceAll(test.name, "/", "-")),
				Actor: "operator", FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
				Migration: store.V01ToV02Migration{
					GeneratedBy:       "process:migration",
					TimestampPolicy:   store.LegacyTimestampPreserve,
					TimestampConflict: store.TimestampConflictReject,
					Computations: []store.ComputationMigration{{
						Path: "query.md",
						Contract: store.AttestedComputationContract{
							Mode:              store.AttestedComputationModeInline,
							Runtime:           "sql",
							InlineComputation: test.computation,
							InlineLanguage:    "sql",
							Executor: store.ExecutorContract{
								Resource: "executors/sql.md",
								Receipt:  []string{"rows"},
							},
							Attester: store.AttesterContract{Resource: "attesters/sql.md"},
						},
					}},
				},
			}
			planner := NewMigrationPlanner()

			// Act.
			preview, previewErr := planner.Preview(
				context.Background(),
				source,
				testMigrationResolution(t, source),
				request,
			)
			var query []byte
			var replay MigrationPreview
			var replayErr error
			if previewErr == nil {
				query, previewErr = preview.Staged.ReadFile(context.Background(), "query.md")
			}
			if previewErr == nil {
				replay, replayErr = planner.Preview(
					context.Background(),
					preview.Staged,
					testMigrationResolution(t, preview.Staged),
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
			if !bytes.Contains(query, []byte("Narrative stays."+nl+"# Computation"+nl+nl)) ||
				bytes.Count(query, []byte("# Computation")) != 1 {
				t.Fatalf("inline boundary creation mismatch:\n%s", query)
			}
			if len(replay.Preview.Writes) != 0 ||
				replay.Preview.BaseRevision != replay.Preview.ResultRevision {
				t.Fatalf("target replay is not a noop: %#v", replay.Preview)
			}
		})
	}
}
