---
title: "Работа со знаниями"
description: "Работа со знаниями"
permalink: /ru/toolkit/
---

{% include nav_ru.html %}

<span id="toolkit"></span>

# Работать со знаниями проекта {#page-top}

Здесь собраны повседневные операции: создать набор, проверить его, найти правило, подготовить обычные Markdown и посмотреть связи. Команды выполняйте из корня клона; нужны Git и Go 1.25.5 или новее.

## Создать набор {#create}

```sh
work_dir=$(mktemp -d)
go build -o "$work_dir/okf" ./cmd/okf
"$work_dir/okf" init "$work_dir/new-knowledge"
```

Появятся корневой `index.md` и начальная заметка. Откройте файлы и замените учебный текст своим правилом. Тип заметки задаёт `type`; версию всего набора задаёт `okf_version` только в корневом индексе. Источники добавляйте из предоставленных материалов.

Для готового примера:

```sh
cp -R examples/project-knowledge/ru "$work_dir/knowledge"
```

## Проверить, найти и открыть {#read}

```sh
"$work_dir/okf" validate --path "$work_dir/knowledge" --spec auto --strict --check-links --check-orphans --max-warnings=0
"$work_dir/okf" search "$work_dir/knowledge" --query "доставку оператор" --limit 5
"$work_dir/okf" view "$work_dir/knowledge" --output "$work_dir/knowledge.html" --lang ru
"$work_dir/okf" info "$work_dir/knowledge" --spec auto
"$work_dir/okf" parse "$work_dir/knowledge/retry-policy.md" --format json
```

Ожидайте чистую проверку, разделы о доставке и операторе, автономный HTML, сводку набора и разобранную заметку. `search` показывает фрагменты и строки; его оценка отражает совпадение слов. Если слов нет в тексте того же языка, выдача будет пустой.

`--spec auto` использует объявление в корневом индексе; без объявления применяется 0.2 с правилами совместимости. Явный `--spec 0.1` или `0.2` проверяет выбранную версию: противоречащее или повреждённое объявление не заменяется молча. Для неизвестной будущей версии доступны чтение с сохранением объявления и сведения о совместимости.

## Подготовить существующие Markdown {#setup}

`setup` копирует выбранные Markdown в новый набор. Сначала создайте два обычных файла во временной папке:

```sh
mkdir "$work_dir/team-notes"
cat > "$work_dir/team-notes/retries.md" <<'EOF'
# Правило повторной доставки
Повторяйте неудачную доставку 24 часа, затем подключите оператора.
[Действия оператора](operator.md)
EOF
cat > "$work_dir/team-notes/operator.md" <<'EOF'
# Действия оператора
Проверьте причину ошибки перед следующей попыткой.
EOF
"$work_dir/okf" setup --source "$work_dir/team-notes" --target "$work_dir/imported-knowledge" --type Guide
```

Это предварительный просмотр: целевая папка ещё не создана. Ожидайте два документа, `applicable=true`, `published=false` и `Plan digest`. `Guide` — ваш явный выбор для файлов без типа. Прочитайте план и диагностику, затем скопируйте значение после `Plan digest:`:

```sh
printf 'Вставьте контрольную сумму просмотренного плана: '
read -r plan_digest
"$work_dir/okf" setup --source "$work_dir/team-notes" --target "$work_dir/imported-knowledge" --type Guide --apply --plan-digest "$plan_digest"
"$work_dir/okf" validate --path "$work_dir/imported-knowledge" --spec 0.2 --strict --check-links --check-orphans --max-warnings=0
"$work_dir/okf" view "$work_dir/imported-knowledge" --output "$work_dir/imported-knowledge.html" --lang ru
```

Ожидайте `published=true`, проверку без ошибок и два связанных документа в HTML. Исходная папка сохраняется. Если исходники или параметры изменились, получите новый план. Существующая целевая папка, неподдерживаемые вложения/ссылки, повреждённые метаданные и символические ссылки блокируют подготовку; для повторного применения выбирайте новый целевой путь. Условия и пределы — в [контракте подготовки Markdown]({{ '/ru/contracts/markdown-setup/' | relative_url }}).

## Посмотреть связи и оформить файлы {#graph}

```sh
"$work_dir/okf" graph "$work_dir/knowledge" --format mermaid
"$work_dir/okf" fmt "$work_dir/knowledge/retry-policy.md"
"$work_dir/okf" index "$work_dir/knowledge" --spec auto
```

Граф показывает связи документов. `fmt` без `-w` печатает результат для просмотра; `fmt -w` записывает его. `index` обновляет индекс набора — просмотрите изменения в Git. Эти команды не выполняют миграцию и не добавляют сведения о проверке. YAML `relations` — расширение этой реализации; обычные Markdown-ссылки доступны в стандартном формате.

## Переходы и автоматизация {#automation}

Для 0.1 → 0.2 используйте [руководство миграции]({{ '/ru/migration/' | relative_url }}). Для API и всех 14 MCP-инструментов, включая `search_sections`, см. [справочник]({{ '/ru/reference/' | relative_url }}). Изменения через MCP выполняйте парами предварительного просмотра и применения; переносите контрольную сумму и ревизию из полученного плана, а не придумывайте их.

В Go используйте контракты пакетов `bundle`, `validator`, `retrieval`, `setup`, `viewer`, `graph`, `mutation` и `store`; CLI и MCP предоставляют доступ к их операциям. При работе с датами задавайте явный `--as-of`: дата для стандартного профиля или время со смещением для `instant-0b87c52`.

## Если не получилось {#troubleshooting}

При несовпадении версии исправьте объявление или выбранный режим после проверки исходных документов. При предупреждениях прочитайте отчёт: `--max-warnings=0` делает каждое предупреждение причиной ненулевого кода выхода. При существующем HTML используйте новый путь или явно `--overwrite`. `okf help` и справочник показывают полный набор команд и параметров.
