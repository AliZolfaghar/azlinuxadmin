# azlinuxadmin — progress

Track build steps here. Mark each task done when finished; add new tasks as they are specified.

## Goals

- Manage Linux from a terminal TUI
- Ship as a single executable file

## Stack

- Go
- Bubbletea
- Lipgloss
- Bubbles (Charm)

## How we work

You give steps; we implement; we check them off here.

## Tasks

- [x] Scaffold Go module and Charm TUI skeleton
- [x] Produce a single `go build` executable
- [x] Shell UI layout (header / sidebar / main / footer) — no modules yet
- [x] Add `run-dev.sh` for local development (`go run`)
- [x] Rule: all server changes must be safe and undoable (`.cursor/rules/safe-undoable-changes.mdc`)
- [x] Network page: hostname, NIC (netplan), DNS — with confirm + journal undo
- [x] Auto-detect network manager (NM / netplan / networkd / ifupdown) and apply via it
- [x] Network page: edit default gateway (with confirm + undo)
- [x] Users page: add / edit / delete / disable with journal undo
- [x] SSH page: common sshd settings (root login, passwords, port, …) with undo
- [x] SSH page: GatewayPorts setting
- [x] Sudo password popup with remember-until-exit
- [ ] (further tasks added as you specify them)
