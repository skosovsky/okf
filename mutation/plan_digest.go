package mutation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"strings"
	"unicode/utf8"

	"github.com/skosovsky/okf/store"
)

const planDigestPrefix = "sha256:"

// ErrPlanDigestMismatch means apply-time authorization did not reproduce the
// exact preview projection accepted by the caller.
var ErrPlanDigestMismatch = errors.New("mutation plan digest mismatch")

type planDigestEncoder struct{ bytes.Buffer }

func (e *planDigestEncoder) string(value string) {
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], uint64(len(value)))
	e.Write(raw[:])
	e.WriteString(value)
}

func (e *planDigestEncoder) count(value int) {
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], uint64(value))
	e.Write(raw[:])
}

type contextPlanDigestEncoder struct {
	ctx  context.Context
	hash hash.Hash
}

func newContextPlanDigestEncoder(ctx context.Context) *contextPlanDigestEncoder {
	return &contextPlanDigestEncoder{ctx: ctx, hash: sha256.New()}
}

func (e *contextPlanDigestEncoder) writeBytes(value []byte) error {
	const chunkSize = 64 << 10
	for offset := 0; offset < len(value); offset += chunkSize {
		if err := e.ctx.Err(); err != nil {
			return err
		}
		end := min(offset+chunkSize, len(value))
		_, _ = e.hash.Write(value[offset:end])
	}
	return e.ctx.Err()
}

func (e *contextPlanDigestEncoder) string(value string) error {
	if err := e.count(len(value)); err != nil {
		return err
	}
	const chunkSize = 64 << 10
	for offset := 0; offset < len(value); offset += chunkSize {
		if err := e.ctx.Err(); err != nil {
			return err
		}
		end := min(offset+chunkSize, len(value))
		if err := e.writeBytes([]byte(value[offset:end])); err != nil {
			return err
		}
	}
	return e.ctx.Err()
}

func (e *contextPlanDigestEncoder) count(value int) error {
	if err := e.ctx.Err(); err != nil {
		return err
	}
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], uint64(value))
	_, _ = e.hash.Write(raw[:])
	return e.ctx.Err()
}

func (e *contextPlanDigestEncoder) digest() (string, error) {
	if err := e.ctx.Err(); err != nil {
		return "", err
	}
	sum := e.hash.Sum(nil)
	if err := e.ctx.Err(); err != nil {
		return "", err
	}
	digest := planDigestPrefix + hex.EncodeToString(sum)
	if err := e.ctx.Err(); err != nil {
		return "", err
	}
	return digest, nil
}

// PlanDigest binds a canonical ChangeSet request to the exact deterministic
// preview authorization projection. Explanatory Plan/Diagnostics text is not
// authorization data and is intentionally excluded.
func PlanDigest(change store.ChangeSet, preview store.Preview) (string, error) {
	return planDigestContext(context.Background(), change, preview)
}

func planDigestContext(ctx context.Context, change store.ChangeSet, preview store.Preview) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	requestDigest, err := change.RequestDigest()
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !preview.BaseRevision.Valid() || !preview.ResultRevision.Valid() ||
		preview.BaseRevision != change.BaseRevision {
		return "", fmt.Errorf("%w: invalid preview revisions", ErrPlanDigestMismatch)
	}
	reads := append([]store.Read(nil), preview.Reads...)
	writes := append([]store.Write(nil), preview.Writes...)
	deletes := append([]string(nil), preview.Deletes...)
	renames := append([]store.Rename(nil), preview.Renames...)
	for _, write := range writes {
		digest, err := sha256DigestContext(ctx, write.Content)
		if err != nil {
			return "", err
		}
		if write.Digest != digest {
			return "", fmt.Errorf("%w: write digest mismatch for %q", ErrPlanDigestMismatch, write.Path)
		}
	}
	if err := sortSliceCompareContext(ctx, reads, func(ctx context.Context, left, right store.Read) (int, error) {
		return compareStringsContext(ctx, left.Path, right.Path)
	}); err != nil {
		return "", err
	}
	if err := sortSliceCompareContext(ctx, writes, func(ctx context.Context, left, right store.Write) (int, error) {
		if order, err := compareStringsContext(ctx, left.Path, right.Path); err != nil || order != 0 {
			return order, err
		}
		// Digests are validated fixed-width lowercase SHA-256 strings above.
		return compareStringsContext(ctx, left.Digest, right.Digest)
	}); err != nil {
		return "", err
	}
	if err := sortStringsContext(ctx, deletes); err != nil {
		return "", err
	}
	if err := sortSliceCompareContext(ctx, renames, func(ctx context.Context, left, right store.Rename) (int, error) {
		if order, err := compareStringsContext(ctx, left.From, right.From); err != nil || order != 0 {
			return order, err
		}
		return compareStringsContext(ctx, left.To, right.To)
	}); err != nil {
		return "", err
	}
	affected, err := canonicalPlanRelationRefsContext(ctx, preview.AffectedRefs)
	if err != nil {
		return "", err
	}
	reverse, err := canonicalPlanRelationRefsContext(ctx, preview.ReverseImpact)
	if err != nil {
		return "", err
	}

	e := newContextPlanDigestEncoder(ctx)
	writeString := func(value string) error { return e.string(value) }
	writeCount := func(value int) error { return e.count(value) }
	for _, value := range []string{
		"okf:mutation-plan:v1", requestDigest, preview.BaseRevision.String(), preview.ResultRevision.String(), "reads",
	} {
		if err := writeString(value); err != nil {
			return "", err
		}
	}
	if err := writeCount(len(reads)); err != nil {
		return "", err
	}
	for _, read := range reads {
		if err := writeString(read.Path); err != nil {
			return "", err
		}
	}
	if err := writeString("writes"); err != nil {
		return "", err
	}
	if err := writeCount(len(writes)); err != nil {
		return "", err
	}
	for _, write := range writes {
		if err := writeString(write.Path); err != nil {
			return "", err
		}
		if err := writeString(write.Digest); err != nil {
			return "", err
		}
	}
	if err := writeString("deletes"); err != nil {
		return "", err
	}
	if err := writeCount(len(deletes)); err != nil {
		return "", err
	}
	for _, path := range deletes {
		if err := writeString(path); err != nil {
			return "", err
		}
	}
	if err := writeString("renames"); err != nil {
		return "", err
	}
	if err := writeCount(len(renames)); err != nil {
		return "", err
	}
	for _, rename := range renames {
		if err := writeString(rename.From); err != nil {
			return "", err
		}
		if err := writeString(rename.To); err != nil {
			return "", err
		}
	}
	if err := writeString("affected_refs"); err != nil {
		return "", err
	}
	if err := writeCount(len(affected)); err != nil {
		return "", err
	}
	for _, ref := range affected {
		if err := writeString(ref); err != nil {
			return "", err
		}
	}
	if err := writeString("reverse_impact"); err != nil {
		return "", err
	}
	if err := writeCount(len(reverse)); err != nil {
		return "", err
	}
	for _, ref := range reverse {
		if err := writeString(ref); err != nil {
			return "", err
		}
	}
	return e.digest()
}

func canonicalPlanRelationRefsContext(ctx context.Context, refs []store.Ref) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	canonical := append([]store.Ref{}, refs...)
	if err := sortSliceCompareContext(ctx, canonical, compareMigrationRelationRefContext); err != nil {
		return nil, err
	}
	out := make([]string, len(canonical))
	for index, ref := range canonical {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out[index] = ref.String()
	}
	return out, ctx.Err()
}

// ValidatePlanDigest validates the public digest wire shape.
func ValidatePlanDigest(value string) error {
	if len(value) != len(planDigestPrefix)+sha256.Size*2 || !strings.HasPrefix(value, planDigestPrefix) {
		return fmt.Errorf("%w: invalid digest", ErrPlanDigestMismatch)
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(value, planDigestPrefix)); err != nil {
		return fmt.Errorf("%w: invalid digest", ErrPlanDigestMismatch)
	}
	return nil
}

// PlanDigestIdempotencyKey derives the durable transaction identity from the
// exact plan authorization. callerKey is optional domain separation input; it
// is never used directly as the backend replay key.
func PlanDigestIdempotencyKey(namespace string, digest string, callerKey store.IdempotencyKey) (store.IdempotencyKey, error) {
	if namespace == "" || len(namespace) > 128 || !utf8.ValidString(namespace) || strings.TrimSpace(namespace) != namespace {
		return "", fmt.Errorf("%w: invalid plan idempotency namespace", store.ErrInvalidChangeSet)
	}
	for _, character := range namespace {
		if character < 0x20 || character == 0x7f {
			return "", fmt.Errorf("%w: invalid plan idempotency namespace", store.ErrInvalidChangeSet)
		}
	}
	if err := ValidatePlanDigest(digest); err != nil {
		return "", err
	}
	if callerKey != "" {
		if err := callerKey.Validate(); err != nil {
			return "", err
		}
	}
	e := planDigestEncoder{}
	e.string("okf:plan-idempotency:v1")
	e.string(namespace)
	e.string(digest)
	e.string(string(callerKey))
	sum := sha256.Sum256(e.Bytes())
	key := store.IdempotencyKey("okf-plan-v1-" + hex.EncodeToString(sum[:]))
	if err := key.Validate(); err != nil {
		return "", err
	}
	return key, nil
}
