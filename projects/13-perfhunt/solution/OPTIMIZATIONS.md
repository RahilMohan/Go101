# perfhunt — Optimizations

`slow.go` in this package **is** the preserved intentionally-slow baseline
commit (this repo commits once per project on the feature branch, so the
baseline lives side-by-side with the optimized code rather than as a
separate historical commit). Every function below has a `*Slow` twin in
`slow.go`; `bench_test.go` measures them against each other and
`TestSlowMatchesFast*` pins behavioral parity.

Reference numbers: Apple M1 Pro, Go 1.22, dataset = 5000 records /
15000 tags (`-benchtime=10x`, so treat as order-of-magnitude evidence —
run full benchmarks yourself for stable numbers).

## Anti-pattern → fix → expected profile evidence

| # | Slow baseline | Fix | Profile evidence to look for |
|---|---------------|-----|------------------------------|
| 1 | **String concatenation in loops** — per-record fingerprint built with `fingerprint += tag + "\|"` (quadratic reallocation) | Nothing — the fingerprint was dead weight; delete it. Where accumulation is needed, use `strings.Builder` / `[]byte` append | CPU pprof: `runtime.concatstrings`, `runtime.growslice` hot; allocs/op collapses (AggregateSlow 20026 → Aggregate 19) |
| 2 | **Repeated map lookups** — `byRegion[r.Region]` evaluated three times per record (probe, read-back, write-back) | Single `byRegion[r.Region] += v` (one probe) | CPU pprof: fewer `mapaccess` frames; heap profile shows smaller transient map churn |
| 3 | **Interface boxing in hot loop** — every `Value` appended into `[]any` then type-asserted back out; each float64 escapes to the heap | Sum floats directly as they stream past; keep values concrete | Heap/alloc_space pprof: `runtime.convT64` disappears; B/op drops ~3x (603761 → 218688, most of what remains is the `seen` map + result) |
| 4 | **Regex compiled per call** — `NormalizeSlow` calls `regexp.MustCompile` on every invocation (~µs + several allocs each) | No regex at all: one manual ASCII scan over a pre-sized `[]byte` | CPU pprof: `regexp.(*Regexp).Compile` / `syntax.Parse` frames gone; Normalize: 3467 ns → 158 ns, 14 allocs → 1 |
| 5 | **Unbuffered scans** — separate full passes over records for sum, min, max (plus a boxed re-sum) | One pass accumulates everything | CPU flame graph: three sibling loops over the same slice collapse into one; time/op drops ~38x |
| 6 | **O(n²) dedup** — linear scan over a growing `uniqIDs` slice per record | One `seen map[string]struct{}` probe per record | CPU pprof at scale: quadratic curve flattens to linear; `TopKSlow`'s distinct-scan dominates its flame graph, absent in fast |
| 7 | **sync.Mutex where none suffices** — lock/unlock around plain local accumulation and per-item map increments (single-goroutine work!) | Plain locals; maps need no external lock when not shared | Block/mutex profile: `sync.Mutex.Lock` contention rows vanish; even uncontended, lock/unlock pairs cost ~20ns each × n |
| 8 | **Reflection-heavy JSON** — response built as `map[string]any` (every number boxed) then `encoding/json.Marshal` reflects over the map | Hand-assembled JSON via `strconv.AppendInt/AppendFloat/AppendQuote` into one buffer; typed structs where marshaling is warranted | CPU pprof: `encoding/json` reflect machinery (`json.mapEncoder`, `reflect.Value`) replaced by strconv; HandlerStats allocs 70 → 36 |

## Benchmark snapshot (10x iterations)

```
BenchmarkAggregate/Slow   8688692 ns/op   603761 B/op   20026 allocs/op
BenchmarkAggregate/Fast    225342 ns/op   218688 B/op      19 allocs/op
BenchmarkTopK/Slow         543262 ns/op     1248 B/op      11 allocs/op
BenchmarkTopK/Fast         217146 ns/op     3832 B/op       7 allocs/op
BenchmarkNormalize/Slow      3467 ns/op     4804 B/op      14 allocs/op
BenchmarkNormalize/Fast       158 ns/op        6 B/op       1 allocs/op
BenchmarkHandlerStats/Slow 289538 ns/op   222086 B/op      70 allocs/op
BenchmarkHandlerStats/Fast 211454 ns/op   221333 B/op      36 allocs/op
```

Notes:

- TopK slow *allocates less* than fast (its mutex-guarded map is never
  presized) yet runs 2.5x slower — a good reminder that ns/op and
  allocs/op tell different stories.
- HandlerStats deltas look modest because the endpoint is dominated by
  Aggregate itself; the JSON fix shows up in the alloc count halving.
  Fixing the aggregate first is the lesson.

## How to reproduce the profiles

```sh
go test -bench=BenchmarkAggregate -benchmem -cpuprofile cpu.out -memprofile mem.out ./solution/
go tool pprof -top solution/cpu.out
go test -bench=BenchmarkHandlerStats -mutexprofile mutex.out ./solution/
go tool pprof -top solution/mutex.out
```
