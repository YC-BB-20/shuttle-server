package limits

import "syscall"

// On macOS the priority applies to the whole process.
func setNice(n int) error { return syscall.Setpriority(syscall.PRIO_PROCESS, 0, n) }
