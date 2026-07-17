package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gkgoat1/scripts/commitment"
)

func TestRunAnchorValidRoot(t *testing.T) {
	root := commitment.RootHex([32]byte{1, 2, 3})
	var out bytes.Buffer
	if err := runAnchor(root, &out); err != nil {
		t.Fatalf("runAnchor: %v", err)
	}
	if !strings.Contains(out.String(), root) {
		t.Errorf("out = %q, want it to contain %q", out.String(), root)
	}
}

func TestRunAnchorInvalidRoot(t *testing.T) {
	var out bytes.Buffer
	if err := runAnchor("not-hex", &out); err == nil {
		t.Error("runAnchor: want error for invalid hex root")
	}
}

func TestAnchorFlagsRequiresRoot(t *testing.T) {
	if _, err := anchorFlags(nil); err == nil {
		t.Error("anchorFlags: want error when -root is missing")
	}
}

func TestAnchorFlagsParsesRoot(t *testing.T) {
	root, err := anchorFlags([]string{"-root", "deadbeef"})
	if err != nil {
		t.Fatalf("anchorFlags: %v", err)
	}
	if root != "deadbeef" {
		t.Errorf("root = %q, want %q", root, "deadbeef")
	}
}
