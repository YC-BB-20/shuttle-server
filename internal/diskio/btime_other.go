//go:build !darwin

package diskio

import (
	"os"
	"time"
)

// Created is zero here: Linux reports a birth time only through statx,
// which the standard library does not offer. The page shows a dash.
func Created(os.FileInfo) time.Time { return time.Time{} }
