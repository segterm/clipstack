# clipstack

Clipboard history manager for Linux (Wayland / XWayland). Saves everything you copy, lets you browse and re-paste from a fast terminal UI.

## Features

- Automatic background monitoring — copies appear in history within ~500 ms
- Persistent SQLite storage with deduplication (re-copying updates timestamp, no duplicates)
- TUI with vim-style navigation: search, pin, preview, delete
- Static binaries — no runtime dependencies beyond `xsel`

## Requirements

- Linux with Wayland or X11
- [`xsel`](https://github.com/kfish/xsel) available in `PATH`

## Installation

### From release binary

```bash
tar -xzf clipstack-<version>-linux-amd64.tar.gz
mkdir -p ~/.local/bin
cp clipd clip ~/.local/bin/
```

### Build from source

```bash
git clone https://github.com/yourname/clipstack
cd clipstack
make release          # → dist/clipstack-<version>-linux-amd64.tar.gz
```

Requires Go 1.21+. No CGo, no system libraries.

## Auto-start

Add to `~/.bashrc`:

```bash
export PATH="$HOME/.local/bin:$PATH"
pgrep -x clipd > /dev/null || (clipd &)
alias cm='clip'
```

Or use a systemd user unit:

```ini
# ~/.config/systemd/user/clipd.service
[Unit]
Description=Clipboard history daemon

[Service]
ExecStart=%h/.local/bin/clipd
Restart=on-failure
RestartSec=3

[Install]
WantedBy=default.target
```

```bash
systemctl --user enable --now clipd
```

## Usage

```bash
clipd &   # start daemon (or use auto-start above)
clip      # open TUI
```

## Keybindings

| Key | Action |
|-----|--------|
| `j` / `k` / `↑` `↓` | Navigate |
| `Enter` / `Space` | Copy to clipboard |
| `p` | Pin / unpin |
| `d` | Delete |
| `v` | Preview full content |
| `/` | Search |
| `Tab` | Switch All ↔ Pinned |
| `g` / `G` | Jump to top / bottom |
| `q` / `Esc` | Quit |

Navigation also works with the Russian keyboard layout.

## Data

| Path | Description |
|------|-------------|
| `~/.local/share/clipstack/history.db` | SQLite database |
| `~/.local/share/clipstack/daemon.log` | Daemon log |
| `/tmp/clipstack.sock` | Unix socket (runtime) |

## License

MIT
