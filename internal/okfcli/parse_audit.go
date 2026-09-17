package okfcli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/skosovsky/okf/bundle"
)

type semanticFamilyAuditDTO struct {
	Present       bool   `json:"present"`
	Ambiguous     bool   `json:"ambiguous"`
	Shape         string `json:"shape"`
	ResolvedShape string `json:"resolved_shape,omitempty"`
	ShapeValid    bool   `json:"shape_valid"`
	Raw           string `json:"raw,omitempty"`
	ResolvedRaw   string `json:"resolved_raw,omitempty"`
	ViaAlias      bool   `json:"via_alias,omitempty"`
	ViaMerge      bool   `json:"via_merge,omitempty"`
}

type scalarAuditDTO struct {
	Present bool   `json:"present"`
	Valid   bool   `json:"valid"`
	Raw     string `json:"raw,omitempty"`
	Value   string `json:"value,omitempty"`
}

type temporalAuditDTO struct {
	State string `json:"state"`
	Raw   string `json:"raw,omitempty"`
}

type booleanAuditDTO struct {
	Present bool   `json:"present"`
	Valid   bool   `json:"valid"`
	Raw     string `json:"raw,omitempty"`
	Value   bool   `json:"value"`
}

type uint64AuditDTO struct {
	Present bool   `json:"present"`
	Valid   bool   `json:"valid"`
	Raw     string `json:"raw,omitempty"`
	Value   uint64 `json:"value"`
}

type generationAuditDTO struct {
	Family   semanticFamilyAuditDTO `json:"family"`
	Mapping  bool                   `json:"mapping"`
	Valid    bool                   `json:"valid"`
	HasValue bool                   `json:"has_value"`
	By       scalarAuditDTO         `json:"by"`
	At       temporalAuditDTO       `json:"at"`
}

type verificationAuditDTO struct {
	RawBy string           `json:"raw_by,omitempty"`
	RawAt string           `json:"raw_at,omitempty"`
	Valid bool             `json:"valid"`
	At    temporalAuditDTO `json:"at"`
	Value *verificationDTO `json:"value,omitempty"`
}

type verificationsAuditDTO struct {
	Family  semanticFamilyAuditDTO `json:"family"`
	Entries []verificationAuditDTO `json:"entries"`
}

type usageWindowAuditDTO struct {
	Family   *semanticFamilyAuditDTO `json:"family,omitempty"`
	Present  bool                    `json:"present"`
	Mapping  bool                    `json:"mapping"`
	Valid    bool                    `json:"valid"`
	HasValue bool                    `json:"has_value"`
	From     temporalAuditDTO        `json:"from"`
	To       temporalAuditDTO        `json:"to"`
	Value    *usageWindowDTO         `json:"value,omitempty"`
}

type sourceAuditDTO struct {
	Present      bool                `json:"present"`
	Mapping      bool                `json:"mapping"`
	Valid        bool                `json:"valid"`
	HasValue     bool                `json:"has_value"`
	ID           scalarAuditDTO      `json:"id"`
	Resource     scalarAuditDTO      `json:"resource"`
	Title        scalarAuditDTO      `json:"title"`
	Author       scalarAuditDTO      `json:"author"`
	UsageCount   uint64AuditDTO      `json:"usage_count"`
	LastModified temporalAuditDTO    `json:"last_modified"`
	UsageWindow  usageWindowAuditDTO `json:"usage_window"`
	Value        *sourceDTO          `json:"value,omitempty"`
}

type sourcesAuditDTO struct {
	Family  semanticFamilyAuditDTO `json:"family"`
	Entries []sourceAuditDTO       `json:"entries"`
}

type lifecycleAuditDTO struct {
	StatusFamily     semanticFamilyAuditDTO  `json:"status_family"`
	Status           statusAuditDTO          `json:"status"`
	StaleAfterFamily *semanticFamilyAuditDTO `json:"stale_after_family,omitempty"`
	StaleAfter       temporalAuditDTO        `json:"stale_after"`
}

type statusAuditDTO struct {
	Present   bool   `json:"present"`
	Valid     bool   `json:"valid"`
	Raw       string `json:"raw,omitempty"`
	Effective string `json:"effective,omitempty"`
}

type parameterAuditDTO struct {
	Present  bool            `json:"present"`
	Mapping  bool            `json:"mapping"`
	Valid    bool            `json:"valid"`
	HasValue bool            `json:"has_value"`
	Name     scalarAuditDTO  `json:"name"`
	Type     scalarAuditDTO  `json:"type"`
	Required booleanAuditDTO `json:"required"`
	Value    *parameterDTO   `json:"value,omitempty"`
}

type stringSequenceAuditDTO struct {
	Present      bool                     `json:"present"`
	Valid        bool                     `json:"valid"`
	ItemFamilies []semanticFamilyAuditDTO `json:"item_families"`
	Items        []scalarAuditDTO         `json:"items"`
	Values       []string                 `json:"values"`
}

type executorAuditDTO struct {
	Present        bool                    `json:"present"`
	Mapping        bool                    `json:"mapping"`
	Valid          bool                    `json:"valid"`
	HasValue       bool                    `json:"has_value"`
	ResourceFamily *semanticFamilyAuditDTO `json:"resource_family,omitempty"`
	Resource       scalarAuditDTO          `json:"resource"`
	ReceiptFamily  *semanticFamilyAuditDTO `json:"receipt_family,omitempty"`
	Receipt        stringSequenceAuditDTO  `json:"receipt"`
	Value          *executorDTO            `json:"value,omitempty"`
}

type attesterAuditDTO struct {
	Present  bool           `json:"present"`
	Mapping  bool           `json:"mapping"`
	Valid    bool           `json:"valid"`
	HasValue bool           `json:"has_value"`
	Resource scalarAuditDTO `json:"resource"`
	Value    *attesterDTO   `json:"value,omitempty"`
}

type computationContractAuditDTO struct {
	Present           bool                   `json:"present"`
	Valid             bool                   `json:"valid"`
	Runtime           scalarAuditDTO         `json:"runtime"`
	ParametersFamily  semanticFamilyAuditDTO `json:"parameters_family"`
	ParametersPresent bool                   `json:"parameters_present"`
	ParametersValid   bool                   `json:"parameters_valid"`
	Parameters        []parameterAuditDTO    `json:"parameters"`
	Computation       scalarAuditDTO         `json:"computation"`
	ExecutorFamily    semanticFamilyAuditDTO `json:"executor_family"`
	Executor          executorAuditDTO       `json:"executor"`
	AttesterFamily    semanticFamilyAuditDTO `json:"attester_family"`
	Attester          attesterAuditDTO       `json:"attester"`
	Value             *computationDTO        `json:"value,omitempty"`
}

type sourceSpanDTO struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type computationAuditDTO struct {
	TypeMatches     bool                        `json:"type_matches"`
	ContractPresent bool                        `json:"contract_present"`
	Valid           bool                        `json:"valid"`
	Mode            string                      `json:"mode"`
	Contract        computationContractAuditDTO `json:"contract"`
	Inline          string                      `json:"inline,omitempty"`
	FenceSpans      []sourceSpanDTO             `json:"fence_spans"`
	Unclosed        bool                        `json:"unclosed"`
	Multiple        bool                        `json:"multiple"`
	Conflict        bool                        `json:"conflict"`
}

type attributionAuditDTO struct {
	Entries       int                        `json:"entries"`
	Sources       int                        `json:"sources"`
	References    int                        `json:"references"`
	Definitions   int                        `json:"definitions"`
	SourceFamily  *semanticFamilyAuditDTO    `json:"source_family,omitempty"`
	EntryEvidence []attributionEntryAuditDTO `json:"entry_evidence"`
}

type attributionEntryAuditDTO struct {
	Index        int                    `json:"index"`
	Family       semanticFamilyAuditDTO `json:"family"`
	Ambiguous    bool                   `json:"ambiguous"`
	Valid        bool                   `json:"valid"`
	NormalizedID string                 `json:"normalized_id,omitempty"`
	Source       sourceAuditDTO         `json:"source"`
	Value        *attributionDTO        `json:"value,omitempty"`
}

func projectSemanticFamilyObservation(
	state bundle.SemanticFamilyObservation,
) semanticFamilyAuditDTO {
	return semanticFamilyAuditDTO{
		Present:       state.Present,
		Ambiguous:     state.Ambiguous,
		Shape:         string(state.Shape),
		ResolvedShape: string(state.ResolvedShape),
		ShapeValid:    state.ShapeValid,
		Raw:           state.Raw,
		ResolvedRaw:   state.ResolvedRaw,
		ViaAlias:      state.ViaAlias,
		ViaMerge:      state.ViaMerge,
	}
}

func projectKnownSemanticFamily(
	ctx context.Context,
	frontmatter bundle.Frontmatter,
	field string,
) (semanticFamilyAuditDTO, error) {
	observation, err := frontmatter.SemanticFamilyObservationContext(ctx, field)
	if err == nil {
		return projectSemanticFamilyObservation(observation), nil
	}
	if errors.Is(err, bundle.ErrUnknownSemanticFamily) {
		return semanticFamilyAuditDTO{}, fmt.Errorf(
			"internal semantic family invariant %q: %w",
			field,
			err,
		)
	}
	return semanticFamilyAuditDTO{}, err
}

func projectScalarAudit(state bundle.ScalarValueState) scalarAuditDTO {
	return scalarAuditDTO{
		Present: state.Present,
		Valid:   state.Valid,
		Raw:     state.Raw,
		Value:   state.Value,
	}
}

func projectDateAudit(state bundle.DateValue) temporalAuditDTO {
	return temporalAuditDTO{State: string(state.State), Raw: state.Raw}
}

func projectDateTimeAudit(state bundle.DateTimeValue) temporalAuditDTO {
	return temporalAuditDTO{State: string(state.State), Raw: state.Raw}
}

func projectUsageWindowAudit(state bundle.UsageWindowState) usageWindowAuditDTO {
	out := usageWindowAuditDTO{
		Present:  state.Present,
		Mapping:  state.Mapping,
		Valid:    state.Valid,
		HasValue: state.HasValue,
		From:     projectDateAudit(state.From),
		To:       projectDateAudit(state.To),
	}
	if state.Valid {
		out.Value = &usageWindowDTO{From: state.Value.From, To: state.Value.To}
	}
	return out
}

func projectDocumentUsageWindowAudit(
	ctx context.Context,
	document bundle.Document,
) (usageWindowAuditDTO, error) {
	family, err := projectKnownSemanticFamily(ctx, document.Frontmatter, "usage_window")
	if err != nil {
		return usageWindowAuditDTO{}, err
	}
	out := projectUsageWindowAudit(document.Frontmatter.UsageWindowState())
	out.Family = &family
	return out, nil
}

func projectGenerationAudit(
	ctx context.Context,
	document bundle.Document,
) (generationAuditDTO, error) {
	family, err := projectKnownSemanticFamily(ctx, document.Frontmatter, "generated")
	if err != nil {
		return generationAuditDTO{}, err
	}
	state := document.GenerationState()
	return generationAuditDTO{
		Family:   family,
		Mapping:  state.Mapping,
		Valid:    state.Valid,
		HasValue: state.HasValue,
		By:       projectScalarAudit(state.By),
		At:       projectDateTimeAudit(state.At),
	}, nil
}

func projectVerificationsAudit(
	ctx context.Context,
	document bundle.Document,
) (verificationsAuditDTO, error) {
	family, err := projectKnownSemanticFamily(ctx, document.Frontmatter, "verified")
	if err != nil {
		return verificationsAuditDTO{}, err
	}
	states := document.Frontmatter.VerificationStates()
	out := verificationsAuditDTO{
		Family:  family,
		Entries: make([]verificationAuditDTO, 0, len(states)),
	}
	for _, state := range states {
		entry := verificationAuditDTO{
			RawBy: state.RawBy,
			RawAt: state.RawAt,
			Valid: state.Valid,
			At:    projectDateTimeAudit(state.AtValue),
		}
		if state.Valid {
			entry.Value = &verificationDTO{By: state.Value.By, At: state.Value.At}
		}
		out.Entries = append(out.Entries, entry)
	}
	return out, nil
}

func projectSourcesAudit(
	ctx context.Context,
	document bundle.Document,
) (sourcesAuditDTO, error) {
	family, err := projectKnownSemanticFamily(ctx, document.Frontmatter, "sources")
	if err != nil {
		return sourcesAuditDTO{}, err
	}
	states := document.Frontmatter.SourceStates()
	out := sourcesAuditDTO{
		Family:  family,
		Entries: make([]sourceAuditDTO, 0, len(states)),
	}
	for _, state := range states {
		out.Entries = append(out.Entries, projectSourceAudit(state))
	}
	return out, nil
}

func projectSourceAudit(state bundle.ProvenanceSourceState) sourceAuditDTO {
	out := sourceAuditDTO{
		Present:      state.Present,
		Mapping:      state.Mapping,
		Valid:        state.Valid,
		HasValue:     state.HasValue,
		ID:           projectScalarAudit(state.ID),
		Resource:     projectScalarAudit(state.Resource),
		Title:        projectScalarAudit(state.Title),
		Author:       projectScalarAudit(state.Author),
		UsageCount:   projectUint64Audit(state.UsageCount),
		LastModified: projectDateAudit(state.LastModified),
		UsageWindow:  projectUsageWindowAudit(state.UsageWindow),
	}
	if state.Valid {
		value := projectSources([]bundle.ProvenanceSource{state.Value})
		out.Value = &value[0]
	}
	return out
}

func projectUint64Audit(state bundle.Uint64ValueState) uint64AuditDTO {
	return uint64AuditDTO{
		Present: state.Present,
		Valid:   state.Valid,
		Raw:     state.Raw,
		Value:   state.Value,
	}
}

func projectLifecycleAudit(
	ctx context.Context,
	document bundle.Document,
) (lifecycleAuditDTO, error) {
	statusFamily, err := projectKnownSemanticFamily(ctx, document.Frontmatter, "status")
	if err != nil {
		return lifecycleAuditDTO{}, err
	}
	staleAfter, err := document.Frontmatter.StaleAfterObservationContext(ctx)
	if err != nil {
		return lifecycleAuditDTO{}, err
	}
	status := document.StatusState()
	staleAfterFamily := projectSemanticFamilyObservation(staleAfter.Family)
	return lifecycleAuditDTO{
		StatusFamily: statusFamily,
		Status: statusAuditDTO{
			Present:   status.Present,
			Valid:     status.Valid,
			Raw:       status.Raw,
			Effective: status.Effective,
		},
		StaleAfterFamily: &staleAfterFamily,
		StaleAfter:       projectDateAudit(staleAfter.Value),
	}, nil
}

func projectComputationAudit(
	ctx context.Context,
	document bundle.Document,
) (computationAuditDTO, error) {
	parametersFamily, err := projectKnownSemanticFamily(
		ctx,
		document.Frontmatter,
		"parameters",
	)
	if err != nil {
		return computationAuditDTO{}, err
	}
	executorFamily, err := projectKnownSemanticFamily(ctx, document.Frontmatter, "executor")
	if err != nil {
		return computationAuditDTO{}, err
	}
	attesterFamily, err := projectKnownSemanticFamily(ctx, document.Frontmatter, "attester")
	if err != nil {
		return computationAuditDTO{}, err
	}
	executorObservation, err := document.Frontmatter.ExecutorReceiptObservationContext(ctx)
	if err != nil {
		return computationAuditDTO{}, err
	}
	state := document.AttestedComputationState()
	contractState := document.Frontmatter.ComputationContractState()
	out := computationAuditDTO{
		TypeMatches:     state.TypeMatches,
		ContractPresent: state.ContractPresent,
		Valid:           state.Valid,
		Mode:            string(state.Mode),
		Contract:        projectComputationContractAudit(contractState),
		Inline:          state.Inline,
		FenceSpans:      make([]sourceSpanDTO, 0, len(state.FenceSpans)),
		Unclosed:        state.Unclosed,
		Multiple:        state.Multiple,
		Conflict:        state.Conflict,
	}
	out.Contract.ParametersFamily = parametersFamily
	out.Contract.ExecutorFamily = executorFamily
	out.Contract.AttesterFamily = attesterFamily
	projectExecutorReceiptObservation(&out.Contract, executorObservation)
	for _, span := range state.FenceSpans {
		out.FenceSpans = append(out.FenceSpans, sourceSpanDTO{Start: span.Start, End: span.End})
	}
	return out, nil
}

func projectComputationContractAudit(state bundle.ComputationContractState) computationContractAuditDTO {
	out := computationContractAuditDTO{
		Present:           state.Present,
		Valid:             state.Valid,
		Runtime:           projectScalarAudit(state.Runtime),
		ParametersPresent: state.ParametersPresent,
		ParametersValid:   state.ParametersValid,
		Parameters:        make([]parameterAuditDTO, 0, len(state.Parameters)),
		Computation:       projectScalarAudit(state.Computation),
		Executor:          projectExecutorAudit(state.Executor),
		Attester:          projectAttesterAudit(state.Attester),
	}
	for _, parameter := range state.Parameters {
		entry := parameterAuditDTO{
			Present:  parameter.Present,
			Mapping:  parameter.Mapping,
			Valid:    parameter.Valid,
			HasValue: parameter.HasValue,
			Name:     projectScalarAudit(parameter.Name),
			Type:     projectScalarAudit(parameter.Type),
			Required: booleanAuditDTO{
				Present: parameter.Required.Present,
				Valid:   parameter.Required.Valid,
				Raw:     parameter.Required.Raw,
				Value:   parameter.Required.Value,
			},
		}
		if parameter.Valid {
			entry.Value = &parameterDTO{
				Name:     parameter.Value.Name,
				Type:     parameter.Value.Type,
				Required: parameter.Value.Required,
			}
		}
		out.Parameters = append(out.Parameters, entry)
	}
	if state.Present && state.Valid {
		value := computationDTO{
			Runtime:     state.Value.Runtime,
			Parameters:  projectParameters(state.Value.Parameters),
			Computation: state.Value.Computation,
		}
		if state.Value.Executor != nil {
			value.Executor = &executorDTO{
				Resource: state.Value.Executor.Resource,
				Receipt:  nonNilSlice(state.Value.Executor.Receipt),
			}
		}
		if state.Value.Attester != nil {
			value.Attester = &attesterDTO{Resource: state.Value.Attester.Resource}
		}
		out.Value = &value
	}
	return out
}

func projectExecutorAudit(state bundle.ExecutorContractState) executorAuditDTO {
	out := executorAuditDTO{
		Present:  state.Present,
		Mapping:  state.Mapping,
		Valid:    state.Valid,
		HasValue: state.HasValue,
		Resource: projectScalarAudit(state.Resource),
		Receipt: stringSequenceAuditDTO{
			Present: state.Receipt.Present,
			Valid:   state.Receipt.Valid,
			Items:   make([]scalarAuditDTO, 0, len(state.Receipt.Items)),
			Values:  nonNilSlice(append([]string(nil), state.Receipt.Values...)),
		},
	}
	for _, item := range state.Receipt.Items {
		out.Receipt.Items = append(out.Receipt.Items, projectScalarAudit(item))
	}
	if state.Valid && state.HasValue {
		out.Value = &executorDTO{
			Resource: state.Value.Resource,
			Receipt:  nonNilSlice(append([]string(nil), state.Value.Receipt...)),
		}
	}
	return out
}

func projectExecutorReceiptObservation(
	contract *computationContractAuditDTO,
	observation bundle.ExecutorReceiptObservation,
) {
	executorFamily := projectSemanticFamilyObservation(observation.ExecutorFamily)
	resourceFamily := projectSemanticFamilyObservation(observation.ResourceFamily)
	receiptFamily := projectSemanticFamilyObservation(observation.ReceiptFamily)
	contract.ExecutorFamily = executorFamily
	contract.Executor.Present = observation.ExecutorFamily.Present
	contract.Executor.Mapping = observation.ExecutorFamily.ShapeValid &&
		!observation.ExecutorFamily.Ambiguous
	contract.Executor.Valid = observation.Valid
	contract.Executor.HasValue = observation.HasValue
	if !contract.Executor.Mapping {
		return
	}
	contract.Executor.ResourceFamily = &resourceFamily
	contract.Executor.Resource = projectScalarAudit(observation.Resource)
	contract.Executor.ReceiptFamily = &receiptFamily
	contract.Executor.Receipt.Present = observation.ReceiptFamily.Present
	contract.Executor.Receipt.Valid = !observation.ReceiptFamily.Present ||
		observation.ReceiptFamily.ShapeValid &&
			!observation.ReceiptFamily.Ambiguous &&
			allObservedScalarItemsValid(observation.Items)
	contract.Executor.Receipt.ItemFamilies = make(
		[]semanticFamilyAuditDTO,
		0,
		len(observation.ItemFamilies),
	)
	for _, family := range observation.ItemFamilies {
		contract.Executor.Receipt.ItemFamilies = append(
			contract.Executor.Receipt.ItemFamilies,
			projectSemanticFamilyObservation(family),
		)
	}
	contract.Executor.Receipt.Items = make([]scalarAuditDTO, 0, len(observation.Items))
	for _, item := range observation.Items {
		contract.Executor.Receipt.Items = append(
			contract.Executor.Receipt.Items,
			projectScalarAudit(item),
		)
	}
	contract.Executor.Receipt.Values = nonNilSlice(append([]string(nil), observation.Values...))
	contract.Executor.Value = nil
	if observation.Value != nil {
		contract.Executor.Value = &executorDTO{
			Resource: observation.Value.Resource,
			Receipt:  nonNilSlice(append([]string(nil), observation.Value.Receipt...)),
		}
	}
}

func allObservedScalarItemsValid(items []bundle.ScalarValueState) bool {
	for _, item := range items {
		if !item.Valid {
			return false
		}
	}
	return true
}

func projectAttesterAudit(state bundle.AttesterContractState) attesterAuditDTO {
	out := attesterAuditDTO{
		Present:  state.Present,
		Mapping:  state.Mapping,
		Valid:    state.Valid,
		HasValue: state.HasValue,
		Resource: projectScalarAudit(state.Resource),
	}
	if state.Valid && state.HasValue {
		out.Value = &attesterDTO{Resource: state.Value.Resource}
	}
	return out
}

func projectAttributionAudit(
	ctx context.Context,
	document bundle.Document,
	values []attributionDTO,
) (attributionAuditDTO, error) {
	observation, err := document.AttributionObservationContext(ctx)
	if err != nil {
		return attributionAuditDTO{}, err
	}
	family := projectSemanticFamilyObservation(observation.Family)
	out := attributionAuditDTO{
		Entries:       len(values),
		SourceFamily:  &family,
		EntryEvidence: make([]attributionEntryAuditDTO, 0, len(observation.Entries)),
	}
	for _, value := range values {
		out.Sources += len(value.Sources)
		out.References += len(value.References)
		out.Definitions += len(value.Definitions)
	}
	for _, observed := range observation.Entries {
		entry := attributionEntryAuditDTO{
			Index:        observed.Index,
			Family:       projectSemanticFamilyObservation(observed.Family),
			Ambiguous:    observed.Ambiguous,
			Valid:        observed.Valid,
			NormalizedID: observed.NormalizedID,
			Source:       projectSourceAudit(observed.Source),
		}
		if observed.Value != nil {
			projected := projectAttributions([]bundle.Attribution{*observed.Value})
			entry.Value = &projected[0]
		}
		out.EntryEvidence = append(out.EntryEvidence, entry)
	}
	return out, nil
}

func writeSemanticFamilyAuditText(
	stdout io.Writer,
	label string,
	family semanticFamilyAuditDTO,
	suffix string,
) {
	fmt.Fprintf(stdout,
		"%s: present=%t ambiguous=%t shape=%s shape_valid=%t raw=%s%s\n",
		label,
		family.Present,
		family.Ambiguous,
		renderTextString(family.Shape),
		family.ShapeValid,
		renderTextString(family.Raw),
		suffix)
}

func writeObservedSemanticFamilyAuditText(
	stdout io.Writer,
	label string,
	family semanticFamilyAuditDTO,
	suffix string,
) {
	fmt.Fprintf(stdout,
		"%s: present=%t ambiguous=%t shape=%s resolved_shape=%s shape_valid=%t raw=%s resolved_raw=%s via_alias=%t via_merge=%t%s\n",
		label,
		family.Present,
		family.Ambiguous,
		renderTextString(family.Shape),
		renderTextString(family.ResolvedShape),
		family.ShapeValid,
		renderTextString(family.Raw),
		renderTextString(family.ResolvedRaw),
		family.ViaAlias,
		family.ViaMerge,
		suffix)
}

func writeScalarAuditText(stdout io.Writer, indent, label string, state scalarAuditDTO) {
	fmt.Fprintf(stdout, "%s%s: present=%t valid=%t raw=%s value=%s\n",
		indent,
		label,
		state.Present,
		state.Valid,
		renderTextString(state.Raw),
		renderTextString(state.Value))
}

func writeTemporalAuditText(stdout io.Writer, indent, label string, state temporalAuditDTO) {
	fmt.Fprintf(stdout, "%s%s: state=%s raw=%s\n",
		indent,
		label,
		renderTextString(state.State),
		renderTextString(state.Raw))
}

func writeGenerationAuditText(stdout io.Writer, audit generationAuditDTO) {
	writeSemanticFamilyAuditText(
		stdout,
		"generated audit",
		audit.Family,
		fmt.Sprintf(" mapping=%t valid=%t has_value=%t", audit.Mapping, audit.Valid, audit.HasValue),
	)
	writeScalarAuditText(stdout, "  ", "by", audit.By)
	writeTemporalAuditText(stdout, "  ", "at", audit.At)
}

func writeVerificationsAuditText(stdout io.Writer, audit verificationsAuditDTO) {
	writeSemanticFamilyAuditText(
		stdout,
		"verified audit",
		audit.Family,
		fmt.Sprintf(" entries=%d", len(audit.Entries)),
	)
	for index, entry := range audit.Entries {
		fmt.Fprintf(stdout,
			"  [%d] valid=%t raw_by=%s raw_at=%s at_state=%s typed_by=%s typed_at=%s\n",
			index,
			entry.Valid,
			renderTextString(entry.RawBy),
			renderTextString(entry.RawAt),
			renderTextString(entry.At.State),
			renderTextString(valueOrZero(entry.Value, func(value verificationDTO) string { return value.By })),
			renderTextString(valueOrZero(entry.Value, func(value verificationDTO) string { return value.At })))
	}
}

func valueOrZero[T any](value *T, project func(T) string) string {
	if value == nil {
		return ""
	}
	return project(*value)
}

func writeUsageWindowAuditText(
	stdout io.Writer,
	label string,
	indent string,
	audit usageWindowAuditDTO,
) {
	if audit.Family != nil {
		writeSemanticFamilyAuditText(stdout, indent+label+" family", *audit.Family, "")
	}
	fmt.Fprintf(stdout, "%s%s: present=%t mapping=%t valid=%t has_value=%t\n",
		indent, label, audit.Present, audit.Mapping, audit.Valid, audit.HasValue)
	writeTemporalAuditText(stdout, indent+"  ", "from", audit.From)
	writeTemporalAuditText(stdout, indent+"  ", "to", audit.To)
}

func writeSourcesAuditText(stdout io.Writer, audit sourcesAuditDTO) {
	writeSemanticFamilyAuditText(
		stdout,
		"sources audit",
		audit.Family,
		fmt.Sprintf(" entries=%d", len(audit.Entries)),
	)
	for index, entry := range audit.Entries {
		fmt.Fprintf(stdout,
			"  [%d] present=%t mapping=%t valid=%t has_value=%t\n",
			index, entry.Present, entry.Mapping, entry.Valid, entry.HasValue)
		writeScalarAuditText(stdout, "    ", "id", entry.ID)
		writeScalarAuditText(stdout, "    ", "resource", entry.Resource)
		writeScalarAuditText(stdout, "    ", "title", entry.Title)
		writeScalarAuditText(stdout, "    ", "author", entry.Author)
		fmt.Fprintf(stdout,
			"    usage_count: present=%t valid=%t raw=%s value=%d\n",
			entry.UsageCount.Present,
			entry.UsageCount.Valid,
			renderTextString(entry.UsageCount.Raw),
			entry.UsageCount.Value)
		writeTemporalAuditText(stdout, "    ", "last_modified", entry.LastModified)
		writeUsageWindowAuditText(stdout, "usage_window", "    ", entry.UsageWindow)
	}
}

func writeLifecycleAuditText(stdout io.Writer, audit lifecycleAuditDTO) {
	fmt.Fprintf(stdout,
		"lifecycle audit: status_present=%t status_valid=%t status_raw=%s status_effective=%s status_shape=%s status_shape_valid=%t status_ambiguous=%t status_family_raw=%s\n",
		audit.Status.Present,
		audit.Status.Valid,
		renderTextString(audit.Status.Raw),
		renderTextString(audit.Status.Effective),
		renderTextString(audit.StatusFamily.Shape),
		audit.StatusFamily.ShapeValid,
		audit.StatusFamily.Ambiguous,
		renderTextString(audit.StatusFamily.Raw))
	if audit.StaleAfterFamily != nil {
		writeObservedSemanticFamilyAuditText(
			stdout,
			"  stale_after family",
			*audit.StaleAfterFamily,
			"",
		)
	}
	writeTemporalAuditText(stdout, "  ", "stale_after", audit.StaleAfter)
}

func writeAttributionAuditText(stdout io.Writer, audit attributionAuditDTO) {
	fmt.Fprintf(stdout, "attributions audit: entries=%d sources=%d references=%d definitions=%d\n",
		audit.Entries,
		audit.Sources,
		audit.References,
		audit.Definitions)
	if audit.SourceFamily == nil {
		return
	}
	writeObservedSemanticFamilyAuditText(
		stdout,
		"  sources family",
		*audit.SourceFamily,
		fmt.Sprintf(" entry_evidence=%d", len(audit.EntryEvidence)),
	)
	for _, entry := range audit.EntryEvidence {
		fmt.Fprintf(stdout,
			"  entry[%d]: index=%d ambiguous=%t valid=%t normalized_id=%s\n",
			entry.Index,
			entry.Index,
			entry.Ambiguous,
			entry.Valid,
			renderTextString(entry.NormalizedID))
		writeObservedSemanticFamilyAuditText(stdout, "    family", entry.Family, "")
		writeScalarAuditText(stdout, "    ", "id", entry.Source.ID)
		writeScalarAuditText(stdout, "    ", "resource", entry.Source.Resource)
	}
}

func writeComputationAuditText(stdout io.Writer, audit computationAuditDTO) {
	fmt.Fprintf(stdout,
		"attested computation audit: type_matches=%t contract_present=%t valid=%t mode=%s unclosed=%t multiple=%t conflict=%t inline=%s fence_spans=%d\n",
		audit.TypeMatches,
		audit.ContractPresent,
		audit.Valid,
		renderTextString(audit.Mode),
		audit.Unclosed,
		audit.Multiple,
		audit.Conflict,
		renderTextString(audit.Inline),
		len(audit.FenceSpans))
	for index, span := range audit.FenceSpans {
		fmt.Fprintf(stdout, "  fence_span[%d]: start=%d end=%d\n", index, span.Start, span.End)
	}
	contract := audit.Contract
	fmt.Fprintf(stdout,
		"  contract: present=%t valid=%t parameters_present=%t parameters_valid=%t parameters=%d\n",
		contract.Present,
		contract.Valid,
		contract.ParametersPresent,
		contract.ParametersValid,
		len(contract.Parameters))
	writeScalarAuditText(stdout, "    ", "runtime", contract.Runtime)
	writeScalarAuditText(stdout, "    ", "computation", contract.Computation)
	writeSemanticFamilyAuditText(stdout, "    parameters family", contract.ParametersFamily, "")
	for index, parameter := range contract.Parameters {
		fmt.Fprintf(stdout,
			"    parameter[%d]: present=%t mapping=%t valid=%t has_value=%t\n",
			index,
			parameter.Present,
			parameter.Mapping,
			parameter.Valid,
			parameter.HasValue)
		writeScalarAuditText(stdout, "      ", "name", parameter.Name)
		writeScalarAuditText(stdout, "      ", "type", parameter.Type)
		fmt.Fprintf(stdout,
			"      required: present=%t valid=%t raw=%s value=%t\n",
			parameter.Required.Present,
			parameter.Required.Valid,
			renderTextString(parameter.Required.Raw),
			parameter.Required.Value)
	}
	fmt.Fprintf(stdout,
		"    executor: present=%t mapping=%t valid=%t has_value=%t receipt_present=%t receipt_valid=%t receipt_items=%d\n",
		contract.Executor.Present,
		contract.Executor.Mapping,
		contract.Executor.Valid,
		contract.Executor.HasValue,
		contract.Executor.Receipt.Present,
		contract.Executor.Receipt.Valid,
		len(contract.Executor.Receipt.Items))
	writeScalarAuditText(stdout, "      ", "resource", contract.Executor.Resource)
	writeSemanticFamilyAuditText(stdout, "    executor family", contract.ExecutorFamily, "")
	if contract.Executor.ResourceFamily != nil {
		writeObservedSemanticFamilyAuditText(
			stdout,
			"      resource family",
			*contract.Executor.ResourceFamily,
			"",
		)
	}
	if contract.Executor.ReceiptFamily != nil {
		writeObservedSemanticFamilyAuditText(
			stdout,
			"      receipt family",
			*contract.Executor.ReceiptFamily,
			"",
		)
	}
	for index, item := range contract.Executor.Receipt.Items {
		if index < len(contract.Executor.Receipt.ItemFamilies) {
			writeObservedSemanticFamilyAuditText(
				stdout,
				fmt.Sprintf("      receipt[%d] family", index),
				contract.Executor.Receipt.ItemFamilies[index],
				"",
			)
		}
		writeScalarAuditText(stdout, "      ", fmt.Sprintf("receipt[%d]", index), item)
	}
	fmt.Fprintf(stdout,
		"    attester: present=%t mapping=%t valid=%t has_value=%t\n",
		contract.Attester.Present,
		contract.Attester.Mapping,
		contract.Attester.Valid,
		contract.Attester.HasValue)
	writeScalarAuditText(stdout, "      ", "resource", contract.Attester.Resource)
	writeSemanticFamilyAuditText(stdout, "    attester family", contract.AttesterFamily, "")
}
