package mcpserver

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcptest"
)

func TestMCPListAndReadProjectVerificationTrustStalenessAndNonNullArrays(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "index.md", ""+
		"---\n"+
		"okf_version: \"0.2\"\n"+
		"---\n"+
		"# Concepts\n"+
		"- [Unverified](unverified.md)\n"+
		"- [Machine bare](machine-bare.md)\n"+
		"- [Human bare](human-bare.md)\n"+
		"- [Human list](human-list.md)\n")
	writeTestFile(t, root, "unverified.md", ""+
		"---\n"+
		"type: Note\n"+
		"title: Unverified\n"+
		"---\n"+
		"Unverified.\n")
	writeTestFile(t, root, "machine-bare.md", ""+
		"---\n"+
		"type: Note\n"+
		"title: Machine bare\n"+
		"verified: {by: \"process:nightly\", at: 2026-07-28T00:00:00Z}\n"+
		"---\n"+
		"Machine-confirmed using the bare mapping form.\n")
	writeTestFile(t, root, "human-bare.md", ""+
		"---\n"+
		"type: Note\n"+
		"title: Human bare\n"+
		"verified: {by: \"human:reviewer\", at: 2026-07-28T01:00:00Z}\n"+
		"stale_after: 2026-07-29\n"+
		"---\n"+
		"Human-reviewed using the bare mapping form.\n")
	writeTestFile(t, root, "human-list.md", ""+
		"---\n"+
		"type: Note\n"+
		"title: Human list\n"+
		"verified:\n"+
		"  - {by: \"human:reviewer\", at: 2026-07-28T02:00:00Z}\n"+
		"stale_after: 2026-07-30\n"+
		"---\n"+
		"Human-reviewed using the list form.\n")
	srv, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer srv.Close()
	expected := map[string]struct {
		trust         string
		verifiedBy    string
		staleAfter    any
		stale         bool
		verifiedCount int
	}{
		"unverified": {trust: "unverified", verifiedCount: 0},
		"machine-bare": {
			trust: "machine-confirmed", verifiedBy: "process:nightly", verifiedCount: 1,
		},
		"human-bare": {
			trust: "human-reviewed", verifiedBy: "human:reviewer",
			staleAfter: "2026-07-29", stale: true, verifiedCount: 1,
		},
		"human-list": {
			trust: "human-reviewed", verifiedBy: "human:reviewer",
			staleAfter: "2026-07-30", stale: false, verifiedCount: 1,
		},
	}

	// Act.
	listed := callMCPTool(t, srv, "list_concepts", map[string]any{
		"bundle_path": root,
		"as_of":       "2026-07-29",
	})

	// Assert.
	if listed.IsError {
		t.Fatalf("list_concepts returned error: %s", resultText(t, listed))
	}
	listPayload, ok := listed.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("list structuredContent = %T, want map[string]any", listed.StructuredContent)
	}
	if got := listPayload["as_of"]; got != "2026-07-29" {
		t.Fatalf("list as_of = %#v, want 2026-07-29", got)
	}
	concepts, ok := listPayload["concepts"].([]any)
	if !ok || concepts == nil {
		t.Fatalf("list concepts = %#v, want non-null array", listPayload["concepts"])
	}
	if len(concepts) != len(expected) {
		t.Fatalf("list concepts count = %d, want %d", len(concepts), len(expected))
	}

	listByID := make(map[string]map[string]any, len(concepts))
	for _, raw := range concepts {
		concept, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("list concept = %T, want map[string]any", raw)
		}
		id, ok := concept["id"].(string)
		if !ok {
			t.Fatalf("list concept id = %#v, want string", concept["id"])
		}
		listByID[id] = concept
	}

	for id, want := range expected {
		t.Run(id, func(t *testing.T) {
			summary, ok := listByID[id]
			if !ok {
				t.Fatalf("list omitted concept %q", id)
			}
			if got := summary["trust"]; got != want.trust {
				t.Fatalf("list trust = %#v, want %q", got, want.trust)
			}
			if got := summary["stale_after"]; got != want.staleAfter {
				t.Fatalf("list stale_after = %#v, want %#v", got, want.staleAfter)
			}
			if got := summary["stale"]; got != want.stale {
				t.Fatalf("list stale = %#v, want %t", got, want.stale)
			}

			// Act.
			read := callMCPTool(t, srv, "read_concept", map[string]any{
				"bundle_path": root,
				"concept_id":  id,
			})

			// Assert.
			if read.IsError {
				t.Fatalf("read_concept returned error: %s", resultText(t, read))
			}
			readPayload, ok := read.StructuredContent.(map[string]any)
			if !ok {
				t.Fatalf("read structuredContent = %T, want map[string]any", read.StructuredContent)
			}
			projection, ok := readPayload["projection"].(map[string]any)
			if !ok {
				t.Fatalf("read projection = %T, want map[string]any", readPayload["projection"])
			}
			if got := projection["trust"]; got != want.trust {
				t.Fatalf("read trust = %#v, want %q", got, want.trust)
			}
			if got := projection["stale_after"]; got != want.staleAfter {
				t.Fatalf("read stale_after = %#v, want %#v", got, want.staleAfter)
			}
			for _, field := range []string{"tags", "sources", "verified"} {
				values, ok := projection[field].([]any)
				if !ok || values == nil {
					t.Fatalf("read projection %s = %#v, want non-null array", field, projection[field])
				}
			}
			verified := projection["verified"].([]any)
			if len(verified) != want.verifiedCount {
				t.Fatalf("read verified count = %d, want %d", len(verified), want.verifiedCount)
			}
			if want.verifiedCount == 0 {
				return
			}
			verification, ok := verified[0].(map[string]any)
			if !ok {
				t.Fatalf("read verified[0] = %T, want map[string]any", verified[0])
			}
			if got := verification["by"]; got != want.verifiedBy {
				t.Fatalf("read verified[0].by = %#v, want %q", got, want.verifiedBy)
			}
		})
	}
}
