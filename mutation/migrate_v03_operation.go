package mutation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
	"gopkg.in/yaml.v3"
)

type migrationPathKey struct {
	concept string
	field   string
}

type migrationPathEdit struct {
	field, value string
}

func (s *planState) migrateV02ToV03(op store.MigrateV02ToV03) error {
	version, err := s.bundle.VersionResolutionContext(s.ctx, "")
	if err != nil {
		return err
	}
	if version.Declared != bundle.OKFVersion && !(version.Declared == "" && op.AssumeV02) {
		return invalid("migration_v03_source_version", nil, errors.New("migration requires declared OKF 0.2 or an explicit 0.2 assumption for an unversioned bundle"))
	}
	if version.Declared == "" {
		loaded, err := bundle.LoadWithSpec(s.ctx, s.overlay, bundle.OKFVersion)
		if err != nil {
			return err
		}
		s.bundle = loaded
	}
	if failures := s.bundle.ParseErrors(); len(failures) > 0 {
		return invalid("migration_v03_invalid_source", nil, failures[0])
	}

	choices := make(map[migrationPathKey]string)
	for _, choice := range op.PathChoices() {
		choices[migrationPathKey{choice.Concept, choice.Field}] = choice.Target
	}
	var details []string
	var blockers []store.Diagnostic
	var affected []bundle.RelationRef
	var indexEntries []bundle.IndexEntry
	for _, concept := range s.bundle.Concepts() {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		typ, eligible := concept.Document.Frontmatter.Type()
		if !eligible {
			details = append(details, fmt.Sprintf("%s will be read as companion Markdown", concept.Path))
			continue
		}
		title, ok := concept.Document.Frontmatter.Title()
		if !ok {
			title = concept.ID.String()
		}
		description, _ := concept.Document.Frontmatter.Description()
		indexEntries = append(indexEntries, bundle.IndexEntry{Type: typ, Title: title, Link: "/" + concept.Path, Description: description})
		observations, _, err := s.bundle.ConceptPathValueObservationsContext(s.ctx, concept.ID)
		if err != nil {
			return err
		}
		if raw, ok := concept.Document.Frontmatter.Resource(); ok {
			observations.Values = append(observations.Values, bundle.ConceptPathValueObservation{
				Field: bundle.PathFieldConceptResource, FieldPath: "resource",
				Scalar: bundle.ScalarValueState{Present: true, Valid: true, Value: raw},
			})
		}
		var edits []migrationPathEdit
		for _, field := range observations.Values {
			if !field.Scalar.Valid {
				continue
			}
			key := migrationPathKey{concept.ID.String(), field.FieldPath}
			choice, chosen := choices[key]
			if chosen {
				delete(choices, key)
			}
			target, note, err := s.migrationV03PathTarget(concept.Path, field, choice)
			if err != nil {
				return err
			}
			if note != "" {
				details = append(details, note)
			}
			if target == "" && (chosen || strings.HasPrefix(note, "Choose ")) {
				blockers = append(blockers, store.Diagnostic{Code: "migration_v03_path_choice", File: concept.Path, Message: note, Refs: []bundle.RelationRef{{ID: concept.ID}}})
			} else if target != "" && target != field.Scalar.Value {
				edits = append(edits, migrationPathEdit{field.FieldPath, target})
			}
		}
		if len(edits) > 0 {
			data, err := s.overlay.ReadFile(s.ctx, concept.Path)
			if err != nil {
				return err
			}
			updated, err := rewriteMigrationPaths(s.ctx, data, edits)
			if err != nil {
				return fmt.Errorf("%s: %w", concept.Path, err)
			}
			if err := s.overlay.PutContext(s.ctx, concept.Path, updated); err != nil {
				return err
			}
			affected = append(affected, bundle.RelationRef{ID: concept.ID})
		}
		oldTrust, err := concept.Document.TrustTierContext(s.ctx)
		if err != nil {
			return err
		}
		newTrust, err := concept.Document.TrustTierForVersionContext(s.ctx, bundle.VersionResolution{Effective: bundle.OKFVersion03})
		if err != nil {
			return err
		}
		if oldTrust != newTrust {
			details = append(details, fmt.Sprintf("%s trust tier: %s → %s", concept.Path, oldTrust, newTrust))
		}
	}
	affected, details, blockers, err = s.migrateV03Markdown(choices, affected, details, blockers)
	if err != nil {
		return err
	}
	if len(choices) > 0 {
		return invalid("migration_v03_unknown_path_choice", nil, errors.New("a path choice does not identify a migration field in the source"))
	}
	if len(blockers) > 0 {
		return invalid("migration_v03_path_choices_required", blockers, errors.New("path choices required"))
	}
	affected, details, err = s.migrateV03YAML(affected, details)
	if err != nil {
		return err
	}
	affected, err = refsForContext(s.ctx, affected)
	if err != nil {
		return err
	}

	_, indexPresent, err := s.bundle.ReadFileContext(s.ctx, "index.md")
	if err != nil {
		return err
	}
	if !indexPresent {
		if len(indexEntries) == 0 {
			return invalid("migration_v03_no_concepts", nil, errors.New("cannot create an index without a concept"))
		}
		if err := s.overlay.PutContext(s.ctx, "index.md", []byte(bundle.BuildIndexTextV03(indexEntries))); err != nil {
			return err
		}
	}

	// Use the lossless root updater, and report the change as one operation.
	previousPlans := len(s.plans)
	if err := s.setBundleVersion(store.SetBundleVersion{Version: bundle.OKFVersion03}, "migrate_v02_to_v03"); err != nil {
		return err
	}
	s.plans = s.plans[:previousPlans]
	sourceVersion := version.Declared
	if sourceVersion == "" {
		sourceVersion = "unversioned (explicit 0.2 assumption)"
	}
	details = append(details, "index.md: "+sourceVersion+" → 0.3; existing metadata and extensions retained")
	s.add(affected...)
	s.plans = append(s.plans, store.OperationPlan{Operation: op, Details: details, AffectedRefs: affected})
	return s.ctx.Err()
}

func (s *planState) migrateV03Markdown(choices map[migrationPathKey]string, affected []bundle.RelationRef, details []string, blockers []store.Diagnostic) ([]bundle.RelationRef, []string, []store.Diagnostic, error) {
	files, err := s.bundle.MarkdownFilesContext(s.ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, file := range files {
		id, err := bundle.ConceptIDFromPath("", file)
		if err != nil {
			return nil, nil, nil, err
		}
		data, err := s.overlay.ReadFile(s.ctx, file)
		if err != nil {
			return nil, nil, nil, err
		}
		var ordinal int
		var resolveErr error
		updated, err := rewriteMarkdownDestinationsContext(s.ctx, data, func(raw string) (string, bool) {
			ordinal++
			if resolveErr != nil {
				return raw, false
			}
			key := migrationPathKey{id.String(), fmt.Sprintf("markdown[%d]", ordinal)}
			choice, chosen := choices[key]
			if chosen {
				delete(choices, key)
			}
			target, note, failed := s.migrationV03MarkdownTarget(id, file, key.field, raw, choice)
			resolveErr = failed
			if note != "" {
				details = append(details, note)
			}
			if target == "" && (chosen || strings.HasPrefix(note, "Choose ")) {
				blockers = append(blockers, store.Diagnostic{
					Code: "migration_v03_path_choice", File: file, Message: note, Refs: []bundle.RelationRef{{ID: id}},
				})
			}
			return target, target != "" && target != raw
		})
		if resolveErr != nil {
			return nil, nil, nil, resolveErr
		}
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%s: %w", file, err)
		}
		if !bytes.Equal(data, updated) {
			if err := s.overlay.PutContext(s.ctx, file, updated); err != nil {
				return nil, nil, nil, err
			}
			if s.bundle.Contains(id) {
				affected = append(affected, bundle.RelationRef{ID: id})
			}
		}
	}
	return affected, details, blockers, nil
}

func (s *planState) migrationV03MarkdownTarget(source bundle.ConceptID, file, field, raw, choice string) (string, string, error) {
	label := file + " " + field
	kind, err := bundle.ClassifyLinkContext(s.ctx, raw)
	if err != nil {
		return "", "", err
	}
	if kind != bundle.LinkAbsolute && kind != bundle.LinkRelative {
		if choice == "" {
			return "", "", nil
		}
		return "", "Path choice for " + label + " must identify a local file", nil
	}
	legacy, _, err := s.bundle.ResolvePathValueForContext(s.ctx, file, raw, bundle.PathFieldConceptResource)
	if err != nil {
		return "", "", err
	}
	legacy = migrationMarkdownDirectory(s.bundle, legacy)
	if !legacy.Exists {
		id, ok, err := (bundle.Link{Target: raw, Kind: kind}).ResolveContext(s.ctx, source)
		if err != nil {
			return "", "", err
		}
		if ok {
			if path, found, err := s.bundle.ConceptPathContext(s.ctx, id); err != nil {
				return "", "", err
			} else if found {
				legacy.Path, legacy.Exists = path, true
			}
		}
	}
	current, _, err := s.bundle.ResolveMarkdownTargetV03Context(s.ctx, file, raw)
	if err != nil {
		return "", "", err
	}
	current = migrationMarkdownDirectory(s.bundle, current)
	if !legacy.Exists && choice == "" {
		if !current.Exists {
			return "", fmt.Sprintf("%s: unresolved Markdown destination %q", label, raw), nil
		}
		return raw, "", nil
	}
	candidates := make([]string, 0, 2)
	if legacy.Exists {
		candidates = append(candidates, legacy.Path)
	}
	if current.Exists && current.Path != legacy.Path {
		candidates = append(candidates, current.Path)
	}
	if choice == "" && len(candidates) > 1 || choice != "" && !containsMigrationPath(candidates, choice) {
		return "", fmt.Sprintf("Choose a captured target for %s (%q): %s", label, raw, strings.Join(candidates, ", ")), nil
	}
	selected := legacy.Path
	if choice != "" {
		selected = choice
	}
	if choice == "" && current.Exists && current.Path == selected && canonicalMigrationURI(raw, current.Suffix) {
		return raw, "", nil
	}
	target := bundle.EscapePathV03("/"+selected) + legacy.Suffix
	return target, fmt.Sprintf("%s: %q → %q", label, raw, target), nil
}

func migrationMarkdownDirectory(loaded *bundle.Bundle, value bundle.ResolvedPathValue) bundle.ResolvedPathValue {
	if !value.Exists && value.Path != "" {
		index := value.Path + "/index.md"
		if _, _, found := loaded.CapturedFileMetadata(index); found {
			value.Path, value.Exists = index, true
		}
	}
	return value
}

func canonicalMigrationURI(raw, suffix string) bool {
	path := strings.TrimSuffix(raw, suffix)
	decoded, err := url.PathUnescape(path)
	return err == nil && bundle.EscapePathV03(decoded) == path
}

func (s *planState) migrationV03PathTarget(path string, field bundle.ConceptPathValueObservation, choice string) (target, note string, err error) {
	raw := field.Scalar.Value
	legacy, _, err := s.bundle.ResolvePathValueForContext(s.ctx, path, raw, field.Field)
	if err != nil {
		return "", "", err
	}
	if legacy.Kind == bundle.PathValueURL || legacy.Kind == bundle.PathValueScope {
		if choice != "" {
			return "", fmt.Sprintf("Path choice for %s %s must identify a local file", path, field.FieldPath), nil
		}
		return "", "", nil
	}
	current, _, err := s.bundle.ResolveMarkdownTargetV03Context(s.ctx, path, raw)
	if err != nil {
		return "", "", err
	}
	root := legacy
	if !strings.HasPrefix(raw, "/") {
		root, _, err = s.bundle.ResolvePathValueForContext(s.ctx, path, "/"+raw, field.Field)
		if err != nil {
			return "", "", err
		}
	}
	candidates := make([]string, 0, 3)
	for _, resolved := range []bundle.ResolvedPathValue{legacy, root, current} {
		if resolved.Exists && !containsMigrationPath(candidates, resolved.Path) {
			candidates = append(candidates, resolved.Path)
		}
	}
	label := path + " " + field.FieldPath
	if len(candidates) == 0 {
		return "", fmt.Sprintf("%s: unresolved path %q", label, raw), nil
	}
	if choice == "" && (len(candidates) > 1 || !legacy.Exists && !root.Exists) ||
		choice != "" && !containsMigrationPath(candidates, choice) {
		return "", fmt.Sprintf("Choose a captured target for %s (%q): %s", label, raw, strings.Join(candidates, ", ")), nil
	}
	selected := legacy.Path
	if !legacy.Exists {
		selected = root.Path
	}
	if choice != "" {
		selected = choice
	}
	if current.Exists && current.Path == selected && canonicalMigrationURI(raw, current.Suffix) && choice == "" {
		return raw, "", nil
	}
	target = bundle.EscapePathV03("/"+selected) + legacy.Suffix
	return target, fmt.Sprintf("%s: %q → %q", label, raw, target), nil
}

func containsMigrationPath(paths []string, needle string) bool {
	for _, path := range paths {
		if path == needle {
			return true
		}
	}
	return false
}

func migrationPathParent(p *presentation, field string) (*yaml.Node, string, error) {
	parentKey, leaf, nested := strings.Cut(field, ".")
	if !nested {
		return p.root, field, nil
	}
	if offset := strings.IndexByte(parentKey, '['); offset >= 0 {
		index, err := strconv.Atoi(strings.TrimSuffix(parentKey[offset+1:], "]"))
		if err != nil || index < 0 {
			return nil, "", fmt.Errorf("invalid source position in %s", field)
		}
		entry, found, err := p.structuralMappingEntry(p.root, parentKey[:offset])
		if err != nil {
			return nil, "", err
		}
		if !found || entry.value.Kind != yaml.SequenceNode || index >= len(entry.value.Content) {
			return nil, "", fmt.Errorf("source position in %s is missing", field)
		}
		return entry.value.Content[index], leaf, nil
	}
	entry, found, err := p.structuralMappingEntry(p.root, parentKey)
	if err != nil {
		return nil, "", err
	}
	if !found || entry.value.Kind != yaml.MappingNode {
		return nil, "", fmt.Errorf("path field container %s is missing", parentKey)
	}
	return entry.value, leaf, nil
}

func rewriteMigrationPaths(ctx context.Context, data []byte, edits []migrationPathEdit) ([]byte, error) {
	p, err := parsePresentationContext(ctx, data)
	if err != nil {
		return nil, err
	}
	mutation := p.newYAMLMutationContext(ctx)
	for _, edit := range edits {
		parent, key, err := migrationPathParent(p, edit.field)
		if err != nil {
			return nil, err
		}
		if err := mutation.replaceMappingValue(parent, key, yamlString(edit.value)); err != nil {
			return nil, err
		}
	}
	return mutation.apply()
}
