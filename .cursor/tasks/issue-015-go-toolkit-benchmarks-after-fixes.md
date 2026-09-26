# 015 — Benchmarks Go toolkit до и после исправлений

Статус: backlog. Приоритет: P2. Зависимости: 004/005 для итогового corrected run. Запрос пользователя: измерить нашу реализацию после исправлений, а не только перенести методику.

## Цель

Получить воспроизводимые измерения скорости и памяти Go toolkit на representative OKF bundles. Не смешивать performance с истинностью ответа LLM: поведенческие эксперименты выполняются в 012.

## Контракт измерения

- Стандартные Go `Benchmark*`, `testing.B`, `ReportAllocs`, без Python/Node harness. Использовать Go analysis tooling (например benchstat) с pinned version.
- Baseline `ed7ddc28cd127682023bd150ee377906b817e099` и конкретный corrected commit после 004/005; одинаковые Go/toolchain, машина, параметры и corpus bytes.
- Перед таймингом assert корректность ожидаемого результата. Старый validator, быстро отвергший корректный log, не является более эффективной реализацией корректной задачи. Его число можно показать отдельно как historical behavior.
- Generated corpus на 10/100/1000/10000 concepts, fixed seed, размеры bytes/edges/источников/line wraps записаны в manifest. Не включать генерацию fixtures в hot path.
- Отдельные измерения parsing, validation base/strict/links/orphans, typed log read, source-footnote ownership и graph projection. CLI end-to-end отдельно от library calls и compilation; benchmark не должен измерять `go run` build.
- Изменение temporal SPEC отдельно от parser bug fix: legacy date cases, offset datetime cases, equal instants/different offsets; сравнивать только одинаковые поддерживаемые semantics и обозначать unsupported baseline cases.
- Добавить workload с wrapped log и code-owned footnotes из 005 и snapshot-based traversal без обращения к сети. Foreign-derived fixtures pinned, licensed, минимальны.

## Проверка и отчёт

- [ ] Корректность workload проверена AAA-тестами; expected diagnostics заданы по контракту, не числам foreign checker.
- [ ] Выполнено не менее пяти повторов выбранных benchmark suites; опубликованы ns/op, B/op, allocs/op, corpus dimensions, Go/OS/CPU и сырые результаты.
- [ ] Приведены отдельные результаты baseline/corrected, delta и разброс. Невалидные для сравнения cases выделены, не скрыты.
- [ ] Дорогие 10000-concept runs доступны вручную; нормальный PR CI не получает хрупкий wall-clock gate. Regression threshold вводить только после измерения шума.
- [ ] Для существенной регрессии сохранён Go CPU/heap profile и объяснение; необоснованную оптимизацию до baseline не делать.
- [ ] Команды повторного запуска, pinned commits и input hashes записаны рядом с отчётом. Документ без выполненных runs не закрывает задачу.

## Границы

Не переписывать core ради красивых чисел, не убирать проверки корректности/limits/cancellation. Benchmark future search/viewer/backfill добавлять после появления функций отдельными workload; их отсутствие не блокирует текущую задачу.
