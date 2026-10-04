---
layout: default
title: "Preparing a v0.1 migration"
lang: en
document_id: docs-contracts-migration-preparation
permalink: /contracts/migration-preparation/
---

<a id="preparing-a-v01-migration"></a>

# Preparing a v0.1 migration {#doc-section-001}

`migrate-prepare` reads the bundle and creates a new directory outside it. The directory must not exist. The command never opens the private store and never changes source documents.

```sh
okf migrate old-bundle --to 0.2 --format json
okf migrate-prepare old-bundle --output-dir ../migration-inputs --format json
```

The directory contains:

- `citation-mappings.json`: the exact closed `--citation-mappings` array format. The helper fills parser-owned `legacy_number` and exact `legacy_entry` selectors. Every `source_id` starts empty; choose and enter a real source identity before preview. It does not automatically accept proposed titles or resources.
- `generated-at.json`: the exact closed `--generated-at` array format. Documents with a legacy `type` but neither `timestamp` nor `generated` receive a blank `at`. Enter a real historical RFC3339 instant with `Z` or an explicit offset. Do not substitute the migration time for the content's creation time.
- `preparation-report.json`: source SHA-256, byte spans, candidate title/resource values, and unresolved cases. It is advice, never an apply input or authorization proof. `generated_by` is blank because only the caller knows the producer; `timestamp_policy=preserve` and `timestamp_conflict=reject` describe the existing CLI policy.

After resolving the report and filling the templates, preview and apply through the existing migration path:

```sh
okf migrate old-bundle --to 0.2 --actor human:author \
  --citation-mappings ../migration-inputs/citation-mappings.json \
  --generated-at ../migration-inputs/generated-at.json \
  --prepared-source-sha256 HASH_FROM_REPORT --format json

okf migrate old-bundle --to 0.2 --actor human:author \
  --citation-mappings ../migration-inputs/citation-mappings.json \
  --generated-at ../migration-inputs/generated-at.json \
  --prepared-source-sha256 HASH_FROM_REPORT --write --format json
```

`--prepared-source-sha256` rejects a changed bundle before planning. Regenerate the directory under a new name after any source change. The normal preview/apply path still checks its own revision, source resolution, request digest and plan proof. Empty `source_id` or `at` values fail the strict input loaders, so an unconfirmed template cannot be applied. When no per-document instant is needed, `generated-at.json` is `[]`; the flag may be omitted.

For example, two identical unnumbered citations cannot be selected independently by the current mapping DTO. The report marks both as `duplicate or unrepresentable parser-owned citation selectors` and omits them from the template. The caller must resolve the ambiguity in the source or use a supported unique selector, then prepare again. An unrecognized prose paragraph inside `# Citations` is reported with its byte span; it is never silently removed. A fenced pseudo-citation is reported as opaque content, not converted to a source.

If a document already has structured `sources` alongside legacy `# Citations`, the report emits `sources_citations_conflict` and omits a mapping for that document. The migration planner also blocks this mixed representation; the caller must reconcile it explicitly before preparing again.

A leading UTF-8 BOM before `---` is reported as `leading_bom_unsupported` at bytes 0–3. The exact frontmatter parser does not recognize that opening line; migration fails closed and leaves the document bytes unchanged. BOM bytes within valid body text or opaque YAML values are preserved by the existing mutation path.

The helper exports no migration proof and performs no mutation. A blocked preview remains blocked until its manual actions are resolved.
