package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/skosovsky/okf/benchmark/agent"
)

type commandItem struct {
	Type             string `json:"type"`
	Status           string `json:"status"`
	Command          string `json:"command"`
	AggregatedOutput string `json:"aggregated_output"`
	ExitCode         *int   `json:"exit_code"`
}

func directToolInstruction(arm, goCLI string, diagnostic bool) string {
	read := "Read the listed files with read-only shell commands before answering."
	if diagnostic {
		read = "Before answering, run one simple `cat <path>` shell command for each listed evidence file; do not combine commands."
	}
	if arm != agent.Treatment {
		return read
	}
	return read + " Use the Go OKF CLI at " + goCLI + " to run `validate --path . --json --temporal-profile instant-0b87c52` and `parse <concept-file> --json` on concept files. Do not answer before invoking the CLI."
}

func eventMetadata(eventType string, raw json.RawMessage, artifacts []agent.Artifact, goCLI string, conceptPaths map[string]bool) agent.EventMetadata {
	m := agent.EventMetadata{Event: safeEventLabel(eventType)}
	var item commandItem
	if json.Unmarshal(raw, &item) != nil {
		return m
	}
	m.ItemType = safeItemLabel(item.Type)
	m.Status = safeStatusLabel(item.Status)
	m.ExitCode = item.ExitCode
	if item.Type != "command_execution" || item.Command == "" {
		return m
	}
	hash := sha256.Sum256([]byte(item.Command))
	m.CommandSHA256 = hex.EncodeToString(hash[:])
	m.CommandKind = "other"
	if len(catArtifactPaths(item.Command, artifacts)) > 0 {
		m.CommandKind = "artifact_read_attempt"
	}
	if successfulGoCLICommand(raw, goCLI, conceptPaths) {
		m.CommandKind = "go_cli_success"
	}
	return m
}

func safeEventLabel(s string) string {
	switch s {
	case "thread.started", "turn.started", "turn.completed", "turn.failed", "item.started", "item.updated", "item.completed", "error":
		return s
	default:
		return "other"
	}
}

func safeItemLabel(s string) string {
	switch s {
	case "command_execution", "mcp_tool_call", "agent_message", "reasoning", "web_search", "file_change", "plan_update":
		return s
	case "":
		return ""
	default:
		return "other"
	}
}

func safeStatusLabel(s string) string {
	switch s {
	case "in_progress", "completed", "failed", "declined":
		return s
	case "":
		return ""
	default:
		return "other"
	}
}

func confirmedReadArtifacts(raw json.RawMessage, artifacts []agent.Artifact) []string {
	var item commandItem
	if json.Unmarshal(raw, &item) != nil || item.Type != "command_execution" || item.ExitCode == nil || *item.ExitCode != 0 {
		return nil
	}
	paths := catArtifactPaths(item.Command, artifacts)
	var ids []string
	for _, a := range artifacts {
		if paths[a.Path] && item.AggregatedOutput == a.Content {
			ids = append(ids, a.ID)
		}
	}
	return ids
}

// catArtifactPaths recognizes only a simple cat of supplied paths, optionally
// behind the shell wrapper shown in Codex command events. Compound commands,
// redirections, unknown flags and unknown paths fail closed.
func catArtifactPaths(command string, artifacts []agent.Artifact) map[string]bool {
	argv := simpleCommandArgv(command)
	if len(argv) < 2 || (argv[0] != "cat" && argv[0] != "/bin/cat") {
		return nil
	}
	argv = argv[1:]
	if len(argv) > 0 && argv[0] == "--" {
		argv = argv[1:]
	}
	if len(argv) != 1 {
		return nil
	}
	allowed := make(map[string]bool, len(artifacts))
	for _, a := range artifacts {
		allowed[a.Path] = true
	}
	read := make(map[string]bool, len(argv))
	for _, path := range argv {
		if !allowed[path] {
			return nil
		}
		read[path] = true
	}
	return read
}

func simpleCommandArgv(command string) []string {
	for _, shell := range []string{"bash", "/bin/bash", "sh", "/bin/sh", "zsh", "/bin/zsh"} {
		prefix := shell + " -lc "
		if strings.HasPrefix(command, prefix) {
			inner := strings.TrimPrefix(command, prefix)
			if len(inner) < 2 || !((inner[0] == '\'' && inner[len(inner)-1] == '\'') || (inner[0] == '"' && inner[len(inner)-1] == '"')) {
				return nil
			}
			command = inner[1 : len(inner)-1]
			break
		}
	}
	if strings.ContainsAny(command, "\n\r;&|><`$()\\\"'") {
		return nil
	}
	return strings.Fields(command)
}

func containsString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
