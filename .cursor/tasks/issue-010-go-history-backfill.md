# 010 — Восстановление draft bundle из Git-истории

Статус: backlog. Приоритет: P2. Зависимости: 004/005, публикация через существующие mutation/store; 012 нужен до заявления о качестве.

## Проблема и результат

У старого проекта уже есть решения в commit bodies/diffs, но нет bundle. Получить проверяемый draft с привязкой утверждений к evidence и историей отменённых решений, а не по одному concept на commit.

Reference: [backfill skill](https://github.com/scaccogatto/okf-skills/blob/68ce7a0c07f66ca9a6b0beb68937d75314d5e8a5/skills/backfill/SKILL.md), [extractor](https://github.com/scaccogatto/okf-skills/blob/68ce7a0c07f66ca9a6b0beb68937d75314d5e8a5/skills/backfill/scripts/okf_backfill_events.py).

## Контракты

- Go extractor получает frozen repo/ref/range; MVP — только Git, без чтения локальных чатов по умолчанию. Git executable допустим как внешний SCM tool, Python/Node runtime — нет.
- JSON Schema для event, capped diff manifest, analysis, candidate changes, checkpoint и coverage report; версия и digests входов в каждом resumable checkpoint.
- First-parent/merge policy и порядок должны быть явными. Предпочесть causal/topological order для replay: timestamp может идти назад и не доказывает supersession.
- Определить skip reasons для generated/lock files, limits и поведение binary/deleted/renamed files. Полный file stat сохраняется, truncation принадлежит extractor, LLM не вправе заменить этот флаг своим ответом.
- Deterministic extraction отделена от недетерминированной интерпретации. При одинаковом input/config event artifacts совпадают байтово.
- Анализаторы возвращают evidence references и proposal, не пишут bundle. Единственный writer применяет согласованный desired state через preview/apply и revision checks; никаких direct-write обходов store.
- Resume проверяет ref, content digest, extractor/schema/prompt versions. Само наличие файла анализа не делает его валидным checkpoint.
- Все восстановленные concepts явно `draft`, без `verified`; политика draft задана workflow, а не выведена из git age. Producer agent/process отделён от author источника.

## Семантические правила

Concept описывает сущность/решение, а не commit subject. Совпадающие сущности обновляются. Новое решение, отменяющее старое, заменяет действующее утверждение; прошлое получает датированную history note или отдельный deprecated concept с successor. При неясной отмене сохранить unresolved conflict, не придумывать причинность.

Intent в commit/session не равен выполненному действию. Частичный diff не даёт права домысливать оставшуюся реализацию. Source IDs и claim attribution берутся из проверяемого evidence.

Coverage означает «каждое включённое событие рассмотрено и связано с result либо явно отклонено с причиной», а не «каждое событие породило concept». Эта метрика не доказывает истинность знания.

## Этапы

1. Детерминированный Go extractor и bounded diff API с fixture repos.
2. Host-neutral skill/protocol для analysis → single writer. BYOT Go interfaces для будущих provider adapters; встроенный LLM SDK не обязателен для MVP.
3. Draft preview, конфликтные решения, безопасный apply и restart/resume.
4. Только после evals — batching/параллельные analyzers с configurable budgets. Модели не зашивать по чужим ценовым измерениям.

## Приёмка

- [ ] AAA Go tests на extraction/diffs, нет shell interpolation из commit text/path.
- [ ] Фиксированные входы воспроизводятся; truncation/skip counts проверяются независимо от LLM.
- [ ] Interrupted run и resumed run не теряют/дублируют events; изменившийся input инвалидирует checkpoint.
- [ ] Fixture «A → отмена A → B» не оставляет A действующим; ambiguous case остаётся unresolved.
- [ ] Ошибка/cancel до apply не меняет bundle; stale revision не применяется; replay использует store guarantees.
- [ ] Отчёт содержит coverage, unresolved conflicts, evidence truncation, стоимость/usage если доступны и границы качества.
- [ ] Качество проверено через сценарии 012: независимый consumer отвечает по реконструированному bundle на вопросы с отменёнными решениями, неполным evidence и текущим состоянием кода. Отчёт показывает фактические ошибки/отказы отдельно от покрытия событий. Без этого прогона задача остаётся открытой, даже если schema и coverage проходят.

## Не входит

Claude transcript ingestion в MVP, PR/Slack/email adapters, автоматическая verification, recovery чужими Python scripts, массовый replay нашего репозитория в рамках разработки extractor.
