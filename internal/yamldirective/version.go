package yamldirective

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

var yamlVersion = regexp.MustCompile("^[ \t]+([0-9]+)\\.([0-9]+)(?:[ \t]|$)")

type Notice struct {
	Code    string
	Line    int
	Message string
}

// ForV3Decoder adapts leading directives for yaml.v3. Adaptations are the
// same byte width as the original, which callers keep for presentation.
func ForV3Decoder(ctx context.Context, source string) (string, []Notice, error) {
	if !strings.Contains(source, "%") {
		return source, nil, ctx.Err()
	}
	var adapted []byte
	var notices []Notice
	var marker, hasDirective bool
scan:
	for at, lineNumber := 0, 1; at < len(source); lineNumber++ {
		if err := ctx.Err(); err != nil {
			return "", nil, err
		}
		end := len(source)
		if next := strings.IndexAny(source[at:], "\r\n"); next >= 0 {
			end = at + next
		}
		line := source[at:end]
		if at == 0 && strings.HasPrefix(line, "\ufeff") {
			at += len("\ufeff")
			line = source[at:end]
		}
		switch trimmed := strings.TrimLeft(line, " \t"); {
		case trimmed == "" || trimmed[0] == '#':
		case line[0] != '%':
			marker = strings.HasPrefix(line, "---") && (len(line) == 3 || line[3] == ' ' || line[3] == '\t')
			break scan
		default:
			hasDirective = true
			name, rest := directiveParts(line)
			switch name {
			case "":
				return "", nil, fmt.Errorf("invalid YAML directive on line %d", lineNumber)
			case "TAG":
				// The parser owns syntax and tag-handle validation.
			case "YAML":
				match := yamlVersion.FindStringSubmatchIndex(rest)
				if match == nil {
					break
				}
				major := strings.TrimLeft(rest[match[2]:match[3]], "0")
				minor := strings.TrimLeft(rest[match[4]:match[5]], "0")
				if major != "" && major != "1" {
					return "", nil, fmt.Errorf("unsupported YAML major version %s on line %d", major, lineNumber)
				}
				if major == "" || minor == "" {
					break
				}
				if minor != "1" && minor != "2" {
					notices = append(notices, Notice{"yaml_future_minor_version", lineNumber,
						fmt.Sprintf("YAML 1.%s is interpreted using YAML 1.2 Core", minor)})
				}
				if adapted == nil {
					adapted = []byte(source)
				}
				start, stop := at+1+len(name)+match[2], at+1+len(name)+match[5]
				copy(adapted[start:stop], "1.1")
				for i := start + len("1.1"); i < stop; i++ {
					adapted[i] = ' '
				}
			default:
				notices = append(notices, Notice{"yaml_unknown_directive", lineNumber,
					fmt.Sprintf("Unknown YAML directive %%%s was ignored", name)})
				if adapted == nil {
					adapted = []byte(source)
				}
				adapted[at] = '#'
			}
		}
		at = end + 1
		if end+1 < len(source) && source[end] == '\r' && source[end+1] == '\n' {
			at++
		}
	}
	if hasDirective && !marker {
		return "", nil, fmt.Errorf("YAML directives require a document start marker")
	}
	if adapted != nil {
		source = string(adapted)
	}
	return source, notices, ctx.Err()
}

func directiveParts(line string) (name, rest string) {
	at := strings.IndexAny(line, " \t")
	if at < 0 {
		return line[1:], ""
	}
	return line[1:at], line[at:]
}
