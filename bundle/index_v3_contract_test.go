package bundle

import (
	"io/fs"
	"strings"
	"testing"
)

func TestIndexBatchManifestV3RequiresCanonicalInstallArtifacts(t *testing.T) {
	t.Parallel()

	valid := validIndexBatchManifestV3ForTest()
	cases := []struct {
		name   string
		mutate func(*indexBatchManifest)
	}{
		{
			name: "missing new install",
			mutate: func(manifest *indexBatchManifest) {
				manifest.Entries[0].NewInstall = ""
			},
		},
		{
			name: "missing restore install",
			mutate: func(manifest *indexBatchManifest) {
				manifest.Entries[0].RestoreInstall = ""
			},
		},
		{
			name: "missing discard",
			mutate: func(manifest *indexBatchManifest) {
				manifest.Entries[0].Discard = ""
			},
		},
		{
			name: "duplicate bound artifact path",
			mutate: func(manifest *indexBatchManifest) {
				manifest.Entries[0].Discard = manifest.Entries[0].NewInstall
			},
		},
		{
			name: "artifact outside target directory",
			mutate: func(manifest *indexBatchManifest) {
				manifest.Entries[0].NewInstall = "foreign/" + manifest.Entries[0].NewInstall
			},
		},
	}

	if err := validateIndexBatchManifestShape(valid); err != nil {
		t.Fatalf("validateIndexBatchManifestShape(valid) error = %v", err)
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange.
			manifest := validIndexBatchManifestV3ForTest()
			testCase.mutate(&manifest)

			// Act.
			err := validateIndexBatchManifestShape(manifest)

			// Assert.
			if err == nil {
				t.Fatal("validateIndexBatchManifestShape() error = nil, want invalid v3 contract")
			}
		})
	}
}

func TestIndexBatchV3IdentityAliasContract(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		left    indexBatchV3IdentityRole
		right   indexBatchV3IdentityRole
		wantErr bool
	}{
		{name: "new install aliases stage", left: indexBatchV3IdentityNewInstall, right: indexBatchV3IdentityStage},
		{name: "restore install aliases anchor", left: indexBatchV3IdentityRestoreInstall, right: indexBatchV3IdentityAnchor},
		{name: "discard aliases stage", left: indexBatchV3IdentityDiscard, right: indexBatchV3IdentityStage},
		{name: "new install cannot alias anchor", left: indexBatchV3IdentityNewInstall, right: indexBatchV3IdentityAnchor, wantErr: true},
		{name: "restore install cannot alias stage", left: indexBatchV3IdentityRestoreInstall, right: indexBatchV3IdentityStage, wantErr: true},
		{name: "discard cannot alias anchor", left: indexBatchV3IdentityDiscard, right: indexBatchV3IdentityAnchor, wantErr: true},
		{name: "new install cannot alias discard", left: indexBatchV3IdentityNewInstall, right: indexBatchV3IdentityDiscard, wantErr: true},
		{name: "backup cannot alias witness", left: indexBatchV3IdentityBackup, right: indexBatchV3IdentityWitness, wantErr: true},
		{name: "claim cannot alias anchor", left: indexBatchV3IdentityClaim, right: indexBatchV3IdentityAnchor, wantErr: true},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange.
			relations := []indexBatchV3IdentityRelation{{
				left:     testCase.left,
				right:    testCase.right,
				sameFile: true,
			}}

			// Act.
			err := validateIndexBatchV3IdentityRelations(relations)

			// Assert.
			if testCase.wantErr && err == nil {
				t.Fatal("validateIndexBatchV3IdentityRelations() error = nil, want forbidden alias")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("validateIndexBatchV3IdentityRelations() error = %v, want allowed alias", err)
			}
		})
	}
}

func TestIndexBatchManifestV3RejectsV2MagicAndPrefix(t *testing.T) {
	t.Parallel()

	// Arrange.
	v2 := validIndexBatchManifestV3ForTest()
	v2.Version = 2
	v2Name := indexTransactionPrefix + "manifest-v2-" + strings.Repeat("0", 64)

	// Act.
	versionErr := validateIndexBatchManifestShape(v2)
	prefixAccepted := parseIndexBatchManifestName(v2Name)

	// Assert.
	if versionErr == nil {
		t.Fatal("validateIndexBatchManifestShape(v2) error = nil, want unsupported version")
	}
	if prefixAccepted {
		t.Fatalf("parseIndexBatchManifestName(%q) = true, want v2 prefix rejected", v2Name)
	}
}

func validIndexBatchManifestV3ForTest() indexBatchManifest {
	const relative = indexFilename
	mode := fs.FileMode(0o644)
	data := []byte("# index\n")
	artifact := func(kind indexArtifactKind) string {
		return indexArtifactNameFor(kind, relative, mode, data, 0)
	}
	return indexBatchManifest{
		Protocol: indexBatchManifestProtocol,
		Version:  indexBatchManifestVersion,
		Entries: []indexBatchManifestEntry{{
			Path:           relative,
			NewDigest:      indexBatchPayloadDigest(data),
			NewMode:        uint32(mode),
			Stage:          artifact(indexArtifactStage),
			NewInstall:     artifact(indexArtifactNewInstall),
			RestoreInstall: artifact(indexArtifactRestoreInstall),
			Discard:        artifact(indexArtifactDiscard),
		}},
	}
}
