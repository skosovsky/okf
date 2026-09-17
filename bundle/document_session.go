package bundle

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DocumentSession is one path-scoped, immutable document capture. It owns one
// pinned FileSystemSource and the publication lock for its physical scope until
// Close. A session authorizes at most one rewrite attempt.
type DocumentSession struct {
	state *documentSessionState
}

// documentSessionState is indirect so accidental value copies of the public
// handle still share close and one-shot rewrite ownership.
type documentSessionState struct {
	mu sync.Mutex

	source      *FileSystemSource
	releaseLock func()
	parent      *os.Root
	closing     bool
	closed      bool
	closeDone   chan struct{}
	closeErr    error
	attempted   bool

	rootPath     string
	targetPath   string
	relativePath string
	selector     string
	bytes        []byte
	document     Document
	resolution   VersionResolution

	target      documentFileGuard
	rootIndex   documentFileGuard
	parentInfo  os.FileInfo
	ancestors   []documentDirectoryGuard
	discovery   documentScopeDiscovery
	publishHook documentPublishHooks
}

type documentFileGuard struct {
	relative string
	present  bool
	info     os.FileInfo
	mode     fs.FileMode
	size     int64
	modTime  time.Time
	data     []byte
}

type documentDirectoryGuard struct {
	relative string
	info     os.FileInfo
}

type documentScopeDiscovery struct {
	root          string
	target        string
	targetPresent bool
	targetInfo    os.FileInfo
	directories   []documentAbsoluteDirectoryGuard
	indexes       []documentAbsoluteIndexGuard
}

type documentAbsoluteDirectoryGuard struct {
	path string
	info os.FileInfo
}

type documentAbsoluteIndexGuard struct {
	path    string
	present bool
	info    os.FileInfo
	mode    fs.FileMode
	size    int64
	modTime time.Time
	data    []byte
}

// OpenDocumentSessionContext discovers the document's version scope, captures
// it with exactly one Load from exactly one pinned FileSystemSource, and holds
// the physical-root publication lock until Close. selector is an assertion;
// the empty string means automatic version resolution.
func OpenDocumentSessionContext(
	ctx context.Context,
	targetPath string,
	selector string,
) (*DocumentSession, error) {
	return openDocumentSessionContext(ctx, targetPath, selector, documentSessionHooks{})
}

type documentSessionHooks struct {
	afterLock func() error
	afterLoad func() error
	publish   documentPublishHooks
}

func openDocumentSessionContext(
	ctx context.Context,
	targetPath string,
	selector string,
	hooks documentSessionHooks,
) (_ *DocumentSession, err error) {
	defer func() {
		err = mapDocumentPublicError(err)
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	logicalTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return nil, fmt.Errorf("resolve document path: %w", err)
	}
	logicalTarget = filepath.Clean(logicalTarget)
	discovery, err := discoverDocumentScope(ctx, targetPath)
	if err != nil {
		return nil, err
	}
	source := &FileSystemSource{Root: discovery.root}
	root, err := source.openRoot()
	if err != nil {
		return nil, err
	}
	releaseLock, err := acquirePublicationLock(root)
	if err != nil {
		_ = source.Close()
		switch {
		case errors.Is(err, ErrPublicationOwnershipConflict):
			return nil, documentConflict("document publication ownership is already held", err)
		case errors.Is(err, ErrPublicationCapabilityUnsupported):
			return nil, documentConflict("document publication ownership is unavailable", err)
		default:
			return nil, documentConflict("document publication ownership could not be acquired", err)
		}
	}
	var parent *os.Root
	defer func() {
		if err == nil {
			return
		}
		if parent != nil {
			_ = parent.Close()
		}
		_ = source.Close()
		releaseLock()
	}()
	if hooks.afterLock != nil {
		if err := hooks.afterLock(); err != nil {
			return nil, err
		}
	}
	lockedDiscovery, err := discoverDocumentScope(ctx, discovery.target)
	if err != nil {
		return nil, documentConflict("scope changed after lock acquisition", err)
	}
	if err := compareDocumentScopeDiscoveries(ctx, discovery, lockedDiscovery); err != nil {
		return nil, err
	}
	recoveredTargets, err := inspectGlobalDocumentRecoveryNamespace(ctx, root, hooks.publish)
	if err != nil {
		return nil, documentConflict("recover document publication", err)
	}

	recoveredDiscovery, err := discoverDocumentScope(ctx, discovery.target)
	if err != nil {
		return nil, documentConflict("scope changed during recovery", err)
	}
	recoveredRelative, err := filepath.Rel(discovery.root, discovery.target)
	if err != nil {
		return nil, err
	}
	recoveredTarget, recoveredTargetPresent := recoveredTargets[filepath.ToSlash(recoveredRelative)]
	if err := compareDocumentScopeAfterRecovery(
		ctx,
		lockedDiscovery,
		recoveredDiscovery,
		recoveredTarget,
		recoveredTargetPresent,
	); err != nil {
		return nil, err
	}

	loaded, err := Load(ctx, source)
	if err != nil {
		return nil, fmt.Errorf("load document scope: %w", err)
	}
	if hooks.afterLoad != nil {
		if err := hooks.afterLoad(); err != nil {
			return nil, err
		}
	}
	postLoadDiscovery, err := discoverDocumentScope(ctx, discovery.target)
	if err != nil {
		return nil, documentConflict("scope changed across bundle load", err)
	}
	if err := compareDocumentScopeDiscoveries(ctx, recoveredDiscovery, postLoadDiscovery); err != nil {
		return nil, err
	}
	recoveredDiscovery = postLoadDiscovery
	relativePath, err := filepath.Rel(discovery.root, discovery.target)
	if err != nil {
		return nil, err
	}
	relativePath = filepath.ToSlash(relativePath)
	if err := ValidateRevisionPath(relativePath); err != nil {
		return nil, fmt.Errorf("document path is outside revision scope: %w", err)
	}
	logicalRoot := logicalTarget
	for range strings.Split(relativePath, "/") {
		logicalRoot = filepath.Dir(logicalRoot)
	}
	capturedBytes, present, err := loaded.ReadFileContext(ctx, relativePath)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, fmt.Errorf("capture document %q: %w", discovery.target, fs.ErrNotExist)
	}
	document, err := ParseDocumentContext(ctx, string(capturedBytes))
	if err != nil {
		return nil, fmt.Errorf("parse document %q: %w", discovery.target, err)
	}
	resolution, err := loaded.VersionResolutionContext(ctx, selector)
	if err != nil {
		return nil, fmt.Errorf("resolve OKF version: %w", err)
	}

	directory, leaf := path.Split(relativePath)
	directory = strings.TrimSuffix(directory, "/")
	parent, err = openDirectory(root, directory)
	if err != nil {
		return nil, documentConflict("target parent changed during capture", err)
	}
	parentInfo, err := parent.Lstat(".")
	if err != nil || !parentInfo.IsDir() {
		return nil, documentConflict("target parent cannot be pinned", err)
	}
	target, err := captureDocumentFileGuard(ctx, parent, leaf, relativePath)
	if err != nil {
		return nil, err
	}
	if !recoveredDiscovery.targetPresent ||
		recoveredDiscovery.targetInfo == nil ||
		target.info == nil ||
		!os.SameFile(recoveredDiscovery.targetInfo, target.info) {
		return nil, documentConflict("target identity changed across bundle load", nil)
	}
	if !bytes.Equal(target.data, capturedBytes) {
		return nil, documentConflict("target changed after bundle capture", nil)
	}
	ancestors, err := captureDocumentDirectoryGuards(root, directory)
	if err != nil {
		return nil, err
	}
	rootIndex, err := captureRootIndexGuard(ctx, root, loaded)
	if err != nil {
		return nil, err
	}
	if err := verifyRootIndexLoadIdentity(recoveredDiscovery, rootIndex); err != nil {
		return nil, err
	}
	if relativePath == indexFilename &&
		(!rootIndex.present ||
			!os.SameFile(target.info, rootIndex.info) ||
			target.mode != rootIndex.mode ||
			target.size != rootIndex.size ||
			!target.modTime.Equal(rootIndex.modTime) ||
			!bytes.Equal(target.data, rootIndex.data)) {
		return nil, documentConflict("root index target and declaration dependency diverged", nil)
	}

	session := &DocumentSession{
		state: &documentSessionState{
			source:       source,
			releaseLock:  releaseLock,
			parent:       parent,
			closeDone:    make(chan struct{}),
			rootPath:     logicalRoot,
			targetPath:   logicalTarget,
			relativePath: relativePath,
			selector:     selector,
			bytes:        append([]byte(nil), capturedBytes...),
			document:     cloneDocumentValue(document),
			resolution:   resolution,
			target:       target,
			rootIndex:    rootIndex,
			parentInfo:   parentInfo,
			ancestors:    ancestors,
			discovery:    recoveredDiscovery,
			publishHook:  hooks.publish,
		},
	}
	return session, nil
}

func verifyRootIndexLoadIdentity(
	discovery documentScopeDiscovery,
	guard documentFileGuard,
) error {
	rootIndexPath := filepath.Join(discovery.root, indexFilename)
	for _, observed := range discovery.indexes {
		if observed.path != rootIndexPath {
			continue
		}
		if observed.present != guard.present {
			return documentConflict("root index presence changed across bundle load", nil)
		}
		if !observed.present {
			return nil
		}
		if observed.info == nil ||
			guard.info == nil ||
			!os.SameFile(observed.info, guard.info) ||
			observed.mode != guard.mode ||
			observed.size != guard.size ||
			!observed.modTime.Equal(guard.modTime) ||
			!bytes.Equal(observed.data, guard.data) {
			return documentConflict("root index identity changed across bundle load", nil)
		}
		return nil
	}
	if guard.present {
		return documentConflict("root index appeared outside captured bundle scope", nil)
	}
	return nil
}

// Root returns the absolute captured scope root.
func (s *DocumentSession) Root() string {
	if s == nil || s.state == nil {
		return ""
	}
	return s.state.rootPath
}

// Path returns the absolute captured document path.
func (s *DocumentSession) Path() string {
	if s == nil || s.state == nil {
		return ""
	}
	return s.state.targetPath
}

// RelativePath returns the slash path of the document inside Root.
func (s *DocumentSession) RelativePath() string {
	if s == nil || s.state == nil {
		return ""
	}
	return s.state.relativePath
}

// Bytes returns a caller-owned copy of the exact captured document bytes.
func (s *DocumentSession) Bytes() []byte {
	if s == nil || s.state == nil {
		return nil
	}
	return append([]byte(nil), s.state.bytes...)
}

// Document returns a deep copy of the parsed captured document.
func (s *DocumentSession) Document() Document {
	if s == nil || s.state == nil {
		return Document{}
	}
	return cloneDocumentValue(s.state.document)
}

// VersionResolution returns the version decision captured by the session.
func (s *DocumentSession) VersionResolution() VersionResolution {
	if s == nil || s.state == nil {
		return VersionResolution{}
	}
	return s.state.resolution
}

// RewriteContext serializes document and atomically publishes it only if every
// captured identity, byte sequence, mode, declaration dependency, and ancestor
// is unchanged. The first call consumes the session even when it fails.
func (s *DocumentSession) RewriteContext(ctx context.Context, document Document) error {
	if s == nil || s.state == nil {
		return documentConflict("nil session", nil)
	}
	state := s.state
	state.mu.Lock()
	if state.closing || state.closed {
		state.mu.Unlock()
		return documentConflict("session is closed", nil)
	}
	if state.attempted {
		state.mu.Unlock()
		return documentConflict("session is already consumed", nil)
	}
	state.attempted = true
	defer state.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return err
	}
	serialized, err := document.Serialize()
	if err != nil {
		return err
	}
	data := []byte(serialized)
	if state.relativePath == indexFilename {
		state := document.Frontmatter.VersionDeclarationState()
		if state.Present && !state.Valid {
			return fmt.Errorf("%w: %q", ErrInvalidVersionDeclaration, state.Raw)
		}
		if _, err := ResolveVersion(state.Value, s.state.selector); err != nil {
			return err
		}
	}
	return mapDocumentPublicError(
		rewriteCapturedDocument(ctx, s, data, state.publishHook),
	)
}

// Close releases the pinned target parent, then the FileSystemSource, and only
// then the cross-process and process-local publication ownership.
func (s *DocumentSession) Close() error {
	if s == nil || s.state == nil {
		return nil
	}
	state := s.state
	state.mu.Lock()
	if state.closing {
		done := state.closeDone
		state.mu.Unlock()
		<-done
		state.mu.Lock()
		err := state.closeErr
		state.mu.Unlock()
		return err
	}
	if state.closed {
		err := state.closeErr
		state.mu.Unlock()
		return err
	}
	state.closing = true
	parent := state.parent
	source := state.source
	release := state.releaseLock
	state.parent = nil
	state.source = nil
	state.releaseLock = nil
	state.mu.Unlock()

	var err error
	if parent != nil {
		err = errors.Join(err, parent.Close())
	}
	if source != nil {
		err = errors.Join(err, source.Close())
	}
	if release != nil {
		release()
	}
	state.mu.Lock()
	state.closeErr = err
	state.closed = true
	state.closing = false
	close(state.closeDone)
	state.mu.Unlock()
	return err
}

func cloneDocumentValue(document Document) Document {
	return Document{
		Frontmatter: Frontmatter{
			node: cloneYAMLNode(&document.Frontmatter.node),
			raw:  append([]byte(nil), document.Frontmatter.raw...),
		},
		Body:            document.Body,
		HasFrontmatter:  document.HasFrontmatter,
		frontmatterYAML: append([]byte(nil), document.frontmatterYAML...),
	}
}

func documentConflict(reason string, err error) error {
	if err == nil {
		return fmt.Errorf("%w: %s", ErrDocumentConflict, reason)
	}
	return fmt.Errorf("%w: %s: %w", ErrDocumentConflict, reason, err)
}

func mapDocumentPublicError(err error) error {
	if err == nil ||
		errors.Is(err, ErrDocumentConflict) {
		return err
	}
	if errors.Is(err, errPublicationConflict) ||
		errors.Is(err, errPublicationNoReplaceUnsupported) ||
		errors.Is(err, ErrPublicationOwnershipConflict) ||
		errors.Is(err, ErrPublicationCapabilityUnsupported) {
		return fmt.Errorf("%w: %w", ErrDocumentConflict, err)
	}
	if errors.Is(err, ErrPublicationCommitted) {
		return err
	}
	return err
}

func discoverDocumentScope(ctx context.Context, targetPath string) (documentScopeDiscovery, error) {
	if err := ctx.Err(); err != nil {
		return documentScopeDiscovery{}, err
	}
	absolute, err := filepath.Abs(targetPath)
	if err != nil {
		return documentScopeDiscovery{}, fmt.Errorf("resolve document path: %w", err)
	}
	absolute = canonicalVolumePath(filepath.Clean(absolute))
	targetInfo, err := os.Lstat(absolute)
	targetPresent := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return documentScopeDiscovery{}, err
	}
	if targetPresent &&
		(targetInfo.Mode()&os.ModeSymlink != 0 || !targetInfo.Mode().IsRegular()) {
		return documentScopeDiscovery{}, fmt.Errorf(
			"resolve document path: %w: %q",
			ErrNotRegularFile,
			absolute,
		)
	}

	parent := filepath.Dir(absolute)
	volumeRoot := filepath.VolumeName(parent) + string(filepath.Separator)
	var (
		directories []documentAbsoluteDirectoryGuard
		indexes     []documentAbsoluteIndexGuard
		nearest     string
		declaring   string
	)
	for directory := parent; ; directory = filepath.Dir(directory) {
		if err := ctx.Err(); err != nil {
			return documentScopeDiscovery{}, err
		}
		info, err := os.Lstat(directory)
		if err != nil {
			return documentScopeDiscovery{}, err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return documentScopeDiscovery{}, fmt.Errorf(
				"resolve document path: %w: ancestor %q",
				ErrNotDirectory,
				directory,
			)
		}
		directories = append(directories, documentAbsoluteDirectoryGuard{path: directory, info: info})

		indexGuard, state, err := inspectAncestorIndex(ctx, directory)
		if err != nil {
			return documentScopeDiscovery{}, err
		}
		indexes = append(indexes, indexGuard)
		if indexGuard.present {
			if nearest == "" {
				nearest = directory
			}
			if state.Present {
				declaring = directory
			}
		}
		if directory == volumeRoot || filepath.Dir(directory) == directory {
			break
		}
	}
	root := declaring
	if root == "" {
		root = nearest
	}
	if root == "" {
		root = parent
	}
	return documentScopeDiscovery{
		root:          root,
		target:        absolute,
		targetPresent: targetPresent,
		targetInfo:    targetInfo,
		directories:   directories,
		indexes:       indexes,
	}, nil
}

func inspectAncestorIndex(
	ctx context.Context,
	directory string,
) (documentAbsoluteIndexGuard, VersionDeclarationState, error) {
	indexPath := filepath.Join(directory, indexFilename)
	info, err := os.Lstat(indexPath)
	if errors.Is(err, fs.ErrNotExist) {
		return documentAbsoluteIndexGuard{path: indexPath}, VersionDeclarationState{Valid: true}, nil
	}
	if err != nil {
		return documentAbsoluteIndexGuard{}, VersionDeclarationState{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return documentAbsoluteIndexGuard{}, VersionDeclarationState{}, fmt.Errorf(
			"resolve OKF version: %w: root index %q",
			ErrNotRegularFile,
			indexPath,
		)
	}
	root, err := openRootWithoutSymlinks(directory)
	if err != nil {
		return documentAbsoluteIndexGuard{}, VersionDeclarationState{}, err
	}
	defer root.Close()
	data, err := readRegularDocumentFile(ctx, root, indexFilename)
	if err != nil {
		return documentAbsoluteIndexGuard{}, VersionDeclarationState{}, err
	}
	after, err := root.Lstat(indexFilename)
	if err != nil || !os.SameFile(info, after) ||
		indexPreservedMode(info.Mode()) != indexPreservedMode(after.Mode()) {
		return documentAbsoluteIndexGuard{}, VersionDeclarationState{},
			documentConflict("ancestor root index changed during discovery", err)
	}
	second, err := readRegularDocumentFile(ctx, root, indexFilename)
	if err != nil || !bytes.Equal(data, second) {
		return documentAbsoluteIndexGuard{}, VersionDeclarationState{},
			documentConflict("ancestor root index bytes changed during discovery", err)
	}
	final, err := root.Lstat(indexFilename)
	if err != nil || !os.SameFile(after, final) ||
		indexPreservedMode(after.Mode()) != indexPreservedMode(final.Mode()) ||
		after.Size() != final.Size() ||
		!after.ModTime().Equal(final.ModTime()) {
		return documentAbsoluteIndexGuard{}, VersionDeclarationState{},
			documentConflict("ancestor root index observation changed during discovery", err)
	}
	document, parseErr := ParseDocumentContext(ctx, string(data))
	state := VersionDeclarationState{Valid: true}
	if parseErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return documentAbsoluteIndexGuard{}, VersionDeclarationState{}, ctxErr
		}
		if errors.Is(parseErr, ErrYAMLResourceLimit) || errors.Is(parseErr, ErrInvalidYAMLGraph) {
			return documentAbsoluteIndexGuard{}, VersionDeclarationState{}, parseErr
		}
		state = VersionDeclarationState{Present: true}
	} else {
		state = document.Frontmatter.VersionDeclarationState()
	}
	return documentAbsoluteIndexGuard{
		path:    indexPath,
		present: true,
		info:    final,
		mode:    indexPreservedMode(final.Mode()),
		size:    final.Size(),
		modTime: final.ModTime(),
		data:    data,
	}, state, nil
}

func compareDocumentScopeDiscoveries(
	ctx context.Context,
	before documentScopeDiscovery,
	after documentScopeDiscovery,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if before.root != after.root || before.target != after.target ||
		before.targetPresent != after.targetPresent ||
		len(before.directories) != len(after.directories) ||
		len(before.indexes) != len(after.indexes) {
		return documentConflict("scope changed during capture", nil)
	}
	if before.targetPresent &&
		(before.targetInfo == nil ||
			after.targetInfo == nil ||
			!os.SameFile(before.targetInfo, after.targetInfo)) {
		return documentConflict("target changed during capture", nil)
	}
	for index := range before.directories {
		if err := ctx.Err(); err != nil {
			return err
		}
		left, right := before.directories[index], after.directories[index]
		if left.path != right.path || !os.SameFile(left.info, right.info) {
			return documentConflict("ancestor changed during capture", nil)
		}
	}
	for index := range before.indexes {
		if err := ctx.Err(); err != nil {
			return err
		}
		left, right := before.indexes[index], after.indexes[index]
		if left.path != right.path || left.present != right.present {
			return documentConflict("root metadata changed during capture", nil)
		}
		if left.present && (!os.SameFile(left.info, right.info) ||
			left.mode != right.mode ||
			left.size != right.size ||
			!left.modTime.Equal(right.modTime) ||
			!bytes.Equal(left.data, right.data)) {
			return documentConflict("root metadata changed during capture", nil)
		}
	}
	return nil
}

func compareDocumentScopeAfterRecovery(
	ctx context.Context,
	before documentScopeDiscovery,
	after documentScopeDiscovery,
	recoveredTarget publicationFileSpec,
	recoveredTargetPresent bool,
) error {
	if before.targetPresent &&
		after.targetPresent &&
		before.targetInfo != nil &&
		after.targetInfo != nil &&
		os.SameFile(before.targetInfo, after.targetInfo) {
		return compareDocumentScopeDiscoveries(ctx, before, after)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if before.root != after.root ||
		before.target != after.target ||
		len(before.directories) != len(after.directories) ||
		len(before.indexes) != len(after.indexes) {
		return documentConflict("scope changed during recovery", nil)
	}
	for index := range before.directories {
		left, right := before.directories[index], after.directories[index]
		if left.path != right.path || !os.SameFile(left.info, right.info) {
			return documentConflict("ancestor changed during recovery", nil)
		}
	}
	for index := range before.indexes {
		left, right := before.indexes[index], after.indexes[index]
		if left.path != right.path ||
			left.present != right.present ||
			left.present &&
				(!os.SameFile(left.info, right.info) ||
					left.mode != right.mode ||
					left.size != right.size ||
					!left.modTime.Equal(right.modTime) ||
					!bytes.Equal(left.data, right.data)) {
			return documentConflict("root metadata changed during recovery", nil)
		}
	}
	if !before.targetPresent &&
		!after.targetPresent &&
		!recoveredTargetPresent {
		return nil
	}
	if !after.targetPresent ||
		after.targetInfo == nil ||
		!recoveredTargetPresent ||
		recoveredTarget.info == nil ||
		!recoveredTarget.complete ||
		!os.SameFile(after.targetInfo, recoveredTarget.info) {
		return documentConflict("target changed during recovery", nil)
	}
	return nil
}

func captureDocumentFileGuard(
	ctx context.Context,
	parent *os.Root,
	leaf string,
	relative string,
) (documentFileGuard, error) {
	info, err := parent.Lstat(leaf)
	if err != nil {
		return documentFileGuard{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return documentFileGuard{}, fmt.Errorf("%w: %q", ErrNotRegularFile, relative)
	}
	data, err := readRegularDocumentFile(ctx, parent, leaf)
	if err != nil {
		return documentFileGuard{}, err
	}
	after, err := parent.Lstat(leaf)
	if err != nil || !os.SameFile(info, after) ||
		indexPreservedMode(info.Mode()) != indexPreservedMode(after.Mode()) {
		return documentFileGuard{}, documentConflict("target changed while capturing", err)
	}
	second, err := readRegularDocumentFile(ctx, parent, leaf)
	if err != nil || !bytes.Equal(data, second) {
		return documentFileGuard{}, documentConflict("target bytes changed while capturing", err)
	}
	final, err := parent.Lstat(leaf)
	if err != nil || !os.SameFile(after, final) ||
		indexPreservedMode(after.Mode()) != indexPreservedMode(final.Mode()) ||
		after.Size() != final.Size() ||
		!after.ModTime().Equal(final.ModTime()) {
		return documentFileGuard{}, documentConflict("target observation changed while capturing", err)
	}
	return documentFileGuard{
		relative: relative,
		present:  true,
		info:     final,
		mode:     indexPreservedMode(final.Mode()),
		size:     final.Size(),
		modTime:  final.ModTime(),
		data:     data,
	}, nil
}

func captureRootIndexGuard(
	ctx context.Context,
	root *os.Root,
	loaded *Bundle,
) (documentFileGuard, error) {
	data, present, err := loaded.ReadFileContext(ctx, indexFilename)
	if err != nil {
		return documentFileGuard{}, err
	}
	if !present {
		if _, err := root.Lstat(indexFilename); !errors.Is(err, fs.ErrNotExist) {
			return documentFileGuard{}, documentConflict("root index appeared after bundle capture", err)
		}
		return documentFileGuard{relative: indexFilename}, nil
	}
	guard, err := captureDocumentFileGuard(ctx, root, indexFilename, indexFilename)
	if err != nil {
		return documentFileGuard{}, err
	}
	if !bytes.Equal(data, guard.data) {
		return documentFileGuard{}, documentConflict("root index changed after bundle capture", nil)
	}
	return guard, nil
}

func captureDocumentDirectoryGuards(
	root *os.Root,
	directory string,
) ([]documentDirectoryGuard, error) {
	guards := make([]documentDirectoryGuard, 0, strings.Count(directory, "/")+2)
	rootInfo, err := root.Lstat(".")
	if err != nil || !rootInfo.IsDir() {
		return nil, documentConflict("scope root cannot be captured", err)
	}
	guards = append(guards, documentDirectoryGuard{relative: ".", info: rootInfo})
	if directory == "" || directory == "." {
		return guards, nil
	}
	current := ""
	for _, component := range strings.Split(directory, "/") {
		current = path.Join(current, component)
		info, err := root.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, documentConflict("target ancestor cannot be captured", err)
		}
		guards = append(guards, documentDirectoryGuard{relative: current, info: info})
	}
	return guards, nil
}

func readRegularDocumentFile(ctx context.Context, parent *os.Root, leaf string) ([]byte, error) {
	file, err := openRegularFile(parent, leaf)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return ioReadAllContext(ctx, file)
}
