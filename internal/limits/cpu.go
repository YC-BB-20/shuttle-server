package limits

import "runtime"

// CPUResult is what ApplyCPU did, for the page.
type CPUResult struct {
	Procs     int    `json:"procs"`
	NumCPU    int    `json:"num_cpu"`
	Nice      int    `json:"nice"`
	NiceError string `json:"nice_error,omitempty"`
}

// Procs is the core ceiling: share of the cores, at least two (or one on a
// single-core machine).
func Procs(numCPU int, share float64) int {
	n := int(float64(numCPU) * share)
	if n < 2 {
		n = 2
	}
	if n > numCPU {
		n = numCPU
	}
	return n
}

// ApplyCPU caps the cores Go uses and lowers the scheduling priority, so
// anything interactive on the same machine wins. It never uses macOS's
// "background" class, which throttles disk access.
func ApplyCPU(share float64, nice int) CPUResult {
	r := CPUResult{NumCPU: runtime.NumCPU(), Nice: nice}
	r.Procs = Procs(r.NumCPU, share)
	runtime.GOMAXPROCS(r.Procs)
	if err := setNice(nice); err != nil {
		r.NiceError = err.Error()
	}
	return r
}
