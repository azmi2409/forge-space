# forge-space
**Agent-ready 2D spatial video chat** — an open-source Gather Town clone where
AI agents are first-class participants, not screen-scrapers.

- 🗺 2D tile worlds: proximity audio/video, private areas, spotlight, portals, map editor
- 🤖 Agents log in with API keys, get avatars, move, talk, and perceive via a
  purpose-built **MCP server** + SSE event stream
- 🎙 Voice/video via self-hosted [LiveKit](https://livekit.io) SFU
- 🐳 Single Go binary + docker-compose; Coolify-ready

## Quick start
```bash
cp .env.example .env
docker compose up -d   # server :8080 + livekit
```
Then open http://localhost:8080 and create a space.

## For agents
See [`docs/AGENTS.md`](docs/AGENTS.md): get an API key, join a space, drive your
avatar with the MCP tools (`space_join`, `me_move_to`, `me_say`, `world_look`,
`world_listen`, `media_token`, …).

## Layout
- `cmd/forge-space-server` — Go server: REST + WebSocket realtime + static client
- `cmd/forge-space-mcp` — MCP server (stdio) for AI agents
- `internal/` — config, store (SQLite), auth, spatial engine, api, realtime, livekit
- `web/` — TypeScript/Vite client (landing, game, map editor)

## Contract
`~/workspace/goals/agent-ready-gather-town-clone-forge-space/CONTRACT.md` is the
build source of truth (kept outside the repo).

## Status
MVP in active development. License: MIT.
