package lockfile

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestAcquireAndHold(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wa.lock")

	if err := Acquire(path); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer Remove(path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading lock file: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != strconv.Itoa(os.Getpid()) {
		t.Errorf("lock file = %q, want current pid %d", got, os.Getpid())
	}

	// O_EXCL means the second claim by anyone (including us) must fail with
	// a "held by pid" error — the lock is not reentrant.
	if err := Acquire(path); err == nil {
		t.Fatal("second Acquire on a held lock succeeded, want failure")
	}
}

// A lock whose holder is no longer alive must be taken over, or one crash
// would wedge every later invocation.
func TestAcquireTakesOverStaleLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wa.lock")
	const deadPID = 4194304 // beyond any plausible live pid
	if err := os.WriteFile(path, []byte(strconv.Itoa(deadPID)), 0644); err != nil {
		t.Fatalf("writing stale lock: %v", err)
	}

	if err := Acquire(path); err != nil {
		t.Fatalf("Acquire over stale lock: %v", err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), strconv.Itoa(os.Getpid())) {
		t.Errorf("stale lock not taken over, file = %q", data)
	}
	Remove(path)
}

func TestRemoveOnlyOwnLock(t *testing.T) {
	dir := t.TempDir()

	foreign := filepath.Join(dir, "a.lock")
	if err := os.WriteFile(foreign, []byte("999999"), 0644); err != nil {
		t.Fatal(err)
	}
	Remove(foreign)
	if _, err := os.Stat(foreign); err != nil {
		t.Error("Remove deleted a lock that does not belong to us")
	}

	own := filepath.Join(dir, "b.lock")
	if err := os.WriteFile(own, []byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
		t.Fatal(err)
	}
	Remove(own)
	if _, err := os.Stat(own); !os.IsNotExist(err) {
		t.Errorf("own lock not removed (stat err = %v)", err)
	}
}

func TestRemoveMissingFileIsNotAnError(t *testing.T) {
	Remove(filepath.Join(t.TempDir(), "nope.lock")) // must be a silent no-op
}
