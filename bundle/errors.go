package bundle

import "errors"

// ErrUnterminatedFrontmatter marks an opened frontmatter block with no closing delimiter.
var ErrUnterminatedFrontmatter = errors.New("unterminated frontmatter")

// ErrInvalidConceptID marks an invalid OKF concept identifier.
var ErrInvalidConceptID = errors.New("invalid concept id")

// ErrInvalidFrontmatter marks a malformed frontmatter model.
var ErrInvalidFrontmatter = errors.New("invalid frontmatter")

// ErrInvalidEncoding marks a markdown file that is not valid UTF-8.
var ErrInvalidEncoding = errors.New("invalid encoding")

// ErrNotDirectory marks a bundle root that is not a directory.
var ErrNotDirectory = errors.New("not a directory")

// ErrMissingFrontmatterKeys marks a document missing required frontmatter keys.
var ErrMissingFrontmatterKeys = errors.New("missing required frontmatter keys")
