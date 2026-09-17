# Техническое задание: согласовать journal decoder с payload limits

## 1. Проблема

`Config.Validate` допускает enlarged limits до absolute ceilings, writer и
staged reader используют effective Config, но journal decoder hardcode'ит
default 256 MiB/1 GiB. Валидная large-asset transaction может commit'иться, но
не восстановиться после crash с той же конфигурацией.

Проблема обнаружена при аудите v0.2 computation assets, но является общим
durability bug.

## 2. Требования

Выбрать и зафиксировать один contract:

1. Decoder проверяет manifest против immutable absolute ceilings без payload
   I/O, затем `readStagedJournal` применяет effective Config; либо
2. Decoder принимает явно переданные immutable effective limits.

Нельзя:

- ослаблять overflow/ordinal/tamper checks;
- читать payload до manifest/provenance/limit validation;
- менять journal v5 wire format без необходимости;
- аллоцировать сотни MiB только ради test fixture.

## 3. Файлы

- `store/fs/fs.go`;
- `store/fs/staged_payload_limits_test.go`;
- focused recovery integration test.

## 4. Tests

Все tests — AAA.

- Manifest выше defaults, но ниже absolute ceiling, проходит decoder stage.
- Smaller reopen config rejects before payload I/O.
- Same enlarged config recovers successfully.
- Aggregate/size overflow rejected.
- Ordinal/tamper/missing payload contracts не регрессируют.
- Bounded unit fixture вместо реального 256+ MiB allocation.

## 5. Acceptance criteria

- Writer и recovery принимают одинаковую валидную configured transaction.
- Smaller policy безопасно блокирует recovery до payload read.
- Journal wire version unchanged.
- `go test ./store/fs` проходит.
