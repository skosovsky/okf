package bundle

import (
	"io/fs"
	"strings"
	"testing"
)

func TestClassifyIndexTransactionArtifactCanonicalKinds(t *testing.T) {
	t.Parallel()

	kinds := []indexArtifactKind{
		indexArtifactBackup,
		indexArtifactStage,
		indexArtifactRestore,
		indexArtifactAnchor,
		indexArtifactWitness,
		indexArtifactNewInstall,
		indexArtifactRestoreInstall,
		indexArtifactDiscard,
		indexArtifactManifest,
	}
	names := make(map[indexArtifactKind]string, len(kinds))
	for _, kind := range kinds {
		template, err := newIndexArtifactNameTemplate(
			kind,
			"a/index.md",
			fs.FileMode(0o640),
			[]byte("payload\n"),
		)
		if err != nil {
			t.Fatalf("newIndexArtifactNameTemplate(%s) error = %v", kind, err)
		}
		names[kind] = template.Name(0)
	}
	for _, want := range kinds {
		name := names[want]
		got, reserved := classifyIndexTransactionArtifact(name)
		if !reserved || got != want {
			t.Errorf(
				"classifyIndexTransactionArtifact(%q) = %q, %v; want %q, true",
				name,
				got,
				reserved,
				want,
			)
		}
		exactMatches := 0
		for _, candidate := range kinds {
			prefix, err := indexArtifactPrefix(candidate)
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(name, prefix) {
				exactMatches++
			}
		}
		if exactMatches != 1 {
			t.Errorf("canonical %s name has %d exact kind-prefix matches", want, exactMatches)
		}
	}
}

func TestClassifyIndexTransactionArtifactReservedFamilies(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		want indexArtifactKind
	}{
		{name: indexTransactionPrefix + "backup-v9-invalid", want: indexArtifactBackup},
		{name: indexTransactionPrefix + "stage-v9-invalid", want: indexArtifactStage},
		{name: indexTransactionPrefix + "restore-v9-invalid", want: indexArtifactRestore},
		{name: indexTransactionPrefix + "anchor-v9-invalid", want: indexArtifactAnchor},
		{name: indexTransactionPrefix + "witness-v9-invalid", want: indexArtifactWitness},
		{name: indexTransactionPrefix + "new-install-v9-invalid", want: indexArtifactNewInstall},
		{name: indexTransactionPrefix + "restore-install-v9-invalid", want: indexArtifactRestoreInstall},
		{name: indexTransactionPrefix + "discard-v9-invalid", want: indexArtifactDiscard},
		{name: indexTransactionPrefix + "manifest-v9-invalid", want: indexArtifactManifest},
	}
	for _, testCase := range cases {
		got, reserved := classifyIndexTransactionArtifact(testCase.name)
		if !reserved || got != testCase.want {
			t.Errorf(
				"classifyIndexTransactionArtifact(%q) = %q, %v; want %q, true",
				testCase.name,
				got,
				reserved,
				testCase.want,
			)
		}
	}
}
