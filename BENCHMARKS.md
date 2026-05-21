# Benchmarks

Baseline numbers for the gstate engine hot path. Run with:

```bash
just bench
# or: go test -bench=. -benchmem -count=3 -benchtime=1s -run='^$' ./...
```

## Environment

| | |
|---|---|
| Go | 1.26.2 darwin/arm64 |
| CPU | Apple M1 Pro |
| GOMAXPROCS | 10 |

## Results

Each benchmark sends a batch of events through the engine and waits for
all transitions to complete. Numbers are per-iteration (one batch).

### Engine Hot Path (100 transitions per iteration)

| Benchmark | ns/op | B/op | allocs/op | Notes |
|-----------|------:|-----:|----------:|-------|
| SendTransition_TrulyNoObserver | 64,000 | 8,060 | 220 | Baseline: pure engine overhead, zero observers registered |
| SendTransition_TransitionOnlyObserver | 86,000 | 54,920 | 632 | 1 transition observer, triggers lazy event context |
| SendTransition_ThreeTransitionObservers | 95,000 | 55,976 | 652 | 3 transition observers, shows scaling with multiple observers |
| SendTransition_RecordingObserver | 115,000 | 123,025 | 953 | Mutex append + context copies + state recording |
| SendTransition_BaseObserver | 87,000 | 54,936 | 632 | Registers BaseObserver (unused/filtered) + 1 transition observer |

### Context Snapshot Cost (100 transitions per iteration)

| Benchmark | ns/op | B/op | allocs/op | Notes |
|-----------|------:|-----:|----------:|-------|
| ContextSnapshot_ValueCopy | 86,000 | 54,920 | 632 | Plain struct copy |
| ContextSnapshot_Cloner | 89,000 | 74,184 | 630 | Cloner interface: +4% time, ~1.35× bytes |

### Hierarchical Transitions (50 transitions per iteration)

| Benchmark | ns/op | B/op | allocs/op | Notes |
|-----------|------:|-----:|----------:|-------|
| depth=1 | 45,000 | 30,104 | 332 | Flat — no LCA walk |
| depth=4 | 110,000 | 71,576 | 736 | 2.4× depth=1 |
| depth=8 | 222,000 | 127,104 | 1,193 | 4.9× depth=1 — linear in depth |

### Parallel Region Entry (50 transitions per iteration)

| Benchmark | ns/op | B/op | allocs/op | Notes |
|-----------|------:|-----:|----------:|-------|
| regions=2 | 90,000 | 59,992 | 608 | |
| regions=4 | 144,000 | 90,848 | 836 | |
| regions=8 | 247,000 | 153,593 | 1,266 | ~linear in region count |

### Always-Transition Chain (20 rounds per iteration)

| Benchmark | ns/op | B/op | allocs/op | Notes |
|-----------|------:|-----:|----------:|-------|
| steps=5 | 95,000 | 65,160 | 772 | 7 transitions/round (5 always + GO + RESET) |
| steps=25 | 295,000 | 225,161 | 2,772 | 27 transitions/round |
| steps=50 | 548,000 | 425,162 | 5,272 | 52 transitions/round |

### Lifecycle Operations

| Benchmark | ns/op | B/op | allocs/op | Notes |
|-----------|------:|-----:|----------:|-------|
| InvokeStartCancel (20 rounds) | 54,000 | 34,842 | 414 | Goroutine spawn + cancel per round |
| Snapshot/simple | 92 | 64 | 2 | 1 active state, 0 history |
| Snapshot/parallel_4regions | 200 | 192 | 2 | 9 active states |
| Hydrate/simple | 1,780 | 4,712 | 14 | Includes goroutine start |
| Hydrate/parallel_4regions | 2,540 | 5,168 | 17 | |

### Allocation Check

| Benchmark | ns/op | B/op | allocs/op | Notes |
|-----------|------:|-----:|----------:|-------|
| SendTransition_ZeroAlloc | 64,000 | 8,060 | 220 | Same as TrulyNoObserver — per-batch (100 events), not per-event |

> **Note:** The ~220 allocs/iteration in the `TrulyNoObserver` baseline include actor `Start`/`Stop` overhead
> (goroutine, channels, maps). Per-event allocation cost is 0 allocs/event when no observers
> are registered (pure engine hot path).
> Note that the benchmarks consistently include the `actor.Stop()` WaitGroup drain as part of the timed
> execution loop, meaning that actor lifecycle teardown cost is factored into the reported ns/op.
> The `TestSendTransition_ZeroAllocCheck` test logs the per-run breakdown.
