---
title: "Repository knowledge"
description: "Repository knowledge"
permalink: /knowledge/
---

{% include nav.html %}

<span id="repository-knowledge-bundle"></span>

# Architecture knowledge for this repository {#page-top}

`knowledge/` contains notes about OKF: change owners, write boundaries, platforms, and versions. These are working pointers to code and contracts. The source notes remain in English; translating this guide does not change their content.

## Find the document {#read}

Open the [root index](https://github.com/skosovsky/okf/blob/main/knowledge/index.md). For the write boundary, read `mutation` and `store`; for platforms, `platforms`; for versions, `versions`. Inspect the linked contract or Go file before making a change.

Initial notes have `status: draft`. Their sources pin revision `ed7ddc28cd127682023bd150ee377906b817e099`; a later MCP update also uses `0f15ab4091203f11caa7fb0b9e02da47d719a051`. A revision link identifies the material used when writing; check current code for present accuracy.

## Validate and view {#validate}

From the repository root:

```sh
go run ./cmd/okf validate --path knowledge --spec auto --strict --check-links --check-orphans --as-of 2026-09-26 --max-warnings=0 --json
go run ./cmd/okf view knowledge --output /tmp/okf-knowledge-view.html --spec auto --as-of 2026-09-26 --lang en
```

Expect zero errors and warnings, then HTML with an English interface and the original English notes. `2026-09-26` is fixed for reproducible CI; change it together with the validation policy. The command does not fetch external sources. Store HTML outside `knowledge/`; for another export choose a new path or explicitly add `--overwrite`.

## After a change {#update}

Compare affected claims with current sources. Update the note body and the URL for the revision actually used, then add a dated entry to the [log](https://github.com/skosovsky/okf/blob/main/knowledge/log.md). Record verification, authorship, and expiry from actual actions and decisions rather than successful validator execution.

CI publishes derived HTML as the `knowledge-viewer` artifact. Markdown is the source; `<bundle>/.okf` contains internal store journals, rather than concepts or files to commit.

Use the [knowledge upkeep check]({{ '/knowledge-upkeep/' | relative_url }}) for a repeatable process. If a link or rule is uncertain, open the contract and record the discrepancy before changing it.
