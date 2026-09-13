package ssh

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/AliZolfaghar/azlinuxadmin/internal/change"
	"github.com/AliZolfaghar/azlinuxadmin/internal/priv"
)

// ApplySettings writes managed sshd drop-in, validates, reloads, and journals undo.
// draft should contain lowercase keys for settings to set (merged onto existing managed file).
func ApplySettings(j *change.Journal, draft map[string]string) (*change.Entry, error) {
	if err := RequireRoot(); err != nil {
		return nil, err
	}
	if err := ValidateDraft(draft); err != nil {
		return nil, err
	}
	if len(draft) == 0 {
		return nil, fmt.Errorf("no SSH settings to apply")
	}

	backups, err := backupManaged()
	if err != nil {
		return nil, err
	}

	merged := parseManagedFile()
	for k, v := range draft {
		merged[k] = strings.TrimSpace(strings.ToLower(v))
	}

	content := renderManaged(merged)
	if err := priv.Run("mkdir", "-p", filepath.Dir(managedDropIn)); err != nil {
		return nil, err
	}
	if err := priv.WriteFile(managedDropIn, []byte(content), 0o600); err != nil {
		restoreAll(backups)
		return nil, err
	}

	if err := sshdTest(); err != nil {
		restoreAll(backups)
		return nil, fmt.Errorf("sshd config invalid (restored backup): %w", err)
	}
	if err := reloadSSHD(); err != nil {
		restoreAll(backups)
		_ = reloadSSHD()
		return nil, fmt.Errorf("reload failed (restored backup): %w", err)
	}

	summary := summarizeDraft(draft)
	return j.Record("ssh.apply", summary, backups, map[string]string{
		"manager": "sshd",
		"file":    managedDropIn,
	})
}

// UndoSSHChange restores the managed drop-in and reloads sshd.
func UndoSSHChange(j *change.Journal, id string) (*change.Entry, error) {
	if err := RequireRoot(); err != nil {
		return nil, err
	}
	e, err := j.Undo(id)
	if err != nil {
		return nil, err
	}
	if err := sshdTest(); err != nil {
		return e, fmt.Errorf("files restored but sshd -t failed: %w", err)
	}
	if err := reloadSSHD(); err != nil {
		return e, fmt.Errorf("files restored but reload failed: %w", err)
	}
	return e, nil
}

func backupManaged() ([]change.FileBackup, error) {
	b, err := change.BackupFile(managedDropIn)
	if err != nil {
		return nil, err
	}
	return []change.FileBackup{b}, nil
}

func restoreAll(backups []change.FileBackup) {
	for i := len(backups) - 1; i >= 0; i-- {
		_ = change.Restore(backups[i])
	}
}

func renderManaged(values map[string]string) string {
	var b strings.Builder
	b.WriteString("# Managed by AZLinuxAdmin — do not edit manually\n")
	b.WriteString("# Undo via the SSH page history.\n\n")
	for _, def := range CommonSettings {
		if v, ok := values[def.Key]; ok && v != "" {
			b.WriteString(def.Label)
			b.WriteString(" ")
			b.WriteString(v)
			b.WriteString("\n")
		}
	}
	return b.String()
}

func sshdTest() error {
	out, err := priv.CombinedOutput("sshd", "-t")
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

func reloadSSHD() error {
	if err := priv.Run("systemctl", "reload", "ssh"); err == nil {
		return nil
	}
	if err := priv.Run("systemctl", "reload", "sshd"); err == nil {
		return nil
	}
	if err := priv.Run("service", "ssh", "reload"); err == nil {
		return nil
	}
	return priv.Run("systemctl", "reload", "ssh")
}

func summarizeDraft(draft map[string]string) string {
	parts := make([]string, 0, len(draft))
	for _, def := range CommonSettings {
		if v, ok := draft[def.Key]; ok {
			parts = append(parts, fmt.Sprintf("%s=%s", def.Label, v))
		}
	}
	if len(parts) == 0 {
		return "ssh settings updated"
	}
	if len(parts) > 3 {
		return fmt.Sprintf("ssh: %s … (%d settings)", strings.Join(parts[:3], ", "), len(parts))
	}
	return "ssh: " + strings.Join(parts, ", ")
}
