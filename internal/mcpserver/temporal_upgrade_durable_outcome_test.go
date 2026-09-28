package mcpserver

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/mutation"
	"github.com/skosovsky/okf/store"
)

func TestClassifyMCPTemporalCommitOutcomeSeparatesDurableAndUncertainFailure(t *testing.T) {
	// Arrange.
	base := store.Revision("sha256:" + strings.Repeat("1", 64))
	planDigest := "sha256:" + strings.Repeat("2", 64)
	id, err := bundle.ParseConceptID("a")
	if err != nil {
		t.Fatal(err)
	}
	request := mutation.TemporalUpgradeRequest{
		ID: "upgrade-005", Actor: "human:reviewer",
		Mappings: []mutation.TemporalUpgradeMapping{{
			Concept: id, Path: "stale_after", From: "2026-01-02", To: "2026-01-02T12:00:00Z",
		}},
	}
	key, err := mutation.PlanDigestIdempotencyKey("temporal-upgrade:v1", planDigest, "")
	if err != nil {
		t.Fatal(err)
	}
	receipt := durableCommitReceipt(t, request.ID, key, base)
	receipt.RequestDigest, err = temporalUpgradeRequestDigest(request, base)
	if err != nil {
		t.Fatal(err)
	}
	committedErr := store.NewCommittedError(receipt, context.DeadlineExceeded)
	if committedErr == nil {
		t.Fatal("expected committed error")
	}
	foreign := receipt.Clone()
	foreign.ChangeSetID = "other-upgrade"
	wrongDigest := receipt.Clone()
	wrongDigest.RequestDigest = "sha256:" + strings.Repeat("3", 64)
	if wrongDigest.RequestDigest == receipt.RequestDigest {
		wrongDigest.RequestDigest = "sha256:" + strings.Repeat("4", 64)
	}

	// Act.
	durable, durableErr := classifyMCPTemporalCommitOutcome(request, base, planDigest, receipt, committedErr)
	uncertain, uncertainErr := classifyMCPTemporalCommitOutcome(request, base, planDigest, store.CommitReceipt{}, context.DeadlineExceeded)
	rejected, rejectedErr := classifyMCPTemporalCommitOutcome(request, base, planDigest, foreign, store.NewCommittedError(foreign, context.DeadlineExceeded))
	wrong, wrongErr := classifyMCPTemporalCommitOutcome(request, base, planDigest, wrongDigest, store.NewCommittedError(wrongDigest, context.DeadlineExceeded))

	// Assert.
	if durableErr != nil || !reflect.DeepEqual(durable, receipt) {
		t.Fatalf("durable outcome = (%#v, %v), want receipt", durable, durableErr)
	}
	if !reflect.DeepEqual(uncertain, store.CommitReceipt{}) || !errors.Is(uncertainErr, context.DeadlineExceeded) {
		t.Fatalf("uncertain outcome = (%#v, %v), want original deadline error", uncertain, uncertainErr)
	}
	if !reflect.DeepEqual(rejected, store.CommitReceipt{}) || !errors.Is(rejectedErr, mutation.ErrPlanDigestMismatch) {
		t.Fatalf("foreign outcome = (%#v, %v), want plan mismatch", rejected, rejectedErr)
	}
	if !reflect.DeepEqual(wrong, store.CommitReceipt{}) || !errors.Is(wrongErr, mutation.ErrPlanDigestMismatch) {
		t.Fatalf("wrong request digest outcome = (%#v, %v), want plan mismatch", wrong, wrongErr)
	}
}
