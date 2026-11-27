package mutation

import (
	"context"
	"strconv"

	"github.com/skosovsky/okf/bundle"
	"gopkg.in/yaml.v3"
)

type coreYAMLMutationKey struct{}

func usesCoreYAMLMutation(ctx context.Context) bool {
	enabled, _ := ctx.Value(coreYAMLMutationKey{}).(bool)
	return enabled
}

// Keep the source spelling in the mutation tree. The resolver proves the raw
// token, and a collection rewrite can copy an unchanged integer verbatim.
func coreMutationInteger(raw string) (yamlRenderValue, bool) {
	if unsigned, err := bundle.ParseNonNegativeYAMLUint64V03(raw); err == nil {
		value := yamlUint(unsigned)
		value.lexical = raw
		return value, true
	}
	if len(raw) > 1 && raw[0] == '-' {
		if signed, err := strconv.ParseInt(raw, 10, 64); err == nil {
			value := yamlInt(signed)
			value.lexical = raw
			return value, true
		}
	}
	return yamlRenderValue{}, false
}

// yaml.v3 can tag a decimal with a leading zero as a float, for example 079.
// Use the same bounded graph walk for the document and synthetic span probes.
func resolveCoreMutationIntegers(ctx context.Context, root *yaml.Node) error {
	if !usesCoreYAMLMutation(ctx) {
		return ctx.Err()
	}
	stack := []*yaml.Node{root}
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if node.Kind == yaml.ScalarNode && node.Style == 0 && node.Tag == "!!float" {
			if _, ok := coreMutationInteger(node.Value); ok {
				node.Tag = "!!int"
			}
		}
		stack = append(stack, node.Content...)
	}
	return ctx.Err()
}
