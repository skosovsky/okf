---
layout: default
title: Offline knowledge viewer
lang: en
permalink: /readings/viewer/README/
document_id: viewer-readme
source: viewer/README.md
source_revision: 61e75e9aa9a8719dfb480bf1f5226553b3a21d70
revision_note: "The revision identifies the previous published baseline; this edition includes the current task 017 interface language contract."
preserve_source: false
---

{% include nav.html %}

<a id="offline-knowledge-viewer"></a>

# Offline knowledge viewer {#page-top}

Export a bundle as one HTML file to read its notes, check their sources and follow links without installing a separate viewer. Start with the [first working example]({{ '/quickstart/' | relative_url }}). The source-linked repository demo can also be exported with `scripts/build-viewer-demo.sh OUTPUT.html` from the repository root.

## Export and options {#export}

```sh
okf view <bundle> --output viewer.html --lang en
```

The file embeds its CSS, JavaScript, concept projection and relationship data. It opens from disk in a browser without a server, network requests, CDN or external fonts. The interface has a searchable list, a type filter, deep links (`#<URL-encoded ConceptID>`), note details, sources, typed incoming/outgoing links and an optional local SVG graph. The graph uses a deterministic radial layout rather than a force simulation.

| Option | Behavior |
|---|---|
| `--lang en\|ru` | Initial interface language; default `en`. Unsupported languages fail before creating or replacing output. |
| `--output FILE` | Required output path. Use `--overwrite` to replace an existing file. |
| `--max-nodes N` | Maximum concepts, default 10,000. Exceeding the limit fails before publication. |
| `--spec auto\|0.1\|0.2` | Asserts the bundle's version declaration. |
| `--temporal-profile instant-0b87c52\|date-3fcbb9f` | Chooses the pinned OKF 0.2 time contract. The date contract is the default; choose the instant contract explicitly. |
| `--as-of` | Reference time for evaluating staleness: RFC3339 with an offset for instant, YYYY-MM-DD for date. Without this option, staleness is `unevaluated`. The browser clock does not change it. |

The [operational export recipe]({{ '/readings/skills/open-knowledge-format/references/operational-workflows/#section-3' | relative_url }}) explains how to choose the bundle root, version and reference time, handle output collisions, inspect the result locally and proceed when the tool is unavailable.

A common error is exporting to an existing file without `--overwrite`. Choose a new path or explicitly allow replacement. Output is staged in a sibling temporary file and published atomically. A concept's Markdown file and any `.okf/` store path are never valid output targets.

## Projection and security {#security}

Projection JSON includes the exporter version, specification revision, reference basis, separate trust/status/staleness fields, rendered Markdown, sources and typed edges. The HTML/CSS/JavaScript assets are original work without third-party interface libraries. Markdown is rendered by the repository's Go Goldmark dependency. Raw HTML and unsafe URLs are suppressed; image requests are omitted. The content security policy blocks network connections and permits only the embedded script with its matching hash. External source links open only when the reader clicks them.

## Sources and deep links {#sources-and-links}

The Sources panel follows the document's effective provenance rules. The presence of a YAML `sources` key replaces legacy `# Citations`, even when the replacement is empty, null, malformed or duplicated. Only valid structured entries enter the projection. An invalid replacement never reactivates legacy citations.

When `sources` is absent, the parser's legacy citation entries appear in document order. Available titles and resources are preserved; the viewer does not invent source IDs, URLs, verification or trust. Text-only entries have an empty resource and display their text. The original Citations section remains in the rendered body.

An existing ConceptID takes precedence when resolving the decoded URL fragment, including IDs beginning with `fn:` or `fnref:`. Percent-encoded deep links and browser Back/Forward use the same route. Actual Goldmark footnote links and backlinks scroll inside the current card without changing its concept route. An existing footnote anchor fragment preserves that card. An unknown fragment falls back to the first concept. Footnote labels are not source records. Repeated references use Goldmark's numbered backlink anchors `fnref1:`, `fnref2:`, etc., with the same local scrolling behavior.

## Interface language and Go API {#language}

`Options.Language` selects the export language; its empty value means `en`. Existing `Render(ctx, projection)` renders English. Use `RenderWithOptions(ctx, projection, RenderOptions{Language: "ru"})` for Russian. Only `en`, `ru` and the empty default are accepted. Language affects presentation and does not change semantic projection JSON.

The embedded EN/RU selector changes interface labels, known state values and relationship labels. It retains the current concept, search, type filter and graph toggle. Reload returns to the language selected at export; the viewer uses no storage or network to remember a runtime choice. Authored Markdown, identifiers, source text and unknown custom values remain unchanged. The document's `html lang` and title follow the active language.

## Edition and source {#edition}

Canonical source: `viewer/README.md`. The `source_revision` above identifies the previous published baseline, not the complete text of this edition. This edition includes the current task 017 additions: `Options.Language`, `RenderOptions`, `RenderWithOptions`, `--lang` and the runtime language selector. It describes the current working viewer contract and does not redefine the OKF specification.
