package bundle

import (
	"errors"
	"testing"
)

func TestResolveVersionPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		declared string
		selector string
		want     VersionResolution
	}{
		{
			name: "default v0.2",
			want: VersionResolution{Effective: OKFVersion, Source: VersionSourceDefault, Compatibility: VersionCompatibilityNative},
		},
		{
			name:     "declared legacy",
			declared: LegacyOKFVersion,
			want:     VersionResolution{Declared: LegacyOKFVersion, Effective: LegacyOKFVersion, Source: VersionSourceDeclared, Compatibility: VersionCompatibilityLegacy},
		},
		{
			name:     "declared native",
			declared: OKFVersion,
			want:     VersionResolution{Declared: OKFVersion, Effective: OKFVersion, Source: VersionSourceDeclared, Compatibility: VersionCompatibilityNative},
		},
		{
			name:     "future best effort",
			declared: "9.0",
			want:     VersionResolution{Declared: "9.0", Effective: OKFVersion, Source: VersionSourceFutureBestEffort, Compatibility: VersionCompatibilityBestEffort},
		},
		{
			name:     "lower unknown best effort",
			declared: "0.0",
			want:     VersionResolution{Declared: "0.0", Effective: OKFVersion, Source: VersionSourceFutureBestEffort, Compatibility: VersionCompatibilityBestEffort},
		},
		{
			name:     "explicit legacy",
			selector: LegacyOKFVersion,
			want:     VersionResolution{Effective: LegacyOKFVersion, Source: VersionSourceExplicit, Compatibility: VersionCompatibilityLegacy},
		},
		{
			name:     "explicit preserves declaration",
			declared: OKFVersion,
			selector: OKFVersion,
			want:     VersionResolution{Declared: OKFVersion, Effective: OKFVersion, Source: VersionSourceExplicit, Compatibility: VersionCompatibilityNative},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			declared, selector := tt.declared, tt.selector

			// Act.
			got, err := ResolveVersion(declared, selector)

			// Assert.
			if err != nil {
				t.Fatalf("ResolveVersion() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("ResolveVersion() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestResolveVersionRejectsMalformedDeclarations(t *testing.T) {
	t.Parallel()

	for _, declared := range []string{"banana", "0.02", "1", "1.", ".1", " 0.2"} {
		declared := declared
		t.Run(declared, func(t *testing.T) {
			t.Parallel()

			// Act.
			_, err := ResolveVersion(declared, "")

			// Assert.
			if !errors.Is(err, ErrInvalidVersionDeclaration) {
				t.Fatalf("ResolveVersion(%q) error = %v", declared, err)
			}
		})
	}
}

func TestVersionResolutionUnsupportedCanonicalMatrix(t *testing.T) {
	t.Parallel()

	declarations := []string{
		"0.0",
		"0.3",
		"0.10",
		"1.0",
		"999999999999999999999999999999999999.123456789012345678901234567890",
	}
	for _, declaration := range declarations {
		declaration := declaration
		t.Run(declaration, func(t *testing.T) {
			t.Parallel()

			// Arrange / Act.
			got, err := ResolveVersion(declaration, "")

			// Assert.
			if err != nil {
				t.Fatalf("ResolveVersion(%q) error = %v", declaration, err)
			}
			want := VersionResolution{
				Declared:      declaration,
				Effective:     OKFVersion,
				Source:        VersionSourceFutureBestEffort,
				Compatibility: VersionCompatibilityBestEffort,
			}
			if got != want {
				t.Fatalf("ResolveVersion(%q) = %#v, want %#v", declaration, got, want)
			}
		})
	}
}

func TestVersionDeclarationStatePreservesMalformedYAML(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter, err := ParseFrontmatter("okf_version: 0.2\n")
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	state := frontmatter.VersionDeclarationState()

	// Assert.
	if !state.Present || state.Valid || state.Raw != "0.2" || state.Value != "" {
		t.Fatalf("VersionDeclarationState() = %#v", state)
	}
}

func TestBundleVersionResolutionRejectsUnparseableRootIndex(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		root string
		want error
	}{
		{
			name: "malformed yaml",
			root: "---\nokf_version: [\n---\n\n# Root\n",
			want: ErrInvalidFrontmatter,
		},
		{
			name: "unterminated frontmatter",
			root: "---\nokf_version: \"0.2\"\n# Root\n",
			want: ErrUnterminatedFrontmatter,
		},
		{
			name: "invalid utf8",
			root: string(append([]byte("# Root "), 0xff, '\n')),
			want: ErrInvalidEncoding,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			writeFile(t, root, "index.md", test.root)
			loaded, err := LoadBundle(root)
			if err != nil {
				t.Fatalf("LoadBundle() error = %v", err)
			}

			// Act.
			state := loaded.VersionDeclarationState()
			_, resolutionErr := loaded.VersionResolution("")

			// Assert.
			if !state.Present || state.Valid {
				t.Fatalf("VersionDeclarationState() = %#v, want present invalid", state)
			}
			if !errors.Is(resolutionErr, test.want) {
				t.Fatalf("VersionResolution() error = %v, want %v", resolutionErr, test.want)
			}
		})
	}
}

func TestBundleVersionResolutionDistinguishesMalformedDeclarationFromDocumentErrors(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeFile(t, root, "index.md", "---\nokf_version: \"v0.2\"\n---\n\n# Root\n")
	loaded, err := LoadBundle(root)
	if err != nil {
		t.Fatalf("LoadBundle() error = %v", err)
	}

	// Act.
	state := loaded.VersionDeclarationState()
	_, resolutionErr := loaded.VersionResolution("")

	// Assert.
	if !state.Present || state.Valid || state.Raw != "v0.2" {
		t.Fatalf("VersionDeclarationState() = %#v, want malformed present declaration", state)
	}
	if !errors.Is(resolutionErr, ErrInvalidVersionDeclaration) {
		t.Fatalf("VersionResolution() error = %v, want ErrInvalidVersionDeclaration", resolutionErr)
	}
	for _, documentError := range []error{ErrInvalidEncoding, ErrInvalidFrontmatter, ErrUnterminatedFrontmatter} {
		if errors.Is(resolutionErr, documentError) {
			t.Fatalf("VersionResolution() error = %v, unexpectedly matches %v", resolutionErr, documentError)
		}
	}
}

func TestResolveVersionSelectorIsAssertion(t *testing.T) {
	t.Parallel()

	// Arrange.
	declared := LegacyOKFVersion

	// Act.
	_, conflictErr := ResolveVersion(declared, OKFVersion)
	_, unsupportedErr := ResolveVersion("", "9.0")

	// Assert.
	if !errors.Is(conflictErr, ErrVersionConflict) {
		t.Fatalf("conflict error = %v, want ErrVersionConflict", conflictErr)
	}
	if !errors.Is(unsupportedErr, ErrUnsupportedVersionSelector) {
		t.Fatalf("unsupported error = %v, want ErrUnsupportedVersionSelector", unsupportedErr)
	}
}
