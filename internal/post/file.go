package post

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func HighestNumber(dir string) (int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	var highest int64
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSuffix(e.Name(), ".md"), 10, 64)
		if err == nil && n > highest {
			highest = n
		}
	}
	return highest, nil
}

// WriteFile atomically creates a file without replacing an existing post.
// Identical existing bytes are accepted to recover a crash after linking the file.
func WriteFile(name string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(name), ".post-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Link(f.Name(), name); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		existing, readErr := os.ReadFile(name)
		if readErr != nil {
			return readErr
		}
		if !bytes.Equal(existing, data) {
			return fmt.Errorf("refusing to replace existing file %s", name)
		}
	}
	dir, err := os.Open(filepath.Dir(name))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
