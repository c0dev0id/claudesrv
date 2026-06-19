# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Goal

Stream a live Claude coding session to viewers in real time.

- **Client** — tails a Claude Code JSONL session file and POSTs parsed events to the server.
- **Server** — receives events, holds them in memory, and fans them out to browser viewers via SSE.

The display focuses on: user messages, assistant text responses, and brief tool-use summaries.

## Build

```
go build ./cmd/server   # HTTP server binary
go build ./cmd/client   # JSONL watcher binary
go build ./...          # both
```

## Run

```
./server                         # listens on :8080
./server -addr :9000             # custom port

./client -server http://host:8080          # auto-detects latest session file
./client -session /path/to/session.jsonl   # explicit session file
```

## Architecture

Single Go module (`github.com/c0dev0id/claude-live-coding`), two binaries:

```
cmd/client/main.go          JSONL file watcher + parser + HTTP poster
cmd/server/main.go          HTTP server (ingest + SSE + embedded HTML)
cmd/server/static/index.html  Viewer page (SSE + marked.js)
internal/event/types.go     Shared Event struct (Kind: user|assistant|tool)
```

### JSONL parsing (client)

Claude Code writes one JSON object per line. Key types:
- `type: "user"` — real user input arrives as `message.content: "string"` (not an array). Array content means tool results or meta; skip those.
- `type: "assistant"` — content is always `[]block`; emit `text` blocks as `KindAssistant`, `tool_use` blocks as `KindTool`; skip `thinking` blocks.
- All other types (attachment, file-history-snapshot, etc.) are skipped.
- Lines with `isMeta: true` are always skipped.

### Server fan-out

`hub` holds `[]Event` history and a set of subscriber channels. New SSE connections receive the full history replay first, then live events. The ingest `POST /ingest` endpoint accepts one `Event` per request.

`DELETE /ingest` resets the in-memory session (start of new session).
