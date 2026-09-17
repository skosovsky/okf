package mutation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/internal/migrationpath"
	"github.com/skosovsky/okf/internal/receiptprojection"
	"github.com/skosovsky/okf/store"
)

const MigrationPlanProofFormatVersion uint16 = 2

type MigrationPlanWrite struct {
	Path   string
	Digest string
}

// MigrationPlanProof is the canonical, content-free authorization projection
// returned by Preview and required by Apply, including the exact receipt delta.
type MigrationPlanProof struct {
	FormatVersion    uint16
	RequestDigest    string
	ResolutionDigest string
	BaseRevision     store.Revision
	ResultRevision   store.Revision
	Reads            []store.Read
	Writes           []MigrationPlanWrite
	Deletes          []string
	Renames          []store.Rename
	AffectedRefs     []bundle.RelationRef
	ReverseImpact    []bundle.RelationRef
	ChangedFiles     []store.FileChange
	ChangedRefs      []bundle.RelationRef
}

// Clone returns an independent proof while preserving the exact nil or
// non-nil shape of every slice field.
func (p MigrationPlanProof) Clone() MigrationPlanProof {
	cloned, _ := p.cloneContext(context.Background())
	return cloned
}

func (p MigrationPlanProof) cloneContext(ctx context.Context) (MigrationPlanProof, error) {
	if err := ctx.Err(); err != nil {
		return MigrationPlanProof{}, err
	}
	var err error
	if p.Reads, err = cloneMigrationProofSliceContext(ctx, p.Reads); err != nil {
		return MigrationPlanProof{}, err
	}
	if p.Writes, err = cloneMigrationProofSliceContext(ctx, p.Writes); err != nil {
		return MigrationPlanProof{}, err
	}
	if p.Deletes, err = cloneMigrationProofSliceContext(ctx, p.Deletes); err != nil {
		return MigrationPlanProof{}, err
	}
	if p.Renames, err = cloneMigrationProofSliceContext(ctx, p.Renames); err != nil {
		return MigrationPlanProof{}, err
	}
	if p.AffectedRefs, err = cloneMigrationProofSliceContext(ctx, p.AffectedRefs); err != nil {
		return MigrationPlanProof{}, err
	}
	if p.ReverseImpact, err = cloneMigrationProofSliceContext(ctx, p.ReverseImpact); err != nil {
		return MigrationPlanProof{}, err
	}
	if p.ChangedFiles, err = cloneMigrationProofSliceContext(ctx, p.ChangedFiles); err != nil {
		return MigrationPlanProof{}, err
	}
	if p.ChangedRefs, err = cloneMigrationProofSliceContext(ctx, p.ChangedRefs); err != nil {
		return MigrationPlanProof{}, err
	}
	return p, ctx.Err()
}

func cloneMigrationProofSliceContext[T any](ctx context.Context, values []T) ([]T, error) {
	if values == nil {
		return nil, ctx.Err()
	}
	cloned := make([]T, len(values))
	const chunkSize = 1024
	for offset := 0; offset < len(values); offset += chunkSize {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := min(offset+chunkSize, len(values))
		copy(cloned[offset:end], values[offset:end])
	}
	return cloned, ctx.Err()
}

func newMigrationPlanProofContext(
	ctx context.Context,
	change store.ChangeSet,
	preview store.Preview,
	projection receiptprojection.Projection,
	resolution MigrationSourceResolution,
) (MigrationPlanProof, error) {
	if err := ctx.Err(); err != nil {
		return MigrationPlanProof{}, err
	}
	requestDigest, err := change.RequestDigest()
	if err != nil {
		return MigrationPlanProof{}, err
	}
	if err := ctx.Err(); err != nil {
		return MigrationPlanProof{}, err
	}
	if _, err := planDigestContext(ctx, change, preview); err != nil {
		return MigrationPlanProof{}, err
	}
	resolutionDigest, err := migrationSourceResolutionDigestContext(ctx, resolution)
	if err != nil {
		return MigrationPlanProof{}, err
	}
	proof := MigrationPlanProof{
		FormatVersion:    MigrationPlanProofFormatVersion,
		RequestDigest:    requestDigest,
		ResolutionDigest: resolutionDigest,
		BaseRevision:     preview.BaseRevision,
		ResultRevision:   preview.ResultRevision,
		Reads:            make([]store.Read, len(preview.Reads)),
		Writes:           make([]MigrationPlanWrite, len(preview.Writes)),
		Deletes:          make([]string, len(preview.Deletes)),
		Renames:          make([]store.Rename, len(preview.Renames)),
		AffectedRefs:     make([]bundle.RelationRef, len(preview.AffectedRefs)),
		ReverseImpact:    make([]bundle.RelationRef, len(preview.ReverseImpact)),
		ChangedFiles:     make([]store.FileChange, len(projection.ChangedFiles)),
		ChangedRefs:      make([]bundle.RelationRef, len(projection.ChangedRefs)),
	}
	if proof.Reads, err = cloneMigrationProofSliceContext(ctx, preview.Reads); err != nil {
		return MigrationPlanProof{}, err
	}
	if proof.Deletes, err = cloneMigrationProofSliceContext(ctx, preview.Deletes); err != nil {
		return MigrationPlanProof{}, err
	}
	if proof.Renames, err = cloneMigrationProofSliceContext(ctx, preview.Renames); err != nil {
		return MigrationPlanProof{}, err
	}
	if proof.AffectedRefs, err = cloneMigrationProofSliceContext(ctx, preview.AffectedRefs); err != nil {
		return MigrationPlanProof{}, err
	}
	if proof.ReverseImpact, err = cloneMigrationProofSliceContext(ctx, preview.ReverseImpact); err != nil {
		return MigrationPlanProof{}, err
	}
	if proof.ChangedFiles, err = cloneMigrationProofSliceContext(ctx, projection.ChangedFiles); err != nil {
		return MigrationPlanProof{}, err
	}
	if proof.ChangedRefs, err = cloneMigrationProofSliceContext(ctx, projection.ChangedRefs); err != nil {
		return MigrationPlanProof{}, err
	}
	for index, write := range preview.Writes {
		if err := ctx.Err(); err != nil {
			return MigrationPlanProof{}, err
		}
		proof.Writes[index] = MigrationPlanWrite{Path: write.Path, Digest: write.Digest}
	}
	proof, err = canonicalizeMigrationPlanProofContext(ctx, proof)
	if err != nil {
		return MigrationPlanProof{}, err
	}
	if err := validateMigrationPlanProofContext(ctx, change, proof, resolution); err != nil {
		return MigrationPlanProof{}, err
	}
	return proof, nil
}

func canonicalizeMigrationPlanProof(proof *MigrationPlanProof) {
	canonical, err := canonicalizeMigrationPlanProofContext(context.Background(), *proof)
	if err != nil {
		*proof = MigrationPlanProof{}
		return
	}
	*proof = canonical
}

func canonicalizeMigrationPlanProofContext(ctx context.Context, proof MigrationPlanProof) (MigrationPlanProof, error) {
	owned, err := proof.cloneContext(ctx)
	if err != nil {
		return MigrationPlanProof{}, err
	}
	if err := sortSliceCompareContext(ctx, owned.Reads, compareMigrationReadContext); err != nil {
		return MigrationPlanProof{}, err
	}
	if err := sortSliceCompareContext(ctx, owned.Writes, compareMigrationPlanWriteContext); err != nil {
		return MigrationPlanProof{}, err
	}
	if err := sortSliceCompareContext(ctx, owned.Deletes, compareMigrationDeleteContext); err != nil {
		return MigrationPlanProof{}, err
	}
	if err := sortSliceCompareContext(ctx, owned.Renames, compareMigrationRenameContext); err != nil {
		return MigrationPlanProof{}, err
	}
	if err := sortSliceCompareContext(ctx, owned.AffectedRefs, compareMigrationRelationRefContext); err != nil {
		return MigrationPlanProof{}, err
	}
	if err := sortSliceCompareContext(ctx, owned.ReverseImpact, compareMigrationRelationRefContext); err != nil {
		return MigrationPlanProof{}, err
	}
	if err := sortSliceCompareContext(ctx, owned.ChangedFiles, compareMigrationFileChangeContext); err != nil {
		return MigrationPlanProof{}, err
	}
	if err := sortSliceCompareContext(ctx, owned.ChangedRefs, compareMigrationRelationRefContext); err != nil {
		return MigrationPlanProof{}, err
	}
	return owned, ctx.Err()
}

func validateMigrationPlanProofContext(
	ctx context.Context,
	change store.ChangeSet,
	proof MigrationPlanProof,
	resolution MigrationSourceResolution,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if proof.FormatVersion != MigrationPlanProofFormatVersion ||
		proof.Reads == nil || proof.Writes == nil || proof.Deletes == nil ||
		proof.Renames == nil || proof.AffectedRefs == nil || proof.ReverseImpact == nil ||
		proof.ChangedFiles == nil || proof.ChangedRefs == nil {
		return fmt.Errorf("%w: invalid migration proof shape", ErrMigrationPlanMismatch)
	}
	requestDigest, err := change.RequestDigest()
	if err != nil {
		return err
	}
	resolutionDigest, err := migrationSourceResolutionDigestContext(ctx, resolution)
	if err != nil {
		return err
	}
	if proof.RequestDigest != requestDigest ||
		!constantTimeDigestEqual(proof.ResolutionDigest, resolutionDigest) ||
		proof.BaseRevision != change.BaseRevision ||
		!proof.BaseRevision.Valid() || !proof.ResultRevision.Valid() {
		return fmt.Errorf("%w: migration proof request binding mismatch", ErrMigrationPlanMismatch)
	}
	if err := validateProofReadsContext(ctx, proof.Reads); err != nil {
		return err
	}
	if err := validateProofWritesContext(ctx, proof.Writes); err != nil {
		return err
	}
	if err := validateProofPathsContext(ctx, proof.Deletes); err != nil {
		return err
	}
	if err := validateProofRenamesContext(ctx, proof.Renames); err != nil {
		return err
	}
	if err := validateProofRefsContext(ctx, proof.AffectedRefs); err != nil {
		return err
	}
	if err := validateProofRefsContext(ctx, proof.ReverseImpact); err != nil {
		return err
	}
	if err := validateProofFileChangesContext(ctx, proof.ChangedFiles); err != nil {
		return err
	}
	if err := validateProofRefsContext(ctx, proof.ChangedRefs); err != nil {
		return err
	}
	if err := validateMigrationProofScopeContext(ctx, change, proof); err != nil {
		return err
	}
	return ctx.Err()
}

func validateMigrationProofScopeContext(ctx context.Context, change store.ChangeSet, proof MigrationPlanProof) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(change.Operations) != 1 {
		return fmt.Errorf("%w: migration proof operation scope mismatch", ErrMigrationPlanMismatch)
	}
	operation, ok := change.Operations[0].(store.MigrateV01ToV02)
	if !ok {
		return fmt.Errorf("%w: migration proof operation scope mismatch", ErrMigrationPlanMismatch)
	}
	if len(proof.Deletes) != 0 || len(proof.Renames) != 0 {
		return fmt.Errorf("%w: migration proof cannot delete or rename files", ErrMigrationPlanMismatch)
	}
	reads := make(map[string]struct{}, len(proof.Reads))
	for _, read := range proof.Reads {
		if err := ctx.Err(); err != nil {
			return err
		}
		reads[read.Path] = struct{}{}
	}
	created := make(map[string]struct{})
	for _, computation := range operation.Migration().Computations {
		if err := ctx.Err(); err != nil {
			return err
		}
		created[computation.Path] = struct{}{}
		if computation.Asset != nil {
			created[computation.Asset.Path] = struct{}{}
		}
	}
	for _, write := range proof.Writes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, requested := created[write.Path]; requested {
			continue
		}
		if _, existed := reads[write.Path]; existed {
			if !migrationpath.IsMarkdownDocument(write.Path) {
				return fmt.Errorf("%w: migration proof writes unowned existing path", ErrMigrationPlanMismatch)
			}
			continue
		}
		return fmt.Errorf("%w: migration proof writes unrequested path", ErrMigrationPlanMismatch)
	}
	if len(proof.ChangedFiles) != len(proof.Writes) {
		return fmt.Errorf("%w: migration proof receipt file set mismatch", ErrMigrationPlanMismatch)
	}
	for index, write := range proof.Writes {
		if err := ctx.Err(); err != nil {
			return err
		}
		change := proof.ChangedFiles[index]
		if change.Kind != store.FileWrite || change.Path != write.Path || change.From != "" {
			return fmt.Errorf("%w: migration proof receipt file identity mismatch", ErrMigrationPlanMismatch)
		}
	}
	return nil
}

func validateProofReadsContext(ctx context.Context, values []store.Read) error {
	for index, value := range values {
		if err := ctx.Err(); err != nil {
			return err
		}
		if bundle.ValidateRevisionPath(value.Path) != nil {
			return fmt.Errorf("%w: noncanonical migration proof reads", ErrMigrationPlanMismatch)
		}
		if index > 0 {
			order, err := compareMigrationReadContext(ctx, values[index-1], value)
			if err != nil {
				return err
			}
			if order >= 0 {
				return fmt.Errorf("%w: noncanonical migration proof reads", ErrMigrationPlanMismatch)
			}
		}
	}
	return ctx.Err()
}

func validateProofWritesContext(ctx context.Context, values []MigrationPlanWrite) error {
	for index, value := range values {
		if err := ctx.Err(); err != nil {
			return err
		}
		if bundle.ValidateRevisionPath(value.Path) != nil ||
			len(value.Digest) != 64 || strings.ToLower(value.Digest) != value.Digest {
			return fmt.Errorf("%w: noncanonical migration proof writes", ErrMigrationPlanMismatch)
		}
		if _, err := hex.DecodeString(value.Digest); err != nil {
			return fmt.Errorf("%w: invalid migration proof write digest", ErrMigrationPlanMismatch)
		}
		if index > 0 {
			order, err := compareMigrationPlanWriteContext(ctx, values[index-1], value)
			if err != nil {
				return err
			}
			pathOrder, err := compareStringsContext(ctx, values[index-1].Path, value.Path)
			if err != nil {
				return err
			}
			if order >= 0 || pathOrder == 0 {
				return fmt.Errorf("%w: noncanonical migration proof writes", ErrMigrationPlanMismatch)
			}
		}
	}
	return ctx.Err()
}

func validateProofPathsContext(ctx context.Context, values []string) error {
	for index, value := range values {
		if err := ctx.Err(); err != nil {
			return err
		}
		if bundle.ValidateRevisionPath(value) != nil {
			return fmt.Errorf("%w: noncanonical migration proof paths", ErrMigrationPlanMismatch)
		}
		if index > 0 {
			order, err := compareMigrationDeleteContext(ctx, values[index-1], value)
			if err != nil {
				return err
			}
			if order >= 0 {
				return fmt.Errorf("%w: noncanonical migration proof paths", ErrMigrationPlanMismatch)
			}
		}
	}
	return ctx.Err()
}

func validateProofRenamesContext(ctx context.Context, values []store.Rename) error {
	for index, value := range values {
		if err := ctx.Err(); err != nil {
			return err
		}
		if bundle.ValidateRevisionPath(value.From) != nil ||
			bundle.ValidateRevisionPath(value.To) != nil || value.From == value.To {
			return fmt.Errorf("%w: noncanonical migration proof renames", ErrMigrationPlanMismatch)
		}
		if index > 0 {
			order, err := compareMigrationRenameContext(ctx, values[index-1], value)
			if err != nil {
				return err
			}
			if order >= 0 {
				return fmt.Errorf("%w: noncanonical migration proof renames", ErrMigrationPlanMismatch)
			}
		}
	}
	return ctx.Err()
}

func validateProofRefsContext(ctx context.Context, values []bundle.RelationRef) error {
	for index, value := range values {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := bundle.ValidateRelationRef(value); err != nil {
			return fmt.Errorf("%w: invalid migration proof ref", ErrMigrationPlanMismatch)
		}
		if index > 0 {
			order, err := compareMigrationRelationRefContext(ctx, values[index-1], value)
			if err != nil {
				return err
			}
			if order >= 0 {
				return fmt.Errorf("%w: noncanonical migration proof refs", ErrMigrationPlanMismatch)
			}
		}
	}
	return ctx.Err()
}

func compareMigrationRelationRef(left, right bundle.RelationRef) int {
	order, _ := compareMigrationRelationRefContext(context.Background(), left, right)
	return order
}

func compareMigrationRelationRefContext(ctx context.Context, left, right bundle.RelationRef) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	leftKey := relationRefIdentity(left)
	rightKey := relationRefIdentity(right)
	if order, err := compareStringsContext(ctx, leftKey.id, rightKey.id); err != nil || order != 0 {
		return order, err
	}
	return compareStringsContext(ctx, leftKey.fragment, rightKey.fragment)
}

func validateProofFileChangesContext(ctx context.Context, values []store.FileChange) error {
	for index, value := range values {
		if err := ctx.Err(); err != nil {
			return err
		}
		if bundle.ValidateRevisionPath(value.Path) != nil {
			return fmt.Errorf("%w: invalid migration proof file path", ErrMigrationPlanMismatch)
		}
		switch value.Kind {
		case store.FileWrite, store.FileDelete:
			if value.From != "" {
				return fmt.Errorf("%w: invalid migration proof file change", ErrMigrationPlanMismatch)
			}
		case store.FileRename:
			if bundle.ValidateRevisionPath(value.From) != nil || value.From == value.Path {
				return fmt.Errorf("%w: invalid migration proof rename", ErrMigrationPlanMismatch)
			}
		default:
			return fmt.Errorf("%w: invalid migration proof file kind", ErrMigrationPlanMismatch)
		}
		if index > 0 {
			order, err := compareMigrationFileChangeContext(ctx, values[index-1], value)
			if err != nil {
				return err
			}
			if order >= 0 {
				return fmt.Errorf("%w: noncanonical migration proof files", ErrMigrationPlanMismatch)
			}
		}
	}
	return ctx.Err()
}

func compareRename(left, right store.Rename) int {
	order, _ := compareMigrationRenameContext(context.Background(), left, right)
	return order
}

func compareFileChange(left, right store.FileChange) int {
	order, _ := compareMigrationFileChangeContext(context.Background(), left, right)
	return order
}

func compareMigrationReadContext(ctx context.Context, left, right store.Read) (int, error) {
	return compareStringsContext(ctx, left.Path, right.Path)
}

func compareMigrationPlanWriteContext(ctx context.Context, left, right MigrationPlanWrite) (int, error) {
	if order, err := compareStringsContext(ctx, left.Path, right.Path); err != nil || order != 0 {
		return order, err
	}
	return compareStringsContext(ctx, left.Digest, right.Digest)
}

func compareMigrationDeleteContext(ctx context.Context, left, right string) (int, error) {
	return compareStringsContext(ctx, left, right)
}

func compareMigrationRenameContext(ctx context.Context, left, right store.Rename) (int, error) {
	if order, err := compareStringsContext(ctx, left.From, right.From); err != nil || order != 0 {
		return order, err
	}
	return compareStringsContext(ctx, left.To, right.To)
}

func compareMigrationFileChangeContext(ctx context.Context, left, right store.FileChange) (int, error) {
	if order, err := compareStringsContext(ctx, left.Path, right.Path); err != nil || order != 0 {
		return order, err
	}
	if order, err := compareStringsContext(ctx, string(left.Kind), string(right.Kind)); err != nil || order != 0 {
		return order, err
	}
	return compareStringsContext(ctx, left.From, right.From)
}

func migrationPlanProofDigestContext(
	ctx context.Context,
	change store.ChangeSet,
	proof MigrationPlanProof,
	resolution MigrationSourceResolution,
) (string, error) {
	if err := validateMigrationPlanProofContext(ctx, change, proof, resolution); err != nil {
		return "", err
	}
	e := newContextPlanDigestEncoder(ctx)
	if err := e.string("okf:migration-plan-proof:v2"); err != nil {
		return "", err
	}
	if err := e.count(int(proof.FormatVersion)); err != nil {
		return "", err
	}
	for _, value := range []string{
		proof.RequestDigest,
		proof.ResolutionDigest,
		proof.BaseRevision.String(),
		proof.ResultRevision.String(),
		"reads",
	} {
		if err := e.string(value); err != nil {
			return "", err
		}
	}
	if err := e.count(len(proof.Reads)); err != nil {
		return "", err
	}
	for _, value := range proof.Reads {
		if err := e.string(value.Path); err != nil {
			return "", err
		}
	}
	if err := e.string("writes"); err != nil {
		return "", err
	}
	if err := e.count(len(proof.Writes)); err != nil {
		return "", err
	}
	for _, value := range proof.Writes {
		if err := e.string(value.Path); err != nil {
			return "", err
		}
		if err := e.string(value.Digest); err != nil {
			return "", err
		}
	}
	if err := e.string("deletes"); err != nil {
		return "", err
	}
	if err := e.count(len(proof.Deletes)); err != nil {
		return "", err
	}
	for _, value := range proof.Deletes {
		if err := e.string(value); err != nil {
			return "", err
		}
	}
	if err := e.string("renames"); err != nil {
		return "", err
	}
	if err := e.count(len(proof.Renames)); err != nil {
		return "", err
	}
	for _, value := range proof.Renames {
		if err := e.string(value.From); err != nil {
			return "", err
		}
		if err := e.string(value.To); err != nil {
			return "", err
		}
	}
	if err := encodeProofRefsContext(ctx, e, "affected_refs", proof.AffectedRefs); err != nil {
		return "", err
	}
	if err := encodeProofRefsContext(ctx, e, "reverse_impact", proof.ReverseImpact); err != nil {
		return "", err
	}
	if err := e.string("changed_files"); err != nil {
		return "", err
	}
	if err := e.count(len(proof.ChangedFiles)); err != nil {
		return "", err
	}
	for _, value := range proof.ChangedFiles {
		if err := e.string(string(value.Kind)); err != nil {
			return "", err
		}
		if err := e.string(value.Path); err != nil {
			return "", err
		}
		if err := e.string(value.From); err != nil {
			return "", err
		}
	}
	if err := encodeProofRefsContext(ctx, e, "changed_refs", proof.ChangedRefs); err != nil {
		return "", err
	}
	return e.digest()
}

func encodeProofRefsContext(ctx context.Context, e *contextPlanDigestEncoder, label string, refs []bundle.RelationRef) error {
	if err := e.string(label); err != nil {
		return err
	}
	if err := e.count(len(refs)); err != nil {
		return err
	}
	for _, ref := range refs {
		if err := e.string(ref.String()); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func digestPlanEncoder(e planDigestEncoder) string {
	sum := sha256.Sum256(e.Bytes())
	return planDigestPrefix + hex.EncodeToString(sum[:])
}

func equalMigrationPlanProof(left, right MigrationPlanProof) bool {
	equal, _ := equalMigrationPlanProofContext(context.Background(), left, right)
	return equal
}

func equalMigrationPlanProofContext(ctx context.Context, left, right MigrationPlanProof) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if left.FormatVersion != right.FormatVersion {
		return false, nil
	}
	for _, values := range [][2]string{
		{left.RequestDigest, right.RequestDigest},
		{left.ResolutionDigest, right.ResolutionDigest},
		{left.BaseRevision.String(), right.BaseRevision.String()},
		{left.ResultRevision.String(), right.ResultRevision.String()},
	} {
		order, err := compareStringsContext(ctx, values[0], values[1])
		if err != nil || order != 0 {
			return false, err
		}
	}
	if equal, err := equalMigrationProofSlicesContext(ctx, left.Reads, right.Reads, func(ctx context.Context, left, right store.Read) (bool, error) {
		order, err := compareMigrationReadContext(ctx, left, right)
		return order == 0, err
	}); err != nil || !equal {
		return false, err
	}
	if equal, err := equalMigrationProofSlicesContext(ctx, left.Writes, right.Writes, func(ctx context.Context, left, right MigrationPlanWrite) (bool, error) {
		order, err := compareMigrationPlanWriteContext(ctx, left, right)
		return order == 0, err
	}); err != nil || !equal {
		return false, err
	}
	if equal, err := equalMigrationProofSlicesContext(ctx, left.Deletes, right.Deletes, func(ctx context.Context, left, right string) (bool, error) {
		order, err := compareMigrationDeleteContext(ctx, left, right)
		return order == 0, err
	}); err != nil || !equal {
		return false, err
	}
	if equal, err := equalMigrationProofSlicesContext(ctx, left.Renames, right.Renames, func(ctx context.Context, left, right store.Rename) (bool, error) {
		order, err := compareMigrationRenameContext(ctx, left, right)
		return order == 0, err
	}); err != nil || !equal {
		return false, err
	}
	if equal, err := equalMigrationProofSlicesContext(ctx, left.AffectedRefs, right.AffectedRefs, equalMigrationRelationRefContext); err != nil || !equal {
		return false, err
	}
	if equal, err := equalMigrationProofSlicesContext(ctx, left.ReverseImpact, right.ReverseImpact, equalMigrationRelationRefContext); err != nil || !equal {
		return false, err
	}
	if equal, err := equalMigrationProofSlicesContext(ctx, left.ChangedFiles, right.ChangedFiles, func(ctx context.Context, left, right store.FileChange) (bool, error) {
		order, err := compareMigrationFileChangeContext(ctx, left, right)
		return order == 0, err
	}); err != nil || !equal {
		return false, err
	}
	if equal, err := equalMigrationProofSlicesContext(ctx, left.ChangedRefs, right.ChangedRefs, equalMigrationRelationRefContext); err != nil || !equal {
		return false, err
	}
	return true, ctx.Err()
}

func equalMigrationRelationRefContext(ctx context.Context, left, right bundle.RelationRef) (bool, error) {
	order, err := compareMigrationRelationRefContext(ctx, left, right)
	return order == 0, err
}

func equalMigrationProofSlicesContext[T any](
	ctx context.Context,
	left, right []T,
	equal func(context.Context, T, T) (bool, error),
) (bool, error) {
	if (left == nil) != (right == nil) || len(left) != len(right) {
		return false, ctx.Err()
	}
	for index := range left {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		matches, err := equal(ctx, left[index], right[index])
		if err != nil || !matches {
			return false, err
		}
	}
	return true, ctx.Err()
}
