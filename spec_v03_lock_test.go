package okf_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

func TestPinnedOKF03Specification(t *testing.T) {
	// Arrange.
	lockBytes, err := os.ReadFile("fixtures/v03/spec-lock.json")
	if err != nil {
		t.Fatal(err)
	}
	var lock struct {
		Revision string
		SHA256   string
	}
	if err := json.Unmarshal(lockBytes, &lock); err != nil {
		t.Fatal(err)
	}
	spec, err := os.ReadFile("fixtures/v03/SPEC.md")
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	digest := sha256.Sum256(spec)

	// Assert.
	const revision = "527e81aff55992be21507e97004286789126f88c"
	const want = "a1fcc51be23fce5c1e4e198f0a71af6046a5286503959caa4009311f74fac3f1"
	if got := hex.EncodeToString(digest[:]); lock.Revision != revision || lock.SHA256 != want || got != want {
		t.Fatalf("spec revision %s, lock digest %s, file digest %s; want %s and %s", lock.Revision, lock.SHA256, got, revision, want)
	}
}
