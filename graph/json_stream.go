package graph

import (
	"context"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

type jsonProperty struct {
	key   string
	value any
}

type orderedJSONObject []jsonProperty

func (object *orderedJSONObject) setContext(ctx context.Context, key string, value any) error {
	for index := range *object {
		equal, err := equalGraphStringsContext(ctx, (*object)[index].key, key)
		if err != nil {
			return err
		}
		if equal {
			(*object)[index].value = value
			return checkGraphContext(ctx)
		}
	}
	*object = append(*object, jsonProperty{key: key, value: value})
	return checkGraphContext(ctx)
}

func (object orderedJSONObject) getContext(ctx context.Context, key string) (any, bool, error) {
	for _, property := range object {
		equal, err := equalGraphStringsContext(ctx, property.key, key)
		if err != nil {
			return nil, false, err
		}
		if equal {
			return property.value, true, checkGraphContext(ctx)
		}
	}
	return nil, false, checkGraphContext(ctx)
}

func writeOrderedJSONObjectContext(ctx context.Context, w io.Writer, object orderedJSONObject, depth int) error {
	if err := writeStringContext(ctx, w, "{"); err != nil {
		return err
	}
	if len(object) == 0 {
		return writeStringContext(ctx, w, "}")
	}
	properties := append(orderedJSONObject(nil), object...)
	if err := stableSortContext(ctx, properties, func(ctx context.Context, left, right jsonProperty) (bool, error) {
		comparison, err := compareGraphStringsContext(ctx, left.key, right.key)
		return comparison < 0, err
	}); err != nil {
		return err
	}
	for index, property := range properties {
		if index == 0 {
			if err := writeStringContext(ctx, w, "\n"); err != nil {
				return err
			}
		} else if err := writeStringContext(ctx, w, ",\n"); err != nil {
			return err
		}
		if err := writeJSONIndentContext(ctx, w, depth+1); err != nil {
			return err
		}
		if err := writeJSONStringContext(ctx, w, property.key); err != nil {
			return err
		}
		if err := writeStringContext(ctx, w, ": "); err != nil {
			return err
		}
		if err := writeJSONValueContext(ctx, w, property.value, depth+1); err != nil {
			return err
		}
	}
	if err := writeStringContext(ctx, w, "\n"); err != nil {
		return err
	}
	if err := writeJSONIndentContext(ctx, w, depth); err != nil {
		return err
	}
	return writeStringContext(ctx, w, "}")
}

func writeJSONLDDocumentContext(
	ctx context.Context,
	w io.Writer,
	jsonContext map[string]any,
	graph []map[string]any,
) error {
	if err := writeStringContext(ctx, w, "{\n  \"@context\": "); err != nil {
		return err
	}
	if err := writeJSONObjectContext(ctx, w, jsonContext, 1); err != nil {
		return err
	}
	if err := writeStringContext(ctx, w, ",\n  \"@graph\": "); err != nil {
		return err
	}
	if err := writeJSONMapArrayContext(ctx, w, graph, 1); err != nil {
		return err
	}
	return writeStringContext(ctx, w, "\n}\n")
}

func writeOrderedJSONLDDocumentContext(ctx context.Context, w io.Writer, jsonContext orderedJSONObject, graph []orderedJSONObject) error {
	root := orderedJSONObject{{key: "@context", value: jsonContext}, {key: "@graph", value: graph}}
	if err := writeOrderedJSONObjectContext(ctx, w, root, 0); err != nil {
		return err
	}
	return writeStringContext(ctx, w, "\n")
}

func writeJSONObjectContext(ctx context.Context, w io.Writer, object map[string]any, depth int) error {
	if err := writeStringContext(ctx, w, "{"); err != nil {
		return err
	}
	if len(object) == 0 {
		return writeStringContext(ctx, w, "}")
	}
	keys := make([]string, 0, len(object))
	keyIndex := 0
	for key := range object {
		if keyIndex%graphContextCheckInterval == 0 {
			if err := checkGraphContext(ctx); err != nil {
				return err
			}
		}
		keyIndex++
		keys = append(keys, key)
	}
	if err := sortGraphStringsContext(ctx, keys); err != nil {
		return err
	}
	for index, key := range keys {
		if index == 0 {
			if err := writeStringContext(ctx, w, "\n"); err != nil {
				return err
			}
		} else if err := writeStringContext(ctx, w, ",\n"); err != nil {
			return err
		}
		if err := writeJSONIndentContext(ctx, w, depth+1); err != nil {
			return err
		}
		if err := writeJSONStringContext(ctx, w, key); err != nil {
			return err
		}
		if err := writeStringContext(ctx, w, ": "); err != nil {
			return err
		}
		if err := writeJSONValueContext(ctx, w, object[key], depth+1); err != nil {
			return err
		}
	}
	if err := writeStringContext(ctx, w, "\n"); err != nil {
		return err
	}
	if err := writeJSONIndentContext(ctx, w, depth); err != nil {
		return err
	}
	return writeStringContext(ctx, w, "}")
}

func writeJSONMapArrayContext(ctx context.Context, w io.Writer, values []map[string]any, depth int) error {
	if err := writeStringContext(ctx, w, "["); err != nil {
		return err
	}
	if len(values) == 0 {
		return writeStringContext(ctx, w, "]")
	}
	for index, value := range values {
		if index == 0 {
			if err := writeStringContext(ctx, w, "\n"); err != nil {
				return err
			}
		} else if err := writeStringContext(ctx, w, ",\n"); err != nil {
			return err
		}
		if err := writeJSONIndentContext(ctx, w, depth+1); err != nil {
			return err
		}
		if err := writeJSONObjectContext(ctx, w, value, depth+1); err != nil {
			return err
		}
	}
	if err := writeStringContext(ctx, w, "\n"); err != nil {
		return err
	}
	if err := writeJSONIndentContext(ctx, w, depth); err != nil {
		return err
	}
	return writeStringContext(ctx, w, "]")
}

func writeJSONAnyArrayContext(ctx context.Context, w io.Writer, values []any, depth int) error {
	if err := writeStringContext(ctx, w, "["); err != nil {
		return err
	}
	if len(values) == 0 {
		return writeStringContext(ctx, w, "]")
	}
	for index, value := range values {
		if index == 0 {
			if err := writeStringContext(ctx, w, "\n"); err != nil {
				return err
			}
		} else if err := writeStringContext(ctx, w, ",\n"); err != nil {
			return err
		}
		if err := writeJSONIndentContext(ctx, w, depth+1); err != nil {
			return err
		}
		if err := writeJSONValueContext(ctx, w, value, depth+1); err != nil {
			return err
		}
	}
	if err := writeStringContext(ctx, w, "\n"); err != nil {
		return err
	}
	if err := writeJSONIndentContext(ctx, w, depth); err != nil {
		return err
	}
	return writeStringContext(ctx, w, "]")
}

func writeJSONStringArrayContext(ctx context.Context, w io.Writer, values []string, depth int) error {
	items := make([]any, len(values))
	for index, value := range values {
		if index%graphContextCheckInterval == 0 {
			if err := checkGraphContext(ctx); err != nil {
				return err
			}
		}
		items[index] = value
	}
	return writeJSONAnyArrayContext(ctx, w, items, depth)
}

func writeJSONReferenceArrayContext(ctx context.Context, w io.Writer, values []jsonldReference, depth int) error {
	if err := writeStringContext(ctx, w, "["); err != nil {
		return err
	}
	if len(values) == 0 {
		return writeStringContext(ctx, w, "]")
	}
	for index, value := range values {
		if index == 0 {
			if err := writeStringContext(ctx, w, "\n"); err != nil {
				return err
			}
		} else if err := writeStringContext(ctx, w, ",\n"); err != nil {
			return err
		}
		if err := writeJSONIndentContext(ctx, w, depth+1); err != nil {
			return err
		}
		// Struct field order intentionally matches encoding/json and the legacy
		// byte contract; map-key ordering would produce a different byte stream.
		if err := writeStringContext(ctx, w, "{\n"); err != nil {
			return err
		}
		if err := writeJSONIndentContext(ctx, w, depth+2); err != nil {
			return err
		}
		if err := writeStringContext(ctx, w, "\"@type\": "); err != nil {
			return err
		}
		if err := writeJSONStringContext(ctx, w, value.Kind); err != nil {
			return err
		}
		if err := writeStringContext(ctx, w, ",\n"); err != nil {
			return err
		}
		if err := writeJSONIndentContext(ctx, w, depth+2); err != nil {
			return err
		}
		if err := writeStringContext(ctx, w, "\"target\": "); err != nil {
			return err
		}
		if err := writeJSONStringContext(ctx, w, value.Target); err != nil {
			return err
		}
		if err := writeStringContext(ctx, w, ",\n"); err != nil {
			return err
		}
		if err := writeJSONIndentContext(ctx, w, depth+2); err != nil {
			return err
		}
		if err := writeStringContext(ctx, w, fmt.Sprintf("\"exists\": %t\n", value.Exists)); err != nil {
			return err
		}
		if err := writeJSONIndentContext(ctx, w, depth+1); err != nil {
			return err
		}
		if err := writeStringContext(ctx, w, "}"); err != nil {
			return err
		}
	}
	if err := writeStringContext(ctx, w, "\n"); err != nil {
		return err
	}
	if err := writeJSONIndentContext(ctx, w, depth); err != nil {
		return err
	}
	return writeStringContext(ctx, w, "]")
}

func writeJSONValueContext(ctx context.Context, w io.Writer, value any, depth int) error {
	switch typed := value.(type) {
	case string:
		return writeJSONStringContext(ctx, w, typed)
	case bool:
		if typed {
			return writeStringContext(ctx, w, "true")
		}
		return writeStringContext(ctx, w, "false")
	case map[string]any:
		return writeJSONObjectContext(ctx, w, typed, depth)
	case orderedJSONObject:
		return writeOrderedJSONObjectContext(ctx, w, typed, depth)
	case map[string]string:
		object := make(map[string]any, len(typed))
		index := 0
		for key, item := range typed {
			if index%graphContextCheckInterval == 0 {
				if err := checkGraphContext(ctx); err != nil {
					return err
				}
			}
			index++
			object[key] = item
		}
		return writeJSONObjectContext(ctx, w, object, depth)
	case []any:
		return writeJSONAnyArrayContext(ctx, w, typed, depth)
	case []string:
		return writeJSONStringArrayContext(ctx, w, typed, depth)
	case []map[string]any:
		return writeJSONMapArrayContext(ctx, w, typed, depth)
	case []orderedJSONObject:
		values := make([]any, len(typed))
		for index := range typed {
			values[index] = typed[index]
		}
		return writeJSONAnyArrayContext(ctx, w, values, depth)
	case []jsonldReference:
		return writeJSONReferenceArrayContext(ctx, w, typed, depth)
	case nil:
		return writeStringContext(ctx, w, "null")
	default:
		return fmt.Errorf("graph: unsupported JSON-LD value %T", value)
	}
}

func writeJSONIndentContext(ctx context.Context, w io.Writer, depth int) error {
	for remaining := depth * 2; remaining > 0; {
		chunk := min(remaining, graphContextCheckInterval)
		if err := writeStringContext(ctx, w, strings.Repeat(" ", chunk)); err != nil {
			return err
		}
		remaining -= chunk
	}
	return checkGraphContext(ctx)
}

func writeJSONStringContext(ctx context.Context, w io.Writer, value string) error {
	if err := writeStringContext(ctx, w, "\""); err != nil {
		return err
	}
	var buffer strings.Builder
	buffer.Grow(graphContextCheckInterval * 2)
	flush := func() error {
		if buffer.Len() == 0 {
			return checkGraphContext(ctx)
		}
		err := writeStringContext(ctx, w, buffer.String())
		buffer.Reset()
		return err
	}
	for offset := 0; offset < len(value); {
		if buffer.Len() >= graphContextCheckInterval {
			if err := flush(); err != nil {
				return err
			}
		}
		r, size := utf8.DecodeRuneInString(value[offset:])
		if r == utf8.RuneError && size == 1 {
			buffer.WriteRune(utf8.RuneError)
			offset++
			continue
		}
		offset += size
		switch r {
		case '\\', '"':
			buffer.WriteByte('\\')
			buffer.WriteRune(r)
		case '\b':
			buffer.WriteString(`\b`)
		case '\f':
			buffer.WriteString(`\f`)
		case '\n':
			buffer.WriteString(`\n`)
		case '\r':
			buffer.WriteString(`\r`)
		case '\t':
			buffer.WriteString(`\t`)
		case '<':
			buffer.WriteString(`\u003c`)
		case '>':
			buffer.WriteString(`\u003e`)
		case '&':
			buffer.WriteString(`\u0026`)
		case '\u2028':
			buffer.WriteString(`\u2028`)
		case '\u2029':
			buffer.WriteString(`\u2029`)
		default:
			if r < 0x20 {
				const hex = "0123456789abcdef"
				buffer.WriteString(`\u00`)
				buffer.WriteByte(hex[byte(r)>>4])
				buffer.WriteByte(hex[byte(r)&0x0f])
			} else {
				buffer.WriteRune(r)
			}
		}
	}
	if err := flush(); err != nil {
		return err
	}
	return writeStringContext(ctx, w, "\"")
}
