package storage

// Connection map (v0.2.47): for every connected disk, which controller and
// port of the computer it hangs from, what sits in between (hubs, docks),
// and which other disks share that port. On macOS this comes from the
// system's device tree (ioreg), read as a stream so memory stays small
// whatever its size; on Linux from /sys. Read only.

import (
	"bufio"
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Path is where one disk is attached.
type Path struct {
	Key   string   // same Key = same port or controller, so they share its bandwidth
	Bus   string   // plain words, e.g. "Port 2 · Thunderbolt/USB4 bus 2"
	Via   []string // hubs and docks between the computer and the disk, nearest the computer first
	USBID string   // USB location id in hex, to find the disk's link speed in the hardware report
	Speed string   // raw USB speed from the device tree, used only when the report has none
	Thund bool     // reached through a Thunderbolt/USB4 controller (PCIe tunnel), not plain USB
	Port  int      // port number on Apple silicon, -1 when not known
}

type node struct {
	name, class string
	props       map[string]string
}

// Keys of the device tree worth keeping; everything else is skipped while
// reading.
var wantProps = map[string]bool{"BSD Name": true, "locationID": true, "USB Product Name": true, "kUSBProductString": true,
	"Device Speed": true, "USBSpeed": true, "Device Model Name": true, "Device Vendor Name": true}

var wholeRE = regexp.MustCompile(`^disk[0-9]+$`)

// parseIOReg reads `ioreg -p IOService -w0 -l` and returns, for each whole
// disk, the chain of entries from the root down to it. At most 64 disks are
// kept; only the chain to the current entry is held while reading.
func parseIOReg(sc *bufio.Scanner) map[string][]node {
	out := map[string][]node{}
	var stack []node
	for sc.Scan() {
		line := sc.Text()
		if i := strings.Index(line, "+-o "); i >= 0 {
			depth := i / 2
			if depth > len(stack) {
				depth = len(stack)
			}
			rest := line[i+4:]
			name, class := rest, ""
			if j := strings.Index(rest, "  <class "); j >= 0 {
				name = rest[:j]
				class = rest[j+9:]
				if k := strings.IndexAny(class, ",>"); k >= 0 {
					class = class[:k]
				}
			}
			stack = append(stack[:depth], node{name: name, class: class})
			continue
		}
		if len(stack) == 0 {
			continue
		}
		t := strings.TrimLeft(line, " |")
		if !strings.HasPrefix(t, `"`) {
			continue
		}
		end := strings.Index(t[1:], `"`)
		if end < 0 {
			continue
		}
		key := t[1 : end+1]
		if !wantProps[key] {
			continue
		}
		val := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(t[end+2:]), "="))
		val = strings.Trim(val, `"`)
		top := &stack[len(stack)-1]
		if top.props == nil {
			top.props = map[string]string{}
		}
		top.props[key] = val
		if key == "BSD Name" && wholeRE.MatchString(val) && len(out) < 64 {
			if _, dup := out[val]; !dup {
				chain := make([]node, len(stack))
				copy(chain, stack)
				out[val] = chain
			}
		}
	}
	return out
}

var portRE = regexp.MustCompile(`^(acio|usb-drd|atc|dart-acio)([0-9]+)@`)

// pathFromChain works out the port, controller and hubs from a disk's chain.
func pathFromChain(chain []node) Path {
	p := Path{Port: -1}
	var ctrl string
	var usbDevs []node
	tbSwitches := 0
	for _, n := range chain {
		if m := portRE.FindStringSubmatch(n.name); m != nil && p.Port < 0 {
			p.Port, _ = strconv.Atoi(m[2])
		}
		if strings.HasPrefix(n.name, "acio") {
			p.Thund = true
		}
		c := strings.ToLower(n.class)
		// Fallback on Apple silicon: the port's own USB controller is
		// "AppleT<chip>USBXHCI@0N000000", N being the port.
		if p.Port < 0 && strings.HasPrefix(n.class, "AppleT") && strings.HasSuffix(n.class, "USBXHCI") {
			if at := strings.SplitN(n.name, "@", 2); len(at) == 2 && len(at[1]) == 8 {
				if v, err := strconv.ParseUint(at[1][:2], 16, 8); err == nil {
					p.Port = int(v)
				}
			}
		}
		if ctrl == "" && (strings.Contains(c, "xhci") || strings.Contains(c, "thunderboltcontroller") || strings.Contains(c, "thunderbolthal")) {
			ctrl = strings.SplitN(n.name, "@", 2)[0]
		}
		if strings.Contains(c, "thunderbolt") && !strings.Contains(c, "controller") {
			p.Thund = true
		}
		if strings.HasPrefix(c, "iothunderboltswitch") {
			tbSwitches++
			if tbSwitches > 1 { // the first switch is the computer's own
				if m := strings.TrimSpace(n.props["Device Vendor Name"] + " " + n.props["Device Model Name"]); m != "" {
					p.Via = append(p.Via, m)
				}
			}
		}
		if n.class == "IOUSBHostDevice" || n.class == "IOUSBDevice" {
			usbDevs = append(usbDevs, n)
		}
	}
	if len(usbDevs) > 0 {
		dev := usbDevs[len(usbDevs)-1]
		for _, h := range usbDevs[:len(usbDevs)-1] {
			p.Via = append(p.Via, usbName(h))
		}
		if id, err := strconv.ParseInt(dev.props["locationID"], 0, 64); err == nil {
			p.USBID = "0x" + strings.ToLower(strconv.FormatInt(id, 16))
		}
		p.Speed = ioSpeed(dev.props["Device Speed"])
	}
	switch {
	case p.Port >= 0:
		p.Key = "port" + strconv.Itoa(p.Port)
		p.Bus = "Port " + strconv.Itoa(p.Port) + " · Thunderbolt/USB4 bus " + strconv.Itoa(p.Port)
		if !p.Thund && len(usbDevs) > 0 {
			p.Bus += " (its USB controller)"
		}
	case ctrl != "":
		p.Key = "ctrl:" + ctrl
		p.Bus = "Controller " + ctrl
	}
	return p
}

func usbName(n node) string {
	for _, k := range []string{"USB Product Name", "kUSBProductString"} {
		if v := n.props[k]; v != "" {
			return v
		}
	}
	return strings.SplitN(n.name, "@", 2)[0]
}

// ioSpeed: the older "Device Speed" number (0 low, 1 full, 2 high, 3 super,
// 4 super plus, 5 super plus 2x2) as words usbSpeed understands.
func ioSpeed(s string) string {
	switch s {
	case "2":
		return "480"
	case "3":
		return "5 gb"
	case "4":
		return "10 gb"
	case "5":
		return "20 gb"
	}
	return ""
}

// The device tree is read once per change of the set of disks, or after
// topoFresh; one read at a time.
const topoFresh = 30 * time.Second

var (
	topoMu   sync.Mutex
	topoAt   time.Time
	topoSig  string
	topoData map[string]Path
)

// Topology returns the attachment of every whole disk, read again when sig
// (the list of mounted disks) changes.
func Topology(ctx context.Context, sig string) map[string]Path {
	topoMu.Lock()
	defer topoMu.Unlock()
	if topoData != nil && sig == topoSig && time.Since(topoAt) < topoFresh {
		return topoData
	}
	ctx, cancel := context.WithTimeout(ctx, profileTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/sbin/ioreg", "-p", "IOService", "-w0", "-l")
	cmd.WaitDelay = time.Second
	out, err := cmd.StdoutPipe()
	if err != nil || cmd.Start() != nil {
		return topoData
	}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	chains := parseIOReg(sc)
	cmd.Wait()
	if ctx.Err() != nil && len(chains) == 0 {
		return topoData
	}
	m := map[string]Path{}
	for d, ch := range chains {
		m[d] = pathFromChain(ch)
	}
	topoData, topoAt, topoSig = m, time.Now(), sig
	return m
}

// ForgetHardware drops the cached reports so the next look reads them again.
func ForgetHardware() {
	topoMu.Lock()
	topoAt = time.Time{}
	topoMu.Unlock()
	profMu.Lock()
	profAt = time.Time{}
	profMu.Unlock()
}

// usbLinkByLocation finds the USB device with this location id in the
// hardware report (SPUSBHostDataType or SPUSBDataType) and returns the
// speed text it reports, e.g. "Limited to 5 Gb/s".
func usbLinkByLocation(tree any, loc string) string {
	if loc == "" {
		return ""
	}
	want, err := strconv.ParseUint(loc, 0, 64)
	if err != nil {
		return ""
	}
	var found string
	var walk func(v any) bool
	walk = func(v any) bool {
		switch x := v.(type) {
		case map[string]any:
			for k, val := range x {
				s, ok := val.(string)
				if !ok || !strings.Contains(strings.ToLower(k), "location") {
					continue
				}
				f := strings.Fields(s)
				if len(f) == 0 {
					continue
				}
				if n, err := strconv.ParseUint(f[0], 0, 64); err == nil && n == want {
					found = speedOf(x)
					return true
				}
			}
			for _, c := range x {
				if walk(c) {
					return true
				}
			}
		case []any:
			for _, c := range x {
				if walk(c) {
					return true
				}
			}
		}
		return false
	}
	walk(tree)
	return found
}

// thunderboltBusSpeed is the link speed of Thunderbolt/USB4 bus n, as the
// hardware report names its buses ("Thunderbolt/USB4 Bus 2").
func thunderboltBusSpeed(tree any, n int) string {
	top, _ := tree.(map[string]any)
	for _, bus := range asList(top["SPThunderboltDataType"]) {
		b, _ := bus.(map[string]any)
		name, _ := b["_name"].(string)
		f := strings.Fields(name)
		if len(f) == 0 || f[len(f)-1] != strconv.Itoa(n) {
			continue
		}
		for k, v := range b {
			if r, ok := v.(map[string]any); ok && strings.HasPrefix(k, "receptacle") {
				if s, _ := r["current_speed_key"].(string); s != "" {
					return s
				}
			}
		}
	}
	return ""
}

// linuxPath reads the controller of a block device from its /sys path:
// the PCI device above the first usbN or Thunderbolt domainN.
func linuxPath(link string) Path {
	p := Path{Port: -1}
	parts := strings.Split(link, "/")
	for i, s := range parts {
		if strings.HasPrefix(s, "domain") && strings.Contains(link, "thunderbolt") {
			p.Key, p.Bus, p.Thund = "tb:"+s, "Thunderbolt "+s, true
			return p
		}
		if strings.HasPrefix(s, "usb") && len(s) > 3 && s[3] >= '0' && s[3] <= '9' {
			ctrl := ""
			if i > 0 {
				ctrl = parts[i-1]
			}
			p.Key = "usbctl:" + ctrl
			p.Bus = "USB controller " + ctrl + " (bus " + s[3:] + ")"
			return p
		}
	}
	if strings.Contains(link, "/pci") {
		for _, s := range parts {
			if strings.Count(s, ":") == 2 && strings.Contains(s, ".") {
				p.Key, p.Bus = "pci:"+s, "PCIe "+s
			}
		}
	}
	return p
}
