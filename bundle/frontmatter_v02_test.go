package bundle

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestFrontmatterV02TypedAccessors(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter, err := ParseFrontmatter(`
type: Attested Computation
sources:
  - id: policy
    resource: https://example.com/policy
    author: team:finance
    usage_count: 5000
    last_modified: 2026-06-18
  - id: local
    resource: references/source.json
    usage_window: {from: 2026-05-01, to: 2026-05-31}
usage_window: {from: 2026-06-01, to: 2026-06-30}
generated: {by: agent/model, at: 2026-06-28T14:00:00Z, x-extra: keep}
verified:
  - {by: process:nightly, at: 2026-06-29T02:00:00Z}
  - {by: human:sergey, at: 2026-06-29T08:00:00Z}
status: deprecated
stale_after: 2026-07-29
runtime: bigquery
parameters:
  - {name: year, type: integer, required: true}
computation: references/revenue.sql
executor:
  resource: references/run.md
  receipt: [job_id, executed_sql]
attester: {resource: references/check.py}
relations: {depends_on: [{target: policy}]}
`)
	if err != nil {
		t.Fatalf("ParseFrontmatter() error = %v", err)
	}

	// Act.
	sources := frontmatter.Sources()
	shared, sharedOK := frontmatter.UsageWindow()
	effectiveFirst, firstOK := frontmatter.EffectiveUsageWindow(sources[0])
	effectiveSecond, secondOK := frontmatter.EffectiveUsageWindow(sources[1])
	generated, generatedOK := frontmatter.Generated()
	verifications := frontmatter.Verifications()
	contract, contractOK := frontmatter.AttestedComputation()

	// Assert.
	if len(sources) != 2 || sources[0].UsageCount == nil || *sources[0].UsageCount != 5000 {
		t.Fatalf("Sources() = %#v", sources)
	}
	if !sharedOK || shared != (UsageWindow{From: "2026-06-01", To: "2026-06-30"}) {
		t.Fatalf("UsageWindow() = %#v, %v", shared, sharedOK)
	}
	if !firstOK || effectiveFirst != shared {
		t.Fatalf("effective first = %#v, %v", effectiveFirst, firstOK)
	}
	if !secondOK || effectiveSecond != (UsageWindow{From: "2026-05-01", To: "2026-05-31"}) {
		t.Fatalf("effective second = %#v, %v", effectiveSecond, secondOK)
	}
	if !generatedOK || generated.By != "agent/model" || generated.At != "2026-06-28T14:00:00Z" {
		t.Fatalf("Generated() = %#v, %v", generated, generatedOK)
	}
	if len(verifications) != 2 || frontmatter.TrustTier() != TrustHumanReviewed {
		t.Fatalf("Verifications/TrustTier = %#v/%q", verifications, frontmatter.TrustTier())
	}
	if status := frontmatter.EffectiveStatus(); status != StatusDeprecated {
		t.Fatalf("EffectiveStatus() = %q", status)
	}
	if !frontmatter.IsStale(time.Date(2026, 7, 29, 23, 0, 0, 0, time.FixedZone("local", 7*60*60))) {
		t.Fatal("IsStale(boundary) = false, want true")
	}
	if !contractOK || contract.Runtime != "bigquery" || contract.Executor == nil || len(contract.Executor.Receipt) != 2 || contract.Attester == nil {
		t.Fatalf("AttestedComputation() = %#v, %v", contract, contractOK)
	}
	if got, want := frontmatter.ExtensionKeys(), []string{"relations"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ExtensionKeys() = %#v, want %#v", got, want)
	}
}

func TestFrontmatterV02BareVerificationAndTrustTiers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		yaml string
		want TrustTier
	}{
		{name: "absent", yaml: "type: Note\n", want: TrustUnverified},
		{name: "malformed event", yaml: "type: Note\nverified: {}\n", want: TrustUnverified},
		{name: "machine bare", yaml: "type: Note\nverified: {by: process:nightly, at: 2026-07-29T00:00:00Z}\n", want: TrustMachineConfirmed},
		{name: "human bare", yaml: "type: Note\nverified: {by: human:sergey, at: 2026-07-29T00:00:00Z}\n", want: TrustHumanReviewed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			frontmatter, err := ParseFrontmatter(tt.yaml)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			got := frontmatter.TrustTier()

			// Assert.
			if got != tt.want {
				t.Fatalf("TrustTier() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFrontmatterV02LifecycleDefaults(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter, err := ParseFrontmatter("type: Note\n")
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	raw, rawOK := frontmatter.Status()
	effective := frontmatter.EffectiveStatus()
	state := frontmatter.StatusState()
	stale := frontmatter.IsStale(time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC))

	// Assert.
	if rawOK || raw != "" {
		t.Fatalf("Status() = %q, %v; want absent", raw, rawOK)
	}
	if effective != StatusStable {
		t.Fatalf("EffectiveStatus() = %q, want %q", effective, StatusStable)
	}
	if state.Present || !state.Valid || state.Effective != StatusStable {
		t.Fatalf("StatusState() = %#v", state)
	}
	if stale {
		t.Fatal("IsStale() = true without stale_after")
	}
}

func TestFrontmatterV02MalformedStatusIsNotStable(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter, err := ParseFrontmatter("status: 17\n")
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	state := frontmatter.StatusState()
	effective := frontmatter.EffectiveStatus()

	// Assert.
	if !state.Present || state.Valid || state.Raw != "17" || state.Effective != "" {
		t.Fatalf("StatusState() = %#v", state)
	}
	if effective != "" {
		t.Fatalf("EffectiveStatus() = %q, want unresolved", effective)
	}
}

func TestFrontmatterV02StaleAfterAccessorRejectsMalformedDates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		yaml string
		want string
		ok   bool
	}{
		{name: "absent", yaml: "type: Note\n"},
		{name: "malformed date", yaml: "stale_after: tomorrow\n"},
		{name: "non-string scalar", yaml: "stale_after: 17\n"},
		{name: "valid date", yaml: "stale_after: 2026-07-29\n", want: "2026-07-29", ok: true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			frontmatter, err := ParseFrontmatter(tt.yaml)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			got, ok := frontmatter.StaleAfter()

			// Assert.
			if got != tt.want || ok != tt.ok {
				t.Fatalf("StaleAfter() = %q, %v; want %q, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestEffectiveContentChangeTimeLegacyFallback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		yaml      string
		want      string
		wantRaw   string
		wantState TemporalValueState
	}{
		{
			name:      "legacy absent generated",
			yaml:      "timestamp: 2026-05-28T00:00:00Z\n",
			want:      "2026-05-28T00:00:00Z",
			wantRaw:   "2026-05-28T00:00:00Z",
			wantState: TemporalValid,
		},
		{
			name:      "generated valid actor and at",
			yaml:      "timestamp: 2026-05-28T00:00:00Z\ngenerated: {by: agent/v1, at: 2026-07-29T00:00:00Z}\n",
			want:      "2026-07-29T00:00:00Z",
			wantRaw:   "2026-07-29T00:00:00Z",
			wantState: TemporalValid,
		},
		{
			name:      "generated missing actor and valid at",
			yaml:      "timestamp: 2026-05-28T00:00:00Z\ngenerated: {at: \"2026-07-29T00:00:00Z\"}\n",
			want:      "2026-07-29T00:00:00Z",
			wantRaw:   "2026-07-29T00:00:00Z",
			wantState: TemporalValid,
		},
		{
			name:      "generated malformed actor tag and valid at",
			yaml:      "timestamp: 2026-05-28T00:00:00Z\ngenerated: {by: 17, at: 2026-07-29T00:00:00Z}\n",
			want:      "2026-07-29T00:00:00Z",
			wantRaw:   "2026-07-29T00:00:00Z",
			wantState: TemporalValid,
		},
		{
			name:      "generated malformed actor value and valid at",
			yaml:      "timestamp: 2026-05-28T00:00:00Z\ngenerated: {by: 'human:', at: 2026-07-29T00:00:00Z}\n",
			want:      "2026-07-29T00:00:00Z",
			wantRaw:   "2026-07-29T00:00:00Z",
			wantState: TemporalValid,
		},
		{
			name:      "generated valid actor and malformed at",
			yaml:      "timestamp: 2026-05-28T00:00:00Z\ngenerated: {by: agent/v1, at: not-a-time}\n",
			wantRaw:   "not-a-time",
			wantState: TemporalMalformed,
		},
		{
			name:      "generated missing actor and malformed at",
			yaml:      "timestamp: 2026-05-28T00:00:00Z\ngenerated: {at: not-a-time}\n",
			wantRaw:   "not-a-time",
			wantState: TemporalMalformed,
		},
		{
			name:      "generated malformed actor and malformed at tag",
			yaml:      "timestamp: 2026-05-28T00:00:00Z\ngenerated: {by: 17, at: false}\n",
			wantRaw:   "false",
			wantState: TemporalMalformed,
		},
		{
			name:      "generated valid actor without at blocks fallback",
			yaml:      "timestamp: 2026-05-28T00:00:00Z\ngenerated: {by: agent/model}\n",
			wantState: TemporalAbsent,
		},
		{
			name:      "generated malformed actor without at blocks fallback",
			yaml:      "timestamp: 2026-05-28T00:00:00Z\ngenerated: {by: 17}\n",
			wantState: TemporalAbsent,
		},
		{
			name:      "malformed generated blocks fallback",
			yaml:      "timestamp: 2026-05-28T00:00:00Z\ngenerated: wrong\n",
			wantRaw:   "wrong",
			wantState: TemporalMalformed,
		},
		{
			name: "explicit generated without at overrides merged generated and blocks fallback",
			yaml: "defaults: &defaults {generated: {at: 2026-07-29T00:00:00Z}}\n" +
				"<<: *defaults\n" +
				"generated: {by: 17}\n" +
				"timestamp: 2026-05-28T00:00:00Z\n",
			wantState: TemporalAbsent,
		},
		{
			name:      "malformed legacy timestamp without generated",
			yaml:      "timestamp: not-a-time\n",
			wantRaw:   "not-a-time",
			wantState: TemporalMalformed,
		},
		{
			name:      "both families absent",
			yaml:      "type: Note\n",
			wantState: TemporalAbsent,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			frontmatter, err := ParseFrontmatter(tt.yaml)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			got, ok := frontmatter.EffectiveContentChangeTime()
			value := frontmatter.EffectiveContentChangeTimeValue()
			generatedAt := frontmatter.GeneratedAtValue()
			observation := frontmatter.LegacyFallbackObservation()

			// Assert.
			wantOK := tt.wantState == TemporalValid
			if got != tt.want || ok != wantOK {
				t.Fatalf("EffectiveContentChangeTime() = %q, %v; want %q, %v", got, ok, tt.want, wantOK)
			}
			if value.State != tt.wantState || value.Raw != tt.wantRaw {
				t.Fatalf("EffectiveContentChangeTimeValue() = %#v; want raw=%q state=%q", value, tt.wantRaw, tt.wantState)
			}
			if observation.GeneratedPresent &&
				(generatedAt.State != tt.wantState || generatedAt.Raw != tt.wantRaw) {
				t.Fatalf("GeneratedAtValue() = %#v; want raw=%q state=%q", generatedAt, tt.wantRaw, tt.wantState)
			}
		})
	}
}

func TestEffectiveContentChangeTimeUsesSemanticGeneratedAt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "terminal aliases",
			yaml: "generated_value: &generated {by: 17, at: 2026-07-29T00:00:00Z}\n" +
				"generated: *generated\n" +
				"timestamp: 2026-05-28T00:00:00Z\n",
			want: "2026-07-29T00:00:00Z",
		},
		{
			name: "merged generated family",
			yaml: "defaults: &defaults\n" +
				"  generated: {by: 17, at: 2026-07-29T00:00:00Z}\n" +
				"<<: *defaults\n" +
				"timestamp: 2026-05-28T00:00:00Z\n",
			want: "2026-07-29T00:00:00Z",
		},
		{
			name: "merged at field",
			yaml: "generation_defaults: &generation_defaults {at: 2026-07-29T00:00:00Z}\n" +
				"generated:\n" +
				"  <<: *generation_defaults\n" +
				"  by: 17\n" +
				"timestamp: 2026-05-28T00:00:00Z\n",
			want: "2026-07-29T00:00:00Z",
		},
		{
			name: "aliased at field",
			yaml: "generated_at: &generated_at 2026-07-29T00:00:00Z\n" +
				"generated: {by: 17, at: *generated_at}\n" +
				"timestamp: 2026-05-28T00:00:00Z\n",
			want: "2026-07-29T00:00:00Z",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			frontmatter, err := ParseFrontmatter(tt.yaml)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			got, ok := frontmatter.EffectiveContentChangeTime()
			value := frontmatter.EffectiveContentChangeTimeValue()

			// Assert.
			if !ok || got != tt.want {
				t.Fatalf("EffectiveContentChangeTime() = %q, %v; want %q, true", got, ok, tt.want)
			}
			if value.State != TemporalValid || value.Raw != tt.want {
				t.Fatalf("EffectiveContentChangeTimeValue() = %#v; want raw=%q valid", value, tt.want)
			}
		})
	}
}

func TestEffectiveContentChangeTimeConcurrentReads(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter, err := ParseFrontmatter(
		"timestamp: 2026-05-28T00:00:00Z\n" +
			"generated: {by: 17, at: 2026-07-29T00:00:00Z}\n",
	)
	if err != nil {
		t.Fatal(err)
	}
	const readers = 32
	start := make(chan struct{})
	failures := make(chan string, readers)
	var wait sync.WaitGroup

	// Act.
	for range readers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			got, ok := frontmatter.EffectiveContentChangeTime()
			value := frontmatter.EffectiveContentChangeTimeValue()
			if !ok || got != "2026-07-29T00:00:00Z" ||
				value.State != TemporalValid || value.Raw != got {
				failures <- fmt.Sprintf("string=%q/%v value=%#v", got, ok, value)
			}
		}()
	}
	close(start)
	wait.Wait()
	close(failures)

	// Assert.
	for failure := range failures {
		t.Error(failure)
	}
}

func TestLegacyFallbackObservationUsesSemanticFamilyPresence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		yaml string
		want LegacyFallbackObservation
	}{
		{
			name: "absent",
			yaml: "type: Note\n",
			want: LegacyFallbackObservation{TimestampAllowed: true, CitationsAllowed: true},
		},
		{
			name: "terminal aliases",
			yaml: "generated_value: &generated {by: process:build}\n" +
				"sources_value: &sources []\n" +
				"generated: *generated\n" +
				"sources: *sources\n",
			want: LegacyFallbackObservation{GeneratedPresent: true, SourcesPresent: true},
		},
		{
			name: "merged families",
			yaml: "defaults: &defaults\n" +
				"  generated: wrong\n" +
				"  sources: wrong\n" +
				"<<: *defaults\n",
			want: LegacyFallbackObservation{GeneratedPresent: true, SourcesPresent: true},
		},
		{
			name: "explicit presence overrides donor",
			yaml: "defaults: &defaults\n" +
				"  generated: {by: process:old}\n" +
				"  sources: [{resource: old}]\n" +
				"<<: *defaults\n" +
				"generated: []\n" +
				"sources: []\n",
			want: LegacyFallbackObservation{GeneratedPresent: true, SourcesPresent: true},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			frontmatter, err := ParseFrontmatter(test.yaml)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			got := frontmatter.LegacyFallbackObservation()

			// Assert.
			if got != test.want {
				t.Fatalf("LegacyFallbackObservation() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestTypedAccessorsReturnDefensiveCopies(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter, err := ParseFrontmatter("type: Attested Computation\nsources: [{id: a, resource: x, usage_count: 1, usage_window: {from: 2026-01-01, to: 2026-01-31}}]\nexecutor: {resource: runner.go, receipt: [one]}\n")
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	sources := frontmatter.Sources()
	contract, _ := frontmatter.AttestedComputation()
	*sources[0].UsageCount = 99
	sources[0].UsageWindow.From = "mutated"
	contract.Executor.Receipt[0] = "mutated"
	reloadedSources := frontmatter.Sources()
	reloadedContract, _ := frontmatter.AttestedComputation()

	// Assert.
	if *reloadedSources[0].UsageCount != 1 || reloadedSources[0].UsageWindow.From != "2026-01-01" {
		t.Fatalf("Sources() shared caller mutations: %#v", reloadedSources)
	}
	if reloadedContract.Executor.Receipt[0] != "one" {
		t.Fatalf("AttestedComputation() shared caller mutations: %#v", reloadedContract)
	}
}

func TestFrontmatterBYOTEncodeDecode(t *testing.T) {
	t.Parallel()

	type callerModel struct {
		Type   string         `yaml:"type"`
		Custom map[string]any `yaml:"custom"`
	}

	// Arrange.
	input := callerModel{Type: "Note", Custom: map[string]any{"enabled": true}}

	// Act.
	frontmatter, err := EncodeFrontmatter(input)
	if err != nil {
		t.Fatal(err)
	}
	output, err := DecodeFrontmatter[callerModel](frontmatter)

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if output.Type != input.Type || !reflect.DeepEqual(output.Custom, input.Custom) {
		t.Fatalf("DecodeFrontmatter() = %#v, want %#v", output, input)
	}
}

func TestSourcesAcceptStrictYAMLUint64Lexemes(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter, err := ParseFrontmatter("sources:\n  - {resource: a, usage_count: +1}\n  - {resource: b, usage_count: 0x10}\n  - {resource: c, usage_count: -0}\n  - {resource: d, usage_count: 18446744073709551615}\n  - {resource: e, usage_count: 18446744073709551616}\n")
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	sources := frontmatter.Sources()

	// Assert.
	want := []uint64{1, 16, 0, ^uint64(0)}
	if len(sources) != len(want) {
		t.Fatalf("Sources() len = %d, want %d valid sources: %#v", len(sources), len(want), sources)
	}
	for index, value := range want {
		if sources[index].UsageCount == nil || *sources[index].UsageCount != value {
			t.Fatalf("sources[%d].UsageCount = %#v, want %d", index, sources[index].UsageCount, value)
		}
	}
	states := frontmatter.SourceStates()
	if len(states) != 5 || states[4].Valid || states[4].UsageCount.Valid {
		t.Fatalf("overflow source state = %#v, want invalid raw-only", states)
	}
}

func TestTypedV02ModelsDoNotCoerceMalformedYAMLTags(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter, err := ParseFrontmatter(`
sources:
  - {id: 17, resource: true, author: 9, usage_count: "10"}
generated: {by: 17, at: 2026-07-29T00:00:00Z}
verified:
  - {by: "human:", at: 2026-07-29T00:00:00Z}
  - {by: process:nightly, at: not-a-time}
runtime: 17
parameters:
  - {name: 17, type: true, required: "true"}
executor:
  resource: 17
  receipt: [valid_name, 9, ""]
attester: {resource: false}
`)
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	sources := frontmatter.Sources()
	generated, generatedOK := frontmatter.Generated()
	verificationStates := frontmatter.VerificationStates()
	verifications := frontmatter.Verifications()
	contract, contractOK := frontmatter.AttestedComputation()
	contractState := frontmatter.ComputationContractState()

	// Assert.
	if len(sources) != 0 {
		t.Fatalf("Sources() coerced malformed tags: %#v", sources)
	}
	if generatedOK || generated != (Generation{}) {
		t.Fatalf("Generated() = %#v, %v; want raw-only invalid", generated, generatedOK)
	}
	if len(verificationStates) != 2 || verificationStates[0].Valid || verificationStates[1].Valid || len(verifications) != 0 || frontmatter.TrustTier() != TrustUnverified {
		t.Fatalf("verification states = %#v, typed=%#v, trust=%q", verificationStates, verifications, frontmatter.TrustTier())
	}
	if contractOK || !reflect.DeepEqual(contract, AttestedComputationContract{}) {
		t.Fatalf("AttestedComputation() coerced malformed tags: %#v", contract)
	}
	if contractState.Valid || len(contractState.Parameters) != 1 ||
		!contractState.Parameters[0].Required.Present || contractState.Parameters[0].Required.Valid {
		t.Fatalf("ComputationContractState() = %#v", contractState)
	}
}

func TestGenerationStatePreservesIndependentTypedFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		yaml      string
		wantBy    string
		wantAt    string
		wantValid bool
	}{
		{
			name:   "valid timestamp without required actor",
			yaml:   "generated: {at: 2026-07-29T00:00:00Z}\n",
			wantAt: "2026-07-29T00:00:00Z",
		},
		{
			name:   "valid timestamp with malformed actor",
			yaml:   "generated: {by: 17, at: 2026-07-29T00:00:00Z}\n",
			wantAt: "2026-07-29T00:00:00Z",
		},
		{
			name:   "valid actor with malformed timestamp",
			yaml:   "generated: {by: process:new, at: yesterday}\n",
			wantBy: "process:new",
		},
		{
			name:      "complete contract",
			yaml:      "generated: {by: process:new, at: 2026-07-29T00:00:00Z}\n",
			wantBy:    "process:new",
			wantAt:    "2026-07-29T00:00:00Z",
			wantValid: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			frontmatter, err := ParseFrontmatter(tt.yaml)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			state := frontmatter.GenerationState()

			// Assert.
			if !state.Present || !state.Mapping || state.Valid != tt.wantValid {
				t.Fatalf("GenerationState() = %#v, want present mapping valid=%v", state, tt.wantValid)
			}
			if state.By.Value != tt.wantBy || state.By.Valid != (tt.wantBy != "") {
				t.Fatalf("GenerationState().By = %#v, want %q", state.By, tt.wantBy)
			}
			if (state.At.State == TemporalValid) != (tt.wantAt != "") {
				t.Fatalf("GenerationState().At = %#v, want valid=%v", state.At, tt.wantAt != "")
			}
			if tt.wantAt != "" && state.At.Raw != tt.wantAt {
				t.Fatalf("GenerationState().At = %#v, want %q", state.At, tt.wantAt)
			}
			if state.HasValue != (tt.wantBy != "" || tt.wantAt != "") {
				t.Fatalf("GenerationState().HasValue = %v", state.HasValue)
			}
		})
	}
}

func TestCoreScalarAndTagStatesDoNotDisplayCoerceYAMLTags(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter, err := ParseFrontmatter(`
title: 17
description: true
resource: 1.5
timestamp: false
tags: [valid, 7, false, ""]
`)
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	title, titleOK := frontmatter.Title()
	description, descriptionOK := frontmatter.Description()
	resource, resourceOK := frontmatter.Resource()
	timestamp, timestampOK := frontmatter.Timestamp()
	tags := frontmatter.Tags()
	tagState := frontmatter.TagsState()

	// Assert.
	if titleOK || descriptionOK || resourceOK || timestampOK ||
		title != "" || description != "" || resource != "" || timestamp != "" {
		t.Fatalf("coerced core scalars: %q/%v %q/%v %q/%v %q/%v",
			title, titleOK, description, descriptionOK, resource, resourceOK, timestamp, timestampOK)
	}
	if got, want := tags, []string{"valid"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Tags() = %#v, want %#v", got, want)
	}
	if !tagState.Present || tagState.Valid || len(tagState.Items) != 4 ||
		tagState.Items[1].Raw != "7" || tagState.Items[1].Valid {
		t.Fatalf("TagsState() = %#v", tagState)
	}
}

func TestTemporalValuesPreserveMalformedPresence(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter, err := ParseFrontmatter("generated: {by: agent/v1, at: not-a-time}\nstale_after: 29-07-2026\ntimestamp: 2026-07-29T00:00:00Z\n")
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	generated := frontmatter.GeneratedAtValue()
	effective := frontmatter.EffectiveContentChangeTimeValue()
	stale := frontmatter.StaleAfterValue()

	// Assert.
	if generated.State != TemporalMalformed || generated.Raw != "not-a-time" {
		t.Fatalf("GeneratedAtValue() = %#v", generated)
	}
	if effective.State != TemporalMalformed || effective.Raw != "not-a-time" {
		t.Fatalf("EffectiveContentChangeTimeValue() = %#v; legacy fallback must be blocked", effective)
	}
	if stale.State != TemporalMalformed || stale.Raw != "29-07-2026" {
		t.Fatalf("StaleAfterValue() = %#v", stale)
	}
}
