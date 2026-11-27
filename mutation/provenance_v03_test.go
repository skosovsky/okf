package mutation

import (
	"bytes"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
)

func TestOKF03ProvenanceTreatsEmptyFamiliesAsAbsent(t *testing.T) {
	id := ref(t, "note").ID
	review := store.Verification{By: "human:reviewer", At: "2026-11-27"}
	generate, err := store.NewSetGeneratedV03(id, store.Generation{By: "urn:okf:publisher", At: review.At})
	if err != nil {
		t.Fatal(err)
	}
	ensure, err := store.NewEnsureVerificationV03(id, review)
	if err != nil {
		t.Fatal(err)
	}
	remove, err := store.NewRemoveVerificationV03(id, review)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", " null", " ~", " ''", " []"} {
		t.Run(value, func(t *testing.T) {
			source := memorySource{
				"index.md": []byte("---\nokf_version: '0.3'\n---\n# Notes\n\n- [Note](note.md)\n"),
				"note.md":  []byte("---\ntype: Note\ngenerated:" + value + "\nverified:" + value + "\nexternal: {owner: keep} # retained\n---\n\nBody.\n"),
			}
			change := store.ChangeSet{Version: store.ChangeSetFormatVersion, ID: "provenance", Actor: "test", BaseRevision: revisionFor(t, source), Operations: []store.Operation{remove}}
			result, err := Plan(t.Context(), source, change)
			if err != nil || len(result.Preview.Writes) != 0 {
				t.Fatalf("removing absent verification: %+v, %v", result.Preview.Writes, err)
			}
			change.Operations = []store.Operation{generate, ensure}
			result, err = Plan(t.Context(), source, change)
			if err != nil {
				t.Fatal(err)
			}
			written, err := result.Staged.ReadFile(t.Context(), "note.md")
			if err != nil || !bytes.Contains(written, []byte("external: {owner: keep} # retained\n")) {
				t.Fatalf("written = %q, %v", written, err)
			}
			doc, err := bundle.ParseDocument(string(written))
			if err != nil {
				t.Fatal(err)
			}
			trust, err := doc.Frontmatter.TrustStateV03Context(t.Context())
			if err != nil || trust.Tier != bundle.TrustHumanReviewed {
				t.Fatalf("trust = %+v, %v", trust, err)
			}
		})
	}
}
