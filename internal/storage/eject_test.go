package storage

import "testing"

func TestEjectReasonIsPlain(t *testing.T) {
	for out, want := range map[string]string{
		"Unmount of disk4 failed: at least one volume could not be unmounted\nUnmount failed for /Volumes/T5: dissented by PID 312": "the disk is in use: close any file or Finder window open on it, then try again",
		"umount: /mnt/x: target is busy.": "the disk is in use: close any file or Finder window open on it, then try again",
		"umount: /mnt/x: not mounted.":    "the disk is no longer connected",
		"":                                "the system could not eject the disk",
		"something odd\nsecond line":      "the system could not eject the disk: something odd",
	} {
		if got := ejectReason(out); got != want {
			t.Errorf("%q: got %q, want %q", out, got, want)
		}
	}
}
