package agent

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/skosovsky/okf/internal/okfcli"
)

func TestManualWriterConsumerBaseline(t *testing.T) {
	// Arrange: the manually authored treatment is the writer's published bundle.
	f, err := os.Open("corpus/writer_consumer_baseline.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cases, _, err := LoadCorpus(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 1 {
		t.Fatalf("want one frozen case, got %d", len(cases))
	}
	root := t.TempDir()
	for _, artifact := range cases[0].Treatment {
		path := filepath.Join(root, artifact.Path)
		if err := os.WriteFile(path, []byte(artifact.Content), 0600); err != nil {
			t.Fatal(err)
		}
	}

	// Act: validate the exact bundle bytes that the independent consumer will see.
	var stdout, stderr bytes.Buffer
	code := okfcli.Run([]string{"validate", "--path", root, "--json"}, &stdout, &stderr)

	// Assert: publication and syntax are preconditions, not consumer accuracy.
	if code != 0 {
		t.Fatalf("Go CLI rejected manually authored bundle: exit %d, stdout %s, stderr %s", code, stdout.String(), stderr.String())
	}
	if cases[0].Expected.Answer != "B" || len(cases[0].Expected.Evidence) != 1 || cases[0].Expected.Evidence[0] != "decision" {
		t.Fatalf("unexpected frozen gold answer: %+v", cases[0].Expected)
	}
}
