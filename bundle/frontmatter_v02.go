package bundle

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/skosovsky/okf/internal/markdownowner"
	"gopkg.in/yaml.v3"
)

// Generation records who produced the current content and when it changed.
type Generation struct {
	By string `yaml:"by,omitempty"`
	At string `yaml:"at,omitempty"`
}

// GenerationState preserves generated mapping shape and validates actor/time
// independently. Value is populated only when the complete generated contract
// is valid; By and At remain available to typed projections independently.
type GenerationState struct {
	Present  bool
	Mapping  bool
	Valid    bool
	HasValue bool
	By       ScalarValueState
	At       DateTimeValue
	Value    Generation
}

// Verification records one independent confirmation event.
type Verification struct {
	By string `yaml:"by,omitempty"`
	At string `yaml:"at,omitempty"`
}

// VerificationState preserves raw scalar observations while exposing a typed
// event only when actor and RFC3339 datetime are both valid.
type VerificationState struct {
	RawBy   string
	RawAt   string
	Valid   bool
	Value   Verification
	AtValue DateTimeValue
}

// UsageWindow frames source usage_count observations.
type UsageWindow struct {
	From string `yaml:"from,omitempty"`
	To   string `yaml:"to,omitempty"`
}

// ProvenanceSource is one source from which a concept derives.
type ProvenanceSource struct {
	ID           string       `yaml:"id,omitempty"`
	Resource     string       `yaml:"resource,omitempty"`
	Title        string       `yaml:"title,omitempty"`
	Author       string       `yaml:"author,omitempty"`
	UsageCount   *uint64      `yaml:"usage_count,omitempty"`
	LastModified string       `yaml:"last_modified,omitempty"`
	UsageWindow  *UsageWindow `yaml:"usage_window,omitempty"`
}

// Uint64ValueState preserves an optional YAML integer observation.
type Uint64ValueState struct {
	Present bool
	Valid   bool
	Raw     string
	Value   uint64
}

// UsageWindowState preserves mapping shape and independently validates dates.
type UsageWindowState struct {
	Present  bool
	Mapping  bool
	Valid    bool
	HasValue bool
	From     DateValue
	To       DateValue
	Value    UsageWindow
}

// ProvenanceSourceState preserves every sources[] entry while projecting only
// correctly tagged fields. HasValue prevents malformed/empty mappings from
// becoming a fabricated empty ProvenanceSource.
type ProvenanceSourceState struct {
	Present      bool
	Mapping      bool
	Valid        bool
	HasValue     bool
	ID           ScalarValueState
	Resource     ScalarValueState
	Title        ScalarValueState
	Author       ScalarValueState
	UsageCount   Uint64ValueState
	LastModified DateValue
	UsageWindow  UsageWindowState
	Value        ProvenanceSource
}

// TemporalValueState distinguishes absent values from valid and malformed
// present YAML.
type TemporalValueState string

const (
	TemporalAbsent    TemporalValueState = "absent"
	TemporalValid     TemporalValueState = "valid"
	TemporalMalformed TemporalValueState = "malformed"
)

// DateValue is a validated YYYY-MM-DD value.
type DateValue struct {
	Raw   string
	Time  time.Time
	State TemporalValueState
}

// DateTimeValue is a validated RFC3339 datetime value.
type DateTimeValue struct {
	Raw   string
	Time  time.Time
	State TemporalValueState
}

// LegacyFallbackObservation records semantic presence of standard v0.2
// families that suppress legacy timestamp and Citations fallbacks.
type LegacyFallbackObservation struct {
	GeneratedPresent bool
	SourcesPresent   bool
	TimestampPresent bool
	CitationsPresent bool
	TimestampAllowed bool
	CitationsAllowed bool
	TimestampActive  bool
	CitationsActive  bool
}

// TrustTier is the advisory tier derived from verified actors.
type TrustTier string

const (
	TrustUnverified       TrustTier = "unverified"
	TrustMachineConfirmed TrustTier = "machine-confirmed"
	TrustHumanReviewed    TrustTier = "human-reviewed"
)

// Lifecycle status values are open strings; these constants name the standard
// v0.2 values without introducing a closed enum.
const (
	StatusDraft      = "draft"
	StatusStable     = "stable"
	StatusDeprecated = "deprecated"
)

// StatusState distinguishes an absent lifecycle status (whose effective value
// is stable) from malformed present YAML, which has no effective status.
type StatusState struct {
	Present   bool
	Valid     bool
	Raw       string
	Effective string
}

// ComputationParameter declares one runtime-defined typed hole.
type ComputationParameter struct {
	Name     string `yaml:"name,omitempty"`
	Type     string `yaml:"type,omitempty"`
	Required bool   `yaml:"required,omitempty"`
}

// BooleanValueState distinguishes absent/malformed booleans from a valid
// false observation.
type BooleanValueState struct {
	Present bool
	Valid   bool
	Raw     string
	Value   bool
}

// ComputationParameterState preserves the strict shape of one parameter.
type ComputationParameterState struct {
	Present  bool
	Mapping  bool
	Valid    bool
	HasValue bool
	Name     ScalarValueState
	Type     ScalarValueState
	Required BooleanValueState
	Value    ComputationParameter
}

// StringSequenceState preserves sequence shape and per-item string validity.
type StringSequenceState struct {
	Present bool
	Valid   bool
	Items   []ScalarValueState
	Values  []string
}

// ExecutorContract describes the referenced runner and required receipt field
// names. It does not define or execute a runtime ABI.
type ExecutorContract struct {
	Resource string   `yaml:"resource,omitempty"`
	Receipt  []string `yaml:"receipt,omitempty"`
}

// AttesterContract describes referenced deterministic attester code.
type AttesterContract struct {
	Resource string `yaml:"resource,omitempty"`
}

// AttestedComputationContract is the v0.2 frontmatter read model. Runtime,
// parameter types, and receipt names deliberately remain open strings.
type AttestedComputationContract struct {
	Runtime     string                 `yaml:"runtime,omitempty"`
	Parameters  []ComputationParameter `yaml:"parameters,omitempty"`
	Computation string                 `yaml:"computation,omitempty"`
	Executor    *ExecutorContract      `yaml:"executor,omitempty"`
	Attester    *AttesterContract      `yaml:"attester,omitempty"`
}

// ExecutorContractState preserves strict resource/receipt observations.
type ExecutorContractState struct {
	Present  bool
	Mapping  bool
	Valid    bool
	HasValue bool
	Resource ScalarValueState
	Receipt  StringSequenceState
	Value    ExecutorContract
}

// AttesterContractState preserves a strict attester resource observation.
type AttesterContractState struct {
	Present  bool
	Mapping  bool
	Valid    bool
	HasValue bool
	Resource ScalarValueState
	Value    AttesterContract
}

// ComputationContractState is the strict field-level observation surface for
// an optional Attested Computation contract.
type ComputationContractState struct {
	Present           bool
	Valid             bool
	Runtime           ScalarValueState
	ParametersPresent bool
	ParametersValid   bool
	Parameters        []ComputationParameterState
	Computation       ScalarValueState
	Executor          ExecutorContractState
	Attester          AttesterContractState
	Value             AttestedComputationContract
}

// DecodeFrontmatter decodes into a caller-owned type from a defensive YAML
// node copy.
func DecodeFrontmatter[T any](frontmatter Frontmatter) (T, error) {
	var out T
	if err := ValidateYAMLNode(&frontmatter.node); err != nil {
		return out, err
	}
	node := frontmatter.YAMLNode()
	if err := node.Decode(&out); err != nil {
		return out, fmt.Errorf("%w: %v", ErrInvalidFrontmatter, err)
	}
	return out, nil
}

// EncodeFrontmatter encodes a caller-owned value into an independent,
// order-preserving Frontmatter mapping.
func EncodeFrontmatter[T any](value T) (Frontmatter, error) {
	var node yaml.Node
	if err := node.Encode(value); err != nil {
		return Frontmatter{}, fmt.Errorf("%w: %v", ErrInvalidFrontmatter, err)
	}
	return NewFrontmatterFromNode(&node)
}

// Sources returns source values containing at least one valid typed
// observation. Malformed/empty entries remain visible through SourceStates/Get
// and never become an empty fabricated source.
func (f Frontmatter) Sources() []ProvenanceSource {
	out, _ := f.SourcesContext(context.Background())
	return out
}

// SourcesContext is the cancellation-aware typed sources projection.
// Cancellation returns no partial result.
func (f Frontmatter) SourcesContext(ctx context.Context) ([]ProvenanceSource, error) {
	states, err := f.SourceStatesContext(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ProvenanceSource, 0, len(states))
	for _, state := range states {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if state.Valid {
			out = append(out, cloneProvenanceSource(state.Value))
		}
	}
	if len(out) == 0 {
		out = nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// SourceStates returns every sources[] entry with strict field observations.
func (f Frontmatter) SourceStates() []ProvenanceSourceState {
	out, _ := f.SourceStatesContext(context.Background())
	return out
}

// SourceStatesContext is the cancellation-aware lossless sources projection.
// It resolves the retained, already-validated frontmatter graph through the
// shared semantic resolver. Cancellation returns no partial result.
func (f Frontmatter) SourceStatesContext(ctx context.Context) ([]ProvenanceSourceState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root := f.mappingNode()
	resolver := newYAMLSemanticResolver(ctx)
	selection, err := resolver.mappingValue(&root, "sources")
	if err != nil {
		return nil, err
	}
	node, present := semanticSelectionNode(selection)
	if !present || node.Kind != yaml.SequenceNode {
		return nil, nil
	}
	var out []ProvenanceSourceState
	for _, item := range node.Content {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resolved, resolveErr := resolver.value(item)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if resolved.resolved {
			item = resolved.node
		}
		if item == nil || item.Kind != yaml.MappingNode {
			out = append(out, ProvenanceSourceState{Present: true})
			continue
		}
		state, stateErr := provenanceSourceStateContext(ctx, resolver, item)
		if stateErr != nil {
			return nil, stateErr
		}
		out = append(out, state)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (f Frontmatter) sourceStatesContext(ctx context.Context) ([]ProvenanceSourceState, error) {
	return f.SourceStatesContext(ctx)
}

// UsageWindow returns the shared source usage window when it is a mapping.
func (f Frontmatter) UsageWindow() (UsageWindow, bool) {
	value, valid, _ := f.UsageWindowContext(context.Background())
	return value, valid
}

// UsageWindowContext is the cancellation-aware shared usage-window
// projection.
func (f Frontmatter) UsageWindowContext(ctx context.Context) (UsageWindow, bool, error) {
	state, err := f.UsageWindowStateContext(ctx)
	if err != nil {
		return UsageWindow{}, false, err
	}
	return state.Value, state.Valid, nil
}

// UsageWindowState returns strict shared usage-window observations.
func (f Frontmatter) UsageWindowState() UsageWindowState {
	state, _ := f.UsageWindowStateContext(context.Background())
	return state
}

// UsageWindowStateContext is the cancellation-aware strict shared-window
// observation over the retained, already-validated semantic graph.
func (f Frontmatter) UsageWindowStateContext(ctx context.Context) (UsageWindowState, error) {
	if err := ctx.Err(); err != nil {
		return UsageWindowState{}, err
	}
	root := f.mappingNode()
	resolver := newYAMLSemanticResolver(ctx)
	return mappingUsageWindowStateContext(ctx, resolver, &root, "usage_window")
}

// EffectiveUsageWindow returns a source override, or the shared window.
func (f Frontmatter) EffectiveUsageWindow(source ProvenanceSource) (UsageWindow, bool) {
	value, valid, _ := f.EffectiveUsageWindowContext(context.Background(), source)
	return value, valid
}

// EffectiveUsageWindowContext returns a defensively copied source override,
// or resolves the shared window when the override is absent.
func (f Frontmatter) EffectiveUsageWindowContext(
	ctx context.Context,
	source ProvenanceSource,
) (UsageWindow, bool, error) {
	if err := ctx.Err(); err != nil {
		return UsageWindow{}, false, err
	}
	if source.UsageWindow != nil {
		value, err := copyUsageWindowContext(ctx, *source.UsageWindow)
		if err != nil {
			return UsageWindow{}, false, err
		}
		return value, true, nil
	}
	return f.UsageWindowContext(ctx)
}

// EffectiveUsageWindowState returns a present per-source observation even when
// malformed; shared fallback applies only when the override is absent.
func (f Frontmatter) EffectiveUsageWindowState(source ProvenanceSourceState) UsageWindowState {
	state, _ := f.EffectiveUsageWindowStateContext(context.Background(), source)
	return state
}

// EffectiveUsageWindowStateContext preserves a present source observation,
// including malformed data; shared fallback applies only when it is absent.
func (f Frontmatter) EffectiveUsageWindowStateContext(
	ctx context.Context,
	source ProvenanceSourceState,
) (UsageWindowState, error) {
	if err := ctx.Err(); err != nil {
		return UsageWindowState{}, err
	}
	if source.UsageWindow.Present {
		return copyUsageWindowStateContext(ctx, source.UsageWindow)
	}
	return f.UsageWindowStateContext(ctx)
}

// Generated returns generated when the key contains a mapping, including
// partial mappings. Raw malformed values remain accessible through Get.
func (f Frontmatter) Generated() (Generation, bool) {
	value, valid, _ := f.GeneratedContext(context.Background())
	return value, valid
}

// GeneratedContext is the cancellation-aware typed generation projection.
func (f Frontmatter) GeneratedContext(ctx context.Context) (Generation, bool, error) {
	state, err := f.GenerationStateContext(ctx)
	if err != nil {
		return Generation{}, false, err
	}
	return state.Value, state.Valid, nil
}

// GenerationState returns the generated observation without collapsing a
// valid actor or timestamp because its sibling is malformed.
func (f Frontmatter) GenerationState() GenerationState {
	state, _ := f.GenerationStateContext(context.Background())
	return state
}

// GenerationStateContext is the cancellation-aware generated-family
// observation over the retained, already-validated semantic graph.
func (f Frontmatter) GenerationStateContext(ctx context.Context) (GenerationState, error) {
	if err := ctx.Err(); err != nil {
		return GenerationState{}, err
	}
	root := f.mappingNode()
	resolver := newYAMLSemanticResolver(ctx)
	selection, err := resolver.mappingValue(&root, "generated")
	if err != nil {
		return GenerationState{}, err
	}
	node, present := semanticSelectionNode(selection)
	state := GenerationState{Present: present}
	if !present {
		return state, nil
	}
	if node.Kind != yaml.MappingNode {
		return state, nil
	}
	state.Mapping = true
	state.By, err = mappingStringStateContext(ctx, resolver, node, "by")
	if err != nil {
		return GenerationState{}, err
	}
	if state.By.Valid {
		validActor, actorErr := validActorContext(ctx, state.By.Value)
		if actorErr != nil {
			return GenerationState{}, actorErr
		}
		state.By.Valid = validActor
	}
	atNode, atPresent, err := semanticMappingNodeContext(resolver, node, "at")
	if err != nil {
		return GenerationState{}, err
	}
	state.At, err = dateTimeValueFromNodeContext(ctx, atNode, atPresent)
	if err != nil {
		return GenerationState{}, err
	}
	state.HasValue = state.By.Valid || state.At.State == TemporalValid
	state.Valid = state.By.Valid && state.At.State != TemporalMalformed
	if state.Valid {
		state.Value = Generation{By: state.By.Value}
		if state.At.State == TemporalValid {
			state.Value.At = state.At.Raw
		}
	}
	if err := ctx.Err(); err != nil {
		return GenerationState{}, err
	}
	return state, nil
}

// Verifications normalizes a bare mapping to one event and defensively decodes
// only valid list mappings.
func (f Frontmatter) Verifications() []Verification {
	out, _ := f.VerificationsContext(context.Background())
	return out
}

// VerificationsContext is the cancellation-aware typed verification
// projection. Cancellation returns no partial result.
func (f Frontmatter) VerificationsContext(ctx context.Context) ([]Verification, error) {
	states, err := f.VerificationStatesContext(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Verification, 0, len(states))
	for _, state := range states {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if state.Valid {
			out = append(out, state.Value)
		}
	}
	if len(out) == 0 {
		out = nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// VerificationStates returns every bare/list observation with raw actor/time
// strings and validity. Non-mapping entries remain visible as invalid states.
func (f Frontmatter) VerificationStates() []VerificationState {
	out, _ := f.VerificationStatesContext(context.Background())
	return out
}

// VerificationStatesContext is the cancellation-aware bare/list verified
// projection. It resolves the retained, already-validated frontmatter graph
// through the shared semantic resolver. Cancellation returns no partial state.
func (f Frontmatter) VerificationStatesContext(ctx context.Context) ([]VerificationState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root := f.mappingNode()
	resolver := newYAMLSemanticResolver(ctx)
	selection, err := resolver.mappingValue(&root, "verified")
	if err != nil {
		return nil, err
	}
	node, present := semanticSelectionNode(selection)
	if !present {
		return nil, nil
	}
	nodes := []*yaml.Node{node}
	if node.Kind == yaml.SequenceNode {
		nodes = node.Content
	}
	var out []VerificationState
	for _, item := range nodes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resolved, resolveErr := resolver.value(item)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if resolved.resolved {
			item = resolved.node
		}
		if item == nil || item.Kind != yaml.MappingNode {
			out = append(out, VerificationState{})
			continue
		}
		byNode, _, lookupErr := semanticMappingNodeContext(resolver, item, "by")
		if lookupErr != nil {
			return nil, lookupErr
		}
		atNode, atPresent, lookupErr := semanticMappingNodeContext(resolver, item, "at")
		if lookupErr != nil {
			return nil, lookupErr
		}
		byState, stateErr := strictStringNodeStateContext(ctx, byNode, byNode != nil)
		if stateErr != nil {
			return nil, stateErr
		}
		rawBy, rawErr := rawScalarContext(ctx, byNode)
		if rawErr != nil {
			return nil, rawErr
		}
		rawAt, rawErr := rawScalarContext(ctx, atNode)
		if rawErr != nil {
			return nil, rawErr
		}
		atValue, valueErr := dateTimeValueFromNodeContext(ctx, atNode, atPresent)
		if valueErr != nil {
			return nil, valueErr
		}
		actorValid := false
		if byState.Valid {
			actorValid, stateErr = validActorContext(ctx, byState.Value)
			if stateErr != nil {
				return nil, stateErr
			}
		}
		valid := actorValid && atValue.State == TemporalValid
		state := VerificationState{RawBy: rawBy, RawAt: rawAt, Valid: valid, AtValue: atValue}
		if valid {
			state.Value = Verification{By: byState.Value, At: atValue.Raw}
		}
		out = append(out, state)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// TrustTier derives the standard advisory trust tier from verified actors.
func (f Frontmatter) TrustTier() TrustTier {
	tier, _ := f.TrustTierContext(context.Background())
	return tier
}

// TrustTierContext derives the advisory tier from VerificationsContext.
// Cancellation returns the zero TrustTier and the context error.
func (f Frontmatter) TrustTierContext(ctx context.Context) (TrustTier, error) {
	verifications, err := f.VerificationsContext(ctx)
	if err != nil {
		return "", err
	}
	if len(verifications) == 0 {
		return TrustUnverified, nil
	}
	hasVerifier := false
	for _, verification := range verifications {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if verification.By == "" {
			continue
		}
		hasVerifier = true
		if strings.HasPrefix(verification.By, "human:") {
			return TrustHumanReviewed, nil
		}
	}
	if !hasVerifier {
		return TrustUnverified, nil
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return TrustMachineConfirmed, nil
}

// Status returns the raw lifecycle status string.
func (f Frontmatter) Status() (string, bool) {
	return f.stringScalar("status")
}

// StatusState returns the raw/effective lifecycle resolution without treating
// malformed present YAML as absence.
func (f Frontmatter) StatusState() StatusState {
	state, _ := f.StatusStateContext(context.Background())
	return state
}

// StatusStateContext returns the lifecycle status without contextless YAML
// traversal or attacker-sized scalar copies.
func (f Frontmatter) StatusStateContext(ctx context.Context) (StatusState, error) {
	state, err := f.semanticStringStateContext(ctx, "status")
	if err != nil {
		return StatusState{}, err
	}
	if !state.Present {
		return StatusState{Valid: true, Effective: StatusStable}, nil
	}
	if state.Valid {
		return StatusState{Present: true, Valid: true, Raw: state.Value, Effective: state.Value}, nil
	}
	return StatusState{Present: true, Raw: state.Raw}, ctx.Err()
}

// EffectiveStatus returns stable only when status is absent. Malformed present
// status has no effective value and returns an empty string.
func (f Frontmatter) EffectiveStatus() string {
	return f.StatusState().Effective
}

// StaleAfter returns a validated absolute lifecycle date.
func (f Frontmatter) StaleAfter() (string, bool) {
	value := f.StaleAfterValue()
	if value.State != TemporalValid {
		return "", false
	}
	return value.Raw, true
}

// StaleAfterValue validates stale_after without conflating malformed presence
// with absence.
func (f Frontmatter) StaleAfterValue() DateValue {
	node, present := f.SemanticGet("stale_after")
	if !present {
		return DateValue{State: TemporalAbsent}
	}
	raw := rawScalar(node)
	typed, validTag := dateScalar(node)
	if !validTag {
		return DateValue{Raw: raw, State: TemporalMalformed}
	}
	return parseDateValue(typed, true)
}

// IsStale applies the v0.2 inclusive boundary using the caller's explicit
// reference date. Malformed or absent stale_after is not treated as stale.
func (f Frontmatter) IsStale(referenceDate time.Time) bool {
	stale, _ := f.IsStaleContext(context.Background(), referenceDate)
	return stale
}

// IsStaleContext applies the inclusive v0.2 boundary using a cancellable
// stale_after observation.
func (f Frontmatter) IsStaleContext(ctx context.Context, referenceDate time.Time) (bool, error) {
	observation, err := f.StaleAfterObservationContext(ctx)
	if err != nil {
		return false, err
	}
	value := observation.Value
	if value.State != TemporalValid {
		return false, nil
	}
	y, m, d := referenceDate.Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return !today.Before(value.Time), ctx.Err()
}

// LegacyFallbackObservation uses YAML alias/merge semantics. Physical donor
// placement never overrides an explicit standard-family value.
func (f Frontmatter) LegacyFallbackObservation() LegacyFallbackObservation {
	observation, _ := f.legacyFallbackObservationContext(context.Background())
	return observation
}

func (f Frontmatter) legacyFallbackObservationContext(ctx context.Context) (LegacyFallbackObservation, error) {
	if err := ctx.Err(); err != nil {
		return LegacyFallbackObservation{}, err
	}
	root := f.mappingNode()
	resolver := newYAMLSemanticResolver(ctx)
	generated, err := resolver.mappingValue(&root, "generated")
	if err != nil {
		return LegacyFallbackObservation{}, err
	}
	sources, err := resolver.mappingValue(&root, "sources")
	if err != nil {
		return LegacyFallbackObservation{}, err
	}
	timestamp, err := resolver.mappingValue(&root, "timestamp")
	if err != nil {
		return LegacyFallbackObservation{}, err
	}
	generatedPresent := generated.present
	sourcesPresent := sources.present
	timestampPresent := timestamp.present
	if err := ctx.Err(); err != nil {
		return LegacyFallbackObservation{}, err
	}
	return LegacyFallbackObservation{
		GeneratedPresent: generatedPresent,
		SourcesPresent:   sourcesPresent,
		TimestampPresent: timestampPresent,
		TimestampAllowed: !generatedPresent,
		CitationsAllowed: !sourcesPresent,
		TimestampActive:  !generatedPresent && timestampPresent,
	}, nil
}

// AttestedComputation returns a typed contract only for the exact
// "Attested Computation" concept type. ComputationContractState remains the
// ungated raw field observation API.
func (f Frontmatter) AttestedComputation() (AttestedComputationContract, bool) {
	contract, valid, _ := f.attestedComputationContext(context.Background())
	return contract, valid
}

func (f Frontmatter) attestedComputationContext(ctx context.Context) (AttestedComputationContract, bool, error) {
	typed, err := f.IsAttestedComputationContext(ctx)
	if err != nil {
		return AttestedComputationContract{}, false, err
	}
	if !typed {
		return AttestedComputationContract{}, false, nil
	}
	state, err := f.ComputationContractStateContext(ctx)
	if err != nil {
		return AttestedComputationContract{}, false, err
	}
	return state.Value, state.Present && state.Valid, nil
}

// ComputationContractState returns strict field observations without
// conflating absent/malformed required booleans with valid false.
func (f Frontmatter) ComputationContractState() ComputationContractState {
	state, _ := f.ComputationContractStateContext(context.Background())
	return state
}

// ComputationContractStateContext observes every contract field with no
// partial state on cancellation.
func (f Frontmatter) ComputationContractStateContext(ctx context.Context) (ComputationContractState, error) {
	keys := []string{"runtime", "parameters", "computation", "executor", "attester"}
	state := ComputationContractState{Valid: true}
	root := f.mappingNode()
	resolver := newYAMLSemanticResolver(ctx)
	for _, key := range keys {
		_, present, err := semanticMappingNodeContext(resolver, &root, key)
		if err != nil {
			return ComputationContractState{}, err
		}
		if present {
			state.Present = true
			break
		}
	}
	if !state.Present {
		return state, ctx.Err()
	}
	var err error
	if state.Runtime, err = mappingStringStateContext(ctx, resolver, &root, "runtime"); err != nil {
		return ComputationContractState{}, err
	}
	if state.Computation, err = mappingStringStateContext(ctx, resolver, &root, "computation"); err != nil {
		return ComputationContractState{}, err
	}
	if state.Runtime.Valid {
		state.Value.Runtime = state.Runtime.Value
	}
	if state.Computation.Valid {
		state.Value.Computation = state.Computation.Value
	}
	parameters, parametersPresent, err := semanticMappingNodeContext(resolver, &root, "parameters")
	if err != nil {
		return ComputationContractState{}, err
	}
	state.ParametersPresent = parametersPresent
	state.ParametersValid = !parametersPresent
	if parametersPresent {
		state.ParametersValid = parameters.Kind == yaml.SequenceNode
		if parameters.Kind == yaml.SequenceNode {
			for _, item := range parameters.Content {
				resolved, resolveErr := resolver.value(item)
				if resolveErr != nil {
					return ComputationContractState{}, resolveErr
				}
				if resolved.resolved {
					item = resolved.node
				}
				parameter, parameterErr := computationParameterStateContext(ctx, resolver, item)
				if parameterErr != nil {
					return ComputationContractState{}, parameterErr
				}
				state.Parameters = append(state.Parameters, parameter)
				if parameter.Valid {
					state.Value.Parameters = append(state.Value.Parameters, parameter.Value)
				}
				if !parameter.Valid {
					state.ParametersValid = false
				}
			}
		}
	}
	executor, executorPresent, err := semanticMappingNodeContext(resolver, &root, "executor")
	if err != nil {
		return ComputationContractState{}, err
	}
	if state.Executor, err = executorContractStateContext(ctx, resolver, executor, executorPresent); err != nil {
		return ComputationContractState{}, err
	}
	if state.Executor.Valid && state.Executor.HasValue {
		value := state.Executor.Value
		value.Receipt = append([]string(nil), value.Receipt...)
		state.Value.Executor = &value
	}
	attester, attesterPresent, err := semanticMappingNodeContext(resolver, &root, "attester")
	if err != nil {
		return ComputationContractState{}, err
	}
	if state.Attester, err = attesterContractStateContext(ctx, resolver, attester, attesterPresent); err != nil {
		return ComputationContractState{}, err
	}
	if state.Attester.Valid && state.Attester.HasValue {
		value := state.Attester.Value
		state.Value.Attester = &value
	}
	for _, scalar := range []ScalarValueState{state.Runtime, state.Computation} {
		if scalar.Present && !scalar.Valid {
			state.Valid = false
		}
	}
	if !state.ParametersValid || state.Executor.Present && !state.Executor.Valid ||
		state.Attester.Present && !state.Attester.Valid {
		state.Valid = false
	}
	if err := ctx.Err(); err != nil {
		return ComputationContractState{}, err
	}
	return state, nil
}

// EffectiveContentChangeTime returns generated.at when generated is present.
// Legacy timestamp is consulted only when generated is completely absent.
func (f Frontmatter) EffectiveContentChangeTime() (string, bool) {
	value, valid, _ := f.EffectiveContentChangeTimeContext(context.Background())
	return value, valid
}

// EffectiveContentChangeTimeContext applies generated.at/legacy timestamp
// precedence with cancellation and no partial string.
func (f Frontmatter) EffectiveContentChangeTimeContext(ctx context.Context) (string, bool, error) {
	value, err := f.EffectiveContentChangeTimeValueContext(ctx)
	if err != nil {
		return "", false, err
	}
	if value.State != TemporalValid {
		return "", false, nil
	}
	return value.Raw, true, nil
}

// GeneratedAtValue validates generated.at and preserves malformed presence.
func (f Frontmatter) GeneratedAtValue() DateTimeValue {
	value, _ := f.GeneratedAtValueContext(context.Background())
	return value
}

// GeneratedAtValueContext validates generated.at without requiring a valid
// generated.by observation.
func (f Frontmatter) GeneratedAtValueContext(ctx context.Context) (DateTimeValue, error) {
	if err := ctx.Err(); err != nil {
		return DateTimeValue{}, err
	}
	root := f.mappingNode()
	resolver := newYAMLSemanticResolver(ctx)
	selection, err := resolver.mappingValue(&root, "generated")
	if err != nil {
		return DateTimeValue{}, err
	}
	node, present := semanticSelectionNode(selection)
	if !present {
		return DateTimeValue{State: TemporalAbsent}, nil
	}
	if node.Kind != yaml.MappingNode {
		raw := ""
		if node.Kind == yaml.ScalarNode {
			raw, err = stringFromStringContext(ctx, node.Value)
			if err != nil {
				return DateTimeValue{}, err
			}
		}
		return DateTimeValue{Raw: raw, State: TemporalMalformed}, nil
	}
	atNode, atPresent, err := semanticMappingNodeContext(resolver, node, "at")
	if err != nil {
		return DateTimeValue{}, err
	}
	return dateTimeValueFromNodeContext(ctx, atNode, atPresent)
}

// EffectiveContentChangeTimeValue validates generated.at, or legacy timestamp
// only when generated is completely absent.
func (f Frontmatter) EffectiveContentChangeTimeValue() DateTimeValue {
	value, _ := f.EffectiveContentChangeTimeValueContext(context.Background())
	return value
}

// EffectiveContentChangeTimeValueContext validates generated.at, or legacy
// timestamp only when generated is completely absent.
func (f Frontmatter) EffectiveContentChangeTimeValueContext(ctx context.Context) (DateTimeValue, error) {
	observation, err := f.legacyFallbackObservationContext(ctx)
	if err != nil {
		return DateTimeValue{}, err
	}
	if !observation.TimestampAllowed {
		return f.GeneratedAtValueContext(ctx)
	}
	node, present, err := f.SemanticGetContext(ctx, "timestamp")
	if err != nil {
		return DateTimeValue{}, err
	}
	return dateTimeValueFromNodeContext(ctx, node, present)
}

func computationParameterState(node *yaml.Node) ComputationParameterState {
	state, _ := computationParameterStateContext(context.Background(), newYAMLSemanticResolver(context.Background()), node)
	return state
}

func computationParameterStateContext(
	ctx context.Context,
	resolver *yamlSemanticResolver,
	node *yaml.Node,
) (ComputationParameterState, error) {
	if err := ctx.Err(); err != nil {
		return ComputationParameterState{}, err
	}
	state := ComputationParameterState{Present: true}
	if node == nil || node.Kind != yaml.MappingNode {
		return state, nil
	}
	state.Mapping = true
	var err error
	if state.Name, err = mappingStringStateContext(ctx, resolver, node, "name"); err != nil {
		return ComputationParameterState{}, err
	}
	if state.Type, err = mappingStringStateContext(ctx, resolver, node, "type"); err != nil {
		return ComputationParameterState{}, err
	}
	if state.Required, err = mappingBooleanStateContext(ctx, resolver, node, "required"); err != nil {
		return ComputationParameterState{}, err
	}
	if state.Name.Valid {
		state.Value.Name, state.HasValue = state.Name.Value, true
	}
	if state.Type.Valid {
		state.Value.Type, state.HasValue = state.Type.Value, true
	}
	if state.Required.Valid {
		state.Value.Required, state.HasValue = state.Required.Value, true
	}
	state.Valid = state.Name.Valid && state.Type.Valid && state.Required.Valid
	if err := ctx.Err(); err != nil {
		return ComputationParameterState{}, err
	}
	return state, nil
}

func executorContractState(node *yaml.Node, present bool) ExecutorContractState {
	state, _ := executorContractStateContext(context.Background(), newYAMLSemanticResolver(context.Background()), node, present)
	return state
}

func executorContractStateContext(
	ctx context.Context,
	resolver *yamlSemanticResolver,
	node *yaml.Node,
	present bool,
) (ExecutorContractState, error) {
	if !present {
		return ExecutorContractState{}, ctx.Err()
	}
	state := ExecutorContractState{Present: true}
	if node == nil || node.Kind != yaml.MappingNode {
		return state, nil
	}
	state.Mapping = true
	var err error
	if state.Resource, err = mappingStringStateContext(ctx, resolver, node, "resource"); err != nil {
		return ExecutorContractState{}, err
	}
	if state.Receipt, err = mappingStringSequenceStateContext(ctx, resolver, node, "receipt"); err != nil {
		return ExecutorContractState{}, err
	}
	if state.Resource.Valid {
		state.Value.Resource, state.HasValue = state.Resource.Value, true
	}
	if state.Receipt.Present {
		state.Value.Receipt = append([]string(nil), state.Receipt.Values...)
		if len(state.Receipt.Values) > 0 {
			state.HasValue = true
		}
	}
	state.Valid = state.Resource.Valid &&
		(!state.Receipt.Present || state.Receipt.Valid)
	if err := ctx.Err(); err != nil {
		return ExecutorContractState{}, err
	}
	return state, nil
}

func attesterContractState(node *yaml.Node, present bool) AttesterContractState {
	state, _ := attesterContractStateContext(context.Background(), newYAMLSemanticResolver(context.Background()), node, present)
	return state
}

func attesterContractStateContext(
	ctx context.Context,
	resolver *yamlSemanticResolver,
	node *yaml.Node,
	present bool,
) (AttesterContractState, error) {
	if !present {
		return AttesterContractState{}, ctx.Err()
	}
	state := AttesterContractState{Present: true}
	if node == nil || node.Kind != yaml.MappingNode {
		return state, nil
	}
	state.Mapping = true
	var err error
	if state.Resource, err = mappingStringStateContext(ctx, resolver, node, "resource"); err != nil {
		return AttesterContractState{}, err
	}
	if state.Resource.Valid {
		state.Value.Resource, state.HasValue = state.Resource.Value, true
	}
	state.Valid = state.Resource.Valid
	if err := ctx.Err(); err != nil {
		return AttesterContractState{}, err
	}
	return state, nil
}

func mappingBooleanState(node *yaml.Node, key string) BooleanValueState {
	state, _ := mappingBooleanStateContext(context.Background(), newYAMLSemanticResolver(context.Background()), node, key)
	return state
}

func mappingBooleanStateContext(ctx context.Context, resolver *yamlSemanticResolver, node *yaml.Node, key string) (BooleanValueState, error) {
	value, present, err := semanticMappingNodeContext(resolver, node, key)
	if err != nil || !present {
		return BooleanValueState{}, err
	}
	state := BooleanValueState{Present: true}
	if value == nil || value.Kind != yaml.ScalarNode {
		return state, nil
	}
	state.Raw, err = stringFromStringContext(ctx, value.Value)
	if err != nil {
		return BooleanValueState{}, err
	}
	if value.Tag != "!!bool" {
		return state, nil
	}
	parsed, parseErr := strconv.ParseBool(state.Raw)
	if parseErr == nil {
		state.Valid, state.Value = true, parsed
	}
	if err := ctx.Err(); err != nil {
		return BooleanValueState{}, err
	}
	return state, nil
}

func mappingStringSequenceState(node *yaml.Node, key string) StringSequenceState {
	state, _ := mappingStringSequenceStateContext(context.Background(), newYAMLSemanticResolver(context.Background()), node, key)
	return state
}

func mappingStringSequenceStateContext(ctx context.Context, resolver *yamlSemanticResolver, node *yaml.Node, key string) (StringSequenceState, error) {
	value, present, err := semanticMappingNodeContext(resolver, node, key)
	if err != nil || !present {
		return StringSequenceState{}, err
	}
	state := StringSequenceState{Present: true}
	if value == nil || value.Kind != yaml.SequenceNode {
		return state, nil
	}
	state.Valid = true
	state.Items = make([]ScalarValueState, 0, len(value.Content))
	for _, item := range value.Content {
		resolved, resolveErr := resolver.value(item)
		if resolveErr != nil {
			return StringSequenceState{}, resolveErr
		}
		if resolved.resolved {
			item = resolved.node
		}
		observation, observationErr := strictStringNodeStateContext(ctx, item, true)
		if observationErr != nil {
			return StringSequenceState{}, observationErr
		}
		state.Items = append(state.Items, observation)
		if observation.Valid {
			state.Values = append(state.Values, observation.Value)
		} else {
			state.Valid = false
		}
	}
	if err := ctx.Err(); err != nil {
		return StringSequenceState{}, err
	}
	return state, nil
}

func usageWindowState(node *yaml.Node, present bool) UsageWindowState {
	if !present {
		return UsageWindowState{}
	}
	state := UsageWindowState{Present: true}
	if node == nil || node.Kind != yaml.MappingNode {
		return state
	}
	state.Mapping = true
	state.From = mappingDateValue(node, "from")
	state.To = mappingDateValue(node, "to")
	if state.From.State == TemporalValid {
		state.Value.From, state.HasValue = state.From.Raw, true
	}
	if state.To.State == TemporalValid {
		state.Value.To, state.HasValue = state.To.Raw, true
	}
	state.Valid = state.From.State == TemporalValid && state.To.State == TemporalValid &&
		!state.From.Time.After(state.To.Time)
	return state
}

func mappingUsageWindowState(node *yaml.Node, key string) UsageWindowState {
	value := firstMappingValue(node, key)
	return usageWindowState(value, value != nil)
}

func semanticSelectionNode(selection yamlSemanticSelection) (*yaml.Node, bool) {
	if !selection.present {
		return nil, false
	}
	if selection.ambiguous || selection.resolved == nil {
		return &yaml.Node{}, true
	}
	return selection.resolved, true
}

func semanticMappingNodeContext(
	resolver *yamlSemanticResolver,
	node *yaml.Node,
	key string,
) (*yaml.Node, bool, error) {
	selection, err := resolver.mappingValue(node, key)
	if err != nil {
		return nil, false, err
	}
	value, present := semanticSelectionNode(selection)
	return value, present, nil
}

func provenanceSourceStateContext(
	ctx context.Context,
	resolver *yamlSemanticResolver,
	node *yaml.Node,
) (ProvenanceSourceState, error) {
	if err := ctx.Err(); err != nil {
		return ProvenanceSourceState{}, err
	}
	if node == nil || node.Kind != yaml.MappingNode {
		return ProvenanceSourceState{Present: true}, nil
	}
	state := ProvenanceSourceState{Present: true, Mapping: true}
	var err error
	if state.ID, err = mappingStringStateContext(ctx, resolver, node, "id"); err != nil {
		return ProvenanceSourceState{}, err
	}
	if state.Resource, err = mappingStringStateContext(ctx, resolver, node, "resource"); err != nil {
		return ProvenanceSourceState{}, err
	}
	if state.Title, err = mappingStringStateContext(ctx, resolver, node, "title"); err != nil {
		return ProvenanceSourceState{}, err
	}
	if state.Author, err = mappingStringStateContext(ctx, resolver, node, "author"); err != nil {
		return ProvenanceSourceState{}, err
	}
	if state.UsageCount, err = mappingUint64StateContext(ctx, resolver, node, "usage_count"); err != nil {
		return ProvenanceSourceState{}, err
	}
	if state.LastModified, err = mappingDateValueContext(ctx, resolver, node, "last_modified"); err != nil {
		return ProvenanceSourceState{}, err
	}
	if state.UsageWindow, err = mappingUsageWindowStateContext(ctx, resolver, node, "usage_window"); err != nil {
		return ProvenanceSourceState{}, err
	}

	if state.ID.Valid {
		state.Value.ID, state.HasValue = state.ID.Value, true
	}
	if state.Resource.Valid {
		state.Value.Resource, state.HasValue = state.Resource.Value, true
	}
	if state.Title.Valid {
		state.Value.Title, state.HasValue = state.Title.Value, true
	}
	if state.Author.Valid {
		state.Value.Author, state.HasValue = state.Author.Value, true
	}
	if state.UsageCount.Valid {
		value := state.UsageCount.Value
		state.Value.UsageCount = &value
		state.HasValue = true
	}
	if state.LastModified.State == TemporalValid {
		state.Value.LastModified = state.LastModified.Raw
		state.HasValue = true
	}
	if state.UsageWindow.HasValue {
		value := state.UsageWindow.Value
		state.Value.UsageWindow = &value
		state.HasValue = true
	}
	state.Valid = state.Resource.Valid && sourceStateFieldsValid(state)
	if err := ctx.Err(); err != nil {
		return ProvenanceSourceState{}, err
	}
	return state, nil
}

func mappingStringStateContext(
	ctx context.Context,
	resolver *yamlSemanticResolver,
	node *yaml.Node,
	key string,
) (ScalarValueState, error) {
	value, present, err := semanticMappingNodeContext(resolver, node, key)
	if err != nil {
		return ScalarValueState{}, err
	}
	return strictStringNodeStateContext(ctx, value, present)
}

func mappingUint64StateContext(
	ctx context.Context,
	resolver *yamlSemanticResolver,
	node *yaml.Node,
	key string,
) (Uint64ValueState, error) {
	value, present, err := semanticMappingNodeContext(resolver, node, key)
	if err != nil {
		return Uint64ValueState{}, err
	}
	if !present {
		return Uint64ValueState{}, nil
	}
	state := Uint64ValueState{Present: true}
	state.Raw, err = rawScalarContext(ctx, value)
	if err != nil {
		return Uint64ValueState{}, err
	}
	if value.Kind != yaml.ScalarNode || value.Tag != "!!int" {
		return state, nil
	}
	parsed, parseErr := parseNonNegativeYAMLUint64Context(ctx, value.Value)
	if parseErr != nil && ctx.Err() != nil {
		return Uint64ValueState{}, ctx.Err()
	}
	if parseErr == nil {
		state.Valid, state.Value = true, parsed
	}
	return state, nil
}

func mappingDateValueContext(
	ctx context.Context,
	resolver *yamlSemanticResolver,
	node *yaml.Node,
	key string,
) (DateValue, error) {
	value, present, err := semanticMappingNodeContext(resolver, node, key)
	if err != nil {
		return DateValue{}, err
	}
	if !present {
		return DateValue{State: TemporalAbsent}, nil
	}
	return dateValueFromNodeContext(ctx, value)
}

func mappingUsageWindowStateContext(
	ctx context.Context,
	resolver *yamlSemanticResolver,
	node *yaml.Node,
	key string,
) (UsageWindowState, error) {
	value, present, err := semanticMappingNodeContext(resolver, node, key)
	if err != nil {
		return UsageWindowState{}, err
	}
	if !present {
		return UsageWindowState{}, nil
	}
	state := UsageWindowState{Present: true}
	if value == nil || value.Kind != yaml.MappingNode {
		return state, nil
	}
	state.Mapping = true
	if state.From, err = mappingDateValueContext(ctx, resolver, value, "from"); err != nil {
		return UsageWindowState{}, err
	}
	if state.To, err = mappingDateValueContext(ctx, resolver, value, "to"); err != nil {
		return UsageWindowState{}, err
	}
	if state.From.State == TemporalValid {
		state.Value.From, state.HasValue = state.From.Raw, true
	}
	if state.To.State == TemporalValid {
		state.Value.To, state.HasValue = state.To.Raw, true
	}
	state.Valid = state.From.State == TemporalValid && state.To.State == TemporalValid &&
		!state.From.Time.After(state.To.Time)
	if err := ctx.Err(); err != nil {
		return UsageWindowState{}, err
	}
	return state, nil
}

func dateValueFromNodeContext(ctx context.Context, node *yaml.Node) (DateValue, error) {
	if err := ctx.Err(); err != nil {
		return DateValue{}, err
	}
	if node == nil || node.Kind != yaml.ScalarNode {
		return DateValue{State: TemporalMalformed}, nil
	}
	owned, err := stringFromStringContext(ctx, node.Value)
	if err != nil {
		return DateValue{}, err
	}
	malformed := DateValue{Raw: owned, State: TemporalMalformed}
	if node.Tag != "!!str" && node.Tag != "!!timestamp" {
		return malformed, nil
	}
	nonBlank, err := nonBlankStringValueContext(ctx, node.Value)
	if err != nil {
		return DateValue{}, err
	}
	if !nonBlank || len(owned) != len("2006-01-02") {
		return malformed, nil
	}
	parsed := parseDateValue(owned, true)
	if err := ctx.Err(); err != nil {
		return DateValue{}, err
	}
	return parsed, nil
}

func copyUsageWindowContext(ctx context.Context, value UsageWindow) (UsageWindow, error) {
	from, err := stringFromStringContext(ctx, value.From)
	if err != nil {
		return UsageWindow{}, err
	}
	to, err := stringFromStringContext(ctx, value.To)
	if err != nil {
		return UsageWindow{}, err
	}
	return UsageWindow{From: from, To: to}, nil
}

func copyUsageWindowStateContext(ctx context.Context, state UsageWindowState) (UsageWindowState, error) {
	fromRaw, err := stringFromStringContext(ctx, state.From.Raw)
	if err != nil {
		return UsageWindowState{}, err
	}
	toRaw, err := stringFromStringContext(ctx, state.To.Raw)
	if err != nil {
		return UsageWindowState{}, err
	}
	value, err := copyUsageWindowContext(ctx, state.Value)
	if err != nil {
		return UsageWindowState{}, err
	}
	state.From.Raw = fromRaw
	state.To.Raw = toRaw
	state.Value = value
	if err := ctx.Err(); err != nil {
		return UsageWindowState{}, err
	}
	return state, nil
}

func mappingStringState(node *yaml.Node, key string) ScalarValueState {
	value := firstMappingValue(node, key)
	return strictStringNodeState(value, value != nil)
}

func mappingUint64State(node *yaml.Node, key string) Uint64ValueState {
	value := firstMappingValue(node, key)
	if value == nil {
		return Uint64ValueState{}
	}
	state := Uint64ValueState{Present: true, Raw: rawScalar(value)}
	if value.Kind != yaml.ScalarNode || value.Tag != "!!int" {
		return state
	}
	parsed, err := ParseNonNegativeYAMLUint64(value.Value)
	if err != nil {
		return state
	}
	state.Valid, state.Value = true, parsed
	return state
}

func mappingDateValue(node *yaml.Node, key string) DateValue {
	value := firstMappingValue(node, key)
	if value == nil {
		return DateValue{State: TemporalAbsent}
	}
	raw := rawScalar(value)
	typed, validTag := dateScalar(value)
	if !validTag {
		return DateValue{Raw: raw, State: TemporalMalformed}
	}
	return parseDateValue(typed, true)
}

func sourceStateFieldsValid(state ProvenanceSourceState) bool {
	for _, scalar := range []ScalarValueState{state.ID, state.Resource, state.Title, state.Author} {
		if scalar.Present && !scalar.Valid {
			return false
		}
	}
	if state.UsageCount.Present && !state.UsageCount.Valid {
		return false
	}
	if state.LastModified.State == TemporalMalformed {
		return false
	}
	return !state.UsageWindow.Present || state.UsageWindow.Valid
}

func firstMappingValue(node *yaml.Node, key string) *yaml.Node {
	value, _ := semanticMappingValue(node, key, make(map[*yaml.Node]bool))
	return value
}

func mappingNonBlankString(node *yaml.Node, key string) string {
	value, _ := nonBlankString(firstMappingValue(node, key))
	return value
}

func nonBlankString(node *yaml.Node) (string, bool) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!str" || strings.TrimSpace(node.Value) == "" {
		return "", false
	}
	return node.Value, true
}

func rawScalar(node *yaml.Node) string {
	value, _ := rawScalarContext(context.Background(), node)
	return value
}

func rawScalarContext(ctx context.Context, node *yaml.Node) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if node == nil || node.Kind != yaml.ScalarNode {
		return "", nil
	}
	return stringFromStringContext(ctx, node.Value)
}

func mappingRawScalar(node *yaml.Node, key string) string {
	return rawScalar(firstMappingValue(node, key))
}

func mappingDateTimeScalar(node *yaml.Node, key string) string {
	value, _ := dateTimeScalar(firstMappingValue(node, key))
	return value
}

func dateScalar(node *yaml.Node) (string, bool) {
	if node == nil || node.Kind != yaml.ScalarNode || (node.Tag != "!!str" && node.Tag != "!!timestamp") || strings.TrimSpace(node.Value) == "" {
		return "", false
	}
	return node.Value, true
}

func dateTimeScalar(node *yaml.Node) (string, bool) {
	return dateScalar(node)
}

func parseDateValue(raw string, present bool) DateValue {
	value, _ := parseDateValueContext(context.Background(), raw, present)
	return value
}

func parseDateValueContext(ctx context.Context, raw string, present bool) (DateValue, error) {
	if !present {
		return DateValue{State: TemporalAbsent}, ctx.Err()
	}
	owned, err := stringFromStringContext(ctx, raw)
	if err != nil {
		return DateValue{}, err
	}
	value, err := time.Parse("2006-01-02", owned)
	if err != nil {
		return DateValue{Raw: owned, State: TemporalMalformed}, nil
	}
	if err := ctx.Err(); err != nil {
		return DateValue{}, err
	}
	return DateValue{Raw: owned, Time: value, State: TemporalValid}, nil
}

func parseDateTimeValue(raw string, present bool) DateTimeValue {
	value, _ := parseDateTimeValueContext(context.Background(), raw, present)
	return value
}

func parseDateTimeValueContext(ctx context.Context, raw string, present bool) (DateTimeValue, error) {
	if !present {
		return DateTimeValue{State: TemporalAbsent}, ctx.Err()
	}
	owned, err := stringFromStringContext(ctx, raw)
	if err != nil {
		return DateTimeValue{}, err
	}
	malformed := DateTimeValue{Raw: owned, State: TemporalMalformed}
	bounded, validShape, err := boundedRFC3339ForParseContext(ctx, owned)
	if err != nil {
		return DateTimeValue{}, err
	}
	if !validShape {
		return malformed, nil
	}
	value, parseErr := time.Parse(time.RFC3339, bounded)
	if err := ctx.Err(); err != nil {
		return DateTimeValue{}, err
	}
	if parseErr != nil {
		return malformed, nil
	}
	return DateTimeValue{Raw: owned, Time: value, State: TemporalValid}, nil
}

func dateTimeValueFromNodeContext(
	ctx context.Context,
	node *yaml.Node,
	present bool,
) (DateTimeValue, error) {
	if err := ctx.Err(); err != nil {
		return DateTimeValue{}, err
	}
	if !present {
		return DateTimeValue{State: TemporalAbsent}, nil
	}
	if node == nil || node.Kind != yaml.ScalarNode {
		return DateTimeValue{State: TemporalMalformed}, nil
	}
	owned, err := stringFromStringContext(ctx, node.Value)
	if err != nil {
		return DateTimeValue{}, err
	}
	malformed := DateTimeValue{Raw: owned, State: TemporalMalformed}
	if node.Tag != "!!str" && node.Tag != "!!timestamp" {
		return malformed, nil
	}
	nonBlank, err := nonBlankStringValueContext(ctx, node.Value)
	if err != nil {
		return DateTimeValue{}, err
	}
	if !nonBlank {
		return malformed, nil
	}
	return parseDateTimeValueContext(ctx, owned, true)
}

func boundedRFC3339ForParseContext(ctx context.Context, value string) (string, bool, error) {
	const boundedRFC3339Length = 64
	if len(value) <= boundedRFC3339Length {
		return value, true, nil
	}
	// RFC3339 has fixed-width date/time and zone syntax. Inputs longer than the
	// bounded parser surface can only be valid through an arbitrary-length
	// fractional-second run, which Go accepts while retaining nanoseconds.
	if len(value) < 22 || value[19] != '.' && value[19] != ',' {
		return "", false, nil
	}
	zoneStart := -1
	for index := 20; index < len(value); index++ {
		if (index-20)%contextStringChunkSize == 0 {
			if err := ctx.Err(); err != nil {
				return "", false, err
			}
		}
		current := value[index]
		if current >= '0' && current <= '9' {
			continue
		}
		if current == 'Z' || current == '+' || current == '-' {
			zoneStart = index
			break
		}
		return "", false, nil
	}
	if zoneStart <= 20 {
		return "", false, nil
	}
	zone := value[zoneStart:]
	if zone != "Z" && (len(zone) != len("+00:00") || zone[0] != '+' && zone[0] != '-') {
		return "", false, nil
	}
	if zone != "Z" {
		for _, index := range []int{1, 2, 4, 5} {
			if zone[index] < '0' || zone[index] > '9' {
				return "", false, nil
			}
		}
		if zone[3] != ':' {
			return "", false, nil
		}
	}
	fractionEnd := min(zoneStart, 29)
	bounded := value[:fractionEnd] + zone
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	return bounded, true, nil
}

func validActorContext(ctx context.Context, actor string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if actor == "" {
		return false, nil
	}
	first := true
	lastSpace := false
	slashes := 0
	slashIndex := -1
	nextCheck := 0
	for offset := 0; offset < len(actor); {
		if offset >= nextCheck {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			nextCheck = offset + contextStringChunkSize
		}
		current, size := utf8.DecodeRuneInString(actor[offset:])
		if current == utf8.RuneError && size == 1 {
			return false, nil
		}
		space := unicode.IsSpace(current)
		if first && space || unicode.IsControl(current) {
			return false, nil
		}
		first = false
		lastSpace = space
		if current == '/' {
			slashes++
			slashIndex = offset
		}
		offset += size
	}
	if lastSpace {
		return false, nil
	}
	for _, prefix := range []string{"human:", "process:"} {
		if strings.HasPrefix(actor, prefix) {
			nonBlank, err := nonBlankStringValueContext(ctx, actor[len(prefix):])
			return nonBlank, err
		}
	}
	if slashes != 1 {
		return false, nil
	}
	producerValid, err := nonBlankStringValueContext(ctx, actor[:slashIndex])
	if err != nil || !producerValid {
		return false, err
	}
	versionValid, err := nonBlankStringValueContext(ctx, actor[slashIndex+1:])
	if err != nil {
		return false, err
	}
	return versionValid, nil
}

// ValidActor reports whether actor follows one of the v0.2 actor conventions.
func ValidActor(actor string) bool {
	valid, _ := validActorContext(context.Background(), actor)
	return valid
}

// AtValue validates generated.at.
func (g Generation) AtValue() DateTimeValue {
	return parseDateTimeValue(g.At, g.At != "")
}

// AtValue validates verified[].at.
func (v Verification) AtValue() DateTimeValue {
	return parseDateTimeValue(v.At, v.At != "")
}

// FromValue validates usage_window.from.
func (w UsageWindow) FromValue() DateValue {
	return parseDateValue(w.From, w.From != "")
}

// ToValue validates usage_window.to.
func (w UsageWindow) ToValue() DateValue {
	return parseDateValue(w.To, w.To != "")
}

// LastModifiedValue validates sources[].last_modified.
func (s ProvenanceSource) LastModifiedValue() DateValue {
	return parseDateValue(s.LastModified, s.LastModified != "")
}

// Document convenience accessors keep callers out of raw YAML traversal.
func (d Document) Sources() []ProvenanceSource {
	out, _ := d.SourcesContext(context.Background())
	return out
}
func (d Document) SourcesContext(ctx context.Context) ([]ProvenanceSource, error) {
	return d.Frontmatter.SourcesContext(ctx)
}
func (d Document) SourceStates() []ProvenanceSourceState {
	out, _ := d.SourceStatesContext(context.Background())
	return out
}
func (d Document) SourceStatesContext(ctx context.Context) ([]ProvenanceSourceState, error) {
	return d.Frontmatter.SourceStatesContext(ctx)
}
func (d Document) UsageWindow() (UsageWindow, bool) {
	value, valid, _ := d.UsageWindowContext(context.Background())
	return value, valid
}
func (d Document) UsageWindowContext(ctx context.Context) (UsageWindow, bool, error) {
	return d.Frontmatter.UsageWindowContext(ctx)
}
func (d Document) UsageWindowState() UsageWindowState {
	state, _ := d.UsageWindowStateContext(context.Background())
	return state
}
func (d Document) UsageWindowStateContext(ctx context.Context) (UsageWindowState, error) {
	return d.Frontmatter.UsageWindowStateContext(ctx)
}
func (d Document) EffectiveUsageWindow(source ProvenanceSource) (UsageWindow, bool) {
	value, valid, _ := d.EffectiveUsageWindowContext(context.Background(), source)
	return value, valid
}
func (d Document) EffectiveUsageWindowContext(
	ctx context.Context,
	source ProvenanceSource,
) (UsageWindow, bool, error) {
	return d.Frontmatter.EffectiveUsageWindowContext(ctx, source)
}
func (d Document) EffectiveUsageWindowState(source ProvenanceSourceState) UsageWindowState {
	state, _ := d.EffectiveUsageWindowStateContext(context.Background(), source)
	return state
}
func (d Document) EffectiveUsageWindowStateContext(
	ctx context.Context,
	source ProvenanceSourceState,
) (UsageWindowState, error) {
	return d.Frontmatter.EffectiveUsageWindowStateContext(ctx, source)
}
func (d Document) Generated() (Generation, bool) {
	value, valid, _ := d.GeneratedContext(context.Background())
	return value, valid
}
func (d Document) GeneratedContext(ctx context.Context) (Generation, bool, error) {
	return d.Frontmatter.GeneratedContext(ctx)
}
func (d Document) GenerationState() GenerationState {
	state, _ := d.GenerationStateContext(context.Background())
	return state
}
func (d Document) GenerationStateContext(ctx context.Context) (GenerationState, error) {
	return d.Frontmatter.GenerationStateContext(ctx)
}
func (d Document) Verifications() []Verification {
	out, _ := d.VerificationsContext(context.Background())
	return out
}
func (d Document) VerificationsContext(ctx context.Context) ([]Verification, error) {
	return d.Frontmatter.VerificationsContext(ctx)
}
func (d Document) VerificationStates() []VerificationState {
	out, _ := d.VerificationStatesContext(context.Background())
	return out
}
func (d Document) VerificationStatesContext(ctx context.Context) ([]VerificationState, error) {
	return d.Frontmatter.VerificationStatesContext(ctx)
}
func (d Document) TrustTier() TrustTier {
	tier, _ := d.TrustTierContext(context.Background())
	return tier
}
func (d Document) TrustTierContext(ctx context.Context) (TrustTier, error) {
	return d.Frontmatter.TrustTierContext(ctx)
}
func (d Document) Status() (string, bool)   { return d.Frontmatter.Status() }
func (d Document) StatusState() StatusState { return d.Frontmatter.StatusState() }
func (d Document) StatusStateContext(ctx context.Context) (StatusState, error) {
	return d.Frontmatter.StatusStateContext(ctx)
}
func (d Document) EffectiveStatus() string              { return d.Frontmatter.EffectiveStatus() }
func (d Document) StaleAfter() (string, bool)           { return d.Frontmatter.StaleAfter() }
func (d Document) StaleAfterValue() DateValue           { return d.Frontmatter.StaleAfterValue() }
func (d Document) IsStale(referenceDate time.Time) bool { return d.Frontmatter.IsStale(referenceDate) }
func (d Document) IsStaleContext(ctx context.Context, referenceDate time.Time) (bool, error) {
	return d.Frontmatter.IsStaleContext(ctx, referenceDate)
}
func (d Document) LegacyFallbackObservation() LegacyFallbackObservation {
	observation, _ := d.LegacyFallbackObservationContext(context.Background())
	return observation
}

// LegacyFallbackObservationContext adds parser-owned legacy Citations presence
// to the replacement-family observation without hiding parser/walk
// cancellation from context-bearing callers.
func (d Document) LegacyFallbackObservationContext(ctx context.Context) (LegacyFallbackObservation, error) {
	if err := ctx.Err(); err != nil {
		return LegacyFallbackObservation{}, err
	}
	observation, err := d.Frontmatter.legacyFallbackObservationContext(ctx)
	if err != nil {
		return LegacyFallbackObservation{}, err
	}
	projection, err := markdownowner.CollectCitationSectionProjection(ctx, []byte(d.Body))
	if err != nil {
		return LegacyFallbackObservation{}, err
	}
	observation.CitationsPresent = err == nil && projection.Found
	observation.CitationsActive = observation.CitationsAllowed && observation.CitationsPresent
	if err := ctx.Err(); err != nil {
		return LegacyFallbackObservation{}, err
	}
	return observation, nil
}
func (d Document) AttestedComputation() (AttestedComputationContract, bool) {
	state := d.AttestedComputationState()
	if !state.TypeMatches || !state.Valid ||
		!state.ContractPresent || !state.ContractState.Valid {
		return AttestedComputationContract{}, false
	}
	return state.Contract, true
}
func (d Document) EffectiveContentChangeTime() (string, bool) {
	value, valid, _ := d.EffectiveContentChangeTimeContext(context.Background())
	return value, valid
}
func (d Document) EffectiveContentChangeTimeContext(ctx context.Context) (string, bool, error) {
	return d.Frontmatter.EffectiveContentChangeTimeContext(ctx)
}
func (d Document) GeneratedAtValue() DateTimeValue {
	value, _ := d.GeneratedAtValueContext(context.Background())
	return value
}
func (d Document) GeneratedAtValueContext(ctx context.Context) (DateTimeValue, error) {
	return d.Frontmatter.GeneratedAtValueContext(ctx)
}
func (d Document) EffectiveContentChangeTimeValue() DateTimeValue {
	value, _ := d.EffectiveContentChangeTimeValueContext(context.Background())
	return value
}
func (d Document) EffectiveContentChangeTimeValueContext(ctx context.Context) (DateTimeValue, error) {
	return d.Frontmatter.EffectiveContentChangeTimeValueContext(ctx)
}
