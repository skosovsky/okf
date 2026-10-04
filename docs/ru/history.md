---
title: История проекта
description: История проекта
lang: ru
permalink: /ru/history/
---

{% include nav_ru.html %}

# История проекта {#page-top}

Эти материалы описывают состояние проекта на указанную дату. Результаты измерений и прежние решения сохранены; для текущего использования начинайте с первого запуска и справочника.

## Архив {#documents}

<ul class="documentation-index">
{% for document in site.data.documentation.documents %}
{% if document.status == 'historical' %}
  {% assign localized_document = document.ru %}
  {% assign document_title = localized_document.path %}
  {% for document_page in site.pages %}
    {% if document_page.url == localized_document.url %}
      {% assign document_title = document_page.title | default: localized_document.path %}
      {% break %}
    {% endif %}
  {% endfor %}
  <li><a href="{{ localized_document.url | relative_url }}">{{ document_title | escape }}</a>{% if document.normative_source %} <small>(нормативный контракт)</small>{% endif %}</li>
{% endif %}
{% endfor %}
</ul>
