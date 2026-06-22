package bundle

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadBundleLoadsAllConcepts(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := appendixABundle(t)

	// Act.
	bundle, err := LoadBundle(root)

	// Assert.
	if err != nil {
		t.Fatalf("LoadBundle() error = %v", err)
	}
	if got, want := bundle.Len(), 3; got != want {
		t.Fatalf("Len() = %d, want %d", got, want)
	}
	assertBundleContains(t, bundle, "tables/orders")
	assertBundleContains(t, bundle, "datasets/sales")
	if got := len(bundle.ParseErrors()); got != 0 {
		t.Fatalf("len(ParseErrors()) = %d, want 0", got)
	}
}

func TestLoadBundleResolvesCrossLinksAndBacklinks(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := appendixABundle(t)
	bundle, err := LoadBundle(root)
	if err != nil {
		t.Fatalf("LoadBundle() error = %v", err)
	}
	sales := mustParseConceptID(t, "datasets/sales")
	orders := mustParseConceptID(t, "tables/orders")
	customers := mustParseConceptID(t, "tables/customers")

	// Act.
	salesLinks := bundle.LinksFrom(sales)
	orderBacklinks := bundle.Backlinks(orders)

	// Assert.
	if !resolvedLinksContain(salesLinks, orders) {
		t.Fatalf("sales links = %#v, want target %s", salesLinks, orders)
	}
	if !resolvedLinksContain(salesLinks, customers) {
		t.Fatalf("sales links = %#v, want target %s", salesLinks, customers)
	}
	for _, link := range salesLinks {
		if !link.Exists {
			t.Fatalf("sales link %#v Exists = false, want true", link)
		}
	}
	if !conceptIDsContain(orderBacklinks, sales) {
		t.Fatalf("orders backlinks = %#v, want %s", orderBacklinks, sales)
	}
	if !conceptIDsContain(orderBacklinks, customers) {
		t.Fatalf("orders backlinks = %#v, want %s", orderBacklinks, customers)
	}
	if got := len(bundle.BrokenLinks()); got != 0 {
		t.Fatalf("len(BrokenLinks()) = %d, want 0", got)
	}
}

func TestBrokenLinksAreDetectedButNotFatal(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeFile(t, root, "a.md", "---\ntype: Note\n---\nSee [missing](/does/not/exist.md).\n")
	bundle, err := LoadBundle(root)
	if err != nil {
		t.Fatalf("LoadBundle() error = %v", err)
	}

	// Act.
	broken := bundle.BrokenLinks()

	// Assert.
	if got, want := len(broken), 1; got != want {
		t.Fatalf("len(BrokenLinks()) = %d, want %d", got, want)
	}
	if got, want := broken[0].Raw, "/does/not/exist.md"; got != want {
		t.Fatalf("BrokenLinks()[0].Raw = %q, want %q", got, want)
	}
}

func TestReservedFilesAreRecognizedNotConcepts(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeFile(t, root, "a.md", "---\ntype: Note\n---\nbody\n")
	writeFile(t, root, "index.md", "# Listing\n\n* [a](a.md)\n")
	writeFile(t, root, "log.md", "# Log\n\n## 2026-05-22\n* **Update**: did a thing.\n")

	// Act.
	bundle, err := LoadBundle(root)

	// Assert.
	if err != nil {
		t.Fatalf("LoadBundle() error = %v", err)
	}
	if got, want := bundle.Len(), 1; got != want {
		t.Fatalf("Len() = %d, want %d", got, want)
	}
	if got, want := len(bundle.IndexFiles()), 1; got != want {
		t.Fatalf("len(IndexFiles()) = %d, want %d", got, want)
	}
	if got, want := len(bundle.LogFiles()), 1; got != want {
		t.Fatalf("len(LogFiles()) = %d, want %d", got, want)
	}
}

func TestOKFVersionReadFromRootIndex(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeFile(t, root, "a.md", "---\ntype: Note\n---\nbody\n")
	writeFile(t, root, "index.md", "---\nokf_version: \"0.1\"\n---\n\n# Listing\n")
	bundle, err := LoadBundle(root)
	if err != nil {
		t.Fatalf("LoadBundle() error = %v", err)
	}

	// Act.
	version, ok := bundle.OKFVersion()

	// Assert.
	if !ok {
		t.Fatal("OKFVersion() ok = false, want true")
	}
	if version != "0.1" {
		t.Fatalf("OKFVersion() = %q, want 0.1", version)
	}
}

func TestLoadBundleCollectsParseErrors(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeFile(t, root, "bad.md", "---\ntype: Note\n")

	// Act.
	bundle, err := LoadBundle(root)

	// Assert.
	if err != nil {
		t.Fatalf("LoadBundle() error = %v", err)
	}
	if got, want := bundle.Len(), 0; got != want {
		t.Fatalf("Len() = %d, want %d", got, want)
	}
	if got, want := len(bundle.ParseErrors()), 1; got != want {
		t.Fatalf("len(ParseErrors()) = %d, want %d", got, want)
	}
}

func appendixABundle(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "datasets/sales.md", "---\n"+
		"type: BigQuery Dataset\n"+
		"title: Sales\n"+
		"description: All sales-related tables for the retail business.\n"+
		"resource: https://console.cloud.google.com/bigquery?p=acme&d=sales\n"+
		"tags: [sales]\n"+
		"timestamp: 2026-05-28T00:00:00Z\n"+
		"---\n\n"+
		"The sales dataset contains transactional tables, including\n"+
		"[orders](/tables/orders.md) and [customers](/tables/customers.md).\n")
	writeFile(t, root, "tables/orders.md", "---\n"+
		"type: BigQuery Table\n"+
		"title: Orders\n"+
		"description: One row per completed customer order.\n"+
		"resource: https://console.cloud.google.com/bigquery?p=acme&d=sales&t=orders\n"+
		"tags: [sales, orders]\n"+
		"timestamp: 2026-05-28T00:00:00Z\n"+
		"---\n\n"+
		"# Schema\n\n"+
		"Part of the [sales dataset](/datasets/sales.md). FK to [customers](/tables/customers.md).\n")
	writeFile(t, root, "tables/customers.md", "---\n"+
		"type: BigQuery Table\n"+
		"title: Customers\n"+
		"description: One row per customer.\n"+
		"timestamp: 2026-05-28T00:00:00Z\n"+
		"---\n\n"+
		"Linked from [orders](/tables/orders.md).\n")
	return root
}

func writeFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func mustParseConceptID(t *testing.T, raw string) ConceptID {
	t.Helper()
	id, err := ParseConceptID(raw)
	if err != nil {
		t.Fatalf("ParseConceptID(%q) error = %v", raw, err)
	}
	return id
}

func assertBundleContains(t *testing.T, bundle *Bundle, raw string) {
	t.Helper()
	id := mustParseConceptID(t, raw)
	if !bundle.Contains(id) {
		t.Fatalf("Contains(%q) = false, want true", raw)
	}
}

func resolvedLinksContain(links []ResolvedLink, target ConceptID) bool {
	for _, link := range links {
		if link.Target.String() == target.String() {
			return true
		}
	}
	return false
}

func conceptIDsContain(ids []ConceptID, target ConceptID) bool {
	for _, id := range ids {
		if id.String() == target.String() {
			return true
		}
	}
	return false
}
