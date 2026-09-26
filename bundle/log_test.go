package bundle

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseLog(t *testing.T) {
	t.Parallel()

	// Arrange.
	text := "# Directory Update Log\n\n" +
		"## 2026-05-22\n" +
		"* **Update**: Added a new table reference.\n" +
		"- **Creation** Established the playbook.\n" +
		"* Plain entry.\n"

	// Act.
	log := ParseLog(text)

	// Assert.
	if got, want := log.Title, "Directory Update Log"; got != want {
		t.Fatalf("Title = %q, want %q", got, want)
	}
	if got, want := len(log.Days), 1; got != want {
		t.Fatalf("len(Days) = %d, want %d", got, want)
	}
	day := log.Days[0]
	if got, want := day.Date, "2026-05-22"; got != want {
		t.Fatalf("Date = %q, want %q", got, want)
	}
	if got, want := len(day.Entries), 3; got != want {
		t.Fatalf("len(Entries) = %d, want %d", got, want)
	}
	if got, want := day.Entries[0], (LogEntry{Kind: "Update", Text: "Added a new table reference."}); got != want {
		t.Fatalf("Entries[0] = %#v, want %#v", got, want)
	}
	if got, want := day.Entries[1], (LogEntry{Kind: "Creation", Text: "Established the playbook."}); got != want {
		t.Fatalf("Entries[1] = %#v, want %#v", got, want)
	}
	if got, want := day.Entries[2], (LogEntry{Text: "Plain entry."}); got != want {
		t.Fatalf("Entries[2] = %#v, want %#v", got, want)
	}
}

func TestParseLogPreservesWrappedEntries(t *testing.T) {
	t.Parallel()

	// Arrange.
	for _, lineEnding := range []string{"\n", "\r\n"} {
		input := strings.Join([]string{
			"# Log", "", "## 2026-09-21", "* **Update**: First line", "  indented continuation", "lazy continuation", "* Next item", "",
		}, lineEnding)

		// Act.
		parsed := ParseLog(input)

		// Assert.
		if len(parsed.Days) != 1 || len(parsed.Days[0].Entries) != 2 {
			t.Fatalf("line ending %q: parsed = %#v", lineEnding, parsed)
		}
		if got, want := parsed.Days[0].Entries[0], (LogEntry{Kind: "Update", Text: "First line\nindented continuation\nlazy continuation"}); got != want {
			t.Fatalf("line ending %q: first entry = %#v, want %#v", lineEnding, got, want)
		}
		if got := ParseLog(parsed.Markdown()).Days[0].Entries[0]; got != parsed.Days[0].Entries[0] {
			t.Fatalf("line ending %q: round trip = %#v", lineEnding, got)
		}
	}
}

func TestParseLogKeepsLooseItemParagraphs(t *testing.T) {
	t.Parallel()

	// Arrange.
	input := "# Log\n\n## 2026-09-21\n* First paragraph\n\n  Second paragraph\n"

	// Act.
	parsed := ParseLog(input)
	roundTrip := ParseLog(parsed.Markdown())

	// Assert.
	if len(parsed.Days) != 1 || len(parsed.Days[0].Entries) != 1 ||
		parsed.Days[0].Entries[0].Text != "First paragraph\n\nSecond paragraph" {
		t.Fatalf("parsed = %#v", parsed)
	}
	if !reflect.DeepEqual(parsed, roundTrip) {
		t.Fatalf("round trip = %#v, want %#v", roundTrip, parsed)
	}
}

func TestSimpleLogFastPathMatchesMarkdownOwnership(t *testing.T) {
	t.Parallel()

	// Arrange.
	inputs := []string{
		"# Log\n\n## 2026-09-21\n* One entry\n",
		"# Directory Update Log\n\n## 2026-09-21\n* First entry\n- Second entry.\n\n## 2026-09-20\n* Older entry\n",
		"# Log\n\n## 2026-09-21\n",
	}

	for _, input := range inputs {
		// Act.
		fast, ok := parseSimpleLog(input)
		slow := parseLogMarkdown(input)

		// Assert.
		if !ok || !reflect.DeepEqual(fast, slow) || !reflect.DeepEqual(ParseLog(input), slow) {
			t.Fatalf("fast=%#v ok=%t slow=%#v for %q", fast, ok, slow, input)
		}
	}
}

func TestSimpleLogFastPathDefersAmbiguousMarkdown(t *testing.T) {
	t.Parallel()

	// Arrange.
	inputs := []string{
		"# Log\r\n\r\n## 2026-09-21\r\n* One entry\r\n",
		"# Log\n\n## 2026-09-21\n* First\n  continuation\n",
		"# Log\n\n## 2026-09-21\n* First\nlazy continuation\n",
		"# Log\n\n## 2026-09-21\n* First\n\n  Second paragraph\n",
		"# Log\n\n## 2026-09-21\n* **Update**: First entry\n",
		"# Log\n\n## 2026-09-21\n* - nested list\n",
		"# Log\n\n## 2026-09-21\n* `literal`\n",
		"# Log\n\n## 2026-09-21\n* [linked](target.md)\n",
	}

	for _, input := range inputs {
		// Act.
		_, ok := parseSimpleLog(input)
		parsed := ParseLog(input)
		slow := parseLogMarkdown(input)

		// Assert.
		if ok || !reflect.DeepEqual(parsed, slow) {
			t.Fatalf("fast route=%t parsed=%#v slow=%#v for %q", ok, parsed, slow, input)
		}
	}
}

func TestSimpleLogFastPathPunctuationParity(t *testing.T) {
	t.Parallel()

	// Arrange.
	for _, sample := range []struct {
		value string
		fast  bool
	}{
		{"One entry", true}, {"A - B", true}, {"A + B", true}, {"A 1. item", true},
		{"A &amp; B", true}, {"A https://example.test/path", true}, {"A (note).", true},
		{"A: decision", true}, {"A 2026-09-21", true}, {"A: decision!", false},
	} {
		input := "# Log\n\n## 2026-09-21\n* " + sample.value + "\n"

		// Act.
		fast, ok := parseSimpleLog(input)
		slow := parseLogMarkdown(input)
		parsed := ParseLog(input)

		// Assert.
		if ok != sample.fast || !reflect.DeepEqual(parsed, slow) || (ok && !reflect.DeepEqual(fast, slow)) {
			t.Fatalf("value %q: fast=%#v ok=%t parsed=%#v slow=%#v", sample.value, fast, ok, parsed, slow)
		}
	}
}

func TestLogMarkdown(t *testing.T) {
	t.Parallel()

	// Arrange.
	log := Log{
		Title: "Directory Update Log",
		Days: []LogDay{
			{
				Date: "2026-05-22",
				Entries: []LogEntry{
					{Kind: "Update", Text: "Added a new table reference."},
					{Text: "Plain entry."},
				},
			},
			{
				Date:    "2026-05-21",
				Entries: []LogEntry{{Kind: "Creation", Text: "Established the playbook."}},
			},
		},
	}

	// Act.
	got := log.Markdown()

	// Assert.
	want := "# Directory Update Log\n\n" +
		"## 2026-05-22\n" +
		"* **Update**: Added a new table reference.\n" +
		"* Plain entry.\n\n" +
		"## 2026-05-21\n" +
		"* **Creation**: Established the playbook.\n"
	if got != want {
		t.Fatalf("Markdown() = %q, want %q", got, want)
	}
}

func TestLogInvalidDates(t *testing.T) {
	t.Parallel()

	// Arrange.
	log := Log{
		Days: []LogDay{
			{Date: "2026-05-22"},
			{Date: "May 22"},
			{Date: "2026-13-01"},
		},
	}

	// Act.
	got := log.InvalidDates()

	// Assert.
	want := []string{"May 22", "2026-13-01"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("InvalidDates() = %#v, want %#v", got, want)
	}
}

func TestLogEmptyAndOutOfOrderDates(t *testing.T) {
	t.Parallel()

	// Arrange.
	log := Log{
		Days: []LogDay{
			{Date: "2026-05-21", Entries: []LogEntry{{Text: "older"}}},
			{Date: "2026-05-22", Entries: []LogEntry{{Text: "newer"}}},
			{Date: "2026-05-20"},
			{Date: "May 19"},
		},
	}

	// Act.
	empty := log.EmptyDates()
	outOfOrder := log.OutOfOrderDates()

	// Assert.
	if want := []string{"2026-05-20", "May 19"}; !reflect.DeepEqual(empty, want) {
		t.Fatalf("EmptyDates() = %#v, want %#v", empty, want)
	}
	if want := []string{"2026-05-22"}; !reflect.DeepEqual(outOfOrder, want) {
		t.Fatalf("OutOfOrderDates() = %#v, want %#v", outOfOrder, want)
	}
}
