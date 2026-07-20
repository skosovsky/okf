// Package mutation plans semantic OKF changes entirely in memory.
package mutation

import (
	"context"
	"fmt"
	"sort"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
)

// Overlay is an immutable-base, copy-on-write bundle.Source.  Every mutating
// method copies its input, and every read returns a fresh copy.  In particular,
// Rename installs a tombstone at From so it can never fall back to the base.
type Overlay struct {
	base        bundle.Source
	changed     map[string][]byte
	deleted     map[string]struct{}
	manifest    store.Manifest
	hasManifest bool
}

// NewOverlay returns an empty overlay over base.
func NewOverlay(base bundle.Source) *Overlay {
	o := &Overlay{base: base, changed: make(map[string][]byte), deleted: make(map[string]struct{})}
	if source, ok := base.(store.ManifestSource); ok {
		m := source.Manifest()
		if m.Valid() && manifestPathsAreSourcePaths(m) {
			o.manifest, o.hasManifest = m.Clone(), true
		}
	}
	return o
}

func (o *Overlay) clone() *Overlay {
	if o == nil {
		return nil
	}
	cloned := &Overlay{
		base:        o.base,
		changed:     make(map[string][]byte, len(o.changed)),
		deleted:     make(map[string]struct{}, len(o.deleted)),
		hasManifest: o.hasManifest,
	}
	for name, data := range o.changed {
		cloned.changed[name] = append([]byte(nil), data...)
	}
	for name := range o.deleted {
		cloned.deleted[name] = struct{}{}
	}
	if o.hasManifest {
		cloned.manifest = o.manifest.Clone()
	}
	return cloned
}

// Update replaces an existing or staged file. It is an alias for Put.
func (o *Overlay) Update(name string, content []byte) error { return o.Put(name, content) }

// Create installs a new file and rejects an existing visible path.
func (o *Overlay) Create(ctx context.Context, name string, content []byte) error {
	name, err := sourcePath(name)
	if err != nil {
		return err
	}
	if _, err := o.ReadFile(ctx, name); err == nil {
		return fmt.Errorf("mutation: file already exists %q", name)
	}
	return o.putContext(ctx, name, content)
}

// Put stages content at name.
func (o *Overlay) Put(name string, content []byte) error {
	return o.PutContext(context.Background(), name, content)
}

// PutContext stages content and propagates ctx to manifest hashing.
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
	if o.hasManifest {
		manifest, err := o.manifest.PutContext(ctx, name, content)
		if err != nil {
			return err
		}
		o.manifest = manifest
	}
	o.changed[name] = append([]byte(nil), content...)
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
	o.deleted[name] = struct{}{}
	if o.hasManifest {
		o.manifest = o.manifest.Delete(name)
	}
	return nil
}

// Rename moves a visible file. It never leaves a fallback at from.
func (o *Overlay) Rename(ctx context.Context, from, to string) error {
	from, err := sourcePath(from)
	if err != nil {
		return err
	}
	to, err = sourcePath(to)
	if err != nil {
		return err
	}
	if from == to {
		return nil
	}
	data, err := o.ReadFile(ctx, from)
	if err != nil {
		return err
	}
	if _, err := o.ReadFile(ctx, to); err == nil {
		return fmt.Errorf("mutation: rename target exists %q", to)
	}
	var renamed store.Manifest
	if o.hasManifest {
		renamed, err = o.manifest.Rename(from, to)
		if err != nil {
			return err
		}
	}
	// Do not route this through Put: a rename is a path-only operation and the
	// cached content digest must move without hashing the defensive read copy.
	delete(o.changed, from)
	o.deleted[from] = struct{}{}
	o.changed[to] = append([]byte(nil), data...)
	delete(o.deleted, to)
	if o.hasManifest {
		o.manifest = renamed
	}
	return nil
}

// Manifest returns a defensive digest cache when the immutable base exposed
// one. An invalid zero manifest signals that a full hash is required.
func (o *Overlay) Manifest() store.Manifest {
	if o == nil || !o.hasManifest {
		return store.Manifest{}
	}
	return o.manifest.Clone()
}

// Paths lists visible paths in lexical slash-path order.
func (o *Overlay) Paths(ctx context.Context) ([]string, error) {
	if o == nil || o.base == nil {
		return nil, fmt.Errorf("mutation: nil overlay base")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	base, err := o.base.Paths(ctx)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(base)+len(o.changed))
	for _, p := range base {
		p, err = sourcePath(p)
		if err != nil {
			return nil, err
		}
		if _, gone := o.deleted[p]; !gone {
			seen[p] = struct{}{}
		}
	}
	for p := range o.changed {
		seen[p] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
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
	if data, ok := o.changed[name]; ok {
		return append([]byte(nil), data...), nil
	}
	data, err := o.base.ReadFile(ctx, name)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), data...), nil
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
func manifestPathsAreSourcePaths(manifest store.Manifest) bool {
	for name := range manifest.Digests() {
		if _, err := sourcePath(name); err != nil {
			return false
		}
	}
	return true
}
