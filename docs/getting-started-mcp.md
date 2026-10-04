---
title: "Connect MCP"
description: "Connect MCP"
permalink: /getting-started-mcp/
---

{% include nav.html %}

<span id="connect-mcp-to-codex-cli"></span>

# Let an agent find the rule through MCP {#page-top}

MCP gives an agent tools to read knowledge. Here the agent should find the delivery rule, read the note and its source, then answer with citations. You need an authenticated Codex CLI and the source checkout from the quickstart.

## Prepare a separate session {#connect}

Run from the checkout root. Use a separate training bundle copy and settings for this session; `-c` does not save the MCP server to global configuration.

```sh
codex --version
mcp_demo_dir=$(mktemp -d)
go build -o "$mcp_demo_dir/okf-mcp" ./cmd/okf-mcp
cp -R examples/project-knowledge/en "$mcp_demo_dir/knowledge"
knowledge_root="$mcp_demo_dir/knowledge"
printf '%s\n' "$knowledge_root"
cd "$mcp_demo_dir"
codex -c "mcp_servers.okf.command=\"$mcp_demo_dir/okf-mcp\"" -c 'mcp_servers.okf.args=[]'
```

Open `/mcp` in the client. The `okf` server should be connected, and its tools should include `search_sections` and `read_concept`. The server receives an absolute `bundle_path` with every call, rather than a `-root` command argument.

## Ask a question {#ask}

Substitute the printed path for `<absolute path>`, then send:

> Use the okf MCP server with bundle_path `<absolute path>`. Find the delivery rule using search_sections with query `delivery operator`, read retry-policy using read_concept, then read source-material as its source. When should an operator get involved? Answer from the supplied requirements, citing the note and source. Show the calls actually made. Do not change anything.

Expected result: the agent actually calls the tools and answers **24 hours**, citing `retry-policy` and `source-material`. Compare its answer with the documents. If you instead use the updated copy from the quickstart, the expected deadline is **48 hours**.

## Check individual calls {#diagnostics}

If the client answers without tools, request these calls explicitly. The JSON path must be absolute:

`search_sections`:

```json
{"bundle_path":"<absolute path>","query":"delivery operator","limit":5}
```

`read_concept`:

```json
{"bundle_path":"<absolute path>","concept_id":"retry-policy"}
```

```json
{"bundle_path":"<absolute path>","concept_id":"source-material"}
```

Search returns snippets, lines, and digests. Reading returns the note content and source metadata; the second read lets you inspect the source itself. Retain the client version, settings without secrets, prompt, calls, and answer as evidence of this particular integration run.

## If something fails {#troubleshooting}

- The server does not start: check the absolute path to the built `okf-mcp` and the client messages.
- The bundle is missing: use the absolute path printed before launching the client.
- Search is empty: check the training folder language and query words.
- The answer has no calls: request the diagnostic calls; a configuration entry alone does not confirm a connection.

[Agent skill]({{ '/skill/' | relative_url }}) is installed separately. The catalog of all 14 tools and their limits is in the [reference]({{ '/reference/' | relative_url }}). This run checks connectivity and reading, rather than measuring model quality.
