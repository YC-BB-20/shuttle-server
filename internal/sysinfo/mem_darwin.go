// Package sysinfo reads the few machine facts the limits are scaled by.
package sysinfo

import (
	"encoding/binary"
	"syscall"
)

// TotalRAM returns physical memory in bytes, or 0 if unknown.
func TotalRAM() uint64 {
	s, err := syscall.Sysctl("hw.memsize")
	if err != nil {
		return 0
	}
	b := []byte(s) // syscall.Sysctl drops one trailing zero byte; pad back
	for len(b) < 8 {
		b = append(b, 0)
	}
	return binary.LittleEndian.Uint64(b[:8])
}
