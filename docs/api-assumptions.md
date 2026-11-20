# API contract and assumptions

What the CLI sends and reads, endpoint by endpoint, and where the spec (CLT-15, DEC-152) was
silent so a convention was chosen. **Rows marked "assumed" are the ones to reconcile with the
server.** The fake server in `internal/commands/fake_test.go` implements exactly this contract.

Bearer tokens: `Authorization: Bearer waniis_pat_…` (personal) or `waniis_cli_…` (device grant).
Every `/v1` request also sends `x-rafiiq-workspace` when `--workspace` / `WANIIS_WORKSPACE` is set,
and `User-Agent: waniis-cli/<version> (<os>; <arch>)`. Requests and responses are JSON.

## Endpoints

| Call | Used by | Sends | Relies on |
|---|---|---|---|
| `POST /auth/device/code` (no auth) | `login` | JSON `{client_id: "waniis-cli", device_name, scope}` (`scope` is space separated, default `read sessions`) | RFC 8628 answer: `device_code`, `user_code`, `verification_uri`, `verification_uri_complete`, `expires_in`, `interval` |
| `POST /auth/device/token` (no auth) | `login` (poll) | JSON `{grant_type: "urn:ietf:params:oauth:grant-type:device_code", device_code, client_id}` | 200 `{access_token, refresh_token, token_type, expires_in, scope}`; or 400 `{error}` with `authorization_pending`, `slow_down` (+5 s), `access_denied`, `expired_token` |
| `POST /auth/device/token` (no auth) | token refresh | JSON `{grant_type: "refresh_token", refresh_token, client_id}` | Same 200 body, with a **rotated** refresh token (the CLI stores both). Any error means "sign in again" |
| `POST /auth/device/revoke` (no auth) | `logout` (browser sign-ins only) | JSON `{token: <refresh token>, client_id}` | Any 2xx. Best effort: a failure doesn't stop logout |
| `GET /v1/me` | `whoami`, login check | | `id`, `login`, `name`, `workspaces[{id, name, kind}]` (as today) |
| `GET /v1/models` | `models`, chat `/model` | | **Confirmed** (`apps/api/src/models/view.ts`). `{providers: [{id, title, managed, key: {last4} \| null}], models: [{ref, provider, title, tier, source, available}], routes: {agent: {model, fallbacks}}, canManage}`. A model is usable when `available`; otherwise the CLI says "no <provider> key" when its provider has no key and isn't managed, or "not allowed in this workspace". The default is `routes.agent.model`. Needs the `read` scope |
| `GET /v1/sessions` | `sessions list`, `export sessions` | Query: `filter` (mine, shared, archived, all), `status` (repeatable), `repo` (repeatable), `author` (`me` or a login), `stage` (chat, working), `since`, `until`, `hasPr=true`, `limit` (1 to 100), `cursor` | `{items: [...], next: "<cursor>" \| null}` (G4). The CLI also reads today's `{sessions: [...]}` and `nextCursor` |
| `GET /v1/sessions/search` | `sessions search` | The list query plus `q` (the text) and `interpret=false` for `--no-interpret` | Same page as the list, plus `interpreted: {repos, people, status, stage, since, until, prState, text}` (G5). All its fields are optional |
| `GET /v1/sessions/:id` | `sessions show`, after `start --wait` | | The session summary (see below) |
| `POST /v1/sessions` | `sessions start` | `Idempotency-Key: <uuid>` and JSON `{title?, visibility?, teamId?, repos?: [{cloneUrl, branch?}], stage?: "chat", prompt: {type: "prompt.submit", text, mode?, model?, source: "cli"}}` | 201 `{id}`. Repeating the same key must return the same session and not create another (G6) |
| `GET /v1/sessions/:id/events` | `logs`, `wait`, `--until`, `chat`, `start --wait` (polls) | One of `?tail=N` (1 to 1000), `?since=S&limit=N` or `?before=S&limit=N` (limit 1 to 1000, default 500) | **Confirmed.** `{events, lastSeq, hasMore}`: `lastSeq` is the newest seq of the whole log, `hasMore` means older events remain for `tail` and `before`, newer for `since`. Pages go forward with the last event's seq as the next `since`. `tail` can't combine with the others (400 `invalid_query`). Private items keep their seq with `private: {hidden: true}`. The CLI also copes with an older answer of only `{events}`: it keeps the last N client side for `--tail`, and stops after one page. A `--tail` above 1000 reads the whole log and cuts it |
| `GET /v1/sessions/:id/stream?since=<seq>` (WebSocket) | `logs -f`, `watch`, `--follow`, `chat` | `Authorization: Bearer` on the upgrade, `x-rafiiq-workspace`, and `client=cli` | **Confirmed** (bearer, `since` resume, 426 without an upgrade, 401 without auth; read-only tokens join as viewers). Frames with `seq`, `type` and `payload` are events; others (presence, metrics) are skipped. The CLI drops events at or below the last seq (the replay after a reconnect), reconnects with backoff (0.5 s doubling to 15 s), refreshes the token once on a 401 upgrade, and polls `/events` after 3 failed connections in a row. 403 and 404 on the upgrade end the follow |
| `GET /v1/sessions/:id/status` | `sessions status`, `steer` (is a run going?), MCP `session_status` | | **Confirmed.** `{stage: chat\|working, activity: running\|idle, run: {id, promptId, mode, stage: starting\|working\|verifying\|publishing, startedAt} \| null, queued, lastRun: {id, outcome, summary, at} \| null, pullRequest: {number, url, repo?, draft} \| null, sandbox: {status, detail?} \| null, lastSeq, at}`. A 404 from this route falls back to `GET /v1/sessions/:id` (a real 404 stops there) plus the latest 300 events folded into the same shape |
| `POST /v1/sessions/:id/commands` | `sessions stop` (`run.stop`), `prompt` (`prompt.submit`), `steer` (`run.steer`), `chat` (`tool.approval.decide`) | `Idempotency-Key` (reused across retries) and JSON: `{type: "prompt.submit", text, mode?, model?, source: "cli"}`, `{type: "run.steer", text}`, `{type: "run.stop"}`, `{type: "tool.approval.decide", requestId, decision: once\|session\|deny, source: "cli"}` | **Confirmed.** 200 `{events, promptId?, runId?}`: `promptId` from `prompt.queued`, `runId` from `prompt.started` (absent when the run starts asynchronously) or `run.steered`. The queue position is read from the `prompt.queued` event. The CLI follows its prompt's run through the stream (`prompt.started` links the prompt to a run). A read-only token gets 403 `insufficient_scope` (exit 3). The server has no dedicated "no active run" code, so `steer` checks `/status` first and, if that says idle, tells the person to use `prompt` without sending anything; any other 4xx about the steer becomes the same advice |
| `POST /v1/sessions/:id/network/access` | `chat` | `{kind: "decide", requestId, decision: approve\|deny, scope: "session"}` or `{kind: "allow", host, scope: "session", access: "read", retry: true}` | As in `session-network-access.ts`: who may decide is the server's call. A 403 is shown as "you can't decide that here" and the chat stops asking about blocked hosts |
| `GET /v1/sessions/:id/surfaces`, `GET /:id/surfaces/:slug/open` | `chat /preview` | slug `p<port>` | The 302's `Location` (a one-time, 60-second ticket link) is printed, never followed |
| `GET /v1/sessions/:id/diff` | `chat /diff` | | Files, additions, deletions and PR per repo (patches are ignored), plus the gaps the server reports |
| `PUT /v1/sessions/:id/share-link` | `share --link` | JSON `{role: "viewer" \| "collaborator"}` | `{link: {role, url, createdAt}}` |
| `DELETE /v1/sessions/:id/share-link` | `share --revoke-link` | | 2xx |
| `GET /v1/sessions/:id/people` | `share --remove` (login to id) | | `{people: [{id, login, name, role}]}` |
| `POST /v1/sessions/:id/people` | `share --invite` | `Idempotency-Key` and JSON `{login, role}` | 201 `{person}` |
| `DELETE /v1/sessions/:id/people/:userId` | `share --remove` | | 2xx |
| `PUT /v1/sessions/:id/visibility` | `share --visibility` | JSON `{visibility, teamId?}` | 2xx |

`sessions open` calls no endpoint: it opens `<app url>/s/<id>` (the API alias `api.waniis.com` maps
to `app.waniis.com`).

### Session summary fields the CLI reads

`id`, `title`, `status` (running, idle, sleeping, failed), `stage`, `repos[{fullName, cloneUrl,
branch}]`, `pullRequest {number, url} | null`, `createdAt`, `lastActiveAt` (ISO 8601), `visibility`,
`owner {id, login, name}`. Other fields are passed through untouched in `--json` output.

### Errors

The body is `{error: "<code>", message?, reason?, hint?, issues?}` (a nested `{error: {code,
message}}` is read too). The `x-request-id` response header is shown and put in the JSON error.

| Condition | Exit code |
|---|---|
| 401, or codes `unauthorized`, `token_expired`, `token_revoked`, `insufficient_scope`, `missing_scope` | 3, "run `waniis login`" |
| 403 `access_pending` (DEC-157) | 3, "Your access request hasn't been approved yet." |
| 402, or code `billing_blocked` | 6 |
| 403, 404, 410 | 4 |
| 409, 412 | 5 |
| 429, 502, 503, 504, network failure (after 3 retries) | 7 |
| other 4xx/5xx | 1 |

`reason_required` (403) is shown with its message and mapped to exit 4. Retries: reads and
PUT/DELETE, and any write carrying an `Idempotency-Key`, are retried on network errors, 429 and
5xx (not 501) with backoff 0.5 s, 1 s, 2 s, or the `Retry-After` header (up to 60 s). The key is
generated once per command and reused for every attempt.

## Where the spec was silent (assumed)

1. **Device flow encoding.** The two `/auth/device/*` calls take JSON, not
   `application/x-www-form-urlencoded` as RFC 8628 says. The server's routes parse JSON today. If
   the server accepts only form encoding, change `auth/device.go`.
2. **Grant type string** is the RFC's `urn:ietf:params:oauth:grant-type:device_code`.
3. **Refresh** reuses `POST /auth/device/token` with `grant_type=refresh_token` (RFC 6749). The CLI
   refreshes when the access token is within 60 s of `expires_in`, and once after a 401.
4. **Revoke** is `POST /auth/device/revoke` (RFC 7009 style). The spec says logout revokes the
   grant server-side but names no endpoint. Personal tokens are not revoked by `logout` (they are
   managed in Settings); only the local copy is deleted.
5. **Scopes** are sent as `scope: "read sessions"` (space separated, OAuth style) while the spec
   writes them comma separated for personal tokens.
6. **Keychain account** is the API base URL (`https://app.waniis.com`), under the service `waniis`.
   The spec says `<host>:<user id>`, but at lookup time the user id isn't known; the user id and
   login are stored inside the entry.
7. **File fallback is automatic.** The spec (FR-5) wants `--insecure-storage` before using a file;
   the task for this phase asked for a 0600 file fallback, so the CLI falls back when the keychain
   errors and says so on stderr.
8. **List response key.** `items`/`next` per the spec; `sessions`/`nextCursor` are read too so the
   CLI works against today's API. `--json` always writes `{items, next}`.
9. **Filter query names** are camelCase like the API's fields: `hasPr` (not `has_pr`), plus `repo`
   and `status` repeated. `since`/`until` are passed through as typed (ISO 8601).
10. **Search** is `GET /v1/sessions/search?q=` and `interpret=false`; the spec names the path but
    not the parameters.
11. **First prompt in `POST /v1/sessions`** (G6) is a `prompt` object that is a protocol
    `prompt.submit` command with `source: "cli"`, so the server can validate it with the same
    schema.
12. **`start --wait`** detects the end of the first run with `run.completed` events (polling `events`,
    not the WebSocket stream). `needs_input` counts as a finished run with exit 0.
13. **Repository clone URL** is built as `https://github.com/<owner>/<name>.git`, which is the
    form the API's `cloneUrl` takes. Only GitHub repos are recognized.
14. **`stop`** needs no confirmation (ids are explicit). The spec's filter form
    (`--status running --yes`) is not built in phase A; ids can come from `sessions list` through
    stdin (`-`).
15. **`share --remove <login>`** looks the login up in `GET /:id/people`, because the API removes
    by user id.
16. **`export sessions`** has the columns `id, title, status, stage, repos, owner,
    pullRequestNumber, pullRequestUrl, visibility, createdAt, lastActiveAt, url`.
17. **Config file**: none in phase A. Flags and environment only (no `config.toml`, no profiles).
18. **Telemetry, version headers (G10)** are not implemented.

## Watching, steering, the chat and MCP (added with `logs`, `prompt`, `steer`, `wait`, `chat`, `mcp`)

Events are read from the protocol's frames (FND-05): `{v, seq, at, actor, private?, type, payload}`.
Types the CLI renders: `session.*`, `prompt.*`, `chat.message`, `sandbox.status`,
`agent.message(.delta)`, `agent.retrying`, `model.fallback`, `tool.call`/`tool.result`,
`file.changed`, `run.*`, `changes.*`, `pr.opened`/`pr.updated`/`pr.follow_up`, `verification.*`,
`review.*`, `plan.*`, `child.*`, `error`, `mcp.*`, `sandbox.egress_blocked`, `network.*`,
`environment.*`, `guardrails.escalated`, `engineer_review.*`. Unknown types are ignored, as the
protocol asks; `pr.status` and chat reactions and edits are left out as noise. A run "starts" at
`prompt.started` and ends at `run.completed` (`success`, `failed`, `stopped`, `needs_input`).

19. **`--until` and `wait`** are decided on the log folded from the whole session (read in pages of
    500), then on live events. `run-end`: when a run is going or a prompt is queued, the next
    `run.completed`; with nothing going, at once with the last outcome. `idle`: nothing running and
    nothing queued. `pr-open`: a `pr.opened` was seen (earlier or later); if the session goes idle
    first, exit 10 for a failed run, else exit 1 (`no_pull_request`). `prompt --wait` ends with that
    prompt's own run. Outcomes `failed` and `stopped` exit 10; `success` and `needs_input` exit 0.
20. **Names in lines** (`› prompt (maya)`) come from `GET /:id/people` (`id` to `login`), best
    effort: without it the user id is printed.
21. **Chart blocks** follow `packages/protocol/src/answer-chart.ts` (kinds `bar`, `stacked_bar`,
    `line`, `area`; the same limits and validation, mirrored in `internal/chart`). The table matches
    the Slack bot's (a blank first header, then the series; values formatted en-US like
    `formatChartValue`); the currency code or the plain unit follows the title. A block that doesn't
    validate is printed as the code block it was.
22. **MCP** uses `github.com/modelcontextprotocol/go-sdk` v1.3.1 (the newest that builds with
    Go 1.24; v1.4 and later need 1.25). Write tools are on by default, including `stop_session`;
    `--read-only` removes them. There is no publish, delete or admin tool (spec CLT-15/FR-22; open question CLI6).
23. **Chat attachments** are not offered: attaching needs the declare-and-upload flow
    (`POST /:id/attachments`), which the CLI doesn't have yet. `/login` inside the chat is also
    not built; `/logout` signs out and leaves.

## Who is who in chat and logs

The CLI never prints a user, session, run or prompt id in human output (they stay in `--json`).
The signed-in person (`GET /v1/me`) reads "you"; others read as the `name` from
`GET /v1/sessions/:id/people`, else their `login`, else "someone". The route answers with the id as
`name` (and a null `login`) when it knows nothing better (`sessions.ts`); the CLI treats a name
that is an id as no name. The people are read once when a chat or log is opened and again when an
event mentions someone not known yet (the same person at most every 30 seconds). Your own
messages in the chat are matched to their echoes (`chat.message`, `prompt.queued`) by the
`promptId` of the command's reply, or by author, text and recency (two minutes), and not shown
again; a `prompt.queued` that carries `continues` (a conversation handed to a sandbox) is the same
message and is not shown again either.
