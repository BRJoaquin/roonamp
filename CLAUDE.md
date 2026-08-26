# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

# roonamp -- Claude Code Task Specification

## What this is

A terminal TUI music controller for Roon, written in Go using the Charm ecosystem (Bubble Tea, Lip Gloss, Bubbles). It communicates directly with the Roon Core over WebSocket using the native MOO protocol. No Node.js bridge, no HTTP proxy -- pure Go talking to Roon.

## Commands

```bash
go build -o roonamp .          # build the static binary
go run . -host <ip> -port <port>   # build and run against a Roon Core
go vet ./...                   # static analysis
gofmt -l -w .                  # format (CI/standard Go formatting)
```

- Requires Go 1.26+ (see `go.mod`).
- There is no test suite, no Makefile, and no linter config -- `go vet` and `gofmt` are the only checks.
- Running needs a reachable Roon Core. Address resolution order: `-host`/`-port` flags, `ROON_HOST`/`ROON_PORT` env vars, last-good address cached in `~/.config/roonamp/server`, then SOOD UDP discovery. The HTTP port varies per install (commonly 9100/9150/9200/9330).
- The binary writes nothing to stdout once the TUI starts; `log` output is discarded (see "Logging" below) to avoid corrupting the alt-screen.

## Constraints

- NO emojis or unicode icons in the UI. ASCII-only for state indicators: `[>]`, `[=]`, `[x]`, `(*)`, etc.
- Unicode block characters are OK for the animated progress bar (bubbles/progress default).
- NO Node.js dependency. Direct WebSocket to Roon Core.
- Server address via `-host`/`-port` flags or `ROON_HOST`/`ROON_PORT` env vars; with neither set, the cached last-good address is tried, then SOOD auto-discovery.
- Single static binary. Preferences stored in `~/.config/roonamp/`.

## Architecture

```
┌─────────────────┐                            ┌───────────┐
│   roonamp (Go)  │ <──── WebSocket ──────────>│ Roon Core │
│   Bubble Tea    │   ws://{ip}:{port}/api     │           │
└─────────────────┘   MOO protocol messages    └───────────┘
```

### Startup sequence (main.go)

All network setup is **synchronous and blocking, before the TUI launches**: `Connect` -> `GetInfo` -> `Register` -> `SubscribeZones`, each failing fast to stderr. Only after zones are subscribed does `tui.NewModel(client)` run inside `tea.NewProgram(..., WithAltScreen())`. The Roon `Client` is fully connected and registered by the time any Bubble Tea code sees it.

### Concurrency model (the part that spans files)

There are two concurrent worlds and one bridge between them:

1. **MOO/WebSocket goroutine** (`roon/moo.go`): `MooConn.ReadLoop()` runs in a background goroutine started by `Client.Connect`. It parses every incoming frame and dispatches by `Request-Id`: REQUEST/COMPLETE replies go to a per-request reply channel registered in `handlers`; subscription CONTINUE frames are routed to the handler registered via `Subscribe`. `Client.handleZoneUpdate` is that handler for zones -- it mutates the client's zone map under a mutex and then invokes the callback installed via `Client.SetOnZonesUpdated`.

2. **Bubble Tea event loop** (`tui/app.go`): a single `Model` drives both the player and browser views (a `view` field routes `Update`/`View`). State changes only ever happen inside `Update`.

3. **The bridge** (`Model.zoneCh` + `Model.listenForZones`): `NewModel` creates a buffered channel and installs a callback once via `client.SetOnZonesUpdated` that pushes zone snapshots into it (latest-wins: a queued snapshot is replaced rather than the new one dropped). `listenForZones` is a `tea.Cmd` that blocks on that channel and returns the result as a `zonesUpdatedMsg`; each time `Update` handles one it re-issues `listenForZones()` to wait for the next push. This is how asynchronous Roon push updates become serialized Bubble Tea messages -- **do not touch zone state from the callback or any goroutine; convert it to a `Msg` and handle it in `Update`.**

Synchronous request/response calls (`Control`, `ChangeVolume`, `Browse`, `Load`) are issued from within `tea.Cmd` closures (e.g. `controlCmd`, `volumeCmd`) so the blocking WebSocket round-trip happens off the UI loop.

Two local `tea.Tick` loops also feed `Update`: `seekTickCmd` (1s, refreshes the link state and forces a re-render; the displayed seek position is interpolated from a wall-clock anchor, see `updateSeekAnchor`/`effectiveSeekPos`) and `animTickCmd` (60fps, drives the harmonica spring animations for the zone-swipe and volume-pulse effects).

### Logging

`main.go` sets `log.SetOutput(io.Discard)` so stray logging cannot corrupt the alt-screen TUI. A `-debug` flag intended to redirect logs to `~/.config/roonamp/debug.log` is referenced in a comment but **not currently implemented** in `config.go`.

## Project structure

```
roonamp/
├── main.go                        # Entry point: connect, register, launch TUI
├── go.mod
├── CLAUDE.md
├── README.md
├── internal/
│   ├── config/
│   │   └── config.go              # CLI flags, env vars, XDG persistence (token, zone, prefs, zones whitelist)
│   ├── roon/
│   │   ├── sood.go                # SOOD UDP discovery (fallback when no address given/cached)
│   │   ├── moo.go                 # MOO/1 message framing over WebSocket
│   │   ├── client.go              # High-level Roon API client
│   │   └── types.go               # All JSON-mapped structs
│   └── tui/
│       ├── app.go                 # Main Bubble Tea model, view routing, key handling
│       ├── player.go              # Now Playing view rendering
│       ├── browser.go             # Library browser (custom list with fuzzy filter)
│       ├── albumart.go            # Album art fetching and terminal rendering
│       └── styles.go              # Lip Gloss styles and color palette
```

## Protocols

### MOO over WebSocket

- Connect to `ws://{host}:{port}/api`
- Binary frames with HTTP-like format: `MOO/1 VERB path\nRequest-Id: N\n...`
- Verbs: REQUEST (send), COMPLETE (response), CONTINUE (subscription update)
- Flow: info -> register -> subscribe_zones -> transport controls

### SOOD Discovery (UDP)

- Implemented in `sood.go`; used automatically when no address is given via flags/env and the cached address fails
- Roon broadcasts on UDP 9003, multicast 239.255.90.90
- Discovery returns early ~500ms after the first core responds; the last successfully connected address is cached at `~/.config/roonamp/server` so discovery only runs again when the Core's IP changes

## Roon API notes

- `zones_removed` sends an array of zone ID **strings**, not Zone objects
- `now_playing` can be null (nothing playing)
- `volume` on an output can be null (fixed volume device)
- The extension API does NOT expose signal path info (sample rate, bit depth, codec)
- Browse API `item_key` must be omitted (not null) to trigger "go back" -- use `omitempty` on the struct tag
- Browse items with `hint: "action_list"` return a sub-menu (Play Now, Add to Queue, etc.) -- navigate into them, don't treat as immediate actions
- Only `hint: "action"` items trigger immediately
- First-time auth: user must enable extension in Roon Settings -> Extensions
- Auth token persisted at `~/.config/roonamp/token`

## Browser implementation

The browser uses a **client-side navigation stack** instead of relying on Roon's browse "go back" mechanism (which doesn't work reliably with omitted `item_key`):

- Forward navigation: push current items/cursor/title onto stack, then browse+load new level from Roon
- Back navigation: pop from stack instantly (no API call, cursor position preserved)
- Fuzzy filter: uses `sahilm/fuzzy` (same algorithm as fzf) for `/` filtering
- Global library search (`s`): the Roon "Search" item lives inside Library on stock installs, so `searchCmd` does `pop_all` → loads root → looks for Search → otherwise drills into "Library" and looks there → browses Search with `input: <query>`
- Artist pages and `multi_session_key`: drilling into an artist prepends a synthetic "Show all albums (incl. streaming)" row that runs a side-trip search → Albums drill. To avoid invalidating the main session's `item_key`s (the side trip needs `pop_all`), the synthetic action and the empty-page auto-fallback both run under `multi_session_key: "synthetic"`. Each `browseLevel` on the stack remembers which session it belonged to so `goBack` restores the right session along with the items.

## Current state

### Implemented
- MOO/1 protocol over WebSocket (binary frames, subscriptions, ping handler)
- SOOD discovery protocol (auto-discovery fallback; flags/env and cached address take priority)
- Roon client: connect, register, subscribe zones, transport controls, browse API
- Config: CLI flags (`-host`, `-port`), env var fallback, XDG token/zone/prefs persistence
- TUI player view: track info, animated progress bar, album art, zone switcher
- TUI browser view: custom list with vim-style navigation and fzf-style fuzzy filtering
- Transport controls: play/pause, next/prev, stop, volume (+/- by 1)
- Album art rendering in terminal (half-block characters)
- Volume auto-hide after 5 seconds (shows on local or external changes)
- Persisted preferences: selected zone, show/hide album art
- Zone whitelist: optional `~/.config/roonamp/zones` file (one entry per line; exact zone ID or case-insensitive substring of the display name; `#` comments; no matches → fall back to all zones)
- Text truncation for long track/artist/album names
- Queue info display (remaining tracks and time)
- Zone settings display (shuffle, loop)
- Log output suppressed to avoid TUI corruption

### Keybindings

#### Player view
- `space` -- play/pause
- `n` -- next track, `p` -- previous track, `s` -- stop
- `+`/`=` -- volume up 1, `-` -- volume down 1
- `<`/`,` and `>`/`.` or arrow keys -- switch zone
- `a` -- toggle album art (persisted)
- `b` -- open library browser
- `q` -- quit, `ctrl+c` -- force quit

#### Browser view
- `j`/`k` or arrows -- navigate up/down
- `l`/`enter`/`right` -- drill into item
- `h`/`backspace`/`left` -- go back one level
- `/` -- fuzzy filter (fzf-style) on the current list
- `s` -- global library search (artists, albums, tracks) via Roon's browse search
- `esc`/`q` -- return to player

#### Filter mode (in browser)
- Type to filter (fuzzy match on title + subtitle)
- `enter` -- accept filter
- `esc` -- clear filter
- `backspace` -- delete character

## Dependencies

```
github.com/charmbracelet/bubbletea   v1.3.x
github.com/charmbracelet/lipgloss    v1.1.x
github.com/charmbracelet/bubbles     v1.0.x   (progress bar)
github.com/charmbracelet/harmonica   v0.2.x   (spring animations)
github.com/gorilla/websocket         v1.5.x
github.com/sahilm/fuzzy              v0.1.x   (fzf-style fuzzy matching)
```
