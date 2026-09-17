package migrationpath

import "testing"

func TestIsMarkdownDocument_ClassifiesEverySafeMarkdownRole(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{path: "index.md", want: true},
		{path: "log.md", want: true},
		{path: "nested/index.md", want: true},
		{path: "nested/log.md", want: true},
		{path: "orphan.md", want: true},
		{path: "asset.sql", want: false},
		{path: "../escape.md", want: false},
		{path: ".okf/private.md", want: false},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			if got := IsMarkdownDocument(test.path); got != test.want {
				t.Fatalf("IsMarkdownDocument(%q) = %t, want %t", test.path, got, test.want)
			}
		})
	}
}
