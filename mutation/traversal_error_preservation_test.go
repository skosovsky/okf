package mutation

import (
	"context"
	"errors"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"gopkg.in/yaml.v3"
)

func TestPreserveTraversalErrorCanonicalClasses(t *testing.T) {
	// Arrange.
	p, err := parsePresentationContext(context.Background(), []byte("---\ntype: Knowledge\nconfig: {}\n---\n"))
	if err != nil {
		t.Fatal(err)
	}
	owner := mappingValuesForTest(t, p.root, "config")[0]
	wantLocation := p.yamlNodesSpan(owner)
	existing := &PresentationError{
		Code: "existing", Format: "yaml", Location: SourceSpan{Start: 1, End: 2}, Err: ErrAmbiguousPresentation,
	}
	resource := &bundle.YAMLResourceLimitError{
		Kind: bundle.YAMLResourceGraphNodes, Limit: bundle.MaxYAMLGraphNodes, Observed: bundle.MaxYAMLGraphNodes + 1,
	}
	integrity := &bundle.YAMLIntegrityError{Code: bundle.YAMLIntegrityContentCycle}
	tests := []struct {
		name          string
		input         error
		fallbackCode  string
		fallbackCause error
		wantIdentity  error
		wantCode      string
		wantResource  bool
		wantIntegrity bool
	}{
		{name: "canceled", input: context.Canceled, wantIdentity: context.Canceled},
		{name: "deadline", input: context.DeadlineExceeded, wantIdentity: context.DeadlineExceeded},
		{name: "located presentation", input: existing, wantIdentity: existing},
		{name: "resource", input: resource, fallbackCode: "invalid_render_value", fallbackCause: ErrUnsupportedPresentation, wantCode: yamlCodeResourceLimit, wantResource: true},
		{name: "integrity", input: integrity, fallbackCode: yamlCodeUnsupportedCollection, fallbackCause: ErrUnsupportedPresentation, wantCode: yamlCodeGraphIntegrity, wantIntegrity: true},
		{name: "render fallback", input: errors.New("invalid render"), fallbackCode: "invalid_render_value", fallbackCause: ErrUnsupportedPresentation, wantCode: "invalid_render_value"},
		{name: "selector fallback", input: errors.New("selector mismatch"), fallbackCode: yamlCodeSelectorMismatch, fallbackCause: ErrUnsupportedPresentation, wantCode: yamlCodeSelectorMismatch},
		{name: "merge fallback", input: errYAMLDesiredConflict, fallbackCode: yamlCodeOverlappingPatch, fallbackCause: ErrUnsupportedPresentation, wantCode: yamlCodeOverlappingPatch},
		{name: "lineage duplicate", input: errYAMLDuplicateSequenceIdentity, fallbackCode: yamlCodeAmbiguousSelector, fallbackCause: ErrAmbiguousPresentation, wantCode: yamlCodeAmbiguousSelector},
		{name: "semantic shadow", input: errors.New("semantic path"), fallbackCode: "semantic_edit_path", fallbackCause: ErrUnsupportedPresentation, wantCode: "semantic_edit_path"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Act.
			got := p.preserveTraversalError(test.input, test.fallbackCode, test.fallbackCause, owner)

			// Assert.
			if test.wantIdentity != nil {
				if got != test.wantIdentity {
					t.Fatalf("error identity = %#v, want %#v", got, test.wantIdentity)
				}
				return
			}
			var presentation *PresentationError
			if !errors.As(got, &presentation) || presentation.Code != test.wantCode || presentation.Location != wantLocation || !errors.Is(got, test.fallbackCause) {
				t.Fatalf("classified error = %#v / %#v, want code=%q location=%v", got, presentation, test.wantCode, wantLocation)
			}
			var gotResource *bundle.YAMLResourceLimitError
			if errors.As(got, &gotResource) != test.wantResource {
				t.Fatalf("resource discoverability = %t, want %t", gotResource != nil, test.wantResource)
			}
			var gotIntegrity *bundle.YAMLIntegrityError
			if errors.As(got, &gotIntegrity) != test.wantIntegrity {
				t.Fatalf("integrity discoverability = %t, want %t", gotIntegrity != nil, test.wantIntegrity)
			}
		})
	}
}

func TestTraversalEntrypointsPreserveTypedGraphFailures(t *testing.T) {
	// Arrange.
	p, err := parsePresentationContext(context.Background(), []byte("---\ntype: Knowledge\nsources: []\n---\n"))
	if err != nil {
		t.Fatal(err)
	}
	sources := mappingValuesForTest(t, p.root, "sources")[0]
	wide := yamlRenderValue{kind: yamlRenderSequence, sequence: make([]yamlRenderValue, bundle.MaxYAMLGraphNodes)}
	cycleContent := make([]yamlRenderValue, 1)
	cycle := yamlRenderValue{kind: yamlRenderSequence, sequence: cycleContent}
	cycleContent[0] = cycle
	tests := []struct {
		name      string
		desired   yamlRenderValue
		operation func(*yamlMutation, yamlRenderValue) error
		wantCode  string
		owner     *yaml.Node
		resource  bool
		integrity bool
	}{
		{name: "mapping resource", desired: wide, operation: func(m *yamlMutation, desired yamlRenderValue) error {
			return m.insertMappingValue(p.root, "too_wide", desired)
		}, wantCode: yamlCodeResourceLimit, owner: p.root, resource: true},
		{name: "whole collection integrity", desired: cycle, operation: func(m *yamlMutation, desired yamlRenderValue) error {
			return m.replaceCollectionValue(sources, desired)
		}, wantCode: yamlCodeGraphIntegrity, owner: sources, integrity: true},
		{name: "ensure resource", desired: wide, operation: func(m *yamlMutation, desired yamlRenderValue) error {
			return m.ensureSequenceItem(sources, yamlExactSelector(desired), desired)
		}, wantCode: yamlCodeResourceLimit, owner: sources, resource: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Act.
			mutation := p.newYAMLMutationContext(context.Background())
			err := test.operation(mutation, test.desired)
			out, applyErr := mutation.apply()

			// Assert.
			var presentation *PresentationError
			var resource *bundle.YAMLResourceLimitError
			var integrity *bundle.YAMLIntegrityError
			if !errors.As(err, &presentation) || presentation.Code != test.wantCode ||
				presentation.Location != p.yamlNodesSpan(test.owner) ||
				errors.As(err, &resource) != test.resource || errors.As(err, &integrity) != test.integrity ||
				!errors.Is(err, ErrUnsupportedPresentation) || out != nil || applyErr != err {
				t.Fatalf("entrypoint error/apply = %#v / %#v, bytes=%q", err, applyErr, out)
			}
		})
	}
}
