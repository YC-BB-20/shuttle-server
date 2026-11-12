// Package sysinfo reads the few machine facts the limits are scaled by.
package sysinfo

import "syscall"

// TotalRAM returns physical memory in bytes, or 0 if unknown.
func TotalRAM() uint64 {
	var si syscall.Sysinfo_t
	if err := syscall.Sysinfo(&si); err != nil {
		return 0
	}
	return uint64(si.Totalram) * uint64(si.Unit)
}
