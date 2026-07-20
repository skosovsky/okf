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
2. **Go library** - domain packages `bundle`, `validator`, `graph`, `store` и `store/fs`, включая snapshot-based transactional mutation для local bundles.
3. **MCP server** - `cmd/okf-mcp` для MCP-capable agents: читать, проверять, строить graph и безопасно редактировать local OKF bundles через stdio tools. `write_concept` использует staged validation и durable cooperating-writer commit path.
4. **Agent skill** - `skills/open-knowledge-format` для создания, конвертации, обогащения, проверки и работы с OKF bundles.

Graph output имеет два слоя: Markdown links для human navigation и YAML
`relations` для строгих semantic dependencies. Targets в `relations` - OKF
concept refs вроде `tables/orders#col-status`; для nested field-level sources
нужен явный `id` или `anchor`. Target с fragment существует, только если
существуют его concept и один уникальный matching fragment.

Некорректные или неразрешенные semantic relations остаются structured
diagnostics. Baseline validation в CLI и MCP ограничен conformance v0.1;
Go-клиент может включить relation reporting через `ValidatorConfig.CheckRelations`,
а mutation и write paths всегда отклоняют blocking relation diagnostics. Они
исключаются из resolved outgoing, incoming и reverse indexes,
а также из всех semantic graph exporters. Dangling Markdown links — отдельный
навигационный слой и могут по-прежнему отображаться как missing.

Только nested mapping определяют fragments; top-level frontmatter `id` и
`anchor` — metadata concept. Для nested mapping `id` canonical. Отличающийся валидный `anchor` —
noncanonical alias для информации и навигации; semantic edits и relation refs
используют canonical `id`.

Transactional API использует algorithm-qualified revisions, preview и CAS
commits. По умолчанию revision — `sha256:<lowercase-hex>`;
`fs.Config.HashAlgorithm` может заменить алгоритм. Она включает каждый regular
file под bundle root, включая non-Markdown и reserved index/log files, кроме
`.okf/**`; symlinks никогда не читаются и не хешируются, а internal journal,
receipts и lease исключены. Journal v5 фиксирует алгоритм и canonical
request/result/replay data в compact bounded manifest; durable staged payloads
хранят post-state bytes. Перед apply recovery проверяет safe no-follow path,
declared size и SHA-256 digest каждого payload, затем очищает данные и требует
ту же configured algorithm. Persisted receipt envelope использует v2. Filesystem backend
имеет advisory locks и scope одного filesystem. MCP `write_concept` использует
тот же idempotent pipeline без повторной публикации идентичного retry, но
сохраняет fixed success schema (`status`, `path`, `diagnostics`) и не раскрывает
receipt или commit evidence. API,
idempotency и ограничения — в [Toolkit](toolkit/).

Parser-backed mutations сохраняют presentation вместо нормализации: Goldmark
доказывает eligible Markdown body destinations, а exact collector отображает
их byte spans в полный файл; `yaml.v3` доказывает supported block scalar subset
во frontmatter. Autolinks, raw HTML, invalid UTF-8 и unsupported или ambiguous
presentation fail-closed, а не переписываются. Flat overlay делит immutable
staged payloads и результат только успешного `Paths` discovery; HAMT и parent
chain отсутствуют. `bundle.SourceFromFS` — non-owning adapter и требует stable
snapshot `fs.FS`. Эти внутренние детали сохраняют CLI и MCP schemas.

Граница edit точная: parsing идёт только по body, а collector offsets
отображаются в полный файл. Переписываются только Goldmark AST inline
links/images и reference definitions; semantic link/image внутри inline-HTML
container остаётся eligible, если Goldmark создаёт `Link`/`Image`. Autolinks,
raw-HTML `href`/URLs, code spans, fenced/indented code и unresolved/malformed
references исключены. Touched YAML принимает доказанные plain/single/double-
quoted scalar keys и values и fail-closed для flow mapping/sequence,
literal/folded block scalar, explicit/custom tag, direct anchor или complex key
(Unsupported); alias/merge provenance и duplicate semantic
relations/type/target/id/anchor (Ambiguous); остальные duplicate touched mapping
keys (Unsupported); `%YAML`/`%TAG`, inner
document/end markers или multidoc frontmatter и
unprovable comment/range; unrelated nonintersecting extension bytes могут
остаться. Invalid UTF-8 и любой unsupported/ambiguous случай возвращают typed
error и не создают stage. YAML задают только recognized outer frontmatter
delimiters: body thematic `---` и setext underline — Markdown, не YAML multi-doc.

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

Filesystem durability: `MaxStagedFiles` по умолчанию и максимум 100 000 (`payload-00000`…`payload-99999`). Case-folding и Unicode-normalization aliases определяются независимо. `.okf` directories no-follow 0700, private files и lease 0600 либо Open fail-closed.
