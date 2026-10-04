# okf

Project knowledge beside your code, for your team and AI agents.

Keep architecture decisions, constraints, and instructions in Markdown with links to their sources. `okf` checks the document structure, gives agents tools to read and change the knowledge, and builds an HTML viewer you can open offline.

Use it when you need to:

- Find why a design decision was made, and follow the evidence.
- Locate a constraint before changing a service.
- Review affected knowledge after a code change.

[Open the example](https://skosovsky.github.io/okf/demo/knowledge.html#architecture) · [Try it locally](docs/quickstart.md) · [Русский](README.ru.md)

## What lives on disk?

A bundle is a folder of Markdown notes. Each note has YAML frontmatter; `index.md` lists them. Your repository, editor, Git history, and review process still work with those files. Compared with an ordinary Markdown folder, OKF adds a shared metadata contract, structural validation, source attribution, and tools for bounded agent access and reviewed edits.

[Open Knowledge Format](skills/open-knowledge-format/references/spec-v02.md) is the upstream format. This repository is a Go implementation: reusable packages, the `okf` CLI, an `okf-mcp` server, and optional agent skills. The current document contract is `0.2`; `0.1` reading and explicit migration remain supported. A package release version does not select a document version.

## A first useful question

Open **Package boundaries** in the example and ask: “Where should an implementation change go?” The note points to the toolkit guide and Go module declaration, and links to **Mutation boundary** and **Version resolution**. The [quickstart](docs/quickstart.md) reproduces that viewer, then creates a sourced note from supplied material. The [MCP walkthrough](docs/getting-started-mcp.md) explains how to connect a client and check its answer.

## Install

Requires Git and Go **1.25.5 or newer** for the source walkthrough. To install binaries:

```sh
go install github.com/skosovsky/okf/cmd/okf@latest
go install github.com/skosovsky/okf/cmd/okf-mcp@latest
okf version
```

Installing a skill supplies instructions; connecting MCP supplies tools. Neither action automatically performs the other or ensures that an agent uses them.

## What a check means

Validation checks structure, not truth. `sources` records supplied evidence; `generated` identifies a known producer; `verified` records actual checks. The toolkit does not infer verification or freshness from a successful validation, and does not execute code embedded in knowledge.

The viewer demonstration is a CLI smoke test. Offline retrieval measurements concern finding source material; they do not establish improved LLM answer quality.

## Next steps

- [Toolkit reference](docs/reference.md): versions, consumption rules, mutation and migration guarantees, MCP, and Go APIs.
- [Examples](examples/README.md): a readable repository bundle and format test cases.
- [Maintain knowledge after changes](docs/knowledge-upkeep.md).
- [Development and package checks](docs/reference.md#development).

- [Search and prepare existing Markdown](docs/quickstart.md): continue the quickstart.
