# AZLinuxAdmin

Terminal UI to manage a Linux VPS from one binary. Built with Go and Charm (Bubbletea / Lipgloss / Bubbles).

**Requires root** — run with `sudo`.

## Features

### Implemented

| Module | What you can do |
|--------|-----------------|
| **Users** | Add, edit, delete, and disable accounts (with journal undo) |
| **SSH** | Edit common `sshd` settings via a managed drop-in; view latest failed login attempts; apply / clear / undo |
| **Fail2Ban** | Install, start/stop, edit jail settings (`bantime`, `findtime`, `maxretry`, `ignoreip`, SSH jail), live log view |
| **Network** | Hostname, NIC addressing, DNS, default gateway — auto-detects NetworkManager, netplan, systemd-networkd, or ifupdown |

**Safety:** server mutations take a backup and are recorded in a change journal under `/var/lib/azlinuxadmin` so you can undo from the TUI.

**SSH settings managed:** PermitRootLogin, PasswordAuthentication, PubkeyAuthentication, PermitEmptyPasswords, KbdInteractiveAuthentication, X11Forwarding, GatewayPorts, MaxAuthTries, ClientAliveInterval / CountMax, Port. Changes go to `/etc/ssh/sshd_config.d/99-azlinuxadmin.conf`.

### Coming soon

Dashboard, Firewall, Services, Packages, Settings (sidebar placeholders).

## Quick start

```bash
# Development (re-execs under sudo if needed)
./run-dev.sh

# Build a single binary
go build -o azlinuxadmin .
sudo ./azlinuxadmin
```

Go **1.24+** recommended (`go.mod` pins `1.24.2`).

## Navigation

- Sidebar: `↑` / `↓`, `Enter` to open a module, `Esc` back to sidebar
- Inside modules: follow the footer hints (edit, confirm, history, undo, refresh)
- Quit: select **Exit** or `Ctrl+C`

## Stack

- [Bubbletea](https://github.com/charmbracelet/bubbletea) — TUI framework
- [Lipgloss](https://github.com/charmbracelet/lipgloss) — styling
- [Bubbles](https://github.com/charmbracelet/bubbles) — inputs / viewport

See [PROGRESS.md](PROGRESS.md) for the build checklist.
