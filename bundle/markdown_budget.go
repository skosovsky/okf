package bundle

import "github.com/skosovsky/okf/internal/markdownlimit"

const (
	// MaxMarkdownDocumentBytes is the maximum UTF-8 Markdown document accepted
	// before an atomic parser boundary.
	MaxMarkdownDocumentBytes = markdownlimit.MaxDocumentBytes
	// MaxMarkdownBodyBytes is the corresponding limit for an already separated
	// Markdown body.
	MaxMarkdownBodyBytes = markdownlimit.MaxBodyBytes
)

// ErrMarkdownResourceLimit marks Markdown input that exceeds a public parser
// resource boundary.
var ErrMarkdownResourceLimit = markdownlimit.ErrResourceLimit

type MarkdownResourceLimitKind = markdownlimit.Kind

const (
	MarkdownResourceDocumentBytes = markdownlimit.ResourceDocumentBytes
	MarkdownResourceBodyBytes     = markdownlimit.ResourceBodyBytes
)

// MarkdownResourceLimitError reports the first public Markdown resource
// boundary crossed. Observed is saturated at Limit+1.
type MarkdownResourceLimitError = markdownlimit.Error
