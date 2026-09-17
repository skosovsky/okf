# OKF v0.2 examples

Canonical examples live in the shared `fixtures/v02` corpus. Do not duplicate
their YAML here: fixture-backed files are parsed by bundle/validator/CLI/MCP
tests and therefore cannot silently drift from documentation.

Use `fixtures/v02/corpus.yaml` to locate the exact paths for:

- minimal type-only concept;
- Appendix A bundle;
- human-authored concept;
- multiple sources and keyed footnotes;
- bare and list `verified`;
- draft, stable, deprecated, fresh, and stale boundary cases;
- inline and file-backed Attested Computation;
- narrative concept linking sanctioned computations;
- declared/undeclared v0.1 compatibility;
- simultaneous legacy/v0.2 provenance;
- unknown future version;
- forward-compatible unknown extension fields;
- adversarial lifecycle self-promotion, optional shapes, and inert executor
  content.

All repository-authored synthetic materials use reserved domains such as
`https://example.invalid/` and say that they are synthetic. Upstream Appendix A
content remains upstream content and is not relabeled.

## Minimal authoring pattern

For a new v0.2 concept, begin with:

```markdown
---
type: <descriptive type>
---

<known content>
```

Add optional fields only when warranted:

- `generated` only with known actor;
- `sources` only from real materials;
- `verified` only after real checking;
- lifecycle only from an explicit lifecycle decision;
- computation contract only for a standalone sanctioned computation.

## Human-authored is not automatically verified

A human author uses the `human:` actor convention in `generated.by`. That says
who wrote the current content. It does not add `verified` and does not yield
`human-reviewed` unless a human actually confirms the content.

## Multiple sources

Each attributable source gets a stable `sources[].id`. Body claims use keyed
footnotes with the same label. Reordering `sources` must not change mapping.

## Bare/list verification

A bare mapping and a one-item sequence have identical consumption semantics.
Keep the original YAML shape on read; normalize only the typed projection.

## Computation examples are inert

Inline and file-backed fixtures describe contract shapes. They do not promise
runtime discovery, binding, executor/attester packaging, receipt/verdict
formats, sandboxing, caching, or execution by this toolkit.

## Intentional compatibility examples

Legacy `timestamp` and `# Citations` occur only in compatibility/migration
fixtures. In mixed provenance, v0.2 is the effective read because fallback
applies only when its replacement is absent; raw legacy data remains lossless.
Migration blocks coexistence of `sources` and legacy Citations instead of
merging them and reports `reconcile_sources_and_citations`. Future versions are
read best-effort without rewriting their declaration.

## Citation mapping selector examples

CLI and MCP share this exact closed shape:

```json
[
  {
    "path": "index.md",
    "entries": [
      {"legacy_number": 1, "source_id": "numbered"}
    ]
  },
  {
    "path": "log.md",
    "entries": [
      {
        "legacy_entry": "https://example.invalid/raw",
        "source_id": "raw-url"
      }
    ]
  },
  {
    "path": "nested/report.md",
    "entries": [
      {
        "legacy_number": 2,
        "legacy_entry": "[Report](https://example.invalid/report)",
        "source_id": "report",
        "title": "Report"
      },
      {
        "legacy_number": 3,
        "legacy_entry": "[Report](https://example.invalid/report)",
        "source_id": "report-copy",
        "title": "Report"
      }
    ]
  }
]
```

The first entry selects numbered `[1]`; the second selects an unnumbered raw
URL. The two nested entries demonstrate valid disjoint full pairs: identical
raw text is allowed because distinct nonzero numbers make both AND selectors
non-overlapping. Duplicate nonzero numbers always fail. An entry-only selector
overlaps any reuse of the same raw text, including another entry-only selector,
and is rejected as ambiguous. Exact raw comparison performs no trimming or
newline normalization. `legacy_entry` must be valid UTF-8, 1..4096 bytes,
`TrimSpace`-nonblank, and NUL-free. TAB/LF/CR are allowed; other C0 controls and
DEL are rejected. Exact bytes bind authorization/digest, so LF and CRLF are
distinct inputs. Canonical order is path, then `legacy_number`, then exact
`legacy_entry`.
Duplicate number/raw selector ambiguity reports `disambiguate_citation_entry`;
it must not be mislabeled as `disambiguate_citation_destination`, which is
reserved for parser link-destination ownership ambiguity.
An explicit citation, generated-at, or computation path must name an existing
bundle document. A missing path blocks with `migration_document_missing`,
returns no manual actions, and cannot be created by mappings; no new action
code is introduced.
Migration input validation runs before source resolution. Any supplied
structurally/domain-invalid individual actor, timestamp, citation, generated-at,
computation, or asset field is rejected even for `target-noop` or a rootless
bundle and follows the same zero-write guarantee.
Source resolution is computed exactly once before Preview, and Preview and
Apply use the same complete frozen `expected_source`. It includes requested
selector, `declaration_present`, `declaration_valid`, `declaration_raw`,
`declared_version`, resolved/provenance/transition fields, and ordered
candidates/blockers. A proof-bound transition uses only
`format_version: 2` with required non-empty `resolution_digest`; a live
`target-noop` is proofless but still validates the target document.
§13 fallback is presence-only and version-source agnostic: default, declared,
or explicit v0.1/v0.2 and future resolution use the same predicate.
`GeneratedPresent`/`SourcesPresent` suppress fallback even when malformed;
`TimestampAllowed`/`CitationsAllowed` record replacement absence, while
`TimestampActive`/`CitationsActive` also require the actual legacy form.
`CitationsActive` requires a parser-owned exact `# Citations` heading; a numeric
marker alone is inactive. Marker-only `[1]` prose in an undeclared bundle is
native v0.2 `target-noop`, not inferred legacy evidence. Common document-aware
preflight still validates explicit inputs first. MCP remains proofless,
store-free, and zero-write; CLI dry-run builds a proof without store access,
while CLI `--write` commits an empty CAS and durable `.okf` receipt without
changing revision-visible files. A number-only mapping on that target is a replay
assertion, not a rewrite: the keyed footnote and concept source metadata must
already match. Otherwise `migration_replay_mismatch` blocks with no invented
manual action; normalized label collision uses
`normalized_footnote_label_collision` and
`disambiguate_citation_entry`.
For every mapping with nonzero `legacy_number`, the selected parser-owned `[n]`
must be absent and normalized keyed `[^SourceID]` must be referenced. Entry-only
mappings have no claim-reference requirement. Leftover selected, missing, or
wrong references return `migration_replay_mismatch` at the exact parser-owned
span with zero writes and no proof/plan authorization. Marker-like bytes inside
inline/fenced code or raw HTML are opaque and ignored.
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

## `skosovsky/okf` RelationRef wire examples

These strings describe the repository YAML-relations extension, not upstream
OKF v0.2:

| Wire string | Structural identity |
| --- | --- |
| `source` | root concept `source` |
| `source#part` | concept `source`, fragment `part` |
| `source\#part` | root concept whose ID is `source#part` |
| `source\#part#leaf\value` | concept `source#part`, fragment `leaf\value` |

Only concept-ID `#` bytes use `\#`; the first unescaped `#` is the delimiter,
and fragment backslashes are unchanged. Stray/non-canonical concept escapes are
invalid. Ordinary strings round-trip byte-identically. JSON transports the
logical `source\#part` as `"source\\#part"` without changing graph/MCP schema
shapes or store receipt v1 `[]string`.

## MCP `usage_count` wire example

The domain value remains `uint64`, but every MCP structured JSON surface uses a
canonical decimal string, or `null` when the output field is nullable:

```json
{"usage_count":"18446744073709551615"}
```

The canonical pattern is `^(0|[1-9][0-9]*)$`; overflow is rejected on input.
The string wire form prevents `float64` precision loss for `MaxUint64`.
Legacy text fallbacks retain their existing representation.

## MCP `set_usage_window` selector examples

The selector is a closed union. Omit both selector fields for the shared
window:

```json
{
  "kind": "set_usage_window",
  "concept_id": "report",
  "usage_window": {"from": "2026-07-01", "to": "2026-07-31"}
}
```

Use one non-empty `source_id` for an identified source:

```json
{
  "kind": "set_usage_window",
  "concept_id": "report",
  "source_id": "policy",
  "usage_window": {"from": "2026-07-01", "to": "2026-07-31"}
}
```

Use one exact `source` object without `id` for an anonymous source:

```json
{
  "kind": "set_usage_window",
  "concept_id": "report",
  "source": {"resource": "policy.md", "title": "Policy"},
  "usage_window": {"from": "2026-07-01", "to": "2026-07-31"}
}
```

Empty `source_id`, both selector fields together, unknown selector/source
fields, an `id` inside the exact anonymous `source`, and ambiguous exact
anonymous matches are rejected. Use `usage_window: null` with the same selector
forms to remove the selected window.

## MCP `remove_source` selector examples

Remove an identified source with one non-empty `source_id`:

```json
{
  "kind": "remove_source",
  "concept_id": "report",
  "source_id": "policy"
}
```

Remove an anonymous source with one exact `source` object without `id`:

```json
{
  "kind": "remove_source",
  "concept_id": "report",
  "source": {"resource": "policy.md", "title": "Policy"}
}
```

`remove_source` has no shared selector form. Empty `source_id`, both selector
fields together, unknown selector/source fields, an `id` inside the exact
anonymous `source`, and ambiguous exact anonymous matches are rejected.
