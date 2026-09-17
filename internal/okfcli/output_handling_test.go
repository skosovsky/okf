package okfcli

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestTextCommandsPropagateFailingAndShortStdout(t *testing.T) {
	commands := []struct {
		name string
		args func(*testing.T) []string
	}{
		{name: "help", args: func(*testing.T) []string { return []string{"help"} }},
		{name: "version", args: func(*testing.T) []string { return []string{"version"} }},
		{name: "validate", args: func(t *testing.T) []string {
			root, _ := makeTextWriterBundle(t, "0.2")
			return []string{"validate", "--path", root}
		}},
		{name: "info", args: func(t *testing.T) []string {
			root, _ := makeTextWriterBundle(t, "0.2")
			return []string{"info", root}
		}},
		{name: "index", args: func(t *testing.T) []string {
			root, _ := makeTextWriterBundle(t, "0.2")
			return []string{"index", root}
		}},
		{name: "graph", args: func(t *testing.T) []string {
			root, _ := makeTextWriterBundle(t, "0.2")
			return []string{"graph", root}
		}},
		{name: "parse", args: func(t *testing.T) []string {
			_, note := makeTextWriterBundle(t, "0.2")
			return []string{"parse", note}
		}},
		{name: "fmt", args: func(t *testing.T) []string {
			_, note := makeTextWriterBundle(t, "0.2")
			return []string{"fmt", note}
		}},
		{name: "migrate", args: func(t *testing.T) []string {
			root, _ := makeTextWriterBundle(t, "0.1")
			return []string{"migrate", root, "--to", "0.2", "--actor", "human:test"}
		}},
	}
	writerModes := []struct {
		name  string
		short bool
	}{
		{name: "error"},
		{name: "short", short: true},
	}

	for _, command := range commands {
		for _, mode := range writerModes {
			t.Run(command.name+"/"+mode.name, func(t *testing.T) {
				// Arrange.
				args := command.args(t)
				writer := &scriptedWriter{short: mode.short, err: io.ErrClosedPipe}
				var stderr bytes.Buffer

				// Act.
				code := Run(args, writer, &stderr)

				// Assert.
				if code != 1 || !strings.Contains(stderr.String(), "write stdout") {
					t.Fatalf("Run(%v) code/stderr = %d/%q, want 1/write stdout", args, code, stderr.String())
				}
			})
		}
	}
}
