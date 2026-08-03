package safefile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReadBeneathRejectsEscapesSymlinksAndLargeFiles(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "regular"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if data, err := ReadBeneath(root, "regular", 5); err != nil || string(data) != "hello" {
		t.Fatalf("regular read = %q, %v", data, err)
	}
	if _, err := ReadBeneath(root, "../secret", 100); !errors.Is(err, ErrEscapesRoot) {
		t.Fatalf("escape error = %v", err)
	}
	if _, err := ReadBeneath(root, "link", 100); !errors.Is(err, ErrSymlink) {
		t.Fatalf("symlink error = %v", err)
	}
	if _, err := ReadBeneath(root, "regular", 4); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("size error = %v", err)
	}
}
