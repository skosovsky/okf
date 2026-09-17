package fs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/skosovsky/okf/store"
)

const claimDirectory = internalDirectory + "/claims"
const directorySentinelName = ".okf-claim-sentinel.json"

type artifactRole string

const (
	claimJournal        artifactRole = "journal"
	claimPayload        artifactRole = "payload"
	claimStageDir       artifactRole = "stage-dir"
	claimScratch        artifactRole = "scratch"
	claimVisible        artifactRole = "visible"
	claimVisibleInstall artifactRole = "visible-installed"
	claimDirectoryRole  artifactRole = "directory"
	claimCapability     artifactRole = "capability"
	claimReceipt        artifactRole = "receipt"
)

var errArtifactClaimConflict = errors.New("fs store: artifact claim conflict")

type artifactClaimKey struct {
	Operation string
	Path      string
	Role      artifactRole
}

type directoryWitnessRecord struct {
	Version   uint16       `json:"version"`
	Operation string       `json:"operation"`
	Path      string       `json:"path"`
	Role      artifactRole `json:"role"`
	Identity  string       `json:"identity"`
	Source    string       `json:"source,omitempty"`
}

type artifactSourceRetainedError struct {
	cause     error
	crashLike bool
}

func (e *artifactSourceRetainedError) Error() string { return e.cause.Error() }
func (e *artifactSourceRetainedError) Unwrap() error { return e.cause }

func retainArtifactSource(err error) error {
	if err == nil {
		return nil
	}
	var retained *artifactSourceRetainedError
	if errors.As(err, &retained) {
		return err
	}
	return &artifactSourceRetainedError{cause: err}
}

func retainArtifactSourceCrash(err error) error {
	if err == nil {
		return nil
	}
	return &artifactSourceRetainedError{cause: err, crashLike: true}
}

func artifactSourceRetained(err error) bool {
	var retained *artifactSourceRetainedError
	return errors.As(err, &retained)
}

func artifactSourceCrash(err error) bool {
	var retained *artifactSourceRetainedError
	return errors.As(err, &retained) && retained.crashLike
}

func validArtifactRole(role artifactRole) bool {
	switch role {
	case claimJournal, claimPayload, claimStageDir, claimScratch, claimVisible, claimVisibleInstall, claimDirectoryRole, claimCapability, claimReceipt:
		return true
	default:
		return false
	}
}

func newArtifactClaimKey(operation, name string, role artifactRole) (artifactClaimKey, error) {
	if operation == "" || len(operation) > 256 || !safeClaimSourcePath(name) || !validArtifactRole(role) {
		return artifactClaimKey{}, metadataCorrupt(fmt.Errorf("invalid artifact claim binding"))
	}
	return artifactClaimKey{Operation: operation, Path: path.Clean(name), Role: role}, nil
}

func safeClaimSourcePath(name string) bool {
	clean := path.Clean(name)
	return clean != "." && clean != "" && !strings.HasPrefix(clean, "/") && !strings.HasPrefix(clean, "../") && !strings.Contains(clean, "//")
}

func (k artifactClaimKey) stem() string {
	h := sha256.Sum256([]byte("okf:artifact-claim:v1\x00" + k.Operation + "\x00" + k.Path + "\x00" + string(k.Role)))
	return hex.EncodeToString(h[:])
}

func (k artifactClaimKey) claimPath() string { return path.Join(claimDirectory, k.stem()+".claim") }
func (k artifactClaimKey) witnessPath(dir bool) string {
	if dir {
		return path.Join(k.Path, directorySentinelName)
	}
	return path.Join(claimDirectory, k.stem()+".witness")
}
func (k artifactClaimKey) claimedDirectoryWitnessPath() string {
	return path.Join(k.claimPath(), directorySentinelName)
}
func (k artifactClaimKey) bindingPath() string {
	return path.Join(claimDirectory, k.stem()+".binding.json")
}

func internalArtifactOperation(name string) string {
	h := sha256.Sum256([]byte("okf:internal-operation:v1\x00" + path.Clean(name)))
	return "internal:" + hex.EncodeToString(h[:])
}

func (s *Store) ensureClaimNamespace(ctx context.Context) error {
	if err := s.mkdirAllContext(ctx, claimDirectory, 0o700); err != nil {
		return err
	}
	return s.syncDirAt(claimDirectory)
}

func (s *Store) syncClaimProtocolParent(name string) error {
	clean := path.Clean(name)
	if clean == temporaryDirectory || strings.HasPrefix(clean, temporaryDirectory+"/") {
		return s.syncTemporaryDirectory()
	}
	return s.syncDirAt(path.Dir(clean))
}

func (s *Store) prepareArtifactWitness(ctx context.Context, key artifactClaimKey, expected fs.FileInfo, wantDir bool) (fs.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if expected == nil || expected.IsDir() != wantDir {
		return nil, metadataCorrupt(errors.New("artifact witness type mismatch"))
	}
	if err := s.ensureClaimNamespace(ctx); err != nil {
		return nil, err
	}
	if !wantDir {
		return s.prepareRegularWitnessFrom(ctx, key, key.Path, expected)
	}
	return s.prepareDirectoryWitnessAt(ctx, key, key.Path, expected)
}

func (s *Store) prepareDirectoryWitnessAt(ctx context.Context, key artifactClaimKey, directory string, expected fs.FileInfo) (fs.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if expected == nil || !expected.IsDir() {
		return nil, errArtifactClaimConflict
	}
	identity, ok := fileIdentityKey(expected)
	if !ok {
		return nil, errors.Join(errArtifactClaimConflict, errors.New("directory identity is not persistable"))
	}
	record := directoryWitnessRecord{Version: 1, Operation: key.Operation, Path: key.Path, Role: key.Role, Identity: identity}
	// The external immutable binding is the durable capability. It must exist
	// before the in-directory sentinel can make a directory publishable.
	if err := s.ensureBindingRecord(ctx, key.bindingPath(), record); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	witnessName := path.Join(directory, directorySentinelName)
	f, createErr := s.fdOpen(witnessName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if createErr == nil {
		if _, err := s.write(f, raw); err != nil {
			return nil, errors.Join(err, f.Close())
		}
		if err := s.syncFile(f); err != nil {
			return nil, errors.Join(err, f.Close())
		}
		if err := f.Close(); err != nil {
			return nil, err
		}
		if err := s.syncDirAt(directory); err != nil {
			return nil, err
		}
	} else if !errors.Is(createErr, os.ErrExist) {
		return nil, createErr
	}
	stored, err := s.readMetadataLimitObserved(ctx, witnessName, maxJournalManifestRead)
	if err != nil {
		return nil, err
	}
	got, err := decodeDirectoryWitness(stored)
	if err != nil || got != record {
		return nil, errors.Join(errArtifactClaimConflict, err, metadataCorrupt(errors.New("directory witness binding mismatch")))
	}
	return s.rootFD.Lstat(witnessName)
}

func decodeDirectoryWitness(raw []byte) (directoryWitnessRecord, error) {
	var record directoryWitnessRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return record, errors.Join(errArtifactClaimConflict, metadataCorrupt(err))
	}
	canonical, err := json.Marshal(record)
	if err != nil || !json.Valid(raw) || string(canonical) != string(raw) || record.Version != 1 || !validArtifactRole(record.Role) || !safeClaimSourcePath(record.Path) || record.Operation == "" || record.Identity == "" || record.Source != "" && !safeClaimSourcePath(record.Source) {
		return record, errors.Join(errArtifactClaimConflict, metadataCorrupt(errors.New("invalid directory witness")), err)
	}
	return record, nil
}

func (s *Store) verifyDirectoryWitnessAt(ctx context.Context, key artifactClaimKey, directory string, expected fs.FileInfo) (fs.FileInfo, error) {
	return s.verifyArtifactProofAt(ctx, key, directory, expected, true)
}

func (s *Store) prepareRegularWitnessFrom(ctx context.Context, key artifactClaimKey, source string, expected fs.FileInfo) (fs.FileInfo, error) {
	return s.prepareRegularWitnessFromObserved(ctx, key, source, expected, nil)
}

func (s *Store) prepareRegularWitnessFromObserved(ctx context.Context, key artifactClaimKey, source string, expected fs.FileInfo, observe func(binding, witness fs.FileInfo)) (fs.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.ensureClaimNamespace(ctx); err != nil {
		return nil, err
	}
	identity, ok := fileIdentityKey(expected)
	if !ok || !safeClaimSourcePath(source) {
		return nil, errArtifactClaimConflict
	}
	current, sourceErr := s.rootFD.Lstat(source)
	if sourceErr != nil || current == nil || !current.Mode().IsRegular() || !os.SameFile(expected, current) {
		return nil, errors.Join(errArtifactClaimConflict, sourceErr)
	}
	record := directoryWitnessRecord{Version: 1, Operation: key.Operation, Path: key.Path, Role: key.Role, Identity: identity, Source: path.Clean(source)}
	// The self-describing binding is durable before the hard-link witness can
	// become visible. Recovery can therefore resume every factual state; an
	// unbound witness is never created by this protocol.
	binding, err := s.ensureBindingRecordObserved(ctx, key.bindingPath(), record)
	if observe != nil {
		observe(binding, nil)
	}
	if err != nil {
		return nil, retainArtifactSource(err)
	}
	if err := s.runDescriptorBarrier("artifact_binding_ready:" + string(key.Role)); err != nil {
		return nil, retainArtifactSource(err)
	}
	witnessName := key.witnessPath(false)
	err = s.fdLinkOwnedNoReplace(source, witnessName, expected)
	if err != nil && !errors.Is(err, os.ErrExist) {
		return nil, retainArtifactSource(err)
	}
	if err == nil {
		if barrierErr := s.runDescriptorBarrier("witness_after_link"); barrierErr != nil {
			return nil, retainArtifactSource(barrierErr)
		}
	}
	witness, statErr := s.rootFD.Lstat(witnessName)
	if observe != nil {
		observe(binding, witness)
	}
	if statErr != nil || witness == nil || !witness.Mode().IsRegular() || !os.SameFile(expected, witness) {
		return nil, retainArtifactSource(errors.Join(errArtifactClaimConflict, statErr))
	}
	current, sourceErr = s.rootFD.Lstat(source)
	if sourceErr != nil || current == nil || !current.Mode().IsRegular() || !os.SameFile(expected, current) {
		return nil, retainArtifactSource(errors.Join(errArtifactClaimConflict, sourceErr))
	}
	if err == nil {
		physicalSynced := false
		if syncErr := s.syncDirAtObserved(claimDirectory, func() error { physicalSynced = true; return nil }); syncErr != nil {
			if physicalSynced {
				return nil, retainArtifactSourceCrash(syncErr)
			}
			return nil, retainArtifactSource(syncErr)
		}
	}
	if err := s.runDescriptorBarrier("artifact_witness_ready:" + string(key.Role)); err != nil {
		return nil, retainArtifactSource(err)
	}
	return witness, nil
}

func (s *Store) ensureBindingRecord(ctx context.Context, name string, record directoryWitnessRecord) error {
	_, err := s.ensureBindingRecordObserved(ctx, name, record)
	return err
}

// prepareRegularWitnessForCompensation closes attempt-local proof without
// re-entering publication fault seams. The caller has already pinned and
// revalidated the exact source inode.
func (c scratchAttemptCompensation) prepareWitness() (fs.FileInfo, error) {
	s, key, source, expected := c.store, c.key, c.source, c.owned
	if err := s.ensureClaimNamespace(context.Background()); err != nil {
		return nil, err
	}
	identity, ok := fileIdentityKey(expected)
	if !ok || !safeClaimSourcePath(source) {
		return nil, errArtifactClaimConflict
	}
	current, err := s.rootFD.Lstat(source)
	if err != nil || current == nil || !current.Mode().IsRegular() || !os.SameFile(expected, current) {
		return nil, errors.Join(errArtifactClaimConflict, err)
	}
	record := directoryWitnessRecord{Version: 1, Operation: key.Operation, Path: key.Path, Role: key.Role, Identity: identity, Source: path.Clean(source)}
	raw, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	f, err := s.fdOpen(key.bindingPath(), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	binding, writeErr := f.Stat()
	if writeErr == nil {
		_, writeErr = s.write(f, raw)
	}
	if writeErr == nil {
		writeErr = s.syncFile(f)
	}
	if err := errors.Join(writeErr, f.Close()); err != nil {
		return nil, err
	}
	if err := s.syncDirectory(claimDirectory); err != nil {
		return nil, err
	}
	if err := s.fdLinkOwnedNoReplaceMode(source, key.witnessPath(false), expected, false); err != nil {
		return nil, err
	}
	if err := s.syncDirectory(claimDirectory); err != nil {
		return nil, err
	}
	witness, err := s.rootFD.Lstat(key.witnessPath(false))
	if err != nil || witness == nil || binding == nil || !os.SameFile(expected, witness) {
		return nil, errors.Join(errArtifactClaimConflict, err)
	}
	return witness, nil
}

func (s *Store) ensureBindingRecordObserved(ctx context.Context, name string, record directoryWitnessRecord) (fs.FileInfo, error) {
	raw, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	f, createErr := s.fdOpen(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if createErr == nil {
		if err := s.runDescriptorBarrier("artifact_binding_opened:" + string(record.Role)); err != nil {
			identity, statErr := f.Stat()
			return identity, errors.Join(err, statErr, f.Close())
		}
		identity, statErr := f.Stat()
		if statErr != nil {
			return nil, errors.Join(statErr, f.Close())
		}
		if err := s.runDescriptorBarrier("artifact_binding_stat_ready:" + string(record.Role)); err != nil {
			return identity, errors.Join(err, f.Close())
		}
		if _, err := s.write(f, raw); err != nil {
			return identity, errors.Join(err, f.Close())
		}
		if err := s.runDescriptorBarrier("artifact_binding_written:" + string(record.Role)); err != nil {
			return identity, errors.Join(err, f.Close())
		}
		if err := s.syncFile(f); err != nil {
			return identity, errors.Join(err, f.Close())
		}
		if err := s.runDescriptorBarrier("artifact_binding_synced:" + string(record.Role)); err != nil {
			return identity, errors.Join(err, f.Close())
		}
		if err := f.Close(); err != nil {
			return identity, err
		}
		if err := s.runDescriptorBarrier("artifact_binding_closed:" + string(record.Role)); err != nil {
			return identity, err
		}
		physicalSynced := false
		syncErr := s.syncDirAtObserved(claimDirectory, func() error { physicalSynced = true; return nil })
		if syncErr != nil && physicalSynced {
			return identity, retainArtifactSourceCrash(syncErr)
		}
		return identity, syncErr
	}
	if !errors.Is(createErr, os.ErrExist) {
		return nil, createErr
	}
	stored, err := s.readMetadataLimitObserved(ctx, name, maxJournalManifestRead)
	if err != nil {
		return nil, err
	}
	var got directoryWitnessRecord
	if json.Unmarshal(stored, &got) != nil || got != record {
		return nil, errArtifactClaimConflict
	}
	canonical, _ := json.Marshal(got)
	if string(canonical) != string(stored) {
		return nil, errArtifactClaimConflict
	}
	identity, err := s.rootFD.Lstat(name)
	if err != nil || identity == nil || !identity.Mode().IsRegular() {
		return identity, errors.Join(errArtifactClaimConflict, err)
	}
	return identity, nil
}

func (s *Store) verifyArtifactClaim(ctx context.Context, key artifactClaimKey, expected fs.FileInfo, wantDir bool) (fs.FileInfo, error) {
	return s.verifyArtifactProofAt(ctx, key, key.claimPath(), expected, wantDir)
}

func (s *Store) verifyArtifactProofAt(ctx context.Context, key artifactClaimKey, name string, expected fs.FileInfo, wantDir bool) (fs.FileInfo, error) {
	claim, err := s.rootFD.Lstat(name)
	if err != nil || claim == nil || claim.IsDir() != wantDir || !os.SameFile(expected, claim) {
		return claim, errors.Join(errArtifactClaimConflict, err)
	}
	identity, ok := fileIdentityKey(claim)
	if !ok {
		return claim, errors.Join(errArtifactClaimConflict, errors.New("claim identity unavailable"))
	}
	bindingRaw, err := s.readMetadataLimitObserved(ctx, key.bindingPath(), maxJournalManifestRead)
	if err != nil {
		return claim, errors.Join(errArtifactClaimConflict, err)
	}
	binding, err := decodeDirectoryWitness(bindingRaw)
	if err != nil || binding.Operation != key.Operation || binding.Path != key.Path || binding.Role != key.Role || binding.Identity != identity {
		return claim, errors.Join(errArtifactClaimConflict, err)
	}
	if !wantDir {
		if binding.Source == "" {
			return claim, errArtifactClaimConflict
		}
		witness, err := s.rootFD.Lstat(key.witnessPath(false))
		if err != nil || witness == nil || !os.SameFile(claim, witness) {
			return claim, errors.Join(errArtifactClaimConflict, err)
		}
		return claim, nil
	}
	if binding.Source != "" {
		return claim, errors.Join(errArtifactClaimConflict, metadataCorrupt(errors.New("directory binding has a source")))
	}
	raw, err := s.readMetadataLimitObserved(ctx, path.Join(name, directorySentinelName), maxJournalManifestRead)
	if err != nil {
		return claim, errors.Join(errArtifactClaimConflict, err)
	}
	var record directoryWitnessRecord
	if json.Unmarshal(raw, &record) != nil || record != binding {
		return claim, errors.Join(errArtifactClaimConflict, metadataCorrupt(errors.New("directory claim witness mismatch")))
	}
	canonical, _ := json.Marshal(record)
	if string(canonical) != string(raw) {
		return claim, errors.Join(errArtifactClaimConflict, metadataCorrupt(errors.New("non-canonical directory claim witness")))
	}
	return claim, nil
}

func canonicalRegularArtifactSource(name string) bool {
	clean := path.Clean(name)
	if clean != name || path.Dir(clean) != temporaryDirectory {
		return false
	}
	_, _, ok := parseCanonicalTempArtifactName(path.Base(clean))
	return ok
}

func formatCanonicalTempArtifactName(nanos int64, attempt uint64) (string, error) {
	if nanos <= 0 || attempt >= 100 {
		return "", errors.New("fs store: invalid temporary file identity")
	}
	return ".okf-tmp-" + strconv.FormatInt(nanos, 10) + "-" + strconv.FormatUint(attempt, 10), nil
}

func parseCanonicalTempArtifactName(base string) (int64, uint64, bool) {
	if !strings.HasPrefix(base, ".okf-tmp-") {
		return 0, 0, false
	}
	parts := strings.Split(strings.TrimPrefix(base, ".okf-tmp-"), "-")
	if len(parts) != 2 {
		return 0, 0, false
	}
	nanos, nanosErr := strconv.ParseInt(parts[0], 10, 64)
	attempt, attemptErr := strconv.ParseUint(parts[1], 10, 64)
	if nanosErr != nil || attemptErr != nil || nanos <= 0 || attempt >= 100 || strconv.FormatInt(nanos, 10) != parts[0] || strconv.FormatUint(attempt, 10) != parts[1] {
		return 0, 0, false
	}
	return nanos, attempt, true
}

// validatePrivateArtifactRole closes the pathname semantics for private
// regular-artifact bindings before recovery consults any pathname identity.
// A syntactically valid binding is not ownership evidence when its role,
// operation, target, or source could name a different protocol artifact.
func validatePrivateArtifactRole(record directoryWitnessRecord) (artifactClaimKey, error) {
	if record.Source == "" || !canonicalRegularArtifactSource(record.Source) {
		return artifactClaimKey{}, errArtifactClaimConflict
	}
	switch record.Role {
	case claimJournal:
		want, err := journalPath(record.Operation)
		if err != nil || record.Path != want {
			return artifactClaimKey{}, errors.Join(errArtifactClaimConflict, err)
		}
	case claimPayload:
		wantDir, err := journalStage(record.Operation)
		if err != nil || path.Dir(record.Path) != wantDir || !safePayloadName(path.Base(record.Path)) {
			return artifactClaimKey{}, errors.Join(errArtifactClaimConflict, err)
		}
	case claimScratch:
		if record.Source != record.Path || !canonicalRegularArtifactSource(record.Path) {
			return artifactClaimKey{}, errArtifactClaimConflict
		}
	default:
		return artifactClaimKey{}, errArtifactClaimConflict
	}
	key, err := newArtifactClaimKey(record.Operation, record.Path, record.Role)
	if err != nil {
		return artifactClaimKey{}, errors.Join(errArtifactClaimConflict, err)
	}
	return key, nil
}

func (s *Store) verifyInstalledRegularArtifact(ctx context.Context, key artifactClaimKey, name string, expected fs.FileInfo) (directoryWitnessRecord, error) {
	verified, err := s.verifyArtifactProofAt(ctx, key, name, expected, false)
	if err != nil {
		return directoryWitnessRecord{}, err
	}
	raw, err := s.readMetadataLimitObserved(ctx, key.bindingPath(), maxJournalManifestRead)
	if err != nil {
		return directoryWitnessRecord{}, errors.Join(errArtifactClaimConflict, err)
	}
	record, err := decodeDirectoryWitness(raw)
	if err != nil || !canonicalRegularArtifactSource(record.Source) || verified == nil || !os.SameFile(expected, verified) {
		return record, errors.Join(errArtifactClaimConflict, err)
	}
	return record, nil
}

func (s *Store) resolveInstalledRegularArtifact(ctx context.Context, name string, role artifactRole, expected fs.FileInfo) (artifactClaimKey, directoryWitnessRecord, error) {
	dir, err := openPinnedMetadataDir(s.rootFD, claimDirectory)
	if err != nil {
		return artifactClaimKey{}, directoryWitnessRecord{}, errors.Join(errArtifactClaimConflict, err)
	}
	entries, readErr := dir.ReadDir(-1)
	closeErr := dir.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return artifactClaimKey{}, directoryWitnessRecord{}, err
	}
	var found *artifactClaimKey
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return artifactClaimKey{}, directoryWitnessRecord{}, err
		}
		if !strings.HasSuffix(entry.Name(), ".binding.json") {
			continue
		}
		raw, err := s.readMetadataLimitObserved(ctx, path.Join(claimDirectory, entry.Name()), maxJournalManifestRead)
		if err != nil {
			return artifactClaimKey{}, directoryWitnessRecord{}, err
		}
		record, err := decodeDirectoryWitness(raw)
		if err != nil {
			return artifactClaimKey{}, directoryWitnessRecord{}, err
		}
		key, err := newArtifactClaimKey(record.Operation, record.Path, record.Role)
		if err != nil || path.Base(key.bindingPath()) != entry.Name() {
			return artifactClaimKey{}, directoryWitnessRecord{}, errors.Join(errArtifactClaimConflict, err)
		}
		if record.Path != path.Clean(name) || record.Role != role {
			continue
		}
		if found != nil {
			return artifactClaimKey{}, directoryWitnessRecord{}, errArtifactClaimConflict
		}
		candidate := key
		found = &candidate
	}
	if found == nil {
		return artifactClaimKey{}, directoryWitnessRecord{}, errArtifactClaimConflict
	}
	record, err := s.verifyInstalledRegularArtifact(ctx, *found, name, expected)
	return *found, record, err
}

func (s *Store) cleanupCapabilityInventory(ctx context.Context) error {
	dir, err := openPinnedMetadataDir(s.rootFD, claimDirectory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	entries, readErr := dir.ReadDir(-1)
	closeErr := dir.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !strings.HasSuffix(entry.Name(), ".binding.json") {
			continue
		}
		raw, err := s.readMetadataLimitObserved(ctx, path.Join(claimDirectory, entry.Name()), maxJournalManifestRead)
		if err != nil {
			return err
		}
		record, err := decodeDirectoryWitness(raw)
		if err != nil {
			return err
		}
		if record.Role != claimCapability {
			continue
		}
		key, err := newArtifactClaimKey(record.Operation, record.Path, record.Role)
		if err != nil || path.Base(key.bindingPath()) != entry.Name() || path.Dir(record.Path) != path.Join(internalDirectory, "capabilities") || !canonicalRegularArtifactSource(record.Source) {
			return errors.Join(errArtifactClaimConflict, err)
		}
		witness, witnessErr := s.rootFD.Lstat(key.witnessPath(false))
		if errors.Is(witnessErr, os.ErrNotExist) {
			target, targetErr := s.rootFD.Lstat(key.Path)
			identity, ok := fileIdentityKey(target)
			if targetErr != nil || target == nil || !target.Mode().IsRegular() || !ok || identity != record.Identity {
				return errors.Join(errArtifactClaimConflict, targetErr)
			}
			if _, err := s.prepareRegularWitnessFrom(ctx, key, key.Path, target); err != nil {
				return err
			}
		} else if witnessErr != nil || witness == nil {
			return errors.Join(errArtifactClaimConflict, witnessErr)
		}
		removed, err := s.consumeOwnedClaim(ctx, key, false)
		if err != nil || !removed {
			return errors.Join(errArtifactClaimConflict, err)
		}
		if err := s.syncDirAt(path.Dir(key.Path)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) cleanupReceiptPruneInventory(ctx context.Context) error {
	dir, err := openPinnedMetadataDir(s.rootFD, claimDirectory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	entries, readErr := dir.ReadDir(-1)
	closeErr := dir.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".binding.json") {
			continue
		}
		raw, err := s.readMetadataLimitObserved(ctx, path.Join(claimDirectory, entry.Name()), maxJournalManifestRead)
		if err != nil {
			return err
		}
		record, err := decodeDirectoryWitness(raw)
		if err != nil {
			return err
		}
		if record.Role != claimReceipt {
			continue
		}
		key, target, claimed, err := s.authenticateReceiptPruneBinding(ctx, record, entry.Name())
		if err != nil {
			return err
		}
		witness, witnessErr := s.rootFD.Lstat(key.witnessPath(false))
		if errors.Is(witnessErr, os.ErrNotExist) {
			if claimed {
				return errArtifactClaimConflict
			}
			if _, err := s.prepareRegularWitnessFrom(ctx, key, key.Path, target); err != nil {
				return err
			}
		} else if witnessErr != nil || witness == nil {
			return errors.Join(errArtifactClaimConflict, witnessErr)
		}
		removed, err := s.consumeOwnedClaim(ctx, key, false)
		if err != nil || !removed {
			return errors.Join(errArtifactClaimConflict, err)
		}
	}
	return nil
}

// authenticateReceiptPruneBinding proves the exact byte-bearing receipt and
// its immutable binding before recovery is allowed to repair a witness or
// consume a claim. The operation is derived from the authenticated raw bytes,
// never trusted from the binding inventory.
func (s *Store) authenticateReceiptPruneBinding(ctx context.Context, record directoryWitnessRecord, bindingBase string) (artifactClaimKey, fs.FileInfo, bool, error) {
	if record.Source != record.Path || path.Dir(record.Path) != path.Join(internalDirectory, "receipts") || !strings.HasPrefix(record.Operation, "receipt:") {
		return artifactClaimKey{}, nil, false, errArtifactClaimConflict
	}
	key, err := newArtifactClaimKey(record.Operation, record.Path, record.Role)
	if err != nil || path.Base(key.bindingPath()) != bindingBase {
		return artifactClaimKey{}, nil, false, errors.Join(errArtifactClaimConflict, err)
	}
	target, targetErr := s.rootFD.Lstat(key.Path)
	claim, claimErr := s.rootFD.Lstat(key.claimPath())
	claimed := false
	readName := key.Path
	switch {
	case targetErr == nil && claimErr == nil:
		return artifactClaimKey{}, nil, false, errArtifactClaimConflict
	case targetErr == nil && errors.Is(claimErr, os.ErrNotExist):
	case errors.Is(targetErr, os.ErrNotExist) && claimErr == nil:
		target, readName, claimed = claim, key.claimPath(), true
	case errors.Is(targetErr, os.ErrNotExist) && errors.Is(claimErr, os.ErrNotExist):
		return artifactClaimKey{}, nil, false, errors.Join(errArtifactClaimConflict, os.ErrNotExist)
	default:
		return artifactClaimKey{}, nil, false, errors.Join(targetErr, claimErr)
	}
	identity, ok := fileIdentityKey(target)
	if target == nil || !target.Mode().IsRegular() || !ok || identity != record.Identity {
		return artifactClaimKey{}, nil, false, errArtifactClaimConflict
	}
	raw, err := s.readMetadataLimitOwned(ctx, readName, maxMetadataRead, target)
	if err != nil {
		return artifactClaimKey{}, nil, false, err
	}
	receipt, err := decodeReceiptFile(raw)
	if err != nil {
		return artifactClaimKey{}, nil, false, errors.Join(errArtifactClaimConflict, metadataCorrupt(err))
	}
	if s.receiptPath(store.IdempotencyKey(receipt.Key)) != record.Path || receipt.Receipt.RequestDigest != receipt.Digest || receiptPruneOperation(record.Path, raw) != record.Operation {
		return artifactClaimKey{}, nil, false, errArtifactClaimConflict
	}
	return key, target, claimed, nil
}

func (s *Store) restoreForeignClaim(key artifactClaimKey, foreign fs.FileInfo, wantDir bool) error {
	if err := s.runDescriptorBarrier("claim_before_restore"); err != nil {
		return err
	}
	if wantDir {
		_, err := s.fdRenameOwnedNoReplace(key.claimPath(), key.Path, foreign, true)
		if err != nil {
			return errors.Join(errArtifactClaimConflict, err)
		}
	} else {
		if err := s.fdLinkOwnedNoReplace(key.claimPath(), key.Path, foreign); err != nil {
			return errors.Join(errArtifactClaimConflict, err)
		}
		if err := s.syncDirAt(path.Dir(key.Path)); err != nil {
			return err
		}
		removed, err := s.fdRemoveClaimOwned(claimZonePath(key.claimPath()), foreign, false)
		if err != nil || !removed {
			return errors.Join(errArtifactClaimConflict, err)
		}
	}
	return errors.Join(s.syncDirAt(path.Dir(key.Path)), s.syncDirAt(claimDirectory), errArtifactClaimConflict)
}

// prepareOwnedClaim is the live-attempt half of owned deletion. It may create
// proof only from the caller's pinned inode and publishes the canonical name
// into the trusted claim namespace with NOREPLACE.
func (s *Store) prepareOwnedClaim(ctx context.Context, key artifactClaimKey, expected fs.FileInfo, wantDir bool) (fs.FileInfo, error) {
	claimName := key.claimPath()
	claim, claimErr := s.rootFD.Lstat(claimName)
	if errors.Is(claimErr, os.ErrNotExist) {
		source, sourceErr := s.rootFD.Lstat(key.Path)
		if sourceErr != nil {
			return nil, sourceErr
		}
		if source == nil || expected == nil || source.IsDir() != wantDir || !os.SameFile(source, expected) {
			return nil, errArtifactClaimConflict
		}
		if _, err := s.prepareArtifactWitness(ctx, key, expected, wantDir); err != nil {
			return nil, err
		}
		identity, err := s.fdRenameOwnedNoReplace(key.Path, claimName, expected, wantDir)
		if err != nil {
			return nil, err
		}
		claim = identity
		if err := s.syncDirAt(path.Dir(key.Path)); err != nil {
			if !wantDir || key.Role != claimStageDir || !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
			if err := s.syncStageClaimSourceParent(key); err != nil {
				return nil, err
			}
		}
		if err := s.syncDirAt(claimDirectory); err != nil {
			return nil, err
		}
	} else if claimErr != nil {
		return nil, claimErr
	} else if _, originalErr := s.rootFD.Lstat(key.Path); originalErr == nil {
		return nil, errArtifactClaimConflict
	} else if !errors.Is(originalErr, os.ErrNotExist) {
		return nil, originalErr
	} else {
		if err := s.syncDirAt(path.Dir(key.Path)); err != nil {
			if !wantDir || key.Role != claimStageDir || !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
			if err := s.syncStageClaimSourceParent(key); err != nil {
				return nil, err
			}
		}
		if err := s.syncDirAt(claimDirectory); err != nil {
			return nil, err
		}
	}
	return claim, nil
}

// consumeOwnedClaim is recovery-safe and consume-only. It never calls a proof
// preparation or creation path: missing or contradictory proof is preserved
// and reported as a conflict.
func (s *Store) consumeOwnedClaim(ctx context.Context, key artifactClaimKey, wantDir bool) (bool, error) {
	claimName := key.claimPath()
	claim, claimErr := s.rootFD.Lstat(claimName)
	if errors.Is(claimErr, os.ErrNotExist) {
		binding, bindingErr := s.rootFD.Lstat(key.bindingPath())
		witness, witnessErr := s.rootFD.Lstat(key.witnessPath(wantDir))
		if bindingErr != nil && !errors.Is(bindingErr, os.ErrNotExist) {
			return false, bindingErr
		}
		if witnessErr != nil && !errors.Is(witnessErr, os.ErrNotExist) {
			return false, witnessErr
		}
		proofPresent := bindingErr == nil || witnessErr == nil
		source, sourceErr := s.rootFD.Lstat(key.Path)
		if sourceErr == nil && !proofPresent {
			return false, nil
		}
		if errors.Is(sourceErr, os.ErrNotExist) && !proofPresent {
			return true, nil
		}
		if errors.Is(sourceErr, os.ErrNotExist) && bindingErr == nil && errors.Is(witnessErr, os.ErrNotExist) {
			if err := s.verifyArtifactBindingRecord(ctx, key, binding); err != nil {
				return false, err
			}
			if err := s.removeArtifactBinding(key); err != nil {
				return false, err
			}
			return true, nil
		}
		if errors.Is(sourceErr, os.ErrNotExist) && bindingErr == nil && witnessErr == nil && !wantDir {
			if _, err := s.verifyBindingIdentity(ctx, key, witness); err != nil {
				return false, err
			}
			if err := s.removeArtifactWitness(key, witness); err != nil {
				return false, err
			}
			return true, nil
		}
		if sourceErr != nil {
			return false, errors.Join(errArtifactClaimConflict, sourceErr)
		}
		verifiedSource, err := s.verifyArtifactProofAt(ctx, key, key.Path, source, wantDir)
		if err != nil {
			return false, err
		}
		claim, err = s.fdRenameOwnedNoReplace(key.Path, claimName, verifiedSource, wantDir)
		if err != nil {
			return false, err
		}
		if err := s.syncDirAt(path.Dir(key.Path)); err != nil {
			if !wantDir || key.Role != claimStageDir || !errors.Is(err, os.ErrNotExist) {
				return false, err
			}
			if err := s.syncStageClaimSourceParent(key); err != nil {
				return false, err
			}
		}
		if err := s.syncDirAt(claimDirectory); err != nil {
			return false, err
		}
		claimErr = nil
	}
	if claimErr != nil {
		return false, claimErr
	}
	if _, err := s.rootFD.Lstat(key.Path); err == nil {
		return false, errArtifactClaimConflict
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := s.syncDirAt(path.Dir(key.Path)); err != nil {
		if !wantDir || key.Role != claimStageDir || !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		if err := s.syncStageClaimSourceParent(key); err != nil {
			return false, err
		}
	}
	if err := s.syncDirAt(claimDirectory); err != nil {
		return false, err
	}
	verified, err := s.verifyArtifactClaim(ctx, key, claim, wantDir)
	if err != nil {
		return false, err
	}
	if err := s.runDescriptorBarrier("claim_before_unlink"); err != nil {
		return false, err
	}
	if err := s.runDescriptorBarrier("claim_before_unlink:" + string(key.Role)); err != nil {
		return false, err
	}
	if wantDir {
		sentinelName := key.claimedDirectoryWitnessPath()
		sentinel, err := s.rootFD.Lstat(sentinelName)
		if err != nil || sentinel == nil || !sentinel.Mode().IsRegular() {
			return false, errors.Join(errArtifactClaimConflict, err)
		}
		removed, err := s.fdRemoveClaimOwned(claimZonePath(sentinelName), sentinel, false)
		if err != nil || !removed {
			return false, errors.Join(errArtifactClaimConflict, err)
		}
		if err := s.runDescriptorBarrier("directory_sentinel_removed"); err != nil {
			return false, err
		}
		if err := s.syncDirAt(claimName); err != nil {
			return false, err
		}
		if err := s.runDescriptorBarrier("directory_sentinel_parent_synced"); err != nil {
			return false, err
		}
	}
	removed, err := s.fdRemoveClaimOwned(claimZonePath(claimName), verified, wantDir)
	if err != nil || !removed {
		return false, errors.Join(errArtifactClaimConflict, err)
	}
	if err := s.runDescriptorBarrier("claim_object_removed"); err != nil {
		return false, err
	}
	if err := s.syncDirAt(claimDirectory); err != nil {
		return false, err
	}
	if err := s.runDescriptorBarrier("claim_object_parent_synced"); err != nil {
		return false, err
	}
	if wantDir {
		if key.Role == claimStageDir {
			if err := s.runDescriptorBarrier("stage_claim_parent_synced"); err != nil {
				return false, err
			}
		}
		if err := s.removeArtifactBinding(key); err != nil {
			return false, err
		}
		return true, nil
	}
	witnessName := key.witnessPath(false)
	witness, err := s.rootFD.Lstat(witnessName)
	if err != nil {
		return false, err
	}
	if err := s.removeArtifactWitness(key, witness); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) verifyArtifactBindingRecord(ctx context.Context, key artifactClaimKey, expected fs.FileInfo) error {
	if expected == nil || !expected.Mode().IsRegular() {
		return errArtifactClaimConflict
	}
	raw, err := s.readMetadataLimitObserved(ctx, key.bindingPath(), maxJournalManifestRead)
	if err != nil {
		return err
	}
	record, err := decodeDirectoryWitness(raw)
	if err != nil || record.Operation != key.Operation || record.Path != key.Path || record.Role != key.Role {
		return errors.Join(errArtifactClaimConflict, err)
	}
	current, err := s.rootFD.Lstat(key.bindingPath())
	if err != nil || current == nil || !os.SameFile(expected, current) {
		return errors.Join(errArtifactClaimConflict, err)
	}
	return nil
}

func (s *Store) restoreExpectedArtifactClaim(key artifactClaimKey, expected fs.FileInfo, wantDir bool) error {
	claim, err := s.verifyArtifactClaim(context.Background(), key, expected, wantDir)
	if err != nil {
		return err
	}
	if _, err := s.rootFD.Lstat(key.Path); err == nil {
		return errArtifactClaimConflict
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := s.runDescriptorBarrier("claim_before_restore"); err != nil {
		return err
	}
	if wantDir {
		if _, err := s.fdRenameOwnedNoReplace(key.claimPath(), key.Path, claim, true); err != nil {
			return err
		}
	} else {
		if err := s.fdLinkOwnedNoReplace(key.claimPath(), key.Path, claim); err != nil {
			return err
		}
		if err := s.syncDirAt(path.Dir(key.Path)); err != nil {
			return err
		}
		removed, err := s.fdRemoveClaimOwned(claimZonePath(key.claimPath()), claim, false)
		if err != nil || !removed {
			return errors.Join(errArtifactClaimConflict, err)
		}
	}
	return errors.Join(s.syncDirAt(path.Dir(key.Path)), s.syncDirAt(claimDirectory))
}

func (s *Store) restoreStageClaims(j journal) error {
	paths := make([]struct {
		name string
		role artifactRole
		dir  bool
	}, 0, len(j.Files)+2)
	for _, entry := range j.Files {
		paths = append(paths, struct {
			name string
			role artifactRole
			dir  bool
		}{path.Join(j.Stage, entry.Payload), claimPayload, false})
	}
	paths = append(paths,
		struct {
			name string
			role artifactRole
			dir  bool
		}{j.Stage, claimStageDir, true},
		struct {
			name string
			role artifactRole
			dir  bool
		}{path.Dir(j.Stage), claimStageDir, true},
	)
	for _, item := range paths {
		key, err := newArtifactClaimKey(j.Receipt.RequestDigest, item.name, item.role)
		if err != nil {
			return err
		}
		claim, err := s.rootFD.Lstat(key.claimPath())
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err := s.restoreExpectedArtifactClaim(key, claim, item.dir); err != nil {
			return err
		}
	}
	return nil
}

// installOwnedClaimed consumes a verified regular source token into an absent
// destination with NOREPLACE. Its deterministic witness makes the operation
// replayable after a stop at any point between rename and parent syncs.
func (s *Store) installOwnedClaimed(ctx context.Context, key artifactClaimKey, source string, expected fs.FileInfo, deferTargetSync bool) (fs.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	target, targetErr := s.rootFD.Lstat(key.Path)
	_, sourceErr := s.rootFD.Lstat(source)
	if targetErr == nil {
		witness, witnessErr := s.rootFD.Lstat(key.witnessPath(false))
		if !errors.Is(sourceErr, os.ErrNotExist) || target == nil || !target.Mode().IsRegular() || !os.SameFile(expected, target) {
			return nil, errors.Join(errArtifactClaimConflict, sourceErr, witnessErr)
		}
		if errors.Is(witnessErr, os.ErrNotExist) {
			return target, nil
		}
		if witnessErr != nil || witness == nil || !os.SameFile(target, witness) {
			return nil, errors.Join(errArtifactClaimConflict, witnessErr)
		}
		if err := s.syncClaimProtocolParent(source); err != nil {
			return nil, err
		}
		if !deferTargetSync {
			if err := s.syncDirAt(path.Dir(key.Path)); err != nil {
				return nil, err
			}
		}
		if err := s.runDescriptorBarrier("installed_target_parent_synced:" + string(key.Role)); err != nil {
			return nil, err
		}
		if key.Role == claimJournal || key.Role == claimPayload || key.Role == claimCapability {
			return target, nil
		}
		if err := s.removeArtifactWitness(key, witness); err != nil {
			return nil, err
		}
		return target, nil
	}
	if !errors.Is(targetErr, os.ErrNotExist) {
		return nil, targetErr
	}
	if sourceErr != nil {
		return nil, sourceErr
	}
	witness, err := s.prepareRegularWitnessFrom(ctx, key, source, expected)
	if err != nil {
		return nil, err
	}
	installed, err := s.fdRenameOwnedNoReplace(source, key.Path, expected, false)
	if err != nil {
		return installed, err
	}
	if err := s.runDescriptorBarrier("install_after_noreplace"); err != nil {
		return installed, err
	}
	if err := s.syncClaimProtocolParent(source); err != nil {
		return installed, err
	}
	if !deferTargetSync {
		if err := s.syncDirAt(path.Dir(key.Path)); err != nil {
			return installed, err
		}
	}
	current, statErr := s.rootFD.Lstat(key.Path)
	if statErr != nil {
		return installed, statErr
	}
	if current == nil || installed == nil || !current.Mode().IsRegular() || !os.SameFile(expected, current) || !os.SameFile(current, witness) {
		return installed, s.restoreForeignInstall(key, source, current)
	}
	if key.Role == claimJournal || key.Role == claimPayload || key.Role == claimCapability {
		return installed, nil
	}
	if err := s.removeArtifactWitness(key, witness); err != nil {
		return installed, err
	}
	return installed, nil
}

func (s *Store) restoreForeignInstall(key artifactClaimKey, source string, foreign fs.FileInfo) error {
	if foreign == nil {
		return errArtifactClaimConflict
	}
	if err := s.runDescriptorBarrier("claim_before_restore"); err != nil {
		return err
	}
	if err := s.fdLinkOwnedNoReplace(key.Path, source, foreign); err != nil {
		return errors.Join(errArtifactClaimConflict, err)
	}
	if err := s.syncClaimProtocolParent(source); err != nil {
		return err
	}
	// The public target is outside the trusted claims capability. Restoring the
	// exact inode to its source is safe; terminal deletion is not. Preserve both
	// names and report the factual conflict.
	return errArtifactClaimConflict
}

func (s *Store) removeProvenArtifactAlias(ctx context.Context, key artifactClaimKey, name string, expected fs.FileInfo) error {
	if _, err := s.verifyArtifactProofAt(ctx, key, name, expected, false); err != nil {
		return err
	}
	claimName := key.claimPath()
	if existing, err := s.rootFD.Lstat(claimName); err == nil {
		if _, err := s.verifyArtifactClaim(ctx, key, existing, false); err != nil {
			return err
		}
		removed, err := s.fdRemoveClaimOwned(claimZonePath(claimName), existing, false)
		if err != nil || !removed {
			return errors.Join(errArtifactClaimConflict, err)
		}
		if err := s.syncDirAt(claimDirectory); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	claim, err := s.fdRenameOwnedNoReplace(name, claimName, expected, false)
	if err != nil {
		return err
	}
	if err := errors.Join(s.syncDirAt(path.Dir(name)), s.syncDirAt(claimDirectory)); err != nil {
		return err
	}
	removed, err := s.fdRemoveClaimOwned(claimZonePath(claimName), claim, false)
	if err != nil || !removed {
		return errors.Join(errArtifactClaimConflict, err)
	}
	return s.syncDirAt(claimDirectory)
}

func (c scratchAttemptCompensation) removeAlias(ctx context.Context) error {
	s, key, name, expected := c.store, c.key, c.source, c.owned
	if _, err := s.verifyArtifactProofAt(ctx, key, name, expected, false); err != nil {
		return err
	}
	claimName := key.claimPath()
	claim, err := s.rootFD.Lstat(claimName)
	if errors.Is(err, os.ErrNotExist) {
		claim, err = s.fdRenameOwnedNoReplaceMode(name, claimName, expected, false, false)
		if err != nil {
			return err
		}
		if err := errors.Join(s.syncDirectory(path.Dir(name)), s.syncDirectory(claimDirectory)); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if _, err := s.verifyArtifactClaim(ctx, key, claim, false); err != nil {
		return err
	}
	removed, err := s.fdRemoveClaimOwnedMode(claimZonePath(claimName), claim, false, false)
	if err != nil || !removed {
		return errors.Join(errArtifactClaimConflict, err)
	}
	return s.syncDirectory(claimDirectory)
}

func (s *Store) removeArtifactWitness(key artifactClaimKey, witness fs.FileInfo) error {
	if err := s.runDescriptorBarrier("claim_before_unlink"); err != nil {
		return err
	}
	removed, err := s.fdRemoveClaimOwned(claimZonePath(key.witnessPath(false)), witness, false)
	if err != nil || !removed {
		return errors.Join(errArtifactClaimConflict, err)
	}
	if err := s.syncDirAt(claimDirectory); err != nil {
		return err
	}
	if err := s.runDescriptorBarrier("artifact_proof_parent_synced"); err != nil {
		return err
	}
	return s.removeArtifactBinding(key)
}

func (c scratchAttemptCompensation) removeProof(witness fs.FileInfo) error {
	s, key := c.store, c.key
	current, err := s.rootFD.Lstat(key.witnessPath(false))
	if err != nil || current == nil || witness == nil || !os.SameFile(witness, current) {
		return errors.Join(errArtifactClaimConflict, err)
	}
	removed, err := s.fdRemoveClaimOwnedMode(claimZonePath(key.witnessPath(false)), current, false, false)
	if err != nil || !removed {
		return errors.Join(errArtifactClaimConflict, err)
	}
	if err := s.syncDirectory(claimDirectory); err != nil {
		return err
	}
	binding, err := s.rootFD.Lstat(key.bindingPath())
	if err != nil {
		return err
	}
	removed, err = s.fdRemoveClaimOwnedMode(claimZonePath(key.bindingPath()), binding, false, false)
	if err != nil || !removed {
		return errors.Join(errArtifactClaimConflict, err)
	}
	return s.syncDirectory(claimDirectory)
}

func (s *Store) removeArtifactBinding(key artifactClaimKey) error {
	binding, err := s.rootFD.Lstat(key.bindingPath())
	if err != nil {
		return err
	}
	removed, err := s.fdRemoveClaimOwned(claimZonePath(key.bindingPath()), binding, false)
	if err != nil || !removed {
		return errors.Join(errArtifactClaimConflict, err)
	}
	if err := s.runDescriptorBarrier("artifact_binding_removed"); err != nil {
		return err
	}
	return s.syncDirAt(claimDirectory)
}

// recoverJournalClaims converges only claims whose bytes decode to a canonical
// journal and whose deterministic binding can be recomputed. Unknown or
// hostile inventory is preserved and reported; recovery never sweeps it.
func (s *Store) recoverJournalClaims(ctx context.Context) error {
	dir, err := openPinnedMetadataDir(s.rootFD, claimDirectory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	entries, readErr := dir.ReadDir(-1)
	closeErr := dir.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !strings.HasSuffix(entry.Name(), ".claim") {
			continue
		}
		claimName := path.Join(claimDirectory, entry.Name())
		stem := strings.TrimSuffix(entry.Name(), ".claim")
		bindingRaw, bindingErr := s.readMetadataLimitObserved(ctx, path.Join(claimDirectory, stem+".binding.json"), maxJournalManifestRead)
		if bindingErr != nil {
			return errors.Join(errArtifactClaimConflict, bindingErr)
		}
		var binding directoryWitnessRecord
		if json.Unmarshal(bindingRaw, &binding) != nil || binding.Version != 1 || binding.Role != claimJournal {
			continue
		}
		info, err := s.rootFD.Lstat(claimName)
		if err != nil || info == nil || !info.Mode().IsRegular() {
			return errors.Join(errArtifactClaimConflict, err)
		}
		raw, err := s.readMetadataLimitObserved(ctx, claimName, maxJournalManifestRead)
		if err != nil {
			return err
		}
		j, err := decodeJournalWithAlgorithmNameAndLimits(raw, s.config.HashAlgorithm, s.hashAlgorithmName, absoluteStagedManifestLimits())
		if err != nil {
			return errors.Join(errArtifactClaimConflict, metadataCorrupt(err))
		}
		journalName, err := journalPath(j.Receipt.RequestDigest)
		if err != nil {
			return errors.Join(errArtifactClaimConflict, err)
		}
		key, err := newArtifactClaimKey(j.Receipt.RequestDigest, journalName, claimJournal)
		if err != nil || path.Base(key.claimPath()) != entry.Name() {
			return errors.Join(errArtifactClaimConflict, err)
		}
		removed, err := s.consumeOwnedClaim(ctx, key, false)
		if err != nil || !removed {
			return newRecoveryOutcomeError(j, errors.Join(errArtifactClaimConflict, err))
		}
	}
	return nil
}

// resumeRegularInstallBinding closes the two durable pre-install proof states:
// binding-only and binding+witness. The source path and inode identity are
// immutable binding fields, so recovery never guesses which object to resume.
func (s *Store) resumeRegularInstallBinding(ctx context.Context, key artifactClaimKey, record directoryWitnessRecord) error {
	if record.Source == "" || !canonicalRegularArtifactSource(record.Source) || record.Operation != key.Operation || record.Path != key.Path || record.Role != key.Role {
		return errArtifactClaimConflict
	}
	if _, err := s.rootFD.Lstat(key.claimPath()); err == nil {
		return errArtifactClaimConflict
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	source, sourceErr := s.rootFD.Lstat(record.Source)
	if sourceErr != nil && !errors.Is(sourceErr, os.ErrNotExist) {
		return sourceErr
	}
	if sourceErr == nil {
		identity, ok := fileIdentityKey(source)
		if source == nil || !source.Mode().IsRegular() || !ok || identity != record.Identity {
			return errArtifactClaimConflict
		}
	}
	if key.Role == claimPayload {
		if !safeStageDirectory(path.Dir(key.Path)) || !safePayloadName(path.Base(key.Path)) {
			return errArtifactClaimConflict
		}
		for _, directory := range []string{path.Dir(path.Dir(key.Path)), path.Dir(key.Path)} {
			directoryKey, err := newArtifactClaimKey(key.Operation, directory, claimStageDir)
			if err != nil {
				return err
			}
			if _, err := s.createOwnedStageDirectory(ctx, directoryKey); err != nil {
				return err
			}
		}
	}
	witness, witnessErr := s.rootFD.Lstat(key.witnessPath(false))
	if witnessErr == nil {
		identity, ok := fileIdentityKey(witness)
		if witness == nil || !witness.Mode().IsRegular() || !ok || identity != record.Identity || sourceErr == nil && !os.SameFile(source, witness) {
			return errArtifactClaimConflict
		}
	} else if !errors.Is(witnessErr, os.ErrNotExist) {
		return witnessErr
	}
	target, targetErr := s.rootFD.Lstat(key.Path)
	switch {
	case errors.Is(targetErr, os.ErrNotExist) && sourceErr == nil:
		_, err := s.installOwnedClaimed(ctx, key, record.Source, source, false)
		return err
	case errors.Is(targetErr, os.ErrNotExist) && key.Role == claimPayload && witnessErr == nil:
		// The durable journal declares this already-installed payload and the
		// closed binding+witness pair proves which inode belonged here. Its
		// disappearance is structural tampering, not a raw pathname I/O error.
		return metadataCorrupt(fmt.Errorf("declared staged payload is missing: %s: %w", key.Path, targetErr))
	case errors.Is(targetErr, os.ErrNotExist):
		return errArtifactClaimConflict
	case targetErr != nil:
		return targetErr
	case target == nil || !target.Mode().IsRegular():
		return errors.Join(errArtifactClaimConflict, metadataCorrupt(errors.New("regular install target is not a regular file")))
	case sourceErr == nil && !os.SameFile(source, target):
		return errArtifactClaimConflict
	case sourceErr != nil:
		identity, ok := fileIdentityKey(target)
		if !ok || identity != record.Identity || witnessErr == nil && !os.SameFile(target, witness) {
			return errArtifactClaimConflict
		}
		if errors.Is(witnessErr, os.ErrNotExist) {
			return errArtifactClaimConflict
		}
		_, err := s.installOwnedClaimed(ctx, key, record.Source, target, false)
		return err
	default:
		_, err := s.installOwnedClaimed(ctx, key, record.Source, target, false)
		return err
	}
}

// compensateUndurableRegularBindings removes closed private artifacts only
// when their operation has no durable journal. Any inode disagreement is
// preserved as an explicit conflict.
func (s *Store) compensateUndurableRegularBindings(ctx context.Context) error {
	dir, err := openPinnedMetadataDir(s.rootFD, claimDirectory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	entries, readErr := dir.ReadDir(-1)
	closeErr := dir.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !strings.HasSuffix(entry.Name(), ".binding.json") {
			continue
		}
		raw, err := s.readMetadataLimitObserved(ctx, path.Join(claimDirectory, entry.Name()), maxJournalManifestRead)
		if err != nil {
			return err
		}
		record, err := decodeDirectoryWitness(raw)
		if err != nil || (record.Role != claimPayload && record.Role != claimJournal) {
			continue
		}
		key, err := validatePrivateArtifactRole(record)
		if err != nil || path.Base(key.bindingPath()) != entry.Name() {
			return errors.Join(errArtifactClaimConflict, err)
		}
		journalName, err := journalPath(record.Operation)
		if err != nil {
			return errArtifactClaimConflict
		}
		if _, err := s.rootFD.Lstat(journalName); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := s.compensateRegularBinding(ctx, key, record); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) compensateRegularBinding(ctx context.Context, key artifactClaimKey, record directoryWitnessRecord) error {
	if claim, err := s.rootFD.Lstat(key.claimPath()); err == nil {
		if claim == nil || !claim.Mode().IsRegular() {
			return errArtifactClaimConflict
		}
		removed, consumeErr := s.consumeOwnedClaim(ctx, key, false)
		if consumeErr != nil || !removed {
			return errors.Join(errArtifactClaimConflict, consumeErr)
		}
		for _, name := range []string{key.Path, key.claimPath()} {
			if _, statErr := s.rootFD.Lstat(name); !errors.Is(statErr, os.ErrNotExist) {
				return errors.Join(errArtifactClaimConflict, statErr)
			}
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var owned fs.FileInfo
	for _, name := range []string{record.Source, key.Path} {
		info, err := s.rootFD.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || info == nil || !info.Mode().IsRegular() {
			return errors.Join(errArtifactClaimConflict, err)
		}
		identity, ok := fileIdentityKey(info)
		if !ok || identity != record.Identity || owned != nil && !os.SameFile(owned, info) {
			return errArtifactClaimConflict
		}
		owned = info
	}
	witness, witnessErr := s.rootFD.Lstat(key.witnessPath(false))
	if witnessErr == nil {
		identity, ok := fileIdentityKey(witness)
		if !ok || identity != record.Identity || owned != nil && !os.SameFile(owned, witness) {
			return errArtifactClaimConflict
		}
		owned = witness
	} else if !errors.Is(witnessErr, os.ErrNotExist) {
		return witnessErr
	}
	if errors.Is(witnessErr, os.ErrNotExist) && owned != nil {
		sourceName := record.Source
		if _, err := s.rootFD.Lstat(sourceName); errors.Is(err, os.ErrNotExist) {
			sourceName = key.Path
		} else if err != nil {
			return err
		}
		if err := s.fdLinkOwnedNoReplace(sourceName, key.witnessPath(false), owned); err != nil {
			return err
		}
		if err := s.syncDirAt(claimDirectory); err != nil {
			return err
		}
		witness, witnessErr = s.rootFD.Lstat(key.witnessPath(false))
		if witnessErr != nil || witness == nil || !os.SameFile(owned, witness) {
			return errors.Join(errArtifactClaimConflict, witnessErr)
		}
	}
	for _, name := range []string{record.Source, key.Path} {
		info, err := s.rootFD.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || owned == nil || !os.SameFile(owned, info) {
			return errors.Join(errArtifactClaimConflict, err)
		}
		if err := s.removeProvenArtifactAlias(context.Background(), key, name, info); err != nil {
			return err
		}
		if err := s.runDescriptorBarrier("undurable_source_parent_synced:" + string(key.Role)); err != nil {
			return err
		}
	}
	if witnessErr == nil {
		return s.removeArtifactWitness(key, witness)
	}
	return s.removeArtifactBinding(key)
}

func (s *Store) resumeRegularBindingsForJournal(ctx context.Context, j journal) error {
	items := make([]struct {
		name string
		role artifactRole
	}, 0, len(j.Files)+1)
	journalName, err := journalPath(j.Receipt.RequestDigest)
	if err != nil {
		return err
	}
	items = append(items, struct {
		name string
		role artifactRole
	}{name: journalName, role: claimJournal})
	for _, entry := range j.Files {
		items = append(items, struct {
			name string
			role artifactRole
		}{name: path.Join(j.Stage, entry.Payload), role: claimPayload})
	}
	for _, item := range items {
		key, err := newArtifactClaimKey(j.Receipt.RequestDigest, item.name, item.role)
		if err != nil {
			return err
		}
		raw, err := s.readMetadataLimitObserved(ctx, key.bindingPath(), maxJournalManifestRead)
		if errors.Is(err, os.ErrNotExist) {
			current, statErr := s.rootFD.Lstat(item.name)
			if statErr == nil && current != nil {
				return errArtifactClaimConflict
			}
			if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
				return statErr
			}
			continue
		}
		if err != nil {
			return err
		}
		record, err := decodeDirectoryWitness(raw)
		if err != nil {
			return err
		}
		if err := s.resumeRegularInstallBinding(ctx, key, record); err != nil {
			return err
		}
	}
	return nil
}

// restoreClaimInventory restores interrupted claims to their canonical public
// names before journal/provenance processing. Binding files and directory
// sentinels are closed, canonical recovery evidence; unknown inventory is
// preserved with an explicit conflict.
func (s *Store) restoreClaimInventory(ctx context.Context) error {
	dir, err := openPinnedMetadataDir(s.rootFD, claimDirectory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	entries, readErr := dir.ReadDir(-1)
	closeErr := dir.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		var record directoryWitnessRecord
		var raw []byte
		var claimName string
		switch {
		case strings.HasSuffix(entry.Name(), ".binding.json"):
			raw, err = s.readMetadataLimitObserved(ctx, path.Join(claimDirectory, entry.Name()), maxJournalManifestRead)
			if err != nil {
				return err
			}
		case entry.IsDir() && strings.HasSuffix(entry.Name(), ".claim"):
			claimName = path.Join(claimDirectory, entry.Name())
			raw, err = s.readMetadataLimitObserved(ctx, path.Join(claimName, directorySentinelName), maxJournalManifestRead)
			if err != nil {
				return errors.Join(errArtifactClaimConflict, err)
			}
		default:
			continue
		}
		if json.Unmarshal(raw, &record) != nil || record.Version != 1 || !validArtifactRole(record.Role) || !safeClaimSourcePath(record.Path) || record.Source != "" && !safeClaimSourcePath(record.Source) {
			return errArtifactClaimConflict
		}
		canonical, _ := json.Marshal(record)
		if string(canonical) != string(raw) {
			return errArtifactClaimConflict
		}
		key, err := newArtifactClaimKey(record.Operation, record.Path, record.Role)
		if err != nil {
			return err
		}
		if claimName == "" && record.Source != "" && (record.Role == claimPayload || record.Role == claimJournal) {
			if path.Base(key.bindingPath()) != entry.Name() {
				return errArtifactClaimConflict
			}
			continue
		}
		if record.Role == claimJournal {
			continue
		}
		if record.Role == claimStageDir || record.Role == claimScratch || record.Role == claimVisibleInstall || record.Role == claimVisible || record.Role == claimCapability || record.Role == claimReceipt {
			// Stage directory claims encode cleanup-in-progress and are consumed,
			// never restored, by cleanupStageNamespaceClaims.
			continue
		}
		if claimName == "" {
			if path.Base(key.bindingPath()) != entry.Name() {
				return errArtifactClaimConflict
			}
			claimName = key.claimPath()
		} else if path.Base(key.claimPath()) != entry.Name() {
			return errArtifactClaimConflict
		}
		claim, err := s.rootFD.Lstat(claimName)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err := s.restoreExpectedArtifactClaim(key, claim, claim.IsDir()); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) directorySentinelKey(ctx context.Context, directory string) (artifactClaimKey, error) {
	raw, err := s.readMetadataLimitObserved(ctx, path.Join(directory, directorySentinelName), maxJournalManifestRead)
	if err != nil {
		return artifactClaimKey{}, err
	}
	var record directoryWitnessRecord
	if json.Unmarshal(raw, &record) != nil || record.Version != 1 || record.Path != directory || record.Role != claimStageDir || record.Source != "" {
		return artifactClaimKey{}, errArtifactClaimConflict
	}
	canonical, _ := json.Marshal(record)
	if string(canonical) != string(raw) {
		return artifactClaimKey{}, errArtifactClaimConflict
	}
	key, err := newArtifactClaimKey(record.Operation, record.Path, record.Role)
	if err != nil {
		return artifactClaimKey{}, err
	}
	current, err := s.rootFD.Lstat(directory)
	if err != nil || current == nil || !current.IsDir() {
		return artifactClaimKey{}, errors.Join(errArtifactClaimConflict, err)
	}
	if _, err := s.verifyDirectoryWitnessAt(ctx, key, directory, current); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// The sentinel already proved this is an R6-owned namespace. Missing
			// external proof is contradictory, not a legacy sentinel absence.
			return artifactClaimKey{}, errArtifactClaimConflict
		}
		return artifactClaimKey{}, err
	}
	return key, nil
}
