package bundle

import (
	"reflect"
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
