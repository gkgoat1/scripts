package wrappers

import (
	"bufio"
	"strings"
	"testing"
)

func TestReadPINPreservesSixRunes(t *testing.T) {
	got, err := readPIN(bufio.NewReader(strings.NewReader("éabcde\n")))
	if err != nil {
		t.Fatal(err)
	}
	if got != "éabcde" {
		t.Errorf("readPIN = %q", got)
	}
}
