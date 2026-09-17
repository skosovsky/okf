package markdownowner

import (
	"context"
	"testing"
)

func TestReservedSectionsRequireExactRawTopLevelATXHeading(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		line  string
		exact bool
	}{
		{name: "exact LF", line: "# %s\n", exact: true},
		{name: "exact CRLF", line: "# %s\r\n", exact: true},
		{name: "setext", line: "%s\n===\n"},
		{name: "styled", line: "# *%s*\n"},
		{name: "closing hash", line: "# %s #\n"},
		{name: "multiple spaces", line: "#  %s\n"},
		{name: "leading space", line: " # %s\n"},
		{name: "tab separator", line: "#\t%s\n"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			computation := []byte(formatHeading(tt.line, "Computation") + "```\nSELECT 1;\n```\n")
			citations := []byte(formatHeading(tt.line, "Citations") + "- https://example.test/source\n")

			// Act.
			inspection := InspectComputation(string(computation))
			projection, err := CollectCitationSectionProjection(context.Background(), citations)

			// Assert.
			if err != nil {
				t.Fatal(err)
			}
			if got := inspection.State == ComputationInline; got != tt.exact {
				t.Fatalf("InspectComputation() inline = %v, want %v; %#v", got, tt.exact, inspection)
			}
			if projection.Found != tt.exact {
				t.Fatalf("CollectCitationSectionProjection().Found = %v, want %v; %#v", projection.Found, tt.exact, projection)
			}
		})
	}
}

func formatHeading(format, name string) string {
	for index := 0; index+1 < len(format); index++ {
		if format[index] == '%' && format[index+1] == 's' {
			return format[:index] + name + format[index+2:]
		}
	}
	return format
}
