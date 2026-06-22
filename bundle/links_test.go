package bundle

import "testing"

func TestClassifyLink(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		target string
		want   LinkKind
	}{
		{name: "absolute", target: "/tables/users.md", want: LinkAbsolute},
		{name: "relative current", target: "./other.md", want: LinkRelative},
		{name: "relative parent", target: "../sibling.md", want: LinkRelative},
		{name: "https", target: "https://example.com", want: LinkExternal},
		{name: "mailto", target: "mailto:a@b.com", want: LinkExternal},
		{name: "anchor", target: "#section", want: LinkAnchor},
		{name: "protocol relative", target: "//cdn.example.com/x.js", want: LinkExternal},
		{name: "empty", target: "", want: LinkOther},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			target := tt.target

			// Act.
			got := ClassifyLink(target)

			// Assert.
			if got != tt.want {
				t.Fatalf("ClassifyLink(%q) = %s, want %s", target, got, tt.want)
			}
		})
	}
}

func TestExtractLinks(t *testing.T) {
	t.Parallel()

	// Arrange.
	body := `See [customers](/tables/customers.md) and [docs](https://example.com "title").`

	// Act.
	links := ExtractLinks(body)

	// Assert.
	if got, want := len(links), 2; got != want {
		t.Fatalf("len(ExtractLinks()) = %d, want %d", got, want)
	}
	if got, want := links[0].Text, "customers"; got != want {
		t.Fatalf("links[0].Text = %q, want %q", got, want)
	}
	if got, want := links[0].Target, "/tables/customers.md"; got != want {
		t.Fatalf("links[0].Target = %q, want %q", got, want)
	}
	if got, want := links[0].Kind, LinkAbsolute; got != want {
		t.Fatalf("links[0].Kind = %s, want %s", got, want)
	}
	if got, want := links[1].Target, "https://example.com"; got != want {
		t.Fatalf("links[1].Target = %q, want %q", got, want)
	}
}

func TestResolveAbsoluteLink(t *testing.T) {
	t.Parallel()

	// Arrange.
	source, err := ParseConceptID("tables/orders")
	if err != nil {
		t.Fatalf("ParseConceptID() error = %v", err)
	}
	link := Link{
		Text:   "customers",
		Target: "/tables/customers.md",
		Kind:   LinkAbsolute,
	}

	// Act.
	target, ok := link.Resolve(source)

	// Assert.
	if !ok {
		t.Fatal("Resolve() ok = false, want true")
	}
	if got, want := target.String(), "tables/customers"; got != want {
		t.Fatalf("Resolve() = %q, want %q", got, want)
	}
}

func TestResolveRelativeLink(t *testing.T) {
	t.Parallel()

	// Arrange.
	source, err := ParseConceptID("tables/orders")
	if err != nil {
		t.Fatalf("ParseConceptID() error = %v", err)
	}
	neighbor := Link{Text: "neighbor", Target: "./customers.md", Kind: LinkRelative}
	up := Link{Text: "up", Target: "../datasets/sales.md", Kind: LinkRelative}

	// Act.
	neighborTarget, neighborOK := neighbor.Resolve(source)
	upTarget, upOK := up.Resolve(source)

	// Assert.
	if !neighborOK {
		t.Fatal("neighbor.Resolve() ok = false, want true")
	}
	if got, want := neighborTarget.String(), "tables/customers"; got != want {
		t.Fatalf("neighbor.Resolve() = %q, want %q", got, want)
	}
	if !upOK {
		t.Fatal("up.Resolve() ok = false, want true")
	}
	if got, want := upTarget.String(), "datasets/sales"; got != want {
		t.Fatalf("up.Resolve() = %q, want %q", got, want)
	}
}

func TestResolveAbsoluteLinkNormalizesDotSegments(t *testing.T) {
	t.Parallel()

	// Arrange.
	source, err := ParseConceptID("a/b")
	if err != nil {
		t.Fatalf("ParseConceptID() error = %v", err)
	}
	link := Link{Text: "x", Target: "/tables/../datasets/sales.md", Kind: LinkAbsolute}

	// Act.
	target, ok := link.Resolve(source)

	// Assert.
	if !ok {
		t.Fatal("Resolve() ok = false, want true")
	}
	if got, want := target.String(), "datasets/sales"; got != want {
		t.Fatalf("Resolve() = %q, want %q", got, want)
	}
}

func TestResolveExternalLinkReturnsFalse(t *testing.T) {
	t.Parallel()

	// Arrange.
	source, err := ParseConceptID("a")
	if err != nil {
		t.Fatalf("ParseConceptID() error = %v", err)
	}
	link := Link{Text: "x", Target: "https://example.com", Kind: LinkExternal}

	// Act.
	_, ok := link.Resolve(source)

	// Assert.
	if ok {
		t.Fatal("Resolve() ok = true, want false")
	}
}

func TestExtractLinksInsideCodeAreIgnored(t *testing.T) {
	t.Parallel()

	// Arrange.
	body := "Real [a](/a.md).\n\n```\nNot a [link](/b.md) in code.\n```\n\nInline `[c](/c.md)` ignored.\n"

	// Act.
	links := ExtractLinks(body)

	// Assert.
	if got, want := len(links), 1; got != want {
		t.Fatalf("len(ExtractLinks()) = %d, want %d", got, want)
	}
	if got, want := links[0].Target, "/a.md"; got != want {
		t.Fatalf("links[0].Target = %q, want %q", got, want)
	}
}

func TestExtractLinksTildeFenceIsIgnored(t *testing.T) {
	t.Parallel()

	// Arrange.
	body := "Real [a](/a.md).\n\n~~~\nNot a [link](/b.md) in code.\n~~~\n"

	// Act.
	links := ExtractLinks(body)

	// Assert.
	if got, want := len(links), 1; got != want {
		t.Fatalf("len(ExtractLinks()) = %d, want %d", got, want)
	}
	if got, want := links[0].Target, "/a.md"; got != want {
		t.Fatalf("links[0].Target = %q, want %q", got, want)
	}
}

func TestExtractLinksNestedTextAndParentheses(t *testing.T) {
	t.Parallel()

	// Arrange.
	body := "See [outer [inner]](/docs/a_(b).md)."

	// Act.
	links := ExtractLinks(body)

	// Assert.
	if got, want := len(links), 1; got != want {
		t.Fatalf("len(ExtractLinks()) = %d, want %d", got, want)
	}
	if got, want := links[0].Text, "outer [inner]"; got != want {
		t.Fatalf("links[0].Text = %q, want %q", got, want)
	}
	if got, want := links[0].Target, "/docs/a_(b).md"; got != want {
		t.Fatalf("links[0].Target = %q, want %q", got, want)
	}
}

func TestExtractCitations(t *testing.T) {
	t.Parallel()

	// Arrange.
	body := "Prose.\n\n# Citations\n\n" +
		"[1] [BigQuery schema](https://bq.example/schema)\n" +
		"[2] [Runbook](https://wiki.acme.internal/runbook)\n"

	// Act.
	citations := ExtractCitations(body)

	// Assert.
	if got, want := len(citations), 2; got != want {
		t.Fatalf("len(ExtractCitations()) = %d, want %d", got, want)
	}
	if got, want := citations[0].Number, 1; got != want {
		t.Fatalf("citations[0].Number = %d, want %d", got, want)
	}
	if got, want := citations[0].Text, "BigQuery schema"; got != want {
		t.Fatalf("citations[0].Text = %q, want %q", got, want)
	}
	if got, want := citations[0].Target, "https://bq.example/schema"; got != want {
		t.Fatalf("citations[0].Target = %q, want %q", got, want)
	}
	if got, want := citations[1].Number, 2; got != want {
		t.Fatalf("citations[1].Number = %d, want %d", got, want)
	}
}

func TestExtractCitationsStopAtNextHeading(t *testing.T) {
	t.Parallel()

	// Arrange.
	body := "# Citations\n[1] [a](https://a)\n\n# Other\n[2] [b](https://b)\n"

	// Act.
	citations := ExtractCitations(body)

	// Assert.
	if got, want := len(citations), 1; got != want {
		t.Fatalf("len(ExtractCitations()) = %d, want %d", got, want)
	}
	if got, want := citations[0].Target, "https://a"; got != want {
		t.Fatalf("citations[0].Target = %q, want %q", got, want)
	}
}

func TestExtractCitationsRawEntryWithoutLink(t *testing.T) {
	t.Parallel()

	// Arrange.
	body := "# Citations\n[3] Internal spreadsheet, archived copy.\n"

	// Act.
	citations := ExtractCitations(body)

	// Assert.
	if got, want := len(citations), 1; got != want {
		t.Fatalf("len(ExtractCitations()) = %d, want %d", got, want)
	}
	if got, want := citations[0].Number, 3; got != want {
		t.Fatalf("citations[0].Number = %d, want %d", got, want)
	}
	if got, want := citations[0].Raw, "Internal spreadsheet, archived copy."; got != want {
		t.Fatalf("citations[0].Raw = %q, want %q", got, want)
	}
	if citations[0].Text != "" || citations[0].Target != "" {
		t.Fatalf("citation link fields = %q, %q; want empty", citations[0].Text, citations[0].Target)
	}
}
