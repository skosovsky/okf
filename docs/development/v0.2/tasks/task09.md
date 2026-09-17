# Техническое задание: OKF v0.2 — contract baseline и migration roadmap

## 1. Цель

Зафиксировать исполняемый контракт поддержки Open Knowledge Format v0.2 до
изменения публичных API. Этот task является dependency root для остальных
задач миграции.

Source of truth:

- upstream `okf/SPEC.md`, commit
  `3fcbb9f828c2f23d109c855ee403c3a4c81f3a96`;
- нормативные разделы §§1–13;
- Appendix A как canonical end-to-end example.

Нельзя ориентироваться только на текущий `main`: повторяемая сборка и tests
должны ссылаться на зафиксированную ревизию спецификации.

## 2. Version contract

Поддерживаемые версии:

- `0.1` — legacy read/validate/migration source;
- `0.2` — current default read/write contract;
- неизвестная будущая версия — best-effort consumption без отказа только из-за
  значения `okf_version`.

Resolution policy:

1. Явный поддерживаемый `okf_version` в root `index.md` задаёт contract.
2. При отсутствии declaration effective version — `0.2`, но разрешены legacy
   fallbacks из §13.
3. Неизвестная declaration читается v0.2 consumer'ом best-effort и остаётся
   видимой как declared version.
4. Явный selector API/CLI является assertion. Конфликт с declaration не должен
   молча менять semantics.

Модель resolution должна различать:

- declared version;
- effective version;
- source: `declared`, `default`, `explicit`, `future-best-effort`;
- native/legacy/best-effort compatibility.

Версия Go module, CLI binary и plugin package не является `okf_version`.

## 3. Conformance matrix

Создать versioned matrix `spec clause → implementation → diagnostics → tests`.

Hard `[ERROR]` остаются ограничены §11:

- parseable UTF-8 Markdown/YAML frontmatter для concepts;
- непустой string `type`;
- структура reserved `index.md` и `log.md`.

Optional v0.2 families не становятся hard requirements:

- `sources`, `usage_window`;
- `generated`, `verified`;
- `status`, `stale_after`;
- computation fields.

Malformed присутствующая family проверяется strict/policy layer и не должна
ломать permissive loading. Unknown keys/types/runtimes остаются consumable.

## 4. Legacy contract

Поддержать два fallback без скрытой миграции:

- `timestamp` используется только когда `generated` полностью отсутствует;
- `# Citations` читается только когда `sources` отсутствует.

Наличие legacy representation само по себе не является conformance failure.
Чтение, `fmt`, `index` и graph не должны переписывать v0.1 документы.

Миграция должна быть отдельной preview/apply операцией с explicit actor и source
mapping.

## 5. Ambiguity ledger

Зафиксировать решения tooling policy отдельно от upstream specification:

- `sources[].author` ссылается на actor convention, но upstream examples
  используют неописанный `team:*`; валидировать как non-empty string;
- grammar и uniqueness `sources[].id` не определены; uniqueness является
  toolkit strict policy для безопасного attribution join;
- `sources[].resource` может быть scope descriptor, а не path/URI;
- timezone для `today >= stale_after` не определена; tooling принимает explicit
  reference date, CLI default документируется отдельно;
- `usage_count` не является score;
- `runtime` и parameter types не имеют registry;
- обязательность executor/attester слабее `runtime` и остаётся strict guidance;
- simultaneous legacy/v0.2 provenance не имеет upstream precedence/dedup rule;
- YAML `relations` — extension `skosovsky/okf`, не часть upstream v0.2.

Каждое решение должно быть отражено одинаково в bundle, validator, CLI, MCP,
fixtures и docs.

## 6. Canonical fixtures

Добавить один общий corpus:

- minimal v0.2 concept только с `type`;
- полный Appendix A;
- inline и file-backed Attested Computation;
- bare/list `verified`;
- v0.1 declared и undeclared;
- mixed legacy/v0.2;
- unknown future version;
- adversarial shapes и unknown extensions.

Fixtures должны переиспользоваться package tests, CLI/MCP E2E и docs snippets.

## 7. Deferred ABI

Явно исключить:

- executor runtime;
- parameter binding implementation;
- receipt/verdict wire format;
- attester ABI, portability и sandbox;
- attestation caching;
- выполнение bundle content.

`store.CommitReceipt` не связан с `executor.receipt`.

## 8. Acceptance criteria

- Contract и ambiguity ledger зафиксированы в versioned documentation.
- Есть clause-level conformance matrix и canonical fixture corpus.
- Все downstream tasks ссылаются на одинаковую version policy.
- v0.1 fallback и future best-effort покрыты tests.
- Ни один task не выдумывает deferred runtime ABI.
- `go test ./...` и `git diff --check` проходят после добавления contract corpus.

## 9. Dependency graph

Порядок:

1. Этот task.
2. `bundle`.
3. Параллельно: `validator`, `graph`, mutation engine, store guardrails.
4. Mutation operations и migration.
5. CLI и MCP.
6. Skill/docs и финальный cross-surface conformance gate.
