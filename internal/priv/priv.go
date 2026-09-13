package priv

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Ensure returns an error unless the process is running as root.
func Ensure() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("root required — run with sudo")
	}
	return nil
}

// IsRoot reports whether the process is root.
func IsRoot() bool {
	return os.Geteuid() == 0
}

// Run runs a command as the current (root) process.
func Run(name string, args ...string) error {
	if err := Ensure(); err != nil {
		return err
	}
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

// Output returns stdout from a command.
func Output(name string, args ...string) ([]byte, error) {
	if err := Ensure(); err != nil {
		return nil, err
	}
	return exec.Command(name, args...).Output()
}

// CombinedOutput returns combined stdout/stderr from a command.
func CombinedOutput(name string, args ...string) ([]byte, error) {
	if err := Ensure(); err != nil {
		return nil, err
	}
	return exec.Command(name, args...).CombinedOutput()
}

// RunWithStdin runs a command with the given stdin payload.
func RunWithStdin(stdin string, name string, args ...string) error {
	if err := Ensure(); err != nil {
		return err
	}
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

// ReadFile reads a file (requires root for protected paths).
func ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// WriteFile writes a file (requires root for protected paths).
func WriteFile(path string, data []byte, mode os.FileMode) error {
	if err := Ensure(); err != nil {
		return err
	}
	return os.WriteFile(path, data, mode)
}

// Remove deletes a path.
func Remove(path string) error {
	if err := Ensure(); err != nil {
		return err
	}
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
