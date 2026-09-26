package agent

import (
	"strings"
	"testing"
)

func upkeepTestStudy(t *testing.T) (UpkeepStudy, UpkeepStudyRows) {
	t.Helper()
	base := []Artifact{{ID: "index", Path: "index.md", Content: "index"}, {ID: "decision", Path: "decision.md", Content: "mode A"}}
	codeBefore := []Artifact{{ID: "code", Path: "decision.go", Content: "A"}}
	codeAfter := []Artifact{{ID: "code", Path: "decision.go", Content: "B"}}
	plan := UpkeepStudy{ID: "study", SourceCorpusSHA256: strings.Repeat("f", 64), Repeats: 1, WriterModel: "writer-v1", ConsumerModel: "consumer-v1", WriterPrompt: strings.Repeat("a", 64), ConsumerPrompt: strings.Repeat("b", 64), CheckerSHA256: strings.Repeat("c", 64), Cases: []UpkeepCase{{ID: "reversal", WriterTask: "Change A to B", InitialCode: codeBefore, TargetCode: codeAfter, InitialBundle: base, ConsumerQuestion: "Current mode?", Expected: Expected{Answer: "B", Evidence: []string{"decision"}, StaleAnswers: []string{"A"}}}}}
	start, err := upkeepStartingHash(plan.Cases[0])
	if err != nil {
		t.Fatal(err)
	}
	updated := []Artifact{{ID: "index", Path: "index.md", Content: "index"}, {ID: "decision", Path: "decision.md", Content: "mode B"}}
	baseHash, err := UpkeepArtifactsSHA256(base)
	if err != nil {
		t.Fatal(err)
	}
	updatedHash, err := UpkeepArtifactsSHA256(updated)
	if err != nil {
		t.Fatal(err)
	}
	rows := UpkeepStudyRows{
		Writers: []UpkeepWriterRow{
			{CaseID: "reversal", Arm: UpkeepCheckerOff, Repeat: 1, SessionID: "writer-off", StartingSHA256: start, FinalCode: codeAfter, FinalBundle: base},
			{CaseID: "reversal", Arm: UpkeepCheckerOn, Repeat: 1, SessionID: "writer-on", StartingSHA256: start, FinalCode: codeAfter, FinalBundle: updated, CheckerInitial: `{"status":"needs_review","fingerprint":"` + strings.Repeat("d", 64) + `","changes":[{"path":"decision.go","status":" M"}],"advisory":true}`, CheckerResult: `{"status":"reviewed_updated","fingerprint":"` + strings.Repeat("e", 64) + `","changes":[{"path":"decision.go","status":" M"}],"advisory":true}`},
		},
		Consumers: []UpkeepConsumerRow{
			{CaseID: "reversal", Arm: UpkeepCheckerOff, Repeat: 1, ConsumerSessionID: "consumer-off", VisibleSHA256: baseHash, Observation: Observation{Answer: "A", Evidence: []string{"decision"}}},
			{CaseID: "reversal", Arm: UpkeepCheckerOn, Repeat: 1, ConsumerSessionID: "consumer-on", VisibleSHA256: updatedHash, Observation: Observation{Answer: "B", Evidence: []string{"decision"}}},
		},
	}
	return plan, rows
}

func TestAnalyzeUpkeepStudyPairedConsumer(t *testing.T) {
	// Arrange
	plan, rows := upkeepTestStudy(t)
	// Act
	report, err := AnalyzeUpkeepStudy(plan, rows)
	// Assert
	if err != nil || !report.Valid {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if report.Arms[UpkeepCheckerOff].DocUpdateRate != 0 || report.Arms[UpkeepCheckerOff].Stale != 1 {
		t.Fatalf("off=%+v", report.Arms[UpkeepCheckerOff])
	}
	if report.Arms[UpkeepCheckerOn].ConceptUpdateRate != 1 || report.Arms[UpkeepCheckerOn].AnswerQualityRate != 1 {
		t.Fatalf("on=%+v", report.Arms[UpkeepCheckerOn])
	}
}

func TestAnalyzeUpkeepStudyRejectsMismatchedConsumerBundle(t *testing.T) {
	// Arrange
	plan, rows := upkeepTestStudy(t)
	rows.Consumers[1].VisibleSHA256 = rows.Consumers[0].VisibleSHA256
	// Act
	_, err := AnalyzeUpkeepStudy(plan, rows)
	// Assert
	if err == nil || !strings.Contains(err.Error(), "did not receive writer bundle") {
		t.Fatalf("error=%v", err)
	}
}

func TestAnalyzeUpkeepStudyUnsupportedCitationIsUngradable(t *testing.T) {
	// Arrange: the answer matches gold, but the cited ID was never in the writer's bundle.
	plan, rows := upkeepTestStudy(t)
	rows.Consumers[1].Observation.Evidence = []string{"unrelated"}
	// Act.
	report, err := AnalyzeUpkeepStudy(plan, rows)
	// Assert: one model-format error does not prevent analysis of the paired rows.
	if err != nil || !report.Valid {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	on := report.Arms[UpkeepCheckerOn]
	if on.Correct != 0 || on.Ungradable != 1 || on.UnsupportedCitations != 1 || on.AnswerQualityRate != 0 {
		t.Fatalf("on=%+v", on)
	}
	if report.Arms[UpkeepCheckerOff].Stale != 1 || len(report.CitationIssues) != 1 || !strings.Contains(report.CitationIssues[0], `citation "unrelated" absent from writer bundle`) {
		t.Fatalf("report=%+v", report)
	}
}

func TestAnalyzeUpkeepStudyRefusalPrecedesUnsupportedCitation(t *testing.T) {
	// Arrange: a refusal can include an extraneous evidence ID.
	plan, rows := upkeepTestStudy(t)
	rows.Consumers[1].Observation = Observation{Refused: true, Evidence: []string{"unrelated"}}
	// Act.
	report, err := AnalyzeUpkeepStudy(plan, rows)
	// Assert: preserve Grade's refusal category while recording the citation issue.
	if err != nil || !report.Valid {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	on := report.Arms[UpkeepCheckerOn]
	if on.Refusal != 1 || on.Ungradable != 0 || on.UnsupportedCitations != 1 || len(report.CitationIssues) != 1 {
		t.Fatalf("report=%+v", report)
	}
}

func TestAnalyzeUpkeepStudyFailurePrecedesUnsupportedCitation(t *testing.T) {
	// Arrange: a partial answer with a foreign citation accompanies adapter failure.
	plan, rows := upkeepTestStudy(t)
	rows.Consumers[1].Observation.Evidence = []string{"unrelated"}
	rows.Consumers[1].Observation.AdapterError = "transport interrupted"
	// Act.
	report, err := AnalyzeUpkeepStudy(plan, rows)
	// Assert: operational failure stays primary and is not counted as a graded citation.
	if err != nil || report.Valid {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	on := report.Arms[UpkeepCheckerOn]
	if on.Failures != 1 || on.Ungradable != 0 || on.UnsupportedCitations != 0 || len(report.CitationIssues) != 0 {
		t.Fatalf("report=%+v", report)
	}
}

func TestAnalyzeUpkeepStudyRejectsSharedSession(t *testing.T) {
	// Arrange
	plan, rows := upkeepTestStudy(t)
	rows.Consumers[1].ConsumerSessionID = rows.Writers[1].SessionID
	// Act
	_, err := AnalyzeUpkeepStudy(plan, rows)
	// Assert
	if err == nil || !strings.Contains(err.Error(), "non-independent") {
		t.Fatalf("error=%v", err)
	}
}

func TestAnalyzeUpkeepStudyMissingConsumerStaysInDenominator(t *testing.T) {
	// Arrange
	plan, rows := upkeepTestStudy(t)
	rows.Consumers = rows.Consumers[:1]
	// Act
	report, err := AnalyzeUpkeepStudy(plan, rows)
	// Assert
	if err != nil || report.Valid || report.Arms[UpkeepCheckerOn].Expected != 1 || report.Arms[UpkeepCheckerOn].AnswerQualityRate != 0 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestAnalyzeUpkeepStudyFailedWriterStaysInDenominator(t *testing.T) {
	// Arrange
	plan, rows := upkeepTestStudy(t)
	rows.Writers[1].Failure = "model timeout"
	rows.Writers[1].FinalCode = nil
	rows.Writers[1].FinalBundle = nil
	rows.Writers[1].CheckerInitial = ""
	rows.Writers[1].CheckerResult = ""
	rows.Consumers = rows.Consumers[:1]
	// Act
	report, err := AnalyzeUpkeepStudy(plan, rows)
	// Assert
	if err != nil || report.Valid || report.Arms[UpkeepCheckerOn].Failures != 1 || report.Arms[UpkeepCheckerOn].Expected != 1 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestAnalyzeUpkeepStudyFailedWriterWithoutSessionStaysInDenominator(t *testing.T) {
	// Arrange: the adapter failed before it returned a real session ID.
	plan, rows := upkeepTestStudy(t)
	rows.Writers[1].SessionID = ""
	rows.Writers[1].Failure = "adapter timeout"
	rows.Writers[1].FinalCode = nil
	rows.Writers[1].FinalBundle = nil
	rows.Writers[1].CheckerInitial = ""
	rows.Writers[1].CheckerResult = ""
	rows.Consumers = rows.Consumers[:1]
	// Act.
	report, err := AnalyzeUpkeepStudy(plan, rows)
	// Assert.
	if err != nil || report.Valid || report.Arms[UpkeepCheckerOn].WriterObserved != 1 || report.Arms[UpkeepCheckerOn].Failures != 1 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestAnalyzeUpkeepStudyFailedConsumerWithoutSessionStaysInDenominator(t *testing.T) {
	// Arrange: preserve the exact visible bundle hash but no invented session.
	plan, rows := upkeepTestStudy(t)
	rows.Consumers[1].ConsumerSessionID = ""
	rows.Consumers[1].Failure = "adapter timeout"
	rows.Consumers[1].Observation = Observation{}
	// Act.
	report, err := AnalyzeUpkeepStudy(plan, rows)
	// Assert.
	if err != nil || report.Valid || report.Arms[UpkeepCheckerOn].ConsumerObserved != 1 || report.Arms[UpkeepCheckerOn].Failures != 1 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestAnalyzeUpkeepStudyRejectsMissingSessionOnSuccessfulRow(t *testing.T) {
	// Arrange.
	plan, rows := upkeepTestStudy(t)
	rows.Writers[1].SessionID = ""
	// Act.
	_, err := AnalyzeUpkeepStudy(plan, rows)
	// Assert.
	if err == nil {
		t.Fatal("successful writer without a session was accepted")
	}
	plan, rows = upkeepTestStudy(t)
	rows.Consumers[1].ConsumerSessionID = ""
	_, err = AnalyzeUpkeepStudy(plan, rows)
	if err == nil {
		t.Fatal("successful consumer without a session was accepted")
	}
}

func TestAnalyzeUpkeepStudyRejectsUnrelatedCheckerResult(t *testing.T) {
	// Arrange
	plan, rows := upkeepTestStudy(t)
	rows.Writers[1].CheckerInitial = `{"status":"needs_review","fingerprint":"` + strings.Repeat("d", 64) + `","changes":[{"path":"other.go","status":" M"}],"advisory":true}`
	// Act
	_, err := AnalyzeUpkeepStudy(plan, rows)
	// Assert
	if err == nil || !strings.Contains(err.Error(), "unrelated") {
		t.Fatalf("error=%v", err)
	}
}

func TestAnalyzeUpkeepStudyAdapterErrorIsFailure(t *testing.T) {
	// Arrange
	plan, rows := upkeepTestStudy(t)
	rows.Consumers[1].Observation.AdapterError = "model transport failed after partial answer"
	// Act
	report, err := AnalyzeUpkeepStudy(plan, rows)
	// Assert
	if err != nil || report.Valid || report.Arms[UpkeepCheckerOn].Correct != 0 || report.Arms[UpkeepCheckerOn].Failures != 1 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestUpkeepDocChangesDoesNotCountDeletionAsUpdate(t *testing.T) {
	// Arrange
	before := []Artifact{{ID: "index", Path: "index.md", Content: "index"}, {ID: "decision", Path: "decision.md", Content: "mode A"}}
	after := before[:1]
	// Act
	delta := upkeepDocChanges(before, after)
	// Assert
	if delta.updated || delta.conceptUpdated || !delta.deleted || !delta.conceptDeleted {
		t.Fatalf("delta=%+v", delta)
	}
}
