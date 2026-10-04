---
title: MCP concept queries
description: Search and neighbor result bounds, continuation, and temporal observation.
permalink: /contracts/concept-queries/
---

{% include nav.html %}

# MCP concept queries (v1)

`search_concepts` and `get_neighbors` are read-only tools over one loaded bundle snapshot. Their checked-in JSON Schema files under `internal/mcpserver/contracts/` are the wire contracts. Every call uses the same root pinning, parse-error handling, input validation, error envelope, and output budget as the older MCP tools.

## Search

Required inputs: `bundle_path`, a non-whitespace `query`. `limit` defaults to 20 and must be 1–100. Matching is a literal substring after Unicode case folding and NFC normalization. It is not token, fuzzy, semantic, or embedding search. A concept matches if the query appears in its canonical ID, title, description, tags, or Markdown body. The highest-priority matching field determines `match`: ID, title, description, tags, body. Results sort by that field priority, then canonical concept ID. `total` counts all matches; `hits` contains at most `limit`; `truncated` reports omitted hits.

## Neighbors

Required inputs: `bundle_path`, canonical `concept_id`. `direction` defaults to `both`; `kinds` defaults to `navigation`, `relation`, and `source`; `limit` defaults to 20 and must be 1–100. `navigation` denotes resolved Markdown concept links, including relative and bundle-root targets. `relation` denotes resolved typed `relations` extension edges; their `from`/`to` preserve parser-owned escaped concept and fragment identities, and `relation_type` records the declared type. Broken links and unresolved relations are excluded. `source` denotes provenance `sources[].resource` entries and is returned in `sources`, never as a concept edge. Code-owned pseudo-links are excluded by the Markdown parser.

`total` counts selected concept edges before the limit; `edges` is capped at `limit`; `truncated` reports omitted edges. Edges sort by kind, source identity, target identity, then relation type. Provenance sources are outbound and appear only for `out` or `both`; they sort by ID, resource, and title. More than 100 provenance sources returns `resource_limit` instead of silently dropping them. The query rejects a neighborhood exceeding 100,000 candidate edges. V1 has no cursor or pagination: callers can narrow direction/kinds or use the existing graph tool for complete traversal.

## Continuing bounded reads

Use `search_concepts` first with a distinctive literal substring from the question. If `truncated` is true, inspect `total`, raise `limit` up to 100 when useful, or issue a more specific literal query based on an ID, title, tag, or phrase. The query is one substring, not an AND expression. Read only selected IDs with `read_concept`, then request their bounded `get_neighbors`. A concept beyond the first broad result remains addressable by its canonical ID and can be found by a distinguishing query. `list_concepts` can expose IDs when no query term is known, but returns the whole list and can be large; it is not a page/cursor. Do not claim that an omitted hit was inspected.

For a truncated neighborhood, filter `direction` and `kinds`, increase `limit` up to 100, or use `get_semantic_graph` if the task requires complete traversal and its output fits. `total/truncated` refer to concept edges; provenance `sources` are a separate group with their own hard cap of 100. If an undifferentiated result set cannot be narrowed and the whole graph exceeds its output limit, v1 cannot enumerate the omitted results. Report that limit rather than claiming completeness. A cursor or range-read would require a separate schema-first contract covering ordering, snapshot binding, root, expiry, and cancellation.

Current server bounds: bundle loading at most 10,000 files and 10,000 concepts, 64 MiB aggregate bundle bytes, 64 MiB per structured JSON or compatibility text output, 16 MiB per concept read payload and per graph renderer, 1 MiB concept frontmatter, search/neighbors 20 default and 100 maximum, 100 provenance sources per selected concept, 100,000 candidate neighbor edges. `resource_limit` is a failed call, not a partial page; a bundle over the file/concept cap needs a smaller bundle, since query narrowing cannot bypass loading. Context cancellation also fails the call without partial success. The compatibility text fallback remains alongside structured content and may be smaller or larger depending on the tool.

## Temporal observation and bounds

`temporal_profile` defaults to `date-3fcbb9f`. Its `as_of` value must be a `YYYY-MM-DD` date. Explicit `instant-0b87c52` requires RFC3339 datetime with a known UTC offset. The response repeats the profile and reference time. Each concept card reports status, status state, trust, stale-after, and stale independently. Without `as_of`, `stale` is null. An unparseable or absent stale-after value also produces null rather than an invented freshness claim.

Bundle loading is capped at 10,000 concepts and the existing MCP byte limits. Search and neighbor responses are schema-validated and capped by the common MCP output budget. Context cancellation returns an error, not a partial success. The existing nine tool names and contracts remain available; the server also has a temporal-upgrade preview/apply pair and the additive `search_sections` tool, for fourteen tools total.
