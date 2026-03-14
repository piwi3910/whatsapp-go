package pidfile

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Write creates a PID file at path containing the current process ID.
func Write(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0644)
}

// Read returns the PID stored in the file.
func Read(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

// Remove deletes the PID file.
func Remove(path string) {
	os.Remove(path)
}

// IsRunning checks if a process with the PID in the file is still alive.
func IsRunning(path string) bool {
	pid, err := Read(path)
	if err != nil {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 checks if process exists without actually sending a signal
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

// ServerAddress returns the running server's address if a server is detected.
// Returns empty string if no server is running.
func ServerAddress(pidPath, host string, port int) string {
	if !IsRunning(pidPath) {
		return ""
	}
	return fmt.Sprintf("http://%s:%d", host, port)
}
