# 011 — GitHub Action над нашим Go validator

Статус: выполнено (локально и в PR #7). Приоритет: P1. Зависимости: решения 004/005; существующая release-engineering задача 003 не дублируется.

## Результат

Внешний репозиторий подключает один `uses` step, получает version-aware validation, явную warning policy и тот же отчёт, что локально. Никакого второго checker на Python.

Reference: [action.yml](https://github.com/scaccogatto/okf-skills/blob/68ce7a0c07f66ca9a6b0beb68937d75314d5e8a5/action.yml).

## Контракт до реализации

- Inputs: bundle path, spec selector, strict checks, link/orphan checks, optional reference time и warning budget. Точные имена/defaults закрепить в docs/action metadata.
- У нас `--strict` включает advisory checks. Не превращать его молча в warnings-as-errors. Отдельная opt-in policy `max-warnings` должна быть одинаково определена на CLI и в Action либо в общем Go policy adapter.
- Warning policy меняет CI outcome, но не `conformant`; описать precedence для base errors, policy failure и operational failure. Сохранить старые exit codes там, где новая policy не включена.
- Один validation run производит и outcome, и JSON report. Чужой Action вызывает checker повторно ради JSON; у нас это создаёт ненужный риск отчёта от другого snapshot.
- Определить bounded output `report`, большие reports как artifact/file; ошибки самого процесса нельзя маскировать `|| true`.
- Поставка: Go binary либо build exact pinned action revision с Go toolchain. Не скачивать `latest` поверх выбранной версии. Минимальный shell только обвязка, логика на Go, inputs через env/argv без interpolation в script.

## Работа

1. Зафиксировать warning policy и transport контракты.
2. Реализовать недостающую policy в Go поверх существующего Report.
3. Создать Action wrapper и documentation examples с pinned refs.
4. Integration fixtures: valid, malformed, strict warnings, warning budget exceeded, missing directory, selector conflict.
5. Добавить self-test Action в workflow, не дублируя всю матрицу release engineering.

## Приёмка

- [x] Локальный CLI и Action дают одинаковые diagnostics/outcome для одного snapshot/config.
- [x] AAA Go tests для границ warning budget (0/N/N+1), malformed input и policy/base distinction.
- [x] JSON доступен и при validation failure; operational failure явно отличим.
- [x] Adversarial paths с пробелами/quotes/metacharacters не исполняются как shell.
- [x] Сам Action реально запущен в GitHub Actions на integration fixtures: valid, malformed, strict warnings, warning budget exceeded, missing directory и selector conflict. Ссылки на run и сверка report/outcome с локальным Go CLI приложены; одних unit tests wrapper недостаточно.
- [x] Python, Node и uv не требуются нашим shipped validator runtime; сторонние стандартные setup actions не содержат нашу бизнес-логику.

Доказательства: [контракт и локальный эквивалент](../../docs/github-action.md), [Action](../../action.yml), [матрица с `cmp` JSON-отчёта против Go CLI](../../.github/workflows/action-integration.yml); [успешный GitHub Actions run на опубликованном коммите `21689f8`](https://github.com/skosovsky/okf/actions/runs/36237293789). Матрица покрывает шесть обязательных сценариев и instant profile. Два независимых ревью реализации и проверок: PASS.

## Не входит

Автоисправление bundle, миграция во время validation, PR comments от имени пользователя, изменение strict semantics ради совместимости с чужим CLI.
