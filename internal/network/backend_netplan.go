package network

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/AliZolfaghar/azlinuxadmin/internal/change"
	"gopkg.in/yaml.v3"
)

type netplanDoc struct {
	Path string
	Root map[string]any
}

func readAllNetplan() ([]netplanDoc, error) {
	ents, err := os.ReadDir(netplanDir)
	if err != nil {
		return nil, err
	}
	var docs []netplanDoc
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !(strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")) {
			continue
		}
		path := filepath.Join(netplanDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var root map[string]any
		if err := yaml.Unmarshal(data, &root); err != nil {
			continue
		}
		docs = append(docs, netplanDoc{Path: path, Root: root})
	}
	return docs, nil
}

func enrichFromNetplan(ifaces []Iface, docs []netplanDoc) {
	idx := map[string]*Iface{}
	for i := range ifaces {
		idx[ifaces[i].Name] = &ifaces[i]
	}
	for _, doc := range docs {
		netw, _ := doc.Root["network"].(map[string]any)
		if netw == nil {
			continue
		}
		for _, key := range []string{"ethernets", "wifis", "bridges", "bonds", "vlans"} {
			section, _ := netw[key].(map[string]any)
			for name, raw := range section {
				cfg, _ := raw.(map[string]any)
				ifc, ok := idx[name]
				if !ok || cfg == nil {
					continue
				}
				ifc.NetplanKey = key
				if v, ok := cfg["dhcp4"]; ok {
					b := asBool(v)
					ifc.DHCP4 = &b
				}
				ifc.Addresses = asStringSlice(cfg["addresses"])
				ifc.Gateway = gatewayFrom(cfg)
				if ns, _ := cfg["nameservers"].(map[string]any); ns != nil {
					ifc.Nameservers = asStringSlice(ns["addresses"])
				}
			}
		}
	}
}

func applyIfaceNetplan(j *change.Journal, upd IfaceUpdate) (*change.Entry, error) {
	backups, err := backupNetplanDir()
	if err != nil {
		return nil, err
	}
	if err := writeIfaceNetplan(upd); err != nil {
		restoreAll(backups)
		return nil, err
	}
	if err := netplanApply(); err != nil {
		restoreAll(backups)
		_ = netplanApply()
		return nil, fmt.Errorf("netplan apply failed (restored backup): %w", err)
	}
	return j.Record("network.iface", ifaceSummary(upd), backups, map[string]string{
		"iface":   upd.Name,
		"manager": string(ManagerNetplan),
	})
}

func applyDNSNetplan(j *change.Journal, upd IfaceUpdate) (*change.Entry, error) {
	backups, err := backupNetplanDir()
	if err != nil {
		return nil, err
	}
	if err := writeIfaceNetplan(upd); err != nil {
		restoreAll(backups)
		return nil, err
	}
	if err := netplanApply(); err != nil {
		restoreAll(backups)
		_ = netplanApply()
		return nil, fmt.Errorf("netplan apply failed (restored backup): %w", err)
	}
	return j.Record("network.dns",
		fmt.Sprintf("DNS on %s → %s", upd.Name, strings.Join(upd.Nameservers, ", ")),
		backups, map[string]string{"iface": upd.Name, "manager": string(ManagerNetplan)})
}

func applyGatewayNetplan(j *change.Journal, upd IfaceUpdate) (*change.Entry, error) {
	backups, err := backupNetplanDir()
	if err != nil {
		return nil, err
	}
	if err := writeIfaceNetplan(upd); err != nil {
		restoreAll(backups)
		return nil, err
	}
	if err := netplanApply(); err != nil {
		restoreAll(backups)
		_ = netplanApply()
		return nil, fmt.Errorf("netplan apply failed (restored backup): %w", err)
	}
	return j.Record("network.gateway",
		fmt.Sprintf("gateway on %s → %s", upd.Name, upd.Gateway),
		backups, map[string]string{"iface": upd.Name, "manager": string(ManagerNetplan)})
}

func backupNetplanDir() ([]change.FileBackup, error) {
	ents, err := os.ReadDir(netplanDir)
	if err != nil {
		return nil, err
	}
	var out []change.FileBackup
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !(strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")) {
			continue
		}
		b, err := change.BackupFile(filepath.Join(netplanDir, name))
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no netplan files found in %s", netplanDir)
	}
	return out, nil
}

func writeIfaceNetplan(upd IfaceUpdate) error {
	docs, err := readAllNetplan()
	if err != nil {
		return err
	}
	if len(docs) == 0 {
		return fmt.Errorf("cannot read netplan files (need root)")
	}

	targetIdx := 0
	sectionKey := "ethernets"
	found := false
	for i, doc := range docs {
		netw, _ := doc.Root["network"].(map[string]any)
		if netw == nil {
			continue
		}
		for _, key := range []string{"ethernets", "wifis", "bridges", "bonds", "vlans"} {
			section, _ := netw[key].(map[string]any)
			if section == nil {
				continue
			}
			if _, ok := section[upd.Name]; ok {
				targetIdx = i
				sectionKey = key
				found = true
				break
			}
		}
		if found {
			break
		}
	}

	doc := docs[targetIdx]
	netw, _ := doc.Root["network"].(map[string]any)
	if netw == nil {
		netw = map[string]any{"version": 2}
		doc.Root["network"] = netw
	}
	section, _ := netw[sectionKey].(map[string]any)
	if section == nil {
		section = map[string]any{}
		netw[sectionKey] = section
	}
	cfg, _ := section[upd.Name].(map[string]any)
	if cfg == nil {
		cfg = map[string]any{}
	}

	if upd.DHCP4 {
		cfg["dhcp4"] = true
		delete(cfg, "addresses")
		delete(cfg, "routes")
		delete(cfg, "gateway4")
	} else {
		cfg["dhcp4"] = false
		cfg["dhcp6"] = false
		cfg["addresses"] = []any{strings.TrimSpace(upd.AddressCIDR)}
		delete(cfg, "gateway4")
		if upd.Gateway != "" {
			cfg["routes"] = []any{
				map[string]any{"to": "default", "via": upd.Gateway},
			}
		}
	}
	if len(upd.Nameservers) > 0 {
		ns := make([]any, len(upd.Nameservers))
		for i, s := range upd.Nameservers {
			ns[i] = s
		}
		cfg["nameservers"] = map[string]any{"addresses": ns}
	}
	section[upd.Name] = cfg

	out, err := yaml.Marshal(doc.Root)
	if err != nil {
		return err
	}
	return os.WriteFile(doc.Path, out, 0o600)
}

func netplanApply() error {
	cmd := exec.Command("netplan", "apply")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

func asBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "true" || t == "yes"
	default:
		return false
	}
}

func asStringSlice(v any) []string {
	switch t := v.(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, x := range t {
			out = append(out, fmt.Sprint(x))
		}
		return out
	case []string:
		return append([]string{}, t...)
	default:
		return nil
	}
}

func gatewayFrom(cfg map[string]any) string {
	if g, ok := cfg["gateway4"].(string); ok {
		return g
	}
	routes := asMapSlice(cfg["routes"])
	for _, r := range routes {
		to := fmt.Sprint(r["to"])
		if to == "default" || to == "0.0.0.0/0" {
			if via, ok := r["via"].(string); ok {
				return via
			}
		}
	}
	return ""
}

func asMapSlice(v any) []map[string]any {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(arr))
	for _, x := range arr {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}
