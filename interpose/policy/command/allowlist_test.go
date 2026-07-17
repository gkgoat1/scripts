package command

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAllowsMatchesOnlyExactRules(t *testing.T) {
	list := Allowlist{"kill": {{"-0", "{pid}"}}}
	for _, args := range [][]string{{"-0", "0"}, {"-0", "12345"}} {
		if !list.Allows("kill", args) {
			t.Errorf("Allows(kill, %q) = false, want true", args)
		}
	}
	for _, args := range [][]string{{"-0", "-1"}, {"-0", "1x"}, {"-9", "1"}, {"-0", "1", "extra"}} {
		if list.Allows("kill", args) {
			t.Errorf("Allows(kill, %q) = true, want false", args)
		}
	}
}

func TestLoadUsesEmbeddedDefaultOnlyWhenConfigMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")
	list, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !list.Allows("kill", []string{"-0", "42"}) {
		t.Fatal("embedded default should allow a PID liveness probe")
	}
	if err := os.WriteFile(path, []byte(`{"kill":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	list, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if list.Allows("kill", []string{"-0", "42"}) {
		t.Fatal("explicit config must replace the embedded default")
	}
}
