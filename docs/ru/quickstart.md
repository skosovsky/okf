---
title: "Первый запуск"
description: "Первый запуск"
permalink: /ru/quickstart/
---

{% include nav_ru.html %}

<span id="try-okf"></span>

# Первое правило с источником {#page-top}

В вымышленном наборе OKF 0.3 срок доставки увеличили с 24 до 48 часов.

## Подготовка {#prerequisites}

Понадобятся Git, Go 1.25.5 или новее и браузер. Выполняйте команды в одной оболочке.

```sh
git clone https://github.com/skosovsky/okf.git
cd okf
demo_dir=$(mktemp -d)
go build -o "$demo_dir/okf" ./cmd/okf
cp -R examples/project-knowledge/ru "$demo_dir/my-knowledge"
```

## Посмотреть правило {#create}

Откройте `retry-policy-current.md`. В заметке указан срок **48 часов**. Сноска `[^requirements]` указывает на источник, а ссылка открывает `source-material.md`.

## Проверить и открыть {#validate}

```sh
"$demo_dir/okf" validate --path "$demo_dir/my-knowledge" --spec 0.3 --strict --check-links --check-orphans --as-of 2026-11-27 --max-warnings=1
"$demo_dir/okf" view "$demo_dir/my-knowledge" --output "$demo_dir/my-knowledge.html" --as-of 2026-11-27 --lang ru
```

Ожидайте ноль ошибок и одно предупреждение о просроченном пересмотре старого правила. Откройте `my-knowledge.html` и выберите **Когда подключать оператора**. Проверка относится к текущему тексту. **Срок пересмотра** показывает, когда правило нужно снова сверить.

## Найти ответ {#search}

```sh
"$demo_dir/okf" search "$demo_dir/my-knowledge" --query "48 часов оператор" --limit 5
```

Откройте `retry-policy-current.md` по найденным строкам и сверьте правило с источником.

## Найти замену {#update}

```sh
"$demo_dir/okf" search "$demo_dir/my-knowledge" --query "прежнему правилу повторяли" --current-only
```

Поиск покажет, где раньше стоял срок 24 часа, и укажет замену `retry-policy-current`. В просмотрщике откройте **Прежнее правило доставки** и нажмите **Открыть замену**. Старая проверка сделана до смысловой правки и остаётся в истории.

## Если не получилось {#troubleshooting}

Если HTML уже существует, выберите новый путь или передайте `--overwrite`. Используйте слова из русского набора.

Дальше: [подготовить свои Markdown]({{ '/ru/toolkit/' | relative_url }}#setup), [установить навык]({{ '/ru/skill/' | relative_url }}) или [подключить MCP]({{ '/ru/getting-started-mcp/' | relative_url }}).
