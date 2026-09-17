# okf

`okf` is a Go toolkit, CLI, MCP server, and agent skill for
[Open Knowledge Format (OKF) v0.2](skills/open-knowledge-format/references/spec-v02.md):
portable knowledge bundles made of Markdown concepts with YAML frontmatter.

This repository supports three OKF version modes:

- `0.2`: current default read/write contract;
- `0.1`: intentional legacy read/validate/migration source;
- unknown future declarations: best-effort, lossless consumption.

The version axes are independent:

- plugin package: `0.2.0`;
- repository/Go module release tag: `v0.2.1`;
- OKF document spec: `okf_version: "0.2"`.

Matching numeric components do not make one axis select another.

Russian documentation: [README.ru.md](README.ru.md).

## Surfaces

1. `okf`: version-aware validation, inspection, formatting, indexing, graph
   projection, and explicit migration.
2. Go packages: `bundle`, `validator`, `graph`, `store`, `store/fs`, and
   `mutation`.
3. `okf-mcp`: schema-first stdio tools with legacy text fallbacks.
4. `open-knowledge-format`: an agent skill pinned to the upstream v0.2 spec.

## Install

```sh
go install github.com/skosovsky/okf/cmd/okf@latest
go install github.com/skosovsky/okf/cmd/okf-mcp@latest
```

Check the binary separately from the supported OKF specs:

```sh
okf version
okf version --json
```

## Minimal OKF v0.2 bundle

`index.md`:

```markdown
---
okf_version: "0.2"
---

# Concepts

* [Minimal](minimal.md) - Minimal conformant v0.2 concept.
```

`minimal.md`:

```markdown
---
type: Unknown Producer Type
---

Minimal conformant v0.2 concept.
```

`type` is the only always-required concept field. Optional provenance, trust,
lifecycle, and computation families are not base conformance requirements.

## Authoring rules

- Write `okf_version` only in the root `index.md`.
- Add `generated` only when the producer actor is known.
- Create `sources` only from actual materials.
- Attribute claims with footnotes keyed by `sources[].id`.
- Record `verified` only after a real check.
- Do not infer `status`, `stale_after`, credibility, or verification from git,
  timestamps, authorship, migration, or successful validation.
- Keep sanctioned computation in a standalone `type: Attested Computation`
  concept.
- Preserve unknown keys/types/content for forward compatibility.

Canonical examples are fixture-backed through
[`fixtures/v02/corpus.yaml`](fixtures/v02/corpus.yaml), including minimal,
Appendix A, bare/list verification, lifecycle, inline/file computation, legacy,
mixed, future, and adversarial cases. Repository-authored synthetic examples
use reserved domains such as `example.invalid`.

## Consumption rules

Resolve declared and effective versions before interpreting fields:

1. A supported declaration in root `index.md` selects its contract.
2. No declaration defaults to v0.2 with §13 legacy fallbacks.
3. A present malformed `okf_version` is not absence: it is a hard reserved-index
   error and never makes the bundle conformant through the v0.2 traversal default.
4. An unknown future declaration is retained and consumed best-effort.
5. An explicit selector is an assertion; a declaration conflict fails.

For v0.2 consumption:

- prefer `generated` and `sources`;
- fall back to `timestamp` only if `generated` is wholly absent;
- fall back to `# Citations` only if `sources` is absent;
- normalize bare `verified` to a one-item typed list;
- derive trust only from `verified`;
- surface trust, status, and staleness separately.

When legacy and v0.2 provenance coexist, v0.2 is the effective read because
the §13 fallback applies only when its replacement is absent. Raw legacy data
is still preserved. Migration always blocks a document containing both
`sources` and legacy Citations with manual action
`reconcile_sources_and_citations`; it never invents a merge/dedup policy.

## CLI

Common commands:

```sh
okf validate --path <bundle> --spec auto
okf validate --path <bundle> --spec auto --strict --as-of 2026-07-29
okf info <bundle> --spec auto --as-of 2026-07-29
okf parse <concept.md>
okf fmt <concept.md>
okf fmt <concept.md> -w
okf index <bundle>
okf graph <bundle>
okf migrate <bundle> --to 0.2
okf migrate <bundle> --to 0.2 --citation-mappings <json-file>
```

Migration is dry-run by default. Applying it requires explicit inputs for safe
timestamp conversion. Unresolved legacy Citations remain blockers until the
caller supplies the bounded JSON file `--citation-mappings <json-file>`. Its
exact top-level value is the closed array
`[{path,entries:[{legacy_number?,legacy_entry?,source_id,title?,resource?}]}]`,
keyed by bundle-relative Markdown `path`—including root `index.md`, logs, and
nested files. Every entry requires at least one selector: nonzero
`legacy_number` for `[1]`, or exact nonblank `legacy_entry` for an unnumbered
bullet/raw URL. If both are supplied, both must match the same actual entry.
CLI and MCP use the same DTO, canonically sorted by path, number, and exact raw
entry. Duplicate nonzero numbers always fail. The same `legacy_entry` is valid
only in distinct full number+entry pairs; an entry-only selector overlaps any
reuse of that raw text and fails. Source ID reuse requires consistent metadata.
`legacy_entry` is valid UTF-8, 1..4096 bytes, `TrimSpace`-nonblank, and NUL-free.
TAB/LF/CR are allowed; other C0 controls and DEL are rejected. Exact bytes enter
authorization/digest, so LF and CRLF are distinct and never normalized.
An explicit citation, generated-at, or computation path must name an existing
bundle document. A missing path blocks with `migration_document_missing`,
returns no manual actions, and cannot be created by mappings; no new action
code is introduced.
Migration publishes the root `index.md` physical write/rename last.
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
before store open and creates no `.okf`. Surface behavior for live
`target-noop` is explicit: MCP is proofless and opens no store; CLI dry-run
builds a proof without opening the store; CLI `--write` commits an empty CAS
and durably creates or replays its receipt under `.okf`. All three leave
revision-visible bundle files path-and-byte identical.
CLI `--actor` is the document producer written to `generated.by`; it is not the
store transaction principal. It may be omitted for dry preview, which reports
a manual action when generation metadata is needed; `--write` requires it
before such a change can apply. A version-only migration of type-only legacy
concepts does not need an actor or time and must not create `generated`.

Validation remains layered:

| Layer | Meaning |
| --- | --- |
| Base | UTF-8/parseable concept frontmatter, non-empty string `type`, reserved file structure. |
| Strict | Advisory validation of present v0.2 families, attribution, lifecycle, and computation shapes. |
| Links | Advisory internal link diagnostics. |
| Orphans | Advisory index coverage diagnostics. |

Unknown types/keys/runtimes, missing optional fields, broken links, and missing
indexes do not become base errors.

## Go library

```go
b, err := bundle.LoadBundle("./knowledge")
if err != nil {
	return err
}

report := validator.ValidateBundle(b, &validator.ValidatorConfig{
	Strict:       true,
	CheckLinks:   true,
	CheckOrphans: true,
})
if !report.IsConformant() {
	return errors.New("bundle is not conformant")
}
```

The read model remains permissive and Bring Your Own Types friendly. Typed v0.2
accessors are projections over lossless YAML; unknown fields remain available
to caller-owned models and survive round trips.

Mutation is desired-state, preview-first, and transactional. Semantic migration
is separate from parse, format, index, and graph operations.

## MCP server

```json
{
  "mcpServers": {
    "okf": {
      "command": "okf-mcp",
      "args": ["-root", "/absolute/path/to/bundle"]
    }
  }
}
```

Compatibility tools:

- `list_concepts`
- `read_concept`
- `validate_bundle`
- `get_semantic_graph`
- `write_concept`

The server exposes four safe v0.2 tools, for nine tools total:

- `preview_concept_patch` / `apply_concept_patch`
- `preview_v02_migration` / `apply_v02_migration`

Patch apply uses `expected_revision` plus its preview plan digest. Migration
apply is transition-discriminated: `v0.1-to-v0.2` requires preview proof
`format_version: 2` with a required non-empty `resolution_digest`, a non-empty
`expected_plan_digest`, and the same frozen `expected_source`; no earlier proof
format is accepted. Live `target-noop` requires only that frozen `expected_source`, is
proofless, and still validates the supplied migration target state. Migration
has no separate `expected_revision`; when proof exists, `proof.base_revision`
is authoritative. Actor metadata is not authentication. An identical migration
apply is replay-safe.
MCP actor-bearing fields have a separate 256-byte transport/resource cap:
257+ bytes returns `resource_limit`, not an invalid-actor verdict. Within the
cap, shared `ValidActor` decides semantic validity. The adapter cap is not actor
grammar and does not impose a bundle/store domain length ceiling.

Across MCP structured JSON, every present `usage_count` is a canonical decimal
string matching `^(0|[1-9][0-9]*)$`, or `null` where the output field is
nullable. Patch input rejects semantic uint64 overflow. Decimal strings avoid
`float64` precision loss in decoded MCP Arguments, including `MaxUint64`.
Legacy text fallbacks are unchanged.
The `set_usage_window` selector is a closed union: shared (neither `source_id`
nor `source`), identified (`source_id` is non-empty), or exact anonymous
(`source` is present and its `id` is absent). Empty `source_id`, mixed or
unknown selector forms, and ambiguous exact anonymous matches are rejected.
The `remove_source` selector is a closed union of identified (`source_id` is
non-empty) or exact anonymous (`source` is present and its `id` is absent); it
has no shared form. Empty `source_id`, mixed or unknown selector forms, and
ambiguous exact anonymous matches are rejected.

## Attested Computation safety boundary

OKF records computation and attestation contracts; this repository does not
execute bundle content merely because a field or resource exists.

`computation`, `executor.resource`, and `attester.resource` are inert data.
Per the pinned spec, an agent MAY supply only values for declared parameters
and MUST NOT author or edit the sanctioned computation.
Execution requires a separately trusted runtime and authorization. OKF v0.2
does not define runtime discovery, parameter binding implementation,
receipt/verdict wire format, attester ABI, portability, sandboxing, or cache.
`store.CommitReceipt` is unrelated to `executor.receipt`.

## `skosovsky/okf` relations extension

YAML `relations` is a repository extension/tooling policy, not part of upstream
OKF v0.2. It adds typed semantic edges and optional relation validation.
Markdown links remain the standard OKF navigation/relationship mechanism.
Legacy graph profiles and wire formats remain available as compatibility
surfaces; their version does not select the OKF document contract.

## Migration

See:

- [Migration guide](docs/migration.md)
- [Pinned v0.2 spec](skills/open-knowledge-format/references/spec-v02.md)
- [Agent workflow](skills/open-knowledge-format/SKILL.md)

Migration never invents producer actors, source mappings, verification,
lifecycle, freshness, credibility signals, receipt, or attestation.

## Development

```sh
go test ./...
go vet ./...
git diff --check
```

The final lock-file check verifies that `skills-lock.json` matches the exact
contents of `skills/open-knowledge-format`. Recompute that lock only after the
skill and its references are stable.
