package prtag

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReadWriteFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".prtag")
	in := File{Name: "proj", Body: "hello\n", MetaHeader: "metadata"}
	if err := WriteFile(path, in, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != in.Name || got.Body != in.Body {
		t.Errorf("got = %+v want %+v", got, in)
	}
}

func TestFormatErrors(t *testing.T) {
	cases := []File{
		{Name: ""},
		{Name: "bad:name"},
		{MetaHeader: "bad[header"},
	}
	for i, f := range cases {
		if _, err := Format(f); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
}

func TestParseEmptyFile(t *testing.T) {
	_, err := Parse([]byte(""))
	if !errors.Is(err, ErrInvalidHeader) {
		t.Errorf("err = %v", err)
	}
}

func TestParseCRLF(t *testing.T) {
	f, err := Parse([]byte("proj:\r\nbody\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if f.Body != "body\n" {
		t.Errorf("body = %q", f.Body)
	}
}

func TestSentinelErrors(t *testing.T) {
	cases := map[string]error{
		"nope\n":                    ErrInvalidHeader,
		"proj:\n---\n":              ErrInvalidDelimiter,
		"proj:\n---\n[metadata]\nkey: v\n": ErrNonEmptyMetadata,
	}
	for in, want := range cases {
		_, err := Parse([]byte(in))
		if !errors.Is(err, want) {
			t.Errorf("Parse(%q) err = %v want %v", in, err, want)
		}
	}
}

func TestWriteFileCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".prtag")
	if err := WriteFile(path, File{Name: "x"}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
