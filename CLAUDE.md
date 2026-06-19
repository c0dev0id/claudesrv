# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Goal

Stream a live Claude coding session to viewers in real time.

- **Client** — captures an in-progress Claude session and POSTs/streams the data to the server endpoint.
- **Server** — receives the stream and serves it as a public, auto-updating live session focused on the user↔AI conversation.

## Architecture Intention

Two components, clearly separated:

1. `client/` — reads Claude session output (stdin pipe, PTY, or Claude Code hooks) and forwards it to the server.
2. `server/` — ingests the stream, persists the current session state, and pushes updates to connected browsers (SSE or WebSocket).

The display focus is the conversation (user messages and AI responses), not terminal chrome or tool call internals.
