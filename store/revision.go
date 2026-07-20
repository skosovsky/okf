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
// Implementations must return a fresh hash on every New call. SHA-256 is the
// default; the algorithm name is part of every produced digest and revision.
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
	algorithm HashAlgorithm
	digests   map[string]string
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
	if err := validateHashAlgorithm(algorithm); err != nil {
		return Manifest{}, err
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
		m.digests[e.Path] = digest
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
	return name + ":" + hex.EncodeToString(sum), nil
}
func (m Manifest) Clone() Manifest {
	return Manifest{algorithm: m.algorithm, digests: cloneDigests(m.digests)}
}
func (m Manifest) Digests() map[string]string { return cloneDigests(m.digests) }
func cloneDigests(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for p, d := range in {
		out[p] = d
	}
	return out
}
func (m Manifest) Digest(path string) (string, bool) { d, ok := m.digests[path]; return d, ok }
func (m Manifest) Valid() bool {
	_, err := hashAlgorithmName(m.algorithm)
	return err == nil && m.digests != nil
}
func (m Manifest) Put(path string, content []byte) (Manifest, error) {
	return m.PutContext(context.Background(), path, content)
}

// PutContext returns a manifest with content cached under path. It propagates
// cancellation to ContextHashAlgorithm implementations and while hashing data.
func (m Manifest) PutContext(ctx context.Context, path string, content []byte) (Manifest, error) {
	if err := validateHashAlgorithm(m.algorithm); err != nil {
		return Manifest{}, fmt.Errorf("%w: %w", ErrInvalidManifest, err)
	}
	if m.digests == nil {
		return Manifest{}, fmt.Errorf("%w: invalid manifest", ErrInvalidManifest)
	}
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	if err := validateManifestPath(path); err != nil {
		return Manifest{}, err
	}
	n := m.Clone()
	digest, err := digestContentContext(ctx, n.algorithm, content)
	if err != nil {
		return Manifest{}, manifestHashError(err)
	}
	n.digests[path] = digest
	return n, nil
}
func (m Manifest) Delete(path string) Manifest { n := m.Clone(); delete(n.digests, path); return n }
func (m Manifest) Rename(from, to string) (Manifest, error) {
	if err := validateHashAlgorithm(m.algorithm); err != nil {
		return Manifest{}, fmt.Errorf("%w: %w", ErrInvalidManifest, err)
	}
	if m.digests == nil {
		return Manifest{}, fmt.Errorf("%w: invalid manifest", ErrInvalidManifest)
	}
	if err := validateManifestPath(to); err != nil {
		return Manifest{}, err
	}
	d, ok := m.digests[from]
	if !ok {
		return Manifest{}, fmt.Errorf("%w: missing path %q", ErrInvalidManifest, from)
	}
	n := m.Delete(from)
	n.digests[to] = d
	return n, nil
}
func (m Manifest) Revision() (Revision, error) {
	return m.RevisionContext(context.Background())
}
func (m Manifest) RevisionContext(ctx context.Context) (Revision, error) {
	if err := validateHashAlgorithm(m.algorithm); err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidManifest, err)
	}
	if m.digests == nil {
		return "", fmt.Errorf("%w: invalid manifest", ErrInvalidManifest)
	}
	name, err := hashAlgorithmName(m.algorithm)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidManifest, err)
	}
	paths := make([]string, 0, len(m.digests))
	for p := range m.digests {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	h, err := newHashContext(ctx, m.algorithm)
	if err != nil {
		return "", manifestHashError(err)
	}
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if err := validateManifestPath(p); err != nil {
			return "", err
		}
		d := m.digests[p]
		parts := strings.Split(d, ":")
		if len(parts) != 2 || parts[0] != name || !validHexDigest(parts[1]) {
			return "", fmt.Errorf("%w: invalid digest for %q", ErrInvalidManifest, p)
		}
		raw, _ := hex.DecodeString(parts[1])
		if err := writeHashLengthPrefixed(h, []byte(p)); err != nil {
			return "", manifestHashError(err)
		}
		if err := writeHashLengthPrefixed(h, raw); err != nil {
			return "", manifestHashError(err)
		}
	}
	sum, err := sumHash(h)
	if err != nil {
		return "", manifestHashError(err)
	}
	return Revision(name + ":" + hex.EncodeToString(sum)), nil
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
