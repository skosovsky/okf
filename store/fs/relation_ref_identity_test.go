package fs

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
	"github.com/skosovsky/okf/validator"
)

func TestValidationReplayRoundTripsEscapedRootRelationRefsStructurally(t *testing.T) {
	// Arrange.
	rootBang, err := bundle.ParseRelationRef("a!")
	if err != nil {
		t.Fatal(err)
	}
	fragmentBoundary, err := bundle.ParseRelationRef("a#z")
	if err != nil {
		t.Fatal(err)
	}
	embeddedID, err := bundle.NewConceptID([]string{"source#part"})
	if err != nil {
		t.Fatal(err)
	}
	sourceID, err := bundle.ParseConceptID("source")
	if err != nil {
		t.Fatal(err)
	}
	embedded := bundle.RelationRef{ID: embeddedID}
	fragment := bundle.RelationRef{ID: sourceID, Fragment: "part"}
	report := validator.Report{
		ScannedFiles: 1,
		Diagnostics: []validator.Diagnostic{{
			Code:     "relation",
			File:     ".",
			Severity: validator.SeverityWarning,
			Message:  "relation warning",
			Source:   embedded,
			Refs:     []bundle.RelationRef{fragmentBoundary, embedded, rootBang, fragment},
		}},
	}

	// Act.
	receipt := testJournalReceipt(
		"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"escaped-root-replay",
		"escaped-root-replay-key",
	)
	replay, err := sealReplay(receipt, freezeValidation(report))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(replay)
	if err != nil {
		t.Fatal(err)
	}
	var restoredReplay replaceReplay
	err = json.Unmarshal(raw, &restoredReplay)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := restoredReplay.validation()

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	wire := replay.Validation.Diagnostics[0]
	if wire.Source != `source\#part` {
		t.Fatalf("replay source = %q, want escaped root", wire.Source)
	}
	if want := []string{"a!", "a#z", "source#part", `source\#part`}; !reflect.DeepEqual(wire.Refs, want) {
		t.Fatalf("replay refs = %#v, want %#v", wire.Refs, want)
	}
	if len(restored.Diagnostics) != 1 || len(restored.Diagnostics[0].Refs) != 4 {
		t.Fatalf("restored report = %#v, want one diagnostic with four refs", restored)
	}
	if got := relationRefKeyOf(restored.Diagnostics[0].Source); got != (relationRefKey{id: "source#part"}) {
		t.Fatalf("restored source = %#v, want escaped-root tuple", got)
	}
	if got := []relationRefKey{
		relationRefKeyOf(restored.Diagnostics[0].Refs[0]),
		relationRefKeyOf(restored.Diagnostics[0].Refs[1]),
		relationRefKeyOf(restored.Diagnostics[0].Refs[2]),
		relationRefKeyOf(restored.Diagnostics[0].Refs[3]),
	}; !reflect.DeepEqual(got, []relationRefKey{
		{id: "a!"},
		{id: "a", fragment: "z"},
		{id: "source", fragment: "part"},
		{id: "source#part"},
	}) {
		t.Fatalf("restored refs = %#v, want exact structural tuples", got)
	}
}

func TestCommitPersistsAndReplaysV1LexicalRefOrderAcrossRootFragmentBoundary(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "a!.md", "---\ntype: Note\n---\nRoot with punctuation.\n")
	writeTestFile(t, root, "a.md", "---\ntype: Note\nparts:\n  - id: z\n---\nFragment target.\n")
	s, err := openObserved(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	registerStoreCleanup(t, s)
	base := adversarialSnapshot(t, s)
	source := adversarialRef(t, "a!")
	target := adversarialRef(t, "a#z")
	change := store.ChangeSet{
		Version:      store.ChangeSetFormatVersion,
		ID:           "lexical-v1-real-commit",
		Actor:        "legacy-automation",
		BaseRevision: base.Revision(),
		Operations: []store.Operation{store.EnsureRelation{
			Source: source,
			Type:   "uses",
			Target: target,
		}},
	}
	options := store.CommitOptions{IdempotencyKey: "lexical-v1-real-commit"}

	// Act.
	receipt, commitErr := s.Commit(context.Background(), change, options)
	raw, readErr := readMetadata(context.Background(), s.rootFD, s.receiptPath(options.IdempotencyKey))
	durable, decodeErr := decodeReceiptFile(raw)
	replayed, replayErr := s.Commit(context.Background(), change, options)

	// Assert.
	if commitErr != nil || readErr != nil || decodeErr != nil || replayErr != nil {
		t.Fatalf("v1 receipt errors = commit:%v read:%v decode:%v replay:%v", commitErr, readErr, decodeErr, replayErr)
	}
	const changedRefs = `"ChangedRefs":["a!","a#z"]`
	if !bytes.Contains(raw, []byte(changedRefs)) {
		t.Fatalf("durable receipt = %s, want lexical v1 refs %s", raw, changedRefs)
	}
	wantRefs := []string{"a!", "a#z"}
	for label, gotReceipt := range map[string]store.CommitReceipt{
		"commit":  receipt,
		"durable": durable.Receipt,
		"replay":  replayed,
	} {
		gotRefs := []string{gotReceipt.ChangedRefs[0].String(), gotReceipt.ChangedRefs[1].String()}
		if !reflect.DeepEqual(gotRefs, wantRefs) {
			t.Fatalf("%s ChangedRefs = %#v, want %#v", label, gotRefs, wantRefs)
		}
	}
	if !reflect.DeepEqual(replayed, receipt) {
		t.Fatalf("replayed receipt = %#v, want %#v", replayed, receipt)
	}
}

func TestValidationReplayRejectsAmbiguousAndNonCanonicalRelationRefEscapes(t *testing.T) {
	// Arrange.
	tests := []string{
		"source#part#tail",
		`source\part`,
		`source\`,
	}

	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			replay := replaceReplay{
				Version:   replayFormatVersion,
				Algorithm: replayAlgorithm,
				Domain:    replayDomain,
				Validation: validationReplay{
					ScannedFiles: 1,
					Diagnostics: []validationDiagnosticReplay{{
						Severity: "WARN",
						File:     ".",
						Message:  "relation warning",
						Source:   raw,
						Refs:     []string{},
					}},
				},
			}

			// Act.
			err := validateReplay(replay, false)

			// Assert.
			if err == nil {
				t.Fatalf("validateReplay() accepted relation ref %q", raw)
			}
		})
	}
}
