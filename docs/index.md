---
title: "OKF: project knowledge"
description: "OKF: project knowledge"
permalink: /
---

{% include nav.html %}

<span id="project-knowledge-beside-your-code"></span>

# OKF: project knowledge beside your code {#page-top}

Keep rules, decisions, and instructions in Markdown so your team and AI agents can find an answer and open its source.

For example, a service retries failed delivery for 24 hours, then involves an operator. Record the rule, link it to the requirements, and let an agent find the answer. When the deadline changes, update the source and the note in the same workflow.

[Open the training example]({{ '/demo/project-en.html#retry-policy' | relative_url }}) · [Try it locally]({{ '/quickstart/' | relative_url }})

## When it helps {#use-cases}

- Before changing a service: find a constraint and inspect its source.
- When handing over a project: explain a decision together with the material behind it.
- After requirements change: find and review the affected notes.

## What OKF includes {#components}

**Open Knowledge Format** describes Markdown files with metadata and links to sources. A knowledge bundle is a folder of these files with a root `index.md`.

This repository provides Go libraries, the `okf` command, an `okf-mcp` server, agent instructions, and an offline HTML viewer. Start with files and viewing, then connect an agent.

## What to check yourself {#validation}

OKF validation finds structural and link errors. Compare content with its source: the presence of `sources` does not mean a claim was checked. Add `verified` only after a real check. Document version `0.2` and program release version are separate values.

## Next step {#next-steps}

1. [Create and find your first rule]({{ '/quickstart/' | relative_url }}).
2. [Install an agent skill]({{ '/skill/' | relative_url }}) and [connect MCP]({{ '/getting-started-mcp/' | relative_url }}).
3. [Work with files and commands]({{ '/toolkit/' | relative_url }}).
4. [Review knowledge after changes]({{ '/knowledge-upkeep/' | relative_url }}).

Exact fields, limits, and interfaces are in the [reference]({{ '/reference/' | relative_url }}). The [architecture example for this repository]({{ '/demo/knowledge.html#architecture' | relative_url }}) remains available separately in English.
