package simulation

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// FileSnapshot stores one complete snapshot in an existing directory. It needs
// a single writer and a local filesystem with atomic same-directory rename and
// reliable file/directory Sync semantics (tested on macOS; intended for Unix).
// It does not implement interprocess locking or storage-corruption recovery.
type FileSnapshot struct{ Path string }

func (s FileSnapshot) Load() ([]byte, error) {
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err == nil && len(data) == 0 {
		return nil, errors.New("empty snapshot file")
	}
	return data, err
}

func (s FileSnapshot) Save(data []byte) error {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return errors.New("file snapshots require Linux or macOS rename/sync semantics")
	}
	dir := filepath.Dir(s.Path)
	f, err := os.CreateTemp(dir, ".attribution-snapshot-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), s.Path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
