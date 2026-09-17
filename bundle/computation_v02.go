package bundle

import (
	"context"

	"github.com/skosovsky/okf/internal/markdownowner"
)

// AttestedComputationType is the exact standard v0.2 concept type.
const AttestedComputationType = "Attested Computation"

// ComputationMode describes how an exact Attested Computation concept provides
// its computation payload.
type ComputationMode string

const (
	ComputationModeAbsent    ComputationMode = "absent"
	ComputationModeInline    ComputationMode = "inline"
	ComputationModeFile      ComputationMode = "file"
	ComputationModeAmbiguous ComputationMode = "ambiguous"
	ComputationModeMalformed ComputationMode = "malformed"
)

// AttestedComputationState is a type-gated contract and payload projection.
// Valid covers payload ownership only; strict contract-field validation remains
// a validator responsibility.
type AttestedComputationState struct {
	TypeMatches     bool
	ContractPresent bool
	Mode            ComputationMode
	Valid           bool
	Contract        AttestedComputationContract
	ContractState   ComputationContractState
	Inline          string
	FenceSpans      []SourceSpan
	Unclosed        bool
	Multiple        bool
	Conflict        bool
}

// SourceSpan is a half-open byte range in a document body.
type SourceSpan struct {
	Start int
	End   int
}

// IsAttestedComputation reports an exact, non-coerced type match.
func (f Frontmatter) IsAttestedComputation() bool {
	matched, _ := f.IsAttestedComputationContext(context.Background())
	return matched
}

// IsAttestedComputationContext reports the exact type match with cancellable
// scalar comparison.
func (f Frontmatter) IsAttestedComputationContext(ctx context.Context) (bool, error) {
	typ, ok, err := f.TypeContext(ctx)
	if err != nil || !ok {
		return false, err
	}
	matched, err := equalStringContext(ctx, typ, AttestedComputationType)
	return matched, err
}

// IsAttestedComputation reports an exact, non-coerced type match.
func (d Document) IsAttestedComputation() bool {
	return d.Frontmatter.IsAttestedComputation()
}

// IsAttestedComputationContext is the cancellation-aware document wrapper.
func (d Document) IsAttestedComputationContext(ctx context.Context) (bool, error) {
	return d.Frontmatter.IsAttestedComputationContext(ctx)
}

// AttestedComputationState returns the exact-type contract and parser-owned
// inline/file payload mode without executing or interpreting the runtime.
func (d Document) AttestedComputationState() AttestedComputationState {
	out, _ := d.AttestedComputationStateContext(context.Background())
	return out
}

// AttestedComputationStateContext returns the cancellation-aware computation
// contract and parser-owned payload projection.
func (d Document) AttestedComputationStateContext(ctx context.Context) (AttestedComputationState, error) {
	if err := ctx.Err(); err != nil {
		return AttestedComputationState{}, err
	}
	typeMatches, err := d.IsAttestedComputationContext(ctx)
	if err != nil {
		return AttestedComputationState{}, err
	}
	state := AttestedComputationState{TypeMatches: typeMatches, Mode: ComputationModeAbsent}
	if !state.TypeMatches {
		return state, nil
	}
	contractState, err := d.Frontmatter.ComputationContractStateContext(ctx)
	if err != nil {
		return AttestedComputationState{}, err
	}
	state.ContractState = contractState
	state.Contract, state.ContractPresent = contractState.Value, contractState.Present

	computationPresent := contractState.Computation.Present
	fileValid := contractState.Computation.Valid
	inspection, err := markdownowner.InspectComputationContext(ctx, d.Body)
	if err != nil {
		return AttestedComputationState{}, err
	}
	fences := append([]markdownowner.Fence(nil), inspection.DirectFences...)
	fences = append(fences, inspection.ConflictingFences...)
	for _, fence := range fences {
		state.FenceSpans = append(state.FenceSpans, SourceSpan{Start: fence.Start, End: fence.End})
		if !fence.Closed {
			state.Unclosed = true
		}
	}
	state.Multiple = len(fences) > 1 || len(inspection.Sections) > 1
	inlineValid := inspection.State == markdownowner.ComputationInline
	state.Conflict = fileValid && inlineValid
	switch {
	case state.Multiple:
		state.Mode = ComputationModeAmbiguous
	case state.Unclosed || len(inspection.ConflictingFences) > 0 ||
		inspection.State == markdownowner.ComputationMalformed:
		state.Mode = ComputationModeMalformed
	case state.Conflict:
		state.Mode = ComputationModeAmbiguous
	case fileValid:
		state.Mode, state.Valid = ComputationModeFile, true
	case inlineValid:
		state.Mode, state.Valid, state.Inline = ComputationModeInline, true, inspection.DirectFences[0].Content
	case computationPresent:
		state.Mode = ComputationModeMalformed
	default:
		state.Mode = ComputationModeAbsent
	}
	if err := ctx.Err(); err != nil {
		return AttestedComputationState{}, err
	}
	return state, nil
}

// ComputationMode returns the type-gated computation payload mode.
func (d Document) ComputationMode() ComputationMode {
	return d.AttestedComputationState().Mode
}
