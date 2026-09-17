package mutation

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/skosovsky/okf/store"
)

type v02DescriptorFixture struct {
	name, path, detail string
	value              store.Operation
	pointer            store.Operation
	typedNil           store.Operation
	want, wantAbsent   []byte
}

type v02PreflightNoReadSource struct{ t *testing.T }

func (s v02PreflightNoReadSource) Paths(context.Context) ([]string, error) {
	s.t.Fatal("invalid closed-union member reached Source.Paths")
	return nil, errors.New("unreachable")
}

func (s v02PreflightNoReadSource) ReadFile(context.Context, string) ([]byte, error) {
	s.t.Fatal("invalid closed-union member reached Source.ReadFile")
	return nil, errors.New("unreachable")
}

func (s v02PreflightNoReadSource) Manifest() store.Manifest {
	s.t.Fatal("invalid closed-union member reached Source.Manifest")
	return store.Manifest{}
}

func (s v02PreflightNoReadSource) ManifestContext(context.Context) (store.Manifest, error) {
	s.t.Fatal("invalid closed-union member reached Source.ManifestContext")
	return store.Manifest{}, errors.New("unreachable")
}

func TestPlannerV02OperationDescriptors_PublicBoundaryAndReplay(t *testing.T) {
	// Arrange.
	fixtures := v02DescriptorFixtures(t)
	if len(fixtures) != 9 {
		t.Fatalf("fixture count = %d, want 9 v0.2 operations", len(fixtures))
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name+"/value and replay", func(t *testing.T) {
			// Arrange.
			source := v02DescriptorSource()

			// Act.
			result, err := Plan(context.Background(), source, change(t, source, fixture.value))
			var first []byte
			if err == nil {
				first, err = result.Staged.ReadFile(context.Background(), fixture.path)
			}
			var repeated Result
			if err == nil {
				repeated, err = Plan(context.Background(), result.Staged, change(t, result.Staged, fixture.value))
			}
			var second []byte
			if err == nil {
				second, err = repeated.Staged.ReadFile(context.Background(), fixture.path)
			}

			// Assert.
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Preview.Plan) != 1 || reflect.TypeOf(result.Preview.Plan[0].Operation) != reflect.TypeOf(fixture.value) {
				t.Fatalf("planned operation = %#v, want exact %T value", result.Preview.Plan, fixture.value)
			}
			if got := result.Preview.Plan[0].Details; len(got) != 1 || got[0] != fixture.detail {
				t.Fatalf("plan details = %#v, want canonical wiring %q", got, fixture.detail)
			}
			if len(fixture.want) != 0 && !bytes.Contains(first, fixture.want) || len(fixture.wantAbsent) != 0 && bytes.Contains(first, fixture.wantAbsent) {
				t.Fatalf("%s wiring mismatch:\n%s", fixture.name, first)
			}
			if len(repeated.Preview.Writes) != 0 || !bytes.Equal(second, first) {
				t.Fatalf("replay changed desired state: writes=%#v\nfirst:\n%s\nsecond:\n%s", repeated.Preview.Writes, first, second)
			}
		})

	}

	t.Run("set_bundle_version/canonical diagnostic name", func(t *testing.T) {
		// Arrange.
		source := v02DescriptorSource()
		source["index.md"] = []byte("---\nokf_version: !!str \"0.1\"\n---\n# Notes\n\n* [A](a.md) - A\n")

		// Act.
		result, err := Plan(context.Background(), source, change(t, source, fixtures[8].value))

		// Assert.
		var presentation *PresentationError
		if !errors.As(err, &presentation) || presentation.Operation != "set_bundle_version" {
			t.Fatalf("Plan() error = %#v, want set_bundle_version PresentationError", err)
		}
		if result.Staged != nil || len(result.Preview.Writes) != 0 || len(result.Preview.Plan) != 0 {
			t.Fatalf("rejected tagged version exposed stage: %#v", result)
		}
	})
}

func TestPlannerV02ClosedUnionPreflightRejectsBeforeSourceIO(t *testing.T) {
	// Arrange. The revision is syntactically valid and deliberately cannot be
	// derived from source: validation must reject every non-value union member
	// before consulting paths, contents, or an optional manifest.
	fixtures := v02DescriptorFixtures(t)
	valid := fixtures[0].value
	precondition := store.RefExists{Ref: ref(t, "a")}
	tests := make([]struct {
		name          string
		operation     store.Operation
		preconditions []store.Precondition
	}, 0, len(fixtures)*2+2)
	for _, fixture := range fixtures {
		tests = append(tests,
			struct {
				name          string
				operation     store.Operation
				preconditions []store.Precondition
			}{name: fixture.name + "/pointer", operation: fixture.pointer},
			struct {
				name          string
				operation     store.Operation
				preconditions []store.Precondition
			}{name: fixture.name + "/typed nil", operation: fixture.typedNil},
		)
	}
	tests = append(tests,
		struct {
			name          string
			operation     store.Operation
			preconditions []store.Precondition
		}{name: "precondition/pointer", operation: valid, preconditions: []store.Precondition{&precondition}},
		struct {
			name          string
			operation     store.Operation
			preconditions []store.Precondition
		}{name: "precondition/typed nil", operation: valid, preconditions: []store.Precondition{(*store.RefExists)(nil)}},
	)

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changeSet := store.ChangeSet{
				Version:       store.ChangeSetFormatVersion,
				ID:            "v02-closed-union-preflight",
				Actor:         "principal",
				BaseRevision:  "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				Preconditions: test.preconditions,
				Operations:    []store.Operation{test.operation},
			}

			// Act.
			result, err := Plan(context.Background(), v02PreflightNoReadSource{t: t}, changeSet)

			// Assert.
			var invalid *store.InvalidChangeSet
			if !errors.As(err, &invalid) {
				t.Fatalf("Plan(%T) error = %#v, want InvalidChangeSet", test.operation, err)
			}
			if !reflect.DeepEqual(result, Result{}) {
				t.Fatalf("rejected union member result = %#v, want empty Result", result)
			}
		})
	}
}

func v02DescriptorFixtures(t *testing.T) []v02DescriptorFixture {
	t.Helper()
	id := ref(t, "a").ID
	verification := store.Verification{By: "human:reviewer", At: "2026-02-02T03:04:05Z"}
	removeVerification := store.Verification{By: "process:remove", At: "2026-01-01T00:00:00Z"}
	putSource, err := store.NewPutSource(id, store.ProvenanceSource{ID: "policy", Resource: "policy.md"})
	if err != nil {
		t.Fatal(err)
	}
	removeSelector, err := store.SourceByID("remove")
	if err != nil {
		t.Fatal(err)
	}
	removeSource, err := store.NewRemoveSource(id, removeSelector)
	if err != nil {
		t.Fatal(err)
	}
	usageWindow, err := store.NewSetUsageWindow(id, nil, &store.UsageWindow{From: "2026-02-01", To: "2026-02-28"})
	if err != nil {
		t.Fatal(err)
	}
	status := "stable"
	lifecycle, err := store.NewSetLifecycle(id, store.Lifecycle{Status: &status})
	if err != nil {
		t.Fatal(err)
	}
	computation, err := store.NewPutAttestedComputation(id, store.AttestedComputationContract{
		Mode:            store.AttestedComputationModeFile,
		Runtime:         "postgres",
		ComputationPath: "query.sql",
		Executor:        store.ExecutorContract{Resource: "run.md", Receipt: []string{"result"}},
		Attester:        store.AttesterContract{Resource: "attest.py"},
	})
	if err != nil {
		t.Fatal(err)
	}
	generated := store.SetGenerated{Concept: id, Generated: store.Generation{By: "process:new"}}
	ensure := store.EnsureVerification{Concept: id, Verification: verification}
	removeVerificationOperation := store.RemoveVerification{Concept: id, Verification: removeVerification}
	version := store.SetBundleVersion{Version: "0.2"}

	return []v02DescriptorFixture{
		{name: "set_generated", path: "a.md", detail: "set_generated desired state applied", value: generated, pointer: &generated, typedNil: (*store.SetGenerated)(nil), want: []byte("by: process:new")},
		{name: "ensure_verification", path: "a.md", detail: "ensure_verification desired state applied", value: ensure, pointer: &ensure, typedNil: (*store.EnsureVerification)(nil), want: []byte("human:reviewer")},
		{name: "remove_verification", path: "a.md", detail: "remove_verification desired state applied", value: removeVerificationOperation, pointer: &removeVerificationOperation, typedNil: (*store.RemoveVerification)(nil), wantAbsent: []byte("process:remove")},
		{name: "put_source", path: "a.md", detail: "put_source desired state applied", value: putSource, pointer: &putSource, typedNil: (*store.PutSource)(nil), want: []byte("id: policy")},
		{name: "remove_source", path: "a.md", detail: "remove_source desired state applied", value: removeSource, pointer: &removeSource, typedNil: (*store.RemoveSource)(nil), wantAbsent: []byte("id: remove")},
		{name: "set_usage_window", path: "a.md", detail: "set_usage_window desired state applied", value: usageWindow, pointer: &usageWindow, typedNil: (*store.SetUsageWindow)(nil), want: []byte("from: 2026-02-01")},
		{name: "set_lifecycle", path: "a.md", detail: "set_lifecycle desired state applied", value: lifecycle, pointer: &lifecycle, typedNil: (*store.SetLifecycle)(nil), want: []byte("status: stable")},
		{name: "put_attested_computation", path: "a.md", detail: "put_attested_computation desired state applied", value: computation, pointer: &computation, typedNil: (*store.PutAttestedComputation)(nil), want: []byte("computation: query.sql")},
		{name: "set_bundle_version", path: "index.md", detail: "root bundle version set", value: version, pointer: &version, typedNil: (*store.SetBundleVersion)(nil), want: []byte("okf_version: \"0.2\"")},
	}
}

func v02DescriptorSource() memorySource {
	return memorySource{
		"index.md": []byte("---\nokf_version: \"0.1\"\n---\n# Notes\n\n* [A](a.md) - A\n"),
		"a.md": []byte("---\n" +
			"type: Note\n" +
			"generated: {by: process:old}\n" +
			"verified: {by: process:remove, at: 2026-01-01T00:00:00Z}\n" +
			"sources: [{id: remove, resource: remove.md}]\n" +
			"usage_window: {from: 2026-01-01, to: 2026-01-31}\n" +
			"status: draft\n" +
			"---\nA\n"),
	}
}
