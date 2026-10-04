---
title: Prepare existing Markdown
description: Preview and apply a managed copy into a new OKF bundle.
permalink: /contracts/markdown-setup/
---

# Managed Markdown setup contract (v1)

`okf setup --source DIR --target NEW_DIR [--files a.md,b/c.md] [--type Guide] [--json]`
previews a managed copy. `--apply --plan-digest SHA256` publishes the exact
preview. No source writes and no existing target replacement are permitted.
`init` is unchanged. Source and target must be disjoint directories; target's
parent must exist. Symlinks and special files in the selected tree are blockers.

Selection defaults to every `.md` file recursively; `--files` is a comma-separated
list of exact slash-relative Markdown paths. Duplicates, missing paths, escaping
paths, reserved index.md/log.md and transaction names block setup. Explicit
selection is useful when an existing ordinary index.md must remain in the source.
Non-Markdown files are not copied. Local links/images must target selected Markdown
or anchors; assets, directories, missing/unselected files, query strings, HTML
blocks/inline HTML and unsupported URI schemes block publication. HTTPS/HTTP,
mailto links and local anchors are retained byte-exactly. No links are rewritten.

Existing valid frontmatter and document bytes are retained exactly. For missing
`type`, a nonempty explicit `--type` is required; a supplied type is added as a
quoted YAML scalar without changing existing YAML bytes, body or unknown metadata.
An existing invalid `type` is a blocker rather than overwritten. Unterminated,
malformed and duplicate YAML keys are blockers. No metadata other than type is
inferred; provenance, verification, freshness and status are never invented.
Indexes are generated with the existing bundle index renderer and root declaration
`okf_version: "0.2"`. The shared parser/validator checks the complete staged bundle.

JSON is the same versioned object for preview/apply: source and target are absolute
paths; files are sorted paths with source/output SHA256; diagnostics are sorted
stable code/path/message objects; applicable indicates no blockers. Digest hashes
canonical JSON of options, files and diagnostics, binding selected bytes, paths,
policy and target. Source inventory/bytes are re-read for apply. Any digest mismatch
is source/plan drift and blocks without publication. Replay after success is a
collision; choose a new target and preview again. No plan JSON is trusted as data.

Limits: 1,000 selected documents, 2 MiB per document, 16 MiB total source document
bytes; no silent truncation. Cancellation is checked at traversal, parsing,
staging and immediately before publication. Errors before atomic no-replace rename
clean up the sibling stage; no partially published bundle exists. Cancellation
observed after publication cannot undo the committed bundle. Parent fsync failure
reports `published: true` plus an error, so callers do not assume rollback.
Filesystem sources must be quiescent during each read; apply rechecks inventory
immediately before publication. No process can guarantee safety from concurrent
source edits after its last read. Content is inert and never executes commands.
