# roonamp — Claude Code Task Specification

## What to build

A Winamp-inspired terminal TUI music controller for Roon, written in Go using the Charm ecosystem (Bubble Tea, Lip Gloss, Bubbles). It communicates DIRECTLY with the Roon Core over WebSocket using the native MOO protocol. No Node.js bridge, no HTTP proxy — pure Go talking to Roon.

Auto-discovers Roon Cores on the local network using SOOD (Roon's UDP discovery protocol) and presents a server selection screen.

## IMPORTANT CONSTRAINTS

- NO emojis or unicode icons anywhere in the UI. Use ASCII-only characters: `>`, `*`, `|`, `-`, `=`, `#`, `[`, `]`, etc.
- NO Node.js dependency. The app talks directly to Roon Core.
- Auto-discover Roon servers. Also allow manual IP:port via env vars as fallback.
- Single static binary, no config files for MVP (env vars only).

## Architecture

```
                        SOOD (UDP 9003)
                     multicast 239.255.90.90
                     + subnet broadcast
┌─────────────────┐ ─────────────────────────> ┌───────────┐
│   roonamp (Go)  │                            │ Roon Core │
│   Bubble Tea    │ <──── WebSocket ──────────>│           │
│                 │   ws://{ip}:{http_port}/api│           │
└─────────────────┘   MOO protocol messages    └───────────┘
```

## Reference implementations to study

Before writing any code, clone and read the source of these projects to understand the wire protocols:

1. **node-roon-api** (official, JS): https://github.com/RoonLabs/node-roon-api
   - `sood.js` — SOOD discovery protocol implementation
   - `moo.js` — MOO message framing
   - `moomsj.js` — MOO message serialization
   - `lib.js` — Connection flow, registration, service subscription

2. **pyroon** (Python): https://github.com/pavoni/pyroon
   - `roonapi/roonapi.py` — Main API class
   - `roonapi/roonapisocket.py` — WebSocket + MOO implementation
   - `roonapi/discovery.py` — SOOD discovery in Python
   - `roonapi/constants.py` — Service names

3. **RoonApi** (C#): https://github.com/ChristianRiedl/RoonApi
   - `RoonApiLib/` — Clean implementation with Discovery.cs, Moo.cs

Read these before starting. The protocols are not officially documented — the source IS the documentation.

## Protocol 1: SOOD Discovery (UDP)

SOOD = "Simple Out Of Distribution Discovery". Roon's custom discovery protocol.

### How it works

- Roon Core periodically broadcasts/multicasts UDP packets on port **9003**
- Multicast group: **239.255.90.90**
- Also broadcasts to the subnet broadcast address on port 9003
- Packets use a simple text-based format called `SOOD/2`

### SOOD packet format

Packets are binary with a text-based TLV structure. The format from studying sood.js:

```
"SOOD" (4 bytes magic)
 2     (1 byte, ASCII '2' = version)
type   ("Q" for query, "R" for response — 1 byte)

Then repeated TLV fields:
  type_char (1 byte: 'e' for IP, 'c' for service_id, etc.)
  length    (2 bytes, big-endian uint16)
  value     (length bytes, UTF-8 string)
```

The key fields in a Roon Core broadcast (type "R"):
- service_id (`c` type): `"00720724-5143-4a9b-abac-0e50cba674bb"` (this is the Roon Core service ID)
- unique_id: the core's unique identifier
- display_name: e.g. "My Roon Core"
- http_port: the TCP port for WebSocket connections (varies! could be 9100, 9150, 9200, 9330, etc.)
- ip: the core's IP address (from the `e` type field or from the UDP source address)

### Discovery implementation

To discover Roon Cores:

1. Create a UDP socket bound to port 9003
2. Join multicast group 239.255.90.90
3. Also send a SOOD query packet to broadcast/multicast to solicit responses
4. Listen for incoming SOOD "R" (response) packets
5. Parse the TLV fields to extract `display_name`, `http_port`, and IP (from UDP source addr)
6. Maintain a list of discovered cores with a timeout (cores that stop advertising are removed)

To send a query (solicit responses from cores):
- Build a SOOD/2 "Q" packet with `_tid` field (unique transaction ID)
- Send to multicast 239.255.90.90:9003 AND subnet broadcast:9003

**Fallback**: if env var ROON_HOST and ROON_PORT are set, skip discovery and connect directly.

## Protocol 2: MOO over WebSocket

MOO = Roon's custom message protocol, transported over standard WebSocket.

### Connection

Connect a WebSocket to: `ws://{host}:{http_port}/api`

Standard WebSocket upgrade handshake. No special headers needed.

### MOO message format

Each WebSocket **binary** frame contains one MOO message. The format is HTTP-like with a `MOO/1` version prefix. **Request-Id is a separate header line, NOT on the first line.**

For sending a request:
```
MOO/1 REQUEST {service_path}\n
Request-Id: {request_id}\n
[Content-Type: application/json\n]
[Content-Length: {n}\n]
\n
[{json_body}]
```

For receiving responses:
```
MOO/1 COMPLETE {status_name}\n
Request-Id: {request_id}\n
[Content-Type: application/json\n]
[Content-Length: {n}\n]
\n
[{json_body}]
```

For subscription updates:
```
MOO/1 CONTINUE {status_name}\n
Request-Id: {request_id}\n
[Content-Type: application/json\n]
[Content-Length: {n}\n]
\n
[{json_body}]
```

The Roon Core can also send REQUEST messages to the extension (e.g. ping). The extension responds with COMPLETE.

Content-Type and Content-Length headers are present only when there is a JSON body.

Header order: first line (MOO/1 VERB name), Request-Id, Content-Length (if body), Content-Type (if body), blank line, body.

### Connection flow

1. Open WebSocket to `ws://{host}:{port}/api`

2. Send info request:
   ```
   MOO/1 REQUEST com.roonlabs.registry:1/info
   Request-Id: 1
   ```
   Response: COMPLETE with `{"core_id": "...", "display_name": "...", "display_version": "..."}`

3. Send register:
   ```
   MOO/1 REQUEST com.roonlabs.registry:1/register
   Request-Id: 2
   Content-Type: application/json
   Content-Length: ...

   {
     "extension_id": "com.brokenrubik.roonamp",
     "display_name": "roonamp",
     "display_version": "0.1.0",
     "publisher": "BrokenRubik",
     "email": "dev@brokenrubik.com",
     "required_services": ["com.roonlabs.transport:2", "com.roonlabs.browse:1"],
     "optional_services": ["com.roonlabs.image:1"],
     "provided_services": ["com.roonlabs.ping:1"],
     "token": "{saved_token_or_empty}"
   }
   ```

   Response: CONTINUE with `{"core_id": "...", "display_name": "...", "token": "...", "provided_services": [...], "http_port": ...}`

   **First time**: the user must go to Roon Settings -> Extensions and click "Enable". The register response arrives only AFTER the user approves. Save the token for future connections.

4. Handle ping requests from Roon Core:
   When Roon sends: `MOO/1 REQUEST com.roonlabs.ping:1/ping` (with Request-Id header)
   Respond with: `MOO/1 COMPLETE Success` (with matching Request-Id header)

5. Subscribe to zones:
   ```
   MOO/1 REQUEST com.roonlabs.transport:2/subscribe_zones
   Request-Id: 3
   Content-Type: application/json
   Content-Length: ...

   {"subscription_key": "0"}
   ```
   
   Initial response: CONTINUE with `{"zones": [...]}`  (status = "Subscribed")
   Updates: CONTINUE with `{"zones_changed": [...]}`, `{"zones_removed": [...]}`, `{"zones_added": [...]}`

### Transport control methods

All sent as REQUEST to `com.roonlabs.transport:2/{method}`:

| Method | Body | Description |
|---|---|---|
| `control` | `{"zone_or_output_id": "{id}", "control": "play"}` | control: play, pause, playpause, stop, previous, next |
| `change_volume` | `{"output_id": "{id}", "how": "relative", "value": 5}` | how: "absolute" or "relative" |
| `mute` | `{"output_id": "{id}", "how": "mute"}` | how: "mute" or "unmute" |
| `change_settings` | `{"zone_or_output_id": "{id}", "shuffle": true}` | shuffle, auto_radio, loop |
| `seek` | `{"zone_or_output_id": "{id}", "how": "absolute", "seconds": 30}` | how: "absolute" or "relative" |

### Browse API

Sent as REQUEST to `com.roonlabs.browse:1/browse` and `com.roonlabs.browse:1/load`:

Browse (navigate):
```json
{
  "hierarchy": "browse",
  "zone_or_output_id": "{zone_id}",
  "pop_all": true,
  "item_key": null,
  "input": null,
  "set_display_offset": 0
}
```

Load (get items for current list):
```json
{
  "hierarchy": "browse",
  "offset": 0,
  "set_display_offset": 0,
  "count": 100
}
```

Load response:
```json
{
  "items": [
    { "title": "Artists", "subtitle": null, "item_key": "...", "image_key": null, "hint": "list" }
  ],
  "offset": 0,
  "list": { "title": "Library", "count": 7, "level": 0 }
}
```

To navigate into an item: browse with item_key set. To search: set input. To go back: browse with pop_all or without item_key.

### Token persistence

Save auth token to `~/.config/roonamp/token` (or `$XDG_CONFIG_HOME/roonamp/token`). Load on startup, pass in register request. If invalid, Roon requires re-authorization.

## Zone JSON structure

```json
{
  "zone_id": "16011a60867...",
  "display_name": "Living Room",
  "state": "playing",
  "is_next_allowed": true,
  "is_previous_allowed": true,
  "is_pause_allowed": true,
  "is_play_allowed": false,
  "is_seek_allowed": true,
  "settings": { "loop": "disabled", "shuffle": false, "auto_radio": true },
  "now_playing": {
    "seek_position": 154,
    "length": 355,
    "one_line": { "line1": "Track - Artist" },
    "two_line": { "line1": "Track", "line2": "Artist" },
    "three_line": { "line1": "Track Title", "line2": "Artist Name", "line3": "Album Name" },
    "image_key": "abc123..."
  },
  "outputs": [
    {
      "output_id": "17010049...",
      "display_name": "FiiO K17",
      "zone_id": "16011a60867...",
      "volume": { "type": "number", "min": 0, "max": 100, "value": 42, "step": 1, "is_muted": false }
    }
  ],
  "queue_items_remaining": 12,
  "queue_time_remaining": 2845
}
```

now_playing can be null. volume on an output can be null.

## Project structure

```
roonamp/
├── main.go
├── go.mod                         # module github.com/brokenrubik/roonamp
├── Makefile
├── .gitignore
├── README.md
├── CLAUDE.md
├── internal/
│   ├── config/
│   │   └── config.go              # Env vars + XDG token path
│   ├── roon/
│   │   ├── sood.go                # SOOD UDP discovery protocol
│   │   ├── moo.go                 # MOO message framing over WebSocket
│   │   ├── client.go              # High-level Roon API client
│   │   └── types.go               # All JSON-mapped structs
│   └── tui/
│       ├── app.go                 # Main Bubble Tea model (Elm Architecture)
│       ├── discovery.go           # Server selection view
│       ├── player.go              # Now Playing view
│       ├── browser.go             # Library browser view
│       ├── zones.go               # Zone selection view
│       └── styles.go              # Lip Gloss styles (ASCII-only Winamp theme)
```

## Dependencies

```
github.com/charmbracelet/bubbletea   (latest v1.x)
github.com/charmbracelet/lipgloss    (latest v1.x)
github.com/charmbracelet/bubbles     (latest v0.x)
github.com/gorilla/websocket         (WebSocket client)
```

Plus standard library: net, encoding/json, encoding/binary, fmt, strings, time, io, os, path/filepath, sync.

## TUI Design — ASCII-only, no emojis

### ASCII symbol table

| Concept | Symbol |
|---|---|
| Playing | `[>]` |
| Paused | `[=]` |
| Stopped | `[x]` |
| Loading | `[..]` |
| Selected cursor | `> ` |
| Unselected | `  ` |
| Active zone | `(*)` |
| Progress filled | `=` |
| Progress empty | `-` |
| Volume filled | `#` |
| Volume empty | `.` |
| Browse folder | `[+]` |
| Browse action | `[>]` |
| Tab separator | ` \| ` |

### Four views

**1. Servers View** (first launch):
```
+----------------------------------------------+
|  roonamp                                     |
|                                              |
|  Scanning for Roon servers...                |
|                                              |
|  > My Roon Core (192.168.1.50:9330)          |
|    Office Server (192.168.1.100:9200)        |
|                                              |
|  [enter] connect  [r] refresh  [m] manual    |
+----------------------------------------------+
```

**2. Player View**:
```
+----------------------------------------------+
|  roonamp  |  Living Room  |  [>] PLAYING     |
|                                              |
|  Bohemian Rhapsody                           |
|  Queen                                       |
|  A Night at the Opera                        |
|                                              |
|  [=========-----------]  02:34 / 05:55       |
|                                              |
|  VOL [#########...............] 42           |
|                                              |
|  [p] prev  [space] play/pause  [n] next     |
|  [s] stop  [-/+] volume                     |
+----------------------------------------------+
```

**3. Zones View**:
```
+----------------------------------------------+
|  Zones                                       |
|                                              |
|  > [>] Living Room (*)  Bohemian Rhapsody    |
|    [x] Bedroom                               |
|    [=] Office           Clair de Lune        |
|                                              |
|  [enter] select  [esc] back                  |
+----------------------------------------------+
```

**4. Library Browser**:
```
+----------------------------------------------+
|  Library > Artists > Queen                   |
|                                              |
|  > [>] Bohemian Rhapsody -- A Night at ...   |
|    [>] Somebody to Love -- A Day at ...      |
|    [+] Greatest Hits                         |
|                                              |
|  24 items                                    |
|                                              |
|  [enter] open  [/] search  [bksp] back      |
+----------------------------------------------+
```

## Keybindings

### Global
- `q`, `ctrl+c` -- quit
- `z` -- Zones view
- `b` -- Library browser
- `S` -- Servers view

### Servers view
- `j`/down, `k`/up -- navigate
- `enter` -- connect
- `r` -- rescan
- `m` -- manual IP entry

### Player view
- `space` -- play/pause
- `n` -- next, `p` -- previous, `s` -- stop
- `+`/`=` -- volume up 5, `-` -- volume down 5

### Zones view
- `j`/down, `k`/up -- navigate
- `enter` -- select zone
- `esc` -- back to player

### Browser (normal)
- `j`/down, `k`/up -- navigate
- `enter` -- open/play
- `backspace` -- go up
- `/` -- search mode
- `esc` -- back to player

### Browser (search mode)
- printable chars -- type
- `backspace` -- delete char
- `enter` -- submit
- `esc` -- cancel

## Implementation order

1. `internal/roon/types.go` -- all structs
2. `internal/roon/moo.go` -- MOO framing (study node-roon-api/moo.js and pyroon/roonapisocket.py first!)
3. `internal/roon/sood.go` -- SOOD discovery (study node-roon-api/sood.js and pyroon/discovery.py first!)
4. `internal/roon/client.go` -- high-level client
5. `internal/config/config.go` -- env vars + token
6. `internal/tui/styles.go` -- ASCII-only theme
7. `internal/tui/discovery.go` -- server selection
8. `internal/tui/zones.go` -- zone list
9. `internal/tui/player.go` -- now playing
10. `internal/tui/browser.go` -- library browser
11. `internal/tui/app.go` -- main model
12. `main.go` -- entry point

## CRITICAL: read the source code first

The MOO and SOOD protocols are NOT formally documented. Clone these repos and read the specific files listed above BEFORE writing moo.go and sood.go:

```bash
git clone https://github.com/RoonLabs/node-roon-api.git
git clone https://github.com/pavoni/pyroon.git
git clone https://github.com/ChristianRiedl/RoonApi.git
```

## Quality

- Must compile with `go build ./...`
- `gofmt` all files
- Handle nil pointers (NowPlaying, Volume, empty zones)
- ASCII-only UI — no emojis, no fancy unicode
- Max 80 col content width
- Clean WebSocket shutdown on quit

## Current progress

### Done
- Project scaffolding: go.mod, directory structure, dependencies installed
- `internal/roon/types.go` -- all JSON-mapped structs (zones, outputs, volume, transport, register, etc.)
- `internal/roon/moo.go` -- MOO/1 protocol framing over WebSocket (binary frames, Request-Id header)
- `internal/roon/sood.go` -- SOOD UDP discovery (multicast + broadcast, TLV parsing)
- `internal/roon/client.go` -- high-level client (connect, info, register, subscribe_zones, ping handler)
- `internal/config/config.go` -- env vars (ROON_HOST, ROON_PORT) + XDG token persistence
- `main.go` -- working proof-of-concept (discovers/connects, registers, shows zones + now playing)
- PoC tested successfully against live Roon Core (192.168.0.159:9330)
- Auth token saved to ~/.config/roonamp/token

### Roon server details (dev environment)
- Roon Core: k-server at 192.168.0.159, port 9330
- Ubuntu host, RoonAppliance process
- Firewall ports opened: 9003 (UDP), 9330 (TCP)
- Two active zones observed: "Living Room speaker", "Fiio K17"

### Next: build the Bubble Tea TUI
1. `internal/tui/styles.go` -- Lip Gloss ASCII-only Winamp theme
2. `internal/tui/discovery.go` -- server selection view
3. `internal/tui/zones.go` -- zone list view
4. `internal/tui/player.go` -- now playing view (main view)
5. `internal/tui/browser.go` -- library browser view
6. `internal/tui/app.go` -- main Bubble Tea model routing between views
7. Update `main.go` to use TUI instead of proof-of-concept output
8. Add transport controls (play/pause/next/prev/volume/seek)
9. Add library browsing via Browse API
