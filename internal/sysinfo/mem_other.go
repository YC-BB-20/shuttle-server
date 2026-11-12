//go:build !(darwin || linux)

// Package sysinfo reads the few machine facts the limits are scaled by.
package sysinfo

// TotalRAM is unknown on this platform.
func TotalRAM() uint64 { return 0 }
