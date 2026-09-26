package okfcli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMigratePrepareExportsFillableInputsAndPreservesBundleOnCollision(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	parent := t.TempDir()
	output := filepath.Join(parent, "prepared")
	writeFixtureFile(t, filepath.Join(root, "index.md"), "---\nokf_version: '0.1'\n---\n\n# Knowledge\n\n- [Concept](concept.md)\n")
	writeFixtureFile(t, filepath.Join(root, "concept.md"), "---\ntype: Knowledge\n---\n\nClaim [1].\n\n# Citations\n\n[1] https://example.test/source\n")
	before := snapshotFilesystemTree(t, root)
	var stdout, stderr bytes.Buffer

	// Act.
	code := Run([]string{"migrate-prepare", root, "--output-dir", output, "--format", "json"}, &stdout, &stderr)
	after := snapshotFilesystemTree(t, root)
	var report migrationPreparationReport
	decodeErr := json.Unmarshal(stdout.Bytes(), &report)
	citationData, citationErr := os.ReadFile(filepath.Join(output, "citation-mappings.json"))
	instantData, instantErr := os.ReadFile(filepath.Join(output, "generated-at.json"))
	var secondOut, secondErr bytes.Buffer
	secondCode := Run([]string{"migrate-prepare", root, "--output-dir", output}, &secondOut, &secondErr)
	secondCitation, secondReadErr := os.ReadFile(filepath.Join(output, "citation-mappings.json"))
	alias := filepath.Join(parent, "bundle-alias")
	if err := os.Symlink(root, alias); err == nil {
		var aliasOut, aliasErr bytes.Buffer
		if aliasCode := Run([]string{"migrate-prepare", root, "--output-dir", filepath.Join(alias, "prepared")}, &aliasOut, &aliasErr); aliasCode != 1 {
			t.Fatalf("output inside bundle accepted through alias: %d/%q", aliasCode, aliasErr.String())
		}
	}

	// Assert.
	if code != 0 || stderr.Len() != 0 || decodeErr != nil || citationErr != nil || instantErr != nil ||
		!reflect.DeepEqual(before, after) || len(report.SourceSHA256) != 64 {
		t.Fatalf("prepare code/stderr/report/files/bundle = %d/%q/%v/%v/%v/%t", code, stderr.String(), decodeErr, citationErr, instantErr, reflect.DeepEqual(before, after))
	}
	if _, err := parseCitationMappings(citationData); err == nil {
		t.Fatal("blank citation source ID unexpectedly accepted")
	}
	if _, err := parseGeneratedAtMappings(instantData); err == nil {
		t.Fatal("blank historical instant unexpectedly accepted")
	}
	var unconfirmedOut, unconfirmedErr bytes.Buffer
	unconfirmedCode := Run([]string{
		"migrate", root, "--to", "0.2", "--actor", "human:test",
		"--citation-mappings", filepath.Join(output, "citation-mappings.json"),
		"--generated-at", filepath.Join(output, "generated-at.json"), "--write",
	}, &unconfirmedOut, &unconfirmedErr)
	if unconfirmedCode != 1 || unconfirmedOut.Len() != 0 || unconfirmedErr.Len() == 0 ||
		!reflect.DeepEqual(before, snapshotFilesystemTree(t, root)) {
		t.Fatalf("unconfirmed prepare input wrote bundle: %d/%q/%q", unconfirmedCode, unconfirmedOut.String(), unconfirmedErr.String())
	}
	if secondCode != 1 || secondOut.Len() != 0 || secondErr.Len() == 0 || secondReadErr != nil ||
		!bytes.Equal(citationData, secondCitation) || !reflect.DeepEqual(before, snapshotFilesystemTree(t, root)) {
		t.Fatalf("collision modified prior template or bundle: code=%d stderr=%q", secondCode, secondErr.String())
	}
	if _, err := os.Stat(filepath.Join(root, ".okf")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("preparation created private store: %v", err)
	}
	writeFixtureFile(t, filepath.Join(root, "concept.md"), "---\ntype: Knowledge\n---\n\nChanged.\n")
	var staleOut, staleErr bytes.Buffer
	staleCode := Run([]string{"migrate", root, "--to", "0.2", "--actor", "human:test", "--prepared-source-sha256", report.SourceSHA256, "--format", "json"}, &staleOut, &staleErr)
	if staleCode != 1 || staleOut.Len() != 0 || !bytes.Contains(staleErr.Bytes(), []byte("stale")) {
		t.Fatalf("stale preparation accepted: %d/%q/%q", staleCode, staleOut.String(), staleErr.String())
	}
	if _, err := os.Stat(filepath.Join(root, ".okf")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale preparation opened store: %v", err)
	}
}

func TestMigrateExplicitGeneratedAtUsesExistingPreviewApply(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	parent := t.TempDir()
	output := filepath.Join(parent, "prepared")
	writeFixtureFile(t, filepath.Join(root, "index.md"), "---\nokf_version: '0.1'\n---\n\n# Knowledge\n\n- [Concept](concept.md)\n")
	writeFixtureFile(t, filepath.Join(root, "concept.md"), "---\r\ntype: Knowledge\r\nx-opaque: \"keep-\uFEFF-value\"\r\n---\r\n\r\nClaim [1] with \uFEFFliteral.\r\n\r\n# Citations\r\n\r\n[1] https://example.test/source\r\n")
	var prepOut, prepErr bytes.Buffer
	if code := Run([]string{"migrate-prepare", root, "--output-dir", output}, &prepOut, &prepErr); code != 0 {
		t.Fatalf("prepare = %d, %s", code, prepErr.String())
	}
	citationPath := filepath.Join(output, "citation-mappings.json")
	generatedAtPath := filepath.Join(output, "generated-at.json")
	citationData, err := os.ReadFile(citationPath)
	if err != nil {
		t.Fatal(err)
	}
	var citationTemplate migrationMappingTemplate
	if err := json.Unmarshal(citationData, &citationTemplate); err != nil {
		t.Fatal(err)
	}
	citationTemplate[0].Entries[0].SourceID = "source"
	confirmedCitation, err := json.Marshal(citationTemplate)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(citationPath, confirmedCitation, 0o600); err != nil {
		t.Fatal(err)
	}
	confirmedTime := []byte(`[{"path":"concept.md","at":"2026-01-01T00:00:00Z"}]`)
	if err := os.WriteFile(generatedAtPath, confirmedTime, 0o600); err != nil {
		t.Fatal(err)
	}
	before := snapshotFilesystemTree(t, root)
	var report migrationPreparationReport
	// The report is a freshness input only; it does not authorize preview/apply.
	prepReportData, err := os.ReadFile(filepath.Join(output, "preparation-report.json"))
	if err != nil || json.Unmarshal(prepReportData, &report) != nil {
		t.Fatalf("read preparation report: %v", err)
	}
	args := []string{"migrate", root, "--to", "0.2", "--actor", "human:test", "--citation-mappings", citationPath, "--generated-at", generatedAtPath, "--prepared-source-sha256", report.SourceSHA256, "--format", "json"}
	var previewOut, previewErr bytes.Buffer

	// Act.
	previewCode := Run(args, &previewOut, &previewErr)
	previewTree := snapshotFilesystemTree(t, root)
	var preview migrationReport
	decodeErr := json.Unmarshal(previewOut.Bytes(), &preview)
	var applyOut, applyErr bytes.Buffer
	applyCode := Run(append(args, "--write"), &applyOut, &applyErr)
	var applied migrationReport
	applyDecodeErr := json.Unmarshal(applyOut.Bytes(), &applied)

	// Assert.
	if previewCode != 0 || previewErr.Len() != 0 || decodeErr != nil ||
		preview.Outcome == "blocked" || preview.PlanDigest == "" ||
		!reflect.DeepEqual(before, previewTree) {
		t.Fatalf("preview code/error/report/unchanged = %d/%q/%#v/%t", previewCode, previewErr.String(), preview, reflect.DeepEqual(before, previewTree))
	}
	if applyCode != 0 || applyErr.Len() != 0 || applyDecodeErr != nil || !applied.Applied {
		t.Fatalf("apply code/error/report = %d/%q/%#v", applyCode, applyErr.String(), applied)
	}
	content, err := os.ReadFile(filepath.Join(root, "concept.md"))
	if err != nil || !bytes.Contains(content, []byte("2026-01-01T00:00:00Z")) ||
		!bytes.Contains(content, []byte("x-opaque: \"keep-\uFEFF-value\"\r\n")) ||
		!bytes.Contains(content, []byte("Claim [^source] with \uFEFFliteral.\r\n")) {
		t.Fatalf("explicit generated time missing: %v, %s", err, content)
	}
}

func TestMigrationPreparationPublishesThroughPinnedParentAfterAliasSwap(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	outside := t.TempDir()
	aliasParent := t.TempDir()
	alias := filepath.Join(aliasParent, "output-parent")
	if err := os.Symlink(outside, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	output := filepath.Join(alias, "prepared")
	rootBefore := snapshotFilesystemTree(t, root)
	template := migrationMappingTemplate{}
	report := migrationPreparationReport{SourceSHA256: "test", GeneratedAt: []migrationGeneratedAtTemplate{}}

	// Act. Swap the caller-visible alias after the parent directory is pinned.
	err := writeMigrationPreparationDirectoryWithHook(root, output, template, report, func() error {
		if err := os.Remove(alias); err != nil {
			return err
		}
		return os.Symlink(root, alias)
	})
	_, outsideErr := os.Stat(filepath.Join(outside, "prepared", "citation-mappings.json"))
	_, bundleErr := os.Stat(filepath.Join(root, "prepared"))
	rootAfter := snapshotFilesystemTree(t, root)

	// Assert.
	if err != nil || outsideErr != nil || !errors.Is(bundleErr, os.ErrNotExist) ||
		!reflect.DeepEqual(rootBefore, rootAfter) {
		t.Fatalf("output escaped pinned parent: err=%v outside=%v bundle=%v unchanged=%t",
			err, outsideErr, bundleErr, reflect.DeepEqual(rootBefore, rootAfter))
	}
}

func TestMigrationPreparationFailureCleansOnlyPinnedStage(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	outside := t.TempDir()
	marker := errors.New("injected pre-publication failure")
	beforeRoot := snapshotFilesystemTree(t, root)

	// Act.
	err := writeMigrationPreparationDirectoryWithHook(
		root, filepath.Join(outside, "prepared"), migrationMappingTemplate{},
		migrationPreparationReport{GeneratedAt: []migrationGeneratedAtTemplate{}},
		func() error { return marker },
	)
	entries, readErr := os.ReadDir(outside)

	// Assert.
	if !errors.Is(err, marker) || readErr != nil || len(entries) != 0 ||
		!reflect.DeepEqual(beforeRoot, snapshotFilesystemTree(t, root)) {
		t.Fatalf("failed output left stage or changed bundle: %v / %v / %#v", err, readErr, entries)
	}
}

func TestLeadingBOMMigrationFailsClosedWithoutChangingBytes(t *testing.T) {
	// Arrange. A leading UTF-8 BOM prevents the exact frontmatter delimiter
	// from being recognized; preparation reports that precise unsupported form.
	root := t.TempDir()
	outside := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "index.md"), "---\nokf_version: '0.1'\n---\n\n# Knowledge\n\n- [BOM](bom.md)\n")
	writeFixtureFile(t, filepath.Join(root, "bom.md"), "\uFEFF---\ntype: Knowledge\ntimestamp: 2026-01-01T00:00:00Z\n---\n\n# BOM\n")
	before := snapshotFilesystemTree(t, root)
	var prepareOut, prepareErr bytes.Buffer

	// Act.
	prepareCode := Run([]string{"migrate-prepare", root, "--output-dir", filepath.Join(outside, "prepared"), "--format", "json"}, &prepareOut, &prepareErr)
	var report migrationPreparationReport
	decodeErr := json.Unmarshal(prepareOut.Bytes(), &report)
	var migrateOut, migrateErr bytes.Buffer
	migrateCode := Run([]string{"migrate", root, "--to", "0.2", "--actor", "human:test", "--write", "--format", "json"}, &migrateOut, &migrateErr)
	after := snapshotFilesystemTree(t, root)
	_, storeErr := os.Stat(filepath.Join(root, ".okf"))

	// Assert.
	if prepareCode != 0 || prepareErr.Len() != 0 || decodeErr != nil || len(report.Unresolved) == 0 ||
		report.Unresolved[0].Reason != "generated.by requires an explicit producer actor (--actor)" {
		t.Fatalf("BOM preparation = %d/%q/%#v", prepareCode, prepareErr.String(), report)
	}
	foundBOM := false
	for _, unresolved := range report.Unresolved {
		if unresolved.Path == "bom.md" && unresolved.Reason == "leading_bom_unsupported" && unresolved.StartByte == 0 && unresolved.EndByte == 3 {
			foundBOM = true
		}
	}
	if !foundBOM || migrateCode == 0 || !reflect.DeepEqual(before, after) || !errors.Is(storeErr, os.ErrNotExist) {
		t.Fatalf("leading BOM was not fail-closed: found=%t code=%d stderr=%q unchanged=%t store=%v", foundBOM, migrateCode, migrateErr.String(), reflect.DeepEqual(before, after), storeErr)
	}
}
