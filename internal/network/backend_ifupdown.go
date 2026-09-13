package network

import (
	"fmt"
	"os"
	"strings"

	"github.com/AliZolfaghar/azlinuxadmin/internal/change"
	"github.com/AliZolfaghar/azlinuxadmin/internal/priv"
)

const interfacesPath = "/etc/network/interfaces"

func enrichFromIfupdown(ifaces []Iface) {
	data, err := priv.ReadFile(interfacesPath)
	if err != nil {
		return
	}
	idx := map[string]*Iface{}
	for i := range ifaces {
		idx[ifaces[i].Name] = &ifaces[i]
	}
	var cur *Iface
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		if fields[0] == "iface" && len(fields) >= 4 && fields[2] == "inet" {
			if ifc, ok := idx[fields[1]]; ok {
				cur = ifc
				dhcp := fields[3] == "dhcp"
				cur.DHCP4 = &dhcp
			} else {
				cur = nil
			}
			continue
		}
		if cur == nil {
			continue
		}
		switch fields[0] {
		case "address":
			if len(fields) > 1 {
				cur.Addresses = []string{fields[1]}
			}
		case "gateway":
			if len(fields) > 1 {
				cur.Gateway = fields[1]
			}
		case "dns-nameservers":
			cur.Nameservers = append([]string{}, fields[1:]...)
		}
	}
}

func applyIfaceIfupdown(j *change.Journal, upd IfaceUpdate) (*change.Entry, error) {
	b, err := change.BackupFile(interfacesPath)
	if err != nil {
		return nil, err
	}
	backups := []change.FileBackup{b}
	data, err := priv.ReadFile(interfacesPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	updated := upsertIfupdownIface(string(data), upd)
	if err := priv.WriteFile(interfacesPath, []byte(updated), 0o644); err != nil {
		restoreAll(backups)
		return nil, err
	}
	if err := reloadIfupdown(upd.Name); err != nil {
		restoreAll(backups)
		_ = reloadIfupdown(upd.Name)
		return nil, fmt.Errorf("ifupdown reload failed (restored backup): %w", err)
	}
	return j.Record("network.iface", ifaceSummary(upd), backups, map[string]string{
		"iface":   upd.Name,
		"manager": string(ManagerIfupdown),
	})
}

func applyDNSIfupdown(j *change.Journal, upd IfaceUpdate) (*change.Entry, error) {
	b, err := change.BackupFile(interfacesPath)
	if err != nil {
		return nil, err
	}
	backups := []change.FileBackup{b}
	data, err := priv.ReadFile(interfacesPath)
	if err != nil {
		return nil, err
	}
	updated := upsertIfupdownDNS(string(data), upd.Name, upd.Nameservers)
	if err := priv.WriteFile(interfacesPath, []byte(updated), 0o644); err != nil {
		restoreAll(backups)
		return nil, err
	}
	if err := reloadIfupdown(upd.Name); err != nil {
		restoreAll(backups)
		_ = reloadIfupdown(upd.Name)
		return nil, fmt.Errorf("ifupdown reload failed (restored backup): %w", err)
	}
	return j.Record("network.dns",
		fmt.Sprintf("DNS on %s → %s", upd.Name, strings.Join(upd.Nameservers, ", ")),
		backups, map[string]string{"iface": upd.Name, "manager": string(ManagerIfupdown)})
}

func applyGatewayIfupdown(j *change.Journal, upd IfaceUpdate) (*change.Entry, error) {
	b, err := change.BackupFile(interfacesPath)
	if err != nil {
		return nil, err
	}
	backups := []change.FileBackup{b}
	data, err := priv.ReadFile(interfacesPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	updated := upsertIfupdownIface(string(data), upd)
	if err := priv.WriteFile(interfacesPath, []byte(updated), 0o644); err != nil {
		restoreAll(backups)
		return nil, err
	}
	if err := reloadIfupdown(upd.Name); err != nil {
		restoreAll(backups)
		_ = reloadIfupdown(upd.Name)
		return nil, fmt.Errorf("ifupdown reload failed (restored backup): %w", err)
	}
	return j.Record("network.gateway",
		fmt.Sprintf("gateway on %s → %s", upd.Name, upd.Gateway),
		backups, map[string]string{"iface": upd.Name, "manager": string(ManagerIfupdown)})
}

func upsertIfupdownIface(content string, upd IfaceUpdate) string {
	stanza := buildIfupdownStanza(upd)
	lines := strings.Split(content, "\n")
	var out []string
	skip := false
	replaced := false
	for i := 0; i < len(lines); i++ {
		fields := strings.Fields(strings.TrimSpace(lines[i]))
		if len(fields) >= 2 && (fields[0] == "auto" || fields[0] == "allow-hotplug") && fields[1] == upd.Name {
			continue
		}
		if len(fields) >= 2 && fields[0] == "iface" && fields[1] == upd.Name {
			skip = true
			if !replaced {
				out = append(out, strings.Split(strings.TrimRight(stanza, "\n"), "\n")...)
				replaced = true
			}
			continue
		}
		if skip {
			if len(fields) > 0 && (fields[0] == "iface" || fields[0] == "auto" || fields[0] == "allow-hotplug" || fields[0] == "mapping" || fields[0] == "source" || fields[0] == "source-directory") {
				skip = false
				i--
			}
			continue
		}
		out = append(out, lines[i])
	}
	if !replaced {
		if len(out) > 0 && out[len(out)-1] != "" {
			out = append(out, "")
		}
		out = append(out, strings.Split(strings.TrimRight(stanza, "\n"), "\n")...)
	}
	return strings.Join(out, "\n") + "\n"
}

func buildIfupdownStanza(upd IfaceUpdate) string {
	var b strings.Builder
	b.WriteString("auto " + upd.Name + "\n")
	if upd.DHCP4 {
		b.WriteString("iface " + upd.Name + " inet dhcp\n")
	} else {
		b.WriteString("iface " + upd.Name + " inet static\n")
		b.WriteString("    address " + strings.TrimSpace(upd.AddressCIDR) + "\n")
		if upd.Gateway != "" {
			b.WriteString("    gateway " + upd.Gateway + "\n")
		}
		if len(upd.Nameservers) > 0 {
			b.WriteString("    dns-nameservers " + strings.Join(upd.Nameservers, " ") + "\n")
		}
	}
	return b.String()
}

func upsertIfupdownDNS(content, iface string, dns []string) string {
	lines := strings.Split(content, "\n")
	var out []string
	inIface := false
	replaced := false
	for _, line := range lines {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) >= 2 && fields[0] == "iface" {
			inIface = fields[1] == iface
			out = append(out, line)
			continue
		}
		if inIface && len(fields) > 0 && (fields[0] == "iface" || fields[0] == "auto" || fields[0] == "allow-hotplug") {
			if !replaced {
				out = append(out, "    dns-nameservers "+strings.Join(dns, " "))
				replaced = true
			}
			inIface = false
		}
		if inIface && len(fields) > 0 && fields[0] == "dns-nameservers" {
			out = append(out, "    dns-nameservers "+strings.Join(dns, " "))
			replaced = true
			continue
		}
		out = append(out, line)
	}
	if inIface && !replaced {
		out = append(out, "    dns-nameservers "+strings.Join(dns, " "))
	}
	return strings.Join(out, "\n")
}

func reloadIfupdown(iface string) error {
	_ = priv.Run("ifdown", iface)
	if err := priv.Run("ifup", iface); err != nil {
		if err2 := priv.Run("systemctl", "restart", "networking"); err2 != nil {
			return fmt.Errorf("ifup %s: %v; networking restart: %v", iface, err, err2)
		}
	}
	return nil
}
