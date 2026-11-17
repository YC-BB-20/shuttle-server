package copyjob

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/YC-BB-20/shuttle-server/internal/diskio"
)

// The destination's format, checked before a copy is added (v0.2.92,
// DESIGN.md section 15 proposal 7). Counts only, never a list of files.
// Files that FAT32 cannot hold fail at once when their turn comes, rather
// than after 4 GB has been written. Names and case clashes are only
// warned about: the copy still tries them, and a refusal fails that file.

// fat32Max is the largest file FAT32 can hold: 4 GiB minus one byte.
const fat32Max = 1<<32 - 1

type destFormat struct {
	Name     string // as the owner knows it: FAT32, exFAT, NTFS, APFS…
	MaxFile  int64  // 0: no limit Shuttle knows of
	Strict   bool   // may refuse \ : * ? " < > | and control characters
	FoldCase bool   // names that differ only by case are the same file
}

var formatNames = map[string]string{"msdos": "FAT32", "vfat": "FAT32", "fat32": "FAT32", "exfat": "exFAT",
	"ntfs": "NTFS", "ntfs3": "NTFS", "apfs": "APFS", "hfs": "Mac OS Extended", "ext4": "ext4", "btrfs": "Btrfs", "xfs": "XFS",
	"smbfs": "network share (SMB)", "cifs": "network share (SMB)", "smb3": "network share (SMB)"}

// formatOf reads the format of the disk that holds dir (an existing folder).
func formatOf(dir string) destFormat {
	v, err := diskio.VolumeOf(dir)
	if err != nil {
		return destFormat{}
	}
	t := strings.ToLower(v.Type)
	f := destFormat{Name: formatNames[t]}
	if f.Name == "" {
		f.Name = v.Type
	}
	switch t {
	case "msdos", "vfat", "fat32":
		f.MaxFile, f.Strict = fat32Max, true
	case "exfat", "ntfs", "ntfs3", "smbfs", "cifs", "smb3":
		f.Strict = true // an SMB server usually keeps Windows' name rules (v0.2.97)
	}
	folds, known := foldsCase(dir, v.Mount)
	if !known {
		switch t { // the usual setting for the format
		case "msdos", "vfat", "fat32", "exfat", "ntfs", "ntfs3", "apfs", "hfs", "smbfs", "cifs", "smb3":
			folds = true
		}
	}
	f.FoldCase = folds
	return f
}

// foldsCase asks the disk itself: a folder below the mount point is looked
// up with its letters' case flipped. Only reads; known is false when no
// folder there has a letter to flip.
func foldsCase(dir, mount string) (folds, known bool) {
	dir = filepath.Clean(dir)
	for d := dir; d != mount && len(d) > len(mount); d = filepath.Dir(d) {
		base := filepath.Base(d)
		flip := strings.Map(func(r rune) rune {
			if unicode.IsUpper(r) {
				return unicode.ToLower(r)
			}
			return unicode.ToUpper(r)
		}, base)
		if flip == base {
			continue
		}
		a, err := os.Stat(d)
		if err != nil {
			return false, false
		}
		b, err := os.Stat(filepath.Join(filepath.Dir(d), flip))
		return err == nil && os.SameFile(a, b), true
	}
	return false, false
}

// badName says whether a path holds characters FAT32, exFAT or NTFS may
// refuse.
func badName(rel string) bool {
	return strings.ContainsAny(rel, "\\:*?\"<>|") || strings.ContainsFunc(rel, func(r rune) bool { return r < 32 })
}

// FormatShort counts the files the destination's format may not take.
type FormatShort struct {
	Disk        string `json:"disk"`
	Format      string `json:"format"`
	TooBig      int    `json:"too_big"`       // over FAT32's 4 GB: these fail
	TooBigBytes int64  `json:"too_big_bytes"` //
	Names       int    `json:"names"`         // characters the format may refuse
	Clashes     int    `json:"clashes"`       // names that differ only by case
	Message     string `json:"message"`
}

// FormatError is returned by Add when some files may not fit the
// destination's format; adding again with IgnoreFormat copies the rest.
type FormatError struct{ Short FormatShort }

func (e *FormatError) Error() string { return e.Short.Message }

// Bounds on what the case check keeps in memory: one folder's names, and
// the folder paths seen. Past them it checks less, never more.
const (
	maxFolderNames = 100_000
	maxFolders     = 50_000
)

// fitCheck counts, while a source is listed, the files the destination's
// format may not take. Files of one folder arrive together (location.Walk),
// so only that folder's names are held.
type fitCheck struct {
	f       destFormat
	clash   bool // the destination folds case and the source does not
	s       FormatShort
	dir     string
	dirBad  bool
	names   map[string]string
	folders map[string]string
}

// newFitCheck prepares the check for copying src into dst (both existing
// folders). It returns nil when nothing about dst's format needs checking.
func (m *Manager) newFitCheck(src, dst string) *fitCheck {
	f := m.formatFor(dst)
	srcFolds := true // a source that folds case cannot hold two such names
	if m.formatHook != nil {
		srcFolds = m.formatHook(src).FoldCase
	} else if v, err := diskio.VolumeOf(src); err == nil {
		folds, known := foldsCase(src, v.Mount)
		srcFolds = folds || !known && formatOf(src).FoldCase
	}
	c := &fitCheck{f: f, clash: f.FoldCase && !srcFolds}
	if f.MaxFile == 0 && !f.Strict && !c.clash {
		return nil
	}
	c.s.Format = f.Name
	return c
}

func (m *Manager) formatFor(dir string) destFormat {
	if m.formatHook != nil {
		return m.formatHook(dir)
	}
	return formatOf(dir)
}

func (c *fitCheck) add(rel string, size int64) {
	if c == nil {
		return
	}
	if c.f.MaxFile > 0 && size > c.f.MaxFile {
		c.s.TooBig++
		c.s.TooBigBytes += size
	}
	if c.f.Strict && badName(rel) {
		c.s.Names++
	}
	if !c.clash {
		return
	}
	parent, name := path.Split(rel)
	if parent != c.dir || c.names == nil {
		c.dir, c.dirBad, c.names = parent, false, map[string]string{}
		if c.folders == nil {
			c.folders = map[string]string{}
		}
		low := strings.ToLower(parent)
		if prev, ok := c.folders[low]; ok && prev != parent {
			c.dirBad = true // another folder with this name in another case
		} else if !ok && len(c.folders) < maxFolders {
			c.folders[low] = parent
		}
	}
	if c.dirBad {
		c.s.Clashes++
		return
	}
	low := strings.ToLower(name)
	if prev, ok := c.names[low]; ok && prev != name {
		c.s.Clashes++
	} else if !ok && len(c.names) < maxFolderNames {
		c.names[low] = name
	}
}

// short is the finding, or nil when every file fits the format.
func (c *fitCheck) short(disk string) *FormatShort {
	if c == nil || c.s.TooBig+c.s.Names+c.s.Clashes == 0 {
		return nil
	}
	s := c.s
	s.Disk = disk
	var parts []string
	if s.TooBig > 0 {
		parts = append(parts, fmt.Sprintf("%s over 4 GB (%s in all) cannot be copied: %s is formatted %s, which holds files up to 4 GB. %s fail at once, the rest are copied",
			plural(s.TooBig, "file", "files"), Size(s.TooBigBytes), disk, s.Format, theyIt(s.TooBig)))
	}
	if s.Names > 0 {
		parts = append(parts, fmt.Sprintf("%s characters (\\ : * ? \" < > |) that a %s disk may refuse; each is tried, and fails if the disk refuses it",
			plural(s.Names, "file name has", "file names have"), s.Format))
	}
	if s.Clashes > 0 {
		parts = append(parts, fmt.Sprintf("%s the same as another except for capital letters; %s treats them as one name, so only the first is copied and the others fail without touching it",
			plural(s.Clashes, "file name is", "file names are"), disk))
	}
	s.Message = strings.Join(parts, ". ") + "."
	return &s
}

func theyIt(n int) string {
	if n == 1 {
		return "It will"
	}
	return "They will"
}

// unfit is the reason a file cannot be copied to a destination of format
// f, or "" (only what is certain: FAT32's 4 GB limit).
func unfit(f destFormat, size int64) string {
	if f.MaxFile > 0 && size > f.MaxFile {
		return fmt.Sprintf("too big for the destination disk: it is formatted %s, which holds files up to 4 GB", f.Name)
	}
	return ""
}
