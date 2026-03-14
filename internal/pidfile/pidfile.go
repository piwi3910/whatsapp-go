package pidfile

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// Write writes the current process PID to path, creating parent directories
// as needed. The file is written with mode 0o644.
func Write(path string) error {
	if err := os.MkdirAll(parentDir(path), 0o700); err != nil {
		return fmt.Errorf("pidfile: create directory: %w", err)
	}
	data := []byte(strconv.Itoa(os.Getpid()) + "\n")
	return os.WriteFile(path, data, 0o644)
}

// Read reads and returns the PID stored in path. It returns an error if the
// file does not exist or does not contain a valid integer.
func Read(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("pidfile: read: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("pidfile: parse pid: %w", err)
	}
	return pid, nil
}

// Remove deletes the pid file at path. It returns nil if the file does not
// exist.
func Remove(path string) error {
	err := os.Remove(path)
	if err != nil && errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// IsRunning returns true when a PID file exists at path and the process with
// that PID is currently alive. It returns false if the file does not exist, the
// PID cannot be read, or the process is not running.
func IsRunning(path string) bool {
	pid, err := Read(path)
	if err != nil {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds; we must send signal 0 to check
	// whether the process is alive.
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

// ServerAddress returns "host:port" for the server recorded in pidPath.
// This is a convenience helper that lets callers derive the API base URL
// without having to read the full config. The host and port values come from
// the caller (typically loaded from Config) rather than the pid file itself.
func ServerAddress(pidPath, host string, port int) string {
	return fmt.Sprintf("%s:%d", host, port)
}

// parentDir returns the directory component of path, defaulting to "." when
// there is no directory separator.
func parentDir(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[:i]
		}
	}
	return "."
}
