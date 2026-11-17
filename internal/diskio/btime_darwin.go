package diskio

import (
	"os"
	"syscall"
	"time"
)

// Created is when a file or folder was made (v0.2.100, the file browser's
// Created column): macOS keeps a birth time for every file.
func Created(fi os.FileInfo) time.Time {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Birthtimespec.Sec > 0 {
		return time.Unix(st.Birthtimespec.Sec, st.Birthtimespec.Nsec)
	}
	return time.Time{}
}
