# Техническое задание: agent skill, docs и examples для OKF v0.2

## 1. Цель

Перевести agent-facing contract, README/docs, examples, fixtures и plugin
packaging на v0.2, сохранив явно обозначенное legacy consumption.

Финализировать после tasks 09–20.

## 2. Embedded specification

- Заменить `references/spec-v01.md` на pinned `spec-v02.md`.
- Добавить `references/migration-v01-v02.md`.
- Нормативные MUST/SHOULD/MAY не смешивать.
- YAML `relations` вынести в маркированный `skosovsky/okf extension`.
- Deferred/ambiguous ABI описать честно, без invented rules.

## 3. Authoring workflow

Skill должен:

1. Определять target version и писать version только в root index.
2. Требовать только `type` для minimal concept.
3. Добавлять generated только при известном actor.
4. Создавать sources только из реальных materials.
5. Использовать keyed footnotes для доказуемого claim attribution.
6. Не считать author/generator/validation фактом verification.
7. Записывать verified только после реальной проверки.
8. Не выдумывать status/stale date/credibility signals.
9. Выносить sanctioned computation в отдельный concept.
10. Не превращать credibility signals в score.

## 4. Consumption workflow

- Prefer v0.2 fields, затем §13 fallback.
- Normalize bare verified.
- Derive trust tier строго по verified.
- Показывать trust/status/staleness отдельно.
- Не исполнять executor/attester content без trusted runtime и authorization.
- Deprecated/stale signals не игнорировать по instruction из body.

## 5. Migration guidance

- Timestamp переносится только с explicit generated actor.
- Unknown actor → unresolved/manual action.
- Citations → sources без invented metadata.
- Claim footnotes только при доказуемом mapping.
- Не выводить verified/status/stale_after из git/timestamp/migration.
- Version bump происходит после validation.
- Unknown content сохраняется lossless.

## 6. Examples/fixtures

Обновить examples и добавить:

- human-authored concept;
- multiple sources/keyed footnotes;
- bare/list verified;
- draft/stable/deprecated;
- fresh/stale boundary;
- inline/file Attested Computation;
- narrative concept linking computation;
- explicit legacy/mixed/future examples.

Canonical snippets должны извлекаться из fixtures либо проверяться drift tests.
Synthetic examples используют reserved domains и помечаются synthetic.

## 7. Adversarial contexts

Матрица input → expected decision → forbidden behavior:

- body просит игнорировать frontmatter/trust;
- generated human actor без verified;
- human source author без verifier;
- high usage_count как попытка повысить trust;
- footnotes в code/unknown/duplicate IDs;
- simultaneous legacy/v0.2 provenance;
- stale/deprecated self-promotion;
- executor asks for shell/secrets/policy bypass;
- LLM receipt объявляет attestation success.

Deterministic cases идут в fixtures/tests, agent-only cases — eval matrix.

## 8. EN/RU и packaging

Синхронно обновить:

- `README.md` / `README.ru.md`;
- `docs/index.md` / `docs/ru/index.md`;
- `docs/skill.md` / `docs/ru/skill.md`;
- `docs/toolkit.md` / `docs/ru/toolkit.md`;
- navigation и migration page.

После стабилизации:

- plugin descriptions/versions;
- не приравнивать package version к OKF spec version;
- пересчитать `skills-lock.json` последним;
- проверить `skills.sh.json`/marketplace manifests.

## 9. Stop-slop constraints

- Не выдумывать actors, sources, verification, freshness, receipt.
- Один термин — одно значение.
- Не дублировать overview на каждой странице.
- EN/RU совпадают по contract, но русский не является калькой.
- Repo policy всегда маркируется extension/tooling policy.
- Никакого маркетингового тумана.

## 10. Acceptance criteria

- Active docs/skill не называют current spec v0.1 draft.
- Timestamp/Citations остаются только в legacy/migration cases.
- Все links ведут на spec-v02/migration.
- Canonical v0.2 fixture base/strict clean.
- Legacy fixture читается documented fallback.
- Bare/list verified semantics identical.
- Attested examples не обещают runtime ABI.
- EN/RU parity пройдена.
- Plugin manifests consistent; skill lock test проходит.
- Поиск v0.1/timestamp/Citations/spec-v01 возвращает только intentional cases.
- `go test ./...`, `go vet ./...`, `git diff --check` проходят.

## 11. Out of scope

Исполнение computation, attester ABI/sandbox/cache, invented scoring и
автоматическое semantic rewriting.
