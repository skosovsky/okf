package bundle

import (
	"strings"
	"time"

	"github.com/skosovsky/okf/internal/markdownowner"
)

// Log is a parsed log.md update history.
type Log struct {
	Title string
	Days  []LogDay
}

// LogDay contains entries under one date heading.
type LogDay struct {
	Date    string
	Entries []LogEntry
}

// LogEntry is one bullet in a log day.
type LogEntry struct {
	Kind string
	Text string
}

// ParseLog parses log.md text.
func ParseLog(text string) Log {
	if log, ok := parseSimpleLog(text); ok {
		return log
	}
	return parseLogMarkdown(text)
}

// parseSimpleLog recognizes only the canonical single-line form emitted by
// Log.Markdown. Anything with Markdown ownership ambiguity falls through to
// the shared parser, including continuations and alternate line endings.
func parseSimpleLog(text string) (Log, bool) {
	if !strings.HasPrefix(text, "# ") || !strings.HasSuffix(text, "\n") || strings.IndexByte(text, '\r') >= 0 {
		return Log{}, false
	}
	titleLine, rest, ok := strings.Cut(text[2:], "\n\n")
	if !ok || !plainLogText(titleLine) || !strings.HasPrefix(rest, "## ") {
		return Log{}, false
	}
	log := Log{Title: titleLine}
	for rest != "" {
		if !strings.HasPrefix(rest, "## ") {
			return Log{}, false
		}
		date, remainder, ok := strings.Cut(rest[3:], "\n")
		if !ok || !isoDateShape(date) {
			return Log{}, false
		}
		day := LogDay{Date: date}
		rest = remainder
		for rest != "" && rest != "\n" && !strings.HasPrefix(rest, "\n## ") {
			if !strings.HasPrefix(rest, "* ") && !strings.HasPrefix(rest, "- ") {
				return Log{}, false
			}
			body, next, ok := strings.Cut(rest[2:], "\n")
			if !ok || !plainLogText(body) {
				return Log{}, false
			}
			day.Entries = append(day.Entries, LogEntry{Text: body})
			rest = next
		}
		log.Days = append(log.Days, day)
		if rest == "\n" {
			return Log{}, false
		}
		if rest != "" {
			rest = rest[1:]
		}
	}
	return log, true
}

func plainLogText(value string) bool {
	if value == "" || strings.TrimSpace(value) != value ||
		!(value[0] >= 'A' && value[0] <= 'Z' || value[0] >= 'a' && value[0] <= 'z') {
		return false
	}
	for _, ch := range value {
		if ch < ' ' || strings.ContainsRune("\\`*_[]<>#!|", ch) {
			return false
		}
	}
	return true
}

func isoDateShape(value string) bool {
	if len(value) != 10 || value[4] != '-' || value[7] != '-' {
		return false
	}
	for _, index := range []int{0, 1, 2, 3, 5, 6, 8, 9} {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

func parseLogMarkdown(text string) Log {
	var log Log
	var current *LogDay

	for _, block := range markdownowner.TopLevelStructure(text) {
		switch block.Kind {
		case markdownowner.TopLevelHeading:
			if block.HeadingLevel == 1 && log.Title == "" && current == nil {
				log.Title = block.Text
			} else if block.HeadingLevel == 2 {
				if current != nil {
					log.Days = append(log.Days, *current)
				}
				current = &LogDay{Date: block.Text}
			}
		case markdownowner.TopLevelList:
			if current == nil || block.Indent != 0 {
				continue
			}
			for _, item := range block.Items {
				if item.Indent == 0 && !item.HasNestedList {
					current.Entries = append(current.Entries, parseLogEntry(item.Content))
				}
			}
		}
	}
	if current != nil {
		log.Days = append(log.Days, *current)
	}

	return log
}

// Markdown renders the log back to markdown.
func (l Log) Markdown() string {
	var out strings.Builder
	if l.Title != "" {
		out.WriteString("# ")
		out.WriteString(l.Title)
		out.WriteString("\n\n")
	}

	for i, day := range l.Days {
		if i > 0 {
			out.WriteByte('\n')
		}
		out.WriteString("## ")
		out.WriteString(day.Date)
		out.WriteByte('\n')
		for _, entry := range day.Entries {
			if entry.Kind != "" {
				out.WriteString("* **")
				out.WriteString(entry.Kind)
				out.WriteString("**: ")
				writeLogEntryText(&out, entry.Text)
				out.WriteByte('\n')
				continue
			}
			out.WriteString("* ")
			writeLogEntryText(&out, entry.Text)
			out.WriteByte('\n')
		}
	}

	return out.String()
}

func writeLogEntryText(out *strings.Builder, value string) {
	lines := strings.Split(value, "\n")
	for index, line := range lines {
		if index > 0 {
			out.WriteByte('\n')
			if line != "" {
				out.WriteString("  ")
			}
		}
		out.WriteString(line)
	}
}

// InvalidDates returns date headings that are not valid YYYY-MM-DD dates.
func (l Log) InvalidDates() []string {
	var invalid []string
	for _, day := range l.Days {
		if !isISODate(day.Date) {
			invalid = append(invalid, day.Date)
		}
	}
	return invalid
}

// EmptyDates returns date headings with no log entries.
func (l Log) EmptyDates() []string {
	var empty []string
	for _, day := range l.Days {
		if len(day.Entries) == 0 {
			empty = append(empty, day.Date)
		}
	}
	return empty
}

// OutOfOrderDates returns date headings that break newest-first ordering.
func (l Log) OutOfOrderDates() []string {
	var out []string
	previous := ""
	for _, day := range l.Days {
		if !isISODate(day.Date) {
			continue
		}
		if previous != "" && day.Date > previous {
			out = append(out, day.Date)
		}
		previous = day.Date
	}
	return out
}

func isISODate(value string) bool {
	if len(value) != 10 || value[4] != '-' || value[7] != '-' {
		return false
	}
	for _, index := range []int{0, 1, 2, 3, 5, 6, 8, 9} {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	_, err := time.Parse("2006-01-02", value)
	return err == nil
}

func parseLogEntry(body string) LogEntry {
	trimmed := strings.TrimSpace(body)
	if rest, ok := strings.CutPrefix(trimmed, "**"); ok {
		if end := strings.Index(rest, "**"); end >= 0 {
			kind := strings.TrimSpace(rest[:end])
			text := strings.TrimLeft(rest[end+2:], " \t")
			text = strings.TrimLeft(strings.TrimPrefix(text, ":"), " \t")
			return LogEntry{Kind: kind, Text: text}
		}
	}
	return LogEntry{Text: trimmed}
}
