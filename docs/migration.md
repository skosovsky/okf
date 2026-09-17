---
title: Migrating OKF v0.1 to v0.2
description: Lossless, explicit, transaction-bound migration from OKF v0.1.
permalink: /migration/
---

{% include nav.html %}

# Migration v0.1 → v0.2

v0.2 changes two legacy forms:

- `timestamp` is superseded by `generated.at`;
- body `# Citations` is superseded by frontmatter `sources` and keyed
  footnotes.

Reading legacy data is not migration. Parse, format, index, graph, and normal
read operations preserve legacy bytes.
An absent declaration can resolve through the documented source rules. A
present malformed root `okf_version` is instead a hard reserved-index error,
not an absent/default or conformant migration source. Unsupported canonical
future declarations stay declared and are blocked from v0.1 migration.

## Safe workflow

1. Preview without writes.
2. Supply an actor, time, or citation mappings only for transformations that
   actually need them.
3. Resolve conflicts/manual actions.
4. Preserve unknown YAML, Markdown, and assets.
5. Validate the entire staged bundle as v0.2.
6. Stage and publish the root `index.md` physical write/rename last; its
   `okf_version` change is the transaction's final visible publication.
7. Apply atomically against the preview-frozen `expected_source`; for a
   `v0.1-to-v0.2` transition also echo its proof v2 and non-empty
   `expected_plan_digest`.

Every preview, noop, rejected, blocked, invalid, or cancelled non-publication
path leaves the entire filesystem tree path-for-path and byte-for-byte identical
and creates no `.okf` or staging artifacts. Only an authorized actual commit
may publish filesystem changes; an identical successful replay returns the
recorded result without a second publication.
Migration input validation runs before source resolution. Any supplied
structurally/domain-invalid individual actor, timestamp, citation, generated-at,
computation, or asset field is rejected even for `target-noop` or a rootless
bundle and follows the same zero-write guarantee.
Source resolution is computed exactly once before Preview, and the same complete
frozen `expected_source` is used by Preview and Apply. It carries
`requested_selector`, declaration state (`declaration_present`,
`declaration_valid`, `declaration_raw`, `declared_version`), resolved/provenance
and transition fields, and the ordered legacy candidates and blockers. Apply
rejects a changed resolution or changed resolution evidence.
§13 fallback is presence-only and version-source agnostic: default, declared,
or explicit v0.1/v0.2 and future resolution use the same predicate.
`GeneratedPresent`/`SourcesPresent` suppress fallback even when malformed;
`TimestampAllowed`/`CitationsAllowed` record replacement absence, while
`TimestampActive`/`CitationsActive` also require the actual legacy form.
`CitationsActive` requires a parser-owned exact `# Citations` heading; a numeric
marker alone is inactive.
For every citation mapping with a nonzero `legacy_number`, replay requires the
selected parser-owned `[n]` marker to be gone and a reference to the normalized
keyed `[^SourceID]` to exist. An entry-only mapping has no claim-reference
requirement. A leftover selected marker, a missing keyed reference, or a wrong
keyed reference blocks with `migration_replay_mismatch` at the exact
parser-owned span. Inline/fenced code and raw HTML are opaque: their marker-like
bytes are neither claim evidence nor replay failures. Every mismatch is
zero-write and returns no proof or plan authorization.
An unrenderable individual migration field is `invalid_request`. A normalized
per-document SourceID collision is instead blocked with
`normalized_footnote_label_collision` and `disambiguate_citation_entry`,
including on `target-noop`; an existing-document collision reports the same
exact span. Neither is published and both are zero-write.
A proof-bound `v0.1-to-v0.2` apply may return transition-noop only after
authenticating and rebuilding the exact proof and plan digest; it returns noop
before store open and creates no `.okf`. For live `target-noop`, MCP is
proofless and opens no store, CLI dry-run builds a proof without opening the
store, and CLI `--write` commits an empty CAS with a durable `.okf` receipt.
Each surface leaves revision-visible bundle files path-and-byte identical.

An identical successful apply is replay-safe: the adapter returns the recorded
result rather than publishing the migration again.

Preview resolves `from: auto|0.1` into a frozen source identity. A
`v0.1-to-v0.2` MCP preview with a non-empty plan digest also returns a
content-free proof with `format_version: 2` and a required non-empty
`resolution_digest`; its apply branch requires that proof, the non-empty
`expected_plan_digest`, and the same frozen `expected_source`. No earlier proof
format is accepted. A live `target-noop` branch requires the frozen `expected_source`,
omits both proof and plan digest, and still validates the supplied migration
target state. Blocked preview cannot be applied. Migration has no separate
`expected_revision`: when proof exists, `proof.base_revision` is authoritative.
The proof freezes the request and full source resolution, revision digests,
read paths, write path+digest pairs, deletes, renames, affected and
reverse-impact refs, and changed file/ref summaries. Its arrays are non-null
and it contains no file bytes, frontmatter, or body. Noop/blocked preview omits
the proof.

## Timestamp rule

A legacy timestamp contains no actor. It can become `generated.at` only when
the caller supplies `generated.by`. Transaction actor, git author, file owner,
or current user is not an implicit producer.

For `okf migrate`, `--actor` is that explicit document producer and is written
to `generated.by`. It is not `store.ChangeSet.Actor`; the CLI adapter uses a
separate internal transaction principal. Dry preview may omit `--actor`; if the
plan needs generation metadata, preview returns a manual action. Applying that
change with `--write` requires the actor.

If a type-only legacy bundle has no timestamp to convert, migration changes
only the root version declaration. It does not require actor/time input and
does not create `generated`.

For MCP actor-bearing fields, a separate 256-byte transport/resource cap runs
before semantic validation: 257+ bytes returns `resource_limit`, not invalid
actor. Within the cap, shared `ValidActor` decides semantics. This adapter cap
is not actor grammar and does not limit the bundle/store domain.

Unknown actor is a manual action, not a guessed value. Conflicting
`timestamp`/`generated.at` values require explicit policy.

## Citation rule

Every legacy entry needs an explicit stable source ID mapping. CLI accepts it
only through the bounded JSON file `--citation-mappings <json-file>`; MCP uses
the same exact closed array
`[{path,entries:[{legacy_number?,legacy_entry?,source_id,title?,resource?}]}]`.
Each group is keyed by bundle-relative Markdown `path`, not concept ID, so root
`index.md`, log files, and nested Markdown are addressable without identity
guessing. Each mapping entry requires one or both selectors:

- nonzero `legacy_number` selects numbered `[1]`;
- exact nonblank `legacy_entry` selects raw entry text, including an unnumbered
  bullet or raw URL;
- when both are present, they AND-match the same actual entry.

`legacy_entry` is matched exactly without trimming, case folding, URL
canonicalization, or newline normalization. It must be valid UTF-8, 1..4096
bytes, nonblank under `strings.TrimSpace`, and contain no NUL. TAB (`0x09`), LF
(`0x0a`), and CR (`0x0d`) are allowed; every other C0 control and DEL (`0x7f`)
is rejected. Validation does not trim the value: exact bytes are preserved and
bind authorization/digest. LF and CRLF therefore authorize and digest as
distinct values. An entry-only selector that matches duplicate identical raw
entries is ambiguous and rejected. Duplicate nonzero `legacy_number` values
always fail. The same `legacy_entry` is allowed only when every reuse also has a
distinct nonzero number, producing disjoint full AND-selector pairs.
Entry-only plus any reuse of the same raw text overlaps and fails; two
entry-only copies fail. Distinct full pairs by number are valid.
Implementations canonically sort by path, then `legacy_number`, then exact
`legacy_entry`; reject overlapping selectors; and require consistent metadata
when a source ID is reused. Unknown fields, unsafe paths, oversized
files/arrays/values, and incomplete mappings fail closed.
Only claim markers with demonstrable ownership are rewritten to keyed
footnotes.

The CLI file has no wrapper object:

```json
[
  {
    "path": "index.md",
    "entries": [
      {
        "legacy_number": 1,
        "source_id": "policy",
        "title": "Synthetic policy",
        "resource": "https://example.invalid/policy"
      }
    ]
  },
  {
    "path": "logs/2026-07.md",
    "entries": [
      {
        "legacy_entry": "https://example.invalid/runbook",
        "source_id": "runbook"
      }
    ]
  },
  {
    "path": "nested/report.md",
    "entries": [
      {
        "legacy_number": 2,
        "legacy_entry": "[Incident](https://example.invalid/incident)",
        "source_id": "incident-report"
      },
      {
        "legacy_number": 3,
        "legacy_entry": "[Incident](https://example.invalid/incident)",
        "source_id": "incident-report-copy"
      }
    ]
  }
]
```

Migration does not invent source titles, authors, usage, last-modified dates, or
claim mappings. Ambiguous/duplicate/unresolved Markdown blocks migration.

When `sources` and legacy Citations coexist, `sources` is still the effective
read because fallback applies only when `sources` is absent. Migration always
blocks that document with `reconcile_sources_and_citations`: it does not merge,
deduplicate, or delete either form.

An explicit citation, generated-at, or computation path must name an existing
bundle document. A missing path blocks with `migration_document_missing`,
returns no manual actions, and cannot be created by mappings; no new action
code is introduced.

Stable migration manual-action taxonomy is exact:

- `provide_citation_mapping`: mapping is missing/unresolved, or
  `legacy_number` + `legacy_entry` contradict the actual entry;
- `disambiguate_citation_entry`: duplicate number/raw selector matches;
- `disambiguate_citation_destination`: parser ownership cannot select one link
  destination;
- `normalize_citations_section`: opaque or unowned extra section content;
- `reconcile_sources_and_citations`: structured `sources` coexist with legacy
  Citations;
- `repair_invalid_utf8`: invalid UTF-8;
- `provide_generated_at`: generation time is required but absent;
- `provide_generated_by`: producer actor is required but absent.

Duplicate raw selector ambiguity is not destination ambiguity.

## What migration never infers

- `verified`, verification, or trust;
- lifecycle status or stale date;
- credibility score/signals;
- receipt/verdict or successful attestation;
- an Attested Computation from narrative prose.

See the full
[skill migration policy](https://github.com/skosovsky/okf/blob/main/skills/open-knowledge-format/references/migration-v01-v02.md)
and the [pinned §13 contract](https://github.com/skosovsky/okf/blob/main/skills/open-knowledge-format/references/spec-v02.md).
