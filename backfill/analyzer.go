package backfill

import "context"

// Analyzer is provider-neutral. Adapters may use an LLM, a human review queue,
// or deterministic rules, but can only return evidence-bound proposals.
type Analyzer interface {
	Analyze(context.Context, AnalysisRequest) (Analysis, error)
}

type AnalysisRequest struct {
	Manifest        Manifest
	ProtocolVersion string
}

// RunAnalyzer validates the provider's complete output before any writer sees
// it. No bundle mutation capability is passed to an Analyzer.
func RunAnalyzer(ctx context.Context, analyzer Analyzer, m Manifest) (Analysis, Coverage, error) {
	if err := ctx.Err(); err != nil {
		return Analysis{}, Coverage{}, err
	}
	a, err := analyzer.Analyze(ctx, AnalysisRequest{Manifest: m, ProtocolVersion: SchemaVersion})
	if err != nil {
		return Analysis{}, Coverage{}, err
	}
	report, _, err := ValidateAnalysis(m, a)
	if err != nil {
		return Analysis{}, report, err
	}
	return a, report, nil
}
