package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

type process struct{ pid, pgid int }
type server struct {
	socket, shim      string
	allowGetTaskAllow bool
	procs             map[int]process
	mu                sync.Mutex
}

func main() {
	if len(os.Args) >= 4 && os.Args[1] == "--client" {
		client(os.Args[2], os.Args[3:])
		return
	}
	s := &server{socket: "/tmp/sandboxd.sock", procs: map[int]process{}}
	for i := 1; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--socket":
			if i+1 < len(os.Args) {
				s.socket = os.Args[i+1]
				i++
			}
		case "--shim":
			if i+1 < len(os.Args) {
				s.shim = os.Args[i+1]
				i++
			}
		case "--allow-get-task-allow":
			s.allowGetTaskAllow = true
		}
	}
	_ = os.Remove(s.socket)
	if err := os.MkdirAll(filepath.Dir(s.socket), 0700); err != nil {
		panic(err)
	}
	l, err := net.Listen("unix", s.socket)
	if err != nil {
		panic(err)
	}
	defer os.Remove(s.socket)
	defer l.Close()
	_ = os.Chmod(s.socket, 0600)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		s.mu.Lock()
		for _, p := range s.procs {
			killGroup(p)
		}
		s.mu.Unlock()
		os.Exit(0)
	}()
	for {
		c, err := l.Accept()
		if err == nil {
			go s.handle(c)
		}
	}
}
func client(socket string, args []string) {
	c, err := net.Dial("unix", socket)
	if err != nil {
		os.Exit(1)
	}
	defer c.Close()
	fmt.Fprintln(c, strings.Join(args, " "))
	_, _ = io.Copy(os.Stdout, c)
}
func killGroup(p process) {
	if p.pgid > 0 {
		_ = syscall.Kill(-p.pgid, syscall.SIGKILL)
	}
	_ = syscall.Kill(p.pid, syscall.SIGKILL)
}
func (s *server) handle(c net.Conn) {
	defer c.Close()
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		return
	}
	line = strings.TrimSuffix(line, "\n")
	if strings.HasPrefix(line, "REWRITE ") {
		p, err := s.rewrite(strings.TrimPrefix(line, "REWRITE "))
		if err != nil {
			fmt.Fprintf(c, "ERR %s\n", err)
			return
		}
		fmt.Fprintf(c, "OK %s\n", p)
		return
	}
	f := strings.Fields(line)
	if len(f) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch strings.ToUpper(f[0]) {
	case "PING":
		fmt.Fprintln(c, "OK")
	case "REGISTER":
		if len(f) >= 2 {
			pid, _ := strconv.Atoi(f[1])
			pgid := pid
			if len(f) > 2 {
				pgid, _ = strconv.Atoi(f[2])
			}
			s.procs[pid] = process{pid, pgid}
			fmt.Fprintln(c, "OK")
		}
	case "KILL":
		if len(f) > 1 {
			pid, _ := strconv.Atoi(f[1])
			if p, ok := s.procs[pid]; ok {
				killGroup(p)
				delete(s.procs, pid)
			}
			fmt.Fprintln(c, "OK")
		}
	case "KILLALL":
		for pid, p := range s.procs {
			killGroup(p)
			delete(s.procs, pid)
		}
		fmt.Fprintln(c, "OK")
	case "LIST":
		for _, p := range s.procs {
			fmt.Fprintf(c, "%d %d\n", p.pid, p.pgid)
		}
	}
}

func (s *server) rewrite(src string) (string, error) {
	if s.shim == "" {
		return "", fmt.Errorf("daemon has no --shim")
	}
	in, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	shim, err := os.ReadFile(s.shim)
	if err != nil {
		return "", err
	}
	entitlements, err := readOriginalEntitlements(src)
	if err != nil {
		// Invalid, unsigned, or unparsable source signatures contribute no
		// entitlements. The rewritten binary still receives the hardened runtime.
		entitlements = originalEntitlements{}
	}
	h := sha256.New()
	h.Write(in)
	h.Write(shim)
	h.Write([]byte(fmt.Sprintf("jit=%t;unsigned=%t;task=%t", entitlements.jit, entitlements.unsigned, entitlements.task && s.allowGetTaskAllow)))
	name := hex.EncodeToString(h.Sum(nil))
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cache, "sandbox", "binaries")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	out := filepath.Join(dir, name)
	if st, e := os.Stat(out); e == nil && st.Mode()&0111 != 0 {
		return out, nil
	}
	if err = rewriteMachO(in, out, s.shim); err != nil {
		return "", err
	}
	_ = os.Chmod(out, 0755)
	if _, e := os.Stat("/usr/bin/codesign"); e == nil {
		if err := signHardened(out, dir, entitlements, s.allowGetTaskAllow); err != nil {
			return "", err
		}
	}
	return out, nil
}

type originalEntitlements struct{ jit, unsigned, task bool }

func readOriginalEntitlements(path string) (originalEntitlements, error) {
	var result originalEntitlements
	if err := exec.Command("codesign", "--verify", "--strict", path).Run(); err != nil {
		return result, err
	}
	cmd := exec.Command("codesign", "-d", "--entitlements", ":-", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return result, err
	}
	dec := xml.NewDecoder(strings.NewReader(string(out)))
	var key string
	for {
		tok, e := dec.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return result, e
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "key" {
				var v string
				if e := dec.DecodeElement(&v, &t); e != nil {
					return result, e
				}
				key = v
			}
			if (t.Name.Local == "true" || t.Name.Local == "false") && key != "" {
				value := t.Name.Local == "true"
				switch key {
				case "com.apple.security.cs.allow-jit":
					result.jit = value
				case "com.apple.security.cs.allow-unsigned-executable-memory":
					result.unsigned = value
				case "com.apple.security.get-task-allow":
					result.task = value
				}
				key = ""
			}
		}
	}
	return result, nil
}

func signHardened(path, cacheDir string, original originalEntitlements, allowTask bool) error {
	// --options runtime remains mandatory. Only explicitly valid source
	// entitlements are copied; get-task-allow additionally requires a daemon flag.
	entitlements := filepath.Join(cacheDir, "entitlements.plist")
	task := original.task && allowTask
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>com.apple.security.cs.allow-jit</key><%s/>
<key>com.apple.security.cs.allow-unsigned-executable-memory</key><%s/>
<key>com.apple.security.get-task-allow</key><%s/>
</dict></plist>
`, boolTag(original.jit), boolTag(original.unsigned), boolTag(task))
	if err := os.WriteFile(entitlements, []byte(plist), 0600); err != nil {
		return err
	}
	if err := exec.Command("codesign", "--force", "--sign", "-", "--timestamp=none", "--options", "runtime", "--entitlements", entitlements, path).Run(); err != nil {
		return fmt.Errorf("hardened codesign: %w", err)
	}
	return nil
}
func boolTag(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// rewriteMachO adds LC_LOAD_DYLIB into existing Mach-O header padding. It never
// shifts segments or modifies offsets, and refuses binaries without safe padding.
func rewriteMachO(in []byte, out, name string) error {
	if len(in) < 32 || binary.LittleEndian.Uint32(in) != 0xfeedfacf {
		return fmt.Errorf("unsupported Mach-O: expected thin 64-bit binary")
	}
	ncmd := binary.LittleEndian.Uint32(in[16:20])
	sizeof := binary.LittleEndian.Uint32(in[20:24])
	old := uint64(32) + uint64(sizeof)
	if old > uint64(len(in)) {
		return fmt.Errorf("invalid load commands")
	}
	var first uint64
	off := uint64(32)
	for i := uint32(0); i < ncmd; i++ {
		if off+8 > uint64(len(in)) {
			return fmt.Errorf("invalid load command")
		}
		cmdsz := uint64(binary.LittleEndian.Uint32(in[off+4:]))
		if cmdsz < 8 || off+cmdsz > uint64(len(in)) {
			return fmt.Errorf("invalid load command size")
		}
		if binary.LittleEndian.Uint32(in[off:]) == 0x19 && cmdsz >= 48 && first == 0 {
			first = binary.LittleEndian.Uint64(in[off+40:])
		}
		off += cmdsz
	}
	if first == 0 || first < old {
		return fmt.Errorf("no safe load-command padding")
	}
	n := uint64(len(name) + 1)
	cmdsz := (24 + n + 7) &^ 7
	if first-old < cmdsz {
		return fmt.Errorf("no Mach-O load-command padding")
	}
	buf := append([]byte(nil), in...)
	for j := old; j < old+cmdsz; j++ {
		buf[j] = 0
	}
	binary.LittleEndian.PutUint32(buf[old:], 0xc)
	binary.LittleEndian.PutUint32(buf[old+4:], uint32(cmdsz))
	binary.LittleEndian.PutUint32(buf[old+8:], 24)
	binary.LittleEndian.PutUint32(buf[old+12:], 2)
	binary.LittleEndian.PutUint32(buf[old+16:], 0x10000)
	binary.LittleEndian.PutUint32(buf[old+20:], 0x10000)
	copy(buf[old+24:], name)
	binary.LittleEndian.PutUint32(buf[16:], ncmd+1)
	binary.LittleEndian.PutUint32(buf[20:], sizeof+uint32(cmdsz))
	return os.WriteFile(out, buf, 0755)
}
