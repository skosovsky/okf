---
layout: default
title: "OKF operational recipes — reading edition"
lang: en
permalink: /readings/skills/open-knowledge-format/references/operational-workflows/
document_id: skills-open-knowledge-format-references-operational-workflows
---

{% include nav.html %}

This is a full reading edition of the runtime instruction. Install the original skill package, not this page. Canonical source: [skills/open-knowledge-format/references/operational-workflows.md](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/skills/open-knowledge-format/references/operational-workflows.md). Verified source revision: `61e75e9aa9a8719dfb480bf1f5226553b3a21d70`.

# OKF operational recipes
{: #section-1 }

Use these recipes when the request is to check or show an existing bundle.
They are part of `open-knowledge-format`, not separate one-command skills. The
tool's live schema and the [pinned format spec]({{ "/readings/skills/open-knowledge-format/references/spec-v02/" | relative_url }}) govern semantics.
Commands below assume an installed `okf` CLI; from this repository checkout,
`go run ./cmd/okf` is equivalent. Check availability before choosing a path.
Do not install packages, register hooks, or guess a host-specific MCP prefix.

## Validate and explain a bundle
{: #section-2 }

**Input:** bundle root, desired `--spec auto|0.1|0.2`, temporal profile
(`date-3fcbb9f` by default or explicit `instant-0b87c52`), and optional
reference time. Ask for a warning budget only if the caller/CI needs a policy
threshold. `auto` resolves the root declaration; an explicit selector asserts
it. A malformed present declaration is an error, not a request to assume v0.2.

1. Confirm the root and available tool. Prefer `validate_bundle` from the
   connected OKF MCP server when its live schema covers the requested options;
   otherwise use the CLI. For repeatable evidence, request JSON:

   ```sh
   okf validate --path ./knowledge --spec auto --format json --as-of 2026-09-27
   ```

   For the instant profile use `--temporal-profile instant-0b87c52` and an
   RFC3339 offset datetime for `--as-of`. For the date profile use YYYY-MM-DD.
2. Record declared/effective version, resolution, profile, scanned count,
   `conformant`, errors and diagnostics. Address base errors first. Then, if
   the request calls for quality guidance, run the same snapshot with
   `--strict --check-links --check-orphans`; `--max-warnings 0` is an optional
   **policy budget**, not a different conformance definition. Keep strict
   warnings and budget failure distinct from base errors.
3. Explain each actionable diagnostic with its path/field and the smallest
   supported correction. Re-run after changes and show the final report or
   report path. A successful validator run checks format, not claim truth,
   provenance accuracy, or `verified` status.

**Fallback:** If MCP is absent, check `okf help` (or the checked-out Go CLI).
If the CLI is unavailable or unsupported on this host, inspect root `index.md`
and selected Markdown only; identify this as manual inspection and leave
conformance unverified. On interruption, preserve the last report and bundle
snapshot identity, then rerun on the current bundle; do not reuse a result from
before edits. Read/validate never changes knowledge files.

**Done:** Report names the bundle, selector/profile/reference time, actual
tool run, base outcome, separate optional guidance/budget outcome, and any
remaining diagnostics. No fact-checking claim is implied.

| Request | Actions | Result to show |
| --- | --- | --- |
| “Check the format of `knowledge` as of September 27.” | Resolve root and date profile; run `okf validate --path ./knowledge --spec auto --format json --as-of 2026-09-27`. | Declared/effective version, base `conformant`, errors and diagnostics. |
| “CI must reject every warning.” | Run base check, then `okf validate --path ./knowledge --spec auto --strict --check-links --check-orphans --max-warnings 0 --format json`. | Base conformance and the separate warning-budget policy result; full JSON artifact when available. |
| “Why does this bundle fail validation?” | Inspect existing JSON diagnostics; narrow to cited paths, correct authorized files, rerun with same flags. | Before/after diagnostic counts, changed files, unresolved findings; never `verified` from validation. |

## Export and inspect an offline viewer
{: #section-3 }

**Input:** bundle root, output HTML path, version/profile/reference time and
whether replacing an existing output is authorized. Resolve version as above.
The viewer is a local read-only export; it does not execute bundle assets or
send content to a server.

1. Confirm the CLI with `okf help`, or from this checkout use `go run ./cmd/okf`.
   Choose a fresh output path outside the bundle and `.okf/` store. The exporter
   refuses an existing file unless `--overwrite` is supplied; request/derive
   replacement authority from the task before passing it. Do not silently
   replace an artifact.
2. Export with actual flags, for example:

   ```sh
   okf view ./knowledge --output /tmp/knowledge-viewer.html --spec auto \
     --as-of 2026-09-27
   ```

   For instant profile use `--temporal-profile instant-0b87c52` and an offset
   datetime. Without `--as-of`, staleness remains `unevaluated`.
3. Open the **local file** in a browser, check concept list/search, a selected
   detail, navigation links, provenance and trust/status/staleness labels. Show
   or link the HTML artifact. Export success alone is not visual inspection and
   does not verify concept claims.

**Fallback:** There is no MCP viewer export in the current 14-tool catalog.
If CLI/build/browser is missing or unsupported, report that limitation and
provide selected Markdown/graph results if available. Do not fetch a remote
viewer, upload bundle content, or pretend a local HTML was inspected. On
interruption, check whether the output exists and rerun only after resolving
collision authority. Read/view leaves knowledge files byte-identical.

**Done:** State bundle/version/profile/as-of, output path, collision decision,
export result, and which browser checks actually ran. Keep a failed or
uninspected artifact labelled accordingly.

| Request | Actions | Result to show |
| --- | --- | --- |
| “Show the bundle as offline HTML.” | Export to a new `/tmp/knowledge-viewer.html`, open that local file, inspect list and concept details. | Path to HTML plus observed navigation/provenance state. |
| “Rebuild the existing viewer after the edit.” | Check authority to replace, use `--overwrite`, inspect the newly written file. | Updated artifact path and visual check; no claim that claims became verified. |
| “Show staleness at a precise instant.” | Select instant profile, supply offset datetime `--as-of`, inspect labels. | Chosen reference instant and observed labels; no browser-clock inference. |

## Knowledge upkeep fallback
{: #section-4 }

Use the separate `okf-maintain` skill when installed. This fallback is for a
host that has only this skill and a repository checker. Upkeep is an optional
repository policy outside OKF conformance: capture `okf-upkeep baseline`
**before** code edits, read only relevant concepts, compare their claims with
current code/contract, then update affected concepts or record a concrete
unaffected reason. Run `okf-upkeep check` after the final edit and bind the
decision to its current fingerprint. The commands and JSON decision shapes
are in the repository's `docs/knowledge-upkeep.md` or the installed checker's
contracts. A log edit, validator pass, or old fingerprint is not evidence of
review. If baseline was missed, say so and review the intervening diff
manually; do not claim a fingerprint-bound completion.

