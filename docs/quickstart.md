---
title: "Quickstart"
description: "Quickstart"
permalink: /quickstart/
---

{% include nav.html %}

<span id="try-okf"></span>

# Your first sourced rule {#page-top}

In a few commands, create two Markdown documents, find the delivery rule, open its source, and update the deadline. All requirements in this example are fictional.

## Prerequisites {#prerequisites}

You need Git, Go 1.25.5 or newer, a browser, and a shell with `mktemp` and `cp`. Run all commands in one shell. Keep the repository example as a template and work on a temporary copy.

```sh
git clone https://github.com/skosovsky/okf.git
cd okf
repo_root=$(pwd)
demo_dir=$(mktemp -d)
go build -o "$demo_dir/okf" ./cmd/okf
cp -R examples/project-knowledge/en "$demo_dir/my-knowledge"
```

## Inspect the rule {#create}

Open `retry-policy.md` in your editor. Its metadata gives the note type and source; the footnote connects the claim to the requirements:

```yaml
type: Guide
title: When to involve an operator
sources:
  - id: requirements
    title: Training delivery requirements
    resource: source-material.md
```

The body states a **24-hour** deadline followed by operator review, with `[^requirements]`. `source-material.md` is the supplied training source. The root `index.md` declares `okf_version: "0.2"` and lists both documents.

## Validate and open {#validate}

```sh
"$demo_dir/okf" validate --path "$demo_dir/my-knowledge" --spec 0.2 --strict --check-links --check-orphans --max-warnings=0
"$demo_dir/okf" view "$demo_dir/my-knowledge" --output "$demo_dir/my-knowledge.html" --spec 0.2 --lang en
```

Expected result: no validation errors or warnings, and a `my-knowledge.html` file. Open it in a browser, choose **When to involve an operator**, and follow the **supplied requirements** link in the note body. Alternatively, choose **Training delivery requirements** in the list on the left. The sources section displays material details; use the body link or note list to open the local document. Validation checks structure and local links; compare the deadline with the requirements text.

## Find the answer {#search}

```sh
"$demo_dir/okf" search "$demo_dir/my-knowledge" --query "delivery operator" --limit 5
"$demo_dir/okf" search "$demo_dir/my-knowledge" --query "delivery operator" --limit 5 --json
```

Expect results from the training documents, snippets, and line numbers. Read the matched file: operator review is needed after 24 hours of failed delivery. Search requires every query word in one section, its heading, or the note title. It normalizes case and Unicode but does not translate queries or expand synonyms.

## Change the requirement and note {#update}

Training change: the deadline becomes **48 hours**. In your editor, first replace `24` with `48` in `source-material.md`, then review the rule and make the same change in `retry-policy.md`. Keep the source link. Do not automatically add `verified` or dates: those are separate decisions requiring actual checks.

```sh
"$demo_dir/okf" validate --path "$demo_dir/my-knowledge" --spec 0.2 --strict --check-links --check-orphans --max-warnings=0
"$demo_dir/okf" search "$demo_dir/my-knowledge" --query "delivery operator" --limit 5
"$demo_dir/okf" view "$demo_dir/my-knowledge" --output "$demo_dir/my-knowledge.html" --spec 0.2 --lang en --overwrite
```

Both documents should now state 48 hours. Export replaces the HTML because you explicitly passed `--overwrite`; an already opened page does not update itself. Old line numbers refer to the previous file state.

## If something fails {#troubleshooting}

- `go` is missing: install the required Go version and rebuild.
- The output HTML exists: choose a new name or explicitly use `--overwrite`.
- Search found nothing: use words present in the same language; the English and Russian folders are separate translations.
- The source link fails: check `resource: source-material.md`, the filename, and the root index.

Next: [prepare your Markdown]({{ '/toolkit/' | relative_url }}#setup), [install the skill]({{ '/skill/' | relative_url }}) or [connect MCP]({{ '/getting-started-mcp/' | relative_url }}).
