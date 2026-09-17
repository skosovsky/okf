package validator

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/internal/markdownowner"
	"gopkg.in/yaml.v3"
)

func (v *validator) validateStrictV02() error {
	ids, err := v.bundle.ConceptIDsContext(v.ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := v.check(); err != nil {
			return err
		}
		path, ok, err := v.bundle.ConceptPathContext(v.ctx, id)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		document, ok, err := v.readDocumentContext(path, false)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		concept := bundle.NewConcept(id, path, document)
		if err := v.validateV02Resource(concept); err != nil {
			return err
		}
		if err := v.validateSources(concept); err != nil {
			return err
		}
		if err := v.validateSourceFootnotes(concept); err != nil {
			return err
		}
		useCitations, err := v.useLegacyCitationsFallback(concept)
		if err != nil {
			return err
		}
		if useCitations {
			if err := v.validateLegacyCitations(concept); err != nil {
				return err
			}
		}
		if err := v.validateGenerated(concept); err != nil {
			return err
		}
		if err := v.validateVerified(concept); err != nil {
			return err
		}
		if err := v.validateLifecycle(concept); err != nil {
			return err
		}
		if err := v.validateAttestedComputation(concept); err != nil {
			return err
		}
	}
	return nil
}

func (v *validator) useLegacyCitationsFallback(concept bundle.Concept) (bool, error) {
	observation, err := concept.Document.LegacyFallbackObservationContext(v.ctx)
	if err != nil {
		return false, err
	}
	return observation.CitationsActive, v.check()
}

func (v *validator) useLegacyTimestampFallback(concept bundle.Concept) (bool, error) {
	observation, err := concept.Document.LegacyFallbackObservationContext(v.ctx)
	if err != nil {
		return false, err
	}
	return observation.TimestampActive, v.check()
}

func (v *validator) validateV02Resource(concept bundle.Concept) error {
	frontmatter := concept.Document.Frontmatter
	node, ok, err := frontmatter.SemanticGetContext(v.ctx, "resource")
	if err != nil {
		return err
	}
	if valid, err := nonEmptyScalarContext(v.ctx, node); err != nil {
		return err
	} else if ok && !valid {
		v.warn("path_value_invalid", concept.Path, "resource", "'resource' should be a non-empty string path, URL, or scope descriptor")
	}
	tags, present, err := frontmatter.SemanticGetContext(v.ctx, "tags")
	if err != nil {
		return err
	}
	if !present {
		return nil
	}
	if tags.Kind != yaml.SequenceNode {
		v.warn("tags_invalid", concept.Path, "tags", "'tags' should be a YAML sequence of strings")
		return nil
	}
	for i, tag := range tags.Content {
		if err := v.check(); err != nil {
			return err
		}
		tag, _, err = bundle.SemanticNodeContext(v.ctx, tag)
		if err != nil {
			return err
		}
		valid, err := nonEmptyScalarContext(v.ctx, tag)
		if err != nil {
			return err
		}
		if !valid {
			path := fmt.Sprintf("tags[%d]", i)
			v.warn("tags_invalid", concept.Path, path, fmt.Sprintf("'%s' should be a non-empty string", path))
		}
	}
	return v.check()
}

func (v *validator) validateSources(concept bundle.Concept) error {
	frontmatter := concept.Document.Frontmatter
	sourceIDs := v.newStringBuckets()
	normalizedSourceIDs := v.newStringValueBuckets()
	sharedWindow, hasSharedWindow, err := frontmatter.SemanticGetContext(v.ctx, "usage_window")
	if err != nil {
		return err
	}
	sharedWindowValid := false
	if hasSharedWindow {
		sharedWindowValid, err = v.validateUsageWindow(concept.Path, "usage_window", sharedWindow)
		if err != nil {
			return err
		}
	}

	sources, ok, err := frontmatter.SemanticGetContext(v.ctx, "sources")
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if sources.Kind != yaml.SequenceNode {
		v.warn("sources_invalid", concept.Path, "sources", "'sources' should be a YAML sequence of mappings")
		return nil
	}

	for i, source := range sources.Content {
		if err := v.check(); err != nil {
			return err
		}
		path := fmt.Sprintf("sources[%d]", i)
		source, _, err = bundle.SemanticNodeContext(v.ctx, source)
		if err != nil {
			return err
		}
		if source == nil {
			v.warn("source_invalid", concept.Path, path, fmt.Sprintf("'%s' should be a mapping", path))
			continue
		}
		if source.Kind != yaml.MappingNode {
			v.warn("source_invalid", concept.Path, path, fmt.Sprintf("'%s' should be a mapping", path))
			continue
		}

		resource, hasResource, err := bundle.SemanticMappingValueContext(v.ctx, source, "resource")
		if err != nil {
			return err
		}
		validResource, err := nonEmptyScalarContext(v.ctx, resource)
		if err != nil {
			return err
		}
		if !hasResource || !validResource {
			v.warn("source_resource_missing", concept.Path, path+".resource", fmt.Sprintf("'%s.resource' should be a non-empty string", path))
		}

		for _, field := range []string{"id", "title", "author"} {
			if err := v.check(); err != nil {
				return err
			}
			node, present, err := bundle.SemanticMappingValueContext(v.ctx, source, field)
			if err != nil {
				return err
			}
			if !present {
				continue
			}
			fieldPath := path + "." + field
			value, valid, err := nonEmptyStringContext(v.ctx, node)
			if err != nil {
				return err
			}
			if !valid {
				v.warn("source_field_invalid", concept.Path, fieldPath, fmt.Sprintf("'%s' should be a non-empty string", fieldPath))
				continue
			}
			if field == "id" {
				duplicate, err := sourceIDs.InsertContext(v.ctx, value)
				if err != nil {
					return err
				}
				if duplicate {
					v.warn("source_id_duplicate", concept.Path, fieldPath, v.diagnosticMessage("source id %q is duplicated", value))
				}
				normalized, err := markdownowner.NormalizeFootnoteLabelContext(v.ctx, value)
				if err != nil {
					return err
				}
				previous, collision, err := normalizedSourceIDs.LookupContext(v.ctx, normalized)
				if err != nil {
					return err
				}
				sameValue := false
				if collision {
					result, err := compareValidatorStringsContext(v.ctx, previous, value)
					if err != nil {
						return err
					}
					sameValue = result == 0
				}
				if collision && !sameValue {
					v.warn(
						"normalized_footnote_label_collision",
						concept.Path,
						fieldPath,
						v.diagnosticMessage("source id %q collides with %q after CommonMark label normalization; action: disambiguate_citation_entry", value, previous),
					)
				} else if !collision {
					if err := normalizedSourceIDs.PutContext(v.ctx, normalized, value); err != nil {
						return err
					}
				}
			}
		}

		if modified, present, err := bundle.SemanticMappingValueContext(v.ctx, source, "last_modified"); err != nil {
			return err
		} else if valid, err := validDateScalarContext(v.ctx, modified); err != nil {
			return err
		} else if present && !valid {
			v.warn("source_field_invalid", concept.Path, path+".last_modified", fmt.Sprintf("'%s.last_modified' should be YYYY-MM-DD", path))
		}

		localWindow, hasLocalWindow, err := bundle.SemanticMappingValueContext(v.ctx, source, "usage_window")
		if err != nil {
			return err
		}
		localWindowValid := false
		if hasLocalWindow {
			localWindowValid, err = v.validateUsageWindow(concept.Path, path+".usage_window", localWindow)
			if err != nil {
				return err
			}
		}
		if usageCount, present, err := bundle.SemanticMappingValueContext(v.ctx, source, "usage_count"); err != nil {
			return err
		} else if present {
			valid, err := validNonNegativeIntegerContext(v.ctx, usageCount)
			if err != nil {
				return err
			}
			if !valid {
				v.warn("source_usage_count_invalid", concept.Path, path+".usage_count", fmt.Sprintf("'%s.usage_count' should be a non-negative integer", path))
			}
			effectiveWindowValid := sharedWindowValid
			if hasLocalWindow {
				effectiveWindowValid = localWindowValid
			}
			if !effectiveWindowValid {
				v.warn("source_usage_window_missing", concept.Path, path+".usage_count", fmt.Sprintf("'%s.usage_count' requires a valid effective usage_window", path))
			}
		}
	}
	return v.check()
}

func (v *validator) validateUsageWindow(file, path string, node *yaml.Node) (bool, error) {
	if node == nil || node.Kind != yaml.MappingNode {
		v.warn("usage_window_invalid", file, path, fmt.Sprintf("'%s' should be a mapping with from/to dates", path))
		return false, nil
	}
	from, hasFrom, err := bundle.SemanticMappingValueContext(v.ctx, node, "from")
	if err != nil {
		return false, err
	}
	to, hasTo, err := bundle.SemanticMappingValueContext(v.ctx, node, "to")
	if err != nil {
		return false, err
	}
	fromDate, fromOK, err := parseDateScalarContext(v.ctx, from)
	if err != nil {
		return false, err
	}
	toDate, toOK, err := parseDateScalarContext(v.ctx, to)
	if err != nil {
		return false, err
	}
	if !hasFrom || !fromOK {
		v.warn("usage_window_invalid", file, path+".from", fmt.Sprintf("'%s.from' should be YYYY-MM-DD", path))
	}
	if !hasTo || !toOK {
		v.warn("usage_window_invalid", file, path+".to", fmt.Sprintf("'%s.to' should be YYYY-MM-DD", path))
	}
	if fromOK && toOK && fromDate.After(toDate) {
		v.warn("usage_window_order", file, path, fmt.Sprintf("'%s.from' should be on or before '%s.to'", path, path))
		return false, nil
	}
	return hasFrom && hasTo && fromOK && toOK, v.check()
}

func (v *validator) validateSourceFootnotes(concept bundle.Concept) error {
	attributions, err := concept.Document.AttributionsContext(v.ctx)
	if err != nil {
		return err
	}
	for _, attribution := range attributions {
		if err := v.check(); err != nil {
			return err
		}
		fieldPath := v.diagnosticMessage("body.footnotes[%s]", attribution.ID)
		if len(attribution.Sources) == 0 && (len(attribution.References) != 0 || len(attribution.Definitions) != 0) {
			v.warn("source_footnote_unknown", concept.Path, fieldPath, v.diagnosticMessage("footnote %q does not match a sources[].id", attribution.ID))
		}
		if len(attribution.References) != 0 && len(attribution.Definitions) == 0 {
			v.warn("source_footnote_definition_missing", concept.Path, fieldPath, v.diagnosticMessage("footnote %q has no definition", attribution.ID))
		}
		if len(attribution.Definitions) > 1 {
			v.warn("source_footnote_definition_duplicate", concept.Path, fieldPath, v.diagnosticMessage("footnote %q has duplicate definitions", attribution.ID))
		}
	}
	return v.check()
}

func (v *validator) validateGenerated(concept bundle.Concept) error {
	generated, present, err := concept.Document.Frontmatter.SemanticGetContext(v.ctx, "generated")
	if err != nil {
		return err
	}
	if !present {
		useTimestamp, err := v.useLegacyTimestampFallback(concept)
		if err != nil {
			return err
		}
		if useTimestamp {
			if err := v.validateLegacyTimestamp(concept); err != nil {
				return err
			}
		}
		return v.check()
	}
	if generated.Kind != yaml.MappingNode {
		v.warn("generated_invalid", concept.Path, "generated", "'generated' should be a mapping")
		return v.check()
	}
	by, hasBy, err := bundle.SemanticMappingValueContext(v.ctx, generated, "by")
	if err != nil {
		return err
	}
	validActor, err := validActorScalarContext(v.ctx, by)
	if err != nil {
		return err
	}
	if !hasBy || !validActor {
		code := "generated_by_missing"
		if hasBy {
			code = "actor_invalid"
		}
		v.warn(code, concept.Path, "generated.by", "'generated.by' should follow the OKF actor convention")
	}
	if at, ok, err := bundle.SemanticMappingValueContext(v.ctx, generated, "at"); err != nil {
		return err
	} else if valid, err := validRFC3339ScalarContext(v.ctx, at); err != nil {
		return err
	} else if ok && !valid {
		v.warn("generated_at_invalid", concept.Path, "generated.at", "'generated.at' should be RFC3339")
	}
	return v.check()
}

func (v *validator) validateVerified(concept bundle.Concept) error {
	verified, present, err := concept.Document.Frontmatter.SemanticGetContext(v.ctx, "verified")
	if err != nil {
		return err
	}
	if err := v.check(); err != nil {
		return err
	}
	if !present {
		return nil
	}
	var events []*yaml.Node
	switch verified.Kind {
	case yaml.MappingNode:
		events = []*yaml.Node{verified}
	case yaml.SequenceNode:
		events = verified.Content
		if len(events) == 0 {
			v.warn("verified_invalid", concept.Path, "verified", "'verified' should contain at least one verification event")
			return nil
		}
	default:
		v.warn("verified_invalid", concept.Path, "verified", "'verified' should be a mapping or sequence of mappings")
		return nil
	}
	for i, event := range events {
		if err := v.check(); err != nil {
			return err
		}
		path := fmt.Sprintf("verified[%d]", i)
		event, _, err = bundle.SemanticNodeContext(v.ctx, event)
		if err != nil {
			return err
		}
		if event == nil {
			v.warn("verification_invalid", concept.Path, path, fmt.Sprintf("'%s' should be a mapping", path))
			continue
		}
		if event.Kind != yaml.MappingNode {
			v.warn("verification_invalid", concept.Path, path, fmt.Sprintf("'%s' should be a mapping", path))
			continue
		}
		by, hasBy, err := bundle.SemanticMappingValueContext(v.ctx, event, "by")
		if err != nil {
			return err
		}
		byNonEmpty, err := nonEmptyScalarContext(v.ctx, by)
		if err != nil {
			return err
		}
		if !hasBy || !byNonEmpty {
			v.warn("verification_by_missing", concept.Path, path+".by", fmt.Sprintf("'%s.by' should be a non-empty actor", path))
		} else if valid, err := validActorScalarContext(v.ctx, by); err != nil {
			return err
		} else if !valid {
			v.warn("actor_invalid", concept.Path, path+".by", fmt.Sprintf("'%s.by' should follow the OKF actor convention", path))
		}
		at, hasAt, err := bundle.SemanticMappingValueContext(v.ctx, event, "at")
		if err != nil {
			return err
		}
		if !hasAt {
			v.warn("verification_at_missing", concept.Path, path+".at", fmt.Sprintf("'%s.at' is required", path))
		} else if valid, err := validRFC3339ScalarContext(v.ctx, at); err != nil {
			return err
		} else if !valid {
			v.warn("verification_at_invalid", concept.Path, path+".at", fmt.Sprintf("'%s.at' should be RFC3339", path))
		}
	}
	return v.check()
}

func (v *validator) validateLegacyTimestamp(concept bundle.Concept) error {
	timestamp, present, err := concept.Document.Frontmatter.SemanticGetContext(v.ctx, "timestamp")
	if err != nil {
		return err
	}
	valid, err := validRFC3339ScalarContext(v.ctx, timestamp)
	if err != nil {
		return err
	}
	if present && !valid {
		v.warn("timestamp_invalid", concept.Path, "timestamp", "'timestamp' should be RFC3339")
	}
	return v.check()
}

func (v *validator) validateLifecycle(concept bundle.Concept) error {
	if err := v.check(); err != nil {
		return err
	}
	if status, present, err := concept.Document.Frontmatter.SemanticGetContext(v.ctx, "status"); err != nil {
		return err
	} else if present {
		value, ok, err := nonEmptyStringContext(v.ctx, status)
		if err != nil {
			return err
		}
		if !ok || value != "draft" && value != "stable" && value != "deprecated" {
			v.warn("status_invalid", concept.Path, "status", "'status' should be draft, stable, or deprecated")
		}
	}
	staleAfter, present, err := concept.Document.Frontmatter.SemanticGetContext(v.ctx, "stale_after")
	if err != nil {
		return err
	}
	if !present {
		return v.check()
	}
	_, ok, err := parseDateScalarContext(v.ctx, staleAfter)
	if err != nil {
		return err
	}
	if !ok {
		v.warn("stale_after_invalid", concept.Path, "stale_after", "'stale_after' should be YYYY-MM-DD")
		return v.check()
	}
	if !v.cfg.ReferenceDate.IsZero() {
		reference := civilDate(v.cfg.ReferenceDate)
		if concept.Document.Frontmatter.IsStale(reference) {
			v.warn("stale", concept.Path, "stale_after", fmt.Sprintf("concept is stale as of %s", reference.Format(time.DateOnly)))
		}
	}
	return v.check()
}

func (v *validator) validateAttestedComputation(concept bundle.Concept) error {
	payloadState, err := concept.Document.AttestedComputationStateContext(v.ctx)
	if err != nil {
		return err
	}
	if !payloadState.TypeMatches {
		return nil
	}
	frontmatter := concept.Document.Frontmatter

	runtime, present, err := frontmatter.SemanticGetContext(v.ctx, "runtime")
	if err != nil {
		return err
	}
	if !present {
		v.warn("runtime_missing", concept.Path, "runtime", "'runtime' is required for Attested Computation")
	} else if valid, err := nonEmptyScalarContext(v.ctx, runtime); err != nil {
		return err
	} else if !valid {
		v.warn("runtime_invalid", concept.Path, "runtime", "'runtime' should be a non-empty string")
	}

	if parameters, present, err := frontmatter.SemanticGetContext(v.ctx, "parameters"); err != nil {
		return err
	} else if present {
		if err := v.validateParameters(concept.Path, parameters); err != nil {
			return err
		}
	}

	computation, computationPresent, err := frontmatter.SemanticGetContext(v.ctx, "computation")
	if err != nil {
		return err
	}
	fileMode, err := nonEmptyScalarContext(v.ctx, computation)
	if err != nil {
		return err
	}
	fileMode = computationPresent && fileMode
	if computationPresent && !fileMode {
		v.warn("computation_invalid", concept.Path, "computation", "'computation' should be a non-empty string path")
	}
	if fileMode && payloadState.Conflict {
		v.warn("computation_conflict", concept.Path, "computation", "file and inline computation modes are mutually exclusive")
	} else if payloadState.Multiple {
		v.warn("computation_inline_multiple", concept.Path, "body.# Computation", "inline computation mode permits exactly one directly-owned closed fence under '# Computation'")
	} else if (payloadState.Mode == bundle.ComputationModeMalformed || payloadState.Mode == bundle.ComputationModeAmbiguous) &&
		len(payloadState.FenceSpans) != 0 {
		v.warn("computation_inline_malformed", concept.Path, "body.# Computation", "observed inline computation candidates must be one directly-owned closed fence under exact '# Computation'")
	} else if !fileMode {
		switch payloadState.Mode {
		case bundle.ComputationModeInline:
		default:
			v.warn("computation_inline_missing", concept.Path, "body.# Computation", "inline computation mode requires one directly-owned closed fence under exact '# Computation'")
		}
	}

	executor, hasExecutor, err := frontmatter.SemanticGetContext(v.ctx, "executor")
	if err != nil {
		return err
	}
	if !hasExecutor {
		v.warn("executor_missing", concept.Path, "executor", "'executor' is recommended for usable Attested Computation")
	} else {
		if err := v.validateExecutor(concept.Path, executor); err != nil {
			return err
		}
	}
	attester, hasAttester, err := frontmatter.SemanticGetContext(v.ctx, "attester")
	if err != nil {
		return err
	}
	if !hasAttester {
		v.warn("attester_missing", concept.Path, "attester", "'attester' is recommended for usable Attested Computation")
	} else {
		if err := v.validateAttester(concept.Path, attester); err != nil {
			return err
		}
	}
	return v.check()
}

func (v *validator) validateParameters(file string, parameters *yaml.Node) error {
	if parameters.Kind != yaml.SequenceNode {
		v.warn("parameters_invalid", file, "parameters", "'parameters' should be a sequence of mappings")
		return nil
	}
	names := v.newStringBuckets()
	for i, parameter := range parameters.Content {
		if err := v.check(); err != nil {
			return err
		}
		path := fmt.Sprintf("parameters[%d]", i)
		parameter, _, err := bundle.SemanticNodeContext(v.ctx, parameter)
		if err != nil {
			return err
		}
		if parameter == nil {
			v.warn("parameter_invalid", file, path, fmt.Sprintf("'%s' should be a mapping", path))
			continue
		}
		if parameter.Kind != yaml.MappingNode {
			v.warn("parameter_invalid", file, path, fmt.Sprintf("'%s' should be a mapping", path))
			continue
		}
		name, hasName, err := bundle.SemanticMappingValueContext(v.ctx, parameter, "name")
		if err != nil {
			return err
		}
		nameValue, validName, err := nonEmptyStringContext(v.ctx, name)
		if err != nil {
			return err
		}
		if !hasName || !validName {
			v.warn("parameter_name_missing", file, path+".name", fmt.Sprintf("'%s.name' should be a non-empty string", path))
		} else if duplicate, err := names.InsertContext(v.ctx, nameValue); err != nil {
			return err
		} else if duplicate {
			v.warn("parameter_name_duplicate", file, path+".name", v.diagnosticMessage("parameter name %q is duplicated", nameValue))
		}
		typ, hasType, err := bundle.SemanticMappingValueContext(v.ctx, parameter, "type")
		if err != nil {
			return err
		}
		validType, err := nonEmptyScalarContext(v.ctx, typ)
		if err != nil {
			return err
		}
		if !hasType || !validType {
			v.warn("parameter_type_missing", file, path+".type", fmt.Sprintf("'%s.type' should be a non-empty string", path))
		}
		required, hasRequired, err := bundle.SemanticMappingValueContext(v.ctx, parameter, "required")
		if err != nil {
			return err
		}
		if !hasRequired || required.Kind != yaml.ScalarNode || required.Tag != "!!bool" {
			v.warn("parameter_required_invalid", file, path+".required", fmt.Sprintf("'%s.required' should be a boolean", path))
		}
	}
	return v.check()
}

func (v *validator) validateExecutor(file string, executor *yaml.Node) error {
	if executor.Kind != yaml.MappingNode {
		v.warn("executor_invalid", file, "executor", "'executor' should be a mapping")
		return nil
	}
	resource, hasResource, err := bundle.SemanticMappingValueContext(v.ctx, executor, "resource")
	if err != nil {
		return err
	}
	validResource, err := nonEmptyScalarContext(v.ctx, resource)
	if err != nil {
		return err
	}
	if !hasResource || !validResource {
		v.warn("executor_resource_missing", file, "executor.resource", "'executor.resource' should be a non-empty string path")
	}
	receipt, hasReceipt, err := bundle.SemanticMappingValueContext(v.ctx, executor, "receipt")
	if err != nil {
		return err
	}
	if !hasReceipt {
		v.warn("receipt_missing", file, "executor.receipt", "'executor.receipt' is recommended for usable attestation")
		return nil
	}
	if receipt.Kind != yaml.SequenceNode {
		v.warn("receipt_invalid", file, "executor.receipt", "'executor.receipt' should be a sequence of field names")
		return nil
	}
	if len(receipt.Content) == 0 {
		v.warn("receipt_invalid", file, "executor.receipt", "'executor.receipt' should contain at least one field name")
		return nil
	}
	fields := v.newStringBuckets()
	for i, field := range receipt.Content {
		if err := v.check(); err != nil {
			return err
		}
		path := fmt.Sprintf("executor.receipt[%d]", i)
		field, _, err = bundle.SemanticNodeContext(v.ctx, field)
		if err != nil {
			return err
		}
		value, ok, err := nonEmptyStringContext(v.ctx, field)
		if err != nil {
			return err
		}
		if !ok {
			v.warn("receipt_field_invalid", file, path, fmt.Sprintf("'%s' should be a non-empty string", path))
			continue
		}
		duplicate, err := fields.InsertContext(v.ctx, value)
		if err != nil {
			return err
		}
		if duplicate {
			v.warn("receipt_field_duplicate", file, path, v.diagnosticMessage("receipt field %q is duplicated", value))
		}
	}
	return v.check()
}

func (v *validator) validateAttester(file string, attester *yaml.Node) error {
	if attester.Kind != yaml.MappingNode {
		v.warn("attester_invalid", file, "attester", "'attester' should be a mapping")
		return nil
	}
	resource, hasResource, err := bundle.SemanticMappingValueContext(v.ctx, attester, "resource")
	if err != nil {
		return err
	}
	valid, err := nonEmptyScalarContext(v.ctx, resource)
	if err != nil {
		return err
	}
	if !hasResource || !valid {
		v.warn("attester_resource_missing", file, "attester.resource", "'attester.resource' should be a non-empty string path")
	}
	return v.check()
}

func (v *validator) warn(code, file, fieldPath, message string) {
	v.addSpecAt(SeverityWarning, code, strictSpecRef(code), file, fieldPath, message)
}

func strictSpecRef(code string) string {
	switch code {
	case "tags_invalid":
		return "okf-v0.2#4.1"
	case "source_id_duplicate":
		return "toolkit#sources-id-unique"
	case "normalized_footnote_label_collision":
		return "toolkit#source-footnote-integrity"
	case "source_usage_count_invalid", "source_usage_window_missing", "usage_window_order":
		return "toolkit#sources-usage-profile"
	case "source_footnote_unknown", "source_footnote_definition_missing", "source_footnote_definition_duplicate":
		return "toolkit#source-footnote-integrity"
	case "sources_invalid", "source_invalid", "source_resource_missing", "source_field_invalid", "usage_window_invalid":
		return "okf-v0.2#5.1"
	case "generated_invalid", "generated_by_missing", "generated_at_invalid", "verified_invalid",
		"verification_invalid", "verification_by_missing", "verification_at_missing", "verification_at_invalid":
		return "okf-v0.2#5.2"
	case "timestamp_invalid":
		return "okf-v0.2#13.1"
	case "actor_invalid":
		return "okf-v0.2#7"
	case "status_invalid":
		return "okf-v0.2#5.4"
	case "stale_after_invalid", "stale":
		return "okf-v0.2#5.5"
	case "computation_invalid":
		return "okf-v0.2#6.2"
	case "computation_conflict", "computation_inline_malformed", "computation_inline_missing", "computation_inline_multiple":
		return "okf-v0.2#10.3"
	case "runtime_missing", "runtime_invalid", "parameters_invalid", "parameter_invalid",
		"parameter_name_missing", "parameter_type_missing", "parameter_required_invalid":
		return "okf-v0.2#10.2"
	case "parameter_name_duplicate":
		return "toolkit#computation-parameter-unique"
	case "executor_invalid", "executor_resource_missing", "receipt_invalid", "receipt_field_invalid",
		"attester_invalid", "attester_resource_missing":
		return "okf-v0.2#10.2"
	case "receipt_field_duplicate":
		return "toolkit#receipt-field-unique"
	case "executor_missing", "receipt_missing", "attester_missing":
		return "toolkit#computation-usability"
	case "path_value_invalid":
		return "okf-v0.2#6.2"
	default:
		return "okf-v0.2#11"
	}
}

func nonEmptyString(node *yaml.Node) (string, bool) {
	value, valid, _ := nonEmptyStringContext(context.Background(), node)
	return value, valid
}

func nonEmptyStringContext(ctx context.Context, node *yaml.Node) (string, bool, error) {
	valid, err := nonEmptyScalarContext(ctx, node)
	if err != nil || !valid {
		return "", false, err
	}
	return node.Value, true, nil
}

func nonEmptyScalarContext(ctx context.Context, node *yaml.Node) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return false, nil
	}
	for offset := 0; offset < len(node.Value); {
		if offset%(64<<10) == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		current, size := utf8.DecodeRuneInString(node.Value[offset:])
		if current == utf8.RuneError && size == 1 {
			return false, nil
		}
		if !unicode.IsSpace(current) {
			return true, ctx.Err()
		}
		offset += size
	}
	return false, ctx.Err()
}

func validActorScalarContext(ctx context.Context, node *yaml.Node) (bool, error) {
	value, ok, err := nonEmptyStringContext(ctx, node)
	if err != nil || !ok {
		return false, err
	}
	first := true
	lastSpace := false
	slashes, slashIndex := 0, -1
	for offset := 0; offset < len(value); {
		if offset%(64<<10) == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		current, size := utf8.DecodeRuneInString(value[offset:])
		if current == utf8.RuneError && size == 1 {
			return false, nil
		}
		space := unicode.IsSpace(current)
		if first && space || unicode.IsControl(current) {
			return false, nil
		}
		first, lastSpace = false, space
		if current == '/' {
			slashes, slashIndex = slashes+1, offset
		}
		offset += size
	}
	if lastSpace {
		return false, nil
	}
	for _, prefix := range []string{"human:", "process:"} {
		if strings.HasPrefix(value, prefix) {
			return nonBlankStringValueContext(ctx, value[len(prefix):])
		}
	}
	if slashes != 1 {
		return false, nil
	}
	left, err := nonBlankStringValueContext(ctx, value[:slashIndex])
	if err != nil || !left {
		return false, err
	}
	return nonBlankStringValueContext(ctx, value[slashIndex+1:])
}

func nonBlankStringValueContext(ctx context.Context, value string) (bool, error) {
	node := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
	return nonEmptyScalarContext(ctx, node)
}

func validRFC3339ScalarContext(ctx context.Context, node *yaml.Node) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!str" && node.Tag != "!!timestamp" {
		return false, nil
	}
	for offset := 0; offset < len(node.Value); offset += 64 << 10 {
		if err := ctx.Err(); err != nil {
			return false, err
		}
	}
	_, err := time.Parse(time.RFC3339, node.Value)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return false, ctxErr
	}
	return err == nil, nil
}

func validDateScalarContext(ctx context.Context, node *yaml.Node) (bool, error) {
	_, valid, err := parseDateScalarContext(ctx, node)
	return valid, err
}

func parseDateScalarContext(ctx context.Context, node *yaml.Node) (time.Time, bool, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, false, err
	}
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!str" && node.Tag != "!!timestamp" || len(node.Value) != len(time.DateOnly) {
		return time.Time{}, false, nil
	}
	value, err := time.Parse(time.DateOnly, node.Value)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return time.Time{}, false, ctxErr
	}
	return value, err == nil, nil
}

func validNonNegativeIntegerContext(ctx context.Context, node *yaml.Node) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!int" {
		return false, nil
	}
	for offset := 0; offset < len(node.Value); offset += 64 << 10 {
		if err := ctx.Err(); err != nil {
			return false, err
		}
	}
	_, err := bundle.ParseNonNegativeYAMLUint64(node.Value)
	if ctxErr := validNonNegativeIntegerPostParseContext(ctx); ctxErr != nil {
		return false, ctxErr
	}
	return err == nil, nil
}

func validNonNegativeIntegerPostParseContext(ctx context.Context) error {
	return ctx.Err()
}

func nonEmptyStringNode(node *yaml.Node) bool {
	valid, _ := nonEmptyScalarContext(context.Background(), node)
	return valid
}

func nonEmptyScalar(node *yaml.Node) bool {
	valid, _ := nonEmptyScalarContext(context.Background(), node)
	return valid
}

func validActorScalar(node *yaml.Node) bool {
	valid, _ := validActorScalarContext(context.Background(), node)
	return valid
}

func validRFC3339Scalar(node *yaml.Node) bool {
	valid, _ := validRFC3339ScalarContext(context.Background(), node)
	return valid
}

func validDateScalar(node *yaml.Node) bool {
	valid, _ := validDateScalarContext(context.Background(), node)
	return valid
}

func parseDateScalar(node *yaml.Node) (time.Time, bool) {
	value, valid, _ := parseDateScalarContext(context.Background(), node)
	return value, valid
}

func validNonNegativeInteger(node *yaml.Node) bool {
	valid, _ := validNonNegativeIntegerContext(context.Background(), node)
	return valid
}

func civilDate(value time.Time) time.Time {
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}
