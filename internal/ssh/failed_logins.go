package ssh

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// FailedLogin is one failed SSH authentication attempt.
type FailedLogin struct {
	Time   string
	User   string
	IP     string
	Detail string
	Raw    string
}

var (
	reFailedPass  = regexp.MustCompile(`(?i)Failed password for (?:invalid user )?(\S+) from (\S+)(?: port (\d+))?`)
	reInvalidUser = regexp.MustCompile(`(?i)Invalid user (\S+) from (\S+)(?: port (\d+))?`)
	reAuthFail    = regexp.MustCompile(`(?i)authentication failure.*rhost=(\S+).*user=(\S+)`)
	rePreauth     = regexp.MustCompile(`(?i)Connection closed by authenticating user (\S+) (\S+) port (\d+)`)
	reTimestamp   = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}T[^\s]+|\w{3}\s+\d+\s+\d{2}:\d{2}:\d{2})`)
)

// LoadFailedLogins returns the newest failed SSH login attempts (newest last).
func LoadFailedLogins(limit int) []FailedLogin {
	if limit <= 0 {
		limit = 30
	}
	lines := collectAuthLines()
	var out []FailedLogin
	for _, line := range lines {
		if f, ok := parseFailedLine(line); ok {
			out = append(out, f)
		}
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

func collectAuthLines() []string {
	var lines []string
	for _, path := range []string{"/var/log/auth.log", "/var/log/secure"} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		lines = append(lines, strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")...)
		break
	}
	if len(lines) == 0 {
		cmd := exec.Command("journalctl", "-u", "ssh", "-u", "sshd", "-t", "sshd",
			"--no-pager", "-n", "500", "-o", "short-iso")
		out, err := cmd.CombinedOutput()
		if err == nil {
			lines = strings.Split(string(out), "\n")
		}
	}
	return lines
}

func parseFailedLine(line string) (FailedLogin, bool) {
	if line == "" || !strings.Contains(line, "sshd") {
		return FailedLogin{}, false
	}
	lower := strings.ToLower(line)
	interesting := strings.Contains(lower, "failed password") ||
		strings.Contains(lower, "invalid user") ||
		strings.Contains(lower, "authentication failure") ||
		strings.Contains(lower, "connection closed by authenticating")
	if !interesting {
		return FailedLogin{}, false
	}

	f := FailedLogin{Raw: line, Detail: "failed"}
	if m := reTimestamp.FindStringSubmatch(line); len(m) > 1 {
		f.Time = m[1]
	}

	switch {
	case reFailedPass.MatchString(line):
		m := reFailedPass.FindStringSubmatch(line)
		f.User, f.IP = m[1], m[2]
		f.Detail = "failed password"
	case reInvalidUser.MatchString(line):
		m := reInvalidUser.FindStringSubmatch(line)
		f.User, f.IP = m[1], m[2]
		f.Detail = "invalid user"
	case rePreauth.MatchString(line):
		m := rePreauth.FindStringSubmatch(line)
		f.User, f.IP = m[1], m[2]
		f.Detail = "preauth closed"
	case reAuthFail.MatchString(line):
		m := reAuthFail.FindStringSubmatch(line)
		f.IP, f.User = m[1], m[2]
		f.Detail = "auth failure"
	default:
		return FailedLogin{}, false
	}
	return f, true
}

// FormatFailedLogins renders attempts for the TUI (newest at bottom).
func FormatFailedLogins(attempts []FailedLogin) string {
	if len(attempts) == 0 {
		return "(no recent failed SSH logins found)"
	}
	var b strings.Builder
	for _, a := range attempts {
		user := a.User
		if user == "" {
			user = "?"
		}
		ip := a.IP
		if ip == "" {
			ip = "?"
		}
		ts := a.Time
		if ts == "" {
			ts = "-"
		}
		if i := strings.IndexByte(ts, '.'); i > 0 {
			ts = ts[:i]
		}
		if i := strings.IndexByte(ts, '+'); i > 0 {
			ts = ts[:i]
		}
		b.WriteString(ts)
		b.WriteString("  ")
		b.WriteString(user)
		b.WriteString("@")
		b.WriteString(ip)
		b.WriteString("  ")
		b.WriteString(a.Detail)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}
