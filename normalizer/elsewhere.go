package normalizer

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Files kept elsewhere (object storage, #86; web/fetch.go): a file of a recording can be only in the bucket,
// not on the disk. The pipeline finds it as if it were there (exists, Source, Stat), and fetches it before
// anything reads it (Need: every ffmpeg input, a copy, a rename). The hooks are set once, before any file is
// touched; unset, every file is on the disk.
var (
	// Elsewhere is the files of dir kept elsewhere and not on the disk, by name.
	Elsewhere func(dir string) map[string]fs.FileInfo
	// Need puts a file kept elsewhere on the disk.
	Need func(ctx context.Context, path string) error
	// Forget removes a file kept elsewhere: there, and from what tells of it.
	Forget func(path string) error
)

// elsewhere is what Elsewhere tells of path, if it is kept elsewhere.
func elsewhere(path string) (fs.FileInfo, bool) {
	if Elsewhere == nil {
		return nil, false
	}
	info, ok := Elsewhere(filepath.Dir(path))[filepath.Base(path)]
	return info, ok
}

// Exists reports whether path is a file or folder, on the disk or kept elsewhere.
func Exists(path string) bool {
	_, err := Stat(path)
	return err == nil
}

// Stat is os.Stat, or for a file kept elsewhere what is known of it (its size and time).
func Stat(path string) (fs.FileInfo, error) {
	st, err := os.Stat(path)
	if err != nil && errors.Is(err, fs.ErrNotExist) {
		if info, ok := elsewhere(path); ok {
			return info, nil
		}
	}
	return st, err
}

// NeedFile puts path on the disk if it is kept elsewhere; for any other path it does nothing (a missing file
// fails where it is read, as before).
func NeedFile(ctx context.Context, path string) error {
	if Need == nil {
		return nil
	}
	if _, err := os.Lstat(path); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if _, ok := elsewhere(path); !ok {
		return nil
	}
	return Need(ctx, path)
}

// NeedInputs puts the input files of ffmpeg's arguments (-i) on the disk.
func NeedInputs(ctx context.Context, args []string) error {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-i" {
			if err := NeedFile(ctx, args[i+1]); err != nil {
				return err
			}
		}
	}
	return nil
}
