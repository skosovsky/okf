package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/skosovsky/okf/bundle"
)

func TestCommitReceiptJSONRejectsAmbiguousAndInvalidValues(t *testing.T) {
	// Arrange.
	revision := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	valid := `{"FormatVersion":1,"ChangeSetID":"change","IdempotencyKey":"retry","RequestDigest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","BaseRevision":"` + revision + `","ResultRevision":"` + revision + `","CommitTime":"2026-07-19T00:00:00Z","ChangedRefs":["alpha"],"ChangedFiles":[{"Kind":"write","Path":"a.md","From":""}]}`
	tests := []struct {
		name string
		raw  string
	}{
		{name: "unknown", raw: valid[:len(valid)-1] + `,"extra":true}`},
		{name: "duplicate nested", raw: `{"FormatVersion":1,"ChangeSetID":"change","IdempotencyKey":"retry","RequestDigest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","BaseRevision":"` + revision + `","ResultRevision":"` + revision + `","CommitTime":"2026-07-19T00:00:00Z","ChangedRefs":["alpha"],"ChangedFiles":[{"Kind":"write","Kind":"delete","Path":"a.md","From":""}]}`},
		{name: "missing", raw: `{"FormatVersion":1}`},
		{name: "null refs", raw: strings.Replace(valid, `"ChangedRefs":["alpha"]`, `"ChangedRefs":null`, 1)},
		{name: "null files", raw: strings.Replace(valid, `"ChangedFiles":[{"Kind":"write","Path":"a.md","From":""}]`, `"ChangedFiles":null`, 1)},
		{name: "missing from", raw: strings.Replace(valid, `,"From":""`, ``, 1)},
		{name: "null path", raw: strings.Replace(valid, `"Path":"a.md"`, `"Path":null`, 1)},
		{name: "wrong kind type", raw: strings.Replace(valid, `"Kind":"write"`, `"Kind":true`, 1)},
		{name: "wrong ref element", raw: strings.Replace(valid, `"ChangedRefs":["alpha"]`, `"ChangedRefs":[{}]`, 1)},
		{name: "fractional version", raw: strings.Replace(valid, `"FormatVersion":1`, `"FormatVersion":1.0`, 1)},
		{name: "null revision", raw: strings.Replace(valid, `"BaseRevision":"`+revision+`"`, `"BaseRevision":null`, 1)},
		{name: "unknown file field", raw: strings.Replace(valid, `"From":""}`, `"From":"","extra":true}`, 1)},
		{name: "unsorted refs", raw: strings.Replace(valid, "[\"alpha\"]", "[\"beta\",\"alpha\"]", 1)},
		{name: "trailing", raw: valid + ` {}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act.
			var receipt CommitReceipt
			err := json.Unmarshal([]byte(tt.raw), &receipt)

			// Assert.
			if err == nil || (tt.name != "trailing" && !errors.Is(err, ErrStorageCorrupt)) {
				t.Fatalf("Unmarshal error = %v, want storage corruption", err)
			}
		})
	}
}

func TestCommitReceiptJSONRejectsNonCanonicalButSemanticallyEquivalentBytes(t *testing.T) {
	// Arrange. These forms decode to the same Go value under encoding/json but
	// must not become alternate durable receipt encodings.
	revision := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	canonical := `{"FormatVersion":1,"ChangeSetID":"change","IdempotencyKey":"retry","RequestDigest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","BaseRevision":"` + revision + `","ResultRevision":"` + revision + `","CommitTime":"2026-07-19T00:00:00Z","ChangedRefs":["alpha"],"ChangedFiles":[{"Kind":"write","Path":"a.md","From":""}]}`
	reordered := `{"ChangeSetID":"change","FormatVersion":1,` + strings.TrimPrefix(canonical, `{"FormatVersion":1,"ChangeSetID":"change",`)
	tests := []string{
		strings.Replace(canonical, `{"FormatVersion"`, `{ "FormatVersion"`, 1),
		strings.Replace(canonical, `"ChangeSetID":"change"`, `"ChangeSetID":"ch\u0061nge"`, 1),
		reordered,
	}
	for _, raw := range tests {
		var receipt CommitReceipt
		if err := json.Unmarshal([]byte(raw), &receipt); !errors.Is(err, ErrStorageCorrupt) {
			t.Fatalf("noncanonical receipt error=%v", err)
		}
	}
}

func TestCommitReceiptJSONRejectsMalformedUTF8BeforeDecoding(t *testing.T) {
	// Arrange. Each value exercises a public or nested durable string field.
	revision := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	valid := `{"FormatVersion":1,"ChangeSetID":"change-id","IdempotencyKey":"retry-key","RequestDigest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","BaseRevision":"` + revision + `","ResultRevision":"` + revision + `","CommitTime":"2026-07-19T00:00:00Z","ChangedRefs":["alpha"],"ChangedFiles":[{"Kind":"write","Path":"a.md","From":"old.md"}]}`
	for _, value := range []string{"change-id", "retry-key", "sha256:bbbb", revision, "2026-07-19", "alpha", "write", "a.md", "old.md"} {
		t.Run(value, func(t *testing.T) {
			raw := append([]byte(nil), valid...)
			at := bytes.Index(raw, []byte(value))
			if at < 0 {
				t.Fatalf("fixture does not contain %q", value)
			}
			raw[at] = 0xff

			// Act.
			var receipt CommitReceipt
			err := json.Unmarshal(raw, &receipt)

			// Assert.
			if err == nil || !errors.Is(err, ErrStorageCorrupt) {
				t.Fatalf("Unmarshal malformed %q error = %v, want storage corruption", value, err)
			}
		})
	}
}

func TestCommitReceiptJSONRoundTripIsCanonical(t *testing.T) {
	// Arrange.
	alpha, err := bundle.ParseRelationRef("alpha")
	if err != nil {
		t.Fatal(err)
	}
	beta, err := bundle.ParseRelationRef("beta")
	if err != nil {
		t.Fatal(err)
	}
	revision := Revision("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	receipt := CommitReceipt{FormatVersion: CommitReceiptFormatVersion, ChangeSetID: "change", IdempotencyKey: "retry", RequestDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", BaseRevision: revision, ResultRevision: revision, CommitTime: time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC), ChangedRefs: []bundle.RelationRef{alpha, beta}, ChangedFiles: []FileChange{{Kind: FileDelete, Path: "a.md"}, {Kind: FileWrite, Path: "z.md"}}}

	// Act.
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var restored CommitReceipt
	err = json.Unmarshal(raw, &restored)

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if restored.CommitTime.Location() != time.UTC || restored.ChangedRefs[0].String() != "alpha" || restored.ChangedFiles[0].Path != "a.md" {
		t.Fatalf("canonical receipt = %#v", restored)
	}
	const want = `{"FormatVersion":1,"ChangeSetID":"change","IdempotencyKey":"retry","RequestDigest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","BaseRevision":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","ResultRevision":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","CommitTime":"2026-07-19T00:00:00Z","ChangedRefs":["alpha","beta"],"ChangedFiles":[{"Kind":"delete","Path":"a.md","From":""},{"Kind":"write","Path":"z.md","From":""}]}`
	if string(raw) != want {
		t.Fatalf("canonical receipt bytes = %s, want %s", raw, want)
	}
}

func TestCommitReceiptJSONPreservesV1LexicalWireOrderAcrossRootFragmentBoundary(t *testing.T) {
	// Arrange. v1 receipts canonically sort the serialized ref strings. Sorting
	// the structural (ID, Fragment) tuple would reverse this historical order.
	root, err := bundle.ParseRelationRef("a!")
	if err != nil {
		t.Fatal(err)
	}
	fragment, err := bundle.ParseRelationRef("a#z")
	if err != nil {
		t.Fatal(err)
	}
	revision := Revision("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	receipt := CommitReceipt{
		FormatVersion:  CommitReceiptFormatVersion,
		ChangeSetID:    "lexical-v1",
		IdempotencyKey: "lexical-v1-key",
		RequestDigest:  "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		BaseRevision:   revision,
		ResultRevision: revision,
		CommitTime:     time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC),
		ChangedRefs:    []bundle.RelationRef{root, fragment},
		ChangedFiles:   make([]FileChange, 0),
	}
	const want = `{"FormatVersion":1,"ChangeSetID":"lexical-v1","IdempotencyKey":"lexical-v1-key","RequestDigest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","BaseRevision":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","ResultRevision":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","CommitTime":"2026-07-19T00:00:00Z","ChangedRefs":["a!","a#z"],"ChangedFiles":[]}`

	// Act.
	raw, marshalErr := json.Marshal(receipt)
	var restored CommitReceipt
	unmarshalErr := json.Unmarshal([]byte(want), &restored)

	// Assert.
	if marshalErr != nil || unmarshalErr != nil {
		t.Fatalf("canonical v1 receipt errors = marshal:%v unmarshal:%v", marshalErr, unmarshalErr)
	}
	if string(raw) != want {
		t.Fatalf("canonical v1 receipt = %s, want %s", raw, want)
	}
	if got := []string{restored.ChangedRefs[0].String(), restored.ChangedRefs[1].String()}; !reflect.DeepEqual(got, []string{"a!", "a#z"}) {
		t.Fatalf("restored ChangedRefs = %#v, want lexical v1 order", got)
	}
}

func TestCommitReceiptJSONRoundTripsEscapedRootRelationRefStructurally(t *testing.T) {
	// Arrange.
	embeddedID, err := bundle.NewConceptID([]string{"source#part"})
	if err != nil {
		t.Fatal(err)
	}
	sourceID, err := bundle.ParseConceptID("source")
	if err != nil {
		t.Fatal(err)
	}
	revision := Revision("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	receipt := CommitReceipt{
		FormatVersion:  CommitReceiptFormatVersion,
		ChangeSetID:    "escaped-root",
		RequestDigest:  "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		BaseRevision:   revision,
		ResultRevision: revision,
		CommitTime:     time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC),
		ChangedRefs: []bundle.RelationRef{
			{ID: sourceID, Fragment: "part"},
			{ID: embeddedID},
		},
	}

	// Act.
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var restored CommitReceipt
	err = json.Unmarshal(raw, &restored)

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	const want = `["source#part","source\\#part"]`
	if !bytes.Contains(raw, []byte(want)) {
		t.Fatalf("receipt ChangedRefs encoding = %s, want fragment and escaped-root tuples %s", raw, want)
	}
	if len(restored.ChangedRefs) != 2 ||
		relationRefIdentityOf(restored.ChangedRefs[0]) != (relationRefIdentity{id: "source", fragment: "part"}) ||
		relationRefIdentityOf(restored.ChangedRefs[1]) != (relationRefIdentity{id: "source#part"}) {
		t.Fatalf("restored ChangedRefs = %#v, want exact structural tuples", restored.ChangedRefs)
	}
}

func TestCommitReceiptJSONRejectsAmbiguousAndNonCanonicalRelationRefEscapes(t *testing.T) {
	// Arrange.
	revision := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	template := `{"FormatVersion":1,"ChangeSetID":"change","IdempotencyKey":"","RequestDigest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","BaseRevision":"` + revision + `","ResultRevision":"` + revision + `","CommitTime":"2026-07-19T00:00:00Z","ChangedRefs":[%s],"ChangedFiles":[]}`
	tests := []struct {
		name string
		ref  string
	}{
		{name: "ambiguous extra delimiter", ref: `"source#part#tail"`},
		{name: "escape without hash", ref: `"source\\part"`},
		{name: "trailing escape", ref: `"source\\"`},
		{name: "noncanonical JSON escape", ref: `"source\u005c#part"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var receipt CommitReceipt

			// Act.
			err := json.Unmarshal([]byte(fmt.Sprintf(template, tt.ref)), &receipt)

			// Assert.
			if !errors.Is(err, ErrStorageCorrupt) {
				t.Fatalf("UnmarshalJSON() error = %v, want storage corruption", err)
			}
		})
	}
}

func TestCommitReceiptMarshalJSONRejectsInvalidReceipt(t *testing.T) {
	// Arrange.
	revision := Revision("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	alpha, err := bundle.ParseRelationRef("alpha")
	if err != nil {
		t.Fatal(err)
	}
	beta, err := bundle.ParseRelationRef("beta")
	if err != nil {
		t.Fatal(err)
	}
	valid := CommitReceipt{FormatVersion: CommitReceiptFormatVersion, ChangeSetID: "change", RequestDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", BaseRevision: revision, ResultRevision: revision, CommitTime: time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC), ChangedRefs: []bundle.RelationRef{alpha, beta}}
	tests := []CommitReceipt{
		{FormatVersion: valid.FormatVersion, ChangeSetID: valid.ChangeSetID, RequestDigest: valid.RequestDigest, BaseRevision: valid.BaseRevision, ResultRevision: valid.ResultRevision, CommitTime: valid.CommitTime, ChangedRefs: []bundle.RelationRef{beta, alpha}},
		{FormatVersion: valid.FormatVersion, ChangeSetID: valid.ChangeSetID, RequestDigest: valid.RequestDigest, BaseRevision: valid.BaseRevision, ResultRevision: valid.ResultRevision, CommitTime: valid.CommitTime, ChangedRefs: []bundle.RelationRef{alpha, alpha}},
		{FormatVersion: valid.FormatVersion, ChangeSetID: valid.ChangeSetID, RequestDigest: valid.RequestDigest, BaseRevision: valid.BaseRevision, ResultRevision: valid.ResultRevision, ChangedRefs: valid.ChangedRefs},
	}

	for _, receipt := range tests {
		// Act.
		_, marshalErr := json.Marshal(receipt)

		// Assert.
		if !errors.Is(marshalErr, ErrStorageCorrupt) {
			t.Fatalf("MarshalJSON() error = %v, want storage corruption", marshalErr)
		}
	}
}

func TestCommitReceiptFileChangePathsUseRevisionPathPolicy(t *testing.T) {
	// Arrange.
	revision := Revision("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	base := CommitReceipt{
		FormatVersion:  CommitReceiptFormatVersion,
		ChangeSetID:    "change",
		RequestDigest:  "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		BaseRevision:   revision,
		ResultRevision: revision,
		CommitTime:     time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC),
	}
	invalidUTF8 := string([]byte{'b', 'a', 'd', 0xff})
	tests := []struct {
		name  string
		value string
		valid bool
	}{
		{name: "canonical file", value: "concepts/alpha.md", valid: true},
		{name: "unicode", value: "assets/данные.csv", valid: true},
		{name: "nested metadata name", value: "assets/.okf/data", valid: true},
		{name: "invalid UTF-8", value: invalidUTF8},
		{name: "NUL", value: "bad\x00path"},
		{name: "C0", value: "bad\x1fpath"},
		{name: "DEL", value: "bad\x7fpath"},
		{name: "empty", value: ""},
		{name: "absolute", value: "/absolute.md"},
		{name: "backslash", value: `dir\file.md`},
		{name: "current directory", value: "."},
		{name: "parent directory", value: ".."},
		{name: "traversal", value: "../escape.md"},
		{name: "embedded traversal", value: "dir/../escape.md"},
		{name: "dot prefix", value: "./file.md"},
		{name: "duplicate slash", value: "dir//file.md"},
		{name: "reserved metadata root", value: ".okf"},
		{name: "reserved metadata descendant", value: ".okf/journal"},
	}

	for _, field := range []string{"Path", "From"} {
		t.Run(field, func(t *testing.T) {
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					receipt := base
					switch field {
					case "Path":
						receipt.ChangedFiles = []FileChange{{Kind: FileWrite, Path: tt.value}}
					case "From":
						receipt.ChangedFiles = []FileChange{{Kind: FileRename, Path: "target.md", From: tt.value}}
					default:
						t.Fatalf("unknown field %q", field)
					}

					// Act.
					revisionPathErr := bundle.ValidateRevisionPath(tt.value)
					validateErr := ValidateCommitReceipt(receipt)
					raw, marshalErr := receipt.MarshalJSON()

					// Assert.
					if (revisionPathErr == nil) != tt.valid {
						t.Fatalf("bundle.ValidateRevisionPath(%q) error = %v, valid = %t", tt.value, revisionPathErr, tt.valid)
					}
					if (validateErr == nil) != tt.valid {
						t.Fatalf("ValidateCommitReceipt(%s=%q) error = %v, valid = %t", field, tt.value, validateErr, tt.valid)
					}
					if (marshalErr == nil) != tt.valid {
						t.Fatalf("MarshalJSON(%s=%q) error = %v, valid = %t", field, tt.value, marshalErr, tt.valid)
					}
					if !tt.valid {
						if !errors.Is(validateErr, ErrStorageCorrupt) || !errors.Is(marshalErr, ErrStorageCorrupt) {
							t.Fatalf("%s=%q errors = validate:%v marshal:%v, want storage corruption", field, tt.value, validateErr, marshalErr)
						}
						if raw != nil {
							t.Fatalf("MarshalJSON(%s=%q) bytes = %q, want nil before encoding normalization", field, tt.value, raw)
						}
					}
				})
			}
		})
	}
}

func TestCommitReceiptMarshalJSONRejectsMalformedFilePathBeforeEncodingNormalization(t *testing.T) {
	// Arrange. encoding/json would otherwise replace malformed UTF-8 with
	// U+FFFD, producing durable bytes for a value that never passed the path
	// contract.
	revision := Revision("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	base := CommitReceipt{
		FormatVersion:  CommitReceiptFormatVersion,
		ChangeSetID:    "change",
		RequestDigest:  "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		BaseRevision:   revision,
		ResultRevision: revision,
		CommitTime:     time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC),
	}
	malformed := string([]byte{'b', 'a', 'd', 0xff})
	tests := []struct {
		name string
		file FileChange
	}{
		{name: "Path", file: FileChange{Kind: FileWrite, Path: malformed}},
		{name: "From", file: FileChange{Kind: FileRename, Path: "target.md", From: malformed}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			receipt := base
			receipt.ChangedFiles = []FileChange{tt.file}

			// Act.
			raw, err := receipt.MarshalJSON()

			// Assert.
			if raw != nil || !errors.Is(err, ErrStorageCorrupt) {
				t.Fatalf("MarshalJSON() = %q, %v, want nil storage corruption", raw, err)
			}
		})
	}
}
