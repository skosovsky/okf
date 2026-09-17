// Package markdownlimit owns the dependency-neutral Markdown parser resource
// boundary shared by internal parser owners and the public bundle contract.
package markdownlimit

import (
	"errors"
	"fmt"
)

const (
	MaxDocumentBytes = 16 << 20
	MaxBodyBytes     = MaxDocumentBytes
)

var ErrResourceLimit = errors.New("Markdown resource limit exceeded")

type Kind string

const (
	ResourceDocumentBytes Kind = "document_bytes"
	ResourceBodyBytes     Kind = "body_bytes"
)

type Error struct {
	Kind     Kind
	Limit    int
	Observed int
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s: %s limit %d (observed %d)", ErrResourceLimit, e.Kind, e.Limit, e.Observed)
}

func (e *Error) Unwrap() error { return ErrResourceLimit }

func NewError(kind Kind, observed int) error {
	limit := MaxDocumentBytes
	if kind == ResourceBodyBytes {
		limit = MaxBodyBytes
	}
	if observed > limit+1 {
		observed = limit + 1
	}
	return &Error{Kind: kind, Limit: limit, Observed: observed}
}
