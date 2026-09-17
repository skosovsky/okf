package main

import (
	"fmt"
	"os"

	"github.com/mark3labs/mcp-go/server"
	"github.com/skosovsky/okf/internal/mcpserver"
)

func main() {
	mcpServer, err := mcpserver.NewServer()
	if err == nil {
		err = server.ServeStdio(mcpServer)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "okf-mcp: %v\n", err)
		os.Exit(1)
	}
}
