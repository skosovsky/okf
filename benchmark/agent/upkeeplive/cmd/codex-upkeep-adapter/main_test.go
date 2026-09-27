package main

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/skosovsky/okf/benchmark/agent"
	"github.com/skosovsky/okf/benchmark/agent/upkeeplive"
	"github.com/skosovsky/okf/upkeep"
)

func TestRoleSelectionAndFrozenPromptHashes(t *testing.T) {
	// Arrange.
	w := agent.UpkeepWriterRequest{CaseID: "case"}
	c := agent.UpkeepConsumerRequest{CaseID: "case"}
	cases := []struct {
		req           upkeeplive.Request
		model, prompt string
	}{
		{upkeeplive.Request{Role: "writer", Writer: &w}, "writer-v1", writerPrompt},
		{upkeeplive.Request{Role: "writer_revision", Writer: &w, Draft: &upkeeplive.Draft{}, CheckerInitial: &upkeep.Result{Status: "needs_review"}}, "writer-v1", writerPrompt},
		{upkeeplive.Request{Role: "consumer", Consumer: &c}, "consumer-v1", consumerPrompt},
	}
	// Act and assert.
	for _, tc := range cases {
		model, prompt, schema, err := selectRole(tc.req, "writer-v1", "consumer-v1")
		if err != nil || model != tc.model || prompt != tc.prompt || schema == "" {
			t.Fatalf("role=%s model=%s err=%v", tc.req.Role, model, err)
		}
		hash := sha256.Sum256([]byte(prompt))
		if len(hex.EncodeToString(hash[:])) != 64 {
			t.Fatal("bad prompt hash")
		}
	}
	if _, _, _, err := selectRole(upkeeplive.Request{Role: "consumer", Consumer: &c, Writer: &w}, "w", "c"); err == nil {
		t.Fatal("consumer accepted writer data")
	}
}

func TestConsumerPayloadExcludesHarnessHints(t *testing.T) {
	// Arrange: ID contains a strong answer hint, which the model must not see.
	req := upkeeplive.Request{Role: "consumer", Repeat: 3, Model: "model", PromptSHA256: strings.Repeat("a", 64), Consumer: &agent.UpkeepConsumerRequest{CaseID: "upkeep-truncated", Question: "Did rollout complete?", Artifacts: []agent.Artifact{{ID: "handler", Path: "handler.md", Content: "Evidence incomplete."}}}}
	// Act.
	b, err := modelPayload(req)
	if err != nil {
		t.Fatal(err)
	}
	// Assert.
	for _, leak := range []string{"case_id", "upkeep-truncated", "repeat", "role", "model", "prompt_sha256", "checker", "expected", "target_code"} {
		if strings.Contains(string(b), leak) {
			t.Fatalf("consumer leak %q: %s", leak, b)
		}
	}
	if !strings.Contains(string(b), "Did rollout complete?") || !strings.Contains(string(b), "Evidence incomplete.") {
		t.Fatalf("consumer lost evidence: %s", b)
	}
}

func TestReadEventsUsesRealCodexThreadID(t *testing.T) {
	var resp upkeeplive.Response
	stream := []byte("{\"type\":\"thread.started\",\"thread_id\":\"thread-real-1\"}\n{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":12,\"output_tokens\":3}}\n")
	if err := readEvents(stream, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.SessionID != "thread-real-1" || resp.InputTokens != 12 || resp.OutputTokens != 3 {
		t.Fatalf("response=%+v", resp)
	}
}
