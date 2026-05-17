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
| SendTransition_NoObserver | 93,000 | 10,320 | 520 | Baseline: pure engine overhead |
| SendTransition_NopObserver | 99,000 | 10,376 | 522 | +6% — dispatch overhead, no recording |
| SendTransition_RecordingObserver | 125,000 | 112,648 | 934 | +34% — mutex append + context copies |

### Context Snapshot Cost (100 transitions per iteration)

| Benchmark | ns/op | B/op | allocs/op | Notes |
|-----------|------:|-----:|----------:|-------|
| ContextSnapshot_ValueCopy | 103,000 | 10,320 | 520 | Plain struct copy |
| ContextSnapshot_Cloner | 108,000 | 56,072 | 819 | Cloner interface: +5% time, +1.6× allocs |

### Hierarchical Transitions (50 transitions per iteration)

| Benchmark | ns/op | B/op | allocs/op | Notes |
|-----------|------:|-----:|----------:|-------|
| depth=1 | 47,000 | 7,504 | 270 | Flat — no LCA walk |
| depth=4 | 125,000 | 17,464 | 674 | 2.7× depth=1 |
| depth=8 | 218,000 | 30,976 | 1,131 | 4.6× depth=1 — linear in depth |

### Parallel Region Entry (50 transitions per iteration)

| Benchmark | ns/op | B/op | allocs/op | Notes |
|-----------|------:|-----:|----------:|-------|
| regions=2 | 92,000 | 16,592 | 546 | |
| regions=4 | 140,000 | 26,648 | 774 | |
| regions=8 | 310,000 | 47,792 | 1,204 | ~linear in region count |

### Always-Transition Chain (20 rounds per iteration)

| Benchmark | ns/op | B/op | allocs/op | Notes |
|-----------|------:|-----:|----------:|-------|
| steps=5 | 96,000 | 12,560 | 720 | 7 transitions/round (5 always + GO + RESET) |
| steps=25 | 311,000 | 34,960 | 2,720 | 27 transitions/round |
| steps=50 | 551,000 | 62,960 | 5,220 | 52 transitions/round |

### Lifecycle Operations

| Benchmark | ns/op | B/op | allocs/op | Notes |
|-----------|------:|-----:|----------:|-------|
| InvokeStartCancel (20 rounds) | 59,000 | 11,312 | 301 | Goroutine spawn + cancel per round |
| Snapshot/simple | 107 | 64 | 2 | 1 active state, 0 history |
| Snapshot/parallel_4regions | 208 | 192 | 2 | 9 active states |
| Hydrate/simple | 2,231 | 4,376 | 12 | Includes goroutine start |
| Hydrate/parallel_4regions | 3,799 | 4,832 | 15 | |

### Allocation Check

| Benchmark | ns/op | B/op | allocs/op | Notes |
|-----------|------:|-----:|----------:|-------|
| SendTransition_ZeroAlloc | 92,000 | 10,320 | 520 | Same as NoObserver — per-batch (100 events), not per-event |

> **Note:** The 520 allocs/iteration include actor Start/Stop overhead
> (goroutine, channels, maps). Per-event allocation cost is
> ~5 allocs/event, dominated by observer payload construction.
> The `TestSendTransition_ZeroAllocCheck` test logs the per-run
> breakdown.
