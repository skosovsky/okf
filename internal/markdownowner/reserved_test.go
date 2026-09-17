package markdownowner

import "testing"

func TestTopLevelStructureOwnsOnlyDirectMarkdownBlocks(t *testing.T) {
	t.Parallel()

	// Arrange.
	markdown := "Setext\n======\n\n" +
		"* [direct](direct.md) - Direct description\n\n" +
		"> # Quoted\n> * [quoted](quoted.md)\n\n" +
		"- item\n\n  # Listed\n\n  * [nested](nested.md)\n\n" +
		"```md\n# Code\n* [code](code.md)\n```\n\n" +
		"\\# Escaped\n"

	// Act.
	blocks := TopLevelStructure(markdown)

	// Assert.
	headings, linkedLists := 0, 0
	for _, block := range blocks {
		if block.Kind == TopLevelHeading {
			headings++
			if block.HeadingLevel != 1 || block.Text != "Setext" {
				t.Fatalf("heading = %#v", block)
			}
		}
		if block.Kind == TopLevelList {
			for _, item := range block.Items {
				if item.HasLink {
					linkedLists++
					if len(item.Links) != 1 || item.Links[0].Target != "direct.md" ||
						item.Description != "Direct description" {
						t.Fatalf("direct item = %#v", item)
					}
				}
			}
		}
	}
	if headings != 1 || linkedLists != 1 {
		t.Fatalf("TopLevelStructure() = %#v, want one direct heading/list link", blocks)
	}
}

func TestTopLevelStructureProjectsLazyListContinuations(t *testing.T) {
	t.Parallel()

	// Arrange.
	markdown := "## 2026-07-29\n* listed entry\nplain paragraph\n"

	// Act.
	blocks := TopLevelStructure(markdown)

	// Assert.
	if len(blocks) != 2 || len(blocks[1].Items) != 1 {
		t.Fatalf("TopLevelStructure() = %#v", blocks)
	}
	item := blocks[1].Items[0]
	if !item.Continuation || len(item.ContinuationTexts) != 1 || item.ContinuationTexts[0] != "plain paragraph" {
		t.Fatalf("list item = %#v, want parser-owned lazy continuation", item)
	}
}
