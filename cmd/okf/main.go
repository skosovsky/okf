package main

import (
	"io"
	"os"

	"github.com/skosovsky/okf/internal/okfcli"
)

func main() {
	exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

var exit = os.Exit

func run(args []string, stdout, stderr io.Writer) int {
	return okfcli.Run(args, stdout, stderr)
}
