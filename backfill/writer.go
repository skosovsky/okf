package backfill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
	storefs "github.com/skosovsky/okf/store/fs"
	"gopkg.in/yaml.v3"
)

// DocumentWriter is the narrow store boundary. The caller owns the store and
// may inject a fake for deterministic cancellation and replay tests.
type DocumentWriter interface {
	Snapshot(context.Context) (store.Snapshot, error)
	PreviewBackfillBatch(context.Context, storefs.BackfillBatchRequest) (storefs.BackfillBatchPreview, error)
	ApplyBackfillBatch(context.Context, storefs.BackfillBatchRequest, store.CommitOptions) (store.CommitReceipt, error)
	VerifyBackfillBatchReceipt(context.Context, storefs.BackfillBatchRequest, store.IdempotencyKey, store.CommitReceipt) error
	LookupBackfillBatchReceipt(context.Context, storefs.BackfillBatchRequest, store.IdempotencyKey) (store.CommitReceipt, bool, error)
}

type PlannedConcept struct {
	CandidateID string `json:"candidate_id"`
	ConceptID   string `json:"concept_id"`
	Document    string `json:"document"`
}

type PlannedIndex struct {
	Path     string `json:"path"`
	Document string `json:"document"`
}

type Plan struct {
	SchemaVersion  string           `json:"schema_version"`
	EventsSHA256   string           `json:"events_sha256"`
	AnalysisSHA256 string           `json:"analysis_sha256"`
	BaseRevision   store.Revision   `json:"base_revision"`
	IndexDocument  string           `json:"index_document"`
	Indexes        []PlannedIndex   `json:"indexes"`
	Concepts       []PlannedConcept `json:"concepts"`
	Coverage       Coverage         `json:"coverage"`
}

// Prepare has no bundle side effects. Every concept is a draft and contains
// only citations to exactly bound Git events.
func Prepare(ctx context.Context, writer DocumentWriter, m Manifest, a Analysis) (Plan, error) {
	coverage, active, err := ValidateAnalysis(m, a)
	if err != nil {
		return Plan{}, err
	}
	sha, err := AnalysisDigest(a)
	if err != nil {
		return Plan{}, err
	}
	snapshot, err := writer.Snapshot(ctx)
	if err != nil {
		return Plan{}, err
	}
	p := Plan{SchemaVersion: SchemaVersion, EventsSHA256: m.EventsSHA256, AnalysisSHA256: sha, BaseRevision: snapshot.Revision(), Concepts: []PlannedConcept{}, Coverage: coverage}
	events := map[string]Event{}
	for _, e := range m.Events {
		events[e.ID] = e
	}
	for _, c := range active {
		if err := ctx.Err(); err != nil {
			return Plan{}, err
		}
		doc, err := renderCandidate(c, a.ProducedAt, events)
		if err != nil {
			return Plan{}, err
		}
		raw, err := doc.Serialize()
		if err != nil {
			return Plan{}, err
		}
		p.Concepts = append(p.Concepts, PlannedConcept{CandidateID: c.ID, ConceptID: c.ConceptID, Document: raw})
	}
	if len(active) == 0 {
		return p, nil
	}
	p.IndexDocument, p.Indexes, err = desiredIndexes(ctx, snapshot, active)
	if err != nil {
		return Plan{}, err
	}
	request, err := batchRequest(p, p.BaseRevision)
	if err != nil {
		return Plan{}, err
	}
	if preview, err := writer.PreviewBackfillBatch(ctx, request); err != nil {
		return Plan{}, fmt.Errorf("backfill preview: %w: %v", err, preview.Validation.Diagnostics)
	}
	return p, nil
}

type indexEntry struct{ Target, Title, Description string }

func desiredIndexes(ctx context.Context, snapshot store.Snapshot, active []Candidate) (string, []PlannedIndex, error) {
	groups := map[string][]indexEntry{}
	for _, c := range active {
		segments := strings.Split(c.ConceptID, "/")
		for depth := 0; depth < len(segments); depth++ {
			indexPath := "index.md"
			if depth > 0 {
				indexPath = strings.Join(segments[:depth], "/") + "/index.md"
			}
			entry := indexEntry{}
			if depth == len(segments)-1 {
				entry = indexEntry{Target: segments[depth] + ".md", Title: c.Title, Description: c.Description}
			} else {
				entry = indexEntry{Target: segments[depth] + "/index.md", Title: segments[depth], Description: "Backfilled concepts."}
			}
			groups[indexPath] = append(groups[indexPath], entry)
		}
	}
	paths := make([]string, 0, len(groups))
	for p := range groups {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	indexes := []PlannedIndex{}
	root := ""
	for _, path := range paths {
		raw, err := snapshot.ReadFile(ctx, path)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) || path == "index.md" {
				return "", nil, fmt.Errorf("read index %s: %w", path, err)
			}
			raw = []byte("# Concepts\n")
		}
		doc, err := bundle.ParseDocument(string(raw))
		if err != nil {
			return "", nil, err
		}
		linked := map[string]bool{}
		text := string(raw)
		for _, link := range doc.Links() {
			linked[link.Target] = true
		}
		entries := groups[path]
		sort.Slice(entries, func(i, j int) bool { return entries[i].Target < entries[j].Target })
		for _, entry := range entries {
			if linked[entry.Target] {
				continue
			}
			addition := "\n- [" + escapeMarkdown(entry.Title) + "](" + entry.Target + ") - " + escapeMarkdown(entry.Description) + "\n"
			if path == "index.md" {
				doc.Body += addition
			} else {
				text += addition
			}
			linked[entry.Target] = true
		}
		serialized := text
		if path == "index.md" {
			serialized, err = doc.Serialize()
			if err != nil {
				return "", nil, err
			}
			root = serialized
		} else {
			indexes = append(indexes, PlannedIndex{Path: path, Document: serialized})
		}
	}
	return root, indexes, nil
}

func escapeMarkdown(value string) string {
	return strings.NewReplacer("\\", "\\\\", "[", "\\[", "]", "\\]", "\n", " ", "\r", " ").Replace(value)
}

func batchRequest(plan Plan, rev store.Revision) (storefs.BackfillBatchRequest, error) {
	index, err := bundle.ParseDocument(plan.IndexDocument)
	if err != nil {
		return storefs.BackfillBatchRequest{}, err
	}
	req := storefs.BackfillBatchRequest{ChangeSetID: store.ChangeSetID("backfill-" + plan.AnalysisSHA256[:32]), Actor: "process:okf-backfill", BaseRevision: rev, IndexDocument: index, Documents: []storefs.BackfillDocument{}}
	for _, p := range plan.Concepts {
		id, err := bundle.ParseConceptID(p.ConceptID)
		if err != nil {
			return req, err
		}
		doc, err := bundle.ParseDocument(p.Document)
		if err != nil {
			return req, err
		}
		req.Documents = append(req.Documents, storefs.BackfillDocument{ConceptID: id, Document: doc})
	}
	for _, entry := range plan.Indexes {
		req.IndexDocuments = append(req.IndexDocuments, storefs.BackfillIndex{Path: entry.Path, Content: entry.Document})
	}
	return req, nil
}

type sourceRecord struct {
	ID           string `yaml:"id"`
	Resource     string `yaml:"resource"`
	Title        string `yaml:"title"`
	Author       string `yaml:"author"`
	LastModified string `yaml:"last_modified"`
}

func renderCandidate(c Candidate, producedAt string, events map[string]Event) (bundle.Document, error) {
	fm := bundle.NewFrontmatter()
	for _, kv := range [][2]string{{"type", "Note"}, {"title", c.Title}, {"description", c.Description}, {"status", "draft"}} {
		if err := fm.SetString(kv[0], kv[1]); err != nil {
			return bundle.Document{}, err
		}
	}
	generated := map[string]string{"by": "process:okf-backfill", "at": producedAt}
	node, err := yamlNode(generated)
	if err != nil {
		return bundle.Document{}, err
	}
	if err := fm.Set("generated", node); err != nil {
		return bundle.Document{}, err
	}
	seen := map[string]bool{}
	sources := []sourceRecord{}
	notes := []string{}
	for _, ref := range c.Evidence {
		e := events[ref.EventID]
		if seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		id := "git-" + e.Commit[:12]
		sources = append(sources, sourceRecord{ID: id, Resource: "git:" + e.Commit, Title: e.Subject, Author: e.Author, LastModified: e.CommitterTime})
		notes = append(notes, fmt.Sprintf("[^%s]: Git commit `%s` (%s); diff sha256 `%s`.", id, e.Commit, e.CommitterTime, e.DiffSHA256))
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].ID < sources[j].ID })
	sort.Strings(notes)
	node, err = yamlNode(sources)
	if err != nil {
		return bundle.Document{}, err
	}
	if err := fm.Set("sources", node); err != nil {
		return bundle.Document{}, err
	}
	var body strings.Builder
	body.WriteString(strings.TrimSpace(c.Body))
	body.WriteString("\n\n## Evidence\n\n")
	for _, ref := range c.Evidence {
		e := events[ref.EventID]
		fmt.Fprintf(&body, "- Git commit `%s`", e.Commit)
		if ref.Path != "" {
			fmt.Fprintf(&body, " affecting `%s`", ref.Path)
		}
		fmt.Fprintf(&body, " [^git-%s]\n", e.Commit[:12])
	}
	if c.EvidenceIncomplete {
		body.WriteString("\nEvidence is incomplete: at least one diff was truncated during extraction. Claims beyond captured evidence require review.\n")
	}
	if len(c.History) > 0 {
		body.WriteString("\n## History\n\n")
		for _, h := range c.History {
			fmt.Fprintf(&body, "- %s: %s (event `%s`).\n", events[h.EventID].CommitterTime, strings.TrimSpace(h.Text), h.EventID)
		}
	}
	body.WriteString("\n")
	body.WriteString(strings.Join(notes, "\n"))
	body.WriteString("\n")
	return bundle.NewDocument(fm, body.String()), nil
}

func yamlNode(v any) (*yaml.Node, error) {
	b, err := yaml.Marshal(v)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) != 1 {
		return nil, fmt.Errorf("invalid YAML value")
	}
	return doc.Content[0], nil
}

// Apply publishes the complete desired state in one journal transaction. A
// checkpoint is written before commit; retry after a lost response replays it.
func Apply(ctx context.Context, writer DocumentWriter, plan Plan, m Manifest, a Analysis, checkpointPath string) (Checkpoint, error) {
	if checkpointPath == "" {
		return Checkpoint{}, fmt.Errorf("checkpoint path required")
	}
	if len(plan.Concepts) == 0 {
		return Checkpoint{}, fmt.Errorf("no publishable concepts; resolve conflicts or use plan coverage report")
	}
	coverage, active, err := ValidateAnalysis(m, a)
	if err != nil {
		return Checkpoint{}, err
	}
	if len(active) != len(plan.Concepts) || !reflect.DeepEqual(coverage, plan.Coverage) {
		return Checkpoint{}, fmt.Errorf("stale plan")
	}
	analysisHash, err := AnalysisDigest(a)
	if err != nil {
		return Checkpoint{}, err
	}
	if plan.SchemaVersion != SchemaVersion || plan.EventsSHA256 != m.EventsSHA256 || plan.AnalysisSHA256 != analysisHash || !plan.BaseRevision.Valid() {
		return Checkpoint{}, fmt.Errorf("plan binding mismatch")
	}
	for i, c := range active {
		if c.ID != plan.Concepts[i].CandidateID || c.ConceptID != plan.Concepts[i].ConceptID {
			return Checkpoint{}, fmt.Errorf("plan candidate mismatch")
		}
		d, err := renderCandidate(c, a.ProducedAt, eventMap(m))
		if err != nil {
			return Checkpoint{}, err
		}
		raw, err := d.Serialize()
		if err != nil || raw != plan.Concepts[i].Document {
			return Checkpoint{}, fmt.Errorf("plan document mismatch")
		}
	}
	if plan.IndexDocument == "" {
		return Checkpoint{}, fmt.Errorf("missing root index plan")
	}
	planBytes, err := json.Marshal(plan)
	if err != nil {
		return Checkpoint{}, err
	}
	planHash := digest(planBytes)
	cp := Checkpoint{SchemaVersion: SchemaVersion, ExtractorVersion: ExtractorVersion, ConfigSHA256: m.ConfigSHA256, EventsSHA256: m.EventsSHA256, AnalysisSHA256: analysisHash, PlanSHA256: planHash, PromptVersion: a.PromptVersion, BaseRevision: plan.BaseRevision, Receipts: []store.CommitReceipt{}}
	recoveredWithoutCheckpoint := false
	if data, err := os.ReadFile(checkpointPath); err == nil {
		if err := json.Unmarshal(data, &cp); err != nil {
			return Checkpoint{}, err
		}
		if cp.SchemaVersion != SchemaVersion || cp.ExtractorVersion != ExtractorVersion || cp.ConfigSHA256 != m.ConfigSHA256 || cp.EventsSHA256 != m.EventsSHA256 || cp.AnalysisSHA256 != analysisHash || cp.PlanSHA256 != planHash || cp.PromptVersion != a.PromptVersion || cp.NextCandidate < 0 || cp.NextCandidate > 1 || len(cp.Receipts) != cp.NextCandidate {
			return Checkpoint{}, fmt.Errorf("checkpoint binding mismatch")
		}
		if cp.NextCandidate == 0 && cp.BaseRevision != plan.BaseRevision {
			return Checkpoint{}, fmt.Errorf("checkpoint base mismatch")
		}
		if cp.NextCandidate > 0 && cp.BaseRevision != cp.Receipts[cp.NextCandidate-1].ResultRevision {
			return Checkpoint{}, fmt.Errorf("checkpoint revision mismatch")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		request, requestErr := batchRequest(plan, plan.BaseRevision)
		if requestErr != nil {
			return Checkpoint{}, requestErr
		}
		key := store.IdempotencyKey(request.ChangeSetID)
		receipt, found, lookupErr := writer.LookupBackfillBatchReceipt(ctx, request, key)
		if lookupErr != nil {
			return Checkpoint{}, lookupErr
		}
		if found {
			cp.Receipts = append(cp.Receipts, receipt)
			cp.NextCandidate = 1
			cp.BaseRevision = receipt.ResultRevision
			recoveredWithoutCheckpoint = true
		} else {
			snapshot, snapshotErr := writer.Snapshot(ctx)
			if snapshotErr != nil {
				return Checkpoint{}, snapshotErr
			}
			if snapshot.Revision() != plan.BaseRevision {
				return Checkpoint{}, &store.Conflict{Expected: plan.BaseRevision, Actual: snapshot.Revision(), Retryable: true}
			}
			expectedIndex, expectedIndexes, indexErr := desiredIndexes(ctx, snapshot, active)
			if indexErr != nil {
				return Checkpoint{}, indexErr
			}
			if expectedIndex != plan.IndexDocument || !reflect.DeepEqual(expectedIndexes, plan.Indexes) {
				return Checkpoint{}, fmt.Errorf("root index plan mismatch")
			}
			if err := saveCheckpoint(checkpointPath, cp); err != nil {
				return Checkpoint{}, err
			}
		}
	} else {
		return Checkpoint{}, err
	}
	if cp.NextCandidate == 1 {
		request, err := batchRequest(plan, plan.BaseRevision)
		if err != nil {
			return cp, err
		}
		key := store.IdempotencyKey(request.ChangeSetID)
		if err := writer.VerifyBackfillBatchReceipt(ctx, request, key, cp.Receipts[0]); err != nil {
			return cp, err
		}
		snapshot, err := writer.Snapshot(ctx)
		if err != nil {
			return cp, err
		}
		if snapshot.Revision() != cp.BaseRevision || snapshot.Revision() != cp.Receipts[0].ResultRevision {
			return cp, fmt.Errorf("completed checkpoint no longer matches bundle revision")
		}
		indexBytes, err := snapshot.ReadFile(ctx, "index.md")
		if err != nil {
			return cp, err
		}
		if string(indexBytes) != plan.IndexDocument {
			return cp, fmt.Errorf("completed checkpoint root index differs")
		}
		for _, entry := range plan.Indexes {
			raw, err := snapshot.ReadFile(ctx, entry.Path)
			if err != nil {
				return cp, err
			}
			if string(raw) != entry.Document {
				return cp, fmt.Errorf("completed checkpoint directory index %s differs", entry.Path)
			}
		}
		for _, concept := range plan.Concepts {
			raw, err := snapshot.ReadFile(ctx, concept.ConceptID+".md")
			if err != nil {
				return cp, err
			}
			if string(raw) != concept.Document {
				return cp, fmt.Errorf("completed checkpoint concept %s differs", concept.ConceptID)
			}
		}
		if recoveredWithoutCheckpoint {
			if err := saveCheckpoint(checkpointPath, cp); err != nil {
				return cp, err
			}
		}
		return cp, nil
	}
	if err := ctx.Err(); err != nil {
		return cp, err
	}
	req, err := batchRequest(plan, cp.BaseRevision)
	if err != nil {
		return cp, err
	}
	key := store.IdempotencyKey(req.ChangeSetID)
	receipt, commitErr := writer.ApplyBackfillBatch(ctx, req, store.CommitOptions{IdempotencyKey: key})
	if commitErr != nil {
		var committed *store.CommittedError
		if errors.As(commitErr, &committed) {
			receipt = committed.Receipt()
		} else {
			return cp, commitErr
		}
	}
	if !receipt.ResultRevision.Valid() {
		return cp, fmt.Errorf("store returned no durable receipt")
	}
	cp.Receipts = append(cp.Receipts, receipt)
	cp.NextCandidate = 1
	cp.BaseRevision = receipt.ResultRevision
	if err := saveCheckpoint(checkpointPath, cp); err != nil {
		return cp, err
	}
	if commitErr != nil {
		return cp, commitErr
	}
	return cp, nil
}

func eventMap(m Manifest) map[string]Event {
	out := make(map[string]Event, len(m.Events))
	for _, e := range m.Events {
		out[e.ID] = e
	}
	return out
}

func saveCheckpoint(path string, cp Checkpoint) error {
	// CommitReceipt requires its nested wire form to remain canonical JSON.
	b, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".backfill-checkpoint-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
