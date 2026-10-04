# okf

Project knowledge beside your code, for your team and AI agents.

Record a rule in Markdown, cite its source, and let an agent find the answer. For example: failed job delivery needs operator review after 24 hours. When requirements change, review the related note alongside code.

[Open the training example](https://skosovsky.github.io/okf/demo/project-en.html#retry-policy) · [Try it locally](docs/quickstart.md) · [Русский](README.ru.md)

## When to use it

- Find a constraint before changing a service and open its source.
- Explain an architecture decision together with the material behind it.
- Review affected knowledge after requirements or code change.

## How it works

A knowledge bundle is a folder of Markdown files with YAML metadata and a root `index.md`. Edit the files, keep them in Git, and review them in PRs. OKF adds shared metadata, source links, structural checks, search, and agent tools.

Open Knowledge Format is the document specification. This repository is a Go implementation: libraries, the `okf` CLI, `okf-mcp` server, skills, and an offline HTML viewer. The current document contract is 0.2; 0.1 reading and explicit migration remain supported. Program release versions do not select document versions.

## Install and get a first result

You need Git and Go **1.25.5 or newer** for the source walkthrough. To install the commands:

```sh
go install github.com/skosovsky/okf/cmd/okf@latest
go install github.com/skosovsky/okf/cmd/okf-mcp@latest
okf version
```

If commands are missing, add the `bin` subdirectory of `go env GOPATH` to `PATH`. For a reproducible first run, the [quickstart](docs/quickstart.md) builds from the checkout and copies the training example into a temporary folder. Then run:

```sh
okf search examples/project-knowledge/en --query "delivery operator" --limit 5
```

Expect snippets about 24 hours and an operator. Open `retry-policy.md`, then `source-material.md`; the full walkthrough also shows a manual deadline update to 48 hours.

## What validation means

The validator checks structure and links. Compare claims with sources. `sources` identifies material, `generated` a known producer, and `verified` an actual check. Successful validation does not create this metadata automatically. Embedded computations require a separately selected and authorized runtime.

## Next steps

- [File operations, search, and Markdown preparation](docs/toolkit.md).
- [Install the agent skill](docs/skill.md) and [connect MCP](docs/getting-started-mcp.md): instructions and tools are connected separately.
- [Knowledge review after changes](docs/knowledge-upkeep.md).
- [Commands, Go APIs, and all 14 MCP tools](docs/reference.md).
- [Examples](examples/README.md) and [current releases](https://github.com/skosovsky/okf/releases).
