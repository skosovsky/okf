package bundle

import (
	"bytes"
	"context"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

type yamlKeySource struct {
	raw   []byte
	lines []yamlKeySourceLine
}

type yamlKeySourceLine struct {
	start            int
	nonASCII         bool
	characterOffsets []int
}

func newYAMLKeySource(ctx context.Context, raw []byte) (yamlKeySource, error) {
	if bytes.IndexByte(raw, '!') < 0 {
		return yamlKeySource{}, ctx.Err()
	}
	source := yamlKeySource{raw: raw, lines: []yamlKeySourceLine{{}}}
	for offset, nextCheck := 0, 0; offset < len(raw); {
		if offset >= nextCheck {
			if err := ctx.Err(); err != nil {
				return yamlKeySource{}, err
			}
			nextCheck = offset + contextStringChunkSize
		}
		r, size := utf8.DecodeRune(raw[offset:])
		offset += size
		switch r {
		case '\r':
			if offset < len(raw) && raw[offset] == '\n' {
				offset++
			}
			fallthrough
		case '\n', '\u0085', '\u2028', '\u2029':
			source.lines = append(source.lines, yamlKeySourceLine{start: offset})
		default:
			source.lines[len(source.lines)-1].nonASCII = source.lines[len(source.lines)-1].nonASCII || size > 1
		}
	}
	return source, ctx.Err()
}

// yaml.v3 resolves a bare `!` as if the tag were absent. Its source position
// still points to the initial property, including an anchor before the tag.
func (s yamlKeySource) hasNonSpecificTag(node *yaml.Node) bool {
	if node.Line < 1 || node.Line > len(s.lines) || node.Column < 1 {
		return false
	}
	line := &s.lines[node.Line-1]
	column := node.Column - 1
	offset := line.start + column
	if line.nonASCII {
		if line.characterOffsets == nil {
			end := len(s.raw)
			if node.Line < len(s.lines) {
				end = s.lines[node.Line].start
			}
			for pos := line.start; pos < end; {
				line.characterOffsets = append(line.characterOffsets, pos)
				_, size := utf8.DecodeRune(s.raw[pos:end])
				pos += size
			}
		}
		if column >= len(line.characterOffsets) {
			return false
		}
		offset = line.characterOffsets[column]
	}
	if offset >= len(s.raw) {
		return false
	}
	raw := s.raw[offset:]
	if raw[0] == '&' && node.Anchor != "" {
		raw = raw[min(len(raw), 1+len(node.Anchor)):]
		for {
			raw = bytes.TrimLeft(raw, " \t\r\n\u0085\u2028\u2029")
			if len(raw) == 0 || raw[0] != '#' {
				break
			}
			end := bytes.IndexAny(raw, "\r\n\u0085\u2028\u2029")
			if end < 0 {
				return false
			}
			raw = raw[end:]
		}
	}
	if len(raw) == 0 || raw[0] != '!' {
		return false
	}
	if len(raw) == 1 {
		return true
	}
	r, _ := utf8.DecodeRune(raw[1:])
	const separators = " \t\r\n\u0085\u2028\u2029,[]{}"
	return strings.ContainsRune(separators, r)
}
