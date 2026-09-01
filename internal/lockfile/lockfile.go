// Package lockfile provides a process lock guarding direct whatsmeow
// connections (issue #22).
//
// WhatsApp's multi-device protocol does not allow two live connections from
// the same device. `wa serve` is already covered by the PID file (which the
// CLI checks before deciding to proxy), but nothing guards the case where two
// direct CLI invocations race — the second connection kicks the first off
// the phone, and the user's session flap is only visible as confusing
// reconnect loops on both sides.
//
// The lock is a file created with O_EXCL: concurrent creation fails, and the
// failure is the whole mechanism — no fcntl/flock, no polling. A lock left
// behind by a crashed process is detected by the PID it stores and taken
// over.
package lockfile

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Acquire takes the lock at path, recording the current PID. If another
// live process already holds it, Acquire fails with an error naming that
// PID. A lock whose holder PID is no longer alive is stale and is taken
// over, so a crash never wedges the CLI permanently.
//
// The lock file is removed with Remove; a SIGKILLed process leaves its lock
// behind, and the next Acquire recovers it.
func Acquire(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}

	pid := os.Getpid()
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			_, werr := f.Write([]byte(strconv.Itoa(pid)))
			f.Close()
			if werr != nil {
				os.Remove(path) // do not leave a half-written lock behind
			}
			return werr
		}
		if !os.IsExist(err) {
			return err
		}

		// Someone else holds it — is that someone still alive?
		data, rerr := os.ReadFile(path)
		if rerr != nil || !isProcessAlive(pidFromData(data)) {
			// Stale (or unreadable): take it over and retry the claim.
			os.Remove(path)
			continue
		}
		return fmt.Errorf("another wa process (pid %d) is using this WhatsApp device; wait for it to finish", pidFromData(data))
	}
}

// Remove releases the lock. Missing or foreign lock files are not an error:
// this process may have died and been restarted, and deleting a live
// process's lock would let two connections race.
func Remove(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return // already gone
	}
	if pidFromData(data) == os.Getpid() {
		os.Remove(path)
	}
}

func pidFromData(data []byte) int {
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return -1
	}
	return pid
}

// isProcessAlive is the signal-0 liveness check shared in spirit with
// pidfile.IsRunning: signal 0 reaches the process but delivers nothing.
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
