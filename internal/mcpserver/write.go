package mcpserver

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/validator"
)

func writeConcept(root string, id bundle.ConceptID, frontmatterText, body string) (writeConceptResponse, error) {
	targetPath := id.ToPath(root)
	if !isInside(root, targetPath) {
		return writeConceptResponse{}, fmt.Errorf("concept path escapes bundle root")
	}
	if err := rejectWriteSymlinks(root, targetPath); err != nil {
		return writeConceptResponse{}, err
	}

	frontmatter, err := bundle.ParseFrontmatter(frontmatterText)
	if err != nil {
		return writeConceptResponse{}, err
	}
	document := bundle.NewDocument(frontmatter, body)
	serialized, err := document.Serialize()
	if err != nil {
		return writeConceptResponse{}, err
	}

	stagingRoot, err := os.MkdirTemp("", "okf-mcp-*")
	if err != nil {
		return writeConceptResponse{}, err
	}
	defer os.RemoveAll(stagingRoot)

	if err := copyBundleTree(root, stagingRoot); err != nil {
		return writeConceptResponse{}, err
	}
	stagedTarget := id.ToPath(stagingRoot)
	if err := os.MkdirAll(filepath.Dir(stagedTarget), 0o755); err != nil {
		return writeConceptResponse{}, err
	}
	if err := os.WriteFile(stagedTarget, []byte(serialized), 0o644); err != nil {
		return writeConceptResponse{}, err
	}

	cfg := validator.ValidatorConfig{
		Strict:       true,
		CheckLinks:   true,
		CheckOrphans: true,
	}
	report := validator.ValidatePath(stagingRoot, &cfg)
	response := writeConceptResponse{
		Status:      "success",
		Path:        relativeSlashPath(root, targetPath),
		Diagnostics: reportResponse(stagingRoot, report).Diagnostics,
	}
	if !report.IsConformant() {
		response.Status = "rejected"
		return response, nil
	}

	if err := atomicWriteFile(root, targetPath, []byte(serialized)); err != nil {
		return writeConceptResponse{}, err
	}
	return response, nil
}

func copyBundleTree(root, stagingRoot string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		target := filepath.Join(stagingRoot, rel)
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		return copyRegularFile(path, target, info.Mode().Perm())
	})
}

func copyRegularFile(source, target string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func atomicWriteFile(root, targetPath string, data []byte) error {
	parent := filepath.Dir(targetPath)
	if err := rejectWriteSymlinks(root, targetPath); err != nil {
		return err
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	if err := rejectWriteSymlinks(root, targetPath); err != nil {
		return err
	}

	mode := os.FileMode(0o644)
	if info, err := os.Lstat(targetPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("target path is a symlink")
		}
		if info.IsDir() {
			return fmt.Errorf("target path is a directory")
		}
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return err
	}

	tmp, err := os.CreateTemp(parent, ".okf-mcp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func rejectReadSymlinks(root, targetPath string) error {
	return rejectExistingSymlinks(root, targetPath, true)
}

func rejectWriteSymlinks(root, targetPath string) error {
	return rejectExistingSymlinks(root, targetPath, false)
}

func rejectExistingSymlinks(root, targetPath string, requireTarget bool) error {
	cleanRoot := filepath.Clean(root)
	cleanTarget := filepath.Clean(targetPath)
	if !isInside(cleanRoot, cleanTarget) {
		return fmt.Errorf("path escapes bundle root")
	}
	rel, err := filepath.Rel(cleanRoot, cleanTarget)
	if err != nil {
		return err
	}
	parts := splitPath(rel)
	current := cleanRoot
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				if requireTarget && i < len(parts)-1 {
					return err
				}
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path contains symlink: %s", relativeSlashPath(cleanRoot, current))
		}
	}
	return nil
}

func splitPath(path string) []string {
	if path == "." || path == "" {
		return nil
	}
	parts := strings.Split(filepath.ToSlash(path), "/")
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			filtered = append(filtered, part)
		}
	}
	return filtered
}
