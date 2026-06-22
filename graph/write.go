package graph

import (
	"fmt"
	"io"
)

func writeString(w io.Writer, text string) error {
	written, err := io.WriteString(w, text)
	if err != nil {
		return err
	}
	if written != len(text) {
		return io.ErrShortWrite
	}
	return nil
}

func writef(w io.Writer, format string, args ...any) error {
	return writeString(w, fmt.Sprintf(format, args...))
}

func writeln(w io.Writer, text string) error {
	return writeString(w, text+"\n")
}
