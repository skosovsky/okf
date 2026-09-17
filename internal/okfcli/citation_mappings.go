package okfcli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/skosovsky/okf/internal/migrationpath"
	"github.com/skosovsky/okf/store"
)

const (
	maxCitationMappingsFileBytes = 16 << 20
	maxCitationMappingDocuments  = 10_000
	maxCitationMappingEntries    = 10_000
	maxCitationMappingString     = 4_096
	maxCitationMappingNumber     = 1_000_000
	maxCitationMappingsJSONDepth = 16
)

type citationMappingDocumentInput struct {
	Path    strictJSONString            `json:"path"`
	Entries []citationMappingEntryInput `json:"entries"`
}

type citationMappingEntryInput struct {
	LegacyNumber strictJSONUint64 `json:"legacy_number,omitempty"`
	LegacyEntry  strictJSONString `json:"legacy_entry,omitempty"`
	SourceID     strictJSONString `json:"source_id"`
	Title        strictJSONString `json:"title,omitempty"`
	Resource     strictJSONString `json:"resource,omitempty"`
}

type strictJSONString struct {
	Value   string
	Present bool
}

func (value *strictJSONString) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return fmt.Errorf("must be a string")
	}
	if err := json.Unmarshal(data, &value.Value); err != nil {
		return err
	}
	value.Present = true
	return nil
}

type strictJSONUint64 struct {
	Value   uint64
	Present bool
}

func (value *strictJSONUint64) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return fmt.Errorf("must be an unsigned integer")
	}
	if err := json.Unmarshal(data, &value.Value); err != nil {
		return err
	}
	value.Present = true
	return nil
}

func loadCitationMappings(path string) ([]store.DocumentCitationMigration, error) {
	return loadCitationMappingsWithOpener(path, openCitationMappingsFile)
}

func openCitationMappingsFile(path string) (io.ReadCloser, error) {
	return os.Open(path)
}

func loadCitationMappingsWithOpener(
	path string,
	opener citationMappingsOpener,
) ([]store.DocumentCitationMigration, error) {
	if path == "" {
		return nil, nil
	}
	if opener == nil {
		return nil, errors.New("citation mappings opener is nil")
	}
	reader, err := opener(path)
	if err != nil {
		return nil, fmt.Errorf("read citation mappings %q: %w", path, err)
	}
	if reader == nil {
		return nil, fmt.Errorf("read citation mappings %q: opener returned nil reader", path)
	}
	return loadCitationMappingsFromReadCloser(path, reader)
}

// loadCitationMappingsFromReadCloser owns reader and closes it exactly once.
// Parsing is completed before the result is returned, so the mapping handle
// can never survive into migration resolution or durable apply.
func loadCitationMappingsFromReadCloser(
	path string,
	reader io.ReadCloser,
) ([]store.DocumentCitationMigration, error) {
	data, readErr := io.ReadAll(io.LimitReader(reader, maxCitationMappingsFileBytes+1))
	var mappings []store.DocumentCitationMigration
	var operationErr error
	switch {
	case readErr != nil:
		operationErr = fmt.Errorf("read citation mappings %q: %w", path, readErr)
	case len(data) > maxCitationMappingsFileBytes:
		operationErr = fmt.Errorf("read citation mappings %q: file size limit exceeded", path)
	default:
		mappings, operationErr = parseCitationMappings(data)
	}
	closeErr := reader.Close()
	if closeErr != nil {
		closeErr = fmt.Errorf("close citation mappings %q: %w", path, closeErr)
	}
	if joined := errors.Join(operationErr, closeErr); joined != nil {
		return nil, joined
	}
	return mappings, nil
}

func parseCitationMappings(data []byte) ([]store.DocumentCitationMigration, error) {
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("decode citation mappings: JSON must be valid UTF-8")
	}
	if err := validateCitationMappingsJSONStructure(data); err != nil {
		return nil, fmt.Errorf("decode citation mappings: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var input []citationMappingDocumentInput
	if err := decoder.Decode(&input); err != nil {
		return nil, fmt.Errorf("decode citation mappings: %w", err)
	}
	if input == nil {
		return nil, fmt.Errorf("decode citation mappings: top-level value must be an array")
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, fmt.Errorf("decode citation mappings: %w", err)
	}
	if len(input) > maxCitationMappingDocuments {
		return nil, fmt.Errorf("decode citation mappings: document item limit exceeded")
	}

	documents := make([]store.DocumentCitationMigration, 0, len(input))
	seenPaths := make(map[string]struct{}, len(input))
	totalEntries := 0
	for documentIndex, document := range input {
		if !document.Path.Present {
			return nil, fmt.Errorf("decode citation mappings: mappings[%d].path is required", documentIndex)
		}
		path := document.Path.Value
		if len(path) == 0 || len(path) > maxCitationMappingString ||
			!migrationpath.IsMarkdownDocument(path) {
			return nil, fmt.Errorf("decode citation mappings: mappings[%d].path is invalid", documentIndex)
		}
		if _, duplicate := seenPaths[path]; duplicate {
			return nil, fmt.Errorf("decode citation mappings: duplicate path %q", path)
		}
		seenPaths[path] = struct{}{}
		if document.Entries == nil || len(document.Entries) == 0 {
			return nil, fmt.Errorf("decode citation mappings: mappings[%d].entries must be a non-empty array", documentIndex)
		}
		if len(document.Entries) > maxCitationMappingEntries-totalEntries {
			return nil, fmt.Errorf("decode citation mappings: entry item limit exceeded")
		}
		totalEntries += len(document.Entries)

		entries := make([]store.LegacyCitationMapping, 0, len(document.Entries))
		seenNumbers := make(map[uint64]struct{}, len(document.Entries))
		seenLegacyEntries := make(map[string]bool, len(document.Entries))
		for entryIndex, entry := range document.Entries {
			if entry.LegacyNumber.Present &&
				(entry.LegacyNumber.Value == 0 || entry.LegacyNumber.Value > maxCitationMappingNumber) {
				return nil, fmt.Errorf(
					"decode citation mappings: mappings[%d].entries[%d].legacy_number is invalid",
					documentIndex,
					entryIndex,
				)
			}
			if err := validateCitationMappingLegacyEntry(entry.LegacyEntry); err != nil {
				return nil, fmt.Errorf(
					"decode citation mappings: mappings[%d].entries[%d].legacy_entry %w",
					documentIndex,
					entryIndex,
					err,
				)
			}
			if !entry.LegacyNumber.Present && !entry.LegacyEntry.Present {
				return nil, fmt.Errorf(
					"decode citation mappings: mappings[%d].entries[%d] requires legacy_number or legacy_entry",
					documentIndex,
					entryIndex,
				)
			}
			if entry.LegacyNumber.Present {
				if _, duplicate := seenNumbers[entry.LegacyNumber.Value]; duplicate {
					return nil, fmt.Errorf(
						"decode citation mappings: mappings[%d] has duplicate legacy_number %d",
						documentIndex,
						entry.LegacyNumber.Value,
					)
				}
				seenNumbers[entry.LegacyNumber.Value] = struct{}{}
			}
			if entry.LegacyEntry.Present {
				previousHasNumber, duplicate := seenLegacyEntries[entry.LegacyEntry.Value]
				if duplicate && (!previousHasNumber || !entry.LegacyNumber.Present) {
					return nil, fmt.Errorf(
						"decode citation mappings: mappings[%d] has overlapping legacy_entry selector",
						documentIndex,
					)
				}
				seenLegacyEntries[entry.LegacyEntry.Value] = entry.LegacyNumber.Present
			}
			if err := validateCitationMappingString(entry.SourceID, true); err != nil {
				return nil, fmt.Errorf(
					"decode citation mappings: mappings[%d].entries[%d].source_id %w",
					documentIndex,
					entryIndex,
					err,
				)
			}
			if err := validateCitationMappingString(entry.Title, false); err != nil {
				return nil, fmt.Errorf(
					"decode citation mappings: mappings[%d].entries[%d].title %w",
					documentIndex,
					entryIndex,
					err,
				)
			}
			if err := validateCitationMappingString(entry.Resource, false); err != nil {
				return nil, fmt.Errorf(
					"decode citation mappings: mappings[%d].entries[%d].resource %w",
					documentIndex,
					entryIndex,
					err,
				)
			}
			entries = append(entries, store.LegacyCitationMapping{
				LegacyNumber: entry.LegacyNumber.Value,
				LegacyEntry:  entry.LegacyEntry.Value,
				SourceID:     entry.SourceID.Value,
				Title:        entry.Title.Value,
				Resource:     entry.Resource.Value,
			})
		}
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].LegacyNumber != entries[j].LegacyNumber {
				return entries[i].LegacyNumber < entries[j].LegacyNumber
			}
			return entries[i].LegacyEntry < entries[j].LegacyEntry
		})
		documents = append(documents, store.DocumentCitationMigration{Path: path, Entries: entries})
	}
	sort.Slice(documents, func(i, j int) bool {
		return documents[i].Path < documents[j].Path
	})
	return documents, nil
}

func validateCitationMappingLegacyEntry(value strictJSONString) error {
	if !value.Present {
		return nil
	}
	if len(value.Value) == 0 || len(value.Value) > maxCitationMappingString ||
		!utf8.ValidString(value.Value) || strings.TrimSpace(value.Value) == "" {
		return fmt.Errorf("is invalid")
	}
	for _, character := range value.Value {
		if character < 0x20 && character != '\t' && character != '\n' && character != '\r' ||
			character == 0x7f {
			return fmt.Errorf("is invalid")
		}
	}
	return nil
}

func validateCitationMappingString(value strictJSONString, required bool) error {
	if required && !value.Present {
		return fmt.Errorf("is required")
	}
	if !value.Present {
		return nil
	}
	if required && value.Value == "" {
		return fmt.Errorf("must not be empty")
	}
	if len(value.Value) > maxCitationMappingString || !utf8.ValidString(value.Value) ||
		strings.TrimSpace(value.Value) != value.Value {
		return fmt.Errorf("is invalid")
	}
	for _, character := range value.Value {
		if character < 0x20 || character == 0x7f {
			return fmt.Errorf("is invalid")
		}
	}
	return nil
}

func validateCitationMappingsJSONStructure(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if err := walkCitationMappingsJSON(decoder, token, 0); err != nil {
		return err
	}
	return requireJSONEOF(decoder)
}

func walkCitationMappingsJSON(decoder *json.Decoder, token json.Token, depth int) error {
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	if depth+1 > maxCitationMappingsJSONDepth {
		return fmt.Errorf("JSON nesting depth limit exceeded")
	}
	switch delimiter {
	case '[':
		for decoder.More() {
			child, err := decoder.Token()
			if err != nil {
				return err
			}
			if err := walkCitationMappingsJSON(decoder, child, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return fmt.Errorf("invalid JSON array")
		}
	case '{':
		keys := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("invalid JSON object key")
			}
			if _, duplicate := keys[key]; duplicate {
				return fmt.Errorf("duplicate JSON object key %q", key)
			}
			keys[key] = struct{}{}
			child, err := decoder.Token()
			if err != nil {
				return err
			}
			if err := walkCitationMappingsJSON(decoder, child, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return fmt.Errorf("invalid JSON object")
		}
	default:
		return fmt.Errorf("invalid JSON delimiter %q", delimiter)
	}
	return nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}
