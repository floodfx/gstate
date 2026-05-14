# Contributing

Thanks for your interest in `gstate`. Issues and pull requests are welcome.

## Pull requests

- Open a PR against `main`. CI runs `go build`, `go vet`, `go test`, and `go test -race`; please make sure all four are green locally before requesting review.
- Add tests for new behavior. The repo uses deterministic synchronisation in tests (channels, `sync.WaitGroup`, observer barriers) — please avoid `time.Sleep` as a synchronisation primitive. `time.After` is acceptable only as a hard-timeout floor paired with a deterministic signal.

## Commit messages

This repo uses [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) and squash-merges PRs. The PR title becomes the merge-commit message on `main`, and [Release Please](https://github.com/googleapis/release-please) reads those commit messages to decide version bumps.

Format: `<type>[!]: <description>`

| Type        | When to use                                                            | Version bump        |
| ----------- | ---------------------------------------------------------------------- | ------------------- |
| `feat`      | A new feature.                                                         | minor (e.g. 0.2.0 → 0.3.0) |
| `fix`       | A bug fix.                                                             | patch (e.g. 0.2.0 → 0.2.1) |
| `perf`      | A performance improvement that doesn't change behavior.                | patch               |
| `docs`      | Documentation only.                                                    | none                |
| `refactor`  | Internal restructuring with no external behavior change.               | none                |
| `test`      | Test-only changes.                                                     | none                |
| `ci`        | CI/CD configuration.                                                   | none                |
| `chore`     | Tooling, deps, repo housekeeping.                                      | none                |
| `style`     | Formatting only (whitespace, semicolons).                              | none                |

Append `!` after the type to signal a breaking change — e.g. `feat!: rename Actor.Send to Actor.Dispatch`. Under v0.x, a breaking change still bumps the minor version (semver pre-1.0 convention); after v1.0 it bumps major.

Examples:

- `feat: add MultiObserver for fan-out`
- `fix: race in TestSelfTransitionReEntry counters`
- `feat!: remove deprecated StartWithOptions`
- `docs: document Hydrate's no-OnStateEntered behavior`
- `ci: add baseline GitHub Actions workflow`

The PR title alone is enough — you don't need to repeat the format on every commit in a feature branch, since the squash-merge collapses them. Inside the PR description, a short reviewer-focused summary (what changed, why) is plenty.

## Releases

Release Please watches `main` and keeps an open PR titled "release: vX.Y.Z" whenever there are unreleased commits. Merging that PR creates the git tag and the GitHub release with an auto-generated changelog. There's no manual tagging step.
