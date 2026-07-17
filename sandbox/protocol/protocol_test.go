package protocol

import (
	"bytes"
	"strings"
	"testing"
)

func TestExecRequestRoundTrip(t *testing.T) {
	want := ExecRequest{Path: "/usr/bin/git", Dir: "/guest/repo", Argv: []string{"git", "reset", "--hard"}, Env: []string{"HOME=/guest", "PATH=/usr/bin"}}
	data, err := EncodeExecRequest(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeExecRequest(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != want.Path || got.Dir != want.Dir || strings.Join(got.Argv, "\x00") != strings.Join(want.Argv, "\x00") || strings.Join(got.Env, "\x00") != strings.Join(want.Env, "\x00") {
		t.Fatalf("got %#v want %#v", got, want)
	}
}
func TestFrameRejectsOversizeAndTrailingData(t *testing.T) {
	var b bytes.Buffer
	if err := WriteFrame(&b, TypeExecReq, []byte("x")); err != nil {
		t.Fatal(err)
	}
	typ, data, err := ReadFrame(&b)
	if err != nil || typ != TypeExecReq || string(data) != "x" {
		t.Fatalf("frame = %d %q %v", typ, data, err)
	}
	if _, err := DecodeExecRequest(append([]byte{0, 0, 0, 0, 0, 0, 0, 0}, 1)); err == nil {
		t.Fatal("accepted trailing data")
	}
	var hdr bytes.Buffer
	hdr.Write([]byte{0, 16, 0, 1})
	if _, _, err := ReadFrame(&hdr); err == nil {
		t.Fatal("accepted oversized frame")
	}
}
func TestOperationRoundTrip(t *testing.T) {
	want := Operation{ID: 4, Kind: OpRun, Path: "/usr/bin/git", Args: []string{"-C", "/guest", "branch"}, Dir: "/guest", Env: []string{"HOME=/guest"}, Capture: true}
	data, err := EncodeOperation(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeOperation(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != want.ID || got.Kind != want.Kind || got.Path != want.Path || strings.Join(got.Args, " ") != strings.Join(want.Args, " ") || !got.Capture {
		t.Fatalf("got %#v", got)
	}
}
