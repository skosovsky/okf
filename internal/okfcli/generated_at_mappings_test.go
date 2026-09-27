package okfcli

import (
	"reflect"
	"testing"
)

func TestGeneratedAtMappingsRequireExplicitInstantsAndClosedFields(t *testing.T) {
	// Arrange.
	cases := []struct {
		name  string
		input string
		valid bool
	}{
		{name: "valid offset", input: `[{"path":"b.md","at":"2026-01-01T10:30:00+07:00"},{"path":"a.md","at":"2026-01-01T03:30:00Z"}]`, valid: true},
		{name: "empty array", input: `[]`, valid: true},
		{name: "blank instant", input: `[{"path":"a.md","at":""}]`},
		{name: "civil date", input: `[{"path":"a.md","at":"2026-01-01"}]`},
		{name: "no timezone", input: `[{"path":"a.md","at":"2026-01-01T10:30:00"}]`},
		{name: "duplicate path", input: `[{"path":"a.md","at":"2026-01-01T00:00:00Z"},{"path":"a.md","at":"2026-02-01T00:00:00Z"}]`},
		{name: "unknown field", input: `[{"path":"a.md","at":"2026-01-01T00:00:00Z","owner":"agent"}]`},
		{name: "duplicate key", input: `[{"path":"a.md","path":"b.md","at":"2026-01-01T00:00:00Z"}]`},
		{name: "invalid path", input: `[{"path":"../a.md","at":"2026-01-01T00:00:00Z"}]`},
		{name: "null", input: `null`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act.
			got, err := parseGeneratedAtMappings([]byte(tc.input))

			// Assert.
			if (err == nil) != tc.valid {
				t.Fatalf("parseGeneratedAtMappings() = %#v, %v; valid=%t", got, err, tc.valid)
			}
			if tc.name == "valid offset" && !reflect.DeepEqual([]string{got[0].Path, got[1].Path}, []string{"a.md", "b.md"}) {
				t.Fatalf("generated-at mappings not canonical: %#v", got)
			}
		})
	}
}
