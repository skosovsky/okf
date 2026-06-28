package main

import (
	"fmt"
	"os"

	"github.com/mark3labs/mcp-go/server"
	"github.com/skosovsky/okf/internal/mcpserver"
)

func main() {
	if err := server.ServeStdio(mcpserver.NewServer()); err != nil {
		fmt.Fprintf(os.Stderr, "okf-mcp: %v\n", err)
		os.Exit(1)
	}
}
