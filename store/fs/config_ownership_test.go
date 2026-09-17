package fs

import (
	"reflect"
	"testing"
	"time"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/validator"
)

func TestOpenFreezesValidatorConfigOwnership(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "concept.md", adversarialDocument("config ownership"))
	caller := &validator.ValidatorConfig{
		Strict:         true,
		CheckLinks:     true,
		CheckOrphans:   true,
		Spec:           bundle.OKFVersion,
		ReferenceDate:  time.Date(2026, time.July, 29, 0, 0, 0, 0, time.UTC),
		CheckRelations: true,
	}
	want := *caller

	// Act.
	s, err := openObserved(root, Config{ValidatorConfig: caller})
	if err != nil {
		t.Fatal(err)
	}
	registerStoreCleanup(t, s)
	stored := s.config.ValidatorConfig
	caller.Strict = false
	caller.CheckLinks = false
	caller.CheckOrphans = false
	caller.Spec = bundle.LegacyOKFVersion
	caller.ReferenceDate = time.Time{}
	caller.CheckRelations = false

	// Assert.
	if stored == caller {
		t.Fatal("Store retained the caller-owned ValidatorConfig pointer")
	}
	if !reflect.DeepEqual(*stored, want) {
		t.Fatalf("stored ValidatorConfig = %#v, want frozen %#v", *stored, want)
	}
}

func TestOpenValidatorConfigSnapshotsAreStoreIndependent(t *testing.T) {
	// Arrange.
	caller := &validator.ValidatorConfig{
		Strict:        true,
		CheckLinks:    true,
		Spec:          bundle.LegacyOKFVersion,
		ReferenceDate: time.Date(2025, time.January, 2, 0, 0, 0, 0, time.UTC),
	}
	firstWant := *caller
	firstRoot := t.TempDir()
	secondRoot := t.TempDir()
	writeTestFile(t, firstRoot, "concept.md", adversarialDocument("first"))
	writeTestFile(t, secondRoot, "concept.md", adversarialDocument("second"))

	// Act.
	first, err := openObserved(firstRoot, Config{ValidatorConfig: caller})
	if err != nil {
		t.Fatal(err)
	}
	registerStoreCleanup(t, first)
	caller.Strict = false
	caller.CheckLinks = false
	caller.CheckRelations = true
	caller.Spec = bundle.OKFVersion
	caller.ReferenceDate = time.Date(2026, time.February, 3, 0, 0, 0, 0, time.UTC)
	secondWant := *caller
	second, err := openObserved(secondRoot, Config{ValidatorConfig: caller})
	if err != nil {
		t.Fatal(err)
	}
	registerStoreCleanup(t, second)
	caller.Spec = "auto"
	caller.ReferenceDate = time.Time{}

	// Assert.
	if first.config.ValidatorConfig == second.config.ValidatorConfig ||
		first.config.ValidatorConfig == caller ||
		second.config.ValidatorConfig == caller {
		t.Fatal("independent stores share ValidatorConfig ownership")
	}
	if !reflect.DeepEqual(*first.config.ValidatorConfig, firstWant) {
		t.Fatalf("first Store config = %#v, want %#v", *first.config.ValidatorConfig, firstWant)
	}
	if !reflect.DeepEqual(*second.config.ValidatorConfig, secondWant) {
		t.Fatalf("second Store config = %#v, want %#v", *second.config.ValidatorConfig, secondWant)
	}
}

func TestFrozenValidatorConfigIsIndependentOfConcurrentCallerMutation(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "concept.md", adversarialDocument("race ownership"))
	caller := &validator.ValidatorConfig{
		Strict:        true,
		CheckLinks:    true,
		Spec:          bundle.OKFVersion,
		ReferenceDate: time.Date(2026, time.March, 4, 0, 0, 0, 0, time.UTC),
	}
	want := *caller
	s, err := openObserved(root, Config{ValidatorConfig: caller})
	if err != nil {
		t.Fatal(err)
	}
	registerStoreCleanup(t, s)
	done := make(chan struct{})

	// Act.
	go func() {
		defer close(done)
		for i := 0; i < 256; i++ {
			caller.Strict = i%2 == 0
			caller.CheckLinks = i%3 == 0
			caller.Spec = bundle.LegacyOKFVersion
			caller.ReferenceDate = time.Unix(int64(i+1), 0).UTC()
		}
	}()
	for i := 0; i < 256; i++ {
		if !reflect.DeepEqual(*s.config.ValidatorConfig, want) {
			t.Fatalf("stored ValidatorConfig changed at iteration %d: %#v", i, *s.config.ValidatorConfig)
		}
	}
	<-done

	// Assert.
	if !reflect.DeepEqual(*s.config.ValidatorConfig, want) {
		t.Fatalf("stored ValidatorConfig changed after caller mutation: %#v", *s.config.ValidatorConfig)
	}
}
