# 013 — Поддержание bundle: переносимый workflow и опциональная Go-проверка

Статус: выполнено. Приоритет: P2. Зависимости: 007 для пилота; 005 для root metadata boundary; 012 для оценки эффекта.

## Проблема и переоценка

Агент может изменить код и забыть документацию. Чужой Stop hook полезен как дешёвое напоминание, хотя изменение одного `log.md` не доказывает синхронизацию знаний. Не отвергать сам механизм из-за слабости конкретной проверки.

У их hook есть осмысленные opt-in, override и loop guard. Но уже изменённый до задачи log удовлетворяет gate; новая untracked сущность не учитывается; одна строка лога может скрыть устаревший concept. Поэтому checker сообщает о необходимости review, а не о доказанной истинности документации. Их gate benchmark измерял инструкцию в `AGENTS.md` в эксперименте writer→consumer, а не надёжность shipped bash Stop hook.

References: [hook](https://github.com/scaccogatto/okf-skills/blob/68ce7a0c07f66ca9a6b0beb68937d75314d5e8a5/hooks/okf-stop-check.sh), [adoption snippet](https://github.com/scaccogatto/okf-skills/blob/68ce7a0c07f66ca9a6b0beb68937d75314d5e8a5/templates/CLAUDE-okf.md).

## Этап 1 — без обязательного hook

Добавить переносимые AGENTS/CLAUDE snippets: перед работой прочитать релевантные concepts; после изменения указать обновлённые concepts либо явное решение «документация не затронута» с причиной. Не загружать весь bundle в каждый prompt. Миграция остаётся явной отдельной операцией.

## Этап 2 — opt-in checker на Go

- До реализации определить config schema, result JSON и host adapter input/output. Policy хранится вне root index: `upkeep` не добавлять в нормативный OKF frontmatter.
- Config явно задаёт repo root, bundle root, relevant paths и exclusions. Не считать любой dirty tree своей сессией.
- Определить baseline текущей работы и fingerprint changes: чужие/исходно dirty changes и ранее изменённый log не должны давать ложный результат.
- Поддержать rename/delete/untracked relevant assets и исключить tooling noise; `git status --porcelain -z` разбирать на Go, не grep по именам с пробелами/newlines.
- Результаты: no relevant change, reviewed/updated, needs review, explicit unaffected с reason и привязкой к fingerprint. Это свидетельства процесса, не доказательство истинности текста.
- Дефолт advisory; blocking режим только явный opt-in. Loop guard, timeout, override и fail-open/fail-closed policy документированы. Не создавать бесконечные повторные блокировки.
- Claude Stop adapter может вызывать Go binary; host-neutral CLI используется из Codex/CI вручную. Не обещать API hooks, которых хост не предоставляет.
- Не сохранять постоянный pass из-за одной старой правки log. Неподдерживаемый host не должен блокировать обычную работу.

## Приёмка

- [x] Snippet работает без установки Python/Node/Claude plugin.
- [x] AAA Go tests: clean tree, unrelated change, changed asset, new/deleted asset, pre-existing dirty state, старый log edit, paths с newline, explicit unaffected и устаревший fingerprint.
- [x] Не меняет bundle, trust, status или verification самостоятельно.
- [x] Opt-out/loop guard и bounded runtime проверены.
- [x] После реализации checker выполнен отдельный [валидный writer→consumer rerun](../../benchmark/agent/runs/20260926-upkeep-live-v2/README.md): частота обновления docs и качество ответа consumer с checker и без него опубликованы на двух одинаковых случаях. Первый [невалидный прогон](../../benchmark/agent/runs/20260926-upkeep-live-v1/README.md) сохранён отдельно. Успех hook не называется доказательством актуальности знаний.

## Не входит

Автоматическая оценка истинности документа по diff, обязательное внедрение hooks во все проекты, изменение Git hooks или пользовательских global configs без отдельной задачи.
