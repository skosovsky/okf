package fs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/internal/receiptprojection"
	"github.com/skosovsky/okf/store"
	"github.com/skosovsky/okf/validator"
)

func TestCommitConcurrentEnsureRelationOneCommitThenFreshReplan(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	root, s := adversarialStore(t, Config{Fault: func(step Step) error {
		if step == StepJournalWrite {
			once.Do(func() { close(entered); <-release })
		}
		return nil
	}})
	base := adversarialSnapshot(t, s)
	first := adversarialChange(t, "ensure-one", base.Revision(), "depends_on")
	second := adversarialChange(t, "ensure-two", base.Revision(), "depends_on")
	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)
	go func() { _, err := s.Commit(context.Background(), first, store.CommitOptions{}); firstDone <- err }()
	<-entered
	go func() { _, err := s.Commit(context.Background(), second, store.CommitOptions{}); secondDone <- err }()
	close(release)
	err1, err2 := <-firstDone, <-secondDone
	if err1 != nil {
		t.Fatalf("first Commit() = %v", err1)
	}
	var conflict *store.Conflict
	if !errors.As(err2, &conflict) {
		t.Fatalf("second Commit() = %v, want conflict", err2)
	}
	if got := relationRefStrings(conflict.ChangedRefs); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("stale relation ChangedRefs = %v, want [a b]", got)
	}
	fresh := adversarialSnapshot(t, s)
	second.BaseRevision = fresh.Revision()
	if _, err := s.Commit(context.Background(), second, store.CommitOptions{}); err != nil {
		t.Fatalf("fresh replan Commit() = %v", err)
	}
	snap := adversarialSnapshot(t, s)
	b, err := bundle.Load(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(b.SemanticLinksFrom(adversarialRef(t, "a").ID)); got != 1 {
		t.Fatalf("desired edge count = %d, want 1", got)
	}
	_ = root
}

func TestSemanticChangedRefsIncludesContentAndFragmentsInStableOrder(t *testing.T) {
	// Arrange.
	base, err := newSnapshot(context.Background(), map[string][]byte{
		"a.md": []byte("---\ntype: Note\nparts:\n  - id: old\n---\nOld\n"),
		"b.md": []byte("---\ntype: Note\n---\nB\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	current, err := newSnapshot(context.Background(), map[string][]byte{
		"a.md": []byte("---\ntype: Note\nparts:\n  - id: new\n---\nNew\n"),
		"b.md": []byte("---\ntype: Note\n---\nB changed\n"),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	got := deriveChangedRefStrings(t, base, current)

	// Assert. Repeating the comparison also guards map iteration order.
	want := []string{"a", "a#new", "a#old", "b"}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(deriveChangedRefStrings(t, base, current), want) {
		t.Fatalf("semantic changed refs = %v, want %v", got, want)
	}
}

func TestSemanticChangedRefsIncludesAddedAndDeletedConceptRefs(t *testing.T) {
	// Arrange.
	base, err := newSnapshot(context.Background(), map[string][]byte{
		"deleted.md": []byte("---\ntype: Note\nparts:\n  - id: old\n---\nDeleted\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	current, err := newSnapshot(context.Background(), map[string][]byte{
		"added.md": []byte("---\ntype: Note\nparts:\n  - id: new\n---\nAdded\n"),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	got := deriveChangedRefStrings(t, base, current)

	// Assert.
	want := []string{"added", "added#new", "deleted", "deleted#old"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("semantic changed refs = %v, want %v", got, want)
	}
}

func deriveChangedRefStrings(t *testing.T, base, target *snapshot) []string {
	t.Helper()
	projection, err := receiptprojection.Derive(context.Background(), base.concepts, target.concepts)
	if err != nil {
		t.Fatalf("receiptprojection.Derive() error = %v", err)
	}
	return relationRefStrings(projection.ChangedRefs)
}

func TestCommitReceiptChangedRefsDescribeStagedSemanticDiff(t *testing.T) {
	tests := []struct {
		name       string
		files      map[string]string
		operations []store.Operation
		want       []string
	}{
		{
			name: "move retains old and new explicit fragments with inbound source",
			files: map[string]string{
				"a.md": "---\ntype: Note\nparts:\n  - id: first\n  - id: second\n---\nA\n",
				"c.md": "---\ntype: Note\nrelations:\n  uses:\n    - target: a#first\n---\nC\n",
			},
			operations: []store.Operation{store.MoveConcept{From: adversarialRef(t, "a").ID, To: adversarialRef(t, "b").ID}},
			want:       []string{"a", "a#first", "a#second", "b", "b#first", "b#second", "c"},
		},
		{
			name: "relation only includes source namespace and exact target",
			files: map[string]string{
				"a.md": "---\ntype: Note\nparts:\n  - id: source\n---\nA\n",
				"b.md": "---\ntype: Note\nparts:\n  - id: target\n---\nB\n",
			},
			operations: []store.Operation{store.EnsureRelation{Source: adversarialRef(t, "a#source"), Type: "uses", Target: adversarialRef(t, "b#target")}},
			want:       []string{"a", "a#source", "b#target"},
		},
		{
			name: "fragment rename includes old new and rewritten source",
			files: map[string]string{
				"a.md": "---\ntype: Note\nparts:\n  - id: old\n---\nA\n",
				"c.md": "---\ntype: Note\nrelations:\n  uses:\n    - target: a#old\n---\nC\n",
			},
			operations: []store.Operation{store.RenameFragment{Concept: adversarialRef(t, "a").ID, From: "old", To: "new"}},
			want:       []string{"a", "a#new", "a#old", "c"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			for path, content := range tt.files {
				writeTestFile(t, root, path, content)
			}
			s, err := openObserved(root, Config{})
			if err != nil {
				t.Fatal(err)
			}
			registerStoreCleanup(t, s)
			base := adversarialSnapshot(t, s)
			change := store.ChangeSet{Version: store.ChangeSetFormatVersion, ID: "semantic-" + store.ChangeSetID(tt.name), Actor: "tester", BaseRevision: base.Revision(), Operations: tt.operations}

			// Act.
			receipt, err := s.Commit(context.Background(), change, store.CommitOptions{IdempotencyKey: "semantic-" + store.IdempotencyKey(tt.name)})

			// Assert.
			if err != nil {
				t.Fatal(err)
			}
			if got := relationRefStrings(receipt.ChangedRefs); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("receipt ChangedRefs = %v, want %v", got, tt.want)
			}
			raw, err := json.Marshal(receipt)
			if err != nil {
				t.Fatal(err)
			}
			var restored store.CommitReceipt
			if err := json.Unmarshal(raw, &restored); err != nil {
				t.Fatal(err)
			}
			if got := relationRefStrings(restored.ChangedRefs); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("JSON ChangedRefs = %v, want %v", got, tt.want)
			}
			replay, err := s.Commit(context.Background(), change, store.CommitOptions{IdempotencyKey: "semantic-" + store.IdempotencyKey(tt.name)})
			if err != nil || !reflect.DeepEqual(replay, receipt) {
				t.Fatalf("replay = %#v, err=%v; want %#v", replay, err, receipt)
			}
		})
	}
}

func TestCommitReceiptChangedRefsAreEmptyForIdempotentNoOp(t *testing.T) {
	// Arrange.
	_, s := adversarialStore(t, Config{})
	base := adversarialSnapshot(t, s)
	first := adversarialChange(t, "semantic-first", base.Revision(), "uses")
	if _, err := s.Commit(context.Background(), first, store.CommitOptions{}); err != nil {
		t.Fatal(err)
	}
	current := adversarialSnapshot(t, s)
	noOp := adversarialChange(t, "semantic-no-op", current.Revision(), "uses")

	// Act.
	receipt, err := s.Commit(context.Background(), noOp, store.CommitOptions{})

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ResultRevision != current.Revision() || len(receipt.ChangedFiles) != 0 || len(receipt.ChangedRefs) != 0 {
		t.Fatalf("no-op receipt = %#v, want unchanged revision and no semantic changes", receipt)
	}
}

func TestReplaceConceptReceiptChangedRefsDescribeContentAndAddedConcepts(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "---\ntype: Note\nparts:\n  - id: old\n---\nA\n")
	s, err := openObserved(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	registerStoreCleanup(t, s)
	parse := func(raw string) bundle.Document {
		t.Helper()
		document, err := bundle.ParseDocument(raw)
		if err != nil {
			t.Fatal(err)
		}
		return document
	}
	base := adversarialSnapshot(t, s)
	replace := ReplaceConceptRequest{ChangeSetID: "semantic-replace", Actor: "tester", BaseRevision: base.Revision(), ConceptID: adversarialRef(t, "a").ID, Document: parse("---\ntype: Note\nparts:\n  - id: new\n---\nUpdated\n")}

	// Act.
	updated, err := s.ReplaceConcept(context.Background(), replace, store.CommitOptions{IdempotencyKey: "semantic-replace"})
	createBase := adversarialSnapshot(t, s)
	create := ReplaceConceptRequest{ChangeSetID: "semantic-create", Actor: "tester", BaseRevision: createBase.Revision(), ConceptID: adversarialRef(t, "added").ID, Document: parse("---\ntype: Note\nparts:\n  - id: fragment\n---\nAdded\n")}
	created, createErr := s.ReplaceConcept(context.Background(), create, store.CommitOptions{})

	// Assert.
	if err != nil || createErr != nil {
		t.Fatalf("replace errors: update=%v create=%v", err, createErr)
	}
	if got, want := relationRefStrings(updated.Receipt.ChangedRefs), []string{"a", "a#new", "a#old"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("update ChangedRefs = %v, want %v", got, want)
	}
	if got, want := relationRefStrings(created.Receipt.ChangedRefs), []string{"added", "added#fragment"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("create ChangedRefs = %v, want %v", got, want)
	}
	replay, replayErr := s.ReplaceConcept(context.Background(), replace, store.CommitOptions{IdempotencyKey: "semantic-replace"})
	if replayErr != nil || !reflect.DeepEqual(replay.Receipt, updated.Receipt) {
		t.Fatalf("replace replay = %#v, err=%v; want %#v", replay, replayErr, updated)
	}
}

func relationRefStrings(refs []bundle.RelationRef) []string {
	out := make([]string, len(refs))
	for i, ref := range refs {
		out[i] = ref.String()
	}
	return out
}

func TestCommitConcurrentEnsureRelationAndMoveConceptFreshReplan(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	_, s := adversarialStore(t, Config{Fault: func(step Step) error {
		if step == StepJournalWrite {
			once.Do(func() { close(entered); <-release })
		}
		return nil
	}})
	base := adversarialSnapshot(t, s)
	ensure := adversarialChange(t, "ensure-before-move", base.Revision(), "depends_on")
	move := store.ChangeSet{Version: store.ChangeSetFormatVersion, ID: "move-after-ensure", Actor: "adversary-test", BaseRevision: base.Revision(), Operations: []store.Operation{store.MoveConcept{From: adversarialRef(t, "a").ID, To: adversarialRef(t, "nested/a").ID}}}
	ensureDone, moveDone := make(chan error, 1), make(chan error, 1)
	go func() { _, err := s.Commit(context.Background(), ensure, store.CommitOptions{}); ensureDone <- err }()
	<-entered
	go func() { _, err := s.Commit(context.Background(), move, store.CommitOptions{}); moveDone <- err }()
	close(release)
	if err := <-ensureDone; err != nil {
		t.Fatalf("ensure Commit() = %v", err)
	}
	err := <-moveDone
	var conflict *store.Conflict
	if !errors.As(err, &conflict) {
		t.Fatalf("move Commit() = %v, want conflict", err)
	}
	move.BaseRevision = adversarialSnapshot(t, s).Revision()
	if _, err := s.Commit(context.Background(), move, store.CommitOptions{}); err != nil {
		t.Fatalf("fresh move Commit() = %v", err)
	}
	snap := adversarialSnapshot(t, s)
	b, err := bundle.Load(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	links := b.SemanticLinksFrom(adversarialRef(t, "nested/a").ID)
	if len(links) != 1 || links[0].Target.String() != "b" {
		t.Fatalf("moved relation = %#v", links)
	}
	if _, err := snap.ReadFile(context.Background(), "a.md"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old concept remains: %v", err)
	}
}

func TestReplaceConceptCreateUpdateRejectsStaleAndPreservesModes(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "existing.md", adversarialDocument("old"))
	if err := os.Chmod(filepath.Join(root, "existing.md"), 0o640); err != nil {
		t.Fatal(err)
	}
	s, err := openObserved(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	registerStoreCleanup(t, s)
	parse := func(text string) bundle.Document {
		d, err := bundle.ParseDocument(text)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	base := adversarialSnapshot(t, s)
	created := ReplaceConceptRequest{ChangeSetID: "replace-create", Actor: "adversary-test", BaseRevision: base.Revision(), ConceptID: adversarialRef(t, "created").ID, Document: parse(adversarialDocument("created"))}
	createdResult, err := s.ReplaceConcept(context.Background(), created, store.CommitOptions{})
	if err != nil {
		t.Fatalf("create = %v", err)
	}
	if got := createdResult.Receipt.ChangedRefs; len(got) != 1 || got[0].String() != created.ConceptID.String() {
		t.Fatalf("create ChangedRefs = %#v, want [%s]", got, created.ConceptID)
	}
	createdBytes, err := os.ReadFile(filepath.Join(root, "created.md"))
	if err != nil {
		t.Fatal(err)
	}
	wantCreated, _ := created.Document.Serialize()
	if string(createdBytes) != wantCreated {
		t.Fatalf("create bytes = %q, want %q", createdBytes, wantCreated)
	}
	if mode, _ := os.Stat(filepath.Join(root, "created.md")); mode.Mode().Perm() != 0o644 {
		t.Fatalf("new mode = %o, want 644", mode.Mode().Perm())
	}
	updateBase := adversarialSnapshot(t, s)
	updated := ReplaceConceptRequest{ChangeSetID: "replace-update", Actor: "adversary-test", BaseRevision: updateBase.Revision(), ConceptID: adversarialRef(t, "existing").ID, Document: parse(adversarialDocument("new"))}
	updatedResult, err := s.ReplaceConcept(context.Background(), updated, store.CommitOptions{})
	if err != nil {
		t.Fatalf("update = %v", err)
	}
	if got := updatedResult.Receipt.ChangedRefs; len(got) != 1 || got[0].String() != updated.ConceptID.String() {
		t.Fatalf("update ChangedRefs = %#v, want [%s]", got, updated.ConceptID)
	}
	updatedBytes, err := os.ReadFile(filepath.Join(root, "existing.md"))
	if err != nil {
		t.Fatal(err)
	}
	wantUpdated, _ := updated.Document.Serialize()
	if string(updatedBytes) != wantUpdated {
		t.Fatalf("update bytes = %q, want %q", updatedBytes, wantUpdated)
	}
	if mode, _ := os.Stat(filepath.Join(root, "existing.md")); mode.Mode().Perm() != 0o640 {
		t.Fatalf("existing mode = %o, want 640", mode.Mode().Perm())
	}
	_, err = s.ReplaceConcept(context.Background(), updated, store.CommitOptions{})
	var conflict *store.Conflict
	if !errors.As(err, &conflict) {
		t.Fatalf("stale update = %v, want conflict", err)
	}
}

func TestReplaceConceptAllowsInformationalAnchorAliasAndRejectsBlockingRelations(t *testing.T) {
	// Arrange.
	root, s := adversarialStore(t, Config{})
	base := adversarialSnapshot(t, s)
	aliasDocument, err := bundle.ParseDocument("---\ntype: Note\nparts:\n  - id: canonical\n    anchor: legacy\n---\n\nReplacement\n")
	if err != nil {
		t.Fatal(err)
	}
	aliasRequest := ReplaceConceptRequest{ChangeSetID: "replace-anchor-alias", Actor: "adversary-test", BaseRevision: base.Revision(), ConceptID: adversarialRef(t, "a").ID, Document: aliasDocument}

	// Act.
	aliasResult, aliasErr := s.ReplaceConcept(context.Background(), aliasRequest, store.CommitOptions{})
	afterAlias := adversarialSnapshot(t, s)
	beforeRejected, err := os.ReadFile(filepath.Join(root, "a.md"))
	if err != nil {
		t.Fatal(err)
	}
	malformedDocument, err := bundle.ParseDocument("---\ntype: Note\nrelations:\n  depends_on: missing\n---\n\nRejected\n")
	if err != nil {
		t.Fatal(err)
	}
	malformedRequest := ReplaceConceptRequest{ChangeSetID: "replace-malformed-relation", Actor: "adversary-test", BaseRevision: afterAlias.Revision(), ConceptID: adversarialRef(t, "a").ID, Document: malformedDocument}
	malformedResult, malformedErr := s.ReplaceConcept(context.Background(), malformedRequest, store.CommitOptions{})
	afterRejected, readErr := os.ReadFile(filepath.Join(root, "a.md"))

	// Assert.
	if aliasErr != nil || aliasResult.Receipt.ResultRevision != afterAlias.Revision() {
		t.Fatalf("anchor alias replacement = %#v, err=%v; want successful publication", aliasResult, aliasErr)
	}
	var invalid *store.InvalidChangeSet
	if !errors.As(malformedErr, &invalid) {
		t.Fatalf("malformed relation error = %v, want InvalidChangeSet", malformedErr)
	}
	if readErr != nil || !bytes.Equal(beforeRejected, afterRejected) {
		t.Fatalf("rejected replacement changed bytes: read=%v before=%q after=%q", readErr, beforeRejected, afterRejected)
	}
	if len(malformedResult.Validation.Diagnostics) == 0 || malformedResult.Validation.Diagnostics[len(malformedResult.Validation.Diagnostics)-1].Severity != validator.SeverityError {
		t.Fatalf("rejection diagnostics = %#v, want blocking semantic error", malformedResult.Validation.Diagnostics)
	}
	if got := malformedResult.Validation.Diagnostics[len(malformedResult.Validation.Diagnostics)-1]; got.Code != "relation_not_sequence" || got.File != "a.md" || got.Message == "" {
		t.Fatalf("validation diagnostic = %#v, want relation_not_sequence for a.md with message", got)
	}
	if len(invalid.Diagnostics) != 1 || invalid.Diagnostics[0].Code != "relation_not_sequence" || invalid.Diagnostics[0].Message == "" || len(invalid.Diagnostics[0].Refs) != 1 || invalid.Diagnostics[0].Refs[0].ID.String() != malformedRequest.ConceptID.String() {
		t.Fatalf("rejected diagnostics = %#v, want semantic code/message/source", invalid.Diagnostics)
	}
}

func TestReplaceConceptProjectsBlockingRelationDiagnosticsLikePreview(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nOriginal\n")
	writeTestFile(t, root, "target.md", "---\ntype: Note\nparts:\n  - id: duplicate\n  - id: duplicate\n---\nTarget\n")
	s, err := openObserved(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	registerStoreCleanup(t, s)
	base := adversarialSnapshot(t, s)
	before, err := os.ReadFile(filepath.Join(root, "a.md"))
	if err != nil {
		t.Fatal(err)
	}
	document, err := bundle.ParseDocument("---\ntype: Note\nrelations:\n  depends_on: malformed\n  uses:\n    - target: target#duplicate\n    - target: target#duplicate\n---\nRejected\n")
	if err != nil {
		t.Fatal(err)
	}
	req := ReplaceConceptRequest{ChangeSetID: "replace-projected-relation-diagnostics", Actor: "tester", BaseRevision: base.Revision(), ConceptID: adversarialRef(t, "a").ID, Document: document}

	// Act.
	result, replaceErr := s.ReplaceConcept(context.Background(), req, store.CommitOptions{})
	after, readErr := os.ReadFile(filepath.Join(root, "a.md"))

	// Assert.
	var invalid *store.InvalidChangeSet
	if !errors.As(replaceErr, &invalid) {
		t.Fatalf("ReplaceConcept() error = %v, want InvalidChangeSet", replaceErr)
	}
	if readErr != nil || !bytes.Equal(before, after) || result.Receipt.ChangeSetID != "" {
		t.Fatalf("rejected replacement published state: read=%v before=%q after=%q receipt=%#v", readErr, before, after, result.Receipt)
	}
	if got, want := len(invalid.Diagnostics), 3; got != want {
		t.Fatalf("diagnostics count = %d, want %d: %#v", got, want, invalid.Diagnostics)
	}
	want := []store.Diagnostic{
		{Kind: store.DiagnosticRelation, Severity: store.DiagnosticError, Code: "duplicate_fragment", File: "target.md", Message: "duplicate fragment is ambiguous", RawTarget: "duplicate", Refs: []bundle.RelationRef{{ID: adversarialRef(t, "target").ID, Fragment: "duplicate"}}},
		{Kind: store.DiagnosticRelation, Severity: store.DiagnosticError, Code: "missing_or_ambiguous_target_fragment", File: "a.md", Message: "target fragment does not exist or is ambiguous", RelationType: "uses", RawTarget: "target#duplicate", Refs: []bundle.RelationRef{{ID: req.ConceptID}}},
		{Kind: store.DiagnosticRelation, Severity: store.DiagnosticError, Code: "relation_not_sequence", File: "a.md", Message: "relation value must be a sequence", RelationType: "depends_on", Refs: []bundle.RelationRef{{ID: req.ConceptID}}},
	}
	if !reflect.DeepEqual(invalid.Diagnostics, want) {
		t.Fatalf("diagnostics = %#v, want exact preview projection %#v", invalid.Diagnostics, want)
	}
}

func TestReplaceConceptReplaysBeforeStaleCASAndAcrossStores(t *testing.T) {
	// Arrange.
	root, first := adversarialStore(t, Config{})
	second, err := openObserved(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	registerStoreCleanup(t, second)
	base := adversarialSnapshot(t, first)
	doc, err := bundle.ParseDocument(adversarialDocument("replacement"))
	if err != nil {
		t.Fatal(err)
	}
	req := ReplaceConceptRequest{ChangeSetID: "replace-replay", Actor: "adversary-test", BaseRevision: base.Revision(), ConceptID: adversarialRef(t, "a").ID, Document: doc}
	opts := store.CommitOptions{IdempotencyKey: "replace-replay-key"}

	// Act.
	firstResult, firstErr := first.ReplaceConcept(context.Background(), req, opts)
	// Simulate a caller that refreshed the base after its first response was
	// lost. The replacement identity must not include this CAS precondition.
	req.BaseRevision = adversarialSnapshot(t, second).Revision()
	replay, replayErr := second.ReplaceConcept(context.Background(), req, opts)
	different, err := bundle.ParseDocument(adversarialDocument("different"))
	if err != nil {
		t.Fatal(err)
	}
	req.Document = different
	_, conflictErr := second.ReplaceConcept(context.Background(), req, opts)

	// Assert.
	if firstErr != nil || replayErr != nil || replay.Receipt.ResultRevision != firstResult.Receipt.ResultRevision || !replay.Receipt.CommitTime.Equal(firstResult.Receipt.CommitTime) {
		t.Fatalf("replace replay: first=%#v err=%v replay=%#v err=%v", firstResult, firstErr, replay, replayErr)
	}
	wantRef := adversarialRef(t, "a")
	if got := firstResult.Receipt.ChangedRefs; len(got) != 1 || got[0].String() != wantRef.String() {
		t.Fatalf("first ChangedRefs = %#v, want [%s]", got, wantRef)
	}
	if got := replay.Receipt.ChangedRefs; len(got) != 1 || got[0].String() != wantRef.String() {
		t.Fatalf("replay ChangedRefs = %#v, want [%s]", got, wantRef)
	}
	var conflict *store.IdempotencyConflict
	if !errors.As(conflictErr, &conflict) {
		t.Fatalf("same key with another payload = %v, want idempotency conflict", conflictErr)
	}
}

func TestReplaceConceptConcurrentCrossStoreRetryReplaysReceipt(t *testing.T) {
	// Arrange.
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	root, first := adversarialStore(t, Config{Fault: func(step Step) error {
		if step == StepJournalWrite {
			once.Do(func() { close(entered); <-release })
		}
		return nil
	}})
	second, err := openObserved(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	registerStoreCleanup(t, second)
	base := adversarialSnapshot(t, first)
	doc, err := bundle.ParseDocument(adversarialDocument("concurrent replacement"))
	if err != nil {
		t.Fatal(err)
	}
	req := ReplaceConceptRequest{ChangeSetID: "replace-concurrent", Actor: "adversary-test", BaseRevision: base.Revision(), ConceptID: adversarialRef(t, "a").ID, Document: doc}
	opts := store.CommitOptions{IdempotencyKey: "replace-concurrent-key"}
	firstDone := make(chan ReplaceConceptResult, 1)
	secondDone := make(chan ReplaceConceptResult, 1)
	firstErr := make(chan error, 1)
	secondErr := make(chan error, 1)
	go func() {
		result, callErr := first.ReplaceConcept(context.Background(), req, opts)
		firstDone <- result
		firstErr <- callErr
	}()
	<-entered
	go func() {
		result, callErr := second.ReplaceConcept(context.Background(), req, opts)
		secondDone <- result
		secondErr <- callErr
	}()
	close(release)

	// Act.
	left, leftErr := <-firstDone, <-firstErr
	right, rightErr := <-secondDone, <-secondErr

	// Assert.
	if leftErr != nil || rightErr != nil || left.Receipt.ResultRevision != right.Receipt.ResultRevision || !left.Receipt.CommitTime.Equal(right.Receipt.CommitTime) {
		t.Fatalf("concurrent replay: left=%#v err=%v right=%#v err=%v", left, leftErr, right, rightErr)
	}
}

func TestPostJournalFaultMatrixRecoversToValidPreOrPostState(t *testing.T) {
	steps := []Step{StepJournalWrite, StepJournalFileWrite, StepJournalFileSync, StepJournalFileClose, StepJournalRename, StepJournalDirectorySync, StepMkdir, StepChmod, StepFileWrite, StepFileSync, StepFileClose, StepRename, StepRemove, StepDirectorySync}
	for _, step := range steps {
		t.Run(string(step), func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "old.md", adversarialDocument("old"))
			fired := false
			s, err := openObserved(root, Config{})
			if err != nil {
				t.Fatal(err)
			}
			registerStoreCleanup(t, s)
			base := adversarialSnapshot(t, s)
			s.config.PostFault = func(got Step) error {
				if got == step && !fired {
					fired = true
					return errors.New("fault " + string(step))
				}
				return nil
			}
			next, err := newSnapshot(context.Background(), map[string][]byte{"nested/new.md": []byte(adversarialDocument("new"))})
			if err != nil {
				t.Fatal(err)
			}
			err = s.publish(context.Background(), next, testJournalReceipt(base.Revision(), next.Revision(), "matrix-"+string(step), ""))
			if err == nil || !fired {
				t.Fatalf("publish error=%v fired=%v", err, fired)
			}
			// The physical transaction-directory sync, not rename visibility, owns
			// the commit. Snapshot converges only a durable journal; an interrupted
			// pre-boundary rename is cleaned as an atomic abort.
			if step == StepJournalDirectorySync {
				if got := adversarialSnapshot(t, s).Revision(); got != next.Revision() {
					t.Fatalf("published journal fault %s observed %s, want post-state %s", step, got, next.Revision())
				}
			}
			// Keep the faulted handle open to model recovery after an abrupt
			// process stop; registered cleanup runs after recovery assertions.
			reopened, openErr := openObserved(root, Config{})
			if openErr != nil {
				t.Fatalf("reopen = %v", openErr)
			}
			registerStoreCleanup(t, reopened)
			snap := adversarialSnapshot(t, reopened)
			if snap.Revision() != next.Revision() {
				// A failure before the journal is deliberately an atomic abort.
				pre, preErr := newSnapshot(context.Background(), map[string][]byte{"old.md": []byte(adversarialDocument("old"))})
				if preErr != nil || snap.Revision() != pre.Revision() {
					t.Fatalf("recovery revision = %s; neither pre nor post", snap.Revision())
				}
			}
		})
	}
}

func TestWriteFileSyncsNewNestedDirectoriesBottomUp(t *testing.T) {
	root := t.TempDir()
	var synced []string
	s, err := openObserved(root, Config{DirectorySync: func(dir string) error { synced = append(synced, dir); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	registerStoreCleanup(t, s)
	// Ignore Open's metadata setup; this is the new visible directory tree.
	synced = nil
	if err := s.writeFileForTest(context.Background(), "one/two/concept.md", []byte(adversarialDocument("nested"))); err != nil {
		t.Fatal(err)
	}
	publicSyncs := make([]string, 0, len(synced))
	for _, dir := range synced {
		if dir == claimDirectory || strings.HasPrefix(dir, claimDirectory+"/") || dir == internalDirectory {
			continue
		}
		if dir == "." && len(publicSyncs) > 0 && publicSyncs[len(publicSyncs)-1] == "." {
			continue // R5 private-root repair owns this adjacent barrier.
		}
		publicSyncs = append(publicSyncs, dir)
	}
	want := []string{"one/two", "one", "one", ".", temporaryDirectory, "one/two"}
	if len(publicSyncs) != len(want) {
		t.Fatalf("public sync calls = %#v, want %#v; full=%#v", publicSyncs, want, synced)
	}
	for i := range want {
		if publicSyncs[i] != want[i] {
			t.Fatalf("public sync call %d = %q, want %q; all=%#v", i, publicSyncs[i], want[i], synced)
		}
	}
	got, readErr := os.ReadFile(filepath.Join(root, "one", "two", "concept.md"))
	if readErr != nil || !bytes.Equal(got, []byte(adversarialDocument("nested"))) {
		t.Fatalf("visible result read=%v bytes=%q", readErr, got)
	}
	claims, readErr := os.ReadDir(filepath.Join(root, filepath.FromSlash(claimDirectory)))
	if readErr != nil || len(claims) != 0 {
		t.Fatalf("claim residue read=%v entries=%v", readErr, claims)
	}
}

func TestCommitCancellationImmediatelyBeforeJournalAbortsWithoutPublication(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	root, s := adversarialStore(t, Config{Fault: func(step Step) error {
		if step == StepJournalWrite {
			once.Do(func() { close(entered); <-release })
		}
		return nil
	}})
	base := adversarialSnapshot(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := s.Commit(ctx, adversarialChange(t, "cancel-before-journal", base.Revision(), "depends_on"), store.CommitOptions{})
		done <- err
	}()
	<-entered
	cancel()
	close(release)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Commit() = %v, want cancellation", err)
	}
	reopened, err := openObserved(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	registerStoreCleanup(t, reopened)
	if got := adversarialSnapshot(t, reopened).Revision(); got != base.Revision() {
		t.Fatalf("published revision = %s, want %s", got, base.Revision())
	}
}

func TestCommitRejectsSymlinkAncestorAndTargetSwapWithoutEscapingPinnedRoot(t *testing.T) {
	root, s := adversarialStore(t, Config{})
	outside := t.TempDir()
	base := adversarialSnapshot(t, s)
	if err := os.Symlink(outside, filepath.Join(root, "nested")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	move := store.ChangeSet{Version: store.ChangeSetFormatVersion, ID: "symlink-ancestor", Actor: "adversary-test", BaseRevision: base.Revision(), Operations: []store.Operation{store.MoveConcept{From: adversarialRef(t, "a").ID, To: adversarialRef(t, "nested/a").ID}}}
	if _, err := s.Commit(context.Background(), move, store.CommitOptions{}); err == nil {
		t.Fatal("Commit() accepted symlink ancestor")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("ancestor swap escaped root: entries=%#v err=%v", entries, err)
	}
	if err := os.Remove(filepath.Join(root, "nested")); err != nil {
		t.Fatal(err)
	}

	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	s.config.Fault = func(step Step) error {
		if step == StepJournalWrite {
			once.Do(func() { close(entered); <-release })
		}
		return nil
	}
	base = adversarialSnapshot(t, s)
	change := adversarialChange(t, "symlink-target", base.Revision(), "depends_on")
	done := make(chan error, 1)
	go func() { _, err := s.Commit(context.Background(), change, store.CommitOptions{}); done <- err }()
	<-entered
	if err := os.Rename(filepath.Join(root, "a.md"), filepath.Join(root, "a.saved")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "escaped.md"), filepath.Join(root, "a.md")); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err == nil {
		t.Fatal("Commit() accepted symlink target swap")
	}
	if _, err := os.Stat(filepath.Join(outside, "escaped.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target swap escaped root: %v", err)
	}
}
