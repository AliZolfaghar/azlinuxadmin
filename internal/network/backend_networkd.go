package network

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/AliZolfaghar/azlinuxadmin/internal/change"
	"github.com/AliZolfaghar/azlinuxadmin/internal/priv"
)

const networkdDir = "/etc/systemd/network"

func enrichFromNetworkd(ifaces []Iface) {
	files, _ := filepath.Glob(filepath.Join(networkdDir, "*.network"))
	idx := map[string]*Iface{}
	for i := range ifaces {
		idx[ifaces[i].Name] = &ifaces[i]
	}
	for _, path := range files {
		data, err := priv.ReadFile(path)
		if err != nil {
			continue
		}
		name := networkdMatchName(string(data))
		ifc, ok := idx[name]
		if !ok {
			continue
		}
		dhcp := strings.Contains(string(data), "DHCP=yes") || strings.Contains(string(data), "DHCP=ipv4")
		ifc.DHCP4 = &dhcp
		ifc.Addresses = networkdValues(string(data), "Address=")
		if gws := networkdValues(string(data), "Gateway="); len(gws) > 0 {
			ifc.Gateway = gws[0]
		}
		ifc.Nameservers = networkdValues(string(data), "DNS=")
	}
}

func networkdMatchName(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Name=") {
			return strings.TrimPrefix(line, "Name=")
		}
	}
	return ""
}

func networkdValues(content, key string) []string {
	var out []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, key) {
			out = append(out, strings.TrimPrefix(line, key))
		}
	}
	return out
}

func applyIfaceNetworkd(j *change.Journal, upd IfaceUpdate) (*change.Entry, error) {
	path, backups, err := backupNetworkdFile(upd.Name)
	if err != nil {
		return nil, err
	}
	content := buildNetworkdUnit(upd)
	if err := priv.WriteFile(path, []byte(content), 0o644); err != nil {
		restoreAll(backups)
		return nil, err
	}
	if err := reloadNetworkd(); err != nil {
		restoreAll(backups)
		_ = reloadNetworkd()
		return nil, fmt.Errorf("networkd reload failed (restored backup): %w", err)
	}
	return j.Record("network.iface", ifaceSummary(upd), backups, map[string]string{
		"iface":   upd.Name,
		"manager": string(ManagerNetworkd),
		"file":    path,
	})
}

func applyDNSNetworkd(j *change.Journal, upd IfaceUpdate) (*change.Entry, error) {
	path, backups, err := backupNetworkdFile(upd.Name)
	if err != nil {
		return nil, err
	}
	data, err := priv.ReadFile(path)
	if err != nil {
		return nil, err
	}
	updated := upsertNetworkdDNS(string(data), upd.Nameservers)
	if err := priv.WriteFile(path, []byte(updated), 0o644); err != nil {
		restoreAll(backups)
		return nil, err
	}
	if err := reloadNetworkd(); err != nil {
		restoreAll(backups)
		_ = reloadNetworkd()
		return nil, fmt.Errorf("networkd reload failed (restored backup): %w", err)
	}
	return j.Record("network.dns",
		fmt.Sprintf("DNS on %s → %s", upd.Name, strings.Join(upd.Nameservers, ", ")),
		backups, map[string]string{"iface": upd.Name, "manager": string(ManagerNetworkd), "file": path})
}

func applyGatewayNetworkd(j *change.Journal, upd IfaceUpdate) (*change.Entry, error) {
	path, backups, err := backupNetworkdFile(upd.Name)
	if err != nil {
		return nil, err
	}
	content := buildNetworkdUnit(upd)
	if err := priv.WriteFile(path, []byte(content), 0o644); err != nil {
		restoreAll(backups)
		return nil, err
	}
	if err := reloadNetworkd(); err != nil {
		restoreAll(backups)
		_ = reloadNetworkd()
		return nil, fmt.Errorf("networkd reload failed (restored backup): %w", err)
	}
	return j.Record("network.gateway",
		fmt.Sprintf("gateway on %s → %s", upd.Name, upd.Gateway),
		backups, map[string]string{"iface": upd.Name, "manager": string(ManagerNetworkd), "file": path})
}

func backupNetworkdFile(iface string) (string, []change.FileBackup, error) {
	_ = priv.Run("mkdir", "-p", networkdDir)
	files, _ := filepath.Glob(filepath.Join(networkdDir, "*.network"))
	for _, path := range files {
		data, err := priv.ReadFile(path)
		if err != nil {
			continue
		}
		if networkdMatchName(string(data)) == iface {
			b, err := change.BackupFile(path)
			if err != nil {
				return "", nil, err
			}
			return path, []change.FileBackup{b}, nil
		}
	}
	// Create a new unit file for this iface.
	path := filepath.Join(networkdDir, fmt.Sprintf("10-%s.network", iface))
	b, err := change.BackupFile(path) // Existed=false if new
	if err != nil {
		return "", nil, err
	}
	return path, []change.FileBackup{b}, nil
}

func buildNetworkdUnit(upd IfaceUpdate) string {
	var b strings.Builder
	b.WriteString("[Match]\n")
	b.WriteString("Name=" + upd.Name + "\n\n")
	b.WriteString("[Network]\n")
	if upd.DHCP4 {
		b.WriteString("DHCP=ipv4\n")
	} else {
		b.WriteString("DHCP=no\n")
		b.WriteString("Address=" + strings.TrimSpace(upd.AddressCIDR) + "\n")
		if upd.Gateway != "" {
			b.WriteString("Gateway=" + upd.Gateway + "\n")
		}
	}
	for _, ns := range upd.Nameservers {
		b.WriteString("DNS=" + ns + "\n")
	}
	return b.String()
}

func upsertNetworkdDNS(content string, dns []string) string {
	var lines []string
	inNetwork := false
	for _, line := range strings.Split(content, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "[Network]" {
			inNetwork = true
			lines = append(lines, line)
			continue
		}
		if strings.HasPrefix(trim, "[") {
			inNetwork = false
		}
		if inNetwork && strings.HasPrefix(trim, "DNS=") {
			continue
		}
		lines = append(lines, line)
	}
	// Append DNS under [Network]
	out := make([]string, 0, len(lines)+len(dns))
	inserted := false
	inNetwork = false
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "[Network]" {
			inNetwork = true
			out = append(out, line)
			continue
		}
		if inNetwork && strings.HasPrefix(trim, "[") {
			for _, ns := range dns {
				out = append(out, "DNS="+ns)
			}
			inserted = true
			inNetwork = false
		}
		out = append(out, line)
	}
	if !inserted {
		if !strings.Contains(content, "[Network]") {
			out = append(out, "", "[Network]")
		}
		for _, ns := range dns {
			out = append(out, "DNS="+ns)
		}
	}
	return strings.Join(out, "\n")
}

func reloadNetworkd() error {
	_ = priv.Run("networkctl", "reload")
	if err := priv.Run("systemctl", "restart", "systemd-networkd"); err != nil {
		return fmt.Errorf("restart systemd-networkd: %w", err)
	}
	return nil
}
