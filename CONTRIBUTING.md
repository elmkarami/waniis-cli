# Contributing

Thanks for helping. This is the command-line tool for [Waniis](https://waniis.com).

## Set up

You need Go 1.24 or newer. There is no CGO, so it builds anywhere Go does.

```sh
make check    # gofmt, go vet and the tests (what CI runs)
make lint     # golangci-lint, if you have it installed
make build    # ./bin/waniis
```

## Rules

- Tests don't need the network or an account: commands are tested against a fake HTTP server
  (`internal/commands/fake_test.go`) that implements the API contract. If you add a command, add
  tests there, including its `--json` output and its exit codes.
- The code is layered: `internal/api` talks HTTP, `internal/auth` keeps credentials,
  `internal/commands` parses flags and renders, `internal/export` and `internal/output` write
  formats. Keep HTTP out of commands and terminal handling out of the API client.
- Exit codes and `--json` shapes are public. Don't change them without a major version.
- People read "Waniis" in every message the CLI prints.
- Never print or log a token. Use `api.Redact` for anything that could contain one.
- Keep dependencies few and pin exact versions in `go.mod`.

## Pull requests

Keep them small and focused, with a short description of what changed and why.
## Releasing

1. Add the version's section to `CHANGELOG.md` (`## v1.2.3 — YYYY-MM-DD`): what people will
   notice, grouped under Added, Changed and Fixed. It becomes the release notes on GitHub, and
   the Release workflow refuses a version without one.
2. Run the **Release** workflow from the Actions tab with the version (`v1.2.3`). It tags the
   commit, builds every platform, signs, publishes the release and updates the Homebrew tap.

By contributing you agree your work is released under the Apache License 2.0.
