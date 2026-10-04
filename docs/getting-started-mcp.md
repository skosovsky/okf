---
title: Connect MCP
permalink: /getting-started-mcp/
---

{% include nav.html %}

# Connect MCP to Codex CLI

Continue from the [quickstart]({{ '/quickstart/' | relative_url }}). Requires an installed Codex CLI and an authenticated account. This example configures one session; `-c` overrides do not save a server to global settings.

From the clone root:

```sh
codex --version
mcp_demo_dir=$(mktemp -d)
go build -o "$mcp_demo_dir/okf-mcp" ./cmd/okf-mcp
knowledge_root="$(pwd)/knowledge"
printf '%s\n' "$knowledge_root"
codex -c "mcp_servers.okf.command=\"$mcp_demo_dir/okf-mcp\"" -c 'mcp_servers.okf.args=[]'
```

In Codex, open `/mcp` and check that `okf` is connected and `search_concepts`, `read_concept`, and `get_neighbors` are available. A configured entry alone does not prove a connection. If the server fails to start, check the absolute binary path and client messages. The server accepts no `-root` option: each tool call supplies an absolute `bundle_path`.

Copy `knowledge_root` from your shell and substitute it for `<absolute knowledge path>` in this prompt:

> Use the okf MCP server with bundle_path `<absolute knowledge path>`. Find Package boundaries using search_concepts, read architecture using read_concept, and get its outgoing links using get_neighbors. Where should an implementation change start, and what role do CLI/MCP play? Cite the note and original source. Show which tools you called. Do not change anything.

Expected calls:

```json
{"bundle_path":"<absolute knowledge path>","query":"Package boundaries","limit":5}
```

```json
{"bundle_path":"<absolute knowledge path>","concept_id":"architecture"}
```

```json
{"bundle_path":"<absolute knowledge path>","concept_id":"architecture","direction":"out","limit":10}
```

The answer should start from the relevant Go package's contract: CLI/MCP expose it rather than inventing OKF semantics. Its source is **Toolkit guide** in `architecture.sources`; related notes include **Version resolution** and **Mutation boundary**. Compare citations with the [original note](https://github.com/skosovsky/okf/blob/main/knowledge/architecture.md). An answer without tool calls does not verify the connection.

For evidence, retain the client version, configuration without secrets, actual prompt, tool calls, and answer. This single run checks integration, not model quality. A skill installs instructions separately and does not launch the MCP server.
