# waniis

`waniis` is the command-line tool for [Waniis](https://waniis.com) (ونيس), the cloud coding agent.
Start, list, search, stop and share sessions from a terminal, and export them to CSV or Excel.
It is built as much for scripts and AI agents as for people: every command has `--json`, exit
codes are stable, and nothing asks a question when there is no terminal.

It talks to the Waniis API as you, with a token from a browser sign-in or a personal API token.
Nothing runs on your machine: sessions run in Waniis's sandboxes.

> Sign-in, sessions, exports, and now watching, prompting and steering a session, an interactive
> chat and an MCP server for AI agents. See [Status](#status) for what is planned next.

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
| `waniis sessions logs <id>` | What a session did: last 100 events, `--all`, `--follow` (alias `waniis logs`) |
| `waniis watch <id>` | Follow a session live (`logs --follow`) |
| `waniis sessions status <id>` | Running or idle, current run, queue, last outcome, PR, sandbox |
| `waniis sessions prompt <id> "<text>"` | Append a prompt (queued if a run is going) |
| `waniis sessions steer <id> "<text>"` | Steer the run that is going |
| `waniis sessions wait <id>` | Block until a run ends, the session is idle or a PR is open |
| `waniis sessions stop <id>...` | Stop the running work of one or more sessions |
| `waniis sessions search "<text>"` | Search in plain language |
| `waniis sessions share <id> ...` | Share links, people, visibility |
| `waniis sessions open <id>` | Open a session in the browser |
| `waniis export sessions` | Export sessions to CSV or Excel |
| `waniis` / `waniis chat [id]` | Interactive chat in the terminal |
| `waniis mcp` | MCP server over stdio for AI agents |
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

## Watching a session

Fire and forget, then look whenever you like. `logs` prints a session as readable lines:

```sh
waniis logs ses_01abc                  # the last 100 events (--tail 60 for fewer)
waniis logs ses_01abc --all            # the whole session
waniis logs ses_01abc --since 412      # only what came after event 412
waniis logs ses_01abc -f               # keep streaming; Ctrl-C stops watching, the session goes on
waniis watch ses_01abc                 # the same as logs --follow
waniis logs ses_01abc -f --until run-end    # stream until the run ends
waniis sessions status ses_01abc       # one call: is it running, what stage, what's queued
```

```
› prompt (maya): fix the flaky checkout test
▶ run started (build)
Waniis: Looking at the retry logic in src/checkout.ts.
⚙ tool github.get_file src/checkout.ts ✓
⛔ blocked ramp.com (GET) — not allowed
⏸ needs approval: network access to ramp.com (read) — docs; request n_1
✓ checks: tests 24 passed, lint, typecheck
PR acme/api#318 opened https://github.com/acme/api/pull/318
■ run completed: Fixed the race in the retry loop ($0.42)
```

Streamed answers are joined into one line, tool calls are paired with their results, and a personal
result you can't see is shown as a placeholder. Colors appear on a terminal unless `--no-color` or
`NO_COLOR` is set. `--timestamps` adds each event's time.

`--until` ends a follow when a run ends (`run-end`), nothing is running or queued (`idle`) or a PR
is open (`pr-open`), and implies `--follow`. The exit code is 0, 10 if the run failed or was
stopped, 8 on `--timeout`, 130 on Ctrl-C. Following uses the session's WebSocket with your token,
resumes from the last event after a drop (with backoff), and falls back to polling when WebSockets
are blocked.

**For scripts and agents,** `--json` prints every event exactly as the API sends it, one JSON
object per line (NDJSON): `waniis logs ses_01abc --all --json | jq 'select(.type=="pr.opened")'`.

**Charts in answers.** When Waniis answers with a chart, the CLI always prints a table (series as
columns, numbers formatted like the web app, the source line below). On a terminal wide enough
(50 columns) it also draws the chart: horizontal bars with eighth-block precision, a legend and one
color per series, or a small line plot. `--charts auto|always|never` controls the drawing (`auto`
is terminals only); `--json` leaves the chart block as the API sent it. Markdown tables in answers
become aligned tables too.

## Appending, steering and waiting

```sh
waniis sessions prompt ses_01abc "also add a test for the empty cart"   # queued if a run is going
echo "rebase on main" | waniis sessions prompt ses_01abc -              # text from stdin
waniis sessions prompt ses_01abc "explain the retry logic" --mode ask --follow
waniis sessions steer ses_01abc "don't touch the migration files"       # redirects the run that is going
waniis sessions wait ses_01abc --timeout 20m --json                     # block; the exit code says how it ended
waniis new "fix the flaky checkout test" --follow                      # start and stream the first run
```

`prompt` queues behind a running run and starts one when the session is idle. `steer` needs a run
going and says so (with a hint to use `prompt`) when there is none. `--wait` and `--follow` end
with this prompt's run, with exit 0 or 10. Both writes carry an `Idempotency-Key`, so a retry after
a network error never sends twice; pass `--idempotency-key` to choose it.

## Interactive mode

```sh
waniis                # in a terminal: a new chat; the first line starts a session from this repo
waniis chat ses_01abc # follow and talk to an existing session
waniis chat --chat    # a conversation with no repo
```

It is a line-based chat, not a full-screen program: events stream above a prompt line using the
same rendering as `waniis logs`. **Enter** sends a prompt; if a run is going it is queued behind it
(a dim line says so). **`/steer <text>`** redirects the run that is going instead. The prompt shows
the current mode and model (`build · claude-x ›`).

Type **`/`** to open the command menu: it filters as you type, **Up/Down** pick, **Tab** completes,
**Enter** runs the highlighted command, **Esc** closes it. `/help` lists everything.

| Command | |
|---|---|
| `/new [prompt]` · `/sessions` · `/switch <id>` | Start a session, pick a recent one by number, follow another |
| `/status` · `/stop` · `/steer <text>` · `/queue` · `/logs [N]` | Look at and control the session |
| `/diff` · `/pr` · `/preview` · `/open` · `/share [role]` · `/export [csv\|xlsx]` | Changes, pull request, preview links (one-time, 60 seconds), browser, share link, a file with the session's summary row |
| `/mode auto\|build\|plan\|ask` · `/model <id>` | How the next prompts run |
| `/whoami` · `/workspace [name]` · `/login` · `/logout` | Account. `/login` and `/attach` say they aren't available yet |
| `/clear` · `/quit` (`/exit`) | Clear the screen, leave |

When the stream shows something waiting for you, the chat asks inline: `Allow ramp.com (read) for
this session? [y/N]` for the agent's network requests, `[y] once, [s] session, [N] deny` for tool
approvals, and the same for a blocked host. A decision your role doesn't allow is reported, and the
chat stops asking about it.

**Ctrl-C** once stops following (the session keeps running in Waniis); twice, or `/quit`, leaves.
Ctrl-D leaves. On leaving it prints `The session keeps running in Waniis; reopen with waniis chat
<id>`. If stdin isn't a terminal the lines are read one by one with no menu, and at the end of
input the chat waits for the runs it started to finish: `echo "why is CI red?" | waniis chat --chat`.
On Windows use Windows Terminal or another console with ANSI support.

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
| 8 | `--wait`, `wait` or `--follow --timeout` timed out |
| 9 | A bulk action partly failed |
| 10 | The awaited run ended failed or was stopped (`--wait`, `--follow`, `wait`, `--until`) |
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
| `--charts <mode>` | | Draw charts in answers: `auto` (terminals), `always`, `never`; a table is always shown |
| `--verbose` | | Log requests to stderr, with tokens redacted |
| `--timeout <d>` | | Per-request timeout (default 30s) |
| | `WANIIS_TOKEN` | A personal API token; overrides any stored credential |
| | `WANIIS_CONFIG_DIR` | Where the credentials file goes if there is no keychain |
| | `CI` | Treat the session as non-interactive |

Failed requests are retried with backoff (up to 3 times) when they are safe to repeat: reads, and
writes that carry an idempotency key. `Retry-After` is honored.

## Status

Built: sign-in, sessions (list, search, start, stop, share, open), exports, watching (`logs`,
`watch`, `status`), appending and steering (`prompt`, `steer`, `wait`), the interactive chat and
`waniis mcp`. Not built yet: switching workspaces outside the chat, `diff`, PRs and previews as
commands, attachments, and the other areas and export types.

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
