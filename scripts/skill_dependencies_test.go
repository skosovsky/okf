package scripts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/skosovsky/okf/internal/mcpserver"
	"github.com/skosovsky/okf/internal/okfcli"
)

type skillDependencyManifest struct {
	MCPServer string `json:"mcpServer"`
	Skills    map[string]struct {
		MCPTools []string            `json:"mcpTools"`
		CLI      map[string][]string `json:"cli"`
	} `json:"skills"`
}

func TestSkillDependenciesMatchRuntime(t *testing.T) {
	// Arrange: load declared names; runtime catalog and CLI dispatch remain authoritative.
	root := repositoryRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "skill-dependencies.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest skillDependencyManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	tools, err := mcpserver.ServerTools()
	if err != nil {
		t.Fatal(err)
	}
	actual := make([]string, 0, len(tools))
	for _, tool := range tools {
		actual = append(actual, tool.Tool.Name)
	}
	programs := map[string]string{}
	for _, name := range []string{"okf-upkeep", "okf-backfill"} {
		binary := filepath.Join(t.TempDir(), name)
		cmd := exec.Command("go", "build", "-o", binary, "./cmd/"+name)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v: %s", name, err, output)
		}
		programs[name] = binary
	}
	// Act/Assert: each declared dependency resolves against a real runtime surface.
	if manifest.MCPServer != mcpserver.ServerName {
		t.Fatalf("unexpected server identity: %q", manifest.MCPServer)
	}
	for skill, dependencies := range manifest.Skills {
		if err := checkToolNames(dependencies.MCPTools, actual); err != nil {
			t.Errorf("%s: %v", skill, err)
		}
		for program, commands := range dependencies.CLI {
			if program != "okf" && programs[program] == "" {
				t.Errorf("%s: unknown CLI %q", skill, program)
				continue
			}
			for _, command := range commands {
				if program == "okf" {
					var stdout, stderr bytes.Buffer
					code := okfcli.Run([]string{command, "--okf-skill-probe-never-write"}, &stdout, &stderr)
					if code != 1 || !strings.Contains(stderr.String(), "unknown flag: --okf-skill-probe-never-write") || stdout.Len() != 0 {
						t.Errorf("%s: okf command %q did not reach safe runtime argument validation: code=%d stdout=%q stderr=%q", skill, command, code, stdout.String(), stderr.String())
					}
					continue
				}
				cmd := exec.Command(programs[program], command, "-h")
				output, _ := cmd.CombinedOutput() // flag -h may exit 1; the command-specific usage is the signal.
				if !bytes.Contains(output, []byte("Usage of "+command+":")) {
					t.Errorf("%s: missing %s command %q: %s", skill, program, command, output)
				}
			}
		}
	}
	primary := append([]string(nil), manifest.Skills["open-knowledge-format"].MCPTools...)
	slices.Sort(primary)
	slices.Sort(actual)
	if !slices.Equal(primary, actual) {
		t.Errorf("primary skill MCP catalog differs from runtime: manifest=%v runtime=%v", primary, actual)
	}
}

func TestSkillDependencyMissingToolFixture(t *testing.T) {
	// Arrange.
	available := []string{"read_concept"}
	declared := []string{"read_concept", "deleted_tool"}
	// Act.
	err := checkToolNames(declared, available)
	// Assert.
	if err == nil || !strings.Contains(err.Error(), "deleted_tool") {
		t.Fatalf("missing tool not detected: %v", err)
	}
}

func checkToolNames(declared, available []string) error {
	for _, name := range declared {
		if !slices.Contains(available, name) {
			return fmt.Errorf("missing MCP tool %q", name)
		}
	}
	return nil
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(file))
}
