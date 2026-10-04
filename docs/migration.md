---
title: Move an OKF 0.1 bundle to 0.2
description: Preserve legacy notes and explicitly connect their citations to sources.
permalink: /migration/
---

{% include nav.html %}

# Move an OKF 0.1 bundle to 0.2 {#page-top}

Migration moves legacy `timestamp` to `generated.at` and legacy `# Citations` to `sources` with keyed footnotes. It is for the owner of an existing v0.1 bundle. For ordinary Markdown without OKF, use [Markdown preparation]({{ "/toolkit/" | relative_url }}#setup). Reading or formatting a legacy file does not migrate it.

## Before you start {#before}

Complete the source build in the [quickstart]({{ "/quickstart/" | relative_url }}) in the same shell, then make its binary available to the commands below:

```sh
export PATH="$demo_dir:$PATH"
okf version
```

Make a backup of the real bundle and identify its producer and the materials behind each citation. Do not substitute the migration date for the original creation date.

The example below uses **fictional training requirements**: failed delivery attempts are retried for 24 hours, then an operator takes over. Run it in a new temporary directory; the source files you create here are the provided materials, not evidence about a real product.

## Create the legacy example {#example}

```sh
migration_demo=$(mktemp -d)
cd "$migration_demo"
mkdir old-knowledge
cat > old-knowledge/index.md <<'EOF'
---
okf_version: "0.1"
---

# Notes

* [Delivery retries](retries.md)
* [Training requirements](requirements.md)
EOF
cat > old-knowledge/requirements.md <<'EOF'
---
type: Note
title: Training requirements
timestamp: 2026-06-01T10:00:00Z
---

# Training requirements

Retry delivery for at most 24 hours; then hand it to an operator.
EOF
cat > old-knowledge/retries.md <<'EOF'
---
type: Note
title: Delivery retries
timestamp: 2026-06-01T10:00:00Z
---

# Delivery retries

Retry delivery for at most 24 hours; then hand it to an operator.[1]

# Citations

[1] [Training requirements](requirements.md)
EOF
okf validate --path old-knowledge --spec 0.1
```

## Name the source explicitly {#mapping}

The claim ends with `[1]`. Map that number in `retries.md` to the stable source ID `requirements`. `path` is a relative file path, not a concept ID. The top-level JSON value is an array.

```sh
cat > citation-mappings.json <<'EOF'
[
  {"path":"retries.md","entries":[
    {"legacy_number":1,"source_id":"requirements","title":"Training requirements","resource":"requirements.md"}
  ]}
]
EOF
```

## Preview and review {#preview}

`human:trainer` is the known producer of this fictional example. In a real bundle, use the actual producer; transaction ownership is a separate concern. Preview without that value reports a manual action if conversion needs it.

The helper below saves a read-only source fingerprint outside the bundle. Copy `source_sha256` from `migration-inputs/preparation-report.json` at the prompt, then preview with the filled mapping. The helper also provides blank templates for larger real migrations; it never approves a source identity for you.

```sh
okf migrate-prepare old-knowledge --output-dir migration-inputs --format json
cat migration-inputs/preparation-report.json
printf 'Paste source_sha256 from the report: '
read -r source_sha256
okf migrate old-knowledge --to 0.2 --actor human:trainer \
  --citation-mappings citation-mappings.json \
  --prepared-source-sha256 "$source_sha256" --format json
```

Check that only expected documents change: root version becomes `0.2`, legacy time gains the known producer, and citation 1 becomes source `requirements` and footnote `[^requirements]`. The old timestamp is retained by CLI policy. If blocked, resolve the reported manual actions and preview again.

## Apply the reviewed inputs {#apply}

Keep the input files and source bundle unchanged between review and apply. CLI rebuilds and checks its plan internally; `--prepared-source-sha256` additionally rejects changes to the prepared source. After a source change, prepare again in a new directory and review again.

```sh
okf migrate old-knowledge --to 0.2 --actor human:trainer \
  --citation-mappings citation-mappings.json \
  --prepared-source-sha256 "$source_sha256" --write --format json
okf validate --path old-knowledge --spec 0.2 --strict
cat old-knowledge/retries.md
okf view old-knowledge --output migrated.html --lang en
```

Expected result: root `index.md` declares `0.2`, the note has `generated.by: human:trainer`, source `requirements` points to `requirements.md`, and the claim uses a keyed footnote. Unknown fields and untouched text survive. Format validation checks structure; compare the content with the supplied requirements.

## Common blocked cases {#failures}

| Situation | Next action |
| --- | --- |
| Producer or generation time is unknown | Recover it from actual records; do not guess. A type-only migration without timestamps needs neither. |
| Duplicate/unnumbered citation cannot be selected | Inspect the preparation report; resolve ambiguity in the original, then prepare again. |
| `sources` and legacy `# Citations` coexist | Reconcile the two representations explicitly; the tool does not merge them. |
| Prepared source changed | Use a new preparation directory and review the new plan. |
| Wrong/missing document path | Correct the path; a mapping does not create a missing document. |

Preview/rejection does not write the bundle or create `.okf`. A real CLI `--write` may create a transaction receipt there, including for an already-converted bundle. See [exact migration rules]({{ '/reference/' | relative_url }}#migration-input), [selector rules]({{ '/reference/' | relative_url }}#citation-mappings) and [publication/no-op behavior]({{ '/reference/' | relative_url }}#migration-writes).

## Upgrading calendar dates within v0.2 {#temporal-upgrade}

A separate operation upgrades the old v0.2 calendar-date fields to offset-bearing datetimes. Supply a real time and timezone for every observed value; the tool never invents midnight. Follow the [temporal upgrade reference]({{ "/reference/" | relative_url }}#temporal-upgrade), keeping its preview revision and digest. This does not change `okf_version` to a new number.
