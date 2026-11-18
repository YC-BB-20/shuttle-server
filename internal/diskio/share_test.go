package diskio

import (
	"os"
	"path/filepath"
	"testing"
)

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

func TestShareFromAndFindShare(t *testing.T) {
	for id, want := range map[string][2]string{
		"share://filer.local/Media:/Volumes/Media":                                 {"//filer.local/Media", "/Volumes/Media"},
		"share://127.0.0.1:22000/space:/Volumes/bb/space":                          {"//127.0.0.1:22000/space", "/Volumes/bb/space"},
		"share:" + shareFrom("//ana@filer.local/Media") + ":" + "/Volumes/Media-1": {"//filer.local/Media", "/Volumes/Media-1"},
	} {
		f, m, ok := ShareFrom(id)
		if !ok || f != want[0] || m != want[1] {
			t.Errorf("%q: %q %q %v", id, f, m, ok)
		}
	}
	if _, _, ok := ShareFrom("123:456:/Volumes/T5"); ok {
		t.Error("a disk read as a share")
	}
	// The share reconnected as Media-1: found; a disk or another share is not.
	root := t.TempDir()
	for _, n := range []string{"T5", "Media-1", "Other", "lucid/space"} {
		os.MkdirAll(filepath.Join(root, n), 0o755)
	}
	oldR, oldL := shareRoots, shareLookup
	t.Cleanup(func() { shareRoots, shareLookup = oldR, oldL })
	shareRoots = func() []string { return []string{root} }
	shareLookup = func(p string) (Volume, bool) {
		switch filepath.Base(p) {
		case "Media-1":
			return Volume{Mount: p, Share: ShareSMB, From: "//filer.local/Media", ID: shareID("//filer.local/Media", p)}, true
		case "Other":
			return Volume{Mount: p, Share: ShareSMB, From: "//filer.local/Other"}, true
		case "space":
			return Volume{Mount: p, Share: ShareLucid, From: "//127.0.0.1:22000/space"}, true
		}
		return Volume{Mount: "/", ID: "disk"}, true
	}
	if v, ok := FindShare("//filer.local/Media"); !ok || v.Mount != filepath.Join(root, "Media-1") {
		t.Fatalf("%+v %v", v, ok)
	}
	if v, ok := FindShare("//127.0.0.1:22000/space"); !ok || v.Mount != filepath.Join(root, "lucid", "space") {
		t.Fatalf("one folder deeper: %+v %v", v, ok)
	}
	if _, ok := FindShare("//filer.local/Gone"); ok {
		t.Fatal("found a share that is not there")
	}
}
