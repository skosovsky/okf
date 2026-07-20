# Issue 002: overlay baseline vs result

Recorded 2026-07-20 on `darwin/arm64`, Apple M1 Max, Go 1.26.5. Both sides
use `BenchmarkIssue002OverlayComparison`, the same 10,000-file fixtures,
256-byte staged payload, Go toolchain, hardware, `-benchtime=3x`, and
`-count=3`. The table reports the median `ns/op`; allocation columns are the
stable values reported by all three samples.

The baseline is commit `43f7214` (`mutable`), before the Issue 002 working-tree
implementation. The comparison benchmark is self-contained and was copied
unchanged into an archive of that commit:

```sh
mkdir /private/tmp/okf-issue002-baseline
git archive 43f7214 | tar -x -C /private/tmp/okf-issue002-baseline
cp mutation/overlay_comparison_benchmark_test.go \
  /private/tmp/okf-issue002-baseline/mutation/overlay_comparison_benchmark_test.go

(cd /private/tmp/okf-issue002-baseline && \
  GOCACHE=/private/tmp/okf-go-cache-baseline \
  go test ./mutation -run '^$' \
    -bench '^BenchmarkIssue002OverlayComparison$' \
    -benchtime=3x -benchmem -count=3)

GOCACHE=/private/tmp/okf-go-cache-result \
go test ./mutation -run '^$' \
  -bench '^BenchmarkIssue002OverlayComparison$' \
  -benchtime=3x -benchmem -count=3
```

| Fixture | Baseline ns/op | Result ns/op | Baseline B/op | Result B/op | Baseline allocs/op | Result allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| clone changed 10,000 | 1,379,528 | 489,667 | 3,347,232 | 1,049,456 | 10,036 | 37 |
| repeated Paths, base 10,000 | 3,001,708 | 1,467,111 | 764,581 | 600,704 | 35 | 34 |
| rename staged 10,000 | 1,253,625 | 467,014 | 3,349,077 | 1,049,701 | 10,044 | 38 |

The clone and staged-rename allocation reduction is the expected consequence
of sharing private immutable payload bytes while preserving defensive public
reads. The repeated-Paths result includes the public result copy on both sides;
the result tree reuses its successfully enumerated and normalized base-path
cache.
