---
layout: default
title: "OKF 0.1 → 0.2 migration — reading edition"
lang: en
permalink: /readings/skills/open-knowledge-format/references/migration-v01-v02/
document_id: skills-open-knowledge-format-references-migration-v01-v02
---
{% include nav.html %}

This complete reading edition follows the [original](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/skills/open-knowledge-format/references/migration-v01-v02.md) at revision `61e75e9aa9a8719dfb480bf1f5226553b3a21d70`. It does not revise the normative original or the installed skill package. For installation, use the [guide]({{ '/skill/' | relative_url }}).

# Migration OKF 0.1 → 0.2
{: #section-1 }

This guide describes the toolkit's safe migration policy. Normative changes are listed in §13 of the [pinned OKF v0.2 specification]({{ '/readings/skills/open-knowledge-format/references/spec-v02/' | relative_url }}). Toolkit policy does not extend upstream conformance.

## Contents
{: #section-2 }

- [What changes](#section-3)
- [Transaction contract](#section-4)
- [`timestamp` → `generated`](#section-5)
- [`# Citations` → `sources`](#section-6)
- [`skosovsky/okf` RelationRef wire extension](#section-7)
- [Attested Computation boundary](#section-8)
- [Prohibited inferences](#section-9)
- [Legacy consumption after migration tooling](#section-10)
- [Review checklist](#section-11)

## What changes
{: #section-3 }

Two breaking changes:

- `timestamp` is superseded by `generated.at`;
- body `# Citations` is superseded by frontmatter `sources` and keyed footnotes.

Other v0.2 field families are optional. Migration need not add `verified`, `status`, `stale_after`, credibility signals, or Attested Computation.

## Transaction contract
{: #section-4 }

1. Produce a read-only preview.
2. Freeze the source identity. For `v0.1-to-v0.2`, also freeze a deterministic non-empty plan digest and a content-free proof v2 with required `resolution_digest`.
3. Obtain explicit inputs only for the semantic transformations that will actually run.
4. Preserve unknown YAML, Markdown, and assets losslessly.
5. Validate the entire staged bundle as target v0.2.
6. Publish the physical write/rename of root `index.md` last. Changing `okf_version` is the transaction's final visible publication.
7. Apply as one transaction. Patch apply uses `expected_revision` and the preview digest. Migration `v0.1-to-v0.2` uses proof v2, a non-empty `expected_plan_digest`, and the same complete `expected_source`. A live `target-noop` uses only `expected_source`, remains proofless, and validates the target document. Earlier proof formats are not accepted.

Partial migration is prohibited. Any ambiguity leaves the bundle unchanged and returns a blocker/manual action.

Every non-publication branch—preview, noop, rejected, blocked, invalid, or cancelled—leaves the entire filesystem tree identical path-for-path and byte-for-byte and creates no `.okf` or staging artifacts. Only an authorized actual commit may publish filesystem changes. An identical successful replay returns the recorded result without a second publication.

Migration input validation runs before source resolution. Any supplied individual actor, timestamp, citation, generated-at, computation, or asset field that is structurally or domain-invalid is rejected even for `target-noop` or a rootless bundle, with the same zero-write guarantee.

Source resolution is computed exactly once before Preview. Preview and Apply use the same complete frozen `expected_source`: `requested_selector`, declaration state (`declaration_present`, `declaration_valid`, `declaration_raw`, `declared_version`), resolved/provenance/transition fields, and ordered candidates/blockers. Apply rejects changed resolution or evidence before opening the store.

§13 fallback depends only on replacement presence, independently of the version source. Default, declared, or explicit v0.1/v0.2 and future resolution use the same predicate. `GeneratedPresent`/`SourcesPresent` suppress fallback even for malformed values. `TimestampAllowed`/`CitationsAllowed` record replacement absence; `TimestampActive`/`CitationsActive` also require the actual legacy form. `CitationsActive` requires the parser-owned exact heading `# Citations`; a numeric marker alone is insufficient. Prose `[1]` without an exact active section is not legacy evidence. An undeclared bundle with only that marker resolves as native v0.2 `target-noop`, not an inferred v0.1 transition.

Common document-aware input preflight runs before transition planning, including `target-noop`. A successful live MCP target-noop is proofless, opens no store, writes nothing, and creates no `.okf`. CLI dry-run builds a proof without opening the store; CLI `--write` performs an empty CAS and saves or replays a durable receipt in `.okf` without changing revision-visible bundle files.

For `v0.1-to-v0.2`, a number-only citation mapping selects a numbered entry inside the active legacy section. For `target-noop`, it is a replay assertion of already-migrated state: the matching keyed footnote and, for a concept, structured source ID and supplied metadata must exist. It does not rewrite bare prose `[1]`. Missing or different migrated content blocks with `migration_replay_mismatch`, without an invented manual action. A normalized collision with an existing footnote label blocks with `normalized_footnote_label_collision` and `disambiguate_citation_entry`. An active legacy section in a v0.2 target is also a replay mismatch. All these branches are proofless and zero-write.

For every mapping with nonzero `legacy_number`, the selected parser-owned `[n]` must disappear and a reference to normalized keyed `[^SourceID]` must exist. Entry-only mappings do not require a claim reference. A leftover selected, missing, or wrong reference blocks with `migration_replay_mismatch` at the exact parser-owned span, without writes, proof, or plan authorization. Marker-like bytes in inline/fenced code and raw HTML are opaque: they are not evidence and create no replay mismatch.

An unrenderable individual migration field returns `invalid_request`. A normalized per-document SourceID collision instead blocks with `normalized_footnote_label_collision` and `disambiguate_citation_entry`, including `target-noop`. A collision in an existing document returns the same exact span. Neither branch publishes; both are zero-write.

A proof-bound `v0.1-to-v0.2` apply may return transition-noop only after authenticating and rebuilding the exact proof and plan digest. It returns noop before opening the store and creates no `.okf`. For a live `target-noop`, MCP remains proofless and opens no store, CLI dry-run builds a proof without opening the store, and CLI `--write` performs an empty CAS with a durable `.okf` receipt. Each surface leaves revision-visible bundle files identical in paths and bytes.

A migration proof contains only frozen metadata: `format_version: 2`, required non-empty `resolution_digest`, request, complete source resolution, base/result revisions, read paths, write path/digest pairs, deletes, renames, canonical affected/reverse references, and changed-file/reference summaries. Arrays are non-null; file bytes, frontmatter, and body are absent. Earlier proof formats are not accepted. There is no separate migration `expected_revision`: when a proof exists, `proof.base_revision` is authoritative. Noop/blocked previews return no proof; a blocked preview cannot be applied.

## `timestamp` → `generated`
{: #section-5 }

`timestamp` contains a time but no producer identity, so migration requires explicit `generated.by`.

In CLI, `okf migrate --actor` supplies the explicit document producer for `generated.by`. It is not `store.ChangeSet.Actor`: the CLI adapter's transaction principal is separate and internal. A read-only preview may omit `--actor` and return a manual action; `--write` requires an actor before creating `generated`. For MCP actor-bearing fields, a separate 256-byte transport/resource cap is checked before shared `ValidActor`. At 257 or more bytes, the result is `resource_limit`, not an invalid-actor error. The cap is not actor grammar and does not restrict the bundle/store domain.

Safe case:

```yaml
# before
timestamp: 2026-05-28T22:53:05Z

# after, actor supplied by caller
generated:
  by: "process:catalog-export"
  at: 2026-05-28T22:53:05Z
```

Rules:

- Do not use the transaction actor as an implicit producer.
- Do not use the Git author, file owner, or current user as producer.
- If the actor is unknown, return an unresolved/manual action.
- If `generated` is absent and `timestamp` exists, copy the time only with an actor.
- If `generated` and `timestamp` are both absent, do not create `generated`.
- Version-only migration of a type-only concept changes only the root declaration and requires no actor/time.
- Never use `now`.
- Equivalent `generated` means noop.
- Conflicting `timestamp`/`generated.at` is a blocker without an explicit conflict policy.
- Remove the legacy key only according to the chosen policy after a successful copy.
- Comments, duplicate keys, aliases/merges, or ambiguous ownership cause rejection rather than a guessed transformation.

## `# Citations` → `sources`
{: #section-6 }

Migration does no semantic matching. The caller provides an explicit mapping from each legacy entry to a stable source ID and structured resource.

CLI accepts only a bounded JSON file `--citation-mappings <json-file>` with this exact closed top-level array: `[{path,entries:[{legacy_number?,legacy_entry?,source_id,title?,resource?}]}]`. The MCP field contains the same array. `path` is a bundle-relative Markdown path, including root `index.md`, logs, and nested files. Each entry requires one or both selectors: nonzero `legacy_number` for `[1]`, or exact nonblank `legacy_entry` for an unnumbered bullet/raw URL. If both are supplied, both must match one actual entry.

`legacy_entry` must be valid UTF-8, 1..4096 bytes, nonblank after `TrimSpace`, and NUL-free. TAB/LF/CR are allowed; other C0 controls and DEL are prohibited. Exact bytes are not trimmed or case/URL/newline-normalized and bind authorization/digest, so LF and CRLF differ. An entry-only selector matching duplicate identical raw entries is ambiguous and rejected. Duplicate nonzero `legacy_number` is always prohibited. One `legacy_entry` is allowed only in distinct full number-and-entry pairs. An entry-only selector overlaps any reuse of the same raw text, and two entry-only selectors are also prohibited. Distinct full pairs with different numbers are allowed.

Canonical sort is path, then `legacy_number`, then exact `legacy_entry`. Overlapping selectors, unknown fields, and inconsistent metadata for a reused source ID are prohibited.

```markdown
# before
The claim is documented.[1]

# Citations

[1] [Synthetic policy](https://example.invalid/policy)
```

```markdown
# after
---
type: Reference
sources:
  - id: policy
    resource: https://example.invalid/policy
    title: Synthetic policy
---

The claim is documented.[^policy]

[^policy]: Synthetic policy.
```

Rules:

- Create `sources` only from actual legacy entries and explicit mappings.
- Do not invent title, author, usage, `last_modified`, or `usage_window`.
- Replace only proven numeric claim markers.
- A keyed footnote label must match `sources[].id`.
- Remove `# Citations` only after a complete successful mapping.
- Multiple sections, duplicate numbers/IDs, unresolved markers, conflicting definitions, and ambiguous Markdown ownership are blockers.
- Inline/fenced code and raw HTML remain opaque.

If `sources` is already present alongside legacy Citations, effective reading uses `sources`: fallback applies only when the replacement is absent. Migration preserves both raw forms and always blocks; it performs no merge, deduplication, or deletion. The manual action is `reconcile_sources_and_citations`; the caller must normalize the document beforehand.

Manual-action vocabulary:

- Missing/unresolved mapping or a number-and-entry mismatch → `provide_citation_mapping`.
- Duplicate number/raw selector ambiguity → `disambiguate_citation_entry`.
- Parser link-destination ownership ambiguity → `disambiguate_citation_destination`.
- Opaque/unowned extra section → `normalize_citations_section`.
- Mixed structured `sources` and legacy Citations → `reconcile_sources_and_citations`.
- Invalid UTF-8 → `repair_invalid_utf8`.
- Missing generation time → `provide_generated_at`.
- Missing producer actor → `provide_generated_by`.

An explicit citation/generated-at/computation path must name an existing bundle document. A missing path blocks with `migration_document_missing`, returns empty manual actions, and cannot be created by mappings; no new action code is introduced.

Duplicate raw selector ambiguity is not destination ambiguity.

## `skosovsky/okf` RelationRef wire extension
{: #section-7 }

This grammar is a toolkit extension, not an upstream OKF v0.2 migration rule. A relation reference has the form `<escaped-concept-id>[#<fragment>]`. Only `#` inside ConceptID uses the logical spelling `\#`; the first unescaped `#` separates the fragment. Therefore `source#part` and `source\#part` are different identities: concept plus fragment, and root concept with ID `source#part`. Backslashes after the delimiter remain literal fragment bytes. Stray/non-canonical concept escapes are rejected; ordinary references remain byte-identical.

YAML single-quoted spelling: `'source\#part'`. In JSON the same logical reference is `"source\\#part"`. Graph/MCP schema shapes remain string-based, and store receipt format v1 continues to store references as `[]string`.

## Attested Computation boundary
{: #section-8 }

Migration does not automatically turn narrative prose, SQL snippets, or shell examples into Attested Computation. That is a separate authoring decision:

- The caller selects target computation concepts.
- The caller provides the sanctioned computation.
- Inline mode owns exactly one fence under `# Computation`.
- File mode creates an asset transactionally.
- An unknown executor/attester/runtime ABI is not invented.

## Prohibited inferences
{: #section-9 }

Do not infer these from migration, a timestamp, Git history, or validation:

- `verified`;
- trust tier;
- `status`;
- `stale_after`;
- source credibility;
- receipt/verdict;
- successful attestation.

## Legacy consumption after migration tooling
{: #section-10 }

Before apply and for intentional legacy bundles, a v0.2 consumer:

- Uses legacy `timestamp` only when `generated` is entirely absent.
- Reads legacy `# Citations` only when `sources` is absent.
- Does not rewrite input through parse/fmt/index/read.
- Displays fallback as legacy-derived.
- Preserves declared `0.1` and unknown future versions.

## Review checklist
{: #section-11 }

- [ ] Preview changed no bytes.
- [ ] A producer actor was explicitly provided if the plan creates `generated`.
- [ ] Each citation has an explicit source mapping.
- [ ] Unknown content is preserved.
- [ ] No verification, lifecycle, or credibility was invented.
- [ ] Target v0.2 validation passed.
- [ ] The root version changes last.
- [ ] Migration apply repeats the frozen `expected_source`; `v0.1-to-v0.2` additionally supplies proof v2 with `resolution_digest` and a non-empty `expected_plan_digest`, while a live `target-noop` supplies neither proof nor digest.
- [ ] Identical apply is replay-safe: it returns the recorded result without a second publication.
