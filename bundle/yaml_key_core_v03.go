package bundle

import (
	"context"
	"math/big"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	yamlCoreDecimal = regexp.MustCompile(`^[+-]?[0-9]+$`)
	yamlCoreOctal   = regexp.MustCompile(`^0o[0-7]+$`)
	yamlCoreHex     = regexp.MustCompile(`^0x[0-9a-fA-F]+$`)
	yamlCoreFloat   = regexp.MustCompile(`^[+-]?(\.[0-9]+|[0-9]+(\.[0-9]*)?)([eE][+-]?[0-9]+)?$`)
)

// YAMLCoreStringNeedsQuotes reports whether a plain token would be read as
// something other than a string under YAML 1.2 Core.
func YAMLCoreStringNeedsQuotes(value string) bool {
	return yamlCoreScalarTag(&yaml.Node{Value: value}) != "!!str"
}

func quoteCoreYAMLStringsContext(ctx context.Context, root *yaml.Node) error {
	stack := []*yaml.Node{root}
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if node.Kind == yaml.ScalarNode && node.Tag == "!!str" && node.Style == 0 && YAMLCoreStringNeedsQuotes(node.Value) {
			node.Style = yaml.DoubleQuotedStyle
		}
		stack = append(stack, node.Content...)
	}
	return ctx.Err()
}

// yamlCoreScalarTag resolves an implicit scalar according to YAML 1.2 Core.
// Retain the merge extension and explicitly declared tags.
func yamlCoreScalarTag(node *yaml.Node) string {
	if node.Style&yaml.TaggedStyle != 0 || node.Tag == "!!merge" {
		return node.Tag
	}
	if node.Style != 0 {
		return "!!str"
	}
	switch node.Value {
	case "", "null", "Null", "NULL", "~":
		return "!!null"
	case "true", "True", "TRUE", "false", "False", "FALSE":
		return "!!bool"
	case ".inf", ".Inf", ".INF", "+.inf", "+.Inf", "+.INF", "-.inf", "-.Inf", "-.INF", ".nan", ".NaN", ".NAN":
		return "!!float"
	}
	switch node.Value[0] {
	case '-', '+', '.', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		if yamlCoreDecimal.MatchString(node.Value) || yamlCoreOctal.MatchString(node.Value) || yamlCoreHex.MatchString(node.Value) {
			return "!!int"
		}
		if yamlCoreFloat.MatchString(node.Value) {
			return "!!float"
		}
	}
	return "!!str"
}

// yamlCoreKeyScalar compares numbers without a native conversion, which
// could overflow or round two distinct keys to the same value.
func yamlCoreKeyScalar(node *yaml.Node) (tag, canonical string) {
	tag, raw := yamlCoreScalarTag(node), node.Value
	switch tag {
	case "!!null":
		switch raw {
		case "", "null", "Null", "NULL", "~":
			return tag, ""
		}
	case "!!bool":
		switch raw {
		case "true", "True", "TRUE":
			return tag, "true"
		case "false", "False", "FALSE":
			return tag, "false"
		}
	case "!!int":
		switch {
		case yamlCoreDecimal.MatchString(raw):
			return tag, yamlCoreDecimalInteger(raw)
		case yamlCoreOctal.MatchString(raw), yamlCoreHex.MatchString(raw):
			var number big.Int
			number.SetString(raw, 0) // The core grammar above guarantees success.
			return tag, number.String()
		}
	case "!!float":
		switch raw {
		case ".inf", ".Inf", ".INF", "+.inf", "+.Inf", "+.INF":
			return tag, "+inf"
		case "-.inf", "-.Inf", "-.INF":
			return tag, "-inf"
		case ".nan", ".NaN", ".NAN":
			return tag, "nan"
		}
		if yamlCoreFloat.MatchString(raw) {
			return tag, yamlCoreDecimalFloat(raw)
		}
	}
	return tag, raw
}

func yamlCoreDecimalInteger(raw string) string {
	digits := strings.TrimLeft(strings.TrimPrefix(strings.TrimPrefix(raw, "-"), "+"), "0")
	if digits == "" {
		return "0"
	}
	if raw[0] == '-' {
		return "-" + digits
	}
	return digits
}

func yamlCoreDecimalFloat(raw string) string {
	mantissa := strings.TrimPrefix(strings.TrimPrefix(raw, "-"), "+")
	var exponent big.Int
	if at := strings.IndexAny(mantissa, "eE"); at >= 0 {
		exponent.SetString(mantissa[at+1:], 10)
		mantissa = mantissa[:at]
	}
	whole, fraction, _ := strings.Cut(mantissa, ".")
	digits := strings.TrimLeft(whole+fraction, "0")
	if digits == "" {
		return "0"
	}
	significant := strings.TrimRight(digits, "0")
	exponent.Add(&exponent, big.NewInt(int64(len(digits)-len(significant)-len(fraction))))
	if raw[0] == '-' {
		significant = "-" + significant
	}
	return significant + "e" + exponent.String()
}
