# Converting source material to OKF v0.2

Conversion maps known source data into concepts. It is not evidence generation.

## Common workflow

1. Inventory actual materials and stable identifiers.
2. Choose a descriptive, open `type` for each concept.
3. Preserve source content and unknown metadata losslessly.
4. Create `sources` only from materials used to produce the concept.
5. Add keyed footnotes only when claim-to-source mapping is demonstrable.
6. Add `generated` only when the producer actor is known.
7. Add `verified` only after a separate real check.
8. Create directory indexes and one optional root version declaration.
9. Validate; keep strict guidance separate from base conformance.

Do not infer `status`, `stale_after`, verification, credibility, receipt, or
attestation from import success, file dates, git history, or source ownership.

## Notion or document exports

- Map explicit properties to frontmatter without inventing missing values.
- Remove export-only filename suffixes only when link rewrites are provable.
- Convert known internal links to Markdown links.
- Preserve unsupported blocks as Markdown/HTML instead of silently dropping.
- Treat export author/time as source metadata unless they explicitly describe
  production of the current concept.

## Obsidian vaults

- Convert resolvable wikilinks to Markdown links.
- Preserve unresolved wikilinks as content or report manual actions.
- Move tags only when their parsing is unambiguous.
- Require or obtain a `type`; do not guess a closed taxonomy.
- Do not interpret backlinks, modification time, or graph centrality as
  verification or trust.

## CSV and spreadsheets

- Define an explicit row-to-concept mapping before conversion.
- Use a stable source column for filename/ID; fail on collisions.
- Map columns to frontmatter only when column semantics are known.
- Keep empty optional cells absent rather than writing placeholders.
- If rows aggregate multiple source records, record only traceable materials and
  do not fabricate per-claim attribution.

## Arbitrary Markdown directories

- Detect reserved `index.md`/`log.md` before adding concept frontmatter.
- Do not place `okf_version` in nested indexes or concepts.
- Preserve body bytes when ownership of a rewrite is ambiguous.
- Unknown YAML fields are producer extensions and survive round trips.

## Legacy v0.1

Use [migration-v01-v02.md](migration-v01-v02.md). Import/read operations do not
perform hidden migration. `timestamp` and `# Citations` remain intentional only
in legacy consumption or explicit migration inputs.
