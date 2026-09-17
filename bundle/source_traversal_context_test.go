package bundle

import (
	"context"
	"errors"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

func TestSourceTraversalLoadCancelsInsideBYOTPathCopyAndSortBeforeReads(t *testing.T) {
	large := strings.Repeat("p", 2<<20)
	source := &recordingSource{paths: []string{large + "b.bin", large + "a.bin"}, files: map[string][]byte{}}

	probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	_, probeErr := Load(probe, source)
	checks := probe.checks.Load()
	if probeErr != nil || checks < 64 {
		t.Fatalf("probe err=%v checks=%d", probeErr, checks)
	}
	source.reads.Store(0)

	got, gotErr := Load(&cancelAfterErrChecksContext{allowed: checks / 3}, source)
	if got != nil || !errors.Is(gotErr, context.Canceled) || source.reads.Load() != 0 {
		t.Fatalf("Load=(%v,%v), reads=%d, want nil/context.Canceled/0", got, gotErr, source.reads.Load())
	}
}

func TestSourceTraversalSourceFromFSCancelsInsideEntryNameSort(t *testing.T) {
	large := strings.Repeat("n", 2<<20)
	source := SourceFromFS(fstest.MapFS{
		large + "b.bin": &fstest.MapFile{Data: []byte("b"), Mode: fs.FileMode(0o600)},
		large + "a.bin": &fstest.MapFile{Data: []byte("a"), Mode: fs.FileMode(0o600)},
	})
	probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	baseline, probeErr := source.Paths(probe)
	checks := probe.checks.Load()
	if probeErr != nil || len(baseline) != 2 || checks < 32 {
		t.Fatalf("probe=(%d,%v), checks=%d", len(baseline), probeErr, checks)
	}
	got, gotErr := source.Paths(&cancelAfterErrChecksContext{allowed: checks - checks/3})
	if got != nil || !errors.Is(gotErr, context.Canceled) {
		t.Fatalf("Paths=(%v,%v), want nil/context.Canceled", got, gotErr)
	}
}

func TestSourceTraversalLinkOwnersCancelInsideBodyTargetAndIdentity(t *testing.T) {
	large := strings.Repeat("x", 2<<20)
	tests := []struct {
		name string
		act  func(context.Context) (any, error)
		zero any
	}{
		{name: "extract body", act: func(ctx context.Context) (any, error) { return ExtractLinksContext(ctx, "[label]("+large+".md)\n") }, zero: []Link(nil)},
		{name: "classify target", act: func(ctx context.Context) (any, error) { return ClassifyLinkContext(ctx, large) }, zero: LinkOther},
		{name: "resolve target", act: func(ctx context.Context) (any, error) {
			id, _, err := (Link{Kind: LinkRelative, Target: large + ".md"}).ResolveContext(ctx, ConceptID{segments: []string{"source"}})
			return id, err
		}, zero: ConceptID{}},
		{name: "identity dedupe", act: func(ctx context.Context) (any, error) {
			return containsConceptIDContext(ctx, []ConceptID{{segments: []string{large + "a"}}}, ConceptID{segments: []string{large + "b"}})
		}, zero: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
			_, probeErr := test.act(probe)
			checks := probe.checks.Load()
			if probeErr != nil || checks < 16 {
				t.Fatalf("probe err=%v checks=%d", probeErr, checks)
			}
			got, gotErr := test.act(&cancelAfterErrChecksContext{allowed: checks / 2})
			if !errors.Is(gotErr, context.Canceled) || !reflect.DeepEqual(got, test.zero) {
				t.Fatalf("cancel=(%#v,%v), want (%#v,context.Canceled)", got, gotErr, test.zero)
			}
		})
	}
}

func TestSourceTraversalLoadGraphCancellationReturnsNilAndStopsReads(t *testing.T) {
	large := strings.Repeat("segment", 256<<10)
	body := "---\ntype: Note\n---\n[large](" + large + ".md)\n"
	source := &recordingSource{paths: []string{"source.md", large + ".md"}, files: map[string][]byte{
		"source.md": []byte(body), large + ".md": []byte("---\ntype: Note\n---\n"),
	}}
	probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	baseline, probeErr := Load(probe, source)
	checks := probe.checks.Load()
	if probeErr != nil || baseline == nil || source.reads.Load() != 2 || checks < 128 {
		t.Fatalf("probe=(%v,%v), reads=%d checks=%d", baseline != nil, probeErr, source.reads.Load(), checks)
	}
	source.reads.Store(0)
	got, gotErr := Load(&cancelAfterErrChecksContext{allowed: checks - checks/4}, source)
	if got != nil || !errors.Is(gotErr, context.Canceled) || source.reads.Load() > 2 {
		t.Fatalf("Load=(%v,%v), reads=%d", got, gotErr, source.reads.Load())
	}
}

func TestSourceTraversalLinkContextBackgroundParity(t *testing.T) {
	body := "plain [one](nested/one.md) and [external](https://example.test)\n"
	legacy := ExtractLinks(body)
	got, err := ExtractLinksContext(context.Background(), body)
	if err != nil || !reflect.DeepEqual(got, legacy) {
		t.Fatalf("links=(%#v,%v), legacy=%#v", got, err, legacy)
	}
}
