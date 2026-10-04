---
title: Project history
description: Project history
lang: en
permalink: /history/
---

{% include nav.html %}

# Project history {#page-top}

These materials describe the project at their stated date. Measurements and earlier decisions are preserved. Start with the quickstart and reference for current use.

## Archive {#documents}

<ul class="documentation-index">
{% for document in site.data.documentation.documents %}
{% if document.status == 'historical' %}
  {% assign localized_document = document.en %}
  {% assign document_title = localized_document.path %}
  {% for document_page in site.pages %}
    {% if document_page.url == localized_document.url %}
      {% assign document_title = document_page.title | default: localized_document.path %}
      {% break %}
    {% endif %}
  {% endfor %}
  <li><a href="{{ localized_document.url | relative_url }}">{{ document_title | escape }}</a>{% if document.normative_source %} <small>(normative contract)</small>{% endif %}</li>
{% endif %}
{% endfor %}
</ul>
