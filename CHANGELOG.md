# Changelog

## 1.0.0 (2026-05-14)


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
