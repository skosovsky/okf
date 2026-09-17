package bundle

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

type indexV3ClassifierFixture struct {
	old          bool
	n            bool
	a            bool
	r            bool
	d            bool
	claim        bool
	target       string
	independentD bool
	rWithoutA    bool
}

func TestIndexBatchV3RecoveryClassifierLegalStates(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		state indexV3ClassifierFixture
		want  indexBatchV3RecoveryPhase
	}{
		{name: "existing pre new", state: indexV3ClassifierFixture{old: true, n: true, target: "witness"}, want: indexBatchV3PhasePreNew},
		{name: "existing pre new vacated", state: indexV3ClassifierFixture{old: true, n: true, claim: true, target: "missing"}, want: indexBatchV3PhasePreNewVacated},
		{name: "existing pre new anchor", state: indexV3ClassifierFixture{old: true, n: true, a: true, claim: true, target: "missing"}, want: indexBatchV3PhasePreNewRollbackAnchored},
		{name: "existing pre new anchor restore", state: indexV3ClassifierFixture{old: true, n: true, a: true, r: true, claim: true, target: "missing"}, want: indexBatchV3PhasePreNewRollbackReady},
		{name: "existing pre new restored", state: indexV3ClassifierFixture{old: true, n: true, a: true, claim: true, target: "anchor"}, want: indexBatchV3PhasePreNewRestored},
		{name: "existing published", state: indexV3ClassifierFixture{old: true, claim: true, target: "stage"}, want: indexBatchV3PhasePublished},
		{name: "existing rollback anchored", state: indexV3ClassifierFixture{old: true, a: true, claim: true, target: "stage"}, want: indexBatchV3PhaseRollbackAnchored},
		{name: "existing rollback ready", state: indexV3ClassifierFixture{old: true, a: true, r: true, claim: true, target: "stage"}, want: indexBatchV3PhaseRollbackReady},
		{name: "existing pre restore", state: indexV3ClassifierFixture{old: true, a: true, r: true, d: true, claim: true, target: "missing"}, want: indexBatchV3PhasePreRestore},
		{name: "existing restored", state: indexV3ClassifierFixture{old: true, a: true, d: true, claim: true, target: "anchor"}, want: indexBatchV3PhaseRestored},
		{name: "create pre new", state: indexV3ClassifierFixture{n: true, target: "missing"}, want: indexBatchV3PhasePreNew},
		{name: "create published", state: indexV3ClassifierFixture{target: "stage"}, want: indexBatchV3PhasePublished},
		{name: "create rollback vacated", state: indexV3ClassifierFixture{d: true, target: "missing"}, want: indexBatchV3PhaseCreateRollbackVacated},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange.
			prepared, entry, root, before := newIndexV3ClassifierFixture(t, testCase.state)

			// Act.
			got, err := prepared.classifyIndexBatchV3Entry(entry)

			// Assert.
			if err != nil || got != testCase.want {
				t.Fatalf("classifyIndexBatchV3Entry() = %v, %v; want %v", got, err, testCase.want)
			}
			assertIndexV3ClassifierTreeUnchanged(t, root, before)
		})
	}
}

func TestIndexBatchV3RecoveryClassifierRejectsIllegalStatesWithoutMutation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		state indexV3ClassifierFixture
	}{
		{name: "existing consumed new missing without discard", state: indexV3ClassifierFixture{old: true, claim: true, target: "missing"}},
		{name: "create consumed new missing without discard", state: indexV3ClassifierFixture{target: "missing"}},
		{name: "restore token without anchor", state: indexV3ClassifierFixture{old: true, r: true, rWithoutA: true, claim: true, target: "stage"}},
		{name: "discard detached from stage", state: indexV3ClassifierFixture{old: true, a: true, r: true, d: true, independentD: true, claim: true, target: "missing"}},
		{name: "new and discard coexist", state: indexV3ClassifierFixture{old: true, n: true, d: true, claim: true, target: "missing"}},
		{name: "foreign existing target", state: indexV3ClassifierFixture{old: true, n: true, target: "foreign"}},
		{name: "foreign create target", state: indexV3ClassifierFixture{n: true, target: "foreign"}},
		{name: "create entry contains anchor", state: indexV3ClassifierFixture{a: true, target: "stage"}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange.
			prepared, entry, root, before := newIndexV3ClassifierFixture(t, testCase.state)

			// Act.
			got, err := prepared.classifyIndexBatchV3Entry(entry)

			// Assert.
			if err == nil || got != indexBatchV3PhaseInvalid {
				t.Fatalf("classifyIndexBatchV3Entry() = %v, %v; want hard rejection", got, err)
			}
			assertIndexV3ClassifierTreeUnchanged(t, root, before)
		})
	}
}

func TestIndexBatchV3RecoveryStateMixtures(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		phases  []indexBatchV3RecoveryPhase
		wantErr bool
	}{
		{name: "committed all published", phases: []indexBatchV3RecoveryPhase{indexBatchV3PhasePublished, indexBatchV3PhasePublished}},
		{name: "published active restored tail", phases: []indexBatchV3RecoveryPhase{indexBatchV3PhasePublished, indexBatchV3PhaseRollbackReady, indexBatchV3PhaseRestored, indexBatchV3PhasePreNew}},
		{name: "forward vacated then tail", phases: []indexBatchV3RecoveryPhase{indexBatchV3PhasePublished, indexBatchV3PhasePreNewVacated, indexBatchV3PhasePreNew}},
		{name: "restored prefix and untouched tail", phases: []indexBatchV3RecoveryPhase{indexBatchV3PhaseRestored, indexBatchV3PhasePreNew}},
		{name: "published after active", phases: []indexBatchV3RecoveryPhase{indexBatchV3PhaseRollbackReady, indexBatchV3PhasePublished, indexBatchV3PhasePreNew}, wantErr: true},
		{name: "two active entries", phases: []indexBatchV3RecoveryPhase{indexBatchV3PhaseRollbackAnchored, indexBatchV3PhasePreRestore, indexBatchV3PhasePreNew}, wantErr: true},
		{name: "active after restored", phases: []indexBatchV3RecoveryPhase{indexBatchV3PhaseRestored, indexBatchV3PhaseRollbackReady, indexBatchV3PhasePreNew}, wantErr: true},
		{name: "restored after tail", phases: []indexBatchV3RecoveryPhase{indexBatchV3PhasePreNew, indexBatchV3PhaseRestored}, wantErr: true},
		{name: "published root with pre new peer", phases: []indexBatchV3RecoveryPhase{indexBatchV3PhasePreNew, indexBatchV3PhasePublished}, wantErr: true},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange.
			entries := make([]*indexBatchRecoveryEntry, len(testCase.phases))
			for index, phase := range testCase.phases {
				relative := "entry-" + string(rune('a'+index)) + "/index.md"
				if index == len(testCase.phases)-1 {
					relative = indexFilename
				}
				entries[index] = &indexBatchRecoveryEntry{
					contract: indexBatchManifestEntry{Path: relative, Stage: relative + ".stage"},
					v3Phase:  phase,
				}
			}
			prepared := &preparedIndexBatchRecovery{entries: entries}

			// Act.
			err := prepared.validateIndexBatchV3PhaseMixture()

			// Assert.
			if testCase.wantErr && err == nil {
				t.Fatal("validateIndexBatchV3PhaseMixture() error = nil, want rejection")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("validateIndexBatchV3PhaseMixture() error = %v, want legal mixture", err)
			}
		})
	}
}

func TestIndexBatchV3RejectsLegacyAndUnknownReservedNames(t *testing.T) {
	t.Parallel()

	legacy := indexTransactionPrefix + "manifest-v2-" + "0000000000000000000000000000000000000000000000000000000000000000"
	unknown := indexTransactionPrefix + "future-v9-0000-0000000000000000000000000000000000000000000000000000000000000000"

	if parseIndexBatchManifestName(legacy) {
		t.Fatalf("parseIndexBatchManifestName(%q) = true, want v2 rejected", legacy)
	}
	if kind, reserved := classifyIndexTransactionArtifact(unknown); !reserved || kind != indexArtifactUnknown {
		t.Fatalf("classifyIndexTransactionArtifact(%q) = %q, %v; want reserved unknown", unknown, kind, reserved)
	}
}

func newIndexV3ClassifierFixture(
	t *testing.T,
	state indexV3ClassifierFixture,
) (*preparedIndexBatchRecovery, *indexBatchRecoveryEntry, string, map[string]os.FileInfo) {
	t.Helper()
	root := t.TempDir()
	newData := []byte("new\n")
	oldData := []byte("old\n")
	mode := fs.FileMode(0o640)
	stage := indexV3ClassifierCreateItem(t, root, "stage", indexArtifactStage, newData, mode)
	entry := &indexBatchRecoveryEntry{
		contract: indexBatchManifestEntry{
			Path:       indexFilename,
			OldPresent: state.old,
			NewDigest:  indexBatchPayloadDigest(newData),
			NewMode:    uint32(mode),
		},
		stage: stage,
	}
	if state.old {
		entry.contract.OldDigest = indexBatchPayloadDigest(oldData)
		entry.contract.OldMode = uint32(mode)
		entry.witness = indexV3ClassifierCreateItem(t, root, "witness", indexArtifactWitness, oldData, mode)
		entry.backup = indexV3ClassifierCreateItem(t, root, "backup", indexArtifactBackup, oldData, mode)
		if state.claim {
			entry.claim = indexV3ClassifierLinkItem(t, root, "witness", "claim", indexArtifactRestore, oldData)
		}
	}
	if state.n {
		entry.newInstall = indexV3ClassifierLinkItem(t, root, "stage", "new-install", indexArtifactNewInstall, newData)
	}
	if state.a {
		entry.anchor = indexV3ClassifierCreateItem(t, root, "anchor", indexArtifactAnchor, oldData, mode)
	}
	if state.r {
		if state.rWithoutA {
			entry.restoreInstall = indexV3ClassifierCreateItem(t, root, "restore-install", indexArtifactRestoreInstall, oldData, mode)
		} else {
			entry.restoreInstall = indexV3ClassifierLinkItem(t, root, "anchor", "restore-install", indexArtifactRestoreInstall, oldData)
		}
	}
	if state.d {
		if state.independentD {
			entry.discard = indexV3ClassifierCreateItem(t, root, "discard", indexArtifactDiscard, newData, mode)
		} else {
			entry.discard = indexV3ClassifierLinkItem(t, root, "stage", "discard", indexArtifactDiscard, newData)
		}
	}
	targetItem := func(item *indexTransactionInventoryItem, relation indexBatchTargetRelation) {
		entry.target = indexBatchTargetSnapshot{
			entry:    entry.contract,
			info:     item.observation.info,
			data:     append([]byte(nil), item.observation.data...),
			mode:     mode,
			relation: relation,
		}
	}
	switch state.target {
	case "missing":
		entry.target = indexBatchTargetSnapshot{entry: entry.contract, relation: indexBatchTargetForeign}
	case "stage":
		targetItem(stage, indexBatchTargetNew)
	case "witness":
		targetItem(entry.witness, indexBatchTargetOld)
	case "anchor":
		targetItem(entry.anchor, indexBatchTargetOld)
	case "foreign":
		foreign := indexV3ClassifierCreateItem(t, root, "foreign", indexArtifactUnknown, []byte("foreign\n"), mode)
		targetItem(foreign, indexBatchTargetForeign)
	default:
		t.Fatalf("unknown classifier target %q", state.target)
	}
	return &preparedIndexBatchRecovery{}, entry, root, indexV3ClassifierTree(t, root)
}

func indexV3ClassifierCreateItem(
	t *testing.T,
	root, name string,
	kind indexArtifactKind,
	data []byte,
	mode fs.FileMode,
) *indexTransactionInventoryItem {
	t.Helper()
	filename := filepath.Join(root, name)
	if err := os.WriteFile(filename, data, mode); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(filename)
	if err != nil {
		t.Fatal(err)
	}
	return indexV3ClassifierItem(name, kind, info, data)
}

func indexV3ClassifierLinkItem(
	t *testing.T,
	root, source, name string,
	kind indexArtifactKind,
	data []byte,
) *indexTransactionInventoryItem {
	t.Helper()
	if err := os.Link(filepath.Join(root, source), filepath.Join(root, name)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	return indexV3ClassifierItem(name, kind, info, data)
}

func indexV3ClassifierItem(
	name string,
	kind indexArtifactKind,
	info os.FileInfo,
	data []byte,
) *indexTransactionInventoryItem {
	return &indexTransactionInventoryItem{
		discovery: indexTransactionDiscovery{kind: kind, name: name, relative: name},
		destination: &indexDestination{
			output: indexOutput{relative: indexFilename},
		},
		observation: indexRecoveryObservation{
			name:     name,
			relative: name,
			info:     info,
			data:     append([]byte(nil), data...),
		},
	}
}

func indexV3ClassifierTree(t *testing.T, root string) map[string]os.FileInfo {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	tree := make(map[string]os.FileInfo, len(entries))
	for _, entry := range entries {
		info, err := os.Lstat(filepath.Join(root, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		tree[entry.Name()] = info
	}
	return tree
}

func assertIndexV3ClassifierTreeUnchanged(
	t *testing.T,
	root string,
	before map[string]os.FileInfo,
) {
	t.Helper()
	after := indexV3ClassifierTree(t, root)
	if len(after) != len(before) {
		t.Fatalf("classifier mutated entry count: before=%d after=%d", len(before), len(after))
	}
	names := make([]string, 0, len(before))
	for name := range before {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		current, ok := after[name]
		if !ok || !os.SameFile(before[name], current) ||
			before[name].Mode() != current.Mode() ||
			before[name].Size() != current.Size() {
			t.Fatalf("classifier mutated %q", name)
		}
	}
}
