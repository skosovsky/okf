package mutation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/skosovsky/okf/internal/markdownowner"
)

type MarkdownSection struct {
	Heading     string
	Level       int
	HeadingSpan SourceSpan
	Span        SourceSpan
}

type MarkdownCitationEntry struct {
	Ordinal     int
	Number      uint64
	Span        SourceSpan
	ContentSpan SourceSpan
	Title       string
	Resource    string
	LinkTitle   string
	Raw         string
}

type MarkdownFootnoteReference struct {
	Label           string
	NormalizedLabel string
	Span            SourceSpan
}

type MarkdownFootnoteDefinition struct {
	Label           string
	NormalizedLabel string
	Span            SourceSpan
	ContentSpan     SourceSpan
	Content         string
}

type MarkdownNumericMarker struct {
	Number uint64
	Span   SourceSpan
}

type MarkdownLabelCollision struct {
	NormalizedLabel string
	Labels          []string
	Spans           []SourceSpan
}

type MarkdownComputationBoundary struct {
	Sections []MarkdownSection
	Fences   []SourceSpan
}

type MarkdownMigrationOwnership struct {
	Sections              []MarkdownSection
	CitationEntries       []MarkdownCitationEntry
	NumericMarkers        []MarkdownNumericMarker
	FootnoteReferences    []MarkdownFootnoteReference
	FootnoteDefinitions   []MarkdownFootnoteDefinition
	LabelCollisions       []MarkdownLabelCollision
	Computation           MarkdownComputationBoundary
	CitationsSectionIndex int
}

// CollectMarkdownMigrationOwnership delegates all lexical and parser
// ownership decisions to internal/markdownowner. This adapter only converts
// neutral spans and typed collector failures into mutation package contracts.
func CollectMarkdownMigrationOwnership(ctx context.Context, source []byte) (MarkdownMigrationOwnership, error) {
	ownership, err := markdownowner.CollectMarkdownMigrationOwnership(ctx, source)
	if err != nil {
		return MarkdownMigrationOwnership{}, mutationMarkdownOwnerError(err)
	}
	return mutationMigrationOwnershipContext(ctx, ownership)
}

func mutationMarkdownOwnerError(err error) error {
	var ownershipErr *markdownowner.OwnershipError
	if !errors.As(err, &ownershipErr) {
		return err
	}
	cause := error(ErrUnsupportedPresentation)
	if ownershipErr.Ambiguous {
		cause = ErrAmbiguousPresentation
	}
	return markdownPresentationErrorAt(
		ownershipErr.Code,
		fmt.Errorf("%w: %w", cause, ownershipErr),
		mutationSourceSpan(ownershipErr.Span),
	)
}

func mutationMigrationOwnershipContext(ctx context.Context, ownership markdownowner.MigrationOwnership) (MarkdownMigrationOwnership, error) {
	if err := ctx.Err(); err != nil {
		return MarkdownMigrationOwnership{}, err
	}
	out := MarkdownMigrationOwnership{
		Sections:              make([]MarkdownSection, len(ownership.Sections)),
		CitationEntries:       make([]MarkdownCitationEntry, len(ownership.CitationEntries)),
		NumericMarkers:        make([]MarkdownNumericMarker, len(ownership.NumericMarkers)),
		FootnoteReferences:    make([]MarkdownFootnoteReference, len(ownership.FootnoteReferences)),
		FootnoteDefinitions:   make([]MarkdownFootnoteDefinition, len(ownership.FootnoteDefinitions)),
		LabelCollisions:       make([]MarkdownLabelCollision, len(ownership.LabelCollisions)),
		CitationsSectionIndex: ownership.CitationsSectionIndex,
	}
	for index, section := range ownership.Sections {
		if err := ctx.Err(); err != nil {
			return MarkdownMigrationOwnership{}, err
		}
		out.Sections[index] = mutationMarkdownSection(section)
	}
	for index, entry := range ownership.CitationEntries {
		if err := ctx.Err(); err != nil {
			return MarkdownMigrationOwnership{}, err
		}
		out.CitationEntries[index] = MarkdownCitationEntry{
			Ordinal:     entry.Ordinal,
			Number:      entry.Number,
			Span:        mutationSourceSpan(entry.Span),
			ContentSpan: mutationSourceSpan(entry.ContentSpan),
			Title:       entry.Title,
			Resource:    entry.Resource,
			LinkTitle:   entry.LinkTitle,
			Raw:         entry.Raw,
		}
	}
	for index, marker := range ownership.NumericMarkers {
		if err := ctx.Err(); err != nil {
			return MarkdownMigrationOwnership{}, err
		}
		out.NumericMarkers[index] = MarkdownNumericMarker{Number: marker.Number, Span: mutationSourceSpan(marker.Span)}
	}
	for index, reference := range ownership.FootnoteReferences {
		if err := ctx.Err(); err != nil {
			return MarkdownMigrationOwnership{}, err
		}
		out.FootnoteReferences[index] = MarkdownFootnoteReference{
			Label: reference.Label, NormalizedLabel: reference.NormalizedLabel, Span: mutationSourceSpan(reference.Span),
		}
	}
	for index, definition := range ownership.FootnoteDefinitions {
		if err := ctx.Err(); err != nil {
			return MarkdownMigrationOwnership{}, err
		}
		out.FootnoteDefinitions[index] = MarkdownFootnoteDefinition{
			Label: definition.Label, NormalizedLabel: definition.NormalizedLabel,
			Span: mutationSourceSpan(definition.Span), ContentSpan: mutationSourceSpan(definition.ContentSpan),
			Content: definition.Content,
		}
	}
	for index, collision := range ownership.LabelCollisions {
		if err := ctx.Err(); err != nil {
			return MarkdownMigrationOwnership{}, err
		}
		labels := make([]string, len(collision.Labels))
		for labelIndex, label := range collision.Labels {
			if err := ctx.Err(); err != nil {
				return MarkdownMigrationOwnership{}, err
			}
			labels[labelIndex] = label
		}
		spans, err := mutationSourceSpansContext(ctx, collision.Spans)
		if err != nil {
			return MarkdownMigrationOwnership{}, err
		}
		out.LabelCollisions[index] = MarkdownLabelCollision{
			NormalizedLabel: collision.NormalizedLabel,
			Labels:          labels,
			Spans:           spans,
		}
	}
	out.Computation.Sections = make([]MarkdownSection, len(ownership.Computation.Sections))
	for index, section := range ownership.Computation.Sections {
		if err := ctx.Err(); err != nil {
			return MarkdownMigrationOwnership{}, err
		}
		out.Computation.Sections[index] = mutationMarkdownSection(section)
	}
	fences, err := mutationSourceSpansContext(ctx, ownership.Computation.Fences)
	if err != nil {
		return MarkdownMigrationOwnership{}, err
	}
	out.Computation.Fences = fences
	return out, ctx.Err()
}

func mutationMarkdownSection(section markdownowner.MarkdownSection) MarkdownSection {
	return MarkdownSection{
		Heading: section.Heading, Level: section.Level,
		HeadingSpan: mutationSourceSpan(section.HeadingSpan), Span: mutationSourceSpan(section.Span),
	}
}

func mutationSourceSpansContext(ctx context.Context, spans []markdownowner.Span) ([]SourceSpan, error) {
	out := make([]SourceSpan, len(spans))
	for index, span := range spans {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out[index] = mutationSourceSpan(span)
	}
	return out, ctx.Err()
}

func mutationSourceSpan(span markdownowner.Span) SourceSpan {
	return SourceSpan{Start: span.Start, End: span.End}
}

func (o MarkdownMigrationOwnership) citationsSpan() SourceSpan {
	if o.CitationsSectionIndex < 0 || o.CitationsSectionIndex >= len(o.Sections) {
		return SourceSpan{}
	}
	return o.Sections[o.CitationsSectionIndex].Span
}

type legacyCitationMapping struct {
	LegacyNumber uint64
	LegacyEntry  string
	SourceID     string
	Title        string
	Resource     string

	callerLegacyEntry bool
	callerTitle       bool
	callerResource    bool
	linkTitle         string
	sourceSpan        SourceSpan
}

// expectedCitationProjection is the single deterministic projection used by
// both the legacy transition and target replay. Metadata may only come from
// caller input or one parser-owned legacy/source entry supplied by the caller
// of this builder.
type expectedCitationProjection struct {
	Mapping           legacyCitationMapping
	Definition        []byte
	DefinitionContent string
}

func buildExpectedCitationProjectionContext(
	ctx context.Context,
	mapping legacyCitationMapping,
	entry MarkdownCitationEntry,
	newline string,
) (expectedCitationProjection, error) {
	if err := ctx.Err(); err != nil {
		return expectedCitationProjection{}, err
	}
	validSourceID, err := validCitationSourceIDContext(ctx, mapping.SourceID)
	if err != nil {
		return expectedCitationProjection{}, err
	}
	if !validSourceID {
		return expectedCitationProjection{}, markdownPresentationErrorAt(
			"invalid_source_id",
			ErrUnsupportedPresentation,
			entry.Span,
		)
	}
	if mapping.Title == "" {
		mapping.Title = entry.Title
	} else if entry.Title != "" {
		equal, err := citationStringEqualContext(ctx, mapping.Title, entry.Title)
		if err != nil {
			return expectedCitationProjection{}, err
		}
		if !equal {
			return expectedCitationProjection{}, markdownPresentationErrorAt(
				"citation_title_mismatch",
				ErrUnsupportedPresentation,
				entry.ContentSpan,
			)
		}
	}
	if mapping.Resource == "" {
		mapping.Resource = entry.Resource
	} else if entry.Resource != "" {
		equal, err := citationStringEqualContext(ctx, mapping.Resource, entry.Resource)
		if err != nil {
			return expectedCitationProjection{}, err
		}
		if !equal {
			return expectedCitationProjection{}, markdownPresentationErrorAt(
				"citation_resource_mismatch",
				ErrUnsupportedPresentation,
				entry.ContentSpan,
			)
		}
	}
	if mapping.Resource == "" {
		return expectedCitationProjection{}, markdownPresentationErrorAt(
			"missing_citation_resource",
			ErrUnsupportedPresentation,
			entry.ContentSpan,
		)
	}
	mapping.linkTitle = entry.LinkTitle
	mapping.sourceSpan = entry.ContentSpan
	line, err := renderFootnoteDefinitionContext(ctx, mapping, entry, newline)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return expectedCitationProjection{}, err
		}
		return expectedCitationProjection{}, markdownPresentationErrorAt(
			"unsupported_footnote_definition",
			ErrUnsupportedPresentation,
			entry.ContentSpan,
		)
	}
	content, ok, err := renderedFootnoteContentContext(ctx, line, newline)
	if err != nil {
		return expectedCitationProjection{}, err
	}
	if !ok || content == "" {
		return expectedCitationProjection{}, markdownPresentationErrorAt(
			"unsupported_footnote_definition",
			ErrUnsupportedPresentation,
			entry.ContentSpan,
		)
	}
	return expectedCitationProjection{
		Mapping:           mapping,
		Definition:        line,
		DefinitionContent: content,
	}, nil
}

func rewriteLegacyCitationsContext(ctx context.Context, source []byte, mappings []legacyCitationMapping) ([]byte, error) {
	projection, err := markdownowner.CollectCitationSectionProjection(ctx, source)
	if err != nil {
		return nil, mutationMarkdownOwnerError(err)
	}
	if projection.Found && len(projection.Opaque) != 0 {
		spans, spanErr := mutationSourceSpansContext(ctx, projection.Opaque)
		if spanErr != nil {
			return nil, spanErr
		}
		return nil, markdownPresentationErrorAt(
			"opaque_citations_content",
			ErrUnsupportedPresentation,
			unionManySourceSpans(spans),
		)
	}
	if projection.Found && len(projection.Residual) != 0 {
		spans, spanErr := mutationSourceSpansContext(ctx, projection.Residual)
		if spanErr != nil {
			return nil, spanErr
		}
		return nil, markdownPresentationErrorAt(
			"unowned_citations_content",
			ErrUnsupportedPresentation,
			unionManySourceSpans(spans),
		)
	}
	ownership, err := CollectMarkdownMigrationOwnership(ctx, source)
	if err != nil {
		return nil, err
	}
	if ownership.CitationsSectionIndex < 0 {
		if len(mappings) == 0 && len(ownership.NumericMarkers) == 0 {
			return appendBytesContext(ctx, nil, source)
		}
		return nil, markdownPresentationErrorAt("missing_citations_section", ErrUnsupportedPresentation, SourceSpan{Start: 0, End: len(source)})
	}
	if !projection.Found || len(projection.Entries) != len(ownership.CitationEntries) {
		return nil, markdownPresentationErrorAt("citation_ownership_mismatch", ErrUnsupportedPresentation, ownership.citationsSpan())
	}
	resolved, err := resolveCitationMappingsContext(ctx, ownership.CitationEntries, mappings)
	if err != nil {
		return nil, err
	}
	if err := validateFootnoteIdentitiesContext(ctx, ownership, ownership.CitationEntries, resolved); err != nil {
		return nil, err
	}
	byNumber := make(map[uint64]legacyCitationMapping)
	for index, entry := range ownership.CitationEntries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		mapping := resolved[index]
		if entry.Number != 0 {
			byNumber[entry.Number] = mapping
		}
	}
	for _, marker := range ownership.NumericMarkers {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, ok := byNumber[marker.Number]; !ok {
			return nil, markdownPresentationErrorAt("unresolved_claim_marker", ErrUnsupportedPresentation, marker.Span)
		}
	}

	newline := markdownNewline(source)
	definitions := make([]byte, 0)
	existing := make(map[string]MarkdownFootnoteDefinition)
	for _, definition := range ownership.FootnoteDefinitions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		existing[definition.NormalizedLabel] = definition
	}
	type renderedDefinition struct {
		span    SourceSpan
		content string
	}
	seenIDs := make(map[string]renderedDefinition)
	for index, entry := range ownership.CitationEntries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		mapping := resolved[index]
		normalized, err := normalizeMarkdownLabelContext(ctx, mapping.SourceID)
		if err != nil {
			return nil, err
		}
		projected, err := buildExpectedCitationProjectionContext(ctx, mapping, entry, newline)
		if err != nil {
			return nil, err
		}
		line := projected.Definition
		wantContent := projected.DefinitionContent
		if previous, exists := seenIDs[normalized]; exists {
			equal, err := citationStringEqualContext(ctx, previous.content, wantContent)
			if err != nil {
				return nil, err
			}
			if !equal {
				return nil, markdownPresentationErrorAt(
					"reused_source_metadata_conflict",
					ErrAmbiguousPresentation,
					unionSourceSpans(previous.span, entry.Span),
				)
			}
			continue
		}
		seenIDs[normalized] = renderedDefinition{span: entry.Span, content: wantContent}
		if definition, exists := existing[normalized]; exists {
			equal, err := citationStringEqualContext(ctx, definition.Content, wantContent)
			if err != nil {
				return nil, err
			}
			if !equal {
				return nil, markdownPresentationErrorAt(
					"conflicting_footnote_definition",
					ErrAmbiguousPresentation,
					definition.Span,
				)
			}
			continue
		}
		definitions = append(definitions, line...)
	}
	if len(definitions) > 0 && !bytes.HasSuffix(definitions, []byte(newline+newline)) {
		definitions = append(definitions, newline...)
	}

	patches := make([]bytePatch, 0, len(ownership.NumericMarkers)+len(ownership.CitationEntries)+1)
	for _, marker := range ownership.NumericMarkers {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		mapping := byNumber[marker.Number]
		patches = append(patches, bytePatch{
			Start: marker.Span.Start,
			End:   marker.Span.End,
			Text:  []byte("[^" + mapping.SourceID + "]"),
		})
	}
	section := projection.Section
	// Only parser-proven ownership is removed. Opaque fenced/indented code,
	// HTML, comments, and future Markdown constructs inside the former section
	// remain byte-for-byte untouched.
	patches = append(patches, bytePatch{
		Start: section.HeadingSpan.Start,
		End:   section.HeadingSpan.End,
		Text:  definitions,
	})
	for _, entry := range projection.Entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		patches = append(patches, bytePatch{Start: entry.Span.Start, End: entry.Span.End})
	}
	out, err := applyBytePatchesContext(ctx, source, patches)
	if err != nil {
		return nil, err
	}
	after, err := CollectMarkdownMigrationOwnership(ctx, out)
	if err != nil {
		return nil, err
	}
	if after.CitationsSectionIndex >= 0 || len(after.NumericMarkers) != 0 {
		return nil, markdownPresentationErrorAt("citation_semantic_mismatch", ErrUnsupportedPresentation, bytePatchesSpan(patches))
	}
	return out, nil
}

func resolveCitationMappingsContext(ctx context.Context, entries []MarkdownCitationEntry, mappings []legacyCitationMapping) ([]legacyCitationMapping, error) {
	for _, mapping := range mappings {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var matching []SourceSpan
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			numberMatches := mapping.LegacyNumber == 0 || mapping.LegacyNumber == entry.Number
			entryMatches := mapping.LegacyEntry == ""
			if !entryMatches {
				equal, compareErr := citationStringEqualContext(ctx, mapping.LegacyEntry, entry.Raw)
				if compareErr != nil {
					return nil, compareErr
				}
				entryMatches = equal
			}
			hasIdentity := mapping.LegacyNumber != 0 || mapping.LegacyEntry != ""
			if hasIdentity && numberMatches && entryMatches {
				matching = append(matching, entry.Span)
			}
		}
		if len(matching) > 1 {
			return nil, markdownPresentationErrorAt(
				"ambiguous_citation_mapping",
				ErrAmbiguousPresentation,
				unionManySourceSpans(matching),
			)
		}
	}
	if len(entries) != len(mappings) {
		return nil, markdownPresentationErrorAt("incomplete_citation_mapping", ErrUnsupportedPresentation, citationEntriesSpan(entries))
	}
	out := make([]legacyCitationMapping, len(entries))
	used := make([]bool, len(mappings))
	type sourceMetadata struct {
		rawID    string
		title    string
		resource string
		span     SourceSpan
	}
	sourceMetadataByID := make(map[string]sourceMetadata, len(mappings))
	for entryIndex, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		match := -1
		for mappingIndex, mapping := range mappings {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if used[mappingIndex] {
				continue
			}
			numberMatches := mapping.LegacyNumber == 0 || mapping.LegacyNumber == entry.Number
			entryMatches := mapping.LegacyEntry == ""
			if !entryMatches {
				equal, compareErr := citationStringEqualContext(ctx, mapping.LegacyEntry, entry.Raw)
				if compareErr != nil {
					return nil, compareErr
				}
				entryMatches = equal
			}
			hasIdentity := mapping.LegacyNumber != 0 || mapping.LegacyEntry != ""
			if hasIdentity && numberMatches && entryMatches {
				if match >= 0 {
					return nil, markdownPresentationErrorAt("ambiguous_citation_mapping", ErrAmbiguousPresentation, entry.Span)
				}
				match = mappingIndex
			}
		}
		if match < 0 {
			return nil, markdownPresentationErrorAt("unresolved_citation_entry", ErrUnsupportedPresentation, entry.Span)
		}
		mapping := mappings[match]
		mapping.LegacyEntry = entry.Raw
		projected, err := buildExpectedCitationProjectionContext(ctx, mapping, entry, "\n")
		if err != nil {
			return nil, err
		}
		mapping = projected.Mapping
		normalizedID, err := normalizeMarkdownLabelContext(ctx, mapping.SourceID)
		if err != nil {
			return nil, err
		}
		if previous, reused := sourceMetadataByID[normalizedID]; reused {
			equalRawID, err := citationStringEqualContext(ctx, previous.rawID, mapping.SourceID)
			if err != nil {
				return nil, err
			}
			if !equalRawID {
				return nil, markdownPresentationErrorAt(
					"normalized_footnote_label_collision",
					ErrAmbiguousPresentation,
					unionSourceSpans(previous.span, entry.Span),
				)
			}
			equalTitle, err := citationStringEqualContext(ctx, previous.title, mapping.Title)
			if err != nil {
				return nil, err
			}
			equalResource, err := citationStringEqualContext(ctx, previous.resource, mapping.Resource)
			if err != nil {
				return nil, err
			}
			if !equalTitle || !equalResource {
				return nil, markdownPresentationErrorAt(
					"reused_source_metadata_conflict",
					ErrAmbiguousPresentation,
					unionSourceSpans(previous.span, entry.Span),
				)
			}
		}
		sourceMetadataByID[normalizedID] = sourceMetadata{
			rawID: mapping.SourceID, title: mapping.Title, resource: mapping.Resource, span: entry.Span,
		}
		used[match] = true
		out[entryIndex] = mapping
	}
	return out, nil
}

func citationEntriesSpan(entries []MarkdownCitationEntry) SourceSpan {
	if len(entries) == 0 {
		return SourceSpan{}
	}
	spans := make([]SourceSpan, len(entries))
	for index := range entries {
		spans[index] = entries[index].Span
	}
	return unionManySourceSpans(spans)
}

func citationContainsAnyByteContext(ctx context.Context, value, chars string) (bool, error) {
	for index := 0; index < len(value); index++ {
		if index&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		if strings.IndexByte(chars, value[index]) >= 0 {
			return true, nil
		}
	}
	return false, ctx.Err()
}

func citationValidUTF8Context(ctx context.Context, value string) (bool, error) {
	for offset := 0; offset < len(value); {
		if offset&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		decoded, size := utf8.DecodeRuneInString(value[offset:])
		if decoded == utf8.RuneError && size == 1 {
			return false, nil
		}
		offset += size
	}
	return true, ctx.Err()
}

func citationDefinitionCapacity(sourceID, title string, encoded []byte, linkTitle, newline string) (int, bool) {
	total := 2 + len(sourceID) // "[^" and the source identifier.
	add := func(length int) bool {
		if length < 0 || total > int(^uint(0)>>1)-length {
			return false
		}
		total += length
		return true
	}
	if !add(3) { // "]: "
		return 0, false
	}
	if title == "" && linkTitle == "" {
		if !add(len(encoded)) {
			return 0, false
		}
	} else {
		if !add(1) || !add(len(title)) || !add(2) || !add(len(encoded)) || !add(1) {
			return 0, false
		}
		if linkTitle != "" {
			// One separator, two quotes, and at most one escape byte per input byte.
			if len(linkTitle) > (int(^uint(0)>>1)-3)/2 || !add(3+2*len(linkTitle)) {
				return 0, false
			}
		}
	}
	if !add(len(newline)) {
		return 0, false
	}
	return total, true
}

func citationWriteStringContext(ctx context.Context, destination *bytes.Buffer, value string) error {
	const chunkSize = 64 << 10
	for offset := 0; offset < len(value); offset += chunkSize {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := min(offset+chunkSize, len(value))
		if _, err := destination.WriteString(value[offset:end]); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func citationWriteBytesContext(ctx context.Context, destination *bytes.Buffer, value []byte) error {
	const chunkSize = 64 << 10
	for offset := 0; offset < len(value); offset += chunkSize {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := min(offset+chunkSize, len(value))
		if _, err := destination.Write(value[offset:end]); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func renderFootnoteDefinitionContext(ctx context.Context, mapping legacyCitationMapping, entry MarkdownCitationEntry, newline string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	validSourceID, err := validCitationSourceIDContext(ctx, mapping.SourceID)
	if err != nil {
		return nil, err
	}
	if !validSourceID {
		return nil, errors.New("invalid source id")
	}
	resource := mapping.Resource
	if resource == "" {
		resource = entry.Resource
	}
	containsLineBreak, err := citationContainsAnyByteContext(ctx, resource, "\r\n")
	if err != nil {
		return nil, err
	}
	if containsLineBreak {
		return nil, errors.New("multiline resource")
	}
	encoded, err := encodeMarkdownDestinationContext(ctx, resource, false)
	if err != nil {
		return nil, err
	}
	title := mapping.Title
	if title == "" {
		title = entry.Title
	}
	var definition bytes.Buffer
	if total, ok := citationDefinitionCapacity(mapping.SourceID, title, encoded, entry.LinkTitle, newline); ok {
		definition.Grow(total)
	}
	if err := citationWriteStringContext(ctx, &definition, "[^"); err != nil {
		return nil, err
	}
	if err := citationWriteStringContext(ctx, &definition, mapping.SourceID); err != nil {
		return nil, err
	}
	if err := citationWriteStringContext(ctx, &definition, "]: "); err != nil {
		return nil, err
	}
	if title == "" && entry.LinkTitle == "" {
		if err := citationWriteStringContext(ctx, &definition, resource); err != nil {
			return nil, err
		}
	} else {
		unsupportedTitle, err := citationContainsAnyByteContext(ctx, title, "[]\r\n")
		if err != nil {
			return nil, err
		}
		if unsupportedTitle {
			return nil, errors.New("unsupported title")
		}
		if err := citationWriteStringContext(ctx, &definition, "["); err != nil {
			return nil, err
		}
		if err := citationWriteStringContext(ctx, &definition, title); err != nil {
			return nil, err
		}
		if err := citationWriteStringContext(ctx, &definition, "]("); err != nil {
			return nil, err
		}
		if err := citationWriteBytesContext(ctx, &definition, encoded); err != nil {
			return nil, err
		}
		if entry.LinkTitle != "" {
			linkTitle, err := encodeMarkdownLinkTitleContext(ctx, entry.LinkTitle)
			if err != nil {
				return nil, err
			}
			if err := citationWriteStringContext(ctx, &definition, " "); err != nil {
				return nil, err
			}
			if err := citationWriteStringContext(ctx, &definition, linkTitle); err != nil {
				return nil, err
			}
		}
		if err := citationWriteStringContext(ctx, &definition, ")"); err != nil {
			return nil, err
		}
	}
	if err := citationWriteStringContext(ctx, &definition, newline); err != nil {
		return nil, err
	}
	return definition.Bytes(), ctx.Err()
}

func validCitationSourceIDContext(ctx context.Context, value string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if value == "" {
		return false, nil
	}
	validUTF8, err := citationValidUTF8Context(ctx, value)
	if err != nil || !validUTF8 {
		return false, err
	}
	forbidden, err := citationContainsAnyByteContext(ctx, value, "[]\r\n")
	if err != nil || forbidden {
		return false, err
	}
	first, _ := utf8.DecodeRuneInString(value)
	last, _ := utf8.DecodeLastRuneInString(value)
	return !unicode.IsSpace(first) && !unicode.IsSpace(last), ctx.Err()
}

func encodeMarkdownLinkTitleContext(ctx context.Context, title string) (string, error) {
	invalid, err := citationContainsAnyByteContext(ctx, title, "\x00\r\n")
	if err != nil {
		return "", err
	}
	if invalid {
		return "", errors.New("multiline link title")
	}
	var encoded strings.Builder
	encoded.Grow(len(title) + 2)
	encoded.WriteByte('"')
	for offset, value := range title {
		if offset&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		if value == '\\' || value == '"' {
			encoded.WriteByte('\\')
		}
		encoded.WriteRune(value)
	}
	encoded.WriteByte('"')
	return encoded.String(), ctx.Err()
}

func validateFootnoteIdentitiesContext(
	ctx context.Context,
	ownership MarkdownMigrationOwnership,
	entries []MarkdownCitationEntry,
	mappings []legacyCitationMapping,
) error {
	type identity struct {
		raw  string
		span SourceSpan
	}
	byNormalized := make(map[string]identity, len(ownership.FootnoteReferences)+len(ownership.FootnoteDefinitions)+len(mappings))
	definitions := make(map[string]SourceSpan, len(ownership.FootnoteDefinitions))
	add := func(raw string, span SourceSpan, definition bool) error {
		normalized, err := normalizeMarkdownLabelContext(ctx, raw)
		if err != nil {
			return err
		}
		if previous, exists := byNormalized[normalized]; exists {
			equal, err := citationStringEqualContext(ctx, previous.raw, raw)
			if err != nil {
				return err
			}
			if !equal {
				return markdownPresentationErrorAt(
					"normalized_footnote_label_collision",
					ErrAmbiguousPresentation,
					unionSourceSpans(previous.span, span),
				)
			}
		}
		if definition {
			if previous, exists := definitions[normalized]; exists {
				return markdownPresentationErrorAt(
					"normalized_footnote_label_collision",
					ErrAmbiguousPresentation,
					unionSourceSpans(previous, span),
				)
			}
			definitions[normalized] = span
		}
		if _, exists := byNormalized[normalized]; !exists {
			byNormalized[normalized] = identity{raw: raw, span: span}
		}
		return nil
	}
	for _, reference := range ownership.FootnoteReferences {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := add(reference.Label, reference.Span, false); err != nil {
			return err
		}
	}
	for _, definition := range ownership.FootnoteDefinitions {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := add(definition.Label, definition.Span, true); err != nil {
			return err
		}
	}
	for index, mapping := range mappings {
		if err := ctx.Err(); err != nil {
			return err
		}
		span := SourceSpan{}
		if index < len(entries) {
			span = entries[index].Span
		}
		if err := add(mapping.SourceID, span, false); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func renderedFootnoteContentContext(ctx context.Context, line []byte, newline string) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	delimiter := bytes.Index(line, []byte("]: "))
	if delimiter < 0 {
		return "", false, ctx.Err()
	}
	start := delimiter + len("]: ")
	end := len(line)
	if newline != "" && bytes.HasSuffix(line[start:], []byte(newline)) {
		end -= len(newline)
	}
	if end < start {
		return "", false, ctx.Err()
	}
	return string(line[start:end]), true, ctx.Err()
}

func normalizeMarkdownLabelContext(ctx context.Context, label string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	normalized := markdownowner.NormalizeFootnoteLabel(label)
	return normalized, ctx.Err()
}

func citationStringEqualContext(ctx context.Context, left, right string) (bool, error) {
	return stringsEqualContext(ctx, left, right)
}

func markdownNewline(source []byte) string {
	if bytes.Contains(source, []byte("\r\n")) {
		return "\r\n"
	}
	return "\n"
}

func unionManySourceSpans(spans []SourceSpan) SourceSpan {
	if len(spans) == 0 {
		return SourceSpan{}
	}
	out := spans[0]
	for _, span := range spans[1:] {
		out = unionSourceSpans(out, span)
	}
	return out
}
