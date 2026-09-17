package bundle

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
)

var errPublicationConflict = errors.New("publication conflict")
var errPublicationScopeInvalid = errors.New("publication scope invalid")

const publicationPrivateClaimPrefix = ".okf-publication-private-v2-"

// publicationModeMask is the complete mode contract retained by bundle
// publishers. File type bits are verified separately and never copied.
const publicationModeMask = fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky

// publicationDurabilityInvariant is a vocabulary for describing which
// durability guarantees an existing protocol row exercises. Descriptors are
// immutable contract data: they neither select a transition nor execute one.
type publicationDurabilityInvariant uint16

const (
	publicationD1RootLock publicationDurabilityInvariant = 1 << iota
	publicationD2PinnedScope
	publicationD3DurableArtifact
	publicationD4ExactOwnership
	publicationD5BatchCommitOrder
	publicationD6GlobalInventory
	publicationD7LegalRecovery
	publicationD8Compensation
	publicationD9PlatformCapability
)

const publicationD1D9 = publicationD1RootLock |
	publicationD2PinnedScope |
	publicationD3DurableArtifact |
	publicationD4ExactOwnership |
	publicationD5BatchCommitOrder |
	publicationD6GlobalInventory |
	publicationD7LegalRecovery |
	publicationD8Compensation |
	publicationD9PlatformCapability

type publicationDescriptorPhase string

type publicationDescriptorTransition struct {
	name       string
	from       publicationDescriptorPhase
	to         publicationDescriptorPhase
	invariants publicationDurabilityInvariant
}

type publicationDescriptorCheckpoint struct {
	name       string
	invariants publicationDurabilityInvariant
}

type publicationProtocolDescriptor struct {
	name        string
	phases      []publicationDescriptorPhase
	transitions []publicationDescriptorTransition
	checkpoints []publicationDescriptorCheckpoint
}

func publicationDescriptorInvariantCoverage(
	descriptor publicationProtocolDescriptor,
) publicationDurabilityInvariant {
	var coverage publicationDurabilityInvariant
	for _, transition := range descriptor.transitions {
		coverage |= transition.invariants
	}
	for _, checkpoint := range descriptor.checkpoints {
		coverage |= checkpoint.invariants
	}
	return coverage
}

func validatePublicationProtocolDescriptor(
	descriptor publicationProtocolDescriptor,
) error {
	if descriptor.name == "" {
		return errors.New("publication protocol descriptor name is empty")
	}
	if len(descriptor.phases) == 0 {
		return errors.New("publication protocol descriptor has no phases")
	}
	phases := make(map[publicationDescriptorPhase]struct{}, len(descriptor.phases))
	for _, phase := range descriptor.phases {
		if phase == "" {
			return errors.New("publication protocol descriptor contains an empty phase")
		}
		if _, duplicate := phases[phase]; duplicate {
			return fmt.Errorf("publication protocol descriptor contains duplicate phase %q", phase)
		}
		phases[phase] = struct{}{}
	}
	rowNames := make(map[string]struct{}, len(descriptor.transitions)+len(descriptor.checkpoints))
	validateRow := func(name string, invariants publicationDurabilityInvariant) error {
		if name == "" {
			return errors.New("publication protocol descriptor contains an unnamed row")
		}
		if _, duplicate := rowNames[name]; duplicate {
			return fmt.Errorf("publication protocol descriptor contains duplicate row %q", name)
		}
		rowNames[name] = struct{}{}
		if invariants == 0 {
			return fmt.Errorf("publication protocol descriptor row %q has no invariants", name)
		}
		if unknown := invariants &^ publicationD1D9; unknown != 0 {
			return fmt.Errorf(
				"publication protocol descriptor row %q has unknown invariants %#x",
				name,
				unknown,
			)
		}
		return nil
	}
	for _, transition := range descriptor.transitions {
		if err := validateRow(transition.name, transition.invariants); err != nil {
			return err
		}
		if _, declared := phases[transition.from]; !declared {
			return fmt.Errorf(
				"publication protocol descriptor transition %q has undeclared source %q",
				transition.name,
				transition.from,
			)
		}
		if _, declared := phases[transition.to]; !declared {
			return fmt.Errorf(
				"publication protocol descriptor transition %q has undeclared destination %q",
				transition.name,
				transition.to,
			)
		}
		if transition.from == transition.to {
			return fmt.Errorf(
				"publication protocol descriptor transition %q does not change phase",
				transition.name,
			)
		}
	}
	for _, checkpoint := range descriptor.checkpoints {
		if err := validateRow(checkpoint.name, checkpoint.invariants); err != nil {
			return err
		}
	}
	if coverage := publicationDescriptorInvariantCoverage(descriptor); coverage != publicationD1D9 {
		return fmt.Errorf(
			"publication protocol descriptor invariant coverage is %#x, want %#x",
			coverage,
			publicationD1D9,
		)
	}
	return nil
}

type publicationFileSpec struct {
	info     os.FileInfo
	mode     fs.FileMode
	size     int64
	digest   [sha256.Size]byte
	data     []byte
	created  bool
	complete bool
}

type publicationVacateOutcome struct {
	claimed        publicationFileSpec
	renamed        bool
	durableReceipt bool
}

func publicationVacateClaimMatches(
	expected publicationFileSpec,
	candidate publicationFileSpec,
	identityOnly bool,
) bool {
	matches := expected.info != nil &&
		candidate.info != nil &&
		os.SameFile(expected.info, candidate.info)
	if identityOnly {
		return matches
	}
	return matches &&
		candidate.mode == expected.mode &&
		candidate.size == expected.size &&
		candidate.digest == expected.digest &&
		bytes.Equal(candidate.data, expected.data)
}

type publicationScopePhase uint8

const (
	publicationScopeStable publicationScopePhase = iota
	publicationScopeCreate
	publicationScopeVacate
	publicationScopeInstall
	publicationScopeRemove
)

type publicationBarrierHooks struct {
	scopePhase           publicationScopePhase
	validateScope        func(publicationScopePhase) error
	validateCompensation func() error
	fileStat             func(*os.File) (os.FileInfo, error)
	afterOpen            func(*os.File) error
	afterCreate          func(*os.File) error
	afterWrite           func(*os.File) error
	afterChmod           func(*os.File) error
	fileSync             func(*os.File) error
	afterFileSync        func(*os.File) error
	afterClose           func() error
	afterVerify          func() error
	directorySync        func(*os.Root) error
	afterDirectorySync   func() error
	beforeVacate         func() error
	afterVacateRename    func() error
	afterVacate          func() error
	beforeInstall        func() error
	afterInstall         func() error
	beforeRemove         func() error
	afterRemove          func() error
	beforeMutation       func()
}

func validatePublicationScope(hooks publicationBarrierHooks) error {
	if hooks.validateScope == nil {
		return nil
	}
	if err := hooks.validateScope(hooks.scopePhase); err != nil {
		return errors.Join(errPublicationScopeInvalid, err)
	}
	return nil
}

func validatePublicationMutationContext(ctx context.Context, hooks publicationBarrierHooks) error {
	if hooks.beforeMutation != nil {
		hooks.beforeMutation()
	}
	return ctx.Err()
}

func validatePublicationCompensationScope(hooks publicationBarrierHooks) error {
	if hooks.validateCompensation == nil {
		return validatePublicationScope(hooks)
	}
	if err := hooks.validateCompensation(); err != nil {
		return errors.Join(errPublicationScopeInvalid, err)
	}
	return nil
}

func runPublicationBarrier(
	hooks publicationBarrierHooks,
	barrier func() error,
) error {
	if barrier == nil {
		return nil
	}
	barrierErr := barrier()
	return errors.Join(barrierErr, validatePublicationScope(hooks))
}

func publicationPreservedMode(mode fs.FileMode) fs.FileMode {
	return mode & publicationModeMask
}

func writeAllPublicationBytes(file *os.File, data []byte) error {
	for len(data) > 0 {
		count, err := file.Write(data)
		if err != nil {
			return err
		}
		if count == 0 {
			return errors.New("short write")
		}
		data = data[count:]
	}
	return nil
}

func capturePublicationFile(
	ctx context.Context,
	parent *os.Root,
	name string,
) (publicationFileSpec, error) {
	if err := ctx.Err(); err != nil {
		return publicationFileSpec{}, err
	}
	before, err := parent.Lstat(name)
	if err != nil {
		return publicationFileSpec{}, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return publicationFileSpec{}, ErrNotRegularFile
	}
	data, err := readRegularDocumentFile(ctx, parent, name)
	if err != nil {
		return publicationFileSpec{}, err
	}
	after, err := parent.Lstat(name)
	if err != nil || after.Mode()&os.ModeSymlink != 0 || !after.Mode().IsRegular() ||
		!os.SameFile(before, after) ||
		publicationPreservedMode(before.Mode()) != publicationPreservedMode(after.Mode()) ||
		before.Size() != after.Size() ||
		!before.ModTime().Equal(after.ModTime()) {
		return publicationFileSpec{}, publicationConflict("file changed while capturing", err)
	}
	second, err := readRegularDocumentFile(ctx, parent, name)
	if err != nil || !bytes.Equal(data, second) {
		return publicationFileSpec{}, publicationConflict("file bytes changed while capturing", err)
	}
	final, err := parent.Lstat(name)
	if err != nil || !os.SameFile(after, final) ||
		publicationPreservedMode(after.Mode()) != publicationPreservedMode(final.Mode()) ||
		after.Size() != final.Size() ||
		!after.ModTime().Equal(final.ModTime()) {
		return publicationFileSpec{}, publicationConflict("file observation changed while capturing", err)
	}
	return publicationFileSpec{
		info:     final,
		mode:     publicationPreservedMode(final.Mode()),
		size:     final.Size(),
		digest:   sha256.Sum256(data),
		data:     data,
		complete: true,
	}, nil
}

func verifyPublicationFile(
	ctx context.Context,
	parent *os.Root,
	name string,
	expected publicationFileSpec,
	requireIdentity bool,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := parent.Lstat(name)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() ||
		requireIdentity && (expected.info == nil || !os.SameFile(expected.info, info)) ||
		publicationPreservedMode(info.Mode()) != expected.mode ||
		info.Size() != expected.size {
		return publicationConflict("file identity, type, size, or mode changed", err)
	}
	data, err := readRegularDocumentFile(ctx, parent, name)
	if err != nil || sha256.Sum256(data) != expected.digest || !bytes.Equal(data, expected.data) {
		return publicationConflict("file bytes changed", err)
	}
	after, err := parent.Lstat(name)
	if err != nil || !os.SameFile(info, after) ||
		publicationPreservedMode(after.Mode()) != expected.mode ||
		after.Size() != expected.size {
		return publicationConflict("file changed while verifying", err)
	}
	return nil
}

func syncPublicationPath(
	ctx context.Context,
	parent *os.Root,
	name string,
	expected publicationFileSpec,
	identityOnly bool,
	hooks publicationBarrierHooks,
) error {
	verify := func() error {
		if identityOnly {
			info, err := parent.Lstat(name)
			if err != nil ||
				info.Mode()&os.ModeSymlink != 0 ||
				!info.Mode().IsRegular() ||
				expected.info == nil ||
				!os.SameFile(expected.info, info) {
				return publicationConflict("file identity changed around sync", err)
			}
			return nil
		}
		return verifyPublicationFile(ctx, parent, name, expected, true)
	}
	if err := verify(); err != nil {
		return err
	}
	if err := validatePublicationScope(hooks); err != nil {
		return err
	}
	file, err := openRegularFile(parent, name)
	if err != nil {
		return err
	}
	info, statErr := file.Stat()
	if statErr != nil ||
		expected.info == nil ||
		!os.SameFile(expected.info, info) {
		return errors.Join(
			statErr,
			file.Close(),
			publicationConflict("opened sync source identity changed", nil),
		)
	}
	syncErr := syncPublicationFile(file, hooks)
	closeErr := file.Close()
	if err := errors.Join(syncErr, closeErr); err != nil {
		return err
	}
	return verify()
}

func createPublicationArtifact(
	ctx context.Context,
	parent *os.Root,
	name string,
	mode fs.FileMode,
	data []byte,
	hooks publicationBarrierHooks,
) (publicationFileSpec, error) {
	hooks.scopePhase = publicationScopeCreate
	if err := ctx.Err(); err != nil {
		return publicationFileSpec{}, err
	}
	if err := validatePublicationScope(hooks); err != nil {
		return publicationFileSpec{}, err
	}
	privateName, err := allocatePrivatePublicationClaim(parent, name)
	if err != nil {
		return publicationFileSpec{}, err
	}
	if err := validatePublicationScope(hooks); err != nil {
		return publicationFileSpec{}, err
	}
	if err := validatePublicationMutationContext(ctx, hooks); err != nil {
		return publicationFileSpec{}, err
	}
	file, err := parent.OpenFile(
		privateName,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		mode.Perm(),
	)
	if err != nil {
		return publicationFileSpec{}, err
	}
	partial := publicationFileSpec{created: true}
	recoverUnknownIdentity := func(cause error) (publicationFileSpec, error) {
		closeErr := file.Close()
		return partial, errors.Join(
			cause,
			closeErr,
			publicationConflict(
				fmt.Sprintf(
					"created private artifact identity is unavailable; random recovery claim %q was retained for manual resolution",
					privateName,
				),
				nil,
			),
		)
	}
	statFile := file.Stat
	if hooks.fileStat != nil {
		statFile = func() (os.FileInfo, error) {
			info, statErr := hooks.fileStat(file)
			return info, errors.Join(statErr, validatePublicationScope(hooks))
		}
	}
	created, statErr := statFile()
	if statErr != nil {
		created, statErr = statFile()
	}
	if statErr != nil {
		return recoverUnknownIdentity(statErr)
	}
	partial.info = created
	cleanupKnownPrivate := func(
		cause error,
		spec publicationFileSpec,
	) (publicationFileSpec, error) {
		removed, removeErr := guardedRemovePublicationFileWithIdentityAs(
			context.WithoutCancel(ctx),
			parent,
			privateName,
			name,
			spec,
			true,
			publicationBarrierHooks{},
		)
		if removed && publicationEntryAbsent(parent, privateName) {
			return publicationFileSpec{}, errors.Join(cause, removeErr)
		}
		return partial, errors.Join(
			cause,
			removeErr,
			publicationConflict("created private artifact could not be converged", nil),
		)
	}
	if hooks.afterOpen != nil {
		if err := runPublicationBarrier(hooks, func() error {
			return hooks.afterOpen(file)
		}); err != nil {
			if errors.Is(err, errPublicationScopeInvalid) {
				return partial, errors.Join(err, file.Close())
			}
			closeErr := file.Close()
			spec, captureErr := capturePublicationFile(context.WithoutCancel(ctx), parent, privateName)
			if captureErr != nil || spec.info == nil || !os.SameFile(created, spec.info) {
				return partial, errors.Join(
					err,
					closeErr,
					captureErr,
					publicationConflict(
						"created private artifact identity changed after open barrier",
						nil,
					),
				)
			}
			spec.created = true
			return cleanupKnownPrivate(errors.Join(err, closeErr), spec)
		}
	}
	fail := func(cause error) (publicationFileSpec, error) {
		closeErr := file.Close()
		if errors.Is(cause, errPublicationScopeInvalid) {
			return partial, errors.Join(cause, closeErr)
		}
		spec, captureErr := capturePublicationFile(context.WithoutCancel(ctx), parent, privateName)
		if captureErr == nil && spec.info != nil && os.SameFile(created, spec.info) {
			spec.created = true
			return cleanupKnownPrivate(errors.Join(cause, closeErr), spec)
		}
		return partial, errors.Join(
			cause,
			closeErr,
			captureErr,
			publicationConflict("created artifact identity cannot be recovered", nil),
		)
	}
	if hooks.afterCreate != nil {
		if err := runPublicationBarrier(hooks, func() error {
			return hooks.afterCreate(file)
		}); err != nil {
			return fail(err)
		}
	}
	if err := writeAllPublicationBytes(file, data); err != nil {
		return fail(err)
	}
	if hooks.afterWrite != nil {
		if err := runPublicationBarrier(hooks, func() error {
			return hooks.afterWrite(file)
		}); err != nil {
			return fail(err)
		}
	}
	if err := file.Chmod(publicationPreservedMode(mode)); err != nil {
		return fail(err)
	}
	if hooks.afterChmod != nil {
		if err := runPublicationBarrier(hooks, func() error {
			return hooks.afterChmod(file)
		}); err != nil {
			return fail(err)
		}
	}
	if err := syncPublicationFile(file, hooks); err != nil {
		return fail(err)
	}
	if hooks.afterFileSync != nil {
		if err := runPublicationBarrier(hooks, func() error {
			return hooks.afterFileSync(file)
		}); err != nil {
			return fail(err)
		}
	}
	if err := file.Close(); err != nil {
		spec, captureErr := capturePublicationFile(context.WithoutCancel(ctx), parent, privateName)
		if captureErr == nil && spec.info != nil && os.SameFile(created, spec.info) {
			spec.created = true
			return cleanupKnownPrivate(err, spec)
		}
		return partial, errors.Join(
			err,
			captureErr,
			publicationConflict("created artifact identity cannot be recovered after close", nil),
		)
	}
	if hooks.afterClose != nil {
		if err := runPublicationBarrier(hooks, hooks.afterClose); err != nil {
			if errors.Is(err, errPublicationScopeInvalid) {
				return partial, err
			}
			spec, captureErr := capturePublicationFile(context.WithoutCancel(ctx), parent, privateName)
			if captureErr == nil && spec.info != nil && os.SameFile(created, spec.info) {
				spec.created = true
				return cleanupKnownPrivate(err, spec)
			}
			return partial, errors.Join(
				err,
				captureErr,
				publicationConflict("created artifact identity cannot be recovered after close barrier", nil),
			)
		}
	}
	info, err := parent.Lstat(privateName)
	if err != nil || !os.SameFile(created, info) {
		return partial, publicationConflict("created artifact identity changed", err)
	}
	expected := publicationFileSpec{
		info:     info,
		mode:     publicationPreservedMode(mode),
		size:     int64(len(data)),
		digest:   sha256.Sum256(data),
		data:     append([]byte(nil), data...),
		created:  true,
		complete: true,
	}
	if err := verifyPublicationFile(ctx, parent, privateName, expected, true); err != nil {
		return cleanupKnownPrivate(err, expected)
	}
	if hooks.afterVerify != nil {
		if err := runPublicationBarrier(hooks, hooks.afterVerify); err != nil {
			if errors.Is(err, errPublicationScopeInvalid) {
				return expected, err
			}
			return cleanupKnownPrivate(err, expected)
		}
	}
	if err := verifyPublicationFile(ctx, parent, privateName, expected, true); err != nil {
		return cleanupKnownPrivate(err, expected)
	}
	// Make the private inode durable before exposing the canonical protocol
	// name. This first sync is deliberately non-injectable: error-injection
	// hooks model the public barrier below, after mutation becomes observable.
	if err := validatePublicationScope(hooks); err != nil {
		return expected, err
	}
	if err := syncPublicationDirectoryPlatform(parent); err != nil {
		return cleanupKnownPrivate(err, expected)
	}
	if err := validatePublicationScope(hooks); err != nil {
		return expected, err
	}
	if err := validatePublicationMutationContext(ctx, hooks); err != nil {
		return cleanupKnownPrivate(err, expected)
	}
	if err := renamePublicationNoReplace(parent, privateName, name); err != nil {
		return cleanupKnownPrivate(err, expected)
	}
	canonical, captureErr := capturePublicationFile(context.WithoutCancel(ctx), parent, name)
	if captureErr != nil ||
		canonical.info == nil ||
		!os.SameFile(expected.info, canonical.info) {
		return expected, errors.Join(
			captureErr,
			publicationConflict("published artifact identity changed", nil),
		)
	}
	canonical.created = true
	canonical.complete = true
	expected = canonical
	if err := syncPublicationDirectory(parent, publicationBarrierHooks{}); err != nil {
		return expected, err
	}
	if hooks.afterDirectorySync != nil {
		if err := runPublicationBarrier(hooks, hooks.afterDirectorySync); err != nil {
			return expected, err
		}
	}
	return expected, verifyPublicationFile(
		context.WithoutCancel(ctx),
		parent,
		name,
		expected,
		true,
	)
}

// createDurablePublicationCopy persists an independent inode containing the
// exact captured source bytes and mode. It is the preimage primitive for
// transactions whose original inode can still be mutated through another
// open descriptor after the public name has been vacated.
func createDurablePublicationCopy(
	ctx context.Context,
	parent *os.Root,
	sourceName string,
	source publicationFileSpec,
	copyName string,
	hooks publicationBarrierHooks,
) (publicationFileSpec, error) {
	if source.info == nil || !source.complete {
		return publicationFileSpec{}, publicationConflict("durable copy source is incomplete", nil)
	}
	if err := verifyPublicationFile(ctx, parent, sourceName, source, true); err != nil {
		return publicationFileSpec{}, err
	}
	copied, createErr := createPublicationArtifact(
		ctx,
		parent,
		copyName,
		source.mode,
		source.data,
		hooks,
	)
	if createErr != nil {
		return copied, createErr
	}
	convergence := context.WithoutCancel(ctx)
	if copied.info == nil || !copied.complete {
		return copied, publicationConflict("durable copy is incomplete", nil)
	}
	if os.SameFile(source.info, copied.info) {
		return copied, publicationConflict("durable copy aliases its mutable source inode", nil)
	}
	if err := verifyPublicationFile(convergence, parent, sourceName, source, true); err != nil {
		return copied, publicationConflict("durable copy source changed during capture", err)
	}
	if err := verifyPublicationFile(convergence, parent, copyName, copied, true); err != nil {
		return copied, err
	}
	return copied, nil
}

// createDurablePublicationWitness retains the exact source inode under a
// manifest-bound protocol name. Unlike a backup copy, this witness exists only
// to prove that an untouched public leaf still has the captured identity.
func createDurablePublicationWitness(
	ctx context.Context,
	parent *os.Root,
	sourceName string,
	source publicationFileSpec,
	witnessName string,
	hooks publicationBarrierHooks,
) (publicationFileSpec, error) {
	hooks.scopePhase = publicationScopeCreate
	if source.info == nil || !source.complete {
		return publicationFileSpec{}, publicationConflict("durable witness source is incomplete", nil)
	}
	if err := verifyPublicationFile(ctx, parent, sourceName, source, true); err != nil {
		return publicationFileSpec{}, err
	}
	if err := syncPublicationPath(
		ctx,
		parent,
		sourceName,
		source,
		false,
		publicationBarrierHooks{},
	); err != nil {
		return publicationFileSpec{}, err
	}
	if _, err := parent.Lstat(witnessName); !errors.Is(err, fs.ErrNotExist) {
		return publicationFileSpec{}, publicationConflict("durable witness path is occupied", err)
	}
	if err := validatePublicationScope(hooks); err != nil {
		return publicationFileSpec{}, err
	}
	if err := validatePublicationMutationContext(ctx, hooks); err != nil {
		return publicationFileSpec{}, err
	}
	if err := parent.Link(sourceName, witnessName); err != nil {
		return publicationFileSpec{}, err
	}
	convergence := context.WithoutCancel(ctx)
	witness, captureErr := capturePublicationFile(convergence, parent, witnessName)
	witness.created = true
	if captureErr != nil ||
		witness.info == nil ||
		!os.SameFile(source.info, witness.info) {
		return witness, errors.Join(
			captureErr,
			publicationConflict("durable witness did not retain source identity", nil),
		)
	}
	if err := syncPublicationPath(
		convergence,
		parent,
		witnessName,
		witness,
		false,
		hooks,
	); err != nil {
		return witness, err
	}
	if err := syncPublicationDirectory(parent, hooks); err != nil {
		return witness, err
	}
	if hooks.afterDirectorySync != nil {
		if err := runPublicationBarrier(hooks, hooks.afterDirectorySync); err != nil {
			return witness, err
		}
	}
	if err := verifyPublicationFile(convergence, parent, sourceName, source, true); err != nil {
		return witness, err
	}
	return witness, verifyPublicationFile(convergence, parent, witnessName, witness, true)
}

func guardedVacatePublicationLeaf(
	ctx context.Context,
	parent *os.Root,
	leaf string,
	expected publicationFileSpec,
	claimName string,
	hooks publicationBarrierHooks,
) (publicationFileSpec, error) {
	return guardedVacatePublicationLeafWithIdentity(
		ctx,
		parent,
		leaf,
		expected,
		false,
		claimName,
		hooks,
	)
}

func guardedVacatePublicationLeafWithCompensation(
	ctx context.Context,
	parent *os.Root,
	leaf string,
	expected publicationFileSpec,
	claimName string,
	hooks publicationBarrierHooks,
) (publicationFileSpec, error) {
	outcome, vacateErr := guardedVacatePublicationLeafWithOutcome(
		ctx,
		parent,
		leaf,
		expected,
		claimName,
		hooks,
	)
	return outcome.claimed, vacateErr
}

func guardedVacatePublicationLeafWithOutcome(
	ctx context.Context,
	parent *os.Root,
	leaf string,
	expected publicationFileSpec,
	claimName string,
	hooks publicationBarrierHooks,
) (publicationVacateOutcome, error) {
	// The raw outcome separates an in-memory observation of rename success
	// from the durable receipt required by recovery protocols. Before that
	// receipt, every returned error converges the claim back to the target.
	outcome, vacateErr := guardedVacatePublicationLeafWithIdentityOutcome(
		ctx,
		parent,
		leaf,
		expected,
		false,
		claimName,
		hooks,
	)
	if vacateErr == nil || !outcome.renamed || outcome.durableReceipt {
		return outcome, vacateErr
	}
	return compensatePublicationVacateBeforeReceipt(
		ctx,
		parent,
		leaf,
		claimName,
		expected,
		outcome,
		vacateErr,
		hooks,
	)
}

func compensatePublicationVacateBeforeReceipt(
	ctx context.Context,
	parent *os.Root,
	leaf string,
	claimName string,
	expected publicationFileSpec,
	outcome publicationVacateOutcome,
	cause error,
	hooks publicationBarrierHooks,
) (publicationVacateOutcome, error) {
	convergence := context.WithoutCancel(ctx)
	hooks.scopePhase = publicationScopeVacate
	claimed, captureErr := capturePublicationFile(convergence, parent, claimName)
	if captureErr != nil {
		outcome.claimed.created = false
		if _, statErr := parent.Lstat(claimName); statErr == nil {
			outcome.claimed.created = true
		}
		return outcome, errors.Join(
			cause,
			publicationConflict(
				"vacate compensation claim is unavailable",
				captureErr,
			),
		)
	}
	claimed.created = true
	outcome.claimed = claimed

	if !publicationVacateClaimMatches(expected, claimed, false) {
		return outcome, errors.Join(
			cause,
			publicationConflict(
				"vacate compensation claim changed before convergence",
				nil,
			),
		)
	}
	if _, err := parent.Lstat(leaf); !errors.Is(err, fs.ErrNotExist) {
		return outcome, errors.Join(
			cause,
			publicationConflict(
				"vacate compensation is blocked by an occupied target",
				err,
			),
		)
	}

	if err := validatePublicationCompensationScope(hooks); err != nil {
		return outcome, errors.Join(
			cause,
			publicationConflict(
				"vacate compensation scope changed",
				err,
			),
		)
	}
	if err := validatePublicationMutationContext(convergence, hooks); err != nil {
		return outcome, errors.Join(cause, err)
	}
	if err := renamePublicationNoReplace(parent, claimName, leaf); err != nil {
		if recaptured, recaptureErr := capturePublicationFile(
			convergence,
			parent,
			claimName,
		); recaptureErr == nil {
			recaptured.created = true
			outcome.claimed = recaptured
		}
		return outcome, errors.Join(
			cause,
			publicationConflict(
				"vacate compensation failed",
				err,
			),
		)
	}

	outcome.claimed.created = false
	if err := syncPublicationDirectory(parent, publicationBarrierHooks{}); err != nil {
		return outcome, errors.Join(
			cause,
			publicationConflict("vacate compensation is not durable", err),
		)
	}
	restored, err := parent.Lstat(leaf)
	if err != nil ||
		outcome.claimed.info == nil ||
		!os.SameFile(outcome.claimed.info, restored) {
		return outcome, errors.Join(
			cause,
			publicationConflict("vacate compensation changed identity", err),
		)
	}
	if _, err := parent.Lstat(claimName); !errors.Is(err, fs.ErrNotExist) {
		return outcome, errors.Join(
			cause,
			publicationConflict("vacate claim survived compensation", err),
		)
	}
	return outcome, errors.Join(
		cause,
		publicationConflict("vacate was compensated before durable receipt", nil),
	)
}

func guardedVacatePublicationLeafIdentity(
	ctx context.Context,
	parent *os.Root,
	leaf string,
	expectedIdentity os.FileInfo,
	claimName string,
	hooks publicationBarrierHooks,
) (publicationFileSpec, error) {
	return guardedVacatePublicationLeafWithIdentity(
		ctx,
		parent,
		leaf,
		publicationFileSpec{info: expectedIdentity},
		true,
		claimName,
		hooks,
	)
}

func guardedVacatePublicationLeafWithIdentity(
	ctx context.Context,
	parent *os.Root,
	leaf string,
	expected publicationFileSpec,
	identityOnly bool,
	claimName string,
	hooks publicationBarrierHooks,
) (publicationFileSpec, error) {
	outcome, err := guardedVacatePublicationLeafWithIdentityOutcome(
		ctx,
		parent,
		leaf,
		expected,
		identityOnly,
		claimName,
		hooks,
	)
	return outcome.claimed, err
}

func guardedVacatePublicationLeafWithIdentityOutcome(
	ctx context.Context,
	parent *os.Root,
	leaf string,
	expected publicationFileSpec,
	identityOnly bool,
	claimName string,
	hooks publicationBarrierHooks,
) (publicationVacateOutcome, error) {
	hooks.scopePhase = publicationScopeVacate
	if expected.info == nil {
		return publicationVacateOutcome{}, publicationConflict("vacate identity is unavailable", nil)
	}
	verifySource := func() error {
		if identityOnly {
			info, err := parent.Lstat(leaf)
			if err != nil ||
				info.Mode()&os.ModeSymlink != 0 ||
				!info.Mode().IsRegular() ||
				!os.SameFile(expected.info, info) {
				return publicationConflict("vacate source identity or type changed", err)
			}
			return nil
		}
		return verifyPublicationFile(ctx, parent, leaf, expected, true)
	}
	if err := verifySource(); err != nil {
		return publicationVacateOutcome{}, err
	}
	if _, err := parent.Lstat(claimName); !errors.Is(err, fs.ErrNotExist) {
		return publicationVacateOutcome{}, publicationConflict("ownership claim path is occupied", err)
	}
	if hooks.beforeVacate != nil {
		if err := runPublicationBarrier(hooks, hooks.beforeVacate); err != nil {
			return publicationVacateOutcome{}, err
		}
	}
	if err := verifySource(); err != nil {
		return publicationVacateOutcome{}, err
	}
	if _, err := parent.Lstat(claimName); !errors.Is(err, fs.ErrNotExist) {
		return publicationVacateOutcome{}, publicationConflict("ownership claim path appeared", err)
	}
	if err := syncPublicationPath(
		ctx,
		parent,
		leaf,
		expected,
		identityOnly,
		hooks,
	); err != nil {
		return publicationVacateOutcome{}, err
	}
	if err := verifySource(); err != nil {
		return publicationVacateOutcome{}, err
	}
	if _, err := parent.Lstat(claimName); !errors.Is(err, fs.ErrNotExist) {
		return publicationVacateOutcome{}, publicationConflict("ownership claim path appeared during source sync", err)
	}
	// The platform primitive acquires the recovery claim atomically without
	// replacement. Platforms that cannot provide rooted no-replace rename fail
	// closed before mutating the public leaf.
	if err := validatePublicationScope(hooks); err != nil {
		return publicationVacateOutcome{}, err
	}
	if err := validatePublicationMutationContext(ctx, hooks); err != nil {
		return publicationVacateOutcome{}, err
	}
	if err := renamePublicationNoReplace(parent, leaf, claimName); err != nil {
		return publicationVacateOutcome{}, err
	}
	outcome := publicationVacateOutcome{
		claimed: publicationFileSpec{created: true},
		renamed: true,
	}
	convergence := context.WithoutCancel(ctx)
	if hooks.afterVacateRename != nil {
		if err := runPublicationBarrier(hooks, hooks.afterVacateRename); err != nil {
			return outcome, err
		}
	}
	if _, err := parent.Lstat(leaf); !errors.Is(err, fs.ErrNotExist) {
		return outcome, publicationConflict("vacated leaf reappeared", err)
	}
	syncErr := syncPublicationDirectory(parent, hooks)
	captured, captureErr := capturePublicationFile(convergence, parent, claimName)
	outcome.claimed = captured
	outcome.claimed.created = true
	if captureErr != nil {
		return outcome, errors.Join(syncErr, captureErr)
	}
	restoreDisplacedClaim := func(
		candidate publicationFileSpec,
		cause error,
	) (publicationFileSpec, error) {
		// A non-cooperative writer won or mutated the source through an
		// already-open descriptor at the final vacate boundary. Keep that
		// exact foreign inode under the manifest-bound ownership claim. The
		// protocol-specific caller must restore its independent durable
		// preimage and must never publish this changed claim as the original.
		if err := syncPublicationPath(
			convergence,
			parent,
			claimName,
			candidate,
			false,
			hooks,
		); err != nil {
			return candidate, errors.Join(
				cause,
				publicationConflict("foreign leaf recovery source is not durable", err),
			)
		}
		return candidate, errors.Join(
			cause,
			publicationConflict("changed vacate source was retained under its ownership claim", nil),
		)
	}
	if !publicationVacateClaimMatches(expected, outcome.claimed, identityOnly) {
		retained, retainErr := restoreDisplacedClaim(outcome.claimed, syncErr)
		outcome.claimed = retained
		return outcome, retainErr
	}
	if syncErr != nil {
		return outcome, syncErr
	}
	// The claim now has the exact expected identity and payload, the public
	// target is absent, and the parent rename has reached stable storage.
	outcome.durableReceipt = true
	if hooks.afterVacate != nil {
		if err := runPublicationBarrier(hooks, hooks.afterVacate); err != nil {
			return outcome, err
		}
	}
	refreshed, refreshErr := capturePublicationFile(convergence, parent, claimName)
	if refreshErr != nil {
		return outcome, refreshErr
	}
	refreshed.created = true
	if !publicationVacateClaimMatches(expected, refreshed, identityOnly) {
		retained, retainErr := restoreDisplacedClaim(
			refreshed,
			publicationConflict("ownership claim changed after vacate barrier", nil),
		)
		outcome.claimed = retained
		return outcome, retainErr
	}
	outcome.claimed = refreshed
	if _, err := parent.Lstat(leaf); !errors.Is(err, fs.ErrNotExist) {
		return outcome, publicationConflict("vacated leaf was claimed by a foreign writer", err)
	}
	return outcome, nil
}

func guardedInstallPublicationLeaf(
	ctx context.Context,
	parent *os.Root,
	stageName string,
	stage publicationFileSpec,
	leaf string,
	hooks publicationBarrierHooks,
) (installed bool, err error) {
	hooks.scopePhase = publicationScopeInstall
	if err := verifyPublicationFile(ctx, parent, stageName, stage, true); err != nil {
		return false, err
	}
	if _, err := parent.Lstat(leaf); !errors.Is(err, fs.ErrNotExist) {
		return false, publicationConflict("publication leaf is occupied", err)
	}
	if hooks.beforeInstall != nil {
		if err := runPublicationBarrier(hooks, hooks.beforeInstall); err != nil {
			return false, err
		}
	}
	if err := verifyPublicationFile(ctx, parent, stageName, stage, true); err != nil {
		return false, err
	}
	if _, err := parent.Lstat(leaf); !errors.Is(err, fs.ErrNotExist) {
		return false, publicationConflict("publication leaf was claimed before install", err)
	}
	if err := syncPublicationPath(
		ctx,
		parent,
		stageName,
		stage,
		false,
		hooks,
	); err != nil {
		return false, err
	}
	if _, err := parent.Lstat(leaf); !errors.Is(err, fs.ErrNotExist) {
		return false, publicationConflict("publication leaf was claimed while syncing install source", err)
	}
	// Link is no-replace on every supported os.Root platform. A
	// non-cooperative writer racing after the preceding observation can make
	// the link fail, but cannot be overwritten by this publisher.
	if err := validatePublicationScope(hooks); err != nil {
		return false, err
	}
	if err := validatePublicationMutationContext(ctx, hooks); err != nil {
		return false, err
	}
	if err := parent.Link(stageName, leaf); err != nil {
		return false, err
	}
	installed = true
	convergence := context.WithoutCancel(ctx)
	if hooks.afterInstall != nil {
		if err := runPublicationBarrier(hooks, hooks.afterInstall); err != nil {
			return true, err
		}
	}
	if err := verifyPublicationFile(convergence, parent, leaf, stage, true); err != nil {
		return true, err
	}
	if err := syncPublicationDirectory(parent, hooks); err != nil {
		return true, err
	}
	return true, verifyPublicationFile(convergence, parent, leaf, stage, true)
}

func guardedConsumePublicationInstallLeaf(
	ctx context.Context,
	parent *os.Root,
	installName string,
	install publicationFileSpec,
	witnessName string,
	witness publicationFileSpec,
	leaf string,
	hooks publicationBarrierHooks,
) (installed bool, err error) {
	hooks.scopePhase = publicationScopeInstall
	operationCtx := ctx
	verifySources := func() error {
		if install.info == nil ||
			witness.info == nil ||
			!os.SameFile(install.info, witness.info) {
			return publicationConflict("consuming install token is detached from its witness", nil)
		}
		if err := verifyPublicationFile(
			operationCtx,
			parent,
			installName,
			install,
			true,
		); err != nil {
			return err
		}
		return verifyPublicationFile(
			operationCtx,
			parent,
			witnessName,
			witness,
			true,
		)
	}
	if err := verifySources(); err != nil {
		return false, err
	}
	if _, err := parent.Lstat(leaf); !errors.Is(err, fs.ErrNotExist) {
		return false, publicationConflict("publication leaf is occupied", err)
	}
	if hooks.beforeInstall != nil {
		if err := runPublicationBarrier(hooks, hooks.beforeInstall); err != nil {
			return false, err
		}
	}
	if err := verifySources(); err != nil {
		return false, err
	}
	if _, err := parent.Lstat(leaf); !errors.Is(err, fs.ErrNotExist) {
		return false, publicationConflict("publication leaf was claimed before install", err)
	}
	if err := syncPublicationPath(
		ctx,
		parent,
		installName,
		install,
		false,
		hooks,
	); err != nil {
		return false, err
	}
	if _, err := parent.Lstat(leaf); !errors.Is(err, fs.ErrNotExist) {
		return false, publicationConflict("publication leaf was claimed while syncing install source", err)
	}
	if err := validatePublicationScope(hooks); err != nil {
		return false, err
	}
	if err := validatePublicationMutationContext(ctx, hooks); err != nil {
		return false, err
	}
	if err := renamePublicationNoReplace(parent, installName, leaf); err != nil {
		return false, err
	}
	installed = true
	convergence := context.WithoutCancel(ctx)
	operationCtx = convergence
	if hooks.afterInstall != nil {
		if err := runPublicationBarrier(hooks, hooks.afterInstall); err != nil {
			return true, err
		}
	}
	if err := verifyPublicationFile(convergence, parent, leaf, install, true); err != nil {
		return true, err
	}
	if err := verifyPublicationFile(convergence, parent, witnessName, witness, true); err != nil {
		return true, err
	}
	if _, err := parent.Lstat(installName); !errors.Is(err, fs.ErrNotExist) {
		return true, publicationConflict("consumed install token still exists", err)
	}
	if err := syncPublicationDirectory(parent, hooks); err != nil {
		return true, err
	}
	if hooks.afterDirectorySync != nil {
		if err := runPublicationBarrier(hooks, hooks.afterDirectorySync); err != nil {
			return true, err
		}
	}
	if err := verifyPublicationFile(convergence, parent, witnessName, witness, true); err != nil {
		return true, err
	}
	if _, err := parent.Lstat(installName); !errors.Is(err, fs.ErrNotExist) {
		return true, publicationConflict("consumed install token reappeared", err)
	}
	return true, verifyPublicationFile(convergence, parent, leaf, install, true)
}

func guardedRemovePublicationFile(
	ctx context.Context,
	parent *os.Root,
	name string,
	expected publicationFileSpec,
	hooks publicationBarrierHooks,
) (removed bool, err error) {
	return guardedRemovePublicationFileWithIdentityAs(
		ctx,
		parent,
		name,
		name,
		expected,
		false,
		hooks,
	)
}

func guardedRemovePublicationFileIdentity(
	ctx context.Context,
	parent *os.Root,
	name string,
	expectedIdentity os.FileInfo,
	hooks publicationBarrierHooks,
) (removed bool, err error) {
	return guardedRemovePublicationFileWithIdentityAs(
		ctx,
		parent,
		name,
		name,
		publicationFileSpec{info: expectedIdentity},
		true,
		hooks,
	)
}

func guardedRemovePublicationFileAs(
	ctx context.Context,
	parent *os.Root,
	physicalName string,
	canonicalProtocolName string,
	expected publicationFileSpec,
	hooks publicationBarrierHooks,
) (removed bool, err error) {
	return guardedRemovePublicationFileWithIdentityAs(
		ctx,
		parent,
		physicalName,
		canonicalProtocolName,
		expected,
		false,
		hooks,
	)
}

func guardedRemovePublicationFileWithIdentityAs(
	ctx context.Context,
	parent *os.Root,
	physicalName string,
	canonicalProtocolName string,
	expected publicationFileSpec,
	identityOnly bool,
	hooks publicationBarrierHooks,
) (removed bool, err error) {
	nameDigest, err := privatePublicationClaimDigest(canonicalProtocolName)
	if err != nil {
		return false, err
	}
	return guardedRemovePublicationFileWithIdentityDigest(
		ctx,
		parent,
		physicalName,
		nameDigest,
		expected,
		identityOnly,
		hooks,
	)
}

func guardedRemovePublicationFileWithIdentityDigest(
	ctx context.Context,
	parent *os.Root,
	physicalName string,
	nameDigest string,
	expected publicationFileSpec,
	identityOnly bool,
	hooks publicationBarrierHooks,
) (removed bool, err error) {
	hooks.scopePhase = publicationScopeRemove
	if expected.info == nil {
		return false, publicationConflict("removal identity is unavailable", nil)
	}
	verifySource := func() error {
		if identityOnly {
			info, err := parent.Lstat(physicalName)
			if err != nil ||
				info.Mode()&os.ModeSymlink != 0 ||
				!info.Mode().IsRegular() ||
				!os.SameFile(expected.info, info) {
				return publicationConflict("removal source identity or type changed", err)
			}
			return nil
		}
		return verifyPublicationFile(ctx, parent, physicalName, expected, true)
	}
	if err := verifySource(); err != nil {
		return false, err
	}
	privateClaim, err := allocatePrivatePublicationClaimForDigest(parent, nameDigest)
	if err != nil {
		return false, err
	}
	if hooks.beforeRemove != nil {
		if err := runPublicationBarrier(hooks, hooks.beforeRemove); err != nil {
			return false, err
		}
	}
	if err := verifySource(); err != nil {
		return false, err
	}
	if _, err := parent.Lstat(privateClaim); !errors.Is(err, fs.ErrNotExist) {
		return false, publicationConflict("private removal claim appeared", err)
	}
	// The public name is never unlinked. Rename quarantines whichever inode
	// occupies it at the syscall boundary under an unguessable reserved name.
	if err := validatePublicationScope(hooks); err != nil {
		return false, err
	}
	if err := validatePublicationMutationContext(ctx, hooks); err != nil {
		return false, err
	}
	if err := renamePublicationNoReplace(parent, physicalName, privateClaim); err != nil {
		return false, err
	}
	removed = true
	convergence := context.WithoutCancel(ctx)
	claimed, captureErr := capturePublicationFile(convergence, parent, privateClaim)
	if captureErr != nil {
		return true, captureErr
	}
	if expected.info == nil || !os.SameFile(expected.info, claimed.info) {
		// A foreign inode won the last pre-rename window. Restore it with a
		// no-replace link; if restoration is blocked, retain the quarantine.
		if _, err := parent.Lstat(physicalName); !errors.Is(err, fs.ErrNotExist) {
			return true, publicationConflict("foreign artifact cannot be restored because its public name is occupied", err)
		}
		if err := syncPublicationPath(
			convergence,
			parent,
			privateClaim,
			claimed,
			false,
			hooks,
		); err != nil {
			return true, publicationConflict("foreign artifact quarantine is not durable", err)
		}
		if err := validatePublicationScope(hooks); err != nil {
			return true, err
		}
		if err := validatePublicationMutationContext(convergence, hooks); err != nil {
			return true, err
		}
		if err := parent.Link(privateClaim, physicalName); err != nil {
			return true, publicationConflict("foreign artifact could not be restored", err)
		}
		removed = false
		if err := verifyPublicationFile(convergence, parent, physicalName, claimed, true); err != nil {
			return false, publicationConflict("restored foreign artifact is not exact", err)
		}
		if err := syncPublicationDirectory(parent, hooks); err != nil {
			return false, publicationConflict("restored foreign artifact is not durable", err)
		}
		// Only the proven private hardlink is unlinked. The public foreign inode
		// remains attached through name.
		if err := validatePublicationScope(hooks); err != nil {
			return false, err
		}
		if err := validatePublicationMutationContext(convergence, hooks); err != nil {
			return false, err
		}
		if err := parent.Remove(privateClaim); err != nil {
			return false, publicationConflict("foreign artifact quarantine could not be released", err)
		}
		if err := syncPublicationDirectory(parent, hooks); err != nil {
			return false, publicationConflict("foreign artifact quarantine removal is not durable", err)
		}
		return false, publicationConflict("foreign artifact displaced at cleanup boundary", nil)
	}
	// The quarantine name is random, reserved, and was created by the rename
	// above. After the post-syscall SameFile proof it is the only pathname the
	// cleanup protocol ever unlinks.
	if err := validatePublicationScope(hooks); err != nil {
		return true, err
	}
	if err := validatePublicationMutationContext(convergence, hooks); err != nil {
		return true, err
	}
	if err := parent.Remove(privateClaim); err != nil {
		return true, err
	}
	if err := syncPublicationDirectory(parent, hooks); err != nil {
		return true, err
	}
	if hooks.afterRemove != nil {
		if err := runPublicationBarrier(hooks, hooks.afterRemove); err != nil {
			return true, err
		}
	}
	if _, err := parent.Lstat(physicalName); !errors.Is(err, fs.ErrNotExist) {
		return true, publicationConflict("removed publication artifact was replaced", err)
	}
	return true, nil
}

func allocatePrivatePublicationClaim(parent *os.Root, canonicalName string) (string, error) {
	nameDigest, err := privatePublicationClaimDigest(canonicalName)
	if err != nil {
		return "", err
	}
	return allocatePrivatePublicationClaimForDigest(parent, nameDigest)
}

func privatePublicationClaimDigest(canonicalName string) (string, error) {
	if !validPublicationRenameLeaf(canonicalName) ||
		canonicalName != path.Base(canonicalName) ||
		hasASCIIFoldPrefix(canonicalName, publicationPrivateClaimPrefix) {
		return "", publicationConflict("private claim source is not a canonical publication artifact", nil)
	}
	nameDigest := sha256.Sum256([]byte(canonicalName))
	return hex.EncodeToString(nameDigest[:]), nil
}

func allocatePrivatePublicationClaimForDigest(parent *os.Root, nameDigest string) (string, error) {
	decodedDigest, err := hex.DecodeString(nameDigest)
	if err != nil ||
		len(decodedDigest) != sha256.Size ||
		hex.EncodeToString(decodedDigest) != nameDigest {
		return "", publicationConflict("private claim name digest is not canonical", err)
	}
	for attempt := 0; attempt < 32; attempt++ {
		random := make([]byte, 16)
		if _, err := rand.Read(random); err != nil {
			return "", err
		}
		name := publicationPrivateClaimPrefix +
			nameDigest + "-" +
			hex.EncodeToString(random)
		if _, err := parent.Lstat(name); errors.Is(err, fs.ErrNotExist) {
			return name, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", errors.New("private publication claim namespace exhausted")
}

func syncPublicationFile(file *os.File, hooks publicationBarrierHooks) error {
	if err := validatePublicationScope(hooks); err != nil {
		return err
	}
	if hooks.fileSync != nil {
		syncErr := hooks.fileSync(file)
		return errors.Join(syncErr, validatePublicationScope(hooks))
	}
	return file.Sync()
}

func syncPublicationDirectory(parent *os.Root, hooks publicationBarrierHooks) error {
	if err := validatePublicationScope(hooks); err != nil {
		return err
	}
	if hooks.directorySync != nil {
		syncErr := hooks.directorySync(parent)
		return errors.Join(syncErr, validatePublicationScope(hooks))
	}
	return syncPublicationDirectoryPlatform(parent)
}

func publicationSpecFromBytes(info os.FileInfo, data []byte) publicationFileSpec {
	if info == nil {
		return publicationFileSpec{
			size:     int64(len(data)),
			digest:   sha256.Sum256(data),
			data:     append([]byte(nil), data...),
			complete: true,
		}
	}
	return publicationFileSpec{
		info:     info,
		mode:     publicationPreservedMode(info.Mode()),
		size:     int64(len(data)),
		digest:   sha256.Sum256(data),
		data:     append([]byte(nil), data...),
		complete: true,
	}
}

func publicationConflict(reason string, err error) error {
	if err == nil {
		return fmt.Errorf("%w: %s", errPublicationConflict, reason)
	}
	return fmt.Errorf("%w: %s: %w", errPublicationConflict, reason, err)
}
