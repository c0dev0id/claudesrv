# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added
- `cmd/server`: HTTP server with `POST /ingest` for event ingestion, `GET /events` SSE endpoint for browser fan-out, and `GET /` serving an embedded viewer page.
- `cmd/client`: JSONL file watcher that tails Claude Code session files, parses user/assistant/tool events, and POSTs them to the server. Auto-detects the most recently modified session file.
- `internal/event`: Shared `Event` struct with `Kind` (user | assistant | tool), `Text`, `ToolName`, and timestamp.
- Viewer page with SSE auto-reconnect, markdown rendering via marked.js, live status indicator, and full history replay on connect.
