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
