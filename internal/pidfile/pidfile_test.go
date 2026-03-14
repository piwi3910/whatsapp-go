package pidfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAndRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.pid")
	if err := Write(path); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	pid, err := Read(path)
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	if pid != os.Getpid() {
		t.Errorf("Read() = %d, want %d", pid, os.Getpid())
	}
}

func TestRemove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.pid")
	_ = Write(path)
	Remove(path)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("PID file was not removed")
	}
}

func TestIsRunning_NoPidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent.pid")
	if IsRunning(path) {
		t.Error("IsRunning returned true for nonexistent file")
	}
}

func TestIsRunning_CurrentProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.pid")
	if err := Write(path); err != nil {
		t.Fatal(err)
	}
	if !IsRunning(path) {
		t.Error("IsRunning returned false for current process")
	}
}

func TestServerAddress_NoServer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent.pid")
	addr := ServerAddress(path, "localhost", 8080)
	if addr != "" {
		t.Errorf("ServerAddress = %q, want empty string when no server running", addr)
	}
}

func TestServerAddress_Running(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.pid")
	Write(path)
	addr := ServerAddress(path, "localhost", 8080)
	if addr != "http://localhost:8080" {
		t.Errorf("ServerAddress = %q, want %q", addr, "http://localhost:8080")
	}
}
