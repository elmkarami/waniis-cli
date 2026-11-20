# API contract and assumptions

What the CLI sends and reads, endpoint by endpoint, and where the spec (CLT-15, DEC-152) was
silent so a convention was chosen. **Rows marked "assumed" are the ones to reconcile with the
server.** The fake server in `internal/commands/fake_test.go` implements exactly this contract.

Bearer tokens: `Authorization: Bearer rafiiq_pat_…` (personal) or `rafiiq_cli_…` (device grant).
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
| `GET /v1/sessions` | `sessions list`, `export sessions` | Query: `filter` (mine, shared, archived, all), `status` (repeatable), `repo` (repeatable), `author` (`me` or a login), `stage` (chat, working), `since`, `until`, `hasPr=true`, `limit` (1 to 100), `cursor` | `{items: [...], next: "<cursor>" \| null}` (G4). The CLI also reads today's `{sessions: [...]}` and `nextCursor` |
| `GET /v1/sessions/search` | `sessions search` | The list query plus `q` (the text) and `interpret=false` for `--no-interpret` | Same page as the list, plus `interpreted: {repos, people, status, stage, since, until, prState, text}` (G5). All its fields are optional |
| `GET /v1/sessions/:id` | `sessions show`, after `start --wait` | | The session summary (see below) |
| `POST /v1/sessions` | `sessions start` | `Idempotency-Key: <uuid>` and JSON `{title?, visibility?, teamId?, repos?: [{cloneUrl, branch?}], stage?: "chat", prompt: {type: "prompt.submit", text, mode?, model?, source: "cli"}}` | 201 `{id}`. Repeating the same key must return the same session and not create another (G6) |
| `GET /v1/sessions/:id/events?since=<seq>` | `start --wait` (polls every 3 s) | | `{events: [{seq, type, payload}]}`; the CLI waits for `type: "run.completed"` with `payload.outcome` in `success`, `failed`, `stopped`, `needs_input` and `payload.summary` |
| `POST /v1/sessions/:id/commands` | `sessions stop` | `Idempotency-Key` and JSON `{type: "run.stop"}` | 2xx `{events}` |
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
12. **`--wait`** detects the end of the first run with `run.completed` events (polling `events`,
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
