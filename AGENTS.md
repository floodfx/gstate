## Tests

- Strict red → green → refactor TDD for every behavioral change. Doc-only edits are exempt.
- Don't use `time.Sleep` as a synchronization primitive. Use channels signaled from inside a callback, `sync.WaitGroup`, or the `kindBarrier` helper in [`observer_barrier_test.go`](./observer_barrier_test.go). `time.After` is OK only as a hard-timeout floor paired with a real signal.

## PRs

- Use Conventional Commits in PR titles (see [CONTRIBUTING.md](./CONTRIBUTING.md)). The repo squash-merges, so the PR title is what Release Please reads to compute the next version.
