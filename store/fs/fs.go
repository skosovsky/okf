// Package fs implements the durable, single-filesystem store backend.
//
// It deliberately uses an advisory lease: editors which bypass this package can
// observe a multi-file transaction in progress.  On the next Open, an unfinished
// journal is deterministically completed to its recorded post-state.
package fs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/mutation"
	"github.com/skosovsky/okf/store"
	"github.com/skosovsky/okf/validator"
)

const internalDirectory = ".okf"

// maxMetadataRead bounds untrusted durable metadata before decoding it. Journal
// and receipt files are an integrity boundary, not revision-visible content.
const maxMetadataRead = 64 << 20

// maxJournalManifestRead is deliberately smaller than the generic metadata
// ceiling.  A transaction manifest is control-plane data: payload bytes never
// belong in it.  Keeping this bound independent of file size makes the writer
// and recovery reader share one non-amplifying contract.
const maxJournalManifestRead = 16 << 20

// Replay metadata is control-plane input on every idempotent retry. Keep its
// limits independent from bundle content so a corrupt receipt cannot turn a
// retry into an allocation or diagnostic-amplification primitive.
const (
	// DefaultMaxStagedPayloadBytes bounds one durable staged payload (256 MiB,
	// deliberately above the documented >64 MiB asset contract).
	DefaultMaxStagedPayloadBytes int64 = 256 << 20
	// DefaultMaxStagedTransactionBytes bounds aggregate staged payload bytes.
	DefaultMaxStagedTransactionBytes int64 = 1 << 30
	// DefaultMaxStagedFiles is both the public transaction bound and the v5
	// payload-ordinal ceiling: payload-00000 through payload-99999.
	DefaultMaxStagedFiles int    = 100_000
	maxStagedPayloadBytes int64  = 1 << 40
	replayFormatVersion   uint16 = 1
	replayAlgorithm              = "sha256"
	replayDomain                 = "okf:replace-concept-replay:v1"
	maxReplayBytes               = 1 << 20
	maxReplayDiagnostics         = 4096
	maxReplayRefs                = 256
	maxReplayScannedFiles        = 1_000_000
	maxReplayString              = 16 << 10
)

const (
	// MinReceiptRetention is the smallest non-default receipt retention period.
	// A zero Config value selects DefaultConfig instead.
	MinReceiptRetention = time.Nanosecond
	// MaxReceiptRetention bounds receipt retention to one year.
	MaxReceiptRetention = 365 * 24 * time.Hour
	// MinReceiptCount is the minimum number of receipts retained regardless of age.
	MinReceiptCount = 1000
	// MaxReceiptCount bounds metadata growth caused by receipt retention.
	MaxReceiptCount = 1_000_000
)

// Config controls bounded lease acquisition and receipt retention.
type Config struct {
	LeaseTimeout time.Duration
	// HashAlgorithm optionally replaces SHA-256 for snapshot materialization.
	// A ContextHashAlgorithm receives Snapshot/Preview/Commit cancellation.
	HashAlgorithm store.HashAlgorithm
	// ReceiptRetention retains receipts by age. Zero selects the 24-hour
	// default; non-zero values must be in [MinReceiptRetention,
	// MaxReceiptRetention].
	ReceiptRetention time.Duration
	// MinimumReceipts retains this many newest receipts irrespective of age.
	// Zero selects the default; non-zero values must be in
	// [MinReceiptCount, MaxReceiptCount].
	MinimumReceipts int
	// MaxStagedPayloadBytes and MaxStagedTransactionBytes bound durable staged
	// content. Zero selects the production defaults.
	MaxStagedPayloadBytes     int64
	MaxStagedTransactionBytes int64
	// MaxStagedFiles bounds the v5 manifest cardinality. Zero selects 100000;
	// larger values are invalid because payload ordinals are fixed at 5 digits.
	MaxStagedFiles int
	// ValidatorConfig is applied to every staged semantic plan. Nil uses the
	// validator defaults; callers may opt into strict validation.
	ValidatorConfig *validator.ValidatorConfig
	// Fault injects a failure immediately before a durable operation. It is the
	// lower-level error seam; production callers must leave it nil.
	Fault func(Step) error
	// PostFault injects a crash-like failure immediately after a durable
	// operation has succeeded. Unlike Fault it must never be used to model an
	// underlying operation failure. Once the journal directory sync has
	// succeeded, a PostFault may return while recovery owns completion.
	PostFault func(Step) error
	// DirectorySync is a test seam invoked with each directory just before it
	// is synced. Production callers must leave it nil.
	DirectorySync func(string) error
}

// Step identifies a fault-injectable durable filesystem boundary.
type Step string

// durableFaultInventory is the canonical list of transaction-protocol
// boundaries that require deterministic fault coverage. Keep it beside Step
// declarations so a new durable hook cannot hide in an unrelated test.
func durableFaultInventory() []Step {
	return []Step{
		// Generic visible-file boundaries. These remain production boundaries even
		// where metadata paths use a more specific Step below.
		StepMkdir, StepChmod, StepFileWrite, StepFileSync, StepFileClose, StepRename, StepRemove, StepDirectorySync,
		StepJournalWrite, StepJournalFileWrite, StepJournalFileSync, StepJournalFileClose, StepJournalRename, StepJournalDirectorySync,
		StepStageFileWrite, StepStageFileSync, StepStageFileClose, StepStageRename, StepStageDirectorySync,
		StepCapabilityMkdir, StepCapabilityChmod, StepCapabilityFileWrite, StepCapabilityFileSync, StepCapabilityFileClose, StepCapabilityRename, StepCapabilityRemove, StepCapabilityDirectorySync,
		StepReceiptWrite, StepReceiptSync, StepReceiptClose, StepReceiptRename, StepReceiptDirectorySync, StepReceiptPrune,
		StepPrivateMetadataSync, StepPrivateMetadataClose,
		StepPrivateDirectoryChmod, StepPrivateDirectorySync, StepPrivateDirectoryClose, StepPrivateDirectoryParentSync,
		StepStageCleanupPayloadRemove, StepStageCleanupPayloadDirectory, StepStageCleanupPayloadDirRemove, StepStageCleanupStageDirectory, StepStageCleanupStageDirRemove, StepStageCleanupRootDirectory,
		StepTempCleanupRemove, StepTempCleanupDirectorySync,
	}
}

const (
	StepJournalWrite     Step = "journal_write"
	StepJournalFileWrite Step = "journal_file_write"
	StepJournalFileSync  Step = "journal_file_sync"
	StepJournalFileClose Step = "journal_file_close"
	StepJournalRename    Step = "journal_rename"
	// StepJournalDirectorySync is the final publication boundary: the journal
	// pathname is not durable until its containing directory has been synced.
	StepJournalDirectorySync Step = "journal_directory_sync"
	StepStageFileWrite       Step = "stage_file_write"
	StepStageFileSync        Step = "stage_file_sync"
	StepStageFileClose       Step = "stage_file_close"
	StepStageRename          Step = "stage_rename"
	StepStageDirectorySync   Step = "stage_directory_sync"
	// Capability probing is private .okf metadata, never a revision-visible
	// mutation. Keep its fault hooks separate from generic visible-file hooks.
	StepCapabilityMkdir         Step = "capability_mkdir"
	StepCapabilityChmod         Step = "capability_chmod"
	StepCapabilityFileWrite     Step = "capability_file_write"
	StepCapabilityFileSync      Step = "capability_file_sync"
	StepCapabilityFileClose     Step = "capability_file_close"
	StepCapabilityRename        Step = "capability_rename"
	StepCapabilityRemove        Step = "capability_remove"
	StepCapabilityDirectorySync Step = "capability_directory_sync"
	// Stage cleanup has its own fault namespace so crash tests cannot confuse
	// pre-journal staging with post-commit garbage collection.
	StepStageCleanupPayloadRemove    Step = "stage_cleanup_payload_remove"
	StepStageCleanupPayloadDirectory Step = "stage_cleanup_payload_directory_sync"
	StepStageCleanupPayloadDirRemove Step = "stage_cleanup_payload_dir_remove"
	StepStageCleanupStageDirectory   Step = "stage_cleanup_stage_directory_sync"
	StepStageCleanupStageDirRemove   Step = "stage_cleanup_stage_dir_remove"
	StepStageCleanupRootDirectory    Step = "stage_cleanup_root_directory_sync"
	StepTempCleanupRemove            Step = "temp_cleanup_remove"
	StepTempCleanupDirectorySync     Step = "temp_cleanup_directory_sync"
	StepMkdir                        Step = "mkdir"
	StepChmod                        Step = "chmod"
	StepFileWrite                    Step = "file_write"
	StepFileSync                     Step = "file_sync"
	StepFileClose                    Step = "file_close"
	StepRename                       Step = "rename"
	StepRemove                       Step = "remove"
	StepDirectorySync                Step = "directory_sync"
	StepReceiptWrite                 Step = "receipt_write"
	StepReceiptSync                  Step = "receipt_sync"
	StepReceiptClose                 Step = "receipt_close"
	StepReceiptRename                Step = "receipt_rename"
	StepReceiptDirectorySync         Step = "receipt_directory_sync"
	StepReceiptPrune                 Step = "receipt_prune"
	// Private metadata is chmod'ed after its initial atomic write. The second
	// read-only sync/close makes that mode durable and is a distinct operation.
	StepPrivateMetadataSync        Step = "private_metadata_sync"
	StepPrivateMetadataClose       Step = "private_metadata_close"
	StepPrivateDirectoryChmod      Step = "private_directory_chmod"
	StepPrivateDirectorySync       Step = "private_directory_sync"
	StepPrivateDirectoryClose      Step = "private_directory_close"
	StepPrivateDirectoryParentSync Step = "private_directory_parent_sync"
)

// DefaultConfig returns the bounded production defaults. Receipt retention is
// 24 hours, while the most recent 1000 receipts are retained unconditionally.
func DefaultConfig() Config {
	return Config{LeaseTimeout: 30 * time.Second, ReceiptRetention: 24 * time.Hour, MinimumReceipts: MinReceiptCount, MaxStagedPayloadBytes: DefaultMaxStagedPayloadBytes, MaxStagedTransactionBytes: DefaultMaxStagedTransactionBytes, MaxStagedFiles: DefaultMaxStagedFiles}
}

func (c Config) withDefaults() Config {
	d := DefaultConfig()
	if c.LeaseTimeout == 0 {
		c.LeaseTimeout = d.LeaseTimeout
	}
	if c.ReceiptRetention == 0 {
		c.ReceiptRetention = d.ReceiptRetention
	}
	if c.MinimumReceipts == 0 {
		c.MinimumReceipts = d.MinimumReceipts
	}
	if c.MaxStagedPayloadBytes == 0 {
		c.MaxStagedPayloadBytes = d.MaxStagedPayloadBytes
	}
	if c.MaxStagedTransactionBytes == 0 {
		c.MaxStagedTransactionBytes = d.MaxStagedTransactionBytes
	}
	if c.MaxStagedFiles == 0 {
		c.MaxStagedFiles = d.MaxStagedFiles
	}
	return c
}

// Validate rejects unsafe/unbounded configuration.
func (c Config) Validate() error {
	c = c.withDefaults()
	if c.LeaseTimeout <= 0 || c.LeaseTimeout > 24*time.Hour {
		return fmt.Errorf("fs store: lease timeout must be in (0, 24h]")
	}
	if c.ReceiptRetention < MinReceiptRetention || c.ReceiptRetention > MaxReceiptRetention {
		return fmt.Errorf("fs store: receipt retention must be in [%s, %s]", MinReceiptRetention, MaxReceiptRetention)
	}
	if c.MinimumReceipts < MinReceiptCount || c.MinimumReceipts > MaxReceiptCount {
		return fmt.Errorf("fs store: minimum receipts must be in [%d, %d]", MinReceiptCount, MaxReceiptCount)
	}
	if c.MaxStagedPayloadBytes <= 0 || c.MaxStagedPayloadBytes > maxStagedPayloadBytes || c.MaxStagedTransactionBytes < c.MaxStagedPayloadBytes || c.MaxStagedTransactionBytes > maxStagedPayloadBytes {
		return errors.New("fs store: invalid staged payload limits")
	}
	if c.MaxStagedFiles <= 0 || c.MaxStagedFiles > DefaultMaxStagedFiles {
		return errors.New("fs store: invalid staged file limit")
	}
	if c.HashAlgorithm != nil {
		if err := store.ValidateHashAlgorithm(c.HashAlgorithm); err != nil {
			return fmt.Errorf("fs store: %w", err)
		}
	}
	return nil
}

func algorithmName(algorithm store.HashAlgorithm) (name string) {
	if algorithm == nil {
		return "sha256"
	}
	defer func() {
		if recover() != nil {
			name = ""
		}
	}()
	return algorithm.Name()
}

type cachedHashAlgorithm struct {
	algorithm store.HashAlgorithm
	name      string
}

func (a cachedHashAlgorithm) Name() string   { return a.name }
func (a cachedHashAlgorithm) New() hash.Hash { return a.algorithm.New() }

func (a cachedHashAlgorithm) NewContext(ctx context.Context) (hash.Hash, error) {
	if contextual, ok := a.algorithm.(store.ContextHashAlgorithm); ok {
		return contextual.NewContext(ctx)
	}
	return a.algorithm.New(), nil
}

// Store is a durable Store rooted at one bundle directory.
type Store struct {
	// root is retained only for diagnostics and backwards-compatible test
	// helpers. All store I/O is relative to rootFD, which pins the directory
	// selected at Open even if its pathname is subsequently replaced.
	root   string
	rootFD *os.Root
	// dirFD is the descriptor-relative mutation capability.  Never turn a
	// validated relative name back into a pathname for a mutating operation.
	dirFD  *os.File
	config Config
	// hashAlgorithmName is bound once during Open. It is used in durable
	// journal envelopes and never obtained from a caller implementation later.
	hashAlgorithmName string
	// write is the narrow I/O seam used by the durable protocol. It remains
	// unexported so callers cannot weaken atomic publication; tests can model
	// POSIX-legal short writes (n < len(p), nil error).
	write func(*os.File, []byte) (int, error)
	// descriptorBarrier is an internal deterministic test seam. It is not part
	// of Config: durable callers must not be able to alter syscall ordering.
	descriptorBarrier func(string) error
	// provenanceReadHook is a narrow deterministic test seam for the recovery
	// gate. It fires immediately before a known visible regular file is opened.
	provenanceReadHook   func(string)
	caseAliases          bool
	normalizationAliases bool
	mu                   sync.Mutex
	closed               bool
}

// Close releases the pinned root descriptor. Calls after Close return an
// error; it is safe to call Close more than once.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return errors.Join(s.rootFD.Close(), s.dirFD.Close())
}

func (s *Store) usable() error {
	if s.closed {
		return errors.New("fs store: closed")
	}
	return nil
}

// detectPathAliases probes each filesystem equivalence independently under the
// exclusive lease. A volume may fold case without normalizing Unicode (or vice
// versa), so callers must not infer one capability from the other.
func (s *Store) detectPathAliases() error {
	caseAliases, err := s.detectPathAliasProbe("case-probe-a", "CASE-PROBE-A")
	if err != nil {
		return err
	}
	normalizationAliases, err := s.detectPathAliasProbe("norm-probe-\u00e9", "norm-probe-e\u0301")
	if err != nil {
		return err
	}
	s.caseAliases = caseAliases
	s.normalizationAliases = normalizationAliases
	return nil
}

func (s *Store) detectPathAliasProbe(name, alternate string) (aliases bool, err error) {
	probe := path.Join(internalDirectory, "capabilities", name)
	if err := s.writePrivateDurableAt(probe, []byte("okf")); err != nil {
		return false, err
	}
	info, err := s.rootFD.Lstat(path.Join(internalDirectory, "capabilities", alternate))
	if err == nil {
		if !info.Mode().IsRegular() {
			cleanupErr := s.remove(probe)
			return false, errors.Join(errors.New("fs store: invalid capability probe entry"), cleanupErr)
		}
		aliases = true
	} else if !errors.Is(err, os.ErrNotExist) {
		cleanupErr := s.remove(probe)
		return false, errors.Join(err, cleanupErr)
	}
	if err := s.remove(probe); err != nil {
		return false, err
	}
	return aliases, s.syncDirAt(path.Join(internalDirectory, "capabilities"))
}

func (s *Store) validateFilesystemPathAliases(files map[string][]byte) error {
	if !s.caseAliases && !s.normalizationAliases {
		return nil
	}
	seen := make(map[string]string, len(files))
	for p := range files {
		key := s.filesystemPathKey(p)
		if previous, ok := seen[key]; ok && previous != p {
			return fmt.Errorf("%w: filesystem-equivalent result paths %q and %q", store.ErrInvalidChangeSet, previous, p)
		}
		seen[key] = p
	}
	return nil
}

func (s *Store) filesystemPathKey(p string) string {
	if s.normalizationAliases {
		p = norm.NFC.String(p)
	}
	if s.caseAliases {
		p = cases.Fold().String(p)
	}
	if s.normalizationAliases {
		p = norm.NFC.String(p)
	}
	return p
}

// validateFilesystemPathAliasTransition rejects a case/normalization-only
// rename even when the final map itself contains just one spelling. Without
// this base-to-result check, applying a journal for a -> A on a folding volume
// can write A and then remove a (the same directory entry).
func (s *Store) validateFilesystemPathAliasTransition(base, next map[string][]byte) error {
	if err := s.validateFilesystemPathAliases(next); err != nil || (!s.caseAliases && !s.normalizationAliases) {
		return err
	}
	basePaths := make(map[string]string, len(base))
	for p := range base {
		basePaths[s.filesystemPathKey(p)] = p
	}
	for p := range next {
		if previous, ok := basePaths[s.filesystemPathKey(p)]; ok && previous != p {
			return fmt.Errorf("%w: filesystem-equivalent result path %q replaces base path %q", store.ErrInvalidChangeSet, p, previous)
		}
	}
	return nil
}

// validateJournalFilesystemPathAliasTransition is deliberately metadata-only:
// recovery calls it before opening a staged payload, so an aliased forged
// journal cannot turn into either an allocation or a visible write.
func (s *Store) validateJournalFilesystemPathAliasTransition(base []journalBaseFile, next []journalFile) error {
	if !s.caseAliases && !s.normalizationAliases {
		return nil
	}
	basePaths := make(map[string]string, len(base))
	for _, file := range base {
		basePaths[s.filesystemPathKey(file.Path)] = file.Path
	}
	seen := make(map[string]string, len(next))
	for _, file := range next {
		key := s.filesystemPathKey(file.Path)
		if previous, ok := seen[key]; ok && previous != file.Path {
			return fmt.Errorf("%w: filesystem-equivalent result paths %q and %q", store.ErrInvalidChangeSet, previous, file.Path)
		}
		if previous, ok := basePaths[key]; ok && previous != file.Path {
			return fmt.Errorf("%w: filesystem-equivalent result path %q replaces base path %q", store.ErrInvalidChangeSet, file.Path, previous)
		}
		seen[key] = file.Path
	}
	return nil
}

// ReplaceConceptRequest describes one safe document replacement. It is kept
// separate from semantic operations so transport adapters need not invent an
// unsafe second filesystem writer.
type ReplaceConceptRequest struct {
	ChangeSetID  store.ChangeSetID
	Actor        store.Actor
	BaseRevision store.Revision
	ConceptID    bundle.ConceptID
	Document     bundle.Document
}

// ReplaceConceptResult returns the same durable receipt pipeline as Commit.
type ReplaceConceptResult struct {
	Receipt    store.CommitReceipt
	Validation validator.Report
}

// replaceReplay is the durable result projection for ReplaceConcept. Receipts
// are request identities, so replay must not revalidate a later snapshot.
type replaceReplay struct {
	Version    uint16           `json:"version"`
	Algorithm  string           `json:"algorithm"`
	Domain     string           `json:"domain"`
	Binding    string           `json:"binding"`
	Validation validationReplay `json:"validation"`
}

type validationReplay struct {
	Diagnostics  []validationDiagnosticReplay `json:"diagnostics"`
	ScannedFiles int                          `json:"scanned_files"`
}

type validationDiagnosticReplay struct {
	Code         string   `json:"code"`
	File         string   `json:"file"`
	Severity     string   `json:"severity"`
	Message      string   `json:"message"`
	Source       string   `json:"source"`
	RelationType string   `json:"relation_type"`
	RawTarget    string   `json:"raw_target"`
	Refs         []string `json:"refs"`
}

func freezeValidation(report validator.Report) replaceReplay {
	v := validationReplay{ScannedFiles: report.ScannedFiles, Diagnostics: make([]validationDiagnosticReplay, len(report.Diagnostics))}
	for i, d := range report.Diagnostics {
		refs := make([]string, len(d.Refs))
		for j, ref := range d.Refs {
			refs[j] = ref.String()
		}
		v.Diagnostics[i] = validationDiagnosticReplay{Code: d.Code, File: d.File, Severity: d.Severity.String(), Message: d.Message, Source: d.Source.String(), RelationType: d.RelationType, RawTarget: d.RawTarget, Refs: refs}
		sort.Strings(v.Diagnostics[i].Refs)
	}
	sort.Slice(v.Diagnostics, func(i, j int) bool {
		return replayDiagnosticKey(v.Diagnostics[i]) < replayDiagnosticKey(v.Diagnostics[j])
	})
	return replaceReplay{Version: replayFormatVersion, Algorithm: replayAlgorithm, Domain: replayDomain, Validation: v}
}

// sealReplay binds the canonical replay projection to the exact durable
// receipt identity. It deliberately covers receipt bytes, rather than merely
// the request digest, so an attacker cannot transplant diagnostics between two
// receipts for the same request.
func sealReplay(receipt store.CommitReceipt, replay replaceReplay) (replaceReplay, error) {
	if replay.Validation.Diagnostics == nil {
		return replaceReplay{}, nil
	}
	replay.Version, replay.Algorithm, replay.Domain = replayFormatVersion, replayAlgorithm, replayDomain
	replay.Binding = ""
	if err := validateReplay(replay, false); err != nil {
		return replaceReplay{}, err
	}
	canonical, err := json.Marshal(replay)
	if err != nil {
		return replaceReplay{}, err
	}
	receiptBytes, err := json.Marshal(receipt)
	if err != nil {
		return replaceReplay{}, err
	}
	h := sha256.New()
	for _, value := range [][]byte{[]byte(replayDomain), receiptBytes, canonical} {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(value)))
		_, _ = h.Write(n[:])
		_, _ = h.Write(value)
	}
	replay.Binding = "sha256:" + hex.EncodeToString(h.Sum(nil))
	return replay, nil
}

func validateReplay(replay replaceReplay, requireBinding bool) error {
	if replay.Version != replayFormatVersion || replay.Algorithm != replayAlgorithm || replay.Domain != replayDomain {
		return errors.New("unsupported replay schema")
	}
	if requireBinding && !validPayloadDigest(replay.Binding) {
		return errors.New("invalid replay binding")
	}
	if replay.Validation.ScannedFiles < 0 || replay.Validation.ScannedFiles > maxReplayScannedFiles || len(replay.Validation.Diagnostics) > maxReplayDiagnostics {
		return errors.New("invalid replay counts")
	}
	previous := ""
	for _, d := range replay.Validation.Diagnostics {
		if len(d.Refs) > maxReplayRefs || !validReplayDiagnostic(d) {
			return errors.New("invalid replay diagnostic")
		}
		key := replayDiagnosticKey(d)
		if key <= previous {
			return errors.New("replay diagnostics must be canonical, sorted, and unique")
		}
		previous = key
	}
	return nil
}

func validReplayDiagnostic(d validationDiagnosticReplay) bool {
	if d.Severity != "ERROR" && d.Severity != "WARN" && d.Severity != "INFO" {
		return false
	}
	for _, value := range []string{d.Code, d.File, d.Severity, d.Message, d.Source, d.RelationType, d.RawTarget} {
		if !utf8.ValidString(value) || len(value) > maxReplayString {
			return false
		}
	}
	// Validator uses "." as the canonical locator for a bundle-root finding;
	// all other non-empty values are revision-visible relative paths.
	if d.Message == "" || (d.File != "" && d.File != "." && !safePath(d.File)) {
		return false
	}
	if d.Source != "" {
		ref, err := bundle.ParseRelationRef(d.Source)
		if err != nil || ref.String() != d.Source {
			return false
		}
	}
	previous := ""
	for _, raw := range d.Refs {
		if !utf8.ValidString(raw) || len(raw) > maxReplayString {
			return false
		}
		ref, err := bundle.ParseRelationRef(raw)
		if err != nil || ref.String() != raw || raw <= previous {
			return false
		}
		previous = raw
	}
	return true
}

func replayDiagnosticKey(d validationDiagnosticReplay) string {
	// JSON's canonical struct field order makes this unambiguous while avoiding
	// hand-maintained ordering logic that can drift from the durable schema.
	b, _ := json.Marshal(d)
	return string(b)
}

func (r replaceReplay) MarshalJSON() ([]byte, error) {
	if err := validateReplay(r, r.Binding != ""); err != nil {
		return nil, err
	}
	type wire replaceReplay
	return json.Marshal(wire(r))
}

func (r *replaceReplay) UnmarshalJSON(raw []byte) error {
	if len(raw) > maxReplayBytes || !utf8.Valid(raw) {
		return errors.New("invalid replay encoding")
	}
	if err := store.RejectDuplicateJSONKeys(raw); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if len(fields) != 5 {
		return errors.New("replay has an invalid field set")
	}
	for _, name := range []string{"version", "algorithm", "domain", "binding", "validation"} {
		if value, ok := fields[name]; !ok || bytes.Equal(value, []byte("null")) {
			return fmt.Errorf("replay missing %s", name)
		}
	}
	if !jsonCanonicalUint(fields["version"], uint64(replayFormatVersion), true) || !jsonString(fields["algorithm"]) || !jsonString(fields["domain"]) || !jsonString(fields["binding"]) {
		return errors.New("replay scalar has an invalid JSON type or representation")
	}
	if err := validateReplayValidationJSON(fields["validation"]); err != nil {
		return err
	}
	var wire struct {
		Version    uint16           `json:"version"`
		Algorithm  string           `json:"algorithm"`
		Domain     string           `json:"domain"`
		Binding    string           `json:"binding"`
		Validation validationReplay `json:"validation"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return err
	}
	*r = replaceReplay{Version: wire.Version, Algorithm: wire.Algorithm, Domain: wire.Domain, Binding: wire.Binding, Validation: wire.Validation}
	if err := validateReplay(*r, true); err != nil {
		return err
	}
	canonical, err := json.Marshal(*r)
	if err != nil || !bytes.Equal(raw, canonical) {
		return errors.New("replay is not canonical JSON")
	}
	return nil
}

func validateReplayValidationJSON(raw json.RawMessage) error {
	var fields map[string]json.RawMessage
	if !jsonObject(raw) || json.Unmarshal(raw, &fields) != nil || len(fields) != 2 {
		return errors.New("replay validation has an invalid field set")
	}
	for _, name := range []string{"diagnostics", "scanned_files"} {
		if value, ok := fields[name]; !ok || bytes.Equal(value, []byte("null")) {
			return fmt.Errorf("replay validation missing %s", name)
		}
	}
	if !jsonCanonicalUint(fields["scanned_files"], maxReplayScannedFiles, false) {
		return errors.New("replay scanned_files must be a canonical non-negative integer")
	}
	if !jsonArray(fields["diagnostics"]) {
		return errors.New("replay diagnostics must be an array")
	}
	var diagnostics []json.RawMessage
	if err := json.Unmarshal(fields["diagnostics"], &diagnostics); err != nil || len(diagnostics) > maxReplayDiagnostics {
		return errors.New("invalid replay diagnostic count")
	}
	for _, rawDiagnostic := range diagnostics {
		var diagnostic map[string]json.RawMessage
		if !jsonObject(rawDiagnostic) || json.Unmarshal(rawDiagnostic, &diagnostic) != nil || len(diagnostic) != 8 {
			return errors.New("replay diagnostic has an invalid field set")
		}
		for _, name := range []string{"code", "file", "severity", "message", "source", "relation_type", "raw_target", "refs"} {
			if value, ok := diagnostic[name]; !ok || bytes.Equal(value, []byte("null")) {
				return fmt.Errorf("replay diagnostic missing %s", name)
			}
		}
		for _, name := range []string{"code", "file", "severity", "message", "source", "relation_type", "raw_target"} {
			if !jsonString(diagnostic[name]) {
				return fmt.Errorf("replay diagnostic %s must be a string", name)
			}
		}
		if !jsonArray(diagnostic["refs"]) {
			return errors.New("replay diagnostic refs must be an array")
		}
		var refs []json.RawMessage
		if err := json.Unmarshal(diagnostic["refs"], &refs); err != nil || len(refs) > maxReplayRefs {
			return errors.New("invalid replay refs count")
		}
		for _, rawRef := range refs {
			if !jsonString(rawRef) {
				return errors.New("replay diagnostic ref must be a string")
			}
		}
	}
	return nil
}

func jsonString(raw json.RawMessage) bool {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' || !utf8.Valid(raw) {
		return false
	}
	var value string
	return json.Unmarshal(raw, &value) == nil && utf8.ValidString(value)
}

// jsonCanonicalUint rejects JSON's otherwise legal alternate spellings (1.0,
// 1e0 and -0). Durable replay counts have a single wire representation.
func jsonCanonicalUint(raw json.RawMessage, max uint64, exact bool) bool {
	if len(raw) == 0 || raw[0] == '-' || (len(raw) > 1 && raw[0] == '0') {
		return false
	}
	for _, c := range raw {
		if c < '0' || c > '9' {
			return false
		}
	}
	value, err := strconv.ParseUint(string(raw), 10, 64)
	if err != nil || value > max {
		return false
	}
	return !exact || value == max
}

func (r replaceReplay) validation() (validator.Report, error) {
	if err := validateReplay(r, r.Binding != ""); err != nil {
		return validator.Report{}, err
	}
	report := validator.Report{ScannedFiles: r.Validation.ScannedFiles, Diagnostics: make([]validator.Diagnostic, len(r.Validation.Diagnostics))}
	for i, d := range r.Validation.Diagnostics {
		severity := validator.SeverityError
		switch d.Severity {
		case "ERROR":
			severity = validator.SeverityError
		case "WARN":
			severity = validator.SeverityWarning
		case "INFO":
			severity = validator.SeverityInfo
		default:
			return validator.Report{}, errors.New("invalid replay diagnostic severity")
		}
		var err error
		if d.Source != "" {
			report.Diagnostics[i].Source, err = bundle.ParseRelationRef(d.Source)
			if err != nil {
				return validator.Report{}, err
			}
		}
		refs := make([]bundle.RelationRef, len(d.Refs))
		for j, raw := range d.Refs {
			refs[j], err = bundle.ParseRelationRef(raw)
			if err != nil {
				return validator.Report{}, err
			}
		}
		report.Diagnostics[i] = validator.Diagnostic{Code: d.Code, File: d.File, Severity: severity, Message: d.Message, Source: report.Diagnostics[i].Source, RelationType: d.RelationType, RawTarget: d.RawTarget, Refs: refs}
	}
	return report, nil
}

// ReplaceConcept validates and publishes a single document through the same
// lease, CAS, journal, receipt and recovery path used by Commit.
func (s *Store) ReplaceConcept(ctx context.Context, req ReplaceConceptRequest, opts store.CommitOptions) (ReplaceConceptResult, error) {
	if err := ctx.Err(); err != nil {
		return ReplaceConceptResult{}, err
	}
	if err := invalidChangeSet(req.ChangeSetID.Validate()); err != nil {
		return ReplaceConceptResult{}, err
	}
	if err := invalidChangeSet(req.Actor.Validate()); err != nil {
		return ReplaceConceptResult{}, err
	}
	if !req.BaseRevision.Valid() || req.ConceptID.String() == "" {
		return ReplaceConceptResult{}, invalidChangeSet(fmt.Errorf("%w: invalid replace request", store.ErrInvalidChangeSet))
	}
	if err := invalidChangeSet(opts.Validate()); err != nil {
		return ReplaceConceptResult{}, err
	}
	serialized, err := req.Document.Serialize()
	if err != nil {
		return ReplaceConceptResult{}, invalidChangeSet(err)
	}
	digest, err := replaceDigest(req, serialized)
	if err != nil {
		return ReplaceConceptResult{}, err
	}
	// A previous call on this same Store may have crossed the durable journal
	// boundary and returned from an injected crash before writing its receipt.
	// Complete that work before any replay/CAS observation can mistake it for a
	// competing commit.
	if err := s.recoverPending(ctx); err != nil {
		return ReplaceConceptResult{}, err
	}
	// Idempotency is checked before reading or comparing the requested base.
	// A replay is a request identity, not a fresh CAS authorization.
	if opts.IdempotencyKey != "" {
		if r, found, err := s.lookupReceipt(opts.IdempotencyKey, digest); err != nil {
			return ReplaceConceptResult{}, err
		} else if found {
			return s.replaceReplayResult(opts.IdempotencyKey, digest, r)
		}
	}
	base, err := s.Snapshot(ctx)
	if err != nil {
		return ReplaceConceptResult{}, err
	}
	// A concurrent Store may have written the receipt while Snapshot waited for
	// its lease. Replay still wins over a now-stale caller base.
	if opts.IdempotencyKey != "" {
		if r, found, err := s.lookupReceipt(opts.IdempotencyKey, digest); err != nil {
			return ReplaceConceptResult{}, err
		} else if found {
			return s.replaceReplayResult(opts.IdempotencyKey, digest, r)
		}
	}
	if base.Revision() != req.BaseRevision {
		return ReplaceConceptResult{}, &store.Conflict{Expected: req.BaseRevision, Actual: base.Revision(), ChangedRefs: semanticRefs(base.(*snapshot)), Retryable: true}
	}
	targetPath := req.ConceptID.String() + ".md"
	if c, err := base.OpenConcept(req.ConceptID); err == nil {
		targetPath = c.Path
	} else if !errors.Is(err, os.ErrNotExist) {
		return ReplaceConceptResult{}, err
	}
	o, err := mutation.NewOverlayContext(ctx, base)
	if err != nil {
		return ReplaceConceptResult{}, err
	}
	if !safePath(targetPath) || strings.HasPrefix(filepath.Base(targetPath), ".") {
		return ReplaceConceptResult{}, fmt.Errorf("%w: invalid concept path", store.ErrInvalidChangeSet)
	}
	if err := o.PutContext(ctx, targetPath, []byte(serialized)); err != nil {
		return ReplaceConceptResult{}, err
	}
	report, blockedRelations, stagedBundle, err := s.validateStagedSource(ctx, o)
	if err != nil {
		return ReplaceConceptResult{}, err
	}
	if !report.IsConformant() || blockedRelations {
		return ReplaceConceptResult{Validation: report}, &store.InvalidChangeSet{
			Code:        "staged_validation_failed",
			Diagnostics: relationStoreDiagnostics(stagedBundle.RelationDiagnostics()),
		}
	}
	next, err := snapshotFromSource(ctx, o, s.config.HashAlgorithm)
	if err != nil {
		return ReplaceConceptResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usable(); err != nil {
		return ReplaceConceptResult{}, err
	}
	unlock, err := s.acquire(ctx)
	if err != nil {
		return ReplaceConceptResult{}, err
	}
	defer unlock()
	// Another Store may have committed the same request while this caller was
	// planning. Check under the cross-process lease before CAS.
	if opts.IdempotencyKey != "" {
		if r, found, err := s.lookupReceipt(opts.IdempotencyKey, digest); err != nil {
			return ReplaceConceptResult{}, err
		} else if found {
			return s.replaceReplayResult(opts.IdempotencyKey, digest, r)
		}
	}
	current, err := s.snapshotUnlocked(ctx)
	if err != nil {
		return ReplaceConceptResult{}, err
	}
	if current.Revision() != base.Revision() {
		return ReplaceConceptResult{}, &store.Conflict{Expected: base.Revision(), Actual: current.Revision(), ChangedRefs: semanticChangedRefs(base.(*snapshot), current.(*snapshot)), Retryable: true}
	}
	receipt := store.CommitReceipt{FormatVersion: store.CommitReceiptFormatVersion, ChangeSetID: req.ChangeSetID, IdempotencyKey: opts.IdempotencyKey, RequestDigest: digest, BaseRevision: base.Revision(), ResultRevision: next.Revision(), CommitTime: time.Now().UTC(), ChangedRefs: semanticChangedRefs(base.(*snapshot), next), ChangedFiles: diff(base.(*snapshot), next)}
	replay := freezeValidation(report)
	// Return the same canonical projection that will be persisted for a retry.
	report, err = replay.validation()
	if err != nil {
		return ReplaceConceptResult{}, fmt.Errorf("fs store: canonicalize replay: %w", err)
	}
	if err := s.publishWithReplay(ctx, next, receipt, replay); err != nil {
		return ReplaceConceptResult{}, err
	}
	return ReplaceConceptResult{Receipt: receipt.Clone(), Validation: report}, nil
}

func replaceDigest(req ReplaceConceptRequest, text string) (string, error) {
	h := sha256.New()
	// BaseRevision is a CAS precondition, not the identity of the replacement.
	// Retrying the same desired document after refreshing a stale base must find
	// the original receipt instead of becoming an artificial new request.
	for _, v := range []string{"okf:replace-concept:v1", string(req.ChangeSetID), string(req.Actor), req.ConceptID.String(), text} {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(v)))
		if err := writeHash(h, n[:]); err != nil {
			return "", err
		}
		if err := writeHash(h, []byte(v)); err != nil {
			return "", err
		}
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func writeHash(h hash.Hash, data []byte) error {
	n, err := h.Write(data)
	if n != len(data) {
		if err != nil {
			return errors.Join(err, io.ErrShortWrite)
		}
		return io.ErrShortWrite
	}
	return err
}

func (s *Store) fail(step Step) error {
	if s.config.Fault != nil {
		return s.config.Fault(step)
	}
	return nil
}

func (s *Store) postFault(step Step) error {
	if s.config.PostFault != nil {
		return s.config.PostFault(step)
	}
	return nil
}

// Open pins one symlink-free root inode, then completes any interrupted commit.
func Open(root string, config Config) (*Store, error) {
	return OpenContext(context.Background(), root, config)
}

// OpenContext is Open with a cancellation boundary before recovery starts.
func OpenContext(ctx context.Context, root string, config Config) (*Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	config = config.withDefaults()
	configuredAlgorithm := config.HashAlgorithm
	config.HashAlgorithm = nil
	if err := config.Validate(); err != nil {
		return nil, err
	}
	algorithmName := "sha256"
	if configuredAlgorithm != nil {
		var err error
		algorithmName, err = store.ValidateHashAlgorithmName(configuredAlgorithm)
		if err != nil {
			return nil, fmt.Errorf("fs store: %w", err)
		}
		config.HashAlgorithm = cachedHashAlgorithm{algorithm: configuredAlgorithm, name: algorithmName}
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	rootFD, dirFD, err := openRootCapabilities(abs)
	if err != nil {
		return nil, err
	}
	s := &Store{root: abs, rootFD: rootFD, dirFD: dirFD, config: config, hashAlgorithmName: algorithmName, write: func(f *os.File, data []byte) (int, error) { return f.Write(data) }}
	unlock, err := s.acquire(ctx)
	if err != nil {
		return nil, errors.Join(err, rootFD.Close(), dirFD.Close())
	}
	defer unlock()
	for _, dir := range []string{
		internalDirectory,
		path.Join(internalDirectory, "transactions"),
		path.Join(internalDirectory, "staging"),
		path.Join(internalDirectory, "receipts"),
		path.Join(internalDirectory, "capabilities"),
	} {
		if err := s.mkdirAll(dir, 0o700); err != nil {
			return nil, errors.Join(err, rootFD.Close(), dirFD.Close())
		}
	}
	if err := s.detectPathAliases(); err != nil {
		return nil, errors.Join(err, rootFD.Close(), dirFD.Close())
	}
	if err := s.recoverContext(ctx); err != nil {
		return nil, errors.Join(err, rootFD.Close(), dirFD.Close())
	}
	return s, nil
}

// Snapshot captures every revision-visible regular file, rather than retaining
// a live filesystem view.  Returned data is always copied.
func (s *Store) Snapshot(ctx context.Context) (store.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usable(); err != nil {
		return nil, err
	}
	// A durable journal is a promise of the post-state.  Recovery must run
	// before every observation, including a second call on this Store after a
	// PostFault.  Take the exclusive lease directly: attempting recovery after
	// acquireRead would self-deadlock while upgrading the flock.
	unlock, err := s.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := s.recoverContext(ctx); err != nil {
		return nil, err
	}
	return s.snapshotUnlocked(ctx)
}

func (s *Store) snapshotUnlocked(ctx context.Context) (store.Snapshot, error) {
	files, err := readVisibleRoot(ctx, s.rootFD)
	if err != nil {
		return nil, err
	}
	return newSnapshotWithAlgorithm(ctx, files, s.config.HashAlgorithm)
}

func (s *Store) Preview(ctx context.Context, change store.ChangeSet) (store.Preview, error) {
	if err := validatePublicChange(change); err != nil {
		return store.Preview{}, err
	}
	snap, err := s.Snapshot(ctx)
	if err != nil {
		return store.Preview{}, err
	}
	result, err := mutation.NewPlanner(s.config.ValidatorConfig).Plan(ctx, snap, change)
	if err != nil {
		// A staged validation failure has a diagnostic-only preview. Preserve it
		// at the public boundary while retaining the typed error; it contains no
		// publishable writes, deletes, renames, plan, or staged source.
		var invalid *store.InvalidChangeSet
		if errors.As(err, &invalid) && len(result.Preview.Diagnostics) != 0 {
			return result.Preview.Clone(), err
		}
		return store.Preview{}, err
	}
	return result.Preview.Clone(), nil
}

// Commit has a durable cancellation boundary: cancellation before the journal
// is synced aborts without publication. Once the journal is durable, Commit
// always finishes (or leaves recoverable work) but returns ctx.Err and no
// receipt to this caller; an idempotent retry discovers the persisted receipt.
func (s *Store) Commit(ctx context.Context, change store.ChangeSet, options store.CommitOptions) (store.CommitReceipt, error) {
	if err := ctx.Err(); err != nil {
		return store.CommitReceipt{}, err
	}
	if err := validatePublicChange(change); err != nil {
		return store.CommitReceipt{}, err
	}
	if err := invalidChangeSet(options.Validate()); err != nil {
		return store.CommitReceipt{}, err
	}
	digest, err := change.RequestDigest()
	if err != nil {
		return store.CommitReceipt{}, err
	}
	if err := s.recoverPending(ctx); err != nil {
		return store.CommitReceipt{}, err
	}
	if options.IdempotencyKey != "" {
		if r, found, err := s.lookupReceipt(options.IdempotencyKey, digest); err != nil || found {
			return r, err
		}
	}
	// Planning is intentionally outside the lease. The fresh revision check below
	// is the sole authorization to publish this plan.
	base, err := s.Snapshot(ctx)
	if err != nil {
		return store.CommitReceipt{}, err
	}
	if change.BaseRevision != base.Revision() {
		return store.CommitReceipt{}, &store.Conflict{Expected: change.BaseRevision, Actual: base.Revision(), ChangedRefs: semanticRefs(base.(*snapshot)), Retryable: true}
	}
	planned, err := mutation.NewPlanner(s.config.ValidatorConfig).Plan(ctx, base, change)
	if err != nil {
		return store.CommitReceipt{}, err
	}
	if err := ctx.Err(); err != nil {
		return store.CommitReceipt{}, err
	}
	// Keep cooperating snapshots from observing a partial multi-file publish.
	// The advisory filesystem lease provides the same boundary across Store
	// instances and processes.
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usable(); err != nil {
		return store.CommitReceipt{}, err
	}
	unlock, err := s.acquire(ctx)
	if err != nil {
		return store.CommitReceipt{}, err
	}
	defer unlock()
	if options.IdempotencyKey != "" {
		if r, found, err := s.lookupReceipt(options.IdempotencyKey, digest); err != nil || found {
			return r, err
		}
	}
	current, err := s.snapshotUnlocked(ctx)
	if err != nil {
		return store.CommitReceipt{}, err
	}
	if current.Revision() != base.Revision() {
		return store.CommitReceipt{}, &store.Conflict{Expected: base.Revision(), Actual: current.Revision(), ChangedRefs: semanticChangedRefs(base.(*snapshot), current.(*snapshot)), Retryable: true}
	}
	staged, err := snapshotFromSource(ctx, planned.Staged, s.config.HashAlgorithm)
	if err != nil {
		return store.CommitReceipt{}, err
	}
	changes := diff(base.(*snapshot), staged)
	receipt := store.CommitReceipt{FormatVersion: store.CommitReceiptFormatVersion, ChangeSetID: change.ID, IdempotencyKey: options.IdempotencyKey, RequestDigest: digest, BaseRevision: base.Revision(), ResultRevision: staged.Revision(), CommitTime: time.Now().UTC(), ChangedRefs: semanticChangedRefs(base.(*snapshot), staged), ChangedFiles: changes}
	if err := s.publish(ctx, staged, receipt); err != nil {
		return store.CommitReceipt{}, err
	}
	return receipt.Clone(), nil
}

// recoverPending serializes recovery with normal mutations. It deliberately
// runs before replay lookup and snapshot/CAS reads: after a durable journal,
// the only correct observable state is its completed post-state and receipt.
func (s *Store) recoverPending(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usable(); err != nil {
		return err
	}
	unlock, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	return s.recoverContext(ctx)
}

// validatePublicChange is deliberately before RequestDigest: malformed public
// input must always have the same typed error shape and cannot reach hashing.
func validatePublicChange(change store.ChangeSet) error { return invalidChangeSet(change.Validate()) }

func invalidChangeSet(err error) error {
	if err == nil {
		return nil
	}
	var typed *store.InvalidChangeSet
	if errors.As(err, &typed) {
		return err
	}
	return &store.InvalidChangeSet{Code: "invalid_change_set", Cause: err}
}

// semanticChangedRefs returns a deterministic, conservative description of
// semantic refs whose meaning may have changed between two captured snapshots.
// It is the single source for both conflict and receipt payloads. A caller must
// never derive a receipt from Preview: Preview describes planned dependencies,
// while this comparison describes the semantic state that actually changed.
// Changing a concept document changes its root and every extant fragment;
// changing a relation changes both its source and its old/new targets.
func semanticChangedRefs(base, current *snapshot) []bundle.RelationRef {
	if base == nil || current == nil {
		return nil
	}
	seen := make(map[string]bundle.RelationRef)
	add := func(ref bundle.RelationRef) {
		if ref.String() != "" {
			seen[ref.String()] = ref
		}
	}
	addConcept := func(s *snapshot, id bundle.ConceptID) {
		if s == nil || !s.concepts.Contains(id) {
			return
		}
		add(bundle.RelationRef{ID: id})
		for _, fragment := range s.concepts.Subresources(id) {
			add(bundle.RelationRef{ID: id, Fragment: fragment})
		}
	}

	ids := make(map[string]bundle.ConceptID)
	for _, s := range []*snapshot{base, current} {
		for _, concept := range s.concepts.Concepts() {
			ids[concept.ID.String()] = concept.ID
		}
	}
	for _, id := range ids {
		baseConcept, baseOK := base.concepts.Get(id)
		currentConcept, currentOK := current.concepts.Get(id)
		if !baseOK || !currentOK || !bytes.Equal(base.files[baseConcept.Path], current.files[currentConcept.Path]) {
			addConcept(base, id)
			addConcept(current, id)
		}
	}

	type relationKey struct{ source, typ, target string }
	relations := func(s *snapshot) map[relationKey]bundle.Relation {
		out := make(map[relationKey]bundle.Relation)
		for _, concept := range s.concepts.Concepts() {
			for _, relation := range s.concepts.SemanticLinksFrom(concept.ID) {
				out[relationKey{relation.Source.String(), relation.Type, relation.Target.String()}] = relation
			}
		}
		return out
	}
	baseRelations, currentRelations := relations(base), relations(current)
	for key, relation := range baseRelations {
		if _, ok := currentRelations[key]; !ok {
			add(relation.Source)
			add(relation.Target)
		}
	}
	for key, relation := range currentRelations {
		if _, ok := baseRelations[key]; !ok {
			add(relation.Source)
			add(relation.Target)
		}
	}

	out := make([]bundle.RelationRef, 0, len(seen))
	for _, ref := range seen {
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// semanticRefs is the conservative conflict payload when only the actual
// snapshot is available. Every extant semantic ref is reported rather than
// guessing which one changed from a revision hash alone.
func semanticRefs(s *snapshot) []bundle.RelationRef {
	if s == nil || s.concepts == nil {
		return nil
	}
	seen := make(map[string]bundle.RelationRef)
	add := func(ref bundle.RelationRef) {
		if ref.String() != "" {
			seen[ref.String()] = ref
		}
	}
	for _, concept := range s.concepts.Concepts() {
		add(bundle.RelationRef{ID: concept.ID})
		for _, fragment := range s.concepts.Subresources(concept.ID) {
			add(bundle.RelationRef{ID: concept.ID, Fragment: fragment})
		}
		for _, relation := range s.concepts.SemanticLinksFrom(concept.ID) {
			add(relation.Source)
			add(relation.Target)
		}
	}
	out := make([]bundle.RelationRef, 0, len(seen))
	for _, ref := range seen {
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

type snapshot struct {
	files    map[string][]byte
	revision store.Revision
	manifest store.Manifest
	concepts *bundle.Bundle
}

func newSnapshot(ctx context.Context, files map[string][]byte) (*snapshot, error) {
	return newSnapshotWithAlgorithm(ctx, files, nil)
}
func newSnapshotWithAlgorithm(ctx context.Context, files map[string][]byte, algorithm store.HashAlgorithm) (*snapshot, error) {
	owned, err := cloneFilesContext(ctx, files)
	if err != nil {
		return nil, err
	}
	return newOwnedSnapshotWithAlgorithm(ctx, owned, algorithm)
}
func newOwnedSnapshotWithAlgorithm(ctx context.Context, files map[string][]byte, algorithm store.HashAlgorithm) (*snapshot, error) {
	entries := make([]store.ManifestEntry, 0, len(files))
	for p, b := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entries = append(entries, store.ManifestEntry{Path: p, Content: b})
	}
	manifest, err := store.NewManifestContext(ctx, entries, algorithm)
	if err != nil {
		return nil, err
	}
	r, err := manifest.RevisionContext(ctx)
	if err != nil {
		return nil, err
	}
	s := &snapshot{files: files, revision: r, manifest: manifest}
	b, err := bundle.Load(ctx, s)
	if err != nil {
		return nil, err
	}
	s.concepts = b
	return s, nil
}
func snapshotFromSource(ctx context.Context, source bundle.Source, algorithm store.HashAlgorithm) (*snapshot, error) {
	files, err := readSource(ctx, source)
	if err != nil {
		return nil, err
	}
	if cached, ok := source.(*mutation.Overlay); ok {
		manifest, manifestErr := cached.ManifestContext(ctx)
		if manifestErr == nil && manifest.AlgorithmName() == algorithmName(algorithm) && manifest.Len() == len(files) {
			pathsMatch := true
			for p := range files {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if _, exists := manifest.Digest(p); !exists {
					pathsMatch = false
					break
				}
			}
			if pathsMatch {
				r, revisionErr := manifest.RevisionContext(ctx)
				if revisionErr == nil {
					s := &snapshot{files: files, revision: r, manifest: manifest}
					b, loadErr := bundle.Load(ctx, s)
					if loadErr != nil {
						return nil, loadErr
					}
					s.concepts = b
					return s, nil
				}
			}
		}
	}
	return newOwnedSnapshotWithAlgorithm(ctx, files, algorithm)
}
func (s *snapshot) Revision() store.Revision { return s.revision }
func (s *snapshot) Manifest() store.Manifest { return s.manifest.Clone() }
func (s *snapshot) ManifestContext(ctx context.Context) (store.Manifest, error) {
	return s.manifest.CloneContext(ctx)
}
func (s *snapshot) Paths(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(s.files))
	for p := range s.files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	sort.Strings(out)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
func (s *snapshot) ReadFile(ctx context.Context, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !safePath(name) {
		return nil, fmt.Errorf("fs store: invalid path %q", name)
	}
	b, ok := s.files[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	out := make([]byte, len(b))
	const chunk = 64 << 10
	for at := 0; at < len(b); at += chunk {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := at + chunk
		if end > len(b) {
			end = len(b)
		}
		copy(out[at:end], b[at:end])
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
func (s *snapshot) OpenConcept(id bundle.ConceptID) (bundle.Concept, error) {
	c, ok := s.concepts.Get(id)
	if !ok {
		return bundle.Concept{}, os.ErrNotExist
	}
	// Document contains YAML nodes, so copying the struct is not sufficient.
	// Reparse the snapshot-owned bytes to keep callers from mutating it.
	data := s.files[c.Path]
	doc, err := bundle.ParseDocument(string(data))
	if err != nil {
		return bundle.Concept{}, err
	}
	c.Document = doc
	return c, nil
}
func (s *snapshot) ListConcepts() ([]bundle.ConceptID, error) {
	cs := s.concepts.Concepts()
	out := make([]bundle.ConceptID, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.ID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out, nil
}

func readVisibleRoot(ctx context.Context, root *os.Root) (map[string][]byte, error) {
	out := map[string][]byte{}
	if err := walkVisibleRoot(ctx, root, ".", "", out); err != nil {
		return nil, err
	}
	return out, nil
}

func walkVisibleRoot(ctx context.Context, root *os.Root, directory, prefix string, out map[string][]byte) (err error) {
	dir, err := openRootReadNoFollow(root, directory, true)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		rel := path.Join(prefix, entry.Name())
		if rel == internalDirectory || strings.HasPrefix(rel, internalDirectory+"/") {
			continue
		}
		if !safePath(rel) {
			return fmt.Errorf("fs store: invalid revision-visible path %q", rel)
		}
		name := path.Join(directory, entry.Name())
		info, err := root.Lstat(name)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("fs store: symlink revision-visible path %s", rel)
		}
		if info.IsDir() {
			if err := walkVisibleRoot(ctx, root, name, rel, out); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		b, err := readPinnedRegular(ctx, root, rel, info)
		if err != nil {
			return err
		}
		out[rel] = append([]byte(nil), b...)
	}
	return nil
}

// readPinnedRegular makes the final bytes come from the inode that was
// inspected.  Lstat alone is vulnerable to a rename/symlink swap between the
// check and ReadFile; an opened descriptor plus SameFile closes that window.
// Each ancestor is similarly checked so a swap cannot redirect hashing to a
// different in-root subtree either.
func readPinnedRegular(ctx context.Context, root *os.Root, name string, expected os.FileInfo) ([]byte, error) {
	if err := rejectSymlinkPath(root, name); err != nil {
		return nil, err
	}
	parts := strings.Split(name, "/")
	for i := 1; i < len(parts); i++ {
		ancestor := strings.Join(parts[:i], "/")
		before, err := root.Lstat(ancestor)
		if err != nil {
			return nil, err
		}
		if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("fs store: unsafe ancestor %s", ancestor)
		}
		f, err := openRootReadNoFollow(root, ancestor, true)
		if err != nil {
			return nil, err
		}
		after, statErr := f.Stat()
		closeErr := f.Close()
		if statErr != nil || closeErr != nil {
			return nil, errors.Join(statErr, closeErr)
		}
		if !os.SameFile(before, after) || !after.IsDir() {
			return nil, fmt.Errorf("fs store: ancestor changed during snapshot: %s", ancestor)
		}
	}
	f, err := openRootReadNoFollow(root, name, false)
	if err != nil {
		return nil, err
	}
	actual, statErr := f.Stat()
	if statErr == nil && (!actual.Mode().IsRegular() || !os.SameFile(expected, actual)) {
		statErr = fmt.Errorf("fs store: path changed during snapshot: %s", name)
	}
	var data []byte
	if statErr == nil {
		data, statErr = readAllContext(ctx, f)
	}
	closeErr := f.Close()
	if statErr != nil || closeErr != nil {
		return nil, errors.Join(statErr, closeErr)
	}
	return data, nil
}

// validateCurrentJournalProvenance walks the visible namespace without
// materializing files. It is recovery's control-plane gate: only paths named
// by the durable base/result manifests may be opened, and those are hashed in
// a fixed buffer after their size has already been proven admissible.
func (s *Store) validateCurrentJournalProvenance(ctx context.Context, base []journalBaseFile, result []journalFile) error {
	baseByPath := make(map[string]journalBaseFile, len(base))
	resultByPath := make(map[string]journalFile, len(result))
	for _, entry := range base {
		baseByPath[entry.Path] = entry
	}
	for _, entry := range result {
		resultByPath[entry.Path] = entry
	}
	seen := make(map[string]bool, len(baseByPath)+len(resultByPath))
	if err := s.walkCurrentJournalProvenance(ctx, ".", "", baseByPath, resultByPath, seen); err != nil {
		return err
	}
	for name := range baseByPath {
		if !seen[name] {
			if _, alsoResult := resultByPath[name]; alsoResult {
				return errors.New("journal base/result state mismatch")
			}
		}
	}
	for name := range resultByPath {
		if !seen[name] {
			if _, alsoBase := baseByPath[name]; alsoBase {
				return errors.New("journal base/result state mismatch")
			}
		}
	}
	return nil
}

func (s *Store) walkCurrentJournalProvenance(ctx context.Context, directory, prefix string, base map[string]journalBaseFile, result map[string]journalFile, seen map[string]bool) (err error) {
	dir, err := openRootReadNoFollow(s.rootFD, directory, true)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		rel := path.Join(prefix, entry.Name())
		if rel == internalDirectory || strings.HasPrefix(rel, internalDirectory+"/") {
			continue
		}
		if !safePath(rel) {
			return fmt.Errorf("invalid revision-visible path %q", rel)
		}
		name := path.Join(directory, entry.Name())
		info, err := s.rootFD.Lstat(name)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink revision-visible path %s", rel)
		}
		if info.IsDir() {
			if err := s.walkCurrentJournalProvenance(ctx, name, rel, base, result, seen); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		baseEntry, hasBase := base[rel]
		resultEntry, hasResult := result[rel]
		if !hasBase && !hasResult {
			return errors.New("journal base/result state mismatch")
		}
		if (!hasBase || info.Size() != baseEntry.Size) && (!hasResult || info.Size() != resultEntry.Size) {
			return errors.New("journal base/result state mismatch")
		}
		if s.provenanceReadHook != nil {
			s.provenanceReadHook(rel)
		}
		digest, err := streamPinnedRegularDigest(ctx, s.rootFD, rel, info)
		if err != nil {
			return err
		}
		if (!hasBase || digest != baseEntry.Digest) && (!hasResult || digest != resultEntry.Digest) {
			return errors.New("journal base/result state mismatch")
		}
		seen[rel] = true
	}
	return nil
}

func streamPinnedRegularDigest(ctx context.Context, root *os.Root, name string, expected os.FileInfo) (string, error) {
	if err := rejectSymlinkPath(root, name); err != nil {
		return "", err
	}
	f, err := openRootReadNoFollow(root, name, false)
	if err != nil {
		return "", err
	}
	actual, statErr := f.Stat()
	if statErr == nil && (!actual.Mode().IsRegular() || !os.SameFile(expected, actual)) {
		statErr = fmt.Errorf("path changed during provenance scan: %s", name)
	}
	if statErr != nil {
		return "", errors.Join(statErr, f.Close())
	}
	h := sha256.New()
	buf := make([]byte, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return "", errors.Join(err, f.Close())
		}
		n, readErr := f.Read(buf)
		if n > 0 {
			if _, err := h.Write(buf[:n]); err != nil {
				return "", errors.Join(err, f.Close())
			}
		}
		if readErr == io.EOF {
			return "sha256:" + hex.EncodeToString(h.Sum(nil)), f.Close()
		}
		if readErr != nil {
			return "", errors.Join(readErr, f.Close())
		}
	}
}

func readAllContext(ctx context.Context, r io.Reader) ([]byte, error) {
	var out bytes.Buffer
	buf := make([]byte, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := r.Read(buf)
		if n > 0 {
			_, _ = out.Write(buf[:n])
		}
		if err == io.EOF {
			return out.Bytes(), nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// appendBlockingRelationDiagnostics exposes semantic rejection through the
// existing validator report transport. Relation diagnostics are already
// deterministically ordered by bundle; keeping that order after validator
// diagnostics gives MCP callers a stable rejected response in its existing
// diagnostics list. Informational findings, notably anchor_alias, never reject a
// staged mutation.
func appendBlockingRelationDiagnostics(report validator.Report, diagnostics []bundle.RelationDiagnostic) (validator.Report, bool) {
	blocked := false
	for _, diagnostic := range diagnostics {
		if !diagnostic.BlocksMutation() {
			continue
		}
		blocked = true
		source := bundle.RelationRef{ID: diagnostic.Source, Fragment: diagnostic.SourceFragment}
		report.Diagnostics = append(report.Diagnostics, validator.Diagnostic{
			Code:         diagnostic.Code,
			File:         diagnostic.File,
			Severity:     validator.SeverityError,
			Message:      diagnostic.Message,
			Source:       source,
			RelationType: diagnostic.RelationType,
			RawTarget:    diagnostic.RawTarget,
			Refs:         []bundle.RelationRef{source},
		})
	}
	return report, blocked
}

// relationStoreDiagnostics keeps rejected semantic findings contract-equivalent
// to Planner and Preview projections.
func relationStoreDiagnostics(diagnostics []bundle.RelationDiagnostic) []store.Diagnostic {
	projected := store.ProjectRelationDiagnostics(diagnostics)
	out := projected[:0]
	for _, diagnostic := range projected {
		if diagnostic.Severity == store.DiagnosticError {
			out = append(out, diagnostic)
		}
	}
	return out
}

// validateStagedSource is the one arbitration point for revision-visible
// post-states. It loads source once, then validates that exact bundle and
// projects its relation diagnostics. ValidatorConfig determines base
// conformance; independently, blocking semantic relation diagnostics veto
// publication. Recovery uses this same gate before it can touch a visible
// path. The returned bundle is the same immutable bundle used for validation.
func (s *Store) validateStagedSource(ctx context.Context, source bundle.Source) (validator.Report, bool, *bundle.Bundle, error) {
	if err := ctx.Err(); err != nil {
		return validator.Report{}, false, nil, err
	}
	b, err := bundle.Load(ctx, source)
	if err != nil {
		return validator.Report{}, false, nil, err
	}
	report, err := validator.ValidateBundleContext(ctx, b, s.config.ValidatorConfig)
	if err != nil {
		return validator.Report{}, false, nil, err
	}
	if err := ctx.Err(); err != nil {
		return validator.Report{}, false, nil, err
	}
	report, blocked := appendBlockingRelationDiagnostics(report, b.RelationDiagnostics())
	return report, blocked, b, nil
}

// readMetadata opens metadata through the inode inspected by Lstat. os.Root
// confines traversal to the opened root, while the identity checks reject
// symlinks and directory swaps even when they still resolve inside that root.
func readMetadata(ctx context.Context, root *os.Root, name string) ([]byte, error) {
	return readMetadataLimit(ctx, root, name, maxMetadataRead)
}

func readMetadataLimit(ctx context.Context, root *os.Root, name string, limit int64) ([]byte, error) {
	f, err := openPinnedMetadata(root, name, false)
	if err != nil {
		return nil, err
	}
	data, readErr := readAllContextLimit(ctx, f, limit)
	closeErr := f.Close()
	if readErr != nil || closeErr != nil {
		return nil, metadataCorrupt(errors.Join(readErr, closeErr))
	}
	return data, nil
}

func openPinnedMetadataDir(root *os.Root, name string) (*os.File, error) {
	return openPinnedMetadata(root, name, true)
}

func openPinnedMetadata(root *os.Root, name string, wantDir bool) (*os.File, error) {
	clean := path.Clean(name)
	if clean == "." || !strings.HasPrefix(clean, internalDirectory+"/") {
		return nil, metadataCorrupt(fmt.Errorf("unsafe metadata path %q", name))
	}
	parts := strings.Split(clean, "/")
	for i := 1; i <= len(parts); i++ {
		component := strings.Join(parts[:i], "/")
		before, err := root.Lstat(component)
		if err != nil {
			if i == len(parts) && errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
			return nil, metadataCorrupt(err)
		}
		if before.Mode()&os.ModeSymlink != 0 || (i < len(parts) && !before.IsDir()) {
			return nil, metadataCorrupt(fmt.Errorf("unsafe metadata component %s", component))
		}
		f, err := openRootReadNoFollow(root, component, i < len(parts))
		if err != nil {
			return nil, metadataCorrupt(err)
		}
		after, statErr := f.Stat()
		if i != len(parts) {
			closeErr := f.Close()
			if statErr != nil || closeErr != nil {
				return nil, metadataCorrupt(errors.Join(statErr, closeErr))
			}
			if !after.IsDir() || !os.SameFile(before, after) {
				return nil, metadataCorrupt(fmt.Errorf("metadata ancestor changed: %s", component))
			}
			continue
		}
		if statErr != nil {
			return nil, metadataCorrupt(errors.Join(statErr, f.Close()))
		}
		if !os.SameFile(before, after) || (wantDir && !after.IsDir()) || (!wantDir && !after.Mode().IsRegular()) {
			return nil, metadataCorrupt(errors.Join(fmt.Errorf("metadata path changed: %s", component), f.Close()))
		}
		return f, nil
	}
	return nil, metadataCorrupt(fmt.Errorf("empty metadata path"))
}

func readAllContextLimit(ctx context.Context, r io.Reader, limit int64) ([]byte, error) {
	data, err := readAllContext(ctx, io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("metadata exceeds %d byte limit", limit)
	}
	return data, nil
}

func metadataCorrupt(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("fs store: metadata: %w", errors.Join(store.ErrStorageCorrupt, err))
}

// rejectSymlinkPath applies the store's stricter no-follow policy before a
// Root operation. Root itself prevents escape races; this additionally makes
// every symlink in store-managed paths invalid rather than merely contained.
func rejectSymlinkPath(root *os.Root, name string) error {
	if name == "." || name == "" {
		return nil
	}
	for _, part := range strings.Split(path.Clean(name), "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("fs store: unsafe path %q", name)
		}
	}
	parts := strings.Split(path.Clean(name), "/")
	for i := 1; i <= len(parts); i++ {
		p := strings.Join(parts[:i], "/")
		info, err := root.Lstat(p)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("fs store: symlink path component %s", p)
		}
	}
	return nil
}
func readSource(ctx context.Context, source bundle.Source) (map[string][]byte, error) {
	ps, err := source.Paths(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, p := range ps {
		if !safePath(p) {
			return nil, fmt.Errorf("fs store: invalid source path %q", p)
		}
		b, err := source.ReadFile(ctx, p)
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Source transfers ownership of every returned slice to this snapshot.
		out[p] = b
	}
	return out, nil
}
func cloneFilesContext(ctx context.Context, in map[string][]byte) (map[string][]byte, error) {
	out := make(map[string][]byte, len(in))
	for p, b := range in {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		owned := make([]byte, len(b))
		const chunk = 64 << 10
		for at := 0; at < len(b); at += chunk {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			end := at + chunk
			if end > len(b) {
				end = len(b)
			}
			copy(owned[at:end], b[at:end])
		}
		out[p] = owned
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
func safePath(p string) bool {
	return bundle.ValidateRevisionPath(p) == nil
}

func diff(base, next *snapshot) []store.FileChange {
	var out []store.FileChange
	created := make(map[string][]byte)
	removed := make(map[string][]byte)
	for p, b := range next.files {
		if a, ok := base.files[p]; !ok || !bytes.Equal(a, b) {
			if !ok {
				created[p] = b
			} else {
				out = append(out, store.FileChange{Kind: store.FileWrite, Path: p})
			}
		}
	}
	for p := range base.files {
		if _, ok := next.files[p]; !ok {
			removed[p] = base.files[p]
		}
	}
	// Surface pure moves in receipts. The durable protocol still writes the
	// post-state and deletes obsolete paths, which is safer to recover than a
	// sequence of platform-specific rename assumptions.
	createdPaths := make([]string, 0, len(created))
	for p := range created {
		createdPaths = append(createdPaths, p)
	}
	sort.Strings(createdPaths)
	for _, to := range createdPaths {
		content := created[to]
		from := ""
		removedPaths := make([]string, 0, len(removed))
		for candidate := range removed {
			removedPaths = append(removedPaths, candidate)
		}
		sort.Strings(removedPaths)
		for _, candidate := range removedPaths {
			old := removed[candidate]
			if bytes.Equal(content, old) && (from == "" || candidate < from) {
				from = candidate
			}
		}
		if from == "" {
			out = append(out, store.FileChange{Kind: store.FileWrite, Path: to})
			continue
		}
		out = append(out, store.FileChange{Kind: store.FileRename, From: from, Path: to})
		delete(removed, from)
	}
	for p := range removed {
		out = append(out, store.FileChange{Kind: store.FileDelete, Path: p})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path == out[j].Path {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Path < out[j].Path
	})
	return out
}

type journal struct {
	Version       uint16              `json:"version"`
	HashAlgorithm string              `json:"hash_algorithm"`
	Stage         string              `json:"stage"`
	Base          []journalBaseFile   `json:"base"`
	BaseBinding   string              `json:"base_binding"`
	Files         []journalFile       `json:"files"`
	Receipt       store.CommitReceipt `json:"receipt"`
	Replay        *replaceReplay      `json:"replay,omitempty"`
}

// journalBaseFile is the canonical durable provenance of one visible base
// pathname. It lets recovery distinguish a crash during apply from an editor
// changing the bundle behind the store's advisory lease.
type journalBaseFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

// journalFile binds one revision-visible pathname to an already durable,
// private staged payload. Payload is relative to Stage and is never a caller
// supplied pathname in normal publication.
type journalFile struct {
	Path    string `json:"path"`
	Payload string `json:"payload"`
	Size    int64  `json:"size"`
	Digest  string `json:"digest"`
}

// decodeJournal accepts only the current complete on-disk transaction format.
// It validates all data needed to decide that applying its post-state is safe,
// before recovery touches a revision-visible file.
func decodeJournal(raw []byte) (journal, error) {
	return decodeJournalWithAlgorithmName(raw, nil, "sha256")
}

func decodeJournalWithAlgorithm(raw []byte, algorithm store.HashAlgorithm) (journal, error) {
	name := algorithmName(algorithm)
	return decodeJournalWithAlgorithmName(raw, algorithm, name)
}

func decodeJournalWithAlgorithmName(raw []byte, algorithm store.HashAlgorithm, expectedAlgorithm string) (journal, error) {
	if len(raw) > maxJournalManifestRead {
		return journal{}, fmt.Errorf("journal manifest exceeds %d byte limit", maxJournalManifestRead)
	}
	// encoding/json replaces malformed UTF-8 while decoding strings. Reject it
	// first so recovery can never publish a replacement-character filename.
	if !utf8.Valid(raw) {
		return journal{}, errors.New("journal contains invalid UTF-8")
	}
	if err := store.RejectDuplicateJSONKeys(raw); err != nil {
		return journal{}, err
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return journal{}, err
	}
	if len(envelope) != 7 && len(envelope) != 8 {
		return journal{}, errors.New("journal has an invalid field set")
	}
	for _, field := range []string{"version", "hash_algorithm", "stage", "base", "base_binding", "files", "receipt"} {
		value, ok := envelope[field]
		if !ok || bytes.Equal(value, []byte("null")) {
			return journal{}, fmt.Errorf("journal %s is required", field)
		}
	}
	if replay, ok := envelope["replay"]; ok && !jsonObject(replay) {
		return journal{}, errors.New("journal replay must be an object")
	}
	if !jsonArray(envelope["base"]) || !jsonArray(envelope["files"]) || !jsonObject(envelope["receipt"]) {
		return journal{}, errors.New("journal base/files must be arrays and receipt must be an object")
	}
	var j journal
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&j); err != nil {
		return journal{}, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return journal{}, errors.New("trailing journal data")
	}
	// Re-decode the raw receipt through the public strict contract before
	// recovery can publish anything.
	receipt, err := decodeCommitReceipt(envelope["receipt"])
	if err != nil {
		return journal{}, err
	}
	j.Receipt = receipt
	if j.Replay != nil {
		sealed, err := sealReplay(j.Receipt, *j.Replay)
		if err != nil || sealed.Binding != j.Replay.Binding {
			return journal{}, errors.New("journal replay binding does not match receipt")
		}
	}
	if j.Version != 5 {
		return journal{}, fmt.Errorf("unsupported journal version %d", j.Version)
	}
	if j.HashAlgorithm == "" || !j.Receipt.ResultRevision.Valid() || !strings.HasPrefix(string(j.Receipt.ResultRevision), j.HashAlgorithm+":") {
		return journal{}, errors.New("journal hash algorithm does not match result revision")
	}
	if expectedAlgorithm != j.HashAlgorithm {
		return journal{}, fmt.Errorf("journal hash algorithm %q is unavailable in this store", j.HashAlgorithm)
	}
	if err := validateJournalReceipt(j.Receipt); err != nil {
		return journal{}, err
	}
	wantStage, err := journalStage(j.Receipt.RequestDigest)
	if err != nil || j.Stage != wantStage || !safeStageDirectory(j.Stage) {
		return journal{}, fmt.Errorf("unsafe journal stage %q", j.Stage)
	}
	previous := ""
	if len(j.Base) > maxReplayScannedFiles {
		return journal{}, errors.New("journal base manifest exceeds entry limit")
	}
	for _, file := range j.Base {
		if !safePath(file.Path) || file.Path <= previous || file.Size < 0 || !validPayloadDigest(file.Digest) {
			return journal{}, errors.New("invalid journal base manifest")
		}
		previous = file.Path
	}
	if binding, err := journalBaseBinding(j.Receipt, j.Base); err != nil || binding != j.BaseBinding {
		return journal{}, errors.New("journal base manifest binding does not match receipt")
	}
	previous = ""
	if len(j.Files) > DefaultMaxStagedFiles {
		return journal{}, errors.New("journal file manifest exceeds entry limit")
	}
	payloads := make(map[string]struct{}, len(j.Files))
	var aggregate int64
	for i, file := range j.Files {
		if !safePath(file.Path) || file.Path <= previous || file.Payload != payloadName(i) || file.Size < 0 || file.Size > DefaultMaxStagedPayloadBytes || !validPayloadDigest(file.Digest) || aggregate > DefaultMaxStagedTransactionBytes-file.Size {
			return journal{}, errors.New("invalid journal file manifest")
		}
		if _, exists := payloads[file.Payload]; exists {
			return journal{}, errors.New("duplicate journal payload")
		}
		aggregate += file.Size
		previous, payloads[file.Payload] = file.Path, struct{}{}
	}
	canonical, err := json.Marshal(j)
	if err != nil || !bytes.Equal(raw, canonical) {
		return journal{}, errors.New("journal is not canonical JSON")
	}
	return j, nil
}

func jsonObject(raw json.RawMessage) bool {
	var object map[string]json.RawMessage
	return json.Unmarshal(raw, &object) == nil && object != nil
}
func jsonArray(raw json.RawMessage) bool {
	var a []json.RawMessage
	return json.Unmarshal(raw, &a) == nil && a != nil
}

func safeStageDirectory(p string) bool {
	parts := strings.Split(p, "/")
	return len(parts) == 4 && parts[0] == internalDirectory && parts[1] == "staging" && validStageID(parts[2]) && parts[3] == "payload"
}
func validStageID(v string) bool {
	return len(v) == len("txn-")+64 && strings.HasPrefix(v, "txn-") && allHex(v[len("txn-"):])
}
func safePayloadName(v string) bool {
	if len(v) != len("payload-00000") || !strings.HasPrefix(v, "payload-") || !allDecimal(v[len("payload-"):]) {
		return false
	}
	i, err := strconv.Atoi(v[len("payload-"):])
	return err == nil && i >= 0 && i < DefaultMaxStagedFiles
}

func payloadName(index int) string {
	if index < 0 || index >= DefaultMaxStagedFiles {
		return ""
	}
	return fmt.Sprintf("payload-%05d", index)
}
func validPayloadDigest(v string) bool {
	return len(v) == len("sha256:")+64 && strings.HasPrefix(v, "sha256:") && allHex(v[len("sha256:"):])
}
func allHex(v string) bool {
	for _, c := range v {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func allDecimal(v string) bool {
	for _, c := range v {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// transactionID is an unambiguous, filesystem-safe derivation from the whole
// canonical request digest. Do not turn punctuation into path characters: a
// future digest algorithm must not be able to collide with another encoding.
func transactionID(requestDigest string) (string, error) {
	if len(requestDigest) != len("sha256:")+64 || !strings.HasPrefix(requestDigest, "sha256:") || !allHex(requestDigest[len("sha256:"):]) {
		return "", errors.New("invalid transaction request digest")
	}
	sum := sha256.Sum256([]byte("okf:transaction:v1\x00" + requestDigest))
	return "txn-" + hex.EncodeToString(sum[:]), nil
}

func journalStage(requestDigest string) (string, error) {
	id, err := transactionID(requestDigest)
	if err != nil {
		return "", err
	}
	return path.Join(internalDirectory, "staging", id, "payload"), nil
}

func journalPath(requestDigest string) (string, error) {
	id, err := transactionID(requestDigest)
	if err != nil {
		return "", err
	}
	return path.Join(internalDirectory, "transactions", id+".json"), nil
}

func validateJournalReceipt(r store.CommitReceipt) error {
	return store.ValidateCommitReceipt(r)
}

func (s *Store) publish(ctx context.Context, next *snapshot, receipt store.CommitReceipt) error {
	return s.publishWithReplay(ctx, next, receipt, replaceReplay{})
}

func (s *Store) publishWithReplay(ctx context.Context, next *snapshot, receipt store.CommitReceipt, replay replaceReplay) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	journaled := false
	defer func() {
		if journaled && ctx.Err() != nil {
			err = ctx.Err()
		}
	}()
	replay, err = sealReplay(receipt, replay)
	if err != nil {
		return fmt.Errorf("fs store: seal replay: %w", errors.Join(store.ErrStorageCorrupt, err))
	}
	j, err := s.stageJournal(next, receipt, replay)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(j)
	if err != nil {
		return err
	}
	if len(raw) > maxJournalManifestRead {
		return fmt.Errorf("fs store: journal manifest exceeds %d byte limit", maxJournalManifestRead)
	}
	jp, err := journalPath(receipt.RequestDigest)
	if err != nil {
		return err
	}
	// A pathname that happened to be renamed is not a published journal. Its
	// data and directory entry must both be durable before post-state apply.
	if err := s.writeJournal(ctx, jp, raw); err != nil {
		return err
	}
	journaled = true
	// The journal is the point of no return. Do not consult ctx until all
	// durable post-state work is finished; recovery follows the same path.
	if _, err := s.apply(context.Background(), j); err != nil {
		return err
	}
	if receipt.IdempotencyKey != "" {
		if err := s.writeReceiptWithReplay(receipt, replay); err != nil {
			return err
		}
		if err := s.pruneReceipts(); err != nil {
			return err
		}
	}
	if err := s.remove(jp); err != nil {
		return err
	}
	if err := s.syncDirAt(path.Dir(jp)); err != nil {
		return err
	}
	if err := s.cleanupStage(j); err != nil {
		return err
	}
	return ctx.Err()
}

// apply advances a journal only from its recorded base state. A matching
// result state is an interrupted post-state publication and is intentionally
// not rewritten; recovery may then finish only private receipt/journal cleanup.
func (s *Store) apply(ctx context.Context, j journal) (bool, error) {
	if !j.Receipt.ResultRevision.IsZero() && !j.Receipt.ResultRevision.Valid() {
		return false, fmt.Errorf("fs store: invalid journal result revision")
	}
	if j.Version != 5 || j.HashAlgorithm != s.hashAlgorithmName || !strings.HasPrefix(string(j.Receipt.ResultRevision), s.hashAlgorithmName+":") {
		return false, fmt.Errorf("fs store: journal hash algorithm does not match configured algorithm")
	}
	if err := s.validateJournalFilesystemPathAliasTransition(j.Base, j.Files); err != nil {
		return false, metadataCorrupt(err)
	}
	// Prove the visible namespace is a base/result provenance state before any
	// staged payload is opened. This deliberately streams known regular files
	// and rejects extras/size mismatches before open, so editor drift cannot
	// allocate attacker-sized current bytes or mask a bad staged payload.
	if err := s.validateCurrentJournalProvenance(ctx, j.Base, j.Files); err != nil {
		return false, metadataCorrupt(err)
	}
	next, err := s.readStagedJournal(j)
	if err != nil {
		return false, err
	}
	staged, err := newSnapshotWithAlgorithm(ctx, next, s.config.HashAlgorithm)
	if err != nil {
		return false, err
	}
	report, blocked, _, err := s.validateStagedSource(ctx, staged)
	if err != nil {
		return false, err
	}
	if !report.IsConformant() || blocked {
		return false, errors.New("fs store: journal staged post-state fails validation")
	}
	if j.Receipt.ResultRevision.Valid() {
		actual, err := newSnapshotWithAlgorithm(context.Background(), next, s.config.HashAlgorithm)
		if err != nil {
			return false, err
		}
		if actual.Revision() != j.Receipt.ResultRevision {
			return false, fmt.Errorf("fs store: journal content does not match receipt revision")
		}
	}
	// Materialization is intentionally after the metadata/streaming gate and
	// staged payload verification. It is needed only for the actual idempotent
	// apply comparison, never to decide whether staged bytes may be opened.
	current, err := readVisibleRoot(ctx, s.rootFD)
	if err != nil {
		return false, err
	}
	currentSnapshot, err := newSnapshotWithAlgorithm(ctx, current, s.config.HashAlgorithm)
	if err != nil {
		return false, err
	}
	baseExact := currentSnapshot.Revision() == j.Receipt.BaseRevision && sameBaseManifest(current, j.Base)
	resultExact := currentSnapshot.Revision() == j.Receipt.ResultRevision && sameVisibleFiles(current, next)
	if resultExact {
		return false, nil
	}
	if !baseExact {
		if !currentMatchesJournalStates(current, j.Base, next) {
			return false, fmt.Errorf("fs store: journal base/result state mismatch: current=%s base=%s result=%s", currentSnapshot.Revision(), j.Receipt.BaseRevision, j.Receipt.ResultRevision)
		}
		// A crash can occur after any individual visible write, rename, removal,
		// or directory sync. Every path has durable base/result provenance, so
		// this mixed state is safe to finish idempotently.
	}
	paths := make([]string, 0, len(next))
	for p := range next {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		b := next[p]
		if old, ok := current[p]; !ok || string(old) != string(b) {
			if err := s.writeFile(p, b); err != nil {
				return false, err
			}
		}
		delete(current, p)
	}
	obsolete := make([]string, 0, len(current))
	for p := range current {
		obsolete = append(obsolete, p)
	}
	sort.Strings(obsolete)
	for _, p := range obsolete {
		if err := s.remove(p); err != nil {
			return false, err
		}
		if err := s.syncDirAt(path.Dir(p)); err != nil {
			return false, err
		}
	}
	verifiedFiles, err := readVisibleRoot(ctx, s.rootFD)
	if err != nil {
		return false, err
	}
	if !sameVisibleFiles(verifiedFiles, next) {
		return false, errors.New("fs store: post-apply visible state does not match journal result")
	}
	verified, err := newSnapshotWithAlgorithm(ctx, verifiedFiles, s.config.HashAlgorithm)
	if err != nil || verified.Revision() != j.Receipt.ResultRevision {
		return false, errors.New("fs store: post-apply revision does not match journal receipt")
	}
	return true, nil
}

func sameBaseManifest(files map[string][]byte, base []journalBaseFile) bool {
	if len(files) != len(base) {
		return false
	}
	for _, entry := range base {
		data, ok := files[entry.Path]
		if !ok || int64(len(data)) != entry.Size {
			return false
		}
		sum := sha256.Sum256(data)
		if entry.Digest != "sha256:"+hex.EncodeToString(sum[:]) {
			return false
		}
	}
	return true
}

func currentMatchesJournalStates(current map[string][]byte, base []journalBaseFile, result map[string][]byte) bool {
	baseByPath := make(map[string]journalBaseFile, len(base))
	for _, entry := range base {
		baseByPath[entry.Path] = entry
	}
	paths := make(map[string]struct{}, len(base)+len(result))
	for path := range baseByPath {
		paths[path] = struct{}{}
	}
	for path := range result {
		paths[path] = struct{}{}
	}
	if len(current) > len(paths) {
		return false
	}
	for path := range current {
		if _, ok := paths[path]; !ok {
			return false
		}
	}
	for path := range paths {
		data, exists := current[path]
		baseEntry, baseExists := baseByPath[path]
		resultData, resultExists := result[path]
		if !exists {
			if baseExists && resultExists {
				return false
			}
			continue
		}
		matchesBase := baseExists && int64(len(data)) == baseEntry.Size && baseEntry.Digest == sha256Digest(data)
		matchesResult := resultExists && bytes.Equal(data, resultData)
		if !matchesBase && !matchesResult {
			return false
		}
	}
	return true
}

func currentMatchesJournalMetadata(current map[string][]byte, base []journalBaseFile, result []journalFile) bool {
	baseByPath := make(map[string]journalBaseFile, len(base))
	resultByPath := make(map[string]journalFile, len(result))
	paths := make(map[string]struct{}, len(base)+len(result))
	for _, entry := range base {
		baseByPath[entry.Path] = entry
		paths[entry.Path] = struct{}{}
	}
	for _, entry := range result {
		resultByPath[entry.Path] = entry
		paths[entry.Path] = struct{}{}
	}
	if len(current) > len(paths) {
		return false
	}
	for p := range current {
		if _, ok := paths[p]; !ok {
			return false
		}
	}
	for p := range paths {
		data, exists := current[p]
		b, hasBase := baseByPath[p]
		r, hasResult := resultByPath[p]
		if !exists {
			if hasBase && hasResult {
				return false
			}
			continue
		}
		matchesBase := hasBase && int64(len(data)) == b.Size && sha256Digest(data) == b.Digest
		matchesResult := hasResult && int64(len(data)) == r.Size && sha256Digest(data) == r.Digest
		if !matchesBase && !matchesResult {
			return false
		}
	}
	return true
}

func sha256Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func sameVisibleFiles(left, right map[string][]byte) bool {
	if len(left) != len(right) {
		return false
	}
	for path, want := range right {
		got, ok := left[path]
		if !ok || !bytes.Equal(got, want) {
			return false
		}
	}
	return true
}

func (s *Store) stageJournal(next *snapshot, receipt store.CommitReceipt, replay replaceReplay) (journal, error) {
	stage, err := journalStage(receipt.RequestDigest)
	if err != nil {
		return journal{}, err
	}
	baseFiles, err := readVisibleRoot(context.Background(), s.rootFD)
	if err != nil {
		return journal{}, err
	}
	base, err := newSnapshotWithAlgorithm(context.Background(), baseFiles, s.config.HashAlgorithm)
	if err != nil {
		return journal{}, err
	}
	if base.Revision() != receipt.BaseRevision {
		return journal{}, errors.New("fs store: visible base does not match receipt")
	}
	if err := s.validateFilesystemPathAliasTransition(base.files, next.files); err != nil {
		return journal{}, err
	}
	j := journal{Version: 5, HashAlgorithm: s.hashAlgorithmName, Stage: stage, Receipt: receipt}
	for _, p := range sortedFilePaths(base.files) {
		data := base.files[p]
		sum := sha256.Sum256(data)
		j.Base = append(j.Base, journalBaseFile{Path: p, Size: int64(len(data)), Digest: "sha256:" + hex.EncodeToString(sum[:])})
	}
	j.BaseBinding, err = journalBaseBinding(receipt, j.Base)
	if err != nil {
		return journal{}, err
	}
	if replay.Validation.Diagnostics != nil {
		j.Replay = &replay
	}
	paths := sortedFilePaths(next.files)
	if len(paths) > s.config.MaxStagedFiles {
		return journal{}, fmt.Errorf("%w: staged file limit exceeded", store.ErrInvalidChangeSet)
	}
	var aggregate int64
	for _, p := range paths {
		size := int64(len(next.files[p]))
		if size > s.config.MaxStagedPayloadBytes || aggregate > s.config.MaxStagedTransactionBytes-size {
			return journal{}, fmt.Errorf("%w: staged payload limit exceeded", store.ErrInvalidChangeSet)
		}
		aggregate += size
	}
	for i, p := range paths {
		if !safePath(p) {
			return journal{}, fmt.Errorf("fs store: invalid journal path %q", p)
		}
		payload := payloadName(i)
		data := next.files[p]
		sum := sha256.Sum256(data)
		j.Files = append(j.Files, journalFile{Path: p, Payload: payload, Size: int64(len(data)), Digest: "sha256:" + hex.EncodeToString(sum[:])})
		if err := s.writePrivateDurableAt(path.Join(stage, payload), data); err != nil {
			return journal{}, err
		}
	}
	return j, nil
}

func sortedFilePaths(files map[string][]byte) []string {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

func journalBaseBinding(receipt store.CommitReceipt, base []journalBaseFile) (string, error) {
	canonical, err := json.Marshal(base)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, value := range [][]byte{[]byte("okf:journal-base:v1"), []byte(receipt.RequestDigest), []byte(receipt.BaseRevision), []byte(receipt.ResultRevision), canonical} {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(value)))
		if _, err := h.Write(n[:]); err != nil {
			return "", err
		}
		if _, err := h.Write(value); err != nil {
			return "", err
		}
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func (s *Store) readStagedJournal(j journal) (map[string][]byte, error) {
	if len(j.Files) > s.config.MaxStagedFiles {
		return nil, metadataCorrupt(errors.New("staged file count exceeds configured limit"))
	}
	next := make(map[string][]byte, len(j.Files))
	var aggregate int64
	for _, entry := range j.Files {
		if !safePath(entry.Path) || !safeStageDirectory(j.Stage) || !safePayloadName(entry.Payload) {
			return nil, metadataCorrupt(errors.New("unsafe staged payload reference"))
		}
		if entry.Size < 0 || entry.Size > s.config.MaxStagedPayloadBytes || aggregate > s.config.MaxStagedTransactionBytes-entry.Size {
			return nil, metadataCorrupt(errors.New("staged payload exceeds configured limit"))
		}
		aggregate += entry.Size
		// Payloads are revision-visible bytes, not metadata. Their declared size
		// is checked against the opened regular file, while the journal itself
		// remains bounded above. This is what permits legitimate large assets.
		data, err := readMetadataLimit(context.Background(), s.rootFD, path.Join(j.Stage, entry.Payload), entry.Size)
		if err != nil {
			return nil, metadataCorrupt(err)
		}
		if int64(len(data)) != entry.Size {
			return nil, metadataCorrupt(errors.New("staged payload size mismatch"))
		}
		sum := sha256.Sum256(data)
		if entry.Digest != "sha256:"+hex.EncodeToString(sum[:]) {
			return nil, metadataCorrupt(errors.New("staged payload digest mismatch"))
		}
		next[entry.Path] = data
	}
	if err := s.validateFilesystemPathAliases(next); err != nil {
		return nil, metadataCorrupt(err)
	}
	actual, err := newSnapshotWithAlgorithm(context.Background(), next, s.config.HashAlgorithm)
	if err != nil {
		return nil, err
	}
	if actual.Revision() != j.Receipt.ResultRevision {
		return nil, metadataCorrupt(errors.New("journal staged content does not match receipt revision"))
	}
	return next, nil
}

// cleanupStage is only called before a journal is published or after that
// journal has been durably removed. It therefore can never discard recovery's
// sole copy of post-state. Every name comes from the validated manifest.
func (s *Store) cleanupStage(j journal) error {
	if !safeStageDirectory(j.Stage) {
		return metadataCorrupt(errors.New("unsafe stage cleanup path"))
	}
	for _, entry := range j.Files {
		if !safePayloadName(entry.Payload) {
			return metadataCorrupt(errors.New("unsafe stage payload cleanup path"))
		}
		if err := s.cleanupRemoveFile(path.Join(j.Stage, entry.Payload)); err != nil {
			return err
		}
		if err := s.cleanupSyncDir(j.Stage, StepStageCleanupPayloadDirectory); err != nil {
			return err
		}
	}
	if err := s.cleanupRemoveDir(j.Stage, StepStageCleanupPayloadDirRemove); err != nil {
		return err
	}
	parent := path.Dir(j.Stage)
	if err := s.cleanupSyncDir(parent, StepStageCleanupStageDirectory); err != nil {
		return err
	}
	if err := s.cleanupRemoveDir(parent, StepStageCleanupStageDirRemove); err != nil {
		return err
	}
	if err := s.cleanupSyncDir(path.Dir(parent), StepStageCleanupRootDirectory); err != nil {
		return err
	}
	return nil
}

func (s *Store) cleanupRemoveFile(name string) error {
	if err := s.fail(StepStageCleanupPayloadRemove); err != nil {
		return err
	}
	err := s.fdRemove(name)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return s.postFault(StepStageCleanupPayloadRemove)
}

func (s *Store) cleanupRemoveDir(name string, step Step) error {
	if err := s.fail(step); err != nil {
		return err
	}
	err := s.fdRemoveDir(name)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return s.postFault(step)
}

func (s *Store) cleanupSyncDir(name string, step Step) error {
	if err := s.fail(step); err != nil {
		return err
	}
	if s.config.DirectorySync != nil {
		if err := s.config.DirectorySync(name); err != nil {
			return err
		}
	}
	if err := s.fdSyncDir(name); err != nil {
		return err
	}
	return s.postFault(step)
}

func (s *Store) writeJournal(ctx context.Context, target string, data []byte) error {
	if err := s.fail(StepJournalWrite); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.writePrivateDurableAt(target, data); err != nil {
		return err
	}
	return s.postFault(StepJournalWrite)
}
func (s *Store) writeFile(target string, data []byte) error {
	return s.writeDurableAt(target, data, 0o644)
}
func (s *Store) remove(target string) error {
	step := s.durableStep(target, StepRemove)
	if err := s.fail(step); err != nil {
		return err
	}
	removeErr := s.fdRemove(target)
	if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return removeErr
	}
	// A retry which sees ENOENT cannot prove that a previous process did not
	// unlink this entry just before crashing.  Sync the parent on both paths.
	postRemoveErr := s.postFault(step)
	syncErr := s.syncDirAt(path.Dir(target))
	return errors.Join(postRemoveErr, syncErr)
}
func (s *Store) syncDirAt(dir string) error {
	step := StepDirectorySync
	if dir == path.Join(internalDirectory, "transactions") {
		step = StepJournalDirectorySync
	} else if dir == path.Join(internalDirectory, "staging") || strings.HasPrefix(dir, path.Join(internalDirectory, "staging")+"/") {
		step = StepStageDirectorySync
	} else if dir == path.Join(internalDirectory, "receipts") {
		step = StepReceiptDirectorySync
	} else if dir == path.Join(internalDirectory, "capabilities") {
		step = StepCapabilityDirectorySync
	}
	if err := s.fail(step); err != nil {
		return err
	}
	if s.config.DirectorySync != nil {
		if err := s.config.DirectorySync(dir); err != nil {
			return err
		}
	}
	if err := s.fdSyncDir(dir); err != nil {
		return err
	}
	return s.postFault(step)
}

func (s *Store) writePrivateDurableAt(target string, data []byte) error {
	if err := s.mkdirAll(path.Dir(target), 0o700); err != nil {
		return err
	}
	if err := s.writeDurableAt(target, data, 0o600); err != nil {
		return err
	}
	// Mode is metadata: persist it before declaring the file durable.
	if err := s.fail(s.durableStep(target, StepChmod)); err != nil {
		return err
	}
	if err := s.fdChmod(target, 0o600); err != nil {
		return err
	}
	if err := s.postFault(s.durableStep(target, StepChmod)); err != nil {
		return err
	}
	f, err := s.fdOpen(target, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	if err := s.fail(StepPrivateMetadataSync); err != nil {
		_ = f.Close()
		return err
	}
	err = f.Sync()
	if err == nil {
		err = s.postFault(StepPrivateMetadataSync)
	}
	if err == nil {
		err = s.fail(StepPrivateMetadataClose)
	}
	closeErr := f.Close()
	if err == nil && closeErr == nil {
		err = s.postFault(StepPrivateMetadataClose)
	}
	return errors.Join(err, closeErr)
}

func (s *Store) enforcePrivateFileMode(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("fs store: private metadata is not a regular file")
	}
	if info.Mode().Perm() == 0o600 {
		return nil
	}
	if err := s.fail(StepPrivateMetadataSync); err != nil {
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	return s.postFault(StepPrivateMetadataSync)
}

func (s *Store) mkdirAll(dir string, mode os.FileMode) error {
	if dir == "." || dir == "" {
		return nil
	}
	step := s.durableStep(dir, StepMkdir)
	if err := s.fail(step); err != nil {
		return err
	}
	created, err := s.fdMkdirAll(dir, mode)
	if err != nil {
		return err
	}
	if err := s.postFault(step); err != nil {
		return err
	}
	if strings.HasPrefix(dir, internalDirectory) && (dir == internalDirectory || strings.HasPrefix(dir, internalDirectory+"/")) {
		if err := s.ensurePrivateDirectories(dir); err != nil {
			return err
		}
	}
	for i := len(created) - 1; i >= 0; i-- {
		sync := s.syncDirAt
		if strings.HasPrefix(dir, path.Join(internalDirectory, "staging")+"/") {
			sync = s.syncStageDirAt
		}
		if err := sync(created[i]); err != nil {
			return err
		}
		if err := sync(path.Dir(created[i])); err != nil {
			return err
		}
		// A newly-created private directory has no chmod repair to trigger the
		// private metadata boundary. Its parent namespace still needs the same
		// explicitly faultable durability acknowledgement.
		if created[i] == internalDirectory || strings.HasPrefix(created[i], internalDirectory+"/") {
			if err := s.syncPrivateDirectoryParent(created[i]); err != nil {
				return err
			}
		}
	}
	return nil
}

// ensurePrivateDirectories enforces the .okf namespace as owner-only before
// it is used. Each component is opened descriptor-relatively with no-follow;
// a symlink, device, or regular file therefore fails closed.
func (s *Store) ensurePrivateDirectories(dir string) error {
	parts := strings.Split(path.Clean(dir), "/")
	for i := range parts {
		name := strings.Join(parts[:i+1], "/")
		info, err := s.rootFD.Lstat(name)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("fs store: unsafe private metadata directory %q", name)
		}
		if info.Mode().Perm() == 0o700 {
			continue
		}
		f, err := openRootReadNoFollow(s.rootFD, name, true)
		if err != nil {
			return err
		}
		if err := s.fail(StepPrivateDirectoryChmod); err != nil {
			_ = f.Close()
			return err
		}
		err = f.Chmod(0o700)
		if err == nil {
			err = s.postFault(StepPrivateDirectoryChmod)
		}
		if err == nil {
			err = s.fail(StepPrivateDirectorySync)
		}
		if err == nil {
			err = f.Sync()
		}
		if err == nil {
			err = s.postFault(StepPrivateDirectorySync)
		}
		if err == nil {
			err = s.fail(StepPrivateDirectoryClose)
		}
		closeErr := f.Close()
		if err == nil && closeErr == nil {
			err = s.postFault(StepPrivateDirectoryClose)
		}
		if err != nil || closeErr != nil {
			return errors.Join(err, closeErr)
		}
		// chmod changes this directory's metadata; persist the containing
		// namespace as well (root for .okf).
		if err := s.syncPrivateDirectoryParent(name); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) syncPrivateDirectoryParent(name string) error {
	if err := s.fail(StepPrivateDirectoryParentSync); err != nil {
		return err
	}
	parent := path.Dir(name)
	if s.config.DirectorySync != nil {
		if err := s.config.DirectorySync(parent); err != nil {
			return err
		}
	}
	if err := s.fdSyncDir(parent); err != nil {
		return err
	}
	return s.postFault(StepPrivateDirectoryParentSync)
}

func (s *Store) syncStageDirAt(dir string) error {
	if err := s.fail(StepStageDirectorySync); err != nil {
		return err
	}
	if s.config.DirectorySync != nil {
		if err := s.config.DirectorySync(dir); err != nil {
			return err
		}
	}
	if err := s.fdSyncDir(dir); err != nil {
		return err
	}
	return s.postFault(StepStageDirectorySync)
}

func (s *Store) writeDurableAt(target string, data []byte, fallbackMode os.FileMode) (err error) {
	if !safePath(target) && !strings.HasPrefix(target, internalDirectory+"/") {
		return fmt.Errorf("fs store: unsafe path %q", target)
	}
	dir := path.Dir(target)
	if err := s.mkdirAll(dir, 0o755); err != nil {
		return err
	}
	mode := fallbackMode
	if info, err := s.fdLstat(target); err == nil {
		mode = info.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var tmp *os.File
	var tmpName string
	for i := 0; i < 100; i++ {
		tmpName = path.Join(dir, fmt.Sprintf(".okf-tmp-%d-%d", time.Now().UnixNano(), i))
		var err error
		tmp, err = s.fdOpen(tmpName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return err
		}
		break
	}
	if tmp == nil {
		return errors.New("fs store: unable to allocate temporary file")
	}
	renamed := false
	defer func() {
		if renamed {
			return
		}
		if tmp != nil {
			err = errors.Join(err, tmp.Close())
		}
		if cleanupErr := s.cleanupTemp(tmpName, dir); cleanupErr != nil {
			err = errors.Join(err, cleanupErr)
			// Fault seams commonly model a one-shot crash boundary. A best-effort
			// second cleanup keeps an aborted writer from leaving scratch visible
			// to a later Open; the original boundary error is still returned.
			_ = s.cleanupTemp(tmpName, dir)
		}
	}()
	if err := s.fail(s.durableStep(target, StepChmod)); err != nil {
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		return err
	}
	if err := s.postFault(s.durableStep(target, StepChmod)); err != nil {
		return err
	}
	if err := s.fail(s.durableStep(target, StepFileWrite)); err != nil {
		return err
	}
	n, err := s.write(tmp, data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	if err := s.postFault(s.durableStep(target, StepFileWrite)); err != nil {
		return err
	}
	if err := s.fail(s.durableStep(target, StepFileSync)); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := s.postFault(s.durableStep(target, StepFileSync)); err != nil {
		return err
	}
	if err := s.fail(s.durableStep(target, StepFileClose)); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	tmp = nil
	if err := s.postFault(s.durableStep(target, StepFileClose)); err != nil {
		return err
	}
	if err := s.fail(s.durableStep(target, StepRename)); err != nil {
		return err
	}
	if err := s.fdRename(tmpName, target); err != nil {
		return err
	}
	if err := s.postFault(s.durableStep(target, StepRename)); err != nil {
		return err
	}
	// Once rename completed and its immediate fault boundary passed, there is
	// no temporary entry left to clean up. The following directory sync makes
	// the rename durable; an ENOENT cleanup would only duplicate that sync.
	renamed = true
	return s.syncDirAt(dir)
}

// cleanupTemp makes aborted atomic-write scratch files crash-durable. It is a
// separate boundary from visible-file removal: a hook here must never be
// mistaken for a post-state mutation fault.
func (s *Store) cleanupTemp(name, dir string) error {
	if err := s.fail(StepTempCleanupRemove); err != nil {
		return err
	}
	removeErr := s.fdRemove(name)
	if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return removeErr
	}
	// An unlink has changed the parent directory even when a crash hook fires
	// immediately afterwards.  Do not let an ENOENT retry shortcut skip that
	// durability boundary: the prior unlink may have succeeded before a crash.
	postRemoveErr := s.postFault(StepTempCleanupRemove)
	if err := s.fail(StepTempCleanupDirectorySync); err != nil {
		return errors.Join(postRemoveErr, err)
	}
	if s.config.DirectorySync != nil {
		if err := s.config.DirectorySync(dir); err != nil {
			return errors.Join(postRemoveErr, err)
		}
	}
	if err := s.fdSyncDir(dir); err != nil {
		return errors.Join(postRemoveErr, err)
	}
	return errors.Join(postRemoveErr, s.postFault(StepTempCleanupDirectorySync))
}

func (s *Store) durableStep(target string, step Step) Step {
	switch {
	case target == path.Join(internalDirectory, "capabilities") || strings.HasPrefix(target, path.Join(internalDirectory, "capabilities")+"/"):
		switch step {
		case StepMkdir:
			return StepCapabilityMkdir
		case StepChmod:
			return StepCapabilityChmod
		case StepFileWrite:
			return StepCapabilityFileWrite
		case StepFileSync:
			return StepCapabilityFileSync
		case StepFileClose:
			return StepCapabilityFileClose
		case StepRename:
			return StepCapabilityRename
		case StepRemove:
			return StepCapabilityRemove
		default:
			return step
		}
	case strings.HasPrefix(target, internalDirectory+"/transactions/"):
		switch step {
		case StepFileWrite:
			return StepJournalFileWrite
		case StepFileSync:
			return StepJournalFileSync
		case StepFileClose:
			return StepJournalFileClose
		case StepRename:
			return StepJournalRename
		default:
			return step
		}
	case strings.HasPrefix(target, internalDirectory+"/staging/"):
		switch step {
		case StepFileWrite:
			return StepStageFileWrite
		case StepFileSync:
			return StepStageFileSync
		case StepFileClose:
			return StepStageFileClose
		case StepRename:
			return StepStageRename
		default:
			return step
		}
	case strings.HasPrefix(target, internalDirectory+"/receipts/"):
		switch step {
		case StepFileWrite:
			return StepReceiptWrite
		case StepFileSync:
			return StepReceiptSync
		case StepFileClose:
			return StepReceiptClose
		case StepRename:
			return StepReceiptRename
		default:
			return step
		}
	default:
		return step
	}
}

func syncRootDir(root *os.Root, dir string) error {
	if err := rejectSymlinkPath(root, dir); err != nil {
		return err
	}
	f, err := openRootReadNoFollow(root, dir, true)
	if err != nil {
		return err
	}
	err = f.Sync()
	closeErr := f.Close()
	return errors.Join(err, closeErr)
}
func (s *Store) recover() error {
	return s.recoverContext(context.Background())
}

func (s *Store) recoverContext(ctx context.Context) error {
	dir := path.Join(internalDirectory, "transactions")
	if err := s.mkdirAll(dir, 0o700); err != nil {
		return metadataCorrupt(err)
	}
	d, err := openPinnedMetadataDir(s.rootFD, dir)
	if err != nil {
		return err
	}
	entries, err := d.ReadDir(-1)
	closeErr := d.Close()
	if readErr := errors.Join(err, closeErr); readErr != nil {
		return readErr
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		journalName := path.Join(dir, e.Name())
		info, err := s.rootFD.Lstat(journalName)
		if err != nil {
			return err
		}
		// A transaction directory may contain a hostile FIFO/device/socket. It
		// is not a journal and must neither block recovery nor turn a public
		// snapshot into a metadata-corruption error. Symlinks remain corruption:
		// accepting one would weaken the no-follow journal integrity contract.
		if info.Mode()&os.ModeSymlink != 0 {
			return metadataCorrupt(fmt.Errorf("unsafe journal entry %s", e.Name()))
		}
		if !info.Mode().IsRegular() {
			continue
		}
		raw, err := readMetadataLimit(ctx, s.rootFD, journalName, maxJournalManifestRead)
		if err != nil {
			return err
		}
		j, err := decodeJournalWithAlgorithmName(raw, s.config.HashAlgorithm, s.hashAlgorithmName)
		if err != nil {
			return fmt.Errorf("fs store: invalid journal %s: %w", e.Name(), errors.Join(store.ErrStorageCorrupt, err))
		}
		wantName, pathErr := journalPath(j.Receipt.RequestDigest)
		if pathErr != nil || e.Name() != path.Base(wantName) {
			return fmt.Errorf("fs store: invalid journal %s: %w", e.Name(), store.ErrStorageCorrupt)
		}
		if _, err := s.apply(ctx, j); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("fs store: invalid journal %s: %w", e.Name(), errors.Join(store.ErrStorageCorrupt, err))
		}
		if j.Receipt.IdempotencyKey != "" {
			if err := s.writeReceiptWithReplay(j.Receipt, replayOrZero(j.Replay)); err != nil {
				return err
			}
			if err := s.pruneReceipts(); err != nil {
				return err
			}
		}
		if err := s.remove(path.Join(dir, e.Name())); err != nil {
			return err
		}
		if err := s.syncDirAt(dir); err != nil {
			return err
		}
		if err := s.cleanupStage(j); err != nil {
			return err
		}
	}
	if err := s.cleanupOrphanStages(ctx); err != nil {
		return err
	}
	return s.cleanupOrphanTemps(ctx)
}

// cleanupOrphanTemps removes only store-created temporary *regular* files in
// every revision-visible directory.  It never follows or removes symlinks,
// devices, sockets, or FIFOs: those are user-visible filesystem objects, not
// recoverable store scratch.  Every parent whose namespace changed is synced.
func (s *Store) cleanupOrphanTemps(ctx context.Context) error {
	return s.cleanupOrphanTempsDir(ctx, ".")
}

func (s *Store) cleanupOrphanTempsDir(ctx context.Context, directory string) (err error) {
	d, err := openRootReadNoFollow(s.rootFD, directory, true)
	if err != nil {
		return err
	}
	entries, readErr := d.ReadDir(-1)
	closeErr := d.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := path.Join(directory, entry.Name())
		if directory == "." {
			name = entry.Name()
		}
		if name == internalDirectory {
			continue
		}
		info, err := s.rootFD.Lstat(name)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if info.IsDir() {
			if err := s.cleanupOrphanTempsDir(ctx, name); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(entry.Name(), ".okf-tmp-") && info.Mode().IsRegular() {
			if err := s.cleanupTemp(name, directory); err != nil {
				return err
			}
		}
	}
	return nil
}

// cleanupOrphanStages bounds failed-pre-journal attempts. A stage directory is
// private and has no durable manifest referring to it once transaction recovery
// above has completed, so deleting it cannot change published state.
func (s *Store) cleanupOrphanStages(ctx context.Context) error {
	root := path.Join(internalDirectory, "staging")
	d, err := openPinnedMetadataDir(s.rootFD, root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	entries, readErr := d.ReadDir(-1)
	closeErr := d.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return metadataCorrupt(err)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		stageDir := path.Join(root, entry.Name())
		info, err := s.rootFD.Lstat(stageDir)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return metadataCorrupt(fmt.Errorf("unsafe stage entry %q", entry.Name()))
		}
		if !info.IsDir() {
			// Orphan staging has no journal owner. Ignore special artifacts rather
			// than making unrelated Snapshot calls fail or block on a FIFO.
			continue
		}
		if !validStageID(entry.Name()) {
			return metadataCorrupt(fmt.Errorf("invalid stage entry %q", entry.Name()))
		}
		payloadDir := path.Join(root, entry.Name(), "payload")
		pd, err := openPinnedMetadataDir(s.rootFD, payloadDir)
		stageDir = path.Dir(payloadDir)
		// A crash may have happened after payload rmdir but before the parent
		// fsync. That is an expected partial cleanup state, not corruption.
		if errors.Is(err, os.ErrNotExist) {
			if err := s.cleanupSyncDir(stageDir, StepStageCleanupStageDirectory); err != nil {
				return err
			}
			if err := s.cleanupRemoveDir(stageDir, StepStageCleanupStageDirRemove); err != nil {
				return err
			}
			if err := s.cleanupSyncDir(root, StepStageCleanupRootDirectory); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		payloads, readErr := pd.ReadDir(-1)
		closeErr := pd.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return metadataCorrupt(err)
		}
		hasSpecialPayload := false
		for _, payload := range payloads {
			payloadName := path.Join(payloadDir, payload.Name())
			info, err := s.rootFD.Lstat(payloadName)
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return metadataCorrupt(fmt.Errorf("unsafe staged payload %q", payload.Name()))
			}
			if !info.Mode().IsRegular() {
				hasSpecialPayload = true
				continue
			}
			if !safePayloadName(payload.Name()) {
				return metadataCorrupt(fmt.Errorf("invalid staged payload %q", payload.Name()))
			}
			if err := s.cleanupRemoveFile(payloadName); err != nil {
				return err
			}
			if err := s.cleanupSyncDir(payloadDir, StepStageCleanupPayloadDirectory); err != nil {
				return err
			}
		}
		if hasSpecialPayload {
			// Keep an unowned directory containing special files untouched. It
			// cannot affect revision state and attempting rmdir would turn a
			// harmless FIFO/device into a failed public observation.
			continue
		}
		if err := s.cleanupRemoveDir(payloadDir, StepStageCleanupPayloadDirRemove); err != nil {
			return err
		}
		if err := s.cleanupSyncDir(stageDir, StepStageCleanupStageDirectory); err != nil {
			return err
		}
		if err := s.cleanupRemoveDir(stageDir, StepStageCleanupStageDirRemove); err != nil {
			return err
		}
		if err := s.cleanupSyncDir(root, StepStageCleanupRootDirectory); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) acquire(ctx context.Context) (func(), error) {
	deadline := time.Now().Add(s.config.LeaseTimeout)
	p := path.Join(internalDirectory, "lease")
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := s.mkdirAll(path.Dir(p), 0o700); err != nil {
			return nil, err
		}
		f, err := s.fdOpen(p, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}
		if err := s.enforcePrivateFileMode(f); err != nil {
			return nil, errors.Join(err, f.Close())
		}
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
		}
		_ = f.Close()
		if !leaseRetryable(err) {
			return nil, fmt.Errorf("fs store: acquire lease: %w", err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("fs store: lease timeout: %w", context.DeadlineExceeded)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (s *Store) acquireRead(ctx context.Context) (func(), error) {
	deadline := time.Now().Add(s.config.LeaseTimeout)
	p := path.Join(internalDirectory, "lease")
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := s.mkdirAll(path.Dir(p), 0o700); err != nil {
			return nil, err
		}
		f, err := s.fdOpen(p, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}
		if err := s.enforcePrivateFileMode(f); err != nil {
			return nil, errors.Join(err, f.Close())
		}
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
		if err == nil {
			return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
		}
		_ = f.Close()
		if !leaseRetryable(err) {
			return nil, fmt.Errorf("fs store: acquire read lease: %w", err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("fs store: read lease timeout: %w", context.DeadlineExceeded)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func leaseRetryable(err error) bool {
	return errors.Is(err, syscall.EINTR) || errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK)
}

type receiptFile struct {
	Version uint16              `json:"version"`
	Key     string              `json:"key"`
	Digest  string              `json:"digest"`
	Receipt store.CommitReceipt `json:"receipt"`
	Replay  *replaceReplay      `json:"replay,omitempty"`
}

// decodeReceiptFile validates the receipt through the same strict public
// decoder used by API callers; the envelope remains a durable integrity
// boundary and rejects extensions and trailing bytes.
func decodeReceiptFile(raw []byte) (receiptFile, error) {
	// encoding/json replaces malformed UTF-8 while decoding strings. Receipts
	// are replayed durable metadata, so reject it before any JSON scanner can
	// normalize an idempotency key, actor, path, or change code.
	if !utf8.Valid(raw) {
		return receiptFile{}, errors.New("receipt contains invalid UTF-8")
	}
	if err := store.RejectDuplicateJSONKeys(raw); err != nil {
		return receiptFile{}, err
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return receiptFile{}, err
	}
	if len(envelope) != 4 && len(envelope) != 5 {
		return receiptFile{}, errors.New("receipt envelope has an invalid field set")
	}
	for _, name := range []string{"version", "key", "digest", "receipt"} {
		value, ok := envelope[name]
		if !ok || bytes.Equal(value, []byte("null")) {
			return receiptFile{}, fmt.Errorf("receipt envelope missing %s", name)
		}
	}
	if !bytes.Equal(envelope["version"], []byte("2")) || !jsonString(envelope["key"]) || !jsonString(envelope["digest"]) || !jsonObject(envelope["receipt"]) {
		return receiptFile{}, errors.New("receipt envelope has invalid scalar or object types")
	}
	if replay, ok := envelope["replay"]; ok {
		if !jsonObject(replay) {
			return receiptFile{}, errors.New("receipt replay must be an object")
		}
		var checked replaceReplay
		if err := json.Unmarshal(replay, &checked); err != nil {
			return receiptFile{}, err
		}
	}
	var f receiptFile
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&f); err != nil {
		return receiptFile{}, err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return receiptFile{}, err
	}
	if f.Version != 2 || f.Key == "" {
		return receiptFile{}, errors.New("invalid receipt envelope")
	}
	if err := store.IdempotencyKey(f.Key).Validate(); err != nil {
		return receiptFile{}, err
	}
	// Re-decode the embedded receipt as raw JSON to retain the exact strict
	// receipt validation path.
	r, err := decodeCommitReceipt(envelope["receipt"])
	if err != nil {
		return receiptFile{}, err
	}
	f.Receipt = r
	if f.Key != string(r.IdempotencyKey) || f.Digest != r.RequestDigest {
		return receiptFile{}, errors.New("receipt envelope does not match receipt")
	}
	if f.Replay != nil {
		sealed, err := sealReplay(r, *f.Replay)
		if err != nil || sealed.Binding != f.Replay.Binding {
			return receiptFile{}, errors.New("receipt replay binding does not match receipt")
		}
	}
	canonical, err := json.Marshal(f)
	if err != nil || !bytes.Equal(raw, canonical) {
		return receiptFile{}, errors.New("receipt envelope is not canonical JSON")
	}
	return f, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return errors.New("trailing receipt data")
		}
		return err
	}
	return nil
}

func decodeCommitReceipt(raw json.RawMessage) (store.CommitReceipt, error) {
	var receipt store.CommitReceipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return store.CommitReceipt{}, err
	}
	return receipt, nil
}

func (s *Store) receiptPath(key store.IdempotencyKey) string {
	sum := sha256.Sum256([]byte(key))
	return path.Join(internalDirectory, "receipts", hex.EncodeToString(sum[:])+".json")
}
func (s *Store) lookupReceipt(key store.IdempotencyKey, digest string) (store.CommitReceipt, bool, error) {
	f, found, err := s.lookupReceiptFile(key, digest)
	if err != nil || !found {
		return store.CommitReceipt{}, found, err
	}
	return f.Receipt.Clone(), true, nil
}

func (s *Store) lookupReceiptFile(key store.IdempotencyKey, digest string) (receiptFile, bool, error) {
	raw, err := readMetadata(context.Background(), s.rootFD, s.receiptPath(key))
	if errors.Is(err, os.ErrNotExist) {
		return receiptFile{}, false, nil
	}
	if err != nil {
		return receiptFile{}, false, err
	}
	f, err := decodeReceiptFile(raw)
	if err != nil {
		return receiptFile{}, false, fmt.Errorf("fs store: receipt: %w", errors.Join(store.ErrStorageCorrupt, err))
	}
	if f.Key != string(key) {
		return receiptFile{}, false, fmt.Errorf("fs store: receipt: %w", store.ErrStorageCorrupt)
	}
	if f.Digest != digest {
		return receiptFile{}, false, &store.IdempotencyConflict{Key: key, PreviousDigest: f.Digest, RequestDigest: digest}
	}
	return f, true, nil
}
func (s *Store) writeReceipt(r store.CommitReceipt) error {
	return s.writeReceiptWithReplay(r, replaceReplay{})
}
func (s *Store) writeReceiptWithReplay(r store.CommitReceipt, replay replaceReplay) error {
	replay, err := sealReplay(r, replay)
	if err != nil {
		return fmt.Errorf("fs store: seal receipt replay: %w", errors.Join(store.ErrStorageCorrupt, err))
	}
	f := receiptFile{Version: 2, Key: string(r.IdempotencyKey), Digest: r.RequestDigest, Receipt: r}
	if replay.Validation.Diagnostics != nil {
		f.Replay = &replay
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	return s.writePrivateDurableAt(s.receiptPath(r.IdempotencyKey), raw)
}

func replayOrZero(replay *replaceReplay) replaceReplay {
	if replay == nil {
		return replaceReplay{}
	}
	return *replay
}

func (s *Store) replaceReplayResult(key store.IdempotencyKey, digest string, receipt store.CommitReceipt) (ReplaceConceptResult, error) {
	f, found, err := s.lookupReceiptFile(key, digest)
	if err != nil {
		return ReplaceConceptResult{}, err
	}
	if !found || f.Replay == nil {
		return ReplaceConceptResult{}, fmt.Errorf("fs store: replace replay projection missing: %w", store.ErrStorageCorrupt)
	}
	report, err := f.Replay.validation()
	if err != nil {
		return ReplaceConceptResult{}, fmt.Errorf("fs store: replace replay projection: %w", errors.Join(store.ErrStorageCorrupt, err))
	}
	return ReplaceConceptResult{Receipt: receipt, Validation: report}, nil
}

// pruneReceipts keeps the newest configured minimum even when old, then drops
// only receipts outside the retention window. It runs while the transaction
// lease is held (or during single-process Open recovery).
func (s *Store) pruneReceipts() error {
	if err := s.fail(StepReceiptPrune); err != nil {
		return err
	}
	dir := path.Join(internalDirectory, "receipts")
	d, err := openPinnedMetadataDir(s.rootFD, dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	entries, err := d.ReadDir(-1)
	closeErr := d.Close()
	if readErr := errors.Join(err, closeErr); readErr != nil {
		return readErr
	}
	type item struct {
		name string
		at   time.Time
	}
	items := make([]item, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		receiptName := path.Join(dir, entry.Name())
		info, err := s.rootFD.Lstat(receiptName)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return metadataCorrupt(fmt.Errorf("unsafe receipt entry %q", entry.Name()))
		}
		if !info.Mode().IsRegular() {
			continue
		}
		raw, err := readMetadata(context.Background(), s.rootFD, receiptName)
		if err != nil {
			return err
		}
		receipt, err := decodeReceiptFile(raw)
		if err != nil {
			return fmt.Errorf("fs store: receipt: %w", errors.Join(store.ErrStorageCorrupt, err))
		}
		items = append(items, item{name: entry.Name(), at: receipt.Receipt.CommitTime})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].at.Equal(items[j].at) {
			return items[i].name > items[j].name
		}
		return items[i].at.After(items[j].at)
	})
	cutoff := time.Now().UTC().Add(-s.config.ReceiptRetention)
	pruned := false
	for i := s.config.MinimumReceipts; i < len(items); i++ {
		if items[i].at.After(cutoff) {
			continue
		}
		if err := s.remove(path.Join(dir, items[i].name)); err != nil {
			return err
		}
		pruned = true
	}
	if err := s.syncDirAt(dir); err != nil {
		return err
	}
	if pruned {
		return s.postFault(StepReceiptPrune)
	}
	return nil
}

var _ store.Store = (*Store)(nil)
var _ store.Snapshot = (*snapshot)(nil)
