---
title: OKF reference
description: Commands, libraries, MCP and exact format and migration rules.
permalink: /reference/
---

{% include nav.html %}

# OKF reference {#page-top}

This page collects exact command, library and MCP rules. Start with the [quickstart]({{ "/quickstart/" | relative_url }}) to try OKF; use this reference to check details and limits.

- [Format and version selection](#versions)
- [Installation and first files](#installation)
- [Authoring and effective reading](#authoring)
- [Validation and Markdown limits](#validation)
- [CLI command catalog](#cli)
- [Go packages and read model](#go)
- [Section search and Markdown preparation](#search)
- [MCP connection and tool catalog](#mcp)
- [MCP numeric and source selectors](#mcp-fields)
- [Migration inputs and producer](#migration-input)
- [Exact citation mappings](#citation-mappings)
- [Citation ownership and replay](#migration-replay)
- [Frozen source and migration proof](#migration-proof)
- [Publication, no-op and repeated requests](#migration-writes)
- [Migration manual actions](#migration-actions)
- [Explicit temporal upgrade](#temporal-upgrade)
- [Graph and relation-reference encoding](#relations)
- [Attested Computation boundary](#computation)
- [Skills and workflow dependencies](#skills)
- [Development checks](#development)

## Format and version selection {#versions}

The document contract is the [pinned OKF 0.2 specification](https://github.com/skosovsky/okf/blob/main/skills/open-knowledge-format/references/spec-v02.md). The Go module release, plugin release and `okf_version` are independent versions. Check installed binaries with `okf version --json`; find releases in [GitHub Releases](https://github.com/skosovsky/okf/releases).

| Input | Interpretation |
| --- | --- |
| Supported declaration in root `index.md` | Selects that document contract: `0.2` or intentionally supported legacy `0.1`. |
| No declaration | Defaults to `0.2`, with the legacy fallback rules in §13. |
| Present malformed `okf_version` | A hard reserved-index error. Traversal defaults do not make it conformant. |
| Unknown future declaration | Retained; familiar data is consumed best-effort without claiming future conformance. |
| Explicit `--spec 0.1` or `--spec 0.2` | An assertion. A conflict with the declaration fails instead of overriding it. |

Write `okf_version` only in root `index.md`. Two pinned upstream revisions both call themselves `0.2`: the original `date-3fcbb9f` profile remains the default; `instant-0b87c52` requires offset-bearing datetimes in `stale_after`, `sources[].last_modified` and `usage_window`. Choose the latter with `--temporal-profile instant-0b87c52` where supported. Dates cannot become instants without an explicit time and timezone. [ADR 0003](https://github.com/skosovsky/okf/blob/main/docs/adr/0003-okf-v02-temporal-revisions.md) records the revision choice.

## Installation and first files {#installation}

```sh
go install github.com/skosovsky/okf/cmd/okf@latest
go install github.com/skosovsky/okf/cmd/okf-mcp@latest
okf version
okf version --json
okf init ./my-knowledge
okf validate --path ./my-knowledge --spec 0.2
```

`init` requires an absent directory with an existing parent. It never overwrites an existing file, directory or symlink. See the [init contract](https://github.com/skosovsky/okf/blob/main/docs/contracts/cli-init.md).

These two files are also a minimal conformant bundle:

`index.md`:

```markdown
---
okf_version: "0.2"
---

# Concepts

* [Minimal](minimal.md) - A minimal note.
```

`minimal.md`:

```markdown
---
type: Unknown Producer Type
---

A minimal note.
```

`type` is the only always-required concept field. Provenance, verification, lifecycle and computation are optional families. Replace the example body with knowledge from provided materials; the [quickstart]({{ '/quickstart/' | relative_url }}) walks through that operation.

## Authoring and effective reading {#authoring}

| Rule | Consequence |
| --- | --- |
| Add `generated` only for a known producer | Transaction actor, Git author, file owner and current user are not implicit document producers. |
| Add `sources` from actual materials | Attribute claims with footnotes keyed by `sources[].id`. |
| Record `verified` after an actual check | Authorship, Git, timestamps, migration and format validation do not create verification, credibility, lifecycle status or freshness. |
| Preserve unknown keys, types and content | Future fields remain available without narrowing them to the toolkit's typed model. |
| Keep sanctioned computation in a standalone `type: Attested Computation` concept | See [computation](#computation) for execution boundaries. |

For effective v0.2 reading, use `generated` and `sources`. Use `timestamp` only when `generated` is wholly absent, and legacy `# Citations` only when `sources` is absent. Presence, including a malformed replacement, suppresses fallback. This predicate is independent of whether the version was defaulted, declared or explicitly selected, including v0.1 and future versions.

`GeneratedPresent`/`SourcesPresent` describe presence; `TimestampAllowed`/`CitationsAllowed` describe replacement absence; `TimestampActive`/`CitationsActive` additionally require an actual legacy form. Active citations require an exact parser-owned `# Citations` heading. A numeric `[1]` in prose alone is not legacy evidence: an undeclared bundle containing only that prose resolves as native v0.2 `target-noop`.

A bare `verified` value and a one-item list have the same typed semantics without rewriting their YAML. Trust is derived only from `verified`; trust, raw/effective lifecycle status and staleness are separate projections. When `sources` and old citations coexist, reading prefers `sources` and preserves the legacy bytes. Migration blocks with `reconcile_sources_and_citations` instead of merging or deleting them.

Canonical examples are registered in [the fixture corpus](https://github.com/skosovsky/okf/blob/main/fixtures/v02/corpus.yaml): minimal, Appendix A, verification, lifecycle, computation, legacy, mixed, future and adversarial cases. Synthetic examples use reserved domains such as `example.invalid`.

## Validation and Markdown limits {#validation}

| Layer | What it checks |
| --- | --- |
| Base | UTF-8, parseable concept frontmatter, non-empty string `type`, reserved-file structure. |
| Strict | Advisory checks of present v0.2 fields, attribution, lifecycle and computation shapes. |
| Links | Advisory internal-link diagnostics. |
| Orphans | Advisory coverage by indexes, only when requested. |

Unknown types, keys or runtimes, absent optional fields, broken links and missing indexes do not become base errors. A reserved index may start with a title and introduction before its linked groups. Unknown root frontmatter keys alongside a valid `okf_version` survive. Wrapped lines in a flat `log.md` item remain one entry. Strict attribution ignores literal footnotes in code spans.

Parser-owned Markdown processing has separate **16 MiB** limits for a complete document (`bundle.MaxMarkdownDocumentBytes`) and an already separated body (`bundle.MaxMarkdownBodyBytes`). Error classification order is cancellation, invalid UTF-8, `bundle.ErrMarkdownResourceLimit`, then parser/ownership errors. `bundle.MarkdownResourceLimitError` exposes resource kind, limit and saturated observed size. Context APIs return exact zero results on rejection. These limits are not the same as the aggregate [search](#search) limits.

## CLI command catalog {#cli}

| Command | Purpose |
| --- | --- |
| `init` | Create a draft bundle in a new directory. |
| `setup` | Preview/apply a managed copy of ordinary Markdown into a new directory. |
| `search` | Search sections using all query terms. |
| `validate` | Check the document contract and optional diagnostics. |
| `info` | Inspect version-aware projections. |
| `parse` | Parse one document; optionally emit JSON projections. |
| `fmt` | Format one document; `-w` writes it. |
| `index` | Generate navigation indexes. |
| `graph` | Project links and optional typed relations. |
| `view` | Export a standalone HTML viewer; `--lang en|ru` selects initial interface language, default `en`. |
| `migrate-prepare` | Prepare editable migration input templates outside the source bundle. |
| `migrate` | Preview/apply v0.1 → v0.2 migration. |
| `temporal-upgrade` | Preview/apply explicit date → instant mappings within v0.2. |
| `version` | Report program and supported document versions. |
| `help` | Show CLI help. |

```sh
okf search ./knowledge --query "retry deadline" --limit 5 --json
okf setup --source ./notes --target ./prepared-knowledge --type Note --json
okf validate --path ./knowledge --spec auto --strict --as-of 2026-07-29
okf validate --path ./knowledge --check-links --check-orphans
okf validate --path ./knowledge --spec 0.2 --strict --temporal-profile instant-0b87c52 --as-of 2026-09-23T11:00:00Z
okf info ./knowledge --spec auto --as-of 2026-07-29
okf parse ./knowledge/minimal.md --temporal-profile instant-0b87c52 --as-of 2026-09-23T11:00:00Z --format json
okf fmt ./knowledge/minimal.md
okf fmt ./knowledge/minimal.md -w
okf index ./knowledge --spec auto
okf graph ./knowledge --profile skosovsky/okf-v0.2-instant --format ntriples --as-of 2026-09-23T11:00:00Z
okf view ./knowledge --output ./knowledge.html --lang en
```

Use an explicit `--as-of` for reproducible staleness. Parsing, formatting, indexing and graph projection do not migrate provenance, stamp producers or timestamps, add verification, or bump `okf_version`. Unknown data remains lossless within supported parser-backed changes. See the [toolkit guide]({{ '/toolkit/' | relative_url }}) for complete workflows.

## Go packages and read model {#go}

| Package | Responsibility |
| --- | --- |
| `bundle` | Load documents/assets and expose lossless YAML plus typed v0.2 views. |
| `validator` | Base, strict, links and index-coverage diagnostics. |
| `graph` | Standard links, typed relations and export formats. |
| `retrieval` | Shared section search for Go, CLI and MCP. |
| `setup` | `Preview` and digest-bound `Apply` for ordinary Markdown. |
| `viewer` | Project and render a standalone HTML viewer; `Render` defaults to English, `RenderWithOptions` chooses language. |
| `mutation` | Preview and apply semantic desired-state changes. |
| `store` | Transaction, revision, compare-and-swap and receipt contracts. |
| `store/fs` | Filesystem implementation with journal durability. |
| [`backfill`](https://github.com/skosovsky/okf/tree/main/backfill) | Extract bounded Git evidence, validate analyzer proposals and prepare reviewed history reconstruction. |
| [`upkeep`](https://github.com/skosovsky/okf/tree/main/upkeep) | Capture repository baselines, detect affected knowledge and check decisions against current fingerprints. |

```go
b, err := bundle.LoadBundle("./knowledge")
if err != nil {
    return err
}
report := validator.ValidateBundle(b, &validator.ValidatorConfig{
    Strict: true,
    CheckLinks: true,
    CheckOrphans: true,
})
if !report.IsConformant() {
    return errors.New("bundle is not conformant")
}
```

The fragment uses `errors`, `github.com/skosovsky/okf/bundle` and `github.com/skosovsky/okf/validator` imports inside a function returning `error`.

Typed accessors are projections over lossless YAML and return defensive copies. `Get`, YAML-node access and caller-owned structs remain authoritative for unknown data: Bring Your Own Types. Raw frontmatter/body remain available rather than being forced through a closed schema. Non-Markdown source/computation assets are inert revision-visible files; reading them does not execute or fetch them.

Mutation previews staged output, validates the final bundle, preserves untouched presentation and commits through compare-and-swap (CAS) and journal durability. Semantic migration is separate from parsing/formatting/indexing/graph. Transaction actors and `store.ChangeSet`/receipt formats are independent of document actors. Legacy wire/profile versions change only under their own contracts.

## Section search and Markdown preparation {#search}

`search` and MCP `search_sections` require all distinct normalized terms in a section's Markdown, heading or concept title. Unicode case folding and NFC normalize terms; there is no stemming, translation, synonym expansion or semantic search. Markdown code is inert searchable text, not executable input; fenced pseudo-headings do not split sections. Index/log files are excluded.

Hits include original snippets, inclusive physical line ranges, percent-encoded locators, the whole-file SHA-256 and a snapshot fingerprint. Recheck the digest or repeat search after edits. The fingerprint is not a store revision, authorization proof or evidence of content truth. Ranking is lexical BM25 (`k1=1.2`, `b=0.75`; Markdown/heading/title weights `1/3/2`), then concept ID and starting line for ties.

| Search bound | Value |
| --- | --- |
| Query / distinct terms / normalized term | 1–512 Unicode characters / 32 / 4096 characters |
| Requested hits | 1–100; default 20 |
| Captured files / aggregate bytes / one file | 10,000 / 64 MiB / 16 MiB |
| Sections / indexed tokens | 100,000 / 1,000,000 |
| Tree depth / path bytes | 64 / 4096 |
| Heading and title / snippet | 1024 UTF-8 bytes each / 512 Unicode characters |
| JSON response | 1 MiB |

`total` counts matching sections; `truncated` reports omitted hits. There is no cursor or persistent index. Refine terms or raise the limit up to 100. A limit, parse error or cancellation fails without partial results. Loading excludes internal symlinks and rejects symlink roots/escapes. [Full section-search contract]({{ '/contracts/section-search/' | relative_url }}).

`setup` preserves the source and previews copies into a new target. Review its plan and pass its exact digest to apply; source changes, target collisions and unsupported links/assets block publication. The note type is explicit; setup does not infer sources, producers, verification or freshness. [Full Markdown preparation contract]({{ '/contracts/markdown-setup/' | relative_url }}).

## MCP connection and tool catalog {#mcp}

The server has no `-root` flag. Each call supplies an accessible absolute `bundle_path`; host and process permissions determine accessible files. [Connection walkthrough]({{ '/getting-started-mcp/' | relative_url }}).

```json
{"mcpServers":{"okf":{"command":"okf-mcp","args":[]}}}
```

| Tool | Operation |
| --- | --- |
| `list_concepts` | List documents. |
| `read_concept` | Read raw and interpreted document data. |
| `validate_bundle` | Validate a bundle. |
| `get_semantic_graph` | Read graph projections. |
| `write_concept` | Compatibility write interface. |
| `search_concepts` | Bounded literal-substring search. |
| `search_sections` | Ranked sections containing all lexical terms. |
| `get_neighbors` | Navigation and typed-relation neighbors; provenance sources remain separate. |
| `preview_concept_patch` | Preview a desired-state patch. |
| `apply_concept_patch` | Apply a revision/digest-bound patch. |
| `preview_v02_migration` | Preview legacy migration. |
| `apply_v02_migration` | Apply the matching migration. |
| `preview_temporal_upgrade` | Preview explicit date-to-instant mappings. |
| `apply_temporal_upgrade` | Apply the matching temporal plan. |

There are fourteen tools. Compatibility tools retain their text fallbacks. Structured outputs are schema-validated. `search_concepts` and `get_neighbors` return `total`/`truncated` when bounded output omits hits/edges. There is no cursor: narrow input, or request a whole graph if it fits. A hard `resource_limit` fails the call rather than returning a partial page. Invalid inputs produce stable codes and bounded field guidance. [Query contract]({{ '/contracts/concept-queries/' | relative_url }}).

`search_concepts` ranks ID and metadata matches ahead of body matches; it is literal search, separate from section ranking.

Patch apply needs `expected_revision` and the preview plan digest. Migration has a different [proof contract](#migration-proof). Actor metadata is not authentication. Bundle paths obey containment/no-follow rules; a resource value is not permission to access files, network, shell or secrets. [Mutation trust boundaries]({{ '/contracts/mcp-mutation-trust-boundaries/' | relative_url }}).

## MCP numeric and source selectors {#mcp-fields}

MCP actor-bearing fields have a **256-byte transport/resource limit** before domain validation. At 257+ bytes the error is `resource_limit`, not invalid actor. Within that bound shared `ValidActor` decides semantics. This is an adapter limit, not a length rule for bundle/store actors.

In structured MCP JSON, every present `usage_count` is a canonical decimal string matching `^(0|[1-9][0-9]*)$`, or `null` for nullable output fields. Patch input rejects uint64 overflow. Strings avoid `float64` precision loss when decoding Arguments, including `MaxUint64`; legacy text output is unchanged.

| Operation | Allowed selector forms |
| --- | --- |
| `set_usage_window` | Shared: neither `source_id` nor `source`; identified: non-empty `source_id`; exact anonymous: `source` present, its `id` absent. |
| `remove_source` | Identified or exact anonymous, with the same definitions. No shared form. |

Empty `source_id`, mixed forms, unknown selector forms and ambiguous exact anonymous matches are rejected. Source metadata is not matched through a guessed identity.

## Migration inputs and producer {#migration-input}

The [migration guide]({{ '/migration/' | relative_url }}) shows a runnable example. This section defines the detailed behavior.

Migration validates each supplied actor, timestamp, citation, generated-at, computation and asset field **before** source resolution or transition planning, even for a rootless bundle or `target-noop`. Invalid individual input is rejected rather than ignored. An unrenderable individual migration field is `invalid_request`.

A legacy timestamp has no producer. Converting it into `generated.at` requires caller-provided `generated.by`. CLI `--actor` is that document producer, not `store.ChangeSet.Actor`; the adapter uses a separate internal transaction principal. Preview may omit it and receive `provide_generated_by`; writing a generated change needs the producer. Conflicting `timestamp`/`generated.at` requires explicit policy. CLI preserves the legacy timestamp and rejects conflicts. A type-only legacy bundle without timestamps changes only the root version; no actor/time is required and no `generated` is invented.

Explicit citation, generated-at and computation paths must name **existing** bundle documents. A missing path blocks with `migration_document_missing`, with an empty manual-action list; mappings cannot create the missing document.

Migration never infers source titles, authors, usage or last-modified dates, claim mappings, verification or trust, lifecycle status/stale dates, credibility, receipts/verdicts, successful attestation, or an Attested Computation from narrative prose.

## Exact citation mappings {#citation-mappings}

CLI `--citation-mappings <json-file>` and MCP accept the same closed array; there is no wrapper object:

```json
[{"path":"retries.md","entries":[{"legacy_number":1,"source_id":"requirements","title":"Training requirements","resource":"https://example.invalid/requirements"}]}]
```

| Field | Rule |
| --- | --- |
| `path` | Bundle-relative Markdown path, including root `index.md`, logs and nested files; not a concept ID. |
| `legacy_number` | Nonzero number selecting the parser-owned `[n]` citation entry. |
| `legacy_entry` | Exact raw entry, including an unnumbered bullet or URL. At least one selector is required. |
| Both selectors | Both must match the same entry. |
| `source_id` | Explicit stable identity. Reuse requires consistent metadata. |
| `title`, `resource` | Optional explicit metadata. No inferred title or destination. |

`legacy_entry` is valid UTF-8, 1–4096 bytes, nonblank according to `strings.TrimSpace` and contains no NUL. TAB/LF/CR are allowed; other C0 controls and DEL are rejected. Matching does not trim, fold case, canonicalize URLs or normalize newlines. Exact bytes are part of authorization and the digest: LF and CRLF are distinct.

Duplicate nonzero numbers always fail within one document. Reuse of raw `legacy_entry` is valid only in distinct complete number+entry pairs. An entry-only selector overlaps any reuse of that raw text; two entry-only copies are also invalid. An entry-only match to duplicate raw entries is ambiguous. Canonical ordering is path, number, exact raw entry. Unknown fields, unsafe paths, oversized values/files/arrays, inconsistent metadata and incomplete mappings fail closed.

The CLI loader bounds its JSON file at 16 MiB, documents at 10,000, entries at 10,000, each string at 4096 bytes, `legacy_number` at 1,000,000 and JSON depth at 16. These are input-file limits, not general Markdown limits. [Loader implementation](https://github.com/skosovsky/okf/blob/main/internal/okfcli/citation_mappings.go).

## Citation ownership and replay {#migration-replay}

Only claim markers whose ownership the Markdown parser can prove are rewritten. Ambiguous, duplicate or unresolved Markdown blocks migration. Inline/fenced code and raw HTML are opaque: marker-like text there neither satisfies nor fails a claim replay assertion. Literal `[^label]` in inline or multiline code spans is opaque; a real shortcut footnote in prose participates.

On `v0.1-to-v0.2`, a number selects an entry in the active legacy section. On `target-noop`, an explicit mapping asserts the already-migrated state: the keyed footnote must match and, for a concept, so must the structured source ID and supplied metadata. It never converts bare `[1]` prose. An active legacy section on the v0.2 target blocks replay.

For each nonzero `legacy_number`, the selected parser-owned `[n]` marker must be gone and the normalized keyed `[^SourceID]` reference must exist. Entry-only mappings have no claim-reference requirement. A remaining selected marker, missing reference or wrong reference blocks with `migration_replay_mismatch` at the exact parser-owned span, with no invented manual action, proof or plan authorization.

A normalized per-document source-ID/footnote-label collision blocks with `normalized_footnote_label_collision` and `disambiguate_citation_entry`, also on `target-noop`. An existing-document collision reports the same exact span. This is distinct from an unrenderable individual field (`invalid_request`). Both outcomes write nothing.

## Frozen source and migration proof {#migration-proof}

Source resolution runs exactly once before preview. Preview and apply use the same complete `expected_source`: `requested_selector`, declaration state (`declaration_present`, `declaration_valid`, `declaration_raw`, `declared_version`), resolved version/provenance and transition fields, plus ordered legacy candidates and blockers. Apply rejects changed resolution **or its evidence**. A present malformed root version is not an absent/default source; unsupported canonical future declarations remain declared and block v0.1 migration.

| Transition | Apply requirements |
| --- | --- |
| `v0.1-to-v0.2` | Preview proof `format_version: 2`, non-empty `resolution_digest`, non-empty `expected_plan_digest`, and identical full `expected_source`. Earlier proof formats are rejected. |
| Live `target-noop` over MCP | Same frozen `expected_source`, no proof or plan digest; target-state input is still validated. |
| Blocked preview | Cannot be applied. |

There is no separate migration `expected_revision`: `proof.base_revision` is authoritative when proof exists. A successful transition preview with a non-empty plan digest returns a content-free proof. It freezes request and source resolution, revision digests, read paths, write path+digest pairs, deletes, renames, affected and reverse-impact references, and changed file/reference summaries. Arrays are non-null. The proof contains no file bytes, frontmatter or body. Noop/blocked MCP preview omits proof.

A proof-bound transition apply may return transition-noop only after authenticating and rebuilding the exact proof and plan digest. It returns before opening the store and creates no `.okf`.

## Publication, no-op and repeated requests {#migration-writes}

The entire staged bundle must validate as v0.2 before atomic publication. The physical write/rename of root `index.md` is last: its version declaration is the transaction's final visible publication. Unknown YAML, Markdown and assets are preserved.

Every preview, rejected, blocked, invalid, cancelled or other **non-publication** path leaves the complete filesystem tree path-for-path and byte-for-byte identical, with no `.okf` or staging artifacts. Only an authorized actual commit may publish changes. An identical successful replay returns the recorded result without a second publication.

`target-noop` has adapter-specific behavior:

| Surface | Store/receipt behavior | Bundle files |
| --- | --- | --- |
| MCP live `target-noop` | No proof; store is not opened; no `.okf`. | Identical paths and bytes. |
| CLI dry-run | Builds proof without opening store. | Identical paths and bytes. |
| CLI `--write` | Commits empty CAS; durably creates or replays receipt under `.okf`. | Revision-visible files have identical paths and bytes. |

The CLI write case is an actual receipt publication; it is not covered by the non-publication no-artifact rule. Transaction durability evidence (`store.CommitReceipt`) and computation runtime receipt (`executor.receipt`) are separate contracts.

## Migration manual actions {#migration-actions}

| Code | What the caller must resolve |
| --- | --- |
| `provide_citation_mapping` | Missing/unresolved mapping, or number and raw entry contradict the actual citation. |
| `disambiguate_citation_entry` | Duplicate number/raw selector match or normalized source/footnote-label collision. |
| `disambiguate_citation_destination` | Parser ownership cannot identify one link destination. |
| `normalize_citations_section` | Opaque/unowned extra content in the legacy section. |
| `reconcile_sources_and_citations` | Structured sources coexist with the legacy section. No merge, deduplication or deletion is inferred. |
| `repair_invalid_utf8` | Invalid UTF-8. |
| `provide_generated_at` | Required generation time is absent. |
| `provide_generated_by` | Required producer is absent. |

Duplicate raw-entry selection is different from destination ambiguity. `migration_document_missing` and `migration_replay_mismatch` are blocking errors without an invented manual-action code. See [migration preparation](https://github.com/skosovsky/okf/blob/main/docs/contracts/migration-preparation.md) for editable templates and exact byte spans, and the [normative migration policy](https://github.com/skosovsky/okf/blob/main/skills/open-knowledge-format/references/migration-v01-v02.md).

## Explicit temporal upgrade {#temporal-upgrade}

This is separate from v0.1 migration. Supply a mapping for each observed date in `stale_after`, `usage_window.from/to` and `sources[].last_modified`. The tool never invents midnight or end of day. `sources[N]` uses the zero-based position in the previewed revision. Unresolved values block apply.

```json
{"mappings":[{"concept":"payments/retries","path":"stale_after","from":"2026-09-26","to":"2026-09-26T18:00:00+07:00"}]}
```

Save the complete mapping file as `temporal-mappings.json`. Review the preview, then use the exact returned `preview.BaseRevision` and `plan_digest`:

```sh
okf temporal-upgrade ./knowledge --id temporal-2026-09 --actor human:reviewer --mappings temporal-mappings.json
okf temporal-upgrade ./knowledge --id temporal-2026-09 --actor human:reviewer --mappings temporal-mappings.json --write --base-revision 'sha256:<preview.BaseRevision hex>' --plan-digest 'sha256:<preview plan_digest hex>'
```

Apply rebuilds the plan against the current revision and publishes one transaction. If files changed, preview again and review positions/digest. Unsupported YAML presentation, such as an alias or tagged quoted scalar the lossless patcher cannot prove, fails closed. Rewrite that scalar in a plain or unquoted core-tag form and preview again. Select `instant-0b87c52` to read the result; the old date profile remains default. [Temporal contract](https://github.com/skosovsky/okf/blob/main/docs/contracts/mcp-mutation-trust-boundaries.md).

## Graph and relation-reference encoding {#relations}

`relations` is a `skosovsky/okf` extension/tooling policy, not upstream OKF v0.2. Standard Markdown links remain the format's navigation and relationship mechanism. Graph profiles (`skosovsky/okf-v0.2`, `skosovsky/okf-v0.2-instant`, `legacy-v0.1`) are independent of document version. Exports include `dot`, `mermaid`, `json-ld` and `ntriples`; extension inclusion is explicit via `--extension-relations include`.

The relation-reference grammar is `<escaped-concept-id>[#<fragment>]`. Escape only `#` belonging to the ID with the logical two-byte `\#`; the first unescaped `#` starts the fragment:

| Logical reference | Meaning |
| --- | --- |
| `source#part` | Concept `source`, fragment `part`. |
| `source\#part` | Concept whose ID is `source#part`, without fragment. |

After the delimiter, backslashes are ordinary fragment bytes. Stray/noncanonical ID escapes (`source\part`, `source\`, `source\\#part`) fail. Existing references without escaped hashes retain their exact bytes. YAML can carry `'source\#part'`; JSON carries `"source\\#part"` because it escapes the backslash. This additive canonical encoding does not alter graph/MCP schema shape. Store receipt v1 keeps references as `[]string` with lexical canonical wire ordering; escaped-hash support is a canonicalization fix, not a format migration.

## Attested Computation boundary {#computation}

OKF records computation and attestation contracts. `computation`, `executor.resource` and `attester.resource` are inert data. Their presence does not execute content. Execution requires a separately trusted runtime and authorization.

The pinned spec permits an agent to supply values **only for declared parameters** and forbids it to author or edit sanctioned computation. OKF v0.2 does not define runtime discovery, parameter binding implementation, receipt/verdict wire format, attester ABI, portability, sandboxing or cache. The toolkit does not invent these mechanisms. `store.CommitReceipt` is neither stored in nor accepted as `executor.receipt`.

Body instructions cannot override frontmatter, lifecycle/freshness signals, authorization or trusted-runtime policy. LLM prose is not an attestation verdict.

## Skills and workflow dependencies {#skills}

| Skill | Purpose and required tools |
| --- | --- |
| `open-knowledge-format` | Author/read/validate/export/migrate; installed `okf` or connected `okf-mcp`. Pinned to the v0.2 specification. |
| `okf-maintain` | Review repository knowledge after code/contracts change; also needs `okf-upkeep`. |
| `okf-backfill` | Evidence-bound Git-history reconstruction; needs `okf-backfill`, `backfill/PROTOCOL.md` and its schemas. |

Copy the selected directory from `skills/` into the host's skills directory, keeping its relative references. Skill files do not install binaries or grant permissions. Verify actual host registration and tool connectivity; the [skill guide]({{ '/skill/' | relative_url }}) shows a first invocation.

If MCP is missing, check installed CLI or source-checkout Go commands. If neither exists, report manual inspection separately from a completed tool check. Viewer output must be local and outside the bundle; replacing existing output needs authorized `--overwrite`.

`okf-maintain` captures a baseline before repository edits, compares affected notes with code/contracts, and records an updated or reasoned unaffected decision bound to the final fingerprint. `okf-backfill` follows Git extraction, evidence-bound analysis, reviewed plan, authorized apply and independent verification through the Go pipeline.

`skills.sh.json`, both plugin manifests and `skills-lock.json` register the three skills. `skill-dependencies.json` lists workflow dependencies by name/path without duplicating schemas. CI checks dependencies against MCP catalog and CLI dispatch, checks links/assets, and copies only `skills/` to an isolated installation root. The package checker uses the Go YAML parser and resolves inline/reference-style Markdown links inside the installed tree.

## Development checks {#development}

```sh
go test ./...
go vet ./...
git diff --check
node scripts/update-skills-lock.mjs --check
node scripts/check-skill-package.mjs
```

Recompute the skill lock after changed skill content/references are stable. A filesystem/package smoke test proves packaging, not activation or tool invocation in Codex, Claude Code or skills.sh. CI does not exercise those hosts: check each claimed host with a real client before making a release claim.

The repository's own public bundle begins at [knowledge/index.md](https://github.com/skosovsky/okf/blob/main/knowledge/index.md); [repository knowledge]({{ '/knowledge/' | relative_url }}) explains maintenance. Its [published viewer](https://skosovsky.github.io/okf/demo/knowledge.html) is a reproducible snapshot, separate from the bilingual training example.
