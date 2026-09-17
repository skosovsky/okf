package fs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/skosovsky/okf/store"
)

func TestValidateFilesystemPathAliasesReturnsLexicalFirstCollision(t *testing.T) {
	// Arrange.
	s := &Store{caseAliases: true}
	values := map[string][]byte{
		"A.md": nil,
		"a.md": nil,
		"Z.md": nil,
		"z.md": nil,
	}
	orders := [][]string{
		{"z.md", "Z.md", "a.md", "A.md"},
		{"A.md", "a.md", "Z.md", "z.md"},
	}
	const want = `invalid change set: filesystem-equivalent result paths "A.md" and "a.md"`

	for _, order := range orders {
		for run := 0; run < 64; run++ {
			files := orderedFiles(order, values)

			// Act.
			err := s.validateFilesystemPathAliases(files)

			// Assert.
			if !errors.Is(err, store.ErrInvalidChangeSet) || err.Error() != want {
				t.Fatalf("order=%q run=%d error=%v want %q", order, run, err, want)
			}
		}
	}
}

func TestValidateFilesystemPathAliasTransitionHasStablePrecedence(t *testing.T) {
	tests := []struct {
		name       string
		baseValues map[string][]byte
		baseOrders [][]string
		nextValues map[string][]byte
		nextOrders [][]string
		want       string
	}{
		{
			name:       "result collision precedes base transition",
			baseValues: map[string][]byte{"zeta.md": nil},
			baseOrders: [][]string{{"zeta.md"}},
			nextValues: map[string][]byte{"A.md": nil, "a.md": nil, "ZETA.md": nil},
			nextOrders: [][]string{
				{"ZETA.md", "a.md", "A.md"},
				{"A.md", "a.md", "ZETA.md"},
			},
			want: `invalid change set: filesystem-equivalent result paths "A.md" and "a.md"`,
		},
		{
			name:       "lexical result transition wins",
			baseValues: map[string][]byte{"alpha.md": nil, "zeta.md": nil},
			baseOrders: [][]string{
				{"zeta.md", "alpha.md"},
				{"alpha.md", "zeta.md"},
			},
			nextValues: map[string][]byte{"ALPHA.md": nil, "ZETA.md": nil},
			nextOrders: [][]string{
				{"ZETA.md", "ALPHA.md"},
				{"ALPHA.md", "ZETA.md"},
			},
			want: `invalid change set: filesystem-equivalent result path "ALPHA.md" replaces base path "alpha.md"`,
		},
		{
			name:       "lexical base alias candidate wins",
			baseValues: map[string][]byte{"ALPHA.md": nil, "alpha.md": nil},
			baseOrders: [][]string{
				{"alpha.md", "ALPHA.md"},
				{"ALPHA.md", "alpha.md"},
			},
			nextValues: map[string][]byte{"Alpha.md": nil},
			nextOrders: [][]string{{"Alpha.md"}},
			want:       `invalid change set: filesystem-equivalent result path "Alpha.md" replaces base path "ALPHA.md"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			s := &Store{caseAliases: true}

			for _, baseOrder := range test.baseOrders {
				for _, nextOrder := range test.nextOrders {
					for run := 0; run < 32; run++ {
						base := orderedFiles(baseOrder, test.baseValues)
						next := orderedFiles(nextOrder, test.nextValues)

						// Act.
						err := s.validateFilesystemPathAliasTransition(base, next)

						// Assert.
						if !errors.Is(err, store.ErrInvalidChangeSet) || err.Error() != test.want {
							t.Fatalf(
								"base=%q next=%q run=%d error=%v want %q",
								baseOrder,
								nextOrder,
								run,
								err,
								test.want,
							)
						}
					}
				}
			}
		})
	}
}

func TestNewOwnedSnapshotValidatesPathsInLexicalOrder(t *testing.T) {
	// Arrange.
	values := map[string][]byte{
		"../first.md": []byte("first"),
		"/second.md":  []byte("second"),
		"bad\\third":  []byte("third"),
	}
	orders := [][]string{
		{"bad\\third", "/second.md", "../first.md"},
		{"../first.md", "/second.md", "bad\\third"},
	}
	const want = `invalid manifest: invalid path "../first.md"`

	for _, order := range orders {
		for run := 0; run < 64; run++ {
			files := orderedFiles(order, values)

			// Act.
			snapshot, err := newOwnedSnapshotWithAlgorithm(context.Background(), files, nil)

			// Assert.
			if snapshot != nil {
				t.Fatalf("order=%q run=%d snapshot=%#v want nil", order, run, snapshot)
			}
			if !errors.Is(err, store.ErrInvalidManifest) || err.Error() != want {
				t.Fatalf("order=%q run=%d error=%v want %q", order, run, err, want)
			}
		}
	}
}

func TestReadSourceSortsCallerPathsBeforeFirstError(t *testing.T) {
	t.Run("pre-canceled context", func(t *testing.T) {
		// Arrange.
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		source := &validationOrderSource{
			paths:              []string{"a.md"},
			pathsError:         errors.New("hostile source sentinel"),
			ignorePathsContext: true,
		}

		// Act.
		files, err := readSource(ctx, source)

		// Assert.
		if files != nil || err != context.Canceled {
			t.Fatalf("readSource() = %#v, %v, want nil, context.Canceled", files, err)
		}
		if source.pathsCalls != 0 || len(source.reads) != 0 {
			t.Fatalf("source calls = Paths:%d ReadFile:%q, want none", source.pathsCalls, source.reads)
		}
	})

	t.Run("invalid path", func(t *testing.T) {
		// Arrange.
		orders := [][]string{
			{"bad\\third", "/second.md", "../first.md"},
			{"../first.md", "/second.md", "bad\\third"},
		}
		const want = `fs store: invalid source path "../first.md"`

		for _, order := range orders {
			for run := 0; run < 64; run++ {
				source := &validationOrderSource{paths: order}

				// Act.
				files, err := readSource(context.Background(), source)

				// Assert.
				if files != nil || err == nil || err.Error() != want {
					t.Fatalf("order=%q run=%d files=%#v error=%v want %q", order, run, files, err, want)
				}
				if len(source.reads) != 0 {
					t.Fatalf("order=%q run=%d ReadFile calls=%q want none", order, run, source.reads)
				}
			}
		}
	})

	t.Run("read failure", func(t *testing.T) {
		// Arrange.
		errA := errors.New("read a failed")
		errM := errors.New("read m failed")
		errZ := errors.New("read z failed")
		readErrors := map[string]error{"a.md": errA, "m.md": errM, "z.md": errZ}
		orders := [][]string{
			{"z.md", "m.md", "a.md"},
			{"a.md", "m.md", "z.md"},
		}
		const want = `read "a.md": read a failed`

		for _, order := range orders {
			for run := 0; run < 64; run++ {
				source := &validationOrderSource{paths: order, readErrors: readErrors}

				// Act.
				files, err := readSource(context.Background(), source)

				// Assert.
				if files != nil || !errors.Is(err, errA) || err.Error() != want {
					t.Fatalf("order=%q run=%d files=%#v error=%v want %q", order, run, files, err, want)
				}
				if !reflect.DeepEqual(source.reads, []string{"a.md"}) {
					t.Fatalf("order=%q run=%d ReadFile calls=%q want [a.md]", order, run, source.reads)
				}
			}
		}
	})

	t.Run("duplicate path before read failure", func(t *testing.T) {
		// Arrange.
		orders := [][]string{
			{"z.md", "a.md", "a.md"},
			{"a.md", "z.md", "a.md"},
		}
		const want = `fs store: duplicate source path "a.md"`

		for _, order := range orders {
			for run := 0; run < 64; run++ {
				source := &validationOrderSource{
					paths:      order,
					readErrors: map[string]error{"a.md": errors.New("read a failed")},
				}

				// Act.
				files, err := readSource(context.Background(), source)

				// Assert.
				if files != nil || err == nil || err.Error() != want {
					t.Fatalf("order=%q run=%d files=%#v error=%v want %q", order, run, files, err, want)
				}
				if len(source.reads) != 0 {
					t.Fatalf("order=%q run=%d ReadFile calls=%q want none", order, run, source.reads)
				}
			}
		}
	})
}

func TestReadSourceDoesNotMutateProviderPathSlice(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		// Arrange.
		retained := []string{"z.md", "a.md", "m.md"}
		original := append([]string(nil), retained...)
		source := &retainedPathsSource{
			paths: retained,
			files: map[string][]byte{
				"a.md": []byte("a"),
				"m.md": []byte("m"),
				"z.md": []byte("z"),
			},
		}

		// Act.
		files, err := readSource(context.Background(), source)

		// Assert.
		if err != nil || len(files) != 3 {
			t.Fatalf("readSource() = %#v, %v, want three files", files, err)
		}
		if !reflect.DeepEqual(retained, original) {
			t.Fatalf("provider paths mutated: got %q want %q", retained, original)
		}
		if !reflect.DeepEqual(source.reads, []string{"a.md", "m.md", "z.md"}) {
			t.Fatalf("ReadFile calls=%q want [a.md m.md z.md]", source.reads)
		}
	})

	t.Run("failure", func(t *testing.T) {
		// Arrange.
		retained := []string{"z.md", "a.md", "m.md"}
		original := append([]string(nil), retained...)
		injected := errors.New("read failed")
		source := &retainedPathsSource{
			paths:      retained,
			readErrors: map[string]error{"a.md": injected},
		}

		// Act.
		files, err := readSource(context.Background(), source)

		// Assert.
		if files != nil || !errors.Is(err, injected) {
			t.Fatalf("readSource() = %#v, %v, want nil and injected error", files, err)
		}
		if !reflect.DeepEqual(retained, original) {
			t.Fatalf("provider paths mutated: got %q want %q", retained, original)
		}
		if !reflect.DeepEqual(source.reads, []string{"a.md"}) {
			t.Fatalf("ReadFile calls=%q want [a.md]", source.reads)
		}
	})
}

func TestCleanupOrphanStagesReturnsLexicalFirstInvalidStage(t *testing.T) {
	orders := [][]string{
		{"z-invalid", "a-invalid"},
		{"a-invalid", "z-invalid"},
	}
	const want = "fs store: metadata: storage corruption\ninvalid stage entry \"a-invalid\""

	for _, order := range orders {
		t.Run(strings.Join(order, "_"), func(t *testing.T) {
			// Arrange.
			root, s := openValidationOrderStore(t)
			staging := filepath.Join(root, internalDirectory, "staging")
			for _, name := range order {
				if err := os.Mkdir(filepath.Join(staging, name), 0o700); err != nil {
					t.Fatal(err)
				}
			}

			for run := 0; run < 32; run++ {
				// Act.
				err := s.cleanupOrphanStages(context.Background())

				// Assert.
				if !errors.Is(err, store.ErrStorageCorrupt) || err.Error() != want {
					t.Fatalf("run=%d cleanup error=%v, want %q", run, err, want)
				}
			}
		})
	}
}

func TestCleanupOrphanStagesIgnoresInvalidPayloadsWithoutOwnership(t *testing.T) {
	orders := [][]string{
		{"z-invalid", "a-invalid"},
		{"a-invalid", "z-invalid"},
	}
	for _, order := range orders {
		t.Run(strings.Join(order, "_"), func(t *testing.T) {
			// Arrange.
			root, s := openValidationOrderStore(t)
			stageID := "txn-" + strings.Repeat("a", 64)
			payloadDir := filepath.Join(root, internalDirectory, "staging", stageID, "payload")
			if err := os.MkdirAll(payloadDir, 0o700); err != nil {
				t.Fatal(err)
			}
			for _, name := range order {
				if err := os.WriteFile(filepath.Join(payloadDir, name), []byte(name), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			// Act.
			err := s.cleanupOrphanStages(context.Background())

			// Assert.
			if err != nil {
				t.Fatalf("cleanup error=%v", err)
			}
			for _, name := range order {
				got, readErr := os.ReadFile(filepath.Join(payloadDir, name))
				if readErr != nil || string(got) != name {
					t.Fatalf("unproven payload %q changed: %q/%v", name, got, readErr)
				}
			}
		})
	}
}

func TestCleanupOrphanStagesPreservesUnprovenPayloadsWithoutFaults(t *testing.T) {
	orders := [][]string{
		{"payload-00001", "payload-00000"},
		{"payload-00000", "payload-00001"},
	}
	for _, order := range orders {
		t.Run(strings.Join(order, "_"), func(t *testing.T) {
			// Arrange.
			root, s := openValidationOrderStore(t)
			stageID := "txn-" + strings.Repeat("b", 64)
			payloadDir := filepath.Join(root, internalDirectory, "staging", stageID, "payload")
			if err := os.MkdirAll(payloadDir, 0o700); err != nil {
				t.Fatal(err)
			}
			for _, name := range order {
				if err := os.WriteFile(filepath.Join(payloadDir, name), []byte(name), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			postFaultCalls := 0
			s.config.PostFault = func(step Step) error {
				if step == StepStageCleanupPayloadRemove {
					postFaultCalls++
				}
				return nil
			}

			// Act.
			err := s.cleanupOrphanStages(context.Background())

			// Assert.
			if err != nil || postFaultCalls != 0 {
				t.Fatalf("cleanup error=%v calls=%d", err, postFaultCalls)
			}
			for _, name := range order {
				got, readErr := os.ReadFile(filepath.Join(payloadDir, name))
				if readErr != nil || string(got) != name {
					t.Fatalf("unproven payload %q changed: %q/%v", name, got, readErr)
				}
			}
		})
	}
}

func TestCleanupOrphanStagesPreservesUnprovenStageDirectories(t *testing.T) {
	firstID := "txn-" + strings.Repeat("a", 64)
	secondID := "txn-" + strings.Repeat("b", 64)
	orders := [][]string{
		{secondID, firstID},
		{firstID, secondID},
	}
	for _, order := range orders {
		t.Run(strings.Join(order, "_"), func(t *testing.T) {
			// Arrange.
			root, s := openValidationOrderStore(t)
			staging := filepath.Join(root, internalDirectory, "staging")
			for _, stageID := range order {
				if err := os.Mkdir(filepath.Join(staging, stageID), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			postFaultCalls := 0
			s.config.PostFault = func(step Step) error {
				if step == StepStageCleanupStageDirRemove {
					postFaultCalls++
				}
				return nil
			}

			// Act.
			err := s.cleanupOrphanStages(context.Background())

			// Assert.
			if err != nil || postFaultCalls != 0 {
				t.Fatalf("cleanup error=%v calls=%d", err, postFaultCalls)
			}
			for _, stageID := range order {
				if info, statErr := os.Stat(filepath.Join(staging, stageID)); statErr != nil || !info.IsDir() {
					t.Fatalf("unproven stage %q changed: %v/%v", stageID, info, statErr)
				}
			}
		})
	}
}

func TestPruneReceiptsReturnsLexicalFirstValidationError(t *testing.T) {
	orders := [][]string{
		{"z.json", "a.json"},
		{"a.json", "z.json"},
	}
	values := map[string][]byte{
		"a.json": {0xff},
		"z.json": []byte("{"),
	}
	const want = "fs store: receipt: storage corruption\nreceipt contains invalid UTF-8"

	for _, order := range orders {
		t.Run(strings.Join(order, "_"), func(t *testing.T) {
			// Arrange.
			root, s := openValidationOrderStore(t)
			receipts := filepath.Join(root, internalDirectory, "receipts")
			for _, name := range order {
				if err := os.WriteFile(filepath.Join(receipts, name), values[name], 0o600); err != nil {
					t.Fatal(err)
				}
			}

			for run := 0; run < 32; run++ {
				// Act.
				err := s.pruneReceipts()

				// Assert.
				if !errors.Is(err, store.ErrStorageCorrupt) || err.Error() != want {
					t.Fatalf("run=%d prune error=%v, want %q", run, err, want)
				}
			}
		})
	}
}

type validationOrderSource struct {
	paths              []string
	pathsError         error
	ignorePathsContext bool
	pathsCalls         int
	readErrors         map[string]error
	reads              []string
}

type retainedPathsSource struct {
	paths      []string
	files      map[string][]byte
	readErrors map[string]error
	reads      []string
}

func (s *retainedPathsSource) Paths(context.Context) ([]string, error) {
	return s.paths, nil
}

func (s *retainedPathsSource) ReadFile(_ context.Context, path string) ([]byte, error) {
	s.reads = append(s.reads, path)
	if err := s.readErrors[path]; err != nil {
		return nil, err
	}
	return append([]byte(nil), s.files[path]...), nil
}

func (s *validationOrderSource) Paths(ctx context.Context) ([]string, error) {
	s.pathsCalls++
	if !s.ignorePathsContext {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	return append([]string(nil), s.paths...), s.pathsError
}

func (s *validationOrderSource) ReadFile(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.reads = append(s.reads, path)
	if err := s.readErrors[path]; err != nil {
		return nil, fmt.Errorf("read %q: %w", path, err)
	}
	return []byte("content"), nil
}

func orderedFiles(order []string, values map[string][]byte) map[string][]byte {
	out := make(map[string][]byte, len(order))
	for _, path := range order {
		out[path] = values[path]
	}
	return out
}

func openValidationOrderStore(t *testing.T) (string, *Store) {
	t.Helper()
	root := t.TempDir()
	s, err := openObserved(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	registerStoreCleanup(t, s)
	return root, s
}
