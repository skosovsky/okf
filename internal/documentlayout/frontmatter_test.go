package documentlayout

import (
	"errors"
	"testing"
)

func TestSplitUsesOneExactOuterDelimiterContract(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		want    Frontmatter
		wantOK  bool
		wantErr error
	}{
		{name: "lf", data: "---\ntype: thing\n---\nbody", want: Frontmatter{YAMLStart: 4, YAMLEnd: 16, BodyStart: 20}, wantOK: true},
		{name: "crlf", data: "---\r\ntype: thing\r\n---\r\nbody", want: Frontmatter{YAMLStart: 5, YAMLEnd: 18, BodyStart: 23}, wantOK: true},
		{name: "leading space", data: " ---\ntype: thing\n---\nbody"},
		{name: "trailing space", data: "--- \ntype: thing\n---\nbody"},
		{name: "tab", data: "\t---\ntype: thing\n---\nbody"},
		{name: "lone carriage return", data: "---\rtype: thing\r---\rbody"},
		{name: "unterminated", data: "---\ntype: thing\n", want: Frontmatter{YAMLStart: 4, YAMLEnd: 16, BodyStart: 16}, wantOK: true, wantErr: ErrUnterminatedFrontmatter},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act.
			got, ok, err := Split([]byte(tt.data))

			// Assert.
			if got != tt.want || ok != tt.wantOK || !errors.Is(err, tt.wantErr) {
				t.Fatalf("Split() = %#v, %t, %v; want %#v, %t, %v", got, ok, err, tt.want, tt.wantOK, tt.wantErr)
			}
		})
	}
}
