package okfcli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIInstantTemporalProfileAcrossValidateInfoAndGraph(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.md"), []byte("---\nokf_version: \"0.2\"\n---\n# Notes\n- [A](a.md)\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("---\ntype: Note\nstale_after: 2026-09-23T18:00:00+07:00\nsources:\n  - {resource: policy.md, last_modified: 2026-09-23T12:00:00Z}\n---\nA.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	instant := "instant-0b87c52"
	at := "2026-09-23T11:00:00Z"
	var validateOut, infoOut, graphOut, errOut bytes.Buffer

	// Act.
	validateCode := Run([]string{"validate", "--path", root, "--strict", "--format", "json", "--temporal-profile", instant, "--as-of", at}, &validateOut, &errOut)
	infoCode := Run([]string{"info", root, "--format", "json", "--temporal-profile", instant, "--as-of", at}, &infoOut, &errOut)
	graphCode := Run([]string{"graph", root, "--profile", "skosovsky/okf-v0.2-instant", "--format", "ntriples", "--as-of", at}, &graphOut, &errOut)
	var validation, info map[string]any
	validateJSONErr := json.Unmarshal(validateOut.Bytes(), &validation)
	infoJSONErr := json.Unmarshal(infoOut.Bytes(), &info)

	// Assert.
	if validateCode != 0 || infoCode != 0 || graphCode != 0 || validateJSONErr != nil || infoJSONErr != nil {
		t.Fatalf("CLI code/json errors: %d %d %d %v %v; stderr=%s", validateCode, infoCode, graphCode, validateJSONErr, infoJSONErr, errOut.String())
	}
	if validation["temporal_profile"] != instant || info["temporal_profile"] != instant {
		t.Fatalf("profiles: validate=%#v info=%#v", validation, info)
	}
	if info["stale"] != float64(1) || info["as_of"] != at || info["sources"] != float64(1) {
		t.Fatalf("info: %#v", info)
	}
	if !strings.Contains(graphOut.String(), `"true"^^<http://www.w3.org/2001/XMLSchema#boolean>`) || !strings.Contains(graphOut.String(), `"2026-09-23T18:00:00+07:00"^^<http://www.w3.org/2001/XMLSchema#dateTime>`) {
		t.Fatalf("graph: %s", graphOut.String())
	}
}

func TestCLIParseInstantTemporalProfilePreservesTypedProjection(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	path := filepath.Join(root, "a.md")
	content := "---\ntype: Note\nstale_after: 2026-09-23T18:00:00+07:00\nusage_window: {from: 2026-09-23T11:00:00Z, to: 2026-09-23T12:00:00Z}\nsources:\n  - {id: policy, resource: urn:test:policy, last_modified: 2026-09-23T11:00:00Z}\n  - {resource: urn:test:anonymous, last_modified: 2026-09-23T11:00:00Z}\n---\nA.[^policy]\n\n[^policy]: Source.\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	var output, stderr bytes.Buffer

	// Act.
	code := Run([]string{"parse", path, "--format", "json", "--temporal-profile", "instant-0b87c52", "--as-of", "2026-09-23T11:00:00Z"}, &output, &stderr)
	var projected struct {
		TemporalProfile string `json:"temporal_profile"`
		StaleAfter      string `json:"stale_after"`
		Stale           *bool  `json:"stale"`
		AsOf            string `json:"as_of"`
		Sources         []struct {
			LastModified string `json:"last_modified"`
		} `json:"sources"`
		SourcesAudit struct {
			Entries []struct {
				Valid        bool `json:"valid"`
				LastModified struct {
					State string `json:"state"`
				} `json:"last_modified"`
			} `json:"entries"`
		} `json:"sources_audit"`
		Attributions []struct {
			Sources []any `json:"sources"`
		} `json:"attributions"`
		AttributionsAudit struct {
			EntryEvidence []struct {
				Valid        bool   `json:"valid"`
				NormalizedID string `json:"normalized_id"`
				Value        *struct {
					Sources []any `json:"sources"`
				} `json:"value"`
			} `json:"entry_evidence"`
		} `json:"attributions_audit"`
	}
	err := json.Unmarshal(output.Bytes(), &projected)

	// Assert.
	if code != 0 || err != nil {
		t.Fatalf("parse code=%d json=%v stderr=%s output=%s", code, err, stderr.String(), output.String())
	}
	if projected.TemporalProfile != "instant-0b87c52" || projected.StaleAfter != "2026-09-23T18:00:00+07:00" || projected.Stale == nil || !*projected.Stale || projected.AsOf != "2026-09-23T11:00:00Z" {
		t.Fatalf("instant lifecycle: %#v", projected)
	}
	if len(projected.Sources) != 2 || projected.Sources[0].LastModified != "2026-09-23T11:00:00Z" || len(projected.SourcesAudit.Entries) != 2 || !projected.SourcesAudit.Entries[0].Valid || projected.SourcesAudit.Entries[0].LastModified.State != "valid" {
		t.Fatalf("instant sources: %#v", projected)
	}
	if len(projected.Attributions) != 1 || len(projected.Attributions[0].Sources) != 1 {
		t.Fatalf("instant attribution: %#v", projected.Attributions)
	}
	entries := projected.AttributionsAudit.EntryEvidence
	if len(entries) != 2 || !entries[0].Valid || entries[0].NormalizedID != "policy" || entries[0].Value == nil || len(entries[0].Value.Sources) != 1 || entries[1].Valid || entries[1].NormalizedID != "" || entries[1].Value != nil {
		t.Fatalf("instant attribution audit: %#v", entries)
	}
	output.Reset()
	stderr.Reset()
	if code := Run([]string{"parse", path, "--format", "json", "--temporal-profile", "instant-0b87c52", "--as-of", "2026-09-23"}, &output, &stderr); code == 0 || !strings.Contains(stderr.String(), "invalid --as-of datetime") {
		t.Fatalf("date-only as_of accepted by instant parse: code=%d stderr=%s", code, stderr.String())
	}
}

func TestCLIParseInstantAttributionSourcesIgnoreYAMLOrder(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	sourceA := "  - {id: policy, resource: urn:test:a, last_modified: 2026-09-23T11:00:00Z}\n"
	sourceB := "  - {id: policy, resource: urn:test:b, last_modified: 2026-09-23T12:00:00Z}\n"
	project := func(name, sources string) []any {
		t.Helper()
		path := filepath.Join(root, name)
		content := "---\ntype: Note\nsources:\n" + sources + "---\nA.[^policy]\n\n[^policy]: Source.\n"
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		var output, stderr bytes.Buffer
		if code := Run([]string{"parse", path, "--format", "json", "--temporal-profile", "instant-0b87c52"}, &output, &stderr); code != 0 {
			t.Fatalf("parse code=%d stderr=%s", code, stderr.String())
		}
		var value struct {
			Attributions []struct {
				Sources []any `json:"sources"`
			} `json:"attributions"`
		}
		if err := json.Unmarshal(output.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		if len(value.Attributions) != 1 {
			t.Fatalf("attributions: %#v", value.Attributions)
		}
		return value.Attributions[0].Sources
	}

	// Act.
	forward := project("forward.md", sourceA+sourceB)
	reverse := project("reverse.md", sourceB+sourceA)

	// Assert.
	forwardJSON, err := json.Marshal(forward)
	if err != nil {
		t.Fatal(err)
	}
	reverseJSON, err := json.Marshal(reverse)
	if err != nil {
		t.Fatal(err)
	}
	if len(forward) != 2 || !bytes.Equal(forwardJSON, reverseJSON) {
		t.Fatalf("attribution source order changed: forward=%s reverse=%s", forwardJSON, reverseJSON)
	}
}
