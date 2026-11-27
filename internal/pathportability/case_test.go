package pathportability

import (
	"errors"
	"testing"
)

func TestCheckNewCaseAliases(t *testing.T) {
	tests := []struct {
		name           string
		base, proposed []string
		path, existing string
	}{
		{"existing alias may be edited", []string{"Rule.md", "rule.md"}, []string{"rule.md"}, "", ""},
		{"directory spelling", []string{"rules/a.md"}, []string{"Rules/b.md"}, "Rules", "rules"},
		{"existing directory alias may receive a new path", []string{"Rules/a.md", "rules/b.md"}, []string{"rules/c.md"}, "", ""},
		{"new names in one publication", nil, []string{"A.md", "a.md"}, "a.md", "A.md"},
		{"folded component expands", []string{"Straße/a.md"}, []string{"STRASSE/b.md"}, "STRASSE", "Straße"},
		{"repeated directory", nil, []string{"rules/a.md", "rules/b.md"}, "", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := CheckNew(t.Context(), test.base, test.proposed)
			if test.path == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var collision *CaseCollision
			if !errors.As(err, &collision) || collision.Path != test.path || collision.Existing != test.existing {
				t.Fatalf("collision = %v, want %q against %q", err, test.path, test.existing)
			}
		})
	}
}
