package mcpserver

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/skosovsky/okf/bundle"
)

type mutationOutputState struct {
	Status         string   `json:"status"`
	BaseRevision   string   `json:"base_revision"`
	ResultRevision *string  `json:"result_revision"`
	AffectedPaths  []string `json:"affected_paths"`
	ChangedPaths   []string `json:"changed_paths"`
}

type migrationOutputSourceState struct {
	Status string             `json:"status"`
	Source migrationSourceDTO `json:"source"`
}

func validateMutationOutputState(tool string, value any) error {
	switch tool {
	case "preview_concept_patch", "preview_v02_migration",
		"apply_concept_patch", "apply_v02_migration":
	default:
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal %s output invariant: %w", tool, err)
	}
	var state mutationOutputState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("decode %s output invariant: %w", tool, err)
	}
	paths := state.ChangedPaths
	if tool == "preview_concept_patch" || tool == "preview_v02_migration" {
		paths = state.AffectedPaths
	}
	if err := validateCanonicalOutputPaths(tool, paths); err != nil {
		return err
	}
	isPreview := tool == "preview_concept_patch" || tool == "preview_v02_migration"

	switch state.Status {
	case "applicable":
		if !isPreview {
			return fmt.Errorf("%s output has unsupported status %q", tool, state.Status)
		}
		if state.ResultRevision == nil || state.BaseRevision == *state.ResultRevision {
			return fmt.Errorf("%s %s output did not advance the revision", tool, state.Status)
		}
		if len(paths) == 0 {
			return fmt.Errorf("%s %s output has no affected paths", tool, state.Status)
		}
	case "applied":
		if isPreview {
			return fmt.Errorf("%s output has unsupported status %q", tool, state.Status)
		}
		if state.ResultRevision == nil || state.BaseRevision == *state.ResultRevision {
			return fmt.Errorf("%s %s output did not advance the revision", tool, state.Status)
		}
		if len(paths) == 0 {
			return fmt.Errorf("%s %s output has no affected paths", tool, state.Status)
		}
	case "noop":
		if state.ResultRevision == nil || state.BaseRevision != *state.ResultRevision {
			return fmt.Errorf("%s noop output changed the revision", tool)
		}
		if len(paths) != 0 {
			return fmt.Errorf("%s noop output has affected paths", tool)
		}
	case "blocked":
		if tool != "preview_v02_migration" ||
			state.ResultRevision != nil ||
			len(paths) != 0 {
			return fmt.Errorf("%s output has invalid blocked state", tool)
		}
	case "rejected":
		switch tool {
		case "preview_concept_patch":
			if state.ResultRevision != nil || len(paths) != 0 {
				return fmt.Errorf("%s output has invalid rejected state", tool)
			}
		case "apply_concept_patch", "apply_v02_migration":
			if state.ResultRevision == nil ||
				state.BaseRevision != *state.ResultRevision ||
				len(paths) != 0 {
				return fmt.Errorf("%s output has invalid rejected state", tool)
			}
		default:
			return fmt.Errorf("%s output has unsupported status %q", tool, state.Status)
		}
	default:
		return fmt.Errorf("%s output has unsupported status %q", tool, state.Status)
	}
	return nil
}

func validateCanonicalOutputPaths(tool string, paths []string) error {
	for index, path := range paths {
		if err := bundle.ValidateRevisionPath(path); err != nil {
			return fmt.Errorf("%s output contains a noncanonical path", tool)
		}
		if index == 0 {
			continue
		}
		switch {
		case paths[index-1] == path:
			return fmt.Errorf("%s output contains duplicate paths", tool)
		case paths[index-1] > path:
			return fmt.Errorf("%s output paths are not canonical", tool)
		}
	}
	return nil
}

func validateMigrationOutputSourceState(tool string, value any) error {
	if tool != "preview_v02_migration" && tool != "apply_v02_migration" {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal %s migration source invariant: %w", tool, err)
	}
	var state migrationOutputSourceState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("decode %s migration source invariant: %w", tool, err)
	}
	source := migrationSourceFromDTO(state.Source)
	validationErr := validateApplicableMigrationSourceTuple(source)
	if tool == "preview_v02_migration" &&
		errors.Is(validationErr, errExpectedMigrationSourceBlocked) {
		return nil
	}
	if validationErr != nil {
		return fmt.Errorf(
			"%s source is not a canonical migration resolution: %w",
			tool,
			validationErr,
		)
	}
	return nil
}
