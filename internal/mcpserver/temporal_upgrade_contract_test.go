package mcpserver

import (
	"testing"

	"github.com/skosovsky/okf/bundle"
)

func TestTemporalUpgradeMappingSchemaMatchesOffsetBoundary(t *testing.T) {
	// Arrange.
	cases := []struct {
		value string
		valid bool
	}{
		{"2026-09-26T12:34:56Z", true},
		{"2026-09-26T12:34:56.1200+07:00", true},
		{"2026-09-26T12:34:56", false},
		{"2026-09-26T12:34:56-00:00", false},
		{"2026-09-26T12:34:56+24:00", false},
		{"2026-09-26T12:34:60Z", false},
		{"2026-09-26T12:34:56,5Z", false},
		{"2026-09-26t12:34:56Z", false},
		{"2026-09-26T12:34:56z", false},
	}
	for _, contract := range []string{"preview_temporal_upgrade.input", "apply_temporal_upgrade.input"} {
		for _, test := range cases {
			t.Run(contract+"/"+test.value, func(t *testing.T) {
				// Act.
				input := map[string]any{"bundle_path": "/tmp/bundle", "id": "upgrade", "actor": "human:reviewer", "mappings": []any{map[string]any{"concept": "a", "path": "stale_after", "from": "2026-09-26", "to": test.value}}}
				if contract == "apply_temporal_upgrade.input" {
					input["base_revision"] = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
					input["plan_digest"] = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
				}
				schemaErr := validateContract(contract, input)
				_, runtimeErr := bundle.ParseOffsetDateTime(test.value)
				// Assert.
				if (schemaErr == nil) != test.valid || (runtimeErr == nil) != test.valid {
					t.Fatalf("schema/runtime = %v/%v, want valid %t", schemaErr, runtimeErr, test.valid)
				}
			})
		}
	}
}
