package limits

import (
	"os"
	"strconv"
	"syscall"
)

// On Linux nice is per thread, and new threads inherit it from their creator,
// so every thread that exists now is lowered.
func setNice(n int) error {
	ents, err := os.ReadDir("/proc/self/task")
	if err != nil {
		return syscall.Setpriority(syscall.PRIO_PROCESS, 0, n)
	}
	var first error
	for _, e := range ents {
		tid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if err := syscall.Setpriority(syscall.PRIO_PROCESS, tid, n); err != nil && first == nil {
			first = err
		}
	}
	return first
}
