# 009 — Автономный HTML viewer из Go CLI

Статус: реализовано; браузерная приёмка пройдена 2026-09-26. Приоритет: P2. Зависимости: 004/005; переиспользовать query/edge semantics 008, если они уже реализованы.

## Результат

Человек открывает один HTML-файл, находит concept, читает его и видит связи, provenance и отдельно trust/status/staleness. Исходный bundle при экспорте не меняется.

Reference: [visualizer](https://github.com/scaccogatto/okf-skills/blob/68ce7a0c07f66ca9a6b0beb68937d75314d5e8a5/skills/visualize/scripts/okf_visualize.py). У них renderer удобный, но зависимости загружаются с CDN — это не полностью offline artifact.

## Контракт до кода

- Выбрать одну additive CLI поверхность: отдельную export/view команду либо новый формат `okf graph`. Не ломать existing graph profiles и JSON-LD/RDF wire.
- Определить projection DTO: concepts, типизированные edges, text/Markdown, lifecycle/provenance, версия exporter и spec revision, explicit reference time.
- Без reference time показывать stale state как unevaluated. Опциональное отображение «на сейчас» должно явно сообщать clock basis и не менять записанные данные.
- Выходной файл и overwrite policy явные; запрет перезаписи исходных concepts и служебных store paths. Ошибка экспорта не оставляет частичный output вместо существующего файла.
- Go CLI/API, templates и `go:embed` assets. Никаких Python/Node/uv/npm стадий для пользователя или Go CI. JavaScript допустим только как локально встроенный browser UI asset; вычисление OKF семантики остаётся на Go.

## Минимальный UI

- Поиск и фильтр по type; список доступен даже при отключённом графе.
- Deep links на canonical ConceptID, detail panel, incoming/outgoing, различимые edge kinds.
- Markdown, sources и derived trust, status, staleness в карточке. Не рендерить bundle scripts/HTML как исполняемые инструкции.
- Предел размера/nodes и устойчивый layout больших графов. Дефолтный force layout не должен блокировать страницу на 1000+ concepts.
- Все библиотеки/шрифты/стили встроены, provenance/licensing сохранены. Без аналитики и фоновых fetch; внешние ссылки открываются только действием пользователя.

## Приёмка

- [x] Один Go binary генерирует файл, читаемый offline без сетевых запросов.
- [x] AAA Go tests: escaping `</script>`, unsafe URLs/HTML, необычные IDs, empty bundle, broken links, output collision, cancelled export.
- [x] Данные совпадают с API bundle/graph, trust/lifecycle не вычисляются второй противоречивой реализацией в JS.
- [x] Проверены маленький пример, около 1000 concepts и превышение лимита; фильтры/deep links работают.
- [x] Визуально проверены длинные заголовки, русский текст, code blocks, отсутствие metadata. Для этого не вводить Node-based test stack.

## Не входит

Web backend, аккаунты, редактирование знаний из browser, исполнение Attested Computation и отдельный parser формата.

Доказательства: [Go exporter tests](../../viewer/viewer_test.go), [CLI tests](../../internal/okfcli/view_test.go), [offline viewer contract](../../viewer/README.md), [CI artifact](../../.github/workflows/ci.yml), [ручная браузерная проверка](../../docs/viewer-browser-review.md). Go tests подтверждают small/1001-concept export и лимит. Пользователь вручную проверил оба HTML-файла, фильтры, deep links, узкое окно, русский текст, Go-блок, отсутствие metadata и отклик списка из 1000 concepts; к сообщению с результатом приложены четыре скриншота. Два независимых субагента повторно подтвердили полноту и отсутствие блокирующих ошибок; оба запустили `go test ./viewer ./internal/okfcli -count=1` успешно.
