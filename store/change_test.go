package store

import (
	"errors"
	"testing"

	"github.com/skosovsky/okf/bundle"
)

func testRevision(t *testing.T) Revision {
	t.Helper()
	r, err := RevisionFromManifest([]ManifestEntry{{Path: "a.md", Content: []byte("a")}})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func testRef(t *testing.T, raw, fragment string) bundle.RelationRef {
	t.Helper()
	id, err := bundle.ParseConceptID(raw)
	if err != nil {
		t.Fatal(err)
	}
	return bundle.RelationRef{ID: id, Fragment: fragment}
}

func TestChangeSetCanonicalBytes_AreDeterministic(t *testing.T) {
	// Arrange.
	from, to := testRef(t, "a", "source"), testRef(t, "b", "target")
	change := ChangeSet{Version: ChangeSetFormatVersion, ID: "cs-1", Actor: "agent@example.test", BaseRevision: testRevision(t), Operations: []Operation{EnsureRelation{Source: from, Type: "depends_on", Target: to}}, Preconditions: []Precondition{RefExists{Ref: from}, RelationAbsent{Source: from, Type: "depends_on", Target: to}}}

	// Act.
	first, err := change.CanonicalBytes()
	second, secondErr := change.CanonicalBytes()
	digest, digestErr := change.RequestDigest()

	// Assert.
	if err != nil || secondErr != nil || digestErr != nil {
		t.Fatalf("canonicalization errors: %v, %v, %v", err, secondErr, digestErr)
	}
	if string(first) != string(second) {
		t.Fatal("canonical bytes are nondeterministic")
	}
	if len(digest) != len("sha256:")+64 {
		t.Fatalf("unexpected request digest %q", digest)
	}
}

func TestChangeSetValidate_RejectsUnknownOrInvalidParts(t *testing.T) {
	// Arrange.
	change := ChangeSet{Version: ChangeSetFormatVersion, ID: "", Actor: "actor", BaseRevision: testRevision(t), Operations: []Operation{MoveConcept{}}}

	// Act.
	err := change.Validate()

	// Assert.
	if !errors.Is(err, ErrInvalidChangeSet) {
		t.Fatalf("expected invalid changeset, got %v", err)
	}
}

func TestChangeSetValidate_UsesStructuredErrorForPublicScalarContract(t *testing.T) {
	// Arrange.
	base := ChangeSet{Version: ChangeSetFormatVersion, ID: "cs-1", Actor: "actor", BaseRevision: testRevision(t)}
	tests := []struct {
		name string
		op   Operation
	}{
		{name: "reserved relation type", op: EnsureRelation{Source: testRef(t, "a", ""), Type: "title", Target: testRef(t, "b", "")}},
		{name: "fragment control", op: EnsureRelation{Source: testRef(t, "a", "bad\x00fragment"), Type: "depends_on", Target: testRef(t, "b", "")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			change := base
			change.Operations = []Operation{tt.op}

			// Act.
			err := change.Validate()

			// Assert.
			if !errors.Is(err, ErrInvalidChangeSet) {
				t.Fatalf("Validate() error = %v, want ErrInvalidChangeSet", err)
			}
			var structured *InvalidChangeSet
			if !errors.As(err, &structured) || structured.Code != "invalid_change_set" {
				t.Fatalf("Validate() error = %#v, want structured InvalidChangeSet", err)
			}
		})
	}
}

func TestChangeSetValidate_RejectsInvalidUTF8RelationFragments(t *testing.T) {
	base := ChangeSet{Version: ChangeSetFormatVersion, ID: "cs-1", Actor: "actor", BaseRevision: testRevision(t)}
	validSource, validTarget := testRef(t, "source", ""), testRef(t, "target", "")
	invalid := "bad\xe2\x28\xa1fragment"
	tests := []struct {
		name string
		set  func(*ChangeSet)
	}{
		{name: "ensure relation target", set: func(c *ChangeSet) {
			c.Operations = []Operation{EnsureRelation{Source: validSource, Type: "depends_on", Target: bundle.RelationRef{ID: validTarget.ID, Fragment: invalid}}}
		}},
		{name: "rename fragment from", set: func(c *ChangeSet) {
			c.Operations = []Operation{RenameFragment{Concept: validSource.ID, From: invalid, To: "new"}}
		}},
		{name: "rename fragment to", set: func(c *ChangeSet) {
			c.Operations = []Operation{RenameFragment{Concept: validSource.ID, From: "old", To: invalid}}
		}},
		{name: "relation exists target", set: func(c *ChangeSet) {
			c.Operations = []Operation{EnsureRelation{Source: validSource, Type: "depends_on", Target: validTarget}}
			c.Preconditions = []Precondition{RelationExists{Source: validSource, Type: "depends_on", Target: bundle.RelationRef{ID: validTarget.ID, Fragment: invalid}}}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			change := base
			tt.set(&change)

			// Act.
			err := change.Validate()

			// Assert.
			if !errors.Is(err, ErrInvalidChangeSet) {
				t.Fatalf("Validate() error = %v, want ErrInvalidChangeSet", err)
			}
			var structured *InvalidChangeSet
			if !errors.As(err, &structured) || structured.Code != "invalid_change_set" {
				t.Fatalf("Validate() error = %#v, want structured InvalidChangeSet", err)
			}
		})
	}
}

func TestChangeSetValidate_RejectsMoveToReservedLoaderFiles(t *testing.T) {
	// Arrange.
	from := testRef(t, "a", "").ID
	for _, raw := range []string{"index", "log", "nested/index", "nested/log"} {
		t.Run(raw, func(t *testing.T) {
			to := testRef(t, raw, "").ID
			change := ChangeSet{Version: ChangeSetFormatVersion, ID: "cs-1", Actor: "actor", BaseRevision: testRevision(t), Operations: []Operation{MoveConcept{From: from, To: to}}}

			// Act.
			err := change.Validate()

			// Assert.
			if !errors.Is(err, ErrInvalidChangeSet) {
				t.Fatalf("Validate() error = %v, want ErrInvalidChangeSet", err)
			}
		})
	}
}

func TestChangeSetValidate_AllowsPathSafeConceptIDPunctuation(t *testing.T) {
	// Arrange.
	from := testRef(t, "source", "").ID
	to := testRef(t, "complex & \"name]/東京", "").ID
	change := ChangeSet{Version: ChangeSetFormatVersion, ID: "cs-1", Actor: "actor", BaseRevision: testRevision(t), Operations: []Operation{MoveConcept{From: from, To: to}}}

	// Act.
	err := change.Validate()

	// Assert.
	if err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
}

func TestChangeSetValidate_RejectsZeroAndUnsupportedVersion(t *testing.T) {
	// Arrange.
	base := ChangeSet{ID: "cs-1", Actor: "actor", BaseRevision: testRevision(t), Operations: []Operation{EnsureRelation{Source: testRef(t, "a", ""), Type: "depends_on", Target: testRef(t, "b", "")}}}

	// Act.
	zeroErr := base.Validate()
	base.Version = ChangeSetFormatVersion + 1
	unsupportedErr := base.Validate()

	// Assert.
	if !errors.Is(zeroErr, ErrInvalidChangeSet) || !errors.Is(unsupportedErr, ErrInvalidChangeSet) {
		t.Fatalf("invalid versions must be rejected: zero=%v unsupported=%v", zeroErr, unsupportedErr)
	}
}

func TestChangeSetValidate_RejectsMalformedFileDigestPrecondition(t *testing.T) {
	// Arrange.
	base := ChangeSet{
		Version:      ChangeSetFormatVersion,
		ID:           "cs-1",
		Actor:        "actor",
		BaseRevision: testRevision(t),
		Operations:   []Operation{MoveConcept{From: testRef(t, "a", "").ID, To: testRef(t, "b", "").ID}},
	}
	valid := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	tests := []struct {
		name   string
		digest string
	}{
		{name: "uppercase", digest: "0123456789ABCDEF0123456789abcdef0123456789abcdef0123456789abcdef"},
		{name: "non hexadecimal", digest: "g123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
		{name: "wrong length", digest: valid[:63]},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			change := base
			change.Preconditions = []Precondition{FileDigestEquals{Path: "a.md", Digest: tt.digest}}

			// Act.
			err := change.Validate()

			// Assert.
			if !errors.Is(err, ErrInvalidChangeSet) {
				t.Fatalf("Validate() error = %v, want ErrInvalidChangeSet", err)
			}
		})
	}

	// Act.
	base.Preconditions = []Precondition{FileDigestEquals{Path: "a.md", Digest: valid}}
	err := base.Validate()

	// Assert.
	if err != nil {
		t.Fatalf("Validate() valid digest error = %v", err)
	}
}

func TestCommitOptions_EmptyKeyIsAllowedAndInvalidKeyIsRejected(t *testing.T) {
	// Arrange / Act.
	emptyErr := (CommitOptions{}).Validate()
	invalidErr := (CommitOptions{IdempotencyKey: " has spaces "}).Validate()

	// Assert.
	if emptyErr != nil {
		t.Fatalf("empty key disables replay: %v", emptyErr)
	}
	if !errors.Is(invalidErr, ErrInvalidChangeSet) {
		t.Fatalf("expected invalid key, got %v", invalidErr)
	}
}

func TestPreviewClone_IsDefensive(t *testing.T) {
	// Arrange.
	ref := testRef(t, "a", "f")
	preview := Preview{Writes: []Write{{Path: "a.md", Content: []byte("staged")}}, Diagnostics: []Diagnostic{{Code: "x", Refs: []bundle.RelationRef{ref}}}, Plan: []OperationPlan{{Details: []string{"detail"}, AffectedRefs: []bundle.RelationRef{ref}}}}

	// Act.
	copy := preview.Clone()
	copy.Diagnostics[0].Code = "changed"
	copy.Plan[0].Details[0] = "changed"
	copy.Writes[0].Content[0] = 'X'

	// Assert.
	if preview.Diagnostics[0].Code != "x" || preview.Plan[0].Details[0] != "detail" || string(preview.Writes[0].Content) != "staged" {
		t.Fatal("clone aliases mutable fields")
	}
}

func TestStructuredErrors_SupportIsAndAs(t *testing.T) {
	// Arrange.
	err := error(&Conflict{Expected: testRevision(t), Actual: testRevision(t), Retryable: true})

	// Act.
	matched := errors.Is(err, ErrConflict)
	var conflict *Conflict
	as := errors.As(err, &conflict)

	// Assert.
	if !matched || !as || !conflict.Retryable {
		t.Fatalf("structured conflict is not recognizable: is=%v as=%v", matched, as)
	}
}
