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

Do not infer anchors from display `name`. `okf validate --check-links` checks
Markdown links only; graph export still preserves semantic edges whose target
concept is missing.

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
