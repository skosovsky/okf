package mutation

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
	"github.com/skosovsky/okf/validator"
	"gopkg.in/yaml.v3"
)

var ErrTemporalUpgradeIncomplete = errors.New("temporal upgrade has unresolved legacy dates")
var ErrTemporalUpgradeNoop = errors.New("temporal upgrade has no legacy dates")

// TemporalUpgradeMapping is caller-supplied policy for one observed date.
// Neither the planner nor the standard derives a time of day or UTC offset.
type TemporalUpgradeMapping struct {
	Concept bundle.ConceptID `json:"concept"`
	Path    string           `json:"path"`
	From    string           `json:"from"`
	To      string           `json:"to"`
}

type TemporalUpgradeRequest struct {
	ID       store.ChangeSetID        `json:"id"`
	Actor    store.Actor              `json:"actor"`
	Mappings []TemporalUpgradeMapping `json:"mappings"`
}

type TemporalUpgradePreview struct {
	Preview    store.Preview            `json:"preview"`
	Unresolved []TemporalUpgradeMapping `json:"unresolved"`
	PlanDigest string                   `json:"plan_digest,omitempty"`
	Change     store.ChangeSet          `json:"-"`
}

type TemporalUpgradeApplyRequest struct {
	Request      TemporalUpgradeRequest
	BaseRevision store.Revision
	PlanDigest   string
	Options      store.CommitOptions
}

type TemporalUpgradePlanner struct{}

func NewTemporalUpgradePlanner() TemporalUpgradePlanner { return TemporalUpgradePlanner{} }

func (TemporalUpgradePlanner) Preview(ctx context.Context, source bundle.Source, request TemporalUpgradeRequest) (TemporalUpgradePreview, error) {
	if err := ctx.Err(); err != nil {
		return TemporalUpgradePreview{}, err
	}
	if source == nil {
		return TemporalUpgradePreview{}, errors.New("nil temporal upgrade source")
	}
	if err := request.ID.Validate(); err != nil {
		return TemporalUpgradePreview{}, err
	}
	if err := request.Actor.Validate(); err != nil {
		return TemporalUpgradePreview{}, err
	}
	base, err := revision(ctx, source)
	if err != nil {
		return TemporalUpgradePreview{}, err
	}
	loaded, err := bundle.Load(ctx, source)
	if err != nil {
		return TemporalUpgradePreview{}, err
	}
	version, err := loaded.VersionResolutionContext(ctx, "")
	if err != nil {
		return TemporalUpgradePreview{}, err
	}
	if version.Effective != bundle.OKFVersion || (version.Declared != "" && version.Declared != bundle.OKFVersion) {
		return TemporalUpgradePreview{}, fmt.Errorf("temporal revision upgrade requires OKF 0.2, found %s", version.Effective)
	}
	ids, err := loaded.ConceptIDsContext(ctx)
	if err != nil {
		return TemporalUpgradePreview{}, err
	}
	wanted := make(map[string]TemporalUpgradeMapping, len(request.Mappings))
	for _, mapping := range request.Mappings {
		if err := ctx.Err(); err != nil {
			return TemporalUpgradePreview{}, err
		}
		key := temporalUpgradeMappingKey(mapping.Concept, mapping.Path)
		if _, exists := wanted[key]; exists {
			return TemporalUpgradePreview{}, fmt.Errorf("duplicate temporal mapping %s", key)
		}
		wanted[key] = mapping
	}
	seen := make(map[string]bool, len(wanted))
	var operations []store.Operation
	var unresolved []TemporalUpgradeMapping
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return TemporalUpgradePreview{}, err
		}
		file, ok, err := loaded.ConceptPathContext(ctx, id)
		if err != nil {
			return TemporalUpgradePreview{}, err
		}
		if !ok {
			return TemporalUpgradePreview{}, fmt.Errorf("missing concept %s", id)
		}
		data, ok, err := readBundleFileContext(ctx, loaded, file)
		if err != nil {
			return TemporalUpgradePreview{}, err
		}
		if !ok {
			return TemporalUpgradePreview{}, fmt.Errorf("missing concept file %s", file)
		}
		p, err := parsePresentationContext(ctx, data)
		if err != nil {
			return TemporalUpgradePreview{}, err
		}
		observed, err := temporalUpgradeObserve(p)
		if err != nil {
			return TemporalUpgradePreview{}, fmt.Errorf("%s: %w", file, err)
		}
		var rewrites []store.TemporalFieldRewrite
		for _, field := range observed {
			key := temporalUpgradeMappingKey(id, field.Path)
			mapping, found := wanted[key]
			if !found {
				unresolved = append(unresolved, TemporalUpgradeMapping{Concept: id, Path: field.Path, From: field.From})
				continue
			}
			seen[key] = true
			if mapping.From != field.From {
				return TemporalUpgradePreview{}, fmt.Errorf("%s: mapping source changed", key)
			}
			rewrites = append(rewrites, store.TemporalFieldRewrite{Path: field.Path, From: field.From, To: mapping.To})
		}
		if len(rewrites) != 0 {
			op, err := store.NewUpgradeTemporalConcept(id, rewrites)
			if err != nil {
				return TemporalUpgradePreview{}, err
			}
			operations = append(operations, op)
		}
	}
	for key := range wanted {
		if !seen[key] {
			return TemporalUpgradePreview{}, fmt.Errorf("temporal mapping %s has no legacy date", key)
		}
	}
	blocked := TemporalUpgradePreview{Preview: store.Preview{BaseRevision: base, ResultRevision: base}, Unresolved: unresolved}
	if len(unresolved) != 0 {
		return blocked, ErrTemporalUpgradeIncomplete
	}
	if len(operations) == 0 {
		return blocked, ErrTemporalUpgradeNoop
	}
	change := store.ChangeSet{Version: store.ChangeSetFormatVersion, ID: request.ID, Actor: request.Actor, BaseRevision: base, Operations: operations}
	config := &validator.ValidatorConfig{TemporalProfile: bundle.TemporalProfileInstant}
	result, err := NewPlanner(config).Plan(ctx, source, change)
	if err != nil {
		return TemporalUpgradePreview{Preview: result.Preview}, err
	}
	digest, err := planDigestContext(ctx, change, result.Preview)
	if err != nil {
		return TemporalUpgradePreview{}, err
	}
	return TemporalUpgradePreview{Preview: result.Preview, PlanDigest: digest, Change: change}, nil
}

func (p TemporalUpgradePlanner) Apply(ctx context.Context, destination store.Store, request TemporalUpgradeApplyRequest) (store.CommitReceipt, error) {
	if err := ctx.Err(); err != nil {
		return store.CommitReceipt{}, err
	}
	if destination == nil {
		return store.CommitReceipt{}, errors.New("nil temporal upgrade store")
	}
	if !request.BaseRevision.Valid() {
		return store.CommitReceipt{}, store.ErrInvalidRevision
	}
	if err := ValidatePlanDigest(request.PlanDigest); err != nil {
		return store.CommitReceipt{}, err
	}
	options := request.Options
	key, err := PlanDigestIdempotencyKey("temporal-upgrade:v1", request.PlanDigest, options.IdempotencyKey)
	if err != nil {
		return store.CommitReceipt{}, err
	}
	options.IdempotencyKey = key
	snapshot, err := destination.Snapshot(ctx)
	if err != nil {
		return store.CommitReceipt{}, err
	}
	if snapshot.Revision() != request.BaseRevision {
		// The durable store resolves a matching replay before its revision CAS.
		// A different request conflicts without publishing.
		change, err := temporalUpgradeChangeFromRequest(request)
		if err != nil {
			return store.CommitReceipt{}, err
		}
		return destination.Commit(ctx, change, options)
	}
	fresh, err := p.Preview(ctx, snapshot, request.Request)
	if err != nil {
		return store.CommitReceipt{}, err
	}
	if fresh.PlanDigest != request.PlanDigest || fresh.Preview.BaseRevision != request.BaseRevision {
		return store.CommitReceipt{}, ErrPlanDigestMismatch
	}
	if err := ctx.Err(); err != nil {
		return store.CommitReceipt{}, err
	}
	return destination.Commit(ctx, fresh.Change, options)
}

func temporalUpgradeMappingKey(id bundle.ConceptID, path string) string {
	return id.String() + "#" + path
}

func temporalUpgradeChangeFromRequest(request TemporalUpgradeApplyRequest) (store.ChangeSet, error) {
	byConcept := make(map[string][]store.TemporalFieldRewrite)
	for _, mapping := range request.Request.Mappings {
		key := mapping.Concept.String()
		byConcept[key] = append(byConcept[key], store.TemporalFieldRewrite{Path: mapping.Path, From: mapping.From, To: mapping.To})
	}
	keys := make([]string, 0, len(byConcept))
	for key := range byConcept {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var ops []store.Operation
	for _, key := range keys {
		id, err := bundle.ParseConceptID(key)
		if err != nil {
			return store.ChangeSet{}, err
		}
		op, err := store.NewUpgradeTemporalConcept(id, byConcept[key])
		if err != nil {
			return store.ChangeSet{}, err
		}
		ops = append(ops, op)
	}
	change := store.ChangeSet{Version: store.ChangeSetFormatVersion, ID: request.Request.ID, Actor: request.Request.Actor, BaseRevision: request.BaseRevision, Operations: ops}
	return change, change.Validate()
}

func temporalUpgradeObserve(p *presentation) ([]store.TemporalFieldRewrite, error) {
	var out []store.TemporalFieldRewrite
	observe := func(mapping *yaml.Node, key, path string) error {
		match, found, err := p.structuralMappingEntry(mapping, key)
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		if match.value.Kind != yaml.ScalarNode {
			return fmt.Errorf("%s is not scalar", path)
		}
		value := match.value.Value
		if parsed, err := time.Parse("2006-01-02", value); err == nil && parsed.Format("2006-01-02") == value {
			out = append(out, store.TemporalFieldRewrite{Path: path, From: value})
			return nil
		}
		if _, err := bundle.ParseOffsetDateTime(value); err == nil {
			return nil
		}
		return fmt.Errorf("%s has unsupported temporal value %q", path, value)
	}
	window := func(mapping *yaml.Node, prefix string) error {
		match, found, err := p.structuralMappingEntry(mapping, "usage_window")
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		if match.value.Kind != yaml.MappingNode {
			return fmt.Errorf("%susage_window is not mapping", prefix)
		}
		for _, key := range []string{"from", "to"} {
			if err := observe(match.value, key, prefix+"usage_window."+key); err != nil {
				return err
			}
		}
		return nil
	}
	if err := observe(p.root, "stale_after", "stale_after"); err != nil {
		return nil, err
	}
	if err := window(p.root, ""); err != nil {
		return nil, err
	}
	sources, found, err := p.structuralMappingEntry(p.root, "sources")
	if err != nil {
		return nil, err
	}
	if found {
		if sources.value.Kind != yaml.SequenceNode {
			return nil, errors.New("sources is not sequence")
		}
		for i, item := range sources.value.Content {
			if err := p.ctx.Err(); err != nil {
				return nil, err
			}
			if item.Kind != yaml.MappingNode {
				return nil, fmt.Errorf("sources[%d] is not mapping", i)
			}
			prefix := fmt.Sprintf("sources[%d].", i)
			if err := observe(item, "last_modified", prefix+"last_modified"); err != nil {
				return nil, err
			}
			if err := window(item, prefix); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}
