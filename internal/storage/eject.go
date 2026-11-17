package storage

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// ejectTimeout bounds an eject, inside the page's 15 s request limit: the
// system may wait for a disk to finish writing before it lets go.
const ejectTimeout = 12 * time.Second

// Eject asks the system to eject the disk mounted at mount (v0.2.100), or
// to unmount it when it is a network share. It only asks: if a program
// still has a file open there, the system refuses and the reason is
// returned in plain words. Nothing on the disk is changed.
func Eject(ctx context.Context, mount string, share bool) error {
	ctx, cancel := context.WithTimeout(ctx, ejectTimeout)
	defer cancel()
	var cmd *exec.Cmd
	switch {
	case runtime.GOOS == "darwin" && share:
		cmd = exec.CommandContext(ctx, "/usr/sbin/diskutil", "unmount", mount)
	case runtime.GOOS == "darwin":
		cmd = exec.CommandContext(ctx, "/usr/sbin/diskutil", "eject", mount)
	default:
		cmd = exec.CommandContext(ctx, "umount", mount)
	}
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return errors.New("the system did not finish ejecting in time; the disk may still be busy, so wait a moment and look again")
	}
	return errors.New(ejectReason(string(out)))
}

// ejectReason turns the system's answer into one short plain sentence.
func ejectReason(out string) string {
	low := strings.ToLower(out)
	switch {
	case strings.Contains(low, "busy") || strings.Contains(low, "in use") || strings.Contains(low, "dissented"):
		return "the disk is in use: close any file or Finder window open on it, then try again"
	case strings.Contains(low, "not mounted") || strings.Contains(low, "no such") || strings.Contains(low, "not found"):
		return "the disk is no longer connected"
	case strings.Contains(low, "permission") || strings.Contains(low, "not permitted"):
		return "the system did not allow it"
	}
	line := strings.TrimSpace(out)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	if len(line) > 160 {
		line = line[:160]
	}
	if line == "" {
		return "the system could not eject the disk"
	}
	return "the system could not eject the disk: " + line
}
