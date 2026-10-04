---
title: Viewer browser acceptance, 2026-09-26
description: Historical manual viewer acceptance observations and artifact digests.
lang: en
historical: true
permalink: /viewer-browser-review/
status: historical
documentation_id: docs-viewer-browser-review
source_revision: 61e75e9aa9a8719dfb480bf1f5226553b3a21d70
---

{% include nav.html %}

> Historical record. This page describes the results and requirements recorded in [revision `61e75e9`](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/docs/viewer-browser-review.md). It is not a report of checks on the current revision.

# Viewer browser acceptance, 2026-09-26 {#viewer-browser-acceptance-2026-09-26}

Browser acceptance **passed by manual review on 2026-09-26**. The user opened both generated local HTML files and confirmed all four requested scenarios: responsive search, selection, scrolling, graph and `#c-0999` deep link on 1,000 concepts; search and type filters on the small bundle; long Russian title, text and Go block at a narrow width; and `#missing`/`#russian` navigation with absent metadata and graph. The user reported the large page responded instantly and attached four screenshots to the result message. The screenshots show selected states; the interactions and responsiveness are established by the user's explicit report, not inferred from static images.

The browser-control URL policy rejected agent access to local `file://` pages and prohibited using another URL or browser surface as a workaround. The agent did not operate the page. This manual pass records the user's observations and keeps that provenance distinct from automated checks.

Reproduce the generated artifact from the repository root:

```sh
go run ./cmd/okf view knowledge --output /private/tmp/okf-viewer-009-knowledge.html --overwrite
shasum -a 256 /private/tmp/okf-viewer-009-knowledge.html
```

On this checkout the CLI printed `wrote "/private/tmp/okf-viewer-009-knowledge.html"`. The artifact was 49,302 bytes, SHA-256 `4efd1697e0002d09cbcae1104778b991b38f2c4dd1855857c0bf059454dee6a6`.

The executable checks that could run without a browser passed:

```sh
go test ./viewer -run 'Test(LargeBundleUsesBoundedListLayout|UnicodeConceptIDAndMissingMetadata)' -count=1 -v
go test ./viewer ./internal/okfcli -run 'Test(RenderEscapesScriptAndMarkdownHTML|BuildLinksLimitsAndReferenceTime|View)' -count=1 -v
```

The first command covers a 1,001-concept projection, a 1,000-node rejection, Unicode IDs, and absent metadata. The second covers the small bundle, escaping, and CLI export. These Go-level checks complement the manual browser pass.

## Prepared manual-review files {#manual-review-files}

Two further exports are available without running a browser here:

| File | Contents | Bytes | SHA-256 |
| --- | --- | ---: | --- |
| `/private/tmp/okf-viewer-009-small.html` | Three concepts (`russian`, `missing`, `other`) | 14,040 | `88be4465becdd10d8a69a6765c5593a473ef1e0dac2a4634330ea18c93ba4cce` |
| `/private/tmp/okf-viewer-009-1000.html` | Exactly 1,000 `Note` concepts | 285,716 | `11b9d91d0abfc7702526c3490325b4d95cb42bce09774713e215dd58c05b1bad` |

Source bundles are at `/private/tmp/okf-viewer-009-small-bundle` and `/private/tmp/okf-viewer-009-large-bundle`. They were exported with:

```sh
go run ./cmd/okf view /private/tmp/okf-viewer-009-small-bundle --output /private/tmp/okf-viewer-009-small.html --max-nodes 10 --overwrite
go run ./cmd/okf view /private/tmp/okf-viewer-009-large-bundle --output /private/tmp/okf-viewer-009-1000.html --max-nodes 1000 --overwrite
```

The large export with `--max-nodes 999` failed with `bundle has 1000 concepts; exceeds --max-nodes=999` and did not create `/private/tmp/okf-viewer-009-overlimit.html`.

The manual browser pass followed these steps:

1. Select `russian`. Confirm the long Russian title wraps within the list and detail panel at a narrow window width, the Cyrillic paragraph renders, and the fenced Go block retains line breaks and indentation.
2. Search for `проверяшка`; the list should show only `russian`. Clear search, choose type `Decision`; only `other` should remain. Change type to `Architecture Note`; only `russian` should remain.
3. Clear filters. Open `/private/tmp/okf-viewer-009-small.html#missing` directly. The selected detail should show ID/title `missing`, `type unknown`, `No sources recorded.`, and the missing lifecycle values. Click its `назад` link; the URL hash and selected detail should change to `#russian`. Follow the link back to `missing`.
4. Toggle `Show graph` and confirm the detail remains readable and the local link diagram renders without blocking the list.
5. Open the 1,000-concept HTML from disk. Search for `Concept 0999`, select it, clear search, and scroll the list. Confirm the page remains responsive; toggle graph for a concept and confirm it does not attempt a whole-bundle force layout. Open `#c-0999` directly and confirm the correct concept loads.

The user confirmed that all five checks above passed, corresponding to the four grouped scenarios sent in the review request. The four screenshots attached to that message show:

| Screenshot | What it visibly shows |
| --- | --- |
| Large page | Selected `c-0999`, search result and local graph |
| Small page, `other` | Selected `other`, Decision badge and small-bundle list |
| Small page, `russian` | Narrow layout, long Cyrillic title, Russian text, preserved Go block and link |
| Small page, `missing` | `missing`, `type unknown`, missing source text and navigation link |

The screens do not by themselves prove filter transitions, deep-link reloads, scroll responsiveness or timing; those points come from the user's manual report. The 999-node limit rejection comes from the CLI check above.
