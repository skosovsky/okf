---
title: Open Knowledge Format v0.2
description: Формат OKF v0.2, toolkit, migration и agent workflow.
permalink: /ru/
---

{% include nav_ru.html %}

# Open Knowledge Format v0.2

OKF хранит переносимые знания как Markdown concepts с YAML frontmatter. Этот
репозиторий реализует v0.2 contract для чтения и записи, сохраняя explicit v0.1
consumption и migration.

![Пример OKF bundle](../images/01-hero.png?v=20260729){: .hero-image }

## Spec {#spec}

Нормативный contract — [pinned upstream specification](https://github.com/skosovsky/okf/blob/main/skills/open-knowledge-format/references/spec-v02.md)
из commit `3fcbb9f828c2f23d109c855ee403c3a4c81f3a96`.

Base conformance намеренно компактный:

1. Каждый concept — UTF-8 Markdown с parseable YAML frontmatter.
2. В каждом concept есть непустой string `type`.
3. Reserved `index.md` и `log.md` соблюдают заданную структуру.

Provenance, trust, lifecycle и Attested Computation fields optional. Missing
optional data и unknown keys/types/runtimes остаются consumable.
При отсутствии root version допустим v0.2 traversal default. Present malformed
`okf_version` нарушает reserved-index conformance; unsupported canonical future
versions сохраняют declaration и читаются best-effort.

## Быстрый старт {#quickstart}

```text
knowledge/
├── index.md
└── minimal.md
```

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

Проверка:

```sh
okf validate --path ./knowledge --spec auto
```

Для review используй strict guidance и deterministic staleness date:

```sh
okf validate --path ./knowledge --spec auto --strict --as-of 2026-07-29
```

Validation не является verification. Добавляй `verified` только после проверки
concept относительно его sources или resource.

## Provenance, trust и lifecycle

`sources` хранит actual materials. Claims в body используют Markdown footnotes
с labels из `sources[].id`. `generated` называет известного producer текущего
content; `verified` хранит отдельные проверки.

Consumer выводит trust только из `verified`:

- key отсутствует — unverified;
- только non-human verifiers — machine-confirmed;
- есть `human:<id>` verifier — human-reviewed.

Trust, `status` и staleness — разные сигналы. Deprecated concept может быть
human-reviewed; stable concept — unverified; verified concept — stale.

## Attested Computation

Sanctioned computation — отдельный concept `type: Attested Computation`, на
который ссылаются narrative concepts. Computation задаётся либо одним inline
fence под `# Computation`, либо одним file path.

Contract inert. Toolkit не исполняет resource без отдельно trusted runtime и
authorization. v0.2 намеренно откладывает binding, runtime packaging,
receipt/verdict wire format, attester ABI, sandboxing и caching.

По pinned spec agent MAY передавать только значения declared parameters и MUST
NOT создавать или редактировать sanctioned computation.

## Примеры {#examples}

Canonical examples парсятся из
[`fixtures/v02/corpus.yaml`](https://github.com/skosovsky/okf/blob/main/fixtures/v02/corpus.yaml). Corpus покрывает
minimal, Appendix A, verification shapes, lifecycle, inline/file computation,
narrative links, v0.1 compatibility, mixed provenance, future version и
adversarial cases.

Синтетические URL этого repo используют reserved domains вроде
`example.invalid`.

## Инструменты {#tools}

- [Toolkit]({{ '/ru/toolkit/' | relative_url }}): CLI и Go surfaces.
- [Agent skill]({{ '/ru/skill/' | relative_url }}): authoring, consumption и guardrails.
- [Migration]({{ '/ru/migration/' | relative_url }}): explicit v0.1 → v0.2 workflow.

YAML `relations` — `skosovsky/okf` extension/tooling policy, а не часть upstream
v0.2 spec.

## FAQ {#faq}

### Каждому concept нужен provenance?

Нет. `type` — единственное всегда обязательное поле. Отсутствующий provenance
означает unknown provenance, а не invalid concept.

### Human-authored concept автоматически human-reviewed?

Нет. `generated.by: human:...` фиксирует authoring. Human-reviewed требует
реального human event в `verified`.

### Bundle может запускать executor или attester?

Не потому, что это OKF. Resource fields — inert data. Execution требует trusted
runtime и authorization за пределами format/toolkit.

### Что происходит с v0.1?

Он остаётся intentional read/validate/migration source. Legacy fallback не
выполняет hidden migration. См. [migration guide]({{ '/ru/migration/' | relative_url }}).
