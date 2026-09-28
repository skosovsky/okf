// Package upkeep checks whether changes made during a work session received an
// explicit knowledge review. It reports process evidence, never factual truth.
package upkeep

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

type Config struct {
	RepoRoot          string   `json:"repo_root"`
	BundleRoot        string   `json:"bundle_root"`
	RelevantPaths     []string `json:"relevant_paths"`
	ExcludePaths      []string `json:"exclude_paths,omitempty"`
	Mode              string   `json:"mode,omitempty"`           // advisory (default) or blocking
	FailurePolicy     string   `json:"failure_policy,omitempty"` // open (default) or closed
	TimeoutMS         int      `json:"timeout_ms,omitempty"`
	MaxSessionMinutes int      `json:"max_session_minutes,omitempty"`
}

type Entry struct {
	Path     string `json:"path"`
	OldPath  string `json:"old_path,omitempty"`
	Status   string `json:"status"`
	Contents string `json:"contents,omitempty"`
}

type Baseline struct {
	ConfigFingerprint string    `json:"config_fingerprint"`
	RepositoryID      string    `json:"repository_id"`
	Head              string    `json:"head"`
	CapturedAt        time.Time `json:"captured_at"`
	SessionID         string    `json:"session_id"`
	StateFingerprint  string    `json:"state_fingerprint"`
	Entries           []Entry   `json:"entries"`
}

type Decision struct {
	Fingerprint     string   `json:"fingerprint"`
	Kind            string   `json:"kind"` // updated or unaffected
	Reason          string   `json:"reason,omitempty"`
	UpdatedConcepts []string `json:"updated_concepts,omitempty"`
}

type Result struct {
	Status      string   `json:"status"` // no_relevant_change, reviewed_updated, explicit_unaffected, needs_review
	Fingerprint string   `json:"fingerprint"`
	Changes     []Entry  `json:"changes"`
	Reason      string   `json:"reason,omitempty"`
	Evidence    []string `json:"evidence,omitempty"`
	Advisory    bool     `json:"advisory"`
}

func LoadConfig(path string) (Config, error) {
	var cfg Config
	b, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return cfg, err
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return cfg, fmt.Errorf("config has trailing data")
	}
	if cfg.RepoRoot == "" {
		return cfg, errors.New("repo_root is required")
	}
	if !filepath.IsAbs(cfg.RepoRoot) {
		cfg.RepoRoot = filepath.Join(filepath.Dir(path), cfg.RepoRoot)
	}
	cfg.RepoRoot, err = filepath.Abs(cfg.RepoRoot)
	if err != nil {
		return cfg, err
	}
	if cfg.BundleRoot == "" || len(cfg.RelevantPaths) == 0 {
		return cfg, errors.New("bundle_root and relevant_paths are required")
	}
	if err := validPath(cfg.BundleRoot); err != nil {
		return cfg, fmt.Errorf("bundle_root: %w", err)
	}
	for _, p := range append(append([]string{}, cfg.RelevantPaths...), cfg.ExcludePaths...) {
		if err := validPath(p); err != nil {
			return cfg, fmt.Errorf("path %q: %w", p, err)
		}
	}
	if cfg.Mode == "" {
		cfg.Mode = "advisory"
	}
	if cfg.Mode != "advisory" && cfg.Mode != "blocking" {
		return cfg, errors.New("mode must be advisory or blocking")
	}
	if cfg.FailurePolicy == "" {
		cfg.FailurePolicy = "open"
	}
	if cfg.FailurePolicy != "open" && cfg.FailurePolicy != "closed" {
		return cfg, errors.New("failure_policy must be open or closed")
	}
	if cfg.TimeoutMS == 0 {
		cfg.TimeoutMS = 5000
	}
	if cfg.TimeoutMS < 1 || cfg.TimeoutMS > 60000 {
		return cfg, errors.New("timeout_ms must be 1..60000")
	}
	if cfg.MaxSessionMinutes == 0 {
		cfg.MaxSessionMinutes = 1440
	}
	if cfg.MaxSessionMinutes < 1 || cfg.MaxSessionMinutes > 10080 {
		return cfg, errors.New("max_session_minutes must be 1..10080")
	}
	return cfg, nil
}

func validPath(p string) error {
	if p == "" || filepath.IsAbs(p) || path.IsAbs(p) || path.Clean(p) != p || p == "." || strings.HasPrefix(p, "../") || p == ".." || strings.Contains(p, "\\") {
		return errors.New("must be a clean relative path within repo")
	}
	return nil
}

func configFingerprint(cfg Config) string {
	b, _ := json.Marshal(cfg)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func Snapshot(ctx context.Context, cfg Config) (Baseline, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(cfg.TimeoutMS)*time.Millisecond)
	defer cancel()
	repoID, head, err := repositoryIdentity(ctx, cfg.RepoRoot)
	if err != nil {
		return Baseline{}, err
	}
	cmd := exec.CommandContext(ctx, "git", "-C", cfg.RepoRoot, "-c", "core.fsmonitor=false", "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignored=no")
	cmd.WaitDelay = 100 * time.Millisecond
	buf, err := cmd.Output()
	if err != nil {
		return Baseline{}, fmt.Errorf("git status: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Baseline{}, err
	}
	entries, err := parseStatus(buf)
	if err != nil {
		return Baseline{}, err
	}
	for i := range entries {
		// The status alone cannot distinguish a new edit to an already dirty file.
		// Hash the working copy, including untracked files and symlinks.
		if err := ctx.Err(); err != nil {
			return Baseline{}, err
		}
		entries[i].Contents, err = contentsHash(ctx, cfg.RepoRoot, entries[i].Path)
		if err != nil {
			return Baseline{}, err
		}
	}
	sortEntries(entries)
	finalRepoID, finalHead, err := repositoryIdentity(ctx, cfg.RepoRoot)
	if err != nil {
		return Baseline{}, err
	}
	if finalRepoID != repoID || finalHead != head {
		return Baseline{}, errors.New("repository identity or HEAD changed during snapshot")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return Baseline{}, err
	}
	snap := Baseline{ConfigFingerprint: configFingerprint(cfg), RepositoryID: repoID, Head: head, CapturedAt: time.Now().UTC(), SessionID: hex.EncodeToString(nonce[:]), Entries: entries}
	snap.StateFingerprint = stateFingerprint(snap)
	return snap, nil
}

func gitOutput(ctx context.Context, root string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	cmd.WaitDelay = 100 * time.Millisecond
	b, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %v: %w", args, err)
	}
	out := string(b)
	if strings.HasSuffix(out, "\r\n") {
		out = strings.TrimSuffix(out, "\r\n")
	} else {
		out = strings.TrimSuffix(out, "\n")
	}
	return out, nil
}

func committedContentsHash(cfg Config, head, name string) (string, error) {
	if err := validPath(name); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.TimeoutMS)*time.Millisecond)
	defer cancel()
	list := exec.CommandContext(ctx, "git", "-C", cfg.RepoRoot, "ls-tree", "-z", head, "--", name)
	list.Env = append(os.Environ(), "GIT_LITERAL_PATHSPECS=1")
	list.WaitDelay = 100 * time.Millisecond
	b, err := list.Output()
	if err != nil {
		return "", fmt.Errorf("git ls-tree: %w", err)
	}
	if len(b) == 0 {
		return "deleted", nil
	}
	if b[len(b)-1] != 0 {
		return "", errors.New("unterminated ls-tree record")
	}
	record := b[:len(b)-1]
	meta, actual, ok := bytes.Cut(record, []byte{'\t'})
	if !ok || string(actual) != name {
		return "", fmt.Errorf("unexpected ls-tree path for %q", name)
	}
	fields := bytes.Fields(meta)
	if len(fields) != 3 || string(fields[1]) != "blob" {
		return "", fmt.Errorf("unsupported Git tree entry %q", name)
	}
	h := sha256.New()
	switch string(fields[0]) {
	case "100644":
		_, _ = io.WriteString(h, "mode:0644\x00")
	case "100755":
		_, _ = io.WriteString(h, "mode:0755\x00")
	case "120000":
		_, _ = io.WriteString(h, "symlink:")
	default:
		return "", fmt.Errorf("unsupported Git file mode for %q", name)
	}
	cat := exec.CommandContext(ctx, "git", "-C", cfg.RepoRoot, "cat-file", "blob", head+":"+name)
	cat.WaitDelay = 100 * time.Millisecond
	cat.Stdout = h
	if err := cat.Run(); err != nil {
		return "", fmt.Errorf("git cat-file: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func repositoryIdentity(ctx context.Context, root string) (string, string, error) {
	top, err := gitOutput(ctx, root, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", err
	}
	physicalTop, err := filepath.EvalSymlinks(top)
	if err != nil {
		return "", "", err
	}
	physicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", err
	}
	if physicalTop != physicalRoot {
		return "", "", errors.New("repo_root must be the Git worktree root")
	}
	gitDir, err := gitOutput(ctx, root, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", "", err
	}
	physicalGitDir, err := filepath.EvalSymlinks(gitDir)
	if err != nil {
		return "", "", err
	}
	gitDirIdentity, err := repositoryDirectoryIdentity(physicalGitDir)
	if err != nil {
		return "", "", err
	}
	head, err := gitOutput(ctx, root, "rev-parse", "--verify", "HEAD")
	if err != nil {
		if ctx.Err() != nil {
			return "", "", ctx.Err()
		}
		head = "unborn"
	}
	h := sha256.Sum256([]byte(physicalTop + "\x00" + physicalGitDir + "\x00" + gitDirIdentity))
	return hex.EncodeToString(h[:]), head, nil
}

func stateFingerprint(s Baseline) string {
	b, _ := json.Marshal(struct {
		Config  string  `json:"config"`
		Repo    string  `json:"repo"`
		Head    string  `json:"head"`
		Entries []Entry `json:"entries"`
	}{s.ConfigFingerprint, s.RepositoryID, s.Head, s.Entries})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func parseStatus(buf []byte) ([]Entry, error) {
	var out []Entry
	for len(buf) > 0 {
		if len(buf) < 4 || buf[2] != ' ' {
			return nil, errors.New("invalid git porcelain record")
		}
		end := bytes.IndexByte(buf[3:], 0)
		if end < 0 {
			return nil, errors.New("unterminated git porcelain path")
		}
		path := string(buf[3 : 3+end])
		status := string(buf[:2])
		buf = buf[4+end:]
		entry := Entry{Path: path, Status: status}
		if strings.ContainsAny(status, "RC") {
			end = bytes.IndexByte(buf, 0)
			if end < 0 {
				return nil, errors.New("unterminated git rename source")
			}
			entry.OldPath = string(buf[:end])
			buf = buf[end+1:]
		}
		out = append(out, entry)
	}
	return out, nil
}

func contentsHash(ctx context.Context, root, path string) (string, error) {
	f, special, err := openBounded(ctx, root, path)
	if err != nil {
		return "", err
	}
	if special == "deleted" {
		return "deleted", nil
	}
	if strings.HasPrefix(special, "symlink:") {
		h := sha256.Sum256([]byte(special))
		return hex.EncodeToString(h[:]), nil
	}
	defer f.Close()
	h := sha256.New()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	gitMode := "0644"
	if info.Mode().Perm()&0111 != 0 {
		gitMode = "0755"
	}
	_, _ = io.WriteString(h, "mode:"+gitMode+"\x00")
	buf := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, readErr := f.Read(buf)
		if n > 0 {
			if _, err := h.Write(buf[:n]); err != nil {
				return "", err
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func sortEntries(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Path+"\x00"+entries[i].OldPath < entries[j].Path+"\x00"+entries[j].OldPath
	})
}

func pathIn(path, prefix string) bool { return path == prefix || strings.HasPrefix(path, prefix+"/") }

func relevant(e Entry, cfg Config) bool {
	for _, p := range []string{e.Path, e.OldPath} {
		if p == "" {
			continue
		}
		excluded := false
		for _, ex := range cfg.ExcludePaths {
			if pathIn(p, filepath.ToSlash(ex)) {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}
		for _, inc := range cfg.RelevantPaths {
			if pathIn(p, filepath.ToSlash(inc)) {
				return true
			}
		}
	}
	return false
}

func key(e Entry) string { return e.Path + "\x00" + e.OldPath }

func committedChanges(cfg Config, oldHead, newHead string) ([]Entry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.TimeoutMS)*time.Millisecond)
	defer cancel()
	if newHead == "unborn" {
		return nil, errors.New("HEAD disappeared after baseline")
	}
	var args []string
	if oldHead == "unborn" {
		args = []string{"ls-tree", "-r", "--name-only", "-z", newHead}
	} else {
		args = []string{"diff", "--name-status", "-z", "-M", oldHead, newHead, "--"}
	}
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", cfg.RepoRoot}, args...)...)
	cmd.WaitDelay = 100 * time.Millisecond
	b, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git committed changes: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var out []Entry
	for len(b) > 0 {
		end := bytes.IndexByte(b, 0)
		if end < 0 {
			return nil, errors.New("unterminated committed change")
		}
		first := string(b[:end])
		b = b[end+1:]
		if oldHead == "unborn" {
			out = append(out, Entry{Path: first, Status: "committed-A", Contents: newHead})
			continue
		}
		end = bytes.IndexByte(b, 0)
		if end < 0 {
			return nil, errors.New("unterminated committed path")
		}
		p := string(b[:end])
		b = b[end+1:]
		e := Entry{Path: p, Status: "committed-" + first, Contents: newHead}
		if strings.HasPrefix(first, "R") || strings.HasPrefix(first, "C") {
			end = bytes.IndexByte(b, 0)
			if end < 0 {
				return nil, errors.New("unterminated committed rename")
			}
			e.OldPath, e.Path = p, string(b[:end])
			b = b[end+1:]
		}
		out = append(out, e)
	}
	return out, nil
}

func Changes(cfg Config, baseline, current Baseline) ([]Entry, string, error) {
	if baseline.ConfigFingerprint != configFingerprint(cfg) || current.ConfigFingerprint != configFingerprint(cfg) {
		return nil, "", errors.New("baseline/config mismatch")
	}
	if baseline.RepositoryID == "" || baseline.RepositoryID != current.RepositoryID {
		return nil, "", errors.New("baseline repository mismatch")
	}
	maxAge := cfg.MaxSessionMinutes
	if maxAge == 0 {
		maxAge = 1440
	}
	if baseline.SessionID == "" || baseline.CapturedAt.IsZero() || current.CapturedAt.Before(baseline.CapturedAt) || current.CapturedAt.Sub(baseline.CapturedAt) > time.Duration(maxAge)*time.Minute {
		return nil, "", errors.New("baseline session is absent, expired, or later than current state")
	}
	if baseline.StateFingerprint != stateFingerprint(baseline) || current.StateFingerprint != stateFingerprint(current) {
		return nil, "", errors.New("baseline or current state fingerprint mismatch")
	}
	before := make(map[string]Entry, len(baseline.Entries))
	after := make(map[string]Entry, len(current.Entries))
	for _, e := range baseline.Entries {
		before[key(e)] = e
	}
	for _, e := range current.Entries {
		after[key(e)] = e
	}
	keys := make(map[string]struct{}, len(before)+len(after))
	for k := range before {
		keys[k] = struct{}{}
	}
	for k := range after {
		keys[k] = struct{}{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.TimeoutMS)*time.Millisecond)
	defer cancel()
	contentCache := map[string]string{}
	currentContent := func(p string) (string, error) {
		if value, ok := contentCache[p]; ok {
			return value, nil
		}
		value, err := contentsHash(ctx, cfg.RepoRoot, p)
		if err != nil {
			return "", err
		}
		contentCache[p] = value
		return value, nil
	}
	var changed []Entry
	for k := range keys {
		old, hadOld := before[k]
		newEntry, hasNew := after[k]
		if hadOld && hasNew && old.Contents == newEntry.Contents {
			continue
		}
		if !hasNew {
			actual, err := currentContent(old.Path)
			if err != nil {
				return nil, "", err
			}
			if actual == old.Contents {
				continue
			}
			newEntry = Entry{Path: old.Path, OldPath: old.OldPath, Status: "cleaned", Contents: actual}
		}
		if relevant(newEntry, cfg) {
			changed = append(changed, newEntry)
		}
	}
	if baseline.Head != current.Head {
		committed, err := committedChanges(cfg, baseline.Head, current.Head)
		if err != nil {
			return nil, "", err
		}
		for _, e := range committed {
			if relevant(e, cfg) {
				if old := baselineEntryForCommitted(baseline.Entries, e); old != nil {
					actual, err := committedContentsHash(cfg, current.Head, e.Path)
					if err != nil {
						return nil, "", err
					}
					if actual == old.Contents {
						continue
					}
				}
				changed = append(changed, e)
			}
		}
	}
	sortEntries(changed)
	b, _ := json.Marshal(struct {
		Config      string    `json:"config"`
		Session     string    `json:"session"`
		Started     time.Time `json:"started"`
		State       string    `json:"state"`
		CurrentHead string    `json:"current_head"`
		Baseline    []Entry   `json:"baseline"`
		Entries     []Entry   `json:"entries"`
	}{configFingerprint(cfg), baseline.SessionID, baseline.CapturedAt, baseline.StateFingerprint, current.Head, baseline.Entries, changed})
	h := sha256.Sum256(b)
	return changed, hex.EncodeToString(h[:]), nil
}

func Check(cfg Config, baseline, current Baseline, decision *Decision) (Result, error) {
	changes, fingerprint, err := Changes(cfg, baseline, current)
	if err != nil {
		return Result{}, err
	}
	r := Result{Status: "needs_review", Fingerprint: fingerprint, Changes: changes, Advisory: cfg.Mode != "blocking"}
	if len(changes) == 0 {
		r.Changes = []Entry{}
		r.Status = "no_relevant_change"
		return r, nil
	}
	if decision == nil || decision.Fingerprint != fingerprint {
		return r, nil
	}
	switch decision.Kind {
	case "unaffected":
		if strings.TrimSpace(decision.Reason) != "" {
			r.Status = "explicit_unaffected"
			r.Reason = decision.Reason
		}
	case "updated":
		var committed []Entry
		if baseline.Head != current.Head {
			committed, err = committedChanges(cfg, baseline.Head, current.Head)
			if err != nil {
				return Result{}, err
			}
		}
		for _, concept := range decision.UpdatedConcepts {
			if !pathIn(concept, filepath.ToSlash(cfg.BundleRoot)) || !strings.HasSuffix(concept, ".md") || concept == filepath.ToSlash(cfg.BundleRoot)+"/log.md" || concept == filepath.ToSlash(cfg.BundleRoot)+"/index.md" {
				continue
			}
			for _, e := range current.Entries {
				if e.Path != concept || e.Contents == "deleted" {
					continue
				}
				old := findEntry(baseline.Entries, key(e))
				if old == nil || old.Contents != e.Contents {
					r.Evidence = append(r.Evidence, concept)
				}
			}
			deletedNow := false
			for _, e := range current.Entries {
				if e.Path == concept && e.Contents == "deleted" {
					deletedNow = true
				}
			}
			if deletedNow {
				continue
			}
			for _, e := range committed {
				if e.Path == concept && e.Status != "committed-D" && !slices.Contains(r.Evidence, concept) {
					if old := baselineEntryForCommitted(baseline.Entries, e); old != nil {
						actual, hashErr := committedContentsHash(cfg, current.Head, concept)
						if hashErr != nil {
							return Result{}, hashErr
						}
						if actual == old.Contents {
							continue
						}
					}
					r.Evidence = append(r.Evidence, concept)
					break
				}
			}
		}
		if len(r.Evidence) > 0 {
			r.Status = "reviewed_updated"
		}
	}
	return r, nil
}

func findEntry(entries []Entry, k string) *Entry {
	for i := range entries {
		if key(entries[i]) == k {
			return &entries[i]
		}
	}
	return nil
}

func baselineEntryForCommitted(entries []Entry, committed Entry) *Entry {
	return findEntry(entries, key(Entry{Path: committed.Path, OldPath: committed.OldPath}))
}
