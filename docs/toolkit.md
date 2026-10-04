---
title: "Working with knowledge"
description: "Working with knowledge"
permalink: /toolkit/
---

{% include nav.html %}

<span id="toolkit"></span>

# Work with project knowledge {#page-top}

This guide covers everyday operations: create a bundle, validate it, find a rule, prepare ordinary Markdown, and inspect links. Run from the checkout root; you need Git and Go 1.25.5 or newer.

## Create a bundle {#create}

```sh
work_dir=$(mktemp -d)
go build -o "$work_dir/okf" ./cmd/okf
"$work_dir/okf" init "$work_dir/new-knowledge"
```

This creates a root `index.md` and a starter note. Open them and replace the starter text with your rule. `type` defines the note type; only the root index declares the bundle `okf_version`. Add sources from supplied material.

For a ready example:

```sh
cp -R examples/project-knowledge/en "$work_dir/knowledge"
```

## Validate, search, and open {#read}

```sh
"$work_dir/okf" validate --path "$work_dir/knowledge" --spec auto --strict --check-links --check-orphans --max-warnings=0
"$work_dir/okf" search "$work_dir/knowledge" --query "delivery operator" --limit 5
"$work_dir/okf" view "$work_dir/knowledge" --output "$work_dir/knowledge.html" --lang en
"$work_dir/okf" info "$work_dir/knowledge" --spec auto
"$work_dir/okf" parse "$work_dir/knowledge/retry-policy.md" --format json
```

Expect a clean report, delivery/operator sections, an offline HTML file, a bundle summary, and a parsed note. `search` returns snippets and lines; its score reflects word matches. Words absent from the text in the same language produce no matches.

`--spec auto` uses the root declaration; without one it defaults to 0.2 with compatibility rules. Explicit `--spec 0.1` or `0.2` asserts a version: a conflicting or malformed declaration is not silently replaced. Unknown future versions support best-effort reading with the declaration and compatibility information retained.

## Prepare existing Markdown {#setup}

`setup` copies selected Markdown into a new bundle. First create two ordinary files in a temporary folder:

```sh
mkdir "$work_dir/team-notes"
cat > "$work_dir/team-notes/retries.md" <<'EOF'
# Retry policy
Retry failed delivery for 24 hours, then involve an operator.
[Operator steps](operator.md)
EOF
cat > "$work_dir/team-notes/operator.md" <<'EOF'
# Operator steps
Check the cause before another attempt.
EOF
"$work_dir/okf" setup --source "$work_dir/team-notes" --target "$work_dir/imported-knowledge" --type Guide
```

This is a preview: the target folder has not been created. Expect two documents, `applicable=true`, `published=false`, and a `Plan digest`. `Guide` is your explicit type choice for files without one. Read the plan and diagnostics, then copy the value after `Plan digest:`:

```sh
printf 'Paste the reviewed plan digest: '
read -r plan_digest
"$work_dir/okf" setup --source "$work_dir/team-notes" --target "$work_dir/imported-knowledge" --type Guide --apply --plan-digest "$plan_digest"
"$work_dir/okf" validate --path "$work_dir/imported-knowledge" --spec 0.2 --strict --check-links --check-orphans --max-warnings=0
"$work_dir/okf" view "$work_dir/imported-knowledge" --output "$work_dir/imported-knowledge.html" --lang en
```

Expect `published=true`, a clean validation report, and two linked documents in HTML. The source folder is preserved. Changed source or options require a new plan. An existing target, unsupported assets/links, malformed metadata, and symlinks block preparation; choose a new target for another apply. Conditions and limits are in the [Markdown preparation contract]({{ '/contracts/markdown-setup/' | relative_url }}).

## Inspect links and format files {#graph}

```sh
"$work_dir/okf" graph "$work_dir/knowledge" --format mermaid
"$work_dir/okf" fmt "$work_dir/knowledge/retry-policy.md"
"$work_dir/okf" index "$work_dir/knowledge" --spec auto
```

The graph shows document links. `fmt` without `-w` prints output for review; `fmt -w` writes it. `index` updates the bundle index—review the changes in Git. These commands do not migrate documents or add verification metadata. YAML `relations` is an implementation extension; ordinary Markdown links are part of the format.

## Transitions and automation {#automation}

For 0.1 → 0.2 use the [migration guide]({{ '/migration/' | relative_url }}). For APIs and all 14 MCP tools, including `search_sections`, see the [reference]({{ '/reference/' | relative_url }}). Use preview/apply pairs for MCP mutations; copy the digest and revision from the returned plan rather than inventing them.

In Go, use the contracts of `bundle`, `validator`, `retrieval`, `setup`, `viewer`, `graph`, `mutation`, and `store`; CLI and MCP expose their operations. For temporal checks provide explicit `--as-of`: a date for the default profile, or an offset timestamp for `instant-0b87c52`.

## If something fails {#troubleshooting}

For a version mismatch, correct the declaration or selected mode after inspecting the source documents. For warnings, read the report: `--max-warnings=0` makes any warning fail the command. For an existing HTML output, choose a new path or explicitly use `--overwrite`. `okf help` and the reference list all commands and options.
