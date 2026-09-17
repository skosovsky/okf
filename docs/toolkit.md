---
title: OKF Toolkit
description: Version-aware CLI, Go, graph, mutation, and MCP surfaces for OKF v0.2.
permalink: /toolkit/
---

{% include nav.html %}

# Toolkit

The toolkit reads v0.2 by default, retains explicit v0.1 compatibility, and
preserves unknown future declarations for best-effort consumption.

The version axes are independent: plugin package `0.2.0`, repository/Go module
release tag `v0.2.1`, and document spec `okf_version: "0.2"`. None selects
another.
The document contract is the [pinned spec](https://github.com/skosovsky/okf/blob/main/skills/open-knowledge-format/references/spec-v02.md).

## Version resolution

Bundle commands use `--spec auto|0.1|0.2` where supported:

- `auto`: root declaration, otherwise v0.2 default with §13 fallbacks;
- `0.1` or `0.2`: explicit assertion;
- present malformed declaration: hard reserved-index error, never absence or
  default conformance;
- declaration/selector mismatch: failure, not silent override;
- future declaration: best-effort with declared version retained.

## Validation

```sh
okf validate --path <bundle> --spec auto
okf validate --path <bundle> --spec auto --strict --as-of 2026-07-29
okf validate --path <bundle> --check-links --check-orphans
```

Base errors are limited to concept parsing/`type` and reserved-file structure.
Strict mode validates present v0.2 families as guidance. Staleness uses the
explicit `--as-of` date for deterministic runs.

Missing optional fields, unknown types/keys/runtimes, broken links, and missing
indexes do not become base errors.

## Inspect and parse

```sh
okf info <bundle> --spec auto --as-of 2026-07-29
okf parse <concept.md>
```

Version-aware projections keep these values separate:

- declared/effective version and compatibility;
- generated time and legacy-derived marker;
- normalized verification events and derived trust;
- raw/effective lifecycle status;
- staleness at the chosen date;
- sources/attributions;
- inert Attested Computation summary.

Bare and one-item-list verification produce the same typed semantics without
rewriting the original YAML shape. Trust is derived from `verified`, never
stored as a score.

## Format and index

```sh
okf fmt <concept.md>
okf fmt <concept.md> -w
okf index <bundle> --spec auto
```

These commands do not migrate provenance, stamp actors/times, add verification,
or bump `okf_version`. Unknown content remains lossless within supported
parser-backed mutations.

## Graph

```sh
okf graph <bundle>
okf graph <bundle> --format dot
okf graph <bundle> --format mermaid
okf graph <bundle> --format json-ld
okf graph <bundle> --format ntriples
okf graph <bundle> --profile skosovsky/okf-v0.2 --extension-relations include
okf graph <bundle> --profile legacy-v0.1
```

The current v0.2 projection and legacy graph profile are explicit compatibility
choices. Graph profile version is separate from declared/effective OKF version.

YAML `relations` is a `skosovsky/okf` extension/tooling policy. It is not an
upstream v0.2 family. Markdown links remain standard OKF edges.

The extension relation-reference grammar is
`<escaped-concept-id>[#<fragment>]`. Only a `#` that belongs to the concept ID
is escaped, as the logical two-byte spelling `\#`; the first unescaped `#` is
the fragment delimiter. For example, `source#part` means concept `source`,
fragment `part`, while `source\#part` means the root concept whose ID is
`source#part`. Backslashes after the delimiter are fragment bytes and are not
escape syntax. Stray or non-canonical concept escapes such as `source\part`,
`source\`, and `source\\#part` are rejected. Existing references without an
escaped concept hash retain byte-identical strings.

YAML can carry the logical spelling as `'source\#part'`. JSON transport escapes
the backslash and therefore carries it as `"source\\#part"`. This is an
additive canonical string encoding: graph/MCP schema shapes do not change, and
store receipt format v1 continues to expose relation references as `[]string`.
Escaped-hash support is a canonicalization bugfix, not a format migration:
persisted v1 reference arrays retain their lexical canonical wire ordering.

## Migration

```sh
okf migrate <bundle> --to 0.2
okf migrate <bundle> --from auto --to 0.2 --actor human:reviewer
```

Default is read-only preview. Safe conversion requires:

- explicit producer actor for `timestamp` → `generated.at`;
- explicit legacy retention/conflict policies.

Unresolved legacy Citations remain blockers until an explicit legacy
citation → stable source ID mapping is supplied. CLI accepts the bounded JSON
file `--citation-mappings <json-file>`; its top-level value is the exact closed
array
`[{path,entries:[{legacy_number?,legacy_entry?,source_id,title?,resource?}]}]`,
and MCP uses the same field value. Bundle-relative Markdown paths cover root
`index.md`, logs, and nested files. Every entry requires a nonzero
`legacy_number`, an exact nonblank `legacy_entry`, or both; both selectors
AND-match one actual entry. Implementations canonical-sort by path, number, and
exact raw entry. Duplicate nonzero numbers always fail. The same raw selector
is allowed only in distinct full number+entry pairs; entry-only overlaps any
reuse of that raw text. Implementations also reject inconsistent metadata for
reused source IDs and unknown fields. `legacy_entry` is valid UTF-8, 1..4096
bytes, `TrimSpace`-nonblank, and NUL-free. TAB/LF/CR are allowed; other C0
controls and DEL are rejected. Exact bytes bind authorization/digest, so LF and
CRLF are distinct and never normalized. Existing `sources` plus legacy
Citations always blocks migration with `reconcile_sources_and_citations`; no
merge is attempted. A writable plan must be all-or-nothing, validate target
v0.2 first, and publish the root `index.md`
physical write/rename last. CLI `--actor` supplies document `generated.by`; the
transaction principal is separate and internal to the adapter. Dry preview may
omit `--actor` and receive a manual action; `--write` requires it before a
generated change can apply. A version-only type-only migration neither requires
actor/time nor creates `generated`.
An explicit citation, generated-at, or computation path must name an existing
bundle document. A missing path blocks with `migration_document_missing`,
returns no manual actions, and cannot be created by mappings; no new action
code is introduced.

For MCP, source resolution is computed exactly once before Preview, and Preview
and Apply use the same complete frozen `expected_source`. It carries
`requested_selector`, declaration state (`declaration_present`,
`declaration_valid`, `declaration_raw`, `declared_version`), resolved/provenance
and transition fields, and the ordered legacy candidates and blockers. Apply
rejects changed resolution evidence. A `v0.1-to-v0.2` preview returns
content-free proof `format_version: 2`, a required non-empty
`resolution_digest`, and a non-empty plan digest. Apply requires that proof,
`expected_plan_digest`, and the same frozen `expected_source`; no earlier proof
format is accepted. The live `target-noop` branch requires the frozen `expected_source`,
is proofless, omits the plan digest, and still validates the supplied migration
target state. Blocked preview cannot be applied. There is no separate migration
`expected_revision`; when proof exists, `proof.base_revision` is authoritative.
The proof freezes the request and source resolution, revisions, paths, refs,
and changed-file/ref summaries with non-null arrays, never file
bytes/frontmatter/body.
Every MCP preview, noop, rejected, blocked, invalid, or cancelled non-publication
path leaves the entire filesystem tree path-for-path and byte-for-byte identical
and creates no `.okf` or staging artifacts. Only an authorized actual commit
may publish filesystem changes; an identical successful replay returns the
recorded result without a second publication.

Parser-backed Markdown ownership has a public 16 MiB byte boundary for both a
complete document and an already separated body. `bundle.MaxMarkdownDocumentBytes`
and `bundle.MaxMarkdownBodyBytes` expose the limit. Inputs are classified in
this order: caller cancellation, invalid UTF-8, `bundle.ErrMarkdownResourceLimit`,
then parser/ownership errors. `bundle.MarkdownResourceLimitError` carries the
resource kind, limit, and a saturated observed size; Context APIs publish exact
zero results on every rejection.
Migration input validation runs before source resolution. Any supplied
structurally/domain-invalid individual actor, timestamp, citation, generated-at,
computation, or asset field is rejected even for `target-noop` or a rootless
bundle and follows the same zero-write guarantee.
§13 fallback is presence-only and version-source agnostic: default, declared,
or explicit v0.1/v0.2 and future resolution use the same predicate.
`GeneratedPresent`/`SourcesPresent` suppress fallback even when malformed;
`TimestampAllowed`/`CitationsAllowed` record replacement absence, while
`TimestampActive`/`CitationsActive` also require the actual legacy form.
`CitationsActive` requires a parser-owned exact `# Citations` heading; a numeric
marker alone is inactive. In particular, prose `[1]` without that exact active
section is not legacy evidence. An undeclared bundle containing only such prose
resolves as native v0.2 `target-noop`, not as an inferred v0.1 migration.
The same document-aware input preflight runs before transition planning,
including before `target-noop`. A successful live `target-noop` is proofless,
opens no store, writes nothing, and creates no `.okf`.

The CLI uses the same planner with a different durable adapter contract. Its
dry-run builds the proof without opening the store. With `--write`, a live
`target-noop` commits an empty CAS and creates or replays a durable receipt in
`.okf`, while all revision-visible bundle files remain path-and-byte identical.

On `v0.1-to-v0.2`, a number-only mapping selects a numbered entry inside the
active legacy section. On `target-noop`, the same explicit mapping is a replay
assertion: it must already match the keyed footnote and, for a concept, the
structured source ID and supplied metadata. It never converts bare `[1]`
prose. For every mapping with a nonzero `legacy_number`, the selected
parser-owned `[n]` must be gone and a reference to normalized keyed
`[^SourceID]` must exist. Entry-only mappings have no claim-reference
requirement. Leftover selected, missing, or wrong references block with
`migration_replay_mismatch` at the exact parser-owned span and no invented
manual action. Inline/fenced code and raw HTML are opaque: their marker-like
bytes neither satisfy nor fail claim replay. A normalized
existing footnote-label collision blocks with
`normalized_footnote_label_collision` plus
`disambiguate_citation_entry`; an active legacy section on the v0.2 target also
blocks replay. All of these preflight outcomes are proofless and zero-write.
An unrenderable individual migration field is `invalid_request`. A normalized
per-document SourceID collision is instead blocked with
`normalized_footnote_label_collision` and `disambiguate_citation_entry`,
including on `target-noop`; an existing-document collision reports the same
exact span. Neither is published and both are zero-write.
A proof-bound `v0.1-to-v0.2` apply may return transition-noop only after
authenticating and rebuilding the exact proof and plan digest; it returns noop
before store open and creates no `.okf`. The live MCP `target-noop` is
proofless; the CLI behavior is described above. All variants leave
revision-visible bundle files path-and-byte identical.
See
[Migration]({{ '/migration/' | relative_url }}).

## Go read model

The `bundle` package provides permissive typed views for v0.2 fields while
keeping `Get`, YAML-node access, and caller-owned structs authoritative for
unknown data. Accessors return defensive copies.

Non-Markdown computation/source assets are captured as inert revision-visible
bundle files. Reading an asset does not execute or fetch it.

## Mutation and durability

Semantic operations are narrow desired-state operations. They preview staged
output, validate the final bundle, preserve untouched presentation, and commit
through CAS/journal durability.

Existing store ChangeSet/Receipt formats and transaction actors are separate
from OKF document actors. `store.CommitReceipt` is transaction durability
evidence emitted by a successful store commit. `executor.receipt` is a runtime
artifact of an Attested Computation; it is not stored in or accepted as a
canonical transaction receipt.

Legacy wire/profile versions remain stable unless their own contract changes;
OKF v0.2 alone is not a reason to bump them.

## MCP

Compatibility tools retain their text fallbacks:

- `list_concepts`
- `read_concept`
- `validate_bundle`
- `get_semantic_graph`
- `write_concept`

The server exposes nine tools. Its four safe v0.2 tools are preview/apply pairs:

- `preview_concept_patch` / `apply_concept_patch`
- `preview_v02_migration` / `apply_v02_migration`

Structured outputs are schema-validated. Patch apply requires
`expected_revision` plus its preview plan digest. Migration apply is
transition-discriminated: `v0.1-to-v0.2` requires preview proof
`format_version: 2` with non-empty `resolution_digest`, non-empty
`expected_plan_digest`, and the same full `expected_source`; no earlier proof
format is accepted. Live `target-noop` requires only `expected_source`, is proofless, and
validates the target document. When present, `proof.base_revision` is
authoritative. Raw frontmatter/body remain available so unknown YAML is not
forced through a lossy closed model. Identical migration apply is replay-safe.
Across MCP structured JSON, present `usage_count` is a canonical decimal string
matching `^(0|[1-9][0-9]*)$`, or `null` for nullable outputs. Patch input
rejects semantic uint64 overflow. This avoids `float64` precision loss in
decoded MCP Arguments, including `MaxUint64`; legacy text fallbacks are
unchanged.
The `set_usage_window` selector is a closed union: shared (neither `source_id`
nor `source`), identified (`source_id` is non-empty), or exact anonymous
(`source` is present and its `id` is absent). Empty `source_id`, mixed or
unknown selector forms, and ambiguous exact anonymous matches are rejected.
The `remove_source` selector is a closed union of identified (`source_id` is
non-empty) or exact anonymous (`source` is present and its `id` is absent); it
has no shared form. Empty `source_id`, mixed or unknown selector forms, and
ambiguous exact anonymous matches are rejected.

## Security boundary

- Bundle paths use containment/no-follow rules.
- Resource fields do not authorize filesystem, network, shell, or secret use.
- Actor metadata is not authentication.
- MCP actor-bearing fields have an independent 256-byte transport/resource cap.
  257+ bytes returns `resource_limit`, not invalid actor; within the cap shared
  `ValidActor` owns semantics. This does not cap bundle/store actor grammar.
- Executor/attester/computation content is inert without a separate trusted
  runtime and authorization.
- Per the pinned spec, an agent MAY provide only values for declared parameters
  and MUST NOT author or edit the sanctioned computation.
- The toolkit does not define or invent parameter binding, receipt/verdict
  protocol, attester ABI, sandbox, or cache.
