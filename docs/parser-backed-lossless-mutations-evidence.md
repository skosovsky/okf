---
title: Parser-backed lossless mutations implementation evidence
description: Verification commands and performance evidence for parser-backed lossless mutations.
permalink: /parser-backed-lossless-mutations-evidence/
---

# Parser-backed lossless mutations implementation evidence

This record covers the parser-backed lossless mutation phase implemented by
commit [`bb9c169`](https://github.com/skosovsky/okf/commit/bb9c169). That phase
continued the transactional mutation design from
[GitHub issue #1](https://github.com/skosovsky/okf/issues/1). This record
supplements that phase's normative acceptance matrix; it does not relax it.

## Verification commands

Run from the repository root after all implementation files are present:

```sh
go test ./bundle ./mutation
go test ./...
go test -race ./mutation -run '^(TestParserBackedMarkdownCorpus|TestParserBackedYAMLPresentationCorpus|TestPlan_InvalidYAMLReturnsPresentationErrorWithoutStage)$' -count=1
go test -race ./...
go vet ./...
go mod verify
go mod tidy -diff
go test ./mutation -run '^TestPlannerRepeatedPreviewAndValidationSemanticMatrix$' -count=1
go test -run '^$' -bench 'Benchmark(Overlay(Clone|Rename|Paths|ManifestDeltaRevision)|ParserBackedOverlayComparison|PlannerRepresentativeTransaction)$' -benchtime=1x -benchmem ./mutation
go test -run '^$' -fuzz=FuzzYAMLResolver -fuzztime=20s -parallel=1 ./mutation
go test -run '^$' -fuzz=FuzzRewriteMarkdownDestinations -fuzztime=20s -parallel=1 ./mutation
git diff --check
```

The final 2026-07-20 gate passed the complete test, race, vet, module, semantic
matrix, benchmark, and diff commands above. After removing every generated-input
skip and adding the public planner atomicity oracle, `FuzzYAMLResolver` passed
three consecutive 20-second runs; the unrestricted Markdown target passed its
separate 20-second run.

The fuzz targets are intentionally separate commands: Go permits one fuzz
target per `go test` invocation. The deterministic subset now runs in the
repository `CI` workflow. Fuzz and profile commands remain reproducible manual
evidence because they are not on the latency-sensitive pull-request path.

Committed fuzz inputs are present in Go's native corpus encoding and are read
automatically by their corresponding targets:

- `mutation/testdata/fuzz/FuzzRewriteMarkdownDestinations/inline-empty-link-regression-corpus` — empty inline-link panic regression
- `mutation/testdata/fuzz/FuzzRewriteMarkdownDestinations/inline-code-span-ownership-corpus` — link-looking bytes owned by an AST CodeSpan must not compete with the enclosing Link destination
- `mutation/testdata/fuzz/FuzzRewriteMarkdownDestinations/goldmark-fenced-parser-regression-corpus` — Goldmark fenced-code parser panic must become a typed fail-closed Markdown error
- `mutation/testdata/fuzz/FuzzRewriteMarkdownDestinations/invalid-prefix-reference-anchor-corpus` — invalid lexical prefix must not steal the actual Goldmark definition anchor
- `mutation/testdata/fuzz/FuzzRewriteMarkdownDestinations/markdown-corpus`
- `mutation/testdata/fuzz/FuzzRewriteMarkdownDestinations/ordered-mixed-corpus`
- `mutation/testdata/fuzz/FuzzRewriteMarkdownDestinations/repeated-value-callback-corpus`
- `mutation/testdata/fuzz/FuzzYAMLResolver/mixed-sequence-insertion-order-corpus` — mixed scalar/mapping sequence insertion-order regression
- `mutation/testdata/fuzz/FuzzYAMLResolver/multiline-double-quote-insertion-corpus` — multiline double-quoted scalar insertion regression
- `mutation/testdata/fuzz/FuzzYAMLResolver/block-plain-punctuation-corpus` — block-context plain scalars retain flow-only punctuation as data
- `mutation/testdata/fuzz/FuzzYAMLResolver/block-plain-multiline-corpus` — parser-confirmed multiline plain-scalar boundaries
- `mutation/testdata/fuzz/FuzzYAMLResolver/ambiguous-insertion-trivia-corpus` — standalone comment ownership at an AST-sibling insertion boundary must fail closed
- `mutation/testdata/fuzz/FuzzYAMLResolver/ca34eecbb76eb36f` — generated post-insertion YAML syntax locations are remapped to the bounded original insertion range
- `mutation/testdata/fuzz/FuzzYAMLResolver/2b84c7d36903b4e1` and `95edce9880265600` — irregular CR scalar ownership and collection diagnostics retain non-empty source ranges
- `mutation/testdata/fuzz/FuzzYAMLResolver/a9e88922f87ae8de` and `be9bf6b33b1169e5` — empty frontmatter and raw semantic mismatch rejections retain non-empty source ranges
- `mutation/testdata/fuzz/FuzzYAMLResolver/merge-touched-provenance-corpus` — merge-derived `relations`, relation type, `target`, and canonical identity have no unique raw owner and fail closed
- `mutation/testdata/fuzz/FuzzYAMLResolver/non-specific-tag-corpus` — explicit non-specific `!` tags fail closed even though yaml.v3 normalizes their semantic tag
- `mutation/testdata/fuzz/FuzzYAMLResolver/opaque-unrelated-insertion-corpus` — AST-sibling insertion boundaries preserve unrelated complex/merge/block/plain extension subtrees
- `mutation/testdata/fuzz/FuzzYAMLResolver/other-relation-type-corpus`
- `mutation/testdata/fuzz/FuzzYAMLResolver/selector-routes-corpus`
- `mutation/testdata/fuzz/FuzzYAMLResolver/yaml-corpus`

The named regression seeds retain the empty inline-link panic boundary, the
opaque AST descendant ownership boundary, the multiline double-quoted
scalar insertion boundary, and mixed scalar/mapping sequence insertion
ordering respectively.

The targets also retain their explicit `f.Add` seeds in
`mutation/markdown_presentation_test.go` and `mutation/yaml_resolver_test.go`.
Those are committed source seeds (and therefore always available even when a
testdata directory is pruned); the named corpus files make the reviewable
starting inputs visible to Go's fuzz corpus loader.

The behavioral gates are deliberately stronger than a successful compile:

- `FuzzRewriteMarkdownDestinations` collects an ordered projection of concrete
  Goldmark-owned destination nodes (`kind`, semantic value, exact raw bytes,
  and exact source span) for every valid UTF-8 input. It deterministically
  selects a non-empty eligible
  value class when one exists, then proves the post-rewrite ordered
  `kind`/value projection exactly, while separately proving each pre- and
  post-span is sorted, non-overlapping, and in bounds. It also proves output
  reparses, the source input is unchanged, and bytes outside the *actual
  selected patch spans* are identical. An independent Goldmark AST walk runs
  before the collector and builds a sorted `kind`/semantic-value multiset. In
  addition, every arbitrary fuzz input selects from a nine-case bounded
  known-supported matrix covering LF/CRLF, empty destinations, titles, adjacent
  equal siblings, nested equal image/link destinations, full/collapsed/shortcut
  references, multiline definitions, escaped punctuation, and angle-wrapped
  destinations with spaces. Exact expected `kind`/span/raw/value owners are
  stored while building each fixture; an independent decoder and Goldmark walk
  must agree before the collector and rewrite are invoked. The nested fixture
  additionally applies distinct inner-image and outer-link byte patches and
  reparses both values, so equal destinations cannot hide swapped ownership.
  A broad typed rejection therefore cannot bypass the oracle, and the span
  collector cannot validate itself by omission. This oracle does not claim that every
  syntactically link-looking substring is a Goldmark destination. No generated
  input is skipped by a test-only size or structure gate.
- `FuzzYAMLResolver` wraps the same generated frontmatter for each run and
  exercises selector-driven fragment (`id` or `anchor`) rewrite, relation
  target rewrite, and root `EnsureRelation` routes. Successful scalar and
  target routes call `VerifyPatched`, reparse, compare their intended ordered
  projections, preserve bytes outside actual patch spans, and leave input
  unchanged. Valid UTF-8 rejections must be typed Unsupported/Ambiguous;
  invalid UTF-8 must be `ErrInvalidEncoding`. Its independent layout oracle
  locates the first UTF-8 error and the early closing frontmatter delimiter,
  then requires YAML ownership before that boundary and Markdown ownership
  after it; the explicit `---\n<invalid byte>` seed fixes the latter boundary.
  Every generated input also runs
  one deterministic public `Plan` operation from the fragment, relation-target,
  and ensure-relation routes. A rejected plan must expose no staged source,
  writes, renames, or operation plan and must leave source bytes unchanged. No
  generated input is skipped by a test-only size or structure gate. Each
  arbitrary input also selects one of four bounded known-supported fixtures
  spanning LF/CRLF, `id`/`anchor`, plain/quoted scalars, opaque flow extensions,
  and unrelated merge provenance; fragment, target, and ensure routes must all
  succeed against raw expected snippets stored by the fixture rather than
  production traversal helpers. A separate known-rejection matrix fixes the
  expected error kind, stable code, exact source range, and empty public result
  for duplicate targets/fragments and merge-derived targets/fragments. Every
  arbitrary YAML input is also sent through all three public planner routes
  (rename, move, ensure), rather than selecting one route from the input size.
  Accepted arbitrary inputs must satisfy route-specific public semantics:
  exact operation plans, rename metadata, staged moved content, canonical
  fragment replacement counts, relation-target replacement counts, or one
  idempotent root relation as appropriate. A separate public positive matrix
  fixes the complete staged files and preview writes/deletes/renames for all
  three operations, including digests and unchanged source input; this prevents
  internal resolver helpers from standing in for `Planner.Plan` evidence.
  Every internal typed rejection must be a YAML `PresentationError` with a non-empty in-bounds
  location, including invalid encoding. Fuzzing does not claim that every
  arbitrary YAML document supports every route.
- Overlay and source coverage must establish defensive public reads, shallow
  sharing only of private immutable payloads, manifest-delta reuse, and a
  `Paths` cache shared only after successful enumeration. Manifest validation,
  revision/dependency enumeration, cached path traversal, and large defensive
  copies have cancellable context boundaries exercised by countdown contexts.
  `TestPlannerDoesNotTrustArbitraryManifestSource` additionally proves that
  omitted, extra, and forged digests cannot authorize revision/CAS or overlay
  fast paths; only package-proven overlays and consistent snapshots do.
  `TestLinearMutationHelpersHonorCancellation` and
  `TestSourceReadsCancelDuringChunkedCopy` cover byte patching, semantic YAML
  projection, final preview hashing/copying, `SourceFromFS`, and
  `FileSystemSource` cancellation without partial results.
  The linear-helper matrix also exercises cancellable UTF-8 validation, YAML
  resolver line indexing, stream-marker scans, quoted-node traversal, scalar
  token ownership, and Markdown node-window/destination token scans.

`bundle.SourceFromFS` contract tests cover deterministic regular slash paths and
the non-owning stable-snapshot requirement. `internal/documentlayout.Split`
is the shared exact outer-frontmatter boundary for the loader and both mutation
parsers; its LF/CRLF/non-canonical/unterminated matrix prevents loader-owned
YAML bytes from being reclassified as Markdown. Goldmark `v1.8.2` and `yaml.v3`
`v3.0.1` are pinned in `go.mod`; goccy and tree-sitter are absent.

## Benchmark note

The overlay matrix covers 0, 1, 100, and 10,000 visible files where the
operation is defined on an empty overlay (clone, Paths, and manifest delta),
plus 1, 100, and 10,000-file base/staged rename cases. Rename reports ordinary
time/allocation measurements and does not claim zero payload copies while a
defensive public read is timed. The planner transaction and its
repeated semantic preview/validation matrix cover 100, 1,000, and 10,000
concepts; use `-benchtime=1x` for the 10,000 case. Every benchmark reports
allocations, keeps fixture construction outside its timed region, and consumes
the produced result with semantic assertions.

The durable
[`overlay baseline vs result`](parser-backed-lossless-mutations-profiles/overlay-baseline-vs-result-2026-07-20.md)
uses one self-contained 10,000-file fixture on commit `43f7214` and the result
tree with the same Go version, hardware, `-benchmem`, `-benchtime=3x`, and
three samples. The separate
[`planner profile`](parser-backed-lossless-mutations-profiles/planner-10000-2026-07-20.md) records the
complete 10,000-concept result transaction plus CPU and allocation profiles.

The 10,000-concept planner semantic case contains 10,001 visible files including
`index.md`; `files/op` reports that exact fixture size. It is non-race
performance/profile evidence and still runs in normal tests and the
one-iteration benchmark. A
compile-time `race` build-tag guard skips only that subtest under `-race`; race
testing retains the repeated 100/1,000 planner matrix plus all parser and
overlay correctness tests. This avoids turning the race detector into an
executor-timeout test without using runtime environment guesses.

Performance gates are behavioral rather than brittle cross-machine `ns/op`
thresholds:

- Clone and staged rename share the private immutable payload backing array,
  so they make zero payload-byte copies and perform zero unchanged content
  hash constructions.
- A clone's allocation count is equal for 64-byte and 1-MiB staged payloads;
  allocation depends on overlay metadata, not payload size.
- A clone tree performs one base `Paths` enumeration; public `ReadFile` still
  returns a defensive copy.

`TestOverlayPerformanceGates_NoPayloadCopiesOrUnchangedHashes` proves
backing-array identity through clone plus staged rename and asserts that the
injected hash algorithm receives zero new calls after `Put`.
`TestParserBackedAllocationBaseline` derives its allocation baseline with
`testing.AllocsPerRun(100)`: 64 B and 1 MiB payloads must be exactly equal
(zero payload-size slack) and must remain at or below eight allocations. The
recorded clone baseline is five; the three-allocation metadata slack avoids a
brittle runtime/map-layout threshold while still rejecting any payload copy.
Manifest delta composition validates each qualified digest against the
captured `hash.Hash.Size()`; short/long/non-canonical digests and `Size`/`Sum`
disagreement fail closed, while `Valid` and overlay composition do not
construct hashes for unchanged content.

`TestParserBackedMarkdownCorpus` is the deterministic Markdown table (exact raw
spans, sorted/non-overlapping union, reparse, and unchanged bytes outside
patches), including multiline reference-definition destinations covered by
`TestRewriteMarkdownDestinations_MultilineReferenceDefinition` and
`TestMoveConcept_RewritesMultilineReferenceDefinition`. MoveConcept delegates
internal-link classification and resolution to the same `bundle.Link` contract
as graph construction, including absolute bundle paths, query/fragment suffixes,
colon-bearing concept IDs, and lexical parent traversal.
`TestRewriteMarkdownDestinations_NestedImageAndLinkOwnDistinctSpans` proves the
equal destinations in `[![image](a.md)](a.md)` are owned structurally: bracket
matching starts at each Goldmark node, skips parser-owned descendant destination
spans, and must identify exactly one candidate. There is no nested first-match
exception; if delimiter ownership does not reduce multiple candidates to one,
the collector returns `node_source_anchor_ambiguous`.
`TestPlanMoveConcept_PreservesOutgoingTargetsOfMovedDocument` additionally
proves that every statically resolvable relative link, image, and reference
definition in the moved document is resolved from the old path and emitted
relative to the new path. Non-self targets and query/fragment suffixes therefore
retain semantics across directory changes. Invalid UTF-8 in a Markdown body is
reported by `TestPlanMoveConcept_ReportsInvalidUTF8MarkdownBodyLocation` as a
Markdown `invalid_encoding` error at the exact full-file byte, with no stage.
`TestParserBackedYAMLPresentationCorpus` is the deterministic YAML
parser/resolver/planner table (style preservation, quoted structural keys,
block-context punctuation and multiline plain-scalar boundaries, explicit
non-specific tag provenance,
AST-sibling insertion ownership with opaque unrelated extensions,
insertion/idempotence, stream markers with separation whitespace/comments,
duplicate-target and canonical-endpoint ambiguity, typed Unsupported vs
Ambiguous rejections with stable codes, no stage,
and input-byte preservation). Verification/invariant failures such as
`semantic_edit_mismatch` and `outside_span_changed` are Unsupported, not
Ambiguous; Ambiguous remains reserved for multiple semantic candidates
(`duplicate_relations` / `_relation_type` / `_target` / `_canonical_fragment`)
plus alias provenance, duplicate normalized Markdown reference definitions,
and multiple raw Markdown destination ranges for one AST node. They are
direct AAA tests, not fuzz-only evidence.

`TestEnsureRelationRejectsUnsupportedCanonicalSelectorProvenance` applies the
same touched-node proof to `id`/`anchor` source and target selectors: direct
anchors and explicit tags fail closed before staging. Likewise,
`TestPlannerRejectsCanonicalIdentityThroughDirectAlias` covers mapping and
sequence aliases whose referents provide canonical identity across rename and
both EnsureRelation endpoint routes; these are Ambiguous `alias_provenance`,
not generic unsupported touched-anchor failures.

`TestPlanPresentationErrorsCarryFullFileContext` proves
`PresentationError.Location` stays inside the original file and shifts
yaml-relative resolver spans (including raw `explicit_tag` provenance) while
leaving already-absolute spans such as `unterminated_frontmatter` unshifted.
The Markdown and YAML fuzz rejection oracles additionally require every typed
presentation error over non-empty input to carry an in-bounds, non-empty byte
range; parser diagnostics, semantic verification failures, and planner
wrapping therefore cannot regress to an empty marker location.
`TestPlannerMapsGeneratedYAMLSyntaxFailureToOriginalInsertionRange` and its
committed fuzz regression prove that errors produced while reparsing a longer
post-insertion buffer are remapped to the original patch range rather than
leaking generated-buffer offsets beyond the source file.
`TestPlannerYAMLAmbiguityLocationsCoverConcreteCandidates` additionally proves
exact full-file unions for duplicate relations, relation types, targets, and
canonical fragments. `TestPlannerRejectsMergeDerivedTouchedKeysWithoutStage`
and `TestPlannerRejectsMergeDerivedUpdateLookupsWithoutStage` cover public
ensure/move/rename routes: merge-derived touched keys return
`alias_provenance` at the concrete `<<`/alias range with no staged result.
`TestUpdateRoutesRejectDuplicateSemanticCandidatesWithoutStage` proves that
MoveConcept and RenameFragment reject duplicate instances of the same relation
target, duplicate source canonical fragments, and ambiguous destination
canonical fragments before staging; distinct references such as `a` and
`a#fragment` remain independent semantic targets. Fragment lookup traverses the
complete parsed tree and cannot use first-match ownership. The YAML fuzz oracle
also rejects any successful update of duplicate instances of one exact target.
`TestUnrelatedMergeProvenanceDoesNotBlockMutationRoutes` is the complementary
boundary: a merge or alias is opaque when it cannot supply the relation target
or canonical fragment touched by the operation. Move, rename, and selector
resolution may then proceed while preserving that unrelated subtree byte for
byte; only intersecting merge provenance fails closed.
`TestParseDocument_PreservesCRLFBody`,
`TestParseDocumentTrimsExactlyOneBodyTerminator`, and
`TestParseDocument_PreservesBlockScalarTrailingNewlineBeforeClosingDelimiter`
prove `bundle.ParseDocument` keeps exact frontmatter bytes, preserves CRLF body
content, and removes exactly one LF/CRLF serialization terminator without
damaging a preceding CR byte or consuming an additional body newline.

Run the benchmark command with `-benchmem` and record Go version, CPU, and
complete output in review. The result is hardware-specific evidence, not a
hard nanosecond target. It is used to detect material regressions and to
justify any future separate proposal for a HAMT or parent chain; neither
belongs to the parser-backed mutation phase.

The durable representative benchmark and full 40-node CPU/alloc-space textual
profiles are tracked at
[`parser-backed-lossless-mutations-profiles/planner-10000-2026-07-20.md`](parser-backed-lossless-mutations-profiles/planner-10000-2026-07-20.md).
The binary `/private/tmp` profiles are reproducible scratch inputs to that
report, not the delivery artifact.

### Recorded overlay sample

Command (2026-07-20, local workspace):

```sh
go test -run '^$' -bench 'Benchmark(Overlay(Clone|Rename|Paths|ManifestDeltaRevision)|ParserBackedOverlayComparison|PlannerRepresentativeTransaction)$' -benchtime=1x -benchmem ./mutation
```

`go version go1.26.5 darwin/arm64`; host `darwin/arm64`, Apple M1 Max, macOS
27.0. Binary profiles remain reproducible scratch files in `/private/tmp`;
the durable tracked textual profile is linked above. Selected results:

```text
BenchmarkOverlayClone/0-10                                  1        667 ns/op       240 B/op         4 allocs/op
BenchmarkOverlayClone/10000-10                              1     358000 ns/op   1049456 B/op        37 allocs/op
BenchmarkOverlayRename/base-10000-10                        1    1020708 ns/op    328752 B/op        12 allocs/op
BenchmarkOverlayRename/staged-10000-10                      1    1054833 ns/op    328736 B/op        11 allocs/op
BenchmarkOverlayPaths/base-0-10                             1       1500 ns/op       112 B/op         1 allocs/op
BenchmarkOverlayPaths/delta-0-10                            1       2375 ns/op       128 B/op         2 allocs/op
BenchmarkOverlayPaths/base-10000-10                         1    2232084 ns/op    928496 B/op        37 allocs/op
BenchmarkOverlayPaths/delta-10000-10                        1    2244458 ns/op    928496 B/op        37 allocs/op
BenchmarkOverlayManifestDeltaRevision/0-10                  1      20834 ns/op      1280 B/op        20 allocs/op
BenchmarkOverlayManifestDeltaRevision/10000-10              1    7695792 ns/op   3081624 B/op     80082 allocs/op
```

The complete current CPU and allocation-space profile is the tracked planner
artifact linked above. Payload-copy and unchanged-hash properties are enforced
by behavioral tests instead of inferred from a noisy short sample.

This is a current-tree sample, not a target or cross-machine comparison. The
controlled baseline/result comparison is the separately linked durable report.

For an updated textual profile summary, run:

```sh
go test -run '^$' -bench 'BenchmarkOverlay(Clone|Rename|Paths|ManifestDeltaRevision)$' -benchtime=1x -benchmem -cpuprofile /private/tmp/okf-parser-backed-cpu.pprof -memprofile /private/tmp/okf-parser-backed-heap.pprof ./mutation
go tool pprof -top /private/tmp/okf-parser-backed-cpu.pprof
go tool pprof -alloc_space -top /private/tmp/okf-parser-backed-heap.pprof
```

The commands keep binary profiles under `/private/tmp`; their durable textual
delivery projection is the tracked 10,000-concept report linked above.
