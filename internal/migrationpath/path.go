// Package migrationpath owns the path classifier shared by v0.1 -> v0.2
// source probing, request validation, planning, proof binding, and replay.
package migrationpath

import (
	"strings"

	"github.com/skosovsky/okf/bundle"
)

func IsMarkdownDocument(path string) bool {
	return bundle.ValidateRevisionPath(path) == nil && strings.HasSuffix(path, ".md")
}
