package backfill

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

func digest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

func git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out := &metadataWriter{limit: 32 << 20}
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = out, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	if out.overflow {
		return nil, fmt.Errorf("git %s metadata exceeds 32 MiB", args[0])
	}
	return out.Bytes(), nil
}

type metadataWriter struct {
	bytes.Buffer
	limit    int
	overflow bool
}

type LimitError struct {
	Kind    string
	Count   int
	Maximum int
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("backfill %s limit exceeded: %d > %d", e.Kind, e.Count, e.Maximum)
}

func (w *metadataWriter) Write(p []byte) (int, error) {
	n := w.limit - w.Len()
	if n > len(p) {
		n = len(p)
	}
	if n > 0 {
		_, _ = w.Buffer.Write(p[:n])
	}
	if n < len(p) {
		w.overflow = true
	}
	return len(p), nil
}

func revision(ctx context.Context, dir, ref string) (string, error) {
	if ref == "" || strings.HasPrefix(ref, "-") || strings.ContainsAny(ref, "\x00\n\r") {
		return "", fmt.Errorf("invalid Git revision")
	}
	b, err := git(ctx, dir, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(string(b))
	if len(v) != 40 && len(v) != 64 {
		return "", fmt.Errorf("unexpected Git object ID")
	}
	for _, c := range v {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return "", fmt.Errorf("invalid Git object ID")
		}
	}
	return v, nil
}

// Extract freezes the selected commits before reading any event. Git arguments
// are passed as argv; commit messages and paths never enter a shell command.
func Extract(ctx context.Context, cfg Config) (Manifest, error) {
	if cfg.Repository == "" || cfg.Base == "" || cfg.Head == "" {
		return Manifest{}, fmt.Errorf("repository, base and head are required")
	}
	if cfg.MaxDiffBytes == 0 {
		cfg.MaxDiffBytes = 64 << 10
	}
	if cfg.MaxDiffBytes < 0 || cfg.MaxDiffBytes > 16<<20 {
		return Manifest{}, fmt.Errorf("max diff bytes must be 1..16777216")
	}
	if cfg.SkipPaths == nil {
		cfg.SkipPaths = []string{}
	}
	for _, pattern := range cfg.SkipPaths {
		if pattern == "" {
			return Manifest{}, fmt.Errorf("empty skip pattern")
		}
		if _, err := filepath.Match(pattern, "probe"); err != nil {
			return Manifest{}, fmt.Errorf("invalid skip pattern %q: %w", pattern, err)
		}
	}
	rootRaw, err := git(ctx, cfg.Repository, "rev-parse", "--show-toplevel")
	if err != nil {
		return Manifest{}, err
	}
	root, err := filepath.Abs(strings.TrimSpace(string(rootRaw)))
	if err != nil {
		return Manifest{}, err
	}
	base, err := revision(ctx, root, cfg.Base)
	if err != nil {
		return Manifest{}, err
	}
	head, err := revision(ctx, root, cfg.Head)
	if err != nil {
		return Manifest{}, err
	}
	if _, err := git(ctx, root, "merge-base", "--is-ancestor", base, head); err != nil {
		return Manifest{}, fmt.Errorf("base must be ancestor of head: %w", err)
	}
	cfg.Repository, cfg.Base, cfg.Head = root, base, head
	configBytes, err := json.Marshal(cfg)
	if err != nil {
		return Manifest{}, err
	}
	args := []string{"rev-list", "--reverse", "--topo-order"}
	if cfg.FirstParent {
		args = append(args, "--first-parent")
	}
	args = append(args, base+".."+head)
	commitsRaw, err := git(ctx, root, args...)
	if err != nil {
		return Manifest{}, err
	}
	manifest := Manifest{SchemaVersion: SchemaVersion, ExtractorVersion: ExtractorVersion, Config: cfg, Repository: root, Base: base, Head: head, ConfigSHA256: digest(configBytes), Events: []Event{}}
	commits := strings.Fields(string(commitsRaw))
	if len(commits) > 100000 {
		return Manifest{}, &LimitError{Kind: "events", Count: len(commits), Maximum: 100000}
	}
	for _, commit := range commits {
		if err := ctx.Err(); err != nil {
			return Manifest{}, err
		}
		e, err := extractEvent(ctx, root, commit, cfg)
		if err != nil {
			return Manifest{}, err
		}
		manifest.Events = append(manifest.Events, e)
	}
	data, err := json.Marshal(manifest.Events)
	if err != nil {
		return Manifest{}, err
	}
	manifest.EventsSHA256 = digest(data)
	return manifest, nil
}

func extractEvent(ctx context.Context, root, sha string, cfg Config) (Event, error) {
	info, err := git(ctx, root, "show", "-s", "--format=%P%x00%s%x00%b%x00%an <%ae>%x00%aI%x00%cI", sha)
	if err != nil {
		return Event{}, err
	}
	if !utf8.Valid(info) {
		return Event{}, fmt.Errorf("commit %s metadata is not UTF-8", sha)
	}
	fields := strings.SplitN(strings.TrimSuffix(string(info), "\n"), "\x00", 6)
	if len(fields) != 6 {
		return Event{}, fmt.Errorf("invalid metadata for %s", sha)
	}
	parents := strings.Fields(fields[0])
	if parents == nil {
		parents = []string{}
	}
	parent, err := emptyTree(ctx, root)
	if err != nil {
		return Event{}, err
	}
	if len(parents) > 0 {
		parent = parents[0]
	}
	// We intentionally compare merge commits against their first parent. The
	// remaining parent IDs are retained as provenance, without replaying twice.
	stats, err := git(ctx, root, "diff", "--no-ext-diff", "--numstat", "-z", "-M", parent, sha, "--")
	if err != nil {
		return Event{}, err
	}
	files, err := parseNumstat(stats, cfg.SkipPaths)
	if err != nil {
		return Event{}, fmt.Errorf("commit %s: %w", sha, err)
	}
	if len(files) > 10000 {
		return Event{}, &LimitError{Kind: "files_per_event", Count: len(files), Maximum: 10000}
	}
	statuses, err := git(ctx, root, "diff", "--no-ext-diff", "--name-status", "-z", "-M", parent, sha, "--")
	if err != nil {
		return Event{}, err
	}
	if err := mergeStatuses(files, statuses); err != nil {
		return Event{}, err
	}
	diffArgs := []string{"-C", root, "diff", "--no-ext-diff", "--no-textconv", "--no-color", "--find-renames", parent, sha, "--"}
	included := map[string]bool{}
	for _, f := range files {
		if f.SkippedReason != "" {
			continue
		}
		included[f.Path] = true
		if f.OldPath != "" {
			included[f.OldPath] = true
		}
	}
	paths := make([]string, 0, len(included))
	for p := range included {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		diffArgs = append(diffArgs, ":(literal)"+p)
	}
	var stderr bytes.Buffer
	capWriter := &boundedWriter{limit: cfg.MaxDiffBytes, hash: sha256.New()}
	if len(paths) > 0 {
		cmd := exec.CommandContext(ctx, "git", diffArgs...)
		cmd.Stdout, cmd.Stderr = capWriter, &stderr
		if err := cmd.Run(); err != nil {
			return Event{}, fmt.Errorf("git diff %s: %w: %s", sha, err, stderr.String())
		}
	}
	if err := ctx.Err(); err != nil {
		return Event{}, err
	}
	for !utf8.Valid(capWriter.prefix) && len(capWriter.prefix) > 0 {
		capWriter.prefix = capWriter.prefix[:len(capWriter.prefix)-1]
	}
	return Event{ID: "git:" + sha, Commit: sha, Parents: parents, Subject: fields[1], Body: fields[2], Author: fields[3], AuthorTime: fields[4], CommitterTime: fields[5], Files: files, Diff: string(capWriter.prefix), DiffSHA256: hex.EncodeToString(capWriter.hash.Sum(nil)), DiffBytes: capWriter.count, DiffTruncated: capWriter.count > int64(cfg.MaxDiffBytes)}, nil
}

func emptyTree(ctx context.Context, root string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "hash-object", "-t", "tree", "--stdin")
	cmd.Stdin = strings.NewReader("")
	b, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

type boundedWriter struct {
	limit  int
	prefix []byte
	count  int64
	hash   hash.Hash
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	w.count += int64(len(p))
	if w.hash == nil {
		w.hash = sha256.New()
	}
	_, _ = w.hash.Write(p)
	n := w.limit - len(w.prefix)
	if n > len(p) {
		n = len(p)
	}
	if n > 0 {
		w.prefix = append(w.prefix, p[:n]...)
	}
	return len(p), nil
}

func parseNumstat(raw []byte, skip []string) ([]File, error) {
	if len(raw) == 0 {
		return []File{}, nil
	}
	parts := bytes.Split(raw, []byte{0})
	if len(parts) == 0 || len(parts[len(parts)-1]) != 0 {
		return nil, errors.New("unterminated numstat")
	}
	parts = parts[:len(parts)-1]
	out := make([]File, 0, len(parts))
	for i := 0; i < len(parts); i++ {
		entry := parts[i]
		fields := bytes.SplitN(entry, []byte{'\t'}, 3)
		if len(fields) != 3 {
			return nil, errors.New("invalid numstat record")
		}
		f := File{}
		if len(fields[2]) == 0 {
			if i+2 >= len(parts) {
				return nil, errors.New("invalid renamed numstat")
			}
			f.OldPath = string(parts[i+1])
			f.Path = string(parts[i+2])
			i += 2
		} else {
			f.Path = string(fields[2])
		}
		if !utf8.ValidString(f.Path) || !utf8.ValidString(f.OldPath) {
			return nil, fmt.Errorf("Git path is not UTF-8")
		}
		if string(fields[0]) == "-" && string(fields[1]) == "-" {
			f.Binary = true
		} else {
			a, e := strconv.Atoi(string(fields[0]))
			if e != nil {
				return nil, e
			}
			d, e := strconv.Atoi(string(fields[1]))
			if e != nil {
				return nil, e
			}
			f.Added = &a
			f.Deleted = &d
		}
		for _, pattern := range skip {
			if pattern == "" {
				continue
			}
			matched, _ := filepath.Match(pattern, f.Path)
			if matched {
				f.SkippedReason = "configured_pattern:" + pattern
				break
			}
		}
		base := filepath.Base(f.Path)
		if f.SkippedReason == "" && (strings.HasSuffix(f.Path, ".lock") || strings.Contains(f.Path, "/generated/") || base == "go.sum" || base == "package-lock.json" || base == "yarn.lock" || base == "Cargo.lock") {
			f.SkippedReason = "default_generated_or_lock"
		}
		out = append(out, f)
	}
	return out, nil
}

func mergeStatuses(files []File, raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	parts := bytes.Split(raw, []byte{0})
	if len(parts[len(parts)-1]) != 0 {
		return errors.New("unterminated name-status")
	}
	parts = parts[:len(parts)-1]
	byPath := make(map[string]int, len(files))
	for i, f := range files {
		byPath[f.Path] = i
	}
	for i := 0; i < len(parts); i++ {
		s := string(parts[i])
		if i+1 >= len(parts) {
			return errors.New("invalid name-status")
		}
		path := string(parts[i+1])
		i++
		if strings.HasPrefix(s, "R") || strings.HasPrefix(s, "C") {
			if i+1 >= len(parts) {
				return errors.New("invalid rename status")
			}
			path = string(parts[i+1])
			i++
		}
		idx, ok := byPath[path]
		if !ok {
			return fmt.Errorf("status path missing from numstat: %q", path)
		}
		files[idx].Status = s
	}
	return nil
}

var _ io.Writer = (*boundedWriter)(nil)
