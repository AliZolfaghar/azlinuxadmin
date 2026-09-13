package users

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/AliZolfaghar/azlinuxadmin/internal/change"
)

var accountFiles = []string{
	"/etc/passwd",
	"/etc/shadow",
	"/etc/group",
	"/etc/gshadow",
	"/etc/subuid",
	"/etc/subgid",
}

// AddRequest creates a new local user.
type AddRequest struct {
	Name     string
	Password string // optional; empty leaves account locked until set
	Shell    string
	Home     string // optional; default /home/<name>
	GECOS    string
	CreateHome bool
}

// EditRequest updates mutable fields on an existing user.
type EditRequest struct {
	Name     string
	Shell    string
	GECOS    string
	Home     string // move home if changed (usermod -d -m)
	Password string // optional reset; empty = leave unchanged
}

func backupAccountFiles() ([]change.FileBackup, error) {
	var out []change.FileBackup
	for _, p := range accountFiles {
		b, err := change.BackupFile(p)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("could not backup account databases")
	}
	return out, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// AddUser creates a user and journals backups for undo.
func AddUser(j *change.Journal, req AddRequest) (*change.Entry, error) {
	if err := RequireRoot(); err != nil {
		return nil, err
	}
	req.Name = strings.TrimSpace(req.Name)
	if err := validateUsername(req.Name); err != nil {
		return nil, err
	}
	if _, err := findAccount(req.Name); err == nil {
		return nil, fmt.Errorf("user %q already exists", req.Name)
	}
	if req.Shell == "" {
		req.Shell = "/bin/bash"
	}
	if req.Home == "" {
		req.Home = "/home/" + req.Name
	}

	backups, err := backupAccountFiles()
	if err != nil {
		return nil, err
	}

	args := []string{"-s", req.Shell, "-d", req.Home}
	if req.CreateHome {
		args = append(args, "-m")
	}
	if req.GECOS != "" {
		args = append(args, "-c", req.GECOS)
	}
	args = append(args, req.Name)
	if err := runCmd("useradd", args...); err != nil {
		restoreAll(backups)
		return nil, err
	}

	if req.Password != "" {
		if err := setPassword(req.Name, req.Password); err != nil {
			restoreAll(backups)
			_ = runCmd("userdel", "-r", req.Name)
			return nil, err
		}
	}

	return j.Record("users.add", fmt.Sprintf("add user %s", req.Name), backups, map[string]string{
		"user":    req.Name,
		"action":  "add",
		"manager": "passwd",
	})
}

// EditUser updates shell/GECOS/home/password with undo via file restore.
func EditUser(j *change.Journal, req EditRequest) (*change.Entry, error) {
	if err := RequireRoot(); err != nil {
		return nil, err
	}
	req.Name = strings.TrimSpace(req.Name)
	acct, err := findAccount(req.Name)
	if err != nil {
		return nil, err
	}
	if req.Name == "root" && req.Shell == "/usr/sbin/nologin" {
		return nil, fmt.Errorf("refusing to set root shell to nologin")
	}

	backups, err := backupAccountFiles()
	if err != nil {
		return nil, err
	}

	args := []string{}
	if req.Shell != "" && req.Shell != acct.Shell {
		args = append(args, "-s", req.Shell)
	}
	if req.GECOS != acct.GECOS {
		args = append(args, "-c", req.GECOS)
	}
	if req.Home != "" && req.Home != acct.Home {
		args = append(args, "-d", req.Home, "-m")
	}
	if len(args) > 0 {
		args = append(args, req.Name)
		if err := runCmd("usermod", args...); err != nil {
			restoreAll(backups)
			return nil, err
		}
	}
	if req.Password != "" {
		if err := setPassword(req.Name, req.Password); err != nil {
			restoreAll(backups)
			return nil, err
		}
	}

	return j.Record("users.edit", fmt.Sprintf("edit user %s", req.Name), backups, map[string]string{
		"user":    req.Name,
		"action":  "edit",
		"manager": "passwd",
	})
}

// DeleteUser removes a user; backs up account DBs and home archive for undo.
func DeleteUser(j *change.Journal, name string, removeHome bool) (*change.Entry, error) {
	if err := RequireRoot(); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "root" {
		return nil, fmt.Errorf("refusing to delete root")
	}
	acct, err := findAccount(name)
	if err != nil {
		return nil, err
	}

	backups, err := backupAccountFiles()
	if err != nil {
		return nil, err
	}

	meta := map[string]string{
		"user":    name,
		"action":  "delete",
		"manager": "passwd",
		"home":    acct.Home,
	}

	if removeHome && acct.Home != "" && acct.Home != "/" && fileExists(acct.Home) {
		archive, err := archiveHome(j, name, acct.Home)
		if err != nil {
			return nil, fmt.Errorf("home backup: %w", err)
		}
		meta["home_archive"] = archive
	}

	args := []string{name}
	if removeHome {
		args = append([]string{"-r"}, name)
	}
	if err := runCmd("userdel", args...); err != nil {
		restoreAll(backups)
		return nil, err
	}

	return j.Record("users.delete", fmt.Sprintf("delete user %s", name), backups, meta)
}

// SetDisabled locks (disable) or unlocks (enable) a user password.
func SetDisabled(j *change.Journal, name string, disabled bool) (*change.Entry, error) {
	if err := RequireRoot(); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "root" && disabled {
		return nil, fmt.Errorf("refusing to disable root")
	}
	if _, err := findAccount(name); err != nil {
		return nil, err
	}

	backups, err := backupAccountFiles()
	if err != nil {
		return nil, err
	}

	var action string
	if disabled {
		action = "disable"
		if err := runCmd("usermod", "-L", name); err != nil {
			restoreAll(backups)
			return nil, err
		}
		// Also expire to block non-password logins where supported.
		_ = runCmd("usermod", "-e", "1", name)
	} else {
		action = "enable"
		if err := runCmd("usermod", "-U", name); err != nil {
			restoreAll(backups)
			return nil, err
		}
		_ = runCmd("usermod", "-e", "", name)
	}

	return j.Record("users."+action, fmt.Sprintf("%s user %s", action, name), backups, map[string]string{
		"user":    name,
		"action":  action,
		"manager": "passwd",
	})
}

func setPassword(name, password string) error {
	cmd := exec.Command("chpasswd")
	cmd.Stdin = strings.NewReader(name + ":" + password + "\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("chpasswd: %s", msg)
	}
	return nil
}

func archiveHome(j *change.Journal, name, home string) (string, error) {
	dir := filepath.Join(j.Dir, "backups", "homes")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	stamp := time.Now().UTC().Format("20060102T150405")
	path := filepath.Join(dir, fmt.Sprintf("%s-%s.tar.gz", name, stamp))
	// tar from parent so restore can extract into place
	parent := filepath.Dir(home)
	base := filepath.Base(home)
	cmd := exec.Command("tar", "-czf", path, "-C", parent, base)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return path, nil
}

func restoreAll(backups []change.FileBackup) {
	for i := len(backups) - 1; i >= 0; i-- {
		_ = change.Restore(backups[i])
	}
}

// UndoUserChange restores account DB backups and optional home archive.
func UndoUserChange(j *change.Journal, id string) (*change.Entry, error) {
	if err := RequireRoot(); err != nil {
		return nil, err
	}
	e, err := j.Undo(id)
	if err != nil {
		return nil, err
	}

	// If this undoes a delete, restore home from archive when present.
	if e.Kind == "users.delete" {
		if arch := e.Meta["home_archive"]; arch != "" {
			home := e.Meta["home"]
			if home != "" && fileExists(arch) {
				parent := filepath.Dir(home)
				_ = os.MkdirAll(parent, 0o755)
				_ = exec.Command("tar", "-xzf", arch, "-C", parent).Run()
			}
		}
	}

	// If undoing an add, the restored DBs remove the user — clean leftover home if empty add undo.
	// Restored passwd/shadow is enough for account identity.
	return e, nil
}
