---
title: "Manual knowledge review: 2026-09-26"
description: "Manual knowledge review: 2026-09-26"
permalink: /knowledge-manual-review/
---

{% include nav.html %}

<span id="manual-navigation-review-of-knowledge"></span>

# Manual navigation review of `knowledge/` {#page-top}

**Historical check: 2026-09-26.** Results describe the state at that date. The reviewer started at `knowledge/index.md` and followed links as a reader. This checks content and navigation; the draft concepts did not receive formal verification, and pinned source URLs were not fetched over the network.

## Questions and results {#results}

| Engineer question | Route from the index | Answer found | Source checked |
| --- | --- | --- | --- |
| Where does a proposed mutation become a durable write? | `knowledge/index.md` → **Changes and durability** → `knowledge/mutation.md` → `knowledge/store.md` | A typed `store.ChangeSet` is previewed against a revision and plan digest; authorized apply goes through the store. MCP requires preview evidence. The store handles durable commit and recovery. | `store/change.go` defines the closed operation interface; `docs/toolkit.md` at the time documented MCP preview/apply pairs and patch revision/digest requirements. |
| Can the durable filesystem store run on this host? | `knowledge/index.md` → **Changes and durability** → `knowledge/platforms.md` | `store/fs` supports Darwin and Linux when filesystem capability checks pass. Windows and other targets compile, but `Open`/`OpenContext` return `*fs.UnsupportedPlatformError`; callers can use `errors.Is` with `fs.ErrUnsupportedPlatform`. | Package contract and error type in `store/fs/fs.go`; platform matrix in `docs/release-engineering.md`. |
| Does the package release choose the OKF version? | `knowledge/index.md` → **Reading and conformance** → `knowledge/versions.md`; also root index metadata | No. This bundle declares `okf_version: "0.2"`. The document contract is independent of plugin and Go-module versions; an absent declaration defaults to 0.2, an explicit supported selector asserts a version, and mismatch fails. | Version axes and resolution rules in `docs/toolkit.md`; types and implementation in `bundle/version.go`. |

All three routes yielded an answer without search or test assertions. One caveat remained: `knowledge/versions.md` explained the distinction without concrete package versions; the linked toolkit guide then illustrated plugin `0.2.0`, Go module `v0.2.1`, and document `0.2`. These illustrate independent versions rather than the latest release.

The table preserves the exact English source-note headings. For current work, use the [repository knowledge guide]({{ '/knowledge/' | relative_url }}).
