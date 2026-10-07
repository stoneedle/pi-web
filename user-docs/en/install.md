# Installation & Usage

## Features

### Remote control

- Continue any session from the browser with text or image attachments
- Start a brand-new session against any project path, right from the web UI
- In-browser model switching and thinking-level selector, per session
- Per-session worker status (idle / running / error) with auto-recovery on crash
- Multiple sessions run in parallel — kick off work in one, watch another stream
- `PI_WEB_TOKEN` for safe LAN exposure — required by default for any explicit non-loopback bind

### Reading sessions

- Browse sessions across projects with filters, search, and full branch navigation
- Live incremental updates while pi is still running (via fsnotify; ~ms latency)
- Follow mode for tailing active sessions
- Deep links to individual messages
- Download a session as JSONL
- Share static snapshots as secret GitHub Gists
- `/web`, `/remote`, `/refresh`, `/pi-web token` and `/pi-web set-token` pi extensions for opening sessions, remote QR, session sync, and token management
- `/skill:pi-web-schedule`, `/skill:pi-web-notes`, `/skill:pi-web-settings` (`pi-web-ctl`) so a session can manage schedules, the project scratchpad, and settings in natural language

## Requirements

- Pi 1.0.4, Node.js 22.19+, Go 1.26+, Git and GNU Make
- Git Bash on Windows; set `MAKE` to the GNU Make executable if it is outside `PATH`
- Optional: `gh` for sharing and Tailscale for remote access

## Install the source fork

```bash
pi install git:github.com/stoneedle/pi-title-glyphs
pi install git:github.com/stoneedle/pi-web
```

The web package builds its frontend and Go binary from the same fork checkout, then installs the binary and CLI into `~/.pi/agent/bin/`. Its platform installer sets up login startup. The title plugin's README describes the exact Pi 1.0.4 host title repair; apply it and restart Pi. Ordinary extension updates can use `/reload`.

Names come from the title plugin: the first opening text is saved immediately, and an optional background model summary may replace it once. Configure `{ "model": "openai-codex/gpt-6-luna" }` in `<agent-dir>/extension-data/pi-title-glyphs/config.json` to enable that summary. pi-web reads native names and supplies manual renaming. Manual names persist through reopen and defeat pending summaries. Existing named sessions keep their names.

For a local development checkout:

```bash
git clone https://github.com/stoneedle/pi-web.git
cd pi-web
make build
pi install .
node scripts/run-lifecycle.mjs install
```

On Windows use `make build BINARY=pi-web.exe`. The lifecycle installer also builds before installing. In-app updates target this source fork, and Git development builds retain the existing development-version update guard. Keep the build toolchain available to the update process.

### Develop alongside an installed instance

Leave the installed instance running on port `31415`, then start the source
checkout in development mode:

```bash
make dev
```

Open `http://127.0.0.1:31416`. `make dev` sets the internal `PI_WEB_DEV=1`
development environment, so the source checkout shares sessions, settings, and
SQLite data with the installed instance while keeping a separate development
runtime lock and state file. Regular installed and manually launched instances
are unchanged and retain the original single-instance behavior.

To prevent duplicate autonomous work, development mode does not run the
schedule loop, chat-queue drainer, or push notifications. Direct
requests made through the development UI still work. Do not drive the same
chat session from both instances at once; each process has its own RPC worker
manager.

`make dev` requires [Air](https://github.com/air-verse/air) for Go hot reload:

```bash
go install github.com/air-verse/air@latest
```

`PI_WEB_DEV` is development harness plumbing, not a supported production
multi-instance mode.

## Uninstall

```bash
pi remove npm:@ygncode/pi-web@beta
```

This runs the package `preuninstall` script (`uninstall.sh`, or `uninstall.ps1`
on Windows), which stops the running instance and removes:

- the pi-web binary (`~/.pi/agent/bin/pi-web`, or `/usr/local/bin/pi-web` for standalone installs)
- the version file (`~/.pi/agent/pi-web-version`)
- the runtime state file (`~/.pi/agent/pi-web/pi-web-state.json`)
- the auto-start config (launchd plist on macOS, systemd user service on Linux, Run-key entry + launcher scripts on Windows)

Your data is preserved so a later reinstall picks up where you left off:
`~/.pi/agent/pi-web.sqlite`, `~/.pi/agent/pi-web-memory.sqlite`, your session
files under `~/.pi/agent/sessions/`, and `~/.config/pi-web/env` (including
`PI_WEB_TOKEN`). Remove those manually if you want a clean slate.

## Usage

```bash
# Start on the default port (31415)
pi-web

# Start and open a browser
pi-web -o

# Custom port
pi-web -p 8080

# Override bind host (loopback is unauthenticated by default)
pi-web --host 127.0.0.1

# Non-loopback bind requires a token — pi-web refuses to start otherwise
PI_WEB_TOKEN=$(openssl rand -hex 16) pi-web --host 192.168.1.50
```

By default, pi-web binds to `127.0.0.1`. If Tailscale is running with MagicDNS **and `PI_WEB_TOKEN` is set**, pi-web also runs `tailscale serve --bg --https=<port> http://127.0.0.1:<port>` and prints the HTTPS tailnet URL. Without a token, pi-web stays loopback-only and skips Tailscale Serve, so tailnet peers cannot reach the agent unauthenticated. Any explicit non-loopback bind also requires `PI_WEB_TOKEN` to be set; pass `--insecure` to override for local testing.

## Remote Access

Leave pi-web listening locally, then use the printed Tailscale HTTPS URL from your phone or laptop on the tailnet.

On macOS, install and open Tailscale interactively, approve the administrator prompt, and sign in. Then run `/pi-web restart`, followed by `/remote`.

On Linux, allow your user to manage Tailscale before installing/running pi-web, otherwise `tailscale serve` may require sudo and auto-start can fail:

```bash
sudo tailscale set --operator=$USER
```

```bash
# 1. Start pi-web with a token so it publishes the Tailscale HTTPS endpoint
PI_WEB_TOKEN=$(openssl rand -hex 16) pi-web

# 2. From any other Tailscale-connected device, open the printed
#    "Tailscale HTTPS" URL and enter the token once.
```

> By default, pi-web refuses to bind to a non-loopback address unless `PI_WEB_TOKEN` is set — anyone who can reach the bound address could otherwise view sessions and send instructions to pi. To override this guard for local-network testing, pass `--insecure`. **Don't use `--insecure` on Tailscale or any address reachable from outside your machine.**
>
> Clients can pass the token via the `Authorization: Bearer <token>` header, the `X-Pi-Token` header, or once via `?token=<token>` (or the login prompt). When the token arrives through the query string, pi-web sets a `pi_token` cookie and redirects to the same URL with the token stripped, so it does not linger in the address bar or browser history. Prefer the header form for scripts and automation.

## Browser Chat

Open a session page and use the composer at the bottom to continue that exact session.

- `Enter` sends, `Shift+Enter` inserts a newline
- Drag-and-drop or paste images directly into the composer
- The model picker and thinking-level selector live in the header — changes apply to the underlying pi worker immediately
- Each active session gets its own dedicated `pi --mode rpc` worker, so different sessions don't block each other

## Sharing Sessions

Click **Share** on a session page to create a secret GitHub Gist.

Requirements:
- `gh` installed
- `gh auth login` completed

Sharing returns:
- the secret gist URL
- a preview URL at `https://pi.dev/session/#<gistId>`

Shared gists are snapshots and do not live-update.

## Auto-Start on Login

### macOS

```bash
cp init/com.pi-web.plist ~/Library/LaunchAgents/
launchctl load ~/Library/LaunchAgents/com.pi-web.plist
```

### Linux (systemd)

```bash
# Install the systemd user service
mkdir -p ~/.config/systemd/user
cp init/pi-web.service ~/.config/systemd/user/

# Optional: set your PI_WEB_TOKEN for non-loopback binds
# (or use /pi-web set-token <token> from inside pi)
mkdir -p ~/.config/pi-web
echo 'PI_WEB_TOKEN=your-token-here' > ~/.config/pi-web/env

# Enable and start
systemctl --user daemon-reload
systemctl --user enable --now pi-web.service

# Check status
systemctl --user status pi-web.service

# View logs
journalctl --user -u pi-web.service -f
```

> For the service to start at boot (before login), use a system service instead:
> copy `init/pi-web.service` to `/etc/systemd/system/` and use `sudo systemctl`.

### Windows

The installer configures this automatically, without needing admin rights: a
`pi-web` entry under `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
launches `~/.config/pi-web/pi-web-start.vbs` at login, which starts the binary
hidden (no console window) after loading `~/.config/pi-web/env`
(`PI_WEB_TOKEN`, `PATH`, ...).

To manage it by hand:

```powershell
# Start / stop
wscript.exe "$HOME\.config\pi-web\pi-web-start.vbs"
taskkill /IM pi-web.exe /F

# Remove auto-start
Remove-ItemProperty -Path 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run' -Name 'pi-web'
```

There is no service supervision on Windows: if pi-web crashes it stays down
until the next login (launchd/systemd restart it automatically on the other
platforms).
