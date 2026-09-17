# Техническое задание: generalized lossless YAML engine для v0.2

## 1. Цель

Расширить приватный mutation engine структурными mapping/sequence primitives,
не ослабляя fail-closed и byte-preservation contract.

Зависимости: [`task09.md`](task09.md), [`task10.md`](task10.md).

## 2. Gap

Текущий engine ориентирован на `relations`, `id`, `anchor` и string scalars.
OKF v0.2 использует nested mappings/sequences, bool/int/date/datetime и flow
forms. Touched flow nodes сейчас полностью отвергаются.

Flow style не надо патчить изнутри: разрешается атомарная замена целого
доказанного collection value.

## 3. Приватные primitives

Добавить:

- replace/insert/delete root mapping entry;
- replace/insert/delete nested mapping entry;
- whole-value replacement mapping/sequence;
- ensure/update/remove sequence item по unique selector;
- bare mapping → one-element sequence;
- exact span ownership mapping entry/collection value;
- render string/bool/int/date/datetime/mapping/sequence;
- semantic reparse и проверку неизменности bytes вне patch spans.

Selector routes:

- `sources[]` по `id`, entry без ID только exact-value;
- `verified[]` по `(by, at)`;
- `parameters[]` по `name`;
- nested mapping по unique scalar key.

Generic raw YAML path не должен становиться public API.

## 4. Fail-closed contract

- Duplicate touched keys/selectors → `Ambiguous`.
- Alias/merge provenance → `Ambiguous`.
- Anchor/tag/complex key/unowned trivia → `Unsupported`.
- Недоказуемый comment ownership → `Unsupported`.
- Nested flow patch allowed только whole-value.
- Unrelated YAML/Markdown остаётся byte-identical.
- Любая ошибка создаёт zero stage/writes/renames/plan.

Добавить stable error codes и in-bounds source locations.

## 5. Файлы

- `mutation/presentation.go`;
- `mutation/yaml_resolver.go`;
- `mutation/presentation_errors.go`;
- focused tests, parser-backed corpus и fuzz corpus.

## 6. Tests

Все tests — AAA.

- Insert/update/delete mapping entries.
- Block и whole-value flow forms.
- Bare/list sequence normalization.
- String/bool/int/date/datetime.
- LF/CRLF, comments, quotes.
- Duplicate keys/selectors, aliases, merges, anchors, tags, complex keys.
- Invalid UTF-8, nested flow, mixed sequences.
- Caller/source bytes не мутируются.
- Bytes outside spans identical.
- Reparsed semantic projection exact.
- Rejection содержит typed error/location и zero public plan.
- Desired-state second application creates zero diff.
- Fuzz: no panic, spans in bounds, parseable result.
- Race: concurrent planning и caller mutation.

## 7. Acceptance criteria

- Upstream flow examples можно менять whole-family replacement.
- Ни один существующий relation/move/rename digest или semantic contract не
  изменился.
- Lossless guarantees доказаны property/fuzz tests.
- `go test -race ./mutation ./store` проходит.

## 8. Out of scope

Public v0.2 operations, semantic migration, executor runtime и generic
caller-controlled `SetYAML`.
