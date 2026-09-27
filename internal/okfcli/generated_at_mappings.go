package okfcli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/skosovsky/okf/internal/migrationpath"
	"github.com/skosovsky/okf/store"
)

type generatedAtInput struct {
	Path strictJSONString `json:"path"`
	At   strictJSONString `json:"at"`
}

func loadGeneratedAtMappings(path string) ([]store.MigrationGeneratedAt, error) {
	if path == "" {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read generated-at mappings %q: %w", path, err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxCitationMappingsFileBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read generated-at mappings %q: %w", path, readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close generated-at mappings %q: %w", path, closeErr)
	}
	if len(data) > maxCitationMappingsFileBytes {
		return nil, fmt.Errorf("generated-at mappings file size limit exceeded")
	}
	return parseGeneratedAtMappings(data)
}

func parseGeneratedAtMappings(data []byte) ([]store.MigrationGeneratedAt, error) {
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("decode generated-at mappings: invalid UTF-8")
	}
	if err := validateCitationMappingsJSONStructure(data); err != nil {
		return nil, fmt.Errorf("decode generated-at mappings: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var input []generatedAtInput
	if err := decoder.Decode(&input); err != nil {
		return nil, fmt.Errorf("decode generated-at mappings: %w", err)
	}
	if input == nil || len(input) > maxCitationMappingDocuments {
		return nil, fmt.Errorf("decode generated-at mappings: expected bounded array")
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, fmt.Errorf("decode generated-at mappings: %w", err)
	}
	seen := make(map[string]struct{}, len(input))
	output := make([]store.MigrationGeneratedAt, 0, len(input))
	for index, entry := range input {
		if !entry.Path.Present || entry.Path.Value == "" ||
			len(entry.Path.Value) > maxCitationMappingString ||
			!migrationpath.IsMarkdownDocument(entry.Path.Value) {
			return nil, fmt.Errorf("decode generated-at mappings: entries[%d].path is invalid", index)
		}
		if _, duplicate := seen[entry.Path.Value]; duplicate {
			return nil, fmt.Errorf("decode generated-at mappings: duplicate path %q", entry.Path.Value)
		}
		seen[entry.Path.Value] = struct{}{}
		if !entry.At.Present || entry.At.Value == "" || len(entry.At.Value) > maxCitationMappingString {
			return nil, fmt.Errorf("decode generated-at mappings: entries[%d].at is required", index)
		}
		if _, err := time.Parse(time.RFC3339Nano, entry.At.Value); err != nil {
			return nil, fmt.Errorf("decode generated-at mappings: entries[%d].at must be RFC3339 with explicit timezone", index)
		}
		output = append(output, store.MigrationGeneratedAt{Path: entry.Path.Value, At: entry.At.Value})
	}
	sort.Slice(output, func(i, j int) bool { return output[i].Path < output[j].Path })
	return output, nil
}
