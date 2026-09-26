---
title: Repository knowledge bundle
description: How to read and maintain the architecture concepts in knowledge/.
permalink: /knowledge/
---

{% include nav.html %}

# Repository knowledge bundle

[`knowledge/index.md`](https://github.com/skosovsky/okf/blob/main/knowledge/index.md) is a small OKF v0.2 bundle about this repository's architecture. Start there when locating the owner of a change, the write boundary, supported platforms, or the relationship between document and package versions. Open the linked source contract or Go file before relying on a detail in a concept. The concepts are navigation aids, not replacement specifications.

Every initial concept has `status: draft`. Its `sources[].resource` values are URLs pinned to repository commit `ed7ddc28cd127682023bd150ee377906b817e099`. That makes source references followable outside the bundle without pretending they are bundle-internal links. A pinned URL identifies evidence at that revision; it does not mean the current implementation still matches it, and the validator does not fetch it. Footnote IDs in the body identify which source supports a claim. A later editor should change a concept only after checking the current canonical source, update the source URL to the revision actually used, and add a dated entry to [`knowledge/log.md`](https://github.com/skosovsky/okf/blob/main/knowledge/log.md).

Keep producer identity and verification honest. Do not add `generated`, `verified`, `stale_after`, usage counts, or source modification times by inference from Git history or a successful validation run. Record a verification event only when its actor and check actually happened. Record the current rule in the concept body; use the log for the change history. When code and concept disagree, fix the concept or mark its uncertainty explicitly, then confirm against the source contract.

## Validate

Run the Go CLI with an explicit reference time:

```sh
go run ./cmd/okf validate --path knowledge --spec auto --strict --check-links --check-orphans --as-of 2026-09-26 --max-warnings=0 --json
```

The required CI policy is zero errors and zero warnings in this checked-in bundle. `--max-warnings=0` makes the warning limit an exit-code gate; `--strict` enables guidance checks for present v0.2 metadata; `--check-links` and `--check-orphans` check local navigation. The `--as-of` value is fixed for deterministic CI until the date contract is revised; update it deliberately with the associated test and task. A separate audit should still read claims against their external sources because this command does not fetch URLs or prove factual accuracy.

## View

Generate the offline HTML viewer from the authored bundle. Choose an output path outside `knowledge/`, so the exported page is never read back as an OKF concept:

```sh
go run ./cmd/okf view knowledge --output /tmp/okf-knowledge-view.html --spec auto --as-of 2026-09-26
```

Open the generated file in a browser. It is a derived snapshot: regenerate it after changing the concepts. The CI artifact `knowledge-viewer` carries the same generated HTML for review; the HTML is not an editable source file and should not be committed.

For an opt-in check of whether a work session should update this bundle, see the [knowledge upkeep guide](https://github.com/skosovsky/okf/blob/main/docs/knowledge-upkeep.md). Its checker reports review evidence; it does not assign verification or change a concept automatically.

`knowledge/` is the authored bundle. The filesystem store's `<bundle>/.okf` directory contains private journals, staging files, and receipts; do not commit it or use it to store knowledge concepts.
