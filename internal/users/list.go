package users

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Account is a local Linux user summary for the UI.
type Account struct {
	Name     string
	UID      int
	GID      int
	GECOS    string
	Home     string
	Shell    string
	Locked   bool
	Expired  bool
	IsSystem bool // UID < 1000 (except we still list them optionally)
}

// Snapshot is the users view for the UI.
type Snapshot struct {
	Accounts []Account
	Warning  string
}

// LoadSnapshot lists local users from passwd/shadow.
func LoadSnapshot() Snapshot {
	accts, err := listAccounts()
	s := Snapshot{Accounts: accts}
	if err != nil {
		s.Warning = err.Error()
	}
	return s
}

func listAccounts() ([]Account, error) {
	f, err := os.Open("/etc/passwd")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	shadow := readShadowStatus()

	var out []Account
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) < 7 {
			continue
		}
		uid, _ := strconv.Atoi(parts[2])
		gid, _ := strconv.Atoi(parts[3])
		name := parts[0]
		a := Account{
			Name:     name,
			UID:      uid,
			GID:      gid,
			GECOS:    parts[4],
			Home:     parts[5],
			Shell:    parts[6],
			IsSystem: uid < 1000 && name != "root",
		}
		if st, ok := shadow[name]; ok {
			a.Locked = st.locked
			a.Expired = st.expired
		}
		out = append(out, a)
	}
	return out, sc.Err()
}

type shadowStatus struct {
	locked  bool
	expired bool
}

func readShadowStatus() map[string]shadowStatus {
	m := map[string]shadowStatus{}
	f, err := os.Open("/etc/shadow")
	if err != nil {
		return m
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		parts := strings.Split(sc.Text(), ":")
		if len(parts) < 2 {
			continue
		}
		hash := parts[1]
		st := shadowStatus{}
		if hash == "!" || hash == "*" || strings.HasPrefix(hash, "!") {
			st.locked = true
		}
		if len(parts) > 7 {
			if exp := strings.TrimSpace(parts[7]); exp != "" && exp != "99999" {
				if n, err := strconv.Atoi(exp); err == nil && n >= 0 {
					// expire days since epoch; 0 or past ≈ expired/disabled by date
					if n == 0 {
						st.expired = true
					}
				}
			}
		}
		m[parts[0]] = st
	}
	return m
}

// RequireRoot returns an error unless running as root.
func RequireRoot() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("root required to manage users (re-run with sudo)")
	}
	return nil
}

func validateUsername(name string) error {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 32 {
		return fmt.Errorf("invalid username length")
	}
	for i, c := range name {
		ok := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-'
		if i == 0 {
			ok = (c >= 'a' && c <= 'z') || c == '_'
		}
		if !ok {
			return fmt.Errorf("invalid username %q", name)
		}
	}
	return nil
}

func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), msg)
	}
	return nil
}

func findAccount(name string) (*Account, error) {
	accts, err := listAccounts()
	if err != nil {
		return nil, err
	}
	for i := range accts {
		if accts[i].Name == name {
			return &accts[i], nil
		}
	}
	return nil, fmt.Errorf("user %q not found", name)
}
