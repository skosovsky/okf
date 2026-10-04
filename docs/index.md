---
title: Project knowledge beside your code
description: Markdown decisions, constraints, and instructions for your team and AI agents.
---

{% include nav.html %}

# Project knowledge beside your code

Keep decisions, constraints, and instructions in Markdown with links to their sources. OKF checks note structure, gives agents tools to read and change the knowledge, and builds a page you can open in your browser.

[Explore the example]({{ '/demo/knowledge.html#architecture' | relative_url }}) · [Try it locally]({{ '/quickstart/' | relative_url }})

## When is it useful?

- Recover why a design decision was made and find the evidence behind it.
- Find a constraint before changing a service.
- Review affected notes after the code changes.

## What does OKF add? {#spec}

Knowledge stays in ordinary files: Markdown, YAML metadata, and an `index.md` list. You can read it in an editor, keep it in Git, and review changes. OKF adds shared source metadata, structural validation, and tools for agents to work with these files.

[Open Knowledge Format](https://github.com/skosovsky/okf/blob/main/skills/open-knowledge-format/references/spec-v02.md) is the upstream format. This project provides Go libraries, a CLI, an MCP server, and agent instructions. It supports `0.2` documents plus `0.1` reading and migration. Version and compatibility details live in the [reference]({{ '/reference/' | relative_url }}).

## Try it {#quickstart}

[Open the demo]({{ '/demo/knowledge.html#architecture' | relative_url }}) without installing anything. Find **Package boundaries**: the note answers “Where should an implementation change go?”, lists sources, and links to related decisions.

The [quickstart]({{ '/quickstart/' | relative_url }}) reproduces that example locally and creates a note of your own. [Connect MCP]({{ '/getting-started-mcp/' | relative_url }}) to try reading it from an agent.

## Examples {#examples}

Start with [this repository's knowledge](https://github.com/skosovsky/okf/tree/main/knowledge). The separate [format test cases](https://github.com/skosovsky/okf/blob/main/examples/README.md) help test implementations.

## Tools {#tools}

[CLI and libraries]({{ '/toolkit/' | relative_url }}) · [Agent instructions]({{ '/skill/' | relative_url }}) · [Migration]({{ '/migration/' | relative_url }}) · [Reference]({{ '/reference/' | relative_url }})

## Questions {#faq}

**Does validation prove a claim?** No. It checks structure. `sources` records evidence, `generated` identifies a known producer, and `verified` records real checks. Successful structural validation does not create any of them.

**Will an agent connect automatically?** Connect MCP in your client separately from installing a skill, then check that tools are present and used.

**Can a note run code?** Computation fields are data. Execution needs a separately trusted and authorized runtime.
