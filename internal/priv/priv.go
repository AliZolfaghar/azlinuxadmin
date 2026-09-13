package priv

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// ErrNeedPassword means the UI should prompt for a sudo password.
var ErrNeedPassword = errors.New("sudo password required")

// Session holds optional in-memory sudo credentials for this process.
type Session struct {
	mu       sync.Mutex
	password string // kept only when Remember is true (or briefly while validating)
	remember bool
}

// Default is the process-wide privilege session.
var Default Session

// IsRoot reports whether the process is already root.
func IsRoot() bool {
	return os.Geteuid() == 0
}

// HasElevated reports whether privileged ops can run without prompting.
func (s *Session) HasElevated() bool {
	if IsRoot() {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.password != "" {
		return true
	}
	return sudoNonInteractiveOK()
}

// Ensure returns nil if privileged ops can proceed, or ErrNeedPassword.
func Ensure() error {
	return Default.Ensure()
}

// Ensure is the method form of the package-level Ensure.
func (s *Session) Ensure() error {
	if s.HasElevated() {
		return nil
	}
	return ErrNeedPassword
}

// Remembering reports whether a password is cached until exit.
func (s *Session) Remembering() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.remember && s.password != ""
}

// Clear drops cached credentials.
func (s *Session) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.password = ""
	s.remember = false
}

// Authenticate validates password with sudo and optionally remembers it until exit.
func Authenticate(password string, remember bool) error {
	return Default.Authenticate(password, remember)
}

// Authenticate validates password with `sudo -S -v`.
func (s *Session) Authenticate(password string, remember bool) error {
	if IsRoot() {
		return nil
	}
	password = strings.TrimSuffix(password, "\n")
	if password == "" {
		return fmt.Errorf("password is empty")
	}
	cmd := exec.Command("sudo", "-S", "-p", "", "-v")
	cmd.Stdin = strings.NewReader(password + "\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = "incorrect password or sudo denied"
		}
		return fmt.Errorf("%s", msg)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if remember {
		s.password = password
		s.remember = true
	} else {
		// Rely on sudo timestamp cache; do not keep password in memory.
		s.password = ""
		s.remember = false
	}
	return nil
}

func sudoNonInteractiveOK() bool {
	return exec.Command("sudo", "-n", "true").Run() == nil
}

func (s *Session) getPassword() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.password
}

// Command returns an *exec.Cmd that runs name with privilege when needed.
func Command(name string, args ...string) (*exec.Cmd, error) {
	return Default.Command(name, args...)
}

// Command builds a privileged command.
func (s *Session) Command(name string, args ...string) (*exec.Cmd, error) {
	if IsRoot() {
		return exec.Command(name, args...), nil
	}
	if err := s.Ensure(); err != nil {
		return nil, err
	}
	if sudoNonInteractiveOK() {
		return exec.Command("sudo", append([]string{"-n", name}, args...)...), nil
	}
	pass := s.getPassword()
	if pass == "" {
		return nil, ErrNeedPassword
	}
	cmd := exec.Command("sudo", append([]string{"-S", "-p", "", name}, args...)...)
	cmd.Stdin = strings.NewReader(pass + "\n")
	return cmd, nil
}

// Run runs a privileged command.
func Run(name string, args ...string) error {
	return Default.Run(name, args...)
}

// Run executes name with args under sudo when required.
func (s *Session) Run(name string, args ...string) error {
	cmd, err := s.Command(name, args...)
	if err != nil {
		return err
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		// Credential may have expired.
		if isSudoAuthError(msg) {
			s.Clear()
			return ErrNeedPassword
		}
		return fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), msg)
	}
	return nil
}

// Output runs a privileged command and returns stdout.
func Output(name string, args ...string) ([]byte, error) {
	return Default.Output(name, args...)
}

// Output returns stdout from a privileged command.
func (s *Session) Output(name string, args ...string) ([]byte, error) {
	cmd, err := s.Command(name, args...)
	if err != nil {
		return nil, err
	}
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			msg := strings.TrimSpace(string(ee.Stderr))
			if isSudoAuthError(msg) || isSudoAuthError(string(out)) {
				s.Clear()
				return nil, ErrNeedPassword
			}
			if msg != "" {
				return nil, fmt.Errorf("%s", msg)
			}
		}
		return nil, err
	}
	return out, nil
}

// CombinedOutput runs a privileged command and returns combined output.
func CombinedOutput(name string, args ...string) ([]byte, error) {
	return Default.CombinedOutput(name, args...)
}

// CombinedOutput returns stdout+stderr from a privileged command.
func (s *Session) CombinedOutput(name string, args ...string) ([]byte, error) {
	cmd, err := s.Command(name, args...)
	if err != nil {
		return nil, err
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if isSudoAuthError(msg) {
			s.Clear()
			return out, ErrNeedPassword
		}
		return out, err
	}
	return out, nil
}

// RunWithStdin runs a privileged command with the given stdin payload (after sudo auth).
func RunWithStdin(stdin string, name string, args ...string) error {
	return Default.RunWithStdin(stdin, name, args...)
}

// RunWithStdin feeds stdin to the command. When using sudo -S, password is sent first
// via a wrapper script approach: write payload to temp and use sudo.
func (s *Session) RunWithStdin(stdin string, name string, args ...string) error {
	if IsRoot() {
		cmd := exec.Command(name, args...)
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		if err != nil {
			msg := strings.TrimSpace(string(out))
			if msg == "" {
				msg = err.Error()
			}
			return fmt.Errorf("%s: %s", name, msg)
		}
		return nil
	}
	// Avoid mixing sudo password and command stdin: use a temp file.
	tmp, err := os.CreateTemp("", "azla-stdin-*")
	if err != nil {
		return err
	}
	path := tmp.Name()
	defer os.Remove(path)
	if _, err := tmp.WriteString(stdin); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// shell: cat tmp | cmd args
	script := fmt.Sprintf("cat %q | %s", path, shellJoin(name, args))
	return s.Run("bash", "-c", script)
}

func shellJoin(name string, args []string) string {
	parts := make([]string, 0, 1+len(args))
	parts = append(parts, shellQuote(name))
	for _, a := range args {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ReadFile reads a file, using sudo when required.
func ReadFile(path string) ([]byte, error) {
	return Default.ReadFile(path)
}

// ReadFile reads path with privilege escalation if needed.
func (s *Session) ReadFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		return data, nil
	}
	if os.IsNotExist(err) {
		return nil, err
	}
	if IsRoot() {
		return nil, err
	}
	if err := s.Ensure(); err != nil {
		return nil, err
	}
	return s.Output("cat", path)
}

// WriteFile writes a file, using sudo when required.
func WriteFile(path string, data []byte, mode os.FileMode) error {
	return Default.WriteFile(path, data, mode)
}

// WriteFile writes path with privilege escalation if needed.
func (s *Session) WriteFile(path string, data []byte, mode os.FileMode) error {
	if IsRoot() {
		return os.WriteFile(path, data, mode)
	}
	if err := s.Ensure(); err != nil {
		return err
	}
	tmp, err := os.CreateTemp("", "azla-write-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := s.Run("cp", tmpPath, path); err != nil {
		return err
	}
	return s.Run("chmod", fmt.Sprintf("%04o", mode.Perm()), path)
}

// Remove removes a path with privilege when needed.
func Remove(path string) error {
	return Default.Remove(path)
}

// Remove deletes path using sudo when required.
func (s *Session) Remove(path string) error {
	if IsRoot() {
		err := os.Remove(path)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := s.Ensure(); err != nil {
		return err
	}
	return s.Run("rm", "-f", path)
}

func isSudoAuthError(msg string) bool {
	l := strings.ToLower(msg)
	return strings.Contains(l, "password") ||
		strings.Contains(l, "a password is required") ||
		strings.Contains(l, "authentication") ||
		strings.Contains(l, "sorry") ||
		strings.Contains(l, "not in the sudoers")
}

// NeedPassword reports whether err means the UI should ask for sudo.
func NeedPassword(err error) bool {
	return errors.Is(err, ErrNeedPassword)
}
