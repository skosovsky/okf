# `okf init` CLI contract

`okf init <directory> [--json]` creates a new OKF 0.2 bundle. The directory
argument is required and explicit. Its parent must already exist. The command
does not choose `.okf` as a default path; inside an existing bundle, `.okf` is
reserved for private store state.

The command creates exactly two UTF-8 Markdown files:

- `index.md`: root `okf_version: "0.2"` declaration and a link to the concept;
- `getting-started.md`: a `type: Guide` concept with `status: draft` and an
  explicit placeholder body.

There is no `log.md`: a synthetic initialization event would add no useful
history. The template contains no invented source, verification, actor, or
temporal metadata. Replace the draft with domain knowledge and add provenance
only when supported by actual materials.

On success, text mode writes `Created OKF v0.2 bundle at <directory>` followed
by a newline. `--json` writes one JSON object with `path`, `okf_version`, and
`files` (`["index.md","getting-started.md"]`). The `path` value is the
lexically cleaned supplied path; the command does not resolve it to an
absolute path. Success exits 0 with empty stderr. Usage, filesystem, and
publication errors exit 1, write one `error: ...` line to stderr, and write no
stdout. The command accepts no other flags or positional arguments.

The target must be absent. Any existing file, directory (empty or populated),
or symlink is a conflict, including on a repeated invocation. There is no
`--force`. The command stages the two files under a random sibling directory,
syncs them, and atomically renames the complete directory to the target using
the toolkit's no-replace publication primitive. A failed stage, cancelled
operation (including SIGINT/SIGTERM before rename), or rename conflict leaves
no new bundle and preserves the existing target. Signal cancellation is
cooperative: after rename, the completed bundle may already be published.
On a parent-directory sync failure after rename, the error explicitly
says the bundle was published; the visible bundle is complete and may require
a durability check. The implementation does not use the bundle store or create
a second journal.

The scaffold passes base OKF validation. Strict diagnostics may encourage
further authoring; the draft placeholder is intentionally not verified.
