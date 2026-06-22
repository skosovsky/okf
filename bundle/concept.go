package bundle

// Concept is one successfully loaded OKF concept document.
type Concept struct {
	ID       ConceptID
	Path     string
	Document Document
}

// NewConcept creates a concept model from its id, path, and document.
func NewConcept(id ConceptID, path string, document Document) Concept {
	return Concept{
		ID:       id,
		Path:     path,
		Document: document,
	}
}
