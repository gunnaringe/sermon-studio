package normalizer

import (
	"bytes"
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

func exists(path string) bool { return Exists(path) }

func firstExisting(paths ...string) string {
	for _, s := range paths {
		if exists(s) {
			return s
		}
	}
	return ""
}

// MoveFile renames src to dst, falling back to copy and delete when they are
// on different file systems (e.g. an archive on another disk).
func MoveFile(src, dst string) error {
	if err := Rename(src, dst); err == nil {
		return nil
	}
	tmp := dst + ".partial"
	if err := CopyFile(src, tmp); err != nil {
		Remove(tmp)
		return err
	}
	// The source may be the only copy of the recording: verify before deleting it.
	same, err := SameContent(src, tmp)
	if err != nil {
		Remove(tmp)
		return err
	}
	if !same {
		Remove(tmp)
		return fmt.Errorf("copy of %s differs from the original; the original is kept", filepath.Base(src))
	}
	if err := Rename(tmp, dst); err != nil {
		return err
	}
	return Remove(src)
}

// SameContent reports whether a and b have the same bytes.
func SameContent(a, b string) (bool, error) {
	ha, err := fileHash(a)
	if err != nil {
		return false, err
	}
	hb, err := fileHash(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(ha, hb), nil
}

func fileHash(path string) ([]byte, error) {
	if err := NeedFile(context.Background(), path); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

// CopyFile copies src to dst and syncs it to disk.
func CopyFile(src, dst string) error {
	if err := NeedFile(context.Background(), src); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Rename, Remove and RemoveAll are os.Rename, os.Remove and os.RemoveAll that try again for a few seconds
// while Windows says the file is in use: a preview being played, a virus scanner looking at a new file.
// Elsewhere a file in use can be renamed and removed, and they are the os ones.
func Rename(oldpath, newpath string) error {
	if err := needFile(context.Background(), oldpath, false); err != nil { // kept elsewhere: renamed on the disk
		return err
	}
	err := retry(func() error { return os.Rename(oldpath, newpath) })
	if err == nil && Changed != nil {
		Changed(oldpath, newpath)
	}
	return err
}

func Remove(path string) error {
	st, lerr := os.Lstat(path)
	if errors.Is(lerr, fs.ErrNotExist) && Forget != nil {
		if _, ok := elsewhere(path); ok {
			return Forget(path)
		}
	}
	err := retry(func() error { return os.Remove(path) })
	// An empty folder has nothing to remove elsewhere: only files kept there may be under it.
	if err == nil && Changed != nil && (lerr != nil || !st.IsDir()) {
		Changed(path, "")
	}
	return err
}

// Changed, when set, is told of every Rename (from, to) and Remove or RemoveAll (from, "") that succeeded: how
// object storage follows the studio's files (web/mirror.go). It is set once, before any file is touched, and
// must return quickly.
var Changed func(from, to string)

// ReadFile is os.ReadFile, tried again while Windows says the file is in use: it is while another goroutine
// renames a new version over it (writeFileAtomic), where Linux and macOS give the old one or the new.
func ReadFile(path string) ([]byte, error) {
	var data []byte
	err := retry(func() (err error) {
		data, err = os.ReadFile(path)
		return err
	})
	return data, err
}

func RemoveAll(path string) error {
	err := retry(func() error { return os.RemoveAll(path) })
	if err == nil && Changed != nil {
		Changed(path, "")
	}
	return err
}

func retry(op func() error) error {
	err := op()
	for wait := 25 * time.Millisecond; err != nil && inUse(err) && wait <= 1600*time.Millisecond; wait *= 2 {
		time.Sleep(wait)
		err = op()
	}
	return err
}
