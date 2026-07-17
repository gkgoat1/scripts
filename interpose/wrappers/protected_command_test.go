package wrappers

import (
	"bufio"
	"regexp"
	"strings"
	"testing"
)

func TestReadPINTrimsLineEnding(t *testing.T) {
	got, err := readPIN(bufio.NewReader(strings.NewReader("042069\r\n")))
	if err != nil {
		t.Fatal(err)
	}
	if got != "042069" {
		t.Errorf("readPIN = %q", got)
	}
}

func TestNewConfirmationPINIsSixDigits(t *testing.T) {
	for range 20 {
		pin, err := newConfirmationPIN()
		if err != nil {
			t.Fatal(err)
		}
		if !regexp.MustCompile(`^[0-9]{6}$`).MatchString(pin) {
			t.Errorf("newConfirmationPIN() = %q, want six digits", pin)
		}
	}
}