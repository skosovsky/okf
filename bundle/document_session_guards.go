package bundle

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
)

func verifyDocumentSessionPreimage(ctx context.Context, session *DocumentSession) error {
	state := session.state
	if err := verifyDocumentSessionDirectories(ctx, session); err != nil {
		return err
	}
	if err := verifyDocumentGuard(
		ctx,
		state.parent,
		path.Base(state.relativePath),
		state.target,
	); err != nil {
		return err
	}
	return verifyDocumentDiscoveryGuards(ctx, session, nil)
}

func verifyDocumentDependencyGuards(ctx context.Context, session *DocumentSession) error {
	state := session.state
	if err := verifyDocumentSessionDirectories(ctx, session); err != nil {
		return err
	}
	skip := ""
	if state.relativePath == indexFilename {
		skip = state.discovery.target
	}
	return verifyDocumentDiscoveryGuards(ctx, session, func(index documentAbsoluteIndexGuard) bool {
		return index.path == skip
	})
}

func verifyDocumentSessionPostimage(
	ctx context.Context,
	session *DocumentSession,
	publishedInfo os.FileInfo,
	published []byte,
) error {
	state := session.state
	if err := verifyDocumentSessionDirectories(ctx, session); err != nil {
		return err
	}
	if err := verifyPublishedDocument(ctx, session, publishedInfo, published); err != nil {
		return err
	}
	if state.relativePath != indexFilename {
		return verifyDocumentDiscoveryGuards(ctx, session, nil)
	}
	if err := verifyDocumentDiscoveryGuards(
		ctx,
		session,
		func(index documentAbsoluteIndexGuard) bool {
			return index.path == state.discovery.target
		},
	); err != nil {
		return err
	}
	current, declaration, err := inspectAncestorIndex(ctx, filepath.Dir(state.discovery.target))
	if err != nil {
		return documentConflict("rewritten root index cannot be observed", err)
	}
	expected := publicationSpecFromBytes(publishedInfo, published)
	if !current.present ||
		expected.info == nil ||
		!os.SameFile(expected.info, current.info) ||
		expected.mode != current.mode ||
		expected.size != current.size ||
		!bytes.Equal(expected.data, current.data) {
		return documentConflict("rewritten root index changed after publication", nil)
	}
	if declaration.Present && !declaration.Valid {
		return documentConflict("rewritten root index has an invalid version declaration", nil)
	}
	if _, err := ResolveVersion(declaration.Value, state.selector); err != nil {
		return documentConflict("rewritten root index violates version selection", err)
	}
	currentDiscovery, err := discoverDocumentScope(ctx, state.discovery.target)
	if err != nil {
		return documentConflict("rewritten root index changed its scope", err)
	}
	if currentDiscovery.root != state.discovery.root ||
		currentDiscovery.target != state.discovery.target {
		return documentConflict("rewritten root index changed its scope", nil)
	}
	return nil
}

func verifyPublishedDocument(
	ctx context.Context,
	session *DocumentSession,
	publishedInfo os.FileInfo,
	published []byte,
) error {
	state := session.state
	if publishedInfo == nil {
		return documentConflict("published document identity is unavailable", nil)
	}
	if err := verifyPublicationFile(
		ctx,
		state.parent,
		path.Base(state.relativePath),
		publicationSpecFromBytes(publishedInfo, published),
		true,
	); err != nil {
		return documentConflict("published document changed", err)
	}
	return nil
}

func verifyDocumentGuard(
	ctx context.Context,
	parent *os.Root,
	leaf string,
	guard documentFileGuard,
) error {
	if !guard.present || guard.info == nil {
		if _, err := parent.Lstat(leaf); !errors.Is(err, fs.ErrNotExist) {
			return documentConflict("absent document dependency appeared", err)
		}
		return nil
	}
	if err := verifyPublicationFile(
		ctx,
		parent,
		leaf,
		publicationSpecFromDocumentGuard(guard),
		true,
	); err != nil {
		return documentConflict("captured document changed", err)
	}
	return nil
}

func verifyDocumentSessionDirectories(ctx context.Context, session *DocumentSession) error {
	state := session.state
	if err := ctx.Err(); err != nil {
		return err
	}
	parentInfo, err := state.parent.Lstat(".")
	if err != nil || parentInfo.Mode()&os.ModeSymlink != 0 ||
		!parentInfo.IsDir() ||
		state.parentInfo == nil ||
		!os.SameFile(state.parentInfo, parentInfo) {
		return documentConflict("target parent identity changed", err)
	}
	root, err := state.source.openRoot()
	if err != nil {
		return documentConflict("scope root is unavailable", err)
	}
	for _, guard := range state.ancestors {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := root.Lstat(guard.relative)
		if err != nil ||
			info.Mode()&os.ModeSymlink != 0 ||
			!info.IsDir() ||
			guard.info == nil ||
			!os.SameFile(guard.info, info) {
			return documentConflict("target ancestor identity changed", err)
		}
	}
	return nil
}

func verifyDocumentDiscoveryGuards(
	ctx context.Context,
	session *DocumentSession,
	skipIndex func(documentAbsoluteIndexGuard) bool,
) error {
	state := session.state
	for _, guard := range state.discovery.directories {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := os.Lstat(guard.path)
		if err != nil ||
			info.Mode()&os.ModeSymlink != 0 ||
			!info.IsDir() ||
			guard.info == nil ||
			!os.SameFile(guard.info, info) {
			return documentConflict("scope ancestor identity changed", err)
		}
	}
	for _, guard := range state.discovery.indexes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if skipIndex != nil && skipIndex(guard) {
			continue
		}
		current, _, err := inspectAncestorIndex(ctx, filepath.Dir(guard.path))
		if err != nil {
			return documentConflict("root metadata cannot be verified", err)
		}
		if current.path != guard.path || current.present != guard.present {
			return documentConflict("root metadata presence changed", nil)
		}
		if guard.present &&
			(guard.info == nil ||
				!os.SameFile(guard.info, current.info) ||
				guard.mode != current.mode ||
				guard.size != current.size ||
				!guard.modTime.Equal(current.modTime) ||
				!bytes.Equal(guard.data, current.data)) {
			return documentConflict("root metadata changed", nil)
		}
	}
	return nil
}
