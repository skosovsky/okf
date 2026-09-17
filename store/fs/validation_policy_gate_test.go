package fs

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
	"github.com/skosovsky/okf/validator"
)

func TestReplaceConceptRejectsExplicitSpecPolicyFailureWithoutDurableSideEffects(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writePolicyGateFixture(t, root, false)
	s, err := openObserved(root, Config{ValidatorConfig: &validator.ValidatorConfig{Spec: bundle.LegacyOKFVersion}})
	if err != nil {
		t.Fatal(err)
	}
	registerStoreCleanup(t, s)
	before := policyGateVisibleFiles(t, root)
	base := adversarialSnapshot(t, s)
	document := policyGateDocument(t, "---\ntype: Note\n---\n\nPolicy rejected\n")
	request := ReplaceConceptRequest{
		ChangeSetID:  "replace-policy-rejected",
		Actor:        "policy-test",
		BaseRevision: base.Revision(),
		ConceptID:    adversarialRef(t, "a").ID,
		Document:     document,
	}
	options := store.CommitOptions{IdempotencyKey: "replace-policy-rejected"}
	serialized, err := document.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := replaceDigest(request, serialized)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := journalStage(digest)
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	result, replaceErr := s.ReplaceConcept(context.Background(), request, options)
	after := policyGateVisibleFiles(t, root)
	_, receiptErr := os.Stat(filepath.Join(root, filepath.FromSlash(s.receiptPath(options.IdempotencyKey))))
	_, stageErr := os.Stat(filepath.Join(root, filepath.FromSlash(path.Dir(stage))))
	journals := pendingJournalNames(t, root)

	// Assert.
	var invalid *store.InvalidChangeSet
	if !errors.As(replaceErr, &invalid) || invalid.Code != "staged_validation_failed" {
		t.Fatalf("ReplaceConcept() error = %#v, want staged_validation_failed", replaceErr)
	}
	if !result.Validation.IsConformant() ||
		!result.Validation.HasPolicyFailures() ||
		result.Validation.ExitCode() == 0 ||
		!hasPolicyGateDiagnostic(result.Validation, "version_assertion_failed", true) {
		t.Fatalf("validation = %#v, want conformant explicit Spec policy failure", result.Validation)
	}
	if !reflect.DeepEqual(after, before) || !result.Receipt.ResultRevision.IsZero() {
		t.Fatalf("policy rejection changed visible state or returned receipt: before=%#v after=%#v receipt=%#v", before, after, result.Receipt)
	}
	if !errors.Is(receiptErr, os.ErrNotExist) {
		t.Fatalf("policy rejection persisted receipt: %v", receiptErr)
	}
	if !errors.Is(stageErr, os.ErrNotExist) {
		t.Fatalf("policy rejection created transaction stage: %v", stageErr)
	}
	if len(journals) != 0 {
		t.Fatalf("policy rejection persisted journals: %v", journals)
	}
}

func TestRecoveryRejectsPolicyFailureBeforeVisibleApplyAndMatchingConfigRecovers(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writePolicyGateFixture(t, root, false)
	before := policyGateVisibleFiles(t, root)
	request, serialized := stagePendingPolicyReplace(
		t,
		root,
		&validator.ValidatorConfig{Spec: bundle.OKFVersion},
		"recovery-policy-gate",
		"---\ntype: Note\n---\n\nRecovered only by matching policy\n",
	)
	durable := readOnlyPendingJournal(t, root)
	if durable.Receipt.ChangeSetID != request.ChangeSetID {
		t.Fatalf("durable journal change=%q want=%q", durable.Receipt.ChangeSetID, request.ChangeSetID)
	}

	// Act.
	_, mismatchErr := openObserved(root, Config{ValidatorConfig: &validator.ValidatorConfig{Spec: bundle.LegacyOKFVersion}})
	afterMismatch := policyGateVisibleFiles(t, root)
	journalsAfterMismatch := pendingJournalNames(t, root)
	matching, matchingErr := openObserved(root, Config{ValidatorConfig: &validator.ValidatorConfig{Spec: bundle.OKFVersion}})
	if matchingErr != nil {
		t.Fatal(matchingErr)
	}
	registerStoreCleanup(t, matching)
	recovered, snapshotErr := matching.Snapshot(context.Background())
	recoveredBytes, readErr := recovered.ReadFile(context.Background(), "a.md")
	journalsAfterRecovery := pendingJournalNames(t, root)

	// Assert.
	if !errors.Is(mismatchErr, store.ErrStorageCorrupt) ||
		!strings.Contains(mismatchErr.Error(), "journal staged post-state fails validation") {
		t.Fatalf("mismatched recovery error = %v, want policy-gated storage corruption", mismatchErr)
	}
	if !reflect.DeepEqual(afterMismatch, before) {
		t.Fatalf("mismatched recovery changed visible state: before=%#v after=%#v", before, afterMismatch)
	}
	if got, want := journalsAfterMismatch, []string{path.Base(mustJournalPath(t, durable.Receipt.RequestDigest))}; !reflect.DeepEqual(got, want) {
		t.Fatalf("journals after policy rejection = %v, want recoverable %v", got, want)
	}
	if snapshotErr != nil || readErr != nil || !bytes.Equal(recoveredBytes, []byte(serialized)) {
		t.Fatalf("matching recovery snapshot=%v read=%v bytes=%q want=%q", snapshotErr, readErr, recoveredBytes, serialized)
	}
	if recovered.Revision() != durable.Receipt.ResultRevision {
		t.Fatalf("matching recovery revision=%s want=%s", recovered.Revision(), durable.Receipt.ResultRevision)
	}
	if len(journalsAfterRecovery) != 0 {
		t.Fatalf("matching recovery left journals: %v", journalsAfterRecovery)
	}
}

func TestOrdinaryStrictWarningRemainsNonBlockingForReplaceAndRecovery(t *testing.T) {
	t.Run("ReplaceConcept", func(t *testing.T) {
		// Arrange.
		root := t.TempDir()
		writePolicyGateFixture(t, root, true)
		s, err := openObserved(root, Config{ValidatorConfig: &validator.ValidatorConfig{Strict: true, Spec: bundle.OKFVersion}})
		if err != nil {
			t.Fatal(err)
		}
		registerStoreCleanup(t, s)
		base := adversarialSnapshot(t, s)
		document := policyGateDocument(t, "---\ntype: Note\nstatus: archived\n---\n\nStrict warning committed\n")
		request := ReplaceConceptRequest{
			ChangeSetID:  "replace-strict-warning",
			Actor:        "policy-test",
			BaseRevision: base.Revision(),
			ConceptID:    adversarialRef(t, "a").ID,
			Document:     document,
		}
		serialized, err := document.Serialize()
		if err != nil {
			t.Fatal(err)
		}

		// Act.
		result, replaceErr := s.ReplaceConcept(context.Background(), request, store.CommitOptions{})
		after, readErr := os.ReadFile(filepath.Join(root, "a.md"))

		// Assert.
		if replaceErr != nil || readErr != nil {
			t.Fatalf("strict-warning replacement error=%v read=%v", replaceErr, readErr)
		}
		if !result.Validation.IsConformant() ||
			result.Validation.HasPolicyFailures() ||
			result.Validation.ExitCode() != 0 ||
			!hasPolicyGateDiagnostic(result.Validation, "status_invalid", false) {
			t.Fatalf("validation = %#v, want ordinary nonblocking strict warning", result.Validation)
		}
		if !bytes.Equal(after, []byte(serialized)) || result.Receipt.ResultRevision.IsZero() {
			t.Fatalf("strict warning did not commit: bytes=%q want=%q receipt=%#v", after, serialized, result.Receipt)
		}
	})

	t.Run("recovery", func(t *testing.T) {
		// Arrange.
		root := t.TempDir()
		writePolicyGateFixture(t, root, true)
		_, serialized := stagePendingPolicyReplace(
			t,
			root,
			&validator.ValidatorConfig{Strict: true, Spec: bundle.OKFVersion},
			"recovery-strict-warning",
			"---\ntype: Note\nstatus: archived\n---\n\nStrict warning recovered\n",
		)
		durable := readOnlyPendingJournal(t, root)

		// Act.
		reopened, recoveryErr := openObserved(root, Config{ValidatorConfig: &validator.ValidatorConfig{Strict: true, Spec: bundle.OKFVersion}})
		if recoveryErr != nil {
			t.Fatal(recoveryErr)
		}
		registerStoreCleanup(t, reopened)
		recovered, snapshotErr := reopened.Snapshot(context.Background())
		recoveredBytes, readErr := recovered.ReadFile(context.Background(), "a.md")
		journals := pendingJournalNames(t, root)

		// Assert.
		if snapshotErr != nil || readErr != nil || !bytes.Equal(recoveredBytes, []byte(serialized)) {
			t.Fatalf("strict-warning recovery snapshot=%v read=%v bytes=%q want=%q", snapshotErr, readErr, recoveredBytes, serialized)
		}
		if recovered.Revision() != durable.Receipt.ResultRevision {
			t.Fatalf("strict-warning recovery revision=%s want=%s", recovered.Revision(), durable.Receipt.ResultRevision)
		}
		if len(journals) != 0 {
			t.Fatalf("strict-warning recovery left journals: %v", journals)
		}
	})
}

func stagePendingPolicyReplace(
	t *testing.T,
	root string,
	validation *validator.ValidatorConfig,
	id string,
	documentText string,
) (ReplaceConceptRequest, string) {
	t.Helper()
	armed := false
	fired := false
	journalRenamed := false
	config := Config{
		ValidatorConfig: validation,
		PostFault: func(step Step) error {
			if step == StepJournalRename {
				journalRenamed = true
			}
			if armed && journalRenamed && step == StepJournalDirectorySync && !fired {
				fired = true
				return errors.New("crash after durable journal")
			}
			return nil
		},
	}
	writer, err := openObserved(root, config)
	if err != nil {
		t.Fatal(err)
	}
	base := adversarialSnapshot(t, writer)
	document := policyGateDocument(t, documentText)
	request := ReplaceConceptRequest{
		ChangeSetID:  store.ChangeSetID(id),
		Actor:        "policy-test",
		BaseRevision: base.Revision(),
		ConceptID:    adversarialRef(t, "a").ID,
		Document:     document,
	}
	serialized, err := document.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	armed = true
	_, replaceErr := writer.ReplaceConcept(
		context.Background(),
		request,
		store.CommitOptions{IdempotencyKey: store.IdempotencyKey(id)},
	)
	armed = false
	if replaceErr == nil || !fired {
		t.Fatalf("ReplaceConcept() crash error=%v fired=%v", replaceErr, fired)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if got := pendingJournalNames(t, root); len(got) != 1 {
		t.Fatalf("pending journals after crash = %v, want one", got)
	}
	return request, serialized
}

func writePolicyGateFixture(t *testing.T, root string, strictWarning bool) {
	t.Helper()
	status := ""
	if strictWarning {
		status = "status: archived\n"
	}
	writeTestFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n# Knowledge\n\n* [A](a.md)\n")
	writeTestFile(t, root, "a.md", "---\ntype: Note\n"+status+"---\n\nOriginal\n")
}

func policyGateDocument(t *testing.T, raw string) bundle.Document {
	t.Helper()
	document, err := bundle.ParseDocument(raw)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func policyGateVisibleFiles(t *testing.T, root string) map[string][]byte {
	t.Helper()
	files, err := readVisibleRoot(context.Background(), mustOpenRoot(t, root))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func hasPolicyGateDiagnostic(report validator.Report, code string, policyFailure bool) bool {
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Code == code &&
			diagnostic.Severity == validator.SeverityWarning &&
			diagnostic.PolicyFailure == policyFailure {
			return true
		}
	}
	return false
}

func mustJournalPath(t *testing.T, digest string) string {
	t.Helper()
	journalName, err := journalPath(digest)
	if err != nil {
		t.Fatal(err)
	}
	return journalName
}
