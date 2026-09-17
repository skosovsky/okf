package scripts

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const orderingHash = "872e58aec45565f0f8d5d952e994683ba4a9adfc1cd50ef28368551d2cac74b8"
const ambiguityHash = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"

func TestSkillsLockContract(t *testing.T) {
	tests := []struct {
		name, scenario, want, relation string
		a, b                           map[string]string
		rename                         [2]string
		special                        bool
	}{
		{name: "localeCompare ordering", scenario: "hash", want: orderingHash, a: map[string]string{"z-last": "Z", "A-upper": "A", "a-lower": "a", "a-dash": "dash", "a_under": "underscore", "a.dot": "dot", "[bracket]": "bracket", "!bang": "bang", "0digit": "zero", "nested/B-upper": "nested-upper", "nested/b-lower": "nested-lower", ".hidden": "hidden"}},
		{name: "regular recursive files only", scenario: "hash", relation: "equal", special: true, a: map[string]string{"SKILL.md": "skill", "references/nested.md": "nested", ".git/config": "ignored", "node_modules/pkg/index.js": "ignored", "references/node_modules/pkg.txt": "ignored"}, b: map[string]string{"SKILL.md": "skill", "references/nested.md": "nested"}},
		{name: "rename sensitivity", scenario: "hash", relation: "different", rename: [2]string{"before", "after"}, a: map[string]string{"before": "same"}},
		{name: "delimiter ambiguity", scenario: "hash", relation: "equal", want: ambiguityHash, a: map[string]string{"ab": "c"}, b: map[string]string{"a": "bc"}},
		{name: "check write lifecycle", scenario: "lifecycle"},
		{name: "invalid schema no write", scenario: "schema"},
		{name: "optional fields canonical", scenario: "optional"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			switch tc.scenario {
			case "hash":
				// Arrange.
				a := t.TempDir()
				if tc.special {
					a = shortDir(t)
				}
				writeFiles(t, a, tc.a)
				before := hash(t, a)
				if tc.rename[0] != "" {
					must(t, os.Rename(filepath.Join(a, tc.rename[0]), filepath.Join(a, tc.rename[1])))
				}
				if tc.special {
					outside := filepath.Join(t.TempDir(), "outside")
					put(t, filepath.Dir(outside), filepath.Base(outside), "ignored")
					must(t, os.Symlink(outside, filepath.Join(a, "link")))
					listener, err := net.Listen("unix", filepath.Join(a, "socket"))
					must(t, err)
					t.Cleanup(func() { listener.Close() })
				}
				// Act.
				got := hash(t, a)
				var peer string
				if tc.b != nil {
					b := t.TempDir()
					writeFiles(t, b, tc.b)
					peer = hash(t, b)
				}
				// Assert.
				ok(t, tc.want == "" || got == tc.want, "hash = %q, want %q", got, tc.want)
				ok(t, tc.relation != "equal" || got == peer, "hashes differ: %q / %q", got, peer)
				ok(t, tc.relation != "different" || got != before, "rename did not change hash")
			case "lifecycle":
				// Arrange.
				r := t.TempDir()
				put(t, filepath.Join(r, "skills", "valid"), "SKILL.md", "skill")
				h := hash(t, filepath.Join(r, "skills", "valid"))
				stale := document("valid", `{"computedHash":"`+strings.Repeat("0", 64)+`","sourceType":"local","source":"."}`)
				put(t, r, "skills-lock.json", stale)
				path := filepath.Join(r, "skills-lock.json")
				// Act.
				check := tool(t, "--check", "--root", r)
				unchanged, _ := os.ReadFile(path)
				write := tool(t, "--write", "--root", r)
				first, _ := os.ReadFile(path)
				second := tool(t, "--write", "--root", r)
				again, _ := os.ReadFile(path)
				fresh := tool(t, "--check", "--root", r)
				help, usage := tool(t, "--help"), tool(t, "--check", "--write")
				// Assert.
				want := fmt.Sprintf("{\n  \"version\": 1,\n  \"skills\": {\n    \"valid\": {\n      \"source\": \".\",\n      \"sourceType\": \"local\",\n      \"computedHash\": %q\n    }\n  }\n}\n", h)
				ok(t, check.code == 1 && check.out == "" && strings.Contains(check.err, "valid: lock has") && string(unchanged) == stale, "stale check wrote or misreported: %#v", check)
				ok(t, write.code == 0 && write.err == "" && string(first) == want && second.code == 0 && bytes.Equal(first, again), "write is not canonical/idempotent")
				ok(t, fresh.code == 0 && fresh.out == "skills-lock.json is current\n" && fresh.err == "", "fresh check: %#v", fresh)
				ok(t, help.code == 0 && strings.HasPrefix(help.out, "Usage:\n") && usage.code == 2, "help/usage exits = %d/%d", help.code, usage.code)
			case "schema":
				bad := []struct{ doc, message string }{{"[]\n", "JSON object"}, {`{"version":1}` + "\n", "exactly version and skills"}, {`{"version":2,"skills":{}}` + "\n", "version must be 1"}, {`{"version":1,"skills":[]}` + "\n", "skills must be an object"}, {document("../x", entry(".", "local", strings.Repeat("0", 64))+`}`), "invalid skill name"}, {document("valid", `{"sourceType":"local","computedHash":"`+strings.Repeat("0", 64)+`"}`), ".source must"}, {document("valid", entry(".", "local", strings.Repeat("A", 64))+`}`), "64 lowercase hex"}, {document("valid", entry(".", "local", strings.Repeat("0", 64))+`,"mystery":true}`), "unknown field"}, {document("valid", entry(".", "local", strings.Repeat("0", 64))+`,"subagents":[1]}`), "array of strings"}}
				for i, invalid := range bad {
					// Arrange.
					r := t.TempDir()
					put(t, r, "skills-lock.json", invalid.doc)
					// Act.
					got := tool(t, "--write", "--root", r)
					after, _ := os.ReadFile(filepath.Join(r, "skills-lock.json"))
					// Assert.
					ok(t, got.code == 2 && got.out == "" && strings.Contains(got.err, invalid.message) && string(after) == invalid.doc, "case %d result %#v", i, got)
				}
			case "optional":
				// Arrange.
				r := t.TempDir()
				d := filepath.Join(r, "skills", "valid")
				put(t, d, "SKILL.md", "valid")
				h := hash(t, d)
				doc := document("valid", `{"wellKnownDigest":"sha256:abc","subagents":["","worker"],"computedHash":"`+h+`","skillPath":"skills/valid/SKILL.md","sourceType":"github","ref":"main","sourceUrl":"https://example.invalid/repo.git","source":"owner/repo"}`)
				put(t, r, "skills-lock.json", doc)
				// Act.
				check, write := tool(t, "--check", "--root", r), tool(t, "--write", "--root", r)
				written, _ := os.ReadFile(filepath.Join(r, "skills-lock.json"))
				// Assert.
				for _, field := range []string{`"source": "owner/repo"`, `"sourceUrl": "https://example.invalid/repo.git"`, `"ref": "main"`, `"sourceType": "github"`, `"skillPath": "skills/valid/SKILL.md"`, `"computedHash": "` + h + `"`, `"subagents": [`, `"wellKnownDigest": "sha256:abc"`} {
					ok(t, strings.Contains(string(written), field), "canonical output missing %s", field)
				}
				ok(t, check.code == 0 && write.code == 0, "optional check/write = %d/%d", check.code, write.code)
			}
		})
	}
}

type result struct {
	code     int
	out, err string
}

func tool(t *testing.T, args ...string) result {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	command := exec.Command("node", append([]string{filepath.Join(filepath.Dir(file), "update-skills-lock.mjs")}, args...)...)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if err != nil && command.ProcessState == nil {
		t.Fatal(err)
	}
	return result{command.ProcessState.ExitCode(), stdout.String(), stderr.String()}
}

func hash(t *testing.T, dir string) string {
	t.Helper()
	got := tool(t, "--hash", dir)
	ok(t, got.code == 0 && got.err == "", "hash: %#v", got)
	return strings.TrimSuffix(got.out, "\n")
}

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		put(t, root, name, content)
	}
}

func put(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	must(t, os.MkdirAll(filepath.Dir(path), 0o700))
	must(t, os.WriteFile(path, []byte(content), 0o600))
}

func shortDir(t *testing.T) string {
	t.Helper()
	base := os.TempDir()
	if _, err := os.Stat("/private/tmp"); err == nil {
		base = "/private/tmp"
	}
	dir, err := os.MkdirTemp(base, "okf-lock-")
	must(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func document(name, body string) string {
	return `{"version":1,"skills":{"` + name + `":` + body + `}}` + "\n"
}

func entry(source, sourceType, hash string) string {
	return `{"source":"` + source + `","sourceType":"` + sourceType + `","computedHash":"` + hash + `"`
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func ok(t *testing.T, condition bool, format string, args ...any) {
	t.Helper()
	if !condition {
		t.Fatalf(format, args...)
	}
}
