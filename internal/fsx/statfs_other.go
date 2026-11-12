//go:build !(darwin || linux)

package fsx

import "errors"

// FreeBytes is not available on this platform.
func FreeBytes(string) (uint64, error) { return 0, errors.New("free space not supported here") }
