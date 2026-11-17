// Package config reads and writes the host config: config.json in the data
// folder. Every path and limit lives here, never hard-coded elsewhere. A value
// of 0 means "work it out for this machine"; the page shows what was chosen.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"

	"github.com/YC-BB-20/shuttle-server/internal/fsx"
)

// FormatVersion of config.json written by this release.
const FormatVersion = 1

const (
	mb = 1 << 20
	gb = 1 << 30
)

// Config is the host config.
type Config struct {
	FormatVersion    int     `json:"format_version"`
	Listen           string  `json:"listen"`
	DedicatedMachine bool    `json:"dedicated_machine"`
	TempDir          string  `json:"temp_dir"`
	FreeSpaceFloorGB float64 `json:"free_space_floor_gb"`
	BufferBudgetMB   int     `json:"buffer_budget_mb"`
	MemoryCeilingMB  int     `json:"memory_ceiling_mb"`
	CPUShare         float64 `json:"cpu_share"`
	Nice             int     `json:"nice"`
	OpenFilesCap     int     `json:"open_files_cap"`
	PageUpdatesPerS  int     `json:"page_updates_per_second"`
	MaxOpenPages     int     `json:"max_open_pages"`
	PageDir          string  `json:"page_dir"`
	HomebrewPrefix   string  `json:"homebrew_prefix"`
	MenuBarIcon      bool    `json:"menu_bar_icon"`
	UpdateRepo       string  `json:"update_repo"`
	// LucidLink (v0.2.98): the client's command ("" finds it), and how much
	// written data may wait to upload before a copy to LucidLink pauses.
	LucidCommand      string  `json:"lucid_command"`
	LucidMaxWaitingGB float64 `json:"lucid_max_waiting_gb"`
}

// Defaults returns the starting config for this machine (DESIGN.md section 18).
func Defaults() Config {
	prefix := ""
	switch {
	case runtime.GOOS == "darwin" && runtime.GOARCH == "arm64":
		prefix = "/opt/homebrew"
	case runtime.GOOS == "darwin":
		prefix = "/usr/local"
	}
	return Config{
		FormatVersion:     FormatVersion,
		Listen:            "127.0.0.1:8470",
		FreeSpaceFloorGB:  50,
		CPUShare:          0.5,
		Nice:              10,
		PageUpdatesPerS:   4,
		MaxOpenPages:      8,
		HomebrewPrefix:    prefix,
		MenuBarIcon:       runtime.GOOS == "darwin",
		UpdateRepo:        "YC-BB-20/shuttle-server",
		LucidMaxWaitingGB: 4,
	}
}

// Load reads path. If it does not exist, the defaults are written there and
// created is true. Missing fields keep their defaults.
func Load(path string) (c Config, created bool, err error) {
	c = Defaults()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		out, _ := json.MarshalIndent(c, "", "  ")
		return c, true, fsx.WriteFileAtomic(path, append(out, '\n'), 0o600)
	}
	if err != nil {
		return c, false, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return Defaults(), false, fmt.Errorf("not valid JSON: %w", err)
	}
	return c, false, c.Validate()
}

// Validate refuses values that would break a limit or open the page beyond
// this computer.
func (c Config) Validate() error {
	if c.FormatVersion > FormatVersion {
		return fmt.Errorf("format_version %d was written by a newer Shuttle Server", c.FormatVersion)
	}
	host, _, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return fmt.Errorf("listen %q: %w", c.Listen, err)
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("listen %q: this version only listens on this computer (127.0.0.1 or localhost)", c.Listen)
	}
	switch {
	case c.CPUShare <= 0 || c.CPUShare > 1:
		return errors.New("cpu_share must be above 0 and at most 1")
	case c.Nice < 0 || c.Nice > 19:
		return errors.New("nice must be 0 to 19")
	case c.FreeSpaceFloorGB < 0:
		return errors.New("free_space_floor_gb cannot be negative")
	case c.BufferBudgetMB != 0 && c.BufferBudgetMB < 16:
		return errors.New("buffer_budget_mb must be 0 (automatic) or at least 16")
	case c.MemoryCeilingMB != 0 && c.MemoryCeilingMB < c.BufferBudgetMB+64:
		return errors.New("memory_ceiling_mb must be 0 (automatic) or at least buffer_budget_mb + 64")
	case c.PageUpdatesPerS < 1 || c.PageUpdatesPerS > 10:
		return errors.New("page_updates_per_second must be 1 to 10")
	case c.MaxOpenPages < 1 || c.MaxOpenPages > 64:
		return errors.New("max_open_pages must be 1 to 64")
	case c.OpenFilesCap < 0:
		return errors.New("open_files_cap cannot be negative")
	}
	return nil
}

// BufferBudgetBytes is the shared buffer pool's size: 64 MB on a machine with
// 2 GB or less, 256 MB on a dedicated machine, 128 MB otherwise.
func (c Config) BufferBudgetBytes(totalRAM uint64) int64 {
	switch {
	case c.BufferBudgetMB > 0:
		return int64(c.BufferBudgetMB) * mb
	case totalRAM > 0 && totalRAM <= 2*gb:
		return 64 * mb
	case c.DedicatedMachine:
		return 256 * mb
	}
	return 128 * mb
}

// MemoryCeilingBytes is twice the buffer budget plus a margin (512 MB, or
// 128 MB on a small machine): about 1 GB on the mini.
func (c Config) MemoryCeilingBytes(totalRAM uint64) int64 {
	if c.MemoryCeilingMB > 0 {
		return int64(c.MemoryCeilingMB) * mb
	}
	margin := int64(512 * mb)
	if totalRAM > 0 && totalRAM <= 2*gb {
		margin = 128 * mb
	}
	return 2*c.BufferBudgetBytes(totalRAM) + margin
}

// FloorBytes is the free-space floor in bytes.
func (c Config) FloorBytes() uint64 { return uint64(c.FreeSpaceFloorGB * gb) }

// OpenFiles is the app's own cap on open files, kept well below the process
// limit: half of it unless set lower.
func (c Config) OpenFiles(limit uint64) int {
	auto := int(limit / 2)
	if limit == 0 {
		auto = 1024
	}
	if c.OpenFilesCap > 0 && c.OpenFilesCap < auto {
		return c.OpenFilesCap
	}
	return auto
}
