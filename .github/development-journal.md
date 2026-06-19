# Development Journal

## Software Stack

- **Language**: Go 1.26 (single module, two binaries)
- **Dependencies**: `github.com/fsnotify/fsnotify` (kqueue/inotify file watching)
- **Viewer**: Plain HTML + CSS + marked.js (CDN) via `//go:embed`
- **Protocol**: HTTP POST per event (client→server), SSE (server→browser)

## Key Decisions

### Client reads JSONL, not hooks
Chose an external JSONL file watcher over Claude Code hooks. The file-tail approach is fully decoupled from Claude Code's hook system — no settings.json changes needed, works with any session automatically.

### SSE over WebSocket for browser fan-out
SSE is unidirectional (server→browser), which is all we need. It handles reconnection natively via the browser's `EventSource` API and requires no JS library. Simpler than WebSocket.

### One event per POST
Client sends one HTTP POST per parsed event rather than a long-lived streaming POST. This keeps both sides stateless and makes retry logic trivial.

### JSONL format discovery
Real user-typed text arrives as `message.content: "string"` (not a content block array). This is counter to the Anthropic API docs format and required careful inspection of real session files. Array content in user messages always means tool results or system injections — skip those.

### History replay via in-memory slice
Server holds `[]Event` in memory. New SSE subscribers receive the full history before live events. No database needed; state resets on server restart (acceptable for a live-session tool).

### static files embedded in server binary
`//go:embed static/index.html` keeps deployment a single binary copy. The file must live under `cmd/server/static/` since Go embed paths cannot use `../`.

## Core Features

- Auto-detect latest Claude Code session file from `~/.claude/projects/`
- Parse and filter JSONL: user text, assistant text, tool-use summaries
- SSE fan-out with full history replay for late-joining viewers
- Browser auto-reconnect on disconnect
- Markdown rendering of assistant responses
- `DELETE /ingest` to reset session state between sessions
