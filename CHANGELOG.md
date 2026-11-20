# Changelog

What changed in each release of `waniis`. The newest is first. A release's notes on GitHub are
its section here, so every version needs one before it can be released (the Release workflow
checks).

## v0.1.2 — 2026-11-20

### Added

- **Model picker.** `/model` (or `/models`) in the chat opens a picker under the input line,
  with tabs: **All**, **Configured**, **Not configured** and **Search**. ←/→ or Tab change the
  tab, ↑/↓ move, typing searches and Enter chooses. Models without a key are listed too,
  greyed out with the reason (for example "no OpenAI key"), and choosing one says where to add
  it.
- `/model <name>` takes a model's full id (`anthropic/claude-sonnet-5-5`), its id without the
  provider, its name, or any words that match only one model. `/model default` goes back to
  the workspace's model.
- **`waniis models`** lists the workspace's models: usable ones first, the default marked, and
  why each of the others can't be used. Takes `--configured`, `--not-configured`,
  `--search <words>` and `--json`.
- **Markdown in the terminal.** Answers are styled as they stream: headings, **bold**,
  *italic*, ~~strikethrough~~, `inline code`, links (the address shown after the text),
  bullet and numbered lists, task boxes, quotes and rules. Table cells are styled the same way.
- **Syntax highlighting** in code blocks, for every language the block names (Go, TypeScript,
  Python, Rust, SQL, shell, YAML, diffs and hundreds more). It uses your terminal's own colors,
  so it follows a light or dark theme. Comments and strings that span lines stay colored.

### Changed

- Without colors (`--no-color`, `NO_COLOR`, or output piped to a file), answers are printed
  exactly as written, Markdown included.

## v0.1.1 — 2026-11-20

### Changed

- The chat is live: people show by name (never an id), each message prints once, and you can
  keep typing while Waniis works.
- A status line shows what Waniis is doing and for how long.

### Fixed

- A race between typing and the screen being redrawn while an answer streams.

## v0.1.0 — 2026-11-20

The first release.

- `waniis login` (in the browser, by device code) and `waniis logout`, with tokens kept in the
  system keychain.
- Sessions: list, search, show, start (`waniis new "<prompt>"`), stop, share and open, with
  `--json` on every command, and `waniis export sessions` to CSV or Excel.
- Watching and steering: `waniis watch`, `waniis logs` (`--tail`, `--since`, `--follow`),
  `prompt`, `steer` and `wait`.
- `waniis chat`: the interactive mode, with a slash-command menu.
- Charts and tables drawn in the terminal from an answer's data.
- `waniis mcp`: an MCP server over stdio, so other AI agents can use Waniis.
- Builds for 21 platforms (macOS, Linux, Windows, the BSDs; amd64, arm64, 386, armv6/v7 and
  more), signed with cosign, with an SBOM, and a Homebrew tap:
  `brew install elmkarami/tap/waniis`.
