# Техническое задание: version-aware validator OKF v0.2

## 1. Цель

Добавить version-aware base/strict policy для OKF v0.1 и v0.2 без расширения
hard conformance сверх §11.

Зависимости: [`task09.md`](task09.md), [`task10.md`](task10.md).

## 2. Version policy

| Target | Base | Strict |
| --- | --- | --- |
| `0.1` | существующий contract | legacy `timestamp`/`# Citations` |
| `0.2` | §11 | v0.2 families/computation guidance |
| absent | v0.2 + §13 fallbacks | validate present families |
| future | best-effort | только безопасный известный subset |

Version resolution должен быть shared domain API, а не локальная эвристика
validator.

## 3. Severity contract

`ERROR`:

- invalid/missing concept frontmatter;
- missing/empty string `type`;
- invalid reserved file structure.

`WARNING` только strict/policy:

- malformed присутствующая v0.2 family;
- source-footnote integrity;
- malformed lifecycle/freshness;
- unusable Attested Computation contract;
- stale boundary.

Missing optional families, unknown type/key/runtime, broken resource/path не
являются conformance errors.

Каждая новая диагностика получает stable `Code`, file и точный field path
вроде `sources[2].resource`.

## 4. Strict families

### Sources

- `sources` — sequence mappings.
- `resource` required и non-empty scalar в каждом entry.
- `id`, `title`, `author` проверять при наличии.
- IDs уникальны как toolkit attribution policy.
- `usage_count` — non-negative integer toolkit profile.
- Effective `usage_window` имеет `from/to`, valid dates и `from <= to`.
- `usage_count` требует effective window.
- Source footnotes join по ID; code fences игнорируются.
- `sources[].resource` может быть scope descriptor.
- `sources[].author` проверять только как non-empty string из-за upstream
  `team:*` ambiguity.

### Generated/verified

- `generated` mapping; `by` required внутри присутствующего mapping;
  `at`, если есть, RFC3339.
- Actor convention применять к `generated.by`/`verified.by`.
- `verified` принимает mapping и sequence.
- Каждое событие содержит `by/at`; bare mapping нормализуется.
- Trust tier derived, never stored.
- Legacy timestamp fallback только при полном отсутствии `generated`.

### Lifecycle

- status: `draft|stable|deprecated`, absent → stable.
- `stale_after`: valid `YYYY-MM-DD`.
- Reference date инъецируется; `today == stale_after` означает stale.

### Path-valued fields

Поддержать absolute URL, bundle-relative и relative path для:

- `resource`;
- `computation`;
- `executor.resource`;
- `attester.resource`.

Не проверять сеть/существование target как conformance requirement.

## 5. Attested Computation

Для exact type:

- `runtime` non-empty, unknown values allowed;
- parameters — mappings `{name,type,required}`, unique names, boolean required;
- computation задан inline либо path, но не обоими;
- inline mode — один fence под top-level `# Computation`;
- executor/attester shapes проверяются при наличии;
- receipt — unique non-empty field names.

Missing runtime/computation/executor/attester остаются strict warnings, не base
errors. Deferred ABI не валидировать.

## 6. Legacy fallback

- Explicit v0.1 сохраняет старые strict rules.
- v0.2 consumer читает timestamp/Citations только при отсутствии replacement.
- Numeric `[1]` в explicit v0.2 не включает legacy policy автоматически.
- Fallback не переписывает document и не создаёт warning только за legacy form.

## 7. Fixtures/tests

Добавить groups `v02/positive`, `v02/strict`, `v02/compat`,
`v02/adversarial`.

Покрыть:

- minimal type-only concept;
- Appendix A;
- mapping/list verified;
- local/relative/scope resources;
- malformed shape каждой family;
- duplicate IDs/orphan footnotes/code fences;
- actor cases;
- invalid timestamps/dates/windows;
- stale before/equal/after;
- parameter/computation variants;
- declared 0.1, absent, 0.2, future;
- YAML implicit timestamps/dates;
- malformed optional family → warning, `ErrorCount == 0`.

Все tests — AAA, output deterministic.

## 8. Acceptance criteria

- Official v0.2 examples base-conformant.
- Canonical Appendix fixture strict-clean.
- v0.1 regression fixtures проходят.
- Type-only document остаётся conformant.
- Bare verified equals one-item list.
- Нет false warning по missing `timestamp` в v0.2.
- Codes/field paths стабильны.
- `go test ./validator ./bundle` и `go test ./...` проходят.

## 9. Out of scope

Execution, receipt/verdict ABI, attester runtime/sandbox/cache, runtime-specific
binding, network checks и migration.
