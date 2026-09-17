package okfcli

import (
	"fmt"
	"strconv"
	"strings"
)

type flagKind uint8

const (
	stringFlag flagKind = iota
	boolFlag
)

type flagSpec struct {
	Name  string
	Short string
	Kind  flagKind
}

type parsedArgs struct {
	values      map[string]string
	boolValues  map[string]bool
	positionals []string
	seen        map[string]struct{}
}

func parseArgs(args []string, specs []flagSpec) (parsedArgs, error) {
	result := parsedArgs{
		values:     make(map[string]string),
		boolValues: make(map[string]bool),
		seen:       make(map[string]struct{}),
	}
	byName := make(map[string]flagSpec)
	for _, spec := range specs {
		if len(spec.Name) < 3 || !strings.HasPrefix(spec.Name, "--") || strings.Contains(spec.Name, "=") {
			return parsedArgs{}, fmt.Errorf("invalid canonical flag definition: %s", spec.Name)
		}
		byName[spec.Name] = spec
		if spec.Short != "" {
			if len(spec.Short) != 2 || spec.Short[0] != '-' || spec.Short[1] == '-' {
				return parsedArgs{}, fmt.Errorf("invalid short flag definition: %s", spec.Short)
			}
			byName[spec.Short] = spec
		}
	}

	terminated := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if terminated {
			result.positionals = append(result.positionals, arg)
			continue
		}
		if arg == "--" {
			terminated = true
			continue
		}
		if arg == "-" || !strings.HasPrefix(arg, "-") {
			result.positionals = append(result.positionals, arg)
			continue
		}

		name, inline, hasInline := splitFlag(arg)
		spec, ok := byName[name]
		if !ok {
			canonical, compatibilityAlias := validateCompatibilityAlias(name)
			if compatibilityAlias && hasValidateCompatibilityAliasInventory(byName) {
				spec, ok = byName[canonical]
			}
		}
		if !ok {
			return parsedArgs{}, fmt.Errorf("unknown flag: %s", name)
		}
		if _, duplicate := result.seen[spec.Name]; duplicate {
			return parsedArgs{}, fmt.Errorf("duplicate flag: %s", name)
		}
		result.seen[spec.Name] = struct{}{}

		switch spec.Kind {
		case stringFlag:
			if !hasInline {
				if i+1 >= len(args) ||
					args[i+1] == "--" ||
					strings.HasPrefix(args[i+1], "-") {
					return parsedArgs{}, fmt.Errorf("flag needs an argument: %s", name)
				}
				i++
				inline = args[i]
			}
			if inline == "" {
				return parsedArgs{}, fmt.Errorf("flag needs a non-empty argument: %s", name)
			}
			result.values[spec.Name] = inline
		case boolFlag:
			value := true
			if hasInline {
				parsed, err := strconv.ParseBool(inline)
				if err != nil {
					return parsedArgs{}, fmt.Errorf("invalid boolean value %q for %s", inline, name)
				}
				value = parsed
			}
			result.boolValues[spec.Name] = value
		default:
			return parsedArgs{}, fmt.Errorf("unsupported flag definition: %s", spec.Name)
		}
	}
	return result, nil
}

func validateCompatibilityAlias(name string) (string, bool) {
	// This is a closed compatibility surface for validate only. Other
	// single-dash long names remain unknown.
	switch name {
	case "-path":
		return "--path", true
	case "-strict":
		return "--strict", true
	case "-check-links":
		return "--check-links", true
	case "-check-orphans":
		return "--check-orphans", true
	case "-format":
		return "--format", true
	case "-json":
		return "--json", true
	default:
		return "", false
	}
}

func hasValidateCompatibilityAliasInventory(byName map[string]flagSpec) bool {
	// Requiring the complete inventory prevents a shared canonical flag such
	// as --format from enabling validate-only aliases in another command.
	_, path := byName["--path"]
	_, strict := byName["--strict"]
	_, checkLinks := byName["--check-links"]
	_, checkOrphans := byName["--check-orphans"]
	_, format := byName["--format"]
	_, json := byName["--json"]
	return path && strict && checkLinks && checkOrphans && format && json
}

func splitFlag(arg string) (name, value string, hasValue bool) {
	if index := strings.IndexByte(arg, '='); index >= 0 {
		return arg[:index], arg[index+1:], true
	}
	return arg, "", false
}

func (args parsedArgs) value(name, fallback string) string {
	if value, ok := args.values[name]; ok {
		return value
	}
	return fallback
}

func (args parsedArgs) boolValue(name string) bool {
	return args.boolValues[name]
}

func (args parsedArgs) has(name string) bool {
	_, ok := args.seen[name]
	return ok
}

func (args parsedArgs) onePositional(what string) (string, error) {
	if len(args.positionals) == 0 {
		return "", fmt.Errorf("missing %s", what)
	}
	if len(args.positionals) > 1 {
		return "", fmt.Errorf("unexpected argument: %s", args.positionals[1])
	}
	return args.positionals[0], nil
}

func parseSpecSelector(raw string) (string, error) {
	switch raw {
	case "", "auto":
		return "auto", nil
	case "0.1", "0.2":
		return raw, nil
	default:
		return "", fmt.Errorf("unsupported spec selector %q (want auto, 0.1, or 0.2)", raw)
	}
}
