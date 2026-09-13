package ssh

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/AliZolfaghar/azlinuxadmin/internal/priv"
)

const (
	managedDropIn = "/etc/ssh/sshd_config.d/99-azlinuxadmin.conf"
	sshdConfig    = "/etc/ssh/sshd_config"
)

// SettingKind describes how a setting is edited in the UI.
type SettingKind int

const (
	KindBool SettingKind = iota
	KindEnum
	KindInt
)

// SettingDef is a common sshd option we manage.
type SettingDef struct {
	Key         string
	Label       string
	Kind        SettingKind
	EnumValues  []string // for KindEnum
	Description string
}

// CommonSettings are the sshd options exposed in the TUI.
var CommonSettings = []SettingDef{
	{Key: "permitrootlogin", Label: "PermitRootLogin", Kind: KindEnum,
		EnumValues:  []string{"yes", "prohibit-password", "no"},
		Description: "Allow root SSH login"},
	{Key: "passwordauthentication", Label: "PasswordAuthentication", Kind: KindBool,
		Description: "Allow password logins"},
	{Key: "pubkeyauthentication", Label: "PubkeyAuthentication", Kind: KindBool,
		Description: "Allow public-key logins"},
	{Key: "permitemptypasswords", Label: "PermitEmptyPasswords", Kind: KindBool,
		Description: "Allow accounts with empty passwords"},
	{Key: "kbdinteractiveauthentication", Label: "KbdInteractiveAuthentication", Kind: KindBool,
		Description: "Keyboard-interactive auth (often used with PAM)"},
	{Key: "x11forwarding", Label: "X11Forwarding", Kind: KindBool,
		Description: "Allow X11 forwarding"},
	{Key: "gatewayports", Label: "GatewayPorts", Kind: KindEnum,
		EnumValues:  []string{"no", "yes", "clientspecified"},
		Description: "Allow remote hosts to connect to forwarded ports"},
	{Key: "maxauthtries", Label: "MaxAuthTries", Kind: KindInt,
		Description: "Max authentication attempts per connection"},
	{Key: "clientaliveinterval", Label: "ClientAliveInterval", Kind: KindInt,
		Description: "Seconds between keepalive probes (0=off)"},
	{Key: "clientalivecountmax", Label: "ClientAliveCountMax", Kind: KindInt,
		Description: "Keepalive probes before disconnect"},
	{Key: "port", Label: "Port", Kind: KindInt,
		Description: "SSH listen port"},
}

// Snapshot is the effective SSH config view for the UI.
type Snapshot struct {
	Values  map[string]string // lowercase key → value
	Managed map[string]string // values currently in our drop-in
	Warning string
}

// LoadSnapshot reads effective sshd settings (sshd -T) and our managed drop-in.
func LoadSnapshot() Snapshot {
	s := Snapshot{
		Values:  map[string]string{},
		Managed: map[string]string{},
	}
	effective, err := readEffective()
	if err != nil {
		s.Warning = err.Error()
		// Fall back to parsing config files for best-effort display.
		effective = parseConfigFiles()
	}
	for _, def := range CommonSettings {
		if v, ok := effective[def.Key]; ok {
			s.Values[def.Key] = normalizeValue(def, v)
		} else {
			s.Values[def.Key] = defaultValue(def)
		}
	}
	s.Managed = parseManagedFile()
	return s
}

func normalizeValue(def SettingDef, v string) string {
	v = strings.TrimSpace(strings.ToLower(v))
	switch def.Kind {
	case KindBool:
		if v == "true" || v == "yes" || v == "1" {
			return "yes"
		}
		return "no"
	case KindEnum:
		// Map legacy without-password → prohibit-password
		if v == "without-password" {
			return "prohibit-password"
		}
		return v
	default:
		return v
	}
}

func defaultValue(def SettingDef) string {
	switch def.Key {
	case "permitrootlogin":
		return "prohibit-password"
	case "passwordauthentication", "pubkeyauthentication", "kbdinteractiveauthentication":
		return "yes"
	case "permitemptypasswords", "x11forwarding":
		return "no"
	case "gatewayports":
		return "no"
	case "maxauthtries":
		return "6"
	case "clientaliveinterval":
		return "0"
	case "clientalivecountmax":
		return "3"
	case "port":
		return "22"
	default:
		return ""
	}
}

func readEffective() (map[string]string, error) {
	cmd := exec.Command("sshd", "-T")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("sshd -T: %s (need root for full view)", msg)
	}
	m := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		m[strings.ToLower(fields[0])] = strings.Join(fields[1:], " ")
	}
	return m, nil
}

func parseConfigFiles() map[string]string {
	m := map[string]string{}
	// Main file then drop-ins alphabetically — last wins for our simple parser.
	parseFileInto(sshdConfig, m)
	ents, err := os.ReadDir("/etc/ssh/sshd_config.d")
	if err == nil {
		for _, e := range ents {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".conf") {
				continue
			}
			parseFileInto("/etc/ssh/sshd_config.d/"+name, m)
		}
	}
	return m
}

func parseFileInto(path string, m map[string]string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := strings.ToLower(fields[0])
		if key == "include" {
			continue
		}
		m[key] = strings.Join(fields[1:], " ")
	}
}

func parseManagedFile() map[string]string {
	m := map[string]string{}
	parseFileInto(managedDropIn, m)
	return m
}

// RequireRoot returns an error unless privileged ops can run (root or sudo session).
func RequireRoot() error {
	return priv.Ensure()
}

// ValidateDraft checks draft values before apply.
func ValidateDraft(draft map[string]string) error {
	for _, def := range CommonSettings {
		v, ok := draft[def.Key]
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch def.Kind {
		case KindBool:
			if v != "yes" && v != "no" {
				return fmt.Errorf("%s must be yes or no", def.Label)
			}
		case KindEnum:
			found := false
			for _, ev := range def.EnumValues {
				if v == ev {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("%s must be one of %s", def.Label, strings.Join(def.EnumValues, "|"))
			}
		case KindInt:
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return fmt.Errorf("%s must be a non-negative integer", def.Label)
			}
			if def.Key == "port" && (n < 1 || n > 65535) {
				return fmt.Errorf("Port must be 1-65535")
			}
		}
	}
	return nil
}

// CanonicalKey returns the OpenSSH config keyword casing for writing.
func CanonicalKey(key string) string {
	for _, def := range CommonSettings {
		if def.Key == key {
			return def.Label
		}
	}
	return key
}
