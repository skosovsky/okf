package mcpserver

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mark3labs/mcp-go/server"
	"github.com/skosovsky/okf/bundle"
)

func mustServerTools(t testing.TB) []server.ServerTool {
	t.Helper()
	tools, err := ServerTools()
	if err != nil {
		t.Fatalf("ServerTools() error = %v", err)
	}
	return tools
}

func mustContractSchema(t testing.TB, name string) json.RawMessage {
	t.Helper()
	schema, err := contractSchema(name)
	if err != nil {
		t.Fatalf("contractSchema(%q) error = %v", name, err)
	}
	return schema
}

func TestContractSchemaFromFSReturnsMissingAndInvalidErrors(t *testing.T) {
	tests := []struct {
		name      string
		files     fstest.MapFS
		contract  string
		wantError string
	}{
		{
			name:      "missing",
			files:     fstest.MapFS{},
			contract:  "missing",
			wantError: "read contract contracts/missing.schema.json",
		},
		{
			name: "invalid JSON",
			files: fstest.MapFS{
				"contracts/invalid.schema.json": {Data: []byte("{")},
			},
			contract:  "invalid",
			wantError: "invalid contract contracts/invalid.schema.json",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			files := test.files

			// Act.
			schema, err := contractSchemaFromFS(files, test.contract)

			// Assert.
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("contractSchemaFromFS() error = %v, want containing %q", err, test.wantError)
			}
			if schema != nil {
				t.Fatalf("contractSchemaFromFS() schema = %s, want nil", schema)
			}
		})
	}
}

func TestServerConstructionFailsClosedOnSchemaLoadError(t *testing.T) {
	// Arrange.
	sentinel := errors.New("schema unavailable")
	load := func(string) (json.RawMessage, error) {
		return nil, sentinel
	}

	// Act.
	mcpServer, err := newServerWithSchemaLoader(load)

	// Assert.
	if !errors.Is(err, sentinel) {
		t.Fatalf("newServerWithSchemaLoader() error = %v, want %v", err, sentinel)
	}
	if mcpServer != nil {
		t.Fatalf("newServerWithSchemaLoader() server = %#v, want nil", mcpServer)
	}
}

func TestNewServerSucceedsWithEmbeddedContracts(t *testing.T) {
	// Act.
	mcpServer, err := NewServer()

	// Assert.
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	if mcpServer == nil {
		t.Fatal("NewServer() returned nil server")
	}
}

func TestWriteConceptReturnsErrorWhenNoRetryAttemptIsAvailable(t *testing.T) {
	// Arrange.
	id, err := bundle.ParseConceptID("note")
	if err != nil {
		t.Fatalf("ParseConceptID() error = %v", err)
	}

	// Act.
	response, err := writeConceptCommitWithMaxAttempts(
		t.Context(),
		t.TempDir(),
		id,
		"type: Note\n",
		"Body.\n",
		0,
	)

	// Assert.
	if !errors.Is(err, errWriteConceptConflictRetriesExhausted) {
		t.Fatalf("writeConceptCommitWithMaxAttempts() error = %v, want %v", err, errWriteConceptConflictRetriesExhausted)
	}
	if response.Status != "" || response.Path != "" || response.Diagnostics != nil {
		t.Fatalf("writeConceptCommitWithMaxAttempts() response = %#v, want zero value", response)
	}
}
