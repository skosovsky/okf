package markdownowner

import (
	"context"
	"strings"
	"time"
)

// InspectLogContext checks date groups and flat entries in a nonblank log.
// report receives diagnostics in order and can stop inspection with an error.
func InspectLogContext(ctx context.Context, body string, report func(string, ...any) error) error {
	type logDay struct {
		date    string
		entries int
	}
	var days []logDay
	var current *logDay
	var badDateLevels, nonListLines []string

	blocks, err := TopLevelStructureContext(ctx, body)
	if err != nil {
		return err
	}
	for _, block := range blocks {
		if err := ctx.Err(); err != nil {
			return err
		}
		switch block.Kind {
		case TopLevelHeading:
			if block.HeadingLevel == 2 {
				if current != nil {
					days = append(days, *current)
				}
				current = &logDay{date: block.Text}
				continue
			}
			if IsLogDate(block.Text) {
				badDateLevels = append(badDateLevels, block.Raw)
			} else if current != nil {
				nonListLines = append(nonListLines, block.Raw)
			}
		case TopLevelList:
			if current == nil {
				continue
			}
			for _, item := range block.Items {
				if err := ctx.Err(); err != nil {
					return err
				}
				if block.Indent != 0 || item.Indent != 0 || item.HasNestedList {
					nonListLines = append(nonListLines, strings.TrimSpace(item.Raw))
					continue
				}
				current.entries++
			}
		default:
			if current != nil && strings.TrimSpace(block.Raw) != "" {
				nonListLines = append(nonListLines, block.Raw)
			}
		}
	}
	if current != nil {
		days = append(days, *current)
	}
	if len(days) == 0 && len(badDateLevels) == 0 {
		return report("log.md should contain ISO-8601 date headings")
	}
	for _, day := range days {
		if !IsLogDate(day.date) {
			if err := report("log date heading is not ISO-8601 YYYY-MM-DD: %q", day.date); err != nil {
				return err
			}
		}
	}
	for _, badLevel := range badDateLevels {
		if err := report("log date heading should use level 2: %q", badLevel); err != nil {
			return err
		}
	}
	previousDate := ""
	for _, day := range days {
		if day.entries == 0 {
			if err := report("log date heading has no entries: %q", day.date); err != nil {
				return err
			}
		}
	}
	for _, day := range days {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !IsLogDate(day.date) {
			continue
		}
		if previousDate != "" && day.date > previousDate {
			if err := report("log date headings should be newest first: %q", day.date); err != nil {
				return err
			}
		}
		previousDate = day.date
	}
	for _, line := range nonListLines {
		if err := report("log date group contains non-list entry: %q", line); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// IsLogDate accepts valid dates in the exact YYYY-MM-DD form.
func IsLogDate(value string) bool {
	if len(value) != len(time.DateOnly) {
		return false
	}
	_, err := time.Parse(time.DateOnly, value)
	return err == nil
}
