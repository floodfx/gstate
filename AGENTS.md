## Tests

- Strict red → green → refactor TDD for every behavioral change. Doc-only edits are exempt.
- Don't use `time.Sleep` as a synchronization primitive. Use channels signaled from inside a callback, `sync.WaitGroup`, or the `kindBarrier` helper in [`observer_barrier_test.go`](./observer_barrier_test.go). `time.After` is OK only as a hard-timeout floor paired with a real signal. Real `time.Duration` deadlines are allowed only when the *semantics under test* require them (e.g. exercising `context.WithTimeout`'s deadline branch); when used, the test must carry a top-of-function comment explaining why a channel-based synchronizer isn't a substitute.

## Pre-commit checks

- **Run `just ci` locally before every commit.** This runs build → lint → vuln → test → test-race, the same chain CI runs. Catches lint/vuln/test failures before they reach the PR. Skipping this and pushing wastes a CI cycle when the failure was visible locally.
- If `just lint` flags fixable issues, `just fix` applies the safe auto-fixes (`gofmt -s`, `golangci-lint --fix`).

## PRs

- Use Conventional Commits in PR titles (see [CONTRIBUTING.md](./CONTRIBUTING.md)). The repo squash-merges, so the PR title is what Release Please reads to compute the next version.
