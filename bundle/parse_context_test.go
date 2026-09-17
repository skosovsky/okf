package bundle

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestParseHelpersCancelInsideEveryExpensivePhase(t *testing.T) {
	large := strings.Repeat(" ", 4<<20)
	invalid := strings.Repeat("a", 4<<20) + string([]byte{0xff})
	tests := []struct {
		name string
		call func(context.Context) (any, error)
		zero any
	}{
		{
			name: "valid UTF-8 scan",
			call: func(ctx context.Context) (any, error) {
				value, err := validUTF8StringContext(ctx, strings.Repeat("ж", 2<<20))
				return value, err
			},
			zero: false,
		},
		{
			name: "invalid UTF-8 context precedence before terminal invalid byte",
			call: func(ctx context.Context) (any, error) {
				value, err := validUTF8StringContext(ctx, invalid)
				return value, err
			},
			zero: false,
		},
		{
			name: "whitespace scan",
			call: func(ctx context.Context) (any, error) {
				value, err := blankStringContext(ctx, large)
				return value, err
			},
			zero: false,
		},
		{
			name: "string to bytes copy",
			call: func(ctx context.Context) (any, error) {
				value, err := bytesFromStringContext(ctx, strings.Repeat("x", 4<<20))
				return value, err
			},
			zero: []byte(nil),
		},
		{
			name: "bytes to string copy",
			call: func(ctx context.Context) (any, error) {
				value, err := stringFromBytesContext(ctx, bytes.Repeat([]byte("x"), 4<<20))
				return value, err
			},
			zero: "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
			_, probeErr := test.call(probe)
			checks := probe.checks.Load()
			if probeErr != nil || checks < 32 {
				t.Fatalf("probe error=%v checks=%d", probeErr, checks)
			}

			// Act.
			got, gotErr := test.call(&cancelAfterErrChecksContext{allowed: checks / 2})

			// Assert.
			if !errors.Is(gotErr, context.Canceled) || !reflect.DeepEqual(got, test.zero) {
				t.Fatalf("canceled helper=(%#v,%v), want (%#v,context.Canceled)", got, gotErr, test.zero)
			}
		})
	}
}

func TestParseFrontmatterContextCancelsInsideYAMLDecodeAndRawCopy(t *testing.T) {
	largeYAML := "type: Note\npayload: " + strings.Repeat("x", 4<<20) + "\n"
	tests := []struct {
		name  string
		phase string
		hit   int64
	}{
		{name: "yaml decoder reader middle", phase: "contextStringReader).Read", hit: 4},
		{name: "retained raw copy", phase: "bytesFromStringContext", hit: 3},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			ctx := &stackFrameCancelContext{target: test.phase, cancelAt: test.hit}

			// Act.
			got, gotErr := ParseFrontmatterContext(ctx, largeYAML)

			// Assert.
			if !errors.Is(gotErr, context.Canceled) || !reflect.DeepEqual(got, Frontmatter{}) || ctx.matches.Load() != test.hit {
				t.Fatalf("phase %q = (%#v,%v), matches=%d want zero/canceled/%d", test.phase, got, gotErr, ctx.matches.Load(), test.hit)
			}
		})
	}
}

func TestParseDocumentContextCancelsAcrossBoundaryAndCopies(t *testing.T) {
	largeBody := strings.Repeat("body ", 1<<20)
	largeFrontmatter := "type: Note\npayload: " + strings.Repeat("x", 4<<20) + "\n"
	tests := []struct {
		name string
		text string
	}{
		{name: "plain prose copy and delimiter scan", text: largeBody},
		{name: "unterminated frontmatter scan", text: "---\n" + largeFrontmatter},
		{name: "frontmatter and body copies", text: "---\n" + largeFrontmatter + "---\n" + largeBody + "\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
			_, probeErr := ParseDocumentContext(probe, test.text)
			checks := probe.checks.Load()
			if test.name == "unterminated frontmatter scan" {
				if !errors.Is(probeErr, ErrUnterminatedFrontmatter) {
					t.Fatalf("probe error=%v, want unterminated", probeErr)
				}
			} else if probeErr != nil {
				t.Fatal(probeErr)
			}
			if checks < 32 {
				t.Fatalf("probe checks=%d", checks)
			}

			// Act.
			got, gotErr := ParseDocumentContext(&cancelAfterErrChecksContext{allowed: checks / 2}, test.text)

			// Assert.
			if !errors.Is(gotErr, context.Canceled) || !reflect.DeepEqual(got, Document{}) {
				t.Fatalf("canceled document=(%#v,%v), want zero/context.Canceled", got, gotErr)
			}
		})
	}
}

func TestParseBackgroundBytesErrorsLocationsAndOwnership(t *testing.T) {
	tests := []struct {
		name string
		text string
		kind error
	}{
		{name: "plain", text: strings.Repeat("тело ", 1<<18)},
		{name: "frontmatter", text: "---\r\ntype: Note\r\nvalue: 'quoted' # comment\r\n---\r\n\r\nbody\r\n"},
		{name: "unterminated", text: "---\ntype: Note\n", kind: ErrUnterminatedFrontmatter},
		{name: "yaml location", text: "---\ntype: [\n---\n", kind: ErrInvalidFrontmatter},
		{name: "invalid UTF-8", text: strings.Repeat("x", 2<<20) + string([]byte{0xff}), kind: ErrInvalidEncoding},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange / Act.
			legacy, legacyErr := ParseDocument(test.text)
			got, gotErr := ParseDocumentContext(context.Background(), test.text)

			// Assert.
			if !reflect.DeepEqual(got, legacy) || !sameErrorText(gotErr, legacyErr) ||
				test.kind != nil && !errors.Is(gotErr, test.kind) {
				t.Fatalf("Background parity got=(%#v,%v) legacy=(%#v,%v)", got, gotErr, legacy, legacyErr)
			}
			if gotErr == nil && got.HasFrontmatter {
				yamlBytes := got.FrontmatterYAML()
				if len(yamlBytes) > 0 {
					yamlBytes[0] ^= 0xff
					if bytes.Equal(yamlBytes, got.FrontmatterYAML()) {
						t.Fatal("FrontmatterYAML retained caller mutation")
					}
				}
			}
		})
	}
}

func TestParseFrontmatterDecoderMatchesYAMLUnmarshalErrorsAndBytes(t *testing.T) {
	tests := []string{
		"type: Note\nvalue: text\n",
		"type: [\n",
		"---\ntype: Note\n---\n",
		strings.Repeat(" ", 2<<20),
	}
	for _, text := range tests {
		// Arrange.
		var legacyNode yaml.Node
		legacyErr := yaml.Unmarshal([]byte(text), &legacyNode)

		// Act.
		got, gotErr := ParseFrontmatterContext(context.Background(), text)

		// Assert.
		if legacyErr != nil {
			if gotErr == nil || !strings.Contains(gotErr.Error(), legacyErr.Error()) {
				t.Fatalf("decode error=%v, yaml.Unmarshal=%v", gotErr, legacyErr)
			}
			continue
		}
		if gotErr != nil {
			t.Fatal(gotErr)
		}
		if text != "" && strings.TrimSpace(text) != "" && !bytes.Equal(got.raw, []byte(text)) {
			t.Fatalf("raw bytes differ: got=%d want=%d", len(got.raw), len(text))
		}
	}
}
