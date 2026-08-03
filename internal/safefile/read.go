package safefile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrEscapesRoot = errors.New("path escapes root")
	ErrSymlink     = errors.New("symbolic links are not allowed")
	ErrNotRegular  = errors.New("path is not a regular file")
	ErrTooLarge    = errors.New("file exceeds size limit")
	ErrChanged     = errors.New("path changed while opening")
)

func ExistsBeneath(rootPath, relative string) (bool, error) {
	clean, err := cleanRelative(relative)
	if err != nil {
		return false, err
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return false, fmt.Errorf("open root %q: %w", rootPath, err)
	}
	defer root.Close()
	info, err := inspect(root, clean, relative)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.Mode().IsRegular(), nil
}

func ReadBeneath(rootPath, relative string, maximum int64) ([]byte, error) {
	if maximum < 0 {
		return nil, fmt.Errorf("read %q: %w", relative, ErrTooLarge)
	}
	file, err := OpenBeneath(rootPath, relative)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", relative, err)
	}
	if int64(len(data)) > maximum {
		return nil, fmt.Errorf("read %q: %w (%d bytes maximum)", relative, ErrTooLarge, maximum)
	}
	return data, nil
}

func OpenBeneath(rootPath, relative string) (*os.File, error) {
	clean, err := cleanRelative(relative)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, fmt.Errorf("open root %q: %w", rootPath, err)
	}
	defer root.Close()

	initial, err := inspect(root, clean, relative)
	if err != nil {
		return nil, err
	}
	if !initial.Mode().IsRegular() {
		return nil, fmt.Errorf("read %q: %w", relative, ErrNotRegular)
	}

	file, err := root.Open(clean)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, fmt.Errorf("read %q: %w", relative, ErrNotRegular)
	}
	post, err := inspect(root, clean, relative)
	if err != nil {
		file.Close()
		return nil, err
	}
	if !os.SameFile(initial, info) || !os.SameFile(info, post) {
		file.Close()
		return nil, fmt.Errorf("read %q: %w", relative, ErrChanged)
	}
	return file, nil
}

func cleanRelative(relative string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(relative))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("read %q: %w", relative, ErrEscapesRoot)
	}
	return clean, nil
}

func inspect(root *os.Root, clean, original string) (os.FileInfo, error) {
	parts := strings.Split(clean, string(filepath.Separator))
	prefix := ""
	var final os.FileInfo
	for index, part := range parts {
		if prefix == "" {
			prefix = part
		} else {
			prefix = filepath.Join(prefix, part)
		}
		info, err := root.Lstat(prefix)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("read %q: %w", original, ErrSymlink)
		}
		if index < len(parts)-1 && !info.IsDir() {
			return nil, fmt.Errorf("read %q: component %q is not a directory", original, prefix)
		}
		final = info
	}
	return final, nil
}
