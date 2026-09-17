// Package store defines backend-neutral contracts for transactional OKF writes.
package store

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"reflect"
	"sort"
	"strings"

	"github.com/skosovsky/okf/bundle"
)

const revisionAlgorithm = "sha256"

// HashAlgorithm supplies the content and manifest hash used to form a revision.
// Implementations must be immutable after they are supplied to a store API and
// return a fresh, size-consistent hash on every New call. SHA-256 is the default;
// the algorithm name is part of every produced digest and revision.
type HashAlgorithm interface {
	Name() string
	New() hash.Hash
}

// ContextHashAlgorithm is an optional extension for hash implementations that
// may block while acquiring or processing a hash. Implementations must return
// promptly when ctx is cancelled. HashAlgorithm remains useful for ordinary
// in-memory algorithms.
type ContextHashAlgorithm interface {
	HashAlgorithm
	NewContext(context.Context) (hash.Hash, error)
}

type sha256Algorithm struct{}

func (sha256Algorithm) Name() string   { return revisionAlgorithm }
func (sha256Algorithm) New() hash.Hash { return sha256.New() }

var defaultHashAlgorithm HashAlgorithm = sha256Algorithm{}

// Revision is an algorithm-qualified, content-addressed bundle identity.
type Revision string

func ParseRevision(value string) (Revision, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 || !validAlgorithmName(parts[0]) || !validHexDigest(parts[1]) ||
		(parts[0] == revisionAlgorithm && len(parts[1]) != sha256.Size*2) {
		return "", fmt.Errorf("%w: %q", ErrInvalidRevision, value)
	}
	return Revision(value), nil
}

func validAlgorithmName(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}
func validHexDigest(digest string) bool {
	if digest == "" || len(digest)%2 != 0 || digest != strings.ToLower(digest) {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}
func validateSHA256HexDigest(digest string) error {
	if len(digest) != sha256.Size*2 {
		return fmt.Errorf("invalid SHA-256 digest length")
	}
	if !validHexDigest(digest) {
		return fmt.Errorf("invalid SHA-256 digest")
	}
	return nil
}
func (r Revision) String() string { return string(r) }
func (r Revision) IsZero() bool   { return r == "" }
func (r Revision) Valid() bool    { _, err := ParseRevision(string(r)); return err == nil }

type ManifestEntry struct {
	Path    string
	Content []byte
}

// Manifest is an immutable, copyable path-to-qualified-content-digest cache.
// Its accessors never expose its internal map.
type Manifest struct {
	algorithm  HashAlgorithm
	digestSize int
	digests    map[string]string
}

func NewManifest(entries []ManifestEntry) (Manifest, error) {
	return NewManifestContext(context.Background(), entries, defaultHashAlgorithm)
}
func NewManifestWithAlgorithm(entries []ManifestEntry, algorithm HashAlgorithm) (Manifest, error) {
	return NewManifestContext(context.Background(), entries, algorithm)
}
func NewManifestContext(ctx context.Context, entries []ManifestEntry, algorithm HashAlgorithm) (Manifest, error) {
	if algorithm == nil {
		algorithm = defaultHashAlgorithm
	}
	name, err := hashAlgorithmName(algorithm)
	if err != nil {
		return Manifest{}, manifestHashError(err)
	}
	m := Manifest{algorithm: algorithm, digests: make(map[string]string, len(entries))}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return Manifest{}, err
		}
		if err := validateManifestPath(e.Path); err != nil {
			return Manifest{}, err
		}
		if _, exists := m.digests[e.Path]; exists {
			return Manifest{}, fmt.Errorf("%w: duplicate path %q", ErrInvalidManifest, e.Path)
		}
		digest, err := digestContentContext(ctx, algorithm, e.Content)
		if err != nil {
			return Manifest{}, manifestHashError(err)
		}
		parts := strings.Split(digest, ":")
		if len(parts) != 2 || parts[0] != name || !validHexDigest(parts[1]) {
			return Manifest{}, fmt.Errorf("%w: inconsistent digest size", ErrInvalidHashAlgorithm)
		}
		digestSize := len(parts[1]) / 2
		if m.digestSize == 0 {
			m.digestSize = digestSize
		} else if digestSize != m.digestSize {
			return Manifest{}, fmt.Errorf("%w: inconsistent digest size", ErrInvalidHashAlgorithm)
		}
		m.digests[e.Path] = digest
	}
	if len(entries) == 0 {
		_, digestSize, err := hashAlgorithmDigestSpec(ctx, algorithm)
		if err != nil {
			return Manifest{}, manifestHashError(err)
		}
		m.digestSize = digestSize
	}
	return m, nil
}
func digestContentContext(ctx context.Context, algorithm HashAlgorithm, content []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	h, err := newHashContext(ctx, algorithm)
	if err != nil {
		return "", err
	}
	digestSize, err := safeHashSize(h)
	if err != nil || digestSize <= 0 {
		if err == nil {
			err = fmt.Errorf("%w: non-positive digest size", ErrInvalidHashAlgorithm)
		}
		return "", err
	}
	const chunk = 64 << 10
	for len(content) > 0 {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n := len(content)
		if n > chunk {
			n = chunk
		}
		if err := writeHashAll(h, content[:n]); err != nil {
			return "", err
		}
		content = content[n:]
	}
	name, err := hashAlgorithmName(algorithm)
	if err != nil {
		return "", err
	}
	sum, err := sumHash(h)
	if err != nil {
		return "", err
	}
	if len(sum) != digestSize {
		return "", fmt.Errorf("%w: Size and Sum disagree", ErrInvalidHashAlgorithm)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return name + ":" + hex.EncodeToString(sum), nil
}
func (m Manifest) Clone() Manifest {
	cloned, _ := m.CloneContext(context.Background())
	return cloned
}

// CloneContext returns an independent manifest value while honoring ctx.
func (m Manifest) CloneContext(ctx context.Context) (Manifest, error) {
	digests, err := cloneDigestsContext(ctx, m.digests)
	if err != nil {
		return Manifest{}, err
	}
	return Manifest{algorithm: m.algorithm, digestSize: m.digestSize, digests: digests}, nil
}
func (m Manifest) Digests() map[string]string {
	digests, _ := m.DigestsContext(context.Background())
	return digests
}

// DigestsContext returns an owned digest map while honoring ctx.
func (m Manifest) DigestsContext(ctx context.Context) (map[string]string, error) {
	return cloneDigestsContext(ctx, m.digests)
}
func cloneDigestsContext(ctx context.Context, in map[string]string) (map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if in == nil {
		return nil, nil
	}
	out := make(map[string]string, len(in))
	for p, d := range in {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out[p] = d
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
func (m Manifest) Digest(path string) (string, bool) { d, ok := m.digests[path]; return d, ok }

// Len returns the number of cached content digests.
func (m Manifest) Len() int { return len(m.digests) }

// AlgorithmName returns the qualified digest algorithm used by m.  The empty
// string reports an invalid manifest.  It is deliberately a name rather than
// the algorithm object so callers cannot mutate the manifest's hash policy.
func (m Manifest) AlgorithmName() string {
	name, err := hashAlgorithmName(m.algorithm)
	if err != nil {
		return ""
	}
	return name
}

// DigestContentContext computes a qualified content digest using m's
// algorithm. It does not mutate m or expose its internal map.
func (m Manifest) DigestContentContext(ctx context.Context, content []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	name, err := hashAlgorithmName(m.algorithm)
	if err != nil || m.digests == nil || m.digestSize <= 0 {
		return "", fmt.Errorf("%w: invalid manifest digest specification", ErrInvalidManifest)
	}
	digest, err := digestContentContext(ctx, m.algorithm, content)
	if err != nil {
		return "", manifestHashError(err)
	}
	if !qualifiedDigestValid(digest, name, m.digestSize) {
		return "", errors.Join(ErrInvalidManifest, ErrInvalidHashAlgorithm)
	}
	return digest, nil
}

// WithDigests returns a manifest with the same algorithm and the supplied
// already-qualified digests. It is for immutable digest-cache composition;
// content is deliberately not rehashed. The input map is never retained.
func (m Manifest) WithDigests(digests map[string]string) (Manifest, error) {
	return m.WithDigestsContext(context.Background(), digests)
}

// WithDigestsContext composes an immutable validated digest cache while
// honoring ctx at every input-map boundary.
func (m Manifest) WithDigestsContext(ctx context.Context, digests map[string]string) (Manifest, error) {
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	name, err := hashAlgorithmName(m.algorithm)
	if err != nil || m.digests == nil || m.digestSize <= 0 {
		return Manifest{}, fmt.Errorf("%w: invalid manifest digest specification", ErrInvalidManifest)
	}
	digestSize := m.digestSize
	out := Manifest{algorithm: m.algorithm, digestSize: digestSize, digests: make(map[string]string, len(digests))}
	paths, err := sortedManifestMapPathsContext(ctx, digests)
	if err != nil {
		return Manifest{}, err
	}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return Manifest{}, err
		}
		digest := digests[path]
		if err := validateManifestPath(path); err != nil {
			return Manifest{}, err
		}
		if !qualifiedDigestValid(digest, name, digestSize) {
			return Manifest{}, fmt.Errorf("%w: invalid digest for %q", ErrInvalidManifest, path)
		}
		out.digests[path] = digest
	}
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	return out, nil
}
func (m Manifest) Valid() bool {
	return m.ValidateContext(context.Background()) == nil
}

// ValidateContext verifies every cached path and digest while honoring ctx.
func (m Manifest) ValidateContext(ctx context.Context) error {
	_, err := m.PathsContext(ctx)
	return err
}

// PathsContext validates and returns the exact lexical manifest path set.
func (m Manifest) PathsContext(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name, err := hashAlgorithmName(m.algorithm)
	if err != nil || m.digests == nil || m.digestSize <= 0 {
		return nil, fmt.Errorf("%w: invalid manifest digest specification", ErrInvalidManifest)
	}
	paths, err := sortedManifestMapPathsContext(ctx, m.digests)
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		digest := m.digests[path]
		if validateManifestPath(path) != nil || !qualifiedDigestValid(digest, name, m.digestSize) {
			return nil, fmt.Errorf("%w: invalid digest for %q", ErrInvalidManifest, path)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return paths, nil
}

func sortedManifestMapPathsContext(ctx context.Context, values map[string]string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(values))
	for path := range values {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return paths, nil
}

func (m Manifest) Put(path string, content []byte) (Manifest, error) {
	return m.PutContext(context.Background(), path, content)
}

// PutContext returns a manifest with content cached under path. It propagates
// cancellation to ContextHashAlgorithm implementations and while hashing data.
func (m Manifest) PutContext(ctx context.Context, path string, content []byte) (Manifest, error) {
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	if err := m.ValidateContext(ctx); err != nil {
		return Manifest{}, err
	}
	if err := validateManifestPath(path); err != nil {
		return Manifest{}, err
	}
	n, err := m.CloneContext(ctx)
	if err != nil {
		return Manifest{}, err
	}
	digest, err := digestContentContext(ctx, n.algorithm, content)
	if err != nil {
		return Manifest{}, manifestHashError(err)
	}
	if !qualifiedDigestValid(digest, n.AlgorithmName(), n.digestSize) {
		return Manifest{}, errors.Join(ErrInvalidManifest, ErrInvalidHashAlgorithm)
	}
	n.digests[path] = digest
	return n, nil
}
func (m Manifest) Delete(path string) Manifest { n := m.Clone(); delete(n.digests, path); return n }
func (m Manifest) Rename(from, to string) (Manifest, error) {
	return m.RenameContext(context.Background(), from, to)
}

// RenameContext returns a manifest with one cached path renamed.
func (m Manifest) RenameContext(ctx context.Context, from, to string) (Manifest, error) {
	if err := m.ValidateContext(ctx); err != nil {
		return Manifest{}, err
	}
	if err := validateManifestPath(to); err != nil {
		return Manifest{}, err
	}
	d, ok := m.digests[from]
	if !ok {
		return Manifest{}, fmt.Errorf("%w: missing path %q", ErrInvalidManifest, from)
	}
	n, err := m.CloneContext(ctx)
	if err != nil {
		return Manifest{}, err
	}
	delete(n.digests, from)
	n.digests[to] = d
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	return n, nil
}
func (m Manifest) Revision() (Revision, error) {
	return m.RevisionContext(context.Background())
}
func (m Manifest) RevisionContext(ctx context.Context) (Revision, error) {
	revision, _, err := m.RevisionPathsContext(ctx)
	return revision, err
}

// RevisionPathsContext computes the revision and returns the exact sorted
// manifest dependency set from the same cancellable traversal.
func (m Manifest) RevisionPathsContext(ctx context.Context) (Revision, []string, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	if m.digests == nil {
		return "", nil, fmt.Errorf("%w: invalid manifest", ErrInvalidManifest)
	}
	name, digestSize, h, err := newHashDigestSpec(ctx, m.algorithm)
	if err != nil {
		return "", nil, manifestHashError(err)
	}
	if m.digestSize <= 0 || digestSize != m.digestSize {
		return "", nil, fmt.Errorf("%w: inconsistent digest size", ErrInvalidManifest)
	}
	paths, err := m.PathsContext(ctx)
	if err != nil {
		return "", nil, err
	}
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			return "", nil, err
		}
		d := m.digests[p]
		raw, _ := hex.DecodeString(d[len(name)+1:])
		if err := writeHashLengthPrefixed(h, []byte(p)); err != nil {
			return "", nil, manifestHashError(err)
		}
		if err := writeHashLengthPrefixed(h, raw); err != nil {
			return "", nil, manifestHashError(err)
		}
	}
	sum, err := sumHash(h)
	if err != nil {
		return "", nil, manifestHashError(err)
	}
	if len(sum) != digestSize {
		return "", nil, errors.Join(ErrInvalidManifest, ErrInvalidHashAlgorithm)
	}
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	return Revision(name + ":" + hex.EncodeToString(sum)), paths, nil
}

func qualifiedDigestValid(digest, name string, size int) bool {
	if size <= 0 || len(digest) <= len(name) || digest[:len(name)] != name || digest[len(name)] != ':' {
		return false
	}
	raw := digest[len(name)+1:]
	return len(raw) == size*2 && validHexDigest(raw)
}

func hashAlgorithmDigestSpec(ctx context.Context, algorithm HashAlgorithm) (string, int, error) {
	name, size, _, err := newHashDigestSpec(ctx, algorithm)
	return name, size, err
}

func newHashDigestSpec(ctx context.Context, algorithm HashAlgorithm) (string, int, hash.Hash, error) {
	name, err := hashAlgorithmName(algorithm)
	if err != nil {
		return "", 0, nil, err
	}
	h, err := newHashContextValidated(ctx, algorithm)
	if err != nil {
		return "", 0, nil, err
	}
	size, err := safeHashSize(h)
	if err != nil {
		return "", 0, nil, err
	}
	if size <= 0 {
		return "", 0, nil, fmt.Errorf("%w: non-positive digest size", ErrInvalidHashAlgorithm)
	}
	return name, size, h, nil
}

func safeHashSize(h hash.Hash) (size int, err error) {
	defer func() {
		if recover() != nil {
			size = 0
			err = fmt.Errorf("%w: Size panicked", ErrInvalidHashAlgorithm)
		}
	}()
	return h.Size(), nil
}

func RevisionFromManifest(entries []ManifestEntry) (Revision, error) {
	m, err := NewManifest(entries)
	if err != nil {
		return "", err
	}
	return m.Revision()
}
func RevisionFromManifestWithAlgorithm(entries []ManifestEntry, algorithm HashAlgorithm) (Revision, error) {
	m, err := NewManifestWithAlgorithm(entries, algorithm)
	if err != nil {
		return "", err
	}
	return m.Revision()
}

func validateManifestPath(path string) error {
	if err := bundle.ValidateRevisionPath(path); err != nil {
		return fmt.Errorf("%w: invalid path %q", ErrInvalidManifest, path)
	}
	return nil
}

type byteWriter interface{ Write([]byte) (int, error) }

// hashWriter is the hash.Hash panic boundary. Third-party hash implementations
// are configuration input, so a panic must never escape a store operation.
type hashWriter struct{ hash.Hash }

func (w hashWriter) Write(value []byte) (n int, err error) {
	defer func() {
		if recover() != nil {
			n = 0
			err = fmt.Errorf("%w: Write panicked", ErrInvalidHashAlgorithm)
		}
	}()
	return w.Hash.Write(value)
}

func writeHashAll(h hash.Hash, value []byte) error { return writeAll(hashWriter{Hash: h}, value) }

func writeHashLengthPrefixed(h hash.Hash, value []byte) error {
	return writeLengthPrefixed(hashWriter{Hash: h}, value)
}

func sumHash(h hash.Hash) (sum []byte, err error) {
	defer func() {
		if recover() != nil {
			sum = nil
			err = fmt.Errorf("%w: Sum panicked", ErrInvalidHashAlgorithm)
		}
	}()
	return h.Sum(nil), nil
}

func writeLengthPrefixed(w byteWriter, value []byte) error {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	if err := writeAll(w, length[:]); err != nil {
		return err
	}
	return writeAll(w, value)
}

func writeAll(w byteWriter, value []byte) error {
	n, err := w.Write(value)
	if n != len(value) {
		if err != nil {
			return errors.Join(err, io.ErrShortWrite)
		}
		return io.ErrShortWrite
	}
	return err
}

func newHashContext(ctx context.Context, algorithm HashAlgorithm) (hash.Hash, error) {
	if err := validateHashAlgorithm(algorithm); err != nil {
		return nil, err
	}
	if contextual, ok := algorithm.(ContextHashAlgorithm); ok {
		h, err := callNewContext(contextual, ctx)
		if err != nil {
			return nil, err
		}
		if isNilInterface(h) {
			return nil, fmt.Errorf("%w: constructor returned nil hash", ErrInvalidHashAlgorithm)
		}
		return h, nil
	}
	h, err := callNew(algorithm)
	if err != nil {
		return nil, err
	}
	if isNilInterface(h) {
		return nil, fmt.Errorf("%w: constructor returned nil hash", ErrInvalidHashAlgorithm)
	}
	return h, nil
}

// ValidateHashAlgorithm verifies an algorithm can construct a usable hash.
// It is primarily for backend configuration validation.
func ValidateHashAlgorithm(algorithm HashAlgorithm) error {
	_, err := ValidateHashAlgorithmName(algorithm)
	return err
}

// ValidateHashAlgorithmName verifies an algorithm and returns its validated,
// stable-at-call-time name. Backends which persist that name should call this
// once during setup and retain the result rather than calling Name again at a
// durable boundary.
func ValidateHashAlgorithmName(algorithm HashAlgorithm) (string, error) {
	name, err := hashAlgorithmName(algorithm)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidManifest, ErrInvalidHashAlgorithm)
	}
	if _, err := newHashContextValidated(context.Background(), algorithm); err != nil {
		return "", err
	}
	return name, nil
}

// newHashContextValidated is newHashContext after the caller has already
// validated Name. Keeping it separate prevents setup code from invoking a
// stateful Name implementation twice.
func newHashContextValidated(ctx context.Context, algorithm HashAlgorithm) (hash.Hash, error) {
	if contextual, ok := algorithm.(ContextHashAlgorithm); ok {
		h, err := callNewContext(contextual, ctx)
		if err != nil {
			return nil, err
		}
		if isNilInterface(h) {
			return nil, fmt.Errorf("%w: constructor returned nil hash", ErrInvalidHashAlgorithm)
		}
		return h, nil
	}
	h, err := callNew(algorithm)
	if err != nil {
		return nil, err
	}
	if isNilInterface(h) {
		return nil, fmt.Errorf("%w: constructor returned nil hash", ErrInvalidHashAlgorithm)
	}
	return h, nil
}

func validateHashAlgorithm(algorithm HashAlgorithm) error {
	if _, err := hashAlgorithmName(algorithm); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidManifest, ErrInvalidHashAlgorithm)
	}
	return nil
}

func manifestHashError(err error) error {
	if errors.Is(err, ErrInvalidHashAlgorithm) {
		return errors.Join(ErrInvalidManifest, err)
	}
	return err
}

func hashAlgorithmName(algorithm HashAlgorithm) (name string, err error) {
	if isNilInterface(algorithm) {
		return "", fmt.Errorf("%w: nil algorithm", ErrInvalidHashAlgorithm)
	}
	defer func() {
		if recover() != nil {
			name = ""
			err = fmt.Errorf("%w: Name panicked", ErrInvalidHashAlgorithm)
		}
	}()
	name = algorithm.Name()
	if !validAlgorithmName(name) {
		return "", fmt.Errorf("%w: invalid name", ErrInvalidHashAlgorithm)
	}
	return name, nil
}

func callNew(algorithm HashAlgorithm) (h hash.Hash, err error) {
	defer func() {
		if recover() != nil {
			h = nil
			err = fmt.Errorf("%w: New panicked", ErrInvalidHashAlgorithm)
		}
	}()
	return algorithm.New(), nil
}

func callNewContext(algorithm ContextHashAlgorithm, ctx context.Context) (h hash.Hash, err error) {
	defer func() {
		if recover() != nil {
			h = nil
			err = fmt.Errorf("%w: NewContext panicked", ErrInvalidHashAlgorithm)
		}
	}()
	return algorithm.NewContext(ctx)
}

func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}
