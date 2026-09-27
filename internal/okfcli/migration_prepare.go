package okfcli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/internal/markdownowner"
	"github.com/skosovsky/okf/internal/migrationpath"
)

// migrationMappingTemplate is deliberately the existing --citation-mappings
// wire format. Empty source_id fields are placeholders and fail its loader
// until the caller explicitly supplies real identities.
type migrationMappingTemplate []migrationMappingTemplateDocument

type migrationMappingTemplateDocument struct {
	Path    string                          `json:"path"`
	Entries []migrationMappingTemplateEntry `json:"entries"`
}

type migrationMappingTemplateEntry struct {
	LegacyNumber uint64 `json:"legacy_number,omitempty"`
	LegacyEntry  string `json:"legacy_entry,omitempty"`
	SourceID     string `json:"source_id"`
	Title        string `json:"title,omitempty"`
	Resource     string `json:"resource,omitempty"`
}

type migrationPreparationReport struct {
	SourceSHA256 string                           `json:"source_sha256"`
	Required     migrationPreparationRequired     `json:"required_inputs"`
	GeneratedAt  []migrationGeneratedAtTemplate   `json:"generated_at_template"`
	Suggestions  []migrationPreparationSuggestion `json:"suggestions"`
	Unresolved   []migrationPreparationUnresolved `json:"unresolved"`
}

type migrationGeneratedAtTemplate struct {
	Path string `json:"path"`
	At   string `json:"at"`
}

type migrationPreparationRequired struct {
	GeneratedBy       string `json:"generated_by"`
	TimestampPolicy   string `json:"timestamp_policy"`
	TimestampConflict string `json:"timestamp_conflict"`
}

type migrationPreparationSuggestion struct {
	Path         string `json:"path"`
	LegacyNumber uint64 `json:"legacy_number,omitempty"`
	LegacyEntry  string `json:"legacy_entry"`
	StartByte    int    `json:"start_byte"`
	EndByte      int    `json:"end_byte"`
	Title        string `json:"title,omitempty"`
	Resource     string `json:"resource,omitempty"`
	Status       string `json:"status"`
}

type migrationPreparationUnresolved struct {
	Path      string `json:"path"`
	StartByte int    `json:"start_byte"`
	EndByte   int    `json:"end_byte"`
	Reason    string `json:"reason"`
}

// prepareMigrationInputs performs an entirely read-only, parser-backed
// projection. Its digest is an advisory freshness check for the exported
// template; it is never a migration proof. Preview/apply still create and
// verify their own revision, source and plan proof.
func prepareMigrationInputs(ctx context.Context, source bundle.Source) (migrationMappingTemplate, migrationPreparationReport, error) {
	if source == nil {
		return nil, migrationPreparationReport{}, fmt.Errorf("prepare migration: nil source")
	}
	paths, err := source.Paths(ctx)
	if err != nil {
		return nil, migrationPreparationReport{}, err
	}
	sort.Strings(paths)
	hash := sha256.New()
	template := migrationMappingTemplate{}
	report := migrationPreparationReport{
		Required: migrationPreparationRequired{
			GeneratedBy: "", TimestampPolicy: "preserve", TimestampConflict: "reject",
		},
		GeneratedAt: []migrationGeneratedAtTemplate{},
		Suggestions: []migrationPreparationSuggestion{},
		Unresolved: []migrationPreparationUnresolved{{
			Reason: "generated.by requires an explicit producer actor (--actor)",
		}},
	}
	var frame [8]byte
	totalMappings := 0
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, migrationPreparationReport{}, err
		}
		data, err := source.ReadFile(ctx, path)
		if err != nil {
			return nil, migrationPreparationReport{}, err
		}
		binary.BigEndian.PutUint64(frame[:], uint64(len(path)))
		_, _ = hash.Write(frame[:])
		_, _ = hash.Write([]byte(path))
		binary.BigEndian.PutUint64(frame[:], uint64(len(data)))
		_, _ = hash.Write(frame[:])
		_, _ = hash.Write(data)
		if !migrationpath.IsMarkdownDocument(path) {
			continue
		}
		if bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
			report.Unresolved = append(report.Unresolved, migrationPreparationUnresolved{
				Path: path, StartByte: 0, EndByte: 3, Reason: "leading_bom_unsupported",
			})
			continue
		}
		sourcesPresent := false
		if path != "index.md" && path != "log.md" {
			document, parseErr := bundle.ParseDocumentContext(ctx, string(data))
			if parseErr != nil {
				return nil, migrationPreparationReport{}, fmt.Errorf("prepare migration %q: %w", path, parseErr)
			}
			_, sourcesPresent, parseErr = document.Frontmatter.GetContext(ctx, "sources")
			if parseErr != nil {
				return nil, migrationPreparationReport{}, fmt.Errorf("prepare migration %q sources: %w", path, parseErr)
			}
			if _, hasType := document.Frontmatter.Type(); hasType &&
				!document.Frontmatter.TimestampState().Present &&
				!document.Frontmatter.GenerationState().Present {
				if len(report.GeneratedAt) >= maxCitationMappingDocuments {
					return nil, migrationPreparationReport{}, fmt.Errorf("prepare migration: generated-at entry limit exceeded")
				}
				report.GeneratedAt = append(report.GeneratedAt, migrationGeneratedAtTemplate{Path: path, At: ""})
				report.Unresolved = append(report.Unresolved, migrationPreparationUnresolved{
					Path: path, Reason: "generated.at requires an explicit historical instant",
				})
			}
		}
		projection, err := markdownowner.CollectCitationSectionProjection(ctx, data)
		if err != nil {
			var ownershipErr *markdownowner.OwnershipError
			if errors.As(err, &ownershipErr) && ownershipErr.Ambiguous {
				report.Unresolved = append(report.Unresolved, migrationPreparationUnresolved{
					Path: path, StartByte: ownershipErr.Span.Start, EndByte: ownershipErr.Span.End,
					Reason: ownershipErr.Code,
				})
				continue
			}
			return nil, migrationPreparationReport{}, fmt.Errorf("prepare migration %q: %w", path, err)
		}
		if !projection.Found {
			continue
		}
		if sourcesPresent {
			report.Unresolved = append(report.Unresolved, migrationPreparationUnresolved{
				Path: path, StartByte: projection.Section.Span.Start, EndByte: projection.Section.Span.End,
				Reason: "sources_citations_conflict",
			})
			continue
		}
		if len(path) > maxCitationMappingString {
			report.Unresolved = append(report.Unresolved, migrationPreparationUnresolved{
				Path: path, Reason: "path exceeds citation mapping input limit",
			})
			continue
		}
		countNumber := make(map[uint64]int)
		countRaw := make(map[string]int)
		for _, entry := range projection.Entries {
			if entry.Number != 0 {
				countNumber[entry.Number]++
			}
			countRaw[entry.Raw]++
		}
		document := migrationMappingTemplateDocument{Path: path, Entries: []migrationMappingTemplateEntry{}}
		for _, entry := range projection.Entries {
			if err := ctx.Err(); err != nil {
				return nil, migrationPreparationReport{}, err
			}
			suggestion := migrationPreparationSuggestion{
				Path: path, LegacyNumber: entry.Number, LegacyEntry: entry.Raw,
				StartByte: entry.Span.Start, EndByte: entry.Span.End,
				Title: entry.Title, Resource: entry.Resource, Status: "proposal",
			}
			report.Suggestions = append(report.Suggestions, suggestion)
			selector := migrationMappingTemplateEntry{SourceID: ""}
			rawValid := validateCitationMappingLegacyEntry(strictJSONString{Value: entry.Raw, Present: true}) == nil
			switch {
			case entry.Number != 0 && entry.Number <= maxCitationMappingNumber && countNumber[entry.Number] == 1:
				selector.LegacyNumber = entry.Number
				if rawValid {
					selector.LegacyEntry = entry.Raw
				}
			case rawValid && countRaw[entry.Raw] == 1:
				selector.LegacyEntry = entry.Raw
			default:
				report.Unresolved = append(report.Unresolved, migrationPreparationUnresolved{
					Path: path, StartByte: entry.Span.Start, EndByte: entry.Span.End,
					Reason: "duplicate or unrepresentable parser-owned citation selectors",
				})
				continue
			}
			if totalMappings >= maxCitationMappingEntries {
				return nil, migrationPreparationReport{}, fmt.Errorf("prepare migration: citation mapping entry limit exceeded")
			}
			document.Entries = append(document.Entries, selector)
			totalMappings++
		}
		if len(document.Entries) != 0 {
			if len(template) >= maxCitationMappingDocuments {
				return nil, migrationPreparationReport{}, fmt.Errorf("prepare migration: citation mapping document limit exceeded")
			}
			template = append(template, document)
		}
		for _, residual := range projection.Residual {
			report.Unresolved = append(report.Unresolved, migrationPreparationUnresolved{
				Path: path, StartByte: residual.Start, EndByte: residual.End,
				Reason: "unrecognized content in Citations section",
			})
		}
		for _, opaque := range projection.Opaque {
			report.Unresolved = append(report.Unresolved, migrationPreparationUnresolved{
				Path: path, StartByte: opaque.Start, EndByte: opaque.End,
				Reason: "opaque content in Citations section",
			})
		}
	}
	report.SourceSHA256 = hex.EncodeToString(hash.Sum(nil))
	return template, report, nil
}

func migrationSourceSHA256(ctx context.Context, source bundle.Source) (string, error) {
	paths, err := source.Paths(ctx)
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	hash := sha256.New()
	var frame [8]byte
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		data, err := source.ReadFile(ctx, path)
		if err != nil {
			return "", err
		}
		binary.BigEndian.PutUint64(frame[:], uint64(len(path)))
		_, _ = hash.Write(frame[:])
		_, _ = hash.Write([]byte(path))
		binary.BigEndian.PutUint64(frame[:], uint64(len(data)))
		_, _ = hash.Write(frame[:])
		_, _ = hash.Write(data)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
