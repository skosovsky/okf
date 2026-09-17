package mutation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
	"github.com/skosovsky/okf/validator"
)

func TestMigrationPlannerPreview_MigratesTimestampAndCitationsDeterministicallyWithoutWrites(t *testing.T) {
	// Arrange.
	source := memorySource{
		"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [Alpha](alpha.md)\n"),
		"alpha.md": []byte("---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\nx-extra: keep\n---\n\nClaim [1].\n\n# Citations\n\n[1] [Spec](https://example.test/spec)\n"),
	}
	before := cloneMemorySource(source)
	request := MigrationRequest{
		ID:          "migration-preview",
		Actor:       "operator",
		FromVersion: MigrationVersionV01,
		ToVersion:   MigrationVersionV02,
		Migration: store.V01ToV02Migration{
			GeneratedBy:       "process:migration",
			TimestampPolicy:   store.LegacyTimestampRemoveAfterCopy,
			TimestampConflict: store.TimestampConflictReject,
			Citations: []store.DocumentCitationMigration{{
				Path: "alpha.md",
				Entries: []store.LegacyCitationMapping{{
					LegacyNumber: 1,
					LegacyEntry:  "[Spec](https://example.test/spec)",
					SourceID:     "spec",
				}},
			}},
		},
	}
	planner := NewMigrationPlanner()

	// Act.
	first, firstErr := planner.Preview(context.Background(), source, testMigrationResolution(t, source), request)
	second, secondErr := planner.Preview(context.Background(), source, testMigrationResolution(t, source), request)

	// Assert.
	if firstErr != nil || secondErr != nil {
		t.Fatalf("Preview() errors = %v, %v; diagnostics = %#v", firstErr, secondErr, first.Preview.Diagnostics)
	}
	replay, replayErr := planner.Preview(context.Background(), first.Staged, testMigrationResolution(t, first.Staged), request)
	if replayErr != nil {
		t.Fatalf("second migration Preview() error = %v", replayErr)
	}
	if first.PlanDigest == "" || first.PlanDigest != second.PlanDigest {
		t.Fatalf("plan digests = %q, %q", first.PlanDigest, second.PlanDigest)
	}
	if first.Preview.BaseRevision != second.Preview.BaseRevision || first.Preview.ResultRevision != second.Preview.ResultRevision {
		t.Fatalf("preview revisions are not deterministic: %#v %#v", first.Preview, second.Preview)
	}
	if len(replay.Preview.Writes) != 0 || replay.Preview.BaseRevision != replay.Preview.ResultRevision {
		t.Fatalf("identical second migration is not a noop: %#v", replay.Preview)
	}
	if !equalMemorySource(source, before) {
		t.Fatal("Preview mutated source bytes")
	}
	alpha, err := first.Staged.ReadFile(context.Background(), "alpha.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range [][]byte{
		[]byte("generated:\n  by: process:migration\n  at: 2026-06-25T09:00:00Z\n"),
		[]byte("sources:\n  -\n    id: spec\n    title: Spec\n    resource: https://example.test/spec\n"),
		[]byte("x-extra: keep\n"),
		[]byte("Claim [^spec].\n"),
		[]byte("[^spec]: [Spec](https://example.test/spec)\n"),
	} {
		if !bytes.Contains(alpha, want) {
			t.Fatalf("staged alpha.md does not contain %q:\n%s", want, alpha)
		}
	}
	if bytes.Contains(alpha, []byte("timestamp:")) || bytes.Contains(alpha, []byte("# Citations")) {
		t.Fatalf("legacy presentation remains:\n%s", alpha)
	}
	index, err := first.Staged.ReadFile(context.Background(), "index.md")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(index, []byte("okf_version: \"0.2\"")) {
		t.Fatalf("staged index.md =\n%s", index)
	}
	tampered := memorySource{
		"index.md": append([]byte(nil), index...),
		"alpha.md": bytes.Replace(
			alpha,
			[]byte("[^spec]: [Spec](https://example.test/spec)"),
			[]byte("[^spec]: [Spec](https://evil.test/spec)"),
			1,
		),
	}
	blocked, tamperErr := planner.Preview(context.Background(), tampered, testMigrationResolution(t, tampered), request)
	if tamperErr == nil {
		t.Fatalf("Preview(tampered footnote) = %#v, want replay mismatch", blocked)
	}
	if blocked.Staged != nil || len(blocked.Preview.Writes) != 0 {
		t.Fatalf("tampered replay exposed stage: %#v", blocked)
	}
}

func TestMigrationPlannerPreview_MigratesCanonicalV01AppendixLosslesslyAndReplays(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	source := &bundle.FileSystemSource{Root: "../fixtures/spec-minimal"}
	t.Cleanup(func() { _ = source.Close() })
	paths, err := source.Paths(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before := make(map[string][]byte, len(paths))
	for _, path := range paths {
		content, readErr := source.ReadFile(ctx, path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		before[path] = append([]byte(nil), content...)
	}
	resolution, err := ResolveMigrationSource(ctx, source, MigrationSourceOptions{
		RequestedSelector: MigrationSelectorAuto,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := MigrationRequest{
		ID:          "canonical-v01-appendix",
		Actor:       "operator",
		FromVersion: MigrationVersionV01,
		ToVersion:   MigrationVersionV02,
		Migration: store.V01ToV02Migration{
			GeneratedBy:       "process:migration",
			TimestampPolicy:   store.LegacyTimestampRemoveAfterCopy,
			TimestampConflict: store.TimestampConflictReject,
		},
	}
	timestamp := []byte("timestamp: 2026-05-28T00:00:00Z\n")
	generated := []byte("generated:\n  by: process:migration\n  at: 2026-05-28T00:00:00Z\n")
	want := make(map[string][]byte, len(before))
	for path, content := range before {
		want[path] = append([]byte(nil), content...)
	}
	for _, path := range []string{
		"datasets/sales.md",
		"tables/customers.md",
		"tables/orders.md",
	} {
		want[path] = bytes.Replace(want[path], timestamp, generated, 1)
	}
	want["index.md"] = append(
		[]byte("---\nokf_version: \"0.2\"\n---\n"),
		want["index.md"]...,
	)
	planner := NewMigrationPlanner()

	// Act.
	preview, previewErr := planner.Preview(ctx, source, resolution, request)

	// Assert.
	if previewErr != nil {
		t.Fatalf("Preview() error = %v; blockers = %#v", previewErr, preview.Blockers)
	}
	if resolution.ResolutionSource != MigrationResolutionLegacyProbe ||
		resolution.Transition != MigrationTransitionV01ToV02 {
		t.Fatalf("source resolution = %#v, want canonical legacy transition", resolution)
	}
	for _, path := range paths {
		got, readErr := preview.Staged.ReadFile(ctx, path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !bytes.Equal(got, want[path]) {
			t.Fatalf("%s differs from the exact lossless migration:\n%s", path, got)
		}
	}
	target, err := bundle.Load(ctx, preview.Staged)
	if err != nil {
		t.Fatal(err)
	}
	validation, err := validator.ValidateBundleContext(ctx, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !validation.IsConformant() {
		t.Fatalf("migrated Appendix A is not target-v0.2 conformant: %#v", validation)
	}
	replay, replayErr := planner.Preview(ctx, preview.Staged, testMigrationResolution(t, preview.Staged), request)
	if replayErr != nil {
		t.Fatalf("identical replay error = %v; blockers = %#v", replayErr, replay.Blockers)
	}
	if len(replay.Preview.Writes) != 0 ||
		replay.Preview.BaseRevision != replay.Preview.ResultRevision {
		t.Fatalf("identical replay is not a noop: %#v", replay.Preview)
	}
}

func TestMigrationPlannerPreview_PreservesPreExistingGeneratedProducerAcrossMixedReplay(t *testing.T) {
	// Arrange.
	source := memorySource{
		"index.md":    []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [Legacy](legacy.md)\n- [Existing](existing.md)\n"),
		"legacy.md":   []byte("---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n---\n"),
		"existing.md": []byte("---\ntype: Knowledge\ngenerated:\n  by: process:existing\n  at: 2026-06-24T09:00:00Z\n---\n"),
	}
	request := MigrationRequest{
		ID:          "mixed-existing-generated-producer",
		Actor:       "operator",
		FromVersion: MigrationVersionV01,
		ToVersion:   MigrationVersionV02,
		Migration: store.V01ToV02Migration{
			GeneratedBy:       "process:migration",
			TimestampPolicy:   store.LegacyTimestampRemoveAfterCopy,
			TimestampConflict: store.TimestampConflictReject,
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
	replay, replayErr := planner.Preview(
		context.Background(),
		preview.Staged,
		testMigrationResolution(t, preview.Staged),
		request,
	)

	// Assert.
	if previewErr != nil {
		t.Fatalf("Preview() error = %v; blockers = %#v", previewErr, preview.Blockers)
	}
	if replayErr != nil {
		t.Fatalf("identical replay error = %v; blockers = %#v", replayErr, replay.Blockers)
	}
	existing, err := preview.Staged.ReadFile(context.Background(), "existing.md")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(existing, source["existing.md"]) {
		t.Fatalf("pre-existing generated producer changed:\n%s", existing)
	}
	if len(replay.Preview.Writes) != 0 ||
		replay.Preview.BaseRevision != replay.Preview.ResultRevision {
		t.Fatalf("identical replay is not a noop: %#v", replay.Preview)
	}
}

func TestMigrationPlannerReplay_BlocksNormalizedFootnoteLabelDrift(t *testing.T) {
	// Arrange.
	source := memorySource{
		"index.md": []byte("---\nokf_version: \"0.2\"\n---\n\n# Knowledge\n\n- [Alpha](alpha.md)\n"),
		"alpha.md": []byte("---\ntype: Knowledge\n" +
			"generated:\n  by: process:migration\n  at: 2026-06-25T09:00:00Z\n" +
			"sources:\n  -\n    id: spec\n    title: Spec\n    resource: https://example.test/spec\n" +
			"---\n\nClaim [^Spec].\n\n[^Spec]: [Spec](https://example.test/spec)\n"),
	}
	request := MigrationRequest{
		ID: "normalized-replay-drift", Actor: "operator",
		FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
		Migration: store.V01ToV02Migration{
			GeneratedBy:       "process:migration",
			TimestampPolicy:   store.LegacyTimestampRemoveAfterCopy,
			TimestampConflict: store.TimestampConflictReject,
			Citations: []store.DocumentCitationMigration{{
				Path: "alpha.md",
				Entries: []store.LegacyCitationMapping{{
					LegacyNumber: 1,
					LegacyEntry:  "[Spec](https://example.test/spec)",
					SourceID:     "spec",
				}},
			}},
		},
	}

	// Act.
	preview, err := NewMigrationPlanner().Preview(context.Background(), source, testMigrationResolution(t, source), request)

	// Assert.
	if !errors.Is(err, ErrAmbiguousPresentation) {
		t.Fatalf("Preview() error = %v, want ErrAmbiguousPresentation", err)
	}
	if len(preview.Blockers) != 1 ||
		preview.Blockers[0].Code != "normalized_footnote_label_collision" ||
		preview.Blockers[0].Path != "alpha.md" ||
		preview.Blockers[0].Location.End <= preview.Blockers[0].Location.Start {
		t.Fatalf("Blockers = %#v, want exact normalized collision", preview.Blockers)
	}
	if len(preview.ManualActions) != 1 ||
		preview.ManualActions[0].Code != "disambiguate_citation_entry" ||
		preview.ManualActions[0].Path != "alpha.md" {
		t.Fatalf("ManualActions = %#v", preview.ManualActions)
	}
	if preview.Staged != nil || len(preview.Preview.Writes) != 0 {
		t.Fatalf("blocked replay exposed stage: %#v", preview)
	}
}

func TestMigrationPlannerPreview_TimestampConflictReturnsZeroStage(t *testing.T) {
	// Arrange.
	source := memorySource{
		"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [Alpha](alpha.md)\n"),
		"alpha.md": []byte("---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\ngenerated:\n  by: process:old\n  at: 2026-06-26T09:00:00Z\n---\n"),
	}
	request := MigrationRequest{
		ID:          "migration-conflict",
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
	preview, err := NewMigrationPlanner().Preview(context.Background(), source, testMigrationResolution(t, source), request)

	// Assert.
	if !errors.Is(err, ErrAmbiguousPresentation) {
		t.Fatalf("Preview() error = %v, want ErrAmbiguousPresentation", err)
	}
	if preview.Staged != nil || len(preview.Preview.Writes) != 0 || len(preview.Preview.Deletes) != 0 || len(preview.Preview.Renames) != 0 {
		t.Fatalf("blocked preview exposed stage: %#v", preview)
	}
	if len(preview.Blockers) != 1 || preview.Blockers[0].Code != "timestamp_generated_conflict" {
		t.Fatalf("Blockers = %#v", preview.Blockers)
	}
}

func TestMigrationPlannerPreview_ResolvesTimestampConflictsOnlyByExplicitPolicy(t *testing.T) {
	for _, test := range []struct {
		name     string
		policy   store.TimestampConflictPolicy
		wantAt   string
		legacyAt string
	}{
		{
			name:     "use generated",
			policy:   store.TimestampConflictUseGenerated,
			wantAt:   "2026-06-26T09:00:00Z",
			legacyAt: "2026-06-25T09:00:00Z",
		},
		{
			name:     "use timestamp",
			policy:   store.TimestampConflictUseTimestamp,
			wantAt:   "2026-06-25T09:00:00Z",
			legacyAt: "2026-06-25T09:00:00Z",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			source := memorySource{
				"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [Alpha](alpha.md)\n"),
				"alpha.md": []byte("---\ntype: Knowledge\n" +
					"timestamp: 2026-06-25T09:00:00Z\n" +
					"generated:\n  by: process:existing\n  at: 2026-06-26T09:00:00Z\n---\n"),
			}
			request := MigrationRequest{
				ID:    "timestamp-conflict-" + store.ChangeSetID(strings.ReplaceAll(test.name, " ", "-")),
				Actor: "operator", FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
				Migration: store.V01ToV02Migration{
					TimestampPolicy:   store.LegacyTimestampPreserve,
					TimestampConflict: test.policy,
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

			// Assert.
			if previewErr != nil {
				t.Fatalf("Preview() error = %v; blockers = %#v", previewErr, preview.Blockers)
			}
			updated, err := preview.Staged.ReadFile(context.Background(), "alpha.md")
			if err != nil {
				t.Fatal(err)
			}
			wantGenerated := []byte("generated:\n  by: process:existing\n  at: " + test.wantAt + "\n")
			if !bytes.Contains(updated, wantGenerated) ||
				!bytes.Contains(updated, []byte("timestamp: "+test.legacyAt+"\n")) {
				t.Fatalf("explicit %s policy produced:\n%s", test.policy, updated)
			}
			replay, replayErr := planner.Preview(
				context.Background(),
				preview.Staged,
				testMigrationResolution(t, preview.Staged),
				request,
			)
			if replayErr != nil {
				t.Fatalf("identical replay error = %v; blockers = %#v", replayErr, replay.Blockers)
			}
			if len(replay.Preview.Writes) != 0 ||
				replay.Preview.BaseRevision != replay.Preview.ResultRevision {
				t.Fatalf("identical replay is not a noop: %#v", replay.Preview)
			}
		})
	}
}

func TestMigrationPlannerPreview_ExplicitGeneratedAtBindsEffectiveTransitionInstant(t *testing.T) {
	for _, test := range []struct {
		name                string
		document            string
		explicitAt          string
		timestampPolicy     store.LegacyTimestampPolicy
		timestampConflict   store.TimestampConflictPolicy
		wantGeneratedAt     string
		wantLegacyTimestamp bool
		wantCode            string
		wantLocation        string
	}{
		{
			name: "existing flow generated CRLF equivalent instant",
			document: "---\r\ntype: Knowledge\r\n" +
				"generated: {by: process:existing, at: 2026-06-25T16:00:00+07:00}\r\n---\r\n",
			explicitAt:          "2026-06-25T09:00:00.000Z",
			timestampPolicy:     store.LegacyTimestampPreserve,
			timestampConflict:   store.TimestampConflictReject,
			wantGeneratedAt:     "2026-06-25T16:00:00+07:00",
			wantLegacyTimestamp: false,
		},
		{
			name: "existing block generated LF different instant",
			document: "---\ntype: Knowledge\ngenerated:\n" +
				"  by: process:existing\n  at: 2026-06-25T09:00:00Z\n---\n",
			explicitAt:        "2026-06-26T09:00:00Z",
			timestampPolicy:   store.LegacyTimestampPreserve,
			timestampConflict: store.TimestampConflictReject,
			wantCode:          "explicit_generated_at_mismatch",
			wantLocation:      "2026-06-25T09:00:00Z",
		},
		{
			name: "timestamp CRLF preserve equivalent instant",
			document: "---\r\ntype: Knowledge\r\n" +
				"timestamp: 2026-06-25T16:00:00+07:00\r\n---\r\n",
			explicitAt:          "2026-06-25T09:00:00Z",
			timestampPolicy:     store.LegacyTimestampPreserve,
			timestampConflict:   store.TimestampConflictReject,
			wantGeneratedAt:     "2026-06-25T16:00:00+07:00",
			wantLegacyTimestamp: true,
		},
		{
			name:              "timestamp LF preserve different instant",
			document:          "---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n---\n",
			explicitAt:        "2026-06-26T09:00:00Z",
			timestampPolicy:   store.LegacyTimestampPreserve,
			timestampConflict: store.TimestampConflictReject,
			wantCode:          "explicit_generated_at_mismatch",
			wantLocation:      "2026-06-25T09:00:00Z",
		},
		{
			name:                "timestamp LF remove equivalent instant",
			document:            "---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n---\n",
			explicitAt:          "2026-06-25T16:00:00+07:00",
			timestampPolicy:     store.LegacyTimestampRemoveAfterCopy,
			timestampConflict:   store.TimestampConflictReject,
			wantGeneratedAt:     "2026-06-25T09:00:00Z",
			wantLegacyTimestamp: false,
		},
		{
			name: "timestamp CRLF remove different instant",
			document: "---\r\ntype: Knowledge\r\n" +
				"timestamp: 2026-06-25T09:00:00Z\r\n---\r\n",
			explicitAt:        "2026-06-26T09:00:00Z",
			timestampPolicy:   store.LegacyTimestampRemoveAfterCopy,
			timestampConflict: store.TimestampConflictReject,
			wantCode:          "explicit_generated_at_mismatch",
			wantLocation:      "2026-06-25T09:00:00Z",
		},
		{
			name: "flow conflict use generated binds generated instant",
			document: "---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n" +
				"generated: {by: process:existing, at: 2026-06-26T09:00:00Z}\n---\n",
			explicitAt:          "2026-06-26T16:00:00+07:00",
			timestampPolicy:     store.LegacyTimestampPreserve,
			timestampConflict:   store.TimestampConflictUseGenerated,
			wantGeneratedAt:     "2026-06-26T09:00:00Z",
			wantLegacyTimestamp: true,
		},
		{
			name: "flow conflict use generated rejects timestamp instant",
			document: "---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n" +
				"generated: {by: process:existing, at: 2026-06-26T09:00:00Z}\n---\n",
			explicitAt:        "2026-06-25T09:00:00Z",
			timestampPolicy:   store.LegacyTimestampPreserve,
			timestampConflict: store.TimestampConflictUseGenerated,
			wantCode:          "explicit_generated_at_mismatch",
			wantLocation:      "2026-06-26T09:00:00Z",
		},
		{
			name: "block conflict use timestamp binds timestamp instant",
			document: "---\r\ntype: Knowledge\r\ntimestamp: 2026-06-25T09:00:00Z\r\n" +
				"generated:\r\n  by: process:existing\r\n  at: 2026-06-26T09:00:00Z\r\n---\r\n",
			explicitAt:          "2026-06-25T16:00:00+07:00",
			timestampPolicy:     store.LegacyTimestampPreserve,
			timestampConflict:   store.TimestampConflictUseTimestamp,
			wantGeneratedAt:     "2026-06-25T09:00:00Z",
			wantLegacyTimestamp: true,
		},
		{
			name: "block conflict use timestamp rejects generated instant",
			document: "---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n" +
				"generated:\n  by: process:existing\n  at: 2026-06-26T09:00:00Z\n---\n",
			explicitAt:        "2026-06-26T09:00:00Z",
			timestampPolicy:   store.LegacyTimestampPreserve,
			timestampConflict: store.TimestampConflictUseTimestamp,
			wantCode:          "explicit_generated_at_mismatch",
			wantLocation:      "2026-06-25T09:00:00Z",
		},
		{
			name: "conflict reject remains the primary file ambiguity",
			document: "---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n" +
				"generated:\n  by: process:existing\n  at: 2026-06-26T09:00:00Z\n---\n",
			explicitAt:        "2026-06-26T09:00:00Z",
			timestampPolicy:   store.LegacyTimestampPreserve,
			timestampConflict: store.TimestampConflictReject,
			wantCode:          "timestamp_generated_conflict",
			wantLocation:      "2026-06-25T09:00:00Z",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			source := memorySource{
				"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [Alpha](alpha.md)\n"),
				"alpha.md": []byte(test.document),
			}
			before := cloneMemorySource(source)
			request := MigrationRequest{
				ID:    "explicit-generated-at-" + store.ChangeSetID(strings.ReplaceAll(test.name, " ", "-")),
				Actor: "operator", FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
				Migration: store.V01ToV02Migration{
					GeneratedBy: "process:migration",
					GeneratedAt: []store.MigrationGeneratedAt{{
						Path: "alpha.md",
						At:   test.explicitAt,
					}},
					TimestampPolicy:   test.timestampPolicy,
					TimestampConflict: test.timestampConflict,
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

			// Assert.
			if !equalMemorySource(source, before) {
				t.Fatal("Preview mutated source bytes")
			}
			if test.wantCode != "" {
				if previewErr == nil {
					t.Fatalf("Preview() = %#v, want blocker %q", preview, test.wantCode)
				}
				if !errors.Is(previewErr, ErrAmbiguousPresentation) {
					t.Fatalf("Preview() error = %v, want ErrAmbiguousPresentation", previewErr)
				}
				if preview.Staged != nil ||
					len(preview.Preview.Writes) != 0 ||
					preview.Preview.BaseRevision != preview.Preview.ResultRevision {
					t.Fatalf("blocked preview exposed a stage: %#v", preview)
				}
				if len(preview.Blockers) != 1 ||
					preview.Blockers[0].Code != test.wantCode ||
					preview.Blockers[0].Path != "alpha.md" {
					t.Fatalf("Blockers = %#v, want %q on alpha.md", preview.Blockers, test.wantCode)
				}
				location := preview.Blockers[0].Location
				if location.Start < 0 ||
					location.End <= location.Start ||
					location.End > len(source["alpha.md"]) ||
					!bytes.Contains(source["alpha.md"][location.Start:location.End], []byte(test.wantLocation)) {
					t.Fatalf("blocker location = %#v over %q, want owner containing %q",
						location,
						source["alpha.md"],
						test.wantLocation,
					)
				}
				return
			}
			if previewErr != nil {
				t.Fatalf("Preview() error = %v; blockers = %#v", previewErr, preview.Blockers)
			}
			updated, err := preview.Staged.ReadFile(context.Background(), "alpha.md")
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(updated, []byte("at: "+test.wantGeneratedAt)) {
				t.Fatalf("generated.at does not preserve the effective instant owner:\n%s", updated)
			}
			if bytes.Contains(updated, []byte("timestamp:")) != test.wantLegacyTimestamp {
				t.Fatalf("timestamp policy %q produced:\n%s", test.timestampPolicy, updated)
			}
			if !bytes.Contains(updated, []byte("by: process:existing")) &&
				bytes.Contains(source["alpha.md"], []byte("by: process:existing")) {
				t.Fatalf("pre-existing generated.by was not preserved:\n%s", updated)
			}
			replay, replayErr := planner.Preview(
				context.Background(),
				preview.Staged,
				testMigrationResolution(t, preview.Staged),
				request,
			)
			if replayErr != nil {
				t.Fatalf("identical replay error = %v; blockers = %#v", replayErr, replay.Blockers)
			}
			if len(replay.Preview.Writes) != 0 ||
				replay.Preview.BaseRevision != replay.Preview.ResultRevision {
				t.Fatalf("identical replay is not a noop: %#v", replay.Preview)
			}
		})
	}
}

func TestMigrationPlannerPreview_InlineComputationRequiresExactOwnedBoundary(t *testing.T) {
	// Arrange.
	source := memorySource{
		"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Computations\n\n- [Query](query.md)\n"),
		"query.md": []byte("---\ntype: Knowledge\n---\n\n# Computation\n\n```sql\nSELECT 1;\n```\n"),
	}
	request := MigrationRequest{
		ID:          "migration-computation",
		Actor:       "operator",
		FromVersion: MigrationVersionV01,
		ToVersion:   MigrationVersionV02,
		Migration: store.V01ToV02Migration{
			GeneratedBy:       "process:migration",
			GeneratedAt:       []store.MigrationGeneratedAt{{Path: "query.md", At: "2026-06-25T09:00:00Z"}},
			TimestampPolicy:   store.LegacyTimestampPreserve,
			TimestampConflict: store.TimestampConflictReject,
			Computations: []store.ComputationMigration{{
				Path: "query.md",
				Contract: store.AttestedComputationContract{
					Mode:              store.AttestedComputationModeInline,
					Runtime:           "sql",
					InlineComputation: "SELECT 1;\n",
					InlineLanguage:    "sql",
					Executor:          store.ExecutorContract{Resource: "executors/sql.md", Receipt: []string{"rows"}},
					Attester:          store.AttesterContract{Resource: "attesters/sql.md"},
				},
			}},
		},
	}

	// Act.
	preview, err := NewMigrationPlanner().Preview(context.Background(), source, testMigrationResolution(t, source), request)

	// Assert.
	if err != nil {
		t.Fatalf("Preview() error = %v; diagnostics = %#v", err, preview.Preview.Diagnostics)
	}
	query, err := preview.Staged.ReadFile(context.Background(), "query.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range [][]byte{
		[]byte("type: Attested Computation\n"),
		[]byte("runtime: sql\n"),
		[]byte("executor:\n"),
		[]byte("attester:\n"),
		[]byte("```sql\nSELECT 1;\n```\n"),
	} {
		if !bytes.Contains(query, want) {
			t.Fatalf("staged query.md does not contain %q:\n%s", want, query)
		}
	}
}

func TestMigrationPlannerPreview_EmptyInlineComputationTransitionsAndReplays(t *testing.T) {
	// Arrange.
	source := memorySource{
		"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Computations\n\n- [Query](query.md)\n"),
		"query.md": []byte("---\ntype: Knowledge\n---\n\n# Computation\n\n```sql\n```\n"),
	}
	request := MigrationRequest{
		ID:          "migration-empty-inline-computation",
		Actor:       "operator",
		FromVersion: MigrationVersionV01,
		ToVersion:   MigrationVersionV02,
		Migration: store.V01ToV02Migration{
			GeneratedBy:       "process:migration",
			GeneratedAt:       []store.MigrationGeneratedAt{{Path: "query.md", At: "2026-06-25T09:00:00Z"}},
			TimestampPolicy:   store.LegacyTimestampPreserve,
			TimestampConflict: store.TimestampConflictReject,
			Computations: []store.ComputationMigration{{
				Path: "query.md",
				Contract: store.AttestedComputationContract{
					Mode:           store.AttestedComputationModeInline,
					Runtime:        "sql",
					InlineLanguage: "sql",
					Executor:       store.ExecutorContract{Resource: "executors/sql.md", Receipt: []string{"rows"}},
					Attester:       store.AttesterContract{Resource: "attesters/sql.md"},
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
	if previewErr == nil {
		query, previewErr = preview.Staged.ReadFile(context.Background(), "query.md")
	}
	var replay MigrationPreview
	var replayErr error
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
			"empty-inline preview/replay errors = %v/%v; blockers = %#v/%#v",
			previewErr,
			replayErr,
			preview.Blockers,
			replay.Blockers,
		)
	}
	if !bytes.Contains(query, []byte("type: Attested Computation\n")) ||
		!bytes.Contains(query, []byte("```sql\n```\n")) ||
		bytes.Contains(query, []byte("\ncomputation:")) {
		t.Fatalf("empty-inline migration transition mismatch:\n%s", query)
	}
	if len(replay.Preview.Writes) != 0 ||
		replay.Preview.BaseRevision != replay.Preview.ResultRevision {
		t.Fatalf("empty-inline migration replay is not a noop: %#v", replay.Preview)
	}
}

func TestMigrationPlannerPreview_ZeroByteFileAssetIsExplicitInProofAndTargetReplay(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	source := memorySource{
		"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Computations\n\n- [Query](query.md)\n"),
		"query.md": []byte("---\ntype: Knowledge\n---\n\nQuery.\n"),
	}
	request := MigrationRequest{
		ID:          "migration-zero-byte-file-asset",
		Actor:       "operator",
		FromVersion: MigrationVersionV01,
		ToVersion:   MigrationVersionV02,
		Migration: store.V01ToV02Migration{
			GeneratedBy:       "process:migration",
			TimestampPolicy:   store.LegacyTimestampPreserve,
			TimestampConflict: store.TimestampConflictReject,
			Computations: []store.ComputationMigration{{
				Path: "query.md",
				Contract: store.AttestedComputationContract{
					Mode:            store.AttestedComputationModeFile,
					Runtime:         "sql",
					ComputationPath: "references/query.sql",
					Executor: store.ExecutorContract{
						Resource: "executors/sql.md",
						Receipt:  []string{"rows"},
					},
					Attester: store.AttesterContract{Resource: "attesters/sql.md"},
				},
				Asset: &store.MigrationAsset{
					Path:    "references/query.sql",
					Content: []byte{},
				},
			}},
		},
	}
	planner := NewMigrationPlanner()

	// Act.
	preview, previewErr := planner.Preview(
		ctx,
		source,
		testMigrationResolution(t, source),
		request,
	)
	var asset []byte
	var assetErr error
	var paths []string
	var pathsErr error
	if previewErr == nil {
		asset, assetErr = preview.Staged.ReadFile(ctx, "references/query.sql")
		paths, pathsErr = preview.Staged.Paths(ctx)
	}
	var replay MigrationPreview
	var replayErr error
	if previewErr == nil && assetErr == nil && pathsErr == nil {
		replay, replayErr = planner.Preview(
			ctx,
			preview.Staged,
			testMigrationResolution(t, preview.Staged),
			request,
		)
	}

	// Assert.
	if previewErr != nil || assetErr != nil || pathsErr != nil || replayErr != nil {
		t.Fatalf(
			"zero-byte preview/read/replay errors = %v/%v/%v/%v; blockers = %#v/%#v",
			previewErr,
			assetErr,
			pathsErr,
			replayErr,
			preview.Blockers,
			replay.Blockers,
		)
	}
	if len(asset) != 0 {
		t.Fatalf("staged zero-byte asset = %q", asset)
	}
	present := false
	for _, path := range paths {
		if path == "references/query.sql" {
			present = true
			break
		}
	}
	if !present {
		t.Fatalf("zero-byte asset is absent from staged paths: %#v", paths)
	}
	const emptyDigest = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	proofBound := false
	for _, write := range preview.Proof.Writes {
		if write.Path == "references/query.sql" {
			proofBound = write.Digest == emptyDigest
			break
		}
	}
	if !proofBound || preview.PlanDigest == "" {
		t.Fatalf("zero-byte asset is not bound by proof/digest: proof=%#v digest=%q", preview.Proof, preview.PlanDigest)
	}
	if len(replay.Preview.Writes) != 0 ||
		replay.Preview.BaseRevision != replay.Preview.ResultRevision {
		t.Fatalf("zero-byte target replay is not a noop: %#v", replay.Preview)
	}
}

func TestMigrationPlannerPreview_ComputationContractPreservesExtensionsAndReplays(t *testing.T) {
	for _, test := range []struct {
		name        string
		document    string
		computation store.ComputationMigration
		preserved   [][]byte
		removed     [][]byte
	}{
		{
			name: "block inline parameters retain comments while reordering",
			document: "---\n" +
				"type: Knowledge\n" +
				"timestamp: 2026-06-25T09:00:00Z\n" +
				"runtime: legacy\n" +
				"parameters:\n" +
				"  - {name: obsolete, type: string, required: true}\n" +
				"  - name: keep\n" +
				"    type: string\n" +
				"    required: false\n" +
				"    x-param: keep # parameter-comment\n" +
				"  - {name: second, type: integer, required: false}\n" +
				"executor:\n" +
				"  resource: legacy-executor.md\n" +
				"  receipt: [legacy]\n" +
				"  x-executor: keep # executor-comment\n" +
				"attester:\n" +
				"  resource: legacy-attester.md\n" +
				"  x-attester: keep # attester-comment\n" +
				"x-root: keep # root-comment\n" +
				"---\n\n# Computation\n\n```sql\nSELECT 1;\n```\n",
			computation: store.ComputationMigration{
				Path: "query.md",
				Contract: store.AttestedComputationContract{
					Mode:    store.AttestedComputationModeInline,
					Runtime: "sql",
					Parameters: []store.ComputationParameter{
						{Name: "second", Type: "integer", Required: true},
						{Name: "keep", Type: "string", Required: true},
					},
					InlineComputation: "SELECT 1;\n",
					InlineLanguage:    "sql",
					Executor: store.ExecutorContract{
						Resource: "executors/sql.md",
						Receipt:  []string{"rows"},
					},
					Attester: store.AttesterContract{Resource: "attesters/sql.md"},
				},
			},
			preserved: [][]byte{
				[]byte("x-param: keep # parameter-comment"),
				[]byte("x-executor: keep # executor-comment"),
				[]byte("x-attester: keep # attester-comment"),
				[]byte("x-root: keep # root-comment"),
				[]byte("```sql\nSELECT 1;\n```\n"),
			},
			removed: [][]byte{
				[]byte("name: obsolete"),
				[]byte("legacy-executor.md"),
				[]byte("legacy-attester.md"),
			},
		},
		{
			name: "flow file contract retains extensions and creates exact asset",
			document: "---\r\n" +
				"type: Knowledge\r\n" +
				"timestamp: 2026-06-25T16:00:00+07:00\r\n" +
				"runtime: legacy\r\n" +
				"parameters: [{name: keep, type: string, required: false, x-param: keep}, {name: obsolete, type: integer, required: true}]\r\n" +
				"computation: legacy.sql\r\n" +
				"executor: {resource: legacy-executor.md, receipt: [legacy], x-executor: keep}\r\n" +
				"attester: {resource: legacy-attester.md, x-attester: keep}\r\n" +
				"x-root: keep\r\n" +
				"---\r\n\r\nBody.\r\n",
			computation: store.ComputationMigration{
				Path: "query.md",
				Contract: store.AttestedComputationContract{
					Mode:    store.AttestedComputationModeFile,
					Runtime: "sql",
					Parameters: []store.ComputationParameter{
						{Name: "keep", Type: "string", Required: true},
					},
					ComputationPath: "references/query.sql",
					Executor: store.ExecutorContract{
						Resource: "executors/sql.md",
						Receipt:  []string{"rows"},
					},
					Attester: store.AttesterContract{Resource: "attesters/sql.md"},
				},
				Asset: &store.MigrationAsset{
					Path:    "references/query.sql",
					Content: []byte("SELECT 2;\n"),
				},
			},
			preserved: [][]byte{
				[]byte("x-param: keep"),
				[]byte("x-executor: keep"),
				[]byte("x-attester: keep"),
				[]byte("x-root: keep"),
				[]byte("Body.\r\n"),
			},
			removed: [][]byte{
				[]byte("name: obsolete"),
				[]byte("legacy.sql"),
				[]byte("legacy-executor.md"),
				[]byte("legacy-attester.md"),
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			source := memorySource{
				"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Computations\n\n- [Query](query.md)\n"),
				"query.md": []byte(test.document),
			}
			before := cloneMemorySource(source)
			request := MigrationRequest{
				ID:    "computation-preservation-" + store.ChangeSetID(strings.ReplaceAll(test.name, " ", "-")),
				Actor: "operator", FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
				Migration: store.V01ToV02Migration{
					GeneratedBy:       "process:migration",
					TimestampPolicy:   store.LegacyTimestampRemoveAfterCopy,
					TimestampConflict: store.TimestampConflictReject,
					Computations:      []store.ComputationMigration{test.computation},
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

			// Assert.
			if previewErr != nil {
				t.Fatalf("Preview() error = %v; blockers = %#v", previewErr, preview.Blockers)
			}
			if !equalMemorySource(source, before) {
				t.Fatal("Preview mutated source bytes")
			}
			updated, err := preview.Staged.ReadFile(context.Background(), "query.md")
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range test.preserved {
				if !bytes.Contains(updated, want) {
					t.Fatalf("retained extension/trivia %q was lost:\n%s", want, updated)
				}
			}
			for _, obsolete := range test.removed {
				if bytes.Contains(updated, obsolete) {
					t.Fatalf("omitted known state %q remains:\n%s", obsolete, updated)
				}
			}
			if !bytes.Contains(updated, []byte("type: Attested Computation")) ||
				!bytes.Contains(updated, []byte("required: true")) {
				t.Fatalf("known computation contract was not reconciled:\n%s", updated)
			}
			secondAt := bytes.Index(updated, []byte("name: second"))
			keepAt := bytes.Index(updated, []byte("name: keep"))
			if secondAt >= 0 && (keepAt < 0 || secondAt >= keepAt) {
				t.Fatalf("desired parameter order was not applied:\n%s", updated)
			}
			if test.computation.Asset != nil {
				asset, err := preview.Staged.ReadFile(context.Background(), test.computation.Asset.Path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(asset, test.computation.Asset.Content) {
					t.Fatalf("staged asset = %q, want %q", asset, test.computation.Asset.Content)
				}
			}
			replay, replayErr := planner.Preview(
				context.Background(),
				preview.Staged,
				testMigrationResolution(t, preview.Staged),
				request,
			)
			if replayErr != nil {
				t.Fatalf("identical replay error = %v; blockers = %#v", replayErr, replay.Blockers)
			}
			if len(replay.Preview.Writes) != 0 ||
				replay.Preview.BaseRevision != replay.Preview.ResultRevision {
				t.Fatalf("identical computation replay is not a noop: %#v", replay.Preview)
			}
		})
	}
}

func TestMigrationPlannerPreview_BindsNestedComputationAssetByResolvedPath(t *testing.T) {
	sourceFor := func() memorySource {
		return memorySource{
			"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Computations\n\n- [Query](nested/query.md)\n"),
			"nested/query.md": []byte(
				"---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n---\n\nBody.\n",
			),
		}
	}
	requestFor := func(id store.ChangeSetID, computationPath, assetPath string) MigrationRequest {
		return MigrationRequest{
			ID: id, Actor: "operator",
			FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
			Migration: store.V01ToV02Migration{
				GeneratedBy:       "process:migration",
				TimestampPolicy:   store.LegacyTimestampRemoveAfterCopy,
				TimestampConflict: store.TimestampConflictReject,
				Computations: []store.ComputationMigration{{
					Path: "nested/query.md",
					Contract: store.AttestedComputationContract{
						Mode:            store.AttestedComputationModeFile,
						Runtime:         "sql",
						ComputationPath: computationPath,
						Executor: store.ExecutorContract{
							Resource: "executors/sql.md",
							Receipt:  []string{"rows"},
						},
						Attester: store.AttesterContract{Resource: "attesters/sql.md"},
					},
					Asset: &store.MigrationAsset{
						Path:    assetPath,
						Content: []byte("SELECT 1;\n"),
					},
				}},
			},
		}
	}
	planner := NewMigrationPlanner()

	for _, test := range []struct {
		name            string
		computationPath string
		wantKind        bundle.PathValueKind
		wantSuffix      string
	}{
		{
			name:            "document relative",
			computationPath: "../references/query.sql",
			wantKind:        bundle.PathValueRelative,
		},
		{
			name:            "bundle relative",
			computationPath: "/references/query.sql",
			wantKind:        bundle.PathValueBundleRelative,
		},
		{
			name:            "normalized path with query and fragment",
			computationPath: "../references/../references/query.sql?mode=preview#fragment",
			wantKind:        bundle.PathValueRelative,
			wantSuffix:      "?mode=preview#fragment",
		},
	} {
		t.Run("valid/"+test.name, func(t *testing.T) {
			// Arrange.
			source := sourceFor()
			before := cloneMemorySource(source)
			request := requestFor(
				"nested-computation-"+store.ChangeSetID(strings.ReplaceAll(test.name, " ", "-")),
				test.computationPath,
				"references/query.sql",
			)

			// Act.
			preview, previewErr := planner.Preview(
				context.Background(),
				source,
				testMigrationResolution(t, source),
				request,
			)

			// Assert.
			if previewErr != nil {
				t.Fatalf("Preview() error = %v; blockers = %#v", previewErr, preview.Blockers)
			}
			if preview.Staged == nil || preview.PlanDigest == "" {
				t.Fatalf("Preview() did not return a staged proven plan: %#v", preview)
			}
			if !equalMemorySource(source, before) {
				t.Fatal("Preview mutated source bytes")
			}
			document, err := preview.Staged.ReadFile(context.Background(), "nested/query.md")
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(document, []byte(test.computationPath)) {
				t.Fatalf("staged document did not preserve computation path %q:\n%s", test.computationPath, document)
			}
			asset, err := preview.Staged.ReadFile(context.Background(), "references/query.sql")
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(asset, []byte("SELECT 1;\n")) {
				t.Fatalf("staged asset = %q", asset)
			}
			loaded, err := bundle.Load(context.Background(), preview.Staged)
			if err != nil {
				t.Fatal(err)
			}
			resolved, ok := loaded.ResolvePathValueFor(
				"nested/query.md",
				test.computationPath,
				bundle.PathFieldComputation,
			)
			if !ok ||
				resolved.Kind != test.wantKind ||
				resolved.Path != "references/query.sql" ||
				resolved.Suffix != test.wantSuffix ||
				!resolved.Exists {
				t.Fatalf("resolved computation = (%#v, %v)", resolved, ok)
			}

			replay, replayErr := planner.Preview(
				context.Background(),
				preview.Staged,
				testMigrationResolution(t, preview.Staged),
				request,
			)
			if replayErr != nil {
				t.Fatalf("target replay error = %v; blockers = %#v", replayErr, replay.Blockers)
			}
			if len(replay.Preview.Writes) != 0 ||
				replay.Preview.BaseRevision != replay.Preview.ResultRevision {
				t.Fatalf("target replay is not a noop: %#v", replay.Preview)
			}
		})
	}

	for _, test := range []struct {
		name            string
		computationPath string
		assetPath       string
	}{
		{
			name:            "raw equal resolves below nested document",
			computationPath: "references/query.sql",
			assetPath:       "references/query.sql",
		},
		{
			name:            "different canonical asset",
			computationPath: "../references/query.sql",
			assetPath:       "references/other.sql",
		},
		{
			name:            "external URL",
			computationPath: "https://example.test/query.sql",
			assetPath:       "references/query.sql",
		},
		{
			name:            "bundle escape",
			computationPath: "../../references/query.sql",
			assetPath:       "references/query.sql",
		},
		{
			name:            "windows path",
			computationPath: `C:\work\query.sql`,
			assetPath:       "references/query.sql",
		},
		{
			name:            "suffix only",
			computationPath: "#fragment",
			assetPath:       "references/query.sql",
		},
	} {
		t.Run("invalid/"+test.name, func(t *testing.T) {
			// Arrange.
			source := sourceFor()
			before := cloneMemorySource(source)
			request := requestFor(
				"invalid-nested-computation-"+store.ChangeSetID(strings.ReplaceAll(test.name, " ", "-")),
				test.computationPath,
				test.assetPath,
			)

			// Act.
			preview, previewErr := planner.Preview(
				context.Background(),
				source,
				testMigrationResolution(t, source),
				request,
			)

			// Assert.
			if !errors.Is(previewErr, ErrUnsupportedPresentation) {
				t.Fatalf("Preview() error = %v, want ErrUnsupportedPresentation", previewErr)
			}
			if len(preview.Blockers) != 1 ||
				preview.Blockers[0].Code != "migration_computation_asset_path_mismatch" ||
				preview.Blockers[0].Path != "nested/query.md" ||
				preview.Blockers[0].Location != (SourceSpan{}) {
				t.Fatalf("Blockers = %#v", preview.Blockers)
			}
			if preview.Staged != nil ||
				len(preview.Preview.Writes) != 0 ||
				preview.Preview.BaseRevision != preview.Preview.ResultRevision ||
				preview.PlanDigest != "" ||
				!reflect.DeepEqual(preview.Proof, MigrationPlanProof{}) {
				t.Fatalf("blocked Preview exposed authorization or stage: %#v", preview)
			}
			if !equalMemorySource(source, before) {
				t.Fatal("blocked Preview mutated source bytes")
			}
		})
	}

	t.Run("target replay validates the same binding before proof", func(t *testing.T) {
		// Arrange.
		source := sourceFor()
		valid := requestFor(
			"target-replay-computation-binding",
			"../references/query.sql",
			"references/query.sql",
		)
		migrated, err := planner.Preview(
			context.Background(),
			source,
			testMigrationResolution(t, source),
			valid,
		)
		if err != nil {
			t.Fatal(err)
		}
		invalid := valid
		invalid.Migration.Computations[0].Contract.ComputationPath = "references/query.sql"

		// Act.
		replay, replayErr := planner.Preview(
			context.Background(),
			migrated.Staged,
			testMigrationResolution(t, migrated.Staged),
			invalid,
		)

		// Assert.
		if !errors.Is(replayErr, ErrUnsupportedPresentation) ||
			len(replay.Blockers) != 1 ||
			replay.Blockers[0].Code != "migration_computation_asset_path_mismatch" ||
			replay.Staged != nil ||
			len(replay.Preview.Writes) != 0 ||
			replay.PlanDigest != "" ||
			!reflect.DeepEqual(replay.Proof, MigrationPlanProof{}) {
			t.Fatalf("target replay = (%#v, %v)", replay, replayErr)
		}
	})

	t.Run("direct planner bypass rejects before staging", func(t *testing.T) {
		// Arrange.
		source := sourceFor()
		request := requestFor(
			"direct-computation-binding",
			"references/query.sql",
			"references/query.sql",
		)
		operation, err := store.NewMigrateV01ToV02(request.Migration)
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.WithValue(
			context.Background(),
			migrationResolutionContextKey{},
			testMigrationResolution(t, source),
		)

		// Act.
		preview, planErr := NewPlanner(nil).Plan(ctx, source, change(t, source, operation))

		// Assert.
		var invalid *store.InvalidChangeSet
		if !errors.As(planErr, &invalid) ||
			invalid.Code != "migration_computation_asset_path_mismatch" ||
			preview.Staged != nil ||
			len(preview.Preview.Writes) != 0 {
			t.Fatalf("Plan() = (%#v, %v)", preview, planErr)
		}
	})
}

func TestMigrationPlannerPreview_ComputationLateBoundaryFailureHasZeroStage(t *testing.T) {
	// Arrange.
	source := memorySource{
		"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Computations\n\n- [Query](query.md)\n"),
		"query.md": []byte("---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n" +
			"x-root: keep # root-comment\n---\n\n# Computation\n\n```sql\nSELECT legacy;\n```\n"),
	}
	before := cloneMemorySource(source)
	request := MigrationRequest{
		ID:    "computation-late-boundary-failure",
		Actor: "operator", FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
		Migration: store.V01ToV02Migration{
			GeneratedBy:       "process:migration",
			TimestampPolicy:   store.LegacyTimestampRemoveAfterCopy,
			TimestampConflict: store.TimestampConflictReject,
			Computations: []store.ComputationMigration{{
				Path: "query.md",
				Contract: store.AttestedComputationContract{
					Mode:            store.AttestedComputationModeFile,
					Runtime:         "sql",
					ComputationPath: "references/query.sql",
					Executor: store.ExecutorContract{
						Resource: "executors/sql.md",
						Receipt:  []string{"rows"},
					},
					Attester: store.AttesterContract{Resource: "attesters/sql.md"},
				},
				Asset: &store.MigrationAsset{
					Path:    "references/query.sql",
					Content: []byte("SELECT desired;\n"),
				},
			}},
		},
	}

	// Act.
	preview, previewErr := NewMigrationPlanner().Preview(
		context.Background(),
		source,
		testMigrationResolution(t, source),
		request,
	)

	// Assert.
	if !errors.Is(previewErr, ErrUnsupportedPresentation) {
		t.Fatalf("Preview() error = %v, want ErrUnsupportedPresentation", previewErr)
	}
	if preview.Staged != nil ||
		len(preview.Preview.Writes) != 0 ||
		preview.Preview.BaseRevision != preview.Preview.ResultRevision {
		t.Fatalf("late computation failure exposed a stage: %#v", preview)
	}
	if len(preview.Blockers) != 1 ||
		preview.Blockers[0].Code != "sanctioned_computation_mismatch" ||
		preview.Blockers[0].Path != "query.md" {
		t.Fatalf("Blockers = %#v, want sanctioned_computation_mismatch on query.md", preview.Blockers)
	}
	if !equalMemorySource(source, before) {
		t.Fatal("blocked Preview mutated source bytes")
	}
	if _, exists := source["references/query.sql"]; exists {
		t.Fatal("blocked Preview published the computation asset")
	}
}

func TestMigrationPlannerPreview_LegacyCitationsWithoutMappingFailClosed(t *testing.T) {
	// Arrange.
	source := memorySource{
		"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [Alpha](alpha.md)\n"),
		"alpha.md": []byte("---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n---\n\nClaim [1].\n\n# Citations\n\n[1] https://example.test/spec\n"),
	}
	request := MigrationRequest{
		ID:          "migration-missing-citations",
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
	preview, err := NewMigrationPlanner().Preview(context.Background(), source, testMigrationResolution(t, source), request)

	// Assert.
	if !errors.Is(err, ErrUnsupportedPresentation) {
		t.Fatalf("Preview() error = %v, want ErrUnsupportedPresentation", err)
	}
	if preview.Staged != nil || len(preview.Preview.Writes) != 0 {
		t.Fatalf("blocked preview exposed stage: %#v", preview)
	}
	if len(preview.Blockers) != 1 ||
		preview.Blockers[0].Code != "missing_explicit_citation_mapping" ||
		preview.Blockers[0].Path != "alpha.md" {
		t.Fatalf("Blockers = %#v", preview.Blockers)
	}
	if len(preview.ManualActions) != 1 || preview.ManualActions[0].Code != "provide_citation_mapping" {
		t.Fatalf("ManualActions = %#v", preview.ManualActions)
	}
}

func TestMigrationPlannerPreview_TypeOnlyLegacyConceptDoesNotInventGeneratedMetadata(t *testing.T) {
	// Arrange.
	source := memorySource{
		"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [Alpha](alpha.md)\n"),
		"alpha.md": []byte("---\ntype: Knowledge\n---\n\nNo timestamp.\n"),
	}
	request := MigrationRequest{
		ID:          "migration-missing-generated-at",
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
	preview, err := NewMigrationPlanner().Preview(context.Background(), source, testMigrationResolution(t, source), request)

	// Assert.
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if len(preview.Preview.Writes) != 1 || preview.Preview.Writes[0].Path != "index.md" {
		t.Fatalf("Preview writes = %#v, want version-only root write", preview.Preview.Writes)
	}
	alpha, readErr := preview.Staged.ReadFile(context.Background(), "alpha.md")
	if readErr != nil {
		t.Fatal(readErr)
	}
	if bytes.Contains(alpha, []byte("generated:")) {
		t.Fatalf("migration invented generated metadata:\n%s", alpha)
	}
}

func TestMigrationPlannerPreview_GeneratedMetadataIsCreatedOnlyFromLegacyEvidence(t *testing.T) {
	tests := []struct {
		name        string
		source      memorySource
		generatedBy string
		wantWrites  []string
		unchanged   string
	}{
		{
			name:       "rootless bundle with no concepts",
			source:     memorySource{},
			wantWrites: nil,
		},
		{
			name: "mixed timestamp and type-only concepts",
			source: memorySource{
				"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [A](a.md)\n- [B](b.md)\n"),
				"a.md":     []byte("---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n---\n"),
				"b.md":     []byte("---\ntype: Knowledge\n---\n"),
			},
			generatedBy: "process:migration",
			wantWrites:  []string{"a.md", "index.md"},
			unchanged:   "b.md",
		},
		{
			name: "existing generated needs no caller actor",
			source: memorySource{
				"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [A](a.md)\n"),
				"a.md":     []byte("---\ntype: Knowledge\ngenerated:\n  by: process:existing\n  at: 2026-06-25T09:00:00Z\n---\n"),
			},
			wantWrites: []string{"index.md"},
			unchanged:  "a.md",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			request := MigrationRequest{
				ID:    "anti-invention-" + store.ChangeSetID(strings.ReplaceAll(test.name, " ", "-")),
				Actor: "operator", FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
				Migration: store.V01ToV02Migration{
					GeneratedBy:       test.generatedBy,
					TimestampPolicy:   store.LegacyTimestampPreserve,
					TimestampConflict: store.TimestampConflictReject,
				},
			}

			// Act.
			preview, err := NewMigrationPlanner().Preview(context.Background(), test.source, testMigrationResolution(t, test.source), request)

			// Assert.
			if err != nil {
				t.Fatalf("Preview() error = %v", err)
			}
			gotWrites := make([]string, len(preview.Preview.Writes))
			for index, write := range preview.Preview.Writes {
				gotWrites[index] = write.Path
			}
			if strings.Join(gotWrites, ",") != strings.Join(test.wantWrites, ",") {
				t.Fatalf("writes = %v, want %v", gotWrites, test.wantWrites)
			}
			if test.unchanged != "" {
				got, readErr := preview.Staged.ReadFile(context.Background(), test.unchanged)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if !bytes.Equal(got, test.source[test.unchanged]) {
					t.Fatalf("%s changed:\n%s", test.unchanged, got)
				}
			}
		})
	}
}

func TestMigrationPlannerPreview_MissingGeneratedByReturnsDomainManualAction(t *testing.T) {
	// Arrange.
	source := memorySource{
		"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [Alpha](alpha.md)\n"),
		"alpha.md": []byte("---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n---\n\nAlpha.\n"),
	}
	request := MigrationRequest{
		ID:          "migration-missing-generated-by",
		Actor:       "tool:okf-cli",
		FromVersion: MigrationVersionV01,
		ToVersion:   MigrationVersionV02,
		Migration: store.V01ToV02Migration{
			TimestampPolicy:   store.LegacyTimestampPreserve,
			TimestampConflict: store.TimestampConflictReject,
		},
	}

	// Act.
	preview, err := NewMigrationPlanner().Preview(context.Background(), source, testMigrationResolution(t, source), request)

	// Assert.
	if !errors.Is(err, ErrUnsupportedPresentation) {
		t.Fatalf("Preview() error = %v, want ErrUnsupportedPresentation", err)
	}
	if preview.Staged != nil || len(preview.Preview.Writes) != 0 {
		t.Fatalf("blocked preview exposed stage: %#v", preview)
	}
	if len(preview.Blockers) != 1 ||
		preview.Blockers[0].Code != "missing_explicit_generated_by" ||
		preview.Blockers[0].Path != "alpha.md" {
		t.Fatalf("Blockers = %#v", preview.Blockers)
	}
	if len(preview.ManualActions) != 1 || preview.ManualActions[0].Code != "provide_generated_by" {
		t.Fatalf("ManualActions = %#v", preview.ManualActions)
	}
}

func TestMigrationPlannerPreview_AggregatesExplicitInputBlockersDeterministically(t *testing.T) {
	// Arrange.
	source := memorySource{
		"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [A](a.md)\n"),
		"a.md":     []byte("---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n---\n\nClaim [1].\n\n# Citations\n\n[1] https://example.test\n"),
	}
	request := MigrationRequest{
		ID: "aggregate-input-blockers", Actor: "operator",
		FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
		Migration: store.V01ToV02Migration{
			TimestampPolicy:   store.LegacyTimestampPreserve,
			TimestampConflict: store.TimestampConflictReject,
		},
	}

	// Act.
	preview, err := NewMigrationPlanner().Preview(context.Background(), source, testMigrationResolution(t, source), request)

	// Assert.
	if !errors.Is(err, ErrUnsupportedPresentation) {
		t.Fatalf("Preview() error = %v, want ErrUnsupportedPresentation", err)
	}
	wantBlockers := []string{"missing_explicit_citation_mapping", "missing_explicit_generated_by"}
	if len(preview.Blockers) != len(wantBlockers) {
		t.Fatalf("Blockers = %#v", preview.Blockers)
	}
	for index, want := range wantBlockers {
		if preview.Blockers[index].Path != "a.md" || preview.Blockers[index].Code != want {
			t.Fatalf("Blockers = %#v, want stable path/code order", preview.Blockers)
		}
	}
	wantActions := []string{"provide_citation_mapping", "provide_generated_by"}
	if len(preview.ManualActions) != len(wantActions) {
		t.Fatalf("ManualActions = %#v", preview.ManualActions)
	}
	for index, want := range wantActions {
		if preview.ManualActions[index].Path != "a.md" || preview.ManualActions[index].Code != want {
			t.Fatalf("ManualActions = %#v, want stable path/code order", preview.ManualActions)
		}
	}
	if preview.Staged != nil || len(preview.Preview.Writes) != 0 {
		t.Fatalf("blocked preview exposed stage: %#v", preview)
	}
}

func TestMigrationPlannerPreview_RejectsAbsentExplicitFileComputationDocument(t *testing.T) {
	// Arrange.
	source := memorySource{
		"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [Alpha](alpha.md)\n"),
		"alpha.md": []byte("---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n---\n\nAlpha.\n"),
	}
	sql := []byte("SELECT 1;\n")
	request := MigrationRequest{
		ID:          "migration-create-computation",
		Actor:       "operator",
		FromVersion: MigrationVersionV01,
		ToVersion:   MigrationVersionV02,
		Migration: store.V01ToV02Migration{
			GeneratedBy: "process:migration",
			GeneratedAt: []store.MigrationGeneratedAt{{
				Path: "query.md",
				At:   "2026-06-25T10:00:00Z",
			}},
			TimestampPolicy:   store.LegacyTimestampPreserve,
			TimestampConflict: store.TimestampConflictReject,
			Computations: []store.ComputationMigration{{
				Path: "query.md",
				Contract: store.AttestedComputationContract{
					Mode:            store.AttestedComputationModeFile,
					Runtime:         "sql",
					ComputationPath: "references/query.sql",
					Executor:        store.ExecutorContract{Resource: "executors/sql.md", Receipt: []string{"rows"}},
					Attester:        store.AttesterContract{Resource: "attesters/sql.md"},
				},
				Asset: &store.MigrationAsset{Path: "references/query.sql", Content: sql},
			}},
		},
	}

	// Act.
	preview, err := NewMigrationPlanner().Preview(context.Background(), source, testMigrationResolution(t, source), request)

	// Assert.
	if err == nil {
		t.Fatalf("Preview() = %#v, want missing document blocker", preview)
	}
	if len(preview.Blockers) != 1 ||
		preview.Blockers[0].Path != "query.md" ||
		preview.Blockers[0].Code != "migration_document_missing" ||
		preview.Blockers[0].Message != `bundle presentation migration_document_missing: unsupported presentation: migration document "query.md" does not exist` {
		t.Fatalf("Blockers = %#v", preview.Blockers)
	}
	if len(preview.ManualActions) != 0 {
		t.Fatalf("ManualActions = %#v, want none", preview.ManualActions)
	}
	if preview.Staged != nil || len(preview.Preview.Writes) != 0 {
		t.Fatalf("blocked preview exposed stage: %#v", preview)
	}
}

func TestStageTimestampMigration_EquivalentInstantsDoNotConflictAndPreserveSpelling(t *testing.T) {
	// Arrange.
	source := []byte("---\ntype: Knowledge\n" +
		"timestamp: 2026-06-25T16:00:00+07:00\n" +
		"generated:\n  by: process:existing\n  at: 2026-06-25T09:00:00Z\n---\n")
	presentation, err := parsePresentationContext(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	mutation := presentation.newYAMLMutationContext(presentation.ctx)
	request := store.V01ToV02Migration{
		GeneratedBy:       "process:migration",
		TimestampPolicy:   store.LegacyTimestampPreserve,
		TimestampConflict: store.TimestampConflictReject,
	}

	// Act.
	stageErr := stageTimestampMigration(mutation, request, "")
	updated, applyErr := mutation.apply()

	// Assert.
	if stageErr != nil || applyErr != nil {
		t.Fatalf("stage/apply errors = %v, %v", stageErr, applyErr)
	}
	if !bytes.Equal(updated, source) {
		t.Fatalf("equivalent instants changed raw spelling:\n%s", updated)
	}
}

func TestApplyComputationBoundary_FileModePreservesUnownedSiblings(t *testing.T) {
	// Arrange.
	source := []byte("---\ntype: Knowledge\n---\n\n# Computation\n\n" +
		"Narrative stays.\n\n<!-- comment stays -->\n\n" +
		"```sql\nSELECT 1;\n```\n\n<span>HTML stays</span>\n")
	migration := store.ComputationMigration{
		Path: "query.md",
		Contract: store.AttestedComputationContract{
			Mode:            store.AttestedComputationModeFile,
			ComputationPath: "computations/query.sql",
		},
		Asset: &store.MigrationAsset{
			Path:    "computations/query.sql",
			Content: []byte("SELECT 1;\n"),
		},
	}

	// Act.
	updated, err := applyComputationBoundary(context.Background(), source, migration)

	// Assert.
	if err != nil {
		t.Fatalf("applyComputationBoundary() error = %v", err)
	}
	for _, want := range [][]byte{
		[]byte("Narrative stays."),
		[]byte("<!-- comment stays -->"),
		[]byte("<span>HTML stays</span>"),
	} {
		if !bytes.Contains(updated, want) {
			t.Fatalf("updated document lost %q:\n%s", want, updated)
		}
	}
	if bytes.Contains(updated, []byte("# Computation")) || bytes.Contains(updated, []byte("```sql")) {
		t.Fatalf("sanctioned heading/fence remain:\n%s", updated)
	}
}

func TestApplyComputationBoundary_RejectsNestedOrUnclosedFences(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{name: "blockquote", source: "# Computation\n\n> ```sql\n> SELECT 1;\n> ```\n"},
		{name: "list", source: "# Computation\n\n- ```sql\n  SELECT 1;\n  ```\n"},
		{name: "unclosed", source: "# Computation\n\n```sql\nSELECT 1;\n"},
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

			// Assert.
			if updated != nil {
				t.Fatalf("updated = %q, want nil", updated)
			}
			if !errors.Is(err, ErrAmbiguousPresentation) {
				t.Fatalf("error = %v, want ErrAmbiguousPresentation", err)
			}
		})
	}
}

func TestValidateMigrationReceipt_BindsStructuralAndAuthorizedPreviewFields(t *testing.T) {
	// Arrange.
	base := revisionFor(t, memorySource{"a.md": []byte("base")})
	result := revisionFor(t, memorySource{"a.md": []byte("result")})
	request := MigrationRequest{
		ID:          "receipt-binding",
		Actor:       "operator",
		FromVersion: MigrationVersionV01,
		ToVersion:   MigrationVersionV02,
		Migration: store.V01ToV02Migration{
			GeneratedBy:       "process:migration",
			TimestampPolicy:   store.LegacyTimestampPreserve,
			TimestampConflict: store.TimestampConflictReject,
		},
	}
	change, err := migrationChangeSet(request, base)
	if err != nil {
		t.Fatal(err)
	}
	requestDigest, err := change.RequestDigest()
	if err != nil {
		t.Fatal(err)
	}
	key, err := PlanDigestIdempotencyKey(
		"migration:v0.1-to-v0.2",
		"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"caller",
	)
	if err != nil {
		t.Fatal(err)
	}
	rootBang := ref(t, "a!")
	fragment := ref(t, "a#z")
	proof := MigrationPlanProof{
		ResultRevision: result,
		ChangedFiles:   []store.FileChange{{Kind: store.FileWrite, Path: "a.md"}},
		// Proof v2 keeps structural order: ID "a" precedes ID "a!".
		ChangedRefs: []bundle.RelationRef{fragment, rootBang},
	}
	valid := store.CommitReceipt{
		FormatVersion: store.CommitReceiptFormatVersion,
		ChangeSetID:   change.ID, IdempotencyKey: key, RequestDigest: requestDigest,
		BaseRevision: base, ResultRevision: result, CommitTime: time.Now().UTC(),
		ChangedFiles: []store.FileChange{{Kind: store.FileWrite, Path: "a.md"}},
		// Receipt v1 keeps lexical wire order: "a!" precedes "a#z".
		ChangedRefs: []bundle.RelationRef{rootBang, fragment},
	}
	options := store.CommitOptions{IdempotencyKey: key}
	tests := map[string]func(*store.CommitReceipt){
		"format":        func(value *store.CommitReceipt) { value.FormatVersion = 0 },
		"result":        func(value *store.CommitReceipt) { value.ResultRevision = base },
		"changed files": func(value *store.CommitReceipt) { value.ChangedFiles = nil },
		"changed refs":  func(value *store.CommitReceipt) { value.ChangedRefs = nil },
		"key":           func(value *store.CommitReceipt) { value.IdempotencyKey = "other" },
	}

	// Act/Assert.
	if err := validateMigrationReceipt(change, options, proof, valid); err != nil {
		t.Fatalf("valid receipt error = %v", err)
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			hostile := valid
			hostile.ChangedFiles = append([]store.FileChange(nil), valid.ChangedFiles...)
			hostile.ChangedRefs = append([]bundle.RelationRef(nil), valid.ChangedRefs...)
			mutate(&hostile)
			err := validateMigrationReceipt(change, options, proof, hostile)
			if !errors.Is(err, ErrMigrationPlanMismatch) {
				t.Fatalf("hostile receipt error = %v, want ErrMigrationPlanMismatch", err)
			}
		})
	}
	duplicateProof := proof.Clone()
	duplicateProof.ChangedRefs = []bundle.RelationRef{fragment, fragment}
	if err := validateMigrationReceipt(change, options, duplicateProof, valid); !errors.Is(err, ErrMigrationPlanMismatch) {
		t.Fatalf("duplicate proof refs error = %v, want ErrMigrationPlanMismatch", err)
	}
}

func TestMigrationPlannerResolveAndPreview_RootlessLegacyCandidateMigratesWithoutVersionDeclaration(t *testing.T) {
	// Arrange.
	source := memorySource{
		"concept.md": []byte("---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n---\n\nBody.\n"),
	}
	planner := NewMigrationPlanner()

	// Act.
	resolution, resolveErr := planner.ResolveSource(context.Background(), source, MigrationSourceOptions{
		RequestedSelector: MigrationSelectorAuto,
	})
	preview, previewErr := planner.Preview(context.Background(), source, testMigrationResolution(t, source), MigrationRequest{
		ID: "rootless-legacy", Actor: "operator",
		FromVersion: resolution.FromVersion, ToVersion: resolution.ToVersion,
		Migration: store.V01ToV02Migration{
			GeneratedBy:       "process:migration",
			TimestampPolicy:   store.LegacyTimestampRemoveAfterCopy,
			TimestampConflict: store.TimestampConflictReject,
		},
	})

	// Assert.
	if resolveErr != nil || previewErr != nil {
		t.Fatalf("resolve/preview errors = %v, %v", resolveErr, previewErr)
	}
	if resolution.Transition != MigrationTransitionV01ToV02 ||
		resolution.ResolvedSource != MigrationVersionV01 {
		t.Fatalf("resolution = %#v", resolution)
	}
	if len(preview.Preview.Writes) != 1 || preview.Preview.Writes[0].Path != "concept.md" {
		t.Fatalf("preview writes = %#v, want only rootless concept", preview.Preview.Writes)
	}
	if _, err := preview.Staged.ReadFile(context.Background(), "index.md"); err == nil {
		t.Fatal("rootless migration invented index.md")
	}
}

func TestMigrationPlannerReplay_ScansReservedMarkdownAndEveryRequestedPath(t *testing.T) {
	tests := []struct {
		name    string
		source  memorySource
		request store.V01ToV02Migration
	}{
		{
			name: "legacy citations in log",
			source: memorySource{
				"index.md": []byte("---\nokf_version: \"0.2\"\n---\n"),
				"log.md":   []byte("# Citations\n\n[1] https://example.test\n"),
			},
			request: store.V01ToV02Migration{
				GeneratedBy:       "process:migration",
				TimestampPolicy:   store.LegacyTimestampPreserve,
				TimestampConflict: store.TimestampConflictReject,
			},
		},
		{
			name: "deleted generated-at path",
			source: memorySource{
				"index.md": []byte("---\nokf_version: \"0.2\"\n---\n"),
			},
			request: store.V01ToV02Migration{
				GeneratedBy:       "process:migration",
				GeneratedAt:       []store.MigrationGeneratedAt{{Path: "deleted.md", At: "2026-06-25T09:00:00Z"}},
				TimestampPolicy:   store.LegacyTimestampPreserve,
				TimestampConflict: store.TimestampConflictReject,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			request := MigrationRequest{
				ID: "replay-" + store.ChangeSetID(test.name), Actor: "operator",
				FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
				Migration: test.request,
			}

			// Act.
			preview, err := NewMigrationPlanner().Preview(context.Background(), test.source, testMigrationResolution(t, test.source), request)

			// Assert.
			if err == nil {
				t.Fatalf("Preview() = %#v, want replay mismatch", preview)
			}
			if preview.Staged != nil || len(preview.Preview.Writes) != 0 {
				t.Fatalf("blocked replay exposed stage: %#v", preview)
			}
		})
	}
}

func TestMigrationPlannerPreview_MigratesExplicitCitationsOnEveryOwnedMarkdownRole(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		source memorySource
	}{
		{
			name: "root index",
			path: "index.md",
			source: memorySource{
				"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [A](a.md)\n\nClaim [1].\n\n# Citations\n\n[1] https://example.test\n"),
				"a.md":     []byte("---\ntype: Knowledge\n---\n"),
			},
		},
		{
			name: "root log",
			path: "log.md",
			source: memorySource{
				"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [A](a.md)\n"),
				"a.md":     []byte("---\ntype: Knowledge\n---\n"),
				"log.md":   []byte("Claim [1].\n\n# Citations\n\n[1] https://example.test\n\n# Log\n\n## 2026-01-01\n\n- event\n"),
			},
		},
		{
			name: "nested index",
			path: "nested/index.md",
			source: memorySource{
				"index.md":        []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [Nested](nested/index.md)\n"),
				"nested/index.md": []byte("# Nested\n\n- [A](a.md)\n\nClaim [1].\n\n# Citations\n\n[1] https://example.test\n"),
				"nested/a.md":     []byte("---\ntype: Knowledge\n---\n"),
			},
		},
		{
			name: "orphan markdown concept",
			path: "orphan.md",
			source: memorySource{
				"index.md":  []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [Orphan](orphan.md)\n"),
				"orphan.md": []byte("---\ntype: Knowledge\n---\n\nClaim [1].\n\n# Citations\n\n[1] https://example.test\n"),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			request := MigrationRequest{
				ID:    "citation-role-" + store.ChangeSetID(strings.ReplaceAll(test.name, " ", "-")),
				Actor: "operator", FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
				Migration: store.V01ToV02Migration{
					TimestampPolicy:   store.LegacyTimestampPreserve,
					TimestampConflict: store.TimestampConflictReject,
					Citations: []store.DocumentCitationMigration{{
						Path: test.path,
						Entries: []store.LegacyCitationMapping{{
							LegacyNumber: 1,
							LegacyEntry:  "https://example.test",
							SourceID:     "source",
						}},
					}},
				},
			}

			// Act.
			preview, err := NewMigrationPlanner().Preview(context.Background(), test.source, testMigrationResolution(t, test.source), request)

			// Assert.
			if err != nil {
				t.Fatalf("Preview() error = %v", err)
			}
			updated, readErr := preview.Staged.ReadFile(context.Background(), test.path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if bytes.Contains(updated, []byte("# Citations")) ||
				bytes.Contains(updated, []byte("Claim [1]")) ||
				!bytes.Contains(updated, []byte("Claim [^source]")) {
				t.Fatalf("citation migration incomplete:\n%s", updated)
			}
			hasSources := bytes.Contains(updated, []byte("sources:"))
			wantSources := validator.DetermineFileRole(test.path) == validator.RoleConcept
			if hasSources != wantSources {
				t.Fatalf("sources metadata present = %t, want %t:\n%s", hasSources, wantSources, updated)
			}
		})
	}
}

func TestMigrationPlannerReplay_UsesSingleBodyOwnedFootnoteProjection(t *testing.T) {
	tests := []struct {
		name string
		path string
		role string
	}{
		{name: "root index", path: "index.md", role: "index"},
		{name: "root log", path: "log.md", role: "log"},
		{name: "nested index", path: "nested/index.md", role: "nested"},
		{name: "ordinary concept", path: "a.md", role: "concept"},
	}
	newlines := []struct{ name, value string }{
		{name: "LF", value: "\n"},
		{name: "CRLF", value: "\r\n"},
	}
	for _, role := range tests {
		for _, lineEnding := range newlines {
			t.Run(role.name+"/"+lineEnding.name, func(t *testing.T) {
				// Arrange.
				nl := lineEnding.value
				legacyBody := "Claim [1]." + nl + nl +
					"# Citations" + nl + nl +
					"[1] https://example.test" + nl
				source := memorySource{}
				switch role.role {
				case "index":
					source["index.md"] = []byte("---" + nl +
						"okf_version: \"0.1\"" + nl +
						"---" + nl + nl +
						"# Knowledge" + nl + nl +
						"- [A](a.md)" + nl + nl +
						legacyBody)
					source["a.md"] = []byte("---" + nl + "type: Knowledge" + nl + "---" + nl)
				case "log":
					source["index.md"] = []byte("---" + nl + "okf_version: \"0.1\"" + nl + "---" + nl + nl +
						"# Knowledge" + nl + nl +
						"- [A](a.md)" + nl)
					source["a.md"] = []byte("---" + nl + "type: Knowledge" + nl + "---" + nl)
					source["log.md"] = []byte(legacyBody + nl +
						"# Log" + nl + nl +
						"## 2026-01-01" + nl + nl +
						"- event" + nl)
				case "nested":
					source["index.md"] = []byte("---" + nl + "okf_version: \"0.1\"" + nl + "---" + nl + nl +
						"# Knowledge" + nl + nl +
						"- [Nested](nested/index.md)" + nl)
					source["nested/index.md"] = []byte("# Nested" + nl + nl +
						"- [A](a.md)" + nl + nl +
						legacyBody)
					source["nested/a.md"] = []byte("---" + nl + "type: Knowledge" + nl + "---" + nl)
				case "concept":
					source["index.md"] = []byte("---" + nl + "okf_version: \"0.1\"" + nl + "---" + nl + nl +
						"# Knowledge" + nl + nl +
						"- [A](a.md)" + nl)
					source["a.md"] = []byte("---" + nl + "type: Knowledge" + nl + "---" + nl + nl +
						legacyBody)
				default:
					t.Fatalf("unknown role %q", role.role)
				}
				request := MigrationRequest{
					ID: "single-footnote-projection-" + store.ChangeSetID(
						strings.ReplaceAll(role.name+"-"+lineEnding.name, " ", "-"),
					),
					Actor: "operator", FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
					Migration: store.V01ToV02Migration{
						TimestampPolicy:   store.LegacyTimestampPreserve,
						TimestampConflict: store.TimestampConflictReject,
						Citations: []store.DocumentCitationMigration{{
							Path: role.path,
							Entries: []store.LegacyCitationMapping{{
								LegacyNumber: 1, SourceID: "source", Resource: "https://example.test",
							}},
						}},
					},
				}
				planner := NewMigrationPlanner()

				// Act.
				initial, initialErr := planner.Preview(context.Background(), source, testMigrationResolution(t, source), request)

				// Assert.
				if initialErr != nil {
					t.Fatalf("initial Preview() error = %v", initialErr)
				}
				updated, err := initial.Staged.ReadFile(context.Background(), role.path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Contains(updated, []byte("Claim [^source]."+nl)) ||
					!bytes.Contains(updated, []byte("[^source]: https://example.test"+nl)) {
					t.Fatalf("initial migration lost ownership boundaries:\n%s", updated)
				}
				replay, replayErr := planner.Preview(context.Background(), initial.Staged, testMigrationResolution(t, initial.Staged), request)
				if replayErr != nil {
					t.Fatalf("replay Preview() error = %v", replayErr)
				}
				if len(replay.Preview.Writes) != 0 ||
					replay.Preview.BaseRevision != replay.Preview.ResultRevision {
					t.Fatalf("replay is not a noop: %#v", replay.Preview)
				}

				cloneStaged := func() memorySource {
					out := memorySource{}
					for path := range source {
						content, readErr := initial.Staged.ReadFile(context.Background(), path)
						if readErr != nil {
							t.Fatal(readErr)
						}
						out[path] = content
					}
					return out
				}
				tampered := cloneStaged()
				definitionContentStart := bytes.LastIndex(tampered[role.path], []byte("https://example.test"))
				if definitionContentStart < 0 {
					t.Fatalf("generated definition content missing:\n%s", tampered[role.path])
				}
				copy(
					tampered[role.path][definitionContentStart:],
					[]byte("https://invalid.test"),
				)
				blocked, tamperErr := planner.Preview(context.Background(), tampered, testMigrationResolution(t, tampered), request)
				if tamperErr == nil {
					t.Fatalf("tampered Preview() = %#v, want replay mismatch", blocked)
				}
				if blocked.Staged != nil || len(blocked.Preview.Writes) != 0 ||
					len(blocked.Blockers) != 1 ||
					blocked.Blockers[0].Code != "migration_replay_mismatch" {
					t.Fatalf("tampered replay = %#v", blocked)
				}
				wantSpan := SourceSpan{
					Start: definitionContentStart,
					End:   definitionContentStart + len("https://invalid.test"),
				}
				if blocked.Blockers[0].Location != wantSpan {
					t.Fatalf("tamper span = %#v, want %#v", blocked.Blockers[0].Location, wantSpan)
				}

				missing := cloneStaged()
				missing[role.path] = bytes.Replace(
					missing[role.path],
					[]byte("[^source]: https://example.test"+nl),
					nil,
					1,
				)
				missingBlocked, missingErr := planner.Preview(context.Background(), missing, testMigrationResolution(t, missing), request)
				if missingErr == nil {
					t.Fatalf("missing-definition Preview() = %#v, want replay mismatch", missingBlocked)
				}
				if missingBlocked.Staged != nil || len(missingBlocked.Preview.Writes) != 0 ||
					len(missingBlocked.Blockers) != 1 ||
					missingBlocked.Blockers[0].Code != "migration_replay_mismatch" {
					t.Fatalf("missing-definition replay = %#v", missingBlocked)
				}
				wantEOF := SourceSpan{Start: len(missing[role.path]), End: len(missing[role.path])}
				if missingBlocked.Blockers[0].Location != wantEOF {
					t.Fatalf("missing definition span = %#v, want EOF %#v", missingBlocked.Blockers[0].Location, wantEOF)
				}

				if role.role == "concept" {
					derivedRequest := request
					derivedRequest.ID += "-derived-source"
					derivedRequest.Migration.Citations = []store.DocumentCitationMigration{{
						Path: role.path,
						Entries: []store.LegacyCitationMapping{{
							LegacyNumber: 1,
							SourceID:     "source",
						}},
					}}
					derivedReplay, derivedErr := planner.Preview(
						context.Background(),
						initial.Staged, testMigrationResolution(t, initial.Staged),
						derivedRequest,
					)
					if derivedErr != nil ||
						len(derivedReplay.Preview.Writes) != 0 ||
						derivedReplay.Preview.BaseRevision != derivedReplay.Preview.ResultRevision {
						t.Fatalf("derived-source replay = %#v, error = %v", derivedReplay, derivedErr)
					}

					metadataTampered := cloneStaged()
					metadataValueStart := bytes.Index(
						metadataTampered[role.path],
						[]byte("https://example.test"),
					)
					if metadataValueStart < 0 || metadataValueStart == definitionContentStart {
						t.Fatalf("generated source metadata missing:\n%s", metadataTampered[role.path])
					}
					copy(
						metadataTampered[role.path][metadataValueStart:],
						[]byte("https://invalid.test"),
					)
					derivedBlocked, derivedTamperErr := planner.Preview(
						context.Background(),
						metadataTampered, testMigrationResolution(t, metadataTampered),
						derivedRequest,
					)
					derivedWantSpan := SourceSpan{
						Start: definitionContentStart,
						End:   definitionContentStart + len("https://example.test"),
					}
					if derivedTamperErr == nil ||
						len(derivedBlocked.Blockers) != 1 ||
						derivedBlocked.Blockers[0].Code != "migration_replay_mismatch" ||
						derivedBlocked.Blockers[0].Location != derivedWantSpan {
						t.Fatalf(
							"derived metadata mismatch = %#v, error = %v, want definition span %#v",
							derivedBlocked,
							derivedTamperErr,
							derivedWantSpan,
						)
					}

					duplicateSources := cloneStaged()
					sourceIDStart := bytes.Index(duplicateSources[role.path], []byte("id: source"))
					if sourceIDStart < 0 {
						t.Fatalf("generated source id missing:\n%s", duplicateSources[role.path])
					}
					itemPrefix := []byte(nl + "  -")
					itemStart := bytes.LastIndex(duplicateSources[role.path][:sourceIDStart], itemPrefix)
					if itemStart < 0 {
						t.Fatalf("generated source item missing:\n%s", duplicateSources[role.path])
					}
					itemStart += len(nl)
					frontmatterEnd := bytes.Index(
						duplicateSources[role.path][itemStart:],
						[]byte(nl+"---"+nl),
					)
					if frontmatterEnd < 0 {
						t.Fatalf("generated frontmatter end missing:\n%s", duplicateSources[role.path])
					}
					itemEnd := itemStart + frontmatterEnd
					item := append([]byte(nil), duplicateSources[role.path][itemStart:itemEnd]...)
					withDuplicate := make([]byte, 0, len(duplicateSources[role.path])+len(item)+len(nl))
					withDuplicate = append(withDuplicate, duplicateSources[role.path][:itemEnd]...)
					withDuplicate = append(withDuplicate, []byte(nl)...)
					withDuplicate = append(withDuplicate, item...)
					withDuplicate = append(withDuplicate, duplicateSources[role.path][itemEnd:]...)
					duplicateSources[role.path] = withDuplicate
					duplicateBlocked, duplicateErr := planner.Preview(
						context.Background(),
						duplicateSources, testMigrationResolution(t, duplicateSources),
						derivedRequest,
					)
					bodyStart := bytes.Index(duplicateSources[role.path], []byte("Claim [^source]"))
					if duplicateErr == nil ||
						!errors.Is(duplicateErr, ErrAmbiguousPresentation) ||
						len(duplicateBlocked.Blockers) == 0 ||
						duplicateBlocked.Blockers[0].Location.End <= duplicateBlocked.Blockers[0].Location.Start ||
						duplicateBlocked.Blockers[0].Location.End > bodyStart {
						t.Fatalf(
							"duplicate source replay = %#v, error = %v, want exact YAML ambiguity",
							duplicateBlocked,
							duplicateErr,
						)
					}

					metadataBlocked, metadataErr := planner.Preview(
						context.Background(),
						metadataTampered, testMigrationResolution(t, metadataTampered),
						request,
					)
					if metadataErr == nil ||
						metadataBlocked.Staged != nil ||
						len(metadataBlocked.Preview.Writes) != 0 ||
						len(metadataBlocked.Blockers) != 1 ||
						metadataBlocked.Blockers[0].Code != "migration_replay_mismatch" {
						t.Fatalf("metadata-tampered replay = %#v, error = %v", metadataBlocked, metadataErr)
					}
					bodyStart = bytes.Index(metadataTampered[role.path], []byte("Claim [^source]"))
					location := metadataBlocked.Blockers[0].Location
					if location.Start < 0 ||
						location.End <= location.Start ||
						bodyStart < 0 ||
						location.End > bodyStart ||
						location == wantSpan {
						t.Fatalf("metadata mismatch span = %#v, want exact YAML metadata span", location)
					}
				}
			})
		}
	}
}

func TestMigrationPlannerPreview_InsertsUndeclaredExistingRootVersionAfterLegacyPreflight(t *testing.T) {
	tests := []struct {
		name      string
		index     []byte
		wantBytes [][]byte
	}{
		{
			name:  "body only LF comments",
			index: []byte("<!-- keep root comment -->\n\n# Knowledge\n\n- [A](a.md)\n"),
			wantBytes: [][]byte{
				[]byte("<!-- keep root comment -->\n"),
				[]byte("---\nokf_version: \"0.2\"\n---\n"),
				[]byte("okf_version: \"0.2\"\n"),
			},
		},
		{
			name:  "body only CRLF",
			index: []byte("# Knowledge\r\n\r\n- [A](a.md)\r\n"),
			wantBytes: [][]byte{
				[]byte("---\r\nokf_version: \"0.2\"\r\n---\r\n"),
				[]byte("# Knowledge\r\n\r\n- [A](a.md)\r\n"),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			source := memorySource{
				"index.md": append([]byte(nil), test.index...),
				"a.md":     []byte("---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n---\n\nA.\n"),
			}
			before := cloneMemorySource(source)
			request := MigrationRequest{
				ID: "undeclared-root", Actor: "operator",
				FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
				Migration: store.V01ToV02Migration{
					GeneratedBy:       "process:migration",
					TimestampPolicy:   store.LegacyTimestampPreserve,
					TimestampConflict: store.TimestampConflictReject,
				},
			}

			// Act.
			resolution, resolveErr := ResolveMigrationSource(context.Background(), source, MigrationSourceOptions{
				RequestedSelector: MigrationSelectorAuto,
			})
			preview, err := NewMigrationPlanner().Preview(context.Background(), source, testMigrationResolution(t, source), request)

			// Assert.
			if resolveErr != nil || resolution.ResolutionSource != MigrationResolutionLegacyProbe {
				t.Fatalf("ResolveMigrationSource() = %#v, %v", resolution, resolveErr)
			}
			if err != nil {
				t.Fatalf("Preview() error = %v", err)
			}
			index, readErr := preview.Staged.ReadFile(context.Background(), "index.md")
			if readErr != nil {
				t.Fatal(readErr)
			}
			if bytes.Count(index, []byte("okf_version: \"0.2\"")) != 1 {
				t.Fatalf("staged index version =\n%s", index)
			}
			for _, want := range test.wantBytes {
				if !bytes.Contains(index, want) {
					t.Fatalf("staged index omitted %q:\n%s", want, index)
				}
			}
			if !equalMemorySource(source, before) {
				t.Fatal("Preview mutated the source")
			}
		})
	}
}

func TestMigrationPlannerPreview_RejectsMalformedOrDuplicateUndeclaredRootVersion(t *testing.T) {
	tests := []struct {
		name  string
		index string
	}{
		{
			name:  "malformed scalar",
			index: "---\nokf_version: 1\n---\n\n# Knowledge\n\n- [A](a.md)\n",
		},
		{
			name:  "duplicate declaration",
			index: "---\nokf_version: \"0.1\"\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [A](a.md)\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			source := memorySource{
				"index.md": []byte(test.index),
				"a.md":     []byte("---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n---\n"),
			}
			request := MigrationRequest{
				ID: "invalid-root-version", Actor: "operator",
				FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
				Migration: store.V01ToV02Migration{
					GeneratedBy:       "process:migration",
					TimestampPolicy:   store.LegacyTimestampPreserve,
					TimestampConflict: store.TimestampConflictReject,
				},
			}

			// Act.
			preview, err := NewMigrationPlanner().Preview(context.Background(), source, testUnresolvedMigrationResolution(t, source), request)

			// Assert.
			if err == nil {
				t.Fatalf("Preview() = %#v, want malformed declaration rejection", preview)
			}
			if preview.Staged != nil || len(preview.Preview.Writes) != 0 {
				t.Fatalf("rejected preview exposed stage: %#v", preview)
			}
		})
	}
}

func TestMigrationPlannerPreview_RejectsStructuredSourcesWithLegacyCitationsWithoutOverwrite(t *testing.T) {
	const message = "document contains both structured sources and legacy # Citations; normalize to one explicit representation before migration"
	tests := []struct {
		name    string
		sources string
	}{
		{
			name: "keyed source",
			sources: "sources:\n" +
				"  - id: existing\n" +
				"    title: Existing\n" +
				"    resource: https://existing.test\n",
		},
		{
			name: "anonymous source",
			sources: "sources:\n" +
				"  - resource: https://existing.test\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			source := memorySource{
				"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [A](a.md)\n"),
				"a.md": []byte("---\ntype: Knowledge\n" + test.sources + "---\n\n" +
					"Claim [1].\n\n# Citations\n\n[1] https://legacy.test\n"),
			}
			before := cloneMemorySource(source)
			request := MigrationRequest{
				ID: "sources-citations-conflict", Actor: "operator",
				FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
				Migration: store.V01ToV02Migration{
					TimestampPolicy:   store.LegacyTimestampPreserve,
					TimestampConflict: store.TimestampConflictReject,
					Citations: []store.DocumentCitationMigration{{
						Path: "a.md",
						Entries: []store.LegacyCitationMapping{{
							LegacyNumber: 1,
							LegacyEntry:  "https://legacy.test",
							SourceID:     "legacy",
						}},
					}},
				},
			}

			// Act.
			preview, err := NewMigrationPlanner().Preview(context.Background(), source, testMigrationResolution(t, source), request)

			// Assert.
			if !errors.Is(err, ErrAmbiguousPresentation) {
				t.Fatalf("Preview() error = %v, want ErrAmbiguousPresentation", err)
			}
			if len(preview.Blockers) != 1 ||
				preview.Blockers[0].Code != "sources_citations_conflict" ||
				preview.Blockers[0].Path != "a.md" ||
				preview.Blockers[0].Message != message {
				t.Fatalf("Blockers = %#v", preview.Blockers)
			}
			if len(preview.ManualActions) != 1 ||
				preview.ManualActions[0].Code != "reconcile_sources_and_citations" ||
				preview.ManualActions[0].Message != message {
				t.Fatalf("ManualActions = %#v", preview.ManualActions)
			}
			if preview.Staged != nil || len(preview.Preview.Writes) != 0 || !equalMemorySource(source, before) {
				t.Fatalf("conflict changed or exposed a stage: %#v", preview)
			}
		})
	}
}

func TestMigrationPlannerPreview_AggregatesCitationParserFailuresWithExactActions(t *testing.T) {
	// Arrange.
	ambiguous := []byte("---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n---\n\n" +
		"# Citations\n\n[1] [One](https://one.test) [Two](https://two.test)\n")
	invalid := append([]byte("---\ntype: Knowledge\n---\n\n"), 0xff)
	later := []byte("---\ntype: Knowledge\n---\n\nClaim [1].\n\n# Citations\n\n[1] https://later.test\n")
	source := memorySource{
		"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [A](a.md)\n- [B](b.md)\n- [C](c.md)\n"),
		"a.md":     ambiguous,
		"b.md":     invalid,
		"c.md":     later,
	}
	request := MigrationRequest{
		ID: "aggregate-parser-blockers", Actor: "operator",
		FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
		Migration: store.V01ToV02Migration{
			TimestampPolicy:   store.LegacyTimestampPreserve,
			TimestampConflict: store.TimestampConflictReject,
		},
	}

	// Act.
	preview, err := NewMigrationPlanner().Preview(context.Background(), source, testMigrationResolution(t, source), request)

	// Assert.
	if err == nil {
		t.Fatal("Preview() error = nil, want aggregated parser blockers")
	}
	wantBlockers := []struct {
		path string
		code string
	}{
		{"a.md", "ambiguous_citation_destination"},
		{"a.md", "missing_explicit_generated_by"},
		{"b.md", "invalid_utf8"},
		{"c.md", "missing_explicit_citation_mapping"},
	}
	if len(preview.Blockers) != len(wantBlockers) {
		t.Fatalf("Blockers = %#v", preview.Blockers)
	}
	for index, want := range wantBlockers {
		got := preview.Blockers[index]
		if got.Path != want.path || got.Code != want.code {
			t.Fatalf("Blockers = %#v, want stable path/code order", preview.Blockers)
		}
		if (got.Code == "ambiguous_citation_destination" || got.Code == "invalid_utf8") &&
			(got.Location.End <= got.Location.Start || got.Location.End > len(source[got.Path])) {
			t.Fatalf("Blocker location = %#v for %s", got.Location, got.Code)
		}
	}
	wantActions := []struct {
		path string
		code string
	}{
		{"a.md", "disambiguate_citation_destination"},
		{"a.md", "provide_generated_by"},
		{"b.md", "repair_invalid_utf8"},
		{"c.md", "provide_citation_mapping"},
	}
	if len(preview.ManualActions) != len(wantActions) {
		t.Fatalf("ManualActions = %#v", preview.ManualActions)
	}
	for index, want := range wantActions {
		got := preview.ManualActions[index]
		if got.Path != want.path || got.Code != want.code {
			t.Fatalf("ManualActions = %#v, want stable path/code order", preview.ManualActions)
		}
	}
	if preview.Staged != nil || len(preview.Preview.Writes) != 0 {
		t.Fatalf("blocked preview exposed stage: %#v", preview)
	}
}

func TestMigrationPlannerPreview_DuplicateUnnumberedSelectorNeedsEntryDisambiguation(t *testing.T) {
	// Arrange.
	source := memorySource{
		"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [A](a.md)\n"),
		"a.md": []byte("---\ntype: Knowledge\n---\n\n# Citations\n\n" +
			"- https://same.test\n- https://same.test\n"),
	}
	request := MigrationRequest{
		ID: "duplicate-unnumbered-selector", Actor: "operator",
		FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
		Migration: store.V01ToV02Migration{
			TimestampPolicy:   store.LegacyTimestampPreserve,
			TimestampConflict: store.TimestampConflictReject,
			Citations: []store.DocumentCitationMigration{{
				Path: "a.md",
				Entries: []store.LegacyCitationMapping{{
					LegacyEntry: "https://same.test",
					SourceID:    "same",
				}},
			}},
		},
	}

	// Act.
	preview, err := NewMigrationPlanner().Preview(context.Background(), source, testMigrationResolution(t, source), request)

	// Assert.
	if !errors.Is(err, ErrAmbiguousPresentation) {
		t.Fatalf("Preview() error = %v, want ErrAmbiguousPresentation", err)
	}
	if len(preview.Blockers) != 1 || preview.Blockers[0].Code != "ambiguous_citation_mapping" ||
		preview.Blockers[0].Location.End <= preview.Blockers[0].Location.Start {
		t.Fatalf("Blockers = %#v", preview.Blockers)
	}
	if len(preview.ManualActions) != 1 ||
		preview.ManualActions[0].Code != "disambiguate_citation_entry" {
		t.Fatalf("ManualActions = %#v", preview.ManualActions)
	}
	if preview.Staged != nil || len(preview.Preview.Writes) != 0 {
		t.Fatalf("blocked preview exposed stage: %#v", preview)
	}
}

func TestMigrationPlannerPreview_MatchesMultilineLegacyEntryBytesExactly(t *testing.T) {
	tests := []struct {
		name    string
		newline string
	}{
		{name: "LF", newline: "\n"},
		{name: "CRLF", newline: "\r\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			raw := "[Spec](https://example.test/spec)" + test.newline + "  continued provenance"
			source := memorySource{
				"index.md": []byte("---" + test.newline + "okf_version: \"0.1\"" + test.newline + "---" + test.newline +
					"# Knowledge" + test.newline + test.newline + "- [A](a.md)" + test.newline),
				"a.md": []byte("---" + test.newline + "type: Knowledge" + test.newline + "---" + test.newline +
					"# Citations" + test.newline + test.newline + "- " + raw + test.newline),
			}
			request := MigrationRequest{
				ID: "multiline-raw-" + store.ChangeSetID(strings.ToLower(test.name)), Actor: "operator",
				FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
				Migration: store.V01ToV02Migration{
					TimestampPolicy:   store.LegacyTimestampPreserve,
					TimestampConflict: store.TimestampConflictReject,
					Citations: []store.DocumentCitationMigration{{
						Path: "a.md",
						Entries: []store.LegacyCitationMapping{{
							LegacyEntry: raw,
							SourceID:    "source",
						}},
					}},
				},
			}

			// Act.
			preview, err := NewMigrationPlanner().Preview(context.Background(), source, testMigrationResolution(t, source), request)

			// Assert.
			if err != nil {
				t.Fatalf("Preview() error = %v", err)
			}
			updated, readErr := preview.Staged.ReadFile(context.Background(), "a.md")
			if readErr != nil {
				t.Fatal(readErr)
			}
			if bytes.Contains(updated, []byte("# Citations")) ||
				!bytes.Contains(updated, []byte("[^source]: [Spec](https://example.test/spec)")) {
				t.Fatalf("multiline exact selector was not migrated:\n%s", updated)
			}
		})
	}
}

func TestMigrationPlannerPreview_ReusesSourceOnlyWithConsistentResolvedMetadata(t *testing.T) {
	// Arrange.
	source := memorySource{
		"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [A](a.md)\n"),
		"a.md": []byte("---\ntype: Knowledge\n---\n\nClaim [1] and [2].\n\n# Citations\n\n" +
			"[1] [Same](https://same.test)\n\n[2] [Same](https://same.test)\n"),
	}
	baseRequest := MigrationRequest{
		ID: "reuse-source", Actor: "operator",
		FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
		Migration: store.V01ToV02Migration{
			TimestampPolicy:   store.LegacyTimestampPreserve,
			TimestampConflict: store.TimestampConflictReject,
			Citations: []store.DocumentCitationMigration{{
				Path: "a.md",
				Entries: []store.LegacyCitationMapping{
					{LegacyNumber: 1, LegacyEntry: "[Same](https://same.test)", SourceID: "same"},
					{LegacyNumber: 2, LegacyEntry: "[Same](https://same.test)", SourceID: "same"},
				},
			}},
		},
	}

	// Act.
	preview, err := NewMigrationPlanner().Preview(context.Background(), source, testMigrationResolution(t, source), baseRequest)

	// Assert.
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	updated, readErr := preview.Staged.ReadFile(context.Background(), "a.md")
	if readErr != nil {
		t.Fatal(readErr)
	}
	if bytes.Count(updated, []byte("id: same")) != 1 ||
		bytes.Count(updated, []byte("[^same]:")) != 1 ||
		!bytes.Contains(updated, []byte("Claim [^same] and [^same].")) {
		t.Fatalf("consistent source reuse was not deduplicated:\n%s", updated)
	}

	conflicting := baseRequest
	conflicting.ID = "reuse-source-conflict"
	conflicting.Migration = baseRequest.Migration
	conflicting.Migration.Citations = append([]store.DocumentCitationMigration(nil), baseRequest.Migration.Citations...)
	conflicting.Migration.Citations[0].Entries = append(
		[]store.LegacyCitationMapping(nil),
		baseRequest.Migration.Citations[0].Entries...,
	)
	conflicting.Migration.Citations[0].Entries[0].Title = "One"
	conflicting.Migration.Citations[0].Entries[1].Title = "Two"
	blocked, conflictErr := NewMigrationPlanner().Preview(context.Background(), source, testMigrationResolution(t, source), conflicting)
	if !errors.Is(conflictErr, store.ErrInvalidChangeSet) {
		t.Fatalf("conflicting Preview() error = %v, want ErrInvalidChangeSet", conflictErr)
	}
	if !reflect.DeepEqual(blocked, MigrationPreview{}) {
		t.Fatalf("conflicting source reuse preview = %#v", blocked)
	}
}

func TestMigrationPlannerPreview_UsesMergeAwareLegacyFallbackObservation(t *testing.T) {
	tests := []struct {
		name        string
		frontmatter string
		body        string
		wantCode    string
	}{
		{
			name: "merged sources suppress Citations fallback",
			frontmatter: "type: Knowledge\n" +
				"defaults: &defaults\n" +
				"  sources: [{id: existing, resource: https://existing.test}]\n" +
				"<<: *defaults\n",
			body:     "Claim [1].\n\n# Citations\n\n[1] https://legacy.test\n",
			wantCode: "sources_citations_conflict",
		},
		{
			name: "terminal aliased sources suppress Citations fallback",
			frontmatter: "type: Knowledge\n" +
				"shared_sources: &shared_sources [{id: existing, resource: https://existing.test}]\n" +
				"sources: *shared_sources\n",
			body:     "Claim [1].\n\n# Citations\n\n[1] https://legacy.test\n",
			wantCode: "sources_citations_conflict",
		},
		{
			name: "merged generated suppresses timestamp fallback",
			frontmatter: "type: Knowledge\n" +
				"defaults: &defaults\n" +
				"  generated: {by: process:existing, at: 2026-06-25T09:00:00Z}\n" +
				"<<: *defaults\n" +
				"timestamp: 2026-06-25T09:00:00Z\n",
			body:     "A.\n",
			wantCode: "alias_provenance",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			source := memorySource{
				"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [A](a.md)\n"),
				"a.md":     []byte("---\n" + test.frontmatter + "---\n\n" + test.body),
			}
			migration := store.V01ToV02Migration{
				TimestampPolicy:   store.LegacyTimestampPreserve,
				TimestampConflict: store.TimestampConflictReject,
			}
			if strings.Contains(test.body, "# Citations") {
				migration.Citations = []store.DocumentCitationMigration{{
					Path: "a.md",
					Entries: []store.LegacyCitationMapping{{
						LegacyNumber: 1, LegacyEntry: "https://legacy.test", SourceID: "legacy",
					}},
				}}
			}
			request := MigrationRequest{
				ID: "merge-fallback", Actor: "operator",
				FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
				Migration: migration,
			}

			// Act.
			preview, err := NewMigrationPlanner().Preview(context.Background(), source, testMigrationResolution(t, source), request)

			// Assert.
			if err == nil {
				t.Fatalf("Preview() = %#v, want merge-aware blocker", preview)
			}
			found := false
			for _, blocker := range preview.Blockers {
				if blocker.Path == "a.md" && blocker.Code == test.wantCode {
					found = true
				}
				if blocker.Code == "missing_explicit_generated_by" {
					t.Fatalf("semantic generated presence was ignored: %#v", preview.Blockers)
				}
			}
			if !found {
				t.Fatalf("Blockers = %#v, want %s", preview.Blockers, test.wantCode)
			}
			if preview.Staged != nil || len(preview.Preview.Writes) != 0 {
				t.Fatalf("blocked merge preview exposed stage: %#v", preview)
			}
		})
	}
}

func TestMigrationPlannerPreview_AggregatesAllObservableDocumentFailuresDeterministically(t *testing.T) {
	build := func(reverse bool) memorySource {
		files := memorySource{
			"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [A](a.md)\n- [B](b.md)\n- [C](c.md)\n- [D](d.md)\n"),
		}
		values := []struct {
			path string
			data []byte
		}{
			{path: "a.md", data: []byte("---\ntype: [\n---\n")},
			{path: "b.md", data: append([]byte("---\ntype: Knowledge\n---\n"), 0xff)},
			{path: "c.md", data: []byte("---\ntype: Knowledge\n---\n\nClaim [1].\n\n# Citations\n\n[1] https://c.test\n")},
			{path: "d.md", data: []byte("---\ntype: Knowledge\ntimestamp: someday\n---\n")},
		}
		if reverse {
			for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
				values[left], values[right] = values[right], values[left]
			}
		}
		for _, value := range values {
			files[value.path] = value.data
		}
		return files
	}
	request := MigrationRequest{
		ID: "all-document-preflight", Actor: "operator",
		FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
		Migration: store.V01ToV02Migration{
			GeneratedBy:       "process:migration",
			TimestampPolicy:   store.LegacyTimestampPreserve,
			TimestampConflict: store.TimestampConflictReject,
		},
	}
	var baselineBlockers []MigrationBlocker
	var baselineActions []MigrationManualAction
	for _, reverse := range []bool{false, true} {
		// Act.
		preview, err := NewMigrationPlanner().Preview(context.Background(), build(reverse), testMigrationResolution(t, build(reverse)), request)

		// Assert.
		if err == nil {
			t.Fatal("Preview() error = nil, want aggregated failures")
		}
		want := []struct{ path, code string }{
			{path: "a.md", code: "yaml_syntax"},
			{path: "b.md", code: "invalid_utf8"},
			{path: "c.md", code: "missing_explicit_citation_mapping"},
			{path: "d.md", code: "invalid_legacy_timestamp"},
		}
		if len(preview.Blockers) != len(want) {
			t.Fatalf("Blockers = %#v", preview.Blockers)
		}
		for index, expected := range want {
			if preview.Blockers[index].Path != expected.path || preview.Blockers[index].Code != expected.code {
				t.Fatalf("Blockers = %#v, want deterministic all-document order", preview.Blockers)
			}
		}
		if preview.Staged != nil || len(preview.Preview.Writes) != 0 {
			t.Fatalf("blocked preview exposed stage: %#v", preview)
		}
		if baselineBlockers == nil {
			baselineBlockers = preview.Blockers
			baselineActions = preview.ManualActions
			continue
		}
		if !equalMigrationBlockerSlices(baselineBlockers, preview.Blockers) ||
			!equalMigrationActionSlices(baselineActions, preview.ManualActions) {
			t.Fatalf("path insertion order changed findings:\n%#v\n%#v", baselineBlockers, preview.Blockers)
		}
	}
}

func TestMigrationPlannerPreview_MappedDocumentWithoutLegacyStillRunsAllIndependentPreflights(t *testing.T) {
	tests := []struct {
		name       string
		files      memorySource
		want       []struct{ path, code string }
		wantAction []struct{ path, code string }
	}{
		{
			name: "independent failing path",
			files: memorySource{
				"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [A](a.md)\n- [B](b.md)\n"),
				"a.md":     []byte("---\ntype: Knowledge\n---\n\nNo legacy citations.\n"),
				"b.md":     []byte("---\ntype: Knowledge\ntimestamp: someday\n---\n"),
			},
			want: []struct{ path, code string }{
				{path: "a.md", code: "incomplete_citation_mapping"},
				{path: "b.md", code: "invalid_legacy_timestamp"},
			},
			wantAction: []struct{ path, code string }{
				{path: "a.md", code: "provide_citation_mapping"},
			},
		},
		{
			name: "same document timestamp failure",
			files: memorySource{
				"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [A](a.md)\n"),
				"a.md":     []byte("---\ntype: Knowledge\ntimestamp: someday\n---\n\nNo legacy citations.\n"),
			},
			want: []struct{ path, code string }{
				{path: "a.md", code: "incomplete_citation_mapping"},
				{path: "a.md", code: "invalid_legacy_timestamp"},
			},
			wantAction: []struct{ path, code string }{
				{path: "a.md", code: "provide_citation_mapping"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var baselineBlockers []MigrationBlocker
			var baselineActions []MigrationManualAction
			for _, reverse := range []bool{false, true} {
				// Arrange.
				source := memorySource{}
				paths := make([]string, 0, len(test.files))
				for path := range test.files {
					paths = append(paths, path)
				}
				sort.Strings(paths)
				if reverse {
					for left, right := 0, len(paths)-1; left < right; left, right = left+1, right-1 {
						paths[left], paths[right] = paths[right], paths[left]
					}
				}
				for _, path := range paths {
					source[path] = test.files[path]
				}
				request := MigrationRequest{
					ID:    "mapped-no-legacy-" + store.ChangeSetID(strings.ReplaceAll(test.name, " ", "-")),
					Actor: "operator", FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
					Migration: store.V01ToV02Migration{
						GeneratedBy:       "process:migration",
						TimestampPolicy:   store.LegacyTimestampPreserve,
						TimestampConflict: store.TimestampConflictReject,
						Citations: []store.DocumentCitationMigration{{
							Path: "a.md",
							Entries: []store.LegacyCitationMapping{{
								LegacyNumber: 1, SourceID: "source", Resource: "https://example.test",
							}},
						}},
					},
				}

				// Act.
				preview, err := NewMigrationPlanner().Preview(context.Background(), source, testMigrationResolution(t, source), request)

				// Assert.
				if err == nil {
					t.Fatalf("Preview(reverse=%t) error = nil", reverse)
				}
				if len(preview.Blockers) != len(test.want) {
					t.Fatalf("Blockers = %#v", preview.Blockers)
				}
				for index, want := range test.want {
					if preview.Blockers[index].Path != want.path || preview.Blockers[index].Code != want.code {
						t.Fatalf("Blockers = %#v", preview.Blockers)
					}
				}
				if len(preview.ManualActions) != len(test.wantAction) {
					t.Fatalf("ManualActions = %#v", preview.ManualActions)
				}
				for index, want := range test.wantAction {
					if preview.ManualActions[index].Path != want.path || preview.ManualActions[index].Code != want.code {
						t.Fatalf("ManualActions = %#v", preview.ManualActions)
					}
				}
				if preview.Staged != nil || len(preview.Preview.Writes) != 0 {
					t.Fatalf("blocked preview exposed stage: %#v", preview)
				}
				if baselineBlockers == nil {
					baselineBlockers = preview.Blockers
					baselineActions = preview.ManualActions
				} else if !equalMigrationBlockerSlices(baselineBlockers, preview.Blockers) ||
					!equalMigrationActionSlices(baselineActions, preview.ManualActions) {
					t.Fatalf("insertion order changed findings:\n%#v\n%#v", baselineBlockers, preview.Blockers)
				}
			}
		})
	}
}

func TestMigrationPlannerPreview_UsesUnionOfBundleAndExplicitMigrationPaths(t *testing.T) {
	// Arrange.
	source := memorySource{
		"index.md": []byte("---\nokf_version: \"0.1\"\n---\n"),
	}
	baseRequest := MigrationRequest{
		ID: "explicit-path-union", Actor: "operator",
		FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
		Migration: store.V01ToV02Migration{
			GeneratedBy:       "process:migration",
			TimestampPolicy:   store.LegacyTimestampPreserve,
			TimestampConflict: store.TimestampConflictReject,
			Citations: []store.DocumentCitationMigration{{
				Path: "missing-a.md",
				Entries: []store.LegacyCitationMapping{{
					LegacyNumber: 1, SourceID: "source", Resource: "https://example.test",
				}},
			}, {
				Path: "missing-z.md",
				Entries: []store.LegacyCitationMapping{{
					LegacyNumber: 1, SourceID: "source-z", Resource: "https://example.test/z",
				}},
			}},
			GeneratedAt: []store.MigrationGeneratedAt{
				{Path: "missing-b.md", At: "2026-06-25T09:00:00Z"},
				{Path: "missing-y.md", At: "2026-06-25T09:00:00Z"},
			},
			Computations: []store.ComputationMigration{{
				Path: "missing-a.md",
				Contract: store.AttestedComputationContract{
					Mode:              store.AttestedComputationModeInline,
					Runtime:           "sql",
					InlineComputation: "SELECT 0;\n",
					InlineLanguage:    "sql",
					Executor:          store.ExecutorContract{Resource: "executors/sql.md", Receipt: []string{"rows"}},
					Attester:          store.AttesterContract{Resource: "attesters/sql.md"},
				},
			}, {
				Path: "missing-c.md",
				Contract: store.AttestedComputationContract{
					Mode:              store.AttestedComputationModeInline,
					Runtime:           "sql",
					InlineComputation: "SELECT 1;\n",
					InlineLanguage:    "sql",
					Executor:          store.ExecutorContract{Resource: "executors/sql.md", Receipt: []string{"rows"}},
					Attester:          store.AttesterContract{Resource: "attesters/sql.md"},
				},
			}, {
				Path: "missing-x.md",
				Contract: store.AttestedComputationContract{
					Mode:              store.AttestedComputationModeInline,
					Runtime:           "sql",
					InlineComputation: "SELECT 2;\n",
					InlineLanguage:    "sql",
					Executor:          store.ExecutorContract{Resource: "executors/sql.md", Receipt: []string{"rows"}},
					Attester:          store.AttesterContract{Resource: "attesters/sql.md"},
				},
			}},
		},
	}

	want := []struct{ path, code string }{
		{path: "missing-a.md", code: "migration_document_missing"},
		{path: "missing-b.md", code: "migration_document_missing"},
		{path: "missing-c.md", code: "migration_document_missing"},
		{path: "missing-x.md", code: "migration_document_missing"},
		{path: "missing-y.md", code: "migration_document_missing"},
		{path: "missing-z.md", code: "migration_document_missing"},
	}
	var baselineBlockers []MigrationBlocker
	var baselineActions []MigrationManualAction
	for _, reverse := range []bool{false, true} {
		request := baseRequest
		if reverse {
			request.Migration.Citations = append([]store.DocumentCitationMigration(nil), baseRequest.Migration.Citations...)
			request.Migration.GeneratedAt = append([]store.MigrationGeneratedAt(nil), baseRequest.Migration.GeneratedAt...)
			request.Migration.Computations = append([]store.ComputationMigration(nil), baseRequest.Migration.Computations...)
			slices.Reverse(request.Migration.Citations)
			slices.Reverse(request.Migration.GeneratedAt)
			slices.Reverse(request.Migration.Computations)
		}

		// Act.
		preview, err := NewMigrationPlanner().Preview(context.Background(), source, testMigrationResolution(t, source), request)

		// Assert.
		if err == nil {
			t.Fatal("Preview() error = nil")
		}
		if len(preview.Blockers) != len(want) {
			t.Fatalf("Blockers = %#v", preview.Blockers)
		}
		for index, expected := range want {
			if preview.Blockers[index].Path != expected.path || preview.Blockers[index].Code != expected.code {
				t.Fatalf("Blockers = %#v", preview.Blockers)
			}
			wantMessage := fmt.Sprintf(
				`bundle presentation migration_document_missing: unsupported presentation: migration document %q does not exist`,
				expected.path,
			)
			if preview.Blockers[index].Message != wantMessage {
				t.Fatalf("Blocker message = %q, want %q", preview.Blockers[index].Message, wantMessage)
			}
		}
		if len(preview.ManualActions) != 0 {
			t.Fatalf("ManualActions = %#v, want none", preview.ManualActions)
		}
		if preview.Staged != nil || len(preview.Preview.Writes) != 0 {
			t.Fatalf("blocked preview exposed stage: %#v", preview)
		}
		if baselineBlockers == nil {
			baselineBlockers = preview.Blockers
			baselineActions = preview.ManualActions
		} else if !equalMigrationBlockerSlices(baselineBlockers, preview.Blockers) ||
			!equalMigrationActionSlices(baselineActions, preview.ManualActions) {
			t.Fatalf("explicit path order changed findings:\n%#v\n%#v", baselineBlockers, preview.Blockers)
		}
	}
}

func TestMigrationPlannerPreview_AggregatesCitationAndComputationErrorsOnSameDocument(t *testing.T) {
	// Arrange.
	source := memorySource{
		"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Computations\n\n- [Query](query.md)\n"),
		"query.md": []byte("---\ntype: Knowledge\n---\n\n" +
			"No legacy citations.\n\n# Computation\n\n```sql\nSELECT 1;\n"),
	}
	request := MigrationRequest{
		ID: "same-document-citation-computation", Actor: "operator",
		FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
		Migration: store.V01ToV02Migration{
			GeneratedBy:       "process:migration",
			GeneratedAt:       []store.MigrationGeneratedAt{{Path: "query.md", At: "2026-06-25T09:00:00Z"}},
			TimestampPolicy:   store.LegacyTimestampPreserve,
			TimestampConflict: store.TimestampConflictReject,
			Citations: []store.DocumentCitationMigration{{
				Path: "query.md",
				Entries: []store.LegacyCitationMapping{{
					LegacyNumber: 1, SourceID: "source", Resource: "https://example.test",
				}},
			}},
			Computations: []store.ComputationMigration{{
				Path: "query.md",
				Contract: store.AttestedComputationContract{
					Mode:              store.AttestedComputationModeInline,
					Runtime:           "sql",
					InlineComputation: "SELECT 1;\n",
					InlineLanguage:    "sql",
					Executor:          store.ExecutorContract{Resource: "executors/sql.md", Receipt: []string{"rows"}},
					Attester:          store.AttesterContract{Resource: "attesters/sql.md"},
				},
			}},
		},
	}

	// Act.
	preview, err := NewMigrationPlanner().Preview(context.Background(), source, testMigrationResolution(t, source), request)

	// Assert.
	if err == nil {
		t.Fatal("Preview() error = nil")
	}
	want := []string{"ambiguous_computation_boundary", "incomplete_citation_mapping"}
	if len(preview.Blockers) != len(want) {
		t.Fatalf("Blockers = %#v", preview.Blockers)
	}
	for index, code := range want {
		if preview.Blockers[index].Path != "query.md" || preview.Blockers[index].Code != code {
			t.Fatalf("Blockers = %#v", preview.Blockers)
		}
	}
	if len(preview.ManualActions) != 1 ||
		preview.ManualActions[0].Code != "provide_citation_mapping" {
		t.Fatalf("ManualActions = %#v", preview.ManualActions)
	}
	if preview.Staged != nil || len(preview.Preview.Writes) != 0 {
		t.Fatalf("blocked preview exposed stage: %#v", preview)
	}
}

func TestMigrationPlannerPreview_InheritedTimestampFailsClosed(t *testing.T) {
	tests := []struct {
		name        string
		frontmatter string
	}{
		{
			name: "merged mapping donor",
			frontmatter: "type: Knowledge\n" +
				"donor: &donor\n  timestamp: 2026-06-25T09:00:00Z\n" +
				"<<: *donor\n",
		},
		{
			name: "aliased scalar donor",
			frontmatter: "type: Knowledge\n" +
				"donor_timestamp: &donor_timestamp 2026-06-25T09:00:00Z\n" +
				"timestamp: *donor_timestamp\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			source := memorySource{
				"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [A](a.md)\n"),
				"a.md":     []byte("---\n" + test.frontmatter + "---\n"),
			}
			request := MigrationRequest{
				ID:    "inherited-timestamp-" + store.ChangeSetID(strings.ReplaceAll(test.name, " ", "-")),
				Actor: "operator", FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
				Migration: store.V01ToV02Migration{
					GeneratedBy:       "process:migration",
					TimestampPolicy:   store.LegacyTimestampRemoveAfterCopy,
					TimestampConflict: store.TimestampConflictReject,
				},
			}

			// Act.
			preview, err := NewMigrationPlanner().Preview(context.Background(), source, testMigrationResolution(t, source), request)

			// Assert.
			if err == nil {
				t.Fatalf("Preview() = %#v, want donor provenance blocker", preview)
			}
			if len(preview.Blockers) != 1 ||
				preview.Blockers[0].Path != "a.md" ||
				preview.Blockers[0].Code != "alias_provenance" ||
				preview.Blockers[0].Location.End <= preview.Blockers[0].Location.Start {
				t.Fatalf("Blockers = %#v", preview.Blockers)
			}
			if preview.Staged != nil || len(preview.Preview.Writes) != 0 {
				t.Fatalf("blocked donor preview exposed stage: %#v", preview)
			}
		})
	}
}

func TestMigrationPlannerPreview_RootPresentationFailuresAreStructured(t *testing.T) {
	tests := []struct {
		name       string
		root       []byte
		wantCode   string
		wantAction string
	}{
		{
			name:       "invalid UTF-8",
			root:       append([]byte("---\nokf_version: \"0.1\"\n---\n"), 0xff),
			wantCode:   "invalid_utf8",
			wantAction: "repair_invalid_utf8",
		},
		{
			name:     "invalid frontmatter",
			root:     []byte("---\nokf_version: [\n---\n"),
			wantCode: "yaml_syntax",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			source := memorySource{"index.md": test.root}
			request := MigrationRequest{
				ID:    "root-presentation-" + store.ChangeSetID(strings.ReplaceAll(test.name, " ", "-")),
				Actor: "operator", FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
				Migration: store.V01ToV02Migration{
					GeneratedBy:       "process:migration",
					TimestampPolicy:   store.LegacyTimestampPreserve,
					TimestampConflict: store.TimestampConflictReject,
				},
			}

			// Act.
			preview, err := NewMigrationPlanner().Preview(context.Background(), source, testUnresolvedMigrationResolution(t, source), request)

			// Assert.
			if err == nil {
				t.Fatalf("Preview() = %#v, want structured root blocker", preview)
			}
			if len(preview.Blockers) != 1 ||
				preview.Blockers[0].Path != "index.md" ||
				preview.Blockers[0].Code != test.wantCode ||
				preview.Blockers[0].Location.End <= preview.Blockers[0].Location.Start {
				t.Fatalf("Blockers = %#v", preview.Blockers)
			}
			if test.wantAction == "" {
				if len(preview.ManualActions) != 0 {
					t.Fatalf("ManualActions = %#v", preview.ManualActions)
				}
			} else if len(preview.ManualActions) != 1 ||
				preview.ManualActions[0].Path != "index.md" ||
				preview.ManualActions[0].Code != test.wantAction {
				t.Fatalf("ManualActions = %#v", preview.ManualActions)
			}
			if preview.Staged != nil || len(preview.Preview.Writes) != 0 {
				t.Fatalf("blocked root preview exposed stage: %#v", preview)
			}
		})
	}
}

func TestMigrationPlannerPreview_RejectsDuplicateMigrationPathsBeforePreflight(t *testing.T) {
	validCitation := func(path, sourceID string) store.DocumentCitationMigration {
		return store.DocumentCitationMigration{
			Path: path,
			Entries: []store.LegacyCitationMapping{{
				LegacyNumber: 1, SourceID: sourceID, Resource: "https://example.test/" + sourceID,
			}},
		}
	}
	validComputation := func(path, sql string) store.ComputationMigration {
		return store.ComputationMigration{
			Path: path,
			Contract: store.AttestedComputationContract{
				Mode:              store.AttestedComputationModeInline,
				Runtime:           "sql",
				InlineComputation: sql,
				InlineLanguage:    "sql",
				Executor:          store.ExecutorContract{Resource: "executors/sql.md", Receipt: []string{"rows"}},
				Attester:          store.AttesterContract{Resource: "attesters/sql.md"},
			},
		}
	}
	tests := []struct {
		name       string
		migrations [2]store.V01ToV02Migration
	}{
		{
			name: "generated",
			migrations: [2]store.V01ToV02Migration{
				{GeneratedAt: []store.MigrationGeneratedAt{
					{Path: "same.md", At: "2026-06-25T09:00:00Z"},
					{Path: "same.md", At: "2026-06-26T09:00:00Z"},
				}},
				{GeneratedAt: []store.MigrationGeneratedAt{
					{Path: "same.md", At: "2026-06-26T09:00:00Z"},
					{Path: "same.md", At: "2026-06-25T09:00:00Z"},
				}},
			},
		},
		{
			name: "citations",
			migrations: [2]store.V01ToV02Migration{
				{Citations: []store.DocumentCitationMigration{
					validCitation("same.md", "one"), validCitation("same.md", "two"),
				}},
				{Citations: []store.DocumentCitationMigration{
					validCitation("same.md", "two"), validCitation("same.md", "one"),
				}},
			},
		},
		{
			name: "computations",
			migrations: [2]store.V01ToV02Migration{
				{Computations: []store.ComputationMigration{
					validComputation("same.md", "SELECT 1;\n"), validComputation("same.md", "SELECT 2;\n"),
				}},
				{Computations: []store.ComputationMigration{
					validComputation("same.md", "SELECT 2;\n"), validComputation("same.md", "SELECT 1;\n"),
				}},
			},
		},
		{
			name: "mixed duplicate classes",
			migrations: [2]store.V01ToV02Migration{
				{
					GeneratedAt: []store.MigrationGeneratedAt{
						{Path: "generated.md", At: "2026-06-25T09:00:00Z"},
						{Path: "generated.md", At: "2026-06-26T09:00:00Z"},
					},
					Citations: []store.DocumentCitationMigration{
						validCitation("citations.md", "one"), validCitation("citations.md", "two"),
					},
					Computations: []store.ComputationMigration{
						validComputation("computation.md", "SELECT 1;\n"),
						validComputation("computation.md", "SELECT 2;\n"),
					},
				},
				{
					GeneratedAt: []store.MigrationGeneratedAt{
						{Path: "generated.md", At: "2026-06-26T09:00:00Z"},
						{Path: "generated.md", At: "2026-06-25T09:00:00Z"},
					},
					Citations: []store.DocumentCitationMigration{
						validCitation("citations.md", "two"), validCitation("citations.md", "one"),
					},
					Computations: []store.ComputationMigration{
						validComputation("computation.md", "SELECT 2;\n"),
						validComputation("computation.md", "SELECT 1;\n"),
					},
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var baseline string
			for index, migration := range test.migrations {
				// Arrange.
				migration.GeneratedBy = "process:migration"
				migration.TimestampPolicy = store.LegacyTimestampPreserve
				migration.TimestampConflict = store.TimestampConflictReject
				request := MigrationRequest{
					ID: "duplicate-" + store.ChangeSetID(test.name), Actor: "operator",
					FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
					Migration: migration,
				}
				source := memorySource{
					"index.md": []byte("---\nokf_version: \"0.1\"\n---\n"),
					"a.md":     []byte("# Citations\n\n[1] https://preflight.test\n"),
				}

				// Act.
				preview, err := NewMigrationPlanner().Preview(context.Background(), source, testMigrationResolution(t, source), request)

				// Assert.
				if !errors.Is(err, store.ErrInvalidChangeSet) {
					t.Fatalf("Preview(%d) error = %v, want ErrInvalidChangeSet", index, err)
				}
				if !reflect.DeepEqual(preview, MigrationPreview{}) {
					t.Fatalf("Preview(%d) = %#v, want zero preview", index, preview)
				}
				if index == 0 {
					baseline = err.Error()
				} else if err.Error() != baseline {
					t.Fatalf("duplicate order changed error:\n%s\n%s", baseline, err)
				}
			}
		})
	}
}

func TestValidateV01ToV02MigrationInput_TargetNoopParity(t *testing.T) {
	validContract := func(sql string) store.AttestedComputationContract {
		return store.AttestedComputationContract{
			Mode:              store.AttestedComputationModeInline,
			Runtime:           "sql",
			InlineComputation: sql,
			InlineLanguage:    "sql",
			Executor:          store.ExecutorContract{Resource: "executors/sql.md", Receipt: []string{"rows"}},
			Attester:          store.AttesterContract{Resource: "attesters/sql.md"},
		}
	}
	validFileComputation := func(path, assetPath string) store.ComputationMigration {
		return store.ComputationMigration{
			Path: path,
			Contract: store.AttestedComputationContract{
				Mode:            store.AttestedComputationModeFile,
				Runtime:         "sql",
				ComputationPath: assetPath,
				Executor:        store.ExecutorContract{Resource: "executors/sql.md", Receipt: []string{"rows"}},
				Attester:        store.AttesterContract{Resource: "attesters/sql.md"},
			},
			Asset: &store.MigrationAsset{Path: assetPath, Content: []byte("SELECT 1;\n")},
		}
	}
	base := func() store.V01ToV02Migration {
		return store.V01ToV02Migration{
			TimestampPolicy:   store.LegacyTimestampPreserve,
			TimestampConflict: store.TimestampConflictReject,
		}
	}

	t.Run("valid supplied empty input needs no producer", func(t *testing.T) {
		// Arrange.
		input := base()

		// Act.
		err := ValidateV01ToV02MigrationInput(input)

		// Assert.
		if err != nil {
			t.Fatalf("ValidateV01ToV02MigrationInput() error = %v", err)
		}
	})

	t.Run("valid supplied empty target noop stays read only", func(t *testing.T) {
		// Arrange.
		input := base()
		source := memorySource{
			"index.md": []byte("---\nokf_version: \"0.2\"\n---\n"),
		}
		before := cloneMemorySource(source)

		// Act.
		validationErr := ValidateV01ToV02MigrationInput(input)
		resolution, resolutionErr := ResolveMigrationSource(
			context.Background(),
			source,
			MigrationSourceOptions{RequestedSelector: MigrationSelectorAuto},
		)

		// Assert.
		if validationErr != nil || resolutionErr != nil {
			t.Fatalf("validation/resolution errors = %v, %v", validationErr, resolutionErr)
		}
		if resolution.Transition != MigrationTransitionTargetNoop ||
			resolution.ResolvedSource != MigrationVersionV02 {
			t.Fatalf("resolution = %#v", resolution)
		}
		if !equalMemorySource(source, before) {
			t.Fatal("target-noop validation/resolution mutated source")
		}
	})

	t.Run("valid canonicalization does not mutate caller input", func(t *testing.T) {
		// Arrange.
		input := base()
		input.GeneratedAt = []store.MigrationGeneratedAt{
			{Path: "z.md", At: "2026-06-26T09:00:00Z"},
			{Path: "a.md", At: "2026-06-25T09:00:00Z"},
		}
		input.Citations = []store.DocumentCitationMigration{
			{
				Path: "z.md",
				Entries: []store.LegacyCitationMapping{{
					LegacyNumber: 2, SourceID: "two", Resource: "https://example.test/two",
				}, {
					LegacyNumber: 1, SourceID: "one", Resource: "https://example.test/one",
				}},
			},
			{
				Path: "a.md",
				Entries: []store.LegacyCitationMapping{{
					LegacyEntry: "https://example.test/a", SourceID: "a", Resource: "https://example.test/a",
				}},
			},
		}
		input.Computations = []store.ComputationMigration{
			{Path: "z.md", Contract: validContract("SELECT 2;\n")},
			{Path: "a.md", Contract: validContract("SELECT 1;\n")},
		}
		before := store.V01ToV02Migration{
			TimestampPolicy:   input.TimestampPolicy,
			TimestampConflict: input.TimestampConflict,
			GeneratedAt:       append([]store.MigrationGeneratedAt(nil), input.GeneratedAt...),
			Citations:         make([]store.DocumentCitationMigration, len(input.Citations)),
			Computations:      append([]store.ComputationMigration(nil), input.Computations...),
		}
		for index, citation := range input.Citations {
			before.Citations[index] = store.DocumentCitationMigration{
				Path: citation.Path, Entries: append([]store.LegacyCitationMapping(nil), citation.Entries...),
			}
		}

		// Act.
		err := ValidateV01ToV02MigrationInput(input)

		// Assert.
		if err != nil {
			t.Fatalf("ValidateV01ToV02MigrationInput() error = %v", err)
		}
		if !reflect.DeepEqual(input, before) {
			t.Fatalf("validation mutated input:\nbefore=%#v\nafter=%#v", before, input)
		}
	})

	invalid := []struct {
		name  string
		input store.V01ToV02Migration
	}{
		{
			name: "generated traversal",
			input: func() store.V01ToV02Migration {
				value := base()
				value.GeneratedAt = []store.MigrationGeneratedAt{{
					Path: "../escape.md", At: "2026-06-25T09:00:00Z",
				}}
				return value
			}(),
		},
		{
			name: "citation traversal",
			input: func() store.V01ToV02Migration {
				value := base()
				value.Citations = []store.DocumentCitationMigration{{
					Path: "../escape.md",
					Entries: []store.LegacyCitationMapping{{
						LegacyNumber: 1, SourceID: "one", Resource: "https://example.test",
					}},
				}}
				return value
			}(),
		},
		{
			name: "computation traversal",
			input: func() store.V01ToV02Migration {
				value := base()
				value.Computations = []store.ComputationMigration{{
					Path: "../escape.md", Contract: validContract("SELECT 1;\n"),
				}}
				return value
			}(),
		},
		{
			name: "citation has no selector",
			input: func() store.V01ToV02Migration {
				value := base()
				value.Citations = []store.DocumentCitationMigration{{
					Path: "a.md",
					Entries: []store.LegacyCitationMapping{{
						SourceID: "one", Resource: "https://example.test",
					}},
				}}
				return value
			}(),
		},
		{
			name: "duplicate citation selector",
			input: func() store.V01ToV02Migration {
				value := base()
				value.Citations = []store.DocumentCitationMigration{{
					Path: "a.md",
					Entries: []store.LegacyCitationMapping{
						{LegacyNumber: 1, SourceID: "one", Resource: "https://example.test/one"},
						{LegacyNumber: 1, SourceID: "two", Resource: "https://example.test/two"},
					},
				}}
				return value
			}(),
		},
		{
			name: "invalid generated timestamp",
			input: func() store.V01ToV02Migration {
				value := base()
				value.GeneratedAt = []store.MigrationGeneratedAt{{Path: "a.md", At: "someday"}}
				return value
			}(),
		},
		{
			name: "invalid generated producer",
			input: func() store.V01ToV02Migration {
				value := base()
				value.GeneratedBy = "not an actor"
				return value
			}(),
		},
		{
			name: "computation asset traversal",
			input: func() store.V01ToV02Migration {
				value := base()
				value.Computations = []store.ComputationMigration{
					validFileComputation("a.md", "../escape.sql"),
				}
				return value
			}(),
		},
		{
			name: "missing computation asset",
			input: func() store.V01ToV02Migration {
				value := base()
				computation := validFileComputation("a.md", "references/a.sql")
				computation.Asset = nil
				value.Computations = []store.ComputationMigration{computation}
				return value
			}(),
		},
		{
			name: "inline computation has asset",
			input: func() store.V01ToV02Migration {
				value := base()
				value.Computations = []store.ComputationMigration{{
					Path: "a.md", Contract: validContract("SELECT 1;\n"),
					Asset: &store.MigrationAsset{Path: "references/a.sql", Content: []byte("SELECT 1;\n")},
				}}
				return value
			}(),
		},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			// Act.
			err := ValidateV01ToV02MigrationInput(test.input)

			// Assert.
			if !errors.Is(err, store.ErrInvalidChangeSet) {
				t.Fatalf("ValidateV01ToV02MigrationInput() error = %v, want ErrInvalidChangeSet", err)
			}
		})
	}
}

func TestValidateV01ToV02MigrationInput_DuplicatePathsRejectDeterministically(t *testing.T) {
	validCitation := func(sourceID string) store.DocumentCitationMigration {
		return store.DocumentCitationMigration{
			Path: "same.md",
			Entries: []store.LegacyCitationMapping{{
				LegacyNumber: 1, SourceID: sourceID, Resource: "https://example.test/" + sourceID,
			}},
		}
	}
	validComputation := func(sql string) store.ComputationMigration {
		return store.ComputationMigration{
			Path: "same.md",
			Contract: store.AttestedComputationContract{
				Mode:              store.AttestedComputationModeInline,
				Runtime:           "sql",
				InlineComputation: sql,
				InlineLanguage:    "sql",
				Executor:          store.ExecutorContract{Resource: "executors/sql.md", Receipt: []string{"rows"}},
				Attester:          store.AttesterContract{Resource: "attesters/sql.md"},
			},
		}
	}
	tests := []struct {
		name     string
		variants [2]store.V01ToV02Migration
	}{
		{
			name: "generated",
			variants: [2]store.V01ToV02Migration{
				{GeneratedAt: []store.MigrationGeneratedAt{
					{Path: "same.md", At: "2026-06-25T09:00:00Z"},
					{Path: "same.md", At: "2026-06-26T09:00:00Z"},
				}},
				{GeneratedAt: []store.MigrationGeneratedAt{
					{Path: "same.md", At: "2026-06-26T09:00:00Z"},
					{Path: "same.md", At: "2026-06-25T09:00:00Z"},
				}},
			},
		},
		{
			name: "citations",
			variants: [2]store.V01ToV02Migration{
				{Citations: []store.DocumentCitationMigration{validCitation("one"), validCitation("two")}},
				{Citations: []store.DocumentCitationMigration{validCitation("two"), validCitation("one")}},
			},
		},
		{
			name: "computations",
			variants: [2]store.V01ToV02Migration{
				{Computations: []store.ComputationMigration{
					validComputation("SELECT 1;\n"), validComputation("SELECT 2;\n"),
				}},
				{Computations: []store.ComputationMigration{
					validComputation("SELECT 2;\n"), validComputation("SELECT 1;\n"),
				}},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var baseline string
			for index, input := range test.variants {
				input.TimestampPolicy = store.LegacyTimestampPreserve
				input.TimestampConflict = store.TimestampConflictReject

				// Act.
				err := ValidateV01ToV02MigrationInput(input)

				// Assert.
				if !errors.Is(err, store.ErrInvalidChangeSet) {
					t.Fatalf("variant %d error = %v, want ErrInvalidChangeSet", index, err)
				}
				if index == 0 {
					baseline = err.Error()
				} else if err.Error() != baseline {
					t.Fatalf("duplicate order changed error:\n%s\n%s", baseline, err)
				}
			}
		})
	}
}

func TestValidateV01ToV02MigrationInput_CitationPresentationParity(t *testing.T) {
	base := func(entries ...store.LegacyCitationMapping) store.V01ToV02Migration {
		return store.V01ToV02Migration{
			TimestampPolicy:   store.LegacyTimestampPreserve,
			TimestampConflict: store.TimestampConflictReject,
			Citations: []store.DocumentCitationMigration{{
				Path: "a.md", Entries: entries,
			}},
		}
	}
	tests := []struct {
		name     string
		input    store.V01ToV02Migration
		wantCode string
	}{
		{
			name: "bracketed source id",
			input: base(store.LegacyCitationMapping{
				LegacyNumber: 1, SourceID: "bad[id]", Resource: "https://example.test",
			}),
			wantCode: "invalid_source_id",
		},
		{
			name: "unrenderable title",
			input: base(store.LegacyCitationMapping{
				LegacyNumber: 1, SourceID: "source", Title: "[bad]", Resource: "https://example.test",
			}),
			wantCode: "unsupported_footnote_definition",
		},
		{
			name: "normalized label collision",
			input: base(
				store.LegacyCitationMapping{
					LegacyNumber: 2, SourceID: "Spec", Resource: "https://example.test/spec",
				},
				store.LegacyCitationMapping{
					LegacyNumber: 1, SourceID: "spec", Resource: "https://example.test/spec",
				},
			),
			wantCode: "normalized_footnote_label_collision",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Act.
			err := ValidateV01ToV02MigrationInput(test.input)

			// Assert.
			var presentation *PresentationError
			if !errors.As(err, &presentation) ||
				presentation.Code != test.wantCode ||
				presentation.Path != "a.md" ||
				presentation.Operation != "validate_v01_to_v02_migration_input" {
				t.Fatalf("error = %#v, want typed %s", err, test.wantCode)
			}
		})
	}

	t.Run("normalized IDs are scoped per document", func(t *testing.T) {
		// Arrange.
		input := store.V01ToV02Migration{
			TimestampPolicy:   store.LegacyTimestampPreserve,
			TimestampConflict: store.TimestampConflictReject,
			Citations: []store.DocumentCitationMigration{
				{
					Path: "a.md",
					Entries: []store.LegacyCitationMapping{{
						LegacyNumber: 1, SourceID: "Spec", Resource: "https://example.test/spec",
					}},
				},
				{
					Path: "b.md",
					Entries: []store.LegacyCitationMapping{{
						LegacyNumber: 1, SourceID: "spec", Resource: "https://example.test/spec",
					}},
				},
			},
		}

		// Act.
		err := ValidateV01ToV02MigrationInput(input)

		// Assert.
		if err != nil {
			t.Fatalf("ValidateV01ToV02MigrationInput() error = %v", err)
		}
	})

	t.Run("collision error is deterministic after canonicalization", func(t *testing.T) {
		// Arrange.
		entries := []store.LegacyCitationMapping{
			{LegacyNumber: 2, SourceID: "Spec", Resource: "https://example.test/spec"},
			{LegacyNumber: 1, SourceID: "spec", Resource: "https://example.test/spec"},
		}
		var baseline string
		for _, reverse := range []bool{false, true} {
			current := append([]store.LegacyCitationMapping(nil), entries...)
			if reverse {
				slices.Reverse(current)
			}

			// Act.
			err := ValidateV01ToV02MigrationInput(base(current...))

			// Assert.
			if err == nil {
				t.Fatal("ValidateV01ToV02MigrationInput() error = nil")
			}
			if baseline == "" {
				baseline = err.Error()
			} else if err.Error() != baseline {
				t.Fatalf("input order changed error:\n%s\n%s", baseline, err)
			}
		}
	})
}

func TestMigrationPlannerPreview_InvalidCitationPresentationRejectsBeforeSourceRead(t *testing.T) {
	// Arrange.
	request := MigrationRequest{
		ID: "invalid-citation-before-source", Actor: "operator",
		FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
		Migration: store.V01ToV02Migration{
			TimestampPolicy:   store.LegacyTimestampPreserve,
			TimestampConflict: store.TimestampConflictReject,
			Citations: []store.DocumentCitationMigration{{
				Path: "a.md",
				Entries: []store.LegacyCitationMapping{{
					LegacyNumber: 1, SourceID: "source", Title: "[bad]", Resource: "https://example.test",
				}},
			}},
		},
	}

	// Act.
	preview, err := NewMigrationPlanner().Preview(context.Background(), noReadSource{}, MigrationSourceResolution{RequestedSelector: MigrationSelectorAuto, ToVersion: MigrationVersionV02}, request)

	// Assert.
	var presentation *PresentationError
	if !errors.As(err, &presentation) ||
		presentation.Code != "unsupported_footnote_definition" ||
		presentation.Path != "a.md" {
		t.Fatalf("Preview() error = %#v", err)
	}
	if !reflect.DeepEqual(preview, MigrationPreview{}) {
		t.Fatalf("Preview() = %#v, want zero preview", preview)
	}
}

func TestMigrationPlannerPreview_CitationInputAmbiguityReturnsBlockedEnvelopeBeforeSourceRead(t *testing.T) {
	// Arrange.
	request := MigrationRequest{
		ID: "ambiguous-citation-before-source", Actor: "operator",
		FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
		Migration: store.V01ToV02Migration{
			TimestampPolicy:   store.LegacyTimestampPreserve,
			TimestampConflict: store.TimestampConflictReject,
			Citations: []store.DocumentCitationMigration{{
				Path: "a.md",
				Entries: []store.LegacyCitationMapping{
					{LegacyNumber: 1, SourceID: "Spec", Resource: "https://example.test/spec"},
					{LegacyNumber: 2, SourceID: "spec", Resource: "https://example.test/spec"},
				},
			}},
		},
	}

	// Act.
	preview, err := NewMigrationPlanner().Preview(context.Background(), noReadSource{}, MigrationSourceResolution{RequestedSelector: MigrationSelectorAuto, ToVersion: MigrationVersionV02}, request)

	// Assert.
	var presentation *PresentationError
	if !errors.Is(err, ErrAmbiguousPresentation) ||
		!errors.As(err, &presentation) ||
		presentation.Code != "normalized_footnote_label_collision" ||
		presentation.Path != "a.md" {
		t.Fatalf("Preview() error = %#v", err)
	}
	if preview.Staged != nil ||
		!reflect.DeepEqual(preview.Preview, store.Preview{}) ||
		len(preview.Blockers) != 1 ||
		preview.Blockers[0].Code != "normalized_footnote_label_collision" ||
		preview.Blockers[0].Path != "a.md" ||
		len(preview.ManualActions) != 1 ||
		preview.ManualActions[0].Code != "disambiguate_citation_entry" ||
		preview.ManualActions[0].Path != "a.md" {
		t.Fatalf("Preview() = %#v, want blocked ambiguity envelope", preview)
	}
}

func TestMigrationPlannerPreview_InvalidInputRejectsBeforeSourceRead(t *testing.T) {
	// Arrange.
	request := MigrationRequest{
		ID: "invalid-before-source", Actor: "operator",
		FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
		Migration: store.V01ToV02Migration{
			TimestampPolicy:   store.LegacyTimestampPreserve,
			TimestampConflict: store.TimestampConflictReject,
			GeneratedAt: []store.MigrationGeneratedAt{
				{Path: "same.md", At: "2026-06-25T09:00:00Z"},
				{Path: "same.md", At: "2026-06-26T09:00:00Z"},
			},
		},
	}

	// Act.
	preview, err := NewMigrationPlanner().Preview(context.Background(), noReadSource{}, MigrationSourceResolution{RequestedSelector: MigrationSelectorAuto, ToVersion: MigrationVersionV02}, request)

	// Assert.
	if !errors.Is(err, store.ErrInvalidChangeSet) {
		t.Fatalf("Preview() error = %v, want ErrInvalidChangeSet", err)
	}
	if !reflect.DeepEqual(preview, MigrationPreview{}) {
		t.Fatalf("Preview() = %#v, want zero preview", preview)
	}
}

func TestMigrationPlannerPreview_MarkerOnlyDocumentMigratesVersionAndReplays(t *testing.T) {
	// Arrange.
	source := memorySource{
		"index.md": []byte("---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [A](a.md)\n"),
		"a.md":     []byte("---\ntype: Knowledge\n---\n\nA numbered claim [1] remains prose.\n"),
	}
	request := MigrationRequest{
		ID: "marker-only-version-migration", Actor: "operator",
		FromVersion: MigrationVersionV01, ToVersion: MigrationVersionV02,
		Migration: store.V01ToV02Migration{
			TimestampPolicy:   store.LegacyTimestampPreserve,
			TimestampConflict: store.TimestampConflictReject,
		},
	}
	planner := NewMigrationPlanner()

	// Act.
	preview, err := planner.Preview(context.Background(), source, testMigrationResolution(t, source), request)

	// Assert.
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	updated, err := preview.Staged.ReadFile(context.Background(), "a.md")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(updated, source["a.md"]) || !bytes.Contains(updated, []byte("[1]")) {
		t.Fatalf("marker-only document changed:\n%s", updated)
	}
	replay, err := planner.Preview(context.Background(), preview.Staged, testMigrationResolution(t, preview.Staged), request)
	if err != nil {
		t.Fatalf("replay Preview() error = %v", err)
	}
	if replay.Staged == nil ||
		len(replay.Preview.Writes) != 0 ||
		replay.Preview.BaseRevision != replay.Preview.ResultRevision {
		t.Fatalf("replay = %#v, want authenticated noop", replay)
	}
}

func TestPreflightV01ToV02MigrationInput_TargetNoopFindings(t *testing.T) {
	base := func() store.V01ToV02Migration {
		return store.V01ToV02Migration{
			TimestampPolicy:   store.LegacyTimestampPreserve,
			TimestampConflict: store.TimestampConflictReject,
		}
	}
	targetResolution := testDeclaredTargetResolutionV02()
	t.Run("marker-only target is valid and proofless", func(t *testing.T) {
		// Arrange.
		source := memorySource{
			"index.md": []byte("---\nokf_version: \"0.2\"\n---\n\n# Knowledge\n\n- [A](a.md)\n"),
			"a.md":     []byte("---\ntype: Knowledge\n---\n\nA numbered claim [1] remains prose.\n"),
		}

		// Act.
		got, err := PreflightV01ToV02MigrationInput(context.Background(), source, targetResolution, base())

		// Assert.
		if err != nil || len(got.Blockers) != 0 || len(got.ManualActions) != 0 {
			t.Fatalf("PreflightV01ToV02MigrationInput() = (%#v, %v)", got, err)
		}
	})

	t.Run("normalized existing label collision has exact span", func(t *testing.T) {
		// Arrange.
		source := memorySource{
			"index.md": []byte("---\nokf_version: \"0.2\"\n---\n\n# Knowledge\n\n- [A](a.md)\n"),
			"a.md": []byte("---\ntype: Knowledge\nsources:\n  - id: spec\n    resource: https://example.test/spec\n---\n\n" +
				"Claim [^Spec].\n\n[^Spec]: https://example.test/spec\n"),
		}
		input := base()
		input.Citations = []store.DocumentCitationMigration{{
			Path: "a.md",
			Entries: []store.LegacyCitationMapping{{
				LegacyNumber: 1, SourceID: "spec", Resource: "https://example.test/spec",
			}},
		}}
		wantStart := bytes.Index(source["a.md"], []byte("[^Spec]:"))

		// Act.
		got, err := PreflightV01ToV02MigrationInput(context.Background(), source, targetResolution, input)

		// Assert.
		if !errors.Is(err, ErrAmbiguousPresentation) ||
			len(got.Blockers) != 1 ||
			got.Blockers[0].Code != "normalized_footnote_label_collision" ||
			got.Blockers[0].Path != "a.md" ||
			got.Blockers[0].Location.Start != wantStart ||
			got.Blockers[0].Location.End <= got.Blockers[0].Location.Start ||
			len(got.ManualActions) != 1 ||
			got.ManualActions[0].Code != "disambiguate_citation_entry" {
			t.Fatalf("PreflightV01ToV02MigrationInput() = (%#v, %v)", got, err)
		}
	})

	t.Run("missing explicit path is deterministic", func(t *testing.T) {
		// Arrange.
		source := memorySource{
			"index.md": []byte("---\nokf_version: \"0.2\"\n---\n"),
		}
		input := base()
		input.GeneratedAt = []store.MigrationGeneratedAt{{
			Path: "missing.md", At: "2026-06-25T09:00:00Z",
		}}

		// Act.
		got, err := PreflightV01ToV02MigrationInput(context.Background(), source, targetResolution, input)

		// Assert.
		if err == nil ||
			len(got.Blockers) != 1 ||
			got.Blockers[0].Code != "migration_document_missing" ||
			got.Blockers[0].Path != "missing.md" {
			t.Fatalf("PreflightV01ToV02MigrationInput() = (%#v, %v)", got, err)
		}
	})

	for _, newline := range []struct{ name, value string }{
		{name: "LF", value: "\n"},
		{name: "CRLF", value: "\r\n"},
	} {
		t.Run("number-only reserved content conflict/"+newline.name, func(t *testing.T) {
			// Arrange.
			nl := newline.value
			source := memorySource{
				"index.md": []byte("---" + nl + "okf_version: \"0.2\"" + nl + "---" + nl),
				"log.md": []byte(
					"Claim [^source]." + nl + nl +
						"[^source]: https://invalid.test" + nl,
				),
			}
			input := base()
			input.Citations = []store.DocumentCitationMigration{{
				Path: "log.md",
				Entries: []store.LegacyCitationMapping{{
					LegacyNumber: 1, SourceID: "source", Resource: "https://example.test",
				}},
			}}
			wantStart := bytes.Index(source["log.md"], []byte("https://invalid.test"))

			// Act.
			got, err := PreflightV01ToV02MigrationInput(context.Background(), source, targetResolution, input)

			// Assert.
			if err == nil ||
				len(got.Blockers) != 1 ||
				got.Blockers[0].Code != "migration_replay_mismatch" ||
				got.Blockers[0].Location.Start != wantStart ||
				got.Blockers[0].Location.End != wantStart+len("https://invalid.test") {
				t.Fatalf("PreflightV01ToV02MigrationInput() = (%#v, %v)", got, err)
			}
		})
	}

	t.Run("computation contract conflict", func(t *testing.T) {
		// Arrange.
		source := memorySource{
			"index.md": []byte("---\nokf_version: \"0.2\"\n---\n\n# Knowledge\n\n- [Query](query.md)\n"),
			"query.md": []byte("---\ntype: Attested Computation\nruntime: sql\nparameters: []\nexecutor: {resource: executors/sql.md, receipt: [rows]}\n" +
				"attester: {resource: attesters/sql.md}\n---\n\n# Computation\n\n```sql\nSELECT 1;\n```\n"),
		}
		input := base()
		input.Computations = []store.ComputationMigration{{
			Path: "query.md",
			Contract: store.AttestedComputationContract{
				Mode:              store.AttestedComputationModeInline,
				Runtime:           "sql",
				InlineComputation: "SELECT 2;\n",
				InlineLanguage:    "sql",
				Executor:          store.ExecutorContract{Resource: "executors/sql.md", Receipt: []string{"rows"}},
				Attester:          store.AttesterContract{Resource: "attesters/sql.md"},
			},
		}}

		// Act.
		got, err := PreflightV01ToV02MigrationInput(context.Background(), source, targetResolution, input)

		// Assert.
		if err == nil || len(got.Blockers) == 0 || got.Blockers[0].Path != "query.md" {
			t.Fatalf("PreflightV01ToV02MigrationInput() = (%#v, %v)", got, err)
		}
		for _, blocker := range got.Blockers {
			if blocker.Code != "migration_replay_mismatch" &&
				blocker.Code != "sanctioned_computation_mismatch" {
				t.Fatalf("unexpected computation blocker: %#v", got.Blockers)
			}
		}
	})
}

func equalMigrationBlockerSlices(left, right []MigrationBlocker) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func equalMigrationActionSlices(left, right []MigrationManualAction) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func TestDedupeMigrationCauses_PreservesNULBearingErrorIdentity(t *testing.T) {
	// Arrange.
	first := errors.New("left\x00right")
	second := errors.New("left")

	// Act.
	got, err := dedupeMigrationCausesContext(context.Background(), []error{first, second, first})

	// Assert.
	if err != nil || len(got) != 2 || got[0] != first || got[1] != second {
		t.Fatalf("dedupeMigrationCausesContext() = %#v, %v; want two ordered identities", got, err)
	}
}
