# 007 — Knowledge bundle собственной архитектуры

Статус: реализовано. Приоритет: P1. Зависимости: решения 004/005; 006 желательно, но не обязательно.

## Проблема и результат

Репозиторий предоставляет OKF tooling, однако его архитектура и ограничения распределены между README, contracts и ADR. Создать небольшой настоящий bundle, на котором видны и польза формата, и ошибки собственного tooling.

Reference: [чужой self-bundle](https://github.com/scaccogatto/okf-skills/tree/68ce7a0c07f66ca9a6b0beb68937d75314d5e8a5/.okf).

## Контракт и scope

- Предлагаемый путь `knowledge/`, определить его до записи. Не путать с `<bundle>/.okf` служебного store; не менять глобальный gitignore пользователя.
- Первый набор ограничить 8–12 полезными concepts: архитектура пакетов, version resolution, conformance/strict/policy, mutation preview/apply, durable store, MCP contracts, графовые проекции, inert computation, supported platforms, выпуск.
- Источники — конкретные существующие contracts, ADR и код. Bundle объясняет связи и причины решений, а точные API/JSON Schema остаются в исходных контрактах.
- Для путей к исходникам вне bundle не создавать ложные bundle-internal links. Выбрать документированное представление repo source references, например pinned remote URLs; отсутствие network fetch не считается verification.
- Изложение текущего состояния отдельно от истории. Не копировать длинные README/migration passages как вторую независимую спецификацию.
- Все новые непроверенные summaries — явно `draft`; не добавлять `verified` без реальной проверки. Metadata автора означает фактического producer, а не git author источника.

## Работа

1. Составить карту concept → canonical sources → связанные concepts.
2. Написать минимальный bundle, индексы и осмысленный log.
3. Добавить инструкцию чтения/поддержания в docs. Автоматические hooks отложены в 013.
4. Добавить Go validation собственного bundle в CI с объяснённой политикой warnings. Для staleness использовать explicit reference time по контракту 004.
5. После 009 опубликовать viewer как производный artifact, не как ещё один editable source.

## Приёмка

- [x] Каждый concept отвечает на инженерный вопрос и ссылается на проверяемые источники.
- [x] Нет невидимых gitignore файлов знания и нет попадания store receipts/journal в Git.
- [x] Bundle проходит согласованный conformance contract; битые внутренние links/index coverage проверяются.
- [x] Минимум три сценария проверены вручную: найти mutation boundary, определить поддерживаемую platform, понять различие OKF version и package version.
- [x] CI использует Go implementation; не зависит от foreign checker или LLM.

## Не входит

Полная реконструкция истории, дублирование всех исходников, автоматическое назначение freshness/verification и переписывание существующих ADR.

Доказательства: [knowledge/index.md](../../knowledge/index.md), [источники и правило поддержки](../../docs/knowledge.md), [Go validation в CI](../../.github/workflows/ci.yml), [ручные маршруты и ответы](../../docs/knowledge-manual-review.md).
