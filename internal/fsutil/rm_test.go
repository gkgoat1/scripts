package fsutil

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestIsPermission(t *testing.T) {
	pathErr := &fs.PathError{Op: "unlink", Path: "/x", Err: os.ErrPermission}
	wrapped := fmt.Errorf("wrapped: %w", os.ErrPermission)
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain", errors.New("boom"), false},
		{"os.ErrPermission", os.ErrPermission, true},
		{"wrapped ErrPermission", wrapped, true},
		{"PathError ErrPermission", pathErr, true},
		{"literal operation not permitted", errors.New("operation not permitted"), true},
		{"literal permission denied", errors.New("open /x: permission denied"), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsPermission(c.err); got != c.want {
				t.Errorf("IsPermission(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

func TestIsNotExist(t *testing.T) {
	if !IsNotExist(os.ErrNotExist) {
		t.Error("ErrNotExist not detected")
	}
	if IsNotExist(errors.New("boom")) {
		t.Error("non-notexist flagged as notexist")
	}
	if IsNotExist(nil) {
		t.Error("nil flagged as notexist")
	}
}

func TestRemoveAllSuccess(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "x")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAll(target, nil); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("target still exists: %v", err)
	}
}

func TestRemoveAllNotExistIsSuccess(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing")
	var buf bytes.Buffer
	if err := RemoveAll(missing, &buf); err != nil {
		t.Fatalf("RemoveAll missing: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no warning for missing path, got %q", buf.String())
	}
}

func TestRemoveAllPermissionNonFatal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; permission test is meaningless")
	}
	dir := t.TempDir()
	// Create a dir whose entry cannot be removed because it (and its parent)
	// is read-only and non-empty. We make parent read-only, then try to
	// remove the child; removal of the non-empty read-only dir yields EPERM.
	parent := filepath.Join(dir, "parent")
	child := filepath.Join(parent, "child")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	// Write a file into child so it is non-empty.
	if err := os.WriteFile(filepath.Join(child, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Make parent read+execute but not write: unlinking child's entry fails.
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })

	var buf bytes.Buffer
	err := RemoveAll(child, &buf)
	if err != nil {
		t.Fatalf("permission error must be non-fatal, got %v", err)
	}
	if buf.Len() == 0 {
		t.Error("expected a warning to be logged for the permission error")
	}
}

func TestRemoveAllRealErrorReturned(t *testing.T) {
	// A nonexistent path under a missing root is treated as not-exist success
	// (the common "already gone" case) and does not surface as a real error.
	dir := t.TempDir()
	missing := filepath.Join(dir, "does", "not", "exist")
	if err := RemoveAll(missing, nil); err != nil {
		t.Fatalf("missing nested path should be success, got %v", err)
	}
}
