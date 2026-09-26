package okfcli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/skosovsky/okf/bundle"
)

const initIndex = `---
okf_version: "0.2"
---

# Concepts

* [Getting started](getting-started.md) - Draft guide to replace with your own knowledge.
`

const initConcept = `---
type: Guide
title: Getting started
description: Draft guide to replace with your own knowledge.
status: draft
---

# Getting started

This is a draft placeholder. Replace it with a concept based on your own materials.
`

type initResult struct {
	Path       string   `json:"path"`
	OKFVersion string   `json:"okf_version"`
	Files      []string `json:"files"`
}

func cmdInit(args []string, stdout io.Writer) (int, error) {
	return cmdInitWithHook(args, stdout, nil)
}

func cmdInitWithHook(args []string, stdout io.Writer, beforePublish func(context.Context) error) (int, error) {
	parsed, err := parseArgs(args, []flagSpec{{Name: "--json", Kind: boolFlag}})
	if err != nil {
		return 0, err
	}
	target, err := parsed.onePositional("bundle directory")
	if err != nil {
		return 0, err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := initBundleContext(ctx, target, beforePublish); err != nil {
		return 0, err
	}
	result := initResult{Path: filepath.Clean(target), OKFVersion: bundle.OKFVersion, Files: []string{"index.md", "getting-started.md"}}
	if parsed.boolValue("--json") {
		return 0, json.NewEncoder(stdout).Encode(result)
	}
	_, err = fmt.Fprintf(stdout, "Created OKF v%s bundle at %s\n", result.OKFVersion, result.Path)
	return 0, err
}

// initBundleContext stages a complete bundle beside its absent target, then
// atomically installs it using the same no-replace primitive as index writes.
// beforePublish is a test seam for cancellation and failed publication.
func initBundleContext(ctx context.Context, target string, beforePublish func(context.Context) error) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if target == "" || target == "." || target == ".." {
		return fmt.Errorf("init requires a new bundle directory")
	}
	clean := filepath.Clean(target)
	name := filepath.Base(clean)
	if name == "." || name == string(filepath.Separator) || name == ".." {
		return fmt.Errorf("init requires a new bundle directory")
	}
	parentPath := filepath.Dir(clean)
	parent, err := os.OpenRoot(parentPath)
	if err != nil {
		return fmt.Errorf("open bundle parent: %w", err)
	}
	defer parent.Close()
	if _, err := parent.Lstat(name); err == nil {
		return fmt.Errorf("bundle target already exists: %s", clean)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect bundle target: %w", err)
	}
	stageName, err := newInitStageName(parent)
	if err != nil {
		return err
	}
	staged := true
	defer func() {
		if staged {
			if err := parent.RemoveAll(stageName); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("remove init stage: %w", err))
			}
		}
	}()
	stage, err := parent.OpenRoot(stageName)
	if err != nil {
		return fmt.Errorf("open init stage: %w", err)
	}
	for _, file := range []struct{ name, content string }{
		{"index.md", initIndex},
		{"getting-started.md", initConcept},
	} {
		if err := writeInitFile(stage, file.name, file.content); err != nil {
			return errors.Join(fmt.Errorf("stage %s: %w", file.name, err), stage.Close())
		}
	}
	if err := bundle.SyncPublicationDirectory(stage); err != nil {
		return errors.Join(fmt.Errorf("sync init stage: %w", err), stage.Close())
	}
	if err := stage.Close(); err != nil {
		return fmt.Errorf("close init stage: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if beforePublish != nil {
		if err := beforePublish(ctx); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := bundle.PublishNewDirectory(parent, stageName, name); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("bundle target already exists: %s: %w", clean, err)
		}
		return fmt.Errorf("publish bundle: %w", err)
	}
	staged = false
	if err := bundle.SyncPublicationDirectory(parent); err != nil {
		return fmt.Errorf("bundle was published, but parent sync failed: %w", err)
	}
	return nil
}

func newInitStageName(parent *os.Root) (string, error) {
	for i := 0; i < 8; i++ {
		var random [12]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", fmt.Errorf("generate init stage name: %w", err)
		}
		name := ".okf-init-" + hex.EncodeToString(random[:])
		if err := parent.Mkdir(name, 0o755); err == nil {
			return name, nil
		} else if !errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("create init stage: %w", err)
		}
	}
	return "", fmt.Errorf("create init stage: repeated name collision")
}

func writeInitFile(stage *os.Root, name, content string) error {
	file, err := stage.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, writeErr := io.WriteString(file, content)
	syncErr := error(nil)
	if writeErr == nil {
		syncErr = file.Sync()
	}
	return errors.Join(writeErr, syncErr, file.Close())
}
