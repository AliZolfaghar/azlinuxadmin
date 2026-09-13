package network

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Manager is the host network configuration backend we will use for applies.
type Manager string

const (
	ManagerNetworkManager Manager = "NetworkManager"
	ManagerNetplan        Manager = "netplan"
	ManagerNetworkd       Manager = "systemd-networkd"
	ManagerIfupdown       Manager = "ifupdown"
	ManagerUnknown        Manager = "unknown"
)

// Detection describes how this host manages network config.
type Detection struct {
	Manager  Manager
	Label    string // human-readable, shown in the UI first row
	Detail   string // e.g. renderer
	Writable bool
	Reason   string // why this manager was chosen / why not writable
}

// DetectManager inspects the host and picks the backend used for NIC/DNS changes.
//
// Priority:
//  1. NetworkManager (active + nmcli) when netplan is not managing config
//  2. netplan (Ubuntu/Debian with /etc/netplan)
//  3. systemd-networkd (direct .network units, no netplan)
//  4. ifupdown (/etc/network/interfaces)
func DetectManager() Detection {
	nmActive := serviceActive("NetworkManager")
	nmcliOK := lookPath("nmcli")
	hasNetplanCfg := dirHasNetplan()
	netplanOK := lookPath("netplan") && hasNetplanCfg
	networkdActive := serviceActive("systemd-networkd")
	hasIfupdown := fileExists("/etc/network/interfaces")

	// Netplan is the policy layer when present — even if renderer is NM or networkd.
	if netplanOK {
		renderer := netplanRenderer()
		if renderer == "" {
			// File may be root-only; infer from active services.
			if nmActive {
				renderer = "NetworkManager"
			} else if networkdActive {
				renderer = "networkd"
			}
		}
		label := "netplan"
		detail := ""
		if renderer != "" {
			label = fmt.Sprintf("netplan (%s)", renderer)
			detail = "renderer=" + renderer
		}
		return Detection{
			Manager:  ManagerNetplan,
			Label:    label,
			Detail:   detail,
			Writable: true,
			Reason:   "found /etc/netplan configuration",
		}
	}

	if nmActive && nmcliOK {
		return Detection{
			Manager:  ManagerNetworkManager,
			Label:    "NetworkManager",
			Writable: true,
			Reason:   "NetworkManager service active",
		}
	}

	if networkdActive {
		return Detection{
			Manager:  ManagerNetworkd,
			Label:    "systemd-networkd",
			Writable: true,
			Reason:   "systemd-networkd active (no netplan)",
		}
	}

	if hasIfupdown {
		return Detection{
			Manager:  ManagerIfupdown,
			Label:    "ifupdown",
			Writable: true,
			Reason:   "found /etc/network/interfaces",
		}
	}

	// Fallbacks with clearer messages
	if nmcliOK && !nmActive {
		return Detection{
			Manager:  ManagerUnknown,
			Label:    "unknown",
			Writable: false,
			Reason:   "nmcli present but NetworkManager is not active",
		}
	}

	return Detection{
		Manager:  ManagerUnknown,
		Label:    "unknown",
		Writable: false,
		Reason:   "no supported network manager detected",
	}
}

func lookPath(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func serviceActive(name string) bool {
	out, err := exec.Command("systemctl", "is-active", name).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "active"
}

func netplanRenderer() string {
	ents, err := os.ReadDir(netplanDir)
	if err != nil {
		return ""
	}
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !(strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(netplanDir, name))
		if err != nil {
			continue
		}
		// Lightweight scan — avoid full parse when unreadable elsewhere.
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "renderer:") {
				return strings.TrimSpace(strings.TrimPrefix(line, "renderer:"))
			}
		}
	}
	return ""
}

func dirHasNetplan() bool {
	ents, err := os.ReadDir(netplanDir)
	if err != nil {
		return false
	}
	for _, e := range ents {
		name := e.Name()
		if strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") {
			return true
		}
	}
	return false
}
