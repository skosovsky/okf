// Package mutation plans semantic OKF changes entirely in memory.
package mutation

import (
	"context"
	"fmt"
	"sync"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
)

// Overlay is an immutable-base, copy-on-write bundle.Source. Staged payloads
// are private and immutable: PutContext owns exactly one copy, Clone shares
// that copy, and ReadFile always returns a defensive copy. It intentionally
// remains a flat overlay rather than a parent chain.
type Overlay struct {
	base    bundle.Source
	changed map[string]overlayPayload
	deleted map[string]struct{}

	// baseManifest never changes. manifestChanged is only the delta over it;
	// materialization happens at the public Manifest boundary.
	baseManifest    store.Manifest
	manifestChanged map[string]string
	hasManifest     bool
	paths           *overlayPaths
}

type overlayPayload struct {
	data   []byte
	digest string
}

// overlayPaths is deliberately shared by an overlay clone tree. Failed or
// cancelled enumeration is never retained, so a later request can retry.
type overlayPaths struct {
	mu      sync.Mutex
	ready   bool
	paths   []string
	loading chan struct{}
}

// verifiedManifestSource is intentionally package-private: only snapshots
// proven against their Revision and overlays derived from such snapshots may
// authorize the digest fast path.
type verifiedManifestSource interface {
	verifiedManifestContext(context.Context) (store.Manifest, []string, bool, error)
}

// NewOverlay returns an empty overlay over base.
func NewOverlay(base bundle.Source) *Overlay {
	o, err := NewOverlayContext(context.Background(), base)
	if err == nil {
		return o
	}
	// The context-free convenience constructor cannot report a provider error;
	// retain a usable overlay and disable only the optional manifest fast path.
	return &Overlay{
		base:            base,
		changed:         make(map[string]overlayPayload),
		deleted:         make(map[string]struct{}),
		manifestChanged: make(map[string]string),
		paths:           &overlayPaths{},
	}
}

// NewOverlayContext returns an empty overlay and captures an optional manifest
// through cancellable validation. Arbitrary ManifestSource hints are ignored;
// an inconsistent verified snapshot is an error, as are cancellation and
// provider failures.
func NewOverlayContext(ctx context.Context, base bundle.Source) (*Overlay, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	o := &Overlay{
		base:            base,
		changed:         make(map[string]overlayPayload),
		deleted:         make(map[string]struct{}),
		manifestChanged: make(map[string]string),
		paths:           &overlayPaths{},
	}
	if m, paths, ok, err := verifiedSourceManifestContext(ctx, base); err != nil {
		return nil, err
	} else if ok {
		if manifestPathsAreSourcePathsContext(ctx, paths) {
			o.baseManifest, o.hasManifest = m, true
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return o, nil
}

func (o *Overlay) cloneContext(ctx context.Context) (*Overlay, error) {
	if o == nil {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cloned := &Overlay{
		base:            o.base,
		changed:         make(map[string]overlayPayload, len(o.changed)),
		deleted:         make(map[string]struct{}, len(o.deleted)),
		baseManifest:    o.baseManifest,
		manifestChanged: make(map[string]string, len(o.manifestChanged)),
		hasManifest:     o.hasManifest,
		paths:           o.paths,
	}
	for name, payload := range o.changed {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cloned.changed[name] = payload
	}
	for name := range o.deleted {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cloned.deleted[name] = struct{}{}
	}
	for name, digest := range o.manifestChanged {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cloned.manifestChanged[name] = digest
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return cloned, nil
}

// Update replaces an existing or staged file. It is an alias for Put.
func (o *Overlay) Update(name string, content []byte) error { return o.Put(name, content) }

// Create installs a new file and rejects an existing visible path.
func (o *Overlay) Create(ctx context.Context, name string, content []byte) error {
	name, err := sourcePath(name)
	if err != nil {
		return err
	}
	exists, err := o.visiblePath(ctx, name)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("mutation: file already exists %q", name)
	}
	return o.putContext(ctx, name, content)
}

// Put stages content at name.
func (o *Overlay) Put(name string, content []byte) error {
	return o.PutContext(context.Background(), name, content)
}

// PutContext stages content. When a base manifest is available, it hashes the
// single overlay-owned payload copy with its context-aware hash algorithm.
func (o *Overlay) PutContext(ctx context.Context, name string, content []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	name, err := sourcePath(name)
	if err != nil {
		return err
	}
	return o.putContext(ctx, name, content)
}

func (o *Overlay) putContext(ctx context.Context, name string, content []byte) error {
	if o == nil {
		return fmt.Errorf("mutation: nil overlay")
	}
	owned, err := copyBytesContext(ctx, content)
	if err != nil {
		return err
	}
	payload := overlayPayload{data: owned}
	if o.hasManifest {
		digest, err := o.baseManifest.DigestContentContext(ctx, owned)
		if err != nil {
			return err
		}
		payload.digest = digest
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if o.hasManifest {
		o.manifestChanged[name] = payload.digest
	}
	o.changed[name] = payload
	delete(o.deleted, name)
	return nil
}

// Delete hides a base or staged file.
func (o *Overlay) Delete(name string) error {
	name, err := sourcePath(name)
	if err != nil {
		return err
	}
	if o == nil {
		return fmt.Errorf("mutation: nil overlay")
	}
	delete(o.changed, name)
	delete(o.manifestChanged, name)
	o.deleted[name] = struct{}{}
	return nil
}

// Rename moves a visible file. A staged source reuses its immutable payload
// and digest. A base source is read once and adopted as the owned Source
// payload; it is never routed through ReadFile/Put and therefore not copied or
// rehashed a second time.
func (o *Overlay) Rename(ctx context.Context, from, to string) error {
	if o == nil || o.base == nil {
		return fmt.Errorf("mutation: nil overlay base")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	from, err := sourcePath(from)
	if err != nil {
		return err
	}
	to, err = sourcePath(to)
	if err != nil {
		return err
	}
	if from == to {
		exists, err := o.visiblePath(ctx, from)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("mutation: file not found %q", from)
		}
		return ctx.Err()
	}
	targetExists, err := o.visiblePath(ctx, to)
	if err != nil {
		return err
	}
	if targetExists {
		return fmt.Errorf("mutation: rename target exists %q", to)
	}
	sourceExists, err := o.visiblePath(ctx, from)
	if err != nil {
		return err
	}
	if !sourceExists {
		return fmt.Errorf("mutation: file not found %q", from)
	}
	payload, staged := o.changed[from]
	if !staged {
		data, err := o.base.ReadFile(ctx, from)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		payload.data = data
		if o.hasManifest {
			var ok bool
			payload.digest, ok = o.baseManifest.Digest(from)
			if !ok {
				return fmt.Errorf("mutation: manifest missing source path %q", from)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	delete(o.changed, from)
	delete(o.manifestChanged, from)
	o.deleted[from] = struct{}{}
	o.changed[to] = payload
	delete(o.deleted, to)
	if o.hasManifest {
		o.manifestChanged[to] = payload.digest
	}
	return nil
}

// visiblePath proves existence from the overlay delta and the complete base
// path set. It never treats a read, permission, traversal, or cancellation
// error as evidence of absence.
func (o *Overlay) visiblePath(ctx context.Context, name string) (bool, error) {
	if o == nil || o.base == nil {
		return false, fmt.Errorf("mutation: nil overlay base")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if _, gone := o.deleted[name]; gone {
		return false, nil
	}
	if _, changed := o.changed[name]; changed {
		return true, nil
	}
	paths, err := o.cachedBasePaths(ctx)
	if err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	at, err := searchStringsContext(ctx, paths, name)
	if err != nil {
		return false, err
	}
	if at == len(paths) {
		return false, nil
	}
	order, err := compareStringsContext(ctx, paths[at], name)
	return order == 0, err
}

// Manifest materializes a defensive digest cache only at this public boundary.
// An invalid zero manifest signals that a full hash is required.
func (o *Overlay) Manifest() store.Manifest {
	manifest, _ := o.ManifestContext(context.Background())
	return manifest
}

// ManifestContext materializes the immutable base plus staged digest delta
// while honoring cancellation at every map-copy boundary.
func (o *Overlay) ManifestContext(ctx context.Context) (store.Manifest, error) {
	if err := ctx.Err(); err != nil {
		return store.Manifest{}, err
	}
	if o == nil || !o.hasManifest {
		return store.Manifest{}, nil
	}
	digests, err := o.baseManifest.DigestsContext(ctx)
	if err != nil {
		return store.Manifest{}, err
	}
	for name := range o.deleted {
		if err := ctx.Err(); err != nil {
			return store.Manifest{}, err
		}
		delete(digests, name)
	}
	for name, digest := range o.manifestChanged {
		if err := ctx.Err(); err != nil {
			return store.Manifest{}, err
		}
		digests[name] = digest
	}
	materialized, err := o.baseManifest.WithDigestsContext(ctx, digests)
	if err != nil {
		return store.Manifest{}, err
	}
	return materialized, nil
}

func (o *Overlay) verifiedManifestContext(ctx context.Context) (store.Manifest, []string, bool, error) {
	if o == nil || !o.hasManifest {
		return store.Manifest{}, nil, false, nil
	}
	manifest, err := o.ManifestContext(ctx)
	if err != nil {
		return store.Manifest{}, nil, false, err
	}
	paths, err := o.Paths(ctx)
	if err != nil {
		return store.Manifest{}, nil, false, err
	}
	return manifest, paths, true, nil
}

// Paths lists visible paths in lexical slash-path order.
func (o *Overlay) Paths(ctx context.Context) ([]string, error) {
	if o == nil || o.base == nil {
		return nil, fmt.Errorf("mutation: nil overlay base")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	base, err := o.cachedBasePaths(ctx)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(base)+len(o.changed))
	for _, p := range base {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, gone := o.deleted[p]; !gone {
			seen[p] = struct{}{}
		}
	}
	for p := range o.changed {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		seen[p] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := sortStringsContext(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (o *Overlay) cachedBasePaths(ctx context.Context) ([]string, error) {
	cache := o.paths
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cache.mu.Lock()
		if cache.ready {
			// cache.paths is immutable after ready is published. Callers are
			// internal and only iterate it; public Paths builds its own slice.
			out := cache.paths
			cache.mu.Unlock()
			return out, nil
		}
		if wait := cache.loading; wait != nil {
			cache.mu.Unlock()
			select {
			case <-wait:
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		cache.loading = make(chan struct{})
		wait := cache.loading
		cache.mu.Unlock()

		paths, err := o.base.Paths(ctx)
		if err == nil {
			if err = ctx.Err(); err == nil {
				for _, p := range paths {
					if err = ctx.Err(); err != nil {
						break
					}
					if _, pathErr := sourcePath(p); pathErr != nil {
						err = pathErr
						break
					}
				}
				if err == nil {
					err = sortStringsContext(ctx, paths)
				}
			}
		}
		var ownedPaths []string
		if err == nil {
			ownedPaths = make([]string, 0, len(paths))
			for _, name := range paths {
				if err = ctx.Err(); err != nil {
					break
				}
				ownedPaths = append(ownedPaths, name)
			}
		}
		cache.mu.Lock()
		if err == nil {
			cache.paths = ownedPaths
			cache.ready = true
		}
		cache.loading = nil
		close(wait)
		cache.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return cache.paths, nil
	}
}

// ReadFile returns staged content, a defensive copy from base, or its base error.
func (o *Overlay) ReadFile(ctx context.Context, name string) ([]byte, error) {
	name, err := sourcePath(name)
	if err != nil {
		return nil, err
	}
	if o == nil || o.base == nil {
		return nil, fmt.Errorf("mutation: nil overlay base")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, gone := o.deleted[name]; gone {
		return nil, fmt.Errorf("mutation: file not found %q", name)
	}
	if payload, ok := o.changed[name]; ok {
		return copyBytesContext(ctx, payload.data)
	}
	data, err := o.base.ReadFile(ctx, name)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// bundle.Source transfers ownership of returned bytes to its caller; after
	// the context check this slice is already a defensive overlay result.
	return data, nil
}

func copyBytesContext(ctx context.Context, source []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]byte, len(source))
	const chunk = 64 << 10
	for at := 0; at < len(source); at += chunk {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := at + chunk
		if end > len(source) {
			end = len(source)
		}
		copy(out[at:end], source[at:end])
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func sourcePath(name string) (string, error) {
	if err := bundle.ValidateRevisionPath(name); err != nil {
		return "", invalidSourcePath(name)
	}
	return name, nil
}

func invalidSourcePath(name string) error {
	return fmt.Errorf("%w: invalid bundle source path %q", store.ErrInvalidChangeSet, name)
}

// manifestPathsAreSourcePaths prevents metadata from entering the cached
// revision through a ManifestSource without first being visible to Paths.
func manifestPathsAreSourcePathsContext(ctx context.Context, paths []string) bool {
	for _, name := range paths {
		if err := ctx.Err(); err != nil {
			return false
		}
		if _, err := sourcePath(name); err != nil {
			return false
		}
	}
	return true
}

func sourceManifestContext(ctx context.Context, source bundle.Source) (store.Manifest, bool, error) {
	if contextual, ok := source.(store.ContextManifestSource); ok {
		manifest, err := contextual.ManifestContext(ctx)
		return manifest, true, err
	}
	plain, ok := source.(store.ManifestSource)
	if !ok {
		return store.Manifest{}, false, nil
	}
	if err := ctx.Err(); err != nil {
		return store.Manifest{}, false, err
	}
	manifest := plain.Manifest()
	if err := ctx.Err(); err != nil {
		return store.Manifest{}, false, err
	}
	return manifest, true, nil
}

func verifiedSourceManifestContext(ctx context.Context, source bundle.Source) (store.Manifest, []string, bool, error) {
	if verified, ok := source.(verifiedManifestSource); ok {
		manifest, paths, available, err := verified.verifiedManifestContext(ctx)
		if err != nil || !available {
			return store.Manifest{}, nil, available, err
		}
		return verifyManifestPathSetContext(ctx, manifest, paths)
	}
	snapshot, ok := source.(store.Snapshot)
	if !ok {
		return store.Manifest{}, nil, false, nil
	}
	manifest, available, err := sourceManifestContext(ctx, source)
	if err != nil || !available {
		return store.Manifest{}, nil, available, err
	}
	revision, manifestPaths, err := manifest.RevisionPathsContext(ctx)
	if err != nil {
		return store.Manifest{}, nil, false, err
	}
	if revision != snapshot.Revision() {
		return store.Manifest{}, nil, false, fmt.Errorf("%w: snapshot revision and manifest disagree", store.ErrInvalidManifest)
	}
	sourcePaths, err := source.Paths(ctx)
	if err != nil {
		return store.Manifest{}, nil, false, err
	}
	if err := validateAndSortSourcePathsContext(ctx, sourcePaths); err != nil {
		return store.Manifest{}, nil, false, err
	}
	equal, err := equalStringSlicesContext(ctx, manifestPaths, sourcePaths)
	if err != nil {
		return store.Manifest{}, nil, false, err
	}
	if !equal {
		return store.Manifest{}, nil, false, fmt.Errorf("%w: snapshot paths and manifest disagree", store.ErrInvalidManifest)
	}
	return manifest, sourcePaths, true, nil
}

func verifyManifestPathSetContext(ctx context.Context, manifest store.Manifest, sourcePaths []string) (store.Manifest, []string, bool, error) {
	manifestPaths, err := manifest.PathsContext(ctx)
	if err != nil {
		return store.Manifest{}, nil, false, err
	}
	paths := make([]string, 0, len(sourcePaths))
	for _, name := range sourcePaths {
		if err := ctx.Err(); err != nil {
			return store.Manifest{}, nil, false, err
		}
		paths = append(paths, name)
	}
	if err := validateAndSortSourcePathsContext(ctx, paths); err != nil {
		return store.Manifest{}, nil, false, err
	}
	equal, err := equalStringSlicesContext(ctx, manifestPaths, paths)
	if err != nil {
		return store.Manifest{}, nil, false, err
	}
	if !equal {
		return store.Manifest{}, nil, false, fmt.Errorf("%w: source paths and manifest disagree", store.ErrInvalidManifest)
	}
	return manifest, paths, true, nil
}

func validateAndSortSourcePathsContext(ctx context.Context, paths []string) error {
	for _, name := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := sourcePath(name); err != nil {
			return err
		}
	}
	return sortStringsContext(ctx, paths)
}

func equalStringSlicesContext(ctx context.Context, left, right []string) (bool, error) {
	if len(left) != len(right) {
		return false, nil
	}
	for i := range left {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		order, err := compareStringsContext(ctx, left[i], right[i])
		if err != nil {
			return false, err
		}
		if order != 0 {
			return false, nil
		}
	}
	return true, ctx.Err()
}
