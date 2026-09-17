package mcpserver

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/mcptest"
	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/mutation"
	"github.com/skosovsky/okf/store"
)

func TestMCPMigrationProofRelationRefsPreserveStructuralIdentityThroughJSON(t *testing.T) {
	// Arrange.
	escapedRoot, err := bundle.ParseRelationRef(`a\#x`)
	if err != nil {
		t.Fatalf("ParseRelationRef(escaped root) error = %v", err)
	}
	fragment, err := bundle.ParseRelationRef(`a#x`)
	if err != nil {
		t.Fatalf("ParseRelationRef(fragment) error = %v", err)
	}
	upper, err := bundle.ParseRelationRef("aZ")
	if err != nil {
		t.Fatalf("ParseRelationRef(upper root) error = %v", err)
	}
	ordinary, err := bundle.ParseRelationRef("metrics/revenue")
	if err != nil {
		t.Fatalf("ParseRelationRef(ordinary) error = %v", err)
	}
	revision, err := store.ParseRevision("sha256:" + strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("ParseRevision() error = %v", err)
	}
	proof := mutation.MigrationPlanProof{
		FormatVersion:    mutation.MigrationPlanProofFormatVersion,
		RequestDigest:    "sha256:" + strings.Repeat("b", 64),
		ResolutionDigest: "sha256:" + strings.Repeat("c", 64),
		BaseRevision:     revision,
		ResultRevision:   revision,
		Reads:            []store.Read{},
		Writes:           []mutation.MigrationPlanWrite{},
		Deletes:          []string{},
		Renames:          []store.Rename{},
		AffectedRefs:     []bundle.RelationRef{fragment, escapedRoot, upper},
		ReverseImpact:    []bundle.RelationRef{fragment, escapedRoot, upper},
		ChangedFiles:     []store.FileChange{},
		ChangedRefs:      []bundle.RelationRef{fragment, escapedRoot, upper},
	}

	// Act.
	dto, err := migrationPlanProofDTOFromDomainContext(t.Context(), proof)
	if err != nil {
		t.Fatalf("migrationPlanProofDTOFromDomainContext() error = %v", err)
	}
	wire, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("Marshal(proof DTO) error = %v", err)
	}
	var decodedDTO migrationPlanProofDTO
	if err := json.Unmarshal(wire, &decodedDTO); err != nil {
		t.Fatalf("Unmarshal(proof DTO) error = %v", err)
	}
	decoded, result := migrationPlanProofFromDTO(&decodedDTO)

	// Assert.
	if result != nil {
		t.Fatalf("migrationPlanProofFromDTO() error = %s", resultText(t, result))
	}
	if !reflect.DeepEqual(decoded.AffectedRefs, proof.AffectedRefs) ||
		!reflect.DeepEqual(decoded.ReverseImpact, proof.ReverseImpact) ||
		!reflect.DeepEqual(decoded.ChangedRefs, proof.ChangedRefs) {
		t.Fatalf("proof relation refs lost identity:\nwire=%s\ndecoded=%#v\nwant=%#v", wire, decoded, proof)
	}
	if escapedRoot.ID.String() == fragment.ID.String() ||
		escapedRoot.Fragment != "" ||
		fragment.Fragment != "x" {
		t.Fatalf("escaped/delimited refs collapsed: escaped=%#v delimited=%#v",
			escapedRoot, fragment)
	}
	if !bytes.Contains(wire, []byte(`"affected_refs":["a#x","a\\#x","aZ"]`)) {
		t.Fatalf("affected_refs JSON escaping = %s", wire)
	}
	if !bytes.Contains(wire, []byte(`"reverse_impact":["a#x","a\\#x","aZ"]`)) {
		t.Fatalf("reverse_impact JSON escaping = %s", wire)
	}
	wantStructural := []string{"a#x", `a\#x`, "aZ"}
	if !reflect.DeepEqual(dto.AffectedRefs, wantStructural) ||
		!reflect.DeepEqual(dto.ReverseImpact, wantStructural) ||
		!reflect.DeepEqual(dto.ChangedRefs, wantStructural) {
		t.Fatalf("structural proof DTO refs changed: %#v", dto)
	}
	ordinaryDTO, err := migrationPlanProofDTOFromDomainContext(t.Context(), mutation.MigrationPlanProof{
		AffectedRefs:  []bundle.RelationRef{ordinary},
		ReverseImpact: []bundle.RelationRef{ordinary},
		ChangedRefs:   []bundle.RelationRef{ordinary},
	})
	if err != nil {
		t.Fatalf("migrationPlanProofDTOFromDomainContext(ordinary) error = %v", err)
	}
	wantOrdinary := []string{"metrics/revenue"}
	if !reflect.DeepEqual(ordinaryDTO.AffectedRefs, wantOrdinary) ||
		!reflect.DeepEqual(ordinaryDTO.ReverseImpact, wantOrdinary) ||
		!reflect.DeepEqual(ordinaryDTO.ChangedRefs, wantOrdinary) {
		t.Fatalf("ordinary proof DTO refs changed: %#v", ordinaryDTO)
	}
	reencodedDTO, err := migrationPlanProofDTOFromDomainContext(t.Context(), decoded)
	if err != nil {
		t.Fatalf("migrationPlanProofDTOFromDomainContext(decoded) error = %v", err)
	}
	reencoded, err := json.Marshal(reencodedDTO)
	if err != nil {
		t.Fatalf("Marshal(decoded proof DTO) error = %v", err)
	}
	if !bytes.Equal(reencoded, wire) {
		t.Fatalf("proof JSON round trip changed bytes:\nfirst=%s\nsecond=%s", wire, reencoded)
	}
}

func TestMCPMigrationProofStructuralRefOrderApplyReplayAndTamper(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "index.md", "---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [Escaped](a%23x.md)\n- [Fragment](a.md#x)\n- [Upper](aZ.md)\n")
	writeTestFile(t, root, "a#x.md", "---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n---\n\nEscaped.\n")
	writeTestFile(t, root, "a.md", "---\ntype: Knowledge\ntimestamp: 2026-06-26T09:00:00Z\nparts:\n  - id: x\n---\n\nFragment owner.\n")
	writeTestFile(t, root, "aZ.md", "---\ntype: Knowledge\ntimestamp: 2026-06-27T09:00:00Z\n---\n\nUpper.\n")
	srv, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer srv.Close()
	arguments := map[string]any{
		"bundle_path": root, "from": "auto", "generated_by": "process:migration",
		"timestamp_policy": "preserve", "citation_mappings": []any{},
	}
	preview := callMCPTool(t, srv, "preview_v02_migration", arguments)
	if preview.IsError {
		t.Fatalf("migration preview returned error: %s", resultText(t, preview))
	}
	payload := preview.StructuredContent.(map[string]any)
	proof := payload["proof"].(map[string]any)
	changedRefs := proof["changed_refs"].([]any)
	escapedIndex := relationRefWireIndex(changedRefs, `a\#x`)
	fragmentIndex := relationRefWireIndex(changedRefs, "a#x")
	upperIndex := relationRefWireIndex(changedRefs, "aZ")
	if fragmentIndex < 0 || escapedIndex < 0 || upperIndex < 0 ||
		fragmentIndex >= escapedIndex || escapedIndex >= upperIndex {
		t.Fatalf("preview changed_refs are not in core structural order: %#v", changedRefs)
	}
	apply := cloneArguments(arguments)
	apply["expected_source"] = payload["source"]
	apply["proof"] = proof
	apply["expected_plan_digest"] = payload["plan_digest"]

	permutedApply := cloneArguments(apply)
	permutedProof := deepCloneMap(t, proof)
	permutedRefs := permutedProof["changed_refs"].([]any)
	permutedRefs[escapedIndex], permutedRefs[upperIndex] =
		permutedRefs[upperIndex], permutedRefs[escapedIndex]
	permutedApply["proof"] = permutedProof

	noncanonicalApply := cloneArguments(apply)
	noncanonicalProof := deepCloneMap(t, proof)
	noncanonicalRefs := noncanonicalProof["changed_refs"].([]any)
	noncanonicalRefs[escapedIndex] = `a\\#x`
	noncanonicalApply["proof"] = noncanonicalProof
	beforeRejected := protocolTreeSnapshot(t, root)

	// Act.
	permuted := callMCPTool(t, srv, "apply_v02_migration", permutedApply)
	noncanonical := callMCPTool(t, srv, "apply_v02_migration", noncanonicalApply)
	afterRejected := protocolTreeSnapshot(t, root)
	applied := callMCPTool(t, srv, "apply_v02_migration", apply)
	replayed := callMCPTool(t, srv, "apply_v02_migration", apply)

	// Assert.
	assertMCPErrorCode(t, permuted, "plan_mismatch")
	assertMCPErrorCode(t, noncanonical, "invalid_request")
	if !reflect.DeepEqual(afterRejected, beforeRejected) {
		t.Fatalf("rejected proof changed bundle tree:\nbefore=%#v\nafter=%#v", beforeRejected, afterRejected)
	}
	if applied.IsError {
		t.Fatalf("migration apply returned error: %s", resultText(t, applied))
	}
	if replayed.IsError {
		t.Fatalf("migration replay returned error: %s", resultText(t, replayed))
	}
	appliedPayload := applied.StructuredContent.(map[string]any)
	replayedPayload := replayed.StructuredContent.(map[string]any)
	if appliedPayload["status"] != "applied" ||
		appliedPayload["plan_digest"] != payload["plan_digest"] ||
		!reflect.DeepEqual(replayedPayload, appliedPayload) {
		t.Fatalf("apply/replay payloads = %#v / %#v", appliedPayload, replayedPayload)
	}
}

func relationRefWireIndex(values []any, target string) int {
	for index, value := range values {
		if value == target {
			return index
		}
	}
	return -1
}

func assertMCPErrorCode(t *testing.T, result *mcp.CallToolResult, want string) {
	t.Helper()
	if !result.IsError {
		t.Fatalf("call returned success, want %s: %#v", want, result.StructuredContent)
	}
	payload, ok := result.StructuredContent.(map[string]any)
	if !ok || payload["code"] != want {
		t.Fatalf("error envelope = %#v, want code %q", result.StructuredContent, want)
	}
}

func TestMCPMigrationProofRelationRefsRejectStrayAndNonCanonicalEscapes(t *testing.T) {
	// Arrange.
	revision := "sha256:" + strings.Repeat("a", 64)
	base := testMigrationPlanProofDTO(revision, revision, "sha256:"+strings.Repeat("b", 64))
	tests := []struct {
		name string
		raw  string
	}{
		{name: "stray escape", raw: `source\part`},
		{name: "noncanonical doubled escape", raw: `source\\#part`},
	}

	// Act and assert.
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, field := range []string{"affected_refs", "reverse_impact", "changed_refs"} {
				t.Run(field, func(t *testing.T) {
					dto := *base
					switch field {
					case "affected_refs":
						dto.AffectedRefs = []string{test.raw}
					case "reverse_impact":
						dto.ReverseImpact = []string{test.raw}
					case "changed_refs":
						dto.ChangedRefs = []string{test.raw}
					}

					_, result := migrationPlanProofFromDTO(&dto)

					if result == nil || !result.IsError {
						t.Fatalf("%s accepted noncanonical relation ref %q", field, test.raw)
					}
					envelope := result.StructuredContent.(errorEnvelope)
					if envelope.Code != "invalid_request" {
						t.Fatalf("%s error envelope = %#v", field, envelope)
					}
				})
			}
		})
	}
}
