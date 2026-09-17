package bundle

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestStandardScalarContextAccessorsMatchLegacySemantics(t *testing.T) {
	t.Parallel()

	families := []struct {
		field string
		valid string
		other string
	}{
		{field: "type", valid: "Note", other: "Metric"},
		{field: "title", valid: "Title", other: "Other"},
		{field: "description", valid: "Description", other: "Other description"},
		{field: "resource", valid: "scope/resource", other: "scope/other"},
		{field: "timestamp", valid: "2026-08-07T01:02:03Z", other: "2026-08-07T02:03:04Z"},
	}
	scenarios := []struct {
		name        string
		yaml        func(field, valid, other string) string
		wantValid   bool
		wantPresent bool
	}{
		{name: "absent", yaml: func(_, _, _ string) string { return "extension: value\n" }},
		{name: "direct", yaml: func(field, valid, _ string) string { return field + ": " + valid + "\n" }, wantValid: true, wantPresent: true},
		{name: "terminal-alias", yaml: func(field, valid, _ string) string { return "anchor: &value " + valid + "\n" + field + ": *value\n" }, wantValid: true, wantPresent: true},
		{name: "merge", yaml: func(field, valid, _ string) string { return "base: &base {" + field + ": " + valid + "}\n<<: *base\n" }, wantValid: true, wantPresent: true},
		{name: "malformed-tag", yaml: func(field, _, _ string) string { return field + ": 3\n" }, wantPresent: true},
		{name: "blank", yaml: func(field, _, _ string) string { return field + ": '   '\n" }, wantPresent: true},
		{name: "duplicate", yaml: func(field, valid, other string) string {
			return field + ": " + valid + "\n" + field + ": " + other + "\n"
		}, wantPresent: true},
	}

	for _, family := range families {
		family := family
		for _, scenario := range scenarios {
			scenario := scenario
			t.Run(family.field+"/"+scenario.name, func(t *testing.T) {
				t.Parallel()

				// Arrange.
				frontmatter, err := ParseFrontmatter(scenario.yaml(family.field, family.valid, family.other))
				if err != nil {
					t.Fatal(err)
				}
				wantValue, wantValid := legacyScalarFamily(frontmatter, family.field)

				// Act.
				value, valid, contextErr := contextScalarFamily(context.Background(), frontmatter, family.field)

				// Assert.
				if contextErr != nil {
					t.Fatal(contextErr)
				}
				if value != wantValue || valid != wantValid || valid != scenario.wantValid {
					t.Fatalf("%s context = (%q, %v), legacy=(%q, %v)", family.field, value, valid, wantValue, wantValid)
				}
				if family.field != "type" {
					state, stateErr := contextScalarStateFamily(context.Background(), frontmatter, family.field)
					legacyState := legacyScalarStateFamily(frontmatter, family.field)
					if stateErr != nil || !reflect.DeepEqual(state, legacyState) || state.Present != scenario.wantPresent {
						t.Fatalf("%s state = (%#v, %v), legacy=%#v", family.field, state, stateErr, legacyState)
					}
				}
			})
		}
	}
}

func TestStandardScalarContextAccessorsCancelArbitraryLengthValue(t *testing.T) {
	t.Parallel()

	// Arrange.
	value := strings.Repeat("x", 1<<20)
	frontmatter, err := NewFrontmatterFromNode(&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "title"},
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: value},
	}})
	if err != nil {
		t.Fatal(err)
	}
	stateProbe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if got, probeErr := frontmatter.TitleStateContext(stateProbe); probeErr != nil || !got.Valid || got.Value != value {
		t.Fatalf("TitleStateContext() probe = (%#v, %v)", got, probeErr)
	}
	valueProbe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if got, valid, probeErr := frontmatter.TitleContext(valueProbe); probeErr != nil || !valid || got != value {
		t.Fatalf("TitleContext() probe = (%d bytes, %v, %v)", len(got), valid, probeErr)
	}

	// Act.
	state, stateErr := frontmatter.TitleStateContext(&cancelAfterErrChecksContext{allowed: stateProbe.checks.Load() / 2})
	got, valid, valueErr := frontmatter.TitleContext(&cancelAfterErrChecksContext{allowed: valueProbe.checks.Load() / 2})

	// Assert.
	if !reflect.DeepEqual(state, ScalarValueState{}) || !errors.Is(stateErr, context.Canceled) {
		t.Fatalf("mid-value state = (%#v, %v)", state, stateErr)
	}
	if got != "" || valid || !errors.Is(valueErr, context.Canceled) {
		t.Fatalf("mid-value title = (%d bytes, %v, %v)", len(got), valid, valueErr)
	}
}

func TestStandardScalarContextAccessorsCancelLargeRootWithoutPartialValues(t *testing.T) {
	t.Parallel()

	// Arrange.
	content := make([]*yaml.Node, 0, 24_002)
	for index := 0; index < 12_000; index++ {
		content = append(content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "extension-" + zeroPaddedDecimal(index, 5)},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "value"},
		)
	}
	content = append(content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "type"},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "Note"},
	)
	frontmatter, err := NewFrontmatterFromNode(&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: content})
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	if got, valid, probeErr := frontmatter.TypeContext(probe); probeErr != nil || !valid || got != "Note" {
		t.Fatalf("TypeContext() probe = (%q, %v, %v)", got, valid, probeErr)
	}
	totalChecks := probe.checks.Load()

	// Act.
	pre, preValid, preErr := frontmatter.TypeContext(canceled)
	mid, midValid, midErr := frontmatter.TypeContext(&cancelAfterErrChecksContext{allowed: totalChecks / 2})
	nearComplete, nearValid, nearCompleteErr := frontmatter.TypeContext(&cancelAfterErrChecksContext{allowed: totalChecks - 1})

	// Assert.
	for _, result := range []struct {
		name  string
		value string
		valid bool
		err   error
	}{
		{name: "pre", value: pre, valid: preValid, err: preErr},
		{name: "mid", value: mid, valid: midValid, err: midErr},
		{name: "near-complete", value: nearComplete, valid: nearValid, err: nearCompleteErr},
	} {
		if result.value != "" || result.valid || !errors.Is(result.err, context.Canceled) {
			t.Errorf("%s = (%q, %v, %v)", result.name, result.value, result.valid, result.err)
		}
	}
}

func TestStandardScalarContextResultsRemainStableAcrossCallerCopies(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter, err := ParseFrontmatter("type: Note\ntitle: Original title\ndescription: Original description\nresource: scope/resource\ntimestamp: 2026-08-07T01:02:03Z\n")
	if err != nil {
		t.Fatal(err)
	}
	first, valid, err := frontmatter.TitleContext(context.Background())
	if err != nil || !valid {
		t.Fatalf("TitleContext() = (%q, %v, %v)", first, valid, err)
	}
	mutable := []byte(first)

	// Act.
	mutable[0] = 'X'
	again, againValid, againErr := frontmatter.TitleContext(context.Background())

	// Assert.
	if againErr != nil || !againValid || again != "Original title" || string(mutable) == again {
		t.Fatalf("caller copy affected result: mutable=%q again=(%q, %v, %v)", mutable, again, againValid, againErr)
	}
}

func legacyScalarFamily(frontmatter Frontmatter, field string) (string, bool) {
	switch field {
	case "type":
		return frontmatter.Type()
	case "title":
		return frontmatter.Title()
	case "description":
		return frontmatter.Description()
	case "resource":
		return frontmatter.Resource()
	case "timestamp":
		return frontmatter.Timestamp()
	default:
		return "", false
	}
}

func contextScalarFamily(ctx context.Context, frontmatter Frontmatter, field string) (string, bool, error) {
	switch field {
	case "type":
		return frontmatter.TypeContext(ctx)
	case "title":
		return frontmatter.TitleContext(ctx)
	case "description":
		return frontmatter.DescriptionContext(ctx)
	case "resource":
		return frontmatter.ResourceContext(ctx)
	case "timestamp":
		return frontmatter.TimestampContext(ctx)
	default:
		return "", false, nil
	}
}

func legacyScalarStateFamily(frontmatter Frontmatter, field string) ScalarValueState {
	switch field {
	case "title":
		return frontmatter.TitleState()
	case "description":
		return frontmatter.DescriptionState()
	case "resource":
		return frontmatter.ResourceState()
	case "timestamp":
		return frontmatter.TimestampState()
	default:
		return ScalarValueState{}
	}
}

func contextScalarStateFamily(ctx context.Context, frontmatter Frontmatter, field string) (ScalarValueState, error) {
	switch field {
	case "title":
		return frontmatter.TitleStateContext(ctx)
	case "description":
		return frontmatter.DescriptionStateContext(ctx)
	case "resource":
		return frontmatter.ResourceStateContext(ctx)
	case "timestamp":
		return frontmatter.TimestampStateContext(ctx)
	default:
		return ScalarValueState{}, nil
	}
}
