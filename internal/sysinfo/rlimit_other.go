//go:build !(darwin || linux)

package sysinfo

// OpenFileLimit is unknown on this platform.
func OpenFileLimit() uint64 { return 0 }
