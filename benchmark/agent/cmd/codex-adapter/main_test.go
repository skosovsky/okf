package main

import (
	"encoding/json"
	"testing"
)

func TestSuccessfulGoCLICommand(t *testing.T) {
	// Arrange: only a completed command event with a successful direct binary
	// invocation can prove toolkit use. Textual mentions and shell compounds cannot.
	const binary = "/private/tmp/okf-eval-cli"
	conceptPaths := map[string]bool{"current.md": true}
	tests := []struct {
		name string
		item string
		want bool
	}{
		{"validate", `{"type":"command_execution","command":"/private/tmp/okf-eval-cli validate --path . --json --temporal-profile instant-0b87c52","exit_code":0}`, true},
		{"parse", `{"type":"command_execution","command":"/private/tmp/okf-eval-cli parse current.md --json","exit_code":0}`, true},
		{"validate help", `{"type":"command_execution","command":"/private/tmp/okf-eval-cli validate --help","exit_code":0}`, false},
		{"parse help", `{"type":"command_execution","command":"/private/tmp/okf-eval-cli parse --help","exit_code":0}`, false},
		{"validate wrong path", `{"type":"command_execution","command":"/private/tmp/okf-eval-cli validate --path /tmp/empty --json --temporal-profile instant-0b87c52","exit_code":0}`, false},
		{"parse unrelated file", `{"type":"command_execution","command":"/private/tmp/okf-eval-cli parse other.md --json","exit_code":0}`, false},
		{"echo path", `{"type":"command_execution","command":"echo /private/tmp/okf-eval-cli validate","exit_code":0}`, false},
		{"echo words", `{"type":"command_execution","command":"echo okf validate","exit_code":0}`, false},
		{"comment", `{"type":"command_execution","command":"# /private/tmp/okf-eval-cli validate","exit_code":0}`, false},
		{"compound", `{"type":"command_execution","command":"/private/tmp/okf-eval-cli validate --path .; echo done","exit_code":0}`, false},
		{"failed", `{"type":"command_execution","command":"/private/tmp/okf-eval-cli validate","exit_code":1}`, false},
		{"missing exit", `{"type":"command_execution","command":"/private/tmp/okf-eval-cli validate"}`, false},
		{"wrong type", `{"type":"mcp_tool_call","command":"/private/tmp/okf-eval-cli validate","exit_code":0}`, false},
		{"other subcommand", `{"type":"command_execution","command":"/private/tmp/okf-eval-cli help","exit_code":0}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act.
			got := successfulGoCLICommand(json.RawMessage(tt.item), binary, conceptPaths)

			// Assert.
			if got != tt.want {
				t.Fatalf("successfulGoCLICommand() = %t, want %t", got, tt.want)
			}
		})
	}
}
