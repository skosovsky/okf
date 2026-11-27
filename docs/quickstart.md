---
title: "Quickstart"
description: "Quickstart"
permalink: /quickstart/
---

{% include nav.html %}

<span id="try-okf"></span>

# Your first sourced rule {#page-top}

The fictional OKF 0.3 bundle records a change in the delivery deadline from 24 to 48 hours.

## Prerequisites {#prerequisites}

Use Git, Go 1.25.5 or newer, and a browser. Run the commands in one shell.

```sh
git clone https://github.com/skosovsky/okf.git
cd okf
demo_dir=$(mktemp -d)
go build -o "$demo_dir/okf" ./cmd/okf
cp -R examples/project-knowledge/en "$demo_dir/my-knowledge"
```

## Inspect the rule {#create}

Open `retry-policy-current.md`. The rule gives the **48-hour** deadline. The `[^requirements]` footnote names the source, and the link opens `source-material.md`.

## Validate and open {#validate}

```sh
"$demo_dir/okf" validate --path "$demo_dir/my-knowledge" --spec 0.3 --strict --check-links --check-orphans --as-of 2026-11-27 --max-warnings=1
"$demo_dir/okf" view "$demo_dir/my-knowledge" --output "$demo_dir/my-knowledge.html" --as-of 2026-11-27 --lang en
```

Expect zero errors and one warning for the retired rule’s stale date. Open `my-knowledge.html` and select **When to involve an operator**. The review applies to the current text. **Stale after** is the separate deadline for revisiting it.

## Find the answer {#search}

```sh
"$demo_dir/okf" search "$demo_dir/my-knowledge" --query "48 hours operator" --limit 5
```

Open `retry-policy-current.md` at the returned lines and compare it with the source.

## Follow the change {#update}

```sh
"$demo_dir/okf" search "$demo_dir/my-knowledge" --query "previous rule required" --current-only
```

The match points to the original 24-hour text and names `retry-policy-current` as its replacement. In the viewer, open **Previous delivery rule** and select **Open replacement**. The old human review predates a meaningful edit, so it stays in the history.

## If something fails {#troubleshooting}

For an existing HTML file, choose a fresh path or pass `--overwrite`. Use query words from the English bundle.

Next: [prepare your Markdown]({{ '/toolkit/' | relative_url }}#setup), [install the skill]({{ '/skill/' | relative_url }}) or [connect MCP]({{ '/getting-started-mcp/' | relative_url }}).
