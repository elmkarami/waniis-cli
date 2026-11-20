# waniis

`waniis` is the command-line tool for [Waniis](https://waniis.com) (ونيس), the cloud coding agent.
Start, list, search, stop and share sessions from a terminal, and export them to CSV or Excel.
It is built as much for scripts and AI agents as for people: every command has `--json`, exit
codes are stable, and nothing asks a question when there is no terminal.

It talks to the Waniis API as you, with a token from a browser sign-in or a personal API token.
Nothing runs on your machine: sessions run in Waniis's sandboxes.

> Phase A. This release covers sign-in, sessions and exports. See [Status](#status) for what is
> planned next.

## Install

Homebrew (once the tap is published):

```sh
brew install elmkarami/tap/waniis
```

From a release: download the archive for your system from the
[releases page](https://github.com/elmkarami/waniis-cli/releases), check it against
`checksums.txt`, and put `waniis` on your `PATH`. Releases cover macOS, Linux and Windows on
amd64 and arm64.

With Go 1.24 or newer:

```sh
go install github.com/elmkarami/waniis-cli/cmd/waniis@latest
```

Shell completion: `waniis completion bash|zsh|fish|powershell`, for example
`waniis completion zsh > "${fpath[1]}/_waniis"`.

## Sign in

```sh
waniis login
```

The CLI prints a short code and a URL, opens your browser when it can, and waits until you approve
the sign-in at `https://app.waniis.com/device`. Credentials are kept in your OS keychain (macOS
Keychain, Windows Credential Manager, the Secret Service on Linux). Where there is no keychain, they
go in `~/.config/waniis/credentials`, a file only you can read (mode 0600), and the CLI says so.

For CI and other machines with no browser, use a personal API token (create one in Settings in the
web app):

```sh
export WANIIS_TOKEN=...             # used for every command, never stored
echo "$TOKEN" | waniis login --with-token   # or keep it in the keychain
```

`waniis whoami` shows who you are, the host, the workspace and the kind of token (never the token).
`waniis logout` revokes a browser sign-in and forgets the stored credentials.

## Commands

| Command | What it does |
|---|---|
| `waniis login [--token T \| --with-token] [--no-browser]` | Sign in (also `waniis auth login`) |
| `waniis logout` | Sign out |
| `waniis whoami` | Who you are (also `waniis auth status`) |
| `waniis sessions list` | List sessions (alias `ls`) |
| `waniis sessions show <id>` | One session |
| `waniis sessions start "<prompt>"` | Start a session with a first prompt (alias `new`) |
| `waniis sessions stop <id>...` | Stop the running work of one or more sessions |
| `waniis sessions search "<text>"` | Search in plain language |
| `waniis sessions share <id> ...` | Share links, people, visibility |
| `waniis sessions open <id>` | Open a session in the browser |
| `waniis export sessions` | Export sessions to CSV or Excel |
| `waniis version` | Version |
| `waniis completion <shell>` | Shell completion |

`waniis s` is short for `waniis sessions`. Run any command with `--help` for its flags and examples.

### Examples

```sh
# What is running right now?
waniis sessions list --status running

# Sessions that opened a PR in one repo since a date, every page
waniis sessions list --repo acme/api --has-pr --since 2026-11-01 --all

# Start work, wait for the first run to end, get one JSON object back
waniis sessions start "fix the flaky checkout test" --repo acme/api --wait --json

# In a checkout of acme/api the repo comes from `git remote`; a conversation has no repo
waniis sessions start "why is the nightly import slow?" --chat

# Stop several at once; ids can come from stdin
waniis sessions stop ses_01abc ses_01def
waniis sessions list --status running --json | jq -r '.items[].id' | waniis sessions stop -

# Search in plain language; the CLI says how it understood you on stderr
waniis sessions search "payment bugs fixed last week that have a merged PR"

# Share
waniis sessions share ses_01abc --link viewer
waniis sessions share ses_01abc --invite maya --role collaborator
waniis sessions share ses_01abc --revoke-link

# Export
waniis export sessions --format csv --output sessions.csv
waniis export sessions --format xlsx --since 2026-11-01 --output sessions.xlsx
```

`sessions start` always sends an `Idempotency-Key`, so retrying after a network error or a 5xx never
starts a second session. Pass `--idempotency-key` to choose the key yourself.

## Waniis for AI agents (`waniis mcp`)

`waniis mcp` runs a Model Context Protocol server on stdio with the CLI's stored credentials
(or `WANIIS_TOKEN`), so an agent can hand work to Waniis, watch it and read the result.

```sh
claude mcp add waniis -- waniis mcp                 # Claude Code
codex mcp add waniis -- waniis mcp                  # Codex CLI
```

Cursor: add `{"mcpServers": {"waniis": {"command": "waniis", "args": ["mcp"]}}}` to
`~/.cursor/mcp.json`. Any MCP client that can start a stdio command works the same way. Sign in
once with `waniis login` first; a missing sign-in comes back as a tool error that says so.

| Tool | What it does |
|---|---|
| `list_sessions` | Sessions you can see (status, repo, author, PR filters) |
| `search_sessions` | The same in plain language |
| `start_session` | Start work on a repo (or `chat`), optionally `wait` for the first run |
| `session_status` | Running or idle, current run and stage, queue, last outcome, PR, sandbox |
| `session_logs` | The last N events as readable lines, or events after a `since` seq (`lastSeq` to continue) |
| `prompt_session` | Append a prompt (queued if a run is going), optionally `wait` for its run |
| `steer_session` | Steer the run that is going (fails clearly if nothing runs) |
| `stop_session` | Stop the running work |
| `wait_session` | Block until `run-end`, `idle` or `pr-open`; a timeout is reported as `timedOut`, call again |

`--read-only` offers only the tools that change nothing. A run that failed is a normal result
(`outcome: "failed"`), not a tool error.

## Output

Tables on stdout for people; `--json` for machines. Errors always go to stderr, so stdout holds
data only.

Lists:

```json
{
  "items": [
    { "id": "ses_01abc", "title": "Fix the flaky checkout test", "status": "idle",
      "stage": "working", "repos": [{ "fullName": "acme/api" }],
      "pullRequest": { "number": 412, "url": "https://github.com/acme/api/pull/412" },
      "createdAt": "2026-11-19T10:00:00Z", "lastActiveAt": "2026-11-19T11:20:00Z" }
  ],
  "next": "100"
}
```

Items are the API's own objects, with every field it sends. `next` is the cursor for the next page
(`--cursor`), or `null` on the last page; `--all` follows every page. `sessions search` adds
`interpreted`, how the text was understood.

`sessions start`:

```json
{ "id": "ses_01abc", "url": "https://app.waniis.com/s/ses_01abc",
  "outcome": "success", "summary": "...", "session": { "...": "..." } }
```

`outcome` and `session` are present with `--wait`. `sessions stop` and bulk results:

```json
{ "results": [ { "id": "ses_01abc", "ok": true },
               { "id": "ses_01def", "ok": false, "error": { "code": "not_found", "message": "..." } } ],
  "succeeded": 1, "failed": 1 }
```

Errors, with `--json`, on stderr:

```json
{ "error": { "code": "access_pending", "message": "Your access request hasn't been approved yet.",
             "hint": "...", "status": 403, "requestId": "req_..." } }
```

### Exit codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | Error not covered below |
| 2 | Usage error: bad flags or arguments |
| 3 | Not signed in, token expired or revoked, missing scope, or access not approved yet |
| 4 | Not found or not allowed (the API answers 404 for both, on purpose) |
| 5 | Conflict or precondition |
| 6 | Billing blocked the action |
| 7 | Unavailable or rate limited after retries, or a network failure |
| 8 | `--wait` timed out |
| 9 | A bulk action partly failed |
| 10 | The awaited run ended failed or was stopped |
| 130 | Interrupted (Ctrl-C) |

### Exports

CSV is RFC 4180 in UTF-8 with a header row and ISO 8601 UTC timestamps (`--bom` for Excel on
Windows). Cells that start with `=`, `+`, `-` or `@` are prefixed with `'` so a spreadsheet can't
run them. XLSX has a bold frozen header, an autofilter, real dates and numbers, and links to
sessions and pull requests. XLSX is never written to a terminal: use `--output` or redirect.

## Global flags and environment

| Flag | Environment | Meaning |
|---|---|---|
| `--json` | | Machine-readable output |
| `--workspace <id>` | `WANIIS_WORKSPACE` | Workspace to act in (sent as `x-rafiiq-workspace`) |
| `--api-url <url>` | `WANIIS_API_URL` | Default `https://app.waniis.com`. Must be `https`, except for `localhost` |
| `--no-color` | `NO_COLOR` | No color |
| `--verbose` | | Log requests to stderr, with tokens redacted |
| `--timeout <d>` | | Per-request timeout (default 30s) |
| | `WANIIS_TOKEN` | A personal API token; overrides any stored credential |
| | `WANIIS_CONFIG_DIR` | Where the credentials file goes if there is no keychain |
| | `CI` | Treat the session as non-interactive |

Failed requests are retried with backoff (up to 3 times) when they are safe to repeat: reads, and
writes that carry an idempotency key. `Retry-After` is honored.

## Status

This is phase A of the CLI spec (CLT-15). Not built yet: workspaces switching, `logs --follow`,
`prompt`, `steer`, `diff`, PRs, previews, `waniis mcp`, the other areas and more export types.

## Development

```sh
make check   # gofmt, go vet and tests
make lint    # golangci-lint
make build
```

See [CONTRIBUTING.md](CONTRIBUTING.md) and [SECURITY.md](SECURITY.md). The API contract the CLI
relies on, and where it had to assume, is in [docs/api-assumptions.md](docs/api-assumptions.md).

## License

Apache License 2.0. See [LICENSE](LICENSE).
