# CLAUDE.md — clipstack developer guide

Architecture reference and design rationale for contributors and AI agents working on this codebase.

---

## Overview

**clipstack** is a two-binary clipboard history manager for Linux (Wayland + XWayland).

| Binary | Role |
|--------|------|
| `clipd` | Background daemon. Polls the clipboard, persists entries to SQLite, serves a Unix socket. |
| `clip`  | Terminal UI. Connects to the daemon, lets the user browse, search, pin, and re-paste entries. |

The two processes communicate over a Unix domain socket at `/tmp/clipstack.sock` using newline-delimited JSON.

---

## Project structure

```
clipstack/
├── cmd/
│   ├── clipd/main.go      — daemon entry point
│   └── clip/main.go       — TUI entry point
└── internal/
    ├── proto/proto.go      — shared request/response types and JSON encoding
    ├── clipboard/clipboard.go — xsel read/write wrapper
    └── db/db.go            — SQLite persistence layer
```

---

## Design decisions

### Why two binaries?

Separating the daemon from the UI keeps the daemon minimal and always-on. The UI can crash, be restarted, or never opened — clipboard capture is unaffected. It also means other clients (scripts, integrations) can talk to `clipd` over the socket without coupling to the TUI.

### Why xsel and polling?

The target machine has no root access, no `apt`, and no `wl-clipboard`. Only `xsel` is available. True event-driven clipboard monitoring on X11 requires the `XFixes` C extension, which means CGo — ruled out by the static-binary constraint. Polling every 500 ms is the pragmatic choice: latency is imperceptible and CPU impact is negligible.

### Why modernc/sqlite?

`modernc.org/sqlite` is a pure-Go SQLite port — no CGo, no system library. This is the only viable embedded database choice when `CGO_ENABLED=0` is a hard requirement. `mattn/go-sqlite3` is explicitly excluded.

### Why WAL mode?

The daemon writes to the database continuously while the TUI reads from it. SQLite's default journal mode serialises all access; WAL allows concurrent readers and a single writer without blocking, which eliminates read latency in the TUI during active clipboard use.

### Why Unix socket + JSON?

Simple, debuggable, and zero-dependency. The protocol can be tested with `nc -U /tmp/clipstack.sock`. A binary protocol would save a few bytes but offer no practical benefit at this scale.

---

## Protocol

All messages are single-line JSON terminated with `\n`. The TUI holds one persistent connection for its entire session and sends requests sequentially (never in parallel on the same connection).

### Request

```json
{
  "type":   "list | search | pin | unpin | delete | copy",
  "id":     123,
  "query":  "search term",
  "limit":  200,
  "offset": 0
}
```

- `id` — used by `pin`, `unpin`, `delete`, `copy`
- `query` — used by `search`
- `limit` — default 200 when omitted or zero
- `offset` — reserved for pagination (not used by the TUI yet)

### Response

```json
{ "type": "resp", "items": [ ... ] }
{ "type": "err",  "error": "message" }
```

### Item

```json
{
  "id":         42,
  "content":    "copied text",
  "pinned":     false,
  "created_at": "2026-01-15T10:30:00Z"
}
```

---

## Database

**Path:** `~/.local/share/clipstack/history.db`
**Parameters:** `?_journal_mode=WAL&_busy_timeout=5000`, `SetMaxOpenConns(1)`

### Schema

```sql
CREATE TABLE clips (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    content    TEXT NOT NULL UNIQUE,
    pinned     INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL   -- RFC3339 UTC
);
CREATE INDEX idx_created ON clips(created_at DESC);
CREATE INDEX idx_pinned  ON clips(pinned DESC, created_at DESC);
```

**Deduplication:** re-copying existing content updates `created_at` (upsert), never creates a duplicate row.

**Sort order:** pinned entries always sort before unpinned; within each group, newest first.

---

## Daemon internals

### Startup sequence

1. Create `~/.local/share/clipstack/` if absent
2. Open `daemon.log` (append) and redirect all log output there — nothing goes to stdout/stderr
3. Open the database
4. Remove any stale socket, bind `/tmp/clipstack.sock`
5. Register `SIGINT`/`SIGTERM` handler: close listener, remove socket, exit cleanly
6. Launch `pollClipboard` goroutine
7. Accept connections in a loop; each connection gets its own goroutine

### Clipboard polling

Runs forever at 500 ms intervals. An entry is skipped if:
- `xsel` returns an error (display unavailable, etc.) — silent, never crashes
- Content is empty or whitespace-only
- Content is identical to the last captured entry (dedup in memory)
- Content exceeds 64 KB

### Connection handling

Each client connection is handled by a single goroutine that reads requests with a 1 MB scanner buffer (to handle large clipboard entries in responses) and writes JSON responses back. Write errors are silently ignored — the client may have disconnected.

---

## TUI internals

Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea) (MVU architecture).

### Modes

| Mode | Description |
|------|-------------|
| `modeList` | Default. Scrollable item list with tab bar. |
| `modeSearch` | Search input active; list filters as you type. |
| `modePreview` | Full-screen view of the selected entry's content. |

### Auto-refresh

A `tea.Tick` fires every 2 seconds and re-fetches the list from the daemon. New clipboard entries appear automatically without user interaction. All requests to the daemon go through a `sync.Mutex`-protected `client` struct, so the auto-refresh tick and manual user actions never race on the socket.

### Key normalisation

Navigation keys are normalised before dispatch:
- **Russian keyboard layout** — positional equivalents are mapped (е.г. `о→j`, `л→k`, `п→g`, `й→q`) so the app is usable without switching to Latin input
- **Both cases** — `j`/`J`, `k`/`K`, `q`/`Q`, etc. are all accepted, making the app work with CapsLock on

### Visible window (scrolling)

The list renders only the entries that fit on screen. The selected entry is kept near the vertical centre; the window clamps at the top and bottom of the list.

### Timestamp formatting

| Age | Display |
|-----|---------|
| < 1 minute | `just now` |
| < 1 hour | `Xm ago` |
| < 24 hours | `Xh ago` |
| older | `Jan 2` |

---

## Build

```bash
# Development (native platform)
make build

# Release (Linux amd64, stripped, into dist/)
make release

# Versioned release (tag first)
git tag v1.0.0
make release   # → dist/clipstack-v1.0.0-linux-amd64.tar.gz

# Cross-compile for ARM
make release GOARCH=arm64

# Clean
make clean
```

All builds use `CGO_ENABLED=0`. The resulting binaries are fully static and self-contained.

---

## Dependencies

| Package | Version | Purpose |
|---------|---------|---------|
| `github.com/charmbracelet/bubbletea` | v0.26.1 | TUI framework |
| `github.com/charmbracelet/bubbles` | v0.18.0 | Text input component |
| `github.com/charmbracelet/lipgloss` | v0.11.0 | Terminal styling |
| `modernc.org/sqlite` | v1.29.9 | Pure-Go SQLite (no CGo) |

---

## Out of scope

These features were considered and intentionally deferred:

- **Primary X11 selection** (mouse-highlight auto-save) — not needed
- **wl-clipboard / wl-paste** — xsel covers the use case via XWayland
- **TUI pagination** — 200-entry limit is sufficient; `offset` is in the protocol for future use
- **Database encryption**
- **History export**
- **Multiple profiles**
- **GUI**
