//go:build darwin || linux

package sysinfo

import "syscall"

// OpenFileLimit returns the process's current open-file limit. Go raises the
// soft limit to the hard limit at start-up on its own; nothing here changes a
// system setting.
func OpenFileLimit() uint64 {
	var r syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &r); err != nil {
		return 0
	}
	return r.Cur
}
