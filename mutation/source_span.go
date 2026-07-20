package mutation

// SourceSpan is a half-open byte range in the original presentation buffer.
// It deliberately uses bytes, not runes: a mutation copies every byte outside
// its owned spans unchanged.
type SourceSpan struct {
	Start int
	End   int
}

func (s SourceSpan) valid(size int) bool {
	return s.Start >= 0 && s.End >= s.Start && s.End <= size
}
