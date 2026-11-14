// Package storage describes the disks connected to this computer for the
// Storage page (v0.2.32): how each is connected (USB and its speed,
// Thunderbolt, internal), whether it is solid state, its file system, and
// whether it looks like a camera card. It only reads: it asks the system
// (diskutil and system_profiler on macOS, /proc and /sys on Linux) and
// never writes to a disk. Every outside program has a timeout, at most one
// of each runs at a time, and the slow hardware report is cached.
package storage

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Info is what the system says about the disk holding one mount point.
// Empty fields mean "not known".
type Info struct {
	Connection string `json:"connection,omitempty"` // USB, Thunderbolt, Internal, SATA, SD card, Disk image, Network
	Speed      string `json:"speed,omitempty"`      // link speed as the system reports it, e.g. "10 Gb/s"
	Detail     string `json:"detail,omitempty"`     // plain words, e.g. "USB 3.2 Gen 2"
	Model      string `json:"model,omitempty"`      // the drive or enclosure, as the system names it
	FileSystem string `json:"file_system,omitempty"`
	Solid      *bool  `json:"solid_state,omitempty"`
	Internal   bool   `json:"internal,omitempty"`
	Ejectable  bool   `json:"ejectable,omitempty"`
	Device     string `json:"device,omitempty"` // e.g. disk4s1
	Camera     string `json:"camera,omitempty"` // why it looks like a camera card, if it does
	// Where it is attached (v0.2.47): the computer's port or controller,
	// anything in between, and the other disks on the same port.
	Bus    string   `json:"bus,omitempty"`
	Via    []string `json:"via,omitempty"`
	BusKey string   `json:"bus_key,omitempty"`
	Shares []string `json:"shares,omitempty"`
	Trash  bool     `json:"trash,omitempty"` // the disk's Trash holds items, whose space is not free yet
	Unit   string   `json:"-"`               // the physical drive, so two volumes of one drive are not "sharing"
}

// Run time limits for the outside programs.
const (
	infoTimeout    = 5 * time.Second
	profileTimeout = 20 * time.Second
	profileFresh   = 30 * time.Second
)

// Details describes the disk mounted at mount. It never writes anything.
func Details(ctx context.Context, mount string) Info {
	var in Info
	if runtime.GOOS == "darwin" {
		in = darwinInfo(ctx, mount)
	} else {
		in = linuxInfo(mount)
	}
	in.Camera = CameraHint(mount)
	in.Trash = TrashHasItems(mount, in.Internal || mount == "/")
	return in
}

// CameraHint looks only at the names at the top of the disk: the folders
// cameras write. Nothing is opened for writing.
func CameraHint(mount string) string {
	for _, n := range []string{"DCIM", "PRIVATE/M4ROOT", "PRIVATE/AVCHD", "CLIPS001", "XDROOT", "CONTENTS", "Codex", "ARRIRAW"} {
		if fi, err := os.Stat(filepath.Join(mount, filepath.FromSlash(n))); err == nil && fi.IsDir() {
			return "has a " + n + " folder"
		}
	}
	return ""
}

// run starts one outside program with a time limit and returns its output.
// Only one run of each program at a time: a second caller gets nothing.
var running sync.Map

func run(ctx context.Context, limit time.Duration, name string, args ...string) []byte {
	if _, busy := running.LoadOrStore(name, true); busy {
		return nil
	}
	defer running.Delete(name)
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	return out
}

func darwinInfo(ctx context.Context, mount string) Info {
	var in Info
	d := parsePlistDict(run(ctx, infoTimeout, "/usr/sbin/diskutil", "info", "-plist", mount))
	if d == nil {
		return in
	}
	str := func(k string) string { s, _ := d[k].(string); return s }
	flag := func(k string) (bool, bool) { b, ok := d[k].(bool); return b, ok }
	in.Device = str("DeviceIdentifier")
	in.FileSystem = str("FilesystemUserVisibleName")
	if in.FileSystem == "" {
		in.FileSystem = str("FilesystemName")
	}
	in.Model = strings.TrimSpace(str("MediaName"))
	in.Internal, _ = flag("Internal")
	in.Ejectable, _ = flag("Ejectable")
	if b, ok := flag("SolidState"); ok {
		in.Solid = &b
	}
	in.Connection = connectionName(str("BusProtocol"), in.Internal)
	whole := str("ParentWholeDisk")
	if whole == "" {
		whole = in.Device
	}
	if in.Connection == "USB" || in.Connection == "Thunderbolt" {
		attach(ctx, &in, whole)
	}
	return in
}

// attach fills in the port, what is in between and the link speed from the
// device tree and the hardware report (v0.2.47).
func attach(ctx context.Context, in *Info, whole string) {
	tp, ok := Topology(ctx, volumeSig())[whole]
	if !ok {
		in.Speed, in.Detail = linkSpeed(ctx, whole, in.Connection)
		return
	}
	in.Bus, in.Via, in.BusKey, in.Unit = tp.Bus, tp.Via, tp.Key, tp.USBID
	if in.Unit == "" {
		in.Unit = whole
	}
	tree := hardware(ctx)
	if in.Connection == "USB" {
		raw := usbLinkByLocation(tree, tp.USBID)
		if raw == "" {
			raw = speedAbove(tree, whole)
		}
		if raw == "" {
			raw = tp.Speed
		}
		if raw != "" {
			in.Speed, in.Detail = usbSpeed(raw)
			if strings.Contains(strings.ToLower(raw), "limited") {
				in.Detail += " — the drive can go faster: the cable, adapter, hub or port is holding it back"
			}
		}
		if tp.Thund {
			in.Detail = strings.TrimPrefix(in.Detail+" · through a Thunderbolt dock", " · ")
		}
		return
	}
	if tp.Port >= 0 {
		if s := thunderboltBusSpeed(tree, tp.Port); s != "" {
			in.Speed, in.Detail = strings.TrimPrefix(s, "Up to "), "Thunderbolt link"
			return
		}
	}
	in.Speed, in.Detail = linkSpeed(ctx, whole, in.Connection)
}

// volumeSig names the mounted volumes, so the device tree is read again
// as soon as a disk comes or goes.
func volumeSig() string {
	ents, _ := os.ReadDir("/Volumes")
	var b strings.Builder
	for i, e := range ents {
		if i > 100 {
			break
		}
		b.WriteString(e.Name())
		b.WriteByte('/')
	}
	return b.String()
}

// TrashHasItems says whether the disk's Trash holds anything. Files moved to
// the Trash in Finder still take their space until it is emptied. Reads at
// most a few names.
func TrashHasItems(mount string, internal bool) bool {
	uid := strconv.Itoa(os.Getuid())
	dirs := []string{filepath.Join(mount, ".Trashes", uid), filepath.Join(mount, ".Trash-"+uid, "files")}
	if internal {
		home, _ := os.UserHomeDir()
		dirs = []string{filepath.Join(home, ".Trash"), filepath.Join(home, ".local/share/Trash/files")}
	}
	for _, d := range dirs {
		f, err := os.Open(d)
		if err != nil {
			continue
		}
		names, _ := f.Readdirnames(4)
		f.Close()
		for _, n := range names {
			if n != ".DS_Store" && n != ".localized" {
				return true
			}
		}
	}
	return false
}

// connectionName turns diskutil's bus protocol into plain words. An
// external disk on PCI-Express is a Thunderbolt enclosure on a Mac.
func connectionName(bus string, internal bool) string {
	switch b := strings.ToLower(bus); {
	case b == "usb":
		return "USB"
	case b == "thunderbolt":
		return "Thunderbolt"
	case strings.HasPrefix(b, "pci") && !internal:
		return "Thunderbolt"
	case b == "disk image":
		return "Disk image"
	case b == "secure digital":
		return "SD card slot"
	case b == "sata":
		if internal {
			return "Internal"
		}
		return "SATA"
	case internal || b == "apple fabric":
		return "Internal"
	case b == "":
		return ""
	}
	return bus
}

// The hardware report is slow (seconds), so it is read in the background,
// one at a time, with its own time limit, and kept for a little while. A
// caller waits only briefly; a page asking again later gets the result.
var (
	profMu   sync.Mutex
	profAt   time.Time
	profData any
	profBusy chan struct{} // closed when the read under way ends
)

// Pending says whether a hardware read is still under way, so the page can
// ask again for the connection speeds.
func Pending() bool {
	profMu.Lock()
	defer profMu.Unlock()
	return profBusy != nil
}

func hardware(ctx context.Context) any {
	profMu.Lock()
	if profData != nil && time.Since(profAt) < profileFresh {
		d := profData
		profMu.Unlock()
		return d
	}
	if profBusy == nil {
		done := make(chan struct{})
		profBusy = done
		go func() {
			out := run(context.Background(), profileTimeout, "/usr/sbin/system_profiler", "-json",
				"SPUSBHostDataType", "SPUSBDataType", "SPThunderboltDataType")
			var v any
			ok := out != nil && jsonUnmarshal(out, &v) == nil
			profMu.Lock()
			if ok {
				profData, profAt = v, time.Now()
			}
			profBusy = nil
			profMu.Unlock()
			close(done)
		}()
	}
	busy := profBusy
	profMu.Unlock()
	select {
	case <-busy:
	case <-ctx.Done():
	case <-time.After(6 * time.Second):
	}
	profMu.Lock()
	defer profMu.Unlock()
	return profData // the last good one, or nil
}

// linkSpeed finds the disk in the hardware report and the speed of the link
// it hangs from. macOS reports the protocol and speed, not the plug's shape,
// so "USB-C" itself cannot be told from the report.
func linkSpeed(ctx context.Context, whole, conn string) (speed, detail string) {
	tree := hardware(ctx)
	if tree == nil {
		return "", ""
	}
	if conn == "USB" {
		if raw := speedAbove(tree, whole); raw != "" {
			return usbSpeed(raw)
		}
		return "", ""
	}
	// Thunderbolt: the report lists ports and devices, not disks. With
	// exactly one device connected, its port's speed is this disk's.
	speeds := thunderboltSpeeds(tree)
	if len(speeds) == 1 {
		return speeds[0], "Thunderbolt link"
	}
	return "", ""
}

// usbSpeed turns the report's speed into a number and the USB name.
func usbSpeed(raw string) (string, string) {
	r := strings.ToLower(raw)
	switch {
	case strings.Contains(r, "super_speed_plus_by2"), strings.Contains(r, "20 gb"):
		return "20 Gb/s", "USB 3.2 Gen 2x2"
	case strings.Contains(r, "super_speed_plus"), strings.Contains(r, "10 gb"):
		return "10 Gb/s", "USB 3.2 Gen 2"
	case strings.Contains(r, "super_speed"), strings.Contains(r, "5 gb"):
		return "5 Gb/s", "USB 3.2 Gen 1 (USB 3.0)"
	case strings.Contains(r, "high_speed"), strings.Contains(r, "480"):
		return "480 Mb/s", "USB 2 — slow: a USB 2 cable, port or hub may be in the way"
	case strings.Contains(r, "40 gb"):
		return "40 Gb/s", "USB4"
	}
	return raw, ""
}

// speedAbove walks the report and returns the speed of the nearest device
// above the entry whose bsd_name is whole.
func speedAbove(tree any, whole string) string {
	var found string
	var walk func(v any, speeds []string) bool
	walk = func(v any, speeds []string) bool {
		switch x := v.(type) {
		case map[string]any:
			if s := speedOf(x); s != "" {
				speeds = append(speeds, s)
			}
			if b, _ := x["bsd_name"].(string); b == whole && len(speeds) > 0 {
				found = speeds[len(speeds)-1]
				return true
			}
			for _, c := range x {
				if walk(c, speeds) {
					return true
				}
			}
		case []any:
			for _, c := range x {
				if walk(c, speeds) {
					return true
				}
			}
		}
		return false
	}
	walk(tree, nil)
	return found
}

func speedOf(m map[string]any) string {
	for k, v := range m {
		if s, ok := v.(string); ok && strings.Contains(strings.ToLower(k), "speed") && !strings.Contains(strings.ToLower(k), "max") {
			return s
		}
	}
	return ""
}

// thunderboltSpeeds lists the current speed of every Thunderbolt port that
// has a device on it.
func thunderboltSpeeds(tree any) []string {
	top, _ := tree.(map[string]any)
	var out []string
	for _, bus := range asList(top["SPThunderboltDataType"]) {
		b, _ := bus.(map[string]any)
		if len(asList(b["_items"])) == 0 {
			continue
		}
		for k, v := range b {
			if r, ok := v.(map[string]any); ok && strings.HasPrefix(k, "receptacle") {
				if s, _ := r["current_speed_key"].(string); s != "" {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

func asList(v any) []any { l, _ := v.([]any); return l }

// linuxInfo reads /proc and /sys only.
func linuxInfo(mount string) Info {
	var in Info
	b, err := os.ReadFile("/proc/self/mounts")
	if err != nil {
		return in
	}
	dev := ""
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && unescapeMount(f[1]) == mount {
			dev, in.FileSystem = f[0], f[2]
		}
	}
	if !strings.HasPrefix(dev, "/dev/") {
		if strings.Contains(in.FileSystem, "nfs") || strings.Contains(in.FileSystem, "cifs") || strings.Contains(in.FileSystem, "smb") {
			in.Connection = "Network"
		}
		return in
	}
	in.Device = filepath.Base(dev)
	whole := wholeDisk(in.Device)
	if r, err := os.ReadFile("/sys/block/" + whole + "/queue/rotational"); err == nil {
		s := strings.TrimSpace(string(r)) == "0"
		in.Solid = &s
	}
	link, _ := filepath.EvalSymlinks("/sys/block/" + whole)
	if tp := linuxPath(link); tp.Key != "" {
		in.Bus, in.BusKey = tp.Bus, tp.Key
	}
	switch {
	case strings.Contains(link, "/usb"):
		in.Connection = "USB"
		if sp := usbSysSpeed(link); sp != "" {
			in.Speed, in.Detail = usbSpeed(sp + " gb")
			if sp == "480" {
				in.Speed, in.Detail = usbSpeed("480")
			}
		}
	case strings.Contains(link, "/mmc"):
		in.Connection = "SD card slot"
	case strings.Contains(link, "/virtual/"):
		in.Connection = "Disk image"
	case link != "":
		in.Connection, in.Internal = "Internal", true
	}
	if r, err := os.ReadFile("/sys/block/" + whole + "/device/model"); err == nil {
		in.Model = strings.TrimSpace(string(r))
	}
	return in
}

// usbSysSpeed reads the link speed of the USB device above a block device,
// in Mb/s as Linux reports it (480, 5000, 10000, 20000), as Gb/s text.
func usbSysSpeed(link string) string {
	for p := link; p != "/" && p != "."; p = filepath.Dir(p) {
		if b, err := os.ReadFile(filepath.Join(p, "speed")); err == nil {
			s := strings.TrimSpace(string(b))
			switch s {
			case "480":
				return "480"
			case "5000":
				return "5"
			case "10000":
				return "10"
			case "20000":
				return "20"
			}
			return ""
		}
	}
	return ""
}

// wholeDisk: sda1 → sda, nvme0n1p2 → nvme0n1, mmcblk0p1 → mmcblk0.
func wholeDisk(d string) string {
	if i := strings.LastIndex(d, "p"); i > 0 && (strings.HasPrefix(d, "nvme") || strings.HasPrefix(d, "mmcblk")) {
		return d[:i]
	}
	return strings.TrimRight(d, "0123456789")
}

func unescapeMount(s string) string {
	return strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(s)
}
