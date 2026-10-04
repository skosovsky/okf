---
title: Каталог документации
description: Каталог документации
lang: ru
permalink: /ru/documentation/
---

{% include nav_ru.html %}

# Каталог документации {#page-top}

Выберите задачу: сначала попробуйте учебный пример, затем подготовьте свои заметки или подключите агента. Контракты и устройство библиотек доступны ниже.

## С чего начать {#start}

- [Первый запуск]({{ '/ru/quickstart/' | relative_url }})
- [Подготовка существующих Markdown]({{ '/ru/toolkit/#setup' | relative_url }})
- [Установка и применение навыка агента]({{ '/ru/skill/' | relative_url }})
- [Подключение MCP]({{ '/ru/getting-started-mcp/' | relative_url }})
- [Поддержка знаний после изменений]({{ '/ru/knowledge-upkeep/' | relative_url }})
- [Команды и API]({{ '/ru/reference/' | relative_url }})

## Руководства и технические материалы {#documents}

<ul class="documentation-index">
{% for document in site.data.documentation.documents %}
{% unless document.status == 'historical' or document.id == 'documentation-index' or document.id == 'history-index' %}
  {% assign localized_document = document.ru %}
  {% assign document_title = localized_document.path %}
  {% for document_page in site.pages %}
    {% if document_page.url == localized_document.url %}
      {% assign document_title = document_page.title | default: localized_document.path %}
      {% break %}
    {% endif %}
  {% endfor %}
  <li><a href="{{ localized_document.url | relative_url }}">{{ document_title | escape }}</a>{% if document.normative_source %} <small>(нормативный контракт)</small>{% endif %}</li>
{% endunless %}
{% endfor %}
</ul>
