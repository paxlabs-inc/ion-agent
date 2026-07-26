package sandbox

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestFilesRoundTripAndAtomicConcurrentWrites(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	files, err := Open(root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()

	if err := files.WriteFile("nested/value.txt", []byte("first")); err != nil {
		t.Fatal(err)
	}
	content, err := files.ReadFile("nested/value.txt")
	if err != nil || string(content) != "first" {
		t.Fatalf("ReadFile() = %q, %v", content, err)
	}
	info, err := os.Stat(filepath.Join(root, "nested", "value.txt"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, error = %v", info.Mode().Perm(), err)
	}

	values := [][]byte{bytes.Repeat([]byte("a"), 512), bytes.Repeat([]byte("b"), 512)}
	var wait sync.WaitGroup
	for _, value := range values {
		value := value
		wait.Add(1)
		go func() {
			defer wait.Done()
			if err := files.WriteFile("nested/value.txt", value); err != nil {
				t.Errorf("WriteFile() error = %v", err)
			}
		}()
	}
	wait.Wait()
	content, err = files.ReadFile("nested/value.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(content, values[0]) && !bytes.Equal(content, values[1]) {
		t.Fatal("concurrent write produced a partial file")
	}
}

func TestFilesBlocksTraversalAndSymlinks(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret")
	if err := os.WriteFile(secret, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "secret-link")); err != nil {
		t.Fatal(err)
	}
	files, err := Open(root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()

	for _, path := range []string{"../secret", "/etc/passwd", "escape/secret", "secret-link"} {
		if _, err := files.ReadFile(path); err == nil {
			t.Fatalf("ReadFile(%q) escaped sandbox", path)
		}
		if err := files.WriteFile(path, []byte("attack")); err == nil {
			t.Fatalf("WriteFile(%q) escaped sandbox", path)
		}
	}
	content, err := os.ReadFile(secret)
	if err != nil || string(content) != "outside" {
		t.Fatalf("outside file changed: %q, %v", content, err)
	}
}

func TestFilesRejectsOversizeNonRegularAndClosedAccess(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "large"), bytes.Repeat([]byte{1}, 5), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	files, err := Open(root, 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := files.ReadFile("large"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversize error = %v", err)
	}
	if _, err := files.ReadFile("directory"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("directory error = %v", err)
	}
	if err := files.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := files.ReadFile("large"); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed read error = %v", err)
	}
	if err := files.WriteFile("large", nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed write error = %v", err)
	}
}
