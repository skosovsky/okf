package bundle

import (
	"context"

	"github.com/skosovsky/okf/internal/markdownowner"
)

// AttestedComputationStateForVersionContext applies the selected document
// rules. For file mode, Bundle.ComputationStateContext also checks the target.
func (d Document) AttestedComputationStateForVersionContext(ctx context.Context, version VersionResolution) (AttestedComputationState, error) {
	if version.Effective != OKFVersion03 {
		return d.AttestedComputationStateContext(ctx)
	}
	matched, err := d.IsAttestedComputationContext(ctx)
	if err != nil || !matched {
		return AttestedComputationState{Mode: ComputationModeAbsent}, err
	}
	contract, err := d.Frontmatter.computationContractStateContext(ctx, true)
	if err != nil {
		return AttestedComputationState{}, err
	}
	state := AttestedComputationState{
		TypeMatches: true, Mode: ComputationModeAbsent,
		ContractPresent: contract.Present, ContractState: contract, Contract: contract.Value,
	}
	if contract.Computation.Present {
		state.Mode = ComputationModeMalformed
		if contract.Computation.Valid {
			state.Mode = ComputationModeFile
			state.Valid = true
			state.UnavailableReason = "file_unresolved"
		} else {
			state.UnavailableReason = "invalid_computation_path"
		}
		return state, ctx.Err()
	}
	fences, err := markdownowner.ComputationFencesV03Context(ctx, d.Body)
	if err != nil {
		return AttestedComputationState{}, err
	}
	for _, fence := range fences {
		state.FenceSpans = append(state.FenceSpans, SourceSpan{Start: fence.Start, End: fence.End})
	}
	if len(fences) == 0 {
		state.UnavailableReason = "missing_inline_computation"
		return state, nil
	}
	state.Multiple = len(fences) > 1
	state.Unclosed = !fences[0].Closed
	state.Mode, state.Valid, state.Inline = ComputationModeInline, true, fences[0].Content
	setAttestable(&state)
	return state, ctx.Err()
}

// ComputationStateContext resolves an Attested Computation against the
// captured bundle. It reports whether the material needed for attestation is
// present. The projection never runs the computation or attester.
func (b *Bundle) ComputationStateContext(ctx context.Context, concept Concept) (AttestedComputationState, error) {
	if b == nil {
		return concept.Document.AttestedComputationStateContext(ctx)
	}
	return b.computationStateForVersionContext(ctx, concept, b.discoveryVersion)
}

func (b *Bundle) computationStateForVersionContext(ctx context.Context, concept Concept, version VersionResolution) (AttestedComputationState, error) {
	state, err := concept.Document.AttestedComputationStateForVersionContext(ctx, version)
	if err != nil || version.Effective != OKFVersion03 || !state.TypeMatches {
		return state, err
	}
	rootFallback := version.Declared == ""
	if state.Mode == ComputationModeFile {
		path, _, err := b.resolveURIReferenceContext(ctx, concept.Path, state.Contract.Computation, false, rootFallback)
		if err != nil {
			return AttestedComputationState{}, err
		}
		if !path.Exists {
			state.Valid = false
			state.UnavailableReason = "missing_computation_file"
			return state, nil
		}
		state.ComputationPath = path.Path
		setAttestable(&state)
	}
	if state.Attestable {
		location, _, err := b.resolveURIReferenceContext(ctx, concept.Path, state.Contract.Attester.Resource, false, rootFallback)
		if err != nil {
			return AttestedComputationState{}, err
		}
		if !location.Exists && location.Kind != PathValueURL {
			state.Attestable = false
			state.UnavailableReason = "missing_attester_file"
		}
	}
	return state, ctx.Err()
}

func setAttestable(state *AttestedComputationState) {
	switch {
	case state.Contract.Runtime == "":
		state.UnavailableReason = "missing_runtime"
	case state.Contract.Attester == nil:
		state.UnavailableReason = "missing_attester"
	default:
		state.Attestable = true
		state.UnavailableReason = ""
	}
}
