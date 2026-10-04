---
title: Подключение MCP
permalink: /ru/getting-started-mcp/
---

{% include nav_ru.html %}

# Подключить MCP к Codex CLI

Это продолжение [быстрого старта]({{ '/ru/quickstart/' | relative_url }}). Нужен установленный Codex CLI с действующим входом в аккаунт. Пример предназначен для одного сеанса: параметры `-c` не сохраняют сервер в глобальные настройки.

Из корня клона выполните:

```sh
codex --version
mcp_demo_dir=$(mktemp -d)
go build -o "$mcp_demo_dir/okf-mcp" ./cmd/okf-mcp
knowledge_root="$(pwd)/knowledge"
printf '%s\n' "$knowledge_root"
codex -c "mcp_servers.okf.command=\"$mcp_demo_dir/okf-mcp\"" -c 'mcp_servers.okf.args=[]'
```

В Codex откройте `/mcp` и проверьте, что сервер `okf` подключён, а `search_concepts`, `read_concept` и `get_neighbors` доступны. Наличие записи в конфигурации ещё не означает успешное подключение. Если сервер не запускается, проверьте абсолютный путь к бинарнику и сообщения клиента. Сервер не принимает `-root`: абсолютный `bundle_path` передаётся в каждом вызове инструмента.

Скопируйте значение `knowledge_root` из предыдущего shell и задайте запрос, подставив его вместо `<absolute knowledge path>`:

> Используй MCP-сервер okf и bundle_path `<absolute knowledge path>`. Найди заметку Package boundaries с search_concepts, прочитай architecture с read_concept и получи её исходящие связи через get_neighbors. Ответь: в каком пакете нужно начинать изменение реализации и какую роль играют CLI/MCP? Назови заметку и исходный источник. Покажи, какие инструменты ты вызвал. Ничего не изменяй.

Ожидаемые вызовы:

```json
{"bundle_path":"<absolute knowledge path>","query":"Package boundaries","limit":5}
```

```json
{"bundle_path":"<absolute knowledge path>","concept_id":"architecture"}
```

```json
{"bundle_path":"<absolute knowledge path>","concept_id":"architecture","direction":"out","limit":10}
```

Ответ должен опираться на контракт соответствующего Go-пакета: CLI/MCP предоставляют этот контракт, а не изобретают собственную семантику OKF. Источник — **Toolkit guide** из `architecture.sources`; соседние заметки — **Version resolution** и **Mutation boundary**. Сверьте ссылки с [исходной заметкой](https://github.com/skosovsky/okf/blob/main/knowledge/architecture.md). Если агент ответил без вызовов инструментов, сценарий подключения ещё не проверен.

Для отчёта сохраните версию клиента, обезличенную конфигурацию, фактический запрос, вызовы и ответ. Этот один сеанс проверяет интеграцию; он не является оценкой качества модели. Инструкции скила устанавливаются отдельно и не запускают MCP-сервер.
