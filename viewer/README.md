# Offline knowledge viewer

For a runnable source-linked demo, use the [quickstart](../docs/quickstart.md)
and `scripts/build-viewer-demo.sh OUTPUT.html` from the repository root.

`okf view <bundle> --output viewer.html` exports a single read-only HTML file.
The [operational viewer recipe](../skills/open-knowledge-format/references/operational-workflows.md#export-and-inspect-an-offline-viewer) covers root/version/reference-time selection, output collision, local inspection, and missing-tool fallback.
The file embeds its CSS, JavaScript, concept projection, and relationship data.
It opens from disk without a server, network fetch, CDN, external fonts, or
runtime other than a browser. The UI contains a searchable list, a type filter,
deep links (`#<URL-encoded ConceptID>`), concept details, provenance, typed
incoming/outgoing edges, and an optional local SVG diagram. The diagram uses a
deterministic radial layout; it never runs a force simulation.

Flags:

- `--output FILE` is required. Existing files require `--overwrite`.
- `--max-nodes N` limits the bundle to N concepts (default 10,000). Export
  fails before publication when the limit is exceeded.
- `--spec auto|0.1|0.2` asserts the version declaration.
- `--temporal-profile instant-0b87c52|date-3fcbb9f` chooses the pinned 0.2
  temporal contract. The pinned date contract remains the default; instant is
  selected explicitly.
- `--as-of` evaluates staleness at an explicit reference time: RFC3339 with
  offset for instant, YYYY-MM-DD for date. Without it, the UI says
  `unevaluated`. No browser clock changes this state.

The JSON projection includes exporter and spec revision, reference basis,
separate trust/status/staleness fields, rendered Markdown, sources, and typed
edges. The HTML/CSS/JS assets here are original work with no third-party UI
libraries. Markdown uses the repository's Go Goldmark dependency; raw HTML and
unsafe URLs are suppressed, image requests are omitted, and the export uses a
content security policy that disallows network connections and permits only
the embedded script by hash. External source links open only on a user click.

Output is staged in a sibling temporary file and published atomically.
Concept Markdown and `.okf/` store paths are never valid output targets.
