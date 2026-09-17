# OKF v0.2 executable contract

Status: accepted for the v0.2 read/validation migration.

## Normative source

The normative source is `GoogleCloudPlatform/knowledge-catalog/okf/SPEC.md` at
commit `3fcbb9f828c2f23d109c855ee403c3a4c81f3a96`. The immutable raw URL is:

`https://raw.githubusercontent.com/GoogleCloudPlatform/knowledge-catalog/3fcbb9f828c2f23d109c855ee403c3a4c81f3a96/okf/SPEC.md`

Its SHA-256 is
`5a3311d270bebb16d558010e75064f5b75323f284992641732b1c8097511f948`.
`fixtures/v02/spec-lock.json` is the machine-readable lock. Normative clauses
are §§1–13; Appendix A is the canonical end-to-end example.

## Version resolution

The reader keeps the declaration and the effective contract separate.

| Input | Effective contract | Source | Compatibility |
| --- | --- | --- | --- |
| declared `0.1` | `0.1` | `declared` | `legacy` |
| declared `0.2` | `0.2` | `declared` | `native` |
| no declaration | `0.2` | `default` | `native` with §13 fallbacks |
| syntactically valid unsupported declaration | `0.2` | `future-best-effort` | `best-effort` |
| explicit supported selector, no conflicting declaration | selector | `explicit` | native or legacy |

An explicit selector is an assertion. A declaration that conflicts with it is
an error; the implementation must not silently select different semantics.
An `okf_version` declaration uses canonical `<major>.<minor>` syntax.
Syntactically valid unsupported declarations remain visible even though the
effective reader is v0.2 best effort. A malformed declaration is a reserved
root-index structure error and is never mislabeled as future best effort. Go
module, binary and plugin versions are unrelated to `okf_version`.

`future-best-effort` is the frozen public source label for every canonical
unsupported declaration, including numerically older declarations such as
`0.0`. The label identifies the forward-compatible fallback path; it is not a
numeric claim that the declared version is newer than `0.2`. The raw
declaration is always retained. The executable version matrix covers `0.0`,
`0.3`, `0.10`, `1.0`, very large canonical components, and malformed
leading-zero forms.

## Conformance boundary

Base conformance errors are limited to SPEC §11:

1. every non-reserved Markdown concept is valid UTF-8 and has parseable YAML
   frontmatter;
2. `type` is a non-empty YAML string;
3. present `index.md` and `log.md` files follow their reserved structures.

All v0.2 families are optional. Malformed present families are strict-policy
warnings and never make permissive loading fail. Unknown keys, concept types,
runtimes and parameter types remain consumable.

The v0.1 fallbacks are reads, never implicit migrations:

- `timestamp` is effective only when `generated` is completely absent;
- `# Citations` is effective only when `sources` is completely absent.

A present but malformed replacement suppresses its legacy fallback. Reading,
formatting, indexing and graph projection do not rewrite source documents.

## Duplicate YAML key policy

Duplicate YAML mapping keys are preserved by permissive loading rather than
treated as a whole-document parse failure. Unrelated producer-defined
duplicates remain available through the raw/lossless YAML APIs. A duplicated
standard key is still present but semantically unresolved: typed reads fail
closed instead of selecting one value, and duplicated replacement-family keys
still suppress legacy fallback.

The two §11/reserved-structure exceptions are duplicate concept `type` and
duplicate root `okf_version`; validator reports them as base `ERROR`
diagnostics. Other duplicated standard-family keys produce strict `WARNING`
diagnostics with their normal family field paths. Lossless mutation rejects a
duplicate only when the requested operation touches that ambiguous path or
selector; unrelated duplicates remain byte-preserved.

## Toolkit ambiguity ledger

These decisions are toolkit policy, not additions to upstream OKF:

| Ambiguity | Toolkit decision |
| --- | --- |
| `sources[].author` examples use undefined `team:*` | validate as a non-empty string; do not enforce the actor convention |
| `sources[].id` grammar and uniqueness are unspecified | preserve any non-empty string; strict validation requires uniqueness for attribution joins |
| `sources[].resource` may be a scope descriptor | keep it visible and do not force path/URI interpretation |
| timezone for `today >= stale_after` is unspecified | domain APIs require an explicit reference date; CLI/MCP require explicit `--as-of`/`as_of`; omission leaves staleness unevaluated and no surface may consult the wall clock |
| `usage_count` meaning | non-negative integer signal, never a credibility score |
| runtime and parameter type registry | open strings; no registry |
| executor and attester presence | strict guidance, not base conformance |
| simultaneous legacy and v0.2 provenance | v0.2 representation wins; no implicit deduplication |
| canonical unsupported version source label | preserve raw declaration and use the frozen public label `future-best-effort` for both older and newer unsupported versions |
| actor identifier length and `:` characters | enforce only the actor family and non-empty components; no domain length cap, and additional `:` characters inside `human:`/`process:` identifiers are allowed |
| fenced block “under `# Computation`” | sanctioned payload is one closed fenced block that is a direct top-level AST child inside the single top-level H1 section; prose siblings are allowed, while list/blockquote fences are quoted or example content and make the payload conflicting rather than absent |
| YAML `relations` | `skosovsky/okf` extension, not upstream v0.2 |

## Deferred ABI

The reader, validator and graph do not execute bundle content. Parameter
binding, executor runtime, receipt/verdict wire formats, attester ABI,
portability, sandboxing and attestation caching are deliberately deferred.
`store.CommitReceipt` has no relation to `executor.receipt`.

## Canonical corpus

`fixtures/v02/corpus.yaml` inventories shared fixtures for minimal v0.2,
Appendix A, inline and file-backed computation, verified mapping/list shapes,
v0.1 compatibility, mixed provenance, future versions and adversarial optional
families. `fixtures/v02/legacy-inventory.yaml` is the explicit allowlist for
legacy spellings in pinned specification history and regression fixtures.
Package and surface tests should refer to these paths rather than copying
fixture strings.
