package store

import (
	"bytes"
	"encoding/json"
	"errors"
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
