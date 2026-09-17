package store

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/skosovsky/okf/bundle"
)

var (
	_ uint16 = ChangeSetFormatVersion
	_ uint16 = CommitReceiptFormatVersion
)

func TestPreviewDiagnostics_PreserveUnknownValidatorContractThroughCloneAndWire(t *testing.T) {
	// Arrange. Store treats validator codes as opaque identifiers so adding a
	// v0.2 validator rule does not require a store format migration.
	preview := Preview{Diagnostics: []Diagnostic{
		{
			Kind:     DiagnosticValidation,
			Severity: DiagnosticWarning,
			Code:     "generated_actor_invalid",
			File:     "generated.md",
			Message:  "generated.by must use an actor identifier",
		},
		{
			Kind:     DiagnosticValidation,
			Severity: DiagnosticInfo,
			Code:     "executor_receipt_field_unknown",
			File:     "computation.md",
			Message:  "executor.receipt contains an unknown field",
		},
		{
			Kind:         DiagnosticRelation,
			Severity:     DiagnosticError,
			Code:         "missing_or_ambiguous_target_fragment",
			File:         "relations.md",
			Message:      "target fragment does not exist or is ambiguous",
			RelationType: "depends_on",
			RawTarget:    "beta#missing",
			Refs:         []bundle.RelationRef{testRef(t, "alpha", "part")},
		},
	}}
	want := `[{"Kind":"validation","Severity":"warning","Code":"generated_actor_invalid","File":"generated.md","Message":"generated.by must use an actor identifier","Refs":[]},{"Kind":"validation","Severity":"info","Code":"executor_receipt_field_unknown","File":"computation.md","Message":"executor.receipt contains an unknown field","Refs":[]},{"Kind":"relation","Severity":"error","Code":"missing_or_ambiguous_target_fragment","File":"relations.md","Message":"target fragment does not exist or is ambiguous","RelationType":"depends_on","RawTarget":"beta#missing","Refs":["alpha#part"]}]`

	// Act.
	cloned := preview.Clone()
	preview.Diagnostics[0].Code = "mutated"
	preview.Diagnostics[0].File = "mutated.md"
	preview.Diagnostics[0].Severity = DiagnosticError
	preview.Diagnostics[0].Message = "mutated"
	preview.Diagnostics[2].Refs[0].Fragment = "mutated"
	raw, err := json.Marshal(cloned.Diagnostics)

	// Assert.
	if err != nil {
		t.Fatalf("Marshal(cloned diagnostics) error = %v", err)
	}
	if string(raw) != want {
		t.Fatalf("cloned diagnostics JSON = %s, want %s", raw, want)
	}
}

func TestStoreTransactionPublicContract_RemainsSpecAgnosticV1(t *testing.T) {
	// Arrange. Bundle spec versions are not inputs to this backend-neutral
	// transaction contract.
	wantChangeSetFields := []string{
		"Version uint16",
		"ID store.ChangeSetID",
		"Actor store.Actor",
		"BaseRevision store.Revision",
		"Operations []store.Operation",
		"Preconditions []store.Precondition",
	}
	wantReceiptFields := []string{
		"FormatVersion uint16",
		"ChangeSetID store.ChangeSetID",
		"IdempotencyKey store.IdempotencyKey",
		"RequestDigest string",
		"BaseRevision store.Revision",
		"ResultRevision store.Revision",
		"CommitTime time.Time",
		"ChangedRefs []bundle.RelationRef",
		"ChangedFiles []store.FileChange",
	}
	wantPreviewFields := []string{
		"BaseRevision store.Revision",
		"ResultRevision store.Revision",
		"Reads []store.Read",
		"Writes []store.Write",
		"Deletes []string",
		"Renames []store.Rename",
		"AffectedRefs []bundle.RelationRef",
		"ReverseImpact []bundle.RelationRef",
		"Plan []store.OperationPlan",
		"Diagnostics []store.Diagnostic",
	}
	wantDiagnosticFields := []string{
		"Kind store.DiagnosticKind",
		"Severity store.DiagnosticSeverity",
		"Code string",
		"File string",
		"Message string",
		"RelationType string",
		"RawTarget string",
		"Refs []bundle.RelationRef",
	}
	wantStoreMethods := []string{
		"Commit func(context.Context, store.ChangeSet, store.CommitOptions) (store.CommitReceipt, error)",
		"Preview func(context.Context, store.ChangeSet) (store.Preview, error)",
		"Snapshot func(context.Context) (store.Snapshot, error)",
	}
	wantSnapshotMethods := []string{
		"ListConcepts func() ([]bundle.ConceptID, error)",
		"OpenConcept func(bundle.ConceptID) (bundle.Concept, error)",
		"Paths func(context.Context) ([]string, error)",
		"ReadFile func(context.Context, string) ([]uint8, error)",
		"Revision func() store.Revision",
	}
	change := legacyGoldenChangeSet(t)

	// Act.
	changeSetFields := exportedFieldSignatures(reflect.TypeOf(ChangeSet{}))
	receiptFields := exportedFieldSignatures(reflect.TypeOf(CommitReceipt{}))
	previewFields := exportedFieldSignatures(reflect.TypeOf(Preview{}))
	diagnosticFields := exportedFieldSignatures(reflect.TypeOf(Diagnostic{}))
	storeMethods := publicMethodSignatures(reflect.TypeOf((*Store)(nil)).Elem())
	snapshotMethods := publicMethodSignatures(reflect.TypeOf((*Snapshot)(nil)).Elem())
	validationErr := change.Validate()

	// Assert.
	if ChangeSetFormatVersion != 1 || CommitReceiptFormatVersion != 1 {
		t.Fatalf("transaction versions = changeset:%d receipt:%d, want 1/1", ChangeSetFormatVersion, CommitReceiptFormatVersion)
	}
	if !reflect.DeepEqual(changeSetFields, wantChangeSetFields) {
		t.Fatalf("ChangeSet public fields = %#v, want %#v", changeSetFields, wantChangeSetFields)
	}
	if !reflect.DeepEqual(receiptFields, wantReceiptFields) {
		t.Fatalf("CommitReceipt public fields = %#v, want %#v", receiptFields, wantReceiptFields)
	}
	if !reflect.DeepEqual(previewFields, wantPreviewFields) {
		t.Fatalf("Preview public fields = %#v, want %#v", previewFields, wantPreviewFields)
	}
	if !reflect.DeepEqual(diagnosticFields, wantDiagnosticFields) {
		t.Fatalf("Diagnostic public fields = %#v, want %#v", diagnosticFields, wantDiagnosticFields)
	}
	if !reflect.DeepEqual(storeMethods, wantStoreMethods) {
		t.Fatalf("Store public methods = %#v, want %#v", storeMethods, wantStoreMethods)
	}
	if !reflect.DeepEqual(snapshotMethods, wantSnapshotMethods) {
		t.Fatalf("Snapshot public methods = %#v, want %#v", snapshotMethods, wantSnapshotMethods)
	}
	if validationErr != nil {
		t.Fatalf("ChangeSet.Validate() error = %v", validationErr)
	}
}

func TestChangeSetV1Envelope_AllowsAdditiveV02OperationUnion(t *testing.T) {
	// Arrange. Operation is a closed union which may grow additively inside the
	// unchanged v1 ChangeSet envelope. The full v0.2 canonical/deep-copy/digest
	// universe belongs to change_v02_test.go; this file freezes legacy tags and
	// only guards the store envelope boundary.
	change := ChangeSet{
		Version:      ChangeSetFormatVersion,
		ID:           "v02-operation-in-v1-envelope",
		Actor:        "legacy-automation",
		BaseRevision: testRevision(t),
		Operations:   []Operation{SetBundleVersion{Version: "0.2"}},
	}

	// Act.
	validationErr := change.Validate()
	canonical, canonicalErr := change.CanonicalBytes()
	digest, digestErr := change.RequestDigest()

	// Assert.
	if validationErr != nil || canonicalErr != nil || digestErr != nil {
		t.Fatalf("v0.2 operation in ChangeSet v1 errors = validate:%v canonical:%v digest:%v", validationErr, canonicalErr, digestErr)
	}
	if change.Version != 1 || !bytes.Contains(canonical, []byte("set_bundle_version")) {
		t.Fatalf("v0.2 operation escaped v1 envelope: version=%d canonical=%x", change.Version, canonical)
	}
	if len(digest) != len("sha256:")+64 || !strings.HasPrefix(digest, "sha256:") {
		t.Fatalf("RequestDigest() = %q, want canonical ChangeSet digest", digest)
	}
}

func TestActorValidate_AcceptsDocumentAndLegacyActorsWithoutPolicyDrift(t *testing.T) {
	// Arrange.
	tests := []struct {
		name  string
		actor Actor
		valid bool
	}{
		{name: "document human", actor: "human:sergey.kosovsky", valid: true},
		{name: "document process", actor: "process:okf-migration", valid: true},
		{name: "document producer version", actor: "okf-migrator/0.2.0", valid: true},
		{name: "legacy team", actor: "team:data-platform", valid: true},
		{name: "legacy automation", actor: "legacy-automation", valid: true},
		{name: "legacy email", actor: "agent@example.test", valid: true},
		{name: "unicode", actor: "Сергей Косовский / 開発", valid: true},
		{name: "legacy embedded C1 control", actor: "legacy\u0080actor", valid: true},
		{name: "maximum bytes", actor: Actor(strings.Repeat("a", 256)), valid: true},
		{name: "over maximum bytes", actor: Actor(strings.Repeat("a", 257))},
		{name: "empty", actor: ""},
		{name: "leading whitespace", actor: " leading"},
		{name: "trailing whitespace", actor: "trailing "},
		{name: "unicode leading whitespace", actor: "\u00a0leading"},
		{name: "tab control", actor: "embedded\ttab"},
		{name: "newline control", actor: "embedded\nnewline"},
		{name: "delete control", actor: "delete\x7fcontrol"},
		{name: "invalid UTF-8", actor: Actor(string([]byte{'b', 'a', 'd', 0xff}))},
	}
	for control := rune(0); control < 0x20; control++ {
		tests = append(tests, struct {
			name  string
			actor Actor
			valid bool
		}{
			name:  fmt.Sprintf("C0 U+%04X", control),
			actor: Actor("embedded" + string(control) + "control"),
		})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act.
			err := tt.actor.Validate()

			// Assert.
			if tt.valid && err != nil {
				t.Fatalf("Actor(%q).Validate() error = %v, want nil", tt.actor, err)
			}
			if !tt.valid && !errors.Is(err, ErrInvalidChangeSet) {
				t.Fatalf("Actor(%q).Validate() error = %v, want ErrInvalidChangeSet", tt.actor, err)
			}
		})
	}
}

func TestCommitReceiptJSON_RejectsExecutorArtifactShapes(t *testing.T) {
	// Arrange. executor.receipt is an Attested Computation declaration or
	// runtime artifact, not transaction durability evidence.
	revision := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	canonicalPrefix := `{"FormatVersion":1,"ChangeSetID":"change","IdempotencyKey":"retry","RequestDigest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","BaseRevision":"` + revision + `","ResultRevision":"` + revision + `","CommitTime":"2026-07-19T00:00:00Z","ChangedRefs":[],"ChangedFiles":[]`
	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "runtime artifact",
			raw:  `{"executor":{"resource":"executors/sql.md"},"receipt":{"rows":42,"digest":"sha256:result"},"verdict":"pass"}`,
		},
		{
			name: "transaction fields plus executor verdict",
			raw:  canonicalPrefix + `,"ExecutorReceipt":{"rows":42},"Verdict":"pass"}`,
		},
		{
			name: "transaction fields plus lowercase executor",
			raw:  canonicalPrefix + `,"executor":{"resource":"executors/sql.md"}}`,
		},
		{
			name: "transaction fields plus lowercase receipt",
			raw:  canonicalPrefix + `,"receipt":{"rows":42,"digest":"sha256:result"}}`,
		},
		{
			name: "transaction fields plus lowercase verdict",
			raw:  canonicalPrefix + `,"verdict":"pass"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act.
			var receipt CommitReceipt
			err := json.Unmarshal([]byte(tt.raw), &receipt)

			// Assert.
			if !errors.Is(err, ErrStorageCorrupt) {
				t.Fatalf("Unmarshal(executor-like JSON) error = %v, want ErrStorageCorrupt", err)
			}
		})
	}
}

func TestChangeSetCanonicalBytes_LegacyVariantGoldenMatrix(t *testing.T) {
	// Arrange. Only operations and preconditions that predate OKF v0.2 belong
	// in this matrix; additive v0.2 operation tags have their own owner/tests.
	source := testRef(t, "alpha", "part")
	target := testRef(t, "beta", "target")
	ensure := EnsureRelation{Source: source, Type: "depends_on", Target: target}
	revision := Revision("sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	tests := []struct {
		name            string
		operation       Operation
		precondition    Precondition
		canonicalBase64 string
		digest          string
	}{
		{
			name:            "operation ensure relation",
			operation:       ensure,
			canonicalBase64: "AAAAAAAAAA1va2Y6Y2hhbmdlc2V0AAAAAAAAAAEAAAAAAAAADWxlZ2FjeS1nb2xkZW4AAAAAAAAAEWxlZ2FjeS1hdXRvbWF0aW9uAAAAAAAAAEdzaGEyNTY6YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYQAAAAAAAAABAAAAAAAAAA9lbnN1cmVfcmVsYXRpb24AAAAAAAAABWFscGhhAAAAAAAAAARwYXJ0AAAAAAAAAApkZXBlbmRzX29uAAAAAAAAAARiZXRhAAAAAAAAAAZ0YXJnZXQAAAAAAAAAAA==",
			digest:          "sha256:052ebc015a8ffbb2c0c3423b2d0c30188103162b4c80598a8ceae8a6ef9d2c98",
		},
		{
			name:            "operation move concept",
			operation:       MoveConcept{From: testRef(t, "alpha", "").ID, To: testRef(t, "nested/beta", "").ID},
			canonicalBase64: "AAAAAAAAAA1va2Y6Y2hhbmdlc2V0AAAAAAAAAAEAAAAAAAAADWxlZ2FjeS1nb2xkZW4AAAAAAAAAEWxlZ2FjeS1hdXRvbWF0aW9uAAAAAAAAAEdzaGEyNTY6YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYQAAAAAAAAABAAAAAAAAAAxtb3ZlX2NvbmNlcHQAAAAAAAAABWFscGhhAAAAAAAAAAtuZXN0ZWQvYmV0YQAAAAAAAAAA",
			digest:          "sha256:2a05cf1c061f46d5fc078f6364211774b4a59b5e9fde4ac5e5e3be4293a77b1b",
		},
		{
			name:            "operation rename fragment",
			operation:       RenameFragment{Concept: testRef(t, "alpha", "").ID, From: "old", To: "new"},
			canonicalBase64: "AAAAAAAAAA1va2Y6Y2hhbmdlc2V0AAAAAAAAAAEAAAAAAAAADWxlZ2FjeS1nb2xkZW4AAAAAAAAAEWxlZ2FjeS1hdXRvbWF0aW9uAAAAAAAAAEdzaGEyNTY6YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYQAAAAAAAAABAAAAAAAAAA9yZW5hbWVfZnJhZ21lbnQAAAAAAAAABWFscGhhAAAAAAAAAANvbGQAAAAAAAAAA25ldwAAAAAAAAAA",
			digest:          "sha256:0475cae3d5161654c4c5719029196bf3a8e33f1dbffe68d4ace1201530ee85f4",
		},
		{
			name:            "precondition ref exists",
			operation:       ensure,
			precondition:    RefExists{Ref: source},
			canonicalBase64: "AAAAAAAAAA1va2Y6Y2hhbmdlc2V0AAAAAAAAAAEAAAAAAAAADWxlZ2FjeS1nb2xkZW4AAAAAAAAAEWxlZ2FjeS1hdXRvbWF0aW9uAAAAAAAAAEdzaGEyNTY6YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYQAAAAAAAAABAAAAAAAAAA9lbnN1cmVfcmVsYXRpb24AAAAAAAAABWFscGhhAAAAAAAAAARwYXJ0AAAAAAAAAApkZXBlbmRzX29uAAAAAAAAAARiZXRhAAAAAAAAAAZ0YXJnZXQAAAAAAAAAAQAAAAAAAAAKcmVmX2V4aXN0cwAAAAAAAAAFYWxwaGEAAAAAAAAABHBhcnQ=",
			digest:          "sha256:09db2ac441cbf77bc497772d3eb6a38af2349e3b95e7996861f5f74a626de597",
		},
		{
			name:            "precondition ref absent",
			operation:       ensure,
			precondition:    RefAbsent{Ref: target},
			canonicalBase64: "AAAAAAAAAA1va2Y6Y2hhbmdlc2V0AAAAAAAAAAEAAAAAAAAADWxlZ2FjeS1nb2xkZW4AAAAAAAAAEWxlZ2FjeS1hdXRvbWF0aW9uAAAAAAAAAEdzaGEyNTY6YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYQAAAAAAAAABAAAAAAAAAA9lbnN1cmVfcmVsYXRpb24AAAAAAAAABWFscGhhAAAAAAAAAARwYXJ0AAAAAAAAAApkZXBlbmRzX29uAAAAAAAAAARiZXRhAAAAAAAAAAZ0YXJnZXQAAAAAAAAAAQAAAAAAAAAKcmVmX2Fic2VudAAAAAAAAAAEYmV0YQAAAAAAAAAGdGFyZ2V0",
			digest:          "sha256:c41af31954eefb27123c5ec87c70658dd439d9d1be8b757072880f0ca2891ba2",
		},
		{
			name:            "precondition file digest equals",
			operation:       ensure,
			precondition:    FileDigestEquals{Path: "alpha.md", Digest: strings.Repeat("c", 64)},
			canonicalBase64: "AAAAAAAAAA1va2Y6Y2hhbmdlc2V0AAAAAAAAAAEAAAAAAAAADWxlZ2FjeS1nb2xkZW4AAAAAAAAAEWxlZ2FjeS1hdXRvbWF0aW9uAAAAAAAAAEdzaGEyNTY6YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYQAAAAAAAAABAAAAAAAAAA9lbnN1cmVfcmVsYXRpb24AAAAAAAAABWFscGhhAAAAAAAAAARwYXJ0AAAAAAAAAApkZXBlbmRzX29uAAAAAAAAAARiZXRhAAAAAAAAAAZ0YXJnZXQAAAAAAAAAAQAAAAAAAAASZmlsZV9kaWdlc3RfZXF1YWxzAAAAAAAAAAhhbHBoYS5tZAAAAAAAAABAY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjYw==",
			digest:          "sha256:d6cc9fcaa965d177c349a38bd65dc158bc1fa8f4395650fc3f28c6564fb0ee47",
		},
		{
			name:            "precondition relation exists",
			operation:       ensure,
			precondition:    RelationExists{Source: source, Type: "depends_on", Target: target},
			canonicalBase64: "AAAAAAAAAA1va2Y6Y2hhbmdlc2V0AAAAAAAAAAEAAAAAAAAADWxlZ2FjeS1nb2xkZW4AAAAAAAAAEWxlZ2FjeS1hdXRvbWF0aW9uAAAAAAAAAEdzaGEyNTY6YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYQAAAAAAAAABAAAAAAAAAA9lbnN1cmVfcmVsYXRpb24AAAAAAAAABWFscGhhAAAAAAAAAARwYXJ0AAAAAAAAAApkZXBlbmRzX29uAAAAAAAAAARiZXRhAAAAAAAAAAZ0YXJnZXQAAAAAAAAAAQAAAAAAAAAPcmVsYXRpb25fZXhpc3RzAAAAAAAAAAVhbHBoYQAAAAAAAAAEcGFydAAAAAAAAAAKZGVwZW5kc19vbgAAAAAAAAAEYmV0YQAAAAAAAAAGdGFyZ2V0",
			digest:          "sha256:747d287c55d55c10762c141304be7b5aa6bba5c5b3f8619e1430e154bbf9c251",
		},
		{
			name:            "precondition relation absent",
			operation:       ensure,
			precondition:    RelationAbsent{Source: source, Type: "depends_on", Target: target},
			canonicalBase64: "AAAAAAAAAA1va2Y6Y2hhbmdlc2V0AAAAAAAAAAEAAAAAAAAADWxlZ2FjeS1nb2xkZW4AAAAAAAAAEWxlZ2FjeS1hdXRvbWF0aW9uAAAAAAAAAEdzaGEyNTY6YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYQAAAAAAAAABAAAAAAAAAA9lbnN1cmVfcmVsYXRpb24AAAAAAAAABWFscGhhAAAAAAAAAARwYXJ0AAAAAAAAAApkZXBlbmRzX29uAAAAAAAAAARiZXRhAAAAAAAAAAZ0YXJnZXQAAAAAAAAAAQAAAAAAAAAPcmVsYXRpb25fYWJzZW50AAAAAAAAAAVhbHBoYQAAAAAAAAAEcGFydAAAAAAAAAAKZGVwZW5kc19vbgAAAAAAAAAEYmV0YQAAAAAAAAAGdGFyZ2V0",
			digest:          "sha256:feac04aa1d07b595572e0541ed32f96497e3ff5653282966948247e021437683",
		},
		{
			name:            "precondition fragment unique",
			operation:       ensure,
			precondition:    FragmentUnique{Ref: source},
			canonicalBase64: "AAAAAAAAAA1va2Y6Y2hhbmdlc2V0AAAAAAAAAAEAAAAAAAAADWxlZ2FjeS1nb2xkZW4AAAAAAAAAEWxlZ2FjeS1hdXRvbWF0aW9uAAAAAAAAAEdzaGEyNTY6YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYQAAAAAAAAABAAAAAAAAAA9lbnN1cmVfcmVsYXRpb24AAAAAAAAABWFscGhhAAAAAAAAAARwYXJ0AAAAAAAAAApkZXBlbmRzX29uAAAAAAAAAARiZXRhAAAAAAAAAAZ0YXJnZXQAAAAAAAAAAQAAAAAAAAAPZnJhZ21lbnRfdW5pcXVlAAAAAAAAAAVhbHBoYQAAAAAAAAAEcGFydA==",
			digest:          "sha256:d40c13c3b75595614487aa36f13f9b4dee3793e971e7558cee2cab3d3e040745",
		},
		{
			name:            "precondition revision equals",
			operation:       ensure,
			precondition:    RevisionEquals{Revision: revision},
			canonicalBase64: "AAAAAAAAAA1va2Y6Y2hhbmdlc2V0AAAAAAAAAAEAAAAAAAAADWxlZ2FjeS1nb2xkZW4AAAAAAAAAEWxlZ2FjeS1hdXRvbWF0aW9uAAAAAAAAAEdzaGEyNTY6YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYQAAAAAAAAABAAAAAAAAAA9lbnN1cmVfcmVsYXRpb24AAAAAAAAABWFscGhhAAAAAAAAAARwYXJ0AAAAAAAAAApkZXBlbmRzX29uAAAAAAAAAARiZXRhAAAAAAAAAAZ0YXJnZXQAAAAAAAAAAQAAAAAAAAAPcmV2aXNpb25fZXF1YWxzAAAAAAAAAEdzaGEyNTY6YmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYg==",
			digest:          "sha256:2194b841bd90ea7bed5d408bdddc7c31f88e39f73107a7febf6c5a433d86a6f1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			change := legacyVariantChangeSet(tt.operation, tt.precondition)
			want, decodeErr := base64.StdEncoding.DecodeString(tt.canonicalBase64)
			if decodeErr != nil {
				t.Fatalf("DecodeString(golden) error = %v", decodeErr)
			}

			// Act.
			got, canonicalErr := change.CanonicalBytes()
			digest, digestErr := change.RequestDigest()

			// Assert.
			if canonicalErr != nil || digestErr != nil {
				t.Fatalf("canonical errors = bytes:%v digest:%v", canonicalErr, digestErr)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("CanonicalBytes() = %x, want %x", got, want)
			}
			if digest != tt.digest {
				t.Fatalf("RequestDigest() = %q, want %q", digest, tt.digest)
			}
		})
	}
}

func TestChangeSetCanonicalBytes_LegacyMixedGolden(t *testing.T) {
	// Arrange. This complete legacy v1 envelope intentionally contains one
	// operation and multiple preconditions. The golden was independently
	// decoded and hashed outside the canonicalizer before being frozen here.
	const canonicalBase64 = "AAAAAAAAAA1va2Y6Y2hhbmdlc2V0AAAAAAAAAAEAAAAAAAAADWxlZ2FjeS1jaGFuZ2UAAAAAAAAAEWxlZ2FjeS1hdXRvbWF0aW9uAAAAAAAAAEdzaGEyNTY6YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYQAAAAAAAAABAAAAAAAAAA9lbnN1cmVfcmVsYXRpb24AAAAAAAAABWFscGhhAAAAAAAAAARwYXJ0AAAAAAAAAApkZXBlbmRzX29uAAAAAAAAAARiZXRhAAAAAAAAAAZ0YXJnZXQAAAAAAAAAAgAAAAAAAAAKcmVmX2V4aXN0cwAAAAAAAAAFYWxwaGEAAAAAAAAABHBhcnQAAAAAAAAAD3JlbGF0aW9uX2Fic2VudAAAAAAAAAAFYWxwaGEAAAAAAAAABHBhcnQAAAAAAAAACmRlcGVuZHNfb24AAAAAAAAABGJldGEAAAAAAAAABnRhcmdldA=="
	const wantDigest = "sha256:90b4416280a7a27c0e4f572b8c2d2f22de89f50f8879a850774821095a4ec9e2"
	const wantLength = 397
	want, decodeErr := base64.StdEncoding.DecodeString(canonicalBase64)
	if decodeErr != nil {
		t.Fatalf("DecodeString(golden) error = %v", decodeErr)
	}
	firstChange := legacyGoldenChangeSet(t)
	freshChange := legacyGoldenChangeSet(t)

	// Act.
	first, firstErr := firstChange.CanonicalBytes()
	fresh, freshErr := freshChange.CanonicalBytes()
	digest, digestErr := firstChange.RequestDigest()

	// Assert.
	if firstErr != nil || freshErr != nil || digestErr != nil {
		t.Fatalf("canonical errors = first:%v fresh:%v digest:%v", firstErr, freshErr, digestErr)
	}
	if len(want) != wantLength || len(first) != wantLength {
		t.Fatalf("canonical lengths = golden:%d actual:%d, want %d", len(want), len(first), wantLength)
	}
	if !bytes.Equal(first, want) || !bytes.Equal(fresh, want) {
		t.Fatalf("CanonicalBytes() = first:%x fresh:%x, want %x", first, fresh, want)
	}
	if digest != wantDigest {
		t.Fatalf("RequestDigest() = %q, want %q", digest, wantDigest)
	}

	// CanonicalBytes must return fresh storage as well as deterministic bytes.
	first[0] ^= 0xff
	regenerated, regeneratedErr := firstChange.CanonicalBytes()
	if regeneratedErr != nil || !bytes.Equal(regenerated, want) {
		t.Fatalf("CanonicalBytes() after caller mutation = %x, error %v", regenerated, regeneratedErr)
	}
}

func TestChangeSetCanonicalBytes_LegacySequenceOrderIsSignificant(t *testing.T) {
	// Arrange.
	operationsForward := legacyGoldenChangeSet(t)
	operationsReverse := legacyGoldenChangeSet(t)
	move := MoveConcept{From: testRef(t, "alpha", "").ID, To: testRef(t, "nested/beta", "").ID}
	operationsForward.Operations = []Operation{operationsForward.Operations[0], move}
	operationsReverse.Operations = []Operation{move, operationsReverse.Operations[0]}
	preconditionsForward := legacyGoldenChangeSet(t)
	preconditionsReverse := legacyGoldenChangeSet(t)
	preconditionsReverse.Preconditions = []Precondition{
		preconditionsReverse.Preconditions[1],
		preconditionsReverse.Preconditions[0],
	}

	// Act.
	operationsForwardBytes, operationsForwardErr := operationsForward.CanonicalBytes()
	operationsReverseBytes, operationsReverseErr := operationsReverse.CanonicalBytes()
	operationsForwardDigest, operationsForwardDigestErr := operationsForward.RequestDigest()
	operationsReverseDigest, operationsReverseDigestErr := operationsReverse.RequestDigest()
	preconditionsForwardBytes, preconditionsForwardErr := preconditionsForward.CanonicalBytes()
	preconditionsReverseBytes, preconditionsReverseErr := preconditionsReverse.CanonicalBytes()
	preconditionsForwardDigest, preconditionsForwardDigestErr := preconditionsForward.RequestDigest()
	preconditionsReverseDigest, preconditionsReverseDigestErr := preconditionsReverse.RequestDigest()

	// Assert.
	for _, result := range []struct {
		name string
		err  error
	}{
		{"operations forward bytes", operationsForwardErr},
		{"operations reverse bytes", operationsReverseErr},
		{"operations forward digest", operationsForwardDigestErr},
		{"operations reverse digest", operationsReverseDigestErr},
		{"preconditions forward bytes", preconditionsForwardErr},
		{"preconditions reverse bytes", preconditionsReverseErr},
		{"preconditions forward digest", preconditionsForwardDigestErr},
		{"preconditions reverse digest", preconditionsReverseDigestErr},
	} {
		if result.err != nil {
			t.Fatalf("%s error = %v", result.name, result.err)
		}
	}
	if bytes.Equal(operationsForwardBytes, operationsReverseBytes) || operationsForwardDigest == operationsReverseDigest {
		t.Fatal("canonical operation sequence is not order-sensitive")
	}
	if bytes.Equal(preconditionsForwardBytes, preconditionsReverseBytes) || preconditionsForwardDigest == preconditionsReverseDigest {
		t.Fatal("canonical multi-precondition sequence is not order-sensitive")
	}
}

func TestChangeSetCanonicalBytes_LegacyMultiOperationMixedGolden(t *testing.T) {
	// Arrange. This is the complete mixed legacy v1 envelope: two ordered
	// operations and two ordered preconditions. The frozen bytes were decoded
	// and hashed independently outside the canonicalizer.
	const canonicalBase64 = "AAAAAAAAAA1va2Y6Y2hhbmdlc2V0AAAAAAAAAAEAAAAAAAAADWxlZ2FjeS1jaGFuZ2UAAAAAAAAAEWxlZ2FjeS1hdXRvbWF0aW9uAAAAAAAAAEdzaGEyNTY6YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYQAAAAAAAAACAAAAAAAAAA9lbnN1cmVfcmVsYXRpb24AAAAAAAAABWFscGhhAAAAAAAAAARwYXJ0AAAAAAAAAApkZXBlbmRzX29uAAAAAAAAAARiZXRhAAAAAAAAAAZ0YXJnZXQAAAAAAAAADG1vdmVfY29uY2VwdAAAAAAAAAAFYWxwaGEAAAAAAAAAC25lc3RlZC9iZXRhAAAAAAAAAAIAAAAAAAAACnJlZl9leGlzdHMAAAAAAAAABWFscGhhAAAAAAAAAARwYXJ0AAAAAAAAAA9yZWxhdGlvbl9hYnNlbnQAAAAAAAAABWFscGhhAAAAAAAAAARwYXJ0AAAAAAAAAApkZXBlbmRzX29uAAAAAAAAAARiZXRhAAAAAAAAAAZ0YXJnZXQ="
	const wantLength = 449
	const wantDigest = "sha256:1cca72b5df0a2ce703f96e81f829ee72a72cdc3e6ec6c1c3327aebfd892a4450"
	const wantReversedOperationDigest = "sha256:df4ee37df5097197728f0d063d06f53e79a1630fa7a162a8aed0de7e975755ae"
	const wantReversedPreconditionDigest = "sha256:a259f441fcdf3005d32bd09835ba231d80d985f3dccbe85b32bcaa1bda4c9e45"
	want, decodeErr := base64.StdEncoding.DecodeString(canonicalBase64)
	if decodeErr != nil {
		t.Fatalf("DecodeString(golden) error = %v", decodeErr)
	}
	change := legacyMultiOperationGoldenChangeSet(t)
	freshChange := legacyMultiOperationGoldenChangeSet(t)
	reversedOperations := legacyMultiOperationGoldenChangeSet(t)
	reversedOperations.Operations = []Operation{
		reversedOperations.Operations[1],
		reversedOperations.Operations[0],
	}
	reversedPreconditions := legacyMultiOperationGoldenChangeSet(t)
	reversedPreconditions.Preconditions = []Precondition{
		reversedPreconditions.Preconditions[1],
		reversedPreconditions.Preconditions[0],
	}

	// Act.
	raw, canonicalErr := change.CanonicalBytes()
	freshRaw, freshErr := freshChange.CanonicalBytes()
	digest, digestErr := change.RequestDigest()
	reversedOperationRaw, reversedOperationCanonicalErr := reversedOperations.CanonicalBytes()
	reversedOperationDigest, reversedOperationErr := reversedOperations.RequestDigest()
	reversedPreconditionRaw, reversedPreconditionCanonicalErr := reversedPreconditions.CanonicalBytes()
	reversedPreconditionDigest, reversedPreconditionErr := reversedPreconditions.RequestDigest()

	// Assert.
	for _, result := range []struct {
		name string
		err  error
	}{
		{"canonical bytes", canonicalErr},
		{"fresh canonical bytes", freshErr},
		{"canonical digest", digestErr},
		{"reversed operation bytes", reversedOperationCanonicalErr},
		{"reversed operation digest", reversedOperationErr},
		{"reversed precondition bytes", reversedPreconditionCanonicalErr},
		{"reversed precondition digest", reversedPreconditionErr},
	} {
		if result.err != nil {
			t.Fatalf("%s error = %v", result.name, result.err)
		}
	}
	if len(want) != wantLength || len(raw) != wantLength {
		t.Fatalf("canonical lengths = golden:%d actual:%d, want %d", len(want), len(raw), wantLength)
	}
	if !bytes.Equal(raw, want) || !bytes.Equal(freshRaw, want) {
		t.Fatalf("CanonicalBytes() = actual:%x fresh:%x, want %x", raw, freshRaw, want)
	}
	if digest != wantDigest {
		t.Fatalf("RequestDigest() = %q, want %q", digest, wantDigest)
	}
	if bytes.Equal(reversedOperationRaw, want) || reversedOperationDigest != wantReversedOperationDigest {
		t.Fatalf("reversed operation sequence = bytes_equal:%t digest:%q, want digest %q", bytes.Equal(reversedOperationRaw, want), reversedOperationDigest, wantReversedOperationDigest)
	}
	if bytes.Equal(reversedPreconditionRaw, want) || reversedPreconditionDigest != wantReversedPreconditionDigest {
		t.Fatalf("reversed precondition sequence = bytes_equal:%t digest:%q, want digest %q", bytes.Equal(reversedPreconditionRaw, want), reversedPreconditionDigest, wantReversedPreconditionDigest)
	}

	// CanonicalBytes returns fresh storage even for the complete mixed envelope.
	raw[0] ^= 0xff
	regenerated, regeneratedErr := change.CanonicalBytes()
	if regeneratedErr != nil || !bytes.Equal(regenerated, want) {
		t.Fatalf("CanonicalBytes() after caller mutation = %x, error %v", regenerated, regeneratedErr)
	}
}

func TestCommitReceiptJSON_LegacyGolden(t *testing.T) {
	// Arrange.
	revision := Revision("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	receipt := CommitReceipt{
		FormatVersion:  CommitReceiptFormatVersion,
		ChangeSetID:    "legacy-change",
		IdempotencyKey: "legacy-retry",
		RequestDigest:  "sha256:90b4416280a7a27c0e4f572b8c2d2f22de89f50f8879a850774821095a4ec9e2",
		BaseRevision:   revision,
		ResultRevision: revision,
		CommitTime:     time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC),
		ChangedRefs:    []bundle.RelationRef{testRef(t, "alpha", "part"), testRef(t, "beta", "target")},
		ChangedFiles: []FileChange{
			{Kind: FileDelete, Path: "a.md"},
			{Kind: FileRename, Path: "moved.md", From: "old.md"},
			{Kind: FileWrite, Path: "z.md"},
		},
	}
	const want = `{"FormatVersion":1,"ChangeSetID":"legacy-change","IdempotencyKey":"legacy-retry","RequestDigest":"sha256:90b4416280a7a27c0e4f572b8c2d2f22de89f50f8879a850774821095a4ec9e2","BaseRevision":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","ResultRevision":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","CommitTime":"2026-07-19T00:00:00Z","ChangedRefs":["alpha#part","beta#target"],"ChangedFiles":[{"Kind":"delete","Path":"a.md","From":""},{"Kind":"rename","Path":"moved.md","From":"old.md"},{"Kind":"write","Path":"z.md","From":""}]}`

	// Act.
	raw, err := json.Marshal(receipt)

	// Assert.
	if err != nil {
		t.Fatalf("Marshal(CommitReceipt) error = %v", err)
	}
	if string(raw) != want {
		t.Fatalf("CommitReceipt JSON = %s, want %s", raw, want)
	}
}

func exportedFieldSignatures(typ reflect.Type) []string {
	fields := make([]string, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.IsExported() {
			fields = append(fields, field.Name+" "+field.Type.String())
		}
	}
	return fields
}

func publicMethodSignatures(typ reflect.Type) []string {
	methods := make([]string, 0, typ.NumMethod())
	for i := 0; i < typ.NumMethod(); i++ {
		method := typ.Method(i)
		methods = append(methods, method.Name+" "+method.Type.String())
	}
	return methods
}

func legacyVariantChangeSet(operation Operation, precondition Precondition) ChangeSet {
	change := ChangeSet{
		Version:      ChangeSetFormatVersion,
		ID:           "legacy-golden",
		Actor:        "legacy-automation",
		BaseRevision: Revision("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		Operations:   []Operation{operation},
	}
	if precondition != nil {
		change.Preconditions = []Precondition{precondition}
	}
	return change
}

func legacyGoldenChangeSet(t *testing.T) ChangeSet {
	t.Helper()
	source := testRef(t, "alpha", "part")
	target := testRef(t, "beta", "target")
	return ChangeSet{
		Version:      ChangeSetFormatVersion,
		ID:           "legacy-change",
		Actor:        "legacy-automation",
		BaseRevision: Revision("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		Operations: []Operation{
			EnsureRelation{Source: source, Type: "depends_on", Target: target},
		},
		Preconditions: []Precondition{
			RefExists{Ref: source},
			RelationAbsent{Source: source, Type: "depends_on", Target: target},
		},
	}
}

func legacyMultiOperationGoldenChangeSet(t *testing.T) ChangeSet {
	t.Helper()
	change := legacyGoldenChangeSet(t)
	change.Operations = append(change.Operations,
		MoveConcept{From: testRef(t, "alpha", "").ID, To: testRef(t, "nested/beta", "").ID},
	)
	return change
}
