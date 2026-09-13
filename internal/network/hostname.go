package network

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/AliZolfaghar/azlinuxadmin/internal/change"
)

// SetHostname updates /etc/hostname (and matching /etc/hosts) with backup+journal.
func SetHostname(j *change.Journal, newName string) (*change.Entry, error) {
	if err := RequireRoot(); err != nil {
		return nil, err
	}
	newName = strings.TrimSpace(newName)
	if err := validateHostname(newName); err != nil {
		return nil, err
	}
	old := currentHostname()
	if old == newName {
		return nil, fmt.Errorf("hostname already %q", newName)
	}

	hb, err := change.BackupFile(hostnamePath)
	if err != nil {
		return nil, err
	}
	hostsB, err := change.BackupFile(hostsPath)
	if err != nil {
		return nil, err
	}

	if err := os.WriteFile(hostnamePath, []byte(newName+"\n"), 0o644); err != nil {
		return nil, err
	}
	if err := updateHostsHostname(old, newName); err != nil {
		_ = change.Restore(hb)
		return nil, err
	}
	if path, err := exec.LookPath("hostnamectl"); err == nil {
		_ = exec.Command(path, "set-hostname", newName).Run()
	} else {
		_ = exec.Command("hostname", newName).Run()
	}

	return j.Record("network.hostname", fmt.Sprintf("hostname %s → %s", old, newName),
		[]change.FileBackup{hb, hostsB},
		map[string]string{"old": old, "new": newName},
	)
}

func validateHostname(h string) error {
	if h == "" || len(h) > 253 {
		return fmt.Errorf("invalid hostname length")
	}
	for _, part := range strings.Split(h, ".") {
		if part == "" || len(part) > 63 {
			return fmt.Errorf("invalid hostname label")
		}
		for i, c := range part {
			ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-'
			if !ok || (c == '-' && (i == 0 || i == len(part)-1)) {
				return fmt.Errorf("invalid hostname %q", h)
			}
		}
	}
	return nil
}

func updateHostsHostname(old, newName string) error {
	data, err := os.ReadFile(hostsPath)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	replaced := false
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		fields := strings.Fields(trim)
		if len(fields) < 2 {
			continue
		}
		ip := fields[0]
		if ip != "127.0.0.1" && ip != "127.0.1.1" {
			continue
		}
		var names []string
		foundNew := false
		for _, n := range fields[1:] {
			if n == old {
				names = append(names, newName)
				foundNew = true
				replaced = true
				continue
			}
			if n == newName {
				foundNew = true
			}
			names = append(names, n)
		}
		if !foundNew && (ip == "127.0.1.1" || (ip == "127.0.0.1" && !hasLocalHostname(lines))) {
			names = append(names, newName)
			replaced = true
		}
		lines[i] = ip + " " + strings.Join(names, " ")
	}
	if !replaced {
		lines = append(lines, "127.0.1.1 "+newName)
	}
	return os.WriteFile(hostsPath, []byte(strings.Join(lines, "\n")), 0o644)
}

func hasLocalHostname(lines []string) bool {
	for _, line := range lines {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) >= 2 && (fields[0] == "127.0.0.1" || fields[0] == "127.0.1.1") {
			for _, n := range fields[1:] {
				if n != "localhost" {
					return true
				}
			}
		}
	}
	return false
}

// UndoNetworkChange restores backups and reloads the matching manager.
func UndoNetworkChange(j *change.Journal, id string) (*change.Entry, error) {
	if err := RequireRoot(); err != nil {
		return nil, err
	}
	e, err := j.Undo(id)
	if err != nil {
		return nil, err
	}

	manager := Manager(e.Meta["manager"])
	if manager == "" {
		manager = DetectManager().Manager
	}

	switch manager {
	case ManagerNetplan:
		if err := netplanApply(); err != nil {
			return e, fmt.Errorf("files restored but netplan apply failed: %w", err)
		}
	case ManagerNetworkManager:
		_ = exec.Command("nmcli", "connection", "reload").Run()
		if dev := e.Meta["iface"]; dev != "" {
			_ = exec.Command("nmcli", "device", "reapply", dev).Run()
		}
	case ManagerNetworkd:
		_ = exec.Command("networkctl", "reload").Run()
		_ = exec.Command("systemctl", "restart", "systemd-networkd").Run()
	case ManagerIfupdown:
		if iface := e.Meta["iface"]; iface != "" {
			_ = exec.Command("ifdown", iface).Run()
			_ = exec.Command("ifup", iface).Run()
		} else {
			_ = exec.Command("systemctl", "restart", "networking").Run()
		}
	}

	if e.Kind == "network.hostname" {
		if newName := e.Meta["old"]; newName != "" {
			if path, err := exec.LookPath("hostnamectl"); err == nil {
				_ = exec.Command(path, "set-hostname", newName).Run()
			} else {
				_ = exec.Command("hostname", newName).Run()
			}
		}
	}
	return e, nil
}
