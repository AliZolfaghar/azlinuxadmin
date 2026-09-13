package change

import (
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/AliZolfaghar/azlinuxadmin/internal/priv"
)

const appDirName = "azlinuxadmin"

// Entry is one auditable, undoable mutation.
type Entry struct {
	ID        string            `json:"id"`
	Time      time.Time         `json:"time"`
	Kind      string            `json:"kind"`
	Summary   string            `json:"summary"`
	User      string            `json:"user"`
	Backups   []FileBackup      `json:"backups"`
	Meta      map[string]string `json:"meta,omitempty"`
	Undone    bool              `json:"undone"`
	UndoneAt  *time.Time        `json:"undone_at,omitempty"`
}

// FileBackup stores the pre-change contents of a file (or notes it was absent).
type FileBackup struct {
	Path    string `json:"path"`
	Content []byte `json:"content"`
	Existed bool   `json:"existed"`
	Mode    uint32 `json:"mode"`
}

// Journal persists change entries under the app data directory.
type Journal struct {
	Dir string
}

// Open returns a journal rooted in a writable data dir.
func Open() (*Journal, error) {
	dir, err := dataDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "changes"), 0o750); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "backups"), 0o750); err != nil {
		return nil, err
	}
	return &Journal{Dir: dir}, nil
}

func dataDir() (string, error) {
	if os.Geteuid() == 0 {
		return filepath.Join("/var/lib", appDirName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", appDirName), nil
}

// BackupFile reads path into a FileBackup (safe if missing).
func BackupFile(path string) (FileBackup, error) {
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return FileBackup{Path: path, Existed: false}, nil
		}
		// Root-only path: try via sudo.
		data, rerr := priv.ReadFile(path)
		if rerr != nil {
			if os.IsNotExist(rerr) {
				return FileBackup{Path: path, Existed: false}, nil
			}
			return FileBackup{}, rerr
		}
		return FileBackup{
			Path:    path,
			Content: data,
			Existed: true,
			Mode:    0o644,
		}, nil
	}
	data, err := priv.ReadFile(path)
	if err != nil {
		return FileBackup{}, err
	}
	return FileBackup{
		Path:    path,
		Content: data,
		Existed: true,
		Mode:    uint32(fi.Mode().Perm()),
	}, nil
}

// Restore writes a backup back to disk.
func Restore(b FileBackup) error {
	if !b.Existed {
		return priv.Remove(b.Path)
	}
	_ = priv.Run("mkdir", "-p", filepath.Dir(b.Path))
	mode := os.FileMode(0o644)
	if b.Mode != 0 {
		mode = os.FileMode(b.Mode)
	}
	return priv.WriteFile(b.Path, b.Content, mode)
}

// Record writes a new journal entry and returns it.
func (j *Journal) Record(kind, summary string, backups []FileBackup, meta map[string]string) (*Entry, error) {
	now := time.Now().UTC()
	id := now.Format("20060102T150405.000000000")
	username := "unknown"
	if u, err := user.Current(); err == nil {
		username = u.Username
	}
	e := &Entry{
		ID:      id,
		Time:    now,
		Kind:    kind,
		Summary: summary,
		User:    username,
		Backups: backups,
		Meta:    meta,
	}
	path := filepath.Join(j.Dir, "changes", id+".json")
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, data, 0o640); err != nil {
		return nil, err
	}
	return e, nil
}

// List returns entries newest-first.
func (j *Journal) List() ([]Entry, error) {
	dir := filepath.Join(j.Dir, "changes")
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Entry, 0, len(ents))
	for _, ent := range ents {
		if ent.IsDir() || filepath.Ext(ent.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, ent.Name()))
		if err != nil {
			continue
		}
		var e Entry
		if err := json.Unmarshal(data, &e); err != nil {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Time.After(out[j].Time)
	})
	return out, nil
}

// Undo restores all backups for an entry and marks it undone.
func (j *Journal) Undo(id string) (*Entry, error) {
	path := filepath.Join(j.Dir, "changes", id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var e Entry
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, err
	}
	if e.Undone {
		return nil, fmt.Errorf("change %s already undone", id)
	}
	// Restore in reverse order.
	for i := len(e.Backups) - 1; i >= 0; i-- {
		if err := Restore(e.Backups[i]); err != nil {
			return nil, fmt.Errorf("restore %s: %w", e.Backups[i].Path, err)
		}
	}
	now := time.Now().UTC()
	e.Undone = true
	e.UndoneAt = &now
	out, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, out, 0o640); err != nil {
		return nil, err
	}
	return &e, nil
}

// LastActive returns the newest entry matching kind that is not undone.
func (j *Journal) LastActive(kindPrefix string) (*Entry, error) {
	all, err := j.List()
	if err != nil {
		return nil, err
	}
	for i := range all {
		e := &all[i]
		if e.Undone {
			continue
		}
		if kindPrefix == "" || strings.HasPrefix(e.Kind, kindPrefix) {
			return e, nil
		}
	}
	return nil, nil
}
