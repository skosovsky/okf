---
title: OKF - Markdown-спецификация для людей и AI-агентов
description: Русская документация Open Knowledge Format, CLI toolkit и agent skill.
permalink: /ru/
---

{% include nav_ru.html %}

# Open Knowledge Format

OKF - маленький vendor-neutral формат для баз знаний, которые живут в git,
рендерятся где угодно и дают AI-агентам структурный контекст. Под капотом это
Markdown + YAML frontmatter, компактный base conformance и advisory-режимы для
строгого review.

![Пример OKF bundle](../images/01-hero.png?v=20260622){: .hero-image }

<div class="cards">
  <div class="card">
    <h3>Только Markdown</h3>
    <p>Директория <code>.md</code> файлов с YAML frontmatter. Без runtime, backend и проприетарного редактора.</p>
  </div>
  <div class="card">
    <h3>Слоистая валидация</h3>
    <p>Base conformance остается маленьким; strict, link и orphan checks добавляют parseable warnings и info.</p>
  </div>
  <div class="card">
    <h3>Инструменты repo</h3>
    <p>В этом репозитории есть Go CLI toolkit и local agent skill для создания, проверки, анализа и экспорта OKF bundles.</p>
  </div>
</div>

## Spec {#spec}

OKF представляет знания как дерево Markdown-файлов:

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

| Файл | Назначение |
| --- | --- |
| `index.md` | Directory listing для progressive disclosure. |
| `log.md` | Chronological update history. |

Все остальные `.md` файлы - **concept documents**:

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

Единственное обязательное поле frontmatter - `type`. Common optional fields:
`title`, `description`, `resource`, `tags` и `timestamp`; `--strict` дает
warning при отсутствии `title`, `description`, `tags` и `timestamp`, а
`resource` проверяется только если присутствует. Дополнительные
producer-defined fields разрешены.

## Быстрый старт {#quickstart}

Создай маленький bundle:

```sh
mkdir saas-metrics
cd saas-metrics
```

Добавь `mrr.md`:

```markdown
---
type: Metric
title: MRR - Monthly Recurring Revenue
description: Нормализованная monthly recurring revenue для SaaS.
tags: [revenue, saas, finance]
timestamp: 2026-06-13T10:00:00Z
---

# Formula

MRR = Σ(active subscription monthly value)

# Related

- [Churn Rate](./churn.md) снижает MRR.
- [NPS](./nps.md) может быть leading indicator будущего churn.
```

Добавь `index.md`:

```markdown
# SaaS Metrics Bundle

## Metrics

* [MRR](./mrr.md) - Нормализованная monthly recurring revenue для SaaS.
* [Churn](./churn.md) - customer or revenue loss over time
* [NPS](./nps.md) - recommendation score from -100 to 100
```

Вернись в корень репозитория и проверь:

```sh
cd ..
go run ./cmd/okf validate -path ./saas-metrics
```

Для полного review-pass включи advisory-режимы:

```sh
go run ./cmd/okf validate -path ./saas-metrics --strict --check-links --check-orphans
```

Только diagnostics уровня `[ERROR]` делают bundle non-conformant. `[WARN]` и
`[INFO]` - это review-сигналы для recommended metadata, conventional sections,
link targets, anchors и local index coverage.

Этот quickstart намеренно оставляет linked concepts `churn.md` и `nps.md`
ненаписанными; `--check-links` покажет их как `[INFO]` knowledge gaps.

## Примеры {#examples}

Рабочие формы bundle:

- **E-commerce analytics** - tables, metrics, dashboards, owners, freshness SLA, citations.
- **Incident playbooks** - alerts, runbooks, escalation rules, false positives, mitigation commands.
- **API documentation** - auth flows, endpoints, rate limits, caveats, examples.

Пример API concept:

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

## Инструменты {#tools}

В этом репозитории четыре публичные поверхности:

1. **CLI toolkit** - `cmd/okf` для validation, summaries, index generation, graph output (`text`, Graphviz DOT, Mermaid, JSON-LD, N-Triples), parsing и formatting.
2. **Go library** - domain packages `bundle`, `validator` и `graph` для встраивания OKF loading, validation и graph export в Go.
3. **MCP server** - `cmd/okf-mcp` для MCP-capable agents: читать, проверять, строить graph и безопасно редактировать local OKF bundles через stdio tools.
4. **Agent skill** - `skills/open-knowledge-format` для создания, конвертации, обогащения, проверки и работы с OKF bundles.

Graph output имеет два слоя: Markdown links для human navigation и YAML
`relations` для строгих semantic dependencies. Targets в `relations` - OKF
concept refs вроде `tables/orders#col-status`; для nested field-level sources
нужен явный `id` или `anchor`.

Смотри отдельные разделы: [Toolkit](toolkit/) и [Skill](skill/), включая MCP setup.

## FAQ {#faq}

### Нужен backend?

Нет. Bundle - это файлы. Backend нужен только если ты строишь поиск, permissions, enrichment pipeline или catalog UI поверх формата.

### OKF заменяет OpenAPI, Protobuf, Avro или data catalogs?

Нет. OKF ссылается на domain-specific schemas и catalogs. Он хранит контекст вокруг них.

### Это только для BigQuery?

Нет. `type` свободный: `PostgreSQL Table`, `Kafka Topic`, `Metric`, `Runbook`, `API Endpoint`, `Business Process`.

### Чем отличается от AGENTS.md?

AGENTS.md говорит coding agent, как вести себя в проекте. OKF говорит агенту, что существует в домене: tables, metrics, APIs, playbooks, processes и relationships.
