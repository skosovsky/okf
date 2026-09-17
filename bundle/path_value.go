package bundle

import (
	"context"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// PathValueKind classifies a v0.2 path-valued field without assuming a runtime.
type PathValueKind string

const (
	PathValueURL            PathValueKind = "url"
	PathValueBundleRelative PathValueKind = "bundle-relative"
	PathValueRelative       PathValueKind = "relative"
	PathValueScope          PathValueKind = "scope"
	PathValueAmbiguous      PathValueKind = "ambiguous"
)

// PathValueField identifies the contract field whose value is being resolved.
// Only sources[].resource may be a non-path scope descriptor.
type PathValueField string

const (
	PathFieldConceptResource  PathValueField = "concept-resource"
	PathFieldSourceResource   PathValueField = "source-resource"
	PathFieldComputation      PathValueField = "computation"
	PathFieldExecutorResource PathValueField = "executor-resource"
	PathFieldAttesterResource PathValueField = "attester-resource"
)

// ResolvedPathValue is a syntactically classified path value. Path is the
// normalized bundle-relative captured path for local values. Exists reports
// whether that path was captured in the immutable loaded bundle.
type ResolvedPathValue struct {
	Raw    string
	Path   string
	Suffix string
	Kind   PathValueKind
	Exists bool
}

// ConceptPathValueObservations preserves the retained frontmatter shape needed
// to interpret v0.2 path-valued fields without decoding the full contract.
// Values are emitted in field order: sources, computation, executor, attester.
type ConceptPathValueObservations struct {
	SourcesPresent  bool
	SourcesSequence bool
	Values          []ConceptPathValueObservation
}

// ConceptPathValueObservation is a value-only observation of one path-valued
// field. ParentPresent and ParentMapping describe sources items and the
// executor or attester container. They are false for the direct computation
// field, whose Scalar state carries its complete presence and shape.
type ConceptPathValueObservation struct {
	Field         PathValueField
	FieldPath     string
	ParentPresent bool
	ParentMapping bool
	Scalar        ScalarValueState
}

// ConceptPathValueObservationsContext observes v0.2 path-valued fields directly
// from retained frontmatter nodes. Unknown ids and nil bundles return a zero
// observation with found=false. Cancellation never returns partial output.
func (b *Bundle) ConceptPathValueObservationsContext(
	ctx context.Context,
	id ConceptID,
) (observations ConceptPathValueObservations, found bool, err error) {
	if err := ctx.Err(); err != nil {
		return ConceptPathValueObservations{}, false, err
	}
	if b == nil {
		return ConceptPathValueObservations{}, false, nil
	}
	idString, err := conceptIDStringContext(ctx, id)
	if err != nil {
		return ConceptPathValueObservations{}, false, err
	}
	index, found, err := lookupStringMapContext(ctx, b.byID, idString)
	if err != nil {
		return ConceptPathValueObservations{}, false, err
	}
	if !found {
		return ConceptPathValueObservations{}, false, nil
	}
	if err := ctx.Err(); err != nil {
		return ConceptPathValueObservations{}, false, err
	}

	root := b.concepts[index].Document.Frontmatter.mappingNode()
	resolver := newYAMLSemanticResolver(ctx)
	sources, sourcesPresent, err := semanticMappingNodeContext(resolver, &root, "sources")
	if err != nil {
		return ConceptPathValueObservations{}, false, err
	}
	observations.SourcesPresent = sourcesPresent
	if sourcesPresent && sources.Kind == yaml.SequenceNode {
		observations.SourcesSequence = true
		for sourceIndex, source := range sources.Content {
			if err := ctx.Err(); err != nil {
				return ConceptPathValueObservations{}, false, err
			}
			resolved, resolveErr := resolver.value(source)
			if resolveErr != nil {
				return ConceptPathValueObservations{}, false, resolveErr
			}
			sourceResolved := resolved.resolved
			if sourceResolved {
				source = resolved.node
			}
			parentMapping := sourceResolved && source != nil && source.Kind == yaml.MappingNode
			var scalar ScalarValueState
			if parentMapping {
				resource, resourcePresent, resourceErr := semanticMappingNodeContext(resolver, source, "resource")
				if resourceErr != nil {
					return ConceptPathValueObservations{}, false, resourceErr
				}
				scalar, resourceErr = strictStringNodeStateContext(ctx, resource, resourcePresent)
				if resourceErr != nil {
					return ConceptPathValueObservations{}, false, resourceErr
				}
			}
			if err := ctx.Err(); err != nil {
				return ConceptPathValueObservations{}, false, err
			}
			observations.Values = append(observations.Values, ConceptPathValueObservation{
				Field:         PathFieldSourceResource,
				FieldPath:     fmt.Sprintf("sources[%d].resource", sourceIndex),
				ParentPresent: true,
				ParentMapping: parentMapping,
				Scalar:        scalar,
			})
		}
	}

	if err := ctx.Err(); err != nil {
		return ConceptPathValueObservations{}, false, err
	}
	computation, computationPresent, err := semanticMappingNodeContext(resolver, &root, "computation")
	if err != nil {
		return ConceptPathValueObservations{}, false, err
	}
	computationState, err := strictStringNodeStateContext(ctx, computation, computationPresent)
	if err != nil {
		return ConceptPathValueObservations{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return ConceptPathValueObservations{}, false, err
	}
	observations.Values = append(observations.Values, ConceptPathValueObservation{
		Field:     PathFieldComputation,
		FieldPath: "computation",
		Scalar:    computationState,
	})

	for _, container := range []struct {
		key       string
		field     PathValueField
		fieldPath string
	}{
		{key: "executor", field: PathFieldExecutorResource, fieldPath: "executor.resource"},
		{key: "attester", field: PathFieldAttesterResource, fieldPath: "attester.resource"},
	} {
		if err := ctx.Err(); err != nil {
			return ConceptPathValueObservations{}, false, err
		}
		parent, parentPresent, parentErr := semanticMappingNodeContext(resolver, &root, container.key)
		if parentErr != nil {
			return ConceptPathValueObservations{}, false, parentErr
		}
		resolved, resolveErr := resolver.value(parent)
		if resolveErr != nil {
			return ConceptPathValueObservations{}, false, resolveErr
		}
		parentResolved := resolved.resolved
		if parentResolved {
			parent = resolved.node
		}
		parentMapping := parentPresent && parentResolved && parent != nil && parent.Kind == yaml.MappingNode
		var scalar ScalarValueState
		if parentMapping {
			resource, resourcePresent, resourceErr := semanticMappingNodeContext(resolver, parent, "resource")
			if resourceErr != nil {
				return ConceptPathValueObservations{}, false, resourceErr
			}
			scalar, resourceErr = strictStringNodeStateContext(ctx, resource, resourcePresent)
			if resourceErr != nil {
				return ConceptPathValueObservations{}, false, resourceErr
			}
		}
		if err := ctx.Err(); err != nil {
			return ConceptPathValueObservations{}, false, err
		}
		observations.Values = append(observations.Values, ConceptPathValueObservation{
			Field:         container.field,
			FieldPath:     container.fieldPath,
			ParentPresent: parentPresent,
			ParentMapping: parentMapping,
			Scalar:        scalar,
		})
	}
	if err := ctx.Err(); err != nil {
		return ConceptPathValueObservations{}, false, err
	}
	return observations, true, nil
}

// ResolvePathValueFor resolves a path according to its frontmatter field.
// Computation, executor, and attester resources are always path-valued, even
// when their filenames contain spaces. Source resources prefer an exact
// captured local match, but otherwise retain scope or ambiguous state instead
// of inventing an unsafe local path.
func (b *Bundle) ResolvePathValueFor(documentPath, value string, field PathValueField) (ResolvedPathValue, bool) {
	resolved, ok, _ := b.ResolvePathValueForContext(context.Background(), documentPath, value, field)
	return resolved, ok
}

// ResolvePathValueForContext resolves a path-valued field with cancellation
// during ownership of caller-controlled paths and before publication. The
// bounded parser/classifier preserves the exact compatibility semantics of
// ResolvePathValueFor.
func (b *Bundle) ResolvePathValueForContext(
	ctx context.Context,
	documentPath, value string,
	field PathValueField,
) (ResolvedPathValue, bool, error) {
	if err := ctx.Err(); err != nil {
		return ResolvedPathValue{}, false, err
	}
	ownedDocumentPath, err := stringFromStringContext(ctx, documentPath)
	if err != nil {
		return ResolvedPathValue{}, false, err
	}
	ownedValue, err := stringFromStringContext(ctx, value)
	if err != nil {
		return ResolvedPathValue{}, false, err
	}
	resolved, ok, err := b.resolvePathValueForOwnedContext(ctx, ownedDocumentPath, ownedValue, field)
	if err != nil {
		return ResolvedPathValue{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return ResolvedPathValue{}, false, err
	}
	if !ok {
		return ResolvedPathValue{}, false, nil
	}
	if resolved.Raw, err = stringFromStringContext(ctx, resolved.Raw); err != nil {
		return ResolvedPathValue{}, false, err
	}
	if resolved.Path, err = stringFromStringContext(ctx, resolved.Path); err != nil {
		return ResolvedPathValue{}, false, err
	}
	if resolved.Suffix, err = stringFromStringContext(ctx, resolved.Suffix); err != nil {
		return ResolvedPathValue{}, false, err
	}
	return resolved, true, ctx.Err()
}

func (b *Bundle) resolvePathValueForOwned(documentPath, value string, field PathValueField) (ResolvedPathValue, bool) {
	resolved, ok, _ := b.resolvePathValueForOwnedContext(context.Background(), documentPath, value, field)
	return resolved, ok
}

func (b *Bundle) resolvePathValueForOwnedContext(ctx context.Context, documentPath, value string, field PathValueField) (ResolvedPathValue, bool, error) {
	switch field {
	case PathFieldConceptResource,
		PathFieldSourceResource,
		PathFieldComputation,
		PathFieldExecutorResource,
		PathFieldAttesterResource:
	default:
		return ResolvedPathValue{}, false, ctx.Err()
	}
	raw := value
	var err error
	value, err = trimSpaceStringContext(ctx, value)
	if err != nil {
		return ResolvedPathValue{}, false, err
	}
	if value == "" {
		return ResolvedPathValue{}, false, nil
	}
	pathValue, suffix, err := splitPathValueSuffixContext(ctx, value)
	if err != nil {
		return ResolvedPathValue{}, false, err
	}
	if pathValue == "" {
		return ResolvedPathValue{Raw: raw, Suffix: suffix, Kind: PathValueAmbiguous}, true, ctx.Err()
	}

	// Captured local identity wins before URL syntax. POSIX filenames may
	// contain literal percent bytes which url.Parse would reject as malformed
	// escapes; they remain ordinary bundle paths when captured.
	local, err := b.resolveLocalPathValueContext(ctx, documentPath, raw, pathValue, suffix)
	if err != nil {
		return ResolvedPathValue{}, false, err
	}
	if local.Exists {
		return local, true, ctx.Err()
	}
	windowsPath, err := looksLikeWindowsPathContext(ctx, pathValue)
	if err != nil {
		return ResolvedPathValue{}, false, err
	}
	if windowsPath {
		return ResolvedPathValue{Raw: raw, Suffix: suffix, Kind: PathValueAmbiguous}, true, ctx.Err()
	}
	urlLike, err := looksURLLikeContext(ctx, pathValue)
	if err != nil {
		return ResolvedPathValue{}, false, err
	}
	malformed, err := hasMalformedPercentEscapeContext(ctx, value)
	if err != nil {
		return ResolvedPathValue{}, false, err
	}
	if urlLike && malformed {
		return ResolvedPathValue{Raw: raw, Suffix: suffix, Kind: PathValueAmbiguous}, true, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return ResolvedPathValue{}, false, err
	}
	invalidURL, err := invalidURLCharactersContext(ctx, value)
	if err != nil {
		return ResolvedPathValue{}, false, err
	}
	if invalidURL {
		if urlLike {
			return ResolvedPathValue{Raw: raw, Suffix: suffix, Kind: PathValueAmbiguous}, true, ctx.Err()
		}
		classified, classifyErr := classifyUncapturedLocalPathContext(ctx, field, pathValue, local, raw, suffix)
		return classified, true, classifyErr
	}
	explicitURL, err := isExplicitURLContext(ctx, value, field == PathFieldSourceResource)
	if err != nil {
		return ResolvedPathValue{}, false, err
	}
	if explicitURL {
		return ResolvedPathValue{Raw: raw, Suffix: suffix, Kind: PathValueURL}, true, ctx.Err()
	}

	classified, err := classifyUncapturedLocalPathContext(ctx, field, pathValue, local, raw, suffix)
	return classified, true, err
}

func classifyUncapturedLocalPath(field PathValueField, pathValue string, local ResolvedPathValue, raw, suffix string) ResolvedPathValue {
	result, _ := classifyUncapturedLocalPathContext(context.Background(), field, pathValue, local, raw, suffix)
	return result
}

func classifyUncapturedLocalPathContext(ctx context.Context, field PathValueField, pathValue string, local ResolvedPathValue, raw, suffix string) (ResolvedPathValue, error) {
	if field != PathFieldSourceResource {
		return local, ctx.Err()
	}
	if strings.HasPrefix(pathValue, "/") || strings.HasPrefix(pathValue, "./") || strings.HasPrefix(pathValue, "../") {
		return local, ctx.Err()
	}
	scope, err := looksLikeScopeDescriptorContext(ctx, pathValue)
	if err != nil {
		return ResolvedPathValue{}, err
	}
	if scope {
		return ResolvedPathValue{Raw: raw, Suffix: suffix, Kind: PathValueScope}, ctx.Err()
	}
	return ResolvedPathValue{Raw: raw, Suffix: suffix, Kind: PathValueAmbiguous}, ctx.Err()
}

func (b *Bundle) resolveLocalPathValue(documentPath, raw, value, suffix string) ResolvedPathValue {
	resolved, _ := b.resolveLocalPathValueContext(context.Background(), documentPath, raw, value, suffix)
	return resolved
}

func (b *Bundle) resolveLocalPathValueContext(ctx context.Context, documentPath, raw, value, suffix string) (ResolvedPathValue, error) {
	if err := ctx.Err(); err != nil {
		return ResolvedPathValue{}, err
	}
	kind := PathValueRelative
	local := value
	if strings.HasPrefix(local, "/") {
		kind = PathValueBundleRelative
		first := 0
		for first < len(local) && local[first] == '/' {
			if first%(64<<10) == 0 {
				if err := ctx.Err(); err != nil {
					return ResolvedPathValue{}, err
				}
			}
			first++
		}
		local = local[first:]
	} else {
		if err := ctx.Err(); err != nil {
			return ResolvedPathValue{}, err
		}
		var err error
		documentPath, err = b.bundleRelativePathContext(ctx, documentPath)
		if err != nil {
			return ResolvedPathValue{}, err
		}
		directory := documentPath
		for index := len(directory) - 1; index >= 0; index-- {
			if (len(directory)-index)%(64<<10) == 0 && ctx.Err() != nil {
				return ResolvedPathValue{}, ctx.Err()
			}
			if directory[index] == '/' {
				directory = directory[:index]
				break
			}
			if index == 0 {
				directory = ""
			}
		}
		local, err = joinAndCleanPathContext(ctx, directory, local)
		if err != nil {
			return ResolvedPathValue{}, err
		}
	}
	var err error
	local, err = cleanSlashPathContext(ctx, local)
	if err != nil {
		return ResolvedPathValue{}, err
	}
	if local == "." || local == ".." || strings.HasPrefix(local, "../") || strings.HasPrefix(local, "/") {
		return ResolvedPathValue{Raw: raw, Suffix: suffix, Kind: kind}, ctx.Err()
	}
	exists := false
	if b != nil {
		_, found, err := lookupStringMapContext(ctx, b.contents, local)
		if err != nil {
			return ResolvedPathValue{}, err
		}
		exists = found
	}
	return ResolvedPathValue{Raw: raw, Path: local, Suffix: suffix, Kind: kind, Exists: exists}, ctx.Err()
}

func splitPathValueSuffixContext(ctx context.Context, value string) (string, string, error) {
	for index := 0; index < len(value); index++ {
		if index%(64<<10) == 0 {
			if err := ctx.Err(); err != nil {
				return "", "", err
			}
		}
		if value[index] == '?' || value[index] == '#' {
			return value[:index], value[index:], ctx.Err()
		}
	}
	return value, "", ctx.Err()
}

func splitPathValueSuffix(value string) (string, string) {
	index := strings.IndexAny(value, "?#")
	if index < 0 {
		return value, ""
	}
	return value[:index], value[index:]
}

func (b *Bundle) bundleRelativePath(filename string) string {
	value, _ := b.bundleRelativePathContext(context.Background(), filename)
	return value
}

func (b *Bundle) bundleRelativePathContext(ctx context.Context, filename string) (string, error) {
	cleaned, err := cleanSlashPathContext(ctx, filename)
	if err != nil {
		return "", err
	}
	if b == nil || b.root == "" {
		return cleaned, ctx.Err()
	}
	root, err := cleanSlashPathContext(ctx, b.root)
	if err != nil {
		return "", err
	}
	if cleaned == root {
		return ".", ctx.Err()
	}
	prefix, err := appendStringContext(ctx, root, "/")
	if err != nil {
		return "", err
	}
	equal, err := prefixStringContext(ctx, cleaned, prefix)
	if err != nil {
		return "", err
	}
	if equal {
		return cleaned[len(prefix):], ctx.Err()
	}
	return cleaned, ctx.Err()
}

func joinAndCleanPathContext(ctx context.Context, directory, value string) (string, error) {
	if directory == "" {
		return cleanSlashPathContext(ctx, value)
	}
	joined, err := appendStringContext(ctx, directory, "/", value)
	if err != nil {
		return "", err
	}
	return cleanSlashPathContext(ctx, joined)
}

func appendStringContext(ctx context.Context, values ...string) (string, error) {
	var out strings.Builder
	for _, value := range values {
		for offset := 0; offset < len(value); offset += 64 << 10 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			end := min(offset+(64<<10), len(value))
			out.WriteString(value[offset:end])
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return out.String(), nil
}

func cleanSlashPathContext(ctx context.Context, value string) (string, error) {
	absolute := strings.HasPrefix(value, "/")
	segments := make([]string, 0, 16)
	for start := 0; start <= len(value); {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		end := start
		for end < len(value) && value[end] != '/' {
			if (end-start)%(64<<10) == 0 && ctx.Err() != nil {
				return "", ctx.Err()
			}
			end++
		}
		segment := value[start:end]
		switch segment {
		case "", ".":
		case "..":
			if len(segments) > 0 && segments[len(segments)-1] != ".." {
				segments = segments[:len(segments)-1]
			} else if !absolute {
				segments = append(segments, segment)
			}
		default:
			owned, err := stringFromStringContext(ctx, segment)
			if err != nil {
				return "", err
			}
			segments = append(segments, owned)
		}
		if end == len(value) {
			break
		}
		start = end + 1
	}
	var joinedBuilder strings.Builder
	if absolute {
		joinedBuilder.WriteByte('/')
	}
	for index, segment := range segments {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if index > 0 {
			joinedBuilder.WriteByte('/')
		}
		for offset := 0; offset < len(segment); offset += 64 << 10 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			end := min(offset+(64<<10), len(segment))
			joinedBuilder.WriteString(segment[offset:end])
		}
	}
	if joinedBuilder.Len() == 0 {
		joinedBuilder.WriteByte('.')
	}
	joined := joinedBuilder.String()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return joined, nil
}

func prefixStringContext(ctx context.Context, value, prefix string) (bool, error) {
	if len(value) < len(prefix) {
		return false, ctx.Err()
	}
	for offset := 0; offset < len(prefix); offset += 64 << 10 {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		end := min(offset+(64<<10), len(prefix))
		if value[offset:end] != prefix[offset:end] {
			return false, nil
		}
	}
	return true, ctx.Err()
}

func looksLikeScopeDescriptor(value string) bool {
	result, _ := looksLikeScopeDescriptorContext(context.Background(), value)
	return result
}

func looksLikeScopeDescriptorContext(ctx context.Context, value string) (bool, error) {
	if strings.HasPrefix(value, "./") || strings.HasPrefix(value, "../") || strings.HasPrefix(value, "/") {
		return false, ctx.Err()
	}
	space := false
	slash := false
	dotAfterSlash := false
	for index := range value {
		if index%(64<<10) == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		switch value[index] {
		case ' ', '\t', '\r', '\n':
			space = true
		case '/':
			slash = true
		case '.':
			dotAfterSlash = true
		}
	}
	return space && !slash && !dotAfterSlash, ctx.Err()
}

func looksLikeWindowsPath(value string) bool {
	result, _ := looksLikeWindowsPathContext(context.Background(), value)
	return result
}

func looksLikeWindowsPathContext(ctx context.Context, value string) (bool, error) {
	for index := range value {
		if index%(64<<10) == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		if value[index] == '\\' {
			return true, ctx.Err()
		}
	}
	return len(value) >= 2 &&
		(value[0] >= 'A' && value[0] <= 'Z' || value[0] >= 'a' && value[0] <= 'z') &&
		value[1] == ':' &&
		(len(value) == 2 || value[2] == '/' || value[2] == '\\'), ctx.Err()
}

func looksURLLike(value string) bool {
	result, _ := looksURLLikeContext(context.Background(), value)
	return result
}

func looksURLLikeContext(ctx context.Context, value string) (bool, error) {
	if strings.HasPrefix(value, "//") {
		return true, ctx.Err()
	}
	colon := -1
	for index := range value {
		if index%(64<<10) == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		if value[index] == ':' {
			colon = index
			break
		}
	}
	if colon <= 0 {
		return false, ctx.Err()
	}
	for index := 0; index < colon; index++ {
		character := value[index]
		if index == 0 {
			if character < 'A' || character > 'Z' && character < 'a' || character > 'z' {
				return false, ctx.Err()
			}
			continue
		}
		if character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' || character == '+' || character == '-' || character == '.' {
			continue
		}
		return false, ctx.Err()
	}
	return true, ctx.Err()
}

func hasMalformedPercentEscape(value string) bool {
	result, _ := hasMalformedPercentEscapeContext(context.Background(), value)
	return result
}

func hasMalformedPercentEscapeContext(ctx context.Context, value string) (bool, error) {
	for index := 0; index < len(value); index++ {
		if index%(64<<10) == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		if value[index] != '%' {
			continue
		}
		if index+2 >= len(value) || !isHexDigit(value[index+1]) || !isHexDigit(value[index+2]) {
			return true, ctx.Err()
		}
		index += 2
	}
	return false, ctx.Err()
}

func isHexDigit(value byte) bool {
	return value >= '0' && value <= '9' ||
		value >= 'A' && value <= 'F' ||
		value >= 'a' && value <= 'f'
}

func isExplicitURL(value string, sourceResource bool) bool {
	result, _ := isExplicitURLContext(context.Background(), value, sourceResource)
	return result
}

func isExplicitURLContext(ctx context.Context, value string, sourceResource bool) (bool, error) {
	urlLike, err := looksURLLikeContext(ctx, value)
	if err != nil {
		return false, err
	}
	if !urlLike && !strings.HasPrefix(value, "//") {
		return false, nil
	}
	if !sourceResource {
		return true, nil
	}
	containsSchemeSeparator := false
	for index := 0; index+2 < len(value); index++ {
		if index%(64<<10) == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		if value[index:index+3] == "://" {
			containsSchemeSeparator = true
			break
		}
	}
	lowerPrefix := func(prefix string) bool {
		return len(value) >= len(prefix) && strings.EqualFold(value[:len(prefix)], prefix)
	}
	return strings.HasPrefix(value, "//") || containsSchemeSeparator ||
		lowerPrefix("mailto:") || lowerPrefix("tel:") || lowerPrefix("data:") || lowerPrefix("urn:"), ctx.Err()
}

func invalidURLCharactersContext(ctx context.Context, value string) (bool, error) {
	for index := range value {
		if index%(64<<10) == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		if value[index] < 0x20 || value[index] == 0x7f {
			return true, nil
		}
	}
	return false, ctx.Err()
}
