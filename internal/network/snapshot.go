package network

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"

	"github.com/AliZolfaghar/azlinuxadmin/internal/change"
)

const (
	hostnamePath = "/etc/hostname"
	hostsPath    = "/etc/hosts"
	netplanDir   = "/etc/netplan"
)

// Snapshot is the live network view for the UI.
type Snapshot struct {
	Hostname     string
	Interfaces   []Iface
	DNS          []string
	Gateway      string // default IPv4 gateway
	GatewayIface string // interface used by the default route, when known
	Detection    Detection
	Warning      string
}

// Iface is a network interface summary.
type Iface struct {
	Name        string
	State       string
	MAC         string
	IPv4        []string
	IPv6        []string
	DHCP4       *bool
	Addresses   []string
	Gateway     string
	Nameservers []string
	NetplanKey  string
	NMConn      string // NetworkManager connection id/name when applicable
}

// IfaceUpdate is a desired static/DHCP configuration for one NIC.
type IfaceUpdate struct {
	Name        string
	DHCP4       bool
	AddressCIDR string
	Gateway     string
	Nameservers []string
}

// LoadSnapshot gathers hostname, interfaces, DNS, and manager detection.
func LoadSnapshot() Snapshot {
	det := DetectManager()
	s := Snapshot{
		Hostname:  currentHostname(),
		Detection: det,
	}
	ifaces, err := listIfaces()
	if err != nil {
		s.Warning = err.Error()
	} else {
		s.Interfaces = ifaces
	}

	switch det.Manager {
	case ManagerNetplan:
		if docs, err := readAllNetplan(); err == nil {
			enrichFromNetplan(s.Interfaces, docs)
		}
	case ManagerNetworkManager:
		enrichFromNM(s.Interfaces)
	case ManagerNetworkd:
		enrichFromNetworkd(s.Interfaces)
	case ManagerIfupdown:
		enrichFromIfupdown(s.Interfaces)
	}

	dns, err := loadDNS(s.Interfaces)
	if err != nil && s.Warning == "" {
		s.Warning = err.Error()
	}
	s.DNS = dns
	s.Gateway, s.GatewayIface = loadDefaultGateway(s.Interfaces)

	if !det.Writable && s.Warning == "" {
		s.Warning = det.Reason
	}
	return s
}

func currentHostname() string {
	if b, err := os.ReadFile(hostnamePath); err == nil {
		if h := strings.TrimSpace(string(b)); h != "" {
			return h
		}
	}
	h, _ := os.Hostname()
	return h
}

func listIfaces() ([]Iface, error) {
	out, err := exec.Command("ip", "-br", "link").Output()
	if err != nil {
		return nil, fmt.Errorf("ip link: %w", err)
	}
	addrOut, _ := exec.Command("ip", "-br", "addr").Output()
	addrMap := parseAddrBR(string(addrOut))

	var ifaces []Iface
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		if i := strings.IndexByte(name, '@'); i >= 0 {
			name = name[:i]
		}
		if name == "lo" {
			continue
		}
		ifc := Iface{Name: name, State: fields[1]}
		if addrs, ok := addrMap[name]; ok {
			for _, a := range addrs {
				if strings.Contains(a, ":") {
					ifc.IPv6 = append(ifc.IPv6, a)
				} else {
					ifc.IPv4 = append(ifc.IPv4, a)
				}
			}
		}
		ifaces = append(ifaces, ifc)
	}
	return ifaces, nil
}

func parseAddrBR(s string) map[string][]string {
	m := make(map[string][]string)
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		name := fields[0]
		if i := strings.IndexByte(name, '@'); i >= 0 {
			name = name[:i]
		}
		m[name] = append([]string{}, fields[2:]...)
	}
	return m
}

func loadDNS(ifaces []Iface) ([]string, error) {
	for _, ifc := range ifaces {
		if len(ifc.Nameservers) > 0 {
			return append([]string{}, ifc.Nameservers...), nil
		}
	}
	data, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return nil, err
	}
	var dns []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "nameserver ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				dns = append(dns, parts[1])
			}
		}
	}
	return dns, nil
}

func loadDefaultGateway(ifaces []Iface) (gw, iface string) {
	// Prefer live routing table.
	out, err := exec.Command("ip", "-4", "route", "show", "default").Output()
	if err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			fields := strings.Fields(line)
			// default via 1.2.3.4 dev eth0 ...
			for i := 0; i+1 < len(fields); i++ {
				if fields[i] == "via" {
					gw = fields[i+1]
				}
				if fields[i] == "dev" {
					iface = fields[i+1]
				}
			}
			if gw != "" {
				return gw, iface
			}
		}
	}
	// Fall back to configured interface gateway.
	for _, ifc := range ifaces {
		if ifc.Gateway != "" {
			return ifc.Gateway, ifc.Name
		}
	}
	return "", ""
}

// RequireRoot returns an error unless running as root.
func RequireRoot() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("root required to apply network changes (re-run with sudo)")
	}
	return nil
}

func validateIfaceUpdate(upd *IfaceUpdate) error {
	upd.Name = strings.TrimSpace(upd.Name)
	if upd.Name == "" {
		return fmt.Errorf("interface name required")
	}
	if !upd.DHCP4 {
		if _, n, err := net.ParseCIDR(strings.TrimSpace(upd.AddressCIDR)); err != nil || n == nil {
			return fmt.Errorf("invalid address CIDR %q", upd.AddressCIDR)
		}
		if upd.Gateway != "" && net.ParseIP(upd.Gateway) == nil {
			return fmt.Errorf("invalid gateway %q", upd.Gateway)
		}
	}
	for _, ns := range upd.Nameservers {
		if net.ParseIP(ns) == nil {
			return fmt.Errorf("invalid DNS %q", ns)
		}
	}
	return nil
}

func cleanDNSList(nameservers []string) ([]string, error) {
	var clean []string
	for _, ns := range nameservers {
		ns = strings.TrimSpace(ns)
		if ns == "" {
			continue
		}
		if net.ParseIP(ns) == nil {
			return nil, fmt.Errorf("invalid DNS %q", ns)
		}
		clean = append(clean, ns)
	}
	if len(clean) == 0 {
		return nil, fmt.Errorf("at least one DNS server required")
	}
	return clean, nil
}

func ifaceUpdateFromSnap(iface string, nameservers []string) (IfaceUpdate, error) {
	snap := LoadSnapshot()
	if iface == "" {
		if len(snap.Interfaces) == 0 {
			return IfaceUpdate{}, fmt.Errorf("no interfaces found")
		}
		iface = snap.Interfaces[0].Name
	}
	var target *Iface
	for i := range snap.Interfaces {
		if snap.Interfaces[i].Name == iface {
			target = &snap.Interfaces[i]
			break
		}
	}
	if target == nil {
		return IfaceUpdate{}, fmt.Errorf("interface %s not found", iface)
	}
	upd := IfaceUpdate{Name: iface, Nameservers: nameservers}
	if target.DHCP4 != nil && *target.DHCP4 {
		upd.DHCP4 = true
		return upd, nil
	}
	if len(target.Addresses) > 0 {
		upd.AddressCIDR = target.Addresses[0]
		upd.Gateway = target.Gateway
		return upd, nil
	}
	if len(target.IPv4) > 0 {
		upd.AddressCIDR = target.IPv4[0]
		upd.Gateway = target.Gateway
		return upd, nil
	}
	return IfaceUpdate{}, fmt.Errorf("cannot determine addressing for %s; configure NIC first", iface)
}

// ApplyIfaceConfig applies NIC config via the detected manager.
func ApplyIfaceConfig(j *change.Journal, upd IfaceUpdate) (*change.Entry, error) {
	if err := RequireRoot(); err != nil {
		return nil, err
	}
	if err := validateIfaceUpdate(&upd); err != nil {
		return nil, err
	}
	det := DetectManager()
	if !det.Writable {
		return nil, fmt.Errorf("cannot apply: %s", det.Reason)
	}
	switch det.Manager {
	case ManagerNetplan:
		return applyIfaceNetplan(j, upd)
	case ManagerNetworkManager:
		return applyIfaceNM(j, upd)
	case ManagerNetworkd:
		return applyIfaceNetworkd(j, upd)
	case ManagerIfupdown:
		return applyIfaceIfupdown(j, upd)
	default:
		return nil, fmt.Errorf("unsupported network manager %q", det.Manager)
	}
}

// ApplyDNS applies DNS via the detected manager.
func ApplyDNS(j *change.Journal, iface string, nameservers []string) (*change.Entry, error) {
	if err := RequireRoot(); err != nil {
		return nil, err
	}
	clean, err := cleanDNSList(nameservers)
	if err != nil {
		return nil, err
	}
	det := DetectManager()
	if !det.Writable {
		return nil, fmt.Errorf("cannot apply: %s", det.Reason)
	}
	upd, err := ifaceUpdateFromSnap(iface, clean)
	if err != nil {
		return nil, err
	}
	switch det.Manager {
	case ManagerNetplan:
		return applyDNSNetplan(j, upd)
	case ManagerNetworkManager:
		return applyDNSNM(j, upd)
	case ManagerNetworkd:
		return applyDNSNetworkd(j, upd)
	case ManagerIfupdown:
		return applyDNSIfupdown(j, upd)
	default:
		return nil, fmt.Errorf("unsupported network manager %q", det.Manager)
	}
}

// ApplyGateway sets the default gateway on the primary (or named) interface.
func ApplyGateway(j *change.Journal, iface, gateway string) (*change.Entry, error) {
	if err := RequireRoot(); err != nil {
		return nil, err
	}
	gateway = strings.TrimSpace(gateway)
	if gateway == "" || net.ParseIP(gateway) == nil {
		return nil, fmt.Errorf("invalid gateway %q", gateway)
	}
	det := DetectManager()
	if !det.Writable {
		return nil, fmt.Errorf("cannot apply: %s", det.Reason)
	}

	snap := LoadSnapshot()
	if iface == "" {
		iface = snap.GatewayIface
	}
	if iface == "" && len(snap.Interfaces) > 0 {
		iface = snap.Interfaces[0].Name
	}
	if iface == "" {
		return nil, fmt.Errorf("no interface found for gateway")
	}

	var target *Iface
	for i := range snap.Interfaces {
		if snap.Interfaces[i].Name == iface {
			target = &snap.Interfaces[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("interface %s not found", iface)
	}
	if target.DHCP4 != nil && *target.DHCP4 {
		return nil, fmt.Errorf("%s uses DHCP; switch to static in Interfaces first", iface)
	}

	upd := IfaceUpdate{
		Name:        iface,
		Gateway:     gateway,
		Nameservers: target.Nameservers,
	}
	if len(upd.Nameservers) == 0 {
		upd.Nameservers = snap.DNS
	}
	if len(target.Addresses) > 0 {
		upd.AddressCIDR = target.Addresses[0]
	} else if len(target.IPv4) > 0 {
		upd.AddressCIDR = target.IPv4[0]
	} else {
		return nil, fmt.Errorf("cannot determine address for %s; configure NIC first", iface)
	}

	switch det.Manager {
	case ManagerNetplan:
		return applyGatewayNetplan(j, upd)
	case ManagerNetworkManager:
		return applyGatewayNM(j, upd)
	case ManagerNetworkd:
		return applyGatewayNetworkd(j, upd)
	case ManagerIfupdown:
		return applyGatewayIfupdown(j, upd)
	default:
		return nil, fmt.Errorf("unsupported network manager %q", det.Manager)
	}
}

func restoreAll(backups []change.FileBackup) {
	for i := len(backups) - 1; i >= 0; i-- {
		_ = change.Restore(backups[i])
	}
}

func ifaceSummary(upd IfaceUpdate) string {
	if upd.DHCP4 {
		return fmt.Sprintf("iface %s dhcp4=true", upd.Name)
	}
	return fmt.Sprintf("iface %s %s via %s", upd.Name, upd.AddressCIDR, upd.Gateway)
}
