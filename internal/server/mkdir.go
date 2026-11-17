package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/YC-BB-20/shuttle-server/internal/diskio"
	"github.com/YC-BB-20/shuttle-server/internal/storage"
)

// mkdir makes one new, empty folder inside a folder the picker can show, for
// choosing where to copy to. It never replaces anything: an existing name is
// refused. Camera cards, read-only disks, network servers and unmounted
// /Volumes paths are refused, so nothing is ever written to them.
func (s *Server) mkdir(w http.ResponseWriter, r *http.Request) (any, error) {
	var req struct {
		Parent string `json:"parent"`
		Name   string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return nil, errors.New("could not read the request")
	}
	name := strings.TrimSpace(req.Name)
	if err := folderName(name); err != nil {
		return nil, err
	}
	parent := filepath.Clean(req.Parent)
	if !filepath.IsAbs(parent) || !pickable(parent) {
		return nil, errors.New("a folder cannot be made here")
	}
	if !isDir(parent) {
		return nil, errors.New("this folder is no longer there")
	}
	if strings.HasPrefix(parent, "/Volumes/") || strings.HasPrefix(parent, "/media/") {
		if !diskio.VolumeMounted(parent) {
			return nil, errors.New("this disk is not connected")
		}
	}
	v, ok, hung := volume(parent, 2*time.Second)
	switch {
	case hung:
		return nil, errors.New("this disk is not responding")
	case !ok:
		return nil, errors.New("could not tell which disk this is")
	case !v.CopyOK():
		return nil, errors.New("Shuttle copies only with SMB network shares and the LucidLink mount, so no folder is made here")
	case v.ReadOnly:
		return nil, errors.New("this disk is read-only")
	}
	if hint := storage.CameraHint(v.Mount); hint != "" && v.Mount != "/" {
		return nil, errors.New("this looks like a camera card (" + hint + "); Shuttle never writes to one")
	}
	p := filepath.Join(parent, name)
	if err := os.Mkdir(p, 0o755); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, errors.New("something called \"" + name + "\" is already here")
		}
		return nil, errors.New("the folder could not be made here")
	}
	return map[string]string{"path": p}, nil
}

// folderName refuses names that are empty, hidden, too long or contain
// characters that some disks (exFAT, FAT32) or the Finder do not accept.
func folderName(n string) error {
	switch {
	case n == "":
		return errors.New("type a name for the new folder")
	case n == "." || n == ".." || strings.HasPrefix(n, "."):
		return errors.New("a folder name cannot start with a dot")
	case len(n) > 200 || !utf8.ValidString(n):
		return errors.New("that name is too long")
	case strings.ContainsAny(n, "/\\:*?\"<>|") || strings.ContainsFunc(n, func(c rune) bool { return c < 32 }):
		return errors.New("a folder name cannot contain / \\ : * ? \" < > |")
	}
	return nil
}

// pickable is true for the folders the picker shows: the home folder and
// anything under it, and connected disks.
func pickable(p string) bool {
	home, _ := os.UserHomeDir()
	if home != "" && (p == home || strings.HasPrefix(p, home+"/")) {
		return true
	}
	for _, x := range places() {
		if (x.Kind == "disk" || x.Kind == "share") && x.Note == "" && (p == x.Path || strings.HasPrefix(p, x.Path+"/")) {
			return true
		}
	}
	return false
}
