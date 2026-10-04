---
title: Try OKF locally
description: Open a useful example and create a sourced note from supplied material.
permalink: /quickstart/
---

{% include nav.html %}

# Try OKF

## 1. Explore existing knowledge

[Open the example without installing anything]({{ '/demo/knowledge.html#architecture' | relative_url }}). This is a snapshot of knowledge about this repository. In **Package boundaries**, answer “Where should an implementation change go?”:

- CLI and MCP expose package contracts rather than inventing different OKF rules.
- **Sources** lists Toolkit guide and Go module declaration.
- **Mutation boundary** and **Version resolution** link to related notes.

Verification, status, and staleness appear separately. The note is `draft`; missing `verified` means no check is recorded. Missing `stale_after` prevents a staleness calculation.

## 2. Reproduce it locally

Requires Git, Go **1.25.5 or newer**, a network connection to download dependencies, and a browser. These commands use a POSIX shell (macOS/Linux). Start in a directory that does not already contain `okf`. After `cd okf`, run all commands from the clone root.

```sh
git clone https://github.com/skosovsky/okf.git
cd okf
go version
demo_dir=$(mktemp -d)
./scripts/build-viewer-demo.sh "$demo_dir/knowledge-demo.html"
printf '%s\n' "$demo_dir/knowledge-demo.html"
```

The script validates `knowledge/`, then prints the HTML path. Open that file with your browser's Open File command or file manager. Find `Package boundaries`, inspect its sources, and follow **Mutation boundary**. These are meaningful notes linked to repository files.

`mktemp` creates a separate directory; the HTML goes into a new file outside the bundle. Use a new `demo_dir` when repeating: existing output is not overwritten. The script fixes `2026-09-26` as the staleness reference date for reproducibility; that is not a factual verification date. The HTML embeds its data and works offline. External sources need a connection when opened.

## 3. Write a note of your own

Continue in the same shell at the clone root with `demo_dir` still set. The supplied material is fictional and only for this exercise: “The service retries failed job delivery for 24 hours. After that, an operator must review the job before another attempt.”

Create both the supplied material and a note that cites it. `init` requires a new directory under an existing parent. The `rm` command removes only the placeholder it just created in our temporary directory.

```sh
go run ./cmd/okf init "$demo_dir/my-knowledge"
cat > "$demo_dir/my-knowledge/source-material.md" <<'EOF'
---
type: Source Material
title: Training service requirements
---
# Supplied material
This fictional service retries failed job delivery for 24 hours.
After that, an operator must review the job before another attempt.
EOF
cat > "$demo_dir/my-knowledge/retry-policy.md" <<'EOF'
---
type: Operational Note
title: Retry limit
sources:
  - id: requirements
    resource: source-material.md
    title: Training service requirements
---
# How long should delivery retry?
Retry failed job delivery for 24 hours. Then request operator review.[^requirements]

See the [supplied requirements](source-material.md).

[^requirements]: Training service requirements, Supplied material.
EOF
rm "$demo_dir/my-knowledge/getting-started.md"
cat > "$demo_dir/my-knowledge/index.md" <<'EOF'
---
okf_version: "0.2"
---
# Concepts
* [Training service requirements](source-material.md) - Supplied fictional material.
* [Retry limit](retry-policy.md) - A note derived from the supplied requirements.
EOF
go run ./cmd/okf validate --path "$demo_dir/my-knowledge" --spec 0.2 --strict --check-links --check-orphans --as-of 2026-09-26 --max-warnings=0
go run ./cmd/okf view "$demo_dir/my-knowledge" --output "$demo_dir/my-knowledge.html" --spec 0.2 --as-of 2026-09-26
```

Expected result: validation without errors or warnings, then a new HTML page with two notes. Open `my-knowledge.html`, find **Retry limit**, and inspect **Training service requirements**. “When is operator review needed?” has the answer: after 24 hours of failed attempts. No producer, verification, or freshness is invented. Successful validation confirms the file structure.

## 4. Read from an agent

[Connect MCP]({{ '/getting-started-mcp/' | relative_url }}) and ask the agent for an answer with its source. Installing a skill and connecting the server are separate steps.

After your first run: [review after changes](https://github.com/skosovsky/okf/blob/main/docs/knowledge-upkeep.md), [reconstruct from Git](https://github.com/skosovsky/okf/blob/main/backfill/PROTOCOL.md), [full reference]({{ '/reference/' | relative_url }}).

This example checks CLI and viewer behavior. It does not establish improved model answers or automatic tool use by an agent.

## 5. Find a section with several words

From the clone root:

```sh
go run ./cmd/okf search knowledge --query "implementation change" --limit 5
go run ./cmd/okf search knowledge --query "implementation change" --limit 5 --json
```

Results include a section of `architecture.md`, a source snippet, and line numbers. A `locator` identifies the source path and line range; not every Markdown viewer opens line anchors. JSON also includes file and snapshot digests. Repeat search after an edit: old line numbers no longer identify the same evidence.

Every query word must occur in the same section, its heading, or the note title. Search normalizes Unicode and case; it does not translate words or find synonyms. Its score measures lexical relevance, not trust or freshness. If `truncated` is true, refine the query or raise `--limit` up to 100. See the [full search contract]({{ '/contracts/section-search/' | relative_url }}).

MCP exposes the same operation as `search_sections`. Existing `search_concepts` still searches one literal substring.

## 6. Prepare existing Markdown

This fictional exercise copies ordinary notes into a separate new bundle, leaving the source unchanged. Continue in the same shell with `demo_dir` set:

```sh
mkdir "$demo_dir/team-notes"
cat > "$demo_dir/team-notes/retries.md" <<'EOF'
# Retry policy
Retry failed delivery for 24 hours, then request operator review.
See [operator steps](operator.md).
EOF
cat > "$demo_dir/team-notes/operator.md" <<'EOF'
# Operator steps
Review the failed job before another delivery attempt.
EOF
go run ./cmd/okf setup --source "$demo_dir/team-notes" --target "$demo_dir/imported-knowledge" --type Guide
```

This is a preview: no target is created yet. Expect two documents, `applicable=true`, `published=false`, and a `Plan digest`. Review the plan and diagnostics. `Guide` is an explicit choice because these notes have no metadata. No producer, source, or verification is invented.

Copy the value after `Plan digest:`. The following commands ask for it and apply that exact plan:

```sh
printf 'Paste the reviewed plan digest: '
read -r plan_digest
go run ./cmd/okf setup --source "$demo_dir/team-notes" --target "$demo_dir/imported-knowledge" --type Guide --apply --plan-digest "$plan_digest"
go run ./cmd/okf validate --path "$demo_dir/imported-knowledge" --spec 0.2 --strict --check-links --check-orphans --max-warnings=0
go run ./cmd/okf view "$demo_dir/imported-knowledge" --output "$demo_dir/imported-knowledge.html" --spec 0.2
```

Expect `published=true`, successful validation, and HTML with two linked notes. Changed source material or options require a new preview. Existing targets are never replaced; repeating a successful apply requires a new target. Local links must resolve to selected Markdown files or anchors. Assets, unsupported links, malformed metadata, and symlinks block setup rather than disappearing silently. Failure before publication leaves no partial target. See the [Markdown setup contract]({{ '/contracts/markdown-setup/' | relative_url }}).
