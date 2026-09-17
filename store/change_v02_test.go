package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
)

func TestV02OpenStringPreflight_PreservesExecutableContractDomain(t *testing.T) {
	// Arrange.
	concept := testRef(t, "computations/revenue", "").ID
	longValue := strings.Repeat("domain-value-", 400)
	sourceInput := ProvenanceSource{
		ID:       "source",
		Resource: " all assets\nin project X ",
		Title:    longValue,
		Author:   " team:finance ",
	}
	contractInput := AttestedComputationContract{
		Mode:    AttestedComputationModeFile,
		Runtime: " org/runtime:v1?scope=all projects ",
		Parameters: []ComputationParameter{{
			Name: "fiscal\n year",
			Type: longValue,
		}},
		ComputationPath: " queries/\x00revenue.sql ",
		Executor: ExecutorContract{
			Resource: " executor/\x7frun.md ",
			Receipt:  []string{" result\nfield ", longValue},
		},
		Attester: AttesterContract{Resource: " attester/\x00check.py "},
	}

	// Act.
	source, sourceErr := NewPutSource(concept, sourceInput)
	computation, computationErr := NewPutAttestedComputation(concept, contractInput)
	change := ChangeSet{
		Version:      ChangeSetFormatVersion,
		ID:           "v02-open-string-domain",
		Actor:        "principal",
		BaseRevision: testRevision(t),
		Operations:   []Operation{source, computation},
	}
	canonical, canonicalErr := change.CanonicalBytes()
	firstDigest, firstDigestErr := change.RequestDigest()
	secondDigest, secondDigestErr := change.RequestDigest()
	jsonBytes, jsonErr := json.Marshal(contractInput)
	var jsonRoundTrip AttestedComputationContract
	jsonDecodeErr := json.Unmarshal(jsonBytes, &jsonRoundTrip)

	// Assert.
	if sourceErr != nil || computationErr != nil || canonicalErr != nil ||
		firstDigestErr != nil || secondDigestErr != nil || jsonErr != nil || jsonDecodeErr != nil {
		t.Fatalf(
			"open-string errors: source=%v computation=%v canonical=%v digests=(%v,%v) json=(%v,%v)",
			sourceErr, computationErr, canonicalErr,
			firstDigestErr, secondDigestErr, jsonErr, jsonDecodeErr,
		)
	}
	if got := source.Source(); !reflect.DeepEqual(got, sourceInput) {
		t.Fatalf("source getter = %#v, want exact %#v", got, sourceInput)
	}
	if got := computation.Contract(); !reflect.DeepEqual(got, contractInput) {
		t.Fatalf("computation getter = %#v, want exact %#v", got, contractInput)
	}
	if !reflect.DeepEqual(jsonRoundTrip, contractInput) {
		t.Fatalf("JSON round trip = %#v, want exact %#v; wire=%q", jsonRoundTrip, contractInput, jsonBytes)
	}
	if firstDigest != secondDigest {
		t.Fatalf("request digest is not deterministic: %q != %q", firstDigest, secondDigest)
	}
	for name, value := range map[string]string{
		"multiline resource": sourceInput.Resource,
		"long title":         sourceInput.Title,
		"padded author":      sourceInput.Author,
		"punctuated runtime": contractInput.Runtime,
		"multiline name":     contractInput.Parameters[0].Name,
		"control path":       contractInput.ComputationPath,
		"control executor":   contractInput.Executor.Resource,
		"multiline receipt":  contractInput.Executor.Receipt[0],
		"control attester":   contractInput.Attester.Resource,
	} {
		if !bytes.Contains(canonical, []byte(value)) {
			t.Fatalf("canonical bytes lost exact %s %q", name, value)
		}
	}
}

func TestV02OpenStringPreflight_RejectsOnlyBlankOrMalformedUTF8(t *testing.T) {
	// Arrange.
	concept := testRef(t, "computations/revenue", "").ID
	validContract := func() AttestedComputationContract {
		return AttestedComputationContract{
			Mode:            AttestedComputationModeFile,
			Runtime:         "runtime",
			ComputationPath: "query.sql",
			Executor: ExecutorContract{
				Resource: "run.md",
				Receipt:  []string{"result"},
			},
			Attester: AttesterContract{Resource: "attest.py"},
		}
	}
	invalidUTF8 := string([]byte{'v', 0xff})
	tests := []struct {
		name string
		act  func() error
	}{
		{
			name: "blank source resource",
			act: func() error {
				_, err := NewPutSource(concept, ProvenanceSource{Resource: " \t\r\n "})
				return err
			},
		},
		{
			name: "malformed UTF-8 source title",
			act: func() error {
				_, err := NewPutSource(concept, ProvenanceSource{Resource: "source.md", Title: invalidUTF8})
				return err
			},
		},
		{
			name: "blank runtime",
			act: func() error {
				contract := validContract()
				contract.Runtime = "\u00a0\t"
				_, err := NewPutAttestedComputation(concept, contract)
				return err
			},
		},
		{
			name: "malformed UTF-8 parameter type",
			act: func() error {
				contract := validContract()
				contract.Parameters = []ComputationParameter{{Name: "value", Type: invalidUTF8}}
				_, err := NewPutAttestedComputation(concept, contract)
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Act.
			err := test.act()

			// Assert.
			if !errors.Is(err, ErrInvalidChangeSet) {
				t.Fatalf("constructor error = %v, want ErrInvalidChangeSet", err)
			}
		})
	}
}

func TestAttestedComputationModeValidation_ExplicitPresenceInvariant(t *testing.T) {
	// Arrange.
	concept := testRef(t, "computations/revenue", "").ID
	base := AttestedComputationContract{
		Mode:     AttestedComputationModeInline,
		Runtime:  "sql",
		Executor: ExecutorContract{Resource: "run.md", Receipt: []string{"result"}},
		Attester: AttesterContract{Resource: "attest.py"},
	}
	tests := []struct {
		name    string
		mutate  func(*AttestedComputationContract)
		wantErr string
	}{
		{
			name:   "empty inline payload",
			mutate: func(*AttestedComputationContract) {},
		},
		{
			name: "absent mode and payload",
			mutate: func(contract *AttestedComputationContract) {
				contract.Mode = ""
			},
			wantErr: "invalid change set: invalid attested computation mode",
		},
		{
			name: "unknown mode",
			mutate: func(contract *AttestedComputationContract) {
				contract.Mode = "stream"
			},
			wantErr: "invalid change set: invalid attested computation mode",
		},
		{
			name: "inline mode mixed with file path",
			mutate: func(contract *AttestedComputationContract) {
				contract.ComputationPath = "query.sql"
				contract.InlineComputation = "SELECT 1"
			},
			wantErr: "invalid change set: inline computation mode cannot declare a computation path",
		},
		{
			name: "file mode without path",
			mutate: func(contract *AttestedComputationContract) {
				contract.Mode = AttestedComputationModeFile
			},
			wantErr: "invalid change set: invalid computation path",
		},
		{
			name: "file mode mixed with inline payload",
			mutate: func(contract *AttestedComputationContract) {
				contract.Mode = AttestedComputationModeFile
				contract.ComputationPath = "query.sql"
				contract.InlineComputation = "SELECT 1"
			},
			wantErr: "invalid change set: file computation mode cannot declare inline computation",
		},
		{
			name: "file mode mixed with inline language",
			mutate: func(contract *AttestedComputationContract) {
				contract.Mode = AttestedComputationModeFile
				contract.ComputationPath = "query.sql"
				contract.InlineLanguage = "sql"
			},
			wantErr: "invalid change set: file computation mode cannot declare inline language",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			contract := cloneAttestedComputationContract(base)
			test.mutate(&contract)

			// Act.
			operation, err := NewPutAttestedComputation(concept, contract)

			// Assert.
			if test.wantErr == "" {
				if err != nil || !reflect.DeepEqual(operation.Contract(), contract) {
					t.Fatalf("empty inline constructor = (%#v, %v), want exact contract", operation, err)
				}
				return
			}
			if !errors.Is(err, ErrInvalidChangeSet) || err.Error() != test.wantErr {
				t.Fatalf("constructor error = %v, want %q wrapping ErrInvalidChangeSet", err, test.wantErr)
			}
		})
	}
}

func TestAttestedComputationCanonicalEncoding_LengthFramesExplicitMode(t *testing.T) {
	// Arrange.
	concept := testRef(t, "computations/revenue", "").ID
	inlineContract := AttestedComputationContract{
		Mode:     AttestedComputationModeInline,
		Runtime:  "sql",
		Executor: ExecutorContract{Resource: "run.md", Receipt: []string{"result"}},
		Attester: AttesterContract{Resource: "attest.py"},
	}
	fileContract := cloneAttestedComputationContract(inlineContract)
	fileContract.Mode = AttestedComputationModeFile
	fileContract.ComputationPath = "query.sql"
	inlineOperation, inlineErr := NewPutAttestedComputation(concept, inlineContract)
	fileOperation, fileErr := NewPutAttestedComputation(concept, fileContract)
	inlineEncoding := canonicalEncoder{}
	appendCanonicalAttestedComputationContract(&inlineEncoding, inlineContract)
	fileEncoding := canonicalEncoder{}
	appendCanonicalAttestedComputationContract(&fileEncoding, fileContract)

	// Act.
	inlineDigest := canonicalOperationDigest(t, inlineOperation)
	fileDigest := canonicalOperationDigest(t, fileOperation)

	// Assert.
	if inlineErr != nil || fileErr != nil {
		t.Fatalf("constructors errors = inline:%v file:%v", inlineErr, fileErr)
	}
	inlineModeFrame := append([]byte{0, 0, 0, 0, 0, 0, 0, byte(len(AttestedComputationModeInline))}, AttestedComputationModeInline...)
	fileModeFrame := append([]byte{0, 0, 0, 0, 0, 0, 0, byte(len(AttestedComputationModeFile))}, AttestedComputationModeFile...)
	if !bytes.HasPrefix(inlineEncoding.Bytes(), inlineModeFrame) ||
		!bytes.HasPrefix(fileEncoding.Bytes(), fileModeFrame) {
		t.Fatalf(
			"mode framing missing: inline=%x file=%x",
			inlineEncoding.Bytes(),
			fileEncoding.Bytes(),
		)
	}
	if bytes.Equal(inlineEncoding.Bytes(), fileEncoding.Bytes()) || inlineDigest == fileDigest {
		t.Fatalf(
			"mode canonical identity collided: encodings_equal=%t digests=%s/%s",
			bytes.Equal(inlineEncoding.Bytes(), fileEncoding.Bytes()),
			inlineDigest,
			fileDigest,
		)
	}
}

func TestProvenanceSourceValidation_StableFieldPriority(t *testing.T) {
	// Arrange.
	concept := testRef(t, "computations/revenue", "").ID
	tests := []struct {
		name    string
		title   string
		author  string
		wantErr string
	}{
		{
			name:    "title only",
			title:   " \t ",
			author:  "Ada Lovelace",
			wantErr: "invalid change set: invalid source title",
		},
		{
			name:    "author only",
			title:   "Analytical Engine",
			author:  "\n",
			wantErr: "invalid change set: invalid source author",
		},
		{
			name:    "both invalid",
			title:   " \t ",
			author:  "\n",
			wantErr: "invalid change set: invalid source title",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := ProvenanceSource{
				Resource: "source.md",
				Title:    test.title,
				Author:   test.author,
			}

			for iteration := 0; iteration < 1024; iteration++ {
				// Act.
				_, putErr := NewPutSource(concept, source)
				_, exactErr := SourceByExact(source)

				// Assert.
				for _, result := range [...]struct {
					constructor string
					err         error
				}{
					{constructor: "NewPutSource", err: putErr},
					{constructor: "SourceByExact", err: exactErr},
				} {
					if !errors.Is(result.err, ErrInvalidChangeSet) {
						t.Fatalf(
							"%s iteration %d: errors.Is(err, ErrInvalidChangeSet) = false for %v",
							result.constructor,
							iteration,
							result.err,
						)
					}
					if result.err.Error() != test.wantErr {
						t.Fatalf(
							"%s iteration %d: error = %q, want %q",
							result.constructor,
							iteration,
							result.err,
							test.wantErr,
						)
					}
				}
			}
		})
	}
}

func TestSourceIDPreflight_PreservesExactAttributionIdentityDomain(t *testing.T) {
	// Arrange.
	concept := testRef(t, "metrics/revenue", "").ID
	validIDs := []struct {
		name string
		id   string
	}{
		{name: "padded", id: " padded "},
		{name: "multiline", id: "line\nid"},
		{name: "controls", id: "id\x00\x7f"},
		{name: "over 4096 bytes", id: strings.Repeat("source-id-", 500)},
	}

	for _, test := range validIDs {
		t.Run(test.name, func(t *testing.T) {
			// Act.
			selector, selectorErr := SourceByID(test.id)
			source, sourceErr := NewPutSource(
				concept,
				ProvenanceSource{ID: test.id, Resource: "source.md"},
			)
			selectedID, selected := selector.ID()

			// Assert.
			if selectorErr != nil || sourceErr != nil {
				t.Fatalf(
					"source ID %q errors = selector:%v source:%v",
					test.id,
					selectorErr,
					sourceErr,
				)
			}
			if !selected || selectedID != test.id || source.Source().ID != test.id {
				t.Fatalf(
					"source ID %q was normalized: selected=(%q,%t) source=%q",
					test.id,
					selectedID,
					selected,
					source.Source().ID,
				)
			}
		})
	}

	// Act.
	upper, upperErr := SourceByID("Source")
	lower, lowerErr := SourceByID("source")
	padded, paddedErr := SourceByID(" source ")

	// Assert.
	upperID, _ := upper.ID()
	lowerID, _ := lower.ID()
	paddedID, _ := padded.ID()
	if upperErr != nil || lowerErr != nil || paddedErr != nil {
		t.Fatalf("distinct source ID errors = %v, %v, %v", upperErr, lowerErr, paddedErr)
	}
	if upperID == lowerID || lowerID == paddedID || upperID == paddedID {
		t.Fatalf("distinct source IDs were normalized: %q, %q, %q", upperID, lowerID, paddedID)
	}
}

func TestSourceIDPreflight_RejectsBlankOrMalformedUTF8(t *testing.T) {
	// Arrange.
	invalid := []struct {
		name string
		id   string
	}{
		{name: "empty", id: ""},
		{name: "whitespace only", id: " \t\r\n\u00a0 "},
		{name: "malformed UTF-8", id: string([]byte{'s', 0xff})},
	}

	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			// Act.
			selector, err := SourceByID(test.id)

			// Assert.
			if selector != (SourceSelector{}) || !errors.Is(err, ErrInvalidChangeSet) {
				t.Fatalf(
					"SourceByID(%q) = %#v, %v; want zero ErrInvalidChangeSet",
					test.id,
					selector,
					err,
				)
			}
		})
	}
}

func TestV02OperationConstructorInventory_CoversAllNineClosedUnionVariants(t *testing.T) {
	// Arrange.
	concept := testRef(t, "metrics/revenue", "").ID
	selector, err := SourceByID("revenue-policy")
	if err != nil {
		t.Fatal(err)
	}
	window := UsageWindow{From: "2026-07-01", To: "2026-07-29"}
	status := "stable"
	generated := Generation{By: "process:daily:revenue", At: "2026-07-29T10:11:12.123456789+07:00"}
	ensure := Verification{By: "human:reviewer:primary", At: "2026-07-29T03:11:12Z"}
	remove := Verification{By: "human:reviewer:secondary", At: "2026-07-28T22:11:12-05:00"}
	source := ProvenanceSource{ID: "revenue-policy", Resource: "references/revenue.md"}
	contract := AttestedComputationContract{
		Mode:              AttestedComputationModeInline,
		Runtime:           "go",
		InlineComputation: "return 42",
		InlineLanguage:    "go",
		Executor:          ExecutorContract{Resource: "references/run.md", Receipt: []string{"result"}},
		Attester:          AttesterContract{Resource: "references/attest.md"},
	}
	tests := []struct {
		name string
		want Operation
		act  func() (Operation, error)
	}{
		{
			name: "NewSetGenerated",
			want: SetGenerated{Concept: concept, Generated: generated},
			act:  func() (Operation, error) { return NewSetGenerated(concept, generated) },
		},
		{
			name: "NewEnsureVerification",
			want: EnsureVerification{Concept: concept, Verification: ensure},
			act:  func() (Operation, error) { return NewEnsureVerification(concept, ensure) },
		},
		{
			name: "NewRemoveVerification",
			want: RemoveVerification{Concept: concept, Verification: remove},
			act:  func() (Operation, error) { return NewRemoveVerification(concept, remove) },
		},
		{
			name: "NewPutSource",
			want: PutSource{Concept: concept, source: source},
			act:  func() (Operation, error) { return NewPutSource(concept, source) },
		},
		{
			name: "NewRemoveSource",
			want: RemoveSource{Concept: concept, selector: selector},
			act:  func() (Operation, error) { return NewRemoveSource(concept, selector) },
		},
		{
			name: "NewSetUsageWindow",
			want: SetUsageWindow{Concept: concept, window: &window},
			act:  func() (Operation, error) { return NewSetUsageWindow(concept, nil, &window) },
		},
		{
			name: "NewSetLifecycle",
			want: SetLifecycle{Concept: concept, lifecycle: Lifecycle{Status: &status}},
			act:  func() (Operation, error) { return NewSetLifecycle(concept, Lifecycle{Status: &status}) },
		},
		{
			name: "NewPutAttestedComputation",
			want: PutAttestedComputation{Concept: concept, contract: contract},
			act:  func() (Operation, error) { return NewPutAttestedComputation(concept, contract) },
		},
		{
			name: "NewSetBundleVersion",
			want: SetBundleVersion{Version: "12.34"},
			act:  func() (Operation, error) { return NewSetBundleVersion("12.34") },
		},
	}

	seenTypes := make(map[reflect.Type]string, len(tests))
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Act.
			got, constructorErr := test.act()

			// Assert.
			if constructorErr != nil {
				t.Fatalf("constructor error = %v", constructorErr)
			}
			if validationErr := got.validate(); validationErr != nil {
				t.Fatalf("constructed operation validation error = %v", validationErr)
			}
			if reflect.TypeOf(got) != reflect.TypeOf(test.want) {
				t.Fatalf("concrete type = %T, want %T", got, test.want)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("constructed operation = %#v, want exact caller values %#v", got, test.want)
			}
			if previous, duplicate := seenTypes[reflect.TypeOf(got)]; duplicate {
				t.Fatalf("same concrete type is owned by %s", previous)
			}
			seenTypes[reflect.TypeOf(got)] = test.name
		})
	}
	if len(tests) != 9 || len(seenTypes) != 9 {
		t.Fatalf("constructor inventory = %d entries, %d concrete types; want 9/9", len(tests), len(seenTypes))
	}
}

func TestV02OperationConstructors_AcceptCanonicalBoundaries(t *testing.T) {
	// Arrange.
	concept := testRef(t, "metrics/revenue", "").ID
	tests := []struct {
		name string
		act  func() error
	}{
		{name: "generated timestamp omitted", act: func() error {
			_, err := NewSetGenerated(concept, Generation{By: "process:generator"})
			return err
		}},
		{name: "verification fractional offset boundary", act: func() error {
			_, err := NewEnsureVerification(concept, Verification{
				By: "human:reviewer", At: "2026-07-29T10:11:12.999999999+14:00",
			})
			return err
		}},
		{name: "verification negative offset boundary", act: func() error {
			_, err := NewRemoveVerification(concept, Verification{
				By: "human:reviewer", At: "2026-07-29T10:11:12-12:00",
			})
			return err
		}},
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

func TestV02OperationConstructors_RejectInvalidInputAndReturnConcreteZero(t *testing.T) {
	// Arrange.
	concept := testRef(t, "metrics/revenue", "").ID
	index := testRef(t, "index", "").ID
	log := testRef(t, "nested/log", "").ID
	tests := []struct {
		name string
		act  func() (any, error)
	}{
		{name: "generated zero concept", act: func() (any, error) {
			return NewSetGenerated(bundle.ConceptID{}, Generation{By: "process:generator"})
		}},
		{name: "generated reserved index", act: func() (any, error) {
			return NewSetGenerated(index, Generation{By: "process:generator"})
		}},
		{name: "generated invalid actor", act: func() (any, error) {
			return NewSetGenerated(concept, Generation{})
		}},
		{name: "generated invalid optional timestamp", act: func() (any, error) {
			return NewSetGenerated(concept, Generation{By: "process:generator", At: "tomorrow"})
		}},
		{name: "ensure reserved log", act: func() (any, error) {
			return NewEnsureVerification(log, Verification{By: "human:reviewer", At: "2026-07-29T00:00:00Z"})
		}},
		{name: "ensure missing timestamp", act: func() (any, error) {
			return NewEnsureVerification(concept, Verification{By: "human:reviewer"})
		}},
		{name: "ensure invalid actor", act: func() (any, error) {
			return NewEnsureVerification(concept, Verification{By: "human", At: "2026-07-29T00:00:00Z"})
		}},
		{name: "remove zero concept", act: func() (any, error) {
			return NewRemoveVerification(bundle.ConceptID{}, Verification{By: "human:reviewer", At: "2026-07-29T00:00:00Z"})
		}},
		{name: "remove invalid timestamp", act: func() (any, error) {
			return NewRemoveVerification(concept, Verification{By: "human:reviewer", At: "2026-07-29"})
		}},
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

func TestNewSetBundleVersionUsesSharedCanonicalGrammarWithoutComponentCeiling(t *testing.T) {
	// Arrange.
	veryLong := strings.Repeat("9", 4096) + "." + strings.Repeat("8", 4096)
	tests := []struct {
		name    string
		version string
		valid   bool
	}{
		{name: "current", version: "0.2", valid: true},
		{name: "future", version: "999.999", valid: true},
		{name: "very long", version: veryLong, valid: true},
		{name: "zero", version: "0.0", valid: true},
		{name: "multi digit", version: "12.34", valid: true},
		{name: "empty", version: ""},
		{name: "whitespace", version: " \t\r\n"},
		{name: "leading whitespace", version: " 0.2"},
		{name: "trailing whitespace", version: "0.2 "},
		{name: "scalar-like", version: "true"},
		{name: "non numeric", version: "1.two"},
		{name: "leading zero", version: "00.2"},
		{name: "extra component", version: "0.2.0"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Act.
			operation, err := NewSetBundleVersion(test.version)
			_, parseErr := bundle.ParseOKFVersion(test.version)

			// Assert.
			if (err == nil) != test.valid || (parseErr == nil) != test.valid {
				t.Fatalf("constructor error = %v, parser error = %v, valid = %t", err, parseErr, test.valid)
			}
			if test.valid {
				if operation.Version != test.version {
					t.Fatalf("version = %q, want exact input %q", operation.Version, test.version)
				}
				return
			}
			if operation != (SetBundleVersion{}) || !errors.Is(err, ErrInvalidChangeSet) {
				t.Fatalf("invalid constructor result = %#v, %v; want concrete zero ErrInvalidChangeSet", operation, err)
			}
		})
	}
}

func TestV02OperationConstructors_DeepCopyCallerOwnedValuesAndDigest(t *testing.T) {
	// Arrange.
	concept := testRef(t, "metrics/revenue", "").ID
	usageCount := uint64(5000)
	sourceWindow := UsageWindow{From: "2026-06-01", To: "2026-06-30"}
	sourceInput := ProvenanceSource{
		ID:           "revenue-policy",
		Resource:     "references/revenue-policy.md",
		Title:        "Revenue policy",
		Author:       "team:finance",
		UsageCount:   &usageCount,
		LastModified: "2026-05-30",
		UsageWindow:  &sourceWindow,
	}
	source, err := NewPutSource(concept, sourceInput)
	if err != nil {
		t.Fatal(err)
	}
	status, staleAfter := "stable", "2026-12-31"
	lifecycleInput := Lifecycle{Status: &status, StaleAfter: &staleAfter}
	lifecycle, err := NewSetLifecycle(concept, lifecycleInput)
	if err != nil {
		t.Fatal(err)
	}
	parameters := []ComputationParameter{{Name: "year", Type: "integer", Required: true}}
	receipt := []string{"job_id", "executed_sql", "result"}
	contractInput := AttestedComputationContract{
		Mode:              AttestedComputationModeInline,
		Runtime:           "bigquery",
		Parameters:        parameters,
		InlineComputation: "SELECT SUM(amount) FROM revenue WHERE year = @year",
		InlineLanguage:    "sql",
		Executor:          ExecutorContract{Resource: "references/run.md", Receipt: receipt},
		Attester:          AttesterContract{Resource: "references/attest.py"},
	}
	computation, err := NewPutAttestedComputation(concept, contractInput)
	if err != nil {
		t.Fatal(err)
	}
	migrationInput := V01ToV02Migration{
		GeneratedBy:       "process:migration",
		GeneratedAt:       []MigrationGeneratedAt{{Path: "metrics/revenue.md", At: "2026-06-20T22:53:05Z"}},
		TimestampPolicy:   LegacyTimestampRemoveAfterCopy,
		TimestampConflict: TimestampConflictReject,
		Citations: []DocumentCitationMigration{{
			Path: "metrics/revenue.md",
			Entries: []LegacyCitationMapping{{
				LegacyNumber: 1,
				LegacyEntry:  "Revenue policy",
				SourceID:     "revenue-policy",
				Title:        "Revenue policy",
				Resource:     "references/revenue-policy.md",
			}},
		}},
		Computations: []ComputationMigration{{
			Path: "computations/profit.md",
			Contract: AttestedComputationContract{
				Mode:            AttestedComputationModeFile,
				Runtime:         "dbt",
				Parameters:      []ComputationParameter{{Name: "year", Type: "integer", Required: true}},
				ComputationPath: "references/computations/profit.sql",
				Executor:        ExecutorContract{Resource: "references/run-dbt.md", Receipt: []string{"run_id", "compiled_sql", "result"}},
				Attester:        AttesterContract{Resource: "references/attest-dbt.py"},
			},
			Asset: &MigrationAsset{Path: "references/computations/profit.sql", Content: []byte("select gross_profit from profit")},
		}},
	}
	migration, err := NewMigrateV01ToV02(migrationInput)
	if err != nil {
		t.Fatal(err)
	}
	change := ChangeSet{
		Version:      ChangeSetFormatVersion,
		ID:           "v02-deep-copy",
		Actor:        "transaction-principal",
		BaseRevision: testRevision(t),
		Operations: []Operation{
			SetGenerated{Concept: concept, Generated: Generation{By: "reference_agent/gemini-2.5-pro", At: "2026-06-20T22:53:05Z"}},
			EnsureVerification{Concept: concept, Verification: Verification{By: "human:reviewer", At: "2026-06-25T09:00:00Z"}},
			source,
			lifecycle,
			computation,
			SetBundleVersion{Version: "0.2"},
			migration,
		},
	}
	before, err := change.RequestDigest()
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	usageCount = 1
	sourceWindow.From = "1999-01-01"
	status = "deprecated"
	staleAfter = "1999-01-01"
	parameters[0].Name = "mutated"
	receipt[0] = "mutated"
	migrationInput.GeneratedAt[0].At = "1999-01-01T00:00:00Z"
	migrationInput.Citations[0].Entries[0].SourceID = "mutated"
	migrationInput.Computations[0].Contract.Parameters[0].Name = "mutated"
	migrationInput.Computations[0].Asset.Content[0] = 'X'
	sourceCopy := source.Source()
	*sourceCopy.UsageCount = 2
	sourceCopy.UsageWindow.From = "2000-01-01"
	lifecycleCopy := lifecycle.Lifecycle()
	*lifecycleCopy.Status = "draft"
	contractCopy := computation.Contract()
	contractCopy.Parameters[0].Name = "getter-mutated"
	contractCopy.Executor.Receipt[0] = "getter-mutated"
	migrationCopy := migration.Migration()
	migrationCopy.GeneratedAt[0].At = "2000-01-01T00:00:00Z"
	migrationCopy.Computations[0].Contract.Executor.Receipt[0] = "getter-mutated"
	migrationCopy.Computations[0].Asset.Content[0] = 'Y'
	after, afterErr := change.RequestDigest()

	// Assert.
	if afterErr != nil {
		t.Fatal(afterErr)
	}
	if before != after {
		t.Fatalf("caller mutation changed request digest: before=%s after=%s", before, after)
	}
	if before != "sha256:59c6c97ef905df07fc971d072a548547780e7d51355770a8bce3fc97791557c5" {
		t.Fatalf("RequestDigest() = %s; update only for an intentional canonical contract change", before)
	}
}

func TestSourceSelectorOperations_DeepCopyInputsGettersAndDigest(t *testing.T) {
	// Arrange.
	concept := testRef(t, "metrics/revenue", "").ID
	count := ^uint64(0)
	window := UsageWindow{From: "2026-01-01", To: "2026-01-31"}
	exactInput := ProvenanceSource{
		Resource: "policy.md", UsageCount: &count, UsageWindow: &window,
	}
	selector, err := SourceByExact(exactInput)
	if err != nil {
		t.Fatal(err)
	}
	remove, err := NewRemoveSource(concept, selector)
	if err != nil {
		t.Fatal(err)
	}
	setWindow, err := NewSetUsageWindow(concept, &selector, &window)
	if err != nil {
		t.Fatal(err)
	}
	change := ChangeSet{
		Version:      ChangeSetFormatVersion,
		ID:           "source-selector-deep-copy",
		Actor:        "principal",
		BaseRevision: testRevision(t),
		Operations:   []Operation{remove, setWindow},
	}
	before, err := change.RequestDigest()
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	count = 1
	window.From = "1999-01-01"
	selector.exact.Resource = "mutated.md"
	*selector.exact.UsageCount = 2
	removeCopy := remove.Selector()
	removeCopy.exact.Resource = "getter-mutated.md"
	setSelectorCopy, ok := setWindow.Selector()
	if !ok {
		t.Fatal("SetUsageWindow selector is absent")
	}
	setSelectorCopy.exact.Resource = "getter-mutated.md"
	windowCopy, ok := setWindow.Window()
	if !ok {
		t.Fatal("SetUsageWindow window is absent")
	}
	windowCopy.From = "2000-01-01"
	after, afterErr := change.RequestDigest()

	// Assert.
	if afterErr != nil {
		t.Fatal(afterErr)
	}
	if before != after {
		t.Fatalf("caller/getter mutation changed digest: before=%s after=%s", before, after)
	}
	removeExact, _ := remove.Selector().Exact()
	setSelector, _ := setWindow.Selector()
	setExact, _ := setSelector.Exact()
	setValue, _ := setWindow.Window()
	if removeExact.Resource != "policy.md" || setExact.Resource != "policy.md" ||
		*removeExact.UsageCount != ^uint64(0) || *setExact.UsageCount != ^uint64(0) ||
		setValue.From != "2026-01-01" {
		t.Fatalf("defensive values changed: remove=%#v set=%#v window=%#v", removeExact, setExact, setValue)
	}
}

func TestV02CanonicalContract_SerializesEveryAttestedFieldAndListOrder(t *testing.T) {
	// Arrange.
	concept := testRef(t, "computations/revenue", "").ID
	base := AttestedComputationContract{
		Mode:              AttestedComputationModeInline,
		Runtime:           "bigquery",
		Parameters:        []ComputationParameter{{Name: "year", Type: "integer", Required: true}, {Name: "region", Type: "string"}},
		InlineComputation: "select @year, @region",
		InlineLanguage:    "sql",
		Executor:          ExecutorContract{Resource: "references/run.md", Receipt: []string{"job_id", "result"}},
		Attester:          AttesterContract{Resource: "references/attest.py"},
	}
	variants := []AttestedComputationContract{
		func() AttestedComputationContract {
			value := cloneAttestedComputationContract(base)
			value.Runtime = "postgres"
			return value
		}(),
		func() AttestedComputationContract {
			value := cloneAttestedComputationContract(base)
			value.Parameters[0].Name = "fiscal_year"
			return value
		}(),
		func() AttestedComputationContract {
			value := cloneAttestedComputationContract(base)
			value.Parameters[0].Type = "number"
			return value
		}(),
		func() AttestedComputationContract {
			value := cloneAttestedComputationContract(base)
			value.Parameters[0].Required = false
			return value
		}(),
		func() AttestedComputationContract {
			value := cloneAttestedComputationContract(base)
			value.Parameters[0], value.Parameters[1] = value.Parameters[1], value.Parameters[0]
			return value
		}(),
		func() AttestedComputationContract {
			value := cloneAttestedComputationContract(base)
			value.InlineComputation = "select @region, @year"
			return value
		}(),
		func() AttestedComputationContract {
			value := cloneAttestedComputationContract(base)
			value.InlineLanguage = "postgresql"
			return value
		}(),
		func() AttestedComputationContract {
			value := cloneAttestedComputationContract(base)
			value.Executor.Resource = "references/run-v2.md"
			return value
		}(),
		func() AttestedComputationContract {
			value := cloneAttestedComputationContract(base)
			value.Executor.Receipt[0] = "run_id"
			return value
		}(),
		func() AttestedComputationContract {
			value := cloneAttestedComputationContract(base)
			value.Executor.Receipt[0], value.Executor.Receipt[1] = value.Executor.Receipt[1], value.Executor.Receipt[0]
			return value
		}(),
		func() AttestedComputationContract {
			value := cloneAttestedComputationContract(base)
			value.Attester.Resource = "references/attest-v2.py"
			return value
		}(),
		func() AttestedComputationContract {
			value := cloneAttestedComputationContract(base)
			value.InlineComputation = ""
			value.InlineLanguage = ""
			value.ComputationPath = "references/revenue.sql"
			value.Mode = AttestedComputationModeFile
			return value
		}(),
	}
	baseOperation, err := NewPutAttestedComputation(concept, base)
	if err != nil {
		t.Fatal(err)
	}
	baseDigest := canonicalOperationDigest(t, baseOperation)

	// Act / Assert.
	seen := map[string]struct{}{baseDigest: {}}
	for index, variant := range variants {
		operation, err := NewPutAttestedComputation(concept, variant)
		if err != nil {
			t.Fatalf("variant %d: %v", index, err)
		}
		digest := canonicalOperationDigest(t, operation)
		if _, collision := seen[digest]; collision {
			t.Fatalf("variant %d canonical digest collision: %s", index, digest)
		}
		seen[digest] = struct{}{}
	}
}

func TestV02CanonicalContract_SerializesEveryOperationField(t *testing.T) {
	// Arrange.
	concept := testRef(t, "a", "").ID
	otherConcept := testRef(t, "b", "").ID
	verification := Verification{By: "human:reviewer", At: "2026-01-01T00:00:00Z"}
	count1, count2 := uint64(1), uint64(2)
	window := UsageWindow{From: "2026-01-01", To: "2026-01-31"}
	source := ProvenanceSource{
		ID: "policy", Resource: "policy.md", Title: "Policy", Author: "team:finance",
		UsageCount: &count1, LastModified: "2025-12-31", UsageWindow: &window,
	}
	sourceVariants := []ProvenanceSource{
		{ID: "policy-v2", Resource: source.Resource, Title: source.Title, Author: source.Author, UsageCount: &count1, LastModified: source.LastModified, UsageWindow: &window},
		{ID: source.ID, Resource: "policy-v2.md", Title: source.Title, Author: source.Author, UsageCount: &count1, LastModified: source.LastModified, UsageWindow: &window},
		{ID: source.ID, Resource: source.Resource, Title: "Policy v2", Author: source.Author, UsageCount: &count1, LastModified: source.LastModified, UsageWindow: &window},
		{ID: source.ID, Resource: source.Resource, Title: source.Title, Author: "team:risk", UsageCount: &count1, LastModified: source.LastModified, UsageWindow: &window},
		{ID: source.ID, Resource: source.Resource, Title: source.Title, Author: source.Author, UsageCount: &count2, LastModified: source.LastModified, UsageWindow: &window},
		{ID: source.ID, Resource: source.Resource, Title: source.Title, Author: source.Author, LastModified: source.LastModified, UsageWindow: &window},
		{ID: source.ID, Resource: source.Resource, Title: source.Title, Author: source.Author, UsageCount: &count1, LastModified: "2025-12-30", UsageWindow: &window},
		{ID: source.ID, Resource: source.Resource, Title: source.Title, Author: source.Author, UsageCount: &count1, LastModified: source.LastModified, UsageWindow: &UsageWindow{From: "2026-01-02", To: "2026-01-31"}},
		{ID: source.ID, Resource: source.Resource, Title: source.Title, Author: source.Author, UsageCount: &count1, LastModified: source.LastModified, UsageWindow: &UsageWindow{From: "2026-01-01", To: "2026-02-01"}},
		{ID: source.ID, Resource: source.Resource, Title: source.Title, Author: source.Author, UsageCount: &count1, LastModified: source.LastModified},
	}
	putSourceBase := mustPutSource(t, concept, source)
	putSourceVariants := []Operation{mustPutSource(t, otherConcept, source)}
	for _, variant := range sourceVariants {
		putSourceVariants = append(putSourceVariants, mustPutSource(t, concept, variant))
	}
	idSelector, err := SourceByID("policy")
	if err != nil {
		t.Fatal(err)
	}
	otherIDSelector, err := SourceByID("policy-v2")
	if err != nil {
		t.Fatal(err)
	}
	exactSelector, err := SourceByExact(ProvenanceSource{Resource: "policy.md"})
	if err != nil {
		t.Fatal(err)
	}
	removeSourceBase := mustRemoveSource(t, concept, idSelector)
	usageBase := mustSetUsageWindow(t, concept, &idSelector, &window)
	statusStable, statusDraft := "stable", "draft"
	stale := "2026-12-31"
	lifecycleBase := mustSetLifecycle(t, concept, Lifecycle{Status: &statusStable, StaleAfter: &stale})
	computationContract := AttestedComputationContract{
		Mode: AttestedComputationModeInline, Runtime: "postgres", Parameters: []ComputationParameter{{Name: "year", Type: "integer", Required: false}},
		InlineComputation: "select @year", InlineLanguage: "sql",
		Executor: ExecutorContract{Resource: "run.md", Receipt: []string{"result"}},
		Attester: AttesterContract{Resource: "attest.py"},
	}
	tests := []struct {
		name     string
		base     Operation
		variants []Operation
	}{
		{
			name: "SetGenerated",
			base: SetGenerated{Concept: concept, Generated: Generation{By: "process:generator", At: "2026-01-01T00:00:00Z"}},
			variants: []Operation{
				SetGenerated{Concept: otherConcept, Generated: Generation{By: "process:generator", At: "2026-01-01T00:00:00Z"}},
				SetGenerated{Concept: concept, Generated: Generation{By: "process:generator-v2", At: "2026-01-01T00:00:00Z"}},
				SetGenerated{Concept: concept, Generated: Generation{By: "process:generator", At: "2026-01-02T00:00:00Z"}},
				SetGenerated{Concept: concept, Generated: Generation{By: "process:generator"}},
			},
		},
		{
			name: "EnsureVerification",
			base: EnsureVerification{Concept: concept, Verification: verification},
			variants: []Operation{
				EnsureVerification{Concept: otherConcept, Verification: verification},
				EnsureVerification{Concept: concept, Verification: Verification{By: "human:other", At: verification.At}},
				EnsureVerification{Concept: concept, Verification: Verification{By: verification.By, At: "2026-01-02T00:00:00Z"}},
			},
		},
		{
			name: "RemoveVerification",
			base: RemoveVerification{Concept: concept, Verification: verification},
			variants: []Operation{
				RemoveVerification{Concept: otherConcept, Verification: verification},
				RemoveVerification{Concept: concept, Verification: Verification{By: "human:other", At: verification.At}},
				RemoveVerification{Concept: concept, Verification: Verification{By: verification.By, At: "2026-01-02T00:00:00Z"}},
			},
		},
		{name: "PutSource", base: putSourceBase, variants: putSourceVariants},
		{
			name: "RemoveSource",
			base: removeSourceBase,
			variants: []Operation{
				mustRemoveSource(t, otherConcept, idSelector),
				mustRemoveSource(t, concept, otherIDSelector),
				mustRemoveSource(t, concept, exactSelector),
			},
		},
		{
			name: "SetUsageWindow",
			base: usageBase,
			variants: []Operation{
				mustSetUsageWindow(t, otherConcept, &idSelector, &window),
				mustSetUsageWindow(t, concept, nil, &window),
				mustSetUsageWindow(t, concept, &exactSelector, &window),
				mustSetUsageWindow(t, concept, &idSelector, nil),
				mustSetUsageWindow(t, concept, &idSelector, &UsageWindow{From: "2026-01-02", To: window.To}),
				mustSetUsageWindow(t, concept, &idSelector, &UsageWindow{From: window.From, To: "2026-02-01"}),
			},
		},
		{
			name: "SetLifecycle",
			base: lifecycleBase,
			variants: []Operation{
				mustSetLifecycle(t, otherConcept, Lifecycle{Status: &statusStable, StaleAfter: &stale}),
				mustSetLifecycle(t, concept, Lifecycle{Status: &statusDraft, StaleAfter: &stale}),
				mustSetLifecycle(t, concept, Lifecycle{StaleAfter: &stale}),
				mustSetLifecycle(t, concept, Lifecycle{Status: &statusStable, StaleAfter: stringPointer("2026-12-30")}),
				mustSetLifecycle(t, concept, Lifecycle{Status: &statusStable}),
			},
		},
		{
			name: "PutAttestedComputation concept",
			base: mustPutAttestedComputation(t, concept, computationContract),
			variants: []Operation{
				mustPutAttestedComputation(t, otherConcept, computationContract),
			},
		},
		{
			name:     "SetBundleVersion",
			base:     SetBundleVersion{Version: "0.2"},
			variants: []Operation{SetBundleVersion{Version: "0.3"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Act.
			seen := map[string]struct{}{canonicalOperationDigest(t, test.base): {}}
			for index, variant := range test.variants {
				digest := canonicalOperationDigest(t, variant)
				if _, collision := seen[digest]; collision {
					t.Fatalf("variant %d canonical digest collision: %s", index, digest)
				}
				seen[digest] = struct{}{}
			}

			// Assert.
			if len(seen) != len(test.variants)+1 {
				t.Fatalf("distinct canonical digests = %d, want %d", len(seen), len(test.variants)+1)
			}
		})
	}
}

func TestV02CanonicalContract_UsesDistinctAdditiveTagsWithoutFormatBump(t *testing.T) {
	// Arrange.
	concept := testRef(t, "a", "").ID
	source, err := NewPutSource(concept, ProvenanceSource{ID: "policy", Resource: "policy.md"})
	if err != nil {
		t.Fatal(err)
	}
	selector, err := SourceByID("policy")
	if err != nil {
		t.Fatal(err)
	}
	removeSource, err := NewRemoveSource(concept, selector)
	if err != nil {
		t.Fatal(err)
	}
	window, err := NewSetUsageWindow(concept, nil, &UsageWindow{From: "2026-01-01", To: "2026-01-31"})
	if err != nil {
		t.Fatal(err)
	}
	status := "stable"
	lifecycle, err := NewSetLifecycle(concept, Lifecycle{Status: &status})
	if err != nil {
		t.Fatal(err)
	}
	computation, err := NewPutAttestedComputation(concept, AttestedComputationContract{
		Mode:              AttestedComputationModeInline,
		Runtime:           "postgres",
		InlineComputation: "select 1",
		Executor:          ExecutorContract{Resource: "run.md", Receipt: []string{"result"}},
		Attester:          AttesterContract{Resource: "attest.py"},
	})
	if err != nil {
		t.Fatal(err)
	}
	migration, err := NewMigrateV01ToV02(V01ToV02Migration{
		GeneratedBy:       "process:migration",
		TimestampPolicy:   LegacyTimestampPreserve,
		TimestampConflict: TimestampConflictReject,
	})
	if err != nil {
		t.Fatal(err)
	}
	change := ChangeSet{
		Version:      ChangeSetFormatVersion,
		ID:           "v02-tags",
		Actor:        "principal",
		BaseRevision: testRevision(t),
		Operations: []Operation{
			SetGenerated{Concept: concept, Generated: Generation{By: "process:generator"}},
			EnsureVerification{Concept: concept, Verification: Verification{By: "human:reviewer", At: "2026-01-01T00:00:00Z"}},
			RemoveVerification{Concept: concept, Verification: Verification{By: "human:reviewer", At: "2026-01-01T00:00:00Z"}},
			source,
			removeSource,
			window,
			lifecycle,
			computation,
			SetBundleVersion{Version: "0.2"},
			migration,
		},
	}
	tags := []string{
		"set_generated",
		"ensure_verification",
		"remove_verification",
		"put_source",
		"remove_source",
		"set_usage_window",
		"set_lifecycle",
		"put_attested_computation",
		"set_bundle_version",
		"migrate_v01_to_v02",
	}

	// Act.
	canonical, canonicalErr := change.CanonicalBytes()

	// Assert.
	if canonicalErr != nil {
		t.Fatal(canonicalErr)
	}
	if change.Version != 1 || ChangeSetFormatVersion != 1 {
		t.Fatalf("additive operations changed ChangeSet format: change=%d format=%d", change.Version, ChangeSetFormatVersion)
	}
	for _, tag := range tags {
		if count := bytes.Count(canonical, []byte(tag)); count != 1 {
			t.Fatalf("canonical tag %q count = %d in %x", tag, count, canonical)
		}
	}
}

func TestV02SourceSelector_ConstructorsAreClosedAndDefensive(t *testing.T) {
	// Arrange.
	count := uint64(7)
	window := UsageWindow{From: "2026-01-01", To: "2026-01-31"}
	input := ProvenanceSource{
		Resource:    "all queries in project finance",
		UsageCount:  &count,
		UsageWindow: &window,
	}

	// Act.
	exact, err := SourceByExact(input)
	count = 8
	window.From = "1999-01-01"
	got, ok := exact.Exact()
	invalidID, invalidIDErr := SourceByID("")
	identifiedExact := input
	identifiedExact.ID = "not-anonymous"
	invalidExact, invalidExactErr := SourceByExact(identifiedExact)

	// Assert.
	if err != nil || !ok {
		t.Fatalf("SourceByExact() = %#v, %v", exact, err)
	}
	if *got.UsageCount != 7 || got.UsageWindow.From != "2026-01-01" {
		t.Fatalf("selector retained caller pointers: %#v", got)
	}
	if invalidID != (SourceSelector{}) || !errors.Is(invalidIDErr, ErrInvalidChangeSet) {
		t.Fatalf("SourceByID(\"\") = %#v, %v", invalidID, invalidIDErr)
	}
	if invalidExact != (SourceSelector{}) || !errors.Is(invalidExactErr, ErrInvalidChangeSet) {
		t.Fatalf("SourceByExact(identified) = %#v, %v", invalidExact, invalidExactErr)
	}
}

func TestV02OperationPreflight_RejectsInvalidDesiredState(t *testing.T) {
	concept := testRef(t, "metrics/revenue", "").ID
	tests := []struct {
		name string
		act  func() error
	}{
		{
			name: "generated actor convention",
			act: func() error {
				return (SetGenerated{Concept: concept, Generated: Generation{By: "transaction-principal"}}).validate()
			},
		},
		{
			name: "verification date",
			act: func() error {
				return (EnsureVerification{Concept: concept, Verification: Verification{By: "human:reviewer", At: "tomorrow"}}).validate()
			},
		},
		{
			name: "reversed usage window",
			act: func() error {
				_, err := NewSetUsageWindow(concept, nil, &UsageWindow{From: "2026-02-01", To: "2026-01-01"})
				return err
			},
		},
		{
			name: "invalid lifecycle",
			act: func() error {
				status := "archived"
				_, err := NewSetLifecycle(concept, Lifecycle{Status: &status})
				return err
			},
		},
		{
			name: "duplicate parameters",
			act: func() error {
				_, err := NewPutAttestedComputation(concept, AttestedComputationContract{
					Mode:              AttestedComputationModeInline,
					Runtime:           "postgres",
					Parameters:        []ComputationParameter{{Name: "year", Type: "integer"}, {Name: "year", Type: "string"}},
					InlineComputation: "select 1",
					Executor:          ExecutorContract{Resource: "run.md", Receipt: []string{"result"}},
					Attester:          AttesterContract{Resource: "attest.py"},
				})
				return err
			},
		},
		{
			name: "both computation modes",
			act: func() error {
				_, err := NewPutAttestedComputation(concept, AttestedComputationContract{
					Mode:              AttestedComputationModeInline,
					Runtime:           "postgres",
					ComputationPath:   "query.sql",
					InlineComputation: "select 1",
					Executor:          ExecutorContract{Resource: "run.md", Receipt: []string{"result"}},
					Attester:          AttesterContract{Resource: "attest.py"},
				})
				return err
			},
		},
		{
			name: "duplicate receipt fields",
			act: func() error {
				_, err := NewPutAttestedComputation(concept, AttestedComputationContract{
					Mode:              AttestedComputationModeInline,
					Runtime:           "postgres",
					InlineComputation: "select 1",
					Executor:          ExecutorContract{Resource: "run.md", Receipt: []string{"result", "result"}},
					Attester:          AttesterContract{Resource: "attest.py"},
				})
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange / Act.
			err := test.act()

			// Assert.
			if !errors.Is(err, ErrInvalidChangeSet) {
				t.Fatalf("preflight error = %v, want ErrInvalidChangeSet", err)
			}
		})
	}
}

func TestDocumentActorPreflight_UsesSharedV02Predicate(t *testing.T) {
	valid256 := "human:" + strings.Repeat("a", 250)
	invalid257 := valid256 + "b"
	tests := []struct {
		name  string
		actor string
		valid bool
	}{
		{name: "human", actor: "human:reviewer", valid: true},
		{name: "process", actor: "process:nightly", valid: true},
		{name: "producer version", actor: "producer/v1", valid: true},
		{name: "version colon", actor: "producer/v:1", valid: true},
		{name: "namespaced extra colon", actor: "human:team:reviewer", valid: true},
		{name: "producer colon", actor: "producer:name/v1", valid: true},
		{name: "extra slash", actor: "producer/v1/build"},
		{name: "ascii whitespace", actor: "human:review er", valid: true},
		{name: "unicode whitespace", actor: "human:review\u00a0er", valid: true},
		{name: "leading whitespace", actor: " human:reviewer"},
		{name: "trailing whitespace", actor: "human:reviewer "},
		{name: "control", actor: "human:review\x01er"},
		{name: "delete", actor: "human:review\x7fer"},
		{name: "invalid utf8", actor: string([]byte{'h', 'u', 'm', 'a', 'n', ':', 0xff})},
		{name: "maximum bytes", actor: valid256, valid: true},
		{name: "over former transport maximum", actor: invalid257, valid: true},
	}
	concept := testRef(t, "a", "").ID

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			operation := SetGenerated{Concept: concept, Generated: Generation{By: test.actor}}

			// Act.
			shared := bundle.ValidActor(test.actor)
			err := operation.validate()

			// Assert.
			if shared != test.valid {
				t.Fatalf("bundle.ValidActor(%q) = %t, want %t", test.actor, shared, test.valid)
			}
			if (err == nil) != test.valid {
				t.Fatalf("SetGenerated.validate() error = %v, valid=%t", err, test.valid)
			}
		})
	}
}

func TestChangeSetClosedUnions_AcceptValuesAndRejectPointersWithoutPanic(t *testing.T) {
	// Arrange.
	concept := testRef(t, "a", "").ID
	target := testRef(t, "b", "").ID
	relation := EnsureRelation{Source: bundleRef(concept), Type: "depends_on", Target: bundleRef(target)}
	move := MoveConcept{From: concept, To: testRef(t, "moved", "").ID}
	rename := RenameFragment{Concept: concept, From: "old", To: "new"}
	generated := SetGenerated{Concept: concept, Generated: Generation{By: "process:generator"}}
	ensureVerification := EnsureVerification{Concept: concept, Verification: Verification{By: "human:reviewer", At: "2026-01-01T00:00:00Z"}}
	removeVerification := RemoveVerification{Concept: concept, Verification: ensureVerification.Verification}
	putSource, err := NewPutSource(concept, ProvenanceSource{ID: "policy", Resource: "policy.md"})
	if err != nil {
		t.Fatal(err)
	}
	selector, err := SourceByID("policy")
	if err != nil {
		t.Fatal(err)
	}
	removeSource, err := NewRemoveSource(concept, selector)
	if err != nil {
		t.Fatal(err)
	}
	setUsageWindow, err := NewSetUsageWindow(concept, nil, &UsageWindow{From: "2026-01-01", To: "2026-01-31"})
	if err != nil {
		t.Fatal(err)
	}
	status := "stable"
	setLifecycle, err := NewSetLifecycle(concept, Lifecycle{Status: &status})
	if err != nil {
		t.Fatal(err)
	}
	putComputation, err := NewPutAttestedComputation(concept, AttestedComputationContract{
		Mode:              AttestedComputationModeInline,
		Runtime:           "postgres",
		InlineComputation: "select 1",
		Executor:          ExecutorContract{Resource: "run.md", Receipt: []string{"result"}},
		Attester:          AttesterContract{Resource: "attest.py"},
	})
	if err != nil {
		t.Fatal(err)
	}
	setVersion := SetBundleVersion{Version: "0.2"}
	migrate, err := NewMigrateV01ToV02(V01ToV02Migration{
		GeneratedBy:       "process:migration",
		TimestampPolicy:   LegacyTimestampPreserve,
		TimestampConflict: TimestampConflictReject,
	})
	if err != nil {
		t.Fatal(err)
	}
	operations := []struct {
		name     string
		value    Operation
		pointer  Operation
		typedNil Operation
	}{
		{name: "EnsureRelation", value: relation, pointer: &relation, typedNil: (*EnsureRelation)(nil)},
		{name: "MoveConcept", value: move, pointer: &move, typedNil: (*MoveConcept)(nil)},
		{name: "RenameFragment", value: rename, pointer: &rename, typedNil: (*RenameFragment)(nil)},
		{name: "SetGenerated", value: generated, pointer: &generated, typedNil: (*SetGenerated)(nil)},
		{name: "EnsureVerification", value: ensureVerification, pointer: &ensureVerification, typedNil: (*EnsureVerification)(nil)},
		{name: "RemoveVerification", value: removeVerification, pointer: &removeVerification, typedNil: (*RemoveVerification)(nil)},
		{name: "PutSource", value: putSource, pointer: &putSource, typedNil: (*PutSource)(nil)},
		{name: "RemoveSource", value: removeSource, pointer: &removeSource, typedNil: (*RemoveSource)(nil)},
		{name: "SetUsageWindow", value: setUsageWindow, pointer: &setUsageWindow, typedNil: (*SetUsageWindow)(nil)},
		{name: "SetLifecycle", value: setLifecycle, pointer: &setLifecycle, typedNil: (*SetLifecycle)(nil)},
		{name: "PutAttestedComputation", value: putComputation, pointer: &putComputation, typedNil: (*PutAttestedComputation)(nil)},
		{name: "SetBundleVersion", value: setVersion, pointer: &setVersion, typedNil: (*SetBundleVersion)(nil)},
		{name: "MigrateV01ToV02", value: migrate, pointer: &migrate, typedNil: (*MigrateV01ToV02)(nil)},
	}
	refExists := RefExists{Ref: bundleRef(concept)}
	refAbsent := RefAbsent{Ref: bundleRef(concept)}
	fileDigest := FileDigestEquals{Path: "a.md", Digest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	relationExists := RelationExists{Source: bundleRef(concept), Type: "depends_on", Target: bundleRef(target)}
	relationAbsent := RelationAbsent{Source: bundleRef(concept), Type: "depends_on", Target: bundleRef(target)}
	fragmentUnique := FragmentUnique{Ref: testRef(t, "a", "part")}
	revisionEquals := RevisionEquals{Revision: testRevision(t)}
	preconditions := []struct {
		name     string
		value    Precondition
		pointer  Precondition
		typedNil Precondition
	}{
		{name: "RefExists", value: refExists, pointer: &refExists, typedNil: (*RefExists)(nil)},
		{name: "RefAbsent", value: refAbsent, pointer: &refAbsent, typedNil: (*RefAbsent)(nil)},
		{name: "FileDigestEquals", value: fileDigest, pointer: &fileDigest, typedNil: (*FileDigestEquals)(nil)},
		{name: "RelationExists", value: relationExists, pointer: &relationExists, typedNil: (*RelationExists)(nil)},
		{name: "RelationAbsent", value: relationAbsent, pointer: &relationAbsent, typedNil: (*RelationAbsent)(nil)},
		{name: "FragmentUnique", value: fragmentUnique, pointer: &fragmentUnique, typedNil: (*FragmentUnique)(nil)},
		{name: "RevisionEquals", value: revisionEquals, pointer: &revisionEquals, typedNil: (*RevisionEquals)(nil)},
	}
	base := ChangeSet{
		Version:      ChangeSetFormatVersion,
		ID:           "closed-union",
		Actor:        "principal",
		BaseRevision: testRevision(t),
	}

	for _, operation := range operations {
		t.Run("operation value/"+operation.name, func(t *testing.T) {
			// Act.
			change := base
			change.Operations = []Operation{operation.value}
			_, canonicalErr := change.CanonicalBytes()

			// Assert.
			if canonicalErr != nil {
				t.Fatalf("value operation rejected: %v", canonicalErr)
			}
		})
		for _, rejected := range []struct {
			name  string
			value Operation
		}{
			{name: "pointer", value: operation.pointer},
			{name: "typed nil", value: operation.typedNil},
		} {
			t.Run("operation "+rejected.name+"/"+operation.name, func(t *testing.T) {
				// Act.
				change := base
				change.Operations = []Operation{rejected.value}
				validateErr := change.Validate()
				_, canonicalErr := change.CanonicalBytes()

				// Assert.
				if !errors.Is(validateErr, ErrInvalidChangeSet) || !errors.Is(canonicalErr, ErrInvalidChangeSet) {
					t.Fatalf("Validate/CanonicalBytes errors = %v, %v", validateErr, canonicalErr)
				}
			})
		}
	}
	for _, precondition := range preconditions {
		t.Run("precondition value/"+precondition.name, func(t *testing.T) {
			// Act.
			change := base
			change.Operations = []Operation{generated}
			change.Preconditions = []Precondition{precondition.value}
			_, canonicalErr := change.CanonicalBytes()

			// Assert.
			if canonicalErr != nil {
				t.Fatalf("value precondition rejected: %v", canonicalErr)
			}
		})
		for _, rejected := range []struct {
			name  string
			value Precondition
		}{
			{name: "pointer", value: precondition.pointer},
			{name: "typed nil", value: precondition.typedNil},
		} {
			t.Run("precondition "+rejected.name+"/"+precondition.name, func(t *testing.T) {
				// Act.
				change := base
				change.Operations = []Operation{generated}
				change.Preconditions = []Precondition{rejected.value}
				validateErr := change.Validate()
				_, canonicalErr := change.CanonicalBytes()

				// Assert.
				if !errors.Is(validateErr, ErrInvalidChangeSet) || !errors.Is(canonicalErr, ErrInvalidChangeSet) {
					t.Fatalf("Validate/CanonicalBytes errors = %v, %v", validateErr, canonicalErr)
				}
			})
		}
	}
}

func TestMigrateV01ToV02_CanonicalizesMapLikeInputOrder(t *testing.T) {
	// Arrange.
	left, err := NewMigrateV01ToV02(V01ToV02Migration{
		GeneratedBy:       "process:migration",
		TimestampPolicy:   LegacyTimestampPreserve,
		TimestampConflict: TimestampConflictReject,
		GeneratedAt: []MigrationGeneratedAt{
			{Path: "z.md", At: "2026-01-02T00:00:00Z"},
			{Path: "a.md", At: "2026-01-01T00:00:00Z"},
		},
		Citations: []DocumentCitationMigration{
			{Path: "z.md", Entries: []LegacyCitationMapping{{LegacyNumber: 2, LegacyEntry: "Second", SourceID: "second"}, {LegacyNumber: 1, LegacyEntry: "First", SourceID: "first"}}},
			{Path: "a.md", Entries: []LegacyCitationMapping{{LegacyEntry: "Bullet", SourceID: "bullet"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewMigrateV01ToV02(V01ToV02Migration{
		GeneratedBy:       "process:migration",
		TimestampPolicy:   LegacyTimestampPreserve,
		TimestampConflict: TimestampConflictReject,
		GeneratedAt: []MigrationGeneratedAt{
			{Path: "a.md", At: "2026-01-01T00:00:00Z"},
			{Path: "z.md", At: "2026-01-02T00:00:00Z"},
		},
		Citations: []DocumentCitationMigration{
			{Path: "a.md", Entries: []LegacyCitationMapping{{LegacyEntry: "Bullet", SourceID: "bullet"}}},
			{Path: "z.md", Entries: []LegacyCitationMapping{{LegacyNumber: 1, LegacyEntry: "First", SourceID: "first"}, {LegacyNumber: 2, LegacyEntry: "Second", SourceID: "second"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	base := ChangeSet{Version: ChangeSetFormatVersion, ID: "migration-order", Actor: "principal", BaseRevision: testRevision(t)}
	leftChange, rightChange := base, base
	leftChange.Operations = []Operation{left}
	rightChange.Operations = []Operation{right}

	// Act.
	leftDigest, leftErr := leftChange.RequestDigest()
	rightDigest, rightErr := rightChange.RequestDigest()

	// Assert.
	if leftErr != nil || rightErr != nil {
		t.Fatalf("RequestDigest() errors = %v, %v", leftErr, rightErr)
	}
	if leftDigest != rightDigest {
		t.Fatalf("canonical order changed digest: left=%s right=%s", leftDigest, rightDigest)
	}
}

func TestMigrateV01ToV02_ZeroByteAssetPreservesPresenceCanonicalIdentityAndOwnership(t *testing.T) {
	migrationInput := func(content []byte, includeAsset bool) V01ToV02Migration {
		computation := ComputationMigration{
			Path: "query.md",
			Contract: AttestedComputationContract{
				Mode:            AttestedComputationModeFile,
				Runtime:         "sql",
				ComputationPath: "references/query.sql",
				Executor: ExecutorContract{
					Resource: "executors/sql.md",
					Receipt:  []string{"rows"},
				},
				Attester: AttesterContract{Resource: "attesters/sql.md"},
			},
		}
		if includeAsset {
			computation.Asset = &MigrationAsset{
				Path:    "references/query.sql",
				Content: content,
			}
		}
		return V01ToV02Migration{
			TimestampPolicy:   LegacyTimestampPreserve,
			TimestampConflict: TimestampConflictReject,
			Computations:      []ComputationMigration{computation},
		}
	}

	// Arrange.
	input := migrationInput([]byte{}, true)
	operation, err := NewMigrateV01ToV02(input)
	if err != nil {
		t.Fatal(err)
	}
	change := ChangeSet{
		Version:      ChangeSetFormatVersion,
		ID:           "zero-byte-migration-asset",
		Actor:        "operator",
		BaseRevision: testRevision(t),
		Operations:   []Operation{operation},
	}
	before, err := change.RequestDigest()
	if err != nil {
		t.Fatal(err)
	}
	oneByteOperation, err := NewMigrateV01ToV02(migrationInput([]byte{0}, true))
	if err != nil {
		t.Fatal(err)
	}
	oneByteChange := change
	oneByteChange.Operations = []Operation{oneByteOperation}
	oneByteDigest, err := oneByteChange.RequestDigest()
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	input.Computations[0].Asset.Path = "references/mutated.sql"
	input.Computations[0].Asset.Content = []byte("mutated")
	getterCopy := operation.Migration()
	getterCopy.Computations[0].Asset = nil
	afterGetterMutation := operation.Migration()
	after, afterErr := change.RequestDigest()
	_, absentErr := NewMigrateV01ToV02(migrationInput(nil, false))

	// Assert.
	if afterErr != nil {
		t.Fatal(afterErr)
	}
	if afterGetterMutation.Computations[0].Asset == nil ||
		afterGetterMutation.Computations[0].Asset.Path != "references/query.sql" ||
		len(afterGetterMutation.Computations[0].Asset.Content) != 0 {
		t.Fatalf("zero-byte asset presence escaped defensive copy: %#v", afterGetterMutation)
	}
	if before != after {
		t.Fatalf("caller/getter mutation changed digest: before=%s after=%s", before, after)
	}
	if before == oneByteDigest {
		t.Fatalf("zero-byte and one-byte assets share canonical digest %s", before)
	}
	if !errors.Is(absentErr, ErrInvalidChangeSet) {
		t.Fatalf("absent file asset error = %v, want ErrInvalidChangeSet", absentErr)
	}
}

func TestMigrateV01ToV02_ValidatesAssetIdentityIndependentlyFromDocumentPath(t *testing.T) {
	fileComputation := func(computationPath, assetPath string) ComputationMigration {
		return ComputationMigration{
			Path: "nested/query.md",
			Contract: AttestedComputationContract{
				Mode:            AttestedComputationModeFile,
				Runtime:         "sql",
				ComputationPath: computationPath,
				Executor: ExecutorContract{
					Resource: "executors/sql.md",
					Receipt:  []string{"rows"},
				},
				Attester: AttesterContract{Resource: "attesters/sql.md"},
			},
			Asset: &MigrationAsset{
				Path:    assetPath,
				Content: []byte("SELECT 1;\n"),
			},
		}
	}
	migration := func(computation ComputationMigration) V01ToV02Migration {
		return V01ToV02Migration{
			TimestampPolicy:   LegacyTimestampPreserve,
			TimestampConflict: TimestampConflictReject,
			Computations:      []ComputationMigration{computation},
		}
	}

	for _, test := range []struct {
		name            string
		computationPath string
		assetPath       string
	}{
		{
			name:            "document relative path with suffix",
			computationPath: "../references/query.sql?mode=preview#fragment",
			assetPath:       "references/query.sql",
		},
		{
			name:            "bundle relative path with suffix",
			computationPath: "/references/query.sql#fragment",
			assetPath:       "references/query.sql",
		},
		{
			name:            "raw mismatch is deferred to bundle resolver",
			computationPath: "../references/query.sql",
			assetPath:       "references/other.sql",
		},
	} {
		t.Run("valid/"+test.name, func(t *testing.T) {
			// Arrange.
			input := migration(fileComputation(test.computationPath, test.assetPath))

			// Act.
			_, err := NewMigrateV01ToV02(input)

			// Assert.
			if err != nil {
				t.Fatalf("NewMigrateV01ToV02() error = %v", err)
			}
		})
	}

	for _, assetPath := range []string{
		"../references/query.sql",
		"/references/query.sql",
		"references/query.sql?mode=preview",
		"references/query.sql#fragment",
		"references/query.md",
	} {
		t.Run("invalid asset/"+assetPath, func(t *testing.T) {
			// Arrange.
			input := migration(fileComputation("../references/query.sql", assetPath))

			// Act.
			_, err := NewMigrateV01ToV02(input)

			// Assert.
			if !errors.Is(err, ErrInvalidChangeSet) {
				t.Fatalf("NewMigrateV01ToV02() error = %v, want ErrInvalidChangeSet", err)
			}
		})
	}
}

func TestMigrateV01ToV02_ValidatesDualCitationSelectorsAndPreservesRawBytes(t *testing.T) {
	valid := []struct {
		name    string
		entries []LegacyCitationMapping
	}{
		{
			name: "number only",
			entries: []LegacyCitationMapping{{
				LegacyNumber: 1, SourceID: "one",
			}},
		},
		{
			name: "entry only multiline CRLF",
			entries: []LegacyCitationMapping{{
				LegacyEntry: "https://example.test\r\n  continued raw text", SourceID: "one",
			}},
		},
		{
			name: "same raw disjoint numbered AND selectors",
			entries: []LegacyCitationMapping{
				{LegacyNumber: 2, LegacyEntry: "same", SourceID: "two"},
				{LegacyNumber: 1, LegacyEntry: "same", SourceID: "one"},
			},
		},
	}
	for _, test := range valid {
		t.Run("valid/"+test.name, func(t *testing.T) {
			// Act.
			operation, err := NewMigrateV01ToV02(V01ToV02Migration{
				TimestampPolicy:   LegacyTimestampPreserve,
				TimestampConflict: TimestampConflictReject,
				Citations: []DocumentCitationMigration{{
					Path: "a.md", Entries: test.entries,
				}},
			})

			// Assert.
			if err != nil {
				t.Fatalf("NewMigrateV01ToV02() error = %v", err)
			}
			if test.name == "entry only multiline CRLF" &&
				operation.Migration().Citations[0].Entries[0].LegacyEntry != test.entries[0].LegacyEntry {
				t.Fatal("raw legacy entry bytes were normalized")
			}
		})
	}
	boundary := strings.Repeat("a", 4096)
	if _, err := NewMigrateV01ToV02(V01ToV02Migration{
		TimestampPolicy: LegacyTimestampPreserve, TimestampConflict: TimestampConflictReject,
		Citations: []DocumentCitationMigration{{
			Path: "a.md", Entries: []LegacyCitationMapping{{LegacyEntry: boundary, SourceID: "one"}},
		}},
	}); err != nil {
		t.Fatalf("4096-byte legacy entry error = %v", err)
	}

	invalid := []struct {
		name    string
		entries []LegacyCitationMapping
	}{
		{
			name:    "missing selector",
			entries: []LegacyCitationMapping{{SourceID: "one"}},
		},
		{
			name: "duplicate nonzero number",
			entries: []LegacyCitationMapping{
				{LegacyNumber: 1, LegacyEntry: "one", SourceID: "one"},
				{LegacyNumber: 1, LegacyEntry: "other", SourceID: "two"},
			},
		},
		{
			name: "entry only overlaps numbered same raw",
			entries: []LegacyCitationMapping{
				{LegacyEntry: "same", SourceID: "one"},
				{LegacyNumber: 2, LegacyEntry: "same", SourceID: "two"},
			},
		},
		{
			name: "duplicate entry only",
			entries: []LegacyCitationMapping{
				{LegacyEntry: "same", SourceID: "one"},
				{LegacyEntry: "same", SourceID: "two"},
			},
		},
		{
			name:    "blank raw entry",
			entries: []LegacyCitationMapping{{LegacyEntry: " \r\n\t", SourceID: "one"}},
		},
		{
			name:    "NUL raw entry",
			entries: []LegacyCitationMapping{{LegacyEntry: "raw\x00entry", SourceID: "one"}},
		},
		{
			name:    "invalid UTF-8 raw entry",
			entries: []LegacyCitationMapping{{LegacyEntry: string([]byte{0xff}), SourceID: "one"}},
		},
		{
			name:    "4097-byte raw entry",
			entries: []LegacyCitationMapping{{LegacyEntry: boundary + "a", SourceID: "one"}},
		},
		{
			name:    "unsafe C0 raw entry",
			entries: []LegacyCitationMapping{{LegacyEntry: "raw\x01entry", SourceID: "one"}},
		},
		{
			name:    "DEL raw entry",
			entries: []LegacyCitationMapping{{LegacyEntry: "raw\x7fentry", SourceID: "one"}},
		},
	}
	for _, test := range invalid {
		t.Run("invalid/"+test.name, func(t *testing.T) {
			// Act.
			_, err := NewMigrateV01ToV02(V01ToV02Migration{
				TimestampPolicy:   LegacyTimestampPreserve,
				TimestampConflict: TimestampConflictReject,
				Citations: []DocumentCitationMigration{{
					Path: "a.md", Entries: test.entries,
				}},
			})

			// Assert.
			if !errors.Is(err, ErrInvalidChangeSet) {
				t.Fatalf("NewMigrateV01ToV02() error = %v, want ErrInvalidChangeSet", err)
			}
		})
	}

	left, leftErr := NewMigrateV01ToV02(V01ToV02Migration{
		TimestampPolicy: LegacyTimestampPreserve, TimestampConflict: TimestampConflictReject,
		Citations: []DocumentCitationMigration{{
			Path: "a.md", Entries: []LegacyCitationMapping{{LegacyEntry: "line\ncontinued", SourceID: "one"}},
		}},
	})
	right, rightErr := NewMigrateV01ToV02(V01ToV02Migration{
		TimestampPolicy: LegacyTimestampPreserve, TimestampConflict: TimestampConflictReject,
		Citations: []DocumentCitationMigration{{
			Path: "a.md", Entries: []LegacyCitationMapping{{LegacyEntry: "line\r\ncontinued", SourceID: "one"}},
		}},
	})
	if leftErr != nil || rightErr != nil {
		t.Fatalf("newline selector construction errors = %v, %v", leftErr, rightErr)
	}
	base := ChangeSet{
		Version: ChangeSetFormatVersion, ID: "raw-newline-digest", Actor: "principal", BaseRevision: testRevision(t),
	}
	leftChange, rightChange := base, base
	leftChange.Operations = []Operation{left}
	rightChange.Operations = []Operation{right}
	leftDigest, leftErr := leftChange.RequestDigest()
	rightDigest, rightErr := rightChange.RequestDigest()
	if leftErr != nil || rightErr != nil || leftDigest == rightDigest {
		t.Fatalf("LF/CRLF digests = %q/%q, errors %v/%v", leftDigest, rightDigest, leftErr, rightErr)
	}
}

func TestMigrateV01ToV02_AllowsEveryMarkdownCitationPathWithoutInventingGeneratedMetadata(t *testing.T) {
	tests := []string{"index.md", "log.md", "nested/index.md", "nested/log.md", "orphan.md"}
	for _, path := range tests {
		t.Run(path, func(t *testing.T) {
			// Arrange.
			migration := V01ToV02Migration{
				TimestampPolicy:   LegacyTimestampPreserve,
				TimestampConflict: TimestampConflictReject,
				Citations: []DocumentCitationMigration{{
					Path: path,
					Entries: []LegacyCitationMapping{{
						LegacyNumber: 1,
						LegacyEntry:  "https://example.test",
						SourceID:     "source",
					}},
				}},
			}

			// Act.
			_, err := NewMigrateV01ToV02(migration)

			// Assert.
			if err != nil {
				t.Fatalf("NewMigrateV01ToV02() error = %v", err)
			}
		})
	}

	// Computation boundaries remain concept-document-only.
	_, err := NewMigrateV01ToV02(V01ToV02Migration{
		TimestampPolicy:   LegacyTimestampPreserve,
		TimestampConflict: TimestampConflictReject,
		Computations: []ComputationMigration{{
			Path: "nested/log.md",
			Contract: AttestedComputationContract{
				Mode:              AttestedComputationModeInline,
				InlineComputation: "SELECT 1",
			},
		}},
	})
	if !errors.Is(err, ErrInvalidChangeSet) {
		t.Fatalf("reserved computation migration error = %v, want ErrInvalidChangeSet", err)
	}
}

func bundleRef(id bundle.ConceptID) bundle.RelationRef {
	return bundle.RelationRef{ID: id}
}

func mustPutSource(t *testing.T, concept bundle.ConceptID, source ProvenanceSource) PutSource {
	t.Helper()
	operation, err := NewPutSource(concept, source)
	if err != nil {
		t.Fatal(err)
	}
	return operation
}

func mustRemoveSource(t *testing.T, concept bundle.ConceptID, selector SourceSelector) RemoveSource {
	t.Helper()
	operation, err := NewRemoveSource(concept, selector)
	if err != nil {
		t.Fatal(err)
	}
	return operation
}

func mustSetUsageWindow(t *testing.T, concept bundle.ConceptID, selector *SourceSelector, window *UsageWindow) SetUsageWindow {
	t.Helper()
	operation, err := NewSetUsageWindow(concept, selector, window)
	if err != nil {
		t.Fatal(err)
	}
	return operation
}

func mustSetLifecycle(t *testing.T, concept bundle.ConceptID, lifecycle Lifecycle) SetLifecycle {
	t.Helper()
	operation, err := NewSetLifecycle(concept, lifecycle)
	if err != nil {
		t.Fatal(err)
	}
	return operation
}

func mustPutAttestedComputation(t *testing.T, concept bundle.ConceptID, contract AttestedComputationContract) PutAttestedComputation {
	t.Helper()
	operation, err := NewPutAttestedComputation(concept, contract)
	if err != nil {
		t.Fatal(err)
	}
	return operation
}

func stringPointer(value string) *string {
	return &value
}

func canonicalOperationDigest(t *testing.T, operation Operation) string {
	t.Helper()
	change := ChangeSet{
		Version:      ChangeSetFormatVersion,
		ID:           "canonical-operation",
		Actor:        "principal",
		BaseRevision: testRevision(t),
		Operations:   []Operation{operation},
	}
	digest, err := change.RequestDigest()
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
