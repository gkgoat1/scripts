package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gkgoat1/scripts/commitment"
	interposeconfig "github.com/gkgoat1/scripts/interpose/config"
	interposecommand "github.com/gkgoat1/scripts/interpose/policy/command"
	pconfig "github.com/gkgoat1/scripts/pulse/config"
)

func setFakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config", "interpose"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	interposeconfig.Reset()
	t.Cleanup(interposeconfig.Reset)
	return home
}

func writePulseConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunCommitWritesSidecarsAndPrintsMatchingRoot(t *testing.T) {
	home := setFakeHome(t)
	if err := os.WriteFile(filepath.Join(home, ".config", "interpose", "config"), []byte("extra-protected-path: /secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	pulseCfgPath := filepath.Join(home, "jobs")
	writePulseConfig(t, pulseCfgPath, "job: a\ninterval: 1m\ncommand: echo a\n\njob: b\ninterval: 2m\ncommand: echo b\n")

	var out, errBuf bytes.Buffer
	root, err := runCommit(pulseCfgPath, &out, &errBuf)
	if err != nil {
		t.Fatalf("runCommit: %v", err)
	}

	if printed := strings.TrimSpace(out.String()); printed != commitment.RootHex(root) {
		t.Errorf("printed root = %q, want %q", printed, commitment.RootHex(root))
	}

	// Independently recompute the expected root from the same inputs, via
	// the same CommitLeaf() methods runCommit itself uses.
	jobs, err := pconfig.LoadConfig(pulseCfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	var leaves []commitment.Leaf
	for _, j := range jobs {
		leaves = append(leaves, j.CommitLeaf())
	}
	leaves = append(leaves, interposeconfig.Load().CommitLeaf())
	commandPolicy, err := interposecommand.Load(interposecommand.DefaultConfigPath())
	if err != nil {
		t.Fatalf("Load command allowlist: %v", err)
	}
	leaves = append(leaves, commandPolicy.CommitLeaf())
	wantTree, err := commitment.Build(leaves)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if root != wantTree.Root() {
		t.Errorf("root = %x, want %x", root, wantTree.Root())
	}

	// Pulse sidecar: both jobs verify against root.
	pulseProofData, err := os.ReadFile(pulseCfgPath + ".proof")
	if err != nil {
		t.Fatalf("read pulse proof sidecar: %v", err)
	}
	pulsePF, err := commitment.DecodeProofFile(pulseProofData)
	if err != nil {
		t.Fatalf("decode pulse proof sidecar: %v", err)
	}
	for _, j := range jobs {
		proof, ok := pulsePF.Entries[j.Name]
		if !ok {
			t.Fatalf("no proof entry for job %q", j.Name)
		}
		if !commitment.VerifyProof(j.CommitLeaf(), proof, root) {
			t.Errorf("job %q proof does not verify against root", j.Name)
		}
	}

	// Interpose sidecar: policy entry verifies against root.
	interposeProofData, err := os.ReadFile(interposeconfig.DefaultConfigPath() + ".proof")
	if err != nil {
		t.Fatalf("read interpose proof sidecar: %v", err)
	}
	interposePF, err := commitment.DecodeProofFile(interposeProofData)
	if err != nil {
		t.Fatalf("decode interpose proof sidecar: %v", err)
	}
	policyProof, ok := interposePF.Entries[interposeconfig.PolicyLeafID]
	if !ok {
		t.Fatal("no policy proof entry")
	}
	if !commitment.VerifyProof(interposeconfig.Load().CommitLeaf(), policyProof, root) {
		t.Error("policy proof does not verify against root")
	}
	// Command-policy sidecar: allowlist entry verifies against root.
	commandProofData, err := os.ReadFile(interposecommand.DefaultConfigPath() + ".proof")
	if err != nil {
		t.Fatalf("read command-policy proof sidecar: %v", err)
	}
	commandPF, err := commitment.DecodeProofFile(commandProofData)
	if err != nil {
		t.Fatalf("decode command-policy proof sidecar: %v", err)
	}
	commandProof, ok := commandPF.Entries[interposecommand.PolicyLeafID]
	if !ok {
		t.Fatal("no command-policy proof entry")
	}
	if !commitment.VerifyProof(commandPolicy.CommitLeaf(), commandProof, root) {
		t.Error("command-policy proof does not verify against root")
	}
}

func TestRunCommitNoPulseConfigSkipsPulseSidecar(t *testing.T) {
	home := setFakeHome(t)
	missing := filepath.Join(home, "jobs-does-not-exist")

	var out, errBuf bytes.Buffer
	root, err := runCommit(missing, &out, &errBuf)
	if err != nil {
		t.Fatalf("runCommit: %v", err)
	}
	if !strings.Contains(errBuf.String(), "[skip] pulse: no config found") {
		t.Errorf("errBuf = %q, want a [skip] line", errBuf.String())
	}
	if _, err := os.Stat(missing + ".proof"); !os.IsNotExist(err) {
		t.Error("no pulse proof sidecar should be written when pulse has no config")
	}

	// Root should equal a tree built from the default interpose and command-policy leaves.
	commandPolicy, err := interposecommand.Load(interposecommand.DefaultConfigPath())
	if err != nil {
		t.Fatalf("Load command allowlist: %v", err)
	}
	wantTree, err := commitment.Build([]commitment.Leaf{interposeconfig.Load().CommitLeaf(), commandPolicy.CommitLeaf()})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if root != wantTree.Root() {
		t.Errorf("root = %x, want %x (policy-only)", root, wantTree.Root())
	}
}

func TestRunCommitMalformedPulseConfigIsFatal(t *testing.T) {
	home := setFakeHome(t)
	pulseCfgPath := filepath.Join(home, "jobs")
	writePulseConfig(t, pulseCfgPath, "job: a\ninterval: not-a-duration\ncommand: echo a\n")

	var out, errBuf bytes.Buffer
	if _, err := runCommit(pulseCfgPath, &out, &errBuf); err == nil {
		t.Error("runCommit: want error for a malformed (as opposed to absent) pulse config")
	}
}

func TestRunCommitCreatesCommandPolicyParentDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	interposeconfig.Reset()
	t.Cleanup(interposeconfig.Reset)

	missing := filepath.Join(home, "jobs-does-not-exist")
	if _, err := runCommit(missing, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("runCommit: %v", err)
	}
	if _, err := os.Stat(interposecommand.DefaultConfigPath() + ".proof"); err != nil {
		t.Fatalf("command-policy proof sidecar: %v", err)
	}
}
