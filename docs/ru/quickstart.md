---
title: Попробовать OKF локально
description: Откройте готовый пример и создайте заметку из предоставленного материала.
permalink: /ru/quickstart/
---

{% include nav_ru.html %}

# Попробовать OKF

## 1. Посмотреть готовые знания

[Откройте пример без установки]({{ '/demo/knowledge.html#architecture' | relative_url }}). Это снимок знаний об этом репозитории. В **Package boundaries** найдите ответ на вопрос «В каком пакете менять реализацию?»:

- CLI и MCP предоставляют возможности пакетов, а не собственные правила OKF.
- В **Sources** есть Toolkit guide и Go module declaration.
- Ссылки **Mutation boundary** и **Version resolution** ведут к связанным заметкам.

Поля проверки, состояния и свежести показываются отдельно. Эта заметка имеет состояние `draft`; отсутствие `verified` означает, что проверка не записана. Отсутствие `stale_after` не позволяет вычислить устаревание.

## 2. Повторить локально

Нужны Git и Go **1.25.5 или новее**, доступ к сети для загрузки зависимостей и браузер. Команды ниже — для POSIX shell (macOS/Linux). Запустите их из папки, где ещё нет каталога `okf`; после `cd okf` все команды выполняются из корня клона.

```sh
git clone https://github.com/skosovsky/okf.git
cd okf
go version
demo_dir=$(mktemp -d)
./scripts/build-viewer-demo.sh "$demo_dir/knowledge-demo.html"
printf '%s\n' "$demo_dir/knowledge-demo.html"
```

Скрипт сначала проверит `knowledge/`, затем напечатает путь к HTML. Откройте этот файл в браузере через «Открыть файл» или файловый менеджер. Найдите `Package boundaries`, откройте источники и переход к **Mutation boundary**. Это содержательные заметки со ссылками на файлы репозитория, а не пустая заготовка.

`mktemp` создаёт отдельный каталог; HTML записывается в новый файл вне набора знаний. При повторе используйте новый `demo_dir`: существующий выходной файл не перезаписывается. Скрипт фиксирует дату `2026-09-26`, чтобы результат воспроизводился; это дата расчёта свежести, а не подтверждение истинности текста. HTML содержит данные и работает без сети; внешние источники требуют сети при открытии.

## 3. Сделать свою заметку

Продолжайте в том же shell и корне клона, сохранив `demo_dir`. Следующий материал вымышленный и служит только упражнением: «Сервис повторяет неудачную доставку задания в течение 24 часов. Затем оператор должен проверить задание перед следующей попыткой».

Создадим исходный материал и заметку со ссылкой на него. `init` требует новый каталог с существующим родителем. Команда `rm` удаляет только созданную им пустую заметку в нашем временном каталоге.

```sh
go run ./cmd/okf init "$demo_dir/my-knowledge"
cat > "$demo_dir/my-knowledge/source-material.md" <<'EOF'
---
type: Source Material
title: Training service requirements
---
# Supplied material
This fictional service retries failed job delivery for 24 hours.
After that, an operator must review the job before another attempt.
EOF
cat > "$demo_dir/my-knowledge/retry-policy.md" <<'EOF'
---
type: Operational Note
title: Retry limit
sources:
  - id: requirements
    resource: source-material.md
    title: Training service requirements
---
# How long should delivery retry?
Retry failed job delivery for 24 hours. Then request operator review.[^requirements]

See the [supplied requirements](source-material.md).

[^requirements]: Training service requirements, Supplied material.
EOF
rm "$demo_dir/my-knowledge/getting-started.md"
cat > "$demo_dir/my-knowledge/index.md" <<'EOF'
---
okf_version: "0.2"
---
# Concepts
* [Training service requirements](source-material.md) - Supplied fictional material.
* [Retry limit](retry-policy.md) - A note derived from the supplied requirements.
EOF
go run ./cmd/okf validate --path "$demo_dir/my-knowledge" --spec 0.2 --strict --check-links --check-orphans --as-of 2026-09-26 --max-warnings=0
go run ./cmd/okf view "$demo_dir/my-knowledge" --output "$demo_dir/my-knowledge.html" --spec 0.2 --as-of 2026-09-26
```

Ожидаемый результат: проверка без ошибок и предупреждений, затем новый HTML с двумя заметками. Откройте `my-knowledge.html`, найдите **Retry limit** и источник **Training service requirements**. Ответ на вопрос «Когда нужен оператор?» — после 24 часов неудачных попыток. В данных нет выдуманных автора, проверки или свежести. Успешная проверка подтверждает формат этих файлов.

## 4. Прочитать из агента

[Подключите MCP]({{ '/ru/getting-started-mcp/' | relative_url }}) и попросите агента найти ответ со ссылкой на источник. Установка скила и подключение сервера — отдельные действия.

После первого опыта: [поддержка знаний после изменений](https://github.com/skosovsky/okf/blob/main/docs/knowledge-upkeep.md), [восстановление из Git](https://github.com/skosovsky/okf/blob/main/backfill/PROTOCOL.md), [полный справочник]({{ '/ru/reference/' | relative_url }}).

Этот пример проверяет CLI и просмотрщик. Он не доказывает улучшение ответов модели или автоматическое использование инструментов агентом.

## 5. Найти нужный раздел по нескольким словам

Из корня клона выполните:

```sh
go run ./cmd/okf search knowledge --query "implementation change" --limit 5
go run ./cmd/okf search knowledge --query "implementation change" --limit 5 --json
```

В результатах будет раздел заметки `architecture.md` с фрагментом текста и номерами строк. `locator` обозначает путь и диапазон исходных строк; не каждый Markdown-просмотрщик открывает такие якоря. JSON также содержит digest файла и снимка. После изменения файла повторите поиск: прежние номера строк больше не подтверждают источник.

Поиск требует все слова в одном разделе, его заголовке или названии заметки. Он нормализует регистр и Unicode, но не переводит запросы и не ищет синонимы. Оценка показывает совпадение слов, а не достоверность или свежесть. Если `truncated` равно `true`, уточните запрос или увеличьте `--limit` до 100. [Полные правила поиска]({{ '/contracts/section-search/' | relative_url }}).

В MCP та же возможность называется `search_sections`. Старый `search_concepts` продолжает искать одну буквальную подстроку.

## 6. Подготовить существующие Markdown

Этот учебный пример вымышленный. Он показывает перенос обычных заметок в отдельный новый каталог; исходники не меняются. Продолжайте в том же shell, где задан `demo_dir`:

```sh
mkdir "$demo_dir/team-notes"
cat > "$demo_dir/team-notes/retries.md" <<'EOF'
# Retry policy
Retry failed delivery for 24 hours, then request operator review.
See [operator steps](operator.md).
EOF
cat > "$demo_dir/team-notes/operator.md" <<'EOF'
# Operator steps
Review the failed job before another delivery attempt.
EOF
go run ./cmd/okf setup --source "$demo_dir/team-notes" --target "$demo_dir/imported-knowledge" --type Guide
```

Это только предпросмотр: целевой каталог ещё не создаётся. Должны появиться два документа, `applicable=true`, `published=false` и `Plan digest`. Изучите план и диагностику. Тип `Guide` выбран явно, потому что в исходных заметках нет метаданных. Сведения об авторе, источниках и проверках не добавляются.

Скопируйте значение после `Plan digest:`. Следующие команды запросят его и применят именно этот план:

```sh
printf 'Paste the reviewed plan digest: '
read -r plan_digest
go run ./cmd/okf setup --source "$demo_dir/team-notes" --target "$demo_dir/imported-knowledge" --type Guide --apply --plan-digest "$plan_digest"
go run ./cmd/okf validate --path "$demo_dir/imported-knowledge" --spec 0.2 --strict --check-links --check-orphans --max-warnings=0
go run ./cmd/okf view "$demo_dir/imported-knowledge" --output "$demo_dir/imported-knowledge.html" --spec 0.2
```

Ожидайте `published=true`, успешную проверку и HTML с двумя связанными заметками. Изменившийся источник или план требуют нового предпросмотра. Существующая цель не заменяется; повтор после успешного применения требует нового каталога. Локальные ссылки должны вести к выбранным Markdown-файлам или якорям. Картинки, неподдерживаемые ссылки, повреждённые метаданные и символические ссылки блокируют перенос, а не исчезают молча. При ошибке до публикации частичный набор не остаётся. [Контракт подготовки Markdown]({{ '/contracts/markdown-setup/' | relative_url }}).
