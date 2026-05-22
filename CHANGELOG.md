# Changelog

## [0.3.1](https://github.com/floodfx/gstate/compare/v0.3.0...v0.3.1) (2026-05-22)


### Bug Fixes

* stop emitting custom CSS classes to support dark theme renderings ([#50](https://github.com/floodfx/gstate/issues/50)) ([33d897f](https://github.com/floodfx/gstate/commit/33d897f84406dabba6c85e9acc91fe2e646c349d))

## [0.3.0](https://github.com/floodfx/gstate/compare/v0.2.2...v0.3.0) (2026-05-21)


### ⚠ BREAKING CHANGES

* implement safe invoke context mutations ([#41](https://github.com/floodfx/gstate/issues/41)) (#46)

### Features

* add build-time static validation for machine definitions ([#42](https://github.com/floodfx/gstate/issues/42)) ([1ee7055](https://github.com/floodfx/gstate/commit/1ee705562f1f69378018b722d3c71e03dd5a7f70))
* implement safe invoke context mutations ([#41](https://github.com/floodfx/gstate/issues/41)) ([#46](https://github.com/floodfx/gstate/issues/46)) ([c1fdae4](https://github.com/floodfx/gstate/commit/c1fdae4a02c69bb0854639d898617395eb0af708))
* mechanical rename of type parameter C to D ([58a72aa](https://github.com/floodfx/gstate/commit/58a72aa7304a582f77c8a7d54464eb2bc7740425))
* rename Actor.Context to Actor.Data and internal fields ([bd54417](https://github.com/floodfx/gstate/commit/bd544179c40a3a70013b66bd00c9820c8e051b12))
* rename Snapshot and event Context fields to Data ([1baed5d](https://github.com/floodfx/gstate/commit/1baed5d2de9d4b928aca9caecb3ea88396d1b87a))


### Performance Improvements

* **observer:** refactor observers to narrow interfaces and lazy evaluation ([3a7f868](https://github.com/floodfx/gstate/commit/3a7f8686b5f2c5bb10539e76d2cf6bd0f48f9b80))
* optimize active state sorting using slices.SortFunc and add tie-breaker ([#43](https://github.com/floodfx/gstate/issues/43)) ([05201c4](https://github.com/floodfx/gstate/commit/05201c4f06888df04fc0fdaf9488a88c6efed817))

## [0.3.0](https://github.com/floodfx/gstate/compare/v0.2.2...v0.3.0) (2026-05-21)


### ⚠ BREAKING CHANGES

* **observer:** Refactored the monolithic `Observer` interface into specialized narrow interfaces and introduced lazy data cloning.
  1. `Observer` interface is now a sealed *marker* (one unexported method) instead of a 9-method aggregate. Custom observers must embed `gstate.BaseObserver[S, E, D]`.
  2. `NopObserver` is **removed**. Migrate by embedding `BaseObserver` and implementing only the narrow interfaces you need.
  3. Observer callback methods now receive `*XEvent` instead of `XEvent`.
  4. Payload `Data *D` field is removed and replaced by the `Data()` method on a pointer receiver. Custom `MarshalJSON` implementations preserve the JSON shape.
  5. Nine new narrow per-callback interfaces (`TransitionObserver`, `GuardObserver`, etc.) are introduced so observers only listen to relevant lifecycle stages.
  6. `MultiObserver` is **removed**. Variadic `WithObservers(obs ...Observer)` replaces it.
  7. `WithObserver` is **renamed** to `WithObservers` (plural) and is now variadic.
  8. `RecordingObserver` materializes `Data()` eagerly at record time, preserving snapshot semantics.
  9. Multiple observers subscribing to the same callback kind share a single event-payload allocation and a single `Data()` clone per callback firing (via `sync.Once`).

## [0.2.2](https://github.com/floodfx/gstate/compare/v0.2.1...v0.2.2) (2026-05-19)


### Features

* **mermaid:** add entry/exit/invoke labels and switch to flowchart ([5decad0](https://github.com/floodfx/gstate/commit/5decad098998bcd5d83125a3ffa90fc718ac7913))


### Bug Fixes

* guard Always processing against nil stateDef from Hydrate ([d997bba](https://github.com/floodfx/gstate/commit/d997bba95b5fff2548cf0230f75390f60937df8d))

## [0.2.1](https://github.com/floodfx/gstate/compare/v0.2.0...v0.2.1) (2026-05-17)


### Features

* add fuzz targets for Hydrate, builder, and event sequence ([89741c3](https://github.com/floodfx/gstate/commit/89741c30bdd0758a637331f5d7e532b298adcec3))
* add performance benchmarks for engine hot path ([#4](https://github.com/floodfx/gstate/issues/4)) ([31d7c3c](https://github.com/floodfx/gstate/commit/31d7c3ccd6e487ac54f5497e09ecb099c45e2906))


### Bug Fixes

* coalesce nil Snapshot.History to empty map in Hydrate ([3aaa982](https://github.com/floodfx/gstate/commit/3aaa982f4af55dda16b121bbd9acdfdbeb397859))
* remove ineffectual assignment flagged by golangci-lint ([899ea73](https://github.com/floodfx/gstate/commit/899ea7314857d291cb8f1cba49a76301f48b8c4f))


### Performance Improvements

* cache getSortedActiveStatesLocked to eliminate per-call allocation ([b8bd2c5](https://github.com/floodfx/gstate/commit/b8bd2c5049d26ca0de0cdd70eea19ea34a482b21))

## [0.2.0](https://github.com/floodfx/gstate/compare/v0.1.0...v0.2.0) (2026-05-16)


### ⚠ BREAKING CHANGES

* Actor.SendCtx now returns error. Source-compatible for callers that ignored the previous void return; callers that assigned a name with the old signature will need to add a variable.

### Features

* add SignalObserver and ObserverFuncs observer adapters ([#28](https://github.com/floodfx/gstate/issues/28)) ([041c3f8](https://github.com/floodfx/gstate/commit/041c3f8eda0039ab25ffd9e4ac18cb5fe30fdbf9))
* auto-stop actor when machine reaches a "done" top-level state ([e7f12cb](https://github.com/floodfx/gstate/commit/e7f12cb12e57d47b4d7f8dbb49831cb46dd1ea85))
* SendCtx returns error and honors ctx during enqueue ([726e421](https://github.com/floodfx/gstate/commit/726e421a70943c90c93b50265b6b24eb1121cf62))


### Bug Fixes

* drain invoke goroutines and stop accepting sends in Actor.Stop ([804fa4c](https://github.com/floodfx/gstate/commit/804fa4c4b688a4c34b68d6292678c94b2e448eba))

## 0.1.0 (2026-05-14)


### ⚠ BREAKING CHANGES

* StartWithOptions and the Options struct are removed. Construction now uses variadic functional options:

### Features

* add comprehensive examples for all features ([372de96](https://github.com/floodfx/gstate/commit/372de967b360b2ebdce71575d528b8961ed8dced))
* add ToMermaid() using go-mermaid for syntactically valid output ([453bfdb](https://github.com/floodfx/gstate/commit/453bfdbf9b9364f06be477c944c7e36681621492))
* implement core machine builder and actor engine ([13bb844](https://github.com/floodfx/gstate/commit/13bb8449e76fbc12d80c5b2a024618a4aa6c56ff))
* native Observer interface for statechart lifecycle events ([#10](https://github.com/floodfx/gstate/issues/10)) ([4ee854b](https://github.com/floodfx/gstate/commit/4ee854bb6ae32536fde1b100292fcb41b688fe05))
* preserve transition declaration order instead of sorting alphabetically ([44ca1be](https://github.com/floodfx/gstate/commit/44ca1be662dd1933c09868c9d2f5e32c4cfd9c07))


### Bug Fixes

* add ID attribute to history pseudo-state nodes ([7e75359](https://github.com/floodfx/gstate/commit/7e75359ea5a700e7f3a0dbb9a47ff6fdc5dc27d9))
* emit &lt;parallel&gt; directly instead of wrapping in &lt;state&gt; ([c73cae6](https://github.com/floodfx/gstate/commit/c73cae66e90da622f9071f89d6395522f7b86003))
* formatDuration produces SCXML-compatible millisecond durations ([9a2f783](https://github.com/floodfx/gstate/commit/9a2f78399cb148fae9d63c47bc3aa44ca0feb70b))
* preserve context values after invoke completion ([#12](https://github.com/floodfx/gstate/issues/12)) ([66296c7](https://github.com/floodfx/gstate/commit/66296c72b25dea79c7c3545a8aa06ecc9b7f4220))
* race in TestSelfTransitionReEntry counters ([225dff3](https://github.com/floodfx/gstate/commit/225dff309d5c3b19da907bf5ac88180191c579da)), closes [#11](https://github.com/floodfx/gstate/issues/11)
* sort transition events alphabetically for deterministic SCXML output ([8e7c3c5](https://github.com/floodfx/gstate/commit/8e7c3c591360965e13a79eb62ca1aa8913d660e7))
