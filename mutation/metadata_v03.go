package mutation

import (
	"bytes"
	"errors"
	"strings"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/internal/documentlayout"
	"github.com/skosovsky/okf/store"
)

func (s *planState) requireVersion03() error {
	version := s.bundle.DiscoveryVersion()
	if version.Effective != bundle.OKFVersion03 || version.Source == bundle.VersionSourceFutureBestEffort {
		return invalid("okf_v03_required", nil, errors.New("operation requires an OKF 0.3 bundle"))
	}
	return nil
}

func (s *planState) setBundleMetadata(op store.SetBundleMetadata) error {
	if err := s.requireVersion03(); err != nil {
		return err
	}
	const index = "index.md"
	data, present, err := s.bundle.ReadFileContext(s.ctx, index)
	if err != nil {
		return err
	}
	if !present {
		return invalid("bundle_metadata_index", nil, errors.New("root index.md is missing"))
	}
	updated, err := s.replaceBundleMetadata(op, data)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, updated) {
		if err := s.overlay.PutContext(s.ctx, index, updated); err != nil {
			return err
		}
	}
	s.plans = append(s.plans, store.OperationPlan{Operation: op, Details: []string{"root bundle metadata updated"}})
	return nil
}

func (s *planState) replaceBundleMetadata(op store.SetBundleMetadata, data []byte) ([]byte, error) {
	title, description, license := op.Metadata()
	fields := []struct {
		key   string
		value *string
	}{{"title", title}, {"description", description}, {"license", license}}
	if !documentlayout.HasOpeningDelimiter(data) {
		var entries []yamlRenderEntry
		for _, field := range fields {
			if field.value != nil && *field.value != "" {
				entries = append(entries, yamlEntry(field.key, yamlString(*field.value)))
			}
		}
		if len(entries) == 0 {
			return data, nil
		}
		header, err := renderYAMLBlockContext(s.ctx, yamlMapping(entries...), 0, true)
		if err != nil {
			return nil, err
		}
		header = "---\n" + header + "\n---\n"
		crlf, err := containsCRLFContext(s.ctx, data)
		if err != nil {
			return nil, err
		}
		if crlf {
			header = strings.ReplaceAll(header, "\n", "\r\n")
		}
		return appendBytesContext(s.ctx, []byte(header), data)
	}
	p, err := parseBundlePresentationContext(s.ctx, s.bundle, data)
	if err != nil {
		return nil, err
	}
	mutation := p.newYAMLMutationContext(s.ctx)
	for _, field := range fields {
		if field.value == nil {
			continue
		}
		value := field.value
		if *value == "" {
			value = nil
		}
		if err := mutateOptionalRootString(mutation, p.root, field.key, value); err != nil {
			return nil, err
		}
	}
	return mutation.apply()
}

func (s *planState) setSupersededBy(op store.SetSupersededBy) error {
	if err := s.requireVersion03(); err != nil {
		return err
	}
	path := op.Path()
	if path != nil {
		concept, found, err := s.bundle.GetContext(s.ctx, op.Concept)
		if err != nil {
			return err
		}
		if !found {
			return invalid("missing_concept", nil, errors.New("concept does not exist"))
		}
		status, err := concept.Document.Frontmatter.StatusStateV03Context(s.ctx)
		if err != nil {
			return err
		}
		if status.Effective != bundle.StatusDeprecated {
			return invalid("replacement_status", nil, errors.New("superseded_by requires deprecated status"))
		}
		resolved, _, err := s.bundle.ResolvePathValueForContext(s.ctx, concept.Path, *path, bundle.PathFieldSupersededBy)
		if err != nil {
			return err
		}
		target, targetErr := bundle.ConceptIDFromPath("", resolved.Path)
		if !resolved.Exists || targetErr != nil || (target.String()+".md") != resolved.Path || !s.bundle.Contains(target) {
			return invalid("replacement_target", nil, errors.New("superseded_by must resolve to a captured concept"))
		}
	}
	return s.applyV02ConceptOperation(op.Concept, "set_superseded_by", op, func(p *presentation) ([]byte, error) {
		mutation := p.newYAMLMutationContext(p.ctx)
		if err := mutateOptionalRootString(mutation, p.root, "superseded_by", path); err != nil {
			return nil, err
		}
		return mutation.apply()
	})
}
