package bundle

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type sourcePreflightSpy struct {
	paths             []string
	pathsCalls        int
	pathsErr          error
	returnBackingPath bool
	readErrors        map[string]error
	reads             []string
}

func (s *sourcePreflightSpy) Paths(context.Context) ([]string, error) {
	s.pathsCalls++
	if s.pathsErr != nil {
		return nil, s.pathsErr
	}
	if s.returnBackingPath {
		return s.paths, nil
	}
	return append([]string(nil), s.paths...), nil
}

func (s *sourcePreflightSpy) ReadFile(_ context.Context, name string) ([]byte, error) {
	s.reads = append(s.reads, name)
	if err := s.readErrors[name]; err != nil {
		return nil, err
	}
	return []byte(name), nil
}

func TestLoadSourcePathPreflightChecksContextBeforeProvider(t *testing.T) {
	// Arrange. The provider deliberately ignores context and has its own error;
	// neither may become observable after the caller has already canceled.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	source := &sourcePreflightSpy{
		paths:    []string{"a.md"},
		pathsErr: errors.New("hostile provider error"),
	}

	// Act.
	loaded, err := Load(ctx, source)

	// Assert.
	if loaded != nil || err != context.Canceled {
		t.Fatalf("Load() = (%#v, %v), want (nil, context.Canceled)", loaded, err)
	}
	if source.pathsCalls != 0 {
		t.Fatalf("Paths calls = %d, want 0", source.pathsCalls)
	}
	if len(source.reads) != 0 {
		t.Fatalf("ReadFile calls = %q, want none", source.reads)
	}
}

func TestLoadSourcePathPreflightReportsLexicalMalformedPathDeterministically(t *testing.T) {
	// Arrange.
	orders := [][]string{
		{`bad\third.md`, "/second.md", "../first.md"},
		{"../first.md", `bad\third.md`, "/second.md"},
		{"/second.md", "../first.md", `bad\third.md`},
	}
	const want = `invalid bundle source path "../first.md"`

	for _, order := range orders {
		for run := 0; run < 64; run++ {
			source := &sourcePreflightSpy{paths: order}

			// Act.
			loaded, err := Load(context.Background(), source)

			// Assert.
			if loaded != nil || err == nil || err.Error() != want {
				t.Fatalf("order=%q run=%d Load() = (%#v, %v), want (nil, %q)", order, run, loaded, err, want)
			}
			if len(source.reads) != 0 {
				t.Fatalf("order=%q run=%d ReadFile calls = %q, want none", order, run, source.reads)
			}
		}
	}
}

func TestLoadSourcePathPreflightReportsLexicalDuplicateDeterministically(t *testing.T) {
	// Arrange.
	orders := [][]string{
		{"z.md", "a.md", "a.md"},
		{"a.md", "z.md", "a.md"},
		{"a.md", "a.md", "z.md"},
	}
	const want = `duplicate bundle source path "a.md"`

	for _, order := range orders {
		for run := 0; run < 64; run++ {
			source := &sourcePreflightSpy{paths: order}

			// Act.
			loaded, err := Load(context.Background(), source)

			// Assert.
			if loaded != nil || err == nil || err.Error() != want {
				t.Fatalf("order=%q run=%d Load() = (%#v, %v), want (nil, %q)", order, run, loaded, err, want)
			}
			if len(source.reads) != 0 {
				t.Fatalf("order=%q run=%d ReadFile calls = %q, want none", order, run, source.reads)
			}
		}
	}
}

func TestLoadSourcePathPreflightUsesLexicalIssuePrecedence(t *testing.T) {
	tests := []struct {
		name   string
		orders [][]string
		want   string
	}{
		{
			name: "malformed path sorts before duplicate",
			orders: [][]string{
				{"a.md", "a.md", "../first.md"},
				{"../first.md", "a.md", "a.md"},
			},
			want: `invalid bundle source path "../first.md"`,
		},
		{
			name: "duplicate sorts before malformed path",
			orders: [][]string{
				{`bad\third.md`, "a.md", "a.md"},
				{"a.md", `bad\third.md`, "a.md"},
			},
			want: `duplicate bundle source path "a.md"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, order := range test.orders {
				for run := 0; run < 32; run++ {
					// Arrange.
					source := &sourcePreflightSpy{paths: order}

					// Act.
					loaded, err := Load(context.Background(), source)

					// Assert.
					if loaded != nil || err == nil || err.Error() != test.want {
						t.Fatalf("order=%q run=%d Load() = (%#v, %v), want (nil, %q)", order, run, loaded, err, test.want)
					}
					if len(source.reads) != 0 {
						t.Fatalf("order=%q run=%d ReadFile calls = %q, want none", order, run, source.reads)
					}
				}
			}
		})
	}
}

func TestLoadSourcePathPreflightRejectsExactDuplicateAssetPath(t *testing.T) {
	// Arrange.
	source := &sourcePreflightSpy{paths: []string{"nested/asset.bin", "nested/asset.bin"}}

	// Act.
	loaded, err := Load(context.Background(), source)

	// Assert.
	const want = `duplicate bundle source path "nested/asset.bin"`
	if loaded != nil || err == nil || err.Error() != want {
		t.Fatalf("Load() = (%#v, %v), want (nil, %q)", loaded, err, want)
	}
	if len(source.reads) != 0 {
		t.Fatalf("ReadFile calls = %q, want none", source.reads)
	}
}

func TestLoadSourcePathPreflightDoesNotMutateProviderBackingSlice(t *testing.T) {
	// Arrange. This provider violates the defensive-copy recommendation and
	// returns its own backing slice, which Load still must not sort in place.
	paths := []string{"z.bin", "a.bin", "nested/m.bin"}
	wantPaths := append([]string(nil), paths...)
	source := &sourcePreflightSpy{
		paths:             paths,
		returnBackingPath: true,
	}

	// Act.
	loaded, err := Load(context.Background(), source)

	// Assert.
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded == nil {
		t.Fatal("Load() bundle = nil")
	}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Fatalf("provider backing paths = %q, want unchanged %q", paths, wantPaths)
	}
}

func TestLoadSourceReadFailureUsesLexicallyFirstPathDeterministically(t *testing.T) {
	// Arrange.
	errA := errors.New("read a failed")
	errM := errors.New("read m failed")
	errZ := errors.New("read z failed")
	orders := [][]string{
		{"z.md", "m.md", "a.md"},
		{"a.md", "m.md", "z.md"},
		{"m.md", "z.md", "a.md"},
	}
	const want = `read bundle file "a.md": read a failed`

	for _, order := range orders {
		for run := 0; run < 64; run++ {
			source := &sourcePreflightSpy{
				paths: order,
				readErrors: map[string]error{
					"a.md": errA,
					"m.md": errM,
					"z.md": errZ,
				},
			}

			// Act.
			loaded, err := Load(context.Background(), source)

			// Assert.
			if loaded != nil || !errors.Is(err, errA) || err.Error() != want {
				t.Fatalf("order=%q run=%d Load() = (%#v, %v), want (nil, %q)", order, run, loaded, err, want)
			}
			if got := source.reads; !reflect.DeepEqual(got, []string{"a.md"}) {
				t.Fatalf("order=%q run=%d ReadFile calls = %q, want [a.md]", order, run, got)
			}
		}
	}
}

func TestLoadSourcePathPreflightReadsValidPathsOnceInLexicalOrder(t *testing.T) {
	// Arrange.
	source := &sourcePreflightSpy{paths: []string{"z.bin", "nested/a.bin", "a.bin"}}

	// Act.
	loaded, err := Load(context.Background(), source)

	// Assert.
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := []string{"a.bin", "nested/a.bin", "z.bin"}
	if got := loaded.Files(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Files() = %q, want %q", got, want)
	}
	if !reflect.DeepEqual(source.reads, want) {
		t.Fatalf("ReadFile calls = %q, want %q", source.reads, want)
	}
}
