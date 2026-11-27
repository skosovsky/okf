package bundle

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOKF03DocumentSessionComputationUsesCapturedFiles(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeFile(t, root, "index.md", "---\nokf_version: '0.3'\n---\n")
	writeFile(t, root, "refs/query.sql", "SELECT 1")
	writeFile(t, root, "refs/check.py", "Attester fixture")
	writeFile(t, root, "metrics/rule.md", "---\ntype: Attested Computation\nruntime: postgres\n"+
		"computation: ../refs/query.sql\nattester: {resource: /refs/check.py}\n---\n")
	target := filepath.Join(root, "metrics", "rule.md")
	session, err := OpenDocumentSessionContext(t.Context(), target, OKFVersion03)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := os.Remove(filepath.Join(root, "refs", "query.sql")); err != nil {
		t.Fatal(err)
	}

	// Act.
	state, err := session.ComputationStateContext(t.Context())

	// Assert.
	if err != nil || !state.Attestable || !state.Valid || state.ComputationPath != "refs/query.sql" {
		t.Fatalf("captured computation = %+v, %v", state, err)
	}
}

func TestOKF03ComputationResolvesFirstFenceAndFilePrecedence(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeFile(t, root, "index.md", "---\nokf_version: '0.3'\n---\n")
	writeFile(t, root, "refs/check.py", "Attester fixture")
	writeFile(t, root, "refs/query.sql", "SELECT 3")
	base := "---\ntype: Attested Computation\nruntime: postgres\nattester: {resource: /refs/check.py}\nparameters: {name: year, type: integer}\n"
	body := "### Examples\n\n```md\n## Computation\n```\n\n## Computation\n\n" +
		"### Binding\n\n```sql\nSELECT 1\n```\n\n```sql\nSELECT 2\n```\n\n## Next\n\n```sql\nSELECT 4\n```\n"
	writeFile(t, root, "inline.md", base+"---\n\n"+body)
	writeFile(t, root, "file.md", base+"computation: /refs/query.sql\n---\n\n"+body)
	writeFile(t, root, "missing.md", base+"computation: /refs/missing.sql\n---\n\n"+body)
	writeFile(t, root, "empty-section.md", base+"---\n\n## Computation\n\n## Other\n\n```sql\nSELECT 0\n```\n")
	writeFile(t, root, "no-attester.md", "---\ntype: Attested Computation\nruntime: postgres\n---\n\n"+body)
	b, err := LoadBundle(root)
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		id, payload, reason string
		mode                ComputationMode
		attestable          bool
	}{
		{"inline", "SELECT 1\n", "", ComputationModeInline, true},
		{"file", "refs/query.sql", "", ComputationModeFile, true},
		{"missing", "", "missing_computation_file", ComputationModeFile, false},
		{"empty-section", "", "missing_inline_computation", ComputationModeAbsent, false},
		{"no-attester", "SELECT 1\n", "missing_attester", ComputationModeInline, false},
	} {
		t.Run(tt.id, func(t *testing.T) {
			id, err := ParseConceptID(tt.id)
			if err != nil {
				t.Fatal(err)
			}
			concept, ok := b.Get(id)
			if !ok {
				t.Fatal("concept missing")
			}

			// Act.
			state, err := b.ComputationStateContext(t.Context(), concept)

			// Assert.
			if err != nil || state.Mode != tt.mode || state.Inline+state.ComputationPath != tt.payload ||
				state.Attestable != tt.attestable || state.UnavailableReason != tt.reason {
				t.Fatalf("state=%+v err=%v", state, err)
			}
			if tt.id == "inline" && (!state.Multiple || len(state.Contract.Parameters) != 1 || state.Contract.Parameters[0].Required) {
				t.Fatalf("parameters or fence detection: %+v", state)
			}
		})
	}
}
