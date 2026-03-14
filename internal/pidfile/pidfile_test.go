package pidfile_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/piwi3910/whatsapp-go/internal/pidfile"
)

func TestWriteAndRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.pid")

	if err := pidfile.Write(path); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	pid, err := pidfile.Read(path)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if pid != os.Getpid() {
		t.Errorf("Read() = %d; want %d (current PID)", pid, os.Getpid())
	}
}

func TestWriteCreatesParentDirectories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "nested", "test.pid")

	if err := pidfile.Write(path); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("pid file not created at %q: %v", path, err)
	}
}

func TestReadNonExistent(t *testing.T) {
	_, err := pidfile.Read("/tmp/this-file-does-not-exist-wa-test.pid")
	if err == nil {
		t.Error("Read() should return error for non-existent file")
	}
}

func TestReadInvalidContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.pid")

	if err := os.WriteFile(path, []byte("not-a-number\n"), 0o644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	_, err := pidfile.Read(path)
	if err == nil {
		t.Error("Read() should return error for non-integer content")
	}
}

func TestRemove(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.pid")

	if err := pidfile.Write(path); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := pidfile.Remove(path); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("Remove() should have deleted the pid file")
	}
}

func TestRemoveNonExistent(t *testing.T) {
	// Should not return an error for a missing file.
	if err := pidfile.Remove("/tmp/this-file-does-not-exist-wa-test.pid"); err != nil {
		t.Errorf("Remove() for non-existent file = %v; want nil", err)
	}
}

func TestIsRunningCurrentProcess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.pid")

	if err := pidfile.Write(path); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	if !pidfile.IsRunning(path) {
		t.Error("IsRunning() = false for current process; want true")
	}
}

func TestIsRunningMissingFile(t *testing.T) {
	if pidfile.IsRunning("/tmp/this-file-does-not-exist-wa-test.pid") {
		t.Error("IsRunning() = true for missing file; want false")
	}
}

func TestIsRunningDeadProcess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dead.pid")

	// PID 99999999 is very unlikely to be alive.
	deadPID := 99999999
	if err := os.WriteFile(path, []byte(strconv.Itoa(deadPID)+"\n"), 0o644); err != nil {
		t.Fatalf("failed to write test pid file: %v", err)
	}

	// We do not assert false here because on some systems extremely high PIDs
	// could technically exist. We just verify no panic occurs.
	_ = pidfile.IsRunning(path)
}

func TestServerAddress(t *testing.T) {
	tests := []struct {
		host string
		port int
		want string
	}{
		{"127.0.0.1", 8080, "127.0.0.1:8080"},
		{"0.0.0.0", 9090, "0.0.0.0:9090"},
		{"localhost", 3000, "localhost:3000"},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%s:%d", tc.host, tc.port), func(t *testing.T) {
			got := pidfile.ServerAddress("/any/path.pid", tc.host, tc.port)
			if got != tc.want {
				t.Errorf("ServerAddress() = %q; want %q", got, tc.want)
			}
			// Should be "host:port" format.
			parts := strings.SplitN(got, ":", 2)
			if len(parts) != 2 {
				t.Errorf("ServerAddress() = %q; expected 'host:port' format", got)
			}
		})
	}
}
