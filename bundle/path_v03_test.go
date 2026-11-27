package bundle

import (
	"path/filepath"
	"testing"
)

func TestOKF03URIReferencesUseCapturedPaths(t *testing.T) {
	for _, version := range []string{OKFVersion, OKFVersion03} {
		t.Run(version, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			writeFile(t, root, "index.md", "---\nokf_version: '"+version+"'\n---\n")
			for _, path := range []string{"policy.md", "legacy.md", "docs/policy.md", "docs/release note.md"} {
				writeFile(t, root, path, "---\ntype: Policy\n---\n")
			}
			writeFile(t, root, "docs/README.md", "# Read me")
			writeFile(t, root, "docs/asset.png", "Asset")
			writeFile(t, root, "docs/origin.md", "---\ntype: Policy\nresource: policy.md\n---\n\n"+
				"[note][release]\n\n[release]: release%20note.md#policy\n\n"+
				"[readme](README.md) [asset](asset.png) [escape](../../policy.md)\n\n"+
				"```md\n[code](policy.md)\n```")
			loaded, err := LoadBundle(root)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "docs", "origin.md")
			id, err := ConceptIDFromPath(root, path)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			neighbor, _ := loaded.ResolvePathValueFor(path, "policy.md", PathFieldConceptResource)
			fallback, _ := loaded.ResolvePathValueFor(path, "legacy.md", PathFieldComputation)
			encoded, _ := loaded.ResolvePathValueFor(path, "release%20note.md#policy", PathFieldSourceResource)
			wrongCase, _ := loaded.ResolvePathValueFor(path, "Policy.md", PathFieldConceptResource)
			escape, _ := loaded.ResolvePathValueFor(path, "%2e%2e/%2e%2e/policy.md", PathFieldConceptResource)
			rootTarget, _ := loaded.ResolvePathValueFor(path, "/policy.md", PathFieldConceptResource)
			scope, _ := loaded.ResolvePathValueFor(path, "all queries in this project", PathFieldSourceResource)

			// Assert.
			if neighbor.Path != "docs/policy.md" || !neighbor.Exists || neighbor.RootFallback ||
				rootTarget.Path != "policy.md" || !rootTarget.Exists || wrongCase.Exists {
				t.Fatalf("neighbor=%+v root=%+v case=%+v", neighbor, rootTarget, wrongCase)
			}
			if fallback.Exists || fallback.RootFallback {
				t.Fatalf("fallback = %+v", fallback)
			}
			if version == OKFVersion03 {
				if encoded.Path != "docs/release note.md" || encoded.Suffix != "#policy" || !encoded.Exists ||
					escape.Exists || escape.Path != "" || scope.Kind != PathValueScope {
					t.Fatalf("encoded=%+v escape=%+v scope=%+v", encoded, escape, scope)
				}
				links := loaded.LinksFrom(id)
				if len(links) != 1 || links[0].Target.String() != "docs/release note" || !links[0].Exists {
					t.Fatalf("Markdown edges = %+v", links)
				}
				edges, err := loaded.PathConceptEdgesContext(t.Context(), id)
				if err != nil || len(edges) != 1 || edges[0].Target.String() != "docs/policy" || edges[0].FieldPath != "resource" {
					t.Fatalf("path field edges = %+v, err = %v", edges, err)
				}
			}
		})
	}
}

func TestOKF03ResolveCurrentConcept(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeFile(t, root, "index.md", "---\nokf_version: '0.3'\n---\n")
	for path, frontmatter := range map[string]string{
		"old.md":         "status: deprecated\nsuperseded_by: nested/next.md",
		"nested/next.md": "status: deprecated\nsuperseded_by: /current.md",
		"current.md":     "status: null\nsuperseded_by: retired.md",
		"retired.md":     "status: deprecated",
		"cycle.md":       "status: deprecated\nsuperseded_by: cycle.md",
		"missing.md":     "status: deprecated\nsuperseded_by: absent.md",
		"custom.md":      "status: unknown",
	} {
		writeFile(t, root, path, "---\ntype: Policy\n"+frontmatter+"\n---\n")
	}
	loaded, err := LoadBundle(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		id, destination string
		state           ReplacementState
	}{
		{"old", "current", ReplacementCurrent},
		{"current", "current", ReplacementCurrent},
		{"retired", "", ReplacementUnavailable},
		{"cycle", "", ReplacementCycle},
		{"missing", "", ReplacementMissing},
		{"custom", "", ReplacementNonCurrent},
	} {
		t.Run(check.id, func(t *testing.T) {
			id, err := ParseConceptID(check.id)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			target, state, err := loaded.ResolveCurrentConceptContext(t.Context(), id)

			// Assert.
			if err != nil || state != check.state || target.String() != check.destination {
				t.Fatalf("target=%s state=%s err=%v", target.String(), state, err)
			}
		})
	}
}
