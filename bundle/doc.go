// Package bundle implements the Open Knowledge Format (OKF) v0.1 data model,
// document parsing, bundle traversal, semantic relation extraction, indexes,
// and log tooling.
//
// OKF represents knowledge as a directory of Markdown files with YAML
// frontmatter. Validation lives in package validator, and graph rendering lives
// in package graph.
package bundle

// OKFVersion is the OKF specification version implemented by this package.
const OKFVersion = "0.1"
