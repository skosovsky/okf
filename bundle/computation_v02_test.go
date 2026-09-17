package bundle

import (
	"reflect"
	"testing"
)

func TestFrontmatterAttestedComputationRequiresExactTypeWhileStateRemainsUngated(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		yaml      string
		wantTyped bool
		wantValid bool
	}{
		{
			name:      "exact type",
			yaml:      "type: Attested Computation\nexecutor: {resource: runner.md}\n",
			wantTyped: true,
			wantValid: true,
		},
		{
			name:      "missing type retains raw state",
			yaml:      "executor: {resource: runner.md}\n",
			wantValid: true,
		},
		{
			name:      "wrong type retains raw state",
			yaml:      "type: Note\nexecutor: {resource: runner.md}\n",
			wantValid: true,
		},
		{
			name: "malformed exact type contract",
			yaml: "type: Attested Computation\nexecutor: {resource: 17}\n",
		},
		{
			name:      "wrong tagged type",
			yaml:      "type: !!int 17\nexecutor: {resource: runner.md}\n",
			wantValid: true,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			frontmatter, err := ParseFrontmatter(test.yaml)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			contract, ok := frontmatter.AttestedComputation()
			state := frontmatter.ComputationContractState()

			// Assert.
			if ok != test.wantTyped {
				t.Fatalf("AttestedComputation() = (%#v, %v), want typed=%v", contract, ok, test.wantTyped)
			}
			if !state.Present || state.Valid != test.wantValid {
				t.Fatalf("ComputationContractState() = %#v, want present valid=%v", state, test.wantValid)
			}
			if !test.wantTyped && !reflect.DeepEqual(contract, AttestedComputationContract{}) {
				t.Fatalf("ungated typed contract leaked: %#v", contract)
			}
		})
	}
}

func TestAttestedComputationStateIsExactTypeAndPayloadOwned(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		source    string
		mode      ComputationMode
		typeMatch bool
		valid     bool
		inline    string
		unclosed  bool
		multiple  bool
		conflict  bool
	}{
		{name: "wrong type", source: "---\ntype: Metric\ncomputation: query.sql\n---\n", mode: ComputationModeAbsent},
		{name: "file", source: "---\ntype: Attested Computation\ncomputation: query with spaces.sql\n---\n", mode: ComputationModeFile, typeMatch: true, valid: true},
		{name: "inline", source: "---\ntype: Attested Computation\nruntime: bigquery\n---\n# Computation\n```sql\nSELECT 1;\n```\n", mode: ComputationModeInline, typeMatch: true, valid: true, inline: "SELECT 1;\n"},
		{name: "both", source: "---\ntype: Attested Computation\ncomputation: query.sql\n---\n# Computation\n```\nSELECT 1;\n```\n", mode: ComputationModeAmbiguous, typeMatch: true, conflict: true},
		{name: "malformed file", source: "---\ntype: Attested Computation\ncomputation: 17\n---\n", mode: ComputationModeMalformed, typeMatch: true},
		{name: "missing payload", source: "---\ntype: Attested Computation\nruntime: dbt\n---\n", mode: ComputationModeAbsent, typeMatch: true},
		{name: "unclosed inline", source: "---\ntype: Attested Computation\n---\n# Computation\n```sql\nSELECT 1;\n", mode: ComputationModeMalformed, typeMatch: true, unclosed: true},
		{name: "unclosed remains malformed beside file", source: "---\ntype: Attested Computation\ncomputation: query.sql\n---\n# Computation\n```sql\nSELECT 1;\n", mode: ComputationModeMalformed, typeMatch: true, unclosed: true},
		{name: "multiple fences", source: "---\ntype: Attested Computation\n---\n# Computation\n```\none\n```\n```\ntwo\n```\n", mode: ComputationModeAmbiguous, typeMatch: true, multiple: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			document, err := ParseDocument(tt.source)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			state := document.AttestedComputationState()
			mode := document.ComputationMode()

			// Assert.
			if state.TypeMatches != tt.typeMatch || state.Valid != tt.valid || state.Mode != tt.mode ||
				mode != tt.mode || state.Inline != tt.inline || state.Unclosed != tt.unclosed ||
				state.Multiple != tt.multiple || state.Conflict != tt.conflict {
				t.Fatalf("AttestedComputationState() = %#v, mode=%q", state, mode)
			}
			if (tt.unclosed || tt.multiple || tt.conflict) && len(state.FenceSpans) == 0 {
				t.Fatalf("AttestedComputationState() lost fence spans: %#v", state)
			}
			if document.IsAttestedComputation() != tt.typeMatch {
				t.Fatalf("IsAttestedComputation() = %v", document.IsAttestedComputation())
			}
		})
	}
}

func TestDocumentAttestedComputationUsesExactTypeAndPayloadState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "inline only exact type",
			body: "---\ntype: Attested Computation\n---\n# Computation\n```\nSELECT 1;\n```\n",
		},
		{
			name: "file exact type",
			body: "---\ntype: Attested Computation\ncomputation: query.sql\n---\n",
			want: true,
		},
		{
			name: "valid aliased executor contract",
			body: "---\ntype: Attested Computation\ncomputation: query.sql\n" +
				"executor_value: &executor_value {resource: run.md, receipt: [result]}\n" +
				"executor: *executor_value\n---\n",
			want: true,
		},
		{
			name: "duplicate direct executor is ambiguous",
			body: "---\ntype: Attested Computation\ncomputation: query.sql\n" +
				"executor: {resource: one.md, receipt: [one]}\n" +
				"executor: {resource: two.md, receipt: [two]}\n---\n",
		},
		{
			name: "duplicate merge executor is ambiguous",
			body: "---\ntype: Attested Computation\ncomputation: query.sql\n" +
				"left: &left {executor: {resource: one.md, receipt: [one]}}\n" +
				"right: &right {executor: {resource: two.md, receipt: [two]}}\n" +
				"<<: *left\n<<: *right\n---\n",
		},
		{
			name: "explicit executor overrides duplicate merges",
			body: "---\ntype: Attested Computation\ncomputation: query.sql\n" +
				"left: &left {executor: {resource: one.md, receipt: [one]}}\n" +
				"right: &right {executor: {resource: two.md, receipt: [two]}}\n" +
				"<<: *left\n<<: *right\n" +
				"executor: {resource: explicit.md, receipt: [result]}\n---\n",
			want: true,
		},
		{
			name: "wrong type with computation fields",
			body: "---\ntype: Note\nruntime: sql\ncomputation: query.sql\n---\n",
		},
		{
			name: "exact type missing payload",
			body: "---\ntype: Attested Computation\nruntime: sql\n---\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			document, err := ParseDocument(test.body)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			_, ok := document.AttestedComputation()

			// Assert.
			if ok != test.want {
				t.Fatalf("AttestedComputation() ok = %v, want %v; state=%#v", ok, test.want, document.AttestedComputationState())
			}
		})
	}
}

func TestAttestedComputationPathAndInlineObservationMatrix(t *testing.T) {
	t.Parallel()

	pathCases := []struct {
		name  string
		field string
		valid bool
	}{
		{name: "absent"},
		{name: "blank", field: "computation: \"\"\n"},
		{name: "non-string", field: "computation: 17\n"},
		{name: "valid", field: "computation: query.sql\n", valid: true},
	}
	inlineCases := []struct {
		name      string
		body      string
		valid     bool
		malformed bool
	}{
		{name: "none"},
		{name: "valid", body: "# Computation\n```\nSELECT 1;\n```\n", valid: true},
		{name: "malformed", body: "# Computation\n```\nSELECT 1;\n", malformed: true},
	}
	for _, pathCase := range pathCases {
		pathCase := pathCase
		for _, inlineCase := range inlineCases {
			inlineCase := inlineCase
			t.Run(pathCase.name+"/"+inlineCase.name, func(t *testing.T) {
				t.Parallel()

				// Arrange.
				document, err := ParseDocument(
					"---\ntype: Attested Computation\n" + pathCase.field + "---\n" + inlineCase.body,
				)
				if err != nil {
					t.Fatal(err)
				}

				// Act.
				state := document.AttestedComputationState()

				// Assert.
				wantMode := ComputationModeAbsent
				wantValid := false
				wantConflict := false
				switch {
				case inlineCase.malformed:
					wantMode = ComputationModeMalformed
				case pathCase.valid && inlineCase.valid:
					wantMode, wantConflict = ComputationModeAmbiguous, true
				case pathCase.valid:
					wantMode, wantValid = ComputationModeFile, true
				case inlineCase.valid:
					wantMode, wantValid = ComputationModeInline, true
				case pathCase.field != "":
					wantMode = ComputationModeMalformed
				}
				if state.Mode != wantMode || state.Valid != wantValid || state.Conflict != wantConflict {
					t.Fatalf("AttestedComputationState() = %#v, want mode=%q valid=%v conflict=%v", state, wantMode, wantValid, wantConflict)
				}
				if pathCase.field != "" && !state.ContractState.Computation.Present {
					t.Fatalf("malformed path presence was lost: %#v", state.ContractState.Computation)
				}
			})
		}
	}
}
