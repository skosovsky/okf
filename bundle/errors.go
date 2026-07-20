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

// ErrNotRegularFile marks a revision-visible path that is not a regular file.
// FileSystemSource never lists such paths and refuses to read them.
var ErrNotRegularFile = errors.New("not a regular file")

// ErrMissingFrontmatterKeys marks a document missing required frontmatter keys.
var ErrMissingFrontmatterKeys = errors.New("missing required frontmatter keys")

// ErrInvalidRelationRef marks a malformed or non-canonical semantic reference.
var ErrInvalidRelationRef = errors.New("invalid relation ref")

// ErrInvalidRelationType marks an invalid or reserved semantic relation type.
var ErrInvalidRelationType = errors.New("invalid relation type")

// ErrInvalidRelationFragment marks an invalid semantic resource fragment.
var ErrInvalidRelationFragment = errors.New("invalid relation fragment")
