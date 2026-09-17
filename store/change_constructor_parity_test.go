package store

import (
	"encoding/binary"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
)

func TestV02MissingOperationConstructors_PreserveExactCallerValues(t *testing.T) {
	// Arrange.
	concept := testRef(t, "metrics/revenue", "").ID
	generatedInput := Generation{
		By: "process:daily:revenue",
		At: "2026-07-29T10:11:12.123456789+07:00",
	}
	ensureInput := Verification{
		By: "human:reviewer:primary",
		At: "2026-07-29T03:11:12Z",
	}
	removeInput := Verification{
		By: "human:reviewer:secondary",
		At: "2026-07-28T22:11:12-05:00",
	}

	// Act.
	generated, generatedErr := NewSetGenerated(concept, generatedInput)
	ensure, ensureErr := NewEnsureVerification(concept, ensureInput)
	remove, removeErr := NewRemoveVerification(concept, removeInput)
	version, versionErr := NewSetBundleVersion("12.34")
	generatedInput.By = "mutated"
	generatedInput.At = "2000-01-01T00:00:00Z"
	ensureInput.By = "mutated"
	ensureInput.At = "2000-01-01T00:00:00Z"
	removeInput.By = "mutated"
	removeInput.At = "2000-01-01T00:00:00Z"

	// Assert.
	if generatedErr != nil || ensureErr != nil || removeErr != nil || versionErr != nil {
		t.Fatalf("constructor errors: generated=%v ensure=%v remove=%v version=%v", generatedErr, ensureErr, removeErr, versionErr)
	}
	if generated.Concept.String() != "metrics/revenue" ||
		generated.Generated.By != "process:daily:revenue" ||
		generated.Generated.At != "2026-07-29T10:11:12.123456789+07:00" {
		t.Fatalf("NewSetGenerated() did not preserve exact input: %#v", generated)
	}
	if ensure.Concept.String() != "metrics/revenue" ||
		ensure.Verification.By != "human:reviewer:primary" ||
		ensure.Verification.At != "2026-07-29T03:11:12Z" {
		t.Fatalf("NewEnsureVerification() did not preserve exact input: %#v", ensure)
	}
	if remove.Concept.String() != "metrics/revenue" ||
		remove.Verification.By != "human:reviewer:secondary" ||
		remove.Verification.At != "2026-07-28T22:11:12-05:00" {
		t.Fatalf("NewRemoveVerification() did not preserve exact input: %#v", remove)
	}
	if version.Version != "12.34" {
		t.Fatalf("NewSetBundleVersion() version = %q, want exact input", version.Version)
	}
}

func TestV02MissingOperationConstructors_AcceptCanonicalBoundaries(t *testing.T) {
	// Arrange.
	concept := testRef(t, "metrics/revenue", "").ID
	tests := []struct {
		name string
		act  func() error
	}{
		{
			name: "generated timestamp omitted",
			act: func() error {
				_, err := NewSetGenerated(concept, Generation{By: "process:generator"})
				return err
			},
		},
		{
			name: "verification RFC3339 fractional boundary",
			act: func() error {
				_, err := NewEnsureVerification(concept, Verification{
					By: "human:reviewer",
					At: "2026-07-29T10:11:12.999999999+14:00",
				})
				return err
			},
		},
		{
			name: "verification negative offset boundary",
			act: func() error {
				_, err := NewRemoveVerification(concept, Verification{
					By: "human:reviewer",
					At: "2026-07-29T10:11:12-12:00",
				})
				return err
			},
		},
		{
			name: "zero bundle version",
			act: func() error {
				_, err := NewSetBundleVersion("0.0")
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Act.
			err := test.act()

			// Assert.
			if err != nil {
				t.Fatalf("constructor error = %v, want nil", err)
			}
		})
	}
}

func TestV02MissingOperationConstructors_RejectInvalidInputAndReturnZero(t *testing.T) {
	// Arrange.
	concept := testRef(t, "metrics/revenue", "").ID
	index := testRef(t, "index", "").ID
	log := testRef(t, "nested/log", "").ID
	tests := []struct {
		name string
		act  func() (any, error)
	}{
		{
			name: "generated zero concept",
			act: func() (any, error) {
				return NewSetGenerated(bundle.ConceptID{}, Generation{By: "process:generator"})
			},
		},
		{
			name: "generated reserved index",
			act: func() (any, error) {
				return NewSetGenerated(index, Generation{By: "process:generator"})
			},
		},
		{
			name: "generated invalid actor",
			act: func() (any, error) {
				return NewSetGenerated(concept, Generation{By: ""})
			},
		},
		{
			name: "generated invalid optional timestamp",
			act: func() (any, error) {
				return NewSetGenerated(concept, Generation{By: "process:generator", At: "tomorrow"})
			},
		},
		{
			name: "ensure reserved log",
			act: func() (any, error) {
				return NewEnsureVerification(log, Verification{By: "human:reviewer", At: "2026-07-29T00:00:00Z"})
			},
		},
		{
			name: "ensure missing timestamp",
			act: func() (any, error) {
				return NewEnsureVerification(concept, Verification{By: "human:reviewer"})
			},
		},
		{
			name: "ensure invalid actor",
			act: func() (any, error) {
				return NewEnsureVerification(concept, Verification{By: "human", At: "2026-07-29T00:00:00Z"})
			},
		},
		{
			name: "remove zero concept",
			act: func() (any, error) {
				return NewRemoveVerification(bundle.ConceptID{}, Verification{By: "human:reviewer", At: "2026-07-29T00:00:00Z"})
			},
		},
		{
			name: "remove invalid timestamp",
			act: func() (any, error) {
				return NewRemoveVerification(concept, Verification{By: "human:reviewer", At: "2026-07-29"})
			},
		},
		{
			name: "version empty",
			act: func() (any, error) {
				return NewSetBundleVersion("")
			},
		},
		{
			name: "version leading zero",
			act: func() (any, error) {
				return NewSetBundleVersion("00.2")
			},
		},
		{
			name: "version extra component",
			act: func() (any, error) {
				return NewSetBundleVersion("0.2.0")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Act.
			got, err := test.act()

			// Assert.
			if !errors.Is(err, ErrInvalidChangeSet) {
				t.Fatalf("constructor error = %v, want ErrInvalidChangeSet", err)
			}
			if got == nil || !reflect.ValueOf(got).IsZero() {
				t.Fatalf("constructor result = %#v, want concrete zero value", got)
			}
		})
	}
}

func TestNewSetBundleVersionMatchesSharedCanonicalGrammarInParallel(t *testing.T) {
	t.Parallel()

	veryLong := strings.Repeat("9", 4096) + "." + strings.Repeat("8", 4096)
	tests := []struct {
		name    string
		version string
		valid   bool
	}{
		{name: "current v0.2", version: "0.2", valid: true},
		{name: "future best effort", version: "999.999", valid: true},
		{name: "very long canonical", version: veryLong, valid: true},
		{name: "leading whitespace", version: " 0.2"},
		{name: "trailing whitespace", version: "0.2 "},
		{name: "non string scalar text", version: "true"},
		{name: "non numeric component", version: "1.two"},
		{name: "leading zero", version: "00.2"},
		{name: "extra component", version: "0.2.0"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange and act.
			operation, err := NewSetBundleVersion(test.version)
			_, parseErr := bundle.ParseOKFVersion(test.version)

			// Assert.
			if (err == nil) != test.valid || (parseErr == nil) != test.valid {
				t.Fatalf("NewSetBundleVersion(%q) error=%v, ParseOKFVersion error=%v, valid=%v", test.version, err, parseErr, test.valid)
			}
			if test.valid && operation.Version != test.version {
				t.Fatalf("Version = %q, want exact input", operation.Version)
			}
		})
	}
}

func TestV02OperationConstructorInventory_MatchesConcreteTypesAndTags(t *testing.T) {
	// Arrange.
	concept := testRef(t, "metrics/revenue", "").ID
	selector, err := SourceByID("revenue-policy")
	if err != nil {
		t.Fatal(err)
	}
	status := "stable"
	constructors := []struct {
		name     string
		wantType reflect.Type
		wantTag  string
		act      func() (Operation, error)
	}{
		{
			name:     "NewSetGenerated",
			wantType: reflect.TypeOf(SetGenerated{}),
			wantTag:  "set_generated",
			act: func() (Operation, error) {
				return NewSetGenerated(concept, Generation{By: "process:generator"})
			},
		},
		{
			name:     "NewEnsureVerification",
			wantType: reflect.TypeOf(EnsureVerification{}),
			wantTag:  "ensure_verification",
			act: func() (Operation, error) {
				return NewEnsureVerification(concept, Verification{By: "human:reviewer", At: "2026-07-29T00:00:00Z"})
			},
		},
		{
			name:     "NewRemoveVerification",
			wantType: reflect.TypeOf(RemoveVerification{}),
			wantTag:  "remove_verification",
			act: func() (Operation, error) {
				return NewRemoveVerification(concept, Verification{By: "human:reviewer", At: "2026-07-29T00:00:00Z"})
			},
		},
		{
			name:     "NewPutSource",
			wantType: reflect.TypeOf(PutSource{}),
			wantTag:  "put_source",
			act: func() (Operation, error) {
				return NewPutSource(concept, ProvenanceSource{ID: "revenue-policy", Resource: "references/revenue.md"})
			},
		},
		{
			name:     "NewRemoveSource",
			wantType: reflect.TypeOf(RemoveSource{}),
			wantTag:  "remove_source",
			act: func() (Operation, error) {
				return NewRemoveSource(concept, selector)
			},
		},
		{
			name:     "NewSetUsageWindow",
			wantType: reflect.TypeOf(SetUsageWindow{}),
			wantTag:  "set_usage_window",
			act: func() (Operation, error) {
				return NewSetUsageWindow(concept, nil, &UsageWindow{From: "2026-07-01", To: "2026-07-29"})
			},
		},
		{
			name:     "NewSetLifecycle",
			wantType: reflect.TypeOf(SetLifecycle{}),
			wantTag:  "set_lifecycle",
			act: func() (Operation, error) {
				return NewSetLifecycle(concept, Lifecycle{Status: &status})
			},
		},
		{
			name:     "NewPutAttestedComputation",
			wantType: reflect.TypeOf(PutAttestedComputation{}),
			wantTag:  "put_attested_computation",
			act: func() (Operation, error) {
				return NewPutAttestedComputation(concept, AttestedComputationContract{
					Mode:              AttestedComputationModeInline,
					Runtime:           "go",
					InlineComputation: "return 42",
					InlineLanguage:    "go",
					Executor: ExecutorContract{
						Resource: "references/run.md",
						Receipt:  []string{"result"},
					},
					Attester: AttesterContract{Resource: "references/attest.md"},
				})
			},
		},
		{
			name:     "NewSetBundleVersion",
			wantType: reflect.TypeOf(SetBundleVersion{}),
			wantTag:  "set_bundle_version",
			act: func() (Operation, error) {
				return NewSetBundleVersion("0.2")
			},
		},
	}

	// Act.
	seenTypes := make(map[reflect.Type]string, len(constructors))
	seenTags := make(map[string]string, len(constructors))
	for _, constructor := range constructors {
		operation, constructorErr := constructor.act()
		if constructorErr != nil {
			t.Fatalf("%s() error = %v", constructor.name, constructorErr)
		}
		typ := reflect.TypeOf(operation)
		tag := firstOperationCanonicalTag(t, operation)

		// Assert.
		if validationErr := operation.validate(); validationErr != nil {
			t.Fatalf("%s() operation validation error = %v", constructor.name, validationErr)
		}
		if typ != constructor.wantType {
			t.Fatalf("%s() type = %v, want %v", constructor.name, typ, constructor.wantType)
		}
		if tag != constructor.wantTag {
			t.Fatalf("%s() canonical tag = %q, want %q", constructor.name, tag, constructor.wantTag)
		}
		if previous, duplicate := seenTypes[typ]; duplicate {
			t.Fatalf("%s and %s construct the same operation type %v", previous, constructor.name, typ)
		}
		if previous, duplicate := seenTags[tag]; duplicate {
			t.Fatalf("%s and %s emit the same operation tag %q", previous, constructor.name, tag)
		}
		seenTypes[typ] = constructor.name
		seenTags[tag] = constructor.name
	}

	// Assert.
	if len(constructors) != 9 || len(seenTypes) != 9 || len(seenTags) != 9 {
		t.Fatalf("constructor inventory = %d entries, %d types, %d tags; want 9/9/9", len(constructors), len(seenTypes), len(seenTags))
	}
}

func firstOperationCanonicalTag(t *testing.T, operation Operation) string {
	t.Helper()

	encoder := canonicalEncoder{}
	operation.appendCanonical(&encoder)
	raw := encoder.Bytes()
	if len(raw) < 8 {
		t.Fatalf("canonical operation is too short: %x", raw)
	}
	length := binary.BigEndian.Uint64(raw[:8])
	if length > uint64(len(raw)-8) {
		t.Fatalf("canonical operation tag length = %d, payload bytes = %d", length, len(raw)-8)
	}
	return string(raw[8 : 8+int(length)])
}
