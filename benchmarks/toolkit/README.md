# Go toolkit benchmarks (issue 015)

The pinned baseline is `ed7ddc28cd127682023bd150ee377906b817e099`.
Corrected measurements await the frozen 004/005 commit. No regression verdict
is claimed until matched corrected runs and correctness gates pass.

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
no wall-clock gate or performance threshold is set. For a material delta,
repeat the two commits in alternating order on the same machine, then save
CPU and heap profiles if the regression persists. The local Go analyzer
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

For the final comparison, compile `./benchmarks/toolkit` with `go test -c`
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

## Baseline measurements

The table uses median ± MAD across five raw observations. `N/A` means no
correctness-equivalent corrected sample has been measured yet.

| Workload | Baseline N | Baseline ns/op | Baseline B/op | Baseline allocs/op | Corrected N | Corrected ns/op | Corrected B/op | Corrected allocs/op | Δ time | Δ bytes | Δ allocs |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `BenchmarkToolkit/footnotes/ownership-10` | 5 | 9331 ± 106 | 7313 ± 6 | 100 ± 0 | N/A | N/A | N/A | N/A | N/A | N/A | N/A |
| `BenchmarkToolkit/log/typed-10` | 5 | 235 ± 3 | 208 ± 0 | 4 ± 0 | N/A | N/A | N/A | N/A | N/A | N/A | N/A |
| `BenchmarkToolkit/n=10/cli/validate-10` | 5 | 9496810 ± 341782 | 15750 ± 70 | 40 ± 0 | N/A | N/A | N/A | N/A | N/A | N/A | N/A |
| `BenchmarkToolkit/n=10/graph/jsonld-10` | 5 | 930775 ± 10320 | 1086064 ± 162 | 14446 ± 0 | N/A | N/A | N/A | N/A | N/A | N/A | N/A |
| `BenchmarkToolkit/n=10/load-10` | 5 | 206962 ± 978 | 237809 ± 0 | 2516 ± 0 | N/A | N/A | N/A | N/A | N/A | N/A | N/A |
| `BenchmarkToolkit/n=10/parse/document-10` | 5 | 17013 ± 4577 | 14184 ± 0 | 143 ± 0 | N/A | N/A | N/A | N/A | N/A | N/A | N/A |
| `BenchmarkToolkit/n=10/validate/all-10` | 5 | 1285064 ± 92749 | 1147613 ± 388 | 11449 ± 0 | N/A | N/A | N/A | N/A | N/A | N/A | N/A |
| `BenchmarkToolkit/n=10/validate/base-10` | 5 | 198554 ± 1757 | 237354 ± 1 | 2340 ± 0 | N/A | N/A | N/A | N/A | N/A | N/A | N/A |
| `BenchmarkToolkit/n=10/validate/links-10` | 5 | 449756 ± 8836 | 476770 ± 4 | 4819 ± 0 | N/A | N/A | N/A | N/A | N/A | N/A | N/A |
| `BenchmarkToolkit/n=10/validate/orphans-10` | 5 | 212604 ± 478 | 263658 ± 0 | 2651 ± 0 | N/A | N/A | N/A | N/A | N/A | N/A | N/A |
| `BenchmarkToolkit/n=10/validate/strict-10` | 5 | 1252630 ± 198752 | 879069 ± 889 | 8658 ± 0 | N/A | N/A | N/A | N/A | N/A | N/A | N/A |
| `BenchmarkToolkit/n=100/cli/validate-10` | 5 | 40036250 ± 8667167 | 15640 ± 0 | 40 ± 0 | N/A | N/A | N/A | N/A | N/A | N/A | N/A |
| `BenchmarkToolkit/n=100/graph/jsonld-10` | 5 | 10854533 ± 120155 | 11037746 ± 5008 | 139061 ± 1 | N/A | N/A | N/A | N/A | N/A | N/A | N/A |
| `BenchmarkToolkit/n=100/load-10` | 5 | 2721254 ± 275223 | 2359597 ± 10 | 24536 ± 0 | N/A | N/A | N/A | N/A | N/A | N/A | N/A |
| `BenchmarkToolkit/n=100/parse/document-10` | 5 | 16342 ± 3381 | 14184 ± 0 | 143 ± 0 | N/A | N/A | N/A | N/A | N/A | N/A | N/A |
| `BenchmarkToolkit/n=100/validate/all-10` | 5 | 10977262 ± 223638 | 10944098 ± 11775 | 108398 ± 3 | N/A | N/A | N/A | N/A | N/A | N/A | N/A |
| `BenchmarkToolkit/n=100/validate/base-10` | 5 | 2388596 ± 332865 | 1972746 ± 13 | 19462 ± 0 | N/A | N/A | N/A | N/A | N/A | N/A | N/A |
| `BenchmarkToolkit/n=100/validate/links-10` | 5 | 4228438 ± 70034 | 4214565 ± 42 | 42569 ± 1 | N/A | N/A | N/A | N/A | N/A | N/A | N/A |
| `BenchmarkToolkit/n=100/validate/orphans-10` | 5 | 2245609 ± 29362 | 2220896 ± 7 | 22129 ± 0 | N/A | N/A | N/A | N/A | N/A | N/A | N/A |
| `BenchmarkToolkit/n=100/validate/strict-10` | 5 | 8563692 ± 397769 | 8432421 ± 13770 | 82624 ± 2 | N/A | N/A | N/A | N/A | N/A | N/A | N/A |
| `BenchmarkToolkit/n=1000/graph/jsonld-10` | 5 | 153827583 ± 2465292 | 116702616 ± 34824 | 1384704 ± 10 | N/A | N/A | N/A | N/A | N/A | N/A | N/A |
| `BenchmarkToolkit/n=1000/load-10` | 5 | 24648925 ± 973725 | 24324568 ± 14 | 244276 ± 0 | N/A | N/A | N/A | N/A | N/A | N/A | N/A |
| `BenchmarkToolkit/n=1000/parse/document-10` | 5 | 14324 ± 947 | 14184 ± 0 | 143 ± 0 | N/A | N/A | N/A | N/A | N/A | N/A | N/A |
| `BenchmarkToolkit/n=1000/validate/all-10` | 5 | 120305959 ± 1174001 | 107811768 ± 146280 | 1078789 ± 22 | N/A | N/A | N/A | N/A | N/A | N/A | N/A |
| `BenchmarkToolkit/n=1000/validate/base-10` | 5 | 25243448 ± 381260 | 19474177 ± 31 | 191138 ± 0 | N/A | N/A | N/A | N/A | N/A | N/A | N/A |
| `BenchmarkToolkit/n=1000/validate/links-10` | 5 | 64797708 ± 10755042 | 41772640 ± 668 | 420937 ± 4 | N/A | N/A | N/A | N/A | N/A | N/A | N/A |
| `BenchmarkToolkit/n=1000/validate/orphans-10` | 5 | 27643240 ± 315052 | 21957608 ± 12 | 217799 ± 0 | N/A | N/A | N/A | N/A | N/A | N/A | N/A |
| `BenchmarkToolkit/n=1000/validate/strict-10` | 5 | 113602542 ± 2837083 | 82918080 ± 34312 | 822299 ± 4 | N/A | N/A | N/A | N/A | N/A | N/A | N/A |

## Exploratory corrected run

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
measured 5,290 ns/op, 5,944 B/op, 66 allocs/op. This outcome needs a final
paired repeat before claiming a performance gain.

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
exploratory deltas vary with machine load; the final paired run will decide
whether fresh profiles are needed.
