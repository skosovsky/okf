---
title: "OKF agent skill"
description: "OKF agent skill"
permalink: /skill/
---

{% include nav.html %}

<span id="agent-skill"></span>

# Install the OKF agent skill {#page-top}

The `open-knowledge-format` skill gives an agent instructions for reading and authoring notes, citing sources, and checking results. Use it when you want to delegate bundle work to an agent. MCP tools are connected separately.

## Prerequisites {#prerequisites}

You need the OKF checkout, Go 1.25.5 or newer, and an authenticated Codex CLI supporting project skills. This example installs the skill into a separate training directory without changing your working project.

## Install into a project {#install}

```sh
codex --version
repo_root=$(pwd)
skill_demo_dir=$(mktemp -d)
go build -o "$skill_demo_dir/okf" ./cmd/okf
mkdir -p "$skill_demo_dir/.agents/skills"
cp -R skills/open-knowledge-format "$skill_demo_dir/.agents/skills/open-knowledge-format"
cp -R examples/project-knowledge/en "$skill_demo_dir/knowledge"
export PATH="$skill_demo_dir:$PATH"
cd "$skill_demo_dir"
codex
```

Run from the checkout root; `repo_root` stores the path for returning later. Copy the whole skill directory: its references point to nested instructions and the specification. In Codex, open `/skills` and find `open-knowledge-format`. If it is missing, start a fresh session from `skill_demo_dir` and check `.agents/skills/open-knowledge-format/SKILL.md`.

## First use {#use}

Invoke the skill explicitly in your prompt:

> $open-knowledge-format Read the knowledge bundle. When should an operator get involved after failed delivery? Open retry-policy.md and source-material.md, cite both documents. Run okf validate --path knowledge --spec 0.2 --strict --check-links --check-orphans --max-warnings=0 and explain the result. Do not change files.

Expect the agent to read the skill instructions and both documents, then actually run the command. Its answer should state 24 hours and cite the source; validation should report no errors or warnings. The skill listing confirms discovery; the instruction read and command log confirm actual use.

After changing the deadline to 48 hours in both documents, repeat the request. The agent should read the current files rather than repeat the earlier answer.

## Other tasks {#tasks}

- Author a note: supply concrete source material and ask the agent to link claims to sources.
- Move from 0.1 to 0.2: first request a plan using the [migration guide]({{ '/migration/' | relative_url }}).
- Review knowledge after changes: use the separate `okf-maintain` skill and the [upkeep guide]({{ '/knowledge-upkeep/' | relative_url }}).
- Reconstruct knowledge from Git: the separate `okf-backfill` skill guides history and evidence into a reviewed plan; apply changes after reviewing it.

## If something fails {#troubleshooting}

If `okf` is missing, check `PATH` in the shell launching Codex. If the agent merely described validation, request the actual command and output. For MCP access, follow the [separate walkthrough]({{ '/getting-started-mcp/' | relative_url }}).

The skill instructions and formal specification are available in the [document library]({{ '/readings/skills/open-knowledge-format/SKILL/' | relative_url }}). The skill package version, program release, and document `okf_version` are independent. Record producers and checks only from actual actions; embedded computation resources execute only in a separately selected and authorized runtime.

[Official Codex skill instructions](https://learn.chatgpt.com/docs/build-skills). Project directories load from `.agents/skills` between the current directory and repository root.
