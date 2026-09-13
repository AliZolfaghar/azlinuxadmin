package fail2ban

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/AliZolfaghar/azlinuxadmin/internal/change"
	"github.com/AliZolfaghar/azlinuxadmin/internal/priv"
)

const (
	jailLocalPath = "/etc/fail2ban/jail.local"
	logPath       = "/var/log/fail2ban.log"
	pkgName       = "fail2ban"
)

// Snapshot is the Fail2Ban status for the UI.
type Snapshot struct {
	Installed bool
	Running   bool
	Enabled   bool // systemd enabled
	Version   string
	Config    Config
	LogTail   string
	Warning   string
}

// Config holds common jail.local settings we manage.
type Config struct {
	SSHEnabled bool
	Bantime    string // e.g. 1h, 3600
	Findtime   string
	MaxRetry   int
	IgnoreIP   string
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		SSHEnabled: true,
		Bantime:    "1h",
		Findtime:   "10m",
		MaxRetry:   5,
		IgnoreIP:   "127.0.0.1/8 ::1",
	}
}

// LoadSnapshot gathers install/service/config/log state.
func LoadSnapshot(logLines int) Snapshot {
	if logLines <= 0 {
		logLines = 80
	}
	s := Snapshot{Config: DefaultConfig()}
	s.Installed = isInstalled()
	if s.Installed {
		s.Version = version()
		s.Running = serviceActive()
		s.Enabled = serviceEnabled()
		if cfg, err := readConfig(); err == nil {
			s.Config = cfg
		} else if err != nil {
			s.Warning = err.Error()
		}
	}
	s.LogTail = readLogTail(logLines)
	return s
}

func isInstalled() bool {
	if _, err := exec.LookPath("fail2ban-client"); err == nil {
		return true
	}
	out, err := exec.Command("dpkg-query", "-W", "-f=${Status}", pkgName).Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "install ok installed")
}

func version() string {
	out, err := exec.Command("fail2ban-client", "version").CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func serviceActive() bool {
	out, err := exec.Command("systemctl", "is-active", "fail2ban").Output()
	return err == nil && strings.TrimSpace(string(out)) == "active"
}

func serviceEnabled() bool {
	return exec.Command("systemctl", "is-enabled", "fail2ban").Run() == nil
}

func readConfig() (Config, error) {
	cfg := DefaultConfig()
	data, err := os.ReadFile(jailLocalPath)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	section := ""
	for _, line := range strings.Split(string(data), "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") || strings.HasPrefix(trim, ";") {
			continue
		}
		if strings.HasPrefix(trim, "[") && strings.HasSuffix(trim, "]") {
			section = strings.ToLower(strings.Trim(trim, "[]"))
			continue
		}
		parts := strings.SplitN(trim, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		val := strings.TrimSpace(parts[1])
		switch section {
		case "default":
			switch key {
			case "bantime":
				cfg.Bantime = val
			case "findtime":
				cfg.Findtime = val
			case "maxretry":
				if n, err := strconv.Atoi(val); err == nil {
					cfg.MaxRetry = n
				}
			case "ignoreip":
				cfg.IgnoreIP = val
			}
		case "sshd", "ssh":
			if key == "enabled" {
				cfg.SSHEnabled = strings.EqualFold(val, "true")
			}
		}
	}
	return cfg, nil
}

func readLogTail(n int) string {
	paths := []string{logPath, "/var/log/fail2ban.log.1"}
	var data []byte
	var err error
	for _, p := range paths {
		data, err = os.ReadFile(p)
		if err == nil {
			break
		}
	}
	if err != nil {
		if !isInstalled() {
			return "(fail2ban not installed — log will appear after install)"
		}
		return "(no fail2ban log yet — start the service to generate logs)"
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// Install installs fail2ban via apt and enables the service.
func Install(j *change.Journal) (*change.Entry, error) {
	if err := priv.Ensure(); err != nil {
		return nil, err
	}
	if isInstalled() {
		return nil, fmt.Errorf("fail2ban is already installed")
	}

	// Lightweight marker backup so undo knows we installed it.
	marker := filepath.Join(j.Dir, "backups", "fail2ban-was-missing")
	_ = os.MkdirAll(filepath.Dir(marker), 0o750)
	_ = os.WriteFile(marker, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o640)
	mb, _ := change.BackupFile(marker)

	if err := priv.Run("apt-get", "update"); err != nil {
		return nil, fmt.Errorf("apt-get update: %w", err)
	}
	if err := priv.Run("apt-get", "install", "-y", pkgName); err != nil {
		return nil, err
	}
	_ = priv.Run("systemctl", "enable", "fail2ban")
	_ = priv.Run("systemctl", "start", "fail2ban")

	// Apply default managed config if jail.local missing.
	if _, err := os.Stat(jailLocalPath); os.IsNotExist(err) {
		_ = writeJailLocal(DefaultConfig())
		_ = priv.Run("systemctl", "reload", "fail2ban")
	}

	return j.Record("fail2ban.install", "install fail2ban", []change.FileBackup{mb}, map[string]string{
		"action":  "install",
		"package": pkgName,
		"marker":  marker,
	})
}

// ApplyConfig writes jail.local, validates/reloads, journals undo.
func ApplyConfig(j *change.Journal, cfg Config) (*change.Entry, error) {
	if err := priv.Ensure(); err != nil {
		return nil, err
	}
	if !isInstalled() {
		return nil, fmt.Errorf("fail2ban is not installed — install first")
	}
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	backups := []change.FileBackup{}
	b, err := change.BackupFile(jailLocalPath)
	if err != nil {
		return nil, err
	}
	backups = append(backups, b)

	if err := writeJailLocal(cfg); err != nil {
		restoreAll(backups)
		return nil, err
	}
	// fail2ban-client reload or systemctl reload
	if err := priv.Run("fail2ban-client", "reload"); err != nil {
		_ = priv.Run("systemctl", "reload", "fail2ban")
		if err2 := priv.Run("fail2ban-client", "ping"); err2 != nil {
			restoreAll(backups)
			_ = priv.Run("fail2ban-client", "reload")
			return nil, fmt.Errorf("reload failed (restored backup): %v", err)
		}
	}

	summary := fmt.Sprintf("fail2ban config ssh=%v bantime=%s maxretry=%d", cfg.SSHEnabled, cfg.Bantime, cfg.MaxRetry)
	return j.Record("fail2ban.config", summary, backups, map[string]string{
		"action": "config",
		"file":   jailLocalPath,
	})
}

// SetRunning starts or stops fail2ban.
func SetRunning(j *change.Journal, start bool) (*change.Entry, error) {
	if err := priv.Ensure(); err != nil {
		return nil, err
	}
	if !isInstalled() {
		return nil, fmt.Errorf("fail2ban is not installed")
	}
	was := serviceActive()
	action := "start"
	if !start {
		action = "stop"
	}
	if err := priv.Run("systemctl", action, "fail2ban"); err != nil {
		return nil, err
	}
	return j.Record("fail2ban."+action, action+" fail2ban", []change.FileBackup{}, map[string]string{
		"action": action,
		"was":    strconv.FormatBool(was),
	})
}

// UndoChange reverses a fail2ban journal entry.
func UndoChange(j *change.Journal, id string) (*change.Entry, error) {
	if err := priv.Ensure(); err != nil {
		return nil, err
	}
	// Use journal.Undo for file restores; handle package/service specially.
	e, err := j.Undo(id)
	if err != nil {
		return nil, err
	}
	switch e.Kind {
	case "fail2ban.install":
		_ = priv.Run("systemctl", "stop", "fail2ban")
		_ = priv.Run("systemctl", "disable", "fail2ban")
		if err := priv.Run("apt-get", "remove", "-y", pkgName); err != nil {
			return e, fmt.Errorf("files restored but package remove failed: %w", err)
		}
	case "fail2ban.config":
		_ = priv.Run("fail2ban-client", "reload")
	case "fail2ban.start":
		_ = priv.Run("systemctl", "stop", "fail2ban")
	case "fail2ban.stop":
		_ = priv.Run("systemctl", "start", "fail2ban")
	}
	return e, nil
}

func validateConfig(cfg Config) error {
	if cfg.MaxRetry < 1 || cfg.MaxRetry > 100 {
		return fmt.Errorf("maxretry must be 1-100")
	}
	if strings.TrimSpace(cfg.Bantime) == "" || strings.TrimSpace(cfg.Findtime) == "" {
		return fmt.Errorf("bantime and findtime are required")
	}
	return nil
}

func writeJailLocal(cfg Config) error {
	var b strings.Builder
	b.WriteString("# Managed by AZLinuxAdmin — do not edit manually\n")
	b.WriteString("# Undo via the Fail2Ban page history.\n\n")
	b.WriteString("[DEFAULT]\n")
	b.WriteString("bantime = " + cfg.Bantime + "\n")
	b.WriteString("findtime = " + cfg.Findtime + "\n")
	b.WriteString("maxretry = " + strconv.Itoa(cfg.MaxRetry) + "\n")
	if strings.TrimSpace(cfg.IgnoreIP) != "" {
		b.WriteString("ignoreip = " + cfg.IgnoreIP + "\n")
	}
	b.WriteString("\n[sshd]\n")
	if cfg.SSHEnabled {
		b.WriteString("enabled = true\n")
	} else {
		b.WriteString("enabled = false\n")
	}
	if err := os.MkdirAll(filepath.Dir(jailLocalPath), 0o755); err != nil {
		return err
	}
	return priv.WriteFile(jailLocalPath, []byte(b.String()), 0o644)
}

func restoreAll(backups []change.FileBackup) {
	for i := len(backups) - 1; i >= 0; i-- {
		_ = change.Restore(backups[i])
	}
}

// BannedIPs returns currently banned IPs for sshd if available.
func BannedIPs() []string {
	out, err := exec.Command("fail2ban-client", "status", "sshd").CombinedOutput()
	if err != nil {
		return nil
	}
	var ips []string
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, "Banned IP list:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				ips = strings.Fields(parts[1])
			}
		}
	}
	return ips
}
