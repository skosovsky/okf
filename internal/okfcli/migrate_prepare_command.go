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
	"path/filepath"
	"strings"

	"github.com/skosovsky/okf/bundle"
)

type migratePrepareOptions struct {
	BundleRoot string
	OutputDir  string
	Format     string
}

func parseMigratePrepareArgs(args []string) (migratePrepareOptions, error) {
	parsed, err := parseArgs(args, []flagSpec{
		{Name: "--output-dir", Kind: stringFlag},
		{Name: "--format", Kind: stringFlag},
	})
	if err != nil {
		return migratePrepareOptions{}, err
	}
	root, err := parsed.onePositional("<bundle>")
	if err != nil {
		return migratePrepareOptions{}, namedArgumentError("migrate-prepare", err)
	}
	output := parsed.value("--output-dir", "")
	if output == "" {
		return migratePrepareOptions{}, fmt.Errorf("migrate-prepare requires --output-dir")
	}
	format := parsed.value("--format", "text")
	if format != "text" && format != "json" {
		return migratePrepareOptions{}, fmt.Errorf("unsupported migrate-prepare format: %s", format)
	}
	return migratePrepareOptions{BundleRoot: root, OutputDir: output, Format: format}, nil
}

func cmdMigratePrepare(args []string, stdout io.Writer) (int, error) {
	opts, err := parseMigratePrepareArgs(args)
	if err != nil {
		return 0, err
	}
	source := &bundle.FileSystemSource{Root: opts.BundleRoot}
	template, report, prepareErr := prepareMigrationInputs(context.Background(), source)
	closeErr := source.Close()
	if prepareErr != nil {
		return 0, prepareErr
	}
	if closeErr != nil {
		return 0, closeErr
	}
	if err := writeMigrationPreparationDirectory(opts.BundleRoot, opts.OutputDir, template, report); err != nil {
		return 0, err
	}
	if opts.Format == "json" {
		return 0, json.NewEncoder(stdout).Encode(report)
	}
	_, err = fmt.Fprintf(stdout,
		"prepared migration inputs in %s\nsource_sha256: %s\nsuggestions: %d\nunresolved: %d\n",
		opts.OutputDir, report.SourceSHA256, len(report.Suggestions), len(report.Unresolved))
	return 0, err
}

func writeMigrationPreparationDirectory(
	bundleRoot, outputDir string,
	template migrationMappingTemplate,
	report migrationPreparationReport,
) error {
	return writeMigrationPreparationDirectoryWithHook(bundleRoot, outputDir, template, report, nil)
}

func writeMigrationPreparationDirectoryWithHook(
	bundleRoot, outputDir string,
	template migrationMappingTemplate,
	report migrationPreparationReport,
	beforePublish func() error,
) (resultErr error) {
	if outputDir == "" {
		return fmt.Errorf("prepare migration: output directory is required")
	}
	clean := filepath.Clean(outputDir)
	name := filepath.Base(clean)
	if name == "." || name == ".." || name == string(filepath.Separator) {
		return fmt.Errorf("prepare migration: invalid output directory")
	}
	root, err := filepath.EvalSymlinks(bundleRoot)
	if err != nil {
		return fmt.Errorf("prepare migration: resolve bundle root: %w", err)
	}
	parentPath, err := filepath.EvalSymlinks(filepath.Dir(clean))
	if err != nil {
		return fmt.Errorf("prepare migration: resolve output parent: %w", err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	parentPath, err = filepath.Abs(parentPath)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(root, parentPath)
	if err != nil {
		return err
	}
	if relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("prepare migration: output must be outside bundle")
	}
	citationJSON, err := json.MarshalIndent(template, "", "  ")
	if err != nil {
		return err
	}
	generatedAtJSON, err := json.MarshalIndent(report.GeneratedAt, "", "  ")
	if err != nil {
		return err
	}
	reportJSON, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if len(citationJSON)+1 > maxCitationMappingsFileBytes || len(generatedAtJSON)+1 > maxCitationMappingsFileBytes {
		return fmt.Errorf("prepare migration: generated input exceeds CLI file limit")
	}
	parent, err := os.OpenRoot(parentPath)
	if err != nil {
		return fmt.Errorf("prepare migration: open output parent: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, parent.Close()) }()
	if _, err := parent.Lstat(name); err == nil {
		return fmt.Errorf("prepare migration: output already exists: %s", clean)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("prepare migration: inspect output: %w", err)
	}
	stageName, err := newMigrationPrepareStageName(parent)
	if err != nil {
		return err
	}
	staged := true
	defer func() {
		if staged {
			resultErr = errors.Join(resultErr, parent.RemoveAll(stageName))
		}
	}()
	stage, err := parent.OpenRoot(stageName)
	if err != nil {
		return fmt.Errorf("prepare migration: open output stage: %w", err)
	}
	files := []struct {
		name string
		data []byte
	}{
		{"citation-mappings.json", citationJSON},
		{"generated-at.json", generatedAtJSON},
		{"preparation-report.json", reportJSON},
	}
	for _, file := range files {
		data := append(file.data, '\n')
		created, err := stage.OpenFile(file.name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			_, err = created.Write(data)
			if err == nil {
				err = created.Sync()
			}
			err = errors.Join(err, created.Close())
		}
		if err != nil {
			return errors.Join(fmt.Errorf("prepare migration: write %q: %w", file.name, err), stage.Close())
		}
	}
	if err := bundle.SyncPublicationDirectory(stage); err != nil {
		return errors.Join(fmt.Errorf("prepare migration: sync output stage: %w", err), stage.Close())
	}
	if err := stage.Close(); err != nil {
		return fmt.Errorf("prepare migration: close output stage: %w", err)
	}
	if beforePublish != nil {
		if err := beforePublish(); err != nil {
			return err
		}
	}
	if err := bundle.PublishNewDirectory(parent, stageName, name); err != nil {
		return fmt.Errorf("prepare migration: publish output: %w", err)
	}
	staged = false
	if err := bundle.SyncPublicationDirectory(parent); err != nil {
		return fmt.Errorf("prepare migration: output published but parent sync failed: %w", err)
	}
	return nil
}

func newMigrationPrepareStageName(parent *os.Root) (string, error) {
	for i := 0; i < 8; i++ {
		var random [12]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", fmt.Errorf("prepare migration: random stage name: %w", err)
		}
		name := ".okf-migration-prepare-" + hex.EncodeToString(random[:])
		if err := parent.Mkdir(name, 0o700); err == nil {
			return name, nil
		} else if !errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("prepare migration: create output stage: %w", err)
		}
	}
	return "", fmt.Errorf("prepare migration: repeated stage name collision")
}
