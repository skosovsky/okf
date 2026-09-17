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

// ErrUnsupportedVersionSelector marks an explicit selector the read layer
// cannot assert.
var ErrUnsupportedVersionSelector = errors.New("unsupported OKF version selector")

// ErrVersionConflict marks an explicit selector that conflicts with the
// version declared by the bundle.
var ErrVersionConflict = errors.New("OKF version selector conflicts with declaration")

// ErrInvalidVersionDeclaration marks a non-canonical <major>.<minor> value.
var ErrInvalidVersionDeclaration = errors.New("invalid OKF version declaration")

// ErrInvalidYAMLUint64 marks an invalid, negative, or overflowing YAML integer
// for a non-negative uint64 domain field.
var ErrInvalidYAMLUint64 = errors.New("invalid non-negative YAML uint64")

// ErrUnknownSemanticFamily marks a request outside the restricted standard
// family observation surface.
var ErrUnknownSemanticFamily = errors.New("unknown semantic family")

// ErrDocumentConflict marks a stale, concurrently replaced, closed, or
// already-consumed path-scoped document session. Callers must open a fresh
// session instead of retrying publication from an obsolete capture.
var ErrDocumentConflict = errors.New("document rewrite conflict")

// ErrPublicationCommitted marks a publication error returned after the exact
// document or complete index batch was durably installed and passed its commit
// validation. Cleanup may be pending; callers must not blindly retry the write
// and may reopen the bundle to converge retained transaction evidence.
var ErrPublicationCommitted = errors.New("publication committed")
