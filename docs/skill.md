---
title: Skill
description: Repo-local OKF agent skill.
permalink: /skill/
---

{% include nav.html %}

# Skill

The repository includes `skills/open-knowledge-format`: a portable agent skill for consulting, designing, creating, converting, enriching, and validating OKF bundles.

## When to use it

- Explain OKF concepts and conformance rules.
- Design a new OKF bundle structure.
- Convert Markdown, Notion, Obsidian, CSV, or spreadsheet material into OKF.
- Enrich existing concepts with metadata, `# Schema`, `# Examples`, citations, cross-links, indexes, and logs.
- Validate bundles through this repository's CLI.
- Operate on local bundles through the `okf-mcp` server when the host supports MCP tools.
- Inspect, visualize, or export graph output for Markdown links and YAML semantic relations.

## CLI toolkit workflow

For a conformance gate, the skill runs the quality gate:

```sh
go run ./cmd/okf validate -path <bundle>
```

For review workflows, it can enable every advisory mode:

```sh
go run ./cmd/okf validate -path <bundle> --strict --check-links --check-orphans
```

The skill treats `[ERROR]` as hard OKF v0.1 failure. `[WARN]` and `[INFO]`
remain review signals for recommended fields, conventional body sections,
links, anchors, and local index coverage. Missing `resource` is intentionally
allowed for abstract concepts.

With `--check-orphans`, an empty non-root local `index.md` is treated as an
orphan-coverage surface and reports orphan warnings instead of an empty-index
structure error.

For bundle summary and maintenance, the skill can use:

```sh
go run ./cmd/okf info <bundle>
go run ./cmd/okf index <bundle>
go run ./cmd/okf fmt <file>
```

## MCP server workflow

The skill remains a single skill at `skills/open-knowledge-format/SKILL.md`.
When an agent host supports MCP, configure the separate stdio server command:

```sh
go run ./cmd/okf-mcp
```

or install it:

```sh
go install github.com/skosovsky/okf/cmd/okf-mcp@latest
```

Example client configuration:

```json
{
  "mcpServers": {
    "okf": {
      "command": "okf-mcp"
    }
  }
}
```

`stdout` is reserved for the MCP JSON-RPC protocol; diagnostics go to `stderr`.

The server exposes `list_concepts`, `read_concept`, `validate_bundle`,
`get_semantic_graph`, and `write_concept`. All tools require an absolute
`bundle_path`; concept tools require canonical concept ids without `.md`.
`write_concept` validates an in-memory staged bundle with strict/link/orphan
checks, then publishes through a bundle-wide advisory lease, fresh-revision
CAS, Journal v5/recovery, cleanup, and receipt. Journal v5 uses a compact,
bounded manifest that binds algorithm and canonical request/result/replay data,
with separately durable staged payloads; recovery verifies a safe no-follow
path, declared size, and SHA-256 digest before apply. The persisted receipt
envelope is v2. It replans and revalidates a
conflict at most twice; rejected writes return diagnostics and leave the bundle
unchanged. The lease is advisory: raw editors do not coordinate, and raw
readers can observe non-atomic multi-file renames during publication.
Revisions default to `sha256:<lowercase-hex>`, but `fs.Config.HashAlgorithm`
Staged durable payloads are bounded by `fs.Config`: 256 MiB per payload and
1 GiB per transaction by default; recovery rejects an oversized manifest
before allocating payload bytes.
can replace the algorithm; Journal v5 binds it, so recovery requires the same
configured algorithm. Every regular file under the bundle root is revision
visible, including non-Markdown and reserved index/log files, except `.okf/**`;
symlinks are never read or hashed, and the internal journal, receipts, and
lease are excluded. Before changes, inspect context with
`get_semantic_graph` and `read_concept`; validate with `validate_bundle`; use
`write_concept` for concept edits instead of direct filesystem writes when MCP
is available. An identical retry uses the same server-side idempotent pipeline
without republishing; do not add transport-only retry fields. MCP success still
contains only `status`, `path`, and `diagnostics`, never a receipt DTO or commit
evidence.

## Graph output workflow

For quick terminal inspection, the skill can use the default graph output:

```sh
go run ./cmd/okf graph <bundle>
```

For non-default graph output, choose the format by destination:

```sh
go run ./cmd/okf graph <bundle> -format dot
go run ./cmd/okf graph <bundle> -format mermaid
go run ./cmd/okf graph <bundle> -format json-ld
go run ./cmd/okf graph <bundle> -format ntriples
```

Use `-format mermaid` when the graph should be pasted into Markdown or README
content. Mermaid output starts with `graph LR`; broken internal links are dotted
edges labeled `404`. Use `-format dot` for Graphviz tooling; `--dot` remains a
legacy alias for `-format dot`. Use `-format json-ld` when graph tooling or an
agent harness needs machine-readable `@context` and `@graph` output. In JSON-LD,
concepts are `bundle:<id>` nodes with `@type: "okf:Concept"`, and internal
links are `okf:Reference` objects with `target` and `exists`; keep dangling
links visible as `"exists": false`. Use `-format ntriples` when RDF tooling,
bulk-load jobs, streaming graph pipelines, or shell processing need one
full-IRI fact per line.

For impact analysis, prefer semantic YAML `relations` over generic Markdown
links. Markdown links are navigation; `relations` are contract edges. Targets
use OKF concept refs such as `tables/orders#col-status`, not Markdown paths such
as `tables/orders.md#col-status`. For field-level tracing, require an explicit
`id` or `anchor` on the nested YAML mapping:

```yaml
schema:
  fields:
    - id: payload-user_id
      name: user_id
      relations:
        writes_to:
          - target: tables/orders#col-customer_id
```

Do not infer anchors from display `name`. `okf validate` and MCP
`validate_bundle` check base v0.1 conformance; `--check-links` only adds
Markdown-link checks. Go callers can opt into semantic relation reporting with
`ValidatorConfig.CheckRelations`; mutation and write paths always reject
blocking relation diagnostics. Noncanonical anchor aliases are informational.
Semantic failures are excluded from resolved
outgoing, incoming, and reverse indexes and from all semantic graph exporters.
Dangling Markdown links are a separate navigation layer and may still be
rendered as missing.

Only nested mappings define fragments; top-level frontmatter `id` and `anchor`
are concept metadata. For a nested mapping, frontmatter `id` is canonical. A valid, differing
`anchor` is a noncanonical alias for information and navigation; semantic
mutations and relation refs must address the canonical `id`.

Relation ref grammar is `<concept-id>[#<fragment>]`. Invalid refs include
`/tables/orders.md`, `tables/orders.md`, `#local-section`,
`https://example.com/orders`, `urn:orders`, `tables/orders#`,
`tables/orders#col#status`, and `tables/orders# col-status`.

## Install as a local Codex plugin

Run from the repository root:

```sh
codex plugin marketplace add .
codex plugin add okf@okf-local
```

After installation, open a new Codex session and ask to use `$open-knowledge-format`.

## Install as a Claude Code plugin

Use the repository's Claude plugin manifest:

```text
/plugin marketplace add skosovsky/okf
/plugin install okf@okf
/reload-plugins
```

After installation, invoke `/okf:open-knowledge-format` or let Claude Code use
the skill automatically when the task matches OKF.

## Portable skill path

```text
skills/open-knowledge-format/SKILL.md
```

For runtimes that support local skills, register or copy `skills/open-knowledge-format` under the name `open-knowledge-format`.

## Included references

| File | Purpose |
| --- | --- |
| `references/spec-v01.md` | OKF v0.1 reference. |
| `references/examples.md` | Example bundles. |
| `references/conversion.md` | Notion, Obsidian, CSV, and spreadsheet conversion guidance. |

The skill intentionally has no bundled validation script. Deterministic checks and graph extraction go through `cmd/okf`.

Filesystem store: `MaxStagedFiles` defaults to and is capped at 100,000 (`payload-00000`…`payload-99999`). Case-folding and Unicode-normalization aliases are independently detected. `.okf` directories are no-follow 0700; private files and lease are 0600 or fail closed.
