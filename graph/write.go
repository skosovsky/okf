package graph

import (
	"context"
	"fmt"
	"io"
	"strings"
)

const graphFormatChunkBytes = 64 << 10

func renderTransactionalContext(ctx context.Context, w io.Writer, render func(io.Writer) error) error {
	if err := checkGraphContext(ctx); err != nil {
		return err
	}
	var spool transactionalGraphSpool
	if err := render(&spool); err != nil {
		return err
	}
	if err := checkGraphContext(ctx); err != nil {
		return err
	}
	for _, segment := range spool.segments {
		if err := writeStringContext(ctx, w, segment); err != nil {
			return err
		}
	}
	return checkGraphContext(ctx)
}

type transactionalGraphSpool struct {
	segments []string
}

func (spool *transactionalGraphSpool) Write(data []byte) (int, error) {
	spool.segments = append(spool.segments, string(append([]byte(nil), data...)))
	return len(data), nil
}

func writeString(w io.Writer, text string) error {
	return writeStringContext(context.Background(), w, text)
}

func writef(w io.Writer, format string, args ...any) error {
	return writefContext(context.Background(), w, format, args...)
}

func writeln(w io.Writer, text string) error {
	return writelnContext(context.Background(), w, text)
}

func writefContext(ctx context.Context, w io.Writer, format string, args ...any) error {
	if err := checkGraphContext(ctx); err != nil {
		return err
	}
	argument := 0
	for len(format) > 0 {
		percent := strings.IndexByte(format, '%')
		if percent < 0 {
			return writeStringContext(ctx, w, format)
		}
		if percent > 0 {
			if err := writeStringContext(ctx, w, format[:percent]); err != nil {
				return err
			}
			format = format[percent:]
		}
		if strings.HasPrefix(format, "%%") {
			if err := writeStringContext(ctx, w, "%"); err != nil {
				return err
			}
			format = format[2:]
			continue
		}
		if argument >= len(args) || len(format) < 2 {
			return fmt.Errorf("graph: invalid internal format %q", format)
		}
		verb := format[1]
		value := args[argument]
		argument++
		var text string
		switch verb {
		case 's':
			var ok bool
			text, ok = value.(string)
			if !ok {
				return fmt.Errorf("graph: internal %%s argument has type %T", value)
			}
		case 'q':
			stringValue, ok := value.(string)
			if !ok {
				return fmt.Errorf("graph: internal %%q argument has type %T", value)
			}
			var err error
			text, err = quoteGraphStringContext(ctx, stringValue)
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("graph: unsupported internal format verb %%%c", verb)
		}
		if err := writeStringContext(ctx, w, text); err != nil {
			return err
		}
		format = format[2:]
	}
	if argument != len(args) {
		return fmt.Errorf("graph: unused internal format arguments")
	}
	return checkGraphContext(ctx)
}

func quoteGraphStringContext(ctx context.Context, value string) (string, error) {
	if err := checkGraphContext(ctx); err != nil {
		return "", err
	}
	var quoted strings.Builder
	quoted.Grow(len(value) + 2)
	quoted.WriteByte('"')
	for offset := 0; offset < len(value); {
		end := offset + graphFormatChunkBytes
		if end > len(value) {
			end = len(value)
		}
		for end < len(value) && end > offset && value[end]&0xc0 == 0x80 {
			end--
		}
		if end == offset {
			end = offset + 1
		}
		chunk := fmt.Sprintf("%q", value[offset:end])
		quoted.WriteString(chunk[1 : len(chunk)-1])
		offset = end
		if err := checkGraphContext(ctx); err != nil {
			return "", err
		}
	}
	quoted.WriteByte('"')
	if err := checkGraphContext(ctx); err != nil {
		return "", err
	}
	return quoted.String(), nil
}

func writelnContext(ctx context.Context, w io.Writer, text string) error {
	if err := writeStringContext(ctx, w, text); err != nil {
		return err
	}
	return writeStringContext(ctx, w, "\n")
}
