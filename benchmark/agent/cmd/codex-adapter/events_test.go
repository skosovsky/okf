package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/skosovsky/okf/benchmark/agent"
)

func TestConfirmedReadArtifacts(t *testing.T) {
	// Arrange.
	arts := []agent.Artifact{{ID: "historical", Path: "docs/legacy-support.md", Content: "old 0.1"}, {ID: "current", Path: "bundle/doc.go", Content: "current 0.2"}}
	tests := []struct {
		name string
		item string
		want []string
	}{
		{"direct cat", `{"type":"command_execution","command":"cat bundle/doc.go","aggregated_output":"current 0.2","exit_code":0}`, []string{"current"}},
		{"shell wrapper", `{"type":"command_execution","command":"bash -lc 'cat bundle/doc.go'","aggregated_output":"current 0.2","exit_code":0}`, []string{"current"}},
		{"zsh wrapper", `{"type":"command_execution","command":"/bin/zsh -lc 'cat bundle/doc.go'","aggregated_output":"current 0.2","exit_code":0}`, []string{"current"}},
		{"unquoted shell wrapper", `{"type":"command_execution","command":"bash -lc cat bundle/doc.go","aggregated_output":"current 0.2","exit_code":0}`, nil},
		{"no source output", `{"type":"command_execution","command":"cat bundle/doc.go","aggregated_output":"other","exit_code":0}`, nil},
		{"source plus extra", `{"type":"command_execution","command":"/bin/zsh -lc 'cat bundle/doc.go'","aggregated_output":"current 0.2\nextra","exit_code":0}`, nil},
		{"source embedded", `{"type":"command_execution","command":"/bin/zsh -lc 'cat bundle/doc.go'","aggregated_output":"prefix current 0.2 suffix","exit_code":0}`, nil},
		{"failed", `{"type":"command_execution","command":"cat bundle/doc.go","aggregated_output":"current 0.2","exit_code":1}`, nil},
		{"unknown path", `{"type":"command_execution","command":"cat other.go","aggregated_output":"current 0.2","exit_code":0}`, nil},
		{"compound", `{"type":"command_execution","command":"cat bundle/doc.go; echo x","aggregated_output":"current 0.2","exit_code":0}`, nil},
		{"wrapped pipeline", `{"type":"command_execution","command":"/bin/zsh -lc 'cat bundle/doc.go | head'","aggregated_output":"current 0.2","exit_code":0}`, nil},
		{"wrapped comment", `{"type":"command_execution","command":"/bin/zsh -lc 'cat bundle/doc.go # bypass'","aggregated_output":"current 0.2","exit_code":0}`, nil},
		{"multiple files", `{"type":"command_execution","command":"/bin/zsh -lc 'cat docs/legacy-support.md bundle/doc.go'","aggregated_output":"old 0.1current 0.2","exit_code":0}`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act.
			got := confirmedReadArtifacts(json.RawMessage(tt.item), arts)

			// Assert.
			if len(got) != len(tt.want) || (len(got) > 0 && got[0] != tt.want[0]) {
				t.Fatalf("confirmedReadArtifacts = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEventMetadataExcludesSource(t *testing.T) {
	// Arrange.
	const source = "private source payload"
	item := json.RawMessage(`{"type":"command_execution","status":"completed","command":"/bin/zsh -lc 'cat current.md'","aggregated_output":"private source payload","exit_code":0}`)
	arts := []agent.Artifact{{ID: "current", Path: "current.md", Content: source}}

	// Act.
	meta := eventMetadata("item.completed", item, arts, "", nil)
	raw, err := json.Marshal(meta)

	// Assert.
	if err != nil || strings.Contains(string(raw), source) || strings.Contains(string(raw), "current.md") || meta.CommandKind != "artifact_read_attempt" {
		t.Fatalf("unsafe event metadata: %s, error %v", raw, err)
	}
}

func TestEventMetadataRecognizesSuccessfulZshGoCLI(t *testing.T) {
	// Arrange.
	item := json.RawMessage(`{"type":"command_execution","status":"completed","command":"/bin/zsh -lc '/tmp/okf parse current.md --json'","aggregated_output":"private parsed source","exit_code":0}`)

	// Act.
	meta := eventMetadata("item.completed", item, nil, "/tmp/okf", map[string]bool{"current.md": true})
	raw, err := json.Marshal(meta)

	// Assert.
	if err != nil || meta.CommandKind != "go_cli_success" || strings.Contains(string(raw), "private parsed source") {
		t.Fatalf("unsafe or unrecognized CLI event: %s, error %v", raw, err)
	}
}

func TestDiagnosticToolPromptParity(t *testing.T) {
	// Arrange and act.
	control := directToolInstruction(agent.Control, "/tmp/okf", true)
	treatment := directToolInstruction(agent.Treatment, "/tmp/okf", true)

	// Assert: both arms require exactly the same file-reading action; only
	// treatment adds the actual Go CLI intervention.
	if !strings.HasPrefix(treatment, control) || !strings.Contains(control, "cat <path>") || strings.Contains(control, "/tmp/okf") || !strings.Contains(treatment, "/tmp/okf") {
		t.Fatalf("diagnostic prompt/tool parity lost: control=%q treatment=%q", control, treatment)
	}
}
