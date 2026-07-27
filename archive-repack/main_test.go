package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

func TestRunExpandsArchives(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "incoming")
	if err := os.Mkdir(source, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(source, "plain.txt"), "plain")

	zipPath := filepath.Join(source, "nested.zip")
	zipOut, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zipWriter := zip.NewWriter(zipOut)
	member, err := zipWriter.Create("inside.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(member, "zip"); err != nil {
		t.Fatal(err)
	}
	if err := zipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zipOut.Close(); err != nil {
		t.Fatal(err)
	}

	gzipPath := filepath.Join(source, "single.gz")
	gzipOut, err := os.Create(gzipPath)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(gzipOut)
	if _, err := io.WriteString(gzipWriter, "gzip"); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipOut.Close(); err != nil {
		t.Fatal(err)
	}

	destination := filepath.Join(dir, "result.tar.gz")
	if err := run(source, destination); err != nil {
		t.Fatal(err)
	}
	got := tarContents(t, destination, true)
	want := map[string]string{
		"incoming":                   "",
		"incoming/plain.txt":         "plain",
		"incoming/nested":            "",
		"incoming/nested/inside.txt": "zip",
		"incoming/single":            "gzip",
	}
	if len(got) != len(want) {
		t.Fatalf("member count = %d, want %d: %#v", len(got), len(want), got)
	}
	for name, contents := range want {
		if got[name] != contents {
			t.Errorf("member %q = %q, want %q", name, got[name], contents)
		}
	}
}

func TestFileTooLargeStartsContinuation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("RLIMIT_FSIZE is unavailable on Windows")
	}
	if os.Getenv("ARCHIVE_REPACK_FSIZE_HELPER") == "1" {
		limit := syscall.Rlimit{Cur: 3072, Max: 3072}
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limit); err != nil {
			os.Exit(3)
		}
		if err := run(os.Getenv("ARCHIVE_REPACK_SOURCE"), os.Getenv("ARCHIVE_REPACK_DESTINATION")); err != nil {
			os.Exit(4)
		}
		os.Exit(0)
	}

	dir := t.TempDir()
	source := filepath.Join(dir, "in")
	if err := os.Mkdir(source, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(source, "one"), string(bytes.Repeat([]byte{'a'}, 100)))
	writeFile(t, filepath.Join(source, "two"), string(bytes.Repeat([]byte{'b'}, 1200)))
	destination := filepath.Join(dir, "out.tar")

	command := exec.Command(os.Args[0], "-test.run=TestFileTooLargeStartsContinuation")
	command.Env = append(os.Environ(), "ARCHIVE_REPACK_FSIZE_HELPER=1", "ARCHIVE_REPACK_SOURCE="+source, "ARCHIVE_REPACK_DESTINATION="+destination)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("helper failed: %v\n%s", err, output)
	}

	first := tarContents(t, destination, false)
	second := tarContents(t, filepath.Join(dir, "out-2.tar"), false)
	if first["in/one"] != string(bytes.Repeat([]byte{'a'}, 100)) || len(first) != 2 {
		t.Fatalf("first archive = %#v, want root and one", first)
	}
	if second["in/two"] != string(bytes.Repeat([]byte{'b'}, 1200)) || len(second) != 1 {
		t.Fatalf("continuation archive = %#v, want two", second)
	}
}

func TestIsFileTooLargeOnlyAcceptsDestinationErrors(t *testing.T) {
	outputErr := &destinationWriteError{err: syscall.EFBIG}
	if !isFileTooLarge(outputErr) {
		t.Fatal("destination EFBIG was not recognized")
	}
	if isFileTooLarge(errors.New("file too large")) {
		t.Fatal("an input error must not start a continuation archive")
	}
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func tarContents(t *testing.T, path string, compressed bool) map[string]string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var reader io.Reader = file
	var gzipReader *gzip.Reader
	if compressed {
		gzipReader, err = gzip.NewReader(file)
		if err != nil {
			t.Fatal(err)
		}
		defer gzipReader.Close()
		reader = gzipReader
	}
	archive := tar.NewReader(reader)
	contents := make(map[string]string)
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeDir {
			contents[header.Name] = ""
			continue
		}
		data, err := io.ReadAll(archive)
		if err != nil {
			t.Fatal(err)
		}
		contents[header.Name] = string(data)
	}
	return contents
}

func TestSafeMemberPath(t *testing.T) {
	cases := []string{"../escape", "/absolute", `C:\\drive`}
	for _, name := range cases {
		if _, err := safeMemberPath(name); err == nil {
			t.Errorf("safeMemberPath(%q) accepted an unsafe path", name)
		}
	}
}
