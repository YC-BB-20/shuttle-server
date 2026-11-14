package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const diskutilSample = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>BusProtocol</key>
	<string>USB</string>
	<key>DeviceIdentifier</key>
	<string>disk4s2</string>
	<key>Ejectable</key>
	<true/>
	<key>FilesystemUserVisibleName</key>
	<string>Mac OS Extended (Journaled)</string>
	<key>Internal</key>
	<false/>
	<key>MediaName</key>
	<string>Samsung PSSD T7 Media </string>
	<key>ParentWholeDisk</key>
	<string>disk4</string>
	<key>Partitions</key>
	<array><dict><key>Inner</key><string>no</string></dict></array>
	<key>SolidState</key>
	<true/>
	<key>TotalSize</key>
	<integer>1000204886016</integer>
</dict>
</plist>`

func TestParsePlist(t *testing.T) {
	d := parsePlistDict([]byte(diskutilSample))
	if d["BusProtocol"] != "USB" || d["Internal"] != false || d["SolidState"] != true || d["TotalSize"] != int64(1000204886016) ||
		d["ParentWholeDisk"] != "disk4" || d["MediaName"] != "Samsung PSSD T7 Media" || d["Inner"] != nil || d["Ejectable"] != true {
		t.Fatalf("%v", d)
	}
	if parsePlistDict([]byte("garbage")) != nil || parsePlistDict(nil) != nil {
		t.Error("not a plist")
	}
}

func TestConnectionNames(t *testing.T) {
	for _, c := range []struct {
		bus      string
		internal bool
		want     string
	}{{"USB", false, "USB"}, {"PCI-Express", false, "Thunderbolt"}, {"PCI-Express", true, "Internal"}, {"Thunderbolt", false, "Thunderbolt"},
		{"Apple Fabric", true, "Internal"}, {"Disk Image", false, "Disk image"}, {"Secure Digital", false, "SD card slot"}} {
		if got := connectionName(c.bus, c.internal); got != c.want {
			t.Errorf("%s/%v: %s", c.bus, c.internal, got)
		}
	}
}

func TestUSBSpeedFromReport(t *testing.T) {
	var tree any
	json.Unmarshal([]byte(`{"SPUSBDataType":[{"_name":"USB31Bus","_items":[{"_name":"hub","device_speed":"high_speed","_items":[
		{"_name":"T7","device_speed":"super_speed_plus","Media":[{"bsd_name":"disk4","volumes":[{"bsd_name":"disk4s2"}]}]}]}]}]}`), &tree)
	raw := speedAbove(tree, "disk4")
	if sp, det := usbSpeed(raw); sp != "10 Gb/s" || det != "USB 3.2 Gen 2" {
		t.Fatalf("%q → %s %s", raw, sp, det)
	}
	if speedAbove(tree, "disk9") != "" {
		t.Error("found a disk that is not there")
	}
	json.Unmarshal([]byte(`{"SPThunderboltDataType":[{"_name":"bus0","_items":[{"device_name_key":"RAID"}],"receptacle_1_tag":{"current_speed_key":"Up to 40 Gb/s"}},{"_name":"bus1","receptacle_1_tag":{"current_speed_key":"Up to 40 Gb/s"}}]}`), &tree)
	if s := thunderboltSpeeds(tree); len(s) != 1 || s[0] != "Up to 40 Gb/s" {
		t.Fatalf("%v", s)
	}
}

func TestCameraHintAndWholeDisk(t *testing.T) {
	d := t.TempDir()
	if cameraHint(d) != "" {
		t.Error("empty folder looks like a card")
	}
	os.MkdirAll(filepath.Join(d, "DCIM"), 0o755)
	if cameraHint(d) == "" {
		t.Error("DCIM not seen")
	}
	for in, want := range map[string]string{"sda1": "sda", "nvme0n1p2": "nvme0n1", "mmcblk0p1": "mmcblk0", "sdb": "sdb"} {
		if got := wholeDisk(in); got != want {
			t.Errorf("%s → %s", in, got)
		}
	}
}
