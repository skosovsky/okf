package bundle

import (
	"strings"
	"time"
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
	var log Log
	var current *LogDay

	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(trimmed, "## "); ok {
			if current != nil {
				log.Days = append(log.Days, *current)
			}
			current = &LogDay{Date: strings.TrimSpace(rest)}
			continue
		}
		if rest, ok := strings.CutPrefix(trimmed, "# "); ok {
			if log.Title == "" && current == nil {
				log.Title = strings.TrimSpace(rest)
			}
			continue
		}
		unindented := strings.TrimRight(line, " \t\r")
		if body, ok := bulletBody(unindented); ok && current != nil {
			current.Entries = append(current.Entries, parseLogEntry(body))
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
				out.WriteString(entry.Text)
				out.WriteByte('\n')
				continue
			}
			out.WriteString("* ")
			out.WriteString(entry.Text)
			out.WriteByte('\n')
		}
	}

	return out.String()
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

func bulletBody(line string) (string, bool) {
	if rest, ok := strings.CutPrefix(line, "* "); ok {
		return rest, true
	}
	if rest, ok := strings.CutPrefix(line, "- "); ok {
		return rest, true
	}
	return "", false
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
