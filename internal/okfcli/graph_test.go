package okfcli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGraphUsesExplicitToolkitProfile(t *testing.T) {
	// Arrange.
	root := fixturePath(t, "positive", "minimal")
	var stdout, stderr bytes.Buffer

	// Act.
	code := Run([]string{
		"graph", root,
		"--spec", "0.2",
		"--profile", "skosovsky/okf-v0.2",
		"--format", "json-ld",
	}, &stdout, &stderr)

	// Assert.
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("Run(graph) code/stderr = %d/%q", code, stderr.String())
	}
	projection, ok := graphProjectionMetadata(t, stdout.Bytes())
	if !ok || projection["profile"] != "skosovsky/okf-v0.2" {
		t.Fatalf("projection = %#v, want skosovsky/okf-v0.2 profile", projection)
	}
}

func TestGraphCanonicalAppendixANTriplesMatchesFrozenSemanticGolden(t *testing.T) {
	// Arrange.
	root := fixturePath(t, "positive", "appendix-a")
	goldenPath, err := filepath.Abs(filepath.Join("..", "..", "graph", "testdata", "v02", "appendix-a.semantic.nt"))
	if err != nil {
		t.Fatalf("filepath.Abs() error = %v", err)
	}
	golden, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("os.ReadFile(%s) error = %v", goldenPath, err)
	}

	for _, policy := range []string{"include", "exclude"} {
		t.Run(policy, func(t *testing.T) {
			// Arrange.
			var stdout, stderr bytes.Buffer

			// Act.
			code := Run([]string{
				"graph", root,
				"--profile", "skosovsky/okf-v0.2",
				"--format", "ntriples",
				"--as-of", "2026-06-15",
				"--extension-relations", policy,
			}, &stdout, &stderr)

			// Assert.
			if code != 0 || stderr.Len() != 0 {
				t.Fatalf("Run(graph) code/stderr = %d/%q", code, stderr.String())
			}
			if !bytes.Equal(stdout.Bytes(), golden) {
				t.Fatalf("Appendix A CLI N-Triples differs from frozen semantic golden for %s policy", policy)
			}
		})
	}
}

func TestGraphForwardsExtensionRelationPolicy(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "index.md"), "---\nokf_version: \"0.2\"\n---\n# Concepts\n")
	writeFixtureFile(t, filepath.Join(root, "a.md"), "---\ntype: Note\nrelations:\n  depends_on: [{target: b}]\n---\nBody.\n")
	writeFixtureFile(t, filepath.Join(root, "b.md"), "---\ntype: Note\n---\nBody.\n")

	render := func(t *testing.T, policy string) string {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := Run([]string{
			"graph", root,
			"--profile", "skosovsky/okf-v0.2",
			"--format", "ntriples",
			"--extension-relations", policy,
		}, &stdout, &stderr)
		if code != 0 || stderr.Len() != 0 {
			t.Fatalf("Run(graph --extension-relations %s) code/stderr = %d/%q", policy, code, stderr.String())
		}
		return stdout.String()
	}

	// Act.
	included := render(t, "include")
	excluded := render(t, "exclude")

	// Assert.
	if !strings.Contains(included, "ToolkitExtensionRelation") {
		t.Fatalf("included output has no toolkit extension relation:\n%s", included)
	}
	if strings.Contains(excluded, "ToolkitExtensionRelation") {
		t.Fatalf("excluded output contains toolkit extension relation:\n%s", excluded)
	}
	if included == excluded {
		t.Fatal("include and exclude policies produced identical relation-bearing projections")
	}
}

func TestGraphRelationRefWirePreservesStructuralIdentityAndJSONEscaping(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "index.md"),
		"---\nokf_version: \"0.2\"\n---\n\n# Knowledge\n\n- [Consumer](consumer.md)\n")
	writeFixtureFile(t, filepath.Join(root, "consumer.md"),
		"---\ntype: Note\nrelations:\n  uses:\n"+
			"    - target: 'source\\#part'\n"+
			"    - target: 'source#part'\n"+
			"    - target: ordinary\n"+
			"---\nBody.\n")
	writeFixtureFile(t, filepath.Join(root, "source.md"),
		"---\ntype: Note\nfields:\n  - id: part\n---\nBody.\n")
	writeFixtureFile(t, filepath.Join(root, "source#part.md"), "---\ntype: Note\n---\nBody.\n")
	writeFixtureFile(t, filepath.Join(root, "ordinary.md"), "---\ntype: Note\n---\nBody.\n")
	render := func(t *testing.T, format string) string {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := Run([]string{
			"graph", root,
			"--profile", "legacy-v0.1",
			"--format", format,
			"--extension-relations", "include",
		}, &stdout, &stderr)
		if code != 0 || stderr.Len() != 0 {
			t.Fatalf("Run(graph %s) code/stderr = %d/%q", format, code, stderr.String())
		}
		return stdout.String()
	}

	// Act.
	textOutput := render(t, "text")
	jsonOutput := render(t, "json-ld")

	// Assert.
	for _, line := range []string{
		"  => uses source\\#part\n",
		"  => uses source#part\n",
		"  => uses ordinary\n",
	} {
		if strings.Count(textOutput, line) != 1 {
			t.Fatalf("text graph count for %q != 1:\n%s", line, textOutput)
		}
	}
	for _, encoded := range []string{
		`"@id": "bundle:source\\#part"`,
		`"@id": "bundle:source#part"`,
		`"@id": "bundle:ordinary"`,
	} {
		if !strings.Contains(jsonOutput, encoded) {
			t.Fatalf("JSON-LD graph missing exact JSON wire %q:\n%s", encoded, jsonOutput)
		}
	}
	var document struct {
		Graph []map[string]any `json:"@graph"`
	}
	if err := json.Unmarshal([]byte(jsonOutput), &document); err != nil {
		t.Fatalf("json.Unmarshal(graph) error = %v; output=%q", err, jsonOutput)
	}
	var consumer map[string]any
	for _, node := range document.Graph {
		if node["@id"] == "bundle:consumer" {
			consumer = node
			break
		}
	}
	uses, ok := consumer["uses"].([]any)
	if !ok || len(uses) != 3 {
		t.Fatalf("consumer uses = %#v, want three relation objects", consumer["uses"])
	}
	counts := map[string]int{}
	for _, value := range uses {
		reference, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("consumer uses entry = %#v, want object", value)
		}
		target, _ := reference["@id"].(string)
		counts[target]++
	}
	wantTargets := map[string]int{
		"bundle:source\\#part": 1,
		"bundle:source#part":   1,
		"bundle:ordinary":      1,
	}
	if !reflect.DeepEqual(counts, wantTargets) {
		t.Fatalf("consumer relation targets = %#v, want %#v", counts, wantTargets)
	}
}

func TestGraphAutoPreservesDomainVersionResolutionSource(t *testing.T) {
	cases := []struct {
		name         string
		root         string
		wantDeclared string
		wantSource   string
		wantCompat   string
	}{
		{
			name:         "future best effort",
			root:         fixturePath(t, "compat", "future-version"),
			wantDeclared: "9.9",
			wantSource:   "future-best-effort",
			wantCompat:   "best-effort",
		},
		{
			name:       "absent defaults",
			root:       fixturePath(t, "compat", "undeclared-v01"),
			wantSource: "default",
			wantCompat: "native",
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			var stdout, stderr bytes.Buffer

			// Act.
			code := Run([]string{
				"graph", test.root,
				"--profile", "skosovsky/okf-v0.2",
				"--format", "json-ld",
			}, &stdout, &stderr)

			// Assert.
			if code != 0 || stderr.Len() != 0 {
				t.Fatalf("Run(graph) code/stderr = %d/%q", code, stderr.String())
			}
			projection, ok := graphProjectionMetadata(t, stdout.Bytes())
			if !ok {
				t.Fatalf("graph output has no proj:Projection node: %q", stdout.String())
			}
			declared, _ := projection["declaredOKFVersion"].(string)
			if declared != test.wantDeclared || projection["effectiveOKFVersion"] != "0.2" ||
				projection["versionSource"] != test.wantSource || projection["compatibilityMode"] != test.wantCompat {
				t.Fatalf("version projection = %#v, want declared=%q effective=0.2 source=%q compat=%q",
					projection, test.wantDeclared, test.wantSource, test.wantCompat)
			}
		})
	}
}

func TestGraphDefaultProfileFollowsEffectiveVersion(t *testing.T) {
	cases := []struct {
		name        string
		root        string
		wantProfile string
	}{
		{name: "declared v01", root: fixturePath(t, "compat", "declared-v01"), wantProfile: ""},
		{name: "declared v02", root: fixturePath(t, "positive", "minimal"), wantProfile: "skosovsky/okf-v0.2"},
		{name: "absent defaults v02", root: fixturePath(t, "compat", "undeclared-v01"), wantProfile: "skosovsky/okf-v0.2"},
		{name: "future best effort v02", root: fixturePath(t, "compat", "future-version"), wantProfile: "skosovsky/okf-v0.2"},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			var stdout, stderr bytes.Buffer

			// Act.
			code := Run([]string{"graph", test.root, "--format", "json-ld"}, &stdout, &stderr)

			// Assert.
			if code != 0 || stderr.Len() != 0 {
				t.Fatalf("Run(graph) code/stderr = %d/%q", code, stderr.String())
			}
			projection, ok := graphProjectionMetadata(t, stdout.Bytes())
			if test.wantProfile == "" {
				if ok {
					t.Fatalf("legacy graph unexpectedly contains projection metadata: %#v", projection)
				}
				return
			}
			if !ok || projection["profile"] != test.wantProfile {
				t.Fatalf("projection = %#v, want profile %q", projection, test.wantProfile)
			}
		})
	}
}

func graphProjectionMetadata(t *testing.T, payload []byte) (map[string]any, bool) {
	t.Helper()
	var document struct {
		Graph []map[string]any `json:"@graph"`
	}
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; output=%q", err, payload)
	}
	for _, node := range document.Graph {
		if node["@type"] == "proj:Projection" {
			return node, true
		}
	}
	return nil, false
}
