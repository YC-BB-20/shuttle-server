package storage

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A cut-down `ioreg -p IOService -w0 -l` of an Apple-silicon Mac: a T5 on
// port 1 through a hub, a T5 on port 2, and a Thunderbolt NVMe enclosure
// on port 0.
const ioregSample = `+-o Root  <class IORegistryEntry, id 0x100000100, retain 30>
  +-o MacBookPro  <class IOPlatformExpertDevice, id 0x100000110>
    +-o arm-io@10F00000  <class IOPlatformDevice, id 0x100000120>
      +-o usb-drd1@2280000  <class AppleARMIODevice, id 0x100000200>
      | {
      |   "name" = <"usb-drd1">
      | }
      | 
        +-o AppleT6050USBXHCI@01000000  <class AppleT6050USBXHCI, id 0x100000210>
          +-o USB3.1 Hub@01100000  <class IOUSBHostDevice, id 0x100000220>
          | {
          |   "USB Product Name" = "USB3.1 Hub"
          |   "locationID" = 17825792
          | }
          | 
            +-o Portable SSD T5@01110000  <class IOUSBHostDevice, id 0x100000230>
            | {
            |   "USB Product Name" = "Portable SSD T5"
            |   "locationID" = 17891328
            |   "Device Speed" = 3
            | }
            | 
              +-o IOUSBHostInterface@0  <class IOUSBHostInterface, id 0x100000240>
                +-o Samsung Portable SSD T5 Media  <class IOMedia, id 0x100000250>
                | {
                |   "BSD Name" = "disk4"
                | }
                | 
                  +-o T5A@1  <class IOMedia, id 0x100000260>
                  | {
                  |   "BSD Name" = "disk4s1"
                  | }
                  | 
      +-o usb-drd2@3280000  <class AppleARMIODevice, id 0x100000300>
        +-o AppleT6050USBXHCI@02000000  <class AppleT6050USBXHCI, id 0x100000310>
          +-o Portable SSD T5@02100000  <class IOUSBHostDevice, id 0x100000320>
          | {
          |   "USB Product Name" = "Portable SSD T5"
          |   "locationID" = 34603008
          | }
          | 
            +-o Samsung Portable SSD T5 Media  <class IOMedia, id 0x100000350>
            | {
            |   "BSD Name" = "disk6"
            | }
            | 
      +-o acio0@1F00000  <class AppleARMIODevice, id 0x100000400>
        +-o AppleThunderboltHALType5  <class AppleThunderboltHALType5, id 0x100000410>
          +-o IOThunderboltSwitchType5@0  <class IOThunderboltSwitchType5, id 0x100000420>
          | {
          |   "Device Model Name" = "MacBook Pro"
          |   "Device Vendor Name" = "Apple Inc."
          | }
          | 
            +-o IOThunderboltSwitchType3@1  <class IOThunderboltSwitchType3, id 0x100000430>
            | {
            |   "Device Model Name" = "TB3 RAID"
            |   "Device Vendor Name" = "OWC"
            | }
            | 
              +-o IONVMeController  <class IONVMeController, id 0x100000440>
                +-o RAID Media  <class IOMedia, id 0x100000450>
                | {
                |   "BSD Name" = "disk8"
                | }
`

func TestDeviceTreeMap(t *testing.T) {
	sc := bufio.NewScanner(strings.NewReader(ioregSample))
	chains := parseIOReg(sc)
	if len(chains) != 3 || chains["disk4s1"] != nil {
		t.Fatalf("disks: %v", len(chains))
	}
	a, b, c := pathFromChain(chains["disk4"]), pathFromChain(chains["disk6"]), pathFromChain(chains["disk8"])
	if a.Port != 1 || a.Key != "port1" || a.Thund || len(a.Via) != 1 || a.Via[0] != "USB3.1 Hub" || a.USBID != "0x1110000" || a.Speed != "5 gb" {
		t.Errorf("T5 on port 1: %+v", a)
	}
	if !strings.Contains(a.Bus, "Thunderbolt/USB4 bus 1") {
		t.Errorf("bus words: %s", a.Bus)
	}
	if b.Port != 2 || b.Key == a.Key || len(b.Via) != 0 || b.USBID != "0x2100000" {
		t.Errorf("T5 on port 2: %+v", b)
	}
	if c.Port != 0 || !c.Thund || len(c.Via) != 1 || c.Via[0] != "OWC TB3 RAID" {
		t.Errorf("Thunderbolt RAID: %+v", c)
	}
}

func TestPortFromControllerAddress(t *testing.T) {
	p := pathFromChain([]node{{name: "AppleT6050USBXHCI@02000000", class: "AppleT6050USBXHCI"}, {name: "T5@02100000", class: "IOUSBHostDevice", props: map[string]string{"locationID": "34603008"}}})
	if p.Port != 2 || p.Key != "port2" {
		t.Fatalf("%+v", p)
	}
}

func TestReportSpeedsByPlace(t *testing.T) {
	var tree any
	json.Unmarshal([]byte(`{"SPUSBHostDataType":[{"_name":"USB 3.1 Bus","_items":[{"_name":"Portable SSD T5","USBKeyLocationID":"0x02100000","USBDeviceKeyLinkSpeed":"Limited to 5 Gb/s"}]}],
	"SPThunderboltDataType":[{"_name":"Thunderbolt/USB4 Bus 0","receptacle_1_tag":{"current_speed_key":"Up to 40 Gb/s"}},{"_name":"Thunderbolt/USB4 Bus 2","receptacle_1_tag":{"current_speed_key":"Up to 120 Gb/s"}}]}`), &tree)
	raw := usbLinkByLocation(tree, "0x2100000")
	if sp, _ := usbSpeed(raw); raw != "Limited to 5 Gb/s" || sp != "5 Gb/s" {
		t.Errorf("%q %s", raw, sp)
	}
	if usbLinkByLocation(tree, "0x1110000") != "" || usbLinkByLocation(tree, "") != "" {
		t.Error("found a drive that is not in the report")
	}
	if s := thunderboltBusSpeed(tree, 0); s != "Up to 40 Gb/s" {
		t.Errorf("bus 0: %s", s)
	}
	if thunderboltBusSpeed(tree, 1) != "" {
		t.Error("bus 1 is not in the report")
	}
}

func TestLinuxControllers(t *testing.T) {
	p := linuxPath("/sys/devices/pci0000:00/0000:00:14.0/usb2/2-1/2-1:1.0/host0/target0:0:0/0:0:0:0/block/sda")
	q := linuxPath("/sys/devices/pci0000:00/0000:00:14.0/usb1/1-2/1-2:1.0/host1/target1:0:0/1:0:0:0/block/sdb")
	if p.Key == "" || p.Key != q.Key {
		t.Errorf("USB 2 and 3 buses of one controller share it: %q %q", p.Key, q.Key)
	}
	if r := linuxPath("/sys/devices/pci0000:00/0000:00:1d.0/0000:02:00.0/nvme/nvme0/nvme0n1"); r.Key != "pci:0000:02:00.0" {
		t.Errorf("nvme: %+v", r)
	}
}

func TestTrashHasItems(t *testing.T) {
	d := t.TempDir()
	if TrashHasItems(d, false) {
		t.Error("no trash")
	}
	tr := filepath.Join(d, ".Trashes", strconv.Itoa(os.Getuid()))
	os.MkdirAll(tr, 0o755)
	os.WriteFile(filepath.Join(tr, ".DS_Store"), nil, 0o644)
	if TrashHasItems(d, false) {
		t.Error(".DS_Store alone is not an item")
	}
	os.WriteFile(filepath.Join(tr, "clip.mov"), nil, 0o644)
	if !TrashHasItems(d, false) {
		t.Error("item in the Trash not seen")
	}
}
