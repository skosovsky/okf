// Package fs implements the durable, single-filesystem store backend on Darwin
// and Linux.
//
// It deliberately uses an advisory root-inode lock: editors which bypass this package can
// observe a multi-file transaction in progress. Before the next Store
// observation or mutation, an unfinished journal is deterministically
// completed to its recorded post-state.
//
// The package is compile-safe on other platforms, but Open and OpenContext
// return *UnsupportedPlatformError there. No durable filesystem guarantees are
// provided unless both GOOS and the backing filesystem satisfy the documented
// capability checks.
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
	"time"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/internal/receiptprojection"
	"github.com/skosovsky/okf/mutation"
	"github.com/skosovsky/okf/store"
	"github.com/skosovsky/okf/validator"
)

const (
	internalDirectory  = ".okf"
	temporaryDirectory = internalDirectory + "/temporary"
)

// ErrUnsupportedPlatform identifies a platform for which this package has no
// durable filesystem backend. Callers can use errors.Is without parsing error
// text and errors.As to inspect GOOS and GOARCH.
var ErrUnsupportedPlatform = errors.New("fs store: unsupported platform")

// UnsupportedPlatformError reports the target selected when the durable
// filesystem backend is unavailable.
type UnsupportedPlatformError struct {
	GOOS   string
	GOARCH string
}

// Error implements error.
func (e *UnsupportedPlatformError) Error() string {
	if e == nil {
		return ErrUnsupportedPlatform.Error()
	}
	return fmt.Sprintf("%s: %s/%s (supported: darwin, linux)", ErrUnsupportedPlatform, e.GOOS, e.GOARCH)
}

// Unwrap makes UnsupportedPlatformError recognizable with errors.Is.
func (e *UnsupportedPlatformError) Unwrap() error { return ErrUnsupportedPlatform }

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
	DefaultMaxStagedFiles     int    = 100_000
	maxStagedPayloadBytes     int64  = 1 << 40
	maxStagedTransactionBytes int64  = 1 << 40
	replayFormatVersion       uint16 = 1
	replayAlgorithm                  = "sha256"
	replayDomain                     = "okf:replace-concept-replay:v1"
	maxReplayBytes                   = 1 << 20
	maxReplayDiagnostics             = 4096
	maxReplayRefs                    = 256
	maxReplayScannedFiles            = 1_000_000
	maxReplayString                  = 16 << 10
)

type stagedManifestLimits struct {
	maxFiles            int
	maxPayloadBytes     int64
	maxTransactionBytes int64
}

// journalManifestByteLimits keeps the immutable v5 wire ceiling distinct from
// an effective writer/recovery policy. Production binds both to the same
// ceiling; tests may lower either side without mutable globals or large
// allocations.
type journalManifestByteLimits struct {
	absolute  int
	effective int
}

func productionJournalManifestByteLimits() journalManifestByteLimits {
	return journalManifestByteLimits{
		absolute:  maxJournalManifestRead,
		effective: maxJournalManifestRead,
	}
}

func absoluteStagedManifestLimits() stagedManifestLimits {
	return stagedManifestLimits{
		maxFiles:            DefaultMaxStagedFiles,
		maxPayloadBytes:     maxStagedPayloadBytes,
		maxTransactionBytes: maxStagedTransactionBytes,
	}
}

func configuredStagedManifestLimits(config Config) stagedManifestLimits {
	return stagedManifestLimits{
		maxFiles:            config.MaxStagedFiles,
		maxPayloadBytes:     config.MaxStagedPayloadBytes,
		maxTransactionBytes: config.MaxStagedTransactionBytes,
	}
}

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

// Config controls bounded root-lock acquisition and receipt retention.
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
	StepClaimDirectorySync           Step = "claim_directory_sync"
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
	// Store configuration is immutable after binding. Copy the entire value,
	// rather than selecting exported fields, so validator-owned internal
	// checkpoints are frozen together with the public validation policy.
	if c.ValidatorConfig != nil {
		frozen := *c.ValidatorConfig
		c.ValidatorConfig = &frozen
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
	if c.MaxStagedPayloadBytes <= 0 || c.MaxStagedPayloadBytes > maxStagedPayloadBytes || c.MaxStagedTransactionBytes < c.MaxStagedPayloadBytes || c.MaxStagedTransactionBytes > maxStagedTransactionBytes {
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
	// fileSync and directorySync are bound by Open before any private metadata
	// setup. Production binds the real sync syscalls; exhaustive same-process
	// crash matrices may substitute only these physical acknowledgements while
	// retaining every fault hook and real namespace/content operation.
	fileSync      func(*os.File) error
	directorySync func(*Store, string) error
	// closeRead is a Store-local observation seam for pinned recovery readers.
	// Production leaves it nil and closes the descriptor directly.
	closeRead func(*os.File) error
	// clockNow is a Store-local producer seam. Production leaves it nil and
	// temporary artifact names use time.Now; tests can prove invalid clock
	// values fail before any file is created.
	clockNow func() time.Time
	// descriptorBarrier is an internal deterministic test seam. It is not part
	// of Config: durable callers must not be able to alter syscall ordering.
	descriptorBarrier func(string) error
	// rootLockIdentity is a narrow test seam for the mandatory identity
	// revalidation after flock succeeds and before private mutation begins.
	rootLockIdentity func(*os.File, *os.File) error
	// provenanceReadHook is a narrow deterministic test seam for the recovery
	// gate. It fires immediately before a known visible regular file is opened.
	provenanceReadHook func(string)
	// beforeMutationLease is a narrow deterministic test seam between planning
	// and the final cross-process root lock. Production stores leave it nil.
	beforeMutationLease  func()
	caseAliases          bool
	normalizationAliases bool
	// recoveryLimits is fixed at Open. privateReady becomes true only after the
	// complete lazy private initialization succeeds under the root-inode lock.
	recoveryLimits stagedManifestLimits
	privateReady   bool
	mu             sync.Mutex
	closed         bool
}

type mutationTargetIdentity struct {
	info      os.FileInfo
	absent    bool
	operation string
}

func errMutationTargetChanged(name string) error {
	return fmt.Errorf("fs store: mutation target changed before publication: %s", name)
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
// exclusive root lock. A volume may fold case without normalizing Unicode (or vice
// versa), so callers must not infer one capability from the other.
func (s *Store) detectPathAliases() error {
	if err := s.cleanupCapabilityInventory(context.Background()); err != nil {
		return err
	}
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
	publication := journalPublication{
		operation: internalArtifactOperation(fmt.Sprintf("capability:%s:%d", probe, time.Now().UnixNano())),
		role:      claimCapability,
	}
	if err := s.writePrivateDurableAtObserved(context.Background(), probe, []byte("okf"), &publication); err != nil {
		if publication.identity != nil && !publication.postFault {
			err = errors.Join(err, s.removeCapabilityProbe(publication.operation, probe))
		}
		return false, err
	}
	info, err := s.rootFD.Lstat(path.Join(internalDirectory, "capabilities", alternate))
	if err == nil {
		if !info.Mode().IsRegular() {
			cleanupErr := s.removeCapabilityProbe(publication.operation, probe)
			return false, errors.Join(errors.New("fs store: invalid capability probe entry"), cleanupErr)
		}
		aliases = true
	} else if !errors.Is(err, os.ErrNotExist) {
		cleanupErr := s.removeCapabilityProbe(publication.operation, probe)
		return false, errors.Join(err, cleanupErr)
	}
	if err := s.removeCapabilityProbe(publication.operation, probe); err != nil {
		return false, err
	}
	return aliases, nil
}

func (s *Store) removeCapabilityProbe(operation, probe string) error {
	if err := s.fail(StepCapabilityRemove); err != nil {
		return err
	}
	key, err := newArtifactClaimKey(operation, probe, claimCapability)
	if err != nil {
		return err
	}
	removed, err := s.consumeOwnedClaim(context.Background(), key, false)
	if err != nil || !removed {
		return errors.Join(errArtifactClaimConflict, err)
	}
	if err := s.postFault(StepCapabilityRemove); err != nil {
		return err
	}
	return s.syncDirAt(path.Dir(probe))
}

func (s *Store) validateFilesystemPathAliases(files map[string][]byte) error {
	if !s.caseAliases && !s.normalizationAliases {
		return nil
	}
	seen := make(map[string]string, len(files))
	for _, p := range sortedFilePaths(files) {
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
	basePaths := make(map[string][]string, len(base))
	for _, p := range sortedFilePaths(base) {
		key := s.filesystemPathKey(p)
		basePaths[key] = append(basePaths[key], p)
	}
	for _, p := range sortedFilePaths(next) {
		for _, previous := range basePaths[s.filesystemPathKey(p)] {
			if previous != p {
				return fmt.Errorf("%w: filesystem-equivalent result path %q replaces base path %q", store.ErrInvalidChangeSet, p, previous)
			}
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
		key := s.filesystemPathKey(file.Path)
		if previous, ok := basePaths[key]; ok && previous != file.Path {
			return fmt.Errorf("%w: filesystem-equivalent base paths %q and %q", store.ErrInvalidChangeSet, previous, file.Path)
		}
		basePaths[key] = file.Path
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

type recoveryOutcomeError struct {
	err    error
	replay *replaceReplay
}

func (e *recoveryOutcomeError) Error() string { return e.err.Error() }
func (e *recoveryOutcomeError) Unwrap() error { return e.err }

func newRecoveryOutcomeError(j journal, cause error) error {
	committed := store.NewCommittedError(j.Receipt, cause)
	return &recoveryOutcomeError{err: committed, replay: j.Replay}
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

type relationRefKey struct {
	id       string
	fragment string
}

func relationRefKeyOf(ref bundle.RelationRef) relationRefKey {
	return relationRefKey{id: ref.ID.String(), fragment: ref.Fragment}
}

func (left relationRefKey) less(right relationRefKey) bool {
	if left.id != right.id {
		return left.id < right.id
	}
	return left.fragment < right.fragment
}

func freezeValidation(report validator.Report) replaceReplay {
	v := validationReplay{ScannedFiles: report.ScannedFiles, Diagnostics: make([]validationDiagnosticReplay, len(report.Diagnostics))}
	for i, d := range report.Diagnostics {
		refs := make([]string, len(d.Refs))
		for j, ref := range d.Refs {
			refs[j] = ref.String()
		}
		sort.Strings(refs)
		v.Diagnostics[i] = validationDiagnosticReplay{Code: d.Code, File: d.File, Severity: d.Severity.String(), Message: d.Message, Source: d.Source.String(), RelationType: d.RelationType, RawTarget: d.RawTarget, Refs: refs}
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
		if _, err := parseCanonicalReplayRef(d.Source); err != nil {
			return false
		}
	}
	seenRefs := make(map[relationRefKey]struct{}, len(d.Refs))
	previous := ""
	havePrevious := false
	for _, raw := range d.Refs {
		if !utf8.ValidString(raw) || len(raw) > maxReplayString {
			return false
		}
		ref, err := parseCanonicalReplayRef(raw)
		if err != nil {
			return false
		}
		key := relationRefKeyOf(ref)
		if _, duplicate := seenRefs[key]; duplicate || havePrevious && raw <= previous {
			return false
		}
		seenRefs[key] = struct{}{}
		previous, havePrevious = raw, true
	}
	return true
}

func parseCanonicalReplayRef(raw string) (bundle.RelationRef, error) {
	ref, err := bundle.ParseRelationRef(raw)
	if err != nil {
		return bundle.RelationRef{}, err
	}
	if ref.String() != raw {
		return bundle.RelationRef{}, errors.New("relation ref is not canonically serialized")
	}
	return ref, nil
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
// root lock, CAS, journal, receipt and recovery path used by Commit.
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
		return projectReplaceRecoveryError(err, opts.IdempotencyKey, digest)
	}
	// Idempotency is checked before reading or comparing the requested base.
	// A replay is a request identity, not a fresh CAS authorization.
	if opts.IdempotencyKey != "" {
		if r, found, err := s.lookupReceiptContext(ctx, opts.IdempotencyKey, digest); err != nil {
			return ReplaceConceptResult{}, err
		} else if found {
			return s.replaceReplayResultContext(ctx, opts.IdempotencyKey, digest, r)
		}
	}
	base, err := s.Snapshot(ctx)
	if err != nil {
		return ReplaceConceptResult{}, err
	}
	// A concurrent Store may have written the receipt while Snapshot waited for
	// its root lock. Replay still wins over a now-stale caller base.
	if opts.IdempotencyKey != "" {
		if r, found, err := s.lookupReceiptContext(ctx, opts.IdempotencyKey, digest); err != nil {
			return ReplaceConceptResult{}, err
		} else if found {
			return s.replaceReplayResultContext(ctx, opts.IdempotencyKey, digest, r)
		}
	}
	if base.Revision() != req.BaseRevision {
		changedRefs, err := semanticRefs(ctx, base.(*snapshot))
		if err != nil {
			return ReplaceConceptResult{}, err
		}
		return ReplaceConceptResult{}, &store.Conflict{Expected: req.BaseRevision, Actual: base.Revision(), ChangedRefs: changedRefs, Retryable: true}
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
	if report.ExitCode() != 0 || blockedRelations {
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
	if s.beforeMutationLease != nil {
		s.beforeMutationLease()
	}
	unlock, err := s.acquire(ctx)
	if err != nil {
		return ReplaceConceptResult{}, err
	}
	defer unlock()
	// Planning happens outside the cross-process root lock. A different Store may
	// have crossed the durable journal boundary while this caller was planning
	// and then returned before apply/receipt cleanup. Complete that promise
	// before replay lookup, CAS, or publication can observe its invisible base.
	if err := s.prepareLocked(ctx); err != nil {
		return projectReplaceRecoveryError(err, opts.IdempotencyKey, digest)
	}
	// Another Store may have committed the same request while this caller was
	// planning. Check under the cross-process root lock before CAS.
	if opts.IdempotencyKey != "" {
		if r, found, err := s.lookupReceiptContext(ctx, opts.IdempotencyKey, digest); err != nil {
			return ReplaceConceptResult{}, err
		} else if found {
			return s.replaceReplayResultContext(ctx, opts.IdempotencyKey, digest, r)
		}
	}
	current, err := s.snapshotUnlocked(ctx)
	if err != nil {
		return ReplaceConceptResult{}, err
	}
	if current.Revision() != base.Revision() {
		projection, projectionErr := receiptprojection.Derive(ctx, base.(*snapshot).concepts, current.(*snapshot).concepts)
		if projectionErr != nil {
			return ReplaceConceptResult{}, projectionErr
		}
		return ReplaceConceptResult{}, &store.Conflict{Expected: base.Revision(), Actual: current.Revision(), ChangedRefs: projection.ChangedRefs, Retryable: true}
	}
	projection, err := receiptprojection.Derive(ctx, base.(*snapshot).concepts, next.concepts)
	if err != nil {
		return ReplaceConceptResult{}, err
	}
	receipt := store.CommitReceipt{FormatVersion: store.CommitReceiptFormatVersion, ChangeSetID: req.ChangeSetID, IdempotencyKey: opts.IdempotencyKey, RequestDigest: digest, BaseRevision: base.Revision(), ResultRevision: next.Revision(), CommitTime: time.Now().UTC(), ChangedRefs: projection.ChangedRefs, ChangedFiles: projection.ChangedFiles}
	replay := freezeValidation(report)
	// Return the same canonical projection that will be persisted for a retry.
	report, err = replay.validation()
	if err != nil {
		return ReplaceConceptResult{}, fmt.Errorf("fs store: canonicalize replay: %w", err)
	}
	if err := s.publishWithReplay(ctx, next, receipt, replay); err != nil {
		var committed *store.CommittedError
		if errors.As(err, &committed) {
			return ReplaceConceptResult{Receipt: committed.Receipt(), Validation: report}, err
		}
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

func (s *Store) syncFile(file *os.File) error {
	return s.fileSync(file)
}

func (s *Store) closeReadFile(file *os.File) error {
	if s.closeRead != nil {
		return s.closeRead(file)
	}
	return file.Close()
}

func (s *Store) syncDirectory(name string) error {
	return s.directorySync(s, name)
}

// Open validates configuration and pins one symlink-free root inode. Private
// metadata initialization and recovery are lazy.
func Open(root string, config Config) (*Store, error) {
	return OpenContext(context.Background(), root, config)
}

// OpenContext is Open with cancellation. It performs no namespace mutation.
func OpenContext(ctx context.Context, root string, config Config) (*Store, error) {
	return openContextWithRecoveryLimits(ctx, root, config, absoluteStagedManifestLimits())
}

type durabilitySyncImplementation struct {
	file      func(*os.File) error
	directory func(*Store, string) error
}

func productionDurabilitySyncImplementation() durabilitySyncImplementation {
	return durabilitySyncImplementation{
		file: func(file *os.File) error {
			return file.Sync()
		},
		directory: func(store *Store, name string) error {
			return store.fdSyncDir(name)
		},
	}
}

// openContextWithRecoveryLimits is a bounded test seam for exercising recovery
// policy without allocating production-sized payloads. Production callers pass
// only the immutable format ceilings through OpenContext.
func openContextWithRecoveryLimits(ctx context.Context, root string, config Config, absoluteLimits stagedManifestLimits) (*Store, error) {
	return openContextWithRecoveryLimitsAndSync(
		ctx,
		root,
		config,
		absoluteLimits,
		productionDurabilitySyncImplementation(),
	)
}

// openContextWithRecoveryLimitsAndSync is the package-private crash-matrix
// harness. sync is bound before any metadata setup or recovery begins.
func openContextWithRecoveryLimitsAndSync(
	ctx context.Context,
	root string,
	config Config,
	absoluteLimits stagedManifestLimits,
	syncImplementation durabilitySyncImplementation,
) (*Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if syncImplementation.file == nil || syncImplementation.directory == nil {
		return nil, errors.New("fs store: incomplete durability sync implementation")
	}
	if err := platformOpenError(); err != nil {
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
	s := &Store{
		rootFD:            rootFD,
		dirFD:             dirFD,
		config:            config,
		hashAlgorithmName: algorithmName,
		write:             func(f *os.File, data []byte) (int, error) { return f.Write(data) },
		fileSync:          syncImplementation.file,
		directorySync:     syncImplementation.directory,
		recoveryLimits:    absoluteLimits,
		rootLockIdentity:  verifyRootLockIdentity,
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
	// PostFault. Take the exclusive root lock because recovery may mutate the
	// journal's visible and private post-state.
	unlock, err := s.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := s.prepareLocked(ctx); err != nil {
		return nil, err
	}
	return s.snapshotUnlocked(ctx)
}

func (s *Store) snapshotUnlocked(ctx context.Context) (store.Snapshot, error) {
	files, err := s.readVisibleRoot(ctx)
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
// continues with a value-preserving, non-cancellable context. Any failure
// observed after that boundary carries the canonical receipt.
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
		return projectCommitRecoveryError(err, options.IdempotencyKey, digest)
	}
	if options.IdempotencyKey != "" {
		if r, found, err := s.lookupReceiptContext(ctx, options.IdempotencyKey, digest); err != nil || found {
			return r, err
		}
	}
	// Planning is intentionally outside the root lock. The fresh revision check below
	// is the sole authorization to publish this plan.
	base, err := s.Snapshot(ctx)
	if err != nil {
		return store.CommitReceipt{}, err
	}
	if change.BaseRevision != base.Revision() {
		changedRefs, err := semanticRefs(ctx, base.(*snapshot))
		if err != nil {
			return store.CommitReceipt{}, err
		}
		return store.CommitReceipt{}, &store.Conflict{Expected: change.BaseRevision, Actual: base.Revision(), ChangedRefs: changedRefs, Retryable: true}
	}
	planned, err := mutation.NewPlanner(s.config.ValidatorConfig).Plan(ctx, base, change)
	if err != nil {
		return store.CommitReceipt{}, err
	}
	if err := ctx.Err(); err != nil {
		return store.CommitReceipt{}, err
	}
	// Keep cooperating snapshots from observing a partial multi-file publish.
	// The advisory filesystem root lock provides the same boundary across Store
	// instances and processes.
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usable(); err != nil {
		return store.CommitReceipt{}, err
	}
	if s.beforeMutationLease != nil {
		s.beforeMutationLease()
	}
	unlock, err := s.acquire(ctx)
	if err != nil {
		return store.CommitReceipt{}, err
	}
	defer unlock()
	// The root lock may have been held by a publisher that made its journal durable
	// but returned before apply/receipt cleanup. Recovery is the first operation
	// under the final root lock so replay and CAS are refreshed from that post-state.
	if err := s.prepareLocked(ctx); err != nil {
		return projectCommitRecoveryError(err, options.IdempotencyKey, digest)
	}
	if options.IdempotencyKey != "" {
		if r, found, err := s.lookupReceiptContext(ctx, options.IdempotencyKey, digest); err != nil || found {
			return r, err
		}
	}
	current, err := s.snapshotUnlocked(ctx)
	if err != nil {
		return store.CommitReceipt{}, err
	}
	if current.Revision() != base.Revision() {
		projection, projectionErr := receiptprojection.Derive(ctx, base.(*snapshot).concepts, current.(*snapshot).concepts)
		if projectionErr != nil {
			return store.CommitReceipt{}, projectionErr
		}
		return store.CommitReceipt{}, &store.Conflict{Expected: base.Revision(), Actual: current.Revision(), ChangedRefs: projection.ChangedRefs, Retryable: true}
	}
	staged, err := snapshotFromSource(ctx, planned.Staged, s.config.HashAlgorithm)
	if err != nil {
		return store.CommitReceipt{}, err
	}
	projection, err := receiptprojection.Derive(ctx, base.(*snapshot).concepts, staged.concepts)
	if err != nil {
		return store.CommitReceipt{}, err
	}
	receipt := store.CommitReceipt{FormatVersion: store.CommitReceiptFormatVersion, ChangeSetID: change.ID, IdempotencyKey: options.IdempotencyKey, RequestDigest: digest, BaseRevision: base.Revision(), ResultRevision: staged.Revision(), CommitTime: time.Now().UTC(), ChangedRefs: projection.ChangedRefs, ChangedFiles: projection.ChangedFiles}
	if err := s.publish(ctx, staged, receipt); err != nil {
		var committed *store.CommittedError
		if errors.As(err, &committed) {
			return committed.Receipt(), err
		}
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
	return s.prepareLocked(ctx)
}

func matchingRecoveryReceipt(err error, key store.IdempotencyKey, digest string) (store.CommitReceipt, bool) {
	var committed *store.CommittedError
	if !errors.As(err, &committed) {
		return store.CommitReceipt{}, false
	}
	receipt := committed.Receipt()
	return receipt, receipt.RequestDigest == digest && receipt.IdempotencyKey == key
}

func projectCommitRecoveryError(err error, key store.IdempotencyKey, digest string) (store.CommitReceipt, error) {
	receipt, matches := matchingRecoveryReceipt(err, key, digest)
	if !matches {
		return store.CommitReceipt{}, err
	}
	return receipt, err
}

func projectReplaceRecoveryError(err error, key store.IdempotencyKey, digest string) (ReplaceConceptResult, error) {
	receipt, matches := matchingRecoveryReceipt(err, key, digest)
	if !matches {
		return ReplaceConceptResult{}, err
	}
	result := ReplaceConceptResult{Receipt: receipt}
	var outcome *recoveryOutcomeError
	if errors.As(err, &outcome) && outcome.replay != nil {
		report, replayErr := outcome.replay.validation()
		if replayErr != nil {
			return ReplaceConceptResult{}, errors.Join(err, metadataCorrupt(replayErr))
		}
		result.Validation = report
	}
	return result, err
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

// semanticRefs is the conservative conflict payload when only the actual
// snapshot is available. Every extant semantic ref is reported rather than
// guessing which one changed from a revision hash alone.
func semanticRefs(ctx context.Context, s *snapshot) ([]bundle.RelationRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil || s.concepts == nil {
		return nil, nil
	}
	conceptIDs, err := s.concepts.ConceptIDsContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	seen := make(map[relationRefKey]bundle.RelationRef)
	for _, conceptID := range conceptIDs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := addSemanticRef(ctx, seen, bundle.RelationRef{ID: conceptID}); err != nil {
			return nil, err
		}
		fragments, err := s.concepts.SubresourcesContext(ctx, conceptID)
		if err != nil {
			return nil, err
		}
		for _, fragment := range fragments {
			if err := addSemanticRef(ctx, seen, bundle.RelationRef{ID: conceptID, Fragment: fragment}); err != nil {
				return nil, err
			}
		}
		relations, err := s.concepts.SemanticLinksFromContext(ctx, conceptID)
		if err != nil {
			return nil, err
		}
		for _, relation := range relations {
			if err := addSemanticRef(ctx, seen, relation.Source); err != nil {
				return nil, err
			}
			if err := addSemanticRef(ctx, seen, relation.Target); err != nil {
				return nil, err
			}
		}
	}
	return canonicalSemanticRefs(ctx, seen)
}

func addSemanticRef(ctx context.Context, seen map[relationRefKey]bundle.RelationRef, ref bundle.RelationRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	key := relationRefKeyOf(ref)
	if key.id != "" {
		seen[key] = ref
	}
	return nil
}

func canonicalSemanticRefs(
	ctx context.Context,
	seen map[relationRefKey]bundle.RelationRef,
) ([]bundle.RelationRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]bundle.RelationRef, 0, len(seen))
	for _, ref := range seen {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	if err := sortSemanticRefsContext(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// sortSemanticRefsContext preserves canonical lexical ordering without an
// uninterruptible O(n log n) tail after cancellation.
func sortSemanticRefsContext(ctx context.Context, values []bundle.RelationRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(values) < 2 {
		return nil
	}
	scratch := make([]bundle.RelationRef, len(values))
	source, destination := values, scratch
	sourceIsScratch := false
	for width := 1; width < len(values); {
		for left := 0; left < len(values); {
			middle := left + min(width, len(values)-left)
			right := middle + min(width, len(values)-middle)
			first, second, output := left, middle, left
			for first < middle && second < right {
				if err := ctx.Err(); err != nil {
					return err
				}
				if relationRefKeyOf(source[second]).less(relationRefKeyOf(source[first])) {
					destination[output] = source[second]
					second++
				} else {
					destination[output] = source[first]
					first++
				}
				output++
			}
			for first < middle {
				if err := ctx.Err(); err != nil {
					return err
				}
				destination[output] = source[first]
				first++
				output++
			}
			for second < right {
				if err := ctx.Err(); err != nil {
					return err
				}
				destination[output] = source[second]
				second++
				output++
			}
			left = right
		}
		source, destination = destination, source
		sourceIsScratch = !sourceIsScratch
		if width >= len(values)-width {
			width = len(values)
		} else {
			width *= 2
		}
	}
	if sourceIsScratch {
		for index := range source {
			if err := ctx.Err(); err != nil {
				return err
			}
			values[index] = source[index]
		}
	}
	return ctx.Err()
}

type snapshot struct {
	files    map[string][]byte
	revision store.Revision
	manifest store.Manifest
	concepts *bundle.Bundle
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
	paths, err := sortedFilePathsContext(ctx, files)
	if err != nil {
		return nil, err
	}
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entries = append(entries, store.ManifestEntry{Path: p, Content: files[p]})
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
	return readVisibleRootWithClose(ctx, root, func(file *os.File) error { return file.Close() })
}

func (s *Store) readVisibleRoot(ctx context.Context) (map[string][]byte, error) {
	return readVisibleRootWithClose(ctx, s.rootFD, s.closeReadFile)
}

func readVisibleRootWithClose(ctx context.Context, root *os.Root, closeFile func(*os.File) error) (map[string][]byte, error) {
	out := map[string][]byte{}
	if err := walkVisibleRootWithClose(ctx, root, ".", "", out, closeFile); err != nil {
		return nil, err
	}
	return out, nil
}

func walkVisibleRoot(ctx context.Context, root *os.Root, directory, prefix string, out map[string][]byte) (err error) {
	return walkVisibleRootWithClose(ctx, root, directory, prefix, out, func(file *os.File) error { return file.Close() })
}

func walkVisibleRootWithClose(ctx context.Context, root *os.Root, directory, prefix string, out map[string][]byte, closeFile func(*os.File) error) (err error) {
	dir, err := openRootReadNoFollow(root, directory, true)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, closeFile(dir)) }()
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
			return metadataCorrupt(fmt.Errorf("fs store: invalid revision-visible path %q", rel))
		}
		name := path.Join(directory, entry.Name())
		info, err := root.Lstat(name)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return metadataCorrupt(fmt.Errorf("fs store: symlink revision-visible path %s", rel))
		}
		if info.IsDir() {
			if err := walkVisibleRootWithClose(ctx, root, name, rel, out, closeFile); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		b, err := readPinnedRegularWithClose(ctx, root, rel, info, closeFile)
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
	return readPinnedRegularWithClose(ctx, root, name, expected, func(file *os.File) error { return file.Close() })
}

func readPinnedRegularWithClose(ctx context.Context, root *os.Root, name string, expected os.FileInfo, closeFile func(*os.File) error) ([]byte, error) {
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
			return nil, metadataCorrupt(fmt.Errorf("fs store: unsafe ancestor %s", ancestor))
		}
		f, err := openRootReadNoFollow(root, ancestor, true)
		if err != nil {
			return nil, err
		}
		after, statErr := f.Stat()
		var structural error
		if statErr == nil && (!os.SameFile(before, after) || !after.IsDir()) {
			structural = fmt.Errorf("fs store: ancestor changed during snapshot: %s", ancestor)
		}
		closeErr := closeFile(f)
		if err := errors.Join(statErr, closeErr, metadataCorrupt(structural)); err != nil {
			return nil, err
		}
	}
	f, err := openRootReadNoFollow(root, name, false)
	if err != nil {
		return nil, err
	}
	actual, statErr := f.Stat()
	var structural error
	if statErr == nil && (!actual.Mode().IsRegular() || !os.SameFile(expected, actual)) {
		structural = fmt.Errorf("fs store: path changed during snapshot: %s", name)
	}
	var data []byte
	var readErr error
	if statErr == nil && structural == nil {
		data, readErr = readAllContext(ctx, f)
	}
	closeErr := closeFile(f)
	if err := errors.Join(statErr, readErr, closeErr, metadataCorrupt(structural)); err != nil {
		return nil, err
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
				return metadataCorrupt(errors.New("journal base/result state mismatch"))
			}
		}
	}
	for name := range resultByPath {
		if !seen[name] {
			if _, alsoBase := baseByPath[name]; alsoBase {
				return metadataCorrupt(errors.New("journal base/result state mismatch"))
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
			return metadataCorrupt(fmt.Errorf("invalid revision-visible path %q", rel))
		}
		name := path.Join(directory, entry.Name())
		info, err := s.rootFD.Lstat(name)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return metadataCorrupt(fmt.Errorf("symlink revision-visible path %s", rel))
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
			return metadataCorrupt(errors.New("journal base/result state mismatch"))
		}
		if (!hasBase || info.Size() != baseEntry.Size) && (!hasResult || info.Size() != resultEntry.Size) {
			return metadataCorrupt(errors.New("journal base/result state mismatch"))
		}
		if s.provenanceReadHook != nil {
			s.provenanceReadHook(rel)
		}
		if err := s.runDescriptorBarrier("provenance_read"); err != nil {
			return err
		}
		if err := s.runDescriptorBarrier("provenance_read:" + rel); err != nil {
			return err
		}
		digest, err := s.streamPinnedRegularDigest(ctx, rel, info)
		if err != nil {
			return err
		}
		if (!hasBase || digest != baseEntry.Digest) && (!hasResult || digest != resultEntry.Digest) {
			return metadataCorrupt(errors.New("journal base/result state mismatch"))
		}
		seen[rel] = true
	}
	return nil
}

func (s *Store) streamPinnedRegularDigest(ctx context.Context, name string, expected os.FileInfo) (string, error) {
	if err := rejectSymlinkPath(s.rootFD, name); err != nil {
		return "", err
	}
	f, err := openRootReadNoFollow(s.rootFD, name, false)
	if err != nil {
		return "", err
	}
	actual, statErr := f.Stat()
	if statErr == nil && (!actual.Mode().IsRegular() || !os.SameFile(expected, actual)) {
		return "", s.closeStructuralMetadata(f, fmt.Errorf("path changed during provenance scan: %s", name))
	}
	if statErr != nil {
		return "", errors.Join(statErr, s.closeReadFile(f))
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
			if err := s.runDescriptorBarrier("provenance_restat:" + name); err != nil {
				return "", errors.Join(err, f.Close())
			}
			current, pathErr := s.rootFD.Lstat(name)
			var structural error
			if pathErr == nil && (current == nil || !current.Mode().IsRegular() || !os.SameFile(actual, current)) {
				structural = fmt.Errorf("path changed after provenance scan: %s", name)
			}
			closeErr := s.closeReadFile(f)
			if err := errors.Join(closeErr, pathErr, metadataCorrupt(structural)); err != nil {
				return "", err
			}
			return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
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
// conformance and explicit policy assertions; independently, blocking semantic
// relation diagnostics veto publication. Recovery uses this same gate before
// it can touch a visible path. The returned bundle is the same immutable bundle
// used for validation.
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
		return nil, errors.Join(readErr, closeErr)
	}
	return data, nil
}

func (s *Store) readMetadataLimitObserved(ctx context.Context, name string, limit int64) ([]byte, error) {
	if err := s.runDescriptorBarrier("metadata_open"); err != nil {
		return nil, err
	}
	if err := s.runDescriptorBarrier("metadata_open:" + name); err != nil {
		return nil, err
	}
	f, err := s.openPinnedMetadata(name, false)
	if err != nil {
		return nil, err
	}
	identity, statErr := f.Stat()
	if statErr != nil {
		return nil, errors.Join(statErr, s.closeReadFile(f))
	}
	if err := s.runDescriptorBarrier("metadata_read"); err != nil {
		return nil, errors.Join(err, s.closeReadFile(f))
	}
	if err := s.runDescriptorBarrier("metadata_read:" + name); err != nil {
		return nil, errors.Join(err, s.closeReadFile(f))
	}
	data, readErr := readAllContextLimit(ctx, f, limit)
	if err := s.runDescriptorBarrier("metadata_restat:" + name); err != nil {
		return nil, errors.Join(err, s.closeReadFile(f))
	}
	current, pathErr := s.rootFD.Lstat(name)
	var structural error
	if pathErr == nil && (current == nil || !current.Mode().IsRegular() || !os.SameFile(identity, current)) {
		structural = fmt.Errorf("metadata path changed after read: %s", name)
	}
	closeErr := s.closeReadFile(f)
	if err := errors.Join(closeErr, pathErr, readErr, metadataCorrupt(structural)); err != nil {
		return nil, err
	}
	return data, nil
}

// readRecoveryJournal keeps one no-follow descriptor pinned from the initial
// file/type/size inspection through the bounded read and final pathname
// identity proof. Recovery can therefore validate one byte stream and later
// claim ownership of exactly that inode without a close/reopen gap.
func (s *Store) readRecoveryJournal(ctx context.Context, name string, limit int64) ([]byte, os.FileInfo, error) {
	if err := s.runDescriptorBarrier("journal_open"); err != nil {
		return nil, nil, err
	}
	f, err := s.fdOpen(name, os.O_RDONLY, 0)
	if err != nil {
		return nil, nil, err
	}
	if err := s.runDescriptorBarrier("journal_stat"); err != nil {
		return nil, nil, errors.Join(err, f.Close())
	}
	identity, statErr := f.Stat()
	if statErr != nil {
		return nil, nil, errors.Join(statErr, f.Close())
	}
	if identity == nil || !identity.Mode().IsRegular() {
		return nil, nil, s.closeStructuralMetadata(f, errors.New("journal is not a regular file"))
	}
	if identity.Size() > limit {
		return nil, nil, s.closeStructuralMetadata(f, fmt.Errorf("journal manifest exceeds %d byte limit", limit))
	}
	// This seam is after the pinned descriptor's initial type/size proof and
	// before the first byte read. Tests use it to model same-inode growth; a
	// raw seam error remains operational and closes the pinned descriptor.
	if err := s.runDescriptorBarrier("journal_before_read"); err != nil {
		return nil, nil, errors.Join(err, s.closeReadFile(f))
	}
	raw, readErr := readAllContext(ctx, io.LimitReader(f, limit+1))
	if readErr != nil {
		return nil, nil, errors.Join(readErr, f.Close())
	}
	if int64(len(raw)) > limit {
		return nil, nil, s.closeStructuralMetadata(f, fmt.Errorf("journal manifest exceeds %d byte limit", limit))
	}
	// This seam is deliberately after the byte read but while the descriptor is
	// still pinned. A stronger I/O error wins over cancellation at this cut.
	if err := s.runDescriptorBarrier("journal_ownership"); err != nil {
		return nil, nil, errors.Join(err, f.Close())
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, errors.Join(err, f.Close())
	}
	if err := s.runDescriptorBarrier("journal_restat"); err != nil {
		return nil, nil, errors.Join(err, f.Close())
	}
	current, pathErr := s.rootFD.Lstat(name)
	var structural error
	if pathErr == nil && (current == nil || !current.Mode().IsRegular() || !os.SameFile(identity, current)) {
		structural = errors.New("journal identity changed after read")
	}
	closeErr := s.closeReadFile(f)
	if err := errors.Join(closeErr, pathErr, metadataCorrupt(structural)); err != nil {
		return nil, nil, err
	}
	return raw, identity, nil
}

func (s *Store) readMetadataLimitOwned(ctx context.Context, name string, limit int64, expected os.FileInfo) ([]byte, error) {
	if err := s.runDescriptorBarrier("payload_open"); err != nil {
		return nil, err
	}
	f, err := s.fdOpen(name, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	if err := s.runDescriptorBarrier("payload_stat"); err != nil {
		return nil, errors.Join(err, f.Close())
	}
	actual, statErr := f.Stat()
	if statErr != nil {
		return nil, errors.Join(statErr, s.closeReadFile(f))
	}
	if expected == nil || !actual.Mode().IsRegular() || !os.SameFile(expected, actual) {
		return nil, s.closeStructuralMetadata(f, fmt.Errorf("transaction-owned payload changed: %s", name))
	}
	// The barrier models a raw read failure after the owned inode is pinned.
	// Its I/O error wins over a simultaneous cancellation; a nil barrier leaves
	// readAllContextLimit responsible for observing cancellation.
	if err := s.runDescriptorBarrier("payload_read"); err != nil {
		return nil, errors.Join(err, f.Close())
	}
	data, readErr := readAllContextLimit(ctx, f, limit)
	if readErr != nil {
		return nil, errors.Join(readErr, f.Close())
	}
	if err := s.runDescriptorBarrier("payload_restat"); err != nil {
		return nil, errors.Join(err, f.Close())
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(err, f.Close())
	}
	current, pathErr := s.rootFD.Lstat(name)
	var structural error
	if pathErr == nil && (current == nil || !current.Mode().IsRegular() || !os.SameFile(actual, current)) {
		structural = fmt.Errorf("transaction-owned payload changed after read: %s", name)
	}
	closeErr := s.closeReadFile(f)
	if err := errors.Join(closeErr, pathErr, metadataCorrupt(structural)); err != nil {
		return nil, err
	}
	return data, nil
}

func openPinnedMetadataDir(root *os.Root, name string) (*os.File, error) {
	return openPinnedMetadata(root, name, true)
}

func openPinnedMetadata(root *os.Root, name string, wantDir bool) (*os.File, error) {
	return openPinnedMetadataWithClose(root, name, wantDir, func(file *os.File) error { return file.Close() })
}

func (s *Store) openPinnedMetadata(name string, wantDir bool) (*os.File, error) {
	return openPinnedMetadataWithClose(s.rootFD, name, wantDir, s.closeReadFile)
}

func openPinnedMetadataWithClose(root *os.Root, name string, wantDir bool, closeFile func(*os.File) error) (*os.File, error) {
	clean := path.Clean(name)
	if clean == "." || !strings.HasPrefix(clean, internalDirectory+"/") {
		return nil, metadataCorrupt(fmt.Errorf("unsafe metadata path %q", name))
	}
	parts := strings.Split(clean, "/")
	for i := 1; i <= len(parts); i++ {
		component := strings.Join(parts[:i], "/")
		before, err := root.Lstat(component)
		if err != nil {
			return nil, err
		}
		if before.Mode()&os.ModeSymlink != 0 || (i < len(parts) && !before.IsDir()) {
			return nil, metadataCorrupt(fmt.Errorf("unsafe metadata component %s", component))
		}
		f, err := openRootReadNoFollow(root, component, i < len(parts))
		if err != nil {
			return nil, err
		}
		after, statErr := f.Stat()
		if i != len(parts) {
			var structural error
			if statErr == nil && (!after.IsDir() || !os.SameFile(before, after)) {
				structural = fmt.Errorf("metadata ancestor changed: %s", component)
			}
			closeErr := closeFile(f)
			if err := errors.Join(statErr, closeErr, metadataCorrupt(structural)); err != nil {
				return nil, err
			}
			continue
		}
		if statErr != nil {
			return nil, errors.Join(statErr, closeFile(f))
		}
		if !os.SameFile(before, after) || (wantDir && !after.IsDir()) || (!wantDir && !after.Mode().IsRegular()) {
			return nil, closeStructuralMetadataWith(f, fmt.Errorf("metadata path changed: %s", component), closeFile)
		}
		return f, nil
	}
	return nil, metadataCorrupt(fmt.Errorf("empty metadata path"))
}

// closeStructuralMetadata preserves both facts when structural validation and
// descriptor close fail together. Operational I/O is ordered first while the
// structural fact remains discoverable through errors.Is(ErrStorageCorrupt).
func closeStructuralMetadata(file *os.File, structural error) error {
	return closeStructuralMetadataWith(file, structural, func(file *os.File) error { return file.Close() })
}

func (s *Store) closeStructuralMetadata(file *os.File, structural error) error {
	return closeStructuralMetadataWith(file, structural, s.closeReadFile)
}

func closeStructuralMetadataWith(file *os.File, structural error, closeFile func(*os.File) error) error {
	closeErr := closeFile(file)
	if structural == nil {
		return closeErr
	}
	return errors.Join(closeErr, metadataCorrupt(structural))
}

func readAllContextLimit(ctx context.Context, r io.Reader, limit int64) ([]byte, error) {
	data, err := readAllContext(ctx, io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, metadataCorrupt(fmt.Errorf("metadata exceeds %d byte limit", limit))
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
			return metadataCorrupt(fmt.Errorf("fs store: unsafe path %q", name))
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
			return metadataCorrupt(fmt.Errorf("fs store: symlink path component %s", p))
		}
	}
	return nil
}
func readSource(ctx context.Context, source bundle.Source) (map[string][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ps, err := source.Paths(ctx)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	orderedPaths := make([]string, len(ps))
	for index, p := range ps {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		orderedPaths[index] = p
	}
	ps = orderedPaths
	sort.Strings(ps)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for index, p := range ps {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !safePath(p) {
			return nil, fmt.Errorf("fs store: invalid source path %q", p)
		}
		if index > 0 && ps[index-1] == p {
			return nil, fmt.Errorf("fs store: duplicate source path %q", p)
		}
	}
	out := map[string][]byte{}
	for _, p := range ps {
		if err := ctx.Err(); err != nil {
			return nil, err
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
// changing the bundle behind the store's advisory root lock.
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

func decodeJournalWithAlgorithmNameAndLimits(raw []byte, algorithm store.HashAlgorithm, expectedAlgorithm string, limits stagedManifestLimits) (journal, error) {
	if err := validateJournalManifestBytes(raw, maxJournalManifestRead); err != nil {
		return journal{}, err
	}
	// encoding/json replaces malformed UTF-8 while decoding strings. Reject it
	// first so recovery can never publish a replacement-character filename.
	if !utf8.Valid(raw) {
		return journal{}, errors.New("journal contains invalid UTF-8")
	}
	envelope, err := decodeJournalTopLevelEnvelope(raw)
	if err != nil {
		return journal{}, err
	}
	versionRaw, ok := envelope["version"]
	if !ok || !jsonCanonicalUint(versionRaw, ^uint64(0), false) {
		return journal{}, errors.New("journal version must be a canonical non-negative integer")
	}
	version, err := strconv.ParseUint(string(versionRaw), 10, 64)
	if err != nil {
		return journal{}, errors.New("journal version must be a canonical non-negative integer")
	}
	if version != 5 {
		return journal{}, fmt.Errorf("unsupported journal version %d", version)
	}
	// Nested duplicate keys and every v5-only field/type contract deliberately
	// follow the version gate. Unsupported envelopes are never interpreted as
	// the current journal schema, while current v5 retains recursive strictness.
	if err := store.RejectDuplicateJSONKeys(raw); err != nil {
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
	// The decoder is an integrity gate, not the store's configured policy gate.
	// Accept every manifest within immutable format ceilings here; recovery
	// applies the effective Config to the complete manifest before payload I/O.
	if err := validateStagedManifestLimits(j.Files, limits.maxFiles, limits.maxPayloadBytes, limits.maxTransactionBytes); err != nil {
		return journal{}, fmt.Errorf("invalid journal file manifest: %w", err)
	}
	payloads := make(map[string]struct{}, len(j.Files))
	for i, file := range j.Files {
		if !safePath(file.Path) || file.Path <= previous || file.Payload != payloadName(i) || !validPayloadDigest(file.Digest) {
			return journal{}, errors.New("invalid journal file manifest")
		}
		if _, exists := payloads[file.Payload]; exists {
			return journal{}, errors.New("duplicate journal payload")
		}
		previous, payloads[file.Payload] = file.Path, struct{}{}
	}
	canonical, err := json.Marshal(j)
	if err != nil || !bytes.Equal(raw, canonical) {
		return journal{}, errors.New("journal is not canonical JSON")
	}
	return j, nil
}

// decodeJournalTopLevelEnvelope rejects ambiguity in the generic envelope
// without interpreting any nested version-specific value. json.RawMessage
// preserves the exact scalar spelling for the canonical version gate.
func decodeJournalTopLevelEnvelope(raw []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return nil, errors.New("journal must be an object")
	}
	envelope := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		name, ok := token.(string)
		if !ok {
			return nil, errors.New("journal object key is not a string")
		}
		if _, duplicate := envelope[name]; duplicate {
			return nil, fmt.Errorf("duplicate JSON key %q", name)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		envelope[name] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, err
	}
	return envelope, nil
}

func validateJournalManifestBytes(raw []byte, limit int) error {
	if limit <= 0 || len(raw) > limit {
		return fmt.Errorf("journal manifest exceeds %d byte limit", limit)
	}
	return nil
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

// validateStagedManifestLimits is metadata-only. Callers must run it against
// the complete manifest before opening the first staged payload.
func validateStagedManifestLimits(files []journalFile, maxFiles int, maxPayloadBytes, maxTransactionBytes int64) error {
	if maxFiles <= 0 || maxPayloadBytes <= 0 || maxTransactionBytes < maxPayloadBytes || len(files) > maxFiles {
		return errors.New("staged file count exceeds limit")
	}
	var aggregate int64
	for _, entry := range files {
		if entry.Size < 0 || entry.Size > maxPayloadBytes || entry.Size > maxTransactionBytes || aggregate > maxTransactionBytes-entry.Size {
			return errors.New("staged payload exceeds limit")
		}
		aggregate += entry.Size
	}
	return nil
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
	return s.publishWithReplayAndManifestLimits(ctx, next, receipt, replay, productionJournalManifestByteLimits())
}

func (s *Store) publishWithReplayAndManifestLimits(
	ctx context.Context,
	next *snapshot,
	receipt store.CommitReceipt,
	replay replaceReplay,
	manifestLimits journalManifestByteLimits,
) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	replay, err = sealReplay(receipt, replay)
	if err != nil {
		return fmt.Errorf("fs store: seal replay: %w", errors.Join(store.ErrStorageCorrupt, err))
	}
	j, raw, err := s.prepareJournalContext(ctx, next, receipt, replay, manifestLimits)
	if err != nil {
		return err
	}
	stageOwnership, stageErr := s.stageJournalPayloadsObserved(ctx, next, j)
	if stageErr != nil {
		return errors.Join(stageErr, s.cleanupOwnedStage(j, stageOwnership))
	}
	jp, err := journalPath(receipt.RequestDigest)
	if err != nil {
		return errors.Join(err, s.cleanupOwnedStage(j, stageOwnership))
	}
	// A pathname that happened to be renamed is not a published journal. Its
	// data and directory entry must both be durable before post-state apply.
	publication, journalErr := s.writeJournalObserved(ctx, jp, raw)
	if !publication.durable {
		return errors.Join(journalErr, s.cleanupBeforeDurable(jp, publication.identity, j, stageOwnership))
	}
	if journalErr != nil {
		// PostFault models a process stopping after the physical durability
		// acknowledgement. Return the canonical outcome immediately and retain
		// the journal plus stage as the sole recovery evidence.
		return store.NewCommittedError(receipt, journalErr)
	}
	// The physical transaction-directory sync is the point of no return. Keep
	// caller values, but detach cancellation while converging the durable
	// promise. A PostFault at the boundary is an observed post-commit failure,
	// not permission to abandon the journal.
	durableCtx := context.WithoutCancel(ctx)
	var postErr error
	if journalIdentityErr := s.requireOwnedRegular(jp, publication.identity); journalIdentityErr != nil {
		return store.NewCommittedError(receipt, journalIdentityErr)
	}
	if _, applyErr := s.applyWithOwnership(durableCtx, j, &stageOwnership); applyErr != nil {
		postErr = errors.Join(postErr, applyErr)
		return store.NewCommittedError(receipt, postCommitCause(postErr, ctx))
	}
	if cleanupErr := s.cleanupVisibleClaims(j); cleanupErr != nil {
		return store.NewCommittedError(receipt, postCommitCause(cleanupErr, ctx))
	}
	if receipt.IdempotencyKey != "" {
		if receiptErr := s.writeReceiptWithReplay(receipt, replay); receiptErr != nil {
			postErr = errors.Join(postErr, receiptErr)
			return store.NewCommittedError(receipt, postCommitCause(postErr, ctx))
		}
		if pruneErr := s.pruneReceipts(); pruneErr != nil {
			postErr = errors.Join(postErr, pruneErr)
			return store.NewCommittedError(receipt, postCommitCause(postErr, ctx))
		}
	}
	if removeErr := s.removeOwnedDurableJournal(receipt.RequestDigest, jp, publication.identity); removeErr != nil {
		postErr = errors.Join(postErr, removeErr)
		return store.NewCommittedError(receipt, postCommitCause(postErr, ctx))
	}
	if cleanupErr := s.cleanupVisibleInstalledClaims(durableCtx, j); cleanupErr != nil {
		return store.NewCommittedError(receipt, postCommitCause(cleanupErr, ctx))
	}
	if cleanupErr := s.cleanupOwnedStageStrict(j, stageOwnership); cleanupErr != nil {
		postErr = errors.Join(postErr, cleanupErr)
		return store.NewCommittedError(receipt, postCommitCause(postErr, ctx))
	}
	if cause := postCommitCause(postErr, ctx); cause != nil {
		return store.NewCommittedError(receipt, cause)
	}
	return nil
}

func (s *Store) requireOwnedRegular(name string, owned os.FileInfo) error {
	current, err := s.rootFD.Lstat(name)
	if err != nil {
		return err
	}
	if owned == nil || !current.Mode().IsRegular() || !os.SameFile(owned, current) {
		return fmt.Errorf("fs store: transaction-owned artifact changed: %s", name)
	}
	return nil
}

func (s *Store) removeOwnedDurableJournal(operation, name string, owned os.FileInfo) error {
	if err := s.fail(StepRemove); err != nil {
		return err
	}
	key, err := newArtifactClaimKey(operation, name, claimJournal)
	if err != nil {
		return err
	}
	removed, err := s.consumeOwnedClaim(context.Background(), key, false)
	if err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("fs store: durable journal changed before cleanup: %s", name)
	}
	if err := s.postFault(StepRemove); err != nil {
		return err
	}
	if err := s.syncDirAt(path.Dir(name)); err != nil {
		return err
	}
	if _, err := s.rootFD.Lstat(name); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return fmt.Errorf("fs store: durable journal path reappeared after cleanup: %s", name)
		}
		return err
	}
	return nil
}

func (s *Store) cleanupOwnedStageStrict(j journal, owned stagePublicationOwnership) error {
	if err := s.cleanupOwnedStage(j, owned); err != nil {
		return err
	}
	if _, err := s.rootFD.Lstat(path.Dir(j.Stage)); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return fmt.Errorf("fs store: transaction-owned stage changed before cleanup: %s", path.Dir(j.Stage))
}

func (s *Store) captureStageDirectoryOwnership(ctx context.Context, j journal) (stagePublicationOwnership, error) {
	owned := stagePublicationOwnership{directories: make(map[string]os.FileInfo, 2), payloads: make(map[string]os.FileInfo, len(j.Files))}
	for _, name := range []string{path.Dir(j.Stage), j.Stage} {
		info, err := s.rootFD.Lstat(name)
		if err != nil {
			return owned, err
		}
		if info == nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return owned, metadataCorrupt(fmt.Errorf("invalid transaction-owned stage directory: %s", name))
		}
		key, err := newArtifactClaimKey(j.Receipt.RequestDigest, name, claimStageDir)
		if err != nil {
			return owned, err
		}
		verified, err := s.verifyDirectoryWitnessAt(ctx, key, name, info)
		if err != nil {
			return owned, err
		}
		owned.directories[name] = verified
	}
	for _, entry := range j.Files {
		name := path.Join(j.Stage, entry.Payload)
		info, err := s.rootFD.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			return owned, metadataCorrupt(fmt.Errorf("declared staged payload is missing: %s: %w", name, err))
		}
		if err != nil {
			return owned, err
		}
		key, err := newArtifactClaimKey(j.Receipt.RequestDigest, name, claimPayload)
		if err != nil {
			return owned, err
		}
		if _, err := s.verifyInstalledRegularArtifact(ctx, key, name, info); err != nil {
			return owned, err
		}
		owned.payloads[name] = info
	}
	return owned, nil
}

func (s *Store) cleanupBeforeDurable(journalName string, journalIdentity os.FileInfo, j journal, stage stagePublicationOwnership) error {
	absent, err := s.cleanupUnpublishedJournal(j.Receipt.RequestDigest, journalName, journalIdentity)
	if err != nil {
		return err
	}
	if !absent {
		return errors.New("fs store: unpublished journal path is not transaction-owned; stage preserved")
	}
	return s.cleanupOwnedStage(j, stage)
}

func postCommitCause(operationErr error, caller context.Context) error {
	if operationErr != nil {
		return operationErr
	}
	return caller.Err()
}

func (s *Store) applyWithOwnership(ctx context.Context, j journal, ownership *stagePublicationOwnership) (bool, error) {
	if !j.Receipt.ResultRevision.IsZero() && !j.Receipt.ResultRevision.Valid() {
		return false, metadataCorrupt(fmt.Errorf("fs store: invalid journal result revision"))
	}
	if j.Version != 5 || j.HashAlgorithm != s.hashAlgorithmName || !strings.HasPrefix(string(j.Receipt.ResultRevision), s.hashAlgorithmName+":") {
		return false, metadataCorrupt(fmt.Errorf("fs store: journal hash algorithm does not match configured algorithm"))
	}
	if err := s.validateJournalFilesystemPathAliasTransition(j.Base, j.Files); err != nil {
		return false, metadataCorrupt(err)
	}
	// Prove the visible namespace is a base/result provenance state before any
	// staged payload is opened. This deliberately streams known regular files
	// and rejects extras/size mismatches before open, so editor drift cannot
	// allocate attacker-sized current bytes or mask a bad staged payload.
	if err := s.validateCurrentJournalProvenance(ctx, j.Base, j.Files); err != nil {
		return false, err
	}
	next, err := s.readStagedJournal(ctx, j, ownership)
	if err != nil {
		return false, err
	}
	staged, err := newSnapshotWithAlgorithm(ctx, next, s.config.HashAlgorithm)
	if err != nil {
		return false, err
	}
	report, blocked, _, err := s.validateStagedSource(ctx, staged)
	if err != nil {
		if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
			return false, err
		}
		return false, metadataCorrupt(err)
	}
	if report.ExitCode() != 0 || blocked {
		return false, metadataCorrupt(errors.New("fs store: journal staged post-state fails validation"))
	}
	if j.Receipt.ResultRevision.Valid() {
		actual, err := newSnapshotWithAlgorithm(ctx, next, s.config.HashAlgorithm)
		if err != nil {
			return false, err
		}
		if actual.Revision() != j.Receipt.ResultRevision {
			return false, metadataCorrupt(fmt.Errorf("fs store: journal content does not match receipt revision"))
		}
	}
	// Materialization is intentionally after the metadata/streaming gate and
	// staged payload verification. It is needed only for the actual idempotent
	// apply comparison, never to decide whether staged bytes may be opened.
	current, err := s.readVisibleRoot(ctx)
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
		// A prior syscall may have made the journal result visible and then
		// failed at its PostFault before acknowledging the destination
		// namespace. Visibility is therefore not durability evidence. Replay
		// every directory that can contain a journal-visible path before
		// private receipt/journal cleanup is allowed to forget the promise.
		if err := s.syncJournalVisibleNamespace(ctx, j); err != nil {
			return false, err
		}
		return false, nil
	}
	if !baseExact {
		if !currentMatchesJournalStates(current, j.Base, next) {
			return false, metadataCorrupt(fmt.Errorf("fs store: journal base/result state mismatch: current=%s base=%s result=%s", currentSnapshot.Revision(), j.Receipt.BaseRevision, j.Receipt.ResultRevision))
		}
		if rootSignpostPrecedesIncompleteResult(current, j.Base, next) {
			return false, metadataCorrupt(errors.New("fs store: root index signpost precedes incomplete journal result"))
		}
		// A crash can occur after any individual visible write, rename, removal,
		// or directory sync. Every path has durable base/result provenance, so
		// this forward mixed state is safe to finish idempotently. index.md is the
		// final signpost: once it is at result, every non-root path must be there.
	}
	for _, p := range visibleApplicationOrder(current, next) {
		expected, identityErr := s.captureMutationTarget(p)
		if identityErr != nil {
			return false, identityErr
		}
		expected.operation = j.Receipt.RequestDigest
		if data, exists := next[p]; exists {
			if err := s.writeFileGuarded(ctx, p, data, expected); err != nil {
				return false, err
			}
			continue
		}
		if err := s.removeVisibleGuarded(ctx, p, expected); err != nil {
			return false, err
		}
	}
	verifiedFiles, err := s.readVisibleRoot(ctx)
	if err != nil {
		return false, err
	}
	if !sameVisibleFiles(verifiedFiles, next) {
		return false, metadataCorrupt(errors.New("fs store: post-apply visible state does not match journal result"))
	}
	verified, err := newSnapshotWithAlgorithm(ctx, verifiedFiles, s.config.HashAlgorithm)
	if err != nil || verified.Revision() != j.Receipt.ResultRevision {
		return false, metadataCorrupt(errors.New("fs store: post-apply revision does not match journal receipt"))
	}
	if err := s.syncJournalVisibleNamespace(ctx, j); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) captureMutationTarget(name string) (mutationTargetIdentity, error) {
	info, err := s.rootFD.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return mutationTargetIdentity{absent: true}, nil
	}
	if err != nil {
		return mutationTargetIdentity{}, err
	}
	if !info.Mode().IsRegular() {
		return mutationTargetIdentity{}, errMutationTargetChanged(name)
	}
	return mutationTargetIdentity{info: info}, nil
}

func (s *Store) removeVisibleGuarded(ctx context.Context, name string, expected mutationTargetIdentity) error {
	step := s.durableStep(name, StepRemove)
	if err := s.fail(step); err != nil {
		return err
	}
	if expected.operation == "" {
		return errArtifactClaimConflict
	}
	if err := s.removeVisibleClaimed(ctx, name, expected, step); err != nil {
		return err
	}
	return nil
}

// syncJournalVisibleNamespace closes the recovery gap between a visible
// namespace syscall and its skipped PostFault durability boundary. The union
// is intentionally conservative: recovery can prove the journal's base/result
// provenance, but a result-exact retry cannot prove which individual rename,
// removal, or mkdir was acknowledged before the interrupted process stopped.
func (s *Store) syncJournalVisibleNamespace(ctx context.Context, j journal) error {
	directories := make(map[string]struct{}, len(j.Base)+len(j.Files)+1)
	addAncestors := func(name string) {
		dir := path.Dir(name)
		for {
			directories[dir] = struct{}{}
			if dir == "." {
				return
			}
			dir = path.Dir(dir)
		}
	}
	for _, file := range j.Base {
		addAncestors(file.Path)
	}
	for _, file := range j.Files {
		addAncestors(file.Path)
	}
	return s.syncNamespaceDirectories(ctx, directories)
}

func (s *Store) syncNamespaceDirectories(ctx context.Context, directories map[string]struct{}) error {
	ordered := make([]string, 0, len(directories))
	for dir := range directories {
		ordered = append(ordered, dir)
	}
	sort.Slice(ordered, func(i, j int) bool {
		leftDepth := namespaceDepth(ordered[i])
		rightDepth := namespaceDepth(ordered[j])
		if leftDepth != rightDepth {
			return leftDepth > rightDepth
		}
		return ordered[i] < ordered[j]
	})
	for _, dir := range ordered {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.syncDirAt(dir); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func namespaceDepth(dir string) int {
	if dir == "." || dir == "" {
		return 0
	}
	return strings.Count(path.Clean(dir), "/") + 1
}

func knownPrivateDirectories() []string {
	return []string{
		internalDirectory,
		path.Join(internalDirectory, "transactions"),
		path.Join(internalDirectory, "staging"),
		path.Join(internalDirectory, "receipts"),
		path.Join(internalDirectory, "capabilities"),
		claimDirectory,
		temporaryDirectory,
	}
}

func (s *Store) syncKnownPrivateNamespace(ctx context.Context) error {
	directories := make(map[string]struct{}, len(knownPrivateDirectories())+1)
	for _, name := range knownPrivateDirectories() {
		for dir := name; ; dir = path.Dir(dir) {
			directories[dir] = struct{}{}
			if dir == "." {
				break
			}
		}
	}
	return s.syncNamespaceDirectories(ctx, directories)
}

// visibleApplicationOrder derives the deterministic physical publication
// sequence without changing either journal or receipt canonical ordering.
// index.md is the root revision signpost, so it is applied only after every
// other revision-visible write or removal has reached its durability boundary.
func visibleApplicationOrder(current, next map[string][]byte) []string {
	paths := make([]string, 0, len(current)+len(next))
	for p, data := range next {
		old, exists := current[p]
		if !exists || !bytes.Equal(old, data) {
			paths = append(paths, p)
		}
	}
	for p := range current {
		if _, exists := next[p]; !exists {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	for i, p := range paths {
		if p != "index.md" {
			continue
		}
		copy(paths[i:], paths[i+1:])
		paths[len(paths)-1] = p
		break
	}
	return paths
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

// rootSignpostPrecedesIncompleteResult detects a reverse publication state
// that cannot be produced by this store: an actually changed root index.md is
// already at result while at least one non-root path is not. Recovery must not
// publish that missing non-root work after the root revision signpost.
func rootSignpostPrecedesIncompleteResult(current map[string][]byte, base []journalBaseFile, result map[string][]byte) bool {
	const signpost = "index.md"

	baseByPath := make(map[string]journalBaseFile, len(base))
	for _, entry := range base {
		baseByPath[entry.Path] = entry
	}
	rootMatchesBase := matchesJournalBaseState(current, signpost, baseByPath)
	rootMatchesResult := matchesJournalResultState(current, signpost, result)
	if !rootMatchesResult || rootMatchesBase {
		return false
	}

	paths := make(map[string]struct{}, len(base)+len(result))
	for path := range baseByPath {
		if path != signpost {
			paths[path] = struct{}{}
		}
	}
	for path := range result {
		if path != signpost {
			paths[path] = struct{}{}
		}
	}
	for path := range paths {
		if !matchesJournalResultState(current, path, result) {
			return true
		}
	}
	return false
}

func matchesJournalBaseState(current map[string][]byte, path string, base map[string]journalBaseFile) bool {
	data, exists := current[path]
	entry, expected := base[path]
	if !expected {
		return !exists
	}
	return exists && int64(len(data)) == entry.Size && entry.Digest == sha256Digest(data)
}

func matchesJournalResultState(current map[string][]byte, path string, result map[string][]byte) bool {
	data, exists := current[path]
	expected, wanted := result[path]
	if !wanted {
		return !exists
	}
	return exists && bytes.Equal(data, expected)
}

func sha256Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func sha256DigestContext(ctx context.Context, data []byte) (string, error) {
	h := sha256.New()
	const chunk = 64 << 10
	for offset := 0; offset < len(data); {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		end := offset + chunk
		if end > len(data) {
			end = len(data)
		}
		if _, err := h.Write(data[offset:end]); err != nil {
			return "", err
		}
		offset = end
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
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

func (s *Store) prepareJournalContext(
	ctx context.Context,
	next *snapshot,
	receipt store.CommitReceipt,
	replay replaceReplay,
	manifestLimits journalManifestByteLimits,
) (journal, []byte, error) {
	if err := ctx.Err(); err != nil {
		return journal{}, nil, err
	}
	stage, err := journalStage(receipt.RequestDigest)
	if err != nil {
		return journal{}, nil, err
	}
	baseFiles, err := s.readVisibleRoot(ctx)
	if err != nil {
		return journal{}, nil, err
	}
	base, err := newSnapshotWithAlgorithm(ctx, baseFiles, s.config.HashAlgorithm)
	if err != nil {
		return journal{}, nil, err
	}
	if base.Revision() != receipt.BaseRevision {
		return journal{}, nil, errors.New("fs store: visible base does not match receipt")
	}
	if err := s.validateFilesystemPathAliasTransition(base.files, next.files); err != nil {
		return journal{}, nil, err
	}
	j := journal{
		Version:       5,
		HashAlgorithm: s.hashAlgorithmName,
		Stage:         stage,
		Base:          []journalBaseFile{},
		Files:         []journalFile{},
		Receipt:       receipt,
	}
	basePaths, err := sortedFilePathsContext(ctx, base.files)
	if err != nil {
		return journal{}, nil, err
	}
	for _, p := range basePaths {
		if err := ctx.Err(); err != nil {
			return journal{}, nil, err
		}
		data := base.files[p]
		digest, err := sha256DigestContext(ctx, data)
		if err != nil {
			return journal{}, nil, err
		}
		j.Base = append(j.Base, journalBaseFile{Path: p, Size: int64(len(data)), Digest: digest})
	}
	j.BaseBinding, err = journalBaseBindingContext(ctx, receipt, j.Base)
	if err != nil {
		return journal{}, nil, err
	}
	if replay.Validation.Diagnostics != nil {
		j.Replay = &replay
	}
	paths, err := sortedFilePathsContext(ctx, next.files)
	if err != nil {
		return journal{}, nil, err
	}
	if len(paths) > s.config.MaxStagedFiles {
		return journal{}, nil, fmt.Errorf("%w: staged file limit exceeded", store.ErrInvalidChangeSet)
	}
	var aggregate int64
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			return journal{}, nil, err
		}
		size := int64(len(next.files[p]))
		if size > s.config.MaxStagedPayloadBytes || aggregate > s.config.MaxStagedTransactionBytes-size {
			return journal{}, nil, fmt.Errorf("%w: staged payload limit exceeded", store.ErrInvalidChangeSet)
		}
		aggregate += size
	}
	for i, p := range paths {
		if err := ctx.Err(); err != nil {
			return journal{}, nil, err
		}
		if !safePath(p) {
			return journal{}, nil, fmt.Errorf("fs store: invalid journal path %q", p)
		}
		payload := payloadName(i)
		data := next.files[p]
		digest, err := sha256DigestContext(ctx, data)
		if err != nil {
			return journal{}, nil, err
		}
		j.Files = append(j.Files, journalFile{Path: p, Payload: payload, Size: int64(len(data)), Digest: digest})
	}
	raw, err := json.Marshal(j)
	if err != nil {
		return journal{}, nil, err
	}
	for _, limit := range []int{manifestLimits.absolute, manifestLimits.effective} {
		if err := validateJournalManifestBytes(raw, limit); err != nil {
			return journal{}, nil, fmt.Errorf("fs store: %w", err)
		}
	}
	if _, err := decodeJournalWithAlgorithmNameAndLimits(raw, s.config.HashAlgorithm, s.hashAlgorithmName, absoluteStagedManifestLimits()); err != nil {
		return journal{}, nil, fmt.Errorf("fs store: generated invalid journal: %w", errors.Join(store.ErrStorageCorrupt, err))
	}
	return j, raw, nil
}

type stagePublicationOwnership struct {
	directories map[string]os.FileInfo
	payloads    map[string]os.FileInfo
}

func (s *Store) stageJournalPayloadsObserved(ctx context.Context, next *snapshot, j journal) (stagePublicationOwnership, error) {
	owned := stagePublicationOwnership{directories: make(map[string]os.FileInfo, 2), payloads: make(map[string]os.FileInfo, len(j.Files))}
	if err := ctx.Err(); err != nil {
		return owned, err
	}
	for _, directory := range []string{path.Dir(j.Stage), j.Stage} {
		key, err := newArtifactClaimKey(j.Receipt.RequestDigest, directory, claimStageDir)
		if err != nil {
			return owned, err
		}
		identity, err := s.createOwnedStageDirectory(ctx, key)
		if identity != nil {
			current, statErr := s.rootFD.Lstat(directory)
			if statErr == nil && current.IsDir() && os.SameFile(identity, current) {
				owned.directories[directory] = identity
			} else if err != nil {
				cleanupErr := s.cleanupAttemptStageBuild(ctx, key, identity)
				return owned, errors.Join(err, cleanupErr)
			} else {
				return owned, errors.Join(errArtifactClaimConflict, statErr)
			}
		}
		if err != nil {
			return owned, err
		}
	}
	for _, entry := range j.Files {
		if err := ctx.Err(); err != nil {
			return owned, err
		}
		data, ok := next.files[entry.Path]
		digest, digestErr := sha256DigestContext(ctx, data)
		if digestErr != nil {
			return owned, digestErr
		}
		if !ok || int64(len(data)) != entry.Size || digest != entry.Digest {
			return owned, fmt.Errorf("fs store: generated journal payload metadata drift: %w", store.ErrStorageCorrupt)
		}
		payloadPath := path.Join(j.Stage, entry.Payload)
		publication := journalPublication{operation: j.Receipt.RequestDigest, role: claimPayload}
		if err := s.writePrivateDurableAtObserved(ctx, payloadPath, data, &publication); err != nil {
			if publication.identity != nil {
				owned.payloads[payloadPath] = publication.identity
			}
			return owned, err
		}
		owned.payloads[payloadPath] = publication.identity
	}
	return owned, ctx.Err()
}

func sortedFilePaths(files map[string][]byte) []string {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

func sortedFilePathsContext(ctx context.Context, files map[string][]byte) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	sort.Strings(paths)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return paths, nil
}

func journalBaseBinding(receipt store.CommitReceipt, base []journalBaseFile) (string, error) {
	return journalBaseBindingContext(context.Background(), receipt, base)
}

func journalBaseBindingContext(ctx context.Context, receipt store.CommitReceipt, base []journalBaseFile) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(base)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, value := range [][]byte{[]byte("okf:journal-base:v1"), []byte(receipt.RequestDigest), []byte(receipt.BaseRevision), []byte(receipt.ResultRevision), canonical} {
		if err := ctx.Err(); err != nil {
			return "", err
		}
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

func (s *Store) readStagedJournal(ctx context.Context, j journal, ownership *stagePublicationOwnership) (map[string][]byte, error) {
	if err := validateStagedManifestLimits(j.Files, s.config.MaxStagedFiles, s.config.MaxStagedPayloadBytes, s.config.MaxStagedTransactionBytes); err != nil {
		return nil, metadataCorrupt(fmt.Errorf("staged manifest exceeds configured limit: %w", err))
	}
	next := make(map[string][]byte, len(j.Files))
	for _, entry := range j.Files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !safePath(entry.Path) || !safeStageDirectory(j.Stage) || !safePayloadName(entry.Payload) {
			return nil, metadataCorrupt(errors.New("unsafe staged payload reference"))
		}
		// Payloads are revision-visible bytes, not metadata. Their declared size
		// is checked against the opened regular file, while the journal itself
		// remains bounded above. This is what permits legitimate large assets.
		payloadPath := path.Join(j.Stage, entry.Payload)
		var data []byte
		var err error
		if ownership == nil {
			return nil, errArtifactClaimConflict
		} else {
			expected, exists := ownership.payloads[payloadPath]
			if !exists {
				return nil, errArtifactClaimConflict
			}
			data, err = s.readMetadataLimitOwned(ctx, payloadPath, entry.Size, expected)
		}
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, metadataCorrupt(fmt.Errorf("declared staged payload is missing: %s: %w", payloadPath, err))
			}
			return nil, err
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
	actual, err := newSnapshotWithAlgorithm(ctx, next, s.config.HashAlgorithm)
	if err != nil {
		return nil, err
	}
	if actual.Revision() != j.Receipt.ResultRevision {
		return nil, metadataCorrupt(errors.New("journal staged content does not match receipt revision"))
	}
	return next, nil
}

// cleanupOwnedStage is the pre-journal abort path. It removes only inodes
// installed by this attempt; same-name replacements and unrelated entries are
// preserved without turning an otherwise safe abort into a namespace sweep.
func (s *Store) cleanupOwnedStage(j journal, owned stagePublicationOwnership) error {
	if !safeStageDirectory(j.Stage) {
		return metadataCorrupt(errors.New("unsafe owned stage cleanup path"))
	}
	payloadPaths := make([]string, 0, len(owned.payloads))
	for name := range owned.payloads {
		payloadPaths = append(payloadPaths, name)
	}
	sort.Strings(payloadPaths)
	for _, name := range payloadPaths {
		if err := s.cleanupOwnedFile(j.Receipt.RequestDigest, name, owned.payloads[name]); err != nil {
			return err
		}
	}
	payloadCurrent, payloadMatches, err := s.ownedDirectory(j.Stage, owned.directories[j.Stage])
	if err != nil {
		return err
	}
	if payloadCurrent {
		if err := s.cleanupSyncDir(j.Stage, StepStageCleanupPayloadDirectory); err != nil {
			return err
		}
		if !payloadMatches {
			return nil
		}
		if err := s.cleanupOwnedDirectory(j.Receipt.RequestDigest, j.Stage, owned.directories[j.Stage], StepStageCleanupPayloadDirRemove); err != nil {
			return err
		}
	}
	stageName := path.Dir(j.Stage)
	stageCurrent, stageMatches, err := s.ownedDirectory(stageName, owned.directories[stageName])
	if err != nil {
		return err
	}
	if stageCurrent {
		if err := s.cleanupSyncDir(stageName, StepStageCleanupStageDirectory); err != nil {
			return err
		}
		if !stageMatches {
			return nil
		}
		if err := s.cleanupOwnedDirectory(j.Receipt.RequestDigest, stageName, owned.directories[stageName], StepStageCleanupStageDirRemove); err != nil {
			return err
		}
	}
	return s.cleanupSyncDir(path.Dir(stageName), StepStageCleanupRootDirectory)
}

func (s *Store) cleanupOwnedFile(operation, name string, owned os.FileInfo) error {
	if owned == nil {
		return nil
	}
	if err := s.fail(StepStageCleanupPayloadRemove); err != nil {
		return err
	}
	key, err := newArtifactClaimKey(operation, name, claimPayload)
	if err != nil {
		return err
	}
	removed, err := s.consumeOwnedClaim(context.Background(), key, false)
	if err != nil {
		return err
	}
	if !removed {
		return nil
	}
	return s.postFault(StepStageCleanupPayloadRemove)
}

func (s *Store) ownedDirectory(name string, owned os.FileInfo) (exists, matches bool, err error) {
	current, err := s.rootFD.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if !current.IsDir() || current.Mode()&os.ModeSymlink != 0 {
		return true, false, nil
	}
	return true, owned != nil && os.SameFile(owned, current), nil
}

func (s *Store) cleanupOwnedDirectory(operation, name string, owned os.FileInfo, step Step) error {
	key, err := newArtifactClaimKey(operation, name, claimStageDir)
	if err != nil {
		return err
	}
	// Directory creation and its sentinel publication are two separately
	// faultable operations.  The same attempt still has a pinned inode witness
	// when sentinel publication stops early, so finish that ownership record
	// before entering the claim protocol.  Recovery never takes this path for a
	// legacy directory: it lacks the attempt-local inode in owned.
	current, err := s.rootFD.Lstat(name)
	if err != nil {
		return err
	}
	if owned == nil || current == nil || !current.IsDir() || !os.SameFile(owned, current) {
		return errArtifactClaimConflict
	}
	if err := s.fail(step); err != nil {
		return err
	}
	removed, err := s.consumeOwnedClaim(context.Background(), key, true)
	if err != nil {
		return err
	}
	if !removed {
		return nil
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
	if err := s.syncDirectory(name); err != nil {
		return err
	}
	return s.postFault(step)
}

type journalPublication struct {
	identity  os.FileInfo
	durable   bool
	postFault bool
	operation string
	role      artifactRole
}

func (s *Store) writeJournalObserved(ctx context.Context, target string, data []byte) (journalPublication, error) {
	publication := journalPublication{}
	decoded, decodeErr := decodeJournalWithAlgorithmNameAndLimits(data, s.config.HashAlgorithm, s.hashAlgorithmName, absoluteStagedManifestLimits())
	if decodeErr != nil {
		return publication, metadataCorrupt(decodeErr)
	}
	publication.operation = decoded.Receipt.RequestDigest
	publication.role = claimJournal
	if err := s.fail(StepJournalWrite); err != nil {
		return publication, err
	}
	if err := ctx.Err(); err != nil {
		return publication, err
	}
	if err := s.writePrivateDurableAtObserved(ctx, target, data, &publication); err != nil {
		return publication, err
	}
	return publication, s.postFault(StepJournalWrite)
}

// cleanupUnpublishedJournal removes only the inode installed by this
// publication attempt. A same-name foreign replacement is evidence outside
// the transaction and must survive pre-boundary cleanup.
func (s *Store) cleanupUnpublishedJournal(operation, name string, owned os.FileInfo) (bool, error) {
	if owned != nil {
		if err := s.fail(StepRemove); err != nil {
			return false, err
		}
		key, err := newArtifactClaimKey(operation, name, claimJournal)
		if err != nil {
			return false, err
		}
		removed, err := s.consumeOwnedClaim(context.Background(), key, false)
		if err != nil {
			return false, err
		}
		if removed {
			if err := s.postFault(StepRemove); err != nil {
				return false, err
			}
		}
	}
	if err := s.syncDirAt(path.Dir(name)); err != nil {
		return false, err
	}
	_, err := s.rootFD.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, nil
}
func (s *Store) writeFileGuarded(ctx context.Context, target string, data []byte, expected mutationTargetIdentity) error {
	return s.writeDurableAtObserved(ctx, target, data, 0o644, nil, &expected)
}
func (s *Store) syncDirAt(dir string) error {
	return s.syncDirAtObserved(dir, nil)
}

func (s *Store) syncDirAtObserved(dir string, afterPhysicalSync func() error) error {
	step := StepDirectorySync
	if dir == path.Join(internalDirectory, "transactions") {
		step = StepJournalDirectorySync
	} else if dir == path.Join(internalDirectory, "staging") || strings.HasPrefix(dir, path.Join(internalDirectory, "staging")+"/") {
		step = StepStageDirectorySync
	} else if dir == temporaryDirectory || strings.HasPrefix(dir, temporaryDirectory+"/") {
		step = StepTempCleanupDirectorySync
	} else if dir == path.Join(internalDirectory, "receipts") {
		step = StepReceiptDirectorySync
	} else if dir == path.Join(internalDirectory, "capabilities") {
		step = StepCapabilityDirectorySync
	} else if dir == claimDirectory || strings.HasPrefix(dir, claimDirectory+"/") {
		step = StepClaimDirectorySync
	}
	if err := s.fail(step); err != nil {
		return err
	}
	if s.config.DirectorySync != nil {
		if err := s.config.DirectorySync(dir); err != nil {
			return err
		}
	}
	if err := s.syncDirectory(dir); err != nil {
		return err
	}
	if afterPhysicalSync != nil {
		if err := afterPhysicalSync(); err != nil {
			return err
		}
	}
	return s.postFault(step)
}

func (s *Store) writePrivateDurableAt(target string, data []byte) error {
	return s.writePrivateDurableAtObserved(context.Background(), target, data, nil)
}

func (s *Store) writePrivateDurableAtObserved(ctx context.Context, target string, data []byte, publication *journalPublication) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.mkdirAllContext(ctx, path.Dir(target), 0o700); err != nil {
		return err
	}
	if err := s.writeDurableAtObserved(ctx, target, data, 0o600, publication, nil); err != nil {
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
	err = s.syncFile(f)
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

func (s *Store) mkdirAllContext(ctx context.Context, dir string, mode os.FileMode) error {
	return s.mkdirAllObserved(ctx, dir, mode, nil)
}

func (s *Store) mkdirAllObserved(ctx context.Context, dir string, mode os.FileMode, ownership map[string]os.FileInfo) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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
	for _, name := range created {
		if ownership == nil {
			break
		}
		info, statErr := s.rootFD.Lstat(name)
		if statErr != nil {
			return statErr
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return metadataCorrupt(fmt.Errorf("created private directory changed identity: %q", name))
		}
		ownership[name] = info
	}
	if err := s.postFault(step); err != nil {
		return err
	}
	if strings.HasPrefix(dir, internalDirectory) && (dir == internalDirectory || strings.HasPrefix(dir, internalDirectory+"/")) {
		if err := s.ensurePrivateDirectories(ctx, dir); err != nil {
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

func (s *Store) syncStageDirAt(dir string) error {
	if err := s.fail(StepStageDirectorySync); err != nil {
		return err
	}
	if s.config.DirectorySync != nil {
		if err := s.config.DirectorySync(dir); err != nil {
			return err
		}
	}
	if err := s.syncDirectory(dir); err != nil {
		return err
	}
	return s.postFault(StepStageDirectorySync)
}

func (s *Store) writeDurableAtObserved(ctx context.Context, target string, data []byte, fallbackMode os.FileMode, publication *journalPublication, guarded *mutationTargetIdentity) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !safePath(target) && !strings.HasPrefix(target, internalDirectory+"/") {
		return fmt.Errorf("fs store: unsafe path %q", target)
	}
	dir := path.Dir(target)
	if err := s.mkdirAllContext(ctx, dir, 0o755); err != nil {
		return err
	}
	mode := fallbackMode
	expectedTarget := mutationTargetIdentity{absent: true}
	if info, err := s.fdLstat(target); err == nil {
		mode = info.Mode().Perm()
		expectedTarget = mutationTargetIdentity{info: info}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if guarded != nil {
		expectedTarget = *guarded
	}
	var tmp *os.File
	var tmpName string
	var tmpIdentity os.FileInfo
	var scratchKey artifactClaimKey
	var scratchWitness os.FileInfo
	var scratchBinding os.FileInfo
	renamed := false
	postFaulted := false
	scratchOperation := internalArtifactOperation(target)
	if publication != nil && publication.operation != "" {
		scratchOperation = publication.operation
	}
	for i := 0; i < 100; i++ {
		now := time.Now()
		if s.clockNow != nil {
			now = s.clockNow()
		}
		base, formatErr := formatCanonicalTempArtifactName(now.UnixNano(), uint64(i))
		if formatErr != nil {
			return formatErr
		}
		tmpName = path.Join(temporaryDirectory, base)
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
	defer func() {
		if renamed {
			return
		}
		if tmpIdentity == nil && tmp != nil {
			identity, statErr := tmp.Stat()
			if statErr != nil {
				err = errors.Join(err, statErr, tmp.Close())
				tmp = nil
				return
			}
			tmpIdentity = identity
		}
		if tmp != nil {
			if closeErr := tmp.Close(); closeErr != nil {
				err = errors.Join(err, closeErr)
			}
			tmp = nil
		}
		if postFaulted {
			return
		}
		if scratchKey.Operation == "" && tmpIdentity != nil {
			var keyErr error
			scratchKey, keyErr = newArtifactClaimKey(scratchOperation, tmpName, claimScratch)
			if keyErr != nil {
				err = errors.Join(err, keyErr)
				return
			}
		}
		if cleanupErr := s.cleanupScratchAttempt(scratchKey, tmpName, tmpIdentity, scratchBinding, scratchWitness); cleanupErr != nil {
			err = errors.Join(err, cleanupErr)
		}
	}()
	if err := s.runDescriptorBarrier("scratch_opened"); err != nil {
		return err
	}
	if err := s.runDescriptorBarrier("scratch_before_stat"); err != nil {
		return err
	}
	tmpIdentity, err = tmp.Stat()
	if err != nil {
		return err
	}
	if err := s.runDescriptorBarrier("scratch_stat_ready"); err != nil {
		return err
	}
	scratchKey, err = newArtifactClaimKey(scratchOperation, tmpName, claimScratch)
	if err != nil {
		return err
	}
	preparedWitness, prepareErr := s.prepareRegularWitnessFromObserved(ctx, scratchKey, tmpName, tmpIdentity, func(binding, witness os.FileInfo) {
		if binding != nil {
			scratchBinding = binding
		}
		if witness != nil {
			scratchWitness = witness
		}
	})
	if preparedWitness != nil {
		scratchWitness = preparedWitness
	}
	if prepareErr != nil {
		return prepareErr
	}
	postFault := func(step Step) error {
		err := s.postFault(step)
		if err != nil {
			postFaulted = true
			if publication != nil {
				publication.postFault = true
			}
		}
		return err
	}
	if err := s.fail(s.durableStep(target, StepChmod)); err != nil {
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		return err
	}
	if err := postFault(s.durableStep(target, StepChmod)); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.fail(s.durableStep(target, StepFileWrite)); err != nil {
		return err
	}
	const writeChunk = 64 << 10
	for offset := 0; offset < len(data); {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := offset + writeChunk
		if end > len(data) {
			end = len(data)
		}
		n, writeErr := s.write(tmp, data[offset:end])
		if writeErr != nil {
			return writeErr
		}
		if n != end-offset {
			return io.ErrShortWrite
		}
		offset = end
	}
	if err := postFault(s.durableStep(target, StepFileWrite)); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.fail(s.durableStep(target, StepFileSync)); err != nil {
		return err
	}
	if err := s.syncFile(tmp); err != nil {
		return err
	}
	if err := postFault(s.durableStep(target, StepFileSync)); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.fail(s.durableStep(target, StepFileClose)); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	tmp = nil
	if err := postFault(s.durableStep(target, StepFileClose)); err != nil {
		return err
	}
	ready, readyErr := s.readScratchOwned(ctx, tmpName, int64(len(data)), tmpIdentity)
	if readyErr != nil {
		return readyErr
	}
	if !bytes.Equal(ready, data) {
		return metadataCorrupt(errors.New("scratch content changed before consume"))
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.fail(s.durableStep(target, StepRename)); err != nil {
		return err
	}
	var installedIdentity os.FileInfo
	var visibleState visibleReplacement
	if publication != nil && publication.operation != "" && publication.role != "" {
		key, keyErr := newArtifactClaimKey(publication.operation, target, publication.role)
		if keyErr != nil {
			return keyErr
		}
		installedIdentity, err = s.installOwnedClaimed(ctx, key, tmpName, tmpIdentity, true)
		if installedIdentity != nil {
			renamed = true
			publication.identity = installedIdentity
		}
		if err != nil {
			if artifactSourceRetained(err) {
				postFaulted = true
			}
			return err
		}
	} else if guarded != nil && guarded.operation != "" {
		visibleState, installedIdentity, err = s.installVisibleSource(ctx, guarded.operation, target, tmpName, tmpIdentity, expectedTarget)
		if installedIdentity != nil {
			renamed = true
		}
		if err != nil {
			if artifactSourceRetained(err) {
				postFaulted = true
			}
			return err
		}
	} else {
		installedIdentity, err = s.fdRenameGuarded(tmpName, target, tmpIdentity, expectedTarget)
		if err != nil {
			return err
		}
		renamed = true
	}
	if publication != nil {
		if err := s.runDescriptorBarrier("rename_capture"); err != nil {
			return err
		}
		current, statErr := s.rootFD.Lstat(target)
		err = statErr
		if err != nil {
			return err
		}
		if installedIdentity == nil || !current.Mode().IsRegular() || !os.SameFile(installedIdentity, current) {
			return errMutationTargetChanged(target)
		}
		publication.identity = installedIdentity
	}
	if err := postFault(s.durableStep(target, StepRename)); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Rename crosses from the private scratch directory into the destination.
	// Both namespace changes need one durability acknowledgement.
	if err := s.syncTemporaryDirectory(); err != nil {
		return err
	}
	if dir == temporaryDirectory {
		return nil
	}
	if publication == nil {
		if err := s.syncDirAt(dir); err != nil {
			return err
		}
		if guarded != nil && guarded.operation != "" {
			if err := s.runDescriptorBarrier("visible_parent_synced"); err != nil {
				return err
			}
			if err := s.runDescriptorBarrier("visible_parent_synced:" + target); err != nil {
				return err
			}
		}
	} else {
		if err := s.syncDirAtObserved(dir, func() error {
			publication.durable = true
			current, statErr := s.rootFD.Lstat(target)
			if statErr != nil {
				return statErr
			}
			if !current.Mode().IsRegular() || !os.SameFile(publication.identity, current) {
				return metadataCorrupt(errors.New("journal identity changed before durability boundary"))
			}
			return nil
		}); err != nil {
			if publication.durable {
				publication.postFault = true
			}
			return err
		}
	}
	if scratchWitness != nil {
		if err := s.removeArtifactWitness(scratchKey, scratchWitness); err != nil {
			return err
		}
	}
	return s.cleanupVisibleReplacement(visibleState)
}

func (s *Store) cleanupScratchAttempt(key artifactClaimKey, name string, owned, binding, witness os.FileInfo) error {
	if owned == nil || path.Dir(name) != temporaryDirectory || !strings.HasPrefix(path.Base(name), ".okf-tmp-") {
		return errArtifactClaimConflict
	}
	compensation := scratchAttemptCompensation{store: s, key: key, source: name, owned: owned}
	if witness == nil && binding != nil {
		candidate, witnessErr := s.rootFD.Lstat(key.witnessPath(false))
		if witnessErr == nil {
			if candidate == nil || !candidate.Mode().IsRegular() || !os.SameFile(owned, candidate) {
				return errArtifactClaimConflict
			}
			witness = candidate
		} else if !errors.Is(witnessErr, os.ErrNotExist) {
			return witnessErr
		}
	}
	current, err := s.rootFD.Lstat(name)
	if err == nil {
		if current == nil || !current.Mode().IsRegular() || !os.SameFile(owned, current) {
			return errArtifactClaimConflict
		}
		if binding != nil && witness == nil {
			currentBinding, bindingErr := s.rootFD.Lstat(key.bindingPath())
			if bindingErr != nil || currentBinding == nil || !os.SameFile(binding, currentBinding) {
				return errors.Join(errArtifactClaimConflict, bindingErr)
			}
			if removeErr := compensation.removePartialBinding(currentBinding); removeErr != nil {
				return removeErr
			}
			binding = nil
		}
		if witness == nil {
			var prepareErr error
			witness, prepareErr = compensation.prepareWitness()
			if prepareErr != nil {
				return prepareErr
			}
		}
		if err := compensation.removeAlias(context.Background()); err != nil {
			return err
		}
		if err := s.syncDirectory(temporaryDirectory); err != nil {
			return err
		}
		if err := compensation.removeProof(witness); err != nil {
			return err
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if witness == nil {
		candidate, witnessErr := s.rootFD.Lstat(key.witnessPath(false))
		if witnessErr == nil {
			witness = candidate
		} else if !errors.Is(witnessErr, os.ErrNotExist) {
			return witnessErr
		}
	}
	if witness != nil {
		current, err := s.rootFD.Lstat(key.witnessPath(false))
		if err != nil || current == nil || !os.SameFile(witness, current) {
			return errors.Join(errArtifactClaimConflict, err)
		}
		if err := s.removeArtifactWitness(key, current); err != nil {
			return err
		}
		return nil
	}
	if binding != nil {
		current, err := s.rootFD.Lstat(key.bindingPath())
		if err != nil || current == nil || !os.SameFile(binding, current) {
			return errors.Join(errArtifactClaimConflict, err)
		}
		return s.removeArtifactBinding(key)
	}
	return nil
}

func (s *Store) cleanupTempOwned(operation, name string, owned os.FileInfo) error {
	if path.Dir(name) != temporaryDirectory || !strings.HasPrefix(path.Base(name), ".okf-tmp-") {
		return metadataCorrupt(fmt.Errorf("unsafe temporary cleanup path %q", name))
	}
	if err := s.fail(StepTempCleanupRemove); err != nil {
		return err
	}
	if owned == nil || operation == "" {
		return nil
	}
	key, err := newArtifactClaimKey(operation, name, claimScratch)
	if err != nil {
		return err
	}
	// The scratch writer creates and pins the immutable binding+witness before
	// writing. Cleanup consumes that capability; it must not derive a new one
	// from whatever inode currently occupies the temporary pathname.
	removed, err := s.consumeOwnedClaim(context.Background(), key, false)
	if err != nil {
		return err
	}
	if !removed {
		return nil
	}
	// An unlink has changed the parent directory even when a crash hook fires
	// immediately afterwards.  Do not let an ENOENT retry shortcut skip that
	// durability boundary: the prior unlink may have succeeded before a crash.
	if err := s.postFault(StepTempCleanupRemove); err != nil {
		return err
	}
	return s.syncTemporaryDirectory()
}

func (s *Store) syncTemporaryDirectory() error {
	if err := s.fail(StepTempCleanupDirectorySync); err != nil {
		return err
	}
	if s.config.DirectorySync != nil {
		if err := s.config.DirectorySync(temporaryDirectory); err != nil {
			return err
		}
	}
	if err := s.syncDirectory(temporaryDirectory); err != nil {
		return err
	}
	return s.postFault(StepTempCleanupDirectorySync)
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

// prepareLocked is the sole lazy private-namespace entry point. The caller
// holds s.mu and the root-inode lock, so repair, capability discovery and
// recovery complete before any revision observation or mutation.
func (s *Store) prepareLocked(ctx context.Context) error {
	if !s.privateReady {
		for _, dir := range knownPrivateDirectories() {
			if err := s.mkdirAllContext(ctx, dir, 0o700); err != nil {
				return err
			}
		}
		if err := s.syncKnownPrivateNamespace(ctx); err != nil {
			return err
		}
		if err := s.detectPathAliases(); err != nil {
			return err
		}
	}
	absoluteLimits := s.recoveryLimits
	if absoluteLimits.maxFiles == 0 {
		absoluteLimits = absoluteStagedManifestLimits()
	}
	if err := s.recoverContextWithLimits(ctx, absoluteLimits, configuredStagedManifestLimits(s.config)); err != nil {
		return err
	}
	s.privateReady = true
	return nil
}

func (s *Store) recoverContextWithLimits(ctx context.Context, absoluteLimits, effectiveLimits stagedManifestLimits) error {
	return s.recoverContextWithAllLimits(ctx, absoluteLimits, effectiveLimits, productionJournalManifestByteLimits())
}

func (s *Store) recoverContextWithAllLimits(
	ctx context.Context,
	absoluteLimits stagedManifestLimits,
	effectiveLimits stagedManifestLimits,
	manifestLimits journalManifestByteLimits,
) error {
	callerCtx := ctx
	dir := path.Join(internalDirectory, "transactions")
	if err := s.mkdirAllContext(ctx, dir, 0o700); err != nil {
		return err
	}
	if err := s.compensateUndurableRegularBindings(ctx); err != nil {
		return err
	}
	if err := s.cleanupStageNamespaceClaims(ctx); err != nil {
		return err
	}
	if err := s.restoreClaimInventory(ctx); err != nil {
		return err
	}
	if err := s.recoverJournalClaims(ctx); err != nil {
		return err
	}
	// A PostFault may leave a closed private scratch file, or may rename it out
	// before the scratch directory sync. Resolve that source namespace before
	// journal and provenance checks.
	if err := s.cleanupOrphanTemps(ctx); err != nil {
		return err
	}
	// The journal rename/remove may already be visible after an erroring
	// PostFault while its containing-directory acknowledgement was skipped.
	// Confirm the transaction namespace even when its retry listing is empty.
	if err := s.syncDirAt(dir); err != nil {
		return err
	}
	d, err := openPinnedMetadataDir(s.rootFD, dir)
	if err != nil {
		return err
	}
	if err := s.runDescriptorBarrier("recovery_transactions_readdir"); err != nil {
		return errors.Join(err, d.Close())
	}
	entries, err := d.ReadDir(-1)
	closeErr := d.Close()
	if readErr := errors.Join(err, closeErr); readErr != nil {
		return readErr
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	journalEntries := make([]os.DirEntry, 0, 1)
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
		journalEntries = append(journalEntries, e)
	}
	if len(journalEntries) > 1 {
		return metadataCorrupt(fmt.Errorf("multiple pending journals: %d", len(journalEntries)))
	}
	var lastRecovered *journal
	for _, e := range journalEntries {
		if err := ctx.Err(); err != nil {
			return err
		}
		journalName := path.Join(dir, e.Name())
		listedIdentity, err := s.rootFD.Lstat(journalName)
		if err != nil {
			return err
		}
		journalKey, _, err := s.resolveInstalledRegularArtifact(ctx, journalName, claimJournal, listedIdentity)
		if err != nil {
			return fmt.Errorf("fs store: verify durable journal proof: %w", err)
		}
		raw, journalIdentity, err := s.readRecoveryJournal(ctx, journalName, maxJournalManifestRead)
		if err != nil {
			return err
		}
		if journalIdentity == nil || !os.SameFile(listedIdentity, journalIdentity) {
			return fmt.Errorf("fs store: durable journal proof changed before read: %w", errArtifactClaimConflict)
		}
		if err := validateJournalManifestBytes(raw, manifestLimits.absolute); err != nil {
			return fmt.Errorf("fs store: invalid journal %s: %w", e.Name(), errors.Join(store.ErrStorageCorrupt, err))
		}
		j, err := decodeJournalWithAlgorithmNameAndLimits(raw, s.config.HashAlgorithm, s.hashAlgorithmName, absoluteLimits)
		if err != nil {
			return fmt.Errorf("fs store: invalid journal %s: %w", e.Name(), errors.Join(store.ErrStorageCorrupt, err))
		}
		if journalKey.Operation != j.Receipt.RequestDigest {
			return fmt.Errorf("fs store: durable journal proof operation mismatch: %w", errors.Join(errArtifactClaimConflict, store.ErrStorageCorrupt))
		}
		wantName, pathErr := journalPath(j.Receipt.RequestDigest)
		if pathErr != nil || e.Name() != path.Base(wantName) {
			return fmt.Errorf("fs store: invalid journal %s: %w", e.Name(), store.ErrStorageCorrupt)
		}
		if err := validateStagedManifestLimits(j.Files, effectiveLimits.maxFiles, effectiveLimits.maxPayloadBytes, effectiveLimits.maxTransactionBytes); err != nil {
			return fmt.Errorf("fs store: invalid journal %s: %w", e.Name(), errors.Join(store.ErrStorageCorrupt, fmt.Errorf("staged manifest exceeds configured limit: %w", err)))
		}
		if err := validateJournalManifestBytes(raw, manifestLimits.effective); err != nil {
			return fmt.Errorf("fs store: invalid journal %s: %w", e.Name(), errors.Join(store.ErrStorageCorrupt, fmt.Errorf("journal manifest exceeds configured limit: %w", err)))
		}
		if err := s.restoreVisibleClaimsForJournal(j); err != nil {
			return newRecoveryOutcomeError(j, err)
		}
		// Alias and visible provenance are caller-cancellable semantic gates.
		// They precede ownership restoration and every staged payload open.
		if err := s.validateJournalFilesystemPathAliasTransition(j.Base, j.Files); err != nil {
			return metadataCorrupt(err)
		}
		if err := s.validateCurrentJournalProvenance(ctx, j.Base, j.Files); err != nil {
			return err
		}
		if err := s.convergeVisibleVacancyClaims(ctx, j); err != nil {
			return newRecoveryOutcomeError(j, err)
		}
		if err := s.convergeVisibleInstalledClaims(ctx, j); err != nil {
			return newRecoveryOutcomeError(j, err)
		}
		// Metadata, canonical name and both absolute/effective limits establish
		// ownership of a valid durable promise. Cancellation before this point
		// leaves evidence untouched; after it, recovery must converge while
		// retaining caller context values.
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.resumeRegularBindingsForJournal(ctx, j); err != nil {
			return newRecoveryOutcomeError(j, err)
		}
		if err := s.restoreStageClaims(j); err != nil {
			return newRecoveryOutcomeError(j, err)
		}
		stageOwnership, err := s.captureStageDirectoryOwnership(ctx, j)
		if err != nil {
			return newRecoveryOutcomeError(j, err)
		}
		if err := s.runDescriptorBarrier("recovery_ownership_validated"); err != nil {
			return newRecoveryOutcomeError(j, err)
		}
		recoveryCtx := context.WithoutCancel(ctx)
		ctx = recoveryCtx
		if _, err := s.applyWithOwnership(recoveryCtx, j, &stageOwnership); err != nil {
			return newRecoveryOutcomeError(j, fmt.Errorf("apply durable journal: %w", err))
		}
		if err := s.cleanupVisibleClaims(j); err != nil {
			return newRecoveryOutcomeError(j, fmt.Errorf("cleanup visible claim inventory: %w", err))
		}
		if err := s.cleanupScratchInventoryOperation(recoveryCtx, j.Receipt.RequestDigest); err != nil {
			return newRecoveryOutcomeError(j, fmt.Errorf("cleanup scratch claim inventory: %w", err))
		}
		if j.Receipt.IdempotencyKey != "" {
			if err := s.writeReceiptWithReplay(j.Receipt, replayOrZero(j.Replay)); err != nil {
				return newRecoveryOutcomeError(j, err)
			}
			if err := s.pruneReceipts(); err != nil {
				return newRecoveryOutcomeError(j, err)
			}
		}
		if err := s.removeOwnedDurableJournal(j.Receipt.RequestDigest, path.Join(dir, e.Name()), journalIdentity); err != nil {
			return newRecoveryOutcomeError(j, err)
		}
		if err := s.cleanupVisibleInstalledClaims(recoveryCtx, j); err != nil {
			return newRecoveryOutcomeError(j, err)
		}
		if err := s.cleanupOwnedStageStrict(j, stageOwnership); err != nil {
			return newRecoveryOutcomeError(j, err)
		}
		recovered := j
		lastRecovered = &recovered
	}
	if err := s.cleanupVisibleInstalledInventory(ctx); err != nil {
		if lastRecovered != nil {
			return newRecoveryOutcomeError(*lastRecovered, err)
		}
		return err
	}
	if err := s.cleanupScratchInventory(ctx); err != nil {
		if lastRecovered != nil {
			return newRecoveryOutcomeError(*lastRecovered, err)
		}
		return err
	}
	if err := s.cleanupOrphanStages(ctx); err != nil {
		if lastRecovered != nil {
			return newRecoveryOutcomeError(*lastRecovered, err)
		}
		return err
	}
	if lastRecovered != nil && callerCtx.Err() != nil {
		return newRecoveryOutcomeError(*lastRecovered, callerCtx.Err())
	}
	return nil
}

// cleanupOrphanTemps is confined to the private scratch directory. Public
// revision paths are opaque even when their base names resemble scratch files.
// Ordinary entries, directories, symlinks, and special files are preserved.
func (s *Store) cleanupOrphanTemps(ctx context.Context) error {
	d, err := openPinnedMetadataDir(s.rootFD, temporaryDirectory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := s.runDescriptorBarrier("orphan_temporary_readdir"); err != nil {
		return errors.Join(err, d.Close())
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
		name := path.Join(temporaryDirectory, entry.Name())
		info, err := s.rootFD.Lstat(name)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		// Basenames are not ownership. Closed binding inventory is consumed by
		// cleanupScratchInventory before this scan; everything else is preserved.
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// A prior rename-out may have succeeded before its source directory sync.
	// Even an empty scratch directory therefore needs one recovery barrier.
	return s.syncTemporaryDirectory()
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
	if err := s.runDescriptorBarrier("orphan_stage_readdir"); err != nil {
		return errors.Join(err, d.Close())
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
		stageKey, keyErr := s.directorySentinelKey(ctx, stageDir)
		if errors.Is(keyErr, os.ErrNotExist) {
			// Legacy/unproven directories are inert and intentionally preserved.
			continue
		}
		if keyErr != nil {
			return keyErr
		}
		payloadDir := path.Join(root, entry.Name(), "payload")
		pd, err := openPinnedMetadataDir(s.rootFD, payloadDir)
		stageDir = path.Dir(payloadDir)
		// A crash may have happened after payload rmdir but before the parent
		// fsync. That is an expected partial cleanup state, not corruption.
		if errors.Is(err, os.ErrNotExist) {
			removed, removeErr := s.consumeOwnedClaim(ctx, stageKey, true)
			if removeErr != nil || !removed {
				return errors.Join(errArtifactClaimConflict, removeErr)
			}
			if err := s.postFault(StepStageCleanupStageDirRemove); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if err := s.runDescriptorBarrier("orphan_payload_readdir"); err != nil {
			return errors.Join(err, pd.Close())
		}
		payloadKey, err := s.directorySentinelKey(ctx, payloadDir)
		if err != nil || payloadKey.Operation != stageKey.Operation {
			return errors.Join(errArtifactClaimConflict, err)
		}
		payloads, readErr := pd.ReadDir(-1)
		closeErr := pd.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return err
		}
		sort.Slice(payloads, func(i, j int) bool { return payloads[i].Name() < payloads[j].Name() })
		hasSpecialPayload := false
		for _, payload := range payloads {
			if payload.Name() == directorySentinelName {
				continue
			}
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
			payloadClaim, err := newArtifactClaimKey(stageKey.Operation, payloadName, claimPayload)
			if err != nil {
				return err
			}
			removed, err := s.consumeOwnedClaim(ctx, payloadClaim, false)
			if err != nil {
				return err
			}
			if !removed {
				hasSpecialPayload = true
				continue
			}
			if err := s.postFault(StepStageCleanupPayloadRemove); err != nil {
				return err
			}
		}
		// One barrier per visited payload directory covers both removals made in
		// this pass and removals that are already absent after a prior
		// PostFault. It must precede either preserving special entries or rmdir.
		if err := s.cleanupSyncDir(payloadDir, StepStageCleanupPayloadDirectory); err != nil {
			return err
		}
		if hasSpecialPayload {
			// Keep an unowned directory containing special files untouched. It
			// cannot affect revision state and attempting rmdir would turn a
			// harmless FIFO/device into a failed public observation.
			continue
		}
		removed, err := s.consumeOwnedClaim(ctx, payloadKey, true)
		if err != nil || !removed {
			return errors.Join(errArtifactClaimConflict, err)
		}
		if err := s.postFault(StepStageCleanupPayloadDirRemove); err != nil {
			return err
		}
		removed, err = s.consumeOwnedClaim(ctx, stageKey, true)
		if err != nil || !removed {
			return errors.Join(errArtifactClaimConflict, err)
		}
		if err := s.postFault(StepStageCleanupStageDirRemove); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// An interrupted stage-directory removal is absent from the retry listing.
	// One pass-level root barrier therefore closes every such namespace delta,
	// including the zero-entry case.
	return s.cleanupSyncDir(root, StepStageCleanupRootDirectory)
}

func (s *Store) acquire(ctx context.Context) (func(), error) {
	deadline := time.Now().Add(s.config.LeaseTimeout)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		f, err := openRootLockDescriptor(s.dirFD)
		if err != nil {
			return nil, err
		}
		err = lockExclusive(f)
		if err == nil {
			verify := s.rootLockIdentity
			if verify == nil {
				verify = verifyRootLockIdentity
			}
			if err := verify(s.dirFD, f); err != nil {
				return nil, errors.Join(err, unlockFile(f), f.Close())
			}
			return func() { _ = unlockFile(f); _ = f.Close() }, nil
		}
		_ = f.Close()
		if !rootLockRetryable(err) {
			return nil, fmt.Errorf("fs store: acquire root lock: %w", err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("fs store: root lock timeout: %w", context.DeadlineExceeded)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func verifyRootLockIdentity(root, lock *os.File) error {
	want, err := root.Stat()
	if err != nil {
		return err
	}
	got, err := lock.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(want, got) {
		return fmt.Errorf("fs store: root lock inode changed: %w", store.ErrStorageCorrupt)
	}
	return nil
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
func (s *Store) lookupReceiptContext(ctx context.Context, key store.IdempotencyKey, digest string) (store.CommitReceipt, bool, error) {
	f, found, err := s.lookupReceiptFileContext(ctx, key, digest)
	if err != nil || !found {
		return store.CommitReceipt{}, found, err
	}
	return f.Receipt.Clone(), true, nil
}

func (s *Store) lookupReceiptFileContext(ctx context.Context, key store.IdempotencyKey, digest string) (receiptFile, bool, error) {
	if err := ctx.Err(); err != nil {
		return receiptFile{}, false, err
	}
	raw, err := s.readMetadataLimitObserved(ctx, s.receiptPath(key), maxMetadataRead)
	if errors.Is(err, os.ErrNotExist) {
		return receiptFile{}, false, nil
	}
	if err != nil {
		return receiptFile{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return receiptFile{}, false, err
	}
	f, err := decodeReceiptFile(raw)
	if err != nil {
		return receiptFile{}, false, fmt.Errorf("fs store: receipt: %w", errors.Join(store.ErrStorageCorrupt, err))
	}
	if err := ctx.Err(); err != nil {
		return receiptFile{}, false, err
	}
	if f.Key != string(key) {
		return receiptFile{}, false, fmt.Errorf("fs store: receipt: %w", store.ErrStorageCorrupt)
	}
	if f.Digest != digest {
		return receiptFile{}, false, &store.IdempotencyConflict{Key: key, PreviousDigest: f.Digest, RequestDigest: digest}
	}
	return f, true, nil
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

func (s *Store) replaceReplayResultContext(ctx context.Context, key store.IdempotencyKey, digest string, receipt store.CommitReceipt) (ReplaceConceptResult, error) {
	f, found, err := s.lookupReceiptFileContext(ctx, key, digest)
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
// root lock is held (or during lazy first-observation recovery).
func (s *Store) pruneReceipts() error {
	if err := s.fail(StepReceiptPrune); err != nil {
		return err
	}
	if err := s.cleanupReceiptPruneInventory(context.Background()); err != nil {
		return err
	}
	dir := path.Join(internalDirectory, "receipts")
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
	type item struct {
		name      string
		at        time.Time
		operation string
		identity  os.FileInfo
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
		raw, err := s.readMetadataLimitOwned(context.Background(), receiptName, maxMetadataRead, info)
		if err != nil {
			return err
		}
		receipt, err := decodeReceiptFile(raw)
		if err != nil {
			return fmt.Errorf("fs store: receipt: %w", errors.Join(store.ErrStorageCorrupt, err))
		}
		if s.receiptPath(store.IdempotencyKey(receipt.Key)) != receiptName {
			return errors.Join(errArtifactClaimConflict, metadataCorrupt(fmt.Errorf("receipt filename does not match envelope")))
		}
		items = append(items, item{name: entry.Name(), at: receipt.Receipt.CommitTime, operation: receiptPruneOperation(receiptName, raw), identity: info})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].at.Equal(items[j].at) {
			return items[i].name > items[j].name
		}
		return items[i].at.After(items[j].at)
	})
	cutoff := time.Now().UTC().Add(-s.config.ReceiptRetention)
	var pruneItems []item
	for i := s.config.MinimumReceipts; i < len(items); i++ {
		if items[i].at.After(cutoff) {
			continue
		}
		pruneItems = append(pruneItems, items[i])
	}
	sort.Slice(pruneItems, func(i, j int) bool { return pruneItems[i].name < pruneItems[j].name })
	for _, item := range pruneItems {
		if err := s.removeReceiptForPrune(context.Background(), path.Join(dir, item.name), item.operation, item.identity); err != nil {
			return err
		}
	}
	// A prior receipt unlink may already be absent from this retry's listing.
	// Always acknowledge the containing namespace before journal removal.
	if err := s.syncDirAt(dir); err != nil {
		return err
	}
	if err := s.runDescriptorBarrier("receipt_prune_batch_synced"); err != nil {
		return err
	}
	if len(pruneItems) > 0 {
		return s.postFault(StepReceiptPrune)
	}
	return nil
}

func receiptPruneOperation(name string, raw []byte) string {
	h := sha256.New()
	_, _ = h.Write([]byte("okf:receipt-prune:v1\x00"))
	_, _ = h.Write([]byte(path.Clean(name)))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(raw)
	return "receipt:" + hex.EncodeToString(h.Sum(nil))
}

func (s *Store) removeReceiptForPrune(ctx context.Context, name, operation string, identity os.FileInfo) error {
	key, err := newArtifactClaimKey(operation, name, claimReceipt)
	if err != nil {
		return err
	}
	var binding, witness os.FileInfo
	prepared, prepareErr := s.prepareRegularWitnessFromObserved(ctx, key, name, identity, func(gotBinding, gotWitness os.FileInfo) {
		if gotBinding != nil {
			binding = gotBinding
		}
		if gotWitness != nil {
			witness = gotWitness
		}
	})
	if prepared != nil {
		witness = prepared
	}
	if prepareErr != nil {
		if artifactSourceCrash(prepareErr) {
			return prepareErr
		}
		return errors.Join(prepareErr, s.cleanupReceiptAttemptProof(key, identity, binding, witness))
	}
	if err := s.fail(StepRemove); err != nil {
		return err
	}
	removed, err := s.consumeOwnedClaim(ctx, key, false)
	if err != nil || !removed {
		return errors.Join(errArtifactClaimConflict, err)
	}
	return s.postFault(StepRemove)
}

func (s *Store) cleanupReceiptAttemptProof(key artifactClaimKey, receipt, binding, witness os.FileInfo) error {
	if receipt == nil {
		return errArtifactClaimConflict
	}
	if witness == nil && binding != nil {
		candidate, err := s.rootFD.Lstat(key.witnessPath(false))
		if err == nil {
			if candidate == nil || !candidate.Mode().IsRegular() || !os.SameFile(receipt, candidate) {
				return errArtifactClaimConflict
			}
			witness = candidate
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if witness != nil {
		current, err := s.rootFD.Lstat(key.witnessPath(false))
		if err != nil || current == nil || !os.SameFile(witness, current) {
			return errors.Join(errArtifactClaimConflict, err)
		}
		removed, err := s.fdRemoveClaimOwned(claimZonePath(key.witnessPath(false)), current, false)
		if err != nil || !removed {
			return errors.Join(errArtifactClaimConflict, err)
		}
		if err := s.syncDirAt(claimDirectory); err != nil {
			return err
		}
	}
	if binding != nil {
		current, err := s.rootFD.Lstat(key.bindingPath())
		if err != nil || current == nil || !os.SameFile(binding, current) {
			return errors.Join(errArtifactClaimConflict, err)
		}
		removed, err := s.fdRemoveClaimOwned(claimZonePath(key.bindingPath()), current, false)
		if err != nil || !removed {
			return errors.Join(errArtifactClaimConflict, err)
		}
		return s.syncDirAt(claimDirectory)
	}
	return nil
}

var _ store.Store = (*Store)(nil)
var _ store.Snapshot = (*snapshot)(nil)
