---
title: OKF - Markdown spec for humans and AI agents
description: Open Knowledge Format documentation, CLI toolkit, and agent skill.
---

{% include nav.html %}

# Open Knowledge Format

OKF is a small, vendor-neutral format for knowledge bases that live in git, render anywhere, and feed AI agents native context. It is just Markdown plus YAML frontmatter, with a tiny conformance surface.

![Example OKF bundle](images/01-hero.png?v=20260622){: .hero-image }

<div class="cards">
  <div class="card">
    <h3>Only Markdown</h3>
    <p>A directory of <code>.md</code> files with YAML frontmatter. No runtime, no backend, no proprietary editor.</p>
  </div>
  <div class="card">
    <h3>Layered validation</h3>
    <p>Base conformance stays small; strict, link, and orphan checks add parseable warnings and info.</p>
  </div>
  <div class="card">
    <h3>Repo tooling</h3>
    <p>This repository ships a Go CLI toolkit and a local agent skill for creating, checking, analyzing, and exporting OKF bundles.</p>
  </div>
</div>

## Spec {#spec}

OKF represents knowledge as a tree of Markdown files:

```text
knowledge/
├── index.md
├── log.md
├── metrics/
│   ├── index.md
│   └── mrr.md
└── runbooks/
    └── incident-escalation.md
```

Reserved files:

| File | Purpose |
| --- | --- |
| `index.md` | Directory listing for progressive disclosure. |
| `log.md` | Chronological update history. |

Every other `.md` file is a **concept document**:

```markdown
---
type: Metric
title: Monthly Recurring Revenue
description: Normalized monthly recurring subscription revenue.
tags: [revenue, saas]
timestamp: 2026-06-13T10:00:00Z
---

# Definition

MRR is the sum of active subscription revenue normalized to one month.

# Related

- [Churn Rate](./churn.md) affects MRR directly.
```

The only required frontmatter field is `type`. Common optional fields are
`title`, `description`, `resource`, `tags`, and `timestamp`; `--strict` warns
on missing `title`, `description`, `tags`, and `timestamp`, while `resource` is
validated only when present. Additional producer-defined fields are allowed.

## Quickstart {#quickstart}

Create a small bundle:

```sh
mkdir saas-metrics
cd saas-metrics
```

Add `mrr.md`:

```markdown
---
type: Metric
title: MRR - Monthly Recurring Revenue
description: Normalized monthly recurring revenue for a SaaS business.
tags: [revenue, saas, finance]
timestamp: 2026-06-13T10:00:00Z
---

# Formula

MRR = Σ(active subscription monthly value)

# Related

- [Churn Rate](./churn.md) reduces MRR.
- [NPS](./nps.md) can be a leading indicator for future churn.
```

Add `index.md`:

```markdown
# SaaS Metrics Bundle

## Metrics

* [MRR](./mrr.md) - Normalized monthly recurring revenue for a SaaS business.
* [Churn](./churn.md) - customer or revenue loss over time
* [NPS](./nps.md) - recommendation score from -100 to 100
```

Return to the repository root and validate:

```sh
cd ..
go run ./cmd/okf validate -path ./saas-metrics
```

For a fuller review pass, enable the advisory modes:

```sh
go run ./cmd/okf validate -path ./saas-metrics --strict --check-links --check-orphans
```

Only `[ERROR]` diagnostics make the bundle non-conformant. `[WARN]` and
`[INFO]` are review signals for recommended metadata, conventional sections,
link targets, anchors, and local index coverage.

This quickstart intentionally leaves the linked `churn.md` and `nps.md`
concepts unwritten; `--check-links` reports those as `[INFO]` knowledge gaps.

## Examples {#examples}

Useful bundle shapes:

- **E-commerce analytics** - tables, metrics, dashboards, owners, freshness SLA, citations.
- **Incident playbooks** - alerts, runbooks, escalation rules, false positives, mitigation commands.
- **API documentation** - auth flows, endpoints, rate limits, operational caveats, real request examples.

Example API concept:

````markdown
---
type: API Endpoint
title: Create Order
description: Creates a new order. Requires scope orders:write.
resource: https://api.acme.com/v2/orders
tags: [orders, write, core]
method: POST
path: /v2/orders
auth_scope: orders:write
---

# Request

```bash
curl -X POST https://api.acme.com/v2/orders \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json"
```
````

## Tools {#tools}

This repository provides four public surfaces:

1. **CLI toolkit** - `cmd/okf`, used for validation, bundle summaries, index generation, graph output (`text`, Graphviz DOT, Mermaid, JSON-LD, N-Triples), parsing, and formatting.
2. **Go library** - domain packages `bundle`, `validator`, and `graph`, used for embedding OKF loading, validation, and graph export into Go programs.
3. **MCP server** - `cmd/okf-mcp`, used by MCP-capable agents to inspect, validate, graph, and safely edit local OKF bundles through stdio tools.
4. **Agent skill** - `skills/open-knowledge-format`, used by agents to create, convert, enrich, validate, and operate on OKF bundles.

Graph output has two layers: Markdown links for human navigation and YAML
`relations` for strict semantic dependencies. Relation targets are OKF concept
refs such as `tables/orders#col-status`; nested field-level sources require an
explicit `id` or `anchor`.

See [Toolkit](toolkit/) and [Skill](skill/) for the repo-local details,
including MCP setup.

## FAQ {#faq}

### Does OKF need a backend?

No. A bundle is files. Backend infrastructure is only needed if you build search, permissions, enrichment pipelines, or catalog UI on top.

### Does OKF replace OpenAPI, Protobuf, Avro, or data catalogs?

No. OKF references domain-specific schemas and catalogs. It carries contextual knowledge around them.

### Is OKF only for BigQuery?

No. `type` is free-form: `PostgreSQL Table`, `Kafka Topic`, `Metric`, `Runbook`, `API Endpoint`, and `Business Process` all work.

### What is the difference from AGENTS.md?

AGENTS.md tells a coding agent how to behave in a project. OKF tells an agent what exists in a domain: tables, metrics, APIs, playbooks, processes, and relationships.
