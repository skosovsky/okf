// Package bundle implements the Open Knowledge Format (OKF) v0.2 data model,
// document parsing, bundle traversal, semantic relation extraction, indexes,
// and log tooling.
//
// OKF represents knowledge as a directory of Markdown files with YAML
// frontmatter. Validation lives in package validator, and graph rendering lives
// in package graph.
package bundle

// OKFVersion is the OKF specification version implemented by this package.
const (
	// OKFVersion is the current and default OKF specification version.
	OKFVersion = "0.2"
	// LegacyOKFVersion is the legacy version accepted by the v0.2 read layer.
	LegacyOKFVersion = "0.1"
)
