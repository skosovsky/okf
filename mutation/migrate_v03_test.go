package mutation

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
)

func TestOKF03MigrationPreservesLegacyScalarValues(t *testing.T) {
	// Arrange.
	const legacy = "---\r\ntype: 1e9999 # keep\r\ngenerated: {by: 'human:writer', at: 2026-11-27}\r\n" +
		"sources: [{id: report, resource: 'urn:test:report', usage_count: 077}]\r\n" +
		"x-core: {binary: 0b101, inferred-float: 079, decimal: 1_000.5, date: 2026-10-24, 1e9999: untouched}\r\n" +
		"x-anchor: &ref 1e9999\r\nx-mirror: *ref\r\nx-nonspecific: ! 1e9999\r\nx-quoted: !!int &count '077'\r\n" +
		"x-unrelated: 'keep'\r\n---\r\n\r\nNote."
	source := memorySource{
		"index.md": []byte("---\r\nokf_version: '0.2'\r\ntitle: +1e9999 # keep\r\n---\r\n# Rules\r\n\r\n- [Rule](note.md)\r\n"),
		"note.md":  []byte(legacy),
	}
	request := MigrationV03Request{ID: "migration-scalars", Actor: "human:reviewer"}
	operation, err := migrationV03Change(request, revisionFor(t, source))
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	migrated, err := Plan(t.Context(), source, operation)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := bundle.Load(t.Context(), migrated.Staged)
	if err != nil {
		t.Fatal(err)
	}
	note, err := migrated.Staged.ReadFile(t.Context(), "note.md")
	if err != nil {
		t.Fatal(err)
	}
	index, err := migrated.Staged.ReadFile(t.Context(), "index.md")
	if err != nil {
		t.Fatal(err)
	}

	// Assert.
	want := strings.NewReplacer(
		"type: 1e9999", `type: "1e9999"`,
		"usage_count: 077", "usage_count: 63",
		"binary: 0b101", "binary: 5",
		"inferred-float: 079", "inferred-float: 079.0",
		"decimal: 1_000.5", "decimal: 1000.5",
		"date: 2026-10-24", `date: !!timestamp "2026-10-24"`,
		"1e9999: untouched", `"1e9999": untouched`,
		"&ref 1e9999", `&ref "1e9999"`,
		"&count '077'", "&count '63'",
	).Replace(legacy)
	if string(note) != want || !bytes.Contains(index, []byte(`title: "+1e9999" # keep`)) {
		t.Fatalf("note = %q, want %q, index = %q", note, want, index)
	}
	if !migrated.Validation.IsConformant() || !loaded.Contains(ref(t, "note").ID) || string(source["note.md"]) != legacy {
		t.Fatalf("migration did not preserve the source bundle: validation = %+v", migrated.Validation)
	}
}

func TestOKF03MigrationReportsScalarWithoutProvableSpan(t *testing.T) {
	// Arrange.
	const note = "---\ntype: Note\nx-scalar: !<tag:yaml.org,2002:int> \"077\"\n---\nNote.\n"
	source := memorySource{
		"index.md": []byte("---\nokf_version: '0.2'\n---\n# Rules\n\n- [Rule](note.md)\n"),
		"note.md":  []byte(note),
	}
	request := MigrationV03Request{ID: "blocked-scalar", Actor: "human:reviewer"}

	// Act.
	preview, err := PreviewV03Migration(t.Context(), source, request)

	// Assert.
	var blocked *store.InvalidChangeSet
	if !errors.As(err, &blocked) || blocked.Code != "migration_v03_yaml_scalar" || len(blocked.Diagnostics) != 1 ||
		blocked.Diagnostics[0].File != "note.md" || !strings.Contains(blocked.Diagnostics[0].Message, "YAML line 2, column 11") ||
		len(preview.Preview.Writes) != 0 || string(source["note.md"]) != note {
		t.Fatalf("preview = %+v, blocked = %+v, err = %v", preview, blocked, err)
	}
}

func TestOKF03MigrationRequiresAnExplicitUnversionedSourceAssumption(t *testing.T) {
	// Arrange.
	note := []byte("---\ntype: Note\nstale_after: '2026-11-18T09:30:00-05:00'\nx-preserve: true\n---\n\nNote.\n")
	source := memorySource{"notes/my note.md": note}
	request := MigrationV03Request{ID: "upgrade", Actor: "human:reviewer"}

	// Act.
	_, rejected := PreviewV03Migration(t.Context(), source, request)
	request.AssumeV02 = true
	preview, err := PreviewV03Migration(t.Context(), source, request)

	// Assert.
	var blocked *store.InvalidChangeSet
	if !errors.As(rejected, &blocked) || blocked.Code != "migration_v03_source_version" {
		t.Fatalf("unversioned migration error: %v", rejected)
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Preview.Writes) != 1 || preview.Preview.Writes[0].Path != "index.md" ||
		!bytes.Contains(preview.Preview.Writes[0].Content, []byte("/notes/my%20note.md")) ||
		!bytes.Contains(preview.Preview.Writes[0].Content, []byte("okf_version: \"0.3\"")) {
		t.Fatalf("unexpected writes: %+v", preview.Preview.Writes)
	}
	if len(source) != 1 || !bytes.Equal(source["notes/my note.md"], note) {
		t.Fatal("preview mutated the source")
	}
}

func TestOKF03MigrationCannotCreateCaseAliasOfRootIndex(t *testing.T) {
	source := memorySource{"INDEX.md": []byte("---\ntype: Note\n---\nDraft.\n")}
	request := MigrationV03Request{ID: "upgrade", Actor: "human:reviewer", AssumeV02: true}
	preview, err := PreviewV03Migration(t.Context(), source, request)
	var blocked *store.InvalidChangeSet
	if !errors.As(err, &blocked) || blocked.Code != "path_case_collision" || len(preview.Preview.Writes) != 0 {
		t.Fatalf("preview = %+v, %v", preview, err)
	}
	if len(source) != 1 || source["index.md"] != nil {
		t.Fatalf("preview mutated the source: %v", source)
	}
}

func TestOKF03MigrationAndMoveOnlyRewriteTheLogLink(t *testing.T) {
	// Arrange.
	const logPath = "nested/log.md"
	const original = "# History\r\n\r\n## 2026-11-27\r\n* **Update**: See [Rule](../rule).\r\n" +
		"  This line belongs to the entry.\r\n\r\n## 2026-10-01\r\n- Opened the project."
	source := memorySource{
		"index.md": []byte("---\nokf_version: '0.2'\n---\n# Rules\n\n- [Rule](rule.md)\n"),
		"rule.md":  []byte("---\ntype: Note\n---\nRule.\n"),
		logPath:    []byte(original),
	}
	request := MigrationV03Request{ID: "migration-log", Actor: "human:reviewer"}
	operation, err := migrationV03Change(request, revisionFor(t, source))
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	migrated, err := Plan(t.Context(), source, operation)
	if err != nil {
		t.Fatal(err)
	}
	moved, err := Plan(t.Context(), migrated.Staged, change(t, migrated.Staged, store.MoveConcept{
		From: ref(t, "rule").ID, To: ref(t, "rules/new note").ID,
	}))
	if err != nil {
		t.Fatal(err)
	}

	// Assert.
	for _, test := range []struct {
		result Result
		link   string
	}{
		{migrated, "/rule.md"},
		{moved, "/rules/new%20note.md"},
	} {
		raw, err := test.result.Staged.ReadFile(t.Context(), logPath)
		want := bytes.Replace([]byte(original), []byte("../rule"), []byte(test.link), 1)
		if err != nil || !test.result.Validation.IsConformant() || !bytes.Equal(raw, want) {
			t.Fatalf("log bytes=%q, want %q, err=%v", raw, want, err)
		}
		parsed := bundle.ParseLog(string(raw))
		if len(parsed.Days) != 2 || parsed.Days[0].Date != "2026-11-27" || parsed.Days[1].Date != "2026-10-01" ||
			len(parsed.Days[0].Entries) != 1 || len(parsed.InvalidDates()) != 0 {
			t.Fatalf("log structure changed: %+v", parsed)
		}
	}
}

func TestOKF03MigrationRequiresChoiceWhenURIDecodingChangesMarkdownTarget(t *testing.T) {
	source := memorySource{
		"index.md":        []byte("---\nokf_version: '0.2'\n---\n# Notes\n\n- [Policy](policy.md)\n- [Notes](notes/)\n"),
		"policy.md":       []byte("---\ntype: Note\n---\n\n[Claim][source]\n\n[source]: item%20file.md?q=claim#detail\n"),
		"item%20file.md":  []byte("---\ntype: Note\n---\n\nLiteral.\n"),
		"item file.md":    []byte("---\ntype: Note\n---\n\nDecoded.\n"),
		"notes/README.md": []byte("# Instructions\n\n[Policy](/policy#claim)\n"),
		"notes/index.md":  []byte("# Notes\n\n- [Policy](/policy.md)\n"),
	}
	request := MigrationV03Request{ID: "upgrade", Actor: "human:reviewer"}
	_, err := PreviewV03Migration(t.Context(), source, request)
	var blocked *store.InvalidChangeSet
	if !errors.As(err, &blocked) || blocked.Code != "migration_v03_path_choices_required" ||
		len(blocked.Diagnostics) != 1 || !bytes.Contains([]byte(blocked.Diagnostics[0].Message), []byte("policy.md markdown[1]")) {
		t.Fatalf("collision: %#v, %v", blocked, err)
	}
	request.Choices = []store.MigrationV03PathChoice{{Concept: "policy", Field: "markdown[1]", Target: "item%20file.md"}}

	preview, err := PreviewV03Migration(t.Context(), source, request)
	if err != nil {
		t.Fatal(err)
	}
	const reference = "[source]: /item%2520file.md?q=claim#detail"
	for path, want := range map[string]string{"policy.md": reference, "notes/README.md": "[Policy](/policy.md#claim)"} {
		var found bool
		for _, write := range preview.Preview.Writes {
			if write.Path == path && bytes.Contains(write.Content, []byte(want)) {
				found = true
			}
		}
		if !found {
			t.Fatalf("preview does not include %s → %q: %+v", path, want, preview.Preview.Writes)
		}
	}
	if bytes.Contains(source["policy.md"], []byte(reference)) {
		t.Fatal("preview changed the source")
	}
	for _, plan := range preview.Preview.Plan {
		for _, detail := range plan.Details {
			if bytes.Contains([]byte(detail), []byte(`unresolved Markdown destination "notes/"`)) {
				t.Fatalf("valid directory was diagnosed: %s", detail)
			}
		}
	}
	staged := memorySource{}
	for path, data := range source {
		staged[path] = data
	}
	for _, write := range preview.Preview.Writes {
		staged[write.Path] = write.Content
	}
	loaded, err := bundle.Load(t.Context(), staged)
	if err != nil {
		t.Fatal(err)
	}
	target, _, err := loaded.ResolveMarkdownTargetV03Context(t.Context(), "policy.md", "/item%2520file.md?q=claim#detail")
	if err != nil || !target.Exists || target.Path != "item%20file.md" {
		t.Fatalf("target = %+v, %v", target, err)
	}
}
