---
layout: default
title: "Go toolkit benchmarks (issue 015)"
lang: en
permalink: /readings/benchmarks/toolkit/README/
documentation_id: benchmarks-toolkit-readme
source_revision: 61e75e9aa9a8719dfb480bf1f5226553b3a21d70
historical: false
documentation_status: current
---
{% include nav.html %}

**Reading edition of the benchmark methodology.** Source: [benchmarks/toolkit/README.md](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmarks/toolkit/README.md), repository revision `61e75e9aa9a8719dfb480bf1f5226553b3a21d70` (2026-10-04). The methods, dates, measurements and status recorded in that revision are preserved. Reported past results describe those runs and provide no guarantee about current product performance or model answers.

<a id="go-toolkit-benchmarks-issue-015"></a>

# Go toolkit benchmarks (issue 015)
{: #section-1}

The pinned baseline is `ed7ddc28cd127682023bd150ee377906b817e099`;
the corrected implementation is
`0f15ab4091203f11caa7fb0b9e02da47d719a051`. Both were measured from
clean `/private/tmp` archive snapshots with identical benchmark source and
corpus bytes. The final paired series ran after a concurrent full test suite
was stopped. An earlier paired attempt under that load was discarded and is
not part of the published comparison.
The baseline archive did not originally contain this new harness: the exact
`benchmarks/toolkit` tree from corrected commit `0f15ab4` was copied into it.
`diff -rq` of those two harness trees returned no differences. Source SHA-256:
`corpus.go` `91535e840efb20952b22e01fd512515f7c0a9b314f3e2a5c9bd3c78a114598f7`,
`toolkit_test.go` `550eb581810580eb9ee26df4e74cffd8da70723c506b72dbd0d86fc9a202b4f9`.
The input digests in the corpus table below match both snapshots.

<a id="final-paired-result"></a>

## Final paired result
{: #section-2}

Five repetitions per commit were interleaved `AB BA AB BA AB` at
`-benchtime=100ms` using prebuilt Go test and CLI binaries. Baseline and
corrected gates passed before timing; the corrected gate includes the wrapped
log, code-owned footnote, legacy date, and equal-instant offset datetime
cases. Both pinned binaries also passed strict+links+orphans validation of the
manual 10,000-concept corpus with zero diagnostics.

The accepted raw files are
[`baseline` — original](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmarks/toolkit/results/paired-ed7ddc28-0f15ab4-baseline.raw.txt) (SHA-256
`cc278b342a0efa3f008e4631aa5ca622a5e6f873b20603a6d3962533ed862a79`)
and [`corrected` — original](https://github.com/skosovsky/okf/blob/61e75e9aa9a8719dfb480bf1f5226553b3a21d70/benchmarks/toolkit/results/paired-ed7ddc28-0f15ab4-corrected.raw.txt)
(SHA-256
`3b9fa38bf6bcf052e52800496ec112969391c3315c830424103c43aa0849377a`).
They contain 28 × 5 and 34 × 5 workload observations respectively.
The [full comparison]({{ '/readings/benchmarks/toolkit/results/paired-ed7ddc28-0f15ab4-summary/' | relative_url }})
reports median ± MAD for ns/op, B/op, and allocs/op and labels corrected-only
workloads `N/A` on the baseline. The analyzer source is pinned by SHA-256
`ae78d8e8156eae85e9c27122609b46b515fa3262f614cf3b53ab8af7f5c5718d`.

Representative medians from the clean paired series:

| Workload | Baseline ns/op | Corrected ns/op | Time delta | Baseline → corrected B/op | Baseline → corrected allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: |
| Plain typed log | 222 | 155 | −30.0% | 208 → 80 | 4 → 2 |
| 100 concepts, all validation checks | 10,920,854 | 11,328,883 | +3.7% | 10,936,046 → 10,939,834 | 108,394 → 108,496 |
| 1,000 concepts, all validation checks | 122,561,459 | 118,398,375 | −3.4% | 107,666,016 → 107,812,384 | 1,078,763 → 1,079,797 |
| 1,000 concepts, graph JSON-LD | 160,772,166 | 150,407,708 | −6.4% | 116,700,032 → 117,430,704 | 1,384,703 → 1,384,695 |

The corrected-only wrapped log parser measured 5,030 ns/op, 5,944 B/op,
66 allocs/op. Its baseline counterpart rejected the valid input, so no speed
ratio is reported. The 10-concept graph case was 6.3% slower while the
1,000-concept graph case was 6.4% faster; validation deltas were also mixed.
This five-pair series does not establish a broad scaling regression.
CPU/heap profiles of the superseded pre-fast-path
`ParseLog` implementation remain below as historical evidence, not as a
profile of the pinned corrected commit.

Machine: Apple M1 Max, macOS 27.0 / Darwin 27.0.0, `go1.26.5 darwin/arm64`,
`CGO_ENABLED=1`, empty `GOFLAGS`. All runs used the same machine, Go cache,
flags, corpus manifests, and benchmark source. Prebuilt binary SHA-256 values:
baseline test `6aec602c02acb507a538ba185c9a7550fc0b126005604881feffc4a31e2b7b12`,
baseline CLI `313ea5a941a037a3b29bec46ee1b60809e24f1d0e1eb31eec086bb8555f40def`,
corrected test `9b13f43b270d87e67b7cd368f5bad7ab638f1d39a5016a9b22b6d7a1d2a9f2f1`,
corrected CLI `c655b4c0b192d0525b287688063441d27632c3b5bc80c8fa1d701ce84d358fe8`.

The Go generator builds immutable-in-use snapshots from consecutive numeric
IDs. Each concept has one source and one Markdown edge. Directory groups have
at most 100 concepts. The corpus contains a root index, one index per group,
and a log. All bytes are synthetic and local; no foreign code or fixture text
is copied. Manifests in `results/` contain per-file hashes, a digest over
length-prefixed sorted paths and contents, and dimensions.

| Corpus | Concepts | Files | Bytes | Indexes | Edges | Sources | Wrapped lines | SHA-256 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| `n10` | 10 | 13 | 2,033 | 2 | 10 | 10 | 0 | `2ca555604144236f282613c7ec28596c0f2fe847bda62418e0bcf89f6ffeb9c0` |
| `n100` | 100 | 103 | 19,313 | 2 | 100 | 100 | 0 | `4269ee5f46bc067a8ec25f4abc895337a55ca8b1a7480b496b0371ebace662d3` |
| `n1000` | 1,000 | 1,012 | 196,091 | 11 | 1,000 | 1,000 | 0 | `ef62d3064ce91ad1ce720d29144377c16a50ffe776b661292f25eb52522bdba2` |
| `n10000` (manual) | 10,000 | 10,102 | 1,999,871 | 101 | 10,000 | 10,000 | 0 | `0070d845c6eaf710629ce872c3880294fb5fc26a9a52fcda69448087d34cd2b5` |
| `interop` | 10 | 13 | 2,040 | 2 | 10 | 10 | 1 | `b61ac1a0d7d5227be226a3cdfd8b1e993fb89215b51bd46a9f8d5fc57e81e083` |
| `temporal-date` | 10 | 13 | 2,159 | 2 | 10 | 10 | 0 | `4206ecf77a84d498acca8500c5fe95bdde83257b3391922a5c7fb192222f77a5` |
| `temporal-instant` | 10 | 13 | 2,214 | 2 | 10 | 10 | 0 | `e0623170ab61a25ef67810ab1b9e6a7090bd4d91dc6b57053724f6f1afcbe15d` |

The `interop` corpus has one wrapped `log.md` item and a literal `[^fake]`
inside inline code. The datetime corpus has offset timestamps and a
`usage_window` whose endpoints use different offsets for the same instant.
Baseline strict validation of identical bytes returns one log error and two
phantom footnote warnings for `interop`; datetime input produces five strict
warnings under its date profile. The old implementation therefore has no
correctness-equivalent baseline for those workloads. The corrected-only
benchmarks check the valid outcome before timing and are labelled separately.
Baseline date input passes strict validation with zero diagnostics. Baseline
probe JSON is retained under `results/`.

The main benchmark covers document parsing, snapshot bundle loading, base and
strict validation, links, orphans, all checks combined, typed log reading,
source-footnote ownership, toolkit JSON-LD projection, and a separate CLI
process workload. Inputs and the CLI executable are prepared before timers.
The timed `load` calls `bundle.Load` directly, without test assertions inside
the hot path; all operations have a preflight. CLI preflight decodes JSON and
requires a conformant strict/links/orphans result, 13/103 scanned files as
appropriate, and zero errors, warnings, policy failures, and diagnostics.
CLI B/op and allocs/op describe the parent Go test process, excluding child
process memory.

The baseline run was on 2026-09-26 UTC: Apple M1 Max, macOS 27.0 / Darwin
27.0.0, `go1.26.5 darwin/arm64`, `CGO_ENABLED=1`, empty `GOFLAGS`. It used
`-benchtime=100ms -count=5`. Raw records are
`results/baseline-ed7ddc28-v3.raw.txt`, SHA-256
`31413f125813f0707dba65bec18e13a37d0f49b6e8efd3658fc90153bfcfddcd`.
The older `-benchtime=3x` run is retained as exploratory evidence only. Even
the 100ms run has noise: its median absolute deviation (MAD) is reported;
no wall-clock gate or performance threshold is set. The final paired run
above supersedes this standalone baseline as comparison evidence. For a
material delta in later runs, repeat both commits in alternating order on
the same machine, then save CPU and heap profiles if it persists. The local Go analyzer
reports median ± MAD for ns/op, B/op, and allocs/op, plus deltas only when
both sides have equivalent workloads. Its source is pinned by SHA-256
`ae78d8e8156eae85e9c27122609b46b515fa3262f614cf3b53ab8af7f5c5718d`.
The generator and benchmark source hashes for the v3 run are
`91535e840efb20952b22e01fd512515f7c0a9b314f3e2a5c9bd3c78a114598f7`
and `550eb581810580eb9ee26df4e74cffd8da70723c506b72dbd0d86fc9a202b4f9`.

Reproduce the baseline in a fresh directory from the repository root:

```sh
snapshot=$(mktemp -d /tmp/okf-toolkit-baseline.XXXXXX)
git archive ed7ddc28cd127682023bd150ee377906b817e099 | tar -x -C "$snapshot"
mkdir -p "$snapshot/benchmarks"
cp -R benchmarks/toolkit "$snapshot/benchmarks/"
cd "$snapshot"
GOCACHE=/tmp/okf-bench-gocache go test ./benchmarks/toolkit/... -run 'TestCorpus|TestSpecial|TestAnalyzer' -count=1
GOCACHE=/tmp/okf-bench-gocache go build -o "$snapshot/okf-bench-cli" ./cmd/okf
GOCACHE=/tmp/okf-bench-gocache OKF_BENCH_CLI="$snapshot/okf-bench-cli" go test ./benchmarks/toolkit -run '^$' -bench '^BenchmarkToolkit$' -benchtime=100ms -count=5 -timeout=30m > baseline.raw.txt
GOCACHE=/tmp/okf-bench-gocache go run ./benchmarks/toolkit/cmd/analyze -baseline baseline.raw.txt
```

`go run ./benchmarks/toolkit/cmd/corpusgen -out /tmp/okf-toolkit-bench-corpus`
materializes the exact input files and manifests outside timed sections. Add
`-large` and set `OKF_BENCH_LARGE=1` for manual 10,000-concept runs. A normal
PR can run the correctness tests without a timing threshold. For corrected
runs, build that commit's CLI before timing and set
`OKF_BENCH_EXPECT_CORRECTED=1`; run `TestCorrectedInteropContract` first.
The 10,000-concept corpus passed `OKF_BENCH_LARGE=1 go test
./benchmarks/toolkit -run '^TestCorpusContracts$'` with strict, links and
orphans enabled and zero diagnostics on the exploratory corrected tree.

To repeat the final comparison, compile `./benchmarks/toolkit` with `go test -c`
and `./cmd/okf` with `go build` once in each pinned checkout. Both checkouts
must contain the same benchmark source bytes and corpus manifests. Then run
the Go interleaver:

```sh
go run ./benchmarks/toolkit/cmd/paired \
  -baseline-test /path/to/baseline/toolkit.test \
  -baseline-cli /path/to/baseline/okf \
  -corrected-test /path/to/corrected/toolkit.test \
  -corrected-cli /path/to/corrected/okf \
  -out /tmp/okf-toolkit-paired
go run ./benchmarks/toolkit/cmd/analyze \
  -baseline /tmp/okf-toolkit-paired/baseline.raw.txt \
  -corrected /tmp/okf-toolkit-paired/corrected.raw.txt
```

The runner executes `AB BA AB BA AB`, five observations per arm at the same
100ms target, writing each raw result after the subprocess succeeds. Keep
other benchmark jobs off the machine during this run. A paired median ratio
is still descriptive evidence, not a precision claim from only five samples.
The runner was smoke-tested with one executable on both arms at `-benchtime=1x`:
ten subprocesses completed, and the analyzer found five samples for each
shared row plus corrected-only rows with `N/A` baseline. That smoke output is
not part of the performance comparison.

<a id="earlier-standalone-baseline"></a>

## Earlier standalone baseline
{: #section-3}

The standalone `results/baseline-ed7ddc28-v3.raw.txt` run preceded the
paired comparison. It remains as provenance but is superseded for
before/after claims by the pinned paired raw files and full table above.

<a id="exploratory-corrected-run"></a>

## Exploratory corrected run
{: #section-4}

An uncommitted tree after the core 004/005 edits passed
`TestCorrectedInteropContract` and two five-repeat `-benchtime=100ms` runs.
The current exploratory result after the `ParseLog` fast path is
`results/corrected-exploratory-v2.raw.txt` (SHA-256
`57240b9418a8f204fb95e617e935c539c4c882103efe1490d812f6bd1b076aaa3`).
The same corpus hashes above were used. The corrected-only interop and
temporal workloads passed their preflights. This is still an uncommitted
exploratory run, not the pinned corrected result.

The plain typed log case measured 235 ns/op, 208 B/op, 4 allocs/op at the
baseline and 180 ns/op, 80 B/op, 2 allocs/op after the fast path. Wrapped
entries still use the Markdown parser; their corrected-only typed case
measured 5,290 ns/op, 5,944 B/op, 66 allocs/op. The final paired repeat above
supersedes these exploratory timing claims.

Before the fast path, the first exploratory run measured 4,547 ns/op,
4,504 B/op, 54 allocs/op on the plain typed case. Its raw file remains
`results/corrected-exploratory.raw.txt` (SHA-256
`44c29914c82851204e47539420909a083cd065df88af9033a733b09e32ea6636`).
CPU and heap profiles of that *superseded* implementation are saved as
`results/corrected-exploratory-log.cpu.pprof` and
`results/corrected-exploratory-log.heap.pprof`. With
`github.com/google/pprof@v0.0.0-20250317173921-a4b03ec1a45e`, the heap
profile attributes about 96.6% of allocation space to
`markdownowner.TopLevelStructureContext` and its Goldmark calls. The profile
was collected with instrumentation, so its ns/op was not used in the
comparison. The fast path preserves the parser for wrapped items. Other
exploratory deltas varied with machine load; the clean pinned paired run
above is the accepted comparison.
