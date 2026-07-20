package store

import (
	"sort"
	"strings"

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
	sort.Slice(out, func(i, j int) bool { return diagnosticKey(out[i]) < diagnosticKey(out[j]) })
	unique := out[:0]
	for _, diagnostic := range out {
		if len(unique) == 0 || diagnosticKey(unique[len(unique)-1]) != diagnosticKey(diagnostic) {
			unique = append(unique, diagnostic)
		}
	}
	return unique
}

// diagnosticKey is the complete stable identity of a public diagnostic.
func diagnosticKey(diagnostic Diagnostic) string {
	refs := diagnosticRefs(diagnostic.Refs)
	parts := make([]string, len(refs))
	for i, ref := range refs {
		parts[i] = ref.String()
	}
	return string(diagnostic.Kind) + "\x00" + string(diagnostic.Severity) + "\x00" + diagnostic.Code + "\x00" + diagnostic.File + "\x00" + diagnostic.RelationType + "\x00" + diagnostic.RawTarget + "\x00" + diagnostic.Message + "\x00" + strings.Join(parts, "\x00")
}

func diagnosticRefs(in []bundle.RelationRef) []bundle.RelationRef {
	seen := make(map[string]bundle.RelationRef, len(in))
	for _, ref := range in {
		seen[ref.String()] = ref
	}
	out := make([]bundle.RelationRef, 0, len(seen))
	for _, ref := range seen {
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}
