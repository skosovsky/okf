// Package bundle implements the Open Knowledge Format (OKF) data model,
// document parsing, bundle traversal, semantic relation extraction, indexes,
// and log tooling.
//
// OKF represents knowledge as a directory of Markdown files with YAML
// frontmatter. Validation lives in package validator, and graph rendering lives
// in package graph.
package bundle

const (
	// OKFVersion identifies the historical 0.2 contract.
	OKFVersion = "0.2"
	// LegacyOKFVersion is the legacy version accepted by the v0.2 read layer.
	LegacyOKFVersion = "0.1"
)

// OKFVersion03 identifies the OKF 0.3 document contract.
const OKFVersion03 = "0.3"
