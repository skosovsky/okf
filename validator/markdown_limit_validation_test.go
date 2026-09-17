package validator

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
)

func TestMarkdownLimitValidateSourceRejectsOversizeHeadingTargetWithoutPublication(t *testing.T) {
	t.Parallel()

	// Arrange.
	source := validationSource{
		"source.md": []byte("---\ntype: Note\n---\n\n[target](/index.md#heading)\n"),
		"index.md":  []byte("# Heading\n" + strings.Repeat("a", bundle.MaxMarkdownBodyBytes+1)),
	}
	// Act.
	report, err := ValidateSource(context.Background(), source, &ValidatorConfig{CheckLinks: true})

	// Assert.
	if !reflect.DeepEqual(report, Report{}) || !errors.Is(err, bundle.ErrMarkdownResourceLimit) {
		t.Fatalf("ValidateBundleContext() = (%#v, %v)", report, err)
	}
}
