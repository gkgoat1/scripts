package anchor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gkgoat1/scripts/commitment"
)

type fakePlistToJSON struct {
	json []byte
	err  error
}

func (f fakePlistToJSON) Convert(path string) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.json, nil
}

// placeholderPlist creates an empty file at a temp path so os.Stat succeeds;
// its content is irrelevant since Convert is always faked out.
func placeholderPlist(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), Label+".plist")
	if err := os.WriteFile(path, []byte("placeholder"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadRootNotInstalledWhenPlistMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist.plist")
	r := PlistAnchorReader{Converter: fakePlistToJSON{}, Path: missing}

	_, err := r.ReadRoot()
	if err != ErrAnchorNotInstalled {
		t.Errorf("err = %v, want ErrAnchorNotInstalled", err)
	}
}

func TestReadRootSuccess(t *testing.T) {
	leaf := commitment.Leaf{Tool: "pulse", ID: "a", Kind: commitment.KindCommand, Payload: []byte("x")}
	tree, err := commitment.Build([]commitment.Leaf{leaf})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	root := tree.Root()

	path := placeholderPlist(t)
	docJSON := fmt.Sprintf(`{"Label":%q,"ProgramArguments":["/path/to/agentcommit","anchor","-root",%q]}`, Label, commitment.RootHex(root))
	r := PlistAnchorReader{Converter: fakePlistToJSON{json: []byte(docJSON)}, Path: path}

	got, err := r.ReadRoot()
	if err != nil {
		t.Fatalf("ReadRoot: %v", err)
	}
	if got != root {
		t.Errorf("ReadRoot = %x, want %x", got, root)
	}
}

func TestReadRootMissingRootArgument(t *testing.T) {
	path := placeholderPlist(t)
	docJSON := fmt.Sprintf(`{"Label":%q,"ProgramArguments":["/path/to/agentcommit","anchor"]}`, Label)
	r := PlistAnchorReader{Converter: fakePlistToJSON{json: []byte(docJSON)}, Path: path}

	if _, err := r.ReadRoot(); err == nil {
		t.Error("ReadRoot: want error when ProgramArguments has no -root")
	}
}

func TestReadRootBadHex(t *testing.T) {
	path := placeholderPlist(t)
	docJSON := fmt.Sprintf(`{"Label":%q,"ProgramArguments":["bin","anchor","-root","not-hex"]}`, Label)
	r := PlistAnchorReader{Converter: fakePlistToJSON{json: []byte(docJSON)}, Path: path}

	if _, err := r.ReadRoot(); err == nil {
		t.Error("ReadRoot: want error for non-hex -root value")
	}
}

func TestReadRootConverterErrorIsNotConfusedWithNotInstalled(t *testing.T) {
	path := placeholderPlist(t)
	r := PlistAnchorReader{Converter: fakePlistToJSON{err: fmt.Errorf("plutil: command not found")}, Path: path}

	_, err := r.ReadRoot()
	if err == nil {
		t.Fatal("ReadRoot: want error when the converter fails")
	}
	if err == ErrAnchorNotInstalled {
		t.Error("a broken converter must not be reported as ErrAnchorNotInstalled — that would let an attacker blind verification by breaking plutil")
	}
}

func TestReadRootMalformedJSON(t *testing.T) {
	path := placeholderPlist(t)
	r := PlistAnchorReader{Converter: fakePlistToJSON{json: []byte("not json")}, Path: path}

	if _, err := r.ReadRoot(); err == nil {
		t.Error("ReadRoot: want error for malformed JSON")
	}
}

func TestPlistPathIncludesLabel(t *testing.T) {
	if !strings.Contains(PlistPath(), Label) {
		t.Errorf("PlistPath() = %q, want it to contain %q", PlistPath(), Label)
	}
	if !strings.HasSuffix(PlistPath(), ".plist") {
		t.Errorf("PlistPath() = %q, want .plist suffix", PlistPath())
	}
}
