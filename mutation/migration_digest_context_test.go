package mutation

import (
	"bytes"
	"context"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

type migrationDigestRecordingHash struct{ bytes.Buffer }

func (h *migrationDigestRecordingHash) Sum(prefix []byte) []byte {
	return append(prefix, h.Bytes()...)
}

func (h *migrationDigestRecordingHash) Reset()         { h.Buffer.Reset() }
func (h *migrationDigestRecordingHash) Size() int      { return h.Len() }
func (h *migrationDigestRecordingHash) BlockSize() int { return 1 }

type migrationDigestCheckpointContext struct {
	context.Context
	outer     string
	inner     string
	remaining int
	matches   int
}

func (c *migrationDigestCheckpointContext) Err() error {
	callers := make([]uintptr, 32)
	count := runtime.Callers(2, callers)
	frames := runtime.CallersFrames(callers[:count])
	foundOuter := false
	foundInner := false
	for {
		frame, more := frames.Next()
		foundOuter = foundOuter || strings.HasSuffix(frame.Function, c.outer)
		foundInner = foundInner || strings.HasSuffix(frame.Function, c.inner)
		if !more {
			break
		}
	}
	if foundOuter && foundInner {
		c.matches++
		if c.matches >= c.remaining {
			return context.Canceled
		}
	}
	return c.Context.Err()
}

func migrationDigestResolutionFixture() MigrationSourceResolution {
	return MigrationSourceResolution{
		RequestedSelector: MigrationSelectorAuto,
		DeclarationValid:  true,
		ResolvedSource:    MigrationVersionV01,
		ResolutionSource:  MigrationResolutionLegacyProbe,
		FromVersion:       MigrationVersionV01,
		ToVersion:         MigrationVersionV02,
		Transition:        MigrationTransitionV01ToV02,
		Candidates: []MigrationLegacyCandidate{
			{Kind: "citations", Path: "a.md", Location: SourceSpan{Start: 1, End: 4}},
			{Kind: "timestamp", Path: "b.md"},
		},
	}
}

func TestMigrationOwnedDigests_LegacyGoldenCompatibility(t *testing.T) {
	// Arrange.
	resolution := migrationDigestResolutionFixture()
	fixture := newMigrationCommitOutcomeFixture(t)
	change, err := migrationChangeSet(fixture.apply.Request, fixture.apply.Proof.BaseRevision)
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	resolutionDigest, resolutionErr := migrationSourceResolutionDigestContext(context.Background(), resolution)
	proofDigest, proofErr := migrationPlanProofDigestContext(context.Background(), change, fixture.apply.Proof, fixture.apply.Resolution)

	// Assert.
	if resolutionErr != nil || proofErr != nil {
		t.Fatalf("digests errors = %v / %v", resolutionErr, proofErr)
	}
	const wantResolution = "sha256:1ccd411758f6920fa964c7a4b004b6f9202f57a58e11b9de6048348827a50991"
	const wantProof = "sha256:158fd6d671c5523ecc846b4176ffc85c81fe0049be8c4b8977bc5d3fc76e3315"
	if resolutionDigest != wantResolution || proofDigest != wantProof {
		t.Fatalf("legacy digests = %q / %q", resolutionDigest, proofDigest)
	}
}

func TestContextPlanDigestEncoder_PreservesLegacyCanonicalBytes(t *testing.T) {
	// Arrange.
	values := []string{"", "okf:migration", "миграция", strings.Repeat("x", 2<<20)}
	legacy := planDigestEncoder{}
	recorded := &migrationDigestRecordingHash{}
	owned := &contextPlanDigestEncoder{ctx: context.Background(), hash: recorded}

	// Act.
	for index, value := range values {
		legacy.string(value)
		legacy.count(index)
		if err := owned.string(value); err != nil {
			t.Fatal(err)
		}
		if err := owned.count(index); err != nil {
			t.Fatal(err)
		}
	}

	// Assert.
	if !bytes.Equal(recorded.Bytes(), legacy.Bytes()) {
		t.Fatalf("canonical streams differ: context=%d legacy=%d bytes", recorded.Len(), legacy.Len())
	}
}

func TestMigrationDigestEncoding_LongFieldsAndFinalizationCancelToZero(t *testing.T) {
	longPath := strings.Repeat("a", 3<<20) + ".md"
	tests := []struct {
		name      string
		outer     string
		inner     string
		remaining int
		digest    func(context.Context) (string, error)
	}{
		{
			name: "resolution long field chunks", outer: "migrationSourceResolutionDigestContext",
			inner: ".writeBytes", remaining: 14,
			digest: func(ctx context.Context) (string, error) {
				resolution := migrationDigestResolutionFixture()
				resolution.Candidates[0].Path = longPath
				return migrationSourceResolutionDigestContext(ctx, resolution)
			},
		},
		{
			name: "resolution hash finalization", outer: "migrationSourceResolutionDigestContext",
			inner: ".digest", remaining: 2,
			digest: func(ctx context.Context) (string, error) {
				return migrationSourceResolutionDigestContext(ctx, migrationDigestResolutionFixture())
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			ctx := &migrationDigestCheckpointContext{
				Context: context.Background(), outer: test.outer, inner: test.inner, remaining: test.remaining,
			}

			// Act.
			got, err := test.digest(ctx)

			// Assert.
			if err != context.Canceled || got != "" || ctx.matches < test.remaining {
				t.Fatalf("digest = %q, %v; matches=%d", got, err, ctx.matches)
			}
		})
	}
}

func TestMigrationPlanProofDigest_LongRelationFieldEncodingCancelsToZero(t *testing.T) {
	// Arrange.
	fixture := newMigrationCommitOutcomeFixture(t)
	change, err := migrationChangeSet(fixture.apply.Request, fixture.apply.Proof.BaseRevision)
	if err != nil {
		t.Fatal(err)
	}
	proof := fixture.apply.Proof.Clone()
	proof.AffectedRefs[0].Fragment = strings.Repeat("fragment", 512<<10)
	ctx := &migrationDigestCheckpointContext{
		Context: context.Background(), outer: "encodeProofRefsContext", inner: ".writeBytes", remaining: 4,
	}

	// Act.
	digest, err := migrationPlanProofDigestContext(ctx, change, proof, fixture.apply.Resolution)

	// Assert.
	if err != context.Canceled || digest != "" || ctx.matches < ctx.remaining {
		t.Fatalf("migrationPlanProofDigestContext() = %q, %v; matches=%d", digest, err, ctx.matches)
	}
}

func TestMigrationPlannerPreview_DigestCancellationPublishesExactZero(t *testing.T) {
	// Arrange.
	fixture := newMigrationCommitOutcomeFixture(t)
	ctx := &migrationDigestCheckpointContext{
		Context: context.Background(), outer: "migrationSourceResolutionDigestContext",
		inner: ".writeBytes", remaining: 3,
	}

	// Act.
	preview, err := fixture.planner.Preview(
		ctx, fixture.snapshot.memorySource, fixture.apply.Resolution, fixture.apply.Request,
	)

	// Assert.
	if err != context.Canceled || !reflect.DeepEqual(preview, MigrationPreview{}) {
		t.Fatalf("Preview() = (%#v, %v), want exact zero/canceled", preview, err)
	}
}
