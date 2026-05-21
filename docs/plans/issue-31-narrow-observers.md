# Plan: Narrow Observer interfaces + lazy Data (Options B + A) — Issue #31

## Context

`Actor.dataSnapshotPtr()` (actor.go:83-86) calls `Cloner.Clone()` on every
observer payload built during event handling. There are 8 clone sites per
event in the worst case (guard, action, transition, exit, entry, ...).
Every callsite also constructs a typed payload struct, reads `time.Now()`,
and performs an interface-method dispatch — paid regardless of whether the
installed observer cares about that callback kind.

Today's code defaults `cfg.observer = NopObserver{}` when no observer
is installed (actor.go:143-144, 204-205) so callsites can dereference
`a.observer.OnX(...)` unconditionally. The default exists purely so the
callsites don't have to nil-check — and the hot path pays the full
price (Clone, struct alloc, time.Now, dispatch) even when no observer
is installed and even when an installed observer only cares about *one*
of the nine callback kinds.

**In the new design, there is no default observer.** The Actor stores
nine narrow observer fields (`transitionObs`, `guardObs`, ...), each
typed as a narrow interface and each initialized to `nil` when nothing
was installed. Every callsite becomes `if a.transitionObs != nil { ... }`.
`NopObserver` is removed because nothing needs it: nil == no observer ==
zero work. Issue #31 collapses to "skip nil dispatch."

The user has chosen the combined fix:

- **B**: split the monolithic `Observer` interface into nine narrow
  per-event interfaces. The actor discovers which an installed observer
  implements at install time and only builds/dispatches payloads for
  those kinds.
- **A**: convert the `Data *D` field on each payload struct into a
  `Data()` method that clones on demand and memoizes the result. Payload
  construction captures the actor's `D` by value (cheap); observers that
  don't read `Data` pay zero clones.

Combined, an observer that subscribes only to `OnTransition` and never
reads `Data` pays one nil-check per non-transition callback site and one
value-copy on every transition. An observer that subscribes to nothing
(no `WithObserver` call) pays nine nil-checks per event handle and zero
allocations.

Project is at 0.2.2; pre-1.0 breaking changes are acceptable, documented
via CHANGELOG.

---

## Final API surface

### Marker interface `Observer` + base struct `BaseObserver` (observer.go)

The umbrella `Observer` is **redefined as a marker interface**, not a
9-method aggregate. Every narrow interface embeds it. `WithObserver`
stays typed (`WithObserver(Observer)`); no `any` in the public API.

```go
// Observer is the marker every gstate observer must satisfy. The
// unexported method seals the interface so only types embedding
// BaseObserver (or NopObserver, which itself embeds BaseObserver) can
// implement it. This keeps WithObserver compile-time typed while still
// allowing observers to choose which narrow callback interfaces they
// implement.
type Observer[S ~string, E ~string, D Cloner[D]] interface {
    gstateObserver()
}

// BaseObserver is the marker-implementing zero struct. Embed it in your
// observer type to satisfy Observer. Provides no callback methods —
// implement whichever narrow interfaces you care about directly.
//
//   type MyTransitionLogger struct {
//       gstate.BaseObserver[MyState, MyEvent, MyData]
//   }
//   func (l *MyTransitionLogger) OnTransition(
//       ctx context.Context,
//       e *gstate.TransitionEvent[MyState, MyEvent, MyData],
//   ) { /* ... */ }
type BaseObserver[S ~string, E ~string, D Cloner[D]] struct{}

func (BaseObserver[S, E, D]) gstateObserver() {}
```

### Narrow per-callback interfaces

Each narrow interface composes the marker + its callback signature.
Because the marker has an unexported method, satisfying any narrow
interface requires embedding `BaseObserver` (or anything that embeds it,
including `NopObserver`).

```go
type TransitionObserver[S ~string, E ~string, D Cloner[D]] interface {
    Observer[S, E, D]
    OnTransition(context.Context, *TransitionEvent[S, E, D])
}
type GuardObserver[S ~string, E ~string, D Cloner[D]] interface {
    Observer[S, E, D]
    OnGuardEvaluated(context.Context, *GuardEvent[S, E, D])
}
type InvokeStartedObserver[S ~string, E ~string, D Cloner[D]] interface {
    Observer[S, E, D]
    OnInvokeStarted(context.Context, *InvokeEvent[S, E, D])
}
type InvokeCompletedObserver[S ~string, E ~string, D Cloner[D]] interface {
    Observer[S, E, D]
    OnInvokeCompleted(context.Context, *InvokeEvent[S, E, D])
}
type StateEnteredObserver[S ~string, E ~string, D Cloner[D]] interface {
    Observer[S, E, D]
    OnStateEntered(context.Context, *StateEvent[S, E, D])
}
type StateExitedObserver[S ~string, E ~string, D Cloner[D]] interface {
    Observer[S, E, D]
    OnStateExited(context.Context, *StateEvent[S, E, D])
}
type ActionObserver[S ~string, E ~string, D Cloner[D]] interface {
    Observer[S, E, D]
    OnActionExecuted(context.Context, *ActionEvent[S, E, D])
}
type EventReceivedObserver[S ~string, E ~string, D Cloner[D]] interface {
    Observer[S, E, D]
    OnEventReceived(context.Context, *EventNotice[S, E, D])
}
type EventDroppedObserver[S ~string, E ~string, D Cloner[D]] interface {
    Observer[S, E, D]
    OnEventDropped(context.Context, *EventNotice[S, E, D])
}
```

### `WithObservers` (variadic) — multiple observers, shared event payload

```go
func (m *Machine[S, E, D]) WithObservers(obs ...Observer[S, E, D]) Option[S, E, D]
```

Variadic. Each argument is independently type-asserted against the nine
narrow interfaces; matches are appended to per-kind slices on the actor.
Compile-time check that each argument satisfies the marker. **One
observer value can implement any subset (or all nine) of the narrow
interfaces** — there is no "one at a time" restriction. The marker is
*just a marker*; dispatch is determined entirely by which `OnX` methods
the user's type defines.

`WithObservers()` with no arguments, `WithObservers(nil)`, and "never
called" are all equivalent — every per-kind slice stays empty. Multiple
calls follow the existing Option last-wins convention (the second call
replaces the first); users wanting to install multiple observers must
pass them all in one call.

A typical multi-kind observer:

```go
type MyObserver struct {
    gstate.BaseObserver[MyState, MyEvent, MyData]  // satisfies marker
}

// implements TransitionObserver
func (o *MyObserver) OnTransition(ctx context.Context,
    e *gstate.TransitionEvent[MyState, MyEvent, MyData]) { /* ... */ }

// implements GuardObserver
func (o *MyObserver) OnGuardEvaluated(ctx context.Context,
    e *gstate.GuardEvent[MyState, MyEvent, MyData]) { /* ... */ }

// (no OnStateEntered, OnStateExited, etc. — those callbacks never fire
// for this observer; actor's stateEnteredObs/stateExitedObs slices
// simply don't include this value)
```

Install (in actor.go):

```go
func (a *Actor[S, E, D]) installObservers(observers []Observer[S, E, D]) {
    for _, obs := range observers {
        if obs == nil {
            continue
        }
        if v, ok := obs.(TransitionObserver[S, E, D]); ok {
            a.transitionObs = append(a.transitionObs, v)
        }
        if v, ok := obs.(GuardObserver[S, E, D]); ok {
            a.guardObs = append(a.guardObs, v)
        }
        // ... 7 more narrow assertions
    }
}
```

### Shared `Data()` across observers per callback

When multiple observers implement the same narrow interface, the actor
constructs **one** event-payload struct per callsite firing and passes
the same `*TransitionEvent` (etc.) pointer to every matching observer.

Because `Data()` memoizes its first clone via `sync.Once` on the event
struct itself, the *first* observer to call `e.Data()` performs the
Clone; every subsequent observer (same callback firing, same event
pointer) receives the same cached `*D`. Result: at most one `Clone()`
per callback firing regardless of how many observers subscribe to that
kind, regardless of how many of them read `Data()`.

Callsite pattern (example for OnTransition):

```go
if len(a.transitionObs) > 0 {
    e := a.newTransitionEvent(from, to, event) // one alloc
    for _, o := range a.transitionObs {
        o.OnTransition(ctx, e)                  // shared pointer
    }
}
```

There is no default observer and no coalesce. Every callsite is gated
`if len(a.XObs) > 0 { ... }`. Empty means nothing happens — not
"dispatch to a no-op."

### `NopObserver` is removed (and unnecessary)

`NopObserver` is deleted outright. It only existed today so the actor's
callsites could dereference a non-nil observer without nil-checking. In
the new design, every callsite already nil-checks its narrow field; a
"do-nothing observer" is just `nil`, which is the cheapest possible
no-observer representation — no allocation, no method-dispatch, no
struct value.

With narrow interfaces available, the "embed and override one method"
idiom is replaced by "embed `BaseObserver` and implement only the
callback you want." Every existing embedder must migrate. The migration
is mechanical:

```go
// before
type MyObs struct {
    gstate.NopObserver[MyState, MyEvent, MyData]
}
func (m *MyObs) OnTransition(ctx context.Context,
    e gstate.TransitionEvent[MyState, MyEvent, MyData]) { /* ... */ }

// after
type MyObs struct {
    gstate.BaseObserver[MyState, MyEvent, MyData]
}
func (m *MyObs) OnTransition(ctx context.Context,
    e *gstate.TransitionEvent[MyState, MyEvent, MyData]) { /* ... */ }
```

Two changes per embedder: `NopObserver` → `BaseObserver` in the embed,
and `XEvent` → `*XEvent` in each implemented callback signature.

### Idiomatic precedent for the marker-interface pattern

- `protoreflect.Message` / `proto.Message` — interfaces with an
  unexported sealing method, satisfied by embedding a base struct.
- `ast.Node` (`go/ast`) — interfaces sealed via unexported `Pos()`-style
  marker method per node kind.
- `database/sql/driver.Value` and related sealed unions.
- `tea.Msg` / `tea.Model` (charmbracelet/bubbletea) — sealed interface
  family with documented base.

The Go community treats this as the standard way to expose a typed
"family of related interfaces" with compile-time safety.

### Lazy `Data()` on payload structs (pointer-receiver, sync.Once memoized)

The `Data *D` field on each payload is replaced with a `Data()` method
on a pointer receiver. The struct holds the actor's data by value plus
a `sync.Once` and a `*D` cache. First `Data()` call clones and caches;
subsequent calls return the cached pointer. `sync.Once` makes this
safe across goroutines (relevant when consumers like
`RecordingObserver` stash event pointers and read `Data()` later).

```go
type TransitionEvent[S ~string, E ~string, D Cloner[D]] struct {
    MachineID string
    ActorID   ActorID
    From      S
    To        S
    Event     E
    Timestamp time.Time

    data   D
    once   sync.Once
    cached *D
}

func (e *TransitionEvent[S, E, D]) Data() *D {
    e.once.Do(func() {
        c := e.data.Clone()
        e.cached = &c
    })
    return e.cached
}
```

Each narrow observer interface takes a pointer-typed event:

```go
type TransitionObserver[S, E, D] interface {
    Observer[S, E, D]
    OnTransition(context.Context, *TransitionEvent[S, E, D])
}
```

When the actor fans out to multiple observers, the same `*TransitionEvent`
is passed to each. The first observer that calls `e.Data()` triggers the
clone; every subsequent observer sees the same cached pointer. Result:
at most one clone per callback firing regardless of fan-out width.

**Migration impact** (mechanical):
- Every observer method signature: `OnX(ctx, e XEvent)` → `OnX(ctx, e *XEvent)`.
- Every read site: `payload.Data` → `payload.Data()`.
- Payload structs are no longer trivially copyable (sync.Once forbids
  post-use copies); the pointer-typed callbacks naturally avoid copies.

### Stringer / JSON

- `String()` methods stay; they no longer reference `Data` since none
  did before (verified observer.go:90-96, 114-116, 135-144, 172-174,
  193-199, 215-220 — none of the existing `String()` impls touch Data).
- `MarshalJSON`: today only `InvokeEvent` has custom JSON. Other payload
  types use the default reflection-based marshaler which would now skip
  the unexported `data` field. Add a `MarshalJSON` per payload that
  calls `Data()` to materialize the field at serialization time, so the
  wire format is preserved.

### `MultiObserver` is removed

Today's `MultiObserver` (observer.go:234) existed because
`WithObserver` took a single `Observer`. With variadic `WithObservers`,
fan-out is the default, and `MultiObserver` adds nothing.

Remove the `MultiObserver` type entirely. Users who today write:

```go
gstate.MultiObserver[S, E, D]{logger, recorder, tracer}
```

migrate to:

```go
m.WithObservers(logger, recorder, tracer)
```

This is a strict simplification of the API.

### `NopObserver`, `MultiObserver`, `ObserverFuncs`, `SignalObserver`, `RecordingObserver`

- `NopObserver` → **removed**. Embedders migrate to `BaseObserver` (see
  migration block above).
- `MultiObserver` → **removed**. Variadic `WithObservers` replaces it
  (see section above).
- `ObserverFuncs` → embeds `BaseObserver`, implements every narrow
  interface, dispatches to the matching `XFunc` field as today. Callback
  signatures updated to `*XEvent`.
- `SignalObserver` → embeds `BaseObserver`, implements every narrow
  interface. Each callback fires the same signal. Updated to `*XEvent`.
- `RecordingObserver` → embeds `BaseObserver`, implements every narrow
  interface. **Materializes `Data()` at record time** so recorded
  payloads carry the cloned snapshot (preserves today's semantics for
  any test code that reads `rec.Transitions()[0].Data()`).

### Actor struct field layout

Replace the single `observer Observer[S, E, D]` field (actor.go:64)
with nine narrow-typed slices (each empty when no observer subscribes
to that kind):

```go
type Actor[S, E, D Cloner[D]] struct {
    // ...
    transitionObs      []TransitionObserver[S, E, D]
    guardObs           []GuardObserver[S, E, D]
    invokeStartedObs   []InvokeStartedObserver[S, E, D]
    invokeCompletedObs []InvokeCompletedObserver[S, E, D]
    stateEnteredObs    []StateEnteredObserver[S, E, D]
    stateExitedObs     []StateExitedObserver[S, E, D]
    actionObs          []ActionObserver[S, E, D]
    eventReceivedObs   []EventReceivedObserver[S, E, D]
    eventDroppedObs    []EventDroppedObserver[S, E, D]
}
```

Slices are append-only at install time and read-only thereafter, so no
locking is needed beyond what already protects the actor.

---

## Callsite changes (actor.go)

Each of the 12 observer callsites becomes a length-check, single
payload allocation, and a fan-out loop:

```go
// before (actor.go:684)
a.observer.OnEventReceived(ctx, EventNotice[S, E, D]{...})

// after
if len(a.eventReceivedObs) > 0 {
    e := a.newEventReceived(event)
    for _, o := range a.eventReceivedObs {
        o.OnEventReceived(ctx, e)
    }
}
```

The event struct is constructed once; the pointer is shared by every
observer in the slice; `e.Data()` clones at most once across the
entire fan-out (via the event struct's `sync.Once`).

Callsites and their target fields (line numbers from current HEAD):

| Line | Callback                      | Field                |
|------|-------------------------------|----------------------|
| 452  | OnInvokeStarted               | invokeStartedObs     |
| 470  | OnInvokeCompleted             | invokeCompletedObs   |
| 684  | OnEventReceived               | eventReceivedObs     |
| 704  | OnGuardEvaluated              | guardObs             |
| 727  | OnEventDropped                | eventDroppedObs      |
| 745  | OnActionExecuted (internal)   | actionObs            |
| 755  | OnTransition (internal)       | transitionObs        |
| 832  | OnStateExited                 | stateExitedObs       |
| 844  | OnActionExecuted (transition) | actionObs            |
| 880  | OnTransition (transition)     | transitionObs        |
| 905  | OnStateEntered                | stateEnteredObs      |
| 1015 | OnGuardEvaluated (always)     | guardObs             |

Each payload literal drops its `Data:` field; the unexported `data` is
populated by struct literal name (or by constructor — see helper below).

Optional helper to keep each callsite tight:

```go
func (a *Actor[S, E, D]) newTransitionEvent(from, to S, event E) *TransitionEvent[S, E, D] {
    return &TransitionEvent[S, E, D]{
        MachineID: a.machine.ID,
        ActorID:   a.id,
        From:      from,
        To:        to,
        Event:     event,
        Timestamp: time.Now(),
        data:      a.data,
    }
}
```

Nine such constructors (one per payload type). Reduces each callsite to
two lines.

`dataSnapshotPtr` (actor.go:83-86) is removed — no remaining callers.

---

## Install-time wiring (`Start`/`Hydrate`)

Delete the current `NopObserver` coalesce (actor.go:143-144 and
actor.go:204-205). Replace the single-line observer assignment with a
helper that maps the marker-typed argument onto the actor's nine narrow
fields. Any field whose narrow interface the argument doesn't implement
stays nil; any callsite checking that field will skip.

```go
func (a *Actor[S, E, D]) installObservers(observers []Observer[S, E, D]) {
    for _, obs := range observers {
        if obs == nil {
            continue
        }
        if v, ok := obs.(TransitionObserver[S, E, D]); ok {
            a.transitionObs = append(a.transitionObs, v)
        }
        if v, ok := obs.(GuardObserver[S, E, D]); ok {
            a.guardObs = append(a.guardObs, v)
        }
        // ... 7 more narrow assertions
    }
}
```

The argument is a `[]Observer[S, E, D]` (the slice passed via variadic
`WithObservers`). Type-asserting each value from the marker against each
narrow interface is sound because the marker has only the unexported
method; the narrow interfaces add real callback methods, and the
dynamic type either has them or doesn't.

`installMultiObserver` iterates members, building nine per-kind slices,
then wraps each non-empty slice in a small adapter implementing the
narrow interface and assigns to the actor field.

Drop the existing `NopObserver` coalesce — with nil-typed narrow fields,
no coalesce is needed. The current code at actor.go:143-144 and 204-205
becomes a no-op delete.

---

## Migration / breaking changes

This is a breaking change. Summary for CHANGELOG:

1. `Observer` interface is now a sealed *marker* (one unexported method)
   instead of a 9-method aggregate. To satisfy it, embed
   `gstate.BaseObserver[S, E, D]` in your observer type.
2. `NopObserver` is **removed**. Migrate by replacing the embed with
   `BaseObserver` and implementing only the narrow interfaces you need.
3. Observer callback methods now receive `*XEvent` instead of `XEvent`.
   Mechanical `s/(e XEvent)/(e *XEvent)/g` fix per implementation.
4. Payload `Data *D` field removed; replaced by `Data()` method on
   pointer receiver. Mechanical `s/payload.Data/payload.Data()/g`.
   JSON serialization is preserved via custom `MarshalJSON` — wire
   format unchanged for any consumer parsing observer event dumps.
5. Nine new narrow per-callback interfaces (`TransitionObserver`,
   `GuardObserver`, ...). Implement only the ones you care about.
6. `MultiObserver` is **removed**. Variadic `WithObservers(obs ...Observer)`
   replaces it. Migration: `MultiObserver{a,b,c}` → `WithObservers(a,b,c)`.
7. `WithObserver` is **renamed** to `WithObservers` (plural) and is now
   variadic:
   `func WithObservers(obs ...Observer[S,E,D]) Option[S,E,D]`.
8. `RecordingObserver` materializes `Data()` at record time so recorded
   payloads carry the same cloned snapshot today's recorder produces.
9. Multiple observers subscribing to the same callback kind share a
   single event-payload allocation and (via `sync.Once` on the event)
   a single `Data()` clone per callback firing.

No deprecation cycle — clean break at 0.3.0. Pre-1.0 norms apply.

---

## Idiomatic precedents

- **Narrow interfaces + type assertion**: stdlib uses this exact pattern.
  `io.Copy` asserts `WriterTo` and `ReaderFrom`; `http.ResponseWriter`
  consumers assert `http.Flusher`, `http.Hijacker`, `http.Pusher`;
  `sort.Interface` vs the newer `slices.SortFunc` are similar trade.
- **Lazy / memoized getter**: `reflect.Type.Method()` materializes;
  `regexp.Regexp` lazy-compiles; `sync.Once` is the canonical guard.
- **Umbrella interface as composition**: `io.ReadWriteCloser`,
  `fs.ReadDirFile`, `database/sql/driver.Conn` extensions.

---

## XState comparison

XState v5 has one `inspect` callback receiving a discriminated-union
`InspectionEvent` (`@xstate.event`, `@xstate.snapshot`, etc.). Users
filter by `event.type`. JS-idiomatic; Go-idiomatic equivalent is narrow
interfaces with type assertion (this plan). XState also produces one
immutable snapshot per step; reference equality is the perf story
there. Our Option D would mimic XState more directly, but B+A gives us
the same end-state perf via a different idiom.

---

## Implementation phases (TDD)

Phase 1 — Test scaffolding (red).

1.1. Add `countingCloner` test helper: a `D` whose `Clone()` increments
     an atomic counter. Place in `data_safety_test.go` or new file
     `observer_lazy_test.go`.

1.2. Write failing tests:
     - No-observer actor: drive 10 transitions, assert clone count == 0.
     - Narrow `TransitionObserver` only, observer ignores `Data()`:
       assert clone count == 0.
     - Narrow `TransitionObserver` only, observer calls `e.Data()` once
       per callback: assert clone count == 10.
     - Narrow `TransitionObserver` only, observer calls `e.Data()` twice
       per callback: assert clone count == 10 (memoization).
     - Two observers, both implementing `TransitionObserver`, both
       calling `e.Data()`: assert clone count == 10 (shared via sync.Once
       on the event struct).
     - `RecordingObserver`: behavior preserved (count > 0; documents
       record-time materialization).
     - `WithObservers(a, b)` where `a` implements only `TransitionObserver`
       and `b` implements only `GuardObserver`: each fires only for its
       kind.

Phase 2 — Narrow interfaces + install wiring (green).

2.1. Add the nine narrow interfaces to observer.go.
2.2. Redefine `Observer` as composition.
2.3. Change every callback signature in narrow + umbrella interfaces to
     take `*XEvent` (pointer).
2.4. Delete `NopObserver` and `MultiObserver` types. Update
     `ObserverFuncs`, `SignalObserver`, `RecordingObserver` to embed
     `BaseObserver` and use `*XEvent` callback signatures.
2.5. Add `installObservers` helper in actor.go.
2.6. Replace `observer Observer[S, E, D]` field with nine narrow slice
     fields.
2.7. Rename `WithObserver` → `WithObservers` and make it variadic.
2.8. Update `Start` and `Hydrate` to call `installObservers`; delete
     the `NopObserver` coalesce.
2.9. Update all 12 actor.go callsites to length-check + single-payload-
     allocation + fan-out loop. Tests pass except the clone-count tests
     (still fail — `Data()` not implemented yet).

Phase 3 — Lazy Data (green).

3.1. Replace `Data *D` field on each of `TransitionEvent`, `GuardEvent`,
     `StateEvent`, `ActionEvent` with unexported `data D` + `once sync.Once`
     + `cached *D`.
3.2. Add `Data()` method on pointer receiver to each.
3.3. Add `MarshalJSON` on each affected payload type that materializes
     `Data()` into a field at marshal time.
3.4. Update payload constructors (helpers in actor.go) to set `data`.
3.5. Remove `dataSnapshotPtr`. Clone-count tests pass.

Phase 4 — Verification + cleanup.

4.1. `just ci` clean.
4.2. Fix `BenchmarkSendTransition_NoObserver` (bench_test.go:114) — today
     it installs an `ObserverFuncs` waiter, defeating its name. Add a
     parallel `BenchmarkSendTransition_TrulyNoObserver` that drives
     transitions to completion without any observer (poll
     `actor.State()` for terminal). Compare allocs/op before vs after.
4.3. Add `BenchmarkSendTransition_TransitionOnlyObserver` to demonstrate
     the narrow-interface win (observer ignores Data).
4.4. Update CHANGELOG with the breaking-change matrix above.
4.5. Update godoc on `Observer`, each narrow interface, and each
     payload's `Data()` method to document semantics — especially the
     "Data() clones on demand and memoizes" contract.
4.6. Migrate every existing `NopObserver` embedder to `BaseObserver` and
     update each callback signature to pointer-receiver event types.
     Sites identified in Phase 1 exploration:
     - observer_barrier_test.go:19
     - data_safety_test.go:15, 122
     - multi_observer_test.go:11, 101
     - sendctx_test.go:12
     - example_observer_test.go:34, 78
     - machine_options_test.go:51
     - always_delayed_hooks_test.go:49
     - invoke_hooks_test.go:83
     - observer_test.go:165
     Plus any `Data` field reads inside these or other tests — sweep
     for `payload.Data` and convert to `payload.Data()`.

---

## Critical files

- `/Users/donnie/go/src/github.com/floodfx/gstate/observer.go` — biggest
  surface: nine new interfaces, payload struct redesign, MultiObserver
  type change, all helper observer types updated.
- `/Users/donnie/go/src/github.com/floodfx/gstate/actor.go` — Actor
  struct field, Start, Hydrate, install helpers, 12 callsites,
  `dataSnapshotPtr` removal.
- `/Users/donnie/go/src/github.com/floodfx/gstate/bench_test.go` —
  rename misleading benchmark, add two new benchmarks.
- `/Users/donnie/go/src/github.com/floodfx/gstate/CHANGELOG.md` —
  breaking-change entry.
- Every test file listed in step 4.6.

---

## Verification

### Functional
- `just ci` green (build, lint, vuln, test, test-race).
- All nine clone-count assertions from Phase 1 hold.
- `go vet ./...` clean (sync.Once-in-struct lints, etc.).
- Run `go test -race ./...` 10× to catch any sync.Once misuse.

### Performance (before vs after)
- `BenchmarkSendTransition_TrulyNoObserver`: allocs/op should drop to
  near zero per transition (was ~5 clones/transition).
- `BenchmarkSendTransition_TransitionOnlyObserver` (new): allocs/op
  near zero when observer ignores `Data()`; one clone/transition when
  it reads `Data()`.
- `BenchmarkSendTransition_ThreeTransitionObservers` (new): same as
  TransitionOnlyObserver but with three observers in the slice that
  each call `Data()`. Expect *one* clone per transition (memoized),
  proving the shared-payload story.
- `BenchmarkSendTransition_RecordingObserver`: `RecordingObserver`
  materializes `Data()` at record time, so it keeps one clone per
  recorded event (matching today's semantics).

### Wire compatibility
- Snapshot JSON serialization unaffected (Snapshot/Hydrate don't go
  through observer payloads).
- Observer payload JSON serialization: round-trip a recorded
  `TransitionEvent` and assert the JSON shape matches pre-change golden.

---

## Resolved design decisions

- **JSON shape**: preserved via custom `MarshalJSON` on each payload.
  Wire format unchanged from today.
- **RecordingObserver**: materializes `Data()` at record time so
  recorded payloads carry an already-cloned snapshot.
- **NopObserver**: removed. Every embedder migrates to `BaseObserver`
  and implements only the narrow interfaces it actually needs.
