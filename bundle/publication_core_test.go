package bundle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicationCoreIndexBackgroundCallerStillPublishes(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeIndexDoc(t, root, "concept.md", "Concept", "Background publication", "runtime regression")

	// Act.
	written, err := RegenerateIndexes(root)

	// Assert.
	if err != nil || len(written) != 1 || filepath.Base(written[0]) != "index.md" {
		t.Fatalf("RegenerateIndexes() written=%v error=%v", written, err)
	}
	index := readFile(t, root, "index.md")
	if !strings.Contains(index, "[Background publication](concept.md) - runtime regression") {
		t.Fatalf("index.md did not publish expected entry: %q", index)
	}
	entries, readErr := os.ReadDir(root)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".okf-") {
			t.Fatalf("publication residue retained: %q", entry.Name())
		}
	}
}

func TestCreatePublicationArtifactCapturesIdentityBeforeAfterOpen(t *testing.T) {
	t.Parallel()

	// Arrange.
	rootPath := t.TempDir()
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	injected := errors.New("injected after-open failure")
	afterOpen := false

	// Act.
	spec, err := createPublicationArtifact(
		context.Background(),
		root,
		"artifact",
		0o640,
		[]byte("published bytes\n"),
		publicationBarrierHooks{
			afterOpen: func(file *os.File) error {
				afterOpen = true
				if closeErr := file.Close(); closeErr != nil {
					t.Fatal(closeErr)
				}
				return injected
			},
		},
	)

	// Assert.
	if !afterOpen || spec.created || !errors.Is(err, injected) {
		t.Fatalf("afterOpen=%t spec=%+v error=%v; want converged after-open failure", afterOpen, spec, err)
	}
	if names := publicationCoreArtifactNames(t, rootPath); len(names) != 0 {
		t.Fatalf("artifact names after closed-FD failure = %#v, want none", names)
	}

	retry, retryErr := createPublicationArtifact(
		context.Background(),
		root,
		"artifact",
		0o640,
		[]byte("published bytes\n"),
		publicationBarrierHooks{},
	)
	if retryErr != nil || !retry.created || !retry.complete {
		t.Fatalf("retry spec=%+v error=%v; want complete publication", retry, retryErr)
	}
	if got, readErr := os.ReadFile(filepath.Join(rootPath, "artifact")); readErr != nil ||
		string(got) != "published bytes\n" {
		t.Fatalf("published artifact=%q error=%v", got, readErr)
	}
}

func TestCreatePublicationArtifactRetriesInitialIdentityStatBeforeAfterOpen(t *testing.T) {
	t.Parallel()

	// Arrange.
	rootPath := t.TempDir()
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	injected := errors.New("injected first identity stat failure")
	statCalls := 0
	afterOpen := false

	// Act.
	spec, err := createPublicationArtifact(
		context.Background(),
		root,
		"artifact",
		0o640,
		[]byte("published bytes\n"),
		publicationBarrierHooks{
			fileStat: func(file *os.File) (os.FileInfo, error) {
				statCalls++
				if statCalls == 1 {
					return nil, injected
				}
				return file.Stat()
			},
			afterOpen: func(*os.File) error {
				afterOpen = true
				return nil
			},
		},
	)

	// Assert.
	if err != nil || !spec.created || !spec.complete || statCalls != 2 || !afterOpen {
		t.Fatalf(
			"spec=%+v error=%v statCalls=%d afterOpen=%t; want retry then complete",
			spec,
			err,
			statCalls,
			afterOpen,
		)
	}
	if got, readErr := os.ReadFile(filepath.Join(rootPath, "artifact")); readErr != nil ||
		string(got) != "published bytes\n" {
		t.Fatalf("published artifact=%q error=%v", got, readErr)
	}
}

func TestCreatePublicationArtifactPersistentUnknownIdentityRetainsFailClosedPrivateClaim(t *testing.T) {
	t.Parallel()

	// Arrange.
	rootPath := t.TempDir()
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	injected := errors.New("injected persistent identity stat failure")
	statCalls := 0
	afterOpen := false

	// Act.
	spec, err := createPublicationArtifact(
		context.Background(),
		root,
		"artifact",
		0o640,
		[]byte("unpublished bytes\n"),
		publicationBarrierHooks{
			fileStat: func(*os.File) (os.FileInfo, error) {
				statCalls++
				return nil, injected
			},
			afterOpen: func(*os.File) error {
				afterOpen = true
				return nil
			},
		},
	)

	// Assert.
	if !spec.created || spec.info != nil || !errors.Is(err, injected) ||
		!errors.Is(err, errPublicationConflict) || statCalls != 2 || afterOpen {
		t.Fatalf(
			"spec=%+v error=%v statCalls=%d afterOpen=%t; want retained unknown identity",
			spec,
			err,
			statCalls,
			afterOpen,
		)
	}
	if _, statErr := os.Lstat(filepath.Join(rootPath, "artifact")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("canonical artifact Lstat error=%v, want not exist", statErr)
	}
	if names := publicationCoreArtifactNames(t, rootPath); len(names) != 1 ||
		!strings.HasPrefix(names[0], publicationPrivateClaimPrefix) {
		t.Fatalf("artifact names=%#v, want one retained private claim", names)
	}

	before := documentSessionCaptureTree(t, rootPath)
	written, recoveryErr := RegenerateIndexes(rootPath)
	if written != nil || recoveryErr == nil || !errors.Is(recoveryErr, errPublicationConflict) {
		t.Fatalf("recovery RegenerateIndexes()=%#v, %v; want fail-closed blocker", written, recoveryErr)
	}
	documentSessionAssertTreeSnapshotEqual(t, before, documentSessionCaptureTree(t, rootPath))
	repeated, repeatedErr := RegenerateIndexes(rootPath)
	if repeated != nil || repeatedErr == nil || repeatedErr.Error() != recoveryErr.Error() {
		t.Fatalf("repeated RegenerateIndexes()=%#v, %v; want stable blocker %v", repeated, repeatedErr, recoveryErr)
	}
	documentSessionAssertTreeSnapshotEqual(t, before, documentSessionCaptureTree(t, rootPath))
}

func TestCreatePublicationArtifactAfterOpenSwapPreservesForeignPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		replace func(t *testing.T, root *os.Root, rootPath, privateName string)
	}{
		{
			name: "regular",
			replace: func(t *testing.T, root *os.Root, _ string, privateName string) {
				t.Helper()
				if err := root.Remove(privateName); err != nil {
					t.Fatal(err)
				}
				if err := root.WriteFile(privateName, []byte("foreign private inode\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symlink",
			replace: func(t *testing.T, root *os.Root, rootPath, privateName string) {
				t.Helper()
				if err := root.Remove(privateName); err != nil {
					t.Fatal(err)
				}
				external := filepath.Join(t.TempDir(), "external")
				if err := os.WriteFile(external, []byte("foreign symlink target\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(external, filepath.Join(rootPath, privateName)); err != nil {
					t.Skipf("symlinks are unavailable: %v", err)
				}
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			rootPath := t.TempDir()
			root, err := os.OpenRoot(rootPath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = root.Close() })
			injected := errors.New("injected after-open swap")
			privateName := ""

			// Act.
			spec, err := createPublicationArtifact(
				context.Background(),
				root,
				"artifact",
				0o640,
				[]byte("publication payload\n"),
				publicationBarrierHooks{
					afterOpen: func(*os.File) error {
						privateName = publicationCoreSinglePrivateName(t, rootPath)
						test.replace(t, root, rootPath, privateName)
						return injected
					},
				},
			)

			// Assert.
			if privateName == "" || !spec.created || !errors.Is(err, injected) ||
				!errors.Is(err, errPublicationConflict) {
				t.Fatalf("private=%q spec=%+v error=%v; want retained ownership conflict", privateName, spec, err)
			}
			if _, statErr := os.Lstat(filepath.Join(rootPath, "artifact")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("canonical artifact Lstat error=%v, want not exist", statErr)
			}
			if _, statErr := os.Lstat(filepath.Join(rootPath, privateName)); statErr != nil {
				t.Fatalf("foreign private path was removed: %v", statErr)
			}
			switch test.name {
			case "regular":
				got, readErr := os.ReadFile(filepath.Join(rootPath, privateName))
				if readErr != nil || string(got) != "foreign private inode\n" {
					t.Fatalf("foreign regular=%q error=%v", got, readErr)
				}
			case "symlink":
				info, statErr := os.Lstat(filepath.Join(rootPath, privateName))
				if statErr != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Fatalf("foreign symlink info=%v error=%v", info, statErr)
				}
			}
		})
	}
}

func publicationCoreArtifactNames(t *testing.T, rootPath string) []string {
	t.Helper()
	entries, err := os.ReadDir(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		if entry.Name() == "artifact" || strings.HasPrefix(entry.Name(), publicationPrivateClaimPrefix) {
			names = append(names, entry.Name())
		}
	}
	return names
}

func publicationCoreSinglePrivateName(t *testing.T, rootPath string) string {
	t.Helper()
	names := publicationCoreArtifactNames(t, rootPath)
	if len(names) != 1 || !strings.HasPrefix(names[0], publicationPrivateClaimPrefix) {
		t.Fatalf("publication artifacts=%#v, want one private claim", names)
	}
	return names[0]
}

const (
	publicationTestPreNew           publicationDescriptorPhase = "pre-new"
	publicationTestPreNewVacated    publicationDescriptorPhase = "pre-new-vacated"
	publicationTestPreNewAnchored   publicationDescriptorPhase = "pre-new-rollback-anchored"
	publicationTestPreNewReady      publicationDescriptorPhase = "pre-new-rollback-ready"
	publicationTestPreNewRestored   publicationDescriptorPhase = "pre-new-restored"
	publicationTestPublished        publicationDescriptorPhase = "published"
	publicationTestRollbackAnchored publicationDescriptorPhase = "rollback-anchored"
	publicationTestRollbackReady    publicationDescriptorPhase = "rollback-ready"
	publicationTestPreRestore       publicationDescriptorPhase = "pre-restore"
	publicationTestRestored         publicationDescriptorPhase = "restored"
	publicationTestCreateVacated    publicationDescriptorPhase = "create-rollback-vacated"
	publicationTestBlockedAnchored  publicationDescriptorPhase = "blocked-anchored"
)

func TestPublicationProtocolDescriptorShapeAndD1D9Coverage(t *testing.T) {
	t.Parallel()

	// Arrange.
	valid := publicationDocumentDescriptorForTest()
	tests := []struct {
		name   string
		mutate func(*publicationProtocolDescriptor)
		want   string
	}{
		{name: "empty name", mutate: func(value *publicationProtocolDescriptor) { value.name = "" }, want: "name is empty"},
		{name: "no phases", mutate: func(value *publicationProtocolDescriptor) { value.phases = nil }, want: "has no phases"},
		{name: "empty phase", mutate: func(value *publicationProtocolDescriptor) { value.phases[0] = "" }, want: "empty phase"},
		{name: "duplicate phase", mutate: func(value *publicationProtocolDescriptor) { value.phases[1] = value.phases[0] }, want: "duplicate phase"},
		{name: "unnamed transition", mutate: func(value *publicationProtocolDescriptor) { value.transitions[0].name = "" }, want: "unnamed row"},
		{name: "duplicate row", mutate: func(value *publicationProtocolDescriptor) { value.checkpoints[0].name = value.transitions[0].name }, want: "duplicate row"},
		{name: "zero invariants", mutate: func(value *publicationProtocolDescriptor) { value.transitions[0].invariants = 0 }, want: "has no invariants"},
		{name: "unknown invariants", mutate: func(value *publicationProtocolDescriptor) { value.transitions[0].invariants |= 1 << 15 }, want: "unknown invariants"},
		{name: "unknown source", mutate: func(value *publicationProtocolDescriptor) { value.transitions[0].from = "foreign" }, want: "undeclared source"},
		{name: "unknown destination", mutate: func(value *publicationProtocolDescriptor) { value.transitions[0].to = "foreign" }, want: "undeclared destination"},
		{name: "stationary transition", mutate: func(value *publicationProtocolDescriptor) { value.transitions[0].to = value.transitions[0].from }, want: "does not change phase"},
		{name: "incomplete coverage", mutate: func(value *publicationProtocolDescriptor) {
			value.checkpoints = value.checkpoints[:len(value.checkpoints)-1]
		}, want: "invariant coverage"},
	}

	// Act and assert.
	if err := validatePublicationProtocolDescriptor(valid); err != nil {
		t.Fatalf("validatePublicationProtocolDescriptor(valid) error = %v", err)
	}
	if got := publicationDescriptorInvariantCoverage(valid); got != publicationD1D9 {
		t.Fatalf("publicationDescriptorInvariantCoverage() = %#x, want %#x", got, publicationD1D9)
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := publicationDocumentDescriptorForTest()
			test.mutate(&candidate)
			err := validatePublicationProtocolDescriptor(candidate)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validatePublicationProtocolDescriptor() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestPublicationDocumentDescriptorDelegatesEveryLegalPhaseToV2Oracle(t *testing.T) {
	t.Parallel()

	// Arrange.
	tests := []struct {
		name  string
		state documentV2RecoveryState
		want  publicationDescriptorPhase
	}{
		{"pre new", documentV2State(true, false, false, false, false, documentV2TargetWitness), publicationTestPreNew},
		{"pre new vacated", documentV2State(true, false, false, false, true, documentV2TargetMissing), publicationTestPreNewVacated},
		{"pre new anchored", documentV2State(true, true, false, false, true, documentV2TargetMissing), publicationTestPreNewAnchored},
		{"pre new ready", documentV2State(true, true, true, false, true, documentV2TargetMissing), publicationTestPreNewReady},
		{"pre new restored", documentV2State(true, true, false, false, true, documentV2TargetAnchor), publicationTestPreNewRestored},
		{"published", documentV2State(false, false, false, false, true, documentV2TargetStage), publicationTestPublished},
		{"rollback anchored", documentV2State(false, true, false, false, true, documentV2TargetStage), publicationTestRollbackAnchored},
		{"rollback ready", documentV2State(false, true, true, false, true, documentV2TargetStage), publicationTestRollbackReady},
		{"pre restore", documentV2State(false, true, true, true, true, documentV2TargetMissing), publicationTestPreRestore},
		{"restored", documentV2State(false, true, false, true, true, documentV2TargetAnchor), publicationTestRestored},
	}
	descriptor := publicationDocumentDescriptorForTest()

	// Act and assert.
	if err := validatePublicationProtocolDescriptor(descriptor); err != nil {
		t.Fatal(err)
	}
	if len(descriptor.transitions) != 9 {
		t.Fatalf("document descriptor transitions = %d, want every 9 legal edges", len(descriptor.transitions))
	}
	seen := make(map[publicationDescriptorPhase]struct{}, len(tests))
	for _, test := range tests {
		phase, err := classifyDocumentV2RecoveryState(test.state)
		if err != nil {
			t.Fatalf("%s: classifyDocumentV2RecoveryState() error = %v", test.name, err)
		}
		got, ok := publicationDocumentPhaseForTest(phase)
		if !ok || got != test.want {
			t.Fatalf("%s: descriptor phase = %q, %v; want %q", test.name, got, ok, test.want)
		}
		seen[got] = struct{}{}
	}
	if len(seen) != len(descriptor.phases) {
		t.Fatalf("document oracle phases = %d, descriptor phases = %d", len(seen), len(descriptor.phases))
	}
}

func TestPublicationIndexDescriptorDelegatesEveryLegalPhaseToV3Oracle(t *testing.T) {
	t.Parallel()

	// Arrange. Blocked-anchored is exercised by the Unix recovery oracle.
	tests := []struct {
		name  string
		state indexV3ClassifierFixture
		want  publicationDescriptorPhase
	}{
		{"existing pre new", indexV3ClassifierFixture{old: true, n: true, target: "witness"}, publicationTestPreNew},
		{"existing pre new vacated", indexV3ClassifierFixture{old: true, n: true, claim: true, target: "missing"}, publicationTestPreNewVacated},
		{"existing pre new anchored", indexV3ClassifierFixture{old: true, n: true, a: true, claim: true, target: "missing"}, publicationTestPreNewAnchored},
		{"existing pre new ready", indexV3ClassifierFixture{old: true, n: true, a: true, r: true, claim: true, target: "missing"}, publicationTestPreNewReady},
		{"existing pre new restored", indexV3ClassifierFixture{old: true, n: true, a: true, claim: true, target: "anchor"}, publicationTestPreNewRestored},
		{"existing published", indexV3ClassifierFixture{old: true, claim: true, target: "stage"}, publicationTestPublished},
		{"existing rollback anchored", indexV3ClassifierFixture{old: true, a: true, claim: true, target: "stage"}, publicationTestRollbackAnchored},
		{"existing rollback ready", indexV3ClassifierFixture{old: true, a: true, r: true, claim: true, target: "stage"}, publicationTestRollbackReady},
		{"existing pre restore", indexV3ClassifierFixture{old: true, a: true, r: true, d: true, claim: true, target: "missing"}, publicationTestPreRestore},
		{"existing restored", indexV3ClassifierFixture{old: true, a: true, d: true, claim: true, target: "anchor"}, publicationTestRestored},
		{"create pre new", indexV3ClassifierFixture{n: true, target: "missing"}, publicationTestPreNew},
		{"create published", indexV3ClassifierFixture{target: "stage"}, publicationTestPublished},
		{"create rollback vacated", indexV3ClassifierFixture{d: true, target: "missing"}, publicationTestCreateVacated},
	}
	descriptor := publicationIndexDescriptorForTest()

	// Act and assert.
	if err := validatePublicationProtocolDescriptor(descriptor); err != nil {
		t.Fatal(err)
	}
	if len(descriptor.transitions) != 11 {
		t.Fatalf("index descriptor transitions = %d, want every 11 mutating legal edges", len(descriptor.transitions))
	}
	seen := make(map[publicationDescriptorPhase]struct{}, len(tests)+1)
	seen[publicationTestBlockedAnchored] = struct{}{}
	for _, test := range tests {
		prepared, entry, root, before := newIndexV3ClassifierFixture(t, test.state)
		phase, err := prepared.classifyIndexBatchV3Entry(entry)
		if err != nil {
			t.Fatalf("%s: classifyIndexBatchV3Entry() error = %v", test.name, err)
		}
		got, ok := publicationIndexPhaseForTest(phase)
		if !ok || got != test.want {
			t.Fatalf("%s: descriptor phase = %q, %v; want %q", test.name, got, ok, test.want)
		}
		seen[got] = struct{}{}
		assertIndexV3ClassifierTreeUnchanged(t, root, before)
	}
	if len(seen) != len(descriptor.phases) {
		t.Fatalf("index oracle phases = %d, descriptor phases = %d", len(seen), len(descriptor.phases))
	}
}

func TestPublicationDescriptorD1D8OracleBindings(t *testing.T) {
	t.Parallel()

	// Arrange. The full bundle gate executes these pre-existing behavioral
	// oracles; function bindings keep descriptor coverage anchored to them.
	oracles := []struct {
		invariant publicationDurabilityInvariant
		oracle    func(*testing.T)
	}{
		{publicationD1RootLock, TestIndexPublicationLockUsesPhysicalIdentityAcrossRootAliases},
		{publicationD2PinnedScope, TestDocumentSessionRejectsSameByteDifferentInodeSwapAfterBundleSnapshot},
		{publicationD3DurableArtifact, TestCreatePublicationArtifactRetriesInitialIdentityStatBeforeAfterOpen},
		{publicationD4ExactOwnership, TestCreatePublicationArtifactAfterOpenSwapPreservesForeignPath},
		{publicationD5BatchCommitOrder, TestIndexBatchV3RecoveryStateMixtures},
		{publicationD6GlobalInventory, TestGlobalIndexRecoveryValidatesFullInventoryBeforeMutation},
		{publicationD7LegalRecovery, TestDocumentV2RecoveryClassifierLegalStates},
		{publicationD8Compensation, TestDocumentSessionRewriteFaultsRollBackWithoutCancellation},
	}

	// Act.
	var coverage publicationDurabilityInvariant
	for _, oracle := range oracles {
		if oracle.oracle == nil {
			t.Fatalf("D invariant %#x has no behavioral oracle", oracle.invariant)
		}
		coverage |= oracle.invariant
	}

	// Assert.
	if coverage != publicationD1D9&^publicationD9PlatformCapability {
		t.Fatalf("generic oracle coverage = %#x", coverage)
	}
}

func publicationDescriptorCheckpointsForTest(prefix string) []publicationDescriptorCheckpoint {
	return []publicationDescriptorCheckpoint{
		{prefix + "/lock", publicationD1RootLock},
		{prefix + "/freeze", publicationD2PinnedScope | publicationD6GlobalInventory},
		{prefix + "/durable-evidence", publicationD3DurableArtifact | publicationD4ExactOwnership},
		{prefix + "/manifest-proof-boundary", publicationD5BatchCommitOrder},
		{prefix + "/recovery", publicationD7LegalRecovery | publicationD8Compensation},
		{prefix + "/platform", publicationD9PlatformCapability},
	}
}

func publicationDocumentDescriptorForTest() publicationProtocolDescriptor {
	return publicationProtocolDescriptor{
		name: "document-v2",
		phases: []publicationDescriptorPhase{
			publicationTestPreNew, publicationTestPreNewVacated, publicationTestPreNewAnchored,
			publicationTestPreNewReady, publicationTestPreNewRestored, publicationTestPublished,
			publicationTestRollbackAnchored, publicationTestRollbackReady,
			publicationTestPreRestore, publicationTestRestored,
		},
		transitions: []publicationDescriptorTransition{
			{"document/vacate-original", publicationTestPreNew, publicationTestPreNewVacated, publicationD2PinnedScope | publicationD3DurableArtifact | publicationD4ExactOwnership},
			{"document/install-new", publicationTestPreNewVacated, publicationTestPublished, publicationD2PinnedScope | publicationD3DurableArtifact | publicationD4ExactOwnership},
			{"document/anchor-pre-new", publicationTestPreNewVacated, publicationTestPreNewAnchored, publicationD3DurableArtifact | publicationD7LegalRecovery | publicationD8Compensation},
			{"document/prepare-pre-new-restore", publicationTestPreNewAnchored, publicationTestPreNewReady, publicationD3DurableArtifact | publicationD7LegalRecovery | publicationD8Compensation},
			{"document/restore-pre-new", publicationTestPreNewReady, publicationTestPreNewRestored, publicationD3DurableArtifact | publicationD4ExactOwnership | publicationD8Compensation},
			{"document/anchor-published", publicationTestPublished, publicationTestRollbackAnchored, publicationD3DurableArtifact | publicationD7LegalRecovery | publicationD8Compensation},
			{"document/prepare-published-restore", publicationTestRollbackAnchored, publicationTestRollbackReady, publicationD3DurableArtifact | publicationD7LegalRecovery | publicationD8Compensation},
			{"document/vacate-published", publicationTestRollbackReady, publicationTestPreRestore, publicationD2PinnedScope | publicationD4ExactOwnership | publicationD8Compensation},
			{"document/restore-published", publicationTestPreRestore, publicationTestRestored, publicationD3DurableArtifact | publicationD4ExactOwnership | publicationD8Compensation},
		},
		checkpoints: publicationDescriptorCheckpointsForTest("document"),
	}
}

func publicationIndexDescriptorForTest() publicationProtocolDescriptor {
	descriptor := publicationDocumentDescriptorForTest()
	descriptor.name = "index-batch-v3"
	for index := range descriptor.transitions {
		descriptor.transitions[index].name = "index/" + strings.TrimPrefix(
			descriptor.transitions[index].name,
			"document/",
		)
	}
	descriptor.phases = append(descriptor.phases, publicationTestCreateVacated, publicationTestBlockedAnchored)
	descriptor.transitions = append(
		descriptor.transitions,
		publicationDescriptorTransition{
			"index/install-created", publicationTestPreNew, publicationTestPublished,
			publicationD2PinnedScope | publicationD3DurableArtifact | publicationD4ExactOwnership,
		},
		publicationDescriptorTransition{
			"index/vacate-created", publicationTestPublished, publicationTestCreateVacated,
			publicationD2PinnedScope | publicationD4ExactOwnership | publicationD7LegalRecovery | publicationD8Compensation,
		},
	)
	descriptor.checkpoints = publicationDescriptorCheckpointsForTest("index")
	return descriptor
}

func publicationDocumentPhaseForTest(
	phase documentV2RecoveryPhase,
) (publicationDescriptorPhase, bool) {
	switch phase {
	case documentV2PhasePreNew:
		return publicationTestPreNew, true
	case documentV2PhasePreNewVacated:
		return publicationTestPreNewVacated, true
	case documentV2PhasePreNewRollbackAnchored:
		return publicationTestPreNewAnchored, true
	case documentV2PhasePreNewRollbackReady:
		return publicationTestPreNewReady, true
	case documentV2PhasePreNewRestored:
		return publicationTestPreNewRestored, true
	case documentV2PhasePublished:
		return publicationTestPublished, true
	case documentV2PhaseRollbackAnchored:
		return publicationTestRollbackAnchored, true
	case documentV2PhaseRollbackReady:
		return publicationTestRollbackReady, true
	case documentV2PhasePreRestore:
		return publicationTestPreRestore, true
	case documentV2PhaseRestored:
		return publicationTestRestored, true
	default:
		return "", false
	}
}

func publicationIndexPhaseForTest(
	phase indexBatchV3RecoveryPhase,
) (publicationDescriptorPhase, bool) {
	switch phase {
	case indexBatchV3PhasePreNew:
		return publicationTestPreNew, true
	case indexBatchV3PhasePreNewVacated:
		return publicationTestPreNewVacated, true
	case indexBatchV3PhasePreNewRollbackAnchored:
		return publicationTestPreNewAnchored, true
	case indexBatchV3PhasePreNewRollbackReady:
		return publicationTestPreNewReady, true
	case indexBatchV3PhasePreNewRestored:
		return publicationTestPreNewRestored, true
	case indexBatchV3PhasePublished:
		return publicationTestPublished, true
	case indexBatchV3PhaseRollbackAnchored:
		return publicationTestRollbackAnchored, true
	case indexBatchV3PhaseRollbackReady:
		return publicationTestRollbackReady, true
	case indexBatchV3PhasePreRestore:
		return publicationTestPreRestore, true
	case indexBatchV3PhaseRestored:
		return publicationTestRestored, true
	case indexBatchV3PhaseCreateRollbackVacated:
		return publicationTestCreateVacated, true
	case indexBatchV3PhaseBlockedAnchored:
		return publicationTestBlockedAnchored, true
	default:
		return "", false
	}
}

func TestPublicationEngineOwnsInputsAndPreservesOrder(t *testing.T) {
	t.Parallel()

	// Arrange.
	calls := []string{}
	snapshot := []byte("stable")
	steps := []publicationEngineStep[[]byte, string]{
		publicationEngineStepForTest("one", &calls, "", nil),
		publicationEngineStepForTest("two", &calls, "", nil),
	}
	plan, err := newPublicationEnginePlan(snapshot, publicationEngineCloneForTest, publicationEngineCloneReceiptForTest, steps)
	if err != nil {
		t.Fatal(err)
	}
	snapshot[0] = 'X'
	steps[0].name = "caller-mutation"
	steps[0].primitive = nil

	// Act.
	_, err = runPublicationEngine(context.Background(), plan)

	// Assert.
	want := "one/revalidate,one/primitive,one/receipt,one/postverify," +
		"two/revalidate,two/primitive,two/receipt,two/postverify"
	if err != nil || strings.Join(calls, ",") != want {
		t.Fatalf("calls=%q error=%v; want %q and nil", calls, err, want)
	}
}

func TestPublicationEngineRejectsMalformedPlansWithoutAction(t *testing.T) {
	// Arrange.
	calls := []string{}
	valid := publicationEngineStepForTest("valid", &calls, "", nil)
	malformed := []publicationEnginePlan[[]byte, string]{
		{},
		{snapshot: []byte("stable"), cloneSnapshot: publicationEngineCloneForTest, cloneReceipt: publicationEngineCloneReceiptForTest},
		{snapshot: []byte("stable"), cloneSnapshot: publicationEngineCloneForTest,
			cloneReceipt: publicationEngineCloneReceiptForTest, steps: []publicationEngineStep[[]byte, string]{{name: "incomplete"}}},
		{snapshot: []byte("stable"), cloneSnapshot: publicationEngineCloneForTest,
			cloneReceipt: publicationEngineCloneReceiptForTest, steps: []publicationEngineStep[[]byte, string]{valid, valid}},
	}

	// Act / Assert.
	for index, plan := range malformed {
		_, err := runPublicationEngine(context.Background(), plan)
		if !errors.Is(err, errPublicationEnginePlanInvalid) {
			t.Fatalf("plan %d error=%v, want invalid-plan sentinel", index, err)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("malformed plans called adapters: %q", calls)
	}
}

func TestPublicationEngineStopsAtFirstFailure(t *testing.T) {
	t.Parallel()

	for _, stage := range []string{"revalidate", "primitive", "receipt", "postverify"} {
		stage := stage
		t.Run(stage, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			injected := errors.New("injected " + stage)
			calls := []string{}
			steps := []publicationEngineStep[[]byte, string]{
				publicationEngineStepForTest("first", &calls, stage, injected),
				publicationEngineStepForTest("second", &calls, "", nil),
			}
			plan := publicationEnginePlanForTest(t, steps)

			// Act.
			result, err := runPublicationEngine(context.Background(), plan)

			// Assert.
			want := map[string]string{
				"revalidate": "first/revalidate",
				"primitive":  "first/revalidate,first/primitive",
				"receipt":    "first/revalidate,first/primitive,first/receipt",
				"postverify": "first/revalidate,first/primitive,first/receipt,first/postverify",
			}[stage]
			if err != injected || len(result.receipts) != 0 || strings.Join(calls, ",") != want {
				t.Fatalf("result=%+v calls=%q error=%v; want zero result, %q, exact error", result, calls, err, want)
			}
		})
	}
}

func TestPublicationEngineCancellationBoundary(t *testing.T) {
	t.Parallel()

	t.Run("pre-mutation cancellation performs no action", func(t *testing.T) {
		// Arrange.
		calls := []string{}
		plan := publicationEnginePlanForTest(t,
			[]publicationEngineStep[[]byte, string]{publicationEngineStepForTest("only", &calls, "", nil)},
		)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		// Act.
		_, err := runPublicationEngine(ctx, plan)

		// Assert.
		if !errors.Is(err, context.Canceled) || len(calls) != 0 {
			t.Fatalf("calls=%q error=%v; want canceled with no action", calls, err)
		}
	})

}

func TestPublicationEngineCanonicalFailureConvergesAndJoinsCauses(t *testing.T) {
	t.Parallel()

	// Arrange.
	executeFailure := errors.New("injected canonical execute failure")
	postverifyFailure := errors.New("injected postverification failure")
	calls := []string{}
	ctx, cancel := context.WithCancel(context.Background())
	first := publicationEngineStepForTest("first", &calls, "", nil)
	first.primitive = func(context.Context, []byte) (publicationEngineOutcome[string], error) {
		calls = append(calls, "first/primitive")
		cancel()
		return publicationEngineOutcome[string]{receipt: "first", canonicalMutation: true}, executeFailure
	}
	first.postverify = func(ctx context.Context, _ []byte, _ string) error {
		calls = append(calls, "first/postverify")
		if ctx.Err() != nil {
			t.Fatal("postverification received canceled convergence context")
		}
		return postverifyFailure
	}
	second := publicationEngineStepForTest("second", &calls, "", nil)
	secondRevalidate := second.revalidate
	second.revalidate = func(ctx context.Context, snapshot []byte) error {
		if ctx.Err() != nil {
			t.Fatal("remaining step received canceled convergence context")
		}
		return secondRevalidate(ctx, snapshot)
	}
	plan := publicationEnginePlanForTest(t,
		[]publicationEngineStep[[]byte, string]{first, second},
	)

	// Act.
	result, err := runPublicationEngine(ctx, plan)

	// Assert.
	wantCalls := "first/revalidate,first/primitive,first/receipt,first/postverify," +
		"second/revalidate,second/primitive,second/receipt,second/postverify"
	wantError := executeFailure.Error() + "\n" + postverifyFailure.Error()
	if !errors.Is(err, executeFailure) || !errors.Is(err, postverifyFailure) || err.Error() != wantError ||
		!result.canonicalMutation || len(result.receipts) != 2 ||
		result.receipts[0] != "first" || result.receipts[1] != "second" ||
		strings.Join(calls, ",") != wantCalls {
		t.Fatalf("result=%+v calls=%q error=%v; want ordered convergence and joined causes", result, calls, err)
	}
}

func TestPublicationEngineOwnsMutableReceipt(t *testing.T) {
	// Arrange.
	source := []byte("receipt")
	clone := func(value []byte) []byte { return append([]byte(nil), value...) }
	step := publicationEngineStep[[]byte, []byte]{
		name:       "only",
		revalidate: func(context.Context, []byte) error { return nil },
		primitive: func(context.Context, []byte) (publicationEngineOutcome[[]byte], error) {
			return publicationEngineOutcome[[]byte]{receipt: source}, nil
		},
		receipt: func(_ context.Context, _ []byte, receipt []byte) error {
			source[0], receipt[1] = 'S', 'C'
			return nil
		},
		postverify: func(context.Context, []byte, []byte) error { return nil },
	}
	plan, err := newPublicationEnginePlan([]byte("snapshot"), clone, clone, []publicationEngineStep[[]byte, []byte]{step})
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	result, err := runPublicationEngine(context.Background(), plan)

	// Assert.
	if err != nil || len(result.receipts) != 1 || string(result.receipts[0]) != "receipt" {
		t.Fatalf("result=%+v error=%v; want independently owned receipt", result, err)
	}
}

func publicationEngineCloneForTest(snapshot []byte) []byte { return append([]byte(nil), snapshot...) }

func publicationEngineCloneReceiptForTest(receipt string) string { return receipt }

func publicationEnginePlanForTest(t *testing.T, steps []publicationEngineStep[[]byte, string]) publicationEnginePlan[[]byte, string] {
	t.Helper()
	plan, err := newPublicationEnginePlan([]byte("stable"), publicationEngineCloneForTest, publicationEngineCloneReceiptForTest, steps)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func publicationEngineStepForTest(
	name string,
	calls *[]string,
	failureStage string,
	failure error,
) publicationEngineStep[[]byte, string] {
	call := func(stage string, snapshot []byte) error {
		*calls = append(*calls, name+"/"+stage)
		if string(snapshot) != "stable" {
			return errors.New("snapshot ownership violated")
		}
		snapshot[0] = 'X'
		if failureStage == stage {
			return failure
		}
		return nil
	}
	return publicationEngineStep[[]byte, string]{
		name:       name,
		revalidate: func(_ context.Context, snapshot []byte) error { return call("revalidate", snapshot) },
		primitive: func(_ context.Context, snapshot []byte) (publicationEngineOutcome[string], error) {
			err := call("primitive", snapshot)
			return publicationEngineOutcome[string]{receipt: name}, err
		},
		receipt: func(_ context.Context, snapshot []byte, _ string) error {
			return call("receipt", snapshot)
		},
		postverify: func(_ context.Context, snapshot []byte, _ string) error {
			return call("postverify", snapshot)
		},
	}
}
