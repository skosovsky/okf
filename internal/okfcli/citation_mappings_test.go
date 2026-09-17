package okfcli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skosovsky/okf/internal/migrationpath"
)

func TestCitationMappingsStrictBoundedContractAndCanonicalization(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	path := filepath.Join(root, "mappings.json")
	writeFixtureFile(t, path, `[
  {"path":"z/log.md","entries":[
    {"legacy_number":2,"legacy_entry":"numbered raw","source_id":"two","title":"Two"},
    {"legacy_entry":"raw bullet","source_id":"raw"},
    {"legacy_number":1,"legacy_entry":"numbered raw","source_id":"one","resource":"https://one.example"}
  ]},
  {"path":"index.md","entries":[{"legacy_number":7,"source_id":"root"}]}
]`)

	// Act.
	mappings, err := loadCitationMappings(path)

	// Assert.
	if err != nil {
		t.Fatalf("loadCitationMappings() error = %v", err)
	}
	if len(mappings) != 2 || mappings[0].Path != "index.md" || mappings[1].Path != "z/log.md" {
		t.Fatalf("mappings = %#v, want canonical path order", mappings)
	}
	entries := mappings[1].Entries
	if len(entries) != 3 ||
		entries[0].LegacyNumber != 0 || entries[0].LegacyEntry != "raw bullet" ||
		entries[1].LegacyNumber != 1 || entries[1].LegacyEntry != "numbered raw" ||
		entries[2].LegacyNumber != 2 || entries[2].LegacyEntry != "numbered raw" ||
		entries[0].SourceID != "raw" || entries[1].SourceID != "one" || entries[2].SourceID != "two" {
		t.Fatalf("entries = %#v, want canonical legacy_number/legacy_entry order", entries)
	}
}

func TestCitationMappingsPreserveExactMultilineLegacyEntry(t *testing.T) {
	// Arrange.
	path := filepath.Join(t.TempDir(), "mappings.json")
	writeFixtureFile(t, path, `[
		{"path":"a.md","entries":[{
			"legacy_entry":"first line\r\n\r\n  second line\t",
			"source_id":"crlf"
		}]},
		{"path":"b.md","entries":[{
			"legacy_entry":"first line\n\n  second line\t",
			"source_id":"lf"
		}]},
		{"path":"c.md","entries":[{
			"legacy_entry":"`+strings.Repeat("x", maxCitationMappingString)+`",
			"source_id":"limit"
		}]}
	]`)

	// Act.
	mappings, err := loadCitationMappings(path)

	// Assert.
	if err != nil {
		t.Fatalf("loadCitationMappings() error = %v", err)
	}
	if got := mappings[0].Entries[0].LegacyEntry; got != "first line\r\n\r\n  second line\t" {
		t.Fatalf("legacy_entry = %q, want exact CRLF/tab-preserving raw text", got)
	}
	if got := mappings[1].Entries[0].LegacyEntry; got != "first line\n\n  second line\t" {
		t.Fatalf("legacy_entry = %q, want exact LF/tab-preserving raw text", got)
	}
	if mappings[0].Entries[0].LegacyEntry == mappings[1].Entries[0].LegacyEntry {
		t.Fatal("LF and CRLF legacy selectors were normalized to the same value")
	}
	if got := len(mappings[2].Entries[0].LegacyEntry); got != maxCitationMappingString {
		t.Fatalf("legacy_entry length = %d, want exact accepted limit %d", got, maxCitationMappingString)
	}
}

func TestCitationMappingPathsMatchSharedMigrationClassifier(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "root index", path: "index.md", want: true},
		{name: "root log", path: "log.md", want: true},
		{name: "root concept", path: "concept.md", want: true},
		{name: "nested index", path: "nested/index.md", want: true},
		{name: "nested log", path: "nested/log.md", want: true},
		{name: "nested concept", path: "nested/concept.md", want: true},
		{name: "non-Markdown", path: "asset.sql"},
		{name: "noncanonical dot segment", path: "./concept.md"},
		{name: "noncanonical duplicate slash", path: "nested//concept.md"},
		{name: "reserved metadata", path: ".okf/private.md"},
		{name: "out of scope relative", path: "../escape.md"},
		{name: "out of scope absolute", path: "/escape.md"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			if got := migrationpath.IsMarkdownDocument(test.path); got != test.want {
				t.Fatalf("shared IsMarkdownDocument(%q) = %t, want %t", test.path, got, test.want)
			}
			payload, err := json.Marshal([]map[string]any{{
				"path": test.path,
				"entries": []map[string]any{{
					"legacy_number": 1,
					"source_id":     "source",
				}},
			}})
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			path := filepath.Join(t.TempDir(), "mappings.json")
			writeFixtureFile(t, path, string(payload))

			// Act.
			mappings, err := loadCitationMappings(path)

			// Assert.
			if (err == nil) != test.want {
				t.Fatalf("loadCitationMappings(%q) = %#v, %v; want accepted=%t",
					test.path, mappings, err, test.want)
			}
			if test.want && (len(mappings) != 1 || mappings[0].Path != test.path) {
				t.Fatalf("accepted mappings = %#v, want exact path %q", mappings, test.path)
			}
			if !test.want && mappings != nil {
				t.Fatalf("rejected mappings = %#v, want nil", mappings)
			}
		})
	}
}

func TestCitationMappingsRejectMalformedUnboundedAndAmbiguousInput(t *testing.T) {
	validEntry := `{"legacy_number":1,"source_id":"source"}`
	documentItems := func(count int) []byte {
		items := make([]string, count)
		for index := range items {
			items[index] = fmt.Sprintf(`{"path":"p%d.md","entries":[%s]}`, index, validEntry)
		}
		return []byte("[" + strings.Join(items, ",") + "]")
	}
	entryItems := func(count int) []byte {
		items := make([]string, count)
		for index := range items {
			items[index] = fmt.Sprintf(`{"legacy_number":%d,"source_id":"s%d"}`, index+1, index)
		}
		return []byte(`[{"path":"a.md","entries":[` + strings.Join(items, ",") + `]}]`)
	}
	tests := []struct {
		name    string
		payload []byte
	}{
		{name: "malformed JSON", payload: []byte(`[`)},
		{name: "multiple JSON values", payload: []byte(`[] []`)},
		{name: "invalid UTF-8", payload: []byte{0xff}},
		{name: "top-level null", payload: []byte(`null`)},
		{name: "top-level object", payload: []byte(`{}`)},
		{name: "unknown document field", payload: []byte(`[{"path":"a.md","entries":[` + validEntry + `],"wat":true}]`)},
		{name: "unknown entry field", payload: []byte(`[{"path":"a.md","entries":[{"legacy_number":1,"source_id":"s","wat":true}]}]`)},
		{name: "legacy number alias", payload: []byte(`[{"path":"a.md","entries":[{"number":1,"source_id":"s"}]}]`)},
		{name: "duplicate document key", payload: []byte(`[{"path":"a.md","path":"b.md","entries":[` + validEntry + `]}]`)},
		{name: "duplicate entry key", payload: []byte(`[{"path":"a.md","entries":[{"legacy_number":1,"legacy_number":2,"source_id":"s"}]}]`)},
		{name: "missing path", payload: []byte(`[{"entries":[` + validEntry + `]}]`)},
		{name: "null path", payload: []byte(`[{"path":null,"entries":[` + validEntry + `]}]`)},
		{name: "null entries", payload: []byte(`[{"path":"a.md","entries":null}]`)},
		{name: "empty entries", payload: []byte(`[{"path":"a.md","entries":[]}]`)},
		{name: "missing selector", payload: []byte(`[{"path":"a.md","entries":[{"source_id":"s"}]}]`)},
		{name: "zero number", payload: []byte(`[{"path":"a.md","entries":[{"legacy_number":0,"source_id":"s"}]}]`)},
		{name: "number above cap", payload: []byte(`[{"path":"a.md","entries":[{"legacy_number":1000001,"source_id":"s"}]}]`)},
		{name: "null number", payload: []byte(`[{"path":"a.md","entries":[{"legacy_number":null,"source_id":"s"}]}]`)},
		{name: "empty legacy entry", payload: []byte(`[{"path":"a.md","entries":[{"legacy_entry":"","source_id":"s"}]}]`)},
		{name: "blank legacy entry", payload: []byte(`[{"path":"a.md","entries":[{"legacy_entry":" \r\n\t","source_id":"s"}]}]`)},
		{name: "null legacy entry", payload: []byte(`[{"path":"a.md","entries":[{"legacy_entry":null,"source_id":"s"}]}]`)},
		{name: "NUL legacy entry", payload: []byte(`[{"path":"a.md","entries":[{"legacy_entry":"x\u0000y","source_id":"s"}]}]`)},
		{name: "unsafe C0 legacy entry", payload: []byte(`[{"path":"a.md","entries":[{"legacy_entry":"x\u0008y","source_id":"s"}]}]`)},
		{name: "DEL legacy entry", payload: []byte(`[{"path":"a.md","entries":[{"legacy_entry":"x\u007fy","source_id":"s"}]}]`)},
		{name: "missing source ID", payload: []byte(`[{"path":"a.md","entries":[{"legacy_number":1}]}]`)},
		{name: "null source ID", payload: []byte(`[{"path":"a.md","entries":[{"legacy_number":1,"source_id":null}]}]`)},
		{name: "duplicate paths", payload: []byte(`[
			{"path":"a.md","entries":[{"legacy_number":1,"source_id":"one"}]},
			{"path":"a.md","entries":[{"legacy_number":2,"source_id":"two"}]}
		]`)},
		{name: "duplicate numbers", payload: []byte(`[{"path":"a.md","entries":[
			{"legacy_number":1,"source_id":"one"},{"legacy_number":1,"legacy_entry":"other","source_id":"two"}
		]}]`)},
		{name: "duplicate legacy entries", payload: []byte(`[{"path":"a.md","entries":[
			{"legacy_entry":"same","source_id":"one"},{"legacy_number":2,"legacy_entry":"same","source_id":"two"}
		]}]`)},
		{name: "parent path", payload: []byte(`[{"path":"../a.md","entries":[` + validEntry + `]}]`)},
		{name: "absolute path", payload: []byte(`[{"path":"/a.md","entries":[` + validEntry + `]}]`)},
		{name: "backslash path", payload: []byte(`[{"path":"a\\b.md","entries":[` + validEntry + `]}]`)},
		{name: "metadata path", payload: []byte(`[{"path":".okf/a.md","entries":[` + validEntry + `]}]`)},
		{name: "non-Markdown path", payload: []byte(`[{"path":"a.txt","entries":[` + validEntry + `]}]`)},
		{
			name:    "path above cap",
			payload: []byte(`[{"path":"` + strings.Repeat("a", maxCitationMappingString) + `.md","entries":[` + validEntry + `]}]`),
		},
		{
			name:    "source ID above cap",
			payload: []byte(`[{"path":"a.md","entries":[{"legacy_number":1,"source_id":"` + strings.Repeat("s", maxCitationMappingString+1) + `"}]}]`),
		},
		{
			name:    "legacy entry above cap",
			payload: []byte(`[{"path":"a.md","entries":[{"legacy_entry":"` + strings.Repeat("x", maxCitationMappingString+1) + `","source_id":"s"}]}]`),
		},
		{
			name:    "depth above cap",
			payload: []byte(strings.Repeat("[", maxCitationMappingsJSONDepth+1) + "0" + strings.Repeat("]", maxCitationMappingsJSONDepth+1)),
		},
		{name: "document item cap", payload: documentItems(maxCitationMappingDocuments + 1)},
		{name: "entry item cap", payload: entryItems(maxCitationMappingEntries + 1)},
		{name: "file size cap", payload: bytes.Repeat([]byte(" "), maxCitationMappingsFileBytes+1)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			path := filepath.Join(t.TempDir(), "mappings.json")
			writeFixtureFile(t, path, string(test.payload))

			// Act.
			mappings, err := loadCitationMappings(path)

			// Assert.
			if err == nil || mappings != nil {
				t.Fatalf("loadCitationMappings() = %#v, %v; want nil/error", mappings, err)
			}
		})
	}
}
