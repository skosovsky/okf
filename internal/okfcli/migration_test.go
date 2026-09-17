package okfcli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/mutation"
)

func TestMigrateInvalidCitationMappingsRejectBeforeStoreAndLeaveBytesUnchanged(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{name: "unknown field", payload: `[{"path":"legacy.md","entries":[{"legacy_number":1,"source_id":"s","wat":true}]}]`},
		{name: "malformed JSON", payload: `[`},
		{name: "invalid path", payload: `[{"path":"../legacy.md","entries":[{"legacy_number":1,"source_id":"s"}]}]`},
		{name: "duplicate path", payload: `[
			{"path":"legacy.md","entries":[{"legacy_number":1,"source_id":"one"}]},
			{"path":"legacy.md","entries":[{"legacy_number":2,"source_id":"two"}]}
		]`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			indexPath := filepath.Join(root, "index.md")
			legacyPath := filepath.Join(root, "legacy.md")
			mappingsPath := filepath.Join(root, "mappings.json")
			writeFixtureFile(t, indexPath,
				"---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [Legacy](legacy.md)\n")
			writeFixtureFile(t, legacyPath,
				"---\ntype: Knowledge\n---\n\nClaim [1].\n\n# Citations\n\n[1] https://example.test\n")
			writeFixtureFile(t, mappingsPath, test.payload)
			before := snapshotFilesystemTree(t, root)
			var stdout, stderr bytes.Buffer

			// Act.
			code := Run([]string{
				"migrate", root,
				"--to", "0.2",
				"--citation-mappings", mappingsPath,
				"--write",
				"--format", "json",
			}, &stdout, &stderr)
			after := snapshotFilesystemTree(t, root)
			_, storeErr := os.Stat(filepath.Join(root, ".okf"))

			// Assert.
			if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "citation mappings") {
				t.Fatalf("Run(migrate) code/stdout/stderr = %d/%q/%q", code, stdout.String(), stderr.String())
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("invalid citation mapping input changed root tree:\nbefore=%#v\nafter=%#v", before, after)
			}
			if !os.IsNotExist(storeErr) {
				t.Fatalf("invalid citation mapping input opened durable store: %v", storeErr)
			}
		})
	}
}

func TestMigrateRejectsInvalidDomainInputBeforeReadingMissingSource(t *testing.T) {
	// Arrange.
	parent := t.TempDir()
	root := filepath.Join(parent, "missing")
	before := snapshotFilesystemTree(t, parent)
	var stdout, stderr bytes.Buffer

	// Act.
	code := Run([]string{
		"migrate", root,
		"--from", "auto",
		"--to", "0.2",
		"--actor", "not an actor",
		"--write",
		"--format", "json",
	}, &stdout, &stderr)
	after := snapshotFilesystemTree(t, parent)

	// Assert.
	if code != 1 || stdout.Len() != 0 ||
		!strings.Contains(stderr.String(), "invalid document actor") ||
		strings.Contains(stderr.String(), "no such file") {
		t.Fatalf("Run(migrate missing source) code/stdout/stderr = %d/%q/%q",
			code, stdout.String(), stderr.String())
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("invalid migration touched missing source parent:\nbefore=%#v\nafter=%#v", before, after)
	}
}

func TestMigrateCitationMappingsCoverRootAndNestedMarkdownWithoutInventingActor(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("durable filesystem store is unsupported on this platform")
	}

	// Arrange.
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatalf("os.Mkdir() error = %v", err)
	}
	files := map[string]string{
		"index.md":        "---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [Nested](nested/index.md)\n\nClaim [1].\n\n# Citations\n\n[1] [Shared](https://shared.example)\n",
		"log.md":          "Claim [2].\n\n# Citations\n\n[2] [Shared](https://shared.example)\n\n# Log\n\n## 2026-01-01\n\n- event\n",
		"nested/index.md": "# Knowledge\n\n- [A](a.md)\n\nClaim [3].\n\n# Citations\n\n[3] https://nested.example\n",
		"nested/log.md":   "Claim [4].\n\n# Citations\n\n[4] https://nested-log.example\n\n# Log\n\n## 2026-01-01\n\n- event\n",
		"nested/a.md":     "---\ntype: Knowledge\n---\nBody.\n",
	}
	for path, content := range files {
		writeFixtureFile(t, filepath.Join(root, filepath.FromSlash(path)), content)
	}
	mappingsPath := filepath.Join(root, "mappings.json")
	writeFixtureFile(t, mappingsPath, `[
		{"path":"nested/log.md","entries":[{"legacy_number":4,"legacy_entry":"https://nested-log.example","source_id":"nested-log"}]},
		{"path":"index.md","entries":[{"legacy_number":1,"legacy_entry":"[Shared](https://shared.example)","source_id":"shared","title":"Shared","resource":"https://shared.example"}]},
		{"path":"nested/index.md","entries":[{"legacy_number":3,"legacy_entry":"https://nested.example","source_id":"nested"}]},
		{"path":"log.md","entries":[{"legacy_number":2,"legacy_entry":"[Shared](https://shared.example)","source_id":"shared","title":"Shared","resource":"https://shared.example"}]}
	]`)
	before := snapshotRegularFiles(t, root)

	// Act.
	dryCode, dryReport, dryStderr := runMigrateJSONRequestWithMappings(t, root, "auto", "", mappingsPath, false)
	afterDry := snapshotRegularFiles(t, root)
	applyCode, applyReport, applyStderr := runMigrateJSONRequestWithMappings(t, root, "auto", "", mappingsPath, true)
	afterApply := snapshotRevisionVisibleMigrationFiles(t, root)
	noopCode, noopReport, noopStderr := runMigrateJSONRequestWithMappings(t, root, "auto", "", mappingsPath, true)
	afterNoop := snapshotRevisionVisibleMigrationFiles(t, root)

	// Assert.
	if dryCode != 0 || dryStderr != "" || dryReport.Outcome != "preview" ||
		dryReport.Actor != "" || len(dryReport.Blockers) != 0 || len(dryReport.ManualActions) != 0 {
		t.Fatalf("citation preview code/stderr/report = %d/%q/%#v", dryCode, dryStderr, dryReport)
	}
	if !reflect.DeepEqual(before, afterDry) {
		t.Fatal("citation preview changed bundle bytes")
	}
	if applyCode != 0 || applyStderr != "" || !applyReport.Applied ||
		applyReport.Actor != "" || applyReport.Receipt == nil {
		t.Fatalf("citation apply code/stderr/report = %d/%q/%#v", applyCode, applyStderr, applyReport)
	}
	if noopCode != 0 || noopStderr != "" || !noopReport.Noop ||
		noopReport.Outcome != "applied" || !noopReport.Applied || noopReport.Receipt == nil ||
		len(noopReport.Blockers) != 0 || len(noopReport.ManualActions) != 0 ||
		noopReport.PlanDigest == "" || noopReport.ResolutionDigest == "" {
		t.Fatalf("citation target-noop code/stderr/report = %d/%q/%#v",
			noopCode, noopStderr, noopReport)
	}
	if !reflect.DeepEqual(afterApply, afterNoop) {
		t.Fatalf("citation target-noop changed tree:\nafter apply=%#v\nafter noop=%#v",
			afterApply, afterNoop)
	}
	expectedIDs := map[string]string{
		"index.md":        "shared",
		"log.md":          "shared",
		"nested/index.md": "nested",
		"nested/log.md":   "nested-log",
	}
	for path, sourceID := range expectedIDs {
		updated, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			t.Fatalf("os.ReadFile(%s) error = %v", path, err)
		}
		if bytes.Contains(updated, []byte("# Citations")) ||
			!bytes.Contains(updated, []byte("[^"+sourceID+"]")) {
			t.Fatalf("%s migration incomplete:\n%s", path, updated)
		}
	}
	index, _ := os.ReadFile(filepath.Join(root, "index.md"))
	if !bytes.Contains(index, []byte(`okf_version: "0.2"`)) {
		t.Fatalf("root index missing migrated version:\n%s", index)
	}

	tamperedPath := filepath.Join(root, "nested", "log.md")
	tampered, err := os.ReadFile(tamperedPath)
	if err != nil {
		t.Fatalf("os.ReadFile(%s) error = %v", tamperedPath, err)
	}
	tampered = bytes.Replace(
		tampered,
		[]byte("https://nested-log.example"),
		[]byte("https://tampered.example"),
		1,
	)
	if err := os.WriteFile(tamperedPath, tampered, 0o644); err != nil {
		t.Fatalf("os.WriteFile(%s) error = %v", tamperedPath, err)
	}
	beforeRejectedReplay := snapshotFilesystemTree(t, root)

	tamperedCode, tamperedReport, tamperedStderr := runMigrateJSONRequestWithMappings(
		t, root, "auto", "", mappingsPath, true,
	)
	afterRejectedReplay := snapshotFilesystemTree(t, root)

	if tamperedCode != 1 || tamperedStderr != "" ||
		tamperedReport.Outcome != "blocked" || tamperedReport.Noop || tamperedReport.Applied ||
		tamperedReport.Receipt != nil ||
		!containsMigrationBlocker(tamperedReport.Blockers, "migration_replay_mismatch") {
		t.Fatalf("tampered citation replay code/stderr/report = %d/%q/%#v",
			tamperedCode, tamperedStderr, tamperedReport)
	}
	if !reflect.DeepEqual(beforeRejectedReplay, afterRejectedReplay) {
		t.Fatalf("tampered citation replay changed tree:\nbefore=%#v\nafter=%#v",
			beforeRejectedReplay, afterRejectedReplay)
	}
}

func TestMigrateCitationMappingsSelectExactRawCRLFAndUnnumberedBullet(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("durable filesystem store is unsupported on this platform")
	}

	// Arrange.
	root := t.TempDir()
	numberedPath := filepath.Join(root, "numbered.md")
	bulletPath := filepath.Join(root, "bullet.md")
	writeFixtureFile(t, filepath.Join(root, "index.md"),
		"---\r\nokf_version: \"0.1\"\r\n---\r\n\r\n# Knowledge\r\n\r\n- [Numbered](numbered.md)\r\n- [Bullet](bullet.md)\r\n")
	writeFixtureFile(t, numberedPath,
		"---\r\ntype: Knowledge\r\n---\r\n\r\nClaim [1].\r\n\r\n# Citations\r\n\r\n[1] [Spec](https://spec.example)\r\n")
	writeFixtureFile(t, bulletPath,
		"---\r\ntype: Knowledge\r\n---\r\n\r\n# Citations\r\n\r\n- https://bullet.example\r\n")
	mappingsPath := filepath.Join(root, "mappings.json")
	writeFixtureFile(t, mappingsPath, `[
		{"path":"bullet.md","entries":[
			{"legacy_entry":"https://bullet.example","source_id":"bullet"}
		]},
		{"path":"numbered.md","entries":[
			{"legacy_number":1,"legacy_entry":"[Spec](https://spec.example)","source_id":"spec"}
		]}
	]`)

	// Act.
	code, report, stderr := runMigrateJSONRequestWithMappings(t, root, "auto", "", mappingsPath, true)
	numbered, numberedErr := os.ReadFile(numberedPath)
	bullet, bulletErr := os.ReadFile(bulletPath)

	// Assert.
	if code != 0 || stderr != "" || !report.Applied || report.Actor != "" || report.Receipt == nil {
		t.Fatalf("raw-selector apply code/stderr/report = %d/%q/%#v", code, stderr, report)
	}
	if numberedErr != nil || !bytes.Contains(numbered, []byte("Claim [^spec].\r\n")) ||
		!bytes.Contains(numbered, []byte("[^spec]: [Spec](https://spec.example)\r\n")) {
		t.Fatalf("numbered CRLF result/read error = %q/%v", numbered, numberedErr)
	}
	if bulletErr != nil || !bytes.Contains(bullet, []byte("[^bullet]: https://bullet.example\r\n")) {
		t.Fatalf("unnumbered bullet result/read error = %q/%v", bullet, bulletErr)
	}
}

func TestMigrateNormalizedMappingCollisionBlocksTargetNoopWithoutWrites(t *testing.T) {
	for _, write := range []bool{false, true} {
		name := "dry-run"
		if write {
			name = "write"
		}
		t.Run(name, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			writeFixtureFile(t, filepath.Join(root, "index.md"),
				"---\nokf_version: \"0.2\"\n---\n\n# Knowledge\n\n- [Concept](concept.md)\n")
			writeFixtureFile(t, filepath.Join(root, "concept.md"),
				"---\ntype: Knowledge\n---\nBody.\n")
			mappingsPath := filepath.Join(root, "mappings.json")
			writeFixtureFile(t, mappingsPath, `[{"path":"concept.md","entries":[
				{"legacy_number":1,"source_id":"Spec"},
				{"legacy_number":2,"source_id":"spec"}
			]}]`)
			before := snapshotFilesystemTree(t, root)

			// Act.
			code, report, stderr := runMigrateJSONRequestWithMappings(
				t, root, "auto", "", mappingsPath, write,
			)
			after := snapshotFilesystemTree(t, root)
			_, storeErr := os.Stat(filepath.Join(root, ".okf"))

			// Assert.
			if code != 1 || stderr != "" || report.Outcome != "blocked" ||
				report.Applied || report.PlanDigest != "" ||
				len(report.Blockers) != 1 ||
				report.Blockers[0].Code != "normalized_footnote_label_collision" ||
				report.Blockers[0].Path != "concept.md" ||
				len(report.ManualActions) != 1 ||
				report.ManualActions[0].Code != "disambiguate_citation_entry" ||
				report.ManualActions[0].Path != "concept.md" {
				t.Fatalf("target-noop collision code/stderr/report = %d/%q/%#v", code, stderr, report)
			}
			if !reflect.DeepEqual(before, after) || !os.IsNotExist(storeErr) {
				t.Fatalf("target-noop collision changed tree/opened store: unchanged=%t storeErr=%v",
					reflect.DeepEqual(before, after), storeErr)
			}
		})
	}
}

func TestMigrateResolvesOnceAndRejectsFrozenResolutionTamperBeforeStore(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("durable filesystem store is unsupported on this platform")
	}

	tests := []struct {
		name           string
		args           []string
		index          string
		note           string
		tamper         bool
		wantCode       int
		wantTransition string
		wantNoop       bool
		wantApplied    bool
		wantFuture     bool
		wantProof      bool
		wantStore      bool
		wantChanged    bool
	}{
		{
			name:           "rootless default target",
			args:           []string{"--from", "auto"},
			note:           "---\ntype: Knowledge\n---\nTarget-shaped body.\n",
			wantTransition: "target-noop",
			wantNoop:       true,
			wantProof:      true,
		},
		{
			name:           "declared target",
			args:           []string{"--from", "auto"},
			index:          "---\nokf_version: \"0.2\"\n---\n\n# Knowledge\n\n- [Note](note.md)\n",
			note:           "---\ntype: Knowledge\n---\nTarget-shaped body.\n",
			wantTransition: "target-noop",
			wantNoop:       true,
			wantProof:      true,
		},
		{
			name:           "explicit source remains transition on target-shaped bytes",
			args:           []string{"--from", "0.1"},
			note:           "---\ntype: Knowledge\n---\nTarget-shaped body.\n",
			wantTransition: "v0.1-to-v0.2",
			wantNoop:       true,
			wantProof:      true,
		},
		{
			name:           "legacy transition write",
			args:           []string{"--from", "auto", "--write"},
			index:          "---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [Note](note.md)\n",
			note:           "---\ntype: Knowledge\nx-keep: true\n---\nBody.\n",
			wantTransition: "v0.1-to-v0.2",
			wantApplied:    true,
			wantProof:      true,
			wantStore:      true,
			wantChanged:    true,
		},
		{
			name:           "future source is blocked",
			args:           []string{"--from", "auto"},
			index:          "---\nokf_version: \"0.3\"\n---\n\n# Knowledge\n",
			wantCode:       1,
			wantTransition: "blocked",
			wantFuture:     true,
		},
		{
			name:   "tampered declaration state",
			args:   []string{"--from", "auto"},
			note:   "---\ntype: Knowledge\n---\nTarget-shaped body.\n",
			tamper: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			if test.index != "" {
				writeFixtureFile(t, filepath.Join(root, "index.md"), test.index)
			}
			if test.note != "" {
				writeFixtureFile(t, filepath.Join(root, "note.md"), test.note)
			}
			before := snapshotFilesystemTree(t, root)
			calls := 0
			resolver := func(
				ctx context.Context,
				source bundle.Source,
				options mutation.MigrationSourceOptions,
			) (mutation.MigrationSourceResolution, error) {
				calls++
				resolution, err := mutation.ResolveMigrationSource(ctx, source, options)
				if test.tamper && err == nil {
					resolution.DeclarationRaw = "forged"
				}
				return resolution, err
			}
			args := append([]string{"migrate", root, "--to", "0.2", "--format", "json"}, test.args...)
			var stdout bytes.Buffer

			// Act.
			code, err := cmdMigrateWithResolver(args[1:], &stdout, resolver)
			after := snapshotFilesystemTree(t, root)
			_, storeErr := os.Stat(filepath.Join(root, ".okf"))

			// Assert.
			if calls != 1 {
				t.Fatalf("source resolver calls = %d, want exactly one", calls)
			}
			if test.tamper {
				if err == nil || code != 0 || stdout.Len() != 0 {
					t.Fatalf("tampered resolution code/stdout/error = %d/%q/%v, want 0/empty/error",
						code, stdout.String(), err)
				}
			} else {
				var report migrationReport
				decodeErr := json.Unmarshal(stdout.Bytes(), &report)
				if err != nil || code != test.wantCode || decodeErr != nil ||
					report.Transition != test.wantTransition ||
					report.Noop != test.wantNoop ||
					report.Applied != test.wantApplied ||
					report.Future != test.wantFuture {
					t.Fatalf("migration code/error/decode/report = %d/%v/%v/%#v",
						code, err, decodeErr, report)
				}
				hasProof := report.ProofFormatVersion != 0 || report.ResolutionDigest != ""
				if hasProof != test.wantProof {
					t.Fatalf("migration proof presence = %t, want %t; report=%#v",
						hasProof, test.wantProof, report)
				}
				if test.wantProof &&
					(report.ProofFormatVersion != 2 || report.ResolutionDigest == "") {
					t.Fatalf("migration proof format/digest = %d/%q, want v2/non-empty",
						report.ProofFormatVersion, report.ResolutionDigest)
				}
			}
			changed := !reflect.DeepEqual(before, after)
			hasStore := storeErr == nil
			if changed != test.wantChanged || hasStore != test.wantStore {
				t.Fatalf("resolver workflow changed/store = %t/%t, want %t/%t; storeErr=%v",
					changed, hasStore, test.wantChanged, test.wantStore, storeErr)
			}
		})
	}
}

func TestMigrateReceiptUsesFrozenLexicalV1RefOrderAndCanonicalJSONWire(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("durable filesystem store is unsupported on this platform")
	}

	// Arrange.
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "index.md"),
		"---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n"+
			"- [Fragment owner](a.md)\n"+
			"- [Escaped root](a%23x.md)\n"+
			"- [Upper](aZ.md)\n")
	writeFixtureFile(t, filepath.Join(root, "a.md"),
		"---\ntype: Knowledge\ntimestamp: 2026-06-24T09:00:00Z\nfields:\n  - id: x\n---\n\nFragment owner.\n")
	writeFixtureFile(t, filepath.Join(root, "a#x.md"),
		"---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n---\n\nEscaped root.\n")
	writeFixtureFile(t, filepath.Join(root, "aZ.md"),
		"---\ntype: Knowledge\ntimestamp: 2026-06-26T09:00:00Z\n---\n\nUpper.\n")
	run := func(t *testing.T, write bool) (int, migrationReport, string, string) {
		t.Helper()
		args := []string{
			"migrate", root,
			"--from", "auto",
			"--to", "0.2",
			"--actor", "process:migration",
			"--format", "json",
		}
		if write {
			args = append(args, "--write")
		}
		var stdout, stderr bytes.Buffer
		code := Run(args, &stdout, &stderr)
		var report migrationReport
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatalf("json.Unmarshal(migrate) error = %v; stdout=%q stderr=%q",
				err, stdout.String(), stderr.String())
		}
		return code, report, stdout.String(), stderr.String()
	}

	// Act.
	previewCode, preview, _, previewStderr := run(t, false)
	applyCode, applied, appliedJSON, applyStderr := run(t, true)
	replayCode, replay, _, replayStderr := run(t, true)

	// Assert.
	if previewCode != 0 || previewStderr != "" || preview.Outcome != "preview" ||
		preview.PlanDigest == "" ||
		preview.ProofFormatVersion != mutation.MigrationPlanProofFormatVersion ||
		preview.ResolutionDigest == "" ||
		preview.Receipt != nil {
		t.Fatalf("preview code/stderr/report = %d/%q/%#v", previewCode, previewStderr, preview)
	}
	if applyCode != 0 || applyStderr != "" || !applied.Applied ||
		applied.Receipt == nil || applied.PlanDigest != preview.PlanDigest ||
		applied.ProofFormatVersion != mutation.MigrationPlanProofFormatVersion ||
		applied.ResolutionDigest != preview.ResolutionDigest {
		t.Fatalf("apply code/stderr/report = %d/%q/%#v", applyCode, applyStderr, applied)
	}
	wantRefs := []string{"a", "a#x", "aZ", `a\#x`}
	if !reflect.DeepEqual(applied.Receipt.ChangedRefs, wantRefs) {
		t.Fatalf("receipt changed_refs = %#v, want frozen lexical v1 wire order %#v",
			applied.Receipt.ChangedRefs, wantRefs)
	}
	if !strings.Contains(appliedJSON, `"changed_refs":["a","a#x","aZ","a\\#x"]`) {
		t.Fatalf("receipt JSON does not preserve frozen lexical v1 order and canonical escaping:\n%s", appliedJSON)
	}
	if replayCode != 0 || replayStderr != "" || !replay.Noop || !replay.Applied ||
		replay.Receipt == nil || replay.Outcome != "applied" || replay.PlanDigest == "" ||
		replay.ResolutionDigest == "" || replay.Base == "" || replay.Result != replay.Base ||
		len(replay.Receipt.ChangedFiles) != 0 || len(replay.Receipt.ChangedRefs) != 0 {
		t.Fatalf("replay code/stderr/report = %d/%q/%#v", replayCode, replayStderr, replay)
	}
}

func TestMigrateOneBadFileReturnsBlockerAndZeroWrites(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	indexPath := filepath.Join(root, "index.md")
	goodPath := filepath.Join(root, "good.md")
	badPath := filepath.Join(root, "bad.md")
	writeFixtureFile(t, indexPath, "---\nokf_version: \"0.1\"\n---\n\n# Concepts\n\n* [Good](good.md)\n* [Bad](bad.md)\n")
	writeFixtureFile(t, goodPath, "---\ntype: Note\ntimestamp: 2026-06-01T10:00:00Z\n---\n\nGood.\n")
	writeFixtureFile(t, badPath, "---\ntype: Note\ntimestamp: never\n---\n\nBad.\n")
	indexBefore, _ := os.ReadFile(indexPath)
	goodBefore, _ := os.ReadFile(goodPath)
	badBefore, _ := os.ReadFile(badPath)

	// Act.
	code, report, stderr := runMigrateJSON(t, root, false)
	indexAfter, _ := os.ReadFile(indexPath)
	goodAfter, _ := os.ReadFile(goodPath)
	badAfter, _ := os.ReadFile(badPath)

	// Assert.
	if code != 1 || stderr != "" || len(report.Blockers) == 0 {
		t.Fatalf("migration code/stderr/report = %d/%q/%#v, want blocker", code, stderr, report)
	}
	if !bytes.Equal(indexBefore, indexAfter) || !bytes.Equal(goodBefore, goodAfter) || !bytes.Equal(badBefore, badAfter) {
		t.Fatal("blocked migration changed revision-visible bytes")
	}
}

func TestMigrateCanonicalLegacyCitationsRequiresExplicitMapping(t *testing.T) {
	// Arrange.
	root := fixturePath(t, "compat", "declared-v01")

	// Act.
	code, report, stderr := runMigrateJSON(t, root, false)

	// Assert.
	if code != 1 || stderr != "" {
		t.Fatalf("migration code/stderr = %d/%q, want 1/empty", code, stderr)
	}
	if !containsMigrationBlocker(report.Blockers, "missing_explicit_citation_mapping") {
		t.Fatalf("blockers = %#v, missing explicit-citation blocker", report.Blockers)
	}
	if len(report.ManualActions) != 1 || report.ManualActions[0].Code != "provide_citation_mapping" {
		t.Fatalf("manual_actions = %#v, want provide_citation_mapping", report.ManualActions)
	}
}

func TestMigrateMissingActorReturnsDomainManualAction(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "index.md"),
		"---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [Legacy](legacy.md)\n")
	writeFixtureFile(t, filepath.Join(root, "legacy.md"),
		"---\ntype: Knowledge\ntimestamp: 2026-06-25T09:00:00Z\n---\n")
	var stdout, stderr bytes.Buffer

	// Act.
	code := Run([]string{
		"migrate", root,
		"--to", "0.2",
		"--from", "auto",
		"--format", "json",
	}, &stdout, &stderr)

	// Assert.
	if code != 1 || stderr.Len() != 0 {
		t.Fatalf("migration code/stderr = %d/%q, want 1/empty", code, stderr.String())
	}
	var report migrationReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; stdout=%q", err, stdout.String())
	}
	if !containsMigrationBlocker(report.Blockers, "missing_explicit_generated_by") {
		t.Fatalf("blockers = %#v, missing generated-by blocker", report.Blockers)
	}
	if len(report.ManualActions) != 1 || report.ManualActions[0].Code != "provide_generated_by" {
		t.Fatalf("manual_actions = %#v, want provide_generated_by", report.ManualActions)
	}
}
