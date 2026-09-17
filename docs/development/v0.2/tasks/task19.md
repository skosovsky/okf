# Техническое задание: CLI `okf` для OKF v0.2

## 1. Цель

Сделать все CLI read/write surfaces version-aware, показать v0.2 signals и
подключить единый migration planner без domain logic в `main.go`.

Зависимости: [`task09.md`](task09.md)–[`task15.md`](task15.md).

## 2. Архитектура

`cmd/okf/main.go` оставить bootstrap. Вынести command orchestration в
`internal/okfcli`:

- dispatch/args/help/version;
- validate/info/parse/fmt/index/graph/migrate;
- stable text/JSON DTO/renderers.

Version resolution, trust, staleness, provenance, graph и migration приходят
из domain packages. CLI не traverses raw YAML.

## 3. Shared selector

Для bundle commands:

```text
--spec auto|0.1|0.2
```

Default `auto`; resolution следует task09. Explicit selector конфликтующий с
declaration является failure/assertion mismatch, а не silent override.

## 4. Commands

### `version`

Text сообщает CLI version отдельно от supported/default OKF specs.
Добавить `--json`:

```json
{
  "cli_version": "vX.Y.Z",
  "okf_spec_default": "0.2",
  "okf_spec_supported": ["0.1", "0.2"]
}
```

### `validate`

Сохранить существующие flags/output fields, additive добавить:

- declared/effective version;
- resolution/compatibility;
- explicit `--as-of YYYY-MM-DD` для strict staleness;
- stable diagnostic codes/field paths.

v0.2 не получает timestamp/Citations false positives.

### `info`

Добавить deterministic counts:

- trust tiers;
- lifecycle status;
- stale at `--as-of`;
- sources;
- Attested Computation;
- parse errors;
- declared/effective version.

### `parse`

Text/JSON typed projection:

- generated/verified/trust;
- status/staleness;
- sources/attributions;
- legacy fallback marker;
- computation contract summary.

Bare/list verified дают одинаковый result.

### `fmt`

Остаётся parse + canonical serialization:

- не мигрирует timestamp/Citations;
- сохраняет unknown keys;
- не добавляет generated/version;
- no hidden write без `-w`.

### `index`

- сохраняет 0.1/0.2/future root version;
- не stamp'ит и не bump'ит version;
- nested indexes без frontmatter.

### `graph`

Передаёт explicit projection profile/options package `graph`; stdout содержит
только selected graph payload. Legacy `--dot` сохраняется.

### `migrate`

```text
okf migrate <bundle> --to 0.2
  [--from auto|0.1]
  [--actor <actor>]
  [--write]
  [--format text|json]
```

Default dry-run. Делегирует task15 planner, не выполняет собственные YAML/file
edits. Apply использует revision/digest-bound atomic store commit.

## 5. stdout/stderr/exit

- `0`: successful command, conformant validation, valid dry-run/apply.
- `1`: conformance failure, migration blocker, usage/operational error.
- Reports/payload → stdout.
- Usage/I/O errors → stderr с `error:`.
- JSON mode → один JSON document без banners/ANSI.
- Successful command не пишет stderr.
- Migration dry-run с diff возвращает `0`.

## 6. Tests

Все tests — AAA.

- Version text/JSON.
- Declared 0.1/0.2, absent, future, explicit conflict.
- Strict v0.2 без timestamp warning.
- Legacy fallback.
- Trust/status/stale boundary.
- Deterministic text/JSON.
- JSON stdout/stderr purity.
- v0.2 fmt semantic round-trip + unknown keys.
- Index preserves versions.
- Graph profile selection.
- Flag order, `--`, unknown/duplicate/extra args.
- Migration dry-run/noop/conflict/one-bad-file/atomic recovery.
- Binary E2E over canonical fixtures.

## 7. Acceptance criteria

- CLI честно сообщает supported/default specs.
- Все bundle commands используют один resolution contract.
- v0.1 остаётся consumable.
- info/parse показывают v0.2 signals.
- fmt/index не выполняют hidden migration.
- migrate transactional/idempotent.
- `main.go` не содержит domain logic.
- `go test ./...` и binary E2E проходят.

## 8. Out of scope

Execution/attestation runtime, LLM conversion arbitrary SQL/prose и изменение
custom `relations`.
