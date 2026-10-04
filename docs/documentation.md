---
title: Documentation index
description: Documentation index
lang: en
permalink: /documentation/
---

{% include nav.html %}

# Documentation index {#page-top}

Start with the training example, then prepare your own notes or connect an agent. Detailed contracts and library design are listed below.

## Start here {#start}

- [Quickstart]({{ '/quickstart/' | relative_url }})
- [Prepare existing Markdown]({{ '/toolkit/#setup' | relative_url }})
- [Install and use the agent skill]({{ '/skill/' | relative_url }})
- [Connect MCP]({{ '/getting-started-mcp/' | relative_url }})
- [Maintain knowledge after changes]({{ '/knowledge-upkeep/' | relative_url }})
- [Commands and APIs]({{ '/reference/' | relative_url }})

## Guides and technical documents {#documents}

<ul class="documentation-index">
{% for document in site.data.documentation.documents %}
{% unless document.status == 'historical' or document.id == 'documentation-index' or document.id == 'history-index' %}
  {% assign localized_document = document.en %}
  {% assign document_title = localized_document.path %}
  {% for document_page in site.pages %}
    {% if document_page.url == localized_document.url %}
      {% assign document_title = document_page.title | default: localized_document.path %}
      {% break %}
    {% endif %}
  {% endfor %}
  <li><a href="{{ localized_document.url | relative_url }}">{{ document_title | escape }}</a>{% if document.normative_source %} <small>(normative contract)</small>{% endif %}</li>
{% endunless %}
{% endfor %}
</ul>
