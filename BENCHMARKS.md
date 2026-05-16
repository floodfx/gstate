# Benchmarks

Baseline numbers for the gstate engine hot path. Run with:

```bash
just bench
# or: go test -bench=. -benchmem -count=3 -benchtime=1s -run='^$' ./...
```

## Environment

| | |
|---|---|
| Go | 1.25.5 linux/amd64 |
| CPU | AMD EPYC 9354P 32-Core Processor |
| GOMAXPROCS | 2 |

## Results

Each benchmark sends a batch of events through the engine and waits for
all transitions to complete. Numbers are per-iteration (one batch).

### Engine Hot Path (100 transitions per iteration)

| Benchmark | ns/op | B/op | allocs/op | Notes |
|-----------|------:|-----:|----------:|-------|
| SendTransition_NoObserver | 109,000 | 13,504 | 720 | Baseline: pure engine overhead |
| SendTransition_NopObserver | 114,000 | 13,560 | 722 | +5% — dispatch overhead, no recording |
| SendTransition_RecordingObserver | 146,000 | 115,832 | 1,134 | +34% — mutex append + context copies |

### Context Snapshot Cost (100 transitions per iteration)

| Benchmark | ns/op | B/op | allocs/op | Notes |
|-----------|------:|-----:|----------:|-------|
| ContextSnapshot_ValueCopy | 108,000 | 13,504 | 720 | Plain struct copy |
| ContextSnapshot_Cloner | 112,000 | 59,256 | 1,019 | Cloner interface: +4% time, +4.4× allocs |

### Hierarchical Transitions (50 transitions per iteration)

| Benchmark | ns/op | B/op | allocs/op | Notes |
|-----------|------:|-----:|----------:|-------|
| depth=1 | 56,000 | 9,088 | 370 | Flat — no LCA walk |
| depth=4 | 136,000 | 23,848 | 774 | 2.4× depth=1 |
| depth=8 | 319,000 | 43,760 | 1,231 | 5.7× depth=1 — linear in depth |

### Parallel Region Entry (50 transitions per iteration)

| Benchmark | ns/op | B/op | allocs/op | Notes |
|-----------|------:|-----:|----------:|-------|
| regions=2 | 131,000 | 21,376 | 646 | |
| regions=4 | 216,000 | 34,632 | 874 | |
| regions=8 | 462,000 | 62,976 | 1,304 | ~linear in region count |

### Always-Transition Chain (20 rounds per iteration)

| Benchmark | ns/op | B/op | allocs/op | Notes |
|-----------|------:|-----:|----------:|-------|
| steps=5 | 108,000 | 15,424 | 900 | 7 transitions/round (5 always + GO + RESET) |
| steps=25 | 340,000 | 44,224 | 3,300 | 27 transitions/round |
| steps=50 | 647,000 | 80,224 | 6,300 | 52 transitions/round |

### Lifecycle Operations

| Benchmark | ns/op | B/op | allocs/op | Notes |
|-----------|------:|-----:|----------:|-------|
| InvokeStartCancel (20 rounds) | 63,500 | 12,576 | 381 | Goroutine spawn + cancel per round |
| Snapshot/simple | 121 | 64 | 2 | 1 active state, 0 history |
| Snapshot/parallel_4regions | 218 | 192 | 2 | 9 active states |
| Hydrate/simple | 2,355 | 4,360 | 12 | Includes goroutine start |
| Hydrate/parallel_4regions | 3,349 | 4,816 | 15 | |

### Allocation Check

| Benchmark | ns/op | B/op | allocs/op | Notes |
|-----------|------:|-----:|----------:|-------|
| SendTransition_ZeroAlloc | 112,000 | 13,504 | 720 | Same as NoObserver — per-batch (100 events), not per-event |

> **Note:** The 720 allocs/iteration include actor Start/Stop overhead
> (goroutine, channels, maps). Per-event allocation cost is
> ~7 allocs/event, dominated by observer payload construction.
> The `TestSendTransition_ZeroAllocCheck` test logs the per-run
> breakdown.
