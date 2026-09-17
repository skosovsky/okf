package mutation

import (
	"bytes"
	"context"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
)

func TestPlannerV02Families_PreserveUnknownNestedFieldsAndAreIdempotent(t *testing.T) {
	// Arrange.
	source := memorySource{"a.md": []byte("---\n" +
		"type: Note\n" +
		"generated: {by: process:old, at: 2026-01-01T00:00:00Z, x-generated: keep}\n" +
		"verified: {by: process:nightly, at: 2026-01-02T00:00:00Z, x-verification: keep}\n" +
		"sources:\n" +
		"  - {id: policy, resource: old.md, title: Old, x-source: keep}\n" +
		"status: draft\n" +
		"---\nBody\n")}
	id := ref(t, "a").ID
	selector, err := store.SourceByID("policy")
	if err != nil {
		t.Fatal(err)
	}
	count := uint64(42)
	sourceUsageWindow := store.UsageWindow{From: "2026-02-01", To: "2026-02-28"}
	putSource, err := store.NewPutSource(id, store.ProvenanceSource{
		ID:           "policy",
		Resource:     "references/policy.md",
		Title:        "Policy",
		UsageCount:   &count,
		LastModified: "2026-02-01",
		UsageWindow:  &sourceUsageWindow,
	})
	if err != nil {
		t.Fatal(err)
	}
	window, err := store.NewSetUsageWindow(id, &selector, &store.UsageWindow{From: "2026-02-01", To: "2026-02-28"})
	if err != nil {
		t.Fatal(err)
	}
	status, staleAfter := "stable", "2026-12-31"
	lifecycle, err := store.NewSetLifecycle(id, store.Lifecycle{Status: &status, StaleAfter: &staleAfter})
	if err != nil {
		t.Fatal(err)
	}
	operations := []store.Operation{
		store.SetGenerated{Concept: id, Generated: store.Generation{By: "reference_agent/gemini-2.5-pro", At: "2026-02-02T03:04:05Z"}},
		store.EnsureVerification{Concept: id, Verification: store.Verification{By: "human:reviewer", At: "2026-02-03T04:05:06Z"}},
		putSource,
		window,
		lifecycle,
	}
	changeSet := store.ChangeSet{
		Version:      store.ChangeSetFormatVersion,
		ID:           "v02-families",
		Actor:        "principal",
		BaseRevision: revisionFor(t, source),
		Operations:   operations,
	}

	// Act.
	first, err := Plan(context.Background(), source, changeSet)
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, err := first.Staged.ReadFile(context.Background(), "a.md")
	if err != nil {
		t.Fatal(err)
	}
	repeatedSource := memorySource{"a.md": append([]byte(nil), firstBytes...)}
	repeat := changeSet
	repeat.ID = "v02-families-repeat"
	repeat.BaseRevision = revisionFor(t, repeatedSource)
	second, secondErr := Plan(context.Background(), repeatedSource, repeat)
	var secondBytes []byte
	if secondErr == nil {
		secondBytes, secondErr = second.Staged.ReadFile(context.Background(), "a.md")
	}

	// Assert.
	if secondErr != nil {
		t.Fatal(secondErr)
	}
	for _, preserved := range [][]byte{[]byte("x-generated: keep"), []byte("x-verification: keep"), []byte("x-source: keep")} {
		if !bytes.Contains(firstBytes, preserved) {
			t.Fatalf("unknown nested field %q was lost:\n%s", preserved, firstBytes)
		}
	}
	for _, desired := range [][]byte{
		[]byte("reference_agent/gemini-2.5-pro"),
		[]byte("human:reviewer"),
		[]byte("references/policy.md"),
		[]byte("usage_count: 42"),
		[]byte("status: stable"),
		[]byte("stale_after: 2026-12-31"),
	} {
		if !bytes.Contains(firstBytes, desired) {
			t.Fatalf("desired state %q is absent:\n%s", desired, firstBytes)
		}
	}
	if bytes.Count(firstBytes, []byte("human:reviewer")) != 1 {
		t.Fatalf("verification was not ensured exactly once:\n%s", firstBytes)
	}
	if !bytes.Equal(secondBytes, firstBytes) || len(second.Preview.Writes) != 0 {
		t.Fatalf("second desired-state application changed bytes: writes=%#v\nfirst:\n%s\nsecond:\n%s", second.Preview.Writes, firstBytes, secondBytes)
	}
}

func TestPlannerRemoveVerificationAndSource_PreserveRemainingExtensions(t *testing.T) {
	// Arrange.
	source := memorySource{"a.md": []byte("---\n" +
		"type: Note\n" +
		"verified:\n" +
		"  - {by: process:remove, at: 2026-01-01T00:00:00Z, x-remove: keep-until-removed}\n" +
		"  - {by: human:keep, at: 2026-01-02T00:00:00Z, x-keep: preserved}\n" +
		"sources:\n" +
		"  - {id: remove, resource: remove.md, x-remove: true}\n" +
		"  - {id: keep, resource: keep.md, x-keep: preserved}\n" +
		"---\nBody\n")}
	id := ref(t, "a").ID
	sourceSelector, err := store.SourceByID("remove")
	if err != nil {
		t.Fatal(err)
	}
	removeSource, err := store.NewRemoveSource(id, sourceSelector)
	if err != nil {
		t.Fatal(err)
	}
	changeSet := store.ChangeSet{
		Version:      store.ChangeSetFormatVersion,
		ID:           "v02-remove",
		Actor:        "principal",
		BaseRevision: revisionFor(t, source),
		Operations: []store.Operation{
			store.RemoveVerification{Concept: id, Verification: store.Verification{By: "process:remove", At: "2026-01-01T00:00:00Z"}},
			removeSource,
		},
	}

	// Act.
	result, err := Plan(context.Background(), source, changeSet)
	if err != nil {
		t.Fatal(err)
	}
	got, err := result.Staged.ReadFile(context.Background(), "a.md")

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(got, []byte("process:remove")) || bytes.Contains(got, []byte("id: remove")) {
		t.Fatalf("selected values remain:\n%s", got)
	}
	if !bytes.Contains(got, []byte("x-keep: preserved")) || !bytes.Contains(got, []byte("human:keep")) || !bytes.Contains(got, []byte("id: keep")) {
		t.Fatalf("remaining unknown fields were lost:\n%s", got)
	}
}

func TestPlannerSetUsageWindow_SharedInsertRemoveAndIdempotentRemoval(t *testing.T) {
	// Arrange.
	source := memorySource{"a.md": []byte("---\ntype: Note\n---\nBody\n")}
	id := ref(t, "a").ID
	set, err := store.NewSetUsageWindow(id, nil, &store.UsageWindow{From: "2026-01-01", To: "2026-01-31"})
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	inserted, err := Plan(context.Background(), source, change(t, source, set))
	if err != nil {
		t.Fatal(err)
	}
	insertedBytes, err := inserted.Staged.ReadFile(context.Background(), "a.md")
	if err != nil {
		t.Fatal(err)
	}
	insertedSource := memorySource{"a.md": insertedBytes}
	remove, err := store.NewSetUsageWindow(id, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	removed, err := Plan(context.Background(), insertedSource, change(t, insertedSource, remove))
	if err != nil {
		t.Fatal(err)
	}
	removedBytes, err := removed.Staged.ReadFile(context.Background(), "a.md")
	if err != nil {
		t.Fatal(err)
	}
	removedSource := memorySource{"a.md": removedBytes}
	repeated, repeatErr := Plan(context.Background(), removedSource, change(t, removedSource, remove))

	// Assert.
	if repeatErr != nil {
		t.Fatal(repeatErr)
	}
	if !bytes.Contains(insertedBytes, []byte("usage_window:")) || bytes.Contains(removedBytes, []byte("usage_window:")) {
		t.Fatalf("shared usage window insert/remove mismatch:\ninserted:\n%s\nremoved:\n%s", insertedBytes, removedBytes)
	}
	if len(repeated.Preview.Writes) != 0 {
		t.Fatalf("idempotent removal wrote files: %#v", repeated.Preview.Writes)
	}
}

func TestPlannerPutSource_DuplicateIDFailsClosedWithoutStage(t *testing.T) {
	// Arrange.
	source := memorySource{"a.md": []byte("---\ntype: Note\nsources:\n  - {id: duplicate, resource: one.md}\n  - {id: duplicate, resource: two.md}\n---\nBody\n")}
	id := ref(t, "a").ID
	operation, err := store.NewPutSource(id, store.ProvenanceSource{ID: "duplicate", Resource: "desired.md"})
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	result, planErr := Plan(context.Background(), source, change(t, source, operation))

	// Assert.
	if planErr == nil {
		t.Fatal("duplicate source selector was accepted")
	}
	if result.Staged != nil || len(result.Preview.Writes) != 0 || len(result.Preview.Renames) != 0 {
		t.Fatalf("rejected source plan exposed stage: %#v", result)
	}
}

func TestPlannerV02Selectors_UnsafeCandidateProvenanceFailsClosedWithoutStage(t *testing.T) {
	tests := []struct {
		name        string
		frontmatter string
		operation   func(t *testing.T, id bundle.ConceptID) store.Operation
	}{
		{
			name: "verification alias",
			frontmatter: "template: &verification {by: human:reviewer, at: 2026-01-01T00:00:00Z}\n" +
				"verified: [*verification]\n",
			operation: func(t *testing.T, id bundle.ConceptID) store.Operation {
				return store.EnsureVerification{
					Concept:      id,
					Verification: store.Verification{By: "human:reviewer", At: "2026-01-01T00:00:00Z"},
				}
			},
		},
		{
			name: "verification merge",
			frontmatter: "template: &verification {by: human:reviewer, at: 2026-01-01T00:00:00Z}\n" +
				"verified:\n  - {<<: *verification}\n",
			operation: func(t *testing.T, id bundle.ConceptID) store.Operation {
				return store.EnsureVerification{
					Concept:      id,
					Verification: store.Verification{By: "human:reviewer", At: "2026-01-01T00:00:00Z"},
				}
			},
		},
		{
			name:        "verification anchor",
			frontmatter: "verified:\n  - &verification {by: human:reviewer, at: 2026-01-01T00:00:00Z}\n",
			operation: func(t *testing.T, id bundle.ConceptID) store.Operation {
				return store.EnsureVerification{
					Concept:      id,
					Verification: store.Verification{By: "human:reviewer", At: "2026-01-01T00:00:00Z"},
				}
			},
		},
		{
			name:        "verification explicit tag",
			frontmatter: "verified:\n  - !!map {by: human:reviewer, at: 2026-01-01T00:00:00Z}\n",
			operation: func(t *testing.T, id bundle.ConceptID) store.Operation {
				return store.EnsureVerification{
					Concept:      id,
					Verification: store.Verification{By: "human:reviewer", At: "2026-01-01T00:00:00Z"},
				}
			},
		},
		{
			name:        "anonymous source comment",
			frontmatter: "sources:\n  - {resource: policy.md, title: Policy} # provenance-sensitive\n",
			operation: func(t *testing.T, id bundle.ConceptID) store.Operation {
				t.Helper()
				operation, err := store.NewPutSource(id, store.ProvenanceSource{
					Resource: "policy.md",
					Title:    "Policy",
				})
				if err != nil {
					t.Fatal(err)
				}
				return operation
			},
		},
		{
			name:        "anonymous source unknown extension",
			frontmatter: "sources:\n  - {resource: policy.md, title: Policy, x-source: keep}\n",
			operation: func(t *testing.T, id bundle.ConceptID) store.Operation {
				t.Helper()
				operation, err := store.NewPutSource(id, store.ProvenanceSource{
					Resource: "policy.md",
					Title:    "Policy",
				})
				if err != nil {
					t.Fatal(err)
				}
				return operation
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			source := memorySource{"a.md": []byte("---\ntype: Note\n" + test.frontmatter + "---\nBody\n")}
			operation := test.operation(t, ref(t, "a").ID)

			// Act.
			result, err := Plan(context.Background(), source, change(t, source, operation))

			// Assert.
			if err == nil {
				t.Fatal("unsafe selector candidate was accepted")
			}
			if result.Staged != nil || len(result.Preview.Writes) != 0 || len(result.Preview.Renames) != 0 {
				t.Fatalf("rejected selector exposed staged changes: %#v", result)
			}
		})
	}
}

func TestPlannerClosedUnion_RejectsPointersAndTypedNilWithoutStage(t *testing.T) {
	// Arrange.
	source := memorySource{"a.md": []byte("---\ntype: Note\n---\nBody\n")}
	id := ref(t, "a").ID
	operation := store.SetGenerated{Concept: id, Generated: store.Generation{By: "process:generator"}}
	precondition := store.RefExists{Ref: bundle.RelationRef{ID: id}}
	tests := []struct {
		name          string
		operation     store.Operation
		preconditions []store.Precondition
	}{
		{name: "operation pointer", operation: &operation},
		{name: "operation typed nil", operation: (*store.SetGenerated)(nil)},
		{name: "precondition pointer", operation: operation, preconditions: []store.Precondition{&precondition}},
		{name: "precondition typed nil", operation: operation, preconditions: []store.Precondition{(*store.RefExists)(nil)}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changeSet := change(t, source, test.operation)
			changeSet.Preconditions = test.preconditions

			// Act.
			result, err := Plan(context.Background(), source, changeSet)

			// Assert.
			if err == nil {
				t.Fatal("non-value closed-union member was accepted")
			}
			if result.Staged != nil || len(result.Preview.Writes) != 0 || len(result.Preview.Renames) != 0 {
				t.Fatalf("rejected change exposed stage: %#v", result)
			}
		})
	}
}

func TestPlannerAnonymousSourceExactSelector_IsOrderIndependent(t *testing.T) {
	// Arrange.
	source := memorySource{"a.md": []byte("---\ntype: Note\nsources:\n" +
		"  - {title: Policy, resource: policy.md}\n" +
		"  - {id: keep, resource: keep.md}\n" +
		"---\nBody\n")}
	id := ref(t, "a").ID
	exact := store.ProvenanceSource{Resource: "policy.md", Title: "Policy"}
	put, err := store.NewPutSource(id, exact)
	if err != nil {
		t.Fatal(err)
	}
	selector, err := store.SourceByExact(exact)
	if err != nil {
		t.Fatal(err)
	}
	remove, err := store.NewRemoveSource(id, selector)
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	putResult, putErr := Plan(context.Background(), source, change(t, source, put))
	removeResult, removeErr := Plan(context.Background(), source, change(t, source, remove))
	var removed []byte
	if removeErr == nil {
		removed, removeErr = removeResult.Staged.ReadFile(context.Background(), "a.md")
	}

	// Assert.
	if putErr != nil || removeErr != nil {
		t.Fatalf("put/remove exact anonymous source errors = %v, %v", putErr, removeErr)
	}
	if len(putResult.Preview.Writes) != 0 {
		t.Fatalf("mapping-order-only difference caused a write: %#v", putResult.Preview.Writes)
	}
	if bytes.Contains(removed, []byte("policy.md")) || !bytes.Contains(removed, []byte("id: keep")) {
		t.Fatalf("order-independent exact selector removed wrong source:\n%s", removed)
	}
}

func TestPlannerPutAttestedComputation_AtomicInlineAndPathModes(t *testing.T) {
	inlineSource := memorySource{"a.md": []byte("---\n" +
		"type: Note\n" +
		"executor: {resource: old.md, x-executor: keep}\n" +
		"attester: {resource: old.py, x-attester: keep}\n" +
		"---\n# Computation\n\n```sql\nselect old\n```\n")}
	id := ref(t, "a").ID
	inline, err := store.NewPutAttestedComputation(id, store.AttestedComputationContract{
		Mode:              store.AttestedComputationModeInline,
		Runtime:           "bigquery",
		Parameters:        []store.ComputationParameter{{Name: "year", Type: "integer", Required: true}},
		InlineComputation: "select @year",
		InlineLanguage:    "sql",
		Executor:          store.ExecutorContract{Resource: "references/run.md", Receipt: []string{"job_id", "result"}},
		Attester:          store.AttesterContract{Resource: "references/attest.py"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	inlineResult, inlineErr := Plan(context.Background(), inlineSource, change(t, inlineSource, inline))
	var inlineBytes []byte
	if inlineErr == nil {
		inlineBytes, inlineErr = inlineResult.Staged.ReadFile(context.Background(), "a.md")
	}
	inlineRepeatedSource := memorySource{"a.md": append([]byte(nil), inlineBytes...)}
	inlineRepeat, inlineRepeatErr := Plan(context.Background(), inlineRepeatedSource, change(t, inlineRepeatedSource, inline))
	pathOperation, err := store.NewPutAttestedComputation(id, store.AttestedComputationContract{
		Mode:            store.AttestedComputationModeFile,
		Runtime:         "bigquery",
		Parameters:      []store.ComputationParameter{{Name: "year", Type: "integer", Required: true}},
		ComputationPath: "references/query.sql",
		Executor:        store.ExecutorContract{Resource: "references/run.md", Receipt: []string{"job_id", "result"}},
		Attester:        store.AttesterContract{Resource: "references/attest.py"},
	})
	if err != nil {
		t.Fatal(err)
	}
	pathChange := change(t, inlineResult.Staged, pathOperation)
	pathResult, pathErr := Plan(context.Background(), inlineResult.Staged, pathChange)
	var pathBytes []byte
	if pathErr == nil {
		pathBytes, pathErr = pathResult.Staged.ReadFile(context.Background(), "a.md")
	}

	// Assert.
	if inlineErr != nil || inlineRepeatErr != nil || pathErr != nil {
		t.Fatalf("inline/repeat/path errors = %v, %v, %v", inlineErr, inlineRepeatErr, pathErr)
	}
	if len(inlineRepeat.Preview.Writes) != 0 {
		t.Fatalf("second inline desired-state application wrote files: %#v", inlineRepeat.Preview.Writes)
	}
	for _, value := range [][]byte{
		[]byte("type: Attested Computation"),
		[]byte("runtime: bigquery"),
		[]byte("name: year"),
		[]byte("required: true"),
		[]byte("select @year"),
		[]byte("x-executor: keep"),
		[]byte("x-attester: keep"),
	} {
		if !bytes.Contains(inlineBytes, value) {
			t.Fatalf("inline contract missing %q:\n%s", value, inlineBytes)
		}
	}
	if !bytes.Contains(pathBytes, []byte("computation: references/query.sql")) || bytes.Contains(pathBytes, []byte("select @year")) {
		t.Fatalf("path mode did not atomically replace inline mode:\n%s", pathBytes)
	}
}

func TestPlannerSetBundleVersion_UsesOnlyRootIndex(t *testing.T) {
	// Arrange.
	source := memorySource{
		"index.md": []byte("# Notes\n\n* [A](a.md) - A\n"),
		"a.md":     []byte("---\ntype: Note\ntitle: A\ndescription: A\n---\nA\n"),
	}
	op := store.SetBundleVersion{Version: "0.2"}

	// Act.
	result, err := Plan(context.Background(), source, change(t, source, op))
	var got []byte
	if err == nil {
		got, err = result.Staged.ReadFile(context.Background(), "index.md")
	}

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(got, []byte("---\nokf_version: \"0.2\"\n---\n")) {
		t.Fatalf("root index version =\n%s", got)
	}
	if len(result.Preview.Writes) != 1 || result.Preview.Writes[0].Path != "index.md" {
		t.Fatalf("version plan writes = %#v", result.Preview.Writes)
	}
}

func TestMoveConcept_RewritesResolvableV02PathsOnly(t *testing.T) {
	// Arrange.
	source := memorySource{
		"a.md": []byte("---\n" +
			"type: Attested Computation\n" +
			"computation: refs/query.sql\n" +
			"executor: {resource: https://example.test/run}\n" +
			"attester: {resource: all attesters in project X}\n" +
			"---\nA\n"),
		"b.md": []byte("---\n" +
			"type: Note\n" +
			"sources:\n" +
			"  - {id: moved, resource: /a.md}\n" +
			"  - {id: captured, resource: a.md}\n" +
			"  - {id: colon-scope, resource: 'project:acme'}\n" +
			"  - {id: slash-scope, resource: dashboards/exec-revenue}\n" +
			"  - {id: dot-scope, resource: dataset.production}\n" +
			"computation: missing.sql\n" +
			"executor: {resource: a.md}\n" +
			"---\nB\n"),
		"refs/query.sql": []byte("select 1"),
	}
	op := store.MoveConcept{From: ref(t, "a").ID, To: ref(t, "nested/a").ID}

	// Act.
	result, err := Plan(context.Background(), source, change(t, source, op))
	var moved, incoming []byte
	if err == nil {
		moved, err = result.Staged.ReadFile(context.Background(), "nested/a.md")
	}
	if err == nil {
		incoming, err = result.Staged.ReadFile(context.Background(), "b.md")
	}

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(moved, []byte("computation: ../refs/query.sql")) ||
		!bytes.Contains(moved, []byte("https://example.test/run")) ||
		!bytes.Contains(moved, []byte("all attesters in project X")) {
		t.Fatalf("moved outgoing paths =\n%s", moved)
	}
	if !bytes.Contains(incoming, []byte("resource: /nested/a.md")) ||
		bytes.Count(incoming, []byte("resource: nested/a.md")) != 2 ||
		!bytes.Contains(incoming, []byte("resource: 'project:acme'")) ||
		!bytes.Contains(incoming, []byte("resource: dashboards/exec-revenue")) ||
		!bytes.Contains(incoming, []byte("resource: dataset.production")) ||
		!bytes.Contains(incoming, []byte("computation: missing.sql")) {
		t.Fatalf("incoming/broken paths =\n%s", incoming)
	}
}
