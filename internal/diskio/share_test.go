package diskio

import "testing"

func TestShareKind(t *testing.T) {
	for _, c := range []struct{ typ, from, mount, want string }{
		{"smbfs", "//ana@filer.local/Media", "/Volumes/Media", ShareSMB},
		{"cifs", "//10.0.0.5/Media", "/mnt/media", ShareSMB},
		{"macfuse", "lucid", "/Volumes/project", ShareLucid},
		{"nfs", "localhost:/project", "/Volumes/LucidLink/project", ShareLucid},
		{"nfs", "server:/x", "/Volumes/x", ""},
		{"afpfs", "//ana@mac.local/x", "/Volumes/x", ""},
		{"apfs", "/dev/disk3s1", "/Volumes/T5", ""},
		{"apfs", "/dev/disk4s1", "/Volumes/Lucid Archive", ""},
		{"exfat", "/dev/disk5s1", "/Volumes/LUCID", ""},
		// LucidLink on macOS: an SMB share from this computer (v0.2.102)
		{"smbfs", "//127.0.0.1:22000/projects", "/Volumes/work/projects", ShareLucid},
		{"smbfs", "//guest:@127.0.0.1:22000/projects", "/Volumes/work/projects", ShareLucid},
		{"smbfs", "//localhost/x", "/Volumes/x", ShareLucid},
		{"smbfs", "//[::1]:22000/x", "/Volumes/a/x", ShareLucid},
		{"smbfs", "//10.0.0.127/x", "/Volumes/x", ShareSMB},
		{"smbfs", "//127.example.com/x", "/Volumes/x", ShareSMB},
	} {
		if got := shareKind(c.typ, c.from, c.mount); got != c.want {
			t.Errorf("%v: got %q", c, got)
		}
	}
	if !(Volume{Network: true, Share: ShareSMB}).CopyOK() || (Volume{Network: true}).CopyOK() || !(Volume{}).CopyOK() {
		t.Fatal("CopyOK")
	}
}

func TestShareFromDropsUser(t *testing.T) {
	for in, want := range map[string]string{
		"//ana@Filer.local/Media":        "//filer.local/Media",
		"//DOMAIN;ana@filer/Media/Sub":   "//filer/Media/Sub",
		"//filer.local/Media":            "//filer.local/Media",
		"lucid":                          "lucid",
		"//ana:secret@filer.local/Media": "//filer.local/Media",
	} {
		if got := shareFrom(in); got != want {
			t.Errorf("%q: got %q", in, got)
		}
	}
}
