package store

import (
	"encoding/binary"
	"sort"

	"github.com/skosovsky/okf/bundle"
)

// ProjectRelationDiagnostics projects bundle semantic findings onto the public
// store diagnostic contract. The result is deterministic, de-duplicated, and
// independent of the caller's diagnostic slice.
func ProjectRelationDiagnostics(relations []bundle.RelationDiagnostic) []Diagnostic {
	out := make([]Diagnostic, 0, len(relations))
	for _, relation := range relations {
		severity := DiagnosticInfo
		if relation.BlocksMutation() {
			severity = DiagnosticError
		}
		out = append(out, Diagnostic{
			Kind:         DiagnosticRelation,
			Severity:     severity,
			Code:         relation.Code,
			File:         relation.File,
			Message:      relation.Message,
			RelationType: relation.RelationType,
			RawTarget:    relation.RawTarget,
			Refs: []bundle.RelationRef{{
				ID: relation.Source, Fragment: relation.SourceFragment,
			}},
		})
	}
	sort.Slice(out, func(i, j int) bool { return diagnosticLess(out[i], out[j]) })
	unique := out[:0]
	for _, diagnostic := range out {
		if len(unique) == 0 || diagnosticIdentityOf(unique[len(unique)-1]) != diagnosticIdentityOf(diagnostic) {
			unique = append(unique, diagnostic)
		}
	}
	return unique
}

// diagnosticIdentity is the complete comparable identity of a public
// diagnostic. Scalar fields stay separate so their contents cannot cross a
// delimiter boundary. Refs need one string field to keep the identity
// comparable, so they use fixed-width length framing.
type diagnosticIdentity struct {
	kind, severity, code, file, relationType, rawTarget, message string
	refs                                                         string
}

func diagnosticIdentityOf(diagnostic Diagnostic) diagnosticIdentity {
	refs := diagnosticRefs(diagnostic.Refs)
	framedRefs := make([]byte, 0, len(refs)*8)
	var length [8]byte
	for _, ref := range refs {
		for _, component := range []string{ref.ID.String(), ref.Fragment} {
			binary.BigEndian.PutUint64(length[:], uint64(len(component)))
			framedRefs = append(framedRefs, length[:]...)
			framedRefs = append(framedRefs, component...)
		}
	}
	return diagnosticIdentity{
		kind:         string(diagnostic.Kind),
		severity:     string(diagnostic.Severity),
		code:         diagnostic.Code,
		file:         diagnostic.File,
		relationType: diagnostic.RelationType,
		rawTarget:    diagnostic.RawTarget,
		message:      diagnostic.Message,
		refs:         string(framedRefs),
	}
}

func diagnosticLess(left, right Diagnostic) bool {
	for _, fields := range [][2]string{
		{string(left.Kind), string(right.Kind)},
		{string(left.Severity), string(right.Severity)},
		{left.Code, right.Code},
		{left.File, right.File},
		{left.RelationType, right.RelationType},
		{left.RawTarget, right.RawTarget},
		{left.Message, right.Message},
	} {
		if fields[0] != fields[1] {
			return fields[0] < fields[1]
		}
	}
	leftRefs, rightRefs := diagnosticRefs(left.Refs), diagnosticRefs(right.Refs)
	for index := 0; index < len(leftRefs) && index < len(rightRefs); index++ {
		leftIdentity, rightIdentity := relationRefIdentityOf(leftRefs[index]), relationRefIdentityOf(rightRefs[index])
		if leftIdentity != rightIdentity {
			return leftIdentity.less(rightIdentity)
		}
	}
	return len(leftRefs) < len(rightRefs)
}

type relationRefIdentity struct {
	id       string
	fragment string
}

func relationRefIdentityOf(ref bundle.RelationRef) relationRefIdentity {
	return relationRefIdentity{id: ref.ID.String(), fragment: ref.Fragment}
}

func (left relationRefIdentity) less(right relationRefIdentity) bool {
	if left.id != right.id {
		return left.id < right.id
	}
	return left.fragment < right.fragment
}

func diagnosticRefs(in []bundle.RelationRef) []bundle.RelationRef {
	seen := make(map[relationRefIdentity]bundle.RelationRef, len(in))
	for _, ref := range in {
		seen[relationRefIdentityOf(ref)] = ref
	}
	out := make([]bundle.RelationRef, 0, len(seen))
	for _, ref := range seen {
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool {
		return relationRefIdentityOf(out[i]).less(relationRefIdentityOf(out[j]))
	})
	return out
}
