package network

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/AliZolfaghar/azlinuxadmin/internal/change"
	"github.com/AliZolfaghar/azlinuxadmin/internal/priv"
)

func enrichFromNM(ifaces []Iface) {
	for i := range ifaces {
		ifc := &ifaces[i]
		out, err := exec.Command("nmcli", "-g", "GENERAL.CONNECTION", "device", "show", ifc.Name).Output()
		if err != nil {
			continue
		}
		conn := strings.TrimSpace(string(out))
		if conn == "" || conn == "--" {
			continue
		}
		ifc.NMConn = conn

		method, _ := nmcliField(conn, "ipv4.method")
		dhcp := method == "auto"
		ifc.DHCP4 = &dhcp
		if addrs, err := nmcliField(conn, "ipv4.addresses"); err == nil && addrs != "" {
			ifc.Addresses = splitNMList(addrs)
		}
		if gw, err := nmcliField(conn, "ipv4.gateway"); err == nil {
			ifc.Gateway = gw
		}
		if dns, err := nmcliField(conn, "ipv4.dns"); err == nil && dns != "" {
			ifc.Nameservers = splitNMList(dns)
		}
	}
}

func nmcliField(conn, field string) (string, error) {
	out, err := exec.Command("nmcli", "-g", field, "connection", "show", conn).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func splitNMList(s string) []string {
	s = strings.ReplaceAll(s, ",", " ")
	return strings.Fields(s)
}

func applyIfaceNM(j *change.Journal, upd IfaceUpdate) (*change.Entry, error) {
	conn, backups, err := backupNMConnection(upd.Name)
	if err != nil {
		return nil, err
	}

	args := []string{"connection", "modify", conn}
	if upd.DHCP4 {
		args = append(args, "ipv4.method", "auto", "ipv4.addresses", "", "ipv4.gateway", "")
	} else {
		args = append(args, "ipv4.method", "manual", "ipv4.addresses", upd.AddressCIDR)
		if upd.Gateway != "" {
			args = append(args, "ipv4.gateway", upd.Gateway)
		}
	}
	if len(upd.Nameservers) > 0 {
		args = append(args, "ipv4.dns", strings.Join(upd.Nameservers, ","))
	}

	if err := runNM(args...); err != nil {
		restoreAll(backups)
		_ = priv.Run("nmcli", "connection", "reload")
		return nil, err
	}
	if err := runNM("device", "reapply", upd.Name); err != nil {
		// Fallback: up the connection
		if err2 := runNM("connection", "up", conn); err2 != nil {
			restoreAll(backups)
			_ = priv.Run("nmcli", "connection", "reload")
			_ = priv.Run("nmcli", "connection", "up", conn)
			return nil, fmt.Errorf("%v; reapply also failed: %v", err, err2)
		}
	}

	return j.Record("network.iface", ifaceSummary(upd), backups, map[string]string{
		"iface":   upd.Name,
		"manager": string(ManagerNetworkManager),
		"conn":    conn,
	})
}

func applyDNSNM(j *change.Journal, upd IfaceUpdate) (*change.Entry, error) {
	conn, backups, err := backupNMConnection(upd.Name)
	if err != nil {
		return nil, err
	}
	if err := runNM("connection", "modify", conn, "ipv4.dns", strings.Join(upd.Nameservers, ",")); err != nil {
		restoreAll(backups)
		_ = priv.Run("nmcli", "connection", "reload")
		return nil, err
	}
	_ = runNM("device", "reapply", upd.Name)
	_ = runNM("connection", "up", conn)

	return j.Record("network.dns",
		fmt.Sprintf("DNS on %s → %s", upd.Name, strings.Join(upd.Nameservers, ", ")),
		backups, map[string]string{"iface": upd.Name, "manager": string(ManagerNetworkManager), "conn": conn})
}

func applyGatewayNM(j *change.Journal, upd IfaceUpdate) (*change.Entry, error) {
	conn, backups, err := backupNMConnection(upd.Name)
	if err != nil {
		return nil, err
	}
	if err := runNM("connection", "modify", conn, "ipv4.method", "manual", "ipv4.gateway", upd.Gateway); err != nil {
		restoreAll(backups)
		_ = priv.Run("nmcli", "connection", "reload")
		return nil, err
	}
	if upd.AddressCIDR != "" {
		_ = runNM("connection", "modify", conn, "ipv4.addresses", upd.AddressCIDR)
	}
	_ = runNM("device", "reapply", upd.Name)
	_ = runNM("connection", "up", conn)

	return j.Record("network.gateway",
		fmt.Sprintf("gateway on %s → %s", upd.Name, upd.Gateway),
		backups, map[string]string{"iface": upd.Name, "manager": string(ManagerNetworkManager), "conn": conn})
}

func backupNMConnection(iface string) (conn string, backups []change.FileBackup, err error) {
	out, err := exec.Command("nmcli", "-g", "GENERAL.CONNECTION", "device", "show", iface).Output()
	if err != nil {
		return "", nil, fmt.Errorf("nmcli device show %s: %w", iface, err)
	}
	conn = strings.TrimSpace(string(out))
	if conn == "" || conn == "--" {
		return "", nil, fmt.Errorf("no NetworkManager connection for %s", iface)
	}
	uuid, _ := nmcliField(conn, "connection.uuid")
	path := findNMKeyfile(conn, uuid)
	if path == "" {
		return "", nil, fmt.Errorf("could not find keyfile for connection %q", conn)
	}
	b, err := change.BackupFile(path)
	if err != nil {
		return "", nil, err
	}
	return conn, []change.FileBackup{b}, nil
}

func findNMKeyfile(conn, uuid string) string {
	dirs := []string{
		"/etc/NetworkManager/system-connections",
		"/run/NetworkManager/system-connections",
	}
	for _, dir := range dirs {
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range ents {
			path := filepath.Join(dir, e.Name())
			data, err := priv.ReadFile(path)
			if err != nil {
				continue
			}
			text := string(data)
			if uuid != "" && strings.Contains(text, "uuid="+uuid) {
				return path
			}
			if strings.Contains(text, "id="+conn) {
				return path
			}
		}
		// Common filename patterns
		candidates := []string{
			filepath.Join(dir, conn+".nmconnection"),
			filepath.Join(dir, conn),
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				return c
			}
		}
	}
	return ""
}

func runNM(args ...string) error {
	return priv.Run("nmcli", args...)
}
