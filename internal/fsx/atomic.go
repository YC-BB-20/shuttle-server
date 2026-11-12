// Package fsx holds the crash-safe file helpers every state write goes through.
package fsx

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// WriteFileAtomic writes data to a temporary file in the same folder, flushes
// it to the drive, and renames it over path, so path is always either the old
// or the new content, never a mix. On macOS, File.Sync issues F_FULLFSYNC.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) (err error) {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			os.Remove(tmp) // our own temporary file, never a user's file
		}
	}()
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Chmod(tmp, perm); err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	return syncDir(dir)
}

// syncDir flushes the folder entry so the rename itself survives a power cut.
// Some file systems refuse to flush a folder; that is not an error here.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	if err := d.Sync(); err != nil && !errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.ENOTSUP) && !errors.Is(err, syscall.EBADF) {
		return err
	}
	return nil
}
