---
title: Section search v1
description: Lexical search with source-pinned line ranges.
permalink: /contracts/section-search/
---

# Section search v1

`okf search <bundle> --query TEXT [--limit N] [--json]` and MCP
`search_sections` share the Go `retrieval` package. They are additive;
`search_concepts` keeps its literal-substring contract.

Input: a readable bundle, query of 1–512 Unicode characters containing at least
one letter or number, limit 1–100 (default 20). At most 32 distinct terms.
Normalized terms are limited to 4096 Unicode characters each, independently
of the original query limit: case folding can expand characters such as ß.
Tokenization applies Unicode case folding and NFC, splits on anything except
letters, numbers and combining marks. Repeated terms are deduplicated. All
terms must occur in a section's raw Markdown, heading or concept title (AND).
No stemming, synonyms, translation, regex, semantic search or stopword removal.
Markdown code is searchable inert text; fenced pseudo-headings never create
sections. Reserved index/log files are navigation and excluded from search.

Sections are non-overlapping spans: the preamble followed by each parser-owned
heading through the line before the next heading. Empty spans are omitted.
Headings, including Setext and nested headings, are owned by Goldmark AST;
frontmatter boundaries use the shared documentlayout parser. Source lines are
1-based physical LF-delimited lines of the original captured file (CRLF is one
line), inclusive. A heading belongs to its section. Heading-free documents have
one body section. Heading and title are limited to 1024 UTF-8 bytes each.

Ranking uses BM25, k1=1.2, b=0.75 over sections, with term frequency weights:
raw section Markdown 1, heading 3, concept title 2. Document frequency is the
number of sections containing a term; length is the sum of weighted frequencies.
Scores descend; ties sort by concept ID then starting line. Scores indicate
lexical relevance only. No trust, verification or freshness is inferred.

Output: query, normalized distinct terms, snapshot_revision, total, truncated,
and hits. Each hit contains concept_id, bundle-relative path, heading,
locator (percent-encoded path plus `#Lstart-Lend`), line_start, line_end,
content_sha256 of the entire original file, exact snippet (at most 512 runes
from the source span, centered near the first matched body term where possible),
score and matched_terms. A locator names a range; it does not promise that every
Markdown renderer supports line anchors. JSON always contains arrays, including
when no hit exists. CLI text prints path/lines/snippet; JSON is the machine contract.

snapshot_revision is lowercase SHA-256 of canonical JSON for sorted captured
file records `{path,size,sha256}`, with struct key order as shown. It is a read
snapshot fingerprint, not a store revision, proof or authentication token.
Results come from captured bytes, never a second live read. After any edit,
repeat search or compare the file digest before using a locator; the same line
number in a changed file is not evidence. CLI and MCP use the same snapshot
fingerprint for the same bytes, independent of absolute root location.

There is no persistent index or cursor. `total` counts all matching sections;
`truncated` means omitted hits. Raise limit to 100 or refine the terms. If that
cannot enumerate all results, report the limitation. All calls rebuild the
in-memory index. Limits: 10,000 revision-visible files, 64 MiB aggregate captured
bytes, 16 MiB per file, 100,000 sections, 1,000,000 indexed tokens, depth 64,
path 4096 bytes, 1 MiB JSON response. Exceeding a limit or cancellation fails
without partial success. Parse errors block search. Loading uses the existing
no-follow FileSystemSource; symlinks inside the tree are excluded, symlink roots
and attempted escape are rejected. Content is never executed.

MCP input/output schemas under `internal/mcpserver/contracts/search_sections.*`
are the wire contract. Existing error envelopes carry `schema_validation` for
bad input, `resource_limit` for caps, and the established bundle-loading and
cancellation codes. CLI errors exit nonzero and write no bundle data.
