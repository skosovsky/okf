package store

import (
	"errors"
	"testing"
	"time"

	"github.com/skosovsky/okf/bundle"
)

type committedTestCause struct{ message string }

func (e *committedTestCause) Error() string { return e.message }

func TestCommittedErrorPreservesCauseAndValidReceipt(t *testing.T) {
	// Arrange.
	cause := &committedTestCause{message: "directory sync failed"}
	receipt := validCommittedTestReceipt(t)

	// Act.
	err := NewCommittedError(receipt, cause)
	var committed *CommittedError
	if !errors.As(err, &committed) {
		t.Fatalf("NewCommittedError() = %T, want *CommittedError", err)
	}
	gotReceipt := committed.Receipt()
	var gotCause *committedTestCause

	// Assert.
	if !errors.Is(err, cause) || !errors.As(err, &gotCause) || gotCause != cause {
		t.Fatalf("cause identity = (%t, %p), want (true, %p)", errors.Is(err, cause), gotCause, cause)
	}
	if validateErr := ValidateCommitReceipt(gotReceipt); validateErr != nil {
		t.Fatalf("ValidateCommitReceipt() error = %v", validateErr)
	}
	if gotReceipt.FormatVersion == 0 || gotReceipt.ResultRevision.IsZero() {
		t.Fatalf("receipt = %#v, want nonzero durable evidence", gotReceipt)
	}
}

func TestCommittedErrorOwnsReceiptDefensively(t *testing.T) {
	// Arrange.
	receipt := validCommittedTestReceipt(t)
	err := NewCommittedError(receipt, errors.New("late failure"))
	var committed *CommittedError
	if !errors.As(err, &committed) {
		t.Fatalf("NewCommittedError() = %T, want *CommittedError", err)
	}

	// Act.
	receipt.ChangedRefs[0] = bundle.RelationRef{}
	receipt.ChangedFiles[0].Path = "mutated-input.md"
	first := committed.Receipt()
	first.ChangedRefs[0] = bundle.RelationRef{}
	first.ChangedFiles[0].Path = "mutated-output.md"
	second := committed.Receipt()

	// Assert.
	if second.ChangedRefs[0].String() != "alpha" {
		t.Fatalf("stored ChangedRefs = %#v, want alpha", second.ChangedRefs)
	}
	if second.ChangedFiles[0].Path != "alpha.md" {
		t.Fatalf("stored ChangedFiles = %#v, want alpha.md", second.ChangedFiles)
	}
}

func TestCommittedErrorNilSafety(t *testing.T) {
	// Arrange.
	var nilError *CommittedError
	withoutCause := NewCommittedError(validCommittedTestReceipt(t), nil)

	// Act.
	nilMessage := nilError.Error()
	nilReceipt := nilError.Receipt()
	message := withoutCause.Error()

	// Assert.
	if nilMessage != "<nil>" || nilError.Unwrap() != nil || nilReceipt.FormatVersion != 0 ||
		len(nilReceipt.ChangedRefs) != 0 || len(nilReceipt.ChangedFiles) != 0 {
		t.Fatalf("nil methods = (%q, %v, %#v), want safe zero results", nilMessage, nilError.Unwrap(), nilReceipt)
	}
	var committed *CommittedError
	if message != "store committed error: nil cause" || errors.As(withoutCause, &committed) {
		t.Fatalf("nil-cause constructor = (%q, %T), want plain validation error", message, withoutCause)
	}
}

func TestNewCommittedErrorRejectsInvalidReceipt(t *testing.T) {
	// Arrange.
	receipt := CommitReceipt{}
	cause := errors.New("late failure")

	// Act.
	err := NewCommittedError(receipt, cause)
	var committed *CommittedError

	// Assert.
	if err == nil || errors.As(err, &committed) || errors.Is(err, cause) {
		t.Fatalf("NewCommittedError() = %v (%T), want plain receipt validation error", err, err)
	}
}

func validCommittedTestReceipt(t *testing.T) CommitReceipt {
	t.Helper()
	ref, err := bundle.ParseRelationRef("alpha")
	if err != nil {
		t.Fatal(err)
	}
	revision := Revision("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	return CommitReceipt{
		FormatVersion:  CommitReceiptFormatVersion,
		ChangeSetID:    "committed-change",
		IdempotencyKey: "committed-key",
		RequestDigest:  "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		BaseRevision:   revision,
		ResultRevision: revision,
		CommitTime:     time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
		ChangedRefs:    []bundle.RelationRef{ref},
		ChangedFiles:   []FileChange{{Kind: FileWrite, Path: "alpha.md"}},
	}
}
