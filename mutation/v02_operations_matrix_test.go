package mutation

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/internal/documentlayout"
	"github.com/skosovsky/okf/internal/markdownowner"
	"github.com/skosovsky/okf/store"
	"gopkg.in/yaml.v3"
)

var (
	v02ConceptPathSink string
	v02ConceptSink     bundle.Concept
)

func TestV02FamilyFlowPreservesExtensionsAndReplays(t *testing.T) {
	// Arrange.
	source := memorySource{"a.md": []byte("---\n" +
		"type: Note\n" +
		"generated: {by: process:old, at: 2026-01-01T00:00:00Z, x-generated: keep}\n" +
		"verified: {by: process:nightly, at: 2026-01-02T00:00:00Z, x-verification: keep}\n" +
		"sources: [{id: policy, resource: old.md, title: Old, x-source: keep}]\n" +
		"status: draft\n" +
		"---\nBody\n")}
	id := ref(t, "a").ID
	selector, err := store.SourceByID("policy")
	if err != nil {
		t.Fatal(err)
	}
	count := uint64(42)
	putSource, err := store.NewPutSource(id, store.ProvenanceSource{
		ID: "policy", Resource: "references/policy.md", Title: "Policy", UsageCount: &count,
		UsageWindow: &store.UsageWindow{From: "2026-02-01", To: "2026-02-28"},
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
		putSource, window, lifecycle,
	}
	request := store.ChangeSet{Version: store.ChangeSetFormatVersion, ID: "operation-family-flow", Actor: "principal", BaseRevision: revisionFor(t, source), Operations: operations}

	// Act.
	first, err := Plan(context.Background(), source, request)
	var got []byte
	if err == nil {
		got, err = first.Staged.ReadFile(context.Background(), "a.md")
	}
	replaySource := memorySource{"a.md": append([]byte(nil), got...)}
	request.ID = "operation-family-flow-replay"
	request.BaseRevision = revisionFor(t, replaySource)
	var replay Result
	if err == nil {
		replay, err = Plan(context.Background(), replaySource, request)
	}

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{
		"x-generated: keep", "x-verification: keep", "x-source: keep",
		"reference_agent/gemini-2.5-pro", "human:reviewer", "references/policy.md",
		"usage_count: 42", "from: 2026-02-01", "status: stable", "stale_after: 2026-12-31",
	} {
		if !bytes.Contains(got, []byte(value)) {
			t.Fatalf("family flow lost %q:\n%s", value, got)
		}
	}
	if bytes.Count(got, []byte("human:reviewer")) != 1 || len(replay.Preview.Writes) != 0 {
		t.Fatalf("mixed family replay was not idempotent: writes=%#v\n%s", replay.Preview.Writes, got)
	}
}

func TestV02RemovalAndSharedUsageMatrix(t *testing.T) {
	t.Run("verification and source survivors", func(t *testing.T) {
		// Arrange.
		source := memorySource{"a.md": []byte("---\ntype: Note\n" +
			"verified:\n  - {by: process:remove, at: 2026-01-01T00:00:00Z, x-remove: transient}\n  - {by: human:keep, at: 2026-01-02T00:00:00Z, x-keep: verification}\n" +
			"sources:\n  - {id: remove, resource: remove.md, x-remove: true}\n  - {id: keep, resource: keep.md, x-keep: source}\n---\nBody\n")}
		id := ref(t, "a").ID
		selector, err := store.SourceByID("remove")
		if err != nil {
			t.Fatal(err)
		}
		removeSource, err := store.NewRemoveSource(id, selector)
		if err != nil {
			t.Fatal(err)
		}
		request := change(t, source, store.RemoveVerification{Concept: id, Verification: store.Verification{By: "process:remove", At: "2026-01-01T00:00:00Z"}})
		request.Operations = append(request.Operations, removeSource)

		// Act.
		result, err := Plan(context.Background(), source, request)
		var got []byte
		if err == nil {
			got, err = result.Staged.ReadFile(context.Background(), "a.md")
		}

		// Assert.
		if err != nil {
			t.Fatal(err)
		}
		for _, absent := range []string{"process:remove", "id: remove"} {
			if bytes.Contains(got, []byte(absent)) {
				t.Fatalf("removed value %q remains:\n%s", absent, got)
			}
		}
		for _, survivor := range []string{"human:keep", "x-keep: verification", "id: keep", "x-keep: source"} {
			if !bytes.Contains(got, []byte(survivor)) {
				t.Fatalf("survivor %q was lost:\n%s", survivor, got)
			}
		}
	})

	t.Run("shared absent set remove repeat", func(t *testing.T) {
		// Arrange.
		source := memorySource{"a.md": []byte("---\ntype: Note\n---\nBody\n")}
		id := ref(t, "a").ID
		set, err := store.NewSetUsageWindow(id, nil, &store.UsageWindow{From: "2026-01-01", To: "2026-01-31"})
		if err != nil {
			t.Fatal(err)
		}
		remove, err := store.NewSetUsageWindow(id, nil, nil)
		if err != nil {
			t.Fatal(err)
		}

		// Act.
		inserted, err := Plan(context.Background(), source, change(t, source, set))
		var insertedBytes []byte
		if err == nil {
			insertedBytes, err = inserted.Staged.ReadFile(context.Background(), "a.md")
		}
		setSource := memorySource{"a.md": append([]byte(nil), insertedBytes...)}
		var removed Result
		if err == nil {
			removed, err = Plan(context.Background(), setSource, change(t, setSource, remove))
		}
		var removedBytes []byte
		if err == nil {
			removedBytes, err = removed.Staged.ReadFile(context.Background(), "a.md")
		}
		removedSource := memorySource{"a.md": append([]byte(nil), removedBytes...)}
		var repeated Result
		if err == nil {
			repeated, err = Plan(context.Background(), removedSource, change(t, removedSource, remove))
		}

		// Assert.
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(insertedBytes, []byte("usage_window:")) || bytes.Contains(removedBytes, []byte("usage_window:")) || len(repeated.Preview.Writes) != 0 {
			t.Fatalf("shared usage absent/set/remove/repeat mismatch: repeat=%#v\nset:\n%s\nremoved:\n%s", repeated.Preview.Writes, insertedBytes, removedBytes)
		}
	})
}

func TestV02SelectorSafetyMatrix(t *testing.T) {
	tests := []struct {
		name        string
		frontmatter string
		operation   func(*testing.T, bundle.ConceptID) store.Operation
	}{
		{name: "duplicate source id", frontmatter: "sources:\n  - {id: duplicate, resource: one.md}\n  - {id: duplicate, resource: two.md}\n", operation: func(t *testing.T, id bundle.ConceptID) store.Operation {
			op, err := store.NewPutSource(id, store.ProvenanceSource{ID: "duplicate", Resource: "desired.md"})
			if err != nil {
				t.Fatal(err)
			}
			return op
		}},
		{name: "verification alias", frontmatter: "template: &verification {by: human:reviewer, at: 2026-01-01T00:00:00Z}\nverified: [*verification]\n", operation: func(_ *testing.T, id bundle.ConceptID) store.Operation {
			return store.EnsureVerification{Concept: id, Verification: store.Verification{By: "human:reviewer", At: "2026-01-01T00:00:00Z"}}
		}},
		{name: "verification merge", frontmatter: "template: &verification {by: human:reviewer, at: 2026-01-01T00:00:00Z}\nverified:\n  - {<<: *verification}\n", operation: func(_ *testing.T, id bundle.ConceptID) store.Operation {
			return store.EnsureVerification{Concept: id, Verification: store.Verification{By: "human:reviewer", At: "2026-01-01T00:00:00Z"}}
		}},
		{name: "verification anchor", frontmatter: "verified:\n  - &verification {by: human:reviewer, at: 2026-01-01T00:00:00Z}\n", operation: func(_ *testing.T, id bundle.ConceptID) store.Operation {
			return store.EnsureVerification{Concept: id, Verification: store.Verification{By: "human:reviewer", At: "2026-01-01T00:00:00Z"}}
		}},
		{name: "verification explicit tag", frontmatter: "verified:\n  - !!map {by: human:reviewer, at: 2026-01-01T00:00:00Z}\n", operation: func(_ *testing.T, id bundle.ConceptID) store.Operation {
			return store.EnsureVerification{Concept: id, Verification: store.Verification{By: "human:reviewer", At: "2026-01-01T00:00:00Z"}}
		}},
		{name: "anonymous source comment", frontmatter: "sources:\n  - {resource: policy.md, title: Policy} # provenance-sensitive\n", operation: anonymousPutSourceOperation},
		{name: "anonymous source extension", frontmatter: "sources:\n  - {resource: policy.md, title: Policy, x-source: keep}\n", operation: anonymousPutSourceOperation},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			document := []byte("---\ntype: Note\n" + test.frontmatter + "---\nBody\n")
			source := memorySource{"a.md": append([]byte(nil), document...)}

			// Act.
			result, err := Plan(context.Background(), source, change(t, source, test.operation(t, ref(t, "a").ID)))

			// Assert.
			if err == nil {
				t.Fatal("unsafe or ambiguous selector candidate was accepted")
			}
			if result.Staged != nil || len(result.Preview.Writes) != 0 || len(result.Preview.Deletes) != 0 || len(result.Preview.Renames) != 0 || len(result.Preview.Plan) != 0 {
				t.Fatalf("rejected selector exposed stage or plan: %#v", result)
			}
			if !bytes.Equal(source["a.md"], document) {
				t.Fatal("rejected selector mutated caller bytes")
			}
		})
	}
}

func anonymousPutSourceOperation(t *testing.T, id bundle.ConceptID) store.Operation {
	t.Helper()
	op, err := store.NewPutSource(id, store.ProvenanceSource{Resource: "policy.md", Title: "Policy"})
	if err != nil {
		t.Fatal(err)
	}
	return op
}

func TestV02AnonymousExactSelectorOrderAndRemoval(t *testing.T) {
	// Arrange.
	source := memorySource{"a.md": []byte("---\ntype: Note\nsources:\n  - {title: Policy, resource: policy.md}\n  - {id: keep, resource: keep.md}\n---\nBody\n")}
	exact := store.ProvenanceSource{Resource: "policy.md", Title: "Policy"}
	put, err := store.NewPutSource(ref(t, "a").ID, exact)
	if err != nil {
		t.Fatal(err)
	}
	selector, err := store.SourceByExact(exact)
	if err != nil {
		t.Fatal(err)
	}
	remove, err := store.NewRemoveSource(ref(t, "a").ID, selector)
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	putResult, putErr := Plan(context.Background(), source, change(t, source, put))
	removeResult, removeErr := Plan(context.Background(), source, change(t, source, remove))
	var got []byte
	if removeErr == nil {
		got, removeErr = removeResult.Staged.ReadFile(context.Background(), "a.md")
	}

	// Assert.
	if putErr != nil || removeErr != nil {
		t.Fatalf("put/remove errors = %v, %v", putErr, removeErr)
	}
	if len(putResult.Preview.Writes) != 0 || bytes.Contains(got, []byte("policy.md")) || !bytes.Contains(got, []byte("id: keep")) {
		t.Fatalf("exact selector key-order/removal mismatch: put=%#v\n%s", putResult.Preview.Writes, got)
	}
}

func TestV02InlineComputationExtensionsReplayAndAtomicFileTransition(t *testing.T) {
	// Arrange.
	source := memorySource{"a.md": []byte("---\ntype: Note\nexecutor: {resource: old.md, x-executor: keep}\nattester: {resource: old.py, x-attester: keep}\n---\n# Computation\n\n```sql\nselect old\n```\n")}
	id := ref(t, "a").ID
	contract := store.AttestedComputationContract{Mode: store.AttestedComputationModeInline, Runtime: "bigquery", Parameters: []store.ComputationParameter{{Name: "year", Type: "integer", Required: true}}, InlineComputation: "select @year", InlineLanguage: "sql", Executor: store.ExecutorContract{Resource: "references/run.md", Receipt: []string{"job_id", "result"}}, Attester: store.AttesterContract{Resource: "references/attest.py"}}
	inline, err := store.NewPutAttestedComputation(id, contract)
	if err != nil {
		t.Fatal(err)
	}
	contract.Mode, contract.InlineComputation, contract.InlineLanguage, contract.ComputationPath = store.AttestedComputationModeFile, "", "", "references/query.sql"
	file, err := store.NewPutAttestedComputation(id, contract)
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	first, err := Plan(context.Background(), source, change(t, source, inline))
	var inlineBytes []byte
	if err == nil {
		inlineBytes, err = first.Staged.ReadFile(context.Background(), "a.md")
	}
	inlineSource := memorySource{"a.md": append([]byte(nil), inlineBytes...)}
	var replay, transitioned Result
	if err == nil {
		replay, err = Plan(context.Background(), inlineSource, change(t, inlineSource, inline))
	}
	if err == nil {
		transitioned, err = Plan(context.Background(), inlineSource, change(t, inlineSource, file))
	}
	var fileBytes []byte
	if err == nil {
		fileBytes, err = transitioned.Staged.ReadFile(context.Background(), "a.md")
	}

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"type: Attested Computation", "runtime: bigquery", "name: year", "required: true", "select @year", "x-executor: keep", "x-attester: keep"} {
		if !bytes.Contains(inlineBytes, []byte(want)) {
			t.Fatalf("inline contract lost %q:\n%s", want, inlineBytes)
		}
	}
	if len(replay.Preview.Writes) != 0 || !bytes.Contains(fileBytes, []byte("computation: references/query.sql")) || bytes.Contains(fileBytes, []byte("select @year")) {
		t.Fatalf("inline replay/file transition mismatch: replay=%#v\n%s", replay.Preview.Writes, fileBytes)
	}
}

func TestV02SetBundleVersionWritesOnlyRootIndex(t *testing.T) {
	// Arrange.
	source := memorySource{"index.md": []byte("# Notes\n\n* [A](a.md) - A\n"), "a.md": []byte("---\ntype: Note\ntitle: A\ndescription: A\n---\nA\n")}

	// Act.
	result, err := Plan(context.Background(), source, change(t, source, store.SetBundleVersion{Version: "0.2"}))
	var got []byte
	if err == nil {
		got, err = result.Staged.ReadFile(context.Background(), "index.md")
	}

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(got, []byte("---\nokf_version: \"0.2\"\n---\n")) || len(result.Preview.Writes) != 1 || result.Preview.Writes[0].Path != "index.md" {
		t.Fatalf("root-only version plan = %#v\n%s", result.Preview.Writes, got)
	}
}

func TestPlanSourceIDOperationsPreserveOpaqueAnonymousSibling(t *testing.T) {
	tests := []struct {
		name       string
		provenance string
		extension  string
	}{
		{name: "duplicate", extension: "    opaque: first\n    opaque: second\n"},
		{name: "alias", provenance: "shared: &shared preserved\n", extension: "    opaque: *shared\n"},
		{name: "merge", provenance: "defaults: &defaults {opaque: preserved}\n", extension: "    <<: *defaults\n"},
	}
	for _, test := range tests {
		for _, operation := range []string{"append", "update", "remove"} {
			t.Run(test.name+"/"+operation, func(t *testing.T) {
				// Arrange.
				opaqueItem := "  - resource: anonymous.md\n" + test.extension
				document := []byte("---\ntype: Note\n" + test.provenance +
					"sources:\n" + opaqueItem +
					"  - id: selected\n" +
					"    resource: old.md\n" +
					"---\nBody\n")
				source := memorySource{"a.md": document}
				id := ref(t, "a").ID
				var op store.Operation
				switch operation {
				case "append":
					var err error
					op, err = store.NewPutSource(id, store.ProvenanceSource{ID: "added", Resource: "added.md"})
					if err != nil {
						t.Fatal(err)
					}
				case "update":
					var err error
					op, err = store.NewPutSource(id, store.ProvenanceSource{ID: "selected", Resource: "new.md"})
					if err != nil {
						t.Fatal(err)
					}
				case "remove":
					selector, err := store.SourceByID("selected")
					if err != nil {
						t.Fatal(err)
					}
					op, err = store.NewRemoveSource(id, selector)
					if err != nil {
						t.Fatal(err)
					}
				}

				// Act.
				result, planErr := Plan(context.Background(), source, change(t, source, op))
				var updated []byte
				if planErr == nil {
					updated, planErr = result.Staged.ReadFile(context.Background(), "a.md")
				}

				// Assert.
				if planErr != nil {
					t.Fatal(planErr)
				}
				if bytes.Count(updated, []byte(opaqueItem)) != 1 ||
					!bytes.Contains(updated, []byte(test.provenance)) {
					t.Fatalf("opaque anonymous sibling changed:\n%s", updated)
				}
				switch operation {
				case "append":
					if !bytes.Contains(updated, []byte("id: added")) {
						t.Fatalf("appended source missing:\n%s", updated)
					}
				case "update":
					if !bytes.Contains(updated, []byte("resource: new.md")) {
						t.Fatalf("updated source missing:\n%s", updated)
					}
				case "remove":
					if bytes.Contains(updated, []byte("id: selected")) {
						t.Fatalf("removed source remains:\n%s", updated)
					}
				}
			})
		}
	}
}

func TestPlanAnonymousExactRemovalFailsOnUnresolvedOpaqueSibling(t *testing.T) {
	for _, test := range []struct {
		name       string
		provenance string
		extension  string
		code       string
	}{
		{name: "duplicate", extension: "    opaque: first\n    opaque: second\n", code: yamlCodeDuplicateTouchedKey},
		{name: "alias", provenance: "shared: &shared preserved\n", extension: "    opaque: *shared\n", code: yamlCodeAliasProvenance},
		{name: "merge", provenance: "defaults: &defaults {opaque: preserved}\n", extension: "    <<: *defaults\n", code: yamlCodeAliasProvenance},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			document := []byte("---\ntype: Note\n" + test.provenance +
				"sources:\n" +
				"  - resource: opaque.md\n" + test.extension +
				"  - resource: selected.md\n" +
				"---\nBody\n")
			source := memorySource{"a.md": document}
			selector, err := store.SourceByExact(store.ProvenanceSource{Resource: "selected.md"})
			if err != nil {
				t.Fatal(err)
			}
			operation, err := store.NewRemoveSource(ref(t, "a").ID, selector)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			result, planErr := Plan(context.Background(), source, change(t, source, operation))

			// Assert.
			var presentationErr *PresentationError
			if !errors.Is(planErr, ErrAmbiguousPresentation) ||
				!errors.As(planErr, &presentationErr) || presentationErr.Code != test.code ||
				presentationErr.Path != "a.md" || presentationErr.Operation != "remove_source" {
				t.Fatalf("typed Plan() error = %#v", planErr)
			}
			if result.Staged != nil || len(result.Preview.Writes) != 0 ||
				len(result.Preview.Renames) != 0 || len(result.Preview.Plan) != 0 {
				t.Fatalf("rejected exact removal exposed stage: %#v", result)
			}
			if !bytes.Equal(source["a.md"], document) {
				t.Fatal("rejected exact removal mutated caller source")
			}
		})
	}
}

func TestVerificationOperations_AbsentBareAndListDesiredStates(t *testing.T) {
	event := store.Verification{By: "human:reviewer", At: "2026-01-01T00:00:00Z"}
	tests := []struct {
		name        string
		frontmatter string
		operation   func(bundle.ConceptID) store.Operation
		want        []byte
		absent      []byte
		wantWrites  int
	}{
		{
			name:        "insert when absent",
			frontmatter: "type: Note\n",
			operation: func(id bundle.ConceptID) store.Operation {
				return store.EnsureVerification{Concept: id, Verification: event}
			},
			want:       []byte("verified:"),
			wantWrites: 1,
		},
		{
			name:        "bare ensure noop",
			frontmatter: "type: Note\nverified: {by: human:reviewer, at: 2026-01-01T00:00:00Z}\n",
			operation: func(id bundle.ConceptID) store.Operation {
				return store.EnsureVerification{Concept: id, Verification: event}
			},
			want: []byte("verified:"),
		},
		{
			name:        "bare remove last",
			frontmatter: "type: Note\nverified: {by: human:reviewer, at: 2026-01-01T00:00:00Z}\n",
			operation: func(id bundle.ConceptID) store.Operation {
				return store.RemoveVerification{Concept: id, Verification: event}
			},
			absent:     []byte("verified:"),
			wantWrites: 1,
		},
		{
			name: "list ensure noop",
			frontmatter: "type: Note\nverified:\n" +
				"  - {by: process:nightly, at: 2025-12-31T00:00:00Z}\n" +
				"  - {by: human:reviewer, at: 2026-01-01T00:00:00Z}\n",
			operation: func(id bundle.ConceptID) store.Operation {
				return store.EnsureVerification{Concept: id, Verification: event}
			},
			want: []byte("human:reviewer"),
		},
		{
			name: "list remove one",
			frontmatter: "type: Note\nverified:\n" +
				"  - {by: process:nightly, at: 2025-12-31T00:00:00Z}\n" +
				"  - {by: human:reviewer, at: 2026-01-01T00:00:00Z}\n",
			operation: func(id bundle.ConceptID) store.Operation {
				return store.RemoveVerification{Concept: id, Verification: event}
			},
			want:       []byte("process:nightly"),
			absent:     []byte("human:reviewer"),
			wantWrites: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			source := memorySource{"a.md": []byte("---\n" + test.frontmatter + "---\nBody\n")}
			operation := test.operation(ref(t, "a").ID)

			// Act.
			result, err := Plan(context.Background(), source, change(t, source, operation))
			var got []byte
			if err == nil {
				got, err = result.Staged.ReadFile(context.Background(), "a.md")
			}
			repeatedSource := memorySource{"a.md": append([]byte(nil), got...)}
			var repeated Result
			var repeatErr error
			if err == nil {
				repeated, repeatErr = Plan(context.Background(), repeatedSource, change(t, repeatedSource, operation))
			}

			// Assert.
			if err != nil || repeatErr != nil {
				t.Fatalf("first/repeat errors = %v, %v", err, repeatErr)
			}
			if len(result.Preview.Writes) != test.wantWrites {
				t.Fatalf("writes = %#v, want %d", result.Preview.Writes, test.wantWrites)
			}
			if len(test.want) != 0 && !bytes.Contains(got, test.want) {
				t.Fatalf("desired value %q absent:\n%s", test.want, got)
			}
			if len(test.absent) != 0 && bytes.Contains(got, test.absent) {
				t.Fatalf("removed value %q remains:\n%s", test.absent, got)
			}
			if len(repeated.Preview.Writes) != 0 {
				t.Fatalf("second desired-state application wrote files: %#v", repeated.Preview.Writes)
			}
		})
	}
}

func TestSetGenerated_AbsentInsertAndOptionalAtRemovalPreserveExtensions(t *testing.T) {
	tests := []struct {
		name        string
		frontmatter string
		generated   store.Generation
		want        []byte
		absent      []byte
	}{
		{
			name:        "absent insert",
			frontmatter: "type: Note\n",
			generated:   store.Generation{By: "process:nightly", At: "2026-01-01T00:00:00Z"},
			want:        []byte("at: 2026-01-01T00:00:00Z"),
		},
		{
			name: "remove optional at",
			frontmatter: "type: Note\ngenerated:\n" +
				"  by: process:old\n  at: 2025-01-01T00:00:00Z\n" +
				"  x-generated: keep # generated-comment\n",
			generated: store.Generation{By: "process:nightly"},
			want:      []byte("x-generated: keep # generated-comment"),
			absent:    []byte("  at:"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			source := memorySource{"a.md": []byte("---\n" + test.frontmatter + "---\nBody\n")}
			operation := store.SetGenerated{Concept: ref(t, "a").ID, Generated: test.generated}

			// Act.
			result, err := Plan(context.Background(), source, change(t, source, operation))
			var got []byte
			if err == nil {
				got, err = result.Staged.ReadFile(context.Background(), "a.md")
			}
			repeatedSource := memorySource{"a.md": append([]byte(nil), got...)}
			var repeated Result
			if err == nil {
				repeated, err = Plan(context.Background(), repeatedSource, change(t, repeatedSource, operation))
			}

			// Assert.
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range [][]byte{[]byte("generated:"), []byte("by: process:nightly"), test.want} {
				if !bytes.Contains(got, want) {
					t.Fatalf("generated value %q absent:\n%s", want, got)
				}
			}
			if len(test.absent) != 0 && bytes.Contains(got, test.absent) {
				t.Fatalf("removed generated value remains:\n%s", got)
			}
			if len(repeated.Preview.Writes) != 0 {
				t.Fatalf("second generated application wrote files: %#v", repeated.Preview.Writes)
			}
		})
	}
}

func TestApplyV02ConceptOperation_ConceptPathLookupPropagatesContextAndNotFound(t *testing.T) {
	// Arrange.
	source := memorySource{"a.md": []byte("---\ntype: Note\n---\nA\n")}
	loaded, err := bundle.Load(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("concept path lookup canceled")
	canceled := &v02LookupErrorContext{Context: context.Background(), err: injected}
	canceledState := &planState{ctx: canceled, bundle: loaded}
	readInjected := errors.New("concept file read canceled")
	readCanceled := &v02FailAfterErrChecksContext{
		Context: context.Background(),
		allowed: 2,
		err:     readInjected,
	}
	readCanceledState := &planState{ctx: readCanceled, bundle: loaded}
	missingState := &planState{ctx: context.Background(), bundle: loaded}
	operation := store.SetGenerated{Concept: ref(t, "a").ID, Generated: store.Generation{By: "process:test"}}
	mutateCalled := false
	mutate := func(p *presentation) ([]byte, error) {
		mutateCalled = true
		return append([]byte(nil), p.data...), nil
	}

	// Act.
	canceledErr := canceledState.applyV02ConceptOperation(ref(t, "a").ID, "set_generated", operation, mutate)
	readCanceledErr := readCanceledState.applyV02ConceptOperation(ref(t, "a").ID, "set_generated", operation, mutate)
	missingErr := missingState.applyV02ConceptOperation(ref(t, "missing").ID, "set_generated", operation, mutate)
	existingData, existingOK, existingErr := readBundleFileContext(context.Background(), loaded, "a.md")
	missingData, missingOK, missingReadErr := readBundleFileContext(context.Background(), loaded, "missing.md")
	nilData, nilOK, nilReadErr := readBundleFileContext(context.Background(), nil, "a.md")

	// Assert.
	if canceledErr != injected {
		t.Fatalf("canceled lookup error = %#v, want exact injected error", canceledErr)
	}
	if readCanceledErr != readInjected {
		t.Fatalf("canceled read error = %#v, want exact injected error", readCanceledErr)
	}
	var invalidChange *store.InvalidChangeSet
	if !errors.As(missingErr, &invalidChange) || invalidChange.Code != "missing_concept" {
		t.Fatalf("missing lookup error = %#v", missingErr)
	}
	if existingErr != nil || !existingOK || !bytes.Equal(existingData, source["a.md"]) {
		t.Fatalf("existing read = (%q, %t, %v), want exact bytes/true/nil", existingData, existingOK, existingErr)
	}
	if missingReadErr != nil || missingOK || missingData != nil {
		t.Fatalf("missing read = (%q, %t, %v), want nil/false/nil", missingData, missingOK, missingReadErr)
	}
	if nilReadErr != nil || nilOK || nilData != nil {
		t.Fatalf("nil-bundle read = (%q, %t, %v), want nil/false/nil", nilData, nilOK, nilReadErr)
	}
	if mutateCalled {
		t.Fatal("mutation callback ran after canceled/missing concept lookup")
	}
}

func TestApplyV02ConceptOperation_PathLookupAvoidsDeepConceptCloneAllocations(t *testing.T) {
	// Arrange.
	var frontmatter strings.Builder
	frontmatter.WriteString("---\ntype: Note\nsources:\n")
	for index := 0; index < 512; index++ {
		frontmatter.WriteString("  - resource: source.md\n    title: allocation fixture\n")
	}
	frontmatter.WriteString("---\n")
	frontmatter.WriteString(strings.Repeat("Large Markdown body.\n", 16<<10))
	source := memorySource{"a.md": []byte(frontmatter.String())}
	loaded, err := bundle.Load(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	id := ref(t, "a").ID
	var pathErr error

	// Act.
	pathAllocs := testing.AllocsPerRun(100, func() {
		var ok bool
		v02ConceptPathSink, ok, pathErr = loaded.ConceptPathContext(context.Background(), id)
		if !ok {
			pathErr = errors.New("concept path missing")
		}
	})
	cloneAllocs := testing.AllocsPerRun(20, func() {
		v02ConceptSink, _ = loaded.Get(id)
	})

	// Assert.
	if pathErr != nil || v02ConceptPathSink != "a.md" {
		t.Fatalf("ConceptPathContext() = (%q, %v)", v02ConceptPathSink, pathErr)
	}
	if pathAllocs*8 >= cloneAllocs {
		t.Fatalf("path lookup allocations = %.0f, deep clone allocations = %.0f; want at least 8x fewer",
			pathAllocs, cloneAllocs)
	}
}

func TestPutSource_AbsentInsertSupportsMaxUint64AndIsIdempotent(t *testing.T) {
	// Arrange.
	source := memorySource{"a.md": []byte("---\ntype: Note\n---\nBody\n")}
	id := ref(t, "a").ID
	maximum := ^uint64(0)
	operation, err := store.NewPutSource(id, store.ProvenanceSource{
		ID: "policy", Resource: "policy.md", UsageCount: &maximum,
		UsageWindow: &store.UsageWindow{From: "2026-01-01", To: "2026-01-31"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	result, err := Plan(context.Background(), source, change(t, source, operation))
	var got []byte
	if err == nil {
		got, err = result.Staged.ReadFile(context.Background(), "a.md")
	}
	repeatedSource := memorySource{"a.md": append([]byte(nil), got...)}
	var repeated Result
	var repeatErr error
	if err == nil {
		repeated, repeatErr = Plan(context.Background(), repeatedSource, change(t, repeatedSource, operation))
	}

	// Assert.
	if err != nil || repeatErr != nil {
		t.Fatalf("first/repeat errors = %v, %v", err, repeatErr)
	}
	if !bytes.Contains(got, []byte("usage_count: 18446744073709551615")) {
		t.Fatalf("MaxUint64 usage_count was narrowed:\n%s", got)
	}
	if len(repeated.Preview.Writes) != 0 {
		t.Fatalf("second desired-state application wrote files: %#v", repeated.Preview.Writes)
	}
}

func TestV02OpenStrings_ControlAndMultilineRoundTripThroughYAMLMutation(t *testing.T) {
	// Arrange.
	source := memorySource{"a.md": []byte("---\ntype: Note\n---\nBody\n")}
	id := ref(t, "a").ID
	longValue := strings.Repeat("domain-value-", 400)
	provenance := store.ProvenanceSource{
		ID:       " source\nid\x00 ",
		Resource: " scope:all\nassets\x7f ",
		Title:    longValue,
		Author:   " team\nfinance ",
	}
	computation := store.AttestedComputationContract{
		Mode:    store.AttestedComputationModeFile,
		Runtime: " custom/runtime:v1?scope=all projects ",
		Parameters: []store.ComputationParameter{{
			Name: "fiscal\nyear",
			Type: longValue,
		}},
		ComputationPath: " queries/\x00revenue.sql ",
		Executor: store.ExecutorContract{
			Resource: " executor/\x7frun.md ",
			Receipt:  []string{" result\nfield "},
		},
		Attester: store.AttesterContract{Resource: " attester/\x00check.py "},
	}
	putSource, err := store.NewPutSource(id, provenance)
	if err != nil {
		t.Fatal(err)
	}
	putComputation, err := store.NewPutAttestedComputation(id, computation)
	if err != nil {
		t.Fatal(err)
	}
	request := change(t, source, putSource)
	request.Operations = append(request.Operations, putComputation)

	// Act.
	result, err := Plan(context.Background(), source, request)
	var got []byte
	if err == nil {
		got, err = result.Staged.ReadFile(context.Background(), "a.md")
	}
	var presentation *presentation
	if err == nil {
		presentation, err = parsePresentationContext(context.Background(), got)
	}
	repeatedSource := memorySource{"a.md": append([]byte(nil), got...)}
	var repeated Result
	if err == nil {
		repeatedRequest := change(t, repeatedSource, putSource)
		repeatedRequest.Operations = append(repeatedRequest.Operations, putComputation)
		repeated, err = Plan(
			context.Background(),
			repeatedSource,
			repeatedRequest,
		)
	}

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if bytes.IndexByte(got, 0) >= 0 || bytes.IndexByte(got, 0x7f) >= 0 {
		t.Fatalf("YAML renderer emitted raw control bytes:\n%q", got)
	}
	requireScalar := func(mapping *yaml.Node, key, want string) {
		t.Helper()
		match, ok, matchErr := presentation.structuralMappingEntry(mapping, key)
		if matchErr != nil || !ok {
			t.Fatalf("frontmatter key %q = present:%t error:%v", key, ok, matchErr)
		}
		if match.value.Kind != yaml.ScalarNode || match.value.Value != want {
			t.Fatalf("frontmatter key %q = kind:%d value:%q, want scalar %q",
				key, match.value.Kind, match.value.Value, want)
		}
	}
	sourcesMatch, ok, matchErr := presentation.structuralMappingEntry(presentation.root, "sources")
	if matchErr != nil || !ok || sourcesMatch.value.Kind != yaml.SequenceNode ||
		len(sourcesMatch.value.Content) != 1 {
		t.Fatalf("sources = %#v, present:%t error:%v", sourcesMatch.value, ok, matchErr)
	}
	sourceNode := sourcesMatch.value.Content[0]
	requireScalar(sourceNode, "id", provenance.ID)
	requireScalar(sourceNode, "resource", provenance.Resource)
	requireScalar(sourceNode, "title", provenance.Title)
	requireScalar(sourceNode, "author", provenance.Author)

	requireScalar(presentation.root, "runtime", computation.Runtime)
	parametersMatch, ok, matchErr := presentation.structuralMappingEntry(presentation.root, "parameters")
	if matchErr != nil || !ok || parametersMatch.value.Kind != yaml.SequenceNode ||
		len(parametersMatch.value.Content) != 1 {
		t.Fatalf("parameters = %#v, present:%t error:%v", parametersMatch.value, ok, matchErr)
	}
	parameterNode := parametersMatch.value.Content[0]
	requireScalar(parameterNode, "name", computation.Parameters[0].Name)
	requireScalar(parameterNode, "type", computation.Parameters[0].Type)
	requireScalar(presentation.root, "computation", computation.ComputationPath)

	executorMatch, ok, matchErr := presentation.structuralMappingEntry(presentation.root, "executor")
	if matchErr != nil || !ok || executorMatch.value.Kind != yaml.MappingNode {
		t.Fatalf("executor = %#v, present:%t error:%v", executorMatch.value, ok, matchErr)
	}
	requireScalar(executorMatch.value, "resource", computation.Executor.Resource)
	receiptMatch, ok, matchErr := presentation.structuralMappingEntry(executorMatch.value, "receipt")
	if matchErr != nil || !ok || receiptMatch.value.Kind != yaml.SequenceNode ||
		len(receiptMatch.value.Content) != 1 ||
		receiptMatch.value.Content[0].Value != computation.Executor.Receipt[0] {
		t.Fatalf("receipt = %#v, present:%t error:%v", receiptMatch.value, ok, matchErr)
	}

	attesterMatch, ok, matchErr := presentation.structuralMappingEntry(presentation.root, "attester")
	if matchErr != nil || !ok || attesterMatch.value.Kind != yaml.MappingNode {
		t.Fatalf("attester = %#v, present:%t error:%v", attesterMatch.value, ok, matchErr)
	}
	requireScalar(attesterMatch.value, "resource", computation.Attester.Resource)
	if len(repeated.Preview.Writes) != 0 {
		t.Fatalf("second open-string application wrote files: %#v", repeated.Preview.Writes)
	}
}

func TestPutSource_InvalidExistingUsageCountFailsClosedWithoutStage(t *testing.T) {
	for _, value := range []string{"-1", "18446744073709551616"} {
		t.Run(value, func(t *testing.T) {
			// Arrange.
			source := memorySource{"a.md": []byte("---\ntype: Note\nsources:\n" +
				"  - id: policy\n    resource: policy.md\n    usage_count: " + value + "\n---\nBody\n")}
			count := uint64(1)
			operation, err := store.NewPutSource(ref(t, "a").ID, store.ProvenanceSource{
				ID: "policy", Resource: "policy.md", UsageCount: &count,
			})
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			result, err := Plan(context.Background(), source, change(t, source, operation))

			// Assert.
			if err == nil {
				t.Fatalf("invalid existing usage_count %q was rewritten instead of rejected", value)
			}
			if result.Staged != nil || len(result.Preview.Writes) != 0 || len(result.Preview.Renames) != 0 {
				t.Fatalf("rejected usage_count exposed stage: %#v", result)
			}
		})
	}
}

func TestPutSource_RemovesEveryOmittedKnownFieldAndPreservesUnknownTrivia(t *testing.T) {
	// Arrange.
	source := memorySource{"a.md": []byte("---\ntype: Note\nsources:\n" +
		"  - id: policy\n" +
		"    resource: old.md\n" +
		"    title: Old title\n" +
		"    author: Old author\n" +
		"    usage_count: 7\n" +
		"    last_modified: 2026-01-01\n" +
		"    usage_window:\n" +
		"      from: 2026-01-01\n" +
		"      to: 2026-01-31\n" +
		"    x-source: keep # source-comment\n" +
		"---\nBody\n")}
	operation, err := store.NewPutSource(ref(t, "a").ID, store.ProvenanceSource{
		ID: "policy", Resource: "policy.md",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	result, err := Plan(context.Background(), source, change(t, source, operation))
	var got []byte
	if err == nil {
		got, err = result.Staged.ReadFile(context.Background(), "a.md")
	}
	repeatedSource := memorySource{"a.md": append([]byte(nil), got...)}
	var repeated Result
	if err == nil {
		repeated, err = Plan(context.Background(), repeatedSource, change(t, repeatedSource, operation))
	}

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte("resource: policy.md")) ||
		!bytes.Contains(got, []byte("x-source: keep # source-comment")) {
		t.Fatalf("desired/unknown source fields missing:\n%s", got)
	}
	for _, removed := range [][]byte{
		[]byte("title:"), []byte("author:"), []byte("usage_count:"),
		[]byte("last_modified:"), []byte("usage_window:"),
	} {
		if bytes.Contains(got, removed) {
			t.Fatalf("omitted known source field %q remains:\n%s", removed, got)
		}
	}
	if len(repeated.Preview.Writes) != 0 {
		t.Fatalf("second source application wrote files: %#v", repeated.Preview.Writes)
	}
}

func TestPutSource_UsageWindowThreeWayPreservesNestedExtensionsAcrossStylesAndNewlines(t *testing.T) {
	for _, style := range []string{"block", "flow"} {
		for _, newline := range []string{"\n", "\r\n"} {
			newlineName := "LF"
			if newline == "\r\n" {
				newlineName = "CRLF"
			}
			for _, mode := range []string{"set", "remove", "noop"} {
				t.Run(style+"/"+mode+"/"+newlineName, func(t *testing.T) {
					// Arrange.
					initialFrom, initialTo := "2026-01-01", "2026-01-31"
					if mode == "noop" {
						initialFrom, initialTo = "2026-02-01", "2026-02-28"
					}
					initialResource := "policy.md"
					if mode == "set" {
						initialResource = "old.md"
					}
					windowExtension := "x-window: keep"
					if mode != "remove" {
						windowExtension += " # window-comment"
					}
					var frontmatter string
					if style == "block" {
						frontmatter = "type: Note\nsources:\n" +
							"  - id: policy\n" +
							"    resource: " + initialResource + "\n" +
							"    usage_window:\n" +
							"      from: " + initialFrom + "\n" +
							"      to: " + initialTo + "\n" +
							"      " + windowExtension + "\n" +
							"    x-source: keep # source-comment\n"
					} else {
						frontmatter = "type: Note\nsources:\n" +
							"  - {id: policy, resource: " + initialResource + ", usage_window: {from: " + initialFrom +
							", to: " + initialTo + ", x-window: keep}, x-source: keep}"
						if mode == "noop" {
							frontmatter += " # source-comment"
						}
						frontmatter += "\nx-root: keep # source-comment\n"
					}
					source := memorySource{"a.md": []byte(strings.ReplaceAll(
						"---\n"+frontmatter+"---\nBody\n", "\n", newline,
					))}
					desired := store.ProvenanceSource{ID: "policy", Resource: "policy.md"}
					if mode != "remove" {
						desired.UsageWindow = &store.UsageWindow{From: "2026-02-01", To: "2026-02-28"}
					}
					operation, err := store.NewPutSource(ref(t, "a").ID, desired)
					if err != nil {
						t.Fatal(err)
					}

					// Act.
					result, err := Plan(context.Background(), source, change(t, source, operation))
					var got []byte
					if err == nil {
						got, err = result.Staged.ReadFile(context.Background(), "a.md")
					}
					repeatedSource := memorySource{"a.md": append([]byte(nil), got...)}
					var repeated Result
					if err == nil {
						repeated, err = Plan(context.Background(), repeatedSource, change(t, repeatedSource, operation))
					}

					// Assert.
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Contains(got, []byte("x-source: keep")) ||
						!bytes.Contains(got, []byte("# source-comment")) {
						t.Fatalf("source extension/comment was lost:\n%s", got)
					}
					if !bytes.Contains(got, []byte("resource: policy.md")) {
						t.Fatalf("desired source resource absent:\n%s", got)
					}
					if mode == "remove" {
						if bytes.Contains(got, []byte("usage_window:")) {
							t.Fatalf("removed usage window remains:\n%s", got)
						}
					} else {
						for _, want := range [][]byte{
							[]byte("from: 2026-02-01"),
							[]byte("to: 2026-02-28"),
							[]byte("x-window: keep"),
						} {
							if !bytes.Contains(got, want) {
								t.Fatalf("nested usage-window value %q was lost:\n%s", want, got)
							}
						}
					}
					wantWrites := 1
					if mode == "noop" {
						wantWrites = 0
					}
					if len(result.Preview.Writes) != wantWrites {
						t.Fatalf("writes = %#v, want %d", result.Preview.Writes, wantWrites)
					}
					if len(repeated.Preview.Writes) != 0 {
						t.Fatalf("replayed PutSource wrote files: %#v", repeated.Preview.Writes)
					}
					if newline == "\r\n" && bytes.Contains(bytes.ReplaceAll(got, []byte("\r\n"), nil), []byte("\n")) {
						t.Fatalf("CRLF document gained lone LF:\n%q", got)
					}
				})
			}
		}
	}
}

func TestUsageWindowUpdates_PreserveUnknownNestedExtensionsAndTrivia(t *testing.T) {
	tests := []struct {
		name        string
		frontmatter string
		operation   func(t *testing.T, id bundle.ConceptID) store.Operation
	}{
		{
			name: "shared",
			frontmatter: "type: Note\nusage_window:\n" +
				"  from: 2026-01-01\n  to: 2026-01-31\n  x-window: keep # window-comment\n",
			operation: func(t *testing.T, id bundle.ConceptID) store.Operation {
				t.Helper()
				operation, err := store.NewSetUsageWindow(id, nil, &store.UsageWindow{From: "2026-02-01", To: "2026-02-28"})
				if err != nil {
					t.Fatal(err)
				}
				return operation
			},
		},
		{
			name: "per source",
			frontmatter: "type: Note\nsources:\n  - id: policy\n    resource: policy.md\n    usage_window:\n" +
				"      from: 2026-01-01\n      to: 2026-01-31\n      x-window: keep # window-comment\n",
			operation: func(t *testing.T, id bundle.ConceptID) store.Operation {
				t.Helper()
				selector, err := store.SourceByID("policy")
				if err != nil {
					t.Fatal(err)
				}
				operation, err := store.NewSetUsageWindow(id, &selector, &store.UsageWindow{From: "2026-02-01", To: "2026-02-28"})
				if err != nil {
					t.Fatal(err)
				}
				return operation
			},
		},
		{
			name: "put source",
			frontmatter: "type: Note\nsources:\n  - id: policy\n    resource: old.md\n    usage_window:\n" +
				"      from: 2026-01-01\n      to: 2026-01-31\n      x-window: keep # window-comment\n",
			operation: func(t *testing.T, id bundle.ConceptID) store.Operation {
				t.Helper()
				operation, err := store.NewPutSource(id, store.ProvenanceSource{
					ID: "policy", Resource: "policy.md",
					UsageWindow: &store.UsageWindow{From: "2026-02-01", To: "2026-02-28"},
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
			source := memorySource{"a.md": []byte("---\n" + test.frontmatter + "---\nBody\n")}
			operation := test.operation(t, ref(t, "a").ID)

			// Act.
			result, err := Plan(context.Background(), source, change(t, source, operation))
			var got []byte
			if err == nil {
				got, err = result.Staged.ReadFile(context.Background(), "a.md")
			}
			repeatedSource := memorySource{"a.md": append([]byte(nil), got...)}
			var repeated Result
			var repeatErr error
			if err == nil {
				repeated, repeatErr = Plan(context.Background(), repeatedSource, change(t, repeatedSource, operation))
			}

			// Assert.
			if err != nil || repeatErr != nil {
				t.Fatalf("first/repeat errors = %v, %v", err, repeatErr)
			}
			for _, value := range [][]byte{
				[]byte("from: 2026-02-01"),
				[]byte("to: 2026-02-28"),
				[]byte("x-window: keep # window-comment"),
			} {
				if !bytes.Contains(got, value) {
					t.Fatalf("nested value/trivia %q was lost:\n%s", value, got)
				}
			}
			if len(repeated.Preview.Writes) != 0 {
				t.Fatalf("second desired-state application wrote files: %#v", repeated.Preview.Writes)
			}
		})
	}
}

func TestSetUsageWindow_AnonymousExactSelectorRecognizesPreAndPostStates(t *testing.T) {
	oldWindow := store.UsageWindow{From: "2026-01-01", To: "2026-01-31"}
	newWindow := store.UsageWindow{From: "2026-02-01", To: "2026-02-28"}
	tests := []struct {
		name        string
		frontmatter string
		selector    store.ProvenanceSource
		window      *store.UsageWindow
		wantWrites  int
		wantWindow  bool
	}{
		{
			name: "set then post-state noop",
			frontmatter: "type: Note\nsources:\n" +
				"  - resource: policy.md\n    x-source: keep # source-comment\n",
			selector:   store.ProvenanceSource{Resource: "policy.md"},
			window:     &newWindow,
			wantWrites: 1,
			wantWindow: true,
		},
		{
			name: "remove then post-state noop",
			frontmatter: "type: Note\nsources:\n" +
				"  - resource: policy.md\n" +
				"    usage_window: {from: 2026-01-01, to: 2026-01-31}\n" +
				"    x-source: keep # source-comment\n",
			selector:   store.ProvenanceSource{Resource: "policy.md", UsageWindow: &oldWindow},
			wantWrites: 1,
		},
		{
			name: "direct set post-state noop",
			frontmatter: "type: Note\nsources:\n" +
				"  - resource: policy.md\n" +
				"    usage_window: {from: 2026-02-01, to: 2026-02-28}\n" +
				"    x-source: keep # source-comment\n",
			selector:   store.ProvenanceSource{Resource: "policy.md"},
			window:     &newWindow,
			wantWindow: true,
		},
		{
			name: "direct remove post-state noop",
			frontmatter: "type: Note\nsources:\n" +
				"  - resource: policy.md\n    x-source: keep # source-comment\n",
			selector: store.ProvenanceSource{Resource: "policy.md", UsageWindow: &oldWindow},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			source := memorySource{"a.md": []byte("---\n" + test.frontmatter + "---\nBody\n")}
			selector, err := store.SourceByExact(test.selector)
			if err != nil {
				t.Fatal(err)
			}
			operation, err := store.NewSetUsageWindow(ref(t, "a").ID, &selector, test.window)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			result, err := Plan(context.Background(), source, change(t, source, operation))
			var got []byte
			if err == nil {
				got, err = result.Staged.ReadFile(context.Background(), "a.md")
			}
			repeatedSource := memorySource{"a.md": append([]byte(nil), got...)}
			var repeated Result
			if err == nil {
				repeated, err = Plan(context.Background(), repeatedSource, change(t, repeatedSource, operation))
			}

			// Assert.
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Preview.Writes) != test.wantWrites {
				t.Fatalf("first writes = %#v, want %d", result.Preview.Writes, test.wantWrites)
			}
			if !bytes.Contains(got, []byte("x-source: keep # source-comment")) {
				t.Fatalf("unknown exact-source extension/trivia was lost:\n%s", got)
			}
			hasWindow := bytes.Contains(got, []byte("usage_window:"))
			if hasWindow != test.wantWindow {
				t.Fatalf("usage-window presence = %v, want %v:\n%s", hasWindow, test.wantWindow, got)
			}
			if test.wantWindow &&
				(!bytes.Contains(got, []byte("from: 2026-02-01")) || !bytes.Contains(got, []byte("to: 2026-02-28"))) {
				t.Fatalf("desired usage window absent:\n%s", got)
			}
			if len(repeated.Preview.Writes) != 0 {
				t.Fatalf("second exact desired-state application wrote files: %#v", repeated.Preview.Writes)
			}
		})
	}
}

func TestSetUsageWindow_AnonymousExactSelectorAmbiguityRejectsWithoutStage(t *testing.T) {
	const desiredWindow = "    usage_window: {from: 2026-02-01, to: 2026-02-28}\n"
	tests := []struct {
		name    string
		sources string
	}{
		{
			name: "different pre and post candidates",
			sources: "  - resource: policy.md\n" +
				"  - resource: policy.md\n" + desiredWindow,
		},
		{
			name: "duplicate pre-state candidates",
			sources: "  - resource: policy.md\n" +
				"  - resource: policy.md\n",
		},
		{
			name: "duplicate post-state candidates",
			sources: "  - resource: policy.md\n" + desiredWindow +
				"  - resource: policy.md\n" + desiredWindow,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			source := memorySource{"a.md": []byte("---\ntype: Note\nsources:\n" + test.sources + "---\nBody\n")}
			selector, err := store.SourceByExact(store.ProvenanceSource{Resource: "policy.md"})
			if err != nil {
				t.Fatal(err)
			}
			window := store.UsageWindow{From: "2026-02-01", To: "2026-02-28"}
			operation, err := store.NewSetUsageWindow(ref(t, "a").ID, &selector, &window)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			result, err := Plan(context.Background(), source, change(t, source, operation))

			// Assert.
			if err == nil {
				t.Fatal("ambiguous anonymous source transition was accepted")
			}
			if result.Staged != nil || len(result.Preview.Writes) != 0 || len(result.Preview.Renames) != 0 {
				t.Fatalf("rejected anonymous source transition exposed stage: %#v", result)
			}
		})
	}
}

func TestSetUsageWindow_IDSelectorPreservesSelectionPresentationError(t *testing.T) {
	// Arrange.
	data := []byte("---\ntype: Note\nbase: &src {id: policy, resource: policy.md}\nsources:\n  - *src\n---\nBody\n")
	source := memorySource{"a.md": data}
	selector, err := store.SourceByID("policy")
	if err != nil {
		t.Fatal(err)
	}
	operation, err := store.NewSetUsageWindow(
		ref(t, "a").ID,
		&selector,
		&store.UsageWindow{From: "2026-02-01", To: "2026-02-28"},
	)
	if err != nil {
		t.Fatal(err)
	}
	aliasAt := bytes.Index(data, []byte("*src"))

	// Act.
	result, planErr := Plan(context.Background(), source, change(t, source, operation))

	// Assert.
	var presentation *PresentationError
	var invalid *store.InvalidChangeSet
	if !errors.As(planErr, &presentation) ||
		!errors.As(planErr, &invalid) ||
		!errors.Is(planErr, ErrAmbiguousPresentation) ||
		presentation.Code != yamlCodeAliasProvenance ||
		presentation.Location != (SourceSpan{Start: aliasAt, End: aliasAt + len("*src")}) ||
		invalid.Code != "ambiguous_presentation" {
		t.Fatalf("selection error identity/code/location = %#v, presentation=%#v", planErr, presentation)
	}
	if presentation.Code == "missing_source_selector" || result.Staged != nil || len(result.Preview.Writes) != 0 {
		t.Fatalf("selection error was rewritten or staged: %#v, %#v", presentation, result)
	}
}

func TestSourceRemovalAndUsageWindowRemoval_ExactAndLastSource(t *testing.T) {
	// Arrange.
	source := memorySource{"a.md": []byte("---\ntype: Note\nsources:\n" +
		"  - resource: policy.md\n    title: Policy\n    usage_window: {from: 2026-01-01, to: 2026-01-31}\n" +
		"  - id: keep\n    resource: keep.md\n---\nBody\n")}
	id := ref(t, "a").ID
	withWindow, err := store.SourceByExact(store.ProvenanceSource{
		Resource: "policy.md", Title: "Policy",
		UsageWindow: &store.UsageWindow{From: "2026-01-01", To: "2026-01-31"},
	})
	if err != nil {
		t.Fatal(err)
	}
	removeWindow, err := store.NewSetUsageWindow(id, &withWindow, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	windowResult, err := Plan(context.Background(), source, change(t, source, removeWindow))
	var windowBytes []byte
	if err == nil {
		windowBytes, err = windowResult.Staged.ReadFile(context.Background(), "a.md")
	}
	withoutWindow, selectorErr := store.SourceByExact(store.ProvenanceSource{Resource: "policy.md", Title: "Policy"})
	removeSource, removeErr := store.NewRemoveSource(id, withoutWindow)
	windowSource := memorySource{"a.md": append([]byte(nil), windowBytes...)}
	var sourceResult Result
	if err == nil && selectorErr == nil && removeErr == nil {
		sourceResult, removeErr = Plan(context.Background(), windowSource, change(t, windowSource, removeSource))
	}
	var sourceBytes []byte
	if removeErr == nil {
		sourceBytes, removeErr = sourceResult.Staged.ReadFile(context.Background(), "a.md")
	}
	lastSource := memorySource{"a.md": []byte("---\ntype: Note\nsources:\n  - {id: only, resource: only.md}\n---\nBody\n")}
	lastSelector, lastSelectorErr := store.SourceByID("only")
	lastRemove, lastRemoveErr := store.NewRemoveSource(id, lastSelector)
	var lastResult Result
	if lastSelectorErr == nil && lastRemoveErr == nil {
		lastResult, lastRemoveErr = Plan(context.Background(), lastSource, change(t, lastSource, lastRemove))
	}
	var lastBytes []byte
	if lastRemoveErr == nil {
		lastBytes, lastRemoveErr = lastResult.Staged.ReadFile(context.Background(), "a.md")
	}

	// Assert.
	if err != nil || selectorErr != nil || removeErr != nil || lastSelectorErr != nil || lastRemoveErr != nil {
		t.Fatalf("window/selector/remove/last errors = %v, %v, %v, %v, %v", err, selectorErr, removeErr, lastSelectorErr, lastRemoveErr)
	}
	if bytes.Contains(windowBytes, []byte("usage_window:")) || !bytes.Contains(windowBytes, []byte("resource: policy.md")) {
		t.Fatalf("exact source usage-window removal mismatch:\n%s", windowBytes)
	}
	if bytes.Contains(sourceBytes, []byte("policy.md")) || !bytes.Contains(sourceBytes, []byte("id: keep")) {
		t.Fatalf("exact source removal mismatch:\n%s", sourceBytes)
	}
	if bytes.Contains(lastBytes, []byte("sources:")) {
		t.Fatalf("last source removal left empty family:\n%s", lastBytes)
	}
}

func TestSetLifecycle_AbsentInsertIsIdempotent(t *testing.T) {
	// Arrange.
	source := memorySource{"a.md": []byte("---\ntype: Note\n---\nBody\n")}
	status, staleAfter := "stable", "2027-01-31"
	operation, err := store.NewSetLifecycle(ref(t, "a").ID, store.Lifecycle{
		Status: &status, StaleAfter: &staleAfter,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	result, err := Plan(context.Background(), source, change(t, source, operation))
	var got []byte
	if err == nil {
		got, err = result.Staged.ReadFile(context.Background(), "a.md")
	}
	repeatedSource := memorySource{"a.md": append([]byte(nil), got...)}
	var repeated Result
	if err == nil {
		repeated, err = Plan(context.Background(), repeatedSource, change(t, repeatedSource, operation))
	}

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte("status: stable")) || !bytes.Contains(got, []byte("stale_after: 2027-01-31")) {
		t.Fatalf("lifecycle insertion mismatch:\n%s", got)
	}
	if len(repeated.Preview.Writes) != 0 {
		t.Fatalf("second lifecycle application wrote files: %#v", repeated.Preview.Writes)
	}
}

func TestLifecycleOperations_PartialAndFullRemovalAreIdempotent(t *testing.T) {
	// Arrange.
	source := memorySource{"a.md": []byte("---\ntype: Note\nstatus: stable\nstale_after: 2026-12-31\n---\nBody\n")}
	id := ref(t, "a").ID
	newStale := "2027-01-31"
	partial, err := store.NewSetLifecycle(id, store.Lifecycle{StaleAfter: &newStale})
	if err != nil {
		t.Fatal(err)
	}
	full, err := store.NewSetLifecycle(id, store.Lifecycle{})
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	partialResult, err := Plan(context.Background(), source, change(t, source, partial))
	var partialBytes []byte
	if err == nil {
		partialBytes, err = partialResult.Staged.ReadFile(context.Background(), "a.md")
	}
	partialSource := memorySource{"a.md": append([]byte(nil), partialBytes...)}
	var fullResult Result
	if err == nil {
		fullResult, err = Plan(context.Background(), partialSource, change(t, partialSource, full))
	}
	var fullBytes []byte
	if err == nil {
		fullBytes, err = fullResult.Staged.ReadFile(context.Background(), "a.md")
	}
	fullSource := memorySource{"a.md": append([]byte(nil), fullBytes...)}
	var repeated Result
	if err == nil {
		repeated, err = Plan(context.Background(), fullSource, change(t, fullSource, full))
	}

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(partialBytes, []byte("status:")) || !bytes.Contains(partialBytes, []byte("stale_after: 2027-01-31")) {
		t.Fatalf("partial lifecycle removal mismatch:\n%s", partialBytes)
	}
	if bytes.Contains(fullBytes, []byte("status:")) || bytes.Contains(fullBytes, []byte("stale_after:")) {
		t.Fatalf("full lifecycle removal mismatch:\n%s", fullBytes)
	}
	if len(repeated.Preview.Writes) != 0 {
		t.Fatalf("repeated full lifecycle removal wrote files: %#v", repeated.Preview.Writes)
	}
}

func TestSetBundleVersion_UpdateAndSecondApply(t *testing.T) {
	// Arrange.
	source := memorySource{
		"index.md": []byte("---\nokf_version: \"0.1\"\n---\n# Notes\n\n* [A](a.md) - A\n"),
		"a.md":     []byte("---\ntype: Note\n---\nA\n"),
	}
	operation := store.SetBundleVersion{Version: "0.2"}

	// Act.
	result, err := Plan(context.Background(), source, change(t, source, operation))
	var got []byte
	if err == nil {
		got, err = result.Staged.ReadFile(context.Background(), "index.md")
	}
	repeatedSource := memorySource{
		"index.md": append([]byte(nil), got...),
		"a.md":     append([]byte(nil), source["a.md"]...),
	}
	var repeated Result
	if err == nil {
		repeated, err = Plan(context.Background(), repeatedSource, change(t, repeatedSource, operation))
	}

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(got, []byte("okf_version: \"0.2\"")) != 1 || bytes.Contains(got, []byte("\"0.1\"")) {
		t.Fatalf("bundle version update mismatch:\n%s", got)
	}
	if len(repeated.Preview.Writes) != 0 {
		t.Fatalf("second version application wrote files: %#v", repeated.Preview.Writes)
	}
}

func TestPutAttestedComputation_ReconcilesClosedCollectionsAndPreservesExtensions(t *testing.T) {
	// Arrange.
	source := memorySource{"a.md": []byte("---\n" +
		"type: Attested Computation\n" +
		"runtime: old-runtime\n" +
		"parameters:\n" +
		"  - {name: obsolete, type: string, required: true}\n" +
		"  - {name: keep, type: string, required: false, x-param: keep}\n" +
		"  - {name: second, type: integer, required: false}\n" +
		"computation: old.sql\n" +
		"executor:\n" +
		"  resource: old-run.md\n" +
		"  receipt: [obsolete, result]\n" +
		"  x-executor: keep # executor-comment\n" +
		"attester:\n" +
		"  resource: old-attest.py\n" +
		"  x-attester: keep # attester-comment\n" +
		"---\nBody comment must survive.\n")}
	operation, err := store.NewPutAttestedComputation(ref(t, "a").ID, store.AttestedComputationContract{
		Mode:    store.AttestedComputationModeFile,
		Runtime: "postgres",
		Parameters: []store.ComputationParameter{
			{Name: "second", Type: "integer", Required: true},
			{Name: "keep", Type: "string", Required: true},
		},
		ComputationPath: "query.sql",
		Executor:        store.ExecutorContract{Resource: "run.md", Receipt: []string{"result", "fresh"}},
		Attester:        store.AttesterContract{Resource: "attest.py"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	result, err := Plan(context.Background(), source, change(t, source, operation))
	var got []byte
	if err == nil {
		got, err = result.Staged.ReadFile(context.Background(), "a.md")
	}
	repeatedSource := memorySource{"a.md": append([]byte(nil), got...)}
	var repeated Result
	if err == nil {
		repeated, err = Plan(context.Background(), repeatedSource, change(t, repeatedSource, operation))
	}

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	for _, preserved := range [][]byte{
		[]byte("x-param: keep"),
		[]byte("x-executor: keep # executor-comment"),
		[]byte("x-attester: keep # attester-comment"),
		[]byte("Body comment must survive."),
	} {
		if !bytes.Contains(got, preserved) {
			t.Fatalf("unknown field/trivia %q was lost:\n%s", preserved, got)
		}
	}
	for _, obsolete := range [][]byte{[]byte("name: obsolete"), []byte("old-run.md"), []byte("old-attest.py"), []byte("old.sql")} {
		if bytes.Contains(got, obsolete) {
			t.Fatalf("obsolete computation value %q remains:\n%s", obsolete, got)
		}
	}
	secondAt, keepAt := bytes.Index(got, []byte("name: second")), bytes.Index(got, []byte("name: keep"))
	if secondAt < 0 || keepAt < 0 || secondAt >= keepAt ||
		!bytes.Contains(got, []byte("receipt: [result, fresh]")) {
		t.Fatalf("desired parameter/receipt order missing:\n%s", got)
	}
	if bytes.Count(got, []byte("required: true")) != 2 ||
		!bytes.Contains(got, []byte("computation: query.sql")) ||
		!bytes.Contains(got, []byte("resource: run.md")) ||
		!bytes.Contains(got, []byte("resource: attest.py")) {
		t.Fatalf("attested computation reconciliation mismatch:\n%s", got)
	}
	if len(repeated.Preview.Writes) != 0 {
		t.Fatalf("second computation application wrote files: %#v", repeated.Preview.Writes)
	}
}

func TestPutAttestedComputation_ObsoleteParameterFailureFollowsDocumentOrder(t *testing.T) {
	document := func(parameters string) []byte {
		return []byte("---\n" +
			"type: Attested Computation\n" +
			"runtime: postgres\n" +
			"parameters:\n" + parameters +
			"computation: query.sql\n" +
			"executor: {resource: run.md, receipt: [result]}\n" +
			"attester: {resource: attest.py}\n" +
			"---\n")
	}
	operation, err := store.NewPutAttestedComputation(
		ref(t, "a").ID,
		store.AttestedComputationContract{
			Mode:            store.AttestedComputationModeFile,
			Runtime:         "postgres",
			ComputationPath: "query.sql",
			Executor:        store.ExecutorContract{Resource: "run.md", Receipt: []string{"result"}},
			Attester:        store.AttesterContract{Resource: "attest.py"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertRejected := func(source []byte, wantCode string) *PresentationError {
		t.Helper()
		bundleSource := memorySource{"a.md": source}

		// Act.
		result, planErr := Plan(context.Background(), bundleSource, change(t, bundleSource, operation))

		// Assert.
		var presentation *PresentationError
		var invalid *store.InvalidChangeSet
		if !errors.As(planErr, &presentation) ||
			!errors.As(planErr, &invalid) ||
			invalid.Code != "invalid_presentation" ||
			!errors.Is(planErr, ErrUnsupportedPresentation) ||
			presentation.Code != wantCode ||
			presentation.Path != "a.md" ||
			presentation.Operation != "put_attested_computation" {
			t.Fatalf(
				"Plan() error = %#v, presentation = %#v, want unsupported %q at a.md/put_attested_computation",
				planErr,
				presentation,
				wantCode,
			)
		}
		if result.Staged != nil ||
			len(result.Preview.Writes) != 0 ||
			len(result.Preview.Deletes) != 0 ||
			len(result.Preview.Renames) != 0 ||
			len(result.Preview.Plan) != 0 {
			t.Fatalf("rejected obsolete parameter exposed stage or plan: %#v", result)
		}
		return presentation
	}

	// Arrange. Each obsolete item is independently unsafe to remove. The first
	// has provenance-bearing trivia; the second has a YAML anchor.
	firstOnly := document(
		"  - name: first\n" +
			"    type: string # first-parameter provenance\n" +
			"    required: false\n",
	)
	secondOnly := document(
		"  - name: second\n" +
			"    type: &parameter-type integer\n" +
			"    required: false\n",
	)
	combined := document(
		"  - name: first\n" +
			"    type: string # first-parameter provenance\n" +
			"    required: false\n" +
			"  - name: second\n" +
			"    type: &parameter-type integer\n" +
			"    required: false\n",
	)
	firstStart := bytes.Index(combined, []byte("string # first-parameter provenance"))
	if firstStart < 0 {
		t.Fatal("first parameter location fixture is missing")
	}
	wantLocation := SourceSpan{Start: firstStart, End: firstStart + len("string")}

	// Act and assert the independent failure classes before the adversarial
	// repeated combined case.
	assertRejected(firstOnly, yamlCodeUnownedComment)
	assertRejected(secondOnly, yamlCodeTouchedAnchor)
	for iteration := 0; iteration < 256; iteration++ {
		presentation := assertRejected(combined, yamlCodeUnownedComment)
		if presentation.Location != wantLocation {
			t.Fatalf(
				"iteration %d location = %#v, want first obsolete item %#v",
				iteration,
				presentation.Location,
				wantLocation,
			)
		}
	}
}

func TestPutAttestedComputation_PathSecondApplyRequiredFalseAndLateFailureIsAtomic(t *testing.T) {
	// Arrange.
	source := memorySource{"a.md": []byte("---\ntype: Note\n---\nBody\n")}
	id := ref(t, "a").ID
	operation, err := store.NewPutAttestedComputation(id, store.AttestedComputationContract{
		Mode: store.AttestedComputationModeFile, Runtime: "postgres", Parameters: []store.ComputationParameter{{Name: "year", Type: "integer", Required: false}},
		ComputationPath: "query.sql",
		Executor:        store.ExecutorContract{Resource: "run.md", Receipt: []string{"result"}},
		Attester:        store.AttesterContract{Resource: "attest.py"},
	})
	if err != nil {
		t.Fatal(err)
	}
	lateFailureSource := memorySource{"a.md": []byte("---\ntype: Note\n---\n" +
		"# Computation\n\n```sql\nselect 1\n```\n\n# Computation\n\n```sql\nselect 2\n```\n")}
	inline, err := store.NewPutAttestedComputation(id, store.AttestedComputationContract{
		Mode:              store.AttestedComputationModeInline,
		Runtime:           "postgres",
		InlineComputation: "select desired",
		InlineLanguage:    "sql",
		Executor:          store.ExecutorContract{Resource: "run.md", Receipt: []string{"result"}},
		Attester:          store.AttesterContract{Resource: "attest.py"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	result, err := Plan(context.Background(), source, change(t, source, operation))
	var got []byte
	if err == nil {
		got, err = result.Staged.ReadFile(context.Background(), "a.md")
	}
	repeatedSource := memorySource{"a.md": append([]byte(nil), got...)}
	var repeated Result
	if err == nil {
		repeated, err = Plan(context.Background(), repeatedSource, change(t, repeatedSource, operation))
	}
	rejected, rejectedErr := Plan(context.Background(), lateFailureSource, change(t, lateFailureSource, inline))

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte("required: false")) || !bytes.Contains(got, []byte("computation: query.sql")) {
		t.Fatalf("path computation/required false missing:\n%s", got)
	}
	if len(repeated.Preview.Writes) != 0 {
		t.Fatalf("second path application wrote files: %#v", repeated.Preview.Writes)
	}
	if rejectedErr == nil {
		t.Fatal("ambiguous computation body was accepted")
	}
	if rejected.Staged != nil || len(rejected.Preview.Writes) != 0 || len(rejected.Preview.Renames) != 0 {
		t.Fatalf("late rejected computation exposed partial stage: %#v", rejected)
	}
}

func TestPutAttestedComputation_PreservesComputationProseSiblingsAndFollowingSections(t *testing.T) {
	for _, pathMode := range []bool{false, true} {
		name := "inline replacement"
		if pathMode {
			name = "path removal"
		}
		t.Run(name, func(t *testing.T) {
			// Arrange.
			source := memorySource{"a.md": []byte("---\ntype: Note\n---\n" +
				"# Computation\n\nBefore fence prose.\n\n```sql\nselect old\n```\n\nAfter fence prose.\n\n" +
				"# Tail\n\nTail content.\n")}
			contract := store.AttestedComputationContract{
				Runtime:  "postgres",
				Executor: store.ExecutorContract{Resource: "run.md", Receipt: []string{"result"}},
				Attester: store.AttesterContract{Resource: "attest.py"},
			}
			if pathMode {
				contract.Mode = store.AttestedComputationModeFile
				contract.ComputationPath = "query.sql"
			} else {
				contract.Mode = store.AttestedComputationModeInline
				contract.InlineComputation = "select desired"
				contract.InlineLanguage = "sql"
			}
			operation, err := store.NewPutAttestedComputation(ref(t, "a").ID, contract)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			result, err := Plan(context.Background(), source, change(t, source, operation))
			var got []byte
			if err == nil {
				got, err = result.Staged.ReadFile(context.Background(), "a.md")
			}
			repeatedSource := memorySource{"a.md": append([]byte(nil), got...)}
			var repeated Result
			if err == nil {
				repeated, err = Plan(context.Background(), repeatedSource, change(t, repeatedSource, operation))
			}

			// Assert.
			if err != nil {
				t.Fatal(err)
			}
			for _, preserved := range [][]byte{
				[]byte("Before fence prose."), []byte("After fence prose."),
				[]byte("# Tail\n\nTail content."),
			} {
				if !bytes.Contains(got, preserved) {
					t.Fatalf("prose sibling/tail %q was lost:\n%s", preserved, got)
				}
			}
			if bytes.Contains(got, []byte("select old")) {
				t.Fatalf("obsolete fence payload remains:\n%s", got)
			}
			if pathMode && bytes.Contains(got, []byte("```")) {
				t.Fatalf("path mode retained owned fence:\n%s", got)
			}
			if !pathMode && !bytes.Contains(got, []byte("select desired")) {
				t.Fatalf("inline mode failed to replace owned fence:\n%s", got)
			}
			if len(repeated.Preview.Writes) != 0 {
				t.Fatalf("second computation application wrote files: %#v", repeated.Preview.Writes)
			}
		})
	}
}

func TestPutAttestedComputation_EmptySectionInlineInsertionPreservesProse(t *testing.T) {
	// Arrange.
	source := memorySource{"a.md": []byte("---\ntype: Note\n---\n" +
		"# Computation\n\nExplanation without a fence.\n\n# Tail\n\nTail content.\n")}
	operation, err := store.NewPutAttestedComputation(ref(t, "a").ID, store.AttestedComputationContract{
		Mode:              store.AttestedComputationModeInline,
		Runtime:           "postgres",
		InlineComputation: "select desired",
		InlineLanguage:    "sql",
		Executor:          store.ExecutorContract{Resource: "run.md", Receipt: []string{"result"}},
		Attester:          store.AttesterContract{Resource: "attest.py"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	result, err := Plan(context.Background(), source, change(t, source, operation))
	var got []byte
	if err == nil {
		got, err = result.Staged.ReadFile(context.Background(), "a.md")
	}
	repeatedSource := memorySource{"a.md": append([]byte(nil), got...)}
	var repeated Result
	if err == nil {
		repeated, err = Plan(context.Background(), repeatedSource, change(t, repeatedSource, operation))
	}

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range [][]byte{
		[]byte("select desired"),
		[]byte("Explanation without a fence."),
		[]byte("# Tail\n\nTail content."),
	} {
		if !bytes.Contains(got, want) {
			t.Fatalf("empty-section insertion lost %q:\n%s", want, got)
		}
	}
	if len(repeated.Preview.Writes) != 0 {
		t.Fatalf("second empty-section application wrote files: %#v", repeated.Preview.Writes)
	}
}

func TestPutAttestedComputation_EmptyInlinePayload_BlockFlowInsertReconcileAndReplay(t *testing.T) {
	tests := []struct {
		name          string
		source        string
		preserved     string
		wantFirstNoop bool
	}{
		{
			name: "block collections insert empty fence",
			source: "---\n" +
				"type: Note\n" +
				"x-root: keep\n" +
				"---\nBody.\n",
			preserved: "x-root: keep",
		},
		{
			name: "flow collections reconcile file and populated fence",
			source: "---\n" +
				"type: Attested Computation\n" +
				"runtime: old\n" +
				"parameters: [{name: old, type: integer, required: true}]\n" +
				"computation: old.sql\n" +
				"executor: {resource: old.md, receipt: [old], x-executor: keep}\n" +
				"attester: {resource: old.py}\n" +
				"---\n# Computation\n\n```old\nold payload\n```\n",
			preserved: "x-executor: keep",
		},
		{
			name: "existing exact empty fence is noop",
			source: "---\n" +
				"type: Attested Computation\n" +
				"runtime: sql\n" +
				"parameters: [{name: value, type: string, required: false}]\n" +
				"executor: {resource: run.md, receipt: [result]}\n" +
				"attester: {resource: attest.py}\n" +
				"---\n# Computation\n\n```sql\n```\n",
			wantFirstNoop: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			source := memorySource{"a.md": []byte(test.source)}
			operation, err := store.NewPutAttestedComputation(
				ref(t, "a").ID,
				store.AttestedComputationContract{
					Mode:           store.AttestedComputationModeInline,
					Runtime:        "sql",
					Parameters:     []store.ComputationParameter{{Name: "value", Type: "string"}},
					InlineLanguage: "sql",
					Executor:       store.ExecutorContract{Resource: "run.md", Receipt: []string{"result"}},
					Attester:       store.AttesterContract{Resource: "attest.py"},
				},
			)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			result, err := Plan(context.Background(), source, change(t, source, operation))
			var got []byte
			if err == nil {
				got, err = result.Staged.ReadFile(context.Background(), "a.md")
			}
			repeatedSource := memorySource{"a.md": append([]byte(nil), got...)}
			var repeated Result
			if err == nil {
				repeated, err = Plan(context.Background(), repeatedSource, change(t, repeatedSource, operation))
			}
			layout, hasFrontmatter, splitErr := documentlayout.Split(got)
			var inspection markdownowner.ComputationInspection
			if splitErr == nil && hasFrontmatter {
				inspection = markdownowner.InspectComputation(string(got[layout.BodyStart:]))
			}

			// Assert.
			if err != nil || splitErr != nil {
				t.Fatalf("plan/split errors = %v, %v", err, splitErr)
			}
			if test.wantFirstNoop && len(result.Preview.Writes) != 0 {
				t.Fatalf("exact empty-inline contract wrote on first apply: %#v", result.Preview.Writes)
			}
			if inspection.State != markdownowner.ComputationInline ||
				len(inspection.DirectFences) != 1 ||
				inspection.DirectFences[0].Info != "sql" ||
				inspection.DirectFences[0].Content != "" {
				t.Fatalf("empty inline fence mismatch: %#v\n%s", inspection, got)
			}
			if bytes.Contains(got, []byte("\ncomputation:")) ||
				bytes.Contains(got, []byte("old payload")) {
				t.Fatalf("obsolete file/populated payload remains:\n%s", got)
			}
			if test.preserved != "" && !bytes.Contains(got, []byte(test.preserved)) {
				t.Fatalf("extension %q was lost:\n%s", test.preserved, got)
			}
			if len(repeated.Preview.Writes) != 0 {
				t.Fatalf("second empty-inline application wrote files: %#v", repeated.Preview.Writes)
			}
		})
	}
}

func TestPutAttestedComputation_UnclosedOrMultipleFenceRejectsWithoutStage(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		pathMode bool
	}{
		{
			name: "unclosed inline replacement",
			body: "# Computation\n\n```sql\nselect old\nTAIL MUST SURVIVE\n",
		},
		{
			name:     "unclosed path removal",
			body:     "# Computation\n\n```sql\nselect old\nTAIL MUST SURVIVE\n",
			pathMode: true,
		},
		{
			name: "multiple inline replacement",
			body: "# Computation\n\n```sql\nselect one\n```\n\n```sql\nselect two\n```\n",
		},
		{
			name:     "multiple path removal",
			body:     "# Computation\n\n```sql\nselect one\n```\n\n```sql\nselect two\n```\n",
			pathMode: true,
		},
		{
			name: "blockquote fence inline",
			body: "# Computation\n\n> ````sql\n> select nested\n> ````\n",
		},
		{
			name:     "blockquote fence path",
			body:     "# Computation\n\n> ````sql\n> select nested\n> ````\n",
			pathMode: true,
		},
		{
			name: "list fence inline",
			body: "# Computation\n\n- explanation\n\n  ~~~~sql\n  select nested\n  ~~~~\n",
		},
		{
			name:     "list fence path",
			body:     "# Computation\n\n- explanation\n\n  ~~~~sql\n  select nested\n  ~~~~\n",
			pathMode: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			source := memorySource{"a.md": []byte("---\ntype: Note\n---\n" + test.body)}
			contract := store.AttestedComputationContract{
				Runtime:  "postgres",
				Executor: store.ExecutorContract{Resource: "run.md", Receipt: []string{"result"}},
				Attester: store.AttesterContract{Resource: "attest.py"},
			}
			if test.pathMode {
				contract.Mode = store.AttestedComputationModeFile
				contract.ComputationPath = "query.sql"
			} else {
				contract.Mode = store.AttestedComputationModeInline
				contract.InlineComputation = "select desired"
				contract.InlineLanguage = "sql"
			}
			operation, err := store.NewPutAttestedComputation(ref(t, "a").ID, contract)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			result, err := Plan(context.Background(), source, change(t, source, operation))

			// Assert.
			if err == nil {
				t.Fatal("unsafe computation fence boundary was accepted")
			}
			if result.Staged != nil || len(result.Preview.Writes) != 0 || len(result.Preview.Renames) != 0 {
				t.Fatalf("rejected fence boundary exposed stage: %#v", result)
			}
		})
	}
}

func TestPutAttestedComputation_PreservesExactTerminatedPayloadWithoutBlankLine(t *testing.T) {
	tests := []struct {
		name    string
		newline string
		payload string
	}{
		{name: "LF", newline: "\n", payload: "select desired\n"},
		{name: "CRLF", newline: "\r\n", payload: "select desired\r\n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			nl := test.newline
			source := memorySource{"a.md": []byte("---" + nl + "type: Note" + nl + "---" + nl +
				"# Computation" + nl + nl + "```sql" + nl + "select old" + nl + "```" + nl)}
			operation, err := store.NewPutAttestedComputation(ref(t, "a").ID, store.AttestedComputationContract{
				Mode:              store.AttestedComputationModeInline,
				Runtime:           "postgres",
				InlineComputation: test.payload,
				InlineLanguage:    "sql",
				Executor:          store.ExecutorContract{Resource: "run.md", Receipt: []string{"result"}},
				Attester:          store.AttesterContract{Resource: "attest.py"},
			})
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			result, err := Plan(context.Background(), source, change(t, source, operation))
			var got []byte
			if err == nil {
				got, err = result.Staged.ReadFile(context.Background(), "a.md")
			}
			layout, hasFrontmatter, splitErr := documentlayout.Split(got)
			var fence markdownowner.Fence
			var fenceOK bool
			if err == nil && splitErr == nil && hasFrontmatter {
				inspection := markdownowner.InspectComputation(string(got[layout.BodyStart:]))
				fenceOK = inspection.State == markdownowner.ComputationInline
				if fenceOK {
					fence = inspection.DirectFences[0]
				}
			}
			repeatedSource := memorySource{"a.md": append([]byte(nil), got...)}
			var repeated Result
			if err == nil {
				repeated, err = Plan(context.Background(), repeatedSource, change(t, repeatedSource, operation))
			}

			// Assert.
			if err != nil || splitErr != nil {
				t.Fatalf("plan/split errors = %v, %v", err, splitErr)
			}
			if !hasFrontmatter || !fenceOK || !fence.Closed {
				t.Fatalf("closed parser-owned fence absent:\n%s", got)
			}
			if fence.Content != test.payload {
				t.Fatalf("Fence.Content = %q, want exact payload %q", fence.Content, test.payload)
			}
			if len(repeated.Preview.Writes) != 0 {
				t.Fatalf("second exact-payload application wrote files: %#v", repeated.Preview.Writes)
			}
		})
	}
}

func TestPutAttestedComputation_SelectsRepresentationSafeFenceAndReplaysExactly(t *testing.T) {
	styles := []struct {
		name          string
		language      string
		delimiter     byte
		delimiterRun  int
		infoSeparator string
	}{
		{
			name:         "backtick info safe",
			language:     "sql",
			delimiter:    '`',
			delimiterRun: 8,
		},
		{
			name:          "single leading tilde and backtick info",
			language:      "~`template",
			delimiter:     '~',
			delimiterRun:  12,
			infoSeparator: " ",
		},
		{
			name:          "multiple leading tildes and backtick info",
			language:      "~~~~~`template",
			delimiter:     '~',
			delimiterRun:  12,
			infoSeparator: " ",
		},
	}
	modes := []struct {
		name string
		body string
	}{
		{name: "insertion", body: "# Computation\n\nExplanation.\n"},
		{name: "replacement", body: "# Computation\n\n```old\nold payload\n```\n"},
	}
	for _, style := range styles {
		for _, mode := range modes {
			for _, newline := range []string{"\n", "\r\n"} {
				newlineName := "LF"
				if newline == "\r\n" {
					newlineName = "CRLF"
				}
				t.Run(style.name+"/"+mode.name+"/"+newlineName, func(t *testing.T) {
					// Arrange.
					payload := strings.ReplaceAll(
						"before\n```````\n~~~~~~~~~~~\nafter\n",
						"\n",
						newline,
					)
					source := memorySource{"a.md": []byte(strings.ReplaceAll(
						"---\ntype: Note\n---\n"+mode.body,
						"\n",
						newline,
					))}
					contract := store.AttestedComputationContract{
						Mode:              store.AttestedComputationModeInline,
						Runtime:           "postgres",
						InlineComputation: payload,
						InlineLanguage:    style.language,
						Executor:          store.ExecutorContract{Resource: "run.md", Receipt: []string{"result"}},
						Attester:          store.AttesterContract{Resource: "attest.py"},
					}
					operation, err := store.NewPutAttestedComputation(ref(t, "a").ID, contract)
					if err != nil {
						t.Fatal(err)
					}

					// Act.
					result, err := Plan(context.Background(), source, change(t, source, operation))
					var got []byte
					if err == nil {
						got, err = result.Staged.ReadFile(context.Background(), "a.md")
					}
					layout, hasFrontmatter, splitErr := documentlayout.Split(got)
					var fence markdownowner.Fence
					var fenceOK bool
					if err == nil && splitErr == nil && hasFrontmatter {
						inspection := markdownowner.InspectComputation(string(got[layout.BodyStart:]))
						fenceOK = inspection.State == markdownowner.ComputationInline
						if fenceOK {
							fence = inspection.DirectFences[0]
						}
					}
					repeatedSource := memorySource{"a.md": append([]byte(nil), got...)}
					var repeated Result
					if err == nil {
						repeated, err = Plan(context.Background(), repeatedSource, change(t, repeatedSource, operation))
					}
					var migrationApplied []byte
					var migrationErr error
					if err == nil {
						migrationApplied, migrationErr = applyComputationBoundary(
							context.Background(),
							got,
							store.ComputationMigration{Path: "a.md", Contract: contract},
						)
					}

					// Assert.
					if err != nil || splitErr != nil || migrationErr != nil {
						t.Fatalf("plan/split/migration errors = %v, %v, %v", err, splitErr, migrationErr)
					}
					if !hasFrontmatter || !fenceOK || !fence.Closed ||
						fence.Info != style.language || fence.Content != payload {
						t.Fatalf("safe fence projection = %#v, want info=%q exact payload", fence, style.language)
					}
					body := got[layout.BodyStart:]
					if fence.Start < 0 || fence.End > len(body) || fence.Start >= fence.End {
						t.Fatalf("invalid fence span %#v for %d-byte body", fence, len(body))
					}
					renderedFence := body[fence.Start:fence.End]
					delimiter := strings.Repeat(string(style.delimiter), style.delimiterRun)
					wantOpening := delimiter + style.infoSeparator + style.language + newline
					if !bytes.HasPrefix(renderedFence, []byte(wantOpening)) {
						t.Fatalf("opening fence = %q, want prefix %q:\n%s", renderedFence, wantOpening, got)
					}
					if !bytes.HasSuffix(renderedFence, []byte(delimiter+newline)) {
						t.Fatalf("closing fence does not use content-only run %q:\n%s", delimiter, got)
					}
					run := 0
					for run < len(renderedFence) && renderedFence[run] == style.delimiter {
						run++
					}
					if run != style.delimiterRun {
						t.Fatalf("opening delimiter run = %d, want %d:\n%s", run, style.delimiterRun, got)
					}
					if !bytes.Equal(migrationApplied, got) {
						t.Fatalf("migration computation helper changed accepted Plan projection:\n%s", migrationApplied)
					}
					if len(repeated.Preview.Writes) != 0 {
						t.Fatalf("replayed safe fence wrote files: %#v", repeated.Preview.Writes)
					}
				})
			}
		}
	}
}

func TestV02Operations_DuplicateTouchedKeysFailClosedWithoutStage(t *testing.T) {
	tests := []struct {
		name        string
		frontmatter string
		operation   func(t *testing.T, id bundle.ConceptID) store.Operation
	}{
		{
			name:        "generated by",
			frontmatter: "type: Note\ngenerated:\n  by: process:old\n  by: process:other\n",
			operation: func(_ *testing.T, id bundle.ConceptID) store.Operation {
				return store.SetGenerated{Concept: id, Generated: store.Generation{By: "process:new"}}
			},
		},
		{
			name:        "source resource",
			frontmatter: "type: Note\nsources:\n  - id: policy\n    resource: one.md\n    resource: two.md\n",
			operation: func(t *testing.T, id bundle.ConceptID) store.Operation {
				t.Helper()
				operation, err := store.NewPutSource(id, store.ProvenanceSource{ID: "policy", Resource: "desired.md"})
				if err != nil {
					t.Fatal(err)
				}
				return operation
			},
		},
		{
			name: "usage window from",
			frontmatter: "type: Note\nusage_window:\n" +
				"  from: 2026-01-01\n  from: 2026-01-02\n  to: 2026-01-31\n",
			operation: func(t *testing.T, id bundle.ConceptID) store.Operation {
				t.Helper()
				operation, err := store.NewSetUsageWindow(id, nil, &store.UsageWindow{From: "2026-02-01", To: "2026-02-28"})
				if err != nil {
					t.Fatal(err)
				}
				return operation
			},
		},
		{
			name:        "lifecycle status",
			frontmatter: "type: Note\nstatus: draft\nstatus: stable\n",
			operation: func(t *testing.T, id bundle.ConceptID) store.Operation {
				t.Helper()
				status := "deprecated"
				operation, err := store.NewSetLifecycle(id, store.Lifecycle{Status: &status})
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
			source := memorySource{"a.md": []byte("---\n" + test.frontmatter + "---\nBody\n")}
			operation := test.operation(t, ref(t, "a").ID)

			// Act.
			result, err := Plan(context.Background(), source, change(t, source, operation))

			// Assert.
			if err == nil {
				t.Fatal("duplicate touched key was accepted")
			}
			if result.Staged != nil || len(result.Preview.Writes) != 0 || len(result.Preview.Renames) != 0 {
				t.Fatalf("rejected duplicate exposed stage: %#v", result)
			}
		})
	}
}

func TestMoveConcept_RewritesAllV02PathFieldsAndPreservesNonPaths(t *testing.T) {
	// Arrange.
	source := memorySource{
		"a.md": []byte("---\ntype: Note\n" +
			"sources:\n  - {id: outgoing, resource: 'refs/source.md?mode=1'}\n" +
			"computation: refs/query.sql#part\n" +
			"executor: {resource: 'refs/run.md?mode=1'}\n" +
			"attester: {resource: /refs/attest.py#v1}\n---\nA\n"),
		"incoming.md": []byte("---\ntype: Note\n" +
			"sources:\n  - {id: incoming, resource: 'a.md?source=1'}\n" +
			"computation: /a.md#query\n" +
			"executor: {resource: 'a.md?run=1'}\n" +
			"attester: {resource: /a.md#attest}\n---\nIncoming\n"),
		"external.md": []byte("---\ntype: Note\n" +
			"sources:\n" +
			"  - {id: url, resource: 'https://example.test/a.md?source=1'}\n" +
			"  - {id: colon, resource: 'project:acme'}\n" +
			"  - {id: slash, resource: dashboards/exec-revenue}\n" +
			"  - {id: dot, resource: dataset.production}\n" +
			"computation: https://example.test/a.sql#query\n" +
			"executor: {resource: 'https://example.test/a.md?run=1'}\n" +
			"attester: {resource: https://example.test/a.py#attest}\n---\nExternal\n"),
		"broken.md": []byte("---\ntype: Note\n" +
			"sources:\n  - {id: broken, resource: './missing.md?source=1'}\n" +
			"computation: missing.sql#query\n" +
			"executor: {resource: 'missing.md?run=1'}\n" +
			"attester: {resource: missing.py#attest}\n---\nBroken\n"),
		"scope.md": []byte("---\ntype: Note\n" +
			"sources:\n  - {id: scope, resource: all assets in project X}\n" +
			"computation: all computations in project X\n" +
			"executor: {resource: all executors in project X}\n" +
			"attester: {resource: all attesters in project X}\n---\nScope\n"),
		"refs/source.md": []byte("---\ntype: Note\n---\nSource\n"),
		"refs/query.sql": []byte("select 1"),
		"refs/run.md":    []byte("---\ntype: Note\n---\nRun\n"),
		"refs/attest.py": []byte("print('ok')"),
	}
	operation := store.MoveConcept{From: ref(t, "a").ID, To: ref(t, "nested/a").ID}

	// Act.
	result, err := Plan(context.Background(), source, change(t, source, operation))
	read := func(path string) []byte {
		t.Helper()
		if err != nil {
			return nil
		}
		data, readErr := result.Staged.ReadFile(context.Background(), path)
		if readErr != nil {
			err = readErr
		}
		return data
	}
	moved := read("nested/a.md")
	incoming := read("incoming.md")
	external := read("external.md")
	broken := read("broken.md")
	scope := read("scope.md")

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range [][]byte{
		[]byte("../refs/source.md?mode=1"),
		[]byte("../refs/query.sql#part"),
		[]byte("../refs/run.md?mode=1"),
		[]byte("/refs/attest.py#v1"),
	} {
		if !bytes.Contains(moved, want) {
			t.Fatalf("moved outgoing field %q missing:\n%s", want, moved)
		}
	}
	for _, want := range [][]byte{
		[]byte("nested/a.md?source=1"),
		[]byte("/nested/a.md#query"),
		[]byte("nested/a.md?run=1"),
		[]byte("/nested/a.md#attest"),
	} {
		if !bytes.Contains(incoming, want) {
			t.Fatalf("incoming field %q missing:\n%s", want, incoming)
		}
	}
	for name, pair := range map[string]struct {
		got  []byte
		want [][]byte
	}{
		"external/scope": {
			got: append(append([]byte(nil), external...), scope...),
			want: [][]byte{
				[]byte("https://example.test/a.md?source=1"),
				[]byte("project:acme"),
				[]byte("dashboards/exec-revenue"),
				[]byte("dataset.production"),
				[]byte("https://example.test/a.sql#query"),
				[]byte("https://example.test/a.md?run=1"),
				[]byte("https://example.test/a.py#attest"),
				[]byte("all computations in project X"),
				[]byte("all executors in project X"),
				[]byte("all attesters in project X"),
			},
		},
		"broken": {
			got: broken,
			want: [][]byte{
				[]byte("./missing.md?source=1"),
				[]byte("missing.sql#query"),
				[]byte("missing.md?run=1"),
				[]byte("missing.py#attest"),
			},
		},
	} {
		for _, want := range pair.want {
			if !bytes.Contains(pair.got, want) {
				t.Fatalf("%s non-path %q changed or disappeared:\n%s", name, want, pair.got)
			}
		}
	}
	if len(result.Preview.Renames) != 1 {
		t.Fatalf("move renames = %#v", result.Preview.Renames)
	}
}

func TestMoveConcept_RewritesCapturedLiteralPercentPathsAcrossV02Families(t *testing.T) {
	for _, test := range []struct {
		name    string
		newline string
	}{
		{name: "LF", newline: "\n"},
		{name: "CRLF", newline: "\r\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			document := func(value string) []byte {
				t.Helper()
				return []byte(strings.ReplaceAll(value, "\n", test.newline))
			}
			urlDocument := document("---\ntype: Note\n" +
				"sources:\n  - {id: url, resource: 'http://example.test/source%20asset.json?token=%2F#source'}\n" +
				"computation: 'https://example.test/query%2Fasset.sql?token=%20#computation'\n" +
				"executor: {resource: 'https://example.test/run%20asset.md?token=%2F#executor'}\n" +
				"attester: {resource: 'https://example.test/attest%2Fasset.py?token=%20#attester'}\n---\nURLs\n")
			malformedDocument := document("---\ntype: Note\n" +
				"sources:\n" +
				"  - {id: local, resource: './missing%zz.json?token=%zz#source'}\n" +
				"  - {id: url, resource: 'https://example.test/source%zz.json?token=%zz#source'}\n" +
				"computation: './missing%zz.sql?token=%zz#computation'\n" +
				"executor: {resource: 'https://example.test/run%zz.md?token=%zz#executor'}\n" +
				"attester: {resource: './missing%zz.py?token=%zz#attester'}\n---\nMalformed\n")
			source := memorySource{
				"old%zz.md": document("---\ntype: Note\n" +
					"sources:\n  - {id: source, resource: 'references/source%zz.json?token=%zz#source'}\n" +
					"computation: 'references/query%zz.sql?token=%zz#computation'\n" +
					"executor: {resource: 'references/run%zz.md?token=%zz#executor'}\n" +
					"attester: {resource: '/references/attest%zz.py?token=%zz#attester'}\n---\nOld\n"),
				"docs/incoming.md": document("---\ntype: Note\n" +
					"sources:\n  - {id: source, resource: '../old%zz.md?token=%zz#source'}\n" +
					"computation: '/old%zz.md?token=%zz#computation'\n" +
					"executor: {resource: '../old%zz.md?token=%zz#executor'}\n" +
					"attester: {resource: '/old%zz.md?token=%zz#attester'}\n---\nIncoming\n"),
				"urls.md":                   append([]byte(nil), urlDocument...),
				"malformed.md":              append([]byte(nil), malformedDocument...),
				"references/source%zz.json": []byte("{}"),
				"references/query%zz.sql":   []byte("select 1"),
				"references/run%zz.md":      document("---\ntype: Note\n---\nRun\n"),
				"references/attest%zz.py":   []byte("print('ok')"),
			}
			operation := store.MoveConcept{
				From: ref(t, "old%zz").ID,
				To:   ref(t, "nested/new%name").ID,
			}

			// Act.
			result, err := Plan(context.Background(), source, change(t, source, operation))
			read := func(name string) []byte {
				t.Helper()
				if err != nil {
					return nil
				}
				data, readErr := result.Staged.ReadFile(context.Background(), name)
				if readErr != nil {
					err = readErr
				}
				return data
			}
			moved := read("nested/new%name.md")
			incoming := read("docs/incoming.md")
			urls := read("urls.md")
			malformed := read("malformed.md")

			// Assert.
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range [][]byte{
				[]byte("../references/source%zz.json?token=%zz#source"),
				[]byte("../references/query%zz.sql?token=%zz#computation"),
				[]byte("../references/run%zz.md?token=%zz#executor"),
				[]byte("/references/attest%zz.py?token=%zz#attester"),
			} {
				if !bytes.Contains(moved, want) {
					t.Fatalf("moved captured literal-percent path %q missing:\n%s", want, moved)
				}
			}
			for _, want := range [][]byte{
				[]byte("../nested/new%name.md?token=%zz#source"),
				[]byte("/nested/new%name.md?token=%zz#computation"),
				[]byte("../nested/new%name.md?token=%zz#executor"),
				[]byte("/nested/new%name.md?token=%zz#attester"),
			} {
				if !bytes.Contains(incoming, want) {
					t.Fatalf("incoming captured literal-percent path %q missing:\n%s", want, incoming)
				}
			}
			if !bytes.Equal(urls, urlDocument) {
				t.Fatalf("valid encoded URLs were rewritten:\n%s", urls)
			}
			if !bytes.Equal(malformed, malformedDocument) {
				t.Fatalf("malformed uncaptured values were rewritten:\n%s", malformed)
			}
			for _, write := range result.Preview.Writes {
				if write.Path == "urls.md" || write.Path == "malformed.md" {
					t.Fatalf("unchanged URL/malformed document was staged: %#v", result.Preview.Writes)
				}
			}
			if !bytes.Contains(moved, []byte(test.newline)) || !bytes.Contains(incoming, []byte(test.newline)) {
				t.Fatalf("newline convention %q was not preserved", test.newline)
			}
			if test.newline == "\r\n" {
				for name, got := range map[string][]byte{"moved": moved, "incoming": incoming} {
					if bytes.Contains(bytes.ReplaceAll(got, []byte("\r\n"), nil), []byte("\n")) {
						t.Fatalf("%s gained a bare LF under CRLF input:\n%s", name, got)
					}
				}
			} else if bytes.Contains(moved, []byte("\r")) || bytes.Contains(incoming, []byte("\r")) {
				t.Fatalf("LF input gained a CR byte:\nmoved:\n%s\nincoming:\n%s", moved, incoming)
			}
		})
	}
}

func TestMoveConcept_V02PathSuffixesRemainOpaqueAndBytePreserved(t *testing.T) {
	// Arrange.
	source := memorySource{
		"a.md": []byte("---\ntype: Note\n" +
			"sources:\n  - id: outgoing\n    resource: 'refs/source.md?next=/../secret&encoded=%2E%2E%2F'\n" +
			"computation: '/refs/dir/../query.sql#frag/../../secret/%2e%2e%2f'\n" +
			"executor:\n  resource: 'refs/run.md?redirect=/../../run&encoded=%252e%252e#receipt/../x'\n" +
			"attester:\n  resource: '/refs/dir/../attest.py#proof/../../x/%2F..'\n---\nA\n"),
		"incoming.md": []byte("---\ntype: Note\n" +
			"sources:\n  - id: incoming\n    resource: 'a.md?next=/../a.md&encoded=%2E%2E%2F'\n" +
			"computation: '/dir/../a.md#query/../../a.md/%2f..'\n" +
			"executor:\n  resource: './a.md?return=/../../a.md#receipt/../x'\n" +
			"attester:\n  resource: '/dir/../a.md#proof/../../a.md/%252E%252E'\n---\nIncoming\n"),
		"nonpaths.md": []byte("---\ntype: Note\n" +
			"sources:\n" +
			"  - {id: broken, resource: './missing.md?bind=/../a.md'}\n" +
			"  - {id: outside, resource: '../outside.md#bind/../../a.md'}\n" +
			"  - {id: ambiguous, resource: 'project/dataset.v1:events?bind=/../a.md'}\n" +
			"  - {id: scope, resource: 'all assets in project X?bind=/../a.md'}\n" +
			"computation: 'missing.sql#bind/../../a.md'\n" +
			"executor: {resource: 'missing.md?bind=/../a.md'}\n" +
			"attester: {resource: 'missing.py#bind/../../a.md'}\n---\nNon-paths\n"),
		"refs/source.md": []byte("---\ntype: Note\n---\nSource\n"),
		"refs/query.sql": []byte("select 1"),
		"refs/run.md":    []byte("---\ntype: Note\n---\nRun\n"),
		"refs/attest.py": []byte("print('ok')"),
	}
	operation := store.MoveConcept{From: ref(t, "a").ID, To: ref(t, "nested/a").ID}

	// Act.
	result, err := Plan(context.Background(), source, change(t, source, operation))
	read := func(path string) []byte {
		t.Helper()
		if err != nil {
			return nil
		}
		data, readErr := result.Staged.ReadFile(context.Background(), path)
		if readErr != nil {
			err = readErr
		}
		return data
	}
	moved := read("nested/a.md")
	incoming := read("incoming.md")
	nonpaths := read("nonpaths.md")
	var replayedMoved, replayedIncoming []byte
	if err == nil {
		projected, loadErr := bundle.Load(context.Background(), result.Staged)
		if loadErr != nil {
			err = loadErr
		} else {
			replayedMoved, err = rewriteV02PathValuesContext(
				context.Background(), projected,
				"nested/a.md", "nested/a.md", "a.md", "nested/a.md", moved,
			)
			if err == nil {
				replayedIncoming, err = rewriteV02PathValuesContext(
					context.Background(), projected,
					"incoming.md", "incoming.md", "a.md", "nested/a.md", incoming,
				)
			}
		}
	}

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range [][]byte{
		[]byte("../refs/source.md?next=/../secret&encoded=%2E%2E%2F"),
		[]byte("/refs/dir/../query.sql#frag/../../secret/%2e%2e%2f"),
		[]byte("../refs/run.md?redirect=/../../run&encoded=%252e%252e#receipt/../x"),
		[]byte("/refs/dir/../attest.py#proof/../../x/%2F.."),
	} {
		if !bytes.Contains(moved, want) {
			t.Fatalf("moved path/suffix %q missing:\n%s", want, moved)
		}
	}
	for _, want := range [][]byte{
		[]byte("nested/a.md?next=/../a.md&encoded=%2E%2E%2F"),
		[]byte("/nested/a.md#query/../../a.md/%2f.."),
		[]byte("nested/a.md?return=/../../a.md#receipt/../x"),
		[]byte("/nested/a.md#proof/../../a.md/%252E%252E"),
	} {
		if !bytes.Contains(incoming, want) {
			t.Fatalf("incoming path/suffix %q missing:\n%s", want, incoming)
		}
	}
	if !bytes.Equal(nonpaths, source["nonpaths.md"]) {
		t.Fatalf("broken/out-of-scope/ambiguous values were rewritten:\n%s", nonpaths)
	}
	for _, write := range result.Preview.Writes {
		if write.Path == "nonpaths.md" {
			t.Fatalf("non-path-only document was staged: %#v", result.Preview.Writes)
		}
	}
	if !bytes.Equal(replayedMoved, moved) || !bytes.Equal(replayedIncoming, incoming) {
		t.Fatalf("v0.2 path rewrite was not replay-idempotent:\nmoved:\n%s\nreplayed:\n%s\nincoming:\n%s\nreplayed:\n%s",
			moved, replayedMoved, incoming, replayedIncoming)
	}
}

func TestMoveConcept_V02QuotedSurroundingWhitespaceIsExactAndNeverRetargeted(t *testing.T) {
	// Arrange.
	whitespaceDocument := []byte("---\ntype: Note\n" +
		"sources:\n  - {id: leading, resource: ' a.md?next=/../x'}\n" +
		"computation: 'a.md#query/../../x '\n" +
		"executor: {resource: ' a.md?run=/../../x'}\n" +
		"attester: {resource: '/a.md#proof/../../x '}\n---\nWhitespace\n")
	source := memorySource{
		"a.md":           []byte("---\ntype: Note\n---\nA\n"),
		"whitespace.md":  append([]byte(nil), whitespaceDocument...),
		"refs/source.md": []byte("---\ntype: Note\n---\nSource\n"),
	}
	operation := store.MoveConcept{From: ref(t, "a").ID, To: ref(t, "nested/a").ID}

	// Act.
	result, err := Plan(context.Background(), source, change(t, source, operation))
	var got []byte
	if err == nil {
		got, err = result.Staged.ReadFile(context.Background(), "whitespace.md")
	}

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, whitespaceDocument) {
		t.Fatalf("quoted surrounding whitespace was normalized or retargeted:\n%s", got)
	}
	for _, write := range result.Preview.Writes {
		if write.Path == "whitespace.md" {
			t.Fatalf("exact-whitespace document was staged: %#v", result.Preview.Writes)
		}
	}
}

func TestMoveConcept_V02AmbiguousPathValuesAndNilResolverAreNoops(t *testing.T) {
	// Arrange.
	ambiguousDocument := []byte("---\ntype: Note\n" +
		"sources:\n  - {id: suffix, resource: '#part'}\n" +
		"computation: '?query'\n" +
		"executor: {resource: '%zz'}\n" +
		"attester: {resource: 'https://example.test/%zz'}\n---\nAmbiguous\n")
	source := memorySource{
		"a.md":         []byte("---\ntype: Note\n---\nA\n"),
		"ambiguous.md": append([]byte(nil), ambiguousDocument...),
	}
	operation := store.MoveConcept{From: ref(t, "a").ID, To: ref(t, "nested/a").ID}

	// Act.
	nilRewritten, nilErr := rewriteV02PathValuesContext(
		context.Background(), nil,
		"ambiguous.md", "ambiguous.md", "a.md", "nested/a.md", ambiguousDocument,
	)
	result, err := Plan(context.Background(), source, change(t, source, operation))
	var got []byte
	if err == nil {
		got, err = result.Staged.ReadFile(context.Background(), "ambiguous.md")
	}
	nilResolved := make(map[bundle.PathValueField]string)
	for _, field := range []bundle.PathValueField{
		bundle.PathFieldSourceResource,
		bundle.PathFieldComputation,
		bundle.PathFieldExecutorResource,
		bundle.PathFieldAttesterResource,
	} {
		nilResolved[field] = rewritePathValue(nil, "a.md", "nested/a.md", "a.md", "nested/a.md", "a.md#part", field)
	}

	// Assert.
	if nilErr != nil || err != nil {
		t.Fatalf("nil/plan errors = %v, %v", nilErr, err)
	}
	if !bytes.Equal(nilRewritten, ambiguousDocument) || !bytes.Equal(got, ambiguousDocument) {
		t.Fatalf("ambiguous suffix/escape value was rewritten:\nnil:\n%s\nplan:\n%s", nilRewritten, got)
	}
	for field, value := range nilResolved {
		if value != "a.md#part" {
			t.Fatalf("nil resolver %s rewrite = %q, want raw value", field, value)
		}
	}
	for _, write := range result.Preview.Writes {
		if write.Path == "ambiguous.md" {
			t.Fatalf("ambiguous-only document was staged: %#v", result.Preview.Writes)
		}
	}
}

func TestMoveConcept_V02AliasAndMergeProvenanceRejectsWithoutStage(t *testing.T) {
	tests := []struct {
		name        string
		frontmatter string
	}{
		{
			name: "sources collection alias",
			frontmatter: "shared: &shared\n  - resource: refs/source.md\n" +
				"sources: *shared\n",
		},
		{
			name: "sources merge",
			frontmatter: "shared: &shared {resource: refs/source.md}\n" +
				"sources:\n  - {<<: *shared}\n",
		},
		{
			name: "sources terminal alias",
			frontmatter: "target: &target refs/source.md\n" +
				"sources:\n  - resource: *target\n",
		},
		{
			name:        "computation terminal alias",
			frontmatter: "target: &target refs/query.sql\ncomputation: *target\n",
		},
		{
			name:        "executor collection alias",
			frontmatter: "shared: &shared {resource: refs/run.md}\nexecutor: *shared\n",
		},
		{
			name:        "executor merge",
			frontmatter: "shared: &shared {resource: refs/run.md}\nexecutor: {<<: *shared}\n",
		},
		{
			name:        "executor terminal alias",
			frontmatter: "target: &target refs/run.md\nexecutor: {resource: *target}\n",
		},
		{
			name:        "attester collection alias",
			frontmatter: "shared: &shared {resource: refs/attest.py}\nattester: *shared\n",
		},
		{
			name:        "attester merge",
			frontmatter: "shared: &shared {resource: refs/attest.py}\nattester: {<<: *shared}\n",
		},
		{
			name:        "attester terminal alias",
			frontmatter: "target: &target refs/attest.py\nattester: {resource: *target}\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			source := memorySource{
				"a.md":           []byte("---\ntype: Note\n" + test.frontmatter + "---\nA\n"),
				"refs/source.md": []byte("---\ntype: Note\n---\nSource\n"),
				"refs/query.sql": []byte("select 1"),
				"refs/run.md":    []byte("---\ntype: Note\n---\nRun\n"),
				"refs/attest.py": []byte("print('ok')"),
			}
			operation := store.MoveConcept{From: ref(t, "a").ID, To: ref(t, "nested/a").ID}

			// Act.
			result, err := Plan(context.Background(), source, change(t, source, operation))

			// Assert.
			var presentation *PresentationError
			if !errors.Is(err, ErrAmbiguousPresentation) ||
				!errors.As(err, &presentation) ||
				presentation.Code != yamlCodeAliasProvenance {
				t.Fatalf("MoveConcept() error = %#v, presentation = %#v", err, presentation)
			}
			if result.Staged != nil || len(result.Preview.Writes) != 0 || len(result.Preview.Renames) != 0 {
				t.Fatalf("rejected alias/merge move exposed stage: %#v", result)
			}
		})
	}
}

func TestMoveConcept_V02PathRewriteIgnoresUnrelatedOpaqueProvenance(t *testing.T) {
	families := []string{"source", "computation", "executor", "attester"}
	styles := []string{"block", "flow"}
	provenanceKinds := []string{"duplicate", "alias", "merge"}

	for _, family := range families {
		for _, style := range styles {
			for _, provenanceKind := range provenanceKinds {
				t.Run(family+"/"+style+"/"+provenanceKind, func(t *testing.T) {
					// Arrange.
					movingDocument := moveConceptPathFamilyDocument(family, style)
					opaqueDocument := moveConceptOpaqueProvenanceDocument(
						family,
						style,
						provenanceKind,
					)
					source := memorySource{
						"a.md":           movingDocument,
						"opaque.md":      opaqueDocument,
						"refs/target.md": []byte("---\ntype: Note\n---\nTarget\n"),
					}
					operation := store.MoveConcept{
						From: ref(t, "a").ID,
						To:   ref(t, "nested/a").ID,
					}

					// Act.
					result, err := Plan(
						context.Background(),
						source,
						change(t, source, operation),
					)
					var moved, opaque []byte
					if err == nil {
						moved, err = result.Staged.ReadFile(context.Background(), "nested/a.md")
					}
					if err == nil {
						opaque, err = result.Staged.ReadFile(context.Background(), "opaque.md")
					}

					// Assert.
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Contains(moved, []byte("../refs/target.md")) {
						t.Fatalf("moved %s/%s path was not rewritten:\n%s", family, style, moved)
					}
					if !bytes.Equal(opaque, opaqueDocument) {
						t.Fatalf(
							"unrelated %s provenance changed:\nwant:\n%s\ngot:\n%s",
							provenanceKind,
							opaqueDocument,
							opaque,
						)
					}
					if len(result.Preview.Renames) != 1 {
						t.Fatalf("move renames = %#v", result.Preview.Renames)
					}
				})
			}
		}
	}
}

func TestMoveConcept_V02IrrelevantPathProvenanceDoesNotVeto(t *testing.T) {
	families := []string{"source", "computation", "executor", "attester"}
	styles := []string{"block", "flow"}
	provenanceKinds := []string{"duplicate", "alias", "merge"}
	pathKinds := []string{"broken", "external", "scope"}

	for _, family := range families {
		for _, style := range styles {
			for _, provenanceKind := range provenanceKinds {
				for _, pathKind := range pathKinds {
					t.Run(family+"/"+style+"/"+provenanceKind+"/"+pathKind, func(t *testing.T) {
						// Arrange.
						irrelevantDocument := moveConceptIrrelevantPathProvenanceDocument(
							family,
							style,
							provenanceKind,
							pathKind,
						)
						source := memorySource{
							"a.md":          []byte("---\ntype: Note\n---\nA\n"),
							"irrelevant.md": irrelevantDocument,
						}
						operation := store.MoveConcept{
							From: ref(t, "a").ID,
							To:   ref(t, "nested/a").ID,
						}

						// Act.
						result, err := Plan(
							context.Background(),
							source,
							change(t, source, operation),
						)
						var irrelevant []byte
						if err == nil {
							irrelevant, err = result.Staged.ReadFile(
								context.Background(),
								"irrelevant.md",
							)
						}

						// Assert.
						if err != nil {
							t.Fatal(err)
						}
						if !bytes.Equal(irrelevant, irrelevantDocument) {
							t.Fatalf(
								"irrelevant %s path provenance changed:\nwant:\n%s\ngot:\n%s",
								pathKind,
								irrelevantDocument,
								irrelevant,
							)
						}
						if len(result.Preview.Renames) != 1 {
							t.Fatalf("move renames = %#v", result.Preview.Renames)
						}
					})
				}
			}
		}
	}
}

func TestMoveConcept_V02BlockPathRewritePreservesOpaqueSiblings(t *testing.T) {
	families := []string{"source", "computation", "executor", "attester"}
	provenanceKinds := []string{"duplicate", "alias", "merge"}

	for _, family := range families {
		for _, provenanceKind := range provenanceKinds {
			t.Run(family+"/"+provenanceKind, func(t *testing.T) {
				// Arrange.
				document, markers := moveConceptBlockPathWithOpaqueSibling(
					family,
					provenanceKind,
				)
				source := memorySource{
					"a.md":           document,
					"refs/target.md": []byte("---\ntype: Note\n---\nTarget\n"),
				}
				operation := store.MoveConcept{
					From: ref(t, "a").ID,
					To:   ref(t, "nested/a").ID,
				}

				// Act.
				result, err := Plan(
					context.Background(),
					source,
					change(t, source, operation),
				)
				var moved []byte
				if err == nil {
					moved, err = result.Staged.ReadFile(context.Background(), "nested/a.md")
				}

				// Assert.
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Contains(moved, []byte("../refs/target.md")) {
					t.Fatalf("moved %s path was not rewritten:\n%s", family, moved)
				}
				for _, marker := range markers {
					if !bytes.Contains(moved, marker) {
						t.Fatalf("opaque sibling %q was not preserved:\n%s", marker, moved)
					}
				}
			})
		}
	}
}

func TestMoveConcept_V02FlowAtomicRewriteRejectsOpaqueSiblingProvenance(t *testing.T) {
	families := []string{"source", "computation", "executor", "attester"}
	provenanceKinds := []string{"duplicate", "alias", "merge"}

	for _, family := range families {
		for _, provenanceKind := range provenanceKinds {
			t.Run(family+"/"+provenanceKind, func(t *testing.T) {
				// Arrange.
				source := memorySource{
					"a.md": moveConceptFlowPathWithOpaqueSibling(
						family,
						provenanceKind,
					),
					"refs/target.md": []byte("---\ntype: Note\n---\nTarget\n"),
				}
				operation := store.MoveConcept{
					From: ref(t, "a").ID,
					To:   ref(t, "nested/a").ID,
				}

				// Act.
				result, err := Plan(
					context.Background(),
					source,
					change(t, source, operation),
				)

				// Assert.
				var presentation *PresentationError
				if (!errors.Is(err, ErrAmbiguousPresentation) &&
					!errors.Is(err, ErrUnsupportedPresentation)) ||
					!errors.As(err, &presentation) ||
					presentation.Code == "" {
					t.Fatalf("MoveConcept() error = %#v, presentation = %#v", err, presentation)
				}
				if result.Staged != nil ||
					len(result.Preview.Writes) != 0 ||
					len(result.Preview.Deletes) != 0 ||
					len(result.Preview.Renames) != 0 ||
					len(result.Preview.Plan) != 0 {
					t.Fatalf("rejected flow rewrite exposed plan: %#v", result)
				}
			})
		}
	}
}

func TestMoveConcept_V02TouchedPathProvenanceFailsClosed(t *testing.T) {
	families := []string{"source", "computation", "executor", "attester"}
	styles := []string{"block", "flow"}
	provenanceKinds := []string{"duplicate", "alias", "merge"}

	for _, family := range families {
		for _, style := range styles {
			for _, provenanceKind := range provenanceKinds {
				t.Run(family+"/"+style+"/"+provenanceKind, func(t *testing.T) {
					// Arrange.
					source := memorySource{
						"a.md": moveConceptTouchedProvenanceDocument(
							family,
							style,
							provenanceKind,
						),
						"refs/target.md": []byte("---\ntype: Note\n---\nTarget\n"),
						"refs/other.md":  []byte("---\ntype: Note\n---\nOther\n"),
					}
					operation := store.MoveConcept{
						From: ref(t, "a").ID,
						To:   ref(t, "nested/a").ID,
					}

					// Act.
					result, err := Plan(
						context.Background(),
						source,
						change(t, source, operation),
					)

					// Assert.
					var presentation *PresentationError
					wantCode := yamlCodeAliasProvenance
					if provenanceKind == "duplicate" {
						wantCode = yamlCodeDuplicateTouchedKey
					}
					if family == "computation" && style == "flow" && provenanceKind == "alias" {
						wantCode = yamlCodeExplicitTag
					}
					hasPresentation := errors.As(err, &presentation)
					if (!errors.Is(err, ErrAmbiguousPresentation) &&
						!errors.Is(err, ErrUnsupportedPresentation)) ||
						!hasPresentation ||
						presentation.Code != wantCode {
						t.Fatalf(
							"MoveConcept() error = %#v, presentation = %#v, want code %q",
							err,
							presentation,
							wantCode,
						)
					}
					if result.Staged != nil ||
						len(result.Preview.Writes) != 0 ||
						len(result.Preview.Deletes) != 0 ||
						len(result.Preview.Renames) != 0 ||
						len(result.Preview.Plan) != 0 {
						t.Fatalf("rejected touched provenance exposed plan: %#v", result)
					}
				})
			}
		}
	}
}

func moveConceptIrrelevantPathProvenanceDocument(
	family,
	style,
	provenanceKind,
	pathKind string,
) []byte {
	raw := "missing.md"
	switch pathKind {
	case "external":
		raw = "https://example.test/reference"
	case "scope":
		switch family {
		case "source":
			raw = "all assets in project X"
		case "computation":
			raw = "all computations in project X"
		case "executor":
			raw = "all executors in project X"
		case "attester":
			raw = "all attesters in project X"
		}
	}
	key := "resource"
	if family == "computation" {
		key = "computation"
	}
	quoted := "'" + raw + "'"
	var frontmatter string
	if family == "source" {
		switch provenanceKind + "/" + style {
		case "duplicate/block":
			frontmatter = "type: Note\nsources:\n  - resource: " + quoted +
				"\n    resource: " + quoted + "\n"
		case "duplicate/flow":
			frontmatter = "type: Note\nsources: [{resource: " + quoted +
				", resource: " + quoted + "}]\n"
		case "alias/block":
			frontmatter = "target: &target " + quoted +
				"\ntype: Note\nsources:\n  - resource: *target\n"
		case "alias/flow":
			frontmatter = "target: &target " + quoted +
				"\ntype: Note\nsources: [{resource: *target}]\n"
		case "merge/block":
			frontmatter = "path: &path {resource: " + quoted +
				"}\ntype: Note\nsources:\n  - <<: *path\n"
		case "merge/flow":
			frontmatter = "path: &path {resource: " + quoted +
				"}\ntype: Note\nsources: [{<<: *path}]\n"
		}
		return []byte("---\n" + frontmatter + "---\nIrrelevant\n")
	}
	if family == "computation" {
		switch provenanceKind + "/" + style {
		case "duplicate/block":
			frontmatter = "type: Note\ncomputation: " + quoted + "\ncomputation: " + quoted + "\n"
		case "duplicate/flow":
			frontmatter = "{type: Note, computation: " + quoted + ", computation: " + quoted + "}\n"
		case "alias/block":
			frontmatter = "target: &target " + quoted + "\ntype: Note\ncomputation: *target\n"
		case "alias/flow":
			frontmatter = "{target: &target " + quoted + ", type: Note, computation: *target}\n"
		case "merge/block":
			frontmatter = "path: &path {computation: " + quoted + "}\n<<: *path\ntype: Note\n"
		case "merge/flow":
			frontmatter = "{path: &path {computation: " + quoted + "}, <<: *path, type: Note}\n"
		}
		return []byte("---\n" + frontmatter + "---\nIrrelevant\n")
	}
	switch provenanceKind + "/" + style {
	case "duplicate/block":
		frontmatter = "type: Note\n" + family + ":\n  " + key + ": " + quoted +
			"\n  " + key + ": " + quoted + "\n"
	case "duplicate/flow":
		frontmatter = "type: Note\n" + family + ": {" + key + ": " + quoted +
			", " + key + ": " + quoted + "}\n"
	case "alias/block":
		frontmatter = "target: &target " + quoted + "\ntype: Note\n" + family +
			":\n  " + key + ": *target\n"
	case "alias/flow":
		frontmatter = "target: &target " + quoted + "\ntype: Note\n" + family +
			": {" + key + ": *target}\n"
	case "merge/block":
		frontmatter = "path: &path {" + key + ": " + quoted + "}\ntype: Note\n" + family +
			":\n  <<: *path\n"
	case "merge/flow":
		frontmatter = "path: &path {" + key + ": " + quoted + "}\ntype: Note\n" + family +
			": {<<: *path}\n"
	}
	return []byte("---\n" + frontmatter + "---\nIrrelevant\n")
}

func moveConceptBlockPathWithOpaqueSibling(family, provenanceKind string) ([]byte, [][]byte) {
	var prefix, sibling string
	var markers [][]byte
	switch provenanceKind {
	case "duplicate":
		sibling = "note: one\nnote: two\n"
		markers = [][]byte{[]byte("note: one"), []byte("note: two")}
	case "alias":
		prefix = "opaque: &opaque keep\n"
		sibling = "note: *opaque\n"
		markers = [][]byte{[]byte("opaque: &opaque keep"), []byte("note: *opaque")}
	case "merge":
		prefix = "opaque: &opaque {note: keep}\n"
		sibling = "<<: *opaque\n"
		markers = [][]byte{[]byte("opaque: &opaque {note: keep}"), []byte("<<: *opaque")}
	}
	var frontmatter string
	switch family {
	case "source":
		frontmatter = prefix + "type: Note\nsources:\n  - resource: refs/target.md\n" +
			indentYAMLLines(sibling, "    ")
	case "computation":
		frontmatter = prefix + "type: Note\ncomputation: refs/target.md\n" + sibling
	case "executor":
		frontmatter = prefix + "type: Note\nexecutor:\n  resource: refs/target.md\n" +
			indentYAMLLines(sibling, "  ")
	case "attester":
		frontmatter = prefix + "type: Note\nattester:\n  resource: refs/target.md\n" +
			indentYAMLLines(sibling, "  ")
	}
	return []byte("---\n" + frontmatter + "---\nA\n"), markers
}

func moveConceptFlowPathWithOpaqueSibling(family, provenanceKind string) []byte {
	var prefix, sibling string
	switch provenanceKind {
	case "duplicate":
		sibling = "note: one, note: two"
	case "alias":
		prefix = "opaque: &opaque keep\n"
		sibling = "note: *opaque"
	case "merge":
		prefix = "opaque: &opaque {note: keep}\n"
		sibling = "<<: *opaque"
	}
	var frontmatter string
	switch family {
	case "source":
		frontmatter = prefix + "type: Note\nsources: [{resource: refs/target.md, " + sibling + "}]\n"
	case "executor":
		frontmatter = prefix + "type: Note\nexecutor: {resource: refs/target.md, " + sibling + "}\n"
	case "attester":
		frontmatter = prefix + "type: Note\nattester: {resource: refs/target.md, " + sibling + "}\n"
	case "computation":
		switch provenanceKind {
		case "duplicate":
			frontmatter = "{type: Note, computation: refs/target.md, note: one, note: two}\n"
		case "alias":
			frontmatter = "{type: Note, opaque: &opaque keep, computation: refs/target.md, note: *opaque}\n"
		case "merge":
			frontmatter = "{type: Note, opaque: &opaque {note: keep}, <<: *opaque, computation: refs/target.md}\n"
		}
	}
	return []byte("---\n" + frontmatter + "---\nA\n")
}

func moveConceptPathFamilyDocument(family, style string) []byte {
	var frontmatter string
	switch family + "/" + style {
	case "source/block":
		frontmatter = "type: Note\nsources:\n  - resource: refs/target.md\n"
	case "source/flow":
		frontmatter = "type: Note\nsources: [{resource: refs/target.md}]\n"
	case "computation/block":
		frontmatter = "type: Note\ncomputation: refs/target.md\n"
	case "computation/flow":
		frontmatter = "{type: Note, computation: refs/target.md}\n"
	case "executor/block":
		frontmatter = "type: Note\nexecutor:\n  resource: refs/target.md\n"
	case "executor/flow":
		frontmatter = "type: Note\nexecutor: {resource: refs/target.md}\n"
	case "attester/block":
		frontmatter = "type: Note\nattester:\n  resource: refs/target.md\n"
	case "attester/flow":
		frontmatter = "type: Note\nattester: {resource: refs/target.md}\n"
	}
	return []byte("---\n" + frontmatter + "---\nA\n")
}

func moveConceptOpaqueProvenanceDocument(family, style, provenanceKind string) []byte {
	if family == "source" {
		var frontmatter string
		switch provenanceKind + "/" + style {
		case "duplicate/block":
			frontmatter = "type: Note\nsources:\n  - note: one\n    note: two\n"
		case "duplicate/flow":
			frontmatter = "type: Note\nsources: [{note: one, note: two}]\n"
		case "alias/block":
			frontmatter = "opaque: &opaque keep\ntype: Note\nsources:\n  - *opaque\n"
		case "alias/flow":
			frontmatter = "opaque: &opaque keep\ntype: Note\nsources: [*opaque]\n"
		case "merge/block":
			frontmatter = "opaque: &opaque {note: keep}\ntype: Note\nsources:\n  - <<: *opaque\n"
		case "merge/flow":
			frontmatter = "opaque: &opaque {note: keep}\ntype: Note\nsources: [{<<: *opaque}]\n"
		}
		return []byte("---\n" + frontmatter + "---\nOpaque\n")
	}
	opaque := ""
	switch provenanceKind {
	case "duplicate":
		if style == "block" {
			opaque = "note: one\nnote: two\n"
		} else {
			opaque = "note: one, note: two"
		}
	case "alias":
		if style == "block" {
			opaque = "anchor: &opaque keep\nnote: *opaque\n"
		} else {
			opaque = "anchor: &opaque keep, note: *opaque"
		}
	case "merge":
		if style == "block" {
			opaque = "anchor: &opaque {note: keep}\n<<: *opaque\n"
		} else {
			opaque = "anchor: &opaque {note: keep}, <<: *opaque"
		}
	}
	if family == "computation" {
		if style == "block" {
			return []byte("---\ntype: Note\n" + opaque + "---\nOpaque\n")
		}
		return []byte("---\n{type: Note, " + opaque + "}\n---\nOpaque\n")
	}
	if style == "block" {
		return []byte("---\ntype: Note\n" + family + ":\n" + indentYAMLLines(opaque, "  ") + "---\nOpaque\n")
	}
	return []byte("---\ntype: Note\n" + family + ": {" + opaque + "}\n---\nOpaque\n")
}

func moveConceptTouchedProvenanceDocument(family, style, provenanceKind string) []byte {
	if family == "source" {
		var frontmatter string
		switch provenanceKind + "/" + style {
		case "duplicate/block":
			frontmatter = "type: Note\nsources:\n  - resource: refs/target.md\n    resource: refs/other.md\n"
		case "duplicate/flow":
			frontmatter = "type: Note\nsources: [{resource: refs/target.md, resource: refs/other.md}]\n"
		case "alias/block":
			frontmatter = "type: Note\ntarget: &target refs/target.md\nsources:\n  - resource: *target\n"
		case "alias/flow":
			frontmatter = "type: Note\ntarget: &target refs/target.md\nsources: [{resource: *target}]\n"
		case "merge/block":
			frontmatter = "type: Note\npath: &path {resource: refs/target.md}\nsources:\n  - <<: *path\n"
		case "merge/flow":
			frontmatter = "type: Note\npath: &path {resource: refs/target.md}\nsources: [{<<: *path}]\n"
		}
		return []byte("---\n" + frontmatter + "---\nA\n")
	}
	pathKey := "resource"
	if family == "computation" {
		pathKey = "computation"
	}
	var frontmatter string
	switch provenanceKind {
	case "duplicate":
		if family == "computation" {
			if style == "block" {
				frontmatter = "type: Note\ncomputation: refs/target.md\ncomputation: refs/other.md\n"
			} else {
				frontmatter = "{type: Note, computation: refs/target.md, computation: refs/other.md}\n"
			}
		} else if style == "block" {
			frontmatter = "type: Note\n" + family + ":\n  " + pathKey + ": refs/target.md\n  " +
				pathKey + ": refs/other.md\n"
		} else {
			frontmatter = "type: Note\n" + family + ": {" + pathKey +
				": refs/target.md, " + pathKey + ": refs/other.md}\n"
		}
	case "alias":
		if family == "computation" && style == "flow" {
			frontmatter = "{type: Note, target: &target refs/target.md, computation: *target}\n"
		} else if family == "computation" {
			frontmatter = "type: Note\ntarget: &target refs/target.md\ncomputation: *target\n"
		} else if style == "block" {
			frontmatter = "type: Note\ntarget: &target refs/target.md\n" + family +
				":\n  resource: *target\n"
		} else {
			frontmatter = "type: Note\ntarget: &target refs/target.md\n" + family +
				": {resource: *target}\n"
		}
	case "merge":
		if family == "computation" && style == "flow" {
			frontmatter = "{path: &path {computation: refs/target.md}, <<: *path, type: Note}\n"
		} else if family == "computation" {
			frontmatter = "path: &path {computation: refs/target.md}\n<<: *path\ntype: Note\n"
		} else if style == "block" {
			frontmatter = "type: Note\npath: &path {resource: refs/target.md}\n" + family +
				":\n  <<: *path\n"
		} else {
			frontmatter = "type: Note\npath: &path {resource: refs/target.md}\n" + family +
				": {<<: *path}\n"
		}
	}
	return []byte("---\n" + frontmatter + "---\nA\n")
}

func indentYAMLLines(value, prefix string) string {
	lines := strings.Split(value, "\n")
	var output strings.Builder
	for _, line := range lines {
		if line == "" {
			continue
		}
		output.WriteString(prefix)
		output.WriteString(line)
		output.WriteByte('\n')
	}
	return output.String()
}

func TestRewritePathValue_FieldAwareIncomingOutgoingMatrix(t *testing.T) {
	// Arrange.
	source := memorySource{
		"a.md":          []byte("---\ntype: Note\n---\nA\n"),
		"b.md":          []byte("---\ntype: Note\n---\nB\n"),
		"refs/asset.md": []byte("asset"),
	}
	loaded, err := bundle.Load(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	fields := []struct {
		name  string
		field bundle.PathValueField
	}{
		{name: "source", field: bundle.PathFieldSourceResource},
		{name: "computation", field: bundle.PathFieldComputation},
		{name: "executor", field: bundle.PathFieldExecutorResource},
		{name: "attester", field: bundle.PathFieldAttesterResource},
	}

	for _, field := range fields {
		t.Run(field.name, func(t *testing.T) {
			broken := "missing.md"
			if field.field == bundle.PathFieldSourceResource {
				broken = "./missing.md"
			}
			tests := []struct {
				name       string
				sourcePath string
				outputPath string
				value      string
				want       string
			}{
				{name: "incoming local", sourcePath: "b.md", outputPath: "b.md", value: "a.md", want: "nested/a.md"},
				{name: "incoming bundle relative", sourcePath: "b.md", outputPath: "b.md", value: "/a.md", want: "/nested/a.md"},
				{
					name: "incoming local opaque query suffix", sourcePath: "b.md", outputPath: "b.md",
					value: "a.md?next=/../../x&encoded=%2E%2E%2F", want: "nested/a.md?next=/../../x&encoded=%2E%2E%2F",
				},
				{
					name: "incoming bundle opaque fragment suffix", sourcePath: "b.md", outputPath: "b.md",
					value: "/a.md#part/../../x/%252e%252e", want: "/nested/a.md#part/../../x/%252e%252e",
				},
				{
					name: "incoming noncanonical bundle target changes", sourcePath: "b.md", outputPath: "b.md",
					value: "/dir/../a.md#part/../../x", want: "/nested/a.md#part/../../x",
				},
				{
					name: "leading whitespace is exact", sourcePath: "b.md", outputPath: "b.md",
					value: " a.md?next=/../x", want: " a.md?next=/../x",
				},
				{
					name: "trailing whitespace is exact", sourcePath: "b.md", outputPath: "b.md",
					value: "/a.md#part/../x ", want: "/a.md#part/../x ",
				},
				{name: "fragment suffix only ambiguous", sourcePath: "b.md", outputPath: "b.md", value: "#part", want: "#part"},
				{name: "query suffix only ambiguous", sourcePath: "b.md", outputPath: "b.md", value: "?query", want: "?query"},
				{name: "malformed local escape ambiguous", sourcePath: "b.md", outputPath: "b.md", value: "%zz", want: "%zz"},
				{
					name: "malformed URL escape ambiguous", sourcePath: "b.md", outputPath: "b.md",
					value: "https://example.test/%zz", want: "https://example.test/%zz",
				},
				{name: "incoming broken", sourcePath: "b.md", outputPath: "b.md", value: broken, want: broken},
				{name: "incoming URL", sourcePath: "b.md", outputPath: "b.md", value: "https://example.test/a.md", want: "https://example.test/a.md"},
				{name: "incoming scope shaped", sourcePath: "b.md", outputPath: "b.md", value: "all assets in project X", want: "all assets in project X"},
				{name: "outgoing local", sourcePath: "a.md", outputPath: "nested/a.md", value: "refs/asset.md", want: "../refs/asset.md"},
				{name: "outgoing bundle relative", sourcePath: "a.md", outputPath: "nested/a.md", value: "/refs/asset.md", want: "/refs/asset.md"},
				{
					name: "outgoing local opaque query suffix", sourcePath: "a.md", outputPath: "nested/a.md",
					value: "refs/asset.md?next=/../x/%2f..", want: "../refs/asset.md?next=/../x/%2f..",
				},
				{
					name: "outgoing bundle opaque fragment suffix", sourcePath: "a.md", outputPath: "nested/a.md",
					value: "/refs/asset.md#part/../../x/%2E%2E%2F", want: "/refs/asset.md#part/../../x/%2E%2E%2F",
				},
				{
					name: "outgoing noncanonical bundle raw preserved", sourcePath: "a.md", outputPath: "nested/a.md",
					value: "/refs/dir/../asset.md#part/../../x", want: "/refs/dir/../asset.md#part/../../x",
				},
				{name: "outgoing broken", sourcePath: "a.md", outputPath: "nested/a.md", value: broken, want: broken},
				{name: "outgoing URL", sourcePath: "a.md", outputPath: "nested/a.md", value: "https://example.test/asset", want: "https://example.test/asset"},
				{name: "outgoing scope shaped", sourcePath: "a.md", outputPath: "nested/a.md", value: "all assets in project X", want: "all assets in project X"},
			}
			if field.field == bundle.PathFieldSourceResource {
				tests = append(tests,
					struct {
						name       string
						sourcePath string
						outputPath string
						value      string
						want       string
					}{name: "scope colon", sourcePath: "b.md", outputPath: "b.md", value: "project:acme", want: "project:acme"},
					struct {
						name       string
						sourcePath string
						outputPath string
						value      string
						want       string
					}{name: "scope slash", sourcePath: "b.md", outputPath: "b.md", value: "dashboards/exec-revenue", want: "dashboards/exec-revenue"},
					struct {
						name       string
						sourcePath string
						outputPath string
						value      string
						want       string
					}{name: "scope dot", sourcePath: "b.md", outputPath: "b.md", value: "dataset.production", want: "dataset.production"},
				)
			}
			for _, test := range tests {
				t.Run(test.name, func(t *testing.T) {
					// Act.
					got := rewritePathValue(loaded, test.sourcePath, test.outputPath, "a.md", "nested/a.md", test.value, field.field)

					// Assert.
					if got != test.want {
						t.Fatalf("rewritePathValue(%q) = %q, want %q", test.value, got, test.want)
					}
				})
			}
		})
	}
}

type v02LookupErrorContext struct {
	context.Context
	err error
}

func (c *v02LookupErrorContext) Err() error {
	return c.err
}

type v02FailAfterErrChecksContext struct {
	context.Context
	allowed int
	calls   int
	err     error
}

func (c *v02FailAfterErrChecksContext) Err() error {
	c.calls++
	if c.calls > c.allowed {
		return c.err
	}
	return nil
}
