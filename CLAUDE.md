# roonamp -- Claude Code Task Specification

## What this is

A terminal TUI music controller for Roon, written in Go using the Charm ecosystem (Bubble Tea, Lip Gloss, Bubbles). It communicates directly with the Roon Core over WebSocket using the native MOO protocol. No Node.js bridge, no HTTP proxy -- pure Go talking to Roon.

## Constraints

- NO emojis or unicode icons in the UI. ASCII-only for state indicators: `[>]`, `[=]`, `[x]`, `(*)`, etc.
- Unicode block characters are OK for the animated progress bar (bubbles/progress default).
- NO Node.js dependency. Direct WebSocket to Roon Core.
- Server address via `-host`/`-port` flags or `ROON_HOST`/`ROON_PORT` env vars.
- Single static binary. Preferences stored in `~/.config/roonamp/`.

## Architecture

```
┌─────────────────┐                            ┌───────────┐
│   roonamp (Go)  │ <──── WebSocket ──────────>│ Roon Core │
│   Bubble Tea    │   ws://{ip}:{port}/api     │           │
└─────────────────┘   MOO protocol messages    └───────────┘
```

## Project structure

```
roonamp/
├── main.go                        # Entry point: connect, register, launch TUI
├── go.mod
├── CLAUDE.md
├── README.md
├── internal/
│   ├── config/
│   │   └── config.go              # CLI flags, env vars, XDG persistence (token, zone, prefs)
│   ├── roon/
│   │   ├── sood.go                # SOOD UDP discovery (currently unused, manual connect only)
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

- Implemented in `sood.go` but currently disabled
- Roon broadcasts on UDP 9003, multicast 239.255.90.90
- Server address provided via flags/env vars instead

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

## Current state

### Implemented
- MOO/1 protocol over WebSocket (binary frames, subscriptions, ping handler)
- SOOD discovery protocol (unused, manual connect via flags)
- Roon client: connect, register, subscribe zones, transport controls, browse API
- Config: CLI flags (`-host`, `-port`), env var fallback, XDG token/zone/prefs persistence
- TUI player view: track info, animated progress bar, album art, zone switcher
- TUI browser view: custom list with vim-style navigation and fzf-style fuzzy filtering
- Transport controls: play/pause, next/prev, stop, volume (+/- by 1)
- Album art rendering in terminal (half-block characters)
- Volume auto-hide after 5 seconds (shows on local or external changes)
- Persisted preferences: selected zone, show/hide album art
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
- `/` -- fuzzy filter (fzf-style)
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
